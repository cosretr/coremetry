package api

// resource_jvm_test.go — v0.10.1432 (CoSRE kaynak incelemesi R2: JVM/GC/thread
// derinleşmesi ve otomatik drill kuralı). Operatör: "CoSRE sorulan sorulara
// frontier model gibi anlamlandırarak gerekli toolset'lerini koşsun … bir pod'un
// CPU/memory metriklerini grafik olarak getirme; gerekirse GC'sine, JVM
// metriklerine bakıp LLM'e yorumlatma."
//
// Altın PromQL (varyant zinciri başına: semconv / micrometer / process.runtime.*),
// sızıntı kuralı + eğim, GC payı + RuntimeAlertConfig ezmesi, drill karar
// tablosu, yönlendirme taşıması (heap → resource_investigation, "pod'ları nasıl"
// pod_health'te), çip dönüşleri, bütçe pini, önbellek anahtarı tüm girdiler,
// sahte VM koşuları (JVM var / yok / kısmi varyant / VM yok / drill tetikli /
// tetiksiz), salt-okur pini. Synthetic adlar yalnız (orders-api, kxzone).

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

var riJVMScope = resJVMScope{Service: "orders-api", SvcLabel: "service_name", PodLabel: "k8s_pod_name", By: "k8s_pod_name"}

func riJVMVariant(t *testing.T, chain, variant string) resJVMVariant {
	t.Helper()
	for _, ch := range resJVMChains() {
		if ch.ID != chain {
			continue
		}
		for _, v := range ch.Variants {
			if v.ID == variant {
				return v
			}
		}
	}
	t.Fatalf("varyant yok: %s/%s", chain, variant)
	return resJVMVariant{}
}

func TestResJVMQueryGoldens(t *testing.T) {
	cases := []struct{ chain, variant, want string }{
		{resQJVMHeap, "semconv", `topk(20, 100 * sum by (k8s_pod_name) ({__name__=~"^(jvm\\.memory\\.used|jvm_memory_used|jvm_memory_used_seconds|jvm_memory_used_milliseconds|jvm_memory_used_bytes|jvm_memory_used_ratio)$", service_name="orders-api", jvm_memory_type="heap"}) / sum by (k8s_pod_name) ({__name__=~"^(jvm\\.memory\\.limit|jvm_memory_limit|jvm_memory_limit_seconds|jvm_memory_limit_milliseconds|jvm_memory_limit_bytes|jvm_memory_limit_ratio)$", service_name="orders-api", jvm_memory_type="heap"} > 0))`},
		{resQJVMHeap, "micrometer", `topk(20, 100 * sum by (k8s_pod_name) ({__name__=~"^(jvm\\.memory\\.used|jvm_memory_used|jvm_memory_used_seconds|jvm_memory_used_milliseconds|jvm_memory_used_bytes|jvm_memory_used_ratio)$", service_name="orders-api", area="heap"}) / sum by (k8s_pod_name) ({__name__=~"^(jvm\\.memory\\.max|jvm_memory_max|jvm_memory_max_seconds|jvm_memory_max_milliseconds|jvm_memory_max_bytes|jvm_memory_max_ratio)$", service_name="orders-api", area="heap"} > 0))`},
		{resQJVMHeap, "process-runtime", `topk(20, 100 * sum by (k8s_pod_name) ({__name__=~"^(process\\.runtime\\.jvm\\.memory\\.usage|process_runtime_jvm_memory_usage|process_runtime_jvm_memory_usage_seconds|process_runtime_jvm_memory_usage_milliseconds|process_runtime_jvm_memory_usage_bytes|process_runtime_jvm_memory_usage_ratio)$", service_name="orders-api", type="heap"}) / sum by (k8s_pod_name) ({__name__=~"^(process\\.runtime\\.jvm\\.memory\\.limit|process_runtime_jvm_memory_limit|process_runtime_jvm_memory_limit_seconds|process_runtime_jvm_memory_limit_milliseconds|process_runtime_jvm_memory_limit_bytes|process_runtime_jvm_memory_limit_ratio)$", service_name="orders-api", type="heap"} > 0))`},
		{resQJVMAfterGC, "micrometer", `topk(20, 100 * sum by (k8s_pod_name) ({__name__=~"^(jvm\\.gc\\.live\\.data\\.size|jvm_gc_live_data_size|jvm_gc_live_data_size_seconds|jvm_gc_live_data_size_milliseconds|jvm_gc_live_data_size_bytes|jvm_gc_live_data_size_ratio)$", service_name="orders-api"}) / sum by (k8s_pod_name) ({__name__=~"^(jvm\\.gc\\.max\\.data\\.size|jvm_gc_max_data_size|jvm_gc_max_data_size_seconds|jvm_gc_max_data_size_milliseconds|jvm_gc_max_data_size_bytes|jvm_gc_max_data_size_ratio)$", service_name="orders-api"} > 0))`},
		{resQJVMGCShare, "semconv", `topk(20, 100 * sum by (k8s_pod_name) (rate({__name__=~"^(jvm\\.gc\\.duration_sum|jvm_gc_duration_sum|jvm_gc_duration_seconds_sum|jvm_gc_duration_milliseconds_sum|jvm_gc_duration_bytes_sum|jvm_gc_duration_ratio_sum)$", service_name="orders-api"}[120s])))`},
		{resQJVMGCShare, "micrometer", `topk(20, 100 * sum by (k8s_pod_name) (rate({__name__=~"^(jvm\\.gc\\.pause_sum|jvm_gc_pause_sum|jvm_gc_pause_seconds_sum|jvm_gc_pause_milliseconds_sum|jvm_gc_pause_bytes_sum|jvm_gc_pause_ratio_sum)$", service_name="orders-api"}[120s])))`},
		{resQJVMGCShare, "process-runtime", `topk(20, 0.1 * sum by (k8s_pod_name) (rate({__name__=~"^(process\\.runtime\\.jvm\\.gc\\.duration_sum|process_runtime_jvm_gc_duration_sum|process_runtime_jvm_gc_duration_seconds_sum|process_runtime_jvm_gc_duration_milliseconds_sum|process_runtime_jvm_gc_duration_bytes_sum|process_runtime_jvm_gc_duration_ratio_sum)$", service_name="orders-api"}[120s])))`},
		{resQJVMGCRate, "semconv", `topk(20, 60 * sum by (k8s_pod_name) (rate({__name__=~"^(jvm\\.gc\\.duration_count|jvm_gc_duration_count|jvm_gc_duration_seconds_count|jvm_gc_duration_milliseconds_count|jvm_gc_duration_bytes_count|jvm_gc_duration_ratio_count)$", service_name="orders-api"}[120s])))`},
		{resQJVMThreads, "semconv", `topk(20, sum by (k8s_pod_name) ({__name__=~"^(jvm\\.thread\\.count|jvm_thread_count)$", service_name="orders-api"}))`},
		{resQJVMThreads, "micrometer", `topk(20, sum by (k8s_pod_name) ({__name__=~"^(jvm\\.threads\\.live|jvm_threads_live|jvm_threads_live_seconds|jvm_threads_live_milliseconds|jvm_threads_live_bytes|jvm_threads_live_ratio|jvm_threads_live_threads|jvm_threads_live_threads_seconds|jvm_threads_live_threads_milliseconds|jvm_threads_live_threads_bytes|jvm_threads_live_threads_ratio)$", service_name="orders-api"}))`},
		{resQJVMThreads, "process-runtime", `topk(20, sum by (k8s_pod_name) ({__name__=~"^(process\\.runtime\\.jvm\\.threads\\.count|process_runtime_jvm_threads_count)$", service_name="orders-api"}))`},
	}
	for _, c := range cases {
		if got := buildResJVMQuery(riJVMVariant(t, c.chain, c.variant), riJVMScope, 120); got != c.want {
			t.Errorf("%s/%s:\n got %s\nwant %s", c.chain, c.variant, got, c.want)
		}
	}
	// Old gen: havuz etiketi varyanta göre; heap eşleştiricisiyle birlikte.
	for v, want := range map[string]string{
		"semconv":         `jvm_memory_type="heap", jvm_memory_pool_name=~".*(Old|Tenured).*"}`,
		"micrometer":      `area="heap", id=~".*(Old|Tenured).*"}`,
		"process-runtime": `type="heap", pool=~".*(Old|Tenured).*"}`,
	} {
		if q := buildResJVMQuery(riJVMVariant(t, resQJVMOldGen, v), riJVMScope, 120); strings.Count(q, want) != 2 {
			t.Errorf("old gen %s: %s", v, q)
		}
	}
	// Havuz: aktif / tavan (tavan > 0), pod başına en dolu havuz.
	for v, names := range map[string][2]string{
		"micrometer-executor": {"executor_active_threads", "executor_pool_max_threads"},
		"micrometer-tomcat":   {"tomcat_threads_busy_threads", "tomcat_threads_config_max_threads"},
	} {
		q := buildResJVMQuery(riJVMVariant(t, resQJVMPool, v), riJVMScope, 120)
		if !strings.HasPrefix(q, "topk(20, 100 * max by (k8s_pod_name) (") || !strings.Contains(q, names[0]) || !strings.Contains(q, names[1]) || !strings.Contains(q, `"} > 0)))`) {
			t.Errorf("havuz %s: %s", v, q)
		}
	}
	// Pod kipi: pod etiketi tam ad (kaçışlı); workload: Infrastructure regex'i.
	pod := resJVMScope{Service: "orders-api", SvcLabel: "service_name", PodLabel: "k8s_pod_name", PodRe: `orders-api-6d4f8b9c7d-x2k9q`, By: "k8s_pod_name"}
	if q := buildResJVMQuery(riJVMVariant(t, resQJVMThreads, "semconv"), pod, 60); !strings.Contains(q, `service_name="orders-api", k8s_pod_name=~"orders-api-6d4f8b9c7d-x2k9q"}`) {
		t.Errorf("pod kapsamı: %s", q)
	}
}

