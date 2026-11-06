package chstore

// self_es_series.go — v0.10.1878 (operatör, ES aşırı yük olayı 2026-11-06:
// "Elastic'e giden sorgu sayısını takip edeyim; şu an körüm").
//
// Her pod, Elasticsearch'e gönderdiği istekleri dakikalık hücreler hâlinde
// (işlem türü × yüzey) sayar (logstore/es_meter.go) ve dakikada bir bu
// tabloya yazar. Kalıcı seri olması bilinçli: çok-pod kurulumda sayaç pod
// başınadır; yönetim ekranı hangi pod'a düşerse düşsün TÜM pod'ların toplamını
// görmeli ve pod yeniden başlayınca geçmiş kaybolmamalı. Emsal: disk doluluğu
// serisi (self_disk_series.go, v0.10.911).
//
// Bunlar binary'nin KENDİ yazdığı serilerdir → SelfWrittenMetrics kaydında
// (ingest-stall alarmı onları "dışarıdan metrik geliyor" saymaz).

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/cespare/xxhash/v2"
)

const (
	// SelfESRequestsMetric — dakikadaki istek sayısı (hücre başına).
	SelfESRequestsMetric = "coremetry.self.es_requests"
	// SelfESErrorsMetric — dakikadaki başarısız istek sayısı.
	SelfESErrorsMetric = "coremetry.self.es_request_errors"
	// SelfESDurMsMetric — dakikadaki isteklerin TOPLAM süresi (ms).
	SelfESDurMsMetric = "coremetry.self.es_request_ms"
	selfESService     = "coremetry-self/es"
)

// SelfESCell — bir (dakika, işlem, yüzey) hücresi (logstore.ESRequestStat'ın
// chstore tarafı; chstore logstore'u import edemez).
type SelfESCell struct {
	Minute  time.Time
	Op      string
	Surface string
	Count   uint64
	Errors  uint64
	DurMs   float64
}

// SelfESRequestPoints — SAF: hücreler → metrik noktaları. Sıfır sayımlı hücre
// ve sıfır değerli hata/süre noktası yazılmaz.
func SelfESRequestPoints(cells []SelfESCell, pod string) []*MetricPoint {
	out := make([]*MetricPoint, 0, len(cells)*2)
	for _, c := range cells {
		if c.Count == 0 {
			continue
		}
		t := c.Minute.UTC().Truncate(time.Minute)
		keys := []string{"op", "pod", "surface"}
		vals := []string{c.Op, pod, c.Surface}
		add := func(metric, unit, desc string, v float64) {
			out = append(out, &MetricPoint{
				Metric: metric, Instrument: "gauge", Unit: unit, Description: desc,
				ServiceName: selfESService, HostName: pod, Time: t, StartTime: t, Value: v,
				AttrKeys: keys, AttrValues: vals,
				SeriesFingerprint: xxhash.Sum64String(metric + "\x00" + c.Op + "\x00" + c.Surface + "\x00" + pod),
			})
		}
		add(SelfESRequestsMetric, "1", "Elasticsearch requests per minute (Coremetry self-observability)", float64(c.Count))
		if c.Errors > 0 {
			add(SelfESErrorsMetric, "1", "Failed Elasticsearch requests per minute (Coremetry self-observability)", float64(c.Errors))
		}
		if c.DurMs > 0 {
			add(SelfESDurMsMetric, "ms", "Total Elasticsearch request time per minute (Coremetry self-observability)", c.DurMs)
		}
	}
	return out
}

// SelfESBucket — okuma: bir zaman kovasında bir (işlem, yüzey) toplamı,
// tüm pod'lar birleşik.
type SelfESBucket struct {
	T       int64 // kova başı, unix saniye
	Op      string
	Surface string
	Count   float64
	Errors  float64
	DurMs   float64
}

// selfESSeriesSQL — SAF: okuma sorgusu. Zaman sınırlı, LIMIT'li, süre sınırlı;
// tablo niteliksiz. Adım saniyesi doğrulanmış tamsayı olarak gömülür.
func selfESSeriesSQL(stepSec int) string {
	return `
		SELECT toUnixTimestamp(toStartOfInterval(time, toIntervalSecond(` + strconv.Itoa(stepSec) + `))) AS t,
		       attr_values[indexOf(attr_keys, 'op')]      AS op,
		       attr_values[indexOf(attr_keys, 'surface')] AS surface,
		       sumIf(value, metric = ?) AS cnt,
		       sumIf(value, metric = ?) AS errs,
		       sumIf(value, metric = ?) AS ms
		FROM metric_points
		WHERE service_name = ? AND metric IN (?, ?, ?) AND time >= ? AND time < ?
		GROUP BY t, op, surface
		ORDER BY t
		LIMIT 50000
		SETTINGS max_execution_time = 10`
}

// SelfESRequestSeries — [from, to) penceresinde ES istek hücreleri, stepSec
// kovalarında, tüm pod'lar toplanmış.
func (s *Store) SelfESRequestSeries(ctx context.Context, from, to time.Time, stepSec int) ([]SelfESBucket, error) {
	if stepSec < 60 {
		stepSec = 60
	}
	rows, err := s.telemetryReadConn().Query(ctx, selfESSeriesSQL(stepSec),
		SelfESRequestsMetric, SelfESErrorsMetric, SelfESDurMsMetric,
		selfESService, SelfESRequestsMetric, SelfESErrorsMetric, SelfESDurMsMetric, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SelfESBucket
	for rows.Next() {
		var b SelfESBucket
		var t uint32
		if err := rows.Scan(&t, &b.Op, &b.Surface, &b.Count, &b.Errors, &b.DurMs); err != nil {
			return nil, err
		}
		b.T = int64(t)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, nil
}
