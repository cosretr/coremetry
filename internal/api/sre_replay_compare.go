package api

// sre_replay_compare.go — CoSRE ölçüm: iki replay raporunun karşılaştırması
// (sre_eval_compare.go emsali).
//
//   GET /api/sre/replay/compare?base=<raporId>&cand=<raporId> → viewer görür
//       (rapor okumaları gibi; LLM / istem / e-posta yok).
//       200 sre.ReplayCompare · 400 geçersiz kimlik · 404 raporlardan biri yok.
//
// Hesap SAF sre.CompareReplayReports'ta; burada yalnız okuma + hata eşlemesi.
// Eval karşılaştırmasından farkı: 409 yok — replay raporu dondurulmuş küme
// değil, Problem'ler problemId ile eşlenir (yalnız bir tarafta olan ayrı
// sayılır). Bu yüzden serveCached yeterli; anahtar iki kimliği AYNEN ve
// sırayla taşır (taban/aday yer değiştirince fark ters döner).
// requireCopilot arkasında DEĞİL: LLM yok. Yeni uç = yeni dosya
// (TestApiGoDoesNotGrow). MCP aracı yok.

import (
	"context"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/sre"
)

func init() { registerRoutesExtra("sre-replay-compare", (*Server).registerSREReplayCompareRoutes) }

func (s *Server) registerSREReplayCompareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/sre/replay/compare", s.getSREReplayCompare)
}

func sreReplayCompareKey(base, cand string) string { return "sre:replay:compare:" + base + ":" + cand }

func (s *Server) getSREReplayCompare(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSpace(r.URL.Query().Get("base"))
	cand := strings.TrimSpace(r.URL.Query().Get("cand"))
	if !sre.ValidEvalRunID(base) || !sre.ValidEvalRunID(cand) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz rapor kimliği (base, cand)")
		return
	}
	s.serveCached(w, r, sreReplayCompareKey(base, cand), sreReplayTTL, func(ctx context.Context) (any, error) {
		st := sreReplayStoreOf(s)
		b, ok, err := sre.LoadReplayReport(ctx, st, base)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, notFoundf("taban raporu %s", base)
		}
		c, ok, err := sre.LoadReplayReport(ctx, st, cand)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, notFoundf("aday raporu %s", cand)
		}
		return sre.CompareReplayReports(b, c), nil
	})
}
