package ml

// impact_summary_test.go — v0.10.2482: etki özeti (Etki sütunu). Kanıtlar
// dedektörlerin yazdığı alan adlarıyla (svcImpactEvent / rtEvent /
// endpointImpact / lagImpactEvent).

import (
	"math"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func impSumEv(det, code, ev string, baseline float64) chstore.MLEvent {
	return chstore.MLEvent{ID: "ev-" + det, Detector: det, Service: "orders", Code: code, Baseline: baseline,
		StartedAt: 1, LastSeen: 2, Evidence: ev}
}

func TestImpactSummaryOf(t *testing.T) {
	const errHead = `{"source":"vm","rule":"impact","threshold":0.05,"violations":4,"window":5,"minViolations":3,"minLoad":10,`
	const rtHead = `{"source":"vm","rule":"impact","measure":"svc_p50","threshold":280,"violations":4,"window":5,"minViolations":3,"minLoad":10,`
	const lagHead = `{"source":"vm","rule":"impact","threshold":1200,"violations":4,"window":5,"minViolations":3,"sampleSec":60,`
	lagCode := "metric_kafka|consumer_lag#orders.events"
	f := func(v float64) *float64 { return &v }
	cases := []struct {
		name string
		e    chstore.MLEvent
		want *chstore.StdImpactSummary // nil = özet yok
		text string
	}{
		{"hata oranı servis: medyan(oran × yük), pay medyan oran",
			impSumEv("svc_error_rate", "", errHead+`"ratios":[0.01,0.12,0.14,null,0.16],"loads":[2000,2400,2500,0,2300]}`, 0.01),
			&chstore.StdImpactSummary{Kind: "errors", Scope: "service", PerMin: 319, SharePct: 13}, "~319 istek/dk başarısız · trafiğin %13'ü"},
		{"hata oranı uç nokta: pay uç noktanın trafiğinde",
			impSumEv("endpoint_impact", "POST /checkout", errHead+`"measure":"ep_err_ratio","ratios":[0.2,0.2,0.3],"loads":[10,20,20]}`, 0.01),
			&chstore.StdImpactSummary{Kind: "errors", Scope: "route", Route: "POST /checkout", PerMin: 4, SharePct: 20}, "~4 istek/dk başarısız · uç nokta trafiğinin %20'si"},
		{"sıfır yük → özet yok",
			impSumEv("svc_error_rate", "", errHead+`"ratios":[0.2,0.3,0.4],"loads":[0,0,0]}`, 0.01), nil, ""},
		{"kısa kanıt (tek ölçülmüş dakika) → özet yok",
			impSumEv("svc_error_rate", "", errHead+`"ratios":[null,null,0.4],"loads":[50,50,50]}`, 0.01), nil, ""},
		{"yük dizisi yok → özet yok",
			impSumEv("svc_error_rate", "", errHead+`"ratios":[0.2,0.3,0.4]}`, 0.01), nil, ""},
		{"etki kuralı olmayan kanıt → özet yok",
			impSumEv("svc_error_rate", "", `{"source":"vm","ratios":[0.2,0.3],"loads":[50,50]}`, 0.01), nil, ""},
		{"bozuk kanıt → özet yok", impSumEv("svc_error_rate", "", `{`, 0.01), nil, ""},
		{"başka dedektör → özet yok",
			impSumEv("trace_red", "", errHead+`"ratios":[0.2,0.3],"loads":[50,50]}`, 0.01), nil, ""},
		{"yanıt süresi: etkilenen = medyan yük, gecikme = medyan − taban",
			impSumEv("svc_response_time", "", rtHead+`"p50s":[150,800,860,null,900],"p90s":[1,1,1,1,1],"loads":[1000,1200,1250,0,1150]}`, 180),
			&chstore.StdImpactSummary{Kind: "latency", Scope: "service", Measure: chstore.MLRespMedian, PerMin: 1175, DelayMs: 650}, "~1.175 istek/dk · +650 ms"},
		{"yanıt süresi tabanı bilinmiyor → gecikme yazılmaz",
			impSumEv("svc_response_time", "", rtHead+`"p50s":[800,860],"p90s":[1,1],"loads":[40,60]}`, 0),
			&chstore.StdImpactSummary{Kind: "latency", Scope: "service", Measure: chstore.MLRespMedian, PerMin: 50}, "~50 istek/dk"},
		{"lag: biriken en yeni dakika, artış ilk→son",
			impSumEv("metric_kafka", lagCode, lagHead+`"values":[600,40000,null,90000,18600]}`, 600),
			&chstore.StdImpactSummary{Kind: "lag", Backlog: 18600, GrowthPerMin: f(4500)}, "18.600 mesaj birikmiş · +4.500/dk"},
		{"lag tek ölçülmüş dakika (yük yok): artış yok",
			impSumEv("metric_kafka", lagCode, lagHead+`"values":[null,null,null,null,18213]}`, 600),
			&chstore.StdImpactSummary{Kind: "lag", Backlog: 18213}, "18.213 mesaj birikmiş"},
		{"lag eriyor: artış negatif, sıra 0",
			impSumEv("metric_kafka", lagCode, lagHead+`"values":[9000,8000,7000]}`, 600),
			&chstore.StdImpactSummary{Kind: "lag", Backlog: 7000, GrowthPerMin: f(-1000)}, "7.000 mesaj birikmiş"},
	}
	for _, c := range cases {
		got := ImpactSummaryOf(c.e)
		if c.want == nil {
			if got != nil {
				t.Errorf("%s: özet beklenmiyordu: %+v", c.name, got)
			}
			continue
		}
		if got == nil {
			t.Errorf("%s: özet yok", c.name)
			continue
		}
		near := func(a, b float64) bool { return math.Abs(a-b) < 1e-6 }
		gw := func(p *float64) float64 {
			if p == nil {
				return math.NaN()
			}
			return *p
		}
		if got.Kind != c.want.Kind || got.Scope != c.want.Scope || got.Route != c.want.Route || got.Measure != c.want.Measure ||
			!near(got.PerMin, c.want.PerMin) || !near(got.SharePct, c.want.SharePct) || !near(got.DelayMs, c.want.DelayMs) ||
			!near(got.Backlog, c.want.Backlog) || (got.GrowthPerMin == nil) != (c.want.GrowthPerMin == nil) ||
			(got.GrowthPerMin != nil && !near(gw(got.GrowthPerMin), gw(c.want.GrowthPerMin))) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, *got, *c.want)
		}
		if got.Text != c.text {
			t.Errorf("%s: metin %q, beklenen %q", c.name, got.Text, c.text)
		}
	}
}

