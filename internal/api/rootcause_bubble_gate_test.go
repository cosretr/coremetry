package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1119 (inceleme düzeltmesi) — kopuk bubbleUp işine süreç geneli tavan.
// Sabitlenen: (1) aynı anda en çok rootCauseBubbleSlots tarama, (2) yuva
// beklerken iptal edilen istek tarama BAŞLATMAZ ve önbelleğe bir şey yazmaz,
// (3) tam demet yuva alamazsa bubbleUp'sız döner ama gövde önbelleğe girmez,
// (4) bubbleup ucunda ret hatası önbelleğe girmez. Servis adları sentetik.

// withFreshBubbleGate — testin kendi yuva havuzu + bekleme süresi.
func withFreshBubbleGate(t *testing.T, wait time.Duration) {
	t.Helper()
	origSem, origWait := rootCauseBubbleSem, rootCauseBubbleWait
	rootCauseBubbleSem = make(chan struct{}, rootCauseBubbleSlots)
	rootCauseBubbleWait = wait
	t.Cleanup(func() { rootCauseBubbleSem, rootCauseBubbleWait = origSem, origWait })
}

// fillBubbleGate — tüm yuvaları dışarıdan işgal eder; dönen işlev boşaltır.
func fillBubbleGate() func() {
	for i := 0; i < rootCauseBubbleSlots; i++ {
		rootCauseBubbleSem <- struct{}{}
	}
	return func() {
		for i := 0; i < rootCauseBubbleSlots; i++ {
			<-rootCauseBubbleSem
		}
	}
}

// gateStore — her kimlik kendi problemi (ayrı önbellek anahtarı, ayrı
// singleflight yuvası); ServiceBubbleUp eşzamanlılığı ölçülür.
type gateStore struct {
	fakeRootCauseStore
	cur, peak atomic.Int32
	hold      chan struct{}
}

func (g *gateStore) GetProblem(_ context.Context, id string) (*chstore.Problem, error) {
	return &chstore.Problem{ID: id, Service: "svc-checkout", Metric: "error_rate",
		StartedAt: time.Now().Add(-90 * time.Minute).UnixNano()}, nil
}

func (g *gateStore) ServiceBubbleUp(ctx context.Context, svc string, errSub bool, a, b time.Time) (*chstore.BubbleUpResult, error) {
	n := g.cur.Add(1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	<-g.hold
	g.cur.Add(-1)
	return g.fakeRootCauseStore.ServiceBubbleUp(ctx, svc, errSub, a, b)
}

func TestRootCauseBubbleGateCapsConcurrentScans(t *testing.T) {
	withFreshBubbleGate(t, 5*time.Second)
	g := &gateStore{hold: make(chan struct{})}
	g.bubble = sampleBubble()
	orig := rootCauseStoreOf
	t.Cleanup(func() { rootCauseStoreOf = orig })
	rootCauseStoreOf = func(*Server) rootCauseStore { return g }
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(32), stats: newCacheStats()}

	const n = 7
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("p-cap-%d", i)
			codes[i] = callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/x/rootcause/bubbleup", id, context.Background()).Code
		}(i)
	}
	time.Sleep(150 * time.Millisecond)
	if p := g.peak.Load(); p != rootCauseBubbleSlots {
		t.Fatalf("doygunlukta eşzamanlı tarama %d, beklenen tavan %d", p, rootCauseBubbleSlots)
	}
	close(g.hold)
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("istek %d: %d", i, c)
		}
	}
	if p := g.peak.Load(); p > rootCauseBubbleSlots {
		t.Fatalf("tavan aşıldı: %d", p)
	}
	if got := atomic.LoadInt32(&g.bubbleCalls); got != n {
		t.Fatalf("her farklı problem bir kez taranmalı: %d", got)
	}
}

