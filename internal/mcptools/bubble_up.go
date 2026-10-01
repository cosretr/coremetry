package mcptools

// bubble_up.go — v0.10.993 — dış skill denetimi 2026-09-19 V1, dilim 3:
// BubbleUp (Honeycomb'un "bu span'lerde ne özel" analizi) MCP aracı olarak.
// Motor chstore/bubbleup.go; kıyas chstore.ServiceBubbleUp'ta — /rootcause
// paneli, verdict kataloğu ve CoSRE kök-neden demetinin adımıyla (v0.10.992)
// ORTAK, yeni SQL yok.
//
// NEDEN yalnız DIŞ MCP istemcilerine (externalOnlyTools, tools.go): denetimin
// kararı "küçük LLM için prefetch adımı, tool değil". Uygulama içi sohbette
// BubbleUp kök-neden demetinde hazır gelir; aracı sohbet kataloğuna da koymak
// (a) her tur ödenen kompakt kataloğu büyütürdü — 9.098 / 9.100 B, yer yok —
// ve (b) küçük modele ikinci, pahalı bir yol açardı.
//
// Maliyet DÜRÜSTÇE ilan edilir (cost_claim_test.go ruhu): ham spans taraması.
// Pencere [300, 3600] sn'ye kelepçeli, varsayılan 600; çağrı 15 sn tavanlı
// (MCP'nin 20 sn çağrı bütçesinin altında) — dolarsa hata "range_s'i daralt".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/rca"
)

const (
	bubbleUpDefaultRangeS = 600
	bubbleUpMinRangeS     = 300
	bubbleUpMaxRangeS     = 3600
	bubbleUpTimeout       = 15 * time.Second
	// bubbleUpMinDiff — listeye giren en küçük pay farkı (oran): 5 puan
	// (CoSRE demetiyle aynı eşik; gürültüyü "ayrışma" diye sunmaz).
	bubbleUpMinDiff  = 0.05
	bubbleUpValueMax = 120
)

