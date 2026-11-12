package thanos

// pod_family.go — v0.10.1271 (CoSRE adım 2): query_metrics aracının pod
// doygunluk ailelerinin SABİT PromQL şablonları + servis → pod regex'i.
//
// Model PromQL YAZMAZ: aile (cpu | memory | restarts) + pod regex'i + pencere
// sunucuda bu şablonlara girer. Seçici pod/namespace matcher'ları envanter
// sorgularıyla AYNI yardımcılardan (podMatcher / nsMatcher); cluster matcher'ı
// burada DEĞİL — çağıran metricsrc.Resolver.ThanosInstant → doQuery yolundan
// geçirir (paylaşımlı querier'da her seçiciye enjekte edilir).
//
// Pencere toplaması anlık sorguda range-vector ile yapılır (alt sorgu yok):
//   cpu      → rate(container_cpu_usage_seconds_total[W])      — pencerede ORTALAMA çekirdek
//   memory   → max_over_time(container_memory_working_set_bytes[W]) — pencerede TEPE bayt (OOM ekseni)
//   restarts → increase(kube_pod_container_status_restarts_total[W]) — pencerede yeniden başlatma
// Pencere "şimdi"de bitmiyorsa (çıpalı sohbet) `offset` ile geriye kaydırılır.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Pod aile adları (query_metrics enum'unun Thanos yarısı).
const (
	PodFamilyCPU      = "cpu"
	PodFamilyMemory   = "memory"
	PodFamilyRestarts = "restarts"
)

var servicePodEnvSuffixes = []string{"-prod", "-int", "-uat", "-prep"}

// ServicePodRegex — SAF; frontend pages/clusters/podWorkload.ts
// servicePodRegex ile birebir (Infrastructure sekmesiyle aynı pod kümesi):
// deployment | servis | ortam eksiz ad, "-.*" son eki. v0.10.1271 — api'deki
// thanosServicePodRegex'ten taşındı (mcptools da kullanır; api delege eder).
func ServicePodRegex(service, deploy string) string {
	keys := ServicePodKeys(service, deploy)
	cands := make([]string, len(keys))
	for i, k := range keys {
		cands[i] = regexp.QuoteMeta(k)
	}
	return "(" + strings.Join(cands, "|") + ")-.*"
}

