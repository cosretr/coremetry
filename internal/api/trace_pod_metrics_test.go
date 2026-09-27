package api

// trace_pod_metrics_test.go — v0.10.968 — GET /api/trace-pods/metrics
// sözleşmesi (spec §2, apiContract A).
//
// Pinlenen vaatler:
//   - her kötü girdi 400 (errBadRequest) ve Thanos'a SIFIR çağrı;
//   - eşlenmemiş durumlar (Thanos kapalı, cv boş, cv bilinmiyor / cluster
//     kapalı) 200 + dürüst neden, önbelleksiz, SIFIR Thanos çağrısı;
//   - önbellek anahtarı pod KÜMESİNİ sırasız özetler ve cluster kimliği,
//     cfg özeti, pencere kovası, mdp basamağıyla ayrışır (v0.5.187);
//   - TTL: açık pencere 60 s, kapanmış (to < now-10m) 10 dk;
//   - zaman aşımı 502 + "zaman aşımı", hata ASLA önbelleğe girmez;
//   - yanıt ref başına bir satır, istek sırasıyla; mdp basamağa oturur;
//   - rota defterde (buildMux), FE parçalayıcı tavanları Go ile aynı.
//
// Yalnız httptest sahteleri; canlı Thanos'a asla bağlanılmaz.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── sahte Thanos ─────────────────────────────────────────────────────

type tpmFake struct {
	*httptest.Server
	mu      sync.Mutex
	ranges  int
	instant int
	steps   []string
}

func (f *tpmFake) calls() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ranges, f.instant
}

func newTPMFake(t *testing.T, respond func(path, q string) (int, string)) *tpmFake {
	t.Helper()
	f := &tpmFake{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		switch r.URL.Path {
		case "/api/v1/query_range":
			f.ranges++
			f.steps = append(f.steps, r.URL.Query().Get("step"))
		case "/api/v1/query":
			f.instant++
		}
		f.mu.Unlock()
		code, body := respond(r.URL.Path, r.URL.Query().Get("query"))
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

// tpmHappy — fraud-score pod'unun CPU/bellek serisi; diğer her şey boş başarı.
func tpmHappy(path, q string) (int, string) {
	switch {
	case strings.Contains(q, "container_cpu_usage_seconds_total"):
		return 200, `{"status":"success","data":{"resultType":"matrix","result":[` +
			`{"metric":{"namespace":"payments","pod":"fraud-score-prod-6b7d9f8c5-m3t9w"},"values":[[1790496000,"0.5"]]}]}}`
	case strings.Contains(q, "container_memory_working_set_bytes"):
		return 200, `{"status":"success","data":{"resultType":"matrix","result":[` +
			`{"metric":{"namespace":"payments","pod":"fraud-score-prod-6b7d9f8c5-m3t9w"},"values":[[1790496000,"1024"]]}]}}`
	case path == "/api/v1/query_range":
		return 200, `{"status":"success","data":{"resultType":"matrix","result":[]}}`
	}
	return 200, `{"status":"success","data":{"resultType":"vector","result":[]}}`
}

const (
	tpmCID     = "c-1a2b3c4d"
	tpmCluster = "dc-east-1"
	tpmFromNs  = int64(1790496000) * 1e9
	tpmToNs    = int64(1790496900) * 1e9 // 15 dk
)

// newTPMServer — iki kayıt: dc-east-1 etkin, dc-east-2 kapalı. clusters
// verilirse onlar kullanılır (URL boşsa sahteye bağlanır).
func newTPMServer(t *testing.T, f *tpmFake, clusters ...thanos.ClusterConfig) (*Server, *http.ServeMux) {
	t.Helper()
	if clusters == nil {
		clusters = []thanos.ClusterConfig{
			{ID: tpmCID, Name: tpmCluster, Enabled: true},
			{ID: "c-5e6f7a8b", Name: "dc-east-2", Enabled: false},
		}
	}
	for i := range clusters {
		if clusters[i].URL == "" {
			clusters[i].URL = f.URL
		}
	}
	svc := thanos.New()
	svc.Configure(thanos.Settings{Clusters: clusters})
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), thanos: svc}
	mux := http.NewServeMux()
	s.registerTracePodMetricsRoutes(mux)
	return s, mux
}

