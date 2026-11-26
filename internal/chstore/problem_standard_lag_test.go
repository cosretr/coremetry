package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.2475 — consumer lag Problem'inin cümlesi, geri okuması ve standardı.

func TestMLConsumerLagSentenceRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		topic    string
		obs, bas float64
		more     int
		want     string
		key      string
	}{
		{"orders-topic", 17936, 5, 0, "checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936 (topic orders-topic).", "h.mlLagT"},
		{"orders-topic", 165000, 600, 2, "checkout · Kafka consumer lag: normalde ~600 mesaj, şimdi 165.000 (topic orders-topic, +2 topic daha).", "h.mlLagTM"},
		{"", 1200, 0, 0, "checkout · Kafka consumer lag: normalde ~0 mesaj, şimdi 1.200.", "h.mlLag"},
	} {
		s := MLConsumerLagSentence("checkout", tc.topic, tc.obs, tc.bas, tc.more)
		if s != tc.want {
			t.Fatalf("cümle\n got %q\nwant %q", s, tc.want)
		}
		svc, l, ok := ParseMLConsumerLag(s)
		if !ok || svc != "checkout" || l.Topic != tc.topic || l.Observed != tc.obs || l.Baseline != tc.bas || l.More != tc.more {
			t.Fatalf("geri okuma: %v %q %+v", ok, svc, l)
		}
		if m := mlPromotedMsg(s); m == nil || m.K != tc.key {
			t.Fatalf("anahtar %s bekleniyordu: %+v", tc.key, m)
		}
	}
	if _, _, ok := ParseMLConsumerLag("checkout: tek güçlü sinyal (metrik, başka sinyal teyit etmedi) — lag şimdi 17936."); ok {
		t.Fatal("eski cümle lag sayılmamalı")
	}
}

// Ekrandaki vaka (sentetik adlar): grup lag satırı. Manşet standart cümle;
// etki topic + mesaj; kanıt cümlesi iç kapı dili taşımaz; sonraki adım
// Messaging sekmesi topic süzgeciyle; büyüklük (bildirim politikası girdisi)
// değişmez.
func TestStandardizeLagGroupProblem(t *testing.T) {
	desc := MLConsumerLagSentence("checkout", "orders-topic", 26, 5, 0) + " " + REDEvidenceVM + "."
	p := Problem{ID: "anomaly-auto:group:a:checkout", RuleID: MLGroupRulePrefix + "a", RuleName: MLPromotedRuleNamePrefix + "anomali grubu",
		Service: "checkout", Metric: "ml_group_lag", Comparator: ">=", Value: 17936, Threshold: 5, Severity: "critical",
		Status: "open", Description: desc, StartedAt: time.Date(2026, 11, 26, 12, 35, 0, 0, time.UTC).UnixNano()}
	std := StandardizeProblem(p, StdFacts{Now: time.Date(2026, 11, 26, 15, 30, 0, 0, time.UTC)})
	if std.Headline != "checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 26 (topic orders-topic)." {
		t.Fatalf("manşet: %q", std.Headline)
	}
	if std.HeadlineMsg == nil || std.HeadlineMsg.K != "h.mlLagT" {
		t.Fatalf("manşet anahtarı: %+v", std.HeadlineMsg)
	}
	if std.Lag == nil || std.Lag.Topic != "orders-topic" || std.Lag.Observed != 26 || std.Lag.Baseline != 5 {
		t.Fatalf("lag gerçekleri: %+v", std.Lag)
	}
	if std.Impact.Primary.Text != "topic orders-topic: 26 mesaj geride (normalde ~5)" || std.Impact.Primary.Msg.K != "imp.lagT" {
		t.Fatalf("etki: %+v", std.Impact.Primary)
	}
	if r := StdMagnitude(std); r < 3587 || r > 3588 {
		t.Fatalf("büyüklük öncelik ölçüsünden (tepe / taban) değişmemeli: %v", r)
	}
	if strings.Contains(std.Evidence.Sentence, "sürdürme") || !strings.HasPrefix(std.Evidence.Sentence, "Lag etki kuralının eşiğini aştı") {
		t.Fatalf("kanıt cümlesi: %q", std.Evidence.Sentence)
	}
	if std.Evidence.Msg == nil || std.Evidence.Msg.A["baseK"] != "ev.base.lag" || std.Evidence.Msg.A["srcK"] != "ev.src.vm" {
		t.Fatalf("kanıt anahtarı: %+v", std.Evidence.Msg)
	}
	if std.NextStep.Href != "/service?ktopic=orders-topic&name=checkout&tab=messaging" {
		t.Fatalf("sonraki adım: %+v", std.NextStep)
	}
	for _, s := range []string{std.Headline, std.Impact.Primary.Text, std.Evidence.Sentence} {
		for _, bad := range []string{"ml_group_lag", "tek güçlü sinyal", "teyit etmedi"} {
			if strings.Contains(s, bad) {
				t.Fatalf("iç dil sızdı (%q): %q", bad, s)
			}
		}
	}
	// Lag olmayan ML grup satırı aynen (lag alanı yok).
	q := p
	q.Description, q.Metric, q.Value, q.Threshold = "checkout: aynı anda 2 farklı sinyalde anomali var (trace ve log).", "ml_group_signals", 2, 2
	if s := StandardizeProblem(q, StdFacts{}); s.Lag != nil || strings.HasPrefix(s.Evidence.Sentence, "Lag") {
		t.Fatalf("lag dışı satır değişmemeli: %+v", s)
	}
}
