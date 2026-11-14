package thanos

// v0.10.2168 (Clusters S3, plan docs/frontend/clusters-shadcn-plan.md §2–§3)
// — /api/clusters/nodes satırına node başına kapasite / ayrılabilir /
// istek-limit toplamı / koşul / kube_node_info etiketleri.
//
// SÖZLEŞME:
//   - Yeni alanların hepsi BEST-EFFORT: aile yoksa ya da sorgu hata verirse
//     alan boş kalır, satır ve mevcut alanlar (cpu/mem/pct/rol/pod) aynen
//     gelir, NodeMetrics hata DÖNMEZ.
//   - Anahtar KSM `node` etiketi; kube_node_info ile adı çözülemeyen satır
//     (ham instance) KSM alanı almaz ama node_exporter yedeğiyle kapasiteyi
//     (çekirdek sayısı, MemTotal) yine taşır.
//   - Genişletilmiş blok kendi süre tavanıyla koşar (nodeExtraBudget) ve
//     çağıranın ctx'ine bağlıdır: yavaş Thanos mevcut alanları geciktirmez.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func nodeResSample(node, resource, v string) string {
	return `{"metric":{"node":"` + node + `","resource":"` + resource + `"},"value":[1784271068,"` + v + `"]}`
}

func nodeCondSample(node, condition, status string) string {
	return `{"metric":{"node":"` + node + `","condition":"` + condition + `","status":"` + status + `"},"value":[1784271068,"1"]}`
}

// Fixture anahtarları (fakeQuerier alt dize eşler; birbirini kapsamazlar).
const (
	fxCapacity    = `kube_node_status_capacity{resource=~"cpu|memory"`
	fxPodCapacity = `kube_node_status_capacity{resource="pods"`
	fxAllocatable = "kube_node_status_allocatable"
	fxRequests    = "kube_pod_container_resource_requests"
	fxLimits      = "kube_pod_container_resource_limits"
	fxCondition   = "kube_node_status_condition"
)

// nodeCapFixture — nodeBase (worker-1 = 10.0.1.5, 2 çekirdek kullanım,
// 1000 bayt MemTotal, 500 boş) + çekirdek sayısı + tam KSM kapasite ailesi.
func nodeCapFixture() map[string]string {
	m := nodeBase()
	m[`mode="idle"`] = vec(nodeSample("10.0.1.5:9100", "8"))
	m["kube_node_info"] = `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"node":"worker-1","internal_ip":"10.0.1.5","kubelet_version":"v1.29.6","os_image":"Linux 9.4","kernel_version":"5.14.0-427","container_runtime_version":"cri-o://1.29.5"},"value":[1784271068,"1"]}]}}`
	m[fxCapacity] = vec(nodeResSample("worker-1", "cpu", "16"), nodeResSample("worker-1", "memory", "4000"))
	m[fxPodCapacity] = podsByNodeVec("worker-1", "250")
	m[fxAllocatable] = vec(nodeResSample("worker-1", "cpu", "15.5"), nodeResSample("worker-1", "memory", "3600"))
	m[fxRequests] = vec(nodeResSample("worker-1", "cpu", "6"), nodeResSample("worker-1", "memory", "2000"))
	m[fxLimits] = vec(nodeResSample("worker-1", "cpu", "20"), nodeResSample("worker-1", "memory", "5000"))
	m[fxCondition] = vec(
		nodeCondSample("worker-1", "Ready", "true"),
		nodeCondSample("worker-1", "MemoryPressure", "true"),
		nodeCondSample("worker-1", "DiskPressure", "true"))
	return m
}

