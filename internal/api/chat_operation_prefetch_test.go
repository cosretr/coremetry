package api

// CoSRE operasyon tespiti Faz 2 — eşleyici (birebir / harf duyarsız /
// bulanık / çok / yok), özet ve sohbet tohumu.

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

var opCands = []chstore.CatalogOperation{
	{Operation: "KSK_MAIN_PIN_CHANGE", Source: "oracle", Service: "src1", FunctionCodes: []string{"SAMP01", "SAMP02"}, CallCount: 50},
	{Operation: "KSK_MAIN_PIN_CHANGE_V2", Source: "oracle", FunctionCodes: []string{"SAMP03"}, CallCount: 10},
	{Operation: "GET /orders", Source: "span", Service: "checkout", CallCount: 900},
	{Operation: "EXAMPLE_PAYMENT_XFR", Source: "span", Service: "pay", CallCount: 40},
	{Operation: "EXAMPLE_PAYMENT_XFR", Source: "oracle", FunctionCodes: []string{"SAMP05"}, CallCount: 5},
}

func TestOpTokens(t *testing.T) {
	got := opTokens("KSK_MAIN_PIN_CHANGE neden hata veriyor? x ab EXAMPLE_PAYMENT_XFR, KSK_MAIN_PIN_CHANGE")
	want := []string{"KSK_MAIN_PIN_CHANGE", "neden", "hata", "veriyor", "EXAMPLE_PAYMENT_XFR"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v", got)
	}
}

func TestMatchOperations(t *testing.T) {
	cases := []struct {
		msg  string
		kind string
		ops  []string
	}{
		{"KSK_MAIN_PIN_CHANGE neden hata veriyor", "exact", []string{"KSK_MAIN_PIN_CHANGE"}},
		{"ksk_main_pin_change durumu", "case_insensitive", []string{"KSK_MAIN_PIN_CHANGE"}},
		{"EXAMPLE_PAYMENT ne durumda", "fuzzy", []string{"EXAMPLE_PAYMENT_XFR"}},
		{"KSK_MAIN_PIN ne oldu", "fuzzy", []string{"KSK_MAIN_PIN_CHANGE", "KSK_MAIN_PIN_CHANGE_V2"}}, // çok → çip
		{"orders servisi yavaş mı", "", nil},                                                         // sıradan kelime bulanık tetiklemez
		{"bugün durum nasıl", "", nil},
	}
	for _, c := range cases {
		m := matchOperations(opTokens(c.msg), opCands)
		if m.Kind != c.kind || !reflect.DeepEqual(m.Ops, c.ops) && !(len(m.Ops) == 0 && len(c.ops) == 0) {
			t.Errorf("%q → %s %v, want %s %v", c.msg, m.Kind, m.Ops, c.kind, c.ops)
		}
	}
}

func TestOpTargetFor(t *testing.T) {
	tg := opTargetFor("EXAMPLE_PAYMENT_XFR", opCands)
	if !tg.HasSpan || !tg.HasOra || !reflect.DeepEqual(tg.Services, []string{"pay"}) || !reflect.DeepEqual(tg.Codes, []string{"SAMP05"}) {
		t.Fatalf("%+v", tg)
	}
	if h := opTracesHref(tg, "FUNCTION_CODE", time.Unix(0, 0), time.Unix(60, 0)); !strings.Contains(h, "op=EXAMPLE_PAYMENT_XFR") || !strings.Contains(h, "service=pay") {
		t.Errorf("span kaynağında ad + servis süzgeci: %s", h)
	}
	ora := opTargetFor("KSK_MAIN_PIN_CHANGE", opCands)
	if h := opTracesHref(ora, "FUNCTION_CODE", time.Unix(0, 0), time.Unix(60, 0)); !strings.Contains(h, "filters=%5B%7B%22k%22%3A%22FUNCTION_CODE%22") || !strings.Contains(h, "range=custom%3A0-60000") {
		t.Errorf("Oracle kaynağında fonksiyon kodu süzgeci: %s", h)
	}
}

func TestOpPrefetchSummaryAndSeed(t *testing.T) {
	p := &opPrefetch{Operation: "KSK_MAIN_PIN_CHANGE", MatchKind: "exact", Codes: []string{"SAMP01"}, RangeS: 3600,
		From: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC),
		Current: chstore.OpRED{Calls: 200, Errors: 20, P99Ms: 900, Measured: true}, Previous: chstore.OpRED{Calls: 100, Errors: 1, P99Ms: 300, Measured: true},
		Traces:   []opTraceRow{{TraceID: "t1", Service: "atm", DurationMs: 1200, Error: true}, {TraceID: "t2", Service: "atm", DurationMs: 10}},
		Oracle:   []chstore.OracleErrorCount{{Code: "MCA-00029", Count: 7, Sample: "PIN change rejected"}},
		argsJSON: `{"operation":"KSK_MAIN_PIN_CHANGE"}`}
	sum := p.summary()
	for _, want := range []string{"Operasyon KSK_MAIN_PIN_CHANGE otomatik getirildi", "Çağrı 200 (önceki eşit pencere 100, +100%)",
		"hata oranı %10.00 (önce %1.00)", "p99 900 ms (önce 300 ms, +200%)", "Trace: 2 (1 hatalı)", "- t1 · atm · 1200 ms", "MCA-00029 ×7: PIN change rejected"} {
		if !strings.Contains(sum, want) {
			t.Errorf("özet %q içermiyor:\n%s", want, sum)
		}
	}
	if strings.Contains(sum, "t2") {
		t.Error("başarılı trace kimlikleri özete girmez (ham liste değil)")
	}
	msgs := []copilot.ChatMessage{{Role: "user", Text: "KSK_MAIN_PIN_CHANGE?"}}
	seeded := p.seed(msgs)
	if len(seeded) != 3 || seeded[1].ToolCalls[0].Name != opPrefetchTool || seeded[2].ToolResults[0].CallID != opPrefetchToolID || len(msgs) != 1 {
		t.Fatalf("tohum: %+v", seeded)
	}
	var nilP *opPrefetch
	if got := nilP.seed(msgs); len(got) != 1 {
		t.Error("eşleşme yoksa mesajlar aynen")
	}
}

func TestMergeErrorsFirst(t *testing.T) {
	got := mergeErrorsFirst([][]chstore.TraceRow{
		{{TraceID: "a", DurationMs: 5}, {TraceID: "b", DurationMs: 50, HasError: true}},
		{{TraceID: "b", DurationMs: 50, HasError: true}, {TraceID: "c", DurationMs: 99}},
	}, 2)
	if len(got) != 2 || got[0].TraceID != "b" || got[1].TraceID != "c" {
		t.Fatalf("%+v", got)
	}
}
