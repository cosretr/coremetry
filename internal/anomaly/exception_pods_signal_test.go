package anomaly

// exception_pods_signal_test.go — v0.10.847 (operatör-bildirimli, PROD):
// "Explain root cause" cevabı POD YOĞUNLAŞMASINI görmüyordu. Ekranda
// «1 pod · 799 oluşum» yazarken model şema/yetki anlatıyordu, çünkü
// kanıt zinciri pod/instance dağılımını modele HİÇ taşımıyordu.
//
// Bu dosya sinyalin DÜRÜSTLÜK sözleşmesini çiviler: "1 pod" tek başına
// bir sinyal DEĞİL — payda (servisin o pencerede kaç instance'ı vardı)
// ölçülmeden yoğunlaşma İDDİASI üretilemez, ve ölçülemeyen bir payda
// asla "dağılmış" gibi okunamaz.
//
// GİZLİLİK: tüm adlar sentetik (shop-payment, pod-a1, node-1, ns-shop).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func podRow(pod string, occ int64) chstore.ExceptionPodRow {
	return chstore.ExceptionPodRow{
		Cluster: "cl-1", Namespace: "ns-shop", Pod: pod, Node: "node-1",
		Occurrences: occ, LastSeen: time.Unix(1_700_000_000, 0).UTC(),
	}
}

