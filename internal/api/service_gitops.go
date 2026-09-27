package api

// service_gitops.go — v0.10.981 — servis sayfasının GitOps sekmesi
// (Rollouts v2 P3.5'in metrics-only iskeleti; operatör, 2026-09-27: "o
// sekmede rolloutları argocd app info metriklerinden ve rollout
// sayfasından anlasın").
//
//	GET /api/services/{name}/gitops   viewer — iş yükleri + Argo uygulamaları + rollout'lar
//
// Üç kaynak TEK cevapta, çünkü üçü de aynı iş yükü kümesine bağlı:
//  1. İş yükleri: servisin son 24 saatte span ürettiği (cluster, ns,
//     workload) — workload_revision_activity_1m (RolloutRefsForService;
//     MV, raw spans değil). Span cluster değeri → Remote Cluster
//     EffectiveID (rollout_causes.go resolveClusterID ile aynı kural).
//  2. Rollout'lar: o iş yüklerinin son 7 gündeki workload_rollouts satırları
//     (Rollouts sayfasının satır şekli; RolloutsForWorkloads). Bayrak
//     kapalıysa bölüm "kapalı" der, sekme yine açılır.
//  3. Argo: her hub'ın Thanos'unda argocd_app_info — servisin
//     namespace'lerine deploy eden + pinli adlar (topk 50, kopyalar
//     katlanmış, §5.4) ve eşlenenlerin 24 saatlik senkron fazları
//     (argocd_app_sync_total). Eşleme saf: argocd/service_view.go.
//
// NEDEN işçi değil istek anında: P3.1 (argocd-metrics işçisi) ve P3.2
// (mapper) §11 H/N sonuçlarını bekliyor; operatör sekmeyi o zamana kadar
// pinlerle görmek istedi. Maliyet sınırlı: hub başına ≤ 2 anlık sorgu,
// hub ≤ 8 (argocd maxHubs, bugün 2), 60 s önbellek, 25 s toplam bütçe.
// Mapper gelince (3) onun tablosundan okur; cevap şekli aynı kalır.
//
// NEDEN ayrı rota, /api/services/{name}/rollouts değil: o uç pod-churn
// tabanlı eski yüzey (P6'da emekli); bu sekme KSM/reconciler satırını
// (workload_rollouts) okur — Rollouts sayfasıyla aynı kayıt.
//
// v0.10.984 (Rollouts v2 P2.3): rollouts.source="v2" iken (2) rollout_events
// okur (RolloutV2ForWorkloads); satır şekli aynı (RolloutRow + V2 alanları).
//
// v0.10.985 (Rollouts v2 P3.2): argocd-metrics işçisi açıkken (MetricsActive:
// enabled + metricsWorker.enabled + ≥1 hub) ve servisin (cluster, ns)
// çiftlerinde taze eşleme kenarı varken (3) hub Thanos'una GİTMEZ:
// argocd_app_mapping + argocd_app_status son satırı + 24 sa 'sync' satırları
// (service_gitops_mapper.go). Aksi hâlde — bayrak kapalı (varsayılan), kenar
// yok ya da CH okuması düştü — v0.10.981 canlı yolu AYNEN. Cevap şekli aynı +
// argo.source "mapper" | "live".
//
// Hata duruşu: bir hub'ın hatası yalnız o hub'ın satırına yazılır (URL/
// token yankılanmaz — argocdProbeErrText), rollout okuma hatası yalnız
// rollout bölümüne not düşer; iş yükü sorgusu düşerse uç hata döner
// (öteki iki bölüm ona bağlı).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func (s *Server) registerServiceGitOpsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/services/{name}/gitops", s.getServiceGitOps)
}

func init() { registerRoutesExtra("service-gitops", (*Server).registerServiceGitOpsRoutes) }

const (
	serviceGitOpsTTL            = 60 * time.Second
	serviceGitOpsWorkloadWindow = 24 * time.Hour
	serviceGitOpsRolloutWindow  = 7 * 24 * time.Hour
	serviceGitOpsBudget         = 25 * time.Second
	serviceGitOpsCallTimeout    = 15 * time.Second
	serviceGitOpsMaxBody        = 4 << 20
	serviceGitOpsRolloutsCap    = 200 // rolloutsForWorkloadsMax (chstore)
	// serviceGitOpsSyncMaxSeries — senkron sorgusu uygulama × faz seri
	// döndürür (≤ 50 uygulama × 5 faz); kesilirse sayım hiç yazılmaz.
	serviceGitOpsSyncMaxSeries = argocd.ServiceAppCap * 2 * 5
)

