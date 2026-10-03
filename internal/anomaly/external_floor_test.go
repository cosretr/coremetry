package anomaly

// external_floor_test.go — v0.10.1083. Operatör: "Oracle hataları da
// problemse hâlâ düşmüyor" → onay "Yap". Oracle hata tablosu serisi seyrek:
// taban medyanı 0, Problem.Threshold = medyan = 0 → computePriority oran
// kuramıyor, critical satır kaynakta P2'de takılıyordu. Sözleşme:
//   - Threshold = max(medyan, MinAbsDelta tabanı 5) — gerekçe gerçek medyanı yazar
//   - 12/dk critical → 12/5 = 2.4× → P1; 7/dk critical → 1.4× → P2
//   - tabandan önce açılmış satır (Threshold 0) tazelemede tabana yükselir
//   - küme Problem'i iki koldan büyüğüyle ölçülür: en güçlü üye ya da yayılım
//     (N / 3) — tek güçlü üye de, 50 serilik 6/dk fırtına da P1
//   - sürdürme (Dwell) üst-yazımı: 10 ardışık dakika gerekmeden açılmaz

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// zeroBase — n dakikalık sıfır taban (seyrek hata serisi, medyan 0).
func zeroBase(n int) []float64 { return make([]float64, n) }

// floorNow — computePriority'nin bayat-critical kapısı (4 sa) gerçek saatle
// çalışır; StartedAt = tarama anı olsun diye taramalar "şimdi"de koşar.
func floorNow() time.Time { return time.Now().UTC().Truncate(time.Minute) }

func onlyOpened(t *testing.T, f *fakeExtStore) chstore.Problem {
	t.Helper()
	if len(f.upserts) != 1 || f.upserts[0].Status != "open" {
		t.Fatalf("tek açılış bekleniyordu: %+v", f.upserts)
	}
	return f.upserts[0]
}

func priorityOf(p chstore.Problem) (string, string) {
	out := chstore.EnrichProblemsWithPriority([]chstore.Problem{p})
	return out[0].Priority, out[0].PriorityReason
}

func TestExternalThreshold_FloorsMedian(t *testing.T) {
	for _, c := range []struct{ median, floor, want float64 }{
		{0, 5, 5}, {3, 5, 5}, {5, 5, 5}, {40, 5, 40}, {0, 20, 20},
	} {
		if got := externalThreshold(c.median, c.floor); got != c.want {
			t.Errorf("externalThreshold(%v, %v) = %v, istenen %v", c.median, c.floor, got, c.want)
		}
	}
}

