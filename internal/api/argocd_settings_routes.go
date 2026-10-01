package api

// argocd_settings_routes.go — v0.10.957 — Rollouts v2 P1.4 (docs/rollouts/
// v2-audit.md §10.7, §7.2, §5.1–5.6, §12.1; operatör onaylı karar 2 ve 5).
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): yüzeyin üç rotası burada, kayıt
// init()'te route_registry.go defterine ("argocd-settings" adıyla).
//
//   GET  /api/settings/argocd            admin — blob + uygulanan + varsayılan + sınırlar + tokenRef durumu
//   PUT  /api/settings/argocd            admin + audit settings.argocd.update + publishConfigReload("argocd")
//   POST /api/settings/argocd/discover   admin + audit settings.argocd.discover — SALT-OKUNUR keşif probe'u
//
// ── NEDEN yalnız admin (GET dahil) ───────────────────────────────────────
//
// Blob instance API adreslerini, tokenRef'leri (secret DEĞİL ama Secret
// adını/yolunu söyler) ve hub seçimini taşır; thanos_clusters GET'i de
// admin-only. Viewer'ın ihtiyaç duyacağı Argo yüzeyleri (Service kartı,
// feed tetikleyicisi) P3'te kendi viewer uçlarıyla gelir.
//
// ── NEDEN servis paket düzeyinde ─────────────────────────────────────────
//
// Server alanları api.go'da bildiriliyor ve api.go büyümez; emsal
// promqlConsoleCfg / exceptionTriageCfg. main.go SetArgoCDSettings ile
// bağlar (api.SetIngestBatchSize gibi paket düzeyi bir kurulum).
//
// ── Keşif probe'u (P1.4) ─────────────────────────────────────────────────
//
// Hub'ın Thanos'una thanos KONSOL taşımasıyla (ConsoleLabelValues:
// adanmış istemci, gövde tavanı, URL maskeleyen hata metni) bağlanır;
// argocd_app_info'nun `job`, `namespace`, `exported_namespace` değerlerini
// label-values API'siyle okur (§5.4: tekil değerler, seri gövdesi değil;
// start/end AÇIKÇA, sunucu-uygulamalı limit, partial_response=false).
// Adaylar argocd.BuildCandidates'ta saf türetilir ve HİÇBİR ŞEY
// KAYDEDİLMEZ — operatör seçip PUT'la kaydeder (annex §7.2 "öner, asla
// otomatik yazma").
// v0.10.974 — aday bulma label-values'ta KALIR; adaylar kurulduktan SONRA
// ikinci tur sayar (onaylı mockup dipnotu): uygulama sayısı iş başına TEK
// anlık ConsoleQuery `count [by (namespace)] (group by (namespace,
// exported_namespace, name) (argocd_app_info{job="J"}))` (§5.4 kural 1–3:
// anlık, listelemeden önce say, shard/HA kopyalarını grupla; `name` label
// browser'ı ASLA, kural 4), shard sayısı sınırlı `pod` label-values (≤100).
// Sınırlar: ≤500 iş, iş başına ≤500 değer, ≤2000 çağrı (sayımlar DAHİL), çağrı
// başına 15 s, toplam 60 s; pod başına tek eşzamanlı keşif (429). Sayım
// yalnız ARTAN bütçeyi kullanır: aday düşürmez, error/incomplete üretmez,
// 200'ü bozmaz; bütçe biterse kalan adaylara not + countsIncomplete.
// v0.10.990 (operatör: "sadece ilk 50'yi bulduğu için eksikleri oluyor") —
// tavanlar 50 iş / 100 değer / 150 çağrıdan yukarı çekildi: instance başına
// ayrı `job` (<team>-<env>-metrics) taşıyan kurulumda 50'den sonraki
// instance'lar hiç aday olmuyordu. İş başına çağrılar (hepsi `job` süzgeçli,
// §5.4 "asla süzgeçsiz") aynı 60 s'ye sığsın diye argocdDiscoverParallel
// kadar eşzamanlı koşar; aday sırası ve iş başına sözleşme aynıdır.
// Hub yok / bilinmiyor / devre dışı / tokenRef'i çözülemiyor → 400
// guardrail ve upstream'e İSTEK YOK (§7.2 fail-closed ruhu). Upstream
// hatası ConsoleError.StatusCode ile eşlenir; gövde yapılandırılmış URL'yi
// asla taşımaz. injectClusterLabel (§5.6): false iken hub'ın Thanos küme
// etiketi Argo seçicilerine enjekte edilmez (gövde tek seferlik geçersiz
// kılabilir).
//
// v0.10.957 — inceleme (§5.6 iki hub): blob `hubs[{clusterId,
// injectClusterLabel}]` taşır; keşif HUB BAŞINA koşar. Gövdede hub yoksa
// tek kayıtlı hub kullanılır; birden çok hub varken gövde hub'ı seçmek
// zorunda (400 guardrail, istek YOK). Enjeksiyon varsayılanı o hub'ın kendi
// ayarı (listede olmayan hub için true).
//
// v0.10.978 — "yetki yok" (v0.10.974'te ertelenen karar, onaylandı): hub
// Thanos'un 401/403'ü genel "unavailable" değil, sourcestate sözlüğüyle
// `errorType: "unauthorized"` + `upstreamStatus` (401|403) + `hubClusterId`
// taşır; HTTP kodu 502 KALIR — 424 bu depoda upstream kimlik reddi için değil
// "tablo yok" (rollup_routes) için kullanılıyor, promapi.HTTPError da 401/403'ü
// kodu değiştirmeden sourcestate.ErrUnauthorized'a açıyor; burası aynı sözlüğü
// aynalar. Sınıflama HTTP koduna göredir (promapi gibi): 403 + JSON
// errorType "bad_data" da yetki reddidir. Aynı sınıflama aday hatası
// ("unauthorized: …") ve sayım notuna da uygulanır. Audit satırı değişmez
// (status/errorType zaten vardı); gövde URL/host/token taşımaz (thanos
// scrubEndpoint + hub kimliği yalnız id).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() { registerRoutesExtra("argocd-settings", (*Server).registerArgoCDSettingsRoutes) }

func (s *Server) registerArgoCDSettingsRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/settings/argocd",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getArgoCDSettings)))
	mux.Handle("PUT /api/settings/argocd",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.putArgoCDSettings)))
	mux.Handle("POST /api/settings/argocd/discover",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.discoverArgoCDInstances)))
}

// argocdSettingsSvc — süreç-genelinde tek servis (main.go bağlar).
var argocdSettingsSvc atomic.Pointer[argocd.SettingsService]

// SetArgoCDSettings — main.go: boot'ta yüklenmiş servisi bağlar (her rol).
func SetArgoCDSettings(svc *argocd.SettingsService) { argocdSettingsSvc.Store(svc) }

// argocdSettingsStoreOf — depo seçimi (test dikişi; promqlHistoryStoreOf
// emsali). nil *chstore.Store'u arayüze sarmak nil-olmayan bir arayüz
// üretirdi; açıkça nil döner.
var argocdSettingsStoreOf = func(s *Server) argocd.Store {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// argocdPutMu — v0.10.978 — iyimser ön koşul pod içinde ATOMİK: LoadPersisted →
// ApplyPut (damga karşılaştırması) → SavePersisted tek kilit altında; aksi hâlde
// aynı expectedUpdatedAt'i taşıyan iki eşzamanlı PUT ikisi de 200 alır ve ikinci
// birincinin düzenlemesini sessizce ezerdi (kapatılmak istenen kayıp güncelleme,
// ms penceresine daralmış). Admin yazımı nadir; bir CH gidiş-dönüşü boyunca
// beklemek kabul. Pod'lar arası aynı ms'lik çift yazım son-yazan-kazanır kalır:
// system_settings PutSetting koşulsuz INSERT, compare-and-set yok (put.go (0)).
var argocdPutMu sync.Mutex

// reloadArgoCDSettings — peer sinyali (cache.go reloadConfigOnSignal).
func (s *Server) reloadArgoCDSettings(ctx context.Context) {
	svc, st := argocdSettingsSvc.Load(), argocdSettingsStoreOf(s)
	if svc == nil || st == nil {
		return
	}
	if err := svc.LoadPersisted(ctx, st); err != nil {
		log.Printf("[argocd] reload on signal: %v", err)
	}
}

// argocdClusterRefs — doğrulamanın gördüğü Remote Cluster yüzü: maskeli
// anlık görüntüden (token hiç kopyalanmaz). APIServerURLs lane R2'nin
// (v0.10.956) alanı: https://kubernetes.default.svc yalnız hub kaydında
// (argocd.Validate; cluster_identity.go bu kuralı buraya bırakır).
func (s *Server) argocdClusterRefs() []argocd.ClusterRef {
	if s.thanos == nil {
		return nil
	}
	snap := s.thanos.Snapshot()
	out := make([]argocd.ClusterRef, 0, len(snap.Clusters))
	for _, c := range snap.Clusters {
		out = append(out, argocd.ClusterRef{ID: c.ID, Name: c.Name, Enabled: c.Enabled, APIServerURLs: c.APIServerURLs})
	}
	return out
}

// ── GET / PUT ──────────────────────────────────────────────────────────────

type argocdTokenStatus struct {
	TokenRef string `json:"tokenRef"`
	Resolved bool   `json:"resolved"`
	Error    string `json:"error,omitempty"`
}

type argocdHubInfo struct {
	ID                 string `json:"id"`
	Name               string `json:"name,omitempty"`
	Enabled            bool   `json:"enabled"`
	Found              bool   `json:"found"`
	InjectClusterLabel bool   `json:"injectClusterLabel"` // v0.10.957 — uygulanan (hub başına, §5.6)
}

// argocdSettingsResponse — GET/PUT cevabı. settings = saklanan (PUT'a geri
// gönderilebilir), resolved = uygulanan, tokens = instance id → ref durumu
// (çözülen değer ASLA), hubs = hub kayıtlarının özeti (hubs[] sırasıyla;
// v0.10.957 — iki hub, §5.6).
type argocdSettingsResponse struct {
	Settings argocd.Settings              `json:"settings"`
	Resolved argocd.Settings              `json:"resolved"`
	Defaults argocd.Settings              `json:"defaults"`
	Bounds   map[string]argocd.Bound      `json:"bounds"`
	Tokens   map[string]argocdTokenStatus `json:"tokens"`
	Hubs     []argocdHubInfo              `json:"hubs"`
}

func (s *Server) argocdResponse(svc *argocd.SettingsService) argocdSettingsResponse {
	cur := svc.Current()
	resp := argocdSettingsResponse{Settings: cur, Resolved: cur.Normalized(), Defaults: argocd.DefaultSettings(),
		Bounds: argocd.Bounds(), Tokens: map[string]argocdTokenStatus{}, Hubs: []argocdHubInfo{}}
	for _, inst := range cur.Instances {
		if inst.TokenRef == "" {
			continue
		}
		ok, msg := svc.TokenStatus(inst.ID)
		resp.Tokens[inst.ID] = argocdTokenStatus{TokenRef: inst.TokenRef, Resolved: ok, Error: msg}
	}
	if len(cur.Hubs) > 0 {
		refs := s.argocdClusterRefs()
		for _, hub := range cur.Hubs {
			h := argocdHubInfo{ID: hub.ClusterID, InjectClusterLabel: hub.Inject()}
			for _, c := range refs {
				if c.ID == hub.ClusterID {
					h.Name, h.Enabled, h.Found = c.Name, c.Enabled, true
					break
				}
			}
			resp.Hubs = append(resp.Hubs, h)
		}
	}
	return resp
}

func (s *Server) getArgoCDSettings(w http.ResponseWriter, r *http.Request) {
	svc := argocdSettingsSvc.Load()
	if svc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "argocd settings not wired")
		return
	}
	writeJSON(w, s.argocdResponse(svc))
}

