// v0.10.1694 (operator-reported) — servisin "Açık konular" kümesi, şeritle tek okuma.
//
// Servis sayfasının üst şeridi (ServiceAttentionStrip) /api/inbox'u
// service + status=open + limit=20 ile okur ve istemcide üç süzgeç uygular:
// "problem değil" kararlı imzalar gizlenir (filterByVerdictView 'triage'),
// SLO burn-rate problemleri satır değil dipnot, anomaliler dışarıda
// (attentionRows). CoSRE'nin service_health_sweep'i ise yalnız problems
// tablosunu okuyordu: exception/httperror grupları Inbox'ta 13 satır iken
// sohbet "veri yok" dedi.
//
// inboxOpenAttention (Deps.InboxOpen + InboxOpenQuery.Attention) /api/inbox'un
// AYNI inboxView'unu, AYNI önbellek girdisini (şeridin anahtarı) okur ve
// şeridin süzgecinin Go ikizini (serviceAttentionItems) uygular.
package api

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// serviceOpenItemsView — inboxView gövdesinden okunan alanlar.
type serviceOpenItemsView struct {
	Items      []InboxItem `json:"items"`
	Truncated  bool        `json:"truncated"`
	ScanCapped bool        `json:"scanCapped"`
}

// inboxOpenAttention — şeridin kümesi. Karar listesi okunamazsa hiçbir satır
// gizlenmez (şeridin güvenli tarafı).
func (s *Server) inboxOpenAttention(ctx context.Context, q mcptools.InboxOpenQuery) (mcptools.InboxOpenResult, error) {
	key, build := s.inboxView(inboxOpenValues(q))
	body, _, err := s.cachedJSON(ctx, key, inboxListTTL, false, build)
	if err != nil {
		return mcptools.InboxOpenResult{}, err
	}
	var view serviceOpenItemsView
	if err := json.Unmarshal(body, &view); err != nil {
		return mcptools.InboxOpenResult{}, err
	}
	var noise map[string]struct{}
	if s.store != nil {
		if list, lerr := s.store.ListProblemVerdicts(ctx); lerr == nil {
			noise = chstore.NoiseVerdictSignatures(list)
		}
	}
	kept, slo := serviceAttentionItems(view.Items, noise)
	return mcptools.InboxOpenResult{Rows: inboxRowsFrom(kept), Total: len(kept), ScanCapped: view.ScanCapped,
		Truncated: view.Truncated, SLOCount: slo}, nil
}

// inboxItemVerdictSignature — SAF: FE lib/problemVerdict.ts inboxSignature'ın
// Go ikizi. "" = öğretilemeyen satır (incident, kural kimliksiz problem).
func inboxItemVerdictSignature(it InboxItem) string {
	switch it.Kind {
	case "problem":
		if it.Problem != nil {
			return chstore.RuleProblemVerdictSignature(it.Problem.RuleID, it.Service)
		}
	case "exception", "httperror":
		if it.Exception != nil && it.Exception.Fingerprint != "" {
			return "e:" + it.Exception.Fingerprint
		}
	case "anomaly":
		if it.Anomaly != nil && it.Anomaly.Kind != "" {
			return "a:" + it.Anomaly.Kind + "|" + it.Service + "|" + it.Anomaly.Pattern
		}
	}
	return ""
}

// serviceAttentionItems — SAF: şeridin süzgeci (FE filterByVerdictView
// 'triage' + attentionRows varsayılanı). Sıra korunur (sunucu sırası).
// slo = dipnota giden SLO burn-rate problemleri.
func serviceAttentionItems(items []InboxItem, noise map[string]struct{}) (kept []InboxItem, slo int) {
	kept = make([]InboxItem, 0, len(items))
	for _, it := range items {
		if sig := inboxItemVerdictSignature(it); sig != "" {
			if _, isNoise := noise[sig]; isNoise {
				continue
			}
		}
		if it.Kind == "problem" && it.Problem != nil && strings.HasPrefix(it.Problem.RuleID, "slo:") {
			slo++
			continue
		}
		if !inboxItemLinkable(it) {
			continue
		}
		kept = append(kept, it)
	}
	return kept, slo
}

// inboxItemLinkable — FE inboxItemHref != null, anomaliler HARİÇ (şerit
// varsayılanı includeAnomalies=false).
func inboxItemLinkable(it InboxItem) bool {
	switch it.Kind {
	case "problem":
		return it.Problem != nil
	case "exception", "httperror":
		return it.Exception != nil
	case "incident":
		return it.Incident != nil
	}
	return false
}
