package ml

// thanos_source.go — v0.10.2019: ML metrik aileleri için aile başına kaynak
// (VictoriaMetrics | Thanos cluster'ları), vidasız. Saf çekirdek.
//
// Operatör (2026-11-12, docs/plans/ml-next.md "Operatör notu 01.11.2026"):
// JVM metrikleri VM'de değil, uzak cluster'ların OpenShift izleme yığınında
// (Thanos). metric_jvm yalnız VM'e baktığı için prod'da boş dönüyordu.
//
// KAYNAK SEÇİMİ (tek cümle): ailenin ifadelerinden biri VM'de veri
// döndürüyorsa kaynak VM; döndürmüyorsa ailenin metriğini taşıyan Thanos
// cluster'ları; ikisinde de yoksa "bulunamadı"; ölçüm yapılamadıysa önceki
// karar korunur (yoksa VM yolu bugünkü gibi denenir). Tespit saatte bir,
// yalnız açık ailelerde; sonuç süreç içinde + tik durum blobunda
// (MLTickStatus.MetricSources). Ayar alanı YOK.
//
// THANOS OKUMASI: aile ifadesi pod düzeyinde koşar ({svc} → "namespace,
// pod"), pod'lar servise mevcut clusters katmanının eşlemesiyle bağlanır
// (thanos.ServicePodKeys = ServicePodRegex'in anahtarları; servis
// metadata'sındaki deployment adı dahil), servis değeri pod değerlerinden
// ifadenin dış toplayıcısıyla (avg | sum | max; kuantil/oran için max)
// birleşir. Seri parçası ve olay kimliği cluster'ı taşır
// ("<aile>|<ifade>@<cluster>"); servis aynı kalır → D2 grubu servis üzerinden.

import (
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	metricSourceVM     = "vm"
	metricSourceThanos = "thanos"
	metricSourceNone   = "none"

	sourceDetectEvery           = time.Hour
	thanosQueriesPerClusterTick = 12 // değerlendirme sorgusu, cluster × tik
	thanosQueryTimeoutS         = 10

	thanosMaxSeries = 5000
	thanosParallel  = 4
)

// thanosClusterDeadline — cluster başına tik son tarihi (test kısaltır).
var thanosClusterDeadline = 25 * time.Second

// familyCheck — bir kaynağın aile ölçümü.
type familyCheck struct{ Present, Checked bool }

// pickFamilySource — SAF: kaynak seçimi (dosya başı kuralı).
func pickFamilySource(vm familyCheck, th map[string]familyCheck, prev chstore.MLMetricSource, now time.Time) chstore.MLMetricSource {
	if vm.Present {
		return chstore.MLMetricSource{Source: metricSourceVM, CheckedAt: now.Unix()}
	}
	var cls []string
	allChecked := vm.Checked
	for name, c := range th {
		if c.Present {
			cls = append(cls, name)
		}
		allChecked = allChecked && c.Checked
	}
	if len(cls) > 0 {
		sort.Strings(cls)
		return chstore.MLMetricSource{Source: metricSourceThanos, Clusters: cls, CheckedAt: now.Unix()}
	}
	if allChecked {
		return chstore.MLMetricSource{Source: metricSourceNone, CheckedAt: now.Unix(), Note: "ailenin metrikleri VictoriaMetrics'te ve Thanos cluster'larında bulunamadı"}
	}
	if prev.Source != "" {
		return prev
	}
	return chstore.MLMetricSource{Note: "ölçülemedi"}
}

// familyOnVM — SAF: VM yolu bu aileyi sorar mı (ölçülemeyen = bugünkü davranış).
func familyOnVM(src map[string]chstore.MLMetricSource, fam string) bool {
	s := src[fam].Source
	return s == metricSourceVM || s == ""
}

// familyOnThanos — SAF.
func familyOnThanos(src map[string]chstore.MLMetricSource, fam string) bool {
	return src[fam].Source == metricSourceThanos
}

// ── pod → servis ─────────────────────────────────────────────────────────

// podIndex — pod öneki → servis. "" değer = birden çok servise ait (atla).
type podIndex struct{ byKey map[string]string }

// buildPodIndex — SAF: servis adları + metadata deployment'ları.
// Güçlü anahtar (deployment, servis adı) zayıfı (ortam eksiz ad) ezer;
// aynı güçte iki servis = belirsiz.
func buildPodIndex(services []string, meta map[string]chstore.ServiceMetadata) podIndex {
	type ent struct {
		svc    string
		strong bool
	}
	keys := map[string]ent{}
	ambiguous := map[string]bool{}
	sorted := append([]string(nil), services...)
	sort.Strings(sorted)
	for _, svc := range sorted {
		dep := meta[svc].Deployment
		for _, k := range thanos.ServicePodKeys(svc, dep) {
			strong := k == svc || k == dep
			cur, ok := keys[k]
			switch {
			case !ok:
				keys[k] = ent{svc, strong}
			case cur.svc == svc:
				if strong {
					keys[k] = ent{svc, true}
				}
			case strong && !cur.strong:
				keys[k] = ent{svc, true}
				delete(ambiguous, k)
			case strong == cur.strong:
				ambiguous[k] = true
			}
		}
	}
	out := podIndex{byKey: make(map[string]string, len(keys))}
	for k, e := range keys {
		if ambiguous[k] {
			out.byKey[k] = ""
		} else {
			out.byKey[k] = e.svc
		}
	}
	return out
}

