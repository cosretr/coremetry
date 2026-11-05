package api

// logs_query_lang_test.go — v0.10.1819 (Log Search Faz 1 dilim 1.3).
// /api/logs* kapısı: ES'te yalnız YAPISAL kırık 400 (Lucene'in de kesin
// reddettiği); Lucene'e özgü yazımlar geçer; `lang=lucene` kapıyı atlar.
// Yanıt `queryLang` taşır; `lang` önbellek anahtarına girer (v0.5.187).

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogsQuerySyntaxCheck(t *testing.T) {
	for _, tc := range []struct {
		name, backend, search, lang string
		reject                      bool
	}{
		{"ES kapanmamış parantez", "elasticsearch", `level:error AND (a OR b`, "", true},
		{"ES kapanmamış tırnak", "elasticsearch", `message:"disk`, "", true},
		{"ES sonda and", "elasticsearch", `level:error and`, "", true},
		{"ES fuzzy geçer (Lucene)", "elasticsearch", `foo~2`, "", false},
		{"ES alan içinde alan geçer (Lucene)", "elasticsearch", `a:(b:c)`, "", false},
		{"ES KQL yazımı geçer", "elasticsearch", `level: error and latency_ms >= 500`, "", false},
		{"ES lang=lucene kapıyı atlar", "elasticsearch", `(a OR`, "lucene", false},
		{"CH kırık", "clickhouse", `(a OR`, "", true},
		{"CH alan içinde alan (ayrışmaz)", "clickhouse", `a:(b:c)`, "", true},
		{"CH KQL yazımı geçer", "clickhouse", `service.name: orders-api`, "", false},
		{"boş", "elasticsearch", ``, "", false},
		{"bilinmeyen arka uç", "test", `(a OR`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := logsQuerySyntaxCheck(tc.backend, tc.search, tc.lang) != nil; got != tc.reject {
				t.Errorf("reject = %v; want %v", got, tc.reject)
			}
		})
	}
}

func TestLogsLangParamAndKey(t *testing.T) {
	for in, want := range map[string]string{"lucene": "lucene", "LUCENE": "lucene", " lucene ": "lucene", "kql": "", "": "", "sql": ""} {
		if got := logsLangParam(url.Values{"lang": {in}}); got != want {
			t.Errorf("logsLangParam(%q) = %q", in, got)
		}
	}
	f := logstore.Filter{Service: "orders-api", Search: "a and b"}
	auto := logsSearchKey(f, "1", "2")
	f.SearchLang = "lucene"
	forced := logsSearchKey(f, "1", "2")
	if auto == forced {
		t.Fatal("lang=lucene ayrı önbellek girdisi olmalı (aynı metin, farklı ES sorgusu)")
	}
	if logsTimeseriesKey(f, "1", "2", 30, "severity") == logsTimeseriesKey(logstore.Filter{Service: "orders-api", Search: "a and b"}, "1", "2", 30, "severity") {
		t.Fatal("histogram anahtarı lang'i taşımalı")
	}
}

func TestLogsQueryLang(t *testing.T) {
	for _, tc := range []struct {
		backend, search, lang, want string
	}{
		{"elasticsearch", `level:error and x`, "", "kql"},
		{"elasticsearch", `foo~2`, "", "lucene"},
		{"elasticsearch", `a and b`, "lucene", "lucene"},
		{"elasticsearch", ``, "", ""},
		{"clickhouse", `foo~2`, "", "kql"},
	} {
		if got := logsQueryLang(tc.backend, logstore.Filter{Search: tc.search, SearchLang: tc.lang}); got != tc.want {
			t.Errorf("logsQueryLang(%s, %q, %q) = %q; want %q", tc.backend, tc.search, tc.lang, got, tc.want)
		}
	}
}

// esCaptureStore — ES arka ucu gibi görünür, aldığı süzgeci saklar.
type esCaptureStore struct {
	logstore.Store
	got logstore.Filter
}

func (s *esCaptureStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.got = f
	return &logstore.Page{}, nil
}
func (*esCaptureStore) Backend() string { return "elasticsearch" }

func TestLogsHandlerLangEndToEnd(t *testing.T) {
	st := &esCaptureStore{}
	s := &Server{logs: st, cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}

	// Yapısal kırık ES'e GİTMEZ: 400 + konum + düz Türkçe.
	w := httptest.NewRecorder()
	s.getLogs(w, httptest.NewRequest("GET", "/api/logs?search="+url.QueryEscape("level:error AND (foo OR bar")+"&from=1&to=2", nil))
	if w.Code != 400 {
		t.Fatalf("status %d", w.Code)
	}
	var b logQueryErrorBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if b.Error != "( kapatılmamış. Sonuna ) ekleyin." || b.Pos == nil || *b.Pos != 16 {
		t.Fatalf("gövde: %s", w.Body.String())
	}

	// KQL → queryLang kql; lang=lucene → süzgeçte SearchLang + queryLang lucene.
	for _, tc := range []struct{ qs, wantLang, wantFilterLang string }{
		{"search=" + url.QueryEscape("level: error and x"), "kql", ""},
		{"search=" + url.QueryEscape("a and b") + "&lang=lucene", "lucene", "lucene"},
	} {
		w := httptest.NewRecorder()
		s.getLogs(w, httptest.NewRequest("GET", "/api/logs?"+tc.qs+"&from=1&to=3", nil))
		var out struct {
			QueryLang string `json:"queryLang"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 || out.QueryLang != tc.wantLang || st.got.SearchLang != tc.wantFilterLang {
			t.Errorf("%s: code=%d queryLang=%q filterLang=%q", tc.qs, w.Code, out.QueryLang, st.got.SearchLang)
		}
	}
}
