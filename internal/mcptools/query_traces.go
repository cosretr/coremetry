package mcptools

// query_traces.go — v0.10.1271 (CoSRE adım 2): yapısal trace örneklemesi.
// search_traces'ten (serbest metin + attribute süzgeçleri + çok seçenek)
// farkı: dar, enum'lu şema (status any|error, sort_by slowest|recent) ve
// kanıt zarfı. Okuma iki parça, ikisi de mevcut sınırlı okuyucular:
//
//   - servis toplamı: ServiceWindowRED / ServiceEnvWindowRED — service_summary_5m
//     / service_env_summary_5m (MV; çalışan SQL kanıtta birebir).
//   - örnek liste: GetTraces (trace_summary_5m uygunsa MV, değilse LIMIT +
//     max_execution_time + zaman sınırlı ham yol); operasyon süzgeci
//     search_traces'in pencere kapısından (applySearchGate) geçer.
//
// Tavanlar: limit ≤20, pencere ≤24 saat (spanScanMaxRangeS), bütçe 10 s.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

const (
	queryTracesName     = "query_traces"
	qtDefaultLimit      = 10
	qtMaxLimit          = 20
	qtMaxNameRunes      = 200
	qtMaxMinDurationMs  = 3_600_000
	qtSortSlowest       = "slowest"
	qtSortRecent        = "recent"
	qtStatusAny         = "any"
	qtStatusError       = "error"
	qtEvidenceQueryJoin = "\n"
)

var qtStatusEnum = []string{qtStatusAny, qtStatusError}

var qtStatusSynonyms = map[string]string{
	"all": "any", "ok": "any", "hepsi": "any", "tümü": "any",
	"errors": "error", "failed": "error", "fail": "error", "hata": "error", "hatalı": "error", "err": "error",
}

var qtSortEnum = []string{qtSortSlowest, qtSortRecent}

var qtSortSynonyms = map[string]string{
	"duration": "slowest", "slow": "slowest", "yavaş": "slowest", "en_yavaş": "slowest", "longest": "slowest",
	"time": "recent", "newest": "recent", "latest": "recent", "son": "recent", "yeni": "recent",
}

// qtBackends — okuma dikişleri (testte sahte).
type qtBackends struct {
	traces    func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error)
	windowRED func(ctx context.Context, service, env string, from, to time.Time) (chstore.ServiceWindowRED, error)
	envCovers func(ctx context.Context, from time.Time) bool
	// oracleOps — v0.10.1297: operation bir Oracle operasyon adıysa (span'lerde
	// yok) fonksiyon kodu süzgecine çevirir; nil = çeviri yok.
	oracleOps OracleOpSource
}

func qtBackendsFrom(d Deps) qtBackends {
	var b qtBackends
	if d.Store != nil {
		st := d.Store
		b.traces = st.GetTraces
		b.windowRED = func(ctx context.Context, service, env string, from, to time.Time) (chstore.ServiceWindowRED, error) {
			if env != "" {
				return st.ServiceEnvWindowRED(ctx, service, env, from, to)
			}
			return st.ServiceWindowRED(ctx, service, from, to)
		}
		b.envCovers = st.EnvSummaryCovers
	}
	b.oracleOps = d.OracleOps
	return b
}

type qtTrace struct {
	TraceID    string  `json:"traceId"`
	Root       string  `json:"root"`
	Service    string  `json:"service"`
	StartMs    int64   `json:"startMs"`
	DurationMs float64 `json:"durationMs"`
	Spans      uint64  `json:"spans"`
	Error      bool    `json:"error"`
	ErrorSpans uint64  `json:"errorSpans,omitempty"`
}

type qtSummary struct {
	Spans        uint64  `json:"spans"`
	Errors       uint64  `json:"errors"`
	ErrorRatePct float64 `json:"errorRatePct"`
	AvgMs        float64 `json:"avgMs"`
	P50Ms        float64 `json:"p50Ms"`
	P95Ms        float64 `json:"p95Ms"`
	P99Ms        float64 `json:"p99Ms"`
}

func queryTracesTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "query_traces",
		ShortDescription: "Servisin trace örnekleri + toplamı: status any|error, sort_by slowest|recent, ≤20.⟦ Metin/attribute araması: search_traces.| Attribute süzgeçli sayım: trace_stats.⟧", // v0.10.1302 — ⟦full|simple⟧
		Description: "Structured trace sampling for ONE service — no query syntax. Returns trace ids with a summary " +
			"(root operation, start, duration ms, span count, error flag) plus the service's window totals " +
			"(spans, errors, error %, avg/p50/p95/p99 ms) read from the 5-minute service summary pre-aggregate " +
			"(env-scoped variant when env is given). The sample list uses the bounded trace reader " +
			"(trace-summary pre-aggregate when eligible, else LIMIT + time-bounded raw scan). " +
			"Args: service (required unless operation is an Oracle/BSA operation name that resolves to function codes — then the search runs fleet-wide on the code filter), " +
			"operation (exact span/operation name, optional), status any|error, " +
			"min_duration_ms, sort_by slowest|recent, limit (default 10, max 20), window 15m|1h|6h|24h (default 1h, max 24h) " +
			"or from_ms+to_ms, env. An operation filter narrows the window (≤6h, ≤1h with slowest) and drops the totals. " +
			"Unknown enum values fall back to defaults with a `warnings` note. `evidence` carries the executed SQL / reader summary; " +
			"empty=true + 'veri yok: …' means no trace matched. Next step: get_trace with a returned traceId. " +
			"For free-text / attribute filters use search_traces; for grouped stats trace_stats.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":         map[string]any{"type": "string", "description": "Exact service name (list_services). Required unless operation is an Oracle/BSA operation name."}, // v0.10.1302
				"operation":       map[string]any{"type": "string", "description": "Exact operation/span name (list_operations), or an Oracle/BSA operation name (UPPER_CASE_WITH_UNDERSCORES) — translated to its FUNCTION_CODE filter. Optional."},
				"status":          map[string]any{"type": "string", "enum": qtStatusEnum, "description": "error = only traces with an error span. Default any."},
				"min_duration_ms": map[string]any{"type": "number", "minimum": 0, "description": "Only traces at least this slow (ms)."},
				"sort_by":         map[string]any{"type": "string", "enum": qtSortEnum, "description": "slowest first or most recent first. Default recent."},
				"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": qtMaxLimit, "description": "Default 10, max 20."},
				"window":          map[string]any{"type": "string", "enum": []string{"15m", "1h", "6h", "24h"}, "description": "Lookback ending at the chat anchor. Default 1h."},
				"from_ms":         map[string]any{"type": "integer", "description": "Absolute start, unix ms (with to_ms)."},
				"to_ms":           map[string]any{"type": "integer", "description": "Absolute end, unix ms (with from_ms)."},
				"env":             map[string]any{"type": "string", "description": "Deployment environment (e.g. prod, uat, int, prep)."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			return runQueryTraces(ctx, qtBackendsFrom(d), raw)
		},
	}
}

