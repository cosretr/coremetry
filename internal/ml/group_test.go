package ml

// v0.10.2013 — anomali tekilleştirme D2: dedektörler arası gruplama.
// Senaryolar docs/anomaly/anomaly-reassessment-audit.md §4'ten (tek servis
// yavaşlaması ML'de 3 ayrı satır). Servis adları SENTETİK.

import (
	"math/rand"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var gBase = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func gEv(id, det, svc string, fromMin, toMin int) chstore.MLEvent {
	return chstore.MLEvent{ID: id, Detector: det, Signal: chstore.MLDetectorSignal(det), Service: svc, Stage: chstore.MLStageShadow,
		StartedAt: gBase.Add(time.Duration(fromMin) * time.Minute).UnixNano(), LastSeen: gBase.Add(time.Duration(toMin) * time.Minute).UnixNano()}
}

func groupMembers(gs []MLGroup) [][]string {
	out := make([][]string, len(gs))
	for i, g := range gs {
		out[i] = g.Members
	}
	return out
}

func TestGroupMLEvents(t *testing.T) {
	slack := 10 * time.Minute
	cases := []struct {
		name string
		evs  []chstore.MLEvent
		want [][]string
	}{
		{"§4 servis yavaşlaması: trace_red + dependency_latency + trace_change_point tek grup",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("b", detDependencyLatency, "checkout", 5, 25), gEv("c", detTraceChangePoint, "checkout", 12, 40)},
			[][]string{{"a", "b", "c"}}},
		{"log_red aynı servis aynı pencerede: sinyaller arası tek grup",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("l", detErrorRate, "checkout", 8, 20)},
			[][]string{{"a", "l"}}},
		{"farklı servis aynı an: ayrı gruplar",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("p", detOpLatency, "payments", 0, 30)},
			[][]string{{"a"}, {"p"}}},
		{"aynı servis, boşluk > teyit penceresi: ayrı gruplar",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("b", detTraceChangePoint, "checkout", 41, 50)},
			[][]string{{"a"}, {"b"}}},
		{"boşluk tam teyit penceresi: aynı grup (sınır dahil)",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("b", detTraceChangePoint, "checkout", 40, 50)},
			[][]string{{"a", "b"}}},
		{"zincirleme: A–B ve B–C yakın → tek grup",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 10), gEv("b", detDependencyLatency, "checkout", 15, 20), gEv("c", detErrorRate, "checkout", 28, 30)},
			[][]string{{"a", "b", "c"}}},
		{"kronik kayıt gruba girmez",
			[]chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), func() chstore.MLEvent {
				e := gEv("k", detTraceChangePoint, "checkout", -600, 600)
				e.Stage = chstore.MLStageChronic
				return e
			}()},
			[][]string{{"a"}}},
		{"aynı dedektör iki operasyon (iki epizot) aynı servis: tek grup",
			[]chstore.MLEvent{gEv("a1", detOpLatency, "checkout", 0, 30), gEv("a2", detOpLatency, "checkout", 3, 20)},
			[][]string{{"a1", "a2"}}},
		{"-batch servis ile adaşı birleşmez: gruplama servis bazında",
			[]chstore.MLEvent{gEv("b1", detThroughputDrop, "billing-batch", 0, 30), gEv("b2", detLogVolume, "billing-batch", 5, 20), gEv("x", detOpLatency, "billing", 0, 30)},
			[][]string{{"x"}, {"b1", "b2"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := groupMembers(GroupMLEvents(c.evs, slack))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("gruplar = %v, istenen %v", got, c.want)
			}
		})
	}
}

func TestGroupMLEventsShape(t *testing.T) {
	evs := []chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("b", detDependencyLatency, "checkout", 5, 25),
		gEv("l", detErrorRate, "checkout", 8, 45)}
	evs[2].Evidence = `{"dayClass":"holiday"}`
	orig := append([]chstore.MLEvent(nil), evs...)
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 {
		t.Fatalf("1 grup bekleniyordu, %d", len(gs))
	}
	g := gs[0]
	if g.Start != evs[0].StartedAt || g.End != evs[2].LastSeen {
		t.Errorf("pencere = [%d,%d]", g.Start, g.End)
	}
	if !reflect.DeepEqual(g.Detectors, []string{detDependencyLatency, detErrorRate, detOpLatency}) || !reflect.DeepEqual(g.Signals, []string{"log", "trace"}) {
		t.Errorf("dedektör/sinyal = %v / %v", g.Detectors, g.Signals)
	}
	if g.ID != mlGroupID("checkout", "a") || len(g.ID) < 10 {
		t.Errorf("kimlik = %q", g.ID)
	}
	// girdi (takvim sınıfı dahil) değişmez
	if !reflect.DeepEqual(evs, orig) {
		t.Error("gruplama girdiyi değiştirdi")
	}
	if idx := GroupIndex(gs); idx["l"] == nil || idx["l"].ID != g.ID {
		t.Error("GroupIndex üye → grup eşlemesi yok")
	}
}

// Determinizm: girdi sırası grup kimliğini ve üyelerini değiştirmez (iki
// okuma / iki pod aynı sonucu üretir).
func TestGroupMLEventsOrderIndependent(t *testing.T) {
	evs := []chstore.MLEvent{gEv("a", detOpLatency, "checkout", 0, 30), gEv("b", detDependencyLatency, "checkout", 5, 25),
		gEv("c", detTraceChangePoint, "checkout", 12, 40), gEv("p", detOpLatency, "payments", 0, 5), gEv("q", detErrorRate, "payments", 60, 70)}
	want := GroupMLEvents(evs, 10*time.Minute)
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 20; i++ {
		sh := append([]chstore.MLEvent(nil), evs...)
		r.Shuffle(len(sh), func(a, b int) { sh[a], sh[b] = sh[b], sh[a] })
		if got := GroupMLEvents(sh, 10*time.Minute); !reflect.DeepEqual(got, want) {
			t.Fatalf("sıra %d farklı sonuç: %v", i, groupMembers(got))
		}
	}
}
