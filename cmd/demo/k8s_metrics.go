package main

// k8s_metrics.go — v0.10.2486: YALNIZ YEREL demo için OpenShift-biçimli
// Kubernetes metrik simülatörü.
//
// Neden: yerel yığında Remote Cluster yok, VictoriaMetrics'te kube_* /
// container_* serisi sıfır → pod/namespace/cluster grafikleri, restart'lar,
// fazlar, pod_oom dedektörü ve pod sayfası lokalde hiç icra edilmiyordu;
// span'ler ise k8s.pod.name / namespace / cluster'ı zaten basıyordu.
//
// Ne: demo'nun span'lerde kullandığı AYNI (cluster, namespace, iş yükü, pod,
// container) kümesi için uygulamanın sorguladığı serileri (internal/thanos,
// internal/api/resource_bundle.go, internal/entity/samples.go,
// internal/ml/pod_oom.go) üretir:
//   - kube-state-metrics: kube_pod_info, kube_pod_owner, kube_replicaset_owner,
//     kube_pod_status_phase, kube_pod_container_status_{ready,restarts_total,
//     last_terminated_reason,last_terminated_timestamp,waiting_reason},
//     kube_pod_container_resource_{requests,limits}, kube_deployment_*,
//     kube_statefulset_*, kube_node_{info,role,status_condition,
//     status_capacity,status_allocatable,created};
//   - kubelet cAdvisor: container_cpu_usage_seconds_total,
//     container_memory_working_set_bytes, container_cpu_cfs_{periods,
//     throttled_periods}_total (container satırları + container="" pod
//     satırı + container="POD" pause satırı — sorgular bunları süzer),
//     container_network_{receive,transmit}_bytes_total (pod düzeyi);
//   - node-exporter: node_cpu_seconds_total, node_memory_Mem{Total,
//     Available}_bytes, node_network_*_bytes_total, node_filesystem_*.
// Sayaçlar gerçek monoton sayaç (tik başına oran×dt birikir); gauge'lar yük
// modelinden (L.rateFactor / L.latencyFactor) + pod başına sabit sapma +
// yavaş dalga — düz çizgi değil.
//
// Taşıma: VictoriaMetrics `/api/v1/import/prometheus` (açık zaman damgalı
// metin). OTLP → collector → prometheusremotewrite yolu adları/etiketleri
// değiştirirdi (resource_to_telemetry_conversion k8s_* / service_name
// etiketleri ekler, job/instance'ı ezer) ve aynı seriler Coremetry'nin
// metric_points tablosuna da düşerdi; import API uygulamanın sorguladığı
// adları BİREBİR verir.
//
// Açma: DEMO_K8S_METRICS_URL (ör. http://victoriametrics:8428) — boş = KAPALI
// (varsayılan). DEMO_K8S_INCIDENT_EVERY (varsayılan 75m, taban 25m),
// DEMO_K8S_SCRAPE_INTERVAL (varsayılan 15s). Belge: docs/DEMO-REALISM.md.

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	k8sMi = 1024 * 1024
	k8sGi = 1024 * k8sMi

	// Olay hedefi: çok pod'lu, HTTP uçlu kanal iş yükü.
	k8sIncidentService  = "api-gateway"
	k8sIncidentEndpoint = "GET /api/v1/accounts/{id}/balance"
	// Boş durumlar: trafiksiz ama sağlıklı bir pod + başlangıçtan
	// k8sDeletedAfter sonra silinen (serileri duran) bir pod.
	k8sIdleService    = "notification-service"
	k8sIdlePod        = "notif-prod-3"
	k8sDeletedService = "statement-service"
	k8sDeletedPod     = "stmt-prod-3"
	k8sDeletedAfter   = 10 * time.Minute
)

// k8sProfile — dil başına kaynak profili (çekirdek, bayt).
type k8sProfile struct {
	cpuBase, cpuReq, cpuLim float64
	memReq, memLim, memFrac float64
}

