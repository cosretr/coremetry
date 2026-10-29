package anomaly

import (
	"math"

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
	// Evidence — v0.10.1478: serinin kaynak satırı (redSourceEvidenceTR);
	// karar fazı YAZMAZ (kaynaktan bağımsız), scan iliştirir.
	Evidence string
	// OpenedBy — açılış/tazeleme kararını veren kol: "z" (ani sapma) ya da
	// "error_ramp" (sürekli teknik hata yükselişi, error_ramp.go).
	OpenedBy string
	// Ramp — error_rate'te sürekli yükseliş kuralının kararı (kanıt + açıklama
	// ucu); diğer metriklerde sıfır.
	Ramp errorRampResult
	// ZBlocker — ani-sapma kolunda pencerenin İLK açılmayan kovasını durduran
	// kapı ("" = pencerenin tamamı açılma eşiğinde). Açıklama ucu okur.
	ZBlocker string
	// Baseline — kullanılan taban: "seasonal" | "consecutive" (+ örnek sayısı).
	BaselineKind string
	BaselineN    int
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
	zOpen := allOpen
	zBlocker := ""
	if !zOpen && len(rates) == len(buckets) && windowHasSilentBucket(rates, split) {
		// v0.10.1585 — dwell penceresinde trafiksiz (ızgara dolgusu ya da
		// susan kuyruk) kova: seri seyrek, "sürekli" değil (fillDwellGrid).
		zBlocker = zBlockSparse
	} else if !zOpen {
		zBlocker = windowBlocker(metric, median, mad, window, pol, cfg.CriticalZ)
		if zBlocker == "" {
			zBlocker = "hacim kapısı (minBaselineRate) / batch yük kapısı"
		}
	}
	// Sürekli teknik hata yükselişi (error_ramp.go): ani-sapma açmadıysa
	// AÇABİLİR; açık satırı en yeni kova ihlaldeyken kapatmaz. Hacim kapısı
	// (minBaselineRate) bu kola da uygulanır — operatörün "bu hacmin altını
	// sayma" vidası.
	var ramp errorRampResult
	if metric == errorRampMetric {
		w := chstore.NormalizeErrorRamp(cfg.ErrorRamp).WindowBuckets
		ramp = errorRampCheck(buckets, rates, errorRampBaseline(buckets, seasonal, rates, w, seasonalMinSamples), cfg.ErrorRamp)
	}
	openedBy := ""
	if allOpen {
		openedBy = "z"
	} else if ramp.Holds && hasEnoughVolume(rates, pol.minBaselineRate) {
		allOpen = true
		openedBy = "error_ramp"
		cur = anomalyDecision{open: true, severity: "critical", direction: "spiked"}
	}
	action := anomalyAction(hasOpen, allOpen, metric, z)
	if action == "resolve" && ramp.Latest {
		action = "none" // oran hâlâ eşiğin üstünde: toparlanma değil (histerezis)
	}
	bkind := "consecutive"
	if usesSeasonalBaseline(seasonal, seasonalMinSamples) {
		bkind = "seasonal"
	}
	out = anomalyOutcome{
		Action:        action,
		OpenedBy:      openedBy,
		Ramp:          ramp,
		ZBlocker:      zBlocker,
		BaselineKind:  bkind,
		BaselineN:     len(baseline),
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

// windowHasSilentBucket — SAF: hizalı hacim serisinde dwell penceresinin
// (rates[split:]) trafiksiz kovası var mı. Hizasız/boş hacim serisi = bilinmiyor
// → false (gerekçe yalnız ölçülmüş sessizlikte yazılır).
func windowHasSilentBucket(rates []float64, split int) bool {
	if split < 0 || split > len(rates) {
		return false
	}
	for _, r := range rates[split:] {
		if r == 0 {
			return true
		}
	}
	return false
}

// Ani-sapma kapılarının gerekçeleri (decideAnomaly'nin sırasıyla).
const (
	zBlockDirection = "z açılma eşiğinin altında (openZ)"
	zBlockFloorPct  = "göreli değişim tabanın altında (floorPct)"
	zBlockAbsDelta  = "mutlak fark tabanın altında (minAbsDelta)"
	zBlockAbsFloor  = "değer mutlak tabanın altında (absFloor)"
	zBlockCritical  = "z critical eşiğinin altında (criticalZ)"
	zBlockFlip      = "pencere içinde yön değişti"
	zBlockSparse    = "dwell penceresinde trafiksiz kova (seyrek seri)"
)

// decideBlocker — SAF: decideAnomaly'nin İKİZİ; açmayan örnekte ilk kapının
// adı, açanda "". Sözleşme (TestDecideBlockerMatchesDecideAnomaly): blocker ==
// "" ⇔ decideAnomaly(...).open.
func decideBlocker(metric string, z, current, median float64, pol metricPolicy, criticalZ float64) string {
	dirOpen := false
	switch pol.direction {
	case "up":
		dirOpen = z >= openZ
	case "down":
		dirOpen = z <= -openZ
	default:
		dirOpen = math.Abs(z) >= openZ
	}
	if !dirOpen {
		return zBlockDirection
	}
	absDelta := math.Abs(current - median)
	if absDelta/math.Max(math.Abs(median), magnitudeEps) < pol.floorPct {
		return zBlockFloorPct
	}
	if pol.minAbsDelta > 0 && absDelta < pol.minAbsDelta {
		return zBlockAbsDelta
	}
	if z > 0 && pol.absFloor > 0 && current < pol.absFloor {
		return zBlockAbsFloor
	}
	if math.Abs(z) < criticalZ && !(metric == "request_rate" && z < 0) {
		return zBlockCritical
	}
	return ""
}

// windowBlocker — SAF: dwell penceresinde açılmayı durduran ilk kapı.
func windowBlocker(metric string, median, mad float64, window []float64, pol metricPolicy, criticalZ float64) string {
	dir := ""
	for i, v := range window {
		zv := madScale * (v - median) / mad
		if b := decideBlocker(metric, zv, v, median, pol, criticalZ); b != "" {
			return b
		}
		d := "spiked"
		if zv < 0 {
			d = "dropped"
		}
		if i == 0 {
			dir = d
		} else if d != dir {
			return zBlockFlip
		}
	}
	return ""
}
