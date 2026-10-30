package mcptools

// v2_traces_logs.go — CoSRE v2 araç seti dilim 3 (docs/cosre/tools-audit.md
// §5, §6, §9; operatör onayı 2026-10-30, Q7/Q8): find_traces, search_logs
// (v2 yeniden yazımı, aynı ad), explain_trace. Yalnız v2 kataloğu
// (registerV2Subset); registry, dış MCP ve v1 sohbeti bunları GÖRMEZ.
//
// Ortak sözleşme:
//   - `services[]` (döngü user_scope'u enjekte eder; ≤MaxTeamServices, sıralı).
//     Açık `service` kümeden üstündür; search_logs'ta `trace_id` trace'i kapsam
//     yapar (küme uygulanmaz); explain_trace'te küme yalnız İŞARETLER
//     (scope_services_in_trace), trace'i reddetmez.
//   - Gövde ≤ V2ToolMaxBytes (2 KB); sığmayan satır sondan düşer, `truncated`
//     + `page_url` (kök-göreli) sayfayı açar. `query` okumanın kısa özeti.
//   - Önbellek Deps.Memo; anahtar TÜM girdileri hash'ler (sıralı küme FNV,
//     pencere sonu dakikaya yuvarlı) — uzunluk değil (v0.5.187).
//   - Okumalar mevcut sınırlı okuyucular: GetTraces (trace_summary_5m /
//     trace_service_index_5m, ham yolda LIMIT + max_execution_time),
//     logstore.Store.Search/Histogram, GetTrace (trace_id nokta okuması).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

// v2 araç adları.
const (
	FindTracesName   = "find_traces"
	SearchLogsV2Name = tsSearchLogs // v1 aracının yerini v2 kataloğunda ADIYLA alır
	ExplainTraceName = "explain_trace"
)

const (
	// V2ToolMaxBytes — v2 araç gövdesi tavanı (brief "tool text ≤ 2 KB").
	V2ToolMaxBytes = 2048

	ftDefaultRangeS  = 3600
	ftMaxRangeS      = 6 * 3600 // indeksli anahtar penceresi (§5)
	ftMaxRows        = 10
	ftLinkMaxSvcs    = 10 // page_url'e çip olarak yazılan en çok servis
	ftCacheTTL       = 30 * time.Second
	slDefaultRangeS  = 3600
	slMaxRangeS      = 86400
	slMaxRows        = 10
	slBodyRunes      = 160
	slHistBuckets    = 12
	slHistTimeout    = 3 * time.Second
	slCacheTTL       = 30 * time.Second
	etMaxPathSteps   = 6
	etMaxErrorSpans  = 5
	etMsgRunes       = 160
	etCacheTTL       = 60 * time.Second
	v2MinRangeS      = 300
	v2CodeMaxRunes   = 40
	v2SvcSampleNames = 5
)

