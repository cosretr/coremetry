package runtimesig

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Prod biçimi (system-metrics instrumentation, Prometheus adları).
func TestResolveCPythonProm(t *testing.T) {
	cat := []string{
		"process_runtime_cpython_context_switches_total", "process_runtime_cpython_cpu_time_seconds_total",
		"process_runtime_cpython_cpu_utilization_ratio", "process_runtime_cpython_gc_count_bytes_total",
		"process_runtime_cpython_memory_bytes", "process_runtime_cpython_thread_count",
	}
	if rt := DetectRuntime("", "", cat); rt != RuntimePython {
		t.Fatalf("detect = %q", rt)
	}
	p := Resolve(RuntimePython, cat)
	gc, ok := p.Get(SigGCCollections)
	if !ok || gc.Metric != "process_runtime_cpython_gc_count_bytes_total" || gc.Agg != "rate" || gc.GenLabel != "count" {
		t.Fatalf("gc = %+v", gc)
	}
	rss, _ := p.Get(SigMemoryRSS)
	if rss.FilterKey != "type" || rss.FilterValue != "rss" {
		t.Fatalf("rss = %+v", rss)
	}
	for _, k := range []SignalKey{SigCPUUtil, SigThreads, SigCtxSwitches} {
		if !p.Has(k) {
			t.Errorf("%s çözülmedi", k)
		}
	}
	// Yeni semconv noktalı ad.
	q := Resolve(RuntimePython, []string{"cpython.gc.collections"})
	if g, _ := q.Get(SigGCCollections); g.GenLabel != "cpython.gc.generation" {
		t.Fatalf("semconv gc = %+v", g)
	}
	// Python servisinde .NET/JVM sinyali yok.
	if p.Has(SigPoolQueue) || p.Has(SigHeapLimit) {
		t.Fatal("python profili başka runtime sinyali taşımamalı")
	}
}

// Prod VM: .NET serileri pod kimliği taşımıyor (yalnız service_name) →
// sum(rate) pod toplamı; GC payı count ile pod ortalamasına iner.
func TestGCShareNormalisedWithoutPodIdentity(t *testing.T) {
	r := &countReader{fakeReader: fakeReader{
		names: []string{"process_runtime_dotnet_gc_duration_nanoseconds_total"},
		series: map[string][]chstore.SpanMetricSeries{
			// 4 pod × %5 = 0.2 s/s toplam (ns/s olarak).
			"process_runtime_dotnet_gc_duration_nanoseconds_total": {toSMS([]string{"", "", ""}, series(step(0.2e9, 0.2e9)))},
		},
	}, pods: 4}
	rep, err := Analyze(context.Background(), r, "svc-a", "dotnet", "", th(), time.Unix(0, fixNow))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range rep.Signals {
		if s.Check == CheckGCPressure {
			t.Fatalf("GC payı pod toplamı okundu: %+v", s)
		}
	}
	if r.counts != 1 {
		t.Fatalf("count queries = %d", r.counts)
	}
}

type countReader struct {
	fakeReader
	pods   float64
	counts int
}

func (c *countReader) QueryMetric(ctx context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	if q.Aggregation == "count" {
		c.counts++
		return []chstore.SpanMetricSeries{toSMS([]string{"", "", ""}, series(func(int) float64 { return c.pods }))}, nil
	}
	return c.fakeReader.QueryMetric(ctx, q)
}
