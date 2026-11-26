package chstore

// strip_line_step.go — v0.10.2477 (operator-reported: "Traces sayfasındaki
// histogramdaki süre grafiği … daha iyi granüler olabilir. Bazen net
// göstermiyor, Dynatrace daha iyi süre çiziyor").
//
// /traces hacim şeridinin yanıt-süresi ÇİZGİSİ artık çubuklardan ayrı, daha
// ince bir çözünürlükte okunur (Dynatrace'in yanıt-süresi grafiği gibi:
// ~120–240 nokta). Şerit TEK sorgu atar — ince adımda count / errors / rt;
// çubuklar istemcide çubuk adımına toplanır (sayımlar toplanabilir, kayıp
// yok). Bu dosya ince adımın TEK kaynağıdır (metric-batch `linePoints` ve
// /api/traces/histogram `linePoints` ikisi de buradan geçer).
//
// Kurallar (SAF, tablo testli — strip_line_step_test.go):
//   - Nokta tavanı StripLineMaxPoints (240); istemci bütçesi (piksel türevi)
//     bunun altında kalabilir, üstüne çıkamaz → hiçbir zaman pikselden çok
//     nokta yok (şerit ≥ 400 px).
//   - Adım rung merdiveninden (stripLineRungs) — sınırlı kardinalite, cache
//     anahtarı disiplini (v0.8.270).
//   - Adım, pencere başını KAPSAYAN en ince dar rollup katmanının taban
//     adımının KATI ve ondan küçük değil (narrowTiers: 10s ≤7g, 1m ≤14g,
//     5m ≤90g, 1s). Böylece ince çizgi MV granülaritesinin altına inmez ve dar
//     rollup fast-path'i (pickNarrowRollupTier: step % base == 0) açık kalır —
//     ham spans taraması yalnız bugün de ham olan süzgeçlerde (arama, nitelik
//     çipi). 15 s gibi 10'un katı olmayan basamak bu yüzden merdivende YOK.

import (
	"math"
	"time"
)

// StripLineMaxPoints — şerit çizgisinin nokta tavanı (sunucu tarafı clamp).
const StripLineMaxPoints = 240

// stripLineRungs — ince çizgi adım merdiveni (sn). Hepsi 10'un katı; ≥60
// olanlar 60'ın, ≥300 olanlar 300'ün, ≥3600 olanlar 3600'ün katı (katman
// tabanlarına oturur; op-MV fast-path'i de ≥300'de 300'ün katı ister).
var stripLineRungs = []int{10, 20, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400}

// stripLineBaseSec — pencere başını (from) kapsayan EN İNCE dar rollup
// katmanının taban adımı. Hiçbiri kapsamıyorsa en kaba katmanın tabanı.
func stripLineBaseSec(from, now time.Time) int {
	age := now.Sub(from)
	for _, t := range narrowTiers { // ince → kaba sıralı
		if age <= t.retention {
			return int(t.baseSec)
		}
	}
	return int(narrowTiers[len(narrowTiers)-1].baseSec)
}

// StripLineStep — SAF: pencere + nokta bütçesi → çizgi adımı (sn).
// maxPoints ≤ 0 → 0 (çağıran kendi adımını kullanır; özellik kapalı).
func StripLineStep(from, to, now time.Time, maxPoints int) int {
	if maxPoints <= 0 {
		return 0
	}
	if maxPoints > StripLineMaxPoints {
		maxPoints = StripLineMaxPoints
	}
	base := stripLineBaseSec(from, now)
	win := to.Sub(from).Seconds()
	if win <= 0 {
		return base
	}
	raw := int(math.Ceil(win / float64(maxPoints)))
	for _, r := range stripLineRungs {
		if r >= raw && r >= base && r%base == 0 {
			return r
		}
	}
	const day = 86400
	return int(math.Ceil(float64(raw)/day)) * day
}
