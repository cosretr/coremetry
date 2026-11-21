package vmetrics

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTeamsFromLabelRows(t *testing.T) {
	rows := []map[string]string{
		{"service_name": "svc-metric-only", "ug_team": "team-payments", "sy_team": "sre-core"},
		{"service_name": "svc-metric-only", "ug_team": "team-alpha"}, // kararlı: sözlükçe küçük
		{"service_name": "svc-no-team"},
		{"ug_team": "team-orphan"},
	}
	got := TeamsFromLabelRows(rows, "service_name")
	want := map[string]chstore.ServiceTeams{"svc-metric-only": {OwnerTeam: "team-alpha", SRETeam: "sre-core"}}
	if len(got) != 1 || got["svc-metric-only"] != want["svc-metric-only"] {
		t.Fatalf("got %+v", got)
	}
	if q := TeamLabelQuery("service_name"); !strings.HasPrefix(q, "count by (service_name, ug_team, sy_team)") || !strings.Contains(q, "[1h]") {
		t.Fatalf("query %s", q)
	}
}
