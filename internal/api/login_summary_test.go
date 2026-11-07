package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/loginsummary"
)

// resetLoginSummary — paket düzeyindeki bellek durumunu sıfırlar ve testten
// sonra geri yükler.
func resetLoginSummary(t *testing.T) {
	t.Helper()
	oldSnap, oldFlags, oldRate := loginSummarySnap.Load(), loginSummaryFlags.Load(), loginSummaryRate
	loginSummarySnap.Store(nil)
	loginSummaryFlags.Store(nil)
	loginSummaryRate = newIPWindowLimiter(loginSummaryRateLimit, loginSummaryRateWindow, nil)
	t.Cleanup(func() {
		loginSummarySnap.Store(oldSnap)
		loginSummaryFlags.Store(oldFlags)
		loginSummaryRate = oldRate
	})
}

func freshSnapshot(now time.Time) *loginsummary.Snapshot {
	n := uint64(42)
	return &loginsummary.Snapshot{
		V: loginsummary.SnapshotVersion, GeneratedAt: now.UnixMilli(), Salt: "salt",
		SpansToday: &n, State: loginsummary.StateOK, CheckedAt: now.UnixMilli(),
	}
}

func getSummary(h http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "203.0.113.9:40000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// store ve VM nil iken de 200; ayar yüklenmeden kapalı; yükleninceye dek hazır değil.
func TestLoginSummaryEndpointServesWithNilBackends(t *testing.T) {
	resetLoginSummary(t)
	s := &Server{} // store, vmetrics, cache hepsi nil
	h := s.buildMux()

	w := getSummary(h, http.MethodGet, "/api/public/login-summary")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":false,"ready":false}` {
		t.Fatalf("ayar yüklenmeden kapalı: %d %s", w.Code, w.Body)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "max-age=30" {
		t.Errorf("Cache-Control: %q", cc)
	}

	SetLoginSummaryBranding(chstore.BrandingSettings{})
	w = getSummary(h, http.MethodGet, "/api/public/login-summary")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":true,"ready":false}` {
		t.Fatalf("anlık görüntü yok: %d %s", w.Code, w.Body)
	}

	PublishLoginSummary(context.Background(), nil, freshSnapshot(time.Now()))
	w = getSummary(h, http.MethodGet, "/api/public/login-summary")
	var resp loginsummary.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !resp.Enabled || !resp.Ready || resp.SpansToday == nil || *resp.SpansToday != 42 || resp.Components == nil {
		t.Fatalf("hazır cevap: %d %s", w.Code, w.Body)
	}

	SetLoginSummaryBranding(chstore.BrandingSettings{LoginSummary: "off", LoginTopology: "off"})
	w = getSummary(h, http.MethodGet, "/api/public/login-summary")
	if strings.TrimSpace(w.Body.String()) != `{"enabled":false,"ready":false}` {
		t.Fatalf("panel ve topoloji kapalıyken yalnız enabled+ready: %s", w.Body)
	}

	// v0.10.1917 — panel kapalı, topoloji açık: panel alanları YOK (topoloji panelden bağımsız).
	SetLoginSummaryBranding(chstore.BrandingSettings{LoginSummary: "off"})
	w = getSummary(h, http.MethodGet, "/api/public/login-summary")
	var off loginsummary.Response
	if err := json.Unmarshal(w.Body.Bytes(), &off); err != nil {
		t.Fatal(err)
	}
	if off.Enabled || !off.Ready || off.SpansToday != nil || off.HTTP24h != nil || off.Activity != nil || off.Components != nil {
		t.Fatalf("panel kapalıyken panel alanı sızmamalı: %s", w.Body)
	}
}

