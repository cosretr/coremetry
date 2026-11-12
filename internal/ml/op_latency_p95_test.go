package ml

// v0.10.2059 — Davis S1 (docs/anomaly/davis-ml-gap.md satır 1): op_latency
// p95 ölçüsü. Sözleşme: aynı endpoint yavaşlaması TEK olay (aynı kimlik);
// p95 ateşlerse ölçü p95 (ms), yalnız yavaş istek oranı ateşlerse eski B2
// ölçüsü. p95 için iki gürültü kapısı (v0.10.1769 B2 olayı: tek tük yavaş
// istek "normalin 164×" açıyordu): mutlak taban 100 ms ve p95'in temsil
// ettiği istek (%5 × hız × kova) ≥ minAffectedRequests.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestOpLatencyMeasure(t *testing.T) {
	cfg := chstore.DefaultMLConfig() // bucket 300 s, minAffected 5
	fire := func(obs float64) Judgement { return Judgement{Fire: true, Observed: obs, Median: obs / 7} }
	quiet := Judgement{}
	cases := []struct {
		name         string
		slow         Judgement
		slowAffected float64
		p95          Judgement
		rate         float64 // istek/sn
		want         string
	}{
		{"hiçbiri", quiet, 0, quiet, 10, ""},
		{"yalnız p95 (50→400 ms)", quiet, 0, fire(400), 10, "p95"},
		{"ikisi de → tek olay, p95", fire(0.3), 900, fire(2400), 10, "p95"},
		{"yalnız yavaş oran", fire(0.2), 600, quiet, 10, "slow"},
		{"p95 tabanın altında (80 ms)", quiet, 0, fire(80), 10, ""},
		{"p95 tam taban 100 ms", quiet, 0, fire(100), 10, "p95"},
		{"az trafik: %5 × 0,2/sn × 300 sn = 3 istek < 5", quiet, 0, fire(900), 0.2, ""},
		{"az trafik p95 düşer, yavaş oran kalır", fire(0.5), 30, fire(900), 0.2, "slow"},
		{"yavaş oran etkilenen < 5", fire(0.5), 4, quiet, 10, ""},
	}
	for _, c := range cases {
		if got := opLatencyMeasure(c.slow, c.slowAffected, c.p95, c.rate, cfg); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

// eventPart ölçüyü kanıttan okur: p95 olayının serisi "route|p95", eski olay
// (measure yok ya da slow_ratio) "route|slow" — kart grafiği ve oynatma doğru
// seriyi çizer.
func TestOpLatencyEventPart(t *testing.T) {
	for _, c := range []struct{ ev, want string }{
		{`{"measure":"p95"}`, "/v1/orders|p95"},
		{`{"measure":"slow_ratio"}`, "/v1/orders|slow"},
		{``, "/v1/orders|slow"},
	} {
		ev := chstore.MLEvent{Signal: chstore.MLSignalTrace, Detector: detOpLatency, Code: "/v1/orders", Evidence: c.ev}
		if got := eventPart(ev); got != c.want {
			t.Errorf("%q: %s, beklenen %s", c.ev, got, c.want)
		}
	}
}

// Kart cümlesi değer + normal ("p95 ... ; bu saatte normalde ...") ve
// endpoint'i adlandırır.
func TestOpLatencyP95Card(t *testing.T) {
	in := CardInput{Detector: detOpLatency, Signal: chstore.MLSignalTrace, Service: "orders-api", Code: "POST /pay", Part: "POST /pay|p95",
		Observed: 420, Baseline: 60, Direction: "up"}
	sp := specFor(in)
	if sp.unit != unitMs || sp.measure == "" {
		t.Fatalf("p95 spec: %+v", sp)
	}
	if w := whatOf(in, sp); w != "orders-api servisinin POST /pay endpoint'i yavaşladı." {
		t.Fatalf("ne oldu: %q", w)
	}
	slow := specFor(CardInput{Detector: detOpLatency, Part: "POST /pay|slow"})
	if slow.unit != unitRatio {
		t.Fatalf("eski ölçü değişmemeli: %+v", slow)
	}
}
