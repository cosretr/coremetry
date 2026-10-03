package evaluator

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Burn pencereleri ve eşikleri chstore.BurnPolicies'te yaşar (v0.10.794,
// tek kaynak): evaluator alarmı, /api/copilot/explain-slo ve SLO modalı
// aynı çifti okur. Gerekçe ve SRE Workbook değerleri orada.

// sloBurnStore — burn pasının dokunduğu store yüzeyi (v0.10.1081). Gerçekte
// *chstore.Store; testler bellek içi sahteyle koşar (CH'siz) — "kapalıyken
// eşik aşılsa da problem yok" sözleşmesi ancak böyle uçtan uca çivilenir.
type sloBurnStore interface {
	ListSLOs(ctx context.Context) ([]chstore.SLO, error)
	ComputeSLOBurnRate(ctx context.Context, o chstore.SLO, window time.Duration) (float64, uint64, error)
	OpenProblemsSnapshot(ctx context.Context) (*chstore.OpenProblems, error)
	UpsertProblem(ctx context.Context, p chstore.Problem) error
	AttachProblemToIncident(ctx context.Context, p chstore.Problem) (*chstore.Incident, error)
}

// sloBurnDisabledReason — bayrak kapalıyken (operatör sonradan kapattı)
// açık kalan burn problemlerinin kapanış gerekçesi. Tek seferlik göçün
// gerekçesinden (sloBurnDefaultOffReason) bilerek AYRI: teşhiste "varsayılan
// göç" ile "operatör kapattı" ayrışsın.
const sloBurnDisabledReason = "slo burn problems disabled"

// isSLOBurnRule — SAF: SLO burn-rate kural kimliği mi ("slo:<id>:<sev>").
// escalationExempt ile aynı önek (tip sistemi kural id önekidir).
func isSLOBurnRule(ruleID string) bool { return strings.HasPrefix(ruleID, "slo:") }

// evaluateSLOs runs the burn-rate alarm pass — fired by the
// main evaluator tick. Single-leader gating is already in place
// at the caller; this just walks SLOs and opens / closes
// Problems as the burn rate crosses thresholds.
//
// v0.10.1081 (operatör: "SLO burn rate problem olmasın, çıkar. SLO ile ilgili
// beklentim yok.") — Problem üretimi problem_priority.sloBurnProblems
// bayrağına bağlı, VARSAYILAN KAPALI. Kapalıyken pas hiç ölçmez (SLO ×
// politika × iki burn sorgusu CH'ye gitmez); SLO sayfası ve burn grafikleri
// kendi okumalarıyla çalışmaya devam eder.
func (e *Evaluator) evaluateSLOs(ctx context.Context) {
	cfg := chstore.CurrentProblemPriority()
	e.evaluateSLOsWith(ctx, e.store, cfg.SLOBurnProblemsEnabled(), chstore.ProblemPriorityPublished())
}

// evaluateSLOsWith — kapının gövdesi, store enjekte edilebilir.
//
//   - enabled: bugünkü davranış birebir (aç / tazele / kapat).
//   - !enabled && published: açık slo:* problemleri dürüst gerekçeyle
//     kapatılır (operatör bayrağı kapattı; yoksa bayat süpürme ~3 aralık
//     sonra "source silent" diye yanlış gerekçeyle kapatırdı). Yeni problem YOK.
//   - !enabled && !published: ayar bu süreçte hiç okunamadı — "kapalı" bir
//     tahmin; yeni problem açılmaz ama açıklara DOKUNULMAZ (tek yönlü eylem
//     tahminle yapılmaz — aiops §11).
func (e *Evaluator) evaluateSLOsWith(ctx context.Context, st sloBurnStore, enabled, published bool) {
	if !enabled {
		if published {
			e.resolveDisabledSLOProblems(ctx, st, time.Now())
		}
		return
	}
	slos, err := st.ListSLOs(ctx)
	if err != nil {
		log.Printf("[evaluator/slo] list: %v", err)
		return
	}
	for _, slo := range slos {
		for _, pol := range chstore.BurnPolicies {
			e.evaluateSLOBurn(ctx, st, slo, pol)
		}
	}
}

// resolveDisabledSLOProblems — bayrak kapalıyken açık (open + acknowledged)
// slo:* problemlerini normal kapatma yolundan kapatır (MarkResolved, Value
// ezilmez — v0.9.977); incident'ları aynı tikin kaskadı kapatır. Snapshot
// okunamazsa hiçbir şey yapmaz (sonraki tik dener). Kapanış bildirim
// göndermez (bugünkü resolve dalı gibi).
func (e *Evaluator) resolveDisabledSLOProblems(ctx context.Context, st sloBurnStore, now time.Time) {
	snap, err := st.OpenProblemsSnapshot(ctx)
	if err != nil {
		return
	}
	for _, p := range sloBurnProblemsToResolve(snap.All()) {
		chstore.MarkResolved(&p, now.UnixNano())
		p.Description = appendResolveSuffix(p.Description, sloBurnDisabledReason)
		if err := st.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator/slo] disabled resolve %s: %v", p.ID, err)
			continue
		}
		e.countResolved()
		log.Printf("[evaluator/slo] PROBLEM AUTO-RESOLVED (%s): %s · %s", sloBurnDisabledReason, p.Service, p.RuleName)
	}
}

