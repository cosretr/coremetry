package api

// evidence_scale.go — v0.10.2378: KANIT BÜTÇELERİ modelin yürürlükteki
// penceresiyle ölçeklenir (operatör 2026-11-19: "Modelin limitlerini
// kullanalım", "GPU sınırımız yok … en iyi performans ayarlarını default yap").
//
// v0.10.2375 istek sınırlarını (completion bütçesi, örnekleme) model-farkında
// yaptı; ama modelin GÖRDÜĞÜ kanıt hâlâ 8k–32k pencereli küçük model için
// seçilmiş sabitlerle kesiliyordu (araç sonucu 6000 rune, geçmiş 6000 rune,
// stack 1500 rune / 12 satır, 100 span, 15 log satırı …). 262144 pencereli
// modelde cevap kırpılmış stack'ten ve kesik span listesinden üretiliyor,
// pencerenin çoğu boş kalıyordu.
//
// TEK ÖLÇEK: copilot.EvidenceBudgetFor(ctx) — yetenek modelcaps kaydında
// (EvidenceScaling), çarpan = yürürlükteki pencere / 32768, [1, 8]. Model adı
// burada ARANMAZ. Her bütçe yeri tabanını scaleCap ile çarpar; ölçek 1'de
// (gemma, düz qwen3, bilinmeyen model, küçük açılmış geçit) her sabit ve her
// istem bayt bayt bugünkü.
//
// ÖLÇEKLENEN: modelin OLGU olarak okuduğu kanıt (span listesi, event /
// exception / stack, log satırları ve sayıları, araç sonucu, çekmece kanıtı ve
// açıklaması, kayıt bloğu, konuşma geçmişi). ÖLÇEKLENMEYEN: tarayıcıya giden
// önizleme / çip / başlık tavanları, saklanan kayıt boyları, kullanıcı girdisi
// güvenlik tavanları, ClickHouse'a yazılan her şey.
//
// BÜTÜN-İSTEM KORUMASI — ikinci bir mekanizma değil, tur bütçesinin
// (chat_round_budget.go) AYNI sınırı: %80 × (pencere − max_tokens − pay),
// 3 bayt/token.
//
//   - Serbest döngü: fitRoundBudget. Ölçek > 1 iken pencere ad tahmini olsa da
//     devrededir (ölçek o pencereden hesaplandı; ikisi aynı sayıya bakar).
//     Tam daraltma da sığdıramıyorsa konuşma TABAN bütçeye iner
//     (evidenceConvAt ölçek 1: araç sonuçları ve geçmiş bugünkü tavana,
//     notlarıyla) — yani bu dilim yüzünden bugünkünden büyük istek kurulamaz.
//     Karma kurulumda (çağrının profili en geniş profilden dar) konuşma her
//     çağrıda ÖNCE çağrının kendi ölçeğine iner: geçmiş ve sonuçlar başka
//     profilin ölçeğiyle kırpılmış olabilir.
//   - Tek-atış çağrı (aiCall — bütün ✨ / anlatım yüzeylerinin tek kapısı):
//     fitSingleShot. Sınırı aşan user bloğu ORTASINDAN kesilir (baş: zincir +
//     span listesinin başı; son: event / log / soru korunur) ve kesildiği
//     modele söylenir. Yalnız ölçekli istem OLABİLEN kurulumda (MaxScale > 1);
//     ölçeği 1 olan profilde ayrıca pencere geçitten okunmuş olmalı.
//
// "Kırpma söylenir" doktrini aynen: kesilen her yerde mevcut notlar
// (HistoryTrimNote, drawerEvidenceTruncNote, chatToolResultTruncNote) gerçek
// sayılarla basılır.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/assemble"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// evidenceBudgetSeam — test dikişi: nil = copilot.EvidenceBudgetFor.
var evidenceBudgetSeam func(context.Context) copilot.EvidenceBudget

