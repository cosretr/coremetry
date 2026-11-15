package api

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache/cachetest"
	"github.com/cilcenk/coremetry/internal/mcp"
)

// v0.10.2231 (Redis R5) — hız sınırları filo geneli.

// İki replika tek Redis: sınır paylaşılır (60, 2 × 60 değil).
func TestSharedRateLimitAcrossReplicas(t *testing.T) {
	prev := cosreSignalRate
	cosreSignalRate = newCosreSignalLimiter(func() time.Time { return time.Unix(1_800_000_010, 0) })
	t.Cleanup(func() { cosreSignalRate = prev })
	mem := cachetest.NewMem()
	a, b := genTestServer(mem), genTestServer(mem)
	ctx := context.Background()
	allowed := 0
	for i := 0; i < cosreSignalsPerMin; i++ {
		s := a
		if i%2 == 1 {
			s = b
		}
		if ok, _ := s.cosreSignalAllow(ctx, "u1"); ok {
			allowed++
		}
	}
	if allowed != cosreSignalsPerMin {
		t.Fatalf("sınır içinde hepsi geçmeli: %d", allowed)
	}
	ok, wait := b.cosreSignalAllow(ctx, "u1")
	if ok || wait < time.Second {
		t.Fatalf("filo sınırı aşıldı: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := a.cosreSignalAllow(ctx, "u2"); !ok {
		t.Fatal("başka kullanıcı etkilenmemeli")
	}

	// MCP kapısı: aynı kimlik, iki replika.
	at := time.Unix(1_800_000_070, 0)
	call := mcp.GateCall{Kind: "tool", Name: "list_services"}
	for i := 0; i < mcpRateLimit; i++ {
		s := a
		if i%2 == 1 {
			s = b
		}
		if err := s.mcpGateDecide(auth.RoleAdmin, "agent-1", call, at); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if err := a.mcpGateDecide(auth.RoleAdmin, "agent-1", call, at); err == nil {
		t.Fatal("MCP filo sınırı aşıldı")
	}
}

// Redis kesik: pod-yerel limiter (bugünkü davranış) — her pod kendi 60'ı.
func TestSharedRateLimitFallsBackLocal(t *testing.T) {
	prev := cosreSignalRate
	cosreSignalRate = newCosreSignalLimiter(func() time.Time { return time.Unix(1_800_000_010, 0) })
	t.Cleanup(func() { cosreSignalRate = prev })
	mem := cachetest.NewMem()
	mem.Down.Store(true)
	a := genTestServer(mem)
	ctx := context.Background()
	for i := 0; i < cosreSignalsPerMin; i++ {
		if ok, _ := a.cosreSignalAllow(ctx, "u1"); !ok {
			t.Fatalf("Redis yokken yerel limiter izin vermeli: %d", i)
		}
	}
	if ok, _ := a.cosreSignalAllow(ctx, "u1"); ok {
		t.Fatal("yerel limiter yine sınırı uygulamalı")
	}
}
