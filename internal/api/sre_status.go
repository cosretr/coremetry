package api

// sre_status.go — v0.10.1282 (AI SRE Faz 0a: sre rolü, kuyruk, drain, durum).
//
//   GET  /api/sre/status                  → {enabled, launcher, roleSeen, pods[], queueDepth, running}
//   GET  /api/problems/{id}/investigation → {state none|queued|running|done|partial|shared|error, position?, investigation?}
//   POST /api/problems/{id}/investigation → elle "Araştır / Yeniden araştır" (editor+, audit problem.investigation.enqueue)
//        v0.10.1288: gövde {kind:"verify", hypothesis:"<tür>"} → "Doğrula" (hipotezin derin okumaları)
//   GET  /api/settings/sre                → sre_agent ayarı (admin)
//   PUT  /api/settings/sre                → (admin, audit settings.sre_agent.update)
//
// Okumalar rol kapısız — viewer araştırmayı ve kuyruk durumunu GÖRÜR. Kayıt
// tek FINAL nokta okuması (saved_views ORDER BY id), durum bir Redis SCAN +
// iki sınırlı liste/sayım. serveCached 5 s / 10 s; anahtarlar girdinin
// tamamını taşır (durum ucunun girdisi yok, kayıt ucunun tek girdisi id).
// Drain / üretici her yazımda PublishSRE ile önce bu anahtarları düşürür, sonra
// SSE "sre.investigation" basar — FE refetch'i bayat önbelleğe düşmez.
//
// Neden ayrı dosya: api.go büyümez (TestApiGoDoesNotGrow); defter kaydı.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre", (*Server).registerSRERoutes) }

func (s *Server) registerSRERoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sre/status", s.getSREStatus)
	mux.HandleFunc("GET /api/problems/{id}/investigation", s.getProblemInvestigation)
	mux.Handle("POST /api/problems/{id}/investigation",
		auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.postProblemInvestigation)))
	mux.HandleFunc("GET /api/settings/sre", auth.RequireRole(auth.RoleAdmin, s.getSRESettings))
	mux.HandleFunc("PUT /api/settings/sre", auth.RequireRole(auth.RoleAdmin, s.putSRESettings))
}

const (
	sreStatusTTL        = 5 * time.Second
	sreInvestigationTTL = 10 * time.Second
	sreStatusKey        = "sre:status"
)

// :v2: — v0.10.2301 (sunucu metinleri S4) kayıt gövdesine metin anahtarları
// eklendi (summaryMsg, textMsg, labelMsg, gateMsg); dil anahtara GİRMEZ.
func sreInvestigationKey(problemID string) string { return "sre:inv:v2:" + problemID }

// sreAPIStore — uçların okumaları/yazmaları (*chstore.Store karşılar);
// testte sahte depo (problemLogTemplatesStoreOf emsali).
type sreAPIStore interface {
	sre.QueueStore
	sre.SettingsStore
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	// EnrichProblemsForRead — deploy → öncelik zinciri (enrichProblemsForRead
	// ile aynı; TestNoBarePriorityEnrich).
	EnrichProblemsForRead(ctx context.Context, probs []chstore.Problem, lookback time.Duration) []chstore.Problem
}

var sreStoreOf = func(s *Server) sreAPIStore { return s.store }

// PublishSRE — drain / üretici yazımının bildirimi: önce önbellek anahtarları
// (L2 + L1 + eş pod'lar), sonra SSE. main.go Publish olarak bağlar.
func (s *Server) PublishSRE(kind string, payload any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if m, ok := payload.(map[string]any); ok {
		if id, _ := m["problemId"].(string); id != "" {
			s.cacheInvalidate(ctx, sreInvestigationKey(id))
		}
		if run, _ := m["evalRun"].(string); run != "" { // v0.10.1301 — değerlendirme koşusu ilerledi
			s.cacheInvalidate(ctx, sreEvalRunKey(run))
			s.cacheInvalidate(ctx, sreEvalRunsKey)
		}
	}
	s.cacheInvalidate(ctx, sreStatusKey)
	if s.bus != nil {
		s.bus.Publish(kind, payload)
	}
}

