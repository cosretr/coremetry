package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ai_evalset_core_test.go — v0.10.940: Değerlendirme panelinin SAF
// çekirdeği (ai_evalset_core.go). Taşınan skorlayıcıların kendi testleri
// evalset_fixture_test.go'da, değişmeden; bunlar yeni parçaları pinler:
// üretim etiketi tablosu, ortak vaka yolu, gömülü yükleyici, kırpma,
// yüzey özeti, terk türetmesi, liste birleştirme, kıyas.

// TestEvalsetProductionLabelTable — tablo PİN'i. Etiket profil
// yönlendirmesini belirler: yanlış etiket, panelin üretimden BAŞKA bir
// modeli ölçmesi demek ve hiçbir şey kırmızı yanmaz.
func TestEvalsetProductionLabelTable(t *testing.T) {
	want := map[string]string{
		"Trace": "explain-trace", "Span": "explain-span",
		"Problem": "explain-problem", "Exception": "explain-exception",
		"Incident": "explain-incident", "Anomaly": "explain-anomaly",
		"ServiceHealth": "explain-service", "Runbook": "runbook",
		"CompareTraces": "compare-traces", "DeployImpact": "deploy-impact",
		"SLOBurn": "explain-slo", "SlowQuery": "insight-slow-query",
		"NLToQuery": "nl-to-query", "CHQueryOptimize": "ch-optimize",
		"RCAVerdict": "rootcause-verdict", "ServiceCharts": "explain-charts",
		"GeneralChat": "chat-general", "Chat": "chat", "IntentClassify": "chat-intent",
	}
	if len(evalProductionLabels) != len(want) {
		t.Fatalf("tablo %d girdi, pin %d", len(evalProductionLabels), len(want))
	}
	for surface, label := range want {
		if got := evalProductionLabel(surface); got != label {
			t.Errorf("%s → %q, want %q", surface, got, label)
		}
		if _, ok := evalSystemPrompt(surface); !ok {
			t.Errorf("%s tabloda ama sistem promptu çözülmüyor", surface)
		}
		// Export eşlemesiyle geri dönüşüm: 👎 satırı hangi yüzeye düşüyorsa
		// panel o yüzeyi aynı etiketle koşar. Tek bilinçli istisna SlowQuery
		// (bugünkü tek çağıran insight kartı; export eski adları tanıyor).
		if surface != "SlowQuery" && evalSurfaceFromLabel(label) != surface {
			t.Errorf("%s: evalSurfaceFromLabel(%q) = %q — export ve panel ayrıştı", surface, label, evalSurfaceFromLabel(label))
		}
	}
	if aiSurfaceFromPath("/api/insight/slow-query/abc") != "insight-slow-query" {
		t.Error("SlowQuery etiketi insight yolunun ürettiği etiket olmalı")
	}
	if evalProductionLabel("Yok") != "" {
		t.Error("bilinmeyen yüzey boş etiket (→ varsayılan profil)")
	}
}

// fakeCall — runEvalsetCase'e verilen çağrıyı kaydeder.
type fakeCall struct {
	n       int
	surface string
	system  string
	user    string
	schema  map[string]any
	json    bool
	answer  string
	err     error
}

func (f *fakeCall) fn(_ context.Context, surface, system, user string, schema map[string]any, json bool) (string, error) {
	f.n++
	f.surface, f.system, f.user, f.schema, f.json = surface, system, user, schema, json
	return f.answer, f.err
}

