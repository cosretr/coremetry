package api

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// rankSeries — anlık sonuç serisi: değer + etiket çiftleri.
func rankSeries(v float64, kv ...string) chstore.SpanMetricSeries {
	var gk []string
	for i := 0; i+1 < len(kv); i += 2 {
		gk = append(gk, kv[i]+`="`+kv[i+1]+`"`)
	}
	return chstore.SpanMetricSeries{GroupKey: gk, Points: []chstore.SpanMetricPoint{{Value: v}}}
}

func f64(v float64) *float64 { return &v }

// TestServicesMetricRankQueryFleet — filo geneli: servis seçicisi yok, durum
// sınıfı ilk etiket kazanacak sırada, gecikme yalnız avg/p99 sütununda.
func TestServicesMetricRankQueryFleet(t *testing.T) {
	f, _ := redFamilyByID(redFamilyHTTPLegacy)
	q := servicesMetricRankQuery(f, "service_name", "", 3600, "errorRate", false)
	if strings.Contains(q, `service_name=`) || strings.Contains(q, `service_name=~`) {
		t.Fatalf("servis seçicisi olmamalı: %s", q)
	}
	if !strings.HasPrefix(q, "sum by (service_name, cm_s) (") || strings.Contains(q, "cm_k") {
		t.Fatalf("hata sütunu yalnız sayı sınıfı: %s", q)
	}
	// İlk etiket (http_status_code) EN DIŞTA uygulanır → kazanır.
	if i1, i2 := strings.LastIndex(q, `"http_status_code"`), strings.LastIndex(q, `"http_response_status_code"`); i1 < i2 {
		t.Errorf("ilk durum etiketi en dışta olmalı: %s", q)
	}
	if !strings.Contains(q, `http_route!~`) {
		t.Error("tesisat rotaları varsayılan dışlanmalı")
	}
	if q2 := servicesMetricRankQuery(f, "service_name", "", 3600, "errorRate", true); strings.Contains(q2, "http_route!~") {
		t.Error("infra=1 dışlamayı kaldırmalı")
	}
	if p := servicesMetricRankQuery(f, "service_name", `deployment_environment="prod"`, 3600, "p99", false); !strings.Contains(p, `"cm_k", "p"`) || !strings.Contains(p, `deployment_environment="prod"`) {
		t.Errorf("p99 etiketli gecikme + env kapsamı: %s", p)
	}
}

// TestServicesMetricRankFold — sınıflar → istek, hata oranı (durum yoksa nil), gecikme.
func TestServicesMetricRankFold(t *testing.T) {
	f, _ := redFamilyByID(redFamilyHTTP)
	ser := []chstore.SpanMetricSeries{
		rankSeries(90, "service_name", "svc-a", "cm_s", "ok"),
		rankSeries(10, "service_name", "svc-a", "cm_s", "e"),
		rankSeries(50, "service_name", "svc-b"), // durum etiketi yok
		rankSeries(250, "service_name", "svc-a", "cm_k", "p"),
		rankSeries(0, "service_name", "svc-c", "cm_s", "ok"), // istek yok → dışarıda
	}
	er := servicesMetricRankFold(f, "service_name", "errorRate", ser)
	if a := er["svc-a"]; a.Source != redFamilyHTTP || a.Requests != 100 || a.Value == nil || *a.Value != 10 {
		t.Errorf("svc-a: %+v", a)
	}
	if b := er["svc-b"]; b.Requests != 50 || b.Value != nil {
		t.Errorf("svc-b durum etiketsiz hata oranı nil olmalı: %+v", b)
	}
	if _, ok := er["svc-c"]; ok {
		t.Error("istek 0 servis girmemeli")
	}
	if p := servicesMetricRankFold(f, "service_name", "p99", ser); p["svc-a"].Value == nil || *p["svc-a"].Value != 250 || p["svc-b"].Value != nil {
		t.Errorf("p99: %+v", p)
	}
	if n := servicesMetricRankFold(f, "service_name", "spanCount", ser); *n["svc-b"].Value != 50 {
		t.Errorf("istek: %+v", n)
	}
}

// TestServicesMetricRankMerge — metrik değerli servisler önde (dir), kalanlar
// evren (span) sırasıyla; eşitlikte evren sırası; evren dışı metrik servisi yok.
func TestServicesMetricRankMerge(t *testing.T) {
	uni := []chstore.ServiceSummary{{Name: "s1"}, {Name: "s2"}, {Name: "s3"}, {Name: "s4"}, {Name: "s5"}}
	rank := map[string]serviceMetricRankEntry{
		"s2":    {Value: f64(5)},
		"s4":    {Value: f64(50)},
		"s5":    {Value: f64(5)},
		"s3":    {Value: nil}, // ölçülemiyor → span grubu
		"other": {Value: f64(999)},
	}
	names := func(rows []chstore.ServiceSummary) string {
		var out []string
		for _, r := range rows {
			out = append(out, r.Name)
		}
		return strings.Join(out, ",")
	}
	got, n := servicesMetricRankMerge(uni, rank, "desc")
	if names(got) != "s4,s2,s5,s1,s3" || n != 3 {
		t.Errorf("desc: %s n=%d", names(got), n)
	}
	got, _ = servicesMetricRankMerge(uni, rank, "asc")
	if names(got) != "s2,s5,s4,s1,s3" {
		t.Errorf("asc (metrik grubu yine önde): %s", names(got))
	}
	if uni[0].Name != "s1" || uni[3].Name != "s4" {
		t.Error("evren yerinde değişmemeli")
	}
	page, more := servicesMetricRankPage(got, 2, 2)
	if names(page) != "s4,s1" || !more {
		t.Errorf("sayfa: %s %v", names(page), more)
	}
	if page, more = servicesMetricRankPage(got, 4, 2); names(page) != "s3" || more {
		t.Errorf("son sayfa: %s %v", names(page), more)
	}
	if page, more = servicesMetricRankPage(got, 9, 2); len(page) != 0 || more {
		t.Error("taşan offset boş")
	}
}

