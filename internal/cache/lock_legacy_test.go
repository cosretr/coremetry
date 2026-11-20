package cache

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// sharedKV — one "Redis" shared by several pods; podLock — one pod's
// token-owning view of it (same semantics as redisLock: SetNX, token
// match on Refresh/Release).
type sharedKV struct {
	mu sync.Mutex
	m  map[string]string
}

type podLock struct {
	kv  *sharedKV
	tok string
}

func (p podLock) TryAcquire(_ context.Context, k string, _ time.Duration) (bool, error) {
	p.kv.mu.Lock()
	defer p.kv.mu.Unlock()
	if _, ok := p.kv.m[k]; ok {
		return false, nil
	}
	p.kv.m[k] = p.tok
	return true, nil
}

func (p podLock) Refresh(_ context.Context, k string, _ time.Duration) (bool, error) {
	p.kv.mu.Lock()
	defer p.kv.mu.Unlock()
	return p.kv.m[k] == p.tok, nil
}

func (p podLock) Release(_ context.Context, k string) error {
	p.kv.mu.Lock()
	defer p.kv.mu.Unlock()
	if p.kv.m[k] == p.tok {
		delete(p.kv.m, k)
	}
	return nil
}

func TestLegacyLockKeysArePrefixed(t *testing.T) {
	for k, legacy := range legacyLockKeys {
		if !strings.HasPrefix(k, lockPrefix) || strings.HasPrefix(legacy, lockPrefix) || legacy == "" {
			t.Fatalf("bad rename %q → %q", k, legacy)
		}
	}
	if got := LegacyLockKey(lockPrefix + "rb-claim:e1"); got != "rb:claim:e1" {
		t.Fatalf("prefix rule: %q", got)
	}
	if got := LegacyLockKey(lockPrefix + "evaluator"); got != "" {
		t.Fatalf("never-renamed key must have no legacy name: %q", got)
	}
}

// Rolling upgrade: an OLD pod (bare key) and NEW pods (prefixed + legacy)
// must never both be leader.
func TestLegacyLockMixedVersionMutualExclusion(t *testing.T) {
	ctx := context.Background()
	kv := &sharedKV{m: map[string]string{}}
	oldPod := podLock{kv, "old"}
	newA := withLegacyLockKeys(podLock{kv, "a"})
	newB := withLegacyLockKeys(podLock{kv, "b"})
	const key = lockPrefix + "topology-aggregator"
	const bare = "topology-aggregator-leader"

	if ok, _ := oldPod.TryAcquire(ctx, bare, time.Minute); !ok {
		t.Fatal("old pod should lead first")
	}
	if ok, _ := newA.TryAcquire(ctx, key, time.Minute); ok {
		t.Fatal("new pod must not lead while an old pod holds the bare key")
	}
	if _, held := kv.m[key]; held {
		t.Fatal("failed legacy half must release the new key")
	}
	_ = oldPod.Release(ctx, bare)

	if ok, _ := newA.TryAcquire(ctx, key, time.Minute); !ok {
		t.Fatal("new pod should lead once the old pod is gone")
	}
	if ok, _ := oldPod.TryAcquire(ctx, bare, time.Minute); ok {
		t.Fatal("old pod must not lead while a new pod holds the bare key")
	}
	if ok, _ := newB.TryAcquire(ctx, key, time.Minute); ok {
		t.Fatal("second new pod must not lead")
	}
	if ok, _ := newA.Refresh(ctx, key, time.Minute); !ok {
		t.Fatal("refresh should hold both halves")
	}

	// Legacy half evicted → refresh reports lost and frees the new half so
	// the re-acquire is not blocked by our own stale token.
	delete(kv.m, bare)
	if ok, _ := newA.Refresh(ctx, key, time.Minute); ok {
		t.Fatal("refresh must fail when the legacy half is gone")
	}
	if len(kv.m) != 0 {
		t.Fatalf("both halves should be free: %v", kv.m)
	}
	if ok, _ := newB.TryAcquire(ctx, key, time.Minute); !ok {
		t.Fatal("peer should take over")
	}
	_ = newB.Release(ctx, key)
	if len(kv.m) != 0 {
		t.Fatalf("release must drop both halves: %v", kv.m)
	}

	// Never-renamed key: single key only.
	if ok, _ := newA.TryAcquire(ctx, lockPrefix+"evaluator", time.Minute); !ok || len(kv.m) != 1 {
		t.Fatalf("plain key: %v", kv.m)
	}
}

// Real Redis / Valkey (COREMETRY_TEST_REDIS_URL): the wrapped lock New()
// returns takes and releases both names.
func TestRedisLegacyLockContract(t *testing.T) {
	url := os.Getenv("COREMETRY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("COREMETRY_TEST_REDIS_URL yok")
	}
	c, l1, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	_, l2, _ := New(url)
	ctx := context.Background()
	const key = lockPrefix + "cosre-briefing"
	_ = c.Del(ctx, key)
	_ = c.Del(ctx, "cosre-briefing")
	if ok, err := l1.TryAcquire(ctx, key, 5*time.Second); err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if _, ok, _ := c.Get(ctx, "cosre-briefing"); !ok {
		t.Fatal("legacy half not set")
	}
	if ok, _ := l2.TryAcquire(ctx, key, 5*time.Second); ok {
		t.Fatal("second holder")
	}
	if ok, err := l1.Refresh(ctx, key, 5*time.Second); err != nil || !ok {
		t.Fatalf("refresh: %v %v", ok, err)
	}
	if err := l1.Release(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, "cosre-briefing"); ok {
		t.Fatal("legacy half not released")
	}
	// INFO parsing (self-redis-evict reads EvictedKeys) works on Valkey too.
	if st, err := c.Stats(ctx); err != nil || st.Version == "" {
		t.Fatalf("stats: %+v %v", st, err)
	}
}
