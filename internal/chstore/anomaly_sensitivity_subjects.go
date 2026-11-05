package chstore

// v0.10.1790 — Davis paritesi FAZ 1.2 (docs/plans/spec-davis-faz1.md):
// anomali tabanının ÖZNE türleri (endpoint / db / queue) ve mevsimsel gün
// sınıfının saat dilimi, anomaly_sensitivity blob'unun iki yeni bölümü.
//
// Varsayılanlar bilinçli:
//   - Subjects: üç tür de KAPALI. Özneler FAZ 1.5 eval kapısını geçmeden
//     varsayılan açılmaz (spec F1.5). Eski blob (bölüm yok) = kapalı.
//   - SeasonalTimezone: "Europe/Istanbul" (karar 3, operatör 2026-10-29).
//     Eski blob (alan yok) bu varsayılanı alır; mevsimsel gün sınıfı bir
//     kez kayar (UTC 21:00-24:00 kovaları ertesi yerel güne geçer — TR'de
//     yaz saati yok, saat-of-day eşlemesi değişmez). Tüketen yol F1.3
//     (v0.10.1791); bu sürüm yalnız ayarı taşır.

import (
	"fmt"
	"strings"
)

// AnomalySubjectKindConfig — tek özne türünün vidaları.
type AnomalySubjectKindConfig struct {
	// Enabled — tür tabanı hesaplanır mı. Varsayılan false.
	Enabled bool `json:"enabled"`
	// TopN — servis başına en çok kaç özne (trafiğe göre üst-N; karar 1).
	TopN int `json:"topN"`
	// MaxSeriesPerTick — bu türün bir tikte okuyabileceği en çok seri
	// (tüm servisler toplamı). Aşımda tür o tik ATLANIR (kesilmez — yarım
	// filo tabanı "hangi servis eksik" sorusunu cevapsız bırakırdı) ve
	// self-obs sayacı artar.
	MaxSeriesPerTick int `json:"maxSeriesPerTick"`
	// LagMinRecords — v0.10.1802 (Davis dilim 3, Kafka consumer lag): YALNIZ
	// queue türünde anlamlı. consumer_lag olayı cari lag bu kayıt sayısının
	// ALTINDAYSA üretilmez (düşük mutlak lag gürültüsü: 3 → 40 kayıt 13× ama
	// kimseyi ilgilendirmez). Varsayılan 1000; endpoint / db'de 0 (yazılmaz).
	LagMinRecords int `json:"lagMinRecords,omitempty"`
}

// AnomalySubjectsConfig — üç özne türü. Servis düzeyi her zaman açık ve
// burada YOK (bugünkü dedektör).
type AnomalySubjectsConfig struct {
	Endpoint AnomalySubjectKindConfig `json:"endpoint"`
	DB       AnomalySubjectKindConfig `json:"db"`
	Queue    AnomalySubjectKindConfig `json:"queue"`
	// PromotionMode — v0.10.1797 (Davis FAZ 3, spec-davis-faz3.md): özne
	// olaylarının Problem'e terfi kipi. "off" (varsayılan) = terfi yok (FAZ 1
	// davranışı); "shadow" = kapıyı geçen aday Problem AÇMAZ, olay satırına
	// iz düşer (karar 6: önce gölge); "live" = Problem açılır. Bilinmeyen /
	// boş → off (NormalizeAnomalySubjects); PUT bilinmeyeni REDDEDER.
	PromotionMode string `json:"promotionMode"`
	// PromotionMinSustainedSec / PromotionMinRatio — v0.10.1798 (FAZ 3
	// dilim 2): özne terfi kapısının eşikleri (spec adları subjectMin*;
	// özne bölümünde yaşadıkları için önek promotion). Olay en az bu kadar
	// sürmüş VE bölüm tepesi tabanın en az bu katı olmalı. Tabansız
	// error_rate (oran yok) oran yerine anomaly_promotion.countOnlyMinCount
	// sayım tabanını kullanır. 0 / aralık dışı → varsayılan (600 sn, 2,0).
	PromotionMinSustainedSec int     `json:"promotionMinSustainedSec"`
	PromotionMinRatio        float64 `json:"promotionMinRatio"`
}

// Özne terfi kipleri (PromotionMode).
const (
	SubjectPromotionOff    = "off"
	SubjectPromotionShadow = "shadow"
	SubjectPromotionLive   = "live"
)

