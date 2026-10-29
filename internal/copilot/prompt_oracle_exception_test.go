package copilot

// prompt_oracle_exception_test.go — v0.10.1100: Oracle hata grubu prompt'u.
// Sicil (dil + sürüm) ve içerik çıpaları: stack istemez, kod ailesini (ORA-)
// tanır, DB / çağıran ayrımını ister, Türkçe bölüm başlıkları, span
// prompt'undan AYRI metin.

import (
	"strings"
	"testing"
)

func TestOracleExceptionPromptRegistered(t *testing.T) {
	if c, ok := promptRegistry()["OracleException"]; !ok || c != classDirective {
		t.Fatal("OracleException sicilde classDirective olmalı")
	}
	if promptVersionRegistry["systemOracleException"] != systemOracleException {
		t.Fatal("systemOracleException prompt sürümü siciline girmeli (ai_calls.prompt_version)")
	}
	p := SystemPromptOracleException()
	if !strings.HasSuffix(p, AnswerInTurkish) || strings.Count(p, AnswerInTurkish) != 1 {
		t.Fatal("Türkçe cevap direktifiyle bir kez bitmeli")
	}
	if p == SystemPromptException() || strings.HasPrefix(p, systemExceptionBody) {
		t.Fatal("Oracle prompt'u span exception prompt'undan ayrı olmalı")
	}
}

func TestOracleExceptionPromptAnchors(t *testing.T) {
	p := SystemPromptOracleException()
	for _, want := range []string{
		"NO stacktrace", "ORA-", "DB side", "caller side", "CLOSED minutes", "burst",
		"**Hata ne demek**", "**Nerede yoğunlaşıyor**", "**Patlama mı, sürekli akış mı**", "**Ne kontrol edilmeli**",
		"never invent",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Oracle prompt'u %q çıpasını taşımalı", want)
		}
	}
	if strings.Contains(p, "KOD BAĞLAMI") {
		t.Error("Oracle prompt'u kod eki taşımamalı (stack yok, kod dalı koşmaz)")
	}
}

// v0.10.1103 (operatör: "Oracle hata grubu için varsa coremetry üzerindeki
// trace ve o trace loglarını da kullanabilsin") — prompt, girdideki
// Coremetry trace'ini + loglarını tanır: varsa çağıran servis/operasyon ve
// uygulamanın logladığı, yoksa girdinin "yok" satırına bağlı tek cümle.
func TestOracleExceptionPromptCoremetryTrace(t *testing.T) {
	p := SystemPromptOracleException()
	for _, want := range []string{
		"MAY also be present", "Coremetry TRACE", "LOGS", "**Çağıran taraf (Coremetry trace)**",
		"calling service and operation", "Coremetry trace'i: yok", "do not guess the caller",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("Oracle prompt'u %q çıpasını taşımalı", want)
		}
	}
	if strings.Contains(p, "NO span sample") {
		t.Error("span örneği artık olabilir — 'NO span sample' kalmamalı")
	}
}

// v0.10.1370 (operatör-bildirimli: "Teknik hatalar için Exceptions sayfasında
// trace incelemesi gibi loga bakmıyor — Ask CoSRE veya explain için") — iki
// prompt da girdideki "Örnek trace'lerin LOGLARI" bloğunu tanır: Oracle
// prompt'u trace yokken de trace id başına logu raporlar ve logu atan servisi
// olası çağıran sayar; span prompt'u ≤3 örnek trace'in loglarını trace id
// önekiyle anar. Bölüm adları değişmez; prompt sürümü sicilde.
func TestExceptionPromptsSampleTraceLogs(t *testing.T) {
	ora := SystemPromptOracleException()
	for _, want := range []string{
		"Örnek\ntrace'lerin LOGLARI", "\"Örnek trace'lerin LOGLARI\" is present, still",
		"logging service as the\nlikely caller", "span tree is unavailable", "**Çağıran taraf (Coremetry trace)**",
	} {
		if !strings.Contains(ora, want) {
			t.Errorf("Oracle prompt'u %q çıpasını taşımalı", want)
		}
	}
	sp := SystemPromptException()
	for _, want := range []string{"logs of up to 3 sample traces", "\"Örnek trace'lerin\nLOGLARI\"", "trace id prefix", "**Yayılım ve Bağlam**"} {
		if !strings.Contains(sp, want) {
			t.Errorf("span exception prompt'u %q çıpasını taşımalı", want)
		}
	}
	if promptVersionRegistry["systemExceptionBody"] != systemExceptionBody || promptVersionRegistry["systemOracleException"] != systemOracleException {
		t.Fatal("iki prompt da sürüm sicilinde güncel değeriyle olmalı")
	}
}

// TestOracleTraceSynthesisAddendumAnchors — v0.10.1572 (operatör, 2026-10-29):
// Oracle cevabından sonra "trace'le birlikte inceleyeyim mi?" sorusuna olumlu
// cevapta kullanılan ek sicilde (PromptVersion kapsar), Oracle prompt'unun
// bölüm adını AYNEN anar, sunucu bloğunun adını ("TRACE KANITI") ve dosya:satır
// kuralını taşır; dil direktifi tekrarlanmaz (taban prompt zaten taşır).
func TestOracleTraceSynthesisAddendumAnchors(t *testing.T) {
	a := OracleTraceSynthesisAddendum()
	if promptVersionRegistry["oracleTraceSynthesisAddendum"] != a {
		t.Fatal("ek promptVersionRegistry'de değil — PromptVersion değişimi görmez")
	}
	for _, want := range []string{"TRACE KANITI", "HATA ZİNCİRİ", "**Çağıran taraf (Coremetry trace)**", "**Ne kontrol edilmeli**", "dosya:satır"} {
		if !strings.Contains(a, want) {
			t.Errorf("ek %q anmalı", want)
		}
		if strings.HasPrefix(want, "**") && !strings.Contains(SystemPromptOracleException(), want) {
			t.Errorf("başlık %q Oracle prompt'unda yok — iki metin ayrıştı", want)
		}
	}
	if strings.Contains(a, AnswerInTurkish) {
		t.Error("dil direktifi tabanda; ekte tekrarlanmamalı")
	}
	if n := strings.Count(strings.TrimSpace(a), "\n") + 1; n > 10 {
		t.Errorf("ek %d satır, ≤10 olmalı (küçük model)", n)
	}
}
