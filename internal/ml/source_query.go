package ml

// source_query.go — v0.10.2036 (anomali detayı: "grafik servis sayfasının
// paneliyle", kalan olay türleri). Metrik ve bağımlılık olayının detay
// grafiği için DEDEKTÖRÜN KENDİ ifadesinin servis kapsamlı, 1 dakikalık
// aralık sorgusu. SAF: yapılandırma + olay kimliği → ifade; çalıştırma
// api katmanında (VM ya da Thanos, mevcut tavan ve zaman aşımıyla).
//
//   VM metrik ailesi ("aile|ifade")       → ailenin ifadesi, {svc} = servis
//     etiketi, her seçiciye servis="…" eklenir (tek seri: dedektörün ölçtüğü).
//   Thanos ("aile|ifade@cluster")          → v0.10.2060: Pods sekmesinin
//     sorgusu / pod kümesiyle, pod başına seri (thanosSourceQuery).
//   dependency_latency ("aile|hedef")      → hedefin p95'i (ms); hedef etiketi
//     dedektörün aday listesinden `or` ile (tek sorgu, ilk eşleşen etiket).
//
// İfade kullanıcıdan gelmez: aile ifadesi Settings'ten, hedef/servis değerleri
// matcher olarak kaçışlı eklenir.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// SourceQueryWinSec — aralık sorgusunda rate/increase penceresi (1 dk adımla
// en az iki örnek).
const SourceQueryWinSec = 120

// SourceQuery — detay grafiğinin tek sorgusu.
type SourceQuery struct {
	Expr    string `json:"expr"`
	Cluster string `json:"cluster,omitempty"` // Thanos cluster ADI; boş = VM
	Unit    string `json:"unit,omitempty"`
	Label   string `json:"label"`
	// By — seri kırılımının etiketi ("" = tek seri).
	By string `json:"by,omitempty"`
	// Panel — v0.10.2060: Thanos grafiğinin eşi olan Pods paneli (SourcePanel*); boş = dedektör ifadesi.
	Panel string `json:"panel,omitempty"`
}

// BuildSourceQuery — SAF. ok=false: bu olay için ifade kurulamaz (bilinmeyen
// aile/ifade, boş servis) — çağıran kayıtlı görüntüye düşer.
func BuildSourceQuery(cfg chstore.MLConfig, detector, code, service, svcLabel string, scope PodScope) (SourceQuery, bool) {
	if strings.TrimSpace(service) == "" {
		return SourceQuery{}, false
	}
	if svcLabel == "" {
		svcLabel = "service_name"
	}
	if detector == detDependencyLatency {
		return dependencySourceQuery(code, service, svcLabel)
	}
	fam, id, cluster := splitThanosPart(code)
	if fam != detector {
		return SourceQuery{}, false
	}
	var e *chstore.MLMetricExpr
	for i := range cfg.MetricFamilies[fam] {
		if cfg.MetricFamilies[fam][i].ID == id {
			e = &cfg.MetricFamilies[fam][i]
			break
		}
	}
	if e == nil {
		return SourceQuery{}, false
	}
	if cluster != "" {
		return thanosSourceQuery(cfg, *e, fam, cluster, service, scope), true
	}
	expr := thanos.WithEqualMatcher(renderExpr(e.Expr, svcLabel, SourceQueryWinSec), svcLabel, service)
	return SourceQuery{Expr: expr, Unit: e.Unit, Label: e.Label}, true
}

// PodScope — v0.10.2060: servisin pod kümesi, servis metadata'sından
// (Pods sekmesinin kullandığı namespace + deployment). Boş alan = bilinmiyor.
type PodScope struct{ Namespace, Deploy string }

// Thanos detay grafiğinin hangi Pods paneliyle aynı olduğu (istemci kaynak satırı).
const (
	SourcePanelPodsResource = "pods-resource" // Pods sekmesi CPU / Bellek paneli: aynı sorgu
	SourcePanelPods         = "pods"          // Pods sekmesinin pod kümesi (namespace + deployment)
)