// TestRunEvalsetCaseSharedPath — CLI ve sunucunun ortak vaka yolu: JSON
// eşitliği (üretim şemaları), fikstür hatası koşuyu düşürmez, atlama
// çağrı yapmaz, hata "error:" ihlali.
func TestRunEvalsetCaseSharedPath(t *testing.T) {
	zero := 0
	hyp := `{"service":"checkout","topSuspect":"payments","topScore":0.9,"confidence":0.8,"candidates":[{"service":"payments","score":0.9,"hops":1,"reason":"deploy"}]}`
	cases := []struct {
		name       string
		c          evalCase
		answer     string
		err        error
		wantCalls  int
		wantJSON   bool
		wantSchema bool
		wantFail   string // ilk ihlalin öneki ("" = temiz)
		wantSkip   bool
	}{
		{"düz metin", evalCase{ID: "p", Surface: "Problem", User: "checkout", Expect: evalExpect{MustContain: []string{"2.14.0"}, KnownEntities: []string{"checkout"}, MaxUnknownEntities: &zero}},
			"checkout 2.14.0", nil, 1, false, false, "", false},
		{"niyet şemalı", evalCase{ID: "i", Surface: "IntentClassify", User: "checkout nasıl?", Expect: evalExpect{Intent: "service_health", KnownEntities: []string{"checkout"}}},
			`{"intent":"service_health","service":"checkout"}`, nil, 1, true, true, "", false},
		{"NLToQuery üretim şeması", evalCase{ID: "n", Surface: "NLToQuery", User: "q", Expect: evalExpect{MustContain: []string{"x"}}},
			`{"x":1}`, nil, 1, true, true, "", false},
		{"CHQueryOptimize üretim şeması", evalCase{ID: "o", Surface: "CHQueryOptimize", User: "SELECT 1", Expect: evalExpect{MustContain: []string{"x"}}},
			`{"x":1}`, nil, 1, true, true, "", false},
		{"RCA hipotezden şema", evalCase{ID: "r", Surface: "RCAVerdict", Hypothesis: json.RawMessage(hyp), Expect: evalExpect{Verdicts: []string{"insufficient_evidence"}}},
			`{"verdict":"insufficient_evidence","root_cause":{"evidence":[]}}`, nil, 1, true, true, "", false},
		{"sağlayıcı hatası", evalCase{ID: "e", Surface: "Problem", User: "u", Expect: evalExpect{MustContain: []string{"x"}}},
			"", errors.New("boom"), 1, false, false, "error: boom", false},
		{"bilinmeyen yüzey koşuyu düşürmez", evalCase{ID: "u", Surface: "Hayalet", User: "u"},
			"", nil, 0, false, false, "fixture: surface", false},
		{"bozuk hipotez koşuyu düşürmez", evalCase{ID: "b", Surface: "RCAVerdict", Hypothesis: json.RawMessage(`{"service":`)},
			"", nil, 0, false, false, "fixture: hypothesis", false},
		{"kırpık export vakası atlanır", evalCase{ID: "s", Surface: "Problem", Prompt: "p", Provenance: &evalProvenance{Truncated: true}},
			"", nil, 0, false, false, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeCall{answer: c.answer, err: c.err}
			o := runEvalsetCase(context.Background(), f.fn, c.c)
			if f.n != c.wantCalls {
				t.Fatalf("çağrı %d, want %d", f.n, c.wantCalls)
			}
			if o.Skipped != c.wantSkip {
				t.Fatalf("skipped %v", o.Skipped)
			}
			if c.wantCalls > 0 {
				if f.surface != c.c.Surface || f.json != c.wantJSON || (f.schema != nil) != c.wantSchema {
					t.Fatalf("çağrı: surface=%q json=%v schema=%v", f.surface, f.json, f.schema != nil)
				}
			}
			switch {
			case c.wantFail == "" && len(o.Fails) != 0:
				t.Fatalf("temiz vaka kızardı: %v", o.Fails)
			case c.wantFail != "" && (len(o.Fails) == 0 || !strings.HasPrefix(o.Fails[0], c.wantFail)):
				t.Fatalf("ihlal %q bekleniyordu: %v", c.wantFail, o.Fails)
			}
			if strings.HasPrefix(c.wantFail, "fixture:") && !o.Fixture {
				t.Fatal("fikstür hatası işaretlenmeli")
			}
		})
	}
	// Şema İÇERİĞİ üretiminkiyle aynı olmalı (ad değil, gövde).
	f := &fakeCall{answer: "{}"}
	runEvalsetCase(context.Background(), f.fn, evalCase{ID: "i", Surface: "IntentClassify", User: "q", Expect: evalExpect{Intent: "none"}})
	got, _ := json.Marshal(f.schema)
	want, _ := json.Marshal(intentClassifySchema())
	if string(got) != string(want) {
		t.Fatal("IntentClassify şeması üretimin intentClassifySchema()'sı değil")
	}
	// RCA: kurulan prompt modele gider (fikstürde user yok).
	f = &fakeCall{answer: "{}"}
	o := runEvalsetCase(context.Background(), f.fn, evalCase{ID: "r", Surface: "RCAVerdict", Hypothesis: json.RawMessage(hyp), Expect: evalExpect{Verdicts: []string{"x"}}})
	if f.user == "" || o.User != f.user || !o.RCA {
		t.Fatalf("RCA promptu: %q / %q", f.user, o.User)
	}
}

