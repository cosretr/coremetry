package mcptools

// traces_search.go — v0.10.2465 (operator-reported, prod 2026-11-22: "Son 1
// saat içerisinde url'inde ocp5 içeren bir servis çağrımı veya trace'i var
// mı?"). "ocp5" bir URL parçası değil, bir CLUSTER / ENV değeriydi
// (resource.openshift.cluster.name = ocp5, deployment.environment =
// prod-ocp5); operatör cevabı Traces sayfasında `cluster = ocp5` süzgeciyle
// buldu (6 saatte 16.9K istek). CoSRE aynısını yapar ve sayfayı AÇAR.
//
// Tek çağrı, üç adım:
//  1. Değer çözümü (value) — çıplak belirteç önce UCUZ, düşük kardinaliteli
//     bilinen boyutlarda aranır, sırayla: cluster (span `cluster` kolonu —
//     openshift.cluster.name / k8s.cluster.name türetmesi — ve Remote Cluster
//     adları), env (deploy_env; tam eşleşme, sonra "-"/"_"/"." parçası:
//     prod-ocp5), namespace (entity_seen). Kaynak Deps.TraceValueDims (api:
//     picker okumalarıyla aynı, LC kolon / MV, 60 sn memo). Ham öznitelik
//     taraması YOK (find_attribute_by_value filo çapında reddeder — kalır).
//     Hiçbir boyut tutmazsa yorum "url" olur: endpoint_lookup(url=değer)
//     önerilir, burada okuma koşmaz.
//  2. Her boyut yorumu (≤3) için Traces sayfasının Aggregated okuması
//     (GetTraceAggregate, groupBy=service — trace_summary_5m MV hızlı yolu,
//     env/namespace'te sınırlı ham yol): istek / hata toplamı, servis
//     başına p99. İlk yorumda ek olarak operasyonlar (groupBy=operation) ve
//     ≤5 örnek trace id (GetTraces).
//  3. deep_link — ilk yorumun süzgeçleriyle /traces (TracesDeepLink; sayfanın
//     okuduğu parametreler: service, search, filters, hasError, minMs/maxMs,
//     env, cluster, range=custom:ms-ms). Sohbet "Traces'te aç" çipi yapar.
//
// Pencere: varsayılan 1 sa, tavan 6 sa (find_traces'in indeksli tavanı);
// kullanıcı süzgeçleri search_traces kapısından (applySearchGate) geçer.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// TracesSearchName — aracın adı (router ipucu, çip köprüsü, testler).
const TracesSearchName = "traces_search"

const (
	tsrDefaultRangeS = 3600
	tsrMaxRangeS     = 6 * 3600
	tsrMaxMatches    = 3
	tsrSvcGroups     = 50
	tsrTopServices   = 5
	tsrTopOps        = 5
	tsrSamples       = 5
)

// Değer yorumu türleri (çözüm sırası).
const (
	TraceValueCluster   = "cluster"
	TraceValueEnv       = "env"
	TraceValueNamespace = "namespace"
	TraceValueURL       = "url"
)

