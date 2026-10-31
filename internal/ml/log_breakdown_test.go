package ml

// v0.10.1756 — log dilim 3: aykırı pod (oran medyan+MAD, ≥3 pod, ≥5 hata,
// ≥2× medyan) ve nadir hata kodu (14 g görülmemiş, servis geçmişi ≥3 g).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestPodOutliers(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	rows := []logstore.MLPodRow{{Pod: "a", Total: 100, Errors: 1}, {Pod: "b", Total: 100, Errors: 2}, {Pod: "c", Total: 100, Errors: 1}, {Pod: "d", Total: 100, Errors: 40}}
	got := podOutliers(rows, cfg)
	if len(got) != 1 || got[0].Pod != "d" || got[0].Pods != 4 {
		t.Fatalf("got %+v", got)
	}
	if podOutliers(rows[:2], cfg) != nil {
		t.Fatal("<3 pod → karar yok")
	}
	small := []logstore.MLPodRow{{Pod: "a", Total: 100}, {Pod: "b", Total: 100}, {Pod: "c", Total: 100}, {Pod: "d", Total: 100, Errors: 4}}
	if len(podOutliers(small, cfg)) != 0 {
		t.Fatal("pod hatası <5 → aykırı değil")
	}
	tiny := []logstore.MLPodRow{{Pod: "a", Total: 2}, {Pod: "b", Total: 2}, {Pod: "c", Total: 2}, {Pod: "d", Total: 100, Errors: 50}}
	if len(podOutliers(tiny, cfg)) != 0 {
		t.Fatal("hacim tabanı altındaki pod'lar sayılmaz → <3 pod")
	}
}

func TestRareCode(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	now := time.Unix(1_800_000_000, 0)
	first := map[string]time.Time{chstore.MLSeriesKey("pay", "code|E1"): now.Add(-10 * 24 * time.Hour)}
	oldest := map[string]time.Time{"pay": now.Add(-10 * 24 * time.Hour), "new": now.Add(-time.Hour)}
	cases := []struct {
		svc, code string
		n         int64
		want      bool
	}{{"pay", "E2", 10, true}, {"pay", "E1", 10, false}, {"pay", "E2", 2, false}, {"new", "E2", 10, false}, {"ghost", "E2", 10, false}, {"pay", "", 10, false}}
	for _, c := range cases {
		if got := rareCode(c.svc, c.code, c.n, first, oldest, now, cfg); got != c.want {
			t.Errorf("rareCode(%s,%s,%d)=%v want %v", c.svc, c.code, c.n, got, c.want)
		}
	}
}
