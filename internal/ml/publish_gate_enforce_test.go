package ml

// publish_gate_enforce_test.go — v0.10.2343 (anomali kartı A2): anahtar
// dikişle (publishGatesEnforce) açıkken kapı kararlarının yazımı, grup /
// servis düzeyi Problem kapısı, K10 tek sinyal şartı ve K16 rapor alanları.
// Sabit (mlPublishGatesEnforce) hiçbir testte değiştirilmez. Servis adları
// SENTETİK; zamanlar sabit UTC.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func withEnforce(t *testing.T) {
	t.Helper()
	prev := publishGatesEnforce
	publishGatesEnforce = true
	t.Cleanup(func() { publishGatesEnforce = prev })
}

func TestPublishGatesConstantShipsOff(t *testing.T) {
	if mlPublishGatesEnforce || PublishGatesEnforced() || PublishGatesMode() != "A2: kapalı" {
		t.Fatal("A2 kapalı gelir (K16): operatör gölge raporunu onaylamadan açılmaz")
	}
	if !strings.Contains(PublishGatesCriterion, "K16") {
		t.Fatal("ölçüt metni K16'yı anmalı")
	}
}

func a2ByID(evs []chstore.MLEvent) map[string]chstore.MLEvent {
	m := map[string]chstore.MLEvent{}
	for _, e := range evs {
		m[e.ID] = e
	}
	return m
}

func TestPublishGatesA2OnWritesVerdict(t *testing.T) {
	withEnforce(t)
	if PublishGatesMode() != "A2: açık" {
		t.Fatal(PublishGatesMode())
	}
	evs := a2Fixture(t)
	got := map[string]chstore.MLEvent{}
	for _, e := range evs {
		got[e.ID] = applyRecordContract(e)
	}
	cases := []struct{ id, state, class string }{
		{"a1000000000000001", PublishPublished, ClassServiceAnomaly}, // tüm kapılar geçti
		{"a1000000000000002", PublishHeld, ClassServiceAnomaly},      // görünür sapma kaldı → held
		{"a1000000000000003", PublishPublished, ClassInfo},           // tek bağımsız kanıt → info
		{"a1000000000000004", PublishPublished, ClassTelemetryVolume},
		{"a1000000000000005", PublishInvalid, ClassServiceAnomaly}, // doğrulayıcı (A1 ile aynı)
		{"a1000000000000006", "", ""},                              // kayıt yok: dokunulmaz
	}
	for _, c := range cases {
		if e := got[c.id]; e.PublishState != c.state || e.Class != c.class {
			t.Errorf("%s: state=%q class=%q, istenen %q / %q", c.id, e.PublishState, e.Class, c.state, c.class)
		}
	}
	// kayıt JSON'undaki sınıf dedektörün sınıfı kalır (kapı her tik aynı kayıttan)
	var rec EventRecord
	if json.Unmarshal([]byte(got["a1000000000000003"].Record), &rec) != nil || rec.Class != ClassServiceAnomaly {
		t.Fatalf("kayıt sınıfı değişmemeli: %q", rec.Class)
	}
	// taşınan held / info, karar değişince geri alınır
	pub := a2ByID(evs)["a1000000000000001"]
	pub.PublishState, pub.Class = PublishHeld, ClassInfo
	if e := applyRecordContract(pub); e.PublishState != PublishPublished || e.Class != ClassServiceAnomaly {
		t.Fatalf("geri alınmadı: %q %q", e.PublishState, e.Class)
	}
	// tutulan olay Elenenler'de (ShadowHeld) ve kartta okunur
	if !ShadowHeld(got["a1000000000000002"]) || ShadowHeld(got["a1000000000000001"]) {
		t.Fatal("ShadowHeld")
	}
	h := got["a1000000000000002"]
	if v := CardRecordView(&h, CardInput{}); v == nil || v.PublishState != PublishHeld {
		t.Fatalf("tutulan olayın kartı okunmalı: %+v", v)
	}
	i := got["a1000000000000003"]
	if v := CardRecordView(&i, CardInput{}); v == nil || v.Class != ClassInfo {
		t.Fatalf("kart satırın sınıfını göstermeli: %+v", v)
	}
}

