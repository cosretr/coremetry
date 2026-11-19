package modelcaps

import (
	"encoding/json"
	"strings"
)

// tuning.go — v0.10.2375: model kartından gelen SINIRLAR ve ÖRNEKLEME
// önerileri. Operatör kararı 2026-11-19: CoSRE varsayılanı Qwen3.8 Flash
// Next; GPU sınırı yok → varsayılanlar en iyi kalite ayarları, yani üreticinin
// kendi varsayılan ve önerileri (ekonomi ayarı değil).
//
// Kaynak model kartının özetidir (üretici kılavuzu); gerçek geçide karşı
// DOĞRULANMADI. Bu yüzden yeni gövde alanları (top_p / top_k /
// presence_penalty) "reddedilirse sök" sınıfındadır: StripSampling +
// copilot/sampling_fallback.go.
//
// Gönderilmeyenler (bilinçli): reasoning_effort ve preserve_thinking —
// modelin varsayılanı (xhigh, korunur) en iyi kalite ayarı; alan inmez.

// Sampling — bir kip için önerilen örnekleme değerleri.
type Sampling struct {
	Temperature     float64
	TopP            float64
	TopK            int
	PresencePenalty float64
}

// qwen38FlashNext — Qwen/Qwen3.8-Flash-Next-FP8 (prod model kimliği
// "qwen38-flash…"). Aile qwen3 kalır: düşünme anahtarı aynı.
//
// DefaultMaxTokens 32768: düşünme varsayılan açık ve completion bütçesinden
// yer (paket başındaki Qwen3 olayı: 4096'da düşünme bütçeyi yiyip boş cevap).
// 32768 genel tavanın (copilot maxMaxTokens) kendisi; pencerenin 1/8'i.
var qwen38FlashNext = Caps{
	Family:              FamilyQwen3,
	Reasoning:           true,
	ThinkingSwitch:      SwitchChatTemplate,
	ContextWindow:       262144,
	MaxOutputTokens:     131072,
	DefaultMaxTokens:    32768,
	RecommendedSampling: true,
	EvidenceScaling:     true, // v0.10.2378 — kanıt bütçeleri pencereyle ölçeklenir
	SamplingThinking:    Sampling{Temperature: 1.0, TopP: 0.95, TopK: 20, PresencePenalty: 0},
	SamplingDirect:      Sampling{Temperature: 0.7, TopP: 0.8, TopK: 20, PresencePenalty: 1.5},
}

// isQwen38FlashNext — m küçük harfli ad. "qwen3.8" ya da "qwen38" + "flash".
func isQwen38FlashNext(m string) bool {
	return (strings.Contains(m, "qwen3.8") || strings.Contains(m, "qwen38")) && strings.Contains(m, "flash")
}

// SamplingFor — modelin bu kip için önerisi; ok=false: öneri yok (gövde ve
// sıcaklık çağıranın bugünkü değerinde kalır). Düşünme "off" değilse düşünme
// kipi (profil boşken model varsayılan olarak düşünür).
func SamplingFor(c Caps, th Thinking) (Sampling, bool) {
	if !c.RecommendedSampling {
		return Sampling{}, false
	}
	if th == ThinkingOff {
		return c.SamplingDirect, true
	}
	return c.SamplingThinking, true
}

// Örnekleme gövde anahtarları. temperature burada YOK: çekirdek alan
// (provider.Request.Temperature), reddedilme sınıfına girmez.
const (
	keyTopP            = "top_p"
	keyTopK            = "top_k"
	keyPresencePenalty = "presence_penalty"
)

// SamplingKeys — gövdeye inen (ve reddedilirse sökülen) alanların tek listesi.
func SamplingKeys() []string { return []string{keyPresencePenalty, keyTopK, keyTopP} }

// WithSampling — extra'nın KOPYASINA örnekleme alanlarını ekler (extra nil
// olabilir; girdi değişmez). Var olan anahtar korunur.
func WithSampling(extra map[string]any, s Sampling) map[string]any {
	out := make(map[string]any, len(extra)+3)
	for k, v := range extra {
		out[k] = v
	}
	for k, v := range map[string]any{keyTopP: s.TopP, keyTopK: s.TopK, keyPresencePenalty: s.PresencePenalty} {
		if _, taken := out[k]; !taken {
			out[k] = v
		}
	}
	return out
}

