package api

// resource_bundle_test.go — v0.10.1431 (CoSRE kaynak incelemesi R1). Operatör:
// "CoSRE sorulan sorulara frontier model gibi anlamlandırarak gerekli
// toolset'lerini koşsun … bir pod'un CPU/memory metriklerini grafik olarak
// getirme; gerekirse GC'sine, JVM metriklerine bakıp LLM'e yorumlatma."
//
// Altın PromQL (aile × namespace kalkanı × regex), sorgu bütçesi (aile ≤4,
// all ≤7), konsol çözücü, istatistik + eşikler + KAYNAK ÖZETİ biçimi (≤12
// gövde satırı), önbellek anahtarı tüm girdiler, hedef kararı tablosu, sahte
// Thanos + VM ile demet (koş / cluster sor / pod sor / BULUNAMADI / okunamadı /
// Thanos yok / önbellek / JVM drill (v0.10.1432)), salt-okur kaynak pini.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestResourceQueriesGolden(t *testing.T) {
	re := `(orders-api)-.*`
	got := resourceQueries(resFamAll, re, "shop|shop-b", 120)
	want := []string{
		`topk(20, sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",container!="POD",pod=~"(orders-api)-.*",namespace=~"shop|shop-b"}[120s])))`,
		`topk(20, 100 * sum by (namespace, pod) (rate(container_cpu_cfs_throttled_periods_total{container!="",container!="POD",pod=~"(orders-api)-.*",namespace=~"shop|shop-b"}[120s])) / sum by (namespace, pod) (rate(container_cpu_cfs_periods_total{container!="",container!="POD",pod=~"(orders-api)-.*",namespace=~"shop|shop-b"}[120s])))`,
		`topk(20, sum by (namespace, pod) (container_memory_working_set_bytes{container!="",container!="POD",pod=~"(orders-api)-.*",namespace=~"shop|shop-b"}))`,
		`topk(20, sum by (namespace, pod) (kube_pod_container_status_restarts_total{pod=~"(orders-api)-.*",namespace=~"shop|shop-b"}))`,
		`sum by (namespace, pod, resource) (kube_pod_container_resource_limits{resource=~"cpu|memory",pod=~"(orders-api)-.*",namespace=~"shop|shop-b"})`,
		`max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason{pod=~"(orders-api)-.*",namespace=~"shop|shop-b"} == 1)`,
		`max by (namespace, pod, reason) (kube_pod_container_status_waiting_reason{pod=~"(orders-api)-.*",namespace=~"shop|shop-b"} == 1)`,
	}
	if len(got) != len(want) {
		t.Fatalf("all: %d sorgu, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Expr != want[i] {
			t.Errorf("sorgu %d (%s):\n got %s\nwant %s", i, got[i].ID, got[i].Expr, want[i])
		}
	}
	// NamespaceFilter boş → namespace eşleştiricisi yok; pod adı kaçışı + tırnak çerçevesi.
	q := resourceQueries(resFamCPU, `^(a"b)$`, "", 60)
	if q[0].Expr != `topk(20, sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",container!="POD",pod=~"^(a\"b)$"}[60s])))` {
		t.Errorf("kaçış/ns: %s", q[0].Expr)
	}
	if q[3].Expr != `sum by (namespace, pod, resource) (kube_pod_container_resource_requests{resource=~"cpu|memory",pod=~"^(a\"b)$"})` {
		t.Errorf("requests: %s", q[3].Expr)
	}
	if m := resourcePodLookupMatch("(orders-api)-.*", "shop"); m != `kube_pod_info{pod=~"(orders-api)-.*",namespace=~"shop"}` {
		t.Errorf("lookup seçicisi: %s", m)
	}
}

func TestResourceQueryBudget(t *testing.T) {
	want := map[string][]string{
		resFamCPU:      {resQCPU, resQThrottle, resQLimits, resQRequests},
		resFamMemory:   {resQMem, resQLimits, resQRequests, resQLastTerm},
		resFamRestarts: {resQRestarts, resQLastTerm, resQWaiting},
		resFamAll:      {resQCPU, resQThrottle, resQMem, resQRestarts, resQLimits, resQLastTerm, resQWaiting},
	}
	for fam, ids := range want {
		qs := resourceQueries(fam, "x-.*", "", 60)
		max := 4
		if fam == resFamAll {
			max = 7
		}
		if len(qs) > max {
			t.Errorf("%s: %d sorgu > %d", fam, len(qs), max)
		}
		var got []string
		for _, q := range qs {
			got = append(got, q.ID)
			if strings.Contains(q.Expr, "[") && !q.Range && q.ID != resQLimits {
				t.Errorf("%s: anlık sorguda aralık: %s", q.ID, q.Expr)
			}
		}
		if strings.Join(got, ",") != strings.Join(ids, ",") {
			t.Errorf("%s: %v, want %v", fam, got, ids)
		}
	}
}

