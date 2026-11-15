package api

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache/cachetest"
)

// v0.10.2230 (Redis R4) — dağıtık singleflight + olumsuz önbellek.

func dsfWaitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("beklenen olmadı: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// İki replika aynı soğuk anahtarı aynı anda ister: tek hesap, kaybeden
// kazananın zarfını servis eder.
func TestDSFOneComputeAcrossReplicas(t *testing.T) {
	mem := cachetest.NewMem()
	a, b := genTestServer(mem), genTestServer(mem)
	const key = "topology-edges:v1:x"
	var computes atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) (any, error) {
		computes.Add(1)
		<-release
		return map[string]int{"n": 1}, nil
	}
	ctx := context.Background()
	var wg sync.WaitGroup
	var tierA, tierB string
	wg.Add(1)
	go func() { defer wg.Done(); _, tierA, _ = a.cachedJSON(ctx, key, 30*time.Second, false, fn) }()
	dsfWaitFor(t, "A claim", func() bool { return mem.Has(cacheClaimKeyPrefix + key) })
	wg.Add(1)
	go func() { defer wg.Done(); _, tierB, _ = b.cachedJSON(ctx, key, 30*time.Second, false, fn) }()
	time.Sleep(3 * cacheDSFPoll) // B bekliyor
	close(release)
	wg.Wait()
	if n := computes.Load(); n != 1 {
		t.Fatalf("iki replikada tek hesap beklenirdi: %d", n)
	}
	if tierA != "MISS" || tierB != "HIT-PEER" {
		t.Fatalf("A=%s B=%s", tierA, tierB)
	}
	if mem.Has(cacheClaimKeyPrefix + key) {
		t.Fatal("L2 yazımı claim'i silmeli")
	}
}

// Hak sahibi ölmüş (claim var, zarf hiç gelmez): bekleyen süre dolunca
// kendisi hesaplar; istek bütçesini aşmaz.
func TestDSFWaiterFallsBackWhenOwnerDies(t *testing.T) {
	old := cacheDSFWait
	cacheDSFWait = 150 * time.Millisecond
	defer func() { cacheDSFWait = old }()
	mem := cachetest.NewMem()
	s := genTestServer(mem)
	const key = "rootcause:v1:x"
	_ = mem.Set(context.Background(), cacheClaimKeyPrefix+key, []byte("1"), time.Minute)
	t0 := time.Now()
	_, tier, err := s.cachedJSON(context.Background(), key, 30*time.Second, false, func(context.Context) (any, error) { return 1, nil })
	if err != nil || tier != "MISS" {
		t.Fatalf("%s %v", tier, err)
	}
	if d := time.Since(t0); d < cacheDSFWait || d > cacheDSFWait+time.Second {
		t.Fatalf("bekleme sınırı: %v", d)
	}
	// İstek bağlamının süresi kısa: kalan sürenin yarısından fazla beklemez.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = mem.Set(ctx, cacheClaimKeyPrefix+"k2", []byte("1"), time.Minute)
	t0 = time.Now()
	_, _, _ = s.cachedJSON(ctx, "k2", 30*time.Second, false, func(context.Context) (any, error) { return 1, nil })
	if d := time.Since(t0); d > 90*time.Millisecond {
		t.Fatalf("istek bütçesini aştı: %v", d)
	}
}

// SWR tazelemesi: claim başkasındaysa beklemez, atlar.
func TestDSFRefreshSkipsWhenClaimed(t *testing.T) {
	mem := cachetest.NewMem()
	s := genTestServer(mem)
	const key = "svc-deploys:v1:x"
	_ = mem.Set(context.Background(), cacheClaimKeyPrefix+key, []byte("1"), time.Minute)
	called := false
	s.refreshKey(key, time.Second, func(context.Context) (any, error) { called = true; return 1, nil }, "")
	if called {
		t.Fatal("başka replikanın tazelemesi sürerken hesaplanmamalı")
	}
}

