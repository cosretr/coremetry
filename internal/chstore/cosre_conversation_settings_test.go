package chstore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestParseCosreConversation(t *testing.T) {
	cases := []struct {
		raw       string
		ok, state bool
		verify    string
	}{
		{"", true, true, CosreVerifyShadow},
		{`{}`, true, true, CosreVerifyShadow},
		{`{"state":false}`, true, false, CosreVerifyShadow},
		{`{"verification":"off"}`, true, true, CosreVerifyOff},
		{`{"state":true,"verification":"shadow"}`, true, true, CosreVerifyShadow},
		{`{"verification":"loud"}`, true, true, CosreVerifyShadow},
		{`{bozuk`, false, false, ""},
	}
	for _, c := range cases {
		got, ok := parseCosreConversation([]byte(c.raw))
		if ok != c.ok {
			t.Fatalf("%q ok=%v", c.raw, ok)
		}
		if !ok {
			continue
		}
		if got.State != c.state || got.Verification != c.verify {
			t.Fatalf("%q → %+v", c.raw, got)
		}
	}
}

// v0.10.2353 (I5) — interaction: off | on, varsayılan ve bilinmeyen değer off.
func TestParseCosreConversationInteraction(t *testing.T) {
	for raw, want := range map[string]string{
		"": CosreInteractionOff, `{}`: CosreInteractionOff, `{"interaction":"on"}`: CosreInteractionOn,
		`{"interaction":"off"}`: CosreInteractionOff, `{"interaction":"shadow"}`: CosreInteractionOff,
	} {
		if got, ok := parseCosreConversation([]byte(raw)); !ok || got.Interaction != want {
			t.Fatalf("%q → %+v", raw, got)
		}
	}
	SetCosreConversation(CosreConversation{State: true, Verification: CosreVerifyShadow, Interaction: "x"})
	t.Cleanup(func() { SetCosreConversation(DefaultCosreConversation()) })
	if CurrentCosreConversation().Interaction != CosreInteractionOff {
		t.Fatal("geçersiz interaction off'a düşmeli")
	}
}

func TestCosreConversationDefaultStateOn(t *testing.T) {
	if d := DefaultCosreConversation(); !d.State || d.Style || d.Verification != CosreVerifyShadow {
		t.Fatalf("varsayılan %+v", d)
	}
}

// v0.10.2338 (D) — operatörün kalıp ifadeleri: kırpma, tekilleştirme, tavanlar.
func TestCosreBannedPhrases(t *testing.T) {
	got, err := NormalizeCosreBannedPhrases([]string{"  Sorunuzu   inceledim ", "", "sorunuzu inceledim", "Kolay gelsin"})
	if err != nil || len(got) != 2 || got[0] != "Sorunuzu inceledim" || got[1] != "Kolay gelsin" {
		t.Fatalf("normalize: %q %v", got, err)
	}
	long := strings.Repeat("a", CosreBannedPhraseMaxBytes+1)
	if _, err := NormalizeCosreBannedPhrases([]string{long}); err == nil {
		t.Fatal("80 bayttan uzun ifade kabul edildi")
	}
	many := make([]string, CosreBannedPhrasesMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("ifade %d", i)
	}
	if _, err := NormalizeCosreBannedPhrases(many); err == nil {
		t.Fatal("41 ifade kabul edildi")
	}
	if out, err := NormalizeCosreBannedPhrases(many[:CosreBannedPhrasesMax]); err != nil || len(out) != CosreBannedPhrasesMax {
		t.Fatalf("40 ifade: %d %v", len(out), err)
	}
	// Okuma yolu bozuk listeyi bloğu düşürmeden kırpar.
	raw, _ := json.Marshal(map[string]any{"bannedPhrases": append(append([]string{long}, many...), "x")})
	c, ok := parseCosreConversation(raw)
	if !ok || len(c.BannedPhrases) != CosreBannedPhrasesMax || c.BannedPhrases[0] != "ifade 0" {
		t.Fatalf("okuma kırpması: ok=%v n=%d", ok, len(c.BannedPhrases))
	}
	if d, _ := parseCosreConversation(nil); d.BannedPhrases != nil {
		t.Fatal("varsayılan liste boş olmalı")
	}
}

// v0.10.2355 (S1, karar 25) — evidence: off | shadow | on, varsayılan off;
// bilinmeyen kip okuma yolunda off'a düşer, yazımda da off'a normalleşir.
func TestCosreConversationEvidence(t *testing.T) {
	if d := DefaultCosreConversation(); d.Evidence != CosreEvidenceOff {
		t.Fatalf("varsayılan kanıt %q", d.Evidence)
	}
	for raw, want := range map[string]string{
		"":                                CosreEvidenceOff,
		`{}`:                              CosreEvidenceOff,
		`{"evidence":"shadow"}`:           CosreEvidenceShadow,
		`{"evidence":"on"}`:               CosreEvidenceOn,
		`{"evidence":"enforce"}`:          CosreEvidenceOff,
		`{"evidence":"off"}`:              CosreEvidenceOff,
		`{"state":false,"evidence":"on"}`: CosreEvidenceOn,
	} {
		got, ok := parseCosreConversation([]byte(raw))
		if !ok || got.Evidence != want {
			t.Fatalf("%q → %q (ok=%v), beklenen %q", raw, got.Evidence, ok, want)
		}
	}
	prev := CurrentCosreConversation()
	defer SetCosreConversation(prev)
	SetCosreConversation(CosreConversation{State: true, Verification: CosreVerifyShadow, Evidence: "loud"})
	if c := CurrentCosreConversation(); c.Evidence != CosreEvidenceOff {
		t.Fatalf("geçersiz kip yayınlandı: %q", c.Evidence)
	}
}
