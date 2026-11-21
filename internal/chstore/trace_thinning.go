package chstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// trace_thinning.go — v0.10.2440: kademeli trace saklama ("trace inceltme"),
// YALNIZ GÖLGE MOD. Hiçbir şey silinmez; iş 7–30 gün bandındaki her gün
// partition'ı için "kural açık olsaydı ne kalırdı / ne kazanılırdı"yı ölçer.
//
// Kural (operatör onaylı tasarım, docs/plans/trace-thinning-2026-11-21.md):
//   - 0..fullDays gün: her şey kalır;
//   - fullDays..retention: trace_id'nin deterministik %keepPercent'i
//     (cityHash64(trace_id) % 100 < keepPercent) + ÖNEMLİ trace'ler:
//     hata span'i olan, problem/anomali kanıtına (root_cause_hypotheses
//     exemplar), paylaşılan snapshot'a ya da CoSRE konuşmasına bağlı olan,
//     ve kök (servis, operasyon)unun p99'unu aşan yavaş kuyruk.
//
// Ayar `retention.thinning` (system_settings JSON), sonuçlar
// `retention.thinning.shadow` (system_settings JSON) — yeni şema yok.
// Ölçüm sorguları trace_thinning_measure.go'da.

const (
	ThinningSettingKey = "retention.thinning"
	thinningShadowKey  = "retention.thinning.shadow"
	thinningLockKey    = "coremetry:lock:trace-thinning"

	ThinningModeOff    = "off"
	ThinningModeShadow = "shadow"
	ThinningModeOn     = "on" // ayrılmış — gerçek silme bu dilimde YOK

	// Sınıflar — sıra = atfetme önceliği (bir trace yalnız ilk eşleşen
	// sınıfa sayılır; toplam = tutulacaklar).
	ThinClassDrop  = 0
	ThinClassHash  = 1
	ThinClassError = 2
	ThinClassRef   = 3
	ThinClassSlow  = 4

	// thinningSlowMinSamples — kök operasyonun p99'u bu kadar örneklenmiş
	// trace'ten azına dayanıyorsa "yavaş" hükmü verilmez (gürültü).
	thinningSlowMinSamples = 100
	// thinningRemeasureAfter — ölçülmüş gün yeniden ölçülür (sonradan
	// eklenen CoSRE / kanıt referansları önemli kümeyi büyütür).
	thinningRemeasureAfter = 7 * 24 * time.Hour
	// thinningRetryFailed — başarısız gün bu süre geçmeden yeniden
	// denenmez (her tikte aynı ağır sorguyu tekrarlamamak için).
	thinningRetryFailed = 6 * time.Hour
)

// ThinningSettings — `retention.thinning`.
type ThinningSettings struct {
	Mode        string `json:"mode"`
	KeepPercent int    `json:"keepPercent"`
	FullDays    int    `json:"fullDays"`
}

// DefaultThinningSettings — kapalı, %10, 7 gün.
func DefaultThinningSettings() ThinningSettings {
	return ThinningSettings{Mode: ThinningModeOff, KeepPercent: 10, FullDays: 7}
}

// normalize — boş/sıfır alanlar varsayılana döner (eski blob'lar).
func (c ThinningSettings) normalize() ThinningSettings {
	d := DefaultThinningSettings()
	if c.Mode == "" {
		c.Mode = d.Mode
	}
	if c.KeepPercent == 0 {
		c.KeepPercent = d.KeepPercent
	}
	if c.FullDays == 0 {
		c.FullDays = d.FullDays
	}
	return c
}

// Validate — "on" ayrılmış, bu sürümde reddedilir.
func (c ThinningSettings) Validate() error {
	switch c.Mode {
	case ThinningModeOff, ThinningModeShadow:
	case ThinningModeOn:
		return errors.New("mode=on ayrılmış: gerçek inceltme bu sürümde yok, yalnız off|shadow")
	default:
		return fmt.Errorf("mode off|shadow olmalı, %q geldi", c.Mode)
	}
	if c.KeepPercent < 1 || c.KeepPercent > 100 {
		return errors.New("keepPercent 1..100 olmalı")
	}
	if c.FullDays < 1 || c.FullDays > 89 {
		return errors.New("fullDays 1..89 olmalı")
	}
	return nil
}