func TestRootCauseBubbleGateCancelledWaiterDoesNotScan(t *testing.T) {
	withFreshBubbleGate(t, 5*time.Second)
	st := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "p-wait", Service: "svc-checkout", Metric: "error_rate", StartedAt: time.Now().Add(-90 * time.Minute).UnixNano()},
		bubble:  sampleBubble(),
	}
	s := newRootCauseTestServer(t, st)
	free := fillBubbleGate()
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	before := rootCauseBubbleGateStats.cancelled.Load()
	w := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-wait/rootcause/bubbleup", "p-wait", ctx)
	free()
	if w.Code == 200 {
		t.Fatalf("iptal edilen bekleyen 200 dönmemeli")
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 0 {
		t.Fatalf("iptal edilen bekleyen tarama başlattı: %d", n)
	}
	if rootCauseBubbleGateStats.cancelled.Load() <= before {
		t.Error("iptal sayacı artmadı")
	}
	// Önbelleğe bir şey yazılmadı: sonraki istek gerçekten tarar.
	if w2 := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-wait/rootcause/bubbleup", "p-wait", context.Background()); w2.Code != 200 || atomic.LoadInt32(&st.bubbleCalls) != 1 {
		t.Fatalf("sonraki istek: code=%d çağrı=%d", w2.Code, st.bubbleCalls)
	}
}

func TestRootCauseBubbleGateRejectionNotCached(t *testing.T) {
	withFreshBubbleGate(t, 40*time.Millisecond)
	start := time.Now().Add(-3 * time.Hour).UnixNano()
	resolved := start + int64(20*time.Minute)
	st := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "p-rej", Service: "svc-checkout", Metric: "error_rate", StartedAt: start, ResolvedAt: &resolved},
		bubble:  sampleBubble(),
	}
	s := newRootCauseTestServer(t, st)

	free := fillBubbleGate()
	before := rootCauseBubbleGateStats.rejected.Load()
	full := callRootCause(s, (*Server).getProblemRootCause, "/api/problems/p-rej/rootcause", "p-rej", context.Background())
	bub := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-rej/rootcause/bubbleup", "p-rej", context.Background())
	free()
	if rootCauseBubbleGateStats.rejected.Load() < before+2 {
		t.Error("ret sayacı artmadı")
	}
	// Tam demet: eski davranış — bubbleUp'sız 200, ama önbelleğe yazılmaz.
	if full.Code != 200 || full.Header().Get("X-Cache") != "MISS-NOSTORE" {
		t.Fatalf("tam demet: code=%d X-Cache=%q", full.Code, full.Header().Get("X-Cache"))
	}
	var fm map[string]any
	_ = json.Unmarshal(full.Body.Bytes(), &fm)
	if _, has := fm["bubbleUp"]; has || fm["blastRadius"] == nil {
		t.Fatalf("ret yolunda gövde: %v", fm)
	}
	// bubbleup ucu: hata, önbelleğe yazılmaz.
	if bub.Code == 200 {
		t.Fatalf("bubbleup ucu ret yolunda 200 döndü")
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 0 {
		t.Fatalf("ret yolunda tarama koştu: %d", n)
	}

	// Yuvalar boşalınca ikisi de gerçekten hesaplanır (kısmi gövde L1'de değil).
	full2 := callRootCause(s, (*Server).getProblemRootCause, "/api/problems/p-rej/rootcause", "p-rej", context.Background())
	var fm2 map[string]any
	_ = json.Unmarshal(full2.Body.Bytes(), &fm2)
	if full2.Code != 200 || fm2["bubbleUp"] == nil || full2.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("kısmi tam demet önbelleğe girmiş: code=%d X-Cache=%q", full2.Code, full2.Header().Get("X-Cache"))
	}
	if b2 := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-rej/rootcause/bubbleup", "p-rej", context.Background()); b2.Code != 200 {
		t.Fatalf("bubbleup ret hatası önbelleğe girmiş: %d", b2.Code)
	}
}

// uncacheable gövde SWR tazelemesinde bayat girdiyi EZMEZ ve L1'e yazılmaz.
func TestUncacheableBodyIsNeverStored(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(4), stats: newCacheStats()}
	calls := 0
	fn := func(context.Context) (any, error) {
		calls++
		return uncacheable(map[string]int{"n": calls}), nil
	}
	for i := 1; i <= 2; i++ {
		w := httptest.NewRecorder()
		s.serveCached(w, httptest.NewRequest("GET", "/x", nil), "k-nostore", time.Minute, fn)
		if w.Header().Get("X-Cache") != "MISS-NOSTORE" || w.Body.String() != fmt.Sprintf(`{"n":%d}`, i) {
			t.Fatalf("istek %d: X-Cache=%q body=%s", i, w.Header().Get("X-Cache"), w.Body.String())
		}
	}
	if _, ok := s.l1.get("k-nostore"); ok {
		t.Fatal("uncacheable gövde L1'e yazılmış")
	}
}
