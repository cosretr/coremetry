package mcptools

// v2_endpoint_metrics.go — CoSRE v2 araç seti dilim 3b: endpoint_metrics
// (docs/cosre/tools-audit.md §9; tasarım onayı 2026-10-31). Yalnız v2
// kataloğu (registerV2Subset); registry, dış MCP ve v1 sohbeti GÖRMEZ.
//
// Bir servisin uç noktalarının (http_route) RED'i, VictoriaMetrics'teki OTel
// HTTP sunucu histogramından (örneklenmemiş):
//   - route dolu → route kipi: istek hızı · 5xx % · p95/p99 (v0.10.1481 yolu,
//     api copilot_service_route_red.go svcRouteREDQueries);
//   - route boş → top-10 kipi: topk(10, sum by (http_route)(rate)) + 5xx %.
//
// Okuma api'de (Deps.EndpointMetricRED → api/copilot_endpoint_metrics.go);
// mcptools'ta VM kopyası YOK. Span'e geri düşüş YOK: VM yok / gRPC ailesi /
// rota etiketi yok → `skipped` + gerekçe (data-tool-result kind skipped);
// okuma hatası araç hatasıdır (kind error, "okunamadı ≠ yok").
//
// Gövde ≤ V2ToolMaxBytes; satırlar ≤10, sığmayan sondan düşer (`truncated`).
// PromQL'ler kanıt zarfının `evidence.query`'sinde (adım kartı sorguyu gösterir).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// EndpointMetricsName — v2-yalnız aracın adı.
const EndpointMetricsName = "endpoint_metrics"

const (
	emModeRoute = "route"
	emModeTop   = "top"

	emDefaultWindowS = 3600
	emTopK           = 10
	emMaxSteps       = 60
	emMinStepS       = 15
	emCacheTTL       = 60 * time.Second
	emRouteRunes     = 200
)

// endpointWindows — izinli pencereler (saniye; şema enum'u).
var endpointWindows = []int{900, 3600, 21600, 86400}

// EndpointREDResult — api okumasının sonucu (Deps.EndpointMetricRED).
// Skip dolu → okunmadı (gerekçe kartta). Seriler mcptools'ta satıra indirgenir.
type EndpointREDResult struct {
	Skip       string          // "" = okundu
	Env        string          // uygulanan ortam ("" = süzgeç yok)
	Notes      []string        // tek satırlık dürüst notlar (ortam uygulanmadı, fiil etiketi yok, …)
	Queries    []string        // koşulan PromQL'ler (sıra = okuma sırası)
	Rate       []EvChartSeries // route: tek seri; top: rota başına (ad = rota)
	Err5xx     []EvChartSeries // route: tek seri; top: rota başına
	Latency    []EvChartSeries // yalnız route: p95 / p99 (saniye)
	DurationMs int64
}

// EndpointMetricREDReader — api dikişi (nil → araç "VM okuyucusu yok" skipped).
type EndpointMetricREDReader func(ctx context.Context, service, env, route, method string, from, to time.Time) (EndpointREDResult, error)

// snapEndpointWindow — SAF: istenen pencere → izinli basamak (yukarı); 0 → 1 sa.
func snapEndpointWindow(w int) int {
	if w <= 0 {
		return emDefaultWindowS
	}
	for _, r := range endpointWindows {
		if w <= r {
			return r
		}
	}
	return endpointWindows[len(endpointWindows)-1]
}

// EndpointMetricStep — SAF: ≤60 nokta, en az 15 sn (api aynı adımı okur).
func EndpointMetricStep(windowS int) int {
	st := (windowS + emMaxSteps - 1) / emMaxSteps
	if st < emMinStepS {
		st = emMinStepS
	}
	return st
}

var emVerbs = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

type endpointMetricsArgs struct {
	Services []string `json:"services,omitempty"`
	Team     string   `json:"team,omitempty"`
	Service  string   `json:"service,omitempty"`
	Route    string   `json:"route,omitempty"`
	Method   string   `json:"method,omitempty"`
	Env      string   `json:"env,omitempty"`
	WindowS  int      `json:"window_s,omitempty"`
}

