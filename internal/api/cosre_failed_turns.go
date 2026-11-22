package api

// cosre_failed_turns.go — v0.10.2468 (operatör onayı 2026-11-22; DECISIONS
// "2026-11-22 — v0.10.2468"): CoSRE başarısız tur İNCELEME KUYRUĞU ve gerçek
// soruların değerlendirme kümesine terfisi.
//
//	GET  /api/ai/failed-turns?days=1|7|30     admin — bayraklı turlar + haftalık kümeler
//	POST /api/ai/failed-turns/promote         admin + requireCopilot, audit evalset.turn.promote
//
// Liste SAF dedektörü (cosrelearn.DetectFailedTurns) pencerenin turları ve
// sinyalleri üzerinde koşturur; saatlik süpürmenin sakladığı failed_turn
// bayrakları birleşir. Bayrak etiket DEĞİL: puana, aday seçimine ya da
// çalışma zamanı kararına girmez; 👍/👎 okunmaz.
//
// Terfi: herhangi bir tur (bayraklı olmak zorunda değil) operatörün yazdığı
// beklentiyle (kademe / rota, araçlardan biri, geçmesi ve geçmemesi gereken
// ifadeler) saved_views(page='cosre_evalset_candidate') satırı olur — mevcut
// aday akışı, yeni şema yok; Source "promoted", kabul edilmiş doğar. Servis
// adlarını yer tutucuya çevirmek YALNIZ operatör isterse (anonymize çiftleri);
// otomatik maskeleme yok. Anonimleştirilen turun ham cevabı saklanmaz.
// Terfi edilen vaka günlük koşuya girmez; yalnız admin'in "Gerçek sorular"
// koşusunda koşar (ai_evalset_real.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func init() { registerRoutesExtra("cosre-failed-turns", (*Server).registerFailedTurnRoutes) }

func (s *Server) registerFailedTurnRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/failed-turns", auth.RequireRole(auth.RoleAdmin, s.listFailedTurns))
	mux.HandleFunc("POST /api/ai/failed-turns/promote", auth.RequireRole(auth.RoleAdmin, s.requireCopilot(s.promoteFailedTurn)))
}

const (
	failedTurnsListMax     = 300
	failedTurnsClustersMax = 50
	evalPromotedIDPrefix   = "evp-"
	promoteListMax         = 20
	promoteItemMax         = 200
	promoteAnonMax         = 10
	promoteNoteMax         = 500
	// promoteLookback — terfi edilecek turun aranacağı pencere (tablo TTL'i).
	promoteLookback = 180 * 24 * time.Hour
)

// failedTurnStore — G/Ç dikişi (sunucuda *chstore.Store).
type failedTurnStore interface {
	ListCosreInteractionsForScoring(ctx context.Context, from, to time.Time) ([]chstore.CosreInteraction, error)
	ListCosreSignals(ctx context.Context, from, to time.Time) ([]chstore.CosreSignal, error)
}