func TestResJVMScopeFor(t *testing.T) {
	set := map[string]bool{"service_name": true, "k8s_pod_name": true}
	sc, why := resJVMScopeFor(riRoute(resKindPod, "orders-api-6d4f8b9c7d-x2k9q", "orders-api", resFamJVM), vmetrics.LabelMap{}, set)
	if why != "" || sc.PodRe != `orders-api-6d4f8b9c7d-x2k9q` || sc.Service != "orders-api" || sc.By != "k8s_pod_name" {
		t.Errorf("pod: %+v %q", sc, why)
	}
	sc, why = resJVMScopeFor(riRoute(resKindWorkload, "orders-worker", "", resFamJVM), vmetrics.LabelMap{}, set)
	if why != "" || sc.PodRe != thanos.ServicePodRegex("orders-worker", "") || sc.Service != "" {
		t.Errorf("workload: %+v %q", sc, why)
	}
	// Servis etiketi yok → yanlış servisin JVM'i okunmaz (gerekçe).
	if _, why := resJVMScopeFor(riRoute(resKindService, "orders-api", "orders-api", resFamJVM), vmetrics.LabelMap{}, map[string]bool{"k8s_pod_name": true}); why == "" {
		t.Error("servis etiketsiz kapsam reddedilmeli")
	}
	// Pod etiketi yok → pod kipi reddedilir; servis kipi servis etiketiyle kırılır.
	if _, why := resJVMScopeFor(riRoute(resKindPod, "orders-api-6d4f8b9c7d-x2k9q", "orders-api", resFamJVM), vmetrics.LabelMap{}, map[string]bool{"service_name": true}); why == "" {
		t.Error("pod etiketsiz pod kipi reddedilmeli")
	}
	if sc, _ := resJVMScopeFor(riRoute(resKindService, "orders-api", "orders-api", resFamJVM), vmetrics.LabelMap{}, map[string]bool{"service_name": true}); sc.By != "service_name" {
		t.Errorf("pod etiketsiz kırılım servis etiketi olmalı: %+v", sc)
	}
}