func runQueryTraces(ctx context.Context, b qtBackends, raw json.RawMessage) (cosreResult, error) {
	a, warnings := parseCosreArgs(raw)
	service := a.str("service", "service_name", "servis")
	operation := truncateRunes(a.str("operation", "op", "name", "endpoint"), qtMaxNameRunes)
	env := a.str("env", "environment", "ortam")
	status, w := coerceEnum("status", a.str("status", "errors"), qtStatusEnum, qtStatusSynonyms, qtStatusAny)
	if w != "" {
		warnings = append(warnings, w)
	}
	// errors_only:true / status:true gibi mantıksal biçimler.
	if v := a.str("errors_only", "error_only", "only_errors"); v == "true" {
		status = qtStatusError
	}
	sortBy, w := coerceEnum("sort_by", a.str("sort_by", "sort", "order_by"), qtSortEnum, qtSortSynonyms, qtSortRecent)
	if w != "" {
		warnings = append(warnings, w)
	}
	limF, okLim, wl := a.num("limit")
	if wl != "" {
		warnings = append(warnings, wl)
	}
	limit := qtDefaultLimit
	if okLim {
		limit = clampLimit(int(limF), qtDefaultLimit, qtMaxLimit)
		if int(limF) > qtMaxLimit {
			warnings = append(warnings, fmt.Sprintf("limit %d → %d (tavan)", int(limF), qtMaxLimit))
		}
	}
	minMs, okMin, wm := a.num("min_duration_ms", "min_ms", "min_duration")
	if wm != "" {
		warnings = append(warnings, wm)
	}
	if okMin && (minMs < 0 || minMs > qtMaxMinDurationMs) {
		warnings = append(warnings, fmt.Sprintf("min_duration_ms %.0f geçersiz (0..%d) — yok sayıldı", minMs, qtMaxMinDurationMs))
		minMs = 0
	}
	from, to, label, ww := cosreWindow(ctx, a, spanScanMaxRangeS, "1h")
	warnings = append(warnings, ww...)

	req := map[string]any{"service": service, "status": status, "sort_by": sortBy, "limit": limit, "window": label,
		"from_ms": from.UnixMilli(), "to_ms": to.UnixMilli()}
	if operation != "" {
		req["operation"] = operation
	}
	if minMs > 0 {
		req["min_duration_ms"] = minMs
	}
	if env != "" {
		req["env"] = env
	}
	// v0.10.1302 — Oracle/ACM operasyon adı fonksiyon koduna çevrilirse servis
	// gerekmez: kod süzgeci terfi etmiş tek kolona iner (search_traces kapısının
	// "kapsam çipi" sayımı, isBusinessCodeScopeFilter) — simple araç setinde
	// resolve_oracle_operation → query_traces zinciri böyle tamamlanır.
	tr, translated := translateOperationSearch(ctx, b.oracleOps, operation)
	if service == "" && !translated {
		r := cosreFail(queryTracesName, req, warnings, cosreErrBadArgs, mcp.ToolsetText(ctx,
			"service gerekli (list_services ile tam adı bul; Oracle/BSA operasyon adı operation ile servissiz çalışır)⟦; filo geneli arama için search_traces|⟧"))
		r.Error.Need = []string{"service"}
		return r, nil
	}
	if b.traces == nil {
		return cosreFail(queryTracesName, req, warnings, cosreErrNotConfigured, "ClickHouse trace okuyucusu bağlı değil"), nil
	}

	var filters []chstore.FilterExpr
	if translated {
		// v0.10.1297 — operasyon adı span'lerde yok: name süzgeci sıfır trace
		// bulurdu; fonksiyon kodu süzgeci (kanıt href'i de aynısını taşır).
		filters = append(filters, tr.Filter())
		req[qtCodeFilterKey] = tr.Filter()
		req["operation_translated"] = tr
	} else if operation != "" {
		filters = append(filters, chstore.FilterExpr{Key: "name", Op: "=", Values: []string{operation}})
	}
	gate, err := applySearchGate(int(to.Sub(from)/time.Second), limit, filters, true, sortBy == qtSortSlowest, chstore.AttrIndexAvailable())
	if err != nil {
		return cosreFail(queryTracesName, req, warnings, cosreErrBadArgs, err.Error()), nil
	}
	if gate.Reason != "" {
		from = to.Add(-time.Duration(gate.RangeS) * time.Second)
		warnings = append(warnings, "pencere daraltıldı: "+gate.Reason)
		req["from_ms"] = from.UnixMilli()
	}
	limit = gate.Limit

	elapsed := stopwatch()
	rctx, cancel := context.WithTimeout(ctx, cosreBudget)
	defer cancel()

	// 1) Servis toplamı (MV) — operasyon süzgeci yokken.
	var summary *qtSummary
	var queries []string
	kind := evKindSummary
	if operation == "" && b.windowRED != nil {
		useEnv := env
		if env != "" && b.envCovers != nil && !b.envCovers(rctx, from) {
			useEnv = ""
			warnings = append(warnings, "ortam ön-toplamı bu pencerenin başını kapsamıyor — servis toplamı atlandı (örnek liste env ile süzüldü)")
		}
		if env == "" || useEnv != "" {
			red, err := b.windowRED(rctx, service, useEnv, from, to)
			if err != nil {
				if errors.Is(ctx.Err(), context.Canceled) {
					return cosreResult{}, ctx.Err()
				}
				warnings = append(warnings, cosreErrMsg("servis toplamı okunamadı", err))
			} else {
				summary = qtSummaryOf(red)
			}
			queries = append(queries, qtSummarySQL(service, useEnv, from, to))
			kind = evKindSQL
		}
	} else if operation != "" {
		warnings = append(warnings, "operation verildiği için servis toplamı döndürülmedi (toplam servis düzeyindedir)")
	}

	// 2) Örnek liste (sınırlı okuyucu).
	sortCol := "time"
	if sortBy == qtSortSlowest {
		sortCol = "duration"
	}
	f := chstore.TraceFilter{
		Service: service, From: from, To: to, HasError: status == qtStatusError,
		Sort: sortCol, Order: "desc", Limit: limit, CountMode: "skip",
		Filters: filters, Env: env, MinMs: minMs,
	}
	rows, _, hasMore, err := b.traces(rctx, f)
	queries = append(queries, qtListQueryText(f))
	ev := &cosreEvidence{Source: evSourceCH, QueryKind: kind, Query: strings.Join(queries, qtEvidenceQueryJoin), Window: evWin(from, to), Env: env}
	ev.DurationMs = elapsed()
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return cosreResult{}, ctx.Err()
		}
		r := cosreFail(queryTracesName, req, warnings, cosreClassify(err), cosreErrMsg("ClickHouse trace okuması başarısız", err))
		r.Evidence = ev
		return r, nil
	}
	traces := make([]qtTrace, 0, len(rows))
	for _, t := range rows {
		traces = append(traces, qtTrace{TraceID: t.TraceID, Root: t.RootName, Service: t.ServiceName, StartMs: t.StartTime / 1e6,
			DurationMs: round4(t.DurationMs), Spans: t.SpanCount, Error: t.HasError, ErrorSpans: t.ErrorSpans})
	}
	if len(traces) > limit {
		traces = traces[:limit]
	}
	ev.Rows = intPtr(len(traces))
	out := cosreResult{Tool: queryTracesName, OK: true, Request: req, Warnings: warnings, Evidence: ev}
	data := map[string]any{"hasMore": hasMore, "traces": traces}
	if summary != nil {
		data["serviceTotals"] = summary
	}
	if len(traces) == 0 {
		out.Empty = true
		st := ""
		if status == qtStatusError {
			st = "hatalı"
		}
		minTxt := ""
		if minMs > 0 {
			minTxt = fmt.Sprintf("%.0f ms+", minMs)
		}
		out.Message = "veri yok: " + scopeTR([2]string{"servis", service}, [2]string{"operasyon", operation}, [2]string{"durum", st},
			[2]string{"süre", minTxt}, [2]string{"env", env}) + " için " + windowTR(from, to) + " penceresinde trace bulunamadı (ClickHouse)"
	}
	out.Data = data
	return out, nil
}

