// Package api handler for /admin/alert-tuning — surfaces the
// noisiest rules in a window plus per-rule actionable suggestions
// for the v0.5.127-129 dampening knobs. Admin-only read endpoint;
// cached 5 min because rule-noise pattern doesn't shift minute-
// to-minute and operators commonly load this page in bursts.
package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// NoisyRuleWithSuggestion enriches the raw NoisyRule with a
// heuristic suggestion string + structured deltas the UI can
// apply with one click via the existing AlertRule edit endpoint.
type NoisyRuleWithSuggestion struct {
	chstore.NoisyRule
	Suggestion   string `json:"suggestion"`
	SuggestedFor uint32 `json:"suggestedForSec,omitempty"`
	SuggestedMin uint32 `json:"suggestedMinSamples,omitempty"`
	SuggestedCD  uint32 `json:"suggestedCooldownSec,omitempty"`
	CurrentFor   uint32 `json:"currentForSec"`
	CurrentMin   uint32 `json:"currentMinSamples"`
	CurrentCD    uint32 `json:"currentCooldownSec"`
}

// alertTuningNoisyRules ranks rules by problems-opened count.
// Two heuristic-driven suggestions, applied in order:
//   - median duration < 60s + opens > 5      → bump for_sec to 60-120
//     ("flap" pattern — short-lived breaches)
//   - opens > 20 in 24h + no cooldown set    → suggest cooldown_sec = 300
//     ("re-open churn" — threshold jitter)
//
// The thresholds are conservative; operators get a starting
// recommendation, not an auto-tune. We never overwrite a setting
// that the operator already non-zero'd.
func (s *Server) alertTuningNoisyRules(w http.ResponseWriter, r *http.Request) {
	claims := auth.FromContext(r.Context())
	if claims == nil || claims.Role != auth.RoleAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	since := parseDuration(r.URL.Query().Get("since"), 24*time.Hour)
	limit := parseInt(r.URL.Query().Get("limit"), 30)
	to := time.Now()
	from := to.Add(-since)
	// Cache the heavy GROUP BY for 5 min — noise patterns don't
	// change minute-to-minute, and a fleet of operators loading
	// /admin/alert-tuning during morning triage would otherwise
	// hammer CH with duplicate queries.
	key := fmt.Sprintf("alert-tuning-noisy:since=%s:limit=%d",
		since.String(), limit)
	s.serveCached(w, r, key, 5*time.Minute, func(ctx context.Context) (any, error) {
		out, err := s.noisyRulesWithSuggestions(ctx, from, to, limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"rules":    out,
			"from":     from.UnixNano(),
			"to":       to.UnixNano(),
			"sinceSec": int64(since.Seconds()),
		}, nil
	})
}

// noisyRulesWithSuggestions — tuning raporunun ortak çekirdeği
// (v0.9.1079'da handler gövdesinden çıkarıldı; explain-alert-noise da
// aynı paketi kullanır). Davranış bire bir aynı.
func (s *Server) noisyRulesWithSuggestions(ctx context.Context, from, to time.Time, limit int) ([]NoisyRuleWithSuggestion, error) {
	// Fetch the rule list once to surface current knob values
	// + scope the suggestion to "this rule already has X set?"
	allRules, err := s.store.ListAlertRules(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]chstore.AlertRule, len(allRules))
	for _, ar := range allRules {
		byID[ar.ID] = ar
	}
	// v0.10.1069 — devre dışı kural "sıkılabilir gürültü" değildir: artık
	// açılmaz. Yerleşikler varsayılan kapandığında (builtins_default_off.go)
	// 24 saatlik pencere onları hâlâ en gürültülü diye listeliyor, panel zaten
	// kapalı kural için "Disable" öneriyordu. Kapalılar SQL sonrası düşülür;
	// boşalan yer için o kadar fazla satır istenir (store tavanı 200).
	rules, err := s.store.NoisyRules(ctx, from, to, noisyFetchLimit(limit, allRules))
	if err != nil {
		return nil, err
	}
	rules = dropDisabledNoisy(rules, byID, limit)
	out := make([]NoisyRuleWithSuggestion, 0, len(rules))
	for _, n := range rules {
		row := NoisyRuleWithSuggestion{NoisyRule: n}
		if cur, ok := byID[n.RuleID]; ok {
			row.CurrentFor = cur.ForSec
			row.CurrentMin = cur.MinSamples
			row.CurrentCD = cur.CooldownSec
		}
		row.Suggestion, row.SuggestedFor, row.SuggestedMin, row.SuggestedCD =
			deriveSuggestion(n, byID[n.RuleID])
		out = append(out, row)
	}
	// Stable ordering — count desc already from query, but
	// when counts tie we want a deterministic name order so
	// the response is byte-stable across calls.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].OpenCount != out[j].OpenCount {
			return out[i].OpenCount > out[j].OpenCount
		}
		return out[i].RuleName < out[j].RuleName
	})
	return out, nil
}