// TraceValueDims — değer çözümünün aday listeleri (api tarafı doldurur).
// Clusters: span cluster değerleri (son 1 sa, ≤200); Envs: değeri içeren
// deploy_env'ler; Namespaces: görülen namespace'ler.
type TraceValueDims struct {
	Clusters   []string `json:"clusters,omitempty"`
	Envs       []string `json:"envs,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// TraceValueMatch — bir yorum ve (okunduysa) sayıları.
type TraceValueMatch struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Match    string `json:"match"` // exact | segment | fallback
	Requests uint64 `json:"requests"`
	Errors   uint64 `json:"errors"`
	Read     bool   `json:"read"`
	Note     string `json:"note,omitempty"`
}

type tracesSearchArgs struct {
	Value      string         `json:"value,omitempty"`
	Service    string         `json:"service,omitempty"`
	Operation  string         `json:"operation,omitempty"`
	Cluster    string         `json:"cluster,omitempty"`
	Env        string         `json:"env,omitempty"`
	Namespace  string         `json:"namespace,omitempty"`
	Filters    []SearchFilter `json:"filters,omitempty"`
	ErrorsOnly bool           `json:"errors_only,omitempty"`
	RootOnly   bool           `json:"root_only,omitempty"`
	MinMs      float64        `json:"min_ms,omitempty"`
	MaxMs      float64        `json:"max_ms,omitempty"`
	RangeS     int            `json:"range_s,omitempty"`
}

// tsrReader — okuma dikişleri (testte sahte).
type tsrReader struct {
	agg    func(ctx context.Context, f chstore.AggregateFilter) ([]chstore.AggregateRow, error)
	traces func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error)
	dims   func(ctx context.Context, value string) (TraceValueDims, error)
}

func tsrReaderFrom(d Deps) tsrReader {
	r := tsrReader{dims: d.TraceValueDims}
	if d.Store != nil {
		r.agg, r.traces = d.Store.GetTraceAggregate, d.Store.GetTraces
	}
	return r
}

// valueSegments — SAF: "prod-ocp5" → [prod ocp5].
func valueSegments(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' || r == '.' || r == '/' })
}

// ResolveTraceValue — SAF: çıplak değer → yorumlar, çözüm sırasıyla (cluster →
// env → namespace; hiçbiri yoksa url). refs: Remote Cluster'lar (ad / id →
// span değeri).
func ResolveTraceValue(value string, dims TraceValueDims, refs []ClusterRef) []TraceValueMatch {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	var out []TraceValueMatch
	seen := map[string]bool{}
	add := func(kind, val, match string) {
		if k := kind + "\x00" + strings.ToLower(val); !seen[k] && len(out) < tsrMaxMatches {
			seen[k] = true
			out = append(out, TraceValueMatch{Kind: kind, Value: val, Match: match})
		}
	}
	for _, c := range dims.Clusters {
		if strings.EqualFold(c, v) {
			add(TraceValueCluster, c, "exact")
		}
	}
	for _, c := range refs {
		if strings.EqualFold(c.ID, v) || strings.EqualFold(c.Name, v) {
			val := c.Name
			if len(c.SpanValues) > 0 {
				val = c.SpanValues[0]
			}
			add(TraceValueCluster, val, "exact")
		}
	}
	envs := append([]string(nil), dims.Envs...)
	sort.Strings(envs)
	for _, e := range envs {
		if strings.EqualFold(e, v) {
			add(TraceValueEnv, e, "exact")
		}
	}
	for _, e := range envs {
		for _, seg := range valueSegments(e) {
			if strings.EqualFold(seg, v) && !strings.EqualFold(e, v) {
				add(TraceValueEnv, e, "segment")
				break
			}
		}
	}
	for _, n := range dims.Namespaces {
		if strings.EqualFold(n, v) {
			add(TraceValueNamespace, n, "exact")
		}
	}
	if len(out) == 0 {
		out = append(out, TraceValueMatch{Kind: TraceValueURL, Value: v, Match: "fallback",
			Note: "no cluster / env / namespace carries this value — try endpoint_lookup(url=" + v + ") for a URL host/path fragment"})
	}
	return out
}

// tsrScope — bir yorumun kapsam alanları (TraceFilter / AggregateFilter ortak).
type tsrScope struct {
	Cluster, Env string
	Filters      []chstore.FilterExpr
}

// scopeFor — SAF: yorum → kapsam (kullanıcı süzgeçleri önde).
func scopeFor(m TraceValueMatch, user []chstore.FilterExpr) tsrScope {
	sc := tsrScope{Filters: append([]chstore.FilterExpr(nil), user...)}
	switch m.Kind {
	case TraceValueCluster:
		sc.Cluster = m.Value
	case TraceValueEnv:
		sc.Env = m.Value
	case TraceValueNamespace:
		sc.Filters = append(sc.Filters, chstore.FilterExpr{Key: "k8s.namespace.name", Op: "=", Values: []string{m.Value}})
	}
	return sc
}

// tsrService — modele giden servis satırı.
type tsrService struct {
	Service string  `json:"service"`
	Traces  uint64  `json:"traces"`
	Errors  uint64  `json:"errors"`
	P99Ms   float64 `json:"p99_ms"`
}

// sumServiceRows — SAF: servis grupları → toplam + ilk N (trace sayısına göre).
func sumServiceRows(rows []chstore.AggregateRow, top int) (req, errs uint64, out []tsrService) {
	sorted := append([]chstore.AggregateRow(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].TraceCount > sorted[j].TraceCount })
	for _, r := range sorted {
		req += r.TraceCount
		errs += r.ErrorCount
		if len(out) < top {
			out = append(out, tsrService{Service: r.GroupKey, Traces: r.TraceCount, Errors: r.ErrorCount, P99Ms: round4(r.P99Ms)})
		}
	}
	return req, errs, out
}

func runTracesSearch(ctx context.Context, d Deps, r tsrReader, a tracesSearchArgs) (map[string]any, error) {
	if r.agg == nil || r.traces == nil {
		return nil, errors.New("telemetry store not configured")
	}
	var user []chstore.FilterExpr
	for _, sf := range a.Filters {
		fe, err := toFilterExpr(sf)
		if err != nil {
			return nil, err
		}
		user = append(user, fe)
	}
	if err := chstore.ValidateFilters(user); err != nil {
		return nil, fmt.Errorf("filters: %w", err)
	}
	// Yorumlar: açık boyut argümanı > çözülen değer.
	var matches []TraceValueMatch
	switch {
	case strings.TrimSpace(a.Cluster) != "":
		c := strings.TrimSpace(a.Cluster)
		if cs, ok := clustersFor(d, c); ok && len(cs) == 1 && len(cs[0].SpanValues) > 0 {
			c = cs[0].SpanValues[0]
		}
		matches = []TraceValueMatch{{Kind: TraceValueCluster, Value: c, Match: "given"}}
	case strings.TrimSpace(a.Env) != "":
		matches = []TraceValueMatch{{Kind: TraceValueEnv, Value: strings.TrimSpace(a.Env), Match: "given"}}
	case strings.TrimSpace(a.Namespace) != "":
		matches = []TraceValueMatch{{Kind: TraceValueNamespace, Value: strings.TrimSpace(a.Namespace), Match: "given"}}
	case strings.TrimSpace(a.Value) != "":
		var dims TraceValueDims
		var unread string
		if r.dims != nil {
			var err error
			if dims, err = r.dims(ctx, strings.TrimSpace(a.Value)); err != nil {
				unread = "value dimensions: " + err.Error()
			}
		}
		var refs []ClusterRef
		if d.Clusters != nil {
			refs = d.Clusters()
		}
		matches = ResolveTraceValue(a.Value, dims, refs)
		if unread != "" && len(matches) > 0 {
			matches[0].Note = strings.TrimSpace(matches[0].Note + " (" + unread + ")")
		}
	}
	svc, op := strings.TrimSpace(a.Service), strings.TrimSpace(a.Operation)
	if len(matches) == 0 && svc == "" && op == "" && len(user) == 0 {
		return nil, errors.New("give value (bare token like a cluster / env name), cluster, env, namespace, service, operation or filters — ask the user, never guess")
	}
	rangeS := a.RangeS
	if rangeS <= 0 {
		rangeS = tsrDefaultRangeS
	}
	if rangeS > tsrMaxRangeS {
		rangeS = tsrMaxRangeS
	}
	gate, err := applySearchGate(rangeS, tsrSamples, user, true, false, chstore.AttrIndexAvailable())
	if err != nil {
		return nil, err
	}
	from, to := rangeWindow(ctx, gate.RangeS)
	out := map[string]any{"tool": TracesSearchName, "window_s": gate.RangeS}
	if gate.Reason != "" {
		out["window_clamped_reason"] = gate.Reason
	}
	// Boyut yorumu yoksa (yalnız servis / operasyon / süzgeç) tek "given" kapsam.
	runnable := matches
	if len(matches) == 0 {
		runnable = []TraceValueMatch{{Kind: "query", Match: "given"}}
	}
	var unread []string
	primary := -1
	for i := range runnable {
		m := &runnable[i]
		if m.Kind == TraceValueURL {
			continue
		}
		sc := scopeFor(*m, user)
		af := chstore.AggregateFilter{GroupBy: "service", Service: svc, Search: op, From: from, To: to,
			HasError: a.ErrorsOnly, MinMs: a.MinMs, MaxMs: a.MaxMs, Env: sc.Env, Cluster: sc.Cluster,
			Filters: sc.Filters, Sort: "count", Order: "desc", Limit: tsrSvcGroups}
		rows, err := r.agg(ctx, af)
		if err != nil {
			unread = append(unread, m.Kind+" "+m.Value+": "+err.Error())
			continue
		}
		req, errs, top := sumServiceRows(rows, tsrTopServices)
		m.Requests, m.Errors, m.Read = req, errs, true
		if len(rows) >= tsrSvcGroups {
			m.Note = strings.TrimSpace(m.Note + fmt.Sprintf(" totals cover the top %d services only (lower bound)", tsrSvcGroups))
		}
		if primary >= 0 || req == 0 {
			continue
		}
		primary = i
		out["top_services"] = top
		af.GroupBy, af.Limit = "operation", tsrTopOps
		if ops, oerr := r.agg(ctx, af); oerr == nil {
			out["top_operations"] = traceStatsRows(ops)
		} else {
			unread = append(unread, "top_operations: "+oerr.Error())
		}
		tf := chstore.TraceFilter{Service: svc, Search: op, From: from, To: to, HasError: a.ErrorsOnly, RootOnly: a.RootOnly,
			Sort: "time", Order: "desc", Limit: tsrSamples, CountMode: "skip", Filters: sc.Filters, Env: sc.Env, Cluster: sc.Cluster,
			MinMs: a.MinMs, MaxMs: a.MaxMs}
		if trs, _, _, terr := r.traces(ctx, tf); terr == nil {
			ids := make([]string, 0, len(trs))
			for _, t := range trs {
				ids = append(ids, t.TraceID)
			}
			out["sample_trace_ids"] = ids
		} else {
			unread = append(unread, "sample_trace_ids: "+terr.Error())
		}
		out["deep_link"] = TracesSearchLink(tf, from, to)
		out["matched"] = m.Kind
	}
	if len(matches) > 0 {
		out["interpretations"] = runnable
	} else if primary >= 0 {
		out["totals"] = map[string]uint64{"requests": runnable[0].Requests, "errors": runnable[0].Errors}
	}
	if primary < 0 {
		out["notes"] = []string{"no trace matched any interpretation in the window — say so; do not invent counts"}
	}
	if len(unread) > 0 {
		out["unread"] = unread
	}
	return out, nil
}

func tracesSearchTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             TracesSearchName,
		ShortDescription: "Çıplak değer (ör. cluster/env adı) ya da süzgeçle trace: değer çözümü (cluster→env→namespace→url), istek/hata/p99, servis+operasyon, örnek id, Traces linki.",
		Description: "Answer 'is there any trace / call in X', 'what runs in cluster X', 'traces containing X' in ONE call, the way the Traces page would. " +
			"value = a bare token the user gave (e.g. a cluster or environment name): it is resolved against cheap known dimensions in order " +
			"cluster (span cluster = openshift/k8s cluster name, Remote Clusters) → env (deploy_env exact, then a -/_/. segment, e.g. prod-X) → namespace; " +
			"if none matches the interpretation is `url` and you should call endpoint_lookup(url=value). Or pass cluster / env / namespace / service / operation / filters directly. " +
			"Returns interpretations[] (kind, value, match, requests, errors — every matched reading, so say which one(s) matched), " +
			"top_services (≤5: traces, errors, p99_ms from the Traces aggregate), top_operations (≤5), sample_trace_ids (≤5), " +
			"deep_link (opens /traces with exactly these filters — the UI shows it as a chip), unread (reads that failed — not 'none'). " +
			"Window default 3600 s, max 21600 s. Quote numbers exactly from the result.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value":       map[string]any{"type": "string", "description": "Bare token from the question (cluster / env / namespace name or URL fragment) to resolve."},
				"cluster":     map[string]any{"type": "string", "description": "Cluster name / Remote Cluster id when already known."},
				"env":         map[string]any{"type": "string", "description": "deploy_env value when already known."},
				"namespace":   map[string]any{"type": "string", "description": "Exact k8s namespace."},
				"service":     map[string]any{"type": "string", "description": "Exact service name."},
				"operation":   map[string]any{"type": "string", "description": "Span / operation name text (same as the Traces search box)."},
				"errors_only": map[string]any{"type": "boolean", "description": "Only traces with an error span."},
				"root_only":   map[string]any{"type": "boolean", "description": "Only traces whose root span was ingested."},
				"min_ms":      map[string]any{"type": "number", "description": "Only traces slower than this (ms)."},
				"max_ms":      map[string]any{"type": "number", "description": "Only traces faster than this (ms)."},
				"filters": map[string]any{
					"type":        "array",
					"description": "Attribute filters (resource.* allowed), AND-joined: {key, op, value|values}; op eq|ne|in|contains|prefix|regex|exists|not_exists. Keys from describe_attributes only.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key":    map[string]any{"type": "string"},
							"op":     map[string]any{"type": "string", "enum": []string{"eq", "ne", "in", "contains", "prefix", "regex", "exists", "not_exists"}},
							"value":  map[string]any{"type": "string"},
							"values": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
						"required": []string{"key"},
					},
				},
				"range_s": map[string]any{"type": "integer", "minimum": 300, "maximum": tsrMaxRangeS, "description": "Lookback seconds. Default 3600."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a tracesSearchArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			return runTracesSearch(ctx, d, tsrReaderFrom(d), a)
		},
	}
}

// TracesSearchLink — SAF: TracesDeepLink + env yoksa AÇIK boş `env=`. Traces
// sayfası env'i useUrlEnv ile okur: param yoksa kullanıcının yapışkan env
// seçimi uygulanır ve link başka bir kümeyi açardı; boş param "tüm ortamlar"
// demektir (resolveEnv). Sözleşme Go↔TS: testdata/cosre/traces-deeplink-v1.json.
func TracesSearchLink(f chstore.TraceFilter, from, to time.Time) string {
	link := TracesDeepLink(f, from, to)
	if f.Env == "" {
		link += "&env="
	}
	return link
}
