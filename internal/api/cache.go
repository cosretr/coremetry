package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/selfobs"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Multi-tier caching for the hot-read API endpoints:
//
//   L0 in-flight dedupe (singleflight) — N concurrent callers
//     missing the same cache key collapse to one upstream
//     ClickHouse call; the rest wait for the result. Without
//     this, a cold key on a high-traffic endpoint produces a
//     thundering herd against CH on cache expiry.
//
//   L1 in-process cache — a tiny FIFO map sitting in front
//     of Redis. Catches burst traffic within a single node
//     without crossing the network. Per-entry TTL is short
//     (≤5s) so freshness expectations follow Redis, not the
//     longer in-process window.
//
//   L2 Redis cache (existing) — shared across nodes, primary
//     source of truth for "this query was answered N seconds
//     ago". Stores an envelope { written, body } so the read
//     path can compute age and decide whether to serve fresh,
//     serve stale + async-refresh (SWR), or treat as a hard
//     miss.
//
// SWR (stale-while-revalidate): every cache write stamps a
// timestamp; reads compute age. If age < softTtl → serve and
// log HIT. If softTtl ≤ age < 3*softTtl → serve immediately,
// kick a background refresh (deduped via singleflight). Past
// the 3x window we treat the entry as a hard miss and the
// caller pays the upstream cost. Net effect: most reads
// return in <50ms even when "the cache expired" because the
// stale-but-recent value is good enough for short-TTL
// dashboard queries.

// l1Cache is an in-process FIFO with per-entry TTL. Capped at
// `cap` entries; insertion order drives eviction (not true
// LRU — true LRU would need a linked list, FIFO is good
// enough for the burst-coalescing role).
type l1Cache struct {
	mu      sync.Mutex
	entries map[string]l1Entry
	order   []string
	cap     int
}

type l1Entry struct {
	data    []byte
	expires time.Time
}

func newL1Cache(cap int) *l1Cache {
	return &l1Cache{entries: map[string]l1Entry{}, cap: cap}
}

func (l *l1Cache) get(key string) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.data, true
}

// delPrefix removes every entry whose key starts with prefix.
// O(n) over the L1 map; n is bounded by cap. Used by the
// prefix-style invalidation path for parameter-keyed cache
// namespaces (e.g. "topology-edges:*").
func (l *l1Cache) delPrefix(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	for k := range l.entries {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			delete(l.entries, k)
			removed++
		}
	}
	// Rebuild the order slice in one pass to keep it consistent
	// with the entries map.
	if removed > 0 {
		newOrder := l.order[:0]
		for _, k := range l.order {
			if _, ok := l.entries[k]; ok {
				newOrder = append(newOrder, k)
			}
		}
		l.order = newOrder
	}
	return removed
}