func tpmQuery(cv string, from, to int64, mdp string, pods ...string) url.Values {
	v := url.Values{}
	v.Set("cv", cv)
	if from != 0 {
		v.Set("from", strconv.FormatInt(from, 10))
	}
	if to != 0 {
		v.Set("to", strconv.FormatInt(to, 10))
	}
	if mdp != "" {
		v.Set("mdp", mdp)
	}
	if pods != nil {
		v.Set("pods", strings.Join(pods, ","))
	}
	return v
}

func tpmGet(mux *http.ServeMux, q url.Values) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/trace-pods/metrics?"+q.Encode(), nil))
	return rec
}

type tpmBody struct {
	Thanos         bool             `json:"thanos"`
	ClusterValue   string           `json:"clusterValue"`
	Mapped         bool             `json:"mapped"`
	UnmappedReason string           `json:"unmappedReason"`
	Cluster        *tracePodCluster `json:"cluster"`
	Start          int64            `json:"start"`
	Step           int              `json:"step"`
	Points         int              `json:"points"`
	Instant        string           `json:"instant"`
	Pods           []struct {
		NS       string `json:"ns"`
		NsFilled bool   `json:"nsFilled"`
		Pod      string `json:"pod"`
		State    string `json:"state"`
	} `json:"pods"`
}

func decodeTPM(t *testing.T, rec *httptest.ResponseRecorder) tpmBody {
	t.Helper()
	var b tpmBody
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("gövde JSON değil: %v — %s", err, rec.Body.String())
	}
	return b
}

func tpmPods(n, width int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("fraud-score-prod-%0*d", width, i)
		out = append(out, "payments/"+name)
	}
	return out
}

// ── 1. Doğrulama ────────────────────────────────────────────────────

