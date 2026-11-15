package sre

// enqueuer.go — v0.10.1282 — otomatik üretici. YALNIZ WORKER rolünde
// (main.go `if mode.worker`, TestSREStartsOnlyUnderSREMode), lider kilidi
// "coremetry:lock:sre-enqueue", 30 s tik. Yalnız ÜRETİR: kuyruğa satır yazar,
// Problem'e ASLA dokunmaz (UpsertProblem yok — TestEnqueueNeverUpsertsProblems).
//
// Aday: OpenProblemsSnapshot (/aiops §1 — her arka plan döngüsü buradan okur)
// + EnrichProblemsWithPriority bir KOPYA üzerinde (snapshot paylaşımlı memo).
// Tavanlar: tik başına MaxPerTick, saatte MaxPerHour (Redis sayaç + bellek),
// servis başına ServiceCooldownMin (SETNX "coremetry:sre:enq:svc:<svc>" →
// problemId; aynı servisin ikinci problemi "shared" satır alır, yeniden
// koşmaz).

import (
	"context"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	enqueueLockKey   = "coremetry:lock:sre-enqueue"
	enqueueTick      = 30 * time.Second
	candidateMaxAge  = 15 * time.Minute
	hourKeyPrefix    = "coremetry:sre:enq:hour:"
	serviceKeyPrefix = "coremetry:sre:enq:svc:"
)

// EnqueuerStore — üreticinin okumaları (*chstore.Store karşılar).
type EnqueuerStore interface {
	QueueStore
	OpenProblemsSnapshot(ctx context.Context) (*chstore.OpenProblems, error)
}

type cooldownEntry struct {
	problemID string
	until     time.Time
}

type enqueuer struct {
	store    EnqueuerStore
	cache    cache.Cache
	publish  func(kind string, payload any)
	now      func() time.Time
	seen     map[string]time.Time
	cooldown map[string]cooldownEntry
	hourKey  string
	hourMem  int
	fullLog  time.Time
}

// StartEnqueuer — lider kilitli üretici döngüsü (bloklar; `go` ile çağır).
func StartEnqueuer(ctx context.Context, store EnqueuerStore, c cache.Cache, lock cache.Lock, publish func(kind string, payload any)) {
	leader := cache.NewLeaderHolder(lock, enqueueLockKey, cache.LeaderTTL(enqueueTick))
	e := &enqueuer{store: store, cache: c, publish: publish, now: time.Now,
		seen: map[string]time.Time{}, cooldown: map[string]cooldownEntry{}}
	leader.SetOnAcquire(func() { e.tick(ctx) })
	leader.Start(ctx)
	t := time.NewTicker(enqueueTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if leader.IsLeader() {
				e.tick(ctx)
			}
		}
	}
}

// pickCandidates — SAF: otomatik araştırma adayları. Açık/onaylı, servis
// öznesi dolu (kind=service), başlangıcı ≤ 15 dk, öncelik cfg listesinde;
// exception fırtınası, self-* ve incident:* hariç (servis taraması onlara bir
// şey söylemez). Sıra: P1 önce, sonra en yeni.
func pickCandidates(open []chstore.Problem, now time.Time, cfg Settings) []chstore.Problem {
	prio := map[string]bool{}
	for _, p := range cfg.Priorities() {
		prio[p] = true
	}
	var out []chstore.Problem
	for _, p := range open {
		if p.Status != "open" && p.Status != "acknowledged" {
			continue
		}
		if p.Service == "" || chstore.ProblemSubjectKind(p.Kind) != chstore.ProblemKindService {
			continue
		}
		if p.RuleID == "exception-storm" || strings.HasPrefix(p.RuleID, "self-") || strings.HasPrefix(p.RuleID, "incident:") {
			continue
		}
		age := now.Sub(time.Unix(0, p.StartedAt))
		if age > candidateMaxAge || age < -time.Minute {
			continue
		}
		if !prio[p.Priority] {
			continue
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Priority == "P1") != (out[j].Priority == "P1") {
			return out[i].Priority == "P1"
		}
		return out[i].StartedAt > out[j].StartedAt
	})
	return out
}

