package ml

// v0.10.1814 — öğrenme döngüsü Faz 1 dilim 1.3 (audit §4.1–4.2): günlük
// baseline gözlem çekirdeği. Sözleşme: bir yerel günün ml_series kovaları →
// (sinyal, servis, part, yerel saat) başına q10/q50/q90/q99/vmax + çağrı +
// kova sayısı; sınıf takvimden (tek işlev), etiketli gerçek anomali kovaları
// dışlanır, aynı girdi her liderde aynı satırları üretir.

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var istanbul = func() *time.Location {
	l, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		panic(err)
	}
	return l
}()

func TestQuantileHF7(t *testing.T) {
	twelve := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	cases := []struct {
		name string
		xs   []float64
		p    float64
		want float64
	}{
		{"12 örnek q10", twelve, 0.10, 2.1},
		{"12 örnek q50", twelve, 0.50, 6.5},
		{"12 örnek q90", twelve, 0.90, 10.9},
		{"12 örnek q99", twelve, 0.99, 11.89},
		{"12 örnek q100 = max", twelve, 1, 12},
		{"tek örnek", []float64{7}, 0.9, 7},
		{"iki örnek doğrusal", []float64{0, 10}, 0.10, 1},
		{"boş", nil, 0.5, 0},
	}
	for _, c := range cases {
		if got := quantileHF7(c.xs, c.p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBaselineObsClass(t *testing.T) {
	cal := chstore.AnomalyCalendarConfig{Timezone: "Europe/Istanbul", FixedHolidaysTR: true,
		Days: []chstore.AnomalySpecialDay{{From: "2026-11-30", Kind: chstore.CalendarMonthEnd}}}
	cases := []struct {
		at   time.Time
		want string
	}{
		{time.Date(2026, 11, 4, 10, 0, 0, 0, istanbul), "normal"},   // çarşamba
		{time.Date(2026, 11, 8, 10, 0, 0, 0, istanbul), "normal"},   // pazar: gün sınıfı slotta
		{time.Date(2026, 10, 29, 10, 0, 0, 0, istanbul), "holiday"}, // 29 Ekim
		{time.Date(2026, 10, 28, 12, 0, 0, 0, istanbul), "normal"},  // yarım gün sabahı
		{time.Date(2026, 10, 28, 13, 0, 0, 0, istanbul), "half_day"},
		{time.Date(2026, 11, 30, 9, 0, 0, 0, istanbul), "month_end"},
	}
	for _, c := range cases {
		if got := baselineObsClass(c.at, cal); got != c.want {
			t.Errorf("%s: %q, want %q", c.at, got, c.want)
		}
	}
}

func TestBaselinePendingDays(t *testing.T) {
	at := func(d, h int) time.Time { return time.Date(2026, 11, d, h, 0, 0, 0, istanbul) }
	cases := []struct {
		name string
		now  time.Time
		last string
		want []string
	}{
		{"03:00 sonrası, hiç işlenmemiş: son 7 gün, eskiden yeniye", at(5, 4), "",
			[]string{"2026-10-29", "2026-10-30", "2026-10-31", "2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"}},
		{"03:00 öncesi dün henüz sırada değil", at(5, 2), "2026-11-02", []string{"2026-11-03"}},
		{"bir gün kaçmış", at(5, 4), "2026-11-02", []string{"2026-11-03", "2026-11-04"}},
		{"güncel", at(5, 4), "2026-11-04", nil},
		{"uzun kesinti: yalnız en yeni 7", at(5, 4), "2026-09-01",
			[]string{"2026-10-29", "2026-10-30", "2026-10-31", "2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"}},
		{"bozuk kayıt baştan sayılır", at(5, 4), "dün", []string{"2026-10-29", "2026-10-30", "2026-10-31", "2026-11-01", "2026-11-02", "2026-11-03", "2026-11-04"}},
	}
	for _, c := range cases {
		if got := baselinePendingDays(c.now, istanbul, c.last, 7); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestBaselineManualDay(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, istanbul)
	cases := []struct {
		day string
		ok  bool
	}{
		{"2026-11-04", true},
		{"2026-10-08", true}, // 28 gün önce: ml_series TTL içinde
		{"2026-10-07", false},
		{"2026-11-05", false}, // bugün: gün bitmedi
		{"2026-11-06", false},
		{"04.11.2026", false},
		{"", false},
	}
	for _, c := range cases {
		if err := BaselineManualDayOK(c.day, now, istanbul); (err == nil) != c.ok {
			t.Errorf("%q: err=%v, ok beklenen %v", c.day, err, c.ok)
		}
	}
}

func TestBaselineDayBounds(t *testing.T) {
	from, to, err := baselineDayBounds("2026-11-04", istanbul)
	if err != nil {
		t.Fatal(err)
	}
	if !from.Equal(time.Date(2026, 11, 3, 21, 0, 0, 0, time.UTC)) || to.Sub(from) != 24*time.Hour {
		t.Fatalf("bounds %v .. %v", from, to)
	}
}

// dayFixture — 2026-11-04 (çarşamba, İstanbul) günü.
func dayFixture() []chstore.MLSeriesRow {
	h := func(hour, min int) time.Time { return time.Date(2026, 11, 4, hour, min, 0, 0, istanbul) }
	var rows []chstore.MLSeriesRow
	// log orders-api _total, 10:00 saati: 12 kova, 1..12
	for i := 0; i < 12; i++ {
		rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: h(10, 5*i), BucketSec: 300, Count: float64(i + 1)})
	}
	// 11:00 saati yalnız saatlik backfill satırı (3600 sn): 5 dk'ya normalize 300, tek örnek
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: h(11, 0), BucketSec: 3600, Count: 3600})
	// 12:00 saati: hem saatlik hem 5 dk satırı → yalnız en ince tane (5 dk)
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: h(12, 0), BucketSec: 3600, Count: 99999})
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: h(12, 0), BucketSec: 300, Count: 40})
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "_total", Bucket: h(12, 5), BucketSec: 300, Count: 60})
	// gün kovası (kod geçmişi, 86400 sn) saate sığmaz → yok sayılır
	rows = append(rows, chstore.MLSeriesRow{Signal: "log", Service: "orders-api", Part: "code|E42", Bucket: h(3, 0), BucketSec: 86400, Count: 7})
	// trace payments-mobile iki route, istek/sn → servis düzeyi _calls
	for i := 0; i < 2; i++ {
		rows = append(rows, chstore.MLSeriesRow{Signal: "trace", Service: "payments-mobile", Part: "/a|count", Bucket: h(9, 5*i), BucketSec: 300, Count: 2})
		rows = append(rows, chstore.MLSeriesRow{Signal: "trace", Service: "payments-mobile", Part: "/b|count", Bucket: h(9, 5*i), BucketSec: 300, Count: 1})
		rows = append(rows, chstore.MLSeriesRow{Signal: "trace", Service: "payments-mobile", Part: "/a|p95", Bucket: h(9, 5*i), BucketSec: 300, Count: 120})
	}
	// metrik gauge: çağrı yok, değer olduğu gibi
	rows = append(rows, chstore.MLSeriesRow{Signal: "metric", Service: "branch-teller", Part: "metric_jvm|heap_used", Bucket: h(14, 0), BucketSec: 300, Count: 5e8})
	return rows
}

