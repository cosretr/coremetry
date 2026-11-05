package anomaly

// v0.10.1793 — Davis paritesi FAZ 1.5: eval aracının sözleşmesi (CI'da, etiket
// gerektirmez). Sentetik fikstür (testdata/eval/anomaly_cases.sample.jsonl)
// aracın uçtan uca çalıştığını gösterir; GERÇEK eval seti operatörde — kapı
// sonucu fikstürde "ÖLÇÜLMEDİ (eval seti bekleniyor)" olmak ZORUNDA (8 < 50
// vaka): sentetik setle "GEÇTİ" yazmak, eval geçmeden özneleri açtırırdı.

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func loadSampleEval(t *testing.T) []EvalCase {
	t.Helper()
	f, err := os.Open("testdata/eval/anomaly_cases.sample.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cs, err := ParseEvalCases(f)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestParseEvalCases(t *testing.T) {
	if n := len(loadSampleEval(t)); n != 8 {
		t.Fatalf("fikstür %d vaka, want 8", n)
	}
	bad := map[string]string{
		"id yok":        `{"family":"metric","series":{"buckets":[1],"rates":[1]}}`,
		"aile yok":      `{"id":"a","series":{"buckets":[1],"rates":[1]}}`,
		"pencere bozuk": `{"id":"a","family":"metric","subject":"s","from":10,"to":5}`,
		"özne yok":      `{"id":"a","family":"metric","from":1,"to":5}`,
		"tekrar id":     "{\"id\":\"a\",\"family\":\"m\",\"series\":{\"buckets\":[1],\"rates\":[1]}}\n{\"id\":\"a\",\"family\":\"m\",\"series\":{\"buckets\":[1],\"rates\":[1]}}",
		"bozuk JSON":    `{"id":`,
	}
	for name, in := range bad {
		if _, err := ParseEvalCases(strings.NewReader(in)); err == nil {
			t.Errorf("%s: hata bekleniyordu", name)
		}
	}
	cs, err := ParseEvalCases(strings.NewReader("# yorum\n\n{\"id\":\"x\",\"family\":\"m\",\"subject\":\"s\",\"from\":1,\"to\":2}\n"))
	if err != nil || len(cs) != 1 {
		t.Fatalf("yorum/boş satır: %v %d", err, len(cs))
	}
}

func TestEvalSampleEndToEnd(t *testing.T) {
	cases := loadSampleEval(t)
	outs := RunEval(context.Background(), cases, ChainReplayer{SeriesReplayer{Sens: chstore.DefaultAnomalySensitivity()}})
	got := map[string]EvalOutcome{}
	for _, o := range outs {
		got[o.CaseID] = o
	}
	want := map[string]bool{"m1": true, "m2": false, "m3": false, "m4": true, "e1": true, "e2": false, "b1": false}
	for id, w := range want {
		if o := got[id]; o.Err != "" || o.Opened != w {
			t.Errorf("%s: opened=%v err=%q reason=%q, want %v", id, o.Opened, o.Err, o.Reason, w)
		}
	}
	if got["t1"].Err == "" {
		t.Error("serisiz vaka ölçülemedi sayılmalı (açılmadı DEĞİL)")
	}
	tab := ScoreEval(cases, outs)
	tot := tab.Total
	if tot.Cases != 8 || tot.Unmeasured != 1 || tot.TP != 3 || tot.FP != 0 || tot.FN != 1 || tot.TN != 3 {
		t.Fatalf("toplam: %+v", tot)
	}
	if tot.FPRate != 0 || math.Abs(tot.Recall-0.75) > 1e-9 || tot.EndpointCaught != 1 || tot.EndpointReal != 1 {
		t.Fatalf("oranlar: fp %.3f recall %.3f endpoint %d/%d", tot.FPRate, tot.Recall, tot.EndpointCaught, tot.EndpointReal)
	}
	// FAZ 0.5 kolonu yalnız ölçülenlerden (t1 dışarıda — aynı payda):
	// m1 TP, m2 TN, m3 FP, m4 TP, e1 FN, e2 TN, b1 FN.
	if tot.P05Cases != 7 || tot.P05TP != 2 || tot.P05FP != 1 || tot.P05FN != 2 || tot.P05TN != 2 {
		t.Fatalf("FAZ 0.5 kolonu: %+v", tot)
	}
	if len(tab.Rows) != 4 || tab.Rows[0].Family != "behavior_change" {
		t.Fatalf("aileler: %+v", tab.Rows)
	}
	v, why := EvalGate(tab)
	if v != "ÖLÇÜLMEDİ (eval seti bekleniyor)" || len(why) == 0 {
		t.Fatalf("sentetik fikstürde kapı %q %v — ÖLÇÜLMEDİ olmalı", v, why)
	}
	md := RenderEvalMarkdown(tab)
	for _, w := range []string{"| TOPLAM | 8 | 1 | 3 | 0 | 1 | 3 | 0.000 | 0.750 |", "Kapı: **ÖLÇÜLMEDİ", "| 1/1 |"} {
		if !strings.Contains(md, w) {
			t.Errorf("markdown'da yok %q:\n%s", w, md)
		}
	}
}

func TestEvalGateVerdicts(t *testing.T) {
	mk := func(tp, fp, fn, tn, ptp, pfp, pfn, ptn, ep int) EvalTable {
		r := finishEvalRow(EvalRow{Family: "TOPLAM", Cases: tp + fp + fn + tn, TP: tp, FP: fp, FN: fn, TN: tn,
			P05TP: ptp, P05FP: pfp, P05FN: pfn, P05TN: ptn, P05Cases: ptp + pfp + pfn + ptn, EndpointCaught: ep, EndpointReal: ep})
		return EvalTable{Total: r}
	}
	cases := []struct {
		name string
		t    EvalTable
		want string
	}{
		{"geçer", mk(30, 2, 0, 28, 30, 5, 0, 25, 1), "GEÇTİ"},
		{"FP kötüleşti", mk(30, 6, 0, 24, 30, 5, 0, 25, 1), "KALDI"},
		{"recall düştü", mk(29, 2, 1, 28, 30, 5, 0, 25, 1), "KALDI"},
		{"endpoint yok", mk(30, 2, 0, 28, 30, 5, 0, 25, 0), "KALDI"},
		{"az vaka", mk(10, 1, 0, 9, 10, 2, 0, 8, 1), "ÖLÇÜLMEDİ (eval seti bekleniyor)"},
		{"tek etiket", mk(60, 0, 0, 0, 60, 0, 0, 0, 1), "ÖLÇÜLMEDİ (eval seti bekleniyor)"},
		{"kıyas yok", mk(30, 2, 0, 28, 0, 0, 0, 0, 1), "ÖLÇÜLMEDİ (eval seti bekleniyor)"},
	}
	for _, c := range cases {
		if v, why := EvalGate(c.t); v != c.want {
			t.Errorf("%s: %q %v, want %q", c.name, v, why, c.want)
		}
	}
}