func TestTracePodMetricsValidation(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	pod := "payments/fraud-score-prod-6b7d9f8c5-m3t9w"
	cases := []struct {
		name string
		q    url.Values
		want string // gövdede aranan parça
	}{
		{"pods yok", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120"), "pod"},
		{"pods boş", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", ""), "pod"},
		{"yalnız boş girdiler", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", " ", " "), "pod"},
		{"65 pod", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", tpmPods(65, 3)...), "64"},
		{"eğik çizgisiz girdi", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "fraud-score-prod-0"), "ns/pod"},
		{"boş pod", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/"), "pod"},
		{"pod 254 karakter", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/"+long(254)), "253"},
		{"namespace 64 karakter", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", long(64)+"/fraud-score-prod-0"), "63"},
		{"from yok", tpmQuery(tpmCluster, 0, tpmToNs, "120", pod), "from"},
		{"to yok", tpmQuery(tpmCluster, tpmFromNs, 0, "120", pod), "to"},
		{"from sayı değil", func() url.Values {
			v := tpmQuery(tpmCluster, 0, tpmToNs, "120", pod)
			v.Set("from", "dün")
			return v
		}(), "from"},
		{"from == to", tpmQuery(tpmCluster, tpmFromNs, tpmFromNs, "120", pod), "from"},
		{"from > to", tpmQuery(tpmCluster, tpmToNs, tpmFromNs, "120", pod), "from"},
		{"pencere 6 sa + 1 sn", tpmQuery(tpmCluster, tpmFromNs, tpmFromNs+int64(6*time.Hour+time.Second), "120", pod), "6"},
		// 64 × (63+1) = 4096 > 4000 — eşlenmiş cluster'da (adım 5).
		{"seçici 4000 karakteri aşıyor", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", tpmPods(64, 46)...), "4000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTPMFake(t, tpmHappy)
			_, mux := newTPMServer(t, f)
			rec := tpmGet(mux, tc.q)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("400 beklenir, %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("gövde %q içermeli: %s", tc.want, rec.Body.String())
			}
			if r, i := f.calls(); r+i != 0 {
				t.Fatalf("400 Thanos'a gitmemeli: %d range + %d instant", r, i)
			}
		})
	}
}

func TestTracePodMetricsValidationBoundariesAccepted(t *testing.T) {
	for _, tc := range []struct {
		name string
		q    url.Values
	}{
		{"tam 6 sa pencere", tpmQuery(tpmCluster, tpmFromNs, tpmFromNs+int64(6*time.Hour), "120", "payments/fraud-score-prod-0")},
		{"tam 64 pod", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", tpmPods(64, 3)...)},
		{"65 girdi, biri tekrar → 64", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", append(tpmPods(64, 3), "payments/fraud-score-prod-000")...)},
		{"pod 253 / ns 63 karakter", tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", strings.Repeat("n", 63)+"/"+strings.Repeat("p", 253))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTPMFake(t, tpmHappy)
			_, mux := newTPMServer(t, f)
			if rec := tpmGet(mux, tc.q); rec.Code != http.StatusOK {
				t.Fatalf("200 beklenir, %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestParseTracePodRefs(t *testing.T) {
	refs, err := parseTracePodRefs(" payments/fraud-score-prod-0 ,/audit-log-prod-1,payments/fraud-score-prod-0,,audit/audit-log-prod-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []thanos.TracePodRef{
		{Namespace: "payments", Pod: "fraud-score-prod-0"},
		{Namespace: "", Pod: "audit-log-prod-1"},
		{Namespace: "audit", Pod: "audit-log-prod-1"},
	}
	if fmt.Sprint(refs) != fmt.Sprint(want) {
		t.Fatalf("tekilleştirme + sıra:\n got  %v\n want %v", refs, want)
	}
	for _, bad := range []string{"", ",", "x", "ns/", strings.Repeat("a,", 1)} {
		if _, err := parseTracePodRefs(bad); err == nil {
			t.Errorf("%q reddedilmeli", bad)
		}
	}
}

func TestTracePodMDP(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 120}, {"abc", 120}, {"0", 120}, {"-5", 120},
		{"100", 120}, {"120", 120}, {"121", 240}, {"240", 240}, {"480", 480}, {"100000", 480},
	} {
		if got := tracePodMDP(tc.raw); got != tc.want {
			t.Errorf("tracePodMDP(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// ── 2. Eşleme: 200, önbelleksiz, sıfır Thanos çağrısı ─────────────────

func TestTracePodMetricsUnmappedStates(t *testing.T) {
	pod := "payments/fraud-score-prod-0"
	cases := []struct {
		name       string
		cv         string
		clusters   []thanos.ClusterConfig
		nilThanos  bool
		wantThanos bool
		wantReason string
		wantCV     string
	}{
		{name: "cv boş", cv: "", wantThanos: true, wantReason: "no_cluster_value"},
		{name: "cv yalnız boşluk", cv: "   ", wantThanos: true, wantReason: "no_cluster_value"},
		{name: "cv bilinmiyor", cv: "dc-east-9", wantThanos: true, wantReason: "no_remote_cluster", wantCV: "dc-east-9"},
		{name: "cv kapalı cluster'ın", cv: "dc-east-2", wantThanos: true, wantReason: "no_remote_cluster", wantCV: "dc-east-2"},
		{name: "etkin cluster yok", cv: tpmCluster, wantThanos: false, wantCV: tpmCluster,
			clusters: []thanos.ClusterConfig{{ID: tpmCID, Name: tpmCluster, Enabled: false}}},
		{name: "thanos servisi yok", cv: tpmCluster, nilThanos: true, wantThanos: false, wantCV: tpmCluster},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newTPMFake(t, tpmHappy)
			s, mux := newTPMServer(t, f, tc.clusters...)
			if tc.nilThanos {
				s.thanos = nil
			}
			rec := tpmGet(mux, tpmQuery(tc.cv, tpmFromNs, tpmToNs, "120", pod))
			if rec.Code != http.StatusOK {
				t.Fatalf("200 beklenir, %d: %s", rec.Code, rec.Body.String())
			}
			b := decodeTPM(t, rec)
			if b.Thanos != tc.wantThanos || b.Mapped || b.UnmappedReason != tc.wantReason ||
				b.ClusterValue != tc.wantCV || b.Cluster != nil {
				t.Fatalf("gövde: %+v", b)
			}
			if !strings.Contains(rec.Body.String(), `"pods":[]`) {
				t.Fatalf("pods null değil [] olmalı: %s", rec.Body.String())
			}
			if rec.Header().Get("X-Cache") != "" {
				t.Fatalf("eşlenmemiş cevap önbellekten geçmemeli: X-Cache=%s", rec.Header().Get("X-Cache"))
			}
			if r, i := f.calls(); r+i != 0 {
				t.Fatalf("eşlenmemiş durum Thanos'a gitmemeli: %d + %d", r, i)
			}
		})
	}
}

// ── 3. Önbellek anahtarı ────────────────────────────────────────────

func TestTracePodMetricsKey(t *testing.T) {
	a := thanos.TracePodRef{Namespace: "payments", Pod: "fraud-score-prod-0"}
	b := thanos.TracePodRef{Namespace: "audit", Pod: "audit-log-prod-1"}
	c := thanos.TracePodRef{Namespace: "", Pod: "audit-log-prod-1"}
	from, to := time.Unix(1790496000, 0), time.Unix(1790496900, 0)
	base := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, b}, from, to, 120, false)

	if !strings.HasPrefix(base, "trace-pod-metrics:v1:"+tpmCID+":d1:") || !strings.HasSuffix(base, ":mdp=120:c=0") {
		t.Fatalf("anahtar biçimi: %s", base)
	}
	if got := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{b, a}, from, to, 120, false); got != base {
		t.Fatalf("pod sırası anahtarı değiştirmemeli:\n %s\n %s", base, got)
	}
	if got := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, b}, from.Add(10*time.Second), to.Add(10*time.Second), 120, false); got != base {
		t.Fatalf("aynı 30 s kovası aynı anahtar: %s", got)
	}
	for name, k := range map[string]string{
		"pod kümesi":        tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a}, from, to, 120, false),
		"namespace'siz ref": tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, c}, from, to, 120, false),
		"pencere kovası":    tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, b}, from.Add(time.Minute), to.Add(time.Minute), 120, false),
		"mdp basamağı":      tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, b}, from, to, 240, false),
		"cfg özeti":         tracePodMetricsKey(tpmCID, "d2", []thanos.TracePodRef{a, b}, from, to, 120, false),
		"cluster kimliği":   tracePodMetricsKey("c-5e6f7a8b", "d1", []thanos.TracePodRef{a, b}, from, to, 120, false),
	} {
		if k == base {
			t.Errorf("%s anahtarı ayırmalı: %s", name, k)
		}
	}
	// v0.10.968 — kapanış evresi: açık pencerede yazılan gövde, to+90 s'deki
	// tek yeniden çekmeye STALE olarak dönmesin diye ayrı anahtar.
	if closed := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{a, b}, from, to, 120, true); closed == base ||
		!strings.HasSuffix(closed, ":mdp=120:c=1") || !strings.HasSuffix(base, ":mdp=120:c=0") {
		t.Errorf("kapanış evresi anahtarı ayırmalı: %s / %s", base, closed)
	}
	// "a|b"+"c" ≠ "a"+"b|c": ref sınırları özetin içinde korunur.
	x := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{{Namespace: "p", Pod: "a,b/c"}}, from, to, 120, false)
	y := tracePodMetricsKey(tpmCID, "d1", []thanos.TracePodRef{{Namespace: "p", Pod: "a"}, {Namespace: "b", Pod: "c"}}, from, to, 120, false)
	if x == y {
		t.Error("ref sınırı özetlenirken kaybolmamalı")
	}
}

