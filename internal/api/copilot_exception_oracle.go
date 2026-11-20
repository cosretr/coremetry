package api

// copilot_exception_oracle.go — v0.10.1100 (operatör onaylı): exception
// grubunun AI girdisini KAYNAĞINA göre seçen tek nokta. `ora:` grubu (Oracle
// hata tablosu, v0.10.1092) span-merkezli girdiyle boş/jenerik açıklama
// alıyordu (stack yok, örnek span yok); artık Oracle bağlamı + kendi sistem
// prompt'u. Kullananlar: ✨ "Explain root cause" (copilot_exception.go) ve
// insight kartı (insight.go). api.go BÜYÜMEZ.
//
// v0.10.1370 (operatör-bildirimli: "Teknik hatalar için Exceptions sayfasında
// trace incelemesi gibi loga bakmıyor — Ask CoSRE veya explain için") — AI
// çekmecesinin takip sohbeti (drawerExceptionInput, chat_exception_followup.go)
// de artık BU dağıtıcıdan geçer: v0.10.1100'deki "bilinçli olarak değişmedi"
// kararı `ora:` grubunun CoSRE takibinde span kurucusuyla (stack yok, örnek
// span yok, log yok) boş kanıt almasına yol açıyordu. Ayrıca interaktif yolda
// Oracle okuyucusu Tempo-önce sarılır (oracleInteractiveReader): trace
// Coremetry'ye örneklenmemiş ama Tempo'da olabilir. Arka plan açıklayıcı
// (anomaly.ExceptionExplainer) CH-only kalır.

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// TEST DİKİŞLERİ (exceptionInputSeam emsali): store'suz testte iki kurucu da
// sahteyle değiştirilir; prod'da aşağıdaki varsayılanlar.
var (
	spanExceptionInputFn = func(s *Server, ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) anomaly.ExceptionExplainInput {
		// v0.10.2384 — kurucunun kendi kanıt tavanları çağrının profilindeki
		// ölçekle (evidence_scale.go; ölçek 1 = bayt bayt eski girdi). Dış
		// kelepçeler (çekmece kanıtı, fitSingleShot) yine son sınır.
		return anomaly.BuildExceptionExplainInputScaled(ctx, s.store, s.logs, g, loc, s.evidenceScale(ctx))
	}
	oracleExceptionInputFn = func(s *Server, ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) anomaly.ExceptionExplainInput {
		var rd anomaly.OracleContextReader
		if s.store != nil { // nil *Store'u arayüze sarmak nil olmayan arayüz verirdi
			rd = oracleInteractiveReader(s, s.store) // v0.10.1370 — Tempo-önce trace okuması
		}
		// v0.10.1103 — s.logs: satırın trace'i Coremetry'de varsa o trace'in
		// logları da girdiye (nil = CH-only kurulum, log bloğu atlanır).
		return buildOracleExceptionInput(ctx, rd, s.logs, g, loc)
	}
	// buildOracleExceptionInput — v0.10.1103 test dikişi: s.logs'un kurucuya
	// ulaştığı store'suz testte pinlenir.
	buildOracleExceptionInput = anomaly.BuildOracleExceptionExplainInput
)

// oracleTempoFirstReader — v0.10.1370: interaktif yolun Oracle okuyucusu.
// GetTrace Tempo-önce resolveTraceSpans'a gider (trace_resolve.go; ≤3 sn
// Tempo, sonra CH) ve anomaly.TraceSourceLoader'ı uygular: CH çözülen
// id'lerin hiçbirini bulamazsa kurucu en yeni id'yi BİR kez bununla dener.
type oracleTempoFirstReader struct {
	anomaly.OracleContextReader
	s *Server
}

func (r oracleTempoFirstReader) GetTrace(ctx context.Context, traceID string) ([]chstore.SpanRow, error) {
	spans, _, err := r.s.resolveTraceSpans(ctx, traceID)
	return spans, err
}

func (r oracleTempoFirstReader) GetTraceSource(ctx context.Context, traceID string) ([]chstore.SpanRow, string, error) {
	return r.s.resolveTraceSpans(ctx, traceID)
}

// oracleInteractiveReader — v0.10.1370: taban okuyucuyu Tempo-önce sarar;
// taban nil → nil (nil arayüz korunur).
func oracleInteractiveReader(s *Server, base anomaly.OracleContextReader) anomaly.OracleContextReader {
	if base == nil || s == nil {
		return base
	}
	return oracleTempoFirstReader{OracleContextReader: base, s: s}
}

// exceptionExplainInput — grubun girdisi + sistem prompt'u. `ora:` → Oracle
// kurucusu + SystemPromptOracleException; aksi span kurucusu + SystemPromptException.
func (s *Server) exceptionExplainInput(ctx context.Context, g *chstore.ExceptionGroup, loc *time.Location) (anomaly.ExceptionExplainInput, string) {
	if chstore.IsOracleGroup(g.Fingerprint) {
		return oracleExceptionInputFn(s, ctx, g, loc), exceptionExplainSystem(g.Fingerprint) // v0.10.1399 — seçim tek yerde
	}
	return spanExceptionInputFn(s, ctx, g, loc), exceptionExplainSystem(g.Fingerprint) // v0.10.1399
}

// exceptionExplainSystem — v0.10.1399: grubun ✨ açıklama sistem prompt'u
// (parmak izinden; `ora:` → Oracle). exceptionExplainInput ve sohbet içi
// açıklama (chat_operation_exception_explain.go) aynı seçimi kullanır.
func exceptionExplainSystem(fp string) string {
	if chstore.IsOracleGroup(fp) {
		return copilot.SystemPromptOracleException()
	}
	return copilot.SystemPromptException()
}