type bubbleUpArgs struct {
	Service string `json:"service"`
	Compare string `json:"compare,omitempty"`
	RangeS  int    `json:"range_s,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

// bubbleUpRow — modele giden satır; yüzdeler 0–100.
type bubbleUpRow struct {
	Key            string  `json:"key"`
	Value          string  `json:"value"`
	SelectionPct   float64 `json:"selection_pct"`
	BaselinePct    float64 `json:"baseline_pct"`
	DiffPct        float64 `json:"diff_pct"`
	SelectionCount int64   `json:"selection_count"`
	BaselineCount  int64   `json:"baseline_count"`
}

// normalizeBubbleUpArgs — SAF (tablo testli): servis zorunlu; compare
// errors (varsayılan) | previous; range_s [300, 3600], varsayılan 600;
// limit [1, 10], varsayılan 5.
func normalizeBubbleUpArgs(a bubbleUpArgs) (service string, errorSubset bool, rangeS, limit int, err error) {
	service = strings.TrimSpace(a.Service)
	if service == "" {
		return "", false, 0, 0, errors.New("service gerekli (tam ad; list_services ile bul)")
	}
	switch strings.ToLower(strings.TrimSpace(a.Compare)) {
	case "", "errors":
		errorSubset = true
	case "previous":
		errorSubset = false
	default:
		return "", false, 0, 0, fmt.Errorf("compare %q: errors | previous", a.Compare)
	}
	rangeS = a.RangeS
	switch {
	case rangeS <= 0:
		rangeS = bubbleUpDefaultRangeS
	case rangeS < bubbleUpMinRangeS:
		rangeS = bubbleUpMinRangeS
	case rangeS > bubbleUpMaxRangeS:
		rangeS = bubbleUpMaxRangeS
	}
	return service, errorSubset, rangeS, clampLimit(a.Limit, 5, 10), nil
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }

// bubbleUpEnvelope — SAF (tablo testli): chstore sonucu → araç zarfı.
// Seçim ya da taban boşsa satır yok ve `note` nedenini söyler (yokluk kanıt
// değildir: "hatalı span yok" ile "ayrışma yok" ayrı cümleler).
func bubbleUpEnvelope(service string, errorSubset bool, rangeS, limit int, bu *chstore.BubbleUpResult) map[string]any {
	compare := "window_vs_previous"
	if errorSubset {
		compare = "errors_vs_all"
	}
	out := map[string]any{
		"service": service, "compare": compare, "window_s": rangeS,
		"min_diff_pct": bubbleUpMinDiff * 100,
		"attributes":   []bubbleUpRow{}, "count": 0, "has_more": false,
	}
	if bu == nil {
		out["note"] = "no result"
		return out
	}
	out["selection_total"], out["baseline_total"] = bu.SelectionTotal, bu.BaselineTotal
	switch {
	case bu.SelectionTotal == 0 && errorSubset:
		out["note"] = "no error spans for this service in the window — nothing to compare (not evidence of health: check the service name with list_services and the window)"
		return out
	case bu.SelectionTotal == 0:
		out["note"] = "no spans for this service in the window — nothing to compare (check the service name with list_services)"
		return out
	case bu.BaselineTotal == 0:
		out["note"] = "no spans in the previous window — nothing to compare against (service or traffic may be new)"
		return out
	}
	tops := rca.TopBubbleUp(bu, limit+1, bubbleUpMinDiff)
	hasMore := len(tops) > limit
	if hasMore {
		tops = tops[:limit]
	}
	rows := make([]bubbleUpRow, 0, len(tops))
	for _, t := range tops {
		rows = append(rows, bubbleUpRow{
			Key: t.Key, Value: truncateRunes(t.Value, bubbleUpValueMax),
			SelectionPct: round1(t.SelPct), BaselinePct: round1(t.BasePct), DiffPct: round1(t.SelPct - t.BasePct),
			SelectionCount: t.SelCount, BaselineCount: t.BaseCount,
		})
	}
	out["attributes"], out["count"], out["has_more"] = rows, len(rows), hasMore
	if len(rows) == 0 {
		out["note"] = "no attribute value is over-represented by at least 5 points — the problem does not concentrate on one route / pod / version"
	}
	return out
}

func bubbleUpTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "bubble_up",
		ShortDescription: "BubbleUp: bir serviste hatalı (ya da bu pencerenin) span'lerinde hangi attribute değeri fazla temsil ediliyor (rota, pod, sürüm). Ham spans taraması; yalnız dış MCP istemcisi için.",
		Description: "BubbleUp for one service: which attribute values (route, pod, version, db statement …) are over-represented in a selection of spans compared to a baseline. " +
			"compare=errors (default): error spans vs ALL spans of the service in the same window — 'what is special about the failures'. " +
			"compare=previous: the window vs the equal-length window right before it — 'which value grew' (use for latency / volume questions, where there is no error subset). " +
			"Returns per attribute its top value with selection_pct, baseline_pct and diff_pct (0–100) plus raw counts; only values over-represented by ≥ 5 points are listed, " +
			"so an empty list with a `note` means 'does not concentrate on one dimension', not 'not checked'. " +
			"COST: this scans raw spans (up to 30 attribute keys). Keep range_s small: default 600, clamped to [300, 3600]; the call is cut at 15 s — on a busy service narrow range_s rather than retrying. " +
			"Use it AFTER get_service_health / list_problems have told you WHICH service; do not use it to find the service (list_services) or to read the persisted hypothesis (get_problem_root_cause). " +
			"Not available to the in-app assistant: there the same comparison arrives pre-fetched in the root-cause evidence.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Exact service.name (from list_services). Required."},
				"compare": map[string]any{"type": "string", "enum": []string{"errors", "previous"}, "description": "errors = error spans vs all spans, same window (default). previous = this window vs the equal window before it."},
				"range_s": map[string]any{"type": "integer", "minimum": 0, "maximum": bubbleUpMaxRangeS, "description": "Window seconds, ending now. Default 600; clamped to [300, 3600]."},
				"limit":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Max attributes. Default 5."},
			},
			"required": []string{"service"},
		},
		MinRole: "", // REST eşi GET /api/spans/bubbleup viewer'a açık
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a bubbleUpArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			service, errorSubset, rangeS, limit, err := normalizeBubbleUpArgs(a)
			if err != nil {
				return nil, err
			}
			from, to := rangeWindow(ctx, rangeS)
			bctx, cancel := context.WithTimeout(ctx, bubbleUpTimeout)
			defer cancel()
			bu, err := d.Store.ServiceBubbleUp(bctx, service, errorSubset, from, to)
			if err != nil {
				if bctx.Err() != nil && ctx.Err() == nil {
					return nil, fmt.Errorf("bubble_up timed out after %s (raw span scan) — narrow range_s", bubbleUpTimeout)
				}
				return nil, err
			}
			return bubbleUpEnvelope(service, errorSubset, rangeS, limit, bu), nil
		},
	}
}
