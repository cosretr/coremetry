package rollout

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// v2worker_test.go — v0.10.982 — Rollouts v2 P2.2 canlı dedektör işçisi
// sözleşmesi (docs/rollouts/v2-audit.md §3.3, §4.10, §10.4, §10.6):
//
//   - Bayrak (enabled VE source=v2) kapalıyken ya da lider değilken tik
//     HİÇBİR şey yapmaz: sorgu yok, CH okuması yok, koşu satırı yok.
//   - İlk-ever koşu yalnız baseline (durum) yazar; nesil artışı + yeni RS →
//     START (progressing, imajdan change_type), sonraki tiklerde SUCCEEDED
//     aynı satırı yerinde günceller.
//   - Kesik/kısmi okuma hiçbir satır yazmaz, koşuyu partial işaretler,
//     belleği bozmaz.
//   - Bellek tikler arasında taşınır (her tik CH okumaz); lider edinimi,
//     liderlik kaybı ve yazım hatası belleği düşürür → durum CH'den kurulur.
//   - Tik başına TEK rollout_worker_runs satırı.

// ── Sahte KSM kümesi ──────────────────────────────────────────────────────

type fkDep struct {
	ns                           string
	gen, og                      uint64
	spec, status, updated, avail uint32
	rs                           map[string]uint32   // RS → spec
	images                       map[string][]string // RS → imajlar
}

type fakeKSM struct {
	mu        sync.Mutex
	deps      map[string]*fkDep
	sampleAt  time.Time
	partial   map[string]bool
	truncated map[string]bool
	fail      map[string]error
	calls     int
	exprs     []string
	ats       []time.Time // her sorgunun değerlendirme zamanı
}

func newFakeKSM() *fakeKSM {
	return &fakeKSM{deps: map[string]*fkDep{}, partial: map[string]bool{}, truncated: map[string]bool{}, fail: map[string]error{}}
}

func (f *fakeKSM) Query(ctx context.Context, clusterID, expr string, at time.Time) (V2QueryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.exprs = append(f.exprs, expr)
	f.ats = append(f.ats, at)
	if err := ctx.Err(); err != nil {
		return V2QueryResult{}, err
	}
	name := ""
	for _, q := range V2TickQueries([]string{V2KindDeployment, V2KindStatefulSet, V2KindDaemonSet}, "") {
		if q.Expr == expr {
			name = q.Name
		}
	}
	if name == "" && strings.Contains(expr, "kube_pod_container_info") {
		name = "images"
	}
	if err := f.fail[name]; err != nil {
		return V2QueryResult{}, err
	}
	var out []V2Sample
	names := make([]string, 0, len(f.deps))
	for n := range f.deps {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := f.deps[n]
		lab := func(kv ...string) map[string]string {
			m := map[string]string{"namespace": d.ns}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i]] = kv[i+1]
			}
			return m
		}
		switch name {
		case V2QDeployGauges:
			for suffix, v := range map[string]float64{"metadata_generation": float64(d.gen), "status_observed_generation": float64(d.og),
				"spec_replicas": float64(d.spec), "status_replicas": float64(d.status), "status_replicas_updated": float64(d.updated),
				"status_replicas_available": float64(d.avail), "spec_paused": 0} {
				out = append(out, V2Sample{Labels: lab("__name__", "kube_deployment_"+suffix, "deployment", n), Value: v})
			}
		case V2QDeploySample:
			out = append(out, V2Sample{Labels: lab("deployment", n), Value: float64(f.sampleAt.UnixMilli()) / 1000})
		case V2QDeployProgressing:
			out = append(out, V2Sample{Labels: lab("deployment", n), Value: 0})
		case V2QRSOwner:
			for rs := range d.rs {
				out = append(out, V2Sample{Labels: lab("replicaset", rs, "owner_name", n), Value: 1})
			}
		case V2QRSActive:
			for rs, spec := range d.rs {
				if spec > 0 {
					out = append(out, V2Sample{Labels: lab("replicaset", rs), Value: float64(spec)})
				}
			}
		case V2QRSSpecCount:
			if len(d.rs) > 0 {
				out = append(out, V2Sample{Labels: map[string]string{}, Value: float64(len(d.rs))})
			}
		case "images":
			for rs, imgs := range d.images {
				for _, im := range imgs {
					out = append(out, V2Sample{Labels: lab("owner_name", rs, "image", im), Value: 1})
				}
			}
		}
	}
	r := V2QueryResult{Samples: out, Series: len(out), TotalSeries: len(out)}
	if f.partial[name] {
		r.Partial = true
	}
	if f.truncated[name] {
		r.Truncated, r.TotalSeries = true, r.Series+10
	}
	return r, nil
}

// ── Sahte CH deposu (RMT: aynı anahtarda büyük version kazanır) ──────────

type fvEvKey struct {
	k   V2Key
	inc int64
	gen uint64
}

type fakeV2Store struct {
	mu                       sync.Mutex
	states                   map[V2Key]V2WorkloadState
	events                   map[fvEvKey]V2Event
	runs                     []WorkerRun
	stateReads, eventReads   int
	eventWrites, stateWrites int
	failRead, failEvents     error
	failStates               error
}

func newFakeV2Store() *fakeV2Store {
	return &fakeV2Store{states: map[V2Key]V2WorkloadState{}, events: map[fvEvKey]V2Event{}}
}

func (s *fakeV2Store) RolloutV2States(ctx context.Context, clusterID string) ([]V2WorkloadState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stateReads++
	if s.failRead != nil {
		return nil, s.failRead
	}
	var out []V2WorkloadState
	for k, st := range s.states {
		if k.ClusterID == clusterID {
			out = append(out, st)
		}
	}
	v2SortStates(out)
	return out, nil
}

func (s *fakeV2Store) RolloutV2LatestEvents(ctx context.Context, clusterID string) ([]V2Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.eventReads++
	var all []V2Event
	for k, e := range s.events {
		if k.k.ClusterID == clusterID {
			all = append(all, e)
		}
	}
	return v2LatestPerIncarnation(all), nil
}

func (s *fakeV2Store) RolloutV2WriteEvents(ctx context.Context, rows []V2Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failEvents != nil {
		return s.failEvents
	}
	for _, e := range rows {
		if err := ValidateV2Event(e); err != nil {
			return err
		}
		k := fvEvKey{e.Key(), e.IncarnationAt.UnixMilli(), e.Generation}
		if cur, ok := s.events[k]; !ok || e.Version >= cur.Version {
			s.events[k] = e
		}
	}
	s.eventWrites++
	return nil
}

func (s *fakeV2Store) RolloutV2WriteStates(ctx context.Context, rows []V2WorkloadState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failStates != nil {
		return s.failStates
	}
	for _, st := range rows {
		if err := ValidateV2State(st); err != nil {
			return err
		}
		if cur, ok := s.states[st.Key()]; !ok || st.Version >= cur.Version {
			s.states[st.Key()] = st
		}
	}
	s.stateWrites++
	return nil
}

func (s *fakeV2Store) RecordRolloutWorkerRun(ctx context.Context, run WorkerRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ValidateWorkerRun(run); err != nil {
		return err
	}
	s.runs = append(s.runs, run)
	return nil
}

func (s *fakeV2Store) eventList() []V2Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]V2Event, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, e)
	}
	v2SortEvents(out)
	return out
}

func (s *fakeV2Store) lastRun(t *testing.T) WorkerRun {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		t.Fatal("koşu satırı yok")
	}
	return s.runs[len(s.runs)-1]
}

