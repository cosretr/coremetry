package ml

// impact_facts_test.go — v0.10.2391: servis yanıt süresi ve consumer lag etki
// olaylarının "Neden" gerçekleri. Olay dedektörün KENDİ yazıcısıyla kurulur
// (rtEvent / lagImpactEvent), gerçekler ImpactRuleFacts ile okunur ve
// standart çekirdeğinden geçirilir: TR satır + EN anahtar. Katsayılar
// impact_rule.go'dan (ikinci kopya yok). Saat / dilim bağımsız: sabit UTC an.

import (
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var factsEnd = time.Date(2026, 11, 20, 12, 5, 0, 0, time.UTC)

// factsReasons — gerçekler → Problem standardının "Neden" satırları + anahtarları.
func factsReasons(t *testing.T, p chstore.Problem, f *chstore.StdImpactRule) ([]string, []string) {
	t.Helper()
	std := chstore.StandardizeProblem(p, chstore.StdFacts{Now: factsEnd, ImpactRule: f})
	if len(std.Evidence.Reasons) != len(std.Evidence.ReasonMsgs) {
		t.Fatalf("satır %d, anahtar %d", len(std.Evidence.Reasons), len(std.Evidence.ReasonMsgs))
	}
	var keys []string
	for _, m := range std.Evidence.ReasonMsgs {
		keys = append(keys, m.K)
	}
	return std.Evidence.Reasons, keys
}

func rtFactsProblem(metric string) chstore.Problem {
	return chstore.Problem{ID: MLSvcRulePrefix + "orders", RuleID: MLSvcRulePrefix + "orders", RuleName: chstore.MLPromotedRuleNamePrefix + "servis anomalisi",
		Severity: "critical", Service: "orders", Metric: metric, Comparator: ">=", Value: 900, Threshold: 300, Status: "open",
		Description: chstore.MLResponseTimeSentence("orders", chstore.MLRespMedian, 900, 190) + " " + chstore.REDEvidenceVM + ".",
		StartedAt:   factsEnd.Add(-10 * time.Minute).UnixNano()}
}

func TestImpactRuleFactsResponseTime(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	base := rtBaseOf(repeat(rtBaseP50, 30), repeat(rtBaseP90, 30))
	zero := rtBaseOf(repeat(0, 30), repeat(0, 30))
	ok50 := rtRep(rtS(rtBaseP50, 600)) // aynı dakikanın yükü iki ölçüde aynı (tek nokta)
	medR, slowR := impactRules[impactResponseMedian], impactRules[impactResponseSlow]
	cases := []struct {
		name    string
		s       [2][]impactSample
		base    rtBase
		metric  string
		want    chstore.StdImpactRule
		reasons []string
		keys    []string
	}{
		{"medyan ateşledi", [2][]impactSample{{rtS(rtBaseP50, 600), impGap, rtS(406.25, 610), rtS(410, 620), rtS(420, 630)},
			{rtS(rtBaseP90, 600), impGap, rtS(rtBaseP90, 610), rtS(rtBaseP90, 620), rtS(rtBaseP90, 630)}}, base, "p50_ms",
			chstore.StdImpactRule{Window: 5, Violations: 3, MinViolations: 3, Threshold: medR.threshold(rtBaseP50), Rel: medR.Rel, Abs: medR.Abs,
				LoadPerMin: 630, MinLoad: 10, Unsampled: true, Measure: chstore.MLRespMedian},
			[]string{"Son 5 dakikanın 3'ünde medyan eşiği aşıldı (en az 3 gerekir)", "Eşik 294 ms: normalin 1,5 katı ve 100 ms üstü",
				"Yük yeterli: dakikada yaklaşık 630 istek (en az 10 gerekir)", "Kaynak örneklenmemiş sunucu metriği"},
			[]string{"why.violMed", "why.thrMs", "why.load", "why.srcVM"}},
		{"en yavaş %10 ateşledi", [2][]impactSample{ok50, {rtS(rtBaseP90, 600), rtS(3750, 610), rtS(3750, 620), rtS(3750, 640), rtS(3750, 650)}}, base, "p90_ms",
			chstore.StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: slowR.threshold(rtBaseP90), Rel: slowR.Rel, Abs: slowR.Abs,
				LoadPerMin: 650, MinLoad: 10, Unsampled: true, Measure: chstore.MLRespSlow},
			[]string{"Son 5 dakikanın 4'ünde en yavaş %10 eşiği aşıldı (en az 3 gerekir)", "Eşik 1,75 sn: normalin 2 katı ve 1 sn üstü",
				"Yük yeterli: dakikada yaklaşık 650 istek (en az 10 gerekir)", "Kaynak örneklenmemiş sunucu metriği"},
			[]string{"why.violSlow", "why.thrMs", "why.load", "why.srcVM"}},
		{"taban 0: eşik taban + 100 ms", [2][]impactSample{{rtS(0, 50), rtS(0, 50), rtS(500, 50), rtS(500, 50), rtS(500, 40)}, {rtS(0, 50), rtS(0, 50), rtS(0, 50), rtS(0, 50), rtS(0, 40)}}, zero, "p50_ms",
			chstore.StdImpactRule{Window: 5, Violations: 3, MinViolations: 3, Threshold: 100, Rel: medR.Rel, Abs: medR.Abs,
				LoadPerMin: 40, MinLoad: 10, Unsampled: true, Measure: chstore.MLRespMedian},
			[]string{"Son 5 dakikanın 3'ünde medyan eşiği aşıldı (en az 3 gerekir)", "Eşik 100 ms: normalin 1,5 katı ve 100 ms üstü",
				"Yük yeterli: dakikada yaklaşık 40 istek (en az 10 gerekir)", "Kaynak örneklenmemiş sunucu metriği"},
			[]string{"why.violMed", "why.thrMs", "why.load", "why.srcVM"}},
	}
	for _, c := range cases {
		v := rtDecide(c.s, c.base, 20, false)
		if !v.Fire {
			t.Fatalf("%s: ön koşul ateşleme: %+v", c.name, v)
		}
		ev := rtEvent("orders", c.s, v, factsEnd, 0, cfg, "weekday")
		f := ImpactRuleFacts(ev)
		if f == nil || *f != c.want {
			t.Fatalf("%s: gerçekler\n%+v\n%+v", c.name, f, c.want)
		}
		got, keys := factsReasons(t, rtFactsProblem(c.metric), f)
		if !reflect.DeepEqual(got, c.reasons) || !reflect.DeepEqual(keys, c.keys) {
			t.Errorf("%s:\n%q\n%q\n%q", c.name, got, c.reasons, keys)
		}
	}

	ev := rtEvent("orders", cases[0].s, rtDecide(cases[0].s, base, 20, false), factsEnd, 0, cfg, "weekday")
	set := func(kv map[string]any) chstore.MLEvent {
		e := ev
		e.Evidence = mergeEvidence(e.Evidence, kv)
		return e
	}
	t.Run("kanıt eksik / uygun değil → nil (bugünkü cümle)", func(t *testing.T) {
		noRule := ev
		noRule.Evidence = `{"source":"vm","measure":"svc_p50"}`
		for name, e := range map[string]chstore.MLEvent{
			"boş kanıt": {Detector: detSvcResponseTime}, "kuralsız kanıt": noRule, "bilinmeyen ölçü": set(map[string]any{"measure": "svc_p99"}),
			"ihlal 0": set(map[string]any{"violations": 0}), "eşik yok": set(map[string]any{"threshold": 0}),
		} {
			if g := ImpactRuleFacts(e); g != nil {
				t.Errorf("%s: %+v", name, g)
			}
		}
		std := chstore.StandardizeProblem(rtFactsProblem("p50_ms"), chstore.StdFacts{Now: factsEnd})
		if std.Evidence.Reasons != nil || std.Evidence.Sentence == "" {
			t.Errorf("gerçeksiz: %+v", std.Evidence)
		}
	})
	t.Run("eşik bugünkü sabitlerle uyuşmuyor → yalnız eşik", func(t *testing.T) {
		g := ImpactRuleFacts(set(map[string]any{"threshold": 320.0}))
		if g == nil || g.Rel != 0 || g.Abs != 0 || g.Threshold != 320 {
			t.Fatalf("%+v", g)
		}
		got, keys := factsReasons(t, rtFactsProblem("p50_ms"), g)
		if got[1] != "Eşik 320 ms" || keys[1] != "why.thrOnlyMs" {
			t.Errorf("%q %q", got, keys)
		}
	})
}

