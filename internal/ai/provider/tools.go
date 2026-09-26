package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// tools.go — tool-calling (function-calling) taşıması. FAZ 1.3:
// copilot/chat.go'daki chatOpenAIWithTools + chatAnthropicWithTools
// gövdeleri ve openAIChatTransport'un uç/header çözümü buraya taşındı;
// tel üstündeki şekil BİREBİR aynı.
//
// Sağlayıcı-nötr sözleşme (ToolSpec/ToolCall/ToolResult/ChatMessage)
// da buraya taşındı ve copilot'ta TAKMA AD olarak yaşıyor: bu tipler
// tel şeklinin parçası (ToolCall.Raw'ın var olma sebebi bir tel
// olayıdır — v0.8.373), taşımanın sözlüğüne aitler. Takma ad seçimi
// churn'ü sıfırlar: internal/api ve internal/mcptools'taki 20+ çağrı
// yeri (copilot.ChatMessage, copilot.ToolSpec, …) TEK SATIR bile
// değişmedi — takma ad Go'da AYNI tiptir, /api/copilot/chat gövdesinin
// JSON alan adları da aynen korunur.
//
// Sağlayıcı-özgü kodlamalar (bilinçli olarak birebir taşındı):
//   - Anthropic: tools + tool_use/tool_result içerik blokları
//   - OpenAI/GitHub: openai tools + tool_calls + role:tool mesajları
//     (+ Gemini uyumluluk ucu için HAM tool_call tekrarı, v0.8.373)

// ToolSpec, LLM'e verilen sağlayıcı-nötr fonksiyon tanımı.
// InputSchema JSON Schema'dır (draft 2020-12) — MCP tool'larının zaten
// bildirdiği şekil, böylece API katmanı mcp.Tool → ToolSpec'i 1:1 eşler.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// ToolCall, modelin istediği tek bir fonksiyon çağrısı.
type ToolCall struct {
	ID    string          // sağlayıcı üretimi id, sonuçta geri yollanır
	Name  string          // tool adı
	Input json.RawMessage // argümanlar (JSON)
	// Raw, openai-compat sağlayıcısının döndürdüğü TAM tool_call
	// nesnesidir (v0.8.373, operatör-bildirimi). Gemini'nin uyumluluk
	// ucu ek alanlar iliştiriyor (extra_content → thought_signature) ve
	// tekrar oynatılan functionCall onları taşımıyorsa sonraki turu 400
	// INVALID_ARGUMENT ile REDDEDİYOR — nesneyi yukarıdaki kırpılmış
	// alanlardan yeniden kurmak bilinmeyen her şeyi sessizce düşürüyordu.
	// Doluysa tekrar kodlayıcısı Raw'ı BİREBİR gönderir; ID/Name/Input
	// yürütücünün çözümlenmiş görüşü olarak kalır. Anthropic yolunda ve
	// eski mesajlarda nil.
	Raw json.RawMessage `json:",omitempty"`
}

// ToolResult, bir ToolCall'ın yürütülmüş çıktısı; model okuyabilsin
// diye sonraki tura geri beslenir.
type ToolResult struct {
	CallID  string
	Name    string
	Content string // JSON'a çevrilmiş tool çıktısı (ya da hata metni)
	IsError bool
}

// ChatMessage, sağlayıcı-nötr tek konuşma turu. Bir kullanıcı turu
// Text (soru) VEYA ToolResults (fonksiyon çıktıları) taşır. Bir
// asistan turu Text (düz metin) ve/veya ToolCalls (çalıştırılmasını
// istediği fonksiyonlar) taşır.
type ChatMessage struct {
	Role        string       // "user" | "assistant"
	Text        string       `json:",omitempty"`
	ToolCalls   []ToolCall   `json:",omitempty"`
	ToolResults []ToolResult `json:",omitempty"`
	// RawContent — Anthropic asistan turunun HAM content dizisi (thinking +
	// text + tool_use, imzalarıyla). Doluysa BİREBİR tekrar oynatılır:
	// düşünen modeller (Sonnet 5 / Opus 5 thinking alanı yokken de düşünür,
	// Fable 5.1 hep düşünür) düşürülmüş ya da değiştirilmiş thinking bloğunu
	// sonraki turda reddeder. ToolCall.Raw'ın (Gemini thought_signature,
	// v0.8.373) Anthropic ikizi; openai-compat yolunda ve eski mesajlarda nil.
	// json:"-": yalnız sunucu döngüsünün içinde yaşar — istemcinin gönderdiği
	// sohbet gövdesinden ham blok enjekte edilemez.
	RawContent json.RawMessage `json:"-"`
}

