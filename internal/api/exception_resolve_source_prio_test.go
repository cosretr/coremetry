package api

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2432 — süpürme kapanışından dönen grup (state=new, kaynak auto)
// merdivende "regressed" P2 almaz; insan kapanışından dönen (regressed) alır.
func TestExceptionPriorityAutoReopenNotRegressed(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	resolved := now.Add(-30 * time.Hour).UnixNano()
	base := chstore.ExceptionGroup{Fingerprint: "a1b2c3d4e5f60718", Service: "svc-orders", Type: "DemoTimeoutError",
		Occurrences: 12, OccurrencesAtResolve: 10, ResolvedAt: &resolved,
		FirstSeen: now.Add(-72 * time.Hour).UnixNano(), LastSeen: now.Add(-2 * time.Minute).UnixNano()}
	cfg := chstore.DefaultExceptionTriage()

	auto := base
	auto.State, auto.ResolveSource = chstore.ExStateNew, chstore.ExResolveSourceAuto
	if _, reason := exceptionPriorityAt(auto, cfg, now); reason == "regressed" {
		t.Errorf("süpürme dönüşü 'regressed' gerekçesi aldı")
	}
	user := base
	user.State, user.ResolveSource = chstore.ExStateRegressed, chstore.ExResolveSourceUser
	if prio, reason := exceptionPriorityAt(user, cfg, now); prio != "P2" || reason != "regressed" {
		t.Errorf("insan kapanışı dönüşü = %s/%s, want P2/regressed", prio, reason)
	}
}
