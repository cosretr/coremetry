package api

// service_gitops_mapper.go — v0.10.985 — Rollouts v2 P3.2: GitOps sekmesinin
// Argo bölümü eşleyicinin tablosundan (docs/rollouts/v2-audit.md §10.6
// "Service Argo card": MV iş yükleri → kenarlar → son durum → senkronlar).
// Rota YOK (aynı GET /api/services/{name}/gitops; api.go +0) — kaynak
// kararı sunucuda, FE yalnız argo.source'u görür.
//
// Karar (service_gitops.go serviceGitOpsArgo):
//   - argocd.MetricsActive(cfg) değilse (varsayılan) bu dosya HİÇ çağrılmaz:
//     v0.10.981 canlı yolu bayt bayt aynı (+ source "live").
//   - v0.10.985 inceleme — KAPSAM KAPISI (serviceGitOpsMapperCoverage):
//     kenarın yaşı işçinin canlı olduğunu söylemez (değişmeyen kenar 20 sa'de
//     bir dokunulur; hazır olmayan instance'ın kenarı 26 sa taze görünür,
//     argocd_app_status yalnız değişimde yazılır → donmuş durum "güncel"
//     okunurdu). Tablo yalnız şu hâlde yetkilidir, aksi hâlde canlı yol +
//     argo.note'ta sebep:
//       · her hub'ın ≥1 instance'ı var ve hub'lardaki HER instance etkin
//         (canlı sorgu hub'daki her argocd_app_info serisini görür; eşleyici
//         yalnız etkin instance'ları — devre dışı / hub'ı instance'sız
//         kurulumda tablo eksik kalırdı);
//       · son argocd-metrics koşusu (rollout_worker_runs, FINAL, 1 gün,
//         LIMIT 1) VAR, 'ok' (her parça okundu VE her instance eşleyiciye
//         hazır — mapper.go hazır olmayanı partial yapar), started_at ≤
//         argocd.MetricsRunFreshness yaşında, son ayar kaydından SONRA
//         başlamış ve kapsamı (scopes_total) ayardaki etkin instance sayısı.
//     Ayarlarda hiç tanımlanmamış bir Argo CD'nin (hub'da koşan ama
//     Settings'te olmayan) uygulamaları tabloda yoktur — bilinçli fark
//     (DECISIONS P3.2).
//   - Kapı açıksa servisin iş yüklerinde CANLI ve TAZE (last_verified_at ≥
//     şimdi − argocd.MappingFreshness) kenar aranır. Hiç yoksa (bu
//     namespace'lere deploy eden uygulama yok) ya da kenar okuması düştüyse
//     canlı yol: işçi açık diye sekme boşalmasın. Durum okuması düşerse de
//     canlı yol (yarım kart yerine eski davranış).
//   - Kenar varsa: uygulama başına argocd_app_status son satırı; son satırı
//     'deleted' ya da hiç satırı olmayan uygulama gösterilmez (log değil,
//     sayı: sekmede not). Senkron sütunu son 24 saatteki 'sync' satırları
//     (işçi tik başına tek satır yazar, bir tik içindeki aynı fazlı ardışık
//     satırlar katlanır → alt sınır); okuma düşerse sütun null ("okunmadı"),
//     kart düşmez.
//   - Eşleme kuralları canlı yolla ORTAK (argocd/mapping.go;
//     TestMapperMatchesLiveMatcher): manual servis içinde adı yener, zayıf
//     kenarlar "aynı namespace'te eşleşmeyen" sayısıdır.
//   - Hub satırları sorgu sonucu değil kayıt durumu: hub Remote Cluster'ı
//     yok/devre dışı ya da tokenRef'i çözülmüyorsa "skipped" (kapı zaten
//     partial koşu yüzünden kapanır; API pod'unun kaydı işçininkinden farklı
//     olabilir), aksi hâlde "ok" + gösterilen uygulama.

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// Argo bölümünün kaynak değerleri (FE lib/types.ts ServiceGitOpsResponse.argo.source).
const (
	serviceGitOpsSourceMapper = "mapper"
	serviceGitOpsSourceLive   = "live"
)

// serviceGitOpsStore — sekmenin CH dikişi (*chstore.Store; testte sahte).
type serviceGitOpsStore interface {
	ServiceWorkloads(ctx context.Context, service string, from, to time.Time) ([]chstore.WorkloadRevisionRef, bool, error)
	ArgoCDServiceEdgesFor(ctx context.Context, workloads []argocd.ServiceWorkload, pairs [][2]string, since time.Time) (chstore.ArgoCDServiceEdges, error)
	ArgoCDLatestStatusFor(ctx context.Context, keys []argocd.AppKey) (map[argocd.AppKey]argocd.StatusRow, error)
	ArgoCDSyncCounts(ctx context.Context, keys []argocd.AppKey, since time.Time, fold time.Duration) (map[argocd.AppKey]map[string]int, error)
	RolloutWorkerLastRun(ctx context.Context, worker string) (*rollout.WorkerRun, error)
}

