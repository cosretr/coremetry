package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestProblemsBreakdownSinceAndKey(t *testing.T) {
	for raw, want := range map[string]string{"": "7d", "24h": "24h", "7d": "7d", "30d": "30d", "1h": "7d", "365d": "7d", " 30d ": "30d"} {
		if got := problemsBreakdownSince(raw); got != want {
			t.Errorf("since(%q) = %q, want %q", raw, got, want)
		}
	}
	// Anahtar tek girdiyi (since basamağı) taşır: farklı basamak farklı anahtar.
	seen := map[string]string{}
	for s := range problemsBreakdownWindows {
		k := problemsBreakdownKey(s)
		if prev, dup := seen[k]; dup {
			t.Fatalf("anahtar çakışması %s / %s", prev, s)
		}
		seen[k] = s
	}
}

func TestBuildProblemsBreakdown(t *testing.T) {
	from := time.Unix(10*86400, 0).UTC()
	to := from.Add(3 * 24 * time.Hour)
	agg := chstore.ProblemSourceAgg{Rows: []chstore.ProblemSourceAggRow{
		{Family: "ml_svc", N: 60, OpenNow: 2, Critical: 50, Warning: 5, MedianSec: 420, Short15: 45, Long1h: 3, Services: 4, CaseN: 6, CaseCorrect: 4},
		{Family: "user_rule", N: 40, OpenNow: 1, Critical: 10, Warning: 30, MedianSec: 5400, Short15: 4, Long1h: 20, Services: 2},
		{Family: "ml_svc", Metric: "error_rate", N: 45, Short15: 40},
		{Family: "ml_svc", Metric: "p99_ms", N: 15, Short15: 5},
		{Family: "ml_svc", Service: "svc-a", N: 30, DayCounts: []uint64{5, 1}},
		{Family: "ml_svc", Service: "svc-b", N: 10, DayCounts: []uint64{3}},
		{Family: "ml_svc", Service: "svc-c", N: 9},
		{Family: "ml_svc", Service: "svc-d", N: 8},
		{Family: "ml_svc", Service: "svc-e", N: 2},
		{Family: "ml_svc", Service: "-", N: 1},
		{Family: "user_rule", Service: "svc-a", N: 40, DayCounts: []uint64{2, 2}},
		{Bucket: 10 * 86400, N: 70},
		{Bucket: 12 * 86400, N: 30},
	}}
	got := buildProblemsBreakdown(agg, "7d", from, to, 86400)
	if got.Total != 100 || got.OpenNow != 3 || got.Short15 != 49 || got.SeverityBasis != "severity" {
		t.Fatalf("toplamlar: %+v", got)
	}
	if len(got.Series) != 3 || got.Series[0].N != 70 || got.Series[1].N != 0 || got.Series[2].N != 30 {
		t.Fatalf("seri sıfır doldurulmalı: %+v", got.Series)
	}
	if len(got.Families) != 2 || got.Families[0].Family != "ml_svc" {
		t.Fatalf("aile sırası: %+v", got.Families)
	}
	f := got.Families[0]
	if f.SharePct != 60 || f.Short15Pct != 75 || f.OtherSev != 5 {
		t.Errorf("oranlar: %+v", f.problemsBreakdownStats)
	}
	if f.CaseLabelled != 6 || f.CaseCorrect != 4 || got.Families[1].CaseLabelled != 0 {
		t.Errorf("vaka etiketi (v0.10.2484): %+v", f.problemsBreakdownStats)
	}
	if f.ChurnPairs != 2 || f.ChurnCount != 8 {
		t.Errorf("churn: pairs=%d count=%d", f.ChurnPairs, f.ChurnCount)
	}
	if len(f.TopServices) != 5 || f.TopServices[0].Service != "svc-a" || f.TopServices[4].Service != "svc-e" {
		t.Errorf("ilk 5 servis ('-' hariç): %+v", f.TopServices)
	}
	if len(f.Metrics) != 2 || f.Metrics[0].Metric != "error_rate" || f.Metrics[0].SharePct != 75 {
		t.Errorf("metrik alt düzeyi (aile içi pay): %+v", f.Metrics)
	}
	if got.Families[1].ChurnPairs != 0 {
		t.Error("günde < 3 açılış churn sayılmaz")
	}
}

// Uç: viewer kapısız GET, şekil (boş dizi, null değil) ve tek anahtar.
func TestGetProblemsBreakdownHandler(t *testing.T) {
	var calls int
	var gotBucket int64
	var gotSpan time.Duration
	orig := problemsBreakdownRead
	problemsBreakdownRead = func(_ context.Context, _ *Server, from, to time.Time, bucket int64) (chstore.ProblemSourceAgg, error) {
		calls++
		gotBucket, gotSpan = bucket, to.Sub(from)
		return chstore.ProblemSourceAgg{}, nil
	}
	t.Cleanup(func() { problemsBreakdownRead = orig })

	s := &Server{cache: &fakeCache{}, l1: newL1Cache(8), stats: newCacheStats()}
	get := func(q string) map[string]any {
		w := httptest.NewRecorder()
		s.getProblemsBreakdown(w, httptest.NewRequest("GET", "/api/problems/breakdown"+q, nil))
		if w.Code != 200 {
			t.Fatalf("%s: kod %d", q, w.Code)
		}
		var m map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := get("?since=24h")
	if gotBucket != 3600 || gotSpan != 24*time.Hour {
		t.Errorf("24h: kova %d, pencere %s", gotBucket, gotSpan)
	}
	if m["since"] != "24h" || m["families"] == nil || m["series"] == nil {
		t.Errorf("şekil: %v", m)
	}
	if s, ok := m["series"].([]any); !ok || len(s) < 24 || len(s) > 25 { // kısmi ilk kova dahil
		t.Errorf("24 saatlik seri: %v", m["series"])
	}
	get("?since=24h")
	if calls != 1 {
		t.Errorf("aynı basamak önbellekten gelmeli: %d okuma", calls)
	}
	get("?since=bogus")
	if gotBucket != 86400 || gotSpan != 7*24*time.Hour || calls != 2 {
		t.Errorf("bozuk since → 7d: kova %d pencere %s okuma %d", gotBucket, gotSpan, calls)
	}
}