// ServicePodKeys — SAF (v0.10.2019): ServicePodRegex'in aday pod öneki
// kümesi (deployment | servis | ortam eksiz ad; sıra ve tekillik aynı).
// Pod, "<anahtar>-" ile başlıyorsa servise aittir. ML metrik Thanos yolu
// (internal/ml) çok servisi tek geçişte eşlemek için regex yerine bu
// kümeyi önek indeksine koyar — anlam ServicePodRegex ile birebir.
func ServicePodKeys(service, deploy string) []string {
	stripped := service
	for _, suf := range servicePodEnvSuffixes {
		if len(service) > len(suf) && strings.HasSuffix(service, suf) {
			stripped = strings.TrimSuffix(service, suf)
			break
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range []string{deploy, service, stripped} {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// promDur — saniye cinsinden PromQL süre literal'i (en az 1s).
func promDur(d time.Duration) string {
	s := int64(d / time.Second)
	if s < 1 {
		s = 1
	}
	return fmt.Sprintf("%ds", s)
}

// PodFamilyQuery — SAF: pod ailesinin anlık PromQL'i. podRe boş = cluster
// geneli (namespace filtresiyle); byPod=false → tek toplam değer;
// byPod=true → topk(limit) pod başına. offset ≤ 0 → kaydırma yok.
func PodFamilyQuery(family, nsFilter, podRe string, window, offset time.Duration, byPod bool, limit int) (string, error) {
	if window <= 0 {
		return "", fmt.Errorf("thanos pod family: window must be positive")
	}
	off := ""
	if offset >= time.Minute {
		off = " offset " + promDur(offset)
	}
	sel := podMatcher(podRe) + nsMatcher(nsFilter)
	var inner string
	switch family {
	case PodFamilyCPU:
		inner = fmt.Sprintf(`rate(container_cpu_usage_seconds_total{container!="",container!="POD",%s}[%s]%s)`, sel, promDur(window), off)
	case PodFamilyMemory:
		inner = fmt.Sprintf(`max_over_time(container_memory_working_set_bytes{container!="",container!="POD",%s}[%s]%s)`, sel, promDur(window), off)
	case PodFamilyRestarts:
		inner = fmt.Sprintf(`increase(kube_pod_container_status_restarts_total{%s}[%s]%s)`, sel, promDur(window), off)
	default:
		return "", fmt.Errorf("thanos pod family: unknown family %q", family)
	}
	if !byPod {
		return "sum(" + inner + ")", nil
	}
	if limit <= 0 || limit > podListLimit {
		limit = podListLimit
	}
	return fmt.Sprintf("topk(%d, sum by (namespace, pod) (%s))", limit, inner), nil
}

// PodWaitingReasonQuery — v0.10.1287 (AI SRE Faz 1b, k8s kontrolü): servis
// pod'larının ŞU ANKİ bekleme sebebi (CrashLoopBackOff / ImagePullBackOff /
// ErrImagePull / CreateContainerConfigError …). KSM serisi
// kube_pod_container_status_waiting_reason (namespace, pod, container, reason)
// etiketli; `== 1` ŞART (podLastTermQuery gerekçesi: KSM sebep dışı reason
// serilerini de 0 değerle basar), container ekseni max by ile katlanır.
// Seçici envanter sorgularıyla AYNI yardımcılar (podMatcher / nsMatcher —
// değer çerçevesi escapeLabelValue ile kaçışlanır); cluster matcher'ı
// çağıranın doQuery yolunda enjekte edilir. Kubernetes Events ingest
// edilmiyor — yalnız KSM durumu okunur. SAF.
func PodWaitingReasonQuery(nsFilter, podRe string) string {
	return fmt.Sprintf(
		`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{%s%s} == 1)`,
		podMatcher(podRe), nsMatcher(nsFilter))
}

// ── v0.10.1288 (AI SRE Faz 2): tembel node / gürültülü komşu kontrolü ─────
//
// Yalnız doygunluk hipotezi isterse koşar (internal/sre); üç anlık sorgu:
// servis pod'larının node'u (kube_pod_info), o node'ların CPU kullanımı
// (allocatable'ın yüzdesi) ve o node'lardaki en işlek pod'lar (allocatable'ın
// yüzdesi). CPU cAdvisor'ın `node` etiketinden okunur — scrape relabel'ı bu
// etiketi taşımıyorsa node sorguları boş döner (çağıran "node etiketi yok"
// der; okunamadı ≠ temiz). Node adları PodNamesRegex ile (QuoteMeta + ^(…)$)
// çerçevelenir, değer çerçevesi escapeLabelValue ile kaçışlanır; cluster
// matcher'ı çağıranın doQuery yolunda enjekte edilir.

// NodeCheckMaxNodes — kontrolün okuduğu en çok node.
const NodeCheckMaxNodes = 3

// PodNodeQuery — SAF: servis pod'larının node'u. KSM kube_pod_info
// (namespace, pod, node) — `node!=""` zamanlanmamış pod'u düşürür.
func PodNodeQuery(nsFilter, podRe string) string {
	return fmt.Sprintf(`max by (namespace, pod, node) (kube_pod_info{node!="",%s%s})`, podMatcher(podRe), nsMatcher(nsFilter))
}

// nodeMatcher — SAF: `node=~"^(a|b)$"` (en çok NodeCheckMaxNodes ad). Ad yoksa
// ok=false (sorgu kurulmaz — boş seçici tüm node'ları okurdu).
func nodeMatcher(nodes []string) (string, bool) {
	if len(nodes) > NodeCheckMaxNodes {
		nodes = nodes[:NodeCheckMaxNodes]
	}
	re, _ := PodNamesRegex(nodes)
	if re == "" {
		return "", false
	}
	return fmt.Sprintf(`node=~"%s"`, escapeLabelValue(re)), true
}

func nodeOffset(offset time.Duration) string {
	if offset >= time.Minute {
		return " offset " + promDur(offset)
	}
	return ""
}

// NodeCPUPressureQuery — SAF: node başına CPU kullanımı, allocatable'ın
// yüzdesi (5 dk hız; offset ≥ 1 dk ise pencere sonuna kaydırılır).
func NodeCPUPressureQuery(nodes []string, offset time.Duration) (string, error) {
	nm, ok := nodeMatcher(nodes)
	if !ok {
		return "", fmt.Errorf("thanos node check: node adı yok")
	}
	off := nodeOffset(offset)
	return fmt.Sprintf(
		`100 * sum by (node) (rate(container_cpu_usage_seconds_total{container!="",container!="POD",pod!="",%s}[5m]%s)) / on (node) sum by (node) (kube_node_status_allocatable{resource="cpu",%s}%s)`,
		nm, off, nm, off), nil
}

// NodeTopPodsQuery — SAF: o node'lardaki en işlek k pod, node allocatable
// CPU'sunun yüzdesi olarak (namespace, pod, node etiketli).
func NodeTopPodsQuery(nodes []string, k int, offset time.Duration) (string, error) {
	nm, ok := nodeMatcher(nodes)
	if !ok {
		return "", fmt.Errorf("thanos node check: node adı yok")
	}
	if k <= 0 || k > 20 {
		k = 5
	}
	off := nodeOffset(offset)
	return fmt.Sprintf(
		`topk(%d, 100 * sum by (namespace, pod, node) (rate(container_cpu_usage_seconds_total{container!="",container!="POD",pod!="",%s}[5m]%s)) / on (node) group_left sum by (node) (kube_node_status_allocatable{resource="cpu",%s}%s))`,
		k, nm, off, nm, off), nil
}