// deriveSuggestion picks the highest-value dampener heuristic
// for one noisy rule and returns the suggestion string plus the
// concrete values the UI's one-click apply needs. Returns empty
// strings + zero values when the rule looks already-tuned or its
// pattern doesn't match a known shape.
func deriveSuggestion(n chstore.NoisyRule, cur chstore.AlertRule) (string, uint32, uint32, uint32) {
	switch {
	case n.OpenCount > 5 && n.MedianDurSec > 0 && n.MedianDurSec < 60 && cur.ForSec == 0:
		// Flap pattern — short-lived breaches. Sustained-breach
		// gate ≥ median × 2 absorbs them.
		suggested := uint32(120)
		if n.MedianDurSec < 30 {
			suggested = 60
		}
		return fmt.Sprintf("Median open %.0fs — %d times. Add for=%ds.",
			n.MedianDurSec, n.OpenCount, suggested), suggested, 0, 0
	case n.OpenCount > 20 && cur.CooldownSec == 0:
		// Re-open churn — value oscillates at threshold. 5-min
		// post-resolution cooldown absorbs the jitter.
		return fmt.Sprintf("%d opens in window — likely threshold jitter. Add cooldown=300s.",
			n.OpenCount), 0, 0, 300
	case n.OpenCount > 50:
		// Sheer volume — operator should tighten threshold
		// regardless. Don't suggest a specific value (depends
		// on metric), just flag.
		return fmt.Sprintf("%d opens — consider tightening the threshold.",
			n.OpenCount), 0, 0, 0
	}
	return "", 0, 0, 0
}

// noisyFetchLimit — SAF: dropDisabledNoisy'nin düşeceği satırlar için
// store'dan istenecek satır sayısı = limit + kapalı kural sayısı, store
// tavanında (200; üstü store'da 50'ye düşer) kelepçeli.
func noisyFetchLimit(limit int, rules []chstore.AlertRule) int {
	n := limit
	for _, r := range rules {
		if !r.Enabled {
			n++
		}
	}
	if n > 200 {
		n = 200
	}
	return n
}

// dropDisabledNoisy — SAF: şu an devre dışı olan kuralların satırlarını
// düşer, sırayı korur, limit'e keser. Kural tablosunda olmayan rule_id
// (anomali, SLO, self-health …) korunur — onları bu rapor zaten
// kural olarak ayarlayamaz ama gizlemek de bu değişikliğin işi değil.
func dropDisabledNoisy(rows []chstore.NoisyRule, byID map[string]chstore.AlertRule, limit int) []chstore.NoisyRule {
	out := make([]chstore.NoisyRule, 0, len(rows))
	for _, n := range rows {
		if cur, ok := byID[n.RuleID]; ok && !cur.Enabled {
			continue
		}
		out = append(out, n)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
