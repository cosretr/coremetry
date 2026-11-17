package api

// chat_answer_class.go — v0.10.2331 (docs/cosre/conversation-audit.md §6.1 B,
// §7 kararları 1, 5, 14 önerildiği gibi): CEVAP SINIFI ve UZUNLUK ÖLÇÜSÜ.
//
// İlke: sınıf cevabın UZUNLUĞUNU ve derinliğini belirler, cümlelerini asla.
// Sınıf saf bir eşlemedir (karar motoru değil — experience-audit §5):
//
//   - guided anlatım: rota niyeti → sınıf (answerClassOfIntent);
//   - serbest döngü: sorunun biçimi + konuşma durumu → deterministik ipucu
//     (answerClassHint; ek model çağrısı YOK);
//   - niyet "none" genel cevabı: sohbet ya da bilgi (answerClassGeneral);
//   - rapor türleri (servis raporu, kaynak incelemesi, trace açıklaması, rota
//     incelemesi; açıklama turu ve Derin kip de) sınıf `report`: ölçü yok
//     (karar 1).
//
// Sınıf yalnız iki şey verir: istemin EN SONUNA tek satır (answerClassLines;
// değişken bloklar sonda — §6.0) ve çıktı tavanı (copilot.WithAnswerCap;
// düşünen modelde kalkar). Tavan dolarsa cevap son tam cümlede biter
// (answerEndClean).
//
// Anahtar: `cosre_conversation.style` (varsayılan KAPALI) VE cosre_v2. Kapalıyken
// ctx, istem ve cevap baytları aynen; sınıf yine hesaplanır ama YALNIZ
// değerlendirme koşucusuna bildirilir (canlı satıra yazılmaz). Değerlendirme
// koşusu anahtarı yalnız o koşu için zorlayabilir (evalConvTurn.Style).

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// answerClass — cevap sınıfı.
type answerClass string

const (
	classNone      answerClass = ""
	classStatus    answerClass = "status"
	classErrorHunt answerClass = "error_hunt"
	classExplain   answerClass = "explanation"
	classCompare   answerClass = "comparison"
	classKnowledge answerClass = "knowledge"
	classChat      answerClass = "chat"
	classReport    answerClass = "report"
)

// answerClassLines — sınıf başına TEK satır (istemin sonuna). Yalnız uzunluk;
// içerik, biçim ve cümle kalıbı vermez. report ve "" satırsız.
var answerClassLines = map[answerClass]string{
	classStatus:    "\n\nCEVAP UZUNLUĞU: 1-2 cümle.",
	classErrorHunt: "\n\nCEVAP UZUNLUĞU: 3-5 cümle; bağlantılar ayrıca kart olarak gösterilir.",
	classExplain:   "\n\nCEVAP UZUNLUĞU: tek kısa paragraf.",
	classCompare:   "\n\nCEVAP UZUNLUĞU: 2-4 cümle.",
	classKnowledge: "\n\nCEVAP UZUNLUĞU: kısa bir paragraf.",
	classChat:      "\n\nCEVAP UZUNLUĞU: 1-2 cümle.",
}

// answerClassCaps — çıktı tavanı (token). Türetme (audit varsayımı 3 B ≈ 1
// token): bu alanda bir Türkçe cümle sayı, servis adı ve markdown ile
// 120–200 B ≈ 40–70 token. Tavan = sınıfın üst cümle sayısı × 70 token ×
// ≈2,5 pay (guided'da modelin aktardığı ≤3 grafik çiti ≈ 60'ar token dahil),
// 128'e yuvarlanmış: normal bir cevap tavana hiç değmez; tavan yalnız
// kaçağı (rapor yazan, kendini tekrar eden) keser.
var answerClassCaps = map[answerClass]int{
	classStatus:    384, // ≤2 cümle ≈ 140
	classChat:      384, // ≤2 cümle ≈ 140
	classCompare:   640, // ≤4 cümle ≈ 280
	classErrorHunt: 768, // ≤5 cümle ≈ 350 (+ çit payı)
	classExplain:   768, // kısa paragraf ≈ 4–6 cümle
	classKnowledge: 768,
}

// answerLoopToolFloor — döngü turlarında tavanın tabanı: tavan araç çağıran
// turlara da iner (cevap mı araç mı olacağı önceden bilinmez); birkaç paralel
// çağrının JSON argümanı kısa tavana sığmalı.
const answerLoopToolFloor = 512

