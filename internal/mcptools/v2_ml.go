package mcptools

// v2_ml.go — ML programı dilim 5 (v0.10.1760): CoSRE v2 araçları.
//
//   - get_log_anomalies — ML gölge olayları (ml_events; varsayılan log
//     sinyali, trace/metric de seçilebilir): servis, dedektör, bölüm,
//     gözlenen/taban, skor, teyit, operatör kararı. Gölge: Problem DEĞİL.
//   - explain_ml_event — tek olayın replay kaydından (ml_event_snapshots)
//     açıklama olguları: takvim sınıfı, taban medyan/MAD, etkin z, tetiklenen
//     kova sayısı, serinin tepe/taban özeti, teyit eden olaylar, servisin
//     diğer sinyalleri, rollout'lar, exception/Oracle/BubbleUp referansları,
//     ≤3 örnek trace ID, Replay page_url. Ham seri DÖNMEZ.
//
// v0.10.1875 (öğrenme döngüsü Faz 2 parti B, audit §7.3 "çıktı struct'ı
// API'de, CoSRE aracında ve bildirimde aynen"): satır `summary` (ml.
// ExplainSummary — liste ve detayla AYNI cümle), explain_ml_event `summary`
// + `card` (ne oldu / ne kadar / ne zamandan beri / etki; ml.BuildExplainCard).
// Yeni metin üretici YOK; CoSRE adım kartı bu alanları gösterir.
//
// Yalnız v2 sohbet kataloğu (registerV2Subset): v1 kataloğu ve dış MCP
// tools/list değişmez. Salt okuma, viewer düzeyi (REST eşleri /api/ml/events
// ve /api/ml/{id}/replay her role açık). Gövde ≤ 2 KB.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/ml"
)

const (
	GetLogAnomaliesName = "get_log_anomalies"
	ExplainMLEventName  = "explain_ml_event"
	mlToolDefaultRows   = 10
	mlToolMaxRows       = 20
	mlToolMaxBytes      = 2048
)

