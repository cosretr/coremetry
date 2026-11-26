package api

// chat_free_calls.go — v0.10.2480 (operatör-bildirimi, A/B): serbest döngünün
// bütçe muhasebesi "harcanan" ve "ücretsiz" çağrıyı ayırır.
//
// Prod v0.10.2478 turunda kayıtlı araştırma tohumlanmıştı, model yine de kök
// neden ve korelasyon kanıtını yeniden çekti; 6 çağrılık tavan dolunca turun en
// ilgili aracı (messaging_flow) "atlandı". Artık:
//   - aynı Problem için taze kayıttan karşılanan çağrı (chatProblemRecord.serve)
//     ve aynı turda aynı araç + argüman (tekrar muhafızının işaret ettiği önceki
//     sonuç) bütçeden DÜŞMEZ; adımlarda "kayıttan" / "önbellekten" görünür;
//   - tavan yalnız harcanan çağrıya uygulanır: ücretsiz çağrı tavan dolsa da
//     karşılanır (splitByChargedBudget);
//   - stop olayı ve loop_stop sinyali harcanan (calls) ile ücretsizi (free)
//     ayrı taşır.
//
// Tahmin ham argümanla yapılır (ekran kapsamı uygulanmadan); yürütmede
// gerçekleşen sonuç farklıysa hak düzeltilir (chargeFix).

import (
	"strings"
	"time"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// Ücretsiz çağrının kaynağı (step-result `served`).
const (
	servedFromRecord = "record"
	servedFromCache  = "cache"
)

// chatFieldNameRuleTR — G: tek satır üslup kuralı (istem büyümesi en az).
const chatFieldNameRuleTR = "\nCevapta araç alan adlarını (peakRatio, fingerprint gibi) yazma; değeri insan diliyle söyle (\"tabanın 9 katı\", \"hata grubu\").\n"

// splitByChargedBudget — SAF: turun çağrıları → çalışacaklar (sıra korunur;
// freeAt[k] = run[k] bütçeden düşmeyecek) ve tavanı aşanlar. free her çağrı
// için sırayla BİR kez sorulur (aynı turdaki yinelemeyi de görür).
func splitByChargedBudget(calls []copilot.ToolCall, left int, free func(copilot.ToolCall) bool) (run, over []copilot.ToolCall, freeAt []bool) {
	left = max(left, 0)
	for _, tc := range calls {
		f := free != nil && free(tc)
		switch {
		case f:
			run, freeAt = append(run, tc), append(freeAt, true)
		case left > 0:
			left--
			run, freeAt = append(run, tc), append(freeAt, false)
		default:
			over = append(over, tc)
		}
	}
	return run, over, freeAt
}

func countTrue(bs []bool) int {
	n := 0
	for _, b := range bs {
		if b {
			n++
		}
	}
	return n
}

// chargeFix — SAF: tahmin (wasFree) ile gerçekleşen (nowFree) ayrışırsa hak
// düzeltilir: ücretli sanılıp ücretsiz çıkan iade edilir, ücretsiz sanılıp
// yürüyen düşülür (0'ın altına inmez).
func chargeFix(callsLeft int, wasFree, nowFree bool) int {
	switch {
	case nowFree && !wasFree:
		callsLeft++
	case !nowFree && wasFree:
		callsLeft = max(callsLeft-1, 0)
	}
	return callsLeft
}

// loopCallFree — tahmin: kayıttan karşılanacak mı ya da bu turda aynı araç +
// ham argümanla zaten istendi mi (ilk görüşte kaydeder).
func loopCallFree(rec *chatProblemRecord, seen map[string]bool, tc copilot.ToolCall, now time.Time) bool {
	if _, ok := rec.serve(tc.Name, tc.Input, now); ok {
		return true
	}
	key := agenttools.RepeatCallKey(tc.Name, tc.Input)
	if seen[key] {
		return true
	}
	seen[key] = true
	return false
}

// servedStepResult — SAF: ücretsiz çağrının step-result gövdesi (ölçüm değil:
// durationMs yok). Önizleme sunucunun kısa cümlesi.
func servedStepResult(i int, tool, content, from string) map[string]any {
	preview := "kayıttan karşılandı — kayıtlı araştırmada var, araç yeniden koşmadı"
	if from == servedFromCache {
		preview = "önbellekten — aynı çağrı bu turda yapıldı, sonuç yukarıda"
	}
	return map[string]any{"i": i, "tool": tool, "ok": true, "served": from,
		"preview": preview, "truncated": false, "bytes": len(content)}
}

// chatStopEventFree — stop yüküne ücretsiz çağrı sayısı (0 → alan yok; eski şekil aynen).
func chatStopEventFree(ev map[string]any, free int) map[string]any {
	if free > 0 {
		ev["free"] = free
	}
	return ev
}

// noteComplexity — loop_stop künyesine karmaşıklık kademesi + düşüş.
func (x *chatExchange) noteComplexity(c loopComplexity) {
	if x == nil {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.loopShape.Tier = strings.TrimSpace(complexityTierASCII(c.Tier))
	x.loopShape.CueFallback = c.Fallback
}

// complexityTierASCII — sinyal ayrıntısı ASCII anahtar (kardinalite sınırlı).
func complexityTierASCII(t complexityTier) string {
	switch t {
	case complexitySimple:
		return "simple"
	case complexityMedium:
		return "medium"
	case complexityComplex:
		return "complex"
	}
	return ""
}

// seededPairs — SAF (F): konuşmaya sunucunun eklediği sentetik çağrı +
// sonuç çiftleri (istemci geçmişinin ardındaki kısım; yoksa nil).
func seededPairs(conv []copilot.ChatMessage, n int) []copilot.ChatMessage {
	if n < 0 || n >= len(conv) {
		return nil
	}
	return conv[n:]
}
