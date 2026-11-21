package runtimesig

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestDetectRuntime(t *testing.T) {
	cases := []struct {
		lang, rtName string
		cat          []string
		want         Runtime
	}{
		{"dotnet", "", nil, RuntimeDotnet},
		{"java", "", []string{"process_runtime_dotnet_timer_count"}, RuntimeJVM}, // dil kazanır
		{"", ".NET", nil, RuntimeDotnet},
		{"", "OpenJDK Runtime Environment", nil, RuntimeJVM},
		{"", "go", nil, RuntimeGo},
		{"", "", []string{"process_runtime_dotnet_gc_heap_size_bytes", "process_runtime_dotnet_timer_count", "jvm_thread_count"}, RuntimeDotnet},
		{"", "", []string{"dotnet.gc.collections"}, RuntimeDotnet},
		{"", "", []string{"jvm.memory.used"}, RuntimeJVM},
		{"", "", []string{"http.server.request.duration"}, RuntimeUnknown},
		{"rust", "", nil, RuntimeUnknown},
	}
	for _, c := range cases {
		if got := DetectRuntime(c.lang, c.rtName, c.cat); got != c.want {
			t.Errorf("DetectRuntime(%q,%q,%v) = %q, want %q", c.lang, c.rtName, c.cat, got, c.want)
		}
	}
}

// Prod'da görülen eski kuşak Prometheus adları (operatör listesi) çözülür.
func TestResolveDotnetInstrumentationProm(t *testing.T) {
	cat := []string{
		"process_runtime_dotnet_exceptions_count_total",
		"process_runtime_dotnet_gc_allocations_size_bytes_total",
		"process_runtime_dotnet_gc_collections_count_total",
		"process_runtime_dotnet_gc_committed_memory_size_bytes",
		"process_runtime_dotnet_gc_duration_nanoseconds_total",
		"process_runtime_dotnet_gc_heap_fragmentation_size_bytes",
		"process_runtime_dotnet_gc_heap_size_bytes",
		"process_runtime_dotnet_jit_methods_compiled_count_total",
		"process_runtime_dotnet_monitor_lock_contention_count_total",
		"process_runtime_dotnet_thread_pool_completed_items_count_total",
		"process_runtime_dotnet_thread_pool_queue_length",
		"process_runtime_dotnet_thread_pool_threads_count",
	}
	p := Resolve(DetectRuntime("", "", cat), cat)
	if p.Runtime != RuntimeDotnet || p.DotnetGen != GenInstrumentation {
		t.Fatalf("runtime/gen = %q/%q", p.Runtime, p.DotnetGen)
	}
	want := map[SignalKey]string{
		SigGCCollections:  "process_runtime_dotnet_gc_collections_count_total",
		SigGCTime:         "process_runtime_dotnet_gc_duration_nanoseconds_total",
		SigPoolQueue:      "process_runtime_dotnet_thread_pool_queue_length",
		SigLockContention: "process_runtime_dotnet_monitor_lock_contention_count_total",
		SigExceptions:     "process_runtime_dotnet_exceptions_count_total",
		SigHeapUsed:       "process_runtime_dotnet_gc_heap_size_bytes",
		SigAllocRate:      "process_runtime_dotnet_gc_allocations_size_bytes_total",
	}
	for k, name := range want {
		r, ok := p.Get(k)
		if !ok || r.Metric != name {
			t.Errorf("%s → %+v, want %s", k, r, name)
		}
	}
	gc, _ := p.Get(SigGCTime)
	if gc.Agg != "rate" || gc.Scale != 1e-9 {
		t.Errorf("gc_time agg/scale = %s/%g", gc.Agg, gc.Scale)
	}
	heap, _ := p.Get(SigHeapUsed)
	if heap.Agg != "avg" || heap.GenLabel != "generation" {
		t.Errorf("heap agg/gen = %s/%s", heap.Agg, heap.GenLabel)
	}
	if p.Has(SigWorkingSet) {
		t.Error("working set eski kuşakta yoksa çözülmemeli")
	}
}

