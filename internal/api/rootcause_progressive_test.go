package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1119 regresyon testi — /rootcause soğuk yol (operatör: ilk açılış
// 30–45 sn). Panel artık /rootcause/core + /rootcause/bubbleup okur. Bu dosya
// (1) iki parçanın birleşiminin tam /rootcause demetiyle ALAN ALAN aynı
// olduğunu, (2) çekirdeğin bubbleUp taramasını hiç koşmadığını, (3) bubbleUp
// hatasının önbelleğe yazılmadığını, (4) eşzamanlı isteklerin TEK hesap
// paylaştığını, (5) hesabın istek iptalinden kopuk koştuğunu sabitler.
// Servis adları sentetik.

type fakeRootCauseStore struct {
	mu        sync.Mutex
	problem   *chstore.Problem
	event     *chstore.AnomalyEvent
	bubble    *chstore.BubbleUpResult
	bubbleErr error
	// bubbleGate — doluysa ServiceBubbleUp kapı kapanana dek bekler.
	bubbleGate  chan struct{}
	bubbleCalls int32
	// bubbleCtxErr — ServiceBubbleUp çağrıldığı andaki ctx.Err().
	bubbleCtxErr     error
	bubbleHasDeadine bool
	bubbleArgs       []any
}

func (f *fakeRootCauseStore) GetProblem(context.Context, string) (*chstore.Problem, error) {
	return f.problem, nil
}
func (f *fakeRootCauseStore) GetAnomalyEvent(context.Context, string, time.Duration) (*chstore.AnomalyEvent, error) {
	return f.event, nil
}
func (f *fakeRootCauseStore) EnrichProblemsWithDeploys(_ context.Context, ps []chstore.Problem, _ time.Duration) []chstore.Problem {
	out := append([]chstore.Problem(nil), ps...)
	out[0].RecentDeploy = &chstore.RecentDeploy{Version: "v2.4.1", AgeSeconds: 420}
	return out
}
func (f *fakeRootCauseStore) EnrichAnomaliesWithDeploys(_ context.Context, es []chstore.AnomalyEvent, _ time.Duration) []chstore.AnomalyEvent {
	out := append([]chstore.AnomalyEvent(nil), es...)
	out[0].RecentDeploy = &chstore.RecentDeploy{Version: "v2.4.1", AgeSeconds: 120}
	return out
}
func (f *fakeRootCauseStore) GetCorrelatedChangesMVTop(context.Context, time.Time, int, int, int) ([]chstore.ChangedService, error) {
	return []chstore.ChangedService{
		{Service: "svc-orders", Score: 72, Reasons: []string{"error rate 0.1% → 4.2% ↑"}},
		{Service: "svc-inventory", Score: 31, Reasons: []string{"p99 120ms → 480ms (300%) ↑"}},
	}, nil
}
func (f *fakeRootCauseStore) GetServiceGraphTopN(context.Context, string, time.Duration, time.Time, time.Time, int) ([]chstore.ServiceEdge, error) {
	return []chstore.ServiceEdge{{Source: "svc-checkout", Target: "svc-orders", CallCount: 900}}, nil
}
func (f *fakeRootCauseStore) GetServiceBlastRadius(_ context.Context, svc string, _, _ time.Time) (chstore.BlastRadius, error) {
	return chstore.BlastRadius{Service: svc, TotalCallers: 1, Callers: []chstore.BlastRadiusCaller{{Service: "svc-gateway", Calls: 50}}}, nil
}
func (f *fakeRootCauseStore) FindExemplar(_ context.Context, req chstore.ExemplarReq) (*chstore.Exemplar, error) {
	return &chstore.Exemplar{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", Service: req.Service, Name: "POST /orders"}, nil
}
func (f *fakeRootCauseStore) GetHypothesis(context.Context, string, string) (*chstore.RootCauseHypothesis, error) {
	return &chstore.RootCauseHypothesis{Service: "svc-checkout", TopSuspect: "svc-orders", Confidence: 0.7}, nil
}
func (f *fakeRootCauseStore) ServiceBubbleUp(ctx context.Context, service string, errorSubset bool, started, end time.Time) (*chstore.BubbleUpResult, error) {
	atomic.AddInt32(&f.bubbleCalls, 1)
	f.mu.Lock()
	f.bubbleCtxErr = ctx.Err()
	_, f.bubbleHasDeadine = ctx.Deadline()
	f.bubbleArgs = []any{service, errorSubset, started.UnixNano(), end.UnixNano()}
	gate, res, err := f.bubbleGate, f.bubble, f.bubbleErr
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	return res, err
}

func newRootCauseTestServer(t *testing.T, st *fakeRootCauseStore) *Server {
	t.Helper()
	orig := rootCauseStoreOf
	t.Cleanup(func() { rootCauseStoreOf = orig })
	rootCauseStoreOf = func(*Server) rootCauseStore { return st }
	return &Server{cache: &fakeCache{}, l1: newL1Cache(32), stats: newCacheStats()}
}

func callRootCause(s *Server, h func(*Server, http.ResponseWriter, *http.Request), path, id string, ctx context.Context) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	r.SetPathValue("id", id)
	h(s, w, r)
	return w
}

func sampleBubble() *chstore.BubbleUpResult {
	return &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{{
		Key: "k8s.pod.name", Values: []chstore.BubbleUpValue{{Value: "svc-checkout-7d9f-abcde", SelectionCount: 30, BaselineCount: 90,
			SelectionPct: 0.75, BaselinePct: 0.1, Score: 0.65}},
	}}}
}

