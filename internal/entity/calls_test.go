package entity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// v0.10.2240 (S5) — yatay eksen: kimlik, saf görüntü, syncer geçişi.

func TestTopologyEntityIDRoundTrip(t *testing.T) {
	cases := []struct{ node, id, typ, name string }{
		{"checkout-api", "svc:checkout-api", TypeService, "checkout-api"},
		{"db:postgresql@orders-db", "db:postgresql@orders-db", TypeDatabase, "postgresql@orders-db"},
		{"queue:kafka:orders.created", "queue:kafka:orders.created", TypeQueue, "kafka:orders.created"},
		{"ext:dns:///payments.example:443", "ext:dns:///payments.example:443", TypeExternal, "dns:///payments.example:443"},
		{"node:worker-1", "", "", ""},
		{"", "", "", ""},
		{"db:", "", "", ""},
	}
	for _, c := range cases {
		got := TopologyEntityID(c.node)
		if got != c.id {
			t.Errorf("TopologyEntityID(%q) = %q, want %q", c.node, got, c.id)
			continue
		}
		if c.id == "" {
			continue
		}
		ref, ok := ParseID(got)
		if !ok || ref.Type != c.typ || ref.Name != c.name || ref.ClusterID != "" {
			t.Errorf("ParseID(%q) = %+v ok=%v", got, ref, ok)
		}
		if ref.String() != got {
			t.Errorf("gidiş-dönüş %q → %q", got, ref.String())
		}
	}
}

func TestBuildCallSnapshot(t *testing.T) {
	edges := []CallEdge{
		{Parent: "checkout-api", Child: "orders-api", Calls: 50},
		{Parent: "checkout-api", Child: "db:postgresql@orders-db", Calls: 40},
		{Parent: "checkout-api", Child: "queue:kafka:orders.created", Calls: 30},
		{Parent: "queue:kafka:orders.created", Child: "billing-worker", Calls: 30}, // tüketici
		{Parent: "checkout-api", Child: "ext:payments.example", Calls: 9},
		{Parent: "checkout-api", Child: "ext:geo.example", Calls: 1}, // tavan dışı
		{Parent: "checkout-api", Child: "checkout-api", Calls: 5},    // kendine
		{Parent: "checkout-api", Child: "node:worker-1", Calls: 5},   // RUNS_ON
		{Parent: "db:postgresql@orders-db", Child: "orders-api", Calls: 1},
		{Parent: "queue:kafka:orders.created", Child: "db:x", Calls: 1},
	}
	snap := BuildCallSnapshot(edges, 1)
	wantRels := []string{
		relKey(RelCalls, "svc:checkout-api", "svc:orders-api"),
		relKey(RelCalls, "svc:checkout-api", "db:postgresql@orders-db"),
		relKey(RelCalls, "svc:checkout-api", "queue:kafka:orders.created"),
		relKey(RelCalls, "queue:kafka:orders.created", "svc:billing-worker"),
		relKey(RelCalls, "svc:checkout-api", "ext:payments.example"),
	}
	if len(snap.Relations) != len(wantRels) {
		t.Fatalf("kenar sayısı %d, want %d: %v", len(snap.Relations), len(wantRels), snap.Relations)
	}
	for _, k := range wantRels {
		rl, ok := snap.Relations[k]
		if !ok || !IsStableRelation(rl) || rl.ClusterID != TopologyClusterID || rl.Source != SourceTopology {
			t.Errorf("eksik/yanlış kenar %s: %+v", k, rl)
		}
	}
	if snap.DroppedExt != 1 {
		t.Errorf("ext tavanı: dropped=%d", snap.DroppedExt)
	}
	if len(snap.Entities) != 3 {
		t.Fatalf("db+queue+ext = 3 entity (servis satırı yazılmaz), alınan %v", snap.Entities)
	}
	db := snap.Entities["db:postgresql@orders-db"]
	if db.Type != TypeDatabase || db.Name != "postgresql@orders-db" || db.Labels["system"] != "postgresql" || !IsStableType(db.Type) {
		t.Errorf("db entity: %+v", db)
	}
	if q := snap.Entities["queue:kafka:orders.created"]; q.Labels["system"] != "kafka" {
		t.Errorf("queue system: %+v", q)
	}
}

type fakeCalls struct {
	edges     []CallEdge
	truncated bool
	err       error
	n         int
}

func (f *fakeCalls) RecentCalls(ctx context.Context, since time.Time, limit int) ([]CallEdge, bool, error) {
	f.n++
	return f.edges, f.truncated, f.err
}

