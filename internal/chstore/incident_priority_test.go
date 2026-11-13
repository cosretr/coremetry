package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.796 — incident önceliği tek kaynak; liste + detay sayfaları
// emekli (v0.10.2075), okuyan yalnız Inbox.
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
}

// Tüketici tek kaynağı okur: Inbox eşlemesi (kural görünür — v0.10.364).
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
}
