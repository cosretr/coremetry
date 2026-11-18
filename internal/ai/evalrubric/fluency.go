package evalrubric

// fluency.go — v0.10.2322 (CoSRE konuşma kalitesi H1,
// docs/cosre/conversation-audit.md §4 S2): cevap metninin akıcılık
// ölçütleri için SAF yardımcılar. Sayımlar audit'in S2 örnekleme sorgusunun
// Go karşılığıdır — prod cosre_interactions üzerindeki SQL ile değerlendirme
// koşusu aynı tanımla ölçer.

import (
	"regexp"
	"strings"
)

// sourceLineRe — sunucunun eklediği son "Kaynak: …" satırı (S2: govde).
var sourceLineRe = regexp.MustCompile(`\n+(\S+ )?Kaynak: [^\n]*$`)

// serverSourceLineRe — v0.10.2338 (karar 9): sunucunun DÜZ "Kaynak: …" satırı
// (uyarılı "⚠ Kaynak:" dürüstlük notu hariç) — sunucu kalıbı sayımı.
var serverSourceLineRe = regexp.MustCompile(`(^|\n)Kaynak: [^\n]*$`)

// HasServerSourceLine — v0.10.2338: cevap sunucunun düz "Kaynak:" satırıyla bitiyor.
func HasServerSourceLine(answer string) bool {
	return serverSourceLineRe.MatchString(strings.TrimSpace(answer))
}

// sentenceEndRe — S2 ort_cumle: `[.!?…](\s|$)`.
var sentenceEndRe = regexp.MustCompile(`[.!?…](\s|$)`)

// StripSourceLine — cevabın gövdesi: kırpılmış, sondaki "Kaynak:" satırı
// düşmüş (S2 `govde`).
func StripSourceLine(answer string) string {
	return sourceLineRe.ReplaceAllString(strings.TrimSpace(answer), "")
}

// Sentences — gövdedeki cümle sayısı (noktalama sayımı; S2 ile aynı kural,
// numaralı liste satırı cümle sayılır). Noktalamasız boş olmayan metin 1.
func Sentences(answer string) int {
	body := StripSourceLine(answer)
	if strings.TrimSpace(body) == "" {
		return 0
	}
	n := len(sentenceEndRe.FindAllStringIndex(body, -1))
	if n == 0 {
		return 1
	}
	return n
}

// ── v0.10.2323 — akıcılık ölçütleri ─────────────────────────────────────

// BoilerplateOpenings / BoilerplateClosings — kalıp açılış ve kapanış
// listesi. TEK YER: audit §4 S2 sorgusundaki `acilis_kaliplari` /
// `kapanis_kaliplari` dizileriyle birebir (TestBoilerplateListsMatchAuditS2
// belgeyle eşitliği pinler); değerlendirme raporu ve ileride D dilimi
// süzgeci bu listeyi okur. Küçük harf, Türkçe İ → i katlanmış metne karşı.
var (
	BoilerplateOpenings = []string{"tabii", "elbette", "merhaba", "selam", "harika bir soru", "güzel soru", "anladım", "hemen bakıyorum",
		"size yardımcı", "yardımcı olayım", "aşağıda", "işte ", "öncelikle", "verilen veriye göre",
		"sağlanan verilere göre", "verilere göre", "elimdeki verilere göre", "incelediğimde", "analiz sonucunda"}
	BoilerplateClosings = []string{"başka bir sorunuz", "başka sorunuz", "başka bir konuda", "yardımcı olabileceğim başka", "umarım yardımcı",
		"umarım faydalı", "sormaktan çekinme", "bana bildirin", "haber verin", "iyi çalışmalar", "kolay gelsin",
		"daha fazla bilgi isterseniz", "detay isterseniz", "ister misiniz"}
)

var (
	leadingMarkupRe = regexp.MustCompile(`^[\s*_>#-]+`)
	bulletLineRe    = regexp.MustCompile(`(?m)^\s*([-*•]|\d+[.)])\s`)
)

// foldTR — S2 ile aynı katlama: 'İ' → 'i', sonra küçük harf.
func foldTR(s string) string { return strings.ToLower(strings.ReplaceAll(s, "İ", "i")) }

// Fluency — tek cevabın otomatik akıcılık ölçütleri (S2 tanımları).
type Fluency struct {
	BoilerplateOpening bool `json:"boilerplateOpening"`
	BoilerplateClosing bool `json:"boilerplateClosing"`
	Sentences          int  `json:"sentences"`
	Chars              int  `json:"chars"`
	Bullets            bool `json:"bullets"`
	EndsWithQuestion   bool `json:"endsWithQuestion"`
}

// FluencyOf — SAF: cevabın ölçütleri. Açılış: gövdenin baştaki biçim
// işaretleri atılmış ilk 80 karakterinde önek; kapanış: son 200 karakterde
// içerme; soru: gövde "?" ile biter; madde listesi: "-", "*", "•" ya da
// "1." / "1)" ile başlayan satır.
func FluencyOf(answer string) Fluency {
	body := StripSourceLine(answer)
	rs := []rune(leadingMarkupRe.ReplaceAllString(body, ""))
	head := foldTR(string(rs[:min(len(rs), 80)]))
	br := []rune(body)
	tail := foldTR(string(br[max(0, len(br)-200):]))
	f := Fluency{Sentences: Sentences(answer), Chars: len(br), Bullets: bulletLineRe.MatchString(answer),
		EndsWithQuestion: strings.HasSuffix(strings.TrimRight(body, " \t\r\n"), "?")}
	for _, p := range BoilerplateOpenings {
		if strings.HasPrefix(head, p) {
			f.BoilerplateOpening = true
			break
		}
	}
	for _, p := range BoilerplateClosings {
		if strings.Contains(tail, p) {
			f.BoilerplateClosing = true
			break
		}
	}
	return f
}
