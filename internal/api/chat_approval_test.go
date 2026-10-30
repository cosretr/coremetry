package api

// chat_approval_test.go — agent-core S4 (docs/cosre/agent-core-ideas.md §4.7 Saf):
// onay jetonu bağlanır (exchange, adım, araç, argüman digest'i, kullanıcı,
// rol), tek kullanımlıktır, zaman aşımı RET'tir; Redis yoksa RET. Onay-kapılı
// araç katalogda yok; approvalGatePlan yalnız daraltır; onay bekleyen tur
// modeli yeniden çağırmaz (tavan turu da yok).

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcp"
)

func TestApprovalArgsDigestCanonical(t *testing.T) {
	a := approvalArgsDigest(json.RawMessage(`{"problemId":"p1","text":"x"}`))
	b := approvalArgsDigest(json.RawMessage(`{ "text":"x", "problemId":"p1" }`))
	c := approvalArgsDigest(json.RawMessage(`{"problemId":"p1","text":"y"}`))
	if a != b || a == c || len(a) != 64 {
		t.Fatalf("digest: %s %s %s", a, b, c)
	}
	if approvalArgsDigest(json.RawMessage(`{bozuk`)) == approvalArgsDigest(json.RawMessage(`{bozuk2`)) {
		t.Fatal("çözülemeyen argüman ham bayttan ayrışmalı")
	}
}

func TestDecideApprovalBinding(t *testing.T) {
	svc := auth.NewService("test-secret-that-is-long-enough-0123456789", time.Hour)
	now := time.Unix(1_800_000_000, 0)
	args := json.RawMessage(`{"problemId":"p1","text":"not"}`)
	tok, _, err := svc.SignApproval(auth.ApprovalClaims{
		Exchange: "x-1", Step: "s4", Tool: "add_problem_note", ArgsDigest: approvalArgsDigest(args), User: "u-1",
	}, now, 300*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	good := approvalAnswer{StepID: "s4", Tool: "add_problem_note", Args: args, Token: tok, Approved: true}
	editor := approvalCaller{UserID: "u-1", Role: auth.RoleEditor}
	cases := []struct {
		name     string
		at       time.Time
		mut      func(*approvalAnswer)
		exchange string
		who      approvalCaller
		minRole  string
		want     approvalDecision
	}{
		{"onay", now, nil, "x-1", editor, "editor", approvalApproved},
		{"ret", now, func(a *approvalAnswer) { a.Approved = false }, "x-1", editor, "editor", approvalDenied},
		{"zaman aşımı = ret", now.Add(301 * time.Second), nil, "x-1", editor, "editor", approvalExpired},
		{"başka alışveriş", now, nil, "x-2", editor, "editor", approvalMismatch},
		{"başka adım", now, func(a *approvalAnswer) { a.StepID = "s5" }, "x-1", editor, "editor", approvalMismatch},
		{"başka araç", now, func(a *approvalAnswer) { a.Tool = "mute_problem" }, "x-1", editor, "editor", approvalMismatch},
		{"argüman değişti", now, func(a *approvalAnswer) { a.Args = json.RawMessage(`{"problemId":"p2","text":"not"}`) }, "x-1", editor, "editor", approvalMismatch},
		{"argüman sırası farkı aynı", now, func(a *approvalAnswer) { a.Args = json.RawMessage(`{"text":"not","problemId":"p1"}`) }, "x-1", editor, "editor", approvalApproved},
		{"başka kullanıcı", now, nil, "x-1", approvalCaller{UserID: "u-2", Role: auth.RoleAdmin}, "editor", approvalForbidden},
		{"boş kullanıcı", now, nil, "x-1", approvalCaller{Role: auth.RoleAdmin}, "editor", approvalForbidden},
		{"viewer'a düşmüş hesap", now, nil, "x-1", approvalCaller{UserID: "u-1", Role: auth.RoleViewer}, "editor", approvalForbidden},
		{"admin-yalnız ayar, editor", now, nil, "x-1", editor, "admin", approvalForbidden},
		{"admin-yalnız ayar, admin", now, nil, "x-1", approvalCaller{UserID: "u-1", Role: auth.RoleAdmin}, "admin", approvalApproved},
		{"kurcalanmış jeton", now, func(a *approvalAnswer) { a.Token = a.Token[:len(a.Token)-2] + "xx" }, "x-1", editor, "editor", approvalInvalid},
		{"boş jeton", now, func(a *approvalAnswer) { a.Token = "" }, "x-1", editor, "editor", approvalInvalid},
	}
	for _, c := range cases {
		a := good
		if c.mut != nil {
			c.mut(&a)
		}
		cl, verr := svc.VerifyApproval(a.Token, c.at)
		got := decideApproval(verr, cl, a, c.exchange, c.who, c.minRole)
		if got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
		if got.executes() != (c.want == approvalApproved) {
			t.Errorf("%s: executes()=%v", c.name, got.executes())
		}
	}
}

// setnxFake — tek-kullanım dikişi: gerçek SETNX anlamı (ilk kazanır) + hata.
type setnxFake struct {
	cache.Cache
	seen map[string]bool
	err  error
}

func (f *setnxFake) SetNX(_ context.Context, key string, _ []byte, _ time.Duration) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	if f.seen[key] {
		return false, nil
	}
	f.seen[key] = true
	return true, nil
}

