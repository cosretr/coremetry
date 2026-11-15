package api

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// chat_answer_degen.go — v0.10.1324 (operatör-bildirimi): serbest döngünün
// FİNAL METNİ süzgeci ve yineleme düşüşü. Hepsi saf, tablo testli.
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// Prod'da gemma4 31b "INSURANCE_DOCUMENT_TRX_GET_EXAMPLECO_POLICY_PDF_PRINT_REST
// operasyonu neden hata veriyor?" sorusuna sistem prompt'undaki kapsam
// cümlesini yankılayarak başladı ve "Soru AKSİNİ SÖYLEMEDİKÇE bu boyutları …
// tool argümanlarında kullan." satırını max_tokens dolana dek onlarca kez
// yazdı. Kök neden üç ayrı önsözde birbirinin ikizi BÜYÜK HARFLİ emir
// cümleleriydi (v0.10.1274'ten beri kapsamı sunucu uyguladığı için gereksiz;
// bu sürümde kalktı). Ama yineleme sınıfı modele özgü: final metne hiçbir
// yineleme / yankı süzgeci yoktu, iki final çıkışı yalnız sanitizeAnswerLinks
// çağırıyordu.
//
// ── KATMANLAR ───────────────────────────────────────────────────────────
//
//  1. Akış muhafızı (provider/degen_guard.go) akan turu keser: Degenerated.
//  2. collapseRepeatedLines — akmayan / buffered yol için aynı sınıf: ≥3 kez
//     geçen birim (satır ya da cümle) ilk görünüşüne indirilir.
//  3. stripInstructionEcho — BU İSTEĞİN gerçekten kullandığı önsözlerden
//     türeyen cümlelerle ≥%80 token örtüşen cevap birimleri düşer.
//  4. chatDegenDecide — kullanılamaz cevap: Derin kipte BİR araçsız yeniden
//     deneme, değilse (ya da o da bozulursa) deterministik kanıt özeti.

const (
	// chatEchoPhraseMinRunes — yankı cümlesi sayılacak önsöz cümlesinin
	// alt sınırı (kısa başlıklar yankı değil, olağan kelime).
	chatEchoPhraseMinRunes = 25
	// chatEchoOverlap — cevap biriminin token'larının en az bu kadarı tek bir
	// önsöz cümlesinde geçiyorsa birim yankıdır.
	chatEchoOverlap = 0.8
	// chatEchoMinTokens — bu kadar farklı token'dan kısa birim yargılanmaz.
	chatEchoMinTokens = 4
	// chatCollapseMinRunes / chatCollapseRepeat — buffered yineleme eşiği
	// (akış muhafızıyla aynı: ≥20 rune, ≥3 kez).
	chatCollapseMinRunes = 20
	chatCollapseRepeat   = 3
	// chatDegenMinAnswerRunes — kanıt toplanmışken bundan kısa temiz cevap
	// kullanılamaz sayılır.
	chatDegenMinAnswerRunes = 80
	// chatDegenRetryMaxTokens — araçsız yeniden denemenin completion tavanı.
	chatDegenRetryMaxTokens = 700
	// chatDegenFallbackSteps / chatDegenPreviewBytes — deterministik özetin
	// en çok kaç araç önizlemesi ve önizleme başına kaç bayt taşıdığı.
	chatDegenFallbackSteps = 6
	chatDegenPreviewBytes  = 600
)

// Operatöre görünen adım çipleri.
const (
	chatDegenRetryLabel    = "model cevabı yinelenen metne düştü — araçsız yeniden deneniyor"
	chatDegenFallbackLabel = "model cevabı yinelenen metne düştü — kanıt özeti gösteriliyor"
	// ai_calls.error_msg (status ok kalır).
	chatDegenReasonRetry    = "degraded: model cevabı yinelenen metne düştü; araçsız yeniden deneme cevabı gösterildi"
	chatDegenReasonFallback = "degraded: model cevabı yinelenen metne düştü; deterministik kanıt özeti gösterildi"
)

// chatLoopPreambles — serbest döngü prompt'una giren bağlam önsözleri, TAM
// sırasıyla (ekran → sayfa/pin → sohbet bağlamı). loopPrompt bunların
// birleşimini taşır; yankı süzgeci de aynı dilimden cümle türetir. Saf.
func chatLoopPreambles(screen ChatScreenContext, page, pinned *agentctx.PageContext, cc ChatContext) []string {
	return []string{
		screenContextPreambleTR(screen),
		agentctx.PreambleTR(page, pinned), // v0.10.539 — sayfa bağlamı (pin önce)
		chatContextPreambleTR(cc),         // v0.10.478 — aktif sohbet bağlamı
	}
}

// ── birimler ────────────────────────────────────────────────────────────