// del removes a key from the L1 map. Used by the cross-pod
// invalidation flow (v0.5.337): when a peer pod mutates a
// cached resource it publishes the key; every pod's subscribe
// loop calls del() so stale L1 entries vanish within ~50ms
// instead of waiting out the soft TTL.
func (l *l1Cache) del(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.entries[key]; !ok {
		return
	}
	delete(l.entries, key)
	// Remove from order slice. O(n) but n is bounded by cap
	// and invalidation is rare relative to set/get.
	for i, k := range l.order {
		if k == key {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
}

func (l *l1Cache) set(key string, data []byte, ttl time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.entries[key]; !exists {
		l.order = append(l.order, key)
	}
	l.entries[key] = l1Entry{data: data, expires: time.Now().Add(ttl)}
	// FIFO eviction. Re-inserting an existing key doesn't bump
	// it forward — keeps the cap predictable under churn.
	for len(l.entries) > l.cap && len(l.order) > 0 {
		head := l.order[0]
		l.order = l.order[1:]
		delete(l.entries, head)
	}
}

// Envelope (JSON v1 / binary v2, R6) → cache_envelope.go.

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// l1TTL bounds how long an entry stays in the in-process layer.
// Capped at 5s so a deploying replica doesn't serve a value
// that's behind every other node's view. Shorter than the
// minimum Redis cache TTL we use elsewhere (15s for the
// hottest endpoints).
const l1TTL = 5 * time.Second

// staleFactor caps how long past the soft TTL we'll still
// serve a stale value with an async refresh. 3x = e.g. a 30s
// TTL stays usable for 90s. Past that we fall back to a hard
// miss because the data is too out of date to call "fresh
// enough".
const staleFactor = 3

// serveCached is the read-through cache wrapper. Reads check
// L1 → L2 (Redis with SWR) → upstream fn (with singleflight
// dedupe). Writes populate both tiers. Writers also stamp the
// X-Cache response header so the operator can see what tier
// served the request from the browser network panel.
//
// `refresh=1` in the query string forces a recompute (e.g.
// when the operator just changed a setting and wants the
// dashboard to reflect it). The fresh result is still written
// so subsequent callers benefit.
//
// Failure modes:
//   - L1 corrupt entry: caller never set it, can't happen
//   - L2 Redis down: Get/Set return errors, we log + fall
//     through to the live path (same as pre-tiered behaviour)
//   - fn() error: surface to caller, do not poison the cache
//
// fn receives the context to query under (v0.8.319): the foreground
// miss path passes the request context; the SWR background refresh
// passes its own detached 20s context. Closures must use that ctx —
// closing over r.Context() was the bug: by the time the background
// refresh ran, the request had returned and its context was
// cancelled, so EVERY refresh died with context.Canceled and stale
// entries only advanced on a full miss at ≥3×TTL (a "30s-fresh"
// surface frozen for 90s).
func (s *Server) serveCached(w http.ResponseWriter, r *http.Request, key string, ttl time.Duration, fn func(ctx context.Context) (any, error)) {
	// v0.10.254 (perf §7 madde 8) — her CH sorgusuna log_comment =
	// route:<pattern>. fn sarmalanır ki SWR arka-plan tazelemesi (kendi
	// Background ctx'i) de etiketi taşısın.
	start := time.Now() // v0.10.1817 — Server-Timing total (yalnız kaydedicili uçlarda basılır)
	tag := chstore.RouteQueryTag(r.Pattern, r.URL.Path)
	tagged := func(ctx context.Context) (any, error) { return fn(chstore.WithQueryTag(ctx, tag)) }
	body, tier, err := s.cachedJSON(r.Context(), key, ttl, r.URL.Query().Get("refresh") == "1", tagged)
	if err != nil {
		setServerTiming(w, r, start, "")
		if writeBadQueryErr(w, r, err) { // v0.10.1818 — log sorgusu sözdizimi reddi 400 + konum
			return
		}
		if writePromQLQueryErr(w, err) { // v0.10.1841 — PromQL/MetricsQL hatası + konum (promql_error_pos.go)
			return
		}
		writeErr(w, err)
		return
	}
	setServerTiming(w, r, start, tier)
	writeCacheHit(w, tier, body)
}

// cachedJSON — serveCached'in HTTP'siz çekirdeği (v0.10.146, perf P5).
//
// Neden ayrı: bir HTTP yanıtı olmayan tüketiciler de aynı katmanı
// istiyor — /api/dashboards/data bundle'ı her panelini kendi anahtarıyla
// önbelleklerken tek bir gövde yazar. Çekirdeği kopyalamak yerine
// serveCached bunun üstüne ince bir sarmalayıcı oldu: iki yolun
// davranışı (L1 → L2 taze/bayat-SWR/eski-zarf → singleflight'lı
// miss → L1 senkron + L2 ayrık yazım) TEK yerde yaşar; bundle
// bir kez daha kardeşinden ayrışamaz (v0.9.566 sınıfı).
//
// Dönüş: JSON gövdesi + X-Cache katmanı (HIT-L1 / HIT / STALE /
// HIT-LEGACY / MISS / BYPASS). `bypass` = ?refresh=1 semantiği: okuma
// katmanları atlanır, sonuç yine yazılır.
func (s *Server) cachedJSON(ctx context.Context, key string, ttl time.Duration, bypass bool, fn func(ctx context.Context) (any, error)) ([]byte, string, error) {
	// gen — v0.10.2229 (R3): hesaplamadan ÖNCE okunan nesil; yazılan zarf
	// bunu taşır, araya giren bir geçersizleştirme sonucu bayat bırakır.
	gen := ""
	if bypass {
		gen = s.cacheGenOf(ctx, key)
	} else {
		// ── L1 ────────────────────────────────────────────────
		if data, ok := s.l1.get(key); ok {
			s.stats.record("HIT-L1", key)
			return data, "HIT-L1", nil
		}
		// ── L2 with SWR ───────────────────────────────────────
		// v0.10.2226: devre kesicili (G8); R3: + nesil; R4: + olumsuz
		// girdi — hepsi tek MGET.
		lk := s.l2Lookup(ctx, key, false)
		gen = lk.gen
		if lk.ok {
			raw := lk.raw
			written, body, envGen, envOK := unwrapEnvelopeGen(raw)
			switch {
			case gen != "" && (!envOK || !genMatches(gen, envGen)):
				// v0.10.2229 (R3) — eski nesil (ya da nesilsiz eski zarf):
				// geçersizleştirilmiş, miss'e düş.
			case envOK:
				age := time.Since(written)
				if age < ttl {
					// Fresh hit. Populate L1 with the remaining
					// freshness window (capped) so future
					// burst reads on this node skip Redis too.
					s.l1.set(key, body, minDur(ttl-age, l1TTL))
					s.stats.record("HIT", key)
					return body, "HIT", nil
				}
				if age < ttl*staleFactor {
					// Stale-but-usable. Serve immediately,
					// kick a background refresh (deduped via
					// singleflight so concurrent stale hits
					// share one upstream call).
					if lk.neg == nil { // R4: zaman aşımı yeni düştüyse tazeleme bekler
						go s.refreshKey(key, ttl, fn, gen)
					}
					s.stats.record("STALE", key)
					return body, "STALE", nil
				}
				// Past hard window → fall through to miss.
			default:
				// R6: the "|b2" key only ever holds our binary
				// envelope — an undecodable value is a miss (pre-R6
				// this served the raw bytes as HIT-LEGACY).
			}
		}

		// v0.10.2230 (R4, O4) — bu nesilde yakın zamanda zaman aşımına
		// düşmüş: CH'yi yeniden yormadan aynı hata, < 10 ms.
		if lk.neg != nil {
			s.stats.record("NEG", key)
			return nil, "", lk.neg
		}
	}

	// ── Miss path with singleflight dedupe ────────────────────
	mode := dsfWait // R4: soğuk anahtar replikalar arası tek hesap
	if bypass {
		mode = dsfOff
	}
	body, noStore, peer, err := s.computeBodyStore(ctx, key, gen, mode, ttl, fn)
	if err != nil {
		return nil, "", err
	}
	if peer {
		// R4 — başka replikanın az önce yazdığı zarf: L2'ye yeniden yazılmaz.
		s.l1.set(key, body, minDur(ttl, l1TTL))
		s.stats.record("HIT-PEER", key)
		return body, "HIT-PEER", nil
	}
	if noStore {
		// v0.10.1119 — fn gövdeyi uncacheable() ile işaretledi (ör. kök-neden
		// demeti bubbleUp yuvası alamadı): istemciye gider, hiçbir katmana
		// yazılmaz — kısmi sonuç 60 sn taze diye saklanmaz.
		s.stats.record("MISS-NOSTORE", key)
		return body, "MISS-NOSTORE", nil
	}
	// v0.8.350 (HA 🟡5) — the response must not wait on Redis. L1 is
	// set synchronously (in-process, same-node burst coalescing needs
	// it before this handler returns); the L2 (Redis) write detaches
	// onto its own goroutine + 2s context. Pre-v0.8.350 this was a
	// synchronous storeCached: with a blackholed Redis (node lost
	// without an RST) every MISS paid the full client dial/retry stall
	// a second time AFTER the upstream query had already succeeded.
	// The SWR refreshKey path keeps its synchronous storeCached — it
	// already runs on a detached background goroutine.
	s.l1.set(key, body, s.l2().l1Cap(ttl)) // devre açıkken TTL boyunca (G8)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.storeL2(ctx, key, body, ttl, gen)
	}()
	tier := "MISS"
	if bypass {
		tier = "BYPASS"
	}
	s.stats.record(tier, key)
	return body, tier, nil
}

// writeCacheHit emits the standard headers + body for a tier
// hit. Pulled into a helper so the four hit paths
// (HIT-L1 / HIT / STALE / HIT-LEGACY) stay consistent.
func writeCacheHit(w http.ResponseWriter, tier string, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Cache", tier)
	w.Write(body)
}

// storeCached writes the envelope to Redis and the bare body
// to L1. Redis TTL is staleFactor × softTtl so the SWR window
// has room to breathe past nominal expiry. Errors are logged
// but never fatal. Callers on a request-serving path must NOT
// call this synchronously — see the serveCached miss path
// (v0.8.350), which sets L1 inline and detaches the L2 write.
// The remaining synchronous callers (refreshKey, the warmer)
// already run on background goroutines.
func (s *Server) storeCached(ctx context.Context, key string, body []byte, ttl time.Duration, gen string) {
	s.storeL2(ctx, key, body, ttl, gen)
	s.l1.set(key, body, s.l2().l1Cap(ttl))
}

// storeL2 is the Redis half of storeCached (split out in v0.8.350 so
// the miss path can run it async while keeping the L1 set synchronous).
func (s *Server) storeL2(ctx context.Context, key string, body []byte, ttl time.Duration, gen string) {
	if gen == cacheGenUnknown {
		return // R3: nesil okunamadı — hiçbir okumayla eşleşmeyecek girdi yazılmaz
	}
	env, err := wrapEnvelope(body, gen)
	if !s.l2().allow() {
		return
	}
	// v0.10.2230 (R4): gövde + olumsuz girdiyi ve claim'i silme tek turda.
	// R6: tavanı aşan gövde L2'ye yazılmaz (L1'de kalır); claim/olumsuz
	// girdi yine silinir, bekleyen replikalar beklemeden kendisi hesaplar.
	valOp := cache.PipeOp{Key: l2ValueKey(key), Value: env, TTL: ttl * staleFactor}
	if err != nil {
		s.stats.oversize(key, len(body))
		valOp = cache.PipeOp{Key: l2ValueKey(key), Del: true}
	}
	t0 := time.Now()
	err = s.cache.Pipeline(ctx, []cache.PipeOp{
		valOp,
		{Key: cacheNegKeyPrefix + key, Del: true},
		{Key: cacheClaimKeyPrefix + key, Del: true},
	})
	selfobs.ObserveCacheL2("set", time.Since(t0), err)
	s.l2().done(ctx, err)
	if err != nil {
		log.Printf("[cache] set %s: %v", key, err)
	}
}

// refreshKey is the background half of SWR. Runs the upstream
// fn under a fresh context (the request that triggered the
// refresh has already returned), updates both cache tiers.
// Deduped via singleflight under the cache key so concurrent
// stale-hits don't fan out into N parallel CH queries.
func (s *Server) refreshKey(key string, ttl time.Duration, fn func(ctx context.Context) (any, error), gen string) {
	// Defensive timeout — same as the warmer's queryBudg.
	// A refresh that hangs longer than this would block
	// the singleflight slot for new concurrent refreshes
	// in the same window.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// v0.8.319 — the fresh context now actually reaches the
	// upstream query. fn() used to close over the (already
	// cancelled) request context, so every background refresh
	// aborted with context.Canceled.
	body, noStore, _, err := s.computeBodyStore(ctx, key, gen, dsfSkip, ttl, fn)
	if errors.Is(err, errDSFSkipped) {
		return // R4: tazelemeyi başka replika yapıyor
	}
	if err != nil {
		log.Printf("[cache] refresh %s: %v", key, err)
		return
	}
	if noStore {
		// v0.10.1119 — kısmi (uncacheable) sonuç bayat girdiyi EZMEZ.
		return
	}
	s.storeCached(ctx, key, body, ttl, gen)
}

// computeBodyStore — singleflight altındaki TEK hesaplama noktası (v0.10.146):
// fn → NaN/Inf temizliği → json.Marshal, hepsi sf.Do'nun İÇİNDE; slot'u
// paylaşan her bekleyen aynı DEĞİŞMEZ bayt dilimini alır.
//
// İki HEAD hatası burada kapanır (inceleme, v0.10.146):
//   - bekleyenler ortak `v`yi kendi başlarına sanitizeFloats'lıyor ve
//     marshal'lıyordu — reflect ile yazılan float'lar üstünde veri
//     yarışı (bundle'da aynı anahtarı paylaşan iki panel bunu tek
//     istekte tetikliyordu);
//   - refreshKey aynı sf anahtarında (nil, nil) dönüyordu: SWR
//     tazelemesi sürerken sert pencereyi geçip miss'e düşen bir ön plan
//     çağrısı slot'a katılıp `null` gövdesini servis edip cache'liyordu.
//
// Şimdi katılan kim olursa olsun bayt alır.
//
// v0.5.303 — scrub NaN/Inf floats anywhere in the result tree before
// json.Marshal. Defence-in-depth for the "encoding/json: unsupported
// value NaN" 500s; complements the per-Scan safeF guards from v0.5.301.
//
// v0.10.1119 — computeBodyStore adını aldı: ek dönüş değeri fn'in
// uncacheable() işareti (aşağıda).
// noStoreResult — v0.10.1119: fn'in "bu gövdeyi önbelleğe YAZMA" işareti
// (uncacheable). Gövde istemciye ve singleflight bekleyenlerine gider; L1/L2'ye
// yazılmaz, SWR tazelemesi bayat girdiyi bununla ezmez. Kullanım: kök-neden
// demeti bubbleUp yuvası alamadığında kısmi sonuç (rootcause_bubble_gate.go).
type noStoreResult struct{ v any }

// uncacheable — fn dönüşünü önbelleğe yazılmayacak diye işaretler.
func uncacheable(v any) any { return noStoreResult{v: v} }

// computedBody — singleflight yuvasının paylaşılan değeri.
type computedBody struct {
	body    []byte
	noStore bool
	peer    bool // R4: gövde başka replikanın L2 yazımından
}

// computeBodyStore — gövde + uncacheable işareti (v0.10.1119).
//
// gen (v0.10.2229, R3) singleflight anahtarına girer: geçersizleştirmeden
// sonra gelen çağıran, öncesinde başlamış bir hesaplamaya katılıp eski
// gövdeyi yeni nesille yazamaz.
//
// mode/ttl (v0.10.2230, R4): süreç içi slot'un İÇİNDE dağıtık claim
// (cache_dsf.go); zaman aşımı sınıfı hata olumsuz önbelleğe yazılır.
func (s *Server) computeBodyStore(ctx context.Context, key, gen string, mode dsfMode, ttl time.Duration, fn func(ctx context.Context) (any, error)) ([]byte, bool, bool, error) {
	sfKey := key
	if gen != "" {
		sfKey = key + "\x00g" + gen
	}
	v, err, _ := s.sf.Do(sfKey, func() (any, error) {
		claimed := false
		if mode != dsfOff && gen != cacheGenUnknown {
			claimed = s.dsfClaim(ctx, key)
			if !claimed {
				if mode == dsfSkip {
					return nil, errDSFSkipped
				}
				if body, err, done := s.dsfAwait(ctx, key, gen, ttl); done {
					if err != nil {
						return nil, err
					}
					return computedBody{body: body, peer: true}, nil
				}
			}
		}
		val, err := fn(ctx)
		if err != nil {
			s.storeNeg(key, gen, ttl, err)
			if claimed {
				s.dsfRelease(key)
			}
			return nil, err
		}
		noStore := false
		if ns, ok := val.(noStoreResult); ok {
			val, noStore = ns.v, true
		}
		sanitizeFloats(val)
		b, err := json.Marshal(val)
		if err != nil {
			return nil, err
		}
		if noStore && claimed {
			s.dsfRelease(key)
		}
		return computedBody{body: b, noStore: noStore}, nil
	})
	if err != nil {
		return nil, false, false, err
	}
	cb, _ := v.(computedBody)
	return cb.body, cb.noStore, cb.peer, nil
}

// invalidateCacheChannel is the Redis pub/sub channel that
// carries L1 invalidation hints between pods. One channel for
// all keys — the payload IS the cache key. Keep the name in
// sync with the subscribe loop in Server.startCacheInvalidation.
const invalidateCacheChannel = "coremetry:cache:invalidate"

// cacheInvalidate evicts a cache key from every tier across
// every replica. Call from mutating endpoints right after the
// write commits.
//
// Order matters:
//  1. L2 (Redis) DEL — the canonical cache. Removed first so
//     peers reading mid-PUBLISH don't repopulate L1 from a
//     stale L2 entry.
//  2. L1 (local) DEL — own pod's in-memory tier. Avoids the
//     race where the publisher's own subscribe loop is slow
//     to receive its own message.
//  3. PUBLISH — broadcast to peers. Each pod's subscribe loop
//     calls l1.del on receipt.
//
// Errors are logged but never bubbled. Invalidation is a hint
// (the soft TTL is the safety net); a failed publish at most
// extends the staleness window by a few seconds.
func (s *Server) cacheInvalidate(ctx context.Context, key string) {
	// R6: binary "|b2" value + the pre-R6 bare key (old pods mid-rollout).
	if err := s.cache.Pipeline(ctx, []cache.PipeOp{{Key: l2ValueKey(key), Del: true}, {Key: key, Del: true}}); err != nil {
		log.Printf("[cache] invalidate L2 del %s: %v", key, err)
	}
	s.l1.del(key)
	if err := s.cache.Publish(ctx, invalidateCacheChannel, []byte(key)); err != nil {
		log.Printf("[cache] invalidate publish %s: %v", key, err)
	}
}

// cacheInvalidatePrefix evicts every cached entry whose key
// starts with prefix — across L1 (local + peers via pub/sub)
// and L2 (v0.10.2229, R3: one INCR on the prefix's generation
// counter, see cache_gen.go — no SCAN). Use for parameter-keyed cache
// namespaces where a single mutation affects many keys (e.g.
// "topology-edges:*" — one mute change invalidates every
// time-window-keyed topology view).
//
// Wire format on the pub/sub channel: "prefix:<P>". The
// receiver looks for the "prefix:" marker and routes to
// delPrefix rather than del. Exact-key payloads stay
// unprefixed for compatibility.
func (s *Server) cacheInvalidatePrefix(ctx context.Context, prefix string) {
	// L2 — v0.10.2229 (Redis R3, G2): nesil sayacına tek INCR; eski
	// nesil girdileri okumada miss, Redis TTL'iyle düşer. v0.6.11'den beri
	// burada istek yolunda SCAN + UNLINK (DelPrefix) vardı. Kayıt dışı önek
	// (olmamalı — cache_gen_test.go) DelPrefix'e istek yolu DIŞINDA düşer.
	//
	// Best-effort: an error here logs and continues so the L1
	// eviction + cross-pod broadcast still happen.
	if isCacheGenPrefix(prefix) {
		if err := s.bumpCacheGen(ctx, prefix); err != nil {
			log.Printf("[cache] invalidate L2 gen %s: %v", prefix, err)
		}
	} else {
		s.delPrefixOffPath(prefix)
	}
	// Local L1.
	s.l1.delPrefix(prefix)
	// Broadcast.
	payload := "prefix:" + prefix
	if err := s.cache.Publish(ctx, invalidateCacheChannel, []byte(payload)); err != nil {
		log.Printf("[cache] invalidate-prefix publish %s: %v", prefix, err)
	}
}

// publishConfigReload broadcasts a "config:<svc>" message on
// the invalidate channel. The subscriber loop dispatches to
// the named service's LoadPersisted. Called from settings
// PUT handlers right after the write commits so peer pods
// converge on the new config in <50ms instead of waiting out
// the per-service 30s StartConfigRefresh tick.
func (s *Server) publishConfigReload(ctx context.Context, svc string) {
	payload := "config:" + svc
	if err := s.cache.Publish(ctx, invalidateCacheChannel, []byte(payload)); err != nil {
		log.Printf("[cache] config-reload publish %s: %v", svc, err)
	}
}

// reloadConfigOnSignal dispatches a peer's config:<svc>
// invalidation message to the matching in-memory service's
// LoadPersisted call. Each call carries its own short
// timeout — a CH stall on the read side shouldn't backlog
// the subscriber goroutine.
func (s *Server) reloadConfigOnSignal(ctx context.Context, svc string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch svc {
	case "trace-facets": // v0.10.302
		if s.store != nil {
			if err := s.store.LoadTraceFacets(ctx); err != nil {
				log.Printf("[chstore] trace facets reload: %v", err)
			}
		}
	case "ai", "copilot":
		if s.copilot != nil {
			if err := s.copilot.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload copilot: %v", err)
			}
		}
	// v0.10.1067 — OIDC/SSO ayarı (auth_oidc_settings.go PUT). Case uçla AYNI
	// sürümde (thanos v0.9.237 dersi): peer pod'un giriş düğmesi ve IdP'si
	// 30 s poll'ü beklemeden yeni yapılandırmaya geçer. LoadPersisted değişim
	// tespitli — blob aynıysa keşif koşmaz; gerekirse keşif bu 5 s'lik
	// bağlamdan AYRILMIŞ kendi ≤10 s süresiyle koşar (auth/oidc_settings.go apply).
	case "oidc":
		if s.oidc != nil {
			if err := s.oidc.LoadPersisted(ctx); err != nil {
				log.Printf("[cache] config-reload oidc: %v", err)
			}
		}
	// v0.10.1122 — wiki bilgisi ayarı (wiki.go PUT).
	case "wiki":
		if w := wikiKB(); w != nil && s.store != nil {
			if err := w.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload wiki: %v", err)
			}
		}
	case "ldap":
		if s.ldap != nil {
			if err := s.ldap.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload ldap: %v", err)
			}
		}
	case "tempo":
		if s.tempo != nil {
			if err := s.tempo.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload tempo: %v", err)
			}
		}
	case "pipeline":
		if s.pipeline != nil {
			if err := s.pipeline.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload pipeline: %v", err)
			}
		}
	case "logstore":
		if s.logsMgr != nil {
			if err := s.logsMgr.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload logstore: %v", err)
			}
		}
	// v0.9.237 — putThanosSettings has published "thanos" since it shipped,
	// but there was no case for it: the signal fell into default: and was
	// dropped, so peer pods converged on their 30s poll instead of the
	// sub-50ms the publish was written for. A publish with no listener reads
	// as working right up until someone measures it.
	case "thanos":
		if s.thanos != nil {
			if err := s.thanos.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload thanos: %v", err)
			}
		}
	case "logs_search":
		// v0.10.1831 — /logs örnek boyu üst sınırı (logs_search_settings.go PUT).
		s.LoadLogsSearchSettings(ctx)
	case "promql_console":
		// v0.10.952 — PromQL konsolu korkulukları (promql_console_settings.go
		// PUT). Case uçla AYNI sürümde (thanos v0.9.237 dersi): dinleyicisiz
		// publish, peer pod'ların 30 s boyunca eski timeout/limitlerle
		// konsol sorgusu koşması demek olurdu. Store nil kontrolü Load'da.
		s.LoadPromQLConsoleSettings(ctx)
	case "entities":
		// v0.10.129 — entity katmanı bayrağı/vidaları (entity_routes.go PUT).
		if s.entitySettings != nil {
			if err := s.entitySettings.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[entity] reload on signal: %v", err)
			}
		}
	case "rollouts":
		// v0.10.200 — rollouts bayrağı/vidaları (rollouts.go PUT): api pod'ları
		// 404 kapısını, worker reconciler'ı 30 s poll'süz görsün.
		if s.rolloutCfg != nil {
			if err := s.rolloutCfg.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[rollout] reload on signal: %v", err)
			}
		}
	case "argocd":
		// v0.10.957 — Argo CD ayar blobu (argocd_settings_routes.go PUT).
		// Case uçla AYNI sürümde (thanos v0.9.237 dersi). v0.10.983: worker
		// pod'undaki argocd-metrics işçisi aynı servisi okur — enabled bayrağı
		// ve aralıklar peer'da 30 s poll'ü beklemeden uygulanır. Nil-güvenli.
		s.reloadArgoCDSettings(ctx)
	case "rag":
		if s.rag != nil {
			if err := s.rag.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload rag: %v", err)
			}
		}
	// v0.9.1150 — VictoriaMetrics okuma backend'i. Case uçla AYNI
	// sürümde: bu ayarın gecikmesi ötekilerden daha pahalı, çünkü
	// hangi STORE'un cevap verdiğini belirliyor — dinleyicisiz bir
	// publish, peer pod'ların 30 saniye boyunca farklı bir backend'den
	// cevap vermesi demek olurdu.
	case "victoria-metrics":
		if s.vmetrics != nil {
			if err := s.vmetrics.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload victoria-metrics: %v", err)
			}
		}
	// v0.10.580 — Oracle kaynak listesi. Case UÇLA AYNI SÜRÜMDE (thanos
	// v0.9.237 dersi: dinleyicisiz publish peer pod'ları 30 s poll'a
	// bırakır). config_reload_test.go bu eşleşmeyi zaten kapıyor.
	case "oracle":
		if s.oracle != nil {
			if err := s.oracle.LoadPersisted(ctx, s.oracleStore()); err != nil {
				log.Printf("[cache] config-reload oracle: %v", err)
			}
		}
	// v0.9.829 — Azure DevOps / TFS bağlantısı. Case'i uçla AYNI
	// sürümde ekliyoruz: v0.9.237'de thanos'un publish'i dinleyicisiz
	// kaldığı için peer pod'lar 30s poll'u beklemişti.
	case "devops":
		if s.devops != nil {
			if err := s.devops.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload devops: %v", err)
			}
		}
	// v0.10.87 — dış MCP sunucu listesi. Case uçla AYNI sürümde
	// (thanos v0.9.237 dersi: dinleyicisiz publish peer pod'ları 30s
	// poll'a bırakır — burada bedeli modelin ESKİ dış tool listesiyle
	// konuşması olurdu).
	case "mcpclient":
		if s.mcpClient != nil {
			if err := s.mcpClient.LoadPersisted(ctx, s.store); err != nil {
				log.Printf("[cache] config-reload mcpclient: %v", err)
			}
		}
	// v0.9.233 — custom roles had no reload case, and the gap failed OPEN.
	// userPayload only emits customRolePages when CustomRolePages(name)
	// returns non-nil; a peer pod that hasn't polled yet returns nil, the
	// field is omitted, and the frontend reads an absent list as "no
	// restriction" (AppShell.tsx: `if (!allowed) return`). So for up to the
	// 30s role poll — stacked on the 30s /api/auth/me cache, which is
	// pod-local — a restricted viewer on another pod saw the FULL sidebar.
	// meUsers must be cleared too, or the reloaded pages sit behind a
	// cached payload that still omits them.
	case "custom_roles":
		if err := s.auth.LoadPersistedCustomRoles(ctx, s.store); err != nil {
			log.Printf("[cache] config-reload custom_roles: %v", err)
		}
		s.meUsers.clear()
	default:
		// Unknown service — silently ignore so a forward-compat
		// peer publishing a config key the older pod doesn't
		// recognise doesn't spam the log.
	}
}

