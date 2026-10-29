package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1495 regresyon testi — operatör (prod): `runtime:jvm-gc:<svc>:<pod>`
// problemi "Where errors concentrate — top attribute over the error spans"
// gösterdi; problem hatayla ilgili değildi. Kıyas bölümü problem TÜRÜNDEN
// seçilir: errors (hata) / window (gecikme, trafik…) / none (runtime, db,
// monitor, self, servis olmayan özne — tarama yok). Servis adları sentetik.

func TestRootCauseBubbleModeByKind(t *testing.T) {
	// v0.10.1577 — tür tablosu chstore/bubbleup_mode_test.go'ya taşındı (karar
	// orada); burada sarmalayıcının aynı kararı aynı sabitlerle döndürdüğü.
	for _, p := range []*chstore.Problem{
		{ID: "r1", Service: "orders-api", Metric: "error_rate"},
		{ID: "r3", Service: "orders-api", Metric: "p99_ms"},
		{ID: "runtime:heap:core-api", Service: "core-api", Metric: "heap_used_pct"},
		nil,
	} {
		if got, want := rootCauseBubbleMode(p), string(chstore.ProblemBubbleMode(p)); got != want {
			t.Errorf("rootCauseBubbleMode(%+v) = %q, chstore %q", p, got, want)
		}
	}
	if bubbleModeErrors != "errors" || bubbleModeWindow != "window" || bubbleModeNone != "none" {
		t.Error("bubbleMode sabitleri istemci sözleşmesidir (errors/window/none)")
	}
	if anomalyBubbleMode("trace_op") != bubbleModeErrors || anomalyBubbleMode("log_pattern") != bubbleModeWindow ||
		anomalyBubbleMode("trace_op_latency") != bubbleModeWindow {
		t.Error("anomali çıpası: trace_op → errors, diğerleri → window")
	}
}

// TestRootCauseBubbleNoneSkipsScan — runtime problemi: /rootcause/bubbleup ve
// tam /rootcause ham spans kıyasını HİÇ koşmaz, cevap mode=none taşır;
// çekirdek bubbleMode=none. Hata problemi errors alt kümesiyle koşar.
func TestRootCauseBubbleNoneSkipsScan(t *testing.T) {
	start := time.Now().Add(-90 * time.Minute).UnixNano()
	st := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "runtime:jvm-gc:core-api:core-api-0", Service: "core-api", Pod: "core-api-0", Metric: "jvm_gc_pause_ratio", StartedAt: start},
		bubble:  sampleBubble(),
	}
	s := newRootCauseTestServer(t, st)
	w := callRootCause(s, (*Server).getProblemRootCauseBubbleUp, "/x", st.problem.ID, context.Background())
	if w.Code != 200 {
		t.Fatalf("code %d %s", w.Code, w.Body.String())
	}
	m := decodeMap(t, w.Body.Bytes())
	if m["mode"] != bubbleModeNone || m["bubbleUp"] != nil {
		t.Fatalf("bubbleup cevabı mode=none ve bubbleUp'sız olmalı: %v", m)
	}
	core := decodeMap(t, callRootCause(s, (*Server).getProblemRootCauseCore, "/x", st.problem.ID, context.Background()).Body.Bytes())
	if core["bubbleMode"] != bubbleModeNone {
		t.Fatalf("çekirdek bubbleMode = %v", core["bubbleMode"])
	}
	full := decodeMap(t, callRootCause(s, (*Server).getProblemRootCause, "/x", st.problem.ID, context.Background()).Body.Bytes())
	if full["bubbleUp"] != nil || full["bubbleMode"] != bubbleModeNone {
		t.Fatalf("tam demet runtime'da kıyas taşımamalı: %v", full["bubbleUp"])
	}
	if n := atomic.LoadInt32(&st.bubbleCalls); n != 0 {
		t.Fatalf("runtime problemi ham spans kıyası koştu: %d çağrı", n)
	}

	st2 := &fakeRootCauseStore{
		problem: &chstore.Problem{ID: "p-err", Service: "orders-api", Metric: "error_rate", StartedAt: start},
		bubble:  sampleBubble(),
	}
	s2 := newRootCauseTestServer(t, st2)
	m2 := decodeMap(t, callRootCause(s2, (*Server).getProblemRootCauseBubbleUp, "/x", "p-err", context.Background()).Body.Bytes())
	if m2["mode"] != bubbleModeErrors || m2["bubbleUp"] == nil {
		t.Fatalf("hata problemi: mode=errors + bubbleUp beklenir: %v", m2)
	}
	if st2.bubbleArgs[1] != true {
		t.Fatalf("hata problemi hatalı alt kümeyle koşmalı: %v", st2.bubbleArgs)
	}
}
