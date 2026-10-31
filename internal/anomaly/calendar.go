package anomaly

// calendar.go — v0.10.1483: mevsimsel baseline'ların TAKVİM ETKİLERİ (saf
// çekirdek). Ayar: chstore.AnomalyCalendarConfig (anomaly_sensitivity blob'u).
//
// Gün sınıfı (dayClass) üç sınıftan beşe genişler — YALNIZ takvim bir gün
// anarsa:
//
//   - holiday        → baseline önceki "holiday" günlerinin aynı slotundan
//     (lookback içinde, ≥ min örnek + ≥ 2 farklı gün); yetmezse PAZAR sınıfı.
//   - half-day       → önceki "half-day" günleri; yetmezse CUMARTESİ sınıfı.
//   - month-end-like → önceki "month-end-like" günleri (açık satırlar +
//     isteğe bağlı otomatik ayın son N iş günü); yetmezse günün kendi sınıfı.
//
// Terfi bastırma (en dar güvenli davranış): holiday ve half-day günlerinde
// metrik dedektörünün request_rate DÜŞÜŞ anomalisi Problem AÇMAZ — olay
// anomaly_events'e "calendar_traffic_drop" olarak yazılır (/anomalies'te
// görünür, bildirim/incident yok). Kapsam dışı ve bilinçli: sıçramalar, hata
// oranı, gecikme, service_silent (tam kesinti tatilde de alarm), zaten açık
// bir Problem'in tazelenmesi/kapanması ve month-end-like günleri (ay sonu
// yoğun gün; düşüş anlamlı). Davranış motorunun düşüş adayları zaten terfi
// etmez (behavior_scan.go behaviorEvent); motorun 28 günlük haftanın-saati
// baseline'ından geçmiş holiday / half-day kovaları çıkarılır
// (dropCalendarBaselineRows) — tatil pazartesisi sıradan pazartesiyi kirletmez.
//
// v0.10.1490 — takvim dilimi (cal.Timezone, boş = UTC): özel gün üyeliği ve ay
// sonu iş günü hesabı YEREL günle; gün sınıfı (dayClass) ve slot (saniye-of-
// day) UTC'de kalır. Önceki özel günlerin SQL yüklemi yerel günün UTC
// anlarıyla kurulur (calendarDayWindows → buildCalendarSeasonalQuery).

