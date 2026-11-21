package chstore

// route_prefix_red.go — v0.10.1482: `/x/**` önek kipinin http_route okuması.
// CoSRE endpoint sorusu "/x/** yavaş mı" (Markdown sarmalayıcısı OLMAYAN
// sondaki `/**` ya da `/*`) önekin altındaki TÜM rotaları ister: rota başına
// satır (çağrı sırası, LIMIT'li) + önekin TOPLAMI (cur + önceki eşit pencere,
// rota sayısı). MV-first (spanmetrics_1m), giriş-noktası kind yüklemi
// routeREDQuery ile aynı (client/producer hariç), yarı-açık pencere,
// max_execution_time. p95 yalnız SATIR başına — yüzdelikler toplanmaz.

import (
	"context"
	"strings"
	"time"
)

// RoutePrefixMaxRows — rota satırı tavanı (sorgu LIMIT'i; çağıran daha azını ister).
const RoutePrefixMaxRows = 50

// RoutePrefixRow — önekin altındaki tek (servis, rota).
type RoutePrefixRow struct {
	Service string  `json:"service"`
	Route   string  `json:"route"`
	Calls   uint64  `json:"calls"`
	Errors  uint64  `json:"errors"`
	AvgMs   float64 `json:"avgMs"`
	P95Ms   float64 `json:"p95Ms"`
}

// RoutePrefixTotal — bir pencerenin önek toplamı (p95 YOK).
type RoutePrefixTotal struct {
	Calls  uint64  `json:"calls"`
	Errors uint64  `json:"errors"`
	AvgMs  float64 `json:"avgMs"`
	Routes uint64  `json:"routes"`
}

// RoutePrefixREDResult — RoutePrefixRED sonucu.
type RoutePrefixREDResult struct {
	Rows      []RoutePrefixRow `json:"rows"`
	Cur, Prev RoutePrefixTotal
}

// routePrefixWhere — SAF: önek yüklemi (önek kendisi + "önek/" ile başlayanlar).
func routePrefixWhere(service, prefix string) (string, []any) {
	prefix = strings.TrimRight(prefix, "/")
	where := "(http_route = ? OR startsWith(http_route, ?))"
	args := []any{prefix, prefix + "/"}
	if service != "" {
		where = "service_name = ? AND " + where
		args = append([]any{service}, args...)
	}
	return where + " AND kind NOT IN ('client', 'producer')", args
}

// routePrefixRowsQuery — SAF: rota başına satırlar, çağrı sırası, LIMIT.
func routePrefixRowsQuery(service, prefix string, from, to time.Time, limit int) (string, []any) {
	if limit <= 0 || limit > RoutePrefixMaxRows {
		limit = RoutePrefixMaxRows
	}
	where, args := routePrefixWhere(service, prefix)
	q := `SELECT service_name, http_route, countMerge(calls_state) AS c, countMerge(error_state),
	             if(c = 0, 0, sumMerge(duration_sum_state) / c / 1e6),
	             arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 3) / 1e6
	      FROM spanmetrics_1m
	      WHERE ` + where + `
	        AND time_bucket >= ? AND time_bucket < ?
	      GROUP BY service_name, http_route
	      ORDER BY c DESC, service_name, http_route
	      LIMIT ?
	      SETTINGS max_execution_time = 5`
	return q, append(args, from, to, limit)
}

// routePrefixTotalsQuery — SAF: cur (from..to) + prev (prevFrom..from) toplamları,
// TÜM rotalar (LIMIT yok, en çok 2 satır).
func routePrefixTotalsQuery(service, prefix string, prevFrom, from, to time.Time) (string, []any) {
	where, args := routePrefixWhere(service, prefix)
	q := `SELECT if(time_bucket >= ?, 'cur', 'prev') AS win, countMerge(calls_state) AS c, countMerge(error_state),
	             if(c = 0, 0, sumMerge(duration_sum_state) / c / 1e6), uniqExact(service_name, http_route)
	      FROM spanmetrics_1m
	      WHERE ` + where + `
	        AND time_bucket >= ? AND time_bucket < ?
	      GROUP BY win
	      LIMIT 2
	      SETTINGS max_execution_time = 5`
	return q, append(append([]any{from}, args...), prevFrom, to)
}

// RoutePrefixRED — v0.10.1482: önek altındaki rotalar (≤limit satır) + toplamlar.
// Servis boşsa tüm servisler.
func (s *Store) RoutePrefixRED(ctx context.Context, service, prefix string, from, to time.Time, limit int) (RoutePrefixREDResult, error) {
	var out RoutePrefixREDResult
	q, args := routePrefixRowsQuery(service, prefix, from, to, limit)
	q = s.weighSQL(q, from, "spanmetrics_1m") // v0.10.2435 — sampling-weight Phase 2
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var r RoutePrefixRow
		if err := rows.Scan(&r.Service, &r.Route, &r.Calls, &r.Errors, &r.AvgMs, &r.P95Ms); err != nil {
			rows.Close()
			return out, err
		}
		if r.P95Ms != r.P95Ms { // NaN
			r.P95Ms = 0
		}
		out.Rows = append(out.Rows, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	prevFrom := from.Add(-to.Sub(from))
	tq, targs := routePrefixTotalsQuery(service, prefix, prevFrom, from, to)
	tq = s.weighSQL(tq, prevFrom, "spanmetrics_1m") // v0.10.2435 — sampling-weight Phase 2
	trows, err := s.conn.Query(ctx, tq, targs...)
	if err != nil {
		return out, err
	}
	defer trows.Close()
	for trows.Next() {
		var win string
		var t RoutePrefixTotal
		if err := trows.Scan(&win, &t.Calls, &t.Errors, &t.AvgMs, &t.Routes); err != nil {
			return out, err
		}
		if win == "cur" {
			out.Cur = t
		} else {
			out.Prev = t
		}
	}
	return out, trows.Err()
}