func TestDecodeResResult(t *testing.T) {
	m := `[{"metric":{"pod":"a","namespace":"n"},"values":[[1700000000,"1.5"],[1700000030.5,"NaN"],[1700000060,"2"]]}]`
	s, err := decodeResResult("matrix", json.RawMessage(m))
	if err != nil || len(s) != 1 || len(s[0].Points) != 2 || s[0].Points[1] != [2]float64{1700000060000, 2} || s[0].Labels["pod"] != "a" {
		t.Fatalf("matrix: %+v %v", s, err)
	}
	v := `[{"metric":{"pod":"a","resource":"cpu"},"value":[1700000000,"0.5"]}]`
	s, err = decodeResResult("vector", json.RawMessage(v))
	if err != nil || len(s) != 1 || s[0].Points[0][1] != 0.5 {
		t.Fatalf("vector: %+v %v", s, err)
	}
	if _, err := decodeResResult("scalar", json.RawMessage(`[1,"1"]`)); err == nil {
		t.Error("scalar reddedilmeli")
	}
	if _, err := decodeResResult("vector", json.RawMessage(`[{"metric":{},"value":[1,"x"]}]`)); err == nil {
		t.Error("bozuk sayı hata vermeli")
	}
	if s, err := decodeResResult("matrix", nil); err != nil || s != nil {
		t.Error("boş sonuç nil olmalı")
	}
}

// riPts — 0..n-1 noktası, 30 sn arayla, f(i) değerli.
func riPts(n int, f func(i int) float64) [][2]float64 {
	base := float64(time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC).UnixMilli())
	out := make([][2]float64, n)
	for i := range out {
		out[i] = [2]float64{base + float64(i)*30000, f(i)}
	}
	return out
}

