package chstore

import (
	"strings"
	"testing"
)

// incident_rootcause_test.go — v0.10.698; seçici + eşik testleri
// v0.10.2075'te (Incidents ölü kod 5) zenginleştirmeyle birlikte silindi.

// Toplu okuma şekli: tek IN-listesi + tavan + max_execution_time; tek tek
// IncidentProblems çağrısı (N+1) değil.
func TestIncidentRootCauseBatchShape(t *testing.T) {
	src := mustReadSource(t, "incident_rootcause.go")
	for _, want := range []string{
		"FROM incident_problems FINAL",
		"WHERE incident_id IN (",
		"max_execution_time = 10",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("incident_rootcause.go %q içermeli", want)
		}
	}
	if strings.Contains(src, "s.IncidentProblems(ctx") {
		t.Error("incident başına IncidentProblems çağrısı N+1 — toplu IncidentProblemIDs kullan")
	}
}