// .NET 9 semconv — noktalı (CH) ve Prometheus (VM) yazımları.
func TestResolveDotnetSemconvBothSpellings(t *testing.T) {
	dotted := []string{"dotnet.gc.collections", "dotnet.gc.pause.time", "dotnet.thread_pool.queue.length",
		"dotnet.monitor.lock_contentions", "dotnet.exceptions", "dotnet.process.memory.working_set", "dotnet.gc.last_collection.heap.size"}
	prom := []string{"dotnet_gc_collections_total", "dotnet_gc_pause_time_seconds_total", "dotnet_thread_pool_queue_length",
		"dotnet_monitor_lock_contentions_total", "dotnet_exceptions_total", "dotnet_process_memory_working_set_bytes", "dotnet_gc_last_collection_heap_size_bytes"}
	for _, cat := range [][]string{dotted, prom} {
		p := Resolve(RuntimeDotnet, cat)
		if p.DotnetGen != GenSemconv {
			t.Fatalf("gen = %q for %v", p.DotnetGen, cat)
		}
		for i, k := range []SignalKey{SigGCCollections, SigGCTime, SigPoolQueue, SigLockContention, SigExceptions, SigWorkingSet, SigHeapUsed} {
			r, ok := p.Get(k)
			if !ok || r.Metric != cat[i] {
				t.Errorf("%s → %q, want %q", k, r.Metric, cat[i])
			}
		}
		gc, _ := p.Get(SigGCTime)
		if gc.Scale != 1 {
			t.Errorf("pause.time scale = %g (saniye)", gc.Scale)
		}
		g, _ := p.Get(SigGCCollections)
		if g.GenLabel != "dotnet.gc.heap.generation" {
			t.Errorf("gen label %q", g.GenLabel)
		}
	}
	// İkisi birden: semconv önce, kuşak mixed değil semconv (aynı sinyal).
	p := Resolve(RuntimeDotnet, append(append([]string{}, prom...), "process_runtime_dotnet_gc_heap_size_bytes"))
	if r, _ := p.Get(SigHeapUsed); r.Gen != GenSemconv {
		t.Errorf("semconv öncelikli olmalı: %+v", r)
	}
}

// Her Prometheus adı OTLP→Prom dönüşümünün ürettiği biçimde: noktalı ad
// sanitize edilince Prom adının ÖNEKİ olmalı (birim / _total soneki hariç).
// Bu, eşleme tablosuna yazım hatası girmesini yakalar.
func TestMappingPromNamesDeriveFromDotted(t *testing.T) {
	for rt, m := range mappings {
		for k, srcs := range m {
			for _, s := range srcs {
				if len(s.Names) != 2 {
					continue
				}
				san := vmetrics.NameCandidates("gauge", s.Names[0])
				base := san[len(san)-1]
				if len(san) > 1 {
					base = san[1]
				}
				if !strings.HasPrefix(s.Names[1], base) {
					t.Errorf("%s/%s: prom %q, dotted %q (sanitize %q)", rt, k, s.Names[1], s.Names[0], base)
				}
				if s.Kind == KindCounter && !strings.HasSuffix(s.Names[1], "_total") {
					t.Errorf("%s/%s: sayaç prom adı _total ile bitmeli: %q", rt, k, s.Names[1])
				}
			}
		}
	}
}

func TestResolveJVMAndUnknown(t *testing.T) {
	p := Resolve(RuntimeJVM, []string{"jvm_memory_used_bytes", "jvm_gc_duration_seconds_sum", "jvm_thread_count"})
	if !p.Has(SigHeapUsed) || !p.Has(SigGCTime) || !p.Has(SigThreads) || p.DotnetGen != "" {
		t.Errorf("jvm profile %+v", p)
	}
	if q := Resolve(RuntimeUnknown, []string{"jvm_thread_count"}); len(q.Signals) != 0 {
		t.Errorf("unknown runtime çözülmemeli: %+v", q)
	}
}
