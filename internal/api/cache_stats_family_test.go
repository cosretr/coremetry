package api

import "testing"

// v0.10.2227 (Redis R1): aile = kod sabiti önek; kullanıcı girdisi ölçü
// etiketine sızmaz.
func TestCacheKeyFamily(t *testing.T) {
	cases := map[string]string{
		"problems:v3:abc": "problems",
		"svc-bundle:x":    "svc-bundle",
		"nocolon":         "other",
		":leading":        "other",
		"Upper:x":         "other",
		"a b:x":           "other",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:x": "other",
	}
	for k, want := range cases {
		if got := cacheKeyFamily(k); got != want {
			t.Errorf("cacheKeyFamily(%q)=%q, beklenen %q", k, got, want)
		}
	}
}

func TestCacheStatsFamilies(t *testing.T) {
	cs := newCacheStats()
	cs.record("MISS", "services:a")
	cs.record("HIT", "services:a")
	cs.record("HIT", "services:b")
	cs.record("MISS", "clusters:x")
	snap := cs.snapshot(nil)
	if snap.Counts["HIT"] != 2 || snap.Counts["MISS"] != 2 {
		t.Fatalf("katman toplamları: %v", snap.Counts)
	}
	if snap.Families["services"]["HIT"] != 2 || snap.Families["clusters"]["MISS"] != 1 {
		t.Fatalf("aile kırılımı: %v", snap.Families)
	}
	if len(snap.TopKeys) != 2 {
		t.Fatalf("yalnız isabetler keyHits'e: %v", snap.TopKeys)
	}
}
