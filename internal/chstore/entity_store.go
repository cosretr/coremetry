package chstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/cilcenk/coremetry/internal/entity"
)

// entity_store.go — K8s ENTITY KATMANI CH kapısı (v0.10.129, design §2/§3).
//
// entity.Store'un chstore uygulaması + system_settings["entity_layer"]
// blob kapısı. Yazım: tam-satır INSERT (ReplacingMergeTree(version) —
// invariant #4: her alan taşınır), async_insert dokunulmaz (PrepareBatch
// batch, entityApplyBatchRows parçaları). Okuma: FINAL, cluster_id
// + valid_to = 0 + last_seen zaman sınırı + LIMIT (ev kuralı: her yeni CH
// sorgusu zaman filtresi + limit).

const entitySettingsKey = entity.SettingsKey

func (s *Store) GetEntitySettingsRaw(ctx context.Context) ([]byte, error) {
	return s.GetSetting(ctx, entitySettingsKey)
}

func (s *Store) PutEntitySettingsRaw(ctx context.Context, raw []byte) error {
	return s.PutSetting(ctx, entitySettingsKey, raw)
}

// entityOpenLookback — açık ömür okumasının zaman sınırı. Kalıcı satır
// günde bir tazelenir (entity.RefreshEvery); 60 g dışında last_seen'i
// olan "açık" satır fiilen ölü, diff onu görmez.
const entityOpenLookback = 60 * 24 * time.Hour

// entityOpenLimit — kalıcı katmanın açık satır tavanı (cluster başına;
// beklenen ≤ ~90K, entity-storage-cost-note §4 C).
const entityOpenLimit = 200_000

// entityOpenEntitiesSQL — v0.10.2225: YALNIZ kalıcı tipler. entity_type
// ORDER BY'ın ilk kolonu: pod/container satırlarının (S1 mirası, TTL'e
// bırakıldı — E3) granülleri birincil anahtarla elenir; FINAL yalnız
// kalıcı aralıkta birleştirir. Saf; testte pinli.
func entityOpenEntitiesSQL() string {
	return `
		SELECT entity_type, entity_id, namespace, name, uid, parent_id, label_keys, label_values, source,
		       valid_from, last_seen
		FROM entities FINAL
		WHERE entity_type IN (?) AND cluster_id = ? AND valid_to = toDateTime(0) AND last_seen >= ?
		LIMIT ` + strconv.Itoa(entityOpenLimit) + `
		SETTINGS max_execution_time = 10`
}

// entityOpenRelationsSQL — kalıcı parent kenarları (cluster→node,
// cluster→ns, ns→wl) + S2 köprüleri (serves, same_as; v0.10.2232) + S5
// calls (v0.10.2240; yalnız TopologyClusterID altında yazılır) + S6
// host→svc runs (v0.10.2260; yalnız `host:` ebeveynli — S1'den kalan açık
// pod→svc runs satırları okunmaz, TTL'e bırakıldı): rel_type + cluster_id
// + parent_id öneki birincil anahtardan; runs_on / wl→pod aralıkları
// okunmaz. Saf; pinli.
func entityOpenRelationsSQL() string {
	return `
		SELECT rel_type, parent_id, child_id, source, valid_from, last_seen
		FROM entity_relations FINAL
		WHERE rel_type IN ('parent', 'serves', 'same_as', 'calls', 'runs') AND cluster_id = ?
		  AND (rel_type != 'runs' OR startsWith(parent_id, 'host:'))
		  AND (rel_type != 'parent' OR ((startsWith(parent_id, 'cluster:') OR startsWith(parent_id, 'ns:'))
		  AND NOT startsWith(child_id, 'pod:') AND NOT startsWith(child_id, 'ctr:')))
		  AND valid_to = toDateTime(0) AND last_seen >= ?
		LIMIT ` + strconv.Itoa(entityOpenLimit) + `
		SETTINGS max_execution_time = 10`
}

