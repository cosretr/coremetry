package logstore

import (
	"context"
	"strings"
	"time"
)

// pattern_insights.go — v0.10.1896 (Log Search Faz 4 dilim 4.2; audit §3.2
// "İki aralık" / "İlk kez görüldü"). /logs Desenler sekmesinin iki ek ES
// sorusu, ikisi de desen başına TEK `_msearch` içinde:
//
//   - PatternTrends: desenin pencere içindeki eğilim çizgisi (size:0 +
//     date_histogram, ≤ PatternInsightsMax alt arama).
//   - PatternsExist: verilen pencerede desene uyan EN AZ BİR satır var mı
//     (size:0, terminate_after:1, en çok 1'e kadar sayım). "İlk kez görüldü"
//     (pencereden geriye 7 gün) ve iki aralık karşılaştırmasının
//     "yeni / kaybolan" doğrulaması (v0.10.1897) bunu kullanır.
//
// Eşleşme yüklemi = desen satırının "Ara" düğmesinin yazdığı sorgu
// (PatternSearchQuery, tırnaklı parçalar AND) + sayfanın süzgeci: sayım,
// operatörün tıklayınca göreceği kümeyle aynıdır (yaklaşık; şablonun
// literal parçaları).
//
// Yalnız Elasticsearch uygular (prod log yolu ES; CH log yoluna emek yok —
// operatör 2026-11-06). CH'de arayüz yok → çağıran "bu log arka ucunda yok"
// der, sessizce boş dönmez.

// PatternInsightsMax — tek `_msearch`'teki en çok alt arama (ES maliyet sınırı).
const PatternInsightsMax = 20

// PatternInsightTimeout — alt arama başına ES yumuşak zaman aşımı (≤ 2 sn).
const PatternInsightTimeout = "2s"

// PatternTrend — bir desenin kovaları (pencere başından, eşit aralıklı, boş
// kova 0). Partial: ES zaman aşımı / shard hatası — kovalar alt küme.
// Failed: alt arama hata döndü — Points boş.
type PatternTrend struct {
	Points  []int64
	Partial bool
	Failed  bool
}

// PatternProbe — bir varlık yoklaması: Query desen sorgusu, [From, To) pencere.
type PatternProbe struct {
	Query string
	From  time.Time
	To    time.Time
}

// ExistState — yoklama sonucu. Unknown = ölçülemedi (hata, zaman aşımı ya da
// shard hatası ile sıfır): "yok" DENMEZ.
type ExistState string

const (
	ExistFound   ExistState = "found"
	ExistAbsent  ExistState = "absent"
	ExistUnknown ExistState = "unknown"
)

// PatternInsighter — ES'e özgü desen soruları (logstore.Unwrap ile alınır).
type PatternInsighter interface {
	PatternTrends(ctx context.Context, f Filter, queries []string, bucketSec int) ([]PatternTrend, error)
	PatternsExist(ctx context.Context, f Filter, probes []PatternProbe) ([]ExistState, error)
}

// CombinePatternSearch — SAF: sayfanın serbest metni ile desen sorgusu;
// "⊕ mevcut aramaya ekle" ile aynı biçim. Boş desen → "" (sorgu atılmaz).
func CombinePatternSearch(search, pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return ""
	}
	search = strings.TrimSpace(search)
	if search == "" {
		return pattern
	}
	return "(" + search + ") AND (" + pattern + ")"
}

// patternTrendRungs — eğilim kovası basamakları (sn). Kova sayısı ≤ 30 →
// date_histogram küçük kalır; basamaklı → aynı pencere aynı gövde (ES istek
// önbelleği) ve sınırlı anahtar kardinalitesi.
var patternTrendRungs = []int{60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400}

const patternTrendMaxBuckets = 30

// PatternTrendBucketSec — SAF: pencere uzunluğu → kova (sn), en ince basamak
// ki kova sayısı ≤ 30. Çok uzun pencerede son basamak (1 gün).
func PatternTrendBucketSec(window time.Duration) int {
	sec := int(window / time.Second)
	for _, r := range patternTrendRungs {
		if sec <= r*patternTrendMaxBuckets {
			return r
		}
	}
	return patternTrendRungs[len(patternTrendRungs)-1]
}

// TrendPoints — SAF: seyrek (kova başı ms → sayı) haritasını [from, to)
// penceresinin eşit kovalarına yayar; boş kova 0. Kova hizası ES
// date_histogram'ınkiyle aynı (epoch'tan bucketSec katları).
func TrendPoints(byKeyMs map[int64]int64, from, to time.Time, bucketSec int) []int64 {
	if bucketSec <= 0 || !to.After(from) {
		return nil
	}
	step := int64(bucketSec) * 1000
	start := from.UnixMilli() / step * step
	end := to.UnixMilli()
	var out []int64
	for k := start; k < end && len(out) < 400; k += step {
		out = append(out, byKeyMs[k])
	}
	return out
}

// PatternInsightsMeta — v0.10.1896: Desenler yanıtındaki ek soruların durum
// zarfı. Trend: ok | partial | error | unsupported. FirstSeen: ok | partial |
// error | unsupported | no-service | no-baseline | none (yoklanacak desen yok).
type PatternInsightsMeta struct {
	BucketSec    int    `json:"bucketSec"`
	FromNs       int64  `json:"fromNs"`
	ToNs         int64  `json:"toNs"`
	Trend        string `json:"trend"`
	FirstSeen    string `json:"firstSeen"`
	LookbackDays int    `json:"lookbackDays"`
}
