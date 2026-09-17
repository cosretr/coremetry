package notify

// incident_alert_test.go — v0.10.748 incident bildirimi: şekillendirici
// sözleşmesi (kimlik grameri, durum eşlemesi, varsayılanlar), konu linki,
// öncelik ve tür sınıflandırması, related_kind; ve yedi şablonun tümünün
// subjectURL'den geçtiğinin pini.

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestIncidentAsProblem(t *testing.T) {
	resolved := int64(1_700_000_100) * 1e9
	inc := chstore.Incident{
		ID: "9f2c", Title: "shop — p99 burst", Severity: "critical", Service: "shop",
		Summary: "3 problem attached", StartedAt: 1_700_000_000 * 1e9, Clusters: []string{"prod-eu"},
	}
	p := incidentAsProblem(inc, chstore.IncidentEvent{Kind: "created", Actor: "system", Body: "Auto-created from p99 rule"})
	if p.ID != "9f2c" || p.RuleID != "incident:9f2c" || p.Kind != chstore.NotifyKindIncident || p.Metric != "incident" {
		t.Fatalf("kimlik grameri: %+v", p)
	}
	if p.Status != "open" || p.Severity != "critical" || p.Service != "shop" || p.StartedAt != inc.StartedAt {
		t.Fatalf("alanlar: %+v", p)
	}
	for _, want := range []string{"shop — p99 burst", "3 problem attached", "Auto-created from p99 rule", "actor: system"} {
		if !strings.Contains(p.Description, want) {
			t.Errorf("açıklama %q içermeli: %q", want, p.Description)
		}
	}
	if !strings.HasPrefix(p.RuleName, "Incident · ") || len(p.Clusters) != 1 {
		t.Errorf("başlık/cluster: %+v", p)
	}
	// Tür + öncelik: kanal süzgeci "incident", critical → P1.
	if k := chstore.ProblemNotifyKind(p); k != chstore.NotifyKindIncident {
		t.Errorf("tür %q, incident bekleniyordu", k)
	}
	if out := chstore.EnrichProblemsWithPriority([]chstore.Problem{p}); len(out) != 1 || out[0].Priority != "P1" {
		t.Errorf("critical incident P1 olmalı: %+v", out)
	}
	// Çözüm olayı → resolved; manuel aktör gövdede; warning → P2.
	inc.Severity = ""
	inc.ResolvedAt = &resolved
	r := incidentAsProblem(inc, chstore.IncidentEvent{Kind: "resolved", Actor: "cenk", Body: "Incident resolved"})
	if r.Status != "resolved" || r.Severity != "warning" || r.ResolvedAt == nil || *r.ResolvedAt != resolved {
		t.Fatalf("çözüm: %+v", r)
	}
	if !strings.Contains(r.Description, "actor: cenk") {
		t.Errorf("manuel aktör açıklamada yok: %q", r.Description)
	}
	if out := chstore.EnrichProblemsWithPriority([]chstore.Problem{r}); out[0].Priority != "P2" {
		t.Errorf("warning incident P2 olmalı: %s", out[0].Priority)
	}
	// Boş aktör → system.
	if s := incidentAsProblem(inc, chstore.IncidentEvent{Kind: "created"}); !strings.Contains(s.Description, "actor: system") {
		t.Errorf("boş aktör system olmalı: %q", s.Description)
	}
}

func TestSubjectURLAndRelatedKind(t *testing.T) {
	n := New(nil)
	n.SetPublicURL("https://coremetry.example.com")
	inc := incidentAsProblem(chstore.Incident{ID: "9f2c", Title: "t"}, chstore.IncidentEvent{Kind: "created"})
	if got := n.subjectURL(inc); got != "https://coremetry.example.com/incident?id=9f2c" {
		t.Errorf("incident linki: %s", got)
	}
	if got := n.subjectURL(chstore.Problem{ID: "p1"}); got != "https://coremetry.example.com/problems?problem=p1" {
		t.Errorf("problem linki: %s", got)
	}
	n.SetPublicURL("")
	if n.subjectURL(inc) != "" {
		t.Error("public URL yokken link boş olmalı")
	}
	if got := problemRelatedKind(inc); got != "incident" {
		t.Errorf("related_kind %q, incident bekleniyordu", got)
	}
}

func TestAllTemplatesUseSubjectURL(t *testing.T) {
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if n := strings.Count(s, "n.problemURL(p.ID)"); n != 0 {
		t.Fatalf("notify.go'da %d ham problemURL(p.ID) çağrısı kaldı — incident linki /problems'a düşer", n)
	}
	if n := strings.Count(s, "n.subjectURL(p)"); n < 7 {
		t.Fatalf("subjectURL çağrısı %d (< 7): şablonlar konu linkinden geçmiyor", n)
	}
}
