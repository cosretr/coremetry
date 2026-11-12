package ml

// v0.10.2015 — anomali tekilleştirme D3: grup terfi kararı (gölge).
// v0.10.2052 — D4 (operatör onayı 2026-11-12): kural "aynı servisin
// grubunda ≥ 2 FARKLI sinyal (trace / log / metric) → Problem"; gölge ve
// canlı AYNI işlev. Önceki kural "≥ 2 farklı dedektör" + takvim dilimi ön
// koşuluydu (iki trace dedektörü Problem açardı; varsayılan dilim boş olduğu
// için hiçbir grup açılmazdı). Servis adları SENTETİK.

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

func closed(e chstore.MLEvent) chstore.MLEvent {
	e.Stage = chstore.MLStageClosed
	return e
}

func TestDecideGroupPromotion(t *testing.T) {
	isBatch := func(s string) bool { return strings.HasSuffix(s, "-batch") }
	in := GroupGateInput{IsBatch: isBatch}
	cases := []struct {
		name              string
		evs               []chstore.MLEvent
		in                GroupGateInput
		wantOpen          bool
		wantPrio, wantWhy string
	}{
		{"1 sinyal (tek trace dedektörü) → açmaz",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 500)}, in, false, "", "tek sinyal"},
		{"2 sinyal (trace + log) → açar P2",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 14)}, in, true, "P2", "2 farklı sinyal"},
		{"3 sinyal, büyük sapma → yine P2 (öncelik sabit, merdiven 4 sa kuralı ayrı)",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 900), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 90),
				gateEv("m", "metric_pool", "orders-api", 8, 25, 1, 9)}, in, true, "P2", "3 farklı sinyal"},
		{"aynı sinyalin iki olayı (iki trace dedektörü) → açmaz",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300), gateEv("b", detDependencyLatency, "orders-api", 5, 25, 50, 80),
				gateEv("c", detTraceChangePoint, "orders-api", 12, 40, 100, 150)}, in, false, "", "tek sinyal"},
		{"aynı dedektörün iki olayı → açmaz",
			[]chstore.MLEvent{gateEv("a1", detOpLatency, "orders-api", 0, 30, 100, 500), gateEv("a2", detOpLatency, "orders-api", 3, 20, 100, 500)}, in, false, "", "tek sinyal"},
		{"-batch servis, yalnız yük sinyalleri (trace hacim + log hacim) → açmaz",
			[]chstore.MLEvent{gateEv("t", detThroughputDrop, "billing-batch", 0, 30, 100, 10), gateEv("v", detLogVolume, "billing-batch", 5, 20, 100, 900)}, in, false, "", "batch"},
		{"-batch servis, trace yükü + log hacmi + trace gecikme → yük düşer, tek sinyal kalır → açmaz",
			[]chstore.MLEvent{gateEv("t", detThroughputDrop, "billing-batch", 0, 30, 100, 10), gateEv("v", detLogVolume, "billing-batch", 2, 20, 100, 900),
				gateEv("a", detOpLatency, "billing-batch", 5, 20, 100, 400)}, in, false, "", "tek sinyal"},
		{"-batch servis, gerçek hata oranı (log) + trace gecikme → açar (batch yalnız yükü susturur)",
			[]chstore.MLEvent{gateEv("l", detErrorRate, "billing-batch", 0, 30, 10, 40), gateEv("a", detOpLatency, "billing-batch", 5, 20, 100, 150)}, in, true, "P2", "2 farklı sinyal"},
		{"bitmiş grup (iki sinyal ama hiçbir koşu açık değil) → yeni Problem yok",
			[]chstore.MLEvent{closed(gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300)), closed(gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 40))}, in, false, "", "bitti"},
		{"bitmiş grup, Open haritası (tracker) → yeni Problem yok",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300), gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 40)},
			GroupGateInput{IsBatch: isBatch, Open: map[string]bool{"x": true}}, false, "", "bitti"},
		{"takvim dilimi yok artık kapı değil → açar",
			[]chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300), gateEv("l", detErrorRate, "orders-api", 5, 25, 50, 80)},
			GroupGateInput{}, true, "P2", "2 farklı sinyal"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideFor(t, c.evs, c.in)
			if d.WouldOpen != c.wantOpen || d.Priority != c.wantPrio || !strings.Contains(d.Reason, c.wantWhy) {
				t.Fatalf("karar = %+v; istenen open=%v prio=%q neden~%q", d, c.wantOpen, c.wantPrio, c.wantWhy)
			}
			if d.WouldOpen && d.Severity != "critical" {
				t.Errorf("şiddet %q, istenen critical", d.Severity)
			}
			if !strings.HasPrefix(d.RuleID, "anomaly-auto:group:") || d.Group == "" {
				t.Errorf("kural kimliği %q / grup %q", d.RuleID, d.Group)
			}
			if ev, ok := GroupEventOfRule(d.RuleID); !ok || ev != c.evs[0].ID {
				t.Errorf("kural → olay %q ok=%v, istenen ilk üye %q", ev, ok, c.evs[0].ID)
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

// Kanıta yazılan karar geri okunur (liste ucu bunu gösterir); canlı alanlar dahil.
func TestGroupShadowEvidenceRoundTrip(t *testing.T) {
	d := GroupDecision{Group: "mlg-x", WouldOpen: true, RuleID: "anomaly-auto:group:x", Priority: "P2", Reason: "açardı", At: 1,
		Live: true, ProblemID: "anomaly-auto:group:x:orders-api"}
	ev := stampGroupShadow(`{"dayClass":"workday"}`, d)
	got, ok := GroupShadowOf(ev)
	if !ok || got.Group != d.Group || !got.WouldOpen || got.Priority != "P2" || !got.Live || got.ProblemID != d.ProblemID {
		t.Fatalf("geri okuma: %+v ok=%v", got, ok)
	}
	if !strings.Contains(ev, `"dayClass":"workday"`) {
		t.Error("mevcut kanıt (takvim sınıfı) korunmalı")
	}
	if _, ok := GroupShadowOf(`{"x":1}`); ok {
		t.Error("kararsız kanıt ok=false olmalı")
	}
}

// Replay kancası: geçmiş ml_events satırları üzerinde aynı karar.
func TestReplayGroupDecisions(t *testing.T) {
	evs := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300), gateEv("l", detErrorRate, "orders-api", 5, 25, 50, 80),
		gateEv("p", detOpLatency, "payments-api", 0, 10, 100, 300)}
	ds := ReplayGroupDecisions(evs, 10*time.Minute, GroupGateInput{}, gBase)
	if len(ds) != 2 || !ds[0].WouldOpen || ds[1].WouldOpen {
		t.Fatalf("replay: %+v", ds)
	}
}

