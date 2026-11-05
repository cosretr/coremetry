package api

// ml_baseline.go — v0.10.1814, öğrenme döngüsü Faz 1 dilim 1.3 (audit
// docs/anomaly/learning-loop-audit.md §4.2): günlük baseline işinin elle
// koşusu (doğrulama için).
//
//   POST /api/ml/baseline/run?day=YYYY-MM-DD — admin, audit ml.baseline.run
//   POST /api/ml/baseline/backfill {days, signals} — admin, audit ml.baseline.backfill
//        ?estimate=1 → yalnız plan (backend başına sorgu sayısı + süre), yazım/audit yok
//   GET  /api/ml/baseline/backfill — her rol (viewer ilerlemeyi salt-okur görür)
//   DELETE /api/ml/baseline/backfill — admin, audit ml.baseline.backfill.cancel
//
// v0.10.1815: geçmiş doldurma ASLA otomatik değil (prod ES/VM maliyeti);
// yalnız bu POST başlatır, koşu lider pod'da (ml.Recorder.maybeBackfill).
//
// İş ML lider pod'unda koşar (sre rolü, ML kilidi): uç işi KENDİSİ koşmaz,
// `ml_baseline_control` blobuna istek yazar, lider 30 sn içinde alır ve
// sonucu `ml_baseline_status.manual`'a yazar (/api/ml/scorecard `baseline`
// alanında görünür). Neden kendisi koşmuyor: istek api pod'una düşer; ikinci
// bir yazıcı durum blobunu ezer ve lider kilidi dışında CH taraması açardı.
// 202 = sıraya alındı. ML ana anahtarı ve `ml.baseline.enabled` kapalıyken
// 409 (lider döngüsü isteği zaten almaz). api.go BÜYÜMEZ: kayıt init()'te.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-baseline", (*Server).registerMLBaselineRoutes) }

func (s *Server) registerMLBaselineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ml/baseline/run", auth.RequireRole(auth.RoleAdmin, s.postMLBaselineRun))
	mux.HandleFunc("POST /api/ml/baseline/backfill", auth.RequireRole(auth.RoleAdmin, s.postMLBaselineBackfill))
	mux.HandleFunc("GET /api/ml/baseline/backfill", auth.RequireAnyRole(mlReadRoles, s.getMLBaselineBackfill))
	mux.HandleFunc("DELETE /api/ml/baseline/backfill", auth.RequireRole(auth.RoleAdmin, s.deleteMLBaselineBackfill))
}