func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("json: %v (%s)", err, b)
	}
	return m
}

func TestRootCauseProgressivePartsEqualFullBundle(t *testing.T) {
	base := time.Now().Add(-3 * time.Hour).UnixNano()
	resolved := base + int64(25*time.Minute)
	for _, tc := range []struct {
		name string
		p    chstore.Problem
	}{
		// Çözülmüş: pencere [started, resolved] (sabit).
		{"çözülmüş hata problemi", chstore.Problem{ID: "p-1", Service: "svc-checkout", Metric: "error_rate", StartedAt: base, ResolvedAt: &resolved}},
		// Açık ama 3 sa önce başlamış: pencere 1 sa tavanına sıkışır (sabit).
		{"açık gecikme problemi", chstore.Problem{ID: "p-2", Service: "svc-checkout", Metric: "p99_ms", StartedAt: base}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			st := &fakeRootCauseStore{problem: &p, bubble: sampleBubble()}
			s := newRootCauseTestServer(t, st)
			ctx := context.Background()

			full := callRootCause(s, (*Server).getProblemRootCause, "/api/problems/x/rootcause", p.ID, ctx)
			core := callRootCause(s, (*Server).getProblemRootCauseCore, "/api/problems/x/rootcause/core", p.ID, ctx)
			if full.Code != 200 || core.Code != 200 {
				t.Fatalf("full=%d core=%d", full.Code, core.Code)
			}
			if n := atomic.LoadInt32(&st.bubbleCalls); n != 1 {
				t.Fatalf("çekirdek bubbleUp taramasını koşmamalı: çağrı %d (beklenen yalnız tam demetten 1)", n)
			}
			fullArgs := st.bubbleArgs
			bub := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/x/rootcause/bubbleup", p.ID, ctx)
			if bub.Code != 200 {
				t.Fatalf("bubbleup=%d %s", bub.Code, bub.Body.String())
			}
			if !reflect.DeepEqual(fullArgs, st.bubbleArgs) {
				t.Fatalf("bubbleUp girdileri ayrıştı: tam=%v parça=%v", fullArgs, st.bubbleArgs)
			}

			fm, cm, bm := decodeMap(t, full.Body.Bytes()), decodeMap(t, core.Body.Bytes()), decodeMap(t, bub.Body.Bytes())
			if fm["bubbleUp"] == nil {
				t.Fatal("tam demet bubbleUp taşımalı")
			}
			if _, has := cm["bubbleUp"]; has {
				t.Fatal("çekirdek bubbleUp taşımamalı")
			}
			if !reflect.DeepEqual(fm["bubbleUp"], bm["bubbleUp"]) {
				t.Fatalf("bubbleUp ayrıştı:\ntam=%v\nparça=%v", fm["bubbleUp"], bm["bubbleUp"])
			}
			if fm["fromNs"] != bm["fromNs"] || fm["toNs"] != bm["toNs"] {
				t.Fatalf("pencere ayrıştı: tam=%v..%v parça=%v..%v", fm["fromNs"], fm["toNs"], bm["fromNs"], bm["toNs"])
			}
			merged := map[string]any{}
			for k, v := range cm {
				merged[k] = v
			}
			merged["bubbleUp"] = bm["bubbleUp"]
			if !reflect.DeepEqual(fm, merged) {
				t.Fatalf("çekirdek + bubbleup ≠ tam demet:\ntam=%v\nbirleşik=%v", fm, merged)
			}
		})
	}
}

