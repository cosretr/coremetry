package api

import (
	"reflect"
	"strings"
	"testing"
)

// numeric_claims_test.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// cevaptaki sayısal iddiaların kanıt denetimi. Hem kaçırma (uydurma sayı
// geçer) hem sahte alarm (kimlik/tarih/sürüm sayı sanılır) yönü pinli —
// sahte alarm uyarı körlüğü yaratır ve denetimi öldürür.

func TestUngroundedNumbers(t *testing.T) {
	const ev = "[K1] Sorun penceresi: istek 1234 (1.37/s), hata 36 (hata oranı %2.917), p95 234.46 ms, p99 900.7 ms; " +
		"öz süre payments 900.1 ms (öz sürenin %72.9'i); değişim +34.2%; 3 span; pencere 15 dk"
	cases := []struct {
		name   string
		answer string
		want   []string
	}{
		{"birebir", "p95 234.46 ms, p99 900.7 ms", nil},
		{"1 ondalığa yuvarlama", "p95 234.5 ms", nil},
		{"tam sayıya yuvarlama", "p95 yaklaşık 234 ms", nil},
		{"Türkçe ondalık virgül", "hata oranı %2,92", nil},
		{"Türkçe binlik nokta", "1.234 istek geldi", nil},
		{"İngilizce binlik virgül", "1,234 requests", nil},
		{"işaret yok sayılır", "p95 %34,2 arttı (+34.2%)", nil},
		{"uydurma sayı", "p95 480 ms'ye çıktı", []string{"480 ms"}},
		{"tek basamak birimli", "5 ms bekledi", []string{"5 ms"}},
		{"tek basamak düz — iddia değil", "3 servis ve 2 pod etkilendi", nil},
		{"iki basamak düz", "toplam 77 çağrı", []string{"77"}},
		{"Türkçe ek", "hataların 48'i payments'ta", []string{"48"}},
		{"yüzde öneki", "hata oranı %45", []string{"%45"}},
		{"tekrar tekil", "480 ms ... yine 480 ms", []string{"480 ms"}},
		{"ikiden fazla ondalık birebir ister", "0.123 s", []string{"0.123 s"}},
		{"birim dönüşümü kabul edilmez", "p99 0,9 s", []string{"0,9 s"}},
		{"kimlik/etiket sayı değil", "[T1] [K12] p95 p99 5xx v2 trace 4bf92f3577b34da6a3ce929d0e0e4736", nil},
		{"tarih ve saat", "2026-09-26T10:04:05Z ve 14:32'de başladı", nil},
		{"sürüm dizgesi", "sürüm 1.4.2 → 10.0.12", nil},
		{"bağlantı", "bkz. /trace?id=abc&span=1234567890abcdef ve https://example.test/x/99", nil},
		{"kanıttaki pencere", "15 dk pencerede", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ungroundedNumbers(c.answer, ev)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ungroundedNumbers(%q) = %q; want %q", c.answer, got, c.want)
			}
		})
	}
}

