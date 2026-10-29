package api

// cosre_signal_weights_test.go — operatör kararı 2026-10-29 §1: kart sinyali
// ağırlıkları system_settings("cosre").signals.weights. Pinler: rotalar
// admin kapısının arkasında defterde, PUT audit yazar, katı denetim 400.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func TestCosreSignalWeightsRoutesAndAudit(t *testing.T) {
	src, err := os.ReadFile("cosre_signal_weights.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{
		`registerRoutesExtra("cosre-signal-weights"`,
		`auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getCosreSignalWeights))`,
		`auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putCosreSignalWeights))`,
		`s.audit(r, "settings.cosre_signal_weights.update", "settings", cosrelearn.SignalSettingsKey`,
		`http.MaxBytesReader(w, r.Body, cosreSignalWeightsMaxBody)`,
	} {
		if !strings.Contains(string(src), must) {
			t.Errorf("cosre_signal_weights.go %q içermiyor", must)
		}
	}
	mux := (&Server{}).buildMux()
	for _, m := range []string{http.MethodGet, http.MethodPut} {
		_, pattern := mux.Handler(httptest.NewRequest(m, "/api/admin/cosre/signal-weights", nil))
		if pattern != m+" /api/admin/cosre/signal-weights" {
			t.Errorf("%s çözümü %q", m, pattern)
		}
	}
}

func TestPutCosreSignalWeightsValidation(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		name string
		body string
		code int
	}{
		{"bozuk JSON", `{`, http.StatusBadRequest},
		{"bilinmeyen alan", `{"evidence_relevant":0.1,"x":1}`, http.StatusBadRequest},
		{"ilgili > 0.5", `{"evidence_relevant":0.6,"evidence_irrelevant":-0.1,"note":0}`, http.StatusBadRequest},
		{"ilgisiz pozitif", `{"evidence_relevant":0.1,"evidence_irrelevant":0.1,"note":0}`, http.StatusBadRequest},
		{"geçerli, depo yok", `{"evidence_relevant":0.2,"evidence_irrelevant":-0.2,"note":0}`, http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		s.putCosreSignalWeights(w, httptest.NewRequest(http.MethodPut, "/api/admin/cosre/signal-weights", strings.NewReader(c.body)))
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d (%s)", c.name, w.Code, c.code, w.Body.String())
		}
	}
}

func TestGetCosreSignalWeightsDefaultsWithoutStore(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).getCosreSignalWeights(w, httptest.NewRequest(http.MethodGet, "/api/admin/cosre/signal-weights", nil))
	var got cosrelearn.SignalWeights
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got != cosrelearn.DefaultSignalWeights() {
		t.Fatalf("varsayılanlar beklendi: %s (%v)", w.Body.String(), err)
	}
}