// LoadThinningSettings — kayıtlı blob (yoksa varsayılan). İşçi her tik
// çağırır: çok pod'lu kurulumda PUT'u başka pod yapmış olabilir.
func (s *Store) LoadThinningSettings(ctx context.Context) (ThinningSettings, error) {
	raw, err := s.GetSetting(ctx, ThinningSettingKey)
	if err != nil {
		return DefaultThinningSettings(), err
	}
	var c ThinningSettings
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &c); err != nil {
			return DefaultThinningSettings(), fmt.Errorf("%s çözülemedi: %w", ThinningSettingKey, err)
		}
	}
	return c.normalize(), nil
}

// SaveThinningSettings — doğrula + yaz (audit çağıranda).
func (s *Store) SaveThinningSettings(ctx context.Context, c ThinningSettings) (ThinningSettings, error) {
	c = c.normalize()
	if err := c.Validate(); err != nil {
		return c, err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return c, err
	}
	return c, s.PutSetting(ctx, ThinningSettingKey, raw)
}

// ── saf kural ────────────────────────────────────────────────────────────

// ThinTrace — bir trace'in kural girdileri. HashBucket =
// cityHash64(trace_id) % 100 (SQL'de hesaplanır; Go aynası yok, bilinçli:
// kural tek yerde, SQL'de koşar — thinningClassSQL bu fonksiyonla eşlenik
// tutulur, TestThinningClassSQLMirrorsGo).
type ThinTrace struct {
	HashBucket int
	Errors     uint64
	IsRef      bool
	DurNs      int64
	OpP99Ns    float64
	OpSamples  uint64
}

// ThinningClassify — tutulma sınıfı (0 = düşer). Öncelik: hash → hata →
// referans → yavaş kuyruk.
func ThinningClassify(t ThinTrace, keepPercent int) int {
	switch {
	case t.HashBucket < keepPercent:
		return ThinClassHash
	case t.Errors > 0:
		return ThinClassError
	case t.IsRef:
		return ThinClassRef
	case t.OpSamples >= thinningSlowMinSamples && t.OpP99Ns > 0 && float64(t.DurNs) > t.OpP99Ns:
		return ThinClassSlow
	}
	return ThinClassDrop
}

// thinningClassSQL — ThinningClassify'ın SQL ikizi (iç sorgu kolonları:
// trace_id, errs, is_ref, dur, p99_op, n_op). keepPercent tamsayı, %d ile
// gömülür (parametre değil: multiIf içinde sabit).
func thinningClassSQL(keepPercent int) string {
	return fmt.Sprintf(`multiIf(cityHash64(trace_id) %% 100 < %d, %d, errs > 0, %d, is_ref, %d, n_op >= %d AND p99_op > 0 AND dur > p99_op, %d, %d)`,
		keepPercent, ThinClassHash, ThinClassError, ThinClassRef, thinningSlowMinSamples, ThinClassSlow, ThinClassDrop)
}

// ── saf tahminci ─────────────────────────────────────────────────────────

// ThinClassCount — bir sınıfın ağırlıklı trace / span sayısı.
type ThinClassCount struct {
	Traces uint64 `json:"traces"`
	Spans  uint64 `json:"spans"`
}

// thinSampleAgg — ölçüm sorgularının ham (ağırlıksız) çıktısı.
type thinSampleAgg struct {
	Traces  uint64
	Spans   uint64
	Classes [5]ThinClassCount // index = sınıf
}

