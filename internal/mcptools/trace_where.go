package mcptools

// trace_where.go — v0.10.2449 (operatör raporu 2026-11-22: "timeout'un
// hangi poddan geldiğini biliyor musun", "ocpka veya ocpkb'deki podlardan
// birine düştüğünü biliyorum", "hangi mq'da uzamış", "pod içerisinde geçen
// sürelerde 200ms göremiyorum", "hata osb'den mi dönmüş", "external call var
// mı"). Span başına NEREDE: servis · pod (host_name) · cluster · karşı taraf
// (server.address / db / mesaj hedefi) · süre · durum.
//
// İki kip:
//   - trace_id: Store.GetTrace (trace_summary_5m pencere araması + bloom
//     budamalı, zaman sınırlı okuma) → SAF özet (traceWhereSummary): hata
//     KÖKENİ (çocuğu hatasız en derin hatalı span), zaman aşımı span'leri,
//     servis başına ÖZ süre (çocuklar düşülmüş), dış çağrılar (izlenmeyen
//     karşı taraf), mesaj hedefi başına süre, ≤40 satır.
//   - service + mode (errors | slow | messaging): chstore.TraceWhere — servis
//     önekli, ≤6 sa, satır/süre tavanlı ham okuma; pod · cluster · karşı taraf
//     grupları + örnek trace id.
// peer parçası (ör. "osb") iki kipte de karşı tarafı / span adını / durum
// mesajını süzer: "hata X'ten mi dönmüş" sorusu kanıtla cevaplanır.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// TraceWhereName — aracın adı.
const TraceWhereName = "trace_where"

const (
	twRowsMax        = 40
	twOriginsMax     = 3
	twExternalMax    = 10
	twDefaultRangeS  = 3600
	twMaxRangeS      = 6 * 3600
	twDefaultSlowMs  = 1000
	twMsgRunes       = 160
	twGroupRowsMax   = 20
	twServiceSelfMax = 10
)

var twTraceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

