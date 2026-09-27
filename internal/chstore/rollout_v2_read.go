package chstore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

// rollout_v2_read.go — v0.10.984 — ROLLOUTS v2 P2.3 okuma yolu
// (docs/rollouts/v2-audit.md §10.6 "Feed" / "Tail", §2.2, §2.4, §10.3.8).
//
// rollouts.source="v2" iken /api/rollouts, /api/rollout, /api/rollout/detail,
// /api/rollouts/stats, SSE tail, GitOps sekmesi ve servis kapsamlı okuma
// workload_rollouts yerine rollout_events'i (P2.2 dedektörü yazar) okur.
// Cevap şekli v1'in RolloutRow'u: v2 satırı RolloutRowFromV2 ile v1
// alanlarına eşlenir (durum v1 sözlüğüne — rollout.V2StatusToV1), v2'ye
// özgü alanlar RolloutRow.V2'de EKLEMELİ JSON anahtarları olur. v1 yolunun
// cevabı bayt bayt aynı kalır (V2 nil → anahtar yok; rollout_v2_read_test.go
// v1 anahtar kümesini pinler).
//
// Okumaların hepsi FINAL + zaman sınırlı WHERE (started_at ya da updated_at)
// + LIMIT + max_execution_time; state tabloları → in-order ana bağlantı
// (s.conn; conn_strategy_test stateTables). Tablo bölümsüz ve küçük (iş
// yükü × rollout, TTL 180 g): FINAL tam taramadır, v1'in workload_rollouts
// okumalarıyla aynı sınıf (§10.6 "measure first" — ölçüm prod'da P2.3
// açılışında, DECISIONS kaydı).
//
// Süzgeç anlamı v1 ile aynı: ?status= v1 değeri (in_progress…) v2'ye çevrilir
// (rollout.V1StatusToV2), ?kind= workload_kind'e, ?cluster= EffectiveID'ye.

// RolloutV2ID — v0.10.984 — rollout_events anahtarı (6 parçalı ?rollout=
// bağlantısı: cluster|namespace|kind|workload|incarnationAtMs|generation).
type RolloutV2ID struct {
	ClusterID, Namespace, Kind, Workload string
	IncarnationAt                        time.Time
	Generation                           uint64
}

// RolloutV2Fields — v0.10.984 — v2 satırının v1 şekline sığmayan alanları
// (RolloutRow.V2; JSON'da eklemeli anahtarlar, lib/types.ts WorkloadRollout
// opsiyonel alanları).
type RolloutV2Fields struct {
	IncarnationAt      time.Time
	Generation         uint64
	Status             string // ham v2 durumu (progressing | succeeded | stuck | rolled_back | superseded)
	ChangeType         string
	ObservedGeneration uint64
	SpecReplicas       uint32
	UpdatedReplicas    uint32
	AvailableReplicas  uint32
	Images             []string
	PrevImages         []string
	VersionTag         string
	StuckReason        string
	SucceededAt        time.Time
	StuckAt            time.Time
	FinishedAt         time.Time
}

// rolloutV2DetectedBy — v2'de her olay KSM'den (karar 4: "Kaynak" kolonu
// v2'de düşer; alan v1 şekli için dolu kalır).
const rolloutV2DetectedBy = "ksm"

