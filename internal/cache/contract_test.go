package cache

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// v0.10.2227 (Redis R1) — Pipeline + Incr sözleşmesi. Noop her zaman;
// gerçek Redis COREMETRY_TEST_REDIS_URL verilirse (ör. redis://localhost:6379/15).
func TestNoopPipelineIncr(t *testing.T) {
	c, _ := NewNoop()
	ctx := context.Background()
	if err := c.Pipeline(ctx, []PipeOp{{Key: "a", Value: []byte("1"), TTL: time.Second}, {Key: "a", Del: true}}); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Incr(ctx, "n", 1, time.Hour); err != nil || v != 0 {
		t.Fatalf("Noop Incr (0, nil) dönmeli: %d %v", v, err)
	}
	sw := NewSwitchableCache(c)
	if v, err := sw.Incr(ctx, "n", 1, time.Hour); err != nil || v != 0 {
		t.Fatal("Switchable Incr iletmeli")
	}
}

func TestRedisPipelineIncrContract(t *testing.T) {
	url := os.Getenv("COREMETRY_TEST_REDIS_URL")
	if url == "" {
		t.Skip("COREMETRY_TEST_REDIS_URL yok")
	}
	c, _, err := New(url)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := "coremetry:test:contract:" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":"
	if err := c.Pipeline(ctx, []PipeOp{
		{Key: p + "a", Value: []byte("x"), TTL: time.Minute},
		{Key: p + "b", Value: []byte("y"), TTL: time.Minute},
		{Key: p + "a", Del: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, p+"a"); ok {
		t.Fatal("pipeline sırası: a silinmeli")
	}
	if v, ok, _ := c.Get(ctx, p+"b"); !ok || string(v) != "y" {
		t.Fatal("pipeline Set yazmalı")
	}
	for i := 1; i <= 3; i++ {
		v, err := c.Incr(ctx, p+"n", 1, time.Minute)
		if err != nil || v != int64(i) {
			t.Fatalf("Incr %d: %d %v", i, v, err)
		}
	}
	if raw, ok, _ := c.Get(ctx, p+"n"); !ok || string(raw) != "3" {
		t.Fatalf("Incr değeri Get ile ondalık okunmalı: %q", raw)
	}
	rc := c.(*redisCache)
	ttl1 := rc.cli.PTTL(ctx, p+"n").Val()
	if ttl1 <= 0 || ttl1 > time.Minute {
		t.Fatalf("Incr TTL koymalı: %v", ttl1)
	}
	if _, err := c.Incr(ctx, p+"n", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if ttl2 := rc.cli.PTTL(ctx, p+"n").Val(); ttl2 > time.Minute {
		t.Fatalf("EXPIRE NX: sonraki artış pencereyi uzatmamalı: %v", ttl2)
	}
	_ = c.Pipeline(ctx, []PipeOp{{Key: p + "b", Del: true}, {Key: p + "n", Del: true}})
}
