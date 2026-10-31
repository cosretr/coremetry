package api

// ai_loop_budget_settings_test.go — CoSRE v2 araç seti dilim 4 (docs/cosre/tools-audit.md
// §10 G3): `ai.loopBudget` ayarı sabitleri yalnız DARALTIR; ≤0 / eksik alan
// sabite düşer. Tavanı aşan değer tavana kırpılır (ayar genişletemez).

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestNarrowLoopBudget(t *testing.T) {
	ceil := loopBudget{MaxCalls: 6, MaxRounds: 5, MaxTokens: 0, MaxTime: 540 * time.Second, ToolTextBytes: 2048}
	cases := []struct {
		name string
		set  chstore.AILoopBudget
		want loopBudget
	}{
		{"boş ayar = sabitler", chstore.AILoopBudget{}, ceil},
		{"negatif = sabit", chstore.AILoopBudget{MaxCalls: -1, MaxRounds: -3, MaxTokens: -5, MaxSeconds: -1, ToolTextBytes: -9}, ceil},
		{"daraltma", chstore.AILoopBudget{MaxCalls: 3, MaxRounds: 2, MaxTokens: 50000, MaxSeconds: 120, ToolTextBytes: 1024},
			loopBudget{MaxCalls: 3, MaxRounds: 2, MaxTokens: 50000, MaxTime: 120 * time.Second, ToolTextBytes: 1024}},
		{"genişletme tavana kırpılır", chstore.AILoopBudget{MaxCalls: 99, MaxRounds: 99, MaxSeconds: 99999, ToolTextBytes: 1 << 20},
			ceil},
		{"tavana eşit", chstore.AILoopBudget{MaxCalls: 6, MaxRounds: 5, MaxSeconds: 540, ToolTextBytes: 2048}, ceil},
		{"tek alan", chstore.AILoopBudget{MaxRounds: 1},
			loopBudget{MaxCalls: 6, MaxRounds: 1, MaxTime: 540 * time.Second, ToolTextBytes: 2048}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := narrowLoopBudget(c.set, ceil); got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}

// Token tavanı: sabit 0 = kapalı (sınırsız); ayar açar, tavan varsa onu aşamaz.
func TestNarrowLoopBudgetTokens(t *testing.T) {
	cases := []struct {
		ceil uint64
		set  int64
		want uint64
	}{
		{0, 0, 0}, {0, -1, 0}, {0, 1000, 1000}, {500, 1000, 500}, {5000, 1000, 1000}, {5000, 0, 5000},
	}
	for _, c := range cases {
		got := narrowLoopBudget(chstore.AILoopBudget{MaxTokens: c.set}, loopBudget{MaxTokens: c.ceil}).MaxTokens
		if got != c.want {
			t.Errorf("ceil=%d set=%d: got %d want %d", c.ceil, c.set, got, c.want)
		}
	}
}

// Value+unit kuralı (v0.6.36): saniye alanı time.Duration'a SANİYE olarak çevrilir.
func TestLoopBudgetSecondsUnits(t *testing.T) {
	for _, s := range []int64{1, 60, 179, 900} {
		got := narrowLoopBudget(chstore.AILoopBudget{MaxSeconds: s}, loopBudget{MaxTime: time.Hour}).MaxTime
		if got != time.Duration(s)*time.Second {
			t.Errorf("%d s → %v", s, got)
		}
	}
}

func TestClampToolResultBytes(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		max   int
		trunc bool
	}{
		{"sığar", "kısa", 2048, false},
		{"tam sınır", strings.Repeat("a", 2048), 2048, false},
		{"ascii taşar", strings.Repeat("a", 3000), 2048, true},
		{"çok baytlı taşar", strings.Repeat("ş", 1500), 2048, true},
		{"tek bayt kayması", "a" + strings.Repeat("ş", 1500), 2048, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, trunc := clampToolResultBytes(c.in, c.max)
			if trunc != c.trunc {
				t.Fatalf("trunc=%v want %v", trunc, c.trunc)
			}
			if !utf8.ValidString(out) {
				t.Fatal("rune sınırı bozuldu")
			}
			if !trunc {
				if out != c.in {
					t.Fatal("kırpılmayan içerik değişti")
				}
				return
			}
			body := out[:strings.Index(out, "\n\n[kırpıldı")]
			if len(body) > c.max || len(body) < c.max-utf8.UTFMax {
				t.Fatalf("gövde %d bayt, tavan %d", len(body), c.max)
			}
			kept := utf8.RuneCountInString(body)
			if !strings.HasSuffix(out, sprintfTruncNote(kept, utf8.RuneCountInString(c.in)-kept)) {
				t.Fatalf("not yanlış: %q", out[len(body):])
			}
		})
	}
}

// v1 (plan yok) bayt aynı: sabit rune yolu; v2 planı bayt tavanını uygular.
func TestClampToolResultForLoop(t *testing.T) {
	big := strings.Repeat("a", 3000)
	if out, _ := clampToolResultForLoop(nil, big); out != big {
		t.Fatal("v1 yolu 3000 baytı kırpmamalı (6000 rune tavanı)")
	}
	out, trunc := clampToolResultForLoop(&v2LoopPlan{Budget: loopBudget{ToolTextBytes: 2048}}, big)
	if !trunc || !strings.HasPrefix(out, strings.Repeat("a", 2048)+"\n\n[kırpıldı") {
		t.Fatal("v2 planı 2048 bayta kırpmalı")
	}
	if out, _ := clampToolResultForLoop(&v2LoopPlan{}, big); out != big {
		t.Fatal("bütçesiz v2 planı sabit rune yoluna düşmeli")
	}
}

// PUT: gönderilmeyen alan korunur, negatif 0'a (= sabit) iner.
func TestMergeLoopBudgetPut(t *testing.T) {
	cur := chstore.AILoopBudget{MaxCalls: 4, MaxRounds: 3, ToolTextBytes: 1024}
	i := func(v int) *int { return &v }
	i64 := func(v int64) *int64 { return &v }
	cases := []struct {
		name string
		in   aiLoopBudgetPut
		want chstore.AILoopBudget
	}{
		{"boş gövde", aiLoopBudgetPut{}, cur},
		{"tek alan", aiLoopBudgetPut{MaxRounds: i(2)}, chstore.AILoopBudget{MaxCalls: 4, MaxRounds: 2, ToolTextBytes: 1024}},
		{"sıfırla", aiLoopBudgetPut{MaxCalls: i(0), ToolTextBytes: i(0)}, chstore.AILoopBudget{MaxRounds: 3}},
		{"negatif", aiLoopBudgetPut{MaxTokens: i64(-1), MaxSeconds: i64(-5)}, cur},
		{"token + süre", aiLoopBudgetPut{MaxTokens: i64(8000), MaxSeconds: i64(300)},
			chstore.AILoopBudget{MaxCalls: 4, MaxRounds: 3, MaxTokens: 8000, MaxSeconds: 300, ToolTextBytes: 1024}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mergeLoopBudgetPut(cur, c.in); got != c.want {
				t.Fatalf("got %+v want %+v", got, c.want)
			}
		})
	}
}
