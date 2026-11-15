package ml

// dependency_record.go — v0.10.2234, anomali kartı sözleşmesi H9
// (docs/ml/anomaly-card-audit.md §9–§10): bağımlılık dedektörleri
// (dependency_latency, dependency_error_rate; dependency.go) olay kaydını
// doldurur.
//
// Seri: dedektörün koştuğu client histogram ifadesi, servis × hedef
// kapsamında, dedektörün kova penceresiyle — latency p95 (ms), hata oranı
// error_type dolu çağrı / tüm çağrı (yüzde; dedektörün min(1, …)'i
// clamp_max ile). Aile, servis ve hedef etiket adları tik anında kanıta
// yazılır (svcLabel/targetLabel); render anında bugünkü ayardan yeniden kurulmaz.
// Snapshot aynı ifadenin 1 dk adımlı aralık sorgusu (olay ±30 dk); hacim aynı
// çiftin _count'u (dakika başına çağrı). Tepe, tespit anı değeri, onset, bitiş
// YALNIZ bu seriden. Bant alarmı veren medyan ± criticalZ·effMAD (judge).
// Tespit mantığı, eşikler, started_at değişmez; gates[] boş, sınıf
// service_anomaly. Maliyet: açık olay başına tik başına 2 VM aralık sorgusu,
// tik başına en çok depRecordMaxPerTick olay.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const depRecordMaxPerTick = 10

// depSeriesQuery — SAF: dedektör ifadesi servis × hedef kapsamında.
// code = olayın Code'u ("<aile>|<hedef>").
func depSeriesQuery(det, code, svcLabel, targetLabel, svc string, winSec int) (latSeries, string, bool) {
	famID, tgt, _ := strings.Cut(code, "|")
	var f depFamily
	for _, x := range depFamilies {
		if x.ID == famID {
			f = x
		}
	}
	if f.ID == "" || tgt == "" || svcLabel == "" || targetLabel == "" || svc == "" || winSec <= 0 {
		return latSeries{}, "", false
	}
	scope := func(m string) string {
		return thanos.WithEqualMatcher(thanos.WithEqualMatcher(m, svcLabel, svc), targetLabel, tgt)
	}
	cnt := scope(f.Metric + "_count")
	s := latSeries{VolumeQuery: fmt.Sprintf("sum(increase(%s[60s]))", cnt)}
	switch det {
	case detDependencyLatency:
		s.Query = fmt.Sprintf("histogram_quantile(0.95, sum by (le) (rate(%s[%ds]))) * %g", scope(f.Metric+"_bucket"), winSec, f.Scale)
		s.Unit, s.Aggregation, s.Scale = "ms", "p95", 1
	case detDependencyErrorRate:
		if !f.ErrorRate {
			return latSeries{}, "", false
		}
		errSel := thanos.WithRegexMatcher(cnt, depErrorLabel, ".+")
		s.Query = fmt.Sprintf("clamp_max(100 * sum(rate(%s[%ds])) / sum(rate(%s[%ds])), 100)", errSel, winSec, cnt, winSec)
		s.Unit, s.Aggregation, s.Scale = "percent", "err_ratio", 100
	default:
		return latSeries{}, "", false
	}
	return s, f.ID, true
}

// msFmt — başlıkta milisaniye.
func msFmt(x float64) string { return trInt(x) + " ms" }

// attachDependencyRecords — recorder: dependency_* olaylarına kayıt (yazım
// öncesi, applyRecordContract doğrular). attachErrorRateRecords ile aynı akış.
func attachDependencyRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	for i := range evs {
		ev := evs[i]
		if (ev.Detector != detDependencyLatency && ev.Detector != detDependencyErrorRate) || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		m := evidenceMap(ev.Evidence)
		svcL, _ := m["svcLabel"].(string)
		tgtL, _ := m["targetLabel"].(string)
		s, fam, ok := depSeriesQuery(ev.Detector, ev.Code, svcL, tgtL, ev.Service, cfg.BucketSec)
		if !ok {
			continue // tik kanıtı yok (eski olay): kayıt yazılmaz
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
		var calls []chstore.SpanMetricPoint
		if vr, verr := latRange(ctx, deps, s.VolumeQuery, from, to); verr == nil && len(vr) > 0 {
			calls = vr[0].Points
		}
		mad, _ := m["mad"].(float64)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildDependencyRecord(depRecordIn(ev, s, fam, mad, cfg, det, closed, rows[0].Points, calls, from, to, conf))
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

// depRecordIn — SAF: olay + seri → BuildREDRecord girdisi; bant seri biriminde.
func depRecordIn(ev chstore.MLEvent, s latSeries, fam string, mad float64, cfg chstore.MLConfig, det time.Time, closed bool,
	pts, calls []chstore.SpanMetricPoint, from, to time.Time, conf []string) REDRecordInput {
	lo, hi := lagBand(ev.Baseline, mad, cfg.CriticalZ)
	in := REDRecordInput{Detector: ev.Detector, Service: ev.Service, Route: ev.Code, Family: fam, Series: s,
		Direction: "up", BucketSec: cfg.BucketSec, Expected: ev.Baseline * s.Scale, Lo: lo * s.Scale, Hi: hi * s.Scale,
		Method: lagBandMethod, DetectedAt: det, Closed: closed, Points: pts, Calls: calls, From: from, To: to, Confirmers: conf,
		Head: "Bağımlılık p95", Label: "p95 gecikmesi", Fmt: msFmt}
	if ev.Detector == detDependencyErrorRate {
		in.Head, in.Label, in.Fmt = "Bağımlılık hata oranı", "hata oranı", pctFmt
	}
	return in
}

// BuildDependencyRecord — SAF: BuildREDRecord + bağımlılık kanıt metni.
func BuildDependencyRecord(in REDRecordInput) (EventRecord, bool) {
	rec, ok := BuildREDRecord(in)
	if ok && len(rec.Evidence) > 0 {
		rec.Evidence[0].Summary = "Servis → bağımlılık " + in.Label + ", client histogram ailesi " + in.Family
	}
	return rec, ok
}
