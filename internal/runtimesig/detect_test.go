package runtimesig

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const minute = int64(60e9)

var fixNow = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC).UnixNano()

// series — 60 dakikalık seri: i. dakika (0 = en eski) için f(i).
func series(f func(i int) float64) []Point {
	out := make([]Point, 0, 60)
	for i := 0; i < 60; i++ {
		out = append(out, Point{T: fixNow - int64(59-i)*minute, V: f(i)})
	}
	return out
}

func step(base, recent float64) func(int) float64 {
	return func(i int) float64 {
		if i >= 50 {
			return recent
		}
		return base
	}
}

func th() Thresholds {
	return ThresholdsFrom(chstore.DefaultRuntimeSignals(), chstore.DefaultRuntimeAlerts())
}

func only(t *testing.T, sigs []Signal, check string) Signal {
	t.Helper()
	for _, s := range sigs {
		if s.Check == check {
			return s
		}
	}
	t.Fatalf("%s yok: %+v", check, sigs)
	return Signal{}
}

func none(t *testing.T, sigs []Signal, check string) {
	t.Helper()
	for _, s := range sigs {
		if s.Check == check {
			t.Fatalf("%s beklenmiyordu: %+v", check, s)
		}
	}
}

func TestGCPressure(t *testing.T) {
	// %4 → %18 (yükselen, warn 10 ≤ x < crit 25) → warning.
	d := PodData{Service: "svc-a", Pod: "pod-1", Series: map[SignalKey][]Point{SigGCTime: series(step(0.04, 0.18))}}
	s := only(t, EvaluatePod(d, fixNow, th()), CheckGCPressure)
	if s.Level != LevelWarning || !s.Rising || s.Value != 18 {
		t.Fatalf("got %+v", s)
	}
	// Sürekli %15 (artmıyor) → info, Problem adayı değil.
	d.Series[SigGCTime] = series(step(0.15, 0.15))
	s = only(t, EvaluatePod(d, fixNow, th()), CheckGCPressure)
	if s.Level != LevelInfo || s.Actionable() {
		t.Fatalf("flat high = %+v", s)
	}
	// %30 → critical.
	d.Series[SigGCTime] = series(step(0.30, 0.30))
	if s = only(t, EvaluatePod(d, fixNow, th()), CheckGCPressure); s.Level != LevelCritical {
		t.Fatalf("crit = %+v", s)
	}
	// %5 → sinyal yok.
	d.Series[SigGCTime] = series(step(0.05, 0.05))
	none(t, EvaluatePod(d, fixNow, th()), CheckGCPressure)
}

func TestGen2Spike(t *testing.T) {
	// 0.2/dk → 3/dk (≥ 2/dk ve ≥ 3× taban).
	d := PodData{Service: "svc-a", Pod: "pod-1", Gen2: series(step(0.2/60, 3.0/60))}
	s := only(t, EvaluatePod(d, fixNow, th()), CheckGen2Spike)
	if s.Level != LevelWarning || s.Value != 3 {
		t.Fatalf("got %+v", s)
	}
	d.Gen2 = series(step(2.5/60, 3.0/60)) // taban zaten yüksek → sıçrama değil
	none(t, EvaluatePod(d, fixNow, th()), CheckGen2Spike)
}

func TestThreadPoolStarvation(t *testing.T) {
	// Kuyruk 2 → 150, tamamlanan iş 400/sn düz, thread 20 → 60 → critical.
	d := PodData{Service: "svc-a", Pod: "pod-1", Series: map[SignalKey][]Point{
		SigPoolQueue:     series(step(2, 150)),
		SigPoolCompleted: series(step(400, 405)),
		SigThreads:       series(step(20, 60)),
	}}
	s := only(t, EvaluatePod(d, fixNow, th()), CheckPoolStarvation)
	if s.Level != LevelCritical || !s.Rising {
		t.Fatalf("got %+v", s)
	}
	for _, w := range []string{"Thread pool açlığı", "düz", "enjeksiyon"} {
		if !strings.Contains(s.Summary, w) {
			t.Errorf("summary %q lacks %q", s.Summary, w)
		}
	}
	// Kuyruk 2 → 30 (warn) ama tamamlanan iş 400 → 900/sn: yük artışı → info.
	d.Series[SigPoolQueue] = series(step(2, 30))
	d.Series[SigPoolCompleted] = series(step(400, 900))
	s = only(t, EvaluatePod(d, fixNow, th()), CheckPoolStarvation)
	if s.Level != LevelInfo || !strings.Contains(s.Summary, "açlık değil") {
		t.Fatalf("load growth = %+v", s)
	}
	// Kuyruk 5 → eşik altı.
	d.Series[SigPoolQueue] = series(step(1, 5))
	none(t, EvaluatePod(d, fixNow, th()), CheckPoolStarvation)
}

func TestLockAndExceptionSpikes(t *testing.T) {
	d := PodData{Service: "svc-a", Pod: "pod-1", Series: map[SignalKey][]Point{
		SigLockContention: series(step(2, 40)),  // 20× ≥ 3×, ≥ 10/s
		SigExceptions:     series(step(0.5, 4)), // 4/s < 5/s taban → yok
	}}
	sigs := EvaluatePod(d, fixNow, th())
	if s := only(t, sigs, CheckLockContention); s.Level != LevelCritical {
		t.Fatalf("lock = %+v", s)
	}
	none(t, sigs, CheckExceptionSpike)
	d.Series[SigExceptions] = series(step(1, 8)) // 8/s, 8× → warning
	if s := only(t, EvaluatePod(d, fixNow, th()), CheckExceptionSpike); s.Level != LevelWarning {
		t.Fatalf("exc = %+v", s)
	}
}

