package copilot

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"

	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
)

// chat_stream_retry.go — v0.10.2321: serbest döngünün model turu geçici ağ
// geçidi hatasında (502/503/504, bağlantı sıfırlanması / beklenmeyen EOF)
// alışveriş başına BİR kez yeniden denenir. Karar api katmanında
// (chat_round_retry.go); burada yalnız iki ctx anahtarı ve sınıflandırma.
//
// Tur başına en çok İKİ sağlayıcı isteği: yeniden deneme hakkı varken akışın
// geçici başarısızlığı buffered'a DÜŞMEZ (WithDeferTransientFallback) — hata
// tek istekle döner; yeniden deneme YALNIZ buffered tek istektir
// (WithBufferedOnly). Hak yoksa (ya da hata geçici değilse) akış→buffered
// düşüşü bugünkü gibi çalışır; düşüş ikinci isteği yaptıysa tur yeniden
// denenmez (ChatTurn.ProviderRequests).

type deferTransientKey struct{}
type bufferedOnlyKey struct{}

// WithDeferTransientFallback — akışın GEÇİCİ başarısızlığında buffered'a
// düşme, hatayı döndür (çağıran tek yeniden denemeyi yapacak).
func WithDeferTransientFallback(ctx context.Context) context.Context {
	return context.WithValue(ctx, deferTransientKey{}, true)
}

// WithBufferedOnly — bu tur akış denemeden tek buffered istek.
func WithBufferedOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, bufferedOnlyKey{}, true)
}

func deferTransientFrom(ctx context.Context) bool {
	v, _ := ctx.Value(deferTransientKey{}).(bool)
	return v
}

func bufferedOnlyFrom(ctx context.Context) bool {
	v, _ := ctx.Value(bufferedOnlyKey{}).(bool)
	return v
}

// noteProviderRequests — turun toplam istek sayısı (mutlak değer).
func noteProviderRequests(ctx context.Context, n uint8) {
	if st := streamStatsFromContext(ctx); st != nil {
		st.mu.Lock()
		st.requests = n
		st.mu.Unlock()
	}
}

func (st *streamStats) providerRequests() uint8 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.requests
}

// fallbackRequests — düşüş politikası (stream.go streamFallbackPolicy) ikinci
// istek yapar mı: yalnız "200 + SSE olmayan gövde" (VerdictParseBuffered)
// gövdeyi çözer, diğer her dal buffered'ı çağırır.
func fallbackRequests(fe *aiprov.StreamFallbackError) uint8 {
	if fe.Stage == aiprov.StageHead && fe.Verdict == aiprov.VerdictParseBuffered {
		return 1
	}
	return 2
}

// transientStreamFailure — akışın ilk bayttan ÖNCEKİ geçici ağ geçidi hatası.
func transientStreamFailure(fe *aiprov.StreamFallbackError) bool {
	switch fe.Stage {
	case aiprov.StageHead:
		return transientGatewayStatus(fe.Status)
	case aiprov.StageConnect, aiprov.StageEmptyStream:
		return fe.Err != nil && isConnResetOrEOF(fe.Err)
	}
	return false
}

func transientGatewayStatus(code int) bool {
	return code == http.StatusBadGateway || code == http.StatusServiceUnavailable || code == http.StatusGatewayTimeout
}

// isConnResetOrEOF — bağlantı sıfırlanması / beklenmeyen EOF. Zaman aşımı ve
// iptal ASLA (yeniden deneme uzun bekleyişi ikiye katlar / cevap beklenmiyor).
func isConnResetOrEOF(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return false
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "connection reset by peer") || strings.Contains(m, "unexpected eof")
}

// IsTransientGatewayErr — döngünün yeniden deneme sınıfı (SAF): ağ geçidi
// 502/503/504 (buffered HTTPError ya da akış başı), bağlantı sıfırlanması /
// beklenmeyen EOF. 4xx (bağlam taşması dahil), zaman aşımı ve iptal DEĞİL.
func IsTransientGatewayErr(err error) bool {
	if err == nil {
		return false
	}
	var he *aiprov.HTTPError
	if errors.As(err, &he) {
		return transientGatewayStatus(he.Status)
	}
	var fe *aiprov.StreamFallbackError
	if errors.As(err, &fe) {
		return transientStreamFailure(fe)
	}
	return isConnResetOrEOF(err)
}
