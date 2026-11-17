package copilot

import (
	"context"
	"errors"
	"time"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// chat_stream.go — v0.10.1273 (CoSRE adım 4): serbest araç döngüsünün AKAN
// turu. ChatWithTools'un ikizi: aynı ChatTurn sözleşmesi (buffered turla
// birebir aynı çözümleme — provider/tools_stream.go), artı onDelta.
//
// Sağlayıcı matrisi:
//   - openai-compat (vLLM / Ollama / llama.cpp / Gemini uyumluluk ucu /
//     barındırılan OpenAI): stream:true + tool_calls delta'ları. Uç
//     stream:true'yu reddederse StreamText'in AYNI politikası
//     (streamFallbackPolicy) buffered'a düşer ve kararı önbelleğe yazar.
//   - anthropic: stream:true + tool_use/input_json_delta; aynı politika.
//   - github copilot: akış sözleşmesi doğrulanmadı (StreamText ile aynı
//     karar) → buffered.
//
// Buffered'a düşen her yolda (bilinen-desteklemez uç, düşüş, github) final
// turun metni TEK bir delta olarak yayınlanır — istemci akmayan sağlayıcıda
// da aynı olay sırasını görür (delta → answer). Tool-call turu metin
// yayınlamaz.

// TextDelta — akan turun parçası (provider.TextDelta takma adı).
type TextDelta = aiprov.TextDelta

// toolsVerdictSuffix — tool'lu akış kararının önbellek öneki: "tool'lu
// akış yok" kararı tek-atış anlatım akışını (StreamText) kapatmamalı; aynı
// uç tool'suz akabilir ama tool parser'ı akışta desteklemeyebilir.
const toolsVerdictSuffix = "+tools"

// ChatWithToolsStream — ChatWithTools gibi ONE model turu; final turun
// metni onDelta'ya akar. onDelta nil olabilir.
func (s *Service) ChatWithToolsStream(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec, onDelta func(TextDelta)) (ChatTurn, error) {
	if !s.activeFor(ctx, true) {
		return ChatTurn{}, errors.New("AI copilot not available (disabled or not configured — open Settings → AI Copilot)")
	}
	if err := s.localOnlyGate(ctx); err != nil { // v0.10.1451 — karar 8: sağlayıcıdan ÖNCE
		return ChatTurn{}, err
	}
	if onDelta == nil {
		onDelta = func(TextDelta) {}
	}
	// v0.10.2318 — ilk token damgası (StreamText'in streamStats'ı): ilk metinli
	// delta. Buffered yolda metin tek delta olarak SONDA gelir → düşüş işaretli,
	// TTFT 0 (bilinmiyor) kalır.
	st := &streamStats{started: time.Now()}
	ctx = withStreamStats(ctx, st)
	inner := onDelta
	onDelta = func(d TextDelta) {
		if d.Text != "" {
			st.markFirst()
		}
		inner(d)
	}
	var turn ChatTurn
	var err error
	provider, _, _ := s.profileIdentity(ctx)
	switch provider {
	case ProviderOpenAI:
		turn, err = s.streamToolsOpenAI(ctx, system, msgs, tools, onDelta)
	case ProviderGitHub:
		markStreamFallback(ctx)
		noteProviderRequests(ctx, 1)
		turn, err = bufferedTurnAsDelta(s.chatGitHubWithTools(ctx, system, msgs, tools))(onDelta)
	default:
		turn, err = s.streamToolsAnthropic(ctx, system, msgs, tools, onDelta)
	}
	if !st.fallbackSet() {
		turn.TTFTMs = st.ttftMs()
	}
	turn.ProviderRequests = st.providerRequests() // v0.10.2321
	return turn, err
}

// bufferedTurnAsDelta — akmayan yolun sonucu: final turda metin tek delta.
func bufferedTurnAsDelta(turn ChatTurn, err error) func(func(TextDelta)) (ChatTurn, error) {
	return func(onDelta func(TextDelta)) (ChatTurn, error) {
		if err == nil && len(turn.ToolCalls) == 0 && turn.Text != "" {
			onDelta(TextDelta{Text: turn.Text})
		}
		return turn, err
	}
}

func (s *Service) streamToolsOpenAI(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec, onDelta func(TextDelta)) (ChatTurn, error) {
	cfg, req, _, base, model := s.callSnapshot(ctx)
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o-mini"
	}
	creq := chatRequest(ctx, req, system, msgs, tools)
	key := ProviderOpenAI + toolsVerdictSuffix
	if bufferedOnlyFrom(ctx) || s.streamKnownUnsupported(key, base, model) { // v0.10.2321 — döngünün yeniden denemesi tek istek
		markStreamFallback(ctx) // v0.10.2318 — TTFT bilinmiyor
		noteProviderRequests(ctx, 1)
		return bufferedTurnAsDelta(s.chatOpenAIWithTools(ctx, system, msgs, tools))(onDelta)
	}
	resp, err := aiprov.StreamOpenAITools(ctx, cfg, creq, onDelta)
	noteProviderRequests(ctx, 1)
	var fe *aiprov.StreamFallbackError
	if errors.As(err, &fe) {
		if deferTransientFrom(ctx) && transientStreamFailure(fe) {
			return ChatTurn{}, fe // v0.10.2321 — düşüş yerine döngünün tek yeniden denemesi
		}
		markStreamFallback(ctx)
		noteProviderRequests(ctx, fallbackRequests(fe))
		turn, ferr := streamFallbackPolicy(s, fe, key, base, model,
			func(b []byte) (ChatTurn, error) {
				r, e := aiprov.ParseOpenAIToolsOneShot(b, creq)
				return chatTurnFrom(r), e
			},
			func() (ChatTurn, error) { return s.chatOpenAIWithTools(ctx, system, msgs, tools) })
		return bufferedTurnAsDelta(turn, ferr)(onDelta)
	}
	return chatTurnFrom(resp), err
}

