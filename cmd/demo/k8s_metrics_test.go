package main

// k8s_metrics_test.go — v0.10.2486: yerel K8s metrik simülatörünün saf
// parçaları: olay durum makinesi, sayaç monotonluğu, span kaynağıyla
// etiket eşliği, metin biçimi + kardinalite sınırı.

import (
	"bytes"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestK8sIncidentStateMachine(t *testing.T) {
	s := defaultK8sSchedule(75 * time.Minute)
	m := time.Minute
	cases := []struct {
		name     string
		at       time.Duration
		ready    bool
		waiting  string
		restarts int
		slowMin  float64
		memMin   float64
		killed   bool
	}{
		{"warmup", 2 * m, true, "", 0, 1, 0, false},
		{"ramp1 start", 4*m + time.Second, true, "", 0, 1, 0.39, false},
		{"ramp1 late (~3× slow)", 11 * m, true, "", 0, 3.0, 0.85, false},
		{"after kill1 not ready", 12*m + 10*time.Second, false, "", 1, 1, 0, true},
		{"recovered", 14 * m, true, "", 1, 1, 0, true},
		{"ramp2 late", 20*m + 30*time.Second, true, "", 1, 3.0, 0.85, true},
		{"kill2 crashloop", 21*m + 10*time.Second, false, "CrashLoopBackOff", 2, 1, 0, true},
		{"kill2 starting", 21*m + 40*time.Second, false, "", 2, 1, 0, true},
		{"quiet tail", 40 * m, true, "", 2, 1, 0, true},
		{"cycle 2 warmup", 76 * m, true, "", 2, 1, 0, true},
		{"cycle 2 kill1", 87*m + 5*time.Second, false, "", 3, 1, 0, true},
	}
	for _, c := range cases {
		st := incidentAt(c.at, s)
		if st.ready != c.ready || st.waiting != c.waiting || st.restarts != c.restarts {
			t.Fatalf("%s: got ready=%v waiting=%q restarts=%d", c.name, st.ready, st.waiting, st.restarts)
		}
		if st.slow < c.slowMin || (c.slowMin == 1 && st.slow > 1.01 && st.ready && st.memFrac == 0) {
			t.Fatalf("%s: slow=%.2f", c.name, st.slow)
		}
		if st.memFrac < c.memMin || st.memFrac > 0.995 {
			t.Fatalf("%s: memFrac=%.3f", c.name, st.memFrac)
		}
		if (st.lastKill >= 0) != c.killed || st.lastKill > c.at {
			t.Fatalf("%s: lastKill=%s", c.name, st.lastKill)
		}
	}
	// restart sayacı monoton, döngü başına tam 2
	prev := 0
	for el := time.Duration(0); el < 3*s.period; el += 5 * time.Second {
		r := incidentAt(el, s).restarts
		if r < prev {
			t.Fatalf("restarts decreased at %s: %d < %d", el, r, prev)
		}
		prev = r
	}
	if prev != 6 {
		t.Fatalf("3 cycles → 6 restarts, got %d", prev)
	}
	if defaultK8sSchedule(time.Minute).period != k8sMinPeriod {
		t.Fatal("period floor")
	}
}

var promLineRe = regexp.MustCompile(`^([a-zA-Z_:][a-zA-Z0-9_:]*)\{((?:[a-z_]+="[^"]*",?)*)\} (\S+) (\d+)$`)

type promSample struct {
	name   string
	labels map[string]string
	v      float64
}

func parseProm(t *testing.T, b []byte) []promSample {
	t.Helper()
	var out []promSample
	lre := regexp.MustCompile(`([a-z_]+)="([^"]*)"`)
	for _, ln := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		m := promLineRe.FindStringSubmatch(ln)
		if m == nil {
			t.Fatalf("bad line: %q", ln)
		}
		v, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			t.Fatalf("bad value: %q", ln)
		}
		l := map[string]string{}
		for _, kv := range lre.FindAllStringSubmatch(m[2], -1) {
			l[kv[1]] = kv[2]
		}
		out = append(out, promSample{m[1], l, v})
	}
	return out
}

func seriesKey(s promSample) string {
	var b strings.Builder
	b.WriteString(s.name)
	for _, k := range []string{"cluster", "namespace", "pod", "container", "node", "instance", "cpu", "mode", "resource", "phase", "reason", "condition", "status", "deployment", "statefulset", "replicaset"} {
		b.WriteString("|" + s.labels[k])
	}
	return b.String()
}

func newTestSim(t *testing.T) (*k8sSim, time.Time) {
	t.Helper()
	t.Cleanup(func() { podGeneration, imageTag = "", "" })
	podGeneration, imageTag = "r4242", "release.20261128.r4242"
	svcs := map[string]Service{}
	for n, s := range services {
		np := make([]string, len(s.Pods))
		for i, p := range s.Pods {
			np[i] = p
			if !statefulServices[n] {
				np[i] = p + "-r4242"
			}
		}
		s.Pods = np
		svcs[n] = s
	}
	start := time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)
	return buildK8sSim(svcs, start, defaultK8sSchedule(25*time.Minute), func() (float64, float64) { return 0.8, 1.0 }), start
}