// TestEvalsetLoaderFS — gömülü yükleyicinin doğrulama metinleri eski disk
// yükleyicisiyle aynı; dosya sırası; expect harfiyen.
func TestEvalsetLoaderFS(t *testing.T) {
	good := fstest.MapFS{
		"b.json":    {Data: []byte(`{"schema":"coremetry.evalset/1","cases":[{"id":"b1","surface":"Problem","why":"w","user":"u","expect":{"mustContain":["x"],"yeniAnahtar":1}}]}`)},
		"a.json":    {Data: []byte(`{"schema":"coremetry.evalset/1","cases":[{"id":"a1","surface":"Problem","why":"w","user":"u","expect":{"maxUnknownEntities":0}}]}`)},
		"README.md": {Data: []byte("# yok sayılır")},
	}
	cases, err := loadEvalsetFS(good)
	if err != nil || len(cases) != 2 || cases[0].ID != "a1" || cases[0].file != "a.json" {
		t.Fatalf("sıra/dosya: %+v %v", cases, err)
	}
	if !strings.Contains(string(evalExpectJSON(cases[1])), "yeniAnahtar") {
		t.Fatalf("expect harfiyen taşınmalı: %s", evalExpectJSON(cases[1]))
	}
	if got := string(evalExpectJSON(evalCase{Expect: evalExpect{Intent: "none"}})); got != `{"intent":"none"}` {
		t.Fatalf("elle kurulan vaka struct'tan: %s", got)
	}
	for _, c := range []struct {
		name string
		fs   fstest.MapFS
		want string
	}{
		{"şema", fstest.MapFS{"x.json": {Data: []byte(`{"schema":"v0","cases":[]}`)}}, `x.json: schema "v0", want "coremetry.evalset/1"`},
		{"JSON", fstest.MapFS{"y.json": {Data: []byte(`{"schema":`)}}, "y.json: "},
	} {
		if _, err := loadEvalsetFS(c.fs); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("%s: %v, want önek %q", c.name, err, c.want)
		}
	}
	// Gemiye binen küme: gömülü FS gerçekten dolu.
	if all, err := loadEvalsetCases(); err != nil || len(all) < 20 || len(all[0].expectRaw) == 0 {
		t.Fatalf("gömülü küme: %d %v", len(all), err)
	}
}

// TestEvalsetTruncateRuneSafe — 8/16 KiB tavanları rune sınırında keser;
// çok baytlı Türkçe harf ortadan bölünmez.
func TestEvalsetTruncateRuneSafe(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		max     int
		wantLen int
		wantCut bool
	}{
		{"tavan altı", "abc", 8, 3, false},
		{"tam tavan", "abcd", 4, 4, false},
		{"ascii kesim", "abcdef", 4, 4, true},
		{"çok baytlı sınır geri çekilir", "aşğ", 4, 3, true}, // a(1) ş(2) ğ(2): 4. bayt ğ'nin ortası
		{"çok baytlı tam sınır", "aşğ", 3, 3, true},
	}
	for _, c := range cases {
		got, cut := evalTruncate(c.in, c.max)
		if len(got) != c.wantLen || cut != c.wantCut || !utf8.ValidString(got) {
			t.Errorf("%s: %q (%d) cut=%v", c.name, got, len(got), cut)
		}
	}
	big := strings.Repeat("ğ", 20<<10) // 40 KiB
	res := evalCaseResultFrom(evalCase{ID: "x", Surface: "Problem"}, evalCaseOutcome{User: big, Answer: big}, "p", "m")
	if !res.InputTruncated || !res.AnswerTruncated || len(res.Input) > evalInputCap || len(res.Answer) > evalAnswerCap ||
		!utf8.ValidString(res.Input) || !utf8.ValidString(res.Answer) || len(res.Answer) < evalAnswerCap-1 {
		t.Fatalf("vaka kırpma: in=%d(%v) ans=%d(%v)", len(res.Input), res.InputTruncated, len(res.Answer), res.AnswerTruncated)
	}
	if res.Fails == nil || res.Expect == nil {
		t.Fatal("fails/expect null olmamalı")
	}
}

