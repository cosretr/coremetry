package thanos

// node_capacity.go — v0.10.2168 (Clusters S3; plan
// docs/frontend/clusters-shadcn-plan.md §2–§3). /api/clusters/nodes satırının
// GENİŞLETİLMİŞ alanları: node başına kapasite (cpu / memory / pods),
// ayrılabilir, istek ve limit toplamı, Ready + baskı koşulları. Operatörün
// sorusu ("node'ların maksimum memory'si gözükmüyor") bunun ilk yarısı.
//
// SÖZLEŞME — hepsi BEST-EFFORT:
//   - Kaynak kube-state-metrics; anahtar KSM `node` etiketi (instance → ad
//     eşlemesi gerekmez). Aile yoksa / sorgu hata verirse alan boş kalır,
//     satır ve mevcut alanlar aynen gelir, NodeMetrics hata dönmez.
//   - Sorgular mevcut düzenle aynı: cluster başına SIRALI, sabit sayıda (6),
//     node sayısından bağımsız, hepsi doQuery'den (cluster matcher
//     enjeksiyonu; çok-cluster varsayımı korunur).
//
// BÜTÇE: handler ctx'i 10 sn ve mevcut 9–10 sorgu aynı döngüde sıralı
// koşuyor. Bu blok EN SONA eklenir ve kendi tavanıyla koşar
// (nodeExtraBudget): yavaş bir Thanos mevcut alanları geciktiremez, eklenen
// bekleme tavanla sınırlıdır, toplam hiçbir durumda çağıranın ctx'ini
// aşamaz. Tavan dolunca kalan sorgular gönderilmez (o alanlar boş kalır,
// 60 sn'lik önbellek süresince).

import (
	"context"
	"net/url"
	"sort"
	"time"
)

// nodeExtraBudget — genişletilmiş bloğun toplam süre tavanı. Ayar değil;
// var olması yalnız testin tavanı kısaltabilmesi için.
var nodeExtraBudget = 3 * time.Second

// nodeResPair — (node, resource) vektöründen bir node'un cpu + memory değeri.
type nodeResPair struct{ cpu, mem float64 }

// nodeExtra — bir node'un genişletilmiş alanları (KSM `node` adı anahtarlı).
type nodeExtra struct {
	capacity, allocatable, requests, limits nodeResPair
	podCapacity                             int
	ready                                   string
	pressure                                []string
}

// nodeInfoLabels — kube_node_info'nun zaten çekilen etiketleri (0 ek sorgu).
type nodeInfoLabels struct{ kubelet, osImage, kernel, runtime string }

// nodeResourcePairs — SAF: `… by (node, resource)` vektörü → node → {cpu,
// memory}. Başka resource, node'suz seri ve sonlu olmayan değer atlanır.
func nodeResourcePairs(series []promSeries) map[string]nodeResPair {
	out := map[string]nodeResPair{}
	for _, ser := range series {
		node, res := ser.Metric["node"], ser.Metric["resource"]
		if node == "" || (res != "cpu" && res != "memory") {
			continue
		}
		v, ok := sampleValue(ser.Value)
		if !ok {
			continue
		}
		p := out[node]
		if res == "cpu" {
			p.cpu = v
		} else {
			p.mem = v
		}
		out[node] = p
	}
	return out
}

// readyRank — çakışan Ready serilerinde (çift scrape, geçiş anı) kötü olan
// kazanır: false > unknown > true. Tanınmayan durum 0 (yazılmaz).
func readyRank(status string) int {
	switch status {
	case "false":
		return 3
	case "unknown":
		return 2
	case "true":
		return 1
	}
	return 0
}

