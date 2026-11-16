package evaluator

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/rollout"
)

const gMin = int64(60 * 1e9)

var gBase = time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC).UnixNano()

func svcProb(id, svc string, minute int64) *chstore.Problem {
	return &chstore.Problem{ID: id, RuleID: "anomaly:" + svc + ":error_rate", Service: svc, Status: "open", StartedAt: gBase + minute*gMin}
}

func dbProb(id, dbID string, minute int64) *chstore.Problem {
	return &chstore.Problem{ID: id, RuleID: "db-health:" + dbID, Service: dbID, Kind: chstore.ProblemKindDB, Status: "open", StartedAt: gBase + minute*gMin}
}

func emptyFacts() *graphFacts {
	return &graphFacts{Callees: map[string][]string{}, Callers: map[string]int{}, WL: map[string][]string{},
		Nodes: map[string][]string{}, NodeSvcs: map[string]int{}, Hosts: map[string][]string{}, HostSvcs: map[string]int{},
		DepTgt: map[string][]string{}, CallsOK: true, HubOK: true, ServesOK: true, NodeOK: true, HostOK: true, EvOK: true}
}

func plan(t *testing.T, f *graphFacts, ps ...*chstore.Problem) graphPlan {
	t.Helper()
	us, kinds := buildGraphUnits(ps, chstore.OwnerPolicy{})
	return planGraph(us, kinds, f)
}

func groupOf(p graphPlan, id string) (chstore.ProblemGraphGroup, bool) {
	return chstore.FindProblemGraphGroup(chstore.ProblemGraphShadow{Groups: p.Groups}, id)
}

func memberByID(g chstore.ProblemGraphGroup, id string) chstore.ProblemGraphMember {
	for _, m := range g.Members {
		if m.ProblemID == id {
			return m
		}
	}
	return chstore.ProblemGraphMember{}
}

const ordersDB = "db:postgresql@orders-db"

func TestGraphCallPlainAndStrict(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	p := plan(t, f, dbProb("p-db", ordersDB, 0), svcProb("p-co", "checkout-api", 3))
	g, ok := groupOf(p, "p-co")
	if !ok || g.Entities != 2 || g.OwnerID != "p-db" {
		t.Fatalf("group = %+v ok=%v", g, ok)
	}
	m := memberByID(g, "p-co")
	if m.Link != chstore.GraphRuleCall || m.Strict || !reflect.DeepEqual(m.Path, []string{"checkout-api", "postgresql@orders-db"}) {
		t.Fatalf("member = %+v", m)
	}
	if len(p.Strict) != 2 {
		t.Fatalf("strict variant must not link without evidence: %d clusters", len(p.Strict))
	}
	f.DepTgt["checkout-api"] = []string{"orders-db"}
	p = plan(t, f, dbProb("p-db", ordersDB, 0), svcProb("p-co", "checkout-api", 3))
	g, _ = groupOf(p, "p-co")
	if !memberByID(g, "p-co").Strict || len(p.Strict) != 1 {
		t.Fatalf("strict variant with evidence: %+v / %d", memberByID(g, "p-co"), len(p.Strict))
	}
}

func TestGraphTimeWindow(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:checkout-api"] = []string{"svc:cart-api"}
	p := plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 45))
	g, ok := groupOf(p, "p-cart")
	if !ok || g.Entities != 1 || len(g.NearMisses) != 1 || g.NearMisses[0].Reason != chstore.GraphNoTime || g.NearMisses[0].Hint != chstore.GraphRuleCall {
		t.Fatalf("N-TIME expected: %+v", g)
	}
	if p.NoLink["p-co"] != chstore.GraphNoTime {
		t.Fatalf("noLink = %v", p.NoLink)
	}
}