// ChatRequest — çok turlu, tool'lu tek bir model çağrısının girdileri.
// Request'in (tek-atış explain) tool'lu ikizi: aynı ayar alanları,
// System + tek User yerine Messages + Tools.
type ChatRequest struct {
	Model     string
	MaxTokens int
	// Temperature nil = gövdeye HİÇ koyma (bkz. Request.Temperature).
	Temperature *float64
	// ExtraBody — v0.10.534: bkz. Request.ExtraBody (yalnız openai-uyumlu gövde).
	ExtraBody map[string]any
	System    string
	Messages  []ChatMessage
	Tools     []ToolSpec
	// NoToolCalls — tur tavanı: tool TANIMLARI gövdede kalır (geçmişteki
	// tool_use/tool_result blokları tanımlı tool ister; önek önbelleği de
	// korunur) ama çağrı yasaktır — Anthropic'te tool_choice {type:"none"},
	// barındırılan OpenAI / GitHub'da tool_choice "none". Yerel openai-compat
	// uçlarında bugünkü şekil (boş tools dizisi) korunur.
	NoToolCalls bool
}

// ChatResponse — çözümlenmiş tur. ToolCalls doluysa çağıran onları
// çalıştırıp döngüye devam eder; boşsa Text nihai cevaptır.
type ChatResponse struct {
	Text      string
	ToolCalls []ToolCall
	// ToolCallsFromText — v0.10.545: tool_calls boştu, çağrılar content'ten
	// ayrıştırıldı (Gemma 4 sınıfı sunucu parser eksikliği). Gözlem için.
	ToolCallsFromText bool
	InputTokens       int
	OutputTokens      int
	CachedTokens      int // v0.10.807 — bkz. Response.CachedTokens
	// RawContent — Anthropic yanıtının ham content dizisi; bir sonraki turda
	// ChatMessage.RawContent olarak aynen geri gider (thinking blokları dâhil).
	RawContent json.RawMessage
}

func (r ChatRequest) resolvedModel(cfg Config, fallback string) string {
	if r.Model != "" {
		return r.Model
	}
	if cfg.Model != "" {
		return cfg.Model
	}
	return fallback
}

func (r ChatRequest) resolvedMaxTokens() int {
	if r.MaxTokens > 0 {
		return r.MaxTokens
	}
	return defaultMaxTokens
}

// ─── Anthropic tool-calling ─────────────────────────────────────────