func TestK8sCountersMonotonicAndOOM(t *testing.T) {
	sim, start := newTestSim(t)
	if sim.incident != "gw-prod-2-r4242" {
		t.Fatalf("incident pod: %q", sim.incident)
	}
	prev := map[string]float64{}
	counters := regexp.MustCompile(`_total$`)
	var lastRestart float64
	sawDeleted, deletedGone := false, false
	for el := 90 * time.Second; el <= 2*sim.sched.period+time.Minute; el += 90 * time.Second {
		var buf bytes.Buffer
		n := sim.tick(start.Add(el), &buf)
		ss := parseProm(t, buf.Bytes())
		if n != len(ss) {
			t.Fatalf("count mismatch %d vs %d", n, len(ss))
		}
		hasDeleted := false
		for _, s := range ss {
			if s.labels["cluster"] == "" {
				t.Fatalf("series without cluster label: %+v", s)
			}
			if s.labels["pod"] == "stmt-prod-3-r4242" {
				hasDeleted = true
			}
			if !counters.MatchString(s.name) {
				continue
			}
			k := seriesKey(s)
			if p, ok := prev[k]; ok && s.v < p {
				t.Fatalf("counter %s decreased at %s: %v < %v", k, el, s.v, p)
			}
			prev[k] = s.v
			if s.name == "kube_pod_container_status_restarts_total" && s.labels["pod"] == sim.incident {
				lastRestart = s.v
			}
		}
		if hasDeleted {
			sawDeleted = true
		} else if sawDeleted {
			deletedGone = true
		}
	}
	if lastRestart != 4 {
		t.Fatalf("2 cycles → 4 OOM restarts, got %v", lastRestart)
	}
	if !sawDeleted || !deletedGone {
		t.Fatalf("deleted pod series must exist then stop (saw=%v gone=%v)", sawDeleted, deletedGone)
	}
}

func TestK8sLabelsMatchSpanAttrs(t *testing.T) {
	sim, start := newTestSim(t)
	byPod := map[string]*k8sPod{}
	for _, p := range sim.pods {
		byPod[p.pod] = p
	}
	for _, s := range services {
		attrs := resAttrMap(k8sWorkloadAttrs(Service{Name: s.Name}))
		for _, base := range s.Pods {
			pod := base
			if !statefulServices[s.Name] {
				pod = base + "-r4242"
			}
			p := byPod[pod]
			if p == nil {
				t.Fatalf("span pod %s missing from simulator", pod)
			}
			if p.ns != attrs["k8s.namespace.name"] || p.container != attrs["k8s.container.name"] ||
				p.cluster != clusterFor(s.Name) || p.ip != podIP(pod) {
				t.Fatalf("%s: labels drift from span attrs: %+v vs %v", pod, p, attrs)
			}
			if rs := attrs["k8s.replicaset.name"]; rs != p.rs {
				t.Fatalf("%s: replicaset %q vs span %q", pod, p.rs, rs)
			}
			if sts := attrs["k8s.statefulset.name"]; sts != "" && p.kind != "StatefulSet" {
				t.Fatalf("%s: STS kind", pod)
			}
		}
	}
	// metin çıktısında: kube_pod_info node'u gerçek bir kube_node_info node'u,
	// kube_pod_owner RS → kube_replicaset_owner Deployment = servis adı.
	var buf bytes.Buffer
	sim.tick(start.Add(time.Minute), &buf)
	nodes, rsOwner := map[string]bool{}, map[string]string{}
	ss := parseProm(t, buf.Bytes())
	for _, s := range ss {
		switch s.name {
		case "kube_node_info":
			nodes[s.labels["cluster"]+"/"+s.labels["node"]] = true
		case "kube_replicaset_owner":
			rsOwner[s.labels["cluster"]+"/"+s.labels["namespace"]+"/"+s.labels["replicaset"]] = s.labels["owner_name"]
		}
	}
	for _, s := range ss {
		switch s.name {
		case "kube_pod_info":
			if !nodes[s.labels["cluster"]+"/"+s.labels["node"]] {
				t.Fatalf("pod %s on unknown node %s", s.labels["pod"], s.labels["node"])
			}
		case "kube_pod_owner":
			if s.labels["owner_kind"] == "ReplicaSet" {
				p := byPod[s.labels["pod"]]
				if rsOwner[s.labels["cluster"]+"/"+s.labels["namespace"]+"/"+s.labels["owner_name"]] != p.workload {
					t.Fatalf("pod %s: RS owner chain broken", s.labels["pod"])
				}
			}
		}
	}
}

func TestK8sSeriesBounded(t *testing.T) {
	sim, start := newTestSim(t)
	var buf bytes.Buffer
	n := sim.tick(start.Add(time.Minute), &buf)
	// pod başına ~26, node başına ~59, iş yükü başına ≤6
	if max := 26*len(sim.pods) + 60*len(sim.nodes) + 6*len(services); n > max {
		t.Fatalf("series %d > bound %d", n, max)
	}
	t.Logf("pods=%d nodes=%d series=%d", len(sim.pods), len(sim.nodes), n)
}

func TestK8sSpanPenaltyOnlyIncidentPodAndEndpoint(t *testing.T) {
	t.Cleanup(func() { k8sMetrics = nil })
	k8sMetrics = &k8sSim{start: time.Now().Add(-11 * time.Minute), sched: defaultK8sSchedule(75 * time.Minute), incident: "gw-prod-2-x"}
	base := 100 * time.Millisecond
	if d := k8sSpanPenalty("api-gateway", "gw-prod-2-x", k8sIncidentEndpoint, base); d < 2*base {
		t.Fatalf("incident pod late ramp penalty %s", d)
	}
	if k8sSpanPenalty("api-gateway", "gw-prod-1-x", k8sIncidentEndpoint, base) != 0 ||
		k8sSpanPenalty("api-gateway", "gw-prod-2-x", "POST /api/v1/transfers", base) != 0 {
		t.Fatal("siblings / other endpoints must stay normal")
	}
	k8sMetrics = nil
	if k8sSpanPenalty("api-gateway", "gw-prod-2-x", k8sIncidentEndpoint, base) != 0 || k8sPodNotReady("api-gateway", "gw-prod-2-x") {
		t.Fatal("disabled simulator must be a no-op")
	}
}
