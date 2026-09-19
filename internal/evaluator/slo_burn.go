package evaluator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Burn pencereleri ve eşikleri chstore.BurnPolicies'te yaşar (v0.10.794,
// tek kaynak): evaluator alarmı, /api/copilot/explain-slo ve SLO modalı
// aynı çifti okur. Gerekçe ve SRE Workbook değerleri orada.

// evaluateSLOs runs the burn-rate alarm pass — fired by the
// main evaluator tick. Single-leader gating is already in place
// at the caller; this just walks SLOs and opens / closes
// Problems as the burn rate crosses thresholds.
func (e *Evaluator) evaluateSLOs(ctx context.Context) {
	slos, err := e.store.ListSLOs(ctx)
	if err != nil {
		log.Printf("[evaluator/slo] list: %v", err)
		return
	}
	for _, slo := range slos {
		for _, pol := range chstore.BurnPolicies {
			e.evaluateSLOBurn(ctx, slo, pol)
		}
	}
}

// evaluateSLOBurn computes the fast + slow burn rates for one
// (slo, policy) pair and opens / refreshes / resolves a
// Problem accordingly. The Problem's rule_id is
// "slo:<id>:<severity>" so each (SLO × severity band) maps to
// at most one open Problem at a time.
func (e *Evaluator) evaluateSLOBurn(ctx context.Context, slo chstore.SLO, pol chstore.BurnPolicy) {
	fastRate, fastTotal, err := e.store.ComputeSLOBurnRate(ctx, slo, pol.FastWindow)
	if err != nil {
		log.Printf("[evaluator/slo] %s fast burn: %v", slo.ID, err)
		return
	}
	slowRate, slowTotal, err := e.store.ComputeSLOBurnRate(ctx, slo, pol.SlowWindow)
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
	open, _ := e.findOpenViaSnapshot(ctx, ruleID, slo.Service) // v0.10.156 — snapshot
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
		if err := e.store.UpsertProblem(ctx, p); err != nil {
			log.Printf("[evaluator/slo] open: %v", err)
			return
		}
		e.countOpened() // v0.9.550 — kalp atışı sayacı
		log.Printf("[evaluator/slo] PROBLEM OPENED: %s %s burn=%.1fx/%.1fx",
			slo.Service, pol.Severity, fastRate, slowRate)
		if _, err := e.store.AttachProblemToIncident(ctx, p); err != nil {
			log.Printf("[evaluator/slo] incident attach: %v", err)
		}
		if e.notifier != nil {
			go e.notifier.SendProblemAlert(context.Background(), p)
		}

	case breached && hasOpen:
		open.Value = fastRate
		_ = e.store.UpsertProblem(ctx, *open)

	case !breached && hasOpen:
		// Resolve when burn drops back. The SRE-book
		// recommendation is to require BOTH fast and slow
		// to be under-threshold to clear; we already require
		// that for breached, so a single failed condition is
		// enough for resolve.
		// v0.9.977 — yanma hızı ihlal anındaki değerinde kalır.
		chstore.MarkResolved(open, time.Now().UnixNano())
		_ = e.store.UpsertProblem(ctx, *open)
		e.countResolved() // v0.9.550 — kalp atışı sayacı
		log.Printf("[evaluator/slo] PROBLEM RESOLVED: %s %s burn=%.1fx/%.1fx",
			slo.Service, pol.Severity, fastRate, slowRate)
	}
}
