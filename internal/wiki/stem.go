package wiki

import "strings"

// stem.go — v0.10.1127 hafif Türkçe kök bulucu (SAF, tablo-testli).
//
// Operatör: "sbox sunucuları neler" ilgisiz sayfa getiriyor, "Sbox Sunucu
// Listesi" doğru sayfayı buluyor. Kök neden: jetonlar TAM sözcük —
// "sunucuları" (katlanmış "sunuculari") hiçbir zaman "sunucu" ile
// eşleşmiyordu. Türkçe eklemeli bir dil; aynı kavram onlarca yüzey biçimiyle
// yazılır (sunucu, sunucusu, sunucuları, sunucularında …).
//
// Yaklaşım (sözlüksüz, hafif): katlanmış (Fold sonrası, yalnız a-z) jetondan
// yaygın ÇEKİM eklerini en uzundan başlayarak tekrar tekrar soy; her adımda
// kök en az stemMinRunes (çoğul eki için stemMinPluralRunes) kalmalı ve ekin
// ses koşulu (ünlü/ünsüz/sert ünsüz sonrası) tutmalı. Katlamadan sonra ı→i,
// ü→u, ö→o olduğu için ünlü uyumu iki biçime iner (lari/leri, nin/nun, …).
//
// BİLİNÇLİ OLARAK SOYULMAYAN:
//   - -lık/-lik/-luk/-lük: YAPIM eki ("güvenlik" ≠ "güven").
//   - -mı/-mi (soru): Türkçe yazımda ayrı sözcük (stopword); bitişik
//     soymak "sistemi"yi "siste" yapardı.
//   - tek ünlü ekler (-ı/-i/-u/-ü, -a/-e): "sunucu"/"kafka" kökünü kemirirdi.
//     Onların yerine eşleşme biçimi (Forms) son ünlüyü düşürülmüş bir "çıplak
//     kök" de üretir: "servisi" → "servis", "ortamı" → "ortam".
//
// Teknik terim korunur: rakam, ayraç (-_./:) taşıyan, a-z dışı harfli ya da
// ≤4 harfli jeton HİÇ köklenmez (svc-orders, wsbxakfp01, err-1042, api,
// prod); özgün metinde TAMAMI BÜYÜK HARF yazılmış sözcük ve bileşik
// tanımlayıcının parçaları da (Tokens tarafında, scanTokens) köklenmez.

const (
	// stemSkipMaxRunes — bu uzunluk ve altındaki jeton köklenmez.
	stemSkipMaxRunes = 4
	// stemMinRunes — ek soyulduktan sonra kalması gereken en kısa kök.
	stemMinRunes = 4
	// stemMinPluralRunes — çoğul eki (-lar/-ler/-ları/-leri) için daha kısa
	// kök serbest: "logları" → "log", "podlar" → "pod".
	stemMinPluralRunes = 3
	// stemMaxSteps — tekrar tavanı (ek zinciri: sunucu-lar-ın-da-ki).
	stemMaxSteps = 5
	// formsMax — bir jetonun en çok kaç eşleşme biçimi.
	formsMax = 4
)

// sufCond — ekten önce kalan kökün son harfi için ses koşulu.
type sufCond uint8

const (
	condAny       sufCond = iota
	condVowel             // kök ünlüyle bitmeli (kaynaştırma: -nın, -sı, -yı, -ya, -yla)
	condConsonant         // kök ünsüzle bitmeli (-ın, -la)
	condVoiceless         // kök sert ünsüzle bitmeli (-ta, -tan, -tır)
	condLocative          // kök -da/-de/-ta/-te ile bitmeli (-ki)
)

type suffixRule struct {
	s      string
	cond   sufCond
	plural bool
}

// stemSuffixes — EN UZUNDAN kısaya; aynı uzunlukta sıra önemsiz (koşullar
// ayrık). Katlanmış biçimler: ı→i, ü→u, ö→o.
var stemSuffixes = func() []suffixRule {
	rules := []suffixRule{
		// 5
		{"ndaki", condVowel, false}, {"ndeki", condVowel, false},
		// 4
		{"lari", condAny, true}, {"leri", condAny, true},
		{"daki", condAny, false}, {"deki", condAny, false},
		{"taki", condVoiceless, false}, {"teki", condVoiceless, false},
		{"ndan", condVowel, false}, {"nden", condVowel, false},
		// 3
		{"lar", condAny, true}, {"ler", condAny, true},
		{"nin", condVowel, false}, {"nun", condVowel, false},
		{"dan", condAny, false}, {"den", condAny, false},
		{"tan", condVoiceless, false}, {"ten", condVoiceless, false},
		{"dir", condAny, false}, {"dur", condAny, false},
		{"tir", condVoiceless, false}, {"tur", condVoiceless, false},
		{"yla", condVowel, false}, {"yle", condVowel, false},
		{"nda", condVowel, false}, {"nde", condVowel, false},
		// 2
		{"in", condConsonant, false}, {"un", condConsonant, false},
		{"si", condVowel, false}, {"su", condVowel, false},
		{"ni", condVowel, false}, {"nu", condVowel, false},
		{"na", condVowel, false}, {"ne", condVowel, false},
		{"yi", condVowel, false}, {"yu", condVowel, false},
		{"ya", condVowel, false}, {"ye", condVowel, false},
		{"da", condAny, false}, {"de", condAny, false},
		{"ta", condVoiceless, false}, {"te", condVoiceless, false},
		{"la", condConsonant, false}, {"le", condConsonant, false},
		{"ki", condLocative, false},
	}
	return rules
}()

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}

