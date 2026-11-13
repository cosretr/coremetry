package evaluator

// selfhealth_volume_test.go — v0.9.1294, hacim sıçraması kuralının saf
// çekirdekleri.
//
// Bu kuralın YANLIŞ POZİTİF riski kardeşlerinden yüksek, çünkü tetikleyicisi
// bir arıza değil bir ORAN: her küçük servis günün birinde 10× dalgalanır.
// Kapıların üçü de ayrı ayrı pinlenir.
//
// MUTASYON KANITI (volumeSpikeDecision): `if cur < minSpans { return false }`
// satırı SİLİNDİĞİNDE (dosya derlenir hâlde kalır) "küçük servis 10× ama az
// hacim" ve "3 span → 30 span" vakaları kızarır. Çalıştırıldı, geri alındı.
//
// İKİNCİ MUTASYON (escalationExempt): `ruleID == selfVolumeRuleID` →
// `false` yapmak TestVolumeSpikeNeverEscalates'i kızartır — ve kızartan
// assert, korkuyu ölçen assert: yaş-eskalasyonu bu kuralı critical'a, oradan
// da P1'e taşıyordu.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestVolumeSpikeDecision(t *testing.T) {
	const (
		minSpans = uint64(100_000)
		factor   = 4.0
	)
	tests := []struct {
		name      string
		cur, prev uint64
		want      bool
	}{
		// NORMAL BÜYÜME — yerel demoda ölçülen gerçek bant (1.19-1.30×).
		{"normal gün-içi büyüme (1.2×)", 115_936, 95_549, false},
		{"pencere ortasında doğan servis (2.42×)", 239_090, 98_630, false},
		{"eşiğin hemen altı (3.9×)", 390_000, 100_000, false},

		// ASIL VAKA.
		{"sıçrama (5.7×)", 1_200_000, 210_000, true},
		{"eşik TAM (4.0×)", 1_000_000, 250_000, true},

		// YENİ SERVİS: önceki pencerede HİÇ veri yok. Yerel ölçümde
		// gerçek vaka — portfolio-service 96.146 / 0. Kapı olmasa
		// 96.146× ile alarm açardı.
		{"yeni servis (önceki 0) → alarm YOK", 96_146, 0, false},
		{"yeni servis, devasa hacim → yine alarm YOK", 50_000_000, 0, false},

		// TABAN GÜRÜLTÜ KAPISI: oran kocaman, hacim önemsiz.
		{"küçük servis 10× ama az hacim", 30_000, 3_000, false},
		{"3 span → 30 span", 30, 3, false},
		{"tabanın bir altı, oran devasa", 99_999, 100, false},
		{"taban TAM + oran ihlali", 100_000, 25_000, true},

		// Hacim büyük ama oran yok: kural sessiz. (Bu kural "çok span
		// yazıyorsun" demiyor, "DÜN'e göre sıçradın" diyor.)
		{"devasa hacim, düz seyir", 900_000_000, 890_000_000, false},
		// Hacim düştü: sıçrama değil.
		{"hacim yarıya indi", 500_000, 1_000_000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := volumeSpikeDecision(tt.cur, tt.prev, minSpans, factor); got != tt.want {
				t.Fatalf("volumeSpikeDecision(cur=%d,prev=%d) = %v, beklenen %v",
					tt.cur, tt.prev, got, tt.want)
			}
		})
	}

	// Katsayı 0/negatif = kural KAPALI. patchSelfHealth normalde 0'ı
	// varsayılana çeviriyor; bu, saf fonksiyonun kendi güvenliği
	// (spoolBreachDecision'ın sıfır-eşik dalıyla aynı duruş).
	if volumeSpikeDecision(10_000_000, 1, minSpans, 0) {
		t.Fatal("katsayı 0'da ihlal iddia edildi")
	}
	if volumeSpikeDecision(10_000_000, 1, minSpans, -1) {
		t.Fatal("negatif katsayıda ihlal iddia edildi")
	}
	// Taban 0: kapı kapalı demek DEĞİL — o zaman yalnız oran konuşur.
	// (Operatör vidasında 0 asla oluşmaz, patch varsayılana çevirir.)
	if !volumeSpikeDecision(30, 3, 0, 4) {
		t.Fatal("taban 0'da oran kapısı da susturuldu")
	}
}

// Yaş-eskalasyonu bu kuralda KAPALI. Test önce TUZAĞIN GERÇEK olduğunu
// gösteriyor (mekanizma), sonra muafiyetin onu kestiğini.
func TestVolumeSpikeNeverEscalates(t *testing.T) {
	esc := chstore.DefaultProblemEscalation()

	// (1) Tuzak gerçek mi: eskalasyon açıkken 24 saat açık kalan bir
	// warning critical'a çıkıyor. Hacim sıçraması TANIMI GEREĞİ en az 24
	// saat açık kalır (pencereler ancak bir gün sonra eşitlenir).
	if got := nextSeverity("warning", 24*time.Hour, esc); got != "critical" {
		t.Fatalf("mekanizma değişmiş: nextSeverity(warning, 24h) = %q, "+
			"critical bekleniyordu — muafiyetin gerekçesini gözden geçir", got)
	}

	// (2) Muafiyet bu kuralı ve SLO burn-rate ailesini (v0.10.800) kesiyor —
	// başka hiçbir kuralı değil.
	if !escalationExempt(selfVolumeRuleID) {
		t.Fatal("self-volume-spike eskalasyondan muaf değil: birkaç saat içinde critical→P1 olur")
	}
	for _, rule := range []string{"slo:abc:warning", "slo:abc:critical"} {
		if !escalationExempt(rule) {
			t.Fatalf("%q muaf değil — SLO şiddeti politikadan gelir, yaşla artmaz (S1)", rule)
		}
	}
	for _, rule := range []string{
		selfIngestRuleID, selfSpoolRuleID, selfDiskRuleID, selfChannelRuleID,
		"exception-storm", "anomaly-auto:abc", "", "slo", "xslo:abc:warning",
	} {
		if escalationExempt(rule) {
			t.Fatalf("%q muaf sayıldı — muafiyet self-volume-spike ve slo: önekine özel olmalı", rule)
		}
	}
}