var failedTurnStoreFor = func(s *Server) failedTurnStore {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// failedTurnsView — liste cevabı.
type failedTurnsView struct {
	Days        int                        `json:"days"`
	Turns       []cosrelearn.FailedTurn    `json:"turns"`
	Total       int                        `json:"total"`
	Scanned     int                        `json:"scanned"`
	Truncated   bool                       `json:"truncated"`
	ByReason    []cosrelearn.ReasonCount   `json:"byReason"`
	Clusters    []cosrelearn.FailedCluster `json:"clusters"`
	GeneratedAt int64                      `json:"generatedAt"`
}

// failedTurnsViewOf — SAF: dedektör + kümeler + sayaçlar (listeler sınırlı).
func failedTurnsViewOf(days int, rows []chstore.CosreInteraction, sigs []chstore.CosreSignal, now time.Time) failedTurnsView {
	turns := cosrelearn.DetectFailedTurns(rows, sigs)
	counts := map[string]int{}
	for _, t := range turns {
		for _, r := range t.Reasons {
			counts[r]++
		}
	}
	v := failedTurnsView{Days: days, Total: len(turns), Scanned: len(rows), ByReason: []cosrelearn.ReasonCount{},
		Clusters: cosrelearn.ClusterFailedTurns(turns), GeneratedAt: now.UnixNano()}
	for _, k := range cosrelearn.FailReasons {
		if counts[k] > 0 {
			v.ByReason = append(v.ByReason, cosrelearn.ReasonCount{Reason: k, Count: counts[k]})
		}
	}
	if len(turns) > failedTurnsListMax {
		turns, v.Truncated = turns[:failedTurnsListMax], true
	}
	v.Turns = turns
	if len(v.Clusters) > failedTurnsClustersMax {
		v.Clusters = v.Clusters[:failedTurnsClustersMax]
	}
	return v
}

func (s *Server) listFailedTurns(w http.ResponseWriter, r *http.Request) {
	// Gün SABİT basamaklardan — cache anahtarına giriyor (router-gaps emsali).
	days := 7
	switch r.URL.Query().Get("days") {
	case "1":
		days = 1
	case "30":
		days = 30
	}
	st := failedTurnStoreFor(s)
	if st == nil {
		writeJSON(w, failedTurnsViewOf(days, nil, nil, time.Now()))
		return
	}
	key := fmt.Sprintf("ai:failed-turns:v1:d=%d", days)
	s.serveCached(w, r, key, time.Minute, func(ctx context.Context) (any, error) {
		now := time.Now()
		from := now.Add(-time.Duration(days) * 24 * time.Hour)
		rows, err := st.ListCosreInteractionsForScoring(ctx, from, now)
		if err != nil {
			return nil, err
		}
		sigs, err := st.ListCosreSignals(ctx, from, now.Add(time.Minute))
		if err != nil {
			return nil, err
		}
		return failedTurnsViewOf(days, rows, sigs, now), nil
	})
}

// promoteTurnReq — terfi isteği; beklentiyi operatör yazar.
type promoteTurnReq struct {
	ExchangeID     string   `json:"exchangeId"`
	Question       string   `json:"question"`
	ExpectTier     string   `json:"expectTier"`
	ExpectRoute    string   `json:"expectRoute"`
	AnyOfTools     []string `json:"anyOfTools"`
	MustContain    []string `json:"mustContain"`
	MustNotContain []string `json:"mustNotContain"`
	// Anonymize — operatörün seçtiği ad → yer tutucu çiftleri (harf duyarsız).
	Anonymize   []promoteAnon `json:"anonymize"`
	BindService string        `json:"bindService"`
	Reasons     []string      `json:"reasons"`
	Note        string        `json:"note"`
}

type promoteAnon struct {
	From string `json:"from"`
	To   string `json:"to"`
}

var promoteSlotRe = regexp.MustCompile(`^[a-z_]{0,64}$`)

func promoteList(name string, in []string) ([]string, error) {
	if len(in) > promoteListMax {
		return nil, fmt.Errorf("%s en çok %d öğe", name, promoteListMax)
	}
	out := []string{}
	for _, x := range in {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if utf8.RuneCountInString(x) > promoteItemMax {
			return nil, fmt.Errorf("%s öğesi en çok %d karakter", name, promoteItemMax)
		}
		out = append(out, x)
	}
	return out, nil
}

// promoteAnonymize — SAF: çiftleri harf duyarsız uygular.
func promoteAnonymize(s string, pairs []promoteAnon) string {
	for _, p := range pairs {
		s = regexp.MustCompile(`(?i)`+regexp.QuoteMeta(p.From)).ReplaceAllLiteralString(s, p.To)
	}
	return s
}

// buildPromotedCandidate — SAF: istek + tur → kabul edilmiş terfi adayı.
func buildPromotedCandidate(req promoteTurnReq, inter chstore.CosreInteraction, v2Tools map[string]bool, who string, now time.Time) (evalsetCandidate, error) {
	q := strings.TrimSpace(req.Question)
	if q == "" {
		q = strings.TrimSpace(inter.Question)
	}
	if q == "" {
		return evalsetCandidate{}, fmt.Errorf("soru boş")
	}
	req.ExpectTier, req.ExpectRoute = strings.TrimSpace(req.ExpectTier), strings.TrimSpace(req.ExpectRoute)
	if !promoteSlotRe.MatchString(req.ExpectTier) || !promoteSlotRe.MatchString(req.ExpectRoute) {
		return evalsetCandidate{}, fmt.Errorf("expectTier / expectRoute küçük harf ve _ olmalı")
	}
	tools, err := promoteList("anyOfTools", req.AnyOfTools)
	if err != nil {
		return evalsetCandidate{}, err
	}
	for _, t := range tools {
		if !v2Tools[t] {
			return evalsetCandidate{}, fmt.Errorf("bilinmeyen araç: %q", t)
		}
	}
	must, err := promoteList("mustContain", req.MustContain)
	if err != nil {
		return evalsetCandidate{}, err
	}
	mustNot, err := promoteList("mustNotContain", req.MustNotContain)
	if err != nil {
		return evalsetCandidate{}, err
	}
	if len(req.Anonymize) > promoteAnonMax {
		return evalsetCandidate{}, fmt.Errorf("anonymize en çok %d çift", promoteAnonMax)
	}
	var pairs []promoteAnon
	for _, p := range req.Anonymize {
		p.From, p.To = strings.TrimSpace(p.From), strings.TrimSpace(p.To)
		if p.From == "" && p.To == "" {
			continue
		}
		if p.From == "" || p.To == "" || utf8.RuneCountInString(p.From) > promoteItemMax || utf8.RuneCountInString(p.To) > 64 {
			return evalsetCandidate{}, fmt.Errorf("anonymize çifti: ad ve yer tutucu dolu olmalı")
		}
		pairs = append(pairs, p)
	}
	bind := strings.TrimSpace(req.BindService)
	answer := inter.Answer
	if len(pairs) > 0 {
		q = promoteAnonymize(q, pairs)
		for i := range must {
			must[i] = promoteAnonymize(must[i], pairs)
		}
		for i := range mustNot {
			mustNot[i] = promoteAnonymize(mustNot[i], pairs)
		}
		answer = "" // ham cevap gerçek adları taşır — operatör anonimleştirmeyi seçti
		if bind == "" && len(pairs) == 1 {
			bind = pairs[0].To // tek yer tutucu: koşucu onu canlı servise bağlar
		}
	}
	known := map[string]bool{}
	for _, k := range cosrelearn.FailReasons {
		known[k] = true
	}
	reasons := []string{}
	for _, r := range req.Reasons {
		if known[r] {
			reasons = append(reasons, r)
		}
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > promoteNoteMax {
		return evalsetCandidate{}, fmt.Errorf("not en çok %d karakter", promoteNoteMax)
	}
	return evalsetCandidate{
		ID: evalPromotedIDPrefix + req.ExchangeID, ExchangeID: req.ExchangeID, Day: now.UTC().Format("2006-01-02"),
		Question: q, Answer: truncRunes(answer, evalCandidateAnswerMax), Signals: reasons, Tier: inter.Tier,
		AnyOfTools: tools, MustNotContain: mustNot, MustContain: must, ExpectTier: req.ExpectTier, ExpectRoute: req.ExpectRoute,
		BindService: bind, Anonymized: len(pairs) > 0, Note: note, Source: evalCandidateSourcePromoted,
		CreatedAt: now.UTC().Format(time.RFC3339), Accepted: true, AcceptedBy: who, AcceptedAt: now.UTC().Format(time.RFC3339),
	}, nil
}

func (s *Server) promoteFailedTurn(w http.ResponseWriter, r *http.Request) {
	var req promoteTurnReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	req.ExchangeID = strings.TrimSpace(req.ExchangeID)
	if req.ExchangeID == "" || len(req.ExchangeID) > aiFeedbackMaxIDLen {
		writeJSONError(w, http.StatusBadRequest, "exchangeId gerekli")
		return
	}
	st := evalCandidateStoreFor(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "depolama yok")
		return
	}
	now := time.Now()
	found, err := st.CosreInteractionsForEval(r.Context(), []string{req.ExchangeID}, now.Add(-promoteLookback))
	if err != nil {
		writeErr(w, err)
		return
	}
	inter, ok := found[req.ExchangeID]
	if !ok {
		writeJSONError(w, http.StatusNotFound, "tur bulunamadı")
		return
	}
	who := ""
	if cl := auth.FromContext(r.Context()); cl != nil {
		who = cl.Email
		if who == "" {
			who = cl.UserID
		}
	}
	c, err := buildPromotedCandidate(req, inter, evalV2ToolNames(), who, now)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	created := now.UnixNano()
	if cur, gerr := st.GetSavedView(r.Context(), c.ID); gerr != nil {
		writeErr(w, gerr)
		return
	} else if cur != nil {
		created = cur.CreatedAt // yeniden terfi = beklentiyi düzenleme; sıra korunur
	}
	row, err := evalCandidateRow(c, created)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := st.UpsertSavedView(r.Context(), row); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "evalset.turn.promote", "ai_evalset_candidate", c.ID,
		fmt.Sprintf("exchange=%s anonymized=%v route=%s tier=%s tools=%d", c.ExchangeID, c.Anonymized, c.ExpectRoute, c.ExpectTier, len(c.AnyOfTools)))
	writeJSON(w, map[string]any{"candidate": c, "caseId": evalPromotedCasePrefix + c.ExchangeID})
}
