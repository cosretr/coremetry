package api

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// answer_verify_flow_test.go — v0.10.2327 (E1): GERÇEK v2 handler'ı + sahte
// model + sahte araç. Pinler: (1) gölge kip akışı bayt bayt "off" ile aynı;
// (2) uyuşmazlık turunda TEK verify_mismatch sinyali (sistem aktörü, cevap
// metni yok) ve cosre_interactions.verify sayaçları; (3) kanıt modele giden
// kırpık kopya değil TAM çıktı.

func setVerification(t *testing.T, mode string) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.Verification = mode
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

type verifyCapture struct {
	mu   sync.Mutex
	rows []chstore.CosreInteraction
	sigs []chstore.CosreSignal
}

func captureVerify(t *testing.T) *verifyCapture {
	t.Helper()
	vc := &verifyCapture{}
	prev := cosreInteractionSeam
	cosreInteractionSeam = func(r chstore.CosreInteraction) {
		vc.mu.Lock()
		vc.rows = append(vc.rows, r)
		vc.mu.Unlock()
	}
	setSystemSignalWriter(func(_ context.Context, g chstore.CosreSignal) error {
		vc.mu.Lock()
		vc.sigs = append(vc.sigs, g)
		vc.mu.Unlock()
		return nil
	})
	t.Cleanup(func() { cosreInteractionSeam = prev; setSystemSignalWriter(nil) })
	return vc
}

