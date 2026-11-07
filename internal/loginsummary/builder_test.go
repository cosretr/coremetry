package loginsummary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── sahteler: gerçeğin kurallarını uygular ──────────────────────────────

// fakeVM — yalnız Queries(family)'nin ÜÇ sabit ifadesini kabul eder; bilinmeyen
// ifadeyi hata ile reddeder (gerçek VM'in "kötü sorgu" davranışı).
type fakeVM struct {
	mu         sync.Mutex
	configured bool
	fail       map[string]bool // "q1" | "q2" → hata
	calls      map[string]int
	windows    []time.Duration
	unknown    []string
}

func newFakeVM() *fakeVM {
	return &fakeVM{configured: true, fail: map[string]bool{}, calls: map[string]int{}}
}
func (f *fakeVM) Configured() bool { return f.configured }
func (f *fakeVM) QueryPromQLRange(ctx context.Context, q string, from, to time.Time, step, maxPts int) ([]chstore.SpanMetricSeries, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, has := ctx.Deadline(); !has {
		return nil, errors.New("fakeVM: bağlamda zaman aşımı yok")
	}
	qs := Queries(HTTPFamily)
	var name string
	switch q {
	case qs.HTTPRate:
		name = "q1"
	case qs.HTTPSum:
		name = "q2"
	case qs.GRPCRate:
		name = "grpc"
	case qs.KafkaRate:
		name = "kafka"
	default:
		f.unknown = append(f.unknown, q)
		return nil, fmt.Errorf("fakeVM: bilinmeyen ifade %q", q)
	}
	f.calls[name]++
	f.windows = append(f.windows, to.Sub(from))
	if step != 300 {
		return nil, fmt.Errorf("fakeVM: adım 300 olmalı, got %d", step)
	}
	if f.fail[name] {
		return nil, errors.New("fakeVM: " + name + " arızası")
	}
	mk := func(key []string, base float64) chstore.SpanMetricSeries {
		s := chstore.SpanMetricSeries{GroupKey: key}
		for t := from.Unix() - from.Unix()%300; t <= to.Unix(); t += 300 {
			s.Points = append(s.Points, chstore.SpanMetricPoint{Time: t * 1e9, Value: base})
		}
		return s
	}
	switch name {
	case "q1":
		// Gerçek istemci (vmetrics.labelSetGroupKey): gruplamasız `sum(...)`
		// sonucunda etiket yoktur → GroupKey nil, tek seri.
		return []chstore.SpanMetricSeries{mk(nil, 51)}, nil
	case "q2":
		return []chstore.SpanMetricSeries{mk(nil, 25.5)}, nil
	case "grpc":
		return []chstore.SpanMetricSeries{mk(nil, 7)}, nil
	default: // kafka
		return []chstore.SpanMetricSeries{mk(nil, 40)}, nil
	}
}

type fakeStore struct {
	countsErr, cosreErr, actErr, bytesErr, topoErr error
	counts                                         chstore.LoginCounts
	cosre                                          uint64
	act                                            chstore.SelfActivityTotals
	bytes                                          uint64
	edges                                          []chstore.ServiceTopologyEdge
	topoWindow                                     time.Duration
	topoLimit                                      int
	topoCalls, countsCalls, endpointCalls          int
	endpoints                                      uint64
	endpointsErr                                   error
	dbNames                                        map[string]string
	dbNamesErr                                     error
	dbNameCalls                                    int
	dbNameKeys                                     []string
	vols                                           map[string]uint64
	volsErr                                        error
}

func (f *fakeStore) CountLoginEndpoints(ctx context.Context) (uint64, error) {
	f.endpointCalls++
	return f.endpoints, f.endpointsErr
}

func (f *fakeStore) LoginDBNames(ctx context.Context, since time.Time, keys []string) (map[string]string, error) {
	f.dbNameCalls++
	f.dbNameKeys = keys
	return f.dbNames, f.dbNamesErr
}

func (f *fakeStore) LoginServiceVolumes(ctx context.Context, since time.Time) (map[string]uint64, error) {
	return f.vols, f.volsErr
}