func riLabels(pod string, kv ...string) map[string]string {
	m := map[string]string{"namespace": "shop", "pod": pod}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

const riPodB = "orders-api-6d4f8b9c7d-b7m4p"

// riResults — sorunlu pod (x2k9q) + sağlıklı pod (b7m4p).
func riResults() map[string][]resSeries {
	const mib = 1 << 20
	return map[string][]resSeries{
		resQCPU: {
			{Labels: riLabels(riPod), Points: riPts(120, func(i int) float64 { return 0.2 + 0.75*float64(i)/119 })},
			{Labels: riLabels(riPodB), Points: riPts(120, func(int) float64 { return 0.1 })},
		},
		resQThrottle: {{Labels: riLabels(riPod), Points: riPts(120, func(i int) float64 { return float64(i % 40) })}},
		resQMem: {
			{Labels: riLabels(riPod), Points: riPts(120, func(i int) float64 { return float64(600*mib) + float64(i)*float64(2.5*mib) })},
			{Labels: riLabels(riPodB), Points: riPts(120, func(int) float64 { return 200 * mib })},
		},
		resQRestarts: {
			{Labels: riLabels(riPod), Points: riPts(120, func(i int) float64 { return float64(i / 50) })},
			{Labels: riLabels(riPodB), Points: riPts(120, func(int) float64 { return 5 })},
		},
		resQLimits: {
			{Labels: riLabels(riPod, "resource", "cpu"), Points: riPts(1, func(int) float64 { return 1 })},
			{Labels: riLabels(riPod, "resource", "memory"), Points: riPts(1, func(int) float64 { return 1024 * mib })},
			{Labels: riLabels(riPodB, "resource", "cpu"), Points: riPts(1, func(int) float64 { return 1 })},
		},
		resQLastTerm: {{Labels: riLabels(riPod, "reason", "OOMKilled"), Points: riPts(1, func(int) float64 { return 1 })}},
	}
}

func TestResourceStatsAndSummary(t *testing.T) {
	ps := resBuildStats(riResults(), 30)
	if len(ps) != 2 {
		t.Fatalf("2 pod bekleniyordu: %+v", ps)
	}
	resSortStats(ps)
	p, q := ps[0], ps[1]
	if p.Pod != riPod {
		t.Fatalf("kritik pod önce olmalı: %s", p.Pod)
	}
	if pct := p.CPUPctMax(); pct < 94.9 || pct > 95.1 {
		t.Errorf("CPU limit yüzdesi %v", pct)
	}
	if pct := p.MemPctMax(); pct < 87 || pct > 88 {
		t.Errorf("bellek limit yüzdesi %v", pct)
	}
	// 2.5 MiB / 30 sn = 300 MiB/sa
	if mib := p.MemSlopeH / (1 << 20); mib < 299 || mib > 301 {
		t.Errorf("bellek eğimi %v MiB/sa", mib)
	}
	if p.Restarts != 2 || p.LastReason != "OOMKilled" || q.Restarts != 0 {
		t.Errorf("restart: %d %q / %d", p.Restarts, p.LastReason, q.Restarts)
	}
	if q.MemPctMax() != -1 || q.CPUPctMax() < 9.9 || q.CPUPctMax() > 10.1 {
		t.Errorf("sağlıklı pod: mem %v cpu %v", q.MemPctMax(), q.CPUPctMax())
	}
	if p.CPUAboveS <= 0 || p.CPUAboveS%30 != 0 {
		t.Errorf("eşik üstü süre adımın katı olmalı: %d", p.CPUAboveS)
	}
	br := resBreaches(p)
	var kinds []string
	for _, b := range br {
		lvl := "w"
		if b.Crit {
			lvl = "c"
		}
		kinds = append(kinds, lvl+":"+strings.Fields(b.Text)[0])
	}
	if strings.Join(kinds, ",") != "c:CPU,w:CPU,w:bellek,w:pencerede" {
		t.Errorf("eşik aşımları: %v", kinds)
	}
	if len(resBreaches(q)) != 0 {
		t.Errorf("sağlıklı pod aşım üretti: %+v", resBreaches(q))
	}
	sum := renderResourceSummaryTR(ps, time.UTC)
	lines := strings.Split(strings.TrimSpace(sum), "\n")
	if !strings.HasPrefix(lines[0], "KAYNAK ÖZETİ (deterministik; model noktaları yeniden sayma") {
		t.Errorf("başlık: %s", lines[0])
	}
	if len(lines)-1 > resSummaryMaxRows {
		t.Errorf("gövde %d satır > %d", len(lines)-1, resSummaryMaxRows)
	}
	for _, w := range []string{
		"- " + riPod + ": CPU son 0.95 core, tepe 0.95 core (11:59; limitin %95'i)",
		"bellek son 897.5 MiB, tepe 897.5 MiB (11:59; limitin %88'i), eğilim yükseliyor (+300 MiB/sa)",
		"restart 2 (son sonlanma OOMKilled)",
		"- " + riPodB + ": CPU son 0.10 core, tepe 0.10 core",
		"limit yok",
		"EŞİK AŞIMI (eşikler: CPU limitin %80/%95'i, throttle %25, bellek limitin %80/%90'i, restart >0/≥3 (uyarı/kritik)):",
		"- KRİTİK " + riPod + ": CPU tepe 0.95 core = limitin %95'i",
		"- UYARI " + riPod + ": pencerede 2 restart (eşik >0 uyarı / ≥3 kritik); son sonlanma sebebi OOMKilled",
	} {
		if !strings.Contains(sum, w) {
			t.Errorf("özet %q taşımalı:\n%s", w, sum)
		}
	}
	// Aşım yoksa tek satır "yok" + eşikler.
	calm := renderResourceSummaryTR([]resPodStats{q}, time.UTC)
	if !strings.Contains(calm, "EŞİK AŞIMI: yok (eşikler:") {
		t.Errorf("sakin özet: %s", calm)
	}
	if !strings.Contains(renderResourceSummaryTR(nil, nil), "EŞİK AŞIMI: değerlendirilemedi") {
		t.Error("veri yok özeti")
	}
	// Çok pod: ≤6 pod satırı + "+N pod daha", gövde yine ≤12.
	var many []resPodStats
	for i := 0; i < 15; i++ {
		x := p
		x.Pod = fmt.Sprintf("orders-api-6d4f8b9c7d-p%04d", i)
		many = append(many, x)
	}
	big := strings.Split(strings.TrimSpace(renderResourceSummaryTR(many, time.UTC)), "\n")
	if len(big)-1 > resSummaryMaxRows || !strings.Contains(strings.Join(big, "\n"), "+9 pod daha") || !strings.Contains(strings.Join(big, "\n"), "eşik satırı daha") {
		t.Errorf("tavan: %d satır\n%s", len(big)-1, strings.Join(big, "\n"))
	}
}

func TestResourceHelpers(t *testing.T) {
	if got := resCounterIncrease([][2]float64{{0, 3}, {1, 4}, {2, 0}, {3, 2}}); got != 3 {
		t.Errorf("sayaç artışı (sıfırlanma sayılmaz): %d", got)
	}
	if fmtDurS(0) != "0 dk" || fmtDurS(45) != "45 sn" || fmtDurS(360) != "6 dk" || fmtDurS(4200) != "1 sa 10 dk" || fmtDurS(7200) != "2 sa" {
		t.Error("süre biçimi")
	}
	if fmtCores(0.004) != "0.004 core" || fmtCores(1.234) != "1.23 core" {
		t.Error("core biçimi")
	}
	if fmtSlopeBytes(-3*(1<<20)) != "-3 MiB/sa" {
		t.Errorf("eğim: %s", fmtSlopeBytes(-3*(1<<20)))
	}
	if resSlopePerHour(nil) != 0 || resAboveS(nil, 0, 80, 30) != 0 {
		t.Error("boş girdi")
	}
}

func TestResourceCacheKeyAllInputs(t *testing.T) {
	from := time.Date(2026, 10, 22, 11, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	base := resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re", true, from, to, 30)
	for name, k := range map[string]string{
		"kaynak":    resourceCacheKey("thanos:c-2|kxzone", resQCPU, "q", "re", true, from, to, 30),
		"kimlik":    resourceCacheKey("thanos:c-1|kxzone", resQMem, "q", "re", true, from, to, 30),
		"sorgu":     resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q2", "re", true, from, to, 30),
		"regex":     resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re2", true, from, to, 30),
		"tür":       resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re", false, from, to, 30),
		"başlangıç": resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re", true, from.Add(-time.Hour), to, 30),
		"bitiş":     resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re", true, from, to.Add(time.Minute), 30),
		"adım":      resourceCacheKey("thanos:c-1|kxzone", resQCPU, "q", "re", true, from, to, 60),
	} {
		if k == base {
			t.Errorf("%s anahtara girmiyor", name)
		}
	}
	if !strings.HasPrefix(base, "cosre-res:v1:") {
		t.Error("anahtar öneki")
	}
	l := resourceLookupKey("thanos:c-1|kxzone", "m", to, 9)
	for name, k := range map[string]string{
		"kaynak": resourceLookupKey("thanos:c-2|kxzone", "m", to, 9),
		"seçici": resourceLookupKey("thanos:c-1|kxzone", "m2", to, 9),
		"uç":     resourceLookupKey("thanos:c-1|kxzone", "m", to.Add(time.Minute), 9),
		"tavan":  resourceLookupKey("thanos:c-1|kxzone", "m", to, 8),
	} {
		if k == l {
			t.Errorf("lookup %s anahtara girmiyor", name)
		}
	}
}

func TestDecideResourceTarget(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name, kind, target string
		looks              []resLookup
		want               resTargetDecision
	}{
		{"pod tam", resKindPod, riPod, []resLookup{{Cluster: "kxzone", Pods: []string{riPod}}, {Cluster: "kxzone-b"}},
			resTargetDecision{Kind: resDecideRun, Cluster: "kxzone", Pod: riPod}},
		{"pod tam iki cluster", resKindPod, riPod, []resLookup{{Cluster: "kxzone", Pods: []string{riPod}}, {Cluster: "kxzone-b", Pods: []string{riPod}}},
			resTargetDecision{Kind: resDecideAskCluster, Options: []string{"kxzone", "kxzone-b"}}},
		{"pod tek önek", resKindPod, "orders-api-6d4f8b9c7d-x2", []resLookup{{Cluster: "kxzone", Pods: []string{riPod}}},
			resTargetDecision{Kind: resDecideRun, Cluster: "kxzone", Pod: riPod}},
		{"pod önek çok", resKindPod, "orders-api-6d4f", []resLookup{{Cluster: "kxzone", Pods: []string{riPod, riPodB}}},
			resTargetDecision{Kind: resDecideAskPod, Options: []string{riPodB, riPod}}},
		{"pod önek kesik", resKindPod, "orders-api", []resLookup{{Cluster: "kxzone", Pods: []string{riPod}, Truncated: true}},
			resTargetDecision{Kind: resDecideAskPod, Options: []string{riPod}, More: true}},
		{"pod yok", resKindPod, "orders-api-zzz", []resLookup{{Cluster: "kxzone"}},
			resTargetDecision{Kind: resDecideNotFound}},
		{"servis tek cluster", resKindService, "orders-api", []resLookup{{Cluster: "kxzone", Pods: []string{riPod}}, {Cluster: "kxzone-b", Err: boom}},
			resTargetDecision{Kind: resDecideRun, Cluster: "kxzone", Failed: []string{"kxzone-b"}}},
		{"servis iki cluster", resKindService, "orders-api", []resLookup{{Cluster: "kxzone", Pods: []string{riPod}}, {Cluster: "kxzone-b", Pods: []string{riPodB}}},
			resTargetDecision{Kind: resDecideAskCluster, Options: []string{"kxzone", "kxzone-b"}}},
		{"servis yok", resKindService, "orders-api", []resLookup{{Cluster: "kxzone"}},
			resTargetDecision{Kind: resDecideNotFound}},
		{"okunamadı", resKindService, "orders-api", []resLookup{{Cluster: "kxzone", Err: boom}},
			resTargetDecision{Kind: resDecideUnreadable, Failed: []string{"kxzone"}}},
	}
	for _, c := range cases {
		got := decideResourceTarget(c.kind, c.target, c.looks)
		if got.Kind != c.want.Kind || got.Cluster != c.want.Cluster || got.Pod != c.want.Pod || got.More != c.want.More ||
			strings.Join(got.Options, ",") != strings.Join(c.want.Options, ",") || strings.Join(got.Failed, ",") != strings.Join(c.want.Failed, ",") {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
	// >8 önek: ilk 8 + More.
	var pods []string
	for i := 0; i < 9; i++ {
		pods = append(pods, fmt.Sprintf("orders-api-6d4f8b9c7d-p%04d", i))
	}
	got := decideResourceTarget(resKindPod, "orders-api-6d4f", []resLookup{{Cluster: "kxzone", Pods: pods}})
	if got.Kind != resDecideAskPod || len(got.Options) != resPrefixHitsMax || !got.More {
		t.Errorf(">8 önek: %+v", got)
	}
}

// ─── Sahte Thanos ───────────────────────────────────────────────────────

type fakeResThanos struct {
	mu       sync.Mutex
	pods     map[string][]string // cluster → kube_pod_info pod değerleri
	labelErr error
	queryErr error
	results  map[string][]resSeries
	queries  []string // "<cluster>|range|<expr>"
	lookups  []string
	lims     []thanos.ConsoleLimits
}

var riPodReRe = regexp.MustCompile(`pod=~"([^"]*)"`)

func (f *fakeResThanos) ConsoleLabelValues(_ context.Context, c thanos.ClusterConfig, name string, q thanos.ConsoleMetaQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleLabelsResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookups = append(f.lookups, c.Name+"|"+name+"|"+strings.Join(q.Match, ";")+fmt.Sprintf("|%d|%s", q.Limit, q.End.Sub(q.Start)))
	if f.labelErr != nil {
		return nil, f.labelErr
	}
	m := riPodReRe.FindStringSubmatch(strings.Join(q.Match, ""))
	re := regexp.MustCompile("^(?:" + m[1] + ")$")
	out := &thanos.ConsoleLabelsResult{Values: []string{}}
	for _, p := range f.pods[c.Name] {
		if re.MatchString(p) {
			if len(out.Values) >= q.Limit {
				out.Truncated = true
				break
			}
			out.Values = append(out.Values, p)
		}
	}
	return out, nil
}

func (f *fakeResThanos) answer(c thanos.ClusterConfig, kind, expr string, lim thanos.ConsoleLimits) (*thanos.ConsoleResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, c.Name+"|"+kind+"|"+expr)
	f.lims = append(f.lims, lim)
	if f.queryErr != nil {
		return nil, f.queryErr
	}
	id := ""
	switch {
	case strings.Contains(expr, "cfs_throttled"):
		id = resQThrottle
	case strings.Contains(expr, "container_cpu_usage_seconds_total"):
		id = resQCPU
	case strings.Contains(expr, "working_set"):
		id = resQMem
	case strings.Contains(expr, "restarts_total"):
		id = resQRestarts
	case strings.Contains(expr, "resource_limits"):
		id = resQLimits
	case strings.Contains(expr, "resource_requests"):
		id = resQRequests
	case strings.Contains(expr, "last_terminated"):
		id = resQLastTerm
	case strings.Contains(expr, "waiting_reason"):
		id = resQWaiting
	}
	type row struct {
		Metric map[string]string `json:"metric"`
		Values [][2]any          `json:"values,omitempty"`
		Value  []any             `json:"value,omitempty"`
	}
	var rows []row
	rt := "matrix"
	if kind == "instant" {
		rt = "vector"
	}
	for _, s := range f.results[id] {
		r := row{Metric: s.Labels}
		for _, p := range s.Points {
			pair := [2]any{p[0] / 1000, fmt.Sprint(p[1])}
			if kind == "instant" {
				r.Value = pair[:]
			} else {
				r.Values = append(r.Values, pair)
			}
		}
		rows = append(rows, r)
	}
	b, _ := json.Marshal(rows)
	if rows == nil {
		b = []byte("[]")
	}
	return &thanos.ConsoleResult{ResultType: rt, Result: b, EffectiveQuery: expr, Series: len(rows)}, nil
}

func (f *fakeResThanos) ConsoleQuery(_ context.Context, c thanos.ClusterConfig, q thanos.ConsoleInstantQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleResult, error) {
	return f.answer(c, "instant", q.Query, lim)
}

func (f *fakeResThanos) ConsoleQueryRange(_ context.Context, c thanos.ClusterConfig, q thanos.ConsoleRangeQuery, lim thanos.ConsoleLimits) (*thanos.ConsoleResult, error) {
	if q.Step != 30*time.Second || q.End.Sub(q.Start) != time.Hour {
		return nil, fmt.Errorf("adım/pencere beklenmedik: %v %v", q.Step, q.End.Sub(q.Start))
	}
	return f.answer(c, "range", q.Query, lim)
}

var riClusters = []thanos.ClusterConfig{
	{ID: "c-00000001", Name: "kxzone", Enabled: true, NamespaceFilter: "shop"},
	{ID: "c-00000002", Name: "kxzone-b", Enabled: true},
}

func riRoute(kind, target, svc, fam string) *guidedRoute {
	r := &guidedRoute{Intent: guidedResourceInvestigation, Service: svc, ResTarget: target, ResKind: kind, ResFamily: fam}
	if kind != resKindPod {
		// v0.10.2239 (S4): demetin resolveResourcePods'u doldurur; burada sabit
		// seçici — testler yalnız seçicinin sorgulara aynen geçtiğini denetler.
		r.ResPodRe = `(` + target + `)-.*`
	}
	return r
}

// v0.10.2239 (Entity S4): depoda pod yoksa seçici boş — cluster geneli Thanos
// okuması YOK, dürüst "pod bilinmiyor".
func TestResourceRunPodsUnknown(t *testing.T) {
	th := &fakeResThanos{pods: map[string][]string{"kxzone": {riPod}}, results: riResults()}
	route := riRoute(resKindService, "orders-api", "orders-api", resFamAll)
	route.ResPodRe = ""
	text, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), route, resourceDeps{Thanos: th, Clusters: riClusters}, riTo, 3600, time.UTC)
	if err != nil || len(th.lookups) != 0 || len(th.queries) != 0 || !strings.Contains(text, "pod bilinmiyor") || !route.ResNotFound {
		t.Fatalf("Thanos'a gidilmemeli: lookups=%v queries=%v text=%q", th.lookups, th.queries, text)
	}
}

