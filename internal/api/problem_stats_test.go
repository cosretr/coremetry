package api

import (
	"os"
	"strings"
	"testing"
	"time"
)

// v0.10.774 — pencere seçimi + route defteri pini (api.go büyümez).
func TestProblemStatsWindow(t *testing.T) {
	for in, want := range map[string]string{"": "24h", "x": "24h", "1h": "1h", " 6h ": "6h", "7d": "7d"} {
		if got, _ := problemStatsWindow(in); got != want {
			t.Errorf("%q → %q, istenen %q", in, got, want)
		}
	}
	if _, d := problemStatsWindow("7d"); d != 7*24*time.Hour {
		t.Error("7d süresi")
	}
}

func TestProblemStatsRouteLivesInOwnFile(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(src), "/api/problems/stats") {
		t.Error("route api.go'ya sızmış — problem_stats.go + route_registry")
	}
	own, err := os.ReadFile("problem_stats.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, must := range []string{`registerRoutesExtra("problem-stats"`, `"GET /api/problems/stats"`, "serveCached"} {
		if !strings.Contains(string(own), must) {
			t.Errorf("%q yok", must)
		}
	}
}
