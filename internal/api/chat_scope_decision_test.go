package api

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

// chat_scope_decision_test.go — v0.10.2355 (investigation-design S1, karar 10).

var scopeTestUser = userScope{Reason: scopeReasonTeam, Team: "team-a", Services: []string{"checkout", "orders"}}

// Karar tablosu: kapsamda / bu turda anılan / ekran / durum varlığı / kaçış / kapsam dışı.
func TestDecideScopeTable(t *testing.T) {
	gate := func(mode string, in scopeGateIn) *scopeGate {
		in.Mode = mode
		if in.Scope.Reason == "" {
			in.Scope = scopeTestUser
		}
		return buildScopeGate(in)
	}
	type tc struct {
		name    string
		mode    string
		in      scopeGateIn
		tool    string
		args    string
		refuse  bool
		counted bool
	}
	cases := []tc{
		{"kapsamda", chstore.CosreEvidenceOn, scopeGateIn{}, "get_service_health", `{"service":"orders"}`, false, false},
		{"kapsamda (büyük harf)", chstore.CosreEvidenceOn, scopeGateIn{}, "get_service_health", `{"service":"Orders"}`, false, false},
		{"kapsam dışı → red", chstore.CosreEvidenceOn, scopeGateIn{}, "get_service_health", `{"service":"payments"}`, true, false},
		{"kapsam dışı services[] → red", chstore.CosreEvidenceOn, scopeGateIn{}, "find_traces", `{"services":["orders","payments"]}`, true, false},
		{"kullanıcı bu turda andı", chstore.CosreEvidenceOn, scopeGateIn{Named: []string{"payments"}}, "get_service_health", `{"service":"payments"}`, false, false},
		{"ekran servisi", chstore.CosreEvidenceOn, scopeGateIn{Screen: []string{"payments", ""}}, "get_service_health", `{"service":"payments"}`, false, false},
		{"durum varlığı", chstore.CosreEvidenceOn, scopeGateIn{State: []string{"payments"}}, "get_service_health", `{"service":"payments"}`, false, false},
		{"muaf araç", chstore.CosreEvidenceOn, scopeGateIn{}, "list_operations", `{"service":"payments"}`, false, false},
		{"sınıfsız (dış) araç", chstore.CosreEvidenceOn, scopeGateIn{}, "ext_tool", `{"service":"payments"}`, false, false},
		{"servis argümanı yok", chstore.CosreEvidenceOn, scopeGateIn{}, "get_service_health", `{}`, false, false},
		{"gölge yalnız sayar", chstore.CosreEvidenceShadow, scopeGateIn{}, "get_service_health", `{"service":"payments"}`, false, true},
		{"gölge kapsamda saymaz", chstore.CosreEvidenceShadow, scopeGateIn{}, "get_service_health", `{"service":"checkout"}`, false, false},
	}
	for _, c := range cases {
		g := gate(c.mode, c.in)
		counted := false
		if g != nil {
			g.signal = func(string, []string) { counted = true }
		}
		oc, refused := g.check(c.tool, json.RawMessage(c.args))
		if refused != c.refuse || counted != c.counted {
			t.Errorf("%s: red=%v sayım=%v", c.name, refused, counted)
		}
		if refused {
			var te map[string]any
			if json.Unmarshal([]byte(oc.Content), &te) != nil || te["error"] != outOfScopeError || te["retryable"] != false ||
				te["hint"] == "" || !strings.Contains(te["service"].(string), "payments") || oc.Kind != agenttools.KindScope || !oc.IsError || oc.Executed {
				t.Errorf("%s: red gövdesi %+v %q", c.name, oc, oc.Content)
			}
		}
	}
	// off, kapsamsız (kaçış dahil) ve trace takibi → kapı yok (değerlendirilmez).
	for name, in := range map[string]scopeGateIn{
		"off":         {Mode: chstore.CosreEvidenceOff, Scope: scopeTestUser},
		"boş kip":     {Scope: scopeTestUser},
		"kaçış":       {Mode: chstore.CosreEvidenceOn, Scope: userScope{Unscoped: true, Reason: scopeReasonEscape}},
		"takım yok":   {Mode: chstore.CosreEvidenceOn, Scope: userScope{Unscoped: true, Reason: scopeReasonNoTeam}},
		"trace takip": {Mode: chstore.CosreEvidenceOn, Scope: scopeTestUser, TraceFollowUp: true},
	} {
		if g := buildScopeGate(in); g != nil {
			t.Errorf("%s: kapı kuruldu", name)
		}
	}
	var nilPlan *v2LoopPlan
	if _, gated := nilPlan.scopeCheck("get_service_health", json.RawMessage(`{"service":"payments"}`)); gated {
		t.Fatal("v1 (plan nil) reddetti")
	}
	if _, gated := (&v2LoopPlan{}).scopeCheck("get_service_health", json.RawMessage(`{"service":"payments"}`)); gated {
		t.Fatal("kapısız plan reddetti")
	}
}

