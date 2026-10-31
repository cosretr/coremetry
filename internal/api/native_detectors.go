package api

// native_detectors.go — v0.10.1746: yerleşik dedektör anahtarları
// (chstore/native_detectors.go). api.go BÜYÜMEZ: registerRoutesExtra.
//
//   GET /api/admin/native-detectors — admin
//   PUT /api/admin/native-detectors — admin; audit eski/yeni; bir sonraki
//                                     tikte geçerli (15 sn önbellek), restart yok.

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("native-detectors", (*Server).registerNativeDetectorRoutes) }

func (s *Server) registerNativeDetectorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/native-detectors", auth.RequireRole(auth.RoleAdmin, s.getNativeDetectors))
	mux.HandleFunc("PUT /api/admin/native-detectors", auth.RequireRole(auth.RoleAdmin, s.putNativeDetectors))
}

func (s *Server) getNativeDetectors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"names": chstore.NativeDetectorNames, "detectors": s.store.GetNativeDetectors(r.Context()).Detectors})
}

func (s *Server) putNativeDetectors(w http.ResponseWriter, r *http.Request) {
	var c chstore.NativeDetectorsConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c = chstore.NormalizeNativeDetectors(c)
	ctx := context.WithoutCancel(r.Context())
	before := s.store.GetNativeDetectors(ctx)
	if err := s.store.SaveNativeDetectors(ctx, c); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"before": before.Detectors, "after": c.Detectors})
	s.audit(r, "native_detectors.config", "settings", "native_detectors", string(details))
	writeJSON(w, map[string]any{"names": chstore.NativeDetectorNames, "detectors": c.Detectors})
}
