package api

// problem_rootcause_label.go — Problem detayında "Kök neden"
// etiketi (CoSRE doğruluk ölçümü Faz A, dilim A1).
//
//	GET /api/problems/{id}/root-cause-label → viewer görür:
//	    {label: rootCauseLabel|null, suggested, categories}
//	PUT /api/problems/{id}/root-cause-label → editor+, audit
//	    problem.rootcause.set: {cause, category, culprit?, link?, resolution?}
//
// Etiket = ekibin elle girdiği "gerçekte ne oldu" — ölçümün TEK doğruluk
// kaynağı. Neden incident_cases: SRE değerlendirmesi (sre/eval.go) doğruluk
// etiketini zaten YALNIZ onaylı incident_cases satırından okur; ayrı bir
// "etiket" deposu iki gerçeği yarıştırırdı. PUT vaka yoksa (cosrelearn
// taslağı kurmadıysa — açık ya da kısa Problem) Problem satırından vakayı
// KURAR, varsa taşıyarak günceller (tam-satır replace, invariant 4) ve
// onaylı yazar — /ai "Onay bekleyen vakalar" ile aynı veri, aynı kategori
// kümesi (cosrelearn.CaseCategories). incident_cases'te yeri olmayan suçlu
// bileşen + bağlantı saved_views ekinde (chstore/problem_rootcause.go).
//
// Neden Problem triage düğmeleri ("Gerçek problem / Problem değil") DEĞİL:
// onlar gürültü kararıdır, kök neden etiketi değil (karar D4). 👍/👎 da
// buraya hiç girmez (vote_runtime_guard_test.go).
//
// Neden ayrı dosya: yeni uç = yeni dosya (TestApiGoDoesNotGrow). Neden
// requireCopilot ARKASINDA DEĞİL: etiket AI kapalıyken de yazılır; tek LLM
// çağrısı mevcut judgeSuggestion (öneri varsa ve metin değiştiyse, yoksa
// -1 "bilinmiyor"). Önbellek yok: tek satır okuması, düşük trafik.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func init() { registerRoutesExtra("problem-rootcause-label", (*Server).registerRootCauseLabelRoutes) }

func (s *Server) registerRootCauseLabelRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/root-cause-label", s.getRootCauseLabel)
	mux.Handle("PUT /api/problems/{id}/root-cause-label",
		auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.putRootCauseLabel)))
}

const (
	rootCauseCulpritMax = 200
	rootCauseLinkMax    = 500
)

// rootCauseLabelStore — uçların store dikişi (*chstore.Store karşılar).
type rootCauseLabelStore interface {
	GetProblem(ctx context.Context, id string) (*chstore.Problem, error)
	GetIncidentCase(ctx context.Context, caseID string) (*chstore.IncidentCase, error)
	UpsertIncidentCases(ctx context.Context, cases []chstore.IncidentCase) error
	GetProblemRootCauseExtra(ctx context.Context, problemID string) (*chstore.ProblemRootCauseExtra, error)
	PutProblemRootCauseExtra(ctx context.Context, e chstore.ProblemRootCauseExtra) error
}

var rootCauseLabelStoreFor = func(s *Server) rootCauseLabelStore { return s.store }

