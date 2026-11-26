package api

// v0.10.2481 — Sinyaller › Bağlanmamış anomaliler: açık Problem'e bağlı
// satırın düşmesi (ML damgası, eski olayın kendi terfi satırı, promotion
// üyeliği), parametre yalnız servis görünümünde ve önbellek anahtarında.
// Servis adları SENTETİK.

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func memberEv(t *testing.T, id, problemID, ruleID, stage string) chstore.MLEvent {
	t.Helper()
	b, err := json.Marshal(chstore.MemberEvidence{ProblemID: problemID, RuleID: ruleID})
	if err != nil {
		t.Fatal(err)
	}
	return chstore.MLEvent{ID: id, Detector: chstore.MLDetMemberPromotion, Stage: stage, Service: "svc-a", Evidence: string(b)}
}

func TestDropAttachedUnified(t *testing.T) {
	open := chstore.NewOpenProblems([]chstore.Problem{
		{ID: "p-open", RuleID: "rule-x", Service: "svc-a", Status: "open"},
		{ID: "p-own", RuleID: chstore.PromotedAnomalyRulePrefix + "lg-own", Service: "svc-b", Status: "open"},
	})
	stamp := func(pid string) string { return `{"problem_id":"` + pid + `"}` }
	items := []chstore.MLEvent{
		{ID: "m-attached", Service: "svc-a", Evidence: stamp("p-open")},
		{ID: "m-closed-problem", Service: "svc-a", Evidence: stamp("p-gone")}, // Problem kapandı → bağlı değil
		{ID: "m-free", Service: "svc-c"},
	}
	legacy := []chstore.AnomalyEvent{
		{ID: "lg-own", Service: "svc-b"},    // kendi terfi satırı açık
		{ID: "lg-member", Service: "svc-a"}, // başka sahibe üye
		{ID: "lg-left", Service: "svc-a"},   // üyelikten ayrıldı
		{ID: "lg-free", Service: "svc-d"},
	}
	promoted := promotedLegacyMembers([]chstore.MLEvent{
		memberEv(t, "x1", "p-open", chstore.PromotedAnomalyRulePrefix+"lg-member", chstore.MLStageLive),
		memberEv(t, "x2", "p-open", chstore.PromotedAnomalyRulePrefix+"lg-left", chstore.MLStageClosed),
		memberEv(t, "x3", "p-gone", chstore.PromotedAnomalyRulePrefix+"lg-free", chstore.MLStageLive),
	}, open)
	row := func(key string, mlIdx, legIdx []int) ml.UnifiedRow {
		return ml.UnifiedRow{Key: key, MLLead: -1, LegLead: -1, ML: mlIdx, Legacy: legIdx}
	}
	rows := []ml.UnifiedRow{
		row("ml-attached", []int{0}, nil),
		row("ml-closed-problem", []int{1}, nil),
		row("ml-free", []int{2}, nil),
		row("leg-own", nil, []int{0}),
		row("leg-member", nil, []int{1}),
		row("leg-left", nil, []int{2}),
		row("leg-free", nil, []int{3}),
		row("group-mixed", []int{2}, []int{1}), // grup: bir üye bağlı → satır düşer
		row("bad-index", []int{9}, []int{-1}),  // bozuk indeks paniklemez, bağlı sayılmaz
	}
	var got []string
	for _, r := range dropAttachedUnified(rows, items, legacy, open, promoted) {
		got = append(got, r.Key)
	}
	want := []string{"ml-closed-problem", "ml-free", "leg-left", "leg-free", "bad-index"}
	if len(got) != len(want) {
		t.Fatalf("kalan satırlar = %v, beklenen %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kalan satırlar = %v, beklenen %v", got, want)
		}
	}
}

func TestUnifiedUnattachedParam(t *testing.T) {
	cases := []struct {
		q    url.Values
		want bool
	}{
		{url.Values{"unattached": {"1"}}, true},
		{url.Values{"unattached": {"true"}}, false},
		{url.Values{}, false},
		{url.Values{"unattached": {"1"}, "view": {"load"}}, false},
		{url.Values{"unattached": {"1"}, "view": {"platform"}}, false},
		{url.Values{"unattached": {"1"}, "event": {"e1"}}, false},
	}
	for _, c := range cases {
		if got := parseUnifiedParams(c.q.Get).Unattached; got != c.want {
			t.Errorf("%v → %v, beklenen %v", c.q, got, c.want)
		}
	}
	a := unifiedKey(unifiedParams{Section: "open", Limit: 50}, "UTC")
	b := unifiedKey(unifiedParams{Section: "open", Limit: 50, Unattached: true}, "UTC")
	if a == b {
		t.Fatal("unattached önbellek anahtarında değil")
	}
}
