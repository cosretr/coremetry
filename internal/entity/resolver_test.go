package entity

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSrc struct {
	seen  []SeenEdge
	links []DeploymentLink
	known []string
	calls int
	err   error
}

func (f *fakeSrc) SeenForPods(_ context.Context, _ []string, _ string, pods []string, _, _ time.Time) ([]SeenEdge, error) {
	f.calls++
	want := map[string]bool{}
	for _, p := range pods {
		want[p] = true
	}
	var out []SeenEdge
	for _, e := range f.seen {
		if want[e.Pod] {
			out = append(out, e)
		}
	}
	return out, f.err
}
func (f *fakeSrc) SeenPods(_ context.Context, _ []string, ns string, _, _ time.Time) ([]SeenEdge, error) {
	f.calls++
	var out []SeenEdge
	for _, e := range f.seen {
		if ns == "" || e.Namespace == ns {
			out = append(out, e)
		}
	}
	return out, f.err
}
func (f *fakeSrc) SeenForService(_ context.Context, svc string, _ []string, _, _ time.Time) ([]SeenEdge, error) {
	f.calls++
	var out []SeenEdge
	for _, e := range f.seen {
		if e.Service == svc {
			out = append(out, e)
		}
	}
	return out, f.err
}
func (f *fakeSrc) DeploymentLinks(context.Context) ([]DeploymentLink, error) { return f.links, nil }
func (f *fakeSrc) KnownServices(context.Context) ([]string, error)           { return f.known, nil }

type fakeClusters struct{}

func (fakeClusters) Values(cid string) []string {
	if cid == "c-1" {
		return []string{"ocpma"}
	}
	return nil
}
func (fakeClusters) ID(v string) (string, bool) { return "c-1", v == "ocpma" }

// v0.10.2193 (Entity S0) — öncelik tablosu (plan §2.4): pod → servis span
// payı; iş yükü → servis pod runs çoğunluğu > attr bağı > ad eşitliği;
// belirsiz → boş; pod ve iş yükü aynı cevabı verir.
func TestResolverPathsPriority(t *testing.T) {
	src := &fakeSrc{
		seen: []SeenEdge{
			{Namespace: "digital", Pod: "dash-7d9f-a", Node: "w-1", Service: "bsa-digital-dashboard-prod", Spans: 900},
			{Namespace: "digital", Pod: "dash-7d9f-a", Node: "w-1", Service: "otel-agent", Spans: 10},
			{Namespace: "", Pod: "dash-7d9f-b", Node: "w-2", Service: "bsa-digital-dashboard-prod", Spans: 5}, // ns basılmamış
			{Namespace: "other", Pod: "lone-1", Service: "leak", Spans: 99},
		},
		links: []DeploymentLink{{Service: "pay-svc", Deployment: "payments", Namespace: "digital"}},
		known: []string{"Batch-Job", "cart"},
	}
	r := NewResolver(src, fakeClusters{})
	wl := func(n string) string { return WorkloadID("c-1", "digital", "Deployment", n) }
	pod := func(n string) string { return PodID("c-1", "digital", n) }
	owned := map[string][]string{
		wl("dash"):      {pod("dash-7d9f-a"), pod("dash-7d9f-b")},
		wl("payments"):  {pod("payments-1")},
		wl("batch-job"): {pod("batch-job-1")},
		wl("mystery"):   {pod("mystery-1")},
	}
	ids := []string{pod("dash-7d9f-a"), pod("dash-7d9f-b"), pod("lone-1"), wl("dash"), wl("payments"), wl("batch-job"), wl("mystery"), "svc:ignored", "garbage"}
	got, err := r.Paths(context.Background(), ids, time.Now(), owned)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		pod("dash-7d9f-a"): "bsa-digital-dashboard-prod", // span payı
		pod("dash-7d9f-b"): "bsa-digital-dashboard-prod", // ns'siz satır sayılır
		pod("lone-1"):      "",                           // başka ns'nin kenarı sızmaz
		wl("dash"):         "bsa-digital-dashboard-prod", // pod runs çoğunluğu
		wl("payments"):     "pay-svc",                    // attr bağı
		wl("batch-job"):    "Batch-Job",                  // ad eşitliği (son çare)
		wl("mystery"):      "",
	}
	for id, want := range cases {
		if s := got[id].ServiceName(); s != want {
			t.Errorf("%s: servis %q, want %q", id, s, want)
		}
	}
	if p := got[pod("dash-7d9f-a")]; p.Workload != wl("dash") || p.NodeName() != "w-1" || p.Cluster != "cluster:c-1" || p.Namespace != "ns:c-1/digital" {
		t.Errorf("pod path %+v", p)
	}
	if len(got) != 7 {
		t.Errorf("yalnız pod/wl id'leri: %d", len(got))
	}
	if src.calls != 1 {
		t.Errorf("tek MV okuması beklenirdi: %d", src.calls)
	}
}

func TestResolverPathsFailOpen(t *testing.T) {
	src := &fakeSrc{err: errors.New("ch down")}
	r := NewResolver(src, fakeClusters{})
	id := PodID("c-1", "ns", "p")
	got, err := r.Paths(context.Background(), []string{id, PodID("c-unknown", "ns", "p")}, time.Now(), nil)
	if err == nil || got[id].Service != "" || got[id].Cluster != "cluster:c-1" {
		t.Fatalf("fail-open: %v %+v", err, got)
	}
}

func TestResolverPathsChunks(t *testing.T) {
	src := &fakeSrc{}
	r := NewResolver(src, fakeClusters{})
	var ids []string
	for i := 0; i < MaxPathIDs+1; i++ {
		ids = append(ids, PodID("c-1", "ns", time.Duration(i).String()))
	}
	if _, err := r.Paths(context.Background(), ids, time.Now(), nil); err != nil || src.calls != 2 {
		t.Fatalf("≤%d pod/okuma: calls=%d err=%v", MaxPathIDs, src.calls, err)
	}
}

func TestResolverLookup(t *testing.T) {
	src := &fakeSrc{seen: []SeenEdge{
		{Namespace: "ns", Pod: "p", Service: "minor", Spans: 1},
		{Namespace: "ns", Pod: "p", Service: "major", Spans: 50},
	}}
	r := NewResolver(src, fakeClusters{})
	got := r.Lookup(context.Background(), Hint{ClusterValue: "ocpma", Namespace: "ns", Pod: "p", Workload: "w"}, time.Now())
	want := []string{"pod:c-1/ns/p", "svc:major", "svc:minor", "wl:c-1/ns/Derived/w"}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, n := range got {
		if n.ID != want[i] {
			t.Errorf("[%d] %s, want %s", i, n.ID, want[i])
		}
	}
	if got := r.Lookup(context.Background(), Hint{ClusterValue: "unmapped", Namespace: "ns", Pod: "p"}, time.Now()); len(got) != 0 {
		t.Errorf("kayıtsız cluster değeri → pod yok (K2 S1'de): %+v", got)
	}
}

// v0.10.2195 — pod yolu kenarın span cluster değerini taşır (trace süzgeci).
func TestResolverPathsClusterValue(t *testing.T) {
	src := &fakeSrc{seen: []SeenEdge{{Namespace: "ns", Pod: "p", Service: "s", Spans: 3, Cluster: "OCPA"}}}
	id := PodID("c-1", "ns", "p")
	got, _ := NewResolver(src, fakeClusters{}).Paths(context.Background(), []string{id}, time.Now(), nil)
	if got[id].ClusterValue != "OCPA" {
		t.Fatalf("%+v", got[id])
	}
}
