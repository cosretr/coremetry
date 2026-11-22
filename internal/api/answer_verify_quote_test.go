package api

// answer_verify_quote_test.go — v0.10.2461 (operatör-bildirimli): alıntı
// denetimi. Kanıtta Türkçe log + İngilizce span mesajı; cevap birini birebir,
// kısaltarak, kısmen ya da ÇEVİREREK alıntılar.

import (
	"strings"
	"testing"
)

func quoteEvidence() verifyEvidence {
	return verifyEvidence{Texts: []string{
		`{"logs":[{"body":"Sana mobil uygulamadan yardımcı olamıyoruz. İşlemini tamamlamak için en yakın şubeye gelmene ihtiyacımız var.","service":"orders-core"}]}`,
		`{"exType":"com.example.order.OrderBusinessException","exMessage":"ORDER_PROCESS_CANCEL - We cannot help you from the \"mobile\" app."}`,
	}}
}

func quoteClaims(r verifyResult) []verifyClaim {
	var out []verifyClaim
	for _, c := range r.Claims {
		if c.Kind == verifyKindQuote {
			out = append(out, c)
		}
	}
	return out
}

func TestVerifyQuoteCheck(t *testing.T) {
	cases := []struct {
		name, answer string
		want         []string // alıntı sonuçları, sırayla
	}{
		{"birebir log", `- Log kanıtı: "Sana mobil uygulamadan yardımcı olamıyoruz. İşlemini tamamlamak için en yakın şubeye gelmene ihtiyacımız var."`, []string{verifyVerified}},
		{"büyük/küçük harf + boşluk", `Mesaj: “sana  mobil uygulamadan YARDIMCI olamıyoruz.”`, []string{verifyVerified}},
		{"kısmi alıntı", `Log: "en yakın şubeye gelmene ihtiyacımız var"`, []string{verifyVerified}},
		{"üç nokta", `Log: "Sana mobil uygulamadan … en yakın şubeye gelmene ihtiyacımız var."`, []string{verifyVerified}},
		{"ascii üç nokta", `Log: "...yardımcı olamıyoruz. İşlemini tamamlamak..."`, []string{verifyVerified}},
		{"ters tırnak span", "Span: `ORDER_PROCESS_CANCEL - We cannot help you`", []string{verifyVerified}},
		{"çeviri", `Hata: "Size mobil uygulamadan yardım edemiyoruz, lütfen şubeye gidin."`, []string{verifyMismatch}},
		{"İngilizceye çeviri", `Log: "We cannot help you from the mobile application, visit a branch."`, []string{verifyMismatch}},
		{"üç nokta, bir parça uydurma", `Log: "Sana mobil uygulamadan … lütfen çağrı merkezini arayın."`, []string{verifyMismatch}},
		{"kısa alıntı denetlenmez", `Durum "iptal edildi" oldu.`, nil},
		{"tanımlayıcı ters tırnak ad denetimine kalır", "Sınıf `com.example.order.OrderBusinessException`.", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := quoteClaims(verifyAnswer(verifyTierLoop, c.answer, quoteEvidence(), nil))
			if len(got) != len(c.want) {
				t.Fatalf("alıntı sayısı %d, beklenen %d: %+v", len(got), len(c.want), got)
			}
			for i, w := range c.want {
				if got[i].Status != w {
					t.Errorf("alıntı %d: %s, beklenen %s (%q)", i, got[i].Status, w, got[i].Text)
				}
			}
		})
	}
}

func TestVerifyQuoteEscapedEvidence(t *testing.T) {
	ans := `Span mesajı: “ORDER_PROCESS_CANCEL - We cannot help you from the "mobile" app.”`
	got := quoteClaims(verifyAnswer(verifyTierLoop, ans, quoteEvidence(), nil))
	if len(got) != 1 || got[0].Status != verifyVerified {
		t.Fatalf("JSON kaçışlı kanıttaki alıntı doğrulanmalı: %+v", got)
	}
}

func TestVerifyQuoteGeneralTierSkipped(t *testing.T) {
	ans := `Bilinen bir söz: "Erken optimizasyon bütün kötülüklerin anasıdır."`
	if got := quoteClaims(verifyAnswer(verifyTierGeneral, ans, verifyEvidence{}, nil)); len(got) != 0 {
		t.Fatalf("genel kademede alıntı denetlenmez: %+v", got)
	}
}

// Alıntı içi öteki denetimlerden maskelenir: çeviride geçen sayı ikinci kez işaretlenmez.
func TestVerifyQuoteMasksInterior(t *testing.T) {
	ans := `Log: "Sipariş 98765 için işlem 3 kez denendi ve başarısız oldu."`
	r := verifyAnswer(verifyTierLoop, ans, quoteEvidence(), nil)
	if len(r.Claims) != 1 || r.Claims[0].Kind != verifyKindQuote || r.Claims[0].Status != verifyMismatch {
		t.Fatalf("tek alıntı iddiası beklenirdi: %+v", r.Claims)
	}
	if !strings.Contains(r.compact(), "quote=1/0/0/1") {
		t.Fatalf("verify kolonu alıntıyı saymalı: %s", r.compact())
	}
}

// Zorla kipi: uyuşmayan alıntının kapanış tırnağının ardına not.
func TestSeamAnnotateQuote(t *testing.T) {
	ans := `- Log kanıtı: "Size mobil uygulamadan yardım edemiyoruz, lütfen şubeye gidin." (×1)`
	mm := verifyAnswer(verifyTierLoop, ans, quoteEvidence(), nil).mismatches()
	got := seamAnnotate(ans, mm)
	want := `gidin." ` + verifyUnverifiedNote + ` (×1)`
	if !strings.Contains(got, want) {
		t.Fatalf("not kapanış tırnağından sonra olmalı:\n%s", got)
	}
}
