package main

// budget_test.go — v0.10.1817 (Log Search Faz 1 dilim 1.1): budget.json
// noktalarının biçimi. windowNs ayrışmayan pencereyi sessizce 1 saate
// çevirir; "15 m" gibi bir yazım hatası 15 dk yerine 1 saati ölçerdi.
// Bilinmeyen yer tutucu da ham kalır ve 400 ile "invalid" olur — burada
// erken yakalanır. Üç log noktası adla pinlenir.

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestBudgetPointsWellFormed(t *testing.T) {
	raw, err := os.ReadFile("../../scripts/perf/budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var bf budgetFile
	if err := json.Unmarshal(raw, &bf); err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{"{from}": true, "{to}": true, "{service}": true, "{traceIds}": true}
	ph := regexp.MustCompile(`\{[a-zA-Z]+\}`)
	seen := map[string]bool{}
	for _, p := range bf.Points {
		id := p.Name + " / " + p.Scenario
		if seen[id] {
			t.Errorf("yinelenen nokta %s", id)
		}
		seen[id] = true
		if p.Window != "" {
			if d, err := time.ParseDuration(p.Window); err != nil || d <= 0 {
				t.Errorf("%s: pencere %q ayrışmıyor", id, p.Window)
			}
		}
		for _, m := range ph.FindAllString(p.Path, -1) {
			if !known[m] {
				t.Errorf("%s: bilinmeyen yer tutucu %s", id, m)
			}
		}
		if p.Budget.AchievableMs <= 0 {
			t.Errorf("%s: achievableMs yok", id)
		}
	}
	for _, want := range []string{
		"GET /api/logs / first-rows-15m-service",
		"GET /api/logs / first-rows-24h-service",
		"GET /api/logs/patterns / patterns-1h-service",
	} {
		if !seen[want] {
			t.Errorf("log perf noktası eksik: %s", want)
		}
	}
}
