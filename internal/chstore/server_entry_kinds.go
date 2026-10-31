package chstore

// server_entry_kinds.go — v0.10.1727 (CoSRE Login Briefing kalem 2, karma
// HTTP/gRPC takım): servis başına SUNUCU giriş span'lerinin rota taşıyan
// (HTTP) ve rota taşımayan (gRPC / diğer RPC) çağrı sayıları. Briefing hangi
// VM RED ailesini (HTTP / gRPC) hangi servis kümesiyle okuyacağını buradan
// seçer — servis başına VM keşif sorgusu YOK. MV okuması (spanmetrics_1m;
// ORDER BY service_name önekli), ham spans DEĞİL.

import (
	"context"
	"fmt"
	"time"
)

// ServerEntryCalls — servisin pencere içi sunucu giriş çağrıları.
type ServerEntryCalls struct {
	HTTP  uint64 // kind='server' AND http_route != ''
	Other uint64 // kind='server' AND http_route = '' (gRPC, diğer RPC)
}

// serverEntryKindsSQL — SAF: zaman sınırlı, LIMIT + max_execution_time.
const serverEntryKindsSQL = `
	SELECT service_name,
	       countMergeIf(calls_state, http_route != '') AS http_calls,
	       countMergeIf(calls_state, http_route = '')  AS other_calls
	FROM %s
	WHERE time_bucket >= ? AND time_bucket < ?
	  AND service_name IN (?)
	  AND kind = 'server'
	GROUP BY service_name
	LIMIT 1000
	SETTINGS max_execution_time = 10`

// ServerEntryKinds — services için [from, to) sunucu giriş çağrıları.
// Satırı olmayan servis haritada yok (pencerede sunucu span'i yok).
func (s *Store) ServerEntryKinds(ctx context.Context, services []string, from, to time.Time) (map[string]ServerEntryCalls, error) {
	out := map[string]ServerEntryCalls{}
	if len(services) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, fmt.Sprintf(serverEntryKindsSQL, s.spanmetricsSourceFor("spanmetrics_1m")),
		chDateTimeArg(from), chDateTimeArg(to), services)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var svc string
		var c ServerEntryCalls
		if err := rows.Scan(&svc, &c.HTTP, &c.Other); err != nil {
			return nil, err
		}
		out[svc] = c
	}
	return out, rows.Err()
}
