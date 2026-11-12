package api

// mcp_service_pods.go — v0.10.1276 (CoSRE adım 7): service_health_sweep'in pod
// bölümünün Thanos adaptörü (Deps.ServicePods).
//
// Pod kümesi guidedThanosPods ile AYNI (thanosPodRegexFor: deployment | servis
// | ortam eksiz ad, Infrastructure sekmesinin seçicisi); cluster başına:
//   - thanos.PodMetrics — anlık CPU/bellek, limit %, faz, son sonlanma (kube-state);
//   - increase(kube_pod_container_status_restarts_total[pencere]) — penceredeki
//     restart ARTIŞI (KSM sayacı ömür boyu; ham sayaç puanlanmaz), PodFamilyQuery
//     şablonu, InstantSamples (paylaşımlı querier'da cluster matcher enjekte).
// Cluster referansları mcptools'tan gelir (servisin eşlenen cluster'ları, yoksa
// tüm etkinler); burada ClusterByRef ile çözülür, etkin olmayan/bilinmeyen düşer.
// Paralellik ve cluster başına 6 sn tavan guidedThanosPods'unkiyle aynı.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// mcpServicePodsMaxClusters — adaptörün kendi tavanı (mcptools ≤6 yollar).
const mcpServicePodsMaxClusters = 6

// mcpServicePodsOrNil — Thanos yoksa nil (bölüm dürüst skipped).
func (s *Server) mcpServicePodsOrNil() mcptools.ServicePodsFunc {
	if s.thanos == nil {
		return nil
	}
	return s.mcpServicePods
}

func (s *Server) mcpServicePods(ctx context.Context, service string, refs []string, from, to time.Time, opt mcptools.ServicePodsOpts) (mcptools.ServicePodsResult, error) {
	var out mcptools.ServicePodsResult
	if s.thanos == nil || !s.thanos.HasEnabledClusters() {
		return out, nil
	}
	out.Thanos = true
	if service == "" {
		return out, fmt.Errorf("service gerekli")
	}
	var clusters []thanos.ClusterConfig
	seen := map[string]bool{}
	if len(refs) == 0 {
		clusters = s.thanosEnabledClusters()
	}
	for _, ref := range refs {
		if c, ok := s.thanos.ClusterByRef(ref); ok && c.Enabled && !seen[c.EffectiveID()] {
			seen[c.EffectiveID()] = true
			clusters = append(clusters, c)
		}
	}
	if len(clusters) > mcpServicePodsMaxClusters {
		clusters = clusters[:mcpServicePodsMaxClusters]
	}
	re := s.thanosPodRegexFor(ctx, service)
	out.PodRegex = re
	window := to.Sub(from)
	if window < time.Minute {
		window = time.Minute
	}
	var offset time.Duration
	if now := time.Now(); now.Sub(to) >= time.Minute {
		offset = now.Sub(to).Truncate(time.Second)
	}
	out.Clusters = make([]mcptools.ServicePodsCluster, len(clusters))
	var wg sync.WaitGroup
	sem := make(chan struct{}, thanosPodsParallel)
	for i, c := range clusters {
		wg.Add(1)
		go func(i int, c thanos.ClusterConfig) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out.Clusters[i] = s.mcpServicePodsCluster(ctx, c, re, window, offset, opt)
		}(i, c)
	}
	wg.Wait()
	return out, nil
}

