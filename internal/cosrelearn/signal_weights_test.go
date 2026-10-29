package cosrelearn

// signal_weights_test.go — operatör kararı 2026-10-29 §1: kart ilgili /
// ilgisiz / not ağırlıkları system_settings("cosre").signals.weights'e
// taşındı; kart puanı [-0.5, +0.5]'e kırpılır. Pinler: varsayılanlar,
// kırpma, okuma normalizasyonu (hoşgörülü), PUT denetimi (katı), blob
// birleştirme (diğer anahtarlar korunur) ve gece işinin ayarı okuması.

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDefaultSignalWeights(t *testing.T) {
	w := DefaultSignalWeights()
	if !near(w.EvidenceRelevant, 0.10) || !near(w.EvidenceIrrelevant, -0.10) || w.Note != 0 {
		t.Fatalf("varsayılanlar +0.10 / -0.10 / 0 olmalı: %+v", w)
	}
	// Weight haritası varsayılanla aynı kaynaktan (oy-ağırlığı pini buna bakar).
	for _, k := range []string{SigEvidenceRelevant, SigEvidenceIrrelevant, SigNote} {
		got, ok := w.For(k)
		if !ok || !near(got, Weight[k]) {
			t.Errorf("%s: ayar %v, Weight %v", k, got, Weight[k])
		}
	}
	if _, ok := w.For(SigCopy); ok {
		t.Error("copy ayarlanabilir tür değil")
	}
}

