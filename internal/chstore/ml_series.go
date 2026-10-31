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
		SELECT service, part, groupArray(count * ? / bucket_sec) AS xs
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
		var xs []float64
		if err := rows.Scan(&svc, &part, &xs); err != nil {
			return nil, err
		}
		out[MLSeriesKey(svc, part)] = xs
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