func TestGraphWorkloadAndSameService(t *testing.T) {
	f := emptyFacts()
	wl := entity.WorkloadID("cluster-a", "shop", "Deployment", "checkout")
	f.WL["svc:checkout-api"] = []string{wl}
	f.WL["svc:checkout-grpc"] = []string{wl}
	member := &chstore.Problem{ID: "p-co-p99", RuleID: "anomaly:checkout-api:p99_ms", Service: "checkout-api", Status: "open", StartedAt: gBase + 6*gMin}
	p := plan(t, f, svcProb("p-co", "checkout-api", 0), svcProb("p-grpc", "checkout-grpc", 4), member)
	g, ok := groupOf(p, "p-grpc")
	if !ok || g.Entities != 2 || len(g.Members) != 3 {
		t.Fatalf("group = %+v", g)
	}
	if m := memberByID(g, "p-grpc"); m.Link != chstore.GraphRuleWL || m.Via != "checkout" {
		t.Fatalf("M-WL member = %+v", m)
	}
	if m := memberByID(g, "p-co-p99"); m.Link != chstore.GraphRuleSvc || m.Peer != "checkout-api" {
		t.Fatalf("M-SVC member = %+v", m)
	}
	if !memberByID(g, "p-co").Owner {
		t.Fatal("earliest row must be the owner")
	}
}

func TestGraphSharedDependencyAndHub(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:cart-api"] = []string{ordersDB}
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	f.Callers[ordersDB] = 2
	f.DepTgt["cart-api"] = []string{"orders-db"}
	f.DepTgt["checkout-api"] = []string{"orders-db:5432"}
	p := plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2))
	if g, _ := groupOf(p, "p-co"); memberByID(g, "p-co").Link != chstore.GraphRuleDep {
		t.Fatalf("M-DEP expected: %+v", g)
	}
	// Ortak noktayı 21 servis kullanıyor: N-HUB, bağ yok.
	f.Callers[ordersDB] = 21
	p = plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2))
	g, _ := groupOf(p, "p-co")
	if g.Entities != 1 || len(g.NearMisses) == 0 || g.NearMisses[0].Reason != chstore.GraphNoHub {
		t.Fatalf("N-HUB expected: %+v", g)
	}
	// Kanıt yok: ilişki var ama koşul yok → N-REL + M-DEP ipucu.
	f.Callers[ordersDB] = 2
	f.DepTgt = map[string][]string{}
	p = plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2))
	g, _ = groupOf(p, "p-co")
	if len(g.NearMisses) == 0 || g.NearMisses[0].Reason != chstore.GraphNoRel || g.NearMisses[0].Hint != chstore.GraphRuleDep {
		t.Fatalf("N-REL/M-DEP hint expected: %+v", g)
	}
}

func TestGraphNodeRule(t *testing.T) {
	f := emptyFacts()
	f.Nodes["svc:cart-api"] = []string{"cluster-a/node-a-01"}
	f.Nodes["svc:orders-api"] = []string{"cluster-a/node-a-01"}
	f.NodeSvcs["cluster-a/node-a-01"] = 4
	p := plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-ord", "orders-api", 1))
	if g, _ := groupOf(p, "p-ord"); memberByID(g, "p-ord").Link != chstore.GraphRuleNode || memberByID(g, "p-ord").Via != "node-a-01" {
		t.Fatalf("M-NODE expected: %+v", g)
	}
	// Sapma tek node ile sınırlı değil (servis 3 node'da) → ipucu, bağ yok.
	f.Nodes["svc:orders-api"] = []string{"cluster-a/node-a-01", "cluster-a/node-a-02", "cluster-a/node-a-03"}
	p = plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-ord", "orders-api", 1))
	if g, _ := groupOf(p, "p-ord"); g.Entities != 1 || g.NearMisses[0].Hint != chstore.GraphRuleNode {
		t.Fatalf("M-NODE hint expected: %+v", g)
	}
}

func TestGraphNoRelationAndMissingGraph(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:cart-api"] = []string{"svc:pricing-api"}
	f.Callees["svc:search-api"] = []string{"svc:index-api"}
	p := plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-search", "search-api", 2), svcProb("p-lone", "lone-api", 3))
	g, _ := groupOf(p, "p-search")
	if g.Entities != 1 || len(g.NearMisses) == 0 {
		t.Fatalf("near misses expected: %+v", g)
	}
	reasons := map[string]string{}
	for _, n := range g.NearMisses {
		reasons[n.ProblemID] = n.Reason
	}
	if reasons["p-cart"] != chstore.GraphNoRel || reasons["p-lone"] != chstore.GraphNoGraph {
		t.Fatalf("reasons = %v", reasons)
	}
	if !contains(p.GraphMissing, "p-lone") || contains(p.GraphMissing, "p-cart") {
		t.Fatalf("graph missing = %v", p.GraphMissing)
	}
}

