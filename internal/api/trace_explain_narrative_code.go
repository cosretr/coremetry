package api

// trace_explain_narrative_code.go — v0.10.2482 (operatör onaylı): "Kodu da
// incele" dalının ANLATIM hâli. Model TÜR + KARAR + kısa "Kod bulgusu"
// (dosya:satır, kodun başarısız değerle ne yaptığı) + Sonraki adım yazar;
// kodu yeniden yazmaz. v0.9.1254 kuralı AYNEN: kod İÇERİĞİ tarayıcıya gitmez —
// `trace_evidence` bloğu kod alanı taşımaz; UI yalnız mevcut kod künyesini
// (dosya, satır aralığı, işaretli satır numarası, depo, DevOps linki —
// codePayload / CodeSourceFooter) katlanır parçada gösterir. Bölümlü biçim
// (traceCodePromptAndKey) bayt bayt aynı.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
)

// traceNarrativeCodeMaxTokens — kod dalında Kod bulgusu için ek pay.
const traceNarrativeCodeMaxTokens = 1200 // v0.10.2483: iki paragraflık KARAR + Kod bulgusu

// traceNarrativeCodeCtx — kod dalının anlatım ctx'i (tavan daha geniş).
func traceNarrativeCodeCtx(ctx context.Context) context.Context {
	return copilot.WithAnswerCap(traceNarrativeCtx(ctx), traceNarrativeCodeMaxTokens)
}

// traceCodePromptAndKeyFor — SAF: biçime göre kod dalının sistem istemi.
// Bölümlü = traceCodePromptAndKey bayt bayt; anlatımda aynı kod bloğu +
// anlatım kod eki (aday operasyonda aday eki). Anahtar parçası iki biçimde
// aynı (sistem metni zaten anahtara girer).
func traceCodePromptAndKeyFor(narrative bool, cc devops.CodeContext, se schemaEvidence) (system, keyExtra string) {
	system, keyExtra = traceCodePromptAndKey(cc, se)
	if !narrative {
		return system, keyExtra
	}
	if cc.Source == devops.CodeSourceOperation && !cc.Empty() && cc.OperationSource == devops.OperationSourceFunctionCode {
		return copilot.SystemPromptTraceNarrativeWithCandidate(), keyExtra
	}
	return copilot.SystemPromptTraceNarrativeWithCode(), keyExtra
}
