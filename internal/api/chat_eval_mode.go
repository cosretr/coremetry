package api

// chat_eval_mode.go — v0.10.1316 (değerlendirme seti gerçek ajan döngüsünde;
// DECISIONS "2026-10-18 — Değerlendirme seti gerçek ajan döngüsünde").
//
// Ajan kipi evalset vakası canlı sohbet handler'ını (copilotChat) SÜREÇ
// İÇİNDE koşar (D1): aynı kademeler, aynı katalog, aynı Executor, aynı tur /
// çağrı tavanları. Handler'ın üretimden ayrıldığı yerler yalnız bu ctx
// işaretine bakan küçük anahtarlardır (copilot_chat.go / chat_exchange.go,
// her biri "v0.10.1316" işaretli):
//
//   - ai_calls yüzeyi + exchange kimliği işaretten (evalset- öneki → /ai'da
//     "Kaynak: Değerlendirme", üretim sayılarına karışmaz);
//   - turun etkileşim satırı (cosre-turn) ve cosre_interactions YAZILMAZ —
//     sonuç Sink'e gider;
//   - araç seti işaretten (koşunun çözümü / açık seçimi);
//   - dış MCP araçları ve sohbet bağlamı araçları kataloğa girmez, sohbet
//     bağlamı (Redis) okunmaz/yazılmaz;
//   - araç başına mcp.tool.call audit'i yazılmaz (D4 — koşu düzeyi audit
//     ai.evalset.run kalır);
//   - v0.10.1321 — niyet kademesi atlanır (SkipIntent; vaka expectTier
//     "intent" sabitlemediyse);
//   - v0.10.1476 — döngü sondası (LoopProbe; vaka expectTier "loop"
//     sabitlediyse): guided, RAG ve niyet kademeleri de atlanır, soru serbest
//     araç döngüsüne ulaşır; döngünün sunduğu katalog OnCatalog'a bildirilir;
//   - her model turu OnTurn'e, her Executor çağrısı OnTool'a bildirilir.
//
// İşaret nil iken (üretim) her yol bayt bayt eskisidir
// (TestChatEvalModeNilIsByteIdentical). Bu dosya bilinçli olarak yüzey
// LİTERALİ taşımaz (TestChatSurfacesRegistered chat_*.go'yu tarar): etiket
// ai_evalset_agent.go'da kurulur, buraya değer olarak gelir.