// serviceGitOpsNow — testler sabitler.
var serviceGitOpsNow = time.Now

type serviceGitOpsWorkload struct {
	argocd.ServiceWorkload
	ClusterName string `json:"clusterName,omitempty"`
}

type serviceGitOpsHub struct {
	HubClusterID string `json:"hubClusterId"`
	HubName      string `json:"hubName,omitempty"`
	Status       string `json:"status"` // ok | error | skipped
	Error        string `json:"error,omitempty"`
	Apps         int    `json:"apps"`
	Truncated    bool   `json:"truncated,omitempty"`
}

type serviceGitOpsArgo struct {
	Configured bool   `json:"configured"`
	Note       string `json:"note,omitempty"`
	// Source — v0.10.985: uygulamaların kaynağı; "mapper" (argocd_app_mapping)
	// | "live" (istek anında hub Thanos'u). Argo aranmadıysa boş.
	Source           string              `json:"source,omitempty"`
	Hubs             []serviceGitOpsHub  `json:"hubs"`
	Apps             []argocd.ServiceApp `json:"apps"`
	OtherInNamespace int                 `json:"otherInNamespace"`
}

type serviceGitOpsRollouts struct {
	Enabled bool                 `json:"enabled"`
	Note    string               `json:"note,omitempty"`
	Rows    []chstore.RolloutRow `json:"rows"`
	Capped  bool                 `json:"capped,omitempty"`
}

type serviceGitOpsResponse struct {
	Service          string                  `json:"service"`
	From             int64                   `json:"rolloutsFrom"`  // ms
	WorkloadsFrom    int64                   `json:"workloadsFrom"` // ms
	To               int64                   `json:"to"`            // ms
	Workloads        []serviceGitOpsWorkload `json:"workloads"`
	WorkloadsCapped  bool                    `json:"workloadsCapped,omitempty"`
	UnmappedClusters []string                `json:"unmappedClusters,omitempty"`
	Argo             serviceGitOpsArgo       `json:"argo"`
	Rollouts         serviceGitOpsRollouts   `json:"rollouts"`
}

func (s *Server) getServiceGitOps(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "service name required")
		return
	}
	now := serviceGitOpsNow().UTC().Truncate(time.Minute)
	svc := argocdSettingsSvc.Load()
	var argoVer int64
	var mapperOn bool
	if svc != nil {
		cur := svc.Current()
		argoVer, mapperOn = cur.UpdatedAt, argocd.MetricsActive(cur)
	}
	cm := s.serviceGitOpsClusterMaps()
	rolloutsOn := s.rolloutCfg != nil && s.rolloutCfg.Resolved().Enabled
	src := s.rolloutSource()
	// Girdiler (hepsi cevabı değiştirir): servis, dakika ızgarası (pencere
	// ondan türer), Argo blob sürümü (pin/hub), Rollouts bayrağı + okuma
	// kaynağı (v0.10.984), Argo kaynak kararının bayrağı (v0.10.985:
	// metricsWorker — blob sürümünde de var ama karar girdisi açıkça
	// anahtarda) ve Remote Cluster eşlemeleri (span değeri, API server URL,
	// ad, token durumu). Tablodaki kenar varlığı veri: 60 s TTL taşır.
	key := fmt.Sprintf("service-gitops:svc=%s:t=%d:argo=%d:mw=%t:ro=%t:src=%s:cl=%s", name, now.Unix(), argoVer, mapperOn, rolloutsOn, src, cm.digest())
	s.serveCached(w, r, key, serviceGitOpsTTL, func(ctx context.Context) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, serviceGitOpsBudget)
		defer cancel()
		return s.buildServiceGitOps(ctx, name, now, svc, cm, rolloutsOn, src)
	})
}

