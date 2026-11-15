package api

import (
	"context"
	"hash/fnv"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// v0.10.2231 (Redis R5, audit O5) — hız sınırları filo geneli.
//
// Kaynak kodu okuma/arama (chat_source_rate.go), MCP çağrı kapısı
// (mcp_gate.go) ve CoSRE sinyali (cosre_signal_rate.go) pod-yerel sabit
// dakika penceresi tutuyordu: N replikada fiilî sınır N × limit. Artık
// sayaç Redis'te: coremetry:rate:v1:<kapsam>:<kimlik özeti>:<pencere başı>,
// tek INCR (R1, EXPIRE NX → pencere uzamaz). Sınırlar, pencereler ve ret
// metinleri değişmedi.
//
// Redis yok / Noop / devre açık / hata → ok=false: çağıran bugünkü yerel
// limiter'a düşer (açık geçer; en kötü durumda bugünkü N × limit).

const (
	sharedRateKeyPrefix = "coremetry:rate:v1:"
	sharedRateTimeout   = 250 * time.Millisecond
)

// sharedRateAllow — (izin, bekleme, ok). ok=false → paylaşımlı sayaç yok,
// karar yerel limiter'ın.
func (s *Server) sharedRateAllow(ctx context.Context, scope, id string, limit int, window time.Duration, now time.Time) (bool, time.Duration, bool) {
	if s == nil || cache.IsNoop(s.cache) || !s.l2().allow() {
		return false, 0, false
	}
	w := int64(window / time.Second)
	sec := now.Unix()
	start := sec - sec%w
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	key := sharedRateKeyPrefix + scope + ":" + strconv.FormatUint(h.Sum64(), 16) + ":" + strconv.FormatInt(start, 10)
	rctx, cancel := context.WithTimeout(ctx, sharedRateTimeout)
	defer cancel()
	n, err := s.cache.Incr(rctx, key, 1, window+10*time.Second)
	s.l2().done(rctx, err)
	if err != nil || n <= 0 {
		return false, 0, false
	}
	if n > int64(limit) {
		retry := time.Unix(start+w, 0).Sub(now)
		if retry < time.Second {
			retry = time.Second
		}
		return false, retry, true
	}
	return true, 0, true
}