func TestSyncCallsLifecycle(t *testing.T) {
	st := newMemStore()
	fc := &fakeCalls{edges: []CallEdge{
		{Parent: "checkout-api", Child: "db:postgresql@orders-db", Calls: 10},
		{Parent: "checkout-api", Child: "orders-api", Calls: 5},
	}}
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := t0
	sy := NewSyncer(&fakeSource{}, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return now }
	sy.SetCallSource(fc)

	sy.Tick(context.Background())
	if fc.n != 1 {
		t.Fatalf("ilk tick geçişi koşturmalı: %d", fc.n)
	}
	open := st.open[TopologyClusterID]
	if len(open) != 3 { // db entity + 2 kenar
		t.Fatalf("açık ömür %d: %v", len(open), open)
	}
	written := len(st.rows) + len(st.rels)

	// Kadans: 60 s sonra geçiş yok.
	now = t0.Add(time.Minute)
	sy.Tick(context.Background())
	if fc.n != 1 {
		t.Fatalf("CallsEvery dolmadan geçiş koştu: %d", fc.n)
	}
	// 15 dk sonra koşar; değişim yok → yazım yok (günlük tazeleme dışında).
	now = t0.Add(CallsEvery)
	sy.Tick(context.Background())
	if fc.n != 2 || len(st.rows)+len(st.rels) != written {
		t.Fatalf("değişimsiz geçiş yazmamalı: n=%d yazım %d → %d", fc.n, written, len(st.rows)+len(st.rels))
	}

	// LIMIT doldu + kenar kayboldu: kapanış YOK.
	fc.edges, fc.truncated = fc.edges[:1], true
	for i := 2; i < 2+int(CallsCloseAfter/CallsEvery)+2; i++ {
		now = t0.Add(time.Duration(i) * CallsEvery)
		sy.Tick(context.Background())
	}
	if _, ok := st.open[TopologyClusterID][relKey(RelCalls, "svc:checkout-api", "svc:orders-api")]; !ok {
		t.Fatal("eksik (truncated) görüntü kenar kapatmamalı")
	}

	// Okuma hatası: yazım/kapanış yok, koşu failed.
	fc.err = errors.New("ch down")
	now = now.Add(CallsEvery)
	before := len(st.rows) + len(st.rels)
	sy.Tick(context.Background())
	if len(st.rows)+len(st.rels) != before || st.runs[len(st.runs)-1].Status != RunFailed {
		t.Fatalf("hata: yazım olmamalı, koşu failed: %+v", st.runs[len(st.runs)-1])
	}

	// Tam görüntü + 24 saat yokluk → kenar kapanır, db kalır.
	fc.err, fc.truncated = nil, false
	start := now
	for now.Sub(start) <= CallsCloseAfter+CallsEvery {
		now = now.Add(CallsEvery)
		sy.Tick(context.Background())
	}
	if _, ok := st.open[TopologyClusterID][relKey(RelCalls, "svc:checkout-api", "svc:orders-api")]; ok {
		t.Fatal("24 saat görülmeyen kenar kapanmalı")
	}
	if _, ok := st.open[TopologyClusterID]["db:postgresql@orders-db"]; !ok {
		t.Fatal("görülen db açık kalmalı")
	}
}

// Kardinalite tavanı: ext düğümleri MaxExternalEntities'te kesilir.
func TestBuildCallSnapshotExtCeiling(t *testing.T) {
	var edges []CallEdge
	for i := 0; i < MaxExternalEntities+50; i++ {
		edges = append(edges, CallEdge{Parent: "checkout-api", Child: fmt.Sprintf("ext:h%05d.example", i), Calls: uint64(i + 1)})
	}
	snap := BuildCallSnapshot(edges, MaxExternalEntities)
	if len(snap.Entities) != MaxExternalEntities || snap.DroppedExt != 50 || len(snap.Relations) != MaxExternalEntities {
		t.Fatalf("tavan: ents=%d dropped=%d rels=%d", len(snap.Entities), snap.DroppedExt, len(snap.Relations))
	}
	if _, ok := snap.Entities["ext:h00000.example"]; ok {
		t.Fatal("en az çağrılan ext düşmeli")
	}
}

type fakeCallRels struct{ edges []Relation }

func (f fakeCallRels) RelationsFor(ctx context.Context, rel string, ids []string, byChild bool, at time.Time, limit int) ([]Relation, error) {
	var out []Relation
	for _, e := range f.edges {
		end := e.ParentID
		if byChild {
			end = e.ChildID
		}
		if e.Type == rel && end == ids[0] {
			out = append(out, e)
		}
	}
	return out, nil
}

func TestResolverCalleesCallers(t *testing.T) {
	snap := BuildCallSnapshot([]CallEdge{
		{Parent: "checkout-api", Child: "db:postgresql@orders-db"},
		{Parent: "orders-api", Child: "db:postgresql@orders-db"},
		{Parent: "checkout-api", Child: "orders-api"},
	}, 10)
	var rels []Relation
	for _, r := range snap.Relations {
		rels = append(rels, r)
	}
	r := (&Resolver{}).WithRelations(fakeCallRels{edges: rels})
	callers, _ := r.Callers(context.Background(), "db:postgresql@orders-db", time.Time{})
	if fmt.Sprint(callers.IDs) != "[svc:checkout-api svc:orders-api]" {
		t.Fatalf("db çağıranları: %v", callers.IDs)
	}
	callees, _ := r.Callees(context.Background(), "svc:checkout-api", time.Time{})
	if fmt.Sprint(callees.IDs) != "[db:postgresql@orders-db svc:orders-api]" {
		t.Fatalf("servisin çağırdıkları: %v", callees.IDs)
	}
}
