package api

// sre_replay.go — CoSRE doğruluk ölçümü Faz A, dilim A2:
// "Kök neden" etiketli Problem'lerde SRE araştırmasının replay notlaması.
//
//	GET  /api/sre/replay/reports        → son 20 rapor (Problem satırları YOK)
//	     + özet + zamanlama; viewer görür (eval koşuları gibi), serveCached 15 s.
//	GET  /api/sre/replay/reports/{id}   → rapor + özet + Problem başına
//	     ortalama / yayılım; viewer görür.
//	POST /api/sre/replay/reports        → admin, audit sre.replay.run:
//	     {days (7|30|90|180), repeats 1–3, limit 1–30, variant:{contextPack, plain}}
//	     409 = ajan kapalı / rapor sürüyor.
//	PUT  /api/sre/replay/schedule       → admin, audit sre.replay.schedule:
//	     {weekly, days, repeats} (system_settings "sre_agent.replay"; kapalı varsayılan).
//
// Notlama bu uçlarda DEĞİL: sre pod'undaki lider kilitli tik
// (sre.StartReplayGrader) yerel hakemi çağırır. Bu yüzden requireCopilot
// arkasında değil — istek yolunda LLM çağrısı yok. Tekrar canlı kaydı
// yazmaz, bildirim üretmez (sre/replay_report.go). Yeni uç = yeni dosya
// (TestApiGoDoesNotGrow).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre-replay", (*Server).registerSREReplayRoutes) }

func (s *Server) registerSREReplayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sre/replay/reports", s.listSREReplayReports)
	mux.HandleFunc("GET /api/sre/replay/reports/{id}", s.getSREReplayReport)
	mux.Handle("POST /api/sre/replay/reports",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.postSREReplayReport)))
	mux.Handle("PUT /api/sre/replay/schedule",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putSREReplaySchedule)))
}

const (
	sreReplayTTL      = 15 * time.Second
	sreReplayListKey  = "sre:replay:reports"
	sreReplayListSize = 20
)

func sreReplayReportKey(id string) string { return "sre:replay:report:" + id }

var (
	sreReplayStoreOf    = func(s *Server) sre.ReplayStore { return s.store }
	sreReplaySettingsOf = func(s *Server) sre.SettingsStore { return s.store }
)

// sreReplayListItem — FE: SREReplayListItem (Problem satırları yok).
type sreReplayListItem struct {
	sre.ReplayReport
	Summary sre.ReplaySummary `json:"summary"`
}

// sreReplayListResponse — FE: SREReplayListResponse.
type sreReplayListResponse struct {
	Reports  []sreReplayListItem `json:"reports"`
	Schedule sre.ReplaySchedule  `json:"schedule"`
	Enabled  bool                `json:"enabled"`
	Grades   map[string]int      `json:"grades"` // not → puan (FE ölçek satırı)
}

// sreReplayItemView — FE: SREReplayItem (+ okuma anında ortalama / yayılım).
type sreReplayItemView struct {
	sre.ReplayItem
	Mean     *float64 `json:"mean"`
	Spread   int      `json:"spread"`
	Unstable bool     `json:"unstable,omitempty"`
}

// sreReplayDetail — FE: SREReplayDetail.
type sreReplayDetail struct {
	sre.ReplayReport
	Items   []sreReplayItemView `json:"items"`
	Summary sre.ReplaySummary   `json:"summary"`
}

func replayGradeScale() map[string]int {
	out := map[string]int{}
	for _, g := range sre.ReplayGrades {
		out[g], _ = sre.GradeScore(g)
	}
	return out
}

// replayDetailOf — SAF: rapor → ayrıntı görünümü.
func replayDetailOf(rep sre.ReplayReport) sreReplayDetail {
	d := sreReplayDetail{ReplayReport: rep, Summary: sre.SummarizeReplay(rep), Items: make([]sreReplayItemView, 0, len(rep.Items))}
	for _, it := range rep.Items {
		if it.Runs == nil {
			it.Runs = []sre.ReplayRun{}
		}
		mean, spread, _, unstable := sre.ItemStats(it)
		d.Items = append(d.Items, sreReplayItemView{ReplayItem: it, Mean: mean, Spread: spread, Unstable: unstable})
	}
	return d
}

