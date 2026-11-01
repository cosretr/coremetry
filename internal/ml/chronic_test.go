package ml

// v0.10.1769 (B2) — op_latency kronik desen: sentetik seri, dakikada bir tek
// Kafka timeout'u (yavaş oran ≈ %0.3, 0.5 istek/sn) → etkilenen istek < 5 →
// olay YOK; sürekli yavaşlama (%40, 5 istek/sn) → olay; ≥5 epizot → kronik.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSlowRatioSporadicNoEvent(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	cfg.MinBaselineRate = 0
	base := make([]float64, 30)
	for i := range base {
		base[i] = 0.001
	}
	j := judge([]float64{0.003, 0.004}, base, cfg, true)
	if a := slowAffected(j.Observed, 0.5, 300); j.Fire && a >= float64(cfg.MinAffectedRequests) {
		t.Fatalf("tek tek timeout'lar olay değil: fire=%v affected=%.2f", j.Fire, a)
	}
	if effMAD(0.001, 0) != 0.005 || effMAD(100, 0) != 10 {
		t.Fatal("oran serisi tabanı 0.005, sayım tabanı 1")
	}
	j = judge([]float64{0.35, 0.4}, base, cfg, true)
	if a := slowAffected(j.Observed, 5, 300); !j.Fire || a < float64(cfg.MinAffectedRequests) {
		t.Fatalf("sürekli yavaşlama olay: fire=%v affected=%.0f", j.Fire, a)
	}
	if isChronic(4, 5) || !isChronic(5, 5) || isChronic(9, 0) {
		t.Fatal("kronik eşiği")
	}
	q := slowShareQuery(anomaly.REDFamilyPins()[1], "service_name", "rpc_method", 300, 1)
	if q != `histogram_share(1000, sum by (service_name, rpc_method, le) (rate(rpc_server_duration_milliseconds_bucket[300s])))` {
		t.Fatalf("ms ailesi eşik 1000: %s", q)
	}
}
