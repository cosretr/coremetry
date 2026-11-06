package vmetrics

// scope.go — v0.10.1839 (Metrics Explorer Faz 1 dilim 1.5, audit §3.1 satır
// 14, §8 S6: cluster kapsamı kod kipinde de uygulanır). Behaviour change
// approved by the operator (audit §11).
//
// The Topbar cluster pick reaches VictoriaMetrics as `extra_filters[]` — a VM
// query arg that adds a series selector to every series the request touches
// WITHOUT rewriting the query text (docs.victoriametrics.com/victoriametrics/
// single-server-victoriametrics/#prometheus-querying-api-enhancements). That
// is the property that matters for the code mode: the operator's MetricsQL is
// sent byte-for-byte, the scope rides beside it, and the chip that says "kapsam:
// acme-prod-1" is telling the truth about BOTH.
//
// ONE label, never an OR across spellings. MetricsQL cannot OR label NAMES in
// one matcher (metricsource.go EnvFilterExpr), and the docs do not say whether
// multiple extra_filters[] combine with OR or AND — so sending
// `{k8s_cluster_name=…}` and `{openshift_cluster_name=…}` together could empty
// every panel. The label is the one the product already uses for multi-cluster
// metric scoping: LabelMap.Cluster (Settings → VictoriaMetrics etiket eşlemesi)
// when configured, else the first cluster role candidate (k8s_cluster_name, the
// OTel k8s.cluster.name convention — RoleCandidates). A series without that
// label is excluded by the scope; the capabilities document says so.
//
// Carried on the CONTEXT (like ResultRecorder) so the chstore-identical seam
// signatures stay untouched; every VM read builds its request through
// requestCtx, so a read added later cannot forget the scope by construction.

import (
	"context"
	"net/url"

	"github.com/cilcenk/coremetry/internal/promapi"
)

type extraFiltersKey struct{}

// WithExtraFilters attaches VM extra_filters[] selectors to ctx (nil/empty =
// no scope).
func WithExtraFilters(ctx context.Context, selectors []string) context.Context {
	if len(selectors) == 0 {
		return ctx
	}
	return context.WithValue(ctx, extraFiltersKey{}, append([]string(nil), selectors...))
}

func extraFiltersFrom(ctx context.Context) []string {
	v, _ := ctx.Value(extraFiltersKey{}).([]string)
	return v
}

// requestCtx — request() plus the context's extra_filters[]. The caller's
// params are COPIED, never mutated (they are reused across attempts).
func (s *Service) requestCtx(ctx context.Context, path string, params url.Values, cfg Settings) promapi.Request {
	ef := extraFiltersFrom(ctx)
	if len(ef) == 0 {
		return s.request(path, params, cfg)
	}
	p := url.Values{}
	for k, v := range params {
		p[k] = append([]string(nil), v...)
	}
	for _, sel := range ef {
		p.Add("extra_filters[]", sel)
	}
	return s.request(path, p, cfg)
}

// ClusterLabel — the VM label cluster scoping filters on (see header).
func (s *Service) ClusterLabel() string {
	if l := s.MetricLabelMap().Get(RoleCluster); l != "" {
		return l
	}
	if c := RoleCandidates(RoleCluster); len(c) > 0 {
		return c[0]
	}
	return "cluster"
}

// ClusterSelector — SAF: `{<label>="<value>"}` (value Prometheus-quoted).
func ClusterSelector(label, value string) string {
	return "{" + label + "=" + quotePromString(value) + "}"
}
