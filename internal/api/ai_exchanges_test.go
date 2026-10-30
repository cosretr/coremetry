package api

import (
	"bytes"
	"encoding/csv"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1646 (operatör onaylı) — /ai "CoSRE etkileşimleri" pencerenin TÜM
// sorularını gösterir: sunucu sayfası + arama + kullanıcı/kademe süzgeci +
// CSV. Önceki semptom: LIMIT 200 eski turları gizliyordu, "tavana dayandı"
// notundan başka yol yoktu. Bu dosya handler'ın SAF çekirdeğini çiviler:
// parametre ayrıştırma, önbellek anahtarı (her girdi anahtarı değiştirir —
// v0.5.187 sınıfı), hasMore, CSV satırları ve rota kapıları.

func TestParseAIExchangesParams(t *testing.T) {
	v := url.Values{}
	v.Set("from", "1761811200000000000")
	v.Set("to", "1761814800000000000")
	v.Set("limit", "100")
	v.Set("offset", "200")
	v.Set("feedback", "down")
	v.Set("q", "  neden yavaş  ")
	v.Set("user", "user-a@example.test")
	v.Set("tier", "explain")
	p := parseAIExchangesParams(v)
	if p.Limit != 100 || p.Offset != 200 || p.Feedback != "down" || p.Q != "neden yavaş" ||
		p.User != "user-a@example.test" || p.Tier != "explain" {
		t.Fatalf("ayrıştırma: %+v", p)
	}
	bad := url.Values{"tier": {"nope"}, "feedback": {"x"}, "offset": {"-3"}, "limit": {"99999"}}
	b := parseAIExchangesParams(bad)
	if b.Tier != "" || b.Feedback != "" || b.Offset != 0 || b.Limit != 100 {
		t.Fatalf("geçersiz girdiler normalize edilmeli: %+v", b)
	}
}

func TestAIExchangesCacheKey_EveryInputChangesKey(t *testing.T) {
	from := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	base := chstore.ListAIExchangesParams{From: from, To: from.Add(time.Hour), Limit: 100}
	mut := map[string]func(p *chstore.ListAIExchangesParams){
		"from":     func(p *chstore.ListAIExchangesParams) { p.From = p.From.Add(time.Second) },
		"to":       func(p *chstore.ListAIExchangesParams) { p.To = p.To.Add(time.Second) },
		"limit":    func(p *chstore.ListAIExchangesParams) { p.Limit = 50 },
		"offset":   func(p *chstore.ListAIExchangesParams) { p.Offset = 100 },
		"feedback": func(p *chstore.ListAIExchangesParams) { p.Feedback = "down" },
		"q":        func(p *chstore.ListAIExchangesParams) { p.Q = "a" },
		"user":     func(p *chstore.ListAIExchangesParams) { p.User = "a" },
		"tier":     func(p *chstore.ListAIExchangesParams) { p.Tier = "loop" },
	}
	k0 := aiExchangesCacheKey(base)
	if k0 != aiExchangesCacheKey(base) {
		t.Fatal("anahtar kararlı değil")
	}
	seen := map[string]string{k0: "base"}
	for name, m := range mut {
		p := base
		m(&p)
		k := aiExchangesCacheKey(p)
		if prev, dup := seen[k]; dup {
			t.Errorf("%s anahtarı %s ile çakışıyor: %s", name, prev, k)
		}
		seen[k] = name
	}
	// Serbest metinler ayrı özetlenir: q="a|b",user="" ≠ q="a",user="|b".
	a, b := base, base
	a.Q, a.User = "ab", ""
	b.Q, b.User = "a", "b"
	if aiExchangesCacheKey(a) == aiExchangesCacheKey(b) {
		t.Error("q/user sınırı anahtarda kayboluyor")
	}
}

func TestAIExchangesHasMore(t *testing.T) {
	cases := []struct {
		off, lim int
		total    uint64
		want     bool
	}{
		{0, 100, 0, false}, {0, 100, 100, false}, {0, 100, 101, true},
		{100, 100, 250, true}, {200, 100, 250, false},
	}
	for _, c := range cases {
		p := chstore.ListAIExchangesParams{Offset: c.off, Limit: c.lim}
		if got := aiExchangesHasMore(p, c.total); got != c.want {
			t.Errorf("off=%d lim=%d total=%d: %v", c.off, c.lim, c.total, got)
		}
	}
}

func TestWriteAIExchangesCSV(t *testing.T) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write(aiExchangesCSVHeader)
	writeAIExchangesCSV(cw, []chstore.AIExchange{{
		ExchangeID: "ex-1", CreatedAt: time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC).UnixNano(),
		Tier: "loop", Route: "r", Deep: true, Status: "ok", DurationMs: 1200,
		Models: []string{"m1", "m2"}, LLMCalls: 2, InputTokens: 10, OutputTokens: 5, Feedback: -1,
		UserID: "u-1", Question: "=HYPERLINK(\"x\")", Answer: "satır1\nsatır, 2", ErrorMsg: "",
	}})
	cw.Flush()
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || len(recs[1]) != len(aiExchangesCSVHeader) {
		t.Fatalf("kayıtlar: %v", recs)
	}
	row := map[string]string{}
	for i, h := range aiExchangesCSVHeader {
		row[h] = recs[1][i]
	}
	want := map[string]string{
		"time": "2026-10-30T09:00:00Z", "exchange_id": "ex-1", "tier": "loop", "deep": "true",
		"duration_ms": "1200", "models": "m1 m2", "llm_calls": "2", "feedback": "-1",
		"user": "u-1", "question": "'=HYPERLINK(\"x\")", "answer": "satır1\nsatır, 2",
	}
	for k, v := range want {
		if row[k] != v {
			t.Errorf("%s = %q, beklenen %q", k, row[k], v)
		}
	}
}

func TestCSVSafeCell(t *testing.T) {
	for in, want := range map[string]string{
		"": "", "abc": "abc", "=1+1": "'=1+1", "+x": "'+x", "-x": "'-x", "@x": "'@x", "\tx": "'\tx", "ğ=": "ğ=",
	} {
		if got := csvSafeCell(in); got != want {
			t.Errorf("csvSafeCell(%q) = %q, beklenen %q", in, got, want)
		}
	}
}

// Yeni uçlar admin kapılı, ai_routes.go'da (api.go büyümez).
func TestAIExchangesRoutesGated(t *testing.T) {
	src := readSrc(t, "ai_routes.go")
	for _, want := range []string{
		`("GET /api/ai/exchanges/users", auth.RequireRole(auth.RoleAdmin, s.listAIExchangeUsers))`,
		`("GET /api/ai/exchanges/csv", auth.RequireRole(auth.RoleAdmin, s.exportAIExchangesCSV))`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("rota eksik ya da kapısız: %s", want)
		}
	}
	if strings.Contains(readSrc(t, "api.go"), "/api/ai/exchanges/") {
		t.Error("api.go'ya rota eklenmemeli")
	}
	// Liste ucu önbellekten geçer (serveCached), anahtar tek yardımcıdan.
	h := readSrc(t, "ai_exchanges.go")
	if !strings.Contains(h, "s.serveCached(w, r, aiExchangesCacheKey(p)") {
		t.Error("listAIExchanges serveCached + aiExchangesCacheKey kullanmalı")
	}
}
