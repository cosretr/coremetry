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
