package ml

// batch.go — v0.10.1811: ML hattında batch servis kuralı (audit
// docs/anomaly/learning-loop-audit.md §13 D8).
//
// Operatör (v0.10.1039): "Bazı batch işlerde ani yük artışı olabilir, onları
// anomali gibi düşünme — özellikle `-batch` geçen servis isimlerinde."
// Klasik hat bunu uyguluyor, internal/ml uygulamıyordu.
//
// TEK YÜKLEM: chstore.AnomalySensitivityConfig.IsBatchService (Settings ›
// Anomaly › Batch servis ad kalıpları; doğrulanmamış ayarda liste boş =
// kural kapalı, AnomalySensitivityForDetectors). İkinci eşleştirici yok.
//
// TEK KARAR NOKTASI: recorder tikinde dedektör adaylarını toplarken
// (batchFilter) — kalite süzgecinden, epizot izleyicisinden ve ml_events
// yazımından ÖNCE. Kesilen aday satır üretmez; sayısı tik durumunda
// (MLTickStatus.BatchCut, sinyal başına MLSignalTick.BatchCut).
//
// KLASİK SEMANTİĞİN AYNASI (internal/anomaly/batch_load.go, trace_ops.go):
//
//	yük (iki yön)        klasik request_rate iki yönde susar ("işin başlaması
//	                     da bitmesi de olay değil") → log_volume, service_silent,
//	                     log change_point (_total), throughput_drop, Load
//	                     işaretli metrik serisi: KESİLİR
//	hata SAYISI          klasik trace_op error_spike batch'te ancak hata PAYI da
//	                     ≥ 3× (traceOpMinRatio) arttıysa açılır → log error_rate
//	                     (error seviyesi sayımı): pay kanıtı varsa aynı kural;
//	                     pay bilinmiyorsa KALIR (susturma yalnız olumlu kanıtla)
//	oran / gecikme /     klasik error_rate, p99 AYNEN → op_error_rate,
//	yapı                 op_latency, dependency_latency, pod aykırıları,
//	                     trace_change_point (p95), rare_*, template_spike,
//	                     varsayılan metrik serileri: KALIR
//
// AYNALANMAYAN: v0.10.1046 "yük ≥ 2× iken gecikme artışı yeni olay açmaz"
// alt kuralı. Karar noktasında aynı servisin istek hacmi okuması yok (trace
// hattı yük ARTIŞI adayı üretmiyor); klasik kuralın "yalnız olumlu kanıtla
// susturur" ilkesi gereği gecikme adayları kalır (DECISIONS 2026-11-05).

import (
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// batchMeasure — olayın ölçtüğü şeyin batch kuralındaki sınıfı.
type batchMeasure int

const (
	batchMeasureOther    batchMeasure = iota // oran / gecikme / yapı: batch'te de değerlendirilir
	batchMeasureLoad                         // servis yükü/hacmi (iki yön): batch'te değerlendirilmez
	batchMeasureErrCount                     // hata sayısı: batch'te yalnız pay da arttıysa
)

// batchMeasureOf — SAF: olay → ölçü sınıfı. loadParts: Load işaretli
// metrik serilerinin part'ları ("aile|id", metricLoadParts).
func batchMeasureOf(ev chstore.MLEvent, loadParts map[string]bool) batchMeasure {
	switch ev.Detector {
	case detLogVolume, detServiceSilent, detThroughputDrop:
		return batchMeasureLoad
	case detErrorRate, detTraceErrorCount: // v0.10.2018: CH hata span sayısı; pay kanıtı yok → KALIR
		return batchMeasureErrCount
	case detChangePoint:
		// log CUSUM iki seri üstünde: _total (hacim) ve _error (hata sayısı)
		if ev.Level == "error" {
			return batchMeasureErrCount
		}
		return batchMeasureLoad
	}
	if ev.Signal == chstore.MLSignalMetric && loadParts[ev.Code] {
		return batchMeasureLoad
	}
	return batchMeasureOther
}

// metricLoadParts — SAF: ayardaki Load işaretli metrik serileri.
func metricLoadParts(cfg chstore.MLConfig) map[string]bool {
	out := map[string]bool{}
	for fam, exprs := range cfg.MetricFamilies {
		for _, e := range exprs {
			if e.Load {
				out[metricPart(fam, e.ID)] = true
			}
		}
	}
	return out
}

// errShareEvidence — SAF: log error_rate olayına pay kanıtı. Toplam ya da
// taban toplamı bilinmiyorsa nil (kanıt yok).
func errShareEvidence(curErr, curTotal, baseErrMed, baseTotalMed float64) map[string]any {
	if curTotal <= 0 || baseTotalMed <= 0 {
		return nil
	}
	return map[string]any{"errShare": curErr / curTotal, "baseErrShare": baseErrMed / baseTotalMed}
}

// batchErrShareHolds — SAF: klasik traceOpBatchShareHolds'un aynası —
// cari pay ≥ 3 × taban payı. Kanıt yoksa ya da taban payı 0 ise (klasikte
// BaseErrs == 0 dalı, new_error AYNEN açılır) true.
func batchErrShareHolds(evidence string) bool {
	var e struct {
		ErrShare     *float64 `json:"errShare"`
		BaseErrShare *float64 `json:"baseErrShare"`
	}
	if evidence == "" || json.Unmarshal([]byte(evidence), &e) != nil || e.ErrShare == nil || e.BaseErrShare == nil || *e.BaseErrShare <= 0 {
		return true
	}
	return *e.ErrShare >= anomaly.BatchErrorShareFactor**e.BaseErrShare
}

// batchKeep — SAF karar: bu aday batch kuralından geçiyor mu?
// isBatch nil = kural kapalı (her şey geçer).
func batchKeep(ev chstore.MLEvent, isBatch func(string) bool, loadParts map[string]bool) bool {
	if isBatch == nil || !isBatch(ev.Service) {
		return true
	}
	switch batchMeasureOf(ev, loadParts) {
	case batchMeasureLoad:
		return false
	case batchMeasureErrCount:
		return batchErrShareHolds(ev.Evidence)
	}
	return true
}

// batchFilter — SAF: adayları süzer; kesilenleri ayrıca döner (sayaç +
// günlük tavan iadesi).
func batchFilter(evs []chstore.MLEvent, isBatch func(string) bool, loadParts map[string]bool) (kept, cut []chstore.MLEvent) {
	if isBatch == nil || len(evs) == 0 {
		return evs, nil
	}
	kept = evs[:0:0]
	for _, ev := range evs {
		if batchKeep(ev, isBatch, loadParts) {
			kept = append(kept, ev)
		} else {
			cut = append(cut, ev)
		}
	}
	return kept, cut
}

// capRefunder — dedektörün günlük olay tavanı (DailyEventCap) adayı
// üretirken harcanır; karar noktası recorder'da olduğu için batch'in
// kestiği aday tavanı GERİ verir. Yoksa uzun koşan batch işlerinin yük
// adayları gün içinde tavanı bitirip batch olmayan servislerin gerçek
// olaylarını aç bırakırdı.
type capRefunder interface{ refundCap(det string) }

// refundDaily — SAF yardımcı: sayaç sıfırın altına inmez.
func refundDaily(daily map[string]int, det string) {
	if daily[det] > 0 {
		daily[det]--
	}
}

func (c *capper) refundCap(det string) { refundDaily(c.daily, det) }

func (d *LogRED) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}

func (d *TraceRED) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}

func (d *MetricML) refundCap(det string) {
	d.mu.Lock()
	refundDaily(d.daily, det)
	d.mu.Unlock()
}
