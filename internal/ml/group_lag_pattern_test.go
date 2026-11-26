package ml

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2476 (operatör kararı): önceki 7 günün ≥ 3'ünde benzer saatte
// tekrarlayan consumer lag → beklenen günlük desen, Problem P3 (bastırılmaz).

// priorDays — olayın başlangıcından k gün önceki aynı saatler.
func priorDays(e chstore.MLEvent, ks ...int) map[string][]int64 {
	var out []int64
	for _, k := range ks {
		out = append(out, e.StartedAt-int64(k)*int64(24*time.Hour))
	}
	return map[string][]int64{chstore.MLSeriesKey(e.Service, e.Code): out}
}

func TestGroupLagDailyPatternDecision(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	imp := lagImpactEvidence(true, 1200, 4)
	big := lagGateEv("a", now, 4*time.Minute, 600, 165000, imp) // ≥ 2 × taban: v0.10.2396 P1
	mid := lagGateEv("a", now, 4*time.Minute, 600, 900, imp)    // 1,5 kat: P2
	cfg := chstore.DefaultProblemPriority()
	for _, tc := range []struct {
		name    string
		e       chstore.MLEvent
		hist    map[string][]int64
		prio    string
		metric  string
		pattern int
	}{
		{"kural kapalı (nil) → P1 aynen", big, nil, "P1", groupLagMetric, 0},
		{"2/7 → P1 aynen", big, priorDays(big, 1, 2), "P1", groupLagMetric, 0},
		{"3/7 → P3 desen", big, priorDays(big, 1, 2, 5), "P3", chstore.MLGroupLagPatternMetric, 3},
		{"4/7 → P3 desen", big, priorDays(big, 1, 3, 4, 7), "P3", chstore.MLGroupLagPatternMetric, 4},
		{"8 gün önce sayılmaz", big, priorDays(big, 1, 2, 8), "P1", groupLagMetric, 0},
		{"başka seri sayılmaz", big, map[string][]int64{chstore.MLSeriesKey("orders", "x"): priorDays(big, 1, 2, 3)[chstore.MLSeriesKey(big.Service, big.Code)]}, "P1", groupLagMetric, 0},
		{"P2 lag de desende P3", mid, priorDays(mid, 1, 2, 3), "P3", chstore.MLGroupLagPatternMetric, 3},
	} {
		gs := GroupMLEvents([]chstore.MLEvent{tc.e}, 10*time.Minute)
		d := DecideGroupPromotion(gs[0], []chstore.MLEvent{tc.e}, GroupGateInput{LagHistory: tc.hist}, now)
		if !d.WouldOpen || d.Priority != tc.prio || d.PatternDays != tc.pattern {
			t.Fatalf("%s: karar %+v", tc.name, d)
		}
		if tc.pattern > 0 && (!strings.Contains(d.Pattern, "beklenen günlük desen") || !strings.Contains(d.Reason, d.Pattern)) {
			t.Fatalf("%s: gerekçe %q / %q", tc.name, d.Pattern, d.Reason)
		}
		plan := planGroupProblems([]decidedGroup{{G: gs[0], D: d, Members: []chstore.MLEvent{tc.e}}}, nil, true, func(decidedGroup) string { return "x" })
		if len(plan.Open) != 1 {
			t.Fatalf("%s: bastırılmamalı, açılış %+v", tc.name, plan)
		}
		p := plan.Open[0]
		if got, why := chstore.PreviewProblemPriority(p, now.UnixNano(), cfg); got != tc.prio || p.Metric != tc.metric {
			t.Fatalf("%s: satır %s %s (%s)", tc.name, got, p.Metric, why)
		}
		// 4 sa sonra da P3 (staleCrit yok; şiddet warning).
		if tc.pattern > 0 {
			if got, _ := chstore.PreviewProblemPriority(p, now.Add(5*time.Hour).UnixNano(), cfg); got != "P3" {
				t.Fatalf("%s: 5 sa sonra %s", tc.name, got)
			}
			if id, ok := ImpactFactsLeadOf(p); !ok || id != "a" {
				t.Fatalf("%s: lider okunmalı: %q %v", tc.name, id, ok)
			}
		}
	}
}

