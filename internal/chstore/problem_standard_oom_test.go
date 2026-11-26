package chstore

// problem_standard_oom_test.go — v0.10.2476: OOMKilled manşeti tek tanım
// (MLPodOOMSentence) ve İngilizce anahtara geri okunur; sonraki adım servisin
// Altyapı sekmesi + rec.sat.oom. Adlar uydurmadır.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMLPodOOMSentence(t *testing.T) {
	cases := []struct {
		name        string
		pods, kills int
		lim         float64
		want, key   string
		args        map[string]any
	}{
		{"limitli", 2, 3, 512, "cart · pod bellek sınırında sonlandırıldı (OOMKilled): 2 pod, son 15 dk'da 3 kez — limit 512 Mi.", "h.mlOom",
			map[string]any{"wl": "cart", "n": "2", "win": "15", "m": "3", "lim": "512"}},
		{"limit yuvarlanır", 1, 1, 1535.6, "cart · pod bellek sınırında sonlandırıldı (OOMKilled): 1 pod, son 15 dk'da 1 kez — limit 1536 Mi.", "h.mlOom",
			map[string]any{"wl": "cart", "n": "1", "win": "15", "m": "1", "lim": "1536"}},
		{"limit yok", 1, 2, 0, "cart · pod bellek sınırında sonlandırıldı (OOMKilled): 1 pod, son 15 dk'da 2 kez.", "h.mlOomNoLim",
			map[string]any{"wl": "cart", "n": "1", "win": "15", "m": "2"}},
	}
	for _, c := range cases {
		got := MLPodOOMSentence("cart", c.pods, c.kills, c.lim)
		if got != c.want {
			t.Errorf("%s:\n%q\n%q", c.name, got, c.want)
			continue
		}
		m := mlPromotedMsg(got)
		if m == nil || m.K != c.key || !reflect.DeepEqual(m.A, c.args) {
			t.Errorf("%s: anahtar %+v", c.name, m)
		}
	}
	if m := mlOOMMsg("cart · pod bellek sınırında sonlandırıldı (OOMKilled): 1 pod. Ek cümle."); m != nil {
		t.Errorf("serbest metin anahtarlanmamalı: %+v", m)
	}
}

func TestMLPodOOMNextStep(t *testing.T) {
	p := Problem{RuleID: "anomaly-auto:svc:cart-api", RuleName: MLPromotedRuleNamePrefix + "servis anomalisi", Service: "cart-api",
		Description: MLPodOOMSentence("cart", 2, 3, 512)}
	ns := problemNextStep(p, stdKindInfo{})
	if ns.Href != "/service?name=cart-api&tab=infra" || ns.Msg == nil || ns.Msg.K != "rec.sat.oom" || ns.Msg.A["pods"] != "cart" {
		t.Errorf("sonraki adım %+v", ns)
	}
	p.Description = MLErrRateSentence("cart-api", 0.068, 0.004)
	if ns := problemNextStep(p, stdKindInfo{}); ns.Msg != nil && ns.Msg.K == "rec.sat.oom" {
		t.Errorf("OOM dışı manşet: %+v", ns)
	}
}

// TestOOMPriorityNeverRendersRatio — v0.10.2476: OOM Problem'inin Value /
// Threshold'u (30 dk kill / pod sayısı) oran değildir; öncelik gerekçesi,
// etki satırı, manşet ve metin anahtarları hiçbir yerde "×" / "x threshold"
// / "kat" göstermez, gerçek nedeni söyler (TR + EN anahtarı).
func TestOOMPriorityNeverRendersRatio(t *testing.T) {
	now := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	cfg := DefaultProblemPriority()
	mk := func(killed, total, k15, k30 int) Problem {
		return Problem{ID: "anomaly-auto:svc:cart-api", RuleID: "anomaly-auto:svc:cart-api", RuleName: MLPromotedRuleNamePrefix + "servis anomalisi",
			Severity: "critical", Service: "cart-api", Metric: MLOOMMetric, Comparator: ">=", Value: float64(k30), Threshold: float64(total),
			Status: "open", StartedAt: now.Add(-10 * time.Minute).UnixNano(), Description: MLPodOOMSentence("cart", killed, k15, 512)}
	}
	cases := []struct {
		name              string
		p                 Problem
		prio, reason, key string
	}{
		{"tekrar → P1 (eski kod 100× idi)", mk(1, 10, 3, 400), "P1", "30 dk'da 400 kez OOMKilled", "prio.oomRepeat"},
		{"pay → P1", mk(3, 5, 2, 2), "P1", "3/5 pod (%60) bellek sınırında sonlandırıldı", "prio.oomShare"},
		{"P2", mk(1, 4, 1, 2), "P2", "OOMKilled: 1/4 pod, 30 dk'da 2 kez", "prio.oom"},
		{"tek replika tek kill → P2", mk(1, 1, 1, 1), "P2", "OOMKilled: 1/1 pod, 30 dk'da 1 kez", "prio.oom"},
	}
	for _, c := range cases {
		prio, why, msg := computePriorityMsg(c.p, now.UnixNano(), cfg)
		if prio != c.prio || why != c.reason || msg == nil || msg.K != c.key {
			t.Errorf("%s: %s %q %+v", c.name, prio, why, msg)
		}
		std := StandardizeProblem(c.p, StdFacts{Now: now, BigBreachRatio: 2})
		raw, _ := json.Marshal(struct {
			W string
			M *StdMsg
			S ProblemStandard
		}{why, msg, std})
		for _, bad := range []string{"×", "x threshold", " kat", "normalin", "100x", "400x"} {
			if strings.Contains(string(raw), bad) {
				t.Errorf("%s: %q görünüyor: %s", c.name, bad, raw)
			}
		}
		if std.Impact.Primary.Ratio != 0 || std.Impact.Primary.Msg == nil || std.Impact.Primary.Msg.K != "imp.oom" {
			t.Errorf("%s: etki %+v", c.name, std.Impact.Primary)
		}
	}
	// Uzun süre açık P2 → mevcut kural (critical open N h).
	old := mk(1, 4, 1, 1)
	old.StartedAt = now.Add(-5 * time.Hour).UnixNano()
	if prio, why, _ := computePriorityMsg(old, now.UnixNano(), cfg); prio != "P1" || !strings.HasPrefix(why, "critical open") {
		t.Errorf("stale: %s %q", prio, why)
	}
	// OOM ölçüsü olmayan ML Problem'i eski merdivende kalır.
	other := mk(1, 4, 1, 400)
	other.Metric = "error_rate"
	if _, why, _ := computePriorityMsg(other, now.UnixNano(), cfg); !strings.Contains(why, "x threshold") {
		t.Errorf("OOM dışı satır değişmemeli: %q", why)
	}
}
