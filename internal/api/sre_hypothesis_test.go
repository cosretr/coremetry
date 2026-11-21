package api

// v0.10.1288 — AI SRE Faz 2: hipotez kartlarının "Ele / Onayla" ucu (rol
// kapısı, audit, skor değişmez, koşan kayıtta 409), "Doğrula" (POST
// investigation {kind:"verify"}), sohbetin tık-only sre_hypothesis aksiyonu.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sre"
)

func seedSREInvestigation(t *testing.T, st *sreFakeStore, state string) []sre.Hypothesis {
	t.Helper()
	hs := []sre.Hypothesis{
		{Kind: sre.HypDeploy, Label: "Deploy", Category: "deploy", Score: 0.78, Band: sre.BandHigh, Prior: 0.2,
			Checks: []sre.HypothesisCheck{{Name: "deploy_near", Outcome: 1, Weight: 0.35, Reason: "12 dk önce rollout"}}},
		{Kind: sre.HypDependency, Label: "Bağımlılık arızası", Category: "dependency", Score: 0.45, Band: sre.BandMedium, Prior: 0.15},
	}
	body, err := sre.Encode(sre.Investigation{ProblemID: "p1", Service: "pay", Kind: sre.KindAuto, State: state, Run: 1, EnqueuedAt: 1, Hypotheses: hs,
		Steps: []sre.Step{{Name: "sweep", State: sre.StepOK}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutSREInvestigation(context.Background(), "p1", state, body, 1); err != nil {
		t.Fatal(err)
	}
	return hs
}

func TestHypothesisMarkRoles(t *testing.T) {
	s, mux, st := newSRETestEnv(t)
	s.registerSREHypothesisRoutes(mux)
	hs := seedSREInvestigation(t, st, sre.StateDone)
	path := "/api/problems/p1/investigation/hypotheses/deploy"

	// viewer 403 — kayıt değişmez.
	if w := sreDo(mux, "POST", path, `{"state":"dismissed"}`, auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d", w.Code)
	}
	inv, _, _ := sre.Load(context.Background(), st, "p1")
	if inv.Hypotheses[0].HumanState != nil {
		t.Fatal("reddedilen istek işaret yazmamalı")
	}
	// Geçersiz durum / tür 400.
	for _, c := range []struct{ path, body string }{
		{path, `{"state":"maybe"}`},
		{"/api/problems/p1/investigation/hypotheses/nope", `{"state":"dismissed"}`},
	} {
		if w := sreDo(mux, "POST", c.path, c.body, auth.RoleEditor); w.Code != http.StatusBadRequest {
			t.Fatalf("%s %s = %d", c.path, c.body, w.Code)
		}
	}
	// editor 200 + audit + skor değişmez.
	w := sreDo(mux, "POST", path, `{"state":"dismissed","note":"deploy geri alındı, sorun sürüyor"}`, auth.RoleEditor)
	if w.Code != 200 {
		t.Fatalf("editor = %d %s", w.Code, w.Body)
	}
	var got sre.Hypothesis
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Score != hs[0].Score || got.Band != hs[0].Band || got.HumanState == nil || got.HumanState.State != sre.MarkDismissed ||
		got.HumanState.By != "editor@example.test" || got.HumanState.Note == "" {
		t.Fatalf("cevap: %+v", got)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "sre.hypothesis.mark" || a.TargetID != "p1" || !strings.Contains(a.Details, `"state":"dismissed"`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("işaret audit yazmalı")
	}
	inv, _, _ = sre.Load(context.Background(), st, "p1")
	if inv.Hypotheses[0].Score != 0.78 || len(inv.Hypotheses[0].Checks) != 1 || inv.Hypotheses[0].HumanState == nil {
		t.Fatalf("kayıt: %+v", inv.Hypotheses[0])
	}
	// admin onaylar.
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation/hypotheses/dependency", `{"state":"confirmed"}`, auth.RoleAdmin); w.Code != 200 {
		t.Fatalf("admin = %d", w.Code)
	}
	<-s.auditQ
	// Karar kaldırma: viewer 403; editor 200 + audit sre.hypothesis.unmark.
	if w := sreDo(mux, "DELETE", "/api/problems/p1/investigation/hypotheses/dependency", "", auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer DELETE = %d", w.Code)
	}
	if w := sreDo(mux, "DELETE", "/api/problems/p1/investigation/hypotheses/dependency", "", auth.RoleEditor); w.Code != 200 {
		t.Fatalf("editor DELETE = %d %s", w.Code, w.Body)
	}
	if a := <-s.auditQ; a.Action != "sre.hypothesis.unmark" || a.TargetID != "p1" {
		t.Fatalf("unmark audit = %+v", a)
	}
	inv, _, _ = sre.Load(context.Background(), st, "p1")
	if inv.Hypotheses[1].HumanState != nil || inv.Hypotheses[0].HumanState == nil {
		t.Fatalf("kaldırma yalnız o kartı etkilemeli: %+v", inv.Hypotheses)
	}
	// Koşan kayıt 409, kayıt yok 404.
	seedSREInvestigation(t, st, sre.StateRunning)
	if w := sreDo(mux, "POST", path, `{"state":"confirmed"}`, auth.RoleEditor); w.Code != http.StatusConflict {
		t.Fatalf("koşan kayıt = %d", w.Code)
	}
	if w := sreDo(mux, "POST", "/api/problems/zz/investigation/hypotheses/deploy", `{"state":"confirmed"}`, auth.RoleEditor); w.Code != http.StatusNotFound {
		t.Fatalf("kayıt yok = %d", w.Code)
	}
	if len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek audit yazmamalı")
	}
}

// TestVerifyEnqueue — "Doğrula": editor POST {kind:"verify", hypothesis} →
// verify kaydı (önceki adımlar taşınır) + audit; geçersiz tür 400; viewer 403.
func TestVerifyEnqueue(t *testing.T) {
	s, mux, st := newSRETestEnv(t)
	seedSREInvestigation(t, st, sre.StateDone)
	if w := sreDo(mux, "POST", "/api/problems/p1/investigation", `{"kind":"verify","hypothesis":"deploy"}`, auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Fatalf("viewer = %d", w.Code)
	}
	for _, body := range []string{`{"kind":"verify","hypothesis":"nope"}`, `{"kind":"replay"}`, `{`} {
		if w := sreDo(mux, "POST", "/api/problems/p1/investigation", body, auth.RoleEditor); w.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, w.Code)
		}
	}
	w := sreDo(mux, "POST", "/api/problems/p1/investigation", `{"kind":"verify","hypothesis":"saturation"}`, auth.RoleEditor)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"created":true`) {
		t.Fatalf("verify = %d %s", w.Code, w.Body)
	}
	inv, _, _ := sre.Load(context.Background(), st, "p1")
	if inv.Kind != sre.KindVerify || inv.Hypothesis != sre.HypSaturation || inv.State != sre.StateQueued || len(inv.Steps) != 1 || inv.Run != 2 {
		t.Fatalf("verify kaydı: %+v", inv)
	}
	select {
	case a := <-s.auditQ:
		if a.Action != "problem.investigation.enqueue" || !strings.Contains(a.Details, `"hypothesis":"saturation"`) {
			t.Fatalf("audit = %+v", a)
		}
	default:
		t.Fatal("verify audit yazmalı")
	}
}

func TestSREHypothesisPick(t *testing.T) {
	content := func(id, band, op string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "displayId": "P-7", "investigation": map[string]any{"hypotheses": []map[string]any{
			{"kind": "deploy", "label": "Deploy", "band": band, "operator": op},
		}}})
		return string(b)
	}
	for _, c := range []struct {
		name, problem, tool, content string
		ok                           bool
	}{
		{"get_problem + yüksek", "p1", "get_problem", content("p1", sre.BandHigh, ""), true},
		{"görünen kimlik", "P-7", "get_problem", content("p1", sre.BandMedium, ""), true},
		{"başka problem", "p2", "get_problem", content("p1", sre.BandHigh, ""), false},
		{"düşük bant", "p1", "get_problem", content("p1", sre.BandLow, ""), false},
		{"operatör işaretlemiş", "p1", "get_problem", content("p1", sre.BandHigh, "dismissed"), false},
		{"başka araç", "p1", "list_problems", content("p1", sre.BandHigh, ""), false},
		{"hipotez yok", "p1", "get_problem", `{"id":"p1","investigation":{"hypotheses":[]}}`, false},
		{"bozuk JSON", "p1", "get_problem", `{`, false},
	} {
		kind, label, ok := sreHypothesisPick(c.problem, c.tool, c.content)
		if ok != c.ok || (ok && (kind != sre.HypDeploy || label != "Deploy")) {
			t.Errorf("%s: %q %q %v", c.name, kind, label, ok)
		}
	}
	s := &Server{}
	ctx := auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u", Role: auth.RoleViewer})
	if _, ok := s.sreHypothesisAction(ctx, "p1", "get_problem", content("p1", sre.BandHigh, "")); ok {
		t.Fatal("viewer'a doğrula aksiyonu önerilmez")
	}
	ctx = auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u", Role: auth.RoleEditor})
	act, ok := s.sreHypothesisAction(ctx, "p1", "get_problem", content("p1", sre.BandHigh, ""))
	if !ok || act["kind"] != "sre_hypothesis" || act["hypothesis"] != "deploy" || act["label"] != "Deploy hipotezini doğrula" || act["problemId"] != "p1" {
		t.Fatalf("aksiyon: %+v", act)
	}
}

// Aksiyon yalnız tool döngüsünde, başarılı sonuçtan ve sayfa bağlamının
// problem kimliğiyle; tur başına bir kez (model metni yolu yok).
func TestSREHypothesisActionEmittedFromToolResult(t *testing.T) {
	src, _ := os.ReadFile("copilot_chat.go")
	b := string(src)
	if strings.Count(b, "s.sreHypothesisAction(ctx, pageCtx.ProblemID, tc.Name, tr.Content)") != 1 ||
		!strings.Contains(b, "if !tr.IsError && !hypOffered && pageCtx != nil") {
		t.Fatal("doğrula aksiyonu tool döngüsünde tek noktadan, başarılı tool sonucundan türemeli")
	}
}
