package anomaly

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1750 — Türkiye sabit tatilleri (her yıl) + ay sonu benzeri otomatik kurallar.
func TestTurkeyCalendarRules(t *testing.T) {
	cal := chstore.AnomalyCalendarConfig{Timezone: "Europe/Istanbul", FixedHolidaysTR: true,
		AutoRules: chstore.CalendarAutoRules{Mid15: true, LastBusinessDay: true, YearEnd: true, PreHolidayLastBusinessDay: true}}
	loc := cal.Location()
	at := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 10, 0, 0, 0, loc) }
	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"29 Ekim 2027 tatil", at(2027, 10, 29), classHoliday},
		{"28 Ekim yarım", at(2026, 10, 28), classHalfDay},
		{"23 Nisan 2030 tatil (her yıl)", at(2030, 4, 23), classHoliday},
		{"15 Eki 2026 Perşembe ayın 15'i", at(2026, 10, 15), classMonthEnd},
		{"15 Kas 2026 Pazar → 16 Kas Pazartesi", at(2026, 11, 16), classMonthEnd},
		{"30 Eki 2026 Cuma son iş günü", at(2026, 10, 30), classMonthEnd},
		{"31 Ara 2026 Perşembe yıl sonu", at(2026, 12, 31), classMonthEnd},
		{"22 Nis 2026 Çarşamba tatil öncesi", at(2026, 4, 22), classMonthEnd},
		{"sıradan 7 Eki 2026", at(2026, 10, 7), "weekday"},
	}
	for _, c := range cases {
		if got := CalendarDayClass(c.t, cal); got != c.want {
			t.Errorf("%s: got %s want %s", c.name, got, c.want)
		}
	}
	off := chstore.AnomalyCalendarConfig{Timezone: "Europe/Istanbul"}
	if CalendarDayClass(at(2027, 10, 29), off) != "weekday" {
		t.Fatal("bayrak kapalıyken sabit tatil uygulanmamalı")
	}
}