// argocdSettingsMaxBody — PUT gövde tavanı (≤500 instance + ≤500 pin sığar; v0.10.990).
const argocdSettingsMaxBody = 1 << 20

// writeArgoCDFieldError — 400 {error, field}; field JSON alan yoludur
// (argocd.FieldError), UI hatayı ilgili girdinin yanına koyar.
func writeArgoCDFieldError(w http.ResponseWriter, err error) {
	field := ""
	var fe *argocd.FieldError
	if errors.As(err, &fe) {
		field = fe.Path
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error(), "field": field})
}

// putArgoCDSettings — ayrıştır (düz token 400) → doğrula (alan yollu 400;
// hub mevcut Remote Cluster'a karşı) → kalıcı yaz → bu pod'da canlı →
// peer'lara yayınla → audit. Yazım istek iptaline bağlı DEĞİL
// (context.WithoutCancel, promql_console_settings.go emsali).
//
// v0.10.974 — Validate yerine argocd.ApplyPut (put.go: boş tokenRef kayıtlıyı
// korur, istek-yalnız clearTokenRef kaldırır, kayıtlı kimlik değiştirilemez,
// bağlı instance'ı olan hub kaldırılamaz). Birleştirmeden ÖNCE kalıcı blob
// best-effort yeniden yüklenir: B pod'undaki PUT, A pod'unun az önce kaydettiği
// bloba karşı birleşir (≤30 s bayat bellek kopyasına değil); okuma hatası
// loglanır, bellekteki blobla sürülür. Audit details birleşmiş blobdur
// (clearTokenRef taşımaz).
//
// v0.10.978 — iyimser ön koşul (put.go (0)): gövdenin istek-yalnız
// expectedUpdatedAt'i, TAZE yüklenen kalıcı blobun updatedAt'iyle tutmazsa 409
// {error, errorType:"stale", updatedAt:<kayıtlı>} — 400 gibi yazım yok, audit
// yok, canlı ayar değişmez. Gönderilmemişse kabul (API/token çağıranlar, eski
// bundle) ama audit details `"precondition":"none"`; tutmuşsa
// `"precondition":"updatedAt"` + gönderilen `expectedUpdatedAt` (hangi sürümün
// üstüne yazıldığı). Karşılaştırma LoadPersisted'tan SONRA: B pod'unun belleği
// bayatken A pod'unun damgası tutar. LoadPersisted hata verirse ve
// expectedUpdatedAt gönderilmişse 503 {error, errorType:"unavailable"} — yazım
// yok, audit yok (bellekteki bayat bloba karşı doğrulanan ön koşul yanlış audit
// + kayıp yazım olurdu); ön koşulsuz PUT'ta v0.10.974 logla-sür korunur.
// LoadPersisted → ApplyPut → SavePersisted argocdPutMu altında: aynı damgayı
// taşıyan iki eşzamanlı PUT'un ikincisi 409 alır, 200 değil (pod içi; pod'lar
// arası kalan pencere argocdPutMu yorumunda).
func (s *Server) putArgoCDSettings(w http.ResponseWriter, r *http.Request) {
	svc := argocdSettingsSvc.Load()
	if svc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "argocd settings not wired")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, argocdSettingsMaxBody))
	if err != nil {
		writeArgoCDFieldError(w, &argocd.FieldError{Msg: "gövde okunamadı ya da 1 MiB'ı aşıyor"})
		return
	}
	in, opts, err := argocd.ParsePut(raw)
	if err != nil {
		writeArgoCDFieldError(w, err)
		return
	}
	st := argocdSettingsStoreOf(s)
	if st == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "ayar deposu bağlı değil")
		return
	}
	argocdPutMu.Lock() // v0.10.978 — yükle → karşılaştır → yaz tek adım (ayrıştırma 400'leri kilidi almaz)
	defer argocdPutMu.Unlock()
	if err := svc.LoadPersisted(r.Context(), st); err != nil {
		if opts.ExpectedUpdatedAt != nil { // v0.10.978 — ön koşul TAZE bloba karşı doğrulanamaz; bellekteki blob bayat olabilir: kabul = kayıp yazım + yanlış audit
			log.Printf("[argocd] PUT öncesi kalıcı blob yüklenemedi, ön koşul doğrulanamadı: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "ön koşul doğrulanamadı — ayar deposu okunamadı, yeniden deneyin", "errorType": "unavailable"})
			return
		}
		log.Printf("[argocd] PUT öncesi kalıcı blob yüklenemedi, bellekteki blobla sürülüyor: %v", err)
	}
	cfg, err := argocd.ApplyPut(in, opts, svc.Current(), s.argocdClusterRefs())
	if err != nil {
		var se *argocd.StaleError
		if errors.As(err, &se) { // v0.10.978 — bayat taban: 409, FE yeniden yükler
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": se.Error(), "errorType": "stale", "updatedAt": se.Current})
			return
		}
		writeArgoCDFieldError(w, err)
		return
	}
	if err := svc.SavePersisted(context.WithoutCancel(r.Context()), st, cfg); err != nil {
		writeErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "argocd") // peer'lar 30 s poll'ü beklemesin (v0.9.237 sınıfı)
	precondition := "none"                       // v0.10.978 — ön koşulsuz yazım denetimde görünür
	if opts.ExpectedUpdatedAt != nil {
		precondition = "updatedAt"
	}
	details, _ := json.Marshal(struct { // tokenRef referanstır; düz token alanı yok
		argocd.Settings
		Precondition      string `json:"precondition"`
		ExpectedUpdatedAt *int64 `json:"expectedUpdatedAt,omitempty"`
	}{svc.Current(), precondition, opts.ExpectedUpdatedAt})
	s.audit(r, "settings.argocd.update", "settings", argocd.SettingsKey, string(details))
	writeJSON(w, s.argocdResponse(svc))
}

