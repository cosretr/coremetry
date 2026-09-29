package copilot

import (
	"strings"
	"testing"
)

// TestTraceInvestigationPromptContract — v0.10.948 (CoSRE araştırma
// asistanı, Faz B): operatörün cevap biçimi ve dürüstlük kuralları metinde
// PİNLİ. Biri silinirse model onu uygulamayı bırakır; test o kaymayı tutar.
//
// v0.10.986 — operatör: "kodu incele dediğimde daha iyi sonuç veriyor, o hali
// olsa daha iyi olacak". Biçim klasik üçlü (systemTraceBody ile aynı başlıklar)
// + koşullu «Eksik veri»; kanıt kimliği ve "Güven:" satırı cevaba GİRMEZ.
func TestTraceInvestigationPromptContract(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	last := -1
	for _, h := range []string{"**İşlem Akışı ve Veri Özeti**", "**Stacktrace Detayı**", "**Kök Neden ve Sonraki Adım**", "**Eksik veri**"} {
		i := strings.Index(p, "\n"+h+" —")
		if i < 0 {
			t.Fatalf("başlık tanımı eksik: %s", h)
		}
		if i < last {
			t.Fatalf("başlık sırası bozuk: %s", h)
		}
		last = i
	}
	for _, old := range []string{"**Bulgu**", "**Kanıt**", "**Olası neden**", "**Sonraki kontrol**", "Güven: kesin", "Güven: olası", "[kimlik]"} {
		if strings.Contains(p, old) {
			t.Errorf("v0.10.986 — eski beşli biçimin parçası istemde kalmamalı: %q", old)
		}
	}
	for _, rule := range []string{
		"NEDEN\nDEĞİL", // korelasyon ≠ neden
		"\"hata yok\" DEĞİLDİR",
		"TOPLAMA", // iç içe/paralel span süreleri
		"CPU tüketimi DEĞİLDİR",
		"Profiling verisi yok",
		"örnekleme",
		"Ortamları karıştırma",
		"UYDURMA",
		"KAYNAK\nDURUMU",
		"cevaba YAZMA", // kanıt kimlikleri modele yönelik, cevapta yok
		"AYNEN",        // değerler kanıttan aynen
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

// TestTraceInvestigationRootCauseSection — v0.10.986: «Kök Neden ve Sonraki
// Adım» klasik içerik: en olası neden + TEK sonraki adım; uydurma yok, kanıt
// yetersizse söylenir, ilişki ≠ neden, yokluktan sonuç yok. Güven satırı YOK
// (arayüz ilk cümleyi Karar şeridine çıkarır — klasik davranış).
func TestTraceInvestigationRootCauseSection(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	sec := section(t, p, "**Kök Neden ve Sonraki Adım** —", "\n**Eksik veri** —")
	for _, want := range []string{
		"en olası kök neden",
		"TEK somut sonraki",
		"UYDURMA",
		"\"kanıt yetersiz\"",
		"NEDEN\nDEĞİL",
		"yokluktan sonuç çıkarma",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Kök Neden tanımında %q yok:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "Güven") {
		t.Error("v0.10.986 — güven satırı yok")
	}
}

// TestTraceInvestigationStacktraceSection — v0.10.972: «Stacktrace Detayı»
// YALNIZ kanıtta stacktrace varken; alanlar tek-atış istemin alanları
// (sınıf+metot, exception tipi, dağıtım birimi, katman, mesaj); Oracle satırı /
// çıplak exception tipi stacktrace sayılmaz; stacktrace yoksa bölüm HİÇ yazılmaz.
func TestTraceInvestigationStacktraceSection(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	sec := section(t, p, "**Stacktrace Detayı** —", "\n**Kök Neden ve Sonraki Adım** —")
	for _, want := range []string{
		"YALNIZ",
		"stacktrace:", // sunucunun L satırındaki alan (trace_investigate.go invRenderLogs)
		"Oracle",      // Oracle satırları stacktrace DEĞİL
		"exception.type",
		"sınıf ve metot",
		"exception tipi",
		".war",
		"BFF / backend / entegrasyon",
		"AYNEN",
		"HİÇ yazma",
		"\"stacktrace yok\"",
		// v0.10.972 — sunucunun "(kaynak kesik: …)" notu: kesik stack görünmeyen
		// Caused by'ı saklayabilir; v0.10.986 — bunu söyler (güven satırı yok).
		"\"kaynak kesik\"",
		"Caused by",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Stacktrace Detayı tanımında %q yok:\n%s", want, sec)
		}
	}
	// Biçim satırı: kanıtı olmayan bölüm yazılmaz; kimlikler cevaba girmez.
	head := section(t, p, "CEVAP BİÇİMİ", "\n**İşlem Akışı ve Veri Özeti** —")
	for _, want := range []string{"HİÇ yazma", "cevaba YAZMA"} {
		if !strings.Contains(head, want) {
			t.Errorf("biçim satırında %q yok:\n%s", want, head)
		}
	}
}

func TestTraceFollowUpAddendumContract(t *testing.T) {
	a := TraceFollowUpAddendum()
	for _, want := range []string{"AKTİF BAĞLAM", "from_iso/to_iso", "source.state", "get_logs_for_trace",
		"bağlamsal", "compare_periods", "list_metric_labels", "Eksik veri", "profiling",
		// v0.10.986 — takip cevabı da ilk cevabın klasik başlıklarıyla, kimliksiz
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

// section — p'de from ile başlayan ve to'dan önce biten parça (yoksa Fatal).
func section(t *testing.T, p, from, to string) string {
	t.Helper()
	i := strings.Index(p, from)
	if i < 0 {
		t.Fatalf("%q bulunamadı", from)
	}
	j := strings.Index(p[i:], to)
	if j < 0 {
		t.Fatalf("%q, %q'dan sonra bulunamadı", to, from)
	}
	return p[i : i+j]
}
