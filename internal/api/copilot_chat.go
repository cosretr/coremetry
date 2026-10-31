package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/assemble"
	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatMessageTexts — geçmiş turlarının METİN yarısı (rune bütçesi için).
//
// assemble paketi transport tipini BİLMİYOR ve bilmemeli (saf bütçe
// hesabı, şekil-bağımsız); dönüşüm burada, tipin evinde yaşıyor.
func chatMessageTexts(msgs []copilot.ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}

// In-app AI chatbot (v0.6.53). An agentic loop that lets the
// operator ask free-form questions ("why is payment-service slow?",
// "errors in the last hour") and answers them grounded in their own
// telemetry. The LLM's function-calling backend is the SAME tool set
// the MCP server exposes (mcptools.ToolList — TEK kayıt defteri;
// güncel sayı ve katalog orada yaşar, burada sayı tutmuyoruz: iki kez
// bayatladı v0.9.1141'e gelene dek) — so the chat can read live data
// without any new query plumbing. ToolList is the single source; this
// comment stopped counting so it can't drift again.
//
// Transport: POST with the full conversation, response streamed as
// SSE. v1 is STEP-streaming (operator decision 2026-05-28): we emit
// a `step` event per tool call so the operator sees "⚙ list_services"
// progress, then an `answer` event with the final prose. v0.8.404
// adds token streaming to the GUIDED path (copilot_guided.go emits
// `delta` events from StreamText, with a transparent buffered
// fallback); this free tool loop stays buffered — tool-call streaming
// is a different beast (see internal/copilot/stream.go header).
//
// Conversation is EPHEMERAL — the frontend holds history in
// component state and sends it whole each turn; BU handler hiçbir şey
// kalıcılaştırmaz — konuşma kalıcılığı v0.9.1139'dan beri AYRI uçta
// (ai_conversations.go, istemci-güdümlü upsert). Auth: any
// authenticated user — tool'lar read-only + MinRole süzgeçli
// (v0.9.1136), so a viewer chatting is safe.

const (
	chatMaxToolRounds = 5  // guardrail: cap the agentic loop so a model can't fan tool calls forever
	chatMaxMessages   = 40 // cap conversation length fed back to the LLM (token budget)
	// chatMaxToolCalls — N6 (docs/audit/cosre-telemetry-agent.md): alışveriş
	// başına tool-çağrı tavanı. Ajan döngüsü prompt'undaki "en çok 6 tool
	// çağrısı" burada zorlanır; prompt yalnız hatırlatır.
	chatMaxToolCalls = 6
)

// chatCallCapContent — tavan dolunca çalıştırılmayan çağrının sonucu.
const chatCallCapContent = `{"error":"tool çağrı tavanı doldu — çağrı çalıştırılmadı; eldekini raporla"}`

// chatLoopHandlerSeam — v0.10.948 TEST DİKİŞİ (prod'da nil): döngünün uçtan
// uca testinde gerçek katalog (ad, şema, rol süzgeci, Executor) aynen kalır,
// yalnız handler sahte koşucuya bağlanır — CH/ES/VM'e hiç dokunulmaz
// (chat_trace_followup_loop_test.go).
var chatLoopHandlerSeam func(name string, h mcp.ToolHandler) mcp.ToolHandler

// splitByCallBudget — SAF: bir turun çağrılarını kalan bütçeye göre ikiye
// ayırır (çalışacaklar, çalışmayacaklar). Sıra korunur.
func splitByCallBudget(calls []copilot.ToolCall, left int) (run, over []copilot.ToolCall) {
	if left < 0 {
		left = 0
	}
	if len(calls) <= left {
		return calls, nil
	}
	return calls[:left], calls[left:]
}

type chatRequest struct {
	Messages []copilot.ChatMessage `json:"messages"`
	// Context (v0.9.164) — frontend'in bulunduğu sayfadan geçirdiği ipucu
	// (context-awareness): mesaj bir servis ADI taşımıyorsa guided router bu
	// servisi varsayılan alır ("neden yavaş?" checkout sayfasında → checkout).
	// Şeffaf: chat banner'ı "checkout servisindesin" der.
	Context struct {
		Service string `json:"service,omitempty"`
		// Operation (v0.9.184) — the ?op= the operator is viewing on a
		// service page; lets a bare "bu operasyonun durumu" scope RED to
		// that span name (guided router's operation fallback).
		Operation string `json:"operation,omitempty"`
		// Explain (v0.9.479) — AI çekmecesindeki sohbetin bağlam devri:
		// operatörün AZ ÖNCE OKUDUĞU açıklamanın metni (+ kanıt id'leri).
		// Boşken bu dosyadaki her yol bayt-bayt eski davranıştadır; dolu
		// olduğunda guided/explain-grounded ayrımını copilot_drawer.go
		// yönetir (kök neden + tasarım orada yazılı).
		Explain string `json:"explain,omitempty"`
		// Subject (v0.9.482) — çekmecenin öznesi, frontend'in `?ai=` kodeği
		// biçiminde ("trace:<id>", "span:<trace>:<span>", "exception:<fp>").
		// Sunucu bundan İLGİLİ EXPLAIN'İN KANIT PAKETİNİ yeniden kurar
		// (copilot_drawer.go): açıklamanın metni takip sorularına yetmiyordu
		// — "logda ne yazıyor" kör cevaplanıyordu (operatör raporu).
		// Boşken v0.9.479 davranışı bayt-bayt korunur.
		Subject string `json:"subject,omitempty"`
		// RangeS (v0.9.529) — operatörün EKRANDAKİ zaman aralığı,
		// saniye. Soru AÇIK bir pencere taşımıyorsa guided router sabit
		// 30dk yerine bunu kullanır: 6 saatlik pencereye bakarken "hata
		// oranı ne" diye soran operatör, baktığından BAŞKA bir pencerenin
		// cevabını alıyordu ve fark görünmüyordu. Açık pencere taşıyan
		// soru bunu EZER. 0/absent = eski istemci, davranış değişmez.
		RangeS int64 `json:"rangeS,omitempty"`
		// ToMs (v0.10.33) — pencerenin BİTİŞ anı, yalnız operatör MUTLAK
		// bir aralık seçtiğinde (custom/zoom) gönderiliyor. Göreli
		// aralıkta boş kalır ve sunucu şimdiye çapalar; sabitlemek uzun
		// bir soruşturmada cevabı DONDURURDU.
		ToMs int64 `json:"toMs,omitempty"`
		// Env (v0.9.1259) — Topbar'daki global env seçimi. Soru AÇIK bir
		// env adı taşımıyorsa guided router bunu varsayılan alır: ekran
		// uat gösterirken cevabın sessizce başka env'i kapsaması (CoSRE
		// denetim bulgusu) biter. Açık env sorusu her zaman ezer.
		Env string `json:"env,omitempty"`
		// v0.10.183 — istek başına model profili (çoklu model dilim C); bilinmeyen
		// kimlik sessizce varsayılana düşer (copilot.WithProfile aynı kuralı uygular).
		Profile string `json:"profile,omitempty"`
		// Trace (v0.9.537) — operatörün EKRANDA baktığı trace'in ID'si
		// (/trace?id=). "bu trace neden yavaş" gibi ID'siz sorular
		// bununla çözülür; mesajda açık 32-hex varsa o kazanır.
		Trace string `json:"trace,omitempty"`
		// TzOffsetMin (v0.10.437, D6) — tarayıcının UTC'den dakika ofseti
		// (doğu pozitif; İstanbul +180). Mutlak tarih/saat soruları
		// ("08/08/2026 04-08 arası") operatörün yerel saatinde yorumlanır;
		// yoksa UTC (eski istemci).
		TzOffsetMin int `json:"tzOffsetMin,omitempty"`
		// Tz (v0.10.445) — tarayıcının IANA saat dilimi adı ("Europe/Istanbul").
		// Sabit ofset DST'yi bilmez: kışın sorulan yaz tarihi bir saat kayıyordu;
		// ad varsa time.LoadLocation (tzdata gömülü), yoksa ofset.
		Tz string `json:"tz,omitempty"`
		// Conversation (v0.10.478, Faz 4) — kalıcı konuşma kimliği; sunucu
		// bağlam state'i (chat_context.go) buna bağlı. Boş = ilk tur.
		Conversation string `json:"conversation,omitempty"`
		// v0.10.539 (Faz 3.2) — sayfa bağlamı protokolü: istemcinin
		// pageContext(pathname, search) çıktısı (her turda) ve varsa sabitlenmiş
		// bağlam (pin). Eski düz alanlar guided/drawer için aynen kalır.
		Page       *agentctx.PageContext `json:"page,omitempty"`
		PinnedPage *agentctx.PageContext `json:"pinnedPage,omitempty"`
		// WikiRefs (v0.10.1134) — önceki asistan turunun "Wiki · " çip/kaynak
		// href'leri: wiki takip sorusu önceki sayfayı yeniden okur
		// (chat_wiki_followup.go). Geçmiş yalnız {role,text} taşıdığı için
		// yapısal kaynak buradan gelir; yoksa sunucu hafızası / metin url'leri.
		WikiRefs []string `json:"wikiRefs,omitempty"`
		// Scope / Command (v0.10.1138, chat_scope.go) — composer'ın @-anmaları
		// ve /-komutu YAPISAL olarak; router sezgilerinden önce uygulanır.
		// İkisi de yokken her yol bayt bayt eski.
		Scope   *chatScope `json:"scope,omitempty"`
		Command string     `json:"command,omitempty"`
		// Deep (v0.10.1150, chat_deep.go) — composer'ın "Derin düşün" anahtarı.
		// Yokken/false iken her yol bayt bayt eski.
		Deep bool `json:"deep,omitempty"`
		// Code (v0.10.1461, chat_code_turn.go) — composer'ın "Kod" hapı: bağımsız
		// sohbette açık kod turu (yerel profil, dış MCP yok). Yokken/false iken
		// bağımsız sohbet v0.10.1050 duruşunda (kod aracı yok), bayt bayt.
		Code bool `json:"code,omitempty"`
		// CodeLocked (v0.10.1465, chat_code_sticky.go) — istemcinin "bu konuşmada
		// kod okundu" ipucu. Yalnız sıkılaştırır (sunucu işareti TTL'le düştüyse).
		CodeLocked bool `json:"codeLocked,omitempty"`
		// Attempt (CoSRE öğrenme Faz 1) — istemcinin beyanı: retry | regenerate |
		// edit; boş = yeni soru. cosre_interactions.attempt (örtük sinyal).
		Attempt string `json:"attempt,omitempty"`
		// ExplainAsk (v0.10.1400, chat_explain_turn.go) — yapısal açıklama
		// niyeti {kind: trace|exception, id, src}: ✨ düğmesi / nudge / ?ai=
		// derin linki / grup çipi açıklamayı sohbetin turu olarak ister.
		// v0.10.1405 — tür kümesi açıklama kaydından (span, problem, incident,
		// anomaly, service-health, runbook, log; chat_explain_registry.go) +
		// `subject` (`?ai=` kodeği). Yokken her yol bayt bayt eski.
		ExplainAsk *chatExplainAsk `json:"explainAsk,omitempty"`
		// BriefingID / BriefingLine (v0.10.1716, cosre_briefing_ask.go) — soru
		// brifing kartının satırından ("Sor"); yalnız v2 döngüsü okur.
		BriefingID   string `json:"briefingId,omitempty"`
		BriefingLine *int   `json:"briefingLine,omitempty"`
	} `json:"context,omitempty"`
	// Approvals — agent-core S5 (chat_approval_resume.go): v2 onay kartının
	// cevabı; dolu istek onay bekleyen alışverişin DEVAMIDIR (kademeler atlanır,
	// döngü jetonun bütçesiyle sürer). v1 handler'ı okumaz.
	Approvals []approvalAnswer `json:"approvals,omitempty"`
}

