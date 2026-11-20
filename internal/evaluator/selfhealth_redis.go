package evaluator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// selfhealth_redis.go — Redis R6 (2026-11-20): self-redis-evict.
//
// The bundled Redis/Valkey runs maxmemory + volatile-lru (K3). Every
// Coremetry key carries a TTL, so under memory pressure the server may
// evict a leader-lock or claim key next to cache values — a second pod
// can then become leader for up to one refresh interval (duplicate alert
// evaluation / notifications). R6 caps cache values at 1 MB and compresses
// them so this should not happen; this rule makes it LOUD when it does.
//
// Signal: INFO evicted_keys is cumulative since the server started, so the
// rule fires on an INCREASE seen by this leader (not on "> 0" forever) and
// resolves after selfRedisEvictQuiet with no further increase. Evidence
// gates (selfhealth.go family rules):
//   - Noop cache (no Redis) → nothing to measure; covered, opens nothing.
//   - INFO error → not measured: neither open nor close (v0.9.984).
//   - first reading after a leader change only sets the baseline; an
//     already-open row is carried (no false resolve on every deploy).
//   - counter going DOWN = server restarted → new baseline.

const (
	selfRedisEvictRuleID = "self-redis-evict"
	selfRedisEvictQuiet  = 15 * time.Minute
)

// redisEvictState — leader-local baseline (reset on leader change).
type redisEvictState struct {
	seen    bool
	last    int64     // last evicted_keys reading
	lastInc time.Time // last tick the counter grew
	episode int64     // keys evicted in the current open episode
}

// redisEvictDecision — SAF (tablo testli): should the problem be open?
func redisEvictDecision(st *redisEvictState, cur int64, now time.Time, wasOpen bool) bool {
	switch {
	case !st.seen:
		st.seen, st.last = true, cur
		if wasOpen {
			st.lastInc = now // carry an open row across leader change
		}
	case cur < st.last:
		st.last = cur // server restarted: new baseline
	case cur > st.last:
		st.episode += cur - st.last
		st.last, st.lastInc = cur, now
	}
	open := !st.lastInc.IsZero() && now.Sub(st.lastInc) < selfRedisEvictQuiet
	if !open {
		st.episode = 0
	}
	return open
}

func (e *Evaluator) selfRedisEvict(ctx context.Context, snap *chstore.OpenProblems) ([]selfProblem, bool) {
	if e.stamps == nil || cache.IsNoop(e.stamps) {
		return nil, true
	}
	st, err := e.stamps.Stats(ctx)
	if err != nil {
		log.Printf("[evaluator] self-health redis INFO okunamadı: %v", err)
		return nil, false
	}
	if !redisEvictDecision(&e.redisEvict, st.EvictedKeys, time.Now(), snap.ByID(selfRedisEvictRuleID) != nil) {
		return nil, true
	}
	return []selfProblem{{
		id:          selfRedisEvictRuleID,
		ruleID:      selfRedisEvictRuleID,
		ruleName:    "Coremetry · Redis anahtar tahliye ediyor",
		metric:      "self.redis_evicted_keys",
		comparator:  ">",
		severity:    "warning",
		value:       float64(e.redisEvict.episode),
		threshold:   0,
		description: redisEvictReason(e.redisEvict.episode, st),
	}}, true
}

// redisEvictReason — SAF gerekçe.
func redisEvictReason(episode int64, st cache.RedisStats) string {
	mem := ""
	if st.MaxMemoryBytes > 0 {
		mem = fmt.Sprintf(" (bellek %s / maxmemory %s)",
			chstore.HumanBytes(uint64(st.UsedMemoryBytes)), chstore.HumanBytes(uint64(st.MaxMemoryBytes)))
	}
	return fmt.Sprintf(
		"Redis maxmemory doldu: bu olayda %s anahtar tahliye edildi (sunucu açılışından beri %s)%s. "+
			"volatile-lru TTL'li her anahtarı silebilir — önbellek değerlerinin yanında lider kilidi ve "+
			"claim anahtarları da; bir kilit giderse iki replika kısa süre aynı arka plan işini yapabilir. "+
			"Müdahale: /admin/stats → Redis kartı (Memory, Evicted) ve /api/admin/cache-stats → l2Oversize;"+
			"redis.resources.limits.memory'yi artırın. %s dk yeni tahliye olmazsa problem kapanır.",
		fmtInt(uint64(episode)), fmtInt(uint64(st.EvictedKeys)), mem, fmt.Sprint(int(selfRedisEvictQuiet.Minutes())))
}
