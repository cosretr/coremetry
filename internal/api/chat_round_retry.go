package api

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// chat_round_retry.go — v0.10.2321: serbest döngünün model turu geçici ağ
// geçidi hatasında BİR kez yeniden denenir.
//
// Sözleşme (tek yer: chatLoopTurn, döngünün tek sağlayıcı çağrısı):
//   - geçici = 502/503/504 ya da bağlantı sıfırlanması / beklenmeyen EOF
//     (copilot.IsTransientGatewayErr); bağlam taşması ve diğer 4xx, zaman
//     aşımı, iptal ve istemcinin gitmesi ASLA;
//   - bu turdan istemciye tek bir delta bile iletildiyse yeniden deneme yok;
//   - alışveriş başına en çok BİR (tur başına değil), sabit kısa duraklamayla;
//   - tur başına en çok İKİ sağlayıcı isteği: hak varken akışın geçici hatası
//     buffered'a düşmez (copilot.WithDeferTransientFallback), yeniden deneme
//     tek buffered istektir (copilot.WithBufferedOnly); düşüş zaten ikinci
//     isteği yaptıysa (ProviderRequests ≥ 2) yeniden deneme yok.
//   - Döngü turu BİR tur görür: başarısız denemenin token'ı sayılmaz, tur
//     sayısı ve span süresi bir kez; ai_calls loop_gateway_retry ve tur
//     span'ı coremetry.ai.turn.gateway_retry işaretlenir.
// Ayar / düğme yok.

// chatRoundRetryPause — yeniden denemeden önceki sabit duraklama.
const chatRoundRetryPause = 1500 * time.Millisecond

// chatRoundRetryLabel — operatörün gördüğü adım satırı.
const chatRoundRetryLabel = "model ağ geçidi geçici hata verdi — tur bir kez yeniden deneniyor"

// chatRoundRetrySleep — test dikişi (duraklama sabit; testler beklemesin).
var chatRoundRetrySleep = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// roundRetry — alışveriş başına yeniden deneme hakkı (döngü tek goroutine).
type roundRetry struct{ used bool }

type roundRetryKey struct{}

func withRoundRetry(ctx context.Context, rr *roundRetry) context.Context {
	return context.WithValue(ctx, roundRetryKey{}, rr)
}

func roundRetryFrom(ctx context.Context) *roundRetry {
	rr, _ := ctx.Value(roundRetryKey{}).(*roundRetry)
	return rr
}

// mark — döngü satırının bayrağı.
func (rr *roundRetry) mark(lu copilot.LoopUsage) copilot.LoopUsage {
	lu.GatewayRetry = rr != nil && rr.used
	return lu
}

// shouldRetryRound — SAF karar (tablo testli).
func shouldRetryRound(ctx context.Context, err error, forwarded bool, requests uint8, used bool) bool {
	return err != nil && !used && !forwarded && requests <= 1 &&
		ctx.Err() == nil && copilot.IsTransientGatewayErr(err)
}

// callWithRoundRetry — call(ctx, onDelta) döngünün tek sağlayıcı çağrısı;
// gerekirse bir kez (buffered tek istek) yeniden.
func callWithRoundRetry(ctx context.Context, emit func(string, any), onDelta func(copilot.TextDelta),
	call func(context.Context, func(copilot.TextDelta)) (copilot.ChatTurn, error)) (copilot.ChatTurn, error) {
	rr := roundRetryFrom(ctx)
	if rr == nil || rr.used {
		return call(ctx, onDelta)
	}
	forwarded := false
	turn, err := call(copilot.WithDeferTransientFallback(ctx), func(d copilot.TextDelta) {
		forwarded = true
		onDelta(d)
	})
	if !shouldRetryRound(ctx, err, forwarded, turn.ProviderRequests, rr.used) {
		return turn, err
	}
	rr.used = true
	trace.SpanFromContext(ctx).SetAttributes(attribute.Bool("coremetry.ai.turn.gateway_retry", true))
	emit("step", map[string]string{"label": chatRoundRetryLabel})
	if chatRoundRetrySleep(ctx, chatRoundRetryPause) != nil {
		return turn, err // duraklamada iptal/tavan: ilk hata bugünkü gibi yüzeye çıkar
	}
	return call(copilot.WithBufferedOnly(ctx), onDelta)
}
