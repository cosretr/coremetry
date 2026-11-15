package api

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// v0.10.2226 (Redis R0, G8) — L2 (Redis) yanıt önbelleği için devre kesici.
//
// Sorun: Redis kesintisinde her L1 miss'i Redis'i istemci sınırı kadar
// (500 ms) bekliyor, sonra CH'ye düşüyordu; L1 tavanı 5 sn kaldığı için
// polled anahtar 30 sn yerine 5 sn'de bir × replika hesaplanıyordu.
//
// Kural (denetim §4 A sınıfı): art arda l2BreakerThreshold hata → devre
// açık; l2BreakerCooldown boyunca L2 okuma/yazma hiç denenmez (miss sayılır,
// 0 ms). Süre dolunca TEK istek yoklar (yarı açık): başarı devreyi kapatır,
// hata yeniden açar. Devre açıkken L1 tavanı l1TTL değil anahtarın kendi
// TTL'i — Redis'in taşıdığı paylaşımlı tazeliği süreç içi kopya taşır.
// Yeni ayar yok; eşikler sabit.
const (
	l2BreakerThreshold = 5
	l2BreakerCooldown  = 5 * time.Second
)

type l2Breaker struct {
	fails     atomic.Int32
	openUntil atomic.Int64 // UnixNano; 0 = kapalı
	probing   atomic.Bool
	now       func() time.Time
}

func newL2Breaker() *l2Breaker { return &l2Breaker{now: time.Now} }

// allow — bu çağrı Redis'e gidebilir mi. Kapalıyken her zaman; açıkken
// soğuma bitince yalnız bir yoklayıcı.
func (b *l2Breaker) allow() bool {
	u := b.openUntil.Load()
	if u == 0 {
		return true
	}
	if b.now().UnixNano() < u {
		return false
	}
	return b.probing.CompareAndSwap(false, true)
}

// open — devre açık mı (yarı açık dahil): L1 tavanı kararı için.
func (b *l2Breaker) open() bool { return b.openUntil.Load() != 0 }

// done — Redis çağrısının sonucu. ctx iptali (istemci koptu) Redis hatası
// sayılmaz.
func (b *l2Breaker) done(ctx context.Context, err error) {
	if err == nil {
		if b.openUntil.Load() != 0 || b.fails.Load() != 0 {
			b.fails.Store(0)
			if b.openUntil.Swap(0) != 0 {
				log.Printf("[cache] L2 circuit CLOSED — Redis answering again")
			}
			b.probing.Store(false)
		}
		return
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		b.probing.Store(false) // yoklayıcı iptal olduysa sıradaki yoklasın
		return
	}
	until := b.now().Add(l2BreakerCooldown).UnixNano()
	if b.probing.Load() {
		b.openUntil.Store(until)
		b.probing.Store(false)
		return
	}
	if b.fails.Add(1) >= l2BreakerThreshold && b.openUntil.CompareAndSwap(0, until) {
		log.Printf("[cache] L2 circuit OPEN — %d consecutive Redis errors (last: %v); L2 skipped, L1 holds entries for their TTL", l2BreakerThreshold, err)
	}
}

// l1Cap — L1'de tutma süresi: normalde min(ttl, l1TTL); devre açıkken ttl.
func (b *l2Breaker) l1Cap(ttl time.Duration) time.Duration {
	if b.open() {
		return ttl
	}
	return minDur(ttl, l1TTL)
}

var l2Breakers sync.Map // *Server → *l2Breaker (api.go büyümez)

func (s *Server) l2() *l2Breaker {
	if b, ok := l2Breakers.Load(s); ok {
		return b.(*l2Breaker)
	}
	b, _ := l2Breakers.LoadOrStore(s, newL2Breaker())
	return b.(*l2Breaker)
}

// l2Get — devre kesicili L2 okuma; devre açıkken Redis'e gitmeden miss.
func (s *Server) l2Get(ctx context.Context, key string) ([]byte, bool) {
	b := s.l2()
	if !b.allow() {
		return nil, false
	}
	t0 := time.Now()
	raw, ok, err := s.cache.Get(ctx, key)
	selfobs.ObserveCacheL2("get", time.Since(t0), err) // v0.10.2227 (R1)
	b.done(ctx, err)
	return raw, err == nil && ok
}

// l2Set — devre kesicili L2 yazma.
func (s *Server) l2Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	b := s.l2()
	if !b.allow() {
		return nil
	}
	t0 := time.Now()
	err := s.cache.Set(ctx, key, val, ttl)
	selfobs.ObserveCacheL2("set", time.Since(t0), err)
	b.done(ctx, err)
	return err
}