// ── Keşif probe'u ──────────────────────────────────────────────────────────

const (
	// argocdDiscoverMaxValues — iş başına namespace / exported_namespace değer
	// tavanı (v0.10.990: 100 → 500; paylaşılan iş adında namespace = instance).
	argocdDiscoverMaxValues = 500
	// argocdDiscoverMaxPods — shard sayımının `pod` değer tavanı (FE "≥100").
	argocdDiscoverMaxPods     = 100
	argocdDiscoverBudget      = 60 * time.Second
	argocdDiscoverCallTimeout = 15 * time.Second
	argocdDiscoverMaxBodyB    = int64(4 << 20) // label-values gövdesi: yüzlerce değer
	argocdDiscoverWindow      = time.Hour      // §5.4 kural 4: metadata HER ZAMAN pencereli
	argocdDiscoverReqMaxBody  = 4 << 10
	// argocdCountMaxSeries — v0.10.974 — anlık count'un seri tavanı: iş başına
	// en çok argocdDiscoverMaxValues namespace satırı (+1 kesilmeyi görür).
	argocdCountMaxSeries = argocdDiscoverMaxValues + 1
)

// v0.10.990 — iş ve çağrı tavanları + eşzamanlılık. Değişken: testler bütçe
// tükenmesini küçük sayılarla ve sıralı (parallel=1) kipte pinler.
var (
	argocdDiscoverMaxJobs  = 500  // `job` label-values limiti (eski 50)
	argocdDiscoverMaxCalls = 2000 // hub başına toplam çağrı, sayımlar dahil (eski 150)
	// argocdDiscoverParallel — aynı anda hub Thanos'una giden keşif çağrısı.
	// 1 = v0.10.974 sıralı davranışı. Küçük tutulur: pod başına tek keşif
	// kuralı (429) hub'ı korumak içindi; 4 hafif label-values çağrısı o ruhu
	// bozmaz, 500 işi 60 s'ye sığdırır.
	argocdDiscoverParallel = 4
)

