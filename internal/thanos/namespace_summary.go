package thanos

// v0.10.2176 (Clusters S8, plan docs/frontend/clusters-shadcn-plan.md §9.3)
// — namespace detayı görünümünün dört kutusu ve pod durum sözcüğü için
// /api/clusters/namespaces/detail'e eklenen anlık alanlar.
//
// SÖZLEŞME:
//   - Dört sorgu, hepsi BEST-EFFORT: aile yoksa ya da sorgu hata verirse
//     alan boş kalır; NamespaceSummary hata DÖNMEZ (trend yolu kendi
//     hatasıyla bağımsız).
//   - İstek / limit tek sorguda iki kaynak (resource=~"cpu|memory").
//   - Son 24 sa yeniden başlama: increase(...[24h]) namespace toplamı; seri
//     yoksa nil (0 ≠ bilinmiyor).
//   - Bekleme sebebi pod başına (PodWaitingReasonQuery şekli, == 1 şart);
//     pod'un birden çok konteyneri farklı sebep taşırsa en kötüsü kalır.
//   - Blok kendi süre tavanıyla koşar (nsSummaryBudget): yavaş Thanos trend
//     yanıtını geciktirmez, tavan dolunca kalan sorgular gönderilmez.

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// nsSummaryBudget — dört sorgunun toplam süre tavanı. Ayar değil; var
// olması yalnız testin tavanı kısaltabilmesi için.
var nsSummaryBudget = 4 * time.Second

// NamespaceSummary — namespace detayının anlık alanları (0 / boş = bilinmiyor).
type NamespaceSummary struct {
	CPURequestCores float64 `json:"cpuRequestCores,omitempty"`
	CPULimitCores   float64 `json:"cpuLimitCores,omitempty"`
	MemRequestBytes float64 `json:"memRequestBytes,omitempty"`
	MemLimitBytes   float64 `json:"memLimitBytes,omitempty"`
	// Son 24 saatteki yeniden başlama toplamı; nil = seri yok.
	Restarts24h *int `json:"restarts24h,omitempty"`
	// Pod → şu anki bekleme sebebi (CrashLoopBackOff, ImagePullBackOff …).
	Waiting map[string]string `json:"waiting,omitempty"`
}

// nsResourceSumQuery — namespace'in istek ya da limit toplamı, kaynak başına.
// kind: "requests" | "limits".
func nsResourceSumQuery(kind, namespace string) string {
	return fmt.Sprintf(
		`sum by (resource) (kube_pod_container_resource_%s{resource=~"cpu|memory",namespace="%s"})`,
		kind, escapeLabelValue(namespace))
}

// nsRestarts24hQuery — son 24 saatte namespace'teki yeniden başlama sayısı.
func nsRestarts24hQuery(namespace string) string {
	return fmt.Sprintf(
		`sum(increase(kube_pod_container_status_restarts_total{namespace="%s"}[24h]))`,
		escapeLabelValue(namespace))
}

// waitingReasonRank — aynı pod'un konteynerleri farklı sebep taşırsa satıra
// en kötüsü çıkar: imaj / yapılandırma hatası > çökme döngüsü > diğer.
func waitingReasonRank(r string) int {
	switch r {
	case "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "InvalidImageName":
		return 3
	case "CrashLoopBackOff":
		return 2
	case "":
		return 0
	default:
		return 1
	}
}

// NamespaceSummary — anlık alanları toplar (best-effort, hata dönmez).
func (s *Service) NamespaceSummary(ctx context.Context, c ClusterConfig, namespace string) NamespaceSummary {
	ctx, cancel := context.WithTimeout(ctx, nsSummaryBudget)
	defer cancel()
	var out NamespaceSummary
	resPair := func(set func(cpu, mem float64)) func([]promSeries) {
		return func(series []promSeries) {
			var cpu, mem float64
			for _, ser := range series {
				v, ok := sampleValue(ser.Value)
				if !ok {
					continue
				}
				switch ser.Metric["resource"] {
				case "cpu":
					cpu = v
				case "memory":
					mem = v
				}
			}
			set(cpu, mem)
		}
	}
	steps := []struct {
		query string
		apply func([]promSeries)
	}{
		{nsResourceSumQuery("requests", namespace), resPair(func(cpu, mem float64) { out.CPURequestCores, out.MemRequestBytes = cpu, mem })},
		{nsResourceSumQuery("limits", namespace), resPair(func(cpu, mem float64) { out.CPULimitCores, out.MemLimitBytes = cpu, mem })},
		{nsRestarts24hQuery(namespace), func(series []promSeries) {
			for _, ser := range series {
				if v, ok := sampleValue(ser.Value); ok {
					n := int(v + 0.5) // increase() ekstrapolasyonu kesirli döner
					out.Restarts24h = &n
					return
				}
			}
		}},
		{PodWaitingReasonQuery(regexp.QuoteMeta(namespace), ""), func(series []promSeries) {
			for _, ser := range series {
				pod, reason := ser.Metric["pod"], ser.Metric["reason"]
				if pod == "" || reason == "" {
					continue
				}
				if out.Waiting == nil {
					out.Waiting = map[string]string{}
				}
				if waitingReasonRank(reason) > waitingReasonRank(out.Waiting[pod]) {
					out.Waiting[pod] = reason
				}
			}
		}},
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