// RolloutRowFromV2 — v0.10.984 — SAF: rollout_events satırı → API satırı.
// v1 zaman alanları: ksmStartedAt = started_at, podsReadyAt = succeeded_at,
// ksmNotReadySince = stuck_at, completedAt = succeeded_at (yoksa
// finished_at — geri alınan / devralınan da biter); span alanları 0 (v2
// span okumaz, §9). Revizyon = new_revision (boş olabilir: STS/DS
// revizyonu henüz okunamadıysa); imaj çifti rollout.PrimaryImagePair
// (değişen repo > iş yükü adı > ilk; sıralı dizinin ilki sidecar olabilir).
func RolloutRowFromV2(e rollout.V2Event) RolloutRow {
	succ := rollout.V2FromCHTime(e.SucceededAt)
	stuck := rollout.V2FromCHTime(e.StuckAt)
	fin := rollout.V2FromCHTime(e.FinishedAt)
	done := succ
	if done.IsZero() {
		done = fin
	}
	curRef, prevRef := rollout.PrimaryImagePair(e.Images, e.PrevImages, e.Workload)
	img, tag := rollout.SplitImageRef(curRef)
	pimg, ptag := rollout.SplitImageRef(prevRef)
	return RolloutRow{
		Rollout: rollout.Rollout{
			ClusterID: e.ClusterID, Namespace: e.Namespace, Workload: e.Workload, Kind: e.WorkloadKind,
			Revision: e.NewRevision, StartedAt: e.StartedAt, Status: rollout.V2StatusToV1(e.Status),
			PrevRevision: e.OldRevision, Image: img, ImageTag: tag, PrevImage: pimg, PrevImageTag: ptag,
			KSMStartedAt: e.StartedAt, PodsReadyAt: succ, KSMNotReadySince: stuck, CompletedAt: done,
			DetectedBy: rolloutV2DetectedBy, Note: e.Note,
		},
		UpdatedAt: e.UpdatedAt,
		V2: &RolloutV2Fields{
			IncarnationAt: e.IncarnationAt, Generation: e.Generation, Status: e.Status, ChangeType: e.ChangeType,
			ObservedGeneration: e.ObservedGeneration, SpecReplicas: e.SpecReplicas, UpdatedReplicas: e.UpdatedReplicas,
			AvailableReplicas: e.AvailableReplicas, Images: nonNilStrings(e.Images), PrevImages: nonNilStrings(e.PrevImages),
			VersionTag: e.VersionTag, StuckReason: e.StuckReason, SucceededAt: succ, StuckAt: stuck, FinishedAt: fin,
		},
	}
}

// v2JSONFields — RolloutRow.MarshalJSON'a eklenecek v2 anahtarları (ms).
func (f *RolloutV2Fields) jsonFields(ms func(time.Time) int64) map[string]any {
	return map[string]any{
		"incarnationAt": ms(f.IncarnationAt), "generation": f.Generation, "v2Status": f.Status, "changeType": f.ChangeType,
		"observedGeneration": f.ObservedGeneration, "specReplicas": f.SpecReplicas, "updatedReplicas": f.UpdatedReplicas,
		"availableReplicas": f.AvailableReplicas, "images": nonNilStrings(f.Images), "prevImages": nonNilStrings(f.PrevImages),
		"versionTag": f.VersionTag, "stuckReason": f.StuckReason,
		"succeededAt": ms(f.SucceededAt), "stuckAt": ms(f.StuckAt), "finishedAt": ms(f.FinishedAt),
	}
}

// scanRolloutV2Event — rolloutV2EventCols sırasıyla tek satır.
func scanRolloutV2Event(rows interface{ Scan(...any) error }) (rollout.V2Event, error) {
	var e rollout.V2Event
	err := rows.Scan(&e.ClusterID, &e.Namespace, &e.WorkloadKind, &e.Workload, &e.IncarnationAt, &e.Generation,
		&e.StartedAt, &e.Status, &e.ChangeType, &e.ObservedGeneration, &e.SpecReplicas, &e.UpdatedReplicas,
		&e.AvailableReplicas, &e.NewRevision, &e.OldRevision, &e.Images, &e.PrevImages, &e.VersionTag, &e.StuckReason,
		&e.SucceededAt, &e.StuckAt, &e.FinishedAt, &e.Note, &e.UpdatedAt, &e.Version)
	return e, err
}

// rolloutV2Where — SAF: v1 RolloutFilter'ın rollout_events karşılığı
// (started_at penceresi + eşitlik süzgeçleri; durum v2'ye çevrilir).
func rolloutV2Where(f RolloutFilter, from, to time.Time) (string, []any) {
	where := "started_at >= toDateTime64(?, 3, 'UTC') AND started_at <= toDateTime64(?, 3, 'UTC')"
	args := []any{chDateTime64Arg(from), chDateTime64Arg(to)}
	add := func(col, v string) {
		if v != "" {
			where += " AND " + col + " = ?"
			args = append(args, v)
		}
	}
	add("cluster_id", f.ClusterID)
	add("namespace", f.Namespace)
	add("workload", f.Workload)
	if f.Status != "" {
		add("status", rollout.V1StatusToV2(f.Status))
	}
	add("workload_kind", f.Kind)
	return where, args
}

