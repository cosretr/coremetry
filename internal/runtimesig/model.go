// Package runtimesig — tek runtime-metrik modeli (2026-11-21, operatör:
// "Coremetry runtime dotnet o metrikleri de kullanabilir, biraz daha akıllı
// olabilir. Aynı Elastic APM olduğu gibi.").
//
// Üç katman, ikisi SAF:
//
//   - model.go (SAF): runtime tespiti (JVM / .NET / Go / Node / Python) +
//     sinyal anahtarı → runtime başına aday metrik adları. .NET'in İKİ kuşağı
//     birlikte: OpenTelemetry.Instrumentation.Runtime (`process.runtime.
//     dotnet.*`, .NET 6/8) ve .NET 9+ yerleşik semconv (`dotnet.*`). Her aday
//     hem noktalı (ClickHouse metric_points) hem Prometheus-dönüşümlü
//     (VictoriaMetrics: birim soneki + sayaçta `_total`) yazımla verilir —
//     vmetrics.nameSpellings `…_count` ile biten adda `_total` adayı
//     ÜRETMEZ (names.go "Known cost"), prod'daki
//     `process_runtime_dotnet_gc_collections_count_total` bu yüzden adla
//     tahmin edilemez; burada AÇIKÇA yazılır.
//   - read.go: servisin ad kataloğundan (tek ListMetricNames) çözümleme +
//     sinyal başına TEK sınırlı sorgu (≤ len(Signals), pencere 60 dk, adım 60 s).
//   - detect.go (SAF): türetilmiş göstergeler (GC baskısı, gen2 sıçraması,
//     thread-pool açlığı, kilit çekişmesi, exception sıçraması, bellek
//     sızıntısı eğilimi), eşikler + kanıt metni.
package runtimesig

import "strings"

// Runtime — servisin dil-runtime ailesi.
type Runtime string

const (
	RuntimeUnknown Runtime = ""
	RuntimeJVM     Runtime = "jvm"
	RuntimeDotnet  Runtime = "dotnet"
	RuntimeGo      Runtime = "go"
	RuntimeNode    Runtime = "nodejs"
	RuntimePython  Runtime = "python"
)

// SignalKey — runtime'dan bağımsız sinyal adı (UI + detektör + CoSRE ortak).
type SignalKey string

const (
	SigHeapUsed       SignalKey = "heap_used"       // bayt (gauge; .NET: son GC sonrası nesil toplamı)
	SigHeapCommitted  SignalKey = "heap_committed"  // bayt
	SigHeapLimit      SignalKey = "heap_limit"      // bayt (yalnız JVM)
	SigFragmentation  SignalKey = "heap_fragmented" // bayt (.NET)
	SigGCCollections  SignalKey = "gc_collections"  // koleksiyon/sn (nesil etiketli olabilir)
	SigGCTime         SignalKey = "gc_time"         // GC saniyesi / duvar saniyesi (oran; ×100 = pay %)
	SigAllocRate      SignalKey = "alloc_rate"      // bayt/sn
	SigThreads        SignalKey = "threads"         // adet (JVM thread / .NET thread-pool thread)
	SigPoolQueue      SignalKey = "pool_queue"      // .NET thread-pool kuyruğu (iş öğesi)
	SigPoolCompleted  SignalKey = "pool_completed"  // tamamlanan iş öğesi/sn
	SigLockContention SignalKey = "lock_contention" // çekişme/sn
	SigExceptions     SignalKey = "exceptions"      // exception/sn
	SigJITMethods     SignalKey = "jit_methods"     // derlenen metot/sn
	SigWorkingSet     SignalKey = "working_set"     // bayt
	SigCPUUtil        SignalKey = "cpu_util"        // 0..1 (CPython)
	SigMemoryRSS      SignalKey = "memory_rss"      // bayt (CPython rss)
	SigCtxSwitches    SignalKey = "ctx_switches"    // bağlam değişimi/sn (CPython)
	// Süreç düzeyi ortak sinyaller (runtime'dan bağımsız; common.go).
	SigFDOpen      SignalKey = "fd_open"       // açık dosya tanıtıcısı
	SigFDMax       SignalKey = "fd_max"        // tanıtıcı sınırı
	SigHTTPPending SignalKey = "http_pending"  // HttpClient havuzu bekleyen istek
	SigHTTPLeased  SignalKey = "http_leased"   // HttpClient havuzu kiralanmış bağlantı
	SigHTTPMax     SignalKey = "http_pool_max" // HttpClient havuz tavanı
)

