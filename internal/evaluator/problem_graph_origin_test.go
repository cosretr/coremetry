package evaluator

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

func originOf(t *testing.T, p graphPlan, id string) *chstore.ProblemGraphOrigin {
	t.Helper()
	g, ok := groupOf(p, id)
	if !ok || g.Origin == nil {
		t.Fatalf("no origin for %s: %+v", id, g)
	}
	return g.Origin
}

func ruleOf(o *chstore.ProblemGraphOrigin, id string) chstore.ProblemGraphOriginRule {
	for _, r := range o.Rules {
		if r.Rule == id {
			return r
		}
	}
	return chstore.ProblemGraphOriginRule{}
}

func planWithOrigin(t *testing.T, f *graphFacts, of *originFacts, ps ...*chstore.Problem) graphPlan {
	t.Helper()
	p := plan(t, f, ps...)
	attachOrigins(&p, f, of)
	return p
}

// Taslaktaki örnek: orders-db iki üyenin ortak çağrılanı ve zincirin son
// halkası → güçlü; K-FIRST 2 dk farkla ayırmaz; K-ROLLOUT rollout yok.
func TestOriginSharedAndDeepStrong(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:cart-api"] = []string{ordersDB}
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	f.WL["svc:checkout-api"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	f.WL["svc:checkout-grpc"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	of := &originFacts{RolloutOK: true, Rollouts: map[string][]int64{}}
	p := planWithOrigin(t, f, of, dbProb("p-db", ordersDB, 2), svcProb("p-cart", "cart-api", 4),
		svcProb("p-co", "checkout-api", 5), svcProb("p-grpc", "checkout-grpc", 7))
	o := originOf(t, p, "p-cart")
	if o.Entity != ordersDB || o.Confidence != chstore.OriginStrong || o.Rule != chstore.OriginRuleShared ||
		!reflect.DeepEqual(o.Leads, []string{chstore.OriginRuleShared, chstore.OriginRuleDeep}) {
		t.Fatalf("origin = %+v", o)
	}
	if r := ruleOf(o, chstore.OriginRuleRollout); r.Separated || r.Off {
		t.Fatalf("rollout rule = %+v", r)
	}
	if r := ruleOf(o, chstore.OriginRuleShared); !reflect.DeepEqual(r.Others, []string{"cart-api", "checkout-api"}) || r.Source != chstore.OriginSrcCalls {
		t.Fatalf("shared rule = %+v", r)
	}
	if r := ruleOf(o, chstore.OriginRuleFirst); r.Separated || r.Runner != "cart-api" {
		t.Fatalf("first rule within 5 min must not separate: %+v", r)
	}
	if o.Earliest != "postgresql@orders-db" || len(o.Rules) != 4 {
		t.Fatalf("earliest / rule order: %+v", o)
	}
	// Kural sırası sözleşme.
	for i, id := range []string{chstore.OriginRuleRollout, chstore.OriginRuleShared, chstore.OriginRuleDeep, chstore.OriginRuleFirst} {
		if o.Rules[i].Rule != id {
			t.Fatalf("rule order %d = %s", i, o.Rules[i].Rule)
		}
	}
}

func TestOriginRolloutWinsWeak(t *testing.T) {
	f := emptyFacts()
	wl := entity.WorkloadID("cluster-a", "shop", "Deployment", "checkout")
	f.Callees["svc:checkout-api"] = []string{"svc:cart-api", "svc:pricing-api"}
	f.Callees["svc:orders-api"] = []string{"svc:cart-api"}
	f.WL["svc:checkout-api"] = []string{wl}
	of := &originFacts{RolloutOK: true, Rollouts: map[string][]int64{wl: {gBase - 10*gMin}}}
	p := planWithOrigin(t, f, of, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2),
		svcProb("p-pr", "pricing-api", 3), svcProb("p-or", "orders-api", 3))
	o := originOf(t, p, "p-co")
	if o.Name != "checkout-api" || o.Rule != chstore.OriginRuleRollout || o.Confidence != chstore.OriginWeak {
		t.Fatalf("origin = %+v", o)
	}
	if r := ruleOf(o, chstore.OriginRuleRollout); r.Via != "checkout" || r.At != gBase-10*gMin {
		t.Fatalf("rollout evidence = %+v", r)
	}
	// İki yaprak (cart-api, pricing-api): K-DEEP ayırmaz.
	if r := ruleOf(o, chstore.OriginRuleDeep); r.Separated || len(r.Others) != 2 {
		t.Fatalf("deep = %+v", r)
	}
	// Rollout okunamadı → kural kapalı, sonraki kural kazanır (cart-api: iki çağıran).
	p = planWithOrigin(t, f, &originFacts{}, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 2),
		svcProb("p-pr", "pricing-api", 3), svcProb("p-or", "orders-api", 3))
	o = originOf(t, p, "p-co")
	if !ruleOf(o, chstore.OriginRuleRollout).Off || o.Rule != chstore.OriginRuleShared || o.Name != "cart-api" {
		t.Fatalf("rollout off: %+v", o)
	}
}

