package ml

// throughput_record.go — v0.10.2222, anomali kartı sözleşmesi H6
// (docs/ml/anomaly-card-audit.md §9–§10): endpoint trafik düşüşü
// (throughput_drop, trace_red.go) olay kaydını doldurur.
//
// Seri: dedektörün "count" ölçüsü — servis × route istek/sn'si (VM RED
// _count rate'i, dedektörün kova penceresiyle); aile ve etiket adları tik
// anında kanıta yazılır (redFamily/svcLabel/routeLabel). Snapshot aynı
// ifadenin 1 dk adımlı aralık sorgusu (olay ±30 dk); dip, tespit anı değeri,
// onset ve bitiş YALNIZ bu seriden (yön "down", alt sınır). Bant alarmı
// veren medyan ± criticalZ·effMAD (judgeMM). Hacim aynı seriden: istek/sn ×
// adım (dakika başına çağrı) — ek sorgu yok. Tespit mantığı, eşikler,
// started_at değişmez; gates[] boş, sınıf service_anomaly. Maliyet: açık
// olay başına tik başına 1 aralık sorgusu, en çok tpRecordMaxPerTick.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const tpRecordMaxPerTick = 20

// tpSeriesQuery — SAF: traceQueries'in count ifadesi, servis × route kapsamında.
func tpSeriesQuery(pin anomaly.REDFamilyPin, svcLabel, routeLabel, svc, route string, winSec int) (latSeries, bool) {
	if svcLabel == "" || routeLabel == "" || svc == "" || route == "" || winSec <= 0 {
		return latSeries{}, false
	}
	cnt := redScope(pin, partCount, svcLabel, routeLabel, svc, route)
	return latSeries{Query: fmt.Sprintf("sum(rate(%s[%ds]))", cnt, winSec), Unit: "req/s", Aggregation: "rate", Scale: 1}, true
}

// rpsFmt — başlıkta istek/sn.
func rpsFmt(x float64) string { return metricNum(x) + " istek/sn" }

// attachThroughputRecords — recorder: throughput_drop olaylarına kayıt
// (yazım öncesi, applyRecordContract doğrular). attachLatencyRecords ile aynı akış.
func attachThroughputRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	pins := map[string]anomaly.REDFamilyPin{}
	for _, p := range anomaly.REDFamilyPins() {
		pins[p.ID] = p
	}
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detThroughputDrop || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		fam, _ := m["redFamily"].(string)
		svcL, _ := m["svcLabel"].(string)
		routeL, _ := m["routeLabel"].(string)
		pin, ok := pins[fam]
		if !ok {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
		}
		s, ok := tpSeriesQuery(pin, svcL, routeL, ev.Service, ev.Code, cfg.BucketSec)
		if !ok {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		bucket := time.Duration(cfg.BucketSec) * time.Second
		from := det.Add(-time.Duration(cfg.DwellBuckets)*bucket - lagRecordPad)
		to := now
		if closed {
			to = time.Unix(0, ev.LastSeen).Add(lagRecordPad)
			if to.After(now) {
				to = now
			}
		}
		*budget--
		rows, err := latRange(ctx, deps, s.Query, from, to)
		if err != nil || len(rows) == 0 {
			continue // önceki kayıt (varsa) aynen kalır
		}
		mad, _ := m["mad"].(float64)
		lo, hi := lagBand(ev.Baseline, mad, cfg.CriticalZ)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildREDRecord(REDRecordInput{Detector: ev.Detector, Service: ev.Service, Route: ev.Code, Family: fam, Series: s,
			Direction: "down", BucketSec: cfg.BucketSec, Expected: ev.Baseline, Lo: lo, Hi: hi, Method: lagBandMethod,
			DetectedAt: det, Closed: closed, Points: rows[0].Points, CallsFromSeries: true, From: from, To: to, Confirmers: conf,
			Head: "İstek hızı", Label: "istek hızı", Fmt: rpsFmt})
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
