package cosrelearn

// cases.go — CoSRE öğrenme Faz 3: vaka hafızası. Kapanan Problem ve çözülen
// exception grubu için TASLAK incident_case kurar: servis, sinyaller (en
// kalabalık hata kodları, hata veren function/channel kodları, RED önce/
// sırasında), penceredeki deploy, CoSRE'nin önerdiği neden. Nihai neden
// YALNIZ insandan gelir (Problem UI onayı, api/cosre_cases.go) — taslak
// hiçbir zaman olgu sayılmaz.
//
// Çekme tabanlı: kapanış kancası yok (MarkResolved'ın ~35 çağıranı var);
// iş 5 dakikada bir son kapananları okur, kurulmamış olanları kurar.

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// CaseStore — kurucunun G/Ç yüzeyi (*chstore.Store karşılar).
type CaseStore interface {
	ListClosedProblemsForCases(ctx context.Context, from time.Time, limit int) ([]chstore.ClosedProblemForCase, error)
	ListResolvedExceptionsForCases(ctx context.Context, from time.Time, limit int) ([]chstore.ResolvedExceptionForCase, error)
	ExistingCaseIDs(ctx context.Context, ids []string) (map[string]bool, error)
	UpsertIncidentCases(ctx context.Context, cases []chstore.IncidentCase) error
	TopExceptionCodesForService(ctx context.Context, service string, from, to time.Time, limit int) ([]string, error)
	TopRollupErrorCodes(ctx context.Context, service, dim string, from, to time.Time, limit int) ([]string, error)
	ServiceREDSeries(ctx context.Context, service string, from, to time.Time) []chstore.SpanMetricSeries
	GetDeploysInWindow(ctx context.Context, from, to time.Time, limit int) ([]chstore.RecentDeployEntry, error)
	GetHypothesis(ctx context.Context, anchorKind, anchorID string) (*chstore.RootCauseHypothesis, error)
	LatestRCAVerdictForAnchor(ctx context.Context, anchorKind, anchorID string) (*chstore.RCAVerdictRecord, error)
}

const (
	caseLockKey      = "coremetry:lock:cosre-case-builder"
	caseInterval     = 5 * time.Minute
	caseLookback     = 6 * time.Hour // yeniden başlatma / lider değişimi örtüşmesi
	casePerTick      = 25            // kaynak başına tik tavanı (vaka başına ~7 okuma)
	caseMinDuration  = 5 * time.Minute
	caseBaselineSpan = time.Hour
	caseCodeLimit    = 5
	caseCauseMax     = 1200
	// CaseProvenance — taslağın nereden geldiği (onayda insan adı eklenir).
	caseProvenance = "auto: cosrelearn case builder"
)

// CaseCategories — onay formunun kapalı kategori kümesi.
var CaseCategories = []string{"deploy", "config", "capacity", "dependency", "code_bug", "infra", "data", "external", "noise", "other"}

// verdictLister — isteğe bağlı yüzey (v0.10.1531, Problem standardı R3 / D4):
// *chstore.Store karşılar. "Gerçek problem" işaretli imzanın kapanan problemi
// süresinden bağımsız taslak alır. Okuma düşerse ya da depo bu yüzeyi
// taşımıyorsa eski uygunluk kuralı aynen geçerli.
type verdictLister interface {
	ListProblemVerdicts(ctx context.Context) ([]chstore.ProblemVerdict, error)
}

// realVerdictSignatures — "Gerçek problem" imza kümesi (hata → boş küme).
func realVerdictSignatures(ctx context.Context, st CaseStore) map[string]bool {
	out := map[string]bool{}
	vl, ok := st.(verdictLister)
	if !ok {
		return out
	}
	list, err := vl.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[cosrelearn] problem verdicts: %v — gerçek işaretleri uygunluğa girmedi", err)
		return out
	}
	for _, v := range list {
		if v.Verdict == chstore.ProblemVerdictReal {
			out[v.Signature] = true
		}
	}
	return out
}

// ProblemCaseEligibleMarked — SAF (v0.10.1531, D4): ProblemCaseEligible ya da
// operatör imzayı "Gerçek problem" diye işaretlemiş. İşaret değerlendirme
// etiketi DEĞİL (D4): yalnız taslağı açar; nihai neden yine insandan.
func ProblemCaseEligibleMarked(p chstore.ClosedProblemForCase, real map[string]bool) bool {
	if ProblemCaseEligible(p) {
		return true
	}
	sig := chstore.RuleProblemVerdictSignature(p.RuleID, p.Service)
	return sig != "" && real[sig]
}

// ProblemCaseEligible — SAF: kısa ömürlü gürültü vaka olmaz; kritik ya da
// ≥5 dk süren problem olur.
func ProblemCaseEligible(p chstore.ClosedProblemForCase) bool {
	if strings.EqualFold(p.Severity, "critical") {
		return true
	}
	return p.ResolvedAt.Sub(p.StartedAt) >= caseMinDuration
}

// REDDelta — SAF: [start-1h, start) taban ve [start, end] olay sırası ortalamaları.
type REDDelta struct {
	RateBefore, RateDuring, ErrBefore, ErrDuring, P99Before, P99During float64
}