var k8sProfiles = map[string]k8sProfile{
	"go":     {0.12, 0.1, 0.5, 256 * k8sMi, 512 * k8sMi, 0.36},
	"java":   {0.30, 0.5, 1.5, 1024 * k8sMi, 1536 * k8sMi, 0.62},
	"python": {0.20, 0.25, 1, 512 * k8sMi, 1024 * k8sMi, 0.50},
	"nodejs": {0.15, 0.2, 1, 384 * k8sMi, 768 * k8sMi, 0.45},
	"dotnet": {0.18, 0.25, 1, 512 * k8sMi, 1024 * k8sMi, 0.48},
	".net":   {0.18, 0.25, 1, 512 * k8sMi, 1024 * k8sMi, 0.48},
	"rust":   {0.06, 0.1, 0.5, 128 * k8sMi, 256 * k8sMi, 0.30},
	"ruby":   {0.16, 0.25, 1, 384 * k8sMi, 768 * k8sMi, 0.55},
}

type k8sPodRole int

const (
	k8sRoleNormal k8sPodRole = iota
	k8sRoleIncident
	k8sRoleIdle
	k8sRoleDeleted
)

type k8sPod struct {
	cluster, ns, kind, workload, rs, pod, container string
	node, ip, uid, image, cid                       string
	prof                                            k8sProfile
	lang                                            string
	channel                                         bool
	jitter, phase                                   float64
	role                                            k8sPodRole
	// sayaçlar (yalnız tick goroutine'i yazar)
	cpuSec, pauseSec, cfsPeriods, cfsThrottled, rx, tx float64
}

// k8sNodeModes — node_cpu_seconds_total mode'ları (sıra = cpuSec indeksi).
var k8sNodeModes = [4]string{"idle", "user", "system", "iowait"}

type k8sNode struct {
	cluster, name, ip, role string
	cpus                    int
	memTotal, created       float64
	cpuSec                  [][4]float64
	rx, tx                  float64
}

type k8sSim struct {
	start    time.Time
	sched    k8sSchedule
	pods     []*k8sPod
	nodes    []*k8sNode
	incident string // olay pod'u adı ("" = yok)
	last     time.Time
	load     func() (rate, lat float64)
}

// k8sMetrics — süreç geneli simülatör; nil = kapalı (span kancaları no-op).
var k8sMetrics *k8sSim

func k8sProfileFor(lang string) k8sProfile {
	if p, ok := k8sProfiles[lang]; ok {
		return p
	}
	return k8sProfiles["go"]
}

// k8sUID — SAF: pod adından deterministik UUID biçimli uid.
func k8sUID(s string) string {
	a, b := fnv32(s), fnv32(s+"#uid")
	c, d := fnv32("uid#"+s), fnv32(s+s)
	return fmt.Sprintf("%08x-%04x-4%03x-a%03x-%08x%04x", a, b>>16, b&0xfff, c&0xfff, d, c>>16)
}

func k8sGenName(base string) string {
	if podGeneration == "" {
		return base
	}
	return base + "-" + podGeneration
}