// rolloutV2OrderKey — toplam sıra için anahtar kolonları (ORDER BY kuyruğu).
const rolloutV2OrderKey = "cluster_id, namespace, workload_kind, workload, incarnation_at, generation"

// rolloutV2ListSQL — SAF: akış (§10.6 Feed). Bağlar: where + LIMIT.
func rolloutV2ListSQL(where string) string {
	return `SELECT ` + rolloutV2EventCols + ` FROM rollout_events FINAL WHERE ` + where + `
		ORDER BY started_at DESC, ` + rolloutV2OrderKey + ` LIMIT ? SETTINGS max_execution_time = 10`
}

// rolloutV2ByIDSQL — SAF: tekil (tam anahtar = ORDER BY). Bağlar: 6.
func rolloutV2ByIDSQL() string {
	return `SELECT ` + rolloutV2EventCols + ` FROM rollout_events FINAL
		WHERE cluster_id = ? AND namespace = ? AND workload_kind = ? AND workload = ?
		  AND incarnation_at = toDateTime64(?, 3, 'UTC') AND generation = ?
		LIMIT 1 SETTINGS max_execution_time = 5`
}

// rolloutV2TailSQL — SAF: tail (§10.6 Tail): keyset (updated_at, anahtar…)
// + watermark + 30 günlük started_at sınırı (v1 RolloutTail ile aynı düzen).
// Bağlar: alt sınır, kursör (7), üst sınır, LIMIT.
func rolloutV2TailSQL() string {
	return `SELECT ` + rolloutV2EventCols + ` FROM rollout_events FINAL
		WHERE updated_at >= toDateTime64(?, 3, 'UTC')
		  AND (updated_at, ` + rolloutV2OrderKey + `) > (toDateTime64(?, 3, 'UTC'), ?, ?, ?, ?, toDateTime64(?, 3, 'UTC'), ?)
		  AND updated_at <= toDateTime64(?, 3, 'UTC') AND started_at >= now() - INTERVAL 30 DAY
		ORDER BY updated_at, ` + rolloutV2OrderKey + `
		LIMIT ? SETTINGS max_execution_time = 5`
}

// rolloutsV2ForWorkloadsSQL — SAF: (cluster_id, namespace, workload) demetleri
// + started_at penceresi (tür anahtarda ama MV iş yükü listesi türsüz →
// her tür). rolloutsForWorkloadsSQL'in (v1) ikizi, aynı tavan.
func rolloutsV2ForWorkloadsSQL(n int) string {
	tuples := make([]string, n)
	for i := range tuples {
		tuples[i] = "(?, ?, ?)"
	}
	return `SELECT ` + rolloutV2EventCols + ` FROM rollout_events FINAL
		WHERE started_at >= toDateTime64(?, 3, 'UTC') AND started_at <= toDateTime64(?, 3, 'UTC')
		  AND (cluster_id, namespace, workload) IN (` + strings.Join(tuples, ", ") + `)
		ORDER BY started_at DESC, ` + rolloutV2OrderKey + `
		LIMIT ` + fmt.Sprint(rolloutsForWorkloadsMax) + ` SETTINGS max_execution_time = 10`
}

func (s *Store) rolloutV2Rows(ctx context.Context, what, sql string, args ...any) ([]RolloutRow, error) {
	rows, err := s.conn.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("rollout_events %s: %w", what, err)
	}
	defer rows.Close()
	out := []RolloutRow{}
	for rows.Next() {
		e, err := scanRolloutV2Event(rows)
		if err != nil {
			return nil, fmt.Errorf("rollout_events %s scan: %w", what, err)
		}
		out = append(out, RolloutRowFromV2(e))
	}
	return out, rows.Err()
}

