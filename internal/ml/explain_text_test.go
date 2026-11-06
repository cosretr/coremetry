package ml

// explain_text_test.go — v0.10.1842 (öğrenme döngüsü Faz 2 dilim 2.2):
// ExplainCard yazım yardımcılarının sözleşmesi. Ek uyumu okunuşun son
// sözcüğünden gelir (14:30 → otuz → 'dan; 14:33 → üç → 'ten); oran
// merdiveni mockup kat() ile birebir; süre birimlerinin HEPSİ (dakika /
// saat / gün) iki biçimde (v0.6.36 birim karışması dersi).

import (
	"testing"
	"time"
)

func TestTrNumbers(t *testing.T) {
	for _, c := range []struct {
		v    float64
		dec  int
		want string
	}{
		{4800, 0, "4.800"}, {40000, 0, "40.000"}, {620, 0, "620"}, {1234567, 0, "1.234.567"},
		{2.1, 1, "2,1"}, {0.5, 1, "0,5"}, {3, 1, "3"}, {12.25, 2, "12,25"}, {1500.5, 1, "1.500,5"}, {-2.5, 1, "-2,5"},
	} {
		if got := trDec(c.v, c.dec); got != c.want {
			t.Errorf("trDec(%v,%d)=%q want %q", c.v, c.dec, got, c.want)
		}
	}
}

func TestSuffixHarmony(t *testing.T) {
	loc := time.FixedZone("TR", 3*3600)
	at := func(h, m int) time.Time { return time.Date(2026, 11, 3, h, m, 0, 0, loc) }
	for _, c := range []struct {
		got, want string
	}{
		{clockSfx(at(14, 30), sfxAbl), "14:30'dan"},
		{clockSfx(at(14, 33), sfxAbl), "14:33'ten"},
		{clockSfx(at(14, 10), sfxAbl), "14:10'dan"},
		{clockSfx(at(12, 44), sfxAbl), "12:44'ten"},
		{clockSfx(at(14, 28), sfxAbl), "14:28'den"},
		{clockSfx(at(14, 31), sfxAbl), "14:31'den"},
		{clockSfx(at(14, 40), sfxAbl), "14:40'tan"},
		{clockSfx(at(14, 0), sfxAbl), "14:00'ten"},
		{clockSfx(at(10, 0), sfxAbl), "10:00'dan"},
		{clockSfx(at(13, 58), sfxLoc), "13:58'de"},
		{clockSfx(at(14, 20), sfxLoc), "14:20'de"},
		{clockSfx(at(10, 32), sfxLoc), "10:32'de"},
		{clockSfx(at(9, 6), sfxLoc), "09:06'da"},
		{clockSfx(at(9, 3), sfxLoc), "09:03'te"},
		{numSfx(6, sfxPoss3), "6'sı"},
		{numSfx(12, sfxPoss3), "12'si"},
		{numSfx(3, sfxPoss3), "3'ü"},
		{numSfx(9, sfxPoss3), "9'u"},
		{numSfx(40, sfxPoss3), "40'ı"},
		{numSfx(1, sfxPoss3), "1'i"},
		{numSfx(6, sfxPossLoc), "6'sında"},
		{numSfx(8, sfxPossLoc), "8'inde"},
		{numSfx(3, sfxPossLoc), "3'ünde"},
		{numSfx(10, sfxPossLoc), "10'unda"},
		{trNumWithSfx("%12", sfxPoss3), "%12'si"},
		{trNumWithSfx("%0,5", sfxPoss3), "%0,5'i"},
		{trNumWithSfx("4.800", sfxPoss3), "4.800'ü"},
		{nameSfx("orders-api", sfxLoc), "orders-api'de"},
		{nameSfx("payments-mobile", sfxLoc), "payments-mobile'de"},
		{nameSfx("notify-gateway", sfxLoc), "notify-gateway'da"},
		{nameSfx("branch-teller", sfxAbl), "branch-teller'den"},
		{nameSfx("svc-2", sfxLoc), "svc-2'de"},
		{nameSfx("db", sfxLoc), "db'de"},
	} {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

func TestDurationTRAllUnits(t *testing.T) {
	for _, c := range []struct {
		d            time.Duration
		plain, since string
		short        string
	}{
		{25 * time.Minute, "25 dakika", "25 dakikadır", "25 dk"},
		{20 * time.Second, "1 dakika", "1 dakikadır", "1 dk"},
		{2 * time.Hour, "2 saat", "2 saattir", "2 sa"},
		{80 * time.Minute, "1 saat 20 dakika", "1 saat 20 dakikadır", "1 sa 20 dk"},
		{200 * time.Minute, "3 saat", "3 saattir", "3 sa"},
		{30 * time.Hour, "30 saat", "30 saattir", "30 sa"},
		{72 * time.Hour, "3 gün", "3 gündür", "3 g"},
	} {
		p, s := durationTR(c.d)
		if p != c.plain || s != c.since || durationShortTR(c.d) != c.short {
			t.Errorf("%v → %q/%q/%q want %q/%q/%q", c.d, p, s, durationShortTR(c.d), c.plain, c.since, c.short)
		}
	}
}

func TestDayWordTR(t *testing.T) {
	loc := time.FixedZone("TR", 3*3600)
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, loc)
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 11, 5, 1, 0, 0, 0, loc), "Bugün"},
		{time.Date(2026, 11, 4, 23, 0, 0, 0, loc), "Dün"},
		{time.Date(2026, 10, 12, 8, 0, 0, 0, loc), "12 Ekim"},
	} {
		if got := dayWordTR(c.t, now); got != c.want {
			t.Errorf("%v → %q want %q", c.t, got, c.want)
		}
	}
}

