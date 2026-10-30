package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	// aiprov — tek transport (bkz. provider_calls.go'daki görev ayrımı).
	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// In-app chatbot tool-calling layer (v0.6.53). The single-shot
// Explain path can't drive the agentic chat: the chat needs
// multi-turn history AND function-calling so the LLM can pull
// telemetry on demand (the 7 MCP tools become its functions).
//
// ChatWithTools is provider-neutral at the boundary — the API
// handler builds a []ChatMessage + []ToolSpec and gets back a
// ChatTurn (either prose, or a batch of tool calls to execute and
// feed back). On-prem installs run any of the three providers, so all
// three carry the tool-calling path (operator decision 2026-05-28).
//
// FAZ 1.3 (v0.9.1125) — sağlayıcıya özgü TEL KODLAMASI bu dosyadan
// gitti: gövde/uç/header/çözümleme artık internal/ai/provider/tools.go.
// Burada kalan: sağlayıcı dallanması, GitHub oturum jetonu takası
// (DURUM), token kelepçesi ve ai_calls kaydı.

// Sağlayıcı-nötr sohbet sözlüğü PROVIDER'da tanımlı; burada TAKMA AD
// olarak yaşıyor.
//
// Neden takma ad, yeniden tanım değil: bu tipler internal/api ve
// internal/mcptools'ta 20+ yerde `copilot.ChatMessage`,
// `copilot.ToolSpec`, `copilot.ToolResult` diye geçiyor VE
// /api/copilot/chat gövdesinin JSON şeklini taşıyorlar. Takma ad Go'da
// AYNI tiptir: tek satır çağrı yeri değişmedi, tel şekli de değişmedi.
// Yeniden tanım (iki ayrı struct + dönüştürücü) hem o dosyaları
// dolaşırdı hem de tam bu fazın kapatmaya çalıştığı "iki yazılış"
// sınıfını geri açardı.
type (
	ToolSpec    = aiprov.ToolSpec
	ToolCall    = aiprov.ToolCall
	ToolResult  = aiprov.ToolResult
	ChatMessage = aiprov.ChatMessage
)

// ChatTurn is one model response. When ToolCalls is non-empty the
// caller must execute them and loop; otherwise Text is the final
// answer.
//
// Bilinçli olarak provider.ChatResponse'un takma adı DEĞİL: token
// alanları burada uint32 (ai_calls sütun tipi), transport'ta int
// (sunucu ne yollarsa). Dönüşüm clampTokens'tan geçer.
type ChatTurn struct {
	Text         string
	ToolCalls    []ToolCall
	InputTokens  uint32
	OutputTokens uint32
	CachedTokens uint32 // v0.10.807 — önek önbelleği; tur başına, döngü toplar
	// ToolCallsFromText — v0.10.545: bkz. provider.ChatResponse.ToolCallsFromText.
	ToolCallsFromText bool
	// Degenerated — v0.10.1324: bkz. provider.ChatResponse.Degenerated. Döngü
	// bunu kullanılamaz cevap sayar (api/chat_answer_degen.go).
	Degenerated bool
	// RawContent — Anthropic asistan turunun ham content'i; döngü onu
	// ChatMessage.RawContent olarak geri besler (thinking blokları imzalı kalır).
	RawContent json.RawMessage
}

type noToolCallsKey struct{}

// WithNoToolCalls — tur tavanı: bu çağrıda tool tanımları gövdede kalır ama
// model çağrı yapamaz (provider.ChatRequest.NoToolCalls).
func WithNoToolCalls(ctx context.Context) context.Context {
	return context.WithValue(ctx, noToolCallsKey{}, true)
}

func noToolCallsFrom(ctx context.Context) bool {
	v, _ := ctx.Value(noToolCallsKey{}).(bool)
	return v
}