// TestPodConcentrationDecision — karar tablosu. Payda HER vakada açıkça
// verilir: sinyalin varlığı paydaya bağlı olduğu için "payda yok" ile
// "payda 1" aynı satırda olamaz.
func TestPodConcentrationDecision(t *testing.T) {
	cases := []struct {
		name     string
		pods     chstore.ExceptionPods
		denom    PodDenominator
		wantKind string
		wantNote string // Notes içinde aranacak alt dize ("" = arama yok)
	}{
		{
			// Operatörün vakası: tüm oluşumlar tek pod'da VE servisin
			// 12 instance'ı var → güçlü sinyal.
			name:     "tek pod, payda 12 → yoğunlaşma",
			pods:     chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcYogunlasma,
		},
		{
			// Aynı numerator, payda 1 → HİÇBİR ŞEY ifade etmez.
			name:     "tek pod, payda 1 → sinyal yok",
			pods:     chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799},
			denom:    PodDenominator{Instances: 1, Measured: true},
			wantKind: PodConcDagilmis,
			wantNote: "TEK instance",
		},
		{
			name: "dağılmış → sinyal yok",
			pods: chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
				podRow("pod-a1", 40), podRow("pod-a2", 35), podRow("pod-a3", 30),
			}, Total: 105},
			denom:    PodDenominator{Instances: 6, Measured: true},
			wantKind: PodConcDagilmis,
		},
		{
			name:     "payda ölçülemedi → ölçülemedi (dağılmış DEĞİL)",
			pods:     chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799},
			denom:    PodDenominator{},
			wantKind: PodConcOlculemedi,
			wantNote: "payda",
		},
		{
			name:     "SchemaMissing → ölçülemedi",
			pods:     chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{}, SchemaMissing: true},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcOlculemedi,
			wantNote: "k8s",
		},
		{
			name: "Sampled → karar yine verilir ama oran YAKLAŞIK ilan edilir",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 2900)}, Total: 2900, Sampled: true},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcYogunlasma,
			wantNote: "YAKLAŞIK",
		},
		{
			name: "Truncated → pod tavanı ilan edilir",
			pods: chstore.ExceptionPods{
				Rows:  []chstore.ExceptionPodRow{podRow("pod-a1", 900), podRow("pod-a2", 10)},
				Total: 910, Truncated: true},
			denom:    PodDenominator{Instances: 60, Measured: true},
			wantKind: PodConcYogunlasma,
			wantNote: "tavanı doldu",
		},
		{
			name: "NoContext > 0 → paydanın eksik olduğu ilan edilir",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 700)}, NoContext: 99, Total: 799},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcYogunlasma,
			wantNote: "pod bağlamı taşımıyor",
		},
		{
			name: "tüm oluşumlar bağlamsız → ölçülemedi",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{}, NoContext: 799, Total: 799},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcOlculemedi,
			wantNote: "pod bağlamı",
		},
		{
			name: "HostOnly → Kubernetes pod'u DEĞİL, ayrı ilan",
			pods: chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
				{Cluster: "cl-1", Pod: "host-a1", Occurrences: 799, HostOnly: true}}, Total: 799},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcYogunlasma,
			wantNote: "host.name",
		},
		{
			name: "az oluşum → iddia edilemez (ölçülemedi)",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 3)}, Total: 3},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcOlculemedi,
			wantNote: "yetersiz",
		},
		{
			name: "payda gözlemden küçük → iki okuma çelişiyor, ölçülemedi",
			pods: chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
				podRow("pod-a1", 400), podRow("pod-a2", 399)}, Total: 799},
			denom:    PodDenominator{Instances: 1, Measured: true},
			wantKind: PodConcOlculemedi,
			wantNote: "çelişiyor",
		},
		{
			name: "SchemaMissing + Sampled + payda → yine ölçülemedi (şema kapısı önce)",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799,
				SchemaMissing: true, Sampled: true},
			denom:    PodDenominator{Instances: 12, Measured: true},
			wantKind: PodConcOlculemedi,
			wantNote: "k8s",
		},
		{
			name: "payda tavanda → instance sayısı 'en az' olarak ilan edilir",
			pods: chstore.ExceptionPods{
				Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799},
			denom:    PodDenominator{Instances: 500, Measured: true, AtCap: true},
			wantKind: PodConcYogunlasma,
			wantNote: "tavanına dayandı",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := podConcentration(tc.pods, tc.denom)
			if got.Kind != tc.wantKind {
				t.Fatalf("kind = %q; %q bekleniyordu (notlar: %v)", got.Kind, tc.wantKind, got.Notes)
			}
			if tc.wantNote != "" && !strings.Contains(strings.Join(got.Notes, " | "), tc.wantNote) {
				t.Fatalf("notlarda %q yok: %v", tc.wantNote, got.Notes)
			}
			// ÖLÇÜLEMEDİ dalı hiçbir oran/pod SIZDIRMAZ: sızdırırsa
			// renderlayıcı "ima etmeme" sözleşmesini tutamaz.
			if got.Kind == PodConcOlculemedi && (got.Share != 0 || got.TopPod != "") {
				t.Fatalf("ölçülemedi dalı oran/pod sızdırıyor: %+v", got)
			}
		})
	}
}

// TestPodConcentrationShareMath — eşik SINIRI. Eşiğin altı ve üstü ayrı
// kararlar olmalı; yuvarlama yukarı YAPILMAZ (%99.6 asla "%100" değil).
func TestPodConcentrationShareMath(t *testing.T) {
	below := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
		podRow("pod-a1", 79), podRow("pod-a2", 21)}, Total: 100}
	above := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
		podRow("pod-a1", 80), podRow("pod-a2", 20)}, Total: 100}
	d := PodDenominator{Instances: 8, Measured: true}
	if k := podConcentration(below, d).Kind; k != PodConcDagilmis {
		t.Errorf("eşiğin ALTI (%%79) = %q; dağılmış bekleniyordu", k)
	}
	if k := podConcentration(above, d).Kind; k != PodConcYogunlasma {
		t.Errorf("eşiğin ÜSTÜ (%%80) = %q; yoğunlaşma bekleniyordu", k)
	}
	// En yoğun satır giriş SIRASINDAN bağımsız seçilir (girdi sıralı
	// gelmeyebilir; saf fonksiyon çağıranın sırasına GÜVENMEZ).
	rev := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
		podRow("pod-a2", 5), podRow("pod-a1", 95)}, Total: 100}
	if c := podConcentration(rev, d); c.TopPod != "pod-a1" {
		t.Errorf("en yoğun pod = %q; pod-a1 bekleniyordu", c.TopPod)
	}
	// %99.6 → floor: "%100" yazmak "hepsi" demektir ve yanlıştır.
	almost := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
		podRow("pod-a1", 996), podRow("pod-a2", 4)}, Total: 1000}
	if txt := renderPodConcentration(podConcentration(almost, d)); strings.Contains(txt, "%100") {
		t.Errorf("%%99.6 yukarı yuvarlanıp %%100 yazıldı: %s", txt)
	}
}

