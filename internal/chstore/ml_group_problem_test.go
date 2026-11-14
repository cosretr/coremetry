package chstore

// v0.10.2052 — anomali tekilleştirme D4: ML grup Problem'i ile evaluator
// süpürmelerinin sınırı + tek anahtar (ml.groupProblems, varsayılan açık).
// Muafiyetsiz bayat süpürme 3 dk'da kapatıp 5 dk'lık ML tikinde yeniden
// açtırırdı (her açılış bir bildirim).

import (
	"testing"
	"time"
)

func TestMLGroupStaleAfter(t *testing.T) {
	cases := []struct {
		tick int
		want time.Duration
	}{
		{300, 20 * time.Minute},
		{60, 8 * time.Minute},
		{3600, 185 * time.Minute},
		{0, 20 * time.Minute}, // bilinmiyor → varsayılan tik
	}
	for _, c := range cases {
		if got := MLGroupStaleAfter(c.tick); got != c.want {
			t.Errorf("tick %d: %v, istenen %v", c.tick, got, c.want)
		}
	}
}

func TestSplitMLGroupStale(t *testing.T) {
	g1 := Problem{ID: "anomaly-auto:group:e1:orders-api", RuleID: "anomaly-auto:group:e1"}
	g2 := Problem{ID: "anomaly-auto:group:e2:payments-api", RuleID: "anomaly-auto:group:e2"}
	rule := Problem{ID: "r", RuleID: "rule-1"}
	promo := Problem{ID: "p", RuleID: "anomaly-auto:abcdef0123456789"}
	sweep, exempt := SplitMLGroupStale([]Problem{g1, rule, g2, promo}, map[string]bool{g2.ID: true})
	if len(sweep) != 3 || sweep[0].ID != "r" || sweep[1].ID != g2.ID || sweep[2].ID != "p" || len(exempt) != 1 || exempt[0].ID != g1.ID {
		t.Fatalf("sweep=%v exempt=%v", sweep, exempt)
	}
	// ikinci okuma başarısız (nil) → hiçbir grup Problem'i süpürülmez
	sweep, exempt = SplitMLGroupStale([]Problem{g1, rule, g2}, nil)
	if len(sweep) != 1 || len(exempt) != 2 {
		t.Fatalf("nil: sweep=%v exempt=%v", sweep, exempt)
	}
	if IsMLGroupProblem(MLGroupRulePrefix) || IsMLGroupProblem("anomaly-auto:subject:x") {
		t.Error("boş kuyruk / başka aile grup Problem'i değil")
	}
	// v0.10.2130 — servis düzeyi ML Problem'i de ML kaydedicisinin: bayat
	// süpürmeden aynı kuralla muaf (ML eşiğini aşınca süpürülür).
	s1 := Problem{ID: "anomaly-auto:svc:orders-api", RuleID: "anomaly-auto:svc:orders-api"}
	sweep, exempt = SplitMLGroupStale([]Problem{s1, rule}, map[string]bool{})
	if len(sweep) != 1 || sweep[0].ID != "r" || len(exempt) != 1 || exempt[0].ID != s1.ID {
		t.Fatalf("svc: sweep=%v exempt=%v", sweep, exempt)
	}
	if sweep, _ = SplitMLGroupStale([]Problem{s1}, map[string]bool{s1.ID: true}); len(sweep) != 1 {
		t.Fatalf("svc ML eşiğini aşınca süpürülür: %v", sweep)
	}
	if IsMLSvcProblem(MLSvcRulePrefix) || IsMLSvcProblem("anomaly-auto:svcx") || !IsMLLiveProblem(MLSvcRulePrefix+"a") || !IsMLLiveProblem(MLGroupRulePrefix+"e") || IsMLLiveProblem("svc-slowdown:a") {
		t.Error("IsMLSvcProblem / IsMLLiveProblem yüklemleri")
	}
}

func TestMLGroupProblemsSwitch(t *testing.T) {
	f, tr := false, true
	cases := []struct {
		name string
		c    MLConfig
		want bool
	}{
		{"ana anahtar kapalı", MLConfig{Enabled: false}, false},
		{"açık, alan yok → varsayılan açık", MLConfig{Enabled: true}, true},
		{"açık, açıkça kapalı", MLConfig{Enabled: true, GroupProblems: &f}, false},
		{"açık, açıkça açık", MLConfig{Enabled: true, GroupProblems: &tr}, true},
	}
	for _, c := range cases {
		if got := c.c.GroupProblemsOn(); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	// Normalize alanı korur (PUT'ta düşmesin) ve tespit özeti değişmez.
	on := DefaultMLConfig()
	on.Enabled = true
	off := on
	off.GroupProblems = &f
	if n := NormalizeMLConfig(off); n.GroupProblems == nil || *n.GroupProblems {
		t.Fatal("Normalize anahtarı düşürdü")
	}
	if on.ConfigHash() != off.ConfigHash() {
		t.Error("Problem anahtarı ConfigHash'e girmemeli (tespit ayarı değil)")
	}
}
