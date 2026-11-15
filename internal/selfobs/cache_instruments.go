package selfobs

import (
	"context"
	"log"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// v0.10.2227 (Redis R1) — önbellek katmanı taban ölçümleri. Denetimin
// (docs/architecture/redis-usage-audit.md §5) sonraki dilimleri önce/sonra
// bunlarla ölçülür:
//
//	cache.requests{family,tier}     — anahtar ailesi × katman (HIT-L1 / HIT /
//	                                  STALE / MISS / …); MISS payı R2'nin ölçüsü
//	cache.l2.duration{op,result}    — Redis turu (get/set × ok/error)
//	redis.keys / redis.evicted_keys — DBSIZE ve INFO evicted_keys (K3)
//
// Kardinalite: family çağıranda sınırlı (kod sabiti önekler + "other"),
// tier/op/result sabit kümeler.

type cacheTierKey struct{ family, tier string }
type cacheL2Key struct{ op, result string }

var (
	cacheOnce  sync.Once
	cacheReqs  metric.Int64Counter
	cacheL2    metric.Float64Histogram
	cacheSets  setCache[cacheTierKey]
	cacheL2Set setCache[cacheL2Key]
)

// cacheL2Buckets — saniye; Redis turu ms mertebesi, istemci tavanı 500 ms.
var cacheL2Buckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}

func cacheInstruments() {
	cacheOnce.Do(func() {
		var e error
		if cacheReqs, e = Meter().Int64Counter("cache.requests",
			metric.WithDescription("Önbellekli okuma, anahtar ailesi × katman (HIT-L1|HIT|STALE|MISS|…)")); e != nil {
			log.Printf("[selfobs] cache.requests: %v", e)
		}
		if cacheL2, e = Meter().Float64Histogram("cache.l2.duration",
			metric.WithUnit("s"),
			metric.WithDescription("L2 (Redis) yanıt önbelleği turu, op=get|set result=ok|error"),
			metric.WithExplicitBucketBoundaries(cacheL2Buckets...)); e != nil {
			log.Printf("[selfobs] cache.l2.duration: %v", e)
		}
	})
}

// ObserveCacheTier — bir önbellekli okumanın katmanı.
func ObserveCacheTier(family, tier string) {
	cacheInstruments()
	if cacheReqs == nil {
		return
	}
	set := cacheSets.get(cacheTierKey{family, tier}, func() attribute.Set {
		return attribute.NewSet(attribute.String("family", family), attribute.String("tier", tier))
	})
	cacheReqs.Add(context.Background(), 1, metric.WithAttributeSet(set))
}

// ObserveCacheL2 — bir L2 turunun süresi + sonucu.
func ObserveCacheL2(op string, d time.Duration, err error) {
	cacheInstruments()
	if cacheL2 == nil {
		return
	}
	result := "ok"
	if err != nil {
		result = "error"
	}
	set := cacheL2Set.get(cacheL2Key{op, result}, func() attribute.Set {
		return attribute.NewSet(attribute.String("op", op), attribute.String("result", result))
	})
	cacheL2.Record(context.Background(), d.Seconds(), metric.WithAttributeSet(set))
}

// RegisterRedisGauges — redis.keys (gauge) + redis.evicted_keys (sayaç,
// INFO'nun kümülatif değeri). read her export turunda (30 s) bir kez,
// kendi 2 s tavanıyla çağrılır; hata = o tur gözlem yok. Bir kez kurulur.
func RegisterRedisGauges(read func(ctx context.Context) (keys, evicted int64, err error)) {
	keysG, e1 := Meter().Int64ObservableGauge("redis.keys",
		metric.WithDescription("Redis DBSIZE (tüm anahtarlar)"))
	evict, e2 := Meter().Int64ObservableCounter("redis.evicted_keys",
		metric.WithDescription("Redis INFO evicted_keys (maxmemory tahliyesi, kümülatif)"))
	if e1 != nil || e2 != nil {
		log.Printf("[selfobs] redis gauges: %v %v", e1, e2)
		return
	}
	if _, err := Meter().RegisterCallback(func(ctx context.Context, o metric.Observer) error {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		k, ev, err := read(rctx)
		if err != nil {
			return nil
		}
		o.ObserveInt64(keysG, k)
		o.ObserveInt64(evict, ev)
		return nil
	}, keysG, evict); err != nil {
		log.Printf("[selfobs] redis gauges callback: %v", err)
	}
}