// ChatAnthropicTools tek bir tool'lu Messages turu yürütür.
func ChatAnthropicTools(ctx context.Context, cfg Config, req ChatRequest) (ChatResponse, error) {
	if cfg.HTTPClient == nil {
		return ChatResponse{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	raw, err := json.Marshal(buildAnthropicToolsBody(cfg, req))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(raw))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-API-Key", cfg.APIKey)
	httpReq.Header.Set("Anthropic-Version", anthropicVersion)

	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("anthropic chat: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if resp.StatusCode >= 300 {
		// HTTPError metni eski fmt.Errorf ile birebir aynı ("anthropic
		// 429: …") — kota devre-kesicisi (copilot.isQuotaErr) METNE
		// bakıyor; ayrıca statü artık tipten okunabiliyor.
		return ChatResponse{}, &HTTPError{Provider: labelAnthropic, Status: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	return parseAnthropicToolsChat(respBody)
}

// buildAnthropicToolsBody — tool_use/tool_result blok kodlaması.
// Saf: golden testler gövdeyi ağsız pinleyebilsin diye ayrı.
func buildAnthropicToolsBody(cfg Config, req ChatRequest) map[string]any {
	apiMsgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "assistant" && len(m.RawContent) > 0 {
			// Ham asistan turu BİREBİR: thinking blokları imzalarıyla birlikte
			// döner; yeniden kurmak onları düşürür ve düşünen model 400 verir.
			apiMsgs = append(apiMsgs, map[string]any{"role": "assistant", "content": m.RawContent})
			continue
		}
		var blocks []map[string]any
		if m.Text != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
		}
		for _, tc := range m.ToolCalls {
			var input any
			_ = json.Unmarshal(tc.Input, &input)
			blocks = append(blocks, map[string]any{
				"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": input,
			})
		}
		for _, tr := range m.ToolResults {
			blocks = append(blocks, map[string]any{
				"type": "tool_result", "tool_use_id": tr.CallID,
				"content": tr.Content, "is_error": tr.IsError,
			})
		}
		apiMsgs = append(apiMsgs, map[string]any{"role": m.Role, "content": blocks})
	}

	apiTools := make([]map[string]any, 0, len(req.Tools))
	for i, t := range req.Tools {
		def := map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": t.InputSchema,
		}
		if i == len(req.Tools)-1 {
			// Araçlar sistemden ÖNCE işlenir; deterministik sıralı katalog
			// tek kesme noktasıyla alışverişler ARASINDA da önbelleklenir
			// (sistem, istek-başı önsözler taşıdığından alışverişten alışverişe
			// değişir).
			def["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		apiTools = append(apiTools, def)
	}

	body := map[string]any{
		"model": req.resolvedModel(cfg, DefaultAnthropicModel),
		// v0.8.393 (AI denetimi A2) — eskiden 1500'dü: Explain'in
		// öğrendiği bütçe reasoning modelleri için küçük (v0.8.384).
		// v0.9.1120 — sabit operatör-ayarlı bir getter'a dönüştü ve
		// temperature eklendi (anthropic gövdeleri hiç taşımıyordu, yani
		// bu yol sağlayıcı varsayılanı ≈1.0'da koşuyordu).
		"max_tokens": req.resolvedMaxTokens(),
		// Sistem + araç kataloğu bir alışverişin turları boyunca bayt-bayt
		// aynı; bu kesme noktası ikisini birlikte önbellekler. Üst düzey
		// cache_control büyüyen konuşmayı izler (otomatik önbellek). Eşiğin
		// altındaki önek sessizce önbelleklenmez.
		"system":        []map[string]any{{"type": "text", "text": req.System, "cache_control": map[string]any{"type": "ephemeral"}}},
		"cache_control": map[string]any{"type": "ephemeral"},
		"messages":      apiMsgs, "tools": apiTools,
	}
	if req.NoToolCalls && len(apiTools) > 0 {
		// Tur tavanı: tanımlar kalır (geçmişteki tool_use/tool_result bloklarının
		// tanımlı tool istemesi + önek önbelleği), çağrı yasak.
		body["tool_choice"] = map[string]any{"type": "none"}
	}
	// temperature bilinçli olarak GÖNDERİLMEZ (anthropic.go başlığı, v0.10.253 D1).
	return body
}

func parseAnthropicToolsChat(respBody []byte) (ChatResponse, error) {
	var parsed struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		Usage      anthropicUsage  `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("decode anthropic chat: %w", err)
	}
	out := ChatResponse{InputTokens: parsed.Usage.totalInput(), OutputTokens: parsed.Usage.OutputTokens,
		CachedTokens: parsed.Usage.CacheReadInputTokens}
	// ParseAnthropic ile aynı sözleşme (v0.10.253 D3): ret açık hatadır, boş
	// nihai cevap değil — araç döngüsü onu "cevap" sanıp künyeyle basıyordu.
	if parsed.StopReason == "refusal" {
		return out, errAnthropicRefusal
	}
	var blocks []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if len(parsed.Content) > 0 {
		if err := json.Unmarshal(parsed.Content, &blocks); err != nil {
			return ChatResponse{}, fmt.Errorf("decode anthropic chat content: %w", err)
		}
	}
	if len(blocks) > 0 {
		out.RawContent = parsed.Content
	}
	var text strings.Builder
	for _, c := range blocks {
		switch c.Type {
		case "text":
			text.WriteString(c.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.ID, Name: c.Name, Input: c.Input})
		}
	}
	out.Text = text.String()
	return out, nil
}

// ─── OpenAI / GitHub tool-calling ───────────────────────────────────
//
// GitHub Copilot openai chat-completions şeklini konuşuyor, o yüzden
// GÖVDE tek yazılış (buildOpenAIToolsBody); yalnız uç + auth header'ları
// ayrışıyor ve o ayrım iki giriş noktasıyla ifade ediliyor. Alternatif
// (tek fonksiyon + "bu github mu" bayrağı) taşımaya sağlayıcı KİMLİĞİ
// sokardı; Do*/Chat*Tools üçlüsü aynı hizada duruyor.

// ChatOpenAITools tek bir tool'lu openai-compat turu yürütür.
func ChatOpenAITools(ctx context.Context, cfg Config, req ChatRequest) (ChatResponse, error) {
	if cfg.HTTPClient == nil {
		return ChatResponse{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultBaseURL
	}
	hdrs := map[string]string{"Content-Type": "application/json"}
	if cfg.APIKey != "" {
		hdrs["Authorization"] = "Bearer " + cfg.APIKey
		// Çıplak api-key ikizi vLLM/KServe tarzı geçitler için —
		// Explain yoluyla aynı gerekçe (v0.8.384).
		hdrs["api-key"] = cfg.APIKey
	}
	// tool_choice "none" yalnız barındırılan OpenAI'de (boş tools dizisini
	// reddediyor); yerel uçların tool_choice desteği doğrulanmadı → bugünkü şekil.
	return chatOpenAIShape(ctx, cfg, req,
		strings.TrimRight(base, "/")+"/chat/completions", hdrs,
		defaultModel, labelOpenAI, strings.TrimRight(base, "/") == defaultBaseURL)
}

// ChatGitHubTools tek bir tool'lu Copilot turu yürütür.
// cfg.APIKey = ÇÖZÜLMÜŞ oturum jetonu (jeton takası DURUM taşır ve
// copilot.Service'te kalır — DoGitHub ile aynı kural).
func ChatGitHubTools(ctx context.Context, cfg Config, req ChatRequest) (ChatResponse, error) {
	if cfg.HTTPClient == nil {
		return ChatResponse{}, errors.New("provider: nil HTTPClient — timeout ve TLS-skip ayarları onun içinde yaşıyor")
	}
	return chatOpenAIShape(ctx, cfg, req, githubChatURL, map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + cfg.APIKey,
		// Entegrasyon header'ları kapı bekçisi — eksikse edge 403 döner.
		"Copilot-Integration-Id": "vscode-chat",
		"Editor-Version":         "vscode/1.85.0",
		"Editor-Plugin-Version":  "copilot-chat/0.12.0",
		"User-Agent":             "GithubCopilot/1.155.0",
	}, defaultGitHubModel, labelGitHub, true)
}

// chatOpenAIShape — toolChoiceNone: uç tool_choice "none"u kabul ediyor
// (barındırılan OpenAI / GitHub); NoToolCalls turunda tanımlar gövdede kalır.
func chatOpenAIShape(ctx context.Context, cfg Config, req ChatRequest,
	url string, hdrs map[string]string, fallbackModel, label string, toolChoiceNone bool) (ChatResponse, error) {

	raw, err := json.Marshal(buildOpenAIToolsBody(cfg, req, fallbackModel, toolChoiceNone))
	if err != nil {
		return ChatResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return ChatResponse{}, err
	}
	for k, v := range hdrs {
		httpReq.Header.Set(k, v)
	}
	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("%s chat: %w", label, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if resp.StatusCode >= 300 {
		return ChatResponse{}, &HTTPError{Provider: label, Status: resp.StatusCode, Body: strings.TrimSpace(string(respBody))}
	}
	// Metin-gömülü çağrı geri düşüşü yalnız tool SUNULMUŞKEN: tur-tavanı
	// turunda çağrı-şekilli metin cevabın parçasıdır.
	offered := toolNames(req.Tools)
	if req.NoToolCalls {
		offered = nil
	}
	return parseOpenAIToolsChat(respBody, label, offered)
}

func toolNames(ts []ToolSpec) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// buildOpenAIToolsBody — openai tools + tool_calls + role:tool mesaj
// kodlaması. Saf.
func buildOpenAIToolsBody(cfg Config, req ChatRequest, fallbackModel string, toolChoiceNone bool) map[string]any {
	apiMsgs := []map[string]any{{"role": "system", "content": req.System}}
	for _, m := range req.Messages {
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			// Ham nesneyi yakaladıysak her tool_call'ı BİREBİR tekrar
			// oynat — Gemini'nin uyumluluk ucu thought_signature (ve
			// gelecekteki her ek alan) eksikse 400 döner (v0.8.373).
			// Yeniden kurma yalnız Raw'sız eski mesajların yedeği.
			tcs := make([]any, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				if len(tc.Raw) > 0 {
					tcs = append(tcs, json.RawMessage(tc.Raw))
					continue
				}
				tcs = append(tcs, map[string]any{
					"id": tc.ID, "type": "function",
					"function": map[string]any{"name": tc.Name, "arguments": string(tc.Input)},
				})
			}
			apiMsgs = append(apiMsgs, map[string]any{
				"role": "assistant", "content": m.Text, "tool_calls": tcs,
			})
			continue
		}
		if len(m.ToolResults) > 0 {
			// OpenAI: her tool sonucu KENDİ role:tool mesajıdır.
			for _, tr := range m.ToolResults {
				apiMsgs = append(apiMsgs, map[string]any{
					"role": "tool", "tool_call_id": tr.CallID, "content": tr.Content,
				})
			}
			continue
		}
		apiMsgs = append(apiMsgs, map[string]any{"role": m.Role, "content": m.Text})
	}

	apiTools := make([]map[string]any, 0, len(req.Tools))
	if !req.NoToolCalls || toolChoiceNone {
		for _, t := range req.Tools {
			apiTools = append(apiTools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name": t.Name, "description": t.Description, "parameters": t.InputSchema,
				},
			})
		}
	}

	body := map[string]any{
		"model": req.resolvedModel(cfg, fallbackModel),
		// v0.8.393 (AI denetimi A2) — 1500 → paylaşılan 4096 bütçesi.
		// v0.9.1120 — iki sabit de getter'a döndü.
		"max_tokens": req.resolvedMaxTokens(),
		"messages":   apiMsgs, "tools": apiTools,
	}
	if req.NoToolCalls && len(apiTools) > 0 {
		// Tur tavanı, barındırılan uç: boş tools dizisi burada 400 alır;
		// tanımlar kalır, çağrı "none" ile kapanır.
		body["tool_choice"] = "none"
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	applyExtraBody(body, req.ExtraBody) // v0.10.534
	return body
}

func parseOpenAIToolsChat(respBody []byte, label string, offered []string) (ChatResponse, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				// Reasoning-model yedekleri (v0.8.384, Explain yoluyla
				// aynı): vLLM ≥0.24 cevabı content:null ile `reasoning`e
				// koyar; deepseek-r1/Qwen3 tarzı sunucular
				// `reasoning_content` kullanır.
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				// Bilinçli olarak ham nesneler (v0.8.373): Gemini'nin
				// uyumluluk ucu sağlayıcı ekleri iliştiriyor
				// (extra_content → thought_signature) ve bunlar tekrar
				// oynatmada HAYATTA KALMALI; her biri aşağıda yürütücünün
				// görüşü için ayrıca çözümlenir.
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int                `json:"prompt_tokens"`
			CompletionTokens    int                `json:"completion_tokens"`
			PromptTokensDetails promptTokensDetail `json:"prompt_tokens_details"` // v0.10.807
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return ChatResponse{}, fmt.Errorf("decode openai-compat chat: %w", err)
	}
	out := ChatResponse{InputTokens: parsed.Usage.PromptTokens, OutputTokens: parsed.Usage.CompletionTokens,
		CachedTokens: parsed.Usage.PromptTokensDetails.CachedTokens}
	if len(parsed.Choices) == 0 {
		// Metin eski yazılışıyla aynı: openai-compat için
		// "openai-compat: empty response". GitHub yolu bu kodlayıcıyı
		// v0.6.53'ten beri paylaşıyor; etiket artık doğruyu söylüyor.
		return out, fmt.Errorf("%s: empty response", label)
	}
	msg := parsed.Choices[0].Message
	out.Text = msg.Content
	// salvaged — metin modelin DÜŞÜNCESİNDEN geliyor; sonda işaretlenir
	// (salvage.go v0.10.66: explain yolunun SalvageAnswer'ıyla aynı kural).
	salvaged := false
	// Satır-içi <think> bloğu (reasoning parser'sız sunucuda Qwen3 sınıfı)
	// soyulur — explain yolu SalvageAnswer'ın 1. basamağıyla aynı. İçerik
	// YALNIZ düşünceyse ve tool çağrısı yoksa 3. basamak: düşüncenin içi.
	if strings.Contains(out.Text, "</think>") {
		if st := StripThinking(out.Text); st != "" {
			out.Text = st
		} else if len(msg.ToolCalls) == 0 {
			out.Text, salvaged = ThinkingContent(msg.Content), true
		} else {
			out.Text = ""
		}
	}
	// Reasoning-model yedeği (v0.8.384): content boşsa cevap
	// reasoning_content / reasoning'de yaşıyor; baştaki <think> bloğu
	// Explain'le aynı şekilde soyulur. YALNIZ tool çağrısı YOKKEN — bir
	// tool-call turunun boş content'i MEŞRUDUR ve doldurulursa model
	// "cevap verdim" sanılıp döngü erken biter.
	if strings.TrimSpace(out.Text) == "" && len(msg.ToolCalls) == 0 {
		if alt := strings.TrimSpace(msg.ReasoningContent); alt != "" {
			out.Text, salvaged = StripThinking(alt), true
		} else if alt := strings.TrimSpace(msg.Reasoning); alt != "" {
			out.Text, salvaged = StripThinking(alt), true
		}
	}
	for _, rawCall := range msg.ToolCalls {
		var tc struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(rawCall, &tc); err != nil {
			return ChatResponse{}, fmt.Errorf("decode openai-compat tool_call: %w", err)
		}
		args := tc.Function.Arguments
		if args == "" {
			args = "{}"
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(args), Raw: rawCall,
		})
	}
	// v0.10.545 — metin-gömülü çağrı geri düşüşü (toolcall_text.go): yalnız
	// tool SUNULMUŞKEN ve yapılandırılmış tool_calls YOKKEN. Açık çağrı
	// sınırlayıcısında ad süzgeci yok (Executor uydurma adı sözleşmeyle
	// düzeltir); cevapların da kullandığı biçimlerde (``` çiti, çıplak JSON)
	// yalnız sunulan adlar — yoksa cevaptaki {"name":"checkout-service",…}
	// örneği çağrıya dönüşüp cevabı yiyordu. Kesik JSON → çağrı yok.
	if len(out.ToolCalls) == 0 && len(offered) > 0 {
		if calls, rest, ok := ParseTextToolCalls(out.Text, offered); ok {
			out.ToolCalls, out.Text, out.ToolCallsFromText = calls, rest, true
		}
	}
	if salvaged && len(out.ToolCalls) == 0 {
		out.Text = MarkSalvagedThinking(out.Text)
	}
	return out, nil
}
