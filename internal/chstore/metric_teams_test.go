package chstore

import "testing"

// Metrik etiketi takımları EN DÜŞÜK öncelik: span değeri alan alan kazanır.
func TestMergeMetricTeams(t *testing.T) {
	span := map[string]ServiceTeams{
		"svc-a": {OwnerTeam: "team-span", SRETeam: ""},
	}
	metric := map[string]ServiceTeams{
		"svc-a":           {OwnerTeam: "team-metric", SRETeam: "sre-metric"},
		"svc-metric-only": {OwnerTeam: "team-payments", SRETeam: "sre-core"},
		"svc-empty":       {},
	}
	got := MergeMetricTeams(span, metric)
	if got["svc-a"] != (ServiceTeams{OwnerTeam: "team-span", SRETeam: "sre-metric"}) {
		t.Fatalf("svc-a = %+v", got["svc-a"])
	}
	if got["svc-metric-only"] != (ServiceTeams{OwnerTeam: "team-payments", SRETeam: "sre-core"}) {
		t.Fatalf("metric-only = %+v", got["svc-metric-only"])
	}
	if _, ok := got["svc-empty"]; ok {
		t.Fatal("empty teams must not create a row")
	}
	if span["svc-a"].SRETeam != "" {
		t.Fatal("input map must not be mutated")
	}
	if g := MergeMetricTeams(span, nil); len(g) != 1 {
		t.Fatal("nil metric teams = span unchanged")
	}
}