// TestEvalsetSurfaceSummaries — yüzey özeti: sayım, sıra, gecikme ortalaması
// (atlananlar hariç) ve unknownEntities null ↔ toplam ayrımı (PLANDAN).
func TestEvalsetSurfaceSummaries(t *testing.T) {
	one := 1
	plan := []evalCase{
		{ID: "p1", Surface: "Problem", Expect: evalExpect{MaxUnknownEntities: &one}},
		{ID: "p2", Surface: "Problem"}, // tavansız: toplamı ETKİLEMEZ
		{ID: "p3", Surface: "Problem", Expect: evalExpect{MaxUnknownEntities: &one}},
		{ID: "i1", Surface: "IntentClassify"},
		{ID: "i2", Surface: "IntentClassify"},
		{ID: "i3", Surface: "IntentClassify"},
		{ID: "a1", Surface: "Anomaly"},
		{ID: "s1", Surface: "SLOBurn", Expect: evalExpect{MaxUnknownEntities: &one}},
	}
	results := []evalCaseResult{
		{ID: "p1", Surface: "Problem", OK: true, LatencyMs: 100, UnknownEntities: 2},
		{ID: "p2", Surface: "Problem", OK: false, LatencyMs: 300, UnknownEntities: 7},
		{ID: "i1", Surface: "IntentClassify", OK: true, LatencyMs: 50},
		{ID: "i2", Surface: "IntentClassify", Skipped: true, LatencyMs: 0},
	}
	got := evalSurfaceSummaries(plan, results)
	order := []string{}
	for _, s := range got {
		order = append(order, s.Surface)
	}
	if strings.Join(order, ",") != "IntentClassify,Problem,Anomaly,SLOBurn" {
		t.Fatalf("sıra (sayı azalan, ad artan): %v", order)
	}
	byName := map[string]evalSurfaceSummary{}
	for _, s := range got {
		byName[s.Surface] = s
	}
	p := byName["Problem"]
	if p.Cases != 3 || p.Pass != 1 || p.Fail != 1 || p.AvgLatencyMs != 200 || p.UnknownEntities == nil || *p.UnknownEntities != 2 {
		t.Fatalf("Problem: %+v unknown=%v", p, p.UnknownEntities)
	}
	ic := byName["IntentClassify"]
	if ic.UnknownEntities != nil || ic.Skipped != 1 || ic.Pass != 1 || ic.AvgLatencyMs != 50 {
		t.Fatalf("IntentClassify (ölçülmüyor → null, atlanan ortalamaya girmez): %+v", ic)
	}
	if s := byName["SLOBurn"]; s.UnknownEntities == nil || *s.UnknownEntities != 0 {
		t.Fatal("tavanlı ama henüz koşmamış yüzey 0 (null değil) — koşu ortasında salınmasın")
	}
	b, _ := json.Marshal(byName["Anomaly"])
	if !strings.Contains(string(b), `"unknownEntities":null`) {
		t.Fatalf("null JSON'da açıkça null: %s", b)
	}
	if evalSurfaceSummaries(nil, nil) == nil {
		t.Fatal("boş plan [] döner")
	}
}

// TestEvalRunDerivedStatus — "abandoned" yalnız okumada türetilir: sahipsiz
// + 15 dk sessiz "running". Çapraz-pod 409 kapısı tümleyeni.
func TestEvalRunDerivedStatus(t *testing.T) {
	now := time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		status string
		age    time.Duration
		owned  bool
		want   string
		blocks bool
	}{
		{"taze running başkasının", evalStatusRunning, time.Minute, false, evalStatusRunning, true},
		{"sınırda running", evalStatusRunning, evalRunStaleAfter, false, evalStatusRunning, true},
		{"bayat running sahipsiz → terk", evalStatusRunning, evalRunStaleAfter + time.Second, false, evalStatusAbandoned, false},
		{"bayat running bu süreçte → sürüyor", evalStatusRunning, time.Hour, true, evalStatusRunning, false},
		{"bitmiş koşu yaşlanmaz", evalStatusDone, 48 * time.Hour, false, evalStatusDone, false},
		{"iptal", evalStatusCancelled, time.Hour, false, evalStatusCancelled, false},
	}
	for _, c := range cases {
		upd := now.Add(-c.age)
		if got := evalDerivedStatus(c.status, upd, now, c.owned); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if c.owned {
			continue // sahiplik süreç-içi kapıda; CH kapısı sahipliği bilmez
		}
		if got := evalRunBlocksStart(chstore.EvalRun{Status: c.status, UpdatedAt: upd}, now); got != c.blocks {
			t.Errorf("%s: blocks=%v, want %v", c.name, got, c.blocks)
		}
	}
}

