package api

// metric_names_meta.go — v0.10.1837 (Metrics Explorer Faz 1 dilim 1.3, audit
// §3.1 satır 1 + 15, §1 risk 1 "metadata").
//
// The METADATA LADDER for a metric name's type / unit / help, and the
// provenance of each answer (`typeSource`), so the UI can say "tahmini"
// instead of presenting a suffix guess as fact (audit risk 1: "sayaç → rate"
// on a wrong guess picks the wrong function):
//
//	ClickHouse:      catalog      — metric_catalog rows (OTLP description /
//	                                unit / type as ingested). typeSource is
//	                                omitted when the row carries none of the
//	                                three (raw-scan fallback rows).
//	VictoriaMetrics: vm-metadata  — /api/v1/metadata (Prometheus HELP/TYPE/
//	                                UNIT), when the VM install stores it;
//	                 inferred     — name-suffix inference (describe.go),
//	                                the last rung.
//
// DEVIATION from the brief's ladder (product catalog → VM metadata → suffix):
// there is no product catalogue of Prometheus-named metrics in the repo, and
// reading ClickHouse's metric_catalog for a VictoriaMetrics answer would be a
// cross-store read the metric seam refuses on principle (metricsource.go
// header: no silent mixing of stores). So on VM the ladder starts at
// vm-metadata. Recorded in DECISIONS (2026-11-06, v0.10.1837).
//
// hiddenByRule (v0.10.1809 pipeline name-drop): names hidden by a drop rule
// are still NEVER returned to non-admins. An admin who asks with an explicit
// `includeHidden=1` gets them back flagged `hiddenByRule: true`, so the
// Discovery tab can show "listeden gizli, sorgu çalışır" (audit §3.1 satır
// 15). The flag forks the cache key only for that admin request (`:ih=1`).

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// typeSource wire values.
const (
	typeSourceCatalog    = "catalog"
	typeSourceVMMetadata = "vm-metadata"
	typeSourceInferred   = "inferred"
)

// metricNameRow — a catalogue row plus the ladder's provenance. The embedded
// MetricInfo keeps every legacy field flattened and in order.
type metricNameRow struct {
	chstore.MetricInfo
	Help         string `json:"help,omitempty"`
	TypeSource   string `json:"typeSource,omitempty"`
	HiddenByRule bool   `json:"hiddenByRule,omitempty"`
}

// metadataUnit — Prometheus metadata UNIT → the display unit the catalogue
// already speaks (describe.go displayUnitBySuffix). Unknown → "".
func metadataUnit(u string) string {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "seconds", "s":
		return "s"
	case "milliseconds", "ms":
		return "ms"
	case "bytes", "b":
		return "B"
	}
	return ""
}

// vmLadder — SAF: one VM name through the ladder. md may be nil/empty
// (metadata unavailable). Exact-name metadata wins; family metadata applies
// the PART's type, not the family's: a histogram's `_count` / `_sum` series
// are cumulative counters ("sum"), only `_bucket` is the histogram itself.
func vmLadder(row chstore.MetricInfo, md map[string]vmetrics.MetricMetadata) metricNameRow {
	out := metricNameRow{MetricInfo: row}
	m, exact := md[row.Name]
	if !exact {
		fam := vmetrics.MetadataFamily(row.Name)
		if fam != row.Name {
			if fm, ok := md[fam]; ok {
				m = fm
				switch famType := vmetrics.PromTypeToCatalog(fm.Type); {
				case strings.HasSuffix(row.Name, "_bucket") && famType == "histogram":
					m.Type = "histogram"
				case famType != "":
					m.Type = "counter" // _count / _sum / _total parts are monotonic
				}
				exact = true
			}
		}
	}
	if exact && (vmetrics.PromTypeToCatalog(m.Type) != "" || strings.TrimSpace(m.Help) != "") {
		out.Help = strings.TrimSpace(m.Help)
		if t := vmetrics.PromTypeToCatalog(m.Type); t != "" {
			out.Type = t
		}
		if u := metadataUnit(m.Unit); u != "" {
			out.Unit = u
		}
		out.TypeSource = typeSourceVMMetadata
		return out
	}
	if out.Type != "" || out.Unit != "" {
		out.TypeSource = typeSourceInferred
	}
	return out
}

