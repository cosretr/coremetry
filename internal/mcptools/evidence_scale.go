package mcptools

// evidence_scale.go — v0.10.2384: araçların KENDİ alan tavanları çağrının
// kanıt ölçeğiyle büyür (v0.10.2378'in ulaşamadığı yer: araç, api katmanı
// sonucu görmeden önce gövdeyi / stack'i / durum metnini kesiyordu).
//
// ÖLÇEK CTX'TE GELİR: in-app sohbet döngüsü (api) modele gidecek araç
// çağrısının ctx'ine WithEvidenceScale(ctx, n) yazar (n = o çağrının
// profilindeki çarpan, 1…8). Bu paket model adı, profil ya da copilot
// BİLMEZ: yalnız sayıyı okur. Ctx'te ölçek yoksa 1'dir — dış MCP sunucusu
// (internal/mcp) ölçek yazmaz, dış istemciler her sonucu bayt bayt bugünkü
// gibi alır.
//
// ÖLÇEKLENEN: modelin OLGU olarak okuduğu kanıt metni (log gövdesi ve
// öznitelik değeri, exception mesajı / stack, span durum ve exception metni,
// problem açıklaması, runbook gövdesi, kaynak kod sonucu, wiki penceresi) ve
// v2 araç gövdesinin bayt bütçesi. Arka uç okuması büyümeyen (zaten okunmuş
// veriden kurulan) satır sayıları da ölçeklenir. ÖLÇEKLENMEYEN: kimlik /
// etiket / başlık tavanları, arayüz bloğu önizlemeleri, araç GİRDİSİ doğrulama
// sınırları, pencere / süre / güvenlik tavanları, bileşik araçların (sweep,
// pivot, log spike, healthcheck) önbelleklenen bulgu satırları ve şemada
// `maximum` ile ilan edilmiş limitler.
//
// ÖNBELLEK: gövdesi ölçeğe bağlı her Memo anahtarı ölçeği taşır
// (scaleCacheKey; ölçek 1'de anahtar aynen) — geniş modelin büyük gövdesi
// küçük modele ya da tersine servis edilmez.
//
// API TAVANIYLA İLİŞKİ: api tarafı aynı çarpanla chatToolResultMaxRunes ×
// ölçek (v1) ve chatV2ToolTextMaxBytes × ölçek (v2) uygular; buradaki araç
// bütçeleri (V2ToolMaxBytes, SourceResultMaxRunes, WikiWindowRunes) AYNI
// çarpanla büyüdüğü için oran ölçek 1'deki gibidir: araç bütçesi iç sınır,
// api tavanı dış sınır.

import (
	"context"
	"strconv"
)

// EvidenceScaleMax — ölçeğin üst sınırı (çağıranın hesapladığı çarpanla aynı
// aralık, 1…8; burada yalnız bozuk girdiye karşı kelepçe).
const EvidenceScaleMax = 8

type evidenceScaleKey struct{}

// WithEvidenceScale — ctx: bu araç çağrısının kanıt ölçeği. n ≤ 1 → ctx
// aynen (ölçek yok = 1); n > EvidenceScaleMax → EvidenceScaleMax.
func WithEvidenceScale(ctx context.Context, n int) context.Context {
	if n <= 1 {
		return ctx
	}
	return context.WithValue(ctx, evidenceScaleKey{}, min(n, EvidenceScaleMax))
}

// EvidenceScale — ctx'teki kanıt ölçeği; yoksa 1.
func EvidenceScale(ctx context.Context) int {
	if ctx == nil {
		return 1
	}
	if n, ok := ctx.Value(evidenceScaleKey{}).(int); ok && n > 1 {
		return min(n, EvidenceScaleMax)
	}
	return 1
}

// scaleCap — SAF: taban tavan × ölçek (ölçek ≤ 1 → taban aynen).
func scaleCap(base, scale int) int {
	if scale <= 1 {
		return base
	}
	return base * min(scale, EvidenceScaleMax)
}

// scaledCap — taban tavan × ctx'teki ölçek.
func scaledCap(ctx context.Context, base int) int {
	return scaleCap(base, EvidenceScale(ctx))
}

// scaleCacheKey — SAF: gövdesi ölçeğe bağlı Memo anahtarı. Ölçek 1'de anahtar
// AYNEN (bugünkü önbellek girdileri geçerli kalır).
func scaleCacheKey(key string, scale int) string {
	if scale <= 1 {
		return key
	}
	return key + ":es" + strconv.Itoa(min(scale, EvidenceScaleMax))
}
