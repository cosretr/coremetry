package chstore

// seasonal_timezone.go — v0.10.2066 (Davis S6, docs/anomaly/davis-ml-gap.md
// satır 12): mevsimsel gün sınıfının saat dilimi TEK yerde. Ayar
// anomaly_sensitivity.seasonalTimezone (v0.10.1790, karar 3, operatör
// 2026-10-29: varsayılan Europe/Istanbul). Önce yalnız Davis özneleri ve
// servis yolu (internal/anomaly) okuyordu; ML (internal/ml) calendar.timezone'u
// okuyup boşken UTC kalıyordu. Artık iki yol da SeasonalLocation'dan geçer —
// özne kodunun silinmesinin ön şartı (sembol özne dosyasından buraya taşındı).

import (
	"fmt"
	"strings"
	"time"
)

// DefaultSeasonalTimezone — karar 3: gün sınıfı operatör diliminde.
const DefaultSeasonalTimezone = "Europe/Istanbul"

// ValidateSeasonalTimezone — SAF: PUT kapısı. ValidateCalendarTimezone'un
// kuralı (boş geçerli = varsayılan, "Local" red, aksi IANA) yalnız hata
// metninin alan adı farklı. Kelepçe değil RED: bilinmeyen ad sessizce
// varsayılana dönseydi taban operatörün haberi olmadan kayardı.
func ValidateSeasonalTimezone(name string) error {
	if err := ValidateCalendarTimezone(name); err != nil {
		return fmt.Errorf("seasonalTimezone%s", strings.TrimPrefix(err.Error(), "calendar.timezone"))
	}
	return nil
}

// normalizeSeasonalTimezone — SAF: boş ya da geçersiz → varsayılan; aksi
// kırpılmış ad. (Geçersiz ad PUT'ta reddedilir; burası elle düzenlenmiş
// blob için.)
func normalizeSeasonalTimezone(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || ValidateCalendarTimezone(name) != nil {
		return DefaultSeasonalTimezone
	}
	return name
}

// SeasonalLocation — SAF: ayar adı → konum (Davis + ML ortak; önce
// anomaly.seasonalLocation). Boş → varsayılan (Europe/Istanbul); "UTC" →
// time.UTC; yüklenemeyen ad → UTC (ayar PUT'ta doğrulandığı için yalnız elle
// düzenlenmiş blobda; güvenli yön eski davranış).
func SeasonalLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultSeasonalTimezone
	}
	if strings.EqualFold(name, "UTC") {
		return time.UTC
	}
	if ValidateSeasonalTimezone(name) != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}