// mcpServicePodsCluster — tek cluster'ın okuması (yalnız kendi dönüşüne yazar).
func (s *Server) mcpServicePodsCluster(ctx context.Context, c thanos.ClusterConfig, re string, window, offset time.Duration, opt mcptools.ServicePodsOpts) mcptools.ServicePodsCluster {
	if opt.Nodes {
		return s.mcpServicePodsNodes(ctx, c, re, offset)
	}
	cl := mcptools.ServicePodsCluster{ID: c.EffectiveID(), Name: c.Name}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, thanosPodsPerCluster)
	defer cancel()
	rows, truncated, err := s.thanos.PodMetrics(cctx, c, re)
	restartExpr, qerr := thanos.PodFamilyQuery(thanos.PodFamilyRestarts, c.NamespaceFilter, re, window, offset, true, 500)
	cl.Query = fmt.Sprintf("PodMetrics pod=~%q (cAdvisor CPU/bellek + kube-state limit/faz/son sonlanma; anlık)", re)
	if qerr == nil {
		cl.Query += "\n" + restartExpr
	}
	if err != nil {
		cl.Err = err.Error()
		cl.DurationMs = time.Since(start).Milliseconds()
		return cl
	}
	for i := range rows {
		if rows[i].Cluster == "" {
			rows[i].Cluster = c.Name
		}
	}
	cl.Rows, cl.Truncated = rows, truncated
	if qerr == nil && len(rows) > 0 {
		if samples, rerr := s.thanos.InstantSamples(cctx, c, restartExpr); rerr == nil {
			cl.Restarts = make(map[string]float64, len(samples))
			for _, smp := range samples {
				cl.Restarts[smp.Labels["namespace"]+"\x00"+smp.Labels["pod"]] = smp.Value
			}
			cl.RestartsOK = true
		}
	}
	// v0.10.1287 (AI SRE Faz 1b, k8s kontrolü) — şu anki bekleme sebebi
	// (CrashLoopBackOff / ImagePullBackOff …); aynı pod seçicisi + cluster'ın
	// namespace kalkanı, anlık. Okunamazsa WaitingOK=false ("okunamadı ≠ yok").
	if opt.WaitingReasons && len(rows) > 0 {
		wq := thanos.PodWaitingReasonQuery(c.NamespaceFilter, re)
		cl.Query += "\n" + wq
		if samples, werr := s.thanos.InstantSamples(cctx, c, wq); werr == nil {
			cl.Waiting = make(map[string]string, len(samples))
			for _, smp := range samples {
				cl.Waiting[smp.Labels["namespace"]+"\x00"+smp.Labels["pod"]] = smp.Labels["reason"]
			}
			cl.WaitingOK = true
		}
	}
	if (opt.Replicas || opt.ClusterHealth || opt.JBossDS) && len(rows) > 0 {
		s.mcpServicePodsExtras(cctx, c, &cl, opt, re, window, offset)
	}
	cl.DurationMs = time.Since(start).Milliseconds()
	return cl
}

