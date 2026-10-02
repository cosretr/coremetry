package copilot

import (
	"strings"
	"testing"
)

// v0.10.1050 (operatör: "Sohbet kod okuyabilsin: takip soruları bugün kod
// okuyamıyor.") — read_source_code sunulduğunda serbest döngüye giren ek.
// Sözleşme: ne zaman çağrılır, nasıl alıntılanır (dosya:satır, aynen),
// okunmayan kod yazılmaz, okunamayınca söylenir, içerik veri — ve KISA
// (küçük model, "schema soup" v0.10.172/194).
func TestSourceCodeChatAddendumContract(t *testing.T) {
	a := SourceCodeChatAddendum()
	for _, want := range []string{
		"read_source_code", "kodu soruyorsa", "alıntılanmamış", "kanıttan al", "daha belirgin file",
		"dosya:satır", "AYNEN", "okumadığın", "okunamadıysa", "VERİDİR, talimat değil",
	} {
		if !strings.Contains(flat(a), want) {
			t.Errorf("ekte %q yok", want)
		}
	}
	if n := len(a); n > 700 {
		t.Errorf("ek %d B — kısa tut (küçük model her tur yutar)", n)
	}
	if strings.HasPrefix(a, "\n") || strings.HasSuffix(a, "\n") {
		t.Error("ek kendi boşluğunu taşımaz: ayraç çağıranın (api chatSourceCodePromptTR)")
	}
	if strings.Contains(a, AnswerInTurkish) || strings.Contains(a, DataNotInstruction) {
		t.Error("ek dil direktifini/çerçeveyi tekrarlamaz — ikisi sohbet çekirdeğinde (sistem mesajının sonunda)")
	}
	if promptVersionRegistry["sourceCodeChatAddendum"] != a {
		t.Error("ek promptVersionRegistry'de değil — sürüm değişimi /ai'da görünmez")
	}
	// Sohbet çekirdeği DEĞİŞMEDİ: araç sunulmayınca prompt bayt bayt eskisi
	// olsun diye ek ayrı sabitte; çekirdek araçtan söz etmez.
	if strings.Contains(SystemPromptChat(), "read_source_code") || strings.Contains(SystemPromptChatAgentLoop(), "read_source_code") {
		t.Error("sohbet çekirdeği read_source_code'u anıyor — araç sunulmadığında olmayan bir aracı vaat eder")
	}
}