func (f *fakeStore) LoginSummaryCounts(ctx context.Context) (chstore.LoginCounts, error) {
	f.countsCalls++
	// gerçek Store gibi: kısmi sayılar + ilk hata birlikte döner.
	return f.counts, f.countsErr
}
func (f *fakeStore) CountCosreQuestions(ctx context.Context, since time.Time) (uint64, error) {
	return f.cosre, f.cosreErr
}
func (f *fakeStore) ReadSelfActivityTotals(ctx context.Context, from, to time.Time) (chstore.SelfActivityTotals, error) {
	return f.act, f.actErr
}
func (f *fakeStore) TotalUncompressedBytes(ctx context.Context) (uint64, error) {
	return f.bytes, f.bytesErr
}
func (f *fakeStore) ReadServiceTopologyAgg(ctx context.Context, from, to time.Time, limit int) ([]chstore.ServiceTopologyEdge, error) {
	f.topoCalls++
	f.topoWindow, f.topoLimit = to.Sub(from), limit
	return f.edges, f.topoErr
}

// fakeRedis — Publish/Load'u gerçek gibi JSON gidiş-dönüşüyle taşır (Redis'e
// serileştirilebilir olmayan bir anlık görüntü yayınlanamaz).
type fakeRedis struct {
	blob []byte
	n    int
}

func (r *fakeRedis) publish(t *testing.T) func(*Snapshot) {
	return func(s *Snapshot) {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("anlık görüntü serileştirilemiyor: %v", err)
		}
		r.blob, r.n = b, r.n+1
	}
}
func (r *fakeRedis) load() *Snapshot {
	if r.blob == nil {
		return nil
	}
	var s Snapshot
	if err := json.Unmarshal(r.blob, &s); err != nil {
		return nil
	}
	return &s
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type harness struct {
	st    *fakeStore
	vm    *fakeVM
	redis *fakeRedis
	clk   *clock
	logs  []string
	b     *Builder
}

func newHarness(t *testing.T) *harness {
	h := &harness{
		st:    &fakeStore{edges: fixtureEdges(), bytes: 1 << 40, cosre: 5, act: chstore.SelfActivityTotals{Logins: 12}, endpoints: 420},
		vm:    newFakeVM(),
		redis: &fakeRedis{},
		clk:   &clock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)},
	}
	h.st.counts = chstore.LoginCounts{Spans: u64(5000), Traces: u64(900), Services: u64(5)}
	h.b = New(Deps{
		Store: h.st, VM: h.vm, Now: h.clk.now, Load: h.redis.load, Publish: h.redis.publish(t),
		Logf: func(f string, a ...any) { h.logs = append(h.logs, fmt.Sprintf(f, a...)) },
	})
	return h
}

func (h *harness) step() *Snapshot {
	h.b.Step(context.Background())
	return h.redis.load()
}

func (h *harness) advance(d time.Duration) { h.clk.t = h.clk.t.Add(d) }

// ── testler ─────────────────────────────────────────────────────────────

func TestBuilderFirstStepPublishesFullSnapshot(t *testing.T) {
	h := newHarness(t)
	snap := h.step()
	if snap == nil {
		t.Fatal("yayın yok")
	}
	if snap.V != SnapshotVersion || snap.GeneratedAt != h.clk.t.UnixMilli() || snap.Salt == "" {
		t.Errorf("başlık: %+v", snap)
	}
	if snap.SpansToday == nil || *snap.SpansToday != 5000 || *snap.Services != 5 {
		t.Errorf("sayılar: %+v", snap)
	}
	if snap.Activity == nil || snap.Activity.Logins != 12 || snap.Activity.CosreQuestions != 5 {
		t.Errorf("aktivite: %+v", snap.Activity)
	}
	if snap.Endpoints == nil || *snap.Endpoints != 420 {
		t.Errorf("endpoint sayısı: %v", snap.Endpoints)
	}
	if snap.CHBytes == nil || *snap.CHBytes != 1<<40 || snap.Topo == nil || snap.Ring == nil {
		t.Errorf("yavaş tur çıktıları: bytes=%v topo=%v ring=%v", snap.CHBytes, snap.Topo != nil, snap.Ring != nil)
	}
	if snap.State != StateOK {
		t.Errorf("durum ok beklenir: %s", snap.State)
	}
	if h.st.topoWindow != 15*time.Minute || h.st.topoLimit != 5000 {
		t.Errorf("topoloji penceresi 15 dk / limit 5000: %v %d", h.st.topoWindow, h.st.topoLimit)
	}
	if len(h.vm.unknown) != 0 {
		t.Errorf("VM'e yalnız üç sabit ifade gider: %v", h.vm.unknown)
	}
	resp := Project(snap, Flags{Enabled: true, Now: h.clk.t})
	if !resp.Ready || resp.HTTP24h == nil {
		t.Fatalf("yayınlanan anlık görüntü Project'ten hazır geçmeli: %+v", resp)
	}
	if len(h.logs) != 1 || !strings.HasPrefix(h.logs[0], "[login-summary] state=ok") || !strings.Contains(h.logs[0], "vm_q1=") || !strings.Contains(h.logs[0], "counts=") {
		t.Errorf("süre günlüğü: %v", h.logs)
	}
}