// v0.10.948 — kanıt tarafı kimlik/tarih rakamlarını DAYANAK saymaz. Eskiden
// evidenceNumbers her rakam dizisini alıyordu: trace id'nin "3577"si, pod
// hash'in "7"si, zaman damgasının günü/dakikası/saniyesi uydurma sayıyı
// "kanıtlı" gösteriyordu. İki koruma: birimli bitişik yazım ("480ms") ve
// sıkışık JSON kanıtı (bağlantı maskesi yok) sahte uyarı üretmez.
func TestUngroundedNumbersEvidenceIdentifiers(t *testing.T) {
	const ev = "[T1] trace 4bf92f3577b34da6a3ce929d0e0e4736 · pod checkout-7d9f8c6b5-x2k4p · " +
		"başlangıç 2026-09-26T08:03:41.512Z · bitiş 2026-09-26T08:56:12Z"
	cases := []struct {
		name, answer, evidence string
		want                   []string
	}{
		{"trace id rakam dizisi", "p99 3577 ms", ev, []string{"3577 ms"}},
		{"trace id iki basamak", "hata oranı %92", ev, []string{"%92"}},
		{"tarihin günü", "istek sayısı 26", ev, []string{"26"}},
		{"saniyenin yuvarlanmışı", "%42", ev, []string{"%42"}},
		{"dakika", "%56", ev, []string{"%56"}},
		{"yalnız pod hash'te geçen değer", "bekleme 7 ms", ev, []string{"7 ms"}},
		{"koruma: bitişik birim kanıtta", "480 ms", "GET /api/orders took 480ms", nil},
		{"koruma: sıkışık JSON kanıtı", "480,2 ms", `{"http_route":"/api/v1/orders","duration_ms":480.2}`, nil},
		// v0.10.948 — sıkışık JSON dizisi tek jeton okunuyordu (120340560 / 12.34):
		// öğeleri doğru aktaran takip cevabı yanlış uyarı alıyordu.
		{"koruma: sıkışık JSON sayı dizisi", "p99 değerleri 340 ve 34 ms", `{"sparkline":[120,340,560],"p99Sparkline":[12,34]}`, nil},
		{"koruma: ondalık dizi", "34,25 ms", `{"v":[12.5,34.25]}`, nil},
		{"düzyazı ondalığı parçalanmaz", "6 ms", "hata oranı %3,6", []string{"6 ms"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ungroundedNumbers(c.answer, c.evidence)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ungroundedNumbers(%q) = %q; want %q", c.answer, got, c.want)
			}
		})
	}
}

// Kanıtın kendi değerleri (JSON anahtarı, kimlik öneki, sürüm) listede tekil ve doğru.
func TestEvidenceNumbers(t *testing.T) {
	got := evidenceNumbers(`p95 480.2 ms [K1] {"p99_ms":900} 4bf92f35 v2 1.4.2 2026-09-26T08:03:41Z %12 30s`)
	want := []float64{480.2, 900, 12, 30}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("evidenceNumbers = %v; want %v", got, want)
	}
}

func TestNumCandidates(t *testing.T) {
	cases := []struct {
		tok  string
		want []numCand
	}{
		{"1234", []numCand{{1234, 0}}},
		{"12.50", []numCand{{12.5, 2}}},
		{"1,5", []numCand{{1.5, 1}}},
		{"1.234", []numCand{{1234, 0}, {1.234, 3}}}, // binlik mi ondalık mı belirsiz: ikisi de
		{"1.234,56", []numCand{{1234.56, 2}}},
		{"1,234.56", []numCand{{1234.56, 2}}},
		{"1.234.567", []numCand{{1234567, 0}}},
		{"1.4.2", nil},    // sürüm
		{"10.0.0.1", nil}, // IP
	}
	for _, c := range cases {
		if got := numCandidates(c.tok); !reflect.DeepEqual(got, c.want) {
			t.Errorf("numCandidates(%q) = %v; want %v", c.tok, got, c.want)
		}
	}
}

func TestNumericClaimWarningTR(t *testing.T) {
	if numericClaimWarningTR(nil) != "" {
		t.Fatal("dayanaksız sayı yokken uyarı basıldı")
	}
	w := numericClaimWarningTR([]string{"480 ms", "%45"})
	if !strings.HasPrefix(w, "⚠ Kanıtta bulunamayan sayı(lar): 480 ms, %45") {
		t.Fatalf("uyarı biçimi: %q", w)
	}
	many := []string{"11", "12", "13", "14", "15", "16", "17", "18", "19", "20"}
	if w := numericClaimWarningTR(many); !strings.Contains(w, "18, …") || strings.Contains(w, "19") {
		t.Fatalf("uzun liste %d değerde kesilmeli: %q", numericClaimMaxListed, w)
	}
}
