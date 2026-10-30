package mcptools

// v2_problems_test.go — briefing Faz 1 dilim 1 (open_problems + list_my_services
// env/açık-Problem; briefing-audit §10 R6). Nötr fikstür.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestOpenProblemsCacheKeyHashesAllInputs(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := openProblemsCacheKey([]string{"orders-api", "payments-core"}, "acme", "prod", 10, now)
	for name, k := range map[string]string{
		"küme":   openProblemsCacheKey([]string{"orders-api", "billing-api"}, "acme", "prod", 10, now),
		"takım":  openProblemsCacheKey([]string{"orders-api", "payments-core"}, "other", "prod", 10, now),
		"env":    openProblemsCacheKey([]string{"orders-api", "payments-core"}, "acme", "stage", 10, now),
		"satır":  openProblemsCacheKey([]string{"orders-api", "payments-core"}, "acme", "prod", 20, now),
		"dakika": openProblemsCacheKey([]string{"orders-api", "payments-core"}, "acme", "prod", 10, now.Add(2*time.Minute)),
	} {
		if k == base {
			t.Errorf("%s anahtarı değiştirmedi: %s", name, k)
		}
	}
	// aynı uzunlukta farklı küme ayrışmalı (v0.5.187)
	if openProblemsCacheKey([]string{"a"}, "", "", 10, now) == openProblemsCacheKey([]string{"b"}, "", "", 10, now) {
		t.Error("uzunluk-yalnız anahtar")
	}
}

func TestMyServicesCacheKeyEnv(t *testing.T) {
	to := time.Unix(1_800_000_000, 0)
	if myServicesCacheKey([]string{"orders-api"}, 0, "acme", "", to, 3600, 10) == myServicesCacheKey([]string{"orders-api"}, 0, "acme", "prod", to, 3600, 10) {
		t.Error("env anahtara girmiyor")
	}
}

func TestOpenProblemsPageURL(t *testing.T) {
	for _, c := range []struct {
		svcs []string
		team string
		want string
	}{
		{[]string{"orders-api"}, "acme", "/problems?service=orders-api"},
		{[]string{"orders-api", "payments-core"}, "acme team", "/inbox?owner=acme+team"},
		{[]string{"orders-api", "payments-core"}, "", "/problems"},
	} {
		if got := OpenProblemsPageURL(c.svcs, c.team); got != c.want {
			t.Errorf("%v/%q → %q, want %q", c.svcs, c.team, got, c.want)
		}
	}
	if V2PageURL(OpenProblemsName, `{"page_url":"/problems?service=x"}`) != "/problems?service=x" {
		t.Error("open_problems page_url V2PageURL'den geçmiyor")
	}
}

func TestProblemReason(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, c := range []struct {
		p    chstore.Problem
		want string
	}{
		{chstore.Problem{PriorityReason: "  P1: 3× eşik ", Description: "d", RuleName: "r"}, "P1: 3× eşik"},
		{chstore.Problem{Description: "hata\noranı  yüksek", RuleName: "r"}, "hata oranı yüksek"},
		{chstore.Problem{RuleName: "error rate"}, "error rate"},
	} {
		if got := problemReason(c.p); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	if n := len([]rune(problemReason(chstore.Problem{Description: long}))); n != openProblemsReasonMax {
		t.Errorf("kırpma %d", n)
	}
}

func TestOpenProblemsPayload(t *testing.T) {
	mk := func(i int) chstore.Problem {
		return chstore.Problem{ID: "p-" + strings.Repeat("9", 20) + string(rune('a'+i%26)), Service: "orders-api", Status: "open",
			Priority: "P2", Description: strings.Repeat("timeout ", 15), StartedAt: int64(1_800_000_000) * int64(time.Second)}
	}
	t.Run("boş → neden, uydurma yok", func(t *testing.T) {
		out := openProblemsPayload("acme", "", []string{"orders-api"}, 0, nil, 10)
		if out["total_open"] != 0 || out["reasons"] == nil || out["has_more"] != false {
			t.Fatalf("%v", out)
		}
	})
	t.Run("limit + 2KB tavanı", func(t *testing.T) {
		var probs []chstore.Problem
		for i := 0; i < 40; i++ {
			probs = append(probs, mk(i))
		}
		out := openProblemsPayload("acme", "prod", []string{"orders-api", "payments-core"}, 3, probs, 25)
		b, _ := json.Marshal(out)
		if len(b) > OpenProblemsMaxBytes {
			t.Fatalf("gövde %d B", len(b))
		}
		if out["total_open"] != 40 || out["has_more"] != true || out["scoped_services"] != 5 || out["env"] != "prod" {
			t.Fatalf("%v", out)
		}
		rows := out["problems"].([]OpenProblemRow)
		if len(rows) == 0 || rows[0].PageURL != ProblemPageURL(rows[0].ID) || rows[0].SinceUnix != 1_800_000_000 {
			t.Fatalf("satır %+v", rows)
		}
	})
}

func TestOpenProblemCounts(t *testing.T) {
	got := OpenProblemCounts([]chstore.Problem{{Service: "orders-api"}, {Service: "orders-api"}, {Service: "payments-core"}})
	if got["orders-api"] != 2 || got["payments-core"] != 1 || got["billing-api"] != 0 {
		t.Fatalf("%v", got)
	}
}

func TestMyServicesPayloadEnvAndOpen(t *testing.T) {
	rows := []chstore.ServiceSummary{{Name: "orders-api", SpanCount: 500, ErrorCount: 5}}
	resolved := []string{"orders-api", "payments-core"}
	out := myServicesPayload("acme", resolved, 0, rows, 3600, 10, "prod", false, map[string]int{"orders-api": 2, "payments-core": 1})
	if out["env"] != "prod" || out["env_applied"] != false || out["env_note"] == nil || out["open_problems_total"] != 3 {
		t.Fatalf("%v", out)
	}
	b, _ := json.Marshal(out["services"])
	if !strings.Contains(string(b), `"open_problems":2`) {
		t.Fatalf("satırda açık sayı yok: %s", b)
	}
	// open nil → alan yazılmaz; env boş → env alanları yok
	out = myServicesPayload("acme", resolved, 0, rows, 3600, 10, "", false, nil)
	b, _ = json.Marshal(out)
	if strings.Contains(string(b), "open_problems") || strings.Contains(string(b), "env_applied") {
		t.Fatalf("beklenmeyen alan: %s", b)
	}
}

func TestOpenProblemsNoScope(t *testing.T) {
	_, err := openProblemsTool(Deps{}).Handler(context.Background(), json.RawMessage(`{}`))
	if err != errNoProblemScope {
		t.Fatalf("err = %v", err)
	}
}