// serviceGitOpsMapperConfigGap — SAF: kapsam kapısının ayar yarısı (dosya
// başı); boş = her hub'ın ≥1 instance'ı var, hepsi etkin. Döner: sebep ve
// etkin instance sayısı (koşunun scopes_total'ıyla karşılaştırılır).
func serviceGitOpsMapperConfigGap(cfg argocd.Settings) (string, int) {
	perHub := map[string]int{}
	for _, h := range cfg.Hubs {
		perHub[h.ClusterID] = 0
	}
	enabled := 0
	for _, in := range cfg.Instances {
		if _, ok := perHub[in.HubClusterID]; !ok {
			continue
		}
		if !in.Enabled {
			return fmt.Sprintf("instance %s devre dışı — canlı sorgu hub'daki her Argo CD'yi görür, eşleyici yalnız etkin instance'ları", in.ID), 0
		}
		perHub[in.HubClusterID]++
		enabled++
	}
	for _, h := range cfg.Hubs {
		if perHub[h.ClusterID] == 0 {
			return fmt.Sprintf("hub %s'in etkin instance'ı yok — eşleyici o hub'ı okumuyor", h.ClusterID), 0
		}
	}
	return "", enabled
}

// serviceGitOpsMapperRunGap — SAF: kapsam kapısının işçi yarısı (dosya
// başı); boş = son argocd-metrics koşusu tabloyu yetkili kılar.
func serviceGitOpsMapperRunGap(cfg argocd.Settings, run *rollout.WorkerRun, enabled int, now time.Time) string {
	switch {
	case run == nil:
		return "argocd-metrics işçisi son 24 saatte koşmadı"
	case now.Sub(run.StartedAt) > argocd.MetricsRunFreshness(cfg):
		return fmt.Sprintf("son argocd-metrics koşusu %s önce — işçi durmuş olabilir", now.Sub(run.StartedAt).Truncate(time.Second))
	case run.Status != rollout.RunOK:
		return fmt.Sprintf("son argocd-metrics koşusu %q (okunamayan hub/instance ya da eşleyiciye hazır olmayan instance)", run.Status)
	case cfg.UpdatedAt > 0 && run.StartedAt.UnixNano() < cfg.UpdatedAt:
		return "Argo CD ayarı kaydedildikten sonra argocd-metrics henüz koşmadı"
	case run.ScopesTotal != enabled:
		return fmt.Sprintf("son koşunun kapsamı %d instance, ayarda %d etkin instance var", run.ScopesTotal, enabled)
	}
	return ""
}

var serviceGitOpsStoreOf = func(s *Server) serviceGitOpsStore { return s.store }

