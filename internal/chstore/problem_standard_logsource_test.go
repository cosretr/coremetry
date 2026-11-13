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

// v0.10.2082 — ML olayı aynı satır şeklinde (kind "ml:<dedektör>"): kaynak
// dedektörün sinyalinden, ayrıntı ML olay sayfası, etiket dedektör adı.
func TestStandardizeMLAnomalyRow(t *testing.T) {
	for kind, want := range map[string]string{
		"ml:new_template": StdSourceLogs, "ml:op_latency": StdSourceSpan, "ml:metric_kafka": StdSourceVM,
	} {
		std := StandardizeAnomalyEvent(AnomalyEvent{ID: "m/1", Kind: kind, Service: "orders-api", Pattern: "p"}, StdFacts{})
		if std.Evidence.Source != want {
			t.Errorf("%s kaynağı %s, istenen %s", kind, std.Evidence.Source, want)
		}
		if std.NextStep.Href != "/anomalies/ml/m%2F1" {
			t.Errorf("%s ayrıntı bağlantısı %s", kind, std.NextStep.Href)
		}
	}
	std := StandardizeAnomalyEvent(AnomalyEvent{ID: "m", Kind: "ml:new_template", Service: "orders-api", Pattern: "Batch job <*> failed"}, StdFacts{})
	if std.Family != StdFamilyError || std.Headline == "" {
		t.Errorf("new_template ailesi / başlık: %+v", std)
	}
	old := StandardizeAnomalyEvent(AnomalyEvent{ID: "a", Kind: "log_template_new", Service: "orders-api"}, StdFacts{})
	if old.NextStep.Href != "/inbox?anomaly=a" {
		t.Errorf("eski olayın bağlantısı değişti: %s", old.NextStep.Href)
	}
}
