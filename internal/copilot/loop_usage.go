package copilot

import "context"

// loop_usage.go — v0.10.2297: serbest döngünün TEK ai_calls satırına tur
// sayısı ve en büyük tek tur giriş token'ı.
//
// Döngü alışveriş başına bir satır yazar ve token'ı turların TOPLAMIDIR
// (api copilot_chat.go totalIn). Toplam tek başına "tek prompt 109K" diye
// okunuyordu; satır artık kaç model gidiş-dönüşü olduğunu ve en büyük tek
// prompt'u da taşır. Değer ctx'ten geçer (WithCallID emsali): RecordUsage'ın
// imzası ve diğer çağıranları değişmez; ctx'te yoksa 0 = "bilinmiyor".

type loopUsageKey struct{}

// LoopUsage — döngünün model gidiş-dönüşleri (tavan turu ve bozuk-cevap
// yeniden denemesi dahil) ve tek turun en büyük giriş token'ı.
type LoopUsage struct {
	Rounds          uint16
	PeakInputTokens uint32
	// v0.10.2318 — cevap turunun ilk token'ı (ttft_ms kolonuna yazılır),
	// turların / yürüyen araçların toplam süresi (ai.chat.turn / ai.tool span
	// sınırları), modelin istediği araç çağrısı ve tek turdaki en çok çağrı.
	// 0 = bilinmiyor.
	AnswerTTFTMs      uint32
	ModelMs           uint32
	ToolMs            uint32
	ToolCalls         uint16
	MaxRoundToolCalls uint16
	// GatewayRetry — v0.10.2321: bir tur geçici ağ geçidi hatasında bir kez
	// yeniden denendi (alışveriş başına en çok bir).
	GatewayRetry bool
	// CompactedResults — v0.10.2340: proaktif tur bütçesi tek bir model
	// çağrısında kaç araç sonucunu tam göstermedi (alışverişin en büyüğü).
	CompactedResults uint16
	// ResearchModel / ResearchTokens — v0.10.2365 (S6): profil ayrımı eşliyken
	// araştırma turlarının modeli ve giriş+çıkış token'ı (satırın model'i
	// anlatım profilidir). "" / 0 = ayrım koşmadı.
	ResearchModel  string
	ResearchTokens uint32
}

// WithLoopUsage — sonraki RecordUsage'ın satırına tur bilgisini bağlar.
// Rounds 0 ise ctx değişmez (satır "bilinmiyor" yazılır).
func WithLoopUsage(ctx context.Context, u LoopUsage) context.Context {
	if u.Rounds == 0 {
		return ctx
	}
	return context.WithValue(ctx, loopUsageKey{}, u)
}

// loopUsageFrom — ctx'teki tur bilgisi (yoksa sıfır değer).
func loopUsageFrom(ctx context.Context) LoopUsage {
	if ctx == nil {
		return LoopUsage{}
	}
	u, _ := ctx.Value(loopUsageKey{}).(LoopUsage)
	return u
}
