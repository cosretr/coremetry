package api

// v0.10.2480 (H) — karmaşıklık kademesi: hoşgörülü okuma, düşüş, bütçe, plan.

import (
	"strings"
	"testing"
)

func TestParseIntentComplexity(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		tier complexityTier
		subs int
		ok   bool
	}{
		{"temiz JSON basit", `{"intent":"none","complexity":"basit","subQuestions":[]}`, complexitySimple, 0, true},
		{"orta, alt soru yok sayılır", `{"intent":"none","complexity":"orta","subQuestions":["a","b"]}`, complexityMedium, 0, true},
		{"karmaşık + alt sorular", `{"intent":"none","complexity":"karmaşık","subQuestions":["lag neden arttı?","hangi pod?","deploy oldu mu?"]}`, complexityComplex, 3, true},
		{"çitli ve önsözlü", "Tamam:\n```json\n{\"complexity\":\"Karmasik\",\"subQuestions\":[\"x nedir?\",\"y nedir?\"]}\n```", complexityComplex, 2, true},
		{"İngilizce yazım", `{"complexity":"medium"}`, complexityMedium, 0, true},
		{"en çok 4 alt soru, yinelenen atılır", `{"complexity":"complex","subQuestions":["a1","a1","b2","c3","d4","e5"]}`, complexityComplex, 4, true},
		{"alt sorular tek metin", `{"complexity":"karmaşık","subQuestions":"birinci soru; ikinci soru"}`, complexityComplex, 2, true},
		{"alan yok → düşüş", `{"intent":"problems","service":""}`, "", 0, false},
		{"tanınmayan değer → düşüş", `{"complexity":"çok zor"}`, "", 0, false},
		{"bozuk JSON, alan okunur", `{"complexity": "orta", "subQuestions": [`, complexityMedium, 0, true},
		{"çöp → düşüş", `bunu bilemem`, "", 0, false},
		{"boş → düşüş", ``, "", 0, false},
	}
	for _, c := range cases {
		tier, subs, ok := parseIntentComplexity(c.raw)
		if tier != c.tier || len(subs) != c.subs || ok != c.ok {
			t.Errorf("%s: %q %v %v", c.name, tier, subs, ok)
		}
	}
}

func TestResolveLoopComplexityFallback(t *testing.T) {
	// Sınıflandırıcı kararı yok → deterministik işaret.
	if got := resolveLoopComplexity(nil, "dünle kıyasla"); !got.Fallback || got.Tier != complexityMedium {
		t.Fatalf("kıyas: %+v", got)
	}
	if got := resolveLoopComplexity(&complexityTurn{}, "neden oluyor bu"); !got.Fallback || got.Tier != complexitySimple {
		t.Fatalf("düz soru: %+v", got)
	}
	// Okunamayan çıktı kayıt bırakmaz → yine düşüş.
	c := &complexityTurn{}
	c.note("garbage")
	if got := resolveLoopComplexity(c, "neden oluyor bu"); !got.Fallback {
		t.Fatalf("çöp kayıt bıraktı: %+v", got)
	}
	c.note(`{"complexity":"karmaşık","subQuestions":["a nedir?","b nedir?"]}`)
	if got := resolveLoopComplexity(c, "neden oluyor bu"); got.Fallback || got.Tier != complexityComplex || len(got.Subs) != 2 {
		t.Fatalf("sınıflandırıcı kararı: %+v", got)
	}
	for cue, want := range map[loopCueKind]complexityTier{loopCueNone: complexitySimple, loopCueMulti: complexityComplex,
		loopCueCompare: complexityMedium, loopCueRank: complexityMedium, loopCueCount: complexityMedium, loopCueWhyVol: complexityMedium} {
		if got := complexityFromCue(cue); got != want {
			t.Errorf("işaret %q: %q", cue, got)
		}
	}
}

// Bütçe = max(deterministik kademe, karmaşıklık kademesi).
func TestLoopBudgetForBothInputs(t *testing.T) {
	cases := []struct {
		scale         int
		problem       bool
		tier          complexityTier
		calls, rounds int
	}{
		{1, false, "", 8, 5},
		{1, false, complexitySimple, 8, 5},
		{1, true, "", 10, 6},                // Problem kapsamı en az 2–3 kademesi
		{1, false, complexityMedium, 10, 6}, // orta en az 10
		{4, false, complexityMedium, 12, 7}, // deterministik daha büyük → o
		{1, false, complexityComplex, 14, 9},
		{16, true, complexityComplex, 14, 9}, // en üst + 1 tur
		{8, true, complexitySimple, 14, 8},
	}
	for _, c := range cases {
		calls, rounds := loopBudgetFor(c.scale, c.problem, c.tier)
		if calls != c.calls || rounds != c.rounds {
			t.Errorf("%+v: %d / %d", c, calls, rounds)
		}
		d := deepMode{loopScale: c.scale, problemScope: c.problem, complexity: c.tier}
		if d.toolCalls() != c.calls || d.toolRounds() != c.rounds {
			t.Errorf("deepMode %+v: %d / %d", c, d.toolCalls(), d.toolRounds())
		}
		deep := d
		deep.On = true
		if deep.toolCalls() < c.calls || deep.toolRounds() < c.rounds {
			t.Errorf("Derin tabanın altına indi %+v: %d / %d", c, deep.toolCalls(), deep.toolRounds())
		}
	}
}

func TestPlanBlockAndStep(t *testing.T) {
	if planBlockTR(nil) != "" {
		t.Fatal("alt soru yokken plan bloğu olmamalı")
	}
	b := planBlockTR([]string{"lag neden arttı?", "```kaçış``` denemesi"})
	if !strings.Contains(b, "```text\n1. lag neden arttı?\n2. ") || !strings.HasSuffix(b, "```\n") {
		t.Fatalf("plan bloğu:\n%s", b)
	}
	if strings.Count(b, "```") != 2 {
		t.Fatalf("çit kaçışı yok:\n%s", b)
	}
	if !strings.Contains(b, "her alt soruyu cevapla") || !strings.Contains(b, "cevaplanamayanı açıkça söyle") {
		t.Fatalf("talimat eksik:\n%s", b)
	}
	if planStepLabelTR(3) != "plan: 3 alt soru" {
		t.Fatal(planStepLabelTR(3))
	}
}

// Sınıflandırıcı şeması alanları taşır (katı şemalı sağlayıcı reddetmesin).
func TestIntentSchemaCarriesComplexity(t *testing.T) {
	props := intentClassifySchema()["properties"].(map[string]any)
	if _, ok := props["complexity"]; !ok {
		t.Fatal("complexity yok")
	}
	if _, ok := props["subQuestions"]; !ok {
		t.Fatal("subQuestions yok")
	}
}
