package api

// v0.10.1719 — Login Briefing kalem 2: HTTP/gRPC sunucu ailesi takımın TÜM
// servisleri için TEK sorgu kümesi (1712: yalnız en yoğun servis).
// v0.10.1727 — karma aileli takım: servis başına tür KATALOGDAN
// (ServerEntryKinds), tür başına bir küme × 2 sorgu. Belirti: HTTP + yalnız
// gRPC servisli takımda gRPC servisleri hiç satır üretmiyordu. Pinler: sorgu
// sayısı (R9 ≤ 6 VM / takım × env; tek tür 4, iki tür 6), gruplama, servis
// regex kaçışı (QuoteMeta + dize kaçışı), by (svc) ve topk YOK, seri → satır.

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
		name     string
		kind     string
		countBy  string
		mustLat  []string
		mustName []string
	}{
		{"HTTP", redKindHTTP, "by (service_name, " + strings.Join(mcStatusLabels, ", ") + ")",
			[]string{"histogram_quantile(0.95, sum by (le, service_name)"}, []string{"http_server_request_duration_seconds"}},
		{"gRPC legacy (ms) or stable", redKindGRPC, "by (service_name, rpc_grpc_status_code, rpc_response_status_code)",
			[]string{") / 1000 or (", "rpc_server_call_duration_seconds_bucket"}, []string{"rpc_server_duration_milliseconds_count", "rpc_server_call_duration_seconds_count"}},
	} {
		qs := briefingHTTPGroupQueries(tc.kind, svcs, "service_name", `deployment_environment="prod"`, 240)
		if len(qs) != 2 || qs[0].Kind != svcHTTPREDRate || qs[1].Kind != svcHTTPREDLatency {
			t.Fatalf("%s: tür başına 2 sorgu (sayı × durum, p95) bekleniyordu: %+v", tc.name, qs)
		}
		for _, q := range qs {
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
		}
		if !strings.Contains(qs[0].Query, tc.countBy) {
			t.Errorf("%s: sayı sorgusunda %q yok: %s", tc.name, tc.countBy, qs[0].Query)
		}
		for _, m := range tc.mustName {
			if !strings.Contains(qs[0].Query, m) {
				t.Errorf("%s: sayı sorgusunda ad %q yok: %s", tc.name, m, qs[0].Query)
			}
		}
		for _, m := range tc.mustLat {
			if !strings.Contains(qs[1].Query, m) {
				t.Errorf("%s: gecikme sorgusunda %q yok: %s", tc.name, m, qs[1].Query)
			}
		}
	}
	if briefingHTTPGroupQueries(redKindHTTP, nil, "service_name", "", 60) != nil {
		t.Error("boş küme sorgu üretti")
	}
	if briefingHTTPGroupQueries(redKindHTTP, svcs, "", "", 60) != nil {
		t.Error("servis etiketi yokken süzgeçsiz (filo) sorgu üretti")
	}
	// Kafka soru sayısı sabiti katalogla eş.
	if len(briefingKafkaQuestions()) != briefingKafkaVMQueries {
		t.Errorf("Kafka soru sayısı %d, sabit %d", len(briefingKafkaQuestions()), briefingKafkaVMQueries)
	}
}