// evidenceBudgetOf — çağrının profilinde kanıt bütçesi girdileri. Ağsız;
// tek istisna ölçekli modelde geçit penceresi henüz okunmamışsa (aşağıda).
// AI yokken ölçek 1.
func (s *Server) evidenceBudgetOf(ctx context.Context) copilot.EvidenceBudget {
	switch {
	case evidenceBudgetSeam != nil:
		return evidenceBudgetSeam(ctx)
	case s != nil && s.copilot != nil:
		b := s.copilot.EvidenceBudgetFor(ctx)
		if b.Scale > 1 && !b.WindowProbed {
			// Ölçek ad tablosundan çıktı (262144 → 8) ama pencere geçitten henüz
			// okunmadı: geçit modeli daha küçük açmışsa ilk ✨ açıklama bağlamı
			// taşırabilirdi. Bir kez geçitten oku (ContextWindowFor: 10 dk
			// önbellek, 2 sn'de vazgeçer; wiki ve trace kırılımı da istek yolunda
			// böyle okur) ve bütçeyi yeniden hesapla. Ölçeği 1 olan modelde (gemma,
			// bilinmeyen) bu dal hiç çalışmaz: fazladan istek yok.
			s.copilot.ContextWindowFor(ctx)
			b = s.copilot.EvidenceBudgetFor(ctx)
		}
		return b
	}
	return copilot.EvidenceBudget{Scale: 1, MaxScale: 1}
}

// evidenceScale — çağrının profilinde kanıt tavanı çarpanı (≥1).
func (s *Server) evidenceScale(ctx context.Context) int {
	return max(1, s.evidenceBudgetOf(ctx).Scale)
}

// evidenceScaleSurface — kanıt ctx'in yüzeyinden BAŞKA bir yüzeyle
// gönderilecekse (sohbet içinden "chat-drawer" / "chat-guided" anlatımı; HTTP
// ✨ düğmesinde yüzey istek yolundan) ölçek o yüzeyin profilinden çözülür.
// Açık profil seçimi (WithProfile) yüzeyi yine ezer.
func (s *Server) evidenceScaleSurface(ctx context.Context, surface string) int {
	if surface != "" {
		m := copilot.MetaFromContext(ctx)
		m.Surface = surface
		ctx = copilot.WithMeta(ctx, m)
	}
	return s.evidenceScale(ctx)
}

// chatEntryEvidenceScale — sohbet isteğinin GİRİŞ ölçeği: geçmiş kırpması
// alışveriş ctx'i kurulmadan önce koşar. Sohbet yüzeyi + (varsa) açık profil
// seçimi. Alışveriş sonradan daha küçük pencereli profile yönlenirse (Derin
// profili, yerel profil, araştırma profili) tur bütçesi geçmişi o profilin
// penceresine göre taban bütçeye indirir (evidenceBaseConv).
func (s *Server) chatEntryEvidenceScale(ctx context.Context, profile string) int {
	if p := strings.TrimSpace(profile); p != "" {
		ctx = copilot.WithProfile(ctx, p)
	}
	return s.evidenceScaleSurface(ctx, "chat")
}

// toolEvidenceScale — SAF (v0.10.2384): araç çağrısının ctx'ine yazılan ölçek
// (mcptools.WithEvidenceScale). mcptools'un kendi alan tavanları ve gövde
// bütçeleri bu sayıyla büyür; api tavanı DIŞ sınır kalsın diye v2 döngüsünde
// planın araç metni tavanının (operatör daraltmış olabilir) izin verdiği
// çarpanı aşmaz: araç bütçesi (V2ToolMaxBytes × n) ≤ plan tavanı. v1'de
// çarpanlar zaten aynı (5800 × n < 6000 × n). Ölçek 1 → 1.
func toolEvidenceScale(v2 *v2LoopPlan, scale int) int {
	if scale <= 1 {
		return 1
	}
	if v2 != nil && v2.Budget.ToolTextBytes > 0 {
		return max(1, min(scale, v2.Budget.ToolTextBytes/chatV2ToolTextMaxBytes))
	}
	return scale
}

// scaleCap — SAF: taban tavan × ölçek (ölçek ≤ 1 → taban aynen).
func scaleCap(base, scale int) int {
	if scale <= 1 {
		return base
	}
	return base * scale
}