const (
	anomalySubjectTopNDefault      = 20
	anomalySubjectTopNMax          = 200
	anomalySubjectMaxSeriesDefault = 2000
	anomalySubjectMaxSeriesMax     = 50000

	// v0.10.1798 — özne terfi kapısı varsayılanları + aralıkları.
	subjectPromotionMinSustainedDefault = 600
	subjectPromotionMinSustainedMin     = 60
	subjectPromotionMinSustainedMax     = 86400
	subjectPromotionMinRatioDefault     = 2.0
	subjectPromotionMinRatioMin         = 1.0
	subjectPromotionMinRatioMax         = 1000.0

	// v0.10.1802 — queue.lagMinRecords varsayılanı + aralığı.
	subjectLagMinRecordsDefault = 1000
	subjectLagMinRecordsMax     = 1_000_000_000

	// DefaultSeasonalTimezone — karar 3: gün sınıfı operatör diliminde.
	DefaultSeasonalTimezone = "Europe/Istanbul"
)

// DefaultAnomalySubjects — üç tür kapalı, tavanlar varsayılan.
func DefaultAnomalySubjects() AnomalySubjectsConfig {
	k := AnomalySubjectKindConfig{TopN: anomalySubjectTopNDefault, MaxSeriesPerTick: anomalySubjectMaxSeriesDefault}
	q := k
	q.LagMinRecords = subjectLagMinRecordsDefault
	return AnomalySubjectsConfig{Endpoint: k, DB: k, Queue: q, PromotionMode: SubjectPromotionOff,
		PromotionMinSustainedSec: subjectPromotionMinSustainedDefault, PromotionMinRatio: subjectPromotionMinRatioDefault}
}

// NormalizeAnomalySubjects — SAF: aralık dışı / eksik (0) tavan varsayılana
// döner; Enabled aynen. Eski blob (bölüm yok) sıfır değerdir → üçü kapalı +
// varsayılan tavanlar.
func NormalizeAnomalySubjects(c AnomalySubjectsConfig) AnomalySubjectsConfig {
	q := normalizeSubjectKind(c.Queue)
	// v0.10.1802 — lag tabanı yalnız queue'da; 0 / aralık dışı → 1000.
	q.LagMinRecords = clampRangeI(c.Queue.LagMinRecords, 1, subjectLagMinRecordsMax, subjectLagMinRecordsDefault)
	return AnomalySubjectsConfig{
		Endpoint: normalizeSubjectKind(c.Endpoint),
		DB:       normalizeSubjectKind(c.DB),
		Queue:    q,
		// v0.10.1797 — bilinmeyen / boş kip KAPALI (güvenli yön: elle
		// bozulmuş blob Problem açmaz).
		PromotionMode: normalizeSubjectPromotionMode(c.PromotionMode),
		PromotionMinSustainedSec: clampRangeI(c.PromotionMinSustainedSec,
			subjectPromotionMinSustainedMin, subjectPromotionMinSustainedMax, subjectPromotionMinSustainedDefault),
		PromotionMinRatio: clampRangeF(c.PromotionMinRatio,
			subjectPromotionMinRatioMin, subjectPromotionMinRatioMax, subjectPromotionMinRatioDefault),
	}
}

// normalizeSubjectPromotionMode — SAF: üç kipten biri, aksi "off".
func normalizeSubjectPromotionMode(m string) string {
	switch m = strings.TrimSpace(m); m {
	case SubjectPromotionShadow, SubjectPromotionLive:
		return m
	}
	return SubjectPromotionOff
}

// ValidateSubjectPromotionMode — SAF: PUT kapısı. Boş geçerli (= off,
// alanı göndermeyen eski istemci); bilinmeyen değer RED — "liv" yazımı
// sessizce off'a dönseydi operatör canlıyı açtığını sanırdı.
func ValidateSubjectPromotionMode(m string) error {
	switch strings.TrimSpace(m) {
	case "", SubjectPromotionOff, SubjectPromotionShadow, SubjectPromotionLive:
		return nil
	}
	return fmt.Errorf("subjects.promotionMode must be off|shadow|live, got %q", m)
}

func normalizeSubjectKind(k AnomalySubjectKindConfig) AnomalySubjectKindConfig {
	return AnomalySubjectKindConfig{
		Enabled:          k.Enabled,
		TopN:             clampRangeI(k.TopN, 1, anomalySubjectTopNMax, anomalySubjectTopNDefault),
		MaxSeriesPerTick: clampRangeI(k.MaxSeriesPerTick, 1, anomalySubjectMaxSeriesMax, anomalySubjectMaxSeriesDefault),
	}
}

// AnyEnabled — en az bir özne türü açık mı.
func (c AnomalySubjectsConfig) AnyEnabled() bool {
	return c.Endpoint.Enabled || c.DB.Enabled || c.Queue.Enabled
}

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