// StartCacheInvalidation subscribes to the invalidation
// channel and drains incoming messages into l1.del. Runs once
// per Server; the subscription lifetime is bound to the
// server's lifetime context. When Subscribe returns an error
// (Redis down, or pub/sub unsupported), we log and exit — the
// soft TTL keeps the L1 tier from growing stale unbounded,
// just for longer.
//
// Called from main.go alongside the other StartConfigRefresh
// loops, exported because the constructor doesn't take a ctx.
func (s *Server) StartCacheInvalidation(ctx context.Context) {
	ch, err := s.cache.Subscribe(ctx, invalidateCacheChannel)
	if err != nil {
		log.Printf("[cache] invalidate subscribe disabled: %v", err)
		return
	}
	go func() {
		log.Printf("[cache] invalidate subscriber online on %q", invalidateCacheChannel)
		for {
			select {
			case <-ctx.Done():
				return
			case payload, ok := <-ch:
				if !ok {
					log.Printf("[cache] invalidate subscriber channel closed")
					return
				}
				key := string(payload)
				if key == "" {
					continue
				}
				if len(key) > 7 && key[:7] == "prefix:" {
					p := key[7:]
					s.l1.delPrefix(p)
					s.stats.record("INVALIDATED-PFX", p)
					continue
				}
				// v0.5.363 — settings PUT on pod A publishes
				// "config:<svc>" so every peer pod hot-reloads
				// the matching in-memory service config from
				// system_settings. Closes the 30s window the
				// per-service StartConfigRefresh poll left open.
				if len(key) > 7 && key[:7] == "config:" {
					svc := key[7:]
					s.reloadConfigOnSignal(context.Background(), svc)
					s.stats.record("CONFIG-RELOAD", svc)
					continue
				}
				s.l1.del(key)
				s.dropL2ValueOnPeerInvalidate(key)
				s.stats.record("INVALIDATED", key)
			}
		}
	}()
}