// argocdDiscoverBusy — pod başına tek eşzamanlı keşif (≤argocdDiscoverMaxCalls hub çağrısı).
var argocdDiscoverBusy atomic.Bool

// argocdDiscoverNow — pencere saati (testler sabitleyebilir).
var argocdDiscoverNow = time.Now

type argocdDiscoverRequest struct {
	HubClusterID       string `json:"hubClusterId"`
	InjectClusterLabel *bool  `json:"injectClusterLabel"`
}

type argocdDiscoverWindowMs struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type argocdDiscoverResult struct {
	HubClusterID       string                 `json:"hubClusterId"`
	HubName            string                 `json:"hubName"`
	InjectClusterLabel bool                   `json:"injectClusterLabel"`
	Window             argocdDiscoverWindowMs `json:"window"`
	Candidates         []argocd.Candidate     `json:"candidates"`
	JobsTruncated      bool                   `json:"jobsTruncated"`
	Incomplete         bool                   `json:"incomplete,omitempty"` // çağrı/zaman bütçesi doldu
	// CountsIncomplete — v0.10.974 — sayım turu (uygulama/shard) bütçeyi
	// bitirdi; aday listesi TAM, Incomplete bunun için set edilmez.
	CountsIncomplete bool     `json:"countsIncomplete,omitempty"`
	Warnings         []string `json:"warnings,omitempty"` // upstream warnings (URL maskeli)
	Calls            int      `json:"calls"`
	Saved            bool     `json:"saved"` // her zaman false: probe salt-okunur
}

func writeArgoCDGuardrail(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "errorType": "guardrail"})
}

// argocdErrUnauthorized — v0.10.978 — hub Thanos 401/403; sourcestate
// sözlüğünün aynısı (FE HubState 'unauthorized' → "yetki yok").
const argocdErrUnauthorized = string(sourcestate.Unauthorized)

// argocdConsoleErrType — v0.10.978 — ConsoleError türü, 401/403 kod
// önceliğiyle (promapi.HTTPError.Unwrap gibi kod kazanır; thanos bunları
// "unavailable" diye normalize eder). Aday hatası öneki, sayım notu ve
// yanıt errorType'ı hep buradan geçer.
func argocdConsoleErrType(ce *thanos.ConsoleError) string {
	if ce.UpstreamStatus == http.StatusUnauthorized || ce.UpstreamStatus == http.StatusForbidden {
		return argocdErrUnauthorized
	}
	return ce.Type
}

// argocdDiscoverError — keşif hata gövdesi. v0.10.978 — upstreamStatus hub
// Thanos'un HTTP kodu (0 = yanıt alınamadı, atlanır); hubClusterId probe
// edilen hub'ın KİMLİĞİ (URL/host/token asla).
type argocdDiscoverError struct {
	Error          string `json:"error"`
	ErrorType      string `json:"errorType"`
	UpstreamStatus int    `json:"upstreamStatus,omitempty"`
	HubClusterID   string `json:"hubClusterId,omitempty"`
}

// argocdDiscoverFailure — hata → (kod, gövde). ConsoleError mesajı URL/host
// içermez (thanos scrubEndpoint); tanınmayan hata ASLA ham yankılanmaz
// (yalnız logda). v0.10.978 — 401/403 → 502 + "unauthorized" (dosya başlığı).
func argocdDiscoverFailure(err error) (int, argocdDiscoverError) {
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		body := argocdDiscoverError{Error: ce.Message, ErrorType: argocdConsoleErrType(ce), UpstreamStatus: ce.UpstreamStatus}
		if body.ErrorType == argocdErrUnauthorized {
			return http.StatusBadGateway, body
		}
		return ce.StatusCode(), body
	}
	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, argocdDiscoverError{ErrorType: thanos.ConsoleErrCanceled}
	}
	log.Printf("[argocd] keşif: beklenmeyen hata: %v", err)
	return http.StatusBadGateway, argocdDiscoverError{Error: "hub query failed", ErrorType: thanos.ConsoleErrInternal}
}