// StripSampling — SAF: ham JSON istek gövdesinden örnekleme alanlarını söker.
// Dönen: alansız gövde, gövdedeki model adı, ok. ok=false: gövde JSON nesnesi
// değil ya da sökülecek alan yok (çağıran yeniden denemez). Diğer alanların
// baytları aynen korunur (json.RawMessage; anahtar sırası zaten sıralı).
func StripSampling(body []byte) (stripped []byte, model string, ok bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return nil, "", false
	}
	found := false
	for _, k := range SamplingKeys() {
		if _, has := m[k]; has {
			delete(m, k)
			found = true
		}
	}
	if !found {
		return nil, "", false
	}
	_ = json.Unmarshal(m["model"], &model)
	out, err := json.Marshal(m)
	if err != nil {
		return nil, "", false
	}
	return out, model, true
}

// MaxTokensCeiling — SAF: completion bütçesinin tavanı. general = çağıranın
// genel tavanı (copilot maxMaxTokens); modelin belgelenmiş çıktı tavanı daha
// büyükse o; pencere biliniyorsa en çok yarısı (girdiye yer kalsın).
func MaxTokensCeiling(c Caps, window, general int) int {
	ceiling := general
	if c.MaxOutputTokens > ceiling {
		ceiling = c.MaxOutputTokens
	}
	if window > 0 && window/2 < ceiling {
		ceiling = window / 2
	}
	return ceiling
}

// ── v0.10.2378 — KANIT BÜTÇESİ ÖLÇEĞİ ──────────────────────────────────────
//
// CoSRE'nin modele gösterdiği kanıtın (span listesi, exception mesajı ve
// stack'i, log satırları, araç sonuçları, konuşma geçmişi) tavanları 8k–32k
// pencereli küçük model için seçilmiş sabitlerdi. 262144 pencereli modelde
// aynı sabitler kanıtı keser, pencerenin çoğu boş kalır (operatör
// 2026-11-19: "Modelin limitlerini kullanalım").
//
// Ölçek = yürürlükteki pencere / EvidenceBaselineWindow, [1, EvidenceScaleMax].
// YALNIZ EvidenceScaling yeteneği olan kayıtlı modelde; yeteneksiz her model
// (gemma, düz qwen3, bilinmeyen) pencere ne olursa olsun 1 alır — istem bayt
// bayt bugünkü. Geçit modeli daha küçük pencereyle açmışsa ölçek onunla iner.

// EvidenceBaselineWindow — bugünkü kanıt sabitlerinin tasarlandığı pencere
// (jeton): 6000 rune'luk araç sonucu / geçmiş / çekmece kanıtı 32k sınıfı
// yerel model içindi.
const EvidenceBaselineWindow = 32768

// EvidenceScaleMax — ölçeğin tavanı. 8 = kayıtlı tek modelin yerel penceresi
// (262144) / taban (32768). Üstü bilinçli kapalı: (a) tavanlar ve bütün-istem
// koruması (api/evidence_scale.go) 8'de doğrulandı; (b) geçit modelin yerel
// penceresinden büyük bir sayı bildirirse (yanlış yapılandırma) bütçe onu
// izlemez; (c) geçmiş her turda yeniden gönderilir, doğrusal büyüyen girdi
// işleme süresi 8 katın ötesinde ölçülmeden açılmaz.
const EvidenceScaleMax = 8

// EvidenceScale — SAF: kanıt bütçesi çarpanı (≥1). window = çağrının
// yürürlükteki penceresi (jeton; 0 = bilinmiyor → 1).
func EvidenceScale(c Caps, window int) int {
	if !c.EvidenceScaling || window <= 0 {
		return 1
	}
	return max(1, min(window/EvidenceBaselineWindow, EvidenceScaleMax))
}
