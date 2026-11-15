package ml

import (
	"encoding/json"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2247 (Dilim G) — kart kaydı: geçerli kayıt döner; yok / invalid /
// çözülemeyen kayıt nil (kart eski yola düşer).
func TestCardRecordView(t *testing.T) {
	raw, _ := json.Marshal(gateLatRec())
	ev := chstore.MLEvent{Detector: "op_latency", Record: string(raw), PublishState: PublishPublished}
	v := CardRecordView(&ev, CardInput{})
	if v == nil || v.Detector != "op_latency" || len(v.Snapshot.V) == 0 || v.Series.Unit != "ms" {
		t.Fatalf("geçerli kayıt dönmeli: %+v", v)
	}
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["snapshot"]; !ok || m["detector"] != "op_latency" {
		t.Fatalf("JSON düz alanlar taşımalı: %s", b)
	}
	inv := ev
	inv.PublishState = PublishInvalid
	bad := ev
	bad.Record = "{"
	for name, e := range map[string]*chstore.MLEvent{"nil": nil, "eski": {Detector: "x"}, "invalid": &inv, "bozuk": &bad} {
		if CardRecordView(e, CardInput{}) != nil {
			t.Errorf("%s: nil beklenir", name)
		}
	}
	metric := ev
	if v := CardRecordView(&metric, CardInput{Signal: chstore.MLSignalMetric, Part: "metric_k8s|memory@cluster-a", MetricLabel: "Bellek"}); v == nil || v.MetricID != "memory" || v.MetricLabel != "Bellek" {
		t.Fatalf("metrik kimliği: %+v", v)
	}
}
