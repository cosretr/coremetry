package vmetrics

import (
	"reflect"
	"testing"
)

// Follow-up 2026-11-21: küme varlığının span ortamlarından harita; belirsiz
// küme ve bilgi taşımayan (ortam = küme adı) çift dışarıda.
func TestClusterEnvMap(t *testing.T) {
	got := ClusterEnvMap([][2]string{
		{"clusterB", "clusterB"},      // bilgi yok
		{"clusterB", "prod-clusterB"}, // → prod
		{"clusterA", "prod"},          // → prod
		{"clusterD", "prod-clusterD"},
		{"clusterD", "test"}, // iki ortam → belirsiz
	})
	want := map[string]string{"clusterb": "prod", "clustera": "prod"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("map = %v", got)
	}
	m := MatchEnvRole("prod", []string{"clusterB", "prod-clusterA"}, []string{"clusterA", "clusterB"}, got)
	if !reflect.DeepEqual(m.Values, []string{"clusterB", "prod-clusterA"}) {
		t.Fatalf("match = %+v", m)
	}
}

func TestMatchEnvRoleSplitsEnvCluster(t *testing.T) {
	live := []string{"prod-clusterA", "prod-clusterC", "test-clusterA", "clusterB", "prod-eu"}
	clusters := []string{"clusterA", "clusterB", "clusterC"}

	// "prod" → tüm prod kümeleri; "prod-eu" küme değil (doğrulama), dışarıda.
	m := MatchEnvRole("prod", live, clusters, nil)
	if m.Tier != EnvTierEnvPart || !reflect.DeepEqual(m.Values, []string{"prod-clusterA", "prod-clusterC"}) {
		t.Fatalf("prod = %+v", m)
	}
	// Yalnız küme taşıyan değer: ortam küme varlığından.
	m = MatchEnvRole("prod", live, clusters, map[string]string{"clusterb": "prod"})
	if !reflect.DeepEqual(m.Values, []string{"clusterB", "prod-clusterA", "prod-clusterC"}) {
		t.Fatalf("prod + entity = %+v", m)
	}
	// Birebir değer de varsa envpart ona eklenir.
	m = MatchEnvRole("prod", append([]string{"prod"}, live...), clusters, nil)
	if m.Tier != EnvTierExact || len(m.Values) != 3 {
		t.Fatalf("exact+envpart = %+v", m)
	}
	// Küme adı istenirse eski prefix kademesi aynen ("clusterA" → iki ortam).
	m = MatchEnvRole("clusterA", live, clusters, nil)
	if m.Tier != EnvTierPrefix || !reflect.DeepEqual(m.Values, []string{"prod-clusterA", "test-clusterA"}) {
		t.Fatalf("cluster want = %+v", m)
	}
	// Küme etiketi okunamadı: ≥3 karakter kuralı ("prod-eu" kalan 2 → dışarıda).
	m = MatchEnvRole("prod", live, nil, nil)
	if !reflect.DeepEqual(m.Values, []string{"prod-clusterA", "prod-clusterC"}) {
		t.Fatalf("no cluster label = %+v", m)
	}
	// Tek başına küme değeri de birebir kümeyle eşleşir (cluster süzgeci).
	if c := MatchEnvValue("clusterB", live); c.Tier != EnvTierExact {
		t.Fatalf("cluster-only = %+v", c)
	}
}