// NoToolCallsSet — agent-core S3: ctx WithNoToolCalls taşıyor mu (döngü
// adım planının kimlik testleri; sağlayıcı yolu noToolCallsFrom'u okur).
func NoToolCallsSet(ctx context.Context) bool { return noToolCallsFrom(ctx) }

// MaxTokensCapOf — agent-core S3: ctx'teki WithMaxTokensCap değeri (yoksa 0).
func MaxTokensCapOf(ctx context.Context) int {
	c, _ := ctx.Value(maxTokensCapKey{}).(int)
	return c
}

type maxTokensCapKey struct{}

// WithMaxTokensCap — v0.10.1324: bu çağrının completion bütçesine TAVAN
// (yineleme sonrası araçsız yeniden deneme ~700). n ≤ 0 → ctx aynen.
func WithMaxTokensCap(ctx context.Context, n int) context.Context {
	if n <= 0 {
		return ctx
	}
	return context.WithValue(ctx, maxTokensCapKey{}, n)
}

// cappedMaxTokens — SAF: tavan varsa bütçe min(bütçe, tavan); bütçe 0
// (sağlayıcı varsayılanı) ise tavanın kendisi.
func cappedMaxTokens(ctx context.Context, maxTok int) int {
	c, _ := ctx.Value(maxTokensCapKey{}).(int)
	if c <= 0 {
		return maxTok
	}
	if maxTok <= 0 || maxTok > c {
		return c
	}
	return maxTok
}

// ChatWithTools runs ONE model turn over the conversation with the
// given tools available. Branches on the configured provider. No
// ai_calls recording here — the handler records once per user
// message after the agentic loop settles (RecordUsage), summing
// the per-turn token usage, so one chat exchange = one ai_calls row.
func (s *Service) ChatWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec) (ChatTurn, error) {
	if !s.activeFor(ctx, true) { // v0.10.175 — çözülen profilin kimliği (#1)
		return ChatTurn{}, errors.New("AI copilot not available (disabled or not configured — open Settings → AI Copilot)")
	}
	if err := s.localOnlyGate(ctx); err != nil { // v0.10.1451 — karar 8: sağlayıcıdan ÖNCE (handler RecordUsage error_class=local_only)
		return ChatTurn{}, err
	}
	provider, _, _ := s.profileIdentity(ctx) // v0.10.175
	switch provider {
	case ProviderGitHub:
		return s.chatGitHubWithTools(ctx, system, msgs, tools)
	case ProviderOpenAI:
		return s.chatOpenAIWithTools(ctx, system, msgs, tools)
	default:
		return s.chatAnthropicWithTools(ctx, system, msgs, tools)
	}
}

