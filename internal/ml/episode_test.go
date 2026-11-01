package ml

// v0.10.1768 (B3) — epizot modeli: sürerken aynı kimlik; 3 tik görülmeyince
// kapanır (stage=closed); 30 dk içinde tekrar aynı epizot; sonra yeni kimlik.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEpisodeLifecycle(t *testing.T) {
	tr := newTracker()
	tick := 5 * time.Minute
	t0 := time.Unix(1_800_000_000, 0)
	ev := chstore.MLEvent{ID: "base", Detector: "pod_outlier", Service: "pay", Signal: "trace"}
	first, ep1 := tr.observe(ev, t0, tick)
	ev.ID = ep1
	tr.setEvent(ev)
	if _, ep := tr.observe(chstore.MLEvent{ID: "base"}, t0.Add(tick), tick); ep != ep1 || !first.Equal(t0) {
		t.Fatal("sürerken aynı epizot")
	}
	if len(tr.closeStale(t0.Add(tick))) != 0 {
		t.Fatal("bu tikte görülen koşu kapanmaz")
	}
	// 2 tik görülmedi → açık; 3. tik → kapandı
	tr.closeStale(t0.Add(2 * tick))
	tr.closeStale(t0.Add(3 * tick))
	closed := tr.closeStale(t0.Add(4 * tick))
	if len(closed) != 1 || closed[0].ID != ep1 || closed[0].Stage != chstore.MLStageClosed {
		t.Fatalf("kapanış: %+v", closed)
	}
	if len(tr.closeStale(t0.Add(5*tick))) != 0 {
		t.Fatal("kapanış bir kez")
	}
	// 30 dk içinde tekrar → aynı epizot yeniden açılır
	if f, ep := tr.observe(chstore.MLEvent{ID: "base"}, t0.Add(4*tick+20*time.Minute), tick); ep != ep1 || !f.Equal(t0) {
		t.Fatal("30 dk içinde tekrar aynı epizot")
	}
	tr.closeStale(t0.Add(8 * tick))
	tr.closeStale(t0.Add(9 * tick))
	tr.closeStale(t0.Add(10 * tick))
	// 30 dk sonra → yeni epizot, yeni kimlik
	if f, ep := tr.observe(chstore.MLEvent{ID: "base"}, t0.Add(10*tick+time.Hour), tick); ep == ep1 || f.Equal(t0) {
		t.Fatal("30 dk sonra yeni epizot")
	}
	if chstore.MLEpisodeID("x", t0) == chstore.MLEpisodeID("x", t0.Add(time.Second)) {
		t.Fatal("epizot kimliği başlangıca bağlı")
	}
}
