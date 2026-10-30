package api

// chat_user_scope_test.go — CoSRE v2 araç seti dilim 2 (docs/cosre/tools-audit.md
// §3, §10 G8/G9/G10, Q1/Q2/Q7). Fikstürler nötr (orders-api, com.acme).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestResolveUserScopeCore(t *testing.T) {
	many := make([]string, mcptools.MaxTeamServices+3)
	for i := range many {
		many[i] = fmt.Sprintf("svc-%03d", i)
	}
	base := userScopeInput{Enabled: true, UserID: "u1", Team: "acme-orders", TeamServices: []string{"billing-api", "orders-api"}}
	with := func(f func(*userScopeInput)) userScopeInput { in := base; f(&in); return in }
	cases := []struct {
		name         string
		in           userScopeInput
		wantReason   string
		wantUnscoped bool
		wantN        int
		wantTrimmed  int
	}{
		{"kapsamlı", base, scopeReasonTeam, false, 2, 0},
		{"anahtar kapalı (Q2)", with(func(i *userScopeInput) { i.Enabled = false }), scopeReasonDisabled, true, 0, 0},
		{"@-kapsam kaçışı (Q2)", with(func(i *userScopeInput) { i.Escape = true }), scopeReasonEscape, true, 0, 0},
		{"kimlik yok", with(func(i *userScopeInput) { i.UserID = "" }), scopeReasonNoIdentity, true, 0, 0},
		{"okuma hatası", with(func(i *userScopeInput) { i.LookupErr = errors.New("ch down") }), scopeReasonError, true, 0, 0},
		{"takım boş (Q1)", with(func(i *userScopeInput) { i.Team = "  " }), scopeReasonNoTeam, true, 0, 0},
		{"takım servis sahibi değil (Q1)", with(func(i *userScopeInput) { i.TeamServices = nil }), scopeReasonNoServices, true, 0, 0},
		{"tavan", with(func(i *userScopeInput) { i.TeamServices = many }), scopeReasonTeam, false, mcptools.MaxTeamServices, 3},
	}
	for _, c := range cases {
		got := resolveUserScopeCore(c.in)
		if got.Reason != c.wantReason || got.Unscoped != c.wantUnscoped || len(got.Services) != c.wantN || got.Trimmed != c.wantTrimmed {
			t.Errorf("%s: %+v", c.name, got)
		}
		if got.Unscoped && len(got.Services) > 0 {
			t.Errorf("%s: kapsamsız ama servis taşıyor (sessiz genişleme/daralma)", c.name)
		}
		if !slices.Contains(uistream.ScopeReasons, got.Reason) {
			t.Errorf("%s: %q uistream.ScopeReasons'ta yok", c.name, got.Reason)
		}
	}
}

// Kapalı küme Go tarafında eşit (FE protocol.ts SCOPE_REASONS ile vitest pinler).
func TestScopeReasonsClosedSet(t *testing.T) {
	ours := []string{scopeReasonTeam, scopeReasonNoIdentity, scopeReasonNoTeam, scopeReasonNoServices, scopeReasonDisabled, scopeReasonEscape, scopeReasonError}
	if !slices.Equal(ours, uistream.ScopeReasons) {
		t.Fatalf("api %v ≠ uistream %v", ours, uistream.ScopeReasons)
	}
}

func TestUserScopeEvent(t *testing.T) {
	svcs := make([]string, 30)
	for i := range svcs {
		svcs[i] = fmt.Sprintf("orders-api-%02d", i)
	}
	ev := userScope{Reason: scopeReasonTeam, Team: "acme-orders", Services: svcs, Trimmed: 5}.event()
	if got := ev["services"].([]string); len(got) != scopeEventMaxServices {
		t.Errorf("services %d", len(got))
	}
	if ev["count"] != 35 || ev["trimmed"] != 5 || ev["unscoped"] != false {
		t.Errorf("event %v", ev)
	}
	un := userScope{Unscoped: true, Reason: scopeReasonNoTeam}.event()
	if _, has := un["services"]; has || un["unscoped"] != true || un["count"] != 0 {
		t.Errorf("unscoped event %v", un)
	}
}