var v2CodeRe = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_.-]{1,39}$`)

// v2Window — SAF: range_s → [taban, tavan] kelepçeli saniye.
func v2Window(rangeS, def, max int) int {
	switch {
	case rangeS <= 0:
		return def
	case rangeS < v2MinRangeS:
		return v2MinRangeS
	case rangeS > max:
		return max
	}
	return rangeS
}

// v2Scope — SAF: açık servis > küme. (service, services).
func v2Scope(service string, services []string) (string, []string) {
	if s := strings.TrimSpace(service); s != "" {
		return s, nil
	}
	svcs, _ := normalizeMyServices(services)
	if len(svcs) == 1 {
		return svcs[0], nil
	}
	return "", svcs
}

// v2Code — SAF: iş kodu argümanı (büyük harf, şekil kapısı); geçersiz → hata.
func v2Code(kind, raw string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(raw))
	if c == "" {
		return "", nil
	}
	if !v2CodeRe.MatchString(c) {
		return "", fmt.Errorf("%s %q geçersiz (harf/rakam, 2-%d karakter)", kind, truncateRunes(raw, v2CodeMaxRunes), v2CodeMaxRunes)
	}
	return c, nil
}

// v2Fit — SAF: gövde ≤ V2ToolMaxBytes olana dek `set(n)` ile satır düşürür.
func v2Fit(out map[string]any, n int, set func(int)) map[string]any {
	set(n)
	for n > 0 {
		b, err := json.Marshal(out)
		if err != nil || len(b) <= V2ToolMaxBytes {
			return out
		}
		n--
		set(n)
		out["truncated"] = true
	}
	return out
}

func v2Memo(ctx context.Context, d Deps, key string, ttl time.Duration, run func(context.Context) (map[string]any, error)) (any, error) {
	if d.Memo == nil {
		return run(ctx)
	}
	body, err := d.Memo(ctx, key, ttl, func(c context.Context) (any, bool, error) {
		out, err := run(c)
		return out, err == nil, err
	})
	if err != nil {
		return nil, err
	}
	return json.RawMessage(body), nil
}

func servicesSchema(what string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"maxItems":    MaxTeamServices,
		"description": "Service set to search (any of them). Filled by the server with the user's own services — omit it. Pass only when the user names services explicitly. " + what,
	}
}

func rangeSchema(def, max int) map[string]any {
	return map[string]any{"type": "integer", "minimum": 0, "maximum": max,
		"description": fmt.Sprintf("Lookback seconds ending at the chat anchor. Default %d, floor %d, max %d.", def, v2MinRangeS, max)}
}

// ─── find_traces ───────────────────────────────────────────────

type findTracesArgs struct {
	Services     []string `json:"services,omitempty"`
	Team         string   `json:"team,omitempty"`
	Service      string   `json:"service,omitempty"`
	Operation    string   `json:"operation,omitempty"`
	ChannelCode  string   `json:"channel_code,omitempty"`
	FunctionCode string   `json:"function_code,omitempty"`
	Status       string   `json:"status,omitempty"`
	MinMs        float64  `json:"min_duration_ms,omitempty"`
	SortBy       string   `json:"sort_by,omitempty"`
	Env          string   `json:"env,omitempty"`
	RangeS       int      `json:"range_s,omitempty"`
	Limit        int      `json:"limit,omitempty"`
}

var errFindTracesNoScope = errors.New("no scope: pass service (exact name), a channel_code / function_code, or let the server inject the user's services; if the user has no team ask which service they mean — never guess one")

// findTracesPlan — find_traces'in normalize girdisi (SAF; anahtar + okuma bundan).
type findTracesPlan struct {
	Service  string
	Services []string
	Filters  []chstore.FilterExpr
	Errors   bool
	Slowest  bool
	MinMs    float64
	Env      string
	WindowS  int
	Limit    int
	Narrowed string
}

// planFindTraces — SAF: argüman → plan (pencere kapısı dahil).
func planFindTraces(a findTracesArgs, kvh bool) (findTracesPlan, error) {
	p := findTracesPlan{Env: strings.TrimSpace(a.Env), Limit: clampLimit(a.Limit, ftMaxRows, ftMaxRows)}
	p.Service, p.Services = v2Scope(a.Service, a.Services)
	if op := truncateRunes(strings.TrimSpace(a.Operation), qtMaxNameRunes); op != "" {
		p.Filters = append(p.Filters, chstore.FilterExpr{Key: "name", Op: "=", Values: []string{op}})
	}
	for _, c := range []struct{ key, raw string }{{"channel_code", a.ChannelCode}, {"function_code", a.FunctionCode}} {
		code, err := v2Code(c.key, c.raw)
		if err != nil {
			return p, err
		}
		if code != "" {
			p.Filters = append(p.Filters, OracleCodeFilter(c.key, []string{code}))
		}
	}
	coded := false
	for _, f := range p.Filters {
		if isBusinessCodeScopeFilter(f) {
			coded = true
		}
	}
	if p.Service == "" && len(p.Services) == 0 && !coded {
		return p, errFindTracesNoScope
	}
	st, _ := coerceEnum("status", a.Status, qtStatusEnum, qtStatusSynonyms, qtStatusAny)
	p.Errors = st == qtStatusError
	sb, _ := coerceEnum("sort_by", a.SortBy, qtSortEnum, qtSortSynonyms, qtSortRecent)
	p.Slowest = sb == qtSortSlowest
	if a.MinMs > 0 && a.MinMs <= qtMaxMinDurationMs {
		p.MinMs = a.MinMs
	}
	p.WindowS = v2Window(a.RangeS, ftDefaultRangeS, ftMaxRangeS)
	gate, err := applySearchGate(p.WindowS, p.Limit, p.Filters, true, p.Slowest, kvh)
	if err != nil {
		return p, err
	}
	if gate.Reason != "" {
		p.WindowS, p.Narrowed = gate.RangeS, gate.Reason
	}
	p.Limit = clampLimit(gate.Limit, ftMaxRows, ftMaxRows)
	return p, nil
}

// findTracesCacheKey — SAF: TÜM plan girdileri (sıralı küme FNV, süzgeçler JSON FNV, pencere sonu dakika).
func findTracesCacheKey(p findTracesPlan, to time.Time) string {
	fj, _ := json.Marshal(p.Filters)
	return fmt.Sprintf("mcp:find_traces:v1:%s:%s:%s:%t:%t:%g:%s:%d:%d:%d",
		fnvHex(p.Service), fnvHex(p.Services...), fnvHex(string(fj)), p.Errors, p.Slowest, p.MinMs,
		fnvHex(p.Env), p.WindowS, p.Limit, to.Unix()/60)
}

// traceFilter — plan → okuyucu süzgeci (SAF).
func (p findTracesPlan) traceFilter(from, to time.Time) chstore.TraceFilter {
	sort := "time"
	if p.Slowest {
		sort = "duration"
	}
	return chstore.TraceFilter{
		Service: p.Service, Services: p.Services, From: from, To: to, HasError: p.Errors,
		Sort: sort, Order: "desc", Limit: p.Limit, CountMode: "skip",
		Filters: p.Filters, Env: p.Env, MinMs: p.MinMs,
	}
}

// FindTracesPageURL — SAF: /traces derin bağlantısı. Küme ≤ ftLinkMaxSvcs ise
// `service.name IN` çipi; daha büyük küme sayfada süzülmez (scoped=false).
func FindTracesPageURL(f chstore.TraceFilter, from, to time.Time) (string, bool) {
	scoped := true
	if len(f.Services) > 0 {
		lf := f
		lf.Services = nil
		if len(f.Services) <= ftLinkMaxSvcs {
			lf.Filters = append(append([]chstore.FilterExpr(nil), f.Filters...),
				chstore.FilterExpr{Key: "service.name", Op: "IN", Values: append([]string(nil), f.Services...)})
		} else {
			scoped = false
		}
		f = lf
	}
	return TracesDeepLink(f, from, to), scoped
}

// findTracesQuery — SAF: okumanın kısa özeti (kartta görünür).
func findTracesQuery(p findTracesPlan, f chstore.TraceFilter) string {
	var w []string
	switch {
	case p.Service != "":
		w = append(w, "service_name = "+sqlQuote(p.Service))
	case len(p.Services) > 0:
		w = append(w, fmt.Sprintf("service_name IN (%d services)", len(p.Services)))
	}
	if fs := filtersSummary(f.Filters); fs != "" {
		w = append(w, fs)
	}
	if p.Env != "" {
		w = append(w, "deploy_env = "+sqlQuote(p.Env))
	}
	if p.Errors {
		w = append(w, "has_error")
	}
	if p.MinMs > 0 {
		w = append(w, fmt.Sprintf("duration_ms >= %g", p.MinMs))
	}
	w = append(w, fmt.Sprintf("last %ds", p.WindowS))
	return fmt.Sprintf("GetTraces[trace_summary_5m/trace_service_index_5m → spans] WHERE %s ORDER BY %s DESC LIMIT %d",
		strings.Join(w, " AND "), f.Sort, f.Limit)
}

type ftRow struct {
	ID       string  `json:"id"`
	RootOp   string  `json:"root_op"`
	EntrySvc string  `json:"entry_svc"`
	Ms       float64 `json:"ms"`
	Spans    uint64  `json:"spans"`
	Error    bool    `json:"error"`
}

// findTracesPayload — SAF: ≤ 2 KB gövde.
func findTracesPayload(p findTracesPlan, f chstore.TraceFilter, rows []chstore.TraceRow, hasMore bool, from, to time.Time) map[string]any {
	all := make([]ftRow, 0, len(rows))
	for _, t := range rows {
		all = append(all, ftRow{t.TraceID, truncateRunes(t.RootName, 80), t.ServiceName, round4(t.DurationMs), t.SpanCount, t.HasError})
	}
	if len(all) > p.Limit {
		all, hasMore = all[:p.Limit], true
	}
	href, scoped := FindTracesPageURL(f, from, to)
	out := map[string]any{
		"window_s": p.WindowS, "has_more": hasMore, "query": findTracesQuery(p, f),
		"source":   "ClickHouse traces (trace_summary_5m pre-aggregate when eligible, else bounded raw spans)",
		"page_url": href,
	}
	if len(p.Services) > 0 {
		out["scope_services"] = len(p.Services)
		if !scoped {
			out["page_url_scoped"] = false
		}
	}
	if p.Narrowed != "" {
		out["narrowed"] = p.Narrowed
	}
	if len(all) == 0 {
		out["reasons"] = []string{"no trace matched in the window — empty is not 'no errors anywhere'; widen range_s or drop a filter"}
	}
	return v2Fit(out, len(all), func(n int) { out["traces"], out["count"] = all[:n], n })
}

func findTracesTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             FindTracesName,
		ShortDescription: "Trace ara: kullanıcının servisleri (sunucu koyar) ya da servis / channel_code / function_code; status error, sort_by slowest, ≤10 satır + page_url.",
		Description: "Find traces with CLOSED filters only — no query syntax. Scope: `services` is injected by the server with the user's own services (any of them); an explicit `service` overrides it; a channel_code / function_code equality works fleet-wide. " +
			"Filters: operation (exact span name), channel_code, function_code (business codes, exact), status any|error, min_duration_ms, sort_by recent|slowest, env, range_s (default 1h, max 6h; filtered or slowest searches narrow automatically and say so in `narrowed`). " +
			"Returns ≤10 traces {id, root_op, entry_svc, ms, spans, error}, has_more, the reader summary in `query` and `page_url` (the same search on the Traces page). " +
			"Next step: explain_trace with a returned id. Empty traces = nothing matched in that window, not proof of health.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"services":        servicesSchema(""),
				"team":            map[string]any{"type": "string", "description": "Team label, filled by the server; used only for the page link. Omit."},
				"service":         map[string]any{"type": "string", "description": "Exact service name; overrides `services`."},
				"operation":       map[string]any{"type": "string", "description": "Exact operation/span name (optional)."},
				"channel_code":    map[string]any{"type": "string", "description": "CHANNEL_CODE equality (e.g. KNL123)."},
				"function_code":   map[string]any{"type": "string", "description": "FUNCTION_CODE equality (e.g. BKRB001)."},
				"status":          map[string]any{"type": "string", "enum": qtStatusEnum, "description": "error = only traces with an error span. Default any."},
				"min_duration_ms": map[string]any{"type": "number", "minimum": 0, "description": "Only traces at least this slow (ms)."},
				"sort_by":         map[string]any{"type": "string", "enum": qtSortEnum, "description": "recent (default) or slowest."},
				"env":             map[string]any{"type": "string", "description": "Deployment environment (e.g. prod)."},
				"range_s":         rangeSchema(ftDefaultRangeS, ftMaxRangeS),
				"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": ftMaxRows, "description": "Max rows, default and max 10."},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a findTracesArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			p, err := planFindTraces(a, chstore.AttrIndexAvailable())
			if err != nil {
				return nil, err
			}
			if d.Store == nil {
				return nil, errors.New("ClickHouse trace reader not configured")
			}
			from, to := rangeWindow(ctx, p.WindowS)
			return v2Memo(ctx, d, findTracesCacheKey(p, to), ftCacheTTL, func(c context.Context) (map[string]any, error) {
				rctx, cancel := context.WithTimeout(c, cosreBudget)
				defer cancel()
				f := p.traceFilter(from, to)
				rows, _, hasMore, err := d.Store.GetTraces(rctx, f)
				if err != nil {
					return nil, err
				}
				return findTracesPayload(p, f, rows, hasMore, from, to), nil
			})
		},
	}
}

// ─── search_logs (v2) ──────────────────────────────────────────

type cosreSearchLogsArgs struct {
	Words    string   `json:"words,omitempty"`
	Services []string `json:"services,omitempty"`
	Team     string   `json:"team,omitempty"`
	Service  string   `json:"service,omitempty"`
	Severity string   `json:"severity,omitempty"`
	TraceID  string   `json:"trace_id,omitempty"`
	Env      string   `json:"env,omitempty"`
	RangeS   int      `json:"range_s,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

