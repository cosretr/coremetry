package evaluator

// v0.10.1588 — Davis paritesi FAZ 0.5 / D: kablolama pini. Bağlama
// çekirdekleri (chstore.JoinOpenIncident — chsmoke; bayraklar — chstore
// testleri) ayrı sınanıyor; bu pin çağrıların YERİNDE ve KAPILI kaldığını
// çiviler (test edilmiş ama ulaşılamaz sınıfı): terfi açılışı vidayla
// AttachProblemToIncident, ölümcül / paylaşılan açılış joinOpenIncident
// (asla AttachProblemToIncident — kendi incident'ını açmaz).

import (
	"os"
	"strings"
	"testing"
)

func fnBody(t *testing.T, path, sig string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, sig)
	if i < 0 {
		t.Fatalf("%s: %q yok", path, sig)
	}
	body := s[i:]
	if j := strings.Index(body, "\n}\n"); j >= 0 {
		body = body[:j]
	}
	return body
}

func TestIncidentAttachWiring(t *testing.T) {
	promo := fnBody(t, "evaluator.go", "func (e *Evaluator) promoteStrongAnomalies(")
	gate := strings.Index(promo, "if cfg.AttachesToIncident() {")
	call := strings.Index(promo, "e.store.AttachProblemToIncident(ctx, p)")
	isNew := strings.Index(promo, "if isNew {")
	if gate < 0 || call < 0 || isNew < 0 || !(isNew < gate && gate < call) {
		t.Fatalf("terfi: açılışta, vidayla kapılı AttachProblemToIncident bekleniyordu (isNew=%d gate=%d call=%d)", isNew, gate, call)
	}
	for _, c := range []struct{ path, sig, want string }{
		{"fatal_exception.go", "func (e *Evaluator) reconcileFatalException(", "e.joinOpenIncident(ctx, p, []string{f.Service})"},
		{"shared_exception.go", "func (e *Evaluator) reconcileSharedBurst(", "e.joinOpenIncident(ctx, p, b.Services)"},
	} {
		body := fnBody(t, c.path, c.sig)
		if !strings.Contains(body, c.want) {
			t.Errorf("%s: %q yok", c.path, c.want)
		}
		if strings.Contains(body, "AttachProblemToIncident(") {
			t.Errorf("%s: tekil exception kendi incident'ını AÇMAMALI (AttachProblemToIncident)", c.path)
		}
	}
	join := fnBody(t, "fatal_exception.go", "func (e *Evaluator) joinOpenIncident(")
	if !strings.Contains(join, "SingleGroupJoinsIncident()") || !strings.Contains(join, "e.store.JoinOpenIncident(") {
		t.Fatal("joinOpenIncident vidayla kapılı JoinOpenIncident çağırmalı")
	}
}
