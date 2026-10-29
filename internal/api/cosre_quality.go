package api

// cosre_quality.go — CoSRE öğrenme Faz 2.
//
//   POST /api/ai/signal   — istemcinin örtük sinyali (copy / link_click /
//                           evidence_click / problem_note / followup_click
//                           — v0.10.1291; evidence_relevant /
//                           evidence_irrelevant / note — v0.10.1597).
//                           Kimliği doğrulanmış
//                           her kullanıcı (sohbet edebilen sinyal üretir; 👍/👎
//                           ucu emsal). Sinyal kullanıcı içeriği değil, telemetri:
//                           audit satırı YAZILMAZ (her kopya tıkı audit_log'u
//                           şişirirdi; ai_feedback emsali).
//   GET  /api/ai/quality  — admin: haftanın en kötü 20 cevabı + en kötü tool'lar
//                           (gece puanı, internal/cosrelearn).
//
// Kaynak sinyallerin çoğu türetilir (yeniden sorma, iptal, tool hatası,
// kanıtsız cevap…): gece işi cosre_interactions'tan hesaplar.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

const cosreSignalDetailMax = 512

// cosreNoteDetailMax — v0.10.1597: mesaj düzeyinde serbest not daha uzun
// olabilir (gövde tavanı 4 KiB'ın içinde kalır).
const cosreNoteDetailMax = 2048

// cosreEvidenceDetailRE — evidence_* sinyalinin ayrıntısı: kartın adım
// kimliği (uistream toolCallId / data-step id = s<i>).
var cosreEvidenceDetailRE = regexp.MustCompile(`^s[0-9]{1,4}$`)

func (s *Server) registerCosreQualityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/ai/signal", s.postCosreSignal)
	mux.HandleFunc("GET /api/ai/quality", auth.RequireRole(auth.RoleAdmin, s.getCosreQuality))
	// Faz 4 — en kötü cevaplardan regresyon vakası dosyası (cosre_quality_evalset.go).
	mux.HandleFunc("GET /api/ai/quality/evalset", auth.RequireRole(auth.RoleAdmin, s.exportCosreQualityEvalset))
}

type cosreSignalRequest struct {
	ExchangeID string `json:"exchangeId"`
	Kind       string `json:"kind"`
	Detail     string `json:"detail,omitempty"`
}

// validateCosreSignal — SAF: kapalı tür kümesi + kimlik/ayrıntı tavanı.
func validateCosreSignal(req cosreSignalRequest) (cosreSignalRequest, error) {
	req.ExchangeID, req.Kind = strings.TrimSpace(req.ExchangeID), strings.TrimSpace(req.Kind)
	switch {
	case req.ExchangeID == "":
		return req, errors.New("exchangeId required")
	case len(req.ExchangeID) > aiFeedbackMaxIDLen:
		return req, errors.New("exchangeId too long")
	case !cosrelearn.ClientSignalKinds[req.Kind]:
		return req, fmt.Errorf("unknown kind %q", req.Kind)
	}
	req.Detail = strings.TrimSpace(req.Detail)
	switch req.Kind {
	case cosrelearn.SigEvidenceRelevant, cosrelearn.SigEvidenceIrrelevant:
		if !cosreEvidenceDetailRE.MatchString(req.Detail) {
			return req, fmt.Errorf("detail must be a step id (s<i>) for %s", req.Kind)
		}
	case cosrelearn.SigNote:
		req.Detail = clipBytes(req.Detail, cosreNoteDetailMax)
	default:
		req.Detail = clipBytes(req.Detail, cosreSignalDetailMax)
	}
	return req, nil
}

func (s *Server) postCosreSignal(w http.ResponseWriter, r *http.Request) {
	var req cosreSignalRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	req, err := validateCosreSignal(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	email := ""
	if c := auth.FromContext(r.Context()); c != nil {
		email = c.Email
	}
	if err := s.store.InsertCosreSignal(r.Context(), chstore.CosreSignal{
		ExchangeID: req.ExchangeID, Kind: req.Kind, Detail: req.Detail, UserEmail: email,
	}); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type cosreQualityView struct {
	Days    int                        `json:"days"`
	Answers []chstore.CosreWorstAnswer `json:"answers"`
	Tools   []chstore.CosreWorstTool   `json:"tools"`
}

// cosreQualityDays — SAF: ?days= 1..30, varsayılan 7.
func cosreQualityDays(raw string) int {
	d, err := strconv.Atoi(raw)
	if err != nil || d <= 0 {
		return 7
	}
	if d > 30 {
		return 30
	}
	return d
}

func (s *Server) getCosreQuality(w http.ResponseWriter, r *http.Request) {
	days := cosreQualityDays(r.URL.Query().Get("days"))
	key := fmt.Sprintf("cosre:quality:d=%d", days)
	s.serveCached(w, r, key, 5*time.Minute, func(ctx context.Context) (any, error) {
		from := time.Now().UTC().AddDate(0, 0, -days)
		answers, err := s.store.WorstCosreAnswers(ctx, from, 20)
		if err != nil {
			return nil, err
		}
		tools, err := s.store.WorstCosreTools(ctx, from, 5, 20)
		if err != nil {
			return nil, err
		}
		if answers == nil {
			answers = []chstore.CosreWorstAnswer{}
		}
		if tools == nil {
			tools = []chstore.CosreWorstTool{}
		}
		return cosreQualityView{Days: days, Answers: answers, Tools: tools}, nil
	})
}
