package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// countingDownCache — Redis kesintisi; kaç Get'in Redis'e ulaştığını sayar.
type countingDownCache struct {
	fakeCache
	gets atomic.Int32
	down atomic.Bool
}

func (c *countingDownCache) Get(context.Context, string) ([]byte, bool, error) {
	c.gets.Add(1)
	if c.down.Load() {
		return nil, false, errRedisDown
	}
	return nil, false, nil
}

// v0.10.2226 (Redis R0, G8): art arda hata devreyi açar → L2 atlanır
// (Redis'e çağrı yok); soğuma sonrası tek yoklayıcı geçer, başarı kapatır.
// Devre açıkken L1 tavanı anahtarın TTL'i.
func TestL2BreakerOpensAndRecovers(t *testing.T) {
	c := &countingDownCache{}
	c.down.Store(true)
	s := &Server{cache: c, l1: newL1Cache(8), stats: newCacheStats()}
	clock := time.Unix(1_800_000_000, 0)
	b := s.l2()
	b.now = func() time.Time { return clock }
	ctx := context.Background()

	for i := 0; i < l2BreakerThreshold; i++ {
		s.l2Get(ctx, "k")
	}
	if !b.open() {
		t.Fatal("eşikte devre açılmalı")
	}
	if got := b.l1Cap(30 * time.Second); got != 30*time.Second {
		t.Fatalf("devre açıkken L1 tavanı TTL olmalı: %v", got)
	}
	before := c.gets.Load()
	for i := 0; i < 50; i++ {
		s.l2Get(ctx, "k")
	}
	if c.gets.Load() != before {
		t.Fatal("devre açıkken Redis'e gidilmemeli")
	}
	// Soğuma bitti, Redis hâlâ yok: tek yoklayıcı, yeniden açılır.
	clock = clock.Add(l2BreakerCooldown)
	s.l2Get(ctx, "k")
	s.l2Get(ctx, "k")
	if c.gets.Load() != before+1 || !b.open() {
		t.Fatalf("yarı açıkta tek yoklama, hata → açık: gets=%d", c.gets.Load()-before)
	}
	// Redis döndü.
	c.down.Store(false)
	clock = clock.Add(l2BreakerCooldown)
	s.l2Get(ctx, "k")
	if b.open() {
		t.Fatal("başarılı yoklama devreyi kapatmalı")
	}
	if got := b.l1Cap(30 * time.Second); got != l1TTL {
		t.Fatalf("kapalı devrede L1 tavanı l1TTL: %v", got)
	}
}

// İstek iptali Redis hatası sayılmaz.
func TestL2BreakerIgnoresCanceledCtx(t *testing.T) {
	b := newL2Breaker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < 2*l2BreakerThreshold; i++ {
		b.done(ctx, context.Canceled)
	}
	if b.open() {
		t.Fatal("iptal edilen istekler devreyi açmamalı")
	}
}
