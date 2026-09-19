package notify

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.802 — severity_raised olayı açık incident'ın YENİ şiddetiyle alarm
// üretir; dedup (allowChannelSend) şiddet artışını geçirdiği için "created"
// warning olarak gitmiş olsa da critical yeniden sayfaya düşer.
func TestIncidentAsProblemSeverityRaised(t *testing.T) {
	inc := chstore.Incident{ID: "i1", Title: "shop — errors", Severity: "critical", Service: "shop", StartedAt: 1}
	p := incidentAsProblem(inc, chstore.IncidentEvent{Kind: "severity_raised", Actor: "system", Body: "warning → critical (Critical error rate)"})
	if p.Status != "open" || p.Severity != "critical" || p.ID != "i1" || p.Kind != chstore.NotifyKindIncident {
		t.Fatalf("%+v", p)
	}
	if got := severityRank("critical"); got <= severityRank("warning") {
		t.Fatal("notify severityRank: critical > warning olmalı (dedup artışı geçirir)")
	}
}