// nodeConditions — SAF: nodeConditionQuery vektörü → node → Ready durumu
// ("true" | "false" | "unknown") ve node → etkin baskı koşulları (sıralı,
// tekil). Baskı yalnız status="true" serisinden okunur.
func nodeConditions(series []promSeries) (ready map[string]string, pressure map[string][]string) {
	ready = map[string]string{}
	seen := map[string]map[string]bool{}
	for _, ser := range series {
		node, cond, status := ser.Metric["node"], ser.Metric["condition"], ser.Metric["status"]
		if node == "" {
			continue
		}
		switch cond {
		case "Ready":
			if r := readyRank(status); r > readyRank(ready[node]) {
				ready[node] = status
			}
		case "MemoryPressure", "DiskPressure", "PIDPressure":
			if status != "true" {
				continue
			}
			if seen[node] == nil {
				seen[node] = map[string]bool{}
			}
			seen[node][cond] = true
		}
	}
	pressure = map[string][]string{}
	for node, set := range seen {
		list := make([]string, 0, len(set))
		for c := range set {
			list = append(list, c)
		}
		sort.Strings(list)
		pressure[node] = list
	}
	return ready, pressure
}

// nodeExtras — genişletilmiş alanları toplar (node adı → alanlar). Sıra
// önem sırası: tavan dolarsa önce en az gerekenler düşer.
func (s *Service) nodeExtras(ctx context.Context, c ClusterConfig) map[string]*nodeExtra {
	ctx, cancel := context.WithTimeout(ctx, nodeExtraBudget)
	defer cancel()

	out := map[string]*nodeExtra{}
	get := func(node string) *nodeExtra {
		e := out[node]
		if e == nil {
			e = &nodeExtra{}
			out[node] = e
		}
		return e
	}
	pairs := func(set func(*nodeExtra, nodeResPair)) func([]promSeries) {
		return func(series []promSeries) {
			for node, p := range nodeResourcePairs(series) {
				set(get(node), p)
			}
		}
	}
	steps := []struct {
		query string
		apply func([]promSeries)
	}{
		{nodeCapacityQuery(), pairs(func(e *nodeExtra, p nodeResPair) { e.capacity = p })},
		{nodePodCapacityQuery(), func(series []promSeries) {
			for node, n := range podCountsByNode(series) {
				get(node).podCapacity = n
			}
		}},
		{nodeConditionQuery(), func(series []promSeries) {
			ready, pressure := nodeConditions(series)
			for node, st := range ready {
				get(node).ready = st
			}
			for node, list := range pressure {
				get(node).pressure = list
			}
		}},
		{nodeAllocatableQuery(), pairs(func(e *nodeExtra, p nodeResPair) { e.allocatable = p })},
		{nodeRequestsQuery(), pairs(func(e *nodeExtra, p nodeResPair) { e.requests = p })},
		{nodeLimitsQuery(), pairs(func(e *nodeExtra, p nodeResPair) { e.limits = p })},
	}
	for _, st := range steps {
		if ctx.Err() != nil {
			break // tavan doldu: kalanlar gönderilmez
		}
		series, err := s.doQuery(ctx, c, "/api/v1/query", url.Values{"query": {st.query}})
		if err != nil {
			continue
		}
		st.apply(series)
	}
	return out
}

// applyNodeExtra — SAF: satıra genişletilmiş alanları yazar. e nil olabilir
// (KSM yok ya da ad çözülemedi). Kapasite yedeği node_exporter: çekirdek
// sayısı ve MemTotal (zaten çekiliyor); ikisi de yoksa alan 0 = bilinmiyor.
func applyNodeExtra(row *NodeRow, e *nodeExtra, info nodeInfoLabels, neCores, neMemTotal float64) {
	row.CPUCapacityCores, row.MemCapacityBytes = neCores, neMemTotal
	row.KubeletVersion, row.OSImage = info.kubelet, info.osImage
	row.KernelVersion, row.ContainerRuntime = info.kernel, info.runtime
	if e == nil {
		return
	}
	if e.capacity.cpu > 0 {
		row.CPUCapacityCores = e.capacity.cpu
	}
	if e.capacity.mem > 0 {
		row.MemCapacityBytes = e.capacity.mem
	}
	row.PodCapacity = e.podCapacity
	row.CPUAllocatableCores, row.MemAllocatableBytes = e.allocatable.cpu, e.allocatable.mem
	row.CPURequestCores, row.MemRequestBytes = e.requests.cpu, e.requests.mem
	row.CPULimitCores, row.MemLimitBytes = e.limits.cpu, e.limits.mem
	row.Ready, row.Pressure = e.ready, e.pressure
}
