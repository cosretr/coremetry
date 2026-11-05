package logstore

// es_kql_test.go — v0.10.1819 (Log Search Faz 1 dilim 1.3): ES `query_string`
// metni logql.ToES'ten geçer (KQL yazımları kanonik Lucene; `lang=lucene`
// ve Lucene'e özgü sözdizimi AYNEN). Stub ES'in aldığı gövde ölçülür.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func esQueryString(t *testing.T, raw []byte) string {
	t.Helper()
	var body struct {
		Query struct {
			Bool struct {
				Must []struct {
					QueryString struct {
						Query string `json:"query"`
					} `json:"query_string"`
				} `json:"must"`
			} `json:"bool"`
		} `json:"query"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Query.Bool.Must) == 0 {
		t.Fatalf("gövde ayrışmadı: %v %s", err, raw)
	}
	return body.Query.Bool.Must[0].QueryString.Query
}

func TestESQueryStringTranslation(t *testing.T) {
	es, st, _ := newStubES(t, ESFieldMap{})
	now := time.Now()
	for _, tc := range []struct{ search, lang, want string }{
		{`orders and not timeout`, "", `orders AND NOT timeout`},
		{`latency_ms >= 500 or x`, "", `latency_ms:>=500 OR x`},
		{`orders and not timeout`, "lucene", `orders and not timeout`},
		{`foo~2 and bar`, "", `foo~2 and bar`},
	} {
		if _, err := es.Search(context.Background(), Filter{Service: "orders-api", Search: tc.search, SearchLang: tc.lang, From: now.Add(-time.Hour), To: now}); err != nil {
			t.Fatal(err)
		}
		st.mu.Lock()
		raw := st.lastSearchBody
		st.mu.Unlock()
		if got := esQueryString(t, raw); got != tc.want {
			t.Errorf("Search(%q, lang=%q) query_string = %q; want %q", tc.search, tc.lang, got, tc.want)
		}
	}
}