// serviceGitOpsPairs — SAF: iş yüklerinin tekil, sıralı (cluster, ns) çiftleri.
func serviceGitOpsPairs(workloads []argocd.ServiceWorkload) [][2]string {
	seen := map[[2]string]bool{}
	var out [][2]string
	for _, w := range workloads {
		p := [2]string{w.ClusterID, w.Namespace}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

// serviceGitOpsEdgeApps — SAF: servisin iş yüklerine İŞ YÜKÜ kenarı olan
// uygulamaların tekil, sıralı anahtarları (durum okumasının girdisi).
func serviceGitOpsEdgeApps(edges []argocd.MappingRow, workloads []argocd.ServiceWorkload) []argocd.AppKey {
	wset := map[argocd.ServiceWorkload]bool{}
	for _, w := range workloads {
		wset[w] = true
	}
	seen := map[argocd.AppKey]bool{}
	var out []argocd.AppKey
	for _, e := range edges {
		if e.MatchMethod == argocd.MatchNamespace || !e.RemovedAt.IsZero() {
			continue
		}
		if !wset[argocd.ServiceWorkload{ClusterID: e.ClusterID, Namespace: e.Namespace, Workload: e.Workload}] {
			continue
		}
		k := e.Key().App()
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.InstanceID != b.InstanceID {
			return a.InstanceID < b.InstanceID
		}
		if a.AppNamespace != b.AppNamespace {
			return a.AppNamespace < b.AppNamespace
		}
		return a.Name < b.Name
	})
	return out
}

// serviceGitOpsArgoMapper — true: Argo bölümü eşleyicinin tablosundan
// dolduruldu; false: çağıran canlı yola düşer (dosya başı).
func (s *Server) serviceGitOpsArgoMapper(ctx context.Context, resp *serviceGitOpsResponse, workloads []argocd.ServiceWorkload, cfg argocd.Settings, cm serviceGitOpsClusters, now time.Time) bool {
	st := serviceGitOpsStoreOf(s)
	gap, enabled := serviceGitOpsMapperConfigGap(cfg)
	if gap == "" {
		run, err := st.RolloutWorkerLastRun(ctx, rollout.WorkerArgoCDMetrics)
		if err != nil {
			log.Printf("[service-gitops] argocd-metrics koşu kaydı %s: %v — canlı yol", resp.Service, err)
			gap = "argocd-metrics koşu kaydı okunamadı"
		} else {
			gap = serviceGitOpsMapperRunGap(cfg, run, enabled, now)
		}
	}
	if gap != "" {
		resp.Argo.Note = "eşleyici tablosu kullanılmadı: " + gap + " — canlı sorgu"
		return false
	}
	se, err := st.ArgoCDServiceEdgesFor(ctx, workloads, serviceGitOpsPairs(workloads), now.Add(-argocd.MappingFreshness))
	if err != nil {
		log.Printf("[service-gitops] eşleme okuması %s: %v — canlı yol", resp.Service, err)
		return false
	}
	edges := se.Edges
	if len(edges) == 0 {
		return false
	}
	keys := serviceGitOpsEdgeApps(edges, workloads)
	statuses, err := st.ArgoCDLatestStatusFor(ctx, keys)
	if err != nil {
		log.Printf("[service-gitops] durum okuması %s: %v — canlı yol", resp.Service, err)
		return false
	}
	fold := time.Duration(cfg.Intervals.MetricsS) * time.Second
	syncs, serr := st.ArgoCDSyncCounts(ctx, keys, now.Add(-24*time.Hour), fold)
	if serr != nil {
		log.Printf("[service-gitops] senkron sayımı %s: %v", resp.Service, serr)
	}
	res := argocd.ServiceAppsFromEdges(argocd.ServiceEdgesInput{
		Workloads: workloads, Edges: edges, Statuses: statuses, Syncs: syncs, SyncsOK: serr == nil,
		Settings: cfg, ByServer: cm.byServer,
		Normalize: func(u string) string {
			if n, err := thanos.NormalizeAPIServerURL(u); err == nil {
				return n
			}
			return u
		},
	})
	resp.Argo.Source = serviceGitOpsSourceMapper
	resp.Argo.Apps, resp.Argo.OtherInNamespace = res.Apps, res.OtherInNamespace
	var notes []string
	if se.WorkloadCapped {
		notes = append(notes, fmt.Sprintf("iş yükü eşleme kenarları %d satırda kesildi — liste eksik olabilir", chstore.ArgoCDServiceEdgeMax))
	}
	if se.WeakCapped {
		notes = append(notes, fmt.Sprintf("aynı namespace'teki uygulama kenarları %d satırda kesildi — \"eşleşmeyen\" sayısı alt sınır", chstore.ArgoCDServiceEdgeMax))
	}
	if res.StatusMissing > 0 {
		notes = append(notes, fmt.Sprintf("%d eşlenmiş uygulamanın güncel durumu yok (silinmiş ya da henüz okunmadı) — gösterilmedi", res.StatusMissing))
	}
	if len(notes) > 0 {
		resp.Argo.Note = strings.Join(notes, " · ")
	}
	perHub := map[string]int{}
	for _, a := range res.Apps {
		perHub[a.HubClusterID]++
	}
	for _, h := range cfg.Hubs {
		row := serviceGitOpsHub{HubClusterID: h.ClusterID, HubName: cm.names[h.ClusterID], Status: "ok", Apps: perHub[h.ClusterID]}
		switch _, ok := s.thanosClusterByID(h.ClusterID); {
		case !ok:
			row.Status, row.Error = "skipped", "hub Remote Cluster bilinmiyor ya da devre dışı — eşleyici bu hub'ı okuyamıyor"
		case cm.tokenBad[h.ClusterID]:
			row.Status, row.Error = "skipped", "hub tokenRef'i çözülemedi — eşleyici bu hub'ı okuyamıyor"
		}
		resp.Argo.Hubs = append(resp.Argo.Hubs, row)
	}
	return true
}

// thanosClusterByID — nil thanos'a dayanıklı ClusterByID.
func (s *Server) thanosClusterByID(id string) (thanos.ClusterConfig, bool) {
	if s.thanos == nil {
		return thanos.ClusterConfig{}, false
	}
	return s.thanos.ClusterByID(id)
}