func TestAnomalyRootCauseCoreEqualsFullMinusBubble(t *testing.T) {
	start := time.Now().Add(-2 * time.Hour).UnixNano()
	ev := &chstore.AnomalyEvent{ID: "a-1", Kind: "trace_op", Service: "svc-orders", Pattern: "POST /orders",
		StartedAt: start, LastSeen: start + int64(20*time.Minute)}
	st := &fakeRootCauseStore{event: ev, bubble: sampleBubble()}
	s := newRootCauseTestServer(t, st)
	full := callRootCause(s, (*Server).getAnomalyRootCause, "/api/anomalies/a-1/rootcause", "a-1", context.Background())
	core := callRootCause(s, (*Server).getAnomalyRootCauseCore, "/api/anomalies/a-1/rootcause/core", "a-1", context.Background())
	if full.Code != 200 || core.Code != 200 {
		t.Fatalf("full=%d core=%d", full.Code, core.Code)
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 1 {
		t.Fatalf("çekirdek bubbleUp koşmamalı: %d", n)
	}
	fm, cm := decodeMap(t, full.Body.Bytes()), decodeMap(t, core.Body.Bytes())
	delete(fm, "bubbleUp")
	if !reflect.DeepEqual(fm, cm) {
		t.Fatalf("çekirdek ≠ tam − bubbleUp:\ntam=%v\nçekirdek=%v", fm, cm)
	}
}

func TestRootCauseBubbleUpErrorIsNotCached(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute).UnixNano()
	st := &fakeRootCauseStore{
		problem:   &chstore.Problem{ID: "p-err", Service: "svc-checkout", Metric: "error_rate", StartedAt: start},
		bubbleErr: errors.New("code: 159, timeout exceeded"),
	}
	s := newRootCauseTestServer(t, st)
	call := func() *httptest.ResponseRecorder {
		return callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-err/rootcause/bubbleup", "p-err", context.Background())
	}
	if w := call(); w.Code != 500 {
		t.Fatalf("hata 500 dönmeli: %d %s", w.Code, w.Body.String())
	}
	st.mu.Lock()
	st.bubbleErr, st.bubble = nil, sampleBubble()
	st.mu.Unlock()
	if w := call(); w.Code != 200 {
		t.Fatalf("hata önbelleğe yazılmış: %d %s", w.Code, w.Body.String())
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 2 {
		t.Fatalf("ikinci istek yeniden hesaplamalı: çağrı %d", n)
	}
	if w := call(); w.Code != 200 || atomic.LoadInt32(&st.bubbleCalls) != 2 {
		t.Fatalf("başarılı sonuç önbellekten gelmeli: code=%d çağrı=%d", w.Code, st.bubbleCalls)
	}
}

func TestRootCauseBubbleUpSingleFlight(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute).UnixNano()
	gate := make(chan struct{})
	st := &fakeRootCauseStore{
		problem:    &chstore.Problem{ID: "p-sf", Service: "svc-checkout", Metric: "p99_ms", StartedAt: start},
		bubble:     sampleBubble(),
		bubbleGate: gate,
	}
	s := newRootCauseTestServer(t, st)
	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-sf/rootcause/bubbleup", "p-sf", context.Background()).Code
		}(i)
	}
	// Lider kapıda beklerken diğerleri singleflight'a katılır; kapı
	// açıldıktan sonra gelen (varsa) L1'den okur — her iki durumda tek hesap.
	time.Sleep(100 * time.Millisecond)
	close(gate)
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("istek %d: %d", i, c)
		}
	}
	if got := atomic.LoadInt32(&st.bubbleCalls); got != 1 {
		t.Fatalf("eşzamanlı istekler tek hesap paylaşmalı: %d hesap", got)
	}
}

