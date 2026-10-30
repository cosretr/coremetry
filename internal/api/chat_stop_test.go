package api

// chat_stop_test.go — CoSRE agent-core S1 (docs/cosre/agent-core-ideas.md §2.3).
// Sözleşme: döngünün durma kararı adlandırılmış koşullara indi, KARAR AYNI.
// defaultStop, eski iki dalın (`cancelled || ctx.Err() != nil` ve
// `round == maxRounds-1 || callsLeft <= 0`) erişilebilir tüm ızgarada aynısı.

import (
	"context"
	"testing"
)

// oldLoopDecision — S1 öncesi copilot_chat.go'nun kararı, birebir.
func oldLoopDecision(round, maxRounds, callsLeft int, cancelled bool, ctxErr error) (cancel, capTurn bool) {
	if cancelled || ctxErr != nil {
		return true, false
	}
	return false, round == maxRounds-1 || callsLeft <= 0
}

func TestDefaultStopMatchesOldLoopDecision(t *testing.T) {
	for _, maxRounds := range []int{1, 2, 5, 7, 8} {
		for _, maxCalls := range []int{1, 6, 9, 12} {
			stop := defaultStop(maxRounds)
			for round := 0; round < maxRounds; round++ {
				for callsLeft := -2; callsLeft <= maxCalls; callsLeft++ {
					for _, cancelled := range []bool{false, true} {
						for _, ctxErr := range []error{nil, context.Canceled, context.DeadlineExceeded} {
							r := stop(loopState{Round: round, MaxRounds: maxRounds, CallsLeft: callsLeft, MaxCalls: maxCalls, Cancelled: cancelled, CtxErr: ctxErr})
							wantCancel, wantCap := oldLoopDecision(round, maxRounds, callsLeft, cancelled, ctxErr)
							if (r == stopCancelled) != wantCancel || isCapStop(r) != wantCap {
								t.Fatalf("rounds=%d calls=%d round=%d left=%d cancelled=%v err=%v: reason=%q, eski cancel=%v cap=%v",
									maxRounds, maxCalls, round, callsLeft, cancelled, ctxErr, r, wantCancel, wantCap)
							}
						}
					}
				}
			}
		}
	}
}

func TestStopConditions(t *testing.T) {
	cases := []struct {
		name string
		cond stopCond
		st   loopState
		want stopReason
	}{
		{"ctx: temiz", stopOnCtxDone(), loopState{}, stopNone},
		{"ctx: executor iptali", stopOnCtxDone(), loopState{Cancelled: true}, stopCancelled},
		{"ctx: deadline", stopOnCtxDone(), loopState{CtxErr: context.DeadlineExceeded}, stopCancelled},
		{"round: ilk tur", stopAtRound(5), loopState{Round: 0}, stopNone},
		{"round: sondan bir önce", stopAtRound(5), loopState{Round: 3}, stopNone},
		{"round: son tur", stopAtRound(5), loopState{Round: 4}, stopRoundCap},
		{"round: tek turluk tavan", stopAtRound(1), loopState{Round: 0}, stopRoundCap},
		{"calls: hak var", stopWhenCallsExhausted(), loopState{CallsLeft: 1}, stopNone},
		{"calls: sıfır", stopWhenCallsExhausted(), loopState{CallsLeft: 0}, stopCallCap},
		{"calls: negatif", stopWhenCallsExhausted(), loopState{CallsLeft: -1}, stopCallCap},
		{"tool: çağrılmadı", stopOnToolCall("render_chart"), loopState{LastCalls: []string{"list_services"}}, stopNone},
		{"tool: çağrıldı", stopOnToolCall("render_chart"), loopState{LastCalls: []string{"list_services", "render_chart"}}, "tool_called:render_chart"},
		{"tool: boş tur", stopOnToolCall("x"), loopState{}, stopNone},
		{"tokens: altında", stopAtTokens(1000), loopState{TokensIn: 600, TokensOut: 399}, stopNone},
		{"tokens: tam sınır", stopAtTokens(1000), loopState{TokensIn: 600, TokensOut: 400}, stopTokenBudget},
		{"tokens: uint32 taşmaz", stopAtTokens(1 << 33), loopState{TokensIn: ^uint32(0), TokensOut: ^uint32(0)}, stopNone},
		{"tokens: 0 kapalı", stopAtTokens(0), loopState{TokensIn: 1 << 30}, stopNone},
	}
	for _, c := range cases {
		if got := c.cond(c.st); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStopAnyOrderAndNil(t *testing.T) {
	both := loopState{Round: 4, CallsLeft: 0, Cancelled: true}
	if got := stopAny(stopAtRound(5), stopWhenCallsExhausted())(both); got != stopRoundCap {
		t.Errorf("ilk koşul kazanmalı: %q", got)
	}
	if got := stopAny(stopWhenCallsExhausted(), stopAtRound(5))(both); got != stopCallCap {
		t.Errorf("sıra çevrilince ilk koşul: %q", got)
	}
	if got := defaultStop(5)(both); got != stopCancelled {
		t.Errorf("iptal her tavanı geçer (model yeniden çağrılmaz): %q", got)
	}
	if got := stopAny()(both); got != stopNone {
		t.Errorf("boş birleştirici durdurmaz: %q", got)
	}
	if got := stopAny(nil, stopWhenCallsExhausted())(both); got != stopCallCap {
		t.Errorf("nil koşul atlanır: %q", got)
	}
}

func TestIsCapStop(t *testing.T) {
	for r, want := range map[stopReason]bool{
		stopNone: false, stopCancelled: false, stopRoundCap: true, stopCallCap: true,
		stopTokenBudget: true, "tool_called:x": true, stopApprovalPending: false,
	} {
		if isCapStop(r) != want {
			t.Errorf("isCapStop(%q) = %v", r, !want)
		}
	}
}
