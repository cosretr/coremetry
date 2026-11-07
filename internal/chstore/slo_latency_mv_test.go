package chstore

// v0.10.518 — latency SLI on spanmetrics_1m (inverse CDF from the merged
// t-digest). Prod: "[slo] durum hesabı başarısız … code 159 … 20 s" — the
// 140-SLO fan-out scanned raw spans per latency SLO over WindowDays.
// These pin the pure seam: the level grid, the interpolation, the
// eligibility guard and the SQL shape.

import (
	"math"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSLOLatencyLevelsAscendingAndTailDense(t *testing.T) {
	lv := sloLatencyLevels
	if len(lv) < 100 {
		t.Fatalf("grid too coarse: %d levels", len(lv))
	}
	for i := 1; i < len(lv); i++ {
		if lv[i] <= lv[i-1] {
			t.Fatalf("levels not strictly ascending at %d: %v <= %v", i, lv[i], lv[i-1])
		}
	}
	if lv[0] <= 0 || lv[len(lv)-1] >= 1 {
		t.Fatalf("levels must stay inside (0,1): first %v last %v", lv[0], lv[len(lv)-1])
	}
	// A 99.9 % SLO needs the crossing resolved below its 0.1 pp budget.
	want := []float64{0.99, 0.999, 0.9995, 0.9999, 0.99999}
	for _, w := range want {
		found := false
		for _, l := range lv {
			if math.Abs(l-w) < 1e-12 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("grid lacks level %v", w)
		}
	}
	if !strings.Contains(sloLatencyLevelsSQL, "0.999,") || !strings.Contains(sloLatencyLevelsSQL, "0.99999") {
		t.Errorf("SQL level list missing tail levels: %s", sloLatencyLevelsSQL)
	}
}

func TestSLOGoodFraction(t *testing.T) {
	lv := []float64{0.25, 0.5, 0.75, 0.99}
	qs := []float64{100, 200, 400, 1000}
	cases := []struct {
		name string
		lv   []float64
		qs   []float64
		thr  float64
		want float64
	}{
		{"empty curve → 0, never 'all good'", lv, nil, 500, 0},
		{"length mismatch → 0", lv, []float64{1, 2}, 500, 0},
		{"below first point interpolates from origin", lv, qs, 50, 0.125},
		{"exact grid point", lv, qs, 200, 0.5},
		{"interior interpolation", lv, qs, 300, 0.625},
		{"at last point → 1", lv, qs, 1000, 1},
		{"above last point → 1", lv, qs, 5000, 1},
		{"threshold zero → 0", lv, qs, 0, 0},
		{"plateau (equal quantiles) does not divide by zero", lv, []float64{100, 100, 100, 1000}, 100, 0.75},
	}
	for _, c := range cases {
		got := sloGoodFraction(c.lv, c.qs, c.thr)
		if math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Synthetic population: the interpolation over the real grid must land
// within 0.5 pp of the exact countIf fraction across thresholds that hit
// the body and the tail.
func TestSLOGoodFractionAgainstExactCDF(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	n := 200000
	pop := make([]float64, n)
	for i := range pop {
		pop[i] = math.Exp(r.NormFloat64()*0.8 + math.Log(120e6)) // log-normal around 120 ms
	}
	sort.Float64s(pop)
	qs := make([]float64, len(sloLatencyLevels))
	for i, l := range sloLatencyLevels {
		qs[i] = pop[int(l*float64(n-1))]
	}
	for _, thrMs := range []float64{50, 120, 250, 500, 1000, 2000} {
		thr := thrMs * 1e6
		exact := float64(sort.SearchFloat64s(pop, thr+1)) / float64(n)
		got := sloGoodFraction(sloLatencyLevels, qs, thr)
		if math.Abs(got-exact) > 0.005 {
			t.Errorf("thr %v ms: interpolated %.5f exact %.5f (Δ %.5f)", thrMs, got, exact, got-exact)
		}
	}
}

func TestSLOGoodCountNeverExceedsTotal(t *testing.T) {
	if g := sloGoodCount(1000, 0.5); g != 500 {
		t.Errorf("got %d want 500", g)
	}
	if g := sloGoodCount(1000, 1.0000001); g != 1000 {
		t.Errorf("clamp: got %d want 1000", g)
	}
	if g := sloGoodCount(0, 1); g != 0 {
		t.Errorf("zero total: got %d", g)
	}
}

func TestSLOLatencyMVFrom(t *testing.T) {
	now := time.Date(2026, 11, 7, 7, 15, 0, 0, time.UTC)
	cov := now.Add(-30 * 24 * time.Hour)
	if from, ok := sloLatencyMVFrom(now.Add(-7*24*time.Hour), cov, now); !ok || !from.Equal(now.Add(-7*24*time.Hour)) {
		t.Error("7d window inside 30d coverage: MV from the window start")
	}
	if _, ok := sloLatencyMVFrom(now.Add(-90*24*time.Hour), cov, now); ok {
		t.Error("90d window before coverage must fall back to raw")
	}
	// Fail-safe probe (coverage = now) → never the MV, even for a short window.
	if _, ok := sloLatencyMVFrom(now.Add(-time.Hour), now.Add(time.Second), now); ok {
		t.Error("coverage after window start (fail-safe now) must fall back")
	}
}

// v0.10.1924 (operatör-raporlu, prod: 154 SLO'luk fan-out 20 sn'de kesiliyor)
// — spanmetrics_1m TTL 30 gün, varsayılan pencere 30 gün; kapsam probu en eski
// günü atlayıp ertesi günden başlatıyor. Eski kural (tam kapsam) her 30 günlük
// gecikme SLO'sunu ham span taramasına düşürüyordu. TTL sınırındaki küçük açık
// artık MV'den, kapsam başından okunur.
func TestSLOLatencyMVFromClampsTTLBoundary(t *testing.T) {
	now := time.Date(2026, 11, 7, 7, 15, 0, 0, time.UTC)
	since := now.Add(-30 * 24 * time.Hour) // 2026-10-08 07:15
	// TTL `toDate + 30 DAY` → en eski bölüm 2026-10-08; kapsam ertesi gün.
	cov, ok := coverageFromPartsDate(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), now)
	if !ok {
		t.Fatal("kapsam hesaplanmalı")
	}
	from, ok := sloLatencyMVFrom(since, cov, now)
	if !ok {
		t.Fatal("30 günlük pencere TTL sınırında MV'den okunmalı (eski kural ham yola düşürüyordu)")
	}
	if !from.Equal(cov) || now.Sub(from) < 28*24*time.Hour {
		t.Errorf("kapsam başından, ≥ 28 gün: from=%v", from)
	}
	// Açık 48 sa'i ya da pencerenin %10'unu aşarsa ham yol.
	if _, ok := sloLatencyMVFrom(now.Add(-7*24*time.Hour), now.Add(-5*24*time.Hour), now); ok {
		t.Error("7 günlük pencerede 2 günlük açık (%28) ham yola düşmeli")
	}
	if _, ok := sloLatencyMVFrom(now.Add(-60*24*time.Hour), now.Add(-57*24*time.Hour), now); ok {
		t.Error("72 sa açık (48 sa üstü) ham yola düşmeli")
	}
}

// Üç gecikme yolu (durum, yanma serisi, yanma oranı) aynı kuralı kullanır.
func TestSLOLatencyPathsUseClampRule(t *testing.T) {
	for _, fn := range []string{"func (s *Store) ComputeSLOStatus(", "func (s *Store) ComputeSLOBurnSeries(", "func (s *Store) ComputeSLOBurnRate("} {
		if !strings.Contains(funcBody(t, "slo.go", fn), "sloLatencyMVFrom(") {
			t.Errorf("%s sloLatencyMVFrom kullanmalı", fn)
		}
	}
}

func TestSLOLatencyMVSQLShape(t *testing.T) {
	q := sloLatencyMVSQL("spanmetrics_1m", false, false, 20)
	for _, want := range []string{
		"FROM spanmetrics_1m", "countMerge(calls_state)", "quantilesTDigestMerge(",
		"(duration_q_state)", sloLatencyEntryWhere, "time_bucket >= ?", "service_name = ?",
		"arrayMap(x -> toFloat64(x)", "SETTINGS max_execution_time = 20",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("status SQL lacks %q:\n%s", want, q)
		}
	}
	// " AND name = ?" — bare "name = ?" is a substring of "service_name = ?"
	// (feedback-gate-matches-its-own-text).
	if strings.Contains(q, " AND name = ?") || strings.Contains(q, "GROUP BY") {
		t.Errorf("ungrouped/no-operation SQL must not carry name/GROUP BY:\n%s", q)
	}
	if strings.Contains(q, "FROM spans") {
		t.Errorf("MV path must not touch raw spans:\n%s", q)
	}
	g := sloLatencyMVSQL("spanmetrics_1m", true, true, 15)
	for _, want := range []string{
		"toStartOfDay(time_bucket) AS bucket", " AND name = ?", "GROUP BY bucket ORDER BY bucket",
		"SETTINGS max_execution_time = 15",
	} {
		if !strings.Contains(g, want) {
			t.Errorf("grouped SQL lacks %q:\n%s", want, g)
		}
	}
	// Runtime-verification handle: `go test -run TestSLOLatencyMVSQLShape -v`
	// prints the exact statements so they can be executed against a live CH.
	t.Logf("STATUS_SQL: %s", q)
	t.Logf("GROUPED_SQL: %s", g)
}