// mcpServicePodsExtras — v0.10.2042 (servis sağlık kontrolü dilim 2): pod'u
// bulunan cluster'da, AYNI cluster tavanı (cctx) içinde PARALEL üç anlık
// sorgu — iş yükü replikası (servis pod'larının namespace'i + pod adından
// türeyen iş yükü adları), node Ready sayımı, firing ALERTS. Biri düşerse
// yalnız o alan "okunamadı" kalır (okunamadı ≠ temiz).
//
// v0.10.2043 (dilim 3) — opt.JBossDS: datasource aile adları canlı keşifle
// (jbossDSNames, 10 dk önbellek), sonra şimdiki + pencere başı değerler
// paralel (2 sorgu). Ad bulunamazsa değer sorgusu HİÇ atılmaz.
func (s *Server) mcpServicePodsExtras(ctx context.Context, c thanos.ClusterConfig, cl *mcptools.ServicePodsCluster, opt mcptools.ServicePodsOpts, podRe string, window, offset time.Duration) {
	nsSet := map[string]bool{}
	var namespaces, pods []string
	for _, r := range cl.Rows {
		pods = append(pods, r.Pod)
		if r.Namespace != "" && !nsSet[r.Namespace] {
			nsSet[r.Namespace] = true
			namespaces = append(namespaces, r.Namespace)
		}
	}
	sort.Strings(namespaces)
	var wg sync.WaitGroup
	var rq, nq, aq string
	if opt.Replicas {
		cl.WorkloadNames = thanos.WorkloadNamesFromPods(pods, mcpWorkloadNamesMax)
		if len(cl.WorkloadNames) > 0 && len(namespaces) > 0 {
			rq = thanos.WorkloadReplicasQuery(c.NamespaceFilter, namespaces, cl.WorkloadNames)
			wg.Add(1)
			go func() {
				defer wg.Done()
				samples, err := s.thanos.InstantSamples(ctx, c, rq)
				if err != nil {
					cl.WorkloadsErr = err.Error()
					return
				}
				cl.Workloads, cl.WorkloadsOK = workloadReplicasFrom(samples), true
			}()
		} else {
			cl.WorkloadsOK = true // aranacak ad yok → "bulunamadı"
		}
	}
	if opt.ClusterHealth {
		nq, aq = thanos.NodeReadyQuery(), thanos.FiringAlertsQuery()
		wg.Add(2)
		go func() {
			defer wg.Done()
			samples, err := s.thanos.InstantSamples(ctx, c, nq)
			if err != nil {
				return
			}
			cl.NodesReady, cl.NodesNotReady = nodeReadyCounts(samples)
			cl.NodeReadyOK = true
		}()
		go func() {
			defer wg.Done()
			samples, err := s.thanos.InstantSamples(ctx, c, aq)
			if err != nil {
				return
			}
			cl.Alerts, cl.AlertsOK = clusterAlertsFrom(samples), true
		}()
	}
	var dsQueries []string
	if opt.JBossDS && len(namespaces) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			dsQueries = s.mcpServiceJBossDS(ctx, c, cl, podRe, namespaces, window, offset)
		}()
	}
	wg.Wait()
	for _, q := range append([]string{rq, nq, aq}, dsQueries...) {
		if q != "" {
			cl.Query += "\n" + q
		}
	}
}

// mcpServiceJBossDS — keşif (önbellekli) + değerler. Yalnız cl'nin DS*
// alanlarına yazar; çalışan sorguların metnini döner (kanıt).
func (s *Server) mcpServiceJBossDS(ctx context.Context, c thanos.ClusterConfig, cl *mcptools.ServicePodsCluster, podRe string, namespaces []string, window, offset time.Duration) []string {
	dq := thanos.JBossDSDiscoveryQuery(c.NamespaceFilter, namespaces, podRe)
	names, err := jbossDSDiscovery.names(ctx, c.EffectiveID()+"\x00"+dq, func(ctx context.Context) ([]string, error) {
		samples, err := s.thanos.InstantSamples(ctx, c, dq)
		if err != nil {
			return nil, err
		}
		var out []string
		for _, smp := range samples {
			if n := smp.Labels["__name__"]; thanos.JBossDSRole(n) != "" {
				out = append(out, n)
			}
		}
		sort.Strings(out)
		return out, nil
	})
	queries := []string{dq}
	if err != nil {
		cl.DSErr = err.Error()
		return queries
	}
	cl.DSDiscoveryOK, cl.DSNames = true, names
	if len(names) == 0 {
		return queries
	}
	cq := thanos.JBossDSValuesQuery(names, c.NamespaceFilter, namespaces, podRe, offset)
	pq := thanos.JBossDSValuesQuery(names, c.NamespaceFilter, namespaces, podRe, offset+window)
	var cur, prev []thanos.Sample
	var cerr, perr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); cur, cerr = s.thanos.InstantSamples(ctx, c, cq) }()
	go func() { defer wg.Done(); prev, perr = s.thanos.InstantSamples(ctx, c, pq) }()
	wg.Wait()
	if cerr != nil {
		cl.DSErr = cerr.Error()
		return append(queries, cq, pq)
	}
	cl.DS = mcptools.DatasourcePoolsFrom(cur, prev, perr == nil)
	return append(queries, cq, pq)
}

// jbossDSDiscoveryTTL — keşif sonucu (bulunan ya da BULUNAMAYAN adlar) bu
// süre önbellekte: ad kümesi deploy'la değişir, her tıklamada değil.
const jbossDSDiscoveryTTL = 10 * time.Minute

