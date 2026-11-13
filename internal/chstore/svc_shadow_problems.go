package chstore

// svc_shadow_problems.go — v0.10.2089 (spec docs/plans/spec-ml-service-level.md
// dilim 3): eski servis düzeyi Problem'leri (`svc-slowdown:<svc>`,
// `anomaly:<svc>:error_rate`) — ML gölge karşılaştırmasının "eski" kolu.
// problems düşük hacimli state tablosu (RMT, partition yok, FINAL id'ye
// göre kesin); zaman sınırı started_at, LIMIT + max_execution_time.

import (
	"context"
	"time"
)

// ServiceLevelProblemsCap — karşılaştırma okuma tavanı.
const ServiceLevelProblemsCap = 1000

// ListServiceLevelProblems — since'ten beri BAŞLAMIŞ eski servis düzeyi
// Problem'leri (açık + kapalı), en yeni önce.
func (s *Store) ListServiceLevelProblems(ctx context.Context, since time.Time) ([]Problem, error) {
	rows, err := s.conn.Query(ctx, `
		SELECT `+s.problemSelectExpr()+`
		FROM problems FINAL
		WHERE started_at >= ?
		  AND (startsWith(rule_id, 'svc-slowdown:') OR (startsWith(rule_id, 'anomaly:') AND endsWith(rule_id, ':error_rate')))
		ORDER BY started_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 10`, chDateTime64Arg(since), ServiceLevelProblemsCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Problem
	for rows.Next() {
		p, err := scanProblemRow(rows, s.problemCols())
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PreviewProblemPriority — SAF: Problem'e computePriority'nin vereceği
// öncelik + gerekçe (ML servis düzeyi gölge kapısı: "açsaydı hangi öncelik").
// Okuma / bildirim hattıyla AYNI işlev, aynı config.
func PreviewProblemPriority(p Problem, nowNs int64, cfg ProblemPriorityConfig) (string, string) {
	return computePriority(p, nowNs, cfg)
}
