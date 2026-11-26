package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.2476 — consumer lag günlük desen kuralı: önceki 7 günün ≥ 3'ünde
// benzer saatte (başlangıç saati ± 1 sa) aynı seride lag olayı → beklenen desen.

func TestLagDailyPatternDays(t *testing.T) {
	// Bugün 14:20 UTC; saat dilimi 14:00 → pencere her gün 13:00–16:00 (hariç).
	cur := time.Date(2026, 11, 26, 14, 20, 0, 0, time.UTC)
	day := func(k int, h, m int) int64 {
		return time.Date(2026, 11, 26-k, h, m, 0, 0, time.UTC).UnixNano()
	}
	for _, tc := range []struct {
		name  string
		prior []int64
		days  int
		on    bool
	}{
		{"geçmiş yok", nil, 0, false},
		{"2/7 → desen değil", []int64{day(1, 14, 5), day(2, 13, 40)}, 2, false},
		{"3/7 → desen", []int64{day(1, 14, 5), day(2, 13, 40), day(5, 15, 10)}, 3, true},
		{"aynı günün iki bölümü tek gün sayılır", []int64{day(1, 14, 5), day(1, 15, 0), day(2, 14, 0)}, 2, false},
		{"bugünkü önceki bölüm sayılmaz", []int64{day(0, 13, 10), day(1, 14, 5), day(2, 14, 0)}, 2, false},
		{"8 gün önce pencere dışı", []int64{day(1, 14, 5), day(2, 14, 0), day(8, 14, 0)}, 2, false},
		{"7 gün önce pencere içi", []int64{day(1, 14, 5), day(2, 14, 0), day(7, 14, 0)}, 3, true},
		{"saat kenarı: h-1 başı dahil", []int64{day(1, 13, 0), day(2, 13, 0), day(3, 13, 0)}, 3, true},
		{"saat kenarı: h-1'den 1 sn önce hariç", []int64{day(1, 12, 59) + 59e9, day(2, 14, 0), day(3, 14, 0)}, 2, false},
		{"saat kenarı: h+1 sonu dahil", []int64{day(1, 15, 59), day(2, 15, 59), day(3, 15, 59)}, 3, true},
		{"saat kenarı: h+2 başı hariç", []int64{day(1, 16, 0), day(2, 14, 0), day(3, 14, 0)}, 2, false},
		{"7/7", []int64{day(1, 14, 0), day(2, 14, 0), day(3, 14, 0), day(4, 14, 0), day(5, 14, 0), day(6, 14, 0), day(7, 14, 0)}, 7, true},
		{"sıfır / gelecek değer yok sayılır", []int64{0, -5, cur.Add(time.Hour).UnixNano(), day(1, 14, 0)}, 1, false},
	} {
		got := LagDailyPatternDays(cur.UnixNano(), tc.prior)
		if got != tc.days || IsLagDailyPattern(got) != tc.on {
			t.Errorf("%s: gün %d (beklenen %d), desen %v", tc.name, got, tc.days, IsLagDailyPattern(got))
		}
	}
}

// Gece yarısı kenarı: 00:10'daki olayın penceresi her gün 23:00–02:00; 25'i
// 23:30 bugünkü pencerededir (önceki gün sayılmaz), 24'ü 23:30 dünküdür.
func TestLagDailyPatternDaysMidnight(t *testing.T) {
	cur := time.Date(2026, 11, 26, 0, 10, 0, 0, time.UTC)
	at := func(d, h, m int) int64 { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC).UnixNano() }
	for _, tc := range []struct {
		name  string
		prior []int64
		days  int
	}{
		{"23:xx önceki günün penceresi → sayılır", []int64{at(25, 23, 30), at(24, 23, 30), at(23, 23, 0), at(23, 0, 50)}, 3},
		{"22:59 → hariç", []int64{at(24, 22, 59), at(23, 23, 0), at(23, 1, 30)}, 2},
	} {
		if got := LagDailyPatternDays(cur.UnixNano(), tc.prior); got != tc.days {
			t.Errorf("%s: %d, beklenen %d", tc.name, got, tc.days)
		}
	}
}

func TestLagDailyPatternText(t *testing.T) {
	for _, tc := range []struct {
		days int
		want string
	}{
		{3, "beklenen günlük desen: son 7 günün 3'ünde benzer saatte görüldü"},
		{4, "beklenen günlük desen: son 7 günün 4'ünde benzer saatte görüldü"},
		{5, "beklenen günlük desen: son 7 günün 5'inde benzer saatte görüldü"},
		{6, "beklenen günlük desen: son 7 günün 6'sında benzer saatte görüldü"},
		{7, "beklenen günlük desen: son 7 günün 7'sinde benzer saatte görüldü"},
	} {
		if got := LagDailyPatternText(tc.days); got != tc.want {
			t.Errorf("%d: %q", tc.days, got)
		}
	}
}

// Desen satırı (Metric MLGroupLagPatternMetric) şiddetten bağımsız P3, gerekçe cümleyle.
func TestLagPatternPriorityP3(t *testing.T) {
	now := time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)
	cfg := DefaultProblemPriority()
	for _, sev := range []string{"warning", "critical"} {
		p := Problem{RuleID: MLGroupRulePrefix + "x", Severity: sev, Metric: MLGroupLagPatternMetric, Comparator: ">=",
			Value: 4, Threshold: LagPatternWindowDays, StartedAt: now.Add(-6 * time.Hour).UnixNano()}
		got, why := PreviewProblemPriority(p, now.UnixNano(), cfg)
		if got != "P3" || !strings.Contains(why, "4'ünde") {
			t.Errorf("%s: %s %q", sev, got, why)
		}
	}
}

func TestLagPatternStdLag(t *testing.T) {
	l := lagPatternOf(Problem{Metric: MLGroupLagPatternMetric, Value: 5}, StdLag{Topic: "orders"})
	if l.PatternDays != 5 || !strings.Contains(l.Pattern, "5'inde") {
		t.Fatalf("%+v", l)
	}
	if l := lagPatternOf(Problem{Metric: "ml_group_lag", Value: 5}, StdLag{}); l.PatternDays != 0 || l.Pattern != "" {
		t.Fatalf("desen olmayan satır: %+v", l)
	}
}

func TestLagDailyPatternSetting(t *testing.T) {
	off := false
	if !(MLConfig{}).LagDailyPatternOn() || (MLConfig{LagDailyPattern: &off}).LagDailyPatternOn() {
		t.Fatal("nil = açık, false = kapalı")
	}
	if (MLConfig{}).ConfigHash() != (MLConfig{LagDailyPattern: &off}).ConfigHash() {
		t.Fatal("anahtar ConfigHash'e girmemeli")
	}
}

func TestMLLagHistorySQLBounded(t *testing.T) {
	for _, want := range []string{"LIMIT", "max_execution_time", "started_at >= ?", "started_at < ?", "detector = 'metric_kafka'"} {
		if !strings.Contains(mlLagHistorySQL, want) {
			t.Errorf("sorguda %q yok", want)
		}
	}
}
