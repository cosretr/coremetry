package ml

// v0.10.2015 — anomali tekilleştirme D3: promoteMLGroups gölge kararı.
// Grup Problem kapısından geçseydi "açardı" kaydı; CANLI Problem, bildirim
// ya da anomaly_events kopyası YOK. Kapı audit S2 varsayılanı: ≥ 2 farklı
// dedektörün teyidi; karne kapı değil. Servis adları SENTETİK.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func gateEv(id, det, svc string, from, to int, baseline, observed float64) chstore.MLEvent {
	e := gEv(id, det, svc, from, to)
	e.Baseline, e.Observed, e.Direction = baseline, observed, "up"
	return e
}

func decideFor(t *testing.T, evs []chstore.MLEvent, in GroupGateInput) GroupDecision {
	t.Helper()
	gs := GroupMLEvents(evs, 10*time.Minute)
	if len(gs) != 1 {
		t.Fatalf("tek grup bekleniyordu: %d", len(gs))
	}
	return DecideGroupPromotion(gs[0], evs, in, gBase)
}

func TestDecideGroupPromotion(t *testing.T) {
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	in := GroupGateInput{IsBatch: isBatch, CalendarTZ: "Europe/Istanbul"}
	cases := []struct {
		name              string
		evs               []chstore.MLEvent
		in                GroupGateInput
		wantOpen          bool
		wantPrio, wantWhy string
	}{
		{"§4 yavaşlama, 3 trace dedektörü, 3× sapma → açardı P1",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 300), gateEv("b", detDependencyLatency, "checkout", 5, 25, 50, 80),
				gateEv("c", detTraceChangePoint, "checkout", 12, 40, 100, 150)}, in, true, "P1", "3 dedektör"},
		{"iki dedektör, iki sinyal, küçük sapma → açardı P2",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 150), gateEv("l", detErrorRate, "checkout", 5, 20, 10, 14)}, in, true, "P2", "2 dedektör"},
		{"iki dedektör, tek sinyal, küçük sapma → açardı P3",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 150), gateEv("b", detDependencyLatency, "checkout", 5, 20, 10, 14)}, in, true, "P3", "2 dedektör"},
		{"tek dedektör → açmazdı",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 500)}, in, false, "", "tek dedektör"},
		{"aynı dedektörün iki olayı → açmazdı (teyit başka dedektörden gelmeli)",
			[]chstore.MLEvent{gateEv("a1", detOpLatency, "checkout", 0, 30, 100, 500), gateEv("a2", detOpLatency, "checkout", 3, 20, 100, 500)}, in, false, "", "tek dedektör"},
		{"-batch servis, yalnız yük sinyalleri → açmazdı (batch kuralı)",
			[]chstore.MLEvent{gateEv("t", detThroughputDrop, "billing-batch", 0, 30, 100, 10), gateEv("v", detLogVolume, "billing-batch", 5, 20, 100, 900)}, in, false, "", "batch"},
		{"-batch servis, yük + gecikme → yük düşer, tek dedektör kalır → açmazdı",
			[]chstore.MLEvent{gateEv("t", detThroughputDrop, "billing-batch", 0, 30, 100, 10), gateEv("a", detOpLatency, "billing-batch", 5, 20, 100, 400)}, in, false, "", "tek dedektör"},
		{"-batch servis, gecikme + kırılma → açardı (batch yalnız yükü susturur)",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "billing-batch", 0, 30, 100, 150), gateEv("c", detTraceChangePoint, "billing-batch", 5, 20, 100, 150)}, in, true, "P3", "2 dedektör"},
		{"takvim dilimi tanımlı değil → açmazdı",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 300), gateEv("b", detDependencyLatency, "checkout", 5, 25, 50, 80)},
			GroupGateInput{IsBatch: isBatch}, false, "", "takvim"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideFor(t, c.evs, c.in)
			if d.WouldOpen != c.wantOpen || d.Priority != c.wantPrio || !strings.Contains(d.Reason, c.wantWhy) {
				t.Fatalf("karar = %+v; istenen open=%v prio=%q neden~%q", d, c.wantOpen, c.wantPrio, c.wantWhy)
			}
			if !strings.HasPrefix(d.RuleID, "anomaly-auto:group:mlg-") || d.Group == "" {
				t.Errorf("kural kimliği %q / grup %q", d.RuleID, d.Group)
			}
			if d.At != gBase.Unix() {
				t.Errorf("karar anı %d", d.At)
			}
		})
	}
}

