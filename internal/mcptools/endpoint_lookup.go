package mcptools

// endpoint_lookup.go — v0.10.2449 (operatör raporu 2026-11-22: prod CoSRE
// soruları çoğunlukla ENDPOINT hakkında ve tekrar soruluyor — "bu servisin
// url'i / endpoint'i nedir", "<OPERASYON> exact endpoint url", "<domain> bu
// domainin çözdüğü pod hangisi, 3 cluster…").
//
// Tek deterministik çağrı, dört bölüm:
//   - routes   — servisin GİRİŞ rotaları (http.route / span adı; kind server|
//     consumer) spanmetrics_1m'den (MV; ham span yok).
//   - observed — giriş span'lerinde istemcinin kullandığı host / yol / fiil
//     SAKLANDIĞI gibi (server.address, http.host, url.full …) + cluster + o
//     span'leri üreten pod'lar (host_name). Ham spans, sınırlı (chstore
//     cosre_lookup.go: pencere tavanı, satır tavanı, LIMIT, süre tavanı).
//   - red      — en çok çağrılan ≤3 rota için RED: önce örneklenmemiş kaynak
//     (VictoriaMetrics HTTP sunucu histogramı, Deps.EndpointMetricRED —
//     v0.10.2435 aile önceliği), yoksa spanmetrics_1m (örneklenmiş span;
//     kaynak alanı söyler).
//   - served_by — domain / URL'i servis eden servisler → pod'lar ENTITY
//     modelinden (Deps.EntityNeighbours; ad eşlemesi yok), cluster başına.
//     external_callers: domain bir dış host ise onu çağıran servisler
//     (topology_edges_5m MV).
// Her sayı bir okumadan gelir; okunamayan bölüm `unread`'e yazılır
// (okunamadı ≠ yok).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// EndpointLookupName — aracın adı (router ipucu ve testler okur).
const EndpointLookupName = "endpoint_lookup"

const (
	elDefaultRangeS = 3600
	elMaxRangeS     = 86400
	elRoutesMax     = 15
	elObservedMax   = 20
	elREDMax        = 3
	elServedMax     = 3
	elPodsPerClust  = 10
)

type endpointLookupArgs struct {
	Service   string `json:"service,omitempty"`
	Operation string `json:"operation,omitempty"`
	URL       string `json:"url,omitempty"`
	Domain    string `json:"domain,omitempty"`
	RangeS    int    `json:"range_s,omitempty"`
}

// endpointLookupReader — chstore okumaları (test sahtesi bunu uygular).
type endpointLookupReader interface {
	EndpointEntryRoutes(ctx context.Context, services []string, frag string, from, to time.Time, limit int) ([]chstore.EndpointRouteRow, error)
	EndpointObserved(ctx context.Context, q chstore.EndpointObservedQuery) (chstore.EndpointObservedResult, error)
	OutboundHostCallers(ctx context.Context, host string, from, to time.Time) ([]chstore.ExternalCaller, error)
}