func TestOriginUnclearAndFirst(t *testing.T) {
	f := emptyFacts()
	f.WL["svc:checkout-api"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	f.WL["svc:checkout-grpc"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	of := &originFacts{RolloutOK: true}
	p := planWithOrigin(t, f, of, svcProb("p-co", "checkout-api", 0), svcProb("p-grpc", "checkout-grpc", 3))
	o := originOf(t, p, "p-co")
	if o.Confidence != chstore.OriginUnclear || o.Entity != "" || o.Earliest != "checkout-api" || o.Blast != nil {
		t.Fatalf("unclear = %+v", o)
	}
	p = planWithOrigin(t, f, of, svcProb("p-co", "checkout-api", 0), svcProb("p-grpc", "checkout-grpc", 6))
	o = originOf(t, p, "p-co")
	if o.Rule != chstore.OriginRuleFirst || o.Name != "checkout-api" || o.Confidence != chstore.OriginWeak {
		t.Fatalf("first = %+v", o)
	}
}

// Problem'i olmayan, iki üyede bağımlılık olayıyla kanıtlı ortak bağımlılık aday olur.
func TestOriginSharedDependencyCandidate(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:cart-api"] = []string{ordersDB}
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	f.Callers[ordersDB] = 2
	f.DepTgt["cart-api"] = []string{"orders-db"}
	f.DepTgt["checkout-api"] = []string{"orders-db"}
	p := planWithOrigin(t, f, &originFacts{RolloutOK: true}, svcProb("p-cart", "cart-api", 0), svcProb("p-co", "checkout-api", 1))
	o := originOf(t, p, "p-cart")
	if o.Entity != ordersDB || o.Confidence != chstore.OriginStrong || o.OnsetAt != 0 {
		t.Fatalf("dep origin = %+v", o)
	}
	if r := ruleOf(o, chstore.OriginRuleShared); r.Source != chstore.OriginSrcDepEv {
		t.Fatalf("source = %+v", r)
	}
}

func TestOriginBlastRadius(t *testing.T) {
	f := emptyFacts()
	f.Callees["svc:cart-api"] = []string{ordersDB}
	f.Callees["svc:checkout-api"] = []string{ordersDB}
	f.WL["svc:checkout-api"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	f.WL["svc:checkout-grpc"] = []string{"wl:cluster-a/shop/Deployment/checkout"}
	of := &originFacts{RolloutOK: true, BlastOK: true, CalledBy: map[string][]string{
		ordersDB:            {"svc:cart-api", "svc:checkout-api", "svc:order-history", "svc:invoice-api"},
		"svc:cart-api":      {"svc:web-gateway"},
		"svc:checkout-api":  {"svc:mobile-bff"},
		"svc:checkout-grpc": {"svc:web-gateway", "svc:search-api"},
	}}
	p := planWithOrigin(t, f, of, dbProb("p-db", ordersDB, 2), svcProb("p-cart", "cart-api", 4),
		svcProb("p-co", "checkout-api", 5), svcProb("p-grpc", "checkout-grpc", 7), svcProb("p-search", "search-api", 50))
	b := originOf(t, p, "p-cart").Blast
	if b == nil || !reflect.DeepEqual(b.Affected, []string{"cart-api", "checkout-api", "checkout-grpc"}) ||
		!reflect.DeepEqual(b.AtRisk1, []string{"invoice-api", "order-history"}) || b.AtRisk1N != 2 ||
		!reflect.DeepEqual(b.AtRisk2, []string{"mobile-bff", "web-gateway"}) || b.AtRisk2N != 2 {
		t.Fatalf("blast = %+v", b)
	}
	// search-api açık Problem'li (sapmalı) ama kümede değil: risk listesine girmez.
	// Okuma yoksa Off, etkilenenler yine yazılır.
	p = planWithOrigin(t, f, &originFacts{RolloutOK: true}, dbProb("p-db", ordersDB, 2), svcProb("p-cart", "cart-api", 4),
		svcProb("p-co", "checkout-api", 5))
	if b := originOf(t, p, "p-cart").Blast; !b.Off || len(b.Affected) != 2 {
		t.Fatalf("blast off = %+v", b)
	}
}

func TestBlastHopCap(t *testing.T) {
	cb := map[string][]string{ordersDB: {}}
	for i := 0; i < 150; i++ {
		cb[ordersDB] = append(cb[ordersDB], entity.ServiceID("svc-"+string(rune('a'+i/26))+string(rune('a'+i%26))))
	}
	b := computeBlast(&chstore.ProblemGraphOrigin{Entity: ordersDB}, map[string]string{ordersDB: "orders-db"}, nil,
		&originFacts{BlastOK: true, CalledBy: cb})
	if b.AtRisk1N != blastHopMax || len(b.AtRisk1) != blastNamesMax || !b.Cut {
		t.Fatalf("cap: n=%d names=%d cut=%v", b.AtRisk1N, len(b.AtRisk1), b.Cut)
	}
}

func TestFitProblemGraphBlobDropsRiskNamesFirst(t *testing.T) {
	r := chstore.ProblemGraphShadow{}
	for i := 0; i < 40; i++ {
		r.Groups = append(r.Groups, chstore.ProblemGraphGroup{OwnerID: "g", Origin: &chstore.ProblemGraphOrigin{
			Blast: &chstore.ProblemGraphBlast{AtRisk1: []string{"web-gateway-0000000000000000000000000000000000000000"}, AtRisk1N: 1}}})
	}
	stripped := chstore.ProblemGraphShadow{}
	for range r.Groups {
		stripped.Groups = append(stripped.Groups, chstore.ProblemGraphGroup{OwnerID: "g", Origin: &chstore.ProblemGraphOrigin{
			Blast: &chstore.ProblemGraphBlast{AtRisk1N: 1, NamesDrop: true}}})
	}
	max := chstore.FitProblemGraphBlob(&stripped, 1<<20)
	chstore.FitProblemGraphBlob(&r, max)
	if len(r.Groups) != 40 || r.Truncated || r.Groups[0].Origin.Blast.AtRisk1 != nil || !r.Groups[0].Origin.Blast.NamesDrop || r.Groups[0].Origin.Blast.AtRisk1N != 1 {
		t.Fatalf("fit: groups=%d truncated=%v blast=%+v", len(r.Groups), r.Truncated, r.Groups[0].Origin.Blast)
	}
}