// buildK8sSim — SAF (saat + yük dışarıdan): `services` envanterinden
// cluster → node → pod topolojisi. Ad/etiketler span kaynağıyla AYNI
// yardımcılardan (namespaceFor, clusterFor, rsHashFor, podIP).
func buildK8sSim(svcs map[string]Service, start time.Time, sched k8sSchedule, load func() (float64, float64)) *k8sSim {
	sim := &k8sSim{start: start, sched: sched, last: start, load: load}
	names := make([]string, 0, len(svcs))
	for n := range svcs {
		names = append(names, n)
	}
	sort.Strings(names)
	add := func(s Service, pod string, role k8sPodRole) {
		p := &k8sPod{cluster: clusterFor(s.Name), ns: namespaceFor(s.Name), workload: s.Name,
			pod: pod, container: s.Name, ip: podIP(pod), uid: k8sUID(pod),
			image: imageNameFor(s.Name) + ":" + imageTagFor(), prof: k8sProfileFor(s.Lang), lang: s.Lang,
			channel: channelTierServices[s.Name], role: role,
			jitter: 0.85 + 0.30*float64(fnv32(pod+"#j")%1000)/1000,
			phase:  2 * math.Pi * float64(fnv32(pod+"#p")%1000) / 1000}
		p.cid = fmt.Sprintf("%08x%08x", fnv32(pod+"#cid"), fnv32("cid#"+pod))
		if statefulServices[s.Name] {
			p.kind = "StatefulSet"
		} else {
			p.kind, p.rs = "Deployment", s.Name+"-"+rsHashFor(s.Name)
		}
		sim.pods = append(sim.pods, p)
	}
	for _, n := range names {
		s := svcs[n]
		for i, pod := range s.Pods {
			role := k8sRoleNormal
			if n == k8sIncidentService && i == 1 && len(s.Pods) >= 3 {
				role = k8sRoleIncident
				sim.incident = pod
			}
			add(s, pod, role)
		}
		switch n {
		case k8sIdleService:
			add(s, k8sGenName(k8sIdlePod), k8sRoleIdle)
		case k8sDeletedService:
			add(s, k8sGenName(k8sDeletedPod), k8sRoleDeleted)
		}
	}
	// Node'lar: cluster başına ceil(pod/10) (≥3) worker + 1 infra (platform).
	perCluster := map[string][]*k8sPod{}
	for _, p := range sim.pods {
		perCluster[p.cluster] = append(perCluster[p.cluster], p)
	}
	for ci, c := range demoClusters {
		pods := perCluster[c]
		if len(pods) == 0 {
			continue
		}
		nw := (len(pods) + 9) / 10
		if nw < 3 {
			nw = 3
		}
		var workers []*k8sNode
		mk := func(name, role string, idx int) *k8sNode {
			nd := &k8sNode{cluster: c, name: name + "." + c + ".demo.local", role: role,
				ip: fmt.Sprintf("10.%d.0.%d", 128+ci, 10+idx), cpus: 8, memTotal: 32 * k8sGi,
				created: float64(start.Add(-time.Duration(40+idx) * 24 * time.Hour).Unix())}
			nd.cpuSec = make([][4]float64, nd.cpus)
			sim.nodes = append(sim.nodes, nd)
			return nd
		}
		for i := 0; i < nw; i++ {
			workers = append(workers, mk(fmt.Sprintf("worker-%d", i+1), "worker", i))
		}
		mk("infra-1", "infra", nw)
		for _, p := range pods {
			nd := workers[fnv32(p.pod)%uint32(len(workers))]
			p.node = nd.name
		}
	}
	return sim
}

// k8sPodSample — bir pod'un t anındaki anlık değerleri.
type k8sPodSample struct {
	exists          bool
	cpu, mem, thr   float64 // çekirdek, bayt, throttle oranı
	rxBps, txBps    float64
	ready           bool
	waiting         string
	restarts        int
	lastKill        time.Duration
	pauseCPU, pause float64
}

// sample — SAF: pod + geçen süre + yük → değerler.
func (sim *k8sSim) sample(p *k8sPod, el time.Duration, rate, lat float64) k8sPodSample {
	s := k8sPodSample{exists: true, ready: true, lastKill: -1, pauseCPU: 0.0005, pause: 1.2 * k8sMi}
	if p.role == k8sRoleDeleted && el >= k8sDeletedAfter {
		s.exists = false
		return s
	}
	sec := el.Seconds()
	wave := 1 + 0.08*math.Sin(2*math.Pi*sec/420+p.phase) + 0.03*math.Sin(2*math.Pi*sec/53+2*p.phase)
	act := 0.3 + 0.7*rate
	chMul := 1.0
	if p.channel {
		chMul = 1.6
	}
	s.cpu = p.prof.cpuBase * chMul * p.jitter * wave * act * (1 + 0.5*math.Max(0, lat-1))
	memJ := 0.9 + 0.2*(p.jitter-0.85)/0.30
	s.mem = p.prof.memLim * p.prof.memFrac * memJ * (0.94 + 0.06*rate)
	if p.lang == "java" { // GC testere dişi (3 dk)
		saw := math.Mod(sec/180+p.phase, 1)
		s.mem *= 0.97 + 0.06*saw
	}
	s.thr = 0.004 + 0.08*s.cpu/p.prof.cpuLim
	s.rxBps = 30e3 * chMul * chMul * p.jitter * wave * act
	s.txBps = 1.4 * s.rxBps
	switch p.role {
	case k8sRoleIdle:
		s.cpu, s.mem, s.thr = 0.003*wave, 0.15*p.prof.memLim, 0
		s.rxBps, s.txBps = 300*wave, 200*wave
	case k8sRoleIncident:
		st := incidentAt(el, sim.sched)
		s.ready, s.waiting, s.restarts, s.lastKill = st.ready, st.waiting, st.restarts, st.lastKill
		s.cpu *= st.cpuMult
		if st.memFrac > 0 {
			s.mem = st.memFrac * p.prof.memLim
		}
		if st.throttle > 0 {
			s.thr = st.throttle
		}
		if !st.ready { // endpoints'ten çıktı: trafik kardeşlere
			s.rxBps, s.txBps = s.rxBps*0.05, s.txBps*0.05
		}
	}
	s.thr = clamp01(s.thr)
	return s
}