// rootCauseLabel — FE: RootCauseLabel (lib/types.ts).
type rootCauseLabel struct {
	ProblemID  string `json:"problemId"`
	CaseID     string `json:"caseId"`
	Cause      string `json:"cause"`
	Category   string `json:"category"`
	Culprit    string `json:"culprit,omitempty"`
	Link       string `json:"link,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	By         string `json:"by,omitempty"`
	At         int64  `json:"at"` // unix ms
}

// rootCauseLabelResponse — FE: RootCauseLabelResponse.
type rootCauseLabelResponse struct {
	Label *rootCauseLabel `json:"label"`
	// Suggested — taslak vakanın CoSRE önerisi (formu doldurur; olgu değil).
	Suggested  string   `json:"suggested,omitempty"`
	Categories []string `json:"categories"`
}

type rootCauseLabelInput struct {
	Cause      string `json:"cause"`
	Category   string `json:"category"`
	Culprit    string `json:"culprit"`
	Link       string `json:"link"`
	Resolution string `json:"resolution"`
}

// validateRootCauseLabel — SAF: zorunlu neden, kapalı kategori kümesi, alan
// tavanları, tek satır suçlu, yalnız http(s) bağlantı (javascript: vb. ret).
func validateRootCauseLabel(in rootCauseLabelInput) (rootCauseLabelInput, error) {
	in.Cause = strings.TrimSpace(in.Cause)
	in.Category = strings.TrimSpace(in.Category)
	in.Culprit = strings.TrimSpace(in.Culprit)
	in.Link = strings.TrimSpace(in.Link)
	in.Resolution = strings.TrimSpace(in.Resolution)
	switch {
	case in.Cause == "":
		return in, errors.New("kök neden metni zorunlu")
	case len(in.Cause) > caseFieldMax || len(in.Resolution) > caseFieldMax:
		return in, fmt.Errorf("metin en çok %d bayt", caseFieldMax)
	case !slices.Contains(cosrelearn.CaseCategories, in.Category):
		return in, fmt.Errorf("bilinmeyen kategori %q", in.Category)
	case utf8.RuneCountInString(in.Culprit) > rootCauseCulpritMax || strings.ContainsAny(in.Culprit, "\r\n\t"):
		return in, fmt.Errorf("suçlu bileşen tek satır, en çok %d karakter", rootCauseCulpritMax)
	}
	if in.Link != "" {
		u, err := url.Parse(in.Link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(in.Link) > rootCauseLinkMax {
			return in, fmt.Errorf("bağlantı http(s) adresi olmalı (en çok %d karakter)", rootCauseLinkMax)
		}
	}
	return in, nil
}

// caseFromProblem — SAF: taslağı olmayan Problem için vaka iskeleti (sinyal
// alanları boş — onları yalnız cosrelearn kurucusu doldurur).
func caseFromProblem(p chstore.Problem, now time.Time) chstore.IncidentCase {
	c := chstore.IncidentCase{CaseID: chstore.CaseIDFor("problem", p.ID), Source: "problem", SourceID: p.ID,
		Title: firstNonEmptyStr(p.RuleName, p.Description, p.Metric), Service: p.Service, Severity: p.Severity,
		OpenedAt: time.Unix(0, p.StartedAt), CosreCorrect: -1, Status: chstore.CaseDraft,
		Provenance: "problem detayı kök neden etiketi", CreatedAt: now}
	if p.ResolvedAt != nil && *p.ResolvedAt > 0 {
		c.ClosedAt = time.Unix(0, *p.ResolvedAt)
	}
	return c
}

func firstNonEmptyStr(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// labelOf — SAF: onaylı vaka + ek → etiket (onaysız vaka etiket DEĞİL → nil).
func labelOf(problemID string, c *chstore.IncidentCase, e *chstore.ProblemRootCauseExtra) *rootCauseLabel {
	if c == nil || c.Status != chstore.CaseConfirmed {
		return nil
	}
	l := &rootCauseLabel{ProblemID: problemID, CaseID: c.CaseID, Cause: c.FinalCause, Category: c.Category,
		Resolution: c.Resolution, By: c.ConfirmedBy}
	if !c.ConfirmedAt.IsZero() {
		l.At = c.ConfirmedAt.UnixMilli()
	}
	if e != nil {
		l.Culprit, l.Link = e.Culprit, e.Link
	}
	return l
}

func validProblemID(id string) bool {
	return id != "" && len(id) <= 128 && !strings.ContainsAny(id, " /\r\n\t")
}

func (s *Server) getRootCauseLabel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !validProblemID(id) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz problem kimliği")
		return
	}
	st := rootCauseLabelStoreFor(s)
	c, err := st.GetIncidentCase(r.Context(), chstore.CaseIDFor("problem", id))
	if err != nil {
		writeErr(w, err)
		return
	}
	out := rootCauseLabelResponse{Categories: cosrelearn.CaseCategories}
	if c != nil && c.Status != chstore.CaseConfirmed {
		out.Suggested = c.SuggestedCause
	}
	if c != nil && c.Status == chstore.CaseConfirmed {
		e, err := st.GetProblemRootCauseExtra(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		out.Label = labelOf(id, c, e)
	}
	writeJSON(w, out)
}

func (s *Server) putRootCauseLabel(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !validProblemID(id) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz problem kimliği")
		return
	}
	var in rootCauseLabelInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	in, err := validateRootCauseLabel(in)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	st := rootCauseLabelStoreFor(s)
	ctx := r.Context()
	p, err := st.GetProblem(ctx, id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if p == nil || p.ID == "" {
		writeJSONError(w, http.StatusNotFound, "problem bulunamadı")
		return
	}
	now := time.Now()
	c, err := st.GetIncidentCase(ctx, chstore.CaseIDFor("problem", id))
	if err != nil {
		writeErr(w, err)
		return
	}
	created := c == nil
	if created {
		nc := caseFromProblem(*p, now)
		c = &nc
	}
	who := ""
	if cl := auth.FromContext(ctx); cl != nil {
		who = firstNonEmptyStr(cl.Email, cl.UserID)
	}
	edit := c.Status == chstore.CaseConfirmed
	causeChanged := !edit || c.FinalCause != in.Cause
	c.FinalCause, c.Category, c.Resolution = in.Cause, in.Category, in.Resolution
	if !edit {
		c.Provenance = "confirmed by " + who + " (" + c.Provenance + ")"
	}
	c.Status, c.ConfirmedBy, c.ConfirmedAt = chstore.CaseConfirmed, who, now
	if causeChanged {
		c.CosreCorrect, c.CorrectNote = s.judgeSuggestion(ctx, c.SuggestedCause, c.FinalCause)
	}
	if err := st.UpsertIncidentCases(ctx, []chstore.IncidentCase{*c}); err != nil {
		writeErr(w, err)
		return
	}
	extra := chstore.ProblemRootCauseExtra{ProblemID: id, Culprit: in.Culprit, Link: in.Link, By: who, At: now.UnixNano()}
	if err := st.PutProblemRootCauseExtra(ctx, extra); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"caseId": c.CaseID, "category": c.Category, "culprit": in.Culprit,
		"link": in.Link != "", "edit": edit, "createdCase": created, "cosreCorrect": c.CosreCorrect})
	s.audit(r, "problem.rootcause.set", "problem", id, string(details))
	s.captureRootCauseMemory(r, *p, in.Cause, in.Category, who) // CoSRE Faz E — etiket = doğrulanmış olgu → ekip notu
	s.cacheInvalidatePrefix(ctx, pendingCasesCachePrefix)
	writeJSON(w, rootCauseLabelResponse{Label: labelOf(id, c, &extra), Categories: cosrelearn.CaseCategories})
}
