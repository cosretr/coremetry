package api

// ip_rate.go — v0.10.1905 (login özeti, docs/frontend/login-redesign.md §5.6).
//
// Kimliksiz uçlar için IP-anahtarlı, sabit pencereli, pod-yerel sınırlayıcı.
// Mevcut sınırlayıcılar kullanıcı anahtarlıdır (chat_source_rate.go,
// mcp_gate.go) ve kimliksiz isteğe uymaz; allowSubscribeRate'in clientIP'si
// X-Forwarded-For'a güvenir (sahtelenebilir). Burada anahtar r.RemoteAddr'in
// YALNIZ host kısmıdır (port her bağlantıda değişir; XFF'e güvenilmez).
// Ingress arkasında anahtar ingress IP'sidir: sınır pod başına genel bir
// tavandır — uç O(1) olduğundan yeterli. N pod'da tavan N × limit.
//
// Pencere duvar saatine hizalıdır ve TÜM anahtarlar için birlikte sıfırlanır
// (tek `windowStart`): sayaç haritası pencere başına boşalır, bellek büyümez.
// Tavan (maxKeys) aşılırsa YENİ anahtar sayılmadan geçirilir — O(1) uçta
// sızdırmak, meşru yeni IP'yi reddetmekten iyidir.

import (
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	ipRateMaxKeys    = 50000
	ipRateRetryAfter = "30"
)

type ipWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	ws     time.Time
	counts map[string]int
}

func newIPWindowLimiter(limit int, window time.Duration, now func() time.Time) *ipWindowLimiter {
	if now == nil {
		now = time.Now
	}
	return &ipWindowLimiter{limit: limit, window: window, now: now, counts: map[string]int{}}
}

// allow — anahtar bu pencerede sınırı aşmadıysa true (ve sayar).
func (l *ipWindowLimiter) allow(key string) bool {
	now := l.now()
	ws := now.Truncate(l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !ws.Equal(l.ws) {
		l.ws = ws
		l.counts = map[string]int{}
	}
	n, seen := l.counts[key]
	if !seen && len(l.counts) >= ipRateMaxKeys {
		return true
	}
	if n >= l.limit {
		return false
	}
	l.counts[key] = n + 1
	return true
}

// remoteHost — r.RemoteAddr'in host kısmı (port atılır); ayrıştırılamazsa tamamı.
func remoteHost(r *http.Request) string {
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return h
	}
	return r.RemoteAddr
}
