package api

// ml_recommend_test.go — v0.10.1886. Önerilen kurulum yanıtı: VM yokken
// ölçüm "ölçülemedi", öneri = bugünkü ayar, farklar boş DİZİ (null değil).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestMLRecommendedWithoutVM(t *testing.T) {
	cur := chstore.DefaultMLConfig()
	cur.Enabled = true
	cur.Detectors["metric_kafka"] = true
	got := mlRecommended(context.Background(), nil, cur, time.Unix(1_800_000_000, 0))
	if got.VMConfigured || len(got.Changes) != 0 || !got.Config.Detectors["metric_kafka"] {
		t.Fatalf("VM yokken öneri bugünkü ayar olmalı: %+v", got.Changes)
	}
	for _, c := range got.Checks {
		if c.Checked || c.Note == "" {
			t.Fatalf("VM yokken ölçüm: %+v", c)
		}
	}
	raw, _ := json.Marshal(got)
	if !strings.Contains(string(raw), `"changes":[]`) {
		t.Fatalf("farklar boş dizi olmalı: %s", raw)
	}
}
