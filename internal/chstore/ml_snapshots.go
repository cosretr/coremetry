package chstore

// ml_snapshots.go — ML replay kaydı (v0.10.1752). Tablo store.go
// `ml_event_snapshots`. Satır kendine yeter: seri (±2 sa, kova
// çözünürlüğü), taban merkez + MAD, eşikler + config hash, takvim sınıfı,
// tetiklenen kovalar, teyit eden olay ID'leri, kanıt, sınırlı ham örnek
// (trace ID / log satırı) ve rollout'lar. Replay yalnız bu satırı okur.

import (
	"context"
	"time"
)

// MLSample — sınırlı ham örnek (trace ID ve/veya log satırı).
type MLSample struct {
	T       int64  `json:"t"` // unix ns
	TraceID string `json:"traceId,omitempty"`
	Body    string `json:"body,omitempty"`
	Kind    string `json:"kind"` // span | log
}

// MLSnapshot — tek olay kaydı. JSON alanlar API'ye ham geçer.
type MLSnapshot struct {
	EventID     string   `json:"eventId"`
	Detector    string   `json:"detector"`
	Signal      string   `json:"signal"`
	Service     string   `json:"service"`
	Part        string   `json:"part"`
	DetectedAt  int64    `json:"detectedAt"` // unix ns
	LastSeen    int64    `json:"lastSeen"`
	BucketSec   uint32   `json:"bucketSec"`
	Series      string   `json:"series"` // JSON []MLPoint
	BaselineMed float64  `json:"baselineMed"`
	BaselineMAD float64  `json:"baselineMad"`
	Thresholds  string   `json:"thresholds"` // JSON
	ConfigHash  string   `json:"configHash"`
	DayClass    string   `json:"dayClass"`
	Dwell       string   `json:"dwell"` // JSON []int64 (tetiklenen kova başları)
	ConfirmIDs  []string `json:"confirmIds"`
	Evidence    string   `json:"evidence"`
	Samples     string   `json:"samples"` // JSON []MLSample
	Deploys     string   `json:"deploys"` // JSON []RecentDeployEntry
	Complete    bool     `json:"complete"`
}

// UpsertMLSnapshot — tam satır (RMT: tüm alanlar taşınır).
func (s *Store) UpsertMLSnapshot(ctx context.Context, x MLSnapshot) error {
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_event_snapshots (event_id, detector, signal, service, part, detected_at, last_seen, bucket_sec, series, baseline_med, baseline_mad, thresholds, config_hash, day_class, dwell, confirm_ids, evidence, samples, deploys, complete)`)
	if err != nil {
		return err
	}
	ids := x.ConfirmIDs
	if ids == nil {
		ids = []string{}
	}
	var complete uint8
	if x.Complete {
		complete = 1
	}
	if err := batch.Append(x.EventID, x.Detector, x.Signal, x.Service, x.Part, time.Unix(0, x.DetectedAt), time.Unix(0, x.LastSeen), x.BucketSec,
		x.Series, x.BaselineMed, x.BaselineMAD, x.Thresholds, x.ConfigHash, x.DayClass, x.Dwell, ids, x.Evidence, x.Samples, x.Deploys, complete); err != nil {
		return err
	}
	return batch.Send()
}

// GetMLSnapshot — olay kaydı (FINAL); yoksa ok=false.
func (s *Store) GetMLSnapshot(ctx context.Context, eventID string) (MLSnapshot, bool, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT event_id, detector, signal, service, part, detected_at, last_seen, bucket_sec, series, baseline_med, baseline_mad,
		       thresholds, config_hash, day_class, dwell, confirm_ids, evidence, samples, deploys, complete
		FROM ml_event_snapshots FINAL
		WHERE event_id = ?
		LIMIT 1
		SETTINGS max_execution_time = 5`, eventID)
	if err != nil {
		return MLSnapshot{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return MLSnapshot{}, false, rows.Err()
	}
	var x MLSnapshot
	var det, ls time.Time
	var complete uint8
	if err := rows.Scan(&x.EventID, &x.Detector, &x.Signal, &x.Service, &x.Part, &det, &ls, &x.BucketSec, &x.Series, &x.BaselineMed, &x.BaselineMAD,
		&x.Thresholds, &x.ConfigHash, &x.DayClass, &x.Dwell, &x.ConfirmIDs, &x.Evidence, &x.Samples, &x.Deploys, &complete); err != nil {
		return MLSnapshot{}, false, err
	}
	x.DetectedAt, x.LastSeen, x.Complete = det.UnixNano(), ls.UnixNano(), complete == 1
	return x, true, rows.Err()
}

