package briefing

// briefing_test.go — Login Briefing Faz 1 saf çekirdeği (briefing-audit
// "Tasarım önerisi"). Nötr fikstür: payments-core, orders-api.

import (
	"strings"
	"testing"
)

func TestNormalizeClampsAndDefaults(t *testing.T) {
	c := Config{WindowS: 99999, RefreshIntervalS: 10, TTLS: 60, Envs: []string{" prod ", "prod", "", "a", "b", "c"},
		Thresholds: Thresholds{ErrRateX: 0.5, LatencyX: 0, MinSpans: 5}}.Normalize()
	if c.WindowS != MaxWindowS || c.RefreshIntervalS != MinRefreshS || c.TTLS != MinRefreshS {
		t.Fatalf("pencere/yenileme/ttl: %+v", c)
	}
	if c.Thresholds.ErrRateX != 1.1 || c.Thresholds.LatencyX != 1.5 || c.Thresholds.MinSpans != 10 {
		t.Fatalf("eşikler: %+v", c.Thresholds)
	}
	if strings.Join(c.Envs, ",") != "prod,,a,b" {
		t.Fatalf("envs %q", c.Envs)
	}
	if got := (Config{RefreshIntervalS: 900, TTLS: 300}).Normalize(); got.TTLS != 900 {
		t.Fatalf("ttl refresh'ten kısa: %d", got.TTLS)
	}
	if got := (Config{}).Normalize(); len(got.Envs) != 1 || got.Envs[0] != "" {
		t.Fatalf("boş envs → [\"\"]: %q", got.Envs)
	}
}

func TestParseConfig(t *testing.T) {
	for name, c := range map[string]struct {
		raw     string
		enabled bool
		llm     bool
		envs    string
		window  int
	}{
		"boş":            {"", false, true, ",prod", DefaultWindowS},
		"bozuk":          {"{", false, true, ",prod", DefaultWindowS},
		"açık":           {`{"enabled":true}`, true, true, ",prod", DefaultWindowS},
		"llm kapalı":     {`{"enabled":true,"llm_phrasing":false}`, true, false, ",prod", DefaultWindowS},
		"envs açık":      {`{"envs":["prod"]}`, false, true, "prod", DefaultWindowS},
		"pencere 1 sa":   {`{"window_s":3600}`, false, true, ",prod", 3600},
		"pencere tabanı": {`{"window_s":60}`, false, true, ",prod", MinWindowS},
	} {
		got := ParseConfig([]byte(c.raw))
		if got.Enabled != c.enabled || got.LLMPhrasing != c.llm || strings.Join(got.Envs, ",") != c.envs || got.WindowS != c.window {
			t.Errorf("%s: %+v", name, got)
		}
	}
}

func TestREDFindings(t *testing.T) {
	th := DefaultThresholds()
	kinds := func(fs []Finding) string {
		var ks []string
		for _, f := range fs {
			ks = append(ks, f.Service+":"+f.Kind)
		}
		return strings.Join(ks, ",")
	}
	for name, c := range map[string]struct {
		cur, prev []REDRow
		want      string
	}{
		"hata 3× → err_up":               {[]REDRow{{"orders-api", 1000, 30, 100}}, []REDRow{{"orders-api", 1000, 10, 100}}, "orders-api:err_up"},
		"hata 1.5× → yok":                {[]REDRow{{"orders-api", 1000, 15, 100}}, []REDRow{{"orders-api", 1000, 10, 100}}, ""},
		"mutlak taban altı (0.5%) → yok": {[]REDRow{{"orders-api", 1000, 5, 100}}, []REDRow{{"orders-api", 1000, 1, 100}}, ""},
		"önceki sıfır hata → err_up":     {[]REDRow{{"orders-api", 1000, 20, 100}}, []REDRow{{"orders-api", 1000, 0, 100}}, "orders-api:err_up"},
		"az trafik → oran bayrağı yok":   {[]REDRow{{"orders-api", 100, 50, 900}}, []REDRow{{"orders-api", 100, 1, 100}}, ""},
		"p99 2× → latency_up":            {[]REDRow{{"orders-api", 1000, 0, 400}}, []REDRow{{"orders-api", 1000, 0, 200}}, "orders-api:latency_up"},
		"trafik %60 düştü":               {[]REDRow{{"orders-api", 400, 0, 100}}, []REDRow{{"orders-api", 1000, 0, 100}}, "orders-api:traffic_down"},
		"servis sustu (cur yok)":         {nil, []REDRow{{"payments-core", 1000, 0, 100}}, "payments-core:traffic_down"},
		"önceki az trafik → düşüş yok":   {nil, []REDRow{{"payments-core", 100, 0, 100}}, ""},
		"sıra ada göre": {[]REDRow{{"payments-core", 1000, 30, 100}, {"orders-api", 1000, 30, 100}},
			[]REDRow{{"payments-core", 1000, 10, 100}, {"orders-api", 1000, 10, 100}}, "orders-api:err_up,payments-core:err_up"},
	} {
		got := REDFindings(c.cur, c.prev, th, "prod", 10800)
		if kinds(got) != c.want {
			t.Errorf("%s: %q, want %q", name, kinds(got), c.want)
		}
		for _, f := range got {
			if !strings.HasPrefix(f.PageURL, "/service?") || !strings.Contains(f.PageURL, "env=prod") || !strings.Contains(f.PageURL, "range=3h") {
				t.Errorf("%s: pivot %q", name, f.PageURL)
			}
		}
	}
}

