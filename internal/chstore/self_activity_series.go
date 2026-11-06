package chstore

// self_activity_series.go — v0.10.1902 (login özeti, docs/frontend/login-redesign.md §5.2).
//
// Anonim login özetinin "son 24 saatte" satırındaki iki sayı — giriş adedi ve
// görüntülenen trace adedi — self-obs span'lerinden TÜRETİLEMEZ: kök örnekleme
// %10, başarılı/başarısız giriş ayrımı ve kullanıcı kimliği span'de yok. Bu
// yüzden her pod, bu iki olayı dakikalık hücreler hâlinde sayar
// (api/activity_meter.go) ve 30 sn'de bir bu seriye yazar. Desen ES istek
// sayacının eşidir (self_es_series.go, v0.10.1878): kalıcı seri, çünkü
// çok-pod kurulumda sayaç pod başınadır ve özet tüm pod'ların toplamını
// göstermeli; pod yeniden başlayınca geçmiş kaybolmamalı.
//
// Seri: `coremetry.self.activity`, etiketler `kind=login|trace_view`, `pod`.
// Sayı yalnız adet taşır — kullanıcı kimliği, e-posta, trace kimliği serinin
// hiçbir yerine YAZILMAZ.
//
// Bunlar binary'nin KENDİ yazdığı serilerdir → SelfWrittenMetrics kaydında
// (ingest-stall alarmı onları "dışarıdan metrik geliyor" saymaz).

import (
	"context"
	"time"

	"github.com/cespare/xxhash/v2"
)

const (
	// SelfActivityMetric — dakikadaki olay sayısı (kind başına).
	SelfActivityMetric  = "coremetry.self.activity"
	selfActivityService = "coremetry-self/activity"

	// ActivityKindLogin / ActivityKindTraceView — kapalı küme (kardinalite sınırı).
	ActivityKindLogin     = "login"
	ActivityKindTraceView = "trace_view"
)

// SelfActivityCell — bir (dakika, tür) hücresi.
type SelfActivityCell struct {
	Minute time.Time
	Kind   string
	Count  uint64
}

// SelfActivityPoints — SAF: hücreler → metrik noktaları. Sıfır sayımlı hücre
// ve kapalı kümenin dışındaki tür yazılmaz.
func SelfActivityPoints(cells []SelfActivityCell, pod string) []*MetricPoint {
	out := make([]*MetricPoint, 0, len(cells))
	keys := []string{"kind", "pod"}
	for _, c := range cells {
		if c.Count == 0 || (c.Kind != ActivityKindLogin && c.Kind != ActivityKindTraceView) {
			continue
		}
		t := c.Minute.UTC().Truncate(time.Minute)
		out = append(out, &MetricPoint{
			Metric: SelfActivityMetric, Instrument: "gauge", Unit: "1",
			Description: "Logins and trace views per minute (Coremetry self-observability)",
			ServiceName: selfActivityService, HostName: pod, Time: t, StartTime: t, Value: float64(c.Count),
			AttrKeys: keys, AttrValues: []string{c.Kind, pod},
			SeriesFingerprint: xxhash.Sum64String(SelfActivityMetric + "\x00" + c.Kind + "\x00" + pod),
		})
	}
	return out
}

// SelfActivityTotals — [from, to) penceresinde tüm pod'ların toplamı.
type SelfActivityTotals struct {
	Logins       uint64
	TracesViewed uint64
}

// selfActivityTotalsSQL — SAF: okuma sorgusu. Zaman sınırlı, LIMIT'li, süre
// sınırlı; tablo niteliksiz; `kind` kapalı kümeden olduğu için LIMIT 16 bol.
func selfActivityTotalsSQL() string {
	return `
		SELECT attr_values[indexOf(attr_keys, 'kind')] AS kind, sum(value) AS n
		FROM metric_points
		WHERE service_name = ? AND metric = ? AND time >= ? AND time < ?
		GROUP BY kind
		LIMIT 16
		SETTINGS max_execution_time = 5`
}

// ReadSelfActivityTotals — [from, to) penceresinde giriş ve trace görüntüleme
// toplamları (tüm pod'lar).
func (s *Store) ReadSelfActivityTotals(ctx context.Context, from, to time.Time) (SelfActivityTotals, error) {
	var out SelfActivityTotals
	rows, err := s.telemetryReadConn().Query(ctx, selfActivityTotalsSQL(),
		selfActivityService, SelfActivityMetric, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var n float64
		if err := rows.Scan(&kind, &n); err != nil {
			return out, err
		}
		if n < 0 {
			n = 0
		}
		switch kind {
		case ActivityKindLogin:
			out.Logins = uint64(n)
		case ActivityKindTraceView:
			out.TracesViewed = uint64(n)
		}
	}
	return out, rows.Err()
}