// Recorder adımı: bu tikin olayı, tracker'daki (önceki tikte yazılmış)
// başka sinyalin koşusuyla gruplanır; karar yalnız yazılacak satırın
// kanıtına damgalanır, kronik kayda dokunulmaz.
func TestShadowPromoteGroups(t *testing.T) {
	kept := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 300), func() chstore.MLEvent {
		e := gateEv("k", detTraceChangePoint, "orders-api", -600, 600, 1, 2)
		e.Stage = chstore.MLStageChronic
		return e
	}()}
	running := []chstore.MLEvent{gateEv("l", detErrorRate, "orders-api", 5, 25, 50, 80), gateEv("a", detOpLatency, "orders-api", 0, 20, 100, 300)}
	c := shadowPromoteGroups(kept, running, 10*time.Minute, GroupGateInput{Open: map[string]bool{"a": true, "l": true}}, gBase)
	if c.Groups != 1 || c.WouldOpen != 1 {
		t.Fatalf("sayaç: %+v", c)
	}
	d, ok := GroupShadowOf(kept[0].Evidence)
	if !ok || !d.WouldOpen || len(d.Signals) != 2 {
		t.Fatalf("damga: %+v ok=%v", d, ok)
	}
	if kept[1].Evidence != "" {
		t.Errorf("kronik kayıt damgalanmamalı: %q", kept[1].Evidence)
	}
}

// Bu tikte olayı olmayan ama koşusu açık (1–2 tik kaçırmış) üyenin grubu da
// karara girer (canlı Problem "bitti" sanılıp kapanmasın); koşusu kapanmış
// gruba karar verilmez.
func TestDecideGroupsOpenRunWithoutTickEvent(t *testing.T) {
	running := []chstore.MLEvent{gateEv("a", detOpLatency, "orders-api", 0, 20, 100, 300), gateEv("l", detErrorRate, "orders-api", 5, 20, 50, 80),
		gateEv("p", detOpLatency, "payments-api", 0, 10, 100, 300)}
	gs, c := decideGroups(nil, running, 10*time.Minute, GroupGateInput{Open: map[string]bool{"a": true}}, gBase)
	if len(gs) != 1 || gs[0].G.Service != "orders-api" || !gs[0].D.WouldOpen || c.Groups != 1 {
		t.Fatalf("gruplar: %+v sayaç %+v", gs, c)
	}
}
