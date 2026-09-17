package chstore

// problem_stats_test.go — v0.10.774: kova merdiveni + saf istatistik.

import (
	"testing"
	"time"
)

func TestProblemStatsStep(t *testing.T) {
	for w, want := range map[time.Duration]time.Duration{
		time.Hour: 5 * time.Minute, 6 * time.Hour: 5 * time.Minute, 24 * time.Hour: 15 * time.Minute,
		7 * 24 * time.Hour: time.Hour, 30 * 24 * time.Hour: 6 * time.Hour,
	} {
		if got := problemStatsStep(w); got != want {
			t.Errorf("%v → %v, istenen %v", w, got, want)
		}
	}
}

func TestProblemStatsFromRows(t *testing.T) {
	from := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	ns := func(m int) int64 { return from.Add(time.Duration(m) * time.Minute).UnixNano() }
	ptr := func(v int64) *int64 { return &v }
	rows := []Problem{
		// pencerede açıldı, 12 dk sonra çözüldü
		{ID: "a", RuleID: "builtin-error-rate-15pct", Metric: "error_rate", Severity: "critical", StartedAt: ns(5), ResolvedAt: ptr(ns(17)), Value: 40, Threshold: 15, Status: "resolved"},
		// pencerede açıldı, 30 dk sonra çözüldü
		{ID: "b", RuleID: "slo:x:warning", Severity: "warning", StartedAt: ns(10), ResolvedAt: ptr(ns(40)), Status: "resolved"},
		// pencere ÖNCE açıldı, hâlâ açık → devreden + açık; critical bigBreach → P1
		{ID: "c", RuleID: "anomaly:svc:latency", Severity: "critical", StartedAt: ns(-30), Value: 10, Threshold: 1, Status: "open"},
		// pencerede açıldı, hâlâ açık; fırtına tanım gereği P1 (v0.9.1194)
		{ID: "d", RuleID: "exception-storm", Severity: "warning", StartedAt: ns(50), Status: "open"},
		// pencere öncesinde açılıp pencerede çözüldü → çözülen sayılır, açılan değil, devreden sayılır
		{ID: "e", RuleID: "monitor:1", Severity: "critical", StartedAt: ns(-60), ResolvedAt: ptr(ns(20)), Value: 0, Threshold: 1, Status: "resolved"},
		// pencere sonrası çözülen (sınır dışı) → açılan sayılır, çözülen değil
		{ID: "f", RuleID: "db-capacity:x", Severity: "warning", StartedAt: ns(30), ResolvedAt: ptr(ns(90)), Status: "resolved"},
	}
	st := ProblemStatsFromRows(rows, from, to, 15*time.Minute, to.UnixNano(), ProblemPriorityConfig{}, false)
	if st.StepS != 900 || len(st.Buckets) != 4 || st.Buckets[1].TimeS != from.Add(15*time.Minute).Unix() {
		t.Fatalf("kovalar: %+v", st.Buckets)
	}
	if st.Opened != 4 || st.Resolved != 3 || st.OpenNow != 2 || st.Carried != 2 {
		t.Errorf("sayılar: opened=%d resolved=%d openNow=%d carried=%d", st.Opened, st.Resolved, st.OpenNow, st.Carried)
	}
	// Açılış 05,10 → kova0 ×2; 30 → kova2; 50 → kova3. Çözüm 17,20 → kova1 ×2; 40 → kova2.
	if st.Buckets[0].Opened != 2 || st.Buckets[2].Opened != 1 || st.Buckets[3].Opened != 1 || st.Buckets[1].Resolved != 2 || st.Buckets[2].Resolved != 1 {
		t.Errorf("kova dağılımı: %+v", st.Buckets)
	}
	// MTTR: 12 dk, 30 dk, 80 dk (e: -60 → 20) → ortanca 30 dk, p90 80 dk, ortalama 40.67 dk.
	if st.MTTR.N != 3 || st.MTTR.MedianS != 1800 || st.MTTR.P90S != 4800 || int(st.MTTR.MeanS) != 2440 {
		t.Errorf("mttr: %+v", st.MTTR)
	}
	if st.ByPriority["P1"] != 2 || st.ByPriority["P2"] != 0 || st.ByPriority["P3"] != 0 {
		t.Errorf("öncelik (anomali bigBreach P1 + fırtına P1): %+v", st.ByPriority)
	}
	if len(st.ByCategory) == 0 {
		t.Errorf("kategori: %+v", st.ByCategory)
	}
	empty := ProblemStatsFromRows(nil, from, to, 15*time.Minute, to.UnixNano(), ProblemPriorityConfig{}, true)
	if !empty.Truncated || empty.MTTR.N != 0 || empty.Buckets == nil || empty.ByCategory == nil || len(empty.Buckets) != 4 {
		t.Errorf("boş: %+v", empty)
	}
}

func TestMTTROf(t *testing.T) {
	m := mttrOf([]float64{10, 40, 20, 30})
	if m.N != 4 || m.MedianS != 25 || m.MeanS != 25 || m.P90S != 40 {
		t.Errorf("çift: %+v", m)
	}
	if one := mttrOf([]float64{7}); one.MedianS != 7 || one.P90S != 7 {
		t.Errorf("tek: %+v", one)
	}
}
