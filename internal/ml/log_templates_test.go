package ml

// v0.10.1755 — log dilim 2: aile ifadesi (sabit en uzun dizi), aile seçimi
// (izlenen önce, tavan, aynı Drain ailesi yeni aile açmaz), nadir şablon kuralı.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestTemplatePhrase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"user <*> failed to connect to database pool <*>", "failed to connect to database pool"},
		{"<*> <*> ok", ""},
		{"a b c", ""},
		{"one two three four five six seven eight nine ten", "one two three four five six seven eight"},
		{"id=<*> timeout after retry", "timeout after retry"}, {"id=<*> ok go", ""},
		{"payment gateway returned http <*>", "payment gateway returned http"},
	}
	for _, c := range cases {
		if got := templatePhrase(c.in); got != c.want {
			t.Errorf("templatePhrase(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestSelectFamilies(t *testing.T) {
	tracked := map[string]tplFamily{}
	old := tplFamily{service: "pay", phrase: "failed to connect to database pool", sample: "user <*> failed to connect to database pool <*>"}
	tracked[old.part()+"\x00pay"] = old
	cands := []chstore.LogTemplate{
		{Template: "user <*> failed to connect to database pool <*>", Services: []string{"pay", "bill"}},
		{Template: "payment gateway returned http <*>", Services: []string{"pay"}},
		{Template: "<*>", Services: []string{"pay"}},
	}
	got := selectFamilies(tracked, cands, 100)
	if len(got) != 2 || got[0].service != "bill" || got[1].phrase != "payment gateway returned http" {
		t.Fatalf("got %+v", got)
	}
	if got := selectFamilies(tracked, cands, 2); len(got) != 1 {
		t.Fatalf("tavan: got %+v", got)
	}
}

func TestRareJudge(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	zeros := make([]float64, cfg.MinBaselineSamples)
	if !rareJudge([]float64{6, 9}, zeros, cfg) {
		t.Fatal("sıfır taban + dwell ≥ eşik → nadir")
	}
	if rareJudge([]float64{6, 2}, zeros, cfg) {
		t.Fatal("dwell kovası eşik altında → nadir değil")
	}
	if rareJudge([]float64{6, 9}, zeros[:3], cfg) {
		t.Fatal("eksik taban → sessiz")
	}
	withHit := append(append([]float64{}, zeros...), 1)
	if rareJudge([]float64{6, 9}, withHit, cfg) {
		t.Fatal("tabanda görülmüş şablon nadir değil")
	}
	if r, ok := rulesFor("log", "tpl|x y z", cfg); !ok || r.detector != detTemplateSpike {
		t.Fatal("backtest tpl kuralı")
	}
	if p := eventPart(chstore.MLEvent{Signal: "log", Detector: detRareTemplate, Code: "x y z"}); p != "tpl|x y z" {
		t.Fatalf("eventPart=%q", p)
	}
}
