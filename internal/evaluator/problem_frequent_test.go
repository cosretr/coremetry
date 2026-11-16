package evaluator

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
)

var freqKeep = []string{"anomaly:*"}

func errProb(id, svc string, start int64, status, prio string) chstore.Problem {
	p := chstore.Problem{ID: id, RuleID: "anomaly:" + svc + ":error_rate", Service: svc, Metric: "error_rate", Value: 4, Threshold: 1,
		Severity: "critical", Status: status, Priority: prio, StartedAt: start}
	if status == "resolved" {
		r := start + int64(20*time.Minute)
		p.ResolvedAt = &r
	}
	return p
}

func TestFrequentPassHoldsAndMemoises(t *testing.T) {
	now := time.Unix(0, gBase)
	st := &fakeGraphStore{hist: []chstore.Problem{
		errProb("h1", "cart-api", gBase-2*int64(time.Hour), "resolved", "P2"),
		errProb("h2", "cart-api", gBase-5*int64(time.Hour), "resolved", "P2"),
		errProb("h3", "cart-api", gBase-9*int64(time.Hour), "resolved", "P2"),
		errProb("h4", "search-api", gBase-1*int64(time.Hour), "resolved", "P2"),
	}}
	cur := errProb("p-cart", "cart-api", gBase-int64(time.Minute), "open", "P2")
	fs := newFrequentState()
	out := fs.pass(context.Background(), st, []*chstore.Problem{&cur}, nil, freqKeep, now)
	v, ok := out["p-cart"]
	if !ok || v.Kind != chstore.FrequentKindHold || v.Prior24h != 3 || v.Signature != "svc:cart-api|error|" || len(v.Episodes) != 3 {
		t.Fatalf("verdict = %+v (ok=%v)", v, ok)
	}
	if st.histReads != 1 || len(st.histSubjects) != 1 || st.histSubjects[0] != "cart-api" {
		t.Fatalf("reads=%d subjects=%v", st.histReads, st.histSubjects)
	}
	// İkinci pas: karar ve geçmiş hatırlanır, okuma yok.
	fs.pass(context.Background(), st, []*chstore.Problem{&cur}, nil, freqKeep, now.Add(time.Minute))
	if st.histReads != 1 {
		t.Fatalf("memo: reads=%d", st.histReads)
	}
	r := fs.report()
	if r.Frequent != 1 || r.WouldHold != 1 || r.SeverityUp != 0 || len(r.TopSignatures) != 1 || r.TopSignatures[0].Held != 1 || r.AtThreshold["3"] != 1 || r.AtThreshold["5"] != 0 {
		t.Fatalf("report = %+v", r)
	}
	// Tutulacakken P1'e çıktı → kabul ölçütü sayacı.
	cur.Priority = "P1"
	fs.pass(context.Background(), st, []*chstore.Problem{&cur}, nil, freqKeep, now.Add(2*time.Minute))
	if r := fs.report(); r.HeldBecameP1 != 1 || len(r.BecameP1) != 1 || r.BecameP1[0].ProblemID != "p-cart" {
		t.Fatalf("became P1: %+v", r)
	}
}

func TestFrequentSignatureFollowsOrigin(t *testing.T) {
	now := time.Unix(0, gBase)
	dbID := "db:postgresql@orders-db"
	st := &fakeGraphStore{}
	cur := errProb("p-cart", "cart-api", gBase, "open", "P2")
	fs := newFrequentState()
	fs.pass(context.Background(), st, []*chstore.Problem{&cur}, map[string][2]string{"p-cart": {dbID, "postgresql@orders-db"}}, freqKeep, now)
	if len(st.histSubjects) != 1 || st.histSubjects[0] != dbID {
		t.Fatalf("history subject must be the origin's problems.service value: %v", st.histSubjects)
	}
	if subjectColumn(entity.ServiceID("cart-api")) != "cart-api" || subjectColumn("subj/service:cart-api") != "cart-api" || subjectColumn(dbID) != dbID {
		t.Fatal("subject column")
	}
}

func TestFrequentShadowVerdictOnDemandNoProcessState(t *testing.T) {
	// Kararı yoksa ve depo yoksa (test): ok=false, panik yok.
	e := &Evaluator{}
	if _, ok := e.FrequentShadowVerdict(context.Background(), errProb("p", "cart-api", gBase, "open", "P2")); ok {
		t.Fatal("no store, no verdict")
	}
}
