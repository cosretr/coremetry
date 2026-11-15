package chstore

import (
	"context"
	"time"
)

// Topology hidden-pattern config lives under the "topology_hidden" key
// in system_settings (v0.8.241, operator-requested: nodes matching the
// operator's glob list must never render). The API layer owns marshal /
// unmarshal — chstore only stores the bytes (tempo.go pattern).
const topologyHiddenKey = "topology_hidden"

// GetTopologyHiddenRaw returns the saved JSON blob for the hidden
// pattern list, or nil when none has been persisted yet (the API layer
// falls back to its empty default; api/topology_hidden_seed.go writes the
// row once on leader boot).
func (s *Store) GetTopologyHiddenRaw(ctx context.Context) ([]byte, error) {
	return s.GetSetting(ctx, topologyHiddenKey)
}

// PutTopologyHiddenRaw overwrites the saved JSON blob.
func (s *Store) PutTopologyHiddenRaw(ctx context.Context, raw []byte) error {
	return s.PutSetting(ctx, topologyHiddenKey, raw)
}

// OldestSettingWrite — system_settings'teki en eski kaydın son yazım anı
// (FINAL: anahtar başına son sürüm). ok=false: hiç kayıt yok. Topoloji
// gizli desen göçünün "mevcut kurulum mu" ölçüsü (v0.10.2268).
func (s *Store) OldestSettingWrite(ctx context.Context) (time.Time, bool, error) {
	var n uint64
	var t time.Time
	if err := s.conn.QueryRow(ctx,
		`SELECT count(), min(updated_at) FROM system_settings FINAL SETTINGS max_execution_time = 10`).Scan(&n, &t); err != nil {
		return time.Time{}, false, err
	}
	return t, n > 0, nil
}
