package chstore

import (
	"context"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// ctlcache.go — kontrol düzlemi FINAL okumalarının nesil sayaçlı önbelleği.
//
// query_log denetimi (lokal CH, 6 gün): saved_views (SRE kuyruğu),
// runbook_executions (agent poll'u), monitors (zamanlayıcı) ve
// system_settings (anahtar başına GetSetting, ör. key='ml') okumaları gün
// başına ~5k kez koşuyor; p50 2–5 ms ama max 2.8–5.8 sn — ~16 satırlık
// okumalar için FINAL birleştirme bekleyişi. Bu tablolar nadiren yazılır.
//
// Model (api/cache_gen.go'daki Redis R3 makinesinin chstore ikizi):
//   - Aile = tablo. Redis'te aile başına nesil sayacı
//     (coremetry:ctl:gen:v1:<tablo>) + süreç içi yerel sayaç.
//   - Okuma: nesil TEK MGET ile okunur (CH'den önce), süreç içi girdi aynı
//     nesli taşıyorsa ve ctlMaxAge'den gençse oradan döner; değilse okuma
//     FINAL ile CH'ye gider (değişmez) ve okunan nesille saklanır. Okuma
//     sırasında gelen bir yazım nesli artırır → saklanan girdi sonraki
//     okumada miss olur.
//   - Yazım: tablonun HER yazım yolu (INSERT ve ALTER … DELETE) başarıdan
//     sonra ctlBump çağırır: yerel sayaç + Redis INCR → yazım bütün pod'larda
//     bir sonraki okumada görünür. ctlRebumpDelay sonra İKİNCİ bir artış:
//     ALTER DELETE mutasyonları asenkron, küme kipinde replika gecikmesi var;
//     bu pencerede okunup yeni nesille saklanmış eski bir sonuç böylece düşer.
//
// Degraded (redis-usage-audit §4, R0 sınıf A — hata = miss):
//   - Redis tanımlı ama şu an Noop (boot'ta erişilemedi) → önbellek YOK, her
//     okuma CH'ye (çok pod'lu kurulumda yerel sayaç başka pod'un yazımını
//     göremez).
//   - MGET/INCR hatası → devre ctlBreakerOpen boyunca açık, okumalar doğrudan
//     CH'ye; Redis'i 500 ms'den (istemci tavanı) fazla beklemeyiz.
//   - Redis hiç tanımlı değil (tek pod) → yalnız yerel sayaç.
//   - ctlMaxAge her durumda tavan: kaçırılmış bir artış (INCR hatası, kod
//     dışı yazım) en çok bu kadar bayat kalır.
//
// Dönen değerler PAYLAŞILIR: çağrı yerleri kopya döndürür (clone), önbellekteki
// değer asla dışarı sızmaz.

const (
	ctlSettings   = "system_settings"
	ctlSavedViews = "saved_views"
	ctlRunbookExe = "runbook_executions"
	ctlMonitors   = "monitors"

	ctlGenKeyPrefix = "coremetry:ctl:gen:v1:"
	ctlGenTTL       = 7 * 24 * time.Hour
	ctlMaxAge       = 60 * time.Second
	ctlBreakerOpen  = 5 * time.Second
	ctlRebumpDelay  = 5 * time.Second
	ctlRedisTimeout = 500 * time.Millisecond
	ctlMaxEntries   = 256
)

type ctlEntry struct {
	gen string
	at  time.Time
	val any
}

type ctlCache struct {
	src             cache.Cache
	redisConfigured bool
	now             func() time.Time
	rebumpDelay     time.Duration

	mu           sync.Mutex
	local        map[string]uint64
	entries      map[string]ctlEntry
	breakerUntil time.Time
	warned       bool
}

func newCtlCache(src cache.Cache, redisConfigured bool) *ctlCache {
	return &ctlCache{
		src: src, redisConfigured: redisConfigured, now: time.Now,
		rebumpDelay: ctlRebumpDelay,
		local:       map[string]uint64{}, entries: map[string]ctlEntry{},
	}
}

// SetControlPlaneCache — main.go: kontrol düzlemi okumalarını nesil sayaçlı
// önbelleğe bağlar. src = Switchable önbellek (reprobe takası görünür);
// redisConfigured = COREMETRY_REDIS_URL verildi mi (verildiyse Noop =
// "Redis düştü", önbellek devre dışı). Çağrılmazsa her okuma CH'ye gider.
func (s *Store) SetControlPlaneCache(src cache.Cache, redisConfigured bool) {
	if src == nil {
		src, _ = cache.NewNoop()
	}
	s.ctl = newCtlCache(src, redisConfigured)
}

// genOf — ailenin şu anki nesli; ok=false → önbellek atlanır (CH'ye git).
func (c *ctlCache) genOf(ctx context.Context, family string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	local := c.local[family]
	open := c.now().Before(c.breakerUntil)
	c.mu.Unlock()
	ls := strconv.FormatUint(local, 10)
	if !c.redisConfigured {
		return "L" + ls, true
	}
	if open || cache.IsNoop(c.src) {
		return "", false
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ctlRedisTimeout)
	defer cancel()
	vals, err := c.src.MGet(rctx, []string{ctlGenKeyPrefix + family})
	if err != nil || len(vals) != 1 {
		c.trip(err)
		return "", false
	}
	r := "0"
	if len(vals[0]) > 0 {
		r = string(vals[0])
	}
	return r + "." + ls, true
}

func (c *ctlCache) trip(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.breakerUntil = c.now().Add(ctlBreakerOpen)
	if !c.warned {
		c.warned = true
		log.Printf("[chstore] kontrol düzlemi önbelleği: Redis nesil okuması/artışı başarısız, %s CH'ye düşülüyor: %v", ctlBreakerOpen, err)
	}
}

func (c *ctlCache) get(family, key, gen string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[family+"\x00"+key]
	if !ok || e.gen != gen || c.now().Sub(e.at) >= ctlMaxAge {
		return nil, false
	}
	return e.val, true
}

func (c *ctlCache) put(family, key, gen string, v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= ctlMaxEntries {
		c.entries = map[string]ctlEntry{}
	}
	c.entries[family+"\x00"+key] = ctlEntry{gen: gen, at: c.now(), val: v}
}

// bump — yazım sonrası: yerel sayaç (bu pod anında görür) + Redis INCR
// (diğer pod'lar). İlk INCR (1 = sayaç yoktu) değeri o anki milisaniyeye
// taşır — bumpCacheGen'le aynı: TTL'le düşüp yeniden kurulan sayaç eski
// bir nesli asla geri getirmez.
func (c *ctlCache) bump(ctx context.Context, family string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.local[family]++
	c.mu.Unlock()
	if !c.redisConfigured || cache.IsNoop(c.src) {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ctlRedisTimeout)
	defer cancel()
	k := ctlGenKeyPrefix + family
	n, err := c.src.Incr(rctx, k, 1, ctlGenTTL)
	if err == nil && n == 1 {
		_, err = c.src.Incr(rctx, k, time.Now().UnixMilli(), 0)
	}
	if err != nil {
		c.trip(err)
	}
}

// ctlBump — tablonun yazım yolları çağırır (başarılı yazımdan SONRA).
func (s *Store) ctlBump(ctx context.Context, family string) {
	c := s.ctl
	if c == nil {
		return
	}
	c.bump(ctx, family)
	if c.rebumpDelay > 0 {
		time.AfterFunc(c.rebumpDelay, func() { c.bump(context.Background(), family) })
	}
}

// ctlLoad — önbellekli okuma. load FINAL'li CH okumasıdır; hata
// önbelleklenmez. Dönen değer önbellekle paylaşılır — çağıran kopyalar.
func ctlLoad[T any](ctx context.Context, s *Store, family, key string, load func(context.Context) (T, error)) (T, error) {
	gen, ok := s.ctl.genOf(ctx, family)
	if !ok {
		return load(ctx)
	}
	if v, hit := s.ctl.get(family, key, gen); hit {
		if t, ok := v.(T); ok {
			return t, nil
		}
	}
	v, err := load(ctx)
	if err == nil {
		s.ctl.put(family, key, gen, v)
	}
	return v, err
}