// newMLRequestID — elle istek kimliği (lider aynı isteği iki kez koşmaz).
func newMLRequestID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Server) postMLBaselineRun(w http.ResponseWriter, r *http.Request) {
	day := strings.TrimSpace(r.URL.Query().Get("day"))
	if day == "" && r.Body != nil {
		var body struct {
			Day string `json:"day"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			day = strings.TrimSpace(body.Day)
		}
	}
	ctx := r.Context()
	cfg := s.store.GetMLConfig(ctx)
	if !cfg.BaselineOn() {
		writeJSONError(w, http.StatusConflict, "ML ana anahtarı ve baseline anahtarı (Settings › Anomaly › ML) açık olmalı")
		return
	}
	if err := ml.BaselineManualDayOK(day, time.Now(), ml.CalendarLocation(s.store.AnomalySensitivityForDetectors())); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctl, _, err := s.store.GetMLBaselineControl(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	who := ""
	if c := auth.FromContext(ctx); c != nil {
		who = c.Email
	}
	ctl.Run = &chstore.MLBaselineRunRequest{ID: newMLRequestID(), Day: day, By: who, At: time.Now().Unix()}
	if err := s.store.SaveMLBaselineControl(ctx, ctl); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, "ml:")
	details, _ := json.Marshal(map[string]string{"day": day, "requestId": ctl.Run.ID})
	s.audit(r, "ml.baseline.run", "ml_baseline", day, string(details))
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "day": day, "requestId": ctl.Run.ID})
}

// writeJSONStatus — başlık durumdan ÖNCE (writeJSON yalnız 200 yazar).
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// mlBaselineBackfillTTL — ilerleme okuması (system_settings nokta okuması);
// FE 10 sn'de bir yoklar, yazımlar "ml:" önekini düşürür.
const mlBaselineBackfillTTL = 5 * time.Second

// mlBackfillPlan — tahmin/başlangıç için ortak plan.
func (s *Server) mlBackfillPlan(cfg chstore.MLConfig, days int, signals []string) ml.BaselineBackfillPlan {
	_, esOK := logstore.Unwrap(s.logs).(logstore.MLLogAggregator)
	vmOK := s.vmetrics != nil && s.vmetrics.Configured()
	return ml.PlanBaselineBackfill(cfg, days, signals, esOK, vmOK, len(anomaly.REDFamilyPins()), ml.Observability().ESP95Ms)
}

func (s *Server) postMLBaselineBackfill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Days    int      `json:"days"`
		Signals []string `json:"signals"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if body.Days < 1 || body.Days > ml.BaselineBackfillMaxDays {
		writeJSONError(w, http.StatusBadRequest, "days 1–56 olmalı")
		return
	}
	signals := ml.NormalizeBackfillSignals(body.Signals)
	if len(signals) == 0 {
		writeJSONError(w, http.StatusBadRequest, "en az bir sinyal seçilmeli: log, trace, metric")
		return
	}
	ctx := r.Context()
	cfg := s.store.GetMLConfig(ctx)
	plan := s.mlBackfillPlan(cfg, body.Days, signals)
	if r.URL.Query().Get("estimate") == "1" {
		writeJSON(w, map[string]any{"plan": plan})
		return
	}
	if !cfg.BaselineOn() {
		writeJSONError(w, http.StatusConflict, "ML ana anahtarı ve baseline anahtarı (Settings › Anomaly › ML) açık olmalı")
		return
	}
	if plan.ES+plan.VM == 0 {
		writeJSONError(w, http.StatusConflict, "seçili sinyallerin hiçbiri doldurulamıyor (plan nedenlerine bak)")
		return
	}
	ctl, _, err := s.store.GetMLBaselineControl(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cur, ok, _ := s.store.GetMLBaselineBackfill(ctx); ok && ctl.Backfill != nil && !ctl.Backfill.Cancel && cur.ID == ctl.Backfill.ID && !cur.Complete && !cur.Cancelled {
		writeJSONError(w, http.StatusConflict, "süren bir geçmiş doldurma var; önce iptal et")
		return
	}
	who := ""
	if c := auth.FromContext(ctx); c != nil {
		who = c.Email
	}
	ctl.Backfill = &chstore.MLBaselineBackfillRequest{ID: newMLRequestID(), Days: body.Days, Signals: signals, By: who, At: time.Now().Unix()}
	if err := s.store.SaveMLBaselineControl(ctx, ctl); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, "ml:")
	details, _ := json.Marshal(map[string]any{"requestId": ctl.Backfill.ID, "days": body.Days, "signals": signals, "es": plan.ES, "vm": plan.VM, "estimatedSec": plan.EstimatedSec})
	s.audit(r, "ml.baseline.backfill", "ml_baseline", ctl.Backfill.ID, string(details))
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"queued": true, "requestId": ctl.Backfill.ID, "plan": plan})
}

func (s *Server) getMLBaselineBackfill(w http.ResponseWriter, r *http.Request) {
	// girdisiz uç: anahtar sabit (tek blob okuması)
	s.serveCached(w, r, "ml:baseline:backfill:v1", mlBaselineBackfillTTL, func(ctx context.Context) (any, error) {
		out := map[string]any{"status": nil, "request": nil}
		if st, ok, err := s.store.GetMLBaselineBackfill(ctx); err != nil {
			return nil, err
		} else if ok {
			out["status"] = st
		}
		if ctl, _, err := s.store.GetMLBaselineControl(ctx); err == nil && ctl.Backfill != nil {
			out["request"] = ctl.Backfill
		}
		return out, nil
	})
}

func (s *Server) deleteMLBaselineBackfill(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctl, _, err := s.store.GetMLBaselineControl(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	if ctl.Backfill == nil || ctl.Backfill.Cancel {
		writeJSONError(w, http.StatusConflict, "iptal edilecek geçmiş doldurma yok")
		return
	}
	ctl.Backfill.Cancel = true
	if err := s.store.SaveMLBaselineControl(ctx, ctl); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(ctx, "ml:")
	s.audit(r, "ml.baseline.backfill.cancel", "ml_baseline", ctl.Backfill.ID, `{"cancel":true}`)
	writeJSON(w, map[string]any{"cancelled": true, "requestId": ctl.Backfill.ID})
}
