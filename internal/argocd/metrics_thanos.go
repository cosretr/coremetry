package argocd

// metrics_thanos.go — v0.10.983 — argocd-metrics işçisinin Thanos adaptörleri
// (§3.4 karar 1: işçiler konsol taşımasını thanos.WorkerQuery üzerinden
// kullanır; konsol okuyucusu, doQuery ve promapi dokunulmaz). İnce: çözüm ve
// seçim mantığı SAF fonksiyonlarda (metricsResultFromConsole, registryFrom).

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// ThanosMetricsQuerier — thanos.WorkerQuery → MetricsQueryResult. Sınırlar
// argocd reader{} (Normalized: ≤ 50 000 seri, ≤ 64 MiB, 5–45 s); dedup=true,
// partial_response=false (WorkerLimits sıfır değerleri). Çözülemeyen TokenRef
// → hata, istek YOK (fail-closed). noClusterLabel → hub'ın
// injectClusterLabel=false kararı (WorkerLimits.NoClusterLabel).
type ThanosMetricsQuerier struct {
	Svc *thanos.Service
}

func (q ThanosMetricsQuerier) Query(ctx context.Context, hubClusterID, expr string, at time.Time, noClusterLabel bool, lim Reader) (MetricsQueryResult, error) {
	res, err := q.Svc.WorkerQuery(ctx, hubClusterID, expr, thanos.WorkerLimits{
		MaxSeries: lim.MaxSeries, MaxBodyMiB: lim.MaxBodyMiB, TimeoutS: lim.TimeoutS, Time: at, NoClusterLabel: noClusterLabel,
	})
	if err != nil {
		return MetricsQueryResult{}, err
	}
	return metricsResultFromConsole(res), nil
}

// metricsResultFromConsole — SAF: herhangi bir uyarı Partial (Thanos kısmi
// yanıtı uyarıyla bildirir; §4.10 / §5.4).
func metricsResultFromConsole(res *thanos.ConsoleResult) MetricsQueryResult {
	if res == nil {
		return MetricsQueryResult{ResultType: "vector"}
	}
	return MetricsQueryResult{ResultType: res.ResultType, Result: res.Result, Series: res.Series,
		Truncated: res.Truncated, Partial: res.Partial()}
}

// ThanosRegistry — Remote Cluster kaydı → işçinin hub + dest_server görünümü.
type ThanosRegistry struct {
	Svc *thanos.Service
}

func (r ThanosRegistry) ArgoRegistry() Registry {
	return registryFrom(r.Svc.Snapshot().Clusters, r.Svc.ClusterByID)
}

// registryFrom — SAF: maskeli anlık görüntü (token çözüm durumu, apiServerUrls)
// + ETKİN kayıt çözücüsü (URL, etkin küme etiketi). Devre dışı kayıt hub
// sayılmaz ve dest_server'ı eşlenmez (service_gitops.go emsali).
func registryFrom(snap []thanos.ClusterSnapshot, byID func(string) (thanos.ClusterConfig, bool)) Registry {
	reg := Registry{Hubs: map[string]HubInfo{}, ByServer: map[string]string{}, Normalize: normalizeDestServer}
	for _, c := range snap {
		cc, ok := byID(c.ID)
		if !ok || strings.TrimSpace(cc.URL) == "" {
			continue
		}
		ln, lv := cc.EffectiveThanosLabel()
		reg.Hubs[c.ID] = HubInfo{ID: c.ID, Name: c.Name, URL: cc.URL, LabelName: ln, LabelValue: lv,
			TokenBad: c.TokenRef != "" && !c.TokenResolved}
		for _, u := range c.APIServerURLs {
			if n := normalizeDestServer(u); n != "" {
				reg.ByServer[n] = c.ID
			}
		}
	}
	return reg
}

// normalizeDestServer — thanos.NormalizeAPIServerURL (varsayılan :6443,
// sondaki "/" yok, küçük harf host); çözülemeyen değer olduğu gibi.
func normalizeDestServer(u string) string {
	if n, err := thanos.NormalizeAPIServerURL(u); err == nil {
		return n
	}
	return strings.TrimSpace(u)
}