func TestProblemAndRolloutFindings(t *testing.T) {
	ps := ProblemFindings([]ProblemRow{
		{ID: "p-1", DisplayID: "P-00001", Priority: "P3", Service: "orders-api", Reason: "yavaş", PageURL: "/problems?problem=p-1", SinceUnix: 100},
		{ID: "p-2", Priority: "P1", Service: "payments-core", Reason: "hata", PageURL: "/problems?problem=p-2", SinceUnix: 50},
	})
	ls := Lines(ps)
	if len(ls) != 1 || ls[0].Rows[0].Label != "p-2" || ls[0].Total != 2 || !strings.HasPrefix(ls[0].Text, "P1 · payments-core") {
		t.Fatalf("problem satırı %+v", ls)
	}
	th := DefaultThresholds()
	rs := RolloutFindings([]RolloutRow{
		{Service: "orders-api", FromVersion: "1.2.0", ToVersion: "1.3.0", BeforeSpans: 500, AfterSpans: 500, ErrBeforePct: 0.5, ErrAfterPct: 3},
		{Service: "payments-core", ToVersion: "2.0.0", BeforeSpans: 500, AfterSpans: 500, ErrBeforePct: 1, ErrAfterPct: 1.5}, // +0.5 puan → yok
		{Service: "billing-api", ToVersion: "9", BeforeSpans: 5, AfterSpans: 5, ErrBeforePct: 0, ErrAfterPct: 50},            // az span → yok
	}, th, "", 10800)
	if len(rs) != 1 || rs[0].Label != "1.2.0→1.3.0" || rs[0].Score != 2.5 {
		t.Fatalf("rollout %+v", rs)
	}
}

func TestLinesOrderAndCap(t *testing.T) {
	var fs []Finding
	for _, it := range []string{ItemRollout, ItemRED, ItemProblem} {
		for i := 0; i < 5; i++ {
			fs = append(fs, Finding{Item: it, Service: "orders-api", Label: string(rune('a' + i)), Score: float64(i), Text: it})
		}
	}
	ls := Lines(fs)
	if len(ls) != 3 || ls[0].Item != ItemRED || ls[1].Item != ItemProblem || ls[2].Item != ItemRollout {
		t.Fatalf("sıra %+v", ls)
	}
	if len(ls[0].Rows) != MaxRowsPerLine || ls[0].Total != 5 || ls[0].Rows[0].Score != 4 || !strings.HasSuffix(ls[0].Text, "(+4 daha)") {
		t.Fatalf("tavan %+v", ls[0])
	}
}