// copilotChatWith — sohbet gövdesi (v1 ve v2 taşıma ortak; chat_sink.go).
// Runs the agentic loop and streams progress + the final answer through
// the sink newSink builds. One ai_calls row is written per exchange
// (summing per-round token usage) via RecordUsage.
func (s *Server) copilotChatWith(w http.ResponseWriter, r *http.Request, newSink chatSinkFactory) {
	chatT0 := time.Now() // v0.10.397 — sohbet süresi ai_calls'a
	// v0.10.1604 (operatör kararı 2026-10-29 §4) — v1 ve v2 için TEK gövde
	// duvarı (256 KiB, ai_conversations.go ile aynı); aşan gövde SSE
	// başlığından önce 413. Eskiden v1'de duvar yoktu, v2 400 dönüyordu.
	r.Body = http.MaxBytesReader(w, r.Body, aiChatMaxRequestBytes)
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if code := chatBodyErrStatus(err); code == http.StatusRequestEntityTooLarge {
			http.Error(w, fmt.Sprintf("body > %d KiB", aiChatMaxRequestBytes>>10), code)
			return
		}
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	// v0.10.948 — istemci geçmişi YALNIZ metin turu (chat_history_sanitize.go):
	// rol user/assistant; istemcinin kurduğu araç çağrısı/sonucu sağlayıcıya
	// hiç gitmez. Bütçe (ClampHistory) ve kademeler süzülmüş diziyi görür.
	var droppedTurns, strippedTurns int
	req.Messages, droppedTurns, strippedTurns = sanitizeClientHistory(req.Messages)
	if droppedTurns+strippedTurns > 0 {
		log.Printf("[chat] istemci geçmişi süzüldü: %d tur atıldı, %d turun araç alanı düştü", droppedTurns, strippedTurns)
	}
	if len(req.Messages) == 0 {
		http.Error(w, `{"error":"messages required"}`, http.StatusBadRequest)
		return
	}
	// v0.10.1138 — açık profil seçimi: var mı + rolün seçebilir mi (400/403,
	// SSE başlıklarından ÖNCE; chat_profile_gate.go).
	if !s.chatProfileGate(w, r, req.Context.Profile) {
		return
	}
	// v0.9.1187 (AI Faz 4.5, K3) — geçmiş bütçesi SAYIDAN RUNE'A geçti.
	//
	// Eskiden yalnız "son 40 tur" vardı ve 40 sayısı BOYUT hakkında hiçbir
	// şey söylemiyor: operatörün yapıştırdığı bir log yığını ya da modelin
	// uzun cevapları, 40 turu rahatça bir 2B modelin bağlamının üstüne
	// çıkarıyordu. Belirtisi de sinsi olurdu — kırılma değil, taze kanıtın
	// eski konuşma tarafından bağlamdan atılması.
	//
	// Sayı tavanı KALDI (kısa turlarda bile 40 tur küçük modelde odak
	// kaybettirir); rune bütçesi onun üstüne bindi. Karar saf ve
	// deterministik: assemble.ClampHistory.
	keep, trimmed := assemble.ClampHistory(
		assemble.RuneLens(chatMessageTexts(req.Messages)),
		chatMaxMessages, assemble.HistoryMaxRunes)
	req.Messages = req.Messages[len(req.Messages)-keep:]
	// Kırpma modele SÖYLENİR. Sessiz kırpma, modelin olmayan bir konuşmayı
	// hatırlıyormuş gibi davranmasına yol açar ("az önce dediğin gibi…") ve
	// operatör bunu uydurma sanır — kırpmanın kendisinden pahalı bir hata.
	if note := assemble.TrimNoteIfNeeded(trimmed); note != "" {
		req.Messages = append([]copilot.ChatMessage{{Role: "user", Text: note}}, req.Messages...)
	}

	// v0.10.535 — SSE çıkışı tek yazımda: eager başlık + paylaşılan yazım
	// kilidi + 15 s heartbeat (v0.10.27: serbest döngü buffered, ilk LLM
	// çağrısı 180 s sürebilir, sessiz bağlantı proxy'de kopuyordu) + senkron
	// Close. v0.10.1590 — yazıcı fabrikadan (chat_sink.go): v1 blocks.Emitter,
	// v2 UI Message Stream; üstteki zincir ikisinde de aynı.
	em, ok := newSink(w)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	defer em.Close()
	// v0.9.1229 — ⚙ çip kimliği TEK sayaçtan (chat_step_ids.go): bir istekte
	// birden çok yol adım yayınlayabiliyor; ayrı sayaçlar aynı `i`yi iki kez
	// üretir ve frontend kanıtı yanlış çipe yapıştırırdı.
	emit := withStepIDs(em.Emit)
	// agent-core S5 — onay devamı (yalnız v2): kimlikler aynı asistan mesajında
	// önceki akışın üstünden sayar; kademeler aşağıda atlanır.
	isV2 := isV2Sink(em)
	apCont := newChatApprovalCont(isV2, req.Approvals)
	if apCont.on {
		emit = withStepIDsFrom(em.Emit, apCont.base)
		req.Context.ExplainAsk = nil
	}
	var blockSeq blocks.Sequencer // v0.10.557 — tek sıralayıcı (chart/link/action + guided evidence)
	// v0.10.1444 — sıralayıcının ürettiği `block` çerçeveleri → arşiv zarfı.
	blockCap := &turnBlockCapture{}
	emit = blockCap.wrap(emit) // sıralayıcının ALTINDA: kimlik/sıra atanmış çerçeveyi görür
	emit = withBlockSeq(emit, &blockSeq)

	// Attribution: tag ctx so RecordUsage attributes the exchange to
	// the "chat" surface on the /ai page.
	//
	// exchangeID (v0.8.399, AI audit feedback slice) — one crypto/rand
	// hex id per exchange. Emitted to the UI in the answer event so the
	// thumbs up/down could POST it to /api/ai/feedback (removed v0.10.1393),
	// and threaded via
	// CallMeta into the ai_calls row (exchange_id) so the verdict joins
	// back to the exact call it rates. Provider-agnostic plumbing —
	// works identically for anthropic / openai-compat / github, and for
	// both the guided path and the free tool loop.
	c := auth.FromContext(r.Context())
	uid, email := "", ""
	if c != nil {
		uid, email = c.UserID, c.Email
	}
	exchangeID := newRandID(16)
	// v0.10.1316 — ajan kipi evalset vakası (chat_eval_mode.go): yüzey ve
	// exchange kimliği işaretten; nil (üretim) iken bayt bayt eski.
	evm := chatEvalModeFrom(r.Context())
	if evm != nil && evm.ExchangeID != "" {
		exchangeID = evm.ExchangeID
	}
	// v0.10.24 — UÇTAN UCA TAVAN. Hiçbir katmanda yoktu: handler yalnız
	// tool başına 20s taşıyordu, http.Server'da tavan yok (SSE için
	// DOĞRU — sunucu geneli WriteTimeout her akışı keserdi), istemci ham
	// fetch. Gerçekten sınırsız olan eksen tur başına tool çağrısı
	// SAYISIydı; tool bağlamı bu ctx'ten türediği için tek deadline
	// hepsini kapatıyor (chat_deadline.go).
	exchangeMax := chatExchangeTimeout(s.copilot.ClientTimeout())
	if cosreV2Marked(r.Context()) {
		// araç seti dilim 4 (ai_loop_budget_settings.go) — v2: `ai.loopBudget.maxSeconds`
		// alışveriş tavanını yalnız daraltır; v1 aynen.
		exchangeMax = narrowLoopBudget(chstore.CurrentAILoopBudget(), loopBudget{MaxTime: exchangeMax}).MaxTime
	}
	dctx, cancelExchange := context.WithTimeout(r.Context(), exchangeMax)
	defer cancelExchange()
	callMeta := copilot.CallMeta{
		Surface: "chat", UserID: uid, UserEmail: email, ExchangeID: exchangeID,
	}
	if evm != nil { // v0.10.1316 — RecordUsage satırı evalset- önekli (K2 dışlaması)
		callMeta.Surface = evm.Surface
	}
	ctx := copilot.WithMeta(dctx, callMeta)
	// v0.10.425 (CoSRE denetimi O2) — alışverişin kök span'ı (chat_span.go):
	// kademeler, turlar, araçlar ve CH sorguları bunun altında iç içe geçer.
	// defer: dört erken-dönüş kademesi var, aksi hâlde span sızar.
	ctx, cspan := s.beginChatSpan(ctx, exchangeID)
	defer cspan.end()
	if p := strings.TrimSpace(req.Context.Profile); p != "" {
		// v0.10.183 — seçilen profil bütün alışverişe (guided/intent/loop) uygulanır;
		// yüzey eşlemesini ezer (operatörün açık seçimi). Bilinmeyen id → varsayılan.
		ctx = copilot.WithProfile(ctx, p)
	}
	// v0.10.1150 — "Derin düşün" kararı bir kez türetilir ve ctx ile iner
	// (chat_deep.go); kapalıyken ctx ve olay akışı aynen.
	dm := s.chatDeepMode(r, req.Context.Deep, req.Context.Profile)
	// v0.10.1153 — turun /ai etkileşim satırı (chat_exchange.go): TEK defer,
	// her kademe ve erken dönüş dahil (LLM'siz cevaplar da). emit sarmalanır
	// ki cevap/hata metni okunsun; closure son ctx'i (profil/derin) görür.
	xch := newChatExchange(exchangeID, explainRecordedQuestion(lastUserText(req.Messages), req.Context.ExplainAsk), dm.On, chatT0) // v0.10.1645 — tam kimlik
	xch.setInteraction(req.Context.Conversation, chatUserTurns(req.Messages), req.Context.Attempt,
		agentctx.Sanitize(req.Context.Page), req.Context.Service, req.Context.Trace) // Faz 1 — cosre_interactions
	xch.setBlockCapture(blockCap) // v0.10.1444
	ctx = withChatExchange(ctx, xch)
	ctx = copilot.WithCallSink(ctx, xch.noteModelCall)
	emit = xch.tap(emit)
	defer func() { s.recordChatExchange(ctx, xch, cspan) }()
	if dm.On {
		ctx = withDeepMode(ctx, dm)
		emit("step", map[string]string{"label": dm.stepLabel()})
	}
	// v0.9.528 Faz 2 — model kiminle konuştuğunu bilsin: ad (hitap için)
	// ve rol (viewer'a yapamayacağı eylemi ÖNERMEMESİ için). Ad çözümü
	// /api/auth/me'nin 30s cache'ini kullanır, yani sohbet başına yeni
	// bir FINAL satır okuması EKLEMEZ. Çözülemezse ön-söz boş kalır ve
	// iki prompt da bayt-bayt eskisi olur.
	addressee := s.chatAddressee(r.Context(), c)
	ctx = ctxWithAddressee(ctx, addressee)
	// v0.10.1461 (R2) — bağımsız sohbet kod turu kararı (chat_code_turn.go): hap
	// kapalıyken sıfır değer, ctx ve akış aynen. Kapalı düşüş gerekçesi kademelerden
	// ÖNCE görünür (cevap guided'dan gelse de operatör kodun neden okunmadığını görür).
	ct := s.chatCodeTurn(ctx, req, c, evm != nil)
	if !ct.On {
		if label := ct.stepLabel(false); label != "" {
			emit("step", map[string]string{"label": label})
		}
	}

	// v0.8.397 (AI audit A3) — guided mode first, for EVERY provider:
	// a deterministic intent router recognises the highest-value
	// question shapes, the server prefetches the data, and the model
	// makes exactly ONE tool-less narration call (copilot_guided.go).
	// Deterministic beats tool-roulette on these shapes even for
	// frontier models; the 2B-class primary target (qwen3.5-2b) can't
	// drive the 5-round × 11-schema loop reliably at all. No match →
	// the free tool loop below runs UNCHANGED.
	// v0.10.33 — ZAMAN ÇIPASI, kademelerden ÖNCE hesaplanıyor: guided de
	// serbest döngü de aynı pencereyi görmeli, yoksa aynı soru hangi
	// kademeye düştüğüne göre farklı bir zaman diliminden cevaplanır.
	// v0.10.478 (Faz 4, F4-1) — SUNUCU BAĞLAM STATE'İ: konuşma başına Redis;
	// guided varsayılanı + serbest döngü önsözü + tool'lar (set/get/clear_context)
	// aynı state'i okur; alışveriş sonunda kirliyse yazılır.
	cst := s.loadChatContext(ctx, req.Context.Conversation, len(req.Messages) <= 1) // v0.10.487 — ilk tur köprüyü okumaz
	if evm != nil {
		// v0.10.1316 — değerlendirme vakası konuşmasız: koşuyu başlatanın Redis
		// sohbet bağlamı ne okunur ne yazılır (kullanıcısız durum = flush no-op).
		cst = &chatContextState{}
	}
	ctx = ctxWithChatContext(ctx, cst)
	defer func() {
		fctx, fcancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer fcancel()
		s.flushChatContext(fctx, cst)
	}()
	if chip := chatContextChipTR(cst.ctx); chip != "" {
		emit("step", map[string]string{"label": chip})
	}
	// v0.10.1465 — yapışkan kod konuşması (chat_code_sticky.go): kod okunmuş
	// konuşmanın HER turu her kademeden önce yerel profile sabitlenir; yerel
	// profil yoksa dürüst kapalı cevap, sağlayıcı çağrısı yok.
	ctx, codeSticky, stickyClosed := s.applyCodeSticky(ctx, emit, cst, req.Context.CodeLocked && evm == nil)
	if stickyClosed {
		cspan.tier("code-sticky", true)
		emit("done", map[string]bool{"ok": true})
		return
	}
	anchorTo, anchored := chatAnchorTime(req.Context.ToMs, time.Now())
	if anchored {
		// Çıpa sessizce uygulanmamalı: operatör cevabın GEÇMİŞ bir
		// pencereden geldiğini görebilmeli.
		emit("step", map[string]string{
			"label": "pencere: " + anchorTo.UTC().Format("2006-01-02 15:04 UTC") + "'de bitiyor",
		})
	}

	// v0.10.1400 (Tek CoSRE) — yapısal açıklama turu (chat_explain_turn.go):
	// trace / exception ✨ açıklaması sohbetin İÇİNDE, düğmenin çekirdeği ve
	// önbellek anahtarıyla. Niyet AÇIK olduğu için her metin eşlemesinden
	// (kapsam, hafıza, wiki, operasyon, guided) ÖNCE. Geçersiz niyet → düşer.
	if req.Context.ExplainAsk != nil {
		if handled, xok := s.chatExplainTurn(ctx, emit, req.Context.ExplainAsk, chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin)); handled {
			cspan.tier("explain", xok)
			emit("done", map[string]bool{"ok": xok})
			return
		}
	}

	// v0.10.1138 — yapısal kapsam / eğik komut (chat_scope.go): router
	// sezgilerinden ÖNCE. Kapsam ve komut yoksa kademe hiçbir şeye dokunmaz.
	// v0.10.1139 — çekmece bağlamında @-kapsamı düşer, açık /komut kalır.
	pageTID := ""
	if req.Context.Page != nil {
		pageTID = req.Context.Page.TraceID
	}
	req.Context.Scope = chatScopeDrawerGate(req.Context.Scope, req.Context.Explain, req.Context.Subject, req.Context.Trace, pageTID)
	req.Context.Scope = withWikiMention(req.Context.Scope, lastUserText(req.Messages)) // v0.10.1460 — @wiki metinde de kapsam
	if chatScopeActive(req.Context.Scope, req.Context.Command) {
		rq := chatScopeReq{Msgs: req.Messages, Scope: req.Context.Scope, Command: req.Context.Command,
			Service: req.Context.Service, Env: req.Context.Env, Operation: req.Context.Operation,
			RangeS: req.Context.RangeS, Loc: chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin), AnchorTo: anchorTo}
		var handled, sok bool
		ctx, handled, sok = s.chatScopeTier(ctx, emit, &rq)
		if handled {
			cspan.tier("scope", sok)
			emit("done", map[string]bool{"ok": sok})
			return
		}
		req.Messages, req.Context.Service, req.Context.Env = rq.Msgs, rq.Service, rq.Env
	}

	// v0.10.1289 — hafıza kademesi (chat_memory.go): "hatırla: …", "bunu
	// hatırla", "hangi kuralları biliyorsun". Kapsam kademesinin HEMEN ardından
	// (doğrulanmış @-kapsam ctx'te → kuralın varsayılan kapsamı), wiki / guided /
	// niyet / döngüden önce. Hiçbir şey kaydetmez: onay kartı (action
	// cosre_rule) tık bekler. Eşleşme yoksa akış bayt bayt eski.
	// agent-core S5 — onay devamı kademeleri atlar (asıl tur döngüye düşmüştü);
	// yalnız yapısal kapsam kademesi (yukarıda) aynen koşar.
	memHandled, mok := false, false
	if !apCont.on {
		memHandled, mok = s.chatMemoryTier(ctx, emit, &blockSeq, memoryTierReq{Msgs: req.Messages, CtxService: req.Context.Service})
	}
	if memHandled {
		cspan.tier("memory", mok)
		emit("done", map[string]bool{"ok": mok})
		return
	}

	// v0.10.1571 — düz metin netleştirme cevabı (chat_clarify.go): önceki tur
	// CoSRE bir soru sorduysa ("Coremetry verisinde mi, wikide mi?") bu turun
	// metni cevap olarak yorumlanır; özgün soru seçilen kaynakta yeniden koşar.
	// Bekleyen soru yoksa / cevap tanınmazsa ctx ve mesajlar AYNEN.
	if evm == nil && !apCont.on {
		cr := s.chatClarifyReplyTier(ctx, emit, req.Messages, cst, chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin))
		ctx, req.Messages = cr.ctx, cr.msgs
		if cr.handled {
			switch cr.tier { // literal adlar: ai_surface_registry_test kayıtlı kademeyi denetler
			case "operation":
				cspan.tier("operation", cr.ok)
			case "clarify":
				cspan.tier("clarify", cr.ok)
			case "doc-rows": // v0.10.1576 — doc_or_entity sorusunda doküman seçildi
				cspan.tier("doc-rows", cr.ok)
			default:
				cspan.tier("wiki", cr.ok)
			}
			emit("done", map[string]bool{"ok": cr.ok})
			return
		}
	}

	// v0.10.1124 — AÇIK wiki sorusu ("runbook'u nedir", "wikide nasıl
	// anlatılıyor") guided'dan ÖNCE: servis adı taşıyan wiki sorusu telemetri
	// anlatımına kaçıyordu (chat_wiki_tier.go). İşaretsiz soru bayt bayt eski.
	// İnceleme F1: panel/çekmece/exception/trace bağlamında kademe kapalı.
	wikiTC := wikiTierContext{Explain: req.Context.Explain, Subject: req.Context.Subject,
		Trace: req.Context.Trace, Service: req.Context.Service, PrevWikiRefs: req.Context.WikiRefs}
	if req.Context.Page != nil {
		wikiTC.PageTraceID = req.Context.Page.TraceID
	}
	// v0.10.1143 — içerik yoklaması durumu (chat_wiki_probe.go): kademe ve
	// guidedProblems'in sıfır-eşleşme ipucu aynı tek aramayı paylaşır.
	ctx = s.armWikiProbe(ctx, req.Messages, wikiTC)
	// v0.10.1553 — kaynak keşfi (chat_source_discovery.go): tablo / wiki /
	// telemetri / exceptions paralel ve LLM'siz yoklanır; birden çok kaynakta
	// kanıt varsa en güçlüsü cevaplar, diğerleri alternatif çipi olur. Tek kaynak
	// ya da kanıt yoksa aşağıdaki zincir AYNEN. /telemetri komutu tablo / wiki
	// kademelerini atlatır.
	skipDocs := forcedChatSource(ctx) == srcTelemetry || apCont.on // S5 — onay devamı doküman kademelerini atlar
	// v0.10.1576 (operator-reported) — çıplak takım / servis adı yüklü tabloda da
	// eşleşiyorsa doküman kademeleri kendiliğinden cevaplamaz: düz metin soru
	// (chat_clarify_entity.go). Çıplak varlık değil / eşleşme yok → akış aynen.
	if !skipDocs && evm == nil && s.chatDocOrEntityAskTier(ctx, emit, req.Messages, wikiTC, cst) {
		cspan.tier("clarify", true)
		emit("done", map[string]bool{"ok": true})
		return
	}
	if !skipDocs {
		var dh, dok bool
		dh, dok, emit, skipDocs = s.chatSourceDiscoveryTier(ctx, emit, req.Messages, wikiTC)
		if dh {
			cspan.tier("discovery", dok)
			emit("done", map[string]bool{"ok": dok})
			return
		}
	}
	// v0.10.1176 — yüklenen Excel / CSV'de kimliğin birebir satırları (chat_doc_rows.go):
	// guided'dan ÖNCE ki uygulama kodu telemetriye kaçmasın; telemetri sinyalinde çekilir.
	// v0.10.1551 (operatör: "CRMHouse ait vserver adreslerini getir" → wiki anlatımı
	// 16 satırdan 1'ini okudu) — wiki kademesinden de ÖNCE: güçlü bir değer bir
	// hücreyle birebir eşleşiyorsa TÜM satırlar deterministik gelir; eşleşme yoksa
	// olay basılmaz, wiki kademesi aynen sürer.
	if wikiTierAllowed(wikiTC) && !skipDocs {
		if handled, dok := s.docRowsAnswer(ctx, emit, req.Messages, true); handled {
			cspan.tier("doc-rows", dok)
			emit("done", map[string]bool{"ok": dok})
			return
		}
	}
	if !skipDocs {
		if handled, wok := s.wikiChatAnswer(ctx, emit, req.Messages, wikiTC); handled {
			cspan.tier("wiki", wok)
			emit("done", map[string]bool{"ok": wok})
			return
		}
	}
	// v0.10.1126 — netleştirme kurtarması: guided/niyet yolu YALNIZ "hangisini
	// kastettin?" ile bitecekse ve wiki bu çağırana açıksa önce wiki denenir
	// (chat_disambig_rescue.go). Kapı kapalıyken ctx/emit aynen döner.
	ctx, emit = s.armDisambigRescue(ctx, emit, req.Messages, wikiTC)

	// CoSRE operasyon tespiti Faz 2 (chat_operation_prefetch.go) — mesajda
	// katalogdaki bir operasyon adı varsa model çağrılmadan trace/RED/Oracle
	// getirilir ve serbest döngüye tool kaydı olarak verilir; çok eşleşmede
	// seçim çipleri. Eşleşme yoksa opPre nil ve akış bayt bayt eski.
	ctx = armOutboundSlot(ctx, req.Context.Service, req.Context.Env, int64(req.Context.RangeS), anchorTo) // v0.10.1454 — giden çağrı yuvası (copilot_outbound_call.go)
	opPre, opHandled := s.chatOperationPrefetch(ctx, emit, req.Messages, int64(req.Context.RangeS), anchorTo)
	if opHandled {
		cspan.tier("operation", true)
		emit("done", map[string]bool{"ok": true})
		return
	}
	// v0.10.1399 (operator-reported) — "<OP> operasyonu neden hata veriyor?":
	// ön-getirme Exceptions grubu bulduysa cevap sohbet akışının İÇİNDE grubun
	// Exceptions açıklamasından (aynı girdi + prompt + önbellek;
	// chat_operation_exception_explain.go). Grup yok / okunamadı / soru
	// hata-neden değil → handled=false, akış aşağıda bayt bayt eski.
	if opPre != nil && opPre.errorAsk {
		opPre.screen = opScreenTraceOf(req.Context.Trace, pageTID, req.Context.Subject, req.Context.Service) // v0.10.1572 — sentez sorusunun trace'i
		if handled, eok := s.opExceptionExplainInChat(ctx, emit, opPre, chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin)); handled {
			cspan.tier("operation", eok)
			emit("done", map[string]bool{"ok": eok})
			return
		}
	}
	if opPre == nil && !ct.On { // v0.10.1461 — kod turu guided'ı atlar (chat_code_turn.go)
		handled, gok := false, false
		if !evm.skipGuided() && !apCont.on { // v0.10.1476 — değerlendirme döngü sondası da atlar (chat_eval_mode.go); S5 onay devamı da
			handled, gok = s.copilotChatGuided(ctx, emit, req.Messages, req.Context.Service, req.Context.Operation, req.Context.Explain, req.Context.RangeS, req.Context.Trace, req.Context.Env, anchorTo, req.Context.TzOffsetMin, req.Context.Tz)
		}
		if handled {
			cspan.tier("guided", gok)
			emit("done", map[string]bool{"ok": gok})
			return
		}
	}

	// v0.9.479 — AI çekmecesi sohbeti: ekrandaki açıklama bağlam olarak
	// geldiyse (context.explain) ve guided somut bir özneye oturmadıysa,
	// cevabı tek narration çağrısıyla O AÇIKLAMAYA dayandır. Sıra
	// bilinçli: guided (canlı telemetri, somut özne) > çekmece bağlamı >
	// dokümanlar > serbest döngü. Çekmece sohbeti özne-kapsamlıdır;
	// filo/doküman soruları global CoSRE penceresinde kalır.
	// (ai_calls satırını guided'da olduğu gibi tek narration çağrısı
	// KENDİSİ yazar — burada ikinci bir RecordUsage yok.)
	// v0.9.482 — özne (context.subject) doluysa aynı yol ilgili explain'in
	// HAM KANITINI da yeniden kurup anlatıma katar; kanıt çekilemezse
	// v0.9.479'un metin-tabanlı anlatımı aynen sürer (soft-fail).
	// v0.10.745 — çekmece kanıtındaki damgalar da operatörün diliminde.
	//
	// v0.10.948 (CoSRE Faz B) — TRACE TAKİBİ: özne trace/span ve açıklama
	// bağlamı doluysa (chat_trace_followup.go) guided'ın almadığı takip çekmece
	// anlatımına, RAG'a ve niyet sınıflandırıcısına HİÇ girmez — doğrudan
	// serbest araç döngüsüne: tek-çağrılı anlatım takip sorusunun kanıtını
	// (kıyas, metrik, pod, deploy) toplayamıyordu. Guided yukarıda aynen
	// (yapıştırılan kimlik / somut özne); diğer özneler bayt-bayt eski sırada.
	//
	// v0.10.948 — sayfa bağlamı kapıdan ÖNCE temizlenir: Geçmiş'ten açılan
	// çekmece açıklama gelmeden takip atar; page AYNI trace'i kanıtlıyorsa o
	// takip de döngüye gider (drawerTraceFollowUp). v0.10.542 (Faz 3.4) — açık
	// sayfa yolu: aynı sayfaya giden tool linki "bu sayfada uygula" aksiyonuna
	// dönüşür (chat_actions.go).
	pageCtx := agentctx.Sanitize(req.Context.Page)
	pagePath, pageTraceID := "", ""
	if pageCtx != nil {
		pagePath, pageTraceID = pageCtx.Path, pageCtx.TraceID
	}
	traceSubj, isTraceFollowUp := drawerTraceFollowUp(req.Context.Explain, req.Context.Subject, pageTraceID)
	if !isTraceFollowUp && !ct.On && opPre == nil { // v0.10.1461 — kod turu da orta kademeleri atlar
		if !apCont.on { // agent-core S5 — onay devamı orta kademeleri de atlar
			// v0.10.1571 — kaynak sorusu (chat_clarify.go): telemetri şekilli soru
			// guided'a oturmadı ve wiki gerçek bir alternatif → yalnız "Coremetry
			// verisinde mi, wikide mi?" sorulur (düz metin, LLM'siz). Kapı kapalıysa
			// zincir bayt bayt eski.
			if evm == nil && s.chatSourceAskTier(ctx, emit, req.Messages, wikiTC, cst) {
				cspan.tier("clarify", true)
				emit("done", map[string]bool{"ok": true})
				return
			}
			// v0.10.1053 — exception takibinin tek araçlı kod döngüsü bu alışverişin
			// audit'ini, span'ını, tavanını kullanır (chat_exception_followup.go).
			drawerEnv := drawerLoopEnv{r: r, span: cspan, exchangeMax: exchangeMax, started: chatT0}
			if handled, dok := s.copilotChatDrawer(ctx, emit, req.Messages, req.Context.Explain, req.Context.Subject, req.Context.Service, chatLocationNamed(req.Context.Tz, req.Context.TzOffsetMin), drawerEnv); handled {
				cspan.tier("drawer", dok)
				emit("done", map[string]bool{"ok": dok})
				return
			}

			// v0.8.438 — doküman RAG yolu: guided telemetri router'ı
			// eşleşmediyse ve soru yüklü dokümanlara yeterince benziyorsa
			// (skor tabanı) tek narration çağrısıyla kaynak atıflı cevap.
			// Sıra bilinçli: telemetri şekilleri > dokümanlar > serbest döngü.
			if !evm.skipGuided() { // v0.10.1476 — döngü sondası doküman RAG'ını da atlar
				if handled, rok := s.ragChatAnswer(ctx, emit, req.Messages, req.Context.Service); handled {
					cspan.tier("rag", rok)
					emit("done", map[string]bool{"ok": rok})
					return
				}
			}

			// v0.10.172 — kademe 3.5: LLM niyet sınıflandırıcısı (copilot_intent.go).
			// Deterministik router (kademe 1) ve RAG eşleşmeyen serbest soruyu
			// küçük model tek katı-JSON çağrısıyla kılavuz niyetine eşler; eşlerse
			// cevap AYNI prefetch→anlatım yolundan (runGuidedRoute). Eşlemezse
			// ayara göre öneri çipleri (on_no_loop, prod varsayılanı) ya da aşağıdaki
			// serbest döngü (on). Sınıflandırıcı hatası sessizce düşer — döngü sürer.
			// v0.10.1321 (operatör) — ajan kipi evalset vakasında niyet kademesi
			// kapalı (chatEvalMode.SkipIntent): vaka araç döngüsünü ölçer. nil
			// işarette skipIntent false → üretim aynen.
			if !evm.skipIntent() {
				if handled, iok := s.copilotChatIntent(ctx, emit, req.Messages, req.Context.Service, req.Context.Operation, req.Context.Explain, req.Context.RangeS, req.Context.Env, anchorTo, req.Context.TzOffsetMin, req.Context.Tz); handled {
					cspan.tier("intent", iok)
					emit("done", map[string]bool{"ok": iok})
					return
				}
			}
		}
	}

	// Build the tool set once (closures over the live store + logs)
	// and the LLM-facing specs from the same list.
	//
	// v0.9.1136 (AI Faz 3.1) — rol filtresi: MinRole'ü çağıranın
	// rolünü AŞAN tool ne LİSTELENİR ne de ÇAĞRILABİLİR. Gizlemek
	// reddetmekten iyidir (reddedilen tool bir tur + bağlam harcar),
	// ama yalnız gizlemek yetmez: serbest döngü ismi byName'den
	// çözüyor, yani filtre İKİ map'e de uygulanır — model adı
	// tahmin etse bile "unknown tool" alır.
	role := ""
	if c != nil {
		role = c.Role
	}
	// v0.10.1302 — CoSRE: modele göre araç seti. Çözülen profil (model seçicisi /
	// Derin profili WithProfile ile ctx'te) simple ise serbest-sorgu araçları
	// (query_metric / search_logs / search_traces / list_metric_labels) bu turda
	// sunulmaz; byName de bu listeden kurulur → gizli ad "unknown tool". Katalog
	// metni (ChatDescriptionFor) ve döngü prompt'u aynı varyantı okur; etkileşim
	// satırı (cosre_interactions.toolset) hangi varyantın koştuğunu kaydeder.
	// v0.10.1461 (R2) — kod turu: alışveriş araç seti okunmadan ÖNCE yerel
	// profile sabitlenir ve işaretlenir (katalog o profilin araç setiyle kurulur).
	extReady := s.mcpClient != nil && s.mcpClient.Configured()
	if ct.On {
		ctx = s.applyCodeTurn(ctx, &ct) // yerel profil karardan sonra kaybolduysa kapalı düşer
		emit("step", map[string]string{"label": ct.stepLabel(extReady)})
		if ct.On {
			noteCodeRead(cst, emit) // v0.10.1465 — kod turu konuşmayı yapışkan yapar
		}
	}
	toolset := s.copilot.ToolsetFor(ctx)
	if evm != nil && evm.Toolset != "" {
		toolset = evm.Toolset // v0.10.1316 — koşunun araç seti (çözüm ya da açık seçim)
	}
	simpleTools := toolset == modelcaps.ToolsetSimple
	xch.setToolset(toolset)
	// Araç YÜRÜTMESİ de aynı varyantı görsün: sonuç ipuçları / notlar / hata
	// metinlerindeki ⟦full|simple⟧ işaretleri ctx'ten basılır (exec.Call bu
	// ctx'ten türer); dış MCP istemcisi işaretsiz ctx ile full kalır.
	ctx = mcptools.WithToolset(ctx, toolset)
	// v0.10.1451 — yerli katalog kurucusu (araç setine göre); localOnly yerel
	// profile geçerse o profilin araç setiyle bir kez daha çağrılır (aşağıda).
	nativeTools := func(toolset string) ([]mcp.Tool, bool) {
		tools := toolsForRole(mcptools.ChatToolListFor(s.mcpDeps(), toolset), role) // v0.10.993 — dış-yalnız araçlar (bubble_up) hariç
		if cosreV2Marked(ctx) {
			// araç seti dilim 2 (chat_user_scope.go) — v2-yalnız araçlar (list_my_services); v1 kataloğu aynen.
			// dilim 3 — aynı adlı v2 aracı (search_logs) v1'inkinin yerini alır.
			tools = mcptools.V2MergeTools(tools, toolsForRole(mcptools.V2OnlyTools(s.mcpDeps()), role))
		}
		if evm != nil {
			tools = evalAgentTools(tools) // v0.10.1316 — sohbet bağlamı araçları düşer
		}
		// v0.10.1050 — read_source_code YALNIZ panel trace takibinde ve oturum
		// kullanıcısına (API token'ı değil) (chat_source_code.go). Katalog/spec/prompt
		// bundan kurulur. v0.10.1461 (R2) — bağımsız sohbette YALNIZ açık kod
		// turunda (+ search_code); 1209'un örtük yolu kalktı (chat_code_turn.go).
		codeScope := !isTraceFollowUp && ct.On
		tools = sourceCodeToolsFor(tools, isTraceFollowUp, c, codeScope)
		tools = standaloneSearchCodeFor(tools, isTraceFollowUp, codeScope)
		return tools, codeScope
	}
	tools, codeScope := nativeTools(toolset)
	if codeScope {
		ctx = withResolvedCodeScope(ctx)
	}
	// v0.10.88 (dilim ③) — DIŞ MCP tool'ları yerli kataloğun yanına. Aynı
	// toolsForRole süzgeci, aynı spec şekli; döngü dış/yerli ayrımını görmez.
	// Katalog Registry'nin 5 dk TTL'li önbelleğinden gelir; sunucu erişilemezse
	// boş düşer ve sohbet YERLİ tool'larla aynen sürer (soft-fail).
	// v0.10.1122 — katalog wiki kararından ÖNCE kurulur: dış araç varsa wiki
	// araçları bu turda sunulmaz (wikiToolsFor).
	// v0.10.948 — trace takibi YALNIZ yerli salt-okur katalog: dış MCP tool'ları (yazma yetkisi Coremetry'de denetlenmez, chat_mcp_bridge.go) çekmeceye sızmaz — DECISIONS v0.10.86-89 "guided/drawer/RAG'a sızmaz" + Faz B gereksinim 7.
	var extTools []mcp.Tool
	// v0.10.1461 (R2) — kod turunda dış MCP KURULMAZ: katalog, köprü, extNames boş.
	// v0.10.1465 — yapışkan kod konuşmasında da (hap ne olursa olsun).
	if !isTraceFollowUp && evm == nil && !ct.On && !codeSticky && s.mcpClient != nil && s.mcpClient.Configured() { // v0.10.1316 — değerlendirmede dış MCP yok
		ext := externalChatTools(
			s.mcpClient.Registry().Tools(ctx),
			s.mcpClient.ToolRules,
			s.mcpClient.Registry().Call,
			func(server, tool string, args json.RawMessage) {
				// Her dış çağrı iz bırakır: modelin konuştuğu dış uç,
				// operatörün "bunu kim/ne çağırdı" sorusunun konusu.
				s.audit(r, "mcp.call", "mcp_server", server, mcpCallAuditDetails(tool, args))
			})
		extTools = toolsForRole(ext, role)
	}
	// v0.10.1122 — search_wiki / read_wiki_page YALNIZ oturum kullanıcısına (API
	// token'ı değil) ve YALNIZ dış MCP aracı olmayan turda (onaysız döngüde wiki
	// metni dış araç argümanına taşınamasın; wiki o zaman yalnız RAG kademesinden).
	// Wiki kapalıysa Deps.Wiki nil → zaten yoklar (chat_wiki.go).
	// Tur arası taşıma da kapansın: dış MCP YAPILANDIRILMIŞSA (katalog bu tur
	// boş düşse bile) wiki araçları sunulmaz.
	extConfigured := !isTraceFollowUp && s.mcpClient != nil && s.mcpClient.Configured()
	if ct.On || codeSticky {
		extConfigured = false // v0.10.1461 (R2) — kod turunda dış araç kurulmadı → wiki araçları kalır (ikisi de yerel modele); v0.10.1465 — yapışkan konuşmada da
	}
	tools = wikiToolsFor(tools, c, extConfigured || len(extTools) > 0)
	// v0.10.1451 — R1 localOnly kapısı (karar 8; chat_local_only.go): katalogda
	// kod/wiki aracı varsa alışveriş YEREL profile sabitlenir ve işaretlenir
	// (her model turu copilot kapısından geçer). Yerel profil yoksa araçlar
	// düşer — katalog ve döngü prompt'u o özellikler kapalıymış gibi bayt bayt.
	var lo localOnlyChatDecision
	ctx, lo = s.chatLocalOnly(ctx, tools)
	switch lo {
	case localOnlyDrop:
		tools = dropSensitiveChatTools(tools)
		emit("step", map[string]string{"label": "Kod/wiki okuma " + localOnlyClosedReason})
	case localOnlySwitch:
		// v0.10.1467 — geçiş görünür (kod turu kendi step'inde söyler; orada
		// profil zaten yerel → buraya localOnlyKeep ile gelir, çift step yok).
		emit("step", map[string]string{"label": localOnlySwitchLabel(s.chatProfileLabel(ctx))})
		if evm == nil || evm.Toolset == "" {
			if ts := s.copilot.ToolsetFor(ctx); ts != toolset {
				toolset = ts
				simpleTools = toolset == modelcaps.ToolsetSimple
				xch.setToolset(toolset)
				ctx = mcptools.WithToolset(ctx, toolset)
				tools, _ = nativeTools(toolset)
				tools = wikiToolsFor(tools, c, extConfigured || len(extTools) > 0)
			}
		}
	}
	// agent-core S5 — onay-kapılı araçlar (chat_approval_resume.go): yalnız v2,
	// ayar ai.toolApproval açık + listede, rol yeter, problem sayfası. Ayar
	// kapalıyken nil → katalog bayt aynı.
	apTools, apGated, apCfg := s.chatApprovalCatalog(r, pageCtx, isV2, evm != nil, isTraceFollowUp, role)
	tools = append(tools, apTools...)
	if evm != nil && evm.OnCatalog != nil { // v0.10.1476 — sunulan katalog (değerlendirme izi)
		names := make([]string, 0, len(tools)+len(extTools))
		for _, t := range tools {
			names = append(names, t.Name)
		}
		for _, t := range extTools {
			names = append(names, t.Name)
		}
		evm.OnCatalog(names)
	}
	byName := make(map[string]mcp.ToolHandler, len(tools))
	specs := make([]copilot.ToolSpec, 0, len(tools))
	// v0.9.1230 (AI perf) — katalog DİYETİ: spec'e t.Description değil
	// t.ChatDescription() girer.
	//
	// Ölçüm: 33 tool = 24.268 B İngilizce açıklama + 17.672 B şema =
	// 41.940 B, ve bu katalog her ChatWithTools turunda (aşağıdaki
	// döngü, chatMaxToolRounds=5) YENİDEN gönderiliyordu. Yerel gemma4
	// için bu, copilot_guided.go başlığında adı konmuş başarısızlık
	// modunun ("schema soup") doğrudan kaynağı. ChatDescription() aynı
	// KAYIT DEFTERİNDEN türeyen kompakt Türkçe görünümü verir; MCP
	// tools/list dış istemciye tam İngilizce sözleşmeyi servis etmeye
	// devam eder (mcptools/short_desc_test.go iki yönü de pinler).
	//
	// Şemalar bilinçli olarak DOKUNULMADAN kalıyor: arg adları ve
	// varsayılan/tavan şerhleri doğru ÇAĞRININ koşulu — orada kazanılan
	// bayt, yanlış argümanla harcanan bir tura değmez.
	// v0.10.1274 — ekran kapsamı enjeksiyonunun şema haritası: YALNIZ yerli
	// araçlar (dış MCP argümanlarına dokunulmaz; chat_tool_scope.go).
	toolSchemas := make(map[string]map[string]any, len(tools))
	for _, t := range tools {
		byName[t.Name] = t.Handler
		toolSchemas[t.Name] = t.InputSchema
		specs = append(specs, copilot.ToolSpec{
			Name: t.Name, Description: t.ChatDescriptionFor(simpleTools), InputSchema: mcp.RenderSchemaToolsetMarkers(t.InputSchema, simpleTools), // v0.10.1302 — ⟦full|simple⟧
		})
	}
	// Dış MCP araçları (yukarıda kuruldu) yerli kataloğun ardına; bütçe
	// (clampToolResultForModel) ve hata (ToolErrorJSON) yolları değişmeden işler.
	extNames := map[string]bool{} // v0.10.425 — ai.tool köken etiketi (native | external)
	for _, t := range extTools {  // v0.10.1122 — katalog wiki kararından ÖNCE kuruldu (yukarıda)
		extNames[t.Name] = true
		byName[t.Name] = t.Handler
		specs = append(specs, copilot.ToolSpec{
			Name: t.Name, Description: t.ChatDescription(), InputSchema: t.InputSchema,
		})
	}

	// CoSRE Faz-2 — render_chart server-side emission: the model PICKS
	// the chart by calling the render_chart tool; the SERVER builds the
	// deterministic ```chart``` fence from the handler's VALIDATED spec
	// (chartFence, copilot_guided.go) and appends it to the final
	// answer. A gemma4-class small model never formats chart JSON, and
	// a hallucinated service/metric never reaches the UI (the handler
	// returns ok:false for those). Blocks accumulate across rounds,
	// deduped by service+operation+agg.
	var chartBlocks []string
	// v0.10.541 (Faz 3.3a) — tipli bloklar (event: block), eski çerçevelerle paralel.
	// v0.10.542 (Faz 3.4) — açık sayfa yolu (pagePath) trace takibi kapısının
	// üstünde kurulur (v0.10.948).
	chartSeen := map[string]bool{}
	appendCharts := func(text string) string {
		// v0.10.47 — MODELİN KENDİ ÇİTİ ÖNCE SÖKÜLÜR.
		//
		// Sunucu çitleri metnin SONUNA ekleniyor; modelin metnine hiç çit
		// koymuyoruz. Yani `text` içinde bulunan her ```chart``` çiti tanım
		// gereği model-yazımıdır ve kapsamı DOĞRULANMAMIŞTIR — arayüz ikisini
		// ayırt edemiyor (chatMarkdown.ts yalnız lang'a bakıyor) ve uydurma
		// bir kapsamla CANLI grafik çiziyordu.
		//
		// Sökme, erken dönüşün ÜSTÜNDE: hiç sunucu grafiği olmayan bir turda
		// da model çit yazabilir — asıl tehlikeli hâl tam olarak odur.
		text, _ = stripModelChartFences(text)
		if len(chartBlocks) == 0 {
			return text
		}
		return strings.TrimRight(text, "\n") + "\n" + strings.Join(chartBlocks, "")
	}

	conv := opPre.seed(req.Messages) // operasyon ön-getirmesi varsa tool kaydı (nil → aynen)
	// Taşma yeniden denemesi TEK sefer: ikinci bir deneme aynı duvara
	// bir çağrı daha yakar (kod-explain'in `hb == block` kontrolüyle
	// aynı gerekçe).
	overflowRetried := false
	// v0.10.29 — döngü boyunca çağrılan araçlar; cevabın altındaki
	// deterministik "Kaynak:" künyesini besliyor (chat_source_note.go).
	var calledTools []string
	noteOffered := false // v0.10.1169 — tur başına en çok bir "probleme not" aksiyonu
	hypOffered := false  // v0.10.1288 — tur başına en çok bir "hipotezi doğrula" aksiyonu
	// v0.10.1291 — kural-türevli takip çiplerinin olguları (chat_followup_chips.go);
	// yalnız başarılı, veri döndüren araç sonuçlarından toplanır.
	fuFacts := opPre.followupFacts() // v0.10.1331 Exceptions grubu + v0.10.1388 "<op> örnek trace topolojisi" (nil → yok)
	// v0.10.1050 — okunan kodun MASKELİ özetleri (ai_calls örneği; kod yok).
	var codeReads []string
	// v0.10.948 — trace takibinin sayı denetimi kanıtı: sunucu bağlamı + operatör
	// mesajları + YÜRÜTÜLEN çağrıların argümanı ve tam çıktısı (chat_trace_followup.go).
	var fuEvidence strings.Builder
	var totalIn, totalOut, totalCached uint32 // v0.10.807 — cached: önek önbelleği toplamı
	var lastErr error
	var finalText string
	// v0.9.1181 (Faz 4.3) — ⚙ çipi ile onun "veriyi göster" bloğunu eşleyen
	// sayaç. İstek boyunca tekil olmalı: çipler tur döngüsü boyunca birikiyor,
	// tur-içi indeks ikinci turda çakışırdı. v0.9.1229 — sayının kendisi
	// artık emit sarmalayıcısında; burada yalnız SON çipin kimliği tutulur
	// (eşli step-result için).
	stepN := 0
	// v0.9.1228 — döngü boyunca biriken ürün köprüleri (toolCallLink):
	// cevap çipi olarak yayınlanır; request-ID linkleriyle birleşir.
	var loopLinks []guidedAnswerLink
	// v0.10.495 — döngünün SON trace araması (birebir deep_link); cevabın
	// `open` alanı bundan türer (answerOpenHref: fiil kapısı).
	// v0.10.944 — /logs? bağlantısı da taşıyabilir, ama YALNIZ döngüde trace
	// araması olmadıysa (nextLoopOpen, chat_tool_links.go).
	var loopOpen string
	// v0.10.1272 — kanıt zarflı araçların (query_metrics/traces/logs) kanıtı:
	// step-result.evidence + answer.evidence[] (chat_evidence.go).
	var loopEvidence []mcptools.ChatEvidence
	// v0.10.1324 — başarılı araç önizlemeleri: yineleme düşüşünün deterministik
	// kanıt özeti (chat_answer_degen.go chatDegenFallbackAnswer).
	var stepDigests []chatStepDigest
	// v0.9.528 Faz 2 — serbest döngünün sistem prompt'u da kiminle
	// konuşulduğunu taşır. Ön-söz boşsa sabitin aynısı.
	// v0.10.32 — EKRAN BAĞLAMI. İlk üç kademe req.Context.* alıyordu,
	// serbest döngü HİÇBİRİNİ almıyordu; üstelik prompt "aksini
	// söylemedikçe 1800 (30 dk) kullan" diyor. Ekranda checkout-service
	// açık ve 6 saatlik pencere seçiliyken sorulan soru filo geneline ve
	// 30 dakikaya gidiyordu (chat_screen_context.go).
	// v0.10.944 — pin bir kez temizlenir; hem servis düşümü hem sayfa önsözü kullanır.
	pinnedCtx := agentctx.Sanitize(req.Context.PinnedPage)
	// v0.10.948 — trace takibinde BAŞKA trace'in (ya da traceId'siz) sayfası üç
	// önsözden de düşer; env de: çekmecede ikisi aynı bayat traceCtx'ten.
	// AKTİF BAĞLAM, EKRAN BAĞLAMI ve AÇIK SAYFA böylece aynı şeyi söyler.
	// pagePath istekten kalır (yalnız "bu sayfada uygula" link eşlemesi).
	loopEnv := req.Context.Env
	if isTraceFollowUp && pageCtx != nil && traceFollowUpPage(traceSubj, pageCtx) == nil {
		pageCtx = nil
		loopEnv = ""
	}
	screenCtx := ChatScreenContext{
		Service:   freeLoopScreenService(req.Context.Service, pageCtx, pinnedCtx),
		Operation: req.Context.Operation,
		Env:       loopEnv,
		RangeS:    req.Context.RangeS,
		AnchorTo:  anchorTo,
		Anchored:  anchored,
	}
	// v0.10.50 — ÇIPA ARAÇLARA DA GEÇER.
	//
	// v0.10.33 çıpayı burada operatöre (çip) ve modele (önsöz) İLAN
	// ediyordu, ama araç katmanına hiç ulaşmıyordu: mcptools.rangeWindow
	// koşulsuz `time.Now()` kuruyordu ve hiçbir tool mutlak pencere
	// argümanı almıyor (ev kuralı — küçük modele epoch hesaplatma yok).
	// Yani model BUGÜNÜN sayısını okuyup önsöze uyarak DÜNÜN penceresi
	// diye yazıyordu; çip de o yanlışı TEYİT ediyordu.
	//
	// Etiketli yanlış, etiketsiz yanlıştan tehlikeli: sorgulanmıyor.
	if anchored {
		ctx = mcptools.WithAnchor(ctx, anchorTo)
	}
	// Bağlam SESSİZCE uygulanmamalı: operatör cevabın neden o kapsamda
	// olduğunu görebilmeli (v0.9.1259 env şeffaflığının kalan yarısı).
	if chip := screenContextChipTR(screenCtx); chip != "" {
		emit("step", map[string]string{"label": chip})
	}
	// v0.10.806 (dış skill denetimi L2) — SIRA BİLİNÇLİ BÖYLE KALDI: skill
	// "sabit önek önce" der ama v0.10.33 kuralı (chat_screen_context_test)
	// ekran önsözünün sohbet çekirdeğinden ÖNCE gelmesini ister — küçük
	// model çelişkide ilk gördüğünü izler (çekirdeğin 1800 s varsayılanı).
	// Bugün önbelleklenebilir önek = ajan döngüsü bloğu; tam sabit-önce
	// sıra ancak evalset kanıtıyla (operatör kararı). Ölçüm: ai_calls
	// cached_tokens (v0.10.807).
	// v0.10.948 — trace takibi: AKTİF BAĞLAM (özneden + aynı trace'in page'inden),
	// takip talimatı (copilot.TraceFollowUpAddendum) ve önceki açıklama VERİ
	// bloğu olarak; sohbet çekirdeğinden ÖNCE — DataNotInstruction sonda kalır.
	var traceFU *traceFollowUp
	// v0.10.948 — Geçmiş devralmasında açıklama yok: sıfır araçlı cevap
	// "önceki CoSRE açıklaması"na dayandığını iddia etmez, genel künye kalır.
	fuHadExplain := isTraceFollowUp && strings.TrimSpace(req.Context.Explain) != ""
	if isTraceFollowUp {
		tf := buildTraceFollowUp(traceSubj, pageCtx, loopEnv, req.Context.RangeS, anchorTo, time.Now())
		tf.Simple = simpleTools // v0.10.1302 — takip talimatı araç setine göre
		// v0.10.1147 — ilk cevabın span exception özeti takibe de (aynı okuma,
		// aynı blok; explain_trace_events.go). Okunamazsa "" ve takip aynen.
		tf.Events, tf.Chain, tf.Critical = s.traceFollowUpEvents(ctx, tf.TraceID) // v0.10.1371 — + hata zinciri; v0.10.1627 — + kritik yol (aynı okuma)
		traceFU = &tf
		emit("step", map[string]string{"label": traceFollowUpChipTR(tf)})
		// v0.10.1050 — read_source_code sürümü argümansız çağrıldığında öznenin
		// span'lerinden çözer (chat_source_code.go; tembel, alışveriş başına bir okuma).
		ctx = withSourceSubject(ctx, tf.TraceID, tf.SpanID)
	}
	// v0.10.1274 — ekran kapsamı ARAÇLARA (chat_tool_scope.go): env/servis/
	// pencere/trace argümanı model vermediyse sunucu koyar, künyesi çipte.
	scopeTrace := req.Context.Trace
	if traceFU != nil {
		scopeTrace = traceFU.TraceID
	}
	toolScope := buildToolScreenScope(screenCtx, pageCtx, pinnedCtx, scopeTrace, lastUserText(req.Messages))
	// v0.10.1289 — operatör kuralları (cosre_rules_prompt.go): bağlama uyan ≤5
	// kural çitli veri bloğu + ek olarak sohbet bağlamı önsözünün ARKASINA;
	// araç argümanlarına / router'a girmez. Kural yoksa prompt bayt bayt eski.
	rulesSvc := req.Context.Service
	if rulesSvc == "" {
		rulesSvc = cst.ctx.Service
	}
	rulesBlock, rulesN := s.cosreRulesBlock(ctx, rulesSvc)
	if label := cosreRulesStepLabel(rulesN); label != "" {
		emit("step", map[string]string{"label": label})
	}
	loopPrompt := copilot.SystemPromptChatAgentLoopFor(simpleTools) + // v0.10.482 — telemetri ajanı çekirdek döngüsü (Ek A); v0.10.1302 — araç setine göre
		screenContextPreambleTR(screenCtx) +
		agentctx.PreambleTR(pageCtx, pinnedCtx) + // v0.10.539 — sayfa bağlamı (pin önce)
		chatContextPreambleTR(cst.ctx) + // v0.10.478 — aktif sohbet bağlamı (Ek A ACTIVE_CONTEXT)
		cosreRulesLoopPromptTR(rulesBlock) + // v0.10.1289 — kural yoksa ""
		traceFollowUpPromptTR(traceFU, req.Context.Explain) + // v0.10.948 — boşsa ""
		chatSourceCodePromptTR(tools) + // v0.10.1050 — read_source_code sunulmuyorsa ""
		chatCodeTurnPromptTR(ct.On && hasTool(tools, mcptools.SourceCodeToolName)) + // v0.10.1461 — kod turu yoksa ""
		chatWikiPromptTR(tools) + // v0.10.1122 — wiki araçları sunulmuyorsa ""
		chatCodeResolvePromptTR(tools) + // v0.10.1210 — iş kodu / kod arama eki
		opPre.exceptionsPromptTR() + // v0.10.1331 — operasyonun Exceptions grubu yoksa ""
		opPre.clusterPromptTR() + // v0.10.1374 — küme kıyası yoksa ""
		dm.loopPrefix() + // v0.10.1150 — Derin kapalıyken ""
		withAddressee(addressee, copilot.SystemPromptChat())
	// v0.10.1324 — yankı süzgecinin cümleleri loopPrompt'a giren AYNI önsöz
	// kurucularından, aynı girdilerle (chatLoopPreambles) + tavan / yeniden
	// deneme ekleri (chat_answer_degen.go).
	echoPhrases := chatEchoPhrases(append(chatLoopPreambles(screenCtx, pageCtx, pinnedCtx, cst.ctx),
		copilot.ChatRoundCapAddendum(), copilot.ChatDegenRetryAddendum())...)
	// araç seti dilim 2 (chat_user_scope.go) — YALNIZ v2: kullanıcı kapsamı bir
	// kez çözülür, data-scope olarak söylenir ve döngü planına (prepareStep) iner.
	// v1'de işaret yok → dal koşmaz, akış bayt bayt eski.
	if cosreV2Marked(ctx) {
		teamOverride := ""
		if evm != nil {
			teamOverride = evm.UserTeam
		}
		us := s.resolveUserScope(ctx, requestScopeEscape(req.Context.Scope, req.Context.Command), teamOverride)
		if evm != nil && evm.OnScope != nil {
			evm.OnScope(us)
		}
		emit(chatScopeEventKind, us.event())
		v2p := &v2LoopPlan{Scope: us, Addendum: userScopeAddendum(us, req.Messages) +
			s.briefingAskAddendum(ctx, us, req.Context.Env, req.Context.BriefingID, req.Context.BriefingLine),
			Budget: narrowLoopBudget(chstore.CurrentAILoopBudget(), v2LoopCeilings(dm, exchangeMax))} // dilim 4
		if v2p.Addendum != "" {
			echoPhrases = chatEchoPhrases(append(chatLoopPreambles(screenCtx, pageCtx, pinnedCtx, cst.ctx),
				copilot.ChatRoundCapAddendum(), copilot.ChatDegenRetryAddendum(), v2p.Addendum)...)
		}
		ctx = withV2LoopPlan(ctx, v2p)
	}
	if isTraceFollowUp {
		// v0.10.948 — sayı denetiminin tohumu: açıklamasız AKTİF BAĞLAM + ekran ve
		// sayfa önsözleri + operatör turları. Önceki açıklama YOK (ilk cevabın sayı uyarısı
		// tam da işaretlediği sayıları temellendirirdi).
		fuEvidence.WriteString(traceFollowUpEvidenceSeed([]string{
			traceFollowUpPromptTR(traceFU, ""), screenContextPreambleTR(screenCtx), agentctx.PreambleTR(pageCtx, pinnedCtx),
		}, req.Messages))
	}

	// v0.10.88 — TEKRAR MUHAFIZI (exchange kapsamı). v0.10.84 prompt'u
	// "aynı tool'u aynı argümanlarla iki kez çağırma" diyor; bu harita
	// yasağı ZORLAMAYA çevirir (devops huntWindows `tried` deseni).
	// Dış tool'da bedel ağ + audit satırı, yerlide CH sorgusu — ikisi de
	// aynı cevabı ikinci kez satın almaya değmez.
	// v0.10.536 (Faz 2.3) — tool YÜRÜTME tek yol (ai/agent/tools.Executor):
	// bilinmeyen ad, tekrar muhafızı, kapsam (bugün kısıtsız), 20 s bütçe,
	// JSON'lama, ToolErrorJSON, ai.tool span'ı ve audit satırı orada; döngü
	// yalnız Outcome'u yayınlar (çip, kanıt, köprü) ve konuşmaya ekler.
	if rs := resolvedCodeScopeFrom(ctx); rs != nil {
		for n, h := range byName {
			byName[n] = rs.wrap(n, h)
		}
		// v0.10.1461 (R2) — tohum: ekrandaki servis (istek / sayfa bağlamı) +
		// turun audit satırı (kod yok; araç çağrıları ayrıca mcp.tool.call).
		seeds := codeTurnSeeds(screenCtx.Service, toolScope.Service)
		rs.add(seeds...)
		extServers, extDropped := s.externalToolsCached()
		s.audit(r, "copilot.code_turn", "chat", exchangeID,
			codeTurnAuditDetails(ct.Profile, extServers, extDropped, seeds, hasTool(tools, mcptools.WikiReadToolName)))
	}
	if chatLoopHandlerSeam != nil { // v0.10.948 — yalnız testte dolu
		for n, h := range byName {
			byName[n] = chatLoopHandlerSeam(n, h)
		}
	}
	// v0.10.1465 — başarılı kod okuması konuşmayı yapışkan yapar (trace takibi dahil).
	wrapCodeReadHandlers(byName, func() { noteCodeRead(cst, emit) })
	execHooks := agenttools.Hooks{
		Span: cspan.tool,
		Audit: func(name string, args json.RawMessage, dur time.Duration, err error, bytes int) {
			s.audit(r, "mcp.tool.call", "mcp_tool", name, chatToolAuditDetails(name, args, dur, err, bytes))
		},
	}
	if evm != nil {
		execHooks.Audit = nil // v0.10.1316 (D4) — koşu düzeyi audit ai.evalset.run kalır
	}
	// agent-core S5 — kapılı araç yalnız onaylanmış devam çağrısında yürür;
	// kapılı araç yoksa kanca nil (bugünkü yol).
	apPre := &approvalPreApproved{}
	if len(apGated) > 0 {
		execHooks.Approve = approvalHook(apGated, apPre)
	}
	// v0.10.1456 (C1) — argüman şema doğrulaması (ai_toolarg_settings.go): kip
	// off → nil, Executor bugünkü yol; warn → çağrı aynen koşar, modelin gördüğü
	// değişmez (log + metrik + span özniteliği + ai_calls notu).
	argObs, argScope := applyChatToolArgScope(&execHooks, toolSchemas)
	exec := agenttools.NewExecutor(byName, extNames, execHooks)
	if argScope != nil {
		exec.WithScope(argScope)
	}

	// v0.10.1280 (CoSRE adım 8) — döngünün TEK ai_calls satırı cevaptan SONRA
	// yazılır (RecordUsage, aşağıda); cevap olayının callId'si o satırla
	// eşleşsin diye kimlik ŞİMDİ basılır ve iki yere de aynı değer gider.
	loopCallID := copilot.NewCallID()
	// v0.10.1150 — tavanlar deepMode'dan: kapalıyken chatMaxToolCalls/Rounds.
	maxCalls, maxRounds := dm.toolCalls(), dm.toolRounds()
	v2Plan := v2LoopPlanFrom(ctx)
	if v2Plan != nil {
		// araç seti dilim 4 — v2 bütçesi prepareStep girdisindeki planla (stepInput.V2) aynı.
		maxCalls, maxRounds = v2Plan.Budget.MaxCalls, v2Plan.Budget.MaxRounds
	}
	callsLeft := maxCalls
	loopStop := defaultStop(maxRounds) // agent-core S1 (chat_stop.go)
	if v2Plan != nil && v2Plan.Budget.MaxTokens > 0 {
		loopStop = stopAny(loopStop, stopAtTokens(v2Plan.Budget.MaxTokens)) // dilim 4 — token tavanı tavan turuna
	}
	// v0.10.948 — hak ≠ iş: bilinmeyen/tekrar/kapsam reddi hak yer ama yürümez;
	// bütçe notu yürütülen ve alışveriş boyunca yürütülmeyen çağrıyı ayrı sayar.
	executedN, skippedN := 0, 0
	// v0.10.948 — bütçe sessiz değil: operatör döngünün sınırını baştan görür,
	// dolunca da ne kadarının kullanıldığını (chat_loop_steps.go).
	emit("step", chatBudgetStep(chatBudgetLabelTR(maxCalls, maxRounds), chatBudget{MaxCalls: maxCalls, MaxRounds: maxRounds})) // v0.10.1594 — yapısal bütçe
	// v0.10.1324 (operatör-bildirimi) — FİNAL METİN SÜZGECİ + YİNELEME DÜŞÜŞÜ
	// (chat_answer_degen.go). gemma4 31b bir önsöz cümlesini max_tokens dolana
	// dek yazdı; iki final çıkış da metni yalnız sanitizeAnswerLinks'ten
	// geçiriyordu. Önce yineleme + yankı süzülür; cevap yine kullanılamazsa
	// (akış muhafızı kesti ya da kanıt varken çok kısa) Derin kipte BİR araçsız
	// yeniden deneme, değilse deterministik kanıt özeti. Neden ai_calls
	// error_msg'ine yazılır, status ok kalır (degradedReason).
	var degradedReason string
	finalAnswerText := func(turn copilot.ChatTurn, prompt string, round int) string {
		ca := cleanChatAnswer(turn.Text, echoPhrases)
		hadEvidence := len(calledTools) > 0 || opPre != nil
		v := chatDegenDecide(turn.Degenerated || ca.Repeated, ca.Dropped, ca.Text, hadEvidence, dm.On, false)
		if v == degenUsable {
			return ca.Text
		}
		if v == degenRetryNoTools {
			emit("step", map[string]string{"label": chatDegenRetryLabel})
			emit("delta", chatDeltaPayload(copilot.TextDelta{Reset: true}))
			rctx, endR := cspan.turn(ctx, round, false)
			// agent-core S3 — degenRetryPlan: ek + çağrı yasağı + token tavanı (chat_prepare_step.go).
			rt, _, rerr := s.loopTurn(rctx, prompt, stepInput{Kind: stepDegenRetry, Conv: conv, Specs: specs, Role: role}, emit)
			endR(rt.InputTokens, rt.OutputTokens, rt.CachedTokens, rerr)
			totalIn += rt.InputTokens
			totalCached += rt.CachedTokens
			totalOut += rt.OutputTokens
			if rerr == nil {
				rca := cleanChatAnswer(rt.Text, echoPhrases)
				if chatDegenDecide(rt.Degenerated || rca.Repeated, rca.Dropped, rca.Text, hadEvidence, dm.On, true) == degenUsable {
					degradedReason = chatDegenReasonRetry
					return rca.Text
				}
			}
		}
		emit("step", map[string]string{"label": chatDegenFallbackLabel})
		emit("delta", chatDeltaPayload(copilot.TextDelta{Reset: true}))
		degradedReason = chatDegenReasonFallback
		var pre string
		if opPre != nil {
			pre = opPre.summary()
			calledTools = append(calledTools, opPrefetchTool) // künye: özet ön-getirmeden geliyor
		}
		return chatDegenFallbackAnswer(pre, stepDigests)
	}
	// araç seti dilim 1 (chat_stop_event.go) — her break bir çıkış nedeni yazar.
	exitReason, exitRounds := stopNone, 0
	// agent-core S5 — onay devamı: karar + (onaylıysa) tek yürütme + konuşma
	// çifti; döngü jetonun taşıdığı kalan bütçeyle ve turdan sürer.
	apStartRound := 0
	if apCont.on {
		rr := s.resumeApprovalTurn(ctx, r, emit, exec, apPre, apCont, resumeApprovalIn{
			Answer: apCont.ans, Who: approvalCaller{UserID: uid, Role: role}, Cfg: apCfg, Offered: apGated, Now: time.Now(),
		}, conv, maxCalls, maxRounds)
		conv, callsLeft, apStartRound = rr.conv, rr.callsLeft, rr.startRound
		if rr.executed {
			executedN++
		}
		if rr.skipped {
			skippedN++
		}
	}
	var apPending *pendingApproval // bu turda onay isteyen çağrı (tur sonunda jeton basılır)
	for round := 0; round < maxRounds; round++ {
		if round < apStartRound {
			continue // S5 — devam önceki turların bütçesini harcamış sayılır
		}
		exitRounds = round + 1
		tctx, endTurn := cspan.turn(ctx, round, overflowRetried) // v0.10.425 — ai.chat.turn
		// v0.10.1273 — final turun metni canlı akar (chat_step_phase.go chatLoopTurn).
		turn, _, err := s.loopTurn(tctx, loopPrompt, stepInput{
			State: loopState{Round: round, MaxRounds: maxRounds, CallsLeft: callsLeft, MaxCalls: maxCalls, Executed: executedN, Skipped: skippedN, TokensIn: totalIn, TokensOut: totalOut},
			Kind:  stepNormal, Conv: conv, Specs: specs, Role: role,
		}, emit) // agent-core S3 — normal tur: boş plan (bayt aynı)
		endTurn(turn.InputTokens, turn.OutputTokens, turn.CachedTokens, err)
		if evm != nil && evm.OnTurn != nil {
			evm.OnTurn(round, turn, err) // v0.10.1316
		}
		if turn.ToolCallsFromText {
			// v0.10.545 — sunucu tool_calls üretmedi, çağrı metinden ayrıştırıldı:
			// operatör çipte görür, /ai için log. Sürekli görünüyorsa serving
			// stack'te tool parser eksik (docs/audit/cosre-agent-session1.md §0.5).
			log.Printf("[chat] tool çağrısı METİNDEN ayrıştırıldı (n=%d) — sunucu tool_calls üretmedi", len(turn.ToolCalls))
			emit("step", map[string]string{"label": "tool çağrısı metinden ayrıştırıldı (sunucu parser yok)"})
		}
		totalIn += turn.InputTokens
		totalCached += turn.CachedTokens
		totalOut += turn.OutputTokens
		// v0.10.26 — BAĞLAM TAŞMASI. isContextOverflowErr yazılı ve
		// tablo-testliydi ama tek çağrı yeri copilot_code.go'ydu; sohbet
		// ham İngilizce sağlayıcı gövdesini ekrana basıyordu. Emsalin
		// aynısı: küçült + BİR KEZ yeniden dene (chat_overflow.go).
		if err != nil && isContextOverflowErr(err) && !overflowRetried {
			if shrunk, ok := shrinkConvForRetry(conv); ok {
				overflowRetried = true
				conv = shrunk
				emit("step", map[string]string{"label": "bağlam taştı — geçmiş küçültülüp yeniden deneniyor"})
				round-- // bu tur sayılmasın: yeniden deneme, ilerleme değil
				continue
			}
		}
		if err != nil {
			lastErr = err
			// Tavan dolduysa ham `context deadline exceeded` metni yanıltır:
			// operatör bunu "model zaman aşımına uğradı" diye okur ve modeli
			// suçlar, oysa olan şey ALIŞVERİŞİN tavana dayanmasıdır — farklı
			// bir eylem gerektiriyor (soruyu daralt).
			msg := err.Error()
			switch {
			case dctx.Err() != nil:
				msg = chatDeadlineMessageTR(exchangeMax)
			case isContextOverflowErr(err):
				msg = chatOverflowMessageTR(overflowRetried)
			}
			emit("error", map[string]string{"error": msg})
			exitReason = ctxExitReason(dctx.Err(), stopModelError)
			break
		}
		// No tool calls → this turn's text is the final answer (plus any
		// chart blocks accumulated from earlier render_chart rounds).
		if len(turn.ToolCalls) == 0 {
			// v0.10.29 — KAYNAK KÜNYESİ. Diğer üç kademede vardı, modelin
			// EN SERBEST olduğu bu yolda yoktu. En değerli hâli araç
			// listelemek değil: hiç araç çağrılmadıysa cevabın canlı
			// veriye DAYANMADIĞINI söylemek.
			// v0.10.948 — trace takibinde iki deterministik ek (chat_trace_followup.go):
			// kanıtta olmayan sayı uyarısı ve önceki açıklamayı adlandıran künye.
			// Takip değilse ikisi de bayt-bayt eski künye.
			// v0.10.1297 — model metnindeki uydurma host / araç yankısı süzgeci (chat_answer_links.go).
			// v0.10.1324 — yineleme/yankı süzgeci + düşüş, sanitizeAnswerLinks'ten ÖNCE.
			answerText := finalAnswerText(turn, loopPrompt, round)
			finalText = appendCharts(sanitizeAnswerLinks(answerText, loopLinks, s.publicBaseURL(), chatToolNameSet(toolSchemas))) + traceFollowUpNumericWarning(isTraceFollowUp, answerText, fuEvidence.String()) +
				loopSourceNoteTR(fuHadExplain, opPre.seededTools(), calledTools, executedN+skippedN)
			// v0.9.709 (operatör-bildirimi) — cevaptaki request_id'ler log
			// köprüsü çipi olur; altyapı (links + ChatBubble çipleri)
			// v0.9.419'dan beri hazırdı, yalnız guided yayınlıyordu.
			// v0.9.1228 — tool köprüleri önce (yapısal, döngüden), sonra
			// metin-madeni request-ID linkleri; href'e göre tekil.
			links := loopLinks
			for _, l := range s.answerRequestIDLinks(ctx, finalText, req.Context.Service) {
				links = mergeToolLinks(links, l)
			}
			xch.noteFinalCall(loopCallID) // v0.10.1280 — answer.callId
			emit("answer", withAnswerFollowups(withAnswerEvidence(chatAnswerEvent(finalText, exchangeID, links,
				answerOpenHref(lastUserText(req.Messages), loopOpen)), loopEvidence), s.chatLoopFollowups(ctx, fuFacts, req.Messages, pageCtx))) // v0.10.1291
			exitReason = stopAnswered
			break
		}
		// Record the assistant's tool-call turn, then execute each
		// call and feed results back as a user turn.
		conv = append(conv, copilot.ChatMessage{
			Role: "assistant", Text: turn.Text, ToolCalls: turn.ToolCalls,
			RawContent: turn.RawContent, // Anthropic thinking blokları imzalı geri gider
		})
		results := make([]copilot.ToolResult, 0, len(turn.ToolCalls))
		run, over := splitByCallBudget(turn.ToolCalls, callsLeft)
		callsLeft -= len(run)
		cancelled := false // v0.10.948 — tur ortasında ctx bitti mi
		for _, tc := range run {
			if apPending != nil {
				// agent-core S5 — onay bekleyen çağrıdan sonrakiler yürütülmez
				// (call_cap gibi); hakları jetonun bütçesine geri döner.
				n := emitStepChip(emit, tc.Name, string(tc.Input))
				emit("step-result", skippedStepResult(n, tc.Name, chatCallCapContent, skipReasonCallCap))
				results = append(results, copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: true, Content: chatCallCapContent})
				skippedN++
				apPending.refund++
				continue
			}
			// v0.9.1181 (Faz 4.3) — çipin kimliği. `step` tool ÇALIŞMADAN
			// önce çıkar (ilerleme geri bildirimi), sonuç ise çalıştıktan
			// sonra ayrı bir olayla; ikisini bu sayı eşler. Tur içindeki
			// indeks YETMEZ, çünkü çipler turlar boyunca birikiyor —
			// sayaç istek boyunca tekil.
			// v0.9.1229 — kimliği sarmalayıcı veriyor (emitStepChip);
			// döngünün kendi sayacı guided'ın çipleriyle çakışabiliyordu.
			// v0.10.53 — künye DENENEN aracı değil, VERİ DÖNDÜREN aracı sayar.
			//
			// v0.10.29'da bu satır tam burada duruyordu, yani `byName`
			// kontrolünden ÖNCE: modelin UYDURDUĞU, var olmayan bir araç adı
			// künyeye "Kaynak:" diye giriyordu. Hata dönen araç da öyle.
			//
			// İkisi de künyenin iddiasını çürütüyor: künye cevabın CANLI
			// VERİYE dayandığını söylüyor. Çalışmamış bir araç veri
			// döndürmez; hata metni telemetri değildir. Uydurma bir adı
			// kaynak diye göstermek, uydurmayı KANITLA süslemek olurdu —
			// künyenin var olma sebebinin tam tersi.
			//
			// Kayıt artık başarılı çalıştırmadan SONRA (aşağıda). Çip yine
			// burada çıkıyor: operatör modelin NE DENEDİĞİNİ görmeli,
			// başarısız denemeler dâhil — o ayrı bir soru.
			// v0.10.1274 — ekran kapsamı argümana (deterministik); çip GERÇEKTEN
			// çalışan argümanı gösterir, tekrar muhafızı da onu anahtarlar.
			cargs, scopeApplied := applyScreenContext(tc.Name, tc.Input, toolSchemas[tc.Name], toolScope, time.Now())
			if v2p := v2LoopPlanFrom(ctx); v2p != nil {
				cargs, _ = applyUserScope(tc.Name, cargs, toolSchemas[tc.Name], v2p.Scope) // araç seti dilim 2
			}
			stepN = emitStepChipInput(emit, tc.Name, string(cargs), "", cargs) // v0.10.1594 — argsJson (v2)
			// v0.10.948 — İPTAL her çağrıdan ÖNCE denetlenir: Executor.Call ilk iş
			// ctx'e bakar (Kind cancelled, handler çağrılmaz); turun kalanları da
			// aynı yoldan yürütülmeden geçer ve döngü aşağıda biter.
			// v0.10.1275 — bileşik araç ilerlemesi: etiket-yalnız `step {label, phase}`
			// (ekran-bağlamı çipiyle aynı şekil). Kanca handler'ın KOORDİNATÖR
			// goroutine'inden çağrılır (executor senkron) — emit tek goroutine'de kalır.
			// Gölge ctx bu turun gövdesiyle sınırlı; kanca yalnız bileşik araçta.
			ctx := ctx
			if mcptools.IsCompositeEvidenceTool(tc.Name) {
				ctx = mcptools.WithProgress(ctx, func(phase, label string) {
					emit("step", chatProgressStep(phase, label))
				})
			}
			oc := exec.Call(ctx, tc.Name, cargs)
			if evm != nil && evm.OnTool != nil {
				evm.OnTool(tc.Name, cargs, oc) // v0.10.1316
			}
			if oc.Kind == agenttools.KindApproval {
				// agent-core S5 — operatör onayı: jeton tur sonunda (emitApprovalAsk).
				apPending = &pendingApproval{stepN: stepN, tool: tc.Name, args: oc.Args, resultIdx: len(results)}
				results = append(results, copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: true, Content: oc.Content})
				skippedN++
				continue
			}
			if !oc.Executed {
				// bilinmeyen ad / tekrar muhafızı / kapsam reddi / iptal:
				// yürütülmedi, süre yazılmaz (v0.10.161 — Σ süre hesaplanabilir
				// kalsın). v0.10.948 — `skipped:true` + neden; durationMs YOK
				// (0 ms bir ölçüm gibi okunuyordu; chat_loop_steps.go).
				if oc.Kind == agenttools.KindCancelled {
					cancelled = true
				}
				emit("step-result", skippedStepResult(stepN, tc.Name, oc.Content, oc.Kind))
				results = append(results, copilot.ToolResult{
					CallID: tc.ID, Name: tc.Name, IsError: true, Content: oc.Content,
				})
				skippedN++
				continue
			}
			toolDur := oc.Duration
			executedN++
			if isTraceFollowUp {
				// v0.10.948 — sayı denetiminin kanıtı: YALNIZ yürüyen çağrı, argümanı
				// ve TAM çıktısı (modele giden kırpık kopya değil).
				fuEvidence.Write(cargs)
				fuEvidence.WriteByte('\n')
				// v0.10.1050 — read_source_code'dan YALNIZ referans satırı (kod sayıyı temellendirmez).
				fuEvidence.WriteString(chatFollowUpEvidence(tc.Name, oc.Content))
				fuEvidence.WriteByte('\n')
			}
			// CoSRE Faz-2 — render_chart: handler'ın DOĞRULANMIŞ çıktısı
			// (tc.Input değil) ```chart``` fence'ine dönüşür.
			if tc.Name == "render_chart" && !oc.IsError {
				if block, key := chatChartBlock(oc.Content); block != "" && !chartSeen[key] {
					chartSeen[key] = true
					chartBlocks = append(chartBlocks, block)
					// v0.10.541 — aynı grafik tipli blok olarak da: pencere MUTLAK
					// (fromNs/toNs = tool'un sorguladığı an), now-çapası yok; fence
					// eski istemci/arşiv için aynen kalır.
					if spec, ok := chatChartSpec(oc.Content, time.Now()); ok {
						emit("block", blockSeq.Next(blocks.TypeChart, spec))
					}
				}
			}
			tr := copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: oc.IsError, Content: oc.Content}
			if !tr.IsError && scopeApplied.EnvNotApplied != "" {
				// v0.10.1274 — env-kör araç: model sonucu ekran env'ine daraltılmış sanmasın.
				tr.Content = annotateEnvBlind(tr.Content, scopeApplied.EnvNotApplied, scopeApplied.ScreenEnv)
			}
			// v0.10.944 — kaynak durumu çıktının TAMAMINDAN (önizleme 4 KB'ta
			// kırpılır; rozet oradan okunamayabilir). chat_step_sources.go.
			// Bir kez çözülür: künye ve çip aynı sonucu kullanır.
			srcs := stepSourceStatuses(tr.Content)
			// v0.10.1169 — problem sayfasında başarılı tool sonucu → "probleme not" aksiyonu
			// (yalnız sunucu tarafı türetim; chat_actions.go problemNoteAction).
			if !tr.IsError && !noteOffered && pageCtx != nil && !stepSourcesAllFailed(srcs) {
				if noteAct, ok := s.problemNoteAction(ctx, pageCtx.ProblemID, tc.Name, tr.Content); ok {
					noteOffered = true
					emit("block", blockSeq.Next(blocks.TypeAction, noteAct))
				}
			}
			// v0.10.1288 — get_problem sonucundaki ajan hipotezi → tık-only "doğrula"
			// aksiyonu (yalnız sunucu tarafı türetim; chat_actions.go sreHypothesisAction).
			if !tr.IsError && !hypOffered && pageCtx != nil {
				if hypAct, ok := s.sreHypothesisAction(ctx, pageCtx.ProblemID, tc.Name, tr.Content); ok {
					hypOffered = true
					emit("block", blockSeq.Next(blocks.TypeAction, hypAct))
				}
			}
			if !tr.IsError && !stepSourcesAllFailed(srcs) {
				// v0.10.53 — künyeye YALNIZ buradan: araç vardı, çalıştı, veri döndürdü.
				// v0.10.944 — kaynak hatası artık Go hatası değil, source.state'li
				// başarılı sonuç; tüm kaynakları hata sınıfındaysa veri yok, künyeye girmez.
				calledTools = append(calledTools, tc.Name)
				fuFacts = append(fuFacts, followupFactsFromTool(tc.Name, cargs, tr.Content)...) // v0.10.1291
			}
			// v0.10.1050 — read_source_code'un başarılı sonucu tele YALNIZ referans
			// olarak çıkar (chat_source_code.go chatStepPreview; kod modele gider).
			preview, truncated := chatStepPreview(tc.Name, tr.Content, tr.IsError)
			if !tr.IsError {
				stepDigests = append(stepDigests, chatStepDigest{Tool: tc.Name, Preview: preview}) // v0.10.1324
			}
			if sum := chatCodeReadSummary(tc.Name, tr.Content, tr.IsError); sum != "" {
				codeReads = append(codeReads, sum)
			}
			stepEv := map[string]any{
				"i": stepN, "tool": tc.Name, "ok": !tr.IsError,
				"preview": preview, "truncated": truncated, "bytes": len(tr.Content),
				"durationMs": toolDur.Milliseconds(),
			}
			if !scopeApplied.Empty() {
				stepEv["ctxApplied"] = scopeApplied // v0.10.1274 — opsiyonel; eski FE yok sayar
			}
			// v0.10.944 — denetlenmiş çıktı (nil değil) boş olsa da gönderilir:
			// `sources: []` "okundu, durum yok" demek; eksik alan FE'de "eski
			// sunucu" sayılır. Hata sonucunda gönderilmez (hata rozeti kazanır).
			if srcs != nil && !tr.IsError {
				stepEv["sources"] = srcs
			}
			// v0.9.1228 — çipe ürün köprüsü: başarılı çağrının hedef
			// görünümü (K4-denetimli harita, chat_tool_links.go). Eski FE
			// alanı yok sayar (links/suggestions ileri-uyum sınıfı).
			if !tr.IsError {
				// v0.9.1321 (§3.1 K6) — köprü çipi tool'un GERÇEKTEN
				// sorguladığı pencereyi taşır ([now-range_s, now]);
				// arg'da range_s yoksa pencere yazılmaz.
				// v0.10.495 — sonuçtaki birebir arama linki (deep_link) varsa
				// arg-türevi köprünün yerine geçer; sohbet cevabı `open` ile
				// arkadaki sayfayı da oraya götürebilir.
				if l, ok := toolResultDeepLink(tc.Name, tr.Content); ok {
					l.Href = opPre.rewriteSearchHref(l.Href) // v0.10.1297 — operasyon adı → kod süzgeci
					stepEv["href"] = l.Href
					loopLinks = mergeToolLinks(loopLinks, l)
					loopOpen = nextLoopOpen(loopOpen, tc.Name, l.Href)
					emit("block", blockSeq.Next(blocks.TypeLink, l)) // v0.10.541
					if act, ok := actionForLink(l, pagePath); ok {
						emit("block", blockSeq.Next(blocks.TypeAction, act)) // v0.10.542
					}
				} else if l, ok := buildLinkResultLink(tc.Name, tr.Content); ok {
					// build_link'in href'i: model metninde tıklanamaz, çip olur.
					// open YAZILMAZ — build_link sayfayı kendiliğinden değiştirmez.
					l.Href = opPre.rewriteSearchHref(l.Href) // v0.10.1297 — operasyon adı → kod süzgeci
					stepEv["href"] = l.Href
					loopLinks = mergeToolLinks(loopLinks, l)
					emit("block", blockSeq.Next(blocks.TypeLink, l))
				} else if l, ok := toolCallLink(tc.Name, tc.Input, time.Now()); ok {
					stepEv["href"] = l.Href
					loopLinks = mergeToolLinks(loopLinks, l)
					emit("block", blockSeq.Next(blocks.TypeLink, l)) // v0.10.541
					if act, ok := actionForLink(l, pagePath); ok {
						emit("block", blockSeq.Next(blocks.TypeAction, act)) // v0.10.542
					}
				}
			}
			// v0.10.1122 — wiki sonuçlarının sayfa url'leri cevap çipi olur
			// (tıklanır kaynak; yalnız http(s), en iyi iki sayfa).
			if !tr.IsError {
				for _, l := range wikiToolLinks(tc.Name, tr.Content) {
					if _, set := stepEv["href"]; !set {
						stepEv["href"] = l.Href
					}
					loopLinks = mergeToolLinks(loopLinks, l)
					emit("block", blockSeq.Next(blocks.TypeLink, l))
				}
			}
			// v0.10.1272 — kanıt çipi: sonuçtaki evidence + "Explore'da aç" href'i.
			// Veri sohbet içinde de: aynı sonuçtan chart/trace_list/table bloğu (ek sorgu yok).
			// v0.10.1275 — çok-kanıtlı zarf: bileşik araç (pivot_from_trace) hop başına
			// çip + blok taşır; atomik araçta liste tek kalem (eski gövde aynen).
			if evs := attachStepEvidenceList(stepEv, tc.Name, tr.Content); len(evs) > 0 {
				evs = withEvidenceListScope(stepEv, evs, scopeApplied) // v0.10.1274
				for _, ev := range evs {
					loopEvidence = mergeChatEvidence(loopEvidence, ev)
				}
				for _, b := range mcptools.ToolResultBlocks(tc.Name, tr.Content, evs) {
					emit("block", blockSeq.Next(b.Type, b.Payload))
				}
			}
			if !tr.IsError {
				markV2Skipped(stepEv, tc.Name, tr.Content) // dilim 3b — okunmadı → kind skipped
			}
			emit("step-result", stepEv)
			// v0.9.1230 — MODEL tarafındaki bütçe, çipten SONRA uygulanır:
			// önizleme ve `bytes` kırpılmamış GERÇEK boyu göstermeye devam
			// etsin (operatörün "ne kadarını görmüyorum" sorusu orada
			// cevaplanıyor), modele giden metin ise tavana insin — kırpma
			// içeriğin içinde SÖYLENEREK (chat_tool_budget.go).
			//
			// Konuşmanın tur başında yeniden bütçelenmesi (ClampHistory'nin
			// döngü içinde tekrarı) BİLEREK yapılmıyor: asistanın tool-call
			// turu ile onun ToolResults turu ikizdir, birini düşürmek
			// sağlayıcıda sahipsiz tool sonucu bırakır. Kaynağı burada
			// kesmek doğru yer.
			tr.Content, _ = clampToolResultForLoop(v2Plan, tr.Content) // dilim 4 — v2 bayt tavanı
			results = append(results, tr)
		}
		if len(over) > 0 {
			// N6 — tavanı aşan çağrılar ÇALIŞTIRILMAZ; her birine hata sonucu
			// döner (sağlayıcı sahipsiz tool_call kabul etmez) ve döngü tavan
			// turuna geçer.
			// v0.10.948 — her biri çip + skipped (call_cap): modelin ne denediği
			// görünür; sayı aşağıdaki bütçe notunda.
			for _, tc := range over {
				n := emitStepChip(emit, tc.Name, string(tc.Input))
				emit("step-result", skippedStepResult(n, tc.Name, chatCallCapContent, skipReasonCallCap))
				results = append(results, copilot.ToolResult{CallID: tc.ID, Name: tc.Name, IsError: true, Content: chatCallCapContent})
			}
			skippedN += len(over)
		}
		// agent-core S5 — onay isteği tur sonunda: jeton kalan bütçeyi (onaydan
		// sonra yürütülmeyenlerin hakkı dahil) taşır. Basılamazsa çağrı hata
		// olarak raporlanır ve döngü bugünkü gibi sürer.
		apAsked := false
		if apPending != nil {
			if !cancelled && ctx.Err() == nil {
				content, ok := s.emitApprovalAsk(r, emit, apPending, exchangeID, uid, apCfg, callsLeft, round+1)
				results[apPending.resultIdx].Content = content
				apAsked = ok
			}
			apPending = nil
		}
		// agent-core S1 — durma kararı adlandırılmış koşullardan (chat_stop.go);
		// tek değerlendirme, iki dal: iptal → model çağrılmaz, diğer neden → tavan turu.
		reason := loopStop(loopState{
			Round: round, MaxRounds: maxRounds, CallsLeft: callsLeft, MaxCalls: maxCalls,
			Executed: executedN, Skipped: skippedN, TokensIn: totalIn, TokensOut: totalOut,
			LastCalls: toolCallNames(turn.ToolCalls), Cancelled: cancelled, CtxErr: ctx.Err(),
			ApprovalPending: apAsked,
		})
		if reason == stopCancelled {
			// v0.10.948 — İPTAL: kalan çağrılar yürütülmedi (skipped), model
			// yeniden ÇAĞRILMAZ (tavan turu dâhil) — cevabı bekleyen yok ya da
			// alışveriş tavanı doldu; ikinci bir LLM çağrısı boşa bütçe yakar.
			// ctx turun SON çağrısında bittiyse de (skipped yok) aynı son.
			lastErr = ctx.Err()
			emit("error", map[string]string{"error": chatCancelledMessageTR(lastErr, exchangeMax)})
			exitReason = ctxExitReason(lastErr, stopCancelled)
			break
		}
		if reason == stopApprovalPending {
			// agent-core S4 — onay bekleniyor: model yeniden çağrılmaz (bugün ulaşılmaz).
			exitReason = reason
			break
		}
		conv = append(conv, copilot.ChatMessage{Role: "user", ToolResults: results})

		// Hit the round cap with tool calls still pending → ask the
		// model for a best-effort answer with what it has, no more
		// tools, so the operator isn't left hanging.
		//
		// v0.9.1232 — tavan yönergesi burada satır-içi İngilizce bir
		// literaldi; prompt METNİ olduğu için internal/copilot/prompts.go'ya
		// taşındı (SystemPromptChatRoundCap = taban + ek). Sicil
		// accessor'lardan türediğinden bu ek artık dil kapısının kapsamında.
		if isCapStop(reason) { // = round == maxRounds-1 || callsLeft <= 0 (defaultStop)
			// v0.10.948 — bütçe dolduğu AÇIKÇA söylenir: harcanan hak/tur, yürütülen
			// ve alışveriş boyunca yürütülmeyen çağrı (eski "tool-çağrı tavanı" etiketinin yerine).
			emit("step", chatBudgetStep(chatBudgetExhaustedCapsLabelTR(maxCalls-callsLeft, maxCalls, executedN, skippedN, round+1, maxRounds),
				chatBudget{MaxCalls: maxCalls, MaxRounds: maxRounds, SlotsUsed: maxCalls - callsLeft, Executed: executedN, Skipped: skippedN, RoundsUsed: round + 1, Exhausted: true, StopReason: string(reason)})) // v0.10.1594; S2 neden
			// v0.10.806 — tavan eki döngü prompt'unun SONUNA: önek aynı kalır
			// (önbellek isabeti) ve tavan turu bağlam önsözlerini de görür
			// (eskiden yalnız hitap + sohbet çekirdeği + ek gidiyordu).
			// Tool TANIMLARI gider ama çağrı yasak (WithNoToolCalls): geçmiş
			// tool_use/tool_result taşıyor — Anthropic tanımsız tool'la 400
			// verir, barındırılan OpenAI boş tools dizisini reddeder; tanımlar
			// kalınca önek önbelleği de korunur. Yerel uçlarda gövde bugünkü
			// şekilde (boş dizi) kalır — provider.ChatRequest.NoToolCalls.
			// agent-core S3 — capTurnPlan: loopPrompt + ChatRoundCapAddendum + WithNoToolCalls
			// (chat_prepare_step.go); capPrompt turun gerçek prompt'u (süzgeç/düşüş yeniden kullanır).
			tctx2, endTurn2 := cspan.turn(ctx, round, false) // v0.10.425 — tur tavanı da bir tur
			turn2, capPrompt, err2 := s.loopTurn(tctx2, loopPrompt, stepInput{
				State: loopState{Round: round, MaxRounds: maxRounds, CallsLeft: callsLeft, MaxCalls: maxCalls, Executed: executedN, Skipped: skippedN, TokensIn: totalIn, TokensOut: totalOut},
				Kind:  stepCap, Conv: conv, Specs: specs, Role: role,
			}, emit)
			endTurn2(turn2.InputTokens, turn2.OutputTokens, turn2.CachedTokens, err2)
			if evm != nil && evm.OnTurn != nil {
				evm.OnTurn(-1, turn2, err2) // v0.10.1316 — tavan turu
			}
			totalIn += turn2.InputTokens
			totalCached += turn2.CachedTokens
			totalOut += turn2.OutputTokens
			if err2 != nil {
				lastErr = err2
				// v0.10.61 — HATA SINIFLANDIRMASI BURADA DA.
				//
				// Bu dal ham `err2.Error()` basıyordu, yani v0.10.24 ve
				// v0.10.26'nın düzelttiği iki kusur tur-tavanı yolunda AYNEN
				// duruyordu: operatör ham İngilizce sağlayıcı gövdesini
				// görüyor ve `context deadline exceeded` metnini "model zaman
				// aşımına uğradı" diye okuyup MODELİ suçluyordu — oysa olan
				// şey alışverişin tavana dayanması ve farklı bir eylem
				// gerektiriyor.
				//
				// Sınıflandırma normal yolun AYNISI; ikisinin ayrışmaması
				// için kalıp birebir kopyalanmadı, aynı yardımcılar çağrıldı.
				msg := err2.Error()
				switch {
				case dctx.Err() != nil:
					msg = chatDeadlineMessageTR(exchangeMax)
				case isContextOverflowErr(err2):
					msg = chatOverflowMessageTR(overflowRetried)
				}
				emit("error", map[string]string{"error": msg})
			} else {
				// v0.10.61 — KAYNAK KÜNYESİ BURADA DA.
				//
				// Künye (v0.10.29) normal yolda vardı, tur-tavanı yolunda
				// YOKTU. Oysa bu yol tanımı gereği EN ÇOK araç çağıran yol:
				// tavana ancak tur tur araç çağırarak varılıyor. Yani atıfın
				// en anlamlı olduğu cevap, tek atıfsız cevaptı.
				// v0.10.948 — iki çıkış tek sözleşme: sayı denetimi ve takip künyesi burada da.
				// v0.10.1324 — iki çıkış tek sözleşme: süzgeç + düşüş burada da (tavan prompt'uyla).
				answerText := finalAnswerText(turn2, capPrompt, round)
				finalText = appendCharts(sanitizeAnswerLinks(answerText, loopLinks, s.publicBaseURL(), chatToolNameSet(toolSchemas))) + traceFollowUpNumericWarning(isTraceFollowUp, answerText, fuEvidence.String()) +
					loopSourceNoteTR(fuHadExplain, opPre.seededTools(), calledTools, executedN+skippedN)
				// v0.9.709 (operatör-bildirimi) — cevaptaki request_id'ler log
				// köprüsü çipi olur. v0.9.1228 — tool köprüleri de burada:
				// tur-tavanı cevabı da döngünün kanıt linklerini taşır.
				links := loopLinks
				for _, l := range s.answerRequestIDLinks(ctx, finalText, req.Context.Service) {
					links = mergeToolLinks(links, l)
				}
				xch.noteFinalCall(loopCallID) // v0.10.1280 — answer.callId
				emit("answer", withAnswerFollowups(withAnswerEvidence(chatAnswerEvent(finalText, exchangeID, links,
					answerOpenHref(lastUserText(req.Messages), loopOpen)), loopEvidence), s.chatLoopFollowups(ctx, fuFacts, req.Messages, pageCtx))) // v0.10.1291
			}
			exitReason = reason // tavan; tavan turu alışveriş tavanına dayandıysa deadline
			if err2 != nil {
				exitReason = ctxExitReason(dctx.Err(), reason)
			}
			break // tavan turu son turdur (tur ya da çağrı tavanı)
		}
	}

	// One ai_calls row per exchange. Prompt sample = the operator's
	// last user message; response sample = the final answer.
	status, errMsg := "ok", degradedReason // v0.10.1324 — bozulmuş cevap: status ok, neden error_msg'de
	if lastErr != nil {
		status, errMsg = "error", lastErr.Error()
	}
	// v0.10.1456 (C1) — doğrulama notu; sorun yoksa errMsg aynen.
	errMsg = withToolArgNote(status, errMsg, argObs.note())
	cspan.finish(totalIn, totalOut, totalCached, lastErr) // v0.10.425 — span ve satır aynı toplamlar
	// v0.10.1050 — read_source_code okunduysa örneğe maskeli "[kod: …]" özetleri
	// eklenir (explain yolunun sözleşmesi); okunmadıysa bayt bayt eski örnek.
	s.copilot.RecordUsage(copilot.WithCallID(ctx, loopCallID), chatT0, totalIn, totalOut, totalCached, status, errMsg, chatPromptSample(lastUserText(req.Messages), codeReads), finalText)

	if exitReason == stopNone {
		exitReason = stopRoundCap // savunma: döngü break'siz yalnız turlar bitince biter
	}
	emit("stop", chatStopEvent(exitReason, maxCalls-callsLeft, exitRounds, time.Since(chatT0))) // araç seti dilim 1 — v1 yazıcısı düşürür
	emit("done", map[string]bool{"ok": lastErr == nil})
}

