package anomaly

// v0.10.1490 — takvim dilimi: özel gün üyeliği ve ay sonu iş günü YEREL günle;
// gün sınıfı / slot UTC'de; SQL penceresi Go'nun UTC anları. Tarihler sentetik.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func tzCal(tz string) chstore.AnomalyCalendarConfig {
	c := testCal()
	c.Timezone = tz
	return chstore.NormalizeAnomalyCalendar(c)
}

func utcAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// TestDayClassCalTimezoneBoundary — UTC+3 sınırı: yerel 00:00–03:00 (UTC
// 21:00–24:00 önceki gün) tatile aittir; yedek sınıf (dayClass) UTC kalır.
func TestDayClassCalTimezoneBoundary(t *testing.T) {
	ist, utc := tzCal("Europe/Istanbul"), tzCal("")
	cases := []struct {
		name string
		cal  chstore.AnomalyCalendarConfig
		at   string
		want string
	}{
		{"İST yerel 00:00 yılbaşı", ist, "2030-12-31T21:00:00Z", classHoliday},
		{"İST yerel 01:30 yılbaşı", ist, "2030-12-31T22:30:00Z", classHoliday},
		{"İST yerel 02:55 yılbaşı", ist, "2030-12-31T23:55:00Z", classHoliday},
		{"İST yerel 23:55 arife değil (sıradan salı)", ist, "2030-12-31T20:55:00Z", "weekday"},
		{"İST yerel ertesi 00:05 değil", ist, "2031-01-01T21:05:00Z", "weekday"},
		{"İST bayram son yerel saati", ist, "2031-03-14T20:55:00Z", classHoliday},
		{"İST bayram bitti: yerel cumartesi 00:05, UTC cuma", ist, "2031-03-14T21:05:00Z", "weekday"},
		{"İST arife yerel 00:30 → yarım gün", ist, "2031-03-10T21:30:00Z", classHalfDay},
		{"UTC (boş) eski davranış: UTC 21:00 önceki gün", utc, "2030-12-31T21:00:00Z", "weekday"},
		{"UTC (boş) eski davranış: UTC 00:00", utc, "2031-01-01T00:00:00Z", classHoliday},
	}
	for _, c := range cases {
		if got := dayClassCal(utcAt(c.at), c.cal); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	// Yedek sınıf UTC'ye pinli: yerel cumartesi 00:05 (UTC cuma 21:05) ay sonu
	// satırı olsa da "weekday" yedeğine iner (SQL'in multiIf'i UTC görür).
	if got := calendarFallbackClass(classMonthEnd, utcAt("2031-03-14T21:05:00Z")); got != "weekday" {
		t.Errorf("yedek sınıf UTC: %q", got)
	}
}

// TestCalendarTimezoneDST — Europe/Berlin: yaz saatine geçiş günü (23 saat)
// tatilse yerel 00:00 = UTC 23:00 önceki gün, yerel 23:59 = UTC 21:59.
func TestCalendarTimezoneDST(t *testing.T) {
	cal := chstore.NormalizeAnomalyCalendar(chstore.AnomalyCalendarConfig{Timezone: "Europe/Berlin", Days: []chstore.AnomalySpecialDay{
		{From: "2031-03-30", Kind: chstore.CalendarHoliday}, // pazar, DST başlar
		{From: "2031-10-26", Kind: chstore.CalendarHoliday}, // pazar, DST biter
		{From: "2031-07-01", Kind: chstore.CalendarHoliday}, // yaz (UTC+2)
	}})
	for at, want := range map[string]string{
		"2031-03-29T22:59:00Z": "",                      // yerel 23:59 CET önceki gün
		"2031-03-29T23:00:00Z": chstore.CalendarHoliday, // yerel 00:00 CET
		"2031-03-30T21:59:00Z": chstore.CalendarHoliday, // yerel 23:59 CEST
		"2031-03-30T22:00:00Z": "",                      // yerel ertesi 00:00 CEST
		"2031-10-25T22:00:00Z": chstore.CalendarHoliday, // yerel 00:00 CEST
		"2031-10-26T22:59:00Z": chstore.CalendarHoliday, // yerel 23:59 CET
		"2031-10-26T23:00:00Z": "",
		"2031-06-30T22:00:00Z": chstore.CalendarHoliday,
		"2031-06-30T21:55:00Z": "",
	} {
		if got := calendarDayKind(utcAt(at), cal); got != want {
			t.Errorf("Berlin %s: %q, beklenen %q", at, got, want)
		}
	}
	ws := calendarDayWindows([]string{"2031-03-30", "2031-10-26"}, cal)
	if len(ws) != 2 || ws[0].end.Sub(ws[0].start) != 23*time.Hour || ws[1].end.Sub(ws[1].start) != 25*time.Hour {
		t.Errorf("DST pencereleri: %+v", ws)
	}
}

// TestAutoMonthEndLocal — ay sonu iş günü YEREL günle: İstanbul'da yerel
// cuma 31 Ocak 01:00 (UTC perşembe 30 Ocak 22:00) ayın son iş günüdür; ayın
// ilk yerel günü (UTC'de hâlâ önceki ayın son günü) ay sonu değildir.
func TestAutoMonthEndLocal(t *testing.T) {
	ist := chstore.AnomalyCalendarConfig{Timezone: "Europe/Istanbul", MonthEndAuto: true, MonthEndBusinessDays: 1}
	utc := ist
	utc.Timezone = ""
	cases := []struct {
		name string
		cal  chstore.AnomalyCalendarConfig
		at   string
		want string
	}{
		{"İST yerel cuma 31 01:00 son iş günü", ist, "2031-01-30T22:00:00Z", chstore.CalendarMonthEnd},
		{"UTC aynı an: perşembe 30, son iş günü değil", utc, "2031-01-30T22:00:00Z", ""},
		{"İST yerel cumartesi 1 Şubat 00:30 (UTC cuma 31)", ist, "2031-01-31T21:30:00Z", ""},
		{"UTC aynı an: cuma 31 son iş günü", utc, "2031-01-31T21:30:00Z", chstore.CalendarMonthEnd},
		// Haziran 2031: 30'u pazartesi. İST'de yerel pzt 30 00:30 = UTC pazar 29 21:30.
		{"İST yerel pazartesi 30 Haziran 00:30", ist, "2031-06-29T21:30:00Z", chstore.CalendarMonthEnd},
		{"UTC aynı an pazar: iş günü değil", utc, "2031-06-29T21:30:00Z", ""},
	}
	for _, c := range cases {
		if got := calendarDayKind(utcAt(c.at), c.cal); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
	// Yerel tatil ay sonunu kaydırır: yerel cuma 31 tatil → perşembe 30 son iş günü.
	hol := ist
	hol.Days = []chstore.AnomalySpecialDay{{From: "2031-01-31", Kind: chstore.CalendarHoliday}}
	if got := calendarDayKind(utcAt("2031-01-29T21:30:00Z"), hol); got != chstore.CalendarMonthEnd {
		t.Errorf("yerel perşembe 30 ay sonu olmalı: %q", got)
	}
}

// TestCalendarWindowAgreesWithSpecialKindOn — SQL/Go uyumu: SQL yüklemi
// (time_bucket >= start AND time_bucket < end) Go'nun inCalendarWindows ikiziyle
// birebir; o da her 5 dk kovada SpecialKindOn'la aynı günü seçer. Aralık:
// pencerelerin bir gün öncesinden bir gün sonrasına, üç dilimde.
func TestCalendarWindowAgreesWithSpecialKindOn(t *testing.T) {
	for _, tz := range []string{"", "Europe/Istanbul", "Europe/Berlin", "Asia/Kathmandu", "America/New_York"} {
		cal := chstore.NormalizeAnomalyCalendar(chstore.AnomalyCalendarConfig{Timezone: tz, Days: []chstore.AnomalySpecialDay{
			{From: "2031-01-01", Kind: chstore.CalendarHoliday},
			{From: "2031-03-08", To: "2031-03-10", Kind: chstore.CalendarHoliday}, // ABD DST 9 Mart
			{From: "2031-03-30", Kind: chstore.CalendarHoliday},                   // AB DST
		}})
		// "Şimdi" de bir tatil (5 Nisan): önceki tatiller listesi ondan kurulur.
		at := utcAt("2031-04-05T12:00:00Z")
		cal.Days = append(cal.Days, chstore.AnomalySpecialDay{From: "2031-04-05", Kind: chstore.CalendarHoliday})
		cal = chstore.NormalizeAnomalyCalendar(cal)
		dates := calendarSameKindDates(at, 120, cal)
		if strings.Join(dates, ",") != "2031-01-01,2031-03-08,2031-03-09,2031-03-10,2031-03-30" {
			t.Fatalf("%s: tarihler %v", tz, dates)
		}
		ws := calendarDayWindows(dates, cal)
		q := buildCalendarSeasonalQuery("X", len(ws))
		args := calendarSeasonalArgs(ws, at, 43200, 900)
		if strings.Count(q, "?") != len(args) {
			t.Fatalf("%s: %d yer tutucu, %d bağ", tz, strings.Count(q, "?"), len(args))
		}
		for i, w := range ws {
			if args[2+2*i] != w.start || args[3+2*i] != w.end || w.start.Location() != time.UTC {
				t.Fatalf("%s: bağ sırası %d", tz, i)
			}
		}
		if args[0] != ws[0].start {
			t.Fatalf("%s: alt sınır ilk pencere", tz)
		}
		for b := utcAt("2030-12-30T00:00:00Z").Unix(); b < utcAt("2031-04-01T00:00:00Z").Unix(); b += 300 {
			goSide := cal.SpecialKindOn(time.Unix(b, 0)) == chstore.CalendarHoliday
			if inCalendarWindows(b, ws) != goSide {
				t.Fatalf("%s: kova %s Go=%v SQL=%v", tz, time.Unix(b, 0).UTC().Format(time.RFC3339), goSide, !goSide)
			}
		}
	}
}

// TestCalendarLocalDayKey — gün çeşitliliği yerel günle: İstanbul'da tatilin
// UTC 21:00 sınırındaki iki slotu (önceki UTC günü 21:05 ve tatil UTC günü
// 20:55) TEK yerel gündür.
func TestCalendarLocalDayKey(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Istanbul")
	k := calendarLocalDayKey(loc)
	a, b := k(utcAt("2030-12-31T21:05:00Z").Unix()), k(utcAt("2031-01-01T20:55:00Z").Unix())
	if a != b || a != 20310101 {
		t.Errorf("yerel gün anahtarı: %d %d", a, b)
	}
	if u := calendarLocalDayKey(time.UTC)(utcAt("2030-12-31T21:05:00Z").Unix()); u != 20301231 {
		t.Errorf("UTC: %d", u)
	}
}

// TestDropCalendarBaselineRowsTimezone — davranış baseline'ı yerel tatil
// kovalarını düşürür: İstanbul'da UTC 21:00 önceki gün kovası tatildir.
func TestDropCalendarBaselineRowsTimezone(t *testing.T) {
	ist := tzCal("Europe/Istanbul")
	early := utcAt("2030-12-31T21:00:00Z").Unix()   // yerel yılbaşı 00:00
	before := utcAt("2030-12-31T20:00:00Z").Unix()  // yerel 23:00 önceki gün
	lateUTC := utcAt("2031-01-01T22:00:00Z").Unix() // yerel 2 Ocak 01:00 (UTC'de hâlâ yılbaşı)
	in := map[string][]behaviorRow{"orders-api": {{Unix: before}, {Unix: early}, {Unix: lateUTC}}}
	got := dropCalendarBaselineRows(in, ist, utcAt("2031-03-01T00:00:00Z").Unix())["orders-api"]
	if len(got) != 2 || got[0].Unix != before || got[1].Unix != lateUTC {
		t.Errorf("İstanbul: %+v", got)
	}
	got = dropCalendarBaselineRows(in, tzCal(""), utcAt("2031-03-01T00:00:00Z").Unix())["orders-api"]
	if len(got) != 2 || got[0].Unix != before || got[1].Unix != early {
		t.Errorf("UTC eski davranış: %+v", got)
	}
}

// TestVMSeasonalBucketsCalendarTimezone — VM yolu yerel tatilde yedek sınıfa
// (pazar) iner: İstanbul'da UTC 22:00 (31 Aralık salı) yerel yılbaşıdır.
func TestVMSeasonalBucketsCalendarTimezone(t *testing.T) {
	at := utcAt("2030-12-31T22:02:00Z")
	if _, cls := vmSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, tzCal("Europe/Istanbul"), time.UTC); cls != "sunday" {
		t.Errorf("İstanbul yerel tatil: %s", cls)
	}
	if _, cls := vmSeasonalBucketsCal(at, seasonalDays, seasonalNeighborBuckets, tzCal(""), time.UTC); cls != "weekday" {
		t.Errorf("UTC eski davranış: %s", cls)
	}
}
