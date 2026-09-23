package chstore

import "testing"

// v0.10.894 — ByRule: rule_id tek indeks; aynı rule_id'de started_at en yeni
// kazanır; KOPYA döner; nil alıcı güvenli; ByKey davranışı değişmez; dış seri
// Problem'i melez öznede de Custom kategoride.
func TestOpenProblemsByRule(t *testing.T) {
	ps := []Problem{
		{ID: "a", RuleID: "anomaly:ext:src/OP/E:ext:error_count", Service: "ext:src/OP/E", StartedAt: 10},
		{ID: "b", RuleID: "anomaly:ext:src/OP/E:ext:error_count", Service: "loan-svc", StartedAt: 20},
		{ID: "c", RuleID: "anomaly:svc:p99_ms", Service: "svc", StartedAt: 5},
	}
	o := NewOpenProblems(ps)
	if got := o.ByRule("anomaly:ext:src/OP/E:ext:error_count"); got == nil || got.ID != "b" {
		t.Fatalf("en yeni kazanmalı: %+v", got)
	}
	got := o.ByRule("anomaly:svc:p99_ms")
	got.Service = "mutated"
	if o.ByRule("anomaly:svc:p99_ms").Service != "svc" {
		t.Fatal("kopya dönmeli")
	}
	if o.ByRule("yok") != nil || (*OpenProblems)(nil).ByRule("x") != nil {
		t.Fatal("nil güvenli")
	}
	if o.ByKey("anomaly:ext:src/OP/E:ext:error_count", "loan-svc") == nil || o.ByKey("anomaly:ext:src/OP/E:ext:error_count", "ext:src/OP/E") == nil {
		t.Fatal("ByKey aynen")
	}
	if ProblemCategory(Problem{Kind: ProblemKindService, RuleID: "anomaly:ext:src/OP/E/M/-:ext:error_count", Metric: "ext:error_count"}) != CategoryCustom {
		t.Fatal("dış seri Problem'i melez öznede de Custom")
	}
}