// Aşağı yönlü sapma (trafik düşüşü) oranı taban/gözlenen; taban 0 ise büyük sapma.
func TestGroupPeakRatio(t *testing.T) {
	cases := []struct {
		e    chstore.MLEvent
		want float64
	}{
		{chstore.MLEvent{Baseline: 100, Observed: 300, Direction: "up"}, 3},
		{chstore.MLEvent{Baseline: 100, Observed: 25, Direction: "down"}, 4},
		{chstore.MLEvent{Baseline: 0, Observed: 5, Direction: "up"}, bigBreachRatio},
		{chstore.MLEvent{Baseline: 100, Observed: 0, Direction: "down"}, bigBreachRatio},
	}
	for _, c := range cases {
		if got := eventRatio(c.e); got != c.want {
			t.Errorf("eventRatio(%+v) = %v, istenen %v", c.e, got, c.want)
		}
	}
}

// Kanıta yazılan gölge kararı geri okunur (liste ucu bunu gösterir).
func TestGroupShadowEvidenceRoundTrip(t *testing.T) {
	d := GroupDecision{Group: "mlg-x", WouldOpen: true, RuleID: "anomaly-auto:group:mlg-x", Priority: "P2", Reason: "açardı", At: 1}
	ev := stampGroupShadow(`{"dayClass":"workday"}`, d)
	got, ok := GroupShadowOf(ev)
	if !ok || got.Group != d.Group || !got.WouldOpen || got.Priority != "P2" {
		t.Fatalf("geri okuma: %+v ok=%v", got, ok)
	}
	if !strings.Contains(ev, `"dayClass":"workday"`) {
		t.Error("mevcut kanıt (takvim sınıfı) korunmalı")
	}
	if _, ok := GroupShadowOf(`{"x":1}`); ok {
		t.Error("kararsız kanıt ok=false olmalı")
	}
}

// Replay kancası: geçmiş ml_events satırları üzerinde aynı karar (S1
// örnekleriyle karşılaştırma bunu kullanacak).
func TestReplayGroupDecisions(t *testing.T) {
	evs := []chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 300), gateEv("b", detDependencyLatency, "checkout", 5, 25, 50, 80),
		gateEv("p", detOpLatency, "payments", 0, 10, 100, 300)}
	ds := ReplayGroupDecisions(evs, 10*time.Minute, GroupGateInput{CalendarTZ: "UTC"}, gBase)
	if len(ds) != 2 || !ds[0].WouldOpen || ds[1].WouldOpen {
		t.Fatalf("replay: %+v", ds)
	}
}

// Recorder adımı: bu tikin olayı, tracker'daki (önceki tikte yazılmış)
// başka dedektörün koşusuyla gruplanır; karar yalnız yazılacak satırın
// kanıtına damgalanır, kronik kayda dokunulmaz.
func TestShadowPromoteGroups(t *testing.T) {
	kept := []chstore.MLEvent{gateEv("a", detOpLatency, "checkout", 0, 30, 100, 300), func() chstore.MLEvent {
		e := gateEv("k", detTraceChangePoint, "checkout", -600, 600, 1, 2)
		e.Stage = chstore.MLStageChronic
		return e
	}()}
	running := []chstore.MLEvent{gateEv("b", detDependencyLatency, "checkout", 5, 25, 50, 80), gateEv("a", detOpLatency, "checkout", 0, 20, 100, 300)}
	c := shadowPromoteGroups(kept, running, 10*time.Minute, GroupGateInput{CalendarTZ: "UTC"}, gBase)
	if c.Groups != 1 || c.WouldOpen != 1 {
		t.Fatalf("sayaç: %+v", c)
	}
	d, ok := GroupShadowOf(kept[0].Evidence)
	if !ok || !d.WouldOpen || len(d.Detectors) != 2 {
		t.Fatalf("damga: %+v ok=%v", d, ok)
	}
	if kept[1].Evidence != "" {
		t.Errorf("kronik kayıt damgalanmamalı: %q", kept[1].Evidence)
	}
}
