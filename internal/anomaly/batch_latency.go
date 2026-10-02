// batch_latency.go — v0.10.1046: batch servislerde YÜK ALTINDAKİ gecikme
// artışı YENİ anomali açmaz.
//
// Operatör (prod): "Batch servislerde yük altındaki gecikme artışı da
// anomali sayılmasın." (v0.10.1039'un devamı: o sürüm yükün KENDİSİNİ
// susturmuş, gecikmeyi bilinçli olarak dışarıda bırakmıştı.)
//
// KURAL — tek cümle: batch kalıbına giren bir serviste gecikme sapması,
// AYNI ANDA ölçülen istek hacmi işin OLAĞAN ÇALIŞMA hacminin en az
// batchLoadSurgeFactor katıyken yeni olay AÇMAZ. Tek sabit, üç karar noktası:
//
//	trace_op_latency  (op_latency.go)  çiftin cari kova başına çağrısı ≥ 2 × taban
//	                                   penceresinin AKTİF kova başına çağrısı
//	                                   (batchLoadSurgeCounts; SQL HAVING + Go kemeri)
//	metrik dedektörü  (verdict.go)     p99_ms dwell kovalarının HER BİRİNİN istek
//	                                   hızı ≥ 2 × p99 tabanının KENDİ kovalarının
//	                                   istek hızı medyanı (ardışık ya da mevsimsel
//	                                   slot; batchLatMetricUnderLoad)
//	davranış motoru   (behavior_scan)  p99_ms penceresinin HER diliminin hacmi ≥
//	                                   2 × kendi haftanın-saati kovasının hacim medyanı
//
// YALNIZ AÇILIŞI KESER — "zaten aktif" olana kapı UYGULANMAZ, üç noktada da:
//
//	metrik dedektörü  açık (open/acknowledged) anomaly:<svc>:p99_ms satırı varsa
//	                  (hasOpen) kapı koşmaz: tazelenir, kapanır, küme kaynağı olur
//	trace_op_latency  o (servis, operasyon) için trace_op_latency olayı son 15 dk
//	                  içinde yazılmışsa (opLatActiveAge = 10 dk aktiflik + bir 5 dk
//	                  kova) çift muaf — SQL'de NOT IN, Go'da küme
//	davranış motoru   servisin behavior_change p99_ms olayı AKTİFSE (son yazım
//	                  ≤ 10 dk; her taramada yeniden yazılır) aday kalır
//
// Aktif küme okunamazsa o tik kapı HİÇ uygulanmaz (süzgeç okunamıyorsa
// süzme). Ayrı bir kapatma geçişi YOK.
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
// hacminin EN AZ bu katı. Tek sabit, üç karar noktası; tamsayı (trace_op_latency
// kolu onu SQL'le birebir aynı tamsayı aritmetiğinde kullanır).
const batchLoadSurgeFactor = 2

// batchLatencyMetric — kuralın dokunduğu TEK servis metriği. Liste değil
// sabit (batchLoadMetric gibi): error_rate'i kapsamak gerçek bir hatayı
// susturmak olurdu.
const batchLatencyMetric = "p99_ms"

// batchLoadSurge — SAF yardımcı (ondalık kol: medyanla kıyaslayan metrik
// dedektörü ve davranış motoru). cur ≥ batchLoadSurgeFactor × base mı?
//
// base BİLİNMİYORSA (≤ 0, NaN, +Inf) → false: kanıt yok = susturma yok.
// NaN cur da false döner (NaN karşılaştırması).
func batchLoadSurge(cur, base float64) bool {
	if !(base > 0) || math.IsInf(base, 1) {
		return false
	}
	return cur >= batchLoadSurgeFactor*base
}

