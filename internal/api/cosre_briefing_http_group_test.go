package api

// v0.10.1719 — Login Briefing kalem 2: HTTP/gRPC sunucu ailesi takımın TÜM
// servisleri için TEK sorgu kümesi (1712: yalnız en yoğun servis). Pinler:
// sorgu sayısı (R9 ≤ 6 VM / takım × env), servis regex kaçışı (QuoteMeta +
// dize kaçışı), by (svc) ve topk YOK, küme tavanları, seri → satır eşlemesi.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

func TestBriefingHTTPGroupQueries(t *testing.T) {
	svcs := []string{"orders-api", "pay.core", `odd"svc`, `back\slash`, "a|b"}
	for _, tc := range []struct {
		name   string
		fam    redFamily
		status string
		want   []string // tür sırası
	}{
		{"HTTP + durum etiketi", redFamilies[0], "http_response_status_code", []string{svcHTTPREDRate, svcHTTPRED5xx, svcHTTPREDLatency}},
		{"durum etiketi yok → hata sorgusu yok", redFamilies[0], "", []string{svcHTTPREDRate, svcHTTPREDLatency}},
		{"gRPC (ms ölçeği)", redFamilies[1], "rpc_grpc_status_code", []string{svcHTTPREDRate, svcHTTPRED5xx, svcHTTPREDLatency}},
	} {
		qs := briefingHTTPGroupQueries(tc.fam, svcs, "service_name", `deployment_environment="prod"`, tc.status, 240)
		var kinds []string
		for _, q := range qs {
			kinds = append(kinds, q.Kind)
			for _, must := range []string{
				// sıralı + tekil; her değer QuoteMeta, sonra yalnız \ ve " dize kaçışı.
				`service_name=~"a\\|b|back\\\\slash|odd\"svc|orders-api|pay\\.core"`,
				`deployment_environment="prod"`,
			} {
				if !strings.Contains(q.Query, must) {
					t.Errorf("%s/%s: %q yok:\n%s", tc.name, q.Kind, must, q.Query)
				}
			}
			if strings.Contains(q.Query, "topk") {
				t.Errorf("%s/%s: topk aralık sorgusunda servis serisini deler: %s", tc.name, q.Kind, q.Query)
			}
			by := "by (service_name)"
			if q.Kind == svcHTTPREDLatency {
				by = "by (le, service_name)"
			}
			if !strings.Contains(q.Query, by) {
				t.Errorf("%s/%s: %q yok: %s", tc.name, q.Kind, by, q.Query)
			}
		}
		if strings.Join(kinds, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: türler %v want %v", tc.name, kinds, tc.want)
		}
		if n := briefingClientVMQueries(qs); n > briefingR9VMMax {
			t.Errorf("%s: R9 aşıldı: %d VM sorgusu", tc.name, n)
		}
	}
	if briefingHTTPGroupQueries(redFamilies[0], nil, "service_name", "", "x", 60) != nil {
		t.Error("boş küme sorgu üretti")
	}
	if briefingHTTPGroupQueries(redFamilies[0], svcs, "", "", "x", 60) != nil {
		t.Error("servis etiketi yokken süzgeçsiz (filo) sorgu üretti")
	}
	// Kafka soru sayısı sabiti katalogla eş.
	if len(briefingKafkaQuestions()) != briefingKafkaVMQueries {
		t.Errorf("Kafka soru sayısı %d, sabit %d", len(briefingKafkaQuestions()), briefingKafkaVMQueries)
	}
}

func TestBriefingHTTPGroupServices(t *testing.T) {
	many := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, fmt.Sprintf("svc-%02d", i))
	}
	long := strings.Repeat("x", briefingHTTPGroupRegexMax-5)
	for _, tc := range []struct {
		name string
		in   []string
		want int
		head string
	}{
		{"tavan", many, briefingHTTPGroupMax, "svc-00"},
		{"boş ve kontrol karakteri atılır, tekil", []string{"", "orders-api", "bad\nname", "orders-api", "payments-core"}, 2, "orders-api"},
		{"regex bayt tavanı", []string{long, "orders-api"}, 1, long},
		{"boş", nil, 0, ""},
	} {
		got := briefingHTTPGroupServices(tc.in)
		if len(got) != tc.want || (tc.want > 0 && got[0] != tc.head) {
			t.Errorf("%s: %d servis %v", tc.name, len(got), got)
		}
	}
}

func TestBriefingHTTPGroupRows(t *testing.T) {
	const from = 3_000_000 // ms
	ser := func(val float64, prev float64, kv ...string) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: kv, Points: []chstore.SpanMetricPoint{{Time: (from - 1000) * 1e6, Value: prev}, {Time: from * 1e6, Value: val}}}
	}
	by := map[string][]chstore.SpanMetricSeries{
		svcHTTPREDRate: {ser(2, 1, `service_name="orders-api"`), ser(5, 5, `service_name="outsider"`)},
		svcHTTPRED5xx: {ser(3, 0.5, `service_name="orders-api"`), ser(4, 1, `service_name="payments-core"`),
			ser(9, 1, `service_name="outsider"`)},
		svcHTTPREDLatency: {ser(0.4, 0.1, `phi="0.95"`, `service_name="orders-api"`), ser(2, 1, `phi="0.99"`, `service_name="orders-api"`)},
	}
	rows := briefingHTTPGroupRows(redFamilies[0], by, "service_name", []string{"orders-api", "payments-core"}, from, 10800)
	got := map[string]briefing.ClientRow{}
	for _, r := range rows {
		got[r.Service+"/"+r.Metric] = r
	}
	if len(rows) != 3 {
		t.Fatalf("satırlar (küme dışı servis ve p99 alınmaz): %+v", rows)
	}
	e := got["orders-api/"+briefing.ClientErrPct]
	if e.Family != briefing.FamilyHTTP || e.Now != 3 || e.Prev != 0.5 || !e.HasNow || e.VolNow != 2*10800 || e.VolPrev != 10800 {
		t.Errorf("orders-api hata satırı %+v", e)
	}
	if p := got["payments-core/"+briefing.ClientErrPct]; p.HasNow || p.HasPrev {
		t.Errorf("hacmi okunmamış servisin oranı kıyaslanmamalı: %+v", p)
	}
	if l := got["orders-api/"+briefing.ClientLatency]; l.Now != 0.4 || l.Prev != 0.1 {
		t.Errorf("gecikme satırı %+v", l)
	}
	g := briefingHTTPGroupRows(redFamilies[1], by, "service_name", []string{"orders-api"}, from, 10800)
	if len(g) == 0 || g[0].Family != briefing.FamilyGRPC {
		t.Errorf("gRPC ailesi: %+v", g)
	}
}