func TestGraphKindNearMiss(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	alert := &chstore.Problem{ID: "p-alert", RuleID: "rule-7", Service: "checkout-api", Status: "open", StartedAt: gBase + 5*gMin}
	p := plan(t, f, dbProb("p-db", ordersDB, 0), svcProb("p-co", "checkout-api", 3), alert)
	g, _ := groupOf(p, "p-co")
	found := false
	for _, n := range g.NearMisses {
		found = found || (n.ProblemID == "p-alert" && n.Reason == chstore.GraphNoKind)
	}
	if !found || p.NoLink["p-alert"] != chstore.GraphNoKind {
		t.Fatalf("N-KIND expected: %+v / %v", g.NearMisses, p.NoLink)
	}
}

func TestGraphSizeCapAndDepth(t *testing.T) {
	f := emptyFacts()
	wl := entity.WorkloadID("cluster-a", "shop", "Deployment", "batch")
	var ps []*chstore.Problem
	for i := 0; i < 27; i++ {
		svc := "batch-" + string(rune('a'+i))
		f.WL["svc:"+svc] = []string{wl}
		ps = append(ps, svcProb("p-"+svc, svc, int64(i%20)))
	}
	p := plan(t, f, ps...)
	sizes := []int{}
	for _, c := range p.Clusters {
		sizes = append(sizes, len(c.Units))
	}
	sort.Ints(sizes)
	if sizes[len(sizes)-1] != graphSizeMax {
		t.Fatalf("largest cluster = %v", sizes)
	}
	nsize := 0
	for _, r := range p.NoLink {
		if r == chstore.GraphNoSize {
			nsize++
		}
	}
	if nsize != 2 {
		t.Fatalf("N-SIZE = %d (%v)", nsize, p.NoLink)
	}
	// Zincir a→b→c→d→e: sahipten en çok 3 bağ.
	f = emptyFacts()
	chain := []string{"chain-a", "chain-b", "chain-c", "chain-d", "chain-e"}
	ps = nil
	for i, s := range chain {
		if i+1 < len(chain) {
			f.Callees["svc:"+s] = []string{"svc:" + chain[i+1]}
		}
		ps = append(ps, svcProb("p-"+s, s, int64(i*10)))
	}
	p = plan(t, f, ps...)
	if len(p.Clusters[0].Units) != 4 || p.NoLink["p-chain-e"] != chstore.GraphNoSize {
		t.Fatalf("depth cut: %+v / %v", p.Clusters[0], p.NoLink)
	}
}

func TestGraphFamilyOffNoGuess(t *testing.T) {
	f := &graphFacts{} // bütün aileler kapalı (graf eski / okunamadı)
	p := plan(t, f, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 1))
	for _, c := range p.Clusters {
		if len(c.Units) != 1 {
			t.Fatalf("no link may be guessed without graph: %+v", p.Clusters)
		}
	}
}

func TestDepTargetMatches(t *testing.T) {
	for _, c := range []struct {
		tgt, id string
		want    bool
	}{
		{"orders-db", ordersDB, true},
		{"orders-db:5432", ordersDB, true},
		{"cart-api", "svc:cart-api", true},
		{"payments.example.internal", "ext:payments.example.internal:443", true},
		{"orders", ordersDB, false},
		{"", ordersDB, false},
	} {
		if got := depTargetMatches(c.tgt, c.id); got != c.want {
			t.Errorf("depTargetMatches(%q, %q) = %v", c.tgt, c.id, got)
		}
	}
}

