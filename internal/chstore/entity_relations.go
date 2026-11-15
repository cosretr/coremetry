package chstore

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_relations.go — v0.10.2232 (Entity S2): kalıcı köprü okuması
// (serves, same_as). entity_relations ORDER BY (rel_type, cluster_id,
// parent_id, …): rel_type öneki; child yönü idx_child bloom süzgecinden.
// Sınırlı: uç sayısı ≤ entityRelationsMaxIDs, LIMIT, max_execution_time,
// last_seen alt sınırı (açık satır günde bir tazelenir — entityOpenLookback).

const entityRelationsMaxIDs = 500

// entityRelationsSQL — SAF; byChild: uçlar child_id'de mi.
func entityRelationsSQL(byChild bool, at time.Time, limit int) (string, []any) {
	col := "parent_id"
	if byChild {
		col = "child_id"
	}
	v, vargs := entityValidAtSQL(at)
	return `
		SELECT rel_type, cluster_id, parent_id, child_id, source
		FROM entity_relations FINAL
		WHERE rel_type = ? AND ` + col + ` IN (?) AND ` + v + ` AND last_seen >= ?
		ORDER BY parent_id, child_id
		LIMIT ` + strconv.Itoa(limit) + `
		SETTINGS max_execution_time = 10`, vargs
}

// EntityRelationsFor — rel tipindeki geçerli kenarlar (at sıfır = açık);
// ids parent (byChild=false) ya da child uçları. limit 1..5000.
func (s *Store) EntityRelationsFor(ctx context.Context, rel string, ids []string, byChild bool, at time.Time, limit int) ([]entity.Relation, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > entityRelationsMaxIDs {
		ids = ids[:entityRelationsMaxIDs]
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	sql, vargs := entityRelationsSQL(byChild, at, limit)
	floor := time.Now()
	if !at.IsZero() {
		floor = at
	}
	args := append([]any{rel, ids}, vargs...)
	args = append(args, floor.Add(-entityOpenLookback))
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("entity relations %s: %w", rel, err)
	}
	defer rows.Close()
	var out []entity.Relation
	for rows.Next() {
		var r entity.Relation
		if err := rows.Scan(&r.Type, &r.ClusterID, &r.ParentID, &r.ChildID, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
