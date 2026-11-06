package api

// metrics_discovery.go — v0.10.1840 (Metrics Explorer Faz 1 dilim 1.6; audit
// §3.1 satır 2-4 + 13, §3.2 "VM API'leri: maliyet ve koruma", §8 S10).
//
//	GET /api/metrics/discover/labels        ?match=&q=&limit=&since=&cluster=
//	GET /api/metrics/discover/label-values  ?label=&match=&q=&limit=&since=&cluster=
//	GET /api/metrics/discover/cardinality   ?topN=&focusLabel=&match=&date=&cluster=
//	GET /api/metrics/discover/series-sample ?match=&limit=&since=&cluster=
//	GET /api/metrics/discover/top-queries   ?topN=&maxLifetime=        (admin / editor)
//
// CEILINGS (audit §3.2) — enforced by the pure clamp* functions below, BEFORE
// the cache key is built (a crafted ?limit=10⁶ must not mint its own key):
//
//	labels / label-values   limit ≤ 200 (default 100) · window ≤ 24 h · q < 2 chars ignored · TTL 60 s
//	cardinality (tsdb)      topN ≤ 50 (default 10) · one day (date=YYYY-MM-DD, ≤ today) · TTL 10 min · 15 s timeout
//	series-sample           match MUST name a metric (__name__) · limit ≤ 20 · window ≤ 1 h · TTL 60 s
//	top-queries             topN ≤ 20 (default 10) · maxLifetime ≤ 24 h (default 30m) · TTL 30 s
//
// Server-side search only (q, match) — no eager catalogue anywhere.
//
// ROLES (decision S10): everyone reads labels / values / cardinality / series
// (served from the shared cache, so a viewer's look costs VM nothing extra);
// top-queries is admin/editor — it exposes other operators' query text. The
// gate sits on the route line (api-route skill §5).
//
// BACKEND. ClickHouse answers every endpoint with 200
// {supported:false, reason:"Yalnız VictoriaMetrics"} — the UI draws an empty
// state with a reason, never a blank panel (audit §3.3). On VM, an endpoint
// the install does not serve (HTTP 404 / 405 / 501 — older VM, a proxy that
// strips /status) is remembered PER POD for 10 minutes (vmAPIProbe): the
// first failing call answers supported:false and every later call within the
// window answers it without touching VM. Other errors (5xx, timeouts) are NOT
// remembered — they are the 502 the operator needs to see.
//
// Every key hashes ALL inputs with length-prefixed FNV (v0.5.187).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/promapi"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() { registerRoutesExtra("metrics-discovery", (*Server).registerMetricsDiscoveryRoutes) }

func (s *Server) registerMetricsDiscoveryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/discover/labels", s.discoverLabels)
	mux.HandleFunc("GET /api/metrics/discover/label-values", s.discoverLabelValues)
	mux.HandleFunc("GET /api/metrics/discover/cardinality", s.discoverCardinality)
	mux.HandleFunc("GET /api/metrics/discover/series-sample", s.discoverSeriesSample)
	mux.Handle("GET /api/metrics/discover/top-queries",
		auth.RequireAnyRole(editorRoles, http.HandlerFunc(s.discoverTopQueries)))
}

// ── Ceilings (SAF, metrics_discovery_test.go) ───────────────────────────────

const (
	discLabelsLimitDefault = 100
	discLabelsLimitMax     = 200
	discLabelsWindowMax    = 24 * time.Hour
	discLabelsTTL          = 60 * time.Second
	discQMinLen            = 2

	discTSDBTopNDefault = 10
	discTSDBTopNMax     = 50
	discTSDBTTL         = 10 * time.Minute
	discTSDBTimeout     = 15 * time.Second

	discSeriesLimitMax  = 20
	discSeriesWindowMax = time.Hour
	discSeriesTTL       = 60 * time.Second

	discTopQueriesTopNDefault = 10
	discTopQueriesTopNMax     = 20
	discTopQueriesLifeDefault = 30 * time.Minute
	discTopQueriesLifeMax     = 24 * time.Hour
	discTopQueriesTTL         = 30 * time.Second

	discCallTimeout = 10 * time.Second
	discProbeTTL    = 10 * time.Minute
)

const discReasonCH = "Yalnız VictoriaMetrics"

