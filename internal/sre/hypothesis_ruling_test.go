package sre

import (
	"context"
	"strings"
	"testing"
)

func ruled(h Hypothesis, state string) Hypothesis {
	h.HumanState = &HumanState{State: state, By: "op@example.test", At: 1}
	return h
}

// Ekip kararı: doğrulanan sonuçtur, reddedilen asla en olası neden olmaz;
// otomatik durum / skor / saklı sıra değişmez.
func TestRulingOrderAndGate(t *testing.T) {
	dep := hyp(HypDeploy, 0.8, chk("a", 1, 0.35, "deploys"), chk("b", 1, 0.25, "sweep"))
	dep.Label = "Deploy etiketi"
	db := hyp(HypDBSlow, 0.3, chk("c", 1, 0.2, "events"))
	db.Label = "DB etiketi"
	cfg := hyp(HypConfig, 0.1)
	hs := assignHypothesisStates([]Hypothesis{dep, db, cfg})

	// Reddedilen supported top → kapı onu seçmez; başka supported yok → yetersiz.
	refuted := []Hypothesis{ruled(hs[0], MarkDismissed), hs[1], hs[2]}
	if g := evaluateCoverage(refuted, nil, nil, GateShadow); g.Verdict != GateInsufficient || g.Top != "" {
		t.Fatalf("reddedilen top seçildi: %+v", g)
	}
	if o := rulingOrder(refuted); o[len(o)-1].Kind != HypDeploy || o[0].Kind != HypDBSlow {
		t.Fatalf("sıra: %v", o)
	}
	if refuted[0].State != HypStateSupported {
		t.Fatal("otomatik durum karardan etkilenmemeli")
	}
	// Doğrulanan (düşük skorlu) → yeterli, top = o, Ruling confirmed.
	confirmed := []Hypothesis{hs[0], ruled(hs[1], MarkConfirmed), hs[2]}
	g := evaluateCoverage(confirmed, nil, nil, GateShadow)
	if g.Verdict != GateSufficient || g.Top != HypDBSlow || g.Ruling != MarkConfirmed {
		t.Fatalf("doğrulanan kapı: %+v", g)
	}
	if o := rulingOrder(confirmed); o[0].Kind != HypDBSlow || confirmed[0].Kind != HypDeploy {
		t.Fatal("görünüm sırası doğrulananı öne almalı, girdi değişmemeli")
	}
	// Anlatım: doğrulanan söylenir, reddedilen asla en olası değil.
	txt := renderHypothesesTR(confirmed)
	if !strings.HasPrefix(txt, "EKİBİN DOĞRULADIĞI NEDEN: DB etiketi") || !strings.Contains(txt, "ekip doğruladı") {
		t.Fatalf("anlatım: %s", txt)
	}
	if first := strings.SplitN(renderHypothesesTR(refuted), "\n", 2)[0]; strings.Contains(first, "Deploy etiketi") {
		t.Fatalf("reddedilen en olası söylendi: %s", first)
	}
	// Sohbet özeti: ilk satır doğrulanan.
	if s := summaryHypotheses(confirmed); s[0].Kind != HypDBSlow || s[0].Operator != MarkConfirmed {
		t.Fatalf("sohbet özeti: %+v", s)
	}
}

// Bildirim: doğrulanan "ekip doğruladı" ile söylenir (kayıttaki eski kapı
// kararı ne derse desin); reddedilen hiçbir zaman.
func TestDigestRespectsRulings(t *testing.T) {
	top := Hypothesis{Kind: HypDeploy, Label: "Deploy", Score: 0.8, State: HypStateSupported,
		StateRefs: []string{"deploys", "sweep"}, Checks: []HypothesisCheck{{Name: "deploy_near", Outcome: 1, Weight: 0.35, EvidenceRef: "deploys"}}}
	inv := digestInv(ruled(top, MarkDismissed))
	inv.Coverage = &CoverageGate{Mode: GateShadow, Verdict: GateSufficient, Top: HypDeploy, Sources: []string{"deploys", "sweep"}}
	if d := Digest(inv); d.Sufficient || d.TopKind == HypDeploy {
		t.Fatalf("reddedilen bildirimde söylendi: %+v", d)
	}
	inv = digestInv(top)
	inv.Hypotheses[1] = ruled(inv.Hypotheses[1], MarkConfirmed)
	inv.Coverage = &CoverageGate{Mode: GateShadow, Verdict: GateSufficient, Top: HypDeploy}
	d := Digest(inv)
	if !d.Sufficient || !d.TeamConfirmed || d.TopKind != HypConfig {
		t.Fatalf("doğrulanan bildirimde: %+v", d)
	}
}

// Karar kayıtta yazılır, kapı kararı yenilenir, kaldırılabilir; yeniden
// koşuda taşınır.
func TestRulingPersistsAndClears(t *testing.T) {
	st := newFakeStore()
	hs := assignHypothesisStates(Score(richEvidence()))
	g := evaluateCoverage(hs, nil, nil, GateShadow)
	inv := Investigation{ProblemID: "p1", Service: "checkout", State: StateDone, EnqueuedAt: 1, Hypotheses: hs, Coverage: &g}
	if err := save(context.Background(), st, inv); err != nil {
		t.Fatal(err)
	}
	last := hs[len(hs)-1].Kind
	if _, err := ApplyHypothesisMark(context.Background(), st, "p1", MarkRequest{Kind: last, State: MarkConfirmed, By: "op@x"}); err != nil {
		t.Fatal(err)
	}
	got := st.get(t, "p1")
	if got.Coverage == nil || got.Coverage.Top != last || got.Coverage.Ruling != MarkConfirmed || got.Coverage.Mode != GateShadow {
		t.Fatalf("kapı yenilenmedi: %+v", got.Coverage)
	}
	if h := hypOf(t, carryHuman(Score(richEvidence()), got.Hypotheses), last); h.HumanState == nil || h.HumanState.State != MarkConfirmed {
		t.Fatal("karar yeniden koşuda taşınmalı")
	}
	h, err := ClearHypothesisMark(context.Background(), st, "p1", last)
	if err != nil || h.HumanState != nil {
		t.Fatalf("kaldırma: %+v %v", h, err)
	}
	if got := st.get(t, "p1"); got.Coverage.Ruling != "" || hypOf(t, got.Hypotheses, last).HumanState != nil {
		t.Fatalf("kaldırma sonrası: %+v", got.Coverage)
	}
}
