package api

// insight_pods_test.go — v0.10.847 (operatör-bildirimli, PROD).
//
// Şikâyet: bir DB2 istisnasında arayüzün «Pods · nodes» paneli
// "1 pod · 799 oluşum" gösterirken "Explain root cause" cevabı kök nedeni
// yalnız şema/yetki üzerinden anlatıyordu. Sebep kanıt zinciriydi:
// exceptionEvidence fingerprint/tip/servis/trend/deploy taşıyor, pod
// dağılımını HİÇ taşımıyordu — model o paneli göremiyordu.
//
// Bu test zincirin O HALKASINI çiviler: anomaly tarafında hesaplanan
// yoğunlaşma, insight kanıtına (ve dolayısıyla karta) DEĞİŞMEDEN geçer.
// Yamasız KIRMIZI: insight.ExceptionEvidence'ta Pods alanı yoktu.
//
// GİZLİLİK: adlar sentetik (shop-payment, pod-a1, node-1).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/insight"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExceptionEvidenceCarriesPodConcentration(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC).UnixNano()
	g := &chstore.ExceptionGroup{
		Fingerprint: "fp1", Type: "SQLCODE_-204", Service: "shop-payment", State: "open",
		Occurrences: 799, FirstSeen: now - 6*3600*1e9, LastSeen: now - 120*1e9,
	}
	in := anomaly.ExceptionExplainInput{
		Pods: anomaly.PodConcentration{
			Kind: anomaly.PodConcYogunlasma, TopPod: "pod-a1", TopNode: "node-1",
			TopOccurrences: 799, Attributed: 799, Share: 1, PodsWithHits: 1, Instances: 12,
			Notes: []string{"99 oluşum pod bağlamı taşımıyor"},
		},
	}
	ev := exceptionEvidence(g, in, now)

	if ev.Pods == nil {
		t.Fatal("kanıt zinciri pod yoğunlaşmasını TAŞIMIYOR — model «1 pod · 799 oluşum» panelini göremez")
	}
	if ev.Pods.Kind != insight.PodConcYogunlasma {
		t.Errorf("kind = %q; yoğunlaşma bekleniyordu", ev.Pods.Kind)
	}
	if ev.Pods.TopPod != "pod-a1" || ev.Pods.TopNode != "node-1" {
		t.Errorf("en yoğun instance taşınmadı: %+v", ev.Pods)
	}
	if ev.Pods.Instances != 12 || ev.Pods.Attributed != 799 || ev.Pods.TopOccurrences != 799 {
		t.Errorf("payda/pay taşınmadı: %+v", ev.Pods)
	}

	// Hesaplanmamış yoğunlaşma (Kind boş) NİL kalır — kart olmayan bir
	// satırı çizmez, "ölçülemedi" ile "hiç bakılmadı" karışmaz.
	if ev2 := exceptionEvidence(g, anomaly.ExceptionExplainInput{}, now); ev2.Pods != nil {
		t.Errorf("boş girdide pod satırı uyduruldu: %+v", ev2.Pods)
	}
}

// TestPodConcKindSpellingsMatch — internal/ai/insight, chstore'a
// BAĞLANMAMAK için anomaly'nin kind sabitlerini AYNADA tutuyor
// (DeployCandidate emsali). İki yazım ayrışırsa kart "yogunlasma"
// satırını tanımaz ve sessizce çizmez; bu test o ayrışmayı yakalar.
func TestPodConcKindSpellingsMatch(t *testing.T) {
	pairs := [][2]string{
		{anomaly.PodConcYogunlasma, insight.PodConcYogunlasma},
		{anomaly.PodConcDagilmis, insight.PodConcDagilmis},
		{anomaly.PodConcOlculemedi, insight.PodConcOlculemedi},
	}
	for _, p := range pairs {
		if p[0] != p[1] {
			t.Errorf("kind yazımı ayrıştı: anomaly %q vs insight %q", p[0], p[1])
		}
	}
}

// TestExceptionSignalsRenderPodRow — kart üç kind için ÜÇ AYRI satır
// yazar ve "ölçülemedi" satırı yoğunlaşma iddiası taşımaz.
func TestExceptionSignalsRenderPodRow(t *testing.T) {
	base := insight.ExceptionEvidence{Type: "SQLCODE_-204", Service: "shop-payment", Occurrences: 799}
	val := func(pc *insight.PodConcentration) string {
		ev := base
		ev.Pods = pc
		sigs, _ := insight.ExceptionSignals(ev)
		for _, s := range sigs {
			if s.Label == "Pod dağılımı" {
				return s.Value
			}
		}
		return ""
	}
	conc := val(&insight.PodConcentration{Kind: insight.PodConcYogunlasma, TopPod: "pod-a1",
		TopOccurrences: 799, Attributed: 799, Share: 1, PodsWithHits: 1, Instances: 12})
	dag := val(&insight.PodConcentration{Kind: insight.PodConcDagilmis, TopPod: "pod-a1",
		TopOccurrences: 40, Attributed: 105, Share: 0.38, PodsWithHits: 3, Instances: 6})
	unk := val(&insight.PodConcentration{Kind: insight.PodConcOlculemedi})

	if conc == "" || dag == "" || unk == "" {
		t.Fatalf("kart satırı eksik: conc=%q dag=%q unk=%q", conc, dag, unk)
	}
	if conc == dag || dag == unk || conc == unk {
		t.Fatalf("üç kind aynı satırı üretti: %q / %q / %q", conc, dag, unk)
	}
	if strings.Contains(unk, "pod-a1") {
		t.Errorf("ölçülemedi satırı pod adı sızdırıyor: %q", unk)
	}
	if val(nil) != "" {
		t.Error("kanıt yokken kart pod satırı çizdi")
	}
}
