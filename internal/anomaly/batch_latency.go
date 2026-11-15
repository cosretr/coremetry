// batch_latency.go — v0.10.1046: batch servislerde YÜK ALTINDAKİ gecikme
// artışı YENİ anomali açmaz.
//
// Operatör (prod): "Batch servislerde yük altındaki gecikme artışı da
// anomali sayılmasın." (v0.10.1039'un devamı: o sürüm yükün KENDİSİNİ
// susturmuş, gecikmeyi bilinçli olarak dışarıda bırakmıştı.)
//
// KURAL — tek cümle: batch kalıbına giren bir serviste gecikme sapması,
// AYNI ANDA ölçülen istek hacmi işin OLAĞAN ÇALIŞMA hacminin en az
// batchLoadSurgeFactor katıyken yeni olay AÇMAZ. Tek sabit, tek karar noktası
// (v0.10.2087: trace_op_latency, v0.10.2270: davranış motoru ile birlikte
// diğer noktalar silindi):
//
//	metrik dedektörü  (verdict.go)     p99_ms dwell kovalarının HER BİRİNİN istek
//	                                   hızı ≥ 2 × p99 tabanının KENDİ kovalarının
//	                                   istek hızı medyanı (ardışık ya da mevsimsel
//	                                   slot; batchLatMetricUnderLoad)
//
// YALNIZ AÇILIŞI KESER — "zaten aktif" olana kapı UYGULANMAZ: açık
// (open/acknowledged) anomaly:<svc>:p99_ms satırı varsa (hasOpen) kapı
// koşmaz: tazelenir, kapanır, küme kaynağı olur. Ayrı bir kapatma geçişi YOK.
//
// YALNIZ OLUMLU KANITLA SUSTURUR: taban hacmi bilinmiyorsa (sıfır, yok,
// yetersiz kova, hizasız seri) kural susturmaz — bugünkü gibi açılır. Yük
// ARTMADAN (olağan çalışma hacminde) gelen gecikme artışı batch serviste de
// AYNEN açılır. Orantı denetimi YOK: 2× yükle gelen 50× gecikme de susar
// (DECISIONS v0.10.1046 "Bedeller"). error_rate, request_rate'in v0.10.1039
// kuralı, batch olmayan servisler ve kural kapalıyken (boş kalıp listesi ya
// da doğrulanmamış ayar) hiçbir şey değişmez.
package anomaly

import "math"

// batchLoadSurgeFactor — "yük sıçraması" eşiği: cari hacim olağan çalışma
// hacminin EN AZ bu katı.
const batchLoadSurgeFactor = 2

// batchLatencyMetric — kuralın dokunduğu TEK servis metriği. Liste değil
// sabit (batchLoadMetric gibi): error_rate'i kapsamak gerçek bir hatayı
// susturmak olurdu.
const batchLatencyMetric = "p99_ms"

// batchLoadSurge — SAF yardımcı (medyanla kıyaslayan metrik dedektörü). cur ≥ batchLoadSurgeFactor × base mı?
//
// base BİLİNMİYORSA (≤ 0, NaN, +Inf) → false: kanıt yok = susturma yok.
// NaN cur da false döner (NaN karşılaştırması).
func batchLoadSurge(cur, base float64) bool {
	if !(base > 0) || math.IsInf(base, 1) {
		return false
	}
	return cur >= batchLoadSurgeFactor*base
}

// batchRatesUnderLoad — dwell penceresinin HER kovasının istek hızı, taban
// kovalarının istek hızı MEDYANININ ≥ 2 katı mı?
//
// "Her kova" bilinçli olarak sıkı: p99 kararı da pencerenin TAMAMI
// ateşlemeden açılmıyor (evalWindow allOpen); yükün açıklamadığı tek bir
// ateşleyen kova varsa gecikme yükle açıklanamıyor demektir → açılır.
// Boş pencere ya da boş taban → bilinmiyor → false.
func batchRatesUnderLoad(windowRates, baseRates []float64) bool {
	if len(windowRates) == 0 || len(baseRates) == 0 {
		return false
	}
	base := medianOf(baseRates)
	for _, r := range windowRates {
		if !batchLoadSurge(r, base) {
			return false
		}
	}
	return true
}

// batchLatSeries — evaluateAnomaly'nin batch gecikme kapısı girdisi.
// Sıfır değer = kapı yok (dış seriler, batch olmayan servis).
type batchLatSeries struct {
	// Batch — servis batch kalıbına giriyor mu (scan: sens.IsBatchService).
	Batch bool
	// SeasonalRates — mevsimsel okumanın istek hızı kolonu (aynı satırlar,
	// seasonal değer serisiyle hizalı). Yoksa / hizasızsa mevsimsel tabanda
	// hacim bilinmiyor → susturma yok.
	SeasonalRates []float64
}

// batchLatMetricUnderLoad — metrik dedektörü (p99_ms) SAF kapısı: dwell
// kovalarının (rates[split:]) hızı, p99 tabanının KENDİ kovalarının hız
// medyanının ≥ 2 katı mı?
//
//	p99 tabanı mevsimsel → seasonalRates (aynı slot ±komşu, 14 gün; aynı okuma)
//	p99 tabanı ardışık   → rates[:nConsecutive] (24 sa, kuyruk sessizliği kırpılmış)
//
// Böylece her gün aynı saatte koşan bir işin OLAĞAN yükü sıçrama sayılmaz
// (mevsimsel slot o koşuları içerir); yalnız olağanın 2 katı yük susturur.
// Hizasız seri → bilinmiyor → false.
func batchLatMetricUnderLoad(rates []float64, split, nConsecutive int, seasonalUsed bool, seasonal, seasonalRates []float64) bool {
	if split <= 0 || split >= len(rates) {
		return false
	}
	var base []float64
	if seasonalUsed {
		if len(seasonalRates) != len(seasonal) {
			return false
		}
		base = seasonalRates
	} else {
		if nConsecutive <= 0 || nConsecutive > split {
			return false
		}
		base = rates[:nConsecutive]
	}
	return batchRatesUnderLoad(rates[split:], base)
}