func ComputeREDDelta(series []chstore.SpanMetricSeries, start time.Time) REDDelta {
	var d REDDelta
	startNs := start.UnixNano()
	for _, s := range series {
		if len(s.GroupKey) == 0 {
			continue
		}
		var bs, bn, ds, dn float64
		for _, p := range s.Points {
			if p.Time < startNs {
				bs, bn = bs+p.Value, bn+1
			} else {
				ds, dn = ds+p.Value, dn+1
			}
		}
		b, du := avg(bs, bn), avg(ds, dn)
		switch s.GroupKey[0] {
		case "rate":
			d.RateBefore, d.RateDuring = b, du
		case "error_rate":
			d.ErrBefore, d.ErrDuring = b, du
		case "p99":
			d.P99Before, d.P99During = b, du
		}
	}
	return d
}

func avg(sum, n float64) float64 {
	if n == 0 {
		return 0
	}
	return sum / n
}

// PickDeploy — SAF: servisin [from,to] penceresindeki en yeni deploy'u.
func PickDeploy(entries []chstore.RecentDeployEntry, service string, from, to time.Time) (bool, string) {
	var best chstore.RecentDeployEntry
	found := false
	for _, e := range entries {
		if e.Service != service || e.FirstSeenNs < from.UnixNano() || e.FirstSeenNs > to.UnixNano() {
			continue
		}
		if !found || e.FirstSeenNs > best.FirstSeenNs {
			best, found = e, true
		}
	}
	return found, best.Version
}