var errSearchLogsNoScope = errors.New("no scope: pass service, trace_id, or let the server inject the user's services; if the user has no team ask which service they mean — never guess one")

type searchLogsPlan struct {
	Service  string
	Services []string
	Team     string
	TraceID  string
	Search   string // sanitizeLogText tırnaklı ifadesi
	Words    string // düz metin (gösterim + link)
	Severity string
	SevMin   uint8
	Env      string
	WindowS  int
	Limit    int
}

// planSearchLogs — SAF.
func planSearchLogs(a cosreSearchLogsArgs) (searchLogsPlan, error) {
	p := searchLogsPlan{Env: strings.TrimSpace(a.Env), Limit: clampLimit(a.Limit, slMaxRows, slMaxRows), Team: strings.TrimSpace(a.Team)}
	if raw := strings.TrimSpace(a.TraceID); raw != "" {
		id, err := logsTraceIDArg(raw, false)
		if err != nil {
			return p, fmt.Errorf("trace_id must be 32 hex: %w", err)
		}
		p.TraceID = id
	}
	if p.TraceID == "" { // trace kapsam yapar: küme/servis uygulanmaz
		p.Service, p.Services = v2Scope(a.Service, a.Services)
	}
	if p.TraceID == "" && p.Service == "" && len(p.Services) == 0 {
		return p, errSearchLogsNoScope
	}
	p.Search, p.Words = sanitizeLogText(a.Words)
	p.Severity, _ = coerceEnum("severity", a.Severity, qlSeverityEnum, qlSeveritySynonyms, qlSeverityAny)
	switch p.Severity {
	case qlSeverityWarn:
		p.SevMin = qlOTelWarnFloor
	case qlSeverityError:
		p.SevMin = qlOTelErrorFloor
	}
	p.WindowS = v2Window(a.RangeS, slDefaultRangeS, slMaxRangeS)
	return p, nil
}