// Signals — kararlı sıra (sorgu sırası + JSON sırası).
var Signals = []SignalKey{
	SigHeapUsed, SigHeapCommitted, SigHeapLimit, SigFragmentation,
	SigGCCollections, SigGCTime, SigAllocRate, SigThreads,
	SigPoolQueue, SigPoolCompleted, SigLockContention, SigExceptions,
	SigJITMethods, SigWorkingSet, SigCPUUtil, SigMemoryRSS, SigCtxSwitches,
	SigFDOpen, SigFDMax, SigHTTPPending, SigHTTPLeased, SigHTTPMax,
	SigKafkaWaiting, SigKafkaInFlight, SigKafkaBufExhausted, SigKafkaLag,
}

// Kind — ölçüm sınıfı → okuma toplaması.
type Kind string

const (
	KindGauge   Kind = "gauge"   // pod başına toplam (nesil/havuz toplanır)
	KindCounter Kind = "counter" // rate (sn başına)
)

// Source — bir sinyalin tek aday metriği.
type Source struct {
	// Names — aynı ölçünün yazımları (noktalı OTLP adı + Prometheus adı);
	// katalogda İLK bulunan kullanılır.
	Names []string
	Kind  Kind
	// Scale — okunan değer × Scale = sinyalin taban birimi (ns → s: 1e-9).
	Scale float64
	// GenLabel — nesil / havuz etiketi (.NET gc.collections / heap.size, JVM
	// havuz adı); pod başına toplam bu etiket üzerinden alınır. "" = yok.
	GenLabel string
	// Filter — tek eşitlik süzgeci (JVM jvm.memory.type=heap); boş = yok.
	FilterKey, FilterValue string
	// Gen — .NET kuşağı: "instrumentation" (process.runtime.dotnet.*) |
	// "semconv" (dotnet.*); JVM/Go için boş.
	Gen string
	// Agg — toplama zorlaması ("" = tür varsayılanı: gauge avg, sayaç rate);
	// ör. Kafka records_lag_max için "max".
	Agg string
}

const (
	GenInstrumentation = "instrumentation"
	GenSemconv         = "semconv"
)

func src(kind Kind, scale float64, gen string, names ...string) Source {
	return Source{Names: names, Kind: kind, Scale: scale, Gen: gen}
}

func (s Source) withGen(label string) Source { s.GenLabel = label; return s }
func (s Source) withFilter(k, v string) Source {
	s.FilterKey, s.FilterValue = k, v
	return s
}

const (
	dotnetOldGenLabel = "generation"
	dotnetNewGenLabel = "dotnet.gc.heap.generation"
)