// serviceGitOpsClusters — Remote Cluster kayıtlarından span değeri → id,
// id → ad, normalleşmiş API server URL → id.
type serviceGitOpsClusters struct {
	bySpan   map[string]string
	names    map[string]string
	byServer map[string]string
	tokenBad map[string]bool
}

// digest — önbellek anahtarı parçası: dört haritanın sırasız özeti.
func (c serviceGitOpsClusters) digest() string {
	var parts []string
	for k, v := range c.bySpan {
		parts = append(parts, "s="+k+"\x01"+v)
	}
	for k, v := range c.names {
		parts = append(parts, "n="+k+"\x01"+v)
	}
	for k, v := range c.byServer {
		parts = append(parts, "u="+k+"\x01"+v)
	}
	for k := range c.tokenBad {
		parts = append(parts, "t="+k)
	}
	return correlateKeyDigest(parts...)
}

func (s *Server) serviceGitOpsClusterMaps() serviceGitOpsClusters {
	out := serviceGitOpsClusters{bySpan: map[string]string{}, names: map[string]string{}, byServer: map[string]string{}, tokenBad: map[string]bool{}}
	if s.thanos == nil {
		return out
	}
	for _, c := range s.thanos.Snapshot().Clusters {
		out.names[c.ID] = c.Name
		if c.TokenRef != "" && !c.TokenResolved {
			out.tokenBad[c.ID] = true
		}
		cc, ok := s.thanos.ClusterByID(c.ID)
		if !ok {
			continue // devre dışı: span değeri eşlenmez (rollout satırı da yazılmaz)
		}
		for _, v := range cc.SpanClusterKeys() {
			out.bySpan[v] = c.ID
		}
		for _, u := range c.APIServerURLs {
			out.byServer[u] = c.ID
		}
	}
	return out
}

// serviceGitOpsWorkloads — SAF: MV referansları → tekil iş yükleri
// (EffectiveID) + eşlenemeyen span cluster değerleri. bySpan boşsa (küme
// tanımsız / tek küme) değer olduğu gibi geçer (resolveClusterID kuralı).
func serviceGitOpsWorkloads(refs []chstore.WorkloadRevisionRef, bySpan map[string]string) ([]argocd.ServiceWorkload, []string) {
	seen := map[argocd.ServiceWorkload]bool{}
	unm := map[string]bool{}
	var out []argocd.ServiceWorkload
	for _, r := range refs {
		if r.Workload == "" {
			continue
		}
		id := r.Cluster
		if len(bySpan) > 0 {
			v, ok := bySpan[r.Cluster]
			if !ok {
				unm[r.Cluster] = true
				continue
			}
			id = v
		}
		w := argocd.ServiceWorkload{ClusterID: id, Namespace: r.Namespace, Workload: r.Workload}
		if !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.ClusterID != b.ClusterID {
			return a.ClusterID < b.ClusterID
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Workload < b.Workload
	})
	var um []string
	for v := range unm {
		um = append(um, v)
	}
	sort.Strings(um)
	return out, um
}

func (s *Server) buildServiceGitOps(ctx context.Context, name string, now time.Time, svc *argocd.SettingsService, cm serviceGitOpsClusters, rolloutsOn bool, src string) (*serviceGitOpsResponse, error) {
	wFrom, rFrom := now.Add(-serviceGitOpsWorkloadWindow), now.Add(-serviceGitOpsRolloutWindow)
	resp := &serviceGitOpsResponse{
		Service: name, From: rFrom.UnixMilli(), WorkloadsFrom: wFrom.UnixMilli(), To: now.UnixMilli(),
		Workloads: []serviceGitOpsWorkload{},
		Argo:      serviceGitOpsArgo{Hubs: []serviceGitOpsHub{}, Apps: []argocd.ServiceApp{}},
		Rollouts:  serviceGitOpsRollouts{Rows: []chstore.RolloutRow{}},
	}
	// İş yükü düzeyi (revizyonsuz) liste: çok revizyonlu bir iş yükü tavanı
	// yiyip ötekileri düşürmesin (v0.10.981 inceleme).
	refs, capped, err := serviceGitOpsStoreOf(s).ServiceWorkloads(ctx, name, wFrom, now)
	if err != nil {
		return nil, err
	}
	resp.WorkloadsCapped = capped
	workloads, unmapped := serviceGitOpsWorkloads(refs, cm.bySpan)
	resp.UnmappedClusters = unmapped
	for _, wl := range workloads {
		resp.Workloads = append(resp.Workloads, serviceGitOpsWorkload{ServiceWorkload: wl, ClusterName: cm.names[wl.ClusterID]})
	}

	s.serviceGitOpsRollouts(ctx, resp, workloads, rFrom, now, rolloutsOn, src)
	s.serviceGitOpsArgo(ctx, resp, workloads, svc, cm, now)
	return resp, nil
}

