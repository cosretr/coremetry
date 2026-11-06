package ml

// explain_text.go — v0.10.1842, öğrenme döngüsü Faz 2 dilim 2.2 (audit
// docs/anomaly/learning-loop-audit.md §7.3): ExplainCard'ın Türkçe yazım
// yardımcıları. Hepsi SAF ve tablo testli (explain_text_test.go).
//
//   - sayı: binlik nokta, ondalık virgül ("4.800", "2,1")
//   - ek uyumu: "14:30'dan", "14:33'ten", "13:58'de", "%12'si",
//     "6'sında", "orders-api'de" — sayının OKUNUŞUNUN son sözcüğünden
//     (otuz → dan, üç → ten); ad için son ünlüden (sezgisel)
//   - süre: "25 dakika" / "25 dakikadır", "2 saat" / "2 saattir"
//   - oran merdiveni: mockup kat() birebir (< 1,12 normal aralıkta;
//     < 1,3 biraz yüksek; < 1,8 yaklaşık 1,5 kat; > 20 20 kattan fazla;
//     aksi tam sayıya yuvarlanmış "yaklaşık N kat"); taban 0 → "normalde
//     hiç görülmez" (asla "×0,0", 1587 dersi).
//
// Neden ayrı dosya: kart şablonları (explain_card.go) sözlük + sıra; ek ve
// sayı kuralları tek yerde test edilsin, şablona gömülü kopya çıkmasın.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ── sayı biçimi ───────────────────────────────────────────────────────────

// trInt — SAF: tam sayıya yuvarlar, binlik ayıracı nokta ("40.000").
func trInt(v float64) string {
	n := int64(math.Round(v))
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// trDec — SAF: en çok dec ondalık, virgülle; sondaki sıfırlar atılır
// ("2,1", "0,5", "3"). Tam kısım binlikli.
func trDec(v float64, dec int) string {
	if dec <= 0 {
		return trInt(v)
	}
	p := math.Pow(10, float64(dec))
	r := math.Round(v*p) / p
	whole := math.Trunc(r)
	frac := math.Abs(r - whole)
	fs := strconv.FormatFloat(frac, 'f', dec, 64) // "0.50"
	fs = strings.TrimRight(strings.TrimPrefix(fs, "0."), "0")
	head := trInt(whole)
	if r < 0 && whole == 0 {
		head = "-0"
	}
	if fs == "" {
		return head
	}
	return head + "," + fs
}

// ── ek uyumu ──────────────────────────────────────────────────────────────

var onesTR = [...]string{"sıfır", "bir", "iki", "üç", "dört", "beş", "altı", "yedi", "sekiz", "dokuz"}
var tensTR = [...]string{"", "on", "yirmi", "otuz", "kırk", "elli", "altmış", "yetmiş", "seksen", "doksan"}

// spokenLastWord — SAF: tam sayının Türkçe okunuşunun SON sözcüğü
// (1.150 → "elli", 40.000 → "bin", 4.800 → "yüz", 12 → "iki").
func spokenLastWord(n int64) string {
	if n < 0 {
		n = -n
	}
	switch {
	case n == 0:
		return "sıfır"
	case n%10 != 0:
		return onesTR[n%10]
	case n%100 != 0:
		return tensTR[(n%100)/10]
	case n%1000 != 0:
		return "yüz"
	case n%1_000_000 != 0:
		return "bin"
	case n%1_000_000_000 != 0:
		return "milyon"
	}
	return "milyar"
}

type suffixKind int

const (
	sfxAbl     suffixKind = iota // -dan/-den/-tan/-ten
	sfxLoc                       // -da/-de/-ta/-te
	sfxPoss3                     // -ı/-i/-u/-ü, ünlüden sonra -sı/-si/-su/-sü
	sfxPossLoc                   // -ında/-sında/-inde/-unda/-ünde …
)

func isVowelTR(r rune) bool { return strings.ContainsRune("aeıioöuü", r) }

// suffixFor — SAF: okunan sözcüğe göre ek (kesme işaretsiz).
func suffixFor(word string, k suffixKind) string {
	rs := []rune(strings.ToLower(word))
	lastV := 'e'
	for i := len(rs) - 1; i >= 0; i-- {
		if isVowelTR(rs[i]) {
			lastV = rs[i]
			break
		}
	}
	back := strings.ContainsRune("aıou", lastV)
	var last rune
	if len(rs) > 0 {
		last = rs[len(rs)-1]
	}
	endsV := isVowelTR(last)
	hard := strings.ContainsRune("çfhkpsşt", last)
	d := "d"
	if hard {
		d = "t"
	}
	a := "e"
	if back {
		a = "a"
	}
	v4 := map[rune]string{'a': "ı", 'ı': "ı", 'e': "i", 'i': "i", 'o': "u", 'u': "u", 'ö': "ü", 'ü': "ü"}[lastV]
	switch k {
	case sfxAbl:
		return d + a + "n"
	case sfxLoc:
		return d + a
	case sfxPoss3, sfxPossLoc:
		p := v4
		if endsV {
			p = "s" + v4
		}
		if k == sfxPoss3 {
			return p
		}
		la := "e"
		if v4 == "ı" || v4 == "u" {
			la = "a"
		}
		return p + "nd" + la
	}
	return ""
}

// numSfx — SAF: sayı + kesme + ek ("14'ten" değil: 14 → on dört → "14'ten").
func numSfx(n int64, k suffixKind) string {
	return strconv.FormatInt(n, 10) + "'" + suffixFor(spokenLastWord(n), k)
}

// trNumWithSfx — SAF: biçimlenmiş sayı metni + ek; ek, okunuşun son
// sözcüğünden (ondalıkta virgülden sonraki kısım okunur: "2,5" → beş).
func trNumWithSfx(text string, k suffixKind) string {
	digits := text
	if i := strings.LastIndex(text, ","); i >= 0 {
		digits = text[i+1:]
	}
	digits = strings.ReplaceAll(strings.TrimLeft(digits, "%-"), ".", "")
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return text + "'" + suffixFor(text, k)
	}
	return text + "'" + suffixFor(spokenLastWord(n), k)
}