// promWriter — Prometheus metin biçimi (VM import), satır başına bir örnek.
type promWriter struct {
	b  *bytes.Buffer
	ts string
	n  int
}

var promLabelEsc = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func (w *promWriter) add(name string, v float64, kv ...string) {
	w.b.WriteString(name)
	w.b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			w.b.WriteByte(',')
		}
		w.b.WriteString(kv[i])
		w.b.WriteString(`="`)
		w.b.WriteString(promLabelEsc.Replace(kv[i+1]))
		w.b.WriteByte('"')
	}
	w.b.WriteString("} ")
	w.b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
	w.b.WriteByte(' ')
	w.b.WriteString(w.ts)
	w.b.WriteByte('\n')
	w.n++
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// tick — sayaçları dt kadar ilerletir ve t anının tam serisini yazar.
// Tek goroutine'den çağrılır.
func (sim *k8sSim) tick(now time.Time, buf *bytes.Buffer) int {
	dt := now.Sub(sim.last).Seconds()
	if dt < 0 {
		dt = 0
	}
	sim.last = now
	el := now.Sub(sim.start)
	rate, lat := sim.load()
	w := &promWriter{b: buf, ts: strconv.FormatInt(now.UnixMilli(), 10)}

	type nodeAgg struct{ cpu, mem, rx, tx float64 }
	agg := map[string]*nodeAgg{}
	type wlKey struct{ cluster, ns, name string }
	type wlAgg struct {
		kind, rs    string
		spec, ready int
		cluster, ns string
	}
	wls := map[wlKey]*wlAgg{}
	var wlOrder []wlKey

	for _, p := range sim.pods {
		s := sim.sample(p, el, rate, lat)
		if !s.exists {
			continue
		}
		p.cpuSec += s.cpu * dt
		p.pauseSec += s.pauseCPU * dt
		periods := 10 * dt * math.Min(1, 0.2+s.cpu/p.prof.cpuLim) // 100 ms CFS periyodu, etkin oran
		p.cfsPeriods += periods
		p.cfsThrottled += periods * s.thr
		p.rx += s.rxBps * dt
		p.tx += s.txBps * dt
		a := agg[p.cluster+"|"+p.node]
		if a == nil {
			a = &nodeAgg{}
			agg[p.cluster+"|"+p.node] = a
		}
		a.cpu += s.cpu + s.pauseCPU
		a.mem += s.mem + s.pause
		a.rx += s.rxBps
		a.tx += s.txBps
		k := wlKey{p.cluster, p.ns, p.workload}
		wa := wls[k]
		if wa == nil {
			wa = &wlAgg{kind: p.kind, rs: p.rs, cluster: p.cluster, ns: p.ns}
			wls[k] = wa
			wlOrder = append(wlOrder, k)
		}
		wa.spec++
		if s.ready {
			wa.ready++
		}

		ksm := []string{"cluster", p.cluster, "job", "kube-state-metrics", "namespace", p.ns, "pod", p.pod, "uid", p.uid}
		ksmC := append(append([]string(nil), ksm...), "container", p.container)
		ownerKind, ownerName, createdKind := "ReplicaSet", p.rs, "ReplicaSet"
		if p.kind == "StatefulSet" {
			ownerKind, ownerName, createdKind = "StatefulSet", p.workload, "StatefulSet"
		}
		w.add("kube_pod_info", 1, append(append([]string(nil), ksm...), "node", p.node, "pod_ip", p.ip,
			"host_ip", sim.nodeIP(p.cluster, p.node), "created_by_kind", createdKind, "created_by_name", ownerName,
			"priority_class", "")...)
		w.add("kube_pod_owner", 1, append(append([]string(nil), ksm...), "owner_kind", ownerKind,
			"owner_name", ownerName, "owner_is_controller", "true")...)
		for _, ph := range [...]string{"Pending", "Running", "Succeeded", "Failed", "Unknown"} {
			w.add("kube_pod_status_phase", b2f(ph == "Running"), append(append([]string(nil), ksm...), "phase", ph)...)
		}
		w.add("kube_pod_container_status_ready", b2f(s.ready), ksmC...)
		w.add("kube_pod_container_status_restarts_total", float64(s.restarts), ksmC...)
		if s.lastKill >= 0 {
			w.add("kube_pod_container_status_last_terminated_reason", 1, append(append([]string(nil), ksmC...), "reason", "OOMKilled")...)
			w.add("kube_pod_container_status_last_terminated_timestamp", float64(sim.start.Add(s.lastKill).Unix()), ksmC...)
		}
		if s.waiting != "" {
			w.add("kube_pod_container_status_waiting_reason", 1, append(append([]string(nil), ksmC...), "reason", s.waiting)...)
		}
		for _, r := range [...]struct {
			metric, res, unit string
			v                 float64
		}{
			{"kube_pod_container_resource_requests", "cpu", "core", p.prof.cpuReq},
			{"kube_pod_container_resource_requests", "memory", "byte", p.prof.memReq},
			{"kube_pod_container_resource_limits", "cpu", "core", p.prof.cpuLim},
			{"kube_pod_container_resource_limits", "memory", "byte", p.prof.memLim},
		} {
			w.add(r.metric, r.v, append(append([]string(nil), ksmC...), "node", p.node, "resource", r.res, "unit", r.unit)...)
		}

		// kubelet cAdvisor: container satırı + pod (container="") + pause (POD).
		podID := "/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + strings.ReplaceAll(p.uid, "-", "_") + ".slice"
		cad := func(container, image, id string) []string {
			return []string{"cluster", p.cluster, "job", "kubelet", "metrics_path", "/metrics/cadvisor",
				"namespace", p.ns, "pod", p.pod, "container", container, "node", p.node, "image", image, "id", id}
		}
		ctr := cad(p.container, p.image, podID+"/crio-"+p.cid+".scope")
		pause := cad("POD", "registry.demo.local/ocp/pod:v4.15", podID+"/crio-"+p.cid[:8]+"00000000.scope")
		podRow := cad("", "", podID)
		w.add("container_cpu_usage_seconds_total", p.cpuSec, ctr...)
		w.add("container_cpu_usage_seconds_total", p.pauseSec, pause...)
		w.add("container_cpu_usage_seconds_total", p.cpuSec+p.pauseSec, podRow...)
		w.add("container_memory_working_set_bytes", s.mem, ctr...)
		w.add("container_memory_working_set_bytes", s.pause, pause...)
		w.add("container_memory_working_set_bytes", s.mem+s.pause, podRow...)
		w.add("container_cpu_cfs_periods_total", math.Round(p.cfsPeriods), ctr...)
		w.add("container_cpu_cfs_throttled_periods_total", math.Round(p.cfsThrottled), ctr...)
		w.add("container_network_receive_bytes_total", math.Round(p.rx), append(podRow, "interface", "eth0")...)
		w.add("container_network_transmit_bytes_total", math.Round(p.tx), append(podRow, "interface", "eth0")...)
	}

	for _, k := range wlOrder {
		wa := wls[k]
		base := []string{"cluster", wa.cluster, "job", "kube-state-metrics", "namespace", wa.ns}
		if wa.kind == "StatefulSet" {
			w.add("kube_statefulset_replicas", float64(wa.spec), append(append([]string(nil), base...), "statefulset", k.name)...)
			w.add("kube_statefulset_status_replicas_ready", float64(wa.ready), append(append([]string(nil), base...), "statefulset", k.name)...)
			continue
		}
		w.add("kube_replicaset_owner", 1, append(append([]string(nil), base...), "replicaset", wa.rs,
			"owner_kind", "Deployment", "owner_name", k.name, "owner_is_controller", "true")...)
		dep := append(append([]string(nil), base...), "deployment", k.name)
		w.add("kube_deployment_spec_replicas", float64(wa.spec), dep...)
		w.add("kube_deployment_status_replicas_ready", float64(wa.ready), dep...)
		avail := wa.ready > 0
		for _, st := range [...]string{"true", "false", "unknown"} {
			v := b2f((st == "true" && avail) || (st == "false" && !avail))
			w.add("kube_deployment_status_condition", v, append(append([]string(nil), dep...), "condition", "Available", "status", st)...)
		}
	}

	for _, nd := range sim.nodes {
		a := agg[nd.cluster+"|"+nd.name]
		if a == nil {
			a = &nodeAgg{}
		}
		overheadCPU, overheadMem := 0.35, 3.0*k8sGi
		if nd.role == "infra" { // router, registry, monitoring yığını
			overheadCPU, overheadMem = 1.4, 11.0*k8sGi
		}
		busy := math.Min(float64(nd.cpus)*0.98, a.cpu+overheadCPU*(0.8+0.2*rate))
		per := busy / float64(nd.cpus)
		rates := [4]float64{1 - per, per * 0.72, per * 0.22, per * 0.06}
		for c := range nd.cpuSec {
			for m := range rates {
				nd.cpuSec[c][m] += rates[m] * dt
			}
		}
		nd.rx += (a.rx + 50e3) * dt
		nd.tx += (a.tx + 40e3) * dt
		ksm := []string{"cluster", nd.cluster, "job", "kube-state-metrics", "node", nd.name}
		w.add("kube_node_info", 1, append(append([]string(nil), ksm...), "internal_ip", nd.ip,
			"kernel_version", "5.14.0-427.demo.x86_64", "os_image", "Demo CoreOS 415.92",
			"container_runtime_version", "cri-o://1.28.4", "kubelet_version", "v1.28.9",
			"kubeproxy_version", "v1.28.9", "system_uuid", k8sUID(nd.name), "provider_id", "")...)
		w.add("kube_node_role", 1, append(append([]string(nil), ksm...), "role", nd.role)...)
		for _, cond := range [...]string{"Ready", "MemoryPressure", "DiskPressure", "PIDPressure"} {
			for _, st := range [...]string{"true", "false", "unknown"} {
				v := b2f((cond == "Ready") == (st == "true") && st != "unknown")
				w.add("kube_node_status_condition", v, append(append([]string(nil), ksm...), "condition", cond, "status", st)...)
			}
		}
		for _, r := range [...]struct {
			res, unit   string
			capV, alloc float64
		}{{"cpu", "core", float64(nd.cpus), float64(nd.cpus) - 0.5}, {"memory", "byte", nd.memTotal, nd.memTotal - 2*k8sGi}, {"pods", "integer", 250, 250}} {
			w.add("kube_node_status_capacity", r.capV, append(append([]string(nil), ksm...), "resource", r.res, "unit", r.unit)...)
			w.add("kube_node_status_allocatable", r.alloc, append(append([]string(nil), ksm...), "resource", r.res, "unit", r.unit)...)
		}
		w.add("kube_node_created", nd.created, ksm...)
		// node-exporter: OpenShift'te instance = node adı (relabel); uygulama
		// internal_ip eşleşmezse instance'ı node adı sayar.
		ne := []string{"cluster", nd.cluster, "job", "node-exporter", "instance", nd.name}
		for c := range nd.cpuSec {
			for m, mode := range k8sNodeModes {
				w.add("node_cpu_seconds_total", nd.cpuSec[c][m], append(append([]string(nil), ne...), "cpu", strconv.Itoa(c), "mode", mode)...)
			}
		}
		w.add("node_memory_MemTotal_bytes", nd.memTotal, ne...)
		w.add("node_memory_MemAvailable_bytes", math.Max(0.05*nd.memTotal, nd.memTotal-a.mem-overheadMem), ne...)
		w.add("node_network_receive_bytes_total", math.Round(nd.rx), append(append([]string(nil), ne...), "device", "ens5")...)
		w.add("node_network_transmit_bytes_total", math.Round(nd.tx), append(append([]string(nil), ne...), "device", "ens5")...)
		fs := append(append([]string(nil), ne...), "device", "/dev/nvme0n1p4", "fstype", "xfs", "mountpoint", "/sysroot")
		w.add("node_filesystem_size_bytes", 120*k8sGi, fs...)
		w.add("node_filesystem_avail_bytes", 120*k8sGi*(0.52+0.03*math.Sin(el.Hours())), fs...)
	}
	return w.n
}

