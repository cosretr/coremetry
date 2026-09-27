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
// Sınırlar: ≤50 iş, iş başına ≤100 değer, ≤150 çağrı (sayımlar DAHİL), çağrı
// başına 15 s, toplam 60 s; pod başına tek eşzamanlı keşif (429). Sayım
// yalnız ARTAN bütçeyi kullanır: aday düşürmez, error/incomplete üretmez,
// 200'ü bozmaz; bütçe biterse kalan adaylara not + countsIncomplete.
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
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
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

// argocdSettingsMaxBody — PUT gövde tavanı (≤100 instance + ≤500 pin sığar).
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
	if err := svc.LoadPersisted(r.Context(), st); err != nil {
		log.Printf("[argocd] PUT öncesi kalıcı blob yüklenemedi, bellekteki blobla sürülüyor: %v", err)
	}
	cfg, err := argocd.ApplyPut(in, opts, svc.Current(), s.argocdClusterRefs())
	if err != nil {
		writeArgoCDFieldError(w, err)
		return
	}
	if err := svc.SavePersisted(context.WithoutCancel(r.Context()), st, cfg); err != nil {
		writeErr(w, err)
		return
	}
	s.publishConfigReload(r.Context(), "argocd") // peer'lar 30 s poll'ü beklemesin (v0.9.237 sınıfı)
	details, _ := json.Marshal(svc.Current())    // tokenRef referanstır; düz token alanı yok
	s.audit(r, "settings.argocd.update", "settings", argocd.SettingsKey, string(details))
	writeJSON(w, s.argocdResponse(svc))
}

// ── Keşif probe'u ──────────────────────────────────────────────────────────

const (
	argocdDiscoverMaxJobs     = 50
	argocdDiscoverMaxValues   = 100
	argocdDiscoverMaxCalls    = 150
	argocdDiscoverBudget      = 60 * time.Second
	argocdDiscoverCallTimeout = 15 * time.Second
	argocdDiscoverMaxBodyB    = int64(4 << 20) // label-values gövdesi: onlarca değer
	argocdDiscoverWindow      = time.Hour      // §5.4 kural 4: metadata HER ZAMAN pencereli
	argocdDiscoverReqMaxBody  = 4 << 10
	// argocdCountMaxSeries — v0.10.974 — anlık count'un seri tavanı: iş başına
	// en çok argocdDiscoverMaxValues namespace satırı (+1 kesilmeyi görür).
	argocdCountMaxSeries = argocdDiscoverMaxValues + 1
)

// argocdDiscoverBusy — pod başına tek eşzamanlı keşif (≤150 hub çağrısı).
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

// argocdDiscoverFailure — hata → (kod, tür, mesaj). ConsoleError mesajı
// URL/host içermez (thanos scrubEndpoint); tanınmayan hata ASLA ham
// yankılanmaz (yalnız logda).
func argocdDiscoverFailure(err error) (int, string, string) {
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		return ce.StatusCode(), ce.Type, ce.Message
	}
	if errors.Is(err, context.Canceled) {
		return statusClientClosedRequest, thanos.ConsoleErrCanceled, ""
	}
	log.Printf("[argocd] keşif: beklenmeyen hata: %v", err)
	return http.StatusBadGateway, thanos.ConsoleErrInternal, "hub query failed"
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

	status, errType, msg := http.StatusOK, "", ""
	if err != nil {
		status, errType, msg = argocdDiscoverFailure(err)
	}
	// Probe gerçekten koştu (korkuluklar geçildi): başarılı ya da değil audit.
	details, _ := json.Marshal(map[string]any{"hubClusterId": hubID, "injectClusterLabel": inject,
		"candidates": len(res.Candidates), "calls": res.Calls, "status": status, "errorType": errType,
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
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "errorType": errType})
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
	warns := map[string]bool{}
	values := func(label, sel string, limit int) (*thanos.ConsoleLabelsResult, error) {
		res.Calls++
		out, err := s.thanos.ConsoleLabelValues(ctx, hub, label,
			thanos.ConsoleMetaQuery{Match: []string{sel}, Start: start, End: end, Limit: limit}, lim)
		if err == nil {
			for _, w := range out.Warnings {
				warns[w] = true
			}
		}
		return out, err
	}
	budgetLeft := func(need int) bool { return ctx.Err() == nil && res.Calls+need <= argocdDiscoverMaxCalls }

	jobs, err := values("job", argocd.AppInfoSelector("", ""), argocdDiscoverMaxJobs)
	if err != nil {
		return res, err
	}
	res.JobsTruncated = jobs.Truncated
	probes := make([]argocd.JobProbe, 0, len(jobs.Values))
	for _, job := range jobs.Values {
		if job == "" {
			continue
		}
		p := argocd.JobProbe{Job: job}
		if !budgetLeft(2) {
			p.Err, res.Incomplete = "skipped: discovery budget exhausted", true
			probes = append(probes, p)
			continue
		}
		ns, err := values("namespace", argocd.AppInfoSelector(job, ""), argocdDiscoverMaxValues)
		if err != nil {
			p.Err = argocdProbeErrText(err)
			probes = append(probes, p)
			continue
		}
		ex, err := values("exported_namespace", argocd.AppInfoSelector(job, ""), argocdDiscoverMaxValues)
		if err != nil {
			p.Err = argocdProbeErrText(err)
			probes = append(probes, p)
			continue
		}
		p.Namespaces, p.Truncated = ns.Values, ns.Truncated || ex.Truncated
		if len(ex.Values) > 0 {
			p.Exported = map[string][]string{}
			if len(ns.Values) == 1 {
				p.Exported[ns.Values[0]] = ex.Values
			} else {
				// Paylaşılan iş adı: namespace başına exported_namespace.
				for _, n := range ns.Values {
					if !budgetLeft(1) {
						p.Err, res.Incomplete = "skipped: discovery budget exhausted", true
						break
					}
					exn, err := values("exported_namespace", argocd.AppInfoSelector(job, n), argocdDiscoverMaxValues)
					if err != nil {
						p.Err = argocdProbeErrText(err)
						break
					}
					p.Exported[n] = exn.Values
					p.Truncated = p.Truncated || exn.Truncated
				}
			}
		}
		probes = append(probes, p)
	}
	res.Candidates = argocd.BuildCandidates(res.HubClusterID, probes, existing)
	if res.Candidates == nil {
		res.Candidates = []argocd.Candidate{}
	}
	s.argocdCountPass(ctx, hub, end, res, values, budgetLeft, warns)
	for w := range warns {
		res.Warnings = append(res.Warnings, w)
	}
	sort.Strings(res.Warnings)
	return res, nil
}