type getLogAnomaliesArgs struct {
	Service  string `json:"service,omitempty"`
	Signal   string `json:"signal,omitempty"`
	Detector string `json:"detector,omitempty"`
	RangeS   int    `json:"range_s,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type explainMLEventArgs struct {
	ID string `json:"id"`
}

// mlEventRow — SAF: olay → model satırı (kısa alanlar). summary: sunucunun
// tek satır özeti (boşsa alan yok).
func mlEventRow(e chstore.MLEvent, summary string) map[string]any {
	part := e.Level
	if e.Code != "" {
		part = e.Code
	}
	if e.Pod != "" {
		part = "pod " + e.Pod
	}
	row := map[string]any{"id": e.ID, "signal": e.Signal, "detector": e.Detector, "service": e.Service,
		"started": time.Unix(0, e.StartedAt).UTC().Format(time.RFC3339), "observed": round3(e.Observed),
		"baseline": round3(e.Baseline), "direction": e.Direction, "score": round3(e.Score)}
	if part != "" {
		row["part"] = truncateRunes(part, 80)
	}
	if e.Verdict != "" {
		row["verdict"] = e.Verdict
	}
	if summary != "" {
		row["summary"] = summary
	}
	var ev map[string]any
	if json.Unmarshal([]byte(e.Evidence), &ev) == nil {
		if c, ok := ev["confirmScore"].(float64); ok && c > 0 {
			row["confirmed_by"] = int(c)
		}
	}
	return row
}

// fitRows — SAF: gövde ≤ max bayt olana dek satır düşer.
func fitRows(body map[string]any, key string, rows []map[string]any, maxBytes int) map[string]any {
	for n := len(rows); n >= 0; n-- {
		body[key] = rows[:n]
		body["returned"] = n
		if raw, _ := json.Marshal(body); len(raw) <= maxBytes || n == 0 {
			return body
		}
	}
	return body
}

func getLogAnomaliesTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: GetLogAnomaliesName,
		ShortDescription: "ML gölge anomalileri (varsayılan log; trace/metric seçilebilir): servis, dedektör, gözlenen/taban, skor, teyit, operatör kararı. " +
			"Gölgedir, Problem değildir. Tek olayın ayrıntısı için explain_ml_event.",
		Description: "List recent ML shadow anomaly events (ml_events) — log signal by default (log volume, error rate, silence, template spike, rare template, first-seen log pattern, rare error code, pod outlier, change point); `signal` trace or metric selects the trace (endpoint latency/error/traffic, dependency latency, pod outlier, change point) or metric-family detectors. " +
			"Each row: id, signal, detector, service, part (level / endpoint / code / pod), start time, observed vs baseline, direction, robust score, confirmed_by (other detectors on the same service within the confirm window) and the operator verdict if any. " +
			"A single event never opens a Problem by itself — say so, do not present one as an alert; only when two or more different signals of the same service agree does their group open one Problem (rule anomaly-auto:group:…); the exception is the service-level detectors (svc_error_rate, svc_breadth), which open anomaly-auto:svc:<service> — one ML Problem per service either way, listed by the problem tools. Use it for 'any log anomaly on X', 'what did ML see'. For one event's evidence call explain_ml_event with its id; for real alerts use the problem tools. " +
			"range_s default 86400, max 604800; at most 20 rows, body under 2 KB.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service":  map[string]any{"type": "string", "description": "Exact service name. Empty = all services."},
				"signal":   map[string]any{"type": "string", "enum": []string{"log", "trace", "metric"}, "description": "Signal. Default log."},
				"detector": map[string]any{"type": "string", "description": "Detector name filter (e.g. error_rate, template_spike). Empty = all."},
				"range_s":  map[string]any{"type": "integer", "minimum": 0, "maximum": 604800, "description": "Lookback seconds. Default 86400."},
				"limit":    map[string]any{"type": "integer", "minimum": 1, "maximum": mlToolMaxRows, "description": "Max rows. Default 10, max 20."},
			},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a getLogAnomaliesArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			sig := strings.TrimSpace(a.Signal)
			if sig != chstore.MLSignalTrace && sig != chstore.MLSignalMetric {
				sig = chstore.MLSignalLog
			}
			if a.RangeS <= 0 {
				a.RangeS = 86400
			}
			from, now := rangeWindow(ctx, a.RangeS)
			limit := clampLimit(a.Limit, mlToolDefaultRows, mlToolMaxRows)
			evs, err := d.Store.ListMLEvents(ctx, chstore.MLEventsFilter{Since: from, Signal: sig,
				Service: strings.TrimSpace(a.Service), Detector: strings.TrimSpace(a.Detector), Limit: limit})
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(evs))
			cfg := d.Store.GetMLConfig(ctx)
			loc := ml.OperatorLocation(d.Store.AnomalySensitivityForDetectors())
			for _, e := range evs {
				rows = append(rows, mlEventRow(e, ml.ExplainSummary(ml.SummaryInput(e, cfg, now, loc))))
			}
			body := map[string]any{"signal": sig, "window_s": a.RangeS, "total": len(evs), "stage": "shadow (Problem değil, terfi kapalı)",
				"page_url": "/anomalies?tab=ml&signal=" + url.QueryEscape(sig)}
			return fitRows(body, "events", rows, mlToolMaxBytes), nil
		},
	}
}

// mlEventPageURL — SAF: ML olay detayı (/anomalies/ml/:id; FE mlEventHref ikizi).
// v0.10.1875: eski ?replay= çekmece adresi v0.10.1770'te sayfaya taşınmıştı.
func mlEventPageURL(id string) string { return "/anomalies/ml/" + url.PathEscape(id) }

var errNoMLEventID = errors.New("id zorunlu: get_log_anomalies satırındaki id'yi ver")

// explainMLPayload — SAF: replay kaydı → model olguları (ham seri yok).
func explainMLPayload(s chstore.MLSnapshot, related []chstore.MLSnapshot, card *ml.ExplainCard) map[string]any {
	var pts []chstore.MLPoint
	_ = json.Unmarshal([]byte(s.Series), &pts)
	var dwell []int64
	_ = json.Unmarshal([]byte(s.Dwell), &dwell)
	var thr, ev map[string]any
	_ = json.Unmarshal([]byte(s.Thresholds), &thr)
	_ = json.Unmarshal([]byte(s.Evidence), &ev)
	peak, low := math.Inf(-1), math.Inf(1)
	for _, p := range pts {
		peak, low = math.Max(peak, p.V), math.Min(low, p.V)
	}
	out := map[string]any{"id": s.EventID, "signal": s.Signal, "detector": s.Detector, "service": s.Service, "part": truncateRunes(s.Part, 80),
		"detected": time.Unix(0, s.DetectedAt).UTC().Format(time.RFC3339), "last_seen": time.Unix(0, s.LastSeen).UTC().Format(time.RFC3339),
		"day_class": s.DayClass, "baseline_median": round3(s.BaselineMed), "baseline_mad": round3(s.BaselineMAD),
		"triggered_buckets": len(dwell), "bucket_sec": s.BucketSec, "complete": s.Complete, "stage": "shadow (Problem değil)",
		"page_url": mlEventPageURL(s.EventID)}
	if card != nil {
		out["summary"] = card.Summary
		c := map[string]any{"what": card.What, "how_much": card.HowMuch, "since": card.Since}
		if card.Impact != "" {
			c["impact"] = card.Impact
		}
		out["card"] = c
	}
	if len(pts) > 0 {
		out["series_peak"], out["series_low"], out["series_points"] = round3(peak), round3(low), len(pts)
	}
	for _, k := range []string{"criticalZ", "effectiveZ", "dwellBuckets", "impactMinRel"} {
		if v, ok := thr[k]; ok {
			out[k] = v
		}
	}
	for _, k := range []string{"exceptions", "oracle", "bubbleUp", "rollouts", "influencers", "phrase"} {
		if v, ok := ev[k]; ok {
			out[k] = v
		}
	}
	if len(s.ConfirmIDs) > 0 {
		out["confirmed_by"] = s.ConfirmIDs
	}
	var others []string
	for _, r := range related {
		if r.EventID != s.EventID && len(others) < 5 {
			others = append(others, r.Signal+"/"+r.Detector+" "+truncateRunes(r.Part, 40))
		}
	}
	if len(others) > 0 {
		out["other_signals"] = others
	}
	var samples []chstore.MLSample
	_ = json.Unmarshal([]byte(s.Samples), &samples)
	var tids []string
	for _, x := range samples {
		if x.TraceID != "" && len(tids) < 3 {
			tids = append(tids, x.TraceID)
		}
	}
	if len(tids) > 0 {
		out["sample_trace_ids"] = tids
	}
	if raw, _ := json.Marshal(out); len(raw) > mlToolMaxBytes { // kanıt referansları uzunsa en büyükleri düşer
		for _, k := range []string{"bubbleUp", "influencers", "oracle", "other_signals", "rollouts", "exceptions", "card"} {
			delete(out, k)
			if raw, _ := json.Marshal(out); len(raw) <= mlToolMaxBytes {
				break
			}
		}
	}
	return out
}

func explainMLEventTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name: ExplainMLEventName,
		ShortDescription: "Tek ML olayının replay kaydı: takvim sınıfı, taban, etkin eşik, tetiklenen kovalar, teyit, diğer sinyaller, rollout, " +
			"exception/Oracle/BubbleUp referansı, örnek trace. id get_log_anomalies'ten.",
		Description: "Explain ONE ML shadow event from its self-contained replay record (ml_event_snapshots; no Elasticsearch or rollup read): detector, service, part, detection and last-seen time, calendar day class, baseline median and MAD, critical and effective z (feedback-adjusted), dwell and impact thresholds, how many buckets crossed the band, the series peak/low over ±2 h, confirming events, the service's other ML signals within ±2 h, rollouts within the window, exception group / Oracle error / BubbleUp references (trace events), up to 3 sample trace ids and a page_url to the Replay view. " +
			"Use it after get_log_anomalies when the user asks why an ML event fired. The raw series is NOT returned (summary only); events detected before the replay record existed return a named error. It is a shadow detection, never a Problem.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": "ML event id from get_log_anomalies."},
			},
			"required": []string{"id"},
		},
		MinRole: "",
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a explainMLEventArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			id := strings.TrimSpace(a.ID)
			if id == "" {
				return nil, errNoMLEventID
			}
			s, ok, err := d.Store.GetMLSnapshot(ctx, id)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, fmt.Errorf("bu ML olayı için replay kaydı yok (kayıt v0.10.1752 sonrası açılan olaylarda oluşur): %s", id)
			}
			det := time.Unix(0, s.DetectedAt)
			related, _ := d.Store.ListMLSnapshotsForService(ctx, s.Service, det.Add(-2*time.Hour), det.Add(2*time.Hour))
			// v0.10.1875 — kart: canlı olay satırı (süre/durum güncel) + kayıt;
			// replay ucuyla aynı kurulum (ml.AssembleCardInput), yayın/profil yok.
			_, now := rangeWindow(ctx, 0) // çıpa: kartın "şimdi"si aracın penceresiyle aynı an
			src := ml.CardSources{Snapshot: &s, Related: related, Config: d.Store.GetMLConfig(ctx), Now: now,
				Loc: ml.OperatorLocation(d.Store.AnomalySensitivityForDetectors()), HrefOf: mlEventPageURL}
			if evs, err := d.Store.ListMLEvents(ctx, chstore.MLEventsFilter{ID: id, Since: now.Add(-31 * 24 * time.Hour), Limit: 1}); err == nil && len(evs) > 0 {
				src.Event = &evs[0]
			}
			var card *ml.ExplainCard
			if in, ok := ml.AssembleCardInput(src); ok {
				c := ml.BuildExplainCard(in)
				card = &c
			}
			return explainMLPayload(s, related, card), nil
		},
	}
}
