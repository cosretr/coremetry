package main

// runtime_metrics.go — v0.10.2490 (operatör: "Metrics sekmesinde teknolojiye
// göre, Elastic APM'deki gibi, pod bazlı, JVM bazlı, GC bazlı metrikler
// vardı"). Demo bugüne kadar her servise yalnız ortak adları yolluyordu
// (process.runtime.goroutines / memory.rss / cpu.utilization / gc.pause);
// Service › Metrics › Runtime bölümünün sorguladığı aileye özgü adların
// HİÇBİRİ yoktu → bölüm yalnız başlık satırını çiziyordu.
//
// Burada servisin diline göre, POD BAŞINA ayrı Resource ile (her pod kendi
// çizgisi), arayüzün GERÇEKTEN sorguladığı adlar üretilir — liste tek yerde:
// runtimeUIContract. Kaynaklar:
//   - JVM  : RuntimeCharts.tsx FAMILY_CARDS.jvm, anomaly.HeapMetricPostGC /
//            HeapMetricLimit (HeapBaselineCard), runtimesig JVM eşlemesi,
//            chstore.InstCPUSources (instances CPU), dashboard şablonu
//            jvm.class.count. OTel javaagent 2.x semconv adları/birimleri.
//   - .NET : runtimesig RuntimeDotnet "instrumentation" kuşağı (demo
//            servisleri .NET 8 → OpenTelemetry.Instrumentation.Runtime 1.x,
//            process.runtime.dotnet.*) + process.memory.usage (working set).
//   - Go   : RuntimeCharts.tsx FAMILY_CARDS.go + runtimesig Go eşlemesi
//            (otel-go contrib runtime, process.runtime.go.*).
//   - Python: runtimesig RuntimePython (opentelemetry-instrumentation-
//            system-metrics, process.runtime.cpython.*).
// Node.js için arayüzde aileye özgü sorgu YOK (Node → genel pod kaynak
// grafikleri); tahmin ad üretilmez. HikariCP havuz kartları yalnız
// Prometheus adlarıyla (hikaricp_connections_*, VictoriaMetrics) çözülür;
// OTLP'de o yazım semconv değil → üretilmez.
//
// Değerler demo'nun yük modeline bağlı (rf = trafik, lf = gecikme/olay
// baskısı) ve pod başına sabit bir çarpanla ayrışır. Senaryolu k8s olay
// pod'u (k8s_incident.go, Go api-gateway) atlanır: JVM değil, dokunulmaz.

import (
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"sync"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// runtimeFamilyOf — telemetry.sdk.language → aile (UI runtimeFamily.ts ikizi).
func runtimeFamilyOf(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "java", "kotlin", "scala":
		return "jvm"
	case "dotnet", ".net", "csharp":
		return "dotnet"
	case "go":
		return "go"
	case "python":
		return "python"
	}
	return ""
}

// runtimeUIContract — arayüzün aileye göre sorguladığı (ve demo'nun
// ürettiği) metrik adları. TEK yer: üretici yalnız bunları yollar, testler
// (runtime_metrics_test.go) bu kümenin RuntimeCharts.tsx / runtimesig /
// anomaly adlarını kapsadığını doğrular.
var runtimeUIContract = map[string][]string{
	"jvm": {
		"jvm.memory.used", "jvm.memory.committed", "jvm.memory.limit",
		"jvm.memory.used_after_last_gc", "jvm.gc.duration", "jvm.thread.count",
		"jvm.class.count", "jvm.cpu.recent_utilization",
	},
	"dotnet": {
		"process.runtime.dotnet.gc.collections.count", "process.runtime.dotnet.gc.heap.size",
		"process.runtime.dotnet.gc.committed_memory.size", "process.runtime.dotnet.gc.heap.fragmentation.size",
		"process.runtime.dotnet.gc.duration", "process.runtime.dotnet.gc.allocations.size",
		"process.runtime.dotnet.thread_pool.threads.count", "process.runtime.dotnet.thread_pool.queue.length",
		"process.runtime.dotnet.thread_pool.completed_items.count", "process.runtime.dotnet.monitor.lock_contention.count",
		"process.runtime.dotnet.exceptions.count", "process.runtime.dotnet.jit.methods_compiled.count",
		"process.memory.usage",
	},
	"go": {
		"process.runtime.go.mem.heap_inuse", "process.runtime.go.mem.heap_sys", "process.runtime.go.mem.heap_alloc",
		"process.runtime.go.mem.heap_objects", "process.runtime.go.gc.pause_ns", "process.runtime.go.gc.count",
		"process.runtime.go.goroutines",
	},
	"python": {
		"process.runtime.cpython.gc_count", "process.runtime.cpython.cpu.utilization",
		"process.runtime.cpython.memory", "process.runtime.cpython.thread_count",
		"process.runtime.cpython.context_switches",
	},
}