// argocdCountPass — v0.10.974 — BE2 ikinci tur (dosya başlığı): hatasız
// adaylar iş başına (argocd.PlanCounts) bir anlık count + kapsam başına bir
// `pod` label-values. Her çağrı res.Calls'a sayılır ve AYNI budgetLeft'e
// (≤150 çağrı, 60 s bağlam) tabidir; adaylar zaten kurulu olduğundan sayım
// hiçbir adaya mal olamaz. Başarısız sayım yalnız CountNote yazar (error,
// incomplete, 200 dokunulmaz); bütçe/bağlam biterse kalan adaylar
// CountNoteBudget ve sonuç CountsIncomplete alır. Hub küme etiketi inject=false
// ise çağıran tarafından zaten temizlenmiştir (count da etiketsiz koşar).
func (s *Server) argocdCountPass(ctx context.Context, hub thanos.ClusterConfig, end time.Time, res *argocdDiscoverResult,
	values func(label, sel string, limit int) (*thanos.ConsoleLabelsResult, error), budgetLeft func(int) bool, warns map[string]bool) {
	cands := res.Candidates
	qlim := thanos.ConsoleLimits{Timeout: argocdDiscoverCallTimeout, MaxSeries: argocdCountMaxSeries,
		MaxBodyBytes: argocdDiscoverMaxBodyB, PartialResponse: false}
	skip := func(idx []int) {
		argocd.NoteCounts(cands, idx, argocd.CountNoteBudget)
		res.CountsIncomplete = true
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
			why = ce.Type
		} else {
			log.Printf("[argocd] keşif sayımı: beklenmeyen hata: %v", err)
		}
		argocd.NoteCounts(cands, idx, note(why))
	}
	for _, j := range argocd.PlanCounts(cands) {
		if !budgetLeft(1) {
			skip(j.Idx)
			continue
		}
		res.Calls++
		qr, err := s.thanos.ConsoleQuery(ctx, hub, thanos.ConsoleInstantQuery{Query: argocd.AppCountQuery(j.Job, j.JobWide), Time: end}, qlim)
		switch {
		case err != nil:
			fail(j.Idx, err, argocd.AppCountFailNote)
		case qr.ResultType != "vector":
			argocd.NoteCounts(cands, j.Idx, argocd.AppCountFailNote("beklenmeyen sonuç türü "+qr.ResultType))
		default:
			for _, w := range qr.Warnings {
				warns[w] = true
			}
			byNS, perr := argocd.ParseCountVector(qr.Result)
			if perr != nil {
				log.Printf("[argocd] keşif sayımı %s: %v", j.Job, perr)
				argocd.NoteCounts(cands, j.Idx, argocd.AppCountFailNote("bozuk yanıt"))
				break
			}
			argocd.ApplyAppCounts(cands, j, byNS, qr.Truncated)
		}
		for _, sc := range j.Shards {
			if !budgetLeft(1) {
				skip(sc.Idx)
				continue
			}
			pods, err := values("pod", argocd.AppInfoSelector(sc.Job, sc.Namespace), argocdDiscoverMaxValues)
			if err != nil {
				fail(sc.Idx, err, argocd.ShardCountFailNote)
				continue
			}
			argocd.ApplyShardCount(cands, sc, len(pods.Values), pods.Truncated)
		}
	}
}

// argocdProbeErrText — aday başına hata metni (URL'siz; ConsoleError
// mesajı zaten maskeli).
func argocdProbeErrText(err error) string {
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		return ce.Type + ": " + ce.Message
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "skipped: discovery budget exhausted"
	}
	log.Printf("[argocd] keşif iş sorgusu: %v", err)
	return "hub query failed"
}