// thanosSourceQuery — SAF (v0.10.2060). Thanos olayının detay grafiği,
// servis sayfasının Pods sekmesinin gördüğü pod kümesiyle:
//   - k8s CPU / bellek + namespace ve deployment biliniyor → Pods sekmesinin
//     CPU / Bellek paneli sorgusunun AYNISI (thanos.DeployTrendQuery, pod başına);
//   - diğer ifadeler → dedektörün ifadesi pod başına; JVM'de Settings ifadesi
//     ve yerleşik varyantlar (JMX exporter, Micrometer) `or` ile: cluster hangi
//     adlandırmayı taşıyorsa o döner (eski: yalnız Settings ifadesi → JMX
//     exporter'lı cluster'da seri boş, grafik sessizce kayıtlı görüntüye
//     düşüyordu);
//   - pod seçici: namespace + "<deployment>-" (Pods sekmesi); namespace
//     bilinmiyorsa servisin pod regex'i, deployment adı dahil (eski: deployment
//     adı yoktu, servis adından farklı adlı pod'lar kaçıyordu).
func thanosSourceQuery(cfg chstore.MLConfig, e chstore.MLMetricExpr, fam, cluster, service string, sc PodScope) SourceQuery {
	q := SourceQuery{Cluster: cluster, Unit: e.Unit, Label: e.Label, By: "pod"}
	scoped := sc.Namespace != "" && sc.Deploy != ""
	if scoped && fam == "metric_k8s" && (e.ID == "cpu" || e.ID == "memory") {
		m := "cpu"
		if e.ID == "memory" {
			m = "mem"
		}
		q.Expr, q.Panel = thanos.DeployTrendQuery(sc.Namespace, sc.Deploy, m, true), SourcePanelPodsResource
		return q
	}
	seen := map[string]bool{}
	var parts []string
	for _, set := range familyVariantSets(cfg, fam) {
		for _, x := range set {
			if x.ID != e.ID || seen[x.Expr] {
				continue
			}
			seen[x.Expr] = true
			expr := renderExpr(x.Expr, "pod", SourceQueryWinSec)
			if scoped {
				expr = thanos.WithRegexMatcher(thanos.WithEqualMatcher(expr, "namespace", sc.Namespace), "pod", regexp.QuoteMeta(sc.Deploy)+"-.*")
			} else {
				expr = thanos.WithRegexMatcher(expr, "pod", thanos.ServicePodRegex(service, sc.Deploy))
			}
			parts = append(parts, expr)
		}
	}
	q.Expr = strings.Join(parts, " or ")
	if scoped {
		q.Panel = SourcePanelPods
	}
	return q
}

// TracePodLabels — v0.10.2055: trace_pod_outlier'ın RED ailesinde keşfettiği
// etiket adları (servis, pod, durum). Olay kanıtında da yazılır.
type TracePodLabels struct{ Svc, Pod, Status string }

// PickTracePodLabels — SAF: ailenin `_count` metriğinin etiket adlarından
// dedektörün seçtiği üçlü (trace_pods.go famLabels ile aynı aday sırası).
// svcPreferred: VM LabelMap'in servis etiketi (önce denenir).
func PickTracePodLabels(names []string, svcPreferred string, pin anomaly.REDFamilyPin) (TracePodLabels, bool) {
	l := TracePodLabels{
		Svc:    pickLabel(names, append([]string{svcPreferred}, svcLabelCandidates...)),
		Pod:    pickLabel(names, podLabelCandidates),
		Status: pickLabel(names, pin.StatusLabels),
	}
	return l, l.Svc != "" && l.Pod != "" && l.Status != ""
}

// TracePodSourceQuery — SAF (v0.10.2055): trace_pod_outlier detay grafiği.
// Dedektörün ölçüsü (measure = olayın Code'u: err_ratio | p95) pod başına,
// servis kapsamlı aralık sorgusu: hata oranı yüzde, p95 ms. Seri kırılımı
// pod etiketi; sapan pod'u istemci adıyla vurgular.
func TracePodSourceQuery(pin anomaly.REDFamilyPin, l TracePodLabels, service, measure string) (SourceQuery, bool) {
	if strings.TrimSpace(service) == "" || l.Svc == "" || l.Pod == "" {
		return SourceQuery{}, false
	}
	w := fmt.Sprintf("[%ds]", SourceQueryWinSec)
	count := thanos.WithEqualMatcher(pin.Names+"_count", l.Svc, service)
	switch measure {
	case "err_ratio":
		if l.Status == "" {
			return SourceQuery{}, false
		}
		errSel := thanos.WithRegexMatcher(count, l.Status, pin.ServerErr)
		expr := fmt.Sprintf("100 * sum by (%s) (rate(%s%s)) / sum by (%s) (rate(%s%s))", l.Pod, errSel, w, l.Pod, count, w)
		return SourceQuery{Expr: expr, Unit: "percent", Label: "Pod başına hata oranı", By: l.Pod}, true
	case "p95":
		scale := 1.0
		if pin.Unit == "s" {
			scale = 1000
		}
		bucket := thanos.WithEqualMatcher(pin.Names+"_bucket", l.Svc, service)
		expr := fmt.Sprintf("histogram_quantile(0.95, sum by (%s, le) (rate(%s%s))) * %g", l.Pod, bucket, w, scale)
		return SourceQuery{Expr: expr, Unit: "ms", Label: "Pod başına p95", By: l.Pod}, true
	}
	return SourceQuery{}, false
}

func dependencySourceQuery(code, service, svcLabel string) (SourceQuery, bool) {
	famID, target, ok := strings.Cut(code, "|")
	if !ok || target == "" {
		return SourceQuery{}, false
	}
	var f *depFamily
	for i := range depFamilies {
		if depFamilies[i].ID == famID {
			f = &depFamilies[i]
			break
		}
	}
	if f == nil {
		return SourceQuery{}, false
	}
	parts := make([]string, 0, len(f.Targets))
	for _, tl := range f.Targets {
		sel := thanos.WithEqualMatcher(thanos.WithEqualMatcher(f.Metric+"_bucket", svcLabel, service), tl, target)
		parts = append(parts, fmt.Sprintf("histogram_quantile(0.95, sum by (le) (rate(%s[%ds]))) * %g", sel, SourceQueryWinSec, f.Scale))
	}
	return SourceQuery{Expr: strings.Join(parts, " or "), Unit: "ms", Label: target + " · p95"}, true
}