func TestImpactRuleFactsConsumerLag(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	part := kafkaTopicPart("orders.events")
	rule := impactRules[impactConsumerLag]
	group := chstore.Problem{ID: MLGroupRulePrefix + "ev-1:orders", RuleID: MLGroupRulePrefix + "ev-1", RuleName: groupProblemRuleName,
		Severity: "critical", Service: "orders", Metric: groupProblemMetric, Comparator: ">=", Value: 1, Threshold: 1, Status: "open",
		Description: "orders: tek güçlü sinyal özeti. " + chstore.REDEvidenceVM + ".", StartedAt: factsEnd.Add(-10 * time.Minute).UnixNano()}
	cases := []struct {
		name    string
		samples []impactSample
		base    lagBase
		want    chstore.StdImpactRule
		reasons []string
	}{
		{"taban 600: eşik normalin 2 katı", []impactSample{lagS(600), {}, lagS(40000), lagS(90000), lagS(165000)}, lagBaseOf(repeat(600, 30)),
			chstore.StdImpactRule{Window: 5, Violations: 3, MinViolations: 3, Threshold: 1200, Floor: rule.Floor, Rel: rule.Rel, Unsampled: true, Measure: chstore.MLImpactLag},
			[]string{"Son 5 dakikanın 3'ünde eşik aşıldı (en az 3 gerekir)", "Eşik 1.200 kayıt: en az 1.000 kayıt ve normalin 2 katı", "Kaynak örneklenmemiş sunucu metriği"}},
		{"taban 0: eşik etki tabanı", []impactSample{lagS(0), lagS(1500), lagS(1500), lagS(1500), lagS(2000)}, lagBaseOf(repeat(0, 30)),
			chstore.StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 1000, Floor: rule.Floor, Rel: rule.Rel, Unsampled: true, Measure: chstore.MLImpactLag},
			[]string{"Son 5 dakikanın 4'ünde eşik aşıldı (en az 3 gerekir)", "Eşik 1.000 kayıt: en az 1.000 kayıt ve normalin 2 katı", "Kaynak örneklenmemiş sunucu metriği"}},
	}
	for _, c := range cases {
		v := lagImpactDecide(c.samples, c.base, 20, false)
		ev := lagImpactEvent("orders", part, c.samples, v, factsEnd, 0, cfg, "weekday")
		f := ImpactRuleFacts(ev)
		if f == nil || *f != c.want {
			t.Fatalf("%s: gerçekler\n%+v\n%+v", c.name, f, c.want)
		}
		got, keys := factsReasons(t, group, f)
		if !reflect.DeepEqual(got, c.reasons) || !reflect.DeepEqual(keys, []string{"why.viol", "why.thrLag", "why.srcVM"}) {
			t.Errorf("%s: %q %q", c.name, got, keys)
		}
	}
	// Kaynak işaretsiz (eski) satır: kaynak satırı yazılmaz; yük satırı lag'de hiç yok.
	v := lagImpactDecide(cases[0].samples, cases[0].base, 20, false)
	ev := lagImpactEvent("orders", part, cases[0].samples, v, factsEnd, 0, cfg, "weekday")
	old := group
	old.Description = "orders: tek güçlü sinyal özeti."
	if got, _ := factsReasons(t, old, ImpactRuleFacts(ev)); len(got) != 2 {
		t.Errorf("işaretsiz: %q", got)
	}
	t.Run("uygun olmayan lag olayı → nil", func(t *testing.T) {
		thanos := ev
		thanos.Code = part + "@c1"
		old5 := ev
		old5.Evidence = `{"source":"vm","dwell":3}` // etki kuralı öncesi 5 dk yolu
		for name, e := range map[string]chstore.MLEvent{"thanos": thanos, "eski kanıt": old5, "boş kanıt": {Detector: detMetricKafka, Code: part}} {
			if g := ImpactRuleFacts(e); g != nil {
				t.Errorf("%s: %+v", name, g)
			}
		}
	})
}

