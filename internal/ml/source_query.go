package ml

// source_query.go — v0.10.2036 (anomali detayı: "grafik servis sayfasının
// paneliyle", kalan olay türleri). Metrik ve bağımlılık olayının detay
// grafiği için DEDEKTÖRÜN KENDİ ifadesinin servis kapsamlı, 1 dakikalık
// aralık sorgusu. SAF: yapılandırma + olay kimliği → ifade; çalıştırma
// api katmanında (VM ya da Thanos, mevcut tavan ve zaman aşımıyla).
//
//   VM metrik ailesi ("aile|ifade")       → ailenin ifadesi, {svc} = servis
//     etiketi, her seçiciye servis="…" eklenir (tek seri: dedektörün ölçtüğü).
//   Thanos ("aile|ifade@cluster")          → aynı ifade pod kırılımıyla, her
//     seçiciye pod=~<servis pod regex'i> (Clusters katmanının pod kümesi);
//     pod başına seri.
//   dependency_latency ("aile|hedef")      → hedefin p95'i (ms); hedef etiketi
//     dedektörün aday listesinden `or` ile (tek sorgu, ilk eşleşen etiket).
//
// İfade kullanıcıdan gelmez: aile ifadesi Settings'ten, hedef/servis değerleri
// matcher olarak kaçışlı eklenir.

import (
	"fmt"
	"strings"

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
}

// BuildSourceQuery — SAF. ok=false: bu olay için ifade kurulamaz (bilinmeyen
// aile/ifade, boş servis) — çağıran kayıtlı görüntüye düşer.
func BuildSourceQuery(cfg chstore.MLConfig, detector, code, service, svcLabel string) (SourceQuery, bool) {
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
		expr := thanos.WithRegexMatcher(renderExpr(e.Expr, "pod", SourceQueryWinSec), "pod", thanos.ServicePodRegex(service, ""))
		return SourceQuery{Expr: expr, Cluster: cluster, Unit: e.Unit, Label: e.Label, By: "pod"}, true
	}
	expr := thanos.WithEqualMatcher(renderExpr(e.Expr, svcLabel, SourceQueryWinSec), svcLabel, service)
	return SourceQuery{Expr: expr, Unit: e.Unit, Label: e.Label}, true
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