// searchLogsCacheKey — SAF: TÜM girdiler.
func searchLogsCacheKey(backend string, p searchLogsPlan, to time.Time) string {
	return fmt.Sprintf("mcp:search_logs_v2:v1:%s:%s:%s:%s:%s:%s:%s:%d:%s:%d:%d:%d",
		backend, fnvHex(p.Service), fnvHex(p.Services...), fnvHex(p.Team), p.TraceID, fnvHex(p.Search), p.Severity, p.SevMin,
		fnvHex(p.Env), p.WindowS, p.Limit, to.Unix()/60)
}

func (p searchLogsPlan) filter(from, to time.Time) logstore.Filter {
	return logstore.Filter{
		Service: p.Service, Services: p.Services, Env: p.Env, TraceID: p.TraceID, Search: p.Search,
		From: from, To: to, SeverityMin: p.SevMin, Limit: p.Limit,
		SoftTimeout: searchLogsTimeout - searchLogsSoftMargin,
	}
}

// SearchLogsPageURL — SAF: /logs bağlantısı (kök-göreli). Küme: takım varsa `team=`.
func SearchLogsPageURL(p searchLogsPlan, from, to time.Time) string {
	href := evidenceBuildLink(buildLinkArgs{
		Page: "logs", Query: p.Words, Service: p.Service, Severity: int(p.SevMin), TraceID: p.TraceID, Env: p.Env,
	}, "", fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	if href == "" {
		href = "/logs"
	}
	if p.Service == "" && p.TraceID == "" && len(p.Services) > 0 && p.Team != "" {
		sep := "?"
		if strings.Contains(href, "?") {
			sep = "&"
		}
		href += sep + "team=" + url.QueryEscape(p.Team)
	}
	return href
}

// searchLogsQuery — SAF: okumanın kısa özeti.
func searchLogsQuery(backend string, p searchLogsPlan) string {
	var w []string
	switch {
	case p.TraceID != "":
		w = append(w, "trace_id="+sqlQuote(p.TraceID))
	case p.Service != "":
		w = append(w, "service="+sqlQuote(p.Service))
	default:
		w = append(w, fmt.Sprintf("service IN (%d services)", len(p.Services)))
	}
	if p.Env != "" {
		w = append(w, "env="+sqlQuote(p.Env))
	}
	if p.SevMin > 0 {
		w = append(w, fmt.Sprintf("severity_number>=%d", p.SevMin))
	}
	if p.Search != "" {
		w = append(w, "body:"+p.Search)
	}
	w = append(w, fmt.Sprintf("last %ds", p.WindowS))
	return fmt.Sprintf("logs[%s] WHERE %s ORDER BY time DESC LIMIT %d", backend, strings.Join(w, " AND "), p.Limit)
}

type slRow struct {
	Ts      int64  `json:"ts"`
	Sev     string `json:"sev"`
	Svc     string `json:"svc,omitempty"`
	Body    string `json:"body"`
	TraceID string `json:"trace_id,omitempty"`
}

// slHistogram — SAF: seriler → ≤ slHistBuckets toplam sayı (zaman sırası).
func slHistogram(series []logstore.LogSeries) []int64 {
	byT := map[int64]int64{}
	var ts []int64
	for _, s := range series {
		for _, pt := range s.Points {
			if _, ok := byT[pt.T]; !ok {
				ts = append(ts, pt.T)
			}
			byT[pt.T] += pt.V
		}
	}
	sortInt64s(ts)
	out := make([]int64, 0, len(ts))
	for _, t := range ts {
		out = append(out, byT[t])
	}
	if len(out) > slHistBuckets {
		out = out[len(out)-slHistBuckets:]
	}
	return out
}

func sortInt64s(xs []int64) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}

