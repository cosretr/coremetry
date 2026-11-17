package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// v0.10.2320 — bilinmeyen araç → en yakın geçerli adlar (yalnız verilen katalog).

var testCatalog = []string{
	"get_trace", "get_logs_for_trace", "search_logs", "search_traces", "trace_stats",
	"compare_periods", "query_metric", "list_exception_groups", "get_exception_group",
}

func TestNearestToolNames(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"get_trac", []string{"get_trace"}},                                                // yazım hatası
		{"GetTrace", []string{"get_trace"}},                                                // camelCase
		{"get-logs-for-trace", []string{"get_logs_for_trace"}},                             // ayraç
		{"search_log", []string{"search_logs"}},                                            // tekil
		{"logs_search", []string{"search_logs"}},                                           // sözcük sırası
		{"trace_stat", []string{"trace_stats"}},                                            // çoğul
		{"get_exception_groups", []string{"get_exception_group", "list_exception_groups"}}, // mesafe sırası
		{"exception_group", []string{"get_exception_group", "list_exception_groups"}},      // sözcük eşleşmesi (mesafe eşiği aşıldı)
		{"query_metrics_range", []string{"query_metric"}},                                  // sözcük eşleşmesi: adayın sözcükleri sorguda
		{"get_weather", nil},                                                               // yakın yok → öneri yok
		{"x", nil},                                                                         // çok kısa, yakın yok
		{"", nil},
	}
	for _, c := range cases {
		if got := NearestToolNames(c.name, testCatalog); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// Sıra deterministik: katalog sırası sonucu değiştirmez; en çok üç.
func TestNearestToolNamesDeterministic(t *testing.T) {
	cat := []string{"trace_a", "trace_b", "trace_c", "trace_d", "trace_e"}
	want := []string{"trace_a", "trace_b", "trace_c"}
	for i := 0; i < 20; i++ {
		perm := append([]string(nil), cat...)
		for j := range perm {
			k := (j*7 + i) % len(perm)
			perm[j], perm[k] = perm[k], perm[j]
		}
		if got := NearestToolNames("trace_x", perm); !reflect.DeepEqual(got, want) {
			t.Fatalf("perm %v: %v, beklenen %v", perm, got, want)
		}
	}
}

// Katalogda olmayan ad asla önerilmez (daraltılmış katalog).
func TestNearestToolNamesOnlyFromCatalog(t *testing.T) {
	narrowed := []string{"get_trace", "get_logs_for_trace", "search_traces"}
	for _, q := range []string{"list_problem", "list_problems", "listProblems", "get_problem"} {
		for _, s := range NearestToolNames(q, narrowed) {
			if s != "get_trace" && s != "get_logs_for_trace" && s != "search_traces" {
				t.Fatalf("%q: katalog dışı öneri %q", q, s)
			}
			if strings.Contains(s, "problem") {
				t.Fatalf("%q: düşen araç önerildi: %q", q, s)
			}
		}
	}
}

func TestUnknownToolJSONShape(t *testing.T) {
	var te ToolError
	if err := json.Unmarshal([]byte(UnknownToolJSON("get_trac", []string{"get_trace"})), &te); err != nil {
		t.Fatal(err)
	}
	if te.Error != ToolErrNotFound || te.Retryable || te.Detail != `unknown tool "get_trac"` ||
		!reflect.DeepEqual(te.Suggest, []string{"get_trace"}) || !strings.Contains(te.Hint, "get_trace") {
		t.Fatalf("öneri biçimi: %+v", te)
	}
	raw := UnknownToolJSON("get_weather", nil)
	if strings.Contains(raw, "did_you_mean") || !strings.Contains(raw, `"error":"not_found"`) || !strings.Contains(raw, `unknown tool \"get_weather\"`) {
		t.Fatalf("önerisiz biçim: %s", raw)
	}
	// Diğer araç hataları alanı taşımaz (omitempty).
	if strings.Contains(ToolErrorJSON(nil), "did_you_mean") {
		t.Fatal("did_you_mean yalnız bilinmeyen araçta")
	}
}
