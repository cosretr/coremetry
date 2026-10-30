package mcptools

// v2_problems.go — CoSRE Login Briefing Faz 1 (docs/cosre/briefing-audit.md
// §1 kalem 6, §10 R6; operatör onayı 2026-10-30).
//
// open_problems — kapsamın AÇIK konuları. Operatör kararı 2026-10-31: kaynak
// yalnız problems tablosu DEĞİL, Service sayfası şeridiyle (ServiceAttentionStrip
// → /api/inbox) AYNI boru hattı — problems + exception/httperror grupları,
// şeridin süzgeci (v0.10.1695 Attention: "problem değil" gizli, SLO dipnot, anomali yok). Okuma api tarafındaki inbox
// çekirdeğinden (inboxView) Deps.InboxOpen dikişiyle gelir; burada ikinci bir
// uygulama YOK. Kapsam: takım → Inbox ?team= (owner VEYA sre — TeamServiceNames
// ile aynı birleşim); tek servis → ?service= (şeridin kendisi); takımsız çoklu
// küme → servis başına (≤ inboxFanoutMax) + birebir servis süzgeci.
// Aynı okuma list_my_services satırına `open_problems` sayısını verir (R6).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// OpenProblemsName — v2-yalnız aracın adı.
const OpenProblemsName = "open_problems"

const (
	openProblemsDefaultRows = 10
	openProblemsMaxRows     = 25
	// InboxOpenScan — inbox okumasının satır tavanı (inboxView üst sınırı 500).
	InboxOpenScan = 500
	// inboxFanoutMax — takımsız çoklu kümede servis başına okuma tavanı.
	inboxFanoutMax = 10
	// OpenProblemsMaxBytes — gövde tavanı (≤ 2 KB); fazlası page_url.
	OpenProblemsMaxBytes  = 2048
	openProblemsCacheTTL  = 30 * time.Second
	openProblemsReasonMax = 140
)

// InboxOpenQuery — inbox çekirdeğinin açık-konu sorgusu (status=open).
type InboxOpenQuery struct {
	Team, Service, Env string
	Limit              int
	// Attention — v0.10.1694 (operator-reported): servis sayfası şeridinin
	// istemci süzgeci de uygulanır (api serviceAttentionItems: "problem değil"
	// kararlı imzalar gizlenir, SLO burn-rate satır değil SLOCount, anomaliler
	// dışarıda). Rows/Total şeridin "N açık konu"su olur.
	Attention bool
}

// InboxRow — bir inbox satırının araç şekli (api.InboxItem'dan).
type InboxRow struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // problem | exception | httperror | anomaly | incident
	Priority    string `json:"priority,omitempty"`
	Severity    string `json:"severity,omitempty"`
	Service     string `json:"service"`
	Title       string `json:"title,omitempty"`
	Reason      string `json:"reason"`
	StartedAtNs int64  `json:"-"`
	SinceUnix   int64  `json:"since_unix"`
	PageURL     string `json:"page_url,omitempty"`
}

// InboxOpenResult — okumanın sonucu (Total = tavandan önceki aday sayısı).
type InboxOpenResult struct {
	Rows       []InboxRow
	Total      int
	ScanCapped bool
	// Truncated — v0.10.1694: inbox tavanından fazla aday vardı (sayı "en az").
	Truncated bool
	// SLOCount — v0.10.1694: Attention'da dipnota giden SLO burn-rate problemleri.
	SLOCount int
}

// InboxOpenReader — api'nin inbox çekirdeği (nil = bağlı değil).
type InboxOpenReader func(ctx context.Context, q InboxOpenQuery) (InboxOpenResult, error)

// errNoProblemScope — kapsam yok: model takımı sorar.
var errNoProblemScope = errors.New("no user scope: pass `services` (or `team`) — the caller's team is unknown; ask the user which team they mean, never guess one")

// errNoInboxReader — Deps.InboxOpen bağlı değil (yalnız doğrudan Deps kurulumunda).
var errNoInboxReader = errors.New("open-items reader is not wired on this server")

