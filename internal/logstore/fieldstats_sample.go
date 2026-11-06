package logstore

// fieldstats_sample.go — v0.10.1852 (Log Search Faz 2 dilim 2.1; audit
// docs/logs/log-search-audit.md §3.2 "Alan en sık değerler", §5 risk #1).
//
// Alan paneli üst değerleri bugün pencerenin TAMAMINI tarayan bir terms agg.
// Milyar dokümanlık pencerede bu, bir alan açmanın bedelidir. Çözüm ES
// `random_sampler` (≥ 8.2): terms agg rastgele bir alt kümede koşar, yüzdeler
// aynı alt kümeden hesaplanır (pay + payda aynı örnek → oran tutarlı).
//
//	p = min(0.5, 200.000 / pencere toplamı), basamağa yuvarlı (aşağıda)
//	toplam < 200.000 → örneklemesiz (bugünkü yol, bayt-bayt aynı gövde)
//
// Toplam istemciden gelir (`est` = histogramın pencere toplamı, aynı süzgeç):
// sıfır ek ES isteği. `est` yoksa örnekleme yok. Olasılık BASAMAKLI — önbellek
// anahtarı `est` değil basamak taşır (kardinalite sınırlı, v0.8.270 sınıfı).
//
// Geri düşüş (brief: örneklemesiz yol): ES < 8.2 (ya da OpenSearch — sürüm 1.x/
// 2.x) bağlantıda okunan sürümden bilinir; sürüm okunamadıysa denenir ve ES
// gövdeyi `random_sampler` gerekçesiyle reddederse süreç ömrü boyunca kapanır
// (pitDenied emsali) ve aynı istek örneklemesiz tekrarlanır. CH backend'inde
// yetenek yok → davranış aynı (FieldStatsSampler uygulanmaz).

import (
	"context"
	"strconv"
	"strings"
)

// FieldStatsSampleTarget — örneklemin hedef doküman sayısı (audit §3.2).
const FieldStatsSampleTarget int64 = 200_000

// fieldStatsSampleSeed — sabit tohum: aynı pencere + süzgeç aynı alt kümeyi
// seçer (ES request_cache tekrarı ücretsiz kalır, yenileme sayılar oynamaz).
const fieldStatsSampleSeed = 1852

// fieldStatsSampleRungs — olasılık basamakları, büyükten küçüğe. random_sampler
// 0 < p ≤ 0.5 (ya da tam 1) kabul eder; tavan 0.5.
var fieldStatsSampleRungs = []float64{0.5, 0.2, 0.1, 0.05, 0.02, 0.01, 0.005, 0.002, 0.001, 0.0005, 0.0002, 0.0001}

// FieldStatsSampleProbability — SAF: pencere toplamı → örnekleme olasılığı.
// 0 = örneklemesiz. Basamak: hedefi karşılayan EN KÜÇÜK basamak (örnek ≥ hedef,
// en ucuz); oran en küçük basamağın da altındaysa en küçük basamak.
func FieldStatsSampleProbability(est int64) float64 {
	if est < FieldStatsSampleTarget {
		return 0
	}
	want := float64(FieldStatsSampleTarget) / float64(est)
	p := fieldStatsSampleRungs[0]
	for _, r := range fieldStatsSampleRungs {
		if r < want {
			break
		}
		p = r
	}
	return p
}

// FieldStatsSampler — isteğe bağlı backend yeteneği (FieldStatsMultier emsali):
// p > 0 ise örneklemli üst değerler. Uygulamayan backend (CH) için çağıran
// düz FieldStats'e düşer. Sonuç Sampled/Probability taşır; geri düşüşte
// Sampled=false (yanıt dürüst: "~" etiketi yalnız gerçekten örneklenince).
type FieldStatsSampler interface {
	FieldStatsSampled(ctx context.Context, f Filter, field string, limit int, p float64) (*FieldStatsResult, error)
}

// esSupportsRandomSampler — SAF: bağlantıda okunan `version.number` → yetenek.
// Boş / okunamayan sürüm → true (dene; ret gelirse kapanır). OpenSearch 1.x/2.x
// ana sürümü 8'in altında → false.
func esSupportsRandomSampler(version string) bool {
	v := strings.TrimSpace(version)
	if v == "" {
		return true
	}
	parts := strings.SplitN(v, ".", 3)
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return true
	}
	minor := 0
	if len(parts) > 1 {
		m := parts[1]
		if i := strings.IndexFunc(m, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
			m = m[:i]
		}
		minor, _ = strconv.Atoi(m)
	}
	return major > 8 || (major == 8 && minor >= 2)
}

// isRandomSamplerRejection — SAF: ES reddinin gerekçesi random_sampler mı
// (eski sürüm "Unknown aggregation type [random_sampler]", lisans / yetenek
// reddi). Yalnız örneklemli gövdenin hatasına bakılır.
func isRandomSamplerRejection(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "random_sampler")
}

// FieldStatsSampled — ESStore'un FieldStatsSampler uygulaması.
func (s *ESStore) FieldStatsSampled(ctx context.Context, f Filter, field string, limit int, p float64) (*FieldStatsResult, error) {
	if p <= 0 || p > 0.5 || !esSupportsRandomSampler(s.esVersion) || s.samplerDenied.Load() {
		return s.fieldStats(ctx, f, field, limit, 0)
	}
	res, err := s.fieldStats(ctx, f, field, limit, p)
	if isRandomSamplerRejection(err) {
		s.samplerDenied.Store(true)
		return s.fieldStats(ctx, f, field, limit, 0)
	}
	return res, err
}