func TestRootCauseComputeDetachedFromRequestCancel(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute).UnixNano()
	st := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "p-cx", Service: "svc-checkout", Metric: "error_rate", StartedAt: start},
		bubble:  sampleBubble(),
	}
	s := newRootCauseTestServer(t, st)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // istemci çekmeceyi kapattı
	w := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-cx/rootcause/bubbleup", "p-cx", ctx)
	if w.Code != 200 {
		t.Fatalf("iptal edilmiş istekte hesap yine tamamlanmalı: %d", w.Code)
	}
	st.mu.Lock()
	ctxErr, hasDeadline := st.bubbleCtxErr, st.bubbleHasDeadine
	st.mu.Unlock()
	if ctxErr != nil {
		t.Fatalf("hesap context'i istek iptalini miras almış: %v", ctxErr)
	}
	if !hasDeadline {
		t.Fatal("kopuk hesap bütçesiz kalmamalı (deadline yok)")
	}
	if w2 := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/api/problems/p-cx/rootcause/bubbleup", "p-cx", context.Background()); w2.Code != 200 || atomic.LoadInt32(&st.bubbleCalls) != 1 {
		t.Fatalf("iptal edilen istemcinin hesabı önbelleğe girmeli: code=%d çağrı=%d", w2.Code, st.bubbleCalls)
	}
}

func TestRootCausePartKeysCarryIdentityOnly(t *testing.T) {
	res := int64(42)
	if got, want := rootCausePartKey("core", "p-1", 7, &res), "core:rootcause:p-1:7:42"; got != want {
		t.Fatalf("anahtar %q, beklenen %q", got, want)
	}
	if rootCausePartKey("core", "p-1", 7, nil) == rootCausePartKey("bubbleup", "p-1", 7, nil) {
		t.Fatal("parça anahtarları çakışıyor")
	}
	if rootCausePartKey("core", "p-1", 7, nil) == rootcauseCacheKey("p-1", 7, nil) {
		t.Fatal("çekirdek anahtarı tam demetle çakışıyor — bubbleUp'sız gövde tam demet diye sunulur")
	}
}

func TestRootCauseProgressiveRoutesRegistered(t *testing.T) {
	s := &Server{cache: &fakeCache{}, l1: newL1Cache(4), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerRootCauseProgressiveRoutes(mux)
	for _, path := range []string{
		"/api/problems/p-1/rootcause/core",
		"/api/problems/p-1/rootcause/bubbleup",
		"/api/anomalies/a-1/rootcause/core",
	} {
		r := httptest.NewRequest("GET", path, nil)
		if _, pat := mux.Handler(r); pat == "" {
			t.Errorf("%s kayıtlı değil", path)
		}
	}
	if _, ok := extraRouteRegistrars["rootcause-progressive"]; !ok {
		t.Error("defter kaydı yok (registerRoutesExtra)")
	}
}
