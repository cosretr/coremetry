package api

// v0.10.2135 (CoSRE router boşlukları D2, docs/cosre/router-gaps-audit.md A1)
// — "Açık problemlerin kök nedeni?" çipi. Prod (7 gün): 6 kez router
// boşluğu; çip hipotez vaat ediyor, hiçbir rota filo geneli hipotez
// vermiyordu. Pinler: (1) çip metni (drawerChat.ts FOLLOWUPS) birebir bu
// rotaya gider; (2) yükseltme yalnız v2 + servissiz + problem∧neden;
// (3) hipotez çerçevesi sayımı ve "uydurma yok" cümlesi; (4) dağıtım bağı.

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var prcServices = []string{"svc-a", "svc-b"}

func prcRoute(q, ctxService string, v2 bool) guidedRoute {
	r := routeGuidedIntentExt(q, prcServices, nil, nil, ctxService, false, nil)
	return promoteProblemsRootCause(r, normalizeGuidedMsg(q), v2)
}

// Çipin metni frontend'den OKUNUR: metin değişirse bu test rota bağını sorar.
func TestProblemsRootCauseChipPinned(t *testing.T) {
	b, err := os.ReadFile("../../frontend/src/components/ai/drawerChat.ts")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?s)export const FOLLOWUPS = \[\s*'([^']+)'`).FindStringSubmatch(string(b))
	if m == nil {
		t.Fatal("drawerChat.ts FOLLOWUPS ilk çipi bulunamadı")
	}
	chip := m[1]
	if chip != "Açık problemlerin kök nedeni?" {
		t.Fatalf("çip metni değişti: %q — rota bağını (promoteProblemsRootCause) yeniden doğrula", chip)
	}
	r := prcRoute(chip, "", true)
	if r.Intent != guidedProblems || !r.ProblemsRootCause || guidedRouteLabel(r) != guidedProblemsRootCauseRoute {
		t.Errorf("v2 çip → %q (flag=%v), want %s", r.Intent, r.ProblemsRootCause, guidedProblemsRootCauseRoute)
	}
	if v1 := prcRoute(chip, "", false); v1.ProblemsRootCause || guidedRouteLabel(v1) != string(guidedProblems) {
		t.Errorf("v1 çip değişmemeli: %+v", v1.Intent)
	}
}

func TestPromoteProblemsRootCause(t *testing.T) {
	for _, c := range []struct {
		q, ctxSvc string
		v2        bool
		want      string
	}{
		{"Açık problemlerin kök nedeni?", "", true, guidedProblemsRootCauseRoute},
		{"açık problemlerin sebebi ne", "", true, guidedProblemsRootCauseRoute},
		{"problemlerin nedeni nedir?", "", true, guidedProblemsRootCauseRoute},
		// tutmaması gerekenler
		{"Açık problemlerin kök nedeni?", "", false, string(guidedProblems)}, // v1
		{"açık problemler neler?", "", true, string(guidedProblems)},         // neden yok
		{"Açık problemlerin kök nedeni?", "svc-a", true, string(guidedRootCause)},
		{"svc-a problemlerinin kök nedeni ne?", "", true, string(guidedRootCause)},
	} {
		if got := guidedRouteLabel(prcRoute(c.q, c.ctxSvc, c.v2)); got != c.want {
			t.Errorf("%q ctx=%q v2=%v → %q, want %q", c.q, c.ctxSvc, c.v2, got, c.want)
		}
	}
	// Problem rotası dışındaki rotalar ve servisli problems dokunulmaz.
	for _, r := range []guidedRoute{{Intent: guidedRootCause}, {Intent: guidedProblems, Service: "svc-a"}} {
		if promoteProblemsRootCause(r, normalizeGuidedMsg("problemlerin kök nedeni"), true).ProblemsRootCause {
			t.Errorf("%+v yükselmemeli", r.Intent)
		}
	}
}

func TestProblemsRootCauseFrame(t *testing.T) {
	hyp := &chstore.RootCauseSummary{TopSuspect: "deploy svc-a"}
	cases := []struct {
		name     string
		probs    []chstore.Problem
		headHas  string
		tail     string
		noFabric bool
	}{
		{"hipotez yok", []chstore.Problem{{ID: "p1"}, {ID: "p2"}}, "HİÇBİRİNDE kayıtlı kök-neden hipotezi YOK",
			"\n\nKök neden hipotezi yok: listelenen 2 açık problemin hiçbiri için kayıtlı hipotez bulunmuyor.", true},
		{"kısmi", []chstore.Problem{{ID: "p1", RootCause: hyp}, {ID: "p2"}, {ID: "p3"}}, "3 açık problemin 1 tanesinde",
			"\n\nKök neden hipotezi: listelenen 3 açık problemin 1 tanesinde var, 2 tanesinde yok.", true},
		{"hepsi", []chstore.Problem{{ID: "p1", RootCause: hyp}}, "1 açık problemin 1 tanesinde", "", true},
	}
	for _, c := range cases {
		h, tail := problemsRootCauseFrame(c.probs)
		if !strings.Contains(h, c.headHas) || tail != c.tail {
			t.Errorf("%s: header %q tail %q", c.name, h, tail)
		}
		if c.noFabric && !strings.Contains(h, "UYDURMA") {
			t.Errorf("%s: başlık uydurma yasağını taşımalı", c.name)
		}
	}
	if got := problemsNoneRootCauseTR(""); got != "Şu an açık problem yok; dolayısıyla kök neden hipotezi de yok." {
		t.Errorf("boş cevap %q", got)
	}
	if got := problemsNoneRootCauseTR("prod"); !strings.Contains(got, "(ortam: prod)") {
		t.Errorf("ortamlı boş cevap %q", got)
	}
}

// Dağıtım bağı: bayrak runGuidedRoute'ta hipotez demetine gider, router'ın
// sonucu yükseltmeden geçer, /ai rotası etiketten okunur.
func TestProblemsRootCauseWiring(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoCommentsAPI(string(b))
	for _, want := range []string{
		"route = promoteProblemsRootCause(route, norm, v2)",
		"v2 := cosreV2Marked(ctx)",
		"if route.ProblemsRootCause {",
		"evidence, sources, facts, err = s.guidedProblemsRootCauseBundle(ctx, emit, &route, to)",
		"chatExchangeFrom(ctx).noteRoute(guidedRouteLabel(route))",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("copilot_guided.go bağı eksik: %q", want)
		}
	}
}