// TestProblemImpactSummary — Problem → lider olay → özet; OOM Problem'in
// sayılarından; kural / monitör / kapanmış Problem → nil.
func TestProblemImpactSummary(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	start := now.Add(-10 * time.Minute).UnixNano()
	stamp := func(id string) string { return `,"svcShadow":{"live":true,"problemId":"` + id + `"}}` }
	errEv := func(id string, last int64, problem string, loads string) chstore.MLEvent {
		return chstore.MLEvent{ID: id, Detector: "svc_error_rate", Service: "orders", Baseline: 0.01, StartedAt: start, LastSeen: last,
			Evidence: `{"source":"vm","rule":"impact","threshold":0.05,"violations":4,"window":5,"minViolations":3,"minLoad":10,` +
				`"ratios":[0.1,0.1,0.1],"loads":` + loads + stamp(problem)}
	}
	svc := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate", Status: "open", StartedAt: start}
	evs := []chstore.MLEvent{
		errEv("old", now.Add(-time.Hour).UnixNano(), svc.ID, "[10,10,10]"),
		errEv("new", now.UnixNano(), svc.ID, "[3000,3000,3000]"),
		errEv("other", now.Add(time.Minute).UnixNano(), "anomaly-auto:svc:x", "[99999,99999,99999]"),
	}
	if s := ProblemImpactSummary(svc, evs); s == nil || s.PerMin != 300 || s.Rank != 300 {
		t.Fatalf("servis Problem'i en yeni bağlı olaydan okumalı: %+v", s)
	}
	closed := svc
	closed.Status = "resolved"
	if s := ProblemImpactSummary(closed, evs); s != nil {
		t.Errorf("kapanmış Problem özetsiz: %+v", s)
	}
	rule := chstore.Problem{ID: "r1", RuleID: "rule-1", Service: "orders", Metric: "error_rate", Status: "open", StartedAt: start}
	if s := ProblemImpactSummary(rule, evs); s != nil {
		t.Errorf("kullanıcı alarm kuralının etki ölçüsü yok: %+v", s)
	}
	mon := chstore.Problem{ID: "m1", RuleID: "monitor:abc", Service: "orders", Metric: "status", Status: "open", StartedAt: start}
	if s := ProblemImpactSummary(mon, evs); s != nil {
		t.Errorf("monitör özetsiz: %+v", s)
	}
	oom := chstore.Problem{ID: "anomaly-auto:svc:orders:oom", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: chstore.MLOOMMetric,
		Status: "open", StartedAt: start, Value: 4, Threshold: 5, Description: chstore.MLPodOOMSentence("orders-api", 3, 4, 512)}
	if s := ProblemImpactSummary(oom, nil); s == nil || s.Kind != "oom" || s.Killed != 3 || s.Pods != 5 || s.Text != "3/5 pod" || s.Rank != 3 {
		t.Errorf("OOM: %+v", s)
	}
	// Grup (lag) Problem'i: lider olay.
	grp := chstore.Problem{ID: "anomaly-auto:group:lag-1:orders", RuleID: "anomaly-auto:group:lag-1", Service: "orders", Metric: "ml_group_signals",
		Value: 1, Threshold: 1, Status: "open", StartedAt: start}
	lag := chstore.MLEvent{ID: "lag-1", Detector: "metric_kafka", Service: "orders", Code: "metric_kafka|consumer_lag#orders.events", Baseline: 600,
		StartedAt: start, LastSeen: now.UnixNano(),
		Evidence: `{"source":"vm","rule":"impact","threshold":1200,"violations":4,"window":5,"minViolations":3,"sampleSec":60,"values":[600,40000,null,90000,165000]}`}
	if s := ProblemImpactSummary(grp, []chstore.MLEvent{lag}); s == nil || s.Kind != "lag" || s.Backlog != 165000 {
		t.Errorf("lag lideri: %+v", s)
	}
	// ≥ 2 sinyalli grup: tek olay Problem'in tamamını açıklamaz.
	grp2 := grp
	grp2.Value = 2
	if s := ProblemImpactSummary(grp2, []chstore.MLEvent{lag}); s != nil {
		t.Errorf("çok sinyalli grup özetsiz: %+v", s)
	}
}
