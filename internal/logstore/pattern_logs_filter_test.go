package logstore

// pattern_logs_filter_test.go — v0.10.1071 (operatör, prod ES: anomali
// detayının "Logları aç" pivotu "Desen sayısı" grafiğinin saydığından başka
// satırlar gösterdi). Pivot artık deseni arama diline ÇEVİRMEZ: /logs
// `pattern=<ad>` alır, Filter.Pattern olur ve arka uç dedektörün yan
// tümcesini uygular. Çivilenen sözleşme: grafik (PatternHistogram), dedektör
// (CountPatterns) ve /logs okumaları (Search / Histogram / FieldStats) AYNI
// yüklemi taşır — ES'te bayt bayt aynı query_string, CH'de aynı SQL metni ve
// aynı regex bağı.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Gerçek küratörlü desenlerin şekli (logstore anomaly'yi import edemez —
// döngü; uçtan uca ad → spec api/logs_pattern_param_test.go'da).
var parityPatterns = []PatternSpec{
	{Name: "External system rejected", Regex: `ExternalSystemException|Request not allowed for URI|Service Unavailable`,
		Tokens: []string{"externalsystemexception", "not allowed for uri", "service unavailable"}},
	{Name: "Disk full", Regex: `no space left on device|disk full|ENOSPC`, Tokens: []string{"no space left", "disk full", "enospc"}},
	{Name: "Go panic", Regex: `^panic:|runtime error:`, Tokens: []string{"panic:", "runtime error"}},
	{Name: "quote", Regex: `say "hi"`, Tokens: []string{`say "hi"`}},
}

var (
	parityT0 = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	parityT1 = parityT0.Add(time.Hour)
)

func parityES() *ESStore {
	s := &ESStore{}
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	return s
}

// chartClause — anomali grafiğinin (PatternHistogram) gövdesindeki desen yan
// tümcesi: bool.filter[1] (filter[0] pencere).
func chartClause(t *testing.T, s *ESStore, p PatternSpec) any {
	t.Helper()
	body := patternHistogramBody(patternMatchClause(p, s.fields.Body), s.fields.Body, s.fields.Timestamp,
		"2026-10-03T12:00:00Z", "2026-10-03T13:00:00Z", 60, "10s", nil)
	return body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[1]
}

func filterClauses(t *testing.T, q map[string]any) []any {
	t.Helper()
	b, ok := q["bool"].(map[string]any)
	if !ok {
		t.Fatalf("bool yok: %v", q)
	}
	f, _ := b["filter"].([]any)
	return f
}

func containsClause(list []any, want any) bool {
	for _, c := range list {
		if reflect.DeepEqual(c, want) {
			return true
		}
	}
	return false
}

func TestPatternFilter_ES_SameClauseAsChartAndDetector(t *testing.T) {
	s := parityES()
	for _, p := range parityPatterns {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			chart := chartClause(t, s, p)
			// Dedektör (CountPatterns) aynı yan tümceyi taşır.
			count := patternCountBody(patternMatchClause(p, s.fields.Body), s.fields.Body, s.fields.Timestamp,
				"service.name.keyword", "a", "b", "c", "10s")
			countClause := count["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[1]
			if !reflect.DeepEqual(chart, countClause) {
				t.Fatalf("grafik ≠ dedektör:\n%v\n%v", chart, countClause)
			}
			// v0.10.1087 — 1080 örneklemi de aynı yan tümceyi taşır (bayt bayt).
			sample := patternSampleBody(patternMatchClause(p, s.fields.Body), s.fields.Body, s.fields.Timestamp,
				"a", "c", patternSampleSize, "5s")
			sampleClause := sample["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any)[1]
			cb, _ := json.Marshal(countClause)
			sb, _ := json.Marshal(sampleClause)
			if string(cb) != string(sb) {
				t.Fatalf("örneklem ≠ dedektör:\n%s\n%s", sb, cb)
			}
			// /logs listesi + histogram + fieldstats buildQuery'den okur.
			q := s.buildQuery(Filter{Pattern: &p})
			if !containsClause(filterClauses(t, q), chart) {
				t.Fatalf("/logs sorgusunda grafiğin yan tümcesi yok:\n%v\nwant %v", q, chart)
			}
			// Bayt düzeyinde: aynı JSON parçası histogram gövdesinde.
			cj, _ := json.Marshal(chart)
			hist, _ := json.Marshal(buildSeverityHistogramBody(q, s.fields.Timestamp,
				severityCandidateKeywordFields(s.fields.SeverityTx), s.fields.SeverityNo, 60, "10s"))
			if !strings.Contains(string(hist), string(cj)) {
				t.Fatalf("Logs histogram gövdesi grafiğin yan tümcesini taşımıyor:\n%s\n%s", hist, cj)
			}
		})
	}
}

