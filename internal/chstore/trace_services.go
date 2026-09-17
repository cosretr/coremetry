package chstore

// trace_services.go — v0.10.768 (Oracle test özeti). Trace id → Coremetry
// servisi: kök span'ın servisi, kök yoksa herhangi bir span'ınki. "Bu trace
// Coremetry'de var mı, hangi servis?" sorusu için; ids tavanlı, pencere
// [from, to], LIMIT + bütçe. Yalnız spans (telemetri) → telemetryReadConn.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const traceServicesMaxIDs = 200

// traceServicesSQL — SAF: iki zaman sınırı + IN listesi + LIMIT + bütçe.
func traceServicesSQL(n int) string {
	holders := strings.TrimSuffix(strings.Repeat("?,", n), ",")
	return fmt.Sprintf(`
		SELECT trace_id,
		       anyIf(service_name, parent_id = '') AS root_svc,
		       any(service_name)                  AS any_svc
		FROM spans
		WHERE time >= ? AND time <= ?
		  AND trace_id IN (%s)
		GROUP BY trace_id
		LIMIT %d
		SETTINGS max_execution_time = 5`, holders, traceServicesMaxIDs)
}

// TraceServicesByIDs — ids ≤200 (fazlası kırpılır). Bulunmayan id haritada
// yoktur; bulunan ama servissiz (olmamalı) "" ile döner.
func (s *Store) TraceServicesByIDs(ctx context.Context, ids []string, from, to time.Time) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > traceServicesMaxIDs {
		ids = ids[:traceServicesMaxIDs]
	}
	args := make([]any, 0, len(ids)+2)
	args = append(args, from, to)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.telemetryReadConn().Query(ctx, traceServicesSQL(len(ids)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, root, anyS string
		if err := rows.Scan(&id, &root, &anyS); err != nil {
			return nil, err
		}
		if root == "" {
			root = anyS
		}
		out[id] = root
	}
	return out, rows.Err()
}
