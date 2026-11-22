package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache/cachetest"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// pageWarmTestServer — VM yapılandırılmış (red-metric / spark serveCached'e
// ulaşsın), store'suz; yalnız anahtar yoklaması için.
func pageWarmTestServer(t *testing.T) *Server {
	t.Helper()
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: "http://127.0.0.1:1"})
	return &Server{vmetrics: vm, l1: newL1Cache(8), stats: newCacheStats()}
}

// probeKey — handler'ın serveCached'e vereceği anahtar (hesap yok).
func pageWarmProbeKey(t *testing.T, s *Server, route string, h pageWarmHandler, rawQuery string) string {
	t.Helper()
	var key string
	r, err := http.NewRequestWithContext(withPageWarm(context.Background(), &pageWarmOpts{probe: &key}),
		http.MethodGet, strings.TrimPrefix(route, "GET ")+"?"+rawQuery, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Pattern = route
	h(s, &pageWarmWriter{}, r)
	if key == "" {
		t.Fatalf("%s?%s: serveCached'e ulaşılmadı", route, rawQuery)
	}
	return key
}

// pageURL — tarayıcıdan ölçülen istek (2026-11-22, yerel v0.10.2466) şablonu;
// {R} = tarayıcının from/to'su (sunucu saatinden birkaç sn geride).
func pageURL(tmpl string, browserTo time.Time) string {
	r := fmt.Sprintf("from=%d&to=%d", browserTo.Add(-pageWarmWindow).UnixNano(), browserTo.UnixNano())
	return strings.ReplaceAll(tmpl, "{R}", r)
}

// TestPageWarmKeysMatchPage — ısıtıcının yazdığı anahtar sayfanın okuduğu
// anahtarla BİREBİR (eski services-landing / databases 1 sa girdilerinin ölüm
// nedeni). Sayfa tarafı = ölçülen gerçek URL'ler, farklı from/to ns ile.
func TestPageWarmKeysMatchPage(t *testing.T) {
	s := pageWarmTestServer(t)
	now := time.Now()
	browser := now.Add(-3 * time.Second)
	byRoute := map[string]pageWarmReq{}
	for _, p := range pageWarmRegistry {
		for _, env := range []string{"", "uat"} {
			for _, req := range p.Requests(now.Add(-pageWarmWindow), now, env) {
				byRoute[req.Route+"|"+env] = req
			}
		}
	}
	cases := []struct{ route, env, page string }{
		{"GET /api/services-metric-rank", "", "{R}&limit=50&offset=0&sort=spanCount&dir=desc&withTotal=1"},
		{"GET /api/services-metric-rank", "uat", "{R}&limit=50&offset=0&sort=spanCount&dir=desc&env=uat&withTotal=1"},
		{"GET /api/services-runtimes", "", ""},
		{"GET /api/clusters", "", "{R}"},
		{"GET /api/namespaces", "", "{R}"},
		{"GET /api/endpoints", "", "{R}&limit=100&compare=prior&sort=calls&dir=desc"},
		{"GET /api/databases", "", "{R}"},
		{"GET /api/databases/slow-queries", "", "{R}&limit=10"},
		{"GET /api/databases/problems", "", ""},
		{"GET /api/databases/trends", "", "{R}"},
	}
	for _, c := range cases {
		req, ok := byRoute[c.route+"|"+c.env]
		if !ok {
			t.Errorf("%s env=%q kayıtta yok", c.route, c.env)
			continue
		}
		page := pageWarmProbeKey(t, s, c.route, req.Handle, pageURL(c.page, browser))
		warm := pageWarmProbeKey(t, s, c.route, req.Handle, req.Query.Encode())
		if page != warm {
			t.Errorf("%s env=%q anahtar ayrıştı:\n page=%s\n warm=%s", c.route, c.env, page, warm)
		}
		if staleHorizon(warm, 30*time.Second) < pageWarmStale {
			t.Errorf("%s: anahtar %q uzun bayat ufkunda değil (pageWarmStaleFamilies)", c.route, warm)
		}
	}

	for _, p := range pageWarmRegistry {
		if p.Envs != (p.Name == "services") {
			t.Errorf("%s: env varyantı yalnız Services'te (ham span yolu bütçesi)", p.Name)
		}
	}

	// İkinci dalga: sayfa sırası (b, a) ile gelen adlar; red-metric'in kaynakları.
	rank := byRoute["GET /api/services-metric-rank|"]
	next := rank.Next([]byte(`{"services":[{"name":"b-svc"},{"name":"a-svc"}]}`))
	if len(next) != 2 {
		t.Fatalf("metric-rank → sparkline + red-metric bekleniyordu: %d", len(next))
	}
	second := []struct{ tmpl string }{
		{"{R}&services=b-svc%2Ca-svc&maxSlots=80"},
		{"{R}&services=b-svc%2Ca-svc"},
	}
	for i, n := range next {
		page := pageWarmProbeKey(t, s, n.Route, n.Handle, pageURL(second[i].tmpl, browser))
		warm := pageWarmProbeKey(t, s, n.Route, n.Handle, n.Query.Encode())
		if page != warm || staleHorizon(warm, 30*time.Second) < pageWarmStale {
			t.Errorf("%s anahtar ayrıştı / kısa ufuk:\n page=%s\n warm=%s", n.Route, page, warm)
		}
	}
	spark := next[1].Next([]byte(`{"services":{"b-svc":{"source":"http"},"a-svc":{"source":"spanmetrics"}}}`))
	if len(spark) != 1 {
		t.Fatalf("red-metric → spark bekleniyordu: %d", len(spark))
	}
	page := pageWarmProbeKey(t, s, spark[0].Route, spark[0].Handle, pageURL("{R}&services=a-svc%2Cb-svc&sources=spanmetrics%2Chttp&slots=80", browser))
	warm := pageWarmProbeKey(t, s, spark[0].Route, spark[0].Handle, spark[0].Query.Encode())
	if page != warm || staleHorizon(warm, 30*time.Second) < pageWarmStale {
		t.Errorf("spark anahtar ayrıştı:\n page=%s\n warm=%s", page, warm)
	}
	// env varyantında span sparkline'ı yok (FE: env varken çekilmez).
	if n := byRoute["GET /api/services-metric-rank|uat"].Next([]byte(`{"services":[{"name":"x"}]}`)); len(n) != 1 || n[0].Route != "GET /api/services-red-metric" || n[0].Query.Get("env") != "uat" {
		t.Errorf("env varyantı ikinci dalga: %+v", n)
	}
}

// TestPageWarmFrontendDefaultsPinned — kayıttaki varsayılanlar FE'nin
// varsayılanlarıyla aynı; biri değişirse bu test ısıtıcıyı da güncelletir.
func TestPageWarmFrontendDefaultsPinned(t *testing.T) {
	pins := map[string][]string{
		"../../frontend/src/lib/useUrlRange.ts":       {"export const DEFAULT_RANGE_PRESET = '3h';"},
		"../../frontend/src/lib/servicesTable.ts":     {"DEFAULT_SERVICES_SORT: SortState = { id: 'spanCount', dir: 'desc' }"},
		"../../frontend/src/lib/servicesMetricRed.ts": {"METRIC_SORT_COLS = new Set(['spanCount'"},
		"../../frontend/src/lib/sparkline.ts":         {"export const SPARK_DEFAULT_WIDTH = 80;"},
		"../../frontend/src/pages/Services.tsx":       {"const PAGE_SIZE = 50;", "withTotal: '1',", "if (names.length > 0 && !env) {"},
		"../../frontend/src/pages/Endpoints.tsx": {
			"zParam(zOneOf(ENDPOINT_LIMITS), 100)",
			"DEFAULT_ENDPOINTS_SORT = { id: 'calls', dir: 'desc'",
			"const compare = params.get('compare') !== '0';",
		},
		"../../frontend/src/pages/Databases.tsx": {"limit: 10,", "api.databases(from, to, compare ? 'prior' : undefined, env || undefined)"},
	}
	if pageWarmWindow != 3*time.Hour || servicesWarmSlots != "80" {
		t.Fatal("pencere / slot sabiti FE'den ayrıştı")
	}
	for f, want := range pins {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, w := range want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s artık %q içermiyor — page_warm.go kaydını güncelle", f, w)
			}
		}
	}
}

