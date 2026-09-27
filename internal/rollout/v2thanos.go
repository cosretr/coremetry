package rollout

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

// v2thanos.go — v0.10.982 — P2.2 dedektörünün Thanos adaptörleri (§3.4
// karar 1: işçiler konsol taşımasını thanos.WorkerQuery üzerinden kullanır;
// doQuery / promapi dokunulmaz). İnce: çözüm ve seçim mantığı SAF
// fonksiyonlarda (v2ResultFromConsole, v2ClusterRefs), testleri orada.

// V2ThanosQuerier — thanos.WorkerQuery → V2QueryResult. Limits sıfır değeri
// işçi varsayılanıdır (≤ 50 000 seri, ≤ 64 MiB, 30 s, dedup=true,
// partial_response=false; karar 2). Çözülemeyen TokenRef → hata, istek yok
// (fail-closed) → küme bu tikte atlanır ve koşu notunda görünür. at:
// değerlendirme zamanı (WorkerLimits.Time) — işçi bir kümenin tik
// sorgularını tek zamana sabitler (inceleme düzeltmesi v0.10.982).
type V2ThanosQuerier struct {
	Svc    *thanos.Service
	Limits thanos.WorkerLimits
}

func (q V2ThanosQuerier) Query(ctx context.Context, clusterID, expr string, at time.Time) (V2QueryResult, error) {
	lim := q.Limits
	lim.Time = at
	res, err := q.Svc.WorkerQuery(ctx, clusterID, expr, lim)
	if err != nil {
		return V2QueryResult{}, err
	}
	return v2ResultFromConsole(res)
}

// v2ResultFromConsole — SAF: ConsoleResult → V2QueryResult. Herhangi bir
// uyarı Partial (Thanos kısmi yanıtı uyarıyla bildirir; §4.10).
func v2ResultFromConsole(res *thanos.ConsoleResult) (V2QueryResult, error) {
	if res == nil {
		return V2QueryResult{Samples: []V2Sample{}}, nil
	}
	samples, err := ParseV2Vector(res.ResultType, res.Result)
	if err != nil {
		return V2QueryResult{}, err
	}
	return V2QueryResult{Samples: samples, Series: res.Series, TotalSeries: res.TotalSeries,
		Truncated: res.Truncated, Partial: res.Partial()}, nil
}

// V2ThanosClusters — Remote Cluster kaydı → hedef kümeler. Argo hub'ları
// DAHİL (karar 5 / §5.6: hub sıradan Remote Cluster'dır, entity ve rollout
// işlemesi de alır; v1 reconciler da hub ayırmaz — inceleme düzeltmesi
// v0.10.982).
type V2ThanosClusters struct {
	Svc *thanos.Service
}

func (c V2ThanosClusters) V2Clusters() []V2ClusterRef {
	return v2ClusterRefs(c.Svc.CurrentSettings().Clusters)
}

// v2ClusterRefs — SAF: etkin + URL'li kayıtlar (hub'lar dahil), EffectiveID
// sırasında; namespace kalkanı thanos.NamespaceMatcher ile (§4.9).
func v2ClusterRefs(cfgs []thanos.ClusterConfig) []V2ClusterRef {
	seen := map[string]bool{}
	var out []V2ClusterRef
	for _, c := range cfgs {
		id := c.EffectiveID()
		if !c.Enabled || id == "" || strings.TrimSpace(c.URL) == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, V2ClusterRef{ID: id, Name: c.Name, NSMatcher: thanos.NamespaceMatcher(c.NamespaceFilter)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
