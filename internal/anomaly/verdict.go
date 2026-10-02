package anomaly

import (
	"github.com/cilcenk/coremetry/internal/chstore"
)

// verdict.go — checkOne'ın KARAR fazının saf çıkarımı (v0.9.1068,
// F1.6-R1 / kümeleme spec dilim 1). Davranış birebir: bu dosya yalnız
// v0.8.507→v0.9.1052 arasında checkOne gövdesinde yaşayan karar
// mantığını taşır; yan etkiler (Upsert/notify/incident/log) checkOne'da
// kalır. Ayrımın amacı kümeleme (spec dilim 2-3): scan() önce TÜM
// (servis, metrik) kararlarını toplayabilsin, sonra topoloji-bağlantılı
// open'ları tek Problem'e katlayabilsin. Saf olduğu için tablo-testli.

// anomalyOutcome — bir (servis, metrik) değerlendirmesinin kararı.
type anomalyOutcome struct {
	Action    string // "open" | "resolve" | "none" | "skip"
	Severity  string
	Direction string
	Current   float64
	Median    float64
	MAD       float64
	Z         float64
	Dwell     int
	// LatestHasData — son kovada gerçek trafik var mı (padlenmiş sıfır
	// değil). Resolve gerekçe dürüstlüğü (v0.9.1051) bunu okur.
	LatestHasData bool
}

// evaluateAnomaly — karar fazı. checkOne'ın v0.9.1052 hâlindeki
// sırayla birebir: yeterlilik kapıları (skip), dwell penceresi,
// baseline seçimi (mevsimsel > kırpılmış ardışık), modified z,
// hacim kapısı (yalnız açılmaya), anomalyAction.
//
// v0.10.1046 — bl: batch gecikme kapısının girdisi (scan: servis batch mi +
// mevsimsel okumanın hacim kolonu; dış seriler sıfır değer = kapı yok).
// Yalnız p99_ms'te ve yalnız AÇILIŞTA okunur (aşağıdaki batch yük kapısı).
func evaluateAnomaly(
	metric string,
	buckets, seasonal, rates []float64,
	seasonalMinSamples int,
	hasOpen bool,
	bl batchLatSeries,
	cfg chstore.AnomalySensitivityConfig,
) anomalyOutcome {
	out := anomalyOutcome{Action: "skip"}
	pol := policyFor(metric, cfg)
	dwell := cfg.DwellBuckets
	if !enoughHistory(len(buckets), dwell) {
		return out // not enough history + a full dwell window yet
	}
	split := len(buckets) - dwell
	window := buckets[split:]
	current := buckets[len(buckets)-1]

	// v0.9.1052 (Q3) — padlenmiş sıfırlar baseline'a giremez; yalnız
	// kuyruk koşusu kırpılır (gerekçe trimTrailingSilent başlığında).
	consecutive := trimTrailingSilent(buckets[:split], rates)
	if len(consecutive) < minSamples {
		return out // baseline'ın canlı kısmı karar için çok kısa
	}
	baseline := chooseBaseline(seasonal, consecutive, seasonalMinSamples)

	median, rawMAD := medianMAD(baseline)
	mad := effectiveMAD(metric, median, rawMAD, pol.minMAD)
	z := madScale * (current - median) / mad

	allOpen, _, cur := evalWindow(metric, median, mad, window, pol, cfg.CriticalZ)
	// v0.9.826 — hacim kapısı yalnız AÇILMAYA (gerekçe checkOne'daki
	// orijinal blokta; çözülmeye uygulamak donmuş-kuyruk sınıfını geri
	// getirirdi).
	if allOpen && !hasEnoughVolume(rates, pol.minBaselineRate) {
		allOpen = false
	}
	// v0.10.1046 — batch: YÜK ALTINDAKİ gecikme artışı YENİ problem açmaz
	// (operatör: "Batch servislerde yük altındaki gecikme artışı da anomali
	// sayılmasın"). Kıyas p99 kararıyla AYNI kovalar: dwell kovalarının
	// istek hızı (aynı sorgunun `rate` kolonu) vs p99 tabanının KENDİ
	// kovalarının istek hızı medyanı — mevsimsel tabanda aynı mevsimsel
	// okumanın `rate` kolonu (işin o saatteki olağan çalışma hacmi), ardışık
	// tabanda kırpılmış ardışık kovalar; her dwell kovası ≥ 2×
	// (batchLatMetricUnderLoad). Ek okuma yok.
	//
	// Bilinçli sınırlar (aşırı susturma en kötü sonuç):
	//   - yalnız !hasOpen: açık satırın tazelenmesi/kapanması/küme kaynağı
	//     olması değişmez (yalnız AÇILIŞI keser);
	//   - hacim serisi değer serisiyle hizalı değilse (ardışık ya da
	//     mevsimsel) → bilinmiyor → açılır.
	if allOpen && !hasOpen && bl.Batch && metric == batchLatencyMetric && len(rates) == len(buckets) &&
		batchLatMetricUnderLoad(rates, split, len(consecutive),
			usesSeasonalBaseline(seasonal, seasonalMinSamples), seasonal, bl.SeasonalRates) {
		allOpen = false
	}
	out = anomalyOutcome{
		Action:        anomalyAction(hasOpen, allOpen, metric, z),
		Severity:      cur.severity,
		Direction:     cur.direction,
		Current:       current,
		Median:        median,
		MAD:           mad,
		Z:             z,
		Dwell:         dwell,
		LatestHasData: len(rates) > 0 && rates[len(rates)-1] > 0,
	}
	return out
}