func (s *Server) serviceGitOpsRollouts(ctx context.Context, resp *serviceGitOpsResponse, workloads []argocd.ServiceWorkload, from, to time.Time, enabled bool, src string) {
	if !enabled {
		resp.Rollouts.Note = `Rollouts kapalı — bir admin /rollouts sayfasındaki "Etkinleştir" ile açar`
		return
	}
	resp.Rollouts.Enabled = true
	if len(workloads) == 0 {
		return
	}
	keys := make([]rollout.Key, 0, len(workloads))
	for _, w := range workloads {
		keys = append(keys, rollout.Key{ClusterID: w.ClusterID, Namespace: w.Namespace, Workload: w.Workload})
	}
	rd := rolloutReaderOf(s)
	read := rd.RolloutsForWorkloads
	if src == rollout.SourceV2 {
		read = rd.RolloutV2ForWorkloads
	}
	rows, err := read(ctx, keys, from, to)
	if err != nil {
		log.Printf("[service-gitops] rollouts %s: %v", resp.Service, err)
		resp.Rollouts.Note = "rollout listesi okunamadı (geçici CH hatası olabilir; ayrıntı: sunucu logları)"
		return
	}
	resp.Rollouts.Rows = rows
	resp.Rollouts.Capped = len(rows) >= serviceGitOpsRolloutsCap
}

func (s *Server) serviceGitOpsArgo(ctx context.Context, resp *serviceGitOpsResponse, workloads []argocd.ServiceWorkload, svc *argocd.SettingsService, cm serviceGitOpsClusters, now time.Time) {
	if svc == nil || s.thanos == nil {
		resp.Argo.Note = "Argo CD ayarları bu sunucuda bağlı değil"
		return
	}
	cfg := svc.Resolved()
	if len(cfg.Hubs) == 0 {
		resp.Argo.Note = "Argo CD hub'ı tanımlı değil (Ayarlar › Argo CD)"
		return
	}
	resp.Argo.Configured = true
	if len(workloads) == 0 {
		return
	}
	// v0.10.985 — işçi açıkken eşleyicinin tablosu; kenar yoksa / okuma
	// düştüyse aşağıdaki canlı yol (v0.10.981) aynen.
	if argocd.MetricsActive(cfg) && s.serviceGitOpsArgoMapper(ctx, resp, workloads, cfg, cm, now) {
		return
	}
	resp.Argo.Source = serviceGitOpsSourceLive
	nsSet := map[string]bool{}
	var namespaces []string
	for _, w := range workloads {
		if !nsSet[w.Namespace] {
			nsSet[w.Namespace] = true
			namespaces = append(namespaces, w.Namespace)
		}
	}
	query := argocd.ServiceAppQuery(namespaces, argocd.PinnedAppNames(cfg.Pins, workloads))
	// Pin kolu (≤ 50 ad) + namespace kolu (topk 50) → ≤ 100 seri.
	lim := thanos.ConsoleLimits{Timeout: serviceGitOpsCallTimeout, MaxSeries: argocd.ServiceAppCap * 2, MaxBodyBytes: serviceGitOpsMaxBody}
	hubConf := map[string]thanos.ClusterConfig{}
	var all []argocd.AppStatus
	for _, h := range cfg.Hubs {
		row := serviceGitOpsHub{HubClusterID: h.ClusterID, HubName: cm.names[h.ClusterID]}
		hub, ok := s.thanos.ClusterByID(h.ClusterID)
		switch {
		case !ok:
			row.Status, row.Error = "skipped", "hub Remote Cluster bilinmiyor ya da devre dışı"
		case cm.tokenBad[h.ClusterID]:
			row.Status, row.Error = "skipped", "hub tokenRef'i çözülemedi — kimliksiz istek gönderilmez"
		case ctx.Err() != nil:
			row.Status, row.Error = "skipped", "bütçe doldu"
		}
		if row.Status != "" {
			resp.Argo.Hubs = append(resp.Argo.Hubs, row)
			continue
		}
		if !h.Inject() {
			hub.ThanosLabelName, hub.ThanosLabelValue = "", ""
		}
		hubConf[h.ClusterID] = hub
		qr, err := s.thanos.ConsoleQuery(ctx, hub, thanos.ConsoleInstantQuery{Query: query}, lim)
		if err == nil && qr.ResultType != "vector" {
			err = fmt.Errorf("beklenmeyen sonuç türü %s", qr.ResultType)
		}
		var apps []argocd.AppStatus
		if err == nil {
			apps, err = argocd.ParseAppInfoVector(h.ClusterID, qr.Result)
		}
		if err != nil {
			row.Status, row.Error = "error", serviceGitOpsErrText(err)
			resp.Argo.Hubs = append(resp.Argo.Hubs, row)
			continue
		}
		row.Status, row.Apps, row.Truncated = "ok", len(apps), qr.Truncated
		resp.Argo.Hubs = append(resp.Argo.Hubs, row)
		all = append(all, apps...)
	}
	res := argocd.MatchServiceApps(argocd.ServiceMatchInput{
		Apps: all, Workloads: workloads, Settings: cfg, ClusterByServer: cm.byServer,
		NormalizeServer: func(u string) string {
			if n, err := thanos.NormalizeAPIServerURL(u); err == nil {
				return n
			}
			return u
		},
	})
	resp.Argo.Apps, resp.Argo.OtherInNamespace = res.Apps, res.OtherInNamespace
	syncLim := lim
	syncLim.MaxSeries = serviceGitOpsSyncMaxSeries
	s.serviceGitOpsSyncs(ctx, resp.Argo.Apps, hubConf, syncLim)
}

