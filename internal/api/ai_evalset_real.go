package api

// ai_evalset_real.go — v0.10.2468 (operatör onayı 2026-11-22: "CoSRE
// istemlerini körlemesine değiştirmeyi bırak"; DECISIONS "2026-11-22 —
// v0.10.2468"). GERÇEK SORULAR kümesi ve koşu raporu.
//
//	GET /api/ai/evalset/runs/{id}/report   admin · text/markdown ek dosya
//
// Küme = gömülü tohum (copilot/evalset/real/seed.json: router fikstürleri +
// prod tarzı sorular, servis adları sentetik + bindService) ∪ admin'in
// başarısız-tur listesinden terfi ettirdiği turlar (saved_views
// cosre_evalset_candidate satırları, Source "promoted"). Hepsi ajan kipi:
// canlı v2 sohbet handler'ı, ÜRETİMİN TAM YOLU (niyet sınıflandırıcısı
// atlanmaz — evalCase.real). Yalnız koşu isteğinde "RealQuestions" açıkça
// seçilince yüklenir: "Tümü", günlük koşu ve CI kapısı onu görmez; koşu
// yalnız admin tıklamasıyla başlar (otomatik tetik YOK).
//
// Vaka başına metrikler (rapor + panel): rota / kademe doğruluğu
// (expectRoute / expectTier), araç doğruluğu (anyOfTools / expectedTools /
// forbiddenTools / requireEvidence), doğrulayıcı geçti mi (gölge doğrulama
// uyuşmazlığı 0), alıntı denetimi (quote türü), gecikme, token, düşünme
// yüzeyi kipi. Önceki koşuyla kıyas: aynı vaka kimliği, yan yana.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
)

func init() { registerRoutesExtra("ai-evalset-report", (*Server).registerEvalReportRoutes) }

func (s *Server) registerEvalReportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/evalset/runs/{id}/report", auth.RequireRole(auth.RoleAdmin, s.getAIEvalsetRunReport))
}

// evalRealSurface — koşu isteğindeki sözde yüzey (vakaların yüzeyi ChatLoop).
const evalRealSurface = "RealQuestions"

// evalPromotedCasePrefix — terfi edilen turun vaka kimliği öneki.
const evalPromotedCasePrefix = "real-"

// evalConcurrencyMax — koşu isteğinin vaka eşzamanlılığı tavanı (yerel uç
// üretim sohbetiyle paylaşılır; varsayılan 1 = ardışık, eski davranış).
const evalConcurrencyMax = 3

// evalRealSeedLoader — test dikişi.
var evalRealSeedLoader = func() ([]evalCase, error) {
	sub, err := fs.Sub(evalset.RealFS, "real")
	if err != nil {
		return nil, err
	}
	return loadEvalsetFS(sub)
}

// evalWantsReal — SAF: istek gerçek soruları seçti mi; kalan yüzeyler.
func evalWantsReal(surfaces []string) (bool, []string) {
	want, rest := false, []string{}
	for _, s := range surfaces {
		if strings.TrimSpace(s) == evalRealSurface {
			want = true
			continue
		}
		rest = append(rest, s)
	}
	return want, rest
}

// evalNormConcurrency — SAF: 0 → 1, tavan evalConcurrencyMax.
func evalNormConcurrency(n int) (int, error) {
	switch {
	case n == 0:
		return 1, nil
	case n < 0 || n > evalConcurrencyMax:
		return 0, fmt.Errorf("concurrency 1..%d olmalı", evalConcurrencyMax)
	}
	return n, nil
}

