package api

// trace_explain_narrative.go — v0.10.2480 (operatör-bildirimli, prod
// v0.10.2478: "Trace açıklamasında karar eskiden daha fazla yazı olurdu …
// çok da göz yormasın"; takipler: "KARAR kısmı önemli; hızlı da sonuç
// dönsün", "Qwen hızlı çalışsın, gerekirse tüm logları, trace'leri de verelim").
//
// Trace açıklamasının iki biçimi (cosre_conversation.traceExplainStyle):
//   - anlatim (varsayılan): copilot.SystemPromptTraceNarrative — TÜR + KARAR +
//     Sonraki adım. Model girdisi bölümlü biçimle AYNI tam kanıt (in.User:
//     HATA ZİNCİRİ + KRİTİK YOL başta, span'ler, SPAN EVENT'LERİ, loglar,
//     Oracle satırları) ve AYNI bütçe: span tavanı traceExplainSpanCap (hatalar
//     + en yavaşlar önce), LOGLAR bloğu traceExplainLogsBlockBytes / ≤30 desen
//     (hata kaynağının ve çağıranlarının logu, sonra ERROR/WARN önce;
//     GÖSTERİLMEYEN sayım satırı), Oracle ≤10 satır — hepsi kanıt ölçeğiyle.
//     Hız girdiyi kısaltmaktan değil: kısa ÇIKTI (traceNarrativeMaxTokens),
//     varsayılan kapalı düşünme (copilot.ThinkTraceNarrative), akan KARAR ve
//     modeli beklemeden çizilen kanıt bloğu (blocks.TypeTraceEvidence).
//   - bolumlu: v0.10.2456 şablonu (SystemPromptTrace) bayt bayt.
//
// "Kodu da incele" dalı v0.10.2480'de bölümlü kaldı; v0.10.2482'den beri o da
// biçime uyar (trace_explain_narrative_code.go). Takip soruları
// (copilot.TraceFollowUpAddendumNarrativeFor), odaklı soru yolu
// (trace_explain_unified.go) ve dış MCP explain_trace (mcp_trace_narrative.go)
// da biçimi izler. Önbellek anahtarı sistem + kullanıcı metninden: iki biçim
// ayrı satır.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// traceNarrativeMaxTokens — anlatım cevabının completion tavanı. Hedef ≤170
// kelime (Türkçe ~380–450 token) + TÜR satırı; cümle kesmesin diye ~2×. Yalnız
// düşünme kapalıyken uygulanır (copilot.answerCapLifted).
const traceNarrativeMaxTokens = 900

// traceExplainNarrativeOn — yürürlükteki biçim anlatım mı (blob yok = evet).
func traceExplainNarrativeOn() bool {
	return chstore.CurrentCosreConversation().TraceExplainStyle != chstore.TraceExplainSections
}

// traceExplainPromptFor — biçime göre (sistem, kullanıcı). Kullanıcı metni iki
// biçimde de aynı tam kanıt.
func traceExplainPromptFor(narrative bool, in traceExplainInput) (system, user string) {
	if narrative {
		return copilot.SystemPromptTraceNarrative(), in.User
	}
	return copilot.SystemPromptTrace(), in.User
}

// traceNarrativeCtx — anlatım çağrısının ctx'i: düşünme yüzeyi (varsayılan
// kapalı, Settings'ten açılabilir) + cevap tavanı + biçim işareti
// (v0.10.2482: prefix sayımı anlatım koşularını ayrı sayar).
func traceNarrativeCtx(ctx context.Context) context.Context {
	ctx = copilot.WithThinkingSurface(ctx, copilot.ThinkTraceNarrative)
	return context.WithValue(copilot.WithAnswerCap(ctx, traceNarrativeMaxTokens), traceNarrativeStyleKey{}, true)
}

type traceNarrativeStyleKey struct{}

// traceNarrativeStyleFrom — ctx bir anlatım koşusunun mu (traceNarrativeCtx).
func traceNarrativeStyleFrom(ctx context.Context) bool {
	on, _ := ctx.Value(traceNarrativeStyleKey{}).(bool)
	return on
}

// explainRendererTraceNarrative — künyenin çizim kipi (FE TraceNarrativeBody).
const explainRendererTraceNarrative = "trace-narrative"

func init() { explainRenderers[explainRendererTraceNarrative] = true } // arşiv sanitize beyaz listesi