// TestEvalMergeRunList — bellekteki koşu başa, aynı kimlikli CH satırı düşer,
// kalanlar terk türetmesinden geçer, tavan uygulanır. v0.10.940: son yazımı
// düşen iş bayat CH satırının YERİNE, zamanına oturur (terk türetilmez).
func TestEvalMergeRunList(t *testing.T) {
	now := time.Now()
	ts := func(d time.Duration) string { return evalTimeString(now.Add(d)) }
	rows := []chstore.EvalRun{
		{ID: "ev-own", Status: evalStatusRunning, StartedAt: now.Add(-2 * time.Minute), UpdatedAt: now.Add(-time.Minute)},
		{ID: "ev-dead", Status: evalStatusRunning, StartedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "ev-old", Status: evalStatusDone, StartedAt: now.Add(-4 * time.Hour), UpdatedAt: now.Add(-4 * time.Hour)},
	}
	own := []evalRunSummary{{ID: "ev-own", Status: evalStatusRunning, Done: 9, StartedAt: ts(-2 * time.Minute)}}
	got := evalMergeRunList(rows, own, now, 20)
	if len(got) != 3 || got[0].ID != "ev-own" || got[0].Done != 9 || got[1].Status != evalStatusAbandoned || got[2].Status != evalStatusDone {
		t.Fatalf("birleştirme: %+v", got)
	}
	if got := evalMergeRunList(rows, nil, now, 2); len(got) != 2 || got[0].ID != "ev-own" {
		t.Fatalf("tavan / sahipsiz: %+v", got)
	}

	// Koşan iş (başlangıç yazımı da düşmüş: CH'de yok) + son yazımı düşen iş
	// (CH'de 20 dk önceki bayat "running") + arada başka pod'un koşusu.
	rows = []chstore.EvalRun{
		{ID: "ev-other", Status: evalStatusDone, StartedAt: now.Add(-5 * time.Minute), UpdatedAt: now.Add(-4 * time.Minute)},
		{ID: "ev-last", Status: evalStatusRunning, StartedAt: now.Add(-30 * time.Minute), UpdatedAt: now.Add(-20 * time.Minute)},
		{ID: "ev-old", Status: evalStatusDone, StartedAt: now.Add(-4 * time.Hour), UpdatedAt: now.Add(-4 * time.Hour)},
	}
	own = []evalRunSummary{
		{ID: "ev-run", Status: evalStatusRunning, StartedAt: ts(-time.Second)},
		{ID: "ev-last", Status: evalStatusDone, Done: 4, StartedAt: ts(-30 * time.Minute)},
	}
	got = evalMergeRunList(rows, own, now, 20)
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID+":"+r.Status)
	}
	if strings.Join(ids, ",") != "ev-run:running,ev-other:done,ev-last:done,ev-old:done" {
		t.Fatalf("düşmüş son yazım bayat satırın yerine, zamanına oturmalı: %v", ids)
	}
	if got := evalMergeRunList(nil, nil, now, 20); got == nil || len(got) != 0 {
		t.Fatal("boş liste [] döner (null değil)")
	}
	if s := evalSummaryFromRow(chstore.EvalRun{ID: "x"}, now, false); s.Surfaces == nil || s.BySurface == nil || s.FinishedAt != "" {
		t.Fatalf("özet boş alanları: %+v", s)
	}
}

