package copilot

import (
	"strings"
	"testing"
)

// TestTraceNarrativePromptContract — v0.10.2480: anlatım biçimi sözleşmesi.
// Kapalı TÜR kümesi istemde aynen; üç çıktı parçası; veri-veridir ve birebir
// alıntı kuralları; tek örnek; sonda SON HATIRLATMA; bölümlü biçimden KISA.
func TestTraceNarrativePromptContract(t *testing.T) {
	p := SystemPromptTraceNarrative()
	if !strings.HasSuffix(p, AnswerInTurkish) || strings.Count(p, AnswerInTurkish) != 1 {
		t.Fatal("dil direktifiyle bir kez bitmeli")
	}
	if len(TraceNarrativeLabels) != 8 {
		t.Fatalf("TÜR kümesi 8 etiket, %d", len(TraceNarrativeLabels))
	}
	for _, l := range TraceNarrativeLabels {
		if !strings.Contains(p, "\n"+l+":") {
			t.Errorf("TÜR %q istemde tanımlı değil", l)
		}
	}
	for _, must := range []string{
		"KARAR:", "Sonraki adım:", "VERİDİR", quoteVerbatimRule, "HATA ZİNCİRİ",
		"SONUCUDUR, kök neden değil", "exception event'i", "arıza mı business hata mı",
		"bozuk DEĞİL", "En çok 4 teknik ifade", "yerel savunma düzeltmesi",
		// v0.10.2483 — iki paragraflık tek açıklama, loglar içinde.
		"iki kısa paragraf", "3-4 düz cümle", "2-4 düz cümle", "120-190 kelime", "arıza değil, Business hata",
		"nedenin daraldığı yer", "saati ve servisiyle", "Bu trace için log bulunamadı; karar yalnızca span'lere dayanıyor.",
		"Loglar okunamadı; karar yalnızca span'lere", "YAYGINLIK satırı", "sayıyı yalnız\n   oradan al",
		"Saat yalnız LOGLAR'daki", "Loglar sekmesinde",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("istem %q taşımalı", must)
		}
	}
	if strings.Count(p, "ÖRNEK") != 1 {
		t.Error("tek örnek")
	}
	// v0.10.2483 — örnek iki paragraflı ve 120-190 kelime aralığında; çıktı
	// biçimi ayrıştırıcıdan iki paragraf olarak geçer.
	ex := p[strings.Index(p, "ÖRNEK"):strings.Index(p, "SON HATIRLATMA")]
	ex = ex[strings.Index(ex, "\n")+1:]
	if w := len(strings.Fields(ex)); w < 120 || w > 190 {
		t.Errorf("örnek %d kelime (120-190)", w)
	}
	if n := ParseTraceNarrative(ex); len(n.KararParagraphs()) != 2 || n.Label != "Business hata" || n.Next == "" {
		t.Errorf("örnek iki paragraflı KARAR olmalı: %+v", n)
	}
	if i, j := strings.Index(p, "KURALLAR"), strings.Index(p, "SON HATIRLATMA"); i < 0 || j < i {
		t.Error("SON HATIRLATMA sonda")
	}
	// bölüm şablonu geri gelmesin; dış bekleme satırını sunucu koyar.
	for _, banned := range []string{"**Özet:**", "**Hata Kaynağı**", "Dayanak:", "dış bağımlılık bekleme:"} {
		if strings.Contains(p, banned) {
			t.Errorf("anlatım istemi %q taşımamalı", banned)
		}
	}
	// boyut: bölümlü istemin %60'ından kısa (v0.10.2480 ölçümü ~%45).
	if old := SystemPromptTrace(); len(p)*100 > len(old)*60 {
		t.Errorf("anlatım istemi %d bayt, bölümlü %d — %%60 tavanı aşıldı", len(p), len(old))
	}
	t.Logf("istem boyutu: bölümlü %d bayt / %d rün, anlatım %d bayt / %d rün",
		len(SystemPromptTrace()), len([]rune(SystemPromptTrace())), len(p), len([]rune(p)))
}
