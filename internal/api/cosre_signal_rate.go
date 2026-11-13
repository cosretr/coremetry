package api

// cosre_signal_rate.go — v0.10.2115 (kalite döngüsü audit §6 riski, §8 soru 6;
// operatör onayı 2026-11-13): POST /api/ai/signal kullanıcı başına dakikada
// 60 tavanı; aşımda 429 + Retry-After. Rol ve kabul davranışı aynı (viewer
// da sinyal verir); yalnız taşma reddedilir. Yeni ayar / UI yok.
//
// Pod-yerel sabit dakika penceresi (chat_source_rate.go emsali): cache.Cache'te
// küme-geneli INCR yok; N pod'da üst sınır N × 60. Amaç tek sekmenin kaçak
// döngüsünün cosre_signals'ı (TTL 180 gün) şişirmesini kesmek.

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

const cosreSignalsPerMin = 60

type cosreSignalLimiter struct {
	now   func() time.Time
	mu    sync.Mutex
	win   int64
	count map[string]int
}

func newCosreSignalLimiter(now func() time.Time) *cosreSignalLimiter {
	if now == nil {
		now = time.Now
	}
	return &cosreSignalLimiter{now: now, count: map[string]int{}}
}

// cosreSignalRate — süreç-genelinde tek limiter.
var cosreSignalRate = newCosreSignalLimiter(time.Now)

// allow — kullanıcının bu dakikada hakkı varsa TÜKETİR; yoksa pencerenin
// bitişine kalan süre (≥1 sn). Boş kullanıcı (kimliksiz bağlam) sayılmaz.
// Pencere değişince sayaçlar atılır (harita = son dakikanın kullanıcıları).
func (l *cosreSignalLimiter) allow(user string) (bool, time.Duration) {
	if user == "" {
		return true, 0
	}
	now := l.now()
	sec := now.Unix()
	start := sec - sec%60
	l.mu.Lock()
	defer l.mu.Unlock()
	if start != l.win {
		l.win, l.count = start, map[string]int{}
	}
	if l.count[user] >= cosreSignalsPerMin {
		wait := time.Unix(start+60, 0).Sub(now)
		if wait < time.Second {
			wait = time.Second
		}
		return false, wait
	}
	l.count[user]++
	return true, 0
}

// writeCosreSignalLimited — 429 + Retry-After (tam saniye, yukarı yuvarlı).
func writeCosreSignalLimited(w http.ResponseWriter, wait time.Duration) {
	secs := int((wait + time.Second - 1) / time.Second)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, "too many signals: limit "+strconv.Itoa(cosreSignalsPerMin)+"/min per user", http.StatusTooManyRequests)
}
