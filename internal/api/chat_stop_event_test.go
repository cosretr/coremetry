package api

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// chat_stop_event_test.go — CoSRE v2 araç seti dilim 1 (tools-audit §10 G2):
// data-stop döngünün HER çıkışında; v1 akışı `stop` türünü hiç görmez.

func TestCtxExitReason(t *testing.T) {
	cases := []struct {
		err      error
		fallback stopReason
		want     stopReason
	}{
		{context.DeadlineExceeded, stopModelError, stopDeadline},
		{fmt.Errorf("wrap: %w", context.DeadlineExceeded), stopCancelled, stopDeadline},
		{context.Canceled, stopModelError, stopCancelled},
		{nil, stopModelError, stopModelError},
		{nil, stopRoundCap, stopRoundCap},
		{errors.New("provider 500"), stopCallCap, stopCallCap},
	}
	for _, c := range cases {
		if got := ctxExitReason(c.err, c.fallback); got != c.want {
			t.Errorf("ctxExitReason(%v, %s) = %s, want %s", c.err, c.fallback, got, c.want)
		}
	}
}

func TestChatStopEventShape(t *testing.T) {
	got := chatStopEvent(stopCallCap, 6, 3, 2140*time.Millisecond)
	want := map[string]any{"reason": "call_cap", "calls": 6, "rounds": 3, "elapsed_ms": int64(2140)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("%v != %v", got, want)
	}
}

// Döngünün ürettiği her neden uistream.StopReasons kapalı kümesinde (FE
// protocol.ts STOP_REASONS aynı küme; vitest pinler).
func TestStopReasonsCoverLoopExits(t *testing.T) {
	for _, r := range []stopReason{stopAnswered, stopRoundCap, stopCallCap, stopCancelled, stopDeadline, stopApprovalPending, stopModelError, stopTokenBudget} {
		if !slices.Contains(uistream.StopReasons, string(r)) {
			t.Errorf("%s uistream.StopReasons'ta yok", r)
		}
	}
}

// Her `break` bir çıkış nedeni yazar ve `stop` olayı `done`dan hemen önce,
// tek yerde (kaynak pini: davranış golden'larla, burası sayım).
func TestLoopEveryBreakSetsExitReason(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "for round := 0; round < maxRounds; round++ {")
	j := strings.Index(src, `emit("stop", chatStopEvent(`)
	if i < 0 || j < i {
		t.Fatal("döngü ya da stop olayı bulunamadı")
	}
	loop := src[i:j]
	lines := strings.Split(loop, "\n")
	for k, l := range lines {
		if strings.TrimSpace(l) != "break" && !strings.HasPrefix(strings.TrimSpace(l), "break //") {
			continue
		}
		prev := strings.Join(lines[max(0, k-4):k], "\n")
		if !strings.Contains(prev, "exitReason") {
			t.Errorf("break (döngü satırı %d) exitReason yazmıyor:\n%s", k, prev)
		}
	}
	if n := strings.Count(src, `emit("stop", `); n != 1 {
		t.Errorf(`emit("stop" %d kez, tam 1 bekleniyor`, n)
	}
	if !strings.Contains(src[j:], `emit("done", map[string]bool{"ok": lastErr == nil})`) {
		t.Error("stop olayı done'dan önce değil")
	}
}

// v1 yazıcısı `stop` türünü yazmaz (v1 tel sözleşmesi bayt bayt aynı).
func TestV1SinkDropsV2OnlyKinds(t *testing.T) {
	rec := httptest.NewRecorder()
	sink, ok := v1ChatSink(rec)
	if !ok {
		t.Fatal("v1ChatSink")
	}
	sink.Emit("stop", chatStopEvent(stopAnswered, 1, 1, time.Second))
	sink.Emit("done", map[string]bool{"ok": true})
	sink.Close()
	body := rec.Body.String()
	if strings.Contains(body, "elapsed_ms") || strings.Contains(body, "event: stop") {
		t.Fatalf("v1 akışı stop taşıyor:\n%s", body)
	}
	if !strings.Contains(body, "event: done") {
		t.Fatalf("v1 done düştü:\n%s", body)
	}
}