// ── bütün-istem koruması: tek-atış çağrı ───────────────────────────────────

const (
	// promptGuardMinBytes — bunun altında kesmeye değmez (pencere sistem
	// prompt'una bile yetmiyor): istek bugünkü gibi gider.
	promptGuardMinBytes = 2048
	// promptGuardHeadPct — kesilen blokta BAŞTAN korunan pay. Kalan son
	// taraftan: kanıt paketlerinde kuyruk (event bloğu, loglar, SORU) baştan
	// değerli, kesilen orta kısım span listesinin sonudur.
	promptGuardHeadPct = 40
)

// promptGuardNoteFmt — orta kesimin modele söylenen hâli. %d'ler: çıkarılan,
// baştan verilen, sondan verilen karakter.
const promptGuardNoteFmt = "\n\n[kırpıldı: istem modelin bağlam penceresine sığmadığı için ortasından %d karakter çıkarıldı " +
	"(ilk %d ve son %d karakter verildi). Çıkarılan kısma dayanarak sayım ya da çıkarım yapma; kanıtın eksik olduğunu cevapta söyle.]\n\n"

// promptByteLimit — SAF: bir isteğin (sistem + kullanıcı) bayt sınırı. Tur
// bütçesinin AYNI formülü (roundBudgetLimit) ve kalibrasyonsuz oranı.
func promptByteLimit(window, maxTok int) int {
	return int(float64(roundBudgetLimit(window, maxTok)) * roundBudgetBytesPerToken)
}

// clampPromptBytes — SAF: s en çok maxBytes olacak şekilde ORTASINDAN kesilir
// (rune sınırında), kesim notla söylenir. Sığıyorsa ya da maxBytes not için
// bile küçükse s aynen.
func clampPromptBytes(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes || maxBytes < promptGuardMinBytes {
		return s, false
	}
	room := maxBytes - len(promptGuardNoteFmt) - 3*20 // not + üç sayı payı
	head := room * promptGuardHeadPct / 100
	tail := room - head
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	h, t := s[:head], s[start:]
	return h + fmt.Sprintf(promptGuardNoteFmt, utf8.RuneCountInString(s[head:start]),
		utf8.RuneCountInString(h), utf8.RuneCountInString(t)) + t, true
}

// fitSingleShot — aiCall: tek-atış çağrının user bloğu pencere sınırına
// sığdırılır. Ölçekli istem OLAMAYAN kurulumda (MaxScale ≤ 1), ölçeği 1 olup
// penceresi yalnız ad tahmini olan profilde ve sınırın altında user AYNEN döner.
func (s *Server) fitSingleShot(ctx context.Context, system, user string) string {
	b := s.evidenceBudgetOf(ctx)
	// Pencereye güven: bu profilin ölçeği > 1 ise ölçek zaten o pencereden
	// hesaplandı; ölçeği 1 olan profilde (karma kurulum) yalnız geçitten
	// okunmuş pencere — ad tahmini bugün çalışan istemi kesmemeli.
	if b.Window <= 0 || !(b.Scale > 1 || (b.MaxScale > 1 && b.WindowProbed)) {
		return user
	}
	out, _ := clampPromptBytes(user, promptByteLimit(b.Window, s.roundBudgetMaxTokens(ctx))-len(system))
	return out
}

// ── bütün-istem koruması: serbest döngünün taban bütçeye inişi ─────────────

// roundBudgetBaseChip — taban bütçeye inişin operatöre görünen adım satırı.
const roundBudgetBaseChip = "bağlam daraltıldı: kanıt taban bütçeye indirildi"

// toolTruncNoteRe — chatToolResultTruncNote'un sondaki hâli (yeniden kırpmada
// eski notun atlanan sayısı korunur).
var toolTruncNoteRe = regexp.MustCompile(`\n\n\[kırpıldı: sonucun ilk \d+ karakteri verildi, (\d+) karakter atlandı\. [^\]]*\]$`)

