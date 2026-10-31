package api

// log_ml.go — ES log ML katmanı dilim 0 uçları (docs/audits/ml-audit.md).
// api.go BÜYÜMEZ: registerRoutesExtra ile kayıt.
//
//   GET /api/ml/events?since=24h&signal=&detector=&service=&stage= — her rol
//   GET /api/ml/scorecard                                   — her rol (30 g)
//   PUT /api/ml/{id}/verdict  {verdict, note?, detector, service} — editor+
//   GET /api/admin/ml                                       — admin
//   PUT /api/admin/ml                                       — admin (audit)
//
// Karar anomaly_verdicts'e 'ml:'+id anahtarıyla yazılır (mevcut altyapı);
// kind 'ml:<detector>' — eski anomali kararlarıyla parmak izi çakışmaz.
// Terfi (promotion) dilim 0'da yalnız AYAR: hiçbir dedektör satırı
// anomaly_events'e kopyalanmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml", (*Server).registerMLRoutes) }

var mlReadRoles = []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}

const (
	mlEventsTTL    = 30 * time.Second
	mlScorecardTTL = 60 * time.Second
	mlScoreWindow  = 30 * 24 * time.Hour
)

func (s *Server) registerMLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/events", auth.RequireAnyRole(mlReadRoles, s.getMLEvents))
	mux.HandleFunc("GET /api/ml/scorecard", auth.RequireAnyRole(mlReadRoles, s.getMLScorecard))
	mux.HandleFunc("PUT /api/ml/{id}/verdict", auth.RequireAnyRole(editorRoles, s.putMLVerdict))
	mux.HandleFunc("GET /api/admin/ml", auth.RequireRole(auth.RoleAdmin, s.getMLConfig))
	mux.HandleFunc("PUT /api/admin/ml", auth.RequireRole(auth.RoleAdmin, s.putMLConfig))
}

// withMLStats — getSystemStats yanıtına `ml` bloğu (api.go tek satır).
func withMLStats(st *chstore.SystemStats) *chstore.SystemStats {
	if st != nil {
		st.ML = ml.Observability()
	}
	return st
}

// mlEventsKey — SAF: tüm girdiler anahtarda (since kovası, süzgeçler).
func mlEventsKey(sinceS int64, signal, detector, service, stage string) string {
	return fmt.Sprintf("ml:events:v2:since=%d:sig=%s:d=%s:s=%s:st=%s", sinceS, signal, detector, service, stage)
}

func (s *Server) getMLEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := 24 * time.Hour
	if d, err := time.ParseDuration(q.Get("since")); err == nil && d > 0 && d <= 30*24*time.Hour {
		since = d
	}
	signal, detector, service, stage := strings.TrimSpace(q.Get("signal")), strings.TrimSpace(q.Get("detector")), strings.TrimSpace(q.Get("service")), strings.TrimSpace(q.Get("stage"))
	if signal != "" && signal != chstore.MLSignalLog && signal != chstore.MLSignalTrace {
		signal = ""
	}
	key := mlEventsKey(int64(since.Seconds()), signal, detector, service, stage)
	s.serveCached(w, r, key, mlEventsTTL, func(ctx context.Context) (any, error) {
		items, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{
			Since: time.Now().Add(-since), Signal: signal, Detector: detector, Service: service, Stage: stage, Limit: chstore.MLListCap})
		if err != nil {
			return nil, err
		}
		if items == nil {
			items = []chstore.MLEvent{}
		}
		return map[string]any{"items": items, "truncated": len(items) >= chstore.MLListCap}, nil
	})
}

func (s *Server) getMLScorecard(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "ml:scorecard:v1:30d", mlScorecardTTL, func(ctx context.Context) (any, error) {
		rows, err := s.store.MLScorecard(ctx, time.Now().Add(-mlScoreWindow))
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []chstore.MLDetectorScore{}
		}
		cfg := s.store.GetMLConfig(ctx)
		signals := map[string]string{}
		for _, n := range chstore.MLDetectorNames {
			signals[n] = chstore.MLDetectorSignal(n)
		}
		return map[string]any{"windowDays": 30, "detectors": rows, "promotion": cfg.Promotion, "signals": signals}, nil
	})
}

func (s *Server) putMLVerdict(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Verdict  string `json:"verdict"`
		Note     string `json:"note"`
		Detector string `json:"detector"`
		Service  string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if id == "" || !chstore.ValidVerdict(body.Verdict) {
		writeJSONError(w, http.StatusBadRequest, "verdict 'anomaly' ya da 'not_anomaly' olmalı")
		return
	}
	kind := "ml:" + strings.TrimSpace(body.Detector)
	v := chstore.AnomalyVerdict{
		EventID: chstore.MLVerdictID(id), Fingerprint: verdictFingerprint(kind, id, body.Service),
		Kind: kind, Pattern: id, Service: body.Service,
		Verdict: body.Verdict, Note: truncRunes(strings.TrimSpace(body.Note), 500),
		CreatedBy: claimEmail(auth.FromContext(r.Context())), CreatedAt: time.Now().UnixNano(),
	}
	if err := s.store.UpsertAnomalyVerdict(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "ml:")
	details, _ := json.Marshal(map[string]string{"verdict": body.Verdict, "detector": body.Detector, "service": body.Service, "note": v.Note})
	s.audit(r, "ml.verdict", "ml_event", id, string(details))
	writeJSON(w, v)
}

func (s *Server) getMLConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.GetMLConfig(r.Context()))
}

func (s *Server) putMLConfig(w http.ResponseWriter, r *http.Request) {
	var c chstore.MLConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c = chstore.NormalizeMLConfig(c)
	before := s.store.GetMLConfig(r.Context())
	if err := s.store.SaveMLConfig(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "ml:")
	// audit: kim (claim), ne zaman (audit damgası), eski/yeni değer.
	details, _ := json.Marshal(map[string]any{"before": before, "after": c})
	s.audit(r, "ml.config", "settings", "ml", string(details))
	writeJSON(w, c)
}