func oneNodeRow(t *testing.T, m map[string]string) NodeRow {
	t.Helper()
	srv := fakeQuerier(t, "", m)
	defer srv.Close()
	rows, err := New().NodeMetrics(context.Background(), ClusterConfig{Name: "prod-a", URL: srv.URL, Enabled: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	return rows[0]
}

func TestNodeMetricsCapacityFields(t *testing.T) {
	r := oneNodeRow(t, nodeCapFixture())
	if r.Node != "worker-1" {
		t.Fatalf("node adı: %q", r.Node)
	}
	// Mevcut alanlar değişmedi (yüzde paydası node_exporter).
	if r.CPUCores != 2 || r.MemBytes != 500 || r.CPUPct != 25 || r.MemPct != 50 {
		t.Errorf("mevcut alanlar: cpu=%v mem=%v cpuPct=%v memPct=%v", r.CPUCores, r.MemBytes, r.CPUPct, r.MemPct)
	}
	if r.CPUCapacityCores != 16 || r.MemCapacityBytes != 4000 || r.PodCapacity != 250 {
		t.Errorf("kapasite: cpu=%v mem=%v pods=%v", r.CPUCapacityCores, r.MemCapacityBytes, r.PodCapacity)
	}
	if r.CPUAllocatableCores != 15.5 || r.MemAllocatableBytes != 3600 {
		t.Errorf("ayrılabilir: cpu=%v mem=%v", r.CPUAllocatableCores, r.MemAllocatableBytes)
	}
	if r.CPURequestCores != 6 || r.MemRequestBytes != 2000 || r.CPULimitCores != 20 || r.MemLimitBytes != 5000 {
		t.Errorf("istek/limit: %v %v %v %v", r.CPURequestCores, r.MemRequestBytes, r.CPULimitCores, r.MemLimitBytes)
	}
	if r.Ready != "true" || !reflect.DeepEqual(r.Pressure, []string{"DiskPressure", "MemoryPressure"}) {
		t.Errorf("koşul: ready=%q pressure=%v", r.Ready, r.Pressure)
	}
	if r.KubeletVersion != "v1.29.6" || r.OSImage != "Linux 9.4" || r.KernelVersion != "5.14.0-427" || r.ContainerRuntime != "cri-o://1.29.5" {
		t.Errorf("info etiketleri: %+v", r)
	}
}

// Eksik metrik: aile boş ya da hatalıysa YALNIZ o alanlar boş kalır.
func TestNodeMetricsMissingFamilyLeavesFieldsEmpty(t *testing.T) {
	const promErr = `{"status":"error","errorType":"execution","error":"nope"}`
	cases := []struct {
		name  string
		key   string
		empty func(NodeRow) bool
		kept  func(NodeRow) bool
	}{
		// KSM kapasitesi yok → node_exporter yedeği (8 çekirdek, MemTotal 1000).
		{"kapasite cpu|memory", fxCapacity,
			func(r NodeRow) bool { return r.CPUCapacityCores == 8 && r.MemCapacityBytes == 1000 },
			func(r NodeRow) bool { return r.PodCapacity == 250 && r.CPUAllocatableCores == 15.5 }},
		{"kapasite pods", fxPodCapacity,
			func(r NodeRow) bool { return r.PodCapacity == 0 },
			func(r NodeRow) bool { return r.CPUCapacityCores == 16 }},
		{"ayrılabilir", fxAllocatable,
			func(r NodeRow) bool { return r.CPUAllocatableCores == 0 && r.MemAllocatableBytes == 0 },
			func(r NodeRow) bool { return r.CPUCapacityCores == 16 && r.CPURequestCores == 6 }},
		{"istek", fxRequests,
			func(r NodeRow) bool { return r.CPURequestCores == 0 && r.MemRequestBytes == 0 },
			func(r NodeRow) bool { return r.CPULimitCores == 20 }},
		{"limit", fxLimits,
			func(r NodeRow) bool { return r.CPULimitCores == 0 && r.MemLimitBytes == 0 },
			func(r NodeRow) bool { return r.CPURequestCores == 6 }},
		{"koşul", fxCondition,
			func(r NodeRow) bool { return r.Ready == "" && r.Pressure == nil },
			func(r NodeRow) bool { return r.CPUCapacityCores == 16 }},
	}
	for _, c := range cases {
		for _, how := range []string{"boş", "hata"} {
			m := nodeCapFixture()
			if how == "boş" {
				delete(m, c.key)
			} else {
				m[c.key] = promErr
			}
			r := oneNodeRow(t, m)
			if !c.empty(r) {
				t.Errorf("%s (%s): alan boş kalmalıydı: %+v", c.name, how, r)
			}
			if !c.kept(r) {
				t.Errorf("%s (%s): öteki alanlar etkilenmemeliydi: %+v", c.name, how, r)
			}
			if r.CPUCores != 2 || r.MemBytes != 500 || r.MemPct != 50 {
				t.Errorf("%s (%s): mevcut alanlar bozuldu: %+v", c.name, how, r)
			}
		}
	}
}

// KSM hiç yok (yalnız node_exporter): satır gelir, kapasite yedekten.
func TestNodeMetricsNoKSMFallsBackToNodeExporter(t *testing.T) {
	m := nodeBase()
	delete(m, "kube_node_info")
	m[`mode="idle"`] = vec(nodeSample("10.0.1.5:9100", "8"))
	r := oneNodeRow(t, m)
	if r.Node != "10.0.1.5:9100" {
		t.Fatalf("ham instance bekleniyordu: %q", r.Node)
	}
	if r.CPUCapacityCores != 8 || r.MemCapacityBytes != 1000 {
		t.Errorf("node_exporter yedeği: cpu=%v mem=%v", r.CPUCapacityCores, r.MemCapacityBytes)
	}
	if r.PodCapacity != 0 || r.CPUAllocatableCores != 0 || r.Ready != "" || r.KubeletVersion != "" {
		t.Errorf("KSM alanları boş olmalıydı: %+v", r)
	}
}

// Çekirdek sayısı da yoksa CPU kapasitesi bilinmiyor (0) — uydurulmaz.
func TestNodeMetricsUnknownCapacityStaysZero(t *testing.T) {
	r := oneNodeRow(t, nodeBase())
	if r.CPUCapacityCores != 0 {
		t.Errorf("cpu kapasitesi uyduruldu: %v", r.CPUCapacityCores)
	}
	if r.MemCapacityBytes != 1000 {
		t.Errorf("mem kapasitesi MemTotal olmalıydı: %v", r.MemCapacityBytes)
	}
}

func TestNodeConditions(t *testing.T) {
	series := []promSeries{
		{Metric: map[string]string{"node": "a", "condition": "Ready", "status": "true"}},
		{Metric: map[string]string{"node": "b", "condition": "Ready", "status": "false"}},
		{Metric: map[string]string{"node": "b", "condition": "PIDPressure", "status": "true"}},
		{Metric: map[string]string{"node": "b", "condition": "DiskPressure", "status": "true"}},
		{Metric: map[string]string{"node": "b", "condition": "DiskPressure", "status": "true"}}, // çift scrape
		{Metric: map[string]string{"node": "c", "condition": "Ready", "status": "unknown"}},
		// Baskı koşulunun false/unknown serisi baskı DEĞİLDİR.
		{Metric: map[string]string{"node": "c", "condition": "MemoryPressure", "status": "false"}},
		// Tanınmayan koşul ve node'suz seri yok sayılır.
		{Metric: map[string]string{"node": "c", "condition": "NetworkUnavailable", "status": "true"}},
		{Metric: map[string]string{"condition": "Ready", "status": "true"}},
		// Tanınmayan Ready durumu yazılmaz.
		{Metric: map[string]string{"node": "d", "condition": "Ready", "status": "weird"}},
	}
	ready, pressure := nodeConditions(series)
	if !reflect.DeepEqual(ready, map[string]string{"a": "true", "b": "false", "c": "unknown"}) {
		t.Errorf("ready: %v", ready)
	}
	if !reflect.DeepEqual(pressure, map[string][]string{"b": {"DiskPressure", "PIDPressure"}}) {
		t.Errorf("pressure: %v", pressure)
	}
}

// Çakışan Ready serileri (çift scrape, geçiş anı): kötü olan kazanır.
func TestNodeConditionsWorstReadyWins(t *testing.T) {
	for _, order := range [][]string{{"true", "false"}, {"false", "true"}, {"unknown", "false", "true"}} {
		var series []promSeries
		for _, st := range order {
			series = append(series, promSeries{Metric: map[string]string{"node": "a", "condition": "Ready", "status": st}})
		}
		if ready, _ := nodeConditions(series); ready["a"] != "false" {
			t.Errorf("%v: ready=%q, false bekleniyordu", order, ready["a"])
		}
	}
}

func TestNodeResourcePairs(t *testing.T) {
	pair := func(v string) promSeries { return promSeries{Value: rawPair(t, `[1784271068,"`+v+`"]`)} }
	with := func(s promSeries, m map[string]string) promSeries { s.Metric = m; return s }
	series := []promSeries{
		with(pair("16"), map[string]string{"node": "a", "resource": "cpu"}),
		with(pair("4000"), map[string]string{"node": "a", "resource": "memory"}),
		with(pair("8"), map[string]string{"node": "b", "resource": "cpu"}),
		with(pair("9"), map[string]string{"node": "b", "resource": "ephemeral_storage"}),
		with(pair("3"), map[string]string{"resource": "cpu"}),
		with(pair("NaN"), map[string]string{"node": "c", "resource": "cpu"}),
	}
	got := nodeResourcePairs(series)
	want := map[string]nodeResPair{"a": {cpu: 16, mem: 4000}, "b": {cpu: 8}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNodeCapacityQueriesShape(t *testing.T) {
	two := map[string]string{
		"nodeCapacityQuery":    nodeCapacityQuery(),
		"nodeAllocatableQuery": nodeAllocatableQuery(),
		"nodeRequestsQuery":    nodeRequestsQuery(),
		"nodeLimitsQuery":      nodeLimitsQuery(),
	}
	for name, q := range two {
		// Etiket çarpanı 2 ile sınırlı: 500 node × 2 = maxSeriesParsed.
		if !strings.Contains(q, `resource=~"cpu|memory"`) || !strings.Contains(q, "by (node, resource)") {
			t.Errorf("%s: %s", name, q)
		}
		if strings.Contains(q, "namespace") {
			t.Errorf("%s: node toplamı namespace kalkanı taşımamalı: %s", name, q)
		}
	}
	for name, q := range map[string]string{"nodeRequestsQuery": nodeRequestsQuery(), "nodeLimitsQuery": nodeLimitsQuery()} {
		if !strings.HasPrefix(q, "sum by (node, resource)") || !strings.Contains(q, `node!=""`) {
			t.Errorf("%s: %s", name, q)
		}
	}
	if q := nodePodCapacityQuery(); !strings.Contains(q, `resource="pods"`) || !strings.Contains(q, "by (node)") {
		t.Errorf("nodePodCapacityQuery: %s", q)
	}
	q := nodeConditionQuery()
	for _, want := range []string{`condition="Ready"`, `condition=~"MemoryPressure|DiskPressure|PIDPressure"`, `status="true"`, "== 1", "by (node, condition, status)"} {
		if !strings.Contains(q, want) {
			t.Errorf("nodeConditionQuery %q yok: %s", want, q)
		}
	}
}

// slowExtraQuerier — mevcut sorgular anında, kapasite ailesi `delay` sonra
// (istemci vazgeçerse hemen) cevaplar; kapasite ailesine gelen istek sayılır.
func slowExtraQuerier(t *testing.T, delay time.Duration, extraHits *atomic.Int32) *httptest.Server {
	t.Helper()
	fast := fakeQuerier(t, "", nodeCapFixture())
	t.Cleanup(fast.Close)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		for _, fam := range []string{"kube_node_status_capacity", fxAllocatable, "kube_pod_container_resource_", fxCondition} {
			if strings.Contains(q, fam) {
				extraHits.Add(1)
				select {
				case <-time.After(delay):
				case <-r.Context().Done():
					return
				}
				break
			}
		}
		fast.Config.Handler.ServeHTTP(w, r)
	}))
}

// Bütçe: genişletilmiş blok kendi tavanında kesilir; mevcut alanlar gelir,
// hata yok, bekleme tavanla sınırlı (6 sorgu × gecikme kadar DEĞİL).
func TestNodeMetricsExtraBudgetCapsSlowThanos(t *testing.T) {
	old := nodeExtraBudget
	nodeExtraBudget = 150 * time.Millisecond
	defer func() { nodeExtraBudget = old }()

	var hits atomic.Int32
	srv := slowExtraQuerier(t, 3*time.Second, &hits)
	defer srv.Close()

	start := time.Now()
	rows, err := New().NodeMetrics(context.Background(), ClusterConfig{Name: "prod-a", URL: srv.URL, Enabled: true})
	took := time.Since(start)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	r := rows[0]
	if r.Node != "worker-1" || r.CPUCores != 2 || r.MemBytes != 500 || r.CPUPct != 25 {
		t.Errorf("mevcut alanlar gelmeliydi: %+v", r)
	}
	if r.PodCapacity != 0 || r.CPUAllocatableCores != 0 || r.Ready != "" {
		t.Errorf("tavan dolduktan sonra alan dolmamalıydı: %+v", r)
	}
	// Yedek node_exporter kapasitesi ağ beklemeden gelir.
	if r.CPUCapacityCores != 8 || r.MemCapacityBytes != 1000 {
		t.Errorf("yedek kapasite: %+v", r)
	}
	if took > 2*time.Second {
		t.Errorf("genişletilmiş blok tavanı aşıldı: %v", took)
	}
	// Tavan dolduktan sonra kalan sorgular HİÇ gönderilmez.
	if n := hits.Load(); n != 1 {
		t.Errorf("tavandan sonra sorgu gönderildi: %d istek", n)
	}
}

// Çağıranın ctx'i (handler'ın 10 sn'si) genişletilmiş blokta biterse satırlar
// yine döner — NodeMetrics hata vermez.
func TestNodeMetricsParentDeadlineDuringExtrasStillReturnsRows(t *testing.T) {
	var hits atomic.Int32
	srv := slowExtraQuerier(t, 3*time.Second, &hits)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	rows, err := New().NodeMetrics(ctx, ClusterConfig{Name: "prod-a", URL: srv.URL, Enabled: true})
	if err != nil || len(rows) != 1 || rows[0].MemBytes != 500 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("ctx tavanı aşıldı: %v", took)
	}
}

// Sorgu sayısı sabit, node sayısından bağımsız: genişletilmiş blok 6 istek.
func TestNodeMetricsExtraQueryCountIsFixed(t *testing.T) {
	var hits atomic.Int32
	srv := slowExtraQuerier(t, 0, &hits)
	defer srv.Close()
	if _, err := New().NodeMetrics(context.Background(), ClusterConfig{Name: "prod-a", URL: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := hits.Load(); got != 6 {
		t.Errorf("genişletilmiş sorgu sayısı %d, 6 bekleniyordu", got)
	}
}