// ThinningDay — bir gün partition'ının gölge tahmini.
type ThinningDay struct {
	Day         string `json:"day"` // YYYY-MM-DD (UTC)
	MeasuredAt  int64  `json:"measuredAt"`
	KeepPercent int    `json:"keepPercent"`
	// Kesin (system.parts, spans partition'ı).
	Spans uint64 `json:"spans"`
	Bytes uint64 `json:"bytes"`
	// Tahmin.
	Traces         uint64                    `json:"traces"`
	KeepTraces     uint64                    `json:"keepTraces"`
	KeepSpans      uint64                    `json:"keepSpans"`
	BytesSaved     uint64                    `json:"bytesSaved"`
	ByClass        map[string]ThinClassCount `json:"byClass"` // hash|error|ref|slow
	SampledTraces  uint64                    `json:"sampledTraces"`
	SampleDen      uint64                    `json:"sampleDen"`
	BucketFraction float64                   `json:"bucketFraction"` // okunan 5 dk kova / gün kovası
	RefsInDay      uint64                    `json:"refsInDay"`
	RefsTruncated  bool                      `json:"refsTruncated,omitempty"`
	ElapsedMs      int64                     `json:"elapsedMs"`
	Note           string                    `json:"note,omitempty"`
	// Failed — ölçüm hata verdi (Note = hata); toplamlara girmez,
	// thinningRetryFailed sonra yeniden denenir.
	Failed bool `json:"failed,omitempty"`
}

var thinClassNames = map[int]string{ThinClassHash: "hash", ThinClassError: "error", ThinClassRef: "ref", ThinClassSlow: "slow"}

// thinningEstimate — örneklem (sample, ölçek = 1/(bucketFraction) × den)
// + referans trace'leri (kesin, ağırlık 1) → gün tahmini. Span / byte
// toplamları system.parts'tan KESİN; tutulma ORANI örneklemden.
func thinningEstimate(sample, refs thinSampleAgg, den uint64, bucketFraction float64, partsSpans, partsBytes uint64) ThinningDay {
	if den == 0 {
		den = 1
	}
	scale := float64(den)
	if bucketFraction > 0 && bucketFraction < 1 {
		scale /= bucketFraction
	}
	w := func(n uint64) float64 { return float64(n) * scale }
	d := ThinningDay{Spans: partsSpans, Bytes: partsBytes, SampledTraces: sample.Traces, SampleDen: den, BucketFraction: bucketFraction, ByClass: map[string]ThinClassCount{}}
	estSpans := w(sample.Spans) + float64(refs.Spans)
	d.Traces = uint64(w(sample.Traces) + float64(refs.Traces) + 0.5)
	var keepT, keepS float64
	for cls := ThinClassHash; cls <= ThinClassSlow; cls++ {
		t := w(sample.Classes[cls].Traces) + float64(refs.Classes[cls].Traces)
		sp := w(sample.Classes[cls].Spans) + float64(refs.Classes[cls].Spans)
		keepT += t
		keepS += sp
		// Span sayısı kesin toplama ölçeklenir (örneklem oranı × parts).
		cs := sp
		if estSpans > 0 && partsSpans > 0 {
			cs = sp / estSpans * float64(partsSpans)
		}
		d.ByClass[thinClassNames[cls]] = ThinClassCount{Traces: uint64(t + 0.5), Spans: uint64(cs + 0.5)}
	}
	d.KeepTraces = uint64(keepT + 0.5)
	frac := 1.0
	if estSpans > 0 {
		frac = keepS / estSpans
	}
	if frac > 1 {
		frac = 1
	}
	if partsSpans > 0 {
		d.KeepSpans = uint64(frac*float64(partsSpans) + 0.5)
	} else {
		d.KeepSpans = uint64(keepS + 0.5)
	}
	// Byte tasarrufu span-ağırlıklı (trace başı byte eşit varsayılmaz).
	d.BytesSaved = uint64((1-frac)*float64(partsBytes) + 0.5)
	return d
}

