package api

// sampling_correction.go — v0.10.2435, sampling-weight Phase 2
// (docs/architecture/sampling-weight.md). GET /api/sampling/correction tells
// the UI whether span-derived numbers are sampling-corrected and from when,
// per surface family; the page compares the switch point with its own window
// and shows "sampling-corrected" / "older part uncorrected". Process-local
// (settings + markers refresh every 30 s), no CH read, so it is cheap enough
// for every page that shows RED numbers. Read-only, every role.

import (
	"net/http"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("sampling-correction", (*Server).registerSamplingCorrectionRoutes) }

func (s *Server) registerSamplingCorrectionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sampling/correction", s.getSamplingCorrection)
}

func (s *Server) getSamplingCorrection(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "private, max-age=30")
	writeJSON(w, chstore.CurrentSamplingCorrection())
}
