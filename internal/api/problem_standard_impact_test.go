package api

// problem_standard_impact_test.go — v0.10.2383 (triage mockup dilim 2).
// SÖZLEŞME: etki kuralı gerçekleri ("Neden" satırları) YALNIZ detay ucunda ve
// yalnız ölçüsü servis hata oranı olan ML servis Problem'inde okunur; liste
// yolu (attachInboxStandards) kaynak nesnede gerçek taşımaz → ek okuma yok.

import (
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestStdImpactRuleSubject(t *testing.T) {
	svc := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate", Status: "open"}
	if !stdImpactRuleSubject(svc) {
		t.Fatal("ML servis hata oranı Problem'i gerçekleri okumalı")
	}
	ack := svc
	ack.Status = "acknowledged"
	if !stdImpactRuleSubject(ack) {
		t.Fatal("üstlenilmiş (açık) Problem de okumalı")
	}
	for name, mut := range map[string]func(*chstore.Problem){
		"ölçü yaygın yavaşlamadan":           func(p *chstore.Problem) { p.Metric = "p95_ms" },
		"grup Problem'i":                     func(p *chstore.Problem) { p.RuleID = "anomaly-auto:group:ev1" },
		"eski aile sahibi":                   func(p *chstore.Problem) { p.RuleID = "anomaly:orders:error_rate" },
		"alarm kuralı":                       func(p *chstore.Problem) { p.RuleID = "r-1" },
		"servissiz":                          func(p *chstore.Problem) { p.Service = "" },
		"kapanmış (kanıt sönen son pencere)": func(p *chstore.Problem) { p.Status = "resolved" },
		"anomaly-auto terfi kopyası":         func(p *chstore.Problem) { p.RuleID = "anomaly-auto:7f3a" },
	} {
		p := svc
		mut(&p)
		if stdImpactRuleSubject(p) {
			t.Errorf("%s: okuma yapılmamalı", name)
		}
	}
}

func TestStdImpactEventsSince(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	started := now.Add(-2 * time.Hour)
	if got := stdImpactEventsSince(started.UnixNano(), now); !got.Equal(started.Add(-chstore.ProblemEventSlack)) {
		t.Errorf("pencere başı %v", got)
	}
	// çok eski Problem: 30 günle sınırlı (olay çizelgesi okumasıyla aynı taban)
	if got := stdImpactEventsSince(now.Add(-90*24*time.Hour).UnixNano(), now); !got.Equal(now.Add(-30 * 24 * time.Hour)) {
		t.Errorf("taban %v", got)
	}
}

// Gerçekler kaynak nesneden standarda geçer; nesnede yoksa (liste yolu) satır yok.
func TestStandardizeInboxSrcImpactRule(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	p := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", RuleName: chstore.MLPromotedRuleNamePrefix + "servis anomalisi",
		Severity: "critical", Service: "orders", Metric: "error_rate", Comparator: ">=", Value: 0.068, Threshold: 0.005, Status: "open",
		Description: chstore.MLErrRateSentence("orders", 0.068, 0.004) + " " + chstore.REDEvidenceVM + ".", StartedAt: now.Add(-10 * time.Minute).UnixNano()}
	rule := &chstore.StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 0.014, Floor: 0.01, Rel: 1.5, Abs: 0.01, LoadPerMin: 3100, MinLoad: 10, Unsampled: true}

	list := standardizeInboxSrc(&inboxStdSrc{problem: &p}, chstore.StdFacts{Now: now}, 0)
	if list == nil || list.Evidence.Reasons != nil {
		t.Fatalf("liste yolu neden satırı taşımamalı: %+v", list)
	}
	detail := standardizeInboxSrc(&inboxStdSrc{problem: &p, impactRule: rule}, chstore.StdFacts{Now: now}, 0)
	want := []string{
		"Son 5 dakikanın 4'ünde eşik aşıldı (en az 3 gerekir)",
		"Eşik %1,4: en az %1, normalin 1,5 katı ve 1 puan üstü",
		"Yük yeterli: dakikada yaklaşık 3.100 istek (en az 10 gerekir)",
		"Kaynak örneklenmemiş sunucu metriği",
	}
	if detail == nil || !reflect.DeepEqual(detail.Evidence.Reasons, want) {
		t.Fatalf("detay satırları: %+v", detail)
	}
	// satır ile başlık aynı manşet / kaynak / güven (tek çekirdek)
	if list.Headline != detail.Headline || list.Evidence.Source != detail.Evidence.Source || list.Confidence.Level != detail.Confidence.Level ||
		list.Evidence.Sentence != detail.Evidence.Sentence {
		t.Errorf("liste ↔ detay ayrıştı:\n%+v\n%+v", list, detail)
	}
}