// v0.10.1922 — p95 (süzgeçsiz histogram_quantile) hiç sorgulanmaz: sahte VM
// bilinmeyen ifadeyi reddeder; kurucu yalnız Q1 ve Q2 gönderir.
func TestBuilderNeverQueriesPercentiles(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		h.step()
		h.advance(6 * time.Minute)
	}
	if len(h.vm.unknown) != 0 {
		t.Fatalf("beklenmeyen VM ifadesi: %v", h.vm.unknown)
	}
	qs := Queries(HTTPFamily)
	for _, q := range []string{qs.HTTPRate, qs.HTTPSum, qs.GRPCRate, qs.KafkaRate} {
		if strings.Contains(q, "histogram_quantile") || strings.Contains(q, "_bucket") {
			t.Fatalf("yüzdelik ifadesi kalmamalı: %s", q)
		}
	}
}

// Tuz kararlıdır: sonraki turlarda ve lider değişiminde (yeni Builder + Load) aynı.
func TestBuilderSaltStableAcrossRoundsAndLeaderChange(t *testing.T) {
	h := newHarness(t)
	s1 := h.step()
	h.advance(6 * time.Minute)
	s2 := h.step()
	if s1.Salt != s2.Salt {
		t.Fatal("tuz turlar arasında değişmemeli")
	}
	// yeni lider: aynı Redis'ten devralır
	b2 := New(Deps{Store: h.st, VM: h.vm, Now: h.clk.now, Load: h.redis.load, Publish: h.redis.publish(t), Logf: func(string, ...any) {}})
	h.advance(time.Minute)
	b2.Step(context.Background())
	s3 := h.redis.load()
	if s3.Salt != s1.Salt {
		t.Fatal("lider değişiminde tuz devralınmalı")
	}
	ids := func(s *Snapshot) string {
		var out []string
		for _, n := range s.Topo.Nodes {
			out = append(out, n.ID)
		}
		return strings.Join(out, ",")
	}
	if ids(s1) != ids(s3) {
		t.Error("düğüm id'leri lider değişiminde sabit kalmalı")
	}
	// halka da devralınır: yeni lider 24 saati sıfırdan kurmaz
	if len(s3.Ring.B) < len(s1.Ring.B) {
		t.Errorf("halka devralınmalı: %d < %d", len(s3.Ring.B), len(s1.Ring.B))
	}
	// Reset sonrası aynı Builder yeniden yükler
	h.b.Reset()
	h.advance(time.Minute)
	if s := h.step(); s.Salt != s1.Salt {
		t.Error("Reset sonrası tuz Redis'ten gelir")
	}
}

