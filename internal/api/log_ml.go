package api

// log_ml.go — ES log ML katmanı dilim 0 uçları (docs/audits/log-ml-audit.md).
// api.go BÜYÜMEZ: registerRoutesExtra ile kayıt.
//
//   GET /api/log-ml/events?since=24h&detector=&service=&stage=  — her rol
//   GET /api/log-ml/scorecard                                   — her rol (30 g)
//   PUT /api/log-ml/{id}/verdict  {verdict, note?, detector, service} — editor+
//   GET /api/admin/log-ml                                       — admin
//   PUT /api/admin/log-ml                                       — admin (audit)
//
// Karar anomaly_verdicts'e 'logml:'+id anahtarıyla yazılır (mevcut altyapı);
// kind 'log_ml:<detector>' — eski anomali kararlarıyla parmak izi çakışmaz.
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
	"github.com/cilcenk/coremetry/internal/logml"
)

func init() { registerRoutesExtra("log-ml", (*Server).registerLogMLRoutes) }

var logMLReadRoles = []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}

const (
	logMLEventsTTL    = 30 * time.Second
	logMLScorecardTTL = 60 * time.Second
	logMLScoreWindow  = 30 * 24 * time.Hour
)

func (s *Server) registerLogMLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/log-ml/events", auth.RequireAnyRole(logMLReadRoles, s.getLogMLEvents))
	mux.HandleFunc("GET /api/log-ml/scorecard", auth.RequireAnyRole(logMLReadRoles, s.getLogMLScorecard))
	mux.HandleFunc("PUT /api/log-ml/{id}/verdict", auth.RequireAnyRole(editorRoles, s.putLogMLVerdict))
	mux.HandleFunc("GET /api/admin/log-ml", auth.RequireRole(auth.RoleAdmin, s.getLogMLConfig))
	mux.HandleFunc("PUT /api/admin/log-ml", auth.RequireRole(auth.RoleAdmin, s.putLogMLConfig))
}

// withLogMLStats — getSystemStats yanıtına `logMl` bloğu (api.go tek satır).
func withLogMLStats(st *chstore.SystemStats) *chstore.SystemStats {
	if st != nil {
		st.LogML = logml.Observability()
	}
	return st
}

// logMLEventsKey — SAF: tüm girdiler anahtarda (since kovası, süzgeçler).
func logMLEventsKey(sinceS int64, detector, service, stage string) string {
	return fmt.Sprintf("logml:events:v1:since=%d:d=%s:s=%s:st=%s", sinceS, detector, service, stage)
}

func (s *Server) getLogMLEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := 24 * time.Hour
	if d, err := time.ParseDuration(q.Get("since")); err == nil && d > 0 && d <= 30*24*time.Hour {
		since = d
	}
	detector, service, stage := strings.TrimSpace(q.Get("detector")), strings.TrimSpace(q.Get("service")), strings.TrimSpace(q.Get("stage"))
	key := logMLEventsKey(int64(since.Seconds()), detector, service, stage)
	s.serveCached(w, r, key, logMLEventsTTL, func(ctx context.Context) (any, error) {
		items, err := s.store.ListLogMLEvents(ctx, chstore.LogMLEventsFilter{
			Since: time.Now().Add(-since), Detector: detector, Service: service, Stage: stage, Limit: chstore.LogMLListCap})
		if err != nil {
			return nil, err
		}
		if items == nil {
			items = []chstore.LogMLEvent{}
		}
		return map[string]any{"items": items, "truncated": len(items) >= chstore.LogMLListCap}, nil
	})
}

func (s *Server) getLogMLScorecard(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "logml:scorecard:v1:30d", logMLScorecardTTL, func(ctx context.Context) (any, error) {
		rows, err := s.store.LogMLScorecard(ctx, time.Now().Add(-logMLScoreWindow))
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []chstore.LogMLDetectorScore{}
		}
		cfg := s.store.GetLogMLConfig(ctx)
		return map[string]any{"windowDays": 30, "detectors": rows, "promotion": cfg.Promotion}, nil
	})
}

func (s *Server) putLogMLVerdict(w http.ResponseWriter, r *http.Request) {
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
	kind := "log_ml:" + strings.TrimSpace(body.Detector)
	v := chstore.AnomalyVerdict{
		EventID: chstore.LogMLVerdictID(id), Fingerprint: verdictFingerprint(kind, id, body.Service),
		Kind: kind, Pattern: id, Service: body.Service,
		Verdict: body.Verdict, Note: truncRunes(strings.TrimSpace(body.Note), 500),
		CreatedBy: claimEmail(auth.FromContext(r.Context())), CreatedAt: time.Now().UnixNano(),
	}
	if err := s.store.UpsertAnomalyVerdict(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "logml:")
	details, _ := json.Marshal(map[string]string{"verdict": body.Verdict, "detector": body.Detector, "service": body.Service, "note": v.Note})
	s.audit(r, "logml.verdict", "log_ml_event", id, string(details))
	writeJSON(w, v)
}

func (s *Server) getLogMLConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.GetLogMLConfig(r.Context()))
}

func (s *Server) putLogMLConfig(w http.ResponseWriter, r *http.Request) {
	var c chstore.LogMLConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c = chstore.NormalizeLogMLConfig(c)
	before := s.store.GetLogMLConfig(r.Context())
	if err := s.store.SaveLogMLConfig(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "logml:")
	// audit: kim (claim), ne zaman (audit damgası), eski/yeni değer.
	details, _ := json.Marshal(map[string]any{"before": before, "after": c})
	s.audit(r, "logml.config", "settings", "log_ml", string(details))
	writeJSON(w, c)
}