// sloBurnProblemsToResolve — SAF: açık listeden SLO burn problemleri, kopya
// olarak (snapshot işaretçileri salt-okunur — v0.10.156).
func sloBurnProblemsToResolve(open []*chstore.Problem) []chstore.Problem {
	var out []chstore.Problem
	for _, p := range open {
		if p == nil || !isSLOBurnRule(p.RuleID) {
			continue
		}
		out = append(out, *p)
	}
	return out
}

// evaluateSLOBurn computes the fast + slow burn rates for one
// (slo, policy) pair and opens / refreshes / resolves a
// Problem accordingly. The Problem's rule_id is
// "slo:<id>:<severity>" so each (SLO × severity band) maps to
// at most one open Problem at a time.
func (e *Evaluator) evaluateSLOBurn(ctx context.Context, st sloBurnStore, slo chstore.SLO, pol chstore.BurnPolicy) {
	fastRate, fastTotal, err := st.ComputeSLOBurnRate(ctx, slo, pol.FastWindow)
	if err != nil {
		log.Printf("[evaluator/slo] %s fast burn: %v", slo.ID, err)
		return
	}
	slowRate, slowTotal, err := st.ComputeSLOBurnRate(ctx, slo, pol.SlowWindow)
	if err != nil {
		log.Printf("[evaluator/slo] %s slow burn: %v", slo.ID, err)
		return
	}

	// Need traffic on BOTH windows to make a meaningful
	// statement. A service that emitted no spans in the last
	// hour is silent, not necessarily "healthy" — and the
	// burn-rate division by total=0 isn't sane anyway.
	const minSpans = 50
	hasTraffic := fastTotal >= minSpans && slowTotal >= minSpans
	breached := hasTraffic && fastRate >= pol.FastRate && slowRate >= pol.SlowRate

	ruleID := fmt.Sprintf("slo:%s:%s", slo.ID, pol.Severity)
	// v0.10.156 — snapshot (v0.10.1081: enjekte edilen store'dan).
	var open *chstore.Problem
	if snap, err := st.OpenProblemsSnapshot(ctx); err == nil {
		open = snap.ByKey(ruleID, slo.Service)
	}
	hasOpen := open != nil && open.ID != ""

	switch {
	case breached && !hasOpen:
		p := chstore.Problem{
			ID:        newID(),
			RuleID:    ruleID,
			RuleName:  fmt.Sprintf("SLO burn-rate %s — %s", pol.Severity, slo.Name),
			Severity:  pol.Severity,
			Service:   slo.Service,
			Metric:    fmt.Sprintf("burn_rate_%dm", int(pol.FastWindow.Minutes())),
			Value:     fastRate,
			Threshold: pol.FastRate,
			Status:    "open",
			Description: fmt.Sprintf(
				"Burn rate above %s threshold for SLO %q (target %.2f%%). "+
					"Last %s: %.1fx — %s: %.1fx. At this rate the error budget "+
					"would be exhausted in days, not the SLO's %d-day window.",
				pol.Severity, slo.Name, slo.Target*100,
				pol.FastWindow, fastRate,
				pol.SlowWindow, slowRate,
				slo.WindowDays),
			StartedAt: time.Now().UnixNano(),
		}
		if err := st.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator/slo] open: %v", err)
			return
		}
		e.countOpened() // v0.9.550 — kalp atışı sayacı
		log.Printf("[evaluator/slo] PROBLEM OPENED: %s %s burn=%.1fx/%.1fx",
			slo.Service, pol.Severity, fastRate, slowRate)
		if _, err := st.AttachProblemToIncident(ctx, p); err != nil {
			log.Printf("[evaluator/slo] incident attach: %v", err)
		}
		if e.notifier != nil {
			go e.notifier.SendProblemAlert(context.Background(), p)
		}

	case breached && hasOpen:
		open.Value = fastRate
		// v0.10.800 — şiddet POLİTİKADAN (escalationExempt ile ikinci kelepçe):
		// daha önce yaş merdiveninin critical'a çektiği warning satırları bir
		// sonraki tikte bandına döner.
		open.Severity = pol.Severity
		_ = st.UpsertProblem(ctx, *open)

	case !breached && hasOpen:
		// Resolve when burn drops back. The SRE-book
		// recommendation is to require BOTH fast and slow
		// to be under-threshold to clear; we already require
		// that for breached, so a single failed condition is
		// enough for resolve.
		// v0.9.977 — yanma hızı ihlal anındaki değerinde kalır.
		chstore.MarkResolved(open, time.Now().UnixNano())
		_ = st.UpsertProblem(ctx, *open)
		e.countResolved() // v0.9.550 — kalp atışı sayacı
		log.Printf("[evaluator/slo] PROBLEM RESOLVED: %s %s burn=%.1fx/%.1fx",
			slo.Service, pol.Severity, fastRate, slowRate)
	}
}