type fakeV2Clusters []V2ClusterRef

func (c fakeV2Clusters) V2Clusters() []V2ClusterRef { return c }

// ── Koşum ─────────────────────────────────────────────────────────────────

type v2Harness struct {
	t        *testing.T
	ksm      *fakeKSM
	store    *fakeV2Store
	det      *V2Detector
	clock    time.Time
	settings Settings
	leader   bool
}

func newV2Harness(t *testing.T, clusters ...V2ClusterRef) *v2Harness {
	if len(clusters) == 0 {
		clusters = []V2ClusterRef{{ID: "c-a", Name: "prod-a"}}
	}
	h := &v2Harness{t: t, ksm: newFakeKSM(), store: newFakeV2Store(), clock: detT0, leader: true}
	h.settings = DefaultSettings()
	h.settings.Enabled, h.settings.Source = true, SourceV2
	h.settings.Kinds = []string{V2KindDeployment} // STS/DS sorguları sahte kümede boş döner; sayımlar Deployment'a göre
	h.det = NewV2Detector(h.store, h.ksm, fakeV2Clusters(clusters), func() Settings { return h.settings })
	h.det.now = func() time.Time { return h.clock }
	h.det.SetLeaderCheck(func() bool { return h.leader })
	h.det.host = "pod-1"
	return h
}

// tick — saat 30 s ilerler; KSM örneği tik zamanından 5 s eski.
func (h *v2Harness) tick() bool {
	h.clock = h.clock.Add(30 * time.Second)
	h.ksm.sampleAt = h.clock.Add(-5 * time.Second)
	return h.det.Tick(context.Background())
}

func (h *v2Harness) dep(name string, gen, og uint64, spec, status, updated, avail uint32, rs map[string]uint32, images map[string][]string) {
	h.ksm.deps[name] = &fkDep{ns: "pay", gen: gen, og: og, spec: spec, status: status, updated: updated, avail: avail, rs: rs, images: images}
}

func TestV2DetectorGateOff(t *testing.T) {
	for name, mut := range map[string]func(h *v2Harness){
		"bayrak kapalı": func(h *v2Harness) { h.settings.Enabled = false },
		"source v1":     func(h *v2Harness) { h.settings.Source = SourceV1 },
		"source boş":    func(h *v2Harness) { h.settings.Source = "" },
		"lider değil":   func(h *v2Harness) { h.leader = false },
	} {
		t.Run(name, func(t *testing.T) {
			h := newV2Harness(t)
			h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
			mut(h)
			if h.tick() {
				t.Fatal("kapalı kapıda tik koşmamalı")
			}
			if h.ksm.calls != 0 || h.store.stateReads != 0 || len(h.store.runs) != 0 || h.store.stateWrites != 0 {
				t.Fatalf("kapalı kapıda iş yapıldı: sorgu %d, okuma %d, koşu %d, yazım %d",
					h.ksm.calls, h.store.stateReads, len(h.store.runs), h.store.stateWrites)
			}
		})
	}
	if V2DetectorActive(DefaultSettings()) {
		t.Fatal("varsayılan ayar dedektörü AÇMAMALI (source=v1, enabled=false)")
	}
}

func TestV2DetectorBootstrapThenStartThenSucceeded(t *testing.T) {
	h := newV2Harness(t)
	// Tik 1 — ilk-ever: yalnız baseline.
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, map[string][]string{"api-a": {"reg/api:1"}})
	if !h.tick() {
		t.Fatal("tik koşmalı")
	}
	if n := len(h.store.eventList()); n != 0 {
		t.Fatalf("ilk-ever koşu olay yazmamalı: %d", n)
	}
	if len(h.store.states) != 1 {
		t.Fatalf("baseline durumu yazılmalı: %d", len(h.store.states))
	}
	run := h.store.lastRun(t)
	if run.Worker != WorkerRolloutDetector || run.Status != RunOK || run.ScopesTotal != 1 || run.ScopesOK != 1 ||
		run.RowsWritten != 1 || run.APICalls != 6 || run.Host != "pod-1" || run.SeriesRead == 0 {
		t.Fatalf("bootstrap koşu satırı: %+v", run)
	}
	if !strings.Contains(run.Error, "baseline=1") || !strings.Contains(run.Error, "state_rebuilt=1") {
		t.Fatalf("teşhis özeti error kolonunda olmalı: %q", run.Error)
	}

	// Tik 2 — nesil artışı + yeni RS api-b, rolling: START.
	h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2},
		map[string][]string{"api-a": {"reg/api:1"}, "api-b": {"reg/api:2"}})
	h.tick()
	evs := h.store.eventList()
	if len(evs) != 1 {
		t.Fatalf("START olayı bekleniyordu: %+v", evs)
	}
	e := evs[0]
	if e.Status != V2StatusProgressing || e.ChangeType != V2ChangeRollout || e.Generation != 2 ||
		e.NewRevision != "api-b" || e.OldRevision != "api-a" ||
		!reflect.DeepEqual(e.Images, []string{"reg/api:2"}) || !reflect.DeepEqual(e.PrevImages, []string{"reg/api:1"}) {
		t.Fatalf("START satırı: %+v", e)
	}
	if want := h.ksm.sampleAt.Truncate(time.Millisecond); !e.StartedAt.Equal(want) {
		t.Fatalf("started_at KSM örnek zamanı olmalı: %v != %v", e.StartedAt, want)
	}
	if run = h.store.lastRun(t); run.Status != RunOK || run.APICalls != 7 {
		t.Fatalf("START tiki: 6 KSM + 1 imaj sorgusu beklenir: %+v", run)
	}

	// Tik 3 — tamamlandı: aynı satır succeeded.
	h.dep("api", 2, 2, 3, 3, 3, 3, map[string]uint32{"api-a": 0, "api-b": 3}, map[string][]string{"api-b": {"reg/api:2"}})
	h.tick()
	evs = h.store.eventList()
	if len(evs) != 1 || evs[0].Status != V2StatusSucceeded || evs[0].SucceededAt.IsZero() || evs[0].ChangeType != V2ChangeRollout {
		t.Fatalf("SUCCEEDED aynı satırda olmalı: %+v", evs)
	}
	if evs[0].Version <= e.Version {
		t.Fatalf("yerinde güncelleme daha büyük version taşımalı: %d <= %d", evs[0].Version, e.Version)
	}
	// Bellek taşındı: üç tikte CH durumu bir kez okundu.
	if h.store.stateReads != 1 || h.store.eventReads != 1 {
		t.Fatalf("durum her tik okunmamalı: states %d events %d", h.store.stateReads, h.store.eventReads)
	}
	// Tik 4 — değişiklik yok: satır yazılmaz, koşu satırı yine yazılır.
	writes := h.store.eventWrites + h.store.stateWrites
	h.tick()
	if h.store.eventWrites+h.store.stateWrites != writes {
		t.Fatal("değişiklik yokken satır yazılmamalı")
	}
	if len(h.store.runs) != 4 {
		t.Fatalf("tik başına tek koşu satırı: %d", len(h.store.runs))
	}
}

