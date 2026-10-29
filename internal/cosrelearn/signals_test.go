package cosrelearn

// CoSRE öğrenme Faz 2 — örtük sinyal tespiti ve puanlama (saf çekirdek).

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func turn(id, conv string, at time.Duration, q string) chstore.CosreInteraction {
	return chstore.CosreInteraction{ExchangeID: id, ConversationID: conv, CreatedAt: t0.Add(at), Question: q, TotalMs: 3000, Status: "ok", Tier: "loop"}
}

func TestDeriveSignals(t *testing.T) {
	a := turn("a", "c1", 0, "checkout servisi neden yavaş")
	b := turn("b", "c1", time.Minute, "checkout servisi neden bu kadar yavaş") // yeniden sorma (benzer, <2dk)
	c := turn("c", "c1", 10*time.Minute, "peki deploy oldu mu")                // benzemiyor
	d := turn("d", "c1", 11*time.Minute, "peki deploy oldu mu")
	d.Attempt = "regenerate" // c'yi cezalandırır
	e := turn("e", "c2", 0, "Bu neden oluyor?")
	e.NoEvidence = true
	e.Tools = []chstore.CosreTool{{Name: "find_exceptions", OK: false, Error: "timeout"}, {Name: "q", OK: false, Error: "skipped"}}
	f := turn("f", "c3", 0, "x")
	f.Status, f.ErrorMsg = "error", "context canceled"
	g := turn("g", "c4", 0, "problem ne")
	g.ProblemID = "p1"
	h := turn("h", "", 0, "")
	h.EmptyAnswer = true
	// v0.10.1280 — CoSRE adım 8: yeni sonuç değeri "aborted" de terk sinyali
	// (boş cevap bayrağı iptalde ayrıca sayılmaz).
	k := turn("k", "c5", 0, "y")
	k.Status, k.ErrorMsg, k.EmptyAnswer = "aborted", "context canceled", true

	got := DeriveSignals(Inputs{
		Interactions: []chstore.CosreInteraction{d, c, b, a, e, f, g, h, k},
		Signals: []chstore.CosreSignal{
			{ExchangeID: "d", Kind: SigCopy}, {ExchangeID: "d", Kind: SigCopy},
			{ExchangeID: "d", Kind: "bogus"}, {ExchangeID: "zz", Kind: SigCopy},
		},
		Feedback: map[string]int8{"g": 1, "zz": -1},
		Resolved: map[string]time.Time{"p1": t0.Add(10 * time.Minute)},
	})
	want := map[string][]string{
		"a": {SigRephrase},
		"c": {SigRegenerate},
		"d": {SigCopy, SigCopy},
		"e": {SigNoEvidence, SigToolError},
		"f": {SigAbandon},
		"g": {SigProblemResolved, SigThumbsUp},
		"h": {SigEmpty},
		"k": {SigAbandon},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
}

func TestResolvedOutsideWindowIsNotPositive(t *testing.T) {
	g := turn("g", "c4", 0, "q")
	g.ProblemID = "p1"
	for _, at := range []time.Duration{-time.Minute, 2 * time.Hour} {
		got := DeriveSignals(Inputs{Interactions: []chstore.CosreInteraction{g}, Resolved: map[string]time.Time{"p1": t0.Add(at)}})
		if len(got["g"]) != 0 {
			t.Errorf("resolved %v: %v", at, got["g"])
		}
	}
}

func TestRephraseNeedsTimeAndSimilarity(t *testing.T) {
	a := turn("a", "c", 0, "checkout neden yavaş")
	late := turn("b", "c", 5*time.Minute, "checkout neden yavaş")
	if k := followUpSignal(a, late); k != "" {
		t.Errorf("geç soru yeniden sorma sayıldı: %q", k)
	}
	other := turn("b", "c", 30*time.Second, "Nasıl düzeltirim?")
	if k := followUpSignal(turn("a", "c", 0, "Bu neden oluyor?"), other); k != "" {
		t.Errorf("takip sorusu yeniden sorma sayıldı: %q", k)
	}
}

func TestToolErrorCapped(t *testing.T) {
	r := turn("a", "c", 0, "q")
	for i := 0; i < 5; i++ {
		r.Tools = append(r.Tools, chstore.CosreTool{Name: "t", Error: "x"})
	}
	if got := DeriveSignals(Inputs{Interactions: []chstore.CosreInteraction{r}})["a"]; len(got) != maxToolErrorHits {
		t.Errorf("tool_error tavanı: %v", got)
	}
}

func TestAnswerScore(t *testing.T) {
	cases := []struct {
		kinds    []string
		want     float64
		pos, neg int
	}{
		{nil, 0.5, 0, 0},
		{[]string{SigCopy, SigProblemNote}, 0.95, 2, 0},
		{[]string{SigNoEvidence, SigRephrase}, 0, 0, 2},
		{[]string{SigEmpty, SigError}, 0, 0, 2},
		{[]string{SigThumbsUp, SigProblemNote, SigCopy}, 1, 3, 0},
	}
	for _, c := range cases {
		got, p, n := AnswerScore(c.kinds, nil, DefaultSignalWeights())
		if diff := got - c.want; diff > 1e-9 || diff < -1e-9 || p != c.pos || n != c.neg {
			t.Errorf("%v: %v %d %d", c.kinds, got, p, n)
		}
	}
}

func TestScoreTools(t *testing.T) {
	a := turn("a", "c", 0, "q")
	a.Tools = []chstore.CosreTool{{Name: "logs", OK: true, Ms: 100}, {Name: "logs", OK: false, Ms: 900}}
	b := turn("b", "c", time.Hour, "q2")
	b.Tools = []chstore.CosreTool{{Name: "logs", OK: true, Ms: 200}}
	got := ScoreTools(t0, []chstore.CosreInteraction{a, b}, map[string][]string{"a": {SigRephrase}})
	if len(got) != 2 || got[0].Tool != "" {
		t.Fatalf("koşu işareti + 1 tool beklendi: %+v", got)
	}
	l := got[1]
	if l.Calls != 3 || l.OKCalls != 2 || l.Rephrased != 2 || l.P95Ms != 900 || l.SumMs != 1200 {
		t.Errorf("logs: %+v", l)
	}
	if want := float32(ToolScore(2.0/3, 2.0/3)); l.Score != want {
		t.Errorf("skor %v want %v", l.Score, want)
	}
}

func TestDueDays(t *testing.T) {
	// 15'i 01:00 — 14'ü henüz oturmadı (gün sonu + 2 saat), 12-13 hazır.
	got := DueDays(time.Date(2026, 10, 15, 1, 0, 0, 0, time.UTC))
	if len(got) != 2 || got[0].Day() != 12 || got[1].Day() != 13 {
		t.Fatalf("%v", got)
	}
	got = DueDays(time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC))
	if len(got) != 3 || got[2].Day() != 14 {
		t.Fatalf("%v", got)
	}
}