func (s *Server) discoverArgoCDInstances(w http.ResponseWriter, r *http.Request) {
	svc := argocdSettingsSvc.Load()
	if svc == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "argocd settings not wired")
		return
	}
	var req argocdDiscoverRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, argocdDiscoverReqMaxBody))
	if err != nil {
		writeArgoCDGuardrail(w, "gövde okunamadı ya da çok büyük")
		return
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeArgoCDGuardrail(w, "geçersiz JSON: "+err.Error())
			return
		}
	}
	cur := svc.Current()
	hubID := strings.TrimSpace(req.HubClusterID)
	if hubID == "" {
		switch len(cur.Hubs) {
		case 0:
			writeArgoCDGuardrail(w, "hub Remote Cluster seçilmedi (hubClusterId) — önce Argo CD hub'ını seçin")
			return
		case 1:
			hubID = cur.Hubs[0].ClusterID
		default:
			// v0.10.957 — keşif hub başına (§5.6): hangi hub belirsizse istek YOK.
			writeArgoCDGuardrail(w, "birden çok hub kayıtlı — keşif hub başına koşar; gövdede hubClusterId seçin")
			return
		}
	}
	inject := true // listede olmayan hub (kaydetmeden önce deneme): bugünkü davranış
	if h, ok := cur.HubByID(hubID); ok {
		inject = h.Inject()
	}
	if req.InjectClusterLabel != nil {
		inject = *req.InjectClusterLabel
	}
	if s.thanos == nil {
		writeArgoCDGuardrail(w, "Remote Cluster yapılandırılmamış")
		return
	}
	hub, ok := s.thanos.ClusterByID(hubID)
	if !ok {
		writeArgoCDGuardrail(w, "hub Remote Cluster bilinmiyor ya da devre dışı: "+hubID)
		return
	}
	for _, c := range s.thanos.Snapshot().Clusters {
		if c.ID == hubID && c.TokenRef != "" && !c.TokenResolved {
			writeArgoCDGuardrail(w, "hub Remote Cluster tokenRef'i çözülemedi — kimliksiz istek gönderilmez")
			return
		}
	}
	if !argocdDiscoverBusy.CompareAndSwap(false, true) {
		writeJSONError(w, http.StatusTooManyRequests, "bir Argo CD keşfi zaten koşuyor")
		return
	}
	defer argocdDiscoverBusy.Store(false)
	if !inject {
		hub.ThanosLabelName, hub.ThanosLabelValue = "", ""
	}
	ctx, cancel := context.WithTimeout(r.Context(), argocdDiscoverBudget)
	defer cancel()
	res, err := s.runArgoCDDiscovery(ctx, hub, inject, cur.Instances)

	status, fail := http.StatusOK, argocdDiscoverError{}
	if err != nil {
		status, fail = argocdDiscoverFailure(err)
		fail.HubClusterID = hubID // v0.10.978 — yalnız kimlik; URL/host/token değil
	}
	// Probe gerçekten koştu (korkuluklar geçildi): başarılı ya da değil audit.
	details, _ := json.Marshal(map[string]any{"hubClusterId": hubID, "injectClusterLabel": inject,
		"candidates": len(res.Candidates), "calls": res.Calls, "status": status, "errorType": fail.ErrorType,
		"countsIncomplete": res.CountsIncomplete}) // v0.10.974
	s.audit(r, "settings.argocd.discover", "settings", argocd.SettingsKey, string(details))
	switch {
	case err == nil:
		writeJSON(w, res)
	case status == statusClientClosedRequest:
		w.WriteHeader(status) // istemci gitti: gövdesiz 499 (writeErr v0.7.13 duruşu)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(fail)
	}
}