// mappings — runtime → sinyal → aday kaynaklar (öncelik sırası: yeni
// semconv önce; ikisi birden varsa .NET 9 servisinin kendi kuşağı).
//
// .NET adları ve birimleri: OpenTelemetry.Instrumentation.Runtime 1.x
// README (process.runtime.dotnet.*: gc.collections.count {collections},
// gc.objects.size By, gc.allocations.size By, gc.committed_memory.size By,
// gc.heap.size By [generation], gc.heap.fragmentation.size By [generation],
// gc.duration ns (.NET 7+), jit.il_compiled.size By, jit.methods_compiled.count,
// jit.compilation_time ns, monitor.lock_contention.count,
// thread_pool.threads.count, thread_pool.completed_items.count,
// thread_pool.queue.length, timer.count, assemblies.count, exceptions.count)
// ve OTel semconv "Semantic conventions for .NET runtime metrics"
// (dotnet.gc.collections {collection} [dotnet.gc.heap.generation],
// dotnet.gc.heap.total_allocated By, dotnet.gc.last_collection.heap.size By,
// dotnet.gc.last_collection.heap.fragmentation.size By,
// dotnet.gc.last_collection.memory.committed_size By, dotnet.gc.pause.time s,
// dotnet.thread_pool.thread.count, dotnet.thread_pool.work_item.count,
// dotnet.thread_pool.queue.length, dotnet.monitor.lock_contentions,
// dotnet.exceptions [error.type], dotnet.jit.compiled_methods,
// dotnet.process.memory.working_set By). Prometheus adı: nokta → alt çizgi,
// `By` → `_bytes`, `s` → `_seconds`, `ns` → `_nanoseconds`, `{…}` birimi
// sonek EKLEMEZ, monotonik sayaç sona `_total`.
var mappings = map[Runtime]map[SignalKey][]Source{
	RuntimeDotnet: {
		SigHeapUsed: {
			src(KindGauge, 1, GenSemconv, "dotnet.gc.last_collection.heap.size", "dotnet_gc_last_collection_heap_size_bytes").withGen(dotnetNewGenLabel),
			src(KindGauge, 1, GenInstrumentation, "process.runtime.dotnet.gc.heap.size", "process_runtime_dotnet_gc_heap_size_bytes").withGen(dotnetOldGenLabel),
		},
		SigHeapCommitted: {
			src(KindGauge, 1, GenSemconv, "dotnet.gc.last_collection.memory.committed_size", "dotnet_gc_last_collection_memory_committed_size_bytes"),
			src(KindGauge, 1, GenInstrumentation, "process.runtime.dotnet.gc.committed_memory.size", "process_runtime_dotnet_gc_committed_memory_size_bytes"),
		},
		SigFragmentation: {
			src(KindGauge, 1, GenSemconv, "dotnet.gc.last_collection.heap.fragmentation.size", "dotnet_gc_last_collection_heap_fragmentation_size_bytes").withGen(dotnetNewGenLabel),
			src(KindGauge, 1, GenInstrumentation, "process.runtime.dotnet.gc.heap.fragmentation.size", "process_runtime_dotnet_gc_heap_fragmentation_size_bytes").withGen(dotnetOldGenLabel),
		},
		SigGCCollections: {
			src(KindCounter, 1, GenSemconv, "dotnet.gc.collections", "dotnet_gc_collections_total").withGen(dotnetNewGenLabel),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.gc.collections.count", "process_runtime_dotnet_gc_collections_count_total").withGen(dotnetOldGenLabel),
		},
		SigGCTime: {
			src(KindCounter, 1, GenSemconv, "dotnet.gc.pause.time", "dotnet_gc_pause_time_seconds_total"),
			src(KindCounter, 1e-9, GenInstrumentation, "process.runtime.dotnet.gc.duration", "process_runtime_dotnet_gc_duration_nanoseconds_total"),
		},
		SigAllocRate: {
			src(KindCounter, 1, GenSemconv, "dotnet.gc.heap.total_allocated", "dotnet_gc_heap_total_allocated_bytes_total"),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.gc.allocations.size", "process_runtime_dotnet_gc_allocations_size_bytes_total"),
		},
		SigThreads: {
			src(KindGauge, 1, GenSemconv, "dotnet.thread_pool.thread.count", "dotnet_thread_pool_thread_count"),
			src(KindGauge, 1, GenInstrumentation, "process.runtime.dotnet.thread_pool.threads.count", "process_runtime_dotnet_thread_pool_threads_count"),
		},
		SigPoolQueue: {
			src(KindGauge, 1, GenSemconv, "dotnet.thread_pool.queue.length", "dotnet_thread_pool_queue_length"),
			src(KindGauge, 1, GenInstrumentation, "process.runtime.dotnet.thread_pool.queue.length", "process_runtime_dotnet_thread_pool_queue_length"),
		},
		SigPoolCompleted: {
			src(KindCounter, 1, GenSemconv, "dotnet.thread_pool.work_item.count", "dotnet_thread_pool_work_item_count_total"),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.thread_pool.completed_items.count", "process_runtime_dotnet_thread_pool_completed_items_count_total"),
		},
		SigLockContention: {
			src(KindCounter, 1, GenSemconv, "dotnet.monitor.lock_contentions", "dotnet_monitor_lock_contentions_total"),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.monitor.lock_contention.count", "process_runtime_dotnet_monitor_lock_contention_count_total"),
		},
		SigExceptions: {
			src(KindCounter, 1, GenSemconv, "dotnet.exceptions", "dotnet_exceptions_total"),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.exceptions.count", "process_runtime_dotnet_exceptions_count_total"),
		},
		SigJITMethods: {
			src(KindCounter, 1, GenSemconv, "dotnet.jit.compiled_methods", "dotnet_jit_compiled_methods_total"),
			src(KindCounter, 1, GenInstrumentation, "process.runtime.dotnet.jit.methods_compiled.count", "process_runtime_dotnet_jit_methods_compiled_count_total"),
		},
		SigWorkingSet: {
			src(KindGauge, 1, GenSemconv, "dotnet.process.memory.working_set", "dotnet_process_memory_working_set_bytes"),
			// Eski kuşakta runtime working set vermez; OpenTelemetry.
			// Instrumentation.Process'in process.memory.usage'ı aynı ölçü.
			src(KindGauge, 1, GenInstrumentation, "process.memory.usage", "process_memory_usage_bytes"),
		},
	},
	RuntimeJVM: {
		SigHeapUsed: {src(KindGauge, 1, "", "jvm.memory.used", "jvm_memory_used_bytes").withFilter("jvm.memory.type", "heap").withGen("jvm.memory.pool.name"),
			// Micrometer / client_java eski adı: area=heap, havuz etiketi id.
			src(KindGauge, 1, "", "jvm_memory_bytes_used").withFilter("area", "heap").withGen("id")},
		SigHeapCommitted: {src(KindGauge, 1, "", "jvm.memory.committed", "jvm_memory_committed_bytes").withFilter("jvm.memory.type", "heap").withGen("jvm.memory.pool.name")},
		SigHeapLimit:     {src(KindGauge, 1, "", "jvm.memory.limit", "jvm_memory_limit_bytes").withFilter("jvm.memory.type", "heap").withGen("jvm.memory.pool.name")},
		// jvm.gc.duration histogramdır: VM'de _sum / _count parçaları sayaç.
		// ClickHouse'ta histogramın rate'i tanımsız → yalnız Prom adları.
		SigGCTime:        {src(KindCounter, 1, "", "jvm_gc_duration_seconds_sum")},
		SigGCCollections: {src(KindCounter, 1, "", "jvm_gc_duration_seconds_count")},
		SigThreads: {src(KindGauge, 1, "", "jvm.thread.count", "jvm_thread_count"),
			src(KindGauge, 1, "", "jvm_threads_live_threads"), src(KindGauge, 1, "", "jvm_threads_current")},
	},
	// CPython — opentelemetry-instrumentation-system-metrics (process.runtime.
	// cpython.*; gc_count'un "By" birimi tarihsel hata, değer NESİL BAŞINA
	// koleksiyon sayacı, nesil etiketi `count` = "0" | "1" | "2"; memory
	// `type` = rss | vms; context_switches `type` = voluntary | involuntary) +
	// yeni semconv cpython.gc.* (cpython.gc.generation).
	RuntimePython: {
		SigGCCollections: {
			src(KindCounter, 1, "", "cpython.gc.collections", "cpython_gc_collections_total").withGen("cpython.gc.generation"),
			src(KindCounter, 1, "", "process.runtime.cpython.gc_count", "process_runtime_cpython_gc_count_bytes_total").withGen("count"),
		},
		SigCPUUtil:     {src(KindGauge, 1, "", "process.runtime.cpython.cpu.utilization", "process_runtime_cpython_cpu_utilization_ratio")},
		SigMemoryRSS:   {src(KindGauge, 1, "", "process.runtime.cpython.memory", "process_runtime_cpython_memory_bytes").withFilter("type", "rss")},
		SigThreads:     {src(KindGauge, 1, "", "process.runtime.cpython.thread_count", "process_runtime_cpython_thread_count")},
		SigCtxSwitches: {src(KindCounter, 1, "", "process.runtime.cpython.context_switches", "process_runtime_cpython_context_switches_total")},
	},
	RuntimeGo: {
		SigHeapUsed: {
			src(KindGauge, 1, "", "go.memory.used", "go_memory_used_bytes"),
			src(KindGauge, 1, "", "process.runtime.go.mem.heap_inuse", "process_runtime_go_mem_heap_inuse_bytes"),
		},
		SigAllocRate: {src(KindCounter, 1, "", "go.memory.allocated", "go_memory_allocated_bytes_total")},
		SigThreads: {
			src(KindGauge, 1, "", "go.goroutine.count", "go_goroutine_count"),
			src(KindGauge, 1, "", "process.runtime.go.goroutines", "process_runtime_go_goroutines"),
		},
	},
}