// serviceOf — SAF: pod adı → servis (en uzun "<anahtar>-" öneki).
func (ix podIndex) serviceOf(pod string) (string, bool) {
	for i := len(pod) - 1; i > 0; i-- {
		if pod[i] != '-' {
			continue
		}
		if svc, ok := ix.byKey[pod[:i]]; ok {
			return svc, svc != ""
		}
	}
	return "", false
}

// ── ifade ve parça ───────────────────────────────────────────────────────

// renderThanosExpr — SAF: aile ifadesi pod düzeyinde.
func renderThanosExpr(expr string, winSec int) string {
	return renderExpr(expr, "namespace, pod", winSec)
}

// thanosMetricPart — ml_series parçası / olay Code'u: "<aile>|<ifade>@<cluster>".
func thanosMetricPart(family, exprID, cluster string) string {
	return metricPart(family, exprID) + "@" + cluster
}

// splitThanosPart — SAF: parça → aile, ifade, cluster ("" = VM parçası).
func splitThanosPart(part string) (family, exprID, cluster string) {
	family, rest, _ := strings.Cut(part, "|")
	exprID, cluster, _ = strings.Cut(rest, "@")
	return family, exprID, cluster
}

// exprAgg — SAF: ifadenin dış toplayıcısı (pod → servis birleşimi).
// Kuantil ve oran gibi toplanamayan ifadeler en kötü pod'u (max) alır.
func exprAgg(expr string) string {
	e := strings.TrimSpace(expr)
	for _, a := range []string{"avg", "sum", "max"} {
		if strings.HasPrefix(e, a+" by") || strings.HasPrefix(e, a+"(") || strings.HasPrefix(e, a+" (") {
			if strings.Contains(e, ") / ") { // oran
				return "max"
			}
			return a
		}
	}
	return "max"
}

// combine — SAF.
func combine(agg string, vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	out := vals[0]
	sum := 0.0
	for _, v := range vals {
		sum += v
		if v > out {
			out = v
		}
	}
	switch agg {
	case "sum":
		return sum
	case "avg":
		return sum / float64(len(vals))
	}
	return out
}

// ── bütçe ────────────────────────────────────────────────────────────────

// thanosPlanned — bir cluster'da sorulacak ifade.
type thanosPlanned struct {
	family, id, expr, direction, agg string
}

// budgetPlan — SAF: cluster × tik tavanı; sıra korunur, fazlası sayılır.
func budgetPlan(exprs []thanosPlanned, capN int) ([]thanosPlanned, int) {
	if len(exprs) <= capN {
		return exprs, 0
	}
	return exprs[:capN], len(exprs) - capN
}

// thanosFamilyVariants — v0.10.2019: Settings ifadeleri Thanos'ta veri
// döndürmezse denenen yerleşik ifade setleri (aynı ifade kimlikleriyle,
// etiket/birim Settings'ten çözülür). Yalnız JVM: OpenShift'teki Java
// servisleri çoğunlukla Prometheus JMX exporter / client_java (hotspot)
// ya da Micrometer adlarını taşır; clusters katmanının JMX sekmesi de
// jvm_* ailesini keşfeder (thanos/promql.go jmxDiscoveryQuery).
var thanosFamilyVariants = map[string][][]chstore.MLMetricExpr{
	"metric_jvm": {
		{ // Prometheus client_java hotspot / JMX exporter
			{ID: "heap_used", Expr: "avg by ({svc}) (sum by ({svc}) (jvm_memory_bytes_used{area=\"heap\"}))", Direction: "up"},
			{ID: "gc_time", Expr: "sum by ({svc}) (rate(jvm_gc_collection_seconds_sum[{win}]))", Direction: "up"},
			{ID: "threads", Expr: "avg by ({svc}) (jvm_threads_current)", Direction: "up"},
		},
		{ // Micrometer
			{ID: "heap_used", Expr: "avg by ({svc}) (sum by ({svc}) (jvm_memory_used_bytes{area=\"heap\"}))", Direction: "up"},
			{ID: "gc_time", Expr: "sum by ({svc}) (rate(jvm_gc_pause_seconds_sum[{win}]))", Direction: "up"},
			{ID: "threads", Expr: "avg by ({svc}) (jvm_threads_live_threads)", Direction: "up"},
		},
	},
}

// familyVariantSets — SAF: Settings seti önce, sonra yerleşik varyantlar.
func familyVariantSets(cfg chstore.MLConfig, fam string) [][]chstore.MLMetricExpr {
	out := [][]chstore.MLMetricExpr{cfg.MetricFamilies[fam]}
	return append(out, thanosFamilyVariants[fam]...)
}

// variantPlan — SAF: bir cluster'ın seçili varyant setinden plan (toplayıcı
// Settings ifadesinden; yön Settings'ten, yoksa varyanttan).
func variantPlan(cfg chstore.MLConfig, fam string, set []chstore.MLMetricExpr, winSec int) []thanosPlanned {
	base := map[string]chstore.MLMetricExpr{}
	for _, e := range cfg.MetricFamilies[fam] {
		base[e.ID] = e
	}
	out := make([]thanosPlanned, 0, len(set))
	for _, e := range set {
		agg, dir := exprAgg(e.Expr), e.Direction
		if b, ok := base[e.ID]; ok {
			agg, dir = exprAgg(b.Expr), b.Direction
		}
		out = append(out, thanosPlanned{family: fam, id: e.ID, expr: renderThanosExpr(e.Expr, winSec), direction: dir, agg: agg})
	}
	return out
}
