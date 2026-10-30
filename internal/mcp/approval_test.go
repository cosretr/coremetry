package mcp

// approval_test.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.5):
// onay-kapılı araç dış MCP telinde YOK. Dış istemcinin onay yüzeyi
// olmadığından Approval:true araç kaydedilseydi onaysız yürürdü.

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRegisterToolSkipsApprovalTools(t *testing.T) {
	s := New("t", "0")
	h := func(context.Context, json.RawMessage) (any, error) { return "ok", nil }
	s.RegisterTool(Tool{Name: "read_x", Handler: h})
	s.RegisterTool(Tool{Name: "add_note", Handler: h, MinRole: "editor", Approval: true})
	if n := s.ToolCount(); n != 1 {
		t.Fatalf("ToolCount=%d, want 1 (onay-kapılı araç kaydedilmemeli)", n)
	}
	s.mu.RLock()
	_, has := s.tools["add_note"]
	s.mu.RUnlock()
	if has {
		t.Fatal("onay-kapılı araç MCP kayıt defterinde")
	}
}
