package mcptools

// v2_tools.go — CoSRE v2 araç seti dilim 2 (docs/cosre/tools-audit.md §9,
// §10 G9/G10, Q7/Q8; operatör onayı 2026-10-30).
//
// İki şey burada, TEK yerde (Q8 "bir registerV2Subset"):
//
//  1. registerV2Subset — yalnız v2 sohbetinin (POST /api/cosre/v2/chat)
//     kataloğuna eklenen araçlar. Bugün tek araç: list_my_services. Registry
//     (ToolList), dış MCP sunucusu (Register) ve v1 sohbeti (ChatToolList)
//     bu araçları GÖRMEZ — v1 kataloğu ve dış MCP tools/list bayt bayt aynı
//     (toolset_catalog_golden.json eski anahtarları değişmedi).
//  2. v2 MODEL alt kümesi — G10/Q7: genel sorgu araçları modelden gizlenir
//     (search_traces açık öznitelik regex'i, search_logs Lucene geçişi,
//     query_metric serbest metrik adı + yalnız onu besleyen
//     list_metric_labels) ve explain_log_spike şemasından serbest `query`
//     metni düşer. Registry ve dış MCP sunucusu bunları TUTAR (silme değil,
//     alt küme); api tarafında prepareStep planı uygular
//     (api/chat_user_scope.go v2SubsetPlan). Altın anahtarı `*/v2`.
//
// Kimlik YOK (team_ownership.go başlığı): list_my_services kullanıcıyı
// okumaz. Kapsam döngüden gelir — api user_scope'u (users.team →
// team_aliases → service_metadata owner OR sre, TeamServiceNames) çözer ve
// `services` + `team` argümanı olarak enjekte eder (applyUserScope). Argümansız
// çağrı (kapsam çözülemedi) adlandırılmış hata döner: model "hangi takım?"
// diye sorar, takım uydurmaz.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// ListMyServicesName — v2-yalnız aracın adı (api kapsam enjeksiyonu buna bakar).
const ListMyServicesName = "list_my_services"

const (
	myServicesDefaultRows = 10
	myServicesMaxRows     = 20
	// myServicesSilentNamesMax — sessiz servislerden adıyla söylenenler.
	myServicesSilentNamesMax = 5
	// MyServicesMaxBytes — gövde tavanı (brief "tool text ≤ 2 KB"); fazlası page_url.
	MyServicesMaxBytes = 2048
	myServicesCacheTTL = 30 * time.Second
)

// registerV2Subset — yalnız v2 sohbet kataloğuna eklenen araçlar (sıra = modelin gördüğü sıra).
func registerV2Subset(d Deps) []mcp.Tool {
	return []mcp.Tool{listMyServicesTool(d), openProblemsTool(d), findTracesTool(d), cosreSearchLogsTool(d), explainTraceTool(d)}
}

// V2MergeTools — SAF (dilim 3): v2 kataloğu = taban katalog + v2 araçları;
// aynı adlı v2 aracı (search_logs) tabandakinin yerini AYNI SIRADA alır,
// gerisi sona eklenir. Taban girdi değişmez.
func V2MergeTools(base, v2 []mcp.Tool) []mcp.Tool {
	byName := make(map[string]mcp.Tool, len(v2))
	for _, t := range v2 {
		byName[t.Name] = t
	}
	used := map[string]bool{}
	out := make([]mcp.Tool, 0, len(base)+len(v2))
	for _, t := range base {
		if r, ok := byName[t.Name]; ok {
			t = r
			used[t.Name] = true
		}
		out = append(out, t)
	}
	for _, t := range v2 {
		if !used[t.Name] {
			out = append(out, t)
		}
	}
	return out
}

// v2PageURLTools — sonucu kök-göreli `page_url` taşıyan v2 araçları (adım
// sonucunun href'i buradan; v1 search_logs çıktısında page_url yok).
var v2PageURLTools = map[string]bool{ListMyServicesName: true, OpenProblemsName: true, FindTracesName: true, SearchLogsV2Name: true, ExplainTraceName: true}

// V2PageURL — SAF: v2 aracının JSON sonucundaki kök-göreli page_url ("" = yok).
func V2PageURL(tool, content string) string {
	if !v2PageURLTools[tool] {
		return ""
	}
	var r struct {
		PageURL string `json:"page_url"`
	}
	if json.Unmarshal([]byte(content), &r) != nil {
		return ""
	}
	u := r.PageURL
	if !strings.HasPrefix(u, "/") || strings.HasPrefix(u, "//") || strings.HasPrefix(u, "/\\") || len(u) > 4096 {
		return ""
	}
	return u
}

