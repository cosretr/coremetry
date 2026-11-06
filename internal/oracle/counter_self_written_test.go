package oracle

// v0.10.1858 — sayaç serisinin adı chstore'un "iç yazar" kaydıyla aynı
// kalmalı; ayrışırsa self-ingest-stall alarmı yeniden körleşir (sayaç
// metric_catalog'u taze tutar). Bkz. chstore/self_written_metrics.go.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestCounterMetricIsRegisteredSelfWritten(t *testing.T) {
	if counterMetric != chstore.OracleErrorCounterMetric {
		t.Fatalf("counterMetric = %q, chstore.OracleErrorCounterMetric = %q", counterMetric, chstore.OracleErrorCounterMetric)
	}
	for _, m := range chstore.SelfWrittenMetrics() {
		if m == counterMetric {
			return
		}
	}
	t.Fatalf("%q SelfWrittenMetrics kaydında yok", counterMetric)
}
