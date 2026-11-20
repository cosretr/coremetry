package ml

// source_marker_test.go — v0.10.2383 (triage mockup dilim 2). SÖZLEŞME:
//   - ML canlı yolu Problem açıklamasına lider olayın GERÇEK ölçü kaynağını
//     mevcut işaretle yazar (chstore tek tanım; standart oradan okur);
//   - kaynağı mevcut işaretlerle dürüstçe söylenemeyen dedektör işaret YAZMAZ
//     (bugünkü sınıflama kalır);
//   - işaret 600 rune tavanında kırpılmaz;
//   - servis hata oranı cümlesi "normalde X, şimdi Y (N kat)" (chstore çekirdeği);
//   - etki kuralı gerçekleri kanıttan + impact_rule.go sabitlerinden (tek tanım).

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEventSourceMarkerPerDetector(t *testing.T) {
	vm, logs, none := chstore.REDEvidenceVM, chstore.EvidenceLogs, ""
	cases := []struct {
		det, evidence, want string
	}{
		// trace RED ailesi — VM sunucu histogramı (örneklenmemiş)
		{detSvcErrorRate, "", vm}, {detOpErrorRate, "", vm}, {detOpLatency, "", vm}, {detThroughputDrop, "", vm},
		{detSvcResponseTime, "", vm}, // v0.10.2385 (I4): aynı sunucu histogramının bucket'ları
		// op_latency olaylarından türetilen / aynı VM serilerini okuyanlar
		{detSvcBreadth, "", vm}, {detTracePodOutlier, "", vm}, {detTraceChangePoint, "", vm},
		// VM client histogramları
		{detDependencyLatency, "", vm}, {detDependencyErrorRate, "", vm},
		// metrik aileleri: yalnız kanıt VM diyorsa (Thanos'ta işaret yok)
		{"metric_jvm", `{"source":"vm"}`, vm}, {"metric_kafka", `{"source":"vm","expr":"x"}`, vm},
		{"metric_http_client", `{"source":"vm"}`, vm},
		{"metric_jvm", `{"source":"thanos","cluster":"c1"}`, none}, {"metric_pool", "", none}, {"metric_k8s", "{bozuk", none},
		// log dedektörleri — Elasticsearch sayımı
		{detLogVolume, "", logs}, {detErrorRate, "", logs}, {detServiceSilent, "", logs}, {detTemplateSpike, "", logs},
		{detRareTemplate, "", logs}, {detRareErrorCode, "", logs}, {detPodOutlier, "", logs}, {detChangePoint, "", logs},
		// mevcut işaretlerle dürüstçe söylenemeyenler: işaret yok
		{detTraceErrorCount, `{"source":"spanmetrics_1m"}`, none}, // CH hata span sayımı (hatalar tam; "span · örneklenmiş" değil)
		{detNewTemplate, "", none},                                // Drain defteri (örneklem)
		{chstore.MLDetSvcTelemetryVolume, "", none},
		{"bilinmeyen", "", none}, {"", "", none},
	}
	for _, c := range cases {
		if got := eventSourceMarker(chstore.MLEvent{Detector: c.det, Evidence: c.evidence}); got != c.want {
			t.Errorf("%s %s → %q, istenen %q", c.det, c.evidence, got, c.want)
		}
	}
	// ML ayarındaki her dedektör bu tabloda (yeni dedektör eklenince kaynağı karara bağlansın).
	seen := map[string]bool{}
	for _, c := range cases {
		seen[c.det] = true
	}
	for _, d := range chstore.MLDetectorNames {
		if !seen[d] {
			t.Errorf("dedektör %q kaynak tablosunda yok", d)
		}
	}
}

func TestWithSourceMarker(t *testing.T) {
	vmEv := chstore.MLEvent{Detector: detSvcErrorRate}
	if got := withSourceMarker("cümle.", vmEv); got != "cümle. "+chstore.REDEvidenceVM+"." {
		t.Errorf("%q", got)
	}
	if got := withSourceMarker("cümle.", chstore.MLEvent{Detector: detTraceErrorCount}); got != "cümle." {
		t.Errorf("işaretsiz dedektörde açıklama aynen: %q", got)
	}
	// 600 rune tavanı: gövde kırpılır, işaret kalır ve standart onu okur.
	long := withSourceMarker(strings.Repeat("ğ", 900), vmEv)
	if utf8.RuneCountInString(long) > 601 || !strings.HasSuffix(long, " "+chstore.REDEvidenceVM+".") {
		t.Errorf("tavan: %d rune, son %q", utf8.RuneCountInString(long), long[len(long)-60:])
	}
	if s, _ := chstore.ParseEvidenceSource(long); s != chstore.StdSourceVM {
		t.Errorf("kırpılmış açıklamadan kaynak: %q", s)
	}
	if got := withSourceMarker(strings.Repeat("a", 900), chstore.MLEvent{}); utf8.RuneCountInString(got) != 601 {
		t.Errorf("işaretsiz tavan (600 + …): %d", utf8.RuneCountInString(got))
	}
}