func TestBuilderGapFillBoundedAndFinite(t *testing.T) {
	h := newHarness(t)
	h.step()
	// ilk tur: 1 yakın pencere + 4 boşluk parçası, her biri Q1+Q2
	if h.vm.calls["q1"] != 5 || h.vm.calls["q2"] != 5 {
		t.Errorf("ilk tur 5 pencere (1+4): q1=%d q2=%d", h.vm.calls["q1"], h.vm.calls["q2"])
	}
	rounds := 1
	for i := 0; i < 10; i++ {
		h.advance(5 * time.Minute)
		before := h.vm.calls["q1"]
		h.step()
		rounds++
		if got := h.vm.calls["q1"] - before; got > 5 {
			t.Errorf("tur başına en çok 1+4 pencere, got %d", got)
		}
	}
	// 24 parçanın hepsi ~6 turda dolar; sonra yalnız yakın pencere
	before := h.vm.calls["q1"]
	h.advance(5 * time.Minute)
	h.step()
	if got := h.vm.calls["q1"] - before; got != 1 {
		t.Errorf("soğuk başlangıç bitince tur başına 1 pencere, got %d", got)
	}
}

func TestBuilderSlowRoundCadence(t *testing.T) {
	h := newHarness(t)
	h.step()
	topo, vmq := h.st.topoCalls, h.vm.calls["q1"]
	h.advance(time.Minute)
	h.step()
	h.advance(time.Minute)
	h.step()
	if h.st.topoCalls != topo || h.vm.calls["q1"] != vmq {
		t.Error("5 dk dolmadan VM/topoloji turu yok")
	}
	if h.st.countsCalls != 3 {
		t.Errorf("CH sayıları her tur: %d", h.st.countsCalls)
	}
	h.advance(4 * time.Minute)
	h.step()
	if h.st.topoCalls != topo+1 {
		t.Error("5 dk dolunca yavaş tur")
	}
}

func TestBuilderBreakerSkipsAfterThreeFailuresFor30Min(t *testing.T) {
	h := newHarness(t)
	h.vm.fail["q1"] = true
	for i := 0; i < 3; i++ {
		snap := h.step()
		if snap.State != StateDegraded {
			t.Fatalf("Q1 düşünce durum degraded: %s", snap.State)
		}
		h.advance(5 * time.Minute)
	}
	if h.vm.calls["q1"] != 3 {
		t.Fatalf("3 deneme: %d", h.vm.calls["q1"])
	}
	// 3. hatadan sonra 30 dk atlanır
	for i := 0; i < 4; i++ { // 20 dk
		h.step()
		h.advance(5 * time.Minute)
	}
	if h.vm.calls["q1"] != 3 {
		t.Errorf("30 dk atlanmalı, VM çağrısı arttı: %d", h.vm.calls["q1"])
	}
	// 30 dk geçince yeniden denenir (hâlâ bozuk → 1 deneme daha)
	h.advance(15 * time.Minute)
	h.step()
	if h.vm.calls["q1"] != 4 {
		t.Errorf("30 dk sonra yeniden denenmeli: %d", h.vm.calls["q1"])
	}
	// iyileşince durum ok
	h.vm.fail["q1"] = false
	h.advance(31 * time.Minute)
	if s := h.step(); s.State != StateOK {
		t.Errorf("VM iyileşince ok: %s", s.State)
	}
}

func TestBuilderBreakerNeedsConsecutiveFailures(t *testing.T) {
	h := newHarness(t)
	h.vm.fail["q1"] = true
	h.step()
	h.advance(5 * time.Minute)
	h.step()
	h.vm.fail["q1"] = false
	h.advance(5 * time.Minute)
	h.step() // başarı sayacı sıfırlar
	h.vm.fail["q1"] = true
	h.advance(5 * time.Minute)
	h.step()
	h.advance(5 * time.Minute)
	before := h.vm.calls["q1"]
	h.step()
	if h.vm.calls["q1"] == before {
		t.Error("araya giren başarı sayacı sıfırlar: art arda 3 olmadığı için atlanmamalı")
	}
}

