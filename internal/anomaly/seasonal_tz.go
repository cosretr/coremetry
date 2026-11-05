package anomaly

// seasonal_tz.go — v0.10.1791 (Davis paritesi F1.3, karar 3): mevsimsel
// tabanın gün sınıfı (hafta içi / cumartesi / pazar) ve slot saati operatör
// diliminde (anomaly_sensitivity.seasonalTimezone, varsayılan Europe/Istanbul).
//
// Kural (v0.8.323'ten): Go tarafının slot/sınıf hesabı ile SQL'in
// toHour/toDayOfWeek'i AYNI saati görmeli. Bu dosya iki tarafın da tek
// kaynağı: Go *time.Location, SQL o konumun adı (seasonalTZLiteral).
//
// Yaz saati olmayan dilimde (TR) saat-of-day eşlemesi sabit bir dönmedir ve
// dairesel uzaklık değişmez; değişen yalnız gün sınıfı sınırıdır (UTC 21-24
// kovaları ertesi yerel güne geçer). Davranış motorunun haftanın-saati
// kovası (behavior.go hourOfWeek) UTC'de kalır: sabit ofsette dairesel
// haftanın-saati uzaklığı da değişmez.

import (
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// seasonalUTC — eski (≤ v0.10.1790) dilim; SQL metni bununla bayt bayt aynı.
const seasonalUTC = "UTC"

// seasonalLocation — SAF: ayar adı → konum. Boş → varsayılan (Europe/Istanbul,
// chstore.DefaultSeasonalTimezone); yüklenemeyen ad → UTC (ayar PUT'ta
// doğrulandığı için yalnız elle düzenlenmiş blobda; güvenli yön eski davranış).
func seasonalLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		name = chstore.DefaultSeasonalTimezone
	}
	if strings.EqualFold(name, seasonalUTC) {
		return time.UTC
	}
	if chstore.ValidateSeasonalTimezone(name) != nil {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// seasonalLocOrUTC — nil → UTC.
func seasonalLocOrUTC(loc *time.Location) *time.Location {
	if loc == nil {
		return time.UTC
	}
	return loc
}

// seasonalTZLiteral — SAF: SQL'e giden dilim adı. time.UTC'nin adı "UTC";
// tırnak ve ters bölü süzülür (IANA adlarında yok — savunma).
func seasonalTZLiteral(loc *time.Location) string {
	n := seasonalLocOrUTC(loc).String()
	return strings.NewReplacer("'", "", `\`, "").Replace(n)
}

// seasonalDayKey — SAF: gün çeşitliliğinin gün anahtarı. UTC → nil (çağıran
// t/86400 kullanır — eski davranış bayt bayt); başka dilim → yerel gün.
func seasonalDayKey(loc *time.Location) func(int64) int64 {
	if seasonalLocOrUTC(loc) == time.UTC {
		return nil
	}
	return calendarLocalDayKey(loc)
}

// seasonalDay — SAF: kovanın gün anahtarı (key nil → UTC günü).
func seasonalDay(b int64, key func(int64) int64) int64 {
	if key == nil {
		return b / 86400
	}
	return key(b)
}
