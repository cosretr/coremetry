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
