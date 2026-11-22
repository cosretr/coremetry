package chstore

import (
	"context"
	"sort"
	"time"
)

// service_messaging_spans.go — v0.10.2450 (operatör 2026-11-22): Service →
// Messaging sekmesinin SPAN YEDEĞİ. Sekme önce Kafka istemci METRİKLERİNİ
// (VictoriaMetrics, örneklenmemiş) okur; servis hiçbir aileyi yaymıyorsa
// (api/service_kafka_family.go çözücüsü "spans" der) topic tablosu +
// grafikler bu okumadan gelir: messaging_caller_summary_{1m,5m}, servis
// süzgeçli (service_name yalnız caller MV'sinde). Lag YOK (span'den
// türemez) — FE bunu söyler.
//
// Grenlik messaging_summary_1m ile AYNI kural (msgTrendGrainFor): ≤ 3 sa VE
// 1m tablo pencere başını kapsıyor → 1 dk; aksi 5 dk. Boşluk çizilmez.

// Span yedeği kaynakları — SQL'e YALNIZ bu sabitlerden girer.
const (
	svcMsgTableFine   = "messaging_caller_summary_1m"
	svcMsgTableCoarse = "messaging_caller_summary_5m"
	// svcMsgTopicLimit — tablo satırı tavanı; grafikler ilk svcMsgSeriesTopics.
	svcMsgTopicLimit   = 200
	svcMsgSeriesTopics = 10
)

// ServiceMsgTopic — servis × topic × yön (producer | consumer) pencere RED'i.
type ServiceMsgTopic struct {
	System      string  `json:"system"`
	Cluster     string  `json:"cluster"`
	Destination string  `json:"destination"`
	Kind        string  `json:"kind"`
	Count       uint64  `json:"count"`
	Errors      uint64  `json:"errors"`
	Rate        float64 `json:"rate"` // span / sn (pencere boyu)
	P95Ms       float64 `json:"p95Ms"`
}

// ServiceMsgPoint — tek kova; Rate = kova span'i / BucketSec.
type ServiceMsgPoint struct {
	T      int64   `json:"t"`
	Rate   float64 `json:"rate"`
	ErrPct float64 `json:"errPct"`
	P95Ms  float64 `json:"p95Ms"`
}

// ServiceMsgSeries — (topic, yön) kova serisi; yalnız ilk svcMsgSeriesTopics topic.
type ServiceMsgSeries struct {
	Destination string            `json:"destination"`
	Kind        string            `json:"kind"`
	Points      []ServiceMsgPoint `json:"points"`
}

// ServiceMessagingSpans — span yedeğinin yükü.
type ServiceMessagingSpans struct {
	Source    string             `json:"source"` // messaging_caller_summary_1m | _5m
	BucketSec int                `json:"bucketSec"`
	Topics    []ServiceMsgTopic  `json:"topics"`
	Series    []ServiceMsgSeries `json:"series"`
}

// svcMsgGrainFor — SAF; msgTrendGrainFor'un caller-MV eşi (aynı eşik).
func svcMsgGrainFor(from, to time.Time, fineCovers bool) DBTrendGrain {
	g := msgTrendGrainFor(from, to, fineCovers)
	if g.BucketSec == 60 {
		return DBTrendGrain{Table: svcMsgTableFine, BucketSec: 60}
	}
	return DBTrendGrain{Table: svcMsgTableCoarse, BucketSec: 300}
}

// ServiceMessagingGrain — pencere için kaynağı seçer (60 sn önbellekli prob).
func (s *Store) ServiceMessagingGrain(ctx context.Context, from, to time.Time) DBTrendGrain {
	fine := false
	if w := to.Sub(from); w > 0 && w <= msgTrendFineMaxWindow {
		fine = s.sourceCovers(ctx, priorSrcMsgCallerSummary1m, from.Truncate(time.Minute))
	}
	return svcMsgGrainFor(from, to, fine)
}

func svcMsgTable(t string) string {
	if t != svcMsgTableFine {
		return svcMsgTableCoarse
	}
	return t
}

// svcMsgTopicsSQL — SAF: servisin topic × yön toplamı. Üç sınır.
func svcMsgTopicsSQL(table string) string {
	return `
		SELECT msg_system, cluster, destination, kind,
		       countMerge(span_count_state)  AS span_count,
		       countMerge(error_count_state) AS error_count,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM ` + svcMsgTable(table) + `
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		  AND kind IN ('producer', 'consumer')
		GROUP BY msg_system, cluster, destination, kind
		ORDER BY span_count DESC
		LIMIT 200
		SETTINGS max_execution_time = 10`
}

