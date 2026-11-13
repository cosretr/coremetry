package api

// annotation_feed_test.go — v0.10.2081: zaman şeridi ML olayını da çizer;
// ML olayının hedefi ML olay sayfası (eski /anomalies?event= ML kimliğini
// çözemez), eski olay eski hedefi korur.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestAnomalyAnnotation(t *testing.T) {
	old := anomalyAnnotation(chstore.AnomalyEvent{ID: "a1", Kind: "trace_op", Pattern: "GET /x", Service: "orders-api", StartedAt: 5})
	if old.TargetType != "anomaly" || old.TargetID != "a1" || old.Link != "" {
		t.Fatalf("eski olay hedefi değişti: %+v", old)
	}
	ml := anomalyAnnotation(chstore.AnomalyEvent{ID: "m/1", Kind: "ml:new_template", Pattern: "Batch job <*> failed", Service: "orders-api", StartedAt: 5})
	if ml.TargetType != "event" || ml.Link != "/anomalies/ml/m%2F1" || ml.Kind != "anomaly" || ml.TS != 5 {
		t.Fatalf("ML olay hedefi: %+v", ml)
	}
}
