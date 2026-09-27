package thanos

// trace_pods_test.go — v0.10.968 — Trace › Metrics toplu ucunun (GET
// /api/trace-pods/metrics) Thanos yarısının sözleşmesi.
//
// Pinlenen vaatler (spec §2 "Thanos query builder"):
//   - sekiz PromQL ifadesinin BİREBİR metni (R1/R2 range, I1–I6 instant);
//     namespace kümesi yalnız HER ref namespace taşıyorsa, NamespaceFilter
//     kalkanı her zaman, pod adındaki regex meta'sı kaçışlı;
//   - kova hizası: start adıma AŞAĞI hizalı, points=(end-start)/step+1,
//     boşluk nil, örnek doğru indekste;
//   - ref eşleme: tam (ns,pod); namespace'siz ref tek aday → doldurulur,
//     çok aday → ambiguous; seri yok → no_samples; istek sırası korunur;
//   - maliyet pod sayısından BAĞIMSIZ: 1 ref de 64 ref de tam 2 query_range
//     + 6 query; paylaşımlı querier'da her istek cluster matcher'ı taşır;
//   - instant zinciri best-effort: hatası çağrıyı düşürmez, Instant
//     partial/failed olur; R1/R2 hatası çağrıyı düşürür (kısmi veri YOK).
//
// Yalnız httptest sahteleri; canlı Thanos'a asla bağlanılmaz.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── sabitler ─────────────────────────────────────────────────────────

func TestTracePodRegexMaxMatchesPodNamesRegex(t *testing.T) {
	if TracePodRegexMax != podNamesRegexMaxLen {
		t.Fatalf("TracePodRegexMax (%d) podNamesRegexMaxLen (%d) ile aynı olmalı — FE parçalayıcısı bu tavana göre böler",
			TracePodRegexMax, podNamesRegexMaxLen)
	}
	if TracePodChunkMax != 64 {
		t.Fatalf("TracePodChunkMax = %d, sözleşme 64", TracePodChunkMax)
	}
}

// ── 1. PromQL metinleri ──────────────────────────────────────────────

func TestTracePodQueriesExactStrings(t *testing.T) {
	refs := []TracePodRef{
		{Namespace: "payments", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"},
		{Namespace: "audit", Pod: "audit-log-prod-5f9c7d6b8-q2w4e"},
	}
	qs, truncated := tracePodQueries(refs, "")
	if truncated {
		t.Fatal("iki kısa ad tavanı aşmamalı")
	}
	sel := `pod=~"^(fraud-score-prod-6b7d9f8c5-m3t9w|audit-log-prod-5f9c7d6b8-q2w4e)$",namespace=~"payments|audit"`
	want := map[string]string{
		"cpu":        `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",` + sel + `}[5m]))`,
		"mem":        `sum by (namespace, pod) (container_memory_working_set_bytes{container!="",` + sel + `})`,
		"limits":     `sum by (namespace, pod, resource) (kube_pod_container_resource_limits{resource=~"cpu|memory",` + sel + `})`,
		"requests":   `sum by (namespace, pod, resource) (kube_pod_container_resource_requests{resource=~"cpu|memory",` + sel + `})`,
		"restarts":   `sum by (namespace, pod) (kube_pod_container_status_restarts_total{` + sel + `})`,
		"termReason": `max by (namespace, pod, reason) (kube_pod_container_status_last_terminated_reason{` + sel + `} == 1)`,
		"phase":      `max by (namespace, pod, phase) (kube_pod_status_phase{` + sel + `} == 1)`,
		"termAt":     `max by (namespace, pod) (kube_pod_container_status_last_terminated_timestamp{` + sel + `})`,
	}
	got := map[string]string{
		"cpu": qs.CPU, "mem": qs.Mem, "limits": qs.Limits, "requests": qs.Requests,
		"restarts": qs.Restarts, "termReason": qs.TermReason, "phase": qs.Phase, "termAt": qs.TermAt,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s:\n got  %s\n want %s", k, got[k], w)
		}
	}
}

