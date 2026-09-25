package logstore

import (
	"sync"
	"time"
)

// esDebugLimiter — v0.10.918 (operatör prod logu): "[es-debug] zero hits"
// teşhisi her boş trace/span/servis aramasında TAM sorgu gövdesini
// basıyordu; aynı trace için art arda 6+ satır log akışını boğuyordu.
// Teşhisin değeri İLK örnekte: anahtar (trace → servis) başına pencere
// içinde bir kez yazılır. Harita tavanı aşınca sıfırlanır (bellek sınırlı).
const (
	esDebugEvery  = 10 * time.Minute
	esDebugMaxKey = 4096
)

type esDebugLimiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

var esDebugGate = &esDebugLimiter{}

// allow — SAF zaman girdisiyle: anahtar ilk kez ya da pencere dolduysa true.
func (l *esDebugLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil || len(l.seen) >= esDebugMaxKey {
		l.seen = map[string]time.Time{}
	}
	if at, ok := l.seen[key]; ok && now.Sub(at) < esDebugEvery {
		return false
	}
	l.seen[key] = now
	return true
}

// esDebugKey — trace aramaları trace'e (span'ları ayrı sayılmaz), yoksa
// servise göre gruplanır.
func esDebugKey(f Filter) string {
	if f.TraceID != "" {
		return "t:" + f.TraceID
	}
	return "s:" + f.Service
}