// TestRenderPodConcentrationThreeSentences — üç kind ÜÇ AYRI cümle
// üretir ve "ölçülemedi" dalı yoğunlaşma İDDİASI taşımaz. Bu, şikâyetin
// ikinci yarısı: sinyali modele koymak yetmez, ölçülemeyen hâlin
// "dağılmış" gibi okunmaması gerekir.
func TestRenderPodConcentrationThreeSentences(t *testing.T) {
	pods := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{podRow("pod-a1", 799)}, Total: 799}
	spread := chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{
		podRow("pod-a1", 40), podRow("pod-a2", 35), podRow("pod-a3", 30)}, Total: 105}

	conc := renderPodConcentration(podConcentration(pods, PodDenominator{Instances: 12, Measured: true}))
	dag := renderPodConcentration(podConcentration(spread, PodDenominator{Instances: 6, Measured: true}))
	unk := renderPodConcentration(podConcentration(pods, PodDenominator{}))

	for _, s := range []string{conc, dag, unk} {
		if strings.TrimSpace(s) == "" {
			t.Fatal("boş renderlama — model hiçbir şey görmez")
		}
	}
	if conc == dag || dag == unk || conc == unk {
		t.Fatalf("üç kind aynı cümleyi üretti:\nconc=%s\ndag=%s\nunk=%s", conc, dag, unk)
	}
	// Yoğunlaşma cümlesi PAYDAYI da yazar — "1 pod" paydasız anlamsız.
	if !strings.Contains(conc, "12") || !strings.Contains(conc, "pod-a1") {
		t.Errorf("yoğunlaşma cümlesi payda/pod taşımıyor: %s", conc)
	}
	// ÖLÇÜLEMEDİ cümlesi: iddia YOK, "dağılmış değil" uyarısı VAR.
	if strings.Contains(unk, "pod-a1") || strings.Contains(unk, "TEK") {
		t.Errorf("ölçülemedi dalı yoğunlaşma iddiası taşıyor: %s", unk)
	}
	if !strings.Contains(unk, "ANLAMINA GELMEZ") {
		t.Errorf("ölçülemedi dalı 'dağılmış değil' uyarısını taşımıyor: %s", unk)
	}
	// Her üç cümle de GÖZLEM olarak etiketlenir (sonuç değil).
	for _, s := range []string{conc, dag, unk} {
		if !strings.Contains(s, "GÖZLEM") {
			t.Errorf("gözlem etiketi yok: %s", s)
		}
	}
	// Kind boşsa (hiç hesaplanmadıysa) blok BASILMAZ — eski prompt
	// bayt-paritesi.
	if renderPodConcentration(PodConcentration{}) != "" {
		t.Error("hesaplanmamış yoğunlaşma prompt'a blok bastı")
	}
}

// ── okuma seam'i ────────────────────────────────────────────────────

type fakePodReader struct {
	pods    chstore.ExceptionPods
	podsErr error
	seen    []chstore.EntitySeenAgg
	seenErr error
	seenHit int
	gotClus []string
}

func (f *fakePodReader) GetExceptionGroupPods(ctx context.Context, fp string) (chstore.ExceptionPods, error) {
	return f.pods, f.podsErr
}

