package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/selfobs"
)

// v0.10.2229 (Redis R3, audit O2/G2) — nesil sayaçlı geçersizleştirme.
//
// Önceden cacheInvalidatePrefix istek yolunda DelPrefix ile TÜM anahtar
// uzayını SCAN ediyordu (ack/resolve başına 3 tarama). Artık kayıtlı her
// önekin Redis'te bir nesil sayacı var (coremetry:cache:gen:v1:<önek>);
// geçersizleştirme = o sayaca tek INCR. Bir L2 zarfı, yazıldığı andaki
// (hesaplamadan ÖNCE okunan) nesil dizisini taşır; okuma anahtarı ve
// eşleşen sayaçları TEK MGET'te alır, dizi tutmazsa girdi yok sayılır
// (miss). Eski nesil girdileri Redis TTL'iyle (3 × yumuşak TTL) düşer.
//
// Anlam DelPrefix'ten zayıf değil, daha güçlü: sayaç tüm replikalarda
// aynı Redis'te; ayrıca INCR'dan önce başlamış bir SWR tazelemesi eski
// nesli taşıdığı için yazdığı sonuç bir sonraki okumada miss olur
// (DelPrefix'te o yazım taze sayılıyordu). L1 (≤ 5 sn) yine yerel
// delPrefix + "prefix:" yayınıyla düşer.

// cacheGenPrefixes — kayıtlı önekler (cacheInvalidatePrefix çağrılarının
// tamamı; cache_gen_test.go kaynak taramasıyla sabitler). Bir anahtar
// birden çok önekle başlayabilir ("inbox:count:…" → "inbox:" ve
// "inbox:count"); hepsinin nesli zarfa girer, biri artınca girdi düşer.
var cacheGenPrefixes = []string{
	"problems", "problems:",
	"inbox:", "inbox:count",
	"ml:",
	"rollouts:",
	"problem-verdicts:",
	"anomaly:", "anomaly:unified:", "anomaly:events:",
	"svc-bundle:",
	"exc-groups:",
	"cases-pending:",
	"announcement",
}

const (
	cacheGenKeyPrefix = "coremetry:cache:gen:v1:"
	// cacheGenTTL — sayaç anahtarının ömrü (EXPIRE NX: ilk INCR'da kurulur).
	// En uzun girdi ömrünün (3 × 1 sa) çok üstünde; düşerse sayaç 0'a döner
	// ve o andaki tüm girdiler miss olur (güvenli yön).
	cacheGenTTL = 7 * 24 * time.Hour
	// cacheGenUnknown — nesil okunamadı (Redis hatası/devre açık): bu dizi
	// hiçbir okumayla eşleşmez, yazılan girdi zararsız bir miss olur.
	cacheGenUnknown = "?"
)

func isCacheGenPrefix(p string) bool {
	for _, g := range cacheGenPrefixes {
		if g == p {
			return true
		}
	}
	return false
}

// cacheGenKeys — key'in bağlı olduğu nesil sayaçları (sıra sabit). Boş =
// anahtar hiçbir kayıtlı aileye ait değil, nesil denetimi yok.
func cacheGenKeys(key string) []string {
	var out []string
	for _, p := range cacheGenPrefixes {
		if strings.HasPrefix(key, p) {
			out = append(out, cacheGenKeyPrefix+p)
		}
	}
	return out
}

// genString — MGET sonucundaki sayaçlardan zarf dizisi ("0.17000…").
func genString(vals [][]byte) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		if len(v) == 0 {
			parts[i] = "0"
		} else {
			parts[i] = string(v)
		}
	}
	return strings.Join(parts, ".")
}