// isVoiceless — folded sert ünsüzler (p ç t k f h s ş → ç/ş katlanmış c/s).
func isVoiceless(b byte) bool {
	switch b {
	case 'p', 'c', 't', 'k', 'f', 'h', 's':
		return true
	}
	return false
}

// stemmable — SAF: jeton köklenebilir mi (yalnız a-z, >4 harf).
func stemmable(tok string) bool {
	if len(tok) <= stemSkipMaxRunes {
		return false
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < 'a' || tok[i] > 'z' {
			return false
		}
	}
	return true
}

func condOK(rest string, c sufCond) bool {
	last := rest[len(rest)-1]
	switch c {
	case condVowel:
		return isVowel(last)
	case condConsonant:
		return !isVowel(last)
	case condVoiceless:
		return isVoiceless(last)
	case condLocative:
		return strings.HasSuffix(rest, "da") || strings.HasSuffix(rest, "de") ||
			strings.HasSuffix(rest, "ta") || strings.HasSuffix(rest, "te")
	}
	return true
}

// Stem — SAF: katlanmış jetonun hafif Türkçe kökü. Köklenemeyen (teknik,
// kısa, a-z dışı) jeton AYNEN döner. Yalnız a-z olduğu için bayt = rune.
func Stem(tok string) string {
	st, _ := stemAlt(tok)
	return st
}

// stemAlt — Stem + belirsizlik seçeneği: "-sı/-su" soyulduğu ANDA ("servisi"
// → "servi" mi, "servis"+"i" mi?) soyulmadan önceki biçimin çıplak kökü
// (dropFinalVowel: "servisi" → "servis"). Sözlüksüz ayırt edilemez; iki
// yorum da eşleşme biçimi olur (Forms).
func stemAlt(tok string) (stem, alt string) {
	if !stemmable(tok) {
		return tok, ""
	}
	cur := tok
	for step := 0; step < stemMaxSteps; step++ {
		stripped := false
		for _, r := range stemSuffixes {
			if !strings.HasSuffix(cur, r.s) {
				continue
			}
			rest := cur[:len(cur)-len(r.s)]
			min := stemMinRunes
			if r.plural {
				min = stemMinPluralRunes
			}
			if len(rest) < min || !condOK(rest, r.cond) {
				continue
			}
			if alt == "" && (r.s == "si" || r.s == "su") {
				alt = dropFinalVowel(cur)
			}
			cur, stripped = rest, true
			break
		}
		if !stripped {
			break
		}
	}
	return cur, alt
}

// dropFinalVowel — SAF: ünsüz+ünlü ile biten sözcüğün son ünlüsünü atar
// (kalan ≥ stemMinRunes). Tek ünlü ekin (belirtme/iyelik/yönelme) yerini tutan
// "çıplak kök": "servisi" → "servis", "ortami" → "ortam", "sunucu" → "sunuc".
// İki taraf (indeks + sorgu) aynı biçimi ürettiği için kök gerçek bir sözcük
// olmak zorunda değil; yalnız tutarlı.
func dropFinalVowel(w string) string {
	n := len(w)
	if n-1 < stemMinRunes || !isVowel(w[n-1]) || isVowel(w[n-2]) {
		return ""
	}
	return w[:n-1]
}

// Forms — SAF: katlanmış jetonun eşleşme biçimleri; İLK eleman yüzey
// biçimidir, ardından (farklıysa) kök, "-sı" belirsizliğinin seçeneği
// (stemAlt) ve çıplak kökler. Köklenemeyen jeton
// yalnız kendisi. En çok formsMax biçim, tekrarsız.
func Forms(tok string) []string {
	out := []string{tok}
	if !stemmable(tok) {
		return out
	}
	add := func(s string) {
		if s == "" || len(out) >= formsMax {
			return
		}
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	st, alt := stemAlt(tok)
	add(st)
	add(alt)
	add(dropFinalVowel(st))
	add(dropFinalVowel(tok))
	return out
}

// tokenizerVersion — v0.10.1127: jetonlayıcı sürümü. İndekslenen jeton
// kümesinin (Tokens/IndexTokens/Forms) anlamı değiştiğinde ARTIRILIR; senkron
// bir sonraki geçişte saklı sayfa içeriğinden parçaları yeniden jetonlar
// (Azure DevOps'a gitmeden; reindex.go).
//
//	1 — tam sözcük + teknik bileşikler (v0.10.1122)
//	2 — + Türkçe kök biçimleri (Forms)
const tokenizerVersion = 2

// TokenizerVersion — dışa açık kopya (durum kartı / testler).
const TokenizerVersion = tokenizerVersion
