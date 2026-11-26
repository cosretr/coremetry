package api

// chat_guided_escalate.go — v0.10.2473 (DECISIONS "2026-11-26 — v0.10.2473").
//
// Guided rota = deterministik ön-getirme demeti + TEK araçsız anlatım. Soru
// demetin kapsamadığı ikinci bir parça taşıyorsa ("checkout nasıl ve hangi
// endpoint en yavaş", "checkout ile payments'ı karşılaştır") model hiçbir şey
// çağıramadığı için ikinci parçayı ya atlıyor ya uyduruyordu. Artık:
//
//   - Karar SAF ve deterministik (guidedEscalatesToLoop): rota × döngü işareti
//     (chat_loop_cue.go). Ek model çağrısı yok.
//   - Tırmanırsa demet anlatılmaz: serbest araç döngüsünün konuşmasına sentetik
//     tool çağrısı + sonucu olarak tohumlanır (operasyon ön-getirmesinin
//     seedScaled deseni, chat_operation_prefetch.go) ve model eksik parça için
//     başka araç çağırabilir. Demet yeniden okunmaz.
//   - Yalnız ana guided kademesi (copilotChatGuided) tırmanır: yuva
//     (withGuidedEscSlot) yalnız o çağrının ctx'inde. Niyet sınıflandırıcısı,
//     kapsam / küme kademeleri ve tek-cevaplı rotalar bugünkü gibi anlatır.

import (
	"context"
	"encoding/json"

	"github.com/cilcenk/coremetry/internal/copilot"
)

const (
	guidedEscTool   = "guided_prefetch"
	guidedEscToolID = "cosre-guided-prefetch"
)

// guidedEscNever — hiç tırmanmayan rotalar: deterministik / netleştirme /
// yönlendirme cevapları (demetleri kanıt değil, cevap).
var guidedEscNever = map[guidedIntent]bool{
	guidedNone: true, guidedOffTopic: true, guidedSelfMeta: true, guidedHowTo: true,
	guidedOpenPage: true, guidedAskService: true, guidedFindEntity: true, guidedEvalStub: true,
}

// guidedEscAnswersCompare — demeti zaten bir KIYAS olan rotalar: pencere
// kıyası ve birden çok servisi aynı tabloda veren listeler. Kıyas sorusu
// bunlarda tırmanmaz (çok parçalı soru yine tırmanır).
var guidedEscAnswersCompare = map[guidedIntent]bool{
	guidedWindowCompare: true, guidedServiceSet: true, guidedTopErrorServices: true,
	guidedMyTopErrors: true, guidedTeamTopErrors: true, guidedTeamServices: true, guidedMyServices: true,
}

// guidedEscalatesToLoop — SAF: rota + işaret → demet döngüye tohumlanır mı.
// Yalnız çok parçalı ve kıyas işaretleri: sıralama / sayma / hacim işareti
// (v0.10.2472) yalnız problems / slow_traces'i veto eder, birebir cevap veren
// rotalar ("en çok hata veren 5 servis") anlatımda kalır.
func guidedEscalatesToLoop(intent guidedIntent, cue loopCueKind) bool {
	if guidedEscNever[intent] {
		return false
	}
	switch cue {
	case loopCueMulti:
		return true
	case loopCueCompare:
		return !guidedEscAnswersCompare[intent]
	}
	return false
}

// guidedEscalation — döngüye devredilen demet.
type guidedEscalation struct {
	Intent   guidedIntent
	Cue      loopCueKind
	ArgsJSON string
	Evidence string
}

// newGuidedEscalation — SAF: rota + demet → devir kaydı.
func newGuidedEscalation(route guidedRoute, cue loopCueKind, rangeS int64, evidence string) *guidedEscalation {
	args := map[string]any{"route": string(route.Intent)}
	if route.Service != "" {
		args["service"] = route.Service
	}
	if route.Env != "" {
		args["env"] = route.Env
	}
	if rangeS > 0 {
		args["rangeS"] = rangeS
	}
	b, _ := json.Marshal(args)
	return &guidedEscalation{Intent: route.Intent, Cue: cue, ArgsJSON: string(b), Evidence: evidence}
}

// seedScaled — serbest döngünün konuşmasına sentetik çağrı + sonuç (nil → aynen).
func (g *guidedEscalation) seedScaled(msgs []copilot.ChatMessage, scale int) []copilot.ChatMessage {
	if g == nil {
		return msgs
	}
	out := append([]copilot.ChatMessage{}, msgs...)
	ev, _ := clampToolResultScaled(g.Evidence, scale)
	return append(out,
		copilot.ChatMessage{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: guidedEscToolID, Name: guidedEscTool, Input: json.RawMessage(g.ArgsJSON)}}},
		copilot.ChatMessage{Role: "user", ToolResults: []copilot.ToolResult{{CallID: guidedEscToolID, Name: guidedEscTool, Content: ev}}},
	)
}

// seededTools — künyenin ön-getirme kaynağı (nil → yok).
func (g *guidedEscalation) seededTools() []string {
	if g == nil {
		return nil
	}
	return []string{guidedEscTool}
}

// summary — yineleme düşüşünün deterministik özeti için kanıt ("" = yok).
func (g *guidedEscalation) summary() string {
	if g == nil {
		return ""
	}
	return g.Evidence
}

// guidedEscStepLabelTR — SAF: görünür adım çipi.
func guidedEscStepLabelTR(cue loopCueKind) string {
	if cue == loopCueCompare {
		return "kıyas sorusu: hazır veri araç döngüsüne devredildi"
	}
	return "çok parçalı soru: hazır veri araç döngüsüne devredildi"
}

// guidedEscSlot — ana guided kademesinin devir yuvası.
type guidedEscSlot struct{ esc *guidedEscalation }

type guidedEscSlotKey struct{}

func withGuidedEscSlot(ctx context.Context) (context.Context, *guidedEscSlot) {
	sl := &guidedEscSlot{}
	return context.WithValue(ctx, guidedEscSlotKey{}, sl), sl
}

func guidedEscSlotFrom(ctx context.Context) *guidedEscSlot {
	sl, _ := ctx.Value(guidedEscSlotKey{}).(*guidedEscSlot)
	return sl
}

// get — yuvadaki devir (nil yuva → nil).
func (sl *guidedEscSlot) get() *guidedEscalation {
	if sl == nil {
		return nil
	}
	return sl.esc
}

// tryGuidedEscalate — runGuidedRoute'un anlatımdan önceki kapısı: yuva varsa ve
// karar tırman diyorsa devri yazar, adım çipini basar ve true döner.
func tryGuidedEscalate(ctx context.Context, emit func(string, any), route guidedRoute, question string, rangeS int64, evidence string) bool {
	sl := guidedEscSlotFrom(ctx)
	if sl == nil {
		return false
	}
	cue := chatLoopCue(question)
	if !guidedEscalatesToLoop(route.Intent, cue) {
		return false
	}
	sl.esc = newGuidedEscalation(route, cue, rangeS, evidence)
	emitGuidedContextStep(emit, guidedEscStepLabelTR(cue))
	return true
}
