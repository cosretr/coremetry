package api

// v0.6.11 regression test — cacheInvalidatePrefix used to call
// ScanPrefix and discard the result, never actually deleting
// the matching L2 keys. The L1 + the cross-pod broadcast still
// happened, so the failure mode was operator-reported staleness
// (mute/exclude settings updated → topology view still serves
// the old data) rather than a crash.
//
// This test guards against re-regression: it instruments a fake
// Cache, calls cacheInvalidatePrefix, and asserts that
// (a) DelPrefix was invoked with the exact prefix, and
// (b) Publish broadcast the canonical "prefix:<P>" payload to
//     the invalidation channel.
//
// Original bug: the pre-fix code path read
//
//   if keys, err := s.cache.ScanPrefix(ctx, prefix); err == nil {
//       _ = keys // not used in this path; placeholder
//   }
//
// — i.e. SCAN ran but DEL never did.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// fakeCache records every method invocation so the test can
// assert what cacheInvalidatePrefix did. Implements just enough
// of cache.Cache to satisfy the interface; unused methods
// no-op.
type fakeCache struct {
	mu             sync.Mutex
	delPrefixCalls []string
	publishCalls   []publishCall
	scanPrefixHits int // pre-fix path; should stay 0 after v0.6.11
}

type publishCall struct {
	channel string
	msg     string
}

func (f *fakeCache) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, nil }
func (f *fakeCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (f *fakeCache) SetNX(context.Context, string, []byte, time.Duration) (bool, error) {
	return true, nil
}
func (f *fakeCache) Del(context.Context, string) error { return nil }
func (f *fakeCache) MGet(_ context.Context, keys []string) ([][]byte, error) {
	return make([][]byte, len(keys)), nil
}
func (f *fakeCache) ScanPrefix(_ context.Context, _ string) ([][]byte, error) {
	f.mu.Lock()
	f.scanPrefixHits++
	f.mu.Unlock()
	return nil, nil
}
func (f *fakeCache) DelPrefix(_ context.Context, prefix string) error {
	f.mu.Lock()
	f.delPrefixCalls = append(f.delPrefixCalls, prefix)
	f.mu.Unlock()
	return nil
}
func (f *fakeCache) Ping(context.Context) error { return nil }
func (f *fakeCache) Stats(context.Context) (cache.RedisStats, error) {
	return cache.RedisStats{}, nil
}
func (f *fakeCache) Publish(_ context.Context, channel string, msg []byte) error {
	f.mu.Lock()
	f.publishCalls = append(f.publishCalls, publishCall{channel, string(msg)})
	f.mu.Unlock()
	return nil
}
func (f *fakeCache) Subscribe(ctx context.Context, _ string) (<-chan []byte, error) {
	ch := make(chan []byte)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// v0.10.2229 (Redis R3): kayıt dışı önek → DelPrefix İSTEK YOLU DIŞINDA
// (goroutine), yayın yine eşzamanlı; ScanPrefix asla.
func TestCacheInvalidatePrefix_UnregisteredOffPath(t *testing.T) {
	for _, prefix := range []string{"topology-edges:", "topology-service:", "db-summary:"} {
		fc := &fakeCache{}
		srv := &Server{cache: fc, l1: newL1Cache(8), stats: newCacheStats()}
		srv.cacheInvalidatePrefix(context.Background(), prefix)
		deadline := time.Now().Add(2 * time.Second)
		for {
			fc.mu.Lock()
			n := len(fc.delPrefixCalls)
			fc.mu.Unlock()
			if n == 1 || time.Now().After(deadline) {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		fc.mu.Lock()
		if len(fc.delPrefixCalls) != 1 || fc.delPrefixCalls[0] != prefix {
			t.Errorf("%s: DelPrefix arka planda bir kez çağrılmalı: %v", prefix, fc.delPrefixCalls)
		}
		if fc.scanPrefixHits != 0 {
			t.Errorf("%s: ScanPrefix çağrılmamalı", prefix)
		}
		if len(fc.publishCalls) != 1 || fc.publishCalls[0].msg != "prefix:"+prefix {
			t.Errorf("%s: yayın %v", prefix, fc.publishCalls)
		}
		fc.mu.Unlock()
	}
}

func (*fakeCache) Pipeline(context.Context, []cache.PipeOp) error { return nil }
func (*fakeCache) Incr(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, nil
}
