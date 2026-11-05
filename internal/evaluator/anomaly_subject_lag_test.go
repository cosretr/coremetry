package evaluator

// v0.10.1802 — Davis paritesi dilim 3: Kafka consumer lag özne olayının terfi
// kapısı ve Problem başlığı. Kapı p99 ile AYNI eşikler (sürdürme 600 sn, oran
// 2,0×); tabansız lag'in sayım kolu YOK (count-only yalnız error_rate).

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func lagEv(ratio float64, dir string, startedAgo time.Duration) chstore.AnomalyEvent {
	sid := chstore.QueueSubjectID("kafka", "orders")
	pattern := subjectMetricConsumerLag + " @ " + sid
	bk := chstore.AnomalyBaselineSeasonal
	if ratio == 0 {
		bk = chstore.AnomalyBaselineConsecutive // taban 0: oran bilinmiyor
	}
	return chstore.AnomalyEvent{
		ID:           chstore.FingerprintAnomaly(chstore.SubjectAnomalyKind, pattern, "orders-consumer"),
		Kind:         chstore.SubjectAnomalyKind,
		Pattern:      pattern,
		Service:      "orders-consumer",
		StartedAt:    subjNow.Add(-startedAgo).UnixNano(),
		LastSeen:     subjNow.UnixNano(),
		PeakRatio:    ratio,
		CurrentRatio: ratio,
		CurrentCount: 48_000,
		Direction:    dir,
		BaselineKind: bk,
		SubjectKind:  chstore.AnomalySubjectQueue,
		SubjectID:    sid,
		Status:       "active",
	}
}

func TestSubjectPromotionGateConsumerLag(t *testing.T) {
	scfg, pcfg := subjCfgs()
	cases := []struct {
		name string
		ev   chstore.AnomalyEvent
		want bool
	}{
		{"artış 40×, 15 dk → geçer", lagEv(40, "up", 15*time.Minute), true},
		{"artış tam 2,0×, tam 600 sn → geçer", lagEv(2, "up", 600*time.Second), true},
		{"artış 1,9× → geçmez", lagEv(1.9, "up", 15*time.Minute), false},
		{"artış 40×, 9 dk → geçmez (sürdürme)", lagEv(40, "up", 9*time.Minute), false},
		{"düşüş → geçmez", lagEv(0.1, "down", 15*time.Minute), false},
		{"yön bilinmiyor → geçmez", lagEv(40, "", 15*time.Minute), false},
		{"taban yok (oran 0), yüksek lag → geçmez (sayım kolu yok)", lagEv(0, "up", 15*time.Minute), false},
	}
	for _, c := range cases {
		if got := subjectPromotionGate(c.ev, scfg, pcfg, subjNow); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	if subjectCountOnly(lagEv(0, "up", time.Hour)) {
		t.Error("consumer_lag sayım koluna girdi")
	}
}

func TestSubjectConsumerLagProblemTitle(t *testing.T) {
	scfg, pcfg := subjCfgs()
	ev := lagEv(40, "up", 15*time.Minute)
	cands := subjectLiveCandidates([]chstore.AnomalyEvent{ev}, nil, func(chstore.AnomalyEvent) bool { return true })
	if len(cands) != 1 || cands[0].RuleID != promoteAnomalyRuleID+ev.ID || len(cands[0].Opening) != 1 {
		t.Fatalf("aday: %+v", cands)
	}
	p := subjectGroupProblem(cands[0], nil, scfg, pcfg, subjNow)
	if want := "orders-consumer: orders topic'inde consumer lag 40,0× taban (15 dk)"; p.RuleName != want {
		t.Errorf("başlık:\n got %q\nwant %q", p.RuleName, want)
	}
	if p.Service != "orders-consumer" || p.Metric != "anomaly_ratio" || p.Value != 40 || p.Severity != "critical" {
		t.Errorf("problem: %+v", p)
	}
	if got := subjectGroupTitle(subjectGroupKey{Service: "orders-consumer", Metric: subjectMetricConsumerLag, Direction: "up"},
		[]chstore.AnomalyEvent{lagEv(0, "up", time.Hour)}, subjNow); got != "orders-consumer: orders topic'inde consumer lag taban yok (1 sa)" {
		t.Errorf("tabansız başlık: %q", got)
	}
	// anomaly.subjectMetricConsumerLag ile aynı literal (o paket de pinler).
	if subjectMetricConsumerLag != "consumer_lag" {
		t.Error("metrik adı ikizden ayrıştı")
	}
	if subjectMetricLabel(subjectMetricConsumerLag) != "consumer lag" {
		t.Error("metrik etiketi")
	}
}