func (sim *k8sSim) nodeIP(cluster, node string) string {
	for _, nd := range sim.nodes {
		if nd.cluster == cluster && nd.name == node {
			return nd.ip
		}
	}
	return ""
}

// ── span kancaları (simülatör kapalıyken no-op) ──────────────────────────

// k8sSpanPenalty — olay pod'unun olay ucundaki EK gecikmesi (rampada
// bellek baskısı → ~3×). Yalnız sabit alanları okur (yarışsız).
func k8sSpanPenalty(svc, pod, endpoint string, base time.Duration) time.Duration {
	sim := k8sMetrics
	if sim == nil || svc != k8sIncidentService || endpoint != k8sIncidentEndpoint || pod != sim.incident {
		return 0
	}
	st := incidentAt(time.Since(sim.start), sim.sched)
	return time.Duration(float64(base) * (st.slow - 1))
}

// k8sPodNotReady — olay pod'u şu an endpoints dışında mı (pickPod kardeşe yönlenir).
func k8sPodNotReady(svc, pod string) bool {
	sim := k8sMetrics
	if sim == nil || svc != k8sIncidentService || pod != sim.incident {
		return false
	}
	return !incidentAt(time.Since(sim.start), sim.sched).ready
}

func envDuration(key string, def, min time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Printf("  %s=%q geçersiz, varsayılan %s", key, v, def)
		return def
	}
	if d < min {
		return min
	}
	return d
}