type endpointPlan struct {
	Service  string
	Services []string
	Route    string
	Method   string
	Env      string
	WindowS  int
	Step     int
	Mode     string
}

func errEndpointScope(svcs []string) error {
	if len(svcs) == 0 {
		return errors.New("no service: ask the user which service they mean (exact name) — never guess one")
	}
	names := svcs
	if len(names) > v2SvcSampleNames {
		names = names[:v2SvcSampleNames]
	}
	return fmt.Errorf("the user's scope has %d services (%s…) — ask which service they mean, then call again with `service`; endpoint_metrics reads ONE service", len(svcs), strings.Join(names, ", "))
}

// planEndpointMetrics — SAF: argüman → plan.
func planEndpointMetrics(a endpointMetricsArgs) (endpointPlan, error) {
	p := endpointPlan{Env: strings.TrimSpace(a.Env)}
	svcs, _ := normalizeMyServices(a.Services)
	p.Services = svcs
	p.Service = strings.TrimSpace(a.Service)
	if p.Service == "" {
		if len(svcs) != 1 {
			return p, errEndpointScope(svcs)
		}
		p.Service = svcs[0]
	}
	route := strings.TrimSpace(a.Route)
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if v, rest, cut := strings.Cut(route, " "); cut && emVerbs[strings.ToUpper(v)] {
		if method == "" {
			method = strings.ToUpper(v)
		}
		route = strings.TrimSpace(rest)
	}
	if method != "" && !emVerbs[method] {
		return p, fmt.Errorf("method %q invalid (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS)", truncateRunes(a.Method, 16))
	}
	if route != "" && (!strings.HasPrefix(route, "/") || strings.ContainsAny(route, " ?#\t\n") || len([]rune(route)) > emRouteRunes) {
		return p, fmt.Errorf("route %q is not an http_route template (\"/api/orders/{id}\"); omit route for the top-10 endpoints", truncateRunes(route, 60))
	}
	if method != "" && route == "" {
		return p, errors.New("method needs route: pass the route too, or omit both for the top-10 endpoints")
	}
	p.Route, p.Method = route, method
	p.Mode = emModeTop
	if route != "" {
		p.Mode = emModeRoute
	}
	p.WindowS = snapEndpointWindow(a.WindowS)
	p.Step = EndpointMetricStep(p.WindowS)
	return p, nil
}

// endpointMetricsCacheKey — SAF: TÜM girdiler (kaynak adı, servis, sıralı küme
// FNV, rota, fiil, ortam, pencere, adım, pencere sonu dakika) — uzunluk değil.
func endpointMetricsCacheKey(metricsName string, p endpointPlan, to time.Time) string {
	return fmt.Sprintf("mcp:endpoint_metrics:v1:%s:%s:%s:%s:%s:%s:%d:%d:%d",
		fnvHex(metricsName), fnvHex(p.Service), fnvHex(p.Services...), fnvHex(p.Route), p.Method,
		fnvHex(p.Env), p.WindowS, p.Step, to.Unix()/60)
}

// EndpointMetricsPageURL — SAF: Endpoints sayfası, metrik kipi, rota araması,
// mutlak pencere (Endpoints.tsx: service / src / search / range / env).
func EndpointMetricsPageURL(service, route, env string, from, to time.Time) string {
	q := url.Values{}
	q.Set("service", service)
	q.Set("src", "metric")
	if route != "" {
		q.Set("search", route)
	}
	if env != "" {
		q.Set("env", env)
	}
	q.Set("range", fmt.Sprintf("custom:%d-%d", from.UnixMilli(), to.UnixMilli()))
	return "/endpoints?" + q.Encode()
}

type emRow struct {
	Route     string   `json:"route"`
	Method    string   `json:"method,omitempty"`
	RPS       float64  `json:"rps"`
	Err5xxPct *float64 `json:"err5xx_pct,omitempty"`
	P95Ms     *float64 `json:"p95_ms,omitempty"`
	P99Ms     *float64 `json:"p99_ms,omitempty"`
	Last      float64  `json:"last"`
	Peak      float64  `json:"peak"`
}