func TestGraphBuckets(t *testing.T) {
	if graphAgeBucket(10*gMin) != "0-30" || graphAgeBucket(45*gMin) != "30-60" || graphAgeBucket(89*gMin) != "60-90" || graphAgeBucket(91*gMin) != ">90" {
		t.Fatal("age buckets")
	}
	if graphSizeBucket(2) != "2" || graphSizeBucket(3) != "3" || graphSizeBucket(5) != "4-5" || graphSizeBucket(25) != "11-25" {
		t.Fatal("size buckets")
	}
}

// ── gölge pası: yazma yolu yok, okuma sınırları ──────────────────────────

type fakeGraphStore struct {
	rel, runs, ev, saves, ro int
	saved                    chstore.ProblemGraphShadow
	edges                    map[string][]entity.Relation // rel|byChild
	rollouts                 []chstore.RolloutRow
	hist                     []chstore.Problem
	histReads                int
	histSubjects             []string
}

func (f *fakeGraphStore) ProblemHistoryForSubjects(_ context.Context, subjects []string, _ time.Time) (chstore.FrequentHistoryRead, error) {
	f.histReads++
	if len(subjects) > chstore.FrequentHistorySubjects {
		panic("unbounded history read")
	}
	f.histSubjects = append([]string(nil), subjects...)
	var out []chstore.Problem
	for _, p := range f.hist {
		if contains(subjects, p.Service) {
			out = append(out, p)
		}
	}
	return chstore.FrequentHistoryRead{Rows: out, RowsRead: int64(len(f.hist))}, nil
}

func (f *fakeGraphStore) RolloutsForWorkloads(_ context.Context, keys []rollout.Key, _, _ time.Time) ([]chstore.RolloutRow, error) {
	f.ro++
	if len(keys) > 50 {
		panic("unbounded rollout read")
	}
	return f.rollouts, nil
}

func (f *fakeGraphStore) EntityRelationsFor(_ context.Context, rel string, ids []string, byChild bool, _ time.Time, limit int) ([]entity.Relation, error) {
	f.rel++
	if len(ids) > graphMaxUnits || limit > graphRelLimit {
		panic("unbounded read")
	}
	k := rel
	if byChild {
		k += "|child"
	}
	return f.edges[k], nil
}

func (f *fakeGraphStore) EntityLastRunAt(context.Context, string, time.Time) (time.Time, error) {
	f.runs++
	return time.Now(), nil
}

func (f *fakeGraphStore) ProblemGraphDependencyEvents(context.Context, []string, time.Time, int) ([]chstore.GraphDependencyEvent, error) {
	f.ev++
	return []chstore.GraphDependencyEvent{{Service: "cart-api", Target: "orders-db"}, {Service: "checkout-api", Target: "orders-db"}}, nil
}

func (f *fakeGraphStore) SaveProblemGraphShadow(_ context.Context, r chstore.ProblemGraphShadow) error {
	f.saves++
	f.saved = r
	return nil
}

// TestGraphShadowStoreHasNoProblemWritePath — pasın depo yüzünde Problem,
// üye olayı, bildirim, karar ya da ayar yazan metot YOK; tek yazım blob.
func TestGraphShadowStoreHasNoProblemWritePath(t *testing.T) {
	typ := reflect.TypeOf((*graphShadowStore)(nil)).Elem()
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	want := []string{"EntityLastRunAt", "EntityRelationsFor", "ProblemGraphDependencyEvents", "ProblemHistoryForSubjects", "RolloutsForWorkloads", "SaveProblemGraphShadow"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("graphShadowStore methods = %v, want %v (new method = review the S1 no-write rule)", got, want)
	}
	for _, m := range got {
		for _, bad := range []string{"Upsert", "Insert", "Resolve", "Close", "Notify", "Delete", "Verdict", "Vote", "PutSetting"} {
			if strings.Contains(m, bad) {
				t.Fatalf("write/verdict method %s on the shadow store", m)
			}
		}
	}
}

func shadowFixture() []*chstore.Problem {
	return []*chstore.Problem{
		svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2), dbProb("p-db", ordersDB, 1),
		svcProb("p-search", "search-api", 3),
		{ID: "p-co-p99", RuleID: "anomaly:checkout-api:p99_ms", Service: "checkout-api", Status: "open", StartedAt: gBase + 4*gMin, Severity: "warning"},
	}
}

