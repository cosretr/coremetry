package copilot

import (
	"strings"
	"testing"
)

// TestTraceInvestigationPromptContract — v0.10.948 (CoSRE araştırma
// asistanı, Faz B): operatörün cevap biçimi ve dürüstlük kuralları metinde
// PİNLİ. Biri silinirse model onu uygulamayı bırakır; test o kaymayı tutar.
//
// v0.10.972 — operatör: "Kök neden olsun yine de" + "Stacktrace detayı
// bölümü de geri gelsin". «Olası neden» → «Kök neden» (ilk satır güven);
// koşullu «Stacktrace detayı» Kanıt'tan sonra, Kök neden'den önce.
func TestTraceInvestigationPromptContract(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	// Başlık TANIMLARI (satır başı "**X** —") bu sırayla; Stacktrace detayı
	// koşullu ama yeri sabit.
	last := -1
	for _, h := range []string{"**Bulgu**", "**Kanıt**", "**Stacktrace detayı**", "**Kök neden**", "**Eksik veri**", "**Sonraki kontrol**"} {
		i := strings.Index(p, "\n"+h+" —")
		if i < 0 {
			t.Fatalf("başlık tanımı eksik: %s", h)
		}
		if i < last {
			t.Fatalf("başlık sırası bozuk: %s", h)
		}
		last = i
	}
	if strings.Contains(p, "**Olası neden**") {
		t.Error("v0.10.972 — «Olası neden» başlığı «Kök neden» oldu; eski başlık istemde kalmamalı")
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

// TestTraceInvestigationRootCauseConfidence — v0.10.972: «Kök neden»in İLK
// satırı güven düzeyi; kesin yalnız kesintisiz zincirde, aksi hâlde eksik
// halkayla olası. Kanıt kuralları (kimlik, uydurmama, ilişki ≠ neden) aynen.
// Arayüz (explainAnatomy.ts) bu iki yazımı okur: "kesin" → Karar şeridi.
func TestTraceInvestigationRootCauseConfidence(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	sec := section(t, p, "**Kök neden** —", "\n**Eksik veri** —")
	for _, want := range []string{
		"İLK satırı",
		"\"Güven: kesin\"",
		"\"Güven: olası — <eksik halka>\"",
		"kesintisiz", // kesin'in koşulu: hata veren span/log'dan nedene zincir tam
		"[kimlik]",   // her iddia kanıt kimliğiyle
		"UYDURMA",
		"NEDEN\nDEĞİL",
		"\"kesin\" OLAMAZ", // yalnız zamansal ilişki kesinleştirilemez
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Kök neden tanımında %q yok:\n%s", want, sec)
		}
	}
}

// TestTraceInvestigationStacktraceSection — v0.10.972: «Stacktrace detayı»
// YALNIZ kanıtta stacktrace varken; alanlar eski tek-atış istemin alanları
// (sınıf+metot, exception tipi, dağıtım birimi, katman, mesaj) + kanıt
// kimliği; Oracle satırı / çıplak exception tipi stacktrace sayılmaz;
// stacktrace yoksa bölüm HİÇ yazılmaz.
func TestTraceInvestigationStacktraceSection(t *testing.T) {
	p := SystemPromptTraceInvestigation()
	sec := section(t, p, "**Stacktrace detayı** —", "\n**Kök neden** —")
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
		"kanıt kimliği",
		"HİÇ yazma",
		"\"stacktrace yok\"",
		// v0.10.972 — sunucunun "(kaynak kesik: …)" notu: kesik stack görünmeyen
		// Caused by'ı saklayabilir, tek başına "Güven: kesin" dayanağı olamaz.
		"\"kaynak kesik\"",
		"Caused by",
		"\"Güven: kesin\" dayanağı OLAMAZ",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Stacktrace detayı tanımında %q yok:\n%s", want, sec)
		}
	}
	// Biçim satırı: beş başlık zorunlu, Stacktrace detayı TEK koşullu başlık.
	head := section(t, p, "CEVAP BİÇİMİ", "\n**Bulgu** —")
	for _, want := range []string{"ZORUNLU", "koşullu"} {
		if !strings.Contains(head, want) {
			t.Errorf("biçim satırında %q yok:\n%s", want, head)
		}
	}
}

func TestTraceFollowUpAddendumContract(t *testing.T) {
	a := TraceFollowUpAddendum()
	for _, want := range []string{"AKTİF BAĞLAM", "from_iso/to_iso", "source.state", "get_logs_for_trace",
		"bağlamsal", "compare_periods", "list_metric_labels", "Eksik veri", "profiling",
		// v0.10.972 — takip cevabı da ilk cevabın başlıklarıyla
		"Kök neden", "Güven: kesin", "Güven: olası", "Stacktrace detayı"} {
		if !strings.Contains(a, want) {
			t.Errorf("ek talimatta %q yok", want)
		}
	}
	if strings.Contains(a, "Olası neden") {
		t.Error("v0.10.972 — takip eki eski «Olası neden» başlığını söylememeli")
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
