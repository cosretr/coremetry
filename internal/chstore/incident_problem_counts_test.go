package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.797 — Incidents listesi bağlı problem sayısı: tek sınırlı sorgu,
// LEFT JOIN (yaşı geçmiş problem sayılmaz — OpenIncidentRollups ile aynı),
// liste handler'ı zenginleştirir, hata soft-fail.
func TestIncidentProblemCountsQueryShapeAndConsumer(t *testing.T) {
	src, err := os.ReadFile("incident_problem_counts.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		"FROM incident_problems ip FINAL", "LEFT JOIN problems p FINAL ON p.id = ip.problem_id",
		"WHERE ip.incident_id IN (?)", "LIMIT 500", "max_execution_time = 10",
		"countIf(p.id != '' AND p.status != 'resolved')",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sorgu şekli eksik: %s", want)
		}
	}
	if strings.Contains(s, "telemetryReadConn") {
		t.Error("state tabloları ana bağlantıda okunur")
	}
	api, err := os.ReadFile("../api/api_incidents.go")
	if err != nil {
		t.Fatal(err)
	}
	a := string(api)
	list := a[strings.Index(a, "func (s *Server) listIncidents"):strings.Index(a, "func (s *Server) getIncident")]
	if !strings.Contains(list, "EnrichIncidentsWithProblemCounts(") {
		t.Error("listIncidents sayıları zenginleştirmeli — sütun sayfada görünmez")
	}
}

func TestEnrichIncidentsWithProblemCountsEmptyIsNoop(t *testing.T) {
	var s Store // conn nil — boş girişte sorguya gitmemeli
	if got := s.EnrichIncidentsWithProblemCounts(t.Context(), nil); len(got) != 0 {
		t.Fatalf("boş giriş: %v", got)
	}
}
