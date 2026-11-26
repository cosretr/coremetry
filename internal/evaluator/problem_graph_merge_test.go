package evaluator

// problem_graph_merge_test.go — v0.10.2341 (entity grafı S4): uygulama yolu
// kapalıyken plan / blob bugünküyle aynı; açıkken emme sınırı, "geç katıldı",
// sonradan bağlanan üye, tavan ve geniş ortak nokta kuralı.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

var gKeepAll = []string{"*"}

func p2(p *chstore.Problem) *chstore.Problem {
	p.Priority, p.Severity = "P2", "warning"
	return p
}

// gMergeFixture — orders-db (sahip) ← checkout-api çağırıyor (sonradan) +
// checkout-api'nin aynı servisli ikinci satırı 100 dk sonra.
func gMergeFixture() (*graphFacts, []*chstore.Problem) {
	f := emptyFacts()
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	late := p2(&chstore.Problem{ID: "p-co-late", RuleID: "anomaly:checkout-api:p99_ms", Service: "checkout-api", Status: "open", StartedAt: gBase + 100*gMin})
	return f, []*chstore.Problem{p2(dbProb("p-db", ordersDB, 0)), p2(svcProb("p-co", "checkout-api", 3)), late}
}

func planWith(f *graphFacts, opt graphEnforceOpts, ps ...*chstore.Problem) graphPlan {
	us, kinds := buildGraphUnits(ps, chstore.OwnerPolicy{})
	return planGraphOpts(us, kinds, f, opt)
}

func TestMergeOffPlanIdentical(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(false)()
	f, ps := gMergeFixture()
	us, kinds := buildGraphUnits(ps, chstore.OwnerPolicy{})
	a := planGraph(us, kinds, f)
	b := planGraphOpts(us, kinds, f, graphEnforceOpts{})
	ja, _ := json.Marshal(a.Groups)
	jb, _ := json.Marshal(b.Groups)
	if string(ja) != string(jb) || !reflect.DeepEqual(a.NoLink, b.NoLink) || !reflect.DeepEqual(a.Clusters, b.Clusters) {
		t.Fatalf("kapalı seçenek planı değiştirdi:\n%s\n%s", ja, jb)
	}
	for _, g := range a.Groups {
		if g.Onset != 0 {
			t.Fatalf("kapalıyken Onset yazılmamalı: %+v", g)
		}
		for _, m := range g.Members {
			if m.Late {
				t.Fatalf("kapalıyken geç katıldı işareti olmamalı: %+v", m)
			}
		}
	}
}

