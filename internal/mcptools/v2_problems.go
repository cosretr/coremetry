package mcptools

// v2_problems.go — CoSRE Login Briefing Faz 1 dilim 1 (docs/cosre/briefing-audit.md
// §1 kalem 6, §10 R6; operatör onayı 2026-10-30).
//
// open_problems — kapsamdaki servis KÜMESİNİN açık (resolved dışı) Problem'leri.
// list_problems tekil `service` alır; v2 kapsamı bir küme olduğundan küme
// SQL'e iner (ProblemFilter.Services → service IN …, LIMIT'ten ÖNCE). Aynı
// okuma list_my_services satırına `open_problems` sayısını verir (R6 borcu:
// env + açık-Problem sayısı). Kimlik yok: kapsam döngüden enjekte edilir
// (applyUserScope `services` + `team`).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// OpenProblemsName — v2-yalnız aracın adı.
const OpenProblemsName = "open_problems"

const (
	openProblemsDefaultRows = 10
	openProblemsMaxRows     = 25
	// openProblemsScan — SQL taraması (problems state tablosu küçük; küme
	// SQL'de daraltılır, sayım taramanın tamamından).
	openProblemsScan = 500
	// OpenProblemsMaxBytes — gövde tavanı (≤ 2 KB); fazlası page_url.
	OpenProblemsMaxBytes  = 2048
	openProblemsCacheTTL  = 30 * time.Second
	openProblemsReasonMax = 140
)

// openProblemsNotStatuses — açık = resolved DIŞI her şey (v0.9.322: tanınmayan
// durum insan bekler; izin listesi değil dışlama).
var openProblemsNotStatuses = []string{"resolved"}

// errNoProblemScope — kapsam yok: model takımı sorar.
var errNoProblemScope = errors.New("no user scope: pass `services` (or `team`) — the caller's team is unknown; ask the user which team they mean, never guess one")

type openProblemsArgs struct {
	Services []string `json:"services,omitempty"`
	Team     string   `json:"team,omitempty"`
	Env      string   `json:"env,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// OpenProblemRow — open_problems satırı (briefing kalem 6 da bu şekli taşır).
type OpenProblemRow struct {
	ID        string `json:"id"`
	DisplayID string `json:"display_id,omitempty"`
	Priority  string `json:"priority,omitempty"`
	Severity  string `json:"severity,omitempty"`
	Service   string `json:"service"`
	Kind      string `json:"kind,omitempty"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	SinceUnix int64  `json:"since_unix"`
	PageURL   string `json:"page_url"`
}

// openProblemsCacheKey — SAF: TÜM girdiler (sıralı küme FNV, takım, env, satır, dakika).
func openProblemsCacheKey(services []string, team, env string, limit int, now time.Time) string {
	return fmt.Sprintf("mcp:open_problems:v1:%s:%s:%s:%d:%d", fnvHex(services...), fnvHex(team), fnvHex(env), limit, now.Unix()/60)
}

// ProblemPageURL — SAF: tek Problem'in sayfası (kök-göreli).
func ProblemPageURL(id string) string { return "/problems?problem=" + url.QueryEscape(id) }

// OpenProblemsPageURL — SAF: kümenin liste sayfası. Tek servis → servis süzgeci;
// takım → Inbox sahip süzgeci (yalnız ownerTeam; OR semantiği orada yok); yoksa genel.
func OpenProblemsPageURL(services []string, team string) string {
	if len(services) == 1 {
		return "/problems?service=" + url.QueryEscape(services[0])
	}
	if t := strings.TrimSpace(team); t != "" {
		return "/inbox?owner=" + url.QueryEscape(t)
	}
	return "/problems"
}

// problemReason — SAF: satırın tek-satır nedeni (öncelik nedeni > açıklama > kural adı), kırpılmış.
func problemReason(p chstore.Problem) string {
	r := strings.TrimSpace(p.PriorityReason)
	if r == "" {
		r = strings.TrimSpace(p.Description)
	}
	if r == "" {
		r = strings.TrimSpace(p.RuleName)
	}
	r = strings.Join(strings.Fields(r), " ")
	if rs := []rune(r); len(rs) > openProblemsReasonMax {
		r = string(rs[:openProblemsReasonMax-1]) + "…"
	}
	return r
}

// OpenProblemRows — SAF: Problem → satır (sıra çağırandan: öncelik sırası).
func OpenProblemRows(probs []chstore.Problem) []OpenProblemRow {
	out := make([]OpenProblemRow, 0, len(probs))
	for _, p := range probs {
		out = append(out, OpenProblemRow{
			ID: p.ID, DisplayID: p.DisplayID, Priority: p.Priority, Severity: p.Severity,
			Service: p.Service, Kind: p.Kind, Status: p.Status, Reason: problemReason(p),
			SinceUnix: p.StartedAt / int64(time.Second), PageURL: ProblemPageURL(p.ID),
		})
	}
	return out
}

