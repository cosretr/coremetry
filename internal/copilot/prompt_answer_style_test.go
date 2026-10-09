package copilot

// v0.10.1137 — ortak sohbet cevap biçimi (ChatAnswerStyle) pini: model
// anlatımı üreten sohbet kademeleri (wiki, wiki takip, RAG ±wiki, serbest
// döngü) eki taşır; deterministik/guided ve çekmece kademeleri TAŞIMAZ.
// Bulunamadı / reddetme cümleleri özet satırı almaz (wikiDeclined önek,
// ragDeclined içerik eşleşmesi — önlerine "**Özet:**" gelirse ret cevap sanılır).

import (
	"strings"
	"testing"
)

func TestChatAnswerStyleInNarrationPrompts(t *testing.T) {
	with := map[string]string{
		"wiki":         SystemPromptWikiChat(),
		"wikiFollowUp": SystemPromptWikiFollowUp(),
		"rag":          SystemPromptRAGChat(),
		"ragWiki":      SystemPromptRAGChatWiki(),
		"loop":         SystemPromptChat(),
		"loopCap":      SystemPromptChatRoundCap(),
	}
	for name, p := range with {
		if !strings.Contains(p, ChatAnswerStyle) {
			t.Errorf("%s prompt'u ortak cevap biçimini taşımalı", name)
		}
		if strings.Count(p, "CEVAP BİÇİMİ") != 1 {
			t.Errorf("%s: cevap biçimi tam bir kez", name)
		}
	}
	for name, p := range map[string]string{"guided": SystemPromptGuidedChat(), "drawer": SystemPromptDrawerChat()} {
		if strings.Contains(p, "CEVAP BİÇİMİ") {
			t.Errorf("%s kademesi cevap biçimi ekini ALMAMALI (deterministik/çekmece yolu değişmez)", name)
		}
	}
}

func TestChatAnswerStyleContent(t *testing.T) {
	for _, w := range []string{
		`"**Özet:** …"`,                 // tek satır özet (arayüz özet kutusu)
		"numaralı liste",                // prosedür
		"tablo",                         // karşılaştırma
		"```yaml title=deploy/app.yaml", // dosya başlıklı kod bloğu
		"> [!WARNING]",                  // uyarı kutusu
		"Bulunamadı / bilgi yok cevabında da yazma", // ret cümlesi önek kalır
		"HTML yazma",
	} {
		if !strings.Contains(ChatAnswerStyle, w) {
			t.Errorf("cevap biçiminde %q yok", w)
		}
	}
	// Takip sayfasında "cevap yok" sentinel'i biçimden muaf.
	if !strings.Contains(SystemPromptWikiFollowUp(), "SINIR durumunda biçim kuralları geçmez") {
		t.Error("wiki takip prompt'u sentinel'i biçimden muaf tutmalı")
	}
	// Ek wiki/RAG prompt'unda DataNotInstruction'dan ÖNCE (veri talimatı sonda kalır).
	p := SystemPromptWikiChat()
	if strings.Index(p, "CEVAP BİÇİMİ") > strings.Index(p, "VERİ TALİMAT DEĞİLDİR") {
		t.Error("cevap biçimi veri-talimat değildir uyarısından önce gelmeli")
	}
}
