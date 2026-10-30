package chstore

// cosre_briefings.go — CoSRE Login Briefing Faz 1: kartın deposu. DDL
// store.go (tables dilimi); takım × env başına tek satır, RMT(version) + FINAL.
// Yük (payload) briefing.Briefing'in JSON'u — şema kartın şekline bağlı değil.

import (
	"context"
	"time"
)

// CosreBriefingRow — cosre_briefings satırı.
type CosreBriefingRow struct {
	TeamKey     string
	Env         string
	Team        string
	BriefingID  string
	ScopeHash   string
	WindowS     uint32
	GeneratedAt time.Time
	Silence     bool
	Payload     string
}

// PutCosreBriefing — kartı yazar (aynı (team_key, env) önceki sürümü FINAL'de düşer).
func (s *Store) PutCosreBriefing(ctx context.Context, r CosreBriefingRow) error {
	if r.GeneratedAt.IsZero() {
		r.GeneratedAt = time.Now()
	}
	var silence uint8
	if r.Silence {
		silence = 1
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO cosre_briefings (team_key, env, team, briefing_id, scope_hash, window_s, generated_at, silence, payload)`)
	if err != nil {
		return err
	}
	if err := batch.Append(r.TeamKey, r.Env, r.Team, r.BriefingID, r.ScopeHash, r.WindowS, r.GeneratedAt.UTC(), silence, r.Payload); err != nil {
		return err
	}
	return batch.Send()
}

// GetCosreBriefing — takım × env kartı; notBefore'dan eski kart YOK sayılır
// (ok=false). Nokta okuması: ORDER BY anahtarı tam eşleşme.
func (s *Store) GetCosreBriefing(ctx context.Context, teamKey, env string, notBefore time.Time) (CosreBriefingRow, bool, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT team_key, env, team, briefing_id, scope_hash, window_s, generated_at, silence, payload
		FROM cosre_briefings FINAL
		WHERE team_key = ? AND env = ? AND generated_at >= ?
		LIMIT 1
		SETTINGS max_execution_time = 3`, teamKey, env, chDateTime64Arg(notBefore))
	if err != nil {
		return CosreBriefingRow{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return CosreBriefingRow{}, false, rows.Err()
	}
	var r CosreBriefingRow
	var silence uint8
	if err := rows.Scan(&r.TeamKey, &r.Env, &r.Team, &r.BriefingID, &r.ScopeHash, &r.WindowS, &r.GeneratedAt, &silence, &r.Payload); err != nil {
		return CosreBriefingRow{}, false, err
	}
	r.Silence = silence == 1
	return r, true, nil
}