var riTo = time.Date(2026, 10, 22, 12, 0, 20, 0, time.UTC)

func TestResourceRunService(t *testing.T) {
	th := &fakeResThanos{pods: map[string][]string{"kxzone": {riPod, riPodB}}, results: riResults()}
	vm := &fakeChartReader{labels: map[string][]string{"jvm.memory.used": {"service_name", "k8s_pod_name", "jvm_memory_type"}}}
	s := &Server{cache: newMemCache()}
	ev := &mcEvents{}
	route := riRoute(resKindService, "orders-api", "orders-api", resFamAll)
	d := resourceDeps{Thanos: th, Clusters: riClusters, VM: vm, IsVM: true}
	text, src, err := s.resourceRun(context.Background(), ev.emit(), route, d, riTo, 3600, time.UTC)
	if err != nil || route.DirectAnswer != "" {
		t.Fatalf("err=%v direct=%q", err, route.DirectAnswer)
	}
	if len(th.lookups) != 2 || !strings.Contains(strings.Join(th.lookups, " "), `kxzone|pod|kube_pod_info{pod=~"(orders-api)-.*",namespace=~"shop"}|9|24h0m0s`) {
		t.Errorf("hedef çözümü (iki cluster, 24 sa, tavan 9): %v", th.lookups)
	}
	if len(th.queries) != 7 {
		t.Fatalf("all = 7 sorgu, %d: %v", len(th.queries), th.queries)
	}
	for i, q := range th.queries {
		if !strings.HasPrefix(q, "kxzone|") {
			t.Errorf("sorgu yalnız pod'u olan cluster'a gitmeli: %s", q)
		}
		if l := th.lims[i]; l.Timeout != 8*time.Second || l.MaxSeries != 20 {
			t.Errorf("sınırlar: %+v", l)
		}
	}
	// v0.10.1432 — bellek %88 + restart + OOMKilled → JVM drill koştu (sahte VM
	// seri döndürmüyor): çip yok, dürüst "seri dönmedi".
	if route.ResCluster != "kxzone" || route.ResClusterID != "c-00000001" || !strings.HasPrefix(route.ResHref, "/metrics?") || route.ResJVM {
		t.Errorf("rota çıktıları: %+v", route)
	}
	if !strings.Contains(route.ResHref, "src=thanos") || !strings.Contains(route.ResHref, "cluster=kxzone") {
		t.Errorf("editör linki: %s", route.ResHref)
	}
	if len(ev.blocks) != 4 {
		t.Errorf("4 grafik bloğu (cpu, throttle, bellek, restart) bekleniyordu: %d", len(ev.blocks))
	}
	for _, w := range []string{
		"KAYNAK İNCELEMESİ (orders-api · servis pod'ları · CPU + bellek + restart; son 1sa; kaynak Thanos · kxzone",
		`Pod seçici: pod=~"(orders-api)-.*",namespace=~"shop".`,
		"KAYNAK ÖZETİ (deterministik", "- KRİTİK " + riPod, "OOMKilled",
		"Grafikler cevabın altında kanıt çiplerinde çizildi",
		"JVM drill tetiklendi (bellek limitin %88'i ≥ %80, pencerede 2 restart, son sonlanma OOMKilled).",
		"JVM/GC: VictoriaMetrics'te JVM metriği var ama bu hedef/pencere için seri dönmedi", "JVM/GC yorumu YAPMA",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("kanıt %q taşımalı:\n%s", w, text)
		}
	}
	if !strings.Contains(src, "Thanos · kxzone") {
		t.Errorf("kaynak: %s", src)
	}
	// Önbellek: aynı girdiyle ikinci koşu Thanos'a sorgu atmaz.
	n := len(th.queries)
	route2 := riRoute(resKindService, "orders-api", "orders-api", resFamAll)
	if _, _, err := s.resourceRun(context.Background(), (&mcEvents{}).emit(), route2, d, riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if len(th.queries) != n {
		t.Errorf("önbellek ıskaladı: %d → %d", n, len(th.queries))
	}
	// Farklı aile farklı sorgu kümesi (cpu: 2 yeni = throttle+cpu önbellekte; requests yeni).
	route3 := riRoute(resKindService, "orders-api", "orders-api", resFamCPU)
	if _, _, err := s.resourceRun(context.Background(), (&mcEvents{}).emit(), route3, d, riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if got := len(th.queries) - n; got != 1 {
		t.Errorf("cpu ailesi yalnız requests'i yeni okumalı: %d", got)
	}
}

func TestResourceRunAsksAndNotFound(t *testing.T) {
	d := func(th *fakeResThanos) resourceDeps {
		return resourceDeps{Thanos: th, Clusters: riClusters}
	}
	// Servis iki cluster'da → cluster sor (sorgu yok).
	th := &fakeResThanos{pods: map[string][]string{"kxzone": {riPod}, "kxzone-b": {riPodB}}}
	r := riRoute(resKindService, "orders-api", "orders-api", resFamMemory)
	if _, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if strings.Join(r.ResClusterOptions, ",") != "kxzone,kxzone-b" || !strings.Contains(r.DirectAnswer, "birden çok cluster") || len(th.queries) != 0 {
		t.Errorf("cluster sor: %+v %v", r, th.queries)
	}
	// Sorudaki cluster yalnız onu dener.
	th = &fakeResThanos{pods: map[string][]string{"kxzone": {riPod}, "kxzone-b": {riPodB}}, results: riResults()}
	r = riRoute(resKindService, "orders-api", "orders-api", resFamMemory)
	r.ResCluster = "KXZONE-B"
	if _, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC); err != nil {
		t.Fatal(err)
	}
	if len(th.lookups) != 1 || !strings.HasPrefix(th.lookups[0], "kxzone-b|") || r.ResCluster != "kxzone-b" || r.DirectAnswer != "" {
		t.Errorf("istenen cluster: %v %+v", th.lookups, r)
	}
	// Pod önek → pod sor.
	th = &fakeResThanos{pods: map[string][]string{"kxzone": {riPod, riPodB}}}
	r = riRoute(resKindPod, "orders-api-6d4f", "orders-api", resFamCPU)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC)
	if strings.Join(r.ResPodOptions, ",") != riPodB+","+riPod || !strings.Contains(r.DirectAnswer, "birden çok pod'a uyuyor") {
		t.Errorf("pod sor: %+v", r)
	}
	if !strings.Contains(strings.Join(th.lookups, " "), `kxzone|pod|kube_pod_info{pod=~"orders-api-6d4f.*",namespace=~"shop"}`) {
		t.Errorf("pod önek seçicisi: %v", th.lookups)
	}
	// Tam pod → sorgular tam ada daralır.
	th = &fakeResThanos{pods: map[string][]string{"kxzone": {riPod, riPodB}}, results: riResults()}
	r = riRoute(resKindPod, riPod, "orders-api", resFamRestarts)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC)
	if r.DirectAnswer != "" || len(th.queries) != 3 || !strings.Contains(th.queries[0], `pod=~"`+riPod+`"`) || r.ResNamespace != "shop" {
		t.Errorf("tam pod: %+v %v", r, th.queries)
	}
	// Yok → dürüst BULUNAMADI + denenen seçici.
	th = &fakeResThanos{pods: map[string][]string{}}
	r = riRoute(resKindWorkload, "orders-worker", "", resFamAll)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC)
	if !r.ResNotFound || !strings.Contains(r.DirectAnswer, `BULUNAMADI: "orders-worker"`) || !strings.Contains(r.DirectAnswer, `kube_pod_info{pod=~"(orders-worker)-.*"}`) ||
		!strings.Contains(r.DirectAnswer, "kxzone, kxzone-b") {
		t.Errorf("bulunamadı: %q", r.DirectAnswer)
	}
	// Okunamadı ≠ yok.
	th = &fakeResThanos{labelErr: errors.New("thanos console unavailable: x")}
	r = riRoute(resKindService, "orders-api", "orders-api", resFamAll)
	_, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC)
	if r.ResNotFound || !strings.Contains(r.DirectAnswer, "okunamadı ≠ yok") {
		t.Errorf("okunamadı: %q", r.DirectAnswer)
	}
	// Sorgu hatası: demet düşmez, OKUNAMADI notu.
	th = &fakeResThanos{pods: map[string][]string{"kxzone": {riPod}}, queryErr: errors.New("timeout")}
	r = riRoute(resKindService, "orders-api", "orders-api", resFamRestarts)
	text, _, err := (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, d(th), riTo, 3600, time.UTC)
	if err != nil || !strings.Contains(text, "HİÇBİRİ okunamadı") || strings.Contains(text, "KAYNAK ÖZETİ") {
		t.Errorf("sorgu hatası: %v\n%s", err, text)
	}
}

