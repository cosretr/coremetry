package api

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// redListReader — sorguyu aileye göre yanıtlar: fams[aile kimliği] = servis →
// (durum → sayı). p99 / ortalama sorguları servis başına sabit değer döner.
type redListReader struct {
	famReader
	fams map[string]map[string]map[string]float64
	mu   sync.Mutex
	n    int
	qs   []string
}

func (r *redListReader) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.n++
	r.qs = append(r.qs, q)
	r.mu.Unlock()
	for id, bySvc := range r.fams {
		f, _ := redFamilyByID(id)
		if !strings.Contains(q, f.VMBase) && !(id == redFamilyHTTP && strings.Contains(q, "http_server_request_duration_seconds_count")) {
			continue
		}
		var out []chstore.SpanMetricSeries
		for svc, byStatus := range bySvc {
			if !strings.Contains(q, svc) {
				continue
			}
			if strings.HasPrefix(q, "sum by (service_name, ") {
				for st, v := range byStatus {
					out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + svc + `"`, f.StatusLabels[0] + `="` + st + `"`},
						Points: []chstore.SpanMetricPoint{{Value: v}}})
				}
				continue
			}
			out = append(out, chstore.SpanMetricSeries{GroupKey: []string{`service_name="` + svc + `"`}, Points: []chstore.SpanMetricPoint{{Value: 120}}})
		}
		return out, nil
	}
	return nil, nil
}

// TestServicesREDMetricPrecedence — v0.10.2434: Services listesi servis başına
// İLK veri veren aileyi kullanır (uygulama histogramı > connector), sonraki
// turlar yalnız çözülmemişleri sorar; hata = 5xx / STATUS_CODE_ERROR (4xx değil);
// metrikte olmayan servis cevapta yok (span'e düşer). Sorgu sayısı ölçülür.
func TestServicesREDMetricPrecedence(t *testing.T) {
	to := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	rd := &redListReader{fams: map[string]map[string]map[string]float64{
		redFamilyHTTP:       {"orders-api": {"200": 900, "404": 50, "503": 50}},
		redFamilyHTTPLegacy: {"legacy-billing-api": {"200": 99, "500": 1}},
		// connector her servisi yazar — orders-api'nin connector satırı KULLANILMAZ.
		redFamilySpanMetrics: {"orders-api": {"STATUS_CODE_ERROR": 999}, "mainframe-adapter": {"STATUS_CODE_UNSET": 190, "STATUS_CODE_ERROR": 10}},
	}}
	s := &Server{}
	names := servicesREDMetricNames("orders-api, legacy-billing-api,mainframe-adapter,batch-loader,orders-api")
	if len(names) != 4 {
		t.Fatalf("tekil ad kümesi: %v", names)
	}
	res := s.buildServicesREDMetric(context.Background(), rd, names, "", "", from, to, false)
	want := map[string]struct {
		src string
		err float64
	}{
		"orders-api":         {redFamilyHTTP, 5},
		"legacy-billing-api": {redFamilyHTTPLegacy, 1},
		"mainframe-adapter":  {redFamilySpanMetrics, 5},
	}
	for svc, w := range want {
		r, ok := res.Services[svc]
		if !ok || r.Source != w.src || r.ErrorRate == nil || *r.ErrorRate != w.err {
			t.Errorf("%s: %+v (want %s %.1f%%)", svc, r, w.src, w.err)
		}
		if r.P99Ms == nil || r.AvgMs == nil {
			t.Errorf("%s: gecikme eksik %+v", svc, r)
		}
	}
	if _, ok := res.Services["batch-loader"]; ok {
		t.Error("metrikte olmayan servis cevapta olmamalı (span'e düşer)")
	}
	if r := res.Services["orders-api"]; r.RatePerS != 1000.0/3600 {
		t.Errorf("req/s: %v", r.RatePerS)
	}
	// 5 aile × 3 sorgu = 15 (en kötü); turlar yalnız çözülmemişleri sorar.
	if res.Queries != 15 || rd.n != 15 {
		t.Errorf("sorgu sayısı: %d / %d", res.Queries, rd.n)
	}
	for _, q := range rd.qs {
		if strings.Contains(q, "traces_span_metrics") {
			if strings.Contains(q, "orders-api") || !strings.Contains(q, `span_kind=~"SPAN_KIND_SERVER|SPAN_KIND_CONSUMER"`) {
				t.Errorf("connector turu çözülmüş servisi sormamalı ve giriş span'lerine kapsanmalı: %s", q)
			}
		}
		if strings.Contains(q, "http_server_duration_milliseconds_bucket") && !strings.HasSuffix(q, " * 1") {
			t.Errorf("ms ailesi ölçeklenmemeli: %s", q)
		}
	}
	// Hepsi ilk turda çözülürse tek tur = 3 sorgu.
	rd2 := &redListReader{fams: map[string]map[string]map[string]float64{redFamilyHTTP: {"orders-api": {"200": 10}}}}
	if r := s.buildServicesREDMetric(context.Background(), rd2, []string{"orders-api"}, "", "", from, to, false); r.Queries != 3 {
		t.Errorf("en iyi durum 3 sorgu: %d", r.Queries)
	}
}

func TestServicesREDMetricKeyHashesAllInputs(t *testing.T) {
	to := time.Now()
	from := to.Add(-time.Hour)
	a := servicesREDMetricKey([]string{"a", "b"}, "", "", from, to, rd0LM(), false)
	for _, b := range []string{
		servicesREDMetricKey([]string{"a", "c"}, "", "", from, to, rd0LM(), false),
		servicesREDMetricKey([]string{"a", "b"}, "prod-ist", "", from, to, rd0LM(), false),
		servicesREDMetricKey([]string{"a", "b"}, "", "ist", from, to, rd0LM(), false),
	} {
		if a == b {
			t.Errorf("anahtar girdiyi taşımıyor: %s", a)
		}
	}
	if len(servicesREDMetricNames(strings.Repeat("x,", 0)+strings.Join(make([]string, 0), ","))) != 0 {
		t.Error("boş küme")
	}
}

func rd0LM() vmetrics.LabelMap { return vmetrics.LabelMap{} }
