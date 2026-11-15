package entity

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// v0.10.2261 (S6 parça 2) — metrik kolu: kadans, eşlemesiz atlama, yalnız
// metrik basan servis, canlı dizine metrik runs.

type fakeMetrics struct {
	n    int
	snap MetricSnapshot
	err  error
}

func (f *fakeMetrics) EntityMetricPairs(ctx context.Context) (MetricSnapshot, error) {
	f.n++
	return f.snap, f.err
}

func TestBuildMetricServices(t *testing.T) {
	got := BuildMetricServices([]string{"checkout-api", "metrics-only-job", "", "metrics-only-job", "batch-exporter"},
		map[string]bool{"checkout-api": true}, 1)
	if len(got) != 1 {
		t.Fatalf("tavan: %v", got)
	}
	e, ok := got["svc:batch-exporter"]
	if !ok || e.Source != SourceMetric || e.ClusterID != MetricClusterID {
		t.Fatalf("metrik servis: %+v", got)
	}
}

func TestMetricPodRunsAndHosts(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	ix := IndexMetricRuns(now, []MetricRun{
		{Service: "checkout-api", Pod: "checkout-api-7d9f8b6c5d-x2x9z", Namespace: "shop"},
		{Service: "checkout-api", Pod: "checkout-api-7d9f8b6c5d-x2x9z", Namespace: "other"}, // ns tutmaz
		{Service: "node-exporter", Host: "node-a-01"},
		{Service: "node-exporter", Host: "log-agent-x2x9z"}, // pod şekilli
	})
	ents := map[string]Entity{
		"pod:cluster-a/shop/checkout-api-7d9f8b6c5d-x2x9z": {Type: TypePod, ID: "pod:cluster-a/shop/checkout-api-7d9f8b6c5d-x2x9z", Namespace: "shop", Name: "checkout-api-7d9f8b6c5d-x2x9z"},
	}
	rels := MetricPodRuns("cluster-a", ents, ix)
	if len(rels) != 1 || rels[0].ChildID != "svc:checkout-api" || rels[0].Source != SourceMetric {
		t.Fatalf("pod runs: %+v", rels)
	}
	snap := BuildHostLive(now, map[string]*HostCand{}, nil)
	MergeMetricHosts(&snap, ix, nil)
	if len(snap.Hosts) != 1 || snap.Hosts[0].Name != "node-a-01" || snap.Hosts[0].Source != SourceMetric || snap.Hosts[0].Stable {
		t.Fatalf("metrik host: %+v", snap.Hosts)
	}
}

func TestSyncMetricsLifecycle(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	now := t0
	st := newMemStore()
	seen := &errSeen{rows: []SeenRow{{ClusterValue: "cluster-a", Namespace: "shop", Pod: "checkout-api-0", Service: "checkout-api", Spans: 1}}}
	fm := &fakeMetrics{err: fmt.Errorf("%w: pod etiketi yok", ErrMetricLabelsUnconfigured)}
	sy := NewSyncer(&fakeSource{}, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return now }
	sy.SetSeenReader(seen)
	sy.SetMetricSource(fm)

	// Eşleme yok: geçiş atlanır, yazım / failed koşu yok.
	sy.Tick(context.Background())
	if fm.n != 1 || len(st.open[MetricClusterID]) != 0 {
		t.Fatalf("eşlemesiz: n=%d open=%v", fm.n, st.open[MetricClusterID])
	}
	for _, r := range st.runs {
		if r.ClusterID == MetricClusterID {
			t.Fatalf("eşlemesiz geçiş koşu satırı yazmamalı: %+v", r)
		}
	}
	// Eşleme geldi: 15 dk sonra koşar; span'i olan servis yazılmaz.
	fm.err = nil
	fm.snap = MetricSnapshot{Services: []string{"checkout-api", "batch-exporter"}, Queries: 3}
	now = t0.Add(time.Minute)
	sy.Tick(context.Background())
	if fm.n != 1 {
		t.Fatalf("MetricsEvery dolmadan geçiş: %d", fm.n)
	}
	now = t0.Add(MetricsEvery)
	sy.Tick(context.Background())
	open := st.open[MetricClusterID]
	if _, ok := open["svc:batch-exporter"]; !ok || len(open) != 1 {
		t.Fatalf("yalnız metrik basan servis kalıcı olmalı: %v", open)
	}
	// Backend hatası: failed koşu, kapanış yok.
	fm.err = errors.New("vm down")
	now = now.Add(MetricsEvery)
	sy.Tick(context.Background())
	failed := false
	for _, r := range st.runs {
		failed = failed || (r.ClusterID == MetricClusterID && r.Status == RunFailed)
	}
	if !failed {
		t.Fatal("backend hatası failed koşu satırı yazmalı")
	}
	if _, ok := st.open[MetricClusterID]["svc:batch-exporter"]; !ok {
		t.Fatal("hata kapanış yapmamalı")
	}
}
