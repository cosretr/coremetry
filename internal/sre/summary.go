package sre

// summary.go — v0.10.1287 (AI SRE Faz 1b): sohbetin kayıtlı araştırmayı
// yeniden kullanması.
//
//   - get_problem `investigation{at, state, findings≤3, summary}`
//     (mcptools.InvestigationSummary; api/mcp_deps.go Deps.Investigation →
//     LoadSummary).
//   - guided kök-neden demeti "KAYITLI ARAŞTIRMA (HH:MM)" bloğu
//     (RenderSavedTR; api/copilot_guided.go).
//
// Paylaşılan kayıt (shared) kaynağına BİR atlamayla bağlanır; durum "shared"
// kalır, bulgu ve özet kaynağın. Yalnız okuma — kayıt yazılmaz.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// SummaryOf — SAF: kayıt → sohbet özeti. Kapı kapalıyken (Gated) özet metni
// taşınmaz — sabit "AI özeti yok" cümlesi modele kanıt gibi gitmesin.
func SummaryOf(inv Investigation) mcptools.InvestigationSummary {
	at := inv.FinishedAt
	if at == 0 {
		at = inv.StartedAt
	}
	if at == 0 {
		at = inv.EnqueuedAt
	}
	out := mcptools.InvestigationSummary{At: at, State: inv.State, Findings: []string{}, SharedFrom: inv.SharedFrom}
	for i, f := range inv.Findings {
		if i >= 3 {
			break
		}
		line := fmt.Sprintf("[%s] %s %s", f.Level, kindTR(f.Kind), f.Name)
		if len(f.Reasons) > 0 {
			line += ": " + strings.Join(f.Reasons, "; ")
		}
		out.Findings = append(out.Findings, truncRunes(line, 300))
	}
	if inv.Summary != nil && !inv.Summary.Gated {
		out.Summary = inv.Summary.Text
	}
	out.Hypotheses = summaryHypotheses(inv.Hypotheses)
	out.Recommendations = summaryRecommendations(inv.Recommendations) // v0.10.1293 — ≤2, elenen düşer
	return out
}

// summaryHypotheses — SAF (v0.10.1288): kartın ilk ≤3 hipotezi → sohbet
// özeti (destek / karşı gerekçeler ≤2'şer, operatör işareti).
func summaryHypotheses(hs []Hypothesis) []mcptools.InvestigationHypothesis {
	var out []mcptools.InvestigationHypothesis
	for i, h := range rulingOrder(hs) { // ekip kararı sırayı belirler (hypothesis_ruling.go)
		if i >= HypothesesShown {
			break
		}
		x := mcptools.InvestigationHypothesis{Kind: h.Kind, Label: h.Label, Category: h.Category, Score: h.Score, Band: h.Band}
		for _, c := range h.Checks {
			switch {
			case c.Outcome > 0 && len(x.Support) < 2:
				x.Support = append(x.Support, truncRunes(c.Reason, 200))
			case c.Outcome < 0 && len(x.Against) < 2:
				x.Against = append(x.Against, truncRunes(c.Reason, 200))
			}
		}
		if h.HumanState != nil {
			x.Operator = h.HumanState.State
		}
		out = append(out, x)
	}
	return out
}

// LoadSummary — problemin kaydının özeti (yoksa nil). Paylaşılan kayıt
// kaynağın bulgularını taşır.
func LoadSummary(ctx context.Context, st QueueStore, problemID string) (*mcptools.InvestigationSummary, error) {
	inv, ok, err := Load(ctx, st, problemID)
	if err != nil || !ok {
		return nil, err
	}
	if inv.State == StateShared && inv.SharedFrom != "" {
		src, sok, serr := Load(ctx, st, inv.SharedFrom)
		if serr == nil && sok {
			s := SummaryOf(src)
			s.State, s.SharedFrom = StateShared, inv.SharedFrom
			return &s, nil
		}
	}
	s := SummaryOf(inv)
	return &s, nil
}

// RenderSavedTR — SAF: guided kök-neden demetinin "KAYITLI ARAŞTIRMA (HH:MM)"
// bloğu (damga loc diliminde). Bitmemiş ya da bulgusuz-özetsiz kayıt "".
// v0.10.1462 (A1): Context Pack'ten (BuildSavedPack + RenderTR, chatBudget —
// tavansız, bugünkü davranış); metin bayt-bayt aynı.
func RenderSavedTR(problemRef string, s mcptools.InvestigationSummary, loc *time.Location) string {
	txt, _ := RenderTR(BuildSavedPack(problemRef, s, loc), chatBudget)
	return txt
}

// renderSavedHypothesesTR — SAF: sohbet bloğunun hipotez satırları (kod
// skoru; operatör işareti etiket olarak, skora girmez).
func renderSavedHypothesesTR(hs []mcptools.InvestigationHypothesis) string {
	var b strings.Builder
	for i, h := range hs {
		if i == 0 {
			b.WriteString("Ajanın hipotezleri (kod skoru — değiştirme):\n")
		}
		line := fmt.Sprintf("- %s: %s %.2f", h.Label, h.Band, h.Score)
		if len(h.Support) > 0 {
			line += " — " + strings.Join(h.Support, "; ")
		}
		switch h.Operator {
		case MarkDismissed:
			line += " (ekip reddetti — neden olarak söyleme)"
		case MarkConfirmed:
			line += " (ekip doğruladı)"
		}
		b.WriteString(truncRunes(line, 400) + "\n")
	}
	return b.String()
}
