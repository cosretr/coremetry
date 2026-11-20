package ml

// svc_response_time_record.go — v0.10.2385 (etki olayı programı, dilim I4):
// servis yanıt süresi olayının kaydı (anomali kartı sözleşmesi; hata oranı
// kaydının I3 yolu error_rate_record.go ile aynı akış ve aynı saf kurucu
// BuildREDRecord; op_latency kaydının seri / hacim / ızgara yardımcıları).
//
// Seri: dedektörün YARGILADIĞI seri — olayın ölçüsünün (medyan ya da en yavaş
// %10) 1 dakikalık yüzdeliği, yalnız bu servis için (pencere = adım = 60 sn;
// ifade histogram_quantile, tek servisin bucket'ları: 422 dersindeki "tüm
// servisler × ham bucket aralığı" değil). Bant = [0, etki kuralının eşiği]
// (impactRules; alarmı veren sınır), beklenen = mevsimsel medyan. Pencere
// dakika başına hizalı ve ölçülen son TAM dakikada biter (dedektörün karar
// penceresiyle aynı bitiş). Tepe, tespit anı değeri, onset ve bitiş yalnız
// bu seriden; yük kapısı kayda girmez. Maliyet: açık olay başına tik başına
// 2 aralık sorgusu, tik başına en çok rtRecordMaxPerTick olay.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	rtRecordMaxPerTick = 10
	rtBandMethodMedian = "class_days_hour±1_median_impact_x1.5_plus_100ms"
	rtBandMethodSlow   = "class_days_hour±1_median_impact_x2_plus_1000ms"
)

// rtSeriesQuery — SAF: servisin 1 dakikalık yüzdelik ifadesi (ms) + hacim.
func rtSeriesQuery(pin anomaly.REDFamilyPin, svcLabel, svc string, m rtMeasure) (latSeries, bool) {
	if svcLabel == "" || svc == "" {
		return latSeries{}, false
	}
	// Seçici elle kurulur (ad + {etiket="değer"}): thanos.WithEqualMatcher
	// noktalı OTel adını (HTTP ailesi) parçalara böler; tikin sorguları adı
	// olduğu gibi kullanır, kayıt da öyle.
	bucket := fmt.Sprintf("%s_bucket{%s=%q}", pin.Names, svcLabel, svc)
	count := fmt.Sprintf("%s_count{%s=%q}", pin.Names, svcLabel, svc)
	return latSeries{
		Query:       fmt.Sprintf("histogram_quantile(%g, sum by (le) (increase(%s[%ds]))) * %g", m.q, bucket, impactStepSec, pinMsScale(pin)),
		VolumeQuery: fmt.Sprintf("sum(increase(%s[60s]))", count),
		Unit:        "ms", Aggregation: m.agg, Scale: 1,
	}, true
}

// rtRecordBand — SAF: alarmı veren bant (ms): beklenen = mevsimsel medyan,
// üst sınır = ölçünün etki kuralı eşiği, alt sınır 0.
func rtRecordBand(m rtMeasure, baseline float64) (expected, lo, hi float64, method string) {
	method = rtBandMethodMedian
	if m.kind == impactResponseSlow {
		method = rtBandMethodSlow
	}
	return baseline, 0, impactRules[m.kind].threshold(baseline), method
}

// attachSvcRTRecords — recorder: svc_response_time olaylarına kayıt (yazım
// öncesi, applyRecordContract doğrular). attachErrorRateRecords ile aynı akış.
func attachSvcRTRecords(ctx context.Context, deps Deps, cfg chstore.MLConfig, evs []chstore.MLEvent, closed bool,
	detectedAt func(epID string) time.Time, confirmers func(ev chstore.MLEvent) []string, budget *int, now time.Time) {
	if deps.VM == nil || !deps.VM.Configured() {
		return
	}
	pins := map[string]anomaly.REDFamilyPin{}
	for _, p := range anomaly.REDFamilyPins() {
		pins[p.ID] = p
	}
	end := svcImpactEnd(now, cfg.IngestLagSec)
	for i := range evs {
		ev := evs[i]
		if ev.Detector != detSvcResponseTime || ev.Stage == chstore.MLStageChronic || *budget <= 0 {
			continue
		}
		em := evidenceMap(ev.Evidence)
		fam, _ := em["redFamily"].(string)
		svcL, _ := em["svcLabel"].(string)
		pin, ok := pins[fam]
		if !ok {
			continue // tik kanıtı yok: kayıt yazılmaz
		}
		m := rtMeasureOfEvent(ev)
		s, ok := rtSeriesQuery(pin, svcL, ev.Service, m)
		if !ok {
			continue
		}
		det := detectedAt(ev.ID)
		if det.IsZero() || det.After(now) {
			det = now
		}
		// pencere dakika başına hizalı: aralık noktaları dedektörün dakikalarıyla aynı
		from := det.Add(-impactWindow*impactStepSec*time.Second - lagRecordPad).Truncate(impactStepSec * time.Second)
		to := end
		if closed {
			if t := time.Unix(0, ev.LastSeen).Add(lagRecordPad).Truncate(impactStepSec * time.Second); t.Before(to) {
				to = t
			}
		}
		if !to.After(from) {
			continue
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
		exp, lo, hi, method := rtRecordBand(m, ev.Baseline)
		var conf []string
		if confirmers != nil {
			conf = confirmers(ev)
		}
		rec, ok := BuildREDRecord(REDRecordInput{Detector: ev.Detector, Service: ev.Service, Family: fam, Series: s,
			Direction: "up", BucketSec: impactStepSec, Expected: exp, Lo: lo, Hi: hi, Method: method, DetectedAt: det, Closed: closed,
			Points: rows[0].Points, Calls: calls, From: from, To: to, Confirmers: conf,
			Head: "Yanıt süresi (" + m.agg + ")", Label: "yanıt süresi (" + m.agg + ")", Fmt: msFmt})
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
