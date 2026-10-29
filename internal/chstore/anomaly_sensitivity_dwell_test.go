package chstore

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: trace_op / log_pattern sürdürme
// vidaları varsayılan 2, aralık 1–6, Normalize PUT'ta korur.

import (
	"encoding/json"
	"testing"
)

func TestTraceOpLogPatternDwellDefaultsAndClamp(t *testing.T) {
	d := DefaultAnomalySensitivity()
	if d.TraceOpDwell() != 2 || d.LogPatternDwell() != 2 {
		t.Fatalf("varsayılan 2/2: %d/%d", d.TraceOpDwell(), d.LogPatternDwell())
	}
	for _, c := range []struct{ in, want int }{{0, 2}, {1, 1}, {6, 6}, {7, 2}, {-1, 2}} {
		cfg := AnomalySensitivityConfig{TraceOpDwellBuckets: c.in, LogPatternDwellBuckets: c.in}
		if cfg.TraceOpDwell() != c.want || cfg.LogPatternDwell() != c.want {
			t.Errorf("in=%d: %d/%d, want %d", c.in, cfg.TraceOpDwell(), cfg.LogPatternDwell(), c.want)
		}
	}
	n := NormalizeAnomalySensitivity(AnomalySensitivityConfig{TraceOpDwellBuckets: 3, LogPatternDwellBuckets: 1})
	b, _ := json.Marshal(n)
	var back AnomalySensitivityConfig
	_ = json.Unmarshal(b, &back)
	if back.TraceOpDwellBuckets != 3 || back.LogPatternDwellBuckets != 1 {
		t.Fatalf("Normalize/JSON gidiş-dönüş kaybetti: %s", b)
	}
}