// serviceGitOpsErrText — hub satırının hata metni: bütçe/iptal kendi
// sözüyle (argocdProbeErrText bağlam hatasında "discovery budget" der),
// gerisi argocdProbeErrText (URL/token yankılamaz).
func serviceGitOpsErrText(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "zaman bütçesi doldu"
	}
	return argocdProbeErrText(err)
}

// serviceGitOpsSyncs — eşlenen uygulamaların 24 saatlik senkron fazları,
// hub başına tek sorgu. Hata = sayım yok (Syncs24h nil kalır; log).
func (s *Server) serviceGitOpsSyncs(ctx context.Context, apps []argocd.ServiceApp, hubs map[string]thanos.ClusterConfig, lim thanos.ConsoleLimits) {
	byHub := map[string][]int{}
	for i, a := range apps {
		byHub[a.HubClusterID] = append(byHub[a.HubClusterID], i)
	}
	for hubID, idx := range byHub {
		hub, ok := hubs[hubID]
		if !ok || ctx.Err() != nil {
			continue
		}
		names := make([]string, 0, len(idx))
		for _, i := range idx {
			names = append(names, apps[i].Name)
		}
		qr, err := s.thanos.ConsoleQuery(ctx, hub, thanos.ConsoleInstantQuery{Query: argocd.ServiceSyncQuery(names)}, lim)
		if err != nil || qr.ResultType != "vector" {
			log.Printf("[service-gitops] sync sayımı hub=%s: %v", hubID, err)
			continue
		}
		// Kesik sonuçta eksik uygulama "senkron yok" okunurdu: sayım hiç
		// yazılmaz (Syncs24h null = okunmadı).
		if qr.Truncated {
			log.Printf("[service-gitops] sync sayımı hub=%s: sonuç %d seride kesildi", hubID, qr.Series)
			continue
		}
		m, err := argocd.ParseSyncVector(qr.Result)
		if err != nil {
			log.Printf("[service-gitops] sync sayımı hub=%s: %v", hubID, err)
			continue
		}
		for _, i := range idx {
			c := m[[2]string{apps[i].AppNamespace, apps[i].Name}]
			if c == nil {
				c = map[string]int{}
			}
			apps[i].Syncs24h = c
		}
	}
}
