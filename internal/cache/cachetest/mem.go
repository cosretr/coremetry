// Package cachetest — testler için süreç içi, paylaşılabilir cache.Cache
// (v0.10.2229, Redis R3). İki Server/Notifier örneği aynı *Mem'i alırsa
// "tek Redis'e bağlı iki replika" olur. Yalnız testlerden içe aktarılır.
package cachetest

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// ErrDown — Down=true iken her çağrının hatası.
var ErrDown = errors.New("cachetest: redis down")

type entry struct {
	v   []byte
	exp time.Time // sıfır = süresiz
}

// Mem — TTL'li harita; SCAN sayacı, kesinti anahtarı.
type Mem struct {
	mu    sync.Mutex
	m     map[string]entry
	Now   func() time.Time
	Down  atomic.Bool
	Scans atomic.Int32 // ScanPrefix + DelPrefix çağrısı
	Incrs atomic.Int32
}

// NewMem — boş bellek içi "Redis".
func NewMem() *Mem { return &Mem{m: map[string]entry{}, Now: time.Now} }

var _ cache.Cache = (*Mem)(nil)

func (c *Mem) live(k string) (entry, bool) {
	e, ok := c.m[k]
	if ok && !e.exp.IsZero() && !c.Now().Before(e.exp) {
		delete(c.m, k)
		return entry{}, false
	}
	return e, ok
}

func (c *Mem) exp(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return c.Now().Add(ttl)
}

func (c *Mem) Get(_ context.Context, k string) ([]byte, bool, error) {
	if c.Down.Load() {
		return nil, false, ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.live(k)
	return e.v, ok, nil
}

func (c *Mem) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	if c.Down.Load() {
		return ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = entry{v: append([]byte(nil), v...), exp: c.exp(ttl)}
	return nil
}

func (c *Mem) SetNX(_ context.Context, k string, v []byte, ttl time.Duration) (bool, error) {
	if c.Down.Load() {
		return false, ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.live(k); ok {
		return false, nil
	}
	c.m[k] = entry{v: append([]byte(nil), v...), exp: c.exp(ttl)}
	return true, nil
}

func (c *Mem) Del(_ context.Context, k string) error {
	if c.Down.Load() {
		return ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
	return nil
}

func (c *Mem) MGet(_ context.Context, keys []string) ([][]byte, error) {
	if c.Down.Load() {
		return nil, ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([][]byte, len(keys))
	for i, k := range keys {
		if e, ok := c.live(k); ok {
			out[i] = e.v
		}
	}
	return out, nil
}

func (c *Mem) ScanPrefix(_ context.Context, p string) ([][]byte, error) {
	c.Scans.Add(1)
	if c.Down.Load() {
		return nil, ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out [][]byte
	for k := range c.m {
		if e, ok := c.live(k); ok && strings.HasPrefix(k, p) {
			out = append(out, e.v)
		}
	}
	return out, nil
}

func (c *Mem) DelPrefix(_ context.Context, p string) error {
	c.Scans.Add(1)
	if c.Down.Load() {
		return ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if strings.HasPrefix(k, p) {
			delete(c.m, k)
		}
	}
	return nil
}

func (c *Mem) Pipeline(ctx context.Context, ops []cache.PipeOp) error {
	for _, op := range ops {
		var err error
		if op.Del {
			err = c.Del(ctx, op.Key)
		} else {
			err = c.Set(ctx, op.Key, op.Value, op.TTL)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Incr — INCRBY + EXPIRE NX (redis.go ile aynı sözleşme).
func (c *Mem) Incr(_ context.Context, k string, delta int64, ttl time.Duration) (int64, error) {
	c.Incrs.Add(1)
	if c.Down.Load() {
		return 0, ErrDown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.live(k)
	n := int64(0)
	if ok {
		n, _ = strconv.ParseInt(string(e.v), 10, 64)
	}
	n += delta
	if !ok || e.exp.IsZero() {
		e.exp = c.exp(ttl)
	}
	c.m[k] = entry{v: []byte(strconv.FormatInt(n, 10)), exp: e.exp}
	return n, nil
}

// Expire — test yardımcısı: anahtarı hemen düşürür (TTL bitti / tahliye).
func (c *Mem) Expire(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, k)
}

func (c *Mem) Ping(context.Context) error {
	if c.Down.Load() {
		return ErrDown
	}
	return nil
}
func (c *Mem) Stats(context.Context) (cache.RedisStats, error) { return cache.RedisStats{}, nil }
func (c *Mem) Publish(context.Context, string, []byte) error   { return nil }
func (c *Mem) Subscribe(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}
