package evaluator

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1080 — ES log deseni örneklemde kısmen doğrulandıysa terfi
// Problem'inin açıklaması sayının bir tahmin olduğunu söyler; r 0 (CH /
// örneklenmedi / başka tür) ve r 1'de metin bugünküyle bayt bayt aynı.
func TestAnomalyPromotionDescription_VerifiedNote(t *testing.T) {
	ev := chstore.AnomalyEvent{Kind: "log_pattern", Pattern: "Oracle TNS errors", PeakRatio: 60, CurrentCount: 36}
	base := "Auto-promoted from anomaly: log_pattern / Oracle TNS errors (peak ratio 60.0×, count 36)"
	for _, c := range []struct {
		r    float64
		want string
	}{
		{0, base},
		{1, base},
		{0.4, "Auto-promoted from anomaly: log_pattern / Oracle TNS errors (peak ratio 60.0×, count 36; örneklemde %40 regex doğrulandı)"},
	} {
		ev.VerifiedRatio = c.r
		if got := anomalyPromotionDescription(ev); got != c.want {
			t.Errorf("r=%v:\n got %q\nwant %q", c.r, got, c.want)
		}
	}
}