// splitAnswerUnits — bir satırı cümle birimlerine böler; ayraç boşlukları
// önceki birimde kalır (birleşim = satırın kendisi). Tablo satırı ("|")
// bölünmez; "12." gibi yalnız numara öneki cümle sonu sayılmaz.
func splitAnswerUnits(line string) []string {
	t := strings.TrimLeft(line, " \t")
	if t == "" || t[0] == '|' {
		return []string{line}
	}
	var out []string
	start := 0
	for i := 1; i < len(line); i++ {
		if (line[i] != ' ' && line[i] != '\t') || !strings.ContainsRune(".!?", rune(line[i-1])) {
			continue
		}
		if onlyNumberMarker(line[start:i]) {
			continue
		}
		j := i
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		out = append(out, line[start:j])
		start = j
		i = j - 1
	}
	if start < len(line) {
		out = append(out, line[start:])
	}
	return out
}

func onlyNumberMarker(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 2 || len(s) > 4 {
		return false
	}
	return strings.Trim(s[:len(s)-1], "0123456789") == ""
}

// rewriteAnswerUnits — çit dışındaki her birimi keep'e sorar; düşen
// birimlerden boşalan satır silinir, art arda boş satırlar teke iner.
// Çit (```) içi ve çit satırları dokunulmaz. Düşen birim sayısını döner.
func rewriteAnswerUnits(text string, keep func(unit string) bool) (string, int) {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	fence, dropped := false, 0
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			fence = !fence
			out = append(out, ln)
			continue
		}
		if fence || strings.TrimSpace(ln) == "" {
			out = append(out, ln)
			continue
		}
		units := splitAnswerUnits(ln)
		var b strings.Builder
		lineDropped := 0
		for _, u := range units {
			if keep(u) {
				b.WriteString(u)
				continue
			}
			lineDropped++
		}
		dropped += lineDropped
		if lineDropped == 0 {
			out = append(out, ln)
			continue
		}
		if kept := strings.TrimRight(b.String(), " \t"); strings.TrimSpace(kept) != "" {
			out = append(out, kept)
		}
	}
	if dropped == 0 {
		return text, 0
	}
	// art arda boş satırlar teke
	res := make([]string, 0, len(out))
	blank := false
	for _, ln := range out {
		isBlank := strings.TrimSpace(ln) == ""
		if isBlank && blank {
			continue
		}
		blank = isBlank
		res = append(res, ln)
	}
	return strings.TrimSpace(strings.Join(res, "\n")), dropped
}

// collapseRepeatedLines — buffered / akmayan yolun yineleme süzgeci: çit
// dışında ≥chatCollapseRepeat kez geçen normal birim (≥chatCollapseMinRunes
// rune) İLK görünüşüne indirilir. İki kez geçen birim (meşru tekrar) kalır.
func collapseRepeatedLines(text string) (string, int) {
	counts := map[string]int{}
	_, _ = rewriteAnswerUnits(text, func(u string) bool {
		if n := aiprov.DegenNormalize(u); utf8.RuneCountInString(n) >= chatCollapseMinRunes {
			counts[n]++
		}
		return true
	})
	seen := map[string]bool{}
	return rewriteAnswerUnits(text, func(u string) bool {
		n := aiprov.DegenNormalize(u)
		if counts[n] < chatCollapseRepeat {
			return true
		}
		if seen[n] {
			return false
		}
		seen[n] = true
		return true
	})
}

// ── yankı ───────────────────────────────────────────────────────────────