func TestV2DetectorPartialAndTruncatedSkip(t *testing.T) {
	for _, mode := range []string{"partial", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			h := newV2Harness(t)
			h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
			h.tick() // bootstrap
			writes := h.store.eventWrites + h.store.stateWrites
			// Kısmi okumada RS listesi eksik: api-a görünmez, api-b yeni — fark
			// alınsaydı sahte START yazılırdı.
			h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-b": 2}, nil)
			if mode == "partial" {
				h.ksm.partial[V2QRSOwner] = true
			} else {
				h.ksm.truncated[V2QRSOwner] = true
			}
			h.tick()
			if h.store.eventWrites+h.store.stateWrites != writes {
				t.Fatal("kısmi/kesik okuma hiçbir satır yazmamalı")
			}
			run := h.store.lastRun(t)
			if run.Status != RunPartial || run.ScopesOK != 0 {
				t.Fatalf("koşu partial olmalı: %+v", run)
			}
			if mode == "partial" && (!run.PartialResponse || run.Truncated) {
				t.Fatalf("partial_response bayrağı: %+v", run)
			}
			if mode == "truncated" && (!run.Truncated || run.PartialResponse) {
				t.Fatalf("truncated bayrağı: %+v", run)
			}
			if !strings.Contains(run.Error, "prod-a: kesik/kısmi") || !strings.Contains(run.Error, "skipped_partial=1") {
				t.Fatalf("not + teşhis: %q", run.Error)
			}
			// Sonraki tam okuma normal devam eder (bellek bozulmadı).
			h.ksm.partial, h.ksm.truncated = map[string]bool{}, map[string]bool{}
			h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2}, nil)
			h.tick()
			if evs := h.store.eventList(); len(evs) != 1 || evs[0].NewRevision != "api-b" {
				t.Fatalf("tam okumada START: %+v", evs)
			}
			if h.store.stateReads != 1 {
				t.Fatalf("kısmi okuma belleği düşürmemeli: %d okuma", h.store.stateReads)
			}
		})
	}
}

func TestV2DetectorRebuildOnLeaderAcquire(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.tick() // bootstrap (pod-1)
	if h.store.stateReads != 1 {
		t.Fatalf("ilk tik CH'den kurar: %d", h.store.stateReads)
	}
	h.tick()
	if h.store.stateReads != 1 {
		t.Fatal("bellek varken yeniden okuma yok")
	}
	// Edinim: bellek düşer, sonraki tik CH'den kurar.
	h.det.OnAcquire()
	h.tick()
	if h.store.stateReads != 2 {
		t.Fatalf("OnAcquire sonrası CH'den yeniden kurulmalı: %d", h.store.stateReads)
	}
	// Liderlik kaybı → tik yok, bellek düşer; geri gelince yeniden kurulur.
	h.leader = false
	h.tick()
	h.leader = true
	h.tick()
	if h.store.stateReads != 3 {
		t.Fatalf("liderlik dönüşünde yeniden kurulmalı: %d", h.store.stateReads)
	}

	// Failover: başka pod (boş bellek) aynı depoda devralır — baseline'ı
	// CH'den okuduğu için nesil artışını ilk-ever sanmaz, START yazar.
	h2 := newV2Harness(t)
	h2.store, h2.clock = h.store, h.clock
	h2.det = NewV2Detector(h2.store, h2.ksm, fakeV2Clusters{{ID: "c-a", Name: "prod-a"}}, func() Settings { return h2.settings })
	h2.det.now = func() time.Time { return h2.clock }
	h2.det.host = "pod-2"
	h2.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2}, nil)
	h2.tick()
	evs := h2.store.eventList()
	if len(evs) != 1 || evs[0].ChangeType != V2ChangeRollout || evs[0].NewRevision != "api-b" {
		t.Fatalf("devralan lider START yazmalı (baseline değil): %+v", evs)
	}
	if run := h2.store.lastRun(t); run.Host != "pod-2" || !strings.Contains(run.Error, "state_rebuilt=1") {
		t.Fatalf("devralan koşu: %+v", run)
	}
}

func TestV2DetectorWriteFailureDropsMemory(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.tick()
	h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2}, nil)
	h.store.failEvents = errors.New("ch down")
	h.tick()
	run := h.store.lastRun(t)
	if run.Status != RunFailed || !strings.Contains(run.Error, "rollout_events yazılamadı: ch down") {
		t.Fatalf("yazım hatası koşuyu failed yapmalı: %+v", run)
	}
	h.store.failEvents = nil
	h.tick()
	if h.store.stateReads != 2 {
		t.Fatalf("yazım hatası belleği düşürmeli: %d okuma", h.store.stateReads)
	}
	if evs := h.store.eventList(); len(evs) != 1 || evs[0].Generation != 2 {
		t.Fatalf("yeniden kurulan durumdan START yeniden türemeli: %+v", evs)
	}
}

func TestV2DetectorLeadershipLostBeforeWrite(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	calls := 0
	h.det.SetLeaderCheck(func() bool { calls++; return calls == 1 }) // kapıda lider, yazımda değil
	h.tick()
	if h.store.stateWrites != 0 {
		t.Fatal("liderlik kaybında yazım olmamalı")
	}
	run := h.store.lastRun(t)
	if run.Status != RunPartial || !strings.Contains(run.Error, "liderlik tik sırasında kaybedildi") {
		t.Fatalf("koşu: %+v", run)
	}
}

func TestV2DetectorQueryAndReadErrors(t *testing.T) {
	h := newV2Harness(t, V2ClusterRef{ID: "c-a", Name: "prod-a"}, V2ClusterRef{ID: "c-b", Name: "prod-b"})
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.ksm.fail[V2QRSOwner] = fmt.Errorf("%w (cluster %q)", thanos.ErrWorkerTokenUnresolved, "prod")
	h.tick()
	run := h.store.lastRun(t)
	if run.Status != RunFailed || run.ScopesTotal != 2 || run.ScopesOK != 0 || h.store.stateWrites != 0 {
		t.Fatalf("iki kümede sorgu hatası: %+v", run)
	}
	if !strings.Contains(run.Error, "prod-a: KSM sorgusu rs_owner") || !strings.Contains(run.Error, "prod-b: KSM sorgusu rs_owner") {
		t.Fatalf("küme adlı not: %q", run.Error)
	}
	// Önceki durum okunamazsa KSM'ye hiç gidilmez.
	h2 := newV2Harness(t)
	h2.store.failRead = errors.New("timeout")
	h2.tick()
	if h2.ksm.calls != 0 {
		t.Fatalf("durum okunamadan sorgu gitmemeli: %d", h2.ksm.calls)
	}
	if run := h2.store.lastRun(t); run.Status != RunFailed || !strings.Contains(run.Error, "önceki durum okunamadı") {
		t.Fatalf("okuma hatası koşusu: %+v", run)
	}
}

func TestV2DetectorMixedClustersAndNoClusters(t *testing.T) {
	h := newV2Harness(t)
	h.det.clusters = fakeV2Clusters{}
	h.tick()
	if run := h.store.lastRun(t); run.Status != RunOK || run.ScopesTotal != 0 || !strings.Contains(run.Error, "hedef küme yok") {
		t.Fatalf("hedefsiz koşu: %+v", run)
	}
	h.det.clusters = fakeV2Clusters{{ID: "c-a", Name: "prod-a"}, {ID: "c-b", Name: "prod-b"}}
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.ksm.partial[V2QDeploySample] = true
	h.tick()
	if run := h.store.lastRun(t); run.Status != RunPartial || run.ScopesTotal != 2 {
		t.Fatalf("iki kümede kısmi: %+v", run)
	}
}

