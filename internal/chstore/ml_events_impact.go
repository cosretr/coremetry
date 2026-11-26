package chstore

// ml_events_impact.go — v0.10.2482 (Problems Etki sütunu): /api/inbox'un
// etki özeti için TEK toplu okuma. Liste, standardın trafik okumasının
// yanında bu okumayı derleme başına bir kez yapar (satır başına değil):
// etki kuralı Problem'lerinin servisleri × etki dedektörleri ya da grup
// Problem'lerinin lider olay kimlikleri. Yalnız ml_events (olay satırları,
// ölçü noktası değil); last_seen ile zaman sınırlı, LIMIT + max_execution_time.

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// MLImpactLeadCap — toplu okumanın satır tavanı (en yeni olaylar önce).
const MLImpactLeadCap = 2000

// mlImpactLeadSQL — SAF: WHERE gövdesi + argümanlar. Boş küme = okuma yok
// (ok=false).
func mlImpactLeadSQL(services, detectors, ids []string, since time.Time) (string, []any, bool) {
	var ors []string
	args := []any{chDateTime64Arg(since)}
	if len(services) > 0 && len(detectors) > 0 {
		ors = append(ors, "(service IN ? AND detector IN ?)")
		args = append(args, services, detectors)
	}
	if len(ids) > 0 {
		ors = append(ors, "id IN ?")
		args = append(args, ids)
	}
	if len(ors) == 0 {
		return "", nil, false
	}
	args = append(args, MLImpactLeadCap)
	q := fmt.Sprintf(`
		SELECT %s
		FROM ml_events FINAL
		WHERE last_seen >= ? AND (%s)
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`, strings.Join(mlEventBaseCols, ", "), strings.Join(ors, " OR "))
	return q, args, true
}

// ListImpactLeadEvents — etki özeti adayları: (servis ∈ services VE
// dedektör ∈ detectors) YA DA kimlik ∈ ids, last_seen ≥ since. Karar
// (verdict) okunmaz — özet yalnız kanıtı okur.
func (s *Store) ListImpactLeadEvents(ctx context.Context, services, detectors, ids []string, since time.Time) ([]MLEvent, error) {
	q, args, ok := mlImpactLeadSQL(services, detectors, ids, since)
	if !ok {
		return nil, nil
	}
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLEvent
	for rows.Next() {
		var e MLEvent
		dest, done := mlEventScan(&e, false)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		done()
		out = append(out, e)
	}
	return out, rows.Err()
}
