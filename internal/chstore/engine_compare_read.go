package chstore

// engine_compare_read.go — v0.10.2309: "Davis özneleri ↔ ML" karşılaştırma
// raporunun (GET /api/anomalies/engine-compare) iki yalın okuması. Özne kolu
// mevcut ListAnomalyEvents{Kind: subject_red} — burada değil.
//
//  1. ML: yalnız eşlenen dedektörler, hafif kolonlar + evidence (gölge /
//     canlı kararı ml.GroupShadowOf / ml.SvcShadowOf ile okunur, SQL'de
//     JSONExtract YOK). stage != 'chronic' (tekrarlayan desen epizot değil),
//     dup_of = '' (dup_of yazıcısı v0.10.2067'de promote.go ile silindi;
//     30 g TTL içindeki eski işaretli satır elastic_ml kopyasıdır, sayılmaz).
//     metric_kafka yalnız consumer_lag parçası, dependency_* yalnız messaging
//     ailesi — diğer kodların Davis tarafında karşılığı yok.
//  2. Referans Problem'ler: iki anomali motorunun da açmadığı Problem'ler
//     (kural öneki anomaly-auto: / anomaly: / svc-slowdown: DEĞİL).
//     ListServiceLevelProblems'in şekli; yalın kolonlar.
//
// Her sorgu: zaman sınırı + LIMIT + max_execution_time. Şema değişikliği yok.

import (
	"context"
	"strings"
	"time"
)

// Okuma tavanları (cevap truncated bayrağı tavana değince true).
const (
	EngineCompareMLCap       = 10000
	EngineCompareProblemsCap = 2000
)

// EngineCompareMLDetectors — raporun eşlediği ML dedektörleri (ml/engine_compare.go
// aile tablosu bununla aynı kümeyi kullanır).
var EngineCompareMLDetectors = []string{
	"op_latency", "svc_breadth",
	"op_error_rate", "svc_error_rate", "trace_error_count",
	"throughput_drop",
	"metric_kafka",
	"dependency_latency", "dependency_error_rate",
}

// EngineCompareKafkaLagCodePrefix / EngineCompareMessagingCodePrefix — ML
// code önekleri (ml/kafka_topic.go part; ml/dependency.go target).
const (
	EngineCompareKafkaLagCodePrefix  = "metric_kafka|consumer_lag"
	EngineCompareMessagingCodePrefix = "messaging|"
)

// EngineCompareNonRefRulePrefixes — referans SAYILMAYAN Problem kuralları:
// terfi edilmiş anomali (anomaly-auto:, ML grup / servis / özne dahil) ve
// eski servis düzeyi anomali aileleri (ml.SvcCompareOldFamily: anomaly:,
// svc-slowdown:). Kalanlar (kural / SLO / sağlık …) iki motordan bağımsız.
var EngineCompareNonRefRulePrefixes = []string{PromotedAnomalyRulePrefix, "anomaly:", "svc-slowdown:"}

// EngineCompareMLRow — ML okumasının yalın satırı.
type EngineCompareMLRow struct {
	ID, Detector, Service, Code, Stage, Direction, Evidence string
	StartedAt, LastSeen                                     int64
}

// EngineCompareProblemRow — referans Problem (ResolvedAt 0 = açık).
type EngineCompareProblemRow struct {
	ID, Service, RuleID   string
	StartedAt, ResolvedAt int64
}

// engineCompareMLSQL — SAF: ML okuması (yer tutucular: since, detectors, limit).
func engineCompareMLSQL() string {
	return `
		SELECT id, detector, service, code, stage, direction, evidence, started_at, last_seen
		FROM ml_events FINAL
		WHERE last_seen >= ?
		  AND detector IN ?
		  AND stage != 'chronic'
		  AND dup_of = ''
		  AND (detector != 'metric_kafka' OR startsWith(code, '` + EngineCompareKafkaLagCodePrefix + `'))
		  AND (detector NOT IN ('dependency_latency', 'dependency_error_rate') OR startsWith(code, '` + EngineCompareMessagingCodePrefix + `'))
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// engineCompareProblemsSQL — SAF: referans Problem okuması (yer tutucular:
// since, limit).
func engineCompareProblemsSQL() string {
	conds := make([]string, 0, len(EngineCompareNonRefRulePrefixes))
	for _, p := range EngineCompareNonRefRulePrefixes {
		conds = append(conds, "NOT startsWith(rule_id, '"+p+"')")
	}
	return `
		SELECT id, service, rule_id, started_at, resolved_at
		FROM problems FINAL
		WHERE started_at >= ?
		  AND ` + strings.Join(conds, "\n\t\t  AND ") + `
		ORDER BY started_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// ListEngineCompareMLEvents — since'ten beri görülen eşlenen ML olayları.
func (s *Store) ListEngineCompareMLEvents(ctx context.Context, since time.Time) ([]EngineCompareMLRow, error) {
	rows, err := s.conn.Query(ctx, engineCompareMLSQL(), chDateTime64Arg(since), EngineCompareMLDetectors, EngineCompareMLCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngineCompareMLRow
	for rows.Next() {
		var e EngineCompareMLRow
		var st, ls time.Time
		if err := rows.Scan(&e.ID, &e.Detector, &e.Service, &e.Code, &e.Stage, &e.Direction, &e.Evidence, &st, &ls); err != nil {
			return nil, err
		}
		e.StartedAt, e.LastSeen = st.UnixNano(), ls.UnixNano()
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEngineCompareRefProblems — since'ten beri başlamış referans Problem'ler.
func (s *Store) ListEngineCompareRefProblems(ctx context.Context, since time.Time) ([]EngineCompareProblemRow, error) {
	rows, err := s.conn.Query(ctx, engineCompareProblemsSQL(), chDateTime64Arg(since), EngineCompareProblemsCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EngineCompareProblemRow
	for rows.Next() {
		var p EngineCompareProblemRow
		var st time.Time
		var rs *time.Time
		if err := rows.Scan(&p.ID, &p.Service, &p.RuleID, &st, &rs); err != nil {
			return nil, err
		}
		p.StartedAt = st.UnixNano()
		if rs != nil && !rs.IsZero() {
			p.ResolvedAt = rs.UnixNano()
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
