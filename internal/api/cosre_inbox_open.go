package api

// cosre_inbox_open.go — CoSRE Login Briefing Faz 1 (operatör kararı 2026-10-31):
// open_problems aracı, list_my_services açık-konu sayısı ve briefing kalem 6
// Service sayfası şeridinin (ServiceAttentionStrip → GET /api/inbox) AYNI
// çekirdeğini okur: inboxView (problems + exception/httperror grupları +
// anomaliler, aynı süzgeç / oluşum tabanı / öncelik). İkinci bir okuma
// yazılmaz — burada yalnız sorgu parametresi kurulur ve satır araç şekline
// çevrilir. Rota yok (HTTP'siz dikiş, mcptools.Deps.InboxOpen).
//
// Bilinen fark: şerit ayrıca istemci tarafında "problem değil" kararlı
// imzaları (problemVerdict.ts filterByVerdictView) gizler; o süzgeç sunucuda
// değil, burada da uygulanmaz.

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// inboxOpenValues — SAF: mcptools sorgusu → /api/inbox parametreleri (status=open).
func inboxOpenValues(q mcptools.InboxOpenQuery) url.Values {
	v := url.Values{}
	v.Set("status", "open")
	limit := q.Limit
	if limit <= 0 || limit > mcptools.InboxOpenScan {
		limit = mcptools.InboxOpenScan
	}
	v.Set("limit", strconv.Itoa(limit))
	if q.Team != "" {
		v.Set("team", q.Team)
	}
	if q.Service != "" {
		v.Set("service", q.Service)
	}
	if q.Env != "" {
		v.Set("env", q.Env)
	}
	return v
}

// inboxRowHref — SAF: frontend/src/lib/inboxHref.ts inboxItemHref'in Go eşi.
func inboxRowHref(it InboxItem) string {
	switch {
	case it.Kind == "problem" && it.Problem != nil:
		return "/problems?problem=" + url.QueryEscape(it.Problem.ID)
	case (it.Kind == "exception" || it.Kind == "httperror") && it.Exception != nil:
		return "/problems?exc=" + url.QueryEscape(it.Exception.Fingerprint)
	case it.Kind == "anomaly" && it.Anomaly != nil:
		return "/inbox?anomaly=" + url.QueryEscape(it.Anomaly.ID)
	case it.Kind == "incident" && it.Incident != nil:
		return "/incident?id=" + url.QueryEscape(it.Incident.ID)
	}
	return ""
}

// inboxRowsFrom — SAF: InboxItem → araç satırı.
func inboxRowsFrom(items []InboxItem) []mcptools.InboxRow {
	out := make([]mcptools.InboxRow, 0, len(items))
	for _, it := range items {
		out = append(out, mcptools.InboxRow{
			ID: it.ID, Kind: it.Kind, Priority: it.Priority, Severity: it.Severity, Service: it.Service,
			Title: it.Title, Reason: mcptools.InboxReason(it.PriorityReason, it.Title),
			StartedAtNs: it.StartedAt, SinceUnix: it.StartedAt / 1e9, PageURL: inboxRowHref(it),
		})
	}
	return out
}

// inboxOpen — mcptools.InboxOpenReader: inbox çekirdeğini önbelleksiz koşar
// (araç kendi Memo'sunu, worker kendi tikini taşır).
func (s *Server) inboxOpen(ctx context.Context, q mcptools.InboxOpenQuery) (mcptools.InboxOpenResult, error) {
	_, build := s.inboxView(inboxOpenValues(q))
	raw, err := build(ctx)
	if err != nil {
		return mcptools.InboxOpenResult{}, err
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return mcptools.InboxOpenResult{}, fmt.Errorf("inbox: beklenmeyen gövde %T", raw)
	}
	items, _ := m["items"].([]InboxItem)
	total, _ := m["total"].(int)
	capped, _ := m["scanCapped"].(bool)
	return mcptools.InboxOpenResult{Rows: inboxRowsFrom(items), Total: total, ScanCapped: capped}, nil
}