func TestBuilderVMDisabledHidesHTTPAndKeepsOK(t *testing.T) {
	h := newHarness(t)
	h.vm.configured = false
	snap := h.step()
	if snap.Ring != nil {
		t.Error("VM kapalıyken halka yok")
	}
	if snap.State != StateOK {
		t.Errorf("VM kapalı + CH ok → ok: %s", snap.State)
	}
	if h.vm.calls["q1"] != 0 {
		t.Error("VM kapalıyken sorgu yok")
	}
	if resp := Project(snap, Flags{Enabled: true, Now: h.clk.t}); resp.HTTP24h != nil {
		t.Error("kart gizli")
	}
	// nil VM de çalışır
	h2 := newHarness(t)
	h2.b = New(Deps{Store: h2.st, Now: h2.clk.now, Publish: h2.redis.publish(t), Logf: func(string, ...any) {}})
	if s := h2.step(); s.Ring != nil || s.State != StateOK {
		t.Errorf("nil VM: %+v", s)
	}
}

func TestBuilderFamilyAbsentHidesCardButNotDegraded(t *testing.T) {
	h := newHarness(t)
	empty := &emptyVM{fakeVM: h.vm}
	h.b = New(Deps{Store: h.st, VM: empty, Now: h.clk.now, Publish: h.redis.publish(t), Logf: func(string, ...any) {}})
	snap := h.step()
	if snap.State != StateOK {
		t.Errorf("aile yok ≠ arıza: %s", snap.State)
	}
	if resp := Project(snap, Flags{Enabled: true, Now: h.clk.t}); resp.HTTP24h != nil {
		t.Error("Q1 boşsa http_24h yok")
	}
	if h.vm.calls["q2"] != 0 {
		t.Error("Q1 boşken Q2 sorgulanmaz")
	}
}

type emptyVM struct{ *fakeVM }

func (e *emptyVM) QueryPromQLRange(ctx context.Context, q string, from, to time.Time, step, maxPts int) ([]chstore.SpanMetricSeries, error) {
	e.fakeVM.mu.Lock()
	e.fakeVM.calls["q1"]++
	e.fakeVM.mu.Unlock()
	return nil, nil
}

func TestBuilderCHFailuresDegradeAndDropFields(t *testing.T) {
	h := newHarness(t)
	h.st.countsErr = errors.New("ch: timeout")
	h.st.counts = chstore.LoginCounts{}
	snap := h.step()
	if snap.State != StateDegraded || snap.SpansToday != nil {
		t.Errorf("sayılar düşünce degraded ve alan yok: %+v", snap)
	}
	// kısmi: yalnız servis sorgusu düşer
	h2 := newHarness(t)
	h2.st.countsErr = errors.New("kısmi")
	h2.st.counts = chstore.LoginCounts{Spans: u64(1), Traces: u64(2)}
	s2 := h2.step()
	if s2.SpansToday == nil || s2.Services != nil || s2.State != StateDegraded {
		t.Errorf("kısmi sayılar korunur: %+v", s2)
	}
}

func TestBuilderActivityNeedsBothSources(t *testing.T) {
	h := newHarness(t)
	h.st.cosreErr = errors.New("cosre")
	if s := h.step(); s.Activity != nil {
		t.Error("cosre okunamadıysa aktivite yok")
	}
	h2 := newHarness(t)
	h2.st.actErr = errors.New("act")
	if s := h2.step(); s.Activity != nil {
		t.Error("sayaç okunamadıysa aktivite yok")
	}
}

// Birikimli yapılar (topoloji, boyut) hata turunda son iyi hâlini korur.
func TestBuilderSlowItemsKeepLastGoodOnFailure(t *testing.T) {
	h := newHarness(t)
	s1 := h.step()
	h.st.topoErr, h.st.bytesErr = errors.New("topo"), errors.New("bytes")
	h.advance(6 * time.Minute)
	s2 := h.step()
	if s2.Topo == nil || s2.CHBytes == nil || *s2.CHBytes != *s1.CHBytes {
		t.Errorf("son iyi hâl korunmalı: %+v", s2)
	}
}

func TestBuilderPublishedSnapshotIsIsolatedFromWorkingState(t *testing.T) {
	h := newHarness(t)
	var pubs []*Snapshot
	h.b = New(Deps{Store: h.st, VM: h.vm, Now: h.clk.now, Logf: func(string, ...any) {},
		Publish: func(s *Snapshot) { pubs = append(pubs, s) }})
	h.b.Step(context.Background())
	first := pubs[0]
	n := len(first.Ring.B)
	h.advance(6 * time.Minute)
	h.b.Step(context.Background())
	if len(first.Ring.B) != n {
		t.Error("yayınlanmış anlık görüntü sonraki turdan etkilenmemeli (uç eşzamanlı okur)")
	}
}