func TestTracePodCfgDigest(t *testing.T) {
	base := thanos.ClusterConfig{ID: tpmCID, Name: tpmCluster, URL: "http://thanos-a.example.invalid"}
	d := tracePodCfgDigest(base)
	for name, c := range map[string]thanos.ClusterConfig{
		"URL":             {ID: tpmCID, Name: tpmCluster, URL: "http://thanos-b.example.invalid"},
		"NamespaceFilter": {ID: tpmCID, Name: tpmCluster, URL: base.URL, NamespaceFilter: "payments"},
		"cluster etiketi": {ID: tpmCID, Name: tpmCluster, URL: base.URL, ThanosLabelName: "cluster"},
		"etiket değeri":   {ID: tpmCID, Name: tpmCluster, URL: base.URL, ThanosLabelName: "cluster", ThanosLabelValue: "dc-east-2"},
		// v0.10.968 — ad gövdeye (cluster.name → pod linki) girer; ID sabit kalır.
		"ad (etiketsiz)": {ID: tpmCID, Name: "dc-east-2", URL: base.URL},
	} {
		if tracePodCfgDigest(c) == d {
			t.Errorf("%s değişimi cfg özetini değiştirmeli", name)
		}
	}
	// v0.10.968 — açık etiket değeri adı gölgelerken de ad özete girer.
	lb := thanos.ClusterConfig{ID: tpmCID, Name: tpmCluster, URL: base.URL, ThanosLabelName: "cluster", ThanosLabelValue: "dc-east-1"}
	lr := lb
	lr.Name = "dc-east-2"
	if tracePodCfgDigest(lb) == tracePodCfgDigest(lr) {
		t.Error("açık etiket değeriyle yeniden adlandırma da cfg özetini değiştirmeli")
	}
	tok := base
	tok.Token = "tok-rotated"
	if tracePodCfgDigest(tok) != d {
		t.Error("token dönüşü sonucu değiştirmez; özet aynı kalmalı (clusterCfgDigest emsali)")
	}
}

