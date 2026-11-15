package api

// cache_live_key.go — v0.10.2228 (Redis R2, denetim O1;
// docs/architecture/redis-usage-audit.md).
//
// Zaman kovalı anahtarlar (cacheBucket, cacheRawQuery) `to=now` pencerelerde
// her 30 sn'de yeni bir anahtar üretiyordu; SWR yalnız AYNI anahtarda çalıştığı
// için canlı görünümlerde her 30 sn'de bir bloklayan miss vardı ve 5 dk TTL'li
// anahtarlar (clusters, namespaces) da 30 sn'de bir yeniden hesaplanıyordu.
//
// Kural: `to` şimdiye cacheLiveSlack kadar yakınsa anahtara mutlak zaman
// yerine `live:<süre>` girer (süre = to−from, 30 sn ızgaraya yuvarlı).
// Geçmiş (mutlak) pencere eskisi gibi 30 sn ızgaraya hizalı mutlak anahtar.
// Yükleyici yine isteğin GERÇEK penceresini kullanır; SWR tazelemesi onu
// tetikleyen (taze) isteğin penceresiyle koşar. Bayatlık sınırı K2: en çok
// staleFactor × TTL (30 sn TTL → 90 sn). `?refresh=1` taze yolu korur.
//
// Tüm girdiler anahtarda: canlı pencerenin girdisi "son <süre>"dir; süre
// anahtarda, `to` "şimdi" anlamıyla değişti (CLAUDE.md anahtar kuralı).

import (
	"fmt"
	"time"
)

const (
	cacheKeyGrid = 30 * time.Second
	// cacheLiveSlack — `to` şimdiden bu kadar uzaksa (geçmişte ya da tarayıcı
	// saat kayması ile gelecekte) pencere canlı değil. 2 × ızgara: ağ gecikmesi
	// ve birkaç saniyelik saat kayması canlı sayılır; dakikalar mutlak kalır.
	cacheLiveSlack = 2 * cacheKeyGrid
)

// cacheLiveWindow — SAF: (to, now) canlı mı; canlıysa süre parçası.
func cacheLiveWindow(from, to, now time.Time) (string, bool) {
	d := now.Sub(to)
	if d < -cacheLiveSlack || d > cacheLiveSlack || !from.Before(to) {
		return "", false
	}
	return to.Sub(from).Round(cacheKeyGrid).String(), true
}

// cacheBucketAt — SAF çekirdek: canlı → "live:<süre>", değilse from/to 30 sn
// ızgaraya hizalı mutlak "<from>_<to>".
func cacheBucketAt(from, to, now time.Time) string {
	if dur, ok := cacheLiveWindow(from, to, now); ok {
		return "live:" + dur
	}
	fb := from.Truncate(cacheKeyGrid).UnixNano()
	tb := to.Truncate(cacheKeyGrid).UnixNano()
	return fmt.Sprintf("%d_%d", fb, tb)
}

// cacheBucket — from/to için anahtar parçası (bkz. dosya başı). v0.10.2228
// öncesi her zaman mutlak 30 sn kovaydı.
func cacheBucket(from, to time.Time) string { return cacheBucketAt(from, to, time.Now()) }