func TestV2RunStatus(t *testing.T) {
	for _, c := range []struct {
		total, ok, hard, soft int
		want                  string
	}{
		{0, 0, 0, 0, RunOK}, {2, 2, 0, 0, RunOK}, {2, 1, 1, 0, RunPartial}, {2, 0, 2, 0, RunFailed},
		{2, 0, 0, 2, RunPartial}, {3, 0, 2, 1, RunPartial},
	} {
		if got := v2RunStatus(c.total, c.ok, c.hard, c.soft); got != c.want {
			t.Errorf("%+v → %s, istenen %s", c, got, c.want)
		}
	}
}

func TestV2RunErrorText(t *testing.T) {
	notes := []string{"a", "b", "c", "d", "e", "f", "g"}
	got := v2RunErrorText(notes, map[string]int{"start": 2, "baseline": 1, "zero": 0})
	if got != "a | b | c | d | e | … (+2) || teşhis: baseline=1 start=2" {
		t.Fatalf("metin: %q", got)
	}
	long := v2RunErrorText([]string{strings.Repeat("ş", 5000)}, nil)
	if n := len([]rune(long)); n != v2RunErrorMax {
		t.Fatalf("rune tavanı: %d", n)
	}
	if v2RunErrorText(nil, nil) != "" {
		t.Fatal("boş girdi boş metin")
	}
}

func TestV2LatestPerIncarnation(t *testing.T) {
	k := V2Event{ClusterID: "c", Namespace: "n", WorkloadKind: V2KindDeployment, Workload: "w"}
	ev := func(inc time.Time, gen, ver uint64, status string) V2Event {
		e := k
		e.IncarnationAt, e.Generation, e.Version, e.Status = inc, gen, ver, status
		return e
	}
	inc2 := detT0.Add(time.Hour)
	got := v2LatestPerIncarnation([]V2Event{
		ev(detT0, 3, 10, V2StatusSucceeded), ev(detT0, 5, 11, V2StatusProgressing), ev(detT0, 5, 12, V2StatusSucceeded),
		ev(inc2, 1, 13, V2StatusProgressing),
	})
	if len(got) != 2 || got[0].Generation != 5 || got[0].Version != 12 || got[1].IncarnationAt != inc2 {
		t.Fatalf("incarnation başına son olay: %+v", got)
	}
}

func TestValidateWorkerRun(t *testing.T) {
	ok := WorkerRun{Worker: WorkerRolloutDetector, StartedAt: detT0, FinishedAt: detT0.Add(time.Second), Status: RunOK, Host: "p"}
	if err := ValidateWorkerRun(ok); err != nil {
		t.Fatalf("geçerli satır reddedildi: %v", err)
	}
	for name, mut := range map[string]func(r *WorkerRun){
		"sıfır started_at":        func(r *WorkerRun) { r.StartedAt = time.Time{} },
		"epoch started_at":        func(r *WorkerRun) { r.StartedAt = time.Unix(0, 0) },
		"bitiş başlangıçtan önce": func(r *WorkerRun) { r.FinishedAt = detT0.Add(-time.Second) },
		"bilinmeyen işçi":         func(r *WorkerRun) { r.Worker = "x" },
		"bilinmeyen durum":        func(r *WorkerRun) { r.Status = "done" },
		"boş host":                func(r *WorkerRun) { r.Host = " " },
	} {
		r := ok
		mut(&r)
		if ValidateWorkerRun(r) == nil {
			t.Errorf("%s reddedilmeliydi", name)
		}
	}
}

func TestV2ClusterRefsAndConsoleResult(t *testing.T) {
	cfgs := []thanos.ClusterConfig{
		{ID: "c-b", Name: "b", URL: "https://b", Enabled: true, NamespaceFilter: "pay|shop"},
		{ID: "c-a", Name: "a", URL: "https://a", Enabled: true},
		{ID: "c-hub", Name: "hub", URL: "https://h", Enabled: true}, // Argo hub'ı sıradan kayıt: DAHİL (karar 5)
		{ID: "c-off", Name: "off", URL: "https://o", Enabled: false},
		{ID: "c-nourl", Name: "nourl", Enabled: true},
		{ID: "c-a", Name: "a-dup", URL: "https://a2", Enabled: true},
	}
	got := v2ClusterRefs(cfgs)
	want := []V2ClusterRef{{ID: "c-a", Name: "a"}, {ID: "c-b", Name: "b", NSMatcher: `,namespace=~"pay|shop"`}, {ID: "c-hub", Name: "hub"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hedef kümeler %+v, istenen %+v", got, want)
	}
	res, err := v2ResultFromConsole(&thanos.ConsoleResult{ResultType: "vector",
		Result:       []byte(`[{"metric":{"namespace":"pay"},"value":[1,"2"]}]`),
		ConsoleNotes: thanos.ConsoleNotes{Warnings: []string{"store down"}}, Series: 1, TotalSeries: 3, Truncated: true})
	if err != nil || !res.Partial || !res.Truncated || res.TotalSeries != 3 || len(res.Samples) != 1 {
		t.Fatalf("dönüşüm: %+v %v", res, err)
	}
	if _, err := v2ResultFromConsole(&thanos.ConsoleResult{ResultType: "matrix", Result: []byte(`[]`)}); err == nil {
		t.Fatal("matrix reddedilmeli")
	}
}

func TestV2MassAbsence(t *testing.T) {
	const cid = "c-a"
	now := detT0
	var states []V2WorkloadState
	var obs []V2Observation
	base := map[V2Key]bool{}
	for i := 0; i < 30; i++ {
		name := fmt.Sprintf("w%02d", i)
		states = append(states, V2WorkloadState{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: name, LastSeenAt: now.Add(-time.Hour)})
		base[V2Key{cid, "pay", V2KindDeployment, name}] = true
		if i < 14 { // 14/30 görünür → yarıdan az
			obs = append(obs, V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: name})
		}
	}
	snap := V2Snapshot{Complete: true, Workloads: obs}
	if got := v2MassAbsence(snap, cid, base); !reflect.DeepEqual(got, []string{V2KindDeployment}) {
		t.Fatalf("toplu yokluk görülmeli: %v", got)
	}
	// Yarısı görünüyorsa değil.
	obs15 := append(append([]V2Observation(nil), obs...), V2Observation{Kind: V2KindDeployment, Namespace: "pay", Name: "w14"})
	if got := v2MassAbsence(V2Snapshot{Workloads: obs15}, cid, base); len(got) != 0 {
		t.Fatalf("yarısı görünürken tetiklenmemeli: %v", got)
	}
	// Tür ailesi büsbütün yoksa çekirdek korur (family_absent) — koruma devreye girmez.
	if got := v2MassAbsence(V2Snapshot{}, cid, base); len(got) != 0 {
		t.Fatalf("aile yokken: %v", got)
	}
	// İnceleme düzeltmesi: taban SON KABUL EDİLEN okumada görünenlerdir —
	// önceden silinmiş iş yükleri (durumu yaşar, last_seen_at 48 sa içinde)
	// tabanda değilse canlı sayılmaz: taban = görünen 14 → tetik yok.
	seen14 := v2SnapKeys(snap, cid)
	if got := v2MassAbsence(snap, cid, seen14); len(got) != 0 {
		t.Fatalf("silinmiş iş yükleri tabanda değilken tetiklenmemeli: %v", got)
	}
	// Taban yokken (yeniden kurulum sonrası ilk okuma) son 48 sa canlı durumlar.
	if b := v2AbsenceBase(nil, cid, states, nil, now); len(b) != 30 {
		t.Fatalf("tabansız: 48 sa içindeki durumlar: %d", len(b))
	}
	for i := range states {
		if i >= 14 {
			states[i].LastSeenAt = now.Add(-72 * time.Hour)
		}
	}
	if b := v2AbsenceBase(nil, cid, states, nil, now); len(b) != 14 || v2MassAbsence(snap, cid, b) != nil {
		t.Fatalf("eski (48 sa'ten uzun) durumlar tabana girmemeli: %d", len(b))
	}
	// İnceleme düzeltmesi: pencere v2TouchEvery + 1 sa — 30 sa önce son
	// görülen (dün silinmiş) durum tabansız yeniden kurulumda canlı sayılmaz.
	for i := range states {
		if i >= 14 {
			states[i].LastSeenAt = now.Add(-30 * time.Hour)
		}
	}
	if b := v2AbsenceBase(nil, cid, states, nil, now); len(b) != 14 {
		t.Fatalf("30 sa önce son görülen durum tabana girmemeli: %d", len(b))
	}
	// Güncel incarnation'ının son olayı "gone" ile superseded kapanmış iş
	// yükü (silinmiş) de sayılmaz; eski incarnation'ın superseded olayı
	// durumu dışlamaz.
	for i := range states {
		states[i].LastSeenAt = now.Add(-time.Hour)
	}
	inc := detT0.Add(-48 * time.Hour)
	states[20].IncarnationAt, states[21].IncarnationAt = inc, inc
	evs := []V2Event{
		{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "w20", IncarnationAt: inc, Generation: 3, Status: V2StatusSuperseded},
		{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "w21", IncarnationAt: inc.Add(-time.Hour), Generation: 3, Status: V2StatusSuperseded},
		{ClusterID: cid, Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "w21", IncarnationAt: inc, Generation: 1, Status: V2StatusSucceeded},
	}
	if b := v2AbsenceBase(nil, cid, states, evs, now); len(b) != 29 || b[V2Key{cid, "pay", V2KindDeployment, "w20"}] || !b[V2Key{cid, "pay", V2KindDeployment, "w21"}] {
		t.Fatalf("gone kapanışlı iş yükü dışlanmalı, eski incarnation'ınki değil: %d", len(b))
	}
	if b := v2AbsenceBase(seen14, cid, states, nil, now); len(b) != 14 {
		t.Fatalf("taban varken durumlara bakılmaz: %d", len(b))
	}
	// Küçük nüfus (< 20) korunmaz.
	small := map[V2Key]bool{}
	for k := range base {
		if len(small) < 10 {
			small[k] = true
		}
	}
	if got := v2MassAbsence(V2Snapshot{Workloads: obs[:1]}, cid, small); len(got) != 0 {
		t.Fatalf("küçük nüfus: %v", got)
	}
}