// searchLogsPayload — SAF: ≤ 2 KB gövde.
func searchLogsPayload(backend string, p searchLogsPlan, page *logstore.Page, hist []int64, unapplied []string, from, to time.Time) map[string]any {
	rows := make([]slRow, 0, len(page.Logs))
	for _, rec := range page.Logs {
		if rec == nil {
			continue
		}
		rows = append(rows, slRow{rec.Timestamp / 1e6, logSeverityLabel(rec), rec.ServiceName,
			promptfmt.FenceSafe(truncateRunes(rec.Body, slBodyRunes)), rec.TraceID})
	}
	if len(rows) > p.Limit {
		rows = rows[:p.Limit]
	}
	out := map[string]any{
		"window_s": p.WindowS, "total": page.Total, "has_more": page.NextCursor != "" || page.Total > len(rows),
		"query": searchLogsQuery(backend, p), "source": backend + " logs (logstore)",
		"page_url": SearchLogsPageURL(p, from, to),
	}
	if len(p.Services) > 0 {
		out["scope_services"] = len(p.Services)
	}
	if len(hist) > 0 {
		out["histogram"] = hist
	}
	if len(unapplied) > 0 {
		out["unapplied"] = unapplied
	}
	if page.Partial || page.ShardsFailed > 0 {
		out["partial"] = true
	}
	if len(rows) == 0 {
		out["reasons"] = []string{"no log line matched in the window — empty is not 'no errors'; widen range_s, drop words or lower severity"}
	}
	return v2Fit(out, len(rows), func(n int) { out["rows"], out["count"] = rows[:n], n })
}

func cosreSearchLogsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             SearchLogsV2Name,
		ShortDescription: "Log ara: kullanıcının servisleri (sunucu koyar) ya da servis / trace_id; words düz kelimeler (sorgu dili YOK), severity, ≤10 satır + histogram + page_url.",
		Description: "Search log lines with plain words — NOT a query language (quotes, operators and field syntax are stripped). Scope: `services` is injected by the server with the user's own services (any of them); an explicit `service` overrides it; `trace_id` makes the trace the scope (services are not applied). " +
			"Args: words, severity any|warn|error, env, range_s (default 1h, max 24h), limit (≤10). " +
			"Returns ≤10 rows {ts (unix ms), sev, svc, body ≤160 chars, trace_id}, total, a ≤12-bucket count histogram, the applied reader summary in `query` and `page_url` (the same search on the Logs page). " +
			"`unapplied` names filters the backend could not apply (rows were NOT narrowed by them). Empty rows = nothing matched, not proof of health.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"words":    map[string]any{"type": "string", "description": "Plain words that must appear in the log body (no query syntax)."},
				"services": servicesSchema(""),
				"team":     map[string]any{"type": "string", "description": "Team label, filled by the server; used only for the page link. Omit."},
				"service":  map[string]any{"type": "string", "description": "Exact service name; overrides `services`."},
				"severity": map[string]any{"type": "string", "enum": qlSeverityEnum, "description": "Minimum severity. Default any."},
				"trace_id": map[string]any{"type": "string", "description": "32-hex trace id: the logs of that trace (services ignored)."},
				"env":      map[string]any{"type": "string", "description": "Deployment environment (e.g. prod)."},
				"range_s":  rangeSchema(slDefaultRangeS, slMaxRangeS),
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": slMaxRows, "description": "Max rows, default and max 10."},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a cosreSearchLogsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			p, err := planSearchLogs(a)
			if err != nil {
				return nil, err
			}
			st := d.LogStore
			if st == nil {
				return nil, errors.New("log backend not configured")
			}
			backend := logsBackendSource(st)
			from, to := rangeWindow(ctx, p.WindowS)
			return v2Memo(ctx, d, searchLogsCacheKey(backend, p, to), slCacheTTL, func(c context.Context) (map[string]any, error) {
				f := p.filter(from, to)
				page, err := logstore.SearchWithTimeout(c, st, f, searchLogsTimeout)
				if err != nil {
					return nil, err
				}
				if page == nil {
					page = &logstore.Page{}
				}
				var hist []int64
				hctx, cancel := context.WithTimeout(c, slHistTimeout)
				hf := f
				hf.Limit = 0
				if series, herr := st.Histogram(hctx, hf, max(60, p.WindowS/slHistBuckets), ""); herr == nil {
					hist = slHistogram(series)
				}
				cancel()
				return searchLogsPayload(backend, p, page, hist, qlUnapplied(page, f, logFieldMapping(c, st, true)), from, to), nil
			})
		},
	}
}