// Serbest metin desenle AND'lenir: metin must'ta (expandShorthand'li, AND),
// desen filter'da, ikisi de durur — biri ötekini ezmez.
func TestPatternFilter_ES_ANDsWithFreeText(t *testing.T) {
	s := parityES()
	p := parityPatterns[0]
	q := s.buildQuery(Filter{Pattern: &p, Search: "orders-svc"})
	if !containsClause(filterClauses(t, q), chartClause(t, s, p)) {
		t.Fatalf("desen yan tümcesi yok: %v", q)
	}
	must, _ := q["bool"].(map[string]any)["must"].([]any)
	if len(must) != 1 || must[0].(map[string]any)["query_string"].(map[string]any)["query"] != "orders-svc" {
		t.Fatalf("serbest metin must'ta değil: %v", q)
	}
}

func TestPatternFilter_ES_NilAndTokenless(t *testing.T) {
	s := parityES()
	raw, _ := json.Marshal(s.buildQuery(Filter{Search: "x"}))
	if strings.Contains(string(raw), `"minimum_should_match"`) || strings.Contains(string(raw), "match_none") {
		t.Fatalf("desensiz sorguya desen yan tümcesi sızdı: %s", raw)
	}
	q := s.buildQuery(Filter{Pattern: &PatternSpec{Name: "x", Regex: "x"}})
	if !containsClause(filterClauses(t, q), map[string]any{"match_none": map[string]any{}}) {
		t.Fatalf("token'sız desen match_none olmalı (CountPatterns gibi): %v", q)
	}
}

func TestPatternFilter_CH_SamePredicateAsChartAndDetector(t *testing.T) {
	for _, p := range parityPatterns {
		p := p
		t.Run(p.Name, func(t *testing.T) {
			lit := chBuildTokenLiteral(p.Tokens)
			pred := chPatternMatchSQL(lit) // dedektör (countOnePattern) + grafik
			if !strings.Contains(chPatternHistogramSQL(lit, 60), pred) {
				t.Fatalf("grafik SQL'i yüklemi taşımıyor")
			}
			conj, args := chPatternConjunct(&p)
			if conj != "("+pred+")" || !reflect.DeepEqual(args, []any{p.Regex}) {
				t.Fatalf("conjunct=%q args=%v", conj, args)
			}
			// /logs histogramı
			wc, hargs := chLogsHistogramWhere(Filter{Pattern: &p, Search: "orders-svc"}, parityT0, parityT1)
			if !strings.Contains(wc, " AND ("+pred+")") {
				t.Fatalf("histogram WHERE yüklemi taşımıyor: %s", wc)
			}
			if !containsClause(hargs, any(p.Regex)) {
				t.Fatalf("histogram bağlarında regex yok: %v", hargs)
			}
			// `?` sayısı = bağ sayısı (regex bağı yerinde).
			if strings.Count(wc, "?") != len(hargs) {
				t.Fatalf("? sayısı %d, bağ %d: %s", strings.Count(wc, "?"), len(hargs), wc)
			}
			// /logs listesi (chstore.GetLogs) hazır yüklemi alır.
			lf := chSearchLogFilter(Filter{Pattern: &p})
			if lf.PatternSQL != conj || !reflect.DeepEqual(lf.PatternArgs, args) {
				t.Fatalf("liste filtresi: %q %v", lf.PatternSQL, lf.PatternArgs)
			}
		})
	}
	// Desensiz: hiçbir iz yok.
	if wc, _ := chLogsHistogramWhere(Filter{}, parityT0, parityT1); strings.Contains(wc, "match(") {
		t.Fatalf("desensiz WHERE'e yüklem sızdı: %s", wc)
	}
	if lf := chSearchLogFilter(Filter{}); lf.PatternSQL != "" || lf.PatternArgs != nil {
		t.Fatalf("desensiz liste filtresi: %+v", lf)
	}
}

func TestPatternKey(t *testing.T) {
	if PatternKey(nil) != "" {
		t.Fatal("nil → boş")
	}
	if PatternKey(&PatternSpec{Name: "Disk full", Regex: "x"}) != "Disk full" {
		t.Fatal("ad anahtardır")
	}
	a := PatternKey(&PatternSpec{Regex: "a", Tokens: []string{"x"}})
	b := PatternKey(&PatternSpec{Regex: "a", Tokens: []string{"y"}})
	if a == "" || a == b {
		t.Fatalf("adsız spec yüklemiyle anahtarlanmalı: %q %q", a, b)
	}
}
