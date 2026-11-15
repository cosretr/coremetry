package chstore

import (
	"context"
	"fmt"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_workloads.go — v0.10.2262 (Entity S6 parça 3; entity-s6-cost-note.md
// bulgu 2): açık iş yükü adlarının okuması. workload_revision_activity_1m
// (0012; ORDER BY cluster, k8s_namespace, workload, revision, service_name,
// bucket) son 1 saat, GROUP BY anahtarın ilk beş kolonu — kova düşer.
// workload_kind SimpleAggregateFunction(anyLast). Zaman sınırlı + LIMIT +
// max_execution_time; ham spans yok. Tablo yoksa entity.ErrExplicitWorkloadsAbsent.

// entityExplicitWorkloadsSQL — SAF; testte pinli.
const entityExplicitWorkloadsSQL = `
		SELECT cluster, k8s_namespace, workload, anyLast(workload_kind) AS kind, revision, service_name
		FROM workload_revision_activity_1m
		WHERE bucket >= toStartOfMinute(?) AND bucket <= now()
		  AND workload != '' AND k8s_namespace != ''
		GROUP BY cluster, k8s_namespace, workload, revision, service_name
		LIMIT ?
		SETTINGS max_execution_time = 10`

// EntityExplicitWorkloads — son `since`'ten beri (cluster, ns, iş yükü,
// revizyon, servis) satırları. truncated: LIMIT doldu.
func (s *Store) EntityExplicitWorkloads(ctx context.Context, since time.Time, limit int) ([]entity.ExplicitWorkload, bool, error) {
	if limit <= 0 || limit > entity.MaxExplicitWorkloadRows {
		limit = entity.MaxExplicitWorkloadRows
	}
	rows, err := s.conn.Query(ctx, entityExplicitWorkloadsSQL, since, limit)
	if err != nil {
		if isUnknownTableOrColumn(err) {
			return nil, false, fmt.Errorf("%w: %v", entity.ErrExplicitWorkloadsAbsent, err)
		}
		return nil, false, fmt.Errorf("entity explicit workloads: %w", err)
	}
	defer rows.Close()
	var out []entity.ExplicitWorkload
	for rows.Next() {
		var w entity.ExplicitWorkload
		if err := rows.Scan(&w.ClusterValue, &w.Namespace, &w.Name, &w.Kind, &w.Revision, &w.Service); err != nil {
			return nil, false, err
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return out, len(out) >= limit, nil
}
