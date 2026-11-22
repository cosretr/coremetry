package mcptools

// flow_tools.go — v0.10.2449 (operatör eki 2026-11-22): CoSRE Kafka / MQ ve
// aşağı bağımlılık soruları.
//   - messaging_flow — topic ya da servis → üretici / tüketici servisler (rol
//     başına yayın / işleme süresi, hata %, pod'lar; messaging_caller_summary_5m)
//     + consumer lag (VictoriaMetrics kafka.consumer.records_lag_max, servis
//     healthcheck kontrol 12 ile AYNI katalog satırı ve süzgeç; yoksa dürüst
//     "ölçülmüyor"). "hangi topic'te gecikme var", "bu mesajı kim tüketiyor",
//     "consumer lag var mı", "hangi MQ'da uzamış".
//   - downstream_calls — servis → çağırdığı hedefler: topology_edges_5m (MV,
//     örneklenmiş span) + VM HTTP client histogramı (Deps.DownstreamRED,
//     örnekleme ÖNCESİ tam sayım; runtimesig/downstream.go). "bu servis kimi
//     çağırıyor, hangisi yavaş / hata veriyor".
// Sayılar okumadan aynen; okunamayan bölüm `unread`'e (okunamadı ≠ yok).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// Araç adları.
const (
	MessagingFlowName   = "messaging_flow"
	DownstreamCallsName = "downstream_calls"
)

const (
	mfDefaultRangeS = 3600
	mfMaxRangeS     = 7 * 86400
	mfRowsMax       = 40
	mfLagServices   = 3
	mfLagWindow     = time.Hour
	dcDefaultRangeS = 3600
	dcMaxRangeS     = 86400
	dcEdgesMax      = 15
	dcTargetsMax    = 15
)

type messagingFlowArgs struct {
	Topic   string `json:"topic,omitempty"`
	Service string `json:"service,omitempty"`
	RangeS  int    `json:"range_s,omitempty"`
}

type flowReader interface {
	MessagingFlow(ctx context.Context, topic, service string, from, to time.Time, limit int) ([]chstore.MessagingFlowRow, error)
	DownstreamEdges(ctx context.Context, service string, from, to time.Time, limit int) ([]chstore.DownstreamEdge, error)
}

// mfParty — bir hedefteki tek servis + rol.
type mfParty struct {
	Service string   `json:"service"`
	Spans   uint64   `json:"spans"`
	ErrPct  float64  `json:"error_pct"`
	AvgMs   float64  `json:"avg_ms"`
	P95Ms   float64  `json:"p95_ms"`
	Pods    []string `json:"pods,omitempty"`
}

// mfTopic — hedef (topic / kuyruk) başına üreticiler ve tüketiciler.
type mfTopic struct {
	System      string    `json:"system"`
	Broker      string    `json:"broker,omitempty"`
	Destination string    `json:"destination"`
	Producers   []mfParty `json:"producers"`
	Consumers   []mfParty `json:"consumers"`
	Clients     []mfParty `json:"clients,omitempty"`
}

// mfLag — tüketici servisin topic başına lag'i (VM).
type mfLag struct {
	Service string  `json:"service"`
	Topic   string  `json:"topic"`
	Last    float64 `json:"last_records"`
	Max     float64 `json:"max_records"`
}

func pct(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return round4(float64(part) * 100 / float64(whole))
}

// groupMessagingFlow — SAF: satırlar → hedef başına üretici / tüketici.
func groupMessagingFlow(rows []chstore.MessagingFlowRow) []mfTopic {
	idx := map[string]int{}
	var out []mfTopic
	for _, r := range rows {
		k := r.System + "\x00" + r.Broker + "\x00" + r.Destination
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, mfTopic{System: r.System, Broker: r.Broker, Destination: r.Destination, Producers: []mfParty{}, Consumers: []mfParty{}})
		}
		p := mfParty{Service: r.Service, Spans: r.Spans, ErrPct: pct(r.Errors, r.Spans), AvgMs: round4(r.AvgMs), P95Ms: round4(r.P95Ms), Pods: r.Pods}
		switch strings.ToLower(r.Role) {
		case "producer":
			out[i].Producers = append(out[i].Producers, p)
		case "consumer":
			out[i].Consumers = append(out[i].Consumers, p)
		default:
			out[i].Clients = append(out[i].Clients, p)
		}
	}
	if out == nil {
		out = []mfTopic{}
	}
	return out
}

