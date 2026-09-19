package chstore

// incident_lifecycle_test.go — v0.10.748 incident bildirimi. Saf yarı
// (hangi olay bildirir; incident-şekilli problemin önceliği; tür
// sınıflandırması) + ULAŞILABİLİRLİK pinleri: dört yaşam döngüsü noktası
// AppendIncidentLifecycle'dan geçer ve main.go kancayı bağlar — biri ham
// AppendIncidentEvent'e dönerse o yol sessizce bildirimsiz kalır.

import (
	"os"
	"strings"
	"testing"
)

func TestIncidentLifecycleNotifies(t *testing.T) {
	for kind, want := range map[string]bool{
		"created": true, "resolved": true, "severity_raised": true, // v0.10.802
		"ack": false, "note": false, "problem_attached": false, "problem_resolved": false, "": false,
	} {
		if got := incidentLifecycleNotifies(kind); got != want {
			t.Errorf("kind=%q → %v, istenen %v", kind, got, want)
		}
	}
}

func TestIncidentShapedProblemPriority(t *testing.T) {
	// notify.incidentAsProblem RuleID'yi "incident:<id>" yazar; formül
	// eşik/oran/yaş bakmaz: critical = şimdi (P1), warning = bugün (P2),
	// info = P3 (genel kural).
	cases := []struct{ sev, want string }{
		{"critical", "P1"}, {"warning", "P2"}, {"info", "P3"},
	}
	for _, c := range cases {
		got, _ := computePriority(Problem{RuleID: "incident:abc", Severity: c.sev}, 0, ProblemPriorityConfig{})
		if got != c.want {
			t.Errorf("incident sev=%s → %s, istenen %s", c.sev, got, c.want)
		}
	}
	if got := ProblemNotifyKind(Problem{RuleID: "incident:abc"}); got != NotifyKindIncident {
		t.Errorf("incident: öneki %q sınıflandı, incident bekleniyordu", got)
	}
}

func TestIncidentLifecycleReachable(t *testing.T) {
	pins := []struct {
		path string
		call string
		want int
	}{
		{"incident.go", "s.AppendIncidentLifecycle(ctx, inc,", 2},    // otomatik korelasyon açılışı + şiddet yükselişi (v0.10.802)
		{"../api/api_incidents.go", "AppendIncidentLifecycle(", 2},   // manuel açılış + manuel çözüm
		{"../evaluator/evaluator.go", "AppendIncidentLifecycle(", 1}, // otomatik kademeli çözüm
		{"../../main.go", "SetIncidentLifecycleHook(notifier.IncidentLifecycle)", 1},
	}
	for _, p := range pins {
		src, err := os.ReadFile(p.path)
		if err != nil {
			t.Fatalf("%s: %v", p.path, err)
		}
		if n := strings.Count(string(src), p.call); n != p.want {
			t.Errorf("%s: %q %d kez, istenen %d", p.path, p.call, n, p.want)
		}
	}
	// created/resolved olayı ham AppendIncidentEvent ile yazan kalmadı.
	for _, path := range []string{"incident.go", "../api/api_incidents.go", "../evaluator/evaluator.go"} {
		src, _ := os.ReadFile(path)
		s := string(src)
		for _, kind := range []string{`Kind: "created"`, `Kind:       "resolved"`, `Kind: "resolved"`, `Kind: "severity_raised"`} {
			i := strings.Index(s, kind)
			for i >= 0 {
				head := s[max(0, i-220):i]
				if strings.Contains(head, "AppendIncidentEvent(") && !strings.Contains(head, "AppendIncidentLifecycle(") {
					t.Errorf("%s: %s ham AppendIncidentEvent ile yazılıyor — kanca atlanır", path, kind)
				}
				j := strings.Index(s[i+1:], kind)
				if j < 0 {
					break
				}
				i += 1 + j
			}
		}
	}
}