// RolloutV2List — v0.10.984 — akış (store kelepçeler: 1..1000, varsayılan 200).
func (s *Store) RolloutV2List(ctx context.Context, f RolloutFilter, from, to time.Time, limit int) ([]RolloutRow, error) {
	limit = clampRolloutLimit(limit, 200, 1000)
	where, args := rolloutV2Where(f, from, to)
	return s.rolloutV2Rows(ctx, "list", rolloutV2ListSQL(where), append(args, limit)...)
}

// RolloutV2ByID — v0.10.984 — tekil (nil = yok).
func (s *Store) RolloutV2ByID(ctx context.Context, id RolloutV2ID) (*RolloutRow, error) {
	rows, err := s.rolloutV2Rows(ctx, "by id", rolloutV2ByIDSQL(), id.ClusterID, id.Namespace, id.Kind, id.Workload,
		chDateTime64Arg(id.IncarnationAt), id.Generation)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// RolloutV2Cursor — v0.10.984 — tail kursörü: TAM sıra (updated_at + anahtar).
// Dedektör bir kümenin yazım batch'ine aynı updated_at'i (YAZIM anı —
// rollout.v2StampWrite; tik başlangıcı 15 s watermark'ını geçip satır
// kaçırırdı) basar (RolloutCursor gerekçesi aynen).
type RolloutV2Cursor struct {
	UpdatedAt time.Time
	ID        RolloutV2ID
}

// RolloutV2Tail — v0.10.984 — kursörden sonrası (watermark: updated_at ≤
// now − wm), FINAL + LIMIT; yeni kursör = son satırın anahtarı (kayıt
// yoksa updated_at = now − wm, anahtar boş). v1 RolloutTail'in ikizi.
func (s *Store) RolloutV2Tail(ctx context.Context, cursor RolloutV2Cursor, wm time.Duration, limit int) ([]RolloutRow, RolloutV2Cursor, error) {
	limit = clampRolloutLimit(limit, 500, 5000)
	hi := time.Now().Add(-wm)
	inc := cursor.ID.IncarnationAt
	if inc.IsZero() {
		inc = time.Unix(0, 0).UTC()
	}
	out, err := s.rolloutV2Rows(ctx, "tail", rolloutV2TailSQL(),
		chDateTime64Arg(cursor.UpdatedAt),
		chDateTime64Arg(cursor.UpdatedAt), cursor.ID.ClusterID, cursor.ID.Namespace, cursor.ID.Kind, cursor.ID.Workload,
		chDateTime64Arg(inc), cursor.ID.Generation, chDateTime64Arg(hi), limit)
	if err != nil {
		return nil, cursor, err
	}
	next := cursor
	if n := len(out); n > 0 {
		last := out[n-1]
		next = RolloutV2Cursor{UpdatedAt: last.UpdatedAt, ID: RolloutV2ID{ClusterID: last.ClusterID, Namespace: last.Namespace,
			Kind: last.Kind, Workload: last.Workload, IncarnationAt: last.V2.IncarnationAt, Generation: last.V2.Generation}}
	} else if hi.After(next.UpdatedAt) {
		next = RolloutV2Cursor{UpdatedAt: hi}
	}
	return out, next, nil
}

// RolloutV2ForWorkloads — v0.10.984 — verilen iş yüklerinin [from, to]
// içinde BAŞLAYAN v2 rollout'ları (GitOps sekmesi + servis kapsamlı okuma).
// keys boşsa sorgu yok; 50'de kesilir (v1 RolloutsForWorkloads kuralı).
func (s *Store) RolloutV2ForWorkloads(ctx context.Context, keys []rollout.Key, from, to time.Time) ([]RolloutRow, error) {
	if len(keys) == 0 {
		return []RolloutRow{}, nil
	}
	if len(keys) > 50 {
		keys = keys[:50]
	}
	args := []any{chDateTime64Arg(from), chDateTime64Arg(to)}
	for _, k := range keys {
		args = append(args, k.ClusterID, k.Namespace, k.Workload)
	}
	return s.rolloutV2Rows(ctx, "for workloads", rolloutsV2ForWorkloadsSQL(len(keys)), args...)
}

// rolloutV2StatsTotalsSQL — SAF: toplamlar (v2 durumları) + süre (yalnız
// succeeded: succeeded_at − started_at; DORA lead time, v1 kuralı).
func rolloutV2StatsTotalsSQL(where string) string {
	return `SELECT count(), countIf(status='succeeded'), countIf(status='rolled_back'),
			countIf(status='progressing'), countIf(status='stuck'), countIf(status='superseded'),
			avgIf(dateDiff('second', started_at, succeeded_at), status = 'succeeded' AND succeeded_at > toDateTime64(0,3)),
			quantileIf(0.95)(dateDiff('second', started_at, succeeded_at), status = 'succeeded' AND succeeded_at > toDateTime64(0,3))
		FROM rollout_events FINAL WHERE ` + where + ` SETTINGS max_execution_time = 10`
}

func rolloutV2StatsTopSQL(where, extra string) string {
	return `SELECT cluster_id, namespace, workload, count() AS n FROM rollout_events FINAL
		WHERE ` + where + extra + ` GROUP BY cluster_id, namespace, workload ORDER BY n DESC, cluster_id, namespace, workload LIMIT ? SETTINGS max_execution_time = 10`
}

func rolloutV2StatsByDaySQL(where string) string {
	return `SELECT toString(toDate(started_at)) AS d, count(), countIf(status='rolled_back')
		FROM rollout_events FINAL WHERE ` + where + ` GROUP BY d ORDER BY d LIMIT 400 SETTINGS max_execution_time = 10`
}

// RolloutV2Stats — v0.10.984 — agregat sekmesi, v1 RolloutStats şeklinde
// (completed = succeeded, inProgress = progressing, stalled = stuck).
func (s *Store) RolloutV2Stats(ctx context.Context, clusterID, ns string, from, to time.Time, topN int) (*RolloutStats, error) {
	topN = clampRolloutLimit(topN, 10, 50)
	where, args := rolloutV2Where(RolloutFilter{ClusterID: clusterID, Namespace: ns}, from, to)
	st := &RolloutStats{From: from.UnixMilli(), To: to.UnixMilli(), TopRollback: []RolloutWorkloadN{}, TopDeploy: []RolloutWorkloadN{}, ByDay: []RolloutDayCount{}}
	var total, completed, rolled, inprog, stalled, superseded uint64
	var meanSec, p95Sec float64
	if err := s.conn.QueryRow(ctx, rolloutV2StatsTotalsSQL(where), args...).
		Scan(&total, &completed, &rolled, &inprog, &stalled, &superseded, &meanSec, &p95Sec); err != nil {
		return nil, fmt.Errorf("rollout_events stats: %w", err)
	}
	fillRolloutStatsTotals(st, total, completed, rolled, inprog, stalled, superseded, meanSec, p95Sec, from, to)
	top := func(extra string, dst *[]RolloutWorkloadN) error {
		rows, err := s.conn.Query(ctx, rolloutV2StatsTopSQL(where, extra), append(append([]any{}, args...), topN)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var w RolloutWorkloadN
			var n uint64
			if err := rows.Scan(&w.ClusterID, &w.Namespace, &w.Workload, &n); err != nil {
				return err
			}
			w.N = int(n)
			*dst = append(*dst, w)
		}
		return rows.Err()
	}
	if err := top(" AND status = 'rolled_back'", &st.TopRollback); err != nil {
		return nil, fmt.Errorf("rollout_events stats top rollback: %w", err)
	}
	if err := top("", &st.TopDeploy); err != nil {
		return nil, fmt.Errorf("rollout_events stats top deploy: %w", err)
	}
	rows, err := s.conn.Query(ctx, rolloutV2StatsByDaySQL(where), args...)
	if err != nil {
		return nil, fmt.Errorf("rollout_events stats by day: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var d RolloutDayCount
		var n, rb uint64
		if err := rows.Scan(&d.Day, &n, &rb); err != nil {
			return nil, err
		}
		d.Total, d.RolledBack = int(n), int(rb)
		st.ByDay = append(st.ByDay, d)
	}
	return st, rows.Err()
}

// fillRolloutStatsTotals — SAF: v1 ve v2 istatistiğinin ortak türetimi
// (gün başına, rollback oranı, NaN koruması).
func fillRolloutStatsTotals(st *RolloutStats, total, completed, rolled, inprog, stalled, superseded uint64, meanSec, p95Sec float64, from, to time.Time) {
	st.Total, st.Completed, st.RolledBack, st.InProgress, st.Stalled, st.Superseded = int(total), int(completed), int(rolled), int(inprog), int(stalled), int(superseded)
	if days := to.Sub(from).Hours() / 24; days > 0 {
		st.PerDay = float64(total) / days
	}
	if completed+rolled > 0 {
		st.RollbackRate = float64(rolled) / float64(completed+rolled)
	}
	if meanSec == meanSec { // NaN korunması
		st.MeanDurationSec = meanSec
	}
	if p95Sec == p95Sec {
		st.P95DurationSec = p95Sec
	}
}

// ── rollout_worker_runs okuması (P2.2 yalnız yazıcıyı getirdi) ─────────────

const rolloutWorkerRunReadCols = `worker, started_at, host, finished_at, status, scopes_total, scopes_ok, series_read, truncated,
	partial_response, rows_written, unmapped, api_calls, api_throttled, duration_ms, error`

// rolloutWorkerRunsSQL — SAF: bir işçinin son koşuları. Bağlar: worker, LIMIT.
// Pencere `days` gün (TTL 30 g; 1 = son koşu notu, 7 = admin listesi).
func rolloutWorkerRunsSQL(days int) string {
	return fmt.Sprintf(`SELECT %s FROM rollout_worker_runs FINAL
		WHERE worker = ? AND started_at >= now() - INTERVAL %d DAY
		ORDER BY started_at DESC, host LIMIT ? SETTINGS max_execution_time = 5`, rolloutWorkerRunReadCols, days)
}

func (s *Store) rolloutWorkerRuns(ctx context.Context, worker string, days, limit int) ([]rollout.WorkerRun, error) {
	rows, err := s.conn.Query(ctx, rolloutWorkerRunsSQL(days), worker, limit)
	if err != nil {
		return nil, fmt.Errorf("rollout_worker_runs: %w", err)
	}
	defer rows.Close()
	out := []rollout.WorkerRun{}
	for rows.Next() {
		var r rollout.WorkerRun
		var scopes, scopesOK uint16
		var series, written, unmapped, calls, throttled, dur uint32
		var trunc, partial uint8
		if err := rows.Scan(&r.Worker, &r.StartedAt, &r.Host, &r.FinishedAt, &r.Status, &scopes, &scopesOK, &series, &trunc,
			&partial, &written, &unmapped, &calls, &throttled, &dur, &r.Error); err != nil {
			return nil, fmt.Errorf("rollout_worker_runs scan: %w", err)
		}
		r.ScopesTotal, r.ScopesOK, r.SeriesRead, r.Truncated, r.PartialResponse = int(scopes), int(scopesOK), int(series), trunc == 1, partial == 1
		r.RowsWritten, r.Unmapped, r.APICalls, r.APIThrottled, r.DurationMs = int(written), int(unmapped), int(calls), int(throttled), int(dur)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RolloutWorkerRuns — v0.10.984 — bir v2 işçisinin son N koşusu (7 gün;
// store kelepçeler: 1..1000, varsayılan 50).
func (s *Store) RolloutWorkerRuns(ctx context.Context, worker string, limit int) ([]rollout.WorkerRun, error) {
	return s.rolloutWorkerRuns(ctx, worker, 7, clampRolloutLimit(limit, 50, 1000))
}

// RolloutWorkerLastRun — v0.10.984 — son koşu (1 gün; nil = yok).
func (s *Store) RolloutWorkerLastRun(ctx context.Context, worker string) (*rollout.WorkerRun, error) {
	runs, err := s.rolloutWorkerRuns(ctx, worker, 1, 1)
	if err != nil || len(runs) == 0 {
		return nil, err
	}
	return &runs[0], nil
}