func TestLoginSummaryTopologyFlagFollowsBranding(t *testing.T) {
	resetLoginSummary(t)
	snap := freshSnapshot(time.Now())
	snap.Topo = loginsummary.BuildTopology(loginsummary.PickSilhouette([]chstore.ServiceTopologyEdge{
		{ParentService: "api-gateway", ChildNode: "orders-api", NodeKind: "service", Calls: 10},
	}, 180), snap.Salt)
	PublishLoginSummary(context.Background(), nil, snap)
	h := (&Server{}).buildMux()
	for mode, want := range map[string]struct{ topo, named bool }{
		"off": {false, false}, "": {true, false}, "anonymous": {true, false}, "named": {true, true},
	} {
		SetLoginSummaryBranding(chstore.BrandingSettings{LoginTopology: mode})
		w := getSummary(h, http.MethodGet, "/api/public/login-summary")
		var resp loginsummary.Response
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if (resp.Topology != nil) != want.topo {
			t.Errorf("mod %q: topology varlığı %v", mode, resp.Topology != nil)
		}
		if resp.Topology != nil && resp.Topology.Named != want.named {
			t.Errorf("mod %q: named=%v", mode, resp.Topology.Named)
		}
		if !want.named && strings.Contains(w.Body.String(), "orders-api") {
			t.Errorf("mod %q: servis adı sızdı: %s", mode, w.Body)
		}
	}
}

// Kimliksiz GET 200; POST ve alt yol kimlik ister.
func TestLoginSummaryAuthSkipOnlyGETExactPath(t *testing.T) {
	resetLoginSummary(t)
	s, svc := newSessTestServer(t)
	h := s.auth.Middleware(s.buildMux())
	SetLoginSummaryBranding(chstore.BrandingSettings{})

	if w := getSummary(h, http.MethodGet, "/api/public/login-summary"); w.Code != 200 {
		t.Fatalf("kimliksiz GET → %d", w.Code)
	}
	if w := getSummary(h, http.MethodGet, "/api/public/login-summary?x=1"); w.Code != 200 {
		t.Fatalf("sorgu dizgisi yolu değiştirmez → %d", w.Code)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/public/login-summary"},
		{http.MethodPut, "/api/public/login-summary"},
		{http.MethodGet, "/api/public/login-summary/extra"},
		{http.MethodGet, "/api/public/login-summary/"},
		{http.MethodGet, "/api/public/login-summaryx"},
	} {
		if w := getSummary(h, c.method, c.path); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s kimliksiz → %d, 401 beklenir", c.method, c.path, w.Code)
		}
	}
	_ = svc
	if !auth.SkipPath(http.MethodGet, "/api/public/login-summary") || auth.SkipPath(http.MethodPost, "/api/public/login-summary") {
		t.Error("SkipPath yalnız GET")
	}
}

func TestLoginSummaryRouteRegistered(t *testing.T) {
	if _, ok := extraRouteRegistrars["login-summary"]; !ok {
		t.Fatal("login-summary defterde yok")
	}
	_, pattern := (&Server{}).buildMux().Handler(httptest.NewRequest(http.MethodGet, "/api/public/login-summary", nil))
	if pattern != "GET /api/public/login-summary" {
		t.Fatalf("kalıp %q", pattern)
	}
}