// Mockup kat() merdiveninin her basamağı, iki biçimde; azalma ve sıfır taban.
func TestRatioPhraseLadder(t *testing.T) {
	for _, c := range []struct {
		obs, base   float64
		dir         string
		short, long string
	}{
		{1.05, 1, "up", "normal aralıkta", "normal aralıkta"},
		{1.2, 1, "up", "biraz yüksek", "biraz yüksek"},
		{1.5, 1, "up", "yaklaşık 1,5 kat", "normalin yaklaşık 1,5 katı"},
		{1.79, 1, "up", "yaklaşık 1,5 kat", "normalin yaklaşık 1,5 katı"},
		{1.8, 1, "up", "yaklaşık 2 kat", "normalin yaklaşık 2 katı"},
		{0.12, 0.04, "up", "yaklaşık 3 kat", "normalin yaklaşık 3 katı"},
		{207, 23, "up", "yaklaşık 9 kat", "normalin yaklaşık 9 katı"},
		{20, 1, "up", "yaklaşık 20 kat", "normalin yaklaşık 20 katı"},
		{21, 1, "up", "20 kattan fazla", "normalin 20 katından fazla"},
		{40, 0, "up", "normalde hiç görülmez", "normalde hiç görülmez"},
		{0, 0, "up", "normal aralıkta", "normal aralıkta"},
		{95, 100, "down", "normal aralıkta", "normal aralıkta"},
		{80, 100, "down", "biraz düşük", "biraz düşük"},
		{60, 100, "down", "normalden yaklaşık 1,5 kat az", "normalden yaklaşık 1,5 kat az"},
		{1000, 3000, "down", "normalden yaklaşık 3 kat az", "normalden yaklaşık 3 kat az"},
		{1, 100, "down", "neredeyse durdu", "neredeyse durdu"},
		{0, 100, "down", "durdu", "durdu"},
	} {
		if got := RatioPhrase(c.obs, c.base, c.dir, RatioShort, ""); got != c.short {
			t.Errorf("short %v/%v %s → %q want %q", c.obs, c.base, c.dir, got, c.short)
		}
		if got := RatioPhrase(c.obs, c.base, c.dir, RatioLong, ""); got != c.long {
			t.Errorf("long %v/%v %s → %q want %q", c.obs, c.base, c.dir, got, c.long)
		}
	}
	if got := RatioPhrase(4, 1, "up", RatioLong, "diğer pod'ların"); got != "diğer pod'ların yaklaşık 4 katı" {
		t.Errorf("ref: %q", got)
	}
}