func findObs(obs []chstore.MLBaselineObs, signal, service, part string, slot uint8) (chstore.MLBaselineObs, bool) {
	for _, o := range obs {
		if o.Signal == signal && o.Service == service && o.Part == part && o.Slot == slot {
			return o, true
		}
	}
	return chstore.MLBaselineObs{}, false
}

func TestBaselineDayObs(t *testing.T) {
	in := baselineInput{Loc: istanbul, TargetSec: 300, CfgHash: "h1"}
	obs := BaselineDayObs(dayFixture(), in)
	monday := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name                     string
		signal, service, part    string
		slot                     uint8
		q10, q50, q90, q99, vmax float32
		calls                    uint32
		buckets                  uint8
	}{
		{"12 kova 1..12", "log", "orders-api", "_total", 58, 2.1, 6.5, 10.9, 11.89, 12, 78, 12},
		{"saatlik backfill tek örnek", "log", "orders-api", "_total", 59, 300, 300, 300, 300, 300, 3600, 1},
		{"karışık tane: en ince", "log", "orders-api", "_total", 60, 42, 50, 58, 59.8, 60, 100, 2},
		{"trace route", "trace", "payments-mobile", "/a|count", 57, 2, 2, 2, 2, 2, 1200, 2},
		{"trace servis _calls = route toplamı", "trace", "payments-mobile", "_calls", 57, 3, 3, 3, 3, 3, 1800, 2},
		{"trace p95: çağrı yok", "trace", "payments-mobile", "/a|p95", 57, 120, 120, 120, 120, 120, 0, 2},
		{"metrik gauge", "metric", "branch-teller", "metric_jvm|heap_used", 62, 5e8, 5e8, 5e8, 5e8, 5e8, 0, 1},
	}
	for _, c := range cases {
		o, ok := findObs(obs, c.signal, c.service, c.part, c.slot)
		if !ok {
			t.Errorf("%s: satır yok", c.name)
			continue
		}
		near := func(a, b float32) bool { return math.Abs(float64(a-b)) <= 1e-3*math.Max(1, math.Abs(float64(b))) }
		if !near(o.Q10, c.q10) || !near(o.Q50, c.q50) || !near(o.Q90, c.q90) || !near(o.Q99, c.q99) || !near(o.VMax, c.vmax) ||
			o.Calls != c.calls || o.Buckets != c.buckets {
			t.Errorf("%s: %+v", c.name, o)
		}
		if !o.WeekStart.Equal(monday) || o.Class != "normal" || o.Source != chstore.MLBaselineSourceSeries || o.CfgHash != "h1" {
			t.Errorf("%s: hafta/sınıf/kaynak/hash %+v", c.name, o)
		}
	}
	if _, ok := findObs(obs, "log", "orders-api", "code|E42", 3); ok {
		t.Error("86400 sn kova saate yazılmamalı")
	}
}