func TestStaleHorizon(t *testing.T) {
	if got := staleHorizon("svc-metric-rank-page:v1:x", 30*time.Second); got != pageWarmStale {
		t.Errorf("ısıtılan aile: %v", got)
	}
	if got := staleHorizon("all-service-runtimes", 5*time.Minute); got != 15*time.Minute {
		t.Errorf("uzun TTL'de staleFactor kalır: %v", got)
	}
	if got := staleHorizon("problems:x", 30*time.Second); got != 90*time.Second {
		t.Errorf("diğer aile değişmemeli: %v", got)
	}
}

// TestPageWarmStaleServedWithBackgroundRefresh — 5 dk'lık zarf ısıtılan
// ailede bayat-anında servis edilir + arka planda tazelenir; ısıtılmayan
// ailede (90 sn ufuk) aynı yaş bloklayan miss'tir.
func TestPageWarmStaleServedWithBackgroundRefresh(t *testing.T) {
	mem := cachetest.NewMem()
	s := &Server{cache: mem, l1: newL1Cache(8), stats: newCacheStats()}
	ctx := context.Background()
	seed := func(key string) {
		env, err := encodeEnvelope([]byte(`{"v":1}`), "", time.Now().Add(-5*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		_ = mem.Set(ctx, l2ValueKey(key), env, time.Hour)
	}
	var calls atomic.Int32
	fn := func(context.Context) (any, error) { calls.Add(1); return map[string]int{"v": 2}, nil }

	warmKey := "svc-red-metric:v1:test"
	seed(warmKey)
	body, tier, err := s.cachedJSON(ctx, warmKey, 30*time.Second, false, fn)
	if err != nil || tier != "STALE" || string(body) != `{"v":1}` {
		t.Fatalf("bayat servis bekleniyordu: tier=%s body=%s err=%v", tier, body, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		raw, _, _ := mem.Get(ctx, l2ValueKey(warmKey))
		if _, b, ok := unwrapEnvelope(raw); ok && string(b) == `{"v":2}` {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("arka plan tazelemesi L2'ye yazmadı")
		}
		time.Sleep(10 * time.Millisecond)
	}

	coldKey := "problems:test"
	seed(coldKey)
	_, tier, _ = s.cachedJSON(ctx, coldKey, 30*time.Second, false, fn)
	if tier != "MISS" {
		t.Errorf("ısıtılmayan aile 5 dk'lık girdiyi bayat servis etmemeli: %s", tier)
	}
}

// TestPageWarmComputeRefreshAhead — genç girdi hesapsız (WARM-FRESH), yaşlı
// girdi tazelenir (WARM) ve L2'ye yazılır.
func TestPageWarmComputeRefreshAhead(t *testing.T) {
	mem := cachetest.NewMem()
	s := &Server{cache: mem, l1: newL1Cache(8), stats: newCacheStats()}
	ctx := withPageWarm(context.Background(), &pageWarmOpts{tick: pageWarmTick})
	key := "endpoints:v2:test"
	var calls atomic.Int32
	fn := func(context.Context) (any, error) { calls.Add(1); return []int{int(calls.Load())}, nil }
	put := func(age time.Duration) {
		env, _ := encodeEnvelope([]byte(`[0]`), "", time.Now().Add(-age))
		_ = mem.Set(context.Background(), l2ValueKey(key), env, time.Hour)
	}
	put(2 * time.Second)
	if _, tier, _ := s.cachedJSON(ctx, key, 30*time.Second, false, fn); tier != "WARM-FRESH" || calls.Load() != 0 {
		t.Fatalf("genç girdi hesaplanmamalı: tier=%s calls=%d", tier, calls.Load())
	}
	put(20 * time.Second)
	body, tier, err := s.cachedJSON(ctx, key, 30*time.Second, false, fn)
	if err != nil || tier != "WARM" || string(body) != `[1]` {
		t.Fatalf("yaşlı girdi tazelenmeli: tier=%s body=%s err=%v", tier, body, err)
	}
	raw, _, _ := mem.Get(context.Background(), l2ValueKey(key))
	if _, b, ok := unwrapEnvelope(raw); !ok || string(b) != `[1]` {
		t.Fatalf("L2'ye yazılmadı: %s", b)
	}
}

// TestPageWarmLeaderOnly — tik başına tek pod; Redis hatası açık kapı.
func TestPageWarmLeaderOnly(t *testing.T) {
	mem := cachetest.NewMem()
	ctx := context.Background()
	if !pageWarmClaim(ctx, mem) || pageWarmClaim(ctx, mem) {
		t.Fatal("ilk claim kazanmalı, ikincisi kaybetmeli")
	}
	// claim başkasında → tik hiçbir handler'ı çağırmadan döner (store nil:
	// çağırsaydı panik).
	follower := &Server{cache: mem, l1: newL1Cache(8), stats: newCacheStats()}
	if follower.pageWarmTickRun(0, time.Now()) {
		t.Fatal("lider olmayan pod ısıtmamalı")
	}
	mem.Down.Store(true)
	if !pageWarmClaim(ctx, mem) {
		t.Fatal("Redis yokken açık kapı (her pod kendi L1'i)")
	}
}

// TestPageWarmEnvsBounded — en yeni ≤ 10 env, 24 sa'ten eskiler düşer;
// kademe: 4 tikte her env tam bir kez, tik başına ≤ 3 env + varsayılan.
func TestPageWarmEnvsBounded(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	var raw [][]byte
	for i := 0; i < 15; i++ {
		raw = append(raw, []byte(strconv.FormatInt(now.Unix()-int64(i*60), 10)+"\tenv"+strconv.Itoa(i)))
	}
	raw = append(raw, []byte(strconv.FormatInt(now.Unix()-25*3600, 10)+"\told"), []byte("bozuk"), []byte("1\t"))
	envs := pageWarmPickEnvs(raw, now)
	if len(envs) != pageWarmEnvMax {
		t.Fatalf("tavan %d, gelen %d: %v", pageWarmEnvMax, len(envs), envs)
	}
	for _, e := range envs {
		if n, _ := strconv.Atoi(strings.TrimPrefix(e, "env")); n >= 10 || e == "old" {
			t.Errorf("en yeni 10 dışında: %s", e)
		}
	}
	seen := map[string]int{}
	for tick := 0; tick < pageWarmEnvEvery; tick++ {
		got := pageWarmTickEnvs(tick, envs)
		if got[0] != "" || len(got)-1 > 3 {
			t.Errorf("tik %d: %v", tick, got)
		}
		for _, e := range got[1:] {
			seen[e]++
		}
	}
	for _, e := range envs {
		if seen[e] != 1 {
			t.Errorf("%s 4 tikte %d kez", e, seen[e])
		}
	}
}

// TestNotePageWarmEnvSkipsWarmer — ısıtıcının kendi isteği env izini
// tazelemez (yoksa env 24 sa'te hiç düşmezdi); boş env iz bırakmaz.
func TestNotePageWarmEnvSkipsWarmer(t *testing.T) {
	mem := cachetest.NewMem()
	s := &Server{cache: mem}
	u, _ := url.Parse("/api/databases?env=pw-test-warm")
	r := (&http.Request{URL: u}).WithContext(withPageWarm(context.Background(), &pageWarmOpts{}))
	s.notePageWarmEnv(r, "pw-test-warm")
	s.notePageWarmEnv((&http.Request{URL: u}).WithContext(context.Background()), "")
	time.Sleep(50 * time.Millisecond)
	if raw, _ := mem.ScanPrefix(context.Background(), pageWarmEnvPrefix); len(raw) != 0 {
		t.Fatalf("iz yazılmamalıydı: %q", raw)
	}
	s.notePageWarmEnv((&http.Request{URL: u}).WithContext(context.Background()), "pw-test-user")
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, _ := mem.ScanPrefix(context.Background(), pageWarmEnvPrefix)
		if len(raw) == 1 && strings.HasSuffix(string(raw[0]), "\tpw-test-user") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("kullanıcı isteği iz bırakmalı: %q", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