// rtState — sayaçların pod başına kümülatif değeri (OTLP cumulative Sum).
type rtState struct {
	mu      sync.Mutex
	startNs uint64
	cum     map[string]float64
}

var rtCum = &rtState{cum: map[string]float64{}}

// add — kümülatif sayacı d kadar artırır, yeni değeri döner.
func (r *rtState) add(key string, d float64) float64 {
	if d < 0 {
		d = 0
	}
	r.cum[key] += d
	return r.cum[key]
}

// podFactor — pod başına sabit çarpan 0.85..1.15 (ad özeti; çalıştırmalar arası aynı).
func podFactor(pod string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(pod))
	return 0.85 + 0.30*float64(h.Sum32()%1000)/1000
}

// saw — periyodik testere 0..1 (GC döngüsü), pod fazıyla kaydırılmış.
func saw(nowNs uint64, periodSec, phase float64) float64 {
	return math.Mod(float64(nowNs)/1e9/periodSec+phase, 1)
}

// rtPoint — tek sayı noktası.
func rtPoint(startNs, nowNs uint64, v float64, attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{StartTimeUnixNano: startNs, TimeUnixNano: nowNs,
		Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: v}, Attributes: attrs}
}

func rtGauge(name, unit string, pts ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Unit: unit,
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: pts}}}
}

// rtSum — monotonic=false → UpDownCounter (jvm.memory.*, jvm.thread.count).
func rtSum(name, unit string, monotonic bool, pts ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{Name: name, Unit: unit,
		Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{IsMonotonic: monotonic,
			AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			DataPoints:             pts}}}
}

// rtHist — delta histogram (tek nokta, count + sum; kova yok → avg-only satır).
func rtHist(name, unit string, startNs, nowNs uint64, count uint64, sum float64, attrs ...*commonpb.KeyValue) *metricspb.Metric {
	s := sum
	return &metricspb.Metric{Name: name, Unit: unit,
		Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
			DataPoints: []*metricspb.HistogramDataPoint{{StartTimeUnixNano: startNs, TimeUnixNano: nowNs,
				Count: count, Sum: &s, Attributes: attrs}}}}}
}

const rtFlushSec = 10.0 // sendMetrics kadansı

