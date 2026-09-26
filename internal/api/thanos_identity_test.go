package api

// v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2): PUT dışındaki
// iki thanos_clusters blob yazıcısı — "Detect label" (detect, apply=1) ve span
// değeri atama (assign-span-cluster) — saklı kaydın TAMAMINI kopyalayıp tek
// alanını değiştirir; apiServerUrls / argoSuffix / pairGroup bu yazımlarda
// KAYBOLMAMALI (audit: "bir Save ya da karışık sürüm pod'u alanları siler").
// Handler'ların saf blob hesabı (Reconcile dahil) burada pinlenir; kayıt ve
// yayın I/O'su test dışında.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func identityFieldsCur() thanos.Settings {
	return thanos.Settings{Clusters: []thanos.ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "cluster-a", URL: "http://thanos-a.example.invalid", Enabled: true,
			APIServerURLs: []string{"https://api.cluster-a.example.invalid:6443"}, ArgoSuffix: "ca", PairGroup: "pair-1"},
		{ID: "c-bbbbbbbb", Name: "cluster-b", URL: "http://thanos-b.example.invalid", Enabled: true,
			APIServerURLs: []string{"https://api.cluster-b.example.invalid:6443", "https://api-int.cluster-b.example.invalid:6443"},
			ArgoSuffix:    "cb", PairGroup: "pair-1"},
	}}
}

func assertIdentityFieldsKept(t *testing.T, what string, got, want thanos.ClusterConfig) {
	t.Helper()
	if strings.Join(got.APIServerURLs, ",") != strings.Join(want.APIServerURLs, ",") ||
		got.ArgoSuffix != want.ArgoSuffix || got.PairGroup != want.PairGroup {
		t.Fatalf("%s: rollout alanları korunmalı — alınan %v/%q/%q, beklenen %v/%q/%q", what,
			got.APIServerURLs, got.ArgoSuffix, got.PairGroup, want.APIServerURLs, want.ArgoSuffix, want.PairGroup)
	}
}

func TestAssignSpanClusterKeepsRolloutFields(t *testing.T) {
	cur := identityFieldsCur()
	merged, err := assignSpanClusterSettings(cur, 1, "prod-cluster-b")
	if err != nil {
		t.Fatal(err)
	}
	for i := range cur.Clusters {
		assertIdentityFieldsKept(t, "assign-span-cluster", merged.Clusters[i], cur.Clusters[i])
	}
	if got := merged.Clusters[1].SpanClusterKeys(); strings.Join(got, ",") != "cluster-b,prod-cluster-b" {
		t.Fatalf("atanan değer eklenmeli (örtük ad açıkça korunarak): %v", got)
	}
	// Girdi (canlı yapılandırmanın kopyası) yerinde değişmez.
	if len(cur.Clusters[1].SpanClusterValues) != 0 {
		t.Fatalf("cur yerinde değişmemeli: %+v", cur.Clusters[1])
	}
}

func TestDetectApplyKeepsRolloutFields(t *testing.T) {
	cur := identityFieldsCur()
	d := thanos.Detection{Label: "cluster", Value: "cluster-a", Series: 3}
	merged, err := detectionSettings(cur, 0, d, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i := range cur.Clusters {
		assertIdentityFieldsKept(t, "detect apply", merged.Clusters[i], cur.Clusters[i])
	}
	if c := merged.Clusters[0]; c.ThanosLabelName != "cluster" || c.ThanosLabelSource != "auto" {
		t.Fatalf("algılanan etiket uygulanmalı: %+v", c)
	}
	// Belirsiz algılama yazılmaz: hata döner (handler 200 + error).
	if _, err := detectionSettings(cur, 0, thanos.Detection{Label: "cluster", Ambiguous: true,
		Candidates: map[string][]string{"cluster": {"x", "y"}}}, time.Now()); err == nil {
		t.Fatal("belirsiz algılama hata vermeli")
	}
}