// v0.10.2473 — pasın planı görünüm kipi AYARINA bağlı (bildirim tutması
// sabitine değil): `shadow` = v0.10.2472 planı bayt bayt; `on` = emme sınırı
// + küme başlangıcı; `off` = küme / köken yok, graf okuması yok.
func TestMergeOffShadowPassBlobIdentical(t *testing.T) {
	_, ps := gMergeFixture()
	var reads int64
	run := func(mode string) chstore.ProblemGraphShadow {
		defer chstore.SetProblemGraphMergeModeForTest(mode)()
		st := &fakeGraphStore{edges: map[string][]entity.Relation{
			entity.RelCalls: {{Type: entity.RelCalls, ParentID: "svc:checkout-api", ChildID: ordersDB}}}}
		s := newGraphShadowState(time.Unix(0, gBase))
		s.freq = nil
		runGraphShadow(context.Background(), st, nil, s, ps, chstore.OwnerPolicy{}, time.Unix(0, gBase+101*gMin))
		reads = s.reads.Relations
		return st.saved
	}
	off := run(chstore.GraphMergeShadow)
	if off.MergeReport == nil || off.MergeReport.Enforce || off.MergeReport.Mode != chstore.GraphMergeShadow || off.MergeReport.Joined != 2 {
		t.Fatalf("rapor kipi gölge söylemeli, üyeleri saymalı: %+v", off.MergeReport)
	}
	// Kapalı blobun kümeleri, S4 öncesi plan işlevinin (planGraph) kümeleriyle aynı.
	f := emptyFacts()
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	f.DepTgt["checkout-api"] = []string{"orders-db"}
	us, kinds := buildGraphUnits(ps, chstore.OwnerPolicy{})
	legacy := planGraph(us, kinds, f)
	if len(off.Groups) != len(legacy.Groups) || len(off.Groups) == 0 {
		t.Fatalf("küme sayısı: %d vs %d", len(off.Groups), len(legacy.Groups))
	}
	for i := range legacy.Groups {
		a, b := legacy.Groups[i], off.Groups[i]
		a.Origin, b.Origin = nil, nil // köken S2 okumalarıyla dolar; konu değil
		ja, _ := json.Marshal(a)
		jb, _ := json.Marshal(b)
		if string(ja) != string(jb) {
			t.Fatalf("kapalı blob kümesi değişti:\n%s\n%s", ja, jb)
		}
	}
	for _, g := range off.Groups {
		if g.Onset != 0 {
			t.Fatalf("kapalı blob: %+v", g)
		}
	}
	on := run(chstore.GraphMergeOn)
	// Görünüm açık; bildirim tutması (Enforce) ayardan BAĞIMSIZ kapalı kalır.
	if on.MergeReport == nil || on.MergeReport.Mode != chstore.GraphMergeOn || on.MergeReport.Enforce {
		t.Fatalf("rapor: görünüm on, bildirim tutması kapalı: %+v", on.MergeReport)
	}
	if len(on.Groups) != 1 || on.Groups[0].Onset != gBase || !memberByID(on.Groups[0], "p-co-late").Late {
		t.Fatalf("on: küme başlangıcı + geç katıldı işareti: %+v", on.Groups)
	}
	none := run(chstore.GraphMergeOff)
	if len(none.Groups) != 0 || reads != 0 || none.Units != 0 {
		t.Fatalf("off: küme yok, graf okuması yok: groups=%d reads=%d units=%d", len(none.Groups), reads, none.Units)
	}
}

// v0.10.2473 — zincir: api-gateway → checkout → payments, üçü de sapmış.
// Tek küme; köken zincirin en derin halkası (payments), kural K-DEEP.
// İlgisiz servis (search) kendi başına kalır.
func TestMergeChainOfThreeOneGroupWithOrigin(t *testing.T) {
	defer chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)()
	st := &fakeGraphStore{edges: map[string][]entity.Relation{entity.RelCalls: {
		{Type: entity.RelCalls, ParentID: "svc:api-gateway", ChildID: "svc:checkout"},
		{Type: entity.RelCalls, ParentID: "svc:checkout", ChildID: "svc:payments"},
	}}}
	ps := []*chstore.Problem{svcProb("p-pay", "payments", 0), svcProb("p-co", "checkout", 2), svcProb("p-gw", "api-gateway", 3),
		svcProb("p-search", "search", 1)}
	s := newGraphShadowState(time.Unix(0, gBase))
	s.freq = nil
	runGraphShadow(context.Background(), st, nil, s, ps, chstore.OwnerPolicy{}, time.Unix(0, gBase+5*gMin))
	var chain *chstore.ProblemGraphGroup
	for i, g := range st.saved.Groups {
		if g.Entities >= 2 {
			if chain != nil {
				t.Fatalf("tek bağlı küme beklenir: %+v", st.saved.Groups)
			}
			chain = &st.saved.Groups[i]
		}
		if g.Entities >= 2 && memberByID(g, "p-search").ProblemID != "" {
			t.Fatalf("ilgisiz servis kümeye girmemeli: %+v", g)
		}
	}
	if chain == nil || chain.Entities != 3 || len(chain.Members) != 3 || chain.OwnerID != "p-pay" {
		t.Fatalf("zincir tek küme, sahip en erken başlayan: %+v", chain)
	}
	o := chain.Origin
	if o == nil || o.Entity != "svc:payments" || o.Rule != chstore.OriginRuleDeep || o.Confidence != chstore.OriginWeak {
		t.Fatalf("köken en derin halka (K-DEEP, tek kural): %+v", o)
	}
	std := chstore.StdOriginOf(*chain, "p-gw")
	if std == nil || std.Name != "payments" || std.Rule != chstore.OriginRuleDeep || std.Entities != 3 || std.Self {
		t.Fatalf("manşet kökeni: %+v", std)
	}
	if self := chstore.StdOriginOf(*chain, "p-pay"); self == nil || !self.Self {
		t.Fatalf("kökenin kendi Problem'i Self işaretli: %+v", self)
	}
}