func TestApplyUserScope(t *testing.T) {
	scoped := userScope{Reason: scopeReasonTeam, Team: "acme-orders", Services: []string{"billing-api", "orders-api"}}
	mySchema := mcptools.V2OnlyTools(mcptools.Deps{})[0].InputSchema
	single := map[string]any{"properties": map[string]any{"service": map[string]any{"type": "string"}}}
	spike := map[string]any{"properties": map[string]any{"query": map[string]any{"type": "string"}, "service": map[string]any{"type": "string"}}}
	cases := []struct {
		name, tool, args string
		schema           map[string]any
		u                userScope
		want             string
		applied          bool
	}{
		{"boş argümana kapsam", mcptools.ListMyServicesName, ``, mySchema, scoped, `{"services":["billing-api","orders-api"],"team":"acme-orders"}`, true},
		{"pencere korunur", mcptools.ListMyServicesName, `{"range_s":600}`, mySchema, scoped, `{"range_s":600,"services":["billing-api","orders-api"],"team":"acme-orders"}`, true},
		{"modelin servisi kalır", mcptools.ListMyServicesName, `{"services":["payments-api"]}`, mySchema, scoped, `{"services":["payments-api"]}`, false},
		{"başka takım kalır", mcptools.ListMyServicesName, `{"team":"acme-billing"}`, mySchema, scoped, `{"team":"acme-billing"}`, false},
		{"aynı takım + kapsam", mcptools.ListMyServicesName, `{"team":"ACME-orders"}`, mySchema, scoped, `{"services":["billing-api","orders-api"],"team":"ACME-orders"}`, true},
		{"kapsamsız dokunmaz", mcptools.ListMyServicesName, `{}`, mySchema, userScope{Unscoped: true, Reason: scopeReasonNoTeam}, `{}`, false},
		{"tekil service şemasına dokunmaz", "get_service_health", `{}`, single, scoped, `{}`, false},
		{"explain_log_spike query düşer (kapsamsızken de)", mcptools.ExplainLogSpikeName, `{"query":"level:error","service":"orders-api"}`, spike, userScope{Unscoped: true, Reason: scopeReasonDisabled}, `{"service":"orders-api"}`, false},
	}
	for _, c := range cases {
		got, applied := applyUserScope(c.tool, json.RawMessage(c.args), c.schema, c.u)
		if string(got) != c.want || applied != c.applied {
			t.Errorf("%s: %s applied=%v, want %s %v", c.name, got, applied, c.want, c.applied)
		}
	}
}

