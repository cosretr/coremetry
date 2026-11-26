package api

// problem_standard_impact_test.go — v0.10.2383 (triage mockup dilim 2).
// SÖZLEŞME: etki kuralı gerçekleri ("Neden" satırları) YALNIZ detay ucunda ve
// yalnız ölçüsü servis hata oranı / yanıt süresi olan ML servis Problem'inde
// ya da tek sinyalli (consumer lag) grup Problem'inde okunur (v0.10.2391); liste
// yolu (attachInboxStandards) kaynak nesnede gerçek taşımaz → ek okuma yok.

import (
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestStdImpactReadFor(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	svc := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "error_rate", Status: "open",
		StartedAt: now.Add(-10 * time.Minute).UnixNano()}
	since := stdImpactEventsSince(svc.StartedAt, now)
	// v0.10.2391 — ölçü başına dedektör + beklenen ölçü; servis × dedektör, ≤ 20 satır.
	for metric, want := range map[string][2]string{"error_rate": {"svc_error_rate", ""}, "p50_ms": {"svc_response_time", chstore.MLRespMedian},
		"p90_ms": {"svc_response_time", chstore.MLRespSlow}} {
		p := svc
		p.Metric = metric
		rd, ok := stdImpactReadFor(p, now)
		wantF := chstore.MLEventsFilter{Service: "orders", Detectors: []string{want[0], "endpoint_impact"}, Since: since, Limit: stdImpactEventsCap} // v0.10.2474: + uç nokta
		if !ok || !reflect.DeepEqual(rd.filter, wantF) || rd.measure != want[1] || rd.lead != "" {
			t.Errorf("%s: %+v %v", metric, rd, ok)
		}
	}
	ack := svc
	ack.Status = "acknowledged"
	if _, ok := stdImpactReadFor(ack, now); !ok {
		t.Fatal("üstlenilmiş (açık) Problem de okumalı")
	}
	// Tek sinyal kolunun grup Problem'i: lider olay, birincil anahtarla tek satır.
	grp := chstore.Problem{ID: "anomaly-auto:group:ev1:orders", RuleID: "anomaly-auto:group:ev1", Service: "orders", Metric: "ml_group_signals",
		Value: 1, Threshold: 1, Status: "open", StartedAt: svc.StartedAt}
	rd, ok := stdImpactReadFor(grp, now)
	if wantF := (chstore.MLEventsFilter{ID: "ev1", Service: "orders", Since: since, Limit: 1}); !ok || !reflect.DeepEqual(rd.filter, wantF) ||
		rd.lead != "ev1" || rd.measure != chstore.MLImpactLag {
		t.Fatalf("grup: %+v %v", rd, ok)
	}
	for name, p := range map[string]chstore.Problem{
		"ölçü yaygın yavaşlamadan":           func() chstore.Problem { p := svc; p.Metric = "p95_ms"; return p }(),
		"iki sinyalli grup":                  func() chstore.Problem { p := grp; p.Value, p.Threshold = 2, 2; return p }(),
		"eski aile sahibi":                   func() chstore.Problem { p := svc; p.RuleID = "anomaly:orders:error_rate"; return p }(),
		"alarm kuralı":                       func() chstore.Problem { p := svc; p.RuleID = "r-1"; return p }(),
		"servissiz":                          func() chstore.Problem { p := svc; p.Service = ""; return p }(),
		"kapanmış (kanıt sönen son pencere)": func() chstore.Problem { p := svc; p.Status = "resolved"; return p }(),
		"kapanmış grup":                      func() chstore.Problem { p := grp; p.Status = "resolved"; return p }(),
		"anomaly-auto terfi kopyası":         func() chstore.Problem { p := svc; p.RuleID = "anomaly-auto:7f3a"; return p }(),
	} {
		if _, ok := stdImpactReadFor(p, now); ok {
			t.Errorf("%s: okuma yapılmamalı", name)
		}
	}
}