// ── 4. TTL ──────────────────────────────────────────────────────────

// TestTracePodWindowClosed — v0.10.968 — kapanış evresi sınırı: to+59 s açık,
// to+60 s kapalı (istemci to+90 s'de çeker; 30 s saat kayması payı).
func TestTracePodWindowClosed(t *testing.T) {
	to := time.Unix(1790496900, 0)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{to.Add(-time.Minute), false},
		{to, false},
		{to.Add(59 * time.Second), false},
		{to.Add(60 * time.Second), true},
		{to.Add(90 * time.Second), true},
		{to.Add(24 * time.Hour), true},
	} {
		if got := tracePodWindowClosed(to, tc.now); got != tc.want {
			t.Errorf("now=to%+v: %v, want %v", tc.now.Sub(to), got, tc.want)
		}
	}
}

func TestTracePodMetricsTTL(t *testing.T) {
	now := time.Unix(1790500000, 0)
	for _, tc := range []struct {
		name string
		to   time.Time
		want time.Duration
	}{
		{"to = şimdi", now, 60 * time.Second},
		{"to gelecekte (açık pencere)", now.Add(5 * time.Minute), 60 * time.Second},
		{"to = şimdi-10dk (sınır, açık)", now.Add(-10 * time.Minute), 60 * time.Second},
		{"to şimdi-10dk'dan 1 ns eski", now.Add(-10*time.Minute - time.Nanosecond), 10 * time.Minute},
		{"to bir gün önce", now.Add(-24 * time.Hour), 10 * time.Minute},
	} {
		if got := tracePodMetricsTTL(tc.to, now); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// ── 5. Zaman aşımı + önbellek ───────────────────────────────────────

func TestTracePodMetricsTimeoutIs502AndNotCached(t *testing.T) {
	prev := tracePodMetricsTimeout
	tracePodMetricsTimeout = 50 * time.Millisecond
	t.Cleanup(func() { tracePodMetricsTimeout = prev })
	release := make(chan struct{})
	f := newTPMFake(t, func(path, q string) (int, string) {
		select {
		case <-release:
		case <-time.After(3 * time.Second):
		}
		return tpmHappy(path, q)
	})
	t.Cleanup(func() { close(release) }) // LIFO: f.Close'tan ÖNCE koşar
	_, mux := newTPMServer(t, f)
	q := tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/fraud-score-prod-6b7d9f8c5-m3t9w")

	t0 := time.Now()
	rec := tpmGet(mux, q)
	if el := time.Since(t0); el > 2*time.Second {
		t.Fatalf("handler deadline'ı uygulanmadı: %v", el)
	}
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("502 beklenir, %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "zaman aşımı") || !strings.Contains(body, tpmCluster) || !strings.Contains(body, "Thanos 1 sn") {
		t.Fatalf("gövde cluster adı + süre + \"zaman aşımı\" demeli: %s", body)
	}
	r1, _ := f.calls()
	rec2 := tpmGet(mux, q)
	r2, _ := f.calls()
	if rec2.Code != http.StatusBadGateway || r2 <= r1 {
		t.Fatalf("hata önbelleğe girmemeli: ikinci çağrı %d, range çağrısı %d → %d", rec2.Code, r1, r2)
	}
}

func TestTracePodMetricsRangeErrorIs502AndNotCached(t *testing.T) {
	f := newTPMFake(t, func(path, q string) (int, string) {
		if strings.Contains(q, "container_cpu_usage_seconds_total") {
			return 500, `{"status":"error","errorType":"internal","error":"boom"}`
		}
		return tpmHappy(path, q)
	})
	_, mux := newTPMServer(t, f)
	q := tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/fraud-score-prod-6b7d9f8c5-m3t9w")
	rec := tpmGet(mux, q)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("502 beklenir, %d: %s", rec.Code, rec.Body.String())
	}
	if b := rec.Body.String(); !strings.Contains(b, tpmCluster) || !strings.Contains(b, "HTTP 500") || strings.Contains(b, "zaman aşımı") {
		t.Fatalf("gövde cluster adını + Thanos metnini taşımalı, zaman aşımı DEMEMELİ: %s", b)
	}
	// v0.10.968 — asıl sebep ("boom") ön ekler arasında kaybolmaz; "thanos
	// <ad>:" katmanı tekrarlanmaz, uç nokta sızmaz.
	if b := rec.Body.String(); !strings.Contains(b, "CPU sorgusu: HTTP 500 internal: boom") ||
		strings.Contains(b, "thanos "+tpmCluster) || strings.Contains(b, "127.0.0.1") {
		t.Fatalf("gövde temiz sebebi taşımalı: %s", b)
	}
	r1, _ := f.calls()
	tpmGet(mux, q)
	if r2, _ := f.calls(); r2 <= r1 {
		t.Fatalf("hata önbelleğe girmemeli: range çağrısı %d → %d", r1, r2)
	}
}

// TestTracePodMetricsTransportErrorDoesNotLeak — v0.10.968 — taşıma hatası
// (*url.Error) yapılandırılmış uç nokta URL'sini ve tam PromQL'i taşır; rol
// kapısı olmayan bu uçta her viewer'a gidiyordu. Gövde kaba sebebi söyler,
// ne host ne sorgu yolu sızar (SIZINTI KURALI, thanos/console.go).
func TestTracePodMetricsTransportErrorDoesNotLeak(t *testing.T) {
	f := newTPMFake(t, tpmHappy)
	_, mux := newTPMServer(t, f)
	f.Close() // bağlantı reddedilir
	rec := tpmGet(mux, tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/fraud-score-prod-6b7d9f8c5-m3t9w"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("502 beklenir, %d: %s", rec.Code, rec.Body.String())
	}
	b := rec.Body.String()
	if !strings.Contains(b, "connection failed") || !strings.Contains(b, tpmCluster) {
		t.Fatalf("gövde kaba sebebi + cluster adını taşımalı: %s", b)
	}
	u, _ := url.Parse(f.URL)
	for _, leak := range []string{"127.0.0.1", u.Host, "query_range", "container_cpu_usage", "sum+by", "dial tcp"} {
		if strings.Contains(b, leak) {
			t.Errorf("gövde %q sızdırmamalı: %s", leak, b)
		}
	}
}

func TestTracePodMetricsSuccessIsCached(t *testing.T) {
	f := newTPMFake(t, tpmHappy)
	_, mux := newTPMServer(t, f)
	q := tpmQuery(tpmCluster, tpmFromNs, tpmToNs, "120", "payments/fraud-score-prod-6b7d9f8c5-m3t9w")
	if rec := tpmGet(mux, q); rec.Code != http.StatusOK || rec.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("ilk çağrı MISS: %d %s", rec.Code, rec.Header().Get("X-Cache"))
	}
	r1, i1 := f.calls()
	if r1 != 2 || i1 != 6 {
		t.Fatalf("tam 2 range + 6 instant: %d + %d", r1, i1)
	}
	rec := tpmGet(mux, q)
	if r2, i2 := f.calls(); rec.Header().Get("X-Cache") != "HIT-L1" || r2 != r1 || i2 != i1 {
		t.Fatalf("ikinci çağrı önbellekten: %s, çağrı %d+%d", rec.Header().Get("X-Cache"), r2, i2)
	}
}