// elREDRow — tek rotanın RED'i ve KAYNAĞI.
type elREDRow struct {
	Service   string   `json:"service"`
	Route     string   `json:"route"`
	Source    string   `json:"source"` // vm (örneklenmemiş) | spanmetrics_1m (örneklenmiş span)
	RPS       *float64 `json:"rps,omitempty"`
	Calls     *uint64  `json:"spans,omitempty"`
	ErrPct    *float64 `json:"error_pct,omitempty"`
	Err5xxPct *float64 `json:"err5xx_pct,omitempty"`
	P95Ms     *float64 `json:"p95_ms,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// elServed — domain'i servis eden servisin pod'ları (entity modeli).
type elServed struct {
	Service   string              `json:"service"`
	Clusters  map[string][]string `json:"pods_by_cluster,omitempty"` // cluster id → pod adları (≤10)
	PodCount  int                 `json:"pod_count"`
	PodSource string              `json:"pod_source,omitempty"`
	Note      string              `json:"note,omitempty"`
}

// normalizeLookupDomain — SAF: "https://api.example.com:8443/x" → "api.example.com".
func normalizeLookupDomain(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if h := urlHost(s); h != "" {
		return h
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(strings.Trim(s, ". "))
}

// splitLookupURL — SAF: tam URL → (host, yol parçası); yol yoksa "".
func splitLookupURL(u string) (host, path string) {
	u = strings.TrimSpace(u)
	if h := urlHost(u); h != "" {
		rest := u[strings.Index(u, "://")+3:]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			path = rest[i:]
		}
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if path == "/" {
			path = ""
		}
		return h, path
	}
	return "", u
}

// podsByCluster — SAF: entity pod id'leri (pod:<cid>/<ns>/<ad>) → cluster → adlar.
func podsByCluster(ids []string, perCluster int) map[string][]string {
	out := map[string][]string{}
	for _, id := range ids {
		ref, ok := entity.ParseID(id)
		if !ok || ref.Type != entity.TypePod {
			continue
		}
		if len(out[ref.ClusterID]) < perCluster {
			out[ref.ClusterID] = append(out[ref.ClusterID], ref.Name)
		}
	}
	for k := range out {
		sort.Strings(out[k])
	}
	return out
}

// elServices — SAF: observed + routes satırlarından tekil servisler (hacim sırası).
func elServices(obs []chstore.EndpointObservedRow, routes []chstore.EndpointRouteRow, max int) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] && len(out) < max {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, r := range obs {
		add(r.Service)
	}
	for _, r := range routes {
		add(r.Service)
	}
	return out
}

func runEndpointLookup(ctx context.Context, d Deps, r endpointLookupReader, a endpointLookupArgs) (map[string]any, error) {
	svc := strings.TrimSpace(a.Service)
	op := strings.TrimSpace(a.Operation)
	domain := normalizeLookupDomain(a.Domain)
	urlHostPart, urlPath := splitLookupURL(a.URL)
	if domain == "" && urlHostPart != "" {
		domain = urlHostPart
	}
	if svc == "" && op == "" && domain == "" && urlPath == "" {
		return nil, errors.New("give at least one of service, operation, url or domain — ask the user which service / URL they mean, never guess one")
	}
	if r == nil {
		return nil, errors.New("telemetry store not configured")
	}
	rangeS := a.RangeS
	if rangeS <= 0 {
		rangeS = elDefaultRangeS
	}
	if rangeS > elMaxRangeS {
		rangeS = elMaxRangeS
	}
	from, to := rangeWindow(ctx, rangeS)
	out := map[string]any{"tool": EndpointLookupName, "window_s": rangeS}
	q := map[string]any{}
	for k, v := range map[string]string{"service": svc, "operation": op, "domain": domain, "path": urlPath} {
		if v != "" {
			q[k] = v
		}
	}
	out["query"] = q
	var unread, notes []string

	// 1) giriş rotaları (MV)
	var services []string
	if svc != "" {
		services = []string{svc}
	}
	frag := op
	if frag == "" {
		frag = urlPath
	}
	routes := []chstore.EndpointRouteRow{}
	if svc != "" || frag != "" {
		rr, err := r.EndpointEntryRoutes(ctx, services, frag, from, to, elRoutesMax)
		if err != nil {
			unread = append(unread, "routes: "+err.Error())
		} else {
			routes = rr
		}
	}
	out["routes"] = routes
	out["routes_source"] = "spanmetrics_1m (entry spans: server/consumer)"

	// 2) gözlenen host / yol (ham, sınırlı). Servis yoksa rotalardan gelen
	// servisler önek olur (ucuz yol); o da yoksa yalnız domain/URL ile.
	obsSvcs := services
	if len(obsSvcs) == 0 && op != "" {
		obsSvcs = elServices(nil, routes, 5)
	}
	obs := chstore.EndpointObservedResult{Rows: []chstore.EndpointObservedRow{}}
	if len(obsSvcs) > 0 || domain != "" || urlPath != "" {
		oq := chstore.EndpointObservedQuery{Services: obsSvcs, Host: domain, From: from, To: to, Limit: elObservedMax}
		if op == "" {
			oq.URL = urlPath
		}
		res, err := r.EndpointObserved(ctx, oq)
		if err != nil {
			unread = append(unread, "observed: "+err.Error())
		} else {
			obs = res
		}
	}
	out["observed"] = obs.Rows
	if !obs.From.IsZero() {
		out["observed_window_s"] = int(obs.To.Sub(obs.From).Seconds())
		if obs.Clamped {
			notes = append(notes, fmt.Sprintf("observed URL/host rows read only the last %d s of the window (raw-span cap)", int(obs.To.Sub(obs.From).Seconds())))
		}
		if len(obsSvcs) == 0 {
			notes = append(notes, fmt.Sprintf("domain/URL search without a service reads at most %d spans (%s spans; a missing row is not proof the domain is unused — give the service to search wider)", obs.MaxRows, spanLabel(obs.Weighted)))
		}
	}
	if obs.Weighted {
		notes = append(notes, "observed spans/error_spans/p95_ms are "+spanLabelWeighted+" (sampling-weighted) span counts; rates and error % for decisions come from `red`")
	} else {
		notes = append(notes, "observed spans/error_spans/p95_ms count stored (sampled) spans; rates and error % for decisions come from `red`")
	}

	// 3) RED — örneklenmemiş önce.
	out["red"] = endpointLookupRED(ctx, d, routes, from, to)

	// 4) domain → servis → pod (entity modeli) + dış çağıranlar.
	if domain != "" || urlPath != "" {
		served := []elServed{}
		for _, s := range elServices(obs.Rows, nil, elServedMax) {
			served = append(served, endpointServedBy(ctx, d, s))
		}
		out["served_by"] = served
		if len(obs.Rows) == 0 {
			notes = append(notes, "no instrumented entry span carried this domain/URL in the window — it may be served by an uninstrumented ingress/gateway, or be an external host (see external_callers)")
		}
	}
	if domain != "" {
		callers, err := r.OutboundHostCallers(ctx, domain, from, to)
		if err != nil {
			unread = append(unread, "external_callers: "+err.Error())
		} else if len(callers) > 0 {
			if len(callers) > 10 {
				callers = callers[:10]
			}
			out["external_callers"] = callers
		}
	}
	if len(unread) > 0 {
		out["unread"] = unread
	}
	out["notes"] = notes
	return out, nil
}

// endpointLookupRED — en çok çağrılan ≤3 rota; VM varsa VM, yoksa MV satırı.
func endpointLookupRED(ctx context.Context, d Deps, routes []chstore.EndpointRouteRow, from, to time.Time) []elREDRow {
	out := []elREDRow{}
	for i, rt := range routes {
		if i >= elREDMax {
			break
		}
		route := rt.Route
		if route == "" {
			route = rt.Name
		}
		row := elREDRow{Service: rt.Service, Route: route}
		if d.EndpointMetricRED != nil && rt.Route != "" {
			res, err := d.EndpointMetricRED(ctx, rt.Service, "", rt.Route, "", from, to)
			if err == nil && res.Skip == "" {
				if rows, _ := endpointRows(endpointPlan{Mode: emModeRoute, Route: rt.Route}, res); len(rows) == 1 {
					rps := rows[0].RPS
					row.Source, row.RPS, row.Err5xxPct, row.P95Ms = "vm (unsampled)", &rps, rows[0].Err5xxPct, rows[0].P95Ms
					out = append(out, row)
					continue
				}
			}
			if err != nil {
				row.Note = "vm read failed: " + err.Error()
			} else if res.Skip != "" {
				row.Note = "vm skipped: " + res.Skip
			}
		}
		calls := rt.Calls
		row.Source, row.Calls = "spanmetrics_1m ("+spanLabel(rt.Weighted)+" spans)", &calls
		if rt.Calls > 0 {
			pct := round4(float64(rt.Errors) * 100 / float64(rt.Calls))
			row.ErrPct = &pct
		}
		p95 := round4(rt.P95Ms)
		row.P95Ms = &p95
		out = append(out, row)
	}
	return out
}

// endpointServedBy — servis → pod'lar (entity.Resolver komşuları).
func endpointServedBy(ctx context.Context, d Deps, service string) elServed {
	s := elServed{Service: service}
	if d.EntityNeighbours == nil {
		s.Note = "entity model not available — pods come only from the spans' host_name in observed[].pods"
		return s
	}
	nb, err := d.EntityNeighbours(ctx, EntityCandidate{Kind: entity.TypeService, Name: service, ID: entity.ServiceID(service)})
	if nb == nil {
		if err != nil {
			s.Note = "entity read failed: " + err.Error()
		}
		return s
	}
	s.Clusters = podsByCluster(nb.Pods, elPodsPerClust)
	s.PodCount, s.PodSource = nb.PodCount, nb.PodSource
	if err != nil {
		s.Note = "entity read partial: " + err.Error()
	}
	return s
}

func endpointLookupTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             EndpointLookupName,
		ShortDescription: "Servisin url'i/endpoint'i, domain hangi servis/pod (cluster başına): giriş rotaları, gözlenen host+yol, RED.",
		Description: "Answer 'what is this service's URL / endpoint', 'exact endpoint URL of operation X' and 'which service / pod serves domain D (per cluster)' in ONE call. " +
			"Give any of: service (exact name), operation (span name or route fragment), url (full URL or path fragment), domain (host). Returns " +
			"routes (≤15 entry routes — http.route or span name, kind server/consumer — with spans/errors/p95 from the spanmetrics_1m pre-aggregate), " +
			"observed (≤20 rows: the host, method and normalized path clients actually used, as stored in server.address / http.host / url.full, per cluster, with up to 5 pod names (span host_name) and one sample_url), " +
			"red (≤3 busiest routes: unsampled VictoriaMetrics HTTP server histogram when configured — rps, err5xx_pct, p95_ms — else spanmetrics_1m sampled spans; `source` says which), " +
			"served_by (for domain/url: services seen serving it → their pods per cluster from the entity model), external_callers (services calling the domain as an external host, topology MV), notes, unread (sections that failed — unread is not 'none'). " +
			"COST: routes read the 1-minute MV; observed is a bounded raw-span read (service-scoped ≤30 min; domain-only ≤5 min and a row cap — say so when empty). " +
			"Quote values exactly; do not invent URLs, hosts or pods that are not in the result. For one route's RED over time use endpoint_metrics; for trace-level questions use trace_where.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":   map[string]any{"type": "string", "description": "Exact service name (list_services / resolve_entity)."},
				"operation": map[string]any{"type": "string", "description": "Operation / span name or route fragment (case-insensitive substring)."},
				"url":       map[string]any{"type": "string", "description": "Full URL or path fragment the user gave (e.g. https://api.example.com/orders or /orders)."},
				"domain":    map[string]any{"type": "string", "description": "Host / domain the user gave (e.g. api.example.com); scheme and port are stripped."},
				"range_s": map[string]any{"type": "integer", "minimum": 300, "maximum": elMaxRangeS,
					"description": "Lookback seconds ending at the chat window end. Default 3600. Raw observed rows are capped shorter (see observed_window_s)."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a endpointLookupArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r endpointLookupReader
			if d.Store != nil {
				r = d.Store
			}
			return runEndpointLookup(ctx, d, r, a)
		},
	}
}