// clockSfx — SAF: "14:30" + ek. Dakika 00 ise saat okunur ("14:00'ten").
func clockSfx(t time.Time, k suffixKind) string {
	hm := t.Format("15:04")
	n := int64(t.Minute())
	if n == 0 {
		n = int64(t.Hour())
	}
	return hm + "'" + suffixFor(spokenLastWord(n), k)
}

// nameSfx — SAF (sezgisel): ad + kesme + ek. Sondaki rakamlar sayı gibi
// okunur ("svc-2'de"); harfli adda son ünlü + son harf ("orders-api'de",
// "notify-gateway'de" değil "'da" — son ünlü a). Ünlüsüz ad ön ünlü sayılır.
func nameSfx(name string, k suffixKind) string {
	rs := []rune(name)
	end := len(rs)
	i := end
	for i > 0 && unicode.IsDigit(rs[i-1]) {
		i--
	}
	if i < end {
		n, err := strconv.ParseInt(string(rs[i:end]), 10, 64)
		if err == nil {
			return name + "'" + suffixFor(spokenLastWord(n), k)
		}
	}
	j := end
	for j > 0 && !unicode.IsLetter(rs[j-1]) {
		j--
	}
	k0 := j
	for k0 > 0 && unicode.IsLetter(rs[k0-1]) {
		k0--
	}
	return name + "'" + suffixFor(string(rs[k0:j]), k)
}

// ── süre ve gün ──────────────────────────────────────────────────────────

// durationTR — SAF: süre → ("25 dakika", "25 dakikadır"). < 60 dk dakika;
// < 48 sa saat (≥ 3 sa yuvarlanır, altı "1 saat 20 dakika"); aksi gün.
func durationTR(d time.Duration) (plain, since string) {
	m := int64(math.Round(d.Minutes()))
	if m < 1 {
		m = 1
	}
	switch {
	case m < 60:
		s := fmt.Sprintf("%d dakika", m)
		return s, s + "dır"
	case m < 48*60:
		h, rem := m/60, m%60
		if h >= 3 || rem == 0 {
			hh := int64(math.Round(float64(m) / 60))
			s := fmt.Sprintf("%d saat", hh)
			return s, s + "tir"
		}
		s := fmt.Sprintf("%d saat %d dakika", h, rem)
		return s, s + "dır"
	}
	g := int64(math.Round(float64(m) / 1440))
	s := fmt.Sprintf("%d gün", g)
	return s, s + "dür"
}