// Servis düzeyi Problem: cümle "normalde X, şimdi Y (N kat)", sonda ölçünün
// geldiği olayın kaynak işareti; manşet işareti ve istek bölümünü taşımaz.
func TestSvcProblemDescriptionMarker(t *testing.T) {
	now := time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
	er := svcEv("e1", detSvcErrorRate, "orders", 5, 0.0041, 0.0683)
	ds := decideSvcShadow([]chstore.MLEvent{er}, nil, nil, chstore.DefaultProblemPriority(), now)
	in := svcProblemInputs([]chstore.MLEvent{er}, ds)
	if len(in) != 1 {
		t.Fatalf("girdi: %+v", in)
	}
	head := "orders servisinde hata oranı normalde %0,4, şimdi %6,8 (17 kat)."
	if want := head + " " + chstore.REDEvidenceVM + "."; in[0].Desc != want {
		t.Fatalf("açıklama:\n%q\n%q", in[0].Desc, want)
	}
	p := newSvcProblem(in[0])
	if h, ok := chstore.MLPromotedHeadline(p); !ok || h != head {
		t.Errorf("manşet: %q", h)
	}
	std := chstore.StandardizeProblem(p, chstore.StdFacts{Now: now, BigBreachRatio: 2})
	if std.Evidence.Source != chstore.StdSourceVM || std.Evidence.Sampled || std.Headline != head {
		t.Errorf("standart: kaynak %s örneklenmiş=%v manşet %q", std.Evidence.Source, std.Evidence.Sampled, std.Headline)
	}
	// Problem'in Threshold'u %0,5 tabanlı (öncelik); kat olayın gerçek tabanından.
	if p.Threshold != 0.005 || p.Value != 0.0683 {
		t.Errorf("ölçü: %v / %v", p.Value, p.Threshold)
	}
	zero := svcEv("e2", detSvcErrorRate, "orders", 5, 0, 0.068)
	if got := svcEventSentence(zero); got != "orders servisinde hata oranı normalde neredeyse hiç yok, şimdi %6,8." {
		t.Errorf("taban 0: %q", got)
	}
}

// Grup Problem'i: işaret grubun lider olayının (kural kimliğindeki ilk üye)
// kaynağı; tek sinyal kolunda o tek olayın.
func TestGroupProblemDescriptionMarker(t *testing.T) {
	mem := []chstore.MLEvent{
		gateEv("a", detOpLatency, "orders-api", 0, 30, 100, 150),
		gateEv("l", detErrorRate, "orders-api", 5, 20, 10, 30),
	}
	sum := func(e chstore.MLEvent) string { return e.Service + ": özet " + e.ID + "." }
	body := "orders-api: aynı anda 2 farklı sinyalde anomali var (log ve trace). Log: özet l. Trace: özet a."
	for lead, marker := range map[string]string{"a": chstore.REDEvidenceVM, "l": chstore.EvidenceLogs} {
		g := decidedGroup{G: MLGroup{Service: "orders-api", Members: []string{lead, "x"}}, D: GroupDecision{Signals: []string{"log", "trace"}}, Members: mem}
		if got := groupProblemDescription(g, sum); got != body+" "+marker+"." {
			t.Errorf("lider %s:\n%q", lead, got)
		}
	}
	// lider üyeler arasında yok (koşu durumundan düşmüş) → işaret yok
	g := decidedGroup{G: MLGroup{Service: "orders-api", Members: []string{"yok"}}, D: GroupDecision{Signals: []string{"log", "trace"}}, Members: mem}
	if got := groupProblemDescription(g, sum); got != body {
		t.Errorf("lidersiz: %q", got)
	}

	now := gBase.Add(30 * time.Minute)
	e := gateEv("a", detOpErrorRate, "orders-api", 0, 30, 0.01, 0.05)
	e.Code = "GET /orders"
	gs, _ := decideGroups([]chstore.MLEvent{e}, nil, 10*time.Minute, GroupGateInput{}, now)
	if len(gs) != 1 || !gs[0].D.SingleWouldOpen {
		t.Fatalf("ön koşul: %+v", gs)
	}
	single := singleProblemDescription(gs[0], func(m chstore.MLEvent) CardInput { return SummaryInput(m, chstore.DefaultMLConfig(), now, time.UTC) })
	if !strings.HasSuffix(single, "dakikadır sürüyor. "+chstore.REDEvidenceVM+".") {
		t.Errorf("tek sinyal: %q", single)
	}
}

