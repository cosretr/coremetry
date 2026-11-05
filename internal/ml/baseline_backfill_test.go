package ml

// v0.10.1815 — baseline geçmiş doldurma (audit §4.2, dilim 1.4): kuru koşu
// sorgu sayıları, gün planı (en yeni önce), lider kararı, sürdürme (biten gün
// atlanır), iptal / kaynak beklemesi temiz durur, hesap 1814 çekirdeğiyle
// aynı yol, VM aralık sonucunun kova eşlemesi canlıyla aynı.

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestPlanBaselineBackfill(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors[detLogVolume] = true
	cfg.Detectors[detOpLatency] = true
	cfg.Detectors["metric_jvm"] = true // 3 ifade
	cfg.BackfillIntervalMs = 2000
	all := []string{"log", "trace", "metric"}
	p := PlanBaselineBackfill(cfg, 56, all, true, true, 3, 1000)
	if p.ES != 56*24 || p.PerSignal["trace"].VM != 56*3*5 || p.PerSignal["metric"].VM != 56*3 || p.VM != 56*3*5+56*3 || p.CH != 1 {
		t.Fatalf("plan %+v", p)
	}
	if want := (56*24 + 56*15 + 56*3) * 3; p.EstimatedSec != want {
		t.Fatalf("süre %d, beklenen %d", p.EstimatedSec, want)
	}
	// kaynaksız / dedektörü kapalı sinyal koşmaz ve nedenini söyler
	p = PlanBaselineBackfill(cfg, 7, all, false, false, 3, 0)
	if p.ES != 0 || p.VM != 0 || p.CH != 0 || p.PerSignal["log"].Skipped == "" || p.PerSignal["trace"].Skipped == "" || p.PerSignal["metric"].Skipped == "" {
		t.Fatalf("kaynaksız plan %+v", p)
	}
	cfg.Detectors[detLogVolume] = false
	if s := PlanBaselineBackfill(cfg, 7, []string{"log"}, true, true, 3, 0).PerSignal["log"].Skipped; s == "" {
		t.Fatal("log dedektörleri kapalıyken log doldurulmamalı")
	}
	if got := NormalizeBackfillSignals([]string{"Metric", "log", "log", "x"}); !reflect.DeepEqual(got, []string{"log", "metric"}) {
		t.Fatalf("sinyaller %v", got)
	}
}

func TestBackfillPlanDaysNewestFirst(t *testing.T) {
	got := backfillPlanDays(time.Date(2026, 11, 5, 1, 0, 0, 0, istanbul), istanbul, 3)
	if !reflect.DeepEqual(got, []string{"2026-11-04", "2026-11-03", "2026-11-02"}) {
		t.Fatalf("gün planı %v", got)
	}
}

func TestBackfillAction(t *testing.T) {
	req := &chstore.MLBaselineBackfillRequest{ID: "b1", Days: 3}
	cases := []struct {
		name          string
		req           *chstore.MLBaselineBackfillRequest
		cur           chstore.MLBaselineBackfillStatus
		have          bool
		start, cancel bool
	}{
		{"istek yok", nil, chstore.MLBaselineBackfillStatus{}, false, false, false},
		{"ilk başlangıç", req, chstore.MLBaselineBackfillStatus{}, false, true, false},
		{"yarıda kalan sürer", req, chstore.MLBaselineBackfillStatus{ID: "b1"}, true, true, false},
		{"biten tekrar koşmaz", req, chstore.MLBaselineBackfillStatus{ID: "b1", Complete: true}, true, false, false},
		{"yeni istek eskiyi geçer", &chstore.MLBaselineBackfillRequest{ID: "b2"}, chstore.MLBaselineBackfillStatus{ID: "b1", Complete: true}, true, true, false},
		{"iptal: duran koşu işaretlenir", &chstore.MLBaselineBackfillRequest{ID: "b1", Cancel: true}, chstore.MLBaselineBackfillStatus{ID: "b1"}, true, false, true},
		{"iptal: biten koşu dokunulmaz", &chstore.MLBaselineBackfillRequest{ID: "b1", Cancel: true}, chstore.MLBaselineBackfillStatus{ID: "b1", Complete: true}, true, false, false},
	}
	for _, c := range cases {
		s, cn := backfillAction(c.req, c.cur, c.have)
		if s != c.start || cn != c.cancel {
			t.Errorf("%s: start=%v cancel=%v", c.name, s, cn)
		}
	}
}

type fakeBackfillStore struct {
	obs   []chstore.MLBaselineObs
	saves int
	last  chstore.MLBaselineBackfillStatus
}

