package devops

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/stackparse"
)

// ---------------------------------------------------------------
// v0.10.1040 — "depo ağacı … kesildi" notu YALNIZ ıskada.
//
// Operatör: "'Depo ağacı … kesildi' uyarısı yalnız dosya bulunamadığında
// çıksın." AI panelinin kod incelemesinde not, her frame dosyasına
// eşlendiğinde de basılıyordu (panel Reason'ı + model bloğu) — hiçbir
// kaybı açıklamayan saf gürültü. Aşağıdaki kapılar üç mutasyona kırmızı
// yanar:
//   (1) FetchCode'daki treeCapNoteApplies koşulunu kaldır → "her frame
//       eşlendi" satırları
//   (2) notu hiç basma → "bir frame ıskaladı" satırı (ve v0.9.1269'un
//       TestCappedTreeMissReasonIsHonest'i)
//   (3) ıskayı hunt.misses'ten say ("okunamadı" da sayılır) → "yol
//       bulundu ama okunamadı" satırı
// ---------------------------------------------------------------

func TestTreeCapNoteApplies(t *testing.T) {
	tests := []struct {
		name   string
		capped bool
		missed int
		want   bool
	}{
		{"tam ağaç, ıska yok", false, 0, false},
		{"tam ağaç, ıska var — kesilme yok, açıklanacak bir şey yok", false, 2, false},
		{"kesik ağaç, her frame eşlendi", true, 0, false},
		{"kesik ağaç, bir frame ıskaladı", true, 1, true},
		{"kesik ağaç, çok ıska", true, 5, true},
		{"kesik ağaç, negatif sayaç (savunma)", true, -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := treeCapNoteApplies(tt.capped, tt.missed); got != tt.want {
				t.Fatalf("treeCapNoteApplies(%v, %d) = %v, istenen %v", tt.capped, tt.missed, got, tt.want)
			}
		})
	}
}

// capNoteFrames — CardDetailBusiness (her zaman ağaçta) + verilen ikinci
// uygulama frame'i. İkisi de aynı segmentte: RankFrames metin sırasını
// korur, ilk frame pencere alır, ikincisi ıska/okuma yolunu sürer.
func capNoteFrames(second string) []stackparse.Frame {
	return stackparse.ParseJava("" +
		"jakarta.ejb.EJBException: host response error\n" +
		"\tat deployment.APPWEB.war//com.example.card.CardDetailBusiness.handleHostResponseError(CardDetailBusiness.java:246)\n" +
		"\tat deployment.APPWEB.war//" + second + "\n")
}