func TestImpactFactsDetectorForAndLead(t *testing.T) {
	for metric, want := range map[string][2]string{
		"error_rate": {detSvcErrorRate, ""}, "p50_ms": {detSvcResponseTime, chstore.MLRespMedian}, "p90_ms": {detSvcResponseTime, chstore.MLRespSlow},
	} {
		det, m, ok := ImpactFactsDetectorFor(metric)
		if !ok || det != want[0] || m != want[1] {
			t.Errorf("%s → %s %s %v", metric, det, m, ok)
		}
		// Problem ölçüsünün tek tanımı (svcMetric) ile aynı ad.
		if metric != "error_rate" {
			e := chstore.MLEvent{Detector: detSvcResponseTime, Evidence: `{"measure":"svc_` + metric[:3] + `"}`}
			if svcMetric(e) != metric {
				t.Errorf("svcMetric %s ≠ %s", svcMetric(e), metric)
			}
		}
	}
	if _, _, ok := ImpactFactsDetectorFor("p95_ms"); ok {
		t.Error("yaygın yavaşlamanın etki kuralı yok")
	}
	g := chstore.Problem{RuleID: MLGroupRulePrefix + "ev-1", Metric: groupProblemMetric, Value: 1, Threshold: 1}
	if id, ok := ImpactFactsLeadOf(g); !ok || id != "ev-1" {
		t.Errorf("tek sinyal: %q %v", id, ok)
	}
	two := g
	two.Value, two.Threshold = 2, 2
	svcOwned := g
	svcOwned.Metric = "error_rate"
	svc := g
	svc.RuleID = MLSvcRulePrefix + "orders"
	for name, p := range map[string]chstore.Problem{"iki sinyal": two, "servis ölçüsü devraldı": svcOwned, "servis Problem'i": svc} {
		if _, ok := ImpactFactsLeadOf(p); ok {
			t.Errorf("%s: lider okunmamalı", name)
		}
	}
}