import (
	"context"
	"encoding/json"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatEvalMode — ajan kipi evalset vakasının handler'a taşıdığı işaret.
type chatEvalMode struct {
	// Surface — ai_calls yüzeyi (evalset-<Yüzey>); ExchangeID — "<koşu>/<vaka>".
	Surface, ExchangeID string
	// Toolset — koşunun araç seti (full | simple); "" = canlı çözüm.
	Toolset string
	// SkipIntent — v0.10.1321 (operatör): niyet kademesi (copilotChatIntent —
	// LLM sınıflandırıcısı + on_no_loop genel cevabı) bu vakada koşmaz; düz
	// soru serbest araç döngüsüne ulaşır. guided ve öteki kademeler açık.
	// evalsetServerAgent kurar (evalAgentSkipIntent: vaka expectTier "intent"
	// sabitlemediyse true).
	SkipIntent bool
	// LoopProbe — v0.10.1476 (DECISIONS "Değerlendirme seti prod koşusu
	// düzeltmeleri"): vaka serbest döngünün ARAÇ SEÇİMİNİ ölçer. Prod bu
	// soruların çoğunu deterministik guided rotasına gönderir (o yarı
	// chat_routing_regression_test.go'da CI'da pinli); sonda guided'ı, RAG'ı
	// ve niyet kademesini atlar. evalsetServerAgent kurar (evalAgentLoopProbe).
	LoopProbe bool
	// OnCatalog — v0.10.1476: serbest döngünün bu turda modele SUNDUĞU araç
	// adları (rol / araç seti / kapalı toolset / localOnly süzgeçlerinden
	// sonra). nil = bildirim yok. Beklenen araç sunulmadıysa vaka model
	// hatası değil kurulum (harness) farkıdır.
	OnCatalog func(names []string)
	// OnTurn — serbest döngünün her model turu; round < 0 = tavan turu
	// (araç çağrısı yasak son tur). nil = bildirim yok.
	OnTurn func(round int, turn copilot.ChatTurn, err error)
	// OnTool — Executor'dan geçen her çağrı (yürütülmeyenler dâhil; tavanı
	// aşıp Executor'a hiç gitmeyenler hariç). args = gerçekten koşan argüman.
	OnTool func(name string, args json.RawMessage, oc agenttools.Outcome)
	// Sink — turun sonucu (recordChatExchange'in yerine). nil = sonuç atılır.
	Sink func(evalAgentOutcome)
	// UserTeam — CoSRE v2 araç seti dilim 2 (tools-audit §10 G8): vakanın
	// fikstür users.team'i; doluysa kullanıcı kapsamı users tablosu okunmadan
	// bu takımdan çözülür. "" = üretim yolu (koşuyu başlatanın takımı).
	// v0.10.2106 — v2 işareti artık buna bağlı değil (her ajan vakası v2).
	UserTeam string
	// ResearchProfile — v0.10.2365 (S6): koşunun researchProfile alanı; doluysa
	// profil ayrımı bu profille araştırır (canlı chat-research eşlemesi
	// değerlendirmede okunmaz). "" = ayrım yok.
	ResearchProfile string
	// OnSplit — ayrımın sinyalleri (research_no_tools / research_fallback /
	// narrate_fallback). nil = bildirim yok.
	OnSplit func(signal string)
	// OnScope — v2 döngüsünün çözdüğü kullanıcı kapsamı (puanlama: her çağrının
	// services ⊆ kapsam). nil = bildirim yok.
	OnScope func(userScope)
	// Conversation — v0.10.2322 (H1, conversation-audit.md §6.1): konuşma
	// kipi turu (chat_eval_conversation.go). nil = tek turlu ajan vakası,
	// yukarıdaki bütün anahtarlar eskisi gibi. Doluyken handler ÜRETİM
	// kademe zincirini koşar (singleTurn false), sohbet bağlamı Redis yerine
	// koşucunun bellek içi durumundan okunur ve ona yazılır, döngü ve guided
	// veri okumaları vakanın sahte sonuçlarından gelir.
	Conversation *evalConvTurn
}

// evalAgentOutcome — ajan kipi turun sonucu (chatExchange'den, kilit altında
// okunur). Oy / geri bildirim alanı YOKTUR (TestEvalNeverReadsVotes).
type evalAgentOutcome struct {
	Tier, Route, Status, ErrMsg, Answer, Model string
	// Tools — turun araç çipleri, cosre_interactions ile AYNI kırpmayla
	// (args 1 KiB, çıktı önizlemesi 4 KiB, hata 512 B); guided ön-yüklemesi
	// Origin "guided", niyet sınıflandırıcısı Origin "intent".
	Tools   []chstore.CosreTool
	Deep    bool
	Toolset string
	// ModelCalls / InputTokens / OutputTokens — turun BÜTÜN model çağrıları
	// (niyet, guided anlatımı, döngü) — copilot.CallSink toplamı.
	ModelCalls                uint16
	InputTokens, OutputTokens uint32
	// Loop / TTFTMs — v0.10.2323 (H1): turun v0.10.2318 döngü telemetrisi
	// (ai_calls'a yazılanın aynısı) ve son model çağrısının ilk token'ı.
	Loop   copilot.LoopUsage
	TTFTMs uint32
	// BoundTo — v0.10.1476: koşucunun vakayı bağladığı canlı servis (handler
	// doldurmaz; evalsetServerAgent yazar).
	BoundTo string
	// Verify — v0.10.2327 (E1): turun gölge doğrulama sonucu; nil = anlatım
	// yok ya da doğrulama kapalı.
	Verify *verifyResult
}

type chatEvalModeKey struct{}

// withChatEvalMode — işareti ctx'e koyar; nil ctx'i aynen döndürür.
func withChatEvalMode(ctx context.Context, em *chatEvalMode) context.Context {
	if em == nil {
		return ctx
	}
	return context.WithValue(ctx, chatEvalModeKey{}, em)
}

// chatEvalModeFrom — ctx'teki işaret; üretimde nil.
func chatEvalModeFrom(ctx context.Context) *chatEvalMode {
	if ctx == nil {
		return nil
	}
	em, _ := ctx.Value(chatEvalModeKey{}).(*chatEvalMode)
	return em
}

// skipIntent — v0.10.1321: niyet kademesi atlanır mı. nil (üretim) → false,
// yani kademe kararı bayt bayt eski.
func (em *chatEvalMode) skipIntent() bool { return em != nil && (em.SkipIntent || em.LoopProbe) }

// skipGuided — v0.10.1476: döngü sondasında guided ve RAG kademeleri
// atlanır. nil (üretim) → false, kademe sırası bayt bayt eski.
func (em *chatEvalMode) skipGuided() bool { return em != nil && em.LoopProbe }

// singleTurn — v0.10.2322: tek turlu ajan vakası mı (konuşmasız kademe
// kapıları kapalı). nil (üretim) → false; handler'daki `!evm.singleTurn()`
// kapıları üretimde eski `evm == nil` ile birebir aynıdır. Konuşma kipi
// (Conversation dolu) → false: kademe zinciri üretimdeki gibi açık.
func (em *chatEvalMode) singleTurn() bool { return em != nil && em.Conversation == nil }

// evalChatContext — v0.10.2322: değerlendirme vakasının sohbet bağlamı.
// Tek turlu vaka boş durum (eski davranış); konuşma kipi koşucunun bellek
// içi durumu (kullanıcısız → flushChatContext Redis'e yazmaz).
func (em *chatEvalMode) evalChatContext() *chatContextState {
	if em != nil && em.Conversation != nil && em.Conversation.State != nil {
		return em.Conversation.State
	}
	return &chatContextState{}
}

// evalOutcomeFromExchange — SAF (x kilidi altında okur): Sink yükü.
func evalOutcomeFromExchange(x *chatExchange, tier, status, msg string) evalAgentOutcome {
	x.mu.Lock()
	defer x.mu.Unlock()
	return evalAgentOutcome{
		Tier: tier, Route: x.route, Status: status, ErrMsg: msg, Answer: x.answer, Model: x.model,
		Tools: append([]chstore.CosreTool(nil), x.tools...), Deep: x.deep, Toolset: x.toolset,
		ModelCalls: x.modelCalls, InputTokens: x.inTok, OutputTokens: x.outTok,
		Loop: x.loop, TTFTMs: x.callTTFT, // v0.10.2323
	}
}