func TestBuilderRunStopsOnContextAndResetsWhenNotLeader(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	leader := false
	done := make(chan struct{})
	h.b.d.FastEvery = 10 * time.Millisecond
	go func() { h.b.Run(ctx, func() bool { return leader }); close(done) }()
	time.Sleep(40 * time.Millisecond)
	if h.redis.n != 0 {
		t.Error("lider değilken yayın yok")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run ctx ile durmalı")
	}
}

// v0.10.1915 — endpoint sayısı yalnız yavaş turda okunur (hızlı turlarda son
// değer anlık görüntüde durur); okuma düşerse alan cevaptan düşer.
func TestBuilderEndpointsReadOnSlowRoundOnly(t *testing.T) {
	h := newHarness(t)
	s1 := h.step()
	h.advance(time.Minute)
	s2 := h.step()
	if h.st.endpointCalls != 1 {
		t.Errorf("hızlı turda endpoint okunmaz: %d çağrı", h.st.endpointCalls)
	}
	for i, s := range []*Snapshot{s1, s2} {
		if s.Endpoints == nil || *s.Endpoints != 420 {
			t.Errorf("tur %d: endpoint sayısı taşınmalı: %v", i, s.Endpoints)
		}
	}
	h.st.endpointsErr = errors.New("ch")
	h.advance(5 * time.Minute)
	if s := h.step(); h.st.endpointCalls != 2 || s.Endpoints != nil {
		t.Errorf("yavaş turda okuma düşerse alan düşer: calls=%d %v", h.st.endpointCalls, s.Endpoints)
	}
}

func TestVMFromServiceNilIsUnconfigured(t *testing.T) {
	vm := VMFromService(nil)
	if vm.Configured() {
		t.Error("nil servis: yapılandırılmamış")
	}
	if _, err := vm.QueryPromQLRange(context.Background(), "x", time.Now(), time.Now(), 300, 0); err == nil {
		t.Error("nil servis sorgu hatası döner")
	}
}

// v0.10.1905 — özet ayardan kapalıyken kurucu arka uca HİÇ gitmez ve yayın
// yapmaz; yeniden açılınca ilk tur tam koşar.
func TestBuilderDisabledRunsNoBackendCalls(t *testing.T) {
	h := newHarness(t)
	on := false
	h.b = New(Deps{Store: h.st, VM: h.vm, Now: h.clk.now, Load: h.redis.load, Publish: h.redis.publish(t),
		Logf: func(string, ...any) {}, Enabled: func() bool { return on }, TopologyOn: func() bool { return on }})
	for i := 0; i < 3; i++ {
		h.b.Step(context.Background())
		h.advance(6 * time.Minute)
	}
	if h.st.countsCalls != 0 || h.st.topoCalls != 0 || len(h.vm.calls) != 0 || h.redis.n != 0 {
		t.Fatalf("panel ve topoloji kapalıyken çağrı/yayın olmamalı: counts=%d topo=%d vm=%v yayın=%d", h.st.countsCalls, h.st.topoCalls, h.vm.calls, h.redis.n)
	}
	on = true
	if snap := h.step(); snap == nil || snap.Topo == nil || h.vm.calls["q1"] == 0 {
		t.Fatalf("açılınca ilk tur tam koşmalı: %+v vm=%v", snap, h.vm.calls)
	}
}

