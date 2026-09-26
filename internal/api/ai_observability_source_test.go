package api

// v0.10.940 (değerlendirme paneli, K2) — /ai okuma uçlarının ?source=
// sözleşmesi: yalnız tam "evalset" değerlendirme satırlarını seçer, yokluk
// ve her başka değer üretimdir (evalset DIŞARIDA). Önbellek anahtarı
// NORMALİZE kaynağı taşır: eksikse üretim sekmesi 30 sn boyunca evalset
// KPI'larını (ya da tersini) görürdü — v0.5.187'nin çapraz zehirleme
// sınıfı; normalize değilse el yazımı değerler sınırsız girdi basardı.

import (
	"net/url"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAISourceParam(t *testing.T) {
	cases := []struct {
		raw  string
		want chstore.AICallSource
	}{
		{"", chstore.AICallSourceProduction}, // parametre yok = üretim
		{"production", chstore.AICallSourceProduction},
		{"evalset", chstore.AICallSourceEvalset},
		// Katı eşitlik: FE tek yazımla gönderir, başka her şey üretime düşer.
		{"Evalset", chstore.AICallSourceProduction},
		{"EVALSET", chstore.AICallSourceProduction},
		{" evalset", chstore.AICallSourceProduction},
		{"evalset-IntentClassify", chstore.AICallSourceProduction},
		{"all", chstore.AICallSourceProduction},
	}
	for _, c := range cases {
		if got := aiSourceParam(c.raw); got != c.want {
			t.Errorf("source=%q → %q, beklenen %q", c.raw, got, c.want)
		}
	}
	// URL'den okuma yolu: anahtar hiç yoksa Get "" döner → üretim.
	if got := aiSourceParam(url.Values{}.Get("source")); got != chstore.AICallSourceProduction {
		t.Errorf("?source yokken %q", got)
	}
}

func TestAIStatsSeriesKeysPerSource(t *testing.T) {
	to := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	keyers := map[string]func(chstore.AICallSource) string{
		"ai-stats":  func(s chstore.AICallSource) string { return aiStatsKey(from, to, true, s) },
		"ai-series": func(s chstore.AICallSource) string { return aiSeriesKey(from, to, 720, s) },
	}
	for name, key := range keyers {
		prod := key(aiSourceParam(""))
		eval := key(aiSourceParam("evalset"))
		if prod == eval {
			t.Fatalf("%s: üretim ve evalset aynı anahtar %q — kaynak anahtarda yok", name, prod)
		}
		if key(aiSourceParam("evalset")) != eval {
			t.Errorf("%s: anahtar kararsız", name)
		}
		// Normalizasyon anahtarın ÖNÜNDE: tanınmayan her ham değer tek
		// üretim girdisine düşer (sınırlı kardinalite).
		for _, raw := range []string{"production", "Evalset", "garbage", "evalset-Chat"} {
			if got := key(aiSourceParam(raw)); got != prod {
				t.Errorf("%s: source=%q ayrı girdi basıyor: %q (üretim %q)", name, raw, got, prod)
			}
		}
	}
	// Mevcut ayrıştırıcılar korunur: pencere, ext ve kova hâlâ anahtarda.
	if aiStatsKey(from, to, true, chstore.AICallSourceProduction) == aiStatsKey(from, to, false, chstore.AICallSourceProduction) {
		t.Error("ai-stats: ext bayrağı anahtardan düştü")
	}
	if aiStatsKey(from, to, true, chstore.AICallSourceProduction) == aiStatsKey(from.Add(-time.Hour), to, true, chstore.AICallSourceProduction) {
		t.Error("ai-stats: pencere anahtardan düştü")
	}
	if aiSeriesKey(from, to, 720, chstore.AICallSourceProduction) == aiSeriesKey(from, to, 60, chstore.AICallSourceProduction) {
		t.Error("ai-series: kova anahtardan düştü")
	}
}