// cacheStats records per-tier hit counts and the hottest keys
// since process start. Surfaces on /api/admin/cache-stats so
// the System page can show whether the multi-tier cache is
// actually doing useful work in production.
//
// Memory is bounded: tier × family counters are capped (cacheComboCap,
// then family "other"); keyHits is capped at 4096 keys.
//
// v0.10.2227 (Redis R1, G12): tier sayaçları kilitsiz (sync.Map + atomic);
// global mutex yalnız isabet katmanlarında keyHits için, tavanda doğrusal
// tarama yok (tek kayıt düşer — sıralama yaklaşık, admin tanısı için yeter).
// Her kayıt selfobs cache.requests{family,tier} sayacına da gider.
type cacheStats struct {
	tiers   sync.Map     // cacheTierKey → *atomic.Int64
	combos  atomic.Int32 // tier × family kayıt sayısı
	mu      sync.Mutex
	keyHits map[string]int64
	started time.Time
	// oversizeN — R6 (cache_envelope.go): tavan yüzünden yazılmayan L2 değeri.
	oversizeN atomic.Int64
}

type cacheTierKey struct{ tier, family string }

const (
	cacheStatsKeyCap = 4096
	cacheComboCap    = 2048 // tier × family; aşılınca aile "other"
)

func newCacheStats() *cacheStats {
	return &cacheStats{
		keyHits: map[string]int64{},
		started: time.Now(),
	}
}

