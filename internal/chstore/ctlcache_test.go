package chstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// fakeGenRedis — paylaşımlı "Redis": yalnız MGet + Incr (nesil sayacı).
type fakeGenRedis struct {
	cache.Cache
	mu   sync.Mutex
	kv   map[string]int64
	fail bool
	gets int
}

func newFakeGenRedis() *fakeGenRedis { return &fakeGenRedis{kv: map[string]int64{}} }

func (f *fakeGenRedis) MGet(_ context.Context, keys []string) ([][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.fail {
		return nil, errors.New("redis down")
	}
	out := make([][]byte, len(keys))
	for i, k := range keys {
		if v, ok := f.kv[k]; ok {
			out[i] = []byte(strconv.FormatInt(v, 10))
		}
	}
	return out, nil
}

func (f *fakeGenRedis) Incr(_ context.Context, key string, d int64, _ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return 0, errors.New("redis down")
	}
	f.kv[key] += d
	return f.kv[key], nil
}

// ctlTestStore — iki "pod" aynı sahte Redis'i paylaşır.
func ctlTestStore(r cache.Cache, configured bool) *Store {
	s := &Store{}
	s.SetControlPlaneCache(r, configured)
	s.ctl.rebumpDelay = 0
	return s
}

func countingLoader(n *int, val string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { *n++; return val + strconv.Itoa(*n), nil }
}

func TestCtlCacheHitThenWriteVisibleOnAllPods(t *testing.T) {
	ctx := context.Background()
	r := newFakeGenRedis()
	a, b := ctlTestStore(r, true), ctlTestStore(r, true)
	var na, nb int
	v1, _ := ctlLoad(ctx, a, ctlMonitors, "list", countingLoader(&na, "a"))
	v2, _ := ctlLoad(ctx, a, ctlMonitors, "list", countingLoader(&na, "a"))
	if na != 1 || v1 != v2 {
		t.Fatalf("ikinci okuma önbellekten gelmeli: loads=%d %q %q", na, v1, v2)
	}
	_, _ = ctlLoad(ctx, b, ctlMonitors, "list", countingLoader(&nb, "b"))
	// Pod A yazar → pod B'nin bir sonraki okuması CH'ye gider.
	a.ctlBump(ctx, ctlMonitors)
	_, _ = ctlLoad(ctx, b, ctlMonitors, "list", countingLoader(&nb, "b"))
	_, _ = ctlLoad(ctx, a, ctlMonitors, "list", countingLoader(&na, "a"))
	if nb != 2 || na != 2 {
		t.Fatalf("yazım sonrası iki pod da yeniden okumalı: a=%d b=%d", na, nb)
	}
	// Başka ailenin yazımı bu aileyi düşürmez.
	a.ctlBump(ctx, ctlSettings)
	_, _ = ctlLoad(ctx, a, ctlMonitors, "list", countingLoader(&na, "a"))
	if na != 2 {
		t.Fatalf("aile ayrımı: monitors yeniden okunmamalıydı (loads=%d)", na)
	}
}

func TestCtlCacheRedisDownFallsBackToCH(t *testing.T) {
	ctx := context.Background()
	r := newFakeGenRedis()
	s := ctlTestStore(r, true)
	var n int
	_, _ = ctlLoad(ctx, s, ctlSavedViews, "k", countingLoader(&n, "x"))
	r.fail = true
	for i := 0; i < 3; i++ {
		_, _ = ctlLoad(ctx, s, ctlSavedViews, "k", countingLoader(&n, "x"))
	}
	if n != 4 {
		t.Fatalf("Redis hatasında her okuma CH'ye gitmeli (R0 sınıf A): loads=%d", n)
	}
	// Devre açıkken Redis'e hiç gidilmez (yalnız ilk hatada bir deneme).
	if r.gets != 2 {
		t.Fatalf("devre kesici: MGET sayısı %d, 2 bekleniyordu", r.gets)
	}
}

func TestCtlCacheRedisConfiguredButNoopBypasses(t *testing.T) {
	ctx := context.Background()
	noop, _ := cache.NewNoop()
	s := ctlTestStore(cache.NewSwitchableCache(noop), true)
	var n int
	_, _ = ctlLoad(ctx, s, ctlSettings, "all", countingLoader(&n, "x"))
	_, _ = ctlLoad(ctx, s, ctlSettings, "all", countingLoader(&n, "x"))
	if n != 2 {
		t.Fatalf("Redis tanımlı ama erişilemez (Noop): önbellek kapalı olmalı, loads=%d", n)
	}
}

