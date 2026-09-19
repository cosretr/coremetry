package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.796 — incident önceliği tek kaynak; Inbox, liste ve detay aynı
// işlevi okur.
func TestIncidentPriorityLadder(t *testing.T) {
	cases := []struct {
		sev, status, wantP string
		wantReason         string
	}{
		{"critical", "open", "P1", "Declared incident, critical"},
		{"critical", "acknowledged", "P2", "acknowledged"},
		{"critical", "resolved", "P1", "Declared incident, critical"},
		{"warning", "open", "P2", "Declared incident, warning"},
		{"warning", "acknowledged", "P3", "acknowledged"},
		{"info", "open", "P3", "Declared incident"},
		{"info", "acknowledged", "P3", "Declared incident"},
		{"", "open", "P3", "Declared incident"},
	}
	for _, c := range cases {
		p, r := IncidentPriority(Incident{Severity: c.sev, Status: c.status})
		if p != c.wantP || !strings.Contains(r, c.wantReason) || r == "" {
			t.Errorf("%s/%s → %s %q, istenen %s ~%q", c.sev, c.status, p, r, c.wantP, c.wantReason)
		}
	}
	rows := EnrichIncidentsWithPriority([]Incident{{Severity: "critical", Status: "open"}, {Severity: "warning", Status: "acknowledged"}})
	if rows[0].Priority != "P1" || rows[1].Priority != "P3" || rows[1].PriorityReason == "" {
		t.Errorf("enrich: %+v", rows)
	}
}

// Tüketiciler tek kaynağı okur: Inbox eşlemesi burada, liste + detay
// zenginleştirmesi api_incidents.go'da (kural görünür — v0.10.364 dersi).
func TestIncidentPriorityConsumers(t *testing.T) {
	inbox, err := os.ReadFile("../api/inbox.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(inbox), "chstore.IncidentPriority(inc)") {
		t.Error("inbox.go incidentToInbox chstore.IncidentPriority'yi okumalı (ikinci kopya yok)")
	}
	if strings.Contains(string(inbox), `"Declared incident, critical"`) {
		t.Error("inbox.go'da öncelik metni kopyası kaldı")
	}
	src, err := os.ReadFile("../api/api_incidents.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	list := s[strings.Index(s, "func (s *Server) listIncidents"):strings.Index(s, "func (s *Server) getIncident")]
	get := s[strings.Index(s, "func (s *Server) getIncident"):strings.Index(s, "func (s *Server) createIncident")]
	for name, body := range map[string]string{"listIncidents": list, "getIncident": get} {
		if !strings.Contains(body, "EnrichIncidentsWithPriority(") {
			t.Errorf("%s: EnrichIncidentsWithPriority çağrısı yok — P rozeti sayfada görünmez", name)
		}
	}
}