import (
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Takvim sınıfları (SQL'e GİRMEZ: özel sınıf tarih listesiyle eşleşir).
const (
	classHoliday  = "holiday"
	classHalfDay  = "half_day"
	classMonthEnd = "month_end"
)

// calendarMinDays — özel sınıfın en az kaç farklı günden örnek taşıması
// gerekir (hafta sonu emsali: yılda birkaç kez gelen gün için 2).
const calendarMinDays = 2

// calendarTrafficDropKind — bastırılan düşüşün anomaly_events türü.
const calendarTrafficDropKind = "calendar_traffic_drop"

// isBusinessDay — SAF: YEREL gün hafta içi ve takvimde holiday değil.
func isBusinessDay(t time.Time, cal chstore.AnomalyCalendarConfig) bool {
	switch t.In(cal.Location()).Weekday() {
	case time.Saturday, time.Sunday:
		return false
	}
	return cal.SpecialKindOn(t) != chstore.CalendarHoliday
}

// isAutoMonthEnd — SAF: t'nin YEREL günü (cal.Timezone) ayın son n iş
// gününden biri mi.
func isAutoMonthEnd(t time.Time, n int, cal chstore.AnomalyCalendarConfig) bool {
	loc := cal.Location()
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	if !isBusinessDay(day, cal) {
		return false
	}
	after := 0
	for d := day.AddDate(0, 0, 1); d.Month() == day.Month(); d = d.AddDate(0, 0, 1) {
		if isBusinessDay(d, cal) {
			after++
		}
	}
	return after < n
}

// calendarDayKind — SAF: t'nin takvim türü ("" = sıradan gün). Açık satır
// otomatik ay sonundan önce gelir.
func calendarDayKind(t time.Time, cal chstore.AnomalyCalendarConfig) string {
	if k := cal.SpecialKindOn(t); k != "" {
		return k
	}
	if cal.MonthEndAuto && isAutoMonthEnd(t, cal.MonthEndBusinessDays, cal) {
		return chstore.CalendarMonthEnd
	}
	if autoRuleOn(t, cal) { // v0.10.1750 — ay sonu benzeri otomatik kurallar (ayrı anahtarlar)
		return chstore.CalendarMonthEnd
	}
	return ""
}

// nextBusinessDayFrom — SAF: d dahil ilk iş günü (en çok 14 gün bakar).
func nextBusinessDayFrom(d time.Time, cal chstore.AnomalyCalendarConfig) time.Time {
	for i := 0; i < 14; i++ {
		if isBusinessDay(d, cal) {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
	return d
}

// autoRuleOn — SAF (tablo testli): ayın 15'i (iş günü değilse sonraki iş günü),
// ayın son iş günü, yılın son iş günü, tatil öncesi son iş günü.
func autoRuleOn(t time.Time, cal chstore.AnomalyCalendarConfig) bool {
	r := cal.AutoRules
	if !r.Mid15 && !r.LastBusinessDay && !r.YearEnd && !r.PreHolidayLastBusinessDay {
		return false
	}
	loc := cal.Location()
	day := t.In(loc)
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	if !isBusinessDay(day, cal) {
		return false
	}
	if r.Mid15 && nextBusinessDayFrom(time.Date(day.Year(), day.Month(), 15, 0, 0, 0, 0, loc), cal).Equal(day) {
		return true
	}
	if r.LastBusinessDay && isAutoMonthEnd(day, 1, cal) {
		return true
	}
	if r.YearEnd && day.Month() == time.December && isAutoMonthEnd(day, 1, cal) {
		return true
	}
	if r.PreHolidayLastBusinessDay {
		// yarından sonraki ilk iş gününe kadar bir tatil (holiday/half-day) varsa
		for d := day.AddDate(0, 0, 1); d.Sub(day) <= 14*24*time.Hour; d = d.AddDate(0, 0, 1) {
			k := cal.SpecialKindOn(d)
			if k == chstore.CalendarHoliday || k == chstore.CalendarHalfDay {
				return true
			}
			if isBusinessDay(d, cal) {
				break
			}
		}
	}
	return false
}

// dayClassCal — SAF: takvimli gün sınıfı. Takvim boşsa dayClass ile aynı.
func dayClassCal(t time.Time, cal chstore.AnomalyCalendarConfig) string {
	switch calendarDayKind(t, cal) {
	case chstore.CalendarHoliday:
		return classHoliday
	case chstore.CalendarHalfDay:
		return classHalfDay
	case chstore.CalendarMonthEnd:
		return classMonthEnd
	}
	return dayClass(t)
}

// calendarFallbackClass — SAF: özel sınıf yetmediğinde SQL sınıfı.
func calendarFallbackClass(class string, t time.Time) string {
	switch class {
	case classHoliday:
		return "sunday"
	case classHalfDay:
		return "saturday"
	}
	return dayClass(t)
}

// calendarSameKindDates — SAF: at'ten ÖNCEKİ (bugün hariç) lookback günü
// içinde at ile aynı takvim türünü taşıyan YEREL günler (cal.Timezone),
// "YYYY-MM-DD", artan.
func calendarSameKindDates(at time.Time, days int, cal chstore.AnomalyCalendarConfig) []string {
	kind := calendarDayKind(at, cal)
	if kind == "" {
		return nil
	}
	loc := cal.Location()
	at = at.In(loc)
	today := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)
	var out []string
	for i := days; i >= 1; i-- {
		d := today.AddDate(0, 0, -i)
		if calendarDayKind(d, cal) == kind {
			out = append(out, d.Format("2006-01-02"))
		}
	}
	return out
}

// calendarWindow — yerel bir özel günün UTC anları [start, end).
type calendarWindow struct{ start, end time.Time }

// calendarDayWindows — SAF (v0.10.1490): yerel tarih listesi → UTC pencereleri
// (buildCalendarSeasonalQuery'nin bağları). Geçersiz tarih atlanır.
func calendarDayWindows(dates []string, cal chstore.AnomalyCalendarConfig) []calendarWindow {
	out := make([]calendarWindow, 0, len(dates))
	for _, d := range dates {
		if s, e, ok := cal.DayWindowUTC(d); ok {
			out = append(out, calendarWindow{s, e})
		}
	}
	return out
}

// inCalendarWindows — SAF: SQL yükleminin Go ikizi (time_bucket >= start AND
// time_bucket < end, herhangi bir pencere). Uyum testi bununla koşar.
func inCalendarWindows(unix int64, ws []calendarWindow) bool {
	for _, w := range ws {
		if unix >= w.start.Unix() && unix < w.end.Unix() {
			return true
		}
	}
	return false
}

// calendarLocalDayKey — SAF: unix anının YEREL gün anahtarı (YYYYMMDD) — özel
// gün örneklerinin gün çeşitliliği yerel günle sayılır (İstanbul'da bir
// tatilin 21:00 UTC sınırındaki slotu iki UTC gününe düşer, tek gündür).
func calendarLocalDayKey(loc *time.Location) func(int64) int64 {
	return func(unix int64) int64 {
		t := time.Unix(unix, 0).In(loc)
		return int64(t.Year())*10000 + int64(t.Month())*100 + int64(t.Day())
	}
}

// pickCalendarSeasonal — SAF: servis başına özel sınıfın örnekleri yeterliyse
// (≥ minSamples; gün çeşitliliği çağıranda budanmış) onlar, değilse yedek
// sınıfın örnekleri. rates aynı seçimi izler (hizalı kalır).
func pickCalendarSeasonal(special, specialRates, fallback, fallbackRates map[string][]float64, minSamples int) (map[string][]float64, map[string][]float64) {
	out := make(map[string][]float64, len(fallback))
	var rates map[string][]float64
	if specialRates != nil || fallbackRates != nil {
		rates = make(map[string][]float64)
	}
	for svc, v := range fallback {
		out[svc] = v
		if rates != nil {
			rates[svc] = fallbackRates[svc]
		}
	}
	for svc, v := range special {
		if len(v) >= minSamples {
			out[svc] = v
			if rates != nil {
				rates[svc] = specialRates[svc]
			}
		}
	}
	return out, rates
}

// calendarSuppressesDropPromotion — SAF: bu gün bu (metrik, yön) kararı
// Problem açmasın mı. Yalnız holiday / half-day + request_rate + düşüş.
func calendarSuppressesDropPromotion(kind, metric, direction string) bool {
	if kind != chstore.CalendarHoliday && kind != chstore.CalendarHalfDay {
		return false
	}
	return metric == "request_rate" && direction == "dropped"
}

// calendarDropEvent — SAF: bastırılan düşüşün anomaly_events satırı. Oran
// < 1 olduğu için terfi kapısını (MinPeakRatio) hiçbir zaman geçmez.
func calendarDropEvent(service, kind string, current, median float64, now time.Time) chstore.AnomalyEvent {
	ratio := 0.0
	if median > 0 {
		ratio = current / median
	}
	return chstore.AnomalyEvent{
		ID:           chstore.FingerprintAnomaly(calendarTrafficDropKind, "request_rate", service),
		Kind:         calendarTrafficDropKind,
		Pattern:      "request_rate · takvim günü düşüşü (" + strings.ReplaceAll(kind, "-", " ") + ")",
		Service:      service,
		StartedAt:    now.UnixNano(),
		LastSeen:     now.UnixNano(),
		CurrentRatio: ratio,
		Sample:       "Takvim günü (" + kind + "): request_rate düşüşü kaydedildi, Problem açılmadı ve bildirim gitmedi.",
	}
}

// dropCalendarBaselineRows — SAF (v0.10.1483): davranış motorunun BASELINE
// satırlarından (Unix < recentCutoff) holiday / half-day günlerine düşenleri
// çıkarır. Takvim boşsa girdi aynen döner (kopya yok).
func dropCalendarBaselineRows(in map[string][]behaviorRow, cal chstore.AnomalyCalendarConfig, recentCutoff int64) map[string][]behaviorRow {
	if len(cal.Days) == 0 {
		return in
	}
	// v0.10.1490 — önbellek 15 dk diliminde: her IANA ofseti 15 dk'nın katı,
	// yerel gün sınırı bir dilimin içine düşmez (UTC gün anahtarı yerel
	// sınırı kaçırırdı).
	special := map[int64]bool{}
	isSpecial := func(unix int64) bool {
		slot := unix / 900
		v, ok := special[slot]
		if !ok {
			k := cal.SpecialKindOn(time.Unix(slot*900, 0))
			v = k == chstore.CalendarHoliday || k == chstore.CalendarHalfDay
			special[slot] = v
		}
		return v
	}
	out := make(map[string][]behaviorRow, len(in))
	for svc, rows := range in {
		kept := make([]behaviorRow, 0, len(rows))
		for _, r := range rows {
			if r.Unix < recentCutoff && isSpecial(r.Unix) {
				continue
			}
			kept = append(kept, r)
		}
		out[svc] = kept
	}
	return out
}
