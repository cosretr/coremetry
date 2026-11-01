package ml

// v0.10.1752 — replay kaydı: olay → seri parçası, tetiklenen kovalar (bant),
// tam kayıt zamanlaması (+2 sa, bir kez).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEventPart(t *testing.T) {
	cases := []struct {
		ev   chstore.MLEvent
		want string
	}{
		{chstore.MLEvent{Signal: "log", Detector: "log_volume"}, partTotal},
		{chstore.MLEvent{Signal: "log", Detector: "error_rate", Level: "error"}, partError},
		{chstore.MLEvent{Signal: "trace", Detector: "op_latency", Code: "/pay"}, "/pay|slow"},
		{chstore.MLEvent{Signal: "trace", Detector: "op_error_rate", Code: "/pay"}, "/pay|err_ratio"},
		{chstore.MLEvent{Signal: "trace", Detector: "throughput_drop", Code: "/pay"}, "/pay|count"},
		{chstore.MLEvent{Signal: "metric", Detector: "metric_jvm", Code: "metric_jvm|heap_used"}, "metric_jvm|heap_used"},
	}
	for _, c := range cases {
		if got := eventPart(c.ev); got != c.want {
			t.Errorf("eventPart(%+v)=%q want %q", c.ev, got, c.want)
		}
	}
}

func TestTriggeredBuckets(t *testing.T) {
	pts := []chstore.MLPoint{{T: 0, V: 100}, {T: 300, V: 200}, {T: 600, V: 300}, {T: 900, V: 20}}
	// med 100, mad 0 → eff 10; z 4 → eşik 140 / 60
	if got := triggeredBuckets(pts, 100, 0, 4, 0, 900, "up"); len(got) != 2 || got[0] != 300 {
		t.Fatalf("up=%v", got)
	}
	if got := triggeredBuckets(pts, 100, 0, 4, 0, 1200, "down"); len(got) != 1 || got[0] != 900 {
		t.Fatalf("down=%v", got)
	}
}

func TestDueCompleteOnce(t *testing.T) {
	tr := newTracker()
	t0 := time.Unix(1_800_000_000, 0)
	ev := chstore.MLEvent{ID: "a", Detector: "log_volume", Service: "pay"}
	_, ep := tr.observe(ev, t0, 5*time.Minute)
	ev.ID = ep
	tr.setEvent(ev)
	if len(tr.dueComplete(t0.Add(3*time.Hour), 2*time.Hour)) != 0 {
		t.Fatal("ön kayıt yokken tam kayıt yok")
	}
	tr.snap(ep).pre = true
	if len(tr.dueComplete(t0.Add(time.Hour), 2*time.Hour)) != 0 {
		t.Fatal("+2 sa dolmadan tam kayıt yok")
	}
	if got := tr.dueComplete(t0.Add(2*time.Hour), 2*time.Hour); len(got) != 1 || got[0].ID != ep {
		t.Fatalf("got %+v", got)
	}
	tr.snap(ep).done = true
	if len(tr.dueComplete(t0.Add(3*time.Hour), 2*time.Hour)) != 0 {
		t.Fatal("tam kayıt bir kez")
	}
}
