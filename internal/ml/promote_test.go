package ml

// v0.10.1761 — terfi kopya yolu: varsayılanda hiçbir terfi yok (okuma da
// yok), kapı geçilince kopya; elastic_ml çakışması dup_of.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestPromotionDecision(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	if anyPromotionOn(cfg) {
		t.Fatal("varsayılan: terfi kapalı")
	}
	ev := chstore.MLEvent{ID: "a", Detector: "error_rate", Signal: "log", Service: "pay", Level: "error", StartedAt: 100, LastSeen: 200, Score: 6, Observed: 50, Baseline: 5}
	good := chstore.MLDetectorScore{Samples: 30, Hits: 28, HitRate: 0.93}
	if ok, _ := promotionDecision(ev, cfg, good, "Europe/Istanbul", nil); ok {
		t.Fatal("anahtarlar kapalıyken terfi yok")
	}
	cfg.Enabled, cfg.Detectors["error_rate"] = true, true
	cfg.Promotion["error_rate"] = chstore.MLPromotion{Enabled: true, MinHitRate: 0.8, MinSamples: 20}
	if !anyPromotionOn(cfg) {
		t.Fatal("açık terfi görülmeli")
	}
	if ok, _ := promotionDecision(ev, cfg, good, "", nil); ok {
		t.Fatal("takvimsiz terfi yok")
	}
	if ok, _ := promotionDecision(ev, cfg, good, "Europe/Istanbul", nil); !ok {
		t.Fatal("kapı geçildi → terfi")
	}
	el := []chstore.AnomalyEvent{{ID: "e1", Kind: "elastic_ml", Service: "pay", StartedAt: 150, LastSeen: 300}}
	if ok, dup := promotionDecision(ev, cfg, good, "Europe/Istanbul", el); ok || dup != "e1" {
		t.Fatalf("elastic_ml çakışması: ok=%v dup=%s", ok, dup)
	}
	c := promotedCopy(ev)
	if c.Kind != "ml:error_rate" || c.Pattern != "_error" || c.ID != "a" || c.CurrentCount != 50 {
		t.Fatalf("kopya=%+v", c)
	}
}