// RecordUsage writes a single ai_calls row for a completed chat
// exchange. Mirrors the recording block in Explain so the /ai page
// attributes chat usage alongside the ✨ Explain surfaces. Surface
// comes from MetaFromContext (the handler sets it to "chat").
// RecordUsage — v0.10.397 (CoSRE denetimi O1): `started` çağrının
// başlangıcı; sıfır değer DurationMs'i 0 bırakır (eski davranış). Sohbet
// döngüsü en pahalı yüzeyken /ai gecikme KPI'ları onu 0 sayıyordu.
func (s *Service) RecordUsage(ctx context.Context, started time.Time, inTok, outTok, cachedTok uint32, status, errMsg, promptSample, respSample string) {
	provider, model, baseURL := s.profileIdentity(ctx) // v0.10.175 — kullanılan profil
	meta := MetaFromContext(ctx)
	// v0.10.1280 — kimlik ctx'te sabitlendiyse (WithCallID: döngü cevabı bu
	// satırdan önce yayınladı ve kimliği taşıdı) o, yoksa taze.
	callID := callIDFor(ctx)
	notifyCallSink(ctx, CallNote{CallID: callID, ExchangeID: meta.ExchangeID, Model: model,
		InputTokens: inTok, OutputTokens: outTok}) // Faz 1 — cosre_interactions
	if s.recorder == nil {
		return
	}
	rec := CallRecord{
		ID:             callID,
		CreatedAt:      time.Now(),
		Surface:        recordSurface(ctx, meta.Surface), // v0.10.1316 — ajan kipi evalset öneki
		ExchangeID:     meta.ExchangeID,
		Provider:       provider,
		Model:          model,
		BaseURL:        baseURL,
		InputTokens:    inTok,
		OutputTokens:   outTok,
		CachedTokens:   cachedTok, // v0.10.807
		DurationMs:     durationMsSince(started),
		Status:         status,
		PromptChars:    uint32(len(promptSample)),
		ResponseChars:  uint32(len(respSample)),
		UserID:         meta.UserID,
		UserEmail:      meta.UserEmail,
		PromptSample:   truncForSample(promptSample),
		ResponseSample: truncForSample(respSample),
		TraceID:        callTraceID(ctx), // v0.10.1463 (D1)
	}
	if status == "error" {
		rec.ErrorMsg = truncErr(errMsg)
		rec.ErrorClass = ClassifyAIErrorText(errMsg) // v0.10.409
	} else if errMsg != "" {
		// v0.10.1324 — durum ok kalır (oran/sınıf toplamları status'a bakar),
		// ama bozulmuş cevabın nedeni satırda görünür (ör. yineleme düşüşü).
		// Sınıf YOK: error_class yalnız status='error' satırlarında anlamlı.
		rec.ErrorMsg = truncErr(errMsg)
	}
	rec.PromptVersion = PromptVersion() // v0.10.409
	rec.ProfileID = s.profileID(ctx)
	s.emitRecord(rec)
}

// RecordExchange — v0.10.1153: bir CoSRE kullanıcı turunun /ai etkileşim
// satırı (api/chat_exchange.go). RecordUsage'ın ikizi ve AYNI kaydedici:
// aynı Recorder, aynı 4 KB örnek tavanı (truncForSample), aynı ateşle-unut
// gorutini. Farkı: bu satır bir model çağrısı DEĞİL — token taşımaz (turun
// model çağrıları kendi satırlarında, aynı exchange köküyle) ve sağlayıcı /
// model boştur; LLM'siz (deterministik) bir tur da böylece /ai'da görünür.
// Örnekler: soru (operatörün metni) ve ekrana giden cevap — prompt gövdesi
// DEĞİL. Yüzey ve exchange kimliği meta'dan (çağıran etkileşim etiketini ve
// TurnExchangeID'yi koyar). started sıfırsa süre 0 (RecordUsage sözleşmesi).
func (s *Service) RecordExchange(ctx context.Context, started time.Time, status, errMsg, question, answer string) {
	if s == nil || s.recorder == nil {
		return
	}
	meta := MetaFromContext(ctx)
	created := started
	if created.IsZero() {
		created = time.Now()
	}
	rec := CallRecord{
		ID:             NewCallID(),
		CreatedAt:      created,
		Surface:        recordSurface(ctx, meta.Surface), // v0.10.1316 — ajan kipi evalset öneki
		ExchangeID:     meta.ExchangeID,
		DurationMs:     durationMsSince(started),
		Status:         status,
		PromptChars:    uint32(len(question)),
		ResponseChars:  uint32(len(answer)),
		UserID:         meta.UserID,
		UserEmail:      meta.UserEmail,
		PromptSample:   truncForSample(question),
		ResponseSample: truncForSample(answer),
		TraceID:        callTraceID(ctx), // v0.10.1463 (D1)
	}
	switch status {
	case "error":
		rec.ErrorMsg = truncErr(errMsg)
		rec.ErrorClass = ClassifyAIErrorText(errMsg)
	case "aborted":
		// v0.10.1280 — istemci cevap gelmeden ayrıldı: arıza değil, hata
		// sınıfı da yok; neden metni yine görünür.
		rec.ErrorMsg = truncErr(errMsg)
	}
	rec.ProfileID = s.profileID(ctx)
	s.emitRecord(rec)
}

