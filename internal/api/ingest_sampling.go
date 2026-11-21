package api

// ingest_sampling.go — v0.10.2430: upstream-sampling weight settings + the
// admin verification card (docs/architecture/sampling-weight.md).
//
//   GET /api/admin/ingest-sampling           → settings + this pod's tally (admin)
//   PUT /api/admin/ingest-sampling           → admin · audit settings.ingest_sampling.update
//   GET /api/admin/ingest-sampling/stored?window_s=300
//                                            → weight distribution of stored spans,
//                                              last ≤ 15 min (admin, opt-in, cached 30 s)
//   GET /api/admin/ingest-sampling/rule-shadow → v0.10.2441: stored vs model-weighted
//                                              firing of span error-rate rules
//                                              (shadow) + retune values (admin)
//
// The tally is POD-LOCAL (counts since this process started, the
// otlp_convert_degrades contract): on a split install the api pod converts
// nothing, so the card says so and offers the stored read, which is
// fleet-wide.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/otlp"
)

func init() { registerRoutesExtra("ingest-sampling", (*Server).registerIngestSamplingRoutes) }

func (s *Server) registerIngestSamplingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/ingest-sampling", auth.RequireRole(auth.RoleAdmin, s.getIngestSampling))
	mux.HandleFunc("PUT /api/admin/ingest-sampling", auth.RequireRole(auth.RoleAdmin, s.putIngestSampling))
	mux.HandleFunc("GET /api/admin/ingest-sampling/stored", auth.RequireRole(auth.RoleAdmin, s.getIngestSamplingStored))
	mux.HandleFunc("GET /api/admin/ingest-sampling/rule-shadow", auth.RequireRole(auth.RoleAdmin, s.getIngestSamplingRuleShadow))
}

type ingestSamplingPod struct {
	Host       string                `json:"host"`
	IngestRole bool                  `json:"ingestRole"`
	Tally      []otlp.WeightTallyRow `json:"tally"`
	Overflow   uint64                `json:"overflow"`
}

type ingestSamplingResponse struct {
	Settings     chstore.IngestSampling `json:"settings"`
	ColumnStored bool                   `json:"columnStored"`
	Pod          ingestSamplingPod      `json:"pod"`
}

func (s *Server) getIngestSampling(w http.ResponseWriter, _ *http.Request) {
	host, _ := os.Hostname()
	tally, overflow := otlp.SampleWeightTally()
	resp := ingestSamplingResponse{
		Settings: chstore.CurrentIngestSampling(),
		Pod:      ingestSamplingPod{Host: host, IngestRole: !s.roleIngestOff, Tally: tally, Overflow: overflow},
	}
	if s.store != nil {
		resp.ColumnStored = s.store.HasSampleWeightCol()
	}
	writeJSON(w, resp)
}

// ingestSamplingStore — store seam (tests pass a fake).
type ingestSamplingStore interface {
	SaveIngestSampling(ctx context.Context, c chstore.IngestSampling) (chstore.IngestSampling, error)
}

var ingestSamplingStoreOf = func(s *Server) ingestSamplingStore {
	if s.store == nil {
		return nil
	}
	return s.store
}

func (s *Server) putIngestSampling(w http.ResponseWriter, r *http.Request) {
	st := ingestSamplingStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "no settings store")
		return
	}
	var in chstore.IngestSampling
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	prev := chstore.CurrentIngestSampling()
	in.UpdatedBy, in.UpdatedAt = "", time.Time{}
	if c := auth.FromContext(r.Context()); c != nil {
		in.UpdatedBy = firstNonEmpty(c.Email, c.UserID)
	}
	in.UpdatedAt = time.Now().UTC()
	saved, err := st.SaveIngestSampling(r.Context(), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetIngestSampling(saved)
	strip := func(c chstore.IngestSampling) chstore.IngestSampling {
		c.UpdatedBy, c.UpdatedAt = "", time.Time{}
		return c
	}
	details, _ := json.Marshal(map[string]any{"before": strip(prev), "after": strip(saved)})
	s.audit(r, "settings.ingest_sampling.update", "settings", chstore.IngestSamplingKey, string(details))
	writeJSON(w, saved)
}

// ingestSamplingWindow — SAF: window_s → [60, 900], default 300.
func ingestSamplingWindow(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 300
	}
	return min(max(n, 60), int(chstore.MaxSampleWeightWindow/time.Second))
}

func (s *Server) getIngestSamplingStored(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "no store")
		return
	}
	win := ingestSamplingWindow(r.URL.Query().Get("window_s"))
	key := "admin:ingest-sampling:stored:window=" + strconv.Itoa(win)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		to := time.Now()
		rows, err := s.store.StoredSampleWeights(ctx, to.Add(-time.Duration(win)*time.Second), to)
		if err != nil {
			return nil, err
		}
		return map[string]any{"windowS": win, "rows": rows}, nil
	})
}

// ingestSamplingRuleShadowResponse — v0.10.2441.
type ingestSamplingRuleShadowResponse struct {
	Mode    string                      `json:"mode"`
	ModelOK bool                        `json:"modelOk"`
	Model   chstore.SamplingShadowModel `json:"model"`
	State   chstore.RuleShadowState     `json:"state"`
	Hints   []chstore.RuleRetuneHint    `json:"hints"`
}

// getIngestSamplingRuleShadow — the persisted shadow counts (written by the
// evaluator leader) + retune values from the CURRENT rules and model. Two
// small reads (one settings blob, the rule list); nothing on spans.
func (s *Server) getIngestSamplingRuleShadow(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "no store")
		return
	}
	cfg := chstore.CurrentIngestSampling()
	model, ok := chstore.SamplingShadowModelFor(cfg)
	resp := ingestSamplingRuleShadowResponse{Mode: cfg.Mode, ModelOK: ok, Model: model, Hints: []chstore.RuleRetuneHint{}}
	st, err := s.store.LoadRuleShadow(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	resp.State = st
	if ok {
		rules, err := s.store.ListAlertRules(r.Context())
		if err != nil {
			writeErr(w, err)
			return
		}
		resp.Hints = chstore.RuleRetuneHints(rules, model)
	}
	writeJSON(w, resp)
}
