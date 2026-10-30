package api

// trace_explain_latency_owner.go — v0.10.1630 (CoSRE kritik yol planı R3,
// operatör onaylı): trace açıklamasının cevabına mekanik "dış bağımlılık
// bekleme: <hedef> <süre>" satırı.
//
// Kanıt KRİTİK YOL bloğunu taşır (explain_trace_critpath.go) ve istem kuralı
// (copilot.systemTraceBody "LATENCY OWNER IS PRECOMPUTED") modelden bu satırla
// başlamasını ister; küçük yerel modeller kalın başlıkla başlayabilir. "Sunucu
// toplar, model anlatır" (sloAnswerWithBurnVerdict emsali): satır kanıt
// METNİNDEN (tracechain.ExternalWaitLineFromBlock) kurulur — evalset aynı
// fonksiyonu aynı girdiyle çağırır (evalServerCompose "Trace"), puanlanan
// operatörün gördüğüdür. Önbellek anahtarı değişmez; cevap ilk satırı zaten
// doğru satırsa aynen, yanlış süre/hedefle yazılmışsa o satır düzeltilir.
// Dış bekleme yoksa (ya da blok yoksa) cevap bayt bayt aynı.

import (
	"context"
	"strings"

	"github.com/cilcenk/coremetry/internal/tracechain"
)

// traceAnswerWithLatencyOwner — SAF: evidence = modele giden kullanıcı metni.
func traceAnswerWithLatencyOwner(evidence, answer string) string {
	line := tracechain.ExternalWaitLineFromBlock(evidence)
	if line == "" || strings.TrimSpace(answer) == "" {
		return answer
	}
	first, rest, _ := strings.Cut(answer, "\n")
	norm := strings.TrimSpace(strings.Trim(strings.TrimSpace(first), "*_#> "))
	if norm == line {
		return answer
	}
	if strings.HasPrefix(norm, tracechain.ExternalWaitPrefix) || strings.HasPrefix(norm, "Dış bağımlılık bekleme:") {
		return line + "\n" + rest // model yazdı ama değer kanıttakiyle aynı değil
	}
	return line + "\n\n" + answer
}

// traceComposeRun — ✨ düğmesinin üretim yarısını sarar: üretilen (ve
// önbelleğe yazılan) metin mekanik satırı taşır.
func traceComposeRun(run explainRun, evidence string) explainRun {
	return func(onDelta func(string)) (string, error) {
		out, err := run(onDelta)
		if err != nil {
			return out, err
		}
		return traceAnswerWithLatencyOwner(evidence, out), nil
	}
}

// streamTraceExplainCore / streamTraceExplainCodeCore — sohbet içi trace
// açıklamasının (chat_explain_turn.go, trace_explain_unified.go) çekirdekleri:
// streamExplainCore / streamExplainCodeCore'un AYNISI, yalnız üretilen metin
// önbelleğe yazılmadan ÖNCE mekanik satırı alır — düğme ile sohbet aynı
// anahtarı paylaşır (v0.10.1400/1406), ikisi de aynı metni saklar.
func (s *Server) streamTraceExplainCore(ctx context.Context, emit func(string, any), surface, system, user, cacheKey string, fresh bool) (explainCoreResult, bool) {
	return s.explainCoreRun(ctx, emit, cacheKey, fresh, traceComposeRun(func(onDelta func(string)) (string, error) {
		return s.copilotStreamSurface(ctx, surface, system, user, onDelta)
	}, user))
}

func (s *Server) streamTraceExplainCodeCore(ctx context.Context, emit func(string, any), surface string, p codeExplainPrep, user string, fresh bool) (explainCoreResult, bool) {
	return s.explainCoreRun(ctx, emit, p.CacheKey, fresh, func(onDelta func(string)) (string, error) {
		out, err := s.copilotExplainEvidenceCtx(ctx, surface, p.SystemNoCode, p.SystemWithCode, user, p.CC, p.SE)
		if err != nil {
			return out, err
		}
		out = traceAnswerWithLatencyOwner(user, out)
		onDelta(out)
		return out, nil
	})
}