// Mappings — runtime'ın aday tablosu (salt-okunur kullanım; test + belge).
func Mappings(rt Runtime) map[SignalKey][]Source { return mappings[rt] }

// familyPrefixes — katalogdan runtime tespiti için ad önekleri.
var familyPrefixes = []struct {
	rt       Runtime
	prefixes []string
}{
	{RuntimeDotnet, []string{"process.runtime.dotnet.", "process_runtime_dotnet_", "dotnet.", "dotnet_"}},
	{RuntimeJVM, []string{"jvm.", "jvm_", "process.runtime.jvm.", "process_runtime_jvm_"}},
	{RuntimeGo, []string{"process.runtime.go.", "process_runtime_go_", "go.memory.", "go_memory_", "go.goroutine.", "go_goroutine_"}},
	{RuntimeNode, []string{"v8js.", "v8js_", "nodejs.", "nodejs_"}},
	{RuntimePython, []string{"cpython.", "cpython_", "process.runtime.cpython.", "process_runtime_cpython_"}},
}

// DetectRuntime — resource öznitelikleri önce (telemetry.sdk.language,
// process.runtime.name), yoksa katalogdaki runtime metrik aileleri. SAF.
//
// Dil ile katalog çelişirse DİL kazanır (bir Java servisinin yanında
// sidecar'ın .NET metriği olabilir); dil boş / tanınmıyorsa katalogda en
// çok adı olan aile.
func DetectRuntime(language, runtimeName string, catalog []string) Runtime {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "java", "kotlin", "scala", "jvm":
		return RuntimeJVM
	case "dotnet", "csharp", "c#", ".net", "fsharp":
		return RuntimeDotnet
	case "go", "golang":
		return RuntimeGo
	case "nodejs", "javascript", "js", "typescript":
		return RuntimeNode
	case "python":
		return RuntimePython
	}
	rn := strings.ToLower(runtimeName)
	switch {
	case strings.Contains(rn, ".net"), strings.Contains(rn, "dotnet"), strings.Contains(rn, "coreclr"):
		return RuntimeDotnet
	case strings.Contains(rn, "java"), strings.Contains(rn, "openjdk"), strings.Contains(rn, "jdk"), strings.Contains(rn, "jre"):
		return RuntimeJVM
	case rn == "go" || strings.HasPrefix(rn, "go1") || strings.Contains(rn, "golang"):
		return RuntimeGo
	case strings.Contains(rn, "node"):
		return RuntimeNode
	case strings.Contains(rn, "python"):
		return RuntimePython
	}
	best, bestN := RuntimeUnknown, 0
	for _, f := range familyPrefixes {
		n := 0
		for _, name := range catalog {
			for _, p := range f.prefixes {
				if strings.HasPrefix(name, p) {
					n++
					break
				}
			}
		}
		if n > bestN {
			best, bestN = f.rt, n
		}
	}
	return best
}

