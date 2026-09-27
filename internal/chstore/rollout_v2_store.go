package chstore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

// rollout_v2_store.go — v0.10.982 — ROLLOUTS v2 P2.2 CH kapısı
// (docs/rollouts/v2-audit.md §10.2, §10.3.1/2/8, §10.6; şema
// rollout_v2_schema.go ≡ migrations/0015).
//
// Okuma (dedektörün önceki durumu, §10.6 "Detector previous state"):
//   - rollout_workload_state FINAL WHERE cluster_id = ?, keyset sayfalı
//     (ORDER BY öneki (namespace, workload_kind, workload)); tavan aşımı
//     HATA — kesik durumdan yazım yapılmaz (v1 RolloutRecentRows emsali).
//   - rollout_events FINAL WHERE cluster_id = ?, (iş yükü, incarnation)
//     başına generation'ı en büyük satır (LIMIT 1 BY), aynı keyset düzeni.
//     Çekirdeğin PrevEvents sözleşmesi tam bu alt sınırdır (açık olay
//     dahil); zaman süzgeci YOK — durumu olmayan iş yüklerinin ve durumdan
//     yeni incarnation'ların olayları da gelmeli (kayıp durum kurulumu).
//     Tablo TTL'i 180 gün ve satır sayısı ~iş yükü × incarnation: küçük.
//   Her iki okuma da in-order ANA bağlantıda (s.conn; stateTables pinli).
//
// Yazım: tam satır (RMT(version), invariant #4 — her kolon taşınır), AÇIK
// istemci version'ı (dedektör tekdüze üretir; Replicated insert-dedup
// dersi), asyncInsertCtx (async_insert mekanizması korunur;
// wait_for_async_insert=1 → çağıran yazımın bittiğini bilir). Yazıcı
// sözleşmesi (§10.2): her satır batch'e girmeden ValidateV2* /
// ValidateWorkerRun'dan geçer; TEK geçersiz satır bütün batch'i reddeder
// (yarım yazım yok; dedektör belleği düşürüp CH'den yeniden kurar).

const (
	rolloutV2StatePage    = 20_000
	rolloutV2StateHardCap = 200_000
	rolloutV2EventPage    = 20_000
	rolloutV2EventHardCap = 200_000

	rolloutWorkerRunErrorMax = 4000
)

// Kolon listeleri V2Event / V2WorkloadState `ch` etiketleriyle SIRASIYLA
// aynı (rollout_v2_store_test.go pinler; model 0015'e v2model_test.go'da).
const rolloutV2EventCols = `cluster_id, namespace, workload_kind, workload, incarnation_at, generation, started_at, status, change_type,
	observed_generation, spec_replicas, updated_replicas, available_replicas, new_revision, old_revision, images, prev_images,
	version_tag, stuck_reason, succeeded_at, stuck_at, finished_at, note, updated_at, version`

const rolloutV2StateCols = `cluster_id, namespace, workload_kind, workload, incarnation_at, generation, observed_generation,
	pending_generation, pending_started_at, current_revision, known_revisions, images, open_generation, first_seen_at,
	last_seen_at, version`

const rolloutWorkerRunCols = `worker, started_at, host, finished_at, status, scopes_total, scopes_ok, series_read, truncated,
	partial_response, rows_written, unmapped, api_calls, api_throttled, duration_ms, error, version`

// rolloutV2StatesPageSQL — bağlar: cluster_id, (ns, kind, workload) kursörü.
func rolloutV2StatesPageSQL() string {
	return fmt.Sprintf(`SELECT %s
		FROM rollout_workload_state FINAL
		WHERE cluster_id = ?
		  AND (namespace, workload_kind, workload) > (?, ?, ?)
		ORDER BY namespace, workload_kind, workload
		LIMIT %d
		SETTINGS max_execution_time = 15`, rolloutV2StateCols, rolloutV2StatePage)
}

// rolloutV2LatestEventsPageSQL — bağlar: cluster_id, (ns, kind, workload,
// incarnation_at) kursörü. LIMIT 1 BY sayfa LIMIT'inden önce: grup başına
// en büyük generation, sonra sayfa.
func rolloutV2LatestEventsPageSQL() string {
	return fmt.Sprintf(`SELECT %s
		FROM rollout_events FINAL
		WHERE cluster_id = ?
		  AND (namespace, workload_kind, workload, incarnation_at) > (?, ?, ?, toDateTime64(?, 3, 'UTC'))
		ORDER BY namespace, workload_kind, workload, incarnation_at, generation DESC
		LIMIT 1 BY namespace, workload_kind, workload, incarnation_at
		LIMIT %d
		SETTINGS max_execution_time = 15`, rolloutV2EventCols, rolloutV2EventPage)
}

