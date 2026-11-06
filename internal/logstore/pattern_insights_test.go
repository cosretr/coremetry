package logstore

// pattern_insights_test.go — v0.10.1896 (Log Search Faz 4 dilim 4.2): desen
// eğilimi + varlık yoklaması. ES yükü sözleşmesi: TEK _msearch, ≤20 alt arama,
// her alt arama size:0 + zaman aralığı + timeout 2s; yoklama terminate_after:1.
// Kısmi yanıtta (timed_out / shard hatası) sıfır "yok" sayılmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPatternTrendBucketSec(t *testing.T) {
	cases := []struct {
		w    time.Duration
		want int
	}{
		{15 * time.Minute, 60},
		{30 * time.Minute, 60},
		{31 * time.Minute, 300},
		{time.Hour, 300},
		{6 * time.Hour, 900},
		{24 * time.Hour, 3600},
		{7 * 24 * time.Hour, 21600},
		{30 * 24 * time.Hour, 86400},
		{365 * 24 * time.Hour, 86400},
	}
	for _, c := range cases {
		if got := PatternTrendBucketSec(c.w); got != c.want {
			t.Errorf("%v → %d, want %d", c.w, got, c.want)
		}
	}
}

func TestTrendPoints(t *testing.T) {
	from := time.Date(2026, 11, 6, 10, 0, 30, 0, time.UTC)
	to := from.Add(5 * time.Minute)
	k := func(m int) int64 { return time.Date(2026, 11, 6, 10, m, 0, 0, time.UTC).UnixMilli() }
	got := TrendPoints(map[int64]int64{k(0): 3, k(2): 7, k(9): 99}, from, to, 60)
	// kovalar 10:00, 10:01 … 10:05 (from hizalı aşağı, to hariç); pencere dışı 10:09 yok
	if want := []int64{3, 0, 7, 0, 0, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if TrendPoints(nil, to, from, 60) != nil || TrendPoints(nil, from, to, 0) != nil {
		t.Error("bozuk pencere / kova → nil")
	}
}

func TestCombinePatternSearch(t *testing.T) {
	for _, c := range [][3]string{
		{"", `"disk full"`, `"disk full"`},
		{"level:error", `"disk full"`, `(level:error) AND ("disk full")`},
		{"level:error", "  ", ""},
	} {
		if got := CombinePatternSearch(c[0], c[1]); got != c[2] {
			t.Errorf("(%q,%q) → %q want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestPatternInsightBodies(t *testing.T) {
	q := map[string]any{"match_all": map[string]any{}}
	from := time.UnixMilli(1_000_000)
	tb := patternTrendBody(q, "@timestamp", from, from.Add(time.Hour), 300)
	if tb["size"] != 0 || tb["timeout"] != "2s" || tb["track_total_hits"] != false {
		t.Errorf("trend gövdesi: %v", tb)
	}
	dh := tb["aggs"].(map[string]any)["t"].(map[string]any)["date_histogram"].(map[string]any)
	if dh["fixed_interval"] != "300s" || dh["field"] != "@timestamp" {
		t.Errorf("date_histogram: %v", dh)
	}
	eb := patternExistBody(q)
	if eb["size"] != 0 || eb["terminate_after"] != 1 || eb["track_total_hits"] != 1 || eb["timeout"] != "2s" {
		t.Errorf("yoklama gövdesi: %v", eb)
	}
	if PatternInsightTimeout != "2s" || PatternInsightsMax != 20 {
		t.Error("sınırlar değişti: timeout ≤ 2s, ≤ 20 alt arama")
	}
}

func TestExistStateOf(t *testing.T) {
	dec := func(s string) esInsightExistResp {
		var r esInsightExistResp
		if err := json.Unmarshal([]byte(s), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		body string
		want ExistState
	}{
		{`{"timed_out":false,"_shards":{"total":3,"failed":0},"hits":{"total":{"value":1,"relation":"eq"}}}`, ExistFound},
		{`{"timed_out":false,"terminated_early":true,"_shards":{"total":3,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`, ExistFound},
		{`{"timed_out":false,"_shards":{"total":3,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`, ExistAbsent},
		{`{"timed_out":true,"_shards":{"total":3,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`, ExistUnknown},
		{`{"timed_out":false,"_shards":{"total":3,"failed":1},"hits":{"total":{"value":0,"relation":"eq"}}}`, ExistUnknown},
		{`{"timed_out":true,"_shards":{"total":3,"failed":1},"hits":{"total":{"value":1,"relation":"gte"}}}`, ExistFound},
		{`{"error":{"type":"x"},"status":400}`, ExistUnknown},
		{`{"timed_out":false,"_shards":{"total":3,"failed":0},"hits":{}}`, ExistUnknown},
	}
	for i, c := range cases {
		if got := existStateOf(dec(c.body)); got != c.want {
			t.Errorf("#%d %s → %s want %s", i, c.body, got, c.want)
		}
	}
}

// insightES — sahte ES: her _msearch'i sayar, alt arama sayısını kaydeder,
// yanıtı respond üretir.
func insightES(t *testing.T, respond func(n int) string) (*ESStore, *int, *[]string) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	var sent []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/":
			_, _ = w.Write([]byte(`{"name":"n1","cluster_name":"c","version":{"number":"8.13.0"},"tagline":"You Know, for Search"}`))
		case strings.HasSuffix(r.URL.Path, "/_msearch"):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			calls++
			sent = append(sent, string(b))
			mu.Unlock()
			n := strings.Count(strings.TrimSpace(string(b)), "\n")/2 + 1
			_, _ = w.Write([]byte(respond(n)))
		default:
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"type":"not_found","reason":"stub"},"status":404}`))
		}
	}))
	t.Cleanup(srv.Close)
	es, err := NewES(ESConfig{Addresses: []string{srv.URL}, Fields: ESFieldMap{Body: "message"}})
	if err != nil {
		t.Fatal(err)
	}
	return es, &calls, &sent
}

func TestESPatternTrendsSingleMsearchCapped(t *testing.T) {
	from := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	to := from.Add(10 * time.Minute)
	es, calls, sent := insightES(t, func(n int) string {
		rs := make([]string, n)
		for i := range rs {
			rs[i] = fmt.Sprintf(`{"timed_out":false,"_shards":{"total":1,"failed":0},"aggregations":{"t":{"buckets":[{"key":%d,"doc_count":%d}]}}}`, from.UnixMilli(), i+1)
		}
		return `{"responses":[` + strings.Join(rs, ",") + `]}`
	})
	qs := make([]string, 25)
	for i := range qs {
		qs[i] = fmt.Sprintf(`"part %d"`, i)
	}
	qs[3] = "" // boş desen: yuva boş, alt arama yok
	got, err := es.PatternTrends(context.Background(), Filter{Service: "orders-api", From: from, To: to}, qs, 60)
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("tek _msearch bekleniyordu: %d", *calls)
	}
	if len(got) != PatternInsightsMax {
		t.Fatalf("≤20 yuva: %d", len(got))
	}
	if subs := strings.Count((*sent)[0], `"fixed_interval"`); subs != PatternInsightsMax-1 {
		t.Errorf("alt arama sayısı %d, want 19 (20 − boş)", subs)
	}
	if got[3].Points != nil || got[0].Points[0] != 1 || len(got[0].Points) != 10 || got[4].Points[0] != 4 {
		t.Errorf("yuva hizası: %+v", got[:5])
	}
	for _, want := range []string{`"timeout":"2s"`, `"size":0`, `"track_total_hits":false`, `"gte":"2026-11-06T10:00:00Z"`} {
		if !strings.Contains((*sent)[0], want) {
			t.Errorf("gövdede %s yok", want)
		}
	}
}

func TestESPatternsExistSingleMsearch(t *testing.T) {
	es, calls, sent := insightES(t, func(n int) string {
		return `{"responses":[
			{"timed_out":false,"_shards":{"total":1,"failed":0},"hits":{"total":{"value":1,"relation":"eq"}}},
			{"timed_out":false,"_shards":{"total":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}},
			{"timed_out":true,"_shards":{"total":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}
		]}`
	})
	to := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	from := to.Add(-7 * 24 * time.Hour)
	got, err := es.PatternsExist(context.Background(), Filter{Service: "orders-api"}, []PatternProbe{
		{Query: `"a"`, From: from, To: to}, {Query: `"b"`, From: from, To: to}, {Query: `"c"`, From: from, To: to},
		{Query: `"d"`}, // pencere yok → istek yok, unknown
	})
	if err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("tek _msearch: %d", *calls)
	}
	if want := []ExistState{ExistFound, ExistAbsent, ExistUnknown, ExistUnknown}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for _, want := range []string{`"terminate_after":1`, `"timeout":"2s"`, `"size":0`} {
		if c := strings.Count((*sent)[0], want); c != 3 {
			t.Errorf("%s alt arama başına bir kez olmalı: %d", want, c)
		}
	}
}
