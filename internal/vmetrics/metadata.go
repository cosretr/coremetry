package vmetrics

// metadata.go — v0.10.1837 (Metrics Explorer Faz 1 dilim 1.3, audit §3.1
// satır 1 "metadata merdiveni", §3.2 `/api/v1/metadata`: düşük maliyet,
// `limit`, 5 dk TTL, boş dönerse sessizce atlanır).
//
// VictoriaMetrics answers /api/v1/metadata (Prometheus shape) only when the
// install stores metric metadata — older VM versions and installs without the
// metadata-ingestion flag return an EMPTY map. ListMetricNames' header still
// says "VM does not populate it"; that was true when written and is now
// "depends on the VM version" — NOT VERIFIED against prod (Faz 1 report, §9
// komut 1). So the ladder treats metadata as optional: an empty or failed
// answer means "unavailable", never an error on the names surface.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/cilcenk/coremetry/internal/promapi"
)

// MetadataLimit — max metric families asked from /api/v1/metadata. Prod VM
// carries ~600 names (memory: 582); 5000 is a generous bound that keeps the
// body far under promapi.MaxBodyBytes.
const MetadataLimit = 5000

// MetricMetadata — one family's metadata (Prometheus shape).
type MetricMetadata struct {
	Type string `json:"type"`
	Help string `json:"help"`
	Unit string `json:"unit"`
}

// decodeMetadata — SAF: the `data` member of /api/v1/metadata
// (`{"<family>":[{type,help,unit},…]}`) → first entry per family.
func decodeMetadata(data json.RawMessage) (map[string]MetricMetadata, error) {
	if len(data) == 0 || string(data) == "null" {
		return map[string]MetricMetadata{}, nil
	}
	var raw map[string][]MetricMetadata
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("victoriametrics metadata decode: %w", err)
	}
	out := make(map[string]MetricMetadata, len(raw))
	for name, entries := range raw {
		if len(entries) > 0 {
			out[name] = entries[0]
		}
	}
	return out, nil
}

// FetchMetricMetadata — GET /api/v1/metadata?limit=N&limit_per_metric=1.
// Bounded by MetadataLimit; the caller caches (api: 5 min).
func (s *Service) FetchMetricMetadata(ctx context.Context) (map[string]MetricMetadata, error) {
	cfg, err := s.ready()
	if err != nil {
		return nil, err
	}
	params := url.Values{
		"limit":            {strconv.Itoa(MetadataLimit)},
		"limit_per_metric": {"1"},
	}
	data, _, err := promapi.QueryData(ctx, s.request("/api/v1/metadata", params, cfg))
	if err != nil {
		return nil, err
	}
	return decodeMetadata(data)
}

// MetadataFamily — SAF: the family name metadata is keyed by. Prometheus
// keys a histogram/summary by its FAMILY (`…_seconds`), a counter by its
// name without `_total` in newer exporters and with it in older ones — so
// callers try the name as-is first, then this.
func MetadataFamily(name string) string {
	for _, suf := range []string{bucketSuffix, histogramCountSuffix, histogramSumSuffix, counterSuffix} {
		if strings.HasSuffix(name, suf) {
			return strings.TrimSuffix(name, suf)
		}
	}
	return name
}

// PromTypeToCatalog — Prometheus metadata type → the catalogue's OTLP-style
// vocabulary (chstore MetricInfo.Type: gauge | sum | histogram | summary).
// Unknown / "unknown" → "".
func PromTypeToCatalog(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "counter":
		return "sum"
	case "gauge":
		return "gauge"
	case "histogram", "gaugehistogram":
		return "histogram"
	case "summary":
		return "summary"
	}
	return ""
}

// DescribeMetricName — exported name-suffix inference (describe.go), the
// last rung of the ladder.
func DescribeMetricName(name string) (unit, typ string) { return describeMetricName(name) }
