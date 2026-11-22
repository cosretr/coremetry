package chstore

// cosre_lookup_flow.go — v0.10.2449 (operatör eki 2026-11-22: "hangi topic'te
// gecikme var", "bu mesajı kim tüketiyor", "bu servis kimi çağırıyor,
// hangisi yavaş"). mcptools messaging_flow / downstream_calls okumaları.
// Yalnız 5 dk MV'ler: messaging_caller_summary_5m (telemetryReadConn) ve
// topology_edges_5m FINAL (ana bağlantı). Zaman sınırı (≤7 gün) + LIMIT +
// max_execution_time; ham span YOK.

import (
	"context"
	"strings"
	"time"
)

// lookupMVMax — MV okumalarının pencere tavanı.
const lookupMVMax = 7 * 24 * time.Hour

// MessagingFlowRow — (sistem, broker kümesi, hedef, servis, rol) satırı.
type MessagingFlowRow struct {
	System      string   `json:"system"`
	Broker      string   `json:"broker,omitempty"`
	Destination string   `json:"destination"`
	Service     string   `json:"service"`
	Role        string   `json:"role"` // producer | consumer | client
	Spans       uint64   `json:"spans"`
	Errors      uint64   `json:"error_spans"`
	AvgMs       float64  `json:"avg_ms"`
	P95Ms       float64  `json:"p95_ms"`
	Pods        []string `json:"pods,omitempty"` // host_name (≤5)
}

// messagingFlowSQL — SAF: topic parçası ve/veya servis koşulu; bind sırası:
// from, to, [topic], [from, to, service], limit.
func messagingFlowSQL(topic, service string) string {
	var cond strings.Builder
	if topic != "" {
		cond.WriteString(` AND positionCaseInsensitive(destination, ?) > 0`)
	}
	if service != "" {
		// servisin dokunduğu hedefler → o hedeflerin TÜM üretici/tüketicileri.
		cond.WriteString(` AND destination IN (SELECT destination FROM messaging_caller_summary_5m
  WHERE time_bucket >= ? AND time_bucket < ? AND service_name = ? GROUP BY destination LIMIT 50)`)
	}
	return `SELECT msg_system, cluster, destination, service_name, coalesce(nullIf(kind, ''), 'client') AS role,
       countMerge(span_count_state) AS spans, countIfMerge(error_count_state) AS errors,
       sumMerge(duration_sum_state) / nullIf(countMerge(span_count_state), 0) / 1e6 AS avg_ms,
       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms,
       groupUniqArray(5)(host_name) AS pods
FROM messaging_caller_summary_5m
WHERE time_bucket >= ? AND time_bucket < ?` + cond.String() + `
GROUP BY msg_system, cluster, destination, service_name, role
ORDER BY destination, role, spans DESC
LIMIT ?
SETTINGS max_execution_time = 8`
}

// messagingFlowArgs — SAF: bind değerleri (messagingFlowSQL sırası).
func messagingFlowArgs(topic, service string, from, to time.Time, limit int) []any {
	args := []any{from, to}
	if topic != "" {
		args = append(args, topic)
	}
	if service != "" {
		args = append(args, from, to, service)
	}
	return append(args, limit)
}

// MessagingFlow — topic (parça) ya da servis → üretici / tüketici servisler,
// rol başına yayın / işleme süresi ve hata, pod'lar.
func (s *Store) MessagingFlow(ctx context.Context, topic, service string, from, to time.Time, limit int) ([]MessagingFlowRow, error) {
	out := []MessagingFlowRow{}
	topic, service = strings.TrimSpace(topic), strings.TrimSpace(service)
	if topic == "" && service == "" {
		return out, nil
	}
	if limit <= 0 {
		limit = 40
	}
	from, to, _ = ClampLookupWindow(from, to, lookupMVMax)
	rows, err := s.telemetryReadConn().Query(ctx, messagingFlowSQL(topic, service),
		messagingFlowArgs(topic, service, from.Truncate(5*time.Minute), to, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r MessagingFlowRow
		var avg *float64
		if err := rows.Scan(&r.System, &r.Broker, &r.Destination, &r.Service, &r.Role, &r.Spans, &r.Errors, &avg, &r.P95Ms, &r.Pods); err != nil {
			return nil, err
		}
		r.AvgMs, r.P95Ms = finiteOr0(safeF(avg)), finiteOr0(r.P95Ms)
		out = append(out, r)
	}
	return out, rows.Err()
}

// DownstreamEdge — servisin çağırdığı hedef (topology_edges_5m MV).
type DownstreamEdge struct {
	Target string  `json:"target"`
	Kind   string  `json:"kind"` // service | db | queue | external
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avg_ms"`
	P99Ms  float64 `json:"p99_ms_max_bucket"`
}

// downstreamEdgesSQL — SAF.
const downstreamEdgesSQL = `SELECT child_node, sum(calls) AS c, sum(errors) AS e,
       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms, max(p99_ms) AS p99
FROM topology_edges_5m FINAL
WHERE time_bucket >= ? AND time_bucket <= ? AND parent_service = ? AND ` + TopoCallEdgeFilterSQL + `
GROUP BY child_node
HAVING c > 0
ORDER BY c DESC
LIMIT ?
SETTINGS max_execution_time = 5`

// DownstreamEdges — servisin aşağı bağımlılıkları; çağrı sayısı azalan.
func (s *Store) DownstreamEdges(ctx context.Context, service string, from, to time.Time, limit int) ([]DownstreamEdge, error) {
	out := []DownstreamEdge{}
	if strings.TrimSpace(service) == "" {
		return out, nil
	}
	if limit <= 0 || limit > 30 {
		limit = 15
	}
	from, to, _ = ClampLookupWindow(from, to, lookupMVMax)
	rows, err := s.conn.Query(ctx, downstreamEdgesSQL, from.Truncate(5*time.Minute), to, service, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var node string
		var e DownstreamEdge
		var avg *float64
		if err := rows.Scan(&node, &e.Calls, &e.Errors, &avg, &e.P99Ms); err != nil {
			return nil, err
		}
		kind, name := TopologyNodeIdentity(node)
		if kind == "" {
			kind = NodeKindService
		}
		if name == "" || name == service {
			continue
		}
		e.Target, e.Kind, e.AvgMs, e.P99Ms = name, kind, finiteOr0(safeF(avg)), finiteOr0(e.P99Ms)
		out = append(out, e)
	}
	return out, rows.Err()
}
