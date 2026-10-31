package api

// resource_jvm.go — v0.10.1432 (CoSRE kaynak incelemesi R2: JVM/GC/thread
// derinleşmesi). Operatör: "CoSRE sorulan sorulara frontier model gibi
// anlamlandırarak gerekli toolset'lerini koşsun … bir pod'un CPU/memory
// metriklerini grafik olarak getirme; gerekirse GC'sine, JVM metriklerine
// bakıp LLM'e yorumlatma."
//
// SAF çekirdek + sınırlı VictoriaMetrics okuyucusu (metricChartReader —
// metric_chart'ın okuyucusu; Thanos'a gitmez). Satır zincirleri (her biri
// sıralı VARYANTLAR; metrik adı ELLE yazılmaz — vmetrics.NameCandidates):
//
//	jvm_heap      heap kullanımı, limitin yüzdesi
//	              semconv jvm.memory.used / jvm.memory.limit {jvm_memory_type="heap"}
//	              micrometer jvm.memory.used / jvm.memory.max {area="heap"}
//	              semconv-type jvm.memory.used / jvm.memory.limit {type="heap"}
//	              process-runtime process.runtime.jvm.memory.usage / .limit {type="heap"}
//	jvm_after_gc  son GC sonrası heap, limitin yüzdesi → en küçük kareler eğimi (%/sa)
//	              semconv jvm.memory.used_after_last_gc · semconv-type · process-runtime
//	              …usage_after_last_gc · micrometer jvm.gc.live.data.size / jvm.gc.max.data.size
//	jvm_gc_share  GC'de geçen duvar saati payı % = 100 × rate(_sum) (semconv jvm.gc.duration,
//	              micrometer jvm.gc.pause; process-runtime jvm.gc.duration ms → 0.1 ×)
//	jvm_gc_rate   GC sıklığı /dk = 60 × rate(_count) (aynı varyantlar)
//	jvm_old_gen   old gen havuzu (havuz adı =~ ".*(Old|Tenured).*"), limitin yüzdesi
//	              (havuz etiketi: jvm_memory_pool_name | id | pool)
//	jvm_threads   canlı thread sayısı (jvm.thread.count · jvm.threads.count ·
//	              micrometer jvm_threads_live_threads · process.runtime.jvm.threads.count)
//	jvm_pool      thread havuzu doluluğu % = 100 × max(aktif / tavan) (micrometer
//	              executor_active_threads / executor_pool_max_threads, sonra
//	              tomcat_threads_busy_threads / tomcat_threads_config_max_threads)
//
// Varyant seçimi: Probe metriğinin son 24 sa etiket kümesi (metric_chart'ın 5 dk
// önbellekli /api/v1/labels okuması) boş değilse VE NeedLabels kümede ise uyar;
// ilk uyan koşar. Küme filo genelidir — karma filoda (bir servis semconv, öteki
// micrometer) seçilen varyant hedef için SIFIR seri dönerse sıradaki uyan
// varyant BİR kez denenir (zincir başına ≤2 deneme, toplam ≤resJVMQueryCap).
// Kapsam: servis kipinde servis etiketi (service_name / LabelMap / keşif), pod
// kipinde pod etiketi (vmetrics.ResolveLabelRole(RolePod)) tam ad, workload
// kipinde pod etiketi Infrastructure regex'i. Kırılım pod etiketi (yoksa servis
// etiketi). Her sorgu topk(20), adım ⌈aralık/120⌉, rate penceresi Thanos ile
// aynı; tüm JVM aşaması TEK 8 sn son tarih (etiket yoklamaları dâhil), ≤4
// paralel; her sorgu 60 sn önbellekli, anahtar TÜM girdiler (seçilen varyant,
// zincir, son PromQL, kapsam, kırılım, uçlar, adım).
//
// Eşikler resource_bundle.go'nun TEK resourceThresholds tablosunda: heap ort.
// limitin %80/%90'ı, sızıntı şüphesi (after-GC eğimi >%5/sa VE son seviye ≥%70,
// pencere ≥30 dk), GC payı chstore.RuntimeAlertConfig (varsayılan %10/%25;
// operatör ayarı canlı okunur), havuz doluluğu tepe ≥%90.

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// JVM satır kimlikleri (zincirler).
const (
	resQJVMHeap    = "jvm_heap"
	resQJVMAfterGC = "jvm_after_gc"
	resQJVMGCShare = "jvm_gc_share"
	resQJVMGCRate  = "jvm_gc_rate"
	resQJVMOldGen  = "jvm_old_gen"
	resQJVMThreads = "jvm_threads"
	resQJVMPool    = "jvm_pool"
)

// Bütçeler (v0.10.1432).
const (
	// resJVMQueryCap — JVM aşamasının VM range sorgusu tavanı: 7 zincir + en
	// çok 2 karma-filo yedeği. all + drill = Thanos ≤7 + VM ≤9.
	resJVMQueryCap = 9
	// resJVMTriesPerChain — zincir başına deneme (ilk uyan + bir yedek).
	resJVMTriesPerChain = 2
	// resLeakMinRangeS — sızıntı kuralı en az 30 dk'lık pencerede uygulanır
	// (5 dk'da 1 puanlık kıpırtı saatte 12 puan görünür).
	resLeakMinRangeS = 1800
)

// resOldGenPoolRe — old gen havuz adı (G1 Old Gen, PS Old Gen, CMS Old Gen, Tenured Gen).
const resOldGenPoolRe = ".*(Old|Tenured).*"

// resJVMVariant — bir zincirin tek metrik ailesi.
type resJVMVariant struct {
	ID         string   // semconv | micrometer | semconv-type | process-runtime | micrometer-executor | micrometer-tomcat
	Probes     []string // etiket yoklaması: ilk dolu küme kazanır
	NeedLabels []string
	Num        []string
	NumKind    string // vmetrics.NameCandidates türü ("" = gauge)
	Den        []string
	DenKind    string
	Match      string // sabit eşleştirici(ler)
	Expr       string // {num} {den} {by} {win}
}

