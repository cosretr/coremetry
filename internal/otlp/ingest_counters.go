package otlp

// ingest_counters.go — v0.10.754 (trace bütünlüğü denetimi Q2; operatör
// onayı 2026-09-17): SAYAÇSIZ kayıp noktaları sayılır ve /api/health'e
// çıkar (`otlp_ingest_rejects`). Eskiden:
//   - HTTP gövdesi çözülemedi → 400, log yok, sayaç yok (collector 4xx'i
//     yeniden denemez → sessiz kalıcı kayıp);
//   - 32 MiB üstü gövde LimitReader'da KESİLİP çözme hatasına düşüyordu
//     (400) — şimdi 413 + sayaç;
//   - gRPC MaxRecvMsgSize aşımı kütüphaneden ResourceExhausted, handler
//     hiç çağrılmaz → sayaç yok; şimdi stats handler yakalar;
//   - boş trace_id/span_id '' olarak SAKLANIR (listelenemez), geçersiz
//     zaman damgası (0 / gelecek / saklama dışı) saklanıp TTL'de yiter —
//     ikisi de sayılır, DÜŞÜRÜLMEZ (operatör: "ham span kaybolmasın";
//     düşürmek kaybın ta kendisi olurdu, sayaç görünür kılar).
//
// Log örneklemeli: n = 1, 2, 4, 8, … ve her 1000'de bir (bozuk bir istemci
// pod logunu boğmasın). Sayaçlar pod-içi ve restart'ta sıfırlanır (health
// sayaçlarıyla aynı sözleşme); mutabakat paneli ayrı dilim.

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

var ingestRejects struct {
	httpDecode    atomic.Uint64 // POST /v1/* gövdesi çözülemedi (400) — istek
	httpOversize  atomic.Uint64 // gövde > maxBodyBytes (413) — istek
	grpcOversize  atomic.Uint64 // gRPC MaxRecvMsgSize aşımı — RPC
	spanEmptyID   atomic.Uint64 // trace_id ya da span_id boş — span (saklandı)
	spanInvalidTs atomic.Uint64 // start 0 / > now+1h / < now-31g — span (saklandı)
}

// IngestRejectCounts — /api/health `otlp_ingest_rejects`.
func IngestRejectCounts() map[string]uint64 {
	return map[string]uint64{
		"http_decode_failed":   ingestRejects.httpDecode.Load(),
		"http_body_too_large":  ingestRejects.httpOversize.Load(),
		"grpc_message_too_big": ingestRejects.grpcOversize.Load(),
		"span_empty_id":        ingestRejects.spanEmptyID.Load(),
		"span_invalid_time":    ingestRejects.spanInvalidTs.Load(),
	}
}

// logSampled — n = 1,2,4,8,… ya da 1000'in katı.
func logSampled(n uint64, format string, args ...any) {
	if n&(n-1) == 0 || n%1000 == 0 {
		log.Printf(format, args...)
	}
}

// maxBodyBytes — çözülmüş HTTP gövde tavanı (sıkıştırma bombası koruması).
const maxBodyBytes = 32 << 20

var errBodyTooLarge = errors.New("request body exceeds 32 MiB (decompressed)")

// Span zaman damgası sınırları: gelecekten 1 saat (saat kayması payı),
// geçmişten 31 gün (spans TTL'i 30 gün — daha eskisi yazıldığı gibi yiter).
const (
	spanTsFutureSlack = time.Hour
	spanTsPastLimit   = 31 * 24 * time.Hour
)

var convertNow = time.Now // test: sabit "şimdi"

// validSpanStart — SAF: 0 değil, [now-31g, now+1h] içinde.
func validSpanStart(startNs uint64, now time.Time) bool {
	if startNs == 0 {
		return false
	}
	t := time.Unix(0, int64(startNs))
	return !t.After(now.Add(spanTsFutureSlack)) && !t.Before(now.Add(-spanTsPastLimit))
}

// countSpanQuality — ConvertTraces span başına; saklamayı DEĞİŞTİRMEZ.
func countSpanQuality(traceID, spanID []byte, startNs uint64) {
	if len(traceID) == 0 || len(spanID) == 0 {
		n := ingestRejects.spanEmptyID.Add(1)
		logSampled(n, "[otlp] span with empty trace/span id stored as '' (unlistable) — count %d", n)
	}
	if !validSpanStart(startNs, convertNow()) {
		n := ingestRejects.spanInvalidTs.Add(1)
		logSampled(n, "[otlp] span with invalid start time %d stored (outside now-31d..now+1h; TTL may purge it) — count %d", startNs, n)
	}
}

// isGRPCOversize — kütüphanenin "received message larger than max"
// ResourceExhausted'ı; bizim errBufferFull da ResourceExhausted ama mesajı
// farklı (bufferFullMsg) — karışmaz.
func isGRPCOversize(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok || st == nil || st.Code() != codes.ResourceExhausted {
		return false
	}
	return strings.Contains(st.Message(), "larger than max")
}

// rejectStats — gRPC stats handler: handler çağrılmadan reddedilen RPC'ler
// (oversize) yalnız burada görünür.
type rejectStats struct{}

func (rejectStats) TagRPC(ctx context.Context, _ *stats.RPCTagInfo) context.Context { return ctx }
func (rejectStats) HandleRPC(_ context.Context, s stats.RPCStats) {
	if e, ok := s.(*stats.End); ok && isGRPCOversize(e.Error) {
		n := ingestRejects.grpcOversize.Add(1)
		logSampled(n, "[otlp/grpc] message rejected (larger than 32 MiB): %v — count %d", e.Error, n)
	}
}
func (rejectStats) TagConn(ctx context.Context, _ *stats.ConnTagInfo) context.Context { return ctx }
func (rejectStats) HandleConn(context.Context, stats.ConnStats)                       {}