// Resolved — katalogda bulunan tek sinyal kaynağı (UI bu adı AYNEN sorar).
type Resolved struct {
	Signal SignalKey `json:"signal"`
	Metric string    `json:"metric"` // katalogdaki yazım (VM: Prom adı; CH: noktalı)
	Kind   Kind      `json:"kind"`
	// Agg — gauge: avg (kova ortalaması; nesil/havuz toplamı istemci tarafında,
	// CH `sum` kovadaki export sayısıyla çarpardı) | counter: rate.
	Agg   string  `json:"agg"`
	Scale float64 `json:"scale"`
	// GenLabel — nesil kırılımı için groupBy anahtarı ("" = yok).
	GenLabel    string `json:"genLabel,omitempty"`
	FilterKey   string `json:"filterKey,omitempty"`
	FilterValue string `json:"filterValue,omitempty"`
	Gen         string `json:"gen,omitempty"`
	// PodScoped — follow-up 2026-11-21: seri service_name TAŞIMAZ (HikariCP);
	// servis süzgeci yerine servisin varlık pod'larıyla (host_name) sorulur.
	PodScoped bool `json:"podScoped,omitempty"`
}

// Profile — servisin runtime'ı + çözülmüş sinyaller.
type Profile struct {
	Runtime Runtime `json:"runtime"`
	// DotnetGen — .NET kuşağı (instrumentation | semconv | mixed); diğerlerinde boş.
	DotnetGen string     `json:"dotnetGen,omitempty"`
	Signals   []Resolved `json:"signals"`
}