// resJVMChain — bir JVM satırı.
type resJVMChain struct {
	ID       string
	Title    string // grafik başlığının ölçü kısmı
	Unit     string
	Chart    bool
	Variants []resJVMVariant
}

const (
	resExprRatio = "100 * sum by ({by}) ({num}) / sum by ({by}) ({den} > 0)"
	resExprSum   = "sum by ({by}) ({num})"
)

// resJVMHeapVariants — heap ailesinin varyantları (old gen bunlardan türer).
var resJVMHeapVariants = []resJVMVariant{
	{ID: "semconv", Probes: []string{"jvm.memory.used"}, NeedLabels: []string{"jvm_memory_type"},
		Num: []string{"jvm.memory.used"}, Den: []string{"jvm.memory.limit"}, Match: `jvm_memory_type="heap"`, Expr: resExprRatio},
	{ID: "micrometer", Probes: []string{"jvm.memory.used"}, NeedLabels: []string{"area"},
		Num: []string{"jvm.memory.used"}, Den: []string{"jvm.memory.max"}, Match: `area="heap"`, Expr: resExprRatio},
	{ID: "semconv-type", Probes: []string{"jvm.memory.used"}, NeedLabels: []string{"type"},
		Num: []string{"jvm.memory.used"}, Den: []string{"jvm.memory.limit"}, Match: `type="heap"`, Expr: resExprRatio},
	{ID: "process-runtime", Probes: []string{"process.runtime.jvm.memory.usage"}, NeedLabels: []string{"type"},
		Num: []string{"process.runtime.jvm.memory.usage"}, Den: []string{"process.runtime.jvm.memory.limit"}, Match: `type="heap"`, Expr: resExprRatio},
}

// resJVMOldGenVariants — SAF: heap varyantları + havuz etiketi (varyant başına).
func resJVMOldGenVariants() []resJVMVariant {
	pool := map[string]string{"semconv": "jvm_memory_pool_name", "micrometer": "id", "semconv-type": "pool", "process-runtime": "pool"}
	out := make([]resJVMVariant, 0, len(resJVMHeapVariants))
	for _, v := range resJVMHeapVariants {
		pl := pool[v.ID]
		v.NeedLabels = append(append([]string(nil), v.NeedLabels...), pl)
		v.Match += ", " + pl + `=~"` + resOldGenPoolRe + `"`
		out = append(out, v)
	}
	return out
}

// resJVMGCVariants — SAF: GC payı (sum) / sıklığı (count) varyantları.
func resJVMGCVariants(count bool) []resJVMVariant {
	kind, scaleS, scaleMs := "sum", "100", "0.1"
	if count {
		kind, scaleS, scaleMs = "count", "60", "60"
	}
	expr := func(scale string) string { return scale + " * sum by ({by}) (rate({num}[{win}]))" }
	return []resJVMVariant{
		{ID: "semconv", Probes: []string{"jvm.gc.duration"}, Num: []string{"jvm.gc.duration"}, NumKind: kind, Expr: expr(scaleS)},
		{ID: "micrometer", Probes: []string{"jvm.gc.pause"}, Num: []string{"jvm.gc.pause"}, NumKind: kind, Expr: expr(scaleS)},
		// Eski Java ajanı (1.x): histogram birimi ms → pay = 100 × ms/1000.
		{ID: "process-runtime", Probes: []string{"process.runtime.jvm.gc.duration"}, Num: []string{"process.runtime.jvm.gc.duration"}, NumKind: kind, Expr: expr(scaleMs)},
	}
}

// resJVMChains — SAF: sabit zincir kataloğu (sıra = grafik sırası).
func resJVMChains() []resJVMChain {
	return []resJVMChain{
		{ID: resQJVMHeap, Title: "JVM heap (% limit)", Unit: "%", Chart: true, Variants: resJVMHeapVariants},
		{ID: resQJVMAfterGC, Title: "heap after-GC (% limit)", Unit: "%", Chart: true, Variants: []resJVMVariant{
			{ID: "semconv", Probes: []string{"jvm.memory.used_after_last_gc"}, NeedLabels: []string{"jvm_memory_type"},
				Num: []string{"jvm.memory.used_after_last_gc"}, Den: []string{"jvm.memory.limit"}, Match: `jvm_memory_type="heap"`, Expr: resExprRatio},
			{ID: "semconv-type", Probes: []string{"jvm.memory.used_after_last_gc"}, NeedLabels: []string{"type"},
				Num: []string{"jvm.memory.used_after_last_gc"}, Den: []string{"jvm.memory.limit"}, Match: `type="heap"`, Expr: resExprRatio},
			{ID: "process-runtime", Probes: []string{"process.runtime.jvm.memory.usage_after_last_gc"}, NeedLabels: []string{"type"},
				Num: []string{"process.runtime.jvm.memory.usage_after_last_gc"}, Den: []string{"process.runtime.jvm.memory.limit"}, Match: `type="heap"`, Expr: resExprRatio},
			{ID: "micrometer", Probes: []string{"jvm.gc.live.data.size"},
				Num: []string{"jvm.gc.live.data.size"}, Den: []string{"jvm.gc.max.data.size"}, Expr: resExprRatio},
		}},
		{ID: resQJVMGCShare, Title: "GC süresi payı (%)", Unit: "%", Chart: true, Variants: resJVMGCVariants(false)},
		{ID: resQJVMGCRate, Title: "GC sıklığı (/dk)", Variants: resJVMGCVariants(true)},
		{ID: resQJVMOldGen, Title: "old gen (% limit)", Unit: "%", Variants: resJVMOldGenVariants()},
		{ID: resQJVMThreads, Title: "JVM thread sayısı", Chart: true, Variants: []resJVMVariant{
			{ID: "semconv", Probes: []string{"jvm.thread.count"}, Num: []string{"jvm.thread.count"}, Expr: resExprSum},
			{ID: "semconv-old", Probes: []string{"jvm.threads.count"}, Num: []string{"jvm.threads.count"}, Expr: resExprSum},
			{ID: "micrometer", Probes: []string{"jvm.threads.live", "jvm_threads_live_threads"}, Num: []string{"jvm.threads.live", "jvm_threads_live_threads"}, Expr: resExprSum},
			{ID: "process-runtime", Probes: []string{"process.runtime.jvm.threads.count"}, Num: []string{"process.runtime.jvm.threads.count"}, Expr: resExprSum},
		}},
		{ID: resQJVMPool, Title: "thread havuzu doluluğu (%)", Unit: "%", Chart: true, Variants: []resJVMVariant{
			{ID: "micrometer-executor", Probes: []string{"executor.active", "executor_active_threads"},
				Num: []string{"executor.active", "executor_active_threads"}, Den: []string{"executor.pool.max", "executor_pool_max_threads"},
				Expr: "100 * max by ({by}) ({num} / ({den} > 0))"},
			{ID: "micrometer-tomcat", Probes: []string{"tomcat.threads.busy", "tomcat_threads_busy_threads"},
				Num: []string{"tomcat.threads.busy", "tomcat_threads_busy_threads"}, Den: []string{"tomcat.threads.config.max", "tomcat_threads_config_max_threads"},
				Expr: "100 * max by ({by}) ({num} / ({den} > 0))"},
		}},
	}
}