func TestClampCardScore(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{0, 0}, {0.3, 0.3}, {0.5, 0.5}, {0.51, 0.5}, {7, 0.5},
		{-0.3, -0.3}, {-0.5, -0.5}, {-0.7, -0.5}, {math.Inf(1), 0.5},
		{math.Inf(-1), -0.5}, {math.NaN(), 0},
	} {
		if got := ClampCardScore(c.in); !near(got, c.want) {
			t.Errorf("ClampCardScore(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseSignalWeights(t *testing.T) {
	def := DefaultSignalWeights()
	for _, c := range []struct {
		name string
		raw  string
		want SignalWeights
	}{
		{"boş", "", def},
		{"bozuk JSON", "{", def},
		{"signals yok", `{"other":1}`, def},
		{"tam", `{"signals":{"weights":{"evidence_relevant":0.2,"evidence_irrelevant":-0.3,"note":0.05}}}`, SignalWeights{0.2, -0.3, 0.05}},
		{"eksik alan varsayılan", `{"signals":{"weights":{"evidence_relevant":0.25}}}`, SignalWeights{0.25, -0.10, 0}},
		{"aralık üstü kırpılır", `{"signals":{"weights":{"evidence_relevant":3,"evidence_irrelevant":-9,"note":-2}}}`, SignalWeights{0.5, -0.5, -0.5}},
		{"ters işaret sıfıra kırpılır", `{"signals":{"weights":{"evidence_relevant":-0.2,"evidence_irrelevant":0.2}}}`, SignalWeights{0, 0, 0}},
		{"tür hatası varsayılan", `{"signals":{"weights":"x"}}`, def},
	} {
		got := ParseSignalWeights([]byte(c.raw))
		if !near(got.EvidenceRelevant, c.want.EvidenceRelevant) || !near(got.EvidenceIrrelevant, c.want.EvidenceIrrelevant) || !near(got.Note, c.want.Note) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestValidateSignalWeights(t *testing.T) {
	for _, c := range []struct {
		name string
		w    SignalWeights
		ok   bool
	}{
		{"varsayılan", DefaultSignalWeights(), true},
		{"sınırlar", SignalWeights{0.5, -0.5, -0.5}, true},
		{"sıfırlar", SignalWeights{}, true},
		{"ilgili negatif", SignalWeights{-0.1, -0.1, 0}, false},
		{"ilgisiz pozitif", SignalWeights{0.1, 0.1, 0}, false},
		{"ilgili > 0.5", SignalWeights{0.51, -0.1, 0}, false},
		{"not > 0.5", SignalWeights{0.1, -0.1, 0.6}, false},
		{"NaN", SignalWeights{math.NaN(), -0.1, 0}, false},
		{"Inf", SignalWeights{0.1, math.Inf(-1), 0}, false},
	} {
		if err := ValidateSignalWeights(c.w); (err == nil) != c.ok {
			t.Errorf("%s: err=%v, ok beklenen %v", c.name, err, c.ok)
		}
	}
}

func TestMergeSignalWeightsKeepsOtherKeys(t *testing.T) {
	w := SignalWeights{0.2, -0.2, 0}
	for _, existing := range []string{"", "{", `{"other":{"x":1},"signals":{"keep":true,"weights":{"note":0.4}}}`} {
		out, err := MergeSignalWeights([]byte(existing), w)
		if err != nil {
			t.Fatal(err)
		}
		if got := ParseSignalWeights(out); got != w {
			t.Errorf("%q: geri okunan %+v", existing, got)
		}
		if existing != "" && existing != "{" {
			var m map[string]map[string]any
			if err := json.Unmarshal(out, &m); err != nil || m["other"]["x"] != float64(1) || m["signals"]["keep"] != true {
				t.Errorf("diğer anahtarlar kayboldu: %s", out)
			}
		}
	}
}

func TestCardScoresClampPerCard(t *testing.T) {
	w := DefaultSignalWeights()
	sig := func(ex, kind, detail string) chstore.CosreSignal {
		return chstore.CosreSignal{ExchangeID: ex, Kind: kind, Detail: detail}
	}
	var sigs []chstore.CosreSignal
	for i := 0; i < 8; i++ { // tek kart 8× ilgili → 0.8 değil 0.5
		sigs = append(sigs, sig("a", SigEvidenceRelevant, "s1"))
	}
	sigs = append(sigs,
		sig("a", SigEvidenceIrrelevant, "s2"),
		sig("a", SigNote, "metin"),            // ağırlık 0 → kart açmaz
		sig("x", SigEvidenceRelevant, "s1"),   // penceredeki tur değil
		sig("a", SigCopy, "kod"),              // ayarlanabilir değil
		sig("b", SigEvidenceIrrelevant, "s3"), // ayrı cevap
		sig("b", SigEvidenceIrrelevant, "s3"),
	)
	in := Inputs{Interactions: []chstore.CosreInteraction{{ExchangeID: "a"}, {ExchangeID: "b"}}, Signals: sigs}
	got := CardScores(in, w)
	if len(got) != 2 || len(got["a"]) != 2 || !near(got["a"]["s1"], 0.5) || !near(got["a"]["s2"], -0.1) {
		t.Fatalf("a kartları: %+v", got)
	}
	if !near(got["b"]["s3"], -0.2) {
		t.Fatalf("b kartı: %+v", got["b"])
	}
	// Not ağırlığı ayarla açılınca mesaj kartı da kırpılır.
	w.Note = 0.3
	in.Signals = []chstore.CosreSignal{sig("a", SigNote, "1"), sig("a", SigNote, "2")}
	if got := CardScores(in, w); !near(got["a"][messageCard], 0.5) {
		t.Fatalf("mesaj kartı kırpılmadı: %+v", got)
	}
}

func TestAnswerScoreUsesCardScores(t *testing.T) {
	w := DefaultSignalWeights()
	for _, c := range []struct {
		name     string
		kinds    []string
		cards    map[string]float64
		want     float64
		pos, neg int
	}{
		{"kart yok", []string{SigEvidenceRelevant}, nil, 0.5, 1, 0},
		{"kırpılmış kart", []string{SigEvidenceRelevant, SigEvidenceRelevant}, map[string]float64{"s1": 0.5}, 1, 2, 0},
		{"kırpılmamış girdi de kırpılır", nil, map[string]float64{"s1": 9}, 1, 0, 0},
		{"ilgisiz + kopya", []string{SigEvidenceIrrelevant, SigCopy}, map[string]float64{"s1": -0.1}, 0.55, 1, 1},
		{"not nötr", []string{SigNote}, nil, 0.5, 0, 0},
	} {
		got, p, n := AnswerScore(c.kinds, c.cards, w)
		if !near(got, c.want) || p != c.pos || n != c.neg {
			t.Errorf("%s: %v %d %d", c.name, got, p, n)
		}
	}
}

// Gece işi ayarı her koşuda okur: ayardaki ağırlık puana yansır.
func TestRunQualityDayReadsSignalWeights(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a := turn("a", "c", 0, "q")
	blob, _ := MergeSignalWeights(nil, SignalWeights{EvidenceRelevant: 0.4, EvidenceIrrelevant: -0.1})
	st := &fakeStore{
		rows:     []chstore.CosreInteraction{a},
		settings: blob,
		signals:  []chstore.CosreSignal{{ExchangeID: "a", Kind: SigEvidenceRelevant, Detail: "s1"}},
	}
	if _, _, err := RunQualityDay(context.Background(), st, day); err != nil {
		t.Fatal(err)
	}
	// 0.5 taban − 0.15 (fake 👎) + 0.4 (ayardan kart) = 0.75
	if len(st.answers) != 1 || !near(float64(st.answers[0].Score), float64(float32(0.75))) {
		t.Fatalf("ayar puana yansımadı: %+v", st.answers)
	}
}