// answerClassOfIntent — SAF: guided rota niyeti → sınıf. ask_service (LLM'siz
// soru) ve bilinmeyen niyet "".
func answerClassOfIntent(intent guidedIntent) answerClass {
	switch intent {
	case guidedTraceByID, guidedSpanByID, guidedRequestID, guidedServiceReport, guidedResourceInvestigation:
		return classReport // guidedNarrationPrompt'un rapor anlatıcıları (karar 1)
	case guidedServiceHealth, guidedFamilyHealth, guidedMyServices, guidedTeamServices, guidedPodHealth,
		guidedDBHealth, guidedMessagingHealth, guidedServiceHealthcheck, guidedNamespaceServices,
		guidedMyProblems, guidedProblems, guidedFindEntity, guidedEndpointCandidates:
		return classStatus
	case guidedLogErrors, guidedLogField, guidedMyExceptions, guidedTeamExceptions, guidedServiceExceptions,
		guidedTopErrorServices, guidedMyTopErrors, guidedTeamTopErrors, guidedTopErrorOps, guidedRootCause,
		guidedSlowTraces, guidedTraceSearch, guidedEndpointTraces, guidedFamilyTraces, guidedTraceDrill,
		guidedPairRequests:
		return classErrorHunt
	case guidedWindowCompare, guidedDeployImpact:
		return classCompare
	case guidedServiceTopology, guidedTraceTopology, guidedFanout, guidedCallPeriod, guidedMetricChart,
		guidedShiftSummary, guidedOutboundCall, guidedOpenPage:
		return classExplain
	case guidedHowTo, guidedSelfMeta:
		return classKnowledge
	case guidedOffTopic:
		return classChat
	}
	return classNone
}

// Soru biçimi sözcükleri (normalizeGuidedMsg küçük harf; önek eşleşmesi).
var (
	classChatWords    = []string{"merhaba", "selam", "teşekkür", "tesekkür", "sağol", "sağ", "eyvallah", "tamam", "günaydın", "iyi", "akşamlar", "kolay", "gelsin", "nasılsın", "ok", "okey", "süper", "harika", "ol"}
	classWhyStems     = []string{"neden", "niye", "niçin", "sebep", "sebeb", "kök"}
	classCompareStems = []string{"karşılaştır", "kıyas", "fark", "dünle", "öncekiyle", "versus", "vs"}
	classErrorStems   = []string{"hata", "error", "exception", "istisna", "yavaş", "timeout", "patl", "çök", "crash", "başarısız", "fail"}
	classKnowPhrases  = []string{"nedir", "ne demek", "ne anlama", "nasıl yapılır", "nasıl kurulur", "nasıl ayarlanır", "what is", "how to"}
	classExplainStems = []string{"açıkla", "anlat", "yorumla", "özetle"}
	classStatusStems  = []string{"durum", "nasıl", "sağlık", "sağlıklı", "ayakta", "kaç", "p95", "p99", "oran", "deploy", "var", "iyi"}
)

// answerClassHint — SAF: serbest döngünün sınıfı. report = rota incelemesi,
// trace takibi, açıklama turu ya da Derin kip (çağıran söyler). Sıra: sohbet
// > neden (hata avı) > karşılaştırma > hata sözcüğü (durum sözcüğü yoksa) >
// bilgi (öznesiz) > açıklama > durum > varsayılan (öznesi varsa durum, yoksa
// açıklama).
func answerClassHint(question string, st ChatContext, report bool) answerClass {
	if report {
		return classReport
	}
	n := normalizeGuidedMsg(strings.TrimSpace(question))
	toks := guidedTokens(n)
	if len(toks) == 0 {
		return classChat
	}
	if classIsChat(toks) {
		return classChat
	}
	if tokenHasPrefix(toks, classWhyStems...) {
		return classErrorHunt
	}
	if tokenHasPrefix(toks, classCompareStems...) {
		return classCompare
	}
	status := tokenHasPrefix(toks, classStatusStems...)
	if tokenHasPrefix(toks, classErrorStems...) && !status {
		return classErrorHunt
	}
	subject := stateHasSubject(st)
	for _, p := range classKnowPhrases {
		if strings.Contains(n, p) {
			if subject {
				return classExplain
			}
			return classKnowledge
		}
	}
	if tokenHasPrefix(toks, classExplainStems...) {
		return classExplain
	}
	if status || tokenHasPrefix(toks, classErrorStems...) {
		return classStatus
	}
	if subject {
		return classStatus
	}
	return classExplain
}

