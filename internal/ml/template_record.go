package ml

// template_record.go — v0.10.2253, anomali kartı sözleşmesi H13
// (docs/ml/anomaly-card-audit.md §9–§10): log şablonu dedektörleri
// (template_spike, rare_template; log_templates.go) olay kaydını doldurur.
//
// Seri: dedektörün koştuğu aile serisi — tikte ES şablon sayımından
// (MLTemplateCounts) ml_series'e yazılan servis × `tpl|<ifade>` kova sayısı.
// Snapshot için ES'e GİDİLMEZ: aynı seri ml_series'ten tek servis, zaman sınırlı
// CH okumasıyla (MLSeriesRange: LIMIT + max_execution_time), 1 dk ızgarada
// basamak (H10/H11 kurucusu); eksik kova 0. Bant: template_spike alarmı veren
// medyan ± criticalZ·effMAD (judge); rare_template tabanı tamamen 0 olan aile
// (rareJudge) → bant [0, 0] (hacim tabanı karar kuralıdır, bant değil). Hacim
// aynı seriden (olay penceresindeki satır). Tespit mantığı, eşikler,
// started_at değişmez; sınıf service_anomaly, kapılar gölgede.
// Maliyet: açık olay başına tik başına 1 CH okuması (ml_series), 0 ES sorgusu,
// tik başına en çok tplRecordMaxPerTick.

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	tplRecordMaxPerTick = 20
	tplRareBandMethod   = "zero_baseline_all_samples_rare_rule"
	tplTitleHead        = "Kova başına log sayısı"
)

// attachTemplateRecords — recorder: template_spike / rare_template olaylarına
// kayıt (yazım öncesi, applyRecordContract doğrular).
func attachTemplateRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.Store == nil || cfg.BucketSec <= 0 {
		return
	}
	step := int64(cfg.BucketSec)
	for i := range evs {
		ev := evs[i]
		if (ev.Detector != detTemplateSpike && ev.Detector != detRareTemplate) || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		phrase := evStr(m, "phrase")
		if phrase == "" {
			phrase = ev.Code
		}
		if phrase == "" {
			continue
		}
		part := tplPartPrefix + phrase
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket-lagRecordPad).Unix() / step * step
		last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec).Unix()
		if closed {
			last = min(last, time.Unix(0, ev.LastSeen).Add(lagRecordPad).Unix()/step*step)
		}
		if last <= from {
			continue
		}
		svcName := logServiceOf(ev)
		*budget--
		all, err := deps.Store.MLSeriesRange(ctx, chstore.MLSignalLog, svcName, time.Unix(from, 0), time.Unix(last+step, 0), cfg.BucketSec)
		if err != nil {
			continue // önceki kayıt (varsa) aynen kalır
		}
		v := logREDGrid(all[part], from, last, step)
		lo, hi := 0.0, 0.0
		if ev.Detector == detTemplateSpike {
			mad, _ := m["mad"].(float64)
			lo, hi = lagBand(ev.Baseline, mad, cfg.CriticalZ)
		}
		los, his := make([]float64, len(v)), make([]float64, len(v))
		for j := range v {
			los[j], his[j] = lo, hi
		}
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildTemplateRecord(CPRecordInput{Detector: ev.Detector, Signal: chstore.MLSignalLog, Service: ev.Service, SeriesService: svcName,
			Part: part, BucketSec: cfg.BucketSec, T0: from, V: v, Lo: los, Hi: his, Direction: "up", DetectedAt: det,
			Closed: closed, Confirmers: conf}, phrase)
		if !ok {
			continue
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			continue
		}
		ev.Record, ev.Class, ev.OnsetAt, ev.PublishState = string(raw), rec.Class, rec.OnsetAt, PublishPublished
		evs[i] = ev
	}
}

// BuildTemplateRecord — SAF: kova serisi kurucusu (H10) + şablon bandı yöntemi,
// kanıt ve başlık. Başlık ifadeyi tırnakla taşır; ifadedeki bir sayı başlık
// sayı kuralına takılacaksa (ör. "HTTP 503") ifadesiz başlık yazılır, ifade
// kanıt satırında kalır.
func BuildTemplateRecord(in CPRecordInput, phrase string) (EventRecord, bool) {
	rec, ok := BuildChangePointRecord(in)
	if !ok {
		return rec, false
	}
	rec.Baseline.Method = lagBandMethod
	label := "Log şablonu ailesi"
	if in.Detector == detRareTemplate {
		rec.Baseline.Method = tplRareBandMethod
		label = "Nadir log şablonu ailesi (taban tamamen 0)"
	}
	rec.Evidence[0].Summary = label + " \"" + phrase + "\" satır sayısı, tikteki ES şablon sayımının ml_series kova serisi"
	plain := strings.Replace(rec.Title, tplTitleHead, "Kova başına şablon satırı", 1)
	rec.Title = strings.Replace(rec.Title, tplTitleHead, "Şablon \""+phrase+"\" kova başına satır", 1)
	if len(titleNumbersOutsideFields(rec)) > 0 {
		rec.Title = plain
	}
	return rec, true
}
