package api

import (
	"net/http/httptest"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLPrecisionStatuses(t *testing.T) {
	st := chstore.MLPrecisionState{Detectors: map[string]chstore.MLPrecisionDetState{
		"op_latency":    {Demoted: true, DemotedAt: 100, Reason: "30 günde 24 etiket, 6'sı doğru; üst sınır %45"},
		"error_rate":    {Demoted: true, Virtual: true, DemotedAt: 200},
		"op_error_rate": {RestoredAt: 50},
	}}
	cfg := chstore.DefaultMLPrecisionShadow()
	cfg.Pins = map[string]string{"template_spike": "on"}
	m, list := mlPrecisionStatuses(cfg, st)
	if m["op_latency"].Status != chstore.MLPrecStatusWouldDemote || m["op_latency"].Since != 100 || m["op_latency"].Reason == "" {
		t.Errorf("shadow kipinde düşerdi: %+v", m["op_latency"])
	}
	if m["error_rate"].Status != chstore.MLPrecStatusWouldDemote || m["op_error_rate"].Status != chstore.MLPrecStatusOn ||
		m["template_spike"].Status != chstore.MLPrecStatusPinned || len(list) != 2 || list[0] != "error_rate" {
		t.Errorf("durumlar: %+v %v", m, list)
	}
	cfg.Mode = chstore.MLPrecisionModeOn
	m, _ = mlPrecisionStatuses(cfg, st)
	if m["op_latency"].Status != chstore.MLPrecStatusShadow || m["error_rate"].Status != chstore.MLPrecStatusWouldDemote {
		t.Errorf("on kipinde gerçek kayıt gölgede, sanal düşerdi: %+v", m)
	}
	cfg.Mode = chstore.MLPrecisionModeOff
	if m, list = mlPrecisionStatuses(cfg, st); m["op_latency"].Status != chstore.MLPrecStatusOn || len(list) != 0 {
		t.Errorf("off kipinde rozet yok: %+v", m)
	}
}

func TestMLPrecisionOnTransitionGuard(t *testing.T) {
	mk := func(mode string) chstore.MLConfig {
		c := chstore.NormalizeMLConfig(chstore.MLConfig{})
		c.PrecisionShadow = &chstore.MLPrecisionShadow{Mode: mode}
		return chstore.NormalizeMLConfig(c)
	}
	sh, on, off := mk(chstore.MLPrecisionModeShadow), mk(chstore.MLPrecisionModeOn), mk(chstore.MLPrecisionModeOff)
	if !mlPrecisionOnTransition(sh, on) || !mlPrecisionOnTransition(off, on) || mlPrecisionOnTransition(on, on) ||
		mlPrecisionOnTransition(on, sh) || mlPrecisionOnTransition(sh, off) {
		t.Fatal("yalnız on'a geçiş onay ister")
	}
	// Geçiş değilse koruma store'a dokunmadan geçer (Server{} boş).
	s := &Server{}
	w := httptest.NewRecorder()
	if v, ok := s.mlPrecisionOnGuard(w, httptest.NewRequest("PUT", "/api/admin/ml", nil), sh, off); !ok || v != nil {
		t.Errorf("geçiş yok: %v %v", v, ok)
	}
	s.mlPrecisionOnAudit(httptest.NewRequest("PUT", "/api/admin/ml", nil), nil) // nil rapor: no-op
}
