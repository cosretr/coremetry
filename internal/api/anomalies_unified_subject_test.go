package api

// v0.10.2482 — özne grubu Problem'i ("anomaly-auto:subject:<svc>:<metrik>:<yön>")
// olay kimliği taşımaz: üyelik anahtardan kurulur (chstore.SubjectGroupRuleOf),
// kendi açık satırı ya da açık sahibe üyelik (promotion üye olayı) bağlar.
// ?service= süzgeci sınırlı ve anahtarda. Servis adları SENTETİK.

import (
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestDropAttachedSubjectGroup(t *testing.T) {
	rule := chstore.SubjectGroupRuleID("svc-s", "p95_ms", "up")
	ruleMember := chstore.SubjectGroupRuleID("svc-m", "error_rate", "up")
	open := chstore.NewOpenProblems([]chstore.Problem{
		{ID: rule + ":svc-s", RuleID: rule, Service: "svc-s", Status: "open"},
		{ID: "p-owner", RuleID: "rule-y", Service: "svc-m", Status: "open"},
	})
	subj := func(id, svc, pattern, dir, status, promo string) chstore.AnomalyEvent {
		return chstore.AnomalyEvent{ID: id, Kind: chstore.SubjectAnomalyKind, SubjectKind: chstore.AnomalySubjectEndpoint,
			Service: svc, Pattern: pattern, Direction: dir, Status: status, Promotion: promo}
	}
	legacy := []chstore.AnomalyEvent{
		subj("s-active", "svc-s", "p95_ms @ GET /a", "up", "active", ""),                                 // grup satırı açık → bağlı
		subj("s-live-ended", "svc-s", "p95_ms @ GET /b", "up", "resolved", chstore.AnomalyPromotionLive), // grubu açtı → bağlı
		subj("s-ended", "svc-s", "p95_ms @ GET /c", "up", "resolved", ""),                                // bitti, açan değil → bağlanmamış
		subj("s-other-metric", "svc-s", "error_rate @ GET /a", "up", "active", ""),                       // başka anahtar
		subj("s-member", "svc-m", "error_rate @ GET /x", "up", "active", ""),                             // açık sahibe üye
	}
	promoted := promotedLegacyMembers([]chstore.MLEvent{memberEv(t, "x", "p-owner", ruleMember, chstore.MLStageLive)}, open)
	var rows []ml.UnifiedRow
	for i, e := range legacy {
		rows = append(rows, ml.UnifiedRow{Key: e.ID, MLLead: -1, LegLead: i, Legacy: []int{i}})
	}
	var got []string
	for _, r := range dropAttachedUnified(rows, nil, legacy, open, promoted) {
		got = append(got, r.Key)
	}
	if len(got) != 2 || got[0] != "s-ended" || got[1] != "s-other-metric" {
		t.Fatalf("kalan satırlar = %v, beklenen [s-ended s-other-metric]", got)
	}
}

func TestUnifiedServiceParam(t *testing.T) {
	if got := parseUnifiedParams(url.Values{"service": {" orders-api "}}.Get).Service; got != "orders-api" {
		t.Fatalf("service = %q", got)
	}
	if got := parseUnifiedParams(url.Values{"service": {strings.Repeat("a", 300)}}.Get).Service; got != "" {
		t.Fatal("aşırı uzun servis adı süzgece girmemeli")
	}
	a := unifiedKey(unifiedParams{Section: "open", Limit: 50, Unattached: true}, "UTC")
	b := unifiedKey(unifiedParams{Section: "open", Limit: 50, Unattached: true, Service: "orders-api"}, "UTC")
	c := unifiedKey(unifiedParams{Section: "open", Limit: 50, Unattached: true, Service: "payments-api"}, "UTC")
	if a == b || b == c || !strings.Contains(a, ":v7:") {
		t.Fatal("servis önbellek anahtarında değil ya da anahtar sürümü artmadı")
	}
}