func (f *fakePodReader) EntitySeenForService(ctx context.Context, service string, clusterValues []string, from, to time.Time) ([]chstore.EntitySeenAgg, error) {
	f.seenHit++
	f.gotClus = clusterValues
	return f.seen, f.seenErr
}

// TestBuildPodConcentrationReadFailures — okuma DÜŞERSE explain çalışmaya
// devam eder, yalnız bu kanıt "ölçülemedi" olur (v0.10.820 sözleşmesi:
// doğrulama okuması hatası ≠ iş başarısız).
func TestBuildPodConcentrationReadFailures(t *testing.T) {
	ctx := context.Background()
	ok := chstore.ExceptionPods{
		Rows:  []chstore.ExceptionPodRow{podRow("pod-a1", 799)},
		Total: 799,
		From:  time.Unix(1_700_000_000, 0).UTC(), To: time.Unix(1_700_003_600, 0).UTC(),
	}
	seen12 := make([]chstore.EntitySeenAgg, 12)

	t.Run("pod okuması düşer", func(t *testing.T) {
		f := &fakePodReader{podsErr: errors.New("ch down")}
		c := buildPodConcentration(ctx, f, "fp1", "shop-payment")
		if c.Kind != PodConcOlculemedi {
			t.Fatalf("kind = %q; ölçülemedi bekleniyordu", c.Kind)
		}
		if f.seenHit != 0 {
			t.Error("numerator düşmüşken payda sorgusu yine de koştu — boşa maliyet")
		}
	})

	t.Run("payda okuması düşer", func(t *testing.T) {
		f := &fakePodReader{pods: ok, seenErr: errors.New("entity_seen_5m yok")}
		c := buildPodConcentration(ctx, f, "fp1", "shop-payment")
		if c.Kind != PodConcOlculemedi {
			t.Fatalf("kind = %q; payda düştüğünde ölçülemedi bekleniyordu", c.Kind)
		}
	})

	t.Run("payda boş döner → ölçülemedi (0 DEĞİL)", func(t *testing.T) {
		f := &fakePodReader{pods: ok}
		if c := buildPodConcentration(ctx, f, "fp1", "shop-payment"); c.Kind != PodConcOlculemedi {
			t.Fatalf("kind = %q; boş payda ölçülemedi olmalı", c.Kind)
		}
	})

	t.Run("iki okuma da çalışır → yoğunlaşma + cluster kapsamı taşınır", func(t *testing.T) {
		f := &fakePodReader{pods: ok, seen: seen12}
		c := buildPodConcentration(ctx, f, "fp1", "shop-payment")
		if c.Kind != PodConcYogunlasma {
			t.Fatalf("kind = %q; yoğunlaşma bekleniyordu (notlar %v)", c.Kind, c.Notes)
		}
		if c.Instances != 12 {
			t.Errorf("payda = %d; 12 bekleniyordu", c.Instances)
		}
		// Payda, numerator'ın GÖRDÜĞÜ cluster'larla SINIRLI olmalı;
		// yoksa aynı adı taşıyan başka cluster'ın pod'ları paydayı
		// şişirir ve yoğunlaşma iddiası uydurulmuş olur.
		if len(f.gotClus) != 1 || f.gotClus[0] != "cl-1" {
			t.Errorf("payda cluster kapsamı = %v; [cl-1] bekleniyordu", f.gotClus)
		}
	})

	t.Run("pod satırı yokken payda sorgusu HİÇ koşmaz", func(t *testing.T) {
		f := &fakePodReader{pods: chstore.ExceptionPods{Rows: []chstore.ExceptionPodRow{}, NoContext: 5, Total: 5}}
		if c := buildPodConcentration(ctx, f, "fp1", "shop-payment"); c.Kind != PodConcOlculemedi {
			t.Fatalf("kind = %q", c.Kind)
		}
		if f.seenHit != 0 {
			t.Error("pod satırı yokken payda sorgusu koştu — gereksiz CH okuması")
		}
	})
}