// TestStdImpactFactsOfReadPath — v0.10.2391: okunan satırlardan gerçekler.
// Kanıtlar dedektörlerin yazdığı alan adlarıyla (rtEvent / lagImpactEvent).
func TestStdImpactFactsOfReadPath(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	start := now.Add(-10 * time.Minute).UnixNano()
	rtEv := func(id, measure string, th float64, last int64, problem string) chstore.MLEvent {
		return chstore.MLEvent{ID: id, Detector: "svc_response_time", Service: "orders", Baseline: 180, StartedAt: start, LastSeen: last,
			Evidence: `{"source":"vm","rule":"impact","measure":"` + measure + `","threshold":` + strconv.FormatFloat(th, 'f', -1, 64) +
				`,"violations":4,"window":5,"minViolations":3,"minLoad":10,"p50s":[150,400,410,null,420],"p90s":[700,800,800,null,900],` +
				`"loads":[3000,3050,3100,0,3120],"svcShadow":{"live":true,"problemId":"` + problem + `"}}`}
	}
	svc := chstore.Problem{ID: "anomaly-auto:svc:orders", RuleID: "anomaly-auto:svc:orders", Service: "orders", Metric: "p50_ms", Status: "open", StartedAt: start}
	rd, _ := stdImpactReadFor(svc, now)
	evs := []chstore.MLEvent{
		rtEv("e-old", "svc_p50", 280, now.Add(-time.Hour).UnixNano(), svc.ID),
		rtEv("e-new", "svc_p50", 280, now.UnixNano(), svc.ID),
		rtEv("e-other", "svc_p50", 280, now.Add(time.Minute).UnixNano(), "anomaly-auto:svc:x"), // başka Problem'in olayı
	}
	f := stdImpactFactsOf(rd, svc, evs)
	want := &chstore.StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 280, Rel: 1.5, Abs: 100, LoadPerMin: 3120, MinLoad: 10,
		Unsampled: true, Measure: chstore.MLRespMedian}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("yanıt süresi:\n%+v\n%+v", f, want)
	}
	// Problem'in ölçüsü p50 ama en yeni olay p90 diyor → satır yok (bugünkü cümle).
	if g := stdImpactFactsOf(rd, svc, []chstore.MLEvent{rtEv("e-new", "svc_p90", 1180, now.UnixNano(), svc.ID)}); g != nil {
		t.Errorf("ölçü uyuşmazlığı: %+v", g)
	}
	// Kanıt eksik → nil.
	bare := rtEv("e-new", "svc_p50", 280, now.UnixNano(), svc.ID)
	bare.Evidence = `{"source":"vm","svcShadow":{"live":true,"problemId":"` + svc.ID + `"}}`
	if g := stdImpactFactsOf(rd, svc, []chstore.MLEvent{bare}); g != nil {
		t.Errorf("kanıtsız: %+v", g)
	}

	// Grup Problem'i (tek sinyal kolu, consumer lag): lider olayın kanıtı.
	grp := chstore.Problem{ID: "anomaly-auto:group:lag-1:orders", RuleID: "anomaly-auto:group:lag-1", Service: "orders", Metric: "ml_group_signals",
		Value: 1, Threshold: 1, Status: "open", StartedAt: start}
	grd, _ := stdImpactReadFor(grp, now)
	lag := chstore.MLEvent{ID: "lag-1", Detector: "metric_kafka", Service: "orders", Code: "metric_kafka|consumer_lag#orders.events", Baseline: 600,
		StartedAt: start, LastSeen: now.UnixNano(),
		Evidence: `{"source":"vm","expr":"metric_kafka|consumer_lag#orders.events","rule":"impact","fired":true,"threshold":1200,"violations":4,"window":5,` +
			`"minViolations":3,"sampleSec":60,"values":[600,40000,null,90000,165000]}`}
	g := stdImpactFactsOf(grd, grp, []chstore.MLEvent{lag})
	wantLag := &chstore.StdImpactRule{Window: 5, Violations: 4, MinViolations: 3, Threshold: 1200, Floor: 1000, Rel: 2, Unsampled: true, Measure: chstore.MLImpactLag}
	if !reflect.DeepEqual(g, wantLag) {
		t.Fatalf("lag:\n%+v\n%+v", g, wantLag)
	}
	std := standardizeInboxSrc(&inboxStdSrc{problem: &grp, impactRule: g}, chstore.StdFacts{Now: now}, 0)
	if std == nil || std.Evidence.Reasons[1] != "Eşik 1.200 kayıt: en az 1.000 kayıt ve normalin 2 katı" {
		t.Fatalf("lag satırları: %+v", std)
	}
	// v0.10.2396: lag ≥ 2 × taban satırı (Metric ml_group_lag, Value = lag,
	// Threshold = taban; P1) aynı lider olaydan aynı "Neden" satırlarını verir.
	p1 := grp
	p1.Metric, p1.Value, p1.Threshold = "ml_group_lag", 165000, 600
	p1rd, ok := stdImpactReadFor(p1, now)
	if !ok || p1rd.lead != "lag-1" {
		t.Fatalf("P1 lag satırı lider okumalı: %+v %v", p1rd, ok)
	}
	if g1 := stdImpactFactsOf(p1rd, p1, []chstore.MLEvent{lag}); !reflect.DeepEqual(g1, wantLag) {
		t.Fatalf("P1 lag: %+v", g1)
	} else if s1 := standardizeInboxSrc(&inboxStdSrc{problem: &p1, impactRule: g1}, chstore.StdFacts{Now: now}, 0); s1 == nil || s1.Evidence.Reasons[1] != std.Evidence.Reasons[1] {
		t.Fatalf("P1 lag satırları: %+v", s1)
	}
	// Lider olay değil / lag değil → nil.
	other := lag
	other.ID = "lag-2"
	notLag := rtEv("lag-1", "svc_p50", 280, now.UnixNano(), "")
	for name, e := range map[string]chstore.MLEvent{"başka olay": other, "lider lag değil": notLag} {
		if g := stdImpactFactsOf(grd, grp, []chstore.MLEvent{e}); g != nil {
			t.Errorf("%s: %+v", name, g)
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
