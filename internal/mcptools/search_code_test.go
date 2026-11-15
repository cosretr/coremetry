package mcptools

import (
	"context"
	"encoding/json"
	"testing"
)

// v0.10.1208 — search_code: depo modelden ALINMAZ, sınır ve yokluk.
func TestSearchCodeTool(t *testing.T) {
	var gotSvc, gotQ string
	var gotLim int
	tl := searchCodeTool(Deps{CodeSearch: func(_ context.Context, svc, q string, lim int) (CodeSearchResult, error) {
		gotSvc, gotQ, gotLim = svc, q, lim
		return CodeSearchResult{Outcome: "ok"}, nil
	}})
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"service":" acm-kiosk ","query":"MCA-00029","limit":50,"repo":"evil"}`)); err != nil {
		t.Fatal(err)
	}
	if gotSvc != "acm-kiosk" || gotQ != "MCA-00029" || gotLim != searchCodeMax {
		t.Errorf("%q %q %d", gotSvc, gotQ, gotLim)
	}
	if _, err := tl.Handler(context.Background(), json.RawMessage(`{"service":"a"}`)); err == nil {
		t.Error("query zorunlu")
	}
	out, _ := searchCodeTool(Deps{}).Handler(context.Background(), json.RawMessage(`{"service":"a","query":"b"}`))
	if r, ok := out.(CodeSearchResult); !ok || r.Outcome != "error" {
		t.Errorf("yapılandırılmamış: %+v", out)
	}
	if chatOffered(Deps{}, SearchCodeToolName) || !chatOnlyTools[SearchCodeToolName] {
		t.Error("koşullu + yalnız sohbet")
	}
}

// v0.10.1509 — kind kipi (def/ref/class): sorgu devops.BuildCodeQuery ile
// kurulur; geçersiz tanımlayıcı istek ATMAZ; kind yok / text → sorgu aynen.
func TestSearchCodeToolKind(t *testing.T) {
	var gotQ []string
	tl := searchCodeTool(Deps{CodeSearch: func(_ context.Context, _, q string, _ int) (CodeSearchResult, error) {
		gotQ = append(gotQ, q)
		return CodeSearchResult{Outcome: "ok"}, nil
	}})
	cases := []struct {
		args, wantQ, wantOutcome string
	}{
		{`{"service":"orders-api","query":"approve","kind":"ref"}`, "ref:approve", "ok"},
		{`{"service":"orders-api","query":"approve","kind":"DEF"}`, "def:approve", "ok"},
		{`{"service":"orders-api","query":"OrderService","kind":"class"}`, "class:OrderService", "ok"},
		{`{"service":"orders-api","query":"a b repo:x","kind":"text"}`, "a b repo:x", "ok"},
		{`{"service":"orders-api","query":"\"x\" ext:java"}`, `"x" ext:java`, "ok"},
		{`{"service":"orders-api","query":"approve repo:other","kind":"ref"}`, "", "error"},
		{`{"service":"orders-api","query":"com.x.Y","kind":"class"}`, "", "error"},
		{`{"service":"orders-api","query":"x","kind":"repo"}`, "", "error"},
	}
	for _, c := range cases {
		gotQ = nil
		out, err := tl.Handler(context.Background(), json.RawMessage(c.args))
		r, _ := out.(CodeSearchResult)
		if err != nil || r.Outcome != c.wantOutcome {
			t.Errorf("%s: %+v %v", c.args, out, err)
			continue
		}
		if c.wantQ == "" {
			if len(gotQ) != 0 || r.Hint == "" {
				t.Errorf("%s: istek atılmamalı + ipucu: %v %q", c.args, gotQ, r.Hint)
			}
			continue
		}
		if len(gotQ) != 1 || gotQ[0] != c.wantQ {
			t.Errorf("%s: sorgu %v, want %q", c.args, gotQ, c.wantQ)
		}
	}
	props := tl.InputSchema["properties"].(map[string]any)
	if k, ok := props["kind"].(map[string]any); !ok || len(k["enum"].([]string)) != 4 {
		t.Errorf("şema kind enum: %+v", props["kind"])
	}
}
