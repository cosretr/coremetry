package api

import (
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/servertiming"
)

// server_timing.go — v0.10.1817 (Log Search Faz 1 dilim 1.1; audit §5).
//
// /api/logs* uçları isteğin bağlamına bir servertiming.Recorder bağlar
// (withServerTiming); ES transport'u (logstore.NewES) o kaydediciye
// `es` süresini yazar, serveCached yanıt yazılmadan hemen önce
// `Server-Timing: cache;desc="<X-Cache>", es;dur=…, total;dur=…` basar.
// Kaydedicisiz uçlarda (bugün diğer her şey) başlık YOK — davranış aynı.
// CH log backend'inde `es` girdisi doğal olarak çıkmaz; total + cache kalır.

// withServerTiming — r'nin bağlamına taze kaydedici bağlar.
func withServerTiming(r *http.Request) *http.Request {
	ctx, _ := servertiming.With(r.Context())
	return r.WithContext(ctx)
}

// setServerTiming — kaydedici varsa başlığı yazar (WriteHeader'dan önce).
func setServerTiming(w http.ResponseWriter, r *http.Request, start time.Time, tier string) {
	rec := servertiming.From(r.Context())
	if rec == nil {
		return
	}
	w.Header().Set("Server-Timing", rec.Header(time.Since(start), tier))
}