func TestResJVMVariantFits(t *testing.T) {
	sem := riJVMVariant(t, resQJVMHeap, "semconv")
	if resJVMVariantFits(sem, nil) || resJVMVariantFits(sem, map[string]bool{"area": true}) || !resJVMVariantFits(sem, map[string]bool{"jvm_memory_type": true}) {
		t.Error("semconv uyumu")
	}
	old := riJVMVariant(t, resQJVMOldGen, "semconv")
	if resJVMVariantFits(old, map[string]bool{"jvm_memory_type": true}) || !resJVMVariantFits(old, map[string]bool{"jvm_memory_type": true, "jvm_memory_pool_name": true}) {
		t.Error("old gen havuz etiketi şart")
	}
	if !resJVMVariantFits(riJVMVariant(t, resQJVMThreads, "micrometer"), map[string]bool{"service_name": true}) {
		t.Error("NeedLabels'sız varyant dolu kümeyle uyar")
	}
}

func TestResLeakRule(t *testing.T) {
	cases := []struct {
		level, slope float64
		rangeS       int64
		want         bool
	}{
		{74, 8, 3600, true},
		{70, 5.1, 1800, true},
		{74, 5, 3600, false},   // eğim >5 şart (eşit değil)
		{69.9, 8, 3600, false}, // seviye ≥70 şart
		{74, 8, 1200, false},   // pencere <30 dk
		{95, -2, 3600, false},  // düşen seviye
	}
	for _, c := range cases {
		if got := resLeakSuspect(c.level, c.slope, c.rangeS); got != c.want {
			t.Errorf("seviye %v eğim %v pencere %d → %v, want %v", c.level, c.slope, c.rangeS, got, c.want)
		}
	}
	rows := map[string][]resSeries{
		resQJVMAfterGC: {
			{Labels: map[string]string{"pod": riPod}, Points: riPts(120, func(i int) float64 { return 66 + 8*float64(i)/119 })},
			{Labels: map[string]string{"pod": riPodB}, Points: riPts(120, func(int) float64 { return 40 })},
		},
	}
	by := resBuildJVMStats(rows, 3600, 10, 25)
	a, b := by[riPod], by[riPodB]
	if a == nil || b == nil || !a.Leak || b.Leak || a.AfterSlopeH < 8 || a.AfterSlopeH > 8.2 {
		t.Fatalf("eğim/sızıntı: %+v %+v", a, b)
	}
	sum := renderResourceSummaryTR(resMergeJVM(nil, by), time.UTC)
	if strings.Count(sum, "SIZINTI ŞÜPHESİ") != 1 || strings.Count(sum, "sızıntı şüphesi (kural: eğim >%5/sa VE seviye ≥%70)") != 1 {
		t.Errorf("sızıntı yalnız kural ateşleyen pod'da:\n%s", sum)
	}
	if !strings.Contains(sum, "after-GC son %74, eğim +8.1 %/sa (SIZINTI ŞÜPHESİ)") || !strings.Contains(sum, "after-GC son %40, eğim +0.0 %/sa") {
		t.Errorf("after-GC satırları:\n%s", sum)
	}
	// Kısa pencere: kural uygulanmaz ve bu söylenir.
	short := resBuildJVMStats(rows, 1200, 10, 25)
	if short[riPod].Leak || !strings.Contains(resJVMLineTR(*short[riPod], time.UTC), "pencere <30 dk — sızıntı kuralı uygulanmadı") {
		t.Errorf("kısa pencere: %+v", short[riPod])
	}
}

func TestResGCThresholdsOverride(t *testing.T) {
	if w, c := resGCThresholds(nil); w != 10 || c != 25 {
		t.Errorf("varsayılan (chstore.DefaultRuntimeAlerts) %v/%v", w, c)
	}
	if w, c := resGCThresholds(&chstore.RuntimeAlertConfig{GCShareWarnPct: 5, GCShareCritPct: 11}); w != 5 || c != 11 {
		t.Errorf("ezme %v/%v", w, c)
	}
	if w, c := resGCThresholds(&chstore.RuntimeAlertConfig{GCShareWarnPct: 30, GCShareCritPct: 20}); w != 30 || c != 60 {
		t.Errorf("ters eşik düzeltilmeli %v/%v", w, c)
	}
	gc := func(avg float64, warn, crit float64) []resBreach {
		j := resJVMStats{HasGC: true, GC: metricChartSeriesStats{Avg: avg, Max: avg + 3}, GCWarn: warn, GCCrit: crit}
		return resJVMBreaches(riPod, j)
	}
	if b := gc(12, 10, 25); len(b) != 1 || b[0].Crit || !strings.Contains(b[0].Text, "GC süresi payı ort %12 (tepe %15; eşik %10 uyarı / %25 kritik — RuntimeAlertConfig)") {
		t.Errorf("varsayılan uyarı: %+v", b)
	}
	if b := gc(12, 15, 30); len(b) != 0 {
		t.Errorf("operatör eşiği 15 → aşım yok: %+v", b)
	}
	if b := gc(12, 5, 11); len(b) != 1 || !b[0].Crit {
		t.Errorf("operatör eşiği 5/11 → kritik: %+v", b)
	}
	// Heap ortalamaya, havuz tepeye bakar.
	j := resJVMStats{HasHeap: true, Heap: metricChartSeriesStats{Avg: 91, Max: 98}, HasPool: true, Pool: metricChartSeriesStats{Max: 92, Last: 40}}
	b := resJVMBreaches(riPod, j)
	if len(b) != 2 || !b[0].Crit || b[1].Crit || !strings.Contains(b[1].Text, "thread havuzu doluluğu tepe %92 (son %40; eşik %90)") {
		t.Errorf("heap/havuz aşımları: %+v", b)
	}
	if b := resJVMBreaches(riPod, resJVMStats{HasHeap: true, Heap: metricChartSeriesStats{Avg: 70, Max: 99}}); len(b) != 0 {
		t.Errorf("heap tepesi tek başına aşım değil (testere dişi): %+v", b)
	}
}

