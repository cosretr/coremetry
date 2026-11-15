package api

// copilot_pods_thanos.go — v0.10.1250 (operatör: "pod'ları nasıl" cevabı
// "0 pod" diyordu; Infrastructure sekmesi aynı servisin pod'larını Thanos'ta
// (kube-state + cAdvisor) buluyor). Pod sorusu artık Thanos'a da bakar:
// v0.10.2239 (S4): seçici entity.Resolver'ın svc → pod komşuları (cluster
// başına tam adlar; entity_service_pods.go), pod'u olan her cluster
// paralel ve 6 sn tavanlı (getClusterPods'un bütçesi). Bulursa cluster başına
// pod/çalışan/restart/CPU/bellek özeti + en çok kaynak tüketen pod'lar; ve
// HTTP metrikleri (VictoriaMetrics) için deterministik grafik kartları.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	thanosPodsPerCluster = 6 * time.Second
	thanosPodsParallel   = 6
	thanosPodsListMax    = 8
)

type thanosPodsResult struct {
	Rows   []thanos.PodRow
	Errors []string // okunamayan cluster adları
	// Unknown — v0.10.2239 (S4): entity deposunda servisin pod'u yok; Thanos
	// sorgulanmadı.
	Unknown bool
}

// guidedThanosPods — etkin cluster'larda servisin pod'ları (Thanos yoksa boş).
func (s *Server) guidedThanosPods(ctx context.Context, service string) thanosPodsResult {
	var out thanosPodsResult
	if s.thanos == nil || !s.thanos.HasEnabledClusters() || service == "" {
		return out
	}
	sel := s.servicePodSelector(ctx, service, time.Time{})
	if sel.Unknown() {
		out.Unknown = true
		return out
	}
	var clusters []thanos.ClusterConfig
	for _, c := range s.thanosEnabledClusters() {
		if sel.ByCluster[c.EffectiveID()] != "" {
			clusters = append(clusters, c)
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, thanosPodsParallel)
	for _, c := range clusters {
		wg.Add(1)
		go func(c thanos.ClusterConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, thanosPodsPerCluster)
			defer cancel()
			rows, _, err := s.thanos.PodMetrics(cctx, c, sel.ByCluster[c.EffectiveID()])
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				out.Errors = append(out.Errors, c.Name)
				return
			}
			for i := range rows {
				if rows[i].Cluster == "" {
					rows[i].Cluster = c.Name
				}
			}
			out.Rows = append(out.Rows, rows...)
		}(c)
	}
	wg.Wait()
	sort.Strings(out.Errors)
	return out
}

// thanosEnabledClusters — v0.10.1276: etkin cluster'ların GERÇEK yapılandırması
// (snapshot maskeli; ClusterByName gerçeğini verir). Ad sırası snapshot sırası.
func (s *Server) thanosEnabledClusters() []thanos.ClusterConfig {
	var clusters []thanos.ClusterConfig
	if s.thanos == nil {
		return nil
	}
	for _, c := range s.thanos.Snapshot().Clusters {
		if !c.Enabled {
			continue
		}
		if cfg, ok := s.thanos.ClusterByName(c.Name); ok {
			clusters = append(clusters, cfg)
		}
	}
	return clusters
}

func fmtGiB(b float64) string {
	if b >= 1<<30 {
		return fmt.Sprintf("%.1f GiB", b/(1<<30))
	}
	return fmt.Sprintf("%.0f MiB", b/(1<<20))
}

// renderThanosPodsTR — SAF: cluster başına özet + kaynakça en ağır pod'lar.
func renderThanosPodsTR(res thanosPodsResult, service string) string {
	if res.Unknown {
		return podsUnknownTR(service) + ".\n"
	}
	if len(res.Rows) == 0 && len(res.Errors) == 0 {
		return ""
	}
	var b strings.Builder
	if len(res.Rows) == 0 {
		fmt.Fprintf(&b, "Thanos (kube-state): %s için pod bulunamadı", service)
	} else {
		type agg struct {
			pods, running, restarts int
			cpu, mem                float64
		}
		by := map[string]*agg{}
		var names []string
		for _, r := range res.Rows {
			a := by[r.Cluster]
			if a == nil {
				a = &agg{}
				by[r.Cluster] = a
				names = append(names, r.Cluster)
			}
			a.pods++
			if r.Phase == "" || strings.EqualFold(r.Phase, "Running") {
				a.running++
			}
			a.restarts += r.Restarts
			a.cpu += r.CPUCores
			a.mem += r.MemBytes
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "Thanos (kube-state + cAdvisor) pod envanteri — %s, %d pod, %d cluster:\n", service, len(res.Rows), len(names))
		for _, n := range names {
			a := by[n]
			fmt.Fprintf(&b, "- %s: %d pod (%d çalışıyor), restart %d, CPU %.2f core, bellek %s\n", n, a.pods, a.running, a.restarts, a.cpu, fmtGiB(a.mem))
		}
		rows := append([]thanos.PodRow(nil), res.Rows...)
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Restarts != rows[j].Restarts {
				return rows[i].Restarts > rows[j].Restarts
			}
			return rows[i].MemBytes > rows[j].MemBytes
		})
		if len(rows) > thanosPodsListMax {
			rows = rows[:thanosPodsListMax]
		}
		b.WriteString("En çok restart / bellek:\n")
		for _, r := range rows {
			phase := r.Phase
			if phase == "" {
				phase = "Running"
			}
			lim := ""
			if r.MemPct > 0 {
				lim = fmt.Sprintf(" (limitin %%%.0f'i)", r.MemPct)
			}
			fmt.Fprintf(&b, "- %s/%s: %s, restart %d, CPU %.2f core, bellek %s%s\n", r.Cluster, r.Pod, phase, r.Restarts, r.CPUCores, fmtGiB(r.MemBytes), lim)
		}
	}
	if len(res.Errors) > 0 {
		fmt.Fprintf(&b, "Okunamayan cluster (Thanos zaman aşımı/hata): %s\n", strings.Join(res.Errors, ", "))
	} else if len(res.Rows) == 0 {
		b.WriteString(".\n")
	}
	return b.String()
}

// podHTTPChartFences — v0.10.1250: pod sorusuna HTTP metrikleri (VictoriaMetrics
// RED, CosreChart source=metric) için iki deterministik kart: istek hızı ve p99.
func podHTTPChartFences(service string, rangeS int64) string {
	return chartFence(guidedChartSpec{Title: service + " · istek hızı (HTTP metrikleri)", Service: service, Agg: "rate", RangeS: rangeS, Source: "metric"}) +
		chartFence(guidedChartSpec{Title: service + " · p99 (HTTP metrikleri)", Service: service, Agg: "p99", RangeS: rangeS, Source: "metric"})
}
