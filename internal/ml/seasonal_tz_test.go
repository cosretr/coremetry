package ml

// v0.10.2066 — Davis S6 (docs/anomaly/davis-ml-gap.md satır 12): ML'in gün
// sınıfı / saat penceresi dilimi Davis ile AYNI ayardan
// (anomaly_sensitivity.seasonalTimezone, varsayılan Europe/Istanbul).
// Önce ML calendar.timezone'u okuyordu; boşsa UTC kalıyordu (gece 21-24
// UTC kovaları yanlış yerel güne düşüyordu).

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLUsesSeasonalTimezone(t *testing.T) {
	for _, c := range []struct {
		name     string
		seasonal string
		calTZ    string
		want     string
	}{
		{"ayar yok → Davis varsayılanı", "", "", "Europe/Istanbul"},
		{"takvim dilimi boş, mevsimsel İstanbul", "Europe/Istanbul", "", "Europe/Istanbul"},
		{"mevsimsel UTC açıkça", "UTC", "Europe/Istanbul", "UTC"},
		{"mevsimsel başka dilim", "Europe/Berlin", "", "Europe/Berlin"},
		{"bozuk ad → UTC (Davis ile aynı güvenli yön)", "Mars/Olympus", "", "UTC"},
	} {
		sens := chstore.AnomalySensitivityConfig{SeasonalTimezone: c.seasonal}
		sens.Calendar.Timezone = c.calTZ
		if _, loc := calendarFor(sens); loc.String() != c.want {
			t.Errorf("%s: %s, beklenen %s", c.name, loc, c.want)
		}
		if got := OperatorLocation(sens).String(); got != c.want {
			t.Errorf("%s: OperatorLocation %s", c.name, got)
		}
		if got := chstore.SeasonalLocation(c.seasonal).String(); got != c.want {
			t.Errorf("%s: ortak çözüm %s", c.name, got)
		}
	}
	if chstore.DefaultSeasonalTimezone != "Europe/Istanbul" {
		t.Fatal("varsayılan değişmemeli")
	}
}
