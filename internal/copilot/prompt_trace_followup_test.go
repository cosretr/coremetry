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