// v0.10.1917 — panel kapalı, topoloji açık: tur YALNIZ topolojiyi okur
// (sayı, VM, endpoint, boyut yok) ve panel alanları anlık görüntüde tutulmaz.
func TestBuilderPanelOffTopologyOnReadsOnlyTopology(t *testing.T) {
	h := newHarness(t)
	panel := true
	h.b = New(Deps{Store: h.st, VM: h.vm, Now: h.clk.now, Load: h.redis.load, Publish: h.redis.publish(t),
		Logf: func(string, ...any) {}, Enabled: func() bool { return panel }, TopologyOn: func() bool { return true }})
	if s := h.step(); s.SpansToday == nil || s.Ring == nil {
		t.Fatal("panel açıkken sayılar ve halka dolu olmalı")
	}
	panel = false
	counts, vmq, eps, topo := h.st.countsCalls, h.vm.calls["q1"], h.st.endpointCalls, h.st.topoCalls
	h.advance(6 * time.Minute)
	s := h.step()
	if h.st.countsCalls != counts || h.vm.calls["q1"] != vmq || h.st.endpointCalls != eps {
		t.Errorf("panel kapalıyken sayı/VM/endpoint okunmaz: counts %d→%d vm %d→%d eps %d→%d",
			counts, h.st.countsCalls, vmq, h.vm.calls["q1"], eps, h.st.endpointCalls)
	}
	if h.st.topoCalls != topo+1 || s.Topo == nil {
		t.Errorf("topoloji okunmalı: %d→%d", topo, h.st.topoCalls)
	}
	if s.SpansToday != nil || s.Ring != nil || s.Activity != nil || s.Endpoints != nil || s.CHBytes != nil {
		t.Errorf("panel alanları anlık görüntüde tutulmamalı: %+v", s)
	}
	if s.State != StateOK {
		t.Errorf("yalnız topoloji turunda durum ok: %s", s.State)
	}
}

// v0.10.1905 — loginTopology=off: topoloji okunmaz ve anlık görüntüden düşer.
func TestBuilderTopologyOffSkipsRead(t *testing.T) {
	h := newHarness(t)
	topo := true
	h.b = New(Deps{Store: h.st, VM: h.vm, Now: h.clk.now, Load: h.redis.load, Publish: h.redis.publish(t),
		Logf: func(string, ...any) {}, TopologyOn: func() bool { return topo }})
	if snap := h.step(); snap == nil || snap.Topo == nil {
		t.Fatal("açıkken topoloji dolu olmalı")
	}
	topo = false
	calls := h.st.topoCalls
	h.advance(6 * time.Minute)
	snap := h.step()
	if h.st.topoCalls != calls {
		t.Errorf("off iken topoloji okunmamalı: %d → %d", calls, h.st.topoCalls)
	}
	if snap == nil || snap.Topo != nil {
		t.Errorf("off iken anlık görüntüde topoloji kalmamalı")
	}
}

// v0.10.1905 — günlük dakikada bir yazılmaz: yavaş tur + durum değişimi.
func TestBuilderLogsOnSlowRoundOrStateChange(t *testing.T) {
	h := newHarness(t)
	h.step() // ilk tur yavaş → 1 satır
	h.advance(time.Minute)
	h.step() // hızlı, durum aynı → satır yok
	if len(h.logs) != 1 {
		t.Fatalf("hızlı turda günlük yok: %v", h.logs)
	}
	h.st.countsErr = context.DeadlineExceeded
	h.advance(time.Minute)
	h.step() // durum degraded oldu → satır
	if len(h.logs) != 2 || !strings.Contains(h.logs[1], "state=degraded") {
		t.Fatalf("durum değişimi günlüğe yazılmalı: %v", h.logs)
	}
}

// v0.10.1923 (operatör: "tek grafikte Kafka, gRPC, HTTP throughput") — üç
// protokol de halkaya girer; gRPC / Kafka arızası pencereyi düşürmez, yalnız
// o çizgi eksik kalır.
func TestBuilderProtocolsAndPartialFailure(t *testing.T) {
	h := newHarness(t)
	snap := h.step()
	v, ok := snap.Ring.View()
	if !ok || v.Throughput == nil || v.GRPC == nil || v.Kafka == nil {
		t.Fatalf("üç protokol beklenir: %+v", v)
	}
	if v.Summary.GRPCNow == nil || *v.Summary.GRPCNow != 7 || v.Summary.KafkaNow == nil || *v.Summary.KafkaNow != 40 {
		t.Errorf("anlık değerler: %+v", v.Summary)
	}
	h2 := newHarness(t)
	h2.vm.fail["kafka"] = true
	h2.vm.fail["grpc"] = true
	s2 := h2.step()
	v2, ok := s2.Ring.View()
	if !ok || v2.Throughput == nil || v2.GRPC != nil || v2.Kafka != nil || s2.State != StateOK {
		t.Errorf("gRPC/Kafka arızası HTTP'yi ve durumu etkilememeli: %+v state=%s", v2, s2.State)
	}
}

