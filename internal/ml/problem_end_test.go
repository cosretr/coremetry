package ml

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// TestProblemOpenIDs — v0.10.2476: Problem kararı olayı bitmiş (MLEventOpen
// false) ve ≥ 15 dk önce son görülmüş koşuyu açık saymaz; canlı, yeni bitmiş,
// bu tik görülen ve olayı bilinmeyen koşular açık kalır.
func TestProblemOpenIDs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	ev := func(id string, ago time.Duration, stage string) chstore.MLEvent {
		return chstore.MLEvent{ID: id, WindowSec: 60, Stage: stage, LastSeen: now.Add(-ago).UnixNano()}
	}
	cases := []struct {
		name    string
		running []chstore.MLEvent
		kept    []chstore.MLEvent
		want    bool // "e" açık sayılır mı
	}{
		{"canlı olay", []chstore.MLEvent{ev("e", time.Minute, "")}, nil, true},
		{"bitmiş ama grace dolmadı", []chstore.MLEvent{ev("e", 10*time.Minute, "")}, nil, true},
		{"no-data tutması, 20 dk sessiz", []chstore.MLEvent{ev("e", 20*time.Minute, "")}, nil, false},
		{"no-data tutması, 2 sa sessiz", []chstore.MLEvent{ev("e", 2*time.Hour, "")}, nil, false},
		{"kapalı damgalı, 16 dk", []chstore.MLEvent{ev("e", 16*time.Minute, chstore.MLStageClosed)}, nil, false},
		{"kapalı damgalı, 5 dk", []chstore.MLEvent{ev("e", 5*time.Minute, chstore.MLStageClosed)}, nil, true},
		{"bu tik görüldü (eski damga olsa da)", []chstore.MLEvent{ev("e", time.Hour, "")}, []chstore.MLEvent{{ID: "e"}}, true},
		{"olayı bilinmiyor", nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			open := map[string]bool{"e": true, "other": true}
			got := problemOpenIDs(open, c.running, c.kept, now)
			if got["e"] != c.want {
				t.Fatalf("open[e] = %v, want %v", got["e"], c.want)
			}
			if !got["other"] {
				t.Fatal("ilgisiz koşu düştü")
			}
			if !open["e"] {
				t.Fatal("girdi haritası değiştirildi")
			}
		})
	}
}

// TestGroupFinishesWhenAllEventsEnded — kural grubun kararına iner: tüm
// üyeleri ≥ 15 dk önce bitmiş grup Finished (mevcut kapanış yolu), bir üye
// yeniden görülünce yine açık.
func TestGroupFinishesWhenAllEventsEnded(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	a := chstore.MLEvent{ID: "a", Service: "svc-a", Detector: "svc_error_rate", WindowSec: 60, LastSeen: now.Add(-40 * time.Minute).UnixNano()}
	b := chstore.MLEvent{ID: "b", Service: "svc-a", Detector: "svc_response_time", WindowSec: 60, LastSeen: now.Add(-30 * time.Minute).UnixNano()}
	g := MLGroup{ID: "g", Members: []string{"a", "b"}}
	tracker := map[string]bool{"a": true, "b": true} // no-data tutması: koşular hâlâ açık
	d := DecideGroupPromotion(g, []chstore.MLEvent{a, b}, GroupGateInput{Open: problemOpenIDs(tracker, []chstore.MLEvent{a, b}, nil, now)}, now)
	if !d.Finished {
		t.Fatalf("tüm olaylar bitmiş grup Finished olmalı: %+v", d)
	}
	b.LastSeen = now.UnixNano()
	d = DecideGroupPromotion(g, []chstore.MLEvent{a, b}, GroupGateInput{Open: problemOpenIDs(tracker, []chstore.MLEvent{a, b}, []chstore.MLEvent{b}, now)}, now)
	if d.Finished {
		t.Fatalf("yeniden görülen üye grubu açık tutmalı: %+v", d)
	}
}
