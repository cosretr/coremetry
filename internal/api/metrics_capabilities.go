package api

// metrics_capabilities.go — v0.10.1837 (Metrics Explorer Faz 1 dilim 1.3,
// audit §3.3 "Metrik backend'i ClickHouse iken").
//
//	GET /api/metrics/capabilities[?metricsrc=vm|ch]
//
// One small document the Explorer reads ONCE per page load to decide what to
// draw: which backend answers, which dialect the code mode speaks, which
// discovery features exist, and the hard limits. Without it the UI would have
// to infer the backend from a failing request (a 400 on `?instant=1`, an
// empty cardinality tab) — the "boş panel" the house rules forbid.
//
// Why its own file and not metrics_handlers.go: it is not a metric READ, it is
// the contract of the reads; later slices (top-N 1.4, cluster 1.5, discovery
// 1.6) each flip their own flag here, and one function owning the whole
// document is what keeps the flags honest (capabilitiesFor is pure + tested).
//
// No role gate: the document carries no data, and a viewer must see what the
// page can do (CLAUDE.md "viewer SEES state"). serveCached 30 s; the key
// carries the source and the VM base URL digest (a different VM is a
// different answer) and the metadata probe's outcome rides inside the body.

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promapi"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func init() { registerRoutesExtra("metrics-capabilities", (*Server).registerMetricsCapabilitiesRoutes) }

func (s *Server) registerMetricsCapabilitiesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/metrics/capabilities", s.getMetricsCapabilities)
}

// capFeature — one capability flag; Reason is filled when unsupported so the
// UI can print WHY (audit §3.3: "Boş durum: 'Yalnız VictoriaMetrics'").
type capFeature struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type capMetadata struct {
	// Ladder — the rungs this backend can use, in order.
	Ladder []string `json:"ladder"`
	// VMMetadata — "available" | "unavailable" on VM; omitted on ClickHouse.
	VMMetadata string `json:"vmMetadata,omitempty"`
}

type capLimits struct {
	MaxQueryLength   int `json:"maxQueryLength"`
	MaxDataPoints    int `json:"maxDataPoints"`
	MaxSeries        int `json:"maxSeries,omitempty"` // VM: decoded-series backstop
	MaxRows          int `json:"maxRows,omitempty"`   // CH: row cap (bucket×series)
	NamesPageMax     int `json:"namesPageMax"`
	LabelValuesMax   int `json:"labelValuesMax"`
	AttrKeysMax      int `json:"attrKeysMax"`
	LabelWindowMaxHr int `json:"labelWindowMaxHours"`
}

type metricsCapabilities struct {
	Source       string                `json:"source"`   // vm | clickhouse
	Backend      string                `json:"backend"`  // victoriametrics | clickhouse
	Language     string                `json:"language"` // metricsql | promql-subset
	MetricsQL    bool                  `json:"metricsql"`
	InstantQuery bool                  `json:"instantQuery"`
	Metadata     capMetadata           `json:"metadata"`
	Features     map[string]capFeature `json:"features"`
	Limits       capLimits             `json:"limits"`
	// TopN — v0.10.1838.
	TopN capTopN `json:"topN"`
	// ClusterScope — v0.10.1839. Label is filled per request by the handler
	// (it comes from Settings, not from the pure document).
	ClusterScope capClusterScope `json:"clusterScope"`
}

// capClusterScope — which reads the ?cluster= param narrows.
type capClusterScope struct {
	Label     string `json:"label,omitempty"`
	Query     bool   `json:"query"`
	Selectors bool   `json:"selectors"`
	PromQL    bool   `json:"promql"`
	Reason    string `json:"reason,omitempty"`
}

// capTopN — the allowed rungs and ranking modes. Mode: "topk" (VictoriaMetrics
// MetricsQL topk_* pushdown) | "post-rank" (ClickHouse: ranked in Go).
type capTopN struct {
	Limits  []int    `json:"limits"`
	Default int      `json:"default"`
	Max     int      `json:"max"`
	Ranks   []string `json:"ranks"`
	Mode    string   `json:"mode"`
}

const capReasonVMOnly = "Yalnız VictoriaMetrics"

