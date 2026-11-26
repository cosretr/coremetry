package api

// v0.10.2243 (anomali kartı Dilim C) — görünüm parametresi: anahtarda,
// yalnız beyaz liste; held yalnız admin ve yalnız gölge "tutulur" olaylar.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestUnifiedViewParam(t *testing.T) {
	for raw, want := range map[string]string{"platform": "platform", "held": "held", "": "", "x": "", "service": ""} {
		q := url.Values{"view": {raw}}
		if got := parseUnifiedParams(q.Get).View; got != want {
			t.Errorf("view=%q → %q, beklenen %q", raw, got, want)
		}
	}
	a := unifiedKey(unifiedParams{Section: "open", Limit: 50}, "UTC")
	b := unifiedKey(unifiedParams{Section: "open", Limit: 50, View: "platform"}, "UTC")
	c := unifiedKey(unifiedParams{Section: "open", Limit: 50, View: "held"}, "UTC")
	if a == b || b == c || a == c {
		t.Fatal("görünüm önbellek anahtarında değil")
	}
}

func TestUnifiedHeldAdminOnly(t *testing.T) {
	s := &Server{}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		r := httptest.NewRequest(http.MethodGet, "/api/anomalies/unified?view=held", nil)
		r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{Role: role}))
		w := httptest.NewRecorder()
		s.getAnomaliesUnified(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s rolü Elenenler'i okudu (kod %d)", role, w.Code)
		}
	}
}

func TestHeldOnlyShadowVerdict(t *testing.T) {
	rec := func(gates []ml.RecordGate) string {
		b, _ := json.Marshal(ml.EventRecord{V: ml.RecordVersion, Gates: gates})
		return string(b)
	}
	drop := chstore.MLEvent{ID: "a", Record: rec([]ml.RecordGate{{Name: ml.GateVisibleDeviation, Passed: false}})}
	info := chstore.MLEvent{ID: "b", Record: rec([]ml.RecordGate{{Name: ml.GateEvidence, Passed: false}})}
	pass := chstore.MLEvent{ID: "c", Record: rec([]ml.RecordGate{{Name: ml.GateVisibleDeviation, Passed: true}})}
	invalid := drop
	invalid.ID, invalid.PublishState = "d", ml.PublishInvalid
	legacy := chstore.MLEvent{ID: "e"}
	got := heldOnly([]chstore.MLEvent{drop, info, pass, invalid, legacy}, chstore.MLGatesShadow)
	// v0.10.2476: invalid kayıt (d) da Elenenler'de (hiçbir listede yok).
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "d" {
		t.Fatalf("Elenenler = %+v, beklenen gölge drop kararı (a) + invalid kayıt (d)", got)
	}
}