// qtSummaryOf — SAF.
func qtSummaryOf(r chstore.ServiceWindowRED) *qtSummary {
	s := &qtSummary{Spans: r.Spans, Errors: r.Errors, AvgMs: round4(r.AvgMs), P50Ms: round4(r.P50Ms), P95Ms: round4(r.P95Ms), P99Ms: round4(r.P99Ms)}
	if r.Spans > 0 {
		s.ErrorRatePct = round4(100 * float64(r.Errors) / float64(r.Spans))
	}
	return s
}

// qtSummarySQL — SAF: servis toplamının ÇALIŞAN SQL'i, bind'ler yerinde.
func qtSummarySQL(service, env string, from, to time.Time) string {
	if env != "" {
		return inlineSQL(chstore.ServiceWindowREDSQL(true), service, env, chstore.AlignBucketStart(from), to)
	}
	return inlineSQL(chstore.ServiceWindowREDSQL(false), service, chstore.AlignBucketStart(from), to)
}

// qtListQueryText — SAF: örnek liste okumasının deterministik özeti.
func qtListQueryText(f chstore.TraceFilter) string {
	var where []string
	if f.Service != "" { // v0.10.1302 — çevrilmiş operasyon adı servissiz koşar
		where = append(where, "service_name = "+sqlQuote(f.Service))
	}
	if fs := filtersSummary(f.Filters); fs != "" {
		where = append(where, fs)
	}
	if f.Env != "" {
		where = append(where, "deploy_env = "+sqlQuote(f.Env))
	}
	if f.HasError {
		where = append(where, "has_error")
	}
	if f.MinMs > 0 {
		where = append(where, fmt.Sprintf("duration_ms >= %s", sqlLiteral(f.MinMs)))
	}
	where = append(where, "time >= "+sqlLiteral(f.From), "time < "+sqlLiteral(f.To))
	return fmt.Sprintf("-- örnekler: GetTraces[trace_summary_5m → spans] WHERE %s ORDER BY %s DESC LIMIT %d",
		strings.Join(where, " AND "), f.Sort, f.Limit)
}

// qtCodeFilterKey — normalize istekte çevrilmiş operasyonun kod süzgeci
// (EvidenceHref name süzgeci yerine bunu yazar).
const qtCodeFilterKey = "code_filter"