const jbossDSDiscoveryMax = 512

// dsDiscoveryCache — küçük süreç içi önbellek (anahtar = cluster + sorgu
// metni; sorgu servis pod seçicisini ve namespace'leri taşıdığından TÜM
// girdiler anahtarda). Taşınca tamamen boşaltılır (sınırlı bellek).
type dsDiscoveryCache struct {
	mu  sync.Mutex
	m   map[string]dsDiscoveryEntry
	now func() time.Time
}

type dsDiscoveryEntry struct {
	names []string
	at    time.Time
}

var jbossDSDiscovery = &dsDiscoveryCache{m: map[string]dsDiscoveryEntry{}, now: time.Now}

func (d *dsDiscoveryCache) names(ctx context.Context, key string, load func(context.Context) ([]string, error)) ([]string, error) {
	d.mu.Lock()
	if e, ok := d.m[key]; ok && d.now().Sub(e.at) < jbossDSDiscoveryTTL {
		d.mu.Unlock()
		return e.names, nil
	}
	d.mu.Unlock()
	names, err := load(ctx)
	if err != nil {
		return nil, err // hata önbelleğe girmez
	}
	d.mu.Lock()
	if len(d.m) >= jbossDSDiscoveryMax {
		d.m = map[string]dsDiscoveryEntry{}
	}
	d.m[key] = dsDiscoveryEntry{names: names, at: d.now()}
	d.mu.Unlock()
	return names, nil
}

const mcpWorkloadNamesMax = 10

// workloadReplicasFrom — SAF: replika sorgusunun örnekleri → iş yükü satırları.
func workloadReplicasFrom(samples []thanos.Sample) []mcptools.WorkloadReplicas {
	by := map[string]*mcptools.WorkloadReplicas{}
	var order []string
	for _, smp := range samples {
		kind, name := "Deployment", smp.Labels["deployment"]
		if name == "" {
			kind, name = "StatefulSet", smp.Labels["statefulset"]
		}
		if name == "" {
			continue
		}
		k := smp.Labels["namespace"] + "\x00" + kind + "\x00" + name
		w := by[k]
		if w == nil {
			w = &mcptools.WorkloadReplicas{Namespace: smp.Labels["namespace"], Kind: kind, Name: name}
			by[k] = w
			order = append(order, k)
		}
		switch smp.Labels["__name__"] {
		case "kube_deployment_spec_replicas", "kube_statefulset_replicas":
			w.Desired, w.DesiredOK = smp.Value, true
		case "kube_deployment_status_replicas_ready", "kube_statefulset_status_replicas_ready":
			w.Ready, w.ReadyOK = smp.Value, true
		}
	}
	sort.Strings(order)
	out := make([]mcptools.WorkloadReplicas, 0, len(order))
	for _, k := range order {
		out = append(out, *by[k])
	}
	return out
}

// nodeReadyCounts — SAF: status → sayı; false + unknown "hazır değil".
func nodeReadyCounts(samples []thanos.Sample) (ready, notReady int) {
	for _, smp := range samples {
		n := int(math.Round(smp.Value))
		if smp.Labels["status"] == "true" {
			ready += n
		} else {
			notReady += n
		}
	}
	return ready, notReady
}

// clusterAlertsFrom — SAF: alert sorgusunun örnekleri → satırlar.
func clusterAlertsFrom(samples []thanos.Sample) []mcptools.ClusterAlert {
	out := make([]mcptools.ClusterAlert, 0, len(samples))
	for _, smp := range samples {
		out = append(out, mcptools.ClusterAlert{Name: smp.Labels["alertname"], Severity: smp.Labels["severity"],
			Namespace: smp.Labels["namespace"], Count: int(math.Round(smp.Value))})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity == "critical"
		}
		return out[i].Name+out[i].Namespace < out[j].Name+out[j].Namespace
	})
	return out
}