// SuggestedCause — SAF: CoSRE'nin önerisi, güven sırasıyla: RCA hakem
// hükmü (kök neden bulunduysa) → hipotez baş şüphelisi → proaktif AI özeti.
func SuggestedCause(v *chstore.RCAVerdictRecord, h *chstore.RootCauseHypothesis, aiSummary string) (text, source string) {
	if v != nil && v.Verdict == "root_cause_identified" {
		parts := []string{}
		if v.RCEntity != "" {
			parts = append(parts, v.RCEntity)
		}
		if v.RCFailMode != "" {
			parts = append(parts, v.RCFailMode)
		}
		t := strings.Join(parts, ": ")
		if body := strings.TrimSpace(v.Body); body != "" {
			if t != "" {
				t += " — "
			}
			t += body
		}
		if t != "" {
			return clip(t, caseCauseMax), "rca_verdict"
		}
	}
	if h != nil && h.TopSuspect != "" {
		t := fmt.Sprintf("Olası kaynak: %s (güven %%%d)", h.TopSuspect, int(h.Confidence*100+0.5))
		if len(h.Candidates) > 0 && h.Candidates[0].Reason != "" {
			t += " — " + h.Candidates[0].Reason
		}
		if h.RecentDeploy != nil && h.RecentDeploy.Version != "" {
			t += " · deploy " + h.RecentDeploy.Version
		}
		return clip(t, caseCauseMax), "hypothesis"
	}
	if s := strings.TrimSpace(aiSummary); s != "" {
		return clip(s, caseCauseMax), "ai_summary"
	}
	return "", ""
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// caseSignals — bir servis × pencere için toplanan sinyaller (best-effort:
// okunamayan alan boş kalır, vaka yine kurulur).
func gatherSignals(ctx context.Context, st CaseStore, c *chstore.IncidentCase) {
	from, to := c.OpenedAt, c.ClosedAt
	if c.Service == "" {
		return
	}
	if codes, err := st.TopExceptionCodesForService(ctx, c.Service, from, to, caseCodeLimit); err == nil {
		c.ErrorCodes = mergeCodes(c.ErrorCodes, codes)
	}
	if fn, err := st.TopRollupErrorCodes(ctx, c.Service, "function_code", from, to, caseCodeLimit); err == nil {
		c.FunctionCodes = fn
	}
	if ch, err := st.TopRollupErrorCodes(ctx, c.Service, "channel_code", from, to, caseCodeLimit); err == nil {
		c.ChannelCodes = ch
	}
	d := ComputeREDDelta(st.ServiceREDSeries(ctx, c.Service, from.Add(-caseBaselineSpan), to), from)
	c.RateBefore, c.RateDuring, c.ErrRateBefore, c.ErrRateDuring, c.P99BeforeMs, c.P99DuringMs =
		d.RateBefore, d.RateDuring, d.ErrBefore, d.ErrDuring, d.P99Before, d.P99During
	if deps, err := st.GetDeploysInWindow(ctx, from.Add(-caseBaselineSpan), from.Add(5*time.Minute), 500); err == nil {
		c.DeployInWindow, c.DeployVersion = PickDeploy(deps, c.Service, from.Add(-caseBaselineSpan), from.Add(5*time.Minute))
	}
}

// mergeCodes — SAF: sıra korunarak tekil birleşim, tavan caseCodeLimit*2.
func mergeCodes(a, b []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range append(append([]string{}, a...), b...) {
		x = strings.TrimSpace(x)
		if x != "" && !seen[x] && len(out) < caseCodeLimit*2 {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// DraftFromProblem — SAF: problem girdisinden taslak iskeleti (sinyaller hariç).
func DraftFromProblem(p chstore.ClosedProblemForCase, now time.Time) chstore.IncidentCase {
	title := p.RuleName
	if title == "" {
		title = p.Description
	}
	return chstore.IncidentCase{
		CaseID: chstore.CaseIDFor("problem", p.ID), Source: "problem", SourceID: p.ID,
		Title: clip(title, 300), Service: p.Service, Severity: p.Severity,
		OpenedAt: p.StartedAt, ClosedAt: p.ResolvedAt,
		Status: chstore.CaseDraft, CosreCorrect: -1, Provenance: caseProvenance, CreatedAt: now,
	}
}

// DraftFromException — SAF: exception grubundan taslak; ex_type hata kodudur
// (Oracle gruplarında doğrudan kod, ör. MCA-00029).
func DraftFromException(e chstore.ResolvedExceptionForCase, now time.Time) chstore.IncidentCase {
	title := e.Type
	if e.Message != "" {
		title += ": " + e.Message
	}
	c := chstore.IncidentCase{
		CaseID: chstore.CaseIDFor("exception", e.Fingerprint), Source: "exception", SourceID: e.Fingerprint,
		Title: clip(title, 300), Service: e.Service, OpenedAt: e.FirstSeen, ClosedAt: e.ResolvedAt,
		ErrorCodes: mergeCodes(nil, []string{e.Type}),
		Status:     chstore.CaseDraft, CosreCorrect: -1, Provenance: caseProvenance, CreatedAt: now,
	}
	// Uzun ömürlü grupta pencere son görülmeye daraltılır (RED/deploy anlamlı kalsın).
	if c.ClosedAt.Sub(c.OpenedAt) > 24*time.Hour && !e.LastSeen.IsZero() {
		c.OpenedAt = e.LastSeen.Add(-time.Hour)
	}
	c.SuggestedCause, c.SuggestedSource = SuggestedCause(nil, nil, e.AISummary)
	return c
}

// BuildCases — bir tik: yeni kapananlardan taslaklar.
func BuildCases(ctx context.Context, st CaseStore, now time.Time) (int, error) {
	from := now.Add(-caseLookback)
	probs, err := st.ListClosedProblemsForCases(ctx, from, 500)
	if err != nil {
		return 0, err
	}
	exs, err := st.ListResolvedExceptionsForCases(ctx, from, 500)
	if err != nil {
		return 0, err
	}
	real := realVerdictSignatures(ctx, st) // v0.10.1531 (D4)
	var drafts []chstore.IncidentCase
	for _, p := range probs {
		if ProblemCaseEligibleMarked(p, real) {
			drafts = append(drafts, DraftFromProblem(p, now))
		}
	}
	nProb := len(drafts)
	for _, e := range exs {
		drafts = append(drafts, DraftFromException(e, now))
	}
	ids := make([]string, len(drafts))
	for i, d := range drafts {
		ids[i] = d.CaseID
	}
	have, err := st.ExistingCaseIDs(ctx, ids)
	if err != nil {
		return 0, err
	}
	var out []chstore.IncidentCase
	probN, exN := 0, 0
	for i, d := range drafts {
		if have[d.CaseID] {
			continue
		}
		isProb := i < nProb
		if (isProb && probN >= casePerTick) || (!isProb && exN >= casePerTick) {
			continue // sonraki tike (pencere örtüşür)
		}
		if isProb {
			probN++
			var v *chstore.RCAVerdictRecord
			var h *chstore.RootCauseHypothesis
			if vv, err := st.LatestRCAVerdictForAnchor(ctx, "problem", d.SourceID); err == nil {
				v = vv
			}
			if hh, err := st.GetHypothesis(ctx, "problem", d.SourceID); err == nil {
				h = hh
			}
			d.SuggestedCause, d.SuggestedSource = SuggestedCause(v, h, probs[indexOfProblem(probs, d.SourceID)].AISummary)
		} else {
			exN++
		}
		gatherSignals(ctx, st, &d)
		out = append(out, d)
	}
	return len(out), st.UpsertIncidentCases(ctx, out)
}

func indexOfProblem(ps []chstore.ClosedProblemForCase, id string) int {
	for i, p := range ps {
		if p.ID == id {
			return i
		}
	}
	return 0
}

// StartCaseBuilder — worker'da 5 dakikada bir, Redis lider kilidiyle.
func StartCaseBuilder(ctx context.Context, st CaseStore, lock cache.Lock) {
	leader := cache.NewLeaderHolder(lock, caseLockKey, cache.LeaderTTL(caseInterval))
	leader.Start(ctx)
	tick := func() {
		if !leader.IsLeader() {
			return
		}
		tctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		n, err := BuildCases(tctx, st, time.Now())
		cancel()
		switch {
		case err != nil:
			log.Printf("[cosre-cases] %v", err)
		case n > 0:
			log.Printf("[cosre-cases] %d taslak vaka kuruldu", n)
		}
	}
	tick()
	t := time.NewTicker(caseInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