func (f *fakeBackfillStore) InsertMLBaselineObs(_ context.Context, rows []chstore.MLBaselineObs) error {
	f.obs = append(f.obs, rows...)
	return nil
}
func (f *fakeBackfillStore) SaveMLBaselineBackfill(_ context.Context, st chstore.MLBaselineBackfillStatus) error {
	f.saves++
	f.last = st
	return nil
}

// fakeSource — gün başına tek birim; dayFixture'ı gün kaymasıyla üretir.
type fakeSource struct {
	fetched []string
	errOn   map[string]error
}

func (f *fakeSource) units(from, to time.Time) [][2]time.Time { return dayUnit(from, to) }
func (f *fakeSource) fetch(_ context.Context, from, _ time.Time) ([]chstore.MLSeriesRow, error) {
	day := from.In(istanbul).Format("2006-01-02")
	f.fetched = append(f.fetched, day)
	if err := f.errOn[day]; err != nil {
		return nil, err
	}
	shift := from.Sub(time.Date(2026, 11, 4, 0, 0, 0, 0, istanbul))
	var out []chstore.MLSeriesRow
	for _, r := range dayFixture() {
		if r.Signal == "log" {
			r.Bucket = r.Bucket.Add(shift)
			out = append(out, r)
		}
	}
	return out, nil
}

func backfillFixture(days []string) (*chstore.MLBaselineBackfillStatus, *fakeSource, *fakeBackfillStore, backfillEnv) {
	st := &chstore.MLBaselineBackfillStatus{ID: "b1", Signals: []string{"log", "metric"}, PlanDays: days,
		PerSignal: map[string]*chstore.MLBaselineBackfillSignal{"log": {DaysTotal: len(days)}, "metric": {DaysTotal: len(days), Skipped: "açık metrik ailesi yok"}}}
	src := &fakeSource{errOn: map[string]error{}}
	store := &fakeBackfillStore{}
	in := baselineInput{Loc: istanbul, TargetSec: 300, CfgHash: "h1"}
	env := backfillEnv{store: store, sources: map[string]backfillSource{"log": src}, input: func(string) baselineInput { return in },
		stop: func() (string, bool) { return "", false }}
	return st, src, store, env
}

func TestRunBaselineBackfillSameCoreAndResume(t *testing.T) {
	days := []string{"2026-11-04", "2026-11-03"}
	st, src, store, env := backfillFixture(days)
	st.PerSignal["log"].Done = []string{"2026-11-03"} // önceki koşuda bitmiş
	runBaselineBackfill(context.Background(), env, st)
	if !reflect.DeepEqual(src.fetched, []string{"2026-11-04"}) {
		t.Fatalf("biten gün atlanmalı, atlanan sinyal hiç okunmamalı: %v", src.fetched)
	}
	in := env.input("")
	logRows := []chstore.MLSeriesRow{}
	for _, r := range dayFixture() {
		if r.Signal == "log" {
			logRows = append(logRows, r)
		}
	}
	want := BaselineDayObs(logRows, in)
	if !reflect.DeepEqual(store.obs, want) {
		t.Fatalf("geçmiş doldurma 1814 çekirdeğiyle aynı satırları yazmalı (%d vs %d)", len(store.obs), len(want))
	}
	ls := store.last.PerSignal["log"]
	if !store.last.Complete || store.last.Running || ls.DaysDone != 1 || ls.Rows != len(want) || len(ls.Done) != 2 {
		t.Fatalf("durum %+v log=%+v", store.last, ls)
	}
}

func TestRunBaselineBackfillCancelAndWait(t *testing.T) {
	days := []string{"2026-11-04", "2026-11-03"}
	// iptal: ilk birimden önce durur, gün bitmiş sayılmaz
	st, src, store, env := backfillFixture(days)
	env.stop = func() (string, bool) { return "iptal edildi", true }
	runBaselineBackfill(context.Background(), env, st)
	if len(src.fetched) != 0 || !store.last.Cancelled || store.last.Running || store.last.Complete || len(store.last.PerSignal["log"].Done) != 0 {
		t.Fatalf("iptal: %+v fetched=%v", store.last, src.fetched)
	}
	// kaynak beklemesi: gün bitmiş sayılmaz, koşu durur (sonra sürer), iptal değil
	st, src, store, env = backfillFixture(days)
	src.errOn["2026-11-03"] = errBackfillWait
	runBaselineBackfill(context.Background(), env, st)
	ls := store.last.PerSignal["log"]
	if store.last.Cancelled || store.last.Complete || store.last.Running || ls.DaysDone != 1 || store.last.Stopped == "" {
		t.Fatalf("bekleme: %+v log=%+v", store.last, ls)
	}
	// sıradan hata: gün biter, hata sayılır, koşu sürer
	st, src, store, env = backfillFixture(days)
	src.errOn["2026-11-04"] = errors.New("es 500")
	runBaselineBackfill(context.Background(), env, st)
	ls = store.last.PerSignal["log"]
	if !store.last.Complete || ls.Errors != 1 || ls.DaysDone != 2 || ls.LastError != "es 500" {
		t.Fatalf("hata: %+v log=%+v", store.last, ls)
	}
}

