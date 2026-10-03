package evaluator

// slo_burn_gate_test.go — v0.10.1081 (operatör: "SLO burn rate problem
// olmasın, çıkar. SLO ile ilgili beklentim yok.").
//
// Pinlenen sözleşmeler:
//  1. Bayrak varsayılan KAPALI (nil = kapalı; problem_priority blobu).
//  2. Kapalıyken burn critical eşiğini aşsa da Problem / incident AÇILMAZ ve
//     burn hiç ölçülmez (CH sorgusu yok).
//  3. Açıkken bugünkü davranış birebir: critical + warning ihlalinde iki
//     problem açılır, incident'a bağlanır.
//  4. Kapalı + ayar yayınlanmış: açık slo:* problemleri dürüst gerekçeyle
//     kapanır, başka kurala dokunulmaz. Kapalı + ayar hiç okunamamış: açıklara
//     dokunulmaz (tahminle tek yönlü eylem yok).

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// fakeSLOStore — sloBurnStore'un bellek içi eşi.
type fakeSLOStore struct {
	slos     []chstore.SLO
	burn     float64 // her pencere için aynı hız
	total    uint64
	problems map[string]chstore.Problem
	measured int
	attached []string
}

func (f *fakeSLOStore) ListSLOs(context.Context) ([]chstore.SLO, error) { return f.slos, nil }

func (f *fakeSLOStore) ComputeSLOBurnRate(context.Context, chstore.SLO, time.Duration) (float64, uint64, error) {
	f.measured++
	return f.burn, f.total, nil
}

func (f *fakeSLOStore) OpenProblemsSnapshot(context.Context) (*chstore.OpenProblems, error) {
	var open []chstore.Problem
	for _, p := range f.problems {
		if p.Status == "open" || p.Status == "acknowledged" {
			open = append(open, p)
		}
	}
	return chstore.NewOpenProblems(open), nil
}

func (f *fakeSLOStore) UpsertProblem(_ context.Context, p chstore.Problem) error {
	f.problems[p.ID] = p
	return nil
}

func (f *fakeSLOStore) AttachProblemToIncident(_ context.Context, p chstore.Problem) (*chstore.Incident, error) {
	f.attached = append(f.attached, p.ID)
	return &chstore.Incident{ID: "inc-" + p.ID}, nil
}

// burningFixture — sentetik SLO, burn her iki politikanın da (critical 14.4×
// / warning 6×) üstünde, trafik tabanı geçilmiş.
func burningFixture(problems ...chstore.Problem) *fakeSLOStore {
	f := &fakeSLOStore{
		slos:     []chstore.SLO{{ID: "slo-a", Name: "checkout availability", Service: "svc-checkout", Target: 0.999, WindowDays: 30}},
		burn:     40,
		total:    5000,
		problems: map[string]chstore.Problem{},
	}
	for _, p := range problems {
		f.problems[p.ID] = p
	}
	return f
}

func TestSLOBurnProblemsDefaultOff(t *testing.T) {
	if chstore.DefaultProblemPriority().SLOBurnProblemsEnabled() {
		t.Fatal("varsayılan problem_priority SLO burn problemi üretmemeli (nil = kapalı)")
	}
	if (chstore.ProblemPriorityConfig{}).SLOBurnProblemsEnabled() {
		t.Fatal("alanı yazılmamış blob kapalı okunmalı")
	}
	off, on := false, true
	if (chstore.ProblemPriorityConfig{SLOBurnProblems: &off}).SLOBurnProblemsEnabled() {
		t.Fatal("açık false kapalı olmalı")
	}
	if !(chstore.ProblemPriorityConfig{SLOBurnProblems: &on}).SLOBurnProblemsEnabled() {
		t.Fatal("açık true açık olmalı")
	}
	// Normalize bayrağı taşır (PUT → Save → geri okuma zinciri düşürmesin).
	if !chstore.NormalizeProblemPriority(chstore.ProblemPriorityConfig{SLOBurnProblems: &on}).SLOBurnProblemsEnabled() {
		t.Fatal("NormalizeProblemPriority bayrağı düşürdü")
	}
}