// V2OnlyTools — api v2 döngüsünün yerli kataloğa eklediği araçlar.
func V2OnlyTools(d Deps) []mcp.Tool { return registerV2Subset(d) }

// v2HiddenTools — v2 model alt kümesinden düşen genel sorgu araçları (G10/Q7).
var v2HiddenTools = map[string]bool{
	tsSearchTraces:     true, // açık öznitelik filtresi + regex = genel CH taraması
	tsQueryMetric:      true, // serbest metrik adı + etiket = kılık değiştirmiş PromQL
	tsListMetricLabels: true, // yalnız query_metric'i besliyordu
}

// v2DroppedArgs — v2 şemasından düşen serbest metin argümanları (araç kalır).
var v2DroppedArgs = map[string][]string{
	ExplainLogSpikeName: {"query"}, // "/logs arama dili" = sorgu dili geçişi
}

// V2Hidden — araç v2 model alt kümesinin dışında mı (SAF).
func V2Hidden(name string) bool { return v2HiddenTools[name] }

// V2DroppedArgs — aracın v2'de düşen argümanları (SAF; nil = yok).
func V2DroppedArgs(name string) []string { return v2DroppedArgs[name] }

// V2NarrowSchema — SAF: v2'de düşen argümanlar şemadan çıkar (properties +
// required). Girdi DEĞİŞMEZ (sığ kopya); düşecek argüman yoksa girdi aynen.
func V2NarrowSchema(name string, schema map[string]any) map[string]any {
	drop := v2DroppedArgs[name]
	if len(drop) == 0 || schema == nil {
		return schema
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		out[k] = v
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		np := make(map[string]any, len(props))
		for k, v := range props {
			if !containsStr(drop, k) {
				np[k] = v
			}
		}
		out["properties"] = np
	}
	switch req := schema["required"].(type) {
	case []string:
		nr := make([]string, 0, len(req))
		for _, k := range req {
			if !containsStr(drop, k) {
				nr = append(nr, k)
			}
		}
		out["required"] = nr
	case []any:
		nr := make([]any, 0, len(req))
		for _, k := range req {
			if s, _ := k.(string); !containsStr(drop, s) {
				nr = append(nr, k)
			}
		}
		out["required"] = nr
	}
	return out
}

// V2StripArgs — SAF: v2'de düşen argümanlar çağrı argümanından da çıkar
// (geçmişte eski şemayla üretilmiş çağrı serbest metni taşımasın). Değişiklik
// yoksa girdi aynen; ikinci dönüş düşen anahtarlar.
func V2StripArgs(name string, args json.RawMessage) (json.RawMessage, []string) {
	drop := v2DroppedArgs[name]
	if len(drop) == 0 || len(strings.TrimSpace(string(args))) == 0 {
		return args, nil
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		return args, nil
	}
	var dropped []string
	for _, k := range drop {
		if _, has := m[k]; has {
			delete(m, k)
			dropped = append(dropped, k)
		}
	}
	if len(dropped) == 0 {
		return args, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return args, nil
	}
	return b, dropped
}