// chatChartBlock (CoSRE Faz-2) parses the render_chart handler's output
// — the server-VALIDATED spec, not the model's raw tool input — and
// returns the deterministic ```chart``` fence (chartFence,
// copilot_guided.go) plus a service+operation+agg dedup key. Empty
// block = not renderable (ok:false, malformed, or incomplete spec).
// Pure — table-tested in copilot_chat_test.go.
// chatChartSpec — v0.10.541: render_chart çıktısından MUTLAK pencereli blok
// gövdesi (fence'in aksine fromNs/toNs damgalı; CosreChart mutlak pencereyi
// önceler). Saf; tablo-testli.
func chatChartSpec(out string, now time.Time) (guidedChartSpec, bool) {
	block, _ := chatChartBlock(out)
	if block == "" {
		return guidedChartSpec{}, false
	}
	var spec guidedChartSpec
	j := strings.TrimSuffix(strings.TrimPrefix(block, "\n```chart\n"), "\n```\n")
	if err := json.Unmarshal([]byte(j), &spec); err != nil {
		return guidedChartSpec{}, false
	}
	rs := spec.RangeS
	if rs <= 0 {
		rs = 1800
	}
	spec.ToNs = now.UnixNano()
	spec.FromNs = now.Add(-time.Duration(rs) * time.Second).UnixNano()
	return spec, true
}