// Yalnız zaman aşımı sınıfı hata kısa süre önbelleklenir; doğrulama hatası
// asla; olumsuz girdi 10 sn'de düşer; başarılı hesap onu siler.
func TestNegativeCacheTimeoutOnly(t *testing.T) {
	mem := cachetest.NewMem()
	s := genTestServer(mem)
	ctx := context.Background()
	const key = "logs-patterns:v1:x"
	negKey := cacheNegKeyPrefix + key
	var calls atomic.Int32
	timeout := func(context.Context) (any, error) { calls.Add(1); return nil, context.DeadlineExceeded }

	// Doğrulama hatası: yazılmaz.
	if _, _, err := s.cachedJSON(ctx, key, 30*time.Second, false, func(context.Context) (any, error) {
		return nil, errors.New("geçersiz sorgu")
	}); err == nil {
		t.Fatal("hata beklenirdi")
	}
	time.Sleep(20 * time.Millisecond)
	if mem.Has(negKey) {
		t.Fatal("doğrulama hatası olumsuz önbelleğe girmemeli")
	}
	// İstemci iptali de yazılmaz.
	_, _, _ = s.cachedJSON(ctx, key, 30*time.Second, false, func(context.Context) (any, error) { return nil, context.Canceled })
	time.Sleep(20 * time.Millisecond)
	if mem.Has(negKey) {
		t.Fatal("iptal olumsuz önbelleğe girmemeli")
	}

	if _, _, err := s.cachedJSON(ctx, key, 30*time.Second, false, timeout); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	dsfWaitFor(t, "olumsuz girdi", func() bool { return mem.Has(negKey) })
	b := genTestServer(mem) // başka replika da görür
	_, tier, err := b.cachedJSON(ctx, key, 30*time.Second, false, timeout)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("olumsuz girdi servis edilmeliydi: tier=%s err=%v calls=%d", tier, err, calls.Load())
	}
	mem.Advance(cacheNegMax)
	_, _, _ = b.cachedJSON(ctx, key, 30*time.Second, false, timeout)
	if calls.Load() != 2 {
		t.Fatalf("olumsuz girdi 10 sn'de düşmeliydi: calls=%d", calls.Load())
	}
	dsfWaitFor(t, "olumsuz girdi 2", func() bool { return mem.Has(negKey) })
	// ?refresh=1 başarılı hesap: olumsuz girdiyi siler.
	if _, tier, err := b.cachedJSON(ctx, key, 30*time.Second, true, func(context.Context) (any, error) { return 1, nil }); err != nil || tier != "BYPASS" {
		t.Fatalf("%s %v", tier, err)
	}
	dsfWaitFor(t, "olumsuz girdi silindi", func() bool { return !mem.Has(negKey) })
	if negTTL(time.Second) != cacheNegMin || negTTL(time.Hour) != cacheNegMax {
		t.Fatal("olumsuz TTL 5–10 sn'ye kıstırılmalı")
	}
}

// Devre açık: claim'e/olumsuz girdiye bakılmaz, bugünkü süreç içi yol.
func TestDSFBreakerOpenComputesLocally(t *testing.T) {
	mem := cachetest.NewMem()
	s := genTestServer(mem)
	ctx := context.Background()
	const key = "rootcause:v1:y"
	_ = mem.Set(ctx, cacheClaimKeyPrefix+key, []byte("1"), time.Minute)
	mem.Down.Store(true)
	for i := 0; i < l2BreakerThreshold; i++ {
		s.l2Get(ctx, "k")
	}
	if !s.l2().open() {
		t.Fatal("devre açılmalıydı")
	}
	mem.Down.Store(false)
	t0 := time.Now()
	_, tier, err := s.cachedJSON(ctx, key, 30*time.Second, false, func(context.Context) (any, error) { return 1, nil })
	if err != nil || tier != "MISS" || time.Since(t0) > cacheDSFWait/2 {
		t.Fatalf("devre açıkken beklemeden yerel hesap: %s %v %v", tier, err, time.Since(t0))
	}
	// Redis kesik (devre henüz kapalı) — SetNX hatası = kendin hesapla.
	mem2 := cachetest.NewMem()
	mem2.Down.Store(true)
	s2 := genTestServer(mem2)
	if _, tier, err := s2.cachedJSON(ctx, key, 30*time.Second, false, func(context.Context) (any, error) { return 1, nil }); err != nil || tier != "MISS" {
		t.Fatalf("%s %v", tier, err)
	}
}

func TestIsTimeoutClass(t *testing.T) {
	for err, want := range map[error]bool{
		context.DeadlineExceeded:                  true,
		errors.New("code: 159, TIMEOUT_EXCEEDED"): true,
		context.Canceled:                          false,
		errors.New("unauthorized"):                false,
		errors.New("invalid filter"):              false,
		negCachedErr{msg: "x"}:                    true,
	} {
		if isTimeoutClass(err) != want {
			t.Errorf("%v: %v", err, !want)
		}
	}
}
