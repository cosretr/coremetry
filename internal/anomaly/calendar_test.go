package anomaly

// v0.10.1483 — takvim etkileri: gün sınıfı, baseline seçimi, terfi bastırma,
// davranış baseline'ı. Tarihler sentetik.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func day(s string) time.Time {
	t, _ := time.Parse("2006-01-02 15:04", s)
	return t.UTC()
}

func testCal() chstore.AnomalyCalendarConfig {
	return chstore.NormalizeAnomalyCalendar(chstore.AnomalyCalendarConfig{Days: []chstore.AnomalySpecialDay{
		{From: "2031-01-01", Label: "Yılbaşı", Kind: chstore.CalendarHoliday},
		{From: "2031-03-12", To: "2031-03-14", Label: "Bayram", Kind: chstore.CalendarHoliday},
		{From: "2031-03-11", Label: "Arife", Kind: chstore.CalendarHalfDay},
		{From: "2031-02-27", Label: "Kapanış", Kind: chstore.CalendarMonthEnd},
	}})
}

func TestDayClassCal(t *testing.T) {
	cal := testCal()
	auto := cal
	auto.MonthEndAuto, auto.MonthEndBusinessDays = true, 2
	cases := []struct {
		name string
		at   time.Time
		cal  chstore.AnomalyCalendarConfig
		want string
	}{
		{"takvim yok: hafta içi", day("2031-01-01 10:00"), chstore.AnomalyCalendarConfig{}, "weekday"},
		{"tatil (çarşamba)", day("2031-01-01 10:00"), cal, classHoliday},
		{"aralığın ortası", day("2031-03-13 23:55"), cal, classHoliday},
		{"aralık dışı ertesi gün", day("2031-03-15 00:05"), cal, "saturday"},
		{"yarım gün", day("2031-03-11 09:00"), cal, classHalfDay},
		{"açık ay sonu satırı", day("2031-02-27 12:00"), cal, classMonthEnd},
		{"otomatik ay sonu kapalı", day("2031-01-31 12:00"), cal, "weekday"},
		{"otomatik: ayın son iş günü (cuma 31)", day("2031-01-31 12:00"), auto, classMonthEnd},
		{"otomatik: sondan ikinci iş günü", day("2031-01-30 12:00"), auto, classMonthEnd},
		{"otomatik: sondan üçüncü değil", day("2031-01-29 12:00"), auto, "weekday"},
		{"otomatik: hafta sonu ay sonu sayılmaz", day("2031-05-31 12:00"), auto, "saturday"},
		{"pazar", day("2031-01-05 12:00"), cal, "sunday"},
	}
	for _, c := range cases {
		if got := dayClassCal(c.at, c.cal); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	// Tatil iş günü değildir: ayın son günü tatilse otomatik ay sonu bir önceki iş gününe kayar.
	hol := auto
	hol.Days = append(hol.Days, chstore.AnomalySpecialDay{From: "2031-01-31", Kind: chstore.CalendarHoliday})
	hol = chstore.NormalizeAnomalyCalendar(hol)
	hol.MonthEndAuto = true
	if got := dayClassCal(day("2031-01-29 12:00"), hol); got != classMonthEnd {
		t.Errorf("tatil ay sonunu kaydırmalı: %q", got)
	}
}

func TestCalendarBaselineSelection(t *testing.T) {
	cal := testCal()
	if got := calendarFallbackClass(classHoliday, day("2031-01-01 10:00")); got != "sunday" {
		t.Errorf("tatil yedeği pazar: %q", got)
	}
	if got := calendarFallbackClass(classHalfDay, day("2031-03-11 10:00")); got != "saturday" {
		t.Errorf("yarım gün yedeği cumartesi: %q", got)
	}
	if got := calendarFallbackClass(classMonthEnd, day("2031-02-27 10:00")); got != "weekday" {
		t.Errorf("ay sonu yedeği günün sınıfı: %q", got)
	}
	// Önceki tatil günleri: bugün hariç, artan.
	got := calendarSameKindDates(day("2031-03-14 10:00"), 90, cal)
	if strings.Join(got, ",") != "2031-01-01,2031-03-12,2031-03-13" {
		t.Errorf("önceki tatiller: %v", got)
	}
	if got := calendarSameKindDates(day("2031-03-14 10:00"), 30, cal); strings.Join(got, ",") != "2031-03-12,2031-03-13" {
		t.Errorf("lookback sınırı: %v", got)
	}
	if got := calendarSameKindDates(day("2031-03-16 10:00"), 90, cal); got != nil {
		t.Errorf("sıradan gün: %v", got)
	}
	// Servis başına seçim: özel sınıf ≥ min örnek → o; değilse yedek.
	special := map[string][]float64{"orders-api": {1, 2, 3, 4}, "billing-api": {9}}
	fallback := map[string][]float64{"orders-api": {50, 51}, "billing-api": {60, 61, 62, 63}, "kxzone": {7, 7, 7, 7}}
	out, rates := pickCalendarSeasonal(special, nil, fallback, nil, 4)
	if len(out["orders-api"]) != 4 || out["orders-api"][0] != 1 || out["billing-api"][0] != 60 || len(out["kxzone"]) != 4 || rates != nil {
		t.Errorf("seçim: %v rates=%v", out, rates)
	}
	_, rates = pickCalendarSeasonal(special, map[string][]float64{"orders-api": {10, 10, 10, 10}}, fallback, map[string][]float64{"billing-api": {5}}, 4)
	if rates["orders-api"][0] != 10 || rates["billing-api"][0] != 5 {
		t.Errorf("hacim serisi aynı seçimi izlemeli: %v", rates)
	}
}

func TestBuildCalendarSeasonalQuery(t *testing.T) {
	q := buildCalendarSeasonalQuery("X", 3)
	// v0.10.1490 — yerel günün Go'da hesaplanmış UTC anları; CH'de tarih /
	// dilim dönüşümü yok (oturum TZ'sine güvenilmez).
	const pred = "((time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?) OR (time_bucket >= ? AND time_bucket < ?))"
	if !strings.Contains(q, pred) || strings.Contains(q, "multiIf(") || strings.Contains(q, "toDate(") {
		t.Fatalf("pencere yüklemi:\n%s", q)
	}
	if n := strings.Count(q, "?"); n != 5+2*3 {
		t.Errorf("yer tutucu %d", n)
	}
	if legacy := strings.Replace(q, pred,
		"multiIf(toDayOfWeek(time_bucket, 0, 'UTC') = 6, 'saturday', toDayOfWeek(time_bucket, 0, 'UTC') = 7, 'sunday', 'weekday') = ?", 1); legacy != buildAllSeasonalQuery("X") {
		t.Fatal("takvim sorgusu yalnız sınıf yüklemini değiştirmeli")
	}
	for _, w := range []string{"time_bucket >= ?", "time_bucket < ?", "LIMIT 700 BY service_name", "max_execution_time"} {
		if !strings.Contains(q, w) {
			t.Errorf("%q yok", w)
		}
	}
}

func TestCalendarSuppressesDropPromotion(t *testing.T) {
	cases := []struct {
		kind, metric, dir string
		want              bool
	}{
		{chstore.CalendarHoliday, "request_rate", "dropped", true},
		{chstore.CalendarHalfDay, "request_rate", "dropped", true},
		{chstore.CalendarMonthEnd, "request_rate", "dropped", false},
		{"", "request_rate", "dropped", false},
		{chstore.CalendarHoliday, "request_rate", "spiked", false},
		{chstore.CalendarHoliday, "error_rate", "spiked", false},
		{chstore.CalendarHoliday, "p99_ms", "spiked", false},
	}
	for _, c := range cases {
		if got := calendarSuppressesDropPromotion(c.kind, c.metric, c.dir); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
	ev := calendarDropEvent("orders-api", chstore.CalendarHoliday, 2, 10, day("2031-01-01 10:00"))
	if ev.Kind != calendarTrafficDropKind || ev.CurrentRatio != 0.2 || ev.Service != "orders-api" || ev.ID == "" || !strings.Contains(ev.Sample, "Problem açılmadı") {
		t.Errorf("olay: %+v", ev)
	}
}

// TestApplyOutcomeCalendarWiring — bastırma yalnız YENİ açılışta (açık Problem
// tazelenir), olay yazılır, Problem/bildirim dalına inilmez.
func TestApplyOutcomeCalendarWiring(t *testing.T) {
	src := detectorSrc(t)
	i := strings.Index(src, "func (d *Detector) applyOutcome(")
	body := src[i : i+strings.Index(src[i:], "\n}\n")]
	sup := strings.Index(body, "calendarSuppressesDropPromotion(kind, metric, oc.Direction)")
	ins := strings.Index(body, "if err := d.store.UpsertProblem(ctx, p); err != nil")
	if sup < 0 || ins < 0 || sup > ins || !strings.Contains(body[:ins], "if !hasOpen {") || !strings.Contains(body[sup:ins], "UpsertAnomalyEvent(ctx, calendarDropEvent(") {
		t.Fatal("takvim bastırması yeni Problem'den ÖNCE ve yalnız açık Problem yokken olmalı")
	}
}

func TestDropCalendarBaselineRows(t *testing.T) {
	cal := testCal()
	hol := day("2031-01-01 10:00").Unix()
	normal := day("2031-01-08 10:00").Unix()
	recent := day("2031-03-12 08:00").Unix() // bugün tatil, şimdi penceresi
	in := map[string][]behaviorRow{"orders-api": {{Unix: hol}, {Unix: normal}, {Unix: recent}}}
	out := dropCalendarBaselineRows(in, cal, day("2031-03-12 07:00").Unix())
	if got := out["orders-api"]; len(got) != 2 || got[0].Unix != normal || got[1].Unix != recent {
		t.Errorf("baseline tatil kovası düşmeli, şimdi penceresi kalmalı: %+v", got)
	}
	if out := dropCalendarBaselineRows(in, chstore.AnomalyCalendarConfig{}, 0); len(out["orders-api"]) != 3 {
		t.Error("takvim boşken dokunulmamalı")
	}
}

// TestVMSeasonalBucketsCalendar — v0.10.1483: VM yolu özel günde yedek sınıfı
// kullanır; takvim boşken küme vmSeasonalBuckets ile aynı.
func TestVMSeasonalBucketsCalendar(t *testing.T) {
	at := day("2031-01-01 10:02") // çarşamba, takvimde tatil
	plain := vmSeasonalBuckets(at, seasonalDays, seasonalNeighborBuckets)
	same, cls := vmSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, chstore.AnomalyCalendarConfig{}, time.UTC)
	if cls != "weekday" || len(same) != len(plain) || len(plain) == 0 {
		t.Fatalf("takvimsiz: %s %d/%d", cls, len(same), len(plain))
	}
	hol, cls := vmSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, testCal(), time.UTC)
	if cls != "sunday" || len(hol) == 0 {
		t.Fatalf("tatil: sınıf %s, %d kova", cls, len(hol))
	}
	for _, b := range hol {
		if time.Unix(b, 0).UTC().Weekday() != time.Sunday {
			t.Fatalf("tatilde yalnız pazar kovaları: %v", time.Unix(b, 0).UTC())
		}
	}
}
