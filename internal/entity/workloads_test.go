package entity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// v0.10.2262 (S6 parça 3) — açık iş yükü adı: revizyon eşleşmesi, ad
// türevinin doğrulanması, serves, tablo yoksa ad türevi.

func explicitFixture() map[string]*ExplicitIndex {
	return BuildExplicitIndexes([]ExplicitWorkload{
		{ClusterValue: "cluster-a", Namespace: "shop", Name: "checkout-api", Kind: "Deployment", Revision: "checkout-api-7d9f8b6c5d", Service: "checkout-api"},
		{ClusterValue: "cluster-a", Namespace: "shop", Name: "log-agent", Kind: "DaemonSet", Revision: "v1.2", Service: "log-agent"},
		{ClusterValue: "cluster-a", Namespace: "shop", Name: "orders-v2", Kind: "Deployment", Revision: "orders-v2-5c6b7d8f9", Service: "orders-api"},
		{ClusterValue: "unknown-value", Namespace: "x", Name: "y", Kind: "Job", Revision: "r", Service: "z"}, // tür dışı
	}, []ClusterRef{{ID: "cluster-a", Name: "cluster-a"}})
}

func TestExplicitForPod(t *testing.T) {
	ix := explicitFixture()["cluster-a"]
	cases := []struct {
		pod, kind, name string
		found           bool
	}{
		{"checkout-api-7d9f8b6c5d-x2x9z", "Deployment", "checkout-api", true}, // revizyon
		{"log-agent-x2x9z", "DaemonSet", "log-agent", true},                   // Derived → DaemonSet
		{"orders-v2-5c6b7d8f9-q7w8r", "Deployment", "orders-v2", true},
		{"billing-api-6b7c8d9f5-x2x9z", "", "", false}, // tabloda yok: ad türevi kalır
	}
	for _, c := range cases {
		dk, dn, ok := "", "", false
		if k, n, okk := wlkind.FromPod(c.pod); okk {
			dk, dn, ok = k, n, true
		}
		k, n, found := ix.ForPod("shop", c.pod, dk, dn, ok)
		if found != c.found || k != c.kind || n != c.name {
			t.Errorf("%s → %s/%s %v", c.pod, k, n, found)
		}
	}
	if len(explicitFixture()) != 1 {
		t.Fatal("tür dışı satır dizine girmemeli")
	}
}

func TestSpanSeenExplicitWorkload(t *testing.T) {
	ix := explicitFixture()["cluster-a"]
	rows := []SeenRow{
		{ClusterValue: "cluster-a", Namespace: "shop", Pod: "log-agent-x2x9z", Service: "log-agent", Spans: 3},
		{ClusterValue: "cluster-a", Namespace: "shop", Pod: "billing-api-6b7c8d9f5-x2x9z", Service: "billing-api", Spans: 1},
	}
	ents, rels := SpanSeenToEntitiesWith("cluster-a", rows, map[string]Entity{}, ix)
	byID := map[string]Entity{}
	for _, e := range ents {
		byID[e.ID] = e
	}
	ds := byID["wl:cluster-a/shop/DaemonSet/log-agent"]
	if ds.Type != TypeWorkload || ds.Labels["source"] != "attr" || ds.Labels["derived"] != "" {
		t.Fatalf("açık DaemonSet: %+v", ds)
	}
	if _, ok := byID["wl:cluster-a/shop/Derived/log-agent"]; ok {
		t.Fatal("açık ad varken Derived kimlik doğmamalı")
	}
	if byID["wl:cluster-a/shop/Deployment/billing-api"].Labels["derived"] != "pod-name" {
		t.Fatal("tablonun eşleyemediği pod ad türevinde kalmalı")
	}
	if byID["pod:cluster-a/shop/log-agent-x2x9z"].ParentID != "wl:cluster-a/shop/DaemonSet/log-agent" {
		t.Fatalf("pod ebeveyni: %+v", byID["pod:cluster-a/shop/log-agent-x2x9z"])
	}
	serves := map[string]bool{}
	for _, r := range rels {
		if r.Type == RelServes {
			serves[r.ParentID+">"+r.ChildID] = true
		}
	}
	for _, want := range []string{
		"wl:cluster-a/shop/Deployment/checkout-api>svc:checkout-api",
		"wl:cluster-a/shop/DaemonSet/log-agent>svc:log-agent",
		"wl:cluster-a/shop/Deployment/orders-v2>svc:orders-api",
	} {
		if !serves[want] {
			t.Errorf("serves eksik: %s (%v)", want, serves)
		}
	}
}

type fakeExplicit struct {
	n    int
	rows []ExplicitWorkload
	err  error
}

func (f *fakeExplicit) RecentWorkloads(ctx context.Context, since time.Time, limit int) ([]ExplicitWorkload, bool, error) {
	f.n++
	return f.rows, false, f.err
}

func TestRefreshExplicitAbsentAndCadence(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	sy := NewSyncer(&fakeSource{}, newMemStore(), func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return now }
	fe := &fakeExplicit{err: fmt.Errorf("%w: code 60", ErrExplicitWorkloadsAbsent)}
	sy.SetExplicitWorkloads(fe)
	sy.refreshExplicit(context.Background(), nil)
	if fe.n != 1 || sy.explicitIndexes(now) != nil {
		t.Fatal("tablo yokken dizin olmamalı")
	}
	fe.err, fe.rows = nil, []ExplicitWorkload{{ClusterValue: "cluster-a", Namespace: "shop", Name: "checkout-api", Kind: "Deployment", Revision: "checkout-api-7d9f8b6c5d", Service: "checkout-api"}}
	now = now.Add(time.Minute)
	sy.refreshExplicit(context.Background(), nil)
	if fe.n != 1 {
		t.Fatal("ExplicitWorkloadsEvery dolmadan okuma")
	}
	now = now.Add(ExplicitWorkloadsEvery)
	sy.refreshExplicit(context.Background(), []ClusterRef{{ID: "cluster-a", Name: "cluster-a"}})
	if fe.n != 2 || sy.explicitIndexes(now)["cluster-a"] == nil {
		t.Fatal("dizin kurulmalı")
	}
	if sy.explicitIndexes(now.Add(ExplicitWorkloadsTTL+time.Minute)) != nil {
		t.Fatal("TTL sonrası dizin kullanılmamalı")
	}
}
