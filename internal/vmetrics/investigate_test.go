package vmetrics

// v0.10.944 — CoSRE araç kabiliyetlerinin SAF yarısı ve etiket uçlarının
// isPartial'ı. Sentetik adlar.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ResultUnit — sonucun birimi ad + agregasyondan: açıkça seçilmiş `_count` /
// `_bucket` (yüzdeliksiz) gözlem SAYISIDIR, ailenin `_seconds` eki ona birim
// veremez. Katalog birimi (describeMetricName) değişmedi — describe_test.go
// `_count`'u "s"ye bilerek pinler.
func TestResultUnit(t *testing.T) {
	tests := []struct {
		name, metric, agg, want string
	}{
		{"_count + sum", "http_server_request_duration_seconds_count", "sum", ""},
		{"_count + p95", "http_server_request_duration_seconds_count", "p95", ""},
		{"_bucket + sum", "http_server_request_duration_seconds_bucket", "sum", ""},
		{"_bucket + p95", "http_server_request_duration_seconds_bucket", "p95", "s"},
		{"_sum + sum", "http_server_request_duration_seconds_sum", "sum", "s"},
		{"aile + avg", "http_server_request_duration_seconds", "avg", "s"},
		{"aile + p99", "http_server_request_duration_seconds", "p99", "s"},
		{"cpu saniye sayacı", "process_cpu_seconds_total", "max", "s"},
		{"birimsiz _count", "queue_message_count", "sum", ""},
		{"bayt _sum", "batch_bytes_sum", "sum", "B"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResultUnit(tt.metric, tt.agg); got != tt.want {
				t.Fatalf("ResultUnit(%q, %q) = %q, beklenen %q", tt.metric, tt.agg, got, tt.want)
			}
		})
	}
	// Katalog birimi değişmedi (iki kural ayrı yaşar).
	if u, _ := describeMetricName("http_server_request_duration_seconds_count"); u != "s" {
		t.Fatalf("katalog birimi değişti: %q", u)
	}
}

// Etiket uçlarının isPartial'ı keşif yöntemlerine ulaşır; eski yöntemler
// (MetricAttrKeys / MetricLabelValues) bayrağı düşürür, sözleşmeleri aynı.
func TestLabelDiscoveryCarriesIsPartial(t *testing.T) {
	partial := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flag := ""
		if partial {
			flag = `"isPartial":true,`
		}
		switch {
		case r.URL.Path == "/api/v1/labels":
			_, _ = w.Write([]byte(`{"status":"success",` + flag + `"data":["__name__","job"]}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/label/"):
			_, _ = w.Write([]byte(`{"status":"success",` + flag + `"data":["checkout","payments"]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	from := time.Unix(1700000000, 0)
	names, p, err := s.MetricLabelNamesIn(context.Background(), "up", from, from.Add(time.Hour))
	if err != nil || !p || strings.Join(names, ",") != "job" {
		t.Fatalf("etiket adları: %v partial=%v err=%v", names, p, err)
	}
	vals, p, err := s.MetricLabelValuesIn(context.Background(), "up", "job", from, from.Add(time.Hour), 10)
	if err != nil || !p || len(vals) != 2 {
		t.Fatalf("etiket değerleri: %v partial=%v err=%v", vals, p, err)
	}
	// Eski yöntemler bayraksız, aynı değerler.
	if keys, err := s.MetricAttrKeys(context.Background(), "up", "", time.Hour); err != nil || strings.Join(keys, ",") != "job" {
		t.Fatalf("MetricAttrKeys sözleşmesi: %v %v", keys, err)
	}
	if vs, err := s.MetricLabelValues(context.Background(), "up", "job", time.Hour, "", 10); err != nil || len(vs) != 2 {
		t.Fatalf("MetricLabelValues sözleşmesi: %v %v", vs, err)
	}
	partial = false
	if _, p, err := s.MetricLabelNamesIn(context.Background(), "up", from, from.Add(time.Hour)); err != nil || p {
		t.Fatalf("isPartial yokken kısmi: %v %v", p, err)
	}
}