// evalPromotedCase — SAF: terfi edilen tur → ajan vakası (beklenti operatörün).
func evalPromotedCase(c evalsetCandidate) evalCase {
	why := fmt.Sprintf("terfi edilen tur (%s, %s)", c.Day, c.AcceptedBy)
	if len(c.Signals) > 0 {
		why += "; gerekçeler: " + strings.Join(c.Signals, ", ")
	}
	return evalCase{
		ID: evalPromotedCasePrefix + c.ExchangeID, Surface: evalAgentLoopSurface, Mode: evalModeAgent,
		User: c.Question, Why: why, Note: c.Note, BindService: c.BindService,
		Expect: evalExpect{
			AnyOfTools: c.AnyOfTools, MustContain: c.MustContain, MustNotContain: c.MustNotContain,
			ExpectTier: c.ExpectTier, ExpectRoute: c.ExpectRoute,
		},
		Provenance: &evalProvenance{ExchangeID: c.ExchangeID},
		file:       "promoted", real: true,
	}
}

// evalRealCases — tohum + kabul edilmiş terfi vakaları (real işaretli).
func (s *Server) evalRealCases(ctx context.Context) (cases []evalCase, seed, promoted int, err error) {
	seedCases, err := evalRealSeedLoader()
	if err != nil {
		return nil, 0, 0, err
	}
	for _, c := range seedCases {
		c.real = true
		cases = append(cases, c)
	}
	seed = len(seedCases)
	acc, err := s.acceptedEvalCandidates(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, c := range acc {
		if c.Source == evalCandidateSourcePromoted {
			cases = append(cases, evalPromotedCase(c))
			promoted++
		}
	}
	return cases, seed, promoted, nil
}

// evalCatalogReal — panelin "Gerçek sorular" satırı.
type evalCatalogReal struct {
	Surface        string               `json:"surface"`
	Cases          int                  `json:"cases"`
	Seed           int                  `json:"seed"`
	Promoted       int                  `json:"promoted"`
	MaxConcurrency int                  `json:"maxConcurrency"`
	Profiles       []evalCatalogProfile `json:"profiles"`
	// Thinking — düşünme yüzeyi kipleri (yüzey → off | on | inherit).
	Thinking map[string]string `json:"thinking,omitempty"`
	Error    string            `json:"error,omitempty"`
}

func (s *Server) evalRealCatalog(ctx context.Context) *evalCatalogReal {
	out := &evalCatalogReal{Surface: evalRealSurface, MaxConcurrency: evalConcurrencyMax, Profiles: []evalCatalogProfile{}}
	cases, seed, promoted, err := s.evalRealCases(ctx)
	if err != nil {
		out.Error = err.Error()
	}
	out.Cases, out.Seed, out.Promoted = len(cases), seed, promoted
	if s.copilot != nil {
		profiles, _, _ := s.copilot.ProfilesSnapshot()
		for _, p := range profiles {
			out.Profiles = append(out.Profiles, evalCatalogProfile{ID: p.ID, Label: p.Label, Model: p.Model})
		}
		out.Thinking = s.copilot.ThinkingModes()
	}
	return out
}

// evalThinkSurfaceForTier — SAF: turun kademesi → düşünme yüzeyi.
func evalThinkSurfaceForTier(tier string) copilot.ThinkSurface {
	switch tier {
	case aisurface.TierLoop, "deep", "investigate":
		return copilot.ThinkInvestigation
	case aisurface.TierGuided:
		return copilot.ThinkGuided
	case aisurface.TierIntent:
		return copilot.ThinkIntent
	}
	return copilot.ThinkOther
}

// evalThinkingLabel — vakanın anlatım yüzeyi ve o anki kipi ("guided=off").
func (s *Server) evalThinkingLabel(out evalCaseOutcome) string {
	if s == nil || s.copilot == nil || out.Agent == nil {
		return ""
	}
	surf := evalThinkSurfaceForTier(out.Agent.Tier)
	mode := s.copilot.ThinkingModes()[string(surf)]
	if mode == "" {
		return string(surf)
	}
	return string(surf) + "=" + mode
}

// ── Rapor ──────────────────────────────────────────────────────────────

// evalCaseMetrics — SAF: vaka başına rapor metrikleri. *bool nil = ölçülmedi.
type evalCaseMetrics struct {
	RouteOK   *bool
	ToolsOK   *bool
	VerifyOK  *bool
	QuoteOK   *bool
	LatencyMs int64
	Tokens    uint32
	Thinking  string
}

func evalBoolPtr(b bool) *bool { return &b }

// evalCaseMetricsOf — SAF. Rota: beklenen kademe/rota varsa ihlal yok mu.
// Araç: araç beklentisi varsa ilgili ihlal yok mu. Doğrulayıcı: doğrulama
// koştuysa uyuşmazlık 0 mı. Alıntı: alıntı denetlendiyse uyuşmazlık 0 mı.
func evalCaseMetricsOf(c evalCaseResult, e evalExpect) evalCaseMetrics {
	m := evalCaseMetrics{LatencyMs: c.LatencyMs, Thinking: c.Thinking}
	hasPrefix := func(ps ...string) bool {
		for _, f := range c.Fails {
			for _, p := range ps {
				if strings.HasPrefix(f, p) {
					return true
				}
			}
		}
		return false
	}
	if c.Skipped {
		return m
	}
	if e.ExpectTier != "" || e.ExpectRoute != "" {
		m.RouteOK = evalBoolPtr(!hasPrefix("tier ", "route ", "error:", "no answer"))
	}
	if len(e.AnyOfTools) > 0 || len(e.ExpectedTools) > 0 || len(e.ForbiddenTools) > 0 || e.RequireEvidence {
		m.ToolsOK = evalBoolPtr(!hasPrefix("missing tool", "missing any of tools", "forbidden tool", "no evidence", "error:"))
	}
	if a := c.Agent; a != nil {
		m.Tokens = a.InputTokens + a.OutputTokens
		if a.Verify != nil && a.Verify.Checked > 0 {
			m.VerifyOK = evalBoolPtr(a.Verify.Mismatch == 0)
		}
		if a.QuoteChecked > 0 {
			m.QuoteOK = evalBoolPtr(a.QuoteMismatch == 0)
		}
	}
	return m
}

func evalYesNo(b *bool) string {
	switch {
	case b == nil:
		return "—"
	case *b:
		return "✓"
	}
	return "✗"
}

func evalRate(n, d int) string {
	if d == 0 {
		return "—"
	}
	return fmt.Sprintf("%d/%d (%.0f%%)", n, d, 100*float64(n)/float64(d))
}

// evalReportAgg — SAF: metrik sayaçları.
type evalReportAgg struct {
	cases, scored, pass                                    int
	routeN, routeOK, toolsN, toolsOK, verN, verOK, qN, qOK int
	latency                                                []int64
	tokens                                                 int64
}

func (a *evalReportAgg) add(c evalCaseResult, m evalCaseMetrics) {
	a.cases++
	if c.Skipped {
		return
	}
	a.scored++
	if c.OK {
		a.pass++
	}
	tally := func(b *bool, n, ok *int) {
		if b != nil {
			*n++
			if *b {
				*ok++
			}
		}
	}
	tally(m.RouteOK, &a.routeN, &a.routeOK)
	tally(m.ToolsOK, &a.toolsN, &a.toolsOK)
	tally(m.VerifyOK, &a.verN, &a.verOK)
	tally(m.QuoteOK, &a.qN, &a.qOK)
	a.latency = append(a.latency, m.LatencyMs)
	a.tokens += int64(m.Tokens)
}

func (a *evalReportAgg) p50() string {
	if len(a.latency) == 0 {
		return "—"
	}
	xs := append([]int64(nil), a.latency...)
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return fmt.Sprintf("%.1f s", float64(xs[len(xs)/2])/1000)
}

// evalCaseExpect — vaka sonucunun saklı beklentisi (çözülemezse boş).
func evalCaseExpect(c evalCaseResult) evalExpect {
	var e evalExpect
	if len(c.Expect) > 0 {
		_ = json.Unmarshal(c.Expect, &e)
	}
	return e
}

// evalRunReport — SAF, deterministik: koşunun Markdown raporu; base nil
// değilse vaka kimliğiyle yan yana kıyas.
func evalRunReport(run evalRunSummary, cases []evalCaseResult, base *evalRunSummary, baseCases []evalCaseResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# CoSRE değerlendirme raporu — %s\n\n", run.ID)
	fmt.Fprintf(&b, "- Başlangıç: %s · durum %s · başlatan %s\n", run.StartedAt, run.Status, evalMdCell(run.StartedBy))
	fmt.Fprintf(&b, "- Uygulama %s · prompt %s · model %s · profil %s\n", run.AppVersion, run.PromptVersion, evalMdCell(run.Model), evalMdCell(run.ProfileID))
	fmt.Fprintf(&b, "- Yüzeyler: %s · geçti %d / %d (atlanan %d)\n\n", strings.Join(append([]string{}, run.Surfaces...), ", "), run.Pass, run.Total, run.Skipped)

	byProfile := map[string]*evalReportAgg{}
	var profiles []string
	metrics := make([]evalCaseMetrics, len(cases))
	for i, c := range cases {
		metrics[i] = evalCaseMetricsOf(c, evalCaseExpect(c))
		p := c.ProfileID + " · " + c.Model
		if byProfile[p] == nil {
			byProfile[p] = &evalReportAgg{}
			profiles = append(profiles, p)
		}
		byProfile[p].add(c, metrics[i])
	}
	sort.Strings(profiles)
	b.WriteString("## Özet (profil başına)\n\n| Profil · model | Vaka | Geçti | Rota | Araç | Doğrulayıcı | Alıntı | Gecikme p50 | Token (toplam) |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, p := range profiles {
		a := byProfile[p]
		fmt.Fprintf(&b, "| %s | %d | %s | %s | %s | %s | %s | %s | %d |\n", evalMdCell(p), a.cases, evalRate(a.pass, a.scored),
			evalRate(a.routeOK, a.routeN), evalRate(a.toolsOK, a.toolsN), evalRate(a.verOK, a.verN), evalRate(a.qOK, a.qN), a.p50(), a.tokens)
	}

	baseBy := map[string]evalCaseResult{}
	for _, c := range baseCases {
		baseBy[c.ID+"\x00"+c.ProfileID] = c
	}
	b.WriteString("\n## Vakalar\n\n| Vaka | Profil | Sonuç | Rota | Araç | Doğrulayıcı | Alıntı | Gecikme | Token | Düşünme | Gerekçe |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for i, c := range cases {
		m := metrics[i]
		res := "geçti"
		switch {
		case c.Skipped:
			res = "atlandı"
		case !c.OK:
			res = "kaldı"
		}
		reason := strings.Join(c.Fails, "; ")
		if c.Skipped {
			reason = c.SkipReason
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %.1f s | %d | %s | %s |\n", evalMdCell(c.ID), evalMdCell(c.ProfileID), res,
			evalYesNo(m.RouteOK), evalYesNo(m.ToolsOK), evalYesNo(m.VerifyOK), evalYesNo(m.QuoteOK),
			float64(m.LatencyMs)/1000, m.Tokens, evalMdCell(m.Thinking), evalMdCell(reason))
	}

	if base != nil {
		fmt.Fprintf(&b, "\n## Önceki koşuyla kıyas — %s (%s, model %s)\n\n", base.ID, base.StartedAt, evalMdCell(base.Model))
		b.WriteString("| Vaka | Profil | Önce | Şimdi | Gecikme önce → şimdi | Token önce → şimdi |\n|---|---|---|---|---|---|\n")
		changed := 0
		for i, c := range cases {
			bc, ok := baseBy[c.ID+"\x00"+c.ProfileID]
			if !ok {
				continue
			}
			bm := evalCaseMetricsOf(bc, evalCaseExpect(bc))
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %.1f → %.1f s | %d → %d |\n", evalMdCell(c.ID), evalMdCell(c.ProfileID),
				evalResultWord(bc), evalResultWord(c), float64(bm.LatencyMs)/1000, float64(metrics[i].LatencyMs)/1000, bm.Tokens, metrics[i].Tokens)
			if evalResultWord(bc) != evalResultWord(c) {
				changed++
			}
		}
		fmt.Fprintf(&b, "\nSonucu değişen vaka: %d.\n", changed)
	} else {
		b.WriteString("\n_Kıyaslanacak önceki bitmiş koşu yok._\n")
	}
	b.WriteString("\nTanımlar: Rota = beklenen kademe/rota (expectTier / expectRoute) tuttu; Araç = araç beklentisi (anyOfTools, expectedTools, forbiddenTools, requireEvidence) tuttu; Doğrulayıcı = gölge doğrulama uyuşmazlığı 0; Alıntı = tırnak içi metin kanıtta birebir geçti; \"—\" = ölçülmedi. Düşünme = turun anlatım yüzeyi ve koşu anındaki kipi. Atlanan vaka (altyapı hatası, bağlanacak servis yok) puanlanmaz. Oy / geri bildirim girdi değildir.\n")
	return b.String()
}

