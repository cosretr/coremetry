package cache

import (
	"sync/atomic"
	"time"
)

// DegradedFlag — v0.10.982 — canlı lockDegraded değeri (Redis tanımlı ama
// erişilemez → bu pod always-leader Noop kilitte). Redis reprobe'u gerçek
// kilidi takınca bayrak HEMEN düşmez: LeaderHolder'lar takası ancak bir
// sonraki kalp atışında (ttl/3; LeaderTTL(1 dk) = 3 dk → 60 s) fark eder,
// diğer pod'lar kendi reprobe'larına (≈15 s) dek Noop'ta lider kalır — bu
// pencerede de birden çok yazıcı vardır. ClearAfter(grace) bayrağı grace
// boyunca doğru tutar ki "degraded sürerken her tik CH'den tazele" kuralı
// (rollout dedektörü, argocd-metrics; §10.3.1 mint öncesi FINAL okuma)
// asıl split-brain penceresinde kapanmasın (inceleme düzeltmesi).
type DegradedFlag struct {
	on    atomic.Bool
	until atomic.Int64 // UnixNano; 0 = pay yok
	now   func() time.Time
}

// NewDegradedFlag — başlangıç değeri boot'taki isLockDegraded sonucu.
func NewDegradedFlag(on bool) *DegradedFlag {
	f := &DegradedFlag{now: time.Now}
	f.on.Store(on)
	return f
}

// Load — degraded mı (ya da düşüşten sonraki pay süresinde mi).
func (f *DegradedFlag) Load() bool {
	if f.on.Load() {
		return true
	}
	u := f.until.Load()
	return u != 0 && f.now().UnixNano() < u
}

// ClearAfter — degraded biter; Load grace boyunca doğru kalır. Pay önce
// yazılır, bayrak sonra düşer: arada Load'un false okuduğu an olmaz.
func (f *DegradedFlag) ClearAfter(grace time.Duration) {
	f.until.Store(f.now().Add(grace).UnixNano())
	f.on.Store(false)
}