// rangeVM — aralık sorgusuna sabit seri döner; 422 sayacı.
type rangeVM struct {
	fakeVM
	series  []chstore.SpanMetricSeries
	heavy   int // ilk n çağrı 422
	queries int
}

func (f *rangeVM) MetricLabelMap() vmetrics.LabelMap {
	return vmetrics.LabelMap{Service: "service_name"}
}
func (f *rangeVM) QueryPromQLRange(_ context.Context, _ string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	f.queries++
	if f.queries <= f.heavy {
		return nil, errors.New("HTTP 422: maxSamplesPerQuery")
	}
	return f.series, nil
}

func TestMetricBackfillSourceBuckets(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.Enabled = true
	cfg.Detectors["metric_k8s"] = true
	cfg.MetricFamilies["metric_k8s"] = []chstore.MLMetricExpr{{ID: "cpu", Expr: "x", Direction: "up"}}
	t0 := time.Date(2026, 11, 4, 10, 0, 0, 0, istanbul)
	vm := &rangeVM{series: []chstore.SpanMetricSeries{{GroupKey: []string{`service_name="branch-teller"`},
		Points: []chstore.SpanMetricPoint{{Time: t0.UnixNano(), Value: 2}, {Time: t0.Add(5 * time.Minute).UnixNano(), Value: 4}}}}}
	src := metricBackfillSource{r: vmRanger{vm: vm, live: newVMBackoff(), own: newVMBackoff(), step: 300}, cfg: cfg}
	rows, err := src.fetch(context.Background(), t0.Add(-10*time.Hour), t0.Add(14*time.Hour))
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	obs := BaselineDayObs(rows, baselineInput{Loc: istanbul, TargetSec: 300})
	if len(obs) != 1 || obs[0].Part != "metric_k8s|cpu" || obs[0].Service != "branch-teller" || obs[0].Slot != 58 || obs[0].Q50 != 3 || obs[0].Buckets != 2 {
		t.Fatalf("obs %+v", obs)
	}
}

func TestVMRangerHalvesThenWaits(t *testing.T) {
	own := newVMBackoff()
	// tam gün 422 → iki yarım gün başarılı
	vm := &rangeVM{heavy: 1, series: []chstore.SpanMetricSeries{{GroupKey: []string{"a=b"}}}}
	r := vmRanger{vm: vm, live: newVMBackoff(), own: own, step: 300}
	from := time.Date(2026, 11, 4, 0, 0, 0, 0, istanbul)
	if rs, err := r.query(context.Background(), "metric:metric_k8s", "x", from, from.Add(24*time.Hour)); err != nil || len(rs) != 2 || vm.queries != 3 {
		t.Fatalf("yarılama: rs=%d err=%v q=%d", len(rs), err, vm.queries)
	}
	// yarılar da 422 → kendi backoff'u + errBackfillWait; canlı backoff DOKUNULMAZ
	vm = &rangeVM{heavy: 9}
	live := newVMBackoff()
	r = vmRanger{vm: vm, live: live, own: own, step: 300}
	if _, err := r.query(context.Background(), "metric:metric_k8s", "x", from, from.Add(24*time.Hour)); !errors.Is(err, errBackfillWait) {
		t.Fatalf("bekleme hatası beklenir: %v", err)
	}
	if live.skipping("metric:metric_k8s", time.Now()) || !own.skipping("metric:metric_k8s", time.Now()) {
		t.Fatal("geçmiş sorgusunun 422'si canlı aileyi beklemeye almamalı")
	}
	// canlı aile beklemedeyse geçmiş de sorgu göndermez
	live.fail("trace:http", errors.New("422"), time.Now())
	q := vm.queries
	if _, err := r.query(context.Background(), "trace:http", "x", from, from.Add(time.Hour)); !errors.Is(err, errBackfillWait) || vm.queries != q {
		t.Fatalf("canlı backoff'ta sorgu gitmemeli: err=%v", err)
	}
}