// Medyan 0 → Threshold 5; 12/dk critical → P1 (computePriority 2.4×).
func TestExternalScan_ZeroMedianFloorReachesP1(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := floorNow()
	vals := append(zeroBase(30), repeat(12, cfg.DwellBuckets)...)
	f := &fakeExtStore{cfg: cfg, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
	if _, err := newExtScanner(f, now).Scan(context.Background(), extTarget); err != nil {
		t.Fatal(err)
	}
	p := onlyOpened(t, f)
	if p.Severity != "critical" || p.Value != 12 || p.Threshold != 5 {
		t.Fatalf("critical, Value 12, Threshold = taban 5 bekleniyordu: %+v", p)
	}
	if !strings.Contains(p.Description, "vs baseline 0 ") {
		t.Errorf("gerekçe GERÇEK medyanı (0) yazmalı: %q", p.Description)
	}
	if pr, why := priorityOf(p); pr != "P1" {
		t.Errorf("12/dk vs taban 5 → P1 bekleniyordu, %s (%s)", pr, why)
	}
}

// 7/dk critical (kritik z sorgu eşiğiyle 4'e indirildi) → 1.4× → P2.
func TestExternalScan_ZeroMedianFloorBelowBigBreachStaysP2(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := floorNow()
	vals := append(zeroBase(30), repeat(7, cfg.DwellBuckets)...)
	f := &fakeExtStore{cfg: cfg, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
	target := extTarget
	target.Thresholds = ExternalThresholds{CriticalZ: 4}
	if _, err := newExtScanner(f, now).Scan(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	p := onlyOpened(t, f)
	if p.Severity != "critical" || p.Value != 7 || p.Threshold != 5 {
		t.Fatalf("critical, Value 7, Threshold 5 bekleniyordu: %+v", p)
	}
	if pr, why := priorityOf(p); pr != "P2" {
		t.Errorf("7/dk vs taban 5 (1.4×) → P2 bekleniyordu, %s (%s)", pr, why)
	}
}

// Sorgu eşiği MinAbsDelta'yı yükseltirse taban da yükselir (aynı vida).
func TestExternalScan_FloorFollowsQueryMinAbsDelta(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := floorNow()
	vals := append(zeroBase(30), repeat(30, cfg.DwellBuckets)...)
	f := &fakeExtStore{cfg: cfg, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
	target := extTarget
	target.Thresholds = ExternalThresholds{MinAbsDelta: 20}
	if _, err := newExtScanner(f, now).Scan(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if p := onlyOpened(t, f); p.Threshold != 20 {
		t.Fatalf("taban sorgu eşiğinden (20) gelmeli: %+v", p)
	}
}

// Tabandan önce açılmış satır (Threshold 0 = medyan) tazelemede tabana
// yükselir; daha büyük açılış medyanı korunur.
func TestExternalScan_RefreshLiftsLegacyZeroThreshold(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := floorNow()
	vals := append(zeroBase(30), repeat(12, cfg.DwellBuckets)...)
	for _, c := range []struct {
		name    string
		stored  float64
		wantThr float64
	}{{"eski satır 0 → 5", 0, 5}, {"büyük açılış medyanı korunur", 8, 8}} {
		open := chstore.Problem{ID: "p-open", RuleID: "anomaly:ext:extsrc/OP1/E1:ext:fail_count", Service: "ext:extsrc/OP1/E1",
			Kind: chstore.ProblemKindExternal, Metric: "ext:fail_count", Status: "open", Severity: "critical",
			Value: 12, Threshold: c.stored, StartedAt: now.UnixNano()}
		f := &fakeExtStore{cfg: cfg, open: []chstore.Problem{open}, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
		rep, err := newExtScanner(f, now).Scan(context.Background(), extTarget)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Refreshed != 1 || len(f.upserts) != 1 || f.upserts[0].Threshold != c.wantThr {
			t.Errorf("%s: %+v upserts=%+v", c.name, rep, f.upserts)
		}
	}
}

// Küme: aynı operasyonda 3 seri 12/dk → küme Value 12 / Threshold 5 → P1;
// 7/dk → P2. Eskiden Value = üye sayısı (3) / 3 → hep 1.0× → P2.
func TestExternalScan_ClusterMeasuredByStrongestMember(t *testing.T) {
	for _, c := range []struct {
		level float64
		want  string
	}{{12, "P1"}, {7, "P2"}} {
		cfg := chstore.DefaultAnomalySensitivity()
		now := floorNow()
		var series []chstore.SpanMetricSeries
		for _, code := range []string{"E00", "E01", "E02"} {
			series = append(series, extSeries(append(zeroBase(30), repeat(c.level, cfg.DwellBuckets)...), now, "OP_PAY", code))
		}
		f := &fakeExtStore{cfg: cfg, series: series}
		target := extTarget
		target.Thresholds = ExternalThresholds{CriticalZ: 4}
		if _, err := newExtScanner(f, now).Scan(context.Background(), target); err != nil {
			t.Fatal(err)
		}
		p := onlyOpened(t, f)
		if !strings.HasPrefix(p.RuleID, "anomaly-cluster:ext:") || p.Value != c.level || p.Threshold != 5 {
			t.Fatalf("%v/dk: küme Problem'i en güçlü üyeyle ölçülmeli: %+v", c.level, p)
		}
		if !strings.Contains(p.Description, "3 seri") {
			t.Errorf("üye sayısı gerekçede kalmalı: %q", p.Description)
		}
		if pr, why := priorityOf(p); pr != c.want {
			t.Errorf("%v/dk küme → %s bekleniyordu, %s (%s)", c.level, c.want, pr, why)
		}
	}
}

func TestExternalClusterMeasure_LargerOfPeakAndBreadth(t *testing.T) {
	// Tek güçlü üye kazanır: 12/5 = 2.4 > 3/3 = 1.
	m := externalClusterMeasure([]anomalyOutcome{
		{Current: 30, Median: 20}, // 30/20 = 1.5
		{Current: 12, Median: 0},  // 12/5  = 2.4 ← en güçlü
		{Current: 8, Median: 1},   // 8/5   = 1.6
	}, 5)
	if m.Breadth || m.Value != 12 || m.Threshold != 5 || m.PeakMedian != 0 || !strings.Contains(m.reason(), "en güçlü üye 12/dk") {
		t.Errorf("en güçlü üye kazanmalı: %+v %q", m, m.reason())
	}
	// Geniş fırtına kazanır: 50 seri × 6/dk → 50/3 = 16.7 > 6/5 = 1.2.
	wide := make([]anomalyOutcome, 50)
	for i := range wide {
		wide[i] = anomalyOutcome{Current: 6}
	}
	w := externalClusterMeasure(wide, 5)
	if !w.Breadth || w.Value != 50 || w.Threshold != float64(clusterMinMembers) || !strings.Contains(w.reason(), "yayılım — 50 seri / en az 3") {
		t.Errorf("yayılım kazanmalı: %+v %q", w, w.reason())
	}
}

// Tarama: 50 serilik 6/dk fırtına → küme Value 50 / Threshold 3 → P1 (yalnız
// en güçlü üye ölçülseydi 1.2× → P2).
func TestExternalScan_WideClusterStormReachesP1(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	cfg.ExternalOpenCapPerTick = 200
	now := floorNow()
	var series []chstore.SpanMetricSeries
	for i := 0; i < 50; i++ {
		series = append(series, extSeries(append(zeroBase(30), repeat(6, cfg.DwellBuckets)...), now, "OP_PAY", fmt.Sprintf("E%02d", i)))
	}
	f := &fakeExtStore{cfg: cfg, series: series}
	target := extTarget
	target.Thresholds = ExternalThresholds{CriticalZ: 4}
	if _, err := newExtScanner(f, now).Scan(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	p := onlyOpened(t, f)
	if p.Value != 50 || p.Threshold != 3 || !strings.Contains(p.Description, "Ölçü: yayılım") {
		t.Fatalf("yayılım ölçüsü bekleniyordu: %+v", p)
	}
	if pr, why := priorityOf(p); pr != "P1" {
		t.Errorf("50 serilik fırtına → P1 bekleniyordu, %s (%s)", pr, why)
	}
}

// Sürdürme: Oracle taraması Dwell 10 geçer — 9 dakikalık patlama açmaz, 10 açar.
func TestExternalScan_DwellOverrideNeedsTenMinutes(t *testing.T) {
	for _, c := range []struct {
		minutes int
		opened  int
	}{{9, 0}, {10, 1}} {
		cfg := chstore.DefaultAnomalySensitivity()
		now := floorNow()
		vals := append(zeroBase(40), repeat(60, c.minutes)...)
		f := &fakeExtStore{cfg: cfg, series: []chstore.SpanMetricSeries{extSeries(vals, now, "OP1", "E1")}}
		target := extTarget
		target.Thresholds = ExternalThresholds{Dwell: 10}
		rep, err := newExtScanner(f, now).Scan(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Opened != c.opened {
			t.Errorf("%d dk patlama: açılış %d, istenen %d (%+v)", c.minutes, rep.Opened, c.opened, rep)
		}
	}
}
