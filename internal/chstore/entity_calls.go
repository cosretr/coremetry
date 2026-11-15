package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_calls.go — v0.10.2240 (Entity S5, yatay eksen): syncer'ın calls
// geçişinin okuması. topology_edges_5m (servis düzeyi MV), GROUP BY
// (parent_service, child_node); RUNS_ON çocukları dışlanır. FINAL yok:
// yalnız kenarın VARLIĞI gerekir, RMT'nin birleşmemiş kopyaları sadece
// sıralama sayısını şişirir. Zaman sınırlı + LIMIT + max_execution_time.

// entityCallEdgesSQL — SAF; testte pinli.
const entityCallEdgesSQL = `
		SELECT parent_service, child_node, sum(calls) AS c
		FROM topology_edges_5m
		WHERE time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= now()
		  AND parent_service != '' AND child_node != ''
		  AND ` + TopoCallEdgeFilterSQL + `
		GROUP BY parent_service, child_node
		ORDER BY c DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`

// EntityCallEdges — son `since`'ten beri servis düzeyi kenarlar. `ext:`
// çocuğu bilinen bir servise çözülüyorsa (servis grafiğinin aynı kuralı,
// mergeExtPeerEdgesWith) kenar o servise katlanır; bilinen küme bu okumanın
// kendi servis adlarıdır (ek okuma yok). truncated: LIMIT doldu.
func (s *Store) EntityCallEdges(ctx context.Context, since time.Time, limit int) ([]entity.CallEdge, bool, error) {
	if limit <= 0 || limit > entity.MaxCallEdges {
		limit = entity.MaxCallEdges
	}
	rows, err := s.conn.Query(ctx, entityCallEdgesSQL, since, limit)
	if err != nil {
		return nil, false, fmt.Errorf("entity call edges: %w", err)
	}
	defer rows.Close()
	var out []entity.CallEdge
	for rows.Next() {
		var e entity.CallEdge
		if err := rows.Scan(&e.Parent, &e.Child, &e.Calls); err != nil {
			return nil, false, err
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return foldExtCallEdges(out), len(out) >= limit, nil
}

// foldExtCallEdges — SAF: ext:<peer> çocuğunu bilinen servise katlar
// (küme = öneksiz parent/child adları). Katlanmış kopyalar birleşir.
func foldExtCallEdges(in []entity.CallEdge) []entity.CallEdge {
	known := NewServiceNameSet()
	for _, e := range in {
		for _, n := range []string{e.Parent, e.Child} {
			if kind, _ := TopologyNodeIdentity(n); kind == "" {
				known.Add(n)
			}
		}
	}
	type pc struct{ p, c string }
	idx := map[pc]int{}
	out := make([]entity.CallEdge, 0, len(in))
	for _, e := range in {
		if strings.HasPrefix(e.Child, "ext:") {
			if name, ok := known.ResolveExtPeer(e.Child); ok {
				e.Child = name
			}
		}
		k := pc{e.Parent, e.Child}
		if i, ok := idx[k]; ok {
			out[i].Calls += e.Calls
			continue
		}
		idx[k] = len(out)
		out = append(out, e)
	}
	return out
}
