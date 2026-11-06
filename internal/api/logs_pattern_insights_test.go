package api

// logs_pattern_insights_test.go — v0.10.1896 (Log Search Faz 4 dilim 4.2).
// ES yükü sözleşmesi (1877 olayı): `logs_search.patternInsights` KAPALIYKEN
// eğilim ve ilk-görülme ES istekleri HİÇ gitmez (insights=1 gelse bile);
// açıkken her biri tek çağrı, ≤20 desen; servis seçili değilse yoklama yok.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

type insightStore struct {
	logstore.Store
	cur, base   []*logstore.LogRecord
	cutoffNs    int64 // f.To bundan küçükse taban penceresi
	trendCalls  int
	trendQs     []string
	existCalls  int
	existProbes []logstore.PatternProbe
}

func (p *insightStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	if f.To.UnixNano() <= p.cutoffNs {
		return &logstore.Page{Total: len(p.base), Logs: p.base}, nil
	}
	return &logstore.Page{Total: len(p.cur), Logs: p.cur}, nil
}
func (p *insightStore) Backend() string { return "elasticsearch" }
func (p *insightStore) PatternTrends(_ context.Context, _ logstore.Filter, qs []string, _ int) ([]logstore.PatternTrend, error) {
	p.trendCalls++
	p.trendQs = qs
	out := make([]logstore.PatternTrend, len(qs))
	for i := range out {
		out[i] = logstore.PatternTrend{Points: []int64{1, 2, 3}}
	}
	return out, nil
}
func (p *insightStore) PatternsExist(_ context.Context, _ logstore.Filter, pr []logstore.PatternProbe) ([]logstore.ExistState, error) {
	p.existCalls++
	p.existProbes = pr
	out := make([]logstore.ExistState, len(pr))
	for i := range out {
		out[i] = logstore.ExistAbsent
	}
	return out, nil
}

func withPatternInsights(t *testing.T, on bool) {
	t.Helper()
	prev := currentLogsSearchSettings()
	c := prev
	c.PatternInsights = on
	setLogsSearchSettings(c)
	t.Cleanup(func() { setLogsSearchSettings(prev) })
}

func insightFixture() (*insightStore, string) {
	to := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	var cur []*logstore.LogRecord
	for i := 0; i < 25; i++ { // 25 ayrı desen (her biri 2 satır)
		for k := 0; k < 2; k++ {
			cur = append(cur, &logstore.LogRecord{Timestamp: to.UnixNano() - int64(i*1000+k), ServiceName: "orders-api",
				Body: fmt.Sprintf("payment step%c%c failed for order", 'a'+i%26, 'a'+i/26)})
		}
	}
	base := []*logstore.LogRecord{{Timestamp: from.UnixNano() - 1, ServiceName: "orders-api", Body: "payment stepaa failed for order"}}
	st := &insightStore{cur: cur, base: base, cutoffNs: from.UnixNano()}
	q := fmt.Sprintf("from=%d&to=%d", from.UnixNano(), to.UnixNano())
	return st, q
}

