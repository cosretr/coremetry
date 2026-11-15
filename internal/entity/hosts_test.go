package entity

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// v0.10.2260 (S6 parça 1) — host adayı, terfi kapısı, kalıcı/canlı katman.

func TestLooksLikePod(t *testing.T) {
	for name, want := range map[string]bool{
		"checkout-api-7d9f8b6c5d-x2x9z": true,  // Deployment pod'u
		"log-agent-x2x9z":               true,  // DaemonSet / Job pod'u
		"node-a-01":                     false, // sıra numaralı host adı
		"node-a":                        false,
		"build-host":                    false,
	} {
		if got := LooksLikePod(name); got != want {
			t.Errorf("LooksLikePod(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestHostCandidates(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	rows := []SeenRow{
		{ClusterValue: "cluster-a", Pod: "node-a-01", Service: "checkout-api", Spans: 5, FirstSeen: now.Add(-time.Hour), LastSeen: now},
		{ClusterValue: "", Pod: "node-a-01", Service: "orders-api", Spans: 2, FirstSeen: now.Add(-30 * time.Minute), LastSeen: now},
		{ClusterValue: "cluster-a", Namespace: "shop", Pod: "checkout-api-0", Service: "checkout-api", Spans: 9}, // K8s pod
		{ClusterValue: "cluster-a", Pod: "checkout-api-7d9f8b6c5d-x2x9z", Service: "checkout-api", Spans: 9},     // pod şekilli
		{ClusterValue: "cluster-a", Pod: "orders-db-0", Service: "orders-api", Spans: 9},                         // KSM pod'u
	}
	c := HostCandidates(rows, map[string]bool{"orders-db-0": true})
	if len(c) != 1 || c["node-a-01"] == nil {
		t.Fatalf("aday: %v", c)
	}
	h := c["node-a-01"]
	if h.Spans != 7 || h.Services["checkout-api"] != 5 || h.Services["orders-api"] != 2 || !h.First.Equal(now.Add(-time.Hour)) {
		t.Fatalf("toplam: %+v", h)
	}
}

func TestHostProofProven(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		p    HostProof
		want bool
	}{
		{HostProof{FirstSeen: now.Add(-25 * time.Hour), Hours: 25}, true},
		{HostProof{FirstSeen: now.Add(-24 * time.Hour), Hours: 24}, true},
		{HostProof{FirstSeen: now.Add(-23 * time.Hour), Hours: 24}, false}, // yeni
		{HostProof{FirstSeen: now.Add(-48 * time.Hour), Hours: 20}, false}, // kesintili
		{HostProof{}, false},
	}
	for i, c := range cases {
		if got := c.p.Proven(now); got != c.want {
			t.Errorf("#%d %+v → %v, want %v", i, c.p, got, c.want)
		}
	}
}

type errSeen struct {
	rows []SeenRow
	err  error
}

func (f *errSeen) RecentSeen(ctx context.Context, since time.Time) ([]SeenRow, error) {
	return f.rows, f.err
}
func (f *errSeen) RecentSeenFor(ctx context.Context, since time.Time, v string) ([]SeenRow, error) {
	return nil, nil
}

type fakeProof struct {
	n      int
	proofs map[string]HostProof
}

func (f *fakeProof) HostProof(ctx context.Context, keys []HostKey, since, hoursFrom time.Time) ([]HostProof, error) {
	f.n++
	var out []HostProof
	seen := map[string]bool{}
	for _, k := range keys {
		if p, ok := f.proofs[k.Name]; ok && !seen[k.Name] {
			seen[k.Name] = true
			p.Name = k.Name
			out = append(out, p)
		}
	}
	return out, nil
}

func TestSyncHostsGuard(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := t0
	st := newMemStore()
	seen := &errSeen{rows: []SeenRow{
		{ClusterValue: "", Pod: "node-a-01", Service: "checkout-api", Spans: 5, FirstSeen: t0.Add(-time.Hour), LastSeen: t0},
		{ClusterValue: "", Pod: "node-b-01", Service: "orders-api", Spans: 3, FirstSeen: t0.Add(-time.Hour), LastSeen: t0},
	}}
	proof := &fakeProof{proofs: map[string]HostProof{
		"node-a-01": {FirstSeen: t0.Add(-30 * time.Hour), Hours: 25},
		"node-b-01": {FirstSeen: t0.Add(-2 * time.Hour), Hours: 2}, // henüz 24 saat değil
	}}
	kv := newMemKV()
	sy := NewSyncer(&fakeSource{}, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return now }
	sy.SetSeenReader(seen)
	sy.SetHostProof(proof)
	sy.SetLive(kv)

	sy.Tick(context.Background())
	open := st.open[HostClusterID]
	if _, ok := open["host:node-a-01"]; !ok {
		t.Fatalf("kanıtlı host kalıcı olmalı: %v", open)
	}
	if _, ok := open[relKey(RelRuns, "host:node-a-01", "svc:checkout-api")]; !ok {
		t.Fatalf("host→svc runs kalıcı olmalı: %v", open)
	}
	if _, ok := open["host:node-b-01"]; ok {
		t.Fatal("24 saati dolmamış ad kalıcı katmana yazılmamalı")
	}
	// Canlı blob ikisini de taşır; terfi durumu işaretli.
	snap, err := DecodeLive(kv.m[LiveKey(HostClusterID)])
	if err != nil || len(snap.Hosts) != 2 || !snap.Hosts[0].Stable || snap.Hosts[1].Stable {
		t.Fatalf("canlı host blob'u: %+v err=%v", snap.Hosts, err)
	}
	// Kadans: 1 dk sonra kanıt okuması yok; 15 dk sonra yalnız kanıtlanmamış ad sorulur.
	now = t0.Add(time.Minute)
	sy.Tick(context.Background())
	if proof.n != 1 {
		t.Fatalf("HostEvery dolmadan kanıt okuması: %d", proof.n)
	}
	written := len(st.rows) + len(st.rels)
	now = t0.Add(HostEvery)
	sy.Tick(context.Background())
	if proof.n != 2 || len(st.rows)+len(st.rels) != written {
		t.Fatalf("değişimsiz geçiş: n=%d yazım %d → %d", proof.n, written, len(st.rows)+len(st.rels))
	}
	// Span okuması başarısız: hiçbir şey kapanmaz / yazılmaz.
	seen.err = fmt.Errorf("ch down")
	for i := 2; i < 2+int(HostCloseAfter/HostEvery)+2; i++ {
		now = t0.Add(time.Duration(i) * HostEvery)
		sy.Tick(context.Background())
	}
	if _, ok := st.open[HostClusterID]["host:node-a-01"]; !ok {
		t.Fatal("okuma hatasında host kapanmamalı")
	}
	// Ad KSM pod'u çıktı (tam görüntü, aday yok) → 24 saat sonra kapanır.
	seen.err, seen.rows = nil, nil
	start := now
	for now.Sub(start) <= HostCloseAfter+HostEvery {
		now = now.Add(HostEvery)
		sy.Tick(context.Background())
	}
	if _, ok := st.open[HostClusterID]["host:node-a-01"]; ok {
		t.Fatal("24 saat görülmeyen host kapanmalı")
	}
}

func TestBuildHostStableCeiling(t *testing.T) {
	cands := map[string]*HostCand{}
	proven := map[string]bool{}
	for i := 0; i < 5; i++ {
		n := fmt.Sprintf("node-a-%02d", i)
		cands[n] = &HostCand{Name: n, Services: map[string]int64{"checkout-api": 1}, Spans: int64(i)}
		proven[n] = true
	}
	ents, rels, dropped := BuildHostStable(cands, proven, 3)
	if len(ents) != 3 || len(rels) != 3 || dropped != 2 {
		t.Fatalf("tavan: %d %d %d", len(ents), len(rels), dropped)
	}
	if _, ok := ents["host:node-a-00"]; ok {
		t.Fatal("en az span'li host düşmeli")
	}
}

func TestResolverHostNeighbours(t *testing.T) {
	kv := newMemKV()
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	snap := BuildHostLive(now, map[string]*HostCand{
		"node-a-01": {Name: "node-a-01", Services: map[string]int64{"checkout-api": 4, "orders-api": 1}},
	}, nil)
	b, _ := EncodeLive(snap)
	kv.m[LiveKey(HostClusterID)] = b
	li := NewLiveIndex(kv)
	li.now = func() time.Time { return now }
	r := &Resolver{now: func() time.Time { return now }}
	r.WithLive(li)
	svcs, err := r.HostServices(context.Background(), "node-a-01", time.Time{})
	if err != nil || fmt.Sprint(svcs) != "[checkout-api orders-api]" {
		t.Fatalf("host servisleri: %v %v", svcs, err)
	}
	hosts, _ := r.ServiceHosts(context.Background(), "orders-api", time.Time{})
	if fmt.Sprint(hosts) != "[host:node-a-01]" {
		t.Fatalf("servis host'ları: %v", hosts)
	}
	// Geçmiş an: canlı dizin sorulmaz, kalıcı runs.
	r.WithRelations(fakeCallRels{edges: []Relation{{Type: RelRuns, ParentID: "host:node-b-01", ChildID: "svc:orders-api"}}})
	hosts, _ = r.ServiceHosts(context.Background(), "orders-api", now.Add(-48*time.Hour))
	if fmt.Sprint(hosts) != "[host:node-b-01]" {
		t.Fatalf("geçmiş servis host'ları: %v", hosts)
	}
}
