package api

// ml_baseline.go — v0.10.1814, öğrenme döngüsü Faz 1 dilim 1.3 (audit
// docs/anomaly/learning-loop-audit.md §4.2): günlük baseline işinin elle
// koşusu (doğrulama için).
//
//   POST /api/ml/baseline/run?day=YYYY-MM-DD — admin, audit ml.baseline.run
//
// İş ML lider pod'unda koşar (sre rolü, ML kilidi): uç işi KENDİSİ koşmaz,
// `ml_baseline_control` blobuna istek yazar, lider 30 sn içinde alır ve
// sonucu `ml_baseline_status.manual`'a yazar (/api/ml/scorecard `baseline`
// alanında görünür). Neden kendisi koşmuyor: istek api pod'una düşer; ikinci
// bir yazıcı durum blobunu ezer ve lider kilidi dışında CH taraması açardı.
// 202 = sıraya alındı. ML ana anahtarı ve `ml.baseline.enabled` kapalıyken
// 409 (lider döngüsü isteği zaten almaz). api.go BÜYÜMEZ: kayıt init()'te.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-baseline", (*Server).registerMLBaselineRoutes) }

func (s *Server) registerMLBaselineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ml/baseline/run", auth.RequireRole(auth.RoleAdmin, s.postMLBaselineRun))
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