// emStats — SAF: noktaların ortalaması, sonu, tepesi, toplamı (NaN/Inf atılır).
func emStats(s EvChartSeries) (mean, last, peak, sum float64, ok bool) {
	n := 0
	peak = math.Inf(-1)
	for _, pt := range s.Points {
		v := pt[1]
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		sum += v
		last = v
		if v > peak {
			peak = v
		}
		n++
	}
	if n == 0 {
		return 0, 0, 0, 0, false
	}
	return sum / float64(n), last, peak, sum, true
}

func emMean(s []EvChartSeries, name string) *float64 {
	for _, x := range s {
		if name == "" || x.Name == name {
			if m, _, _, _, ok := emStats(x); ok {
				v := round4(m)
				return &v
			}
			return nil
		}
	}
	return nil
}

func emMs(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := round4(*p * 1000)
	return &v
}

// endpointRows — SAF: seriler → satırlar. route kipi tek satır (istek hızı
// serisi yoksa satır YOK); top kipi hacme (nokta toplamı) göre ilk 10.
func endpointRows(p endpointPlan, r EndpointREDResult) ([]emRow, bool) {
	if p.Mode == emModeRoute {
		if len(r.Rate) == 0 {
			return nil, false
		}
		mean, last, peak, _, ok := emStats(r.Rate[0])
		if !ok {
			return nil, false
		}
		row := emRow{Route: p.Route, Method: p.Method, RPS: round4(mean), Last: round4(last), Peak: round4(peak)}
		if len(r.Err5xx) > 0 {
			row.Err5xxPct = emMean(r.Err5xx[:1], "")
		}
		row.P95Ms, row.P99Ms = emMs(emMean(r.Latency, "p95")), emMs(emMean(r.Latency, "p99"))
		return []emRow{row}, false
	}
	type ranked struct {
		row emRow
		sum float64
	}
	var all []ranked
	for _, s := range r.Rate {
		mean, last, peak, sum, ok := emStats(s)
		if !ok || s.Name == "" {
			continue
		}
		row := emRow{Route: s.Name, RPS: round4(mean), Last: round4(last), Peak: round4(peak), Err5xxPct: emMean(r.Err5xx, s.Name)}
		all = append(all, ranked{row, sum})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].sum != all[j].sum {
			return all[i].sum > all[j].sum
		}
		return all[i].row.Route < all[j].row.Route
	})
	more := len(all) > emTopK
	if more {
		all = all[:emTopK]
	}
	out := make([]emRow, len(all))
	for i, x := range all {
		out[i] = x.row
	}
	return out, more
}

// endpointMetricsPayload — SAF: ≤ 2 KB gövde. Skip → yalnız gerekçe (page_url
// ve kanıt YOK: okunmadı).
func endpointMetricsPayload(p endpointPlan, r EndpointREDResult, from, to time.Time) map[string]any {
	out := map[string]any{"tool": EndpointMetricsName, "service": p.Service, "mode": p.Mode, "window_s": p.WindowS}
	if p.Route != "" {
		out["route"] = p.Route
	}
	if r.Skip != "" {
		out["skipped"], out["reason"] = true, r.Skip
		out["note"] = "not read — no span fallback; say the endpoint metrics are unavailable and why, do not infer health"
		return out
	}
	rows, more := endpointRows(p, r)
	n := len(rows)
	env := r.Env
	out["source"], out["step_s"], out["has_more"] = "vm", p.Step, more
	out["page_url"] = EndpointMetricsPageURL(p.Service, p.Route, env, from, to)
	if env != "" {
		out["env"] = env
	}
	out["evidence"] = cosreEvidence{Source: evSourceVM, QueryKind: evKindPromQL, Query: strings.Join(r.Queries, "\n"),
		Window: evWin(from, to), Env: env, Rows: &n, DurationMs: r.DurationMs}
	if p.Mode == emModeTop {
		out["order"] = "request volume desc (top 10)"
	}
	if len(r.Notes) > 0 {
		out["note"] = strings.Join(r.Notes, "; ")
	}
	if n == 0 {
		what := "this service"
		if p.Route != "" {
			what = "this route"
		}
		out["reasons"] = []string{"VictoriaMetrics has no HTTP server series for " + what + " in the window — absent is not healthy; check the route spelling (http_route template) or widen window_s"}
	}
	return v2Fit(out, n, func(k int) { out["endpoints"], out["count"] = rows[:k], k })
}

