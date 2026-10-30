package copilot

// trace_critpath_prompt_test.go — v0.10.1630 — CoSRE kritik yol planı R3
// (operatör: en büyük öz-süre paylı span CLIENT ise cevap "dış bağımlılık
// bekleme: <hedef> <süre>" ile başlasın; kök SERVER süresi bekleme diye
// sunulmasın, süreler toplanmasın).
//
// Pinler: systemTraceBody KRİTİK YOL bloğunu adlandırır, önek satırının
// birebir biçimini, "kökün/SERVER'ın süresi değil" kuralını ve ANSWER SHAPE'in
// ilk satırını taşır; "toplam sürenin %" (toplamaya davet) geri gelmez; kural
// tüm trace istem bileşimlerine ulaşır; takip eki aynı öneki söyler.

import (
	"strings"
	"testing"
)

func TestSystemTraceLatencyOwnerRule(t *testing.T) {
	for _, want := range []string{
		"LATENCY OWNER IS PRECOMPUTED",
		"KRİTİK YOL",
		"dış bağımlılık bekleme: <hedef> <süre>",
		"never the root or SERVER\nspan's duration, never a sum",
		"(kök süresinin %<share>'ı)",
	} {
		if !strings.Contains(systemTraceBody, want) {
			t.Errorf("systemTraceBody %q taşımıyor", want)
		}
	}
	if strings.Contains(systemTraceBody, "toplam sürenin %") {
		t.Error(`"toplam sürenin %" geri geldi — pay kök süresine oranıdır`)
	}
	// ANSWER SHAPE'te önek satırı ilk bölüm başlığından ÖNCE.
	shape := systemTraceBody[strings.Index(systemTraceBody, "ANSWER SHAPE"):]
	if i, j := strings.Index(shape, "dış bağımlılık bekleme:"), strings.Index(shape, "**İşlem Akışı ve Veri Özeti**"); i < 0 || i > j {
		t.Errorf("ANSWER SHAPE önek satırı başlıktan önce değil (%d, %d)", i, j)
	}
	for name, p := range map[string]string{"trace": SystemPromptTrace(), "code": SystemPromptTraceWithCode(), "opCode": SystemPromptTraceWithOperationCode(), "opCand": SystemPromptTraceWithOperationCandidate()} {
		if !strings.Contains(p, "LATENCY OWNER IS PRECOMPUTED") {
			t.Errorf("%s bileşimi kuralı taşımıyor", name)
		}
	}
	for _, simple := range []bool{false, true} {
		if a := TraceFollowUpAddendumFor(simple); !strings.Contains(a, `"dış bağımlılık bekleme: <hedef> <süre>"`) || !strings.Contains(a, "KRİTİK YOL") {
			t.Errorf("takip eki (simple=%v) kritik yol kuralını taşımıyor", simple)
		}
	}
}