// annotateMetricNames — SAF: rows → ladder rows. hidden marks names a drop
// rule hides (nil = none; only passed for an admin includeHidden request).
func annotateMetricNames(names []chstore.MetricInfo, src string, md map[string]vmetrics.MetricMetadata, hidden func(string) bool) []metricNameRow {
	out := make([]metricNameRow, 0, len(names))
	for _, n := range names {
		var row metricNameRow
		if src == metricSourceVM {
			row = vmLadder(n, md)
		} else {
			row = metricNameRow{MetricInfo: n, Help: n.Description}
			if n.Description != "" || n.Unit != "" || n.Type != "" {
				row.TypeSource = typeSourceCatalog
			}
		}
		if hidden != nil && hidden(n.Name) {
			row.HiddenByRule = true
		}
		out = append(out, row)
	}
	return out
}

// ── VM metadata, cached ─────────────────────────────────────────────────────

// vmMetadataTTL — audit §3.2: `/api/v1/metadata` 5 dk TTL.
const vmMetadataTTL = 5 * time.Minute

// vmMetadataBody — the cached probe. Available=false is CACHED too: an
// install without metadata answers empty on every call, and "probe once,
// cache the probe" is the whole point.
type vmMetadataBody struct {
	Available bool                               `json:"available"`
	Entries   map[string]vmetrics.MetricMetadata `json:"entries"`
}

// vmMetadataKey — SAF: hashes every input that changes the answer: the VM
// base URL (which VM) and the request limit (v0.5.187: all inputs, FNV).
func vmMetadataKey(baseURL string, limit int) string {
	h := fnv.New64a()
	for _, p := range []string{baseURL, strconv.Itoa(limit)} {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return fmt.Sprintf("vm-metadata:v1:%x", h.Sum64())
}

// vmMetadata — the cached metadata map; (nil, false) when unavailable or when
// VM is not configured. Never returns an error: metadata is a refinement.
func (s *Server) vmMetadata(ctx context.Context) (map[string]vmetrics.MetricMetadata, bool) {
	if s.vmetrics == nil || !s.vmetrics.Available() {
		return nil, false
	}
	key := vmMetadataKey(s.vmetrics.CurrentSettings().BaseURL, vmetrics.MetadataLimit)
	raw, _, err := s.cachedJSON(ctx, key, vmMetadataTTL, false, func(ctx context.Context) (any, error) {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		m, ferr := s.vmetrics.FetchMetricMetadata(cctx)
		if ferr != nil || len(m) == 0 {
			return vmMetadataBody{Available: false, Entries: map[string]vmetrics.MetricMetadata{}}, nil
		}
		return vmMetadataBody{Available: true, Entries: m}, nil
	})
	if err != nil {
		return nil, false
	}
	var b vmMetadataBody
	if json.Unmarshal(raw, &b) != nil || !b.Available {
		return nil, false
	}
	return b.Entries, true
}

// namesDropPlan — SAF: the drop rule either FILTERS the page (everyone) or,
// for an admin includeHidden request, only MARKS rows. ihTag forks the cache
// key only in the second case, and only when a rule exists (no rule → nothing
// to reveal → same body → same key).
func namesDropPlan(drop func(string) bool, includeHidden bool) (pageDrop, hiddenMark func(string) bool, ihTag string) {
	if includeHidden && drop != nil {
		return nil, drop, ":ih=1"
	}
	return drop, nil, ""
}

// ladderRows — rows through the metadata ladder; VM metadata from cache.
func (s *Server) ladderRows(ctx context.Context, src metricSource, names []chstore.MetricInfo, hidden func(string) bool) []metricNameRow {
	var md map[string]vmetrics.MetricMetadata
	if src.Name() == metricSourceVM {
		md, _ = s.vmMetadata(ctx)
	}
	return annotateMetricNames(names, src.Name(), md, hidden)
}

// isAdminRequest — the caller's role from the auth middleware's claims.
func isAdminRequest(r *http.Request) bool {
	c := auth.FromContext(r.Context())
	return c != nil && c.Role == auth.RoleAdmin
}
