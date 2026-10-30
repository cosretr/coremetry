package tools

// approval_test.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.7 Executor):
// Approve=nil ≡ eski yol; ApprovalAsk → handler çağrılmadı, span/audit yok,
// Args = kapsam SONRASI argüman; reddedilen/iptal/bilinmeyen çağrı onaya
// hiç sorulmaz (sıra: iptal → bilinmeyen → tekrar → kapsam → onay).

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// rewriteScope — kapsam argümanı değiştirir (onay digest'i bunu görmeli).
type rewriteScope struct{}

func (rewriteScope) Constrain(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
	if name == "deny" {
		return nil, context.Canceled
	}
	return json.RawMessage(`{"scoped":true}`), nil
}

func TestExecutorApproval(t *testing.T) {
	type rec struct{ handler, spans, audits, asks int }
	newX := func(r *rec, approve func(context.Context, string, json.RawMessage) ApprovalDecision) *Executor {
		h := func(context.Context, json.RawMessage) (any, error) { r.handler++; return map[string]int{"ok": 1}, nil }
		hk := Hooks{
			Span: func(ctx context.Context, _ string, _ bool) (context.Context, func(int, bool)) {
				r.spans++
				return ctx, func(int, bool) {}
			},
			Audit: func(string, json.RawMessage, time.Duration, error, int) { r.audits++ },
		}
		if approve != nil {
			hk.Approve = func(ctx context.Context, n string, a json.RawMessage) ApprovalDecision {
				r.asks++
				return approve(ctx, n, a)
			}
		}
		return NewExecutor(map[string]Handler{"add_note": h, "deny": h}, nil, hk).WithScope(rewriteScope{})
	}
	ask := func(context.Context, string, json.RawMessage) ApprovalDecision { return ApprovalAsk }
	allow := func(context.Context, string, json.RawMessage) ApprovalDecision { return ApprovalAllow }

	cases := []struct {
		name                         string
		approve                      func(context.Context, string, json.RawMessage) ApprovalDecision
		tool                         string
		ctxDone                      bool
		wantKind                     string
		wantExec                     bool
		wantHandler, wantAudit, asks int
	}{
		{"nil kanca = eski yol", nil, "add_note", false, KindOK, true, 1, 1, 0},
		{"allow = yürür", allow, "add_note", false, KindOK, true, 1, 1, 1},
		{"ask = durur", ask, "add_note", false, KindApproval, false, 0, 0, 1},
		{"bilinmeyen onaya sorulmaz", ask, "nope", false, KindUnknown, false, 0, 0, 0},
		{"kapsam reddi onaya sorulmaz", ask, "deny", false, KindScope, false, 0, 0, 0},
		{"iptal onaya sorulmaz", ask, "add_note", true, KindCancelled, false, 0, 0, 0},
	}
	for _, c := range cases {
		var r rec
		x := newX(&r, c.approve)
		ctx := context.Background()
		if c.ctxDone {
			cctx, cancel := context.WithCancel(ctx)
			cancel()
			ctx = cctx
		}
		oc := x.Call(ctx, c.tool, json.RawMessage(`{"raw":1}`))
		if oc.Kind != c.wantKind || oc.Executed != c.wantExec || r.handler != c.wantHandler || r.audits != c.wantAudit || r.asks != c.asks {
			t.Errorf("%s: kind=%s exec=%v handler=%d audit=%d asks=%d", c.name, oc.Kind, oc.Executed, r.handler, r.audits, r.asks)
		}
		if c.wantKind == KindApproval {
			if string(oc.Args) != `{"scoped":true}` {
				t.Errorf("%s: Args kapsam sonrası olmalı: %s", c.name, oc.Args)
			}
			if !oc.IsError || oc.Content != ApprovalRequiredJSON || r.spans != 0 {
				t.Errorf("%s: içerik/span: %+v spans=%d", c.name, oc, r.spans)
			}
		} else if oc.Args != nil {
			t.Errorf("%s: Args yalnız onayda dolu: %s", c.name, oc.Args)
		}
	}
	var gotArgs json.RawMessage
	x := NewExecutor(map[string]Handler{"add_note": func(context.Context, json.RawMessage) (any, error) { return 1, nil }}, nil, Hooks{
		Approve: func(_ context.Context, _ string, a json.RawMessage) ApprovalDecision {
			gotArgs = a
			return ApprovalAllow
		},
	}).WithScope(rewriteScope{})
	x.Call(context.Background(), "add_note", json.RawMessage(`{"raw":1}`))
	if string(gotArgs) != `{"scoped":true}` {
		t.Errorf("Approve kapsam sonrası argümanı görmeli: %s", gotArgs)
	}
}