func TestDecideResJVMDrill(t *testing.T) {
	cases := []struct {
		name string
		in   resDrillInput
		want bool
		why  string
	}{
		{"JVM yok", resDrillInput{JVMPresent: false, MemPctMax: 95, Restarts: 3, OOMKilled: true, Ask: resDrillAskSlow}, false, "JVM metriği yok"},
		{"sakin", resDrillInput{JVMPresent: true, MemPctMax: 40}, false, "drill koşulu yok"},
		{"limit yok", resDrillInput{JVMPresent: true, MemPctMax: -1}, false, "drill koşulu yok"},
		{"bellek 80", resDrillInput{JVMPresent: true, MemPctMax: 80}, true, "bellek limitin %80'i ≥ %80"},
		{"bellek 79.4", resDrillInput{JVMPresent: true, MemPctMax: 79.4}, false, "drill koşulu yok"},
		{"restart", resDrillInput{JVMPresent: true, MemPctMax: 10, Restarts: 1}, true, "pencerede 1 restart"},
		{"OOMKilled", resDrillInput{JVMPresent: true, OOMKilled: true, MemPctMax: -1}, true, "son sonlanma OOMKilled"},
		{"yavaş", resDrillInput{JVMPresent: true, MemPctMax: -1, Ask: resDrillAskSlow}, true, "soruda yavaşlık/latency"},
		{"jvm sorusu", resDrillInput{JVMPresent: true, MemPctMax: -1, Ask: resDrillAskJVM}, true, "soruda JVM/heap/GC"},
		{"çoklu", resDrillInput{JVMPresent: true, MemPctMax: 88, Restarts: 2, OOMKilled: true}, true, "bellek limitin %88'i ≥ %80, pencerede 2 restart, son sonlanma OOMKilled"},
	}
	for _, c := range cases {
		got, why := decideResJVMDrill(c.in)
		if got != c.want || !strings.Contains(why, c.why) {
			t.Errorf("%s: %v %q, want %v %q", c.name, got, why, c.want, c.why)
		}
	}
	in := resDrillFromStats(resBuildStats(riResults(), 30), true, "")
	if in.MemPctMax < 87 || in.Restarts != 2 || !in.OOMKilled {
		t.Errorf("özetten girdi: %+v", in)
	}
}