// sreStatusResponse — FE: SREStatus.
type sreStatusResponse struct {
	Enabled    bool            `json:"enabled"`
	Launcher   string          `json:"launcher"` // deployment | inprocess | none
	RoleSeen   bool            `json:"roleSeen"` // son 60 s'de drain kalp atışı
	Pods       []sre.Heartbeat `json:"pods"`
	QueueDepth int             `json:"queueDepth"`
	Running    int             `json:"running"`
}

// buildSREStatus — SAF: parçalar → cevap.
func buildSREStatus(cfg sre.Settings, launcherEnv string, local *sre.Heartbeat, pods []sre.Heartbeat, queued, running int) sreStatusResponse {
	if pods == nil {
		pods = []sre.Heartbeat{}
	}
	if local != nil {
		seen := false
		for _, p := range pods {
			if p.Pod == local.Pod {
				seen = true
			}
		}
		if !seen {
			pods = append(pods, *local)
		}
	}
	return sreStatusResponse{
		Enabled:    cfg.IsEnabled(),
		Launcher:   sre.ResolveLauncher(local != nil, launcherEnv, pods),
		RoleSeen:   len(pods) > 0,
		Pods:       pods,
		QueueDepth: queued,
		Running:    running,
	}
}

func (s *Server) getSREStatus(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, sreStatusKey, sreStatusTTL, func(ctx context.Context) (any, error) {
		st := sreStoreOf(s)
		queued, err := st.CountSREQueued(ctx)
		if err != nil {
			return nil, err
		}
		runningRows, err := st.ListSREInvestigations(ctx, []string{sre.StateRunning}, 0)
		if err != nil {
			return nil, err
		}
		var raw [][]byte
		if s.cache != nil {
			raw, _ = s.cache.ScanPrefix(ctx, sre.HeartbeatPrefix) // hata = kalp atışı görünmüyor
		}
		var local *sre.Heartbeat
		if h, ok := sre.Local(); ok {
			local = &h
		}
		return buildSREStatus(sre.CurrentSettings(), os.Getenv("COREMETRY_SRE_LAUNCHER"), local,
			sre.ParseHeartbeats(raw, time.Now()), queued, len(runningRows)), nil
	})
}

// problemInvestigationResponse — FE: ProblemInvestigationResponse.
type problemInvestigationResponse struct {
	State         string             `json:"state"` // none | queued | running | done | partial | shared | error | skipped
	Position      int                `json:"position,omitempty"`
	Enabled       bool               `json:"enabled"`
	Investigation *sre.Investigation `json:"investigation,omitempty"`
}

// queuePosition — SAF: problemin kuyruktaki sırası (1 tabanlı; drain'in alış
// sırasıyla aynı: elle → P1 → P2 → diğer, eşitlikte en eski). Yoksa 0.
func queuePosition(rows []chstore.SavedView, problemID string) int {
	var invs []sre.Investigation
	for _, r := range rows {
		if inv, ok := sre.Decode(r); ok && inv.State == sre.StateQueued {
			invs = append(invs, inv)
		}
	}
	for i, inv := range sre.OrderQueued(invs) {
		if inv.QueueKey() == problemID { // v0.10.1301 — değerlendirme tekrarı (aynı problemId) canlı sırayı göstermez
			return i + 1
		}
	}
	return 0
}

func (s *Server) getProblemInvestigation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem kimliği zorunlu")
		return
	}
	s.serveCached(w, r, sreInvestigationKey(id), sreInvestigationTTL, func(ctx context.Context) (any, error) {
		st := sreStoreOf(s)
		out := problemInvestigationResponse{State: "none", Enabled: sre.CurrentSettings().IsEnabled()}
		inv, ok, err := sre.Load(ctx, st, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return out, nil
		}
		out.State = inv.State
		out.Investigation = &inv
		if inv.State == sre.StateQueued {
			rows, err := st.ListSREInvestigations(ctx, []string{sre.StateQueued}, 0)
			if err == nil {
				out.Position = queuePosition(rows, id)
			}
		}
		return out, nil
	})
}

