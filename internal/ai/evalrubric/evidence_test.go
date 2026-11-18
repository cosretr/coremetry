package evalrubric

import "testing"

func TestEvidenceCues(t *testing.T) {
	if !ShowMeCue("Kanıtı GÖSTER") || !ShowMeCue("nereden biliyorsun?") || ShowMeCue("durum ne") {
		t.Error("göster ipucu")
	}
	if !ActionCue("payments'ı yeniden başlat") || !ActionCue("Rollback öneririm") || ActionCue("hata yok") {
		t.Error("eylem ipucu")
	}
	if !LiveDataClaim("412 hata") || LiveDataClaim("her şey yolunda") || LiveDataClaim("yolunda.\n\nKaynak: get_service_health 12") {
		t.Error("canlı veri iddiası")
	}
}

func TestEvidenceOf(t *testing.T) {
	zero, one := 0, 1
	cases := []struct {
		name               string
		in                 EvidenceInput
		claim              *bool
		noPile, noGuessAsD bool
	}{
		{"iddia kanıtlı", EvidenceInput{Answer: "412 hata", Calls: 1, Attached: []string{"exception"}}, ptrB(true), true, true},
		{"iddia kanıtsız", EvidenceInput{Answer: "412 hata", Calls: 1}, ptrB(false), true, true},
		{"istenen tür yok", EvidenceInput{Answer: "x", Calls: 1, Attached: []string{"logs"}, Require: []string{"exception"}}, ptrB(false), true, true},
		{"iddiasız ölçülmez", EvidenceInput{Answer: "yolunda", Calls: 1}, nil, true, true},
		{"bilgi turunda blok", EvidenceInput{Answer: "rollback nedir", Attached: []string{"trace"}, Max: &zero}, nil, false, false},
		{"tavan aşımı", EvidenceInput{Answer: "x", Calls: 2, Attached: []string{"trace", "logs"}, Max: &one}, nil, false, true},
		{"aynı tür iki kez", EvidenceInput{Answer: "x", Calls: 2, Attached: []string{"trace", "trace"}}, nil, false, true},
		{"sahtede olmayan sayı", EvidenceInput{Answer: "x", Calls: 1, Ungrounded: 1}, nil, true, false},
		{"araçsız ihtiyatsız neden", EvidenceInput{Answer: "Hata veritabanı yüzünden."}, nil, true, false},
		{"araçsız ihtiyatlı neden", EvidenceInput{Answer: "Muhtemelen veritabanı yüzünden."}, nil, true, true},
	}
	for _, c := range cases {
		got := EvidenceOf(c.in)
		if (got.ClaimsHaveEvidence == nil) != (c.claim == nil) || (c.claim != nil && *got.ClaimsHaveEvidence != *c.claim) ||
			got.NoPile != c.noPile || got.NoGuessAsData != c.noGuessAsD {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

func ptrB(b bool) *bool { return &b }