// Lag dışı tek sinyal olayı desen geçmişi olsa da desen almaz.
func TestGroupLagDailyPatternOnlyLag(t *testing.T) {
	if _, ok := lagDailyPattern([]chstore.MLEvent{{ID: "a", Detector: detErrorRate, Service: "s", StartedAt: gBase.UnixNano()}}, "a",
		map[string][]int64{chstore.MLSeriesKey("s", ""): {gBase.Add(-24 * time.Hour).UnixNano(), gBase.Add(-48 * time.Hour).UnixNano(), gBase.Add(-72 * time.Hour).UnixNano()}}); ok {
		t.Fatal("hata oranı olayı desen almamalı")
	}
}

// Tazeleme: açık P1 satırı desene inmez; desen satırı desen dışı kararla
// (ikinci sinyal) kararın ölçüsünü ve şiddetini alır.
func TestRefreshGroupMeasurePattern(t *testing.T) {
	pat := GroupDecision{Severity: "warning", PatternDays: 4, Value: 4, Threshold: 7}
	multi := GroupDecision{Severity: "critical", Signals: []string{"log", "metric"}}
	p1 := chstore.Problem{Severity: "critical", Metric: groupLagMetric, Value: 165000, Threshold: 600}
	q := p1
	refreshGroupMeasure(&q, pat)
	if q.Metric != groupLagMetric || q.Value != 165000 {
		t.Fatalf("P1 desene inmemeli: %+v", q)
	}
	sig := chstore.Problem{Severity: "critical", Metric: groupProblemMetric, Value: 1, Threshold: 1}
	q = sig
	refreshGroupMeasure(&q, pat)
	if q.Metric != groupProblemMetric {
		t.Fatalf("P2 desene inmemeli: %+v", q)
	}
	q = chstore.Problem{Severity: "warning", Metric: chstore.MLGroupLagPatternMetric, Value: 3, Threshold: 7}
	refreshGroupMeasure(&q, pat)
	if q.Metric != chstore.MLGroupLagPatternMetric || q.Value != 4 || q.Severity != "warning" {
		t.Fatalf("desen tazelenmeli: %+v", q)
	}
	refreshGroupMeasure(&q, multi)
	if q.Metric != groupProblemMetric || q.Severity != "critical" || q.Value != 2 {
		t.Fatalf("ikinci sinyal desenden çıkarmalı: %+v", q)
	}
}

// Geçmiş okuması: yalnız bilinmeyen lag bölümleri; bellek bölüm ayrılınca budanır.
func TestLagHistoryNeedsAndInput(t *testing.T) {
	now := gBase.Add(30 * time.Minute)
	a := lagGateEv("a", now, 4*time.Minute, 600, 165000, "")
	other := chstore.MLEvent{ID: "e", Detector: detErrorRate, Service: "orders", StartedAt: now.UnixNano()}
	memo := lagHistMemo{}
	miss, svcs, codes, since, until := lagHistoryNeeds([]chstore.MLEvent{a, other}, memo)
	if len(miss) != 1 || len(svcs) != 1 || len(codes) != 1 || codes[0] != a.Code {
		t.Fatalf("eksik %v %v %v", miss, svcs, codes)
	}
	if st := time.Unix(0, a.StartedAt); until != st || !since.Before(st.Add(-7*24*time.Hour)) {
		t.Fatalf("aralık %v–%v", since, until)
	}
	memo[lagEpisodeKey(a)] = []int64{1, 2}
	memo["eski@x"] = []int64{9}
	if miss, _, _, _, _ := lagHistoryNeeds([]chstore.MLEvent{a}, memo); len(miss) != 0 {
		t.Fatalf("bellekteki bölüm yeniden okunmamalı: %v", miss)
	}
	in := lagHistoryInput([]chstore.MLEvent{a, other}, memo)
	if got := in[chstore.MLSeriesKey(a.Service, a.Code)]; len(got) != 2 {
		t.Fatalf("girdi %v", in)
	}
	if _, ok := memo["eski@x"]; ok {
		t.Fatal("ayrılan bölüm bellekten düşmeli")
	}
}