func TestUserScopeAddendum(t *testing.T) {
	asked := []copilot.ChatMessage{{Role: "user", Text: "benim servislerim"}, {Role: "assistant", Text: "Hangi takım?"}, {Role: "user", Text: "acme-orders"}}
	userSaid := []copilot.ChatMessage{{Role: "user", Text: "hangi takım bu servisin sahibi?"}}
	cases := []struct {
		name string
		u    userScope
		msgs []copilot.ChatMessage
		want string
	}{
		{"kapsamlı", userScope{Reason: scopeReasonTeam, Team: "acme-orders", Services: []string{"orders-api"}}, nil, copilot.ChatUserScopeAddendum("acme-orders", 1)},
		{"takım yok: bir kez sor", userScope{Unscoped: true, Reason: scopeReasonNoTeam}, nil, copilot.ChatUserScopeAskTeamAddendum()},
		{"kullanıcının sorusu 'soruldu' sayılmaz", userScope{Unscoped: true, Reason: scopeReasonNoServices}, userSaid, copilot.ChatUserScopeAskTeamAddendum()},
		{"zaten soruldu: bir daha sorma", userScope{Unscoped: true, Reason: scopeReasonNoIdentity}, asked, copilot.ChatUserScopeUnscopedAddendum()},
		{"kaçış: ek yok", userScope{Unscoped: true, Reason: scopeReasonEscape}, nil, ""},
		{"kapalı: ek yok", userScope{Unscoped: true, Reason: scopeReasonDisabled}, nil, ""},
	}
	for _, c := range cases {
		if got := userScopeAddendum(c.u, c.msgs); got != c.want {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	if !strings.Contains(strings.ToLower(copilot.ChatUserScopeAskTeamAddendum()), copilot.ChatUserScopeAskMarker) {
		t.Error("sorma eki işaret ifadesini istemiyor: 'bir kez' sezgisi çalışmaz")
	}
}

func specsOf(tools []mcp.Tool) []copilot.ToolSpec {
	out := make([]copilot.ToolSpec, 0, len(tools))
	for _, t := range tools {
		out = append(out, copilot.ToolSpec{Name: t.Name, Description: t.ChatDescription(), InputSchema: t.InputSchema})
	}
	return out
}

func specNames(specs []copilot.ToolSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return out
}

// v2 prepareStep planı ile mcptools.V2ToolList (altın `*/v2`) aynı listeyi verir.
func TestV2PlanMatchesV2ToolList(t *testing.T) {
	d := mcptools.Deps{}
	full := specsOf(append(mcptools.ChatToolListFor(d, "full"), mcptools.V2OnlyTools(d)...))
	in := stepInput{Kind: stepNormal, Specs: full, V2: &v2LoopPlan{Addendum: "\n\nEK"}}
	plan := v2Prepare(in)
	got := specNames(plan.planSpecs(in))
	want := make([]string, 0)
	for _, tl := range mcptools.V2ToolList(d, "full") {
		want = append(want, tl.Name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("plan %v\n≠ V2ToolList %v", got, want)
	}
	if plan.SystemAddendum != "\n\nEK" {
		t.Errorf("ek %q", plan.SystemAddendum)
	}
	for _, sp := range plan.planSpecs(in) {
		if sp.Name == mcptools.ExplainLogSpikeName {
			if _, has := sp.InputSchema["properties"].(map[string]any)["query"]; has {
				t.Error("explain_log_spike.query v2 spec'inde")
			}
		}
	}
	// Tavan turu: alt küme + ek KALIR, tavan eki arkaya eklenir.
	capIn := in
	capIn.Kind = stepCap
	cp := v2Prepare(capIn)
	if !strings.HasPrefix(cp.SystemAddendum, "\n\nEK") || !strings.HasSuffix(cp.SystemAddendum, copilot.ChatRoundCapAddendum()) || cp.Ctx == nil {
		t.Errorf("tavan turu planı %q", cp.SystemAddendum)
	}
}

// v1 (V2 nil): v2 adımı kimlik — defaultPrepare ile aynı plan.
func TestV2SubsetPlanIsIdentityOnV1(t *testing.T) {
	full := specsOf(mcptools.ChatToolListFor(mcptools.Deps{}, "full"))
	for _, k := range []stepKind{stepNormal, stepCap, stepDegenRetry} {
		in := stepInput{Kind: k, Specs: full}
		a, b := defaultPrepare(in), v2Prepare(in)
		if a.Specs != nil || b.Specs != nil || a.SystemAddendum != b.SystemAddendum || (a.Ctx == nil) != (b.Ctx == nil) {
			t.Errorf("kind %d: v1 planı değişti", k)
		}
	}
}

// Konuşmada çağrılmış gizli araç tanımını korur (sağlayıcı 400 vermesin), tam şemasıyla.
func TestNarrowSpecsKeepsCalledHiddenToolAndSubsetSchema(t *testing.T) {
	full := []copilot.ToolSpec{
		{Name: "search_traces", InputSchema: map[string]any{"v": "full"}},
		{Name: mcptools.ExplainLogSpikeName, InputSchema: map[string]any{"v": "full"}},
		{Name: "query_logs", InputSchema: map[string]any{"v": "full"}},
	}
	subset := []copilot.ToolSpec{{Name: mcptools.ExplainLogSpikeName, InputSchema: map[string]any{"v": "narrow"}}, {Name: "query_logs", InputSchema: map[string]any{"v": "full"}}, {Name: "invented_tool"}}
	conv := []copilot.ChatMessage{{Role: "assistant", ToolCalls: []copilot.ToolCall{{Name: "search_traces"}}}}
	got := narrowSpecs(full, subset, conv)
	if names := specNames(got); !slices.Equal(names, []string{"search_traces", mcptools.ExplainLogSpikeName, "query_logs"}) {
		t.Fatalf("adlar %v (araç eklenemez, çağrılan kalır)", names)
	}
	if got[0].InputSchema["v"] != "full" || got[1].InputSchema["v"] != "narrow" {
		t.Errorf("şemalar %v / %v", got[0].InputSchema, got[1].InputSchema)
	}
}

func TestV1OnlySinkDropsScope(t *testing.T) {
	if !v2OnlyEventKinds[chatScopeEventKind] || !uistream.KnownKinds[chatScopeEventKind] {
		t.Fatal("scope v2-yalnız + uistream'de bilinen tür olmalı")
	}
}

func TestCosreV2Marker(t *testing.T) {
	ctx := context.Background()
	if cosreV2Marked(ctx) || v2LoopPlanFrom(ctx) != nil {
		t.Fatal("işaretsiz ctx v2 sayıldı")
	}
	ctx = withCosreV2Loop(ctx)
	if !cosreV2Marked(ctx) {
		t.Fatal("işaret yok")
	}
	p := &v2LoopPlan{Addendum: "x"}
	if v2LoopPlanFrom(withV2LoopPlan(ctx, p)) != p {
		t.Fatal("plan taşınmadı")
	}
}

func TestParseAIUserScope(t *testing.T) {
	for raw, want := range map[string]bool{"": true, "{}": true, "bozuk": true, `{"enabled":true}`: true, `{"enabled":false}`: false, `{"enabled":"no"}`: true} {
		if got := parseAIUserScope([]byte(raw)); got != want {
			t.Errorf("%q → %v", raw, got)
		}
	}
}

func TestAIUserScopeMemoDefaultsOnError(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	calls := 0
	m := &aiUserScopeMemo{now: func() time.Time { return now }, load: func(context.Context) (bool, error) {
		calls++
		return false, errors.New("ch down")
	}}
	if !m.get(context.Background()) {
		t.Fatal("okuma hatası varsayılana (açık) düşmeli")
	}
	m.get(context.Background())
	if calls != 1 {
		t.Errorf("memo %d okuma", calls)
	}
	m.set(false)
	if m.get(context.Background()) {
		t.Error("set yayınlanmadı")
	}
}

func TestEvalScopeFails(t *testing.T) {
	scope := &userScope{Reason: scopeReasonTeam, Services: []string{"billing-api", "orders-api"}}
	c := evalCase{UserTeam: "acme-orders", Expect: evalExpect{ServicesInScope: true}}
	cases := []struct {
		name    string
		c       evalCase
		st      evalAgentStats
		want    int
		wantGap bool
	}{
		{"hepsi kapsamda", c, evalAgentStats{Scope: scope, ScopeCalls: []evalScopeCall{{"list_my_services", []string{"billing-api", "orders-api"}}, {"get_service_health", []string{"orders-api"}}}}, 0, false},
		{"kapsam dışı", c, evalAgentStats{Scope: scope, ScopeCalls: []evalScopeCall{{"get_service_health", []string{"payments-api"}}}}, 1, false},
		{"kapsam çözülmedi → atla", c, evalAgentStats{Scope: &userScope{Unscoped: true, Reason: scopeReasonNoServices}}, 0, true},
		{"v2 koşmadı → atla", c, evalAgentStats{}, 0, true},
		{"beklenti yok", evalCase{}, evalAgentStats{Scope: scope, ScopeCalls: []evalScopeCall{{"x", []string{"payments-api"}}}}, 0, false},
	}
	for _, tc := range cases {
		if got := evalScopeFails(tc.c, tc.st); len(got) != tc.want {
			t.Errorf("%s: %v", tc.name, got)
		}
		if gap := evalScopeHarnessGap(tc.c, tc.st); (gap != "") != tc.wantGap {
			t.Errorf("%s: gap %q", tc.name, gap)
		}
	}
	if got := evalCallServices(json.RawMessage(`{"services":["a"," ",""],"service":"b"}`)); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("evalCallServices %v", got)
	}
	col := &evalAgentCollector{}
	col.onTool("list_my_services", json.RawMessage(`{"services":["orders-api"]}`), agenttools.Outcome{Executed: true})
	col.onTool("get_service_health", json.RawMessage(`{"service":"payments-api"}`), agenttools.Outcome{})
	if len(col.st.ScopeCalls) != 1 {
		t.Errorf("yalnız yürüyen çağrı sayılır: %v", col.st.ScopeCalls)
	}
}

// Rota kayıtlı (kayıtsız rota 200 + boş sayfa döner, 404 değil) ve PUT admin + audit.
func TestAIUserScopeRoutesAndAudit(t *testing.T) {
	mux := (&Server{}).buildMux()
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		if _, pattern := mux.Handler(httptest.NewRequest(m, "/api/ai/user-scope", nil)); pattern != m+" /api/ai/user-scope" {
			t.Errorf("%s çözümü %q", m, pattern)
		}
	}
	src, err := os.ReadFile("ai_user_scope_settings.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		`s.audit(r, "settings.ai_user_scope.update", "settings", aiUserScopeSettingKey`,
		`auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putAIUserScope))`,
		`http.MaxBytesReader(w, r.Body, aiUserScopeMaxBody)`,
	} {
		if !strings.Contains(string(src), must) {
			t.Errorf("ai_user_scope_settings.go %q içermiyor", must)
		}
	}
	if aiUserScopeSettingKey != "ai.userScope" {
		t.Errorf("anahtar %q", aiUserScopeSettingKey)
	}
}