// chatEchoPhrases — verilen prompt bloklarından (bu isteğin önsözleri + tavan
// eki) yankı cümleleri. Veri satırları ("- servis: x") alınmaz: cevapta
// kapsamı anmak meşru, talimat cümlesini yazmak değil. Sarılı satırlar
// paragrafa birleşir (":" ile biten başlık paragrafı kapatır), paragraf
// cümlelere bölünür, ≥chatEchoPhraseMinRunes olanlar kalır. Saf.
func chatEchoPhrases(blocks ...string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(para string) {
		for _, u := range splitAnswerUnits(para) {
			u = strings.TrimSpace(u)
			if utf8.RuneCountInString(u) < chatEchoPhraseMinRunes || seen[u] {
				continue
			}
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, blk := range blocks {
		var para []string
		flush := func() {
			if len(para) > 0 {
				add(strings.Join(para, " "))
				para = nil
			}
		}
		for _, ln := range strings.Split(blk, "\n") {
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "- ") {
				flush()
				continue
			}
			para = append(para, t)
			if strings.HasSuffix(t, ":") {
				flush()
			}
		}
		flush()
	}
	return out
}

// echoTokens — normalleştirilmiş metnin harf/rakam token kümesi (≥2 rune).
func echoTokens(s string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.FieldsFunc(aiprov.DegenNormalize(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if utf8.RuneCountInString(f) >= 2 {
			set[f] = true
		}
	}
	return set
}

// stripInstructionEcho — cevap birimlerinden, token'larının ≥chatEchoOverlap
// kadarı tek bir yankı cümlesinde geçenleri düşürür. Saf.
func stripInstructionEcho(text string, phrases []string) (string, int) {
	if len(phrases) == 0 || strings.TrimSpace(text) == "" {
		return text, 0
	}
	sets := make([]map[string]bool, 0, len(phrases))
	for _, p := range phrases {
		if s := echoTokens(p); len(s) > 0 {
			sets = append(sets, s)
		}
	}
	return rewriteAnswerUnits(text, func(u string) bool {
		toks := echoTokens(u)
		if len(toks) < chatEchoMinTokens {
			return true
		}
		for _, ps := range sets {
			hit := 0
			for t := range toks {
				if ps[t] {
					hit++
				}
			}
			if float64(hit) >= chatEchoOverlap*float64(len(toks)) {
				return false
			}
		}
		return true
	})
}

// chatCleanAnswer — süzgecin sonucu.
type chatCleanAnswer struct {
	Text string
	// Dropped — düşen birim sayısı (yineleme + yankı).
	Dropped int
	// Repeated — collapseRepeatedLines bir birimi ≥chatCollapseRepeat kez
	// gördü: akış muhafızının 1. kuralının buffered karşılığı (dejenere).
	Repeated bool
}

// cleanChatAnswer — iki final çıkışın ortak süzgeci: önce yineleme, sonra
// yankı. sanitizeAnswerLinks'ten ÖNCE çağrılır. Saf.
func cleanChatAnswer(text string, phrases []string) chatCleanAnswer {
	text, a := collapseRepeatedLines(text)
	text, b := stripInstructionEcho(text, phrases)
	return chatCleanAnswer{Text: text, Dropped: a + b, Repeated: a > 0}
}

// ── karar ───────────────────────────────────────────────────────────────

type chatDegenVerdict int

const (
	degenUsable        chatDegenVerdict = iota // cevap aynen kullanılır
	degenRetryNoTools                          // Derin: bir kez araçsız, sıkı ek + tavan
	degenDeterministic                         // kanıt özeti (model yorumu yok)
)

// chatDegenDecide — temizlenmiş final cevabın kaderi. Kullanılamaz =
// akış muhafızı kesti (degenerated) YA DA süzgeç birim düşürdü (dropped > 0)
// ve kanıt toplanmışken (araç çağrıldı / operasyon ön-getirmesi var) kalan
// metin chatDegenMinAnswerRunes'tan kısa. Süzgeçten DOKUNULMADAN geçen kısa
// cevap ("son 30 dakikada hata yok.") meşrudur, düşüşe girmez. Derin kipte ve
// henüz denenmediyse araçsız yeniden deneme; aksi hâlde deterministik özet. Saf.
func chatDegenDecide(degenerated bool, dropped int, clean string, hadEvidence, deep, retried bool) chatDegenVerdict {
	short := dropped > 0 && hadEvidence && utf8.RuneCountInString(strings.TrimSpace(clean)) < chatDegenMinAnswerRunes
	if !degenerated && !short {
		return degenUsable
	}
	if deep && !retried {
		return degenRetryNoTools
	}
	return degenDeterministic
}

// chatStepDigest — deterministik özetin bir araç satırı (başarılı çağrı).
type chatStepDigest struct {
	Tool    string
	Preview string
}

// chatDegenFallbackAnswer — model cevabı kullanılamazken gösterilen
// deterministik metin: operasyon ön-getirme özeti (varsa) + araç başına
// kırpık önizleme. "Kaynak:" künyesini çağıran ekler. Saf.
func chatDegenFallbackAnswer(prefetchSummary string, steps []chatStepDigest) string {
	var b strings.Builder
	b.WriteString("Model cevabı yinelenen metne düştüğü için gösterilmedi; aşağıda toplanan kanıtın özeti var (model yorumu yok).\n")
	if s := strings.TrimSpace(prefetchSummary); s != "" {
		b.WriteString("\n**Operasyon ön-getirmesi**\n\n")
		b.WriteString(s)
		b.WriteString("\n")
	}
	n := 0
	for _, st := range steps {
		p := strings.TrimSpace(st.Preview)
		if p == "" || n == chatDegenFallbackSteps {
			continue
		}
		n++
		clipped := clipBytes(p, chatDegenPreviewBytes)
		if len(clipped) < len(p) {
			clipped += " …"
		}
		fmt.Fprintf(&b, "\n**%s**\n\n```\n%s\n```\n", st.Tool, strings.ReplaceAll(clipped, "```", "'''"))
	}
	if strings.TrimSpace(prefetchSummary) == "" && n == 0 {
		b.WriteString("\nToplanmış kanıt yok — soruyu daraltıp yeniden deneyin.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