func TestGraphShadowPassReadsAndNoMutation(t *testing.T) {
	st := &fakeGraphStore{edges: map[string][]entity.Relation{
		entity.RelCalls: {{Type: entity.RelCalls, ParentID: "svc:cart-api", ChildID: ordersDB}, {Type: entity.RelCalls, ParentID: "svc:checkout-api", ChildID: ordersDB}},
	}}
	open := shadowFixture()
	before := make([]chstore.Problem, len(open))
	for i, p := range open {
		before[i] = *p
	}
	s := newGraphShadowState(time.Unix(0, gBase))
	now := time.Unix(0, gBase+10*gMin)
	pl, wrote := runGraphShadow(context.Background(), st, nil, s, open, chstore.OwnerPolicy{}, now)
	for i, p := range open {
		if !reflect.DeepEqual(*p, before[i]) {
			t.Fatalf("pass mutated problem %s", p.ID)
		}
	}
	if !wrote || st.saves != 1 {
		t.Fatalf("blob write: wrote=%v saves=%d", wrote, st.saves)
	}
	// db Problem'i olan hedef: ortak bağımlılık okuması gerekmez → 2 okuma
	// (S1) + S2 etki alanı called_by en çok 2; iş yükü yok → rollout okuması yok.
	if st.rel != 4 || st.runs != 1 || st.ev != 1 || st.ro != 0 {
		t.Fatalf("reads rel=%d runs=%d ev=%d", st.rel, st.runs, st.ev)
	}
	g, ok := groupOf(pl, "p-co")
	if !ok || g.Entities != 3 {
		t.Fatalf("group = %+v", g)
	}
	r := st.saved
	if r.Links[chstore.GraphRuleCall] != 2 || r.CallStrict != 2 || r.Links[chstore.GraphRuleSvc] != 1 || r.Sizes["3"] != 1 || r.Age["0-30"] != 2 {
		t.Fatalf("report = %+v", r)
	}
	if r.NoLink[chstore.GraphNoGraph] != 1 || r.GraphMissing != 1 {
		t.Fatalf("search-api has no graph data: %+v", r.NoLink)
	}
	// Aynı aday kümesi 5 dk içinde: okuma yok, değişmeyen blob yazılmaz.
	_, wrote = runGraphShadow(context.Background(), st, nil, s, open, chstore.OwnerPolicy{}, now.Add(time.Minute))
	if st.rel != 4 || st.runs != 1 || st.ev != 1 || wrote {
		t.Fatalf("memo: rel=%d runs=%d ev=%d wrote=%v", st.rel, st.runs, st.ev, wrote)
	}
	if !strings.Contains(graphShadowLine(r, 100), "problem_graph_shadow window=24h") {
		t.Fatal("log line")
	}
}

func TestGraphShadowFewCandidatesNoReads(t *testing.T) {
	st := &fakeGraphStore{}
	s := newGraphShadowState(time.Now())
	runGraphShadow(context.Background(), st, nil, s, []*chstore.Problem{svcProb("p-cart", "cart-api", 0)}, chstore.OwnerPolicy{}, time.Now())
	if st.rel+st.runs+st.ev != 0 || s.reads.SkippedFew != 1 {
		t.Fatalf("reads with < 2 candidates: rel=%d runs=%d ev=%d", st.rel, st.runs, st.ev)
	}
}

func TestFitProblemGraphBlob(t *testing.T) {
	r := chstore.ProblemGraphShadow{}
	for i := 0; i < 400; i++ {
		r.Groups = append(r.Groups, chstore.ProblemGraphGroup{OwnerID: strings.Repeat("x", 40), Members: make([]chstore.ProblemGraphMember, 5)})
	}
	n := chstore.FitProblemGraphBlob(&r, 16<<10)
	if n > 16<<10 || !r.Truncated || len(r.Groups) == 0 {
		t.Fatalf("fit: n=%d truncated=%v groups=%d", n, r.Truncated, len(r.Groups))
	}
}
