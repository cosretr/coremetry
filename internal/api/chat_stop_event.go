package api

// chat_stop_event.go — CoSRE v2 araç seti dilim 1 (docs/cosre/tools-audit.md
// §10 G2): "neden durdum" döngünün HER çıkışında. Eskiden neden yalnız tavan
// turunun bütçe adımında (chatBudget.StopReason) vardı; normal cevap, iptal,
// alışveriş tavanı ve model hatası nedensiz bitiyordu.
//
// Tel: v1 olay türü `stop` {reason, calls, rounds, elapsed_ms} → uistream
// `data-stop`. v1 yazıcısı bu türü DÜŞÜRÜR (v1OnlySink): v1 akışı bayt bayt
// aynı (chat_stream_golden_test.go).

import (
	"context"
	"errors"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
)

// Döngü çıkış nedenleri — chat_stop.go'nun durma koşullarına ek olarak
// döngünün kendi sonları.
const (
	// stopAnswered — model araçsız cevap verdi (AI SDK finishReason "stop").
	stopAnswered stopReason = "stop"
	// stopDeadline — alışveriş tavanı (chat_deadline.go) doldu.
	stopDeadline stopReason = "deadline"
	// stopModelError — sağlayıcı/model hatası (tavan ya da iptal değil).
	stopModelError stopReason = "error"
)

// chatStopEventKind — v1 olay türü; yalnız v2 yazıcısına ulaşır.
const chatStopEventKind = "stop"

// chatStopEvent — `stop` olayının yükü (SAF). calls = harcanan çağrı hakkı
// (bütçenin birimi; data-budget slotsUsed ile aynı), rounds = biten tur.
func chatStopEvent(reason stopReason, calls, rounds int, elapsed time.Duration) map[string]any {
	return map[string]any{
		"reason": string(reason), "calls": calls, "rounds": rounds,
		"elapsed_ms": elapsed.Milliseconds(),
	}
}

// ctxExitReason — biten ctx'in nedeni (SAF): tavan → deadline, iptal →
// cancelled; ctx canlıysa fallback.
func ctxExitReason(err error, fallback stopReason) stopReason {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return stopDeadline
	case errors.Is(err, context.Canceled):
		return stopCancelled
	}
	return fallback
}

// v2OnlyEventKinds — v1 tel sözleşmesinde olmayan türler; v1 yazıcısı düşürür.
var v2OnlyEventKinds = map[string]bool{chatStopEventKind: true, chatScopeEventKind: true} // dilim 2: scope

// v1OnlySink — v1 yazıcısının süzgeci: v2'ye özgü türleri yazmaz.
type v1OnlySink struct{ em *blocks.Emitter }

func (s v1OnlySink) Emit(kind string, payload any) {
	if v2OnlyEventKinds[kind] {
		return
	}
	s.em.Emit(kind, payload)
}

func (s v1OnlySink) Close() { s.em.Close() }