// ListMLSnapshotsForService — aynı servisin pencereyle örtüşen diğer olay
// kayıtları (replay "servisin diğer sinyalleri"); yalnız başlık alanları.
func (s *Store) ListMLSnapshotsForService(ctx context.Context, service string, from, to time.Time) ([]MLSnapshot, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT event_id, detector, signal, part, detected_at, last_seen, baseline_med
		FROM ml_event_snapshots FINAL
		WHERE service = ? AND detected_at <= ? AND last_seen >= ?
		ORDER BY detected_at
		LIMIT 50
		SETTINGS max_execution_time = 5`, service, chDateTime64Arg(to), chDateTime64Arg(from))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLSnapshot
	for rows.Next() {
		var x MLSnapshot
		var det, ls time.Time
		if err := rows.Scan(&x.EventID, &x.Detector, &x.Signal, &x.Part, &det, &ls, &x.BaselineMed); err != nil {
			return nil, err
		}
		x.Service, x.DetectedAt, x.LastSeen = service, det.UnixNano(), ls.UnixNano()
		out = append(out, x)
	}
	return out, rows.Err()
}

// MLSnapshotSeriesFor — v0.10.1874: liste ucunun "Seyir" sütunu için TEK
// toplu okuma — olay id'leri → kayıt serisi (JSON). Satır başına istek YOK;
// ORDER BY event_id anahtarında IN, FINAL, LIMIT, max_execution_time.
func (s *Store) MLSnapshotSeriesFor(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT event_id, series FROM ml_event_snapshots FINAL
		WHERE event_id IN (?)
		LIMIT ?
		SETTINGS max_execution_time = 5`, ids, len(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, series string
		if err := rows.Scan(&id, &series); err != nil {
			return nil, err
		}
		out[id] = series
	}
	return out, rows.Err()
}

// MLCallPoint — az çağrılan servisin tek bir giriş çağrısı (nokta grafiği).
type MLCallPoint struct {
	T       int64   `json:"t"`  // unix ms
	Ms      float64 `json:"ms"` // süre
	Err     bool    `json:"err,omitempty"`
	TraceID string  `json:"traceId,omitempty"`
}

// MLCallPointsLimit — nokta grafiği tavanı (az çağrılan serviste pencere
// onlarca çağrı; tavan dolarsa çağıran "ilk N" der).
const MLCallPointsLimit = 500

// MLCallPoints — v0.10.1872: az çağrılan servisin [from, to) giriş span'leri
// (server + consumer), route verilirse yalnız o http_route. AGREGA DEĞİL —
// tek tek çağrı listesi (MV'de karşılığı yok); ham spans ama zaman sınırı
// (ORDER BY anahtarı service_name, time) + LIMIT + max_execution_time.
func (s *Store) MLCallPoints(ctx context.Context, service, route string, from, to time.Time, limit int) ([]MLCallPoint, error) {
	if limit <= 0 || limit > MLCallPointsLimit {
		limit = MLCallPointsLimit
	}
	q := `SELECT toUnixTimestamp64Milli(time), toFloat64(duration) / 1e6, toUInt8(status_code = 'error'), trace_id FROM spans
		WHERE service_name = ? AND time >= ? AND time < ? AND kind IN ('server', 'consumer')`
	args := []any{service, chDateTime64Arg(from), chDateTime64Arg(to)}
	if route != "" {
		q += ` AND http_route = ?`
		args = append(args, route)
	}
	q += ` ORDER BY time LIMIT ? SETTINGS max_execution_time = 5`
	args = append(args, limit)
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MLCallPoint{}
	for rows.Next() {
		var x MLCallPoint
		var isErr uint8
		if err := rows.Scan(&x.T, &x.Ms, &isErr, &x.TraceID); err != nil {
			return nil, err
		}
		x.Err = isErr == 1
		out = append(out, x)
	}
	return out, rows.Err()
}

// MLSampleTraces — replay ham örneği: servisin pencere içindeki en çok 5
// trace ID'si (hata dedektörlerinde yalnız error span). Ham spans ama
// toplam değil örnek: LIMIT + zaman sınırı + max_execution_time.
func (s *Store) MLSampleTraces(ctx context.Context, service, route string, from, to time.Time, errorsOnly bool) ([]MLSample, error) {
	q := `SELECT trace_id, toUnixTimestamp64Nano(time) FROM spans
		WHERE service_name = ? AND time >= ? AND time < ?`
	args := []any{service, chDateTime64Arg(from), chDateTime64Arg(to)}
	if route != "" {
		q += ` AND http_route = ?`
		args = append(args, route)
	}
	if errorsOnly {
		q += ` AND status_code = 'error'`
	}
	q += ` LIMIT 5 SETTINGS max_execution_time = 3`
	rows, err := s.conn.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLSample
	for rows.Next() {
		var x MLSample
		if err := rows.Scan(&x.TraceID, &x.T); err != nil {
			return nil, err
		}
		x.Kind = "span"
		out = append(out, x)
	}
	return out, rows.Err()
}