// ── 6. Yanıt şekli ──────────────────────────────────────────────────

func TestTracePodMetricsResponseShape(t *testing.T) {
	f := newTPMFake(t, tpmHappy)
	_, mux := newTPMServer(t, f)
	pods := []string{
		"audit/audit-log-prod-5f9c7d6b8-q2w4e",
		"/fraud-score-prod-6b7d9f8c5-m3t9w", // namespace'siz → sunucu doldurur
		"shop/checkout-api-7c8d9e0f1-z9x8c",
	}
	rec := tpmGet(mux, tpmQuery(" "+tpmCluster+" ", tpmFromNs, tpmToNs, "", pods...))
	if rec.Code != http.StatusOK {
		t.Fatalf("200 beklenir, %d: %s", rec.Code, rec.Body.String())
	}
	b := decodeTPM(t, rec)
	if !b.Thanos || !b.Mapped || b.ClusterValue != tpmCluster || b.UnmappedReason != "" ||
		b.Cluster == nil || b.Cluster.ID != tpmCID || b.Cluster.Name != tpmCluster {
		t.Fatalf("başlık: %+v", b)
	}
	if b.Start != 1790496000 || b.Step != 15 || b.Points != 61 || b.Instant != "ok" {
		t.Fatalf("ızgara: start=%d step=%d points=%d instant=%s", b.Start, b.Step, b.Points, b.Instant)
	}
	if len(b.Pods) != 3 || b.Pods[0].Pod != "audit-log-prod-5f9c7d6b8-q2w4e" ||
		b.Pods[1].Pod != "fraud-score-prod-6b7d9f8c5-m3t9w" || b.Pods[2].Pod != "checkout-api-7c8d9e0f1-z9x8c" {
		t.Fatalf("ref başına bir satır, istek sırasıyla: %+v", b.Pods)
	}
	if p := b.Pods[1]; p.State != "ok" || p.NS != "payments" || !p.NsFilled {
		t.Fatalf("namespace'siz ref doldurulmalı: %+v", p)
	}
	if b.Pods[0].State != "no_samples" || b.Pods[2].State != "no_samples" {
		t.Fatalf("serisiz pod'lar no_samples: %+v", b.Pods)
	}
}