func TestV2FreezeAbsence(t *testing.T) {
	k := func(kind, name string) V2Key { return V2Key{"c", "pay", kind, name} }
	gone := k(V2KindDeployment, "gone")
	live := k(V2KindDeployment, "live")
	sts := k(V2KindStatefulSet, "db")
	ev := func(key V2Key) V2Event {
		return V2Event{ClusterID: key.ClusterID, Namespace: key.Namespace, WorkloadKind: key.Kind, Workload: key.Workload}
	}
	out := V2Output{
		Events: []V2Event{ev(gone), ev(live), ev(sts)},
		States: []V2WorkloadState{{ClusterID: "c", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "live"}},
		Memory: V2Memory{Absent: map[V2Key]int{gone: 3, sts: 2}},
	}
	seen := map[V2Key]bool{live: true}
	n := v2FreezeAbsence(&out, seen, []string{V2KindDeployment}, V2Memory{Absent: map[V2Key]int{gone: 2, sts: 1}})
	if n != 1 {
		t.Fatalf("dondurulan: %d", n)
	}
	if len(out.Events) != 2 || out.Events[0].Workload != "live" || out.Events[1].Workload != "db" {
		t.Fatalf("görünmeyen dondurulmuş iş yükünün olayı atılmalı, diğerleri kalmalı: %+v", out.Events)
	}
	if len(out.States) != 1 {
		t.Fatalf("görünen iş yükünün durumu kalmalı: %+v", out.States)
	}
	if !reflect.DeepEqual(out.Memory.Absent, map[V2Key]int{gone: 2, sts: 2}) {
		t.Fatalf("yokluk sayacı önceki değere dönmeli (dondurulmayan tür aynen): %v", out.Memory.Absent)
	}
	out2 := V2Output{Memory: V2Memory{Absent: map[V2Key]int{gone: 1}}}
	v2FreezeAbsence(&out2, seen, []string{V2KindDeployment}, V2Memory{})
	if out2.Memory.Absent != nil {
		t.Fatalf("önceki sayaç yoksa sayaç düşmeli: %v", out2.Memory.Absent)
	}
}

