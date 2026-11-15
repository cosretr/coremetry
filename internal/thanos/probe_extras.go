package thanos

// v0.10.2182 (Clusters S13, plan docs/frontend/clusters-shadcn-plan.md §3,
// §10.4, §11 S13) — probe'lu ekler. Hepsi BEST-EFFORT (K12: prod'da varlığı
// doğrulanmadı): seri yoksa alan boş kalır, çağıran hata görmez.
//
//   - Node taint'leri (kube_node_spec_taint), yaşı (kube_node_created) —
//     nodeExtras adımları, KSM `node` anahtarlı.
//   - Node disk doluluğu: `/` ve `/sysroot` bağlama noktalarının DOLU olanı
//     (operatör kararı 2026-11-15; OpenShift CoreOS kökü /sysroot'ta),
//     node_exporter instance anahtarlı.
//   - Pod "yer yok": kube_pod_status_unschedulable (PodMetrics, +1 sorgu).
//   - Pod restart dikey işaretleri: changes(restarts_total[step]) range
//     sorgusu; yalnız istemci restart>0 bildiğinde (pods/detail ?restarts=1).

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"
)

// nodeDiskUsedQuery — instance başına kök dosya sisteminin kullanılan oranı
// (0..1); `/` ve `/sysroot`tan dolu olanı.
func nodeDiskUsedQuery() string {
	sel := `mountpoint=~"/|/sysroot",fstype!~"tmpfs|overlay|squashfs"`
	return fmt.Sprintf(
		`max by (instance) (1 - node_filesystem_avail_bytes{%s} / node_filesystem_size_bytes{%s})`, sel, sel)
}

// nodeTaintQuery — node başına etkin taint'ler.
func nodeTaintQuery() string {
	return `max by (node, key, value, effect) (kube_node_spec_taint)`
}

// nodeCreatedQuery — node oluşturulma anı (unix sn).
func nodeCreatedQuery() string {
	return `max by (node) (kube_node_created)`
}

// podUnschedulableQuery — zamanlanamayan pod'lar (== 1; KSM 0 değerli seri basmaz
// ama emniyet için süzülür). Seçici envanterle aynı kapsam.
func podUnschedulableQuery(nsFilter, podRe string) string {
	return fmt.Sprintf(`max by (namespace, pod) (kube_pod_status_unschedulable{%s%s} == 1)`,
		podMatcher(podRe), nsMatcher(nsFilter))
}

// podRestartChangesQuery — adım başına yeniden başlama artışı (tek pod).
func podRestartChangesQuery(namespace, pod string, stepSec int) string {
	return fmt.Sprintf(
		`sum(changes(kube_pod_container_status_restarts_total{namespace="%s",pod="%s"}[%ds]))`,
		escapeLabelValue(namespace), escapeLabelValue(pod), stepSec)
}

// nodeTaints — SAF: taint vektörü → node → sıralı, tekil "key=value:effect"
// (değer boşsa "key:effect").
func nodeTaints(series []promSeries) map[string][]string {
	set := map[string]map[string]bool{}
	for _, ser := range series {
		node, key := ser.Metric["node"], ser.Metric["key"]
		if node == "" || key == "" {
			continue
		}
		t := key
		if v := ser.Metric["value"]; v != "" {
			t += "=" + v
		}
		if e := ser.Metric["effect"]; e != "" {
			t += ":" + e
		}
		if set[node] == nil {
			set[node] = map[string]bool{}
		}
		set[node][t] = true
	}
	out := map[string][]string{}
	for node, ts := range set {
		list := make([]string, 0, len(ts))
		for t := range ts {
			list = append(list, t)
		}
		sort.Strings(list)
		out[node] = list
	}
	return out
}

// PodRestartMarkers — pencerede yeniden başlamanın görüldüğü kova anları
// (unix sn, artan). Seri yoksa boş dilim.
func (s *Service) PodRestartMarkers(ctx context.Context, c ClusterConfig, namespace, pod string, from, to time.Time) ([]int64, error) {
	step := stepForWindow(from, to)
	series, err := s.doQuery(ctx, c, "/api/v1/query_range", url.Values{
		"query": {podRestartChangesQuery(namespace, pod, int(step))},
		"start": {fmt.Sprintf("%d", from.Unix())},
		"end":   {fmt.Sprintf("%d", to.Unix())},
		"step":  {fmt.Sprintf("%d", step)},
	})
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, ser := range series {
		for _, pair := range ser.Values {
			if v, ts, ok := samplePair(pair); ok && v > 0 {
				seen[ts] = true
			}
		}
	}
	out := make([]int64, 0, len(seen))
	for ts := range seen {
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