func TestTracePodSelectorVariants(t *testing.T) {
	cases := []struct {
		name     string
		refs     []TracePodRef
		nsFilter string
		wantCPU  string
	}{
		{
			name: "bir ref namespace'siz → namespace kümesi YOK",
			refs: []TracePodRef{
				{Namespace: "payments", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"},
				{Namespace: "", Pod: "checkout-api-7c8d9e0f1-z9x8c"},
			},
			wantCPU: `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",pod=~"^(fraud-score-prod-6b7d9f8c5-m3t9w|checkout-api-7c8d9e0f1-z9x8c)$"}[5m]))`,
		},
		{
			name: "NamespaceFilter kalkanı her zaman eklenir",
			refs: []TracePodRef{
				{Namespace: "payments", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"},
			},
			nsFilter: "pay.*|audit",
			wantCPU:  `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",pod=~"^(fraud-score-prod-6b7d9f8c5-m3t9w)$",namespace=~"payments",namespace=~"pay.*|audit"}[5m]))`,
		},
		{
			name: "namespace'siz ref + filtre → yalnız filtre",
			refs: []TracePodRef{
				{Namespace: "", Pod: "audit-log-prod-5f9c7d6b8-q2w4e"},
			},
			nsFilter: "audit",
			wantCPU:  `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",pod=~"^(audit-log-prod-5f9c7d6b8-q2w4e)$",namespace=~"audit"}[5m]))`,
		},
		{
			name: "pod adındaki nokta regex'te kaçışlı, PromQL dizesinde çift ters bölü",
			refs: []TracePodRef{
				{Namespace: "shop", Pod: "checkout-api-v1.2-7d9f8c6b5-x2x9k"},
			},
			wantCPU: `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",pod=~"^(checkout-api-v1\\.2-7d9f8c6b5-x2x9k)$",namespace=~"shop"}[5m]))`,
		},
		{
			name: "aynı pod adı iki namespace'te → regex'te bir kez, namespace kümesi tekil",
			refs: []TracePodRef{
				{Namespace: "payments", Pod: "fraud-score-prod-0"},
				{Namespace: "audit", Pod: "fraud-score-prod-0"},
				{Namespace: "payments", Pod: "fraud-score-prod-1"},
			},
			wantCPU: `sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",pod=~"^(fraud-score-prod-0|fraud-score-prod-1)$",namespace=~"payments|audit"}[5m]))`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			qs, truncated := tracePodQueries(tc.refs, tc.nsFilter)
			if truncated {
				t.Fatal("kısa liste tavanı aşmamalı")
			}
			if qs.CPU != tc.wantCPU {
				t.Fatalf("\n got  %s\n want %s", qs.CPU, tc.wantCPU)
			}
			// Sekiz ifadenin hepsi AYNI seçiciyi taşır.
			sel := strings.TrimSuffix(strings.TrimPrefix(tc.wantCPU,
				`sum by (namespace, pod) (rate(container_cpu_usage_seconds_total{container!="",`), `}[5m]))`)
			for i, q := range []string{qs.Mem, qs.Limits, qs.Requests, qs.Restarts, qs.TermReason, qs.Phase, qs.TermAt} {
				if !strings.Contains(q, sel+"}") {
					t.Errorf("sorgu %d aynı seçiciyi taşımıyor: %s", i, q)
				}
			}
		})
	}
}

func TestTracePodQueriesTruncatedPastRegexCap(t *testing.T) {
	refs := make([]TracePodRef, 0, 64)
	for i := 0; i < 64; i++ {
		// 64 × (63+1) = 4096 > 4000
		refs = append(refs, TracePodRef{Namespace: "payments",
			Pod: fmt.Sprintf("fraud-score-prod-%046d", i)})
	}
	if len(refs[0].Pod) != 63 {
		t.Fatalf("fixture: ad 63 karakter olmalı, %d", len(refs[0].Pod))
	}
	if _, truncated := tracePodQueries(refs, ""); !truncated {
		t.Fatal("4000 karakteri aşan seçici truncated=true dönmeli")
	}
	if _, err := New().TracePodMetrics(context.Background(), ClusterConfig{Name: "dc-east-1", URL: "http://127.0.0.1:0"},
		refs, time.Unix(1790496000, 0), time.Unix(1790496900, 0), 120); err == nil {
		t.Fatal("tavanı aşan liste Thanos'a gitmeden hata dönmeli")
	}
}

// ── 2. Kova hizası ───────────────────────────────────────────────────

func TestTracePodWindowAlignment(t *testing.T) {
	cases := []struct {
		name       string
		from, to   int64
		mdp        int
		wantStart  int64
		wantStep   int
		wantPoints int
	}{
		// ±5 dk: 15 s; start aşağı hizalı (…007 → …000), end …907 → …900.
		{"15 dk pencere, hizasız uçlar", 1790496007, 1790496907, 120, 1790496000, 15, 61},
		// ±15 dk: 30 dk pencere, 1800/15 = 120 ≤ mdp → 15 s.
		{"30 dk pencere, 15 s", 1790496000, 1790497800, 120, 1790496000, 15, 121},
		// ±60 dk: 2 sa pencere; merdiven 30 s ama 7200/30 = 240 > 120 → 60 s.
		{"2 sa pencere, mdp 120 → 60 s", 1790496050, 1790503250, 120, 1790496000, 60, 121},
		// Aynı pencere mdp 240 → 30 s.
		{"2 sa pencere, mdp 240 → 30 s", 1790496000, 1790503200, 240, 1790496000, 30, 241},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, step, points := tracePodWindow(time.Unix(tc.from, 0), time.Unix(tc.to, 0), tc.mdp)
			if start != tc.wantStart || step != tc.wantStep || points != tc.wantPoints {
				t.Fatalf("start/step/points = %d/%d/%d, want %d/%d/%d",
					start, step, points, tc.wantStart, tc.wantStep, tc.wantPoints)
			}
			if start%int64(step) != 0 {
				t.Fatalf("start adıma hizalı değil: %d %% %d", start, step)
			}
		})
	}
}

