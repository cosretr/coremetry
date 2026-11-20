package api

import (
	"context"
	"errors"
	"log"
	"net"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/selfobs"
)

// v0.10.2230 (Redis R4, audit O3/O4, G3/G4) — paylaşılan tek hesaplama ve
// zaman aşımı için kısa olumsuz önbellek.
//
// O3 · Dağıtık singleflight: süreç içi singleflight'ın (sf) İÇİNDE, soğuk
// anahtar ya da SWR tazelemesi hesaplanmadan önce Redis'te
// coremetry:cache:claim:v1:<anahtar> SetNX ile alınır (fleet özeti emsali).
// Kazanan hesaplar; L2 yazımı claim'i aynı pipeline'da siler. Kaybeden:
//   - ön plan (miss): L2'yi kısa süre yoklar (≤ cacheDSFWait, istek
//     bağlamının kalan süresinin yarısı); kazananın zarfı gelirse onu
//     servis eder (HIT-PEER), olumsuz girdi gelirse onun hatasını döner,
//     claim kalkar ya da süre dolarsa KENDİSİ hesaplar;
//   - SWR tazelemesi: hiç beklemez, atlar (bayat değer zaten servis edildi).
// Redis Noop / devre açık / SetNX hatası → bugünkü süreç içi davranış (B
// sınıfı: hata = işi yap). ?refresh=1 claim'e girmez.
//
// O4 · Olumsuz önbellek: YALNIZ zaman aşımı sınıfı hata (context deadline,
// ağ zaman aşımı, CH TIMEOUT_EXCEEDED/159) coremetry:cache:neg:v1:<anahtar>
// altına 5–10 sn (yumuşak TTL'e kıstırılır) "son hata" olarak yazılır.
// Kimlik/doğrulama/sözdizimi hataları ve istemci iptali yazılmaz. Girdi
// nesli taşır (R3): geçersizleştirme onu da düşürür. Başarılı hesaplamanın
// L2 yazımı olumsuz girdiyi aynı pipeline'da siler. ?refresh=1 atlar.

const (
	cacheClaimKeyPrefix = "coremetry:cache:claim:v1:"
	cacheNegKeyPrefix   = "coremetry:cache:neg:v1:"
	// cacheClaimTTL — kazanan ölürse claim'in kendiliğinden düşmesi; SWR
	// tazeleme bütçesiyle (20 sn) aynı. Kaybedenler zaten ≤ cacheDSFWait bekler.
	cacheClaimTTL = 20 * time.Second
	cacheNegMin   = 5 * time.Second
	cacheNegMax   = 10 * time.Second
)

// Test kancaları (üretimde sabit).
var (
	cacheDSFWait = 3 * time.Second
	cacheDSFPoll = 50 * time.Millisecond
)

// dsfMode — computeBodyStore'un dağıtık singleflight kipi.
type dsfMode int

const (
	dsfOff  dsfMode = iota // ?refresh=1, ısıtıcı: yalnız süreç içi
	dsfWait                // ön plan miss: kaybeden bekler
	dsfSkip                // SWR tazelemesi: kaybeden atlar
)

// errDSFSkipped — SWR tazelemesi hakkı başka replikada (sessizce atla).
var errDSFSkipped = errors.New("cache: refresh claimed by peer")

// negCachedErr — olumsuz önbellekten dönen hata: özgün iletiyi taşır,
// context.DeadlineExceeded'i sarar (writeErr aynı yoldan yanıtlar).
type negCachedErr struct{ msg string }

func (e negCachedErr) Error() string { return e.msg }
func (e negCachedErr) Unwrap() error { return context.DeadlineExceeded }

// isTimeoutClass — olumsuz önbelleğe girebilen tek hata sınıfı.
func isTimeoutClass(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var ex *clickhouse.Exception
	if errors.As(err, &ex) && ex.Code == 159 {
		return true
	}
	return strings.Contains(err.Error(), "TIMEOUT_EXCEEDED")
}

func negTTL(ttl time.Duration) time.Duration {
	return min(max(ttl, cacheNegMin), cacheNegMax)
}

// l2Look — tek MGET'in sonucu: gövde, nesil, olumsuz girdi, claim.
type l2Look struct {
	raw    []byte
	ok     bool
	gen    string
	neg    error // nesli tutan olumsuz girdi (yoksa nil)
	claim  bool  // başka bir replika hesaplıyor
	failed bool  // Redis hatası / devre açık
}

