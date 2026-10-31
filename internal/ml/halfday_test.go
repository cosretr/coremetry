package ml

// v0.10.1762 — yarım gün saat desteği (operatör: 13:00 sonrası tatil sınıfı):
// 28 Ekim sabahı sıradan iş günü (düşüş/susma aday), 13:00'ten sonra yarım gün.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLDayClassHalfDayCutoff(t *testing.T) {
	cal := chstore.AnomalyCalendarConfig{FixedHolidaysTR: true}
	day := time.Date(2027, 10, 28, 0, 0, 0, 0, time.UTC) // Perşembe
	if c := mlDayClass(day.Add(10*time.Hour), cal); c != "weekday" || isOffDay(c) {
		t.Fatalf("sabah: %s", c)
	}
	if c := mlDayClass(day.Add(13*time.Hour), cal); c != "half_day" || !isOffDay(c) {
		t.Fatalf("13:00: %s", c)
	}
	if c := mlDayClass(time.Date(2027, 10, 29, 9, 0, 0, 0, time.UTC), cal); c != "holiday" {
		t.Fatalf("29 Eki tatil: %s", c)
	}
}