func TestResourceRunNoThanos(t *testing.T) {
	vm := &fakeChartReader{
		labels: map[string][]string{"process.cpu.utilization": {"service_name", "k8s_pod_name"}, "process.memory.usage": {"service_name", "k8s_pod_name"}},
		series: []chstore.SpanMetricSeries{{GroupKey: []string{`k8s_pod_name="` + riPod + `"`}, Points: []chstore.SpanMetricPoint{{Time: riTo.Add(-time.Minute).UnixNano(), Value: 0.5}}}},
	}
	r := riRoute(resKindService, "orders-api", "orders-api", resFamAll)
	ev := &mcEvents{}
	text, src, err := (&Server{}).resourceRun(context.Background(), ev.emit(), r, resourceDeps{VM: vm, IsVM: true}, riTo, 3600, time.UTC)
	if err != nil || r.DirectAnswer != "" {
		t.Fatal(err, r.DirectAnswer)
	}
	if len(vm.queries) != 2 || !strings.Contains(src, "Thanos yok") {
		t.Errorf("process şablonları (cpu + bellek): %v %s", vm.queries, src)
	}
	for _, w := range []string{"Thanos (cAdvisor + kube-state-metrics) yapılandırılmamış", "process metriği", "GRAFİK ÖZETİ",
		"Restart sayısı/sebebi kube-state-metrics ister", "EŞİK AŞIMI: değerlendirilemedi"} {
		if !strings.Contains(text, w) {
			t.Errorf("Thanos'suz kanıt %q taşımalı:\n%s", w, text)
		}
	}
	// VM de yoksa dürüst "veri yok", sorgu yok.
	r = riRoute(resKindService, "orders-api", "orders-api", resFamCPU)
	text, _, _ = (&Server{}).resourceRun(context.Background(), (&mcEvents{}).emit(), r, resourceDeps{}, riTo, 3600, time.UTC)
	if !strings.Contains(text, "VictoriaMetrics de yapılandırılmamış") {
		t.Errorf("VM'siz: %s", text)
	}
}

