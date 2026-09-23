// Package forecast — v0.10.901 (Dynatrace paritesi #6, dilim 1; spec Onay
// 2026-09-23): "kaç zaman sonra dolar" sorusunun TEK saf çekirdeği.
//
// Repo'da aynı OLS+R² bloğu iki kopya hâlinde yaşıyordu:
// evaluator.capacityETA (DB kapasitesi, saat) ve evaluator.diskETADays (CH
// diski, gün). Satır satır aynı matematik, farklı kapı sabitleri, "zaten
// limitte" hâlinde ZIT sözleşme (capacity: tahmin yok — eşik dalı konuşur;
// disk: 0 gün — dolu diskte susmak alarmı kaçırmaktı). Bu paket matematiği
// tek yerde toplar, kapıları Opts ile çağırandan alır ve "zaten limitte"yi
// bir DURUM (StatusAtLimit) olarak döndürür: karar çağıranda kalır, iki
// test dosyası da değişmeden geçer.
//
// KAYAN NOKTA SIRASI PİNLİ: sx/sy/sxx/sxy birikimi, den, slope, intercept,
// SSres/SStot döngüsü ve lastFit hesabı iki eski çekirdekle BİREBİR aynı
// sırada (linear_test.go içindeki legacy referansı == ile karşılaştırır).
// Eski testler kaba (capacity_eta_test.go 3.2–3.8 sa aralık kontrolü,
// selfhealth_test.go 0.001/0.0001 tolerans); sırayı "temizlemek" o
// testlerden sessizce geçer, bu yüzden pin buradadır.
//
// Eski gövdeden TEK bilinçli sapma: NaN/±Inf örnek (ingest süzmüyor; CH
// avg NaN yayar) eskiden her kapıdan geçip ok=true + ETA=NaN veriyordu
// ("projected full in ~NaNh"). Burada "geçersiz örnek" ile tahmin yok.
//
// Yeni olan yalnız ±band (inceleme turu 901): delta yöntemi — ETA =
// (limit − ŷₙ)/b'nin varyansı hem eğimin (Var b) hem son uydurulmuş
// değerin (Var ŷₙ, kaldıraç 1/n + dx²/Sxx) hem kovaryanslarının
// katkısıyla; kantil t(n−2) (küçük n'de 1.96 fazla dar). Eğim bu güvenle
// sıfırdan ayırt edilemiyorsa üst sınır +Inf ("≥ N"). Band R² kapısının
// YERİNE geçmez, ÜSTÜNE gelir — R² "uyum var mı", band "ne kadar
// belirsiz" sorusudur. Bugün hiçbir çağıran bandı Problem'e yazmıyor;
// dilim 3+ chip'e taşıyacak. Kapsama yaklaşık %95 (küçük n ve R² kapısı
// seçim yanlılığıyla biraz altı; linear_test.go Monte Carlo pini ≥%85/88).
//
// Paket hiçbir iç paketi import etmez (evaluator/anomaly/chstore bağımlılık
// yönü serbest kalsın).
package forecast

import (
	"fmt"
	"math"
)

// Point — zaman-artan sıralı bir örnek: saniye (epoch ya da göreli) + değer.
type Point struct {
	TSec int64
	V    float64
}

// Status — uydurmanın hükmü.
type Status string

const (
	// StatusNone — tahmin YOK; Result.Reason nedeni söyler.
	StatusNone Status = "none"
	// StatusOK — eğim pozitif, uyum yeterli, limit ufukta.
	StatusOK Status = "ok"
	// StatusAtLimit — regresyon doğrusunun bugünkü değeri zaten limitte ya da
	// üstünde. Çağıran karar verir: capacityETA "tahmin yok" (eşik dalı
	// konuşur), diskETADays "0 gün" (dolu diskte susma).
	StatusAtLimit Status = "at_limit"
)

// Opts — kapılar çağırandan gelir; sıfır değer = kapı yok (MinPoints için
// en az 2 nokta zorunlu: bir nokta doğru çizmez).
type Opts struct {
	MinPoints int     // n < MinPoints → none
	MinSpanS  int64   // son−ilk < MinSpanS → none
	MinR2     float64 // r² < MinR2 → none (0 = kapı yok)
	HorizonS  float64 // ETA > HorizonS → none (0 = sınırsız)
}

// Result — uydurma çıktısı. R2 kapı düşse bile (hesaplanabildiyse) dolu:
// capacityETA eskiden de "gürültülü, r²=.38" diye r²'yi döndürüyordu.
type Result struct {
	Status Status
	// Reason — StatusNone'da dolu, operatör cümlesi ("uyum zayıf (R² .38)").
	Reason string
	// ETASec — limit'e kalan saniye (StatusOK'ta > 0; AtLimit'te 0).
	ETASec float64
	// Band — yaklaşık %95 aralığı (HasBand false ise alanlar 0). ETALoSec
	// 0'a kelepçeli; ETAHiSec +Inf olabilir: eğim sıfırdan ayırt
	// edilemiyorsa üst sınır yok ("≥ N").
	HasBand  bool
	ETALoSec float64
	ETAHiSec float64
	// Uydurma ayrıntıları (tanı/tooltip).
	R2        float64
	SlopePerS float64
	Intercept float64
	LastFit   float64 // doğrunun SON noktadaki değeri — ETA buradan ölçülür
	N         int
	SpanS     int64
}