type traceWhereArgs struct {
	TraceID string `json:"trace_id,omitempty"`
	Service string `json:"service,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Peer    string `json:"peer,omitempty"`
	MinMs   int64  `json:"min_ms,omitempty"`
	RangeS  int    `json:"range_s,omitempty"`
}

type traceWhereReader interface {
	GetTrace(ctx context.Context, traceID string) ([]chstore.SpanRow, error)
	TraceWhere(ctx context.Context, q chstore.WhereQuery) (chstore.WhereResult, error)
}

// twRow — tek span.
type twRow struct {
	Service    string  `json:"service"`
	Pod        string  `json:"pod,omitempty"`
	Cluster    string  `json:"cluster,omitempty"`
	Span       string  `json:"span"`
	Kind       string  `json:"kind,omitempty"`
	Peer       string  `json:"peer,omitempty"`
	DurationMs float64 `json:"duration_ms"`
	SelfMs     float64 `json:"self_ms"`
	Status     string  `json:"status"`
	HTTPStatus uint16  `json:"http_status,omitempty"`
	Message    string  `json:"message,omitempty"`
	Timeout    bool    `json:"timeout,omitempty"`
	SpanID     string  `json:"span_id"`
}

// twAttr — SAF: önce span, sonra resource özniteliği.
func twAttr(s chstore.SpanRow, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(s.Attributes[k]); v != "" {
			return v
		}
		if v := strings.TrimSpace(s.ResourceAttributes[k]); v != "" {
			return v
		}
	}
	return ""
}

// spanPeer — SAF: span'in karşı tarafı (lookupPeerSQL ile aynı sıra + URL host'u).
func spanPeer(s chstore.SpanRow) string {
	if v := twAttr(s, "messaging.destination.name", "messaging.destination"); v != "" {
		return v
	}
	if s.DBSystem != "" {
		if n := twAttr(s, "db.name", "db.namespace"); n != "" {
			return s.DBSystem + ":" + n
		}
		return s.DBSystem
	}
	if s.PeerService != "" {
		return s.PeerService
	}
	if v := twAttr(s, "server.address", "net.peer.name"); v != "" {
		return v
	}
	return urlHost(twAttr(s, "url.full", "http.url"))
}

// spanPod / spanCluster — SAF: span'in kendi resource'u (ad eşlemesi yok).
func spanPod(s chstore.SpanRow) string {
	if v := twAttr(s, "k8s.pod.name"); v != "" {
		return v
	}
	return s.HostName
}

func spanCluster(s chstore.SpanRow) string {
	return twAttr(s, "k8s.cluster.name", "openshift.cluster.name", "cluster")
}

func spanIsTimeout(s chstore.SpanRow) bool {
	if s.StatusCode != "error" && s.StatusCode != "STATUS_CODE_ERROR" && s.HTTPStatus != 504 && s.HTTPStatus != 408 {
		return false
	}
	t := strings.ToLower(s.StatusMessage + " " + twAttr(s, "exception.type", "exception.message"))
	return strings.Contains(t, "timeout") || strings.Contains(t, "timed out") || strings.Contains(t, "deadline exceeded") ||
		s.HTTPStatus == 504 || s.HTTPStatus == 408
}

func spanIsError(s chstore.SpanRow) bool {
	return s.StatusCode == "error" || s.StatusCode == "STATUS_CODE_ERROR"
}

// spanSelfMs — SAF: süre − çocukların (kendi aralığına kırpılmış) BİRLEŞİMİ.
func spanSelfMs(p chstore.SpanRow, kids []chstore.SpanRow) float64 {
	type iv struct{ a, b int64 }
	var ivs []iv
	for _, k := range kids {
		a, b := k.StartTime, k.EndTime
		if a < p.StartTime {
			a = p.StartTime
		}
		if b > p.EndTime {
			b = p.EndTime
		}
		if b > a {
			ivs = append(ivs, iv{a, b})
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].a < ivs[j].a })
	var covered, curA, curB int64
	for i, x := range ivs {
		if i == 0 || x.a > curB {
			covered += curB - curA
			curA, curB = x.a, x.b
			continue
		}
		if x.b > curB {
			curB = x.b
		}
	}
	covered += curB - curA
	self := float64(p.EndTime-p.StartTime-covered) / 1e6
	if self < 0 {
		return 0
	}
	return round4(self)
}

// traceWhereSummary — SAF: trace span'leri → nerede özeti.
func traceWhereSummary(traceID string, spans []chstore.SpanRow, peer string) map[string]any {
	kids := map[string][]chstore.SpanRow{}
	byID := map[string]chstore.SpanRow{}
	for _, s := range spans {
		kids[s.ParentSpanID] = append(kids[s.ParentSpanID], s)
		byID[s.SpanID] = s
	}
	peerLower := strings.ToLower(strings.TrimSpace(peer))
	rows := make([]twRow, 0, len(spans))
	selfBySvc := map[string]float64{}
	type msgAgg struct {
		n          int
		total, max float64
	}
	msgs := map[string]*msgAgg{}
	var origins, external, timeouts, peerHits []twRow
	errN := 0
	for _, s := range spans {
		msg := s.StatusMessage
		if msg == "" {
			msg = twAttr(s, "exception.message")
		}
		if r := []rune(msg); len(r) > twMsgRunes {
			msg = string(r[:twMsgRunes])
		}
		row := twRow{Service: s.ServiceName, Pod: spanPod(s), Cluster: spanCluster(s), Span: s.Name, Kind: s.Kind,
			Peer: spanPeer(s), DurationMs: round4(s.DurationMs), SelfMs: spanSelfMs(s, kids[s.SpanID]),
			Status: s.StatusCode, HTTPStatus: s.HTTPStatus, Message: msg, Timeout: spanIsTimeout(s), SpanID: s.SpanID}
		rows = append(rows, row)
		selfBySvc[s.ServiceName] += row.SelfMs
		if spanIsError(s) {
			errN++
			childErr := false
			for _, k := range kids[s.SpanID] {
				if spanIsError(k) {
					childErr = true
					break
				}
			}
			if !childErr {
				origins = append(origins, row)
			}
		}
		if row.Timeout {
			timeouts = append(timeouts, row)
		}
		k := strings.ToLower(s.Kind)
		if (k == "client" || k == "producer") && row.Peer != "" && s.DBSystem == "" {
			served := false
			for _, c := range kids[s.SpanID] {
				if ck := strings.ToLower(c.Kind); ck == "server" || ck == "consumer" {
					served = true
					break
				}
			}
			if !served {
				external = append(external, row)
			}
		}
		if dest := twAttr(s, "messaging.destination.name", "messaging.destination"); dest != "" {
			m := msgs[dest]
			if m == nil {
				m = &msgAgg{}
				msgs[dest] = m
			}
			m.n++
			m.total += s.DurationMs
			if s.DurationMs > m.max {
				m.max = s.DurationMs
			}
		}
		if peerLower != "" && (strings.Contains(strings.ToLower(row.Peer), peerLower) ||
			strings.Contains(strings.ToLower(row.Span), peerLower) || strings.Contains(strings.ToLower(row.Message), peerLower) ||
			strings.Contains(strings.ToLower(row.Service), peerLower)) {
			peerHits = append(peerHits, row)
		}
	}
	// Satır önceliği: hatalı önce, sonra süre.
	sort.SliceStable(rows, func(i, j int) bool {
		ei, ej := rows[i].Status == "error", rows[j].Status == "error"
		if ei != ej {
			return ei
		}
		return rows[i].DurationMs > rows[j].DurationMs
	})
	truncated := len(rows) > twRowsMax
	if truncated {
		rows = rows[:twRowsMax]
	}
	// Köken: en derin (kök span'e uzaklık) önce.
	depth := func(r twRow) int {
		n, id := 0, r.SpanID
		for i := 0; i < 64; i++ {
			p, ok := byID[byID[id].ParentSpanID]
			if !ok {
				break
			}
			n++
			id = p.SpanID
		}
		return n
	}
	sort.SliceStable(origins, func(i, j int) bool { return depth(origins[i]) > depth(origins[j]) })
	if len(origins) > twOriginsMax {
		origins = origins[:twOriginsMax]
	}
	sort.SliceStable(external, func(i, j int) bool { return external[i].DurationMs > external[j].DurationMs })
	if len(external) > twExternalMax {
		external = external[:twExternalMax]
	}
	if len(timeouts) > twExternalMax {
		timeouts = timeouts[:twExternalMax]
	}
	if len(peerHits) > twExternalMax {
		peerHits = peerHits[:twExternalMax]
	}
	type svcSelf struct {
		Service string  `json:"service"`
		SelfMs  float64 `json:"self_ms"`
	}
	var selfs []svcSelf
	for k, v := range selfBySvc {
		selfs = append(selfs, svcSelf{k, round4(v)})
	}
	sort.Slice(selfs, func(i, j int) bool {
		if selfs[i].SelfMs != selfs[j].SelfMs {
			return selfs[i].SelfMs > selfs[j].SelfMs
		}
		return selfs[i].Service < selfs[j].Service
	})
	if len(selfs) > twServiceSelfMax {
		selfs = selfs[:twServiceSelfMax]
	}
	type msgRow struct {
		Destination string  `json:"destination"`
		Spans       int     `json:"spans"`
		TotalMs     float64 `json:"total_ms"`
		MaxMs       float64 `json:"max_ms"`
	}
	var msgRows []msgRow
	for k, v := range msgs {
		msgRows = append(msgRows, msgRow{k, v.n, round4(v.total), round4(v.max)})
	}
	sort.Slice(msgRows, func(i, j int) bool {
		if msgRows[i].TotalMs != msgRows[j].TotalMs {
			return msgRows[i].TotalMs > msgRows[j].TotalMs
		}
		return msgRows[i].Destination < msgRows[j].Destination
	})
	out := map[string]any{
		"tool": TraceWhereName, "mode": "trace", "trace_id": traceID,
		"span_count": len(spans), "error_spans": errN,
		"error_origin":    nonNilRows(origins),
		"timeouts":        nonNilRows(timeouts),
		"external_calls":  nonNilRows(external),
		"self_by_service": selfs,
		"spans":           rows,
	}
	if len(msgRows) > 0 {
		out["messaging"] = msgRows
	}
	if peerLower != "" {
		out["peer_filter"] = peer
		out["peer_matches"] = nonNilRows(peerHits)
	}
	if truncated {
		out["spans_truncated"] = true
	}
	out["notes"] = []string{
		"error_origin = error spans with no failing child (deepest first): the error was raised there; if its kind is client and peer is set, the downstream peer returned it",
		"self_ms = span duration minus time covered by its children; self_by_service sums it per service (time spent inside the service's own pods)",
		"external_calls = client/producer spans whose peer has no instrumented server span in this trace",
	}
	return out
}

func nonNilRows(r []twRow) []twRow {
	if r == nil {
		return []twRow{}
	}
	return r
}

func runTraceWhere(ctx context.Context, r traceWhereReader, a traceWhereArgs) (map[string]any, error) {
	if r == nil {
		return nil, errors.New("telemetry store not configured")
	}
	tid := strings.ToLower(strings.TrimSpace(a.TraceID))
	if tid != "" {
		if !twTraceIDRe.MatchString(tid) {
			return nil, fmt.Errorf("trace_id must be 32 hex characters (got %q) — never shorten or invent one", a.TraceID)
		}
		spans, err := r.GetTrace(ctx, tid)
		if err != nil {
			return nil, err
		}
		if len(spans) == 0 {
			return map[string]any{"tool": TraceWhereName, "mode": "trace", "trace_id": tid, "found": false,
				"note": "trace not found in the span store (expired by retention, sampled out, or wrong id) — say so; do not guess"}, nil
		}
		out := traceWhereSummary(tid, spans, a.Peer)
		out["found"] = true
		return out, nil
	}
	svc := strings.TrimSpace(a.Service)
	if svc == "" {
		return nil, errors.New("give trace_id, or service (+ mode) — ask the user which trace or service they mean, never guess one")
	}
	mode := strings.TrimSpace(a.Mode)
	switch mode {
	case "":
		mode = chstore.WhereModeErrors
	case chstore.WhereModeErrors, chstore.WhereModeSlow, chstore.WhereModeMessaging:
	default:
		return nil, fmt.Errorf("mode must be errors | slow | messaging")
	}
	rangeS := a.RangeS
	if rangeS <= 0 {
		rangeS = twDefaultRangeS
	}
	if rangeS > twMaxRangeS {
		rangeS = twMaxRangeS
	}
	minMs := a.MinMs
	if minMs <= 0 {
		minMs = twDefaultSlowMs
	}
	from, to := rangeWindow(ctx, rangeS)
	res, err := r.TraceWhere(ctx, chstore.WhereQuery{Service: svc, Mode: mode, Peer: strings.TrimSpace(a.Peer), MinMs: minMs,
		From: from, To: to, Limit: twGroupRowsMax})
	if err != nil {
		return nil, err
	}
	out := map[string]any{"tool": TraceWhereName, "mode": mode, "service": svc, "window_s": int(res.To.Sub(res.From).Seconds()),
		"groups": res.Rows, "group_count": len(res.Rows), "has_more": len(res.Rows) >= twGroupRowsMax}
	if mode == chstore.WhereModeSlow {
		out["min_ms"] = minMs
	}
	if a.Peer != "" {
		out["peer_filter"] = a.Peer
	}
	notes := []string{"counts are stored (sampled) spans — use them to locate pods/peers, not as request totals",
		"pass a sample_trace_id back as trace_id for the span-level path of one request"}
	if res.Clamped {
		notes = append(notes, fmt.Sprintf("window capped to the last %d s (raw-span read)", int(res.To.Sub(res.From).Seconds())))
	}
	if len(res.Rows) == 0 {
		notes = append(notes, "no matching span in the window — that is the answer for this window; do not invent pods or peers")
	}
	out["notes"] = notes
	return out, nil
}

func traceWhereTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             TraceWhereName,
		ShortDescription: "Hata/timeout hangi pod/cluster'dan, hangi MQ uzamış, hata X'ten mi, dış çağrı var mı: trace_id ya da servis+mode.",
		Description: "Locate WHERE in the system something happened, with evidence per span: service, pod (host_name / k8s.pod.name), cluster, peer (messaging destination / db / peer.service / server.address), duration, self time and status. " +
			"Two modes. (1) trace_id (32 hex): returns error_origin (failing spans with no failing child, deepest first — 'which pod did the timeout / error come from', 'did the error come from <downstream>'), " +
			"timeouts, external_calls (client/producer spans with no instrumented server behind them — 'is there an external call'), messaging (per destination total/max ms — 'which queue was slow'), " +
			"self_by_service (time spent inside each service's own spans, children subtracted — 'I can't see 200 ms inside the pod'), and ≤40 spans (errors first). " +
			"(2) service + mode errors | slow (min_ms, default 1000) | messaging over range_s (≤6 h): ≤20 groups by pod · cluster · span · peer with spans, error_spans, timeout_spans, p50/p95/max ms, a status message and a sample_trace_id. " +
			"peer (substring, e.g. a downstream name) filters both modes and answers 'did the error come from X'. COST: trace mode reads one trace (time-bounded); service mode is a service-prefixed raw-span read with row and time caps. " +
			"Counts are sampled spans. Quote pods/peers exactly; never invent a pod, cluster or queue that is not in the result. For error-rate trends use get_service_health / compare_baseline.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trace_id": map[string]any{"type": "string", "description": "32-hex trace id (from the user's text, a pasted log, or another tool). When set, service/mode are ignored."},
				"service":  map[string]any{"type": "string", "description": "Exact service name for the window mode."},
				"mode": map[string]any{"type": "string", "enum": []string{chstore.WhereModeErrors, chstore.WhereModeSlow, chstore.WhereModeMessaging},
					"description": "Window mode: errors (default; failing spans, timeouts flagged), slow (duration ≥ min_ms), messaging (per queue/topic latency)."},
				"peer":    map[string]any{"type": "string", "description": "Downstream / peer substring to filter on (e.g. osb, mainframe, a queue name)."},
				"min_ms":  map[string]any{"type": "integer", "minimum": 1, "maximum": 600000, "description": "slow mode threshold in ms. Default 1000."},
				"range_s": map[string]any{"type": "integer", "minimum": 60, "maximum": twMaxRangeS, "description": "Window mode lookback seconds. Default 3600, max 21600."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a traceWhereArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r traceWhereReader
			if d.Store != nil {
				r = d.Store
			}
			return runTraceWhere(ctx, r, a)
		},
	}
}
