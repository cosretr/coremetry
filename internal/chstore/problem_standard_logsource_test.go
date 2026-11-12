package chstore

// v0.10.2038 — Operator-reported: log anomalisi (yeni log kalıbı) detayının
// üst kutusu "KAYNAK: span · örneklenmiş" diyordu. Log türlerinin kaynağı log.

import "testing"

func TestAnomalyEventSourceByKind(t *testing.T) {
	for kind, want := range map[string]string{
		"log_template_new": StdSourceLogs, "log_pattern": StdSourceLogs, "new_template": StdSourceLogs, "elastic_ml": StdSourceLogs,
		"trace_op": StdSourceSpan, "trace_op_latency": StdSourceSpan, "behavior_change": StdSourceSpan,
	} {
		if got := anomalyEventSource(kind); got != want {
			t.Errorf("%s → %s, istenen %s", kind, got, want)
		}
		std := StandardizeAnomalyEvent(AnomalyEvent{ID: "x", Kind: kind, Service: "orders-batch"}, StdFacts{})
		if std.Evidence.Source != want {
			t.Errorf("%s standart kaynağı %s", kind, std.Evidence.Source)
		}
	}
}
