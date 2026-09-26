package copilot

import (
	"strings"
	"testing"
)

// TestTraceInvestigationPromptContract — v0.10.948 (CoSRE araştırma
// asistanı, Faz B): operatörün cevap biçimi ve dürüstlük kuralları metinde
// PİNLİ. Biri silinirse model onu uygulamayı bırakır; test o kaymayı tutar.
func TestTraceInvestigationPromptContract(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	// Beş başlık, bu sırayla.
	last := -1
	for _, h := range []string{"**Bulgu**", "**Kanıt**", "**Olası neden**", "**Eksik veri**", "**Sonraki kontrol**"} {
		i := strings.Index(p, h)
		if i < 0 {
			t.Fatalf("başlık eksik: %s", h)
		}
		if i < last {
			t.Fatalf("başlık sırası bozuk: %s", h)
		}
		last = i
	}
	for _, rule := range []string{
		"NEDEN\nDEĞİL", // korelasyon ≠ neden
		"\"hata yok\" DEĞİLDİR",
		"TOPLAMA", // iç içe/paralel span süreleri
		"CPU tüketimi DEĞİLDİR",
		"Profiling verisi yok",
		"örnekleme",
		"Ortamları karıştırma",
		"uydurma",
		"KAYNAK\nDURUMU",
	} {
		if !strings.Contains(p, rule) {
			t.Errorf("kural eksik: %q", rule)
		}
	}
	if !strings.HasSuffix(p, DataNotInstruction) {
		t.Error("veri-talimat değildir çerçevesi en sonda olmalı (log gövdeleri kanıtın parçası)")
	}
	if strings.HasSuffix(p, AnswerInTurkish) {
		t.Error("Türkçe-native istem ortak dil direktifiyle bitmemeli")
	}
}

func TestTraceFollowUpAddendumContract(t *testing.T) {
	a := TraceFollowUpAddendum()
	for _, want := range []string{"AKTİF BAĞLAM", "from_iso/to_iso", "source.state", "get_logs_for_trace",
		"bağlamsal", "compare_periods", "list_metric_labels", "Eksik veri", "profiling"} {
		if !strings.Contains(a, want) {
			t.Errorf("ek talimatta %q yok", want)
		}
	}
}