// off kipinde katalog bile okunmaz ve kapı kurulmaz; v1'de kip her zaman off.
func TestNewScopeGateOffIsInert(t *testing.T) {
	prev := chstore.CurrentCosreConversation()
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
	c := prev
	c.Evidence = chstore.CosreEvidenceOn
	chstore.SetCosreConversation(c)
	if m := cosreEvidenceMode(context.Background()); m != chstore.CosreEvidenceOff {
		t.Fatalf("v1 kipi %q", m)
	}
	c.Evidence = chstore.CosreEvidenceOff
	chstore.SetCosreConversation(c)
	var s *Server // off'ta okuma yok: nil sunucu panik etmez
	if g := s.newScopeGate(withCosreV2Loop(context.Background()), scopeTestUser, "payments", nil, ChatContext{}, false, "x"); g != nil {
		t.Fatal("off'ta kapı kuruldu")
	}
}

func TestScopeWouldRefuseSignalKind(t *testing.T) {
	if !cosrelearn.SystemSignalKinds[cosrelearn.SigScopeWouldRefuse] || cosrelearn.ClientSignalKinds[cosrelearn.SigScopeWouldRefuse] {
		t.Fatal("scope_would_refuse yalnız sistem sinyali olmalı")
	}
	if _, w := cosrelearn.Weight[cosrelearn.SigScopeWouldRefuse]; w {
		t.Fatal("ölçüm sinyali puana girmemeli")
	}
	if d := scopeWouldRefuseDetail("find_traces", []string{"payments"}); d != "tool=find_traces services=payments" {
		t.Fatalf("ayrıntı %q", d)
	}
}

// Dikiş TEK yerde: argüman kesinleştikten (applyScreenContext + applyUserScope)
// sonra, exec.Call'dan önce. Sonraki dilimler (S3–S6) bu satırı arar.
func TestScopeDecisionSeamPinned(t *testing.T) {
	b, err := os.ReadFile("copilot_chat.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, ".scopeCheck("); n != 1 {
		t.Fatalf("kapsam dikişi %d yerde", n)
	}
	user := strings.Index(src, "applyUserScope(tc.Name, cargs")
	seam := strings.Index(src, "v2LoopPlanFrom(ctx).scopeCheck(tc.Name, cargs)")
	call := strings.Index(src, "oc = exec.Call(ctx, tc.Name, cargs)")
	if user < 0 || seam < user || call < seam {
		t.Fatalf("dikiş sırası: user=%d seam=%d call=%d", user, seam, call)
	}
}

// PUT evidence: kapalı küme; diğer alanlar korunur.
func TestMergeCosreConversationPutEvidence(t *testing.T) {
	cur := chstore.DefaultCosreConversation()
	sh := chstore.CosreEvidenceShadow
	got, err := mergeCosreConversationPut(cur, cosreConversationPut{Evidence: &sh})
	if err != nil || got.Evidence != sh || !got.State || got.Verification != cur.Verification {
		t.Fatalf("shadow: %+v %v", got, err)
	}
	bad := "enforce"
	if _, err := mergeCosreConversationPut(cur, cosreConversationPut{Evidence: &bad}); err == nil {
		t.Fatal("geçersiz kip kabul edildi")
	}
}