// emitRecord — kaydı kendi gorutininde, sınırlı ctx ile yazar (kullanıcı
// yanıtı CH yazımını beklemez; takılı bir yazım gorutini rehin tutamaz).
func (s *Service) emitRecord(rec CallRecord) {
	go func(r Recorder, rec CallRecord) {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r.RecordCall(rctx, rec)
	}(s.recorder, rec)
}

// ─── policy shells ──────────────────────────────────────────────────

// chatRequest — snapshot'ı tool'lu istek şekline çevirir. Ayarlar
// (bütçe/temperature/model) buffered explain ile TEK kaynaktan gelir:
// callSnapshot. 1024 paritesi tam da bunun yokluğundan doğmuştu.
func chatRequest(ctx context.Context, req aiprov.Request, system string, msgs []ChatMessage, tools []ToolSpec) aiprov.ChatRequest {
	return aiprov.ChatRequest{
		Model:       req.Model,
		MaxTokens:   cappedMaxTokens(ctx, req.MaxTokens), // v0.10.1324 — tavan yalnız WithMaxTokensCap ile
		Temperature: req.Temperature,
		ExtraBody:   req.ExtraBody, // v0.10.534 — thinking anahtarı tool döngüsüne de iner
		System:      system,
		Messages:    msgs,
		Tools:       tools,
		NoToolCalls: noToolCallsFrom(ctx),
		StrictTools: req.StrictTools, // v0.10.1456 (C1) — profil StrictToolArgs
	}
}

func chatTurnFrom(r aiprov.ChatResponse) ChatTurn {
	return ChatTurn{
		Text:              r.Text,
		ToolCalls:         r.ToolCalls,
		InputTokens:       clampTokens(r.InputTokens),
		OutputTokens:      clampTokens(r.OutputTokens),
		CachedTokens:      clampTokens(r.CachedTokens), // v0.10.807
		ToolCallsFromText: r.ToolCallsFromText,
		Degenerated:       r.Degenerated, // v0.10.1324
		RawContent:        r.RawContent,
	}
}

func (s *Service) chatAnthropicWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec) (ChatTurn, error) {
	cfg, req, _, _, _ := s.callSnapshot(ctx)
	resp, err := aiprov.ChatAnthropicTools(ctx, cfg, chatRequest(ctx, req, system, msgs, tools))
	return chatTurnFrom(resp), err
}

func (s *Service) chatOpenAIWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec) (ChatTurn, error) {
	cfg, req, _, _, _ := s.callSnapshot(ctx)
	resp, err := aiprov.ChatOpenAITools(ctx, cfg, chatRequest(ctx, req, system, msgs, tools))
	return chatTurnFrom(resp), err
}

// chatGitHubWithTools — Copilot tool-calling. Gövde openai-compat ile
// aynı; ayrışan tek şey uç + oturum jetonu.
//
// Sıra korunuyor: takas ÖNCE, snapshot SONRA (explainGitHub ile aynı
// gerekçe — takas kendi kilidini alıp jeton yazıyor).
func (s *Service) chatGitHubWithTools(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec) (ChatTurn, error) {
	sessTok, err := s.githubSessionToken(ctx)
	if err != nil {
		return ChatTurn{}, err
	}
	cfg, req, _, _, _ := s.callSnapshot(ctx)
	cfg.APIKey = sessTok
	resp, err := aiprov.ChatGitHubTools(ctx, cfg, chatRequest(ctx, req, system, msgs, tools))
	return chatTurnFrom(resp), err
}

// durationMsSince — sıfır zaman → 0 (ölçülmemiş), aksi hâlde geçen ms.
func durationMsSince(started time.Time) uint32 {
	if started.IsZero() {
		return 0
	}
	return uint32(time.Since(started).Milliseconds())
}
