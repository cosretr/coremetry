package entity

import (
	"sort"
	"strings"
)

// workload_service.go — v0.10.2191 (operator-reported: Clusters ns detayında
// her iş yükü "servis eşleşmedi"; Deployment bsa-digital-dashboard-prod ↔
// servis bsa-digital-dashboard-prod 1.4M span'le Services listesindeydi).
// Eski yol tekti: /pods satırlarının metric_points host_name = pod adı
// eşlemesinin çoğunluğu. Uygulama metrik basmıyorsa, host.name pod adı
// değilse, cluster attr'ı farklıysa ya da pod listesi cluster-geneli
// topk'ta kesildiyse sessizce boştu.
//
// Çözüm entity grafiği üzerinden (wl →parent→ pod →runs→ svc), sırayla:
//  1. pod→service "runs" kenarları (entity_seen_5m — span resource'unun
//     k8s.pod.name + k8s.namespace.name + cluster'ı): iş yükü pod'larının
//     span sayısıyla ağırlıklı çoğunluk servisi;
//  2. açık resource attr bağı: servisin k8s.deployment.name (+
//     k8s.namespace.name) değeri (service_metadata türetimi) = iş yükü;
//  3. son çare: iş yükü adı = service.name (büyük/küçük harf duyarsız).
//
// Belirsizlik yanlış etiket yerine "" (pickPodService kuralı).

// SeenEdge — entity_seen_5m satırı: pod →runs→ service (+ span'deki node).
type SeenEdge struct {
	Namespace, Pod, Node, Service string
	Spans                         int64
	// Cluster — span'in cluster DEĞERİ (v0.10.2195; trace süzgeci bu değerle).
	Cluster string
}

// PodEdges — bir pod'un baskın servisi ve span'den bilinen node'u.
type PodEdges struct{ Service, Node, ClusterValue string }

// IndexPodEdges — SAF: namespace'in pod'ları için kenar indeksi. Satırın
// namespace'i boşsa da sayılır (collector k8s.namespace.name basmıyor —
// EntitySeenForPods v0.10.190 sözleşmesi); başka namespace'inki sayılmaz.
// Baskın servis = en çok span (eşitlikte alfabetik ilk — kararlı).
func IndexPodEdges(namespace string, seen []SeenEdge) map[string]PodEdges {
	type acc struct {
		spans   map[string]int64
		node    string
		cluster map[string]int64
	}
	by := map[string]*acc{}
	for _, e := range seen {
		if e.Pod == "" || (e.Namespace != "" && e.Namespace != namespace) {
			continue
		}
		a := by[e.Pod]
		if a == nil {
			a = &acc{spans: map[string]int64{}, cluster: map[string]int64{}}
			by[e.Pod] = a
		}
		if e.Service != "" {
			a.spans[e.Service] += e.Spans
		}
		if e.Cluster != "" {
			a.cluster[e.Cluster] += e.Spans
		}
		if a.node == "" && e.Node != "" {
			a.node = e.Node
		}
	}
	out := make(map[string]PodEdges, len(by))
	for pod, a := range by {
		out[pod] = PodEdges{Service: argmax(a.spans), Node: a.node, ClusterValue: argmax(a.cluster)}
	}
	return out
}

func argmax(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best := ""
	for _, k := range keys {
		if best == "" || m[k] > m[best] {
			best = k
		}
	}
	return best
}

// DeploymentLink — servisin span resource attr'ından iş yükü bağı.
type DeploymentLink struct{ Service, Deployment, Namespace string }

// ResolveWorkloadService — SAF: iş yükünün servisi (sıra dosya başında).
func ResolveWorkloadService(workload, namespace string, pods []string, edges map[string]PodEdges, links []DeploymentLink, known []string) string {
	if workload == "" {
		return ""
	}
	// 1. wl → pod → runs → svc (pod başına baskın servis, pod sayısı çoğunluğu).
	count := map[string]int64{}
	for _, p := range pods {
		if s := edges[p].Service; s != "" {
			count[s]++
		}
	}
	if s := argmax(count); s != "" {
		return s
	}
	// 2. k8s.deployment.name (+ k8s.namespace.name) attr bağı.
	var sameNS, anyNS []string
	for _, l := range links {
		if l.Deployment == "" || !strings.EqualFold(l.Deployment, workload) {
			continue
		}
		anyNS = append(anyNS, l.Service)
		if strings.EqualFold(l.Namespace, namespace) {
			sameNS = append(sameNS, l.Service)
		}
	}
	if len(sameNS) > 0 {
		sort.Strings(sameNS)
		for _, s := range sameNS {
			if strings.EqualFold(s, workload) {
				return s
			}
		}
		return sameNS[0]
	}
	// namespace service.namespace ile maskelenmiş olabilir: tek aday güvenli,
	// birden çok aday (aynı ad başka namespace'lerde) belirsiz.
	if len(anyNS) == 1 {
		return anyNS[0]
	}
	// 3. ad eşitliği.
	match := ""
	for _, s := range known {
		if s == workload {
			return s
		}
		if match == "" && strings.EqualFold(s, workload) {
			match = s
		}
	}
	return match
}