func tpRawPair(ts int64, v string) []json.RawMessage {
	return []json.RawMessage{json.RawMessage(fmt.Sprint(ts)), json.RawMessage(`"` + v + `"`)}
}

func TestTracePodAlignSamples(t *testing.T) {
	const start, step, points = int64(1790496000), 15, 5
	vals := [][]json.RawMessage{
		tpRawPair(1790496000, "1"),   // idx 0
		tpRawPair(1790496030, "3"),   // idx 2 (idx 1 boşluk)
		tpRawPair(1790496060, "NaN"), // idx 4 — sayı değil → boşluk kalır
		tpRawPair(1790495985, "9"),   // start öncesi → atılır
		tpRawPair(1790496075, "9"),   // son kova sonrası → atılır
	}
	got := tracePodAlign(vals, start, step, points, func(v float64) float64 { return v })
	if len(got) != points {
		t.Fatalf("uzunluk %d, want %d", len(got), points)
	}
	want := []string{"1", "nil", "3", "nil", "nil"}
	for i, w := range want {
		g := "nil"
		if got[i] != nil {
			g = fmt.Sprint(*got[i])
		}
		if g != w {
			t.Errorf("idx %d = %s, want %s", i, g, w)
		}
	}
	if tracePodAlign([][]json.RawMessage{tpRawPair(1790495985, "9")}, start, step, points, nil) != nil {
		t.Fatal("hiç örnek yerleşmezse seri nil dönmeli (no_samples ayrımı)")
	}
}

