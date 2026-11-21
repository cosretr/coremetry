package vmetrics

import (
	"reflect"
	"testing"
)

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