func (s *Server) listSREReplayReports(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, sreReplayListKey, sreReplayTTL, func(ctx context.Context) (any, error) {
		reps, err := sre.ListReplayReports(ctx, sreReplayStoreOf(s), sreReplayListSize)
		if err != nil {
			return nil, err
		}
		sc, err := sre.GetReplaySchedule(ctx, sreReplaySettingsOf(s))
		if err != nil {
			return nil, err
		}
		out := sreReplayListResponse{Reports: make([]sreReplayListItem, 0, len(reps)), Schedule: sc,
			Enabled: sre.CurrentSettings().IsEnabled(), Grades: replayGradeScale()}
		for _, rep := range reps {
			sm := sre.SummarizeReplay(rep)
			rep.Items = nil
			out.Reports = append(out.Reports, sreReplayListItem{ReplayReport: rep, Summary: sm})
		}
		return out, nil
	})
}

func (s *Server) getSREReplayReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !sre.ValidEvalRunID(id) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz rapor kimliği")
		return
	}
	s.serveCached(w, r, sreReplayReportKey(id), sreReplayTTL, func(ctx context.Context) (any, error) {
		rep, ok, err := sre.LoadReplayReport(ctx, sreReplayStoreOf(s), id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errNotFound
		}
		return replayDetailOf(rep), nil
	})
}

func (s *Server) postSREReplayReport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Days    int               `json:"days"`
		Repeats int               `json:"repeats"`
		Limit   int               `json:"limit"`
		Variant sre.ReplayVariant `json:"variant"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if in.Days < 0 || in.Repeats < 0 || in.Repeats > sre.ReplayMaxRepeats || in.Limit < 0 || in.Limit > sre.ReplayMaxProblems {
		writeJSONError(w, http.StatusBadRequest, "repeats 1–3, limit 1–30 olmalı")
		return
	}
	by := ""
	if c := auth.FromContext(r.Context()); c != nil {
		by = c.Email
	}
	rep, err := sre.StartReplayReport(r.Context(), sreReplayStoreOf(s), sre.ReplayReportRequest{Days: in.Days, Repeats: in.Repeats,
		Limit: in.Limit, Variant: in.Variant, Trigger: "manual", By: by, Build: s.buildVersion}, sre.CurrentSettings())
	switch {
	case errors.Is(err, sre.ErrDisabled):
		writeJSONError(w, http.StatusConflict, "AI SRE kapalı — yönetici Ayarlar → AI SRE (/settings/ai-sre) bölümünden açabilir")
		return
	case errors.Is(err, sre.ErrReplayBusy):
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeErr(w, err)
		return
	}
	queued := 0
	for _, it := range rep.Items {
		for _, run := range it.Runs {
			if run.State == sre.StateQueued {
				queued++
			}
		}
	}
	details, _ := json.Marshal(map[string]any{"reportId": rep.ReportID, "days": rep.Days, "repeats": rep.Repeats,
		"limit": rep.Limit, "found": rep.Found, "queued": queued, "variant": rep.Variant, "build": rep.Build})
	s.audit(r, "sre.replay.run", "sre-replay", rep.ReportID, string(details))
	s.cacheInvalidate(r.Context(), sreReplayListKey)
	s.cacheInvalidate(r.Context(), sreStatusKey)
	writeJSON(w, replayDetailOf(rep))
}

func (s *Server) putSREReplaySchedule(w http.ResponseWriter, r *http.Request) {
	var in sre.ReplaySchedule
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if c := auth.FromContext(r.Context()); c != nil {
		in.UpdatedBy = c.Email
	}
	in.UpdatedAt = time.Now().UnixNano()
	saved, err := sre.SaveReplaySchedule(r.Context(), sreReplaySettingsOf(s), in)
	if err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"weekly": saved.Weekly, "days": saved.Days, "repeats": saved.Repeats})
	s.audit(r, "sre.replay.schedule", "settings", sre.ReplayScheduleKey, string(details))
	s.cacheInvalidate(r.Context(), sreReplayListKey)
	writeJSON(w, saved)
}
