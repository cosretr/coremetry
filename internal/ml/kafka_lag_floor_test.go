package ml

// v0.10.2041 — Operator-reported: "Consumer lag 12'ye çıkması sorun değil,
// daha yüksek lag'ler olması gerekirdi." Kafka lag olayı, sapma ne kadar
// büyük olursa olsun, değer Messaging "Genel bakış eşiği"nin (1.000 kayıt)
// altındaysa YAZILMAZ; açık koşu sürer. Fikstür nötr.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestKafkaLagImpactFloor(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	t0 := time.Unix(1_800_000_000, 0)
	lag := func(code string, obs, base float64) chstore.MLEvent {
		return chstore.MLEvent{ID: "k|" + code, Detector: "metric_kafka", Signal: "metric", Service: "orders-consumer",
			Code: code, Baseline: base, Observed: obs, Score: 9, Direction: "up"}
	}
	cases := []struct {
		name string
		ev   chstore.MLEvent
		want bool // yazılır mı
	}{
		{"operatör örneği: 12 (normal ~1) → yok", lag("metric_kafka|consumer_lag", 12, 1), false},
		{"önceki örnek: 162 (normal 0) → yok", lag("metric_kafka|consumer_lag", 162, 0), false},
		{"2,8 bin → yazılır", lag("metric_kafka|consumer_lag", 2800, 10), true},
		{"Thanos parçası da: 900 → yok", lag("metric_kafka|consumer_lag@prod-east", 900, 1), false},
		{"tam taban 1.000 → yazılır", lag("metric_kafka|consumer_lag", 1000, 10), true},
		{"Kafka throttle (lag değil) → taban yok", lag("metric_kafka|fetch_throttle", 12, 1), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			kept, sup := qualityFilter([]chstore.MLEvent{c.ev}, cfg, nil, newTracker(), t0)
			if got := len(kept) == 1; got != c.want {
				t.Fatalf("yazıldı=%v, istenen %v (sup=%+v)", got, c.want, sup)
			}
			if !c.want && sup.Impact != 1 {
				t.Fatalf("etki bastırması sayılmalı: %+v", sup)
			}
		})
	}
	// Açık olay: taban altına inse de sürer (yalnız yeni üretim değişir).
	tr := newTracker()
	if kept, _ := qualityFilter([]chstore.MLEvent{lag("metric_kafka|consumer_lag", 2800, 10)}, cfg, nil, tr, t0); len(kept) != 1 {
		t.Fatal("açılış yazılmalı")
	}
	if kept, _ := qualityFilter([]chstore.MLEvent{lag("metric_kafka|consumer_lag", 600, 10)}, cfg, nil, tr, t0.Add(5*time.Minute)); len(kept) != 1 {
		t.Fatal("açık koşu taban altında da sürmeli")
	}
}