type fakeStore struct {
	ground   []chstore.CosreGroundingInput
	grounded []chstore.CosreGrounding
	rows     []chstore.CosreInteraction
	answers  []chstore.CosreAnswerQuality
	tools    []chstore.CosreToolQuality
	from     time.Time
	to       time.Time
	settings []byte
	signals  []chstore.CosreSignal
}

func (f *fakeStore) ListCosreInteractionsForScoring(_ context.Context, from, to time.Time) ([]chstore.CosreInteraction, error) {
	f.from, f.to = from, to
	return f.rows, nil
}
func (f *fakeStore) ListCosreSignals(context.Context, time.Time, time.Time) ([]chstore.CosreSignal, error) {
	return f.signals, nil
}
func (f *fakeStore) AIFeedbackVerdictsSince(context.Context, time.Time) (map[string]int8, error) {
	return map[string]int8{"a": -1}, nil
}
func (f *fakeStore) ProblemResolvedAt(context.Context, []string) (map[string]time.Time, error) {
	return nil, nil
}
func (f *fakeStore) InsertCosreAnswerQuality(_ context.Context, r []chstore.CosreAnswerQuality) error {
	f.answers = r
	return nil
}
func (f *fakeStore) InsertCosreToolQuality(_ context.Context, r []chstore.CosreToolQuality) error {
	f.tools = r
	return nil
}
func (f *fakeStore) CosreQualityDayDone(context.Context, time.Time) (bool, error) { return false, nil }
func (f *fakeStore) GetSetting(_ context.Context, key string) ([]byte, error) {
	if key == SignalSettingsKey {
		return f.settings, nil
	}
	return nil, nil
}

func TestRunQualityDayScoresOnlyThatDay(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a := turn("a", "c", 0, "q") // 14'ü 10:00
	next := chstore.CosreInteraction{ExchangeID: "n", ConversationID: "c", CreatedAt: day.Add(24*time.Hour + time.Minute), Status: "ok"}
	st := &fakeStore{rows: []chstore.CosreInteraction{a, next}}
	n, _, err := RunQualityDay(context.Background(), st, day)
	if err != nil || n != 1 || len(st.answers) != 1 || st.answers[0].ExchangeID != "a" {
		t.Fatalf("n=%d err=%v answers=%+v", n, err, st.answers)
	}
	if st.answers[0].Score != 0.35 || st.answers[0].Reasons[0] != SigThumbsDown { // v0.10.1281: 0.5 − 0.15
		t.Errorf("👎 puanı: %+v", st.answers[0])
	}
	if !st.to.After(day.Add(24 * time.Hour)) {
		t.Errorf("takip turu için pencere gün sonunu aşmalı: %v", st.to)
	}
}

func (f *fakeStore) ListCosreForGrounding(context.Context, time.Time, time.Time, int) ([]chstore.CosreGroundingInput, error) {
	return f.ground, nil
}
func (f *fakeStore) InsertCosreGrounding(_ context.Context, r []chstore.CosreGrounding) error {
	f.grounded = r
	return nil
}
func (f *fakeStore) UngroundedExchanges(context.Context, time.Time, time.Time) (map[string]bool, error) {
	out := map[string]bool{}
	for _, g := range f.grounded {
		if g.Ungrounded > 0 {
			out[g.ExchangeID] = true
		}
	}
	return out, nil
}

// v0.10.1291 — takip çipinden gelen benzer soru "yeniden sorma" cezası almaz;
// çipsiz aynı akış hâlâ rephrase sayılır.
func TestFollowupClickSuppressesRephrase(t *testing.T) {
	a := turn("a", "c1", 0, "checkout servisi neden yavaş")
	b := turn("b", "c1", time.Minute, "checkout servisi neden bu kadar yavaş")
	plain := DeriveSignals(Inputs{Interactions: []chstore.CosreInteraction{a, b}})
	if !reflect.DeepEqual(plain["a"], []string{SigRephrase}) {
		t.Fatalf("çipsiz: %v, want [rephrase]", plain["a"])
	}
	chip := DeriveSignals(Inputs{Interactions: []chstore.CosreInteraction{a, b},
		Signals: []chstore.CosreSignal{{ExchangeID: "a", Kind: SigFollowupClick, Detail: "slow"}}})
	if !reflect.DeepEqual(chip["a"], []string{SigFollowupClick}) {
		t.Fatalf("çipli: %v, want [followup_click]", chip["a"])
	}
}
