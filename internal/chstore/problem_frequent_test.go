package chstore

import (
	"strings"
	"testing"
	"time"
)

const fHour = int64(time.Hour)

func fEps(ref int64, hoursAgo ...float64) []FrequentEpisode {
	var out []FrequentEpisode
	for i, h := range hoursAgo {
		out = append(out, FrequentEpisode{ID: "e" + string(rune('a'+i)), StartedAt: ref - int64(h*float64(fHour)), Priority: "P2", Magnitude: 3})
	}
	return out
}

func TestEvaluateFrequent(t *testing.T) {
	ref := time.Date(2026, 11, 16, 14, 0, 0, 0, time.UTC).UnixNano()
	cur := Problem{ID: "p-now", StartedAt: ref}
	std := ProblemStandard{Impact: StdImpact{Primary: StdMeasure{Ratio: 3}}}
	cases := []struct {
		name      string
		prio      string
		hist      []FrequentEpisode
		kind      string
		p24, days int
	}{
		{"three in 24h → hold", "P2", fEps(ref, 1, 3, 5), FrequentKindHold, 3, 1},
		{"two in 24h, three days → not frequent", "P2", fEps(ref, 1, 3, 30, 54), "", 2, 3},
		{"four distinct days in 7d → hold", "P2", fEps(ref, 30, 54, 78, 102), FrequentKindHold, 0, 4},
		{"older than 7d ignored", "P2", fEps(ref, 1, 3, 200), "", 2, 1},
		{"priority above the window's highest → severity", "P1", fEps(ref, 1, 3, 5), FrequentKindSeverity, 3, 1},
		{"current problem and later episodes excluded", "P2", append(fEps(ref, 1, 3), FrequentEpisode{ID: "p-now", StartedAt: ref - fHour}, FrequentEpisode{ID: "later", StartedAt: ref + fHour}), "", 2, 1},
	}
	for _, c := range cases {
		v := EvaluateFrequent(FrequentVerdict{}, cur, std, c.prio, c.hist)
		if v.Kind != c.kind || v.Prior24h != c.p24 || v.Days7d != c.days || v.Frequent != (c.kind != "") {
			t.Fatalf("%s: %+v", c.name, v)
		}
	}
	// büyüklük ≥ 2× penceredeki en büyük → şiddet arttı
	big := ProblemStandard{Impact: StdImpact{Primary: StdMeasure{Ratio: 6}}}
	if v := EvaluateFrequent(FrequentVerdict{}, cur, big, "P2", fEps(ref, 1, 3, 5)); v.Kind != FrequentKindSeverity || v.Bypass != "impact_2x" {
		t.Fatalf("impact 2x: %+v", v)
	}
	// en çok 5 bölüm, en yeni önce
	if v := EvaluateFrequent(FrequentVerdict{}, cur, std, "P2", fEps(ref, 1, 2, 3, 4, 5, 6, 7)); len(v.Episodes) != 5 || v.Episodes[0].StartedAt != ref-fHour {
		t.Fatalf("episodes: %+v", v.Episodes)
	}
}

func TestFrequentShadowCodeOrder(t *testing.T) {
	hold := FrequentVerdict{Frequent: true, Kind: FrequentKindHold}
	sev := FrequentVerdict{Frequent: true, Kind: FrequentKindSeverity}
	cases := []struct {
		dec  NotifyDecision
		v    FrequentVerdict
		want string
	}{
		{NotifyDecision{Notifiable: true}, hold, FrequentShadowHold},
		{NotifyDecision{Notifiable: true}, sev, FrequentShadowSeverity},
		{NotifyDecision{Hold: NotifyHoldDedupe}, hold, FrequentShadowHold}, // frequent tekrar penceresinden ÖNCE
		{NotifyDecision{Hold: NotifyHoldNoise}, hold, FrequentShadowHold},
		{NotifyDecision{Hold: NotifyHoldImpactErr}, hold, FrequentShadowEarlier}, // etki tabanı daha önce
		{NotifyDecision{Hold: NotifyHoldGatePrefix + "low"}, hold, FrequentShadowEarlier},
		{NotifyDecision{Notifiable: true}, FrequentVerdict{}, ""},
	}
	for i, c := range cases {
		if got := FrequentShadowCode(c.dec, c.v); got != c.want {
			t.Fatalf("case %d: %q want %q", i, got, c.want)
		}
	}
}

func TestFrequentHistorySQLBounded(t *testing.T) {
	q := frequentHistorySQL("id")
	for _, want := range []string{"started_at >= ?", "service IN (?)", "status = 'resolved'", "LIMIT 2000", "max_execution_time = 5", "max_rows_to_read = 5000000", "read_overflow_mode = 'break'"} {
		if !strings.Contains(q, want) {
			t.Fatalf("history SQL missing %q:\n%s", want, q)
		}
	}
	if FrequentDirection(Problem{Value: 1, Threshold: 5}, StdFamilyTraffic) != "down" || FrequentDirection(Problem{Value: 9, Threshold: 5}, StdFamilyTraffic) != "up" ||
		FrequentDirection(Problem{Value: 1, Threshold: 5}, StdFamilyError) != "" {
		t.Fatal("direction only separates traffic")
	}
}
