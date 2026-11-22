package sre

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// v0.10.2453 — dependency_path faz-B'de EK kanıt: yük + künye; bulgular, adım
// durumu ve skor girdisi (DepsFacts) değişmez.
func TestDepsStepPathIsEvidenceOnly(t *testing.T) {
	req := depsRequest{Service: "checkout", From: start, To: t0, At: start, WindowSec: 900}
	base := depsResult{
		edges:   []chstore.ServiceEdge{{Source: "checkout", Target: "payments-db"}},
		changes: []chstore.ChangedService{{Service: "payments-db", Direction: chstore.ChangeWorse, Reasons: []string{"p99 +180%"}}},
	}
	st0, f0 := depsStep(req, base)
	facts0 := depsFactsOf(req, base)

	withPath := base
	withPath.path = &mcptools.DepPathSummary{Walked: 3, Degraded: 1, Source: &mcptools.DepPathSource{Path: []string{"checkout", "payments-api"}}}
	st1, f1 := depsStep(req, withPath)
	if st1.State != st0.State || !reflect.DeepEqual(f0, f1) || !reflect.DeepEqual(facts0, depsFactsOf(req, withPath)) {
		t.Fatalf("yol özeti durumu / bulguyu / skor girdisini değiştirdi: %+v vs %+v", st0, st1)
	}
	if !strings.Contains(string(st1.Payload), `"path":{"walked":3`) || !strings.Contains(st1.Summary, "olası kaynak kenar") {
		t.Errorf("yük / özet: %s | %s", st1.Payload, st1.Summary)
	}
	ev := depsEvidence(req, withPath, make([]int64, 5))
	if last := ev[len(ev)-1]; last.Tool != mcptools.DependencyPathName || last.Rows == nil || *last.Rows != 3 {
		t.Errorf("künye %+v", last)
	}
	// Okuma hatası: künye var, adım durumu aynı.
	failed := base
	failed.pathErr = errors.New("timeout")
	if st2, _ := depsStep(req, failed); st2.State != st0.State {
		t.Errorf("yol hatası adımı bozdu: %v", st2.State)
	}
	if ev := depsEvidence(req, base, make([]int64, 5)); ev[len(ev)-1].Tool == mcptools.DependencyPathName {
		t.Error("okuyucusuz koşuda künye olmamalı")
	}
}