// v0.10.982 — P2.2 incelemesi: toplu yoklukta küme DONMAZ — görünen iş
// yükleri işlenir (bekleyiş sırasında başlayan rollout zamanında START
// alır), yalnız kaybolanların yokluk işlemesi durur ("gone" yok, sayaç
// ilerlemez); 30 dk sonra kabul edilir ve taban sıfırlanır → sonraki düşüş
// korumayı yeniden tetikler (tükenmiş sayaç 48 sa kilitli kalmaz).
func TestV2DetectorMassAbsenceHoldThenAccept(t *testing.T) {
	h := newV2Harness(t)
	h.settings.IncarnationAbsentTicks = 2
	for i := 0; i < 24; i++ {
		h.dep(fmt.Sprintf("w%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("w%02d-a", i): 1}, nil)
	}
	h.tick() // bootstrap: 24 baseline
	if len(h.store.states) != 24 {
		t.Fatalf("baseline: %d", len(h.store.states))
	}
	// Açık olay: w10 rollout ortasında (kaybolacaklardan biri).
	h.dep("w10", 2, 2, 1, 2, 1, 1, map[string]uint32{"w10-a": 1, "w10-b": 1}, nil)
	h.tick()
	if evs := h.store.eventList(); len(evs) != 1 || evs[0].Workload != "w10" || evs[0].Status != V2StatusProgressing {
		t.Fatalf("w10 START: %+v", evs)
	}
	for i := 4; i < 24; i++ { // 20/24 kayboldu (ör. bir KSM shard'ı düştü)
		delete(h.ksm.deps, fmt.Sprintf("w%02d", i))
	}
	holdTicks := int(v2MassAbsenceMaxHold / (30 * time.Second))
	for i := 0; i < holdTicks; i++ {
		if i == 3 { // bekleyiş sırasında görünen iş yükünde rollout
			h.dep("w00", 2, 2, 1, 2, 1, 1, map[string]uint32{"w00-a": 1, "w00-b": 1}, nil)
		}
		h.tick()
		run := h.store.lastRun(t)
		if run.Status != RunPartial || !strings.Contains(run.Error, "mass_absence_hold=1") || !strings.Contains(run.Error, "mass_absence_frozen=20") {
			t.Fatalf("tik %d: toplu yoklukta yokluk dondurulmalı: %+v", i, run)
		}
	}
	var w00 *V2Event
	for _, e := range h.store.eventList() {
		if e.Workload == "w10" && e.Status != V2StatusProgressing {
			t.Fatalf("bekleyişte kaybolan iş yükünün açık olayı kapanmamalı: %+v", e)
		}
		if e.Workload == "w00" {
			e := e
			w00 = &e
		}
	}
	if w00 == nil || w00.ChangeType != V2ChangeRollout {
		t.Fatalf("bekleyiş sırasında görünen iş yükü işlenmeli (START): %+v", h.store.eventList())
	}
	if want := detT0.Add(time.Duration(2+3+1)*30*time.Second - 5*time.Second); !w00.StartedAt.Equal(want) {
		t.Fatalf("started_at bekleyişle kaymamalı: %v != %v", w00.StartedAt, want)
	}
	h.tick() // süre doldu: yokluk kabul edilir, taban sıfırlanır
	if run := h.store.lastRun(t); run.Status != RunOK || !strings.Contains(run.Error, "mass_absence_accepted=1") || !strings.Contains(run.Error, "absent=20") {
		t.Fatalf("süre sonunda kabul: %+v", run)
	}
	h.tick() // K=2: kaybolanlar gone; w10'un açık olayı kapanır
	for _, e := range h.store.eventList() {
		if e.Workload == "w10" && e.Status == V2StatusProgressing {
			t.Fatalf("kabulden sonra yokluk işlenmeli: %+v", e)
		}
	}
	// Yeni taban (4 iş yükü < 20): korumaya takılmaz; nüfus geri gelip sonra
	// yeniden düşerse koruma YENİDEN tetiklenir.
	for i := 4; i < 24; i++ {
		h.dep(fmt.Sprintf("x%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("x%02d-a", i): 1}, nil)
	}
	h.tick()
	for i := 4; i < 24; i++ {
		delete(h.ksm.deps, fmt.Sprintf("x%02d", i))
	}
	h.tick()
	if run := h.store.lastRun(t); !strings.Contains(run.Error, "mass_absence_hold=1") {
		t.Fatalf("kabulden sonra yeni düşüş korumayı yeniden tetiklemeli: %+v", run)
	}
}

// v0.10.982 — P2.2 incelemesi: silinmiş iş yükleri (durumu yaşar) toplu
// yokluk tabanında değildir — bir namespace'in gerçek sökümü, önceden
// silinmişlerle birlikte "yarıdan fazla yok" görünüp kümeyi dondurmaz.
func TestV2DetectorMassAbsenceIgnoresAlreadyGone(t *testing.T) {
	h := newV2Harness(t)
	for i := 0; i < 30; i++ {
		h.dep(fmt.Sprintf("s%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("s%02d-a", i): 1}, nil)
	}
	for i := 0; i < 35; i++ {
		h.dep(fmt.Sprintf("t%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("t%02d-a", i): 1}, nil)
	}
	h.tick() // bootstrap 65
	for i := 0; i < 35; i++ {
		delete(h.ksm.deps, fmt.Sprintf("t%02d", i))
	}
	for i := 0; i < int(v2MassAbsenceMaxHold/(30*time.Second))+1; i++ {
		h.tick() // 35/65 kayboldu: koruma bekler, sonra kabul
	}
	for i := 0; i < 5; i++ {
		h.tick()
	}
	// Şimdi s'lerin yarıdan azı kalacak biçimde değil — tek bir s silinir:
	// taban 30 (t'ler tabanda değil) → tetik yok, koşu ok.
	delete(h.ksm.deps, "s00")
	h.tick()
	if run := h.store.lastRun(t); run.Status != RunOK || strings.Contains(run.Error, "mass_absence") {
		t.Fatalf("silinmişler tabanda olmamalı: %+v", run)
	}
}

// v0.10.982 — P2.2 ikinci inceleme: bir tik tür ailesi büsbütün yok
// okunursa (çekirdek family_absent ile korur, yokluk işlemez) o türün
// tabanı düşmez; sonraki tikte kısmi geri dönüş (V9: KSM shard'ının biri
// önce döner) korumayı YİNE tetikler — kaybolanlar "gone" kapanmaz, açık
// olay açık kalır. Düzeltmeden önce taban = görünenler (boş) olur, 4/24
// geri dönüşte koruma susar, K=2 tik sonra 20 iş yükü gone kapanırdı.
func TestV2DetectorMassAbsenceAfterFamilyAbsent(t *testing.T) {
	h := newV2Harness(t)
	h.settings.IncarnationAbsentTicks = 2
	all := map[string]*fkDep{}
	for i := 0; i < 24; i++ {
		h.dep(fmt.Sprintf("w%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("w%02d-a", i): 1}, nil)
	}
	h.tick() // bootstrap: 24 baseline
	h.dep("w10", 2, 2, 1, 2, 1, 1, map[string]uint32{"w10-a": 1, "w10-b": 1}, nil)
	h.tick() // w10 START (açık olay)
	for n, d := range h.ksm.deps {
		all[n] = d
	}
	h.ksm.deps = map[string]*fkDep{} // tür ailesi büsbütün yok (tam okuma)
	h.tick()
	if run := h.store.lastRun(t); !strings.Contains(run.Error, "family_absent=1") {
		t.Fatalf("aile yokken çekirdek korumalı: %+v", run)
	}
	for i := 0; i < 4; i++ { // yalnız 4/24 geri döndü
		n := fmt.Sprintf("w%02d", i)
		h.ksm.deps[n] = all[n]
	}
	for i := 0; i < 4; i++ {
		h.tick()
		run := h.store.lastRun(t)
		if !strings.Contains(run.Error, "mass_absence_hold=1") || strings.Contains(run.Error, "gone=") {
			t.Fatalf("tik %d: kısmi geri dönüş korumayı tetiklemeli: %+v", i, run)
		}
	}
	for _, e := range h.store.eventList() {
		if e.Workload == "w10" && e.Status != V2StatusProgressing {
			t.Fatalf("kaybolan iş yükünün açık olayı kapanmamalı: %+v", e)
		}
	}
}

func TestV2NextBaseline(t *testing.T) {
	k := func(kind, name string) V2Key { return V2Key{"c", "pay", kind, name} }
	base := map[V2Key]bool{k(V2KindDeployment, "a"): true, k(V2KindDeployment, "b"): true,
		k(V2KindStatefulSet, "db"): true, k(V2KindDaemonSet, "agent"): true}
	seen := map[V2Key]bool{k(V2KindDeployment, "a"): true}
	// STS ailesi yok (etkin) → tabanı kalır; DS etkin değil → düşer;
	// Deployment görünür, dondurulmamış → yalnız görünen.
	got := v2NextBaseline(seen, base, nil, []string{V2KindDeployment, V2KindStatefulSet})
	want := map[V2Key]bool{k(V2KindDeployment, "a"): true, k(V2KindStatefulSet, "db"): true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("taban: %v", got)
	}
	got = v2NextBaseline(seen, base, []string{V2KindDeployment}, []string{V2KindDeployment})
	if !got[k(V2KindDeployment, "b")] || got[k(V2KindStatefulSet, "db")] {
		t.Fatalf("dondurulan türde kaybolan kalmalı: %v", got)
	}
}

// v0.10.982 — P2.2 ikinci inceleme: imaj okuması boş dönen fill adayları
// geri çekilir; anahtar sırasında sonraki iş yükleri de bütçe alır.
// Düzeltmeden önce ilk 20 (imajsız) aday her tik yeniden seçilir, "z00"ın
// durum imajı hiç dolmazdı.
func TestV2DetectorFillBackoffNoStarvation(t *testing.T) {
	h := newV2Harness(t)
	for i := 0; i < 25; i++ {
		h.dep(fmt.Sprintf("a%02d", i), 1, 1, 1, 1, 1, 1, map[string]uint32{fmt.Sprintf("a%02d-x", i): 1}, nil)
	}
	h.dep("z00", 1, 1, 1, 1, 1, 1, map[string]uint32{"z00-x": 1}, map[string][]string{"z00-x": {"reg/z:1"}})
	h.tick() // bootstrap
	zk := V2Key{"c-a", "pay", V2KindDeployment, "z00"}
	filled := false
	for i := 0; i < 6 && !filled; i++ {
		h.tick()
		filled = len(h.store.states[zk].Images) > 0
	}
	if !filled {
		t.Fatalf("geri çekilme sonraki adayı doldurmalı: %v / %q", h.store.states[zk].Images, h.store.lastRun(t).Error)
	}
	if run := h.store.lastRun(t); !strings.Contains(run.Error, "images_fill_deferred=") {
		t.Fatalf("geri çekilen adaylar sayılmalı: %q", run.Error)
	}
}

func TestV2PickAndNoteFill(t *testing.T) {
	k := func(n string) V2Key { return V2Key{"c", "pay", V2KindDeployment, n} }
	tries := map[V2Key]v2FillTry{k("a"): {empty: 1, next: 5}, k("gone"): {empty: 3, next: 99}}
	pick, deferred := v2PickFill([]V2Key{k("a"), k("b"), k("c")}, tries, 3, 1)
	if !reflect.DeepEqual(pick, []V2Key{k("b")}) || deferred != 1 {
		t.Fatalf("geri çekilmedeki atlanır, limit uygulanır: %v %d", pick, deferred)
	}
	if _, ok := tries[k("gone")]; ok {
		t.Fatal("aday olmayan kayıt budanmalı")
	}
	if pick, _ := v2PickFill([]V2Key{k("a")}, tries, 5, 1); len(pick) != 1 {
		t.Fatal("süre dolunca yeniden denenir")
	}
	snap := V2Snapshot{Workloads: []V2Observation{
		{Kind: V2KindDeployment, Namespace: "pay", Name: "a", RevisionImages: map[string][]string{}},
		{Kind: V2KindDeployment, Namespace: "pay", Name: "b", RevisionImages: map[string][]string{"b-1": {"reg/b:1"}}},
	}}
	states := []V2WorkloadState{
		{ClusterID: "c", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "a", CurrentRevision: "a-1"},
		{ClusterID: "c", Namespace: "pay", WorkloadKind: V2KindDeployment, Workload: "b", CurrentRevision: "b-1"},
	}
	tries[k("b")] = v2FillTry{empty: 2, next: 1}
	if n := v2NoteFill(snap, "c", []V2Key{k("a"), k("b")}, states, tries, 10); n != 1 {
		t.Fatalf("boş dönen: %d", n)
	}
	if got := tries[k("a")]; got.empty != 2 || got.next != 14 {
		t.Fatalf("geri çekilme 2^n büyümeli: %+v", got)
	}
	if _, ok := tries[k("b")]; ok {
		t.Fatal("imaj gelince kayıt silinmeli")
	}
	tries[k("a")] = v2FillTry{empty: 20}
	v2NoteFill(snap, "c", []V2Key{k("a")}, states, tries, 10)
	if got := tries[k("a")]; got.next != 10+1<<v2FillBackoffMaxShift {
		t.Fatalf("geri çekilme tavanlı: %+v", got)
	}
}

// v0.10.982 — P2.2 incelemesi: bir kümenin bütün tik sorguları (imaj
// sorguları dahil) TEK değerlendirme zamanında, tik − v2EvalLag.
func TestV2DetectorPinnedEvalTime(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.tick()
	h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2}, map[string][]string{"api-b": {"reg/api:2"}})
	h.ksm.ats = nil
	h.tick()
	if len(h.ksm.ats) != 7 {
		t.Fatalf("6 KSM + 1 imaj sorgusu: %d", len(h.ksm.ats))
	}
	want := h.clock.Add(-v2EvalLag)
	for i, at := range h.ksm.ats {
		if !at.Equal(want) {
			t.Fatalf("sorgu %d zamanı %v, istenen %v (hepsi aynı)", i, at, want)
		}
	}
}

// v0.10.982 — P2.2 incelemesi: tik sürerken liderlik kaybedilip YENİDEN
// edinildiyse (OnAcquire → resetPending) arada başka pod yazmış olabilir:
// bellekten yazım yapılmaz, sonraki tik CH'den kurar.
func TestV2DetectorReacquireDuringTickSkipsWrite(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.tick()
	h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2}, nil)
	calls := 0
	h.det.SetLeaderCheck(func() bool {
		calls++
		if calls == 2 { // yazım öncesi denetimden hemen önce: kayıp + yeniden edinim
			h.det.OnAcquire()
		}
		return true
	})
	h.tick()
	if n := len(h.store.eventList()); n != 0 {
		t.Fatalf("yeniden edinimde bayat bellekten yazılmamalı: %d olay", n)
	}
	if run := h.store.lastRun(t); run.Status != RunPartial || !strings.Contains(run.Error, "yeniden edinildi") {
		t.Fatalf("koşu: %+v", run)
	}
	reads := h.store.stateReads
	h.det.SetLeaderCheck(func() bool { return true })
	h.tick()
	if h.store.stateReads != reads+1 {
		t.Fatalf("sonraki tik CH'den kurmalı: %d → %d", reads, h.store.stateReads)
	}
	if evs := h.store.eventList(); len(evs) != 1 || evs[0].NewRevision != "api-b" {
		t.Fatalf("yeniden kurulan durumdan START: %+v", evs)
	}
}

// v0.10.982 — P2.2 incelemesi (§10.3.1 mint öncesi FINAL okuma): lockDegraded
// sürerken her pod liderdir; durum HER tik CH'den tazelenir ki başka pod'un
// bastığı incarnation benimsensin (kendi belleğinde ikinci incarnation
// taşıyıp her olayı iki kez yazmasın). Yokluk sayaçları korunur.
func TestV2DetectorDegradedRefreshesState(t *testing.T) {
	a := newV2Harness(t)
	degraded := true
	a.det.SetDegradedCheck(func() bool { return degraded })
	a.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	a.tick() // bootstrap
	// İkinci pod aynı depoda (split-brain): yeni iş yükü "web"i ÖNCE o basar.
	b := newV2Harness(t)
	b.store, b.clock = a.store, a.clock
	b.det = NewV2Detector(b.store, b.ksm, fakeV2Clusters{{ID: "c-a", Name: "prod-a"}}, func() Settings { return b.settings })
	b.det.now = func() time.Time { return b.clock }
	b.det.host = "pod-2"
	b.ksm.deps = a.ksm.deps
	a.dep("web", 1, 1, 2, 2, 2, 2, map[string]uint32{"web-a": 2}, nil)
	b.tick() // pod-2: CH'den kurar, web için initial basar
	var inc time.Time
	for _, e := range b.store.eventList() {
		if e.Workload == "web" {
			inc = e.IncarnationAt
		}
	}
	if inc.IsZero() {
		t.Fatalf("pod-2 web initial basmalı: %+v", b.store.eventList())
	}
	reads := a.store.stateReads
	a.clock = b.clock.Add(90 * time.Second) // pod-1 bir scrape sonra: farklı örnek zamanı
	a.tick()
	if a.store.stateReads != reads+1 {
		t.Fatalf("degraded'da durum her tik okunmalı: %d → %d", reads, a.store.stateReads)
	}
	webIncs := map[int64]bool{}
	for _, e := range a.store.eventList() {
		if e.Workload == "web" {
			webIncs[e.IncarnationAt.UnixMilli()] = true
		}
	}
	if len(webIncs) != 1 || !webIncs[inc.UnixMilli()] {
		t.Fatalf("pod-1 pod-2'nin incarnation'ını benimsemeli (tek incarnation): %v", webIncs)
	}
	if run := a.store.lastRun(t); !strings.Contains(run.Error, "state_refreshed_degraded=1") {
		t.Fatalf("teşhis: %q", run.Error)
	}
	// Degraded bitince bellek taşınır (her tik okuma yok).
	degraded = false
	reads = a.store.stateReads
	a.tick()
	if a.store.stateReads != reads {
		t.Fatalf("degraded değilken okuma olmamalı: %d → %d", reads, a.store.stateReads)
	}
}

// v0.10.982 — P2.2 incelemesi: bootstrap sonrası ilk kez görülen ve ilk
// okumada zaten hazır olan iş yükünün initial olayı aynı tikte succeeded
// kapanır — imajı mint tikinde okunur (sonraki tikte hedef olmazdı).
func TestV2DetectorInitialEventGetsImages(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, nil)
	h.tick() // bootstrap
	h.dep("web", 1, 1, 2, 2, 2, 2, map[string]uint32{"web-a": 2}, map[string][]string{"web-a": {"reg/web:7"}})
	h.tick()
	var web *V2Event
	for _, e := range h.store.eventList() {
		if e.Workload == "web" {
			e := e
			web = &e
		}
	}
	if web == nil || web.ChangeType != V2ChangeInitial || web.Status != V2StatusSucceeded {
		t.Fatalf("hazır yeni iş yükü: succeeded initial: %+v", web)
	}
	if !reflect.DeepEqual(web.Images, []string{"reg/web:7"}) {
		t.Fatalf("initial olayın imajı mint tikinde dolmalı: %v", web.Images)
	}
}

// v0.10.982 — P2.2 incelemesi: bootstrap baseline'ı imaj okumaz; artan
// bütçeyle (fill) sonraki tikte durumun imajı dolar → ilk START'ta eski
// pod'lar gitmiş olsa da prev_images durumdan gelir.
func TestV2DetectorBootstrapImagesFilled(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, map[string][]string{"api-a": {"reg/api:1"}})
	h.tick() // bootstrap: imaj yok
	if st := h.store.states[V2Key{"c-a", "pay", V2KindDeployment, "api"}]; len(st.Images) != 0 {
		t.Fatalf("bootstrap imaj okumaz: %v", st.Images)
	}
	h.tick() // fill
	if st := h.store.states[V2Key{"c-a", "pay", V2KindDeployment, "api"}]; !reflect.DeepEqual(st.Images, []string{"reg/api:1"}) {
		t.Fatalf("fill durumun imajını doldurmalı: %v", st.Images)
	}
	if run := h.store.lastRun(t); run.APICalls != 7 {
		t.Fatalf("6 KSM + 1 fill sorgusu: %+v", run)
	}
	h.tick()
	if run := h.store.lastRun(t); run.APICalls != 6 {
		t.Fatalf("imaj dolunca fill hedefi kalmaz: %+v", run)
	}
	// Rollout restart (aynı imaj, yeni RS) ve eski pod'lar okumada yok:
	// prev_images durumdan gelir → config.
	h.dep("api", 2, 2, 3, 3, 3, 3, map[string]uint32{"api-a": 0, "api-b": 3}, map[string][]string{"api-b": {"reg/api:1"}})
	h.tick()
	evs := h.store.eventList()
	if len(evs) != 1 || evs[0].ChangeType != V2ChangeConfig || !reflect.DeepEqual(evs[0].PrevImages, []string{"reg/api:1"}) {
		t.Fatalf("aynı imajlı restart config olmalı: %+v", evs)
	}
}

func TestV2DetectorWaitActive(t *testing.T) {
	h := newV2Harness(t)
	h.settings.Source = SourceV1
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if h.det.WaitActive(ctx, time.Millisecond) {
		t.Fatal("bayrak kapalıyken WaitActive dönmemeli (bağlam kapanınca false)")
	}
	var mu sync.Mutex
	h.det.settings = func() Settings {
		mu.Lock()
		defer mu.Unlock()
		return h.settings
	}
	go func() {
		time.Sleep(5 * time.Millisecond)
		mu.Lock()
		h.settings.Source = SourceV2
		mu.Unlock()
	}()
	if !h.det.WaitActive(context.Background(), time.Millisecond) {
		t.Fatal("bayrak açılınca true dönmeli")
	}
}

// TestV2DetectorStampsUpdatedAtAtWrite — İnceleme (v0.10.984): updated_at
// tik başlangıcı değil YAZIM anı; küme okumaları uzarsa (parallel=4 kuyruğu,
// yavaş Thanos) tik zamanı SSE tail'in 15 s watermark'ını geçer ve keyset
// kursör satırı hiç görmezdi. version tikten kalır.
func TestV2DetectorStampsUpdatedAtAtWrite(t *testing.T) {
	h := newV2Harness(t)
	h.dep("api", 1, 1, 3, 3, 3, 3, map[string]uint32{"api-a": 3}, map[string][]string{"api-a": {"reg/api:1"}})
	h.tick() // bootstrap
	h.dep("api", 2, 2, 3, 4, 2, 3, map[string]uint32{"api-a": 2, "api-b": 2},
		map[string][]string{"api-a": {"reg/api:1"}, "api-b": {"reg/api:2"}})
	h.clock = h.clock.Add(30 * time.Second)
	h.ksm.sampleAt = h.clock.Add(-5 * time.Second)
	// Her saat okuması 1 s ilerler: yazım, tik başlangıcından sonra.
	var seen []time.Time
	h.det.now = func() time.Time {
		at := h.clock.Add(time.Duration(len(seen)) * time.Second)
		seen = append(seen, at)
		return at
	}
	h.det.Tick(context.Background())
	evs := h.store.eventList()
	if len(evs) != 1 || len(seen) < 3 {
		t.Fatalf("START olayı + en az 3 saat okuması bekleniyordu: %d olay, %d okuma", len(evs), len(seen))
	}
	tickAt := seen[1] // tick(): run.StartedAt, ardından tik zamanı
	if !evs[0].UpdatedAt.After(tickAt) {
		t.Fatalf("updated_at yazım anı olmalı (> tik %v): %v", tickAt, evs[0].UpdatedAt)
	}
	if evs[0].Version != uint64(tickAt.UnixNano()) {
		t.Fatalf("version tik zamanından kalmalı: %d != %d", evs[0].Version, tickAt.UnixNano())
	}
	// Saf yardımcı: ms kesim, hepsine aynı an.
	es := []V2Event{{}, {}}
	v2StampWrite(es, detT0.Add(1500*time.Microsecond))
	if !es[0].UpdatedAt.Equal(detT0.Add(time.Millisecond)) || !es[1].UpdatedAt.Equal(es[0].UpdatedAt) {
		t.Fatalf("v2StampWrite: %v %v", es[0].UpdatedAt, es[1].UpdatedAt)
	}
}