// clampInt — SAF: v ≤ 0 or unparsable → def; above max → max.
func discClampInt(raw string, def, max int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// clampWindow — SAF: ?since= duration, default = max, capped at max.
func clampWindow(raw string, max time.Duration) time.Duration {
	d := parseDuration(raw, max)
	if d <= 0 || d > max {
		return max
	}
	return d
}

// discQuery — SAF: q shorter than discQMinLen is ignored (an every-keystroke
// regex over the whole index for "a" is the cost audit §3.2 rules out).
func discQuery(raw string) string {
	q := strings.TrimSpace(raw)
	if len([]rune(q)) < discQMinLen {
		return ""
	}
	return q
}

var promLabelNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// selectorNamesMetric — SAF: the series-sample guard. The selector must pin
// a metric: either a leading metric name (`http_requests_total{…}`) or an
// explicit `__name__` matcher. A bare `{job="x"}` is a fleet-wide series scan.
var leadingMetricRe = regexp.MustCompile(`^\s*[a-zA-Z_:][a-zA-Z0-9_:]*\s*(\{|$)`)

func selectorNamesMetric(sel string) bool {
	return leadingMetricRe.MatchString(sel) || strings.Contains(sel, "__name__")
}

// clampTSDBDate — SAF: "" stays "" (VM default: today, UTC); a valid
// YYYY-MM-DD not after today is kept; anything else is a 400.
func clampTSDBDate(raw string, now time.Time) (string, error) {
	d := strings.TrimSpace(raw)
	if d == "" {
		return "", nil
	}
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return "", fmt.Errorf("%w: date=%q — YYYY-MM-DD bekleniyor", errBadRequest, clampParamValue(d, 16))
	}
	if t.After(now.UTC()) {
		return "", fmt.Errorf("%w: date gelecekte olamaz", errBadRequest)
	}
	return d, nil
}

// clampLifetime — SAF: maxLifetime duration ≤ 24 h, default 30m; rendered
// in Go duration syntax (VM parses Go/Prometheus durations — "30m0s" is not
// guaranteed, so the minute form is used).
func clampLifetime(raw string) string {
	d := parseDuration(raw, discTopQueriesLifeDefault)
	if d <= 0 {
		d = discTopQueriesLifeDefault
	}
	if d > discTopQueriesLifeMax {
		d = discTopQueriesLifeMax
	}
	if d < time.Minute {
		return strconv.Itoa(int(d/time.Second)) + "s"
	}
	return strconv.Itoa(int(d/time.Minute)) + "m"
}

// discKey — length-prefixed FNV over every input.
func discKey(prefix string, parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%s:v1:%x", prefix, h.Sum64())
}

// ── VM API probe (per pod, 10 min) ──────────────────────────────────────────

var vmAPIProbe = struct {
	sync.Mutex
	until map[string]time.Time
}{until: map[string]time.Time{}}

func discProbeKey(base, api string) string { return base + "|" + api }

func vmAPIKnownMissing(base, api string, now time.Time) bool {
	vmAPIProbe.Lock()
	defer vmAPIProbe.Unlock()
	return now.Before(vmAPIProbe.until[discProbeKey(base, api)])
}

// vmAPIMissing — SAF-ish: is err the "this VM does not serve that path"
// class? Records it for discProbeTTL when so.
func vmAPIMissing(base, api string, err error, now time.Time) bool {
	var he *promapi.HTTPError
	if !errors.As(err, &he) {
		return false
	}
	switch he.Status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
	default:
		return false
	}
	vmAPIProbe.Lock()
	vmAPIProbe.until[discProbeKey(base, api)] = now.Add(discProbeTTL)
	vmAPIProbe.Unlock()
	return true
}

func unsupportedBody(reason string) map[string]any {
	return map[string]any{"supported": false, "reason": reason}
}

func vmMissingReason(api string) string {
	return "VictoriaMetrics bu API'yi sunmuyor (" + api + ") — sürüm ya da proxy yolu"
}

// discSource — the request's source; CH (or an error) short-circuits with
// the unsupported body. Returns ok=false when the response was written.
func (s *Server) discSource(w http.ResponseWriter, r *http.Request) (metricSource, string, bool) {
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return nil, "", false
	}
	if src.Name() != metricSourceVM {
		writeJSON(w, unsupportedBody(discReasonCH))
		return nil, "", false
	}
	return src, s.vmetrics.CurrentSettings().BaseURL, true
}