// runtimePodMetrics — bir pod'un dile göre runtime metrikleri. rf trafik,
// lf gecikme/olay baskısı (L.rateFactor / L.latencyFactor). Aile yoksa nil.
func runtimePodMetrics(s Service, pod string, nowNs uint64, rf, lf float64) []*metricspb.Metric {
	fam := runtimeFamilyOf(s.Lang)
	if fam == "" {
		return nil
	}
	rtCum.mu.Lock()
	defer rtCum.mu.Unlock()
	if rtCum.startNs == 0 {
		rtCum.startNs = nowNs
	}
	st := rtCum.startNs
	pf := podFactor(pod)
	phase := pf * 3
	load := math.Max(0.05, rf)
	press := math.Max(1, lf)
	ctr := func(name string, d float64, attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
		key := s.Name + "|" + pod + "|" + name
		for _, a := range attrs {
			key += "|" + a.Key + "=" + a.GetValue().GetStringValue()
		}
		return rtPoint(st, nowNs, rtCum.add(key, d), attrs...)
	}
	const MiB = 1024.0 * 1024
	switch fam {
	case "jvm":
		heapMax := 1024.0 * MiB
		heapT, nonT := kvStr("jvm.memory.type", "heap"), kvStr("jvm.memory.type", "non_heap")
		pool := func(n string) *commonpb.KeyValue { return kvStr("jvm.memory.pool.name", n) }
		eden := (40 + 180*saw(nowNs, 20, phase)) * MiB * (0.6 + 0.6*load)
		surv := (6 + 10*pf) * MiB
		// Old gen: yükle ve olay baskısıyla tırmanan taban + yavaş testere (major GC).
		old := heapMax * math.Min(0.92, (0.22+0.18*load)*pf*(0.9+0.15*saw(nowNs, 180, phase))*(0.85+0.15*press))
		meta, code, ccs := (92+14*pf)*MiB, (34+10*pf)*MiB, (11+2*pf)*MiB
		gcCount := math.Max(1, math.Round((2+6*load)*pf))
		gcAvg := (0.006 + 0.012*load) * press * press // s
		return []*metricspb.Metric{
			rtSum("jvm.memory.used", "By", false,
				rtPoint(st, nowNs, eden, heapT, pool("G1 Eden Space")),
				rtPoint(st, nowNs, surv, heapT, pool("G1 Survivor Space")),
				rtPoint(st, nowNs, old, heapT, pool("G1 Old Gen")),
				rtPoint(st, nowNs, meta, nonT, pool("Metaspace")),
				rtPoint(st, nowNs, code, nonT, pool("CodeCache")),
				rtPoint(st, nowNs, ccs, nonT, pool("Compressed Class Space"))),
			rtSum("jvm.memory.committed", "By", false,
				rtPoint(st, nowNs, 256*MiB, heapT, pool("G1 Eden Space")),
				rtPoint(st, nowNs, 24*MiB, heapT, pool("G1 Survivor Space")),
				rtPoint(st, nowNs, math.Min(heapMax, old*1.25), heapT, pool("G1 Old Gen")),
				rtPoint(st, nowNs, meta*1.05, nonT, pool("Metaspace")),
				rtPoint(st, nowNs, code*1.2, nonT, pool("CodeCache")),
				rtPoint(st, nowNs, ccs*1.1, nonT, pool("Compressed Class Space"))),
			// Sınır yalnız Old Gen (= -Xmx) ve CodeCache'te; Eden/Survivor/Metaspace
			// sınırsız (-1 → javaagent noktayı yollamaz).
			rtSum("jvm.memory.limit", "By", false,
				rtPoint(st, nowNs, heapMax, heapT, pool("G1 Old Gen")),
				rtPoint(st, nowNs, 240*MiB, nonT, pool("CodeCache"))),
			rtSum("jvm.memory.used_after_last_gc", "By", false,
				rtPoint(st, nowNs, 0, heapT, pool("G1 Eden Space")),
				rtPoint(st, nowNs, surv, heapT, pool("G1 Survivor Space")),
				rtPoint(st, nowNs, old*0.82, heapT, pool("G1 Old Gen"))),
			rtHist("jvm.gc.duration", "s", nowNs-uint64(rtFlushSec*1e9), nowNs, uint64(gcCount), gcCount*gcAvg,
				kvStr("jvm.gc.name", "G1 Young Generation"), kvStr("jvm.gc.action", "end of minor GC")),
			rtSum("jvm.thread.count", "{thread}", false, rtPoint(st, nowNs, math.Round((45+90*load*pf)*(0.9+0.2*press)))),
			rtSum("jvm.class.count", "{class}", false, rtPoint(st, nowNs, math.Round(14500+1800*pf))),
			rtGauge("jvm.cpu.recent_utilization", "1", rtPoint(st, nowNs, math.Min(0.98, (0.05+0.35*load)*pf*(0.7+0.3*press)))),
		}
	case "dotnet":
		gen := func(g string) *commonpb.KeyValue { return kvStr("generation", g) }
		g0 := (18 + 30*saw(nowNs, 15, phase)) * MiB * (0.6 + 0.6*load)
		g1, g2 := (6+6*pf)*MiB, (120+90*load*pf)*MiB*(0.9+0.1*press)
		loh, poh := (24+8*pf)*MiB, 1*MiB
		gc0 := (4 + 14*load) * pf * rtFlushSec
		ws := (g0 + g1 + g2 + loh + poh) * 1.6
		return []*metricspb.Metric{
			rtSum("process.runtime.dotnet.gc.collections.count", "{collections}", true,
				ctr("process.runtime.dotnet.gc.collections.count", gc0, gen("gen0")),
				ctr("process.runtime.dotnet.gc.collections.count", gc0/8, gen("gen1")),
				ctr("process.runtime.dotnet.gc.collections.count", gc0/60*press, gen("gen2"))),
			rtGauge("process.runtime.dotnet.gc.heap.size", "By",
				rtPoint(st, nowNs, g0, gen("gen0")), rtPoint(st, nowNs, g1, gen("gen1")),
				rtPoint(st, nowNs, g2, gen("gen2")), rtPoint(st, nowNs, loh, gen("loh")), rtPoint(st, nowNs, poh, gen("poh"))),
			rtGauge("process.runtime.dotnet.gc.committed_memory.size", "By", rtPoint(st, nowNs, (g0+g1+g2+loh+poh)*1.3)),
			rtGauge("process.runtime.dotnet.gc.heap.fragmentation.size", "By",
				rtPoint(st, nowNs, g2*0.04, gen("gen2")), rtPoint(st, nowNs, loh*0.12, gen("loh"))),
			// GC süresi (ns, .NET 7+): duvar saatinin ~%1–4'ü, baskıyla artar.
			rtSum("process.runtime.dotnet.gc.duration", "ns", true,
				ctr("process.runtime.dotnet.gc.duration", rtFlushSec*1e9*(0.008+0.02*load)*press*pf)),
			rtSum("process.runtime.dotnet.gc.allocations.size", "By", true,
				ctr("process.runtime.dotnet.gc.allocations.size", (8+60*load)*MiB*pf*rtFlushSec)),
			rtGauge("process.runtime.dotnet.thread_pool.threads.count", "{threads}", rtPoint(st, nowNs, math.Round((8+24*load)*pf*press))),
			rtGauge("process.runtime.dotnet.thread_pool.queue.length", "{work_items}", rtPoint(st, nowNs, math.Round(math.Max(0, 40*load*(press-1)*pf)))),
			rtSum("process.runtime.dotnet.thread_pool.completed_items.count", "{work_items}", true,
				ctr("process.runtime.dotnet.thread_pool.completed_items.count", (60+900*load)*pf*rtFlushSec)),
			rtSum("process.runtime.dotnet.monitor.lock_contention.count", "{contended_acquisitions}", true,
				ctr("process.runtime.dotnet.monitor.lock_contention.count", (0.2+3*load)*press*pf*rtFlushSec)),
			rtSum("process.runtime.dotnet.exceptions.count", "{exceptions}", true,
				ctr("process.runtime.dotnet.exceptions.count", (0.05+0.6*load)*press*press*pf*rtFlushSec)),
			rtSum("process.runtime.dotnet.jit.methods_compiled.count", "{methods}", true,
				ctr("process.runtime.dotnet.jit.methods_compiled.count", 2*pf*rtFlushSec)),
			rtSum("process.memory.usage", "By", false, rtPoint(st, nowNs, ws)),
		}
	case "go":
		inuse := (24 + 70*load*pf) * MiB * (0.75 + 0.25*saw(nowNs, 30, phase)) * (0.9 + 0.1*press)
		gcN := math.Max(1, math.Round((1+5*load)*pf))
		pauseNs := (60e3 + 180e3*load) * press * press
		return []*metricspb.Metric{
			rtSum("process.runtime.go.mem.heap_inuse", "By", false, rtPoint(st, nowNs, inuse)),
			rtSum("process.runtime.go.mem.heap_sys", "By", false, rtPoint(st, nowNs, inuse*1.6+16*MiB)),
			rtSum("process.runtime.go.mem.heap_alloc", "By", false, rtPoint(st, nowNs, inuse*0.88)),
			rtSum("process.runtime.go.mem.heap_objects", "{objects}", false, rtPoint(st, nowNs, math.Round(inuse/96))),
			rtHist("process.runtime.go.gc.pause_ns", "ns", nowNs-uint64(rtFlushSec*1e9), nowNs, uint64(gcN), gcN*pauseNs),
			rtSum("process.runtime.go.gc.count", "{count}", true, ctr("process.runtime.go.gc.count", gcN)),
			rtSum("process.runtime.go.goroutines", "{goroutines}", false, rtPoint(st, nowNs, math.Round((30+220*load*pf)*(0.9+0.2*press)))),
		}
	case "python":
		gc := func(g string) *commonpb.KeyValue { return kvStr("count", g) }
		g0 := (6 + 30*load) * pf * rtFlushSec
		rss := (140 + 120*load*pf) * MiB * (0.9 + 0.1*press)
		return []*metricspb.Metric{
			rtSum("process.runtime.cpython.gc_count", "By", true,
				ctr("process.runtime.cpython.gc_count", g0, gc("0")),
				ctr("process.runtime.cpython.gc_count", g0/10, gc("1")),
				ctr("process.runtime.cpython.gc_count", g0/100, gc("2"))),
			rtGauge("process.runtime.cpython.cpu.utilization", "1", rtPoint(st, nowNs, math.Min(0.98, (0.04+0.4*load)*pf*(0.8+0.2*press)))),
			rtSum("process.runtime.cpython.memory", "By", false,
				rtPoint(st, nowNs, rss, kvStr("type", "rss")), rtPoint(st, nowNs, rss*2.4, kvStr("type", "vms"))),
			rtSum("process.runtime.cpython.thread_count", "{thread}", false, rtPoint(st, nowNs, math.Round(4+8*load*pf))),
			rtSum("process.runtime.cpython.context_switches", "{count}", true,
				ctr("process.runtime.cpython.context_switches", (200+1800*load)*pf*rtFlushSec, kvStr("type", "voluntary")),
				ctr("process.runtime.cpython.context_switches", (20+300*load)*press*pf*rtFlushSec, kvStr("type", "involuntary"))),
		}
	}
	return nil
}

