package api

// answer_verify_quote.go — v0.10.2461 (operatör-bildirimli, 2026-11-22):
// doğrulayıcının alıntı denetimi.
//
// Trace açıklaması bir exception mesajını tırnak içinde verdi; operatörün
// gördüğü log metni Türkçeydi. İstem artık mesajları kanıttaki dilde ve
// birebir alıntılamayı ister (copilot.quoteVerbatimRule); bu denetim kuralın
// kod tarafıdır: cevaptaki tırnak içi metin ("…", “…”, boşluk içeren `…`)
// verifyQuoteMinRunes'tan uzunsa, normalleştirilmiş (boşluk, büyük/küçük
// harf, JSON kaçışları) hâliyle turun kanıtında geçmelidir. Geçmeyen alıntı
// (çeviri, uydurma, yeniden yazım) "quote" türünde uyuşmazlıktır: zorla
// kipinde öteki değerler gibi "(doğrulanmamış değer)" notunu alır, verify
// kolonu "quote=…" sayar, öz-gözlem cosre.answer.quote_check sayacı artar.
//
// Kısaltma: "…" ya da "..." alıntıyı parçalara böler; her anlamlı parça
// (≥ verifyQuoteFragMinRunes) kanıtta geçmelidir — baştan / sondan kırpılmış
// ya da ortası atlanmış alıntı doğrulanır. Kısmi alıntı (mesajın bir bölümü)
// zaten alt dize olarak geçer. Genel cevap kademesinde denetim yok (canlı
// kanıt beklenmez). Alıntı içi, öteki denetimlerden maskelenir: içindeki
// sayı / ad alıntının hükmüyle gelir, iki kez işaretlenmez.

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	verifyKindQuote = "quote"
	// verifyQuoteMinRunes — bundan uzun (rune) tırnak içi metin denetlenir.
	verifyQuoteMinRunes = 20
	// verifyQuoteFragMinRunes — "…" ile bölünen parçanın denetlenen en kısa boyu.
	verifyQuoteFragMinRunes = 6
)

var verifyQuoteRes = []*regexp.Regexp{
	regexp.MustCompile(`"([^"\n]{1,800})"`),
	regexp.MustCompile(`“([^”\n]{1,800})”`),
	regexp.MustCompile("`([^`\n]{1,800})`"),
}

// verifyQuoteSpan — cevaptaki bir alıntı (bayt aralığı tırnaklar dahil).
type verifyQuoteSpan struct {
	start, end int
	text       string // tırnaklar dahil (işaretleme jetonu)
	inner      string
}

// verifyQuoteClaims — SAF: denetlenecek alıntılar (geliş sırası; çakışan
// eşleşmede önce gelen kalır). Ters tırnak yalnız boşluk içeriyorsa alıntıdır
// (tanımlayıcı `a.b.C` ad denetimine kalır).
func verifyQuoteClaims(s string) []verifyQuoteSpan {
	var out []verifyQuoteSpan
	taken := func(i, j int) bool {
		for _, q := range out {
			if i < q.end && j > q.start {
				return true
			}
		}
		return false
	}
	for k, re := range verifyQuoteRes {
		for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
			inner := s[m[2]:m[3]]
			if utf8.RuneCountInString(strings.TrimSpace(inner)) <= verifyQuoteMinRunes {
				continue
			}
			if k == 2 && !strings.ContainsAny(strings.TrimSpace(inner), " \t") {
				continue
			}
			if taken(m[0], m[1]) {
				continue
			}
			out = append(out, verifyQuoteSpan{start: m[0], end: m[1], text: s[m[0]:m[1]], inner: inner})
		}
	}
	// geliş sırası
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].start < out[j-1].start; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// quoteNormReplacer — JSON kaçışları, tipografik tırnaklar, log satır sonu işareti.
var quoteNormReplacer = strings.NewReplacer(
	`\"`, `"`, `\n`, " ", `\t`, " ", `\r`, " ", `\/`, "/", `\\`, `\`,
	`<`, "<", `>`, ">", `&`, "&", `'`, "'",
	"“", `"`, "”", `"`, "‘", "'", "’", "'", "⏎", " ",
	// Türkçe i/ı katlaması: dört biçim tek harfe (YARDIMCI ↔ yardımcı, ID ↔ id).
	"İ", "i", "I", "i", "ı", "i",
)

// quoteNorm — SAF: karşılaştırma biçimi (kaçışsız, küçük harf, tek boşluk).
func quoteNorm(s string) string {
	s = strings.ToLower(quoteNormReplacer.Replace(s))
	return strings.Join(strings.Fields(s), " ")
}

// verifyQuoteFragments — SAF: alıntının denetlenen parçaları ("…" / "..."
// ile bölünür, uçtaki noktalama kırpılır, kısa parça atılır). Boş = denetlenmez.
func verifyQuoteFragments(inner string) []string {
	inner = strings.ReplaceAll(inner, "...", "…")
	// v0.10.2480 (F) — "Tip: mesaj" alıntısı (model exception tipini mesajın
	// önüne yazar; kanıtta tip ve mesaj ayrı alanlar): boşluksuz ön ek ayrı parça.
	if k := strings.Index(inner, ": "); k > 0 && !strings.ContainsAny(strings.TrimSpace(inner[:k]), " \t") {
		inner = inner[:k] + "…" + inner[k+2:]
	}
	var out []string
	for _, f := range strings.Split(inner, "…") {
		f = quoteNorm(strings.Trim(strings.TrimSpace(f), ".,;:!?-–— "))
		if utf8.RuneCountInString(f) >= verifyQuoteFragMinRunes {
			out = append(out, f)
		}
	}
	return out
}

// verifyQuoteHolds — SAF: her parça normalleştirilmiş kanıtta geçiyor mu.
func verifyQuoteHolds(frags []string, normEvidence string) bool {
	for _, f := range frags {
		if !strings.Contains(normEvidence, f) {
			return false
		}
	}
	return true
}

// verifyQuotePass — SAF: verifyAnswer'ın alıntı adımı. Dönüş: iddialar ve
// alıntıları boşlukla maskelenmiş metin. normEvidence tembel (alıntı yoksa
// kanıt normalleştirilmez).
func verifyQuotePass(text string, normEvidence func() string) ([]verifyClaim, string) {
	qs := verifyQuoteClaims(text)
	if len(qs) == 0 {
		return nil, text
	}
	var claims []verifyClaim
	var b strings.Builder
	last := 0
	ev := ""
	evDone := false
	for _, q := range qs {
		frags := verifyQuoteFragments(q.inner)
		if len(frags) == 0 {
			continue
		}
		if !evDone {
			ev, evDone = normEvidence(), true
		}
		st := verifyMismatch
		if verifyQuoteHolds(frags, ev) {
			st = verifyVerified
		}
		claims = append(claims, verifyClaim{Kind: verifyKindQuote, Text: q.text, Status: st})
		b.WriteString(text[last:q.start])
		b.WriteString(strings.Repeat(" ", q.end-q.start))
		last = q.end
	}
	b.WriteString(text[last:])
	return claims, b.String()
}

// verifyNormEvidence — turun kanıtı + durum adları, alıntı karşılaştırma biçiminde.
func verifyNormEvidence(ev verifyEvidence) string {
	var b strings.Builder
	for _, t := range ev.Texts {
		b.WriteString(t)
		b.WriteByte('\n')
	}
	for _, n := range ev.Names {
		b.WriteString(n)
		b.WriteByte('\n')
	}
	return quoteNorm(b.String())
}