// durationShortTR — SAF: başlık çipi kısaltması ("14 dk", "1 sa 30 dk", "3 g").
func durationShortTR(d time.Duration) string {
	m := int64(math.Round(d.Minutes()))
	if m < 1 {
		m = 1
	}
	if m < 60 {
		return fmt.Sprintf("%d dk", m)
	}
	h := m / 60
	if h < 48 {
		if m%60 == 0 || h >= 3 {
			return fmt.Sprintf("%d sa", int64(math.Round(float64(m)/60)))
		}
		return fmt.Sprintf("%d sa %d dk", h, m%60)
	}
	return fmt.Sprintf("%d g", int64(math.Round(float64(m)/1440)))
}

var monthsTR = [...]string{"", "Ocak", "Şubat", "Mart", "Nisan", "Mayıs", "Haziran", "Temmuz", "Ağustos", "Eylül", "Ekim", "Kasım", "Aralık"}

// dayWordTR — SAF: "Bugün" / "Dün" / "5 Kasım" (t ve now aynı dilimde).
func dayWordTR(t, now time.Time) string {
	ty, tm, td := t.Date()
	ny, nm, nd := now.Date()
	if ty == ny && tm == nm && td == nd {
		return "Bugün"
	}
	y := now.AddDate(0, 0, -1)
	yy, ym, yd := y.Date()
	if ty == yy && tm == ym && td == yd {
		return "Dün"
	}
	return fmt.Sprintf("%d %s", td, monthsTR[tm])
}

// ── oran merdiveni ───────────────────────────────────────────────────────

// RatioForm — merdiven metninin biçimi.
type RatioForm int

const (
	// RatioShort — kart "Ne kadar" sonu ve oynatma cümlesi: "yaklaşık 3 kat".
	RatioShort RatioForm = iota
	// RatioLong — tek satır özet: "normalin yaklaşık 3 katı".
	RatioLong
)

// RatioPhrase — SAF: gözlenen / normal oranı → ifade. dir "down" ise ters
// oran (normal / gözlenen) ve azalma dili. base ≤ 0 ve gözlenen > 0 →
// "normalde hiç görülmez". ref: uzun biçimde "normalin" yerine geçen
// karşılaştırma ("diğer pod'ların"); boş = "normalin".
func RatioPhrase(observed, base float64, dir string, form RatioForm, ref string) string {
	if ref == "" {
		ref = "normalin"
	}
	if dir == "down" {
		if observed <= 0 {
			return "durdu"
		}
		if base <= 0 {
			return "normal aralıkta"
		}
		r := base / observed
		switch {
		case r < 1.12:
			return "normal aralıkta"
		case r < 1.3:
			return "biraz düşük"
		case r < 1.8:
			return "normalden yaklaşık 1,5 kat az"
		case r > 20:
			return "neredeyse durdu"
		}
		return fmt.Sprintf("normalden yaklaşık %d kat az", int64(math.Round(r)))
	}
	if base <= 0 {
		if observed <= 0 {
			return "normal aralıkta"
		}
		return "normalde hiç görülmez"
	}
	r := observed / base
	switch {
	case r < 1.12:
		return "normal aralıkta"
	case r < 1.3:
		return "biraz yüksek"
	case r < 1.8:
		if form == RatioLong {
			return ref + " yaklaşık 1,5 katı"
		}
		return "yaklaşık 1,5 kat"
	case r > 20:
		if form == RatioLong {
			return ref + " 20 katından fazla"
		}
		return "20 kattan fazla"
	}
	n := int64(math.Round(r))
	if form == RatioLong {
		return fmt.Sprintf("%s yaklaşık %d katı", ref, n)
	}
	return fmt.Sprintf("yaklaşık %d kat", n)
}

// capFirst — SAF: ilk harfi büyüt (Türkçe i → İ).
func capFirst(s string) string {
	rs := []rune(s)
	if len(rs) == 0 {
		return s
	}
	if rs[0] == 'i' {
		rs[0] = 'İ'
	} else {
		rs[0] = unicode.ToUpper(rs[0])
	}
	return string(rs)
}
