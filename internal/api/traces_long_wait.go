package api

// traces_long_wait.go — v0.10.2466 (operatör kararı 2026-11-22): "Daha uzun
// bekle".
//
// Genel okuma bütçesi 25 sn AYNEN kalır. Traces listesi bütçeyi aşınca
// kısmi sonuç + not döner (chstore/trace_partial.go); not "Daha uzun bekle"
// düğmesi sunar → YALNIZ o liste isteği ?wait=long ile yeniden koşar:
// 60 sn, admin tavanıyla sınırlı (etkin = min(60, tavan)). Tavan
// system_settings 'traces_long_wait' blob'unda, 25–180; boot'ta hidre edilir,
// 30 sn'de bir yenilenir (trace_root_def kablosu).
//
//   GET /api/settings/traces-long-wait   (tüm roller: not düğmesi süreyi yazar)
//   PUT /api/settings/traces-long-wait   (admin; audit settings.update)
//
// Cache anahtarı bütçeyi taşır (tracesLongWaitKeySuffix): kısa ve uzun
// bütçeli cevaplar ayrı; tavan değişince uzun anahtar da değişir.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("traces-long-wait", (*Server).registerTracesLongWaitRoutes) }

func (s *Server) registerTracesLongWaitRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings/traces-long-wait", s.getTracesLongWait)
	mux.Handle("PUT /api/settings/traces-long-wait",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putTracesLongWait)))
}

// LoadTracesLongWait — boot hidrasyonu + yenileme tiki.
func (s *Server) LoadTracesLongWait(ctx context.Context) {
	chstore.SetLongWaitCap(s.store.GetLongWaitCapSetting(ctx))
}

// parseTracesWait — SAF: "" → 0 (varsayılan bütçe); "long" → min(60, tavan);
// başka değer hata (sessiz yok sayma yok — bütçe açık bir istektir).
func parseTracesWait(v string, capSec int) (int, error) {
	switch strings.TrimSpace(v) {
	case "":
		return 0, nil
	case "long":
		return chstore.EffectiveLongWaitSec(capSec), nil
	}
	return 0, fmt.Errorf("wait must be empty or 'long'")
}

// tracesLongWaitKeySuffix — SAF: varsayılan bütçede boş (anahtar eski,
// dağıtımda soğuk cache yok); uzun bütçede ":lw=<sn>".
func tracesLongWaitKeySuffix(sec int) string {
	if sec <= 0 {
		return ""
	}
	return fmt.Sprintf(":lw=%d", sec)
}

// tracesLongWaitTotal — SAF: uzun bekleyen liste isteğinin toplam son tarihi
// (bütçe + 20 sn pay: basamak + kök/ekstra okumaları; istemci 150 sn bekler).
func tracesLongWaitTotal(sec int) time.Duration {
	return time.Duration(sec)*time.Second + 20*time.Second
}

// tracesPartialPayload — SAF: yanıtın `partial` zarfı (kısmi değilse nil).
// limitSec = bu isteğin bütçesi; longWaitSec = düğmenin sunacağı bütçe
// (zaten uzun bütçeyle koşmuşsa 0 — düğme gizlenir).
func tracesPartialPayload(p chstore.TracePartial, elapsed time.Duration, usedLongSec int) map[string]any {
	if !p.Hit {
		return nil
	}
	limit := chstore.DefaultReadBudgetSec
	offer := chstore.EffectiveLongWaitSec(chstore.LongWaitCap())
	if usedLongSec > 0 {
		limit, offer = usedLongSec, 0
	}
	out := map[string]any{
		"reason":      "timeout",
		"elapsedMs":   elapsed.Milliseconds(),
		"limitSec":    limit,
		"longWaitSec": offer,
	}
	if !p.CoveredFrom.IsZero() {
		out["coveredFromNs"] = p.CoveredFrom.UnixNano()
	}
	return out
}

type tracesLongWaitPayload struct {
	MaxSec       int `json:"maxSec"`
	EffectiveSec int `json:"effectiveSec"`
	MinSec       int `json:"minSec"`
	LimitSec     int `json:"limitSec"`
	DefaultSec   int `json:"defaultSec"`
}

func tracesLongWaitResp(capSec int) tracesLongWaitPayload {
	return tracesLongWaitPayload{
		MaxSec: capSec, EffectiveSec: chstore.EffectiveLongWaitSec(capSec),
		MinSec: chstore.LongWaitCapMin, LimitSec: chstore.LongWaitCapMax,
		DefaultSec: chstore.DefaultReadBudgetSec,
	}
}

// getTracesLongWait — KAYITLI tavan (bu pod henüz yenilememiş olabilir).
func (s *Server) getTracesLongWait(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, tracesLongWaitResp(s.store.GetLongWaitCapSetting(r.Context())))
}

func (s *Server) putTracesLongWait(w http.ResponseWriter, r *http.Request) {
	var in struct {
		MaxSec int `json:"maxSec"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validateLongWaitCap(in.MaxSec); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.store.SaveLongWaitCapSetting(r.Context(), in.MaxSec); err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetLongWaitCap(in.MaxSec) // bu pod anında; diğerleri yenileme tikinde
	s.audit(r, "settings.update", "traces_long_wait", "traces_long_wait", fmt.Sprintf(`{"maxSec":%d}`, in.MaxSec))
	log.Printf("[settings] traces_long_wait: maxSec=%d", in.MaxSec)
	writeJSON(w, tracesLongWaitResp(in.MaxSec))
}

// validateLongWaitCap — SAF: PUT aralık dışını REDDEDER (kelepçe yalnız okumada).
func validateLongWaitCap(sec int) error {
	if sec < chstore.LongWaitCapMin || sec > chstore.LongWaitCapMax {
		return fmt.Errorf("maxSec must be between %d and %d", chstore.LongWaitCapMin, chstore.LongWaitCapMax)
	}
	return nil
}
