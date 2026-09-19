package chstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.811 — /ai kırılımları boş pencerede `[]` (null değil): FE `for…of` /
// `.map` null'da patlıyordu (operatör hatası, prod).
func TestAIStatsBreakdownsNeverNull(t *testing.T) {
	b, err := json.Marshal(newAIStats())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"bySurface":[]`, `"byProvider":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("eksik %s: %s", want, s)
		}
	}
	if strings.Contains(s, "null") {
		t.Errorf("null alan: %s", s)
	}
}
