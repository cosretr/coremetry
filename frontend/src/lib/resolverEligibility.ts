// lib/resolverEligibility.ts — GRAN-D (v0.8.249): the spanmetrics-resolver
// eligibility constants, extracted from pages/explore/model.ts so non-Explore
// surfaces can share them without importing one page's builder model from
// another page/component — the layering concern recorded with the 2026-06-15
// doorway-resolver decision. model.ts re-exports these verbatim; Explore
// behaviour is unchanged.
//
// v0.10.2154 — the one non-Explore surface (ServiceCharts' RED family, the
// Service › Details "Performance" section) was removed by the operator; its
// isResolverEligible gate + serviceRedDescriptors builder went with it. The
// constants stay: Explore's exemplarDescriptor reads them.
//
// The resolver (/api/metrics/resolve, chstore/metricresolve.go) serves a
// MetricQuery off the spanmetrics 1s/10s/1m rollup tiers — far finer than the
// 5m summary MVs — but only for shapes the tiers can express: equality-only
// filters and group-bys on the five rollup dimensions, a rollup-served agg,
// and duration as the only measured field. Anything else must ride the raw
// spanMetric path.

// Mirror of chstore tierDimColumn's accepted keys (both OTel and column
// spellings). A filter or split on anything else can't ride the tiers.
export const TIER_DIM_KEYS = new Set([
  'service.name', 'service_name',
  'name', 'operation',
  'kind', 'span.kind',
  'status', 'status_code',
  'http.route', 'http_route',
]);

// Aggs spanmetricStateAgg can serve (no p999/min/max/last on the rollups).
// Historically named for the exemplar (◆ glyph) path — eligibility for
// exemplars and for tier-served series is the same server-side gate.
export const EXEMPLAR_AGGS = new Set([
  'count', 'rate', 'per_min', 'errors', 'error_rate', 'apdex', 'avg', 'sum', 'p50', 'p90', 'p95', 'p99',
  'band', // v0.8.411 - percentile band; resolver-only (no legacy twin)
]);