func TestClaimApprovalOnce(t *testing.T) {
	ctx := context.Background()
	f := &setnxFake{seen: map[string]bool{}}
	if got := claimApprovalOnce(ctx, f, true, "tok-1", time.Minute); got != approvalApproved {
		t.Fatalf("ilk kullanım: %s", got)
	}
	if got := claimApprovalOnce(ctx, f, true, "tok-1", time.Minute); got != approvalReplayed {
		t.Fatalf("ikinci kullanım: %s", got)
	}
	if got := claimApprovalOnce(ctx, f, true, "tok-2", time.Minute); got != approvalApproved {
		t.Fatalf("başka jeton: %s", got)
	}
	for k := range f.seen {
		if k == "cosre:approval:tok-1" || len(k) != len("cosre:approval:")+64 {
			t.Fatalf("jeton Redis anahtarına ham yazıldı: %s", k)
		}
	}
	noop, _ := cache.NewNoop()
	cases := []struct {
		name  string
		c     cache.Cache
		redis bool
		ttl   time.Duration
	}{
		{"Redis yok (noop her zaman kazanır)", noop, false, time.Minute},
		{"nil önbellek", nil, true, time.Minute},
		{"ttl 0", f, true, 0},
		{"SETNX hatası", &setnxFake{seen: map[string]bool{}, err: errors.New("down")}, true, time.Minute},
	}
	for _, c := range cases {
		if got := claimApprovalOnce(ctx, c.c, c.redis, "tok-x", c.ttl); got.executes() {
			t.Errorf("%s: %s — RET olmalı", c.name, got)
		}
	}
}

func TestToolsForRoleDropsApprovalTools(t *testing.T) {
	tools := []mcp.Tool{{Name: "read"}, {Name: "add_problem_note", MinRole: "editor", Approval: true}, {Name: "ed", MinRole: "editor"}}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor, auth.RoleAdmin} {
		for _, tl := range toolsForRole(tools, role) {
			if tl.Approval {
				t.Errorf("%s: onay-kapılı araç katalogda", role)
			}
		}
	}
	if n := len(toolsForRole(tools, auth.RoleAdmin)); n != 2 {
		t.Errorf("admin: %d araç, want 2", n)
	}
}

func TestApprovalGatePlan(t *testing.T) {
	specs := []copilot.ToolSpec{{Name: "read"}, {Name: "add_problem_note"}, {Name: "render_chart"}}
	gated := map[string]bool{"add_problem_note": true}
	names := func(p stepPlan, in stepInput) []string {
		out := []string{}
		for _, s := range p.planSpecs(in) {
			out = append(out, s.Name)
		}
		return out
	}
	all := []string{"read", "add_problem_note", "render_chart"}
	without := []string{"read", "render_chart"}
	cases := []struct {
		name    string
		gated   map[string]bool
		v2      bool
		role    string
		minRole string
		want    []string
		nilSpec bool
	}{
		{"kapılı araç yok → plan aynen", nil, false, auth.RoleViewer, "editor", all, true},
		{"v1 → düşer", gated, false, auth.RoleAdmin, "editor", without, false},
		{"v2 viewer → düşer", gated, true, auth.RoleViewer, "editor", without, false},
		{"v2 editor, admin-yalnız → düşer", gated, true, auth.RoleEditor, "admin", without, false},
		{"v2 editor → kalır", gated, true, auth.RoleEditor, "editor", all, true},
	}
	for _, c := range cases {
		in := stepInput{Specs: specs, Role: c.role}
		p := prepareChain(capTurnPlan, approvalGatePlan(c.gated, c.v2, c.minRole))(in)
		if got := names(p, in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
		if (p.Specs == nil) != c.nilSpec {
			t.Errorf("%s: Specs nil=%v (düşen yoksa plan spec'e dokunmamalı)", c.name, p.Specs == nil)
		}
	}
	// Çağrılmış kapılı araç düşürülemez (sağlayıcı tanımı ister): narrowSpecs korur.
	in := stepInput{Specs: specs, Role: auth.RoleViewer, Conv: []copilot.ChatMessage{{ToolCalls: []copilot.ToolCall{{Name: "add_problem_note"}}}}}
	if got := names(approvalGatePlan(gated, false, "editor")(in, stepPlan{}), in); !reflect.DeepEqual(got, all) {
		t.Errorf("çağrılmış araç: %v", got)
	}
}

func TestApprovalPendingStopsWithoutCapTurn(t *testing.T) {
	st := loopState{Round: 4, MaxRounds: 5, CallsLeft: 0, ApprovalPending: true}
	r := defaultStop(5)(st)
	if r != stopApprovalPending || isCapStop(r) {
		t.Fatalf("onay bekleyen tur: %q (tavan turu=%v)", r, isCapStop(r))
	}
	st.Cancelled = true
	if r := defaultStop(5)(st); r != stopCancelled {
		t.Fatalf("iptal onaydan önce: %q", r)
	}
}

func TestApprovalReasonConstantsAgree(t *testing.T) {
	if skipReasonApproval != uistream.ReasonApprovalRequired || string(skipReasonApproval) != agenttools.KindApproval {
		t.Fatalf("onay nedeni ayrıştı: api=%q uistream=%q executor=%q", skipReasonApproval, uistream.ReasonApprovalRequired, agenttools.KindApproval)
	}
}

func TestApprovalStepResultShape(t *testing.T) {
	exp := time.UnixMilli(1_800_000_300_000)
	b, _ := json.Marshal(approvalStepResult(3, "add_problem_note", `{"error":"approval_required"}`, "tok", "d1", exp))
	want := `{"approval":{"argsDigest":"d1","expiresAt":1800000300000,"token":"tok"},"bytes":29,"i":3,"ok":false,"preview":"{\"error\":\"approval_required\"}","reason":"approval_required","skipped":true,"tool":"add_problem_note","truncated":false}`
	if string(b) != want {
		t.Fatalf("\n got %s\nwant %s", b, want)
	}
}