// lagRows — SAF: VM serileri → topic başına son / en yüksek lag.
func lagRows(service string, series []chstore.SpanMetricSeries) []mfLag {
	var out []mfLag
	for _, s := range series {
		if len(s.Points) == 0 {
			continue
		}
		mx := math.Inf(-1)
		last := math.NaN()
		for _, p := range s.Points {
			if math.IsNaN(p.Value) || math.IsInf(p.Value, 0) {
				continue
			}
			last = p.Value
			if p.Value > mx {
				mx = p.Value
			}
		}
		if math.IsNaN(last) {
			continue
		}
		out = append(out, mfLag{Service: service, Topic: strings.Join(s.GroupKey, " · "), Last: round4(last), Max: round4(mx)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Max != out[j].Max {
			return out[i].Max > out[j].Max
		}
		return out[i].Topic < out[j].Topic
	})
	return out
}

func runMessagingFlow(ctx context.Context, d Deps, r flowReader, a messagingFlowArgs) (map[string]any, error) {
	topic, svc := strings.TrimSpace(a.Topic), strings.TrimSpace(a.Service)
	if topic == "" && svc == "" {
		return nil, errors.New("give topic (queue/topic name or fragment) or service — ask the user which one, never guess")
	}
	if r == nil {
		return nil, errors.New("telemetry store not configured")
	}
	rangeS := a.RangeS
	if rangeS <= 0 {
		rangeS = mfDefaultRangeS
	}
	if rangeS > mfMaxRangeS {
		rangeS = mfMaxRangeS
	}
	from, to := rangeWindow(ctx, rangeS)
	out := map[string]any{"tool": MessagingFlowName, "window_s": rangeS, "source": "messaging_caller_summary_5m (sampled spans, 5-minute buckets)"}
	if topic != "" {
		out["topic_filter"] = topic
	}
	if svc != "" {
		out["service"] = svc
	}
	rows, err := r.MessagingFlow(ctx, topic, svc, from, to, mfRowsMax)
	if err != nil {
		return nil, err
	}
	topics := groupMessagingFlow(rows)
	out["topics"] = topics
	out["has_more"] = len(rows) >= mfRowsMax
	// Lag — tüketici servisler (≤3), VM.
	var consumers []string
	seen := map[string]bool{}
	for _, t := range topics {
		for _, c := range t.Consumers {
			if !seen[c.Service] && len(consumers) < mfLagServices {
				seen[c.Service] = true
				consumers = append(consumers, c.Service)
			}
		}
	}
	lagFrom := from
	if to.Sub(lagFrom) > mfLagWindow {
		lagFrom = to.Add(-mfLagWindow)
	}
	switch {
	case d.Metrics == nil:
		out["lag_status"] = "not_configured: no metric source — consumer lag is not measured; never state a lag value"
	case len(consumers) == 0:
		out["lag_status"] = "no consumer service seen in the window"
	default:
		lags := []mfLag{}
		var unread []string
		for _, c := range consumers {
			f, ferr := hcKafkaLagFilter(c, lagFrom, to)
			if ferr != nil {
				unread = append(unread, c+": "+ferr.Error())
				continue
			}
			ser, qerr := d.Metrics.QueryMetric(ctx, f)
			if qerr != nil {
				unread = append(unread, c+": "+qerr.Error())
				continue
			}
			for _, l := range lagRows(c, ser) {
				if topic == "" || strings.Contains(strings.ToLower(l.Topic), strings.ToLower(topic)) {
					lags = append(lags, l)
				}
			}
		}
		out["lag"] = lags
		out["lag_source"] = hcKafkaLagMetric + " (consumer client metric, records)"
		if len(lags) == 0 && len(unread) == 0 {
			out["lag_status"] = "no lag series for these consumers (the Kafka client metric is not exported) — lag is not measured"
		}
		if len(unread) > 0 {
			out["unread"] = unread
		}
	}
	out["notes"] = []string{
		"producer p95_ms = publish time (writing to the broker); consumer p95_ms = processing time in the consumer",
		"pods are the spans' host_name; error_pct is over stored (sampled) spans",
	}
	if len(topics) == 0 {
		out["note"] = "no messaging span for this topic/service in the window — say so; do not invent a topic or consumer"
	}
	return out, nil
}

type downstreamCallsArgs struct {
	Service string `json:"service"`
	RangeS  int    `json:"range_s,omitempty"`
}

// dcTarget — VM hedef satırı (örnekleme öncesi).
type dcTarget struct {
	Address          string   `json:"address"`
	Service          string   `json:"service,omitempty"`
	RPS              float64  `json:"rps"`
	P95Ms            *float64 `json:"p95_ms,omitempty"`
	ErrorPct         float64  `json:"error_pct"`
	DominantError    string   `json:"dominant_error,omitempty"`
	DominantErrorPct float64  `json:"dominant_error_pct,omitempty"`
}

func runDownstreamCalls(ctx context.Context, d Deps, r flowReader, a downstreamCallsArgs) (map[string]any, error) {
	svc := strings.TrimSpace(a.Service)
	if svc == "" {
		return nil, errors.New("service is required — ask the user which service, never guess")
	}
	rangeS := a.RangeS
	if rangeS <= 0 {
		rangeS = dcDefaultRangeS
	}
	if rangeS > dcMaxRangeS {
		rangeS = dcMaxRangeS
	}
	from, to := rangeWindow(ctx, rangeS)
	out := map[string]any{"tool": DownstreamCallsName, "service": svc, "window_s": rangeS}
	var unread []string
	if r != nil {
		edges, err := r.DownstreamEdges(ctx, svc, from, to, dcEdgesMax)
		if err != nil {
			unread = append(unread, "topology: "+err.Error())
		} else {
			type edgeRow struct {
				chstore.DownstreamEdge
				ErrPct float64 `json:"error_pct"`
			}
			rows := make([]edgeRow, 0, len(edges))
			for _, e := range edges {
				e.AvgMs = round4(e.AvgMs)
				e.P99Ms = round4(e.P99Ms)
				rows = append(rows, edgeRow{e, pct(e.Errors, e.Calls)})
			}
			out["topology"] = rows
			out["topology_source"] = "topology_edges_5m (sampled spans; avg over the window, p99 = worst 5-minute bucket)"
		}
	}
	if d.DownstreamRED == nil {
		out["vm_status"] = "not_configured: no VictoriaMetrics HTTP client histogram reader — unsampled per-target RED unavailable"
	} else {
		rep, err := d.DownstreamRED(ctx, svc, from, to)
		if err != nil {
			unread = append(unread, "vm: "+err.Error())
		} else {
			ts := make([]dcTarget, 0, len(rep.Targets))
			for i, t := range rep.Targets {
				if i >= dcTargetsMax {
					break
				}
				row := dcTarget{Address: t.Address, Service: t.Service, RPS: round4(t.RPS), ErrorPct: round4(t.ErrorPct),
					DominantError: t.DominantError, DominantErrorPct: round4(t.DominantErrorPct)}
				if t.P95Ms >= 0 {
					v := round4(t.P95Ms)
					row.P95Ms = &v
				}
				ts = append(ts, row)
			}
			out["targets"] = ts
			out["targets_source"] = "VictoriaMetrics " + rep.Metric + " (pre-sampling, exact)"
			if len(rep.Notes) > 0 {
				out["vm_notes"] = rep.Notes
			}
		}
	}
	if len(unread) > 0 {
		out["unread"] = unread
	}
	out["notes"] = []string{"prefer `targets` (unsampled) for rates and error %; `topology` also covers db / queue / non-HTTP targets",
		"answer 'which one is slow / failing' from the rows; do not compute values that are not in the result"}
	return out, nil
}

func messagingFlowTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             MessagingFlowName,
		ShortDescription: "Kafka/MQ: topic ya da servis → üretici/tüketici servisler, yayın/işleme süresi, hata %, pod'lar, consumer lag (VM varsa).",
		Description: "Answer 'who produces / consumes this topic', 'which topic or queue is slow', 'is there consumer lag' for a topic (substring) or a service (its topics). " +
			"Returns topics[] (system, broker, destination, producers[] and consumers[] each with spans, error_pct, avg_ms, p95_ms and up to 5 pods), " +
			"lag[] (per consumer service × topic: last_records / max_records from the VictoriaMetrics Kafka client metric kafka.consumer.records_lag_max over ≤1 h) or lag_status when lag is not measured, unread, notes. " +
			"Producer p95 is publish time; consumer p95 is processing time. COST: one 5-minute MV read (≤7 d) plus ≤3 metric queries. " +
			"Never state a lag value that is not in lag[]; never invent a topic. For the fleet-wide queue overview use get_messaging_health.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic":   map[string]any{"type": "string", "description": "Topic / queue name or fragment (case-insensitive)."},
				"service": map[string]any{"type": "string", "description": "Exact service name: its topics and every producer/consumer on them."},
				"range_s": map[string]any{"type": "integer", "minimum": 300, "maximum": mfMaxRangeS, "description": "Lookback seconds. Default 3600."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a messagingFlowArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r flowReader
			if d.Store != nil {
				r = d.Store
			}
			return runMessagingFlow(ctx, d, r, a)
		},
	}
}

func downstreamCallsTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             DownstreamCallsName,
		ShortDescription: "Servis kimi çağırıyor, hangisi yavaş/hata veriyor: hedef başına RED (VM örneklemesiz önce) + topoloji kenarları.",
		Description: "Answer 'which services / dependencies does service X call, and which one is slow or failing' per target. Returns targets[] (VictoriaMetrics HTTP client histogram, pre-sampling and exact: address, resolved service, rps, p95_ms, error_pct, dominant_error) when configured, " +
			"and topology[] (topology_edges_5m pre-aggregate over sampled spans: target, kind service|db|queue|external, calls, errors, error_pct, avg_ms, worst-bucket p99) — topology also covers DB, queue and non-HTTP calls. " +
			"COST: one MV read + one metric query. range_s ≤ 86400. Quote rows as returned; unread lists sections that failed (not 'none'). For a single trace's downstream path use trace_where.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Exact service name (the caller)."},
				"range_s": map[string]any{"type": "integer", "minimum": 300, "maximum": dcMaxRangeS, "description": "Lookback seconds. Default 3600."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a downstreamCallsArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r flowReader
			if d.Store != nil {
				r = d.Store
			}
			return runDownstreamCalls(ctx, d, r, a)
		},
	}
}