// V2SkipReason — SAF: v2 aracının "okunmadı" gerekçesi ("" = okundu / başka araç).
// Adım sonucunu kind skipped yapar (api emitStepEvidence / sohbet döngüsü).
func V2SkipReason(tool, content string) string {
	if tool != EndpointMetricsName {
		return ""
	}
	var r struct {
		Skipped bool   `json:"skipped"`
		Reason  string `json:"reason"`
	}
	if json.Unmarshal([]byte(content), &r) != nil || !r.Skipped {
		return ""
	}
	return truncateRunes(r.Reason, 300)
}

func endpointMetricsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: EndpointMetricsName,
		ShortDescription: "Bir servisin uç noktalarının (http_route) RED'i VictoriaMetrics'ten (örneklenmemiş): route verilirse o rota (istek hızı, 5xx %, p95/p99), " +
			"verilmezse en yoğun 10 rota (istek hızı + 5xx %). Tek servis; kapsamda çok servis varsa kullanıcıya hangisi diye sor.",
		Description: "Endpoint (http_route) RED for ONE service from VictoriaMetrics — the OTel collector's unsampled HTTP server histogram (same metric as the Endpoints page in metric mode). " +
			"With `route` (an http_route template such as /api/orders/{id}, optional `method`): that route's request rate (rps mean / last / peak), 5xx % and p95/p99 latency in ms. " +
			"Without `route`: the top 10 routes by request volume with rps and 5xx % (no latency). " +
			"Use it for 'which endpoint is slow/failing', 'how is GET /api/orders doing'. Service: pass `service`, or let the server inject the user's services — when the scope holds more than one service the call fails and you must ask which service. " +
			"Not available (returned as skipped with a reason, never a span fallback): VictoriaMetrics not configured, the service's metric family is gRPC, the histogram lacks the http_route label. A read failure is an error (unreadable is not absent). " +
			"Empty endpoints = no series in the window, not proof of health. For span-based operation RED use get_operation_health. Body ≤2 KB, `page_url` opens the Endpoints page; cached 60 s.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"services": servicesSchema("Only a one-service scope is used; with several services ask the user."),
				"team":     map[string]any{"type": "string", "description": "Team label, filled by the server. Omit."},
				"service":  map[string]any{"type": "string", "description": "Exact service name; overrides `services`."},
				"route":    map[string]any{"type": "string", "description": "http_route template, e.g. /api/orders/{id}. Omit for the top 10 endpoints."},
				"method":   map[string]any{"type": "string", "enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}, "description": "HTTP method; only with route."},
				"env":      map[string]any{"type": "string", "description": "Deployment environment (e.g. prod). Empty = all."},
				"window_s": map[string]any{"type": "integer", "enum": endpointWindows, "description": "Window seconds: 900, 3600 (default), 21600 or 86400."},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a endpointMetricsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			p, err := planEndpointMetrics(a)
			if err != nil {
				return nil, err
			}
			from, to := rangeWindow(ctx, p.WindowS)
			if d.EndpointMetricRED == nil {
				return endpointMetricsPayload(p, EndpointREDResult{Skip: "VictoriaMetrics okuyucusu yok"}, from, to), nil
			}
			src := ""
			if d.MetricsName != nil {
				src = d.MetricsName()
			}
			return v2Memo(ctx, d, endpointMetricsCacheKey(src, p, to), emCacheTTL, func(c context.Context) (map[string]any, error) {
				rctx, cancel := context.WithTimeout(c, cosreBudget)
				defer cancel()
				r, err := d.EndpointMetricRED(rctx, p.Service, p.Env, p.Route, p.Method, from, to)
				if err != nil {
					return nil, err
				}
				return endpointMetricsPayload(p, r, from, to), nil
			})
		},
	}
}