// TestEvalSelectCases — boş/yinelenen ad yok sayılır, bilinmeyen ad hata.
func TestEvalSelectCases(t *testing.T) {
	all := []evalCase{{ID: "1", Surface: "A"}, {ID: "2", Surface: "B"}, {ID: "3", Surface: "A"}}
	got, surf, err := evalSelectCases(all, nil)
	if err != nil || len(got) != 3 || surf == nil || len(surf) != 0 {
		t.Fatalf("tümü: %v %v %v", got, surf, err)
	}
	got, surf, err = evalSelectCases(all, []string{"A", " ", "A"})
	if err != nil || len(got) != 2 || strings.Join(surf, ",") != "A" {
		t.Fatalf("alt küme: %v %v %v", got, surf, err)
	}
	if _, _, err := evalSelectCases(all, []string{"A", "Trace"}); err == nil || !strings.Contains(err.Error(), "Trace") {
		t.Fatalf("kümede vakası olmayan yüzey hata olmalı: %v", err)
	}
}

// TestEvalCompareRuns — evalrubric.Diff'in panel hâli: yüzey adları eklenir,
// atlanan vaka kıyasa girmez, boş listeler [] (null değil), not Diff'ten.
func TestEvalCompareRuns(t *testing.T) {
	mk := func(id, model, pv string, cases []evalCaseResult) chstore.EvalRun {
		b, _ := json.Marshal(cases)
		return chstore.EvalRun{ID: id, Model: model, PromptVersion: pv, Status: evalStatusDone, Total: uint32(len(cases)), Cases: string(b), StartedAt: time.Unix(1_700_000_000, 0)}
	}
	base := mk("a", "qwen", "pv1", []evalCaseResult{
		{ID: "c1", Surface: "Problem", OK: true, RubricTotal: 0.9},
		{ID: "c2", Surface: "SLOBurn", OK: false, RubricTotal: 0.4},
		{ID: "c3", Surface: "Anomaly", Skipped: true},
		{ID: "gone", Surface: "Runbook", OK: true, RubricTotal: 1},
	})
	head := mk("b", "qwen", "pv2", []evalCaseResult{
		{ID: "c1", Surface: "Problem", OK: false, RubricTotal: 0.5},
		{ID: "c2", Surface: "SLOBurn", OK: true, RubricTotal: 0.8},
		{ID: "new", Surface: "Incident", OK: true, RubricTotal: 1},
	})
	cmp := evalCompareRuns(base, head)
	if !cmp.Comparable || cmp.Note != "" || cmp.Base.ID != "a" || cmp.Head.ID != "b" {
		t.Fatalf("başlık: %+v", cmp)
	}
	if len(cmp.NewlyFailing) != 1 || cmp.NewlyFailing[0] != (evalCompareCase{ID: "c1", Surface: "Problem"}) {
		t.Fatalf("newlyFailing: %+v", cmp.NewlyFailing)
	}
	if len(cmp.NewlyPassing) != 1 || cmp.NewlyPassing[0].Surface != "SLOBurn" {
		t.Fatalf("newlyPassing: %+v", cmp.NewlyPassing)
	}
	if len(cmp.Regressed) != 1 || cmp.Regressed[0].Before != 0.9 || cmp.Regressed[0].After != 0.5 || len(cmp.Improved) != 1 {
		t.Fatalf("regressed/improved: %+v %+v", cmp.Regressed, cmp.Improved)
	}
	if strings.Join(cmp.OnlyInBase, ",") != "gone" || strings.Join(cmp.OnlyInHead, ",") != "new" {
		t.Fatalf("yalnız birinde (atlanan c3 kıyasa GİRMEZ): %v %v", cmp.OnlyInBase, cmp.OnlyInHead)
	}
	other := mk("c", "gemma", "pv2", nil)
	cmp = evalCompareRuns(base, other)
	if cmp.Comparable || !strings.Contains(cmp.Note, "model farklı") {
		t.Fatalf("model farkı kıyaslanamaz: %+v", cmp)
	}
	b, _ := json.Marshal(cmp)
	for _, k := range []string{"newlyFailing", "newlyPassing", "regressed", "improved", "onlyInHead"} {
		if strings.Contains(string(b), `"`+k+`":null`) {
			t.Fatalf("%s null — boş liste [] olmalı: %s", k, b)
		}
	}
}
