package evaluator

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

func TestRedisEvictDecision(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	min := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }

	// Fresh leader, counter already > 0 → baseline only (no alarm per deploy).
	var st redisEvictState
	if redisEvictDecision(&st, 500, min(0), false) {
		t.Fatal("first reading must only set the baseline")
	}
	if redisEvictDecision(&st, 500, min(1), false) {
		t.Fatal("flat counter must not open")
	}
	// Increase → opens, episode counts the delta.
	if !redisEvictDecision(&st, 530, min(2), false) || st.episode != 30 {
		t.Fatalf("increase must open (episode %d)", st.episode)
	}
	if !redisEvictDecision(&st, 540, min(3), true) || st.episode != 40 {
		t.Fatalf("further increase accumulates (episode %d)", st.episode)
	}
	// Stays open inside the quiet window, closes after it, episode resets.
	if !redisEvictDecision(&st, 540, min(17), true) {
		t.Fatal("must stay open within the quiet window")
	}
	if redisEvictDecision(&st, 540, min(18), true) || st.episode != 0 {
		t.Fatal("must close after 15 min without evictions")
	}
	// Server restart (counter drops) → new baseline, no alarm.
	if redisEvictDecision(&st, 3, min(20), false) || st.last != 3 {
		t.Fatal("counter reset must rebaseline")
	}

	// Leader change while open → carried, not resolved.
	var st2 redisEvictState
	if !redisEvictDecision(&st2, 900, min(0), true) {
		t.Fatal("open row must carry across leader change")
	}
}

func TestRedisEvictReason(t *testing.T) {
	s := redisEvictReason(1234, cache.RedisStats{EvictedKeys: 5000, UsedMemoryBytes: 800 << 20, MaxMemoryBytes: 768 << 20})
	for _, want := range []string{"1.234", "5.000", "lider kilidi", "/admin/stats", "15 dk"} {
		if !strings.Contains(s, want) {
			t.Fatalf("reason lacks %q: %s", want, s)
		}
	}
}