// compareTitleTR — v0.10.547: karşılaştırma başlık eki.
func compareTitleTR(shiftS int64) string {
	switch shiftS {
	case 86400:
		return "dün aynı saat"
	case 604800:
		return "geçen hafta aynı saat"
	}
	return fmt.Sprintf("%d saat önce", shiftS/3600)
}

func chatChartBlock(out string) (block, key string) {
	var rc struct {
		OK   bool `json:"ok"`
		Spec struct {
			Service   string              `json:"service"`
			Operation string              `json:"operation"`
			Agg       string              `json:"agg"`
			RangeS    int64               `json:"rangeS"`
			GroupBy   string              `json:"groupBy"`
			Compare   *guidedChartCompare `json:"compare"` // v0.10.547
			Source    string              `json:"source"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &rc); err != nil || !rc.OK || rc.Spec.Service == "" || rc.Spec.Agg == "" {
		return "", ""
	}
	titleBase := rc.Spec.Service
	if rc.Spec.Operation != "" {
		titleBase = rc.Spec.Operation
	}
	title := titleBase + " · " + rc.Spec.Agg
	if rc.Spec.GroupBy != "" {
		title += " · " + rc.Spec.GroupBy
	}
	if rc.Spec.Compare != nil && rc.Spec.Compare.ShiftS > 0 {
		title += " · " + compareTitleTR(rc.Spec.Compare.ShiftS)
	}
	fence := chartFence(guidedChartSpec{
		Title:     title,
		Service:   rc.Spec.Service,
		Operation: rc.Spec.Operation,
		Agg:       rc.Spec.Agg,
		RangeS:    rc.Spec.RangeS,
		GroupBy:   rc.Spec.GroupBy,
		Compare:   rc.Spec.Compare, // v0.10.547
		Source:    rc.Spec.Source,
	})
	// v0.9.1186 — kırılım DEDUP anahtarına girdi. Girmeseydi aynı servis+
	// agg'ın kırılımlı ve kırılımsız hâli "aynı kart" sayılır, ikincisi
	// sessizce düşerdi — oysa model ikisini bilerek isteyebilir ("toplamı
	// göster, sonra endpoint kırılımını").
	return fence, rc.Spec.Service + "\x00" + rc.Spec.Operation + "\x00" + rc.Spec.Agg + "\x00" + rc.Spec.GroupBy
}

// lastUserText pulls the most recent user-typed message for the
// ai_calls prompt sample (skips tool-result turns).
func lastUserText(msgs []copilot.ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Text) != "" {
			return msgs[i].Text
		}
	}
	return ""
}
