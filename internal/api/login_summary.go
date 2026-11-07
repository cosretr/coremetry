package api

// login_summary.go — v0.10.1905 (yeni login sayfası, docs/frontend/login-redesign.md §4, §5.6).
//
//   GET /api/public/login-summary   — KİMLİKSİZ, yalnız GET (auth.SkipPath).
//
// İstek yolu YALNIZ bellektedir: yayınlanmış anlık görüntü + branding anahtarları
// (atomik işaretçiler) → loginsummary.Project. CH, VM, Redis, store ÇAĞRISI YOK;
// store/VM nil iken de 200 döner. Anlık görüntüyü lider kilitli Builder kurar
// (main.go), Redis'e yazar; her api pod'u 30 sn'de bir çeker. s.serveCached
// KULLANILMAZ: önbellek anahtarı/CH yolu gerekmez, cevap zaten bellekten.
// Anonim olduğu için IP-anahtarlı sabit pencere sınırı (ip_rate.go, 1800/dk/pod)
// ve Cache-Control: max-age=30. Cevap alanları ve sızıntı kuralları
// loginsummary.Project'te; branding ayarı (loginSummary/loginTopology) ≤ 30 sn
// gecikmeyle cfgRefresh'ten gelir.
//
// Ayar henüz hiç okunmamışsa (boot, CH erişilemez) uç KAPALI cevap verir
// ({enabled:false,ready:false}): operatörün "off" tercihi yüklenmeden anonim
// veri açılmaz.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/loginsummary"
)

func init() { registerRoutesExtra("login-summary", (*Server).registerLoginSummaryRoutes) }

func (s *Server) registerLoginSummaryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/login-summary", s.getLoginSummary)
}

const (
	// loginSummaryCacheKey — Redis anahtarı (anlık görüntü biçim sürümüyle uyumlu).
	loginSummaryCacheKey     = "coremetry:login-summary:v1"
	loginSummaryCacheTTL     = 48 * time.Hour
	loginSummaryRedisTimeout = 5 * time.Second

	loginSummaryRateLimit  = 1800
	loginSummaryRateWindow = time.Minute
)

// loginBrandingFlags — branding'in login özetini ilgilendiren anahtarları
// (bellek-içi; cfgRefresh doldurur).
type loginBrandingFlags struct {
	summary  bool
	topology string
}

// loginSummaryPullEvery — Redis'ten çekme aralığı (test kısaltır).
var loginSummaryPullEvery = 30 * time.Second

var (
	loginSummarySnap  atomic.Pointer[loginsummary.Snapshot]
	loginSummaryFlags atomic.Pointer[loginBrandingFlags]
	loginSummaryRate  = newIPWindowLimiter(loginSummaryRateLimit, loginSummaryRateWindow, nil)
)

// SetLoginSummaryBranding — branding okunduğunda (boot + 30 sn cfgRefresh) çağrılır.
func SetLoginSummaryBranding(b chstore.BrandingSettings) {
	loginSummaryFlags.Store(&loginBrandingFlags{summary: b.LoginSummaryEnabled(), topology: b.ResolvedLoginTopology()})
}

// LoginSummaryEnabled — Builder kapısı: özet PANELİ ayarı açık mı (ayar hiç
// okunmadıysa kapalı). Kapalıyken kurucu sayı/VM/boyut okumaz; topoloji
// kendi kapısıyla (LoginSummaryTopologyOn) okunmaya devam edebilir.
func LoginSummaryEnabled() bool {
	fl := loginSummaryFlags.Load()
	return fl != nil && fl.summary
}

// LoginSummaryTopologyOn — Builder kapısı: topoloji arka planı kapalı değilse true.
func LoginSummaryTopologyOn() bool {
	fl := loginSummaryFlags.Load()
	return fl != nil && fl.topology != loginsummary.TopologyOff
}

// PublishLoginSummary — Builder yayını: pod belleğine ve Redis'e (48 sa TTL).
// Redis hatası yalnız günlüğe gider (bellek yayını yine yapıldı).
func PublishLoginSummary(ctx context.Context, c cache.Cache, snap *loginsummary.Snapshot) {
	if snap == nil {
		return
	}
	loginSummarySnap.Store(snap)
	if c == nil {
		return
	}
	blob, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[login-summary] anlık görüntü serileştirilemedi: %v", err)
		return
	}
	rctx, cancel := context.WithTimeout(ctx, loginSummaryRedisTimeout)
	defer cancel()
	if err := c.Set(rctx, loginSummaryCacheKey, blob, loginSummaryCacheTTL); err != nil {
		log.Printf("[login-summary] Redis yayını başarısız: %v", err)
	}
}

// LoadLoginSummary — Builder'ın başlangıç okuması (tuz + halka devralma):
// Redis'teki anlık görüntü, yoksa pod belleği; ikisi de yoksa nil.
func LoadLoginSummary(ctx context.Context, c cache.Cache) *loginsummary.Snapshot {
	if snap := pullLoginSummary(ctx, c); snap != nil {
		return snap
	}
	return loginSummarySnap.Load()
}

func pullLoginSummary(ctx context.Context, c cache.Cache) *loginsummary.Snapshot {
	if c == nil {
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, loginSummaryRedisTimeout)
	defer cancel()
	blob, ok, err := c.Get(rctx, loginSummaryCacheKey)
	if err != nil || !ok || len(blob) == 0 {
		return nil
	}
	var snap loginsummary.Snapshot
	if err := json.Unmarshal(blob, &snap); err != nil || snap.V != loginsummary.SnapshotVersion {
		return nil
	}
	return &snap
}

// StartLoginSummarySync — her api pod'unda: 30 sn'de bir Redis'ten çeker;
// yalnız daha YENİ anlık görüntü bellekteki yerine geçer. ctx bitene dek.
func StartLoginSummarySync(ctx context.Context, c cache.Cache) {
	go func() {
		tk := time.NewTicker(loginSummaryPullEvery)
		defer tk.Stop()
		for {
			if snap := pullLoginSummary(ctx, c); snap != nil {
				if cur := loginSummarySnap.Load(); cur == nil || snap.GeneratedAt > cur.GeneratedAt {
					loginSummarySnap.Store(snap)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
			}
		}
	}()
}

// loginSummaryResponse — SAF karar: ayar yüklenmemişse kapalı; aksi hâlde Project.
func loginSummaryResponse(snap *loginsummary.Snapshot, fl *loginBrandingFlags, allowP95 bool, now time.Time) loginsummary.Response {
	if fl == nil {
		return loginsummary.Response{Enabled: false}
	}
	return loginsummary.Project(snap, loginsummary.Flags{
		Enabled: fl.summary, Topology: fl.topology, AllowP95: allowP95, Now: now,
	})
}

func (s *Server) getLoginSummary(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "max-age=30")
	if !loginSummaryRate.allow(remoteHost(r)) {
		w.Header().Set("Retry-After", ipRateRetryAfter)
		writeJSONError(w, http.StatusTooManyRequests, "rate limited")
		return
	}
	allowP95 := s.vmetrics != nil && s.vmetrics.CurrentSettings().AllowUnfilteredPercentiles
	writeJSON(w, loginSummaryResponse(loginSummarySnap.Load(), loginSummaryFlags.Load(), allowP95, time.Now()))
}
