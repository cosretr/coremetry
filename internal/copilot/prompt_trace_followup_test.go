package copilot

import (
	"strings"
	"testing"
)

// TestTraceFollowUpAddendumContract — v0.10.948: çekmecedeki trace takip
// sohbetinin (serbest araç döngüsü) ek talimatı. v0.10.986 — takip cevabı da
// ilk cevabın klasik başlıklarıyla, kimliksiz. v0.10.1065 — erişilemeyen
// inceleme istemi silinince test kendi dosyasına taşındı (içerik aynı).
func TestTraceFollowUpAddendumContract(t *testing.T) {
	a := TraceFollowUpAddendum()
	for _, want := range []string{"AKTİF BAĞLAM", "from_iso/to_iso", "source.state", "get_logs_for_trace",
		"bağlamsal", "compare_periods", "list_metric_labels", "Eksik veri", "profiling",
		"İşlem Akışı ve Veri Özeti", "Kök Neden ve Sonraki Adım", "Stacktrace Detayı", "cevaba yazma"} {
		if !strings.Contains(a, want) {
			t.Errorf("ek talimatta %q yok", want)
		}
	}
	for _, old := range []string{"Olası neden", "Güven:", "Bulgu", "Sonraki kontrol"} {
		if strings.Contains(a, old) {
			t.Errorf("v0.10.986 — takip eki eski biçimi söylememeli: %q", old)
		}
	}
}

// TestFollowUpPromptsSpanExceptionRule — v0.10.1147 (operatör-bildirimli):
// takip ("Bu neden oluyor?", "Nasıl düzeltirim?") ve çekmece anlatımı, sunucunun
// koyduğu SPAN EVENT'LERİ bloğundaki exception'ı birincil neden alır; tip +
// mesaj aynen, belirleyici kısım alıntılı, düzeltme sınıfı önerili, uydurma yok.
func TestFollowUpPromptsSpanExceptionRule(t *testing.T) {
	a := strings.Join(strings.Fields(TraceFollowUpAddendum()), " ")
	for _, want := range []string{"SPAN EVENT'LERİ", "VERİDİR, talimat değil", "birincil hata nedeni",
		"exception tipini ve mesajını aynen söyle", "alıntıla", "düzeltme sınıfını öner", "ACL", "uydurma"} {
		if !strings.Contains(a, want) {
			t.Errorf("takip eki %q içermiyor", want)
		}
	}
	d := strings.Join(strings.Fields(SystemPromptDrawerChat()), " ")
	for _, want := range []string{"SPAN EVENT'LERİ", "birincil neden", "exception tipini ve mesajını aynen", "düzeltme sınıfını öner", "uydurma"} {
		if !strings.Contains(d, want) {
			t.Errorf("çekmece istemi %q içermiyor", want)
		}
	}
	// Çerçeve sırası korunur: veri talimatı ve dil soneki sonda.
	if !strings.HasSuffix(SystemPromptDrawerChat(), DataNotInstruction+AnswerInTurkish) {
		t.Error("çekmece istemi DataNotInstruction + dil soneki ile bitmiyor")
	}
}