func TestMergeLateSameServiceNoSecondProblem(t *testing.T) {
	f, ps := gMergeFixture()
	p := planWith(f, graphEnforceOpts{On: true}, ps...)
	g, ok := groupOf(p, "p-co-late")
	if !ok || g.Entities != 2 || g.OwnerID != "p-db" || g.Onset != gBase {
		t.Fatalf("geç satır aynı kümede, tek varlık olarak kalmalı: %+v", g)
	}
	if m := memberByID(g, "p-co-late"); !m.Late || m.Link != chstore.GraphRuleSvc {
		t.Fatalf("aynı servis 100 dk sonra: geç katıldı + M-SVC beklenir: %+v", m)
	}
	if m := memberByID(g, "p-co"); m.Late {
		t.Fatalf("3. dakika geç değil: %+v", m)
	}
	h := mergeHoldsOf(p, gKeepAll)
	if c, ok := h["p-co-late"]; !ok || !c.hold.Late || c.hold.Rule != chstore.GraphRuleSvc {
		t.Fatalf("geç satır üye tutma adayı: %+v", h)
	}
}

func TestMergeLaterMemberHoldCandidate(t *testing.T) {
	f, ps := gMergeFixture()
	h := mergeHoldsOf(planWith(f, graphEnforceOpts{On: true}, ps...), gKeepAll)
	if _, ok := h["p-db"]; ok {
		t.Fatal("sahip tutulmaz")
	}
	c, ok := h["p-co"]
	if !ok || c.hold.Rule != chstore.GraphRuleCall || c.hold.OwnerID != "p-db" || c.hold.Owner == nil || c.hold.RefPriority != "P2" {
		t.Fatalf("sonradan bağlanan üye: %+v", c)
	}
	now := time.Unix(0, gBase)
	if !chstore.MergeWouldHold(c.row, "P2", c.hold, now) {
		t.Fatal("P2 üye, şiddet artmadı: tutulur")
	}
	if chstore.MergeWouldHold(c.row, "P1", c.hold, now) {
		t.Fatal("P1'e çıkan üye bildirilir")
	}
}

// Emme sınırı: sahip kapanıp küme yeniden kurulsa da sınır ilk başlangıçtan
// ölçülür; 90 dk sonra başlayan yeni servis bağlanmaz (N-CAP), kendi Problem'i kalır.
func TestMergeAbsorbLimitCrossService(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:b-api"] = []string{"svc:c-api"}
	f.Callees["svc:c-api"] = []string{"svc:d-api"}
	f.Callees["svc:d-api"] = []string{"svc:e-api"}
	b, c, d := svcProb("p-b", "b-api", 25), svcProb("p-c", "c-api", 50), svcProb("p-d", "d-api", 75)
	e := svcProb("p-e", "e-api", 100)
	rem := map[string]int64{"p-b": gBase, "p-c": gBase, "p-d": gBase} // önceki tikte sahibi 0. dk olan kümedeydiler
	on := planWith(f, graphEnforceOpts{On: true, Onset: rem}, b, c, d, e)
	if g, _ := groupOf(on, "p-b"); g.Entities != 3 {
		t.Fatalf("b-c-d kümesi kalmalı: %+v", g)
	}
	if _, in := mergeHoldsOf(on, gKeepAll)["p-e"]; in || on.NoLink["p-e"] != chstore.GraphNoCap {
		t.Fatalf("90 dk sonrası bağlanmaz: nolink=%v", on.NoLink)
	}
	off := planWith(f, graphEnforceOpts{}, b, c, d, e)
	if g, _ := groupOf(off, "p-e"); g.Entities != 4 {
		t.Fatalf("kapalıyken (bugünkü gölge) emme sınırı uygulanmaz: %+v", g)
	}
}