// Yönlendirme: JVM sözcükleri pod_health'ten resource_investigation'a (D2 ikinci eki).
func TestRouteResourceJVM(t *testing.T) {
	ri := guidedResourceInvestigation
	cases := []struct {
		msg, ctx string
		intent   guidedIntent
		kind     string
		target   string
		family   string
		ask      string
	}{
		{"orders-api heap kullanımı nasıl", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api gc durumu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api old gen doluluğu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api metaspace büyüyor mu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api jvm durumu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api thread havuzu dolu mu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api tomcat thread'leri tükeniyor mu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"orders-api executor doluluğu", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{"is there a heap leak in orders-api", "", ri, resKindService, "orders-api", resFamJVM, ""},
		{riPod + " heap durumu", "", ri, resKindPod, riPod, resFamJVM, ""},
		{riPod + " GC durumu", "", ri, resKindPod, riPod, resFamJVM, ""},
		{"deployment orders-worker heap durumu", "", ri, resKindWorkload, "orders-worker", resFamJVM, ""},
		{"heap durumu nasıl", "orders-api", ri, resKindService, "orders-api", resFamJVM, ""},
		// JVM + başka kaynak sözcüğü → all + JVM drill isteği.
		{"orders-api bellek sızıntısı var mı", "", ri, resKindService, "orders-api", resFamAll, resDrillAskJVM},
		{"orders-api memory leak var mı", "", ri, resKindService, "orders-api", resFamAll, resDrillAskJVM},
		{"orders-api heap yüzünden restart ediyor mu", "", ri, resKindService, "orders-api", resFamAll, resDrillAskJVM},
		// Yavaşlık → drill isteği "slow".
		{"orders-api yavaşladı, cpu kullanımı nasıl", "", ri, resKindService, "orders-api", resFamCPU, resDrillAskSlow},
		{"orders-api latency yüksek, bellek durumu", "", ri, resKindService, "orders-api", resFamMemory, resDrillAskSlow},
		// pod_health envanteri + hedefsiz filo heap sıralaması korunur.
		{"orders-api pod'ları nasıl", "", guidedPodHealth, "", "", "", ""},
		{"hangi podun heap kullanımı yüksek", "", guidedPodHealth, "", "", "", ""},
		{"jvm heap durumu nasıl", "", guidedPodHealth, "", "", "", ""},
		{"gc süreleri arttı mı", "", guidedPodHealth, "", "", "", ""},
		// Grafik fiili metric_chart'ta (1386 D2), DB havuzu db_health'te.
		{"orders-api heap grafiği", "", guidedMetricChart, "", "", "", ""},
		{"orders-api gc grafiği", "", guidedMetricChart, "", "", "", ""},
		{"orders-api thread sayısı grafiği", "", guidedMetricChart, "", "", "", ""},
		{"orders-api db bağlantı havuzu nasıl", "", guidedDBHealth, "", "", "", ""},
	}
	for _, c := range cases {
		r := routeGuidedIntent(c.msg, riServices, riEnvs, nil, c.ctx)
		if r.Intent != c.intent {
			t.Errorf("%q → %s, want %s", c.msg, r.Intent, c.intent)
			continue
		}
		if c.intent != ri {
			continue
		}
		if r.ResKind != c.kind || r.ResTarget != c.target || r.ResFamily != c.family || r.ResDrillAsk != c.ask {
			t.Errorf("%q → kind=%q target=%q fam=%q ask=%q; want %+v", c.msg, r.ResKind, r.ResTarget, r.ResFamily, r.ResDrillAsk, c)
		}
		if !hasGuidedSignal(normalizeGuidedMsg(c.msg)) {
			t.Errorf("%q hasGuidedSignal false", c.msg)
		}
	}
	for _, msg := range []string{"gcp maliyeti", "eski generation", "orders-api neden yavaş"} {
		if r := routeGuidedIntent(msg, riServices, riEnvs, nil, ""); r.Intent == ri {
			t.Errorf("%q kaynak incelemesine düşmemeli: %+v", msg, r)
		}
	}
}

// Çipler: jvm ailesi kendi başına geri döner; "GC/heap'e bak" jvm ailesine;
// jvm ailesinde "CPU/bellek/restart" all ailesine; ask çipleri drill isteğini taşır.
func TestResJVMChipsRoute(t *testing.T) {
	for _, target := range []string{"orders-api", riPod} {
		r := routeGuidedIntent(resChipText(target, resFamJVM, ""), riServices, riEnvs, nil, "")
		if r.Intent != guidedResourceInvestigation || r.ResFamily != resFamJVM || r.ResTarget != target || r.ResDrillAsk != "" {
			t.Errorf("jvm çipi %q → %+v", target, r)
		}
	}
	for _, c := range []struct {
		route guidedRoute
		fam   string
		ask   string
	}{
		{guidedRoute{ResFamily: resFamAll, ResDrillAsk: resDrillAskJVM}, resFamAll, resDrillAskJVM},
		{guidedRoute{ResFamily: resFamCPU, ResDrillAsk: resDrillAskSlow}, resFamCPU, resDrillAskSlow},
		{guidedRoute{ResFamily: resFamMemory, ResDrillAsk: resDrillAskSlow, ResCluster: "kxzone"}, resFamMemory, resDrillAskSlow},
		{guidedRoute{ResFamily: resFamJVM}, resFamJVM, ""},
	} {
		chip := resAskChipText(c.route, "orders-api")
		r := routeGuidedIntent(chip, riServices, riEnvs, nil, "")
		if r.Intent != guidedResourceInvestigation || r.ResFamily != c.fam || r.ResDrillAsk != c.ask || r.ResCluster != c.route.ResCluster {
			t.Errorf("ask çipi %q → %+v", chip, r)
		}
	}
	ask := routeGuidedIntent("api heap durumu nasıl", riServices, riEnvs, nil, "")
	if ask.Intent != guidedAskService || ask.ResFamily != resFamJVM {
		t.Fatalf("çok aday jvm: %+v", ask)
	}
	for _, c := range guidedSuggestions(ask) {
		if r := routeGuidedIntent(c, riServices, riEnvs, nil, ""); r.Intent != guidedResourceInvestigation || r.ResFamily != resFamJVM {
			t.Errorf("ask çipi %q → %+v", c, r)
		}
	}
	// Bellek sorusu, JVM var ama drill koşmadı → "GC/heap'e bak" jvm ailesine (pod kipinde pod'a).
	route := guidedRoute{Intent: guidedResourceInvestigation, Service: "orders-api", ResTarget: riPod, ResKind: resKindPod, ResFamily: resFamMemory, ResJVM: true}
	var kinds []string
	for _, f := range guidedFollowups(route, nil) {
		kinds = append(kinds, f.Kind)
		if f.Kind == fuKindResJVM {
			r := routeGuidedIntent(f.Text, riServices, riEnvs, nil, "")
			if r.Intent != guidedResourceInvestigation || r.ResFamily != resFamJVM || r.ResTarget != riPod || r.Service != "orders-api" {
				t.Errorf("GC/heap çipi %q → %+v", f.Text, r)
			}
		}
	}
	if !strings.Contains(strings.Join(kinds, ","), fuKindResJVM) {
		t.Errorf("GC/heap çipi yok: %v", kinds)
	}
	// Drill koştuysa (ResJVM false) çip yok; jvm ailesinde CPU/bellek/restart çipi.
	route.ResJVM, route.ResFamily, route.ResKind, route.ResTarget = false, resFamJVM, resKindService, "orders-api"
	kinds = nil
	for _, f := range guidedFollowups(route, nil) {
		kinds = append(kinds, f.Kind)
		if f.Kind == fuKindResAll {
			r := routeGuidedIntent(f.Text, riServices, riEnvs, nil, "")
			if f.Label != "CPU/bellek/restart" || r.Intent != guidedResourceInvestigation || r.ResFamily != resFamAll || r.ResTarget != "orders-api" {
				t.Errorf("CPU/bellek/restart çipi %q → %+v", f.Text, r)
			}
		}
	}
	if strings.Contains(strings.Join(kinds, ","), fuKindResJVM) || !strings.Contains(strings.Join(kinds, ","), fuKindResAll) {
		t.Errorf("jvm ailesi takipleri: %v", kinds)
	}
	// Link etiketi kaynağa göre.
	route.ResHref = "/metrics?editor=1&src=vm&tab=promql"
	if l := resourceLinks(route); l[0].Label != "Metrics'te aç (VictoriaMetrics)" {
		t.Errorf("VM link etiketi: %+v", l)
	}
	route.ResHref = "/metrics?editor=1&src=thanos&tab=promql"
	if l := resourceLinks(route); l[0].Label != "Metrics'te aç (Thanos)" {
		t.Errorf("Thanos link etiketi: %+v", l)
	}
}

func TestResJVMBudgetAndCacheKey(t *testing.T) {
	chains := resJVMChains()
	if len(chains) != 7 || resJVMQueryCap != 9 || resJVMTriesPerChain != 2 {
		t.Fatalf("bütçe: %d zincir, tavan %d, deneme %d", len(chains), resJVMQueryCap, resJVMTriesPerChain)
	}
	// all + drill = Thanos ≤7 + VM ≤9.
	if n := len(resourceQueries(resFamAll, "x-.*", "", 60)); n+resJVMQueryCap > 16 {
		t.Errorf("all + drill bütçesi %d > 16", n+resJVMQueryCap)
	}
	charts := 0
	for _, ch := range chains {
		if ch.Chart {
			charts++
		}
		if len(ch.Variants) == 0 {
			t.Errorf("%s varyantsız", ch.ID)
		}
	}
	if charts != 5 {
		t.Errorf("grafikli zincir %d, want 5 (heap, after-GC, GC payı, thread, havuz)", charts)
	}
	from := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := resJVMCacheKey(resQJVMHeap, "semconv", "q", riJVMScope, from, to, 30)
	alt := func(f func(*resJVMScope)) resJVMScope { s := riJVMScope; f(&s); return s }
	for name, k := range map[string]string{
		"zincir":     resJVMCacheKey(resQJVMAfterGC, "semconv", "q", riJVMScope, from, to, 30),
		"varyant":    resJVMCacheKey(resQJVMHeap, "micrometer", "q", riJVMScope, from, to, 30),
		"sorgu":      resJVMCacheKey(resQJVMHeap, "semconv", "q2", riJVMScope, from, to, 30),
		"servis":     resJVMCacheKey(resQJVMHeap, "semconv", "q", alt(func(s *resJVMScope) { s.Service = "payments-api" }), from, to, 30),
		"svc etiket": resJVMCacheKey(resQJVMHeap, "semconv", "q", alt(func(s *resJVMScope) { s.SvcLabel = "app" }), from, to, 30),
		"pod re":     resJVMCacheKey(resQJVMHeap, "semconv", "q", alt(func(s *resJVMScope) { s.PodRe = "x" }), from, to, 30),
		"pod etiket": resJVMCacheKey(resQJVMHeap, "semconv", "q", alt(func(s *resJVMScope) { s.PodLabel = "pod" }), from, to, 30),
		"kırılım":    resJVMCacheKey(resQJVMHeap, "semconv", "q", alt(func(s *resJVMScope) { s.By = "pod" }), from, to, 30),
		"başlangıç":  resJVMCacheKey(resQJVMHeap, "semconv", "q", riJVMScope, from.Add(-time.Hour), to, 30),
		"bitiş":      resJVMCacheKey(resQJVMHeap, "semconv", "q", riJVMScope, from, to.Add(time.Minute), 30),
		"adım":       resJVMCacheKey(resQJVMHeap, "semconv", "q", riJVMScope, from, to, 60),
	} {
		if k == base {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	if !strings.HasPrefix(base, "cosre-res-jvm:v1:") {
		t.Error("anahtar öneki")
	}
}

// ─── Sahte VictoriaMetrics ──────────────────────────────────────────────

type fakeJVMReader struct {
	*fakeChartReader
	answer func(q string) []chstore.SpanMetricSeries
	jmu    sync.Mutex
	jq     []string
}

func (f *fakeJVMReader) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	f.jmu.Lock()
	f.jq = append(f.jq, q)
	f.jmu.Unlock()
	if f.answer == nil {
		return nil, nil
	}
	return f.answer(q), nil
}

func (f *fakeJVMReader) queries() []string {
	f.jmu.Lock()
	defer f.jmu.Unlock()
	return append([]string(nil), f.jq...)
}

func riJVMSeries(pod string, fn func(i int) float64) chstore.SpanMetricSeries {
	s := chstore.SpanMetricSeries{GroupKey: []string{`k8s_pod_name="` + pod + `"`, `service_name="orders-api"`}}
	for _, p := range riPts(120, fn) {
		s.Points = append(s.Points, chstore.SpanMetricPoint{Time: int64(p[0]) * 1e6, Value: p[1]})
	}
	return s
}

func riConst(v float64) func(int) float64 { return func(int) float64 { return v } }

// riSemconvAnswer — semconv kurulumu: sorunlu pod (x2k9q) + sağlıklı pod (b7m4p).
func riSemconvAnswer(q string) []chstore.SpanMetricSeries {
	two := func(a, b func(int) float64) []chstore.SpanMetricSeries {
		return []chstore.SpanMetricSeries{riJVMSeries(riPod, a), riJVMSeries(riPodB, b)}
	}
	switch {
	case strings.Contains(q, "used_after_last_gc"):
		return two(func(i int) float64 { return 66 + 8*float64(i)/119 }, riConst(40))
	case strings.Contains(q, "jvm_memory_pool_name=~"):
		return two(riConst(60), riConst(30))
	case strings.Contains(q, "jvm_memory_used|"):
		return two(riConst(85), riConst(50))
	case strings.Contains(q, "duration_sum"):
		return two(riConst(12), riConst(1))
	case strings.Contains(q, "duration_count"):
		return two(riConst(4), riConst(1))
	case strings.Contains(q, "jvm_thread_count"):
		return two(riConst(200), riConst(80))
	case strings.Contains(q, "executor_active"):
		return two(func(i int) float64 { return 50 + 45*float64(i%60)/59 }, riConst(10))
	}
	return nil
}

var riSemconvLabels = map[string][]string{
	"jvm.memory.used":               {"service_name", "k8s_pod_name", "jvm_memory_type", "jvm_memory_pool_name"},
	"jvm.memory.used_after_last_gc": {"service_name", "k8s_pod_name", "jvm_memory_type", "jvm_memory_pool_name"},
	"jvm.gc.duration":               {"service_name", "k8s_pod_name", "jvm_gc_name", "jvm_gc_action"},
	"jvm.thread.count":              {"service_name", "k8s_pod_name", "jvm_thread_state"},
	"executor_active_threads":       {"service_name", "k8s_pod_name", "name"},
}

func TestResourceJVMRunSemconv(t *testing.T) {
	vm := &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: riSemconvLabels}, answer: riSemconvAnswer}
	s := &Server{cache: newMemCache()}
	ev := &mcEvents{}
	route := riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	text, src, err := s.resourceRun(context.Background(), ev.emit(), route, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC)
	if err != nil || route.DirectAnswer != "" {
		t.Fatalf("err=%v direct=%q", err, route.DirectAnswer)
	}
	qs := vm.queries()
	if len(qs) != 7 || len(qs) > resJVMQueryCap {
		t.Fatalf("7 zincir = 7 sorgu bekleniyordu: %d\n%s", len(qs), strings.Join(qs, "\n"))
	}
	for _, q := range qs {
		if !strings.HasPrefix(q, "topk(20, ") || !strings.Contains(q, `service_name="orders-api"`) || strings.Contains(q, "area=") {
			t.Errorf("sorgu: %s", q)
		}
	}
	if len(ev.blocks) != 5 || !strings.Contains(route.ResHref, "src=vm") {
		t.Errorf("5 grafik (heap, after-GC, GC payı, thread, havuz) + VM linki: %d %s", len(ev.blocks), route.ResHref)
	}
	if route.ResJVM {
		t.Error("JVM okunduysa GC/heap çipi sunulmaz")
	}
	for _, w := range []string{
		"KAYNAK İNCELEMESİ (orders-api · servis pod'ları · JVM heap/GC/thread; son 1sa; kaynak VictoriaMetrics — JVM metrikleri",
		"KAYNAK ÖZETİ (deterministik",
		"- " + riPod + ": JVM heap ort limitin %85'i",
		"after-GC son %74, eğim +8.1 %/sa (SIZINTI ŞÜPHESİ)",
		"GC payı ort %12 (tepe %12)", "GC 4.0/dk", "old gen son limitin %60'i", "thread son 200 (tepe 200)", "thread havuzu tepe %95",
		"EŞİK AŞIMI (eşikler:", "JVM heap ort limitin %80/%90'i, GC payı %10/%25, havuz %90, sızıntı eğim >%5/sa ve ≥%70",
		"- UYARI " + riPod + ": JVM heap ort limitin %85'i",
		"- UYARI " + riPod + ": heap after-GC son limitin %74'i, eğim +8.1 %/sa → sızıntı şüphesi",
		"- UYARI " + riPod + ": GC süresi payı ort %12",
		"- UYARI " + riPod + ": thread havuzu doluluğu tepe %95",
		"JVM/GC: OKUNDU — VictoriaMetrics, kapsam service_name=\"orders-api\" (cluster ayrımı yok), 7 sorgu (≤9), varyantlar: jvm_heap=semconv, jvm_after_gc=semconv, jvm_gc_share=semconv, jvm_gc_rate=semconv, jvm_old_gen=semconv, jvm_threads=semconv, jvm_pool=micrometer-executor.",
		"Konteyner CPU/bellek/restart (Thanos) bu cevapta okunmadı",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("kanıt %q taşımalı:\n%s", w, text)
		}
	}
	if strings.Contains(text, "- UYARI "+riPodB) || strings.Contains(text, "KRİTİK "+riPodB) {
		t.Errorf("sağlıklı pod aşım üretti:\n%s", text)
	}
	if !strings.Contains(src, "VictoriaMetrics JVM") {
		t.Errorf("kaynak: %s", src)
	}
	// Önbellek: aynı girdiyle ikinci koşu VM'e sorgu atmaz.
	if _, _, err := s.resourceRun(context.Background(), (&mcEvents{}).emit(), riRoute(resKindService, "orders-api", "orders-api", resFamJVM),
		resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if n := len(vm.queries()); n != 7 {
		t.Errorf("önbellek ıskaladı: %d sorgu", n)
	}
	// Operatörün GC eşiği (RuntimeAlertConfig) canlı uygulanır.
	route = riRoute(resKindPod, riPod, "orders-api", resFamJVM)
	text, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route,
		resourceDeps{VM: vm, IsVM: true, Runtime: &chstore.RuntimeAlertConfig{GCShareWarnPct: 5, GCShareCritPct: 11}}, riTo, 3600, time.UTC)
	if !strings.Contains(text, "- KRİTİK "+riPod+": GC süresi payı ort %12 (tepe %12; eşik %5 uyarı / %11 kritik — RuntimeAlertConfig)") {
		t.Errorf("GC eşik ezmesi:\n%s", text)
	}
	last := vm.queries()[len(vm.queries())-1]
	if !strings.Contains(last, `k8s_pod_name=~"orders-api-6d4f8b9c7d-x2k9q"`) {
		t.Errorf("pod kipi pod etiketiyle süzmeli: %s", last)
	}
}