func TestBriefingSplitKindsAndR9(t *testing.T) {
	cands := []string{"orders-api", "payments-core", "ledger-grpc", "edge-gw", "kafka-worker", "unknown-svc"}
	calls := map[string]chstore.ServerEntryCalls{
		"orders-api":    {HTTP: 900},
		"payments-core": {HTTP: 50},
		"ledger-grpc":   {Other: 400},
		"edge-gw":       {HTTP: 10, Other: 5},
		"kafka-worker":  {}, // yalnız tüketici/istemci
	}
	for _, tc := range []struct {
		name           string
		cands          []string
		known          bool
		wantHTTP, wGRP string
		wantVM         int
	}{
		{"iki tür", cands, true, "orders-api,payments-core,edge-gw", "ledger-grpc,edge-gw", 6},
		{"yalnız HTTP", []string{"orders-api", "payments-core", "kafka-worker"}, true, "orders-api,payments-core", "", 4},
		{"yalnız gRPC", []string{"ledger-grpc"}, true, "", "ledger-grpc", 4},
		{"katalog bilinmiyor → iki türde de", []string{"orders-api", "ledger-grpc"}, false, "orders-api,ledger-grpc", "orders-api,ledger-grpc", 6},
		{"sunucu servisi yok", []string{"kafka-worker"}, true, "", "", 2},
	} {
		h, g := briefingSplitKinds(tc.cands, calls, tc.known)
		if strings.Join(h, ",") != tc.wantHTTP || strings.Join(g, ",") != tc.wGRP {
			t.Errorf("%s: http=%v grpc=%v", tc.name, h, g)
		}
		sets := briefingGroupsQueries(briefingKindGroups(h, g), "service_name", "", 240)
		n := briefingClientVMQueries(sets)
		if n != tc.wantVM || n > briefingR9VMMax {
			t.Errorf("%s: %d VM sorgusu, want %d (R9 ≤ %d)", tc.name, n, tc.wantVM, briefingR9VMMax)
		}
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
	ser := func(val, prev float64, kv ...string) chstore.SpanMetricSeries {
		return chstore.SpanMetricSeries{GroupKey: kv, Points: []chstore.SpanMetricPoint{{Time: (from - 1000) * 1e6, Value: prev}, {Time: from * 1e6, Value: val}}}
	}
	const st = `http_response_status_code`
	by := map[string][]chstore.SpanMetricSeries{
		svcHTTPREDRate: {
			// orders-api: prev 1 req/s (0.5 hata → 50%), now 2 req/s (1 hata → 50%)
			ser(1, 0.5, `service_name="orders-api"`, st+`="200"`),
			ser(1, 0.5, `service_name="orders-api"`, st+`="503"`),
			ser(3, 3, `service_name="orders-api2"`, st+`="404"`), // 4xx hata değil, küme dışı
			// payments-core: durum etiketi yok → oran satırı yok
			ser(4, 4, `service_name="payments-core"`),
			ser(9, 1, `service_name="outsider"`, st+`="500"`),
		},
		svcHTTPREDLatency: {ser(0.4, 0.1, `service_name="orders-api"`)},
	}
	rows := briefingHTTPGroupRows(redKindHTTP, by, "service_name", []string{"orders-api", "payments-core"}, from, 10800)
	got := map[string]briefing.ClientRow{}
	for _, r := range rows {
		got[r.Service+"/"+r.Metric] = r
	}
	if len(rows) != 2 {
		t.Fatalf("satırlar (küme dışı servis, durumsuz servisin oranı alınmaz): %+v", rows)
	}
	e := got["orders-api/"+briefing.ClientErrPct]
	if e.Family != briefing.FamilyHTTP || e.Now != 50 || e.Prev != 50 || !e.HasNow || !e.HasPrev || e.VolNow != 2*10800 || e.VolPrev != 10800 {
		t.Errorf("orders-api hata satırı %+v", e)
	}
	if l := got["orders-api/"+briefing.ClientLatency]; l.Now != 0.4 || l.Prev != 0.1 || l.Family != briefing.FamilyHTTP {
		t.Errorf("gecikme satırı %+v", l)
	}
	// gRPC: kod etiketi iki yazımdan biri; 14 (UNAVAILABLE) sunucu hatası, 5 (NOT_FOUND) değil.
	g := briefingHTTPGroupRows(redKindGRPC, map[string][]chstore.SpanMetricSeries{svcHTTPREDRate: {
		ser(3, 3, `service_name="ledger-grpc"`, `rpc_grpc_status_code="0"`),
		ser(1, 0, `service_name="ledger-grpc"`, `rpc_grpc_status_code="14"`),
		ser(1, 1, `service_name="ledger-grpc"`, `rpc_grpc_status_code="5"`),
	}}, "service_name", []string{"ledger-grpc"}, from, 10800)
	if len(g) != 1 || g[0].Family != briefing.FamilyGRPC || g[0].Now != 20 || g[0].Prev != 0 || !g[0].HasPrev {
		t.Errorf("gRPC satırı: %+v", g)
	}
}