// Salt-okur: yazma / denetim / model çağrısı yok; tek range ve tek anlık
// çağrı yeri, sınırlar ve önbellek anahtarı kaynakta.
func TestResourceInvestigationReadOnly(t *testing.T) {
	for _, f := range []string{"copilot_resource_investigation.go", "resource_bundle.go", "resource_jvm.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := stripGoCommentsAPI(string(b))
		if f == "resource_jvm.go" { // v0.10.1432 — tek izinli okuma: operatörün JVM alarm eşikleri
			if strings.Count(src, "s.store.GetRuntimeAlerts(ctx)") != 1 {
				t.Errorf("%s: RuntimeAlertConfig tek yerden okunmalı", f)
			}
			src = strings.Replace(src, "s.store.GetRuntimeAlerts(ctx)", "", 1)
		}
		for _, w := range []string{"Upsert", "Insert", "Save", "audit", "Vote", "vote", "Feedback", "feedback", "Exec(", "ALTER ", "DELETE",
			"s.store.", "copilotStreamSurface", "copilotExplain", "s.copilot", "http.Post", "NewRequest", "os.Getenv", "/api/v1/admin", "delete_series"} {
			if strings.Contains(src, w) {
				t.Errorf("%s %q içermemeli (salt-okur)", f, w)
			}
		}
	}
	b, _ := os.ReadFile("resource_bundle.go")
	src := stripGoCommentsAPI(string(b))
	for w, n := range map[string]int{".ConsoleQueryRange(": 1, ".ConsoleQuery(": 1, ".ConsoleLabelValues(": 1} {
		if got := strings.Count(src, w); got != n {
			t.Errorf("%s çağrı yeri %d, want %d", w, got, n)
		}
	}
	for _, w := range []string{"thanos.ConsoleLimits{Timeout: resQueryTimeout, MaxSeries: resMaxSeries}", "context.WithTimeout(ctx, resQueryTimeout)",
		"resourceCacheKey(src, q.ID, q.Expr, podRe, q.Range, from, to, step)", "make(chan struct{}, resParallel)"} {
		if !strings.Contains(src, w) {
			t.Errorf("demet %q taşımalı", w)
		}
	}
}