func TestResourceJVMRunPartialAndAbsent(t *testing.T) {
	// Karma filo: küme semconv + micrometer etiketleri taşıyor; hedef micrometer
	// → semconv heap boş döner, micrometer yedeği koşar. Thread yalnız
	// micrometer'ın Prometheus adında; GC/after-GC/havuz yok.
	vm := &fakeJVMReader{
		fakeChartReader: &fakeChartReader{labels: map[string][]string{
			"jvm.memory.used":          {"service_name", "k8s_pod_name", "jvm_memory_type", "area"},
			"jvm_threads_live_threads": {"service_name", "k8s_pod_name", "state"},
		}},
		answer: func(q string) []chstore.SpanMetricSeries {
			switch {
			case strings.Contains(q, `area="heap"`) && !strings.Contains(q, "id=~"):
				return []chstore.SpanMetricSeries{riJVMSeries(riPod, riConst(45))}
			case strings.Contains(q, "jvm_threads_live_threads"):
				return []chstore.SpanMetricSeries{riJVMSeries(riPod, riConst(150))}
			}
			return nil
		},
	}
	route := riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	text, _, _ := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC)
	qs := vm.queries()
	if len(qs) != 3 || !strings.Contains(qs[0]+qs[1]+qs[2], `jvm_memory_type="heap"`) {
		t.Fatalf("heap semconv (boş) + micrometer yedeği + thread = 3 sorgu: %d\n%s", len(qs), strings.Join(qs, "\n"))
	}
	for _, w := range []string{"varyantlar: jvm_heap=micrometer, jvm_threads=micrometer.", "JVM heap ort limitin %45'i", "thread son 150",
		"Not (JVM): jvm_gc_share: metrik yok.", "Not (JVM): jvm_after_gc: metrik yok.", "EŞİK AŞIMI: yok ("} {
		if !strings.Contains(text, w) {
			t.Errorf("kısmi varyant %q taşımalı:\n%s", w, text)
		}
	}
	// JVM metriği yok → dürüst cevap, anlatım yok, sorgu yok.
	vm = &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: map[string][]string{}}}
	route = riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC)
	if !strings.Contains(route.DirectAnswer, "VictoriaMetrics'te JVM metriği yok (son 24 sa; denenen yazımlar: jvm.memory.used, jvm_memory_used") || len(vm.queries()) != 0 {
		t.Errorf("JVM yok: %q %v", route.DirectAnswer, vm.queries())
	}
	// VM yok → dürüst "yapılandırılmamış".
	route = riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{}, riTo, 3600, time.UTC)
	if !strings.Contains(route.DirectAnswer, "VictoriaMetrics yapılandırılmamış — JVM/GC metrikleri OKUNAMADI (yok demek değil).") {
		t.Errorf("VM yok: %q", route.DirectAnswer)
	}
	// Servis etiketi yok → yanlış servisin JVM'i okunmaz.
	vm = &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: map[string][]string{"jvm.memory.used": {"jvm_memory_type", "k8s_pod_name"}}}}
	route = riRoute(resKindService, "orders-api", "orders-api", resFamJVM)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC)
	if !strings.Contains(route.DirectAnswer, "servis etiketi taşımıyor") || len(vm.queries()) != 0 {
		t.Errorf("servis etiketsiz: %q", route.DirectAnswer)
	}
}

