package mcptools

// v2_problems_test.go — briefing Faz 1 dilim 1 (open_problems + list_my_services
// env/açık-Problem; briefing-audit §10 R6). Nötr fikstür.

import (
	"context"
	"encoding/json"
	"fmt"
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
		{[]string{"orders-api"}, "", "/inbox?service=orders-api"},
		{[]string{"orders-api"}, "acme team", "/inbox?team=acme+team"},
		{[]string{"orders-api", "payments-core"}, "", "/inbox"},
	} {
		if got := OpenProblemsPageURL(c.svcs, c.team); got != c.want {
			t.Errorf("%v/%q → %q, want %q", c.svcs, c.team, got, c.want)
		}
	}
	if V2PageURL(OpenProblemsName, `{"page_url":"/inbox?team=x"}`) != "/inbox?team=x" {
		t.Error("open_problems page_url V2PageURL'den geçmiyor")
	}
}

func TestInboxReason(t *testing.T) {
	for _, c := range []struct{ pr, title, want string }{
		{"  P1: 3× eşik ", "t", "P1: 3× eşik"},
		{"", "hata\noranı  yüksek", "hata oranı yüksek"},
		{"", "", ""},
	} {
		if got := InboxReason(c.pr, c.title); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
	if n := len([]rune(InboxReason(strings.Repeat("x", 300), ""))); n != openProblemsReasonMax {
		t.Errorf("kırpma %d", n)
	}
}

func TestInboxOpenPlan(t *testing.T) {
	qs, exact, unread := InboxOpenPlan("acme", []string{"orders-api", "payments-core"}, "prod", 500)
	if len(qs) != 1 || qs[0].Team != "acme" || qs[0].Env != "prod" || exact || unread != 0 {
		t.Fatalf("takım planı %+v %v %d", qs, exact, unread)
	}
	qs, exact, _ = InboxOpenPlan("", []string{"orders-api"}, "", 500)
	if len(qs) != 1 || qs[0].Service != "orders-api" || exact {
		t.Fatalf("tek servis (şeridin kendisi) %+v", qs)
	}
	var many []string
	for i := 0; i < 13; i++ {
		many = append(many, fmt.Sprintf("svc-%02d", i))
	}
	qs, exact, unread = InboxOpenPlan("", many, "", 500)
	if len(qs) != inboxFanoutMax || !exact || unread != 3 {
		t.Fatalf("fan-out %d %v %d", len(qs), exact, unread)
	}
}

func TestReadInboxOpenFanoutExactAndDedup(t *testing.T) {
	d := Deps{InboxOpen: func(_ context.Context, q InboxOpenQuery) (InboxOpenResult, error) {
		// inbox ?service= alt-dize eşler: orders-api sorgusu orders-api-v2'yi de döndürür
		rows := []InboxRow{{ID: "problem:1", Service: q.Service}, {ID: "problem:shared", Service: "orders-api"}, {ID: "x:" + q.Service, Service: q.Service + "-v2"}}
		return InboxOpenResult{Rows: rows, Total: len(rows)}, nil
	}}
	res, unread, err := ReadInboxOpen(context.Background(), d, "", []string{"orders-api", "payments-core"}, "", 500)
	if err != nil || unread != 0 {
		t.Fatal(err)
	}
	got := OpenCounts(res.Rows)
	if got["orders-api"] != 2 || got["payments-core"] != 0 || got["orders-api-v2"] != 0 || res.Total != 2 {
		t.Fatalf("birebir süzgeç + tekilleştirme: %v total=%d", got, res.Total)
	}
	// payments-core satırı "problem:1" kimliğini paylaşıyor → tekilleşti; tek okuma yolunda süzgeç yok (şeritle aynı)
	if _, _, err := ReadInboxOpen(context.Background(), Deps{}, "acme", nil, "", 10); err != errNoInboxReader {
		t.Fatalf("bağlı değil hatası: %v", err)
	}
}

func TestOpenProblemsPayload(t *testing.T) {
	t.Run("boş → neden, uydurma yok", func(t *testing.T) {
		out := openProblemsPayload("acme", "", []string{"orders-api"}, 0, 0, InboxOpenResult{}, 10)
		if out["total_open"] != 0 || out["reasons"] == nil || out["has_more"] != false {
			t.Fatalf("%v", out)
		}
	})
	t.Run("limit + 2KB tavanı", func(t *testing.T) {
		var rows []InboxRow
		for i := 0; i < 40; i++ {
			rows = append(rows, InboxRow{ID: fmt.Sprintf("problem:%030d", i), Kind: "problem", Service: "orders-api", Priority: "P2",
				Reason: strings.Repeat("timeout ", 15), SinceUnix: 1_800_000_000, PageURL: "/problems?problem=p"})
		}
		out := openProblemsPayload("acme", "prod", []string{"orders-api", "payments-core"}, 3, 0, InboxOpenResult{Rows: rows, Total: 41}, 25)
		b, _ := json.Marshal(out)
		if len(b) > OpenProblemsMaxBytes {
			t.Fatalf("gövde %d B", len(b))
		}
		if out["total_open"] != 41 || out["has_more"] != true || out["scoped_services"] != 5 || out["env"] != "prod" {
			t.Fatalf("%v", out)
		}
		if strings.Contains(string(b), "StartedAtNs") {
			t.Fatal("iç alan gövdeye sızdı")
		}
	})
}

func TestOpenCounts(t *testing.T) {
	got := OpenCounts([]InboxRow{{Service: "orders-api"}, {Service: "orders-api"}, {Service: "payments-core"}})
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