// classIsChat — SAF: kısa ve yalnız selam / teşekkür / onay sözcükleri.
func classIsChat(toks []string) bool {
	if len(toks) > 5 {
		return false
	}
	for _, t := range toks {
		hit := false
		for _, w := range classChatWords {
			if strings.HasPrefix(t, w) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// answerClassGeneral — SAF: niyet "none" genel cevabı (araçsız): sohbet ya da bilgi.
func answerClassGeneral(question string) answerClass {
	toks := guidedTokens(normalizeGuidedMsg(strings.TrimSpace(question)))
	if len(toks) == 0 || classIsChat(toks) {
		return classChat
	}
	return classKnowledge
}

// ── Tur durumu ────────────────────────────────────────────────────────────

// answerStyleTurn — bir v2 turunun sınıf durumu. nil = v1 (her metot nil-güvenli).
type answerStyleTurn struct {
	on   bool
	conv *evalConvTurn  // değerlendirme koşucusu (konuşma kipi); üretimde nil
	stt  *chatStateTurn // v0.10.2332 — durumun bu turdaki varsayımları (nil = durum kapalı)

	mu      sync.Mutex
	class   answerClass
	cap     int    // son uygulanan tavan (0 = yok)
	rangeS  int64  // v0.10.2332 — guided anlatımın penceresi (0 = bilinmiyor)
	slotSvc string // v0.10.2332 — durumun cevapladığı servis sorusu
}

type answerStyleKey struct{}

// answerStyleOn — anahtar: v2 VE style. Değerlendirme konuşma kipinde koşunun
// zorlaması (evalConvTurn.Style) canlı ayarın yerine geçer; üretimde
// evalConvFrom nil → yalnız canlı ayar.
func answerStyleOn(ctx context.Context) bool {
	if !cosreV2Marked(ctx) {
		return false
	}
	if t := evalConvFrom(ctx); t != nil && t.Style != nil {
		return *t.Style
	}
	return chstore.CurrentCosreConversation().Style
}

// withAnswerStyle — turun başında (copilotChatWith) bir kez. v1'de ctx aynen.
func withAnswerStyle(ctx context.Context, stt *chatStateTurn) context.Context {
	if !cosreV2Marked(ctx) {
		return ctx
	}
	return context.WithValue(ctx, answerStyleKey{}, &answerStyleTurn{on: answerStyleOn(ctx), conv: evalConvFrom(ctx), stt: stt})
}

func answerStyleFrom(ctx context.Context) *answerStyleTurn {
	t, _ := ctx.Value(answerStyleKey{}).(*answerStyleTurn)
	return t
}

// note — turun sınıfı (son anlatım kademesi kazanır); koşucuya da bildirilir.
func (t *answerStyleTurn) note(c answerClass, capTok int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.class, t.cap = c, capTok
	t.mu.Unlock()
	t.conv.noteAnswerClass(string(c))
}

func (t *answerStyleTurn) snapshot() (answerClass, int) {
	if t == nil {
		return classNone, 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.class, t.cap
}

// answerStyleNarration — tek çağrılı anlatım (guided, genel cevap): sınıfı
// not eder; anahtar açık ve sınıf ölçülü ise tavanlı ctx + istem sonu satırı.
// Kapalıyken (ya da report / "") ctx aynen, satır "".
func answerStyleNarration(ctx context.Context, c answerClass) (context.Context, string) {
	t := answerStyleFrom(ctx)
	if t == nil {
		return ctx, ""
	}
	line, capTok := answerClassLines[c], answerClassCaps[c]
	if !t.on || line == "" {
		t.note(c, 0)
		return ctx, ""
	}
	t.note(c, capTok)
	return copilot.WithAnswerCap(ctx, capTok), line
}

// answerStyleLoop — serbest döngünün sınıfı (döngüden hemen önce). Satır ve
// tavan her döngü turunda answerClassLoopPlan ile iner.
func answerStyleLoop(ctx context.Context, question string, st ChatContext, report bool) {
	t := answerStyleFrom(ctx)
	if t == nil {
		return
	}
	c := answerClassHint(question, st, report)
	t.mu.Lock()
	t.rangeS = 0 // döngü penceresi model seçer; guided'ın penceresi bu cevabın değil
	t.mu.Unlock()
	capTok := 0
	if t.on && answerClassLines[c] != "" {
		capTok = max(answerClassCaps[c], answerLoopToolFloor)
	}
	t.note(c, capTok)
}

// answerClassLoopPlan — döngü turunun planına sınıf satırı (EN SONA) + tavan.
// Anahtar kapalı / sınıf ölçüsüz → plan aynen.
func answerClassLoopPlan(ctx context.Context, p stepPlan) stepPlan {
	t := answerStyleFrom(ctx)
	if t == nil || !t.on {
		return p
	}
	c, capTok := t.snapshot()
	line := answerClassLines[c]
	if line == "" || capTok <= 0 {
		return p
	}
	p.SystemAddendum += line
	p.Ctx = withCtx(p.Ctx, func(ctx context.Context) context.Context { return copilot.WithAnswerCap(ctx, capTok) })
	return p
}

// answerEndClean — tavan uygulanmış turda modelin metni yarım cümleyle
// bitiyorsa son tam cümlede keser (sunucu ekleri bundan SONRA gelir).
// Tavansız turda metin aynen.
func answerEndClean(ctx context.Context, text string) string {
	t := answerStyleFrom(ctx)
	if t == nil || !t.on {
		return text
	}
	_, capTok := t.snapshot()
	return endAtSentence(text, capTok)
}

// endAtSentence — SAF: metin tavana yakınsa (bayt ≥ tavan × 1,6 — 2 B/token'lık
// kötü durumda bile tavana değmiş sayılır) ve temiz bitmiyorsa son cümle sonunda
// (". ", "! ", "? ", "…", paragraf sınırı) keser; açık kod çitinin içinde
// kesmez. Cümle sonu yoksa sona "…" eklenir. Kısa ya da temiz biten metin aynen.
func endAtSentence(text string, capTok int) string {
	t := strings.TrimRight(text, " \t\r\n")
	if capTok <= 0 || t == "" || len(t)*10 < capTok*16 || endsClean(t) {
		return text
	}
	cut := lastSentenceEnd(t)
	if cut > 0 && strings.Count(t[:cut], "```")%2 == 1 {
		cut = lastSentenceEnd(t[:strings.LastIndex(t[:cut], "```")])
	}
	if cut <= 0 {
		return t + "…"
	}
	return strings.TrimRight(t[:cut], " \t\r\n")
}

// endsClean — SAF: metin cümle sonu, kapanan çit ya da tablo satırıyla bitiyor.
func endsClean(t string) bool {
	if strings.Count(t, "```")%2 == 1 {
		return false // açık kalmış çit: kesik
	}
	if strings.HasSuffix(t, "```") {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(t)
	return strings.ContainsRune(".!?…|)\"'*_`", r)
}

// lastSentenceEnd — SAF: son cümle sonunun bitiş indeksi (noktalamadan hemen
// sonra boşluk/satır; ondalık "2.5" sayılmaz) ya da son paragraf sınırı. Yoksa 0.
func lastSentenceEnd(t string) int {
	best := 0
	for i := 0; i < len(t); {
		r, w := utf8.DecodeRuneInString(t[i:])
		end := i + w
		if strings.ContainsRune(".!?…", r) && (end == len(t) || t[end] == ' ' || t[end] == '\n') {
			best = end
		}
		i = end
	}
	if p := strings.LastIndex(t, "\n\n"); p > best {
		best = p
	}
	return best
}

// ── Kayıt ─────────────────────────────────────────────────────────────────

// answerClassTool — etkileşim satırındaki ölçüm adımı (ekranda çizilmez, i=-1;
// emsal clarify_reply düşüşü): örnekleme sorguları sınıfa göre gruplar —
// arrayFirst(t -> t.1 = 'answer_class', tools).
const answerClassTool = "answer_class"

// wrapEmit — "done"dan önce sınıf, anahtar açıksa turun etkileşim satırına
// yazılır; v0.10.2332 — "answer" metninin başına varsayım cümlesi
// (chat_answer_slot.go). Kapalıyken emit aynen.
func (t *answerStyleTurn) wrapEmit(ctx context.Context, emit func(string, any)) func(string, any) {
	if t == nil || !t.on {
		return emit
	}
	return func(kind string, payload any) {
		if kind == "answer" {
			clause := t.assumeClause()
			t.noteAssumeEvidence(verifyTurnFrom(ctx), clause)
			payload = withAssumeClause(payload, clause)
		}
		if kind == "done" {
			if c, _ := t.snapshot(); c != classNone {
				if x := chatExchangeFrom(ctx); x != nil {
					args, _ := json.Marshal(map[string]string{"class": string(c)})
					x.noteStep(map[string]any{"tool": answerClassTool, "args": string(args), "i": -1}, time.Now())
				}
			}
		}
		emit(kind, payload)
	}
}
