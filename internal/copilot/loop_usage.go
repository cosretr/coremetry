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