// runArgoCDDiscovery — hub'a sınırlı label-values turu. İş listesi
// alınamazsa hata (çağıran eşler; sonuç yine döner — audit çağrı sayısını
// görür); tek bir işin sorgusu düşerse o aday Error taşır ve tur sürer.
// Bütçe (çağrı sayısı / bağlam) biterse kalan işler "atlandı" hatasıyla
// döner ve sonuç Incomplete işaretlenir. hub'ın küme etiketi, inject=false
// ise çağıran tarafından zaten temizlenmiştir. v0.10.974 — adaylar kurulunca
// aynı bütçeyle sayım turu (argocdCountPass).
func (s *Server) runArgoCDDiscovery(ctx context.Context, hub thanos.ClusterConfig, inject bool, existing []argocd.Instance) (*argocdDiscoverResult, error) {
	end := argocdDiscoverNow()
	start := end.Add(-argocdDiscoverWindow)
	lim := thanos.ConsoleLimits{Timeout: argocdDiscoverCallTimeout, MaxBodyBytes: argocdDiscoverMaxBodyB, PartialResponse: false}
	res := &argocdDiscoverResult{HubClusterID: hub.EffectiveID(), HubName: hub.Name, InjectClusterLabel: inject,
		Window: argocdDiscoverWindowMs{Start: start.UnixMilli(), End: end.UnixMilli()}, Candidates: []argocd.Candidate{}}
	run := &argocdProbeRun{ctx: ctx, res: res, warns: map[string]bool{}}
	// values — tek label-values çağrısı. Çağrı SAYMAZ: bütçe çağırandadır
	// (run.take), böylece eşzamanlı işler tavanı aşamaz.
	values := func(label, sel string, limit int) (*thanos.ConsoleLabelsResult, error) {
		out, err := s.thanos.ConsoleLabelValues(ctx, hub, label,
			thanos.ConsoleMetaQuery{Match: []string{sel}, Start: start, End: end, Limit: limit}, lim)
		if err == nil {
			run.warn(out.Warnings)
		}
		return out, err
	}

	res.Calls++ // iş listesi çağrısı bütçeden bağımsız atılır (tur onsuz başlayamaz)
	jobs, err := values("job", argocd.AppInfoSelector("", ""), argocdDiscoverMaxJobs)
	if err != nil {
		return res, err
	}
	res.JobsTruncated = jobs.Truncated
	names := make([]string, 0, len(jobs.Values))
	for _, job := range jobs.Values {
		if job != "" {
			names = append(names, job)
		}
	}
	// v0.10.990 — iş başına probe eşzamanlı (argocdDiscoverParallel); sonuç
	// dizine yazılır, yani aday sırası iş listesinin sırası olarak kalır.
	const skipped = "skipped: discovery budget exhausted"
	probes := make([]argocd.JobProbe, len(names))
	argocdForEach(len(names), argocdDiscoverParallel, func(i int) {
		job := names[i]
		p := argocd.JobProbe{Job: job}
		defer func() { probes[i] = p }()
		if !run.take(2) {
			p.Err = skipped
			run.markIncomplete()
			return
		}
		ns, err := values("namespace", argocd.AppInfoSelector(job, ""), argocdDiscoverMaxValues)
		if err != nil {
			run.giveBack(1) // ikinci çağrı atılmadı
			p.Err = argocdProbeErrText(err)
			return
		}
		ex, err := values("exported_namespace", argocd.AppInfoSelector(job, ""), argocdDiscoverMaxValues)
		if err != nil {
			p.Err = argocdProbeErrText(err)
			return
		}
		p.Namespaces, p.Truncated = ns.Values, ns.Truncated || ex.Truncated
		if len(ex.Values) == 0 {
			return
		}
		p.Exported = map[string][]string{}
		if len(ns.Values) == 1 {
			p.Exported[ns.Values[0]] = ex.Values
			return
		}
		// Paylaşılan iş adı: namespace başına exported_namespace.
		for _, n := range ns.Values {
			if !run.take(1) {
				p.Err = skipped
				run.markIncomplete()
				return
			}
			exn, err := values("exported_namespace", argocd.AppInfoSelector(job, n), argocdDiscoverMaxValues)
			if err != nil {
				p.Err = argocdProbeErrText(err)
				return
			}
			p.Exported[n] = exn.Values
			p.Truncated = p.Truncated || exn.Truncated
		}
	})
	res.Candidates = argocd.BuildCandidates(res.HubClusterID, probes, existing)
	if res.Candidates == nil {
		res.Candidates = []argocd.Candidate{}
	}
	s.argocdCountPass(ctx, hub, end, run, values)
	for w := range run.warns {
		res.Warnings = append(res.Warnings, w)
	}
	sort.Strings(res.Warnings)
	return res, nil
}

// argocdProbeRun — v0.10.990 — bir keşif turunun paylaşılan durumu: çağrı
// bütçesi (res.Calls), Incomplete / CountsIncomplete bayrakları ve upstream
// uyarıları. İş başına probe'lar eşzamanlı koştuğu için hepsi mu altında.
type argocdProbeRun struct {
	mu    sync.Mutex
	ctx   context.Context
	res   *argocdDiscoverResult
	warns map[string]bool
}

// take — n çağrılık bütçe ayırır; bağlam bittiyse ya da tavan (≤
// argocdDiscoverMaxCalls) aşılacaksa false ve HİÇBİR ŞEY ayrılmaz. Denetim
// ile sayım tek kilit altında: eşzamanlı işler tavanı aşamaz.
func (p *argocdProbeRun) take(n int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ctx.Err() != nil || p.res.Calls+n > argocdDiscoverMaxCalls {
		return false
	}
	p.res.Calls += n
	return true
}

// giveBack — ayrılıp ATILMAYAN çağrıyı bütçeye geri verir (calls = gerçekten
// giden istek sayısı; audit ve testler bunu okur).
func (p *argocdProbeRun) giveBack(n int) {
	p.mu.Lock()
	p.res.Calls -= n
	p.mu.Unlock()
}

func (p *argocdProbeRun) warn(ws []string) {
	if len(ws) == 0 {
		return
	}
	p.mu.Lock()
	for _, w := range ws {
		p.warns[w] = true
	}
	p.mu.Unlock()
}

func (p *argocdProbeRun) markIncomplete() {
	p.mu.Lock()
	p.res.Incomplete = true
	p.mu.Unlock()
}

func (p *argocdProbeRun) markCountsIncomplete() {
	p.mu.Lock()
	p.res.CountsIncomplete = true
	p.mu.Unlock()
}

