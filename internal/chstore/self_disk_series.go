package chstore

// self_disk_series.go — v0.10.911 (Dynatrace paritesi #6 dilim 4, Karar 1;
// operatör onayı 2026-09-25). CH disk doluluğu KALICI seri olarak yazılır.
//
// NEDEN (v0.9.1279 kararının tersi, DECISIONS.md): self-disk-eta serisi
// evaluator belleğinde, yalnız liderde, 6 saatlik halkaydı. Sonuç: "20 gün
// sonra dolacak" hiçbir yerde görünmüyordu (rozet yalnız 7 günün altındaki
// açık Problem'den), deploy'da 30 dk kör pencere, mevsimsellik imkânsız.
// Lider her tikte system.disks'i ZATEN okuyor; aynı değer metric_points'e
// bir gauge olarak düşer (dakikada düğüm × disk satır — ihmal edilebilir),
// rollup_metrics_1h/5m otomatik → 7 günlük saatlik tarihçe MV-first okunur.
//
// Seri kimliği service_name'de (rollup şeması yalnız (metric, service_name)
// taşır, attr'ları katlar): SelfDiskSeriesService — tek düğümde disk adı,
// kümede host/disk. Yazan (evaluator, hostName() dolu) ve okuyan (sysstats,
// tek düğümde host='') AYNI fonksiyonu çağırır → v0.10.901'deki host
// uyuşmazlığı burada doğmaz.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
)

const (
	// SelfDiskUsedMetric — kullanılan bayt (total − free), gauge.
	SelfDiskUsedMetric    = "coremetry.self.disk_used_bytes"
	selfDiskServicePrefix = "coremetry-self/"
)

// SelfDiskSeriesService — seri kimliği (service_name). Tek düğüm kurulumda
// host yok sayılır (sysstats host=” okur); kümede host/disk.
func (s *Store) SelfDiskSeriesService(host, disk string) string {
	if strings.TrimSpace(s.cfg.ClusterName) == "" {
		return selfDiskServicePrefix + disk
	}
	return selfDiskServicePrefix + DiskKey(host, disk)
}

// SelfDiskPoints — SAF-benzeri (yalnız seri adı için Store): CollectDisks
// sonucu → gauge noktaları. Stat edilemeyen disk (Total 0 / Free > Total)
// yazılmaz ("%100 dolu" iddia etme — selfDiskETA kuralıyla aynı).
func (s *Store) SelfDiskPoints(disks []DiskFree, now time.Time) []*MetricPoint {
	t := now.UTC().Truncate(time.Second)
	out := make([]*MetricPoint, 0, len(disks))
	for _, d := range disks {
		if d.Total == 0 || d.Free > d.Total {
			continue
		}
		svc := s.SelfDiskSeriesService(d.Host, d.Disk)
		out = append(out, &MetricPoint{
			Metric: SelfDiskUsedMetric, Instrument: "gauge", Unit: "By",
			Description: "ClickHouse disk used bytes (Coremetry self-health)",
			ServiceName: svc, HostName: d.Host, Time: t, StartTime: t,
			Value:    float64(d.Total - d.Free),
			AttrKeys: []string{"disk", "host", "total_bytes"}, AttrValues: []string{d.Disk, d.Host, strconv.FormatUint(d.Total, 10)},
			SeriesFingerprint: xxhash.Sum64String(SelfDiskUsedMetric + "\x00" + svc),
		})
	}
	return out
}

// SelfDiskHistory — son window'un saatlik (max) serisi; rollup_metrics_1h'ten
// (QueryMetric rollup kapısı: gauge + max + adım 3600 + filtresiz), yoksa ham.
func (s *Store) SelfDiskHistory(ctx context.Context, service string, window time.Duration, now time.Time) ([]SpanMetricPoint, error) {
	series, err := s.QueryMetric(ctx, MetricQueryFilter{
		Name: SelfDiskUsedMetric, Service: service, Aggregation: "max",
		From: now.Add(-window), To: now, StepSeconds: 3600,
	})
	if err != nil || len(series) == 0 {
		return nil, err
	}
	return series[0].Points, nil
}
