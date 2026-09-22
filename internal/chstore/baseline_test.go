package chstore

import (
	"strings"
	"testing"
)

// v0.10.866 (scale-audit 09-23) — alarm baseline'ı ham spans yerine
// service_summary_5m okur: dört metrik × servisli/servissiz, pencere iki
// yönlü sınırlı, tavanlı, FROM spans YOK; bilinmeyen metrik ok=false.
func TestBaselineSQLReadsSummaryMV(t *testing.T) {
	for _, m := range []string{"p50_ms", "p95_ms", "p99_ms", "avg_ms", "error_rate", "request_rate", "error_count"} {
		for _, withSvc := range []bool{false, true} {
			q, latency, ok := baselineSQL(m, withSvc)
			if !ok {
				t.Fatalf("%s desteklenmeli", m)
			}
			if strings.Contains(q, "FROM spans") || !strings.Contains(q, "FROM service_summary_5m") {
				t.Fatalf("%s: MV okumuyor: %s", m, q)
			}
			if !strings.Contains(q, "time_bucket >= ? AND time_bucket < ?") || !strings.Contains(q, "max_execution_time = 10") {
				t.Fatalf("%s: pencere/tavan eksik: %s", m, q)
			}
			if strings.Contains(q, "service_name = ?") != withSvc {
				t.Fatalf("%s withService=%v: servis filtresi yanlış: %s", m, withSvc, q)
			}
			isLat := strings.HasSuffix(m, "_ms")
			if latency != isLat || (isLat && !strings.Contains(q, "quantilesTDigestMerge(0.5, 0.95, 0.99, 1)(duration_q_state)")) {
				t.Fatalf("%s: gecikme dalı yanlış: latency=%v %s", m, latency, q)
			}
		}
	}
	if _, _, ok := baselineSQL("bogus", false); ok {
		t.Fatal("bilinmeyen metrik desteklenmemeli")
	}
}