// RolloutV2States — v0.10.982 — dedektörün önceki durumu (bir küme).
func (s *Store) RolloutV2States(ctx context.Context, clusterID string) ([]rollout.V2WorkloadState, error) {
	out := []rollout.V2WorkloadState{}
	var cur rollout.V2WorkloadState
	for {
		rows, err := s.conn.Query(ctx, rolloutV2StatesPageSQL(), clusterID, cur.Namespace, cur.WorkloadKind, cur.Workload)
		if err != nil {
			return nil, fmt.Errorf("rollout_workload_state: %w", err)
		}
		n := 0
		for rows.Next() {
			var r rollout.V2WorkloadState
			if err := rows.Scan(&r.ClusterID, &r.Namespace, &r.WorkloadKind, &r.Workload, &r.IncarnationAt, &r.Generation,
				&r.ObservedGeneration, &r.PendingGeneration, &r.PendingStartedAt, &r.CurrentRevision, &r.KnownRevisions,
				&r.Images, &r.OpenGeneration, &r.FirstSeenAt, &r.LastSeenAt, &r.Version); err != nil {
				rows.Close()
				return nil, fmt.Errorf("rollout_workload_state scan: %w", err)
			}
			out = append(out, r)
			cur = r
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(out) > rolloutV2StateHardCap {
			return nil, fmt.Errorf("rollout_workload_state: küme %s için %d satır tavanı aşıldı — kesik durumdan yazım yapılmaz", clusterID, rolloutV2StateHardCap)
		}
		if n < rolloutV2StatePage {
			return out, nil
		}
	}
}

// RolloutV2LatestEvents — v0.10.982 — (iş yükü, incarnation) başına son olay.
func (s *Store) RolloutV2LatestEvents(ctx context.Context, clusterID string) ([]rollout.V2Event, error) {
	out := []rollout.V2Event{}
	var cur rollout.V2Event
	cur.IncarnationAt = time.Unix(0, 0).UTC()
	for {
		rows, err := s.conn.Query(ctx, rolloutV2LatestEventsPageSQL(), clusterID, cur.Namespace, cur.WorkloadKind, cur.Workload,
			chDateTime64Arg(cur.IncarnationAt))
		if err != nil {
			return nil, fmt.Errorf("rollout_events: %w", err)
		}
		n := 0
		for rows.Next() {
			var e rollout.V2Event
			if err := rows.Scan(&e.ClusterID, &e.Namespace, &e.WorkloadKind, &e.Workload, &e.IncarnationAt, &e.Generation,
				&e.StartedAt, &e.Status, &e.ChangeType, &e.ObservedGeneration, &e.SpecReplicas, &e.UpdatedReplicas,
				&e.AvailableReplicas, &e.NewRevision, &e.OldRevision, &e.Images, &e.PrevImages, &e.VersionTag, &e.StuckReason,
				&e.SucceededAt, &e.StuckAt, &e.FinishedAt, &e.Note, &e.UpdatedAt, &e.Version); err != nil {
				rows.Close()
				return nil, fmt.Errorf("rollout_events scan: %w", err)
			}
			out = append(out, e)
			cur = e
			n++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
		if len(out) > rolloutV2EventHardCap {
			return nil, fmt.Errorf("rollout_events: küme %s için %d satır tavanı aşıldı — kesik geçmişten yazım yapılmaz", clusterID, rolloutV2EventHardCap)
		}
		if n < rolloutV2EventPage {
			return out, nil
		}
	}
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// rolloutV2EventArgs — SAF: tam satır bağları (kolon sırası rolloutV2EventCols).
func rolloutV2EventArgs(e rollout.V2Event) []any {
	return []any{e.ClusterID, e.Namespace, e.WorkloadKind, e.Workload, e.IncarnationAt.UTC(), e.Generation, e.StartedAt.UTC(),
		e.Status, e.ChangeType, e.ObservedGeneration, e.SpecReplicas, e.UpdatedReplicas, e.AvailableReplicas, e.NewRevision,
		e.OldRevision, nonNilStrings(e.Images), nonNilStrings(e.PrevImages), e.VersionTag, e.StuckReason,
		rollout.V2CHTime(e.SucceededAt).UTC(), rollout.V2CHTime(e.StuckAt).UTC(), rollout.V2CHTime(e.FinishedAt).UTC(),
		e.Note, e.UpdatedAt.UTC(), e.Version}
}

// rolloutV2StateArgs — SAF: tam satır bağları (kolon sırası rolloutV2StateCols).
func rolloutV2StateArgs(s rollout.V2WorkloadState) []any {
	return []any{s.ClusterID, s.Namespace, s.WorkloadKind, s.Workload, s.IncarnationAt.UTC(), s.Generation, s.ObservedGeneration,
		s.PendingGeneration, rollout.V2CHTime(s.PendingStartedAt).UTC(), s.CurrentRevision, nonNilStrings(s.KnownRevisions),
		nonNilStrings(s.Images), s.OpenGeneration, s.FirstSeenAt.UTC(), s.LastSeenAt.UTC(), s.Version}
}

// validateV2Events / validateV2States — SAF: batch'in HEPSİ yazıcı
// sözleşmesinden geçmeli (ilk ihlal hatası iş yükü kimliğiyle döner).
func validateV2Events(rows []rollout.V2Event) error {
	for _, e := range rows {
		if err := rollout.ValidateV2Event(e); err != nil {
			return fmt.Errorf("rollout_events %s/%s/%s/%s g%d: %w", e.ClusterID, e.Namespace, e.WorkloadKind, e.Workload, e.Generation, err)
		}
	}
	return nil
}

func validateV2States(rows []rollout.V2WorkloadState) error {
	for _, s := range rows {
		if err := rollout.ValidateV2State(s); err != nil {
			return fmt.Errorf("rollout_workload_state %s/%s/%s/%s: %w", s.ClusterID, s.Namespace, s.WorkloadKind, s.Workload, err)
		}
	}
	return nil
}

// RolloutV2WriteEvents — v0.10.982 — rollout_events tam satır batch'i.
func (s *Store) RolloutV2WriteEvents(ctx context.Context, rows []rollout.V2Event) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateV2Events(rows); err != nil {
		return err
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO rollout_events (`+rolloutV2EventCols+`)`)
	if err != nil {
		return err
	}
	for _, e := range rows {
		if err := b.Append(rolloutV2EventArgs(e)...); err != nil {
			return err
		}
	}
	return b.Send()
}

// RolloutV2WriteStates — v0.10.982 — rollout_workload_state tam satır batch'i.
func (s *Store) RolloutV2WriteStates(ctx context.Context, rows []rollout.V2WorkloadState) error {
	if len(rows) == 0 {
		return nil
	}
	if err := validateV2States(rows); err != nil {
		return err
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO rollout_workload_state (`+rolloutV2StateCols+`)`)
	if err != nil {
		return err
	}
	for _, st := range rows {
		if err := b.Append(rolloutV2StateArgs(st)...); err != nil {
			return err
		}
	}
	return b.Send()
}

func clampU16(v int) uint16 {
	switch {
	case v <= 0:
		return 0
	case v > math.MaxUint16:
		return math.MaxUint16
	}
	return uint16(v)
}

func clampU32(v int) uint32 {
	switch {
	case v <= 0:
		return 0
	case int64(v) > math.MaxUint32:
		return math.MaxUint32
	}
	return uint32(v)
}

// rolloutWorkerRunArgs — SAF: rollout_worker_runs bağları (DDL tiplerine
// kelepçe; error rune tavanlı; version = finished_at ns, açık).
func rolloutWorkerRunArgs(r rollout.WorkerRun) []any {
	msg := r.Error
	if rs := []rune(msg); len(rs) > rolloutWorkerRunErrorMax {
		msg = string(rs[:rolloutWorkerRunErrorMax-1]) + "…"
	}
	ver := r.FinishedAt.UnixNano()
	if ver <= 0 {
		ver = r.StartedAt.UnixNano()
	}
	return []any{r.Worker, r.StartedAt.UTC(), r.Host, r.FinishedAt.UTC(), r.Status, clampU16(r.ScopesTotal), clampU16(r.ScopesOK),
		clampU32(r.SeriesRead), boolU8(r.Truncated), boolU8(r.PartialResponse), clampU32(r.RowsWritten), clampU32(r.Unmapped),
		clampU32(r.APICalls), clampU32(r.APIThrottled), clampU32(r.DurationMs), msg, uint64(ver)}
}

// RecordRolloutWorkerRun — v0.10.982 — rollout_worker_runs satırı; DÖRT v2
// işçisinin ortak yazıcısı (`worker` anahtarda, her işçi kendi değeriyle).
func (s *Store) RecordRolloutWorkerRun(ctx context.Context, r rollout.WorkerRun) error {
	if err := rollout.ValidateWorkerRun(r); err != nil {
		return errors.New("rollout_worker_runs: " + strings.ReplaceAll(err.Error(), "\n", "; "))
	}
	b, err := s.conn.PrepareBatch(asyncInsertCtx(ctx), `INSERT INTO rollout_worker_runs (`+rolloutWorkerRunCols+`)`)
	if err != nil {
		return err
	}
	if err := b.Append(rolloutWorkerRunArgs(r)...); err != nil {
		return err
	}
	return b.Send()
}
