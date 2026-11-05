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
}

// AnomalySubjectsConfig — üç özne türü. Servis düzeyi her zaman açık ve
// burada YOK (bugünkü dedektör).
type AnomalySubjectsConfig struct {
	Endpoint AnomalySubjectKindConfig `json:"endpoint"`
	DB       AnomalySubjectKindConfig `json:"db"`
	Queue    AnomalySubjectKindConfig `json:"queue"`
}

const (
	anomalySubjectTopNDefault      = 20
	anomalySubjectTopNMax          = 200
	anomalySubjectMaxSeriesDefault = 2000
	anomalySubjectMaxSeriesMax     = 50000

	// DefaultSeasonalTimezone — karar 3: gün sınıfı operatör diliminde.
	DefaultSeasonalTimezone = "Europe/Istanbul"
)

// DefaultAnomalySubjects — üç tür kapalı, tavanlar varsayılan.
func DefaultAnomalySubjects() AnomalySubjectsConfig {
	k := AnomalySubjectKindConfig{TopN: anomalySubjectTopNDefault, MaxSeriesPerTick: anomalySubjectMaxSeriesDefault}
	return AnomalySubjectsConfig{Endpoint: k, DB: k, Queue: k}
}

// NormalizeAnomalySubjects — SAF: aralık dışı / eksik (0) tavan varsayılana
// döner; Enabled aynen. Eski blob (bölüm yok) sıfır değerdir → üçü kapalı +
// varsayılan tavanlar.
func NormalizeAnomalySubjects(c AnomalySubjectsConfig) AnomalySubjectsConfig {
	return AnomalySubjectsConfig{
		Endpoint: normalizeSubjectKind(c.Endpoint),
		DB:       normalizeSubjectKind(c.DB),
		Queue:    normalizeSubjectKind(c.Queue),
	}
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
