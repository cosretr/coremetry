package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.802 (denetim I1) — bağlanan daha şiddetli problem incident'ı
// YÜKSELTİR (yalnız yukarı), olay yaşam döngüsü kancasından geçer (bildirim).
func TestAttachRaisesIncidentSeverityOnlyUpward(t *testing.T) {
	src, err := os.ReadFile("incident.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (s *Store) AttachProblemToIncidentWith(")
	j := strings.Index(s, "// IncidentProblems lists all problem ids attached to an incident.")
	if i < 0 || j < 0 || j < i {
		t.Fatal("AttachProblemToIncidentWith bulunamadı")
	}
	body := s[i:j]
	for _, want := range []string{
		"inc.Severity = p.Severity",
		`Kind: "severity_raised"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// Yükseltme kancadan geçmeli (bildirim), ham AppendIncidentEvent değil.
	k := strings.Index(body, `Kind: "severity_raised"`)
	if !strings.Contains(body[max(0, k-300):k], "AppendIncidentLifecycle(") {
		t.Error("severity_raised olayı AppendIncidentLifecycle ile yazılmalı")
	}
	// Sıra: critical > warning > info; bilinmeyen/boş şiddet warning sayılır
	// (severityRank) — bu yüzden kapı isKnownSeverity ister.
	if !(severityRank("critical") > severityRank("warning") && severityRank("warning") > severityRank("info")) {
		t.Error("severityRank sırası")
	}
	if !strings.Contains(body, "isKnownSeverity(p.Severity) && severityRank(p.Severity) > severityRank(inc.Severity)") {
		t.Error("yükseltme kapısı bilinmeyen şiddete kapalı olmalı (isKnownSeverity)")
	}
	for s, want := range map[string]bool{"critical": true, "warning": true, "info": true, "": false, "high": false} {
		if isKnownSeverity(s) != want {
			t.Errorf("isKnownSeverity(%q) = %v", s, !want)
		}
	}
}
