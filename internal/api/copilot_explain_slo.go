package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// copilot_explain_slo.go — POST /api/copilot/explain-slo/{id} (v0.9.1083
// yörünge kanıtı; v0.10.794'te api.go'dan buraya taşındı — ratchet aşağı).
// Burn pencereleri chstore.BurnExplainPolicy'den: evaluator alarmıyla aynı çift.

func (s *Server) copilotExplainSLO(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	slo, err := s.store.GetSLO(r.Context(), id)
	if err != nil || slo == nil {
		http.Error(w, "SLO not found", http.StatusNotFound)
		return
	}
	status, _ := s.store.ComputeSLOStatus(r.Context(), *slo)
	// Two-window burn samples — Workbook multi-burn-rate pattern.
	// v0.10.794 — pencereler evaluator'ın critical bandıyla AYNI
	// (chstore.BurnExplainPolicy): önce 5 dk / 1 sa ölçülüyor, problemde
	// 1 sa / 6 sa görünüyordu; operatör iki yerde iki farklı "fast burn"
	// okuyordu.
	pol := chstore.BurnExplainPolicy()
	fastRate, fastTotal, _ := s.store.ComputeSLOBurnRate(r.Context(), *slo, pol.FastWindow)
	slowRate, slowTotal, _ := s.store.ComputeSLOBurnRate(r.Context(), *slo, pol.SlowWindow)

	var sb strings.Builder
	fmt.Fprintf(&sb, "SLO: %s (service=%s)\n", slo.Name, slo.Service)
	switch slo.SLIType {
	case "latency":
		fmt.Fprintf(&sb, "Type: latency, threshold=%.0fms\n", slo.ThresholdMs)
	default:
		fmt.Fprintf(&sb, "Type: %s\n", slo.SLIType)
	}
	fmt.Fprintf(&sb, "Target: %.3f%% over %d-day rolling window\n",
		slo.Target*100, slo.WindowDays)
	if slo.Operation != "" {
		fmt.Fprintf(&sb, "Scope: operation=%q\n", slo.Operation)
	}
	if status != nil {
		fmt.Fprintf(&sb,
			"Current: SLI=%.3f%% (%d good / %d total) · budget remaining=%.2f%% · long-window burn=%.2f · healthy=%v\n",
			status.SLI*100, status.Good, status.Total,
			status.BudgetRemaining*100, status.BurnRate, status.Healthy)
	}
	fmt.Fprintf(&sb, "Fast burn (%s): rate=%.2f, n=%d (alarm threshold %.1fx)\n", pol.FastWindow, fastRate, fastTotal, pol.FastRate)
	fmt.Fprintf(&sb, "Slow burn (%s): rate=%.2f, n=%d (alarm threshold %.1fx)\n", pol.SlowWindow, slowRate, slowTotal, pol.SlowRate)
	// v0.9.1083 (F3.4) — yörünge artık DETERMİNİSTİK girdide: /forecast
	// ve /burn-series uçları vardı ama anlatım beslemesi yoktu; prompt
	// "Y hours to exhaustion" isteyince model Y'yi UYDURUYORDU. Soft-
	// fail: üretilemezse kanıt bunu açıkça söyler, model de öyle der.
	fc, _ := s.store.ComputeSLOForecast(r.Context(), *slo, time.Hour)
	series, _ := s.store.ComputeSLOBurnSeries(r.Context(), *slo, 7)
	sb.WriteString(sloTrajectoryEvidence(fc, series))

	r, xid := withExchange(r)
	out, err := s.copilotExplain(r,
		copilot.SystemPromptSLOBurn(), sb.String())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{
		"explanation": out,
		"exchangeId":  xid,
		"status":      status,
		"fastBurn":    fastRate,
		"slowBurn":    slowRate,
		// v0.10.794 — FE etiketi bu pencereleri basar; eski istemci alanı görmezse
		// etiketsiz düşer (rolling deploy).
		"fastWindowS": int(pol.FastWindow.Seconds()),
		"slowWindowS": int(pol.SlowWindow.Seconds()),
		"fastRate":    pol.FastRate,
		"slowRate":    pol.SlowRate,
	})
}