func (e *enqueuer) tick(ctx context.Context) {
	cfg := CurrentSettings()
	if !cfg.IsEnabled() || len(cfg.Priorities()) == 0 {
		return
	}
	snap, err := e.store.OpenProblemsSnapshot(ctx)
	if err != nil {
		log.Printf("[sre-enqueue] açık problemler okunamadı: %v", err)
		return
	}
	all := snap.All()
	open := make([]chstore.Problem, 0, len(all))
	for _, p := range all {
		open = append(open, *p) // kopya: snapshot paylaşımlı
	}
	open = chstore.EnrichProblemsWithPriority(open)
	now := e.now()
	for id, at := range e.seen {
		if now.Sub(at) > time.Hour {
			delete(e.seen, id)
		}
	}
	created := 0
	for _, p := range pickCandidates(open, now, cfg) {
		if created >= cfg.MaxPerTick {
			break
		}
		if _, ok := e.seen[p.ID]; ok {
			continue
		}
		if e.hourCount(ctx, now) >= cfg.MaxPerHour {
			break
		}
		if _, exists, err := Load(ctx, e.store, p.ID); err != nil {
			continue
		} else if exists {
			e.seen[p.ID] = now
			continue
		}
		shared := e.cooldownOwner(ctx, p.Service, p.ID, now, cfg)
		res, err := Enqueue(ctx, e.store, EnqueueRequest{
			ProblemID: p.ID, DisplayID: p.DisplayID, Service: p.Service, Kind: KindAuto,
			Priority: p.Priority, ProblemStartedAt: p.StartedAt, SharedFrom: shared, Now: now,
		}, cfg)
		if errors.Is(err, ErrQueueFull) {
			if now.Sub(e.fullLog) > 10*time.Minute {
				e.fullLog = now
				log.Printf("[sre-enqueue] kuyruk dolu (%d) — sre rolü koşuyor mu? (GET /api/sre/status)", cfg.MaxQueue)
			}
			break
		}
		if err != nil {
			log.Printf("[sre-enqueue] %s: %v", p.ID, err)
			continue
		}
		e.seen[p.ID] = now
		if res.Created && e.publish != nil {
			e.publish(EventKind, map[string]any{"problemId": p.ID, "state": res.State})
		}
		if res.Created && res.State == StateQueued {
			created++
			e.incHour(ctx, now)
		}
	}
}

func hourKeyOf(now time.Time) string { return hourKeyPrefix + now.UTC().Format("2006010215") }

// hourCount — bu saatin sayacı: bellek ile Redis'in büyüğü (lider değişimi
// sayacı sıfırlamasın; Redis'siz kurulumda bellek tek kaynak).
func (e *enqueuer) hourCount(ctx context.Context, now time.Time) int {
	k := hourKeyOf(now)
	if k != e.hourKey {
		e.hourKey, e.hourMem = k, 0
	}
	n := e.hourMem
	if e.cache != nil {
		if raw, ok, err := e.cache.Get(ctx, k); err == nil && ok {
			if v, err := strconv.Atoi(string(raw)); err == nil && v > n {
				n = v
			}
		}
	}
	return n
}

// incHour — v0.10.2227 (Redis R1): atomik Incr (önceki Get+Set pod'lar
// arası ±1 yarışı yok); bellek Redis'in büyüğüne çekilir.
func (e *enqueuer) incHour(ctx context.Context, now time.Time) {
	k := hourKeyOf(now)
	if k != e.hourKey {
		e.hourKey, e.hourMem = k, 0
	}
	e.hourMem++
	if e.cache != nil {
		if v, err := e.cache.Incr(ctx, k, 1, 2*time.Hour); err == nil && int(v) > e.hourMem {
			e.hourMem = int(v)
		}
	}
}

// cooldownOwner — servisin soğuma sahibi başka bir problemse onun kimliği
// (bu problem "shared" olur); değilse "" ve bu problem sahip olur.
func (e *enqueuer) cooldownOwner(ctx context.Context, service, problemID string, now time.Time, cfg Settings) string {
	ttl := time.Duration(cfg.ServiceCooldownMin) * time.Minute
	if m, ok := e.cooldown[service]; ok && now.Before(m.until) && m.problemID != problemID {
		return m.problemID
	}
	if e.cache != nil {
		key := serviceKeyPrefix + service
		won, err := e.cache.SetNX(ctx, key, []byte(problemID), ttl)
		if err == nil && !won {
			if raw, ok, gerr := e.cache.Get(ctx, key); gerr == nil && ok && string(raw) != "" && string(raw) != problemID {
				e.cooldown[service] = cooldownEntry{problemID: string(raw), until: now.Add(ttl)}
				return string(raw)
			}
		}
	}
	e.cooldown[service] = cooldownEntry{problemID: problemID, until: now.Add(ttl)}
	return ""
}