// mcpServicePodsNodes — v0.10.1288 (AI SRE Faz 2, tembel node kontrolü):
// pod metrikleri OKUNMAZ. Üç anlık sorgu (aynı cluster tavanı):
//  1. thanos.PodNodeQuery — servis pod'larının node'u (KSM kube_pod_info);
//  2. thanos.NodeCPUPressureQuery — en çok servis pod'u taşıyan ≤3 node'un
//     CPU kullanımı (allocatable'ın %'si);
//  3. thanos.NodeTopPodsQuery — o node'lardaki en işlek 5 pod; servisin kendi
//     pod'u (1. sorgunun kümesi) Service=true (komşu sayılmaz).
//
// Eşleme boşsa NodesOK=true + Nodes boş ("node etiketi yok"); 2./3. sorgu
// düşerse NodeErr dolar, eşleme yine taşınır (okunamadı ≠ temiz).
func (s *Server) mcpServicePodsNodes(ctx context.Context, c thanos.ClusterConfig, re string, offset time.Duration) mcptools.ServicePodsCluster {
	cl := mcptools.ServicePodsCluster{ID: c.EffectiveID(), Name: c.Name}
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, thanosPodsPerCluster)
	defer cancel()
	defer func() { cl.DurationMs = time.Since(start).Milliseconds() }()
	pq := thanos.PodNodeQuery(c.NamespaceFilter, re)
	cl.Query = pq
	samples, err := s.thanos.InstantSamples(cctx, c, pq)
	if err != nil {
		cl.Err = err.Error()
		return cl
	}
	cl.NodesOK = true
	cl.PodNodes = make(map[string]string, len(samples))
	count := map[string]int{}
	for _, smp := range samples {
		n := smp.Labels["node"]
		if n == "" {
			continue
		}
		cl.PodNodes[smp.Labels["namespace"]+"\x00"+smp.Labels["pod"]] = n
		count[n]++
	}
	nodes := topNodesByPods(count, thanos.NodeCheckMaxNodes)
	if len(nodes) == 0 {
		return cl
	}
	press := map[string]*mcptools.NodePressure{}
	for _, n := range nodes {
		cl.Nodes = append(cl.Nodes, mcptools.NodePressure{Cluster: c.Name, Node: n})
	}
	for i := range cl.Nodes {
		press[cl.Nodes[i].Node] = &cl.Nodes[i]
	}
	if cq, qerr := thanos.NodeCPUPressureQuery(nodes, offset); qerr == nil {
		cl.Query += "\n" + cq
		if rows, rerr := s.thanos.InstantSamples(cctx, c, cq); rerr == nil {
			for _, smp := range rows {
				if p := press[smp.Labels["node"]]; p != nil {
					v := math.Round(smp.Value*10) / 10
					p.CPUPct = &v
				}
			}
		} else {
			cl.NodeErr = "node CPU baskısı okunamadı: " + rerr.Error()
		}
	}
	if tq, qerr := thanos.NodeTopPodsQuery(nodes, 5, offset); qerr == nil {
		cl.Query += "\n" + tq
		if rows, rerr := s.thanos.InstantSamples(cctx, c, tq); rerr == nil {
			for _, smp := range rows {
				p := press[smp.Labels["node"]]
				if p == nil {
					continue
				}
				ns, pod := smp.Labels["namespace"], smp.Labels["pod"]
				_, own := cl.PodNodes[ns+"\x00"+pod]
				p.TopPods = append(p.TopPods, mcptools.NodeTopPod{Namespace: ns, Pod: pod, Pct: math.Round(smp.Value*10) / 10, Service: own})
			}
		} else if cl.NodeErr == "" {
			cl.NodeErr = "node'daki pod'lar okunamadı: " + rerr.Error()
		}
	}
	return cl
}

// topNodesByPods — SAF: servis pod'u en çok olan n node (eşitlikte ada göre).
func topNodesByPods(count map[string]int, n int) []string {
	out := make([]string, 0, len(count))
	for k := range count {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
