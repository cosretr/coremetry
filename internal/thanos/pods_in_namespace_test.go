package thanos

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// v0.10.2192 — operator-reported (ocpma/digital): "en az 0/180 pod
// çalışıyor", pod satırlarında Status / Node / CPU "—". OpenShift KSM
// etiket şekli (openshift-monitoring/k8s kazıması: namespace/pod KSM'nin
// kendi değerleri, honorLabels) — fazla etiketler (container, endpoint,
// instance, job, prometheus, service, uid) join'i bozmaz. Kök neden:
// cluster-geneli faz / node / limit sorguları 1000-seri tavanında
// kırpılıyor, digital'in serileri kesilen kuyrukta kalıyordu.

func ksmSeries(ns, pod string, extra string, v string) string {
	return fmt.Sprintf(`{"metric":{"__name__":"x","container":"kube-rbac-proxy-main","endpoint":"https-main",`+
		`"instance":"10.128.2.14:8443","job":"kube-state-metrics","namespace":"%s","pod":"%s",`+
		`"prometheus":"openshift-monitoring/k8s","service":"kube-state-metrics","uid":"u-%s"%s},"value":[1784271068,"%s"]}`,
		ns, pod, pod, extra, v)
}

// clusterWideQuerier — ns-kapsamsız sorguya 1001 başka-namespace serisi +
// en sonda digital'inkini döner (tavan digital'i keser); kapsamlı sorguya
// yalnız digital'i.
func clusterWideQuerier(t *testing.T, seen *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		mu.Lock()
		*seen = append(*seen, q)
		mu.Unlock()
		scoped := strings.Contains(q, `namespace=~"digital"`)
		var series []string
		add := func(ns, pod string) {
			switch {
			case strings.Contains(q, "container_cpu_usage_seconds_total"):
				series = append(series, ksmSeries(ns, pod, "", "0.2"))
			case strings.Contains(q, "container_memory_working_set_bytes"):
				series = append(series, ksmSeries(ns, pod, "", "1073741824"))
			case strings.Contains(q, `kube_pod_container_resource_limits{resource="cpu"`):
				series = append(series, ksmSeries(ns, pod, "", "1"))
			case strings.Contains(q, "kube_pod_status_phase"):
				series = append(series, ksmSeries(ns, pod, `,"phase":"Running"`, "1"))
			case strings.Contains(q, "kube_pod_info"):
				series = append(series, fmt.Sprintf(`{"metric":{"namespace":"%s","pod":"%s","node":"ocpma-worker-3"},"value":[1784271068,"1"]}`, ns, pod))
			case strings.Contains(q, "kube_pod_container_status_restarts_total"):
				series = append(series, fmt.Sprintf(`{"metric":{"namespace":"%s","pod":"%s"},"value":[1784271068,"0"]}`, ns, pod))
			}
		}
		usage := strings.Contains(q, "container_")
		if usage || scoped { // cpu/mem: digital topk'un başında (satır var)
			add("digital", "bsa-digital-dashboard-prod-7d9f8c6b5-abcde")
		}
		if !scoped {
			for i := 0; i < 1001; i++ {
				add("aaa-other", fmt.Sprintf("p-%d", i))
			}
			if !usage { // KSM: digital kuyrukta, tavan keser
				add("digital", "bsa-digital-dashboard-prod-7d9f8c6b5-abcde")
			}
		}
		fmt.Fprint(w, vec(series...))
	}))
}

func TestPodMetricsInNamespaceJoinsPhaseNodeLimit(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	srv := clusterWideQuerier(t, &seen, &mu)
	defer srv.Close()
	s := New()
	c := ClusterConfig{Name: "ocpma", URL: srv.URL, Enabled: true}

	// Bug'ın yeniden üretimi: cluster-geneli envanterde digital satırı
	// cpu topk'ta var ama faz / node / limit / restart kesilmiş.
	all, _, err := s.PodMetrics(context.Background(), c, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range all {
		if r.Namespace == "digital" {
			found = true
			if r.Phase != "" || r.Node != "" {
				t.Fatalf("fixture no longer reproduces the cap: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("fixture: digital row missing from cluster-wide inventory")
	}

	seen = nil
	rows, trunc, err := s.PodMetricsInNamespace(context.Background(), c, "digital")
	if err != nil {
		t.Fatal(err)
	}
	if trunc || len(rows) != 1 {
		t.Fatalf("want 1 untruncated row, got trunc=%v %+v", trunc, rows)
	}
	r := rows[0]
	if r.Phase != "Running" || r.Node != "ocpma-worker-3" || r.CPUPct != 20 || r.RestartsUnknown {
		t.Fatalf("phase/node/cpu%%/restarts not joined: %+v", r)
	}
	for _, q := range seen {
		if !strings.Contains(q, `namespace=~"digital"`) {
			t.Errorf("unscoped query in namespace mode: %s", q)
		}
	}
}

func TestScopedNamespaceFilter(t *testing.T) {
	cases := []struct {
		filter, ns, want string
		ok               bool
	}{
		{"", "digital", "digital", true},
		{"digital|bank", "digital", "digital", true},
		{"dig.*", "digital", "digital", true},
		{"bank", "digital", "", false}, // kalkan: filtre dışı
		{"dig", "digital", "", false},  // tam-çapalı
		{"(", "digital", "", false},    // bozuk filtre gevşemez
		{"", "a.b", `a\.b`, true},      // regex meta kaçar
		{"", "", "", false},
	}
	for _, c := range cases {
		got, ok := scopedNamespaceFilter(c.filter, c.ns)
		if got != c.want || ok != c.ok {
			t.Errorf("scopedNamespaceFilter(%q,%q)=(%q,%v) want (%q,%v)", c.filter, c.ns, got, ok, c.want, c.ok)
		}
	}
}