// svcMsgSeriesSQL — SAF: ilk topic'lerin kova serisi. Üç sınır.
func svcMsgSeriesSQL(table string) string {
	return `
		SELECT destination, kind,
		       toUnixTimestamp64Nano(toDateTime64(time_bucket, 9)) AS bucket_ns,
		       countMerge(span_count_state)  AS span_count,
		       countMerge(error_count_state) AS error_count,
		       arrayElement(quantilesTDigestMerge(0.5, 0.95, 0.99)(duration_q_state), 2) / 1e6 AS p95_ms
		FROM ` + svcMsgTable(table) + `
		WHERE service_name = ? AND time_bucket >= ? AND time_bucket < ?
		  AND kind IN ('producer', 'consumer') AND destination IN ?
		GROUP BY destination, kind, time_bucket
		ORDER BY destination, kind, time_bucket
		LIMIT 20000
		SETTINGS max_execution_time = 10`
}

// svcMsgSeriesTopicsOf — SAF: grafiklere giren topic'ler (hacme göre ilk N,
// tekil; topics zaten span_count DESC).
func svcMsgSeriesTopicsOf(topics []ServiceMsgTopic, n int) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range topics {
		if len(out) >= n {
			break
		}
		if !seen[t.Destination] {
			seen[t.Destination] = true
			out = append(out, t.Destination)
		}
	}
	return out
}

// GetServiceMessagingSpans — servisin span yedeği (g: ServiceMessagingGrain).
func (s *Store) GetServiceMessagingSpans(
	ctx context.Context, g DBTrendGrain, service string, from, to time.Time,
) (*ServiceMessagingSpans, error) {
	if g.Table != svcMsgTableFine {
		g = DBTrendGrain{Table: svcMsgTableCoarse, BucketSec: 300}
	}
	out := &ServiceMessagingSpans{Source: g.Table, BucketSec: g.BucketSec,
		Topics: []ServiceMsgTopic{}, Series: []ServiceMsgSeries{}}
	if service == "" {
		return out, nil
	}
	bucketStart := from.Truncate(time.Duration(g.BucketSec) * time.Second)
	winSec := to.Sub(bucketStart).Seconds()
	if winSec <= 0 {
		return out, nil
	}
	rows, err := s.telemetryReadConn().Query(ctx, s.weighSQL(svcMsgTopicsSQL(g.Table), bucketStart, g.Table),
		service, bucketStart, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t ServiceMsgTopic
		var p95 *float64
		if err := rows.Scan(&t.System, &t.Cluster, &t.Destination, &t.Kind, &t.Count, &t.Errors, &p95); err != nil {
			rows.Close()
			return nil, err
		}
		t.Rate = float64(t.Count) / winSec
		t.P95Ms = safeF(p95)
		out.Topics = append(out.Topics, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	dests := svcMsgSeriesTopicsOf(out.Topics, svcMsgSeriesTopics)
	if len(dests) == 0 {
		return out, nil
	}
	srows, err := s.telemetryReadConn().Query(ctx, s.weighSQL(svcMsgSeriesSQL(g.Table), bucketStart, g.Table),
		service, bucketStart, to, dests)
	if err != nil {
		return out, nil // seri best-effort: tablo yine çizilir
	}
	defer srows.Close()
	idx := map[[2]string]int{}
	for srows.Next() {
		var dest, kind string
		var ns int64
		var n, e uint64
		var p95 *float64
		if err := srows.Scan(&dest, &kind, &ns, &n, &e, &p95); err != nil {
			return out, nil
		}
		k := [2]string{dest, kind}
		i, ok := idx[k]
		if !ok {
			out.Series = append(out.Series, ServiceMsgSeries{Destination: dest, Kind: kind, Points: []ServiceMsgPoint{}})
			i = len(out.Series) - 1
			idx[k] = i
		}
		out.Series[i].Points = append(out.Series[i].Points, svcMsgPointOf(ns, n, e, p95, g.BucketSec))
	}
	sort.SliceStable(out.Series, func(a, b int) bool {
		if out.Series[a].Destination != out.Series[b].Destination {
			return out.Series[a].Destination < out.Series[b].Destination
		}
		return out.Series[a].Kind < out.Series[b].Kind
	})
	return out, nil
}

// svcMsgPointOf — SAF: kova noktası; hız kovanın KENDİ genişliğine bölünür.
func svcMsgPointOf(ns int64, n, e uint64, p95 *float64, bucketSec int) ServiceMsgPoint {
	if bucketSec <= 0 {
		bucketSec = 300
	}
	p := ServiceMsgPoint{T: ns, Rate: float64(n) / float64(bucketSec), P95Ms: safeF(p95)}
	if n > 0 {
		p.ErrPct = float64(e) / float64(n) * 100
	}
	return p
}
