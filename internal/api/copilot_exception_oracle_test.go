package api

// copilot_exception_oracle_test.go — v0.10.1100 (operatör onaylı: Oracle hata
// grubu için AI açıklaması span yerine Oracle bağlamı). Pinler: `ora:` grubu
// Oracle kurucusuna + Oracle prompt'una gider, span grubu GİTMEZ (bayt bayt
// eski kurucu + prompt); explain ve insight aynı tek noktadan geçer; Oracle grubunda "Kodu da incele" dalı koşmaz; çağrı copilotExplain
// sarmalayıcısından (explainPrompt) geçer.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestExceptionExplainInputRoutesByGroupSource(t *testing.T) {
	origSpan, origOra := spanExceptionInputFn, oracleExceptionInputFn
	t.Cleanup(func() { spanExceptionInputFn, oracleExceptionInputFn = origSpan, origOra })
	var spanCalls, oraCalls int
	spanExceptionInputFn = func(_ *Server, _ context.Context, _ *chstore.ExceptionGroup, _ *time.Location) anomaly.ExceptionExplainInput {
		spanCalls++
		return anomaly.ExceptionExplainInput{User: "span"}
	}
	oracleExceptionInputFn = func(_ *Server, _ context.Context, _ *chstore.ExceptionGroup, _ *time.Location) anomaly.ExceptionExplainInput {
		oraCalls++
		return anomaly.ExceptionExplainInput{User: "oracle", Oracle: &anomaly.OracleExplainContext{}}
	}
	s := &Server{}
	ora := &chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_042", "OP_TRANSFER"), Type: "APP_ERR_042", Message: "OP_TRANSFER"}
	in, sys := s.exceptionExplainInput(context.Background(), ora, time.UTC)
	if oraCalls != 1 || spanCalls != 0 || in.User != "oracle" || sys != copilot.SystemPromptOracleException() {
		t.Fatalf("ora: grubu Oracle kurucusuna gitmeli: ora=%d span=%d user=%q", oraCalls, spanCalls, in.User)
	}
	span := &chstore.ExceptionGroup{Fingerprint: "0a1b2c3d4e5f6a7b", Type: "java.lang.IllegalStateException", Message: "x"}
	in, sys = s.exceptionExplainInput(context.Background(), span, time.UTC)
	if oraCalls != 1 || spanCalls != 1 || in.User != "span" || in.Oracle != nil || sys != copilot.SystemPromptException() {
		t.Fatalf("span grubu Oracle kurucusuna GİTMEMELİ: ora=%d span=%d user=%q", oraCalls, spanCalls, in.User)
	}
}

// Varsayılan Oracle kurucusu store'suz sunucuda panik yapmaz (nil *Store arayüze
// sarılmaz) ve Oracle bağlamı döner.
func TestDefaultOracleExceptionInputNilStore(t *testing.T) {
	ora := &chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_042", "OP_TRANSFER"),
		Type: "APP_ERR_042", Message: "OP_TRANSFER", Service: chstore.OracleGroupFallbackService("core-errlog")}
	in, sys := (&Server{}).exceptionExplainInput(context.Background(), ora, time.UTC)
	if in.Oracle == nil || in.Oracle.SummaryLine() != "Oracle · core-errlog · APP_ERR_042 · OP_TRANSFER" || sys != copilot.SystemPromptOracleException() {
		t.Fatalf("varsayılan Oracle kurucusu: %+v", in.Oracle)
	}
}

// Kaynak pini: explain ve insight TEK dağıtıcıdan geçer (çekmece takip sohbeti
// — read_source_code yolu — bilinçli olarak değişmedi, kendi pini var); Oracle
// grubunda kod dalı kapalı; LLM çağrısı sarmalayıcıdan (explainPrompt →
// copilotExplain), doğrudan s.copilot.Explain değil.
func TestExceptionExplainCallersUseDispatcher(t *testing.T) {
	for _, f := range []string{"copilot_exception.go", "insight.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		src := string(b)
		if !strings.Contains(src, "s.exceptionExplainInput(") {
			t.Errorf("%s exceptionExplainInput dağıtıcısını kullanmalı", f)
		}
		if strings.Contains(src, "anomaly.BuildExceptionExplainInput(") {
			t.Errorf("%s span kurucusunu doğrudan çağırmamalı (ora: grubu boş bağlam alırdı)", f)
		}
		if strings.Contains(src, "s.copilot.Explain(") {
			t.Errorf("%s s.copilot.Explain'i doğrudan çağırmamalı (/ai atfı)", f)
		}
	}
	b, _ := os.ReadFile("copilot_exception.go")
	if !strings.Contains(string(b), "opts.IncludeCode && in.Oracle == nil") {
		t.Error("Oracle grubunda kod dalı koşmamalı (stack yok)")
	}
	if !strings.Contains(string(b), "s.explainPrompt(r, system, in.User)") {
		t.Error("explain, dağıtıcının seçtiği prompt'la sarmalayıcıdan geçmeli")
	}
}
