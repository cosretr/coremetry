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
		"4-5 düz cümle", "80-130 kelime", "en çok 170 kelime", "arıza değil, Business hata", "süre\nkıyası", "nedenin daraldığı yer",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("istem %q taşımalı", must)
		}
	}
	if strings.Count(p, "ÖRNEK") != 1 {
		t.Error("tek örnek")
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