func TestCtlCacheNoRedisSinglePodLocalGen(t *testing.T) {
	ctx := context.Background()
	s := ctlTestStore(nil, false)
	var n int
	_, _ = ctlLoad(ctx, s, ctlSettings, "all", countingLoader(&n, "x"))
	_, _ = ctlLoad(ctx, s, ctlSettings, "all", countingLoader(&n, "x"))
	if n != 1 {
		t.Fatalf("Redis'siz tek pod: yerel nesil isabet vermeli, loads=%d", n)
	}
	s.ctlBump(ctx, ctlSettings)
	_, _ = ctlLoad(ctx, s, ctlSettings, "all", countingLoader(&n, "x"))
	if n != 2 {
		t.Fatalf("yerel yazım nesli artırmalı, loads=%d", n)
	}
}

func TestCtlCacheMaxAgeAndErrorsNotCached(t *testing.T) {
	ctx := context.Background()
	s := ctlTestStore(newFakeGenRedis(), true)
	now := time.Unix(1_800_000_000, 0)
	s.ctl.now = func() time.Time { return now }
	var n int
	_, _ = ctlLoad(ctx, s, ctlRunbookExe, "k", countingLoader(&n, "x"))
	now = now.Add(ctlMaxAge)
	_, _ = ctlLoad(ctx, s, ctlRunbookExe, "k", countingLoader(&n, "x"))
	if n != 2 {
		t.Fatalf("ctlMaxAge tavanı: loads=%d", n)
	}
	fails := 0
	bad := func(context.Context) (string, error) { fails++; return "", errors.New("ch") }
	_, _ = ctlLoad(ctx, s, ctlRunbookExe, "e", bad)
	_, _ = ctlLoad(ctx, s, ctlRunbookExe, "e", bad)
	if fails != 2 {
		t.Fatalf("hata önbelleklenmemeli: %d", fails)
	}
}

// Okuma sırasında gelen yazım: okunan (eski) nesille saklanan sonuç sonraki
// okumada miss olmalı.
func TestCtlCacheWriteDuringLoadInvalidates(t *testing.T) {
	ctx := context.Background()
	s := ctlTestStore(newFakeGenRedis(), true)
	var n int
	_, _ = ctlLoad(ctx, s, ctlMonitors, "list", func(context.Context) (string, error) {
		n++
		s.ctlBump(ctx, ctlMonitors)
		return "stale", nil
	})
	v, _ := ctlLoad(ctx, s, ctlMonitors, "list", countingLoader(&n, "fresh"))
	if v != "fresh2" {
		t.Fatalf("okuma sırasındaki yazım girdiyi düşürmeli, %q", v)
	}
}

// Store'a önbellek bağlanmamışsa (testler, araçlar) ctlLoad doğrudan yükler.
func TestCtlCacheNilStoreCacheLoadsDirect(t *testing.T) {
	s := &Store{}
	var n int
	_, _ = ctlLoad(context.Background(), s, ctlMonitors, "list", countingLoader(&n, "x"))
	_, _ = ctlLoad(context.Background(), s, ctlMonitors, "list", countingLoader(&n, "x"))
	s.ctlBump(context.Background(), ctlMonitors) // panik yok
	if n != 2 {
		t.Fatalf("önbelleksiz: her okuma yüklemeli, %d", n)
	}
}

// TestCtlCacheEveryWriteBumps — kaynak taraması: önbellekli dört tablonun
// yazım deyimini taşıyan her dosya, aynı ailenin ctlBump'ını da çağırmalı.
// Yeni bir yazım yolu bump'sız eklenirse diğer pod'lar ctlMaxAge boyunca
// bayat okur. Boot göçü (store.go, eski anahtar silme) hariç.
func TestCtlCacheEveryWriteBumps(t *testing.T) {
	fams := map[string]string{
		"system_settings": "ctlSettings", "saved_views": "ctlSavedViews",
		"runbook_executions": "ctlRunbookExe", "monitors": "ctlMonitors",
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "store.go" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for tbl, fam := range fams {
			re := regexp.MustCompile(`(?i)(INSERT INTO|ALTER TABLE)\s+` + tbl + `\b`)
			if re.MatchString(src) && !strings.Contains(src, "ctlBump(ctx, "+fam+")") {
				t.Errorf("%s %s'e yazıyor ama ctlBump(ctx, %s) çağırmıyor", f, tbl, fam)
			}
		}
	}
}