func TestTracePodRounding(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{0.4123456, 0.4123},
		{0.97, 0.97},
		{1234.5678, 1235},
		{0.000123456, 0.0001235},
		{0, 0},
	} {
		if got := round4Sig(tc.in); got != tc.want {
			t.Errorf("round4Sig(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ── 3. Ref eşleme ───────────────────────────────────────────────────

func tpFP(v float64) *float64 { return &v }

func TestBuildTracePodRowsMatching(t *testing.T) {
	const points = 3
	series := func(v float64) []*float64 { return []*float64{tpFP(v), nil, tpFP(v)} }
	rng := map[string]*tracePodRange{
		"payments\x00fraud-score-prod-6b7d9f8c5-m3t9w": {cpu: series(0.4), mem: series(1e9)},
		"audit\x00audit-log-prod-5f9c7d6b8-q2w4e":      {cpu: series(0.1)}, // mem yok → nil dizisi
		// aynı ad iki namespace'te → namespace'siz ref belirsiz
		"payments\x00checkout-api-0": {cpu: series(0.2), mem: series(2e8)},
		"shop\x00checkout-api-0":     {cpu: series(0.3), mem: series(3e8)},
	}
	inst := &tracePodInstant{
		phaseOK: true,
		phase: map[string]string{
			"payments\x00fraud-score-prod-6b7d9f8c5-m3t9w": "Running",
			"audit\x00audit-log-prod-5f9c7d6b8-q2w4e":      "Running",
			"payments\x00feature-flags-prod-0":             "Pending", // yalnız instant'ta → yedek aday
		},
		cpuLimit: map[string]float64{"payments\x00fraud-score-prod-6b7d9f8c5-m3t9w": 1},
		restarts: map[string]int{"payments\x00fraud-score-prod-6b7d9f8c5-m3t9w": 3},
	}
	refs := []TracePodRef{
		{Namespace: "", Pod: "audit-log-prod-5f9c7d6b8-q2w4e"},           // tek aday → doldurulur
		{Namespace: "payments", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"}, // tam
		{Namespace: "", Pod: "checkout-api-0"},                           // iki aday → ambiguous
		{Namespace: "payments", Pod: "checkout-api-9"},                   // seri yok → no_samples
		{Namespace: "", Pod: "feature-flags-prod-0"},                     // range yok, instant tek aday
		{Namespace: "audit", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"},    // ad var, (ns,pod) yok → no_samples
	}
	rows := buildTracePodRows(refs, rng, inst, points)
	if len(rows) != len(refs) {
		t.Fatalf("ref başına bir satır: %d/%d", len(rows), len(refs))
	}
	for i, r := range rows {
		if r.Pod != refs[i].Pod {
			t.Fatalf("istek sırası bozuldu: %d → %s", i, r.Pod)
		}
	}
	// 0: doldurulmuş namespace
	if r := rows[0]; r.State != "ok" || r.Namespace != "audit" || !r.NsFilled ||
		len(r.CPU) != points || len(r.Mem) != points || r.Mem[0] != nil || r.Inventory != "present" {
		t.Errorf("namespace'siz tek aday: %+v", r)
	}
	// 1: tam eşleşme + instant zenginleştirme
	if r := rows[1]; r.State != "ok" || r.NsFilled || r.Namespace != "payments" ||
		r.CPULimit == nil || *r.CPULimit != 1 || r.MemLimit != nil ||
		r.Restarts == nil || *r.Restarts != 3 || r.Phase != "Running" || r.Inventory != "present" {
		t.Errorf("tam eşleşme: %+v", r)
	}
	// 2: belirsiz — seri yok, namespace boş, envanter bilinmiyor
	if r := rows[2]; r.State != "ambiguous" || r.Namespace != "" || r.CPU != nil || r.Mem != nil || r.Inventory != "unknown" {
		t.Errorf("belirsiz: %+v", r)
	}
	// 3: seri yok
	if r := rows[3]; r.State != "no_samples" || r.CPU != nil || r.Inventory != "absent" {
		t.Errorf("no_samples: %+v", r)
	}
	// 4: instant yedek adayı → ns doldurulur, seri yok, envanter present
	if r := rows[4]; r.State != "no_samples" || r.Namespace != "payments" || !r.NsFilled || r.Phase != "Pending" || r.Inventory != "present" {
		t.Errorf("instant yedek aday: %+v", r)
	}
	// 5: namespace verilmiş ref BAŞKA namespace'e kaymaz
	if r := rows[5]; r.State != "no_samples" || r.Namespace != "audit" || r.NsFilled {
		t.Errorf("tam eşleşme kayması: %+v", r)
	}
}

func TestBuildTracePodRowsInventoryUnknownWhenPhaseFailed(t *testing.T) {
	rows := buildTracePodRows([]TracePodRef{{Namespace: "payments", Pod: "fraud-score-prod-0"}},
		map[string]*tracePodRange{}, &tracePodInstant{}, 3)
	if rows[0].Inventory != "unknown" || rows[0].State != "no_samples" {
		t.Fatalf("I5 başarısızken envanter unknown: %+v", rows[0])
	}
}

// ── 4–7. Sahte Thanos ───────────────────────────────────────────────

type tpFake struct {
	*httptest.Server
	mu      sync.Mutex
	ranges  int
	instant int
	queries []string
	params  []string
}

// newTPFake — respond(path, query) → (durum, gövde). Çağrılar sayılır.
func newTPFake(t *testing.T, respond func(path, q string) (int, string)) *tpFake {
	t.Helper()
	f := &tpFake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		f.mu.Lock()
		switch r.URL.Path {
		case "/api/v1/query_range":
			f.ranges++
		case "/api/v1/query":
			f.instant++
		}
		f.queries = append(f.queries, q)
		f.params = append(f.params, r.URL.RawQuery)
		f.mu.Unlock()
		code, body := respond(r.URL.Path, q)
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

func tpMatrix(series ...string) string {
	return `{"status":"success","data":{"resultType":"matrix","result":[` + strings.Join(series, ",") + `]}}`
}

func tpMSeries(ns, pod string, pts ...string) string {
	return fmt.Sprintf(`{"metric":{"namespace":"%s","pod":"%s"},"values":[%s]}`, ns, pod, strings.Join(pts, ","))
}

func tpPt(ts int64, v string) string { return fmt.Sprintf(`[%d,"%s"]`, ts, v) }

func tpVector(samples ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(samples, ",") + `]}}`
}

func tpSample(labels map[string]string, v string) string {
	b, _ := json.Marshal(labels)
	return fmt.Sprintf(`{"metric":%s,"value":[1790497000,"%s"]}`, b, v)
}

const tpErr = `{"status":"error","errorType":"internal","error":"boom"}`

// Pencere: 15 dk, 15 s adım, start 1790496000, 61 nokta.
var (
	tpFrom = time.Unix(1790496007, 0)
	tpTo   = time.Unix(1790496907, 0)
)

// tpHappy — iki pod'un tam cevabı; override(path,q) doluysa önce o sorulur.
func tpHappy(override func(path, q string) (int, string, bool)) func(path, q string) (int, string) {
	return func(path, q string) (int, string) {
		if override != nil {
			if code, body, ok := override(path, q); ok {
				return code, body
			}
		}
		fs := map[string]string{"namespace": "payments", "pod": "fraud-score-prod-6b7d9f8c5-m3t9w"}
		au := map[string]string{"namespace": "audit", "pod": "audit-log-prod-5f9c7d6b8-q2w4e"}
		with := func(m map[string]string, k, v string) map[string]string {
			out := map[string]string{k: v}
			for a, b := range m {
				out[a] = b
			}
			return out
		}
		switch {
		case strings.Contains(q, "container_cpu_usage_seconds_total"):
			return 200, tpMatrix(
				tpMSeries("payments", "fraud-score-prod-6b7d9f8c5-m3t9w", tpPt(1790496000, "0.41234567"), tpPt(1790496030, "0.97")),
				tpMSeries("audit", "audit-log-prod-5f9c7d6b8-q2w4e", tpPt(1790496900, "0.1")))
		case strings.Contains(q, "container_memory_working_set_bytes"):
			return 200, tpMatrix(
				tpMSeries("payments", "fraud-score-prod-6b7d9f8c5-m3t9w", tpPt(1790496000, "1524713472.6")))
		case strings.Contains(q, "kube_pod_container_resource_limits"):
			return 200, tpVector(tpSample(with(fs, "resource", "cpu"), "1"), tpSample(with(fs, "resource", "memory"), "2147483648"))
		case strings.Contains(q, "kube_pod_container_resource_requests"):
			return 200, tpVector(tpSample(with(fs, "resource", "cpu"), "0.5"))
		case strings.Contains(q, "kube_pod_container_status_restarts_total"):
			return 200, tpVector(tpSample(fs, "3"), tpSample(au, "0"))
		case strings.Contains(q, "kube_pod_container_status_last_terminated_reason"):
			return 200, tpVector(
				tpSample(with(fs, "reason", "Completed"), "1"),
				tpSample(with(fs, "reason", "OOMKilled"), "1"),
				tpSample(with(fs, "reason", "Error"), "1"))
		case strings.Contains(q, "kube_pod_status_phase"):
			return 200, tpVector(tpSample(with(fs, "phase", "Running"), "1"))
		case strings.Contains(q, "kube_pod_container_status_last_terminated_timestamp"):
			return 200, tpVector(tpSample(fs, "1790497008"))
		}
		return 200, tpVector()
	}
}

var tpRefs = []TracePodRef{
	{Namespace: "payments", Pod: "fraud-score-prod-6b7d9f8c5-m3t9w"},
	{Namespace: "audit", Pod: "audit-log-prod-5f9c7d6b8-q2w4e"},
}

func TestTracePodMetricsHappyPath(t *testing.T) {
	f := newTPFake(t, tpHappy(nil))
	res, err := New().TracePodMetrics(context.Background(),
		ClusterConfig{Name: "dc-east-1", URL: f.URL, Enabled: true}, tpRefs, tpFrom, tpTo, 120)
	if err != nil {
		t.Fatalf("TracePodMetrics: %v", err)
	}
	if res.Start != 1790496000 || res.Step != 15 || res.Points != 61 || res.Instant != "ok" {
		t.Fatalf("başlık: start=%d step=%d points=%d instant=%s", res.Start, res.Step, res.Points, res.Instant)
	}
	if len(res.Pods) != 2 {
		t.Fatalf("iki ref → iki satır: %+v", res.Pods)
	}
	fs := res.Pods[0]
	if fs.State != "ok" || len(fs.CPU) != 61 || len(fs.Mem) != 61 {
		t.Fatalf("fraud-score satırı: %+v", fs)
	}
	// 7. CPU 4 anlamlı hane, bellek tam bayt.
	if fs.CPU[0] == nil || *fs.CPU[0] != 0.4123 || fs.CPU[2] == nil || *fs.CPU[2] != 0.97 || fs.CPU[1] != nil {
		t.Fatalf("CPU hizası/yuvarlama: %v %v %v", fs.CPU[0], fs.CPU[1], fs.CPU[2])
	}
	if fs.Mem[0] == nil || *fs.Mem[0] != 1524713473 {
		t.Fatalf("bellek tam bayta yuvarlanmalı: %v", fs.Mem[0])
	}
	if fs.CPULimit == nil || *fs.CPULimit != 1 || fs.MemLimit == nil || *fs.MemLimit != 2147483648 ||
		fs.CPURequest == nil || *fs.CPURequest != 0.5 || fs.MemRequest != nil {
		t.Fatalf("limit/istek: %+v", fs)
	}
	if fs.Restarts == nil || *fs.Restarts != 3 || fs.Phase != "Running" || fs.Inventory != "present" ||
		fs.LastTermReason != "OOMKilled" || fs.LastTermAt != 1790497008 {
		t.Fatalf("şu an alanları (en kötü sebep OOMKilled): %+v", fs)
	}
	au := res.Pods[1]
	if au.State != "ok" || au.CPU[60] == nil || *au.CPU[60] != 0.1 || au.Mem == nil || au.Mem[0] != nil {
		t.Fatalf("audit satırı (mem serisi yok → nil dizisi): %+v", au)
	}
	if au.Restarts == nil || *au.Restarts != 0 || au.Inventory != "absent" {
		t.Fatalf("gerçek 0 restart görünür, faz serisi yok → absent: %+v", au)
	}
	// Range parametreleri: start hizalı, end = to, step 15.
	for _, p := range f.params {
		if strings.Contains(p, "start=") && (!strings.Contains(p, "start=1790496000") ||
			!strings.Contains(p, "end=1790496907") || !strings.Contains(p, "step=15")) {
			t.Errorf("range parametreleri: %s", p)
		}
	}
}

func TestTracePodMetricsCostIndependentOfPodCount(t *testing.T) {
	for _, n := range []int{1, 64} {
		t.Run(fmt.Sprintf("%d ref", n), func(t *testing.T) {
			f := newTPFake(t, tpHappy(nil))
			refs := make([]TracePodRef, 0, n)
			for i := 0; i < n; i++ {
				refs = append(refs, TracePodRef{Namespace: "payments", Pod: fmt.Sprintf("fraud-score-prod-6b7d9f8c5-%05d", i)})
			}
			c := ClusterConfig{Name: "dc-east-1", URL: f.URL, ThanosLabelName: "cluster", Enabled: true}
			res, err := New().TracePodMetrics(context.Background(), c, refs, tpFrom, tpTo, 120)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Pods) != n {
				t.Fatalf("satır sayısı %d, want %d", len(res.Pods), n)
			}
			if f.ranges != 2 || f.instant != 6 {
				t.Fatalf("tam 2 query_range + 6 query beklenir, %d + %d", f.ranges, f.instant)
			}
			for _, q := range f.queries {
				if !strings.Contains(q, `cluster="dc-east-1"`) {
					t.Errorf("cluster matcher enjekte edilmemiş: %s", q)
				}
			}
		})
	}
}

func TestTracePodMetricsInstantBestEffort(t *testing.T) {
	c := func(url string) ClusterConfig { return ClusterConfig{Name: "dc-east-1", URL: url, Enabled: true} }

	t.Run("limitler 500 → partial, limitler nil", func(t *testing.T) {
		f := newTPFake(t, tpHappy(func(path, q string) (int, string, bool) {
			if strings.Contains(q, "kube_pod_container_resource_limits") {
				return 500, tpErr, true
			}
			return 0, "", false
		}))
		res, err := New().TracePodMetrics(context.Background(), c(f.URL), tpRefs, tpFrom, tpTo, 120)
		if err != nil {
			t.Fatalf("instant hatası çağrıyı düşürmemeli: %v", err)
		}
		if res.Instant != "partial" {
			t.Fatalf("Instant = %q, want partial", res.Instant)
		}
		if p := res.Pods[0]; p.CPULimit != nil || p.MemLimit != nil || p.CPURequest == nil || p.Inventory != "present" {
			t.Fatalf("limitler nil, istek dolu kalmalı: %+v", p)
		}
	})

	t.Run("tüm instant 500 → failed, envanter unknown", func(t *testing.T) {
		f := newTPFake(t, tpHappy(func(path, q string) (int, string, bool) {
			if path == "/api/v1/query" {
				return 500, tpErr, true
			}
			return 0, "", false
		}))
		res, err := New().TracePodMetrics(context.Background(), c(f.URL), tpRefs, tpFrom, tpTo, 120)
		if err != nil {
			t.Fatal(err)
		}
		if res.Instant != "failed" {
			t.Fatalf("Instant = %q, want failed", res.Instant)
		}
		for _, p := range res.Pods {
			if p.Inventory != "unknown" || p.Restarts != nil || p.Phase != "" || p.State != "ok" {
				t.Fatalf("instant yokken seri var, şu an alanları yok: %+v", p)
			}
		}
	})

	t.Run("I5 başarılı ama seri yok → absent", func(t *testing.T) {
		f := newTPFake(t, tpHappy(func(path, q string) (int, string, bool) {
			if strings.Contains(q, "kube_pod_status_phase") {
				return 200, tpVector(), true
			}
			return 0, "", false
		}))
		res, err := New().TracePodMetrics(context.Background(), c(f.URL), tpRefs, tpFrom, tpTo, 120)
		if err != nil {
			t.Fatal(err)
		}
		if res.Instant != "ok" || res.Pods[0].Inventory != "absent" {
			t.Fatalf("I5 boş → absent: instant=%s inv=%s", res.Instant, res.Pods[0].Inventory)
		}
	})

	t.Run("instant alt-bütçesi dolunca range verisi yine döner", func(t *testing.T) {
		prev := tracePodInstantBudget
		tracePodInstantBudget = 50 * time.Millisecond
		t.Cleanup(func() { tracePodInstantBudget = prev })
		release := make(chan struct{})
		f := newTPFake(t, tpHappy(func(path, q string) (int, string, bool) {
			if path == "/api/v1/query" {
				select {
				case <-release:
				case <-time.After(3 * time.Second):
				}
				return 200, tpVector(), true
			}
			return 0, "", false
		}))
		t.Cleanup(func() { close(release) }) // LIFO: f.Close'tan ÖNCE koşar
		t0 := time.Now()
		res, err := New().TracePodMetrics(context.Background(), c(f.URL), tpRefs, tpFrom, tpTo, 120)
		if err != nil {
			t.Fatal(err)
		}
		if el := time.Since(t0); el > 2*time.Second {
			t.Fatalf("alt-bütçe uygulanmadı: %v", el)
		}
		if res.Instant != "failed" || res.Pods[0].State != "ok" || res.Pods[0].Inventory != "unknown" {
			t.Fatalf("bütçe dolunca: instant=%s state=%s inv=%s", res.Instant, res.Pods[0].State, res.Pods[0].Inventory)
		}
	})
}

func TestTracePodMetricsRangeFailureFailsWholeCall(t *testing.T) {
	for _, metric := range []string{"container_memory_working_set_bytes", "container_cpu_usage_seconds_total"} {
		t.Run(metric, func(t *testing.T) {
			f := newTPFake(t, tpHappy(func(path, q string) (int, string, bool) {
				if strings.Contains(q, metric) {
					return 500, tpErr, true
				}
				return 0, "", false
			}))
			res, err := New().TracePodMetrics(context.Background(),
				ClusterConfig{Name: "dc-east-1", URL: f.URL, Enabled: true}, tpRefs, tpFrom, tpTo, 120)
			if err == nil {
				t.Fatal("range hatası çağrıyı düşürmeli")
			}
			if len(res.Pods) != 0 || res.Points != 0 {
				t.Fatalf("kısmi veri dönmemeli: %+v", res)
			}
			if !strings.Contains(err.Error(), "HTTP 500") {
				t.Fatalf("Thanos metni korunmalı: %v", err)
			}
		})
	}
}

func TestTracePodMetricsDeadlineIsVisible(t *testing.T) {
	release := make(chan struct{})
	f := newTPFake(t, func(path, q string) (int, string) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		return 200, tpMatrix()
	})
	t.Cleanup(func() { close(release) }) // LIFO: f.Close'tan ÖNCE koşar
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New().TracePodMetrics(ctx, ClusterConfig{Name: "dc-east-1", URL: f.URL, Enabled: true}, tpRefs, tpFrom, tpTo, 120)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline hatası errors.Is ile görünmeli (handler \"zaman aşımı\" diyebilsin): %v", err)
	}
}

// ── v0.10.968 — SIZINTI KURALI: istemciye giden range hatası metni ─────

// TestTracePodErrReason — v0.10.968 — ham doQuery hatası → istemciye
// gidebilen kısa sebep. Taşıma hatası kaba sebebe iner (URL + PromQL
// taşımaz); "thanos <ad>:" katmanı tekrarlanmaz; JSON gövdesi errorType +
// error olarak geçer, kesik JSON'da da okunur; JSON olmayan gövde
// yankılanmaz; yapılandırılmış host maskelenir.
func TestTracePodErrReason(t *testing.T) {
	c := ClusterConfig{Name: "dc-east-1", URL: "http://thanos-a.example.invalid:9090"}
	dial := &url.Error{Op: "Get", URL: "http://thanos-a.example.invalid:9090/api/v1/query_range?query=sum",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}}
	long := strings.Repeat("m", 300)
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"taşıma (dial)", fmt.Errorf("thanos dc-east-1: %w", dial), "connection failed"},
		{"HTTP + JSON", errors.New(`thanos dc-east-1: HTTP 500: ` + tpErr), "HTTP 500 internal: boom"},
		{"HTTP 422 execution", errors.New(`thanos dc-east-1: HTTP 422: {"status":"error","errorType":"execution","error":"many-to-many matching not allowed"}`),
			"HTTP 422 execution: many-to-many matching not allowed"},
		{"kesik JSON", errors.New(`thanos dc-east-1: HTTP 422: {"status":"error","errorType":"execution","error":"` + long[:120]),
			"HTTP 422 execution: " + long[:120] + "…"},
		{"JSON olmayan gövde yankılanmaz", errors.New(`thanos dc-east-1: HTTP 502: <html>thanos-a.example.invalid bad gateway</html>`), "HTTP 502"},
		{"200 + status error", errors.New(`thanos dc-east-1: execution: query timed out in expression evaluation`),
			"execution: query timed out in expression evaluation"},
		{"çözme hatası", errors.New(`thanos dc-east-1 decode: invalid character '<' looking for beginning of value`), "yanıt çözülemedi"},
		{"host maskelenir", errors.New(`thanos dc-east-1: HTTP 400: {"errorType":"bad_data","error":"upstream thanos-a.example.invalid:9090 refused"}`),
			"HTTP 400 bad_data: upstream <thanos> refused"},
	} {
		if got := tracePodErrReason(c, tc.err); got != tc.want {
			t.Errorf("%s:\n got  %q\n want %q", tc.name, got, tc.want)
		}
	}
}

// TestTracePodMetricsTransportErrorScrubbed — v0.10.968 — kapalı sahte:
// hata metni uç noktayı / PromQL'i taşımaz, ama errors.Is nöbetçileri için
// ham hata Unwrap zincirinde kalır.
func TestTracePodMetricsTransportErrorScrubbed(t *testing.T) {
	f := newTPFake(t, tpHappy(nil))
	u := f.URL
	f.Close()
	_, err := New().TracePodMetrics(context.Background(), ClusterConfig{Name: "dc-east-1", URL: u, Enabled: true}, tpRefs, tpFrom, tpTo, 120)
	if err == nil {
		t.Fatal("kapalı sunucu hata vermeli")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sorgusu: connection failed") {
		t.Fatalf("kaba sebep beklenir: %s", msg)
	}
	for _, leak := range []string{"127.0.0.1", strings.TrimPrefix(u, "http://"), "query_range", "dial tcp"} {
		if strings.Contains(msg, leak) {
			t.Errorf("metin %q sızdırmamalı: %s", leak, msg)
		}
	}
	var uerr *url.Error
	if !errors.As(err, &uerr) {
		t.Fatalf("ham *url.Error Unwrap zincirinde kalmalı: %#v", err)
	}
}