// Sınır aşımı: 429 + Retry-After: 30; sınır IP başınadır (port değil).
func TestLoginSummaryRateLimited(t *testing.T) {
	resetLoginSummary(t)
	clk := &fakeClock{t: time.Date(2026, 11, 6, 12, 0, 5, 0, time.UTC)}
	loginSummaryRate = newIPWindowLimiter(2, time.Minute, clk.now)
	SetLoginSummaryBranding(chstore.BrandingSettings{})
	h := (&Server{}).buildMux()
	do := func(addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/public/login-summary", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	do("198.51.100.1:1000")
	do("198.51.100.1:2000") // farklı port, aynı IP
	w := do("198.51.100.1:3000")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "30" {
		t.Fatalf("429 + Retry-After 30 beklenir: %d %v", w.Code, w.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["error"] == "" {
		t.Errorf("429 gövdesi JSON hata: %s", w.Body)
	}
	if w := do("198.51.100.2:1000"); w.Code != 200 {
		t.Errorf("başka IP etkilenmemeli: %d", w.Code)
	}
	clk.t = clk.t.Add(time.Minute)
	if w := do("198.51.100.1:4000"); w.Code != 200 {
		t.Errorf("yeni pencerede yeniden izin: %d", w.Code)
	}
}

// ── Redis yayın/çekme ───────────────────────────────────────────────────

// lsMemCache — yalnız Get/Set'i uygular (gerçek Redis gibi bayt saklar; TTL'i
// kaydeder). Diğer yöntemler gömülü nil arayüzdür: çağrılırlarsa panic eder,
// yani yayın/çekme başka bir Redis çağrısı yapmaz.
type lsMemCache struct {
	cache.Cache
	mu    sync.Mutex
	blobs map[string][]byte
	ttls  map[string]time.Duration
}

func newLSMemCache() *lsMemCache {
	return &lsMemCache{blobs: map[string][]byte{}, ttls: map[string]time.Duration{}}
}
func (m *lsMemCache) Get(_ context.Context, k string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[k]
	return b, ok, nil
}
func (m *lsMemCache) Set(_ context.Context, k string, v []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[k], m.ttls[k] = append([]byte(nil), v...), ttl
	return nil
}

func TestLoginSummaryPublishLoadRoundTrip(t *testing.T) {
	resetLoginSummary(t)
	c := newLSMemCache()
	snap := freshSnapshot(time.Now())
	PublishLoginSummary(context.Background(), c, snap)
	if loginSummarySnap.Load() != snap {
		t.Error("yayın pod belleğine de yazılır")
	}
	if c.ttls["coremetry:login-summary:v1"] != 48*time.Hour {
		t.Errorf("anahtar/TTL: %v", c.ttls)
	}
	loginSummarySnap.Store(nil)
	got := LoadLoginSummary(context.Background(), c)
	if got == nil || got.Salt != "salt" || got.SpansToday == nil || *got.SpansToday != 42 {
		t.Fatalf("Redis'ten okunan: %+v", got)
	}
	// Redis yoksa bellek
	loginSummarySnap.Store(snap)
	if LoadLoginSummary(context.Background(), nil) != snap {
		t.Error("Redis yokken pod belleği")
	}
	// bozuk / yanlış sürüm blob yok sayılır
	c.blobs["coremetry:login-summary:v1"] = []byte(`{"v":99}`)
	if pullLoginSummary(context.Background(), c) != nil {
		t.Error("yanlış sürüm yok sayılmalı")
	}
	c.blobs["coremetry:login-summary:v1"] = []byte(`not json`)
	if pullLoginSummary(context.Background(), c) != nil {
		t.Error("bozuk blob yok sayılmalı")
	}
}

func TestLoginSummarySyncOnlyAdoptsNewer(t *testing.T) {
	resetLoginSummary(t)
	oldEvery := loginSummaryPullEvery
	loginSummaryPullEvery = 10 * time.Millisecond
	t.Cleanup(func() { loginSummaryPullEvery = oldEvery })
	c := newLSMemCache()
	now := time.Now()
	newer := freshSnapshot(now)
	older := freshSnapshot(now.Add(-time.Minute))
	older.Salt = "old"
	PublishLoginSummary(context.Background(), c, older)
	loginSummarySnap.Store(newer) // pod belleği daha yeni
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	StartLoginSummarySync(ctx, c)
	time.Sleep(80 * time.Millisecond) // ilk çekme + birkaç tik
	if got := loginSummarySnap.Load(); got != newer {
		t.Errorf("daha eski Redis kopyası belleği ezmemeli: %+v", got)
	}
	// Redis'e daha yeni yazılırsa bir sonraki çekmede alınır
	fresher := freshSnapshot(now.Add(time.Minute))
	fresher.Salt = "fresh"
	PublishLoginSummary(context.Background(), c, fresher)
	loginSummarySnap.Store(newer) // Publish belleği de yazdı; çekmenin gerçekten çalıştığını görmek için geri al
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := loginSummarySnap.Load(); got != nil && got.Salt == "fresh" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daha yeni Redis kopyası alınmalı")
}
