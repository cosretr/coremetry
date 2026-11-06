package api

// logs_patterns_compare_test.go — v0.10.1897 (Log Search Faz 4 dilim 4.4).
// Yeni / artan / kaybolan sınıflaması saf; "yeni" ve "kaybolan" öbür aralıkta
// gerçek sayımla (tek _msearch) doğrulanır; ES işi: iki tek-sayfa örnekleme +
// bir _msearch.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func grp(hash string, n int64, q string) logstore.SignatureGroup {
	return logstore.SignatureGroup{Hash: hash, Template: hash, Count: n, Query: q}
}

func TestComparePatternGroups(t *testing.T) {
	a := &logstore.PatternsResult{Sampled: 100, Groups: []logstore.SignatureGroup{
		grp("new1", 30, `"n1"`), grp("rise", 40, `"r"`), grp("flat", 20, `"f"`), grp("new2", 5, `"n2"`), grp("tiny", 2, `"t"`),
	}}
	b := &logstore.PatternsResult{Sampled: 200, Groups: []logstore.SignatureGroup{
		grp("rise", 20, `"r"`), grp("flat", 40, `"f"`), grp("gone1", 50, `"g1"`), grp("tiny", 1, `"t"`), grp("gone2", 9, ``),
	}}
	nw, up, gone := comparePatternGroups(a, b, 20)
	if len(nw) != 2 || nw[0].Hash != "new1" || nw[1].Hash != "new2" || nw[0].ShareA != 0.3 {
		t.Errorf("yeni: %+v", nw)
	}
	// rise: (40/100)/(20/200) = 4 ≥ 1.8; flat: (20/100)/(40/200) = 1 → değil; tiny A'da < 3 → değil
	if len(up) != 1 || up[0].Hash != "rise" || up[0].Ratio != 4 {
		t.Errorf("artan: %+v", up)
	}
	if len(gone) != 2 || gone[0].Hash != "gone1" || gone[0].CountB != 50 {
		t.Errorf("kaybolan: %+v", gone)
	}
	nw, up, gone = comparePatternGroups(a, b, 1)
	if len(nw) != 1 || len(up) != 1 || len(gone) != 1 {
		t.Error("liste tavanı")
	}
	nw, up, gone = comparePatternGroups(&logstore.PatternsResult{}, &logstore.PatternsResult{}, 5)
	if nw == nil || up == nil || gone == nil {
		t.Error("boş liste nil değil [] olmalı (JSON)")
	}
}

func TestCompareVerifyProbes(t *testing.T) {
	fa := logstore.Filter{From: time.Unix(100, 0), To: time.Unix(200, 0)}
	fb := logstore.Filter{From: time.Unix(0, 0), To: time.Unix(100, 0)}
	nw := []patternsCompareRow{{Query: `"a"`}, {Query: ``}, {Query: `"b"`}, {Query: `"c"`}}
	gone := []patternsCompareRow{{Query: `"g"`}}
	probes, idx := compareVerifyProbes(nw, gone, fa, fb, 2)
	if len(probes) != 3 {
		t.Fatalf("2 yeni (boş sorgu atlanır) + 1 kaybolan: %d", len(probes))
	}
	if !probes[0].From.Equal(fb.From) || !probes[2].From.Equal(fa.From) {
		t.Error("yeni B'de, kaybolan A'da aranır")
	}
	if idx[1] != [2]int{0, 2} || idx[2] != [2]int{1, 0} {
		t.Errorf("idx %v", idx)
	}
}

func TestPatternsCompareWindowOK(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		from, to time.Time
		ok       bool
	}{
		{now.Add(-time.Hour), now, true},
		{time.Time{}, now, false},
		{now, now.Add(-time.Hour), false},
		{now.Add(-32 * 24 * time.Hour), now, false},
		{now.Add(-31 * 24 * time.Hour), now, true},
	} {
		if (patternsCompareWindowOK(c.from, c.to) == nil) != c.ok {
			t.Errorf("%v–%v ok=%v beklendi", c.from, c.to, c.ok)
		}
	}
}

func TestGetLogsPatternsCompare_VerifiesWithOneMsearch(t *testing.T) {
	st, _ := insightFixture() // cur: 25 desen; base (cutoff öncesi): yalnız "stepaa"
	to := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	from := to.Add(-time.Hour)
	q := fmt.Sprintf("service=orders-api&from=%d&to=%d&bfrom=%d&bto=%d",
		from.UnixNano(), to.UnixNano(), from.Add(-time.Hour).UnixNano(), from.UnixNano())
	s := patternsServer(st)
	w := httptest.NewRecorder()
	s.getLogsPatternsCompare(w, httptest.NewRequest("GET", "/api/logs/patterns/compare?"+q, nil))
	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var res patternsCompareResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if st.existCalls != 1 || len(st.existProbes) > logstore.PatternInsightsMax || len(st.existProbes) != patternsCompareVerifyMax {
		t.Fatalf("doğrulama: %d çağrı, %d yoklama", st.existCalls, len(st.existProbes))
	}
	if st.trendCalls != 0 {
		t.Error("karşılaştırma eğilim istemez")
	}
	if len(res.New) != patternsCompareRows || res.Verify != "ok" || res.New[0].Verified != "yes" || res.New[patternsCompareVerifyMax].Verified != "" {
		t.Errorf("yeni: %d satır, verify=%s, ilk=%q, 11.=%q", len(res.New), res.Verify, res.New[0].Verified, res.New[patternsCompareVerifyMax].Verified)
	}
	if res.A.Sampled == 0 || res.B.Sampled != 1 {
		t.Errorf("pencere zarfı: %+v %+v", res.A, res.B)
	}
}

func TestGetLogsPatternsCompare_BadWindow400(t *testing.T) {
	s := patternsServer(&patternsStore{})
	w := httptest.NewRecorder()
	s.getLogsPatternsCompare(w, httptest.NewRequest("GET", "/api/logs/patterns/compare?bfrom=10&bto=5", nil))
	if w.Code != 400 {
		t.Fatalf("ters aralık 400 olmalı: %d", w.Code)
	}
}

func TestGetLogsPatternsCompare_NoCandidatesNoVerify(t *testing.T) {
	s := patternsServer(&patternsStore{rows: []*logstore.LogRecord{{Timestamp: 1, ServiceName: "a", Body: "disk full on volume"}}})
	now := time.Now()
	w := httptest.NewRecorder()
	q := fmt.Sprintf("bfrom=%d&bto=%d", now.Add(-2*time.Hour).UnixNano(), now.Add(-time.Hour).UnixNano())
	s.getLogsPatternsCompare(w, httptest.NewRequest("GET", "/api/logs/patterns/compare?"+q, nil))
	var res patternsCompareResult
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	// iki aralık aynı satırları döndürür → yeni/kaybolan yok → doğrulanacak aday yok
	if w.Code != 200 || res.Verify != "none" {
		t.Fatalf("status %d verify %q", w.Code, res.Verify)
	}
}