func TestBaselineDayObsExcludesLabelledBuckets(t *testing.T) {
	from := time.Date(2026, 11, 4, 10, 0, 0, 0, istanbul).Unix()
	in := baselineInput{Loc: istanbul, TargetSec: 300, Exclude: map[string][][2]int64{"orders-api": {{from, from + 300}}}}
	o, ok := findObs(BaselineDayObs(dayFixture(), in), "log", "orders-api", "_total", 58)
	if !ok || o.Buckets != 10 || o.VMax != 12 || o.Calls != 75 {
		t.Fatalf("dışlanan iki kova (1, 2) sayılmamalı: %+v", o)
	}
	// saatin tüm kovaları dışlanırsa satır buckets=0 ile yine yazılır (yeniden koşu eski satırı ezer)
	in.Exclude = map[string][][2]int64{"orders-api": {{from, from + 3600}}}
	o, ok = findObs(BaselineDayObs(dayFixture(), in), "log", "orders-api", "_total", 58)
	if !ok || o.Buckets != 0 || o.Q50 != 0 {
		t.Fatalf("tamamı dışlanan saat: %+v ok=%v", o, ok)
	}
}

func TestBaselineDayObsHolidayClass(t *testing.T) {
	cal := chstore.AnomalyCalendarConfig{Timezone: "Europe/Istanbul", FixedHolidaysTR: true}
	rows := []chstore.MLSeriesRow{
		{Signal: "log", Service: "orders-api", Part: "_total", Bucket: time.Date(2026, 10, 28, 12, 0, 0, 0, istanbul), BucketSec: 300, Count: 5},
		{Signal: "log", Service: "orders-api", Part: "_total", Bucket: time.Date(2026, 10, 28, 14, 0, 0, 0, istanbul), BucketSec: 300, Count: 5},
	}
	obs := BaselineDayObs(rows, baselineInput{Loc: istanbul, Cal: cal, TargetSec: 300})
	if len(obs) != 2 || obs[0].Class != "normal" || obs[1].Class != "half_day" {
		t.Fatalf("yarım gün 13:00 kuralı: %+v", obs)
	}
}

// İki lider aynı girdiyi (farklı sırayla) okursa birebir aynı satırlar.
func TestBaselineDayObsDeterministic(t *testing.T) {
	in := baselineInput{Loc: istanbul, TargetSec: 300, CfgHash: "h1"}
	want := BaselineDayObs(dayFixture(), in)
	for seed := int64(1); seed <= 5; seed++ {
		rows := dayFixture()
		rand.New(rand.NewSource(seed)).Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
		if got := BaselineDayObs(rows, in); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: çıktı sıraya bağlı", seed)
		}
	}
}

func TestBaselineCfgHashStable(t *testing.T) {
	a := chstore.DefaultMLConfig()
	b := a
	b.TickSec = 600 // gözlemi etkilemeyen ayar
	if baselineCfgHash(a) != baselineCfgHash(b) {
		t.Error("gözlem hash'i tespit ayarına bağlı olmamalı")
	}
	c := a
	c.Quality.HygieneOff = true
	if baselineCfgHash(a) == baselineCfgHash(c) {
		t.Error("hijyen gözlemi değiştirir → hash değişmeli")
	}
}