// l2Lookup — anahtar + olumsuz girdi + (istenirse) claim + nesil sayaçları
// TEK MGET'te (R3'ün okuma turu genişledi, tur sayısı aynı).
func (s *Server) l2Lookup(ctx context.Context, key string, withClaim bool) l2Look {
	gk := cacheGenKeys(key)
	keys := []string{l2ValueKey(key), cacheNegKeyPrefix + key}
	if withClaim {
		keys = append(keys, cacheClaimKeyPrefix+key)
	}
	head := len(keys)
	keys = append(keys, gk...)
	b := s.l2()
	if !b.allow() {
		return l2Look{gen: cacheGenUnknown, failed: true}
	}
	t0 := time.Now()
	vals, err := s.cache.MGet(ctx, keys)
	selfobs.ObserveCacheL2("get", time.Since(t0), err)
	b.done(ctx, err)
	if err != nil || len(vals) != len(keys) {
		return l2Look{gen: cacheGenUnknown, failed: true}
	}
	lk := l2Look{raw: vals[0], ok: len(vals[0]) > 0, gen: ""}
	if len(gk) > 0 {
		lk.gen = genString(vals[head:])
	}
	if n := vals[1]; len(n) > 0 {
		if g, msg, found := strings.Cut(string(n), "\n"); found && g == lk.gen {
			lk.neg = negCachedErr{msg: msg}
		}
	}
	lk.claim = withClaim && len(vals[2]) > 0
	return lk
}

// storeNeg — zaman aşımı hatasını kısa süre "son hata" olarak yazar
// (ayrık goroutine, 2 sn; istek yolunu bekletmez).
func (s *Server) storeNeg(key, gen string, ttl time.Duration, err error) {
	if gen == cacheGenUnknown || !isTimeoutClass(err) || cache.IsNoop(s.cache) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if e := s.l2Set(ctx, cacheNegKeyPrefix+key, []byte(gen+"\n"+err.Error()), negTTL(ttl)); e != nil {
			log.Printf("[cache] neg set %s: %v", key, e)
		}
	}()
}

// dsfClaim — true = bu replika hesaplar. Noop / devre açık / hata = true.
func (s *Server) dsfClaim(ctx context.Context, key string) bool {
	if cache.IsNoop(s.cache) || !s.l2().allow() {
		return true
	}
	ok, err := s.cache.SetNX(ctx, cacheClaimKeyPrefix+key, []byte("1"), cacheClaimTTL)
	s.l2().done(ctx, err)
	return err != nil || ok
}

// dsfRelease — hesap gövde yazmadan bitti (hata / uncacheable): bekleyenler
// süreyi doldurmadan kendileri hesaplasın.
func (s *Server) dsfRelease(key string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.cache.Del(ctx, cacheClaimKeyPrefix+key)
	}()
}

// dsfAwait — kaybedenin beklemesi. (body, nil, true) = kazananın zarfı;
// (nil, err, true) = kazananın olumsuz girdisi ya da istek iptali;
// (_, _, false) = kendin hesapla.
func (s *Server) dsfAwait(ctx context.Context, key, gen string, ttl time.Duration) ([]byte, error, bool) {
	wait := cacheDSFWait
	if dl, ok := ctx.Deadline(); ok {
		wait = min(wait, time.Until(dl)/2)
	}
	tk := time.NewTicker(cacheDSFPoll)
	defer tk.Stop()
	end := time.NewTimer(wait)
	defer end.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err(), true
		case <-end.C:
			return nil, nil, false
		case <-tk.C:
		}
		lk := s.l2Lookup(ctx, key, true)
		if lk.failed || lk.gen != gen {
			return nil, nil, false // Redis gitti ya da nesil değişti: kendin
		}
		if lk.ok {
			if written, body, eg, envOK := unwrapEnvelopeGen(lk.raw); envOK && genMatches(gen, eg) && time.Since(written) < ttl {
				return body, nil, true
			}
		}
		if lk.neg != nil {
			return nil, lk.neg, true
		}
		if !lk.claim {
			return nil, nil, false // kazanan gövdesiz bitti / öldü
		}
	}
}