// thinningSampleDen — hedef ≈ 20M span'lik hash örneklemi (prod ~2.3 milyar
// span/gün → ~115). Okunan kova payı zaten 1/12; bu bölen GROUP BY
// belleğini sınırlar. Lokal (küçük) veride 1 = kovalarda kesin.
func thinningSampleDen(daySpans uint64, bucketFraction float64) uint64 {
	const target = 20_000_000
	inBuckets := float64(daySpans) * bucketFraction
	den := uint64(inBuckets / target)
	if den < 1 {
		den = 1
	}
	if den > 1000 {
		den = 1000
	}
	return den
}

// thinningSampleBuckets — günün her saatinden BİR 5 dk kova (saat başına
// döner ofset; :00 cron sivrilerine hep denk gelmesin). (time_bucket,
// trace_id) birincil anahtarı bu IN kümesiyle granül budar: gün
// partition'ının ~1/12'si okunur.
func thinningSampleBuckets(day time.Time) []time.Time {
	out := make([]time.Time, 0, 24)
	for h := 0; h < 24; h++ {
		out = append(out, day.Add(time.Duration(h)*time.Hour+time.Duration(((h*7)%12)*5)*time.Minute))
	}
	return out
}

// thinningBand — ölçülecek günler (UTC gün başları), yeni → eski: bütünü
// fullDays'ten eski ve retention ufkunun içinde olan günler.
func thinningBand(now time.Time, fullDays, retentionDays int) []time.Time {
	today := now.UTC().Truncate(24 * time.Hour)
	var out []time.Time
	for age := fullDays + 1; age <= retentionDays; age++ {
		out = append(out, today.AddDate(0, 0, -age))
	}
	return out
}

// thinningNextDay — sıradaki ölçülecek gün ("" = yok). Ölçülmemiş gün
// önce (yeni → eski); sonra keepPercent'i değişmiş ya da bayat ölçüm.
func thinningNextDay(band []time.Time, have map[string]ThinningDay, keepPercent int, now time.Time) string {
	for _, d := range band {
		if _, ok := have[d.Format("2006-01-02")]; !ok {
			return d.Format("2006-01-02")
		}
	}
	for _, d := range band {
		k := d.Format("2006-01-02")
		m := have[k]
		age := now.Sub(time.UnixMilli(m.MeasuredAt))
		if m.Failed {
			if age > thinningRetryFailed {
				return k
			}
			continue
		}
		if m.KeepPercent != keepPercent || age > thinningRemeasureAfter {
			return k
		}
	}
	return ""
}

// ── kalıcı durum ─────────────────────────────────────────────────────────

// ThinningShadowState — `retention.thinning.shadow`.
type ThinningShadowState struct {
	Days      []ThinningDay `json:"days"`
	LastRunAt int64         `json:"lastRunAt,omitempty"`
	LastError string        `json:"lastError,omitempty"`
}

var thinningMu sync.Mutex

func (s *Store) loadThinningShadow(ctx context.Context) (ThinningShadowState, error) {
	var st ThinningShadowState
	raw, err := s.GetSetting(WithoutSettingsSnapshot(ctx), thinningShadowKey)
	if err != nil {
		return st, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &st); err != nil {
			return st, fmt.Errorf("%s çözülemedi: %w", thinningShadowKey, err)
		}
	}
	return st, nil
}

// saveThinningShadow — günü yerleştir, bandın dışına düşenleri buda.
func (s *Store) saveThinningShadow(ctx context.Context, band []time.Time, day *ThinningDay, runErr string, now time.Time) error {
	thinningMu.Lock()
	defer thinningMu.Unlock()
	st, err := s.loadThinningShadow(ctx)
	if err != nil {
		return err
	}
	st.Days = thinningMergeDays(st.Days, band, day)
	st.LastRunAt = now.UnixMilli()
	st.LastError = runErr
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, thinningShadowKey, raw)
}

