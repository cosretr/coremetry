package api

// cosre_signal_weights.go — operatör kararı 2026-10-29 §1: CoSRE kart
// "ilgili / ilgisiz" ve mesaj notu ağırlıkları system_settings("cosre")
// blobunun signals.weights yolunda (cosrelearn/signal_weights.go).
//
//   GET /api/admin/cosre/signal-weights → {evidence_relevant, evidence_irrelevant}  (admin)
//   PUT /api/admin/cosre/signal-weights {…}                                        (admin + audit)
//
// Okuyan tek yer gece kalite işi (worker, her koşuda okur) — istek yolunda
// okunmadığı için memo/cache yok. PUT katıdır (işaret + |w| ≤ 0.25, note
// yalnız 0 ya da yok — v0.10.1605; aksi 400);
// okuma hoşgörülüdür (bozuk blob → varsayılanlar). "cosre" blobunun diğer
// anahtarları korunur (MergeSignalWeights).

import (
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func init() {
	registerRoutesExtra("cosre-signal-weights", (*Server).registerCosreSignalWeightRoutes)
}

const cosreSignalWeightsMaxBody = 4 << 10

func (s *Server) registerCosreSignalWeightRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/admin/cosre/signal-weights", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getCosreSignalWeights)))
	mux.Handle("PUT /api/admin/cosre/signal-weights", auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putCosreSignalWeights)))
}

func (s *Server) getCosreSignalWeights(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, cosrelearn.DefaultSignalWeights())
		return
	}
	weights, err := cosrelearn.LoadSignalWeights(r.Context(), s.store)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, weights)
}

func (s *Server) putCosreSignalWeights(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, cosreSignalWeightsMaxBody)
	var in cosrelearn.SignalWeightsInput
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	weights, err := cosrelearn.ValidateSignalWeights(in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu yok")
		return
	}
	existing, err := s.store.GetSetting(r.Context(), cosrelearn.SignalSettingsKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := cosrelearn.MergeSignalWeights(existing, weights)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.store.PutSetting(r.Context(), cosrelearn.SignalSettingsKey, raw); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(weights)
	s.audit(r, "settings.cosre_signal_weights.update", "settings", cosrelearn.SignalSettingsKey, string(details))
	writeJSON(w, weights)
}