// rtIncidentPod — senaryolu k8s olay pod'u mu (k8s_incident.go; dokunulmaz).
// Simülatör kapalıyken de aynı pod atlanır (newK8sSim'in seçimi: olay
// servisinin 2. pod'u, ≥3 pod varsa) — ayrı süreçle çalışan demo da o pod'a
// runtime serisi eklemez.
func rtIncidentPod(s Service, pod string) bool {
	if sim := k8sMetrics; sim != nil && sim.incident != "" {
		return s.Name == k8sIncidentService && pod == sim.incident
	}
	return s.Name == k8sIncidentService && len(s.Pods) >= 3 && pod == s.Pods[1]
}

// runtimeResourceMetrics — her servis × pod için ayrı Resource (pod kırılımı).
// Servisler ada göre sıralı (kararlı çıktı).
func runtimeResourceMetrics(nowNs uint64, rf, lf float64) []*metricspb.ResourceMetrics {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []*metricspb.ResourceMetrics
	for _, n := range names {
		s := services[n]
		if runtimeFamilyOf(s.Lang) == "" {
			continue
		}
		for _, pod := range s.Pods {
			if rtIncidentPod(s, pod) {
				continue
			}
			mts := runtimePodMetrics(s, pod, nowNs, rf, lf)
			if len(mts) == 0 {
				continue
			}
			out = append(out, &metricspb.ResourceMetrics{
				Resource: &resourcepb.Resource{Attributes: metricResourceAttrs(s, pod)},
				ScopeMetrics: []*metricspb.ScopeMetrics{{
					Scope:   &commonpb.InstrumentationScope{Name: "coremetry-demo/runtime"},
					Metrics: mts,
				}},
			})
		}
	}
	return out
}