func TestTracePodMetricsMDPSnapsToRung(t *testing.T) {
	// 2 sa pencere: mdp 120 → 60 s, mdp 240 → 30 s.
	twoH := tpmFromNs + int64(2*time.Hour)
	for _, tc := range []struct {
		mdp  string
		want int
	}{{"100", 60}, {"120", 60}, {"", 60}, {"abc", 60}, {"240", 30}} {
		t.Run("mdp="+tc.mdp, func(t *testing.T) {
			f := newTPMFake(t, tpmHappy)
			_, mux := newTPMServer(t, f)
			rec := tpmGet(mux, tpmQuery(tpmCluster, tpmFromNs, twoH, tc.mdp, "payments/fraud-score-prod-6b7d9f8c5-m3t9w"))
			if rec.Code != http.StatusOK {
				t.Fatalf("200 beklenir, %d: %s", rec.Code, rec.Body.String())
			}
			if b := decodeTPM(t, rec); b.Step != tc.want {
				t.Fatalf("step %d, want %d", b.Step, tc.want)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, s := range f.steps {
				if s != strconv.Itoa(tc.want) {
					t.Fatalf("Thanos'a giden step %s, want %d", s, tc.want)
				}
			}
		})
	}
}

// ── 7. Rota hijyeni ─────────────────────────────────────────────────

func TestTracePodMetricsRouteRegistered(t *testing.T) {
	mux := (&Server{}).buildMux()
	_, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, "/api/trace-pods/metrics", nil))
	if pattern != "GET /api/trace-pods/metrics" {
		t.Fatalf("rota defterde değil (SPA catch-all'a düşer, 200 + boş ekran): %q", pattern)
	}
}

// ── 8. FE parçalayıcı paritesi (route_pins emsali) ────────────────────

func TestTracePodChunkLimitsMatchFrontend(t *testing.T) {
	b, err := os.ReadFile("../../frontend/src/pages/trace/traceMetricsModel.ts")
	if err != nil {
		// v0.10.968 — entegrasyon bitti: dosya yoksa pin sessizce atlanmaz;
		// yeniden adlandırma bu testi kırmalı (route_pins emsali).
		t.Fatalf("traceMetricsModel.ts okunamadı: %v", err)
	}
	for name, want := range map[string]int{
		"TRACE_POD_CHUNK_MAX":       thanos.TracePodChunkMax,
		"TRACE_POD_CHUNK_REGEX_MAX": thanos.TracePodRegexMax,
	} {
		m := regexp.MustCompile(`export const ` + name + `\s*=\s*(\d+)\s*;`).FindSubmatch(b)
		if m == nil {
			t.Errorf("%s bulunamadı — yeniden adlandırıldıysa bu pin de taşınmalı", name)
			continue
		}
		if got, _ := strconv.Atoi(string(m[1])); got != want {
			t.Errorf("FE %s = %d ≠ Go %d — FE parçaları sunucunun 400'üne çarpar", name, got, want)
		}
	}
}