// Kafka ifadesi konu başına serileri toplamaz (çift sayım); gRPC yeni/eski aile
// birlikte toplanmaz (`or`).
func TestQueriesAvoidDoubleCounting(t *testing.T) {
	qs := Queries(HTTPFamily)
	// v0.10.1925 (operatör-raporlu: login'de Kafka 0) — servis sayfalarıyla aynı
	// metrik: records_consumed_rate GÖSTERGESİ, rate() UYGULANMAZ; önce istemci
	// düzeyi seri.
	if !strings.HasPrefix(qs.KafkaRate, `sum(kafka_consumer_records_consumed_rate{topic=""})`) {
		t.Errorf("Kafka önce istemci düzeyi gösterge: %s", qs.KafkaRate)
	}
	if strings.Contains(qs.KafkaRate, "rate(kafka_consumer") || strings.Contains(qs.KafkaRate, "records_consumed_total") {
		t.Errorf("Kafka göstergesine rate() uygulanmaz, _total sayacı kullanılmaz: %s", qs.KafkaRate)
	}
	if !strings.Contains(qs.GRPCRate, " or ") || strings.Contains(qs.GRPCRate, " + ") {
		t.Errorf("gRPC aileleri `or` ile: %s", qs.GRPCRate)
	}
}

// v0.10.1931 — yavaş tur db adlarını okur ve db düğümüne uygular; hata
// topolojiyi düşürmez (sistem adında kalır).
func TestBuilderNamesDatabases(t *testing.T) {
	for _, fail := range []bool{false, true} {
		h := newHarness(t)
		h.st.dbNames = map[string]string{"oracle@db-host-1.acme.internal": "ORDERSDB1.ACME.LOCAL"}
		if fail {
			h.st.dbNamesErr = errors.New("boom")
		}
		snap := h.step()
		if snap == nil || snap.Topo == nil {
			t.Fatal("topoloji yayınlanmalı")
		}
		want := "ordersdb1"
		if fail {
			want = "oracle"
		}
		found := false
		for _, n := range snap.Topo.Nodes {
			if n.Tier == TierDB && n.Name == want {
				found = true
			}
		}
		if len(h.st.dbNameKeys) != 1 || h.st.dbNameKeys[0] != "oracle@db-host-1.acme.internal" {
			t.Errorf("yalnız seçili db anahtarları sorulmalı: %v", h.st.dbNameKeys)
		}
		if !found || h.st.dbNameCalls != 1 {
			t.Errorf("fail=%v: db adı %q beklenir (çağrı %d): %+v", fail, want, h.st.dbNameCalls, snap.Topo.Nodes)
		}
	}
}

// v0.10.1937 — siluet dışındaki servisler adsız yıldız olur; okuma hatası
// topolojiyi düşürmez (yıldızsız yayınlanır).
func TestBuilderAttachesStars(t *testing.T) {
	for _, fail := range []bool{false, true} {
		h := newHarness(t)
		h.st.vols = map[string]uint64{"orders-api": 900, "lonely-batch": 40, "coremetry-api": 5000}
		if fail {
			h.st.volsErr = errors.New("boom")
		}
		snap := h.step()
		if snap == nil || snap.Topo == nil {
			t.Fatal("topoloji yayınlanmalı")
		}
		if fail {
			if len(snap.Topo.Stars) != 0 {
				t.Errorf("hata: yıldız olmamalı: %+v", snap.Topo.Stars)
			}
			continue
		}
		// orders-api siluette (yıldız değil), coremetry-* hariç → yalnız lonely-batch
		if len(snap.Topo.Stars) != 1 || snap.Topo.Stars[0].Near != -1 {
			t.Errorf("tek adsız yıldız (komşusuz) beklenir: %+v", snap.Topo.Stars)
		}
	}
}