// l2GetGen — devre kesicili L2 okuması + nesil: anahtar ve sayaçlar TEK
// MGET'te. gen: zarfa yazılacak/karşılaştırılacak dizi ("" = aile yok,
// cacheGenUnknown = okunamadı). Aile yoksa l2Get ile birebir aynı.
func (s *Server) l2GetGen(ctx context.Context, key string) (raw []byte, gen string, ok bool) {
	gk := cacheGenKeys(key)
	if len(gk) == 0 {
		raw, ok = s.l2Get(ctx, key)
		return raw, "", ok
	}
	b := s.l2()
	if !b.allow() {
		return nil, cacheGenUnknown, false
	}
	t0 := time.Now()
	vals, err := s.cache.MGet(ctx, append([]string{key}, gk...))
	selfobs.ObserveCacheL2("get", time.Since(t0), err)
	b.done(ctx, err)
	if err != nil || len(vals) != len(gk)+1 {
		return nil, cacheGenUnknown, false
	}
	return vals[0], genString(vals[1:]), len(vals[0]) > 0
}

// cacheGenOf — yalnız nesil (bypass yolu ve ısıtıcı: gövde okunmadan
// hesaplanacakken, hesaplamadan ÖNCE alınır).
func (s *Server) cacheGenOf(ctx context.Context, key string) string {
	gk := cacheGenKeys(key)
	if len(gk) == 0 {
		return ""
	}
	b := s.l2()
	if !b.allow() {
		return cacheGenUnknown
	}
	t0 := time.Now()
	vals, err := s.cache.MGet(ctx, gk)
	selfobs.ObserveCacheL2("get", time.Since(t0), err)
	b.done(ctx, err)
	if err != nil || len(vals) != len(gk) {
		return cacheGenUnknown
	}
	return genString(vals)
}

// bumpCacheGen — önekin nesil sayacını artırır (tek INCR, SCAN yok). İlk
// INCR (sonuç 1 = anahtar yoktu) değeri o anki milisaniyeye taşır: sayaç
// TTL'le düşüp yeniden kurulursa eski ömrün değerleri asla geri gelmez,
// hâlâ yaşayan eski bir girdi yeni nesille eşleşemez.
func (s *Server) bumpCacheGen(ctx context.Context, prefix string) error {
	k := cacheGenKeyPrefix + prefix
	n, err := s.cache.Incr(ctx, k, 1, cacheGenTTL)
	if err == nil && n == 1 {
		_, err = s.cache.Incr(ctx, k, time.Now().UnixMilli(), 0)
	}
	return err
}

var unregisteredGenWarn sync.Map

// delPrefixOffPath — kayıt dışı önek için emniyet ağı: eski DelPrefix
// taraması İSTEK YOLUNDAN AYRI goroutine'de (2 sn'lik kendi bağlamıyla).
// cache_gen_test.go her çağrının kayıtlı olduğunu sabitlediği için
// üretimde koşmaz; koşarsa bir kez loglar.
func (s *Server) delPrefixOffPath(prefix string) {
	if _, dup := unregisteredGenWarn.LoadOrStore(prefix, true); !dup {
		log.Printf("[cache] invalidate prefix %q kayıtlı değil (cacheGenPrefixes) — arka planda DelPrefix", prefix)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.cache.DelPrefix(ctx, prefix); err != nil {
			log.Printf("[cache] invalidate L2 del prefix %s: %v", prefix, err)
		}
	}()
}

// l2FreshGen — ısıtıcı: L2'de bu nesilde taze (< ttl) zarf var mı; gen,
// hesaplamadan önce okunan nesil (storeCached'e verilir).
func (s *Server) l2FreshGen(ctx context.Context, key string, ttl time.Duration) (string, bool) {
	raw, gen, ok := s.l2GetGen(ctx, key)
	written, _, eg, envOK := unwrapEnvelopeGen(raw)
	return gen, ok && envOK && genMatches(gen, eg) && time.Since(written) < ttl
}

// genMatches — zarf nesli okunan nesille tutuyor mu (aile yoksa hep evet).
func genMatches(want, have string) bool {
	return want == "" || (want != cacheGenUnknown && want == have)
}