// ─── explain_trace ─────────────────────────────────────────────

type explainTraceArgs struct {
	TraceID  string   `json:"trace_id"`
	Services []string `json:"services,omitempty"`
}

// explainTraceCacheKey — SAF: id + sıralı kapsam FNV.
func explainTraceCacheKey(id string, scope []string) string {
	return fmt.Sprintf("mcp:explain_trace:v1:%s:%s", id, fnvHex(scope...))
}

// ExplainTracePageURL — SAF.
func ExplainTracePageURL(id string) string { return "/trace/" + url.PathEscape(id) }

type etStep struct {
	Service  string  `json:"service"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind,omitempty"`
	SelfMs   float64 `json:"self_on_path_ms"`
	SharePct float64 `json:"share_pct"`
	Target   string  `json:"target,omitempty"`
	Top      bool    `json:"top,omitempty"`
}

type etErr struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Status  string `json:"status_message,omitempty"`
	ExType  string `json:"exception_type,omitempty"`
	ExMsg   string `json:"exception_message,omitempty"`
}

// explainTracePayload — SAF: analiz → ≤ 2 KB özet (span listesi YOK).
func explainTracePayload(id string, an traceToolAnalysis, scope []string) map[string]any {
	steps := make([]etStep, 0, etMaxPathSteps)
	for _, s := range ttTrimPathSteps(an.CriticalPath, etMaxPathSteps) {
		steps = append(steps, etStep{s.Service, truncateRunes(s.Name, 80), s.Kind, s.SelfOnPathMs, s.SharePct, truncateRunes(s.Target, 80), s.Top})
	}
	errs := make([]etErr, 0, etMaxErrorSpans)
	for i, e := range an.ErrorSpans {
		if i >= etMaxErrorSpans {
			break
		}
		errs = append(errs, etErr{e.Service, truncateRunes(e.Name, 80), truncateRunes(e.StatusMessage, etMsgRunes), e.ExceptionType, truncateRunes(e.ExceptionMessage, etMsgRunes)})
	}
	out := map[string]any{
		"trace_id": id, "found": true,
		"wall_ms": an.WallMs, "extent_ms": an.ExtentMs,
		"root":       map[string]any{"service": an.Root.Service, "name": truncateRunes(an.Root.Name, 80), "duration_ms": an.Root.DurationMs},
		"span_count": an.SpanCount, "error_span_count": an.ErrorSpanCount,
		"top_external_wait": an.CriticalPathTopExternalWait,
		"source":            "ClickHouse spans (trace_id point read); analysis over all spans read",
		"page_url":          ExplainTracePageURL(id),
	}
	if len(scope) > 0 {
		in := map[string]bool{}
		for _, s := range scope {
			in[s] = true
		}
		var hit []string
		for _, s := range an.Services {
			if in[s.Service] {
				hit = append(hit, s.Service)
			}
		}
		out["scope_services_in_trace"] = len(hit)
		if len(hit) > v2SvcSampleNames {
			hit = hit[:v2SvcSampleNames]
		}
		if len(hit) > 0 {
			out["scope_service_names"] = hit
		}
	}
	out["critical_path"] = steps
	return v2Fit(out, len(errs), func(n int) {
		out["error_spans"] = errs[:n]
		if n == 0 && len(errs) > 0 {
			// hata satırı sığmadıysa yolu da kısalt
			for len(steps) > 1 {
				if b, _ := json.Marshal(out); len(b) <= V2ToolMaxBytes {
					break
				}
				steps = steps[:len(steps)-1]
				out["critical_path"] = steps
			}
		}
	})
}

func explainTraceTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             ExplainTraceName,
		ShortDescription: "Tek trace'in özeti: wall_ms, kritik yol (≤6 adım, top), dış bekleme, hata span'leri (≤5) + page_url. Span listesi yok; süreler toplanmaz.",
		Description: "Summarise ONE trace by its 32-hex id for 'why is it slow / where did it fail'. Returns wall_ms (root duration), extent_ms, root, span/error counts, " +
			"critical_path ≤6 steps {service, name, kind, self_on_path_ms, share_pct, target, top} — the latency owner chain (name the top=true step instead of the longest span), " +
			"top_external_wait (true = the top step waits on an untraced downstream; the fix lives in that dependency), error_spans ≤5 {service, name, status_message, exception_type, exception_message}, " +
			"and page_url (/trace/<id>, where the full one-click Explain lives). NEVER add span durations together; quote wall_ms. " +
			"`services` (server-injected user scope) only marks how many of the user's services the trace touches; it never hides the trace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trace_id": map[string]any{"type": "string", "description": "32-char hex trace ID."},
				"services": servicesSchema("Used only to mark which of them the trace touches."),
			},
			"required": []string{"trace_id"},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a explainTraceArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("decode args: %w", err)
			}
			id, err := traceToolTraceID(a.TraceID)
			if err != nil {
				return nil, err
			}
			if d.Store == nil {
				return nil, errors.New("ClickHouse trace reader not configured")
			}
			scope, _ := normalizeMyServices(a.Services)
			return v2Memo(ctx, d, explainTraceCacheKey(id, scope), etCacheTTL, func(c context.Context) (map[string]any, error) {
				spans, err := d.Store.GetTrace(c, id)
				if err != nil {
					return nil, err
				}
				if len(spans) == 0 {
					return map[string]any{"trace_id": id, "found": false, "page_url": ExplainTracePageURL(id),
						"reasons": []string{"no span in ClickHouse for this id (retention, sampling or not yet written) — get_trace diagnoses the miss"}}, nil
				}
				return explainTracePayload(id, buildTraceToolAnalysis(spans, len(spans) >= traceStoreSpanCap), scope), nil
			})
		},
	}
}
