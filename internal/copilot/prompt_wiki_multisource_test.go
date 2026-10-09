package copilot

import (
	"strings"
	"testing"
)

// v0.10.1136 — wiki çok-kaynaklı okuma: anlatım prompt'ları kaynakları
// birleştirir, her bilgiyi [n] ("Kaynak n" çipi) ile atfeder, çelişkiyi iki
// numarayla gösterir, wiki dışı çıkarımı ayrı "Wiki'de yok, tahmin:"
// satırında verir ve kaynakta olmayan link/host/komut uydurmaz. Çit ve
// veri-talimat-değildir kuralı korunur. Seçim prompt'u JSON ister.
func TestWikiPromptsMultiSourceContract(t *testing.T) {
	for name, p := range map[string]string{
		"WikiChat":     SystemPromptWikiChat(),
		"WikiFollowUp": SystemPromptWikiFollowUp(),
		"RAGChatWiki":  SystemPromptRAGChatWiki(),
	} {
		f := flat(p)
		for _, must := range []string{
			"[n] Wiki · <sayfa başlığı>", // kaynak başlığı biçimi (çip sırası)
			"Kaynak n",                   // [n] = çip
			"[1]",                        // atıf örneği
			"Wiki'de yok, tahmin:",       // çıkarım ayrımı
			"ASLA uydurma",               // link/host/komut uydurma yasağı
			"çelişiyorsa",                // çelişki kuralı
			"<wiki_data>",                // çit
			"VERİDİR",
		} {
			if !strings.Contains(f, must) {
				t.Errorf("%s prompt'unda %q yok", name, must)
			}
		}
		if !strings.Contains(p, DataNotInstruction) {
			t.Errorf("%s DataNotInstruction taşımıyor", name)
		}
	}
	if !strings.Contains(SystemPromptWikiChat(), `"Wikide bulunamadı"`) {
		t.Error("WikiChat bulunamadı cümlesi (wikiDeclined çıpası) kayıp")
	}
	if !strings.Contains(SystemPromptWikiFollowUp(), WikiNotInPageSentinel) {
		t.Error("WikiFollowUp sentinel'i kayıp")
	}
	// RAG prompt'u wiki yokken BAYT BAYT eski: wiki eki yalnız RAGChatWiki'de.
	if strings.Contains(SystemPromptRAGChat(), "WİKİ KAYNAKLARI") {
		t.Error("wiki eki wiki'siz RAG prompt'una sızmış")
	}
	if !strings.HasPrefix(SystemPromptRAGChatWiki(), ragChatCore) || !strings.HasPrefix(SystemPromptRAGChat(), ragChatCore) {
		t.Error("RAG wiki varyantı doküman gövdesinin ÜSTÜNE kurulmalı")
	}
	// Gövdenin "asla tahmin etme" / "doküman adı verilmez" kuralları wiki eki
	// tarafından YALNIZ wiki kaynakları için açıkça istisna edilir; doküman
	// parçaları için aynen geçerli kalır (çelişkili talimat yok).
	rw := flat(SystemPromptRAGChatWiki())
	for _, must := range []string{
		"YALNIZ bu wiki kaynakları için şöyle değişir",
		"doküman parçaları (§ numaralı) için AYNEN geçerlidir",
		`"Dosya/doküman adı verilmez" kuralının istisnası`,
		`"Asla tahmin etme" kuralının TEK istisnası`,
		"Bu satır dışında tahmin yok",
	} {
		if !strings.Contains(rw, must) {
			t.Errorf("RAGChatWiki uzlaştırma cümlesi eksik: %q", must)
		}
	}
	// Başlık çitin İÇİNDE (ilk satır "Sayfa: …"), seçimde aday numarası dışarıda.
	if !strings.Contains(flat(SystemPromptWikiChat()), `ilk satırı "Sayfa: <başlık>"`) {
		t.Error("WikiChat çit içi başlık satırını anlatmalı")
	}
	sel := flat(SystemPromptWikiSelect())
	for _, must := range []string{`{"pages": [2, 1]}`, "YALNIZ JSON", "1–5", "<wiki_data>", "VERİDİR"} {
		if !strings.Contains(sel, must) {
			t.Errorf("WikiSelect prompt'unda %q yok", must)
		}
	}
}