// A2 açık: Problem kapısı yalnız yayımlanan service_anomaly (+ kaydı olmayan)
// olayı sayar; bildirim plan.Open'dan çıkar.
func TestPublishGatesA2OnPromotion(t *testing.T) {
	withEnforce(t)
	evs := a2Fixture(t)
	now := gBase.Add(40 * time.Minute)
	pub := pubVerdicts{}
	for _, e := range evs {
		pub.observe(applyRecordContract(e), now)
	}
	var out struct {
		Open   []string        `json:"open"`
		Single map[string]bool `json:"single"`
	}
	if err := json.Unmarshal(a2Outputs(t, evs, GroupGateInput{Published: pub}), &out); err != nil {
		t.Fatal(err)
	}
	opened := strings.Join(out.Open, "\n")
	for _, want := range []string{"anomaly-auto:group:a1000000000000001:checkout-api", "anomaly-auto:group:b1:checkout", "anomaly-auto:svc:orders"} {
		if !strings.Contains(opened, want+" ") {
			t.Errorf("açılmalı: %s\n%s", want, opened)
		}
	}
	for _, not := range []string{"cart-api", "payments-api", "search-api"} { // held, info, invalid
		if strings.Contains(opened, not) {
			t.Errorf("açılmamalı (bildirim yok): %s\n%s", not, opened)
		}
	}
	if len(out.Open) != 3 {
		t.Fatalf("3 Problem bekleniyordu: %v", out.Open)
	}
}

func TestPublishGatesA2SingleArmK10(t *testing.T) {
	ev := gateEv("s1", detOpLatency, "orders", 0, 30, 100, 500) // güçlü + uzun
	now := gBase.Add(40 * time.Minute)
	for _, c := range []struct {
		name string
		pub  map[string]PubVerdict
		want bool
	}{
		{"A2 kapalı (nil) → bugünkü kol açar", nil, true},
		{"yayımlanan, 2 bağımsız kanıt → açar", map[string]PubVerdict{"s1": {State: PublishPublished, Class: ClassServiceAnomaly, Independent: 2}}, true},
		{"yayımlanan, 1 bağımsız kanıt → açmaz (K10)", map[string]PubVerdict{"s1": {State: PublishPublished, Class: ClassServiceAnomaly, Independent: 1}}, false},
		{"kaydı yok (0 kanıt) → açmaz (K10)", map[string]PubVerdict{"s1": {}}, false},
		{"held → aday değil", map[string]PubVerdict{"s1": {State: PublishHeld, Class: ClassServiceAnomaly, Independent: 3}}, false},
		{"karar yok (ilk tik) → aday değil", map[string]PubVerdict{}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			gs := GroupMLEvents([]chstore.MLEvent{ev}, 10*time.Minute)
			d := DecideGroupPromotion(gs[0], []chstore.MLEvent{ev}, GroupGateInput{Published: c.pub}, now)
			if d.WouldOpen != c.want || d.SingleWouldOpen != c.want {
				t.Fatalf("WouldOpen=%v istenen %v (%s)", d.WouldOpen, c.want, d.Reason)
			}
		})
	}
	// ≥ 2 sinyal kolu: info üye sayılmaz → tek sinyale düşer
	a, b := gateEv("t1", detOpLatency, "orders", 0, 30, 100, 300), gateEv("t2", detErrorRate, "orders", 5, 30, 10, 30)
	pub := map[string]PubVerdict{"t1": {}, "t2": {State: PublishPublished, Class: ClassInfo}}
	gs := GroupMLEvents([]chstore.MLEvent{a, b}, 10*time.Minute)
	if d := DecideGroupPromotion(gs[0], []chstore.MLEvent{a, b}, GroupGateInput{Published: pub}, now); d.WouldOpen {
		t.Fatalf("info üye ikinci sinyal sayılmamalı: %s", d.Reason)
	}
	if d := DecideGroupPromotion(gs[0], []chstore.MLEvent{a, b}, GroupGateInput{}, now); !d.WouldOpen {
		t.Fatalf("A2 kapalı: bugünkü gibi açar: %s", d.Reason)
	}
}

