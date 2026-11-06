package api

// metric_cluster_scope.go — v0.10.1839 (Metrics Explorer Faz 1 dilim 1.5,
// audit §3.1 satır 14). Behaviour change approved (audit §11, S6).
//
//	?cluster=<name>     on names · labels · attr-keys · query · promql ·
//	                    histogram · dashboards bundle · discover/*
//	absent / "all"      = no scope (today's behaviour, byte-identical keys)
//
// VictoriaMetrics: the scope travels as `extra_filters[]={<label>="<name>"}`
// (vmetrics/scope.go) — the query TEXT is untouched, selectors (names, label
// values, attribute keys) are scoped too. ClickHouse: the builder reads
// (query, histogram, bundle metric slot) get a `cluster = <name>` filter
// conjunct, which metricPointsWellKnown resolves to the six-permutation
// metricClusterExpr coalesce — the same derivation the spans side uses. CH
// cannot scope the discovery selectors (ListMetricNames / MetricLabelValues /
// MetricAttrKeys take no filter) nor an operator-written PromQL query; there
// the param is ignored, the cache key does NOT fork, and capabilities says
// {clusterScope: {selectors:false, promql:false}} so the UI can label the
// chip "yalnız sorgu".
//
// The cache key carries the scope (label + value) whenever it is APPLIED —
// v0.5.187: one operator's acme-prod-1 chart must never be served for
// another's acme-prod-2 request.

import (
	"context"
	"net/http"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// clusterScope — the request's cluster scope resolved against its source.
type clusterScope struct {
	Value string // "" = no scope
	Label string // VM label the selector names ("" on CH)
	vm    bool
}

// parseClusterValue — SAF: "" and "all" (any case) mean no scope.
func parseClusterValue(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" || strings.EqualFold(v, "all") {
		return ""
	}
	return v
}

func (s *Server) clusterScopeFor(r *http.Request, src metricSource) clusterScope {
	v := parseClusterValue(r.URL.Query().Get("cluster"))
	if v == "" {
		return clusterScope{}
	}
	if src.Name() == metricSourceVM {
		return clusterScope{Value: v, Label: s.vmetrics.ClusterLabel(), vm: true}
	}
	return clusterScope{Value: v}
}

// keyTag — appended when the scope applies to THIS read. selector=true for
// the discovery selectors (names / labels / attr-keys) and operator PromQL,
// which only VM can scope.
func (c clusterScope) keyTag(selector bool) string {
	if !c.applies(selector) {
		return ""
	}
	if c.vm {
		return ":cl=" + c.Label + "=" + c.Value
	}
	return ":cl=" + c.Value
}

func (c clusterScope) applies(selector bool) bool {
	return c.Value != "" && (c.vm || !selector)
}

// withCtx — VM: attach the extra_filters[] selector. CH: unchanged.
func (c clusterScope) withCtx(ctx context.Context) context.Context {
	if c.Value == "" || !c.vm {
		return ctx
	}
	return vmetrics.WithExtraFilters(ctx, []string{vmetrics.ClusterSelector(c.Label, c.Value)})
}

// chFilters — CH builder reads: the cluster conjunct appended (copy).
func (c clusterScope) chFilters(in []chstore.FilterExpr) []chstore.FilterExpr {
	if c.Value == "" || c.vm {
		return in
	}
	return append(append([]chstore.FilterExpr(nil), in...), chstore.FilterExpr{Key: "cluster", Op: "=", Values: []string{c.Value}})
}

// metaCluster — the scope as meta reports it ("" when not applied).
func (c clusterScope) metaCluster(selector bool) string {
	if !c.applies(selector) {
		return ""
	}
	return c.Value
}
