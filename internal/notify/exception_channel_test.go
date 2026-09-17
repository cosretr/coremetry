package notify

// exception_channel_test.go — v0.10.782: exception / HTTP-hata grupları →
// kanallar. Tazelik kapısı, kimlik gidiş-dönüşü, sentetik problem, kablolama.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestIsChannelCandidate(t *testing.T) {
	now := time.Date(2026, 9, 17, 23, 30, 0, 0, time.UTC)
	mk := func(firstMin, lastMin int, occ uint64) chstore.ExceptionGroup {
		return chstore.ExceptionGroup{
			FirstSeen:   now.Add(-time.Duration(firstMin) * time.Minute).UnixNano(),
			LastSeen:    now.Add(-time.Duration(lastMin) * time.Minute).UnixNano(),
			Occurrences: occ,
		}
	}
	cases := []struct {
		name  string
		g     chstore.ExceptionGroup
		state string
		want  bool
	}{
		{"yeni + taze", mk(5, 1, 169), chstore.ExStateNew, true},
		{"yeni ama ilk görülme 22 sa önce (yapışkan P1 yığını)", mk(22*60, 1, 1171), chstore.ExStateNew, false},
		{"yeni, ilk görülme taze ama 12 dk'dır sustu", mk(14, 12, 50), chstore.ExStateNew, false},
		{"regressed + aktif (ilk görülme eski olsa da)", mk(4*24*60, 3, 33), chstore.ExStateRegressed, true},
		{"regressed ama sustu", mk(4*24*60, 30, 33), chstore.ExStateRegressed, false},
		{"tek oluşum", mk(1, 0, 1), chstore.ExStateNew, false},
		{"resolved durumu aday değil", mk(1, 0, 9), chstore.ExStateResolved, false},
	}
	for _, c := range cases {
		if got := isChannelCandidate(c.g, c.state, now); got != c.want {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}
}

func TestExceptionGroupIDRoundTrip(t *testing.T) {
	if id := exceptionGroupID("abc", chstore.ExStateNew); id != "exception-group:abc" || exceptionGroupFingerprint(id) != "abc" {
		t.Errorf("new: %q", id)
	}
	if id := exceptionGroupID("abc", chstore.ExStateRegressed); id != "exception-group:abc:regressed" || exceptionGroupFingerprint(id) != "abc" {
		t.Errorf("regressed: %q", id)
	}
	if exceptionGroupFingerprint("incident:1") != "" {
		t.Error("başka kimlik → boş")
	}
}

func TestExceptionGroupProblem(t *testing.T) {
	g := chstore.ExceptionGroup{Fingerprint: "fp1", Type: "504", Message: "upstream timeout", Service: "shop-gateway", Occurrences: 169, FirstSeen: 100, LastSeen: 200}
	p := exceptionGroupProblem(g, chstore.ExStateNew, "P2", "taze && ≥100")
	if p.ID != "exception-group:fp1" || p.RuleID != "exception-group:new" || p.Severity != "warning" || p.Status != "open" {
		t.Errorf("alanlar: %+v", p)
	}
	if p.RuleName != "HTTP hatası · 504" || p.Priority != "P2" || p.PriorityReason != "taze && ≥100" || p.StartedAt != 100 {
		t.Errorf("ad/öncelik: %+v", p)
	}
	if chstore.ProblemNotifyKind(p) != chstore.NotifyKindException || problemRelatedKind(p) != "exception" {
		t.Errorf("tür sınıflandırması: %s / %s", chstore.ProblemNotifyKind(p), problemRelatedKind(p))
	}
	// P1 → critical; exception tipi → "Exception · <tip>"; regressed etiketi.
	q := exceptionGroupProblem(chstore.ExceptionGroup{Fingerprint: "fp2", Type: "java.sql.SQLException", Occurrences: 900}, chstore.ExStateRegressed, "P1", "patlama")
	if q.Severity != "critical" || q.RuleName != "Exception · java.sql.SQLException" || !strings.Contains(q.Description, "regressed") || q.ID != "exception-group:fp2:regressed" {
		t.Errorf("P1/regressed: %+v", q)
	}
	// Öncelik hunide EZİLMEZ (computePriority önek dalı).
	if w := withPriority(p); w.Priority != "P2" || w.PriorityReason != "taze && ≥100" {
		t.Errorf("withPriority ezdi: %s (%s)", w.Priority, w.PriorityReason)
	}
}

// Kablolama: main enjekte eder, ekip maili bu önekte atlanır, huni Kind=exception görür.
func TestExceptionChannelWiring(t *testing.T) {
	m, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m), "notify.NewExceptionNotifier(store, notifier, lockImpl, api.ExceptionPriority)") {
		t.Error("main.go merdiveni enjekte etmeli")
	}
	n, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(n), `p.Status == "open" && !strings.HasPrefix(p.RuleID, chstore.ExceptionGroupRulePrefix)`) {
		t.Error("ekip maili exception grubu için atlanmalı (anons ayrı)")
	}
}