// Has — sinyal çözüldü mü.
func (p Profile) Has(k SignalKey) bool {
	_, ok := p.Get(k)
	return ok
}

// Get — çözülmüş sinyal.
func (p Profile) Get(k SignalKey) (Resolved, bool) {
	for _, r := range p.Signals {
		if r.Signal == k {
			return r, true
		}
	}
	return Resolved{}, false
}

// Resolve — katalog (servisin metrik adları) üzerinden sinyal başına İLK
// eşleşen aday. Katalog verbatim karşılaştırılır (VM ham seri adı, CH noktalı
// ad); bulunamayan sinyal profilde yok. SAF.
func Resolve(rt Runtime, catalog []string) Profile { return ResolveGen(rt, catalog, "") }

// ResolveGen — Resolve, yalnız verilen .NET kuşağının adaylarıyla ("" = hepsi).
// Filo okuması (evaluator) iki kuşağı AYRI çözer: tek katalogda ikisi birden
// bulunur, semconv'u seçmek eski kuşak servislerini körleştirirdi. SAF.
func ResolveGen(rt Runtime, catalog []string, gen string) Profile {
	p := Profile{Runtime: rt, Signals: []Resolved{}}
	m := mappings[rt]
	have := make(map[string]bool, len(catalog))
	for _, n := range catalog {
		have[n] = true
	}
	gens := map[string]bool{}
	for _, k := range Signals {
		cands := m[k]
		if gen == "" {
			// Süreç düzeyi ortak sinyaller (her runtime + bilinmeyen) runtime'ın
			// kendi adaylarından SONRA: aile başına tek seçim, kopya çizgi yok.
			cands = append(append([]Source{}, cands...), commonMappings[k]...)
		}
		for _, s := range cands {
			if gen != "" && s.Gen != gen {
				continue
			}
			hit := ""
			for _, n := range s.Names {
				if have[n] {
					hit = n
					break
				}
			}
			if hit == "" {
				continue
			}
			agg := "avg"
			if s.Kind == KindCounter {
				agg = "rate"
			}
			if s.Agg != "" {
				agg = s.Agg
			}
			p.Signals = append(p.Signals, Resolved{Signal: k, Metric: hit, Kind: s.Kind, Agg: agg, Scale: s.Scale,
				GenLabel: s.GenLabel, FilterKey: s.FilterKey, FilterValue: s.FilterValue, Gen: s.Gen})
			if s.Gen != "" && k != SigWorkingSet {
				gens[s.Gen] = true
			}
			break
		}
	}
	if rt == RuntimeDotnet {
		switch {
		case gens[GenSemconv] && gens[GenInstrumentation]:
			p.DotnetGen = "mixed"
		case gens[GenSemconv]:
			p.DotnetGen = GenSemconv
		case gens[GenInstrumentation]:
			p.DotnetGen = GenInstrumentation
		}
	}
	return p
}

// IsRuntimeMetric — katalog süzgeci: ad bilinen bir runtime ailesinde mi
// (okuma tarafı yalnız bunları taşır).
func IsRuntimeMetric(name string) bool {
	for _, f := range familyPrefixes {
		for _, p := range f.prefixes {
			if strings.HasPrefix(name, p) {
				return true
			}
		}
	}
	if isCommonName(name) {
		return true
	}
	return strings.HasPrefix(name, "process.memory.") || strings.HasPrefix(name, "process_memory_")
}