// batchLoadSurgeCounts — batchLoadSurge'ün TAMSAYI ikizi (trace_op_latency):
// cari pencerenin kova başına çağrısı ≥ 2 × taban penceresinin AKTİF kova
// başına çağrısı mı? Bölmesiz çapraz çarpım:
//
//	curCalls × baseBuckets ≥ 2 × baseCalls × curBuckets
//
// Tamsayı olması bilinçli: SQL ikizi (opLatencyQuery HAVING) aynı UInt64
// aritmetiğini yapar → Go ile SQL eşikte bile birebir aynı karar verir.
// Taban bilinmiyorsa (aktif kova 0, çağrı 0) ya da cari kova sayısı 0 → false.
func batchLoadSurgeCounts(curCalls, curBuckets, baseCalls, baseBuckets uint64) bool {
	if baseBuckets == 0 || baseCalls == 0 || curBuckets == 0 {
		return false
	}
	return curCalls*baseBuckets >= batchLoadSurgeFactor*baseCalls*curBuckets
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

// behaviorWindowUnderLoad — davranış motorunun p99_ms adayı için SAF kapı:
// adayın penceresindeki (son c.Dwell dilim — evalBehaviorWindow'un puanladığı
// dilimlerin ta kendisi) HER dilimin hacmi, KENDİ haftanın-saati kovasının
// hacim medyanının ≥ 2 katı mı? volBase = splitBehaviorSeries(rows,
// "request_rate", …) — p99 tabanıyla aynı satırlar, aynı kova, aynı kesim.
//
// Kova yoksa / yetersizse (behaviorSufficient) / medyan sıfırsa → bilinmiyor
// → false (aday kalır).
func behaviorWindowUnderLoad(c behaviorCandidate, recent []behaviorRow, volBase map[int]behaviorBucket, b behaviorConfigView) bool {
	if c.Dwell <= 0 || len(recent) < c.Dwell {
		return false
	}
	for _, r := range recent[len(recent)-c.Dwell:] {
		vb, ok := volBase[r.HOW]
		if !ok || !behaviorSufficient(vb, b) {
			return false
		}
		if !batchLoadSurge(behaviorMetricValue("request_rate", r), medianOf(vb.Values)) {
			return false
		}
	}
	return true
}

// behaviorBatchLatencyGate — batch servisin p99_ms adayına kapı (SAF).
// Dönen (c, true) yazılır — c DAİMA değiştirilmeden (saf susturma);
// (_, false) aday DEĞİL.
//
// Sıra:
//  1. Yalnız YUKARI yönlü aday susabilir (gecikme ARTIŞI).
//  2. Rejim adayı yük altındaysa mevsimsel pencere AYRICA sorulur:
//     DwellSeasonal > DwellRegime ayarında mevsimsel pencere yükün
//     açıklamadığı dilim taşıyabilir — o zaman sinyal yükle açıklanamıyor,
//     aday (rejim, alanları aynen) KALIR.
//  3. Susturmadan hemen önce `active` sorulur (TEMBEL okuma burada tetiklenir):
//     servisin p99 davranış olayı zaten AKTİFSE ya da aktif küme
//     okunamadıysa aday kalır. nil `active` = bilinmiyor = aday kalır.
func behaviorBatchLatencyGate(
	c behaviorCandidate,
	baseline map[int]behaviorBucket,
	recent []behaviorRow,
	volBase map[int]behaviorBucket,
	pol metricPolicy,
	b behaviorConfigView,
	active func(service string) bool,
) (behaviorCandidate, bool) {
	if c.Direction != "up" || !behaviorWindowUnderLoad(c, recent, volBase, b) {
		return c, true
	}
	if c.Signal == "regime" {
		s, ok := evalBehaviorWindow(c.Service, c.Metric, baseline, recent, pol, b, "seasonal", b.DwellSeasonal)
		if ok && (s.Direction != "up" || !behaviorWindowUnderLoad(s, recent, volBase, b)) {
			return c, true
		}
	}
	if active == nil || active(c.Service) {
		return c, true
	}
	return behaviorCandidate{}, false
}
