package api

// chat_loop_steps.go — v0.10.948 (CoSRE araştırma asistanı, Faz B):
// serbest araç döngüsünün BÜTÇE ve YÜRÜTÜLMEYEN ÇAĞRI sözleşmesi.
//
// İlerleme arayüzü yalnız GERÇEKTEN yürütülen işi göstermeli (operatör
// gereksinimi 8). Eskiden döngüde yürütülmeyen üç çağrı sınıfı iki farklı
// yanlışla görünüyordu:
//
//   - bilinmeyen ad / tekrar muhafızı / kapsam reddi: `ok:false` +
//     `durationMs:0` — arayüz bunu HATA sayıyor ve Σ süreye 0 ms katıyordu;
//   - tavanı aşan çağrılar: hiç çip yok, yalnız bir etiket — model ne denedi,
//     görünmüyordu;
//   - iptal: tur kalan çağrıları sırayla yakıyordu (bitmiş ctx ile handler).
//
// Artık hepsi aynı şekil: `step` çipi (modelin NE DENEDİĞİ) + eşli
// `step-result {skipped:true, reason}` — `durationMs` YOK (ölçüm değil).
// Bütçe de sessiz değil: döngü başında "araştırma bütçesi: N araç · M tur"
// etiketi, dolunca harcanan çağrı hakkı, yürütülen ve yürütülmeyen çağrı.

import (
	"context"
	"errors"
	"fmt"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
)

// Yürütülmeyen çağrının nedeni (step-result `reason`). Executor'ın Kind
// değerleriyle aynı sözlük + döngünün kendi tavanı.
const (
	skipReasonUnknown   = agenttools.KindUnknown
	skipReasonRepeated  = agenttools.KindRepeated
	skipReasonScope     = agenttools.KindScope
	skipReasonCancelled = agenttools.KindCancelled
	skipReasonCallCap   = "call_cap"
)

// skippedStepResult — yürütülmeyen çağrının step-result gövdesi (SAF).
// `ok:false` eski istemciler için (skipped'ı bilmeyen çip "başarılı" demesin);
// `durationMs` bilinçli olarak YOK: Σ süre yalnız ölçülenlerden (toolSteps.ts).
// Önizleme modele giden metnin aynısı — operatör nedeni oradan okur.
func skippedStepResult(i int, tool, content, reason string) map[string]any {
	preview, truncated := clipStepPreview(content)
	return map[string]any{
		"i": i, "tool": tool, "ok": false, "skipped": true, "reason": reason,
		"preview": preview, "truncated": truncated, "bytes": len(content),
	}
}

// chatBudgetLabelTR — döngü başındaki bütçe etiketi (SAF).
func chatBudgetLabelTR(calls, rounds int) string {
	return fmt.Sprintf("araştırma bütçesi: %d araç · %d tur", calls, rounds)
}

// chatBudgetExhaustedLabelTR — bütçe dolduğunda (çağrı tavanı ya da son tur)
// açık not (SAF).
//
// v0.10.948 — slotsUsed BÜTÇEDİR, iş değil: bilinmeyen ad, tekrar ve kapsam
// reddi de hak yer ama yürümez. Eski etiket harcanan hakkı "N/6 araç" diye
// kullanılmış araç gibi gösteriyor, "yürütülmedi" sayısı da yalnız bu turun
// tavan aşımını sayıyordu (gereksinim 8: ilerleme yalnız GERÇEKTEN yürüyeni
// gösterir). Artık hak, yürütülen ve alışveriş boyunca yürütülmeyen ayrı.
func chatBudgetExhaustedLabelTR(slotsUsed, executed, skipped, roundsUsed int) string {
	s := fmt.Sprintf("araştırma bütçesi doldu (%d/%d çağrı hakkı · %d/%d tur) — %d çağrı yürütüldü",
		slotsUsed, chatMaxToolCalls, roundsUsed, chatMaxToolRounds, executed)
	if skipped > 0 {
		s += fmt.Sprintf(", %d yürütülmedi", skipped)
	}
	return s + "; eldeki kanıtla cevaplanıyor"
}

// chatCancelledMessageTR — tur ortasında ctx bittiğinde operatöre giden metin
// (SAF). Alışveriş tavanı ise mevcut tavan cümlesi (soruyu daralt); istemci
// iptali ise kısa bilgi — bağlantı çoğunlukla kopmuştur, yine de dürüst metin.
func chatCancelledMessageTR(err error, limit time.Duration) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return chatDeadlineMessageTR(limit)
	}
	return "İstek iptal edildi — kalan araç çağrıları yürütülmedi."
}
