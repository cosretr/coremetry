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
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("native-detectors", (*Server).registerNativeDetectorRoutes) }

func (s *Server) registerNativeDetectorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/native-detectors", auth.RequireRole(auth.RoleAdmin, s.getNativeDetectors))
	mux.HandleFunc("PUT /api/admin/native-detectors", auth.RequireRole(auth.RoleAdmin, s.putNativeDetectors))
	mux.HandleFunc("POST /api/admin/native-detectors/off-all", auth.RequireRole(auth.RoleAdmin, s.postNativeDetectorsOffAll))
}

func (s *Server) nativeView(ctx context.Context) map[string]any {
	return map[string]any{"names": chstore.NativeDetectorNames, "detectors": s.store.GetNativeDetectors(ctx).Detectors}
}

func (s *Server) getNativeDetectors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.nativeView(r.Context()))
}

// postNativeDetectorsOffAll — A3: tek tıkla eski dedektörlerin hepsi kapalı.
func (s *Server) postNativeDetectorsOffAll(w http.ResponseWriter, r *http.Request) {
	nat := chstore.AllNativeOff()
	before := s.store.GetNativeDetectors(r.Context())
	if err := s.store.SaveNativeDetectors(r.Context(), nat); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"before": before.Detectors, "after": nat.Detectors})
	s.audit(r, "native_detectors.off_all", "settings", "native_detectors", string(details))
	s.cacheInvalidatePrefix(r.Context(), "anomaly:")
	writeJSON(w, s.nativeView(r.Context()))
}

// traceOpHits — Anomalies "Trace operation anomalies" kartı (api.go
// getTraceOpAnomalies): yerleşik trace_op anahtarı kapalıyken canlı tarama
// YAPILMAZ, kart boş (A3: kapalı anahtara rağmen kart canlı spike gösteriyordu).
func (s *Server) traceOpHits(ctx context.Context, window time.Duration) ([]anomaly.TraceOpAnomaly, error) {
	if !s.store.GetNativeDetectors(ctx).On(chstore.NativeTraceOp) {
		return nil, nil
	}
	return anomaly.DetectTraceOpAnomalies(ctx, s.store, window)
}

func (s *Server) putNativeDetectors(w http.ResponseWriter, r *http.Request) {
	var c struct {
		chstore.NativeDetectorsConfig
	}
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c.NativeDetectorsConfig = chstore.NormalizeNativeDetectors(c.NativeDetectorsConfig)
	ctx := context.WithoutCancel(r.Context())
	before := s.store.GetNativeDetectors(ctx)
	if err := s.store.SaveNativeDetectors(ctx, c.NativeDetectorsConfig); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"before": before.Detectors, "after": c.Detectors})
	s.audit(r, "native_detectors.config", "settings", "native_detectors", string(details))
	s.cacheInvalidatePrefix(ctx, "anomaly:")
	writeJSON(w, s.nativeView(ctx))
}