// thinningMergeDays — saf: band içindekiler kalır, day (varsa) yerine
// yazılır; sıra yeni → eski.
func thinningMergeDays(days []ThinningDay, band []time.Time, day *ThinningDay) []ThinningDay {
	in := map[string]bool{}
	for _, d := range band {
		in[d.Format("2006-01-02")] = true
	}
	m := map[string]ThinningDay{}
	for _, d := range days {
		if in[d.Day] {
			m[d.Day] = d
		}
	}
	if day != nil && in[day.Day] {
		m[day.Day] = *day
	}
	out := make([]ThinningDay, 0, len(m))
	for _, d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day > out[j].Day })
	return out
}

// ThinningTotals — bandın toplamı (yalnız ölçülmüş günler).
type ThinningTotals struct {
	Days       int                       `json:"days"`
	Spans      uint64                    `json:"spans"`
	Bytes      uint64                    `json:"bytes"`
	Traces     uint64                    `json:"traces"`
	KeepTraces uint64                    `json:"keepTraces"`
	KeepSpans  uint64                    `json:"keepSpans"`
	BytesSaved uint64                    `json:"bytesSaved"`
	ByClass    map[string]ThinClassCount `json:"byClass"`
}

// thinningTotals — saf toplam.
func thinningTotals(days []ThinningDay) ThinningTotals {
	t := ThinningTotals{ByClass: map[string]ThinClassCount{}}
	for _, d := range days {
		if d.Failed {
			continue
		}
		t.Days++
		t.Spans += d.Spans
		t.Bytes += d.Bytes
		t.Traces += d.Traces
		t.KeepTraces += d.KeepTraces
		t.KeepSpans += d.KeepSpans
		t.BytesSaved += d.BytesSaved
		for k, v := range d.ByClass {
			c := t.ByClass[k]
			c.Traces += v.Traces
			c.Spans += v.Spans
			t.ByClass[k] = c
		}
	}
	return t
}

// ThinningStatus — admin kartının tek yanıtı.
type ThinningStatus struct {
	Settings  ThinningSettings `json:"settings"`
	Days      []ThinningDay    `json:"days"`
	Totals    ThinningTotals   `json:"totals"`
	BandDays  int              `json:"bandDays"`
	Pending   int              `json:"pending"` // ölçülmemiş band günü
	LastRunAt int64            `json:"lastRunAt,omitempty"`
	LastError string           `json:"lastError,omitempty"`
	Generated int64            `json:"generated"`
}

// ThinningStatus — ayar + gölge sonuçları + toplam.
func (s *Store) ThinningStatus(ctx context.Context) (ThinningStatus, error) {
	cfg, err := s.LoadThinningSettings(ctx)
	if err != nil {
		return ThinningStatus{}, err
	}
	st, err := s.loadThinningShadow(ctx)
	if err != nil {
		return ThinningStatus{}, err
	}
	band := thinningBand(time.Now(), cfg.FullDays, s.spansRetentionDays(ctx))
	days := thinningMergeDays(st.Days, band, nil)
	have := map[string]bool{}
	for _, d := range days {
		if !d.Failed {
			have[d.Day] = true
		}
	}
	pending := 0
	for _, d := range band {
		if !have[d.Format("2006-01-02")] {
			pending++
		}
	}
	return ThinningStatus{
		Settings: cfg, Days: days, Totals: thinningTotals(days), BandDays: len(band), Pending: pending,
		LastRunAt: st.LastRunAt, LastError: st.LastError, Generated: time.Now().Unix(),
	}, nil
}

// spansRetentionDays — retention.spans override'ı, yoksa config varsayılanı
// (EnforceRetention ile aynı çözüm), o da yoksa 30.
func (s *Store) spansRetentionDays(ctx context.Context) int {
	sp, err := s.GetRetention(ctx)
	if err == nil && strings.TrimSpace(sp.Spans) != "" {
		if n, perr := parseRetentionDays(sp.Spans); perr == nil && n > 0 {
			return n
		}
	}
	if s.ret.SpansDays > 0 {
		return s.ret.SpansDays
	}
	return 30
}