func evalResultWord(c evalCaseResult) string {
	switch {
	case c.Skipped:
		return "atlandı"
	case c.OK:
		return "geçti"
	}
	return "kaldı"
}

// evalPreviousRun — SAF: rows içinde run'dan ÖNCE başlamış, bitmiş ve aynı
// yüzey seçimini taşıyan en yeni koşu (liste satırları vaka taşımaz; vaka
// eşleşmesi rapor gövdesinde kimlikle). Sıra bağımsız.
func evalPreviousRun(run evalRunSummary, rows []chstore.EvalRun) *chstore.EvalRun {
	key := func(xs []string) string {
		ys := append([]string(nil), xs...)
		sort.Strings(ys)
		return strings.Join(ys, ",")
	}
	want := key(run.Surfaces)
	// Başlangıç: listede kendi satırı varsa tam hassasiyet, yoksa özet dizgesi.
	start, _ := time.Parse(time.RFC3339Nano, run.StartedAt)
	for _, r := range rows {
		if r.ID == run.ID {
			start = r.StartedAt
		}
	}
	var best *chstore.EvalRun
	for i := range rows {
		r := rows[i]
		if r.ID == run.ID || r.Status != evalStatusDone || !r.StartedAt.Before(start) || key(r.Surfaces) != want {
			continue
		}
		if best == nil || r.StartedAt.After(best.StartedAt) {
			best = &rows[i]
		}
	}
	return best
}

