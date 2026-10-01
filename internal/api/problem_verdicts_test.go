package api

// v0.10.1015 — Problems sekmesinde öğretme ucu: girdi doğrulaması, rol kapısı
// (viewer yazamaz), yazılanın cevaba yansıması ve kayıt pini.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNormalizeProblemVerdictInput(t *testing.T) {
	now := time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	v, clear, msg := normalizeProblemVerdictInput(problemVerdictInput{
		Signature: "  p:rule-1|payments-api ", Verdict: "noise", Label: " Error rate > 5% ", Kind: "problem", Service: "payments-api",
	}, "op@example.test", now)
	if msg != "" || clear || v.Signature != "p:rule-1|payments-api" || v.Verdict != "noise" || v.Label != "Error rate > 5%" || v.By != "op@example.test" || v.At != now.UnixNano() {
		t.Fatalf("geçerli girdi: %+v clear=%v msg=%q", v, clear, msg)
	}
	// Boş karar = kaldır.
	if v, clear, msg := normalizeProblemVerdictInput(problemVerdictInput{Signature: "e:abc"}, "x", now); msg != "" || !clear || v.Signature != "e:abc" {
		t.Fatalf("kaldırma: %+v clear=%v msg=%q", v, clear, msg)
	}
	// Uzun başlık rune sınırında kesilir (UTF-8 bölünmez).
	long := strings.Repeat("ş", 500)
	if v, _, _ := normalizeProblemVerdictInput(problemVerdictInput{Signature: "e:abc", Verdict: "real", Label: long}, "x", now); len([]rune(v.Label)) != problemVerdictLabelMax {
		t.Errorf("başlık tavanı: %d rune", len([]rune(v.Label)))
	}
	for name, in := range map[string]problemVerdictInput{
		"imza yok":         {Verdict: "real"},
		"öneksiz imza":     {Signature: "rule-1|svc", Verdict: "real"},
		"bilinmeyen karar": {Signature: "e:abc", Verdict: "anomaly"},
	} {
		if _, _, msg := normalizeProblemVerdictInput(in, "x", now); msg == "" {
			t.Errorf("%s: reddedilmeli", name)
		}
	}
}

func TestReconcileProblemVerdicts(t *testing.T) {
	list := []chstore.ProblemVerdict{{Signature: "e:old", Verdict: "real"}, {Signature: "p:r|s", Verdict: "real"}}
	// Okuma yazımı henüz görmedi (eski karar duruyor) → yazılan kazanır, başa gelir.
	got := reconcileProblemVerdicts(list, chstore.ProblemVerdict{Signature: "p:r|s", Verdict: "noise"}, false)
	if len(got) != 2 || got[0].Signature != "p:r|s" || got[0].Verdict != "noise" || got[1].Signature != "e:old" {
		t.Fatalf("yazım uzlaşması: %+v", got)
	}
	// Kaldırma: okuma hâlâ gösterse de cevapta yok.
	got = reconcileProblemVerdicts(list, chstore.ProblemVerdict{Signature: "p:r|s"}, true)
	if len(got) != 1 || got[0].Signature != "e:old" {
		t.Fatalf("kaldırma uzlaşması: %+v", got)
	}
}

func TestProblemVerdictRouteGates(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	mux := http.NewServeMux()
	s.registerProblemVerdictRoutes(mux)
	put := func(role, body string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, asRole(httptest.NewRequest("PUT", "/api/problem-verdicts", strings.NewReader(body)), role))
		return w.Code
	}
	// Viewer yazamaz (kararları GÖRÜR, değiştiremez).
	if code := put(auth.RoleViewer, `{"signature":"e:abc","verdict":"noise"}`); code != http.StatusForbidden {
		t.Errorf("viewer PUT → %d, 403 beklenir", code)
	}
	// Editor: geçersiz girdi depoya gitmeden 400.
	for name, body := range map[string]string{
		"bozuk JSON":       `{`,
		"öneksiz imza":     `{"signature":"abc","verdict":"real"}`,
		"bilinmeyen karar": `{"signature":"e:abc","verdict":"maybe"}`,
	} {
		if code := put(auth.RoleEditor, body); code != http.StatusBadRequest {
			t.Errorf("%s → %d, 400 beklenir", name, code)
		}
	}
	// v0.10.1016 — politika yalnız admin; eksik alan "kapat" diye okunmaz.
	putPolicy := func(role, body string) int {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, asRole(httptest.NewRequest("PUT", "/api/problem-verdicts/policy", strings.NewReader(body)), role))
		return w.Code
	}
	for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
		if code := putPolicy(role, `{"muteNotifications":true}`); code != http.StatusForbidden {
			t.Errorf("%s politika PUT → %d, 403 beklenir", role, code)
		}
	}
	for name, body := range map[string]string{"bozuk JSON": `{`, "eksik alan": `{}`} {
		if code := putPolicy(auth.RoleAdmin, body); code != http.StatusBadRequest {
			t.Errorf("politika %s → %d, 400 beklenir", name, code)
		}
	}
	src, err := os.ReadFile("problem_verdicts.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{
		`registerRoutesExtra("problem-verdicts"`,
		`auth.RequireAnyRole(editorRoles, s.putProblemVerdict)`,
		`s.serveCached(w, r, problemVerdictCacheKey, 10*time.Second`,
		`s.cacheInvalidatePrefix(ctx, problemVerdictCachePfx)`,
		`s.audit(r, "problem.verdict", "problem", v.Signature`,
		`auth.RequireRole(auth.RoleAdmin, s.putProblemVerdictPolicy)`,
		`s.audit(r, "problem.verdict.policy", "settings", chstore.ProblemVerdictPolicyKey`,
	} {
		if !strings.Contains(string(src), w) {
			t.Errorf("problem_verdicts.go %q taşımalı", w)
		}
	}
}