// argocdForEach — fn(0..n-1), en çok `parallel` eşzamanlı. İşler DİZİN
// SIRASIYLA başlatılır (yuva boşaldıkça), yani bütçe önce baştaki işlere
// gider; parallel ≤ 1 tam sıralı (v0.10.974 davranışı).
func argocdForEach(n, parallel int, fn func(i int)) {
	if parallel <= 1 || n <= 1 {
		for i := 0; i < n; i++ {
			fn(i)
		}
		return
	}
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			fn(i)
		}(i)
	}
	wg.Wait()
}

// argocdCountPass — v0.10.974 — BE2 ikinci tur (dosya başlığı): hatasız
// adaylar iş başına (argocd.PlanCounts) bir anlık count + kapsam başına bir
// `pod` label-values. Her çağrı res.Calls'a sayılır ve AYNI bütçeye (run.take:
// ≤argocdDiscoverMaxCalls çağrı, 60 s bağlam) tabidir; adaylar zaten kurulu
// olduğundan sayım hiçbir adaya mal olamaz. Başarısız sayım yalnız CountNote
// yazar (error, incomplete, 200 dokunulmaz); bütçe/bağlam biterse kalan adaylar
// CountNoteBudget ve sonuç CountsIncomplete alır. Hub küme etiketi inject=false
// ise çağıran tarafından zaten temizlenmiştir (count da etiketsiz koşar).
// v0.10.990 — işler eşzamanlı (argocdDiscoverParallel): her iş yalnız KENDİ
// adaylarının dizinlerine yazar (PlanCounts işleri ayrık böler), yani aday
// dilimi kilitsiz; paylaşılan durum (bütçe, bayrak, uyarı) run'da.
func (s *Server) argocdCountPass(ctx context.Context, hub thanos.ClusterConfig, end time.Time, run *argocdProbeRun,
	values func(label, sel string, limit int) (*thanos.ConsoleLabelsResult, error)) {
	cands := run.res.Candidates
	qlim := thanos.ConsoleLimits{Timeout: argocdDiscoverCallTimeout, MaxSeries: argocdCountMaxSeries,
		MaxBodyBytes: argocdDiscoverMaxBodyB, PartialResponse: false}
	skip := func(idx []int) {
		argocd.NoteCounts(cands, idx, argocd.CountNoteBudget)
		run.markCountsIncomplete()
	}
	// fail — sayım hatası: bağlam bittiyse bütçe (atlandı), değilse kısa ve
	// URL'siz neden (ConsoleError.Type; tanınmayan hata yalnız logda).
	fail := func(idx []int, err error, note func(string) string) {
		if ctx.Err() != nil {
			skip(idx)
			return
		}
		why := thanos.ConsoleErrInternal
		var ce *thanos.ConsoleError
		if errors.As(err, &ce) {
			why = argocdConsoleErrType(ce) // v0.10.978 — 401/403 → unauthorized
		} else {
			log.Printf("[argocd] keşif sayımı: beklenmeyen hata: %v", err)
		}
		argocd.NoteCounts(cands, idx, note(why))
	}
	jobs := argocd.PlanCounts(cands)
	argocdForEach(len(jobs), argocdDiscoverParallel, func(k int) {
		j := jobs[k]
		if !run.take(1) {
			skip(j.Idx)
			return
		}
		qr, err := s.thanos.ConsoleQuery(ctx, hub, thanos.ConsoleInstantQuery{Query: argocd.AppCountQuery(j.Job, j.JobWide), Time: end}, qlim)
		switch {
		case err != nil:
			fail(j.Idx, err, argocd.AppCountFailNote)
		case qr.ResultType != "vector":
			argocd.NoteCounts(cands, j.Idx, argocd.AppCountFailNote("beklenmeyen sonuç türü "+qr.ResultType))
		default:
			run.warn(qr.Warnings)
			byNS, perr := argocd.ParseCountVector(qr.Result)
			if perr != nil {
				log.Printf("[argocd] keşif sayımı %s: %v", j.Job, perr)
				argocd.NoteCounts(cands, j.Idx, argocd.AppCountFailNote("bozuk yanıt"))
				break
			}
			argocd.ApplyAppCounts(cands, j, byNS, qr.Truncated)
		}
		for _, sc := range j.Shards {
			if !run.take(1) {
				skip(sc.Idx)
				continue
			}
			pods, err := values("pod", argocd.AppInfoSelector(sc.Job, sc.Namespace), argocdDiscoverMaxPods)
			if err != nil {
				fail(sc.Idx, err, argocd.ShardCountFailNote)
				continue
			}
			argocd.ApplyShardCount(cands, sc, len(pods.Values), pods.Truncated)
		}
	})
}

// argocdProbeErrText — aday başına hata metni (URL'siz; ConsoleError
// mesajı zaten maskeli). v0.10.978 — önek 401/403'te "unauthorized".
func argocdProbeErrText(err error) string {
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		return argocdConsoleErrType(ce) + ": " + ce.Message
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "skipped: discovery budget exhausted"
	}
	log.Printf("[argocd] keşif iş sorgusu: %v", err)
	return "hub query failed"
}