func getPatterns(t *testing.T, st logstore.Store, qs string) logstore.PatternsResult {
	t.Helper()
	s := patternsServer(st)
	w := httptest.NewRecorder()
	s.getLogsPatterns(w, httptest.NewRequest("GET", "/api/logs/patterns?"+qs, nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var res logstore.PatternsResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPatternInsights_OffSendsNothing(t *testing.T) {
	withPatternInsights(t, false)
	st, q := insightFixture()
	res := getPatterns(t, st, q+"&service=orders-api&baseline=1&insights=1")
	if st.trendCalls != 0 || st.existCalls != 0 {
		t.Fatalf("ayar kapalıyken ES isteği gitti: trend=%d exist=%d", st.trendCalls, st.existCalls)
	}
	if res.Insights != nil {
		t.Errorf("kapalıyken zarf olmamalı: %+v", res.Insights)
	}
}

func TestPatternInsights_OnSingleCallsCapped(t *testing.T) {
	withPatternInsights(t, true)
	st, q := insightFixture()
	res := getPatterns(t, st, q+"&service=orders-api&baseline=1&insights=1")
	if st.trendCalls != 1 || len(st.trendQs) != logstore.PatternInsightsMax {
		t.Fatalf("eğilim: %d çağrı, %d desen (want 1, 20)", st.trendCalls, len(st.trendQs))
	}
	if st.existCalls != 1 || len(st.existProbes) == 0 || len(st.existProbes) > logstore.PatternInsightsMax {
		t.Fatalf("yoklama: %d çağrı, %d desen", st.existCalls, len(st.existProbes))
	}
	pr := st.existProbes[0]
	if pr.To.Sub(pr.From) != 7*24*time.Hour || pr.To.UnixNano() != res.Insights.FromNs {
		t.Errorf("yoklama penceresi [from−7g, from) olmalı: %v–%v", pr.From, pr.To)
	}
	if res.Insights == nil || res.Insights.Trend != "ok" || res.Insights.FirstSeen != "ok" || res.Insights.LookbackDays != 7 {
		t.Fatalf("zarf: %+v", res.Insights)
	}
	var first int
	for _, g := range res.Groups {
		if g.FirstSeen7d == "first" {
			first++
			if !g.New {
				t.Errorf("yalnız önceki pencerede olmayan desen yoklanır: %+v", g)
			}
		}
	}
	if first == 0 {
		t.Error("ilk kez görülen desen bekleniyordu")
	}
}

func TestPatternInsights_NoServiceNoProbe(t *testing.T) {
	withPatternInsights(t, true)
	st, q := insightFixture()
	res := getPatterns(t, st, q+"&baseline=1&insights=1")
	if st.existCalls != 0 {
		t.Fatalf("servissiz yoklama gitti")
	}
	if res.Insights.FirstSeen != "no-service" {
		t.Errorf("FirstSeen = %q", res.Insights.FirstSeen)
	}
	for _, g := range res.Groups {
		if g.FirstSeen7d != "" {
			t.Errorf("servissizken etiket olmamalı: %+v", g)
		}
	}
}

func TestPatternInsights_NoBaselineNoProbe(t *testing.T) {
	withPatternInsights(t, true)
	st, q := insightFixture()
	res := getPatterns(t, st, q+"&service=orders-api&insights=1")
	if st.existCalls != 0 || res.Insights.FirstSeen != "no-baseline" {
		t.Fatalf("taban yokken yoklama: calls=%d state=%q", st.existCalls, res.Insights.FirstSeen)
	}
}

func TestPatternInsights_UnsupportedBackend(t *testing.T) {
	withPatternInsights(t, true)
	s := patternsServer(&patternsStore{rows: []*logstore.LogRecord{{Timestamp: 1, ServiceName: "a", Body: "disk full on volume"}}})
	w := httptest.NewRecorder()
	s.getLogsPatterns(w, httptest.NewRequest("GET", "/api/logs/patterns?insights=1", nil))
	var res logstore.PatternsResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.Insights == nil || res.Insights.Trend != "unsupported" {
		t.Fatalf("CH backend 'unsupported' demeli: %+v", res.Insights)
	}
}

func TestPatternInsightsKey(t *testing.T) {
	if patternInsightsKeySuffix(false) != "" || patternInsightsKeySuffix(true) == "" {
		t.Error("etkin bayrak anahtara girmeli, kapalı anahtar eskisiyle aynı kalmalı")
	}
	for _, c := range [][3]bool{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		if patternInsightsOn(c[0], c[1]) != c[2] {
			t.Errorf("on(%v,%v) != %v", c[0], c[1], c[2])
		}
	}
	if patternFirstSeenScope(logstore.Filter{Service: "orders-api"}) != true ||
		patternFirstSeenScope(logstore.Filter{Service: "a,b"}) ||
		patternFirstSeenScope(logstore.Filter{}) ||
		patternFirstSeenScope(logstore.Filter{Service: "a", TeamValues: []string{"x"}}) {
		t.Error("yoklama yalnız tek serviste")
	}
}