func (s *Service) streamToolsAnthropic(ctx context.Context, system string, msgs []ChatMessage, tools []ToolSpec, onDelta func(TextDelta)) (ChatTurn, error) {
	cfg, req, _, _, model := s.callSnapshot(ctx)
	if model == "" {
		model = aiprov.DefaultAnthropicModel
	}
	creq := chatRequest(ctx, req, system, msgs, tools)
	key := ProviderAnthropic + toolsVerdictSuffix
	if bufferedOnlyFrom(ctx) || s.streamKnownUnsupported(key, "", model) { // v0.10.2321
		markStreamFallback(ctx) // v0.10.2318 — TTFT bilinmiyor
		noteProviderRequests(ctx, 1)
		return bufferedTurnAsDelta(s.chatAnthropicWithTools(ctx, system, msgs, tools))(onDelta)
	}
	resp, err := aiprov.StreamAnthropicTools(ctx, cfg, creq, onDelta)
	noteProviderRequests(ctx, 1)
	var fe *aiprov.StreamFallbackError
	if errors.As(err, &fe) {
		if deferTransientFrom(ctx) && transientStreamFailure(fe) {
			return ChatTurn{}, fe // v0.10.2321 — düşüş yerine döngünün tek yeniden denemesi
		}
		markStreamFallback(ctx)
		noteProviderRequests(ctx, fallbackRequests(fe))
		turn, ferr := streamFallbackPolicy(s, fe, key, "", model,
			func(b []byte) (ChatTurn, error) {
				r, e := aiprov.ParseAnthropicToolsOneShot(b)
				return chatTurnFrom(r), e
			},
			func() (ChatTurn, error) { return s.chatAnthropicWithTools(ctx, system, msgs, tools) })
		return bufferedTurnAsDelta(turn, ferr)(onDelta)
	}
	return chatTurnFrom(resp), err
}
