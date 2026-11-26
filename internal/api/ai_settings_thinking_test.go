package api

// ai_settings_thinking_test.go — v0.10.2458: yüzey başına düşünme ayarı.
// Admin kapısı, kısmi PUT birleşimi, audit satırı, 400'de yazım yok, depo
// yokken 503; sağlık raporu yüzeyi; profil thinkingSwitch korunur + audit.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

type thinkMemStore struct {
	m     map[string][]byte
	saves int
}

func (s *thinkMemStore) GetSetting(_ context.Context, k string) ([]byte, error) { return s.m[k], nil }
func (s *thinkMemStore) PutSetting(_ context.Context, k string, v []byte) error {
	s.m[k] = append([]byte(nil), v...)
	s.saves++
	return nil
}

func TestAIThinkingSettingsRolesAuditRoundTrip(t *testing.T) {
	c, _ := cache.NewNoop()
	cop := copilot.New(copilot.ProviderOpenAI, "", "qwen38-flash-next")
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), copilot: cop, auditQ: make(chan chstore.AuditEntry, 16)}
	st := &thinkMemStore{m: map[string][]byte{}}
	prev := aiThinkingStoreOf
	aiThinkingStoreOf = func(*Server) copilot.SettingsStore { return st }
	t.Cleanup(func() { aiThinkingStoreOf = prev })
	mux := http.NewServeMux()
	s.registerAISettingsThinkingRoutes(mux)

	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if w := rulesDo(mux, "GET", "/api/settings/ai/thinking", "", role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s GET %d", role, w.Code)
		}
		if w := rulesDo(mux, "PUT", "/api/settings/ai/thinking", `{"trace":"on"}`, role, ""); w.Code != http.StatusForbidden {
			t.Fatalf("%s PUT %d", role, w.Code)
		}
	}
	w := rulesDo(mux, "GET", "/api/settings/ai/thinking", "", auth.RoleAdmin, "")
	var body struct {
		Surfaces []struct {
			ID, Label, Mode string
		} `json:"surfaces"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || len(body.Surfaces) != len(copilot.ThinkSurfaces()) {
		t.Fatalf("GET %d: %s", w.Code, w.Body)
	}
	for _, sf := range body.Surfaces { // v0.10.2471 — varsayılan on, intent off
		if want := copilot.DefaultThinkingModes()[sf.ID]; sf.Mode != want || (sf.ID == "intent" || sf.ID == "trace_narrative") != (sf.Mode == "off") { // v0.10.2480 — trace_narrative da kapalı
			t.Fatalf("default must be on except intent: %+v (want %s)", sf, want)
		}
	}

	w = rulesDo(mux, "PUT", "/api/settings/ai/thinking", `{"trace":" on ","investigation":"inherit"}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"trace"`) {
		t.Fatalf("admin PUT %d: %s", w.Code, w.Body)
	}
	if m := cop.ThinkingModes(); m["trace"] != "on" || m["investigation"] != "inherit" || m["intent"] != "off" {
		t.Fatalf("live modes %v", m)
	}
	if !strings.Contains(string(st.m[copilot.ThinkingSettingsKey]), `"trace":"on"`) {
		t.Fatalf("stored %s", st.m[copilot.ThinkingSettingsKey])
	}
	a := nextAudit(t, s)
	if a.Action != "settings.ai.thinking" || a.TargetKind != "settings" || a.Details != "investigation=inherit trace=on" || a.ActorEmail != "admin@example.test" {
		t.Fatalf("audit %+v", a)
	}

	for _, bad := range []string{`{"trace":"auto"}`, `{"nope":"on"}`, `{not json`} {
		if w := rulesDo(mux, "PUT", "/api/settings/ai/thinking", bad, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d", bad, w.Code)
		}
	}
	if st.saves != 1 || len(s.auditQ) != 0 {
		t.Fatalf("400 must not write: saves=%d audit=%d", st.saves, len(s.auditQ))
	}

	aiThinkingStoreOf = func(*Server) copilot.SettingsStore { return nil }
	if w := rulesDo(mux, "PUT", "/api/settings/ai/thinking", `{"trace":"off"}`, auth.RoleAdmin, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store → %d", w.Code)
	}
}

func TestAIThinkingRoutesRegisteredOutsideAPIGo(t *testing.T) {
	routes, err := os.ReadFile("ai_routes.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(routes), "s.registerAISettingsThinkingRoutes(mux)") {
		t.Fatal("ai_routes.go must register the thinking routes")
	}
	apiGo, _ := os.ReadFile("api.go")
	if strings.Contains(string(apiGo), "/api/settings/ai/thinking") {
		t.Fatal("api.go must not carry the route")
	}
}

func TestGuidedThinkingSurface(t *testing.T) {
	if guidedThinkingSurface(guidedServiceHealth) != copilot.ThinkHealthReport || guidedThinkingSurface(guidedServiceReport) != copilot.ThinkHealthReport {
		t.Fatal("service health / report → health_report")
	}
	if guidedThinkingSurface(guidedNone) != "" {
		t.Fatal("other intents keep label classification")
	}
}

func TestProfileThinkingSwitchPreservedAndAudited(t *testing.T) {
	existing := []copilot.ModelProfile{{ID: "p", ThinkingSwitch: true}}
	if !resolveThinkingSwitch(nil, "p", existing) || resolveThinkingSwitch(nil, "q", existing) {
		t.Fatal("nil body keeps the stored flag")
	}
	f := false
	if resolveThinkingSwitch(&f, "p", existing) {
		t.Fatal("explicit false wins")
	}
	if got := aiProfileUpsertAudit(copilot.ModelProfile{Provider: "openai", ThinkingSwitch: true}, false); !strings.HasSuffix(got, " thinkingSwitch=true") {
		t.Fatalf("audit %q", got)
	}
	if got := aiProfileUpsertAudit(copilot.ModelProfile{Provider: "openai"}, false); strings.Contains(got, "thinkingSwitch") {
		t.Fatalf("unset flag keeps old audit line: %q", got)
	}
}
