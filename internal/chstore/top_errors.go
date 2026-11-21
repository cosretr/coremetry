package chstore

// top_errors.go — v0.10.1256 (operatör: "en yavaş trace'ler tüm filoda
// anlamsız; en hatalı servisler, sonra onların operasyonları"). Hata sayısına
// göre sıralama, MV-first: service_summary_5m / operation_summary_5m.

import (
	"context"
	"strings"
	"time"
)

// ErrorRank — bir servisin ya da operasyonun penceredeki hata sayımı.
type ErrorRank struct {
	Name   string `json:"name"`
	Spans  uint64 `json:"spans"`
	Errors uint64 `json:"errors"`
}

// topErrorServicesSQL — SAF: n>0 → service_name IN (n tutucu).
func topErrorServicesSQL(n int) string {
	in := ""
	if n > 0 {
		in = " AND service_name IN (" + strings.TrimSuffix(strings.Repeat("?,", n), ",") + ")"
	}
	return `SELECT service_name, countMerge(span_count_state) AS s, countIfMerge(error_count_state) AS e
		FROM service_summary_5m
		WHERE time_bucket >= ? AND time_bucket < ?` + in + `
		GROUP BY service_name
		HAVING e > 0
		ORDER BY e DESC, service_name
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// topErrorOpsSQL — SAF.
func topErrorOpsSQL() string {
	return `SELECT name, countMerge(span_count_state) AS s, countIfMerge(error_count_state) AS e
		FROM operation_summary_5m
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		GROUP BY name
		HAVING e > 0
		ORDER BY e DESC, name
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// TopErrorServices — en çok hata üreten servisler (services boş = filo).
func (s *Store) TopErrorServices(ctx context.Context, from, to time.Time, services []string, limit int) ([]ErrorRank, error) {
	args := []any{alignBucketStart(from), to}
	for _, sv := range services {
		args = append(args, sv)
	}
	args = append(args, limit)
	return s.scanErrorRanks(ctx, s.weighSQL(topErrorServicesSQL(len(services)), from, "service_summary_5m"), args) // v0.10.2435 — sampling-weight Phase 2
}

// TopErrorOperations — bir servisin en çok hata üreten operasyonları.
func (s *Store) TopErrorOperations(ctx context.Context, service string, from, to time.Time, limit int) ([]ErrorRank, error) {
	return s.scanErrorRanks(ctx, s.weighSQL(topErrorOpsSQL(), from, "operation_summary_5m"), []any{service, alignBucketStart(from), to, limit}) // v0.10.2435 — sampling-weight Phase 2
}

func (s *Store) scanErrorRanks(ctx context.Context, q string, args []any) ([]ErrorRank, error) {
	rows, err := s.telemetryReadConn().Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ErrorRank
	for rows.Next() {
		var r ErrorRank
		if err := rows.Scan(&r.Name, &r.Spans, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