// Etki kuralı gerçekleri: sayılar kanıttan, kuralın katsayıları impact_rule.go'dan.
func TestImpactRuleFacts(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	end := time.Date(2026, 11, 19, 12, 5, 0, 0, time.UTC)
	samples := []impactSample{impS(0.05, 3000), impS(0.05, 3050), impS(0.06, 3080), impS(0.065, 3090), impS(0.0677, 3100)}
	v := svcImpactDecide(samples, repeat(0.004, 30), repeat(51.7, 30), 20, false)
	ev := svcImpactEvent("orders", samples, v, end, 0, cfg, "weekday")
	rule := impactRules[impactFailureRate]

	f := ImpactRuleFacts(ev)
	if f == nil {
		t.Fatalf("gerçek yok: %s", ev.Evidence)
	}
	want := chstore.StdImpactRule{Window: impactWindow, Violations: 5, MinViolations: impactViolations, Threshold: rule.threshold(0.004),
		Floor: rule.Floor, Rel: rule.Rel, Abs: rule.Abs, LoadPerMin: 3100, MinLoad: rule.MinLoad, Unsampled: true}
	if *f != want {
		t.Fatalf("gerçekler:\n%+v\n%+v", *f, want)
	}
	// gölge damgası kanıtı bozmaz
	stamped := []chstore.MLEvent{ev}
	stampSvcShadow(stamped, map[string]SvcShadow{ev.ID: {WouldOpen: true, RuleID: MLSvcRulePrefix + "orders"}})
	if g := ImpactRuleFacts(stamped[0]); g == nil || *g != want {
		t.Errorf("damgalı kanıt: %+v", g)
	}

	set := func(e chstore.MLEvent, kv map[string]any) chstore.MLEvent {
		e.Evidence = mergeEvidence(e.Evidence, kv)
		return e
	}
	t.Run("yük en yeni İHLAL dakikasından", func(t *testing.T) {
		// son dakika eşiğin altında (düzeliyor), ondan önceki ihlal
		s2 := []impactSample{impS(0.05, 3000), impS(0.05, 3050), impS(0.06, 3080), impS(0.065, 2900), impS(0.002, 3100)}
		v2 := svcImpactDecide(s2, repeat(0.004, 30), repeat(51.7, 30), 20, true)
		g := ImpactRuleFacts(svcImpactEvent("orders", s2, v2, end, 0, cfg, "weekday"))
		if g == nil || g.Violations != 4 || g.LoadPerMin != 2900 {
			t.Errorf("%+v", g)
		}
	})
	t.Run("kanıttaki eşik bugünkü sabitlerle uyuşmuyor → katsayı yazılmaz", func(t *testing.T) {
		g := ImpactRuleFacts(set(ev, map[string]any{"threshold": 0.03}))
		if g == nil || g.Rel != 0 || g.Floor != 0 || g.Abs != 0 || g.Threshold != 0.03 {
			t.Errorf("%+v", g)
		}
	})
	t.Run("kaynak vm değil → örneklenmemiş denmez", func(t *testing.T) {
		if g := ImpactRuleFacts(set(ev, map[string]any{"source": "x"})); g == nil || g.Unsampled {
			t.Errorf("%+v", g)
		}
	})
	t.Run("uygun olmayan olay → nil", func(t *testing.T) {
		old := ev
		old.Evidence = `{"source":"vm","dwell":3,"bucketSec":300}` // v0.10.2373 öncesi
		other := ev
		other.Detector = detOpErrorRate
		for name, e := range map[string]chstore.MLEvent{
			"eski kanıt": old, "başka dedektör": other, "boş kanıt": {Detector: detSvcErrorRate}, "bozuk JSON": {Detector: detSvcErrorRate, Evidence: "{"},
			"ihlal 0":  set(ev, map[string]any{"violations": 0}),
			"eşik yok": set(ev, map[string]any{"threshold": 0}),
		} {
			if g := ImpactRuleFacts(e); g != nil {
				t.Errorf("%s: %+v", name, g)
			}
		}
	})
	t.Run("en yeni uygun olay", func(t *testing.T) {
		older := set(ev, map[string]any{"violations": 3})
		older.LastSeen = ev.LastSeen - int64(time.Hour)
		newestOld := ev
		newestOld.Evidence, newestOld.LastSeen = `{"dwell":3}`, ev.LastSeen+int64(time.Hour) // kanıtsız daha yeni olay
		g := NewestImpactRuleFacts([]chstore.MLEvent{older, newestOld, ev})
		if g == nil || g.Violations != 5 {
			t.Errorf("%+v", g)
		}
		if NewestImpactRuleFacts(nil) != nil || NewestImpactRuleFacts([]chstore.MLEvent{newestOld}) != nil {
			t.Error("uygun olay yokken nil")
		}
	})
	if ImpactFactsDetector != detSvcErrorRate {
		t.Error("detay ucu servis hata oranı olaylarını okumalı")
	}
	// kanıt alan adları dedektörün yazdığıyla aynı (sessiz kopma pini)
	var m map[string]any
	if err := json.Unmarshal([]byte(ev.Evidence), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"rule", "source", "threshold", "violations", "window", "minViolations", "minLoad", "ratios", "loads"} {
		if _, ok := m[k]; !ok {
			t.Errorf("kanıtta %q yok", k)
		}
	}
}
