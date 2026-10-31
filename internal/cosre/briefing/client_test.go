package briefing

// client_test.go — v0.10.1712 kalem 2 (client_metrics_diff; Q11 Faz 1 =
// sunucu aileleri + Kafka Go yolu). Nötr fikstür: orders-api, payments-core.

import (
	"math"
	"strings"
	"testing"
)

func TestSplitMeans(t *testing.T) {
	cases := []struct {
		name            string
		pts             [][2]float64
		from            int64
		cur, prev       float64
		hasCur, hasPrev bool
	}{
		{"iki pencere", [][2]float64{{1000, 2}, {2000, 4}, {3000, 10}, {4000, 20}}, 3000, 15, 3, true, true},
		{"yalnız cur", [][2]float64{{3000, 5}}, 3000, 5, 0, true, false},
		{"yalnız prev", [][2]float64{{1000, 5}}, 3000, 0, 5, false, true},
		{"NaN atlanır", [][2]float64{{1000, math.NaN()}, {3000, math.Inf(1)}, {3500, 1}}, 3000, 1, 0, true, false},
		{"boş", nil, 3000, 0, 0, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur, prev, hc, hp := SplitMeans(c.pts, c.from)
			if cur != c.cur || prev != c.prev || hc != c.hasCur || hp != c.hasPrev {
				t.Fatalf("got %v %v %v %v", cur, prev, hc, hp)
			}
		})
	}
}

func TestClientFindings(t *testing.T) {
	th := DefaultThresholds() // ClientX 2, ErrRateMinPct 1, MinSpans 200
	vol := func(r ClientRow) ClientRow {
		r.VolNow, r.VolPrev, r.HasNow, r.HasPrev = 5000, 5000, true, true
		return r
	}
	kaf := func(r ClientRow) ClientRow { r.Family, r.HasNow, r.HasPrev = FamilyKafka, true, true; return r }
	cases := []struct {
		name     string
		row      ClientRow
		wantKind string // "" = bulgu yok
		wantText string
	}{
		{"http 5xx kat", vol(ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientErrPct, Now: 4, Prev: 1}), "http_err_pct", "HTTP 5xx oranı %4.0 (önceki %1.0, 4.0×)"},
		{"grpc hata yeni", vol(ClientRow{Service: "orders-api", Family: FamilyGRPC, Metric: ClientErrPct, Now: 2, Prev: 0}), "grpc_err_pct", "gRPC sunucu-hatası oranı %2.0 (önceki pencerede yoktu)"},
		{"5xx kat altı", vol(ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientErrPct, Now: 3, Prev: 2}), "", ""},
		{"5xx mutlak taban altı", vol(ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientErrPct, Now: 0.5, Prev: 0.1}), "", ""},
		{"5xx az hacim", ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientErrPct, Now: 50, Prev: 1, HasNow: true, HasPrev: true, VolNow: 10, VolPrev: 5000}, "", ""},
		{"prev okunmadı → kıyas yok", ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientErrPct, Now: 9, HasNow: true, VolNow: 5000, VolPrev: 5000}, "", ""},
		{"p95 kat (ms çevrimi)", vol(ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientLatency, Now: 1.2, Prev: 0.3}), "http_latency_p95", "HTTP p95 1.2 s (önceki 300 ms, 4.0×)"},
		{"p95 taban altı", vol(ClientRow{Service: "orders-api", Family: FamilyHTTP, Metric: ClientLatency, Now: 0.03, Prev: 0.01}), "", ""},
		{"kafka gönderim hatası yeni", kaf(ClientRow{Service: "payments-core", Metric: ClientSendErr, Label: "topic=orders", Now: 0.5}), "kafka_send_err", "gönderim hatası 0.50/s (önceki pencerede yoktu) · topic=orders"},
		{"kafka gönderim hatası taban altı", kaf(ClientRow{Service: "payments-core", Metric: ClientSendErr, Now: 0.001}), "", ""},
		{"kafka lag kat", kaf(ClientRow{Service: "payments-core", Metric: ClientLag, Label: "topic=orders", Now: 12000, Prev: 1500}), "kafka_lag", "lag 12000 (önceki 1500, 8.0×) · topic=orders"},
		{"kafka lag taban altı", kaf(ClientRow{Service: "payments-core", Metric: ClientLag, Now: 900, Prev: 100}), "", ""},
		{"aile/ölçü uyumsuz", kaf(ClientRow{Service: "payments-core", Metric: ClientErrPct, Now: 9, Prev: 1}), "", ""},
		{"bilinmeyen aile", vol(ClientRow{Service: "orders-api", Family: "amqp", Metric: ClientErrPct, Now: 9, Prev: 1}), "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClientFindings([]ClientRow{c.row}, th, "prod", 10800)
			if c.wantKind == "" {
				if len(got) != 0 {
					t.Fatalf("bulgu beklenmiyordu: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Kind != c.wantKind || got[0].Item != ItemClient || !strings.Contains(got[0].Text, c.wantText) {
				t.Fatalf("got %+v", got)
			}
			wantTab := "tab=http"
			if c.row.Family == FamilyKafka {
				wantTab = "tab=messaging"
			}
			if !strings.Contains(got[0].PageURL, wantTab) || !strings.Contains(got[0].PageURL, "env=prod") {
				t.Fatalf("pivot %q", got[0].PageURL)
			}
		})
	}
}
