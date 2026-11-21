package evaluator

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSamplingShadowObs(t *testing.T) {
	m := chstore.SamplingShadowModel{ErrWeight: 1, OkWeight: 20}
	r := chstore.AlertRule{ID: "r1", Name: "High error rate", Metric: "error_rate", Comparator: ">", Threshold: 5, WindowSec: 300}

	// stored 8 % fires today; weighted ≈ 0.43 % would not.
	o, ok := samplingShadowObs(m, true, r, "svc-a", 8, false, true)
	if !ok || !o.TodayFires || o.WeightedFires || o.Weighted > 0.5 || o.Family != chstore.RuleShadowFamilyAlert {
		t.Fatalf("obs %+v ok=%v", o, ok)
	}
	// stored 60 % → weighted ≈ 7 %: fires both ways.
	if o, _ := samplingShadowObs(m, true, r, "svc-b", 60, false, true); !o.WeightedFires {
		t.Fatalf("high rate %+v", o)
	}
	// not shadowed: no model, VM source, http_4xx_rate (model does not apply).
	if _, ok := samplingShadowObs(m, false, r, "svc", 8, false, true); ok {
		t.Fatal("no model")
	}
	if _, ok := samplingShadowObs(m, true, r, "svc", 8, true, true); ok {
		t.Fatal("VM source is unsampled")
	}
	// v0.10.2442: sub-5m windows and http_5xx_rate are weighted → shadowed.
	short := r
	short.WindowSec = 60
	if _, ok := samplingShadowObs(m, true, short, "svc", 8, false, true); !ok {
		t.Fatal("sub-5m window is weighted since v0.10.2442")
	}
	h := r
	h.Metric = "http_5xx_rate"
	if _, ok := samplingShadowObs(m, true, h, "svc", 8, false, true); !ok {
		t.Fatal("http_5xx_rate is weighted since v0.10.2442")
	}
	h.Metric = "http_4xx_rate"
	if _, ok := samplingShadowObs(m, true, h, "svc", 8, false, true); ok {
		t.Fatal("http_4xx_rate: 4xx spans are not error spans")
	}
}