type openProblemsArgs struct {
	Services []string `json:"services,omitempty"`
	Team     string   `json:"team,omitempty"`
	Env      string   `json:"env,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// openProblemsCacheKey — SAF: TÜM girdiler (sıralı küme FNV, takım, env, satır, dakika).
func openProblemsCacheKey(services []string, team, env string, limit int, now time.Time) string {
	return fmt.Sprintf("mcp:open_problems:v2:%s:%s:%s:%d:%d", fnvHex(services...), fnvHex(team), fnvHex(env), limit, now.Unix()/60)
}

// OpenProblemsPageURL — SAF: kümenin Inbox görünümü (takım → ?team= birleşimi;
// tek servis → şeridin "tümü" bağlantısı; yoksa genel).
func OpenProblemsPageURL(services []string, team string) string {
	if t := strings.TrimSpace(team); t != "" {
		return "/inbox?team=" + url.QueryEscape(t)
	}
	if len(services) == 1 {
		return "/inbox?service=" + url.QueryEscape(services[0])
	}
	return "/inbox"
}

// InboxReason — SAF: satırın tek-satır nedeni (öncelik nedeni > başlık), boşluk katlanmış, kırpılmış.
func InboxReason(priorityReason, title string) string {
	r := strings.TrimSpace(priorityReason)
	if r == "" {
		r = strings.TrimSpace(title)
	}
	r = strings.Join(strings.Fields(r), " ")
	if rs := []rune(r); len(rs) > openProblemsReasonMax {
		r = string(rs[:openProblemsReasonMax-1]) + "…"
	}
	return r
}

// InboxOpenPlan — SAF: kapsam → inbox sorguları. exact=true → satırlar
// birebir servis kümesiyle süzülür (inbox ?service= alt-dize eşler);
// unread = fan-out tavanına takılıp okunmayan servis.
//
// v0.10.1695 (operatör kararı 2026-10-31) — her sorgu Attention: şeridin
// süzgeci ("problem değil" gizli, SLO dipnot, anomali yok). Tek servis + env
// yok → şeridin SORGUSU birebir (ServiceAttentionQuery: limit 20, aynı
// önbellek girdisi) → sayı şeritle aynı. Takım → ?team= aynı süzgeçle.
func InboxOpenPlan(team string, services []string, env string, limit int) (qs []InboxOpenQuery, exact bool, unread int) {
	if t := strings.TrimSpace(team); t != "" {
		return []InboxOpenQuery{{Team: t, Env: env, Limit: limit, Attention: true}}, false, 0
	}
	if len(services) == 1 {
		return []InboxOpenQuery{serviceStripQuery(services[0], env, limit)}, false, 0
	}
	n := len(services)
	if n > inboxFanoutMax {
		unread, n = n-inboxFanoutMax, inboxFanoutMax
	}
	for _, s := range services[:n] {
		qs = append(qs, serviceStripQuery(s, env, limit))
	}
	return qs, true, unread
}

// serviceStripQuery — SAF: env yoksa şeridin sorgusu birebir; env varsa aynı süzgeç + env.
func serviceStripQuery(service, env string, limit int) InboxOpenQuery {
	if env == "" {
		return ServiceAttentionQuery(service)
	}
	return InboxOpenQuery{Service: service, Env: env, Limit: limit, Attention: true}
}

// ReadInboxOpen — kapsamın açık konuları, inbox çekirdeğinden (plan yukarıda).
// Satırlar inbox sırası (öncelik); çoklu okumada birleşim + tekilleştirme.
func ReadInboxOpen(ctx context.Context, d Deps, team string, services []string, env string, limit int) (InboxOpenResult, int, error) {
	if d.InboxOpen == nil {
		return InboxOpenResult{}, 0, errNoInboxReader
	}
	qs, exact, unread := InboxOpenPlan(team, services, env, limit)
	if len(qs) == 1 {
		r, err := d.InboxOpen(ctx, qs[0])
		return r, unread, err
	}
	allow := make(map[string]bool, len(services))
	for _, s := range services {
		allow[s] = true
	}
	var out InboxOpenResult
	seen := map[string]bool{}
	for _, q := range qs {
		r, err := d.InboxOpen(ctx, q)
		if err != nil {
			return InboxOpenResult{}, 0, err
		}
		out.ScanCapped = out.ScanCapped || r.ScanCapped
		out.Truncated = out.Truncated || r.Truncated || r.Total > len(r.Rows)
		out.SLOCount += r.SLOCount
		for _, row := range r.Rows {
			if (exact && !allow[row.Service]) || seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			out.Rows = append(out.Rows, row)
		}
	}
	out.Total = len(out.Rows)
	return out, unread, nil
}

// OpenCounts — SAF: servis başına açık konu sayısı.
func OpenCounts(rows []InboxRow) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[r.Service]++
	}
	return out
}

// openProblemsPayload — SAF: ≤ OpenProblemsMaxBytes gövde; sığmayan satır sondan düşer.
func openProblemsPayload(team, env string, services []string, trimmed, unread int, res InboxOpenResult, limit int) map[string]any {
	all := append([]InboxRow(nil), res.Rows...)
	if all == nil {
		all = []InboxRow{}
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	out := map[string]any{
		"scoped_services":  len(services) + trimmed,
		"trimmed_services": trimmed,
		"total_open":       res.Total,
		"scan_capped":      res.ScanCapped,
		"truncated_source": res.Truncated,
		"order":            "inbox priority order (P1 first)",
		"source":           "Service page attention strip set (Inbox pipeline, status=open: alert Problems + exception/http-error groups; items marked not-a-problem hidden, SLO burn-rate counted in slo_burn_problems, anomalies excluded)",
		"page_url":         OpenProblemsPageURL(services, team),
	}
	if unread > 0 {
		out["unread_services"] = unread
	}
	if team != "" {
		out["team"] = team
	}
	if env != "" {
		out["env"] = env
	}
	if res.Total == 0 {
		out["reasons"] = []string{fmt.Sprintf("no open item on the %d scoped service(s) — say so plainly; do not invent one", len(services)+trimmed)}
	}
	truncated := false
	for {
		out["items"], out["count"] = all, len(all)
		b, err := json.Marshal(out)
		if err != nil || len(b) <= OpenProblemsMaxBytes || len(all) == 0 {
			break
		}
		all = all[:len(all)-1]
		truncated = true
	}
	if res.SLOCount > 0 {
		out["slo_burn_problems"] = res.SLOCount // şerit gibi: satır değil dipnot
	}
	out["has_more"] = hasMore || truncated || res.Truncated || res.Total > len(res.Rows)
	if truncated {
		out["truncated"] = true
	}
	return out
}

func openProblemsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: OpenProblemsName,
		ShortDescription: "Kullanıcının servislerindeki AÇIK konular (Problem + exception/HTTP hata grubu), P1 önce — " +
			"Service sayfası şeridi / Inbox ile aynı okuma. Kapsamı sunucu koyar. \"Açık problemim var mı / ne alarm veriyor\" sorusunun girişi.",
		Description: "Return the OPEN items on the caller's services — the same Inbox pipeline the Service page attention strip and /inbox read: alert-rule Problems and exception / HTTP-error groups (items marked not-a-problem hidden, SLO burn-rate problems counted in `slo_burn_problems`, anomalies excluded — exactly what the strip counts), with priority (P1/P2/P3), severity, kind, service, a one-line reason, start time and a page_url per item. " +
			"Use it for 'do I have open problems', 'what is alerting in my area'. Scope is filled by the server (team → owner OR sre services); pass `services`/`team` only when the user names them. Without scope the call fails with a named error: ask the user which team, never guess. " +
			"`env` keeps items whose service ran in that environment in the last hour. Rows are Inbox-ordered; the body stays under 2 KB (at most 25 rows) and `total_open` is the Inbox count. " +
			"Cached 30 s. For one problem's evidence use get_problem; for exception detail use the exception tools.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"services": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"maxItems":    MaxTeamServices,
					"description": "Service names. Normally filled by the server from the user's team — omit it.",
				},
				"team": map[string]any{
					"type":        "string",
					"description": "Team label (ownerTeam OR sreTeam). Filled by the server with the user's team. Omit unless the user names a team.",
				},
				"env": map[string]any{
					"type":        "string",
					"description": "Deployment environment (e.g. prod). Empty = all environments.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     openProblemsMaxRows,
					"description": "Max rows. Default 10, max 25.",
				},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a openProblemsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			team, env := strings.TrimSpace(a.Team), strings.TrimSpace(a.Env)
			svcs, trimmed := normalizeMyServices(a.Services)
			if len(svcs) == 0 && team == "" {
				return nil, errNoProblemScope
			}
			limit := clampLimit(a.Limit, openProblemsDefaultRows, openProblemsMaxRows)
			run := func(c context.Context) (map[string]any, error) {
				res, unread, err := ReadInboxOpen(c, d, team, svcs, env, InboxOpenScan)
				if err != nil {
					return nil, err
				}
				return openProblemsPayload(team, env, svcs, trimmed, unread, res, limit), nil
			}
			if d.Memo == nil {
				return run(ctx)
			}
			_, now := rangeWindow(ctx, 60) // çıpalı "şimdi" (anchor_test)
			body, err := d.Memo(ctx, openProblemsCacheKey(svcs, team, env, limit, now), openProblemsCacheTTL, func(c context.Context) (any, bool, error) {
				out, err := run(c)
				return out, err == nil, err
			})
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		},
	}
}