// reclampToolResult — SAF: (ölçekli tavanla kırpılmış olabilecek) sonucu taban
// tavana indirir: maxBytes > 0 ise bayt (v2), değilse maxRunes. Notun sayıları
// ÖZGÜN sonuca göre doğru kalır (eski notun atlanan sayısı eklenir).
func reclampToolResult(s string, maxRunes, maxBytes int) (string, bool) {
	body, prev := s, 0
	if m := toolTruncNoteRe.FindStringSubmatchIndex(s); m != nil {
		prev, _ = strconv.Atoi(s[m[2]:m[3]])
		body = s[:m[0]]
	}
	n := utf8.RuneCountInString(body)
	kept := n
	if maxBytes > 0 {
		if len(body) > maxBytes {
			cut := maxBytes
			for cut > 0 && !utf8.RuneStart(body[cut]) {
				cut--
			}
			body = body[:cut]
			kept = utf8.RuneCountInString(body)
		}
	} else if n > maxRunes {
		body, kept = string([]rune(body)[:maxRunes]), maxRunes
	}
	if kept == n {
		return s, false
	}
	return body + sprintfTruncNote(kept, n-kept+prev), true
}

// loopToolBytesAt — v2 planının araç metni bayt tavanı, verilen ölçekte
// (0 = v1: rune tavanı geçerli). Operatörün daraltması korunur.
func loopToolBytesAt(v2 *v2LoopPlan, scale int) int {
	if v2 == nil || v2.Budget.ToolTextBytes <= 0 {
		return 0
	}
	return min(v2.Budget.ToolTextBytes, scaleCap(chatV2ToolTextMaxBytes, scale))
}

// evidenceConvAt — SAF: konuşmanın verilen ölçeğin bütçesine inmiş kopyası
// (scale 1 = TABAN, bugünkü sabitler). Araç sonuçları o ölçeğin tavanına (v1:
// chatToolResultMaxRunes × ölçek; v2: toolBytes > 0 ise o bayt tavanı) iner;
// baştaki metin turları (istemci geçmişi + aktif soru)
// assemble.HistoryMaxRunes × ölçek ile yeniden kırpılır, not bir kez. Zaten
// bütçenin içindeki konuşma AYNEN döner (changed=false, aynı dilim). Girdi
// DEĞİŞMEZ.
func evidenceConvAt(conv []copilot.ChatMessage, toolBytes, scale int) (out []copilot.ChatMessage, changed bool) {
	out = conv
	own := func() {
		if !changed {
			out = append([]copilot.ChatMessage(nil), conv...)
			changed = true
		}
	}
	for i, m := range conv {
		for j, tr := range m.ToolResults {
			c, cut := reclampToolResult(tr.Content, scaleCap(chatToolResultMaxRunes, scale), toolBytes)
			if !cut {
				continue
			}
			own()
			if &out[i].ToolResults[0] == &conv[i].ToolResults[0] {
				out[i].ToolResults = append([]copilot.ToolResult(nil), conv[i].ToolResults...)
			}
			out[i].ToolResults[j].Content = c
		}
	}
	// baştaki metin turları = geçmiş + aktif soru
	n := 0
	for n < len(conv) && len(conv[n].ToolCalls) == 0 && len(conv[n].ToolResults) == 0 {
		n++
	}
	hist := conv[:n]
	if len(hist) > 0 && hist[0].Role == "user" && assemble.HasTrimNote(hist[0].Text) {
		hist = hist[1:] // önceki kırpmanın notu: yeniden sayılmaz, aşağıda geri eklenir
	}
	keep, trimmed := assemble.ClampHistory(assemble.RuneLens(chatMessageTexts(hist)), chatMaxMessages, scaleCap(assemble.HistoryMaxRunes, scale))
	if trimmed == 0 {
		return out, changed
	}
	rest := out[n:]
	nw := make([]copilot.ChatMessage, 0, 1+keep+len(rest))
	nw = append(nw, copilot.ChatMessage{Role: "user", Text: assemble.HistoryTrimNote})
	nw = append(nw, hist[len(hist)-keep:]...)
	return append(nw, rest...), true
}