// tQuantile975 — Student t üst %2.5 kantili, df=n−2. Küçük n'de normalin
// 1.96'sı bandı fazla daraltır (df=4 → 2.78). df>30 için 2.0 (t(60)),
// df>100 için 1.96 — yaklaşıklık, kapsama pini testte.
func tQuantile975(df int) float64 {
	var table = [...]float64{
		12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228,
		2.201, 2.179, 2.160, 2.145, 2.131, 2.120, 2.110, 2.101, 2.093, 2.086,
		2.080, 2.074, 2.069, 2.064, 2.060, 2.056, 2.052, 2.048, 2.045, 2.042,
	}
	switch {
	case df < 1:
		return math.Inf(1)
	case df <= len(table):
		return table[df-1]
	case df <= 100:
		return 2.0
	}
	return 1.96
}

// Fit — SAF. points zaman-artan sıralı; limit pozitif tavan (aynı birim).
// Sıfır tahsis dışında hiçbir yan etkisi yok.
func Fit(points []Point, limit float64, o Opts) Result {
	n := len(points)
	r := Result{Status: StatusNone, N: n}
	minPts := o.MinPoints
	if minPts < 2 {
		minPts = 2
	}
	if n < minPts {
		r.Reason = fmt.Sprintf("az örnek (%d/%d)", n, minPts)
		return r
	}
	if limit <= 0 {
		r.Reason = "limit yok"
		return r
	}
	r.SpanS = points[n-1].TSec - points[0].TSec
	if r.SpanS < o.MinSpanS {
		r.Reason = fmt.Sprintf("dar aralık (%d s < %d s)", r.SpanS, o.MinSpanS)
		return r
	}
	// Doğrusal regresyon (x = ilk noktaya göre saniye — taşma önlemi).
	// SIRA PİNLİ (paket başlığı).
	x0 := points[0].TSec
	var sx, sy, sxx, sxy float64
	for _, p := range points {
		x := float64(p.TSec - x0)
		sx += x
		sy += p.V
		sxx += x * x
		sxy += x * p.V
	}
	if math.IsNaN(sy) || math.IsInf(sy, 0) || math.IsNaN(sxy) || math.IsInf(sxy, 0) {
		r.Reason = "geçersiz örnek (NaN/Inf)"
		return r
	}
	fn := float64(n)
	den := fn*sxx - sx*sx
	if den == 0 {
		r.Reason = "tüm örnekler aynı anda"
		return r
	}
	slope := (fn*sxy - sx*sy) / den
	r.SlopePerS = slope
	if slope <= 0 {
		if slope == 0 {
			r.Reason = "düz"
		} else {
			r.Reason = "azalıyor"
		}
		return r
	}
	intercept := (sy - slope*sx) / fn
	r.Intercept = intercept
	// R² = 1 − SSres/SStot.
	meanY := sy / fn
	var ssRes, ssTot float64
	for _, p := range points {
		x := float64(p.TSec - x0)
		fit := intercept + slope*x
		ssRes += (p.V - fit) * (p.V - fit)
		ssTot += (p.V - meanY) * (p.V - meanY)
	}
	if ssTot == 0 {
		r.Reason = "sabit seri"
		return r
	}
	r.R2 = 1 - ssRes/ssTot
	if o.MinR2 > 0 && r.R2 < o.MinR2 {
		r.Reason = fmt.Sprintf("uyum zayıf (R² %.2f)", r.R2)
		return r
	}
	xn := float64(points[n-1].TSec - x0)
	lastFit := intercept + slope*xn
	r.LastFit = lastFit
	if lastFit >= limit {
		r.Status = StatusAtLimit
		return r
	}
	gap := limit - lastFit
	r.ETASec = gap / slope
	if o.HorizonS > 0 && r.ETASec > o.HorizonS {
		r.Reason = fmt.Sprintf("ufuk dışı (%.0f s > %.0f s)", r.ETASec, o.HorizonS)
		r.ETASec = 0 // sözleşme: ETASec yalnız StatusOK'ta anlamlı (sayı Reason'da)
		return r
	}
	r.Status = StatusOK
	// ±band (delta yöntemi, paket başlığı). n−2 serbestlik → n ≥ 3 şart;
	// Sxx = sxx − sx²/n (merkezlenmiş kareler toplamı).
	if n >= 3 {
		sxxC := sxx - sx*sx/fn
		if sxxC > 0 {
			s2 := ssRes / float64(n-2)
			varB := s2 / sxxC
			dx := xn - sx/fn // son nokta − x̄ (≥ 0: seri artan sıralı)
			varY := s2 * (1/fn + dx*dx/sxxC)
			cov := s2 * dx / sxxC
			q := tQuantile975(n - 2)
			r.HasBand = true
			varETA := varY/(slope*slope) + gap*gap*varB/(slope*slope*slope*slope) + 2*gap*cov/(slope*slope*slope)
			if varETA < 0 {
				varETA = 0
			}
			half := q * math.Sqrt(varETA)
			r.ETALoSec = math.Max(0, r.ETASec-half)
			if slope-q*math.Sqrt(varB) <= 0 {
				r.ETAHiSec = math.Inf(1) // eğim sıfırdan ayırt edilemiyor → üst sınır yok
			} else {
				r.ETAHiSec = r.ETASec + half
			}
		}
	}
	return r
}

// Wide — bandın "dürüst kısa hâl" eşiği: üst sınır yok, alt sınır sıfıra
// kelepçelendi ya da üst/alt > 3. Chip bu durumda "≈ N" yerine "N+" yazar
// (dilim 3+; burada sözleşme).
func (r Result) Wide() bool {
	if !r.HasBand {
		return false
	}
	if math.IsInf(r.ETAHiSec, 1) || r.ETALoSec <= 0 {
		return true
	}
	return r.ETAHiSec/r.ETALoSec > 3
}