// cacheKeyFamily — anahtarın ailesi: ilk ':' öncesi kod sabiti önek
// (problems, svc-bundle, clusters …). Kullanıcı girdisi ölçü etiketine
// sızmasın: yalnız [a-z0-9._-], ≤ 40 karakter; değilse "other".
func cacheKeyFamily(key string) string {
	i := strings.IndexByte(key, ':')
	if i <= 0 || i > 40 {
		return "other"
	}
	for j := 0; j < i; j++ {
		c := key[j]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return "other"
		}
	}
	return key[:i]
}

// record bumps the tier × family counter and (for hit tiers only)
// the per-key counter. Misses don't update keyHits because a
// missing key isn't yet "hot" — it's a cold one.
func (cs *cacheStats) record(tier, key string) {
	fam := cacheKeyFamily(key)
	k := cacheTierKey{tier, fam}
	v, ok := cs.tiers.Load(k)
	if !ok {
		if cs.combos.Load() >= cacheComboCap {
			fam = "other"
			k = cacheTierKey{tier, fam}
		}
		var loaded bool
		v, loaded = cs.tiers.LoadOrStore(k, new(atomic.Int64))
		if !loaded {
			cs.combos.Add(1)
		}
	}
	v.(*atomic.Int64).Add(1)
	selfobs.ObserveCacheTier(fam, tier)
	switch tier {
	case "HIT-L1", "HIT", "STALE":
		cs.mu.Lock()
		if _, seen := cs.keyHits[key]; !seen && len(cs.keyHits) >= cacheStatsKeyCap {
			for drop := range cs.keyHits { // O(1): ilk gelen kayıt düşer
				delete(cs.keyHits, drop)
				break
			}
		}
		cs.keyHits[key]++
		cs.mu.Unlock()
	}
}