// runDisc — the shared VM call wrapper: probe short-circuit, bounded ctx,
// missing-API → cacheable supported:false, everything else upstream.
func (s *Server) runDisc(ctx context.Context, base, api string, timeout time.Duration, fn func(context.Context) (map[string]any, error)) (any, error) {
	now := time.Now()
	if vmAPIKnownMissing(base, api, now) {
		return unsupportedBody(vmMissingReason(api)), nil
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, err := fn(cctx)
	if err != nil {
		if vmAPIMissing(base, api, err, now) {
			return unsupportedBody(vmMissingReason(api)), nil
		}
		return nil, upstream(err)
	}
	out["supported"] = true
	return out, nil
}

// ── Handlers ────────────────────────────────────────────────────────────────

func (s *Server) discoverLabels(w http.ResponseWriter, r *http.Request) {
	src, base, ok := s.discSource(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	match := strings.TrimSpace(q.Get("match"))
	if len(match) > maxPromQLQueryLen {
		writeJSONError(w, http.StatusBadRequest, "match too long")
		return
	}
	needle := discQuery(q.Get("q"))
	limit := discClampInt(q.Get("limit"), discLabelsLimitDefault, discLabelsLimitMax)
	since := clampWindow(q.Get("since"), discLabelsWindowMax)
	cs := s.clusterScopeFor(r, src)
	key := discKey("metric-disc-labels", base, match, needle, strconv.Itoa(limit), since.String(), cs.keyTag(true))
	s.serveCached(w, r, key, discLabelsTTL, func(ctx context.Context) (any, error) {
		return s.runDisc(ctx, base, "/api/v1/labels", discCallTimeout, func(ctx context.Context) (map[string]any, error) {
			now := time.Now()
			// Ask for one page beyond limit AFTER the substring filter so
			// hasMore is honest; VM's own limit bounds the raw list.
			names, partial, err := s.vmetrics.LabelNames(cs.withCtx(ctx), match, now.Add(-since), now, discLabelsLimitMax*5)
			if err != nil {
				return nil, err
			}
			filtered := filterLabelNames(names, needle)
			hasMore := len(filtered) > limit
			if hasMore {
				filtered = filtered[:limit]
			}
			// Value counts from the SHARED, 10-min cached tsdb stats (one
			// index scan for the whole fleet, reused by the cardinality tab);
			// labels outside its top list carry no count — unknown, not 0.
			counts, haveCounts := s.labelValueCounts(ctx, base, cs)
			rows := make([]map[string]any, 0, len(filtered))
			for _, n := range filtered {
				row := map[string]any{"name": n}
				if c, ok := counts[n]; ok {
					row["valueCount"] = c
				}
				rows = append(rows, row)
			}
			return map[string]any{"labels": rows, "hasMore": hasMore, "partial": partial, "limit": limit,
				"valueCounts": haveCounts, "windowSec": int(since / time.Second), "cluster": cs.metaCluster(true)}, nil
		})
	})
}

// filterLabelNames — SAF: drop __name__, case-insensitive substring.
func filterLabelNames(names []string, needle string) []string {
	n := strings.ToLower(needle)
	out := make([]string, 0, len(names))
	for _, l := range names {
		if l == "" || l == "__name__" {
			continue
		}
		if n != "" && !strings.Contains(strings.ToLower(l), n) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// labelValueCounts — label → distinct value count, OPPORTUNISTIC: read from
// the cardinality endpoint's cached tsdb body (default topN max, today,
// unfocused, same scope) when one exists, never computed here. A cold
// /api/v1/status/tsdb is a daily index scan (audit §3.2) — the labels list
// must not pay seconds of it to decorate rows; once anyone opened the
// cardinality tab the counts appear for everyone. Missing → no counts.
func (s *Server) labelValueCounts(ctx context.Context, base string, cs clusterScope) (map[string]uint64, bool) {
	raw, ok := s.peekCachedJSON(ctx, cardinalityKey(base, vmetrics.TSDBRequest{TopN: discTSDBTopNMax, ExtraLabel: tsdbExtraLabel(cs)}), discTSDBTTL)
	if !ok {
		return nil, false
	}
	var b struct {
		Supported bool                `json:"supported"`
		Stats     vmetrics.TSDBStatus `json:"stats"`
	}
	if json.Unmarshal(raw, &b) != nil || !b.Supported {
		return nil, false
	}
	out := make(map[string]uint64, len(b.Stats.LabelValueCountByLabelName))
	for _, e := range b.Stats.LabelValueCountByLabelName {
		out[e.Name] = e.Value
	}
	return out, true
}

// tsdbExtraLabel — the cluster scope in tsdb's documented form.
func tsdbExtraLabel(cs clusterScope) string {
	if !cs.applies(true) {
		return ""
	}
	return cs.Label + "=" + cs.Value
}

// cardinalityKey — SAF: every tsdb input + the VM base URL.
func cardinalityKey(base string, req vmetrics.TSDBRequest) string {
	return discKey("metric-disc-tsdb", base, strconv.Itoa(req.TopN), req.Date, req.FocusLabel, req.Match, req.ExtraLabel)
}

// peekCachedJSON — a cached body for key from L1 or L2 (fresh or still in the
// stale-while-revalidate window); NEVER computes.
func (s *Server) peekCachedJSON(ctx context.Context, key string, ttl time.Duration) ([]byte, bool) {
	if s.l1 != nil {
		if d, ok := s.l1.get(key); ok {
			return d, true
		}
	}
	if s.cache == nil {
		return nil, false
	}
	raw, ok, err := s.cache.Get(ctx, key)
	if err != nil || !ok {
		return nil, false
	}
	if written, body, envOK := unwrapEnvelope(raw); envOK && time.Since(written) < ttl*staleFactor {
		return body, true
	}
	return nil, false
}

func (s *Server) discoverLabelValues(w http.ResponseWriter, r *http.Request) {
	src, base, ok := s.discSource(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	label := strings.TrimSpace(q.Get("label"))
	if !promLabelNameRe.MatchString(label) {
		writeJSONError(w, http.StatusBadRequest, "label parametresi geçerli bir etiket adı olmalı")
		return
	}
	match := strings.TrimSpace(q.Get("match"))
	if len(match) > maxPromQLQueryLen {
		writeJSONError(w, http.StatusBadRequest, "match too long")
		return
	}
	needle := discQuery(q.Get("q"))
	limit := discClampInt(q.Get("limit"), discLabelsLimitDefault, discLabelsLimitMax)
	since := clampWindow(q.Get("since"), discLabelsWindowMax)
	cs := s.clusterScopeFor(r, src)
	key := discKey("metric-disc-lvals", base, label, match, needle, strconv.Itoa(limit), since.String(), cs.keyTag(true))
	s.serveCached(w, r, key, discLabelsTTL, func(ctx context.Context) (any, error) {
		return s.runDisc(ctx, base, "/api/v1/label/values", discCallTimeout, func(ctx context.Context) (map[string]any, error) {
			now := time.Now()
			sel := match
			if needle != "" {
				// server-side substring: the needle becomes a regex matcher
				// on the label itself, so VM's limit applies AFTER it.
				m := label + "=~`(?i).*" + regexp.QuoteMeta(strings.ReplaceAll(needle, "`", "")) + ".*`"
				if sel == "" {
					sel = "{" + m + "}"
				} else if i := strings.LastIndex(sel, "}"); i > 0 {
					inner := strings.TrimSpace(sel[strings.Index(sel, "{")+1 : i])
					if inner == "" {
						sel = sel[:i] + m + "}"
					} else {
						sel = sel[:i] + ", " + m + "}"
					}
				} else {
					sel = sel + "{" + m + "}"
				}
			}
			vals, partial, err := s.vmetrics.LabelValues(cs.withCtx(ctx), label, sel, now.Add(-since), now, limit+1)
			if err != nil {
				return nil, err
			}
			hasMore := len(vals) > limit
			if hasMore {
				vals = vals[:limit]
			}
			if vals == nil {
				vals = []string{}
			}
			return map[string]any{"label": label, "values": vals, "hasMore": hasMore, "partial": partial, "limit": limit,
				"windowSec": int(since / time.Second), "cluster": cs.metaCluster(true)}, nil
		})
	})
}

func (s *Server) discoverCardinality(w http.ResponseWriter, r *http.Request) {
	src, base, ok := s.discSource(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	date, derr := clampTSDBDate(q.Get("date"), time.Now())
	if derr != nil {
		writeErr(w, derr)
		return
	}
	focus := strings.TrimSpace(q.Get("focusLabel"))
	if focus != "" && !promLabelNameRe.MatchString(focus) {
		writeJSONError(w, http.StatusBadRequest, "focusLabel geçerli bir etiket adı olmalı")
		return
	}
	match := strings.TrimSpace(q.Get("match"))
	if len(match) > maxPromQLQueryLen {
		writeJSONError(w, http.StatusBadRequest, "match too long")
		return
	}
	cs := s.clusterScopeFor(r, src)
	req := vmetrics.TSDBRequest{
		TopN:       discClampInt(q.Get("topN"), discTSDBTopNDefault, discTSDBTopNMax),
		Date:       date,
		FocusLabel: focus,
		Match:      match,
		ExtraLabel: tsdbExtraLabel(cs),
	}
	s.serveCached(w, r, cardinalityKey(base, req), discTSDBTTL, func(ctx context.Context) (any, error) {
		return s.runDisc(ctx, base, "/api/v1/status/tsdb", discTSDBTimeout, func(ctx context.Context) (map[string]any, error) {
			st, err := s.vmetrics.TSDBStats(ctx, req)
			if err != nil {
				return nil, err
			}
			return map[string]any{"stats": st, "topN": req.TopN, "date": req.Date, "focusLabel": req.FocusLabel,
				"cluster": cs.metaCluster(true)}, nil
		})
	})
}

func (s *Server) discoverSeriesSample(w http.ResponseWriter, r *http.Request) {
	src, base, ok := s.discSource(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	match := strings.TrimSpace(q.Get("match"))
	if match == "" || !selectorNamesMetric(match) {
		writeJSONError(w, http.StatusBadRequest, "match bir metrik adı içermeli (ör. http_server_request_duration_seconds_count{…} ya da {__name__=\"…\"})")
		return
	}
	if len(match) > maxPromQLQueryLen {
		writeJSONError(w, http.StatusBadRequest, "match too long")
		return
	}
	limit := discClampInt(q.Get("limit"), discSeriesLimitMax, discSeriesLimitMax)
	since := clampWindow(q.Get("since"), discSeriesWindowMax)
	cs := s.clusterScopeFor(r, src)
	key := discKey("metric-disc-series", base, match, strconv.Itoa(limit), since.String(), cs.keyTag(true))
	s.serveCached(w, r, key, discSeriesTTL, func(ctx context.Context) (any, error) {
		return s.runDisc(ctx, base, "/api/v1/series", discCallTimeout, func(ctx context.Context) (map[string]any, error) {
			now := time.Now()
			series, partial, err := s.vmetrics.SeriesSample(cs.withCtx(ctx), match, now.Add(-since), now, limit)
			if err != nil {
				return nil, err
			}
			if series == nil {
				series = []map[string]string{}
			}
			return map[string]any{"series": series, "limit": limit, "sampleFull": len(series) >= limit, "partial": partial,
				"windowSec": int(since / time.Second), "cluster": cs.metaCluster(true)}, nil
		})
	})
}

func (s *Server) discoverTopQueries(w http.ResponseWriter, r *http.Request) {
	_, base, ok := s.discSource(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	topN := discClampInt(q.Get("topN"), discTopQueriesTopNDefault, discTopQueriesTopNMax)
	life := clampLifetime(q.Get("maxLifetime"))
	key := discKey("metric-disc-topq", base, strconv.Itoa(topN), life)
	s.serveCached(w, r, key, discTopQueriesTTL, func(ctx context.Context) (any, error) {
		return s.runDisc(ctx, base, "/api/v1/status/top_queries", discCallTimeout, func(ctx context.Context) (map[string]any, error) {
			tq, err := s.vmetrics.FetchTopQueries(ctx, topN, life)
			if err != nil {
				return nil, err
			}
			return map[string]any{"queries": tq, "topN": topN, "maxLifetime": life}, nil
		})
	})
}
