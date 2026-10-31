package chstore

// ml_series.go — ML log dilim 1: servis × seviye × kova sayım serisi
// (taban CH'de; ES'e yalnız son pencere sorulur — audit K2). Tablo store.go
// `ml_series` (RMT(version), ORDER BY (signal, service, part, bucket,
// bucket_sec), TTL 28 g). Backfill 1 saatlik kova yazar (bucket_sec=3600);
// taban okuması oranı hedef kovaya normalize eder (count·target/bucket_sec).

import (
	"context"
	"time"
)

type MLSeriesRow struct {
	Signal    string
	Service   string
	Part      string // log: seviye; trace: endpoint
	Bucket    time.Time
	BucketSec uint32
	Count     float64
}

// InsertMLSeries — toplu yazım (RMT: aynı anahtar yeniden yazılır, idempotent).
func (s *Store) InsertMLSeries(ctx context.Context, rows []MLSeriesRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_series (signal, service, part, bucket, bucket_sec, count)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(r.Signal, r.Service, r.Part, r.Bucket.UTC(), r.BucketSec, r.Count); err != nil {
			return err
		}
	}
	return batch.Send()
}

// MLBaselineRequest — aynı takvim sınıfı günler × saat penceresi.
type MLBaselineRequest struct {
	Signal    string
	Dates     []string // "2026-10-24" … (takvim sınıfı eşleşen günler)
	HourFrom  int      // yerel saat penceresi [HourFrom, HourTo] (döngüsel değil; çağıran böler)
	HourTo    int
	TargetSec int // normalize hedefi (ör. 300)
	TZ        string
	// Exclude — taban hijyeni (kalite çerçevesi): servis → [başlangıç, bitiş]
	// unix sn pencereleri (gerçek anomali diye etiketlenmiş olaylar); bu
	// kovalar tabana GİRMEZ, olay normali kirletmez. nil = süzme yok.
	Exclude map[string][][2]int64
}

// MLExcluded — SAF: ts (unix sn) servisin dışlama pencerelerinden birinde mi.
func MLExcluded(windows [][2]int64, ts int64) bool {
	for _, w := range windows {
		if ts >= w[0] && ts <= w[1] {
			return true
		}
	}
	return false
}

// MLBaselineSeries — anahtar → normalize edilmiş oran örnekleri.
type MLBaselineSeries map[string][]float64 // key = service + "\x00" + part

func MLSeriesKey(service, part string) string { return service + "\x00" + part }

// MLBaseline — tek CH okuması; örnekler Go'da medyan/MAD'a gider (ml paketi).
// FINAL yok: RMT çift satırı groupArray'de iki örnek olur, medyan dayanıklı.
func (s *Store) MLBaseline(ctx context.Context, req MLBaselineRequest) (MLBaselineSeries, error) {
	if len(req.Dates) == 0 {
		return MLBaselineSeries{}, nil
	}
	tz := req.TZ
	if tz == "" {
		tz = "UTC"
	}
	rows, err := s.conn.Query(ctx, `
		SELECT service, part, groupArray(toUnixTimestamp(bucket)) AS ts, groupArray(count * ? / bucket_sec) AS xs
		FROM ml_series
		WHERE signal = ?
		  AND toDate(bucket, ?) IN (?)
		  AND toHour(bucket, ?) BETWEEN ? AND ?
		  AND bucket >= ?
		GROUP BY service, part
		LIMIT 20000
		SETTINGS max_execution_time = 10`,
		float64(req.TargetSec), req.Signal, tz, req.Dates, tz, req.HourFrom, req.HourTo,
		time.Now().Add(-35*24*time.Hour).UTC()) // bucket DateTime: DateTime64 bind YOK (AST pini)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := MLBaselineSeries{}
	for rows.Next() {
		var svc, part string
		var ts []uint32
		var xs []float64
		if err := rows.Scan(&svc, &part, &ts, &xs); err != nil {
			return nil, err
		}
		if win := req.Exclude[svc]; len(win) > 0 && len(ts) == len(xs) {
			kept := xs[:0]
			for i, v := range xs {
				if !MLExcluded(win, int64(ts[i])) {
					kept = append(kept, v)
				}
			}
			xs = kept
		}
		out[MLSeriesKey(svc, part)] = xs
	}
	return out, rows.Err()
}

// MLPoint — zaman damgalı seri noktası (backtest / replay).
type MLPoint struct {
	T int64   `json:"t"` // unix sn (kova başı)
	V float64 `json:"v"` // hedef kovaya normalize
}

// MLSeriesRange — bir servisin [from, to) aralığındaki tüm parçaları,
// hedef kovaya normalize (backtest motoru). Servis zorunlu: tek servis ×
// ≤ 6 hafta, tavan 400k nokta. FINAL yok: çift satır aynı (t, v) iki kez
// gelir, çağıran t ile tekilleştirir.
func (s *Store) MLSeriesRange(ctx context.Context, signal, service string, from, to time.Time, targetSec int) (map[string][]MLPoint, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT part, toUnixTimestamp(bucket) AS t, count * ? / bucket_sec AS v
		FROM ml_series
		WHERE signal = ? AND service = ? AND bucket >= ? AND bucket < ?
		ORDER BY part, t
		LIMIT 400000
		SETTINGS max_execution_time = 10`,
		float64(targetSec), signal, service, from.UTC(), to.UTC()) // DateTime kolon: DateTime64 bind YOK
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]MLPoint{}
	for rows.Next() {
		var part string
		var t uint32
		var v float64
		if err := rows.Scan(&part, &t, &v); err != nil {
			return nil, err
		}
		pts := out[part]
		if n := len(pts); n > 0 && pts[n-1].T == int64(t) {
			continue
		}
		out[part] = append(pts, MLPoint{T: int64(t), V: v})
	}
	return out, rows.Err()
}

// MLSeriesOldest — sinyalin en eski kovası (sıfır = satır yok); backfill kararı.
func (s *Store) MLSeriesOldest(ctx context.Context, signal string) (time.Time, error) {
	rows, err := s.conn.Query(ctx, `SELECT min(bucket) FROM ml_series WHERE signal = ? SETTINGS max_execution_time = 5`, signal)
	if err != nil {
		return time.Time{}, err
	}
	defer rows.Close()
	var t time.Time
	if rows.Next() {
		if err := rows.Scan(&t); err != nil {
			return time.Time{}, err
		}
	}
	if t.Year() < 2000 {
		return time.Time{}, rows.Err()
	}
	return t, rows.Err()
}

// MLSeriesKeyRow — bir seri anahtarı.
type MLSeriesKeyRow struct{ Service, Part string }

// MLSeriesParts — `since`'den beri yazılmış, öneki tutan seri anahtarları
// (log dilim 2: izlenen şablon aileleri restart sonrası geri yüklenir).
func (s *Store) MLSeriesParts(ctx context.Context, signal, prefix string, since time.Time) ([]MLSeriesKeyRow, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT DISTINCT service, part FROM ml_series
		WHERE signal = ? AND startsWith(part, ?) AND bucket >= ?
		LIMIT 2000
		SETTINGS max_execution_time = 5`, signal, prefix, since.UTC()) // DateTime kolon: DateTime64 bind YOK
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLSeriesKeyRow
	for rows.Next() {
		var r MLSeriesKeyRow
		if err := rows.Scan(&r.Service, &r.Part); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
