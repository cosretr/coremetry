package copilot

import (
	"strings"
	"testing"
)

func TestParseTraceNarrative(t *testing.T) {
	in := "dış bağımlılık bekleme: POST /charge 12.21 s\n\n**Business hata**\n**KARAR:** orders-api isteği 400 ile bitti.\nİkinci cümle.\nKod bulgusu: OrderMapper.java:42 — tutar boş geçiyor.\nSonraki adım: Kanala bakın."
	n := ParseTraceNarrative(in)
	if n.Label != "Business hata" || n.Karar != "orders-api isteği 400 ile bitti. İkinci cümle." ||
		n.Code != "OrderMapper.java:42 — tutar boş geçiyor." || n.Next != "Kanala bakın." {
		t.Fatalf("%+v", n)
	}
	// işaretsiz KARAR + parantezli etiket
	n = ParseTraceNarrative("Zaman aşımı (deadline)\nÇağrı 5 s sonra kesildi.\nSonraki adım: x")
	if n.Label != "Zaman aşımı" || n.Karar != "Çağrı 5 s sonra kesildi." || n.Code != "" {
		t.Fatalf("%+v", n)
	}
	if ParseTraceNarrative("**Özet:** bölümlü cevap").Label != "" {
		t.Fatal("bölümlü cevapta etiket yok")
	}
	if got := ParseTraceNarrative(in).PlainText(); !strings.HasPrefix(got, "Business hata\nKARAR: ") || !strings.Contains(got, "\nKod bulgusu: ") {
		t.Fatal(got)
	}
	for k := range TraceNarrativeLabelKeys {
		if MatchTraceNarrativeLabel(k) != k {
			t.Errorf("anahtar tablosunda kümede olmayan etiket %q", k)
		}
	}
	if len(TraceNarrativeLabelKeys) != len(TraceNarrativeLabels) {
		t.Fatal("her etiketin anahtarı olmalı")
	}
}

func TestCountNarrativeSentences(t *testing.T) {
	for in, want := range map[string]int{
		"":                               0,
		"Bir. İki! Üç?":                  3,
		"Süre 12.21 s oldu. Sonra bitti": 2,
		`Mesaj "amount must be positive. x" dedi. İkinci.`: 2,
		"Kısaltma ör. bağımlılık ayakta. Son.":             2,
		"İstek 30 ms. Bitti.":                              2,
	} {
		if got := CountNarrativeSentences(in); got != want {
			t.Errorf("%q: %d, want %d", in, got, want)
		}
	}
}

func TestCountCodeTokens(t *testing.T) {
	s := `orders-api POST /orders 400 döndü; http.status_code=400 {"a":1} com.acme.orders.Svc getName 4bf92f3577b34da6 "amount must be positive"`
	if got := CountCodeTokens(s, nil); got != 5 {
		t.Fatalf("got %d", got)
	}
	if got := CountCodeTokens(`"user_id is null" döndü`, []string{"user_id is null"}); got != 0 {
		t.Fatalf("izinli alıntı sayılmamalı: %d", got)
	}
}

// TestTraceNarrativeFollowUpAndCodePrompts — v0.10.2482: anlatım takip eki
// bağlam/araç/neden kurallarını bölümlü ekle AYNI taşır, yalnız cevap
// şablonu değişir; kod dalı anlatımı dört parça ister ve kodu yeniden yazdırmaz.
func TestTraceNarrativeFollowUpAndCodePrompts(t *testing.T) {
	for _, simple := range []bool{false, true} {
		old, nar := TraceFollowUpAddendumFor(simple), TraceFollowUpAddendumNarrativeFor(simple)
		head, _, ok := strings.Cut(old, ToolsetText(traceFollowUpSectionsMarker, simple))
		if !ok || !strings.HasPrefix(nar, head) {
			t.Fatalf("simple=%v: anlatım eki bölümlü ekin başını aynen taşımalı", simple)
		}
		for _, must := range []string{"HATA ZİNCİRİ bloğu varsa", "KRİTİK YOL", "SPAN EVENT'LERİ", "2-5 düz cümle",
			"yeniden dökme", "AYNEN, tırnak içinde", "bu trace'in kanıtı bunu göstermiyor", quoteVerbatimRule} {
			if !strings.Contains(strings.Join(strings.Fields(nar), " "), must) {
				t.Errorf("simple=%v: anlatım takip eki %q taşımalı", simple, must)
			}
		}
		for _, banned := range []string{"**Özet:**", "**Olası Neden**", "kısalık için bilgi"} {
			if strings.Contains(nar, banned) {
				t.Errorf("simple=%v: anlatım takip eki %q taşımamalı", simple, banned)
			}
		}
		// ek kısa kalır: bölümlü ekten uzun değil
		if len(nar) > len(old) {
			t.Errorf("simple=%v: anlatım eki %d bayt > bölümlü %d", simple, len(nar), len(old))
		}
		t.Logf("simple=%v takip eki: bölümlü %d bayt, anlatım %d bayt", simple, len(old), len(nar))
	}
	for name, p := range map[string]string{"code": SystemPromptTraceNarrativeWithCode(), "candidate": SystemPromptTraceNarrativeWithCandidate()} {
		if !strings.HasPrefix(p, systemTraceNarrativeBody) || !strings.HasSuffix(p, AnswerInTurkish) {
			t.Errorf("%s: anlatım gövdesi + dil direktifi", name)
		}
		for _, must := range []string{"Kod bulgusu:", "Kod bloğu koyma", "ÇÖZÜLEMEDİ", "dört parça"} {
			if !strings.Contains(p, must) {
				t.Errorf("%s: %q yok", name, must)
			}
		}
		if strings.Contains(p, "KOD ALINTISI ZORUNLU") {
			t.Errorf("%s: bölümlü kod ekinin alıntı zorunluluğu anlatımda olmamalı", name)
		}
		t.Logf("%s istemi %d bayt (anlatım tabanı %d)", name, len(p), len(SystemPromptTraceNarrative()))
	}
	if !strings.Contains(SystemPromptTraceNarrativeWithCandidate(), "ADAY") {
		t.Error("aday eki")
	}
}
