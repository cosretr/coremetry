package api

// chat_loop_shape_test.go — CoSRE ölçüm: döngü künyesi. Sözleşme: tap
// "stop" olayını yakalar ve olayı değiştirmeden iletir; katalog boyu + şema
// baytı kaydedilir; taşma retried → failed yükselir; döngü koşmadıysa
// loop_stop sinyali YOK; sinyal sistem aktörlü ve doğrulamadan geçer.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func TestLoopStopSignalFromExchange(t *testing.T) {
	x := newChatExchange("xid-1", "checkout neden yavaş", false, time.Now())
	if sigs := loopStopSignals(x); sigs != nil {
		t.Fatalf("döngü koşmadı → sinyal yok: %+v", sigs)
	}
	specs := []copilot.ToolSpec{
		{Name: "search_logs", Description: "log ara", InputSchema: map[string]any{"type": "object"}},
		{Name: "get_service", Description: "servis", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}},
	}
	x.setToolCatalog(specs)
	var forwarded []string
	emit := x.tap(func(kind string, v any) { forwarded = append(forwarded, kind) })
	emit(chatStopEventKind, chatStopEvent(stopRoundCap, 6, 5, time.Second))
	x.noteOverflow(false)
	x.noteOverflow(true)
	x.noteOverflow(false) // son durum "failed" kalır
	if len(forwarded) != 1 || forwarded[0] != chatStopEventKind {
		t.Fatalf("olay iletilmeli: %v", forwarded)
	}
	sigs := loopStopSignals(x)
	if len(sigs) != 1 {
		t.Fatalf("tek sinyal: %+v", sigs)
	}
	g, err := validateSystemSignal(sigs[0])
	if err != nil || g.ExchangeID != "xid-1" || g.Kind != cosrelearn.SigLoopStop {
		t.Fatalf("sinyal: %+v err=%v", g, err)
	}
	l := cosrelearn.ParseLoopStopDetail(g.Detail)
	if l.Stop != "round_cap" || l.Rounds != 5 || l.Calls != 6 || l.Tools != 2 || l.SchemaBytes != toolCatalogBytes(specs) ||
		l.SchemaBytes == 0 || l.Overflow != cosrelearn.OverflowFailed {
		t.Fatalf("künye: %q", g.Detail)
	}
	if got := cosrelearn.LoopStopReasons(g.Detail); strings.Join(got, ",") != "round_cap,context_overflow" {
		t.Fatalf("gerekçeler: %v", got)
	}
}
