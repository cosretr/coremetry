package chstore

// anomaly_calendar.go — v0.10.1483: mevsimsel baseline'ların TAKVİM ETKİLERİ.
//
// Mevsimsel baseline gün sınıfıyla eşleşir (hafta içi / cumartesi / pazar);
// resmi tatil hafta içine düşünce o günün trafiği bir pazarınkine benzer ama
// baseline sıradan bir hafta içi günüdür → yapay "trafik düşüşü" anomalisi.
// Operatör özel günleri (tarih ya da tarih aralığı, etiket, tür) anomaly
// ayar blob'unun (system_settings "anomaly_sensitivity") Calendar bölümüne
// yazar; dedektör (internal/anomaly/calendar.go) o gün sınıfı değiştirir.
//
// Tarihler takvim günüdür (YYYY-MM-DD) ve v0.10.1490'dan beri Timezone'un
// (IANA adı; boş = UTC, eski blob'lar) YEREL günü olarak okunur: İstanbul'da
// tatilin ilk 3 yerel saati (UTC 21:00–24:00, önceki UTC günü) artık tatile
// aittir. Dilim YALNIZ "hangi kova hangi özel güne ait" sorusunu (ve ay sonu
// iş günü hesabını) yanıtlar; mevsimsel baseline'ın saat dilimi / gün sınıfı
// UTC'ye pinli kalır (v0.8.323). SQL'e yerel gün gitmez: Go yerel günü UTC
// anlarına ([başlangıç, bitiş)) çevirip bağlar — CH oturum dilimine güvenilmez.

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Özel gün türleri (JSON değerleri).
const (
	CalendarHoliday  = "holiday"
	CalendarHalfDay  = "half-day"
	CalendarMonthEnd = "month-end-like"
)

const (
	calendarMaxDays       = 200 // satır tavanı (elle düzenlenmiş blob sınırsız büyümesin)
	calendarMaxSpanDays   = 31  // tek satırın aralık tavanı
	calendarLabelMaxRunes = 64
	// CalendarMonthEndDefaultN — otomatik ay sonu sınıfı: ayın son N iş günü.
	CalendarMonthEndDefaultN = 2
	calendarMonthEndMaxN     = 5
)