func (vc *verifyCapture) waitSignals(n int, wait time.Duration) []chstore.CosreSignal {
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		vc.mu.Lock()
		got := len(vc.sigs)
		vc.mu.Unlock()
		if got >= n {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	vc.mu.Lock()
	defer vc.mu.Unlock()
	return append([]chstore.CosreSignal(nil), vc.sigs...)
}

const verifyFlowAnswer = "checkout p95 1.830 ms; payments tarafında p95 2.950 ms."

func verifyFlowLoop(_ string, n int) map[string]any {
	if n > 0 {
		return answerMsg(verifyFlowAnswer)
	}
	return toolCallMsg([2]string{"compare_periods", `{"service":"checkout"}`})
}

// verifyFlowVolatile — iki koşuda doğal olarak farklı olanlar: rastgele
// alışveriş / çağrı kimliği ve ölçülen süreler. Geri kalan her bayt aynı olmalı.
var verifyFlowVolatile = regexp.MustCompile(`[0-9a-f]{24,32}|"(?:elapsed_ms|durationMs|duration_ms)":\d+`)

func runVerifyFlow(t *testing.T, mode string) (string, *verifyCapture) {
	t.Helper()
	setVerification(t, mode)
	vc := captureVerify(t)
	rg := newStateRig(t, verifyFlowLoop, nil)
	body := rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	return verifyFlowVolatile.ReplaceAllString(body, "X"), vc
}

func TestVerifyShadowStreamByteIdentical(t *testing.T) {
	var off, shadow string
	t.Run("off", func(t *testing.T) {
		var vc *verifyCapture
		off, vc = runVerifyFlow(t, chstore.CosreVerifyOff)
		if len(vc.rows) != 1 || vc.rows[0].Verify != "" {
			t.Fatalf("off: verify kolonu boş olmalı: %+v", vc.rows)
		}
		if s := vc.waitSignals(1, 200*time.Millisecond); len(s) != 0 {
			t.Fatalf("off: sinyal yazılmamalı: %+v", s)
		}
	})
	t.Run("shadow", func(t *testing.T) {
		var vc *verifyCapture
		shadow, vc = runVerifyFlow(t, chstore.CosreVerifyShadow)
		if len(vc.rows) != 1 {
			t.Fatalf("satır: %+v", vc.rows)
		}
		if got := vc.rows[0].Verify; got != "v1 tier=loop duration=2/1/0/1 name=2/1/0/1" {
			t.Fatalf("verify kolonu: %q", got)
		}
		sigs := vc.waitSignals(1, 2*time.Second)
		if len(sigs) != 1 {
			t.Fatalf("tek sinyal bekleniyordu: %+v", sigs)
		}
		g := sigs[0]
		if g.Kind != cosrelearn.SigVerifyMismatch || g.UserEmail != cosrelearn.SystemSignalActor || g.ExchangeID == "" {
			t.Fatalf("sinyal: %+v", g)
		}
		if g.Detail != "tier=loop kinds=duration:1,name:1 tokens=payments|2.950 ms" {
			t.Fatalf("ayrıntı: %q", g.Detail)
		}
		if strings.Contains(g.Detail, "1.830") {
			t.Fatal("ayrıntı cevap metni taşımamalı")
		}
	})
	if off == "" || off != shadow {
		t.Fatalf("gölge kip akışı değiştirdi:\n--- off\n%s\n--- shadow\n%s", off, shadow)
	}
	if !strings.Contains(off, "2.950 ms") {
		t.Fatal("cevap akışta yok — test bir şey ölçmüyor")
	}
}

func TestVerifyNonV2Inert(t *testing.T) {
	setVerification(t, chstore.CosreVerifyShadow)
	if v := (&Server{}).newAnswerVerifyTurn(context.Background(), &chatRequest{}); v != nil {
		t.Fatal("v1 (işaretsiz ctx) toplayıcı kurmamalı")
	}
	var v *answerVerifyTurn // nil-güvenli
	v.addText("x")
	v.addTool("t", nil, "{}")
	v.setAnswer(verifyTierLoop, "1.830 ms")
	if _, ok := v.finish(); ok {
		t.Fatal("nil toplayıcı sonuç üretmemeli")
	}
}

func TestVerifyClientEchoNeverEvidence(t *testing.T) {
	v := &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout", "payments"}}
	st := &chatContextState{ctx: ChatContext{Service: "payments", Ledger: []chatFact{{Name: "p95_ms", Value: 2950}}}, fromClient: true}
	v.noteState(st)
	v.addLoopContext(st, nil, "KONUŞMA DURUMU payments p95_ms=2950 ms")
	v.setAnswer(verifyTierLoop, "payments p95 2.950 ms.")
	o, ok := v.finish()
	if !ok || o.Result.compact() != "v1 tier=loop duration=1/0/0/1 name=1/0/0/1" {
		t.Fatalf("istemci yankısı kanıt sayıldı: %+v", o)
	}
	st.fromClient = false
	v2 := &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout", "payments"}}
	v2.noteState(st)
	v2.setAnswer(verifyTierLoop, "payments p95 2.950 ms.")
	if o, _ := v2.finish(); o.Result.compact() != "v1 tier=loop duration=1/1/0/0 name=1/1/0/0" {
		t.Fatalf("sunucu durumu kanıt sayılmadı: %+v", o)
	}
}

func TestVerifyGuidedNarrationFactsAndLedger(t *testing.T) {
	x := &chatExchange{verify: &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout", "payments"}}}
	stt := &chatStateTurn{st: &chatContextState{}}
	ctx := withChatStateTurn(withChatExchange(context.Background(), x), stt)
	facts := guidedSummaryFacts{Scope: "checkout", REDKnown: true, ErrorRatePct: 2.4, P99Ms: 812}
	to := time.Date(2026, 11, 18, 11, 30, 0, 0, time.UTC)
	noteGuidedNarration(ctx, "service_health", "checkout hata oranı %2,4, p99 812 ms; 14:30'a kadar.",
		"KANIT: istek 74167", "", facts, to.Add(-time.Hour), to, 3600)
	if len(stt.facts) != 2 || stt.facts[0].Name != "error_rate_pct" || stt.facts[1].Value != 812 {
		t.Fatalf("defter: %+v", stt.facts)
	}
	x.verify.loc = istanbul(t)
	o, ok := x.verify.finish()
	if !ok || o.Result.Tier != verifyTierGuided {
		t.Fatalf("guided sonucu yok: %+v", o)
	}
	_, by := o.Result.totals()
	if by[verifyKindPercent].Verified != 1 || by[verifyKindDuration].Verified != 1 || by[verifyKindTime].Verified != 1 || by[verifyKindName].Verified != 1 {
		t.Fatalf("guided olgu/pencere kanıtı: %+v", o.Result.Claims)
	}
}

func TestVerifyGeneralTierHook(t *testing.T) {
	x := &chatExchange{verify: &answerVerifyTurn{loc: time.UTC, catalog: []string{"checkout"}}}
	ctx := withChatExchange(context.Background(), x)
	x.verify.addText("kubernetes readiness nedir?")
	verifyTurnFrom(ctx).setAnswer(verifyTierGeneral, "Readiness probe başarısız olursa pod trafik almaz; HTTP 503 döner.")
	o, ok := x.verify.finish()
	if !ok || len(o.Result.Claims) != 0 || o.Compact != "v1 tier=general" {
		t.Fatalf("genel bilgi cevabı iddia üretmemeli: %+v", o)
	}
}
