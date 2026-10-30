package api

// chat_prepare_step_test.go — CoSRE agent-core S3 (docs/cosre/agent-core-ideas.md §3.3).
// Sözleşme: defaultPrepare bugünkü üç turun ayarını AYNEN üretir (kimlik
// planı); plan yalnız daraltır — araç ekleyemez, çağrılmış aracı düşüremez.

import (
	"context"
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestDefaultPrepareIdentity(t *testing.T) {
	specs := []copilot.ToolSpec{{Name: "a"}, {Name: "b"}}
	cases := []struct {
		name      string
		kind      stepKind
		addendum  string
		noTools   bool
		maxTokCap int
	}{
		{"normal", stepNormal, "", false, 0},
		{"tavan turu", stepCap, copilot.ChatRoundCapAddendum(), true, 0},
		{"yineleme düşüşü", stepDegenRetry, copilot.ChatDegenRetryAddendum(), true, chatDegenRetryMaxTokens},
	}
	for _, c := range cases {
		in := stepInput{Kind: c.kind, Specs: specs}
		p := defaultPrepare(in)
		if p.SystemAddendum != c.addendum {
			t.Errorf("%s: ek %q, want %q", c.name, p.SystemAddendum, c.addendum)
		}
		ctx := p.planCtx(context.Background())
		if copilot.NoToolCallsSet(ctx) != c.noTools {
			t.Errorf("%s: NoToolCalls=%v, want %v", c.name, !c.noTools, c.noTools)
		}
		if got := copilot.MaxTokensCapOf(ctx); got != c.maxTokCap {
			t.Errorf("%s: MaxTokensCap=%d, want %d", c.name, got, c.maxTokCap)
		}
		if p.Specs != nil {
			t.Errorf("%s: varsayılan plan spec'e dokunmamalı", c.name)
		}
		if got := p.planSpecs(in); !reflect.DeepEqual(got, specs) {
			t.Errorf("%s: spec listesi değişti: %v", c.name, got)
		}
	}
	// normal tur ctx'i aynen geçer (yeni değer sarılmaz).
	bg := context.Background()
	if defaultPrepare(stepInput{Kind: stepNormal}).planCtx(bg) != bg {
		t.Error("normal tur ctx'i sarmamalı")
	}
}

func TestPrepareChainOrder(t *testing.T) {
	a := func(_ stepInput, p stepPlan) stepPlan { p.SystemAddendum += "A"; return p }
	b := func(_ stepInput, p stepPlan) stepPlan { p.SystemAddendum += "B"; return p }
	if got := prepareChain(a, nil, b)(stepInput{}).SystemAddendum; got != "AB" {
		t.Errorf("sıra: %q", got)
	}
	if got := prepareChain()(stepInput{}); got.SystemAddendum != "" || got.Ctx != nil || got.Specs != nil {
		t.Errorf("boş zincir boş plan: %+v", got)
	}
}

func TestNarrowSpecs(t *testing.T) {
	full := []copilot.ToolSpec{{Name: "list_services"}, {Name: "search_logs"}, {Name: "get_trace"}, {Name: "render_chart"}}
	called := []copilot.ChatMessage{{Role: "assistant", ToolCalls: []copilot.ToolCall{{Name: "get_trace"}}}}
	names := func(ss []copilot.ToolSpec) []string {
		out := []string{}
		for _, s := range ss {
			out = append(out, s.Name)
		}
		return out
	}
	cases := []struct {
		name   string
		subset []copilot.ToolSpec
		conv   []copilot.ChatMessage
		want   []string
	}{
		{"alt küme, boş konuşma", []copilot.ToolSpec{{Name: "search_logs"}}, nil, []string{"search_logs"}},
		{"çağrılmış araç düşmez", []copilot.ToolSpec{{Name: "list_services"}}, called, []string{"list_services", "get_trace"}},
		{"çağrılmamış düşer", []copilot.ToolSpec{}, nil, []string{}},
		{"araç EKLENEMEZ (full dışı)", []copilot.ToolSpec{{Name: "delete_everything"}, {Name: "render_chart"}}, nil, []string{"render_chart"}},
		{"çağrılmış ama full dışı eklenmez", []copilot.ToolSpec{}, []copilot.ChatMessage{{ToolCalls: []copilot.ToolCall{{Name: "ghost"}}}}, []string{}},
		{"full sırası korunur", []copilot.ToolSpec{{Name: "render_chart"}, {Name: "list_services"}}, nil, []string{"list_services", "render_chart"}},
	}
	for _, c := range cases {
		if got := names(narrowSpecs(full, c.subset, c.conv)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// plan.Specs nil-olmayan → planSpecs narrowSpecs'ten geçer.
	in := stepInput{Specs: full, Conv: called}
	p := stepPlan{Specs: []copilot.ToolSpec{{Name: "search_logs"}, {Name: "x"}}}
	if got := names(p.planSpecs(in)); !reflect.DeepEqual(got, []string{"search_logs", "get_trace"}) {
		t.Errorf("planSpecs: %v", got)
	}
}