// TestServicesMetricRankKey — anahtar TÜM girdileri taşır (v0.5.187).
func TestServicesMetricRankKey(t *testing.T) {
	to := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	lm := vmetrics.LabelMap{Service: "service_name"}
	base := servicesMetricRankKey("p99", "prod", "cl-a", from, to, lm, false)
	variants := []string{
		servicesMetricRankKey("avg", "prod", "cl-a", from, to, lm, false),
		servicesMetricRankKey("p99", "stage", "cl-a", from, to, lm, false),
		servicesMetricRankKey("p99", "prod", "cl-b", from, to, lm, false),
		servicesMetricRankKey("p99", "prod", "cl-a", from.Add(-time.Hour), to, lm, false),
		servicesMetricRankKey("p99", "prod", "cl-a", from, to, vmetrics.LabelMap{Service: "svc"}, false),
		servicesMetricRankKey("p99", "prod", "cl-a", from, to, vmetrics.LabelMap{Service: "service_name", Env: "env"}, false),
		servicesMetricRankKey("p99", "prod", "cl-a", from, to, lm, true),
	}
	for i, v := range variants {
		if v == base {
			t.Errorf("varyant %d anahtarı değiştirmedi", i)
		}
	}
	if servicesMetricRankKey("p99", "prod", "cl-a", from, to, lm, false) != base {
		t.Error("aynı girdi aynı anahtar")
	}
}

func TestServicesMetricRankEligible(t *testing.T) {
	if ok, _ := servicesMetricRankEligible("p99", time.Hour); !ok {
		t.Error("p99 1 sa uygun")
	}
	if ok, why := servicesMetricRankEligible("errorRate", 25*time.Hour); ok || why == "" {
		t.Error("24 sa üstü geri düşer, nedenle")
	}
	if ok, why := servicesMetricRankEligible("apdex", time.Hour); ok || why != "" {
		t.Error("metrik dışı sütun sessizce span")
	}
}

// rankReader — aile başına sabit filo sonucu; failID ailesi hata döner.
type rankReader struct {
	famReader
	byFam  map[string][]chstore.SpanMetricSeries
	failID string
	mu     sync.Mutex
	n      int
}

func (r *rankReader) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.n++
	r.mu.Unlock()
	for _, f := range redFamilies {
		hit := strings.Contains(q, f.VMBase) || (f.ID == redFamilyHTTP && strings.Contains(q, "http_server_request_duration_seconds_count"))
		if !hit {
			continue
		}
		if f.ID == r.failID {
			return nil, errors.New("timeout")
		}
		return r.byFam[f.ID], nil
	}
	return nil, nil
}

// TestServicesMetricRankBuild — aile başına TEK sorgu; servis ilk istek veren
// ailede kalır; bir aile okunamazsa kısmi sıra YOK (hata + not).
func TestServicesMetricRankBuild(t *testing.T) {
	to := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	rd := &rankReader{byFam: map[string][]chstore.SpanMetricSeries{
		redFamilyHTTP:        {rankSeries(100, "service_name", "svc-a", "cm_s", "ok")},
		redFamilySpanMetrics: {rankSeries(7, "service_name", "svc-a", "cm_s", "ok"), rankSeries(3, "service_name", "svc-b", "cm_s", "e")},
	}}
	s := &Server{}
	res, notes, err := s.buildServicesMetricRanking(context.Background(), rd, "spanCount", "", "", from, to, false)
	if err != nil || len(notes) != 0 {
		t.Fatalf("err=%v notes=%v", err, notes)
	}
	if res.Queries != len(redFamilies) || rd.n != len(redFamilies) {
		t.Errorf("aile başına 1 sorgu: %d / %d", res.Queries, rd.n)
	}
	if a := res.Services["svc-a"]; a.Source != redFamilyHTTP || *a.Value != 100 {
		t.Errorf("öncelik (uygulama histogramı > connector, toplanmaz): %+v", a)
	}
	if b := res.Services["svc-b"]; b.Source != redFamilySpanMetrics {
		t.Errorf("svc-b: %+v", b)
	}
	rd.failID = redFamilyGRPCStable
	if _, notes, err = s.buildServicesMetricRanking(context.Background(), rd, "spanCount", "", "", from, to, false); err == nil || len(notes) != 1 {
		t.Errorf("kısmi okuma hata olmalı: %v %v", err, notes)
	}
}
