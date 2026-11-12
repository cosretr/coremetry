package thanos

// health_queries.go — servis sağlık kontrolünün (mcptools.ReadServiceHealthcheck)
// cluster kapsamlı PromQL kurucuları. SAF; değerler escapeLabelValue ile
// çerçevelenir, ad listeleri regexp.QuoteMeta ile tam eşleşir (^…$).
//
//	v0.10.2042  hazır replika — kube-state Deployment + StatefulSet (tek sorgu)
//	            cluster sağlığı — node Ready koşulu + firing ALERTS (iki sorgu)
//
// İş yükü adı pod adından türetilir (stripPodSuffixes; Infrastructure
// sekmesinin sezgiseli): servisin GERÇEKTE koşan pod'larının sahipleri —
// ad regex'i değil, böylece "orders-api-worker" gibi komşu iş yükü karışmaz.
// OpenShift DeploymentConfig pod'u (<dc>-<N>-<rasgele>) bu kümeye girmez;
// kontrol o zaman "iş yükü bulunamadı" (Ölçülemedi) der, uydurmaz.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// WorkloadNamesFromPods — SAF: pod adları → tekil, sıralı iş yükü adları (≤max).
func WorkloadNamesFromPods(pods []string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pods {
		if n := stripPodSuffixes(p); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}

// exactAlternation — SAF: ["a","b.c"] → `^(a|b\.c)$` (değer çerçevesi kaçışlı).
func exactAlternation(vals []string) string {
	q := make([]string, len(vals))
	for i, v := range vals {
		q[i] = regexp.QuoteMeta(v)
	}
	return escapeLabelValue("^(" + strings.Join(q, "|") + ")$")
}

// WorkloadReplicasQuery — SAF: servisin iş yüklerinin istenen / hazır
// replikası. Deployment ve StatefulSet tek sorguda (`or`); max by HA'lı KSM
// çiftlerinin kopyasını düzler. namespaces = servis pod'larının namespace'leri
// (aynı adlı başka ns'teki iş yükü karışmasın); nsFilter cluster kalkanı.
func WorkloadReplicasQuery(nsFilter string, namespaces, workloads []string) string {
	ns := fmt.Sprintf(`,namespace=~"%s"`, exactAlternation(namespaces)) + nsMatcher(nsFilter)
	names := exactAlternation(workloads)
	return fmt.Sprintf(`max by (__name__, namespace, deployment, statefulset) (`+
		`{__name__=~"kube_deployment_spec_replicas|kube_deployment_status_replicas_ready",deployment=~"%s"%s}`+
		` or {__name__=~"kube_statefulset_replicas|kube_statefulset_status_replicas_ready",statefulset=~"%s"%s})`,
		names, ns, names, ns)
}

// NodeReadyQuery — SAF: node'ların Ready koşulu durum başına sayısı
// (status = true | false | unknown). `== 1` KSM'nin 0 değerli kardeş
// serilerini eler.
func NodeReadyQuery() string {
	return `count by (status) (kube_node_status_condition{condition="Ready"} == 1)`
}

// FiringAlertsMax — alert sorgusunun satır tavanı.
const FiringAlertsMax = 50

// FiringAlertsQuery — SAF: firing critical / warning alert'leri (ad ×
// önem × namespace sayısı). Watchdog / InfoInhibitor her zaman yanar
// (alertmanager canlılık sinyali) — sayılmaz.
func FiringAlertsQuery() string {
	return fmt.Sprintf(`topk(%d, count by (alertname, severity, namespace) `+
		`(ALERTS{alertstate="firing",severity=~"critical|warning",alertname!~"Watchdog|InfoInhibitor"}))`, FiringAlertsMax)
}