// resJVMPresenceProbes — drill kapısının yoklaması: heap ailesinin probe'ları.
var resJVMPresenceProbes = []string{"jvm.memory.used", "process.runtime.jvm.memory.usage"}

// resJVMScope — JVM sorgularının hedef kapsamı (çözülmüş etiketlerle).
type resJVMScope struct {
	Service  string // servis etiketi değeri ("" = süzgeç yok)
	SvcLabel string
	PodRe    string // pod etiketi regex'i ("" = süzgeç yok)
	PodLabel string
	By       string // kırılım etiketi (pod etiketi; yoksa servis etiketi)
}

// resJVMSel — SAF: `{__name__…, svc="…", pod=~"…", sabit}`.
func resJVMSel(names []string, kind, match string, sc resJVMScope) string {
	parts := []string{vmetrics.NameSelector(vmetrics.NameCandidates(kind, names...))}
	if sc.Service != "" && sc.SvcLabel != "" {
		parts = append(parts, sc.SvcLabel+"="+vmetrics.PromStrTS(sc.Service))
	}
	if sc.PodRe != "" && sc.PodLabel != "" {
		parts = append(parts, sc.PodLabel+"=~"+vmetrics.PromStrTS(sc.PodRe))
	}
	if match != "" {
		parts = append(parts, match)
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// buildResJVMQuery — SAF: varyant + kapsam → TEK PromQL (topk(20, …)).
func buildResJVMQuery(v resJVMVariant, sc resJVMScope, winS int) string {
	den := ""
	if len(v.Den) > 0 {
		den = resJVMSel(v.Den, v.DenKind, v.Match, sc)
	}
	q := strings.NewReplacer(
		"{num}", resJVMSel(v.Num, v.NumKind, v.Match, sc),
		"{den}", den,
		"{by}", sc.By,
		"{win}", fmt.Sprintf("%ds", winS),
	).Replace(v.Expr)
	return fmt.Sprintf("topk(%d, %s)", resMaxSeries, q)
}

// resJVMVariantFits — SAF: etiket kümesi varyanta uyuyor mu.
func resJVMVariantFits(v resJVMVariant, set map[string]bool) bool {
	if len(set) == 0 {
		return false
	}
	for _, l := range v.NeedLabels {
		if !set[l] {
			return false
		}
	}
	return true
}

// resJVMTriedNames — SAF: dürüst "JVM metriği yok" cevabının denenen yazımları.
func resJVMTriedNames(max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range resJVMPresenceProbes {
		for _, c := range vmetrics.NameCandidates("", p) {
			if !seen[c] && len(out) < max {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// resJVMCacheKey — SAF (CLAUDE.md "cache key hashes ALL inputs").
func resJVMCacheKey(chain, variant, query string, sc resJVMScope, from, to time.Time, step int) string {
	return "cosre-res-jvm:v1:" + fnvStr("vm", chain, variant, query, sc.Service, sc.SvcLabel, sc.PodRe, sc.PodLabel, sc.By,
		fmt.Sprint(from.Unix()/60), fmt.Sprint(to.Unix()/60), fmt.Sprint(step))
}

// resJVMScopeFor — SAF: hedef + varyantın etiket kümesi → kapsam. Uygulanamazsa
// gerekçe döner (yanlış servisin JVM'i okunmaz).
func resJVMScopeFor(route *guidedRoute, lm vmetrics.LabelMap, set map[string]bool) (resJVMScope, string) {
	var sc resJVMScope
	if r := vmetrics.ResolveLabelRole(vmetrics.RolePod, lm, set, true); r.Applied() {
		sc.PodLabel = r.Label
	}
	svcLabel := ""
	if r := vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, set, true); r.Applied() {
		svcLabel = r.Label
	}
	switch route.ResKind {
	case resKindPod:
		if sc.PodLabel == "" {
			return sc, "JVM metriği pod etiketi taşımıyor (LabelMap.Pod / k8s_pod_name / pod) — pod süzülemedi"
		}
		sc.PodRe = regexp.QuoteMeta(route.ResTarget)
		if route.Service != "" && svcLabel != "" {
			sc.Service, sc.SvcLabel = route.Service, svcLabel
		}
	case resKindWorkload:
		if sc.PodLabel == "" {
			return sc, "JVM metriği pod etiketi taşımıyor — workload pod'ları süzülemedi"
		}
		sc.PodRe = thanos.ServicePodRegex(route.ResTarget, "")
	default:
		svc := route.Service
		if svc == "" {
			svc = route.ResTarget
		}
		if svcLabel == "" {
			return sc, fmt.Sprintf("JVM metriği servis etiketi taşımıyor (service_name / %s) — %q süzülemedi",
				strings.Join(vmetrics.RoleCandidates(vmetrics.RoleService), ", "), svc)
		}
		sc.Service, sc.SvcLabel = svc, svcLabel
	}
	sc.By = sc.PodLabel
	if sc.By == "" {
		sc.By = svcLabel
	}
	return sc, ""
}

// resJVMLabelService — SAF (v0.10.1704): JVM etiket keşfinin servis kapsamı.
// Servis kipi: route.Service, yoksa hedef (resJVMScopeFor ile aynı). Pod kipi:
// route.Service (biliniyorsa sorgu da servise süzülür). Workload kipi: pod
// regex'iyle süzülür, eşitlik kapsamı yok → "" (filo geneli, sınırlı okuma).
func resJVMLabelService(route *guidedRoute) string {
	switch route.ResKind {
	case resKindWorkload:
		return ""
	case resKindPod:
		return route.Service
	}
	if route.Service != "" {
		return route.Service
	}
	return route.ResTarget
}

// resGCThresholds — SAF: RuntimeAlertConfig → GC payı eşikleri (nil = varsayılan).
func resGCThresholds(c *chstore.RuntimeAlertConfig) (warn, crit float64) {
	warn, crit = resourceThresholds.GCShareWarn, resourceThresholds.GCShareCrit
	if c != nil && c.GCShareWarnPct > 0 {
		warn = c.GCShareWarnPct
	}
	if c != nil && c.GCShareCritPct > 0 {
		crit = c.GCShareCritPct
	}
	if crit <= warn { // ters eşik her şeyi kritik yapardı (patchRuntimeAlerts kuralı)
		crit = warn * 2
	}
	return warn, crit
}

// resourceRuntimeAlerts — operatörün JVM alarm eşikleri (system_settings
// runtime_alerts; yoksa varsayılan). Tek okuma, salt-okur.
func (s *Server) resourceRuntimeAlerts(ctx context.Context) *chstore.RuntimeAlertConfig {
	if s == nil || s.store == nil {
		return nil
	}
	c := s.store.GetRuntimeAlerts(ctx)
	return &c
}

// ─── İstatistik (SAF) ───────────────────────────────────────────────────

// resJVMStats — tek pod'un JVM özeti (yalnız okunan satırlar dolu).
type resJVMStats struct {
	HasHeap bool
	Heap    metricChartSeriesStats

	HasAfterGC  bool
	AfterGC     metricChartSeriesStats
	AfterSlopeH float64 // yüzde puanı / saat
	SlopeValid  bool    // pencere ≥30 dk
	Leak        bool

	HasGC  bool
	GC     metricChartSeriesStats
	HasGCR bool
	GCRate metricChartSeriesStats

	HasOld bool
	Old    metricChartSeriesStats

	HasThreads bool
	Threads    metricChartSeriesStats

	HasPool bool
	Pool    metricChartSeriesStats

	GCWarn, GCCrit float64
}

// resLeakSuspect — SAF: sızıntı şüphesi kuralı (tek yer). Eğim yüzde puanı/sa,
// seviye son after-GC yüzdesi; pencere en az 30 dk.
func resLeakSuspect(lastPct, slopePctH float64, rangeS int64) bool {
	t := resourceThresholds
	return rangeS >= resLeakMinRangeS && slopePctH > t.LeakSlopePctH && lastPct >= t.LeakLevelPct
}

// resBuildJVMStats — SAF: zincir → seriler (Labels["pod"] = kırılım değeri) ⇒
// pod → JVM özeti.
func resBuildJVMStats(rows map[string][]resSeries, rangeS int64, gcWarn, gcCrit float64) map[string]*resJVMStats {
	by := map[string]*resJVMStats{}
	get := func(pod string) *resJVMStats {
		j := by[pod]
		if j == nil {
			j = &resJVMStats{GCWarn: gcWarn, GCCrit: gcCrit}
			by[pod] = j
		}
		return j
	}
	for chain, series := range rows {
		for _, s := range series {
			pod := s.Labels["pod"]
			st, ok := metricChartStats(s.Points)
			if pod == "" || !ok {
				continue
			}
			j := get(pod)
			switch chain {
			case resQJVMHeap:
				j.HasHeap, j.Heap = true, st
			case resQJVMAfterGC:
				j.HasAfterGC, j.AfterGC = true, st
				j.AfterSlopeH = resSlopePerHour(s.Points)
				j.SlopeValid = rangeS >= resLeakMinRangeS
				j.Leak = resLeakSuspect(st.Last, j.AfterSlopeH, rangeS)
			case resQJVMGCShare:
				j.HasGC, j.GC = true, st
			case resQJVMGCRate:
				j.HasGCR, j.GCRate = true, st
			case resQJVMOldGen:
				j.HasOld, j.Old = true, st
			case resQJVMThreads:
				j.HasThreads, j.Threads = true, st
			case resQJVMPool:
				j.HasPool, j.Pool = true, st
			}
		}
	}
	return by
}

// resMergeJVM — SAF: JVM özetlerini pod satırlarına ad üzerinden bağlar;
// Thanos'ta olmayan pod (ör. Thanos yok) kendi satırını alır.
func resMergeJVM(ps []resPodStats, jvm map[string]*resJVMStats) []resPodStats {
	out := append([]resPodStats(nil), ps...)
	seen := map[string]bool{}
	for i := range out {
		if j := jvm[out[i].Pod]; j != nil {
			out[i].HasJVM, out[i].JVM = true, *j
			seen[out[i].Pod] = true
		}
	}
	pods := make([]string, 0, len(jvm))
	for p := range jvm {
		if !seen[p] {
			pods = append(pods, p)
		}
	}
	sort.Strings(pods)
	for _, p := range pods {
		out = append(out, resPodStats{Pod: p, HasJVM: true, JVM: *jvm[p]})
	}
	return out
}

func resPct0(v float64) string { return fmt.Sprintf("%%%.0f", v) }
func resPct1(v float64) string {
	return "%" + strings.TrimSuffix(strings.TrimSuffix(strconv.FormatFloat(v, 'f', 1, 64), "0"), ".")
}

// fmtSlopePct — SAF: yüzde puanı/saat eğimi işaretli ("+6.2 %/sa").
func fmtSlopePct(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "-"
	}
	return sign + strconv.FormatFloat(math.Abs(v), 'f', 1, 64) + " %/sa"
}

// resJVMLineTR — SAF: pod satırının JVM kısmı.
func resJVMLineTR(j resJVMStats, loc *time.Location) string {
	hm := func(ms int64) string { return time.UnixMilli(ms).In(loc).Format("15:04") }
	var parts []string
	if j.HasHeap {
		parts = append(parts, fmt.Sprintf("JVM heap ort limitin %s'i (tepe %s %s, son %s)", resPct0(j.Heap.Avg), resPct0(j.Heap.Max), hm(j.Heap.MaxAtMs), resPct0(j.Heap.Last)))
	}
	if j.HasAfterGC {
		s := fmt.Sprintf("after-GC son %s, eğim %s", resPct0(j.AfterGC.Last), fmtSlopePct(j.AfterSlopeH))
		switch {
		case j.Leak:
			s += " (SIZINTI ŞÜPHESİ)"
		case !j.SlopeValid:
			s += " (pencere <30 dk — sızıntı kuralı uygulanmadı)"
		}
		parts = append(parts, s)
	}
	if j.HasGC {
		parts = append(parts, fmt.Sprintf("GC payı ort %s (tepe %s)", resPct1(j.GC.Avg), resPct1(j.GC.Max)))
	}
	if j.HasGCR {
		parts = append(parts, fmt.Sprintf("GC %s/dk (tepe %s/dk)", strconv.FormatFloat(j.GCRate.Avg, 'f', 1, 64), strconv.FormatFloat(j.GCRate.Max, 'f', 1, 64)))
	}
	if j.HasOld {
		parts = append(parts, fmt.Sprintf("old gen son limitin %s'i", resPct0(j.Old.Last)))
	}
	if j.HasThreads {
		parts = append(parts, fmt.Sprintf("thread son %.0f (tepe %.0f)", j.Threads.Last, j.Threads.Max))
	}
	if j.HasPool {
		parts = append(parts, fmt.Sprintf("thread havuzu tepe %s (son %s)", resPct0(j.Pool.Max), resPct0(j.Pool.Last)))
	}
	if len(parts) == 0 {
		return "JVM seri yok"
	}
	return strings.Join(parts, ", ")
}

// resJVMBreaches — SAF: JVM eşik aşımları (resBreaches çağırır).
func resJVMBreaches(pod string, j resJVMStats) []resBreach {
	t := resourceThresholds
	var out []resBreach
	if j.HasHeap && j.Heap.Avg >= t.HeapWarn {
		out = append(out, resBreach{Pod: pod, Crit: j.Heap.Avg >= t.HeapCrit, Text: fmt.Sprintf(
			"JVM heap ort limitin %s'i (tepe %s; eşik %s uyarı / %s kritik — ortalama, testere dişi tepesi GC tetik noktasıdır)",
			resPct0(j.Heap.Avg), resPct0(j.Heap.Max), resPct0(t.HeapWarn), resPct0(t.HeapCrit))})
	}
	if j.Leak {
		out = append(out, resBreach{Pod: pod, Text: fmt.Sprintf(
			"heap after-GC son limitin %s'i, eğim %s → sızıntı şüphesi (kural: eğim >%s/sa VE seviye ≥%s)",
			resPct0(j.AfterGC.Last), fmtSlopePct(j.AfterSlopeH), resPct0(t.LeakSlopePctH), resPct0(t.LeakLevelPct))})
	}
	if j.HasGC && j.GC.Avg >= j.GCWarn {
		out = append(out, resBreach{Pod: pod, Crit: j.GC.Avg >= j.GCCrit, Text: fmt.Sprintf(
			"GC süresi payı ort %s (tepe %s; eşik %s uyarı / %s kritik — RuntimeAlertConfig)",
			resPct1(j.GC.Avg), resPct1(j.GC.Max), resPct0(j.GCWarn), resPct0(j.GCCrit))})
	}
	if j.HasPool && j.Pool.Max >= t.PoolWarn {
		out = append(out, resBreach{Pod: pod, Text: fmt.Sprintf("thread havuzu doluluğu tepe %s (son %s; eşik %s)",
			resPct0(j.Pool.Max), resPct0(j.Pool.Last), resPct0(t.PoolWarn))})
	}
	return out
}

// resJVMThresholdsTR — SAF: JVM eşiklerinin okunur hâli.
func resJVMThresholdsTR(gcWarn, gcCrit float64) string {
	t := resourceThresholds
	return fmt.Sprintf("JVM heap ort limitin %s/%s'i, GC payı %s/%s, havuz %s, sızıntı eğim >%s/sa ve ≥%s",
		resPct0(t.HeapWarn), resPct0(t.HeapCrit), resPct0(gcWarn), resPct0(gcCrit), resPct0(t.PoolWarn), resPct0(t.LeakSlopePctH), resPct0(t.LeakLevelPct))
}

// ─── Drill kararı (SAF) ─────────────────────────────────────────────────

// resDrillInput — drill kapısının girdileri.
type resDrillInput struct {
	JVMPresent bool
	MemPctMax  float64 // -1 = limit/okuma yok
	Restarts   int
	OOMKilled  bool
	Ask        string // route.ResDrillAsk
}

// decideResJVMDrill — SAF: R2 drill kuralı. JVM metriği VAR (yoklama etiket
// kümesi dolu) VE (bellek limitin ≥%80'i | restart >0 | OOMKilled | soruda
// yavaşlık/latency | soruda JVM sözcüğü) → JVM ailesi aynı cevapta koşar.
// İkinci dönüş gerekçe (koşulduysa tetik, koşulmadıysa eksik koşul).
func decideResJVMDrill(in resDrillInput) (bool, string) {
	if !in.JVMPresent {
		return false, "JVM metriği yok"
	}
	var why []string
	if in.MemPctMax >= resourceThresholds.MemWarn {
		why = append(why, fmt.Sprintf("bellek limitin %s'i ≥ %s", resPct0(in.MemPctMax), resPct0(resourceThresholds.MemWarn)))
	}
	if in.Restarts > 0 {
		why = append(why, fmt.Sprintf("pencerede %d restart", in.Restarts))
	}
	if in.OOMKilled {
		why = append(why, "son sonlanma OOMKilled")
	}
	switch in.Ask {
	case resDrillAskSlow:
		why = append(why, "soruda yavaşlık/latency")
	case resDrillAskJVM:
		why = append(why, "soruda JVM/heap/GC")
	}
	if len(why) == 0 {
		return false, fmt.Sprintf("drill koşulu yok: bellek <%s, restart yok, OOMKilled yok, yavaşlık/JVM sorulmadı", resPct0(resourceThresholds.MemWarn))
	}
	return true, strings.Join(why, ", ")
}

// resDrillFromStats — SAF: pod özetlerinden drill girdileri.
func resDrillFromStats(ps []resPodStats, present bool, ask string) resDrillInput {
	in := resDrillInput{JVMPresent: present, MemPctMax: -1, Ask: ask}
	for _, p := range ps {
		if v := p.MemPctMax(); v > in.MemPctMax {
			in.MemPctMax = v
		}
		in.Restarts += p.Restarts
		if p.LastReason == "OOMKilled" {
			in.OOMKilled = true
		}
	}
	return in
}

// ─── Okuyucu ────────────────────────────────────────────────────────────

// resJVMRow — tek zincirin sonucu.
type resJVMRow struct {
	Chain   resJVMChain
	Variant string
	Query   string
	Res     resQueryResult
	Skip    string // koşulmadıysa gerekçe
}

// resJVMOutcome — JVM aşamasının sonucu.
type resJVMOutcome struct {
	Rows    []resJVMRow
	Queries int
	Scope   resJVMScope
}

// resJVMProber — tek koşu boyunca etiket yoklaması belleği (metricChartLabelSet
// zaten 5 dk önbellekli; bu, aynı koşudaki tekrarları keser).
//
// v0.10.1704 — svc = etiket keşfinin servis kapsamı (resJVMLabelService);
// boş = filo geneli (metricLabelSetFor).
type resJVMProber struct {
	s   *Server
	rd  metricChartReader
	to  time.Time
	svc string
	mu  sync.Mutex
	got map[string]map[string]bool
	err map[string]error
}

func (p *resJVMProber) set(ctx context.Context, name string) (map[string]bool, error) {
	p.mu.Lock()
	if set, ok := p.got[name]; ok {
		err := p.err[name]
		p.mu.Unlock()
		return set, err
	}
	p.mu.Unlock()
	set, err := p.s.metricLabelSetFor(ctx, p.rd, name, p.svc, p.to)
	p.mu.Lock()
	p.got[name], p.err[name] = set, err
	p.mu.Unlock()
	return set, err
}

// variantSet — varyantın ilk dolu probe kümesi.
func (p *resJVMProber) variantSet(ctx context.Context, v resJVMVariant) (map[string]bool, error) {
	var firstErr error
	for _, name := range v.Probes {
		set, err := p.set(ctx, name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(set) > 0 {
			return set, nil
		}
	}
	return nil, firstErr
}

// resourceJVMPresence — drill kapısı: heap ailesi VM'de var mı (son 24 sa) ve
// hedef kapsamı çözülebiliyor mu. Hata = okunamadı (yok değil).
func (s *Server) resourceJVMPresence(ctx context.Context, p *resJVMProber, route *guidedRoute) (bool, string, error) {
	var firstErr error
	for _, v := range resJVMHeapVariants {
		set, err := p.variantSet(ctx, v)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if !resJVMVariantFits(v, set) {
			continue
		}
		if _, why := resJVMScopeFor(route, p.rd.MetricLabelMap().Normalized(), set); why != "" {
			return false, why, nil
		}
		return true, "", nil
	}
	return false, "", firstErr
}

// resourceJVMRun — JVM zincirleri: TEK 8 sn son tarih (yoklamalar dâhil), ≤4
// paralel, zincir başına ≤2 deneme, toplam ≤resJVMQueryCap sorgu; her sorgu 60
// sn önbellekli.
func (s *Server) resourceJVMRun(ctx context.Context, p *resJVMProber, route *guidedRoute, from, to time.Time, step, winS int) resJVMOutcome {
	chains := resJVMChains()
	out := resJVMOutcome{Rows: make([]resJVMRow, len(chains))}
	jctx, cancel := context.WithTimeout(ctx, resQueryTimeout)
	defer cancel()
	lm := p.rd.MetricLabelMap().Normalized()
	sem := make(chan struct{}, resParallel)
	var wg sync.WaitGroup
	var used int32
	var scopeMu sync.Mutex
	for i, ch := range chains {
		wg.Add(1)
		go func(i int, ch resJVMChain) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			row := resJVMRow{Chain: ch}
			tries := 0
			var lastErr error
			for _, v := range ch.Variants {
				if tries >= resJVMTriesPerChain {
					break
				}
				set, err := p.variantSet(jctx, v)
				if err != nil {
					lastErr = err
				}
				if !resJVMVariantFits(v, set) {
					continue
				}
				sc, why := resJVMScopeFor(route, lm, set)
				if why != "" {
					row.Skip = why
					continue
				}
				if atomic.AddInt32(&used, 1) > resJVMQueryCap {
					row.Skip = fmt.Sprintf("JVM sorgu bütçesi (≤%d) doldu", resJVMQueryCap)
					break
				}
				tries++
				q := buildResJVMQuery(v, sc, winS)
				row.Variant, row.Query, row.Skip = v.ID, q, ""
				row.Res = s.resourceJVMQuery(jctx, p.rd, ch.ID, v.ID, q, sc, from, to, step)
				scopeMu.Lock()
				if out.Scope.By == "" || i == 0 { // kanıttaki kapsam: heap zinciri öncelikli
					out.Scope = sc
				}
				scopeMu.Unlock()
				if row.Res.Err != nil || len(row.Res.Series) > 0 {
					break // hata ya da seri: yedek varyant denenmez
				}
			}
			if row.Query == "" && row.Skip == "" {
				if lastErr != nil {
					row.Skip = "etiket okunamadı (okunamadı ≠ yok): " + lastErr.Error()
				} else {
					row.Skip = "metrik yok"
				}
			}
			out.Rows[i] = row
		}(i, ch)
	}
	wg.Wait()
	if n := int(atomic.LoadInt32(&used)); n > resJVMQueryCap {
		out.Queries = resJVMQueryCap
	} else {
		out.Queries = n
	}
	return out
}

// resourceJVMQuery — tek VM range sorgusu (önbellek → okuma). Seriler pod
// etiketinden "pod" anahtarına taşınır (Thanos satırlarıyla aynı birleştirme).
func (s *Server) resourceJVMQuery(ctx context.Context, rd metricChartReader, chain, variant, q string, sc resJVMScope, from, to time.Time, step int) resQueryResult {
	key := resJVMCacheKey(chain, variant, q, sc, from, to, step)
	var r resQueryResult
	if s.metricChartCacheGet(ctx, key, &r) {
		return r
	}
	start := time.Now()
	raw, err := rd.QueryPromQLRange(ctx, q, from, to, step, metricChartMaxPoints)
	r = resQueryResult{DurationMs: time.Since(start).Milliseconds(), Effective: q}
	if err != nil {
		r.Err = err
		return r
	}
	r.Series = resJVMSeries(raw, sc.By)
	if len(raw) > resMaxSeries {
		r.Series, r.Truncated = r.Series[:resMaxSeries], true
	}
	s.metricChartCacheSet(ctx, key, r, resCacheTTL)
	return r
}

// resJVMSeries — SAF: VM serileri (GroupKey `k="v"`) → resSeries; kırılım
// etiketi "pod" anahtarına yazılır; NaN/Inf atılır.
func resJVMSeries(in []chstore.SpanMetricSeries, by string) []resSeries {
	out := make([]resSeries, 0, len(in))
	for _, s := range in {
		l := map[string]string{}
		for _, kv := range s.GroupKey {
			k, v, ok := strings.Cut(kv, "=")
			if !ok {
				continue
			}
			if uq, err := strconv.Unquote(v); err == nil {
				v = uq
			}
			l[k] = v
		}
		if by != "" && l[by] != "" {
			l["pod"] = l[by]
		}
		rs := resSeries{Labels: l}
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			rs.Points = append(rs.Points, [2]float64{float64(p.Time / 1e6), p.Value})
		}
		out = append(out, rs)
	}
	return out
}

// resourceJVMStage — JVM aşaması (drill ya da jvm ailesi): yoklama, karar,
// koşu, grafikler, birleştirme. Döner: pod satırları (JVM birleşmiş), modelin
// okuduğu JVM durum satırları, kaynak eki. force = jvm ailesi (kapı yok).
func (s *Server) resourceJVMStage(ctx context.Context, emit func(string, any), route *guidedRoute, d resourceDeps, stats []resPodStats,
	from, to time.Time, step, winS int, rangeS int64, force bool) ([]resPodStats, []string, string) {
	route.ResJVM = false
	tried := strings.Join(resJVMTriedNames(8), ", ")
	if !d.IsVM || d.VM == nil {
		return stats, []string{"JVM/GC: VictoriaMetrics yapılandırılmamış — JVM/GC metrikleri OKUNAMADI (yok demek değil) — JVM/GC yorumu YAPMA."}, ""
	}
	p := &resJVMProber{s: s, rd: d.VM, to: to, svc: resJVMLabelService(route), got: map[string]map[string]bool{}, err: map[string]error{}}
	pctx, cancel := context.WithTimeout(ctx, resQueryTimeout)
	present, scopeWhy, perr := s.resourceJVMPresence(pctx, p, route)
	cancel()
	switch {
	case perr != nil && !present:
		return stats, []string{"JVM/GC: VictoriaMetrics etiket okuması başarısız (okunamadı ≠ yok): " + perr.Error() + " — JVM/GC yorumu YAPMA."}, ""
	case scopeWhy != "":
		return stats, []string{"JVM/GC: " + scopeWhy + " — JVM/GC yorumu YAPMA."}, ""
	case !present:
		return stats, []string{"JVM/GC: VictoriaMetrics'te JVM metriği yok (son 24 sa; denenen yazımlar: " + tried + ") — JVM/GC yorumu YAPMA."}, ""
	}
	if !force {
		drill, why := decideResJVMDrill(resDrillFromStats(stats, true, route.ResDrillAsk))
		if !drill {
			route.ResJVM = route.Service != "" || route.ResKind == resKindPod
			return stats, []string{"JVM/GC: bu cevapta OKUNMADI (" + why + "; VictoriaMetrics'te JVM metrikleri var, \"GC/heap'e bak\" çipiyle bakılır) — JVM/GC yorumu YAPMA."}, ""
		}
		emitGuidedContextStep(emit, "JVM drill: "+why)
		lines, merged := s.resourceJVMReadAndRender(ctx, emit, p, route, d, stats, from, to, step, winS, rangeS)
		return merged, append([]string{"JVM drill tetiklendi (" + why + ")."}, lines...), " + VictoriaMetrics JVM"
	}
	lines, merged := s.resourceJVMReadAndRender(ctx, emit, p, route, d, stats, from, to, step, winS, rangeS)
	return merged, lines, " + VictoriaMetrics JVM"
}

// resourceJVMReadAndRender — zincirleri koşar, grafik bloklarını yayar, JVM
// özetlerini pod satırlarına bağlar.
func (s *Server) resourceJVMReadAndRender(ctx context.Context, emit func(string, any), p *resJVMProber, route *guidedRoute, d resourceDeps, stats []resPodStats,
	from, to time.Time, step, winS int, rangeS int64) ([]string, []resPodStats) {
	out := s.resourceJVMRun(ctx, p, route, from, to, step, winS)
	gcWarn, gcCrit := resGCThresholds(d.Runtime)
	rows := map[string][]resSeries{}
	var used, notes []string
	for _, r := range out.Rows {
		switch {
		case r.Query == "":
			notes = append(notes, r.Chain.ID+": "+r.Skip)
			continue
		case r.Res.Err != nil:
			notes = append(notes, fmt.Sprintf("%s OKUNAMADI (okunamadı ≠ yok) — %v", r.Chain.ID, r.Res.Err))
			continue
		case len(r.Res.Series) == 0:
			notes = append(notes, r.Chain.ID+": bu hedef/pencere için seri yok (varyant "+r.Variant+")")
			continue
		}
		used = append(used, r.Chain.ID+"="+r.Variant)
		rows[r.Chain.ID] = r.Res.Series
		if r.Res.Truncated {
			notes = append(notes, fmt.Sprintf("%s: seri tavanı (≤%d) uygulandı", r.Chain.ID, resMaxSeries))
		}
		if !r.Chain.Chart {
			continue
		}
		series, trunc := resChartSeries(r.Res.Series, r.Chain.Unit)
		if len(series) == 0 {
			continue
		}
		href := mcptools.PromQLEditorHref(r.Query, "vm", "", from.UnixMilli(), to.UnixMilli())
		if route.ResHref == "" {
			route.ResHref = href
		}
		cargs := fmt.Sprintf(`{"source":"vm","query":%s,"variant":%s,"range_s":%d}`, jsonStr(r.Chain.ID), jsonStr(r.Variant), rangeS)
		cn := emitGuidedStep(emit, "metric_chart", cargs)
		title := route.ResTarget + " · " + r.Chain.Title + " · VictoriaMetrics"
		ns := len(series)
		ev := mcptools.ChatEvidence{Tool: "metric_chart", Source: "VictoriaMetrics", Query: r.Query, QueryKind: "promql",
			Window: mcptools.EvidenceWindow{FromMs: from.UnixMilli(), ToMs: to.UnixMilli()}, Series: &ns,
			DurationMs: r.Res.DurationMs, Href: href}
		chart := &mcptools.EvChartPayload{Title: title, DeepLink: href,
			Window: mcptools.EvBlockWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, Series: series, Truncated: trunc || r.Res.Truncated}
		emitGuidedEvidenceResult(emit, cn, "metric_chart", fmt.Sprintf("%s: %d seri.\nPromQL: %s\n", title, ns, r.Query), ev, chart)
	}
	var lines []string
	if len(rows) == 0 {
		lines = append(lines, "JVM/GC: VictoriaMetrics'te JVM metriği var ama bu hedef/pencere için seri dönmedi — JVM/GC yorumu YAPMA.")
	} else {
		scope := ""
		switch {
		case out.Scope.PodRe != "":
			scope = out.Scope.PodLabel + "=~" + vmetrics.PromStrTS(out.Scope.PodRe)
		case out.Scope.Service != "":
			scope = out.Scope.SvcLabel + "=" + vmetrics.PromStrTS(out.Scope.Service) + " (cluster ayrımı yok)"
		}
		lines = append(lines, fmt.Sprintf("JVM/GC: OKUNDU — VictoriaMetrics, kapsam %s, %d sorgu (≤%d), varyantlar: %s. JVM satırları KAYNAK ÖZETİ'nde; \"sızıntı şüphesi\" yalnız EŞİK AŞIMI'nda yazıyorsa söylenir.",
			scope, out.Queries, resJVMQueryCap, strings.Join(used, ", ")))
	}
	if route.Env != "" {
		notes = append(notes, fmt.Sprintf("ortam %q süzgeci JVM sorgularına uygulanmadı (pod/servis kapsamı)", route.Env))
	}
	for _, n := range notes {
		lines = append(lines, "Not (JVM): "+n+".")
	}
	if len(rows) == 0 {
		return lines, stats
	}
	return lines, resMergeJVM(stats, resBuildJVMStats(rows, rangeS, gcWarn, gcCrit))
}

// resourceJVMOnly — jvm ailesi (heap/gc/thread/havuz sorusu): yalnız
// VictoriaMetrics JVM satırları (Thanos okunmaz). Okunamayan / olmayan
// durumda anlatım çağrısı yok (DirectAnswer).
func (s *Server) resourceJVMOnly(ctx context.Context, emit func(string, any), route *guidedRoute, d resourceDeps, n int, head string,
	from, to time.Time, step, winS int, rangeS int64, notes []string, loc *time.Location) (string, string, error) {
	stats, lines, _ := s.resourceJVMStage(ctx, emit, route, d, nil, from, to, step, winS, rangeS, true)
	if len(stats) == 0 {
		text := strings.Join(lines, "\n")
		text = strings.TrimSuffix(strings.Replace(text, " — JVM/GC yorumu YAPMA.", ".", 1), "\n")
		route.DirectAnswer = text
		emitGuidedStepResult(emit, n, "resource_investigation", text, nil)
		return text, "VictoriaMetrics (JVM okunamadı/yok)", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s; kaynak VictoriaMetrics — JVM metrikleri; adım %ds, rate penceresi %ds):\n", head, step, winS)
	for _, nt := range notes {
		b.WriteString("Not: " + nt + ".\n")
	}
	b.WriteString(renderResourceSummaryTR(stats, loc))
	if route.ResHref != "" {
		b.WriteString("Grafikler cevabın altında kanıt çiplerinde çizildi (sunucu verisi; grafik bloğu YAZMA).\n")
	}
	for _, l := range lines {
		b.WriteString(l + "\n")
	}
	b.WriteString("Konteyner CPU/bellek/restart (Thanos) bu cevapta okunmadı — \"CPU/bellek/restart\" çipiyle bakılır.\n")
	text := b.String()
	emitGuidedStepResult(emit, n, "resource_investigation", text, nil)
	return text, "VictoriaMetrics JVM · resource_investigation/jvm", nil
}