func TestPublishedCandidatesSvc(t *testing.T) {
	evs := []chstore.MLEvent{svcEv("e1", detSvcErrorRate, "orders", 0, 0.01, 0.05), svcEv("e2", detSvcErrorRate, "payments", 0, 0.01, 0.05)}
	if got := publishedCandidates(evs, nil); len(got) != 2 {
		t.Fatal("nil = aynen")
	}
	got := publishedCandidates(evs, map[string]PubVerdict{"e1": {State: PublishHeld}, "e2": {State: PublishPublished, Class: ClassServiceAnomaly}})
	if len(got) != 1 || got[0].ID != "e2" {
		t.Fatalf("%v", got)
	}
}

func TestPubVerdictsPrune(t *testing.T) {
	m := pubVerdicts{}
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	m.observe(chstore.MLEvent{ID: "old"}, t0)
	m.observe(chstore.MLEvent{ID: "new"}, t0.Add(20*time.Hour))
	m.prune(t0.Add(25 * time.Hour))
	if _, ok := m["old"]; ok || len(m) != 1 {
		t.Fatalf("%v", m)
	}
}

// K16 raporu: tutulacak olaylardan Problem'le çakışan / dönüşen + örnekler.
func TestGateShadowK16Fields(t *testing.T) {
	es := map[string]gateEntry{
		"d1": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 10, service: "orders"},
		"d2": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 30, service: "orders", problem: true},
		"d3": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 20, service: "payments", problem: true, escalated: true},
		"d4": {detector: "op_latency", o: gateOutcome{Evaluated: true, Drop: true}, at: 40, service: "checkout"},
		"p1": {detector: "op_latency", o: gateOutcome{Evaluated: true}, at: 50, service: "orders", problem: true},
	}
	r := foldGateShadow(es)["op_latency"]
	if r.WouldDrop != 4 || r.WouldPublish != 1 || r.WouldDropProblem != 2 || r.WouldDropEscalated != 1 {
		t.Fatalf("%+v", r)
	}
	if strings.Join(r.DropExamples, ",") != "d3,d2,d4" {
		t.Fatalf("örnek sırası: %v", r.DropExamples)
	}
	if line := gateShadowLine(map[string]chstore.MLGateShadow{"op_latency": r}); !strings.Contains(line, "would_drop_problem=2 would_drop_escalated=1 enforce=false") {
		t.Fatal(line)
	}
}

func TestMarkGateLinksSticky(t *testing.T) {
	var s stats
	s.observeGate("d1", "op_latency", "orders", gateOutcome{Evaluated: true, Drop: true})
	s.observeGate("p1", "op_latency", "orders", gateOutcome{Evaluated: true})
	if !s.gateShadowHasDrop() {
		t.Fatal("drop var")
	}
	s.markGateLinks(map[string]bool{"d1": true, "p1": true}, map[string]bool{"orders": true})
	s.observeGate("d1", "op_latency", "orders", gateOutcome{Evaluated: true, Drop: true}) // sonraki tik
	r := s.gateShadowReport(time.Now())["op_latency"]
	if r.WouldDropProblem != 1 || r.WouldDropEscalated != 1 {
		t.Fatalf("işaretler kalıcı olmalı: %+v", r)
	}
	g := []decidedGroup{{G: MLGroup{Members: []string{"a", "b"}}, D: GroupDecision{WouldOpen: true}}, {G: MLGroup{Members: []string{"c"}}, D: GroupDecision{WouldOpen: true, Finished: true}}}
	ids := escalatedIDs(g, map[string]SvcShadow{"e": {WouldOpen: true}, "f": {}})
	if !ids["a"] || !ids["b"] || ids["c"] || !ids["e"] || ids["f"] {
		t.Fatalf("%v", ids)
	}
}