func TestMemoryLeakTrend(t *testing.T) {
	mb := float64(1 << 20)
	// 400 MB → ~636 MB doğrusal (+%59) → warning.
	d := PodData{Service: "svc-a", Pod: "pod-1", Series: map[SignalKey][]Point{
		SigHeapUsed: series(func(i int) float64 { return (400 + 4*float64(i)) * mb }),
	}}
	s := only(t, EvaluatePod(d, fixNow, th()), CheckMemoryLeak)
	if s.Level != LevelWarning || s.Extra["r2"] < 0.99 {
		t.Fatalf("got %+v", s)
	}
	// Testere dişi (GC) ama düz ortalama → yok.
	d.Series[SigHeapUsed] = series(func(i int) float64 { return float64(400+(i%5)*40) * mb })
	none(t, EvaluatePod(d, fixNow, th()), CheckMemoryLeak)
	// Heap yoksa working set kullanılır.
	d.Series = map[SignalKey][]Point{SigWorkingSet: series(func(i int) float64 { return (800 + 6*float64(i)) * mb })}
	if s := only(t, EvaluatePod(d, fixNow, th()), CheckMemoryLeak); !strings.Contains(s.Summary, "working set") {
		t.Fatalf("ws = %+v", s)
	}
}

func TestRollupWorstPodAndCount(t *testing.T) {
	sigs := Rollup([]Signal{
		{Check: CheckGCPressure, Level: LevelWarning, Service: "svc-a", Pod: "p1", Pods: 1, Value: 12},
		{Check: CheckGCPressure, Level: LevelCritical, Service: "svc-a", Pod: "p2", Pods: 1, Value: 30},
		{Check: CheckPoolStarvation, Level: LevelCritical, Service: "svc-a", Pod: "p1", Pods: 1, Value: 200},
		{Check: CheckExceptionSpike, Level: LevelInfo, Service: "svc-a", Pod: "p1", Pods: 1, Value: 1},
	})
	if len(sigs) != 3 || sigs[0].Check != CheckPoolStarvation || sigs[1].Pod != "p2" || sigs[1].Pods != 2 || sigs[2].Level != LevelInfo {
		t.Fatalf("rollup = %+v", sigs)
	}
}

// fakeReader — katalog + sabit seriler (QueryMetric ad başına).
type fakeReader struct {
	names   []string
	series  map[string][]chstore.SpanMetricSeries
	queries []chstore.MetricQueryFilter
}

func (f *fakeReader) ListMetricNames(_ context.Context, _, _ string, _, _ int) ([]chstore.MetricInfo, int, error) {
	out := make([]chstore.MetricInfo, 0, len(f.names))
	for _, n := range f.names {
		out = append(out, chstore.MetricInfo{Name: n})
	}
	return out, len(out), nil
}

func (f *fakeReader) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.queries = append(f.queries, q)
	return f.series[q.Name], nil
}

func toSMS(gk []string, pts []Point) chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{GroupKey: gk}
	for _, p := range pts {
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: p.T, Value: p.V})
	}
	return s
}

func TestAnalyzeStarvationEndToEnd(t *testing.T) {
	now := time.Unix(0, fixNow)
	r := &fakeReader{
		names: []string{"process_runtime_dotnet_thread_pool_queue_length", "process_runtime_dotnet_thread_pool_completed_items_count_total",
			"process_runtime_dotnet_gc_collections_count_total", "http_server_request_duration_seconds_bucket"},
		series: map[string][]chstore.SpanMetricSeries{
			"process_runtime_dotnet_thread_pool_queue_length":                {toSMS([]string{"pod-1", "", ""}, series(step(1, 80)))},
			"process_runtime_dotnet_thread_pool_completed_items_count_total": {toSMS([]string{"pod-1", "", ""}, series(step(300, 290)))},
			"process_runtime_dotnet_gc_collections_count_total": {
				toSMS([]string{"pod-1", "", "", "gen0"}, series(step(1, 1))),
				toSMS([]string{"pod-1", "", "", "gen2"}, series(step(0.1/60, 4.0/60))),
			},
		},
	}
	rep, err := Analyze(context.Background(), r, "svc-a", "dotnet", "", th(), now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Profile.Runtime != RuntimeDotnet || rep.Pods != 1 || len(r.queries) != 3 {
		t.Fatalf("rep=%+v queries=%d", rep, len(r.queries))
	}
	for _, q := range r.queries {
		if q.Service != "svc-a" || q.StepSeconds != 60 || !q.PlainSeries || q.To.Sub(q.From) != time.Hour {
			t.Errorf("unbounded / wrong query %+v", q)
		}
	}
	only(t, rep.Signals, CheckPoolStarvation)
	only(t, rep.Signals, CheckGen2Spike)
	// Kip off → okuma yok.
	r.queries = nil
	off := th()
	off.Mode = chstore.RuntimeSignalsOff
	rep, _ = Analyze(context.Background(), r, "svc-a", "dotnet", "", off, now)
	if len(r.queries) != 0 || len(rep.Signals) != 0 || !rep.Profile.Has(SigPoolQueue) {
		t.Fatalf("off mode read: %d queries, %+v", len(r.queries), rep)
	}
}