func TestSLOBurnDisabledOpensNothingEvenAboveCritical(t *testing.T) {
	f := burningFixture()
	e := &Evaluator{}
	e.evaluateSLOsWith(context.Background(), f, false, true)
	if len(f.problems) != 0 {
		t.Fatalf("kapalıyken %d problem açıldı, 0 bekleniyordu", len(f.problems))
	}
	if len(f.attached) != 0 {
		t.Fatalf("kapalıyken incident'a bağlandı: %v", f.attached)
	}
	if f.measured != 0 {
		t.Fatalf("kapalıyken burn %d kez ölçüldü — CH'ye hiç gitmemeli", f.measured)
	}
	// Ayar hiç yayınlanmamış süreçte de aynı: açılış yok.
	e.evaluateSLOsWith(context.Background(), f, false, false)
	if len(f.problems) != 0 {
		t.Fatalf("yayınlanmamış ayarda %d problem açıldı", len(f.problems))
	}
}

func TestSLOBurnEnabledEmitsAsBefore(t *testing.T) {
	f := burningFixture()
	e := &Evaluator{}
	e.evaluateSLOsWith(context.Background(), f, true, true)
	got := map[string]chstore.Problem{}
	for _, p := range f.problems {
		got[p.RuleID] = p
	}
	for _, sev := range []string{"critical", "warning"} {
		p, ok := got["slo:slo-a:"+sev]
		if !ok {
			t.Fatalf("açıkken slo:slo-a:%s açılmadı (%v)", sev, got)
		}
		if p.Severity != sev || p.Status != "open" || p.Service != "svc-checkout" {
			t.Errorf("%s: severity=%q status=%q service=%q", sev, p.Severity, p.Status, p.Service)
		}
		if !strings.HasPrefix(p.RuleName, "SLO burn-rate "+sev) {
			t.Errorf("%s: RuleName %q", sev, p.RuleName)
		}
	}
	if len(f.attached) != 2 {
		t.Fatalf("iki problem de incident'a bağlanmalı, %d bağlandı", len(f.attached))
	}
	if e.opened.Load() != 2 {
		t.Fatalf("kalp atışı sayacı %d, 2 bekleniyordu", e.opened.Load())
	}
	// İkinci tik: açıklar tazelenir, yenisi açılmaz.
	e.evaluateSLOsWith(context.Background(), f, true, true)
	if len(f.problems) != 2 {
		t.Fatalf("ikinci tikte %d problem — tazeleme yeni açmamalı", len(f.problems))
	}
}

func TestSLOBurnDisabledResolvesOpenOnlyWhenPublished(t *testing.T) {
	open := []chstore.Problem{
		{ID: "s1", RuleID: "slo:slo-a:critical", RuleName: "SLO burn-rate critical — checkout", Service: "svc-checkout", Status: "open", Value: 20, Threshold: 14.4, Description: "Burn rate above critical threshold"},
		{ID: "s2", RuleID: "slo:slo-a:warning", Service: "svc-checkout", Status: "acknowledged", Value: 7, Threshold: 6},
		{ID: "u1", RuleID: "user-checkout-p99", Service: "svc-checkout", Status: "open"},
	}

	// Yayınlanmamış ayar: dokunma.
	f := burningFixture(open...)
	e := &Evaluator{}
	e.evaluateSLOsWith(context.Background(), f, false, false)
	for _, id := range []string{"s1", "s2", "u1"} {
		if f.problems[id].Status == "resolved" {
			t.Fatalf("ayar okunmamışken %s kapatıldı", id)
		}
	}

	// Yayınlanmış "kapalı": slo:* kapanır, Value ezilmez, gerekçe dürüst.
	e.evaluateSLOsWith(context.Background(), f, false, true)
	for _, id := range []string{"s1", "s2"} {
		p := f.problems[id]
		if p.Status != "resolved" || p.ResolvedAt == nil {
			t.Fatalf("%s kapanmadı: %q", id, p.Status)
		}
		if !strings.Contains(p.Description, "auto-resolved: "+sloBurnDisabledReason) {
			t.Errorf("%s gerekçe yok: %q", id, p.Description)
		}
	}
	if f.problems["s1"].Value != 20 {
		t.Errorf("Value ezildi: %v", f.problems["s1"].Value)
	}
	if f.problems["u1"].Status != "open" {
		t.Error("kullanıcı kuralının problemi kapatıldı — yalnız slo:* kapanmalı")
	}
	if f.measured != 0 {
		t.Errorf("kapalıyken burn ölçüldü (%d)", f.measured)
	}
}