// V2ToolList — v2 modelinin gördüğü katalog (altın `*/v2`): sohbet kataloğu
// (araç setine göre) + registerV2Subset, gizliler düşer, şemalar daraltılır.
// api'nin prepareStep planı aynı üç kuralı (V2Hidden, V2NarrowSchema,
// V2OnlyTools) uygular; TestV2PlanMatchesV2ToolList ikisini eşitler.
func V2ToolList(d Deps, toolset string) []mcp.Tool {
	// dilim 3 — v1 search_logs (Lucene) v2 search_logs (düz kelime) ile ADIYLA değişir.
	all := V2MergeTools(ChatToolListFor(d, toolset), registerV2Subset(d))
	out := make([]mcp.Tool, 0, len(all))
	for _, t := range all {
		if V2Hidden(t.Name) {
			continue
		}
		t.InputSchema = V2NarrowSchema(t.Name, t.InputSchema)
		out = append(out, t)
	}
	return out
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ─── list_my_services ──────────────────────────────────────────

type listMyServicesArgs struct {
	Services []string `json:"services,omitempty"`
	Team     string   `json:"team,omitempty"`
	Env      string   `json:"env,omitempty"`
	RangeS   int      `json:"range_s,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// errNoMyScope — kapsam yok: model takımı SORAR (uydurmaz).
var errNoMyScope = errors.New("no user scope: the caller's team is unknown or owns no service — ask the user which team they mean (list_teams gives exact names), never guess one")

// normalizeMyServices — SAF: kırp, boşları at, tekilleştir, sırala, tavanla.
func normalizeMyServices(in []string) (out []string, trimmed int) {
	seen := make(map[string]bool, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	if len(out) > MaxTeamServices {
		trimmed = len(out) - MaxTeamServices
		out = out[:MaxTeamServices]
	}
	return out, trimmed
}

// myServicesCacheKey — SAF: TÜM girdiler (sıralı servis kümesi FNV, tavan dışı sayı, takım (gövdede aynen yazılır),
// pencere sonu dakikaya yuvarlı, pencere, satır) — uzunluk değil (v0.5.187).
func myServicesCacheKey(services []string, trimmed int, team, env string, to time.Time, windowS, limit int) string {
	return fmt.Sprintf("mcp:list_my_services:v2:%s:%d:%s:%s:%d:%d:%d",
		fnvHex(services...), trimmed, fnvHex(team), fnvHex(env), to.Unix()/60, windowS, limit)
}

// MyServicesPageURL — SAF: takımın servisleri sayfası (kök-göreli). Takım
// adı yoksa genel liste. Servisler sayfasının serbest süzgeci katalog
// ownerTeam VE sreTeam'i birlikte eşler (Services.tsx) — OR semantiği aynı.
func MyServicesPageURL(team string) string {
	if t := strings.TrimSpace(team); t != "" {
		return "/services?q=" + url.QueryEscape(t)
	}
	return "/services"
}

// myServicesPayload — list_my_services gövdesi (SAF): ≤ MyServicesMaxBytes;
// satırlar hata oranına göre (ReadTeamServicesRED sırası); sığmayan satır
// sondan düşer, `truncated` + page_url söyler.
// env: istenen ortam; envApplied=false → ortam MV'si pencereyi kapsamıyor,
// sayılar tüm ortamların toplamı (zarf söyler). open: servis → açık Problem
// sayısı (R6; nil = okunamadı, alan yazılmaz).
func myServicesPayload(team string, resolved []string, trimmed int, rows []chstore.ServiceSummary, windowS, limit int, env string, envApplied bool, open map[string]int) map[string]any {
	all := teamServiceRows(rows)
	hasMore := limit > 0 && len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	silentNames, silentTotal := silentTeamServices(resolved, rows, myServicesSilentNamesMax)
	type row struct {
		Service      string  `json:"service"`
		ErrorRatePct float64 `json:"error_rate_pct"`
		ErrorCount   uint64  `json:"error_count"`
		SpanCount    uint64  `json:"span_count"`
		P99Ms        float64 `json:"p99_ms"`
		OpenProblems *int    `json:"open_problems,omitempty"`
	}
	slim := make([]row, 0, len(all))
	for _, r := range all {
		rr := row{Service: r.Service, ErrorRatePct: r.ErrorRatePct, ErrorCount: r.ErrorCount, SpanCount: r.SpanCount, P99Ms: r.P99Ms}
		if open != nil {
			n := open[r.Service]
			rr.OpenProblems = &n
		}
		slim = append(slim, rr)
	}
	out := map[string]any{
		"window_s":         windowS,
		"matched_services": len(resolved) + trimmed,
		"trimmed_services": trimmed,
		"silent_services":  silentTotal,
		"has_more":         hasMore,
		"order":            "error_rate_pct desc",
		"source":           "service_summary_5m (5-minute pre-aggregate); ownership = service catalog ownerTeam OR sreTeam",
		"page_url":         MyServicesPageURL(team),
	}
	if team != "" {
		out["team"] = team
	}
	if env != "" {
		out["env"], out["env_applied"] = env, envApplied
		if !envApplied {
			out["env_note"] = "the per-environment pre-aggregate does not cover this window yet — numbers are summed across ALL environments"
		}
	}
	if open != nil {
		total := 0
		for _, s := range resolved {
			total += open[s]
		}
		out["open_problems_total"] = total
	}
	if len(silentNames) > 0 {
		out["silent_service_names"] = silentNames
	}
	if len(slim) == 0 {
		out["reasons"] = []string{fmt.Sprintf("the %d scoped service(s) produced no span in the last %ds — treat them as silent/idle, not healthy; widen range_s", len(resolved), windowS)}
	}
	truncated := false
	for {
		out["services"], out["count"] = slim, len(slim)
		b, err := json.Marshal(out)
		if err != nil || len(b) <= MyServicesMaxBytes || len(slim) == 0 {
			break
		}
		slim = slim[:len(slim)-1]
		truncated = true
		out["truncated"] = true
	}
	if truncated {
		out["has_more"] = true
	}
	return out
}

func listMyServicesTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: ListMyServicesName,
		ShortDescription: "Kullanıcının KENDİ servisleri (takımın ownerTeam VEYA sreTeam'i) ve RED'i, en çok hata alan önce. " +
			"\"Benim servislerim / bugün ne bozuldu\" sorusunun girişi; kapsamı sunucu koyar (services/team verme). " +
			"Kapsam yoksa hata döner: kullanıcıya hangi takım olduğunu sor. env verilirse o ortamın RED'i; satırda açık Problem sayısı.",
		Description: "Return the CALLER's own services — every service whose catalog ownerTeam OR sreTeam is the caller's team — with RED numbers over the window: error rate, error count, span volume, p99 latency. " +
			"Use it FIRST for 'what is broken in my services', 'is anything failing in my area since 14:00', then drill into the worst row with get_service_health / query_traces / query_logs. " +
			"Scope is injected by the chat loop (the user's team from the directory): you do not need to pass `services` or `team`; pass them only when the user explicitly names other services or a team. " +
			"If there is no scope the call fails with a named error — ask the user which team they mean (list_teams has the exact names); never guess a team. " +
			"Rows are ordered by error rate descending; the body stays under 2 KB (at most 20 rows) and `page_url` opens the full list. Services with no span in the window are counted in `silent_services` (silent is NOT healthy). " +
			"Reads the 5-minute service pre-aggregate in ONE query for the whole set (cached 30 s). `env` narrows to one environment when the per-environment pre-aggregate covers the window (`env_applied`; otherwise numbers are summed across environments and `env_note` says so). Each row carries `open_problems` (not resolved) and the body `open_problems_total` — list them with open_problems. For a named team that is not the user's, get_team_services is the sibling.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"services": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"maxItems":    MaxTeamServices,
					"description": "Service names to read. Normally filled by the server from the user's team — omit it. Pass only when the user names services explicitly.",
				},
				"team": map[string]any{
					"type":        "string",
					"description": "Team label (ownerTeam OR sreTeam). Filled by the server with the user's team; when `services` is empty the team is resolved through the catalog. Omit unless the user names a team.",
				},
				"env": map[string]any{
					"type":        "string",
					"description": "Deployment environment (e.g. prod). Empty = all environments.",
				},
				"range_s": map[string]any{
					"type":        "integer",
					"minimum":     0,
					"maximum":     teamSvcMaxRangeS,
					"description": "Lookback seconds. Default 3600 (1h), max 604800 (7d), floor 300 (5-minute pre-aggregate buckets).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"minimum":     1,
					"maximum":     myServicesMaxRows,
					"description": "Max service rows. Default 10, max 20; rows are error-rate ordered so a small limit keeps the worst ones.",
				},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a listMyServicesArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			team := strings.TrimSpace(a.Team)
			svcs, trimmed := normalizeMyServices(a.Services)
			if len(svcs) == 0 && team != "" {
				var err error
				if svcs, trimmed, err = ReadTeamServiceNames(ctx, d, team); err != nil {
					return nil, err
				}
			}
			if len(svcs) == 0 {
				return nil, errNoMyScope
			}
			windowS := teamSvcWindowS(a.RangeS)
			limit := clampLimit(a.Limit, myServicesDefaultRows, myServicesMaxRows)
			from, to := rangeWindow(ctx, windowS)
			env := strings.TrimSpace(a.Env)
			run := func(c context.Context) (map[string]any, error) {
				rows, envApplied, err := ReadTeamServicesREDEnv(c, d, svcs, env, from, to)
				if err != nil {
					return nil, err
				}
				// R6 — açık Problem sayısı; okuma hatası RED'i düşürmez (alan yazılmaz).
				var open map[string]int
				if probs, perr := ReadOpenProblems(c, d, svcs, env, false); perr == nil {
					open = OpenProblemCounts(probs)
				}
				return myServicesPayload(team, svcs, trimmed, rows, windowS, limit, env, envApplied, open), nil
			}
			if d.Memo == nil {
				return run(ctx)
			}
			body, err := d.Memo(ctx, myServicesCacheKey(svcs, trimmed, team, env, to, windowS, limit), myServicesCacheTTL, func(c context.Context) (any, bool, error) {
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