// startK8sMetrics — DEMO_K8S_METRICS_URL doluysa simülatörü kurar ve itme
// döngüsünü başlatır (rollPodGeneration'dan SONRA çağrılır).
func startK8sMetrics(ctx context.Context, wg *sync.WaitGroup) {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("DEMO_K8S_METRICS_URL")), "/")
	if base == "" {
		return
	}
	period := envDuration("DEMO_K8S_INCIDENT_EVERY", 75*time.Minute, k8sMinPeriod)
	every := envDuration("DEMO_K8S_SCRAPE_INTERVAL", 15*time.Second, 5*time.Second)
	sim := buildK8sSim(services, time.Now(), defaultK8sSchedule(period),
		func() (float64, float64) { return L.rateFactor(), L.latencyFactor() })
	k8sMetrics = sim
	sc := sim.sched
	log.Printf("  k8s metrics:      %s/api/v1/import/prometheus every %s — %d pods, %d nodes; OOM incident pod %s (%s, endpoint %q) every %s, first kill at +%s",
		base, every, len(sim.pods), len(sim.nodes), sim.incident, k8sIncidentService, k8sIncidentEndpoint, sc.period, sc.warmup+sc.ramp1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		client := &http.Client{Timeout: 10 * time.Second}
		url := base + "/api/v1/import/prometheus"
		t := time.NewTicker(every)
		defer t.Stop()
		var lastErr time.Time
		first := true
		for {
			var buf bytes.Buffer
			n := sim.tick(time.Now(), &buf)
			resp, err := client.Post(url, "text/plain", &buf)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 300 {
					err = fmt.Errorf("HTTP %d", resp.StatusCode)
				}
			}
			switch {
			case err != nil && time.Since(lastErr) > time.Minute:
				lastErr = time.Now()
				log.Printf("[k8s] push error: %v", err)
			case err == nil && first:
				first = false
				log.Printf("[k8s] first push ok: %d series", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}
