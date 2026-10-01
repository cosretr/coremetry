package api

// problem_verdicts.go — v0.10.1015 — Problems sekmesinde ÖĞRETME (operatör:
// "bütün hepsi gelsin, ben hangisi gerçek problem hangisi değil zamanla
// öğretelim"). Depolama ve kavram: internal/chstore/problem_verdict.go.
//
//	GET /api/problem-verdicts      her rol   — tüm kararlar (≤5000)
//	PUT /api/problem-verdicts      editor+   — {signature, verdict, label?, kind?, service?}
//	                                           verdict: "real" | "noise" | "" (kaldır)
//
// Karar İMZAYA bağlıdır (aynı kural + servis / aynı exception grubu); FE imzayı
// üretir, sunucu biçimini doğrular. Yalnız GÖRÜNÜM: bildirimlere, Problem yaşam
// döngüsüne ve dedektörlere dokunmaz. Viewer kararları GÖRÜR (salt okuma),
// yazamaz. Yazım denetim kaydına "problem.verdict" olarak düşer ve okuma
// önbelleğini düşürür; PUT cevabı taze listenin tamamıdır (FE onu doğrudan
// kullanır — yazdığını bir sonraki okumada bayat önbellekten geri kaybetmez).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("problem-verdicts", (*Server).registerProblemVerdictRoutes) }

func (s *Server) registerProblemVerdictRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problem-verdicts", s.getProblemVerdicts)
	mux.HandleFunc("PUT /api/problem-verdicts", auth.RequireAnyRole(editorRoles, s.putProblemVerdict))
}

const (
	problemVerdictCacheKey = "problem-verdicts:all"
	problemVerdictCachePfx = "problem-verdicts:"
	problemVerdictLabelMax = 200 // rune
	problemVerdictFieldMax = 120 // rune (kind / service)
	problemVerdictMaxBody  = 8 << 10
)

type problemVerdictsResponse struct {
	Verdicts []chstore.ProblemVerdict `json:"verdicts"`
}

type problemVerdictInput struct {
	Signature string `json:"signature"`
	Verdict   string `json:"verdict"` // real | noise | "" (kaldır)
	Label     string `json:"label"`
	Kind      string `json:"kind"`
	Service   string `json:"service"`
}

// normalizeProblemVerdictInput — SAF (tablo testli): gövde → yazılacak karar.
// clear=true: karar kaldırılır. Hata mesajı Türkçe ve alanı söyler.
func normalizeProblemVerdictInput(in problemVerdictInput, by string, now time.Time) (v chstore.ProblemVerdict, clear bool, msg string) {
	sig := strings.TrimSpace(in.Signature)
	if !chstore.ValidProblemSignature(sig) {
		return v, false, "signature geçersiz: p: (alarm kuralı), e: (exception grubu) ya da a: (anomali) önekiyle başlamalı"
	}
	verdict := strings.TrimSpace(in.Verdict)
	if verdict == "" {
		return chstore.ProblemVerdict{Signature: sig}, true, ""
	}
	if !chstore.ValidProblemVerdict(verdict) {
		return v, false, "verdict 'real', 'noise' ya da boş (kaldır) olmalı"
	}
	return chstore.ProblemVerdict{
		Signature: sig, Verdict: verdict,
		Label:   truncRunes(strings.TrimSpace(in.Label), problemVerdictLabelMax),
		Kind:    truncRunes(strings.TrimSpace(in.Kind), problemVerdictFieldMax),
		Service: truncRunes(strings.TrimSpace(in.Service), problemVerdictFieldMax),
		By:      by, At: now.UnixNano(),
	}, false, ""
}

func (s *Server) getProblemVerdicts(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, problemVerdictCacheKey, 10*time.Second, func(ctx context.Context) (any, error) {
		list, err := s.store.ListProblemVerdicts(ctx)
		if err != nil {
			return nil, err
		}
		return problemVerdictsResponse{Verdicts: list}, nil
	})
}

func (s *Server) putProblemVerdict(w http.ResponseWriter, r *http.Request) {
	var in problemVerdictInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, problemVerdictMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	v, clear, msg := normalizeProblemVerdictInput(in, claimEmail(auth.FromContext(r.Context())), time.Now())
	if msg != "" {
		writeJSONError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()
	if clear {
		if err := s.store.ClearProblemVerdict(ctx, v.Signature); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		// Tavan: yeni bir imza 5000'i aşıracaksa reddet (mevcut imzanın
		// kararını değiştirmek her zaman serbest).
		cur, err := s.store.ListProblemVerdicts(ctx)
		if err != nil {
			writeErr(w, err)
			return
		}
		known := false
		for _, c := range cur {
			if c.Signature == v.Signature {
				known = true
				break
			}
		}
		if !known && len(cur) >= chstore.ProblemVerdictMax {
			writeJSONError(w, http.StatusBadRequest, "karar tavanı doldu (5000 imza) — eski kararları kaldırın")
			return
		}
		if err := s.store.SetProblemVerdict(ctx, v); err != nil {
			writeErr(w, err)
			return
		}
	}
	s.cacheInvalidatePrefix(ctx, problemVerdictCachePfx)
	details, _ := json.Marshal(map[string]string{"signature": v.Signature, "verdict": v.Verdict, "kind": v.Kind, "service": v.Service, "label": v.Label})
	s.audit(r, "problem.verdict", "problem", v.Signature, string(details))
	list, err := s.store.ListProblemVerdicts(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	// Yeni yazılan satır okumaya henüz yansımadıysa (eşzamansız ekleme) cevabı
	// yazılanla uzlaştır: FE yazdığını geri kaybetmesin.
	writeJSON(w, problemVerdictsResponse{Verdicts: reconcileProblemVerdicts(list, v, clear)})
}

// reconcileProblemVerdicts — SAF: okunan liste + az önce yazılan karar → cevap.
// Okuma yazımı henüz görmediyse (ya da eskisini gördüyse) yazılan kazanır.
func reconcileProblemVerdicts(list []chstore.ProblemVerdict, written chstore.ProblemVerdict, cleared bool) []chstore.ProblemVerdict {
	out := make([]chstore.ProblemVerdict, 0, len(list)+1)
	if !cleared {
		out = append(out, written)
	}
	for _, v := range list {
		if v.Signature != written.Signature {
			out = append(out, v)
		}
	}
	return out
}
