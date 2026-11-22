package api

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func metricOnlyNames(rows []chstore.ServiceSummary) string {
	var out []string
	for _, r := range rows {
		n := r.Name
		if r.MetricOnly {
			n += "*"
		}
		out = append(out, n)
	}
	return strings.Join(out, ",")
}

// TestServicesMetricOnlyRows — evrende olmayan, istek veren metrik servisleri;
// istek azalan, eşitlikte ad; ad / izin listesi / span süzgeci kuralları.
func TestServicesMetricOnlyRows(t *testing.T) {
	uni := []chstore.ServiceSummary{{Name: "svc-a"}, {Name: "svc-b"}}
	rank := map[string]serviceMetricRankEntry{
		"svc-a":      {Requests: 100, Value: f64(100)}, // span'i var → eklenmez
		"svc-metric": {Requests: 40, Value: f64(40)},
		"Svc-Batch":  {Requests: 40},
		"svc-quiet":  {Requests: 0}, // istek yok
		"other":      {Requests: 5},
	}
	got := servicesMetricOnlyRows(uni, rank, servicesMetricOnlyFilter{})
	if metricOnlyNames(got) != "Svc-Batch*,svc-metric*,other*" {
		t.Fatalf("sıra: %s", metricOnlyNames(got))
	}
	if got[0].SpanCount != 0 || got[0].Apdex != 0 {
		t.Errorf("span alanları sıfır olmalı: %+v", got[0])
	}
	if g := servicesMetricOnlyRows(uni, rank, servicesMetricOnlyFilter{NameMatch: "SVC-"}); metricOnlyNames(g) != "Svc-Batch*,svc-metric*" {
		t.Errorf("ad alt-dizesi büyük/küçük harf duyarsız: %s", metricOnlyNames(g))
	}
	allow := servicesMetricOnlyAllow([]string{"svc-metric", "svc-a"}, true)
	if g := servicesMetricOnlyRows(uni, rank, servicesMetricOnlyFilter{Allow: allow}); metricOnlyNames(g) != "svc-metric*" {
		t.Errorf("takım / namespace izin listesi: %s", metricOnlyNames(g))
	}
	if servicesMetricOnlyAllow(nil, false) != nil {
		t.Error("süzgeç yoksa izin listesi nil")
	}
	if g := servicesMetricOnlyAllow(nil, true); g == nil || len(servicesMetricOnlyRows(uni, rank, servicesMetricOnlyFilter{Allow: g})) != 0 {
		t.Error("boş izin listesi hiçbir metrik-yalnız servisi geçirmez")
	}
	if g := servicesMetricOnlyRows(uni, rank, servicesMetricOnlyFilter{SpanDisplay: true}); len(g) != 0 {
		t.Error("errorsOnly / minSpans / minP99 açıkken metrik-yalnız yok")
	}
}

// TestServicesWithMetricOnlyMerge — evren + ek satırlar tavanla; merge'de
// metrik değerli ek satır metrik grubuna girer, değersiz olan sona.
func TestServicesWithMetricOnlyMerge(t *testing.T) {
	uni := []chstore.ServiceSummary{{Name: "s1"}, {Name: "s2"}}
	extra := []chstore.ServiceSummary{{Name: "m1", MetricOnly: true}, {Name: "m2", MetricOnly: true}}
	all, cut := servicesWithMetricOnly(uni, extra, 3)
	if metricOnlyNames(all) != "s1,s2,m1*" || cut != 1 {
		t.Fatalf("tavan: %s cut=%d", metricOnlyNames(all), cut)
	}
	if all, cut = servicesWithMetricOnly(uni, extra, 1); metricOnlyNames(all) != "s1,s2" || cut != 2 {
		t.Errorf("evren tavanı zaten aşıyorsa ek yok: %s cut=%d", metricOnlyNames(all), cut)
	}
	all, _ = servicesWithMetricOnly(uni, extra, 10)
	if metricOnlyNames(uni) != "s1,s2" {
		t.Error("evren yerinde değişmemeli")
	}
	rank := map[string]serviceMetricRankEntry{"s2": {Value: f64(5)}, "m1": {Value: f64(50)}, "m2": {Requests: 3}}
	got, n := servicesMetricRankMerge(all, rank, "desc")
	if metricOnlyNames(got) != "m1*,s2,s1,m2*" || n != 2 {
		t.Errorf("merge: %s n=%d", metricOnlyNames(got), n)
	}
	page, more := servicesMetricRankPage(got, 2, 2)
	if metricOnlyNames(page) != "s1,m2*" || more {
		t.Errorf("sayfa: %s %v", metricOnlyNames(page), more)
	}
}

func TestMetricRankNames(t *testing.T) {
	rank := map[string]serviceMetricRankEntry{"b": {Requests: 1}, "a": {Requests: 2}, "z": {Requests: 0}, "": {Requests: 9}}
	if got := strings.Join(metricRankNames(rank, 10), ","); got != "a,b" {
		t.Errorf("adlar: %s", got)
	}
	if got := metricRankNames(rank, 1); len(got) != 1 || got[0] != "a" {
		t.Errorf("tavan: %v", got)
	}
}

// TestMetricServiceCatalogueNoVM — VM kaynak değilse katalog nil (yalnız span).
func TestMetricServiceCatalogueNoVM(t *testing.T) {
	if got := (&Server{}).metricServiceCatalogue(t.Context()); got != nil {
		t.Errorf("VM yokken nil: %v", got)
	}
}