// EntityOpenLifetimes — cluster'ın KALICI katmandaki açık ömürleri + tam
// alanları (invariant #4: tazeleme/kapanış CH'deki satırı taşır).
// RowsScanned: sorgu ilerlemesinden gerçek okunan satır (tick günlüğü).
func (s *Store) EntityOpenLifetimes(ctx context.Context, cid string) (entity.OpenSet, error) {
	out := entity.OpenSet{Lifetimes: map[string]entity.Lifetime{}, Entities: map[string]entity.Entity{}, Relations: map[string]entity.Relation{}}
	var scanned atomic.Int64
	pctx := clickhouse.Context(ctx, clickhouse.WithProgress(func(p *clickhouse.Progress) { scanned.Add(int64(p.Rows)) }))
	since := time.Now().Add(-entityOpenLookback)
	rows, err := s.conn.Query(pctx, entityOpenEntitiesSQL(), entity.StableTypes, cid, since)
	if err != nil {
		return out, fmt.Errorf("entity open lifetimes: %w", err)
	}
	for rows.Next() {
		var e entity.Entity
		var keys, vals []string
		var from, last time.Time
		if err := rows.Scan(&e.Type, &e.ID, &e.Namespace, &e.Name, &e.UID, &e.ParentID, &keys, &vals, &e.Source, &from, &last); err != nil {
			rows.Close()
			return out, err
		}
		e.ClusterID = cid
		e.Labels = make(map[string]string, len(keys))
		for i, k := range keys {
			if i < len(vals) {
				e.Labels[k] = vals[i]
			}
		}
		out.Entities[e.ID] = e
		out.Lifetimes[e.ID] = entity.Lifetime{ID: e.ID, UID: e.UID, ValidFrom: from, LastSeen: last}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rrows, err := s.conn.Query(pctx, entityOpenRelationsSQL(), cid, since)
	if err != nil {
		return out, fmt.Errorf("entity open relations: %w", err)
	}
	defer rrows.Close()
	for rrows.Next() {
		var r entity.Relation
		var from, last time.Time
		if err := rrows.Scan(&r.Type, &r.ParentID, &r.ChildID, &r.Source, &from, &last); err != nil {
			return out, err
		}
		r.ClusterID = cid
		key := "rel|" + r.Type + "|" + r.ParentID + "|" + r.ChildID
		out.Relations[key] = r
		out.Lifetimes[key] = entity.Lifetime{ID: key, ValidFrom: from, LastSeen: last}
	}
	if err := rrows.Err(); err != nil {
		return out, err
	}
	out.RowsScanned = scanned.Load()
	return out, nil
}

// entityApplyBatchRows — v0.10.2214 (S1): tek INSERT'in satır tavanı.
// v0.10.2225'ten beri yalnız kalıcı katman (ilk tick / günlük tazeleme
// ~27K satır) — sınırlı parçalar, async_insert dokunulmaz.
const entityApplyBatchRows = 10_000

// EntityApply — tam-satır yazım (varlıklar + ilişkiler), parça parça.
func (s *Store) EntityApply(ctx context.Context, cid string, rows []entity.EntityRow, rels []entity.RelationRow) error {
	for i := 0; i < len(rows); i += entityApplyBatchRows {
		if err := s.entityApplyRows(ctx, cid, rows[i:min(i+entityApplyBatchRows, len(rows))]); err != nil {
			return err
		}
	}
	for i := 0; i < len(rels); i += entityApplyBatchRows {
		if err := s.entityApplyRels(ctx, cid, rels[i:min(i+entityApplyBatchRows, len(rels))]); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) entityApplyRows(ctx context.Context, cid string, rows []entity.EntityRow) error {
	b, err := s.conn.PrepareBatch(ctx, `INSERT INTO entities
		(entity_type, cluster_id, entity_id, valid_from, valid_to, namespace, name, uid, parent_id,
		 label_keys, label_values, source, first_seen, last_seen, stale)`)
	if err != nil {
		return fmt.Errorf("entities batch: %w", err)
	}
	for _, r := range rows {
		stale := uint8(0)
		if r.Stale {
			stale = 1
		}
		if err := b.Append(r.Type, cid, r.ID, r.ValidFrom, zeroOr(r.ValidTo), r.Namespace, r.Name, r.UID, r.ParentID,
			r.LabelKeys, r.LabelValues, r.Source, r.FirstSeen, r.LastSeen, stale); err != nil {
			return err
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("entities send: %w", err)
	}
	return nil
}

func (s *Store) entityApplyRels(ctx context.Context, cid string, rels []entity.RelationRow) error {
	b, err := s.conn.PrepareBatch(ctx, `INSERT INTO entity_relations
		(rel_type, cluster_id, parent_id, child_id, valid_from, valid_to, last_seen, source)`)
	if err != nil {
		return fmt.Errorf("entity_relations batch: %w", err)
	}
	for _, r := range rels {
		if err := b.Append(r.Type, cid, r.ParentID, r.ChildID, r.ValidFrom, zeroOr(r.ValidTo), r.LastSeen, r.Source); err != nil {
			return err
		}
	}
	if err := b.Send(); err != nil {
		return fmt.Errorf("entity_relations send: %w", err)
	}
	return nil
}

// zeroOr — Go sıfır zamanı → CH DateTime 0 (1970-01-01), "açık" işareti.
func zeroOr(t time.Time) time.Time {
	if t.IsZero() {
		return time.Unix(0, 0).UTC()
	}
	return t
}

// EntityRecordRun — sync_runs satırı.
func (s *Store) EntityRecordRun(ctx context.Context, run entity.Run) error {
	b, err := s.conn.PrepareBatch(ctx, `INSERT INTO entity_sync_runs
		(cluster_id, started_at, finished_at, status, entities_written, relations_written, closed,
		 unmapped_keys, unmapped_counts, thanos_ms, ch_ms, error)`)
	if err != nil {
		return err
	}
	keys, counts := run.UnmappedKeys, run.UnmappedCounts
	if keys == nil {
		keys = []string{}
	}
	if counts == nil {
		counts = []uint32{}
	}
	fin := run.FinishedAt
	if fin.IsZero() {
		fin = time.Now()
	}
	if err := b.Append(run.ClusterID, run.StartedAt, fin, run.Status, uint32(run.EntitiesWritten), uint32(run.RelationsWritten),
		uint32(run.Closed), keys, counts, uint32(run.ThanosMs), uint32(run.CHMs), run.Error); err != nil {
		return err
	}
	return b.Send()
}

// EntitySyncRuns — admin görünümü: cluster başına SON koşu + son N koşu.
func (s *Store) EntitySyncRuns(ctx context.Context, since time.Time, limit int) ([]entity.Run, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.conn.Query(ctx, `
		SELECT cluster_id, started_at, finished_at, status, entities_written, relations_written, closed,
		       unmapped_keys, unmapped_counts, thanos_ms, ch_ms, error
		FROM entity_sync_runs FINAL
		WHERE started_at >= ?
		ORDER BY started_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []entity.Run{}
	for rows.Next() {
		var r entity.Run
		var ew, rw, cl, tm, cm uint32
		if err := rows.Scan(&r.ClusterID, &r.StartedAt, &r.FinishedAt, &r.Status, &ew, &rw, &cl,
			&r.UnmappedKeys, &r.UnmappedCounts, &tm, &cm, &r.Error); err != nil {
			return nil, err
		}
		r.EntitiesWritten, r.RelationsWritten, r.Closed, r.ThanosMs, r.CHMs = int(ew), int(rw), int(cl), int(tm), int(cm)
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntitySeenRecent — span geçişi: entity_seen_5m'in son dilimi (zaman
// sınırlı + LIMIT; kova bazlı, pencere ≤ 1 saat → prod'da ≤ 12 kova ×
// aktif (pod, servis)).
func (s *Store) EntitySeenRecent(ctx context.Context, since time.Time) ([]entity.SeenRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT cluster, k8s_namespace, k8s_pod, anyLast(k8s_node) AS node, service_name,
		       toInt64(countMerge(span_count_state)) AS spans,
		       minMerge(first_seen_state) AS first_seen, maxMerge(last_seen_state) AS last_seen
		FROM entity_seen_5m
		WHERE time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= now()
		GROUP BY cluster, k8s_namespace, k8s_pod, service_name
		ORDER BY last_seen DESC
		LIMIT 200000
		SETTINGS max_execution_time = 10`, since)
	if err != nil {
		return nil, fmt.Errorf("entity_seen recent: %w", err)
	}
	defer rows.Close()
	out := []entity.SeenRow{}
	for rows.Next() {
		var r entity.SeenRow
		var spans int64
		if err := rows.Scan(&r.ClusterValue, &r.Namespace, &r.Pod, &r.Node, &r.Service, &spans, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		r.Spans = int(spans)
		out = append(out, r)
	}
	return out, rows.Err()
}

// EntitySeenRecentFor — v0.10.141: yalnız bir span cluster değeri (backfill).
func (s *Store) EntitySeenRecentFor(ctx context.Context, since time.Time, clusterValue string) ([]entity.SeenRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT cluster, k8s_namespace, k8s_pod, anyLast(k8s_node) AS node, service_name,
		       toInt64(countMerge(span_count_state)) AS spans,
		       minMerge(first_seen_state) AS first_seen, maxMerge(last_seen_state) AS last_seen
		FROM entity_seen_5m
		WHERE time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= now() AND cluster = ?
		GROUP BY cluster, k8s_namespace, k8s_pod, service_name
		ORDER BY last_seen DESC
		LIMIT 200000
		SETTINGS max_execution_time = 10`, since, clusterValue)
	if err != nil {
		return nil, fmt.Errorf("entity_seen recent: %w", err)
	}
	defer rows.Close()
	out := []entity.SeenRow{}
	for rows.Next() {
		var r entity.SeenRow
		var spans int64
		if err := rows.Scan(&r.ClusterValue, &r.Namespace, &r.Pod, &r.Node, &r.Service, &spans, &r.FirstSeen, &r.LastSeen); err != nil {
			return nil, err
		}
		r.Spans = int(spans)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── v0.10.141 — span cluster değerleri (otomatik eşleme brief'i) ──
// entity_seen_5m varsa son `since`'ten beri değer başına span sayısı +
// ilk/son görülme (MV, ucuz). MV yoksa (0011 uygulanmamış) spans son 1 saat
// (zaman sınırlı, LIMIT, max_execution_time) + Source ilanı.
type SeenClusterValue struct {
	Value     string    `json:"value"`
	Spans     int64     `json:"spans"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

type SeenClusterValues struct {
	Rows   []SeenClusterValue `json:"rows"`
	Source string             `json:"source"` // "entity_seen_5m" | "spans-1h"
	Since  time.Time          `json:"since"`
}

func (s *Store) EntitySeenClusterValues(ctx context.Context, since time.Time) (SeenClusterValues, error) {
	out := SeenClusterValues{Rows: []SeenClusterValue{}, Source: "entity_seen_5m", Since: since}
	rows, err := s.conn.Query(ctx, `
		SELECT cluster, toInt64(countMerge(span_count_state)) AS spans, min(time_bucket), max(time_bucket)
		FROM entity_seen_5m
		WHERE time_bucket >= toStartOfFiveMinute(?) AND time_bucket <= now() AND cluster != ''
		GROUP BY cluster
		ORDER BY spans DESC
		LIMIT 200
		SETTINGS max_execution_time = 10`, since)
	if err != nil {
		if !isUnknownTableOrColumn(err) {
			return out, fmt.Errorf("span cluster values: %w", err)
		}
		out.Source, out.Since = "spans-1h", time.Now().Add(-time.Hour)
		// Ana bağlantı: bu dosya state tabloları okur (RoundRobin okuma havuzu
		// allow-list'i dışında); nadir admin yedeği için havuz gerekmez.
		rows, err = s.conn.Query(ctx, `
			SELECT cluster, toInt64(count()) AS spans, min(time), max(time)
			FROM spans
			WHERE time >= now() - INTERVAL 1 HOUR AND cluster != ''
			GROUP BY cluster
			ORDER BY spans DESC
			LIMIT 200
			SETTINGS max_execution_time = 15`)
		if err != nil {
			return out, fmt.Errorf("span cluster values (spans): %w", err)
		}
	}
	defer rows.Close()
	for rows.Next() {
		var r SeenClusterValue
		if err := rows.Scan(&r.Value, &r.Spans, &r.FirstSeen, &r.LastSeen); err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, r)
	}
	return out, rows.Err()
}

// isUnknownTableOrColumn — CH 60 UNKNOWN_TABLE / 47 UNKNOWN_IDENTIFIER.
func isUnknownTableOrColumn(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "code: 60") || strings.Contains(m, "code: 47")
}