func TestCacheKeyHashesAllInputs(t *testing.T) {
	base := CacheKey("acme", []string{"orders-api", "payments-core"}, "prod", 10800, 1_800_000_000)
	if CacheKey("acme", []string{"payments-core", "orders-api", "orders-api"}, "prod", 10800, 1_800_000_100) != base {
		t.Error("küme sırası / tekrar / aynı 5 dk kovası anahtarı değiştirmemeli")
	}
	for name, k := range map[string]string{
		"takım":   CacheKey("other", []string{"orders-api", "payments-core"}, "prod", 10800, 1_800_000_000),
		"küme":    CacheKey("acme", []string{"orders-api", "billing-api"}, "prod", 10800, 1_800_000_000),
		"env":     CacheKey("acme", []string{"orders-api", "payments-core"}, "", 10800, 1_800_000_000),
		"pencere": CacheKey("acme", []string{"orders-api", "payments-core"}, "prod", 3600, 1_800_000_000),
		"kova":    CacheKey("acme", []string{"orders-api", "payments-core"}, "prod", 10800, 1_800_000_300),
	} {
		if k == base {
			t.Errorf("%s anahtarı değiştirmedi", name)
		}
	}
	if ScopeHash([]string{"a"}) == ScopeHash([]string{"b"}) {
		t.Error("uzunluk-yalnız özet (v0.5.187)")
	}
}

func TestAssembleSilenceAndHash(t *testing.T) {
	in := AssembleInput{ID: "01J", Team: "Acme", TeamKey: "acme", WindowS: 10800, Services: []string{"orders-api", "payments-core"}, Trimmed: 120,
		Partial: []string{ItemRollout, ItemRollout}}
	b := Assemble(in)
	if !b.Silence || len(b.Lines) != 0 || b.Services != 2 || b.Trimmed != 120 || !strings.Contains(b.Summary, "122 servisinde") || len(b.Partial) != 1 {
		t.Fatalf("sessizlik %+v", b)
	}
	in.Findings = []Finding{{Item: ItemRED, Kind: "err_up", Service: "orders-api", Now: 3, Prev: 1, Text: "x"}}
	b2 := Assemble(in)
	if b2.Silence || FindingsHash(b2) == FindingsHash(b) {
		t.Fatal("bulgu değişince özet değişmeli")
	}
	in.ID, in.GeneratedAtMs = "01K", 99
	if FindingsHash(Assemble(in)) != FindingsHash(b2) {
		t.Fatal("kimlik/zaman özeti değiştirmemeli")
	}
}

func TestServiceURL(t *testing.T) {
	if got := ServiceURL("orders-api", "http", "", 5400); got != "/service?name=orders-api&range=90m&tab=http" {
		t.Fatalf("%q", got)
	}
}

func TestExceptionFindings(t *testing.T) {
	from := int64(1000)
	rows := []ExceptionRow{
		{ID: "exception:a", Kind: "exception", Service: "orders-api", Title: "NullPointerException", StartedAtUnix: 1500, Priority: "P1"},
		{ID: "httperror:b", Kind: "httperror", Service: "payments-core", Title: "503", Status: "regressed", StartedAtUnix: 10},
		{ID: "exception:c", Kind: "exception", Service: "orders-api", Title: "old", Status: "open", StartedAtUnix: 10}, // eski, açık → değil
		{ID: "problem:d", Kind: "problem", Service: "orders-api", StartedAtUnix: 1500},                                 // aile dışı
	}
	fs := ExceptionFindings(rows, from)
	if len(fs) != 2 || fs[0].Kind != "new" || fs[1].Kind != "regressed" || fs[0].Score <= fs[1].Score {
		t.Fatalf("%+v", fs)
	}
}

func TestLogSlicesAndFindings(t *testing.T) {
	if s := LogSlices(0, 10800); len(s) != 3 || s[2] != [2]int64{7200, 10800} {
		t.Fatalf("%v", s)
	}
	if s := LogSlices(0, 5400); len(s) != 2 || s[1] != [2]int64{3600, 5400} {
		t.Fatalf("kısmi dilim %v", s)
	}
	th := DefaultThresholds()
	for name, c := range map[string]struct {
		cur, prev int64
		want      int
	}{
		"taban altı": {10, 0, 0},
		"yeni":       {50, 0, 1},
		"2× → bulgu": {100, 50, 1},
		"1.5× → yok": {75, 50, 0},
	} {
		if got := LogFindings("acme", c.cur, c.prev, false, th, "/logs"); len(got) != c.want {
			t.Errorf("%s: %d", name, len(got))
		}
	}
	if got := LogFindings("acme", 100, 10, true, th, "/logs"); !strings.Contains(got[0].Text, "≥100") {
		t.Fatalf("alt sınır metni %q", got[0].Text)
	}
}