func TestFetchCodeTreeCapNoteOnlyOnMiss(t *testing.T) {
	const (
		biz    = "/src/main/java/com/example/card/CardDetailBusiness.java"
		reader = "/src/main/java/com/example/card/CardReader.java"
	)
	filler := []string{"/README.md", "/docs/a.md", "/docs/b.md", "/docs/c.md", "/docs/d.md", "/docs/e.md", "/docs/f.md"}
	with := func(head ...string) []string { return append(append([]string{}, head...), filler...) }
	// missFrames — ikinci frame'in dosyası depoda HİÇ yok: tam ağaçta da,
	// kapsamlı denemede de ıskalar.
	missFrames := func() []stackparse.Frame {
		return capNoteFrames("com.example.card.CardValidator.validate(CardValidator.java:88)")
	}

	tests := []struct {
		name     string
		tree     []string
		maxPaths int // 0 = ürün tavanı (bu ağaçlar kesilmez)
		frames   []stackparse.Frame
		// capped — ağaç bu kurulumda kesik mi. Notun YOKLUĞUNU ölçen kesik
		// satırlar aynı servisle bir kontrol çağrısı daha yapar: ıskalayan
		// frame notu basmalı, yoksa "not yok" kuraldan değil kesilmemiş
		// ağaçtan geliyor olurdu.
		capped   bool
		wantNote bool
		wants    []string
	}{
		{
			name: "kesik ağaç, frame ağaçta doğrudan eşlendi",
			tree: with(biz), maxPaths: 5, frames: scopedFrames(), capped: true,
		},
		{
			// v0.9.1269 senaryosu: dosya tavanın ötesinde, kapsamlı deneme
			// buldu — ıska yok, not da yok.
			name: "kesik ağaç, frame kapsamlı denemeyle bulundu",
			tree: append(append([]string{}, filler...), biz), maxPaths: 5, frames: scopedFrames(), capped: true,
		},
		{
			name: "kesik ağaç, bir frame ıskaladı — not bugünkü gibi",
			tree: with(biz), maxPaths: 5, frames: missFrames(), capped: true, wantNote: true,
			wants: []string{
				"eşleşmeyen: CardValidator.java",
				"yolda kesildi (tavan 5)", "kesik bölgede olabilir", "kapsamlı denemeler:",
				"/src/main/java/com/example/card",
			},
		},
		{
			// Yol AĞAÇTA bulundu, çekim 404: kesilme bunu açıklayamaz.
			name: "kesik ağaç, yol bulundu ama okunamadı",
			tree: with(biz, reader), maxPaths: 5, capped: true,
			frames: capNoteFrames("com.example.card.CardReader.read(CardReader.java:30)"),
			wants:  []string{"CardReader.java (okunamadı)"},
		},
		{
			name: "tam ağaç, frame ıskaladı",
			tree: with(biz), frames: missFrames(),
			wants: []string{"eşleşmeyen: CardValidator.java"},
		},
		{
			name: "tam ağaç, her frame eşlendi",
			tree: with(biz), frames: scopedFrames(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeTFS(t)
			f.tree = tt.tree
			f.files[biz] = javaFile("com.example.card", "CardDetailBusiness", 400, 246)
			svc := New()
			svc.Configure(f.settings())
			if tt.maxPaths > 0 {
				svc.treeMaxPaths = tt.maxPaths
			}
			cc := svc.FetchCode(context.Background(), "core-service", ProjectHint{}, tt.frames, nil, nil)
			if len(cc.Windows) != 1 || cc.Windows[0].Path != biz {
				t.Fatalf("CardDetailBusiness penceresi beklenirdi, %d pencere (reason=%q)", len(cc.Windows), cc.Reason)
			}
			if got := strings.Contains(cc.Reason, "kesildi"); got != tt.wantNote {
				t.Fatalf("kesilme notu=%v, istenen %v (reason=%q)", got, tt.wantNote, cc.Reason)
			}
			for _, w := range tt.wants {
				if !strings.Contains(cc.Reason, w) {
					t.Fatalf("Reason %q içermiyor: %q", w, cc.Reason)
				}
			}
			// Model bloğu Reason'ı taşımaz; yine de not oraya sızmasın.
			if !tt.wantNote && strings.Contains(cc.PromptBlock(), "kesildi") {
				t.Fatalf("model bloğunda kesilme notu: %q", cc.PromptBlock())
			}
			if !tt.capped {
				f.mu.Lock()
				scoped := f.hits["scoped"]
				f.mu.Unlock()
				if scoped != 0 {
					t.Fatalf("tam ağaçta kapsamlı istek çıkmamalıydı, %d çıktı", scoped)
				}
				return
			}
			if !tt.wantNote {
				ctl := svc.FetchCode(context.Background(), "core-service", ProjectHint{}, missFrames(), nil, nil)
				// Not metni bugünkü varyantlardan biri olabilir ("kesik bölgede
				// olabilir" ya da "N dosya kapsamlı aramayla bulundu"); burada
				// yalnız notun VARLIĞI ölçülür.
				if !strings.Contains(ctl.Reason, "yolda kesildi") {
					t.Fatalf("kontrol: aynı (kesik) ağaçta ıskalayan frame notu basmalıydı: %q", ctl.Reason)
				}
			}
		})
	}
}