func (s *Server) getAIEvalsetRunReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" || len(id) > evalRunIDMax {
		writeJSONError(w, http.StatusNotFound, "koşu bulunamadı")
		return
	}
	var run evalRunSummary
	var cases []evalCaseResult
	if sum, cs, ok := evalJobs.snapshot(id); ok {
		run, cases = sum, cs
	} else {
		row, err := s.loadEvalRun(r.Context(), id)
		if err != nil {
			writeErr(w, err)
			return
		}
		if row == nil {
			writeJSONError(w, http.StatusNotFound, "koşu bulunamadı")
			return
		}
		run, cases = evalSummaryFromRow(*row, time.Now(), false), evalCasesFromRow(*row)
	}
	var base *evalRunSummary
	var baseCases []evalCaseResult
	if st := evalRunStoreFor(s); st != nil {
		if rows, err := st.ListEvalRuns(r.Context(), evalRunsKeep); err == nil {
			if prev := evalPreviousRun(run, rows); prev != nil {
				if full, ferr := s.loadEvalRun(r.Context(), prev.ID); ferr == nil && full != nil {
					sum := evalSummaryFromRow(*full, time.Now(), false)
					base, baseCases = &sum, evalCasesFromRow(*full)
				}
			}
		}
	}
	date := run.StartedAt
	if len(date) >= 10 {
		date = date[:10]
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="cosre-eval-%s-%s.md"`, date, evalFileSafe(run.ID)))
	_, _ = w.Write([]byte(evalRunReport(run, cases, base, baseCases)))
}

// evalFileSafe — SAF: dosya adına güvenli kimlik.
func evalFileSafe(id string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, id)
}