// AnomalySpecialDay — tek özel gün satırı. To boş = tek gün.
type AnomalySpecialDay struct {
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// AnomalyCalendarConfig — takvim bölümü. Sıfır değer = takvim yok (eski
// blob'lar bu bölümü taşımaz; davranış bayt bayt eski).
type AnomalyCalendarConfig struct {
	Days []AnomalySpecialDay `json:"days"`
	// MonthEndAuto — ayın son MonthEndBusinessDays iş günü otomatik
	// "month-end-like" sınıfı (varsayılan KAPALI).
	MonthEndAuto         bool `json:"monthEndAuto"`
	MonthEndBusinessDays int  `json:"monthEndBusinessDays"`
	// Timezone — v0.10.1490: özel günlerin hangi yerel güne ait olduğu (IANA
	// adı, ör. "Europe/Istanbul"). Boş = UTC: eski kurulumlar bayt bayt eski
	// davranışta kalır; UI tarayıcının dilimini ÖNERİR, kendiliğinden yazmaz.
	Timezone string `json:"timezone,omitempty"`
	// FixedHolidaysTR — v0.10.1750 (operatör, Türkiye ön ayarı): sabit resmi
	// tatiller HER YIL otomatik (1 Oca, 23 Nis, 1 May, 19 May, 15 Tem, 30 Ağu,
	// 29 Eki) + 28 Eki yarım gün. Dini bayramlar yıla bağlı → Days listesi.
	FixedHolidaysTR bool `json:"fixedHolidaysTR,omitempty"`
	// AutoRules — "ay sonu benzeri" otomatik sınıflar, her biri ayrı anahtar.
	AutoRules CalendarAutoRules `json:"autoRules"`
}

// CalendarAutoRules — ay sonu benzeri otomatik günler (anomaly/calendar.go uygular).
type CalendarAutoRules struct {
	Mid15                     bool `json:"mid15"`                     // ayın 15'i (iş günü değilse sonraki iş günü)
	LastBusinessDay           bool `json:"lastBusinessDay"`           // ayın son iş günü
	YearEnd                   bool `json:"yearEnd"`                   // yılın son iş günü
	PreHolidayLastBusinessDay bool `json:"preHolidayLastBusinessDay"` // tatil/bayram öncesi son iş günü
}

// trFixedHolidays — ay-gün → tür. 28 Ekim yarım gün (13:00'ten; saat sınıfı yok,
// bütün gün yarım gün sayılır — audit §10).
var trFixedHolidays = map[string]string{
	"01-01": CalendarHoliday, "04-23": CalendarHoliday, "05-01": CalendarHoliday, "05-19": CalendarHoliday,
	"07-15": CalendarHoliday, "08-30": CalendarHoliday, "10-29": CalendarHoliday, "10-28": CalendarHalfDay,
}

// TRFixedHolidayKind — SAF: yıl bağımsız sabit Türkiye tatili türü ("" = değil).
func TRFixedHolidayKind(t time.Time) string { return trFixedHolidays[t.Format("01-02")] }

// ValidateCalendarTimezone — SAF: PUT kapısı. Boş = UTC (geçerli); "Local"
// sunucuya bağlı olduğu için reddedilir; aksi time.LoadLocation (tzdata
// main.go'da gömülü).
func ValidateCalendarTimezone(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if strings.EqualFold(name, "local") {
		return fmt.Errorf("calendar.timezone: %q is server-dependent; use an IANA name such as Europe/Istanbul", name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("calendar.timezone: unknown IANA time zone %q", name)
	}
	return nil
}

// calendarLocCache — ad → *time.Location (LoadLocation gömülü tzdata'yı her
// çağrıda açar; SpecialKindOn bir tikte binlerce kez koşar).
var calendarLocCache sync.Map

// Location — takvimin dilimi; boş ya da çözülemeyen ad = UTC (Normalize
// geçersizi zaten boşaltır; elle düzenlenmiş blob da UTC'ye düşer).
func (c AnomalyCalendarConfig) Location() *time.Location {
	name := c.Timezone
	if name == "" || strings.EqualFold(name, "local") {
		return time.UTC
	}
	if v, ok := calendarLocCache.Load(name); ok {
		return v.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.UTC
	}
	calendarLocCache.Store(name, loc)
	return loc
}

// DayWindowUTC — SAF: "YYYY-MM-DD" yerel gününün UTC anları [start, end).
// DST günü 23 / 25 saattir (time.Date normalizasyonu). SQL'e giden takvim
// yüklemi bu anlarla kurulur; SpecialKindOn ile aynı sınırı görür
// (TestCalendarWindowAgreesWithSpecialKindOn).
func (c AnomalyCalendarConfig) DayWindowUTC(date string) (start, end time.Time, ok bool) {
	d, ok := calendarDate(date)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	loc := c.Location()
	start = time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	end = time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	return start.UTC(), end.UTC(), true
}

// calendarDate — SAF: "YYYY-MM-DD" → UTC gün başı.
func calendarDate(s string) (time.Time, bool) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// validCalendarKind — SAF.
func validCalendarKind(k string) bool {
	switch k {
	case CalendarHoliday, CalendarHalfDay, CalendarMonthEnd:
		return true
	}
	return false
}

// NormalizeAnomalyCalendar — SAF: geçersiz tarih / tür taşıyan satır DÜŞER
// (kelepçe değil: yanlış bir tatil günü sessizce başka güne kaymasın);
// To < From → tek gün; aralık en çok 31 gün; etiket kırpılır; From sırası;
// satır tavanı 200; MonthEndBusinessDays 1..5 (aralık dışı → 2).
func NormalizeAnomalyCalendar(c AnomalyCalendarConfig) AnomalyCalendarConfig {
	out := AnomalyCalendarConfig{Days: []AnomalySpecialDay{}, MonthEndAuto: c.MonthEndAuto, MonthEndBusinessDays: c.MonthEndBusinessDays}
	// v0.10.1490 — geçersiz dilim (elle düzenlenmiş blob; PUT 400 verir) → UTC.
	if tz := strings.TrimSpace(c.Timezone); ValidateCalendarTimezone(tz) == nil {
		out.Timezone = tz
	}
	if out.MonthEndBusinessDays < 1 || out.MonthEndBusinessDays > calendarMonthEndMaxN {
		out.MonthEndBusinessDays = CalendarMonthEndDefaultN
	}
	for _, d := range c.Days {
		kind := strings.TrimSpace(d.Kind)
		from, ok := calendarDate(d.From)
		if !ok || !validCalendarKind(kind) {
			continue
		}
		to := from
		if t, ok := calendarDate(d.To); ok && !t.Before(from) {
			to = t
		}
		if span := int(to.Sub(from).Hours() / 24); span >= calendarMaxSpanDays {
			to = from.AddDate(0, 0, calendarMaxSpanDays-1)
		}
		label := strings.TrimSpace(d.Label)
		if utf8.RuneCountInString(label) > calendarLabelMaxRunes {
			label = string([]rune(label)[:calendarLabelMaxRunes])
		}
		row := AnomalySpecialDay{From: from.Format("2006-01-02"), Label: label, Kind: kind}
		if !to.Equal(from) {
			row.To = to.Format("2006-01-02")
		}
		out.Days = append(out.Days, row)
	}
	sort.SliceStable(out.Days, func(i, j int) bool { return out.Days[i].From < out.Days[j].From })
	if len(out.Days) > calendarMaxDays {
		out.Days = out.Days[len(out.Days)-calendarMaxDays:] // en yeniler kalır
	}
	return out
}

// SpecialKindOn — SAF: t'nin YEREL gününün (Timezone; boş = UTC) AÇIK satır
// türü ("" = yok). Birden çok satır aynı güne düşerse öncelik holiday >
// half-day > month-end-like.
func (c AnomalyCalendarConfig) SpecialKindOn(t time.Time) string {
	day := t.In(c.Location()).Format("2006-01-02")
	best := ""
	rank := map[string]int{CalendarHoliday: 3, CalendarHalfDay: 2, CalendarMonthEnd: 1}
	for _, d := range c.Days {
		to := d.To
		if to == "" {
			to = d.From
		}
		if day >= d.From && day <= to && rank[d.Kind] > rank[best] {
			best = d.Kind
		}
	}
	if c.FixedHolidaysTR {
		if k := TRFixedHolidayKind(t.In(c.Location())); rank[k] > rank[best] {
			best = k
		}
	}
	return best
}