func (s *Server) postProblemInvestigation(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	// v0.10.1288 — isteğe bağlı gövde: {kind:"verify", hypothesis} = "Doğrula".
	// Boş gövde = bugünkü elle yeniden araştırma.
	var body struct {
		Kind       string `json:"kind"`
		Hypothesis string `json:"hypothesis"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	kind := sre.KindManual
	switch strings.TrimSpace(body.Kind) {
	case "", sre.KindManual:
	case sre.KindVerify:
		if !sre.IsHypothesisKind(body.Hypothesis) {
			writeJSONError(w, http.StatusBadRequest, "bilinmeyen hipotez türü")
			return
		}
		kind = sre.KindVerify
	default:
		writeJSONError(w, http.StatusBadRequest, "kind yalnız manual | verify")
		return
	}
	st := sreStoreOf(s)
	p, err := st.GetProblem(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p == nil {
		writeJSONError(w, http.StatusNotFound, "problem bulunamadı")
		return
	}
	if p.Service == "" || chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
		writeJSONError(w, http.StatusBadRequest, "AI SRE şimdilik yalnız servis problemlerini araştırır")
		return
	}
	enriched := st.EnrichProblemsForRead(r.Context(), []chstore.Problem{*p}, problemDeployLookback)[0]
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
	}
	res, err := sre.Enqueue(r.Context(), st, sre.EnqueueRequest{
		ProblemID: p.ID, DisplayID: enriched.DisplayID, Service: p.Service, Kind: kind,
		Priority: enriched.Priority, ProblemStartedAt: p.StartedAt, By: by, Hypothesis: body.Hypothesis,
	}, sre.CurrentSettings())
	switch {
	case errors.Is(err, sre.ErrDisabled):
		writeJSONError(w, http.StatusConflict, "AI SRE kapalı — yönetici Ayarlar → AI SRE (/settings/ai-sre) bölümünden açabilir")
		return
	case errors.Is(err, sre.ErrQueueFull):
		writeJSONError(w, http.StatusTooManyRequests, "kuyruk dolu — sre rolü koşuyor mu?")
		return
	case errors.Is(err, sre.ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	if res.Created {
		det := map[string]any{"state": res.State, "service": p.Service, "kind": kind}
		if kind == sre.KindVerify {
			det["hypothesis"] = body.Hypothesis
		}
		details, _ := json.Marshal(det)
		s.audit(r, "problem.investigation.enqueue", "problem", p.ID, string(details))
		s.PublishSRE(sre.EventKind, map[string]any{"problemId": p.ID, "state": res.State})
	}
	writeJSON(w, res)
}

func (s *Server) getSRESettings(w http.ResponseWriter, r *http.Request) {
	c, err := sre.GetSettings(r.Context(), sreStoreOf(s))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

func (s *Server) putSRESettings(w http.ResponseWriter, r *http.Request) {
	c := sre.DefaultSettings()
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if cl := auth.FromContext(r.Context()); cl != nil {
		c.UpdatedBy = cl.Email
	}
	c.UpdatedAt = time.Now().UnixNano()
	saved, err := sre.SaveSettings(r.Context(), sreStoreOf(s), c)
	if err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"enabled": saved.IsEnabled(), "autoPriorities": saved.Priorities(),
		"maxPerHour": saved.MaxPerHour, "maxConcurrent": saved.MaxConcurrent, "maxQueue": saved.MaxQueue,
		"notifySummary": saved.NotifySummaryOn(), "graphOrigin": saved.GraphOriginOn(), "impactEvents": saved.ImpactEventsOn(),
		"similarCases": saved.SimilarCasesOn()})
	s.audit(r, "settings.sre_agent.update", "settings", sre.SettingsKey, string(details))
	s.cacheInvalidate(r.Context(), sreStatusKey)
	writeJSON(w, saved)
}
