package api

// evalset_fixture_test.go — v0.10.422 (CoSRE denetimi E1/E7): donmuş replay
// vakalarının SÖZLEŞME ve skorlayıcı testleri. Etiketsiz koşar: fikstür
// yazım hatası kırmızı test olur (sessiz atlama değil). Modele giden koşum
// evalset_test.go'da (//go:build evalset). Şema: internal/copilot/evalset/README.md.
//
// v0.10.940 — şema tipleri, yükleyici, surface→sistem promptu haritası ve
// SAF skorlayıcılar üretim koduna taşındı (ai_evalset_core.go — panel koşuyu
// sunucuda başlatıyor); testler burada, değişmeden. loadEvalset artık GÖMÜLÜ
// kümeyi okur: TestEvalsetFixturesValid gemiye bineni doğrular.

import (
	"encoding/json"
	"fmt"
	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// loadEvalset — loadEvalsetCases'in test sarmalayıcısı (hata = t.Fatal).
func loadEvalset(t *testing.T) []evalCase {
	t.Helper()
	cases, err := loadEvalsetCases()
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

// TestEvalsetFixturesValid — etiketsiz; fikstür sözleşmesi.
func TestEvalsetFixturesValid(t *testing.T) {
	cases := loadEvalset(t)
	if len(cases) < 20 {
		t.Fatalf("en az 20 vaka bekleniyor, %d", len(cases))
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			t.Errorf("%s: id boş ya da tekrar: %q", c.file, c.ID)
		}
		seen[c.ID] = true
		if _, ok := evalSystemPrompt(c.Surface); !ok {
			t.Errorf("%s/%s: surface %q çözülemiyor", c.file, c.ID, c.Surface)
		}
		isRCA := c.Surface == "RCAVerdict"
		if strings.TrimSpace(c.Why) == "" || (!isRCA && strings.TrimSpace(c.User) == "" && strings.TrimSpace(c.Prompt) == "") {
			t.Errorf("%s/%s: why ve (user | prompt) zorunlu", c.file, c.ID)
		}
		if isRCA != (len(c.Hypothesis) > 0) {
			t.Errorf("%s/%s: hypothesis yalnız RCAVerdict yüzeyinde ve orada zorunlu", c.file, c.ID)
		}
		if isRCA {
			var h chstore.RootCauseHypothesis
			if err := json.Unmarshal(c.Hypothesis, &h); err != nil || h.Service == "" {
				t.Errorf("%s/%s: hypothesis çözülemiyor ya da service boş: %v", c.file, c.ID, err)
			}
			if len(c.Expect.Verdicts) == 0 {
				t.Errorf("%s/%s: RCA vakası verdicts beklentisi taşımalı", c.file, c.ID)
			}
		}
		if c.User != "" && c.Prompt != "" {
			t.Errorf("%s/%s: user ve prompt birlikte verilmez", c.file, c.ID)
		}
		e := c.Expect
		if len(e.MustContain) == 0 && len(e.MustNotContain) == 0 && e.MaxUnknownEntities == nil && e.Intent == "" && len(e.Verdicts) == 0 {
			t.Errorf("%s/%s: en az bir beklenti gerekli", c.file, c.ID)
		}
		if (c.Surface == "IntentClassify") != (e.Intent != "") {
			t.Errorf("%s/%s: intent beklentisi yalnız IntentClassify yüzeyinde ve orada zorunlu", c.file, c.ID)
		}
	}
}

// TestScoreEvalCase — skorlayıcı saf ve tablolu: E7'nin "davranış" pini
// modelsiz de doğrulanır (skorlayıcının kendisi yalan söylemesin).
func TestScoreEvalCase(t *testing.T) {
	one := 0
	prose := evalCase{ID: "x", Surface: "Problem", User: "Service: checkout", Expect: evalExpect{
		MustContain: []string{"2.14.0"}, MustNotContain: []string{"kubectl"},
		KnownEntities: []string{"checkout"}, MaxUnknownEntities: &one,
	}}
	if fails, _ := scoreEvalCase(prose, "sys", "checkout 2.14.0 sonrası bozuldu", nil); len(fails) != 0 {
		t.Fatalf("temiz cevap kızardı: %v", fails)
	}
	fails, unknown := scoreEvalCase(prose, "sys", "kubectl ile ghost-gateway ve phantom-svc'yi yeniden başlat", nil)
	if unknown != 2 || len(fails) != 3 {
		t.Fatalf("3 ihlal (missing, forbidden, unknown) bekleniyor: %v unknown=%d", fails, unknown)
	}
	if fails, _ := scoreEvalCase(prose, "sys", "", fmt.Errorf("boom")); len(fails) != 1 || !strings.HasPrefix(fails[0], "error:") {
		t.Fatalf("hata tek ihlal: %v", fails)
	}
	intent := evalCase{ID: "i", Surface: "IntentClassify", User: "checkout nasıl?", Expect: evalExpect{
		Intent: "service_health", IntentService: "checkout", KnownEntities: []string{"checkout", "payments"},
	}}
	if fails, _ := scoreEvalCase(intent, "sys", `{"intent":"service_health","service":"checkout","env":"","rangeS":3600}`, nil); len(fails) != 0 {
		t.Fatalf("doğru niyet kızardı: %v", fails)
	}
	if fails, _ := scoreEvalCase(intent, "sys", "```json\n{\"intent\":\"problems\",\"service\":\"\"}\n```", nil); len(fails) != 1 || !strings.HasPrefix(fails[0], "intent \"problems\"") {
		t.Fatalf("yanlış niyet yakalanmalı: %v", fails)
	}
	none := evalCase{ID: "n", Surface: "IntentClassify", User: "hava?", Expect: evalExpect{Intent: "none"}}
	if fails, _ := scoreEvalCase(none, "sys", `{"intent":"none"}`, nil); len(fails) != 0 {
		t.Fatalf("none kızardı: %v", fails)
	}
}

// v0.10.424 — RCA skorlayıcısı saf ve tablolu: katalog kimlikleri
// (E1..), uydurma kimlik (E9) K2'de düşer ve oranı bozar, serbest metindeki
// uydurma ad K3'te sayılır, verdict kümesi dışı kızarır.
func TestScoreRCACase(t *testing.T) {
	h := &chstore.RootCauseHypothesis{Service: "checkout", TopSuspect: "payments", TopScore: 0.9, Confidence: 0.8,
		Candidates: []chstore.ScoredCause{{Service: "payments", Score: 0.9, Hops: 1, Reason: "deploy"}, {Service: "inventory", Score: 0.1, Hops: 1}}}
	cat, rivals, entities, user := evalRCAInputs(h, time.Unix(1_700_000_000, 0))
	if len(cat.PositiveIDs()) < 2 || len(entities) == 0 || user == "" || len(rivals) == 0 {
		t.Fatalf("girdiler: pos=%v entities=%v rivals=%v", cat.PositiveIDs(), entities, rivals)
	}
	one := 1.0
	zero := 0
	c := evalCase{ID: "r", Surface: "RCAVerdict", Expect: evalExpect{Verdicts: []string{"root_cause_identified", "probable_cause"}, MinEvidenceCitationRate: &one, MaxUnknownEntities: &zero}}
	good := `{"verdict":"probable_cause","title":"payments deploy","summary":"payments 503 döndürüyor","root_cause":{"entity":"payments","failure_mode":"503","trigger":"deploy","latent_weakness":"","evidence":["E1"]},"causal_chain":[{"entity":"checkout","effect":"hata oranı","evidence":["E1"]}],"rejected_hypotheses":[],"model_confidence":0.7,"missing_evidence":[],"remediation":[]}`
	if fails, unknown := scoreRCACase(c, h, cat, good); len(fails) != 0 || unknown != 0 {
		t.Fatalf("temiz verdict kızardı: %v unknown=%d", fails, unknown)
	}
	bad := `{"verdict":"root_cause_identified","title":"ghost-gateway","summary":"ghost-gateway çöktü","root_cause":{"entity":"payments","failure_mode":"x","trigger":"y","latent_weakness":"","evidence":["E1","E9"]},"causal_chain":[],"rejected_hypotheses":[],"model_confidence":0.9,"missing_evidence":[],"remediation":[]}`
	fails, unknown := scoreRCACase(c, h, cat, bad)
	if unknown != 1 || len(fails) != 2 {
		t.Fatalf("atıf oranı (E9 düşer) + uydurma ad bekleniyor: %v unknown=%d", fails, unknown)
	}
	if fails, _ := scoreRCACase(c, h, cat, "Elbette, işte JSON: "+good); len(fails) != 0 {
		t.Fatalf("gevezelik onarılmalı (salvage): %v", fails)
	}
	// v0.10.431 — çürütme atıfları paydada: 2 destek + 1 katalog dışı
	// çürütme = 3 atıf, 1 red → 0.67 (eskiden (2-1)/2 = 0.5, aynı sonuç
	// ama sebep yanlıştı); geçerli çürütme atfı oranı düşürmez.
	half := 0.6
	c2 := evalCase{ID: "r2", Surface: "RCAVerdict", Expect: evalExpect{MinEvidenceCitationRate: &half}}
	withRef := `{"verdict":"probable_cause","title":"t","summary":"s","root_cause":{"entity":"payments","failure_mode":"503","trigger":"deploy","latent_weakness":"","evidence":["E1"]},"causal_chain":[{"entity":"checkout","effect":"hata","evidence":["E1"]}],"rejected_hypotheses":[{"hypothesis":"inventory","refuted_by":["E9"]}],"model_confidence":0.7,"missing_evidence":[],"remediation":[]}`
	if fails, _ := scoreRCACase(c2, h, cat, withRef); len(fails) != 0 {
		t.Fatalf("katalog dışı çürütme atfı paydaya girmeli (2/3 ≥ 0.6): %v", fails)
	}
	validRef := strings.Replace(withRef, `"refuted_by":["E9"]`, `"refuted_by":["`+cat.PositiveIDs()[1]+`"]`, 1)
	if fails, _ := scoreRCACase(c, h, cat, validRef); len(fails) != 0 {
		t.Fatalf("geçerli çürütme atfı oranı düşürmemeli: %v", fails)
	}
	if evalCaseSkipReason(evalCase{Provenance: &evalProvenance{Truncated: true}}) == "" || evalCaseSkipReason(evalCase{}) != "" {
		t.Fatal("kırpık vaka atlanır, diğerleri koşar")
	}
	if fails, _ := scoreRCACase(c, h, cat, `{"verdict":"insufficient_evidence","root_cause":{"evidence":[]}}`); len(fails) != 1 || !strings.HasPrefix(fails[0], "verdict") {
		t.Fatalf("küme dışı verdict kızarmalı: %v", fails)
	}
}

// TestEvalRubricInput — adaptör sözleşmesi (CI): JSON yüzeyi dil boyutu
// almaz, tavan vakadan, hata geçer.
func TestEvalRubricInput(t *testing.T) {
	two := 2
	c := evalCase{ID: "x", Surface: "Problem", Expect: evalExpect{MustContain: []string{"checkout"}, MaxUnknownEntities: &two}}
	in := evalRubricInput(c, "checkout için cevap", nil, 1, false)
	if !in.ExpectTurkish || in.MaxUnknown != 2 || in.UnknownCount != 1 || len(in.MustContain) != 1 {
		t.Fatalf("adaptör: %+v", in)
	}
	cj := evalCase{ID: "y", Surface: "IntentClassify"}
	if evalRubricInput(cj, "{}", nil, 0, false).ExpectTurkish {
		t.Fatal("JSON yüzeyi dil boyutu almamalı")
	}
	if evalRubricInput(c, "", nil, 0, true).ExpectTurkish {
		t.Fatal("RCA hakemi dil boyutu almamalı")
	}
	if evalrubric.Score(in).Total <= 0 {
		t.Fatal("geçerli girdi puan üretmeli")
	}
}