// Drill: Thanos demeti sakin → JVM koşmaz (çip); yavaşlık sorusu → koşar.
func TestResourceJVMDrillGate(t *testing.T) {
	const mib = 1 << 20
	calm := map[string][]resSeries{resQMem: {{Labels: riLabels(riPodB), Points: riPts(120, riConst(200*mib))}}}
	run := func(ask string) (*guidedRoute, string, *fakeJVMReader) {
		th := &fakeResThanos{pods: map[string][]string{"kxzone": {riPodB}}, results: calm}
		vm := &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: riSemconvLabels}, answer: riSemconvAnswer}
		route := riRoute(resKindService, "orders-api", "orders-api", resFamMemory)
		route.ResDrillAsk = ask
		d := resourceDeps{Thanos: th, Clusters: riClusters[:1], VM: vm, IsVM: true}
		text, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, d, riTo, 3600, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return route, text, vm
	}
	route, text, vm := run("")
	if len(vm.queries()) != 0 || !route.ResJVM || !strings.Contains(text, "JVM/GC: bu cevapta OKUNMADI (drill koşulu yok: bellek <%80, restart yok, OOMKilled yok, yavaşlık/JVM sorulmadı;") {
		t.Errorf("tetiksiz: %d sorgu, ResJVM=%v\n%s", len(vm.queries()), route.ResJVM, text)
	}
	route, text, vm = run(resDrillAskSlow)
	if len(vm.queries()) != 7 || route.ResJVM || !strings.Contains(text, "JVM drill tetiklendi (soruda yavaşlık/latency).") ||
		!strings.Contains(text, "- "+riPod+": JVM heap ort limitin %85'i") || !strings.Contains(text, "JVM/GC: OKUNDU") {
		t.Errorf("yavaşlık drill'i: %d sorgu\n%s", len(vm.queries()), text)
	}
	// Thanos'ta görünen pod'un satırına JVM eklenir (aynı ad), diğeri ayrı satır.
	if !strings.Contains(text, "- "+riPodB+": bellek son 200 MiB") || !strings.Contains(text, "JVM heap ort limitin %50'i") {
		t.Errorf("pod birleştirme:\n%s", text)
	}
	// Thanos yok + yavaşlık sorusu → process şablonları + JVM drill.
	vm2 := &fakeJVMReader{fakeChartReader: &fakeChartReader{labels: riSemconvLabels}, answer: riSemconvAnswer}
	r := riRoute(resKindService, "orders-api", "orders-api", resFamCPU)
	r.ResDrillAsk = resDrillAskSlow
	text, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, resourceDeps{VM: vm2, IsVM: true}, riTo, 3600, time.UTC)
	if !strings.Contains(text, "Thanos (cAdvisor + kube-state-metrics) yapılandırılmamış") || !strings.Contains(text, "JVM drill tetiklendi (soruda yavaşlık/latency).") {
		t.Errorf("Thanos'suz drill:\n%s", text)
	}
}

// Salt-okur + tek çağrı yeri + bütçe sabitleri kaynakta.
func TestResourceJVMSourcePins(t *testing.T) {
	src := stripGoCommentsAPI(readSourceFile(t, "resource_jvm.go"))
	for w, n := range map[string]int{".QueryPromQLRange(": 1, "p.s.metricLabelSetFor(ctx, p.rd, name, p.svc, p.to)": 1, "s.metricChartLabelSet(": 0, "s.store.GetRuntimeAlerts(ctx)": 1} {
		if got := strings.Count(src, w); got != n {
			t.Errorf("%s çağrı yeri %d, want %d", w, got, n)
		}
	}
	for _, w := range []string{"context.WithTimeout(ctx, resQueryTimeout)", "make(chan struct{}, resParallel)", "resJVMCacheKey(chain, variant, q, sc, from, to, step)",
		"atomic.AddInt32(&used, 1) > resJVMQueryCap", "vmetrics.NameCandidates(kind, names...)", "vmetrics.ResolveLabelRole(vmetrics.RolePod"} {
		if !strings.Contains(src, w) {
			t.Errorf("resource_jvm.go %q taşımalı", w)
		}
	}
}