func TestMergeEnforceKeepsSizeCapAndHub(t *testing.T) {
	// Tavan: 26 servis çağrı zinciri yerine yıldız — 25 varlık sınırı.
	f := emptyFacts()
	ps := []*chstore.Problem{svcProb("p-hub", "hub-api", 0)}
	for i := 0; i < 26; i++ {
		name := "leaf-" + string(rune('a'+i))
		f.Callees["svc:hub-api"] = append(f.Callees["svc:hub-api"], "svc:"+name)
		ps = append(ps, svcProb("p-"+name, name, 1))
	}
	p := planWith(f, graphEnforceOpts{On: true}, ps...)
	if g, _ := groupOf(p, "p-hub"); g.Entities != 25 {
		t.Fatalf("üye tavanı 25: %d", g.Entities)
	}
	// Geniş ortak nokta: 21 servisin çağırdığı veritabanı kendi sinyali yokken bağlamaz.
	h := emptyFacts()
	h.Callees["svc:checkout-api"] = []string{ordersDB}
	h.Callees["svc:payments-api"] = []string{ordersDB}
	h.Callers[ordersDB] = 21
	h.DepTgt["checkout-api"] = []string{"orders-db"}
	h.DepTgt["payments-api"] = []string{"orders-db"}
	q := planWith(h, graphEnforceOpts{On: true}, svcProb("p-co", "checkout-api", 0), svcProb("p-pay", "payments-api", 2))
	if _, ok := mergeHoldsOf(q, gKeepAll)["p-pay"]; ok || q.NoLink["p-pay"] != chstore.GraphNoHub && q.NoLink["p-co"] != chstore.GraphNoHub {
		t.Fatalf("N-HUB bağ kurmaz: %v", q.NoLink)
	}
}

func TestMergeReportCountsAndBecameP1(t *testing.T) {
	f, ps := gMergeFixture()
	s := newGraphShadowState(time.Unix(0, gBase))
	p := planWith(f, graphEnforceOpts{}, ps...)
	s.observeMerge(p, gKeepAll, time.Unix(0, gBase+5*gMin))
	r := s.mergeReport()
	if r.Joined != 2 || r.WouldHold != 2 || r.HeldBecameP1 != 0 || r.Late != 1 {
		t.Fatalf("rapor: %+v", r)
	}
	ps[1].Priority = "P1"
	s.observeMerge(planWith(f, graphEnforceOpts{}, ps...), gKeepAll, time.Unix(0, gBase+6*gMin))
	r = s.mergeReport()
	if r.HeldBecameP1 != 1 || len(r.BecameP1) != 1 || r.BecameP1[0].ProblemID != "p-co" {
		t.Fatalf("tutulacakken P1'e çıkan sayılmalı: %+v", r)
	}
}

func TestMergeHoldDecideJoinsLaterProblem(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(true)()
	st := &fakeGraphStore{edges: map[string][]entity.Relation{
		entity.RelCalls: {{Type: entity.RelCalls, ParentID: "svc:checkout-api", ChildID: ordersDB}}}}
	var h mergeHoldState
	open := []*chstore.Problem{p2(dbProb("p-db", ordersDB, 0))}
	newcomer := *p2(svcProb("p-co", "checkout-api", 3))
	m, ok := h.decide(context.Background(), st, nil, open, newcomer, nil, chstore.OwnerPolicy{Keep: gKeepAll}, time.Unix(0, gBase+4*gMin))
	if !ok || m.OwnerID != "p-db" || m.Rule != chstore.GraphRuleCall {
		t.Fatalf("yeni açılan üye kümeye katılır: %+v ok=%v", m, ok)
	}
	if _, ok := h.decide(context.Background(), st, nil, nil, newcomer, nil, chstore.OwnerPolicy{Keep: gKeepAll}, time.Unix(0, gBase+4*gMin)); ok {
		t.Fatal("tek Problem: tutma yok")
	}
}
