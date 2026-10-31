package chstore

// route_red.go — v0.10.1738 (operatör onayı, mockup "Endpoint seç" ikinci
// adım): CoSRE v2 rota incelemesinin grafikleri VictoriaMetrics yapılandırılmamış
// kurulumda buradan — ham spans DEĞİL, spanmetrics_1m (rota boyutu taşıyan
// span rollup'ı; giriş span'leri = kind client/producer dışı).
//
//   RouteREDSeries — (servis, http_route) için kova başına çağrı · hata ·
//     p50 · p95. Kova = stepS (60'ın katı; MV tanesi 1 dk). Hata = span
//     status error (sunucu span'inde OTel semconv: 5xx).
//   SlowestCallees — servisin en yavaş aşağı bağımlılıkları (topology_edges_5m,
//     ortalama süre azalan); rota incelemesinin "<x>'u incele" çipi.
//
// İkisi de LIMIT + max_execution_time + zaman sınırlı WHERE (CLAUDE.md).
// spanmetrics_1m ortam boyutu taşımaz: env süzgeci burada UYGULANMAZ, çağıran
// bunu notla söyler (sessiz genişleme yok).

import (
	"context"
	"time"
)

// RoutePoint — tek kova.
type RoutePoint struct {
	TimeMs int64
	Calls  uint64
	Errors uint64
	P50Ms  float64
	P95Ms  float64
}

// routeREDMaxBuckets — kova tavanı (24 sa / 1 dk = 1440).
const routeREDMaxBuckets = 1500

// routeREDSQL — SAF: sorgu + bağ argümanları (yer tutucu sırası metindeki
// görünüş sırası: SELECT'teki intDiv, sonra WHERE). from dakikaya hizalı olmalı.
func routeREDSQL(service, route string, from, to time.Time, stepS int) (string, []any) {
	if stepS < 60 {
		stepS = 60
	}
	stepS -= stepS % 60
	sql := `
		SELECT intDiv(toUnixTimestamp(time_bucket) - ?, ?)                               AS b,
		       countMerge(calls_state)                                                   AS calls,
		       countIfMerge(error_state)                                                 AS errors,
		       arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms,
		       arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p95_ms
		FROM spanmetrics_1m
		WHERE time_bucket >= ? AND time_bucket < ?
		  AND service_name = ?
		  AND http_route = ?
		  AND kind NOT IN ('client', 'producer')
		GROUP BY b
		ORDER BY b
		LIMIT ?
		SETTINGS max_execution_time = 10`
	return sql, []any{from.Unix(), stepS, from, to, service, route, routeREDMaxBuckets}
}

// RouteREDSeries — rota RED serisi (spanmetrics_1m). Boş rota / servis → nil.
func (s *Store) RouteREDSeries(ctx context.Context, service, route string, from, to time.Time, stepS int) ([]RoutePoint, error) {
	if service == "" || route == "" || !to.After(from) {
		return nil, nil
	}
	from = from.Truncate(time.Minute)
	if stepS < 60 {
		stepS = 60
	}
	stepS -= stepS % 60
	sql, args := routeREDSQL(service, route, from, to, stepS)
	rows, err := s.telemetryReadConn().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoutePoint
	for rows.Next() {
		var b int64
		var p RoutePoint
		var p50, p95 *float64
		if err := rows.Scan(&b, &p.Calls, &p.Errors, &p50, &p95); err != nil {
			return nil, err
		}
		p.TimeMs = (from.Unix() + b*int64(stepS)) * 1000
		p.P50Ms, p.P95Ms = safeF(p50), safeF(p95)
		out = append(out, p)
	}
	return out, rows.Err()
}

// CalleeLatency — servisin bir aşağı bağımlılığı.
type CalleeLatency struct {
	Callee string
	Kind   string // NodeKind* (service | db | queue | external)
	Calls  uint64
	AvgMs  float64
}

// slowestCalleesSQL — SAF.
func slowestCalleesSQL(service string, from, to time.Time, limit int) (string, []any) {
	sql := `
		SELECT child_node,
		       sum(calls)                                 AS c,
		       sum(sum_duration_ns) / nullIf(sum(calls), 0) / 1e6 AS avg_ms
		FROM topology_edges_5m FINAL
		WHERE time_bucket >= ? AND time_bucket <= ?
		  AND parent_service = ?
		  AND ` + TopoCallEdgeFilterSQL + `
		GROUP BY child_node
		HAVING c > 0
		ORDER BY avg_ms DESC, c DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`
	return sql, []any{from.Truncate(5 * time.Minute), to, service, limit}
}

// SlowestCallees — ortalama süre azalan aşağı bağımlılıklar (≤ limit).
func (s *Store) SlowestCallees(ctx context.Context, service string, from, to time.Time, limit int) ([]CalleeLatency, error) {
	if service == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	sql, args := slowestCalleesSQL(service, from, to, limit)
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalleeLatency
	for rows.Next() {
		var node string
		var c CalleeLatency
		var avg *float64
		if err := rows.Scan(&node, &c.Calls, &avg); err != nil {
			return nil, err
		}
		kind, name := TopologyNodeIdentity(node)
		if kind == "" {
			kind = NodeKindService
		}
		c.Callee, c.Kind, c.AvgMs = name, kind, safeF(avg)
		if c.Callee == "" || c.Callee == service {
			continue
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