// capabilitiesFor — SAF (metrics_capabilities_test.go): the document for one
// source. vmMetadataOK is the cached metadata probe (VM only).
func capabilitiesFor(src string, vmMetadataOK bool) metricsCapabilities {
	c := metricsCapabilities{
		Source: metaSourceName(src),
		Limits: capLimits{
			MaxQueryLength:   maxPromQLQueryLen,
			MaxDataPoints:    4000,
			NamesPageMax:     1000,
			LabelValuesMax:   1000,
			AttrKeysMax:      100,
			LabelWindowMaxHr: 7 * 24,
		},
		Features: map[string]capFeature{},
	}
	vm := src == metricSourceVM
	if vm {
		c.Backend, c.Language, c.MetricsQL, c.InstantQuery = "victoriametrics", "metricsql", true, true
		c.Limits.MaxSeries = promapi.MaxSeriesParsed
		c.Metadata = capMetadata{Ladder: []string{typeSourceVMMetadata, typeSourceInferred}, VMMetadata: "unavailable"}
		if vmMetadataOK {
			c.Metadata.VMMetadata = "available"
		}
	} else {
		c.Backend, c.Language = "clickhouse", "promql-subset"
		c.Limits.MaxRows = chstore.SpanMetricRowCap
		c.Metadata = capMetadata{Ladder: []string{typeSourceCatalog}}
	}
	vmOnly := func(on bool) capFeature {
		if on {
			return capFeature{Supported: true}
		}
		return capFeature{Reason: capReasonVMOnly}
	}
	// Discovery + server-side ranking land in later Faz 1 slices; each flips
	// its own entry here (1.4 topN, 1.5 clusterScope, 1.6 discovery*).
	notYet := capFeature{Reason: "Faz 1 sonraki dilim"}
	// v0.10.1838 — both backends rank server-side; VM pushes topk_* down,
	// ClickHouse ranks its bounded result in Go (metric_topn.go).
	c.Features["topN"] = capFeature{Supported: true}
	c.TopN = capTopN{Limits: append([]int(nil), topNRungs...), Default: topNDefault, Max: topNMax,
		Ranks: []string{vmetrics.RankAvg, vmetrics.RankMax, vmetrics.RankLast}, Mode: "post-rank"}
	if vm {
		c.TopN.Mode = "topk"
	}
	// v0.10.1839 — VM scopes everything (extra_filters[]); CH only the
	// builder reads (query / histogram / bundle), via the cluster coalesce.
	c.Features["clusterScope"] = capFeature{Supported: true}
	c.ClusterScope = capClusterScope{Query: true, Selectors: vm, PromQL: vm}
	if !vm {
		c.ClusterScope.Reason = "ClickHouse: yalnız builder sorgusu kapsanır; ad/etiket seçicileri ve PromQL kapsanmaz"
	}
	c.Features["discoverLabels"] = notYet
	c.Features["discoverLabelValues"] = notYet
	c.Features["cardinality"] = notYet
	c.Features["seriesSample"] = notYet
	c.Features["topQueries"] = notYet
	c.Features["queryTrace"] = capFeature{Reason: "Faz 2 (kod kipi)"}
	c.Features["instantQuery"] = vmOnly(vm)
	c.Features["hiddenByRule"] = capFeature{Supported: true}
	return c
}

// metricsCapabilitiesKey — SAF: source + VM base URL digest.
func metricsCapabilitiesKey(src, baseURL string) string {
	h := fnv.New64a()
	for _, p := range []string{src, baseURL} {
		h.Write([]byte(strconv.Itoa(len(p))))
		h.Write([]byte{':'})
		h.Write([]byte(p))
	}
	return fmt.Sprintf("metric-caps:v1:src=%s:%x", src, h.Sum64())
}

func (s *Server) getMetricsCapabilities(w http.ResponseWriter, r *http.Request) {
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	base := ""
	if src.Name() == metricSourceVM && s.vmetrics != nil {
		base = s.vmetrics.CurrentSettings().BaseURL
	}
	s.serveCached(w, r, metricsCapabilitiesKey(src.Name(), base), 30*time.Second, func(ctx context.Context) (any, error) {
		ok := false
		if src.Name() == metricSourceVM {
			_, ok = s.vmMetadata(ctx)
		}
		c := capabilitiesFor(src.Name(), ok)
		if src.Name() == metricSourceVM {
			c.ClusterScope.Label = s.vmetrics.ClusterLabel()
		}
		return c, nil
	})
}
