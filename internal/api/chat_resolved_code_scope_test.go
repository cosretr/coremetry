package api

// v0.10.1209 — genel sohbette read_source_code kapsamı: yalnız aynı turda
// search_code / resolve_business_code sonucunun döndürdüğü servisler.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestScopeServicesFrom(t *testing.T) {
	args := json.RawMessage(`{"service":"acm-kiosk","query":"x"}`)
	cases := []struct {
		name string
		tool string
		res  any
		want int
	}{
		{"search ok", mcptools.SearchCodeToolName, mcptools.CodeSearchResult{Outcome: "ok"}, 1},
		{"search no_hits (depo çözüldü)", mcptools.SearchCodeToolName, mcptools.CodeSearchResult{Outcome: "no_hits"}, 1},
		{"search no_repo", mcptools.SearchCodeToolName, mcptools.CodeSearchResult{Outcome: "no_repo"}, 0},
		{"biz adayları", "resolve_business_code", map[string]any{"candidates": []mcptools.BizCandidate{
			{Services: []mcptools.BizServiceStat{{Service: "a"}, {Service: "b"}}}}}, 2},
		{"başka araç", "list_services", map[string]any{}, 0},
	}
	for _, c := range cases {
		if got := scopeServicesFrom(c.tool, args, c.res); len(got) != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestResolvedCodeScopeWrap(t *testing.T) {
	ctx := withResolvedCodeScope(context.Background())
	rs := resolvedCodeScopeFrom(ctx)
	h := rs.wrap(mcptools.SearchCodeToolName, func(context.Context, json.RawMessage) (any, error) {
		return mcptools.CodeSearchResult{Outcome: "ok"}, nil
	})
	if _, err := h(ctx, json.RawMessage(`{"service":"acm-kiosk"}`)); err != nil {
		t.Fatal(err)
	}
	if !rs.has("acm-kiosk") || rs.has("other") {
		t.Error("yalnız araç sonucunun servisi kapsama girer")
	}
	if resolvedCodeScopeFrom(context.Background()) != nil {
		t.Error("kapsamsız ctx nil")
	}
}

func TestSourceCodeToolsForResolvedCode(t *testing.T) {
	tools := []mcp.Tool{{Name: mcptools.SourceCodeToolName}, {Name: "x"}}
	user := &auth.Claims{UserID: "u1"}
	token := &auth.Claims{UserID: "token:1"}
	if !hasTool(sourceCodeToolsFor(tools, false, user, true), mcptools.SourceCodeToolName) {
		t.Error("genel sohbet + çözülmüş kod kapsamı → sunulur")
	}
	if hasTool(sourceCodeToolsFor(tools, false, user, false), mcptools.SourceCodeToolName) {
		t.Error("kapsam yoksa sunulmaz")
	}
	if hasTool(sourceCodeToolsFor(tools, false, token, true), mcptools.SourceCodeToolName) {
		t.Error("API token'ına sunulmaz")
	}
}