// OpenProblemCounts — SAF: servis başına açık Problem sayısı.
func OpenProblemCounts(probs []chstore.Problem) map[string]int {
	out := map[string]int{}
	for _, p := range probs {
		out[p.Service]++
	}
	return out
}

// ReadOpenProblems — kümenin açık Problem'leri, öncelik zenginleştirmeli ve
// öncelik sıralı. Boş küme → okuma yok (boş IN = tüm filo değil, hiçbir şey).
// enrich=false yalnız sayım isteyenler için (list_my_services).
func ReadOpenProblems(ctx context.Context, d Deps, services []string, env string, enrich bool) ([]chstore.Problem, error) {
	if len(services) == 0 || d.Store == nil {
		return nil, nil
	}
	rows, err := d.Store.ListProblems(ctx, chstore.ProblemFilter{
		NotStatuses: openProblemsNotStatuses,
		Services:    services,
		Env:         env,
		Limit:       openProblemsScan,
	})
	if err != nil {
		return nil, err
	}
	if enrich {
		rows = d.Store.EnrichProblemsForRead(ctx, rows, mcpDeployLookback)
		rows = chstore.SortProblemsByPriority(rows)
	}
	return rows, nil
}

// openProblemsPayload — SAF: ≤ OpenProblemsMaxBytes gövde; sığmayan satır sondan düşer.
func openProblemsPayload(team, env string, services []string, trimmed int, probs []chstore.Problem, limit int) map[string]any {
	all := OpenProblemRows(probs)
	total := len(all)
	hasMore := total > limit
	if hasMore {
		all = all[:limit]
	}
	out := map[string]any{
		"scoped_services":  len(services) + trimmed,
		"trimmed_services": trimmed,
		"total_open":       total,
		"scan_capped":      len(probs) >= openProblemsScan,
		"order":            "priority (P1 first), then newest",
		"source":           "problems state table (FINAL), status != resolved; service set applied in SQL",
		"page_url":         OpenProblemsPageURL(services, team),
	}
	if team != "" {
		out["team"] = team
	}
	if env != "" {
		out["env"] = env
	}
	if total == 0 {
		out["reasons"] = []string{fmt.Sprintf("no open problem on the %d scoped service(s) — say so plainly; do not invent one", len(services)+trimmed)}
	}
	truncated := false
	for {
		out["problems"], out["count"] = all, len(all)
		b, err := json.Marshal(out)
		if err != nil || len(b) <= OpenProblemsMaxBytes || len(all) == 0 {
			break
		}
		all = all[:len(all)-1]
		truncated = true
	}
	out["has_more"] = hasMore || truncated
	if truncated {
		out["truncated"] = true
	}
	return out
}

func openProblemsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: OpenProblemsName,
		ShortDescription: "Kullanıcının servislerindeki AÇIK Problem'ler (çözülmemiş), P1 önce; kapsamı sunucu koyar. " +
			"\"Açık problemim var mı / ne alarm veriyor\" sorusunun girişi. env verilirse o ortamda koşan servislerle daraltır.",
		Description: "Return the OPEN (not resolved) Problems on a SET of services — normally the caller's own services, injected by the chat loop — with priority (P1/P2/P3), severity, service, kind, a one-line reason, start time and a page_url per problem. " +
			"Use it for 'do I have open problems', 'what is alerting in my area'. Scope is filled by the server; pass `services`/`team` only when the user names them. Without scope the call fails with a named error: ask the user which team, never guess. " +
			"`env` narrows to problems whose service ran in that environment in the last hour. Rows are priority-ordered; the body stays under 2 KB (at most 25 rows) and `total_open` counts every open problem in the set. " +
			"Reads the small problems state table once for the whole set (cached 30 s). For one named service's full history use list_problems; for a problem's evidence use get_problem.",
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
					"description": "Team label (ownerTeam OR sreTeam); used only when `services` is empty. Omit unless the user names a team.",
				},
				"env": map[string]any{
					"type":        "string",
					"description": "Deployment environment (e.g. prod). Empty = all environments.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     openProblemsMaxRows,
					"description": "Max problem rows. Default 10, max 25.",
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
			if len(svcs) == 0 && team != "" {
				var err error
				if svcs, trimmed, err = ReadTeamServiceNames(ctx, d, team); err != nil {
					return nil, err
				}
			}
			if len(svcs) == 0 {
				return nil, errNoProblemScope
			}
			limit := clampLimit(a.Limit, openProblemsDefaultRows, openProblemsMaxRows)
			run := func(c context.Context) (map[string]any, error) {
				probs, err := ReadOpenProblems(c, d, svcs, env, true)
				if err != nil {
					return nil, err
				}
				return openProblemsPayload(team, env, svcs, trimmed, probs, limit), nil
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