// CacheStatsSnapshot is the wire shape for the admin endpoint.
// Counts is a tier → hit count map; TopKeys is a sorted slice
// of the most-frequently-served keys (capped at 20).
type CacheStatsSnapshot struct {
	SinceUnixNano int64            `json:"sinceUnixNano"`
	Counts        map[string]int64 `json:"counts"`
	// Families — v0.10.2227 (R1): anahtar ailesi → katman → sayı; R2'nin
	// önce/sonra ölçüsü (aile başına MISS ÷ (HIT-L1+HIT+STALE+MISS)).
	Families map[string]map[string]int64 `json:"families"`
	TopKeys  []CacheKeyHit               `json:"topKeys"`
	L1Size   int                         `json:"l1Size"`
	L1Cap    int                         `json:"l1Cap"`
	// L2Oversize — R6: L2 tavanını aşıp Redis'e yazılmayan gövde sayısı.
	L2Oversize int64 `json:"l2Oversize"`
}

type CacheKeyHit struct {
	Key  string `json:"key"`
	Hits int64  `json:"hits"`
}

func (cs *cacheStats) snapshot(l1 *l1Cache) CacheStatsSnapshot {
	out := CacheStatsSnapshot{
		SinceUnixNano: cs.started.UnixNano(),
		L2Oversize:    cs.oversizeN.Load(),
		Counts:        map[string]int64{},
		Families:      map[string]map[string]int64{},
	}
	cs.tiers.Range(func(k, v any) bool {
		tk, n := k.(cacheTierKey), v.(*atomic.Int64).Load()
		out.Counts[tk.tier] += n
		if out.Families[tk.family] == nil {
			out.Families[tk.family] = map[string]int64{}
		}
		out.Families[tk.family][tk.tier] += n
		return true
	})
	cs.mu.Lock()
	defer cs.mu.Unlock()
	// Sort keys by hit count desc, take top 20. Cheap on the
	// bounded map; runs on admin requests not hot path.
	keys := make([]CacheKeyHit, 0, len(cs.keyHits))
	for k, v := range cs.keyHits {
		keys = append(keys, CacheKeyHit{Key: k, Hits: v})
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Hits > keys[j].Hits })
	if len(keys) > 20 {
		keys = keys[:20]
	}
	out.TopKeys = keys
	if l1 != nil {
		l1.mu.Lock()
		out.L1Size = len(l1.entries)
		out.L1Cap = l1.cap
		l1.mu.Unlock()
	}
	return out
}

// cachePeek — v0.9.1207 (Faz 6.3). serveCached gövdesine YAN ETKİSİZ
// bakış: L1, sonra L2; miss'te üretim YOK (fn parametresi yok — bu
// bilinçli, insight kartının "LLM ateşlemez" kuralının altyapısı).
// SWR tazelemesi de tetiklenmez: bakış, sahiplik değildir.
func (s *Server) cachePeek(ctx context.Context, key string) ([]byte, bool) {
	if data, ok := s.l1.get(key); ok {
		return data, true
	}
	if raw, gen, ok := s.l2GetGen(ctx, key); ok && len(raw) > 0 {
		_, body, envGen, isEnv := unwrapEnvelopeGen(raw)
		if gen != "" && (!isEnv || !genMatches(gen, envGen)) {
			return nil, false // R3: geçersizleştirilmiş nesil
		}
		if isEnv {
			return body, true
		}
	}
	return nil, false
}
