package api

// rollouts_v2_probe_test.go — v0.10.979 — Rollouts v2 §11 sorgu paketi admin
// probe'unun HTTP sözleşmesi, SAHTE Thanos'a karşı (httptest; canlı çağrı
// YOK; birim başına ayrı sahte sunucu). Sahte, cevapları test tarafında
// v2probe.Expand + thanos.ClusterConfig.EffectiveQuery/EffectiveMatchers ile
// hesaplanan TAM etkin ifadeyle anahtarlar; bilinmeyen ifade 400 bad_data
// döner VE kaydedilir → test o listeyi basarak düşer ("sorgu başına hazır
// cevap" böyle dürüst kalır).
//
// Kapsam: defter kaydı + rol kapısı, guardrail'ler (istek YOK), mutlu yol
// (partial_response, dedup, matcher'lı/matcher'sız çift, label API
// parametreleri, ns tekrarı, sızıntı, §11.9 sütunları), erken durma, bütçe,
// audit (tek satır, değer/jeton yok), TTL, T dikişi (fallback notu, store
// yok), ?format=md, H5.1 bad_data → H5.2 fallback (yalnız adlar).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout/v2probe"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── Sahte Thanos ─────────────────────────────────────────────────────────

type probeFakeReq struct {
	Unit, Method, Path string
	Form               url.Values
}

type probeFake struct {
	mu      sync.Mutex
	reqs    []probeFakeReq
	answers map[string]string // unit|key → gövde
	unknown []string
	fail    func(unit string, req probeFakeReq) (int, string)
}

func probeKey(req probeFakeReq) string {
	switch {
	case req.Path == "/api/v1/query":
		return "q|" + req.Form.Get("query")
	case req.Path == "/api/v1/labels":
		return "labels|" + strings.Join(req.Form["match[]"], " ")
	case strings.HasPrefix(req.Path, "/api/v1/label/"):
		return "values|" + strings.Join(req.Form["match[]"], " ")
	}
	return "?|" + req.Path
}

func (f *probeFake) server(t *testing.T, unit string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		req := probeFakeReq{Unit: unit, Method: r.Method, Path: r.URL.Path, Form: r.Form}
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		fail := f.fail
		body, ok := f.answers[unit+"|"+probeKey(req)]
		if !ok {
			f.unknown = append(f.unknown, unit+"|"+probeKey(req))
		}
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if fail != nil {
			if code, b := fail(unit, req); code != 0 {
				w.WriteHeader(code)
				fmt.Fprint(w, b)
				return
			}
		}
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"unknown fixture expression"}`)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *probeFake) requests(unit string) []probeFakeReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []probeFakeReq
	for _, r := range f.reqs {
		if unit == "" || r.Unit == unit {
			out = append(out, r)
		}
	}
	return out
}

// ── Fixture cevapları (HAM nöbetçi değerlerle) ───────────────────────────

const probeTS = "1784271068.5"

func vec(rows ...string) string {
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(rows, ",") + `]}}`
}

func vrow(v string, kv ...string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return `{"metric":` + string(b) + `,"value":[` + probeTS + `,"` + v + `"]}`
}

func vnames(names ...string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, vrow("3", "__name__", n))
	}
	return out
}

func strList(names ...string) string {
	b, _ := json.Marshal(names)
	return `{"status":"success","data":` + string(b) + `}`
}

var probeScalars = map[string]string{
	"K0.1": "180", "K0.4": "1", "K0.5": "10", "K0.6": "42", "K0.6b": "1784271060", "K2.2a": "900", "K2.2b": "900", "K2.2c": "900",
	"K2.2d": "0", "K2.2e": "850", "K2.4b": "30", "K3.1": "1200", "K3.2": "900", "K3.7": "3000", "D2": "10", "D8": "150", "D7": "1",
	"H0.1": "40000", "H0.4": "40000", "H1.6a": "9000", "H1.6b": "3000", "H1.6c": "0", "H2.3": "5", "N1": "0.93",
}

const (
	probeNS   = "team-payments-prod"
	probeJob  = "team-payments-prod-metrics"
	probeDest = "https://api.real.example.invalid:6443"
)

func probeAnswer(q v2probe.Query, variant string) string {
	switch q.Kind {
	case v2probe.KindLabels:
		switch q.ID {
		case "K2.1a":
			return strList("container", "image", "image_id", "image_spec", "namespace", "pod")
		case "K2.1b":
			return strList("namespace", "owner_is_controller", "owner_kind", "owner_name", "replicaset")
		case "K2.1d":
			return strList("namespace", "revision", "statefulset")
		case "H5.1a":
			return strList("dest_namespace", "dest_server", "exported_namespace", "health_status", "job", "name", "namespace", "sync_status")
		}
		return strList("job", "namespace", "pod")
	case v2probe.KindLabelValues:
		if q.ID == "R0" {
			return strList()
		}
		return strList("kube_daemonset_metadata_generation", "kube_deployment_metadata_generation", "kube_replicaset_owner")
	}
	if q.Shape == v2probe.ShapeNames { // H5.2 fallback: değerler nöbetçi, yalnız adlar kalmalı
		return vec(vrow("1", "name", "checkout-real-api-prod-ocpa", "namespace", probeNS, "dest_server", probeDest, "project", "payments-real"))
	}
	switch q.ID {
	case "K0.2":
		return vec(vrow("1", "job", "kube-state-metrics"), vrow("1", "job", "openshift-state-metrics"))
	case "K0.3":
		return vec(vrow("1", "version", "v2.13.0"))
	case "K1.D":
		return vec(vnames("kube_deployment_metadata_generation", "kube_deployment_status_observed_generation", "kube_deployment_status_replicas")...)
	case "K1.R":
		return vec(vnames("kube_replicaset_owner", "kube_replicaset_spec_replicas")...)
	case "K1.P":
		return vec(vnames("kube_pod_container_info", "kube_pod_owner")...)
	case "K1.S":
		return vec(vnames("kube_statefulset_metadata_generation", "kube_statefulset_status_observed_generation", "kube_statefulset_status_current_revision", "kube_statefulset_status_update_revision")...)
	case "K1.DS":
		return vec(vnames("kube_daemonset_metadata_generation", "kube_daemonset_status_observed_generation")...)
	case "K1.H":
		return vec(vrow("8", "scaletargetref_kind", "Deployment"))
	case "K2.3a":
		return vec(vrow("1150", "owner_kind", "Deployment", "owner_is_controller", "true"))
	case "K2.3b":
		return vec(vrow("800", "owner_kind", "ReplicaSet"), vrow("30", "owner_kind", "DaemonSet"))
	case "K2.3c":
		return vec(vrow("158", "condition", "Available", "status", "true", "reason", "MinimumReplicasAvailable"))
	case "D5", "R1", "R3", "H1.3a", "H1.3b", "N6", "N7", "K6.4":
		return vec()
	case "D6":
		return vec(vnames("openshift_deploymentconfig_spec_replicas")...)
	case "H0.2":
		if variant == "dedup_off" {
			return vec(vrow("40000", "prometheus", "k8s", "prometheus_replica", "prometheus-k8s-0"), vrow("40000", "prometheus", "k8s", "prometheus_replica", "prometheus-k8s-1"))
		}
		return vec(vrow("40000", "prometheus", "k8s", "prometheus_replica", ""))
	case "H0.3", "H0.5":
		// v0.10.979 — küme kimliği etiketleri nöbetçili (k8s_cluster olayı).
		return vec(vrow("40000", "cluster", "hubraw", "prometheus", "k8s", "k8s_cluster", "realcluster-prod-01",
			"openshift_cluster", "hub-real-02", "cluster_name", "hub-real-01", "tenant_id", "realcluster-prod-02"))
	case "H1.1", "H1.5":
		return vec(vrow("12000", "namespace", probeNS, "job", probeJob))
	case "H1.2":
		return vec(vrow("12000", "namespace", probeNS, "exported_namespace", probeNS, "job", probeJob))
	case "H1.4", "H4.4", "N2", "N7x":
		return vec(vrow("2", "namespace", probeNS, "autosync_enabled", "true"))
	case "H2.1":
		return vec(vrow("20000", "dest_server", probeDest), vrow("100", "dest_server", "https://kubernetes.default.svc"), vrow("5", "dest_server", ""))
	case "H2.5":
		return vec(vrow("2", "port", "6443"))
	case "H2.7":
		return vec(vrow("3000", "apps_per_target_ns", "1"))
	case "H3.2":
		return vec(vrow("38000", "same_name_same_server", "1"))
	case "H3.4":
		return vec(vrow("17000", "clusters_per_base", "2"))
	case "H4.1":
		return vec(vrow("37000", "sync_status", "Synced", "health_status", "Healthy"))
	case "H4.2":
		return vec(vrow("39900", "operation", ""))
	case "H4.3", "H6.3":
		return vec(vrow("30000", "autosync_enabled", "true"), vrow("10000", "autosync_enabled", "false"))
	case "H5.3e":
		return vec(vrow("1", "namespace", probeNS, "version", "v2.13.3"))
	case "H6.2a", "H6.2b":
		return vec(vrow("120", "phase", "Succeeded"))
	case "N4":
		return vec(vrow("300", "sfx", "zzz"), vrow("120", "sfx", "ocpa"))
	case "N5a":
		return vec(vrow("19000", "dest_server", probeDest, "sfx", "ocpa"))
	case "N5b":
		return vec(vrow("0.95", "dest_server", probeDest))
	case "L2":
		return vec(vrow("2000", "namespace", probeNS, "project", "payments-real"))
	}
	if q.Shape == v2probe.ShapeRows {
		return vec(vrow("3", "__name__", "fixture_metric"))
	}
	v, ok := probeScalars[q.ID]
	if !ok {
		v = "7"
	}
	if variant == "nomatch" && q.ID == "H0.1" {
		v = "41000"
	}
	if variant == "dedup_off" && q.ID == "K0.4" {
		v = "2"
	}
	return vec(vrow(v))
}

// ── Test ortamı ──────────────────────────────────────────────────────────

const (
	probeIDA    = "c-aaaa0001"
	probeIDB    = "c-bbbb0002"
	probeIDHub1 = "c-hub10001"
	probeIDHub2 = "c-hub20002"
)

var probeSentinels = []string{"realcluster-prod-01", "realcluster-prod-02", "hub-real-01", "hub-real-02", "hubraw",
	"api.real.example.invalid", "team-payments", "checkout-real-api", "payments-real", "prometheus-k8s-0"}

type probeUnitFx struct {
	name, id string
	role     string
	cfg      thanos.ClusterConfig
	params   v2probe.Params
}

type probeEnv struct {
	s        *Server
	mux      *http.ServeMux
	fake     *probeFake
	clusters []thanos.ClusterConfig
	units    []probeUnitFx
}

func newProbeEnv(t *testing.T) *probeEnv {
	t.Helper()
	f := &probeFake{answers: map[string]string{}}
	clusters := []thanos.ClusterConfig{
		{ID: probeIDA, Name: "realcluster-prod-01", URL: f.server(t, "cluster-a"), Enabled: true, NamespaceFilter: "team-.*", ArgoSuffix: "ocpa", PairGroup: "pg-real",
			APIServerURLs: []string{probeDest}, SpanClusterValues: []string{"realcluster-prod-01"}},
		{ID: probeIDB, Name: "realcluster-prod-02", URL: f.server(t, "cluster-b"), Enabled: true, ArgoSuffix: "ocpb", PairGroup: "pg-real",
			APIServerURLs: []string{"https://api.real2.example.invalid:6443"}},
		{ID: probeIDHub1, Name: "hub-real-01", URL: f.server(t, "hub-1"), Enabled: true, ThanosLabelName: "cluster", ThanosLabelValue: "hubraw"},
		{ID: probeIDHub2, Name: "hub-real-02", URL: f.server(t, "hub-2"), Enabled: true},
		{ID: "c-off00001", Name: "cluster-off", URL: f.server(t, "off"), Enabled: false},
	}
	th := thanos.New()
	th.Configure(thanos.Settings{Clusters: clusters})
	svc := argocd.NewSettingsService()
	svc.Configure(argocd.Settings{Enabled: true,
		Hubs:      []argocd.Hub{{ClusterID: probeIDHub1}, {ClusterID: probeIDHub2}},
		EnvList:   []string{"dev", "test", "prod"},
		Instances: []argocd.Instance{{ID: "team-payments-prod", HubClusterID: probeIDHub1, HubNamespace: probeNS, Enabled: true}},
	})
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 64), thanos: th}
	prevSvc := argocdSettingsSvc.Load()
	SetArgoCDSettings(svc)
	prevBudget, prevNow, prevGap := rolloutsV2ProbeBudget, rolloutsV2ProbeNow, rolloutsV2ProbeSecondSampleGap
	prevCov, prevSeen, prevFin := rolloutsV2ProbeSpanCoverage, rolloutsV2ProbeSeenClusters, rolloutsV2ProbeFinalize
	rolloutsV2ProbeSecondSampleGap = 0
	t.Cleanup(func() {
		argocdSettingsSvc.Store(prevSvc)
		rolloutsV2ProbeBudget, rolloutsV2ProbeNow, rolloutsV2ProbeSecondSampleGap = prevBudget, prevNow, prevGap
		rolloutsV2ProbeSpanCoverage, rolloutsV2ProbeSeenClusters, rolloutsV2ProbeFinalize = prevCov, prevSeen, prevFin
		rolloutsV2ProbeBusy.Store(false)
		rolloutsV2ProbeState.mu.Lock()
		rolloutsV2ProbeState.cur = nil
		rolloutsV2ProbeState.mu.Unlock()
	})
	rolloutsV2ProbeState.mu.Lock()
	rolloutsV2ProbeState.cur = nil
	rolloutsV2ProbeState.mu.Unlock()
	mux := http.NewServeMux()
	s.registerRolloutsV2ProbeRoutes(mux)
	hubParams := v2probe.Params{EnvList: []string{"dev", "test", "prod"}, SuffixList: []string{"ocpa", "ocpb"},
		PairGroups: map[string][]string{"pair-1": {"ocpa", "ocpb"}}, K5Window: "6h"}
	h1 := hubParams
	h1.InstanceNS, h1.WithoutMatcher = []string{probeNS}, true
	e := &probeEnv{s: s, mux: mux, fake: f, clusters: clusters, units: []probeUnitFx{
		{"cluster-a", probeIDA, "target", clusters[0], v2probe.Params{NSMatcher: `,namespace=~"team-.*"`, K5Window: "6h"}},
		{"cluster-b", probeIDB, "target", clusters[1], v2probe.Params{K5Window: "6h"}},
		{"hub-1", probeIDHub1, "hub", clusters[2], h1},
		{"hub-2", probeIDHub2, "hub", clusters[3], hubParams},
	}}
	e.seedAnswers(t)
	return e
}

// seedAnswers — her (birim, katalog satırı, varyant) için TAM etkin anahtar.
func (e *probeEnv) seedAnswers(t *testing.T) {
	t.Helper()
	for _, u := range e.units {
		for _, q := range v2probe.Catalogue {
			if !q.Runs(u.role) {
				continue
			}
			insts, err := v2probe.Expand(q, u.params)
			if err != nil {
				t.Fatalf("%s Expand: %v", q.ID, err)
			}
			for _, in := range insts {
				cfg := u.cfg
				if in.Variant == "nomatch" {
					cfg.ThanosLabelName, cfg.ThanosLabelValue = "", ""
				}
				var key string
				switch in.Kind {
				case v2probe.KindInstant:
					key = "q|" + cfg.EffectiveQuery(in.Expr)
				case v2probe.KindLabels:
					key = "labels|" + strings.Join(cfg.EffectiveMatchers([]string{in.Expr}), " ")
				case v2probe.KindLabelValues:
					key = "values|" + strings.Join(cfg.EffectiveMatchers([]string{in.Expr}), " ")
				default:
					continue
				}
				e.fake.answers[u.name+"|"+key] = probeAnswer(q, in.Variant)
			}
		}
	}
}

func (e *probeEnv) do(t *testing.T, method, path, body, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req = req.WithContext(auth.ContextWithClaims(req.Context(), &auth.Claims{UserID: "u-" + role, Email: role + "@example.test", Role: role}))
	}
	w := httptest.NewRecorder()
	e.mux.ServeHTTP(w, req)
	return w
}

func (e *probeEnv) audits() []chstore.AuditEntry {
	var out []chstore.AuditEntry
	for {
		select {
		case a := <-e.s.auditQ:
			out = append(out, a)
		default:
			return out
		}
	}
}

// start — POST, 202 bekler, koşunun bitmesini bekler.
func (e *probeEnv) start(t *testing.T, body string) map[string]any {
	t.Helper()
	w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", body, auth.RoleAdmin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("POST %d %s", w.Code, w.Body)
	}
	rolloutsV2ProbeState.mu.Lock()
	run := rolloutsV2ProbeState.cur
	rolloutsV2ProbeState.mu.Unlock()
	if run == nil {
		t.Fatal("koşu yok")
	}
	select {
	case <-run.done:
	case <-time.After(60 * time.Second):
		t.Fatal("koşu 60 s içinde bitmedi")
	}
	if len(e.fake.unknown) > 0 {
		t.Fatalf("sahte Thanos bilinmeyen ifade gördü (hazır cevap yok):\n%s", strings.Join(e.fake.unknown, "\n"))
	}
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return m
}

func (e *probeEnv) get(t *testing.T, q string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := e.do(t, "GET", "/api/admin/rollouts-v2/probe"+q, "", auth.RoleAdmin)
	var m map[string]any
	if strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(w.Body.Bytes(), &m)
	}
	return w, m
}

func probeResults(m map[string]any) []map[string]any {
	var out []map[string]any
	rs, _ := m["results"].([]any)
	for _, r := range rs {
		out = append(out, r.(map[string]any))
	}
	return out
}

// ── Kayıt + rol ──────────────────────────────────────────────────────────

func TestRolloutsV2ProbeRegisteredViaRegistry(t *testing.T) {
	src, err := os.ReadFile("rollouts_v2_probe.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(stripGoComments(string(src)), `registerRoutesExtra("rollouts-v2-probe", (*Server).registerRolloutsV2ProbeRoutes)`); n != 1 {
		t.Fatalf("init() defter kaydı %d kez (1 olmalı)", n)
	}
	if n := strings.Count(string(src), "registerRoutesExtra("); n != 1 {
		t.Fatalf("registerRoutesExtra( literal'i %d kez — TestRouteRegistryCountPin yorumları da sayar", n)
	}
	if b, _ := os.ReadFile("api.go"); strings.Contains(string(b), "rollouts-v2") || strings.Contains(string(b), "RolloutsV2Probe") {
		t.Fatal("api.go probe'u içeriyor — api.go büyümez")
	}
	mux := (&Server{}).buildMux()
	for _, p := range []struct{ m, want string }{{"POST", "POST /api/admin/rollouts-v2/probe"}, {"GET", "GET /api/admin/rollouts-v2/probe"}} {
		if _, pat := mux.Handler(httptest.NewRequest(p.m, "/api/admin/rollouts-v2/probe", nil)); pat != p.want {
			t.Errorf("%s → %q", p.m, pat)
		}
	}
}

func TestRolloutsV2ProbeAdminOnly(t *testing.T) {
	e := newProbeEnv(t)
	for _, m := range []string{"POST", "GET"} {
		for _, role := range []string{auth.RoleViewer, auth.RoleEditor} {
			if w := e.do(t, m, "/api/admin/rollouts-v2/probe", `{}`, role); w.Code != http.StatusForbidden {
				t.Errorf("%s rol %s → %d", m, role, w.Code)
			}
		}
		if w := e.do(t, m, "/api/admin/rollouts-v2/probe", `{}`, ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s kimliksiz → %d", m, w.Code)
		}
	}
	if len(e.fake.requests("")) != 0 || len(e.audits()) != 0 {
		t.Fatal("reddedilen istek yan etki bıraktı")
	}
}

// ── Guardrail'ler ────────────────────────────────────────────────────────

func TestRolloutsV2ProbeGuardrails(t *testing.T) {
	e := newProbeEnv(t)
	// Çözülmemiş TokenRef'li ETKİN küme: varsayılan plan (bütün etkin
	// kümeler) da onu seçer ve fail-closed guardrail'e takılır — prod'daki
	// doğru davranış; bu yüzden yalnız bu testte eklenir.
	unres := thanos.ClusterConfig{ID: "c-unres001", Name: "cluster-unresolved", URL: e.fake.server(t, "unres"), Enabled: true, AuthType: "bearer", TokenRef: "env:COREMETRY_TEST_UNSET_979"}
	e.s.thanos.Configure(thanos.Settings{Clusters: append(append([]thanos.ClusterConfig{}, e.clusters...), unres)})
	cases := []struct{ name, body, want string }{
		{"varsayılan plan çözülmemiş ref'i seçer", `{}`, "c-unres001"},
		{"bilinmeyen hedef", `{"targets":["c-nope0000"]}`, "c-nope0000"},
		{"devre dışı hedef", `{"targets":["c-off00001"]}`, "c-off00001"},
		{"hub = hedef", `{"targets":["` + probeIDHub1 + `"],"hubs":["` + probeIDHub1 + `"]}`, "hem hub hem hedef"},
		{"bilinmeyen paket", `{"packs":["Z"]}`, "bilinmeyen paket"},
		{"çözülmemiş tokenRef", `{"targets":["c-unres001"]}`, "tokenRef"},
		{"boş plan", `{"targets":[],"hubs":[],"packs":["K"]}`, ""},
		{"geçersiz JSON", `{`, "geçersiz JSON"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", c.body, auth.RoleAdmin)
			if c.name == "boş plan" {
				// hub'lar argocd'den gelir: plan boş değil → 202 değil; hub listesi verildiğinde boş.
				return
			}
			if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"errorType":"guardrail"`) || !strings.Contains(w.Body.String(), c.want) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
	// Boş plan: hub'sız ayar + hedef yok + T yok.
	argocdSettingsSvc.Load().Configure(argocd.Settings{})
	if w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", `{"targets":["`+probeIDA+`"],"packs":["H"]}`, auth.RoleAdmin); w.Code != http.StatusAccepted {
		t.Fatalf("hedefli H planı 202 olmalı (H atlanır): %d %s", w.Code, w.Body)
	}
	rolloutsV2ProbeState.mu.Lock()
	run := rolloutsV2ProbeState.cur
	rolloutsV2ProbeState.mu.Unlock()
	<-run.done
	if len(e.fake.requests("")) != 0 {
		t.Fatalf("guardrail testleri upstream'e istek attı: %d", len(e.fake.requests("")))
	}
	// thanos yok → 503.
	e.s.thanos = nil
	if w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", `{}`, auth.RoleAdmin); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("thanos yok → %d", w.Code)
	}
}

func TestRolloutsV2ProbeBusy(t *testing.T) {
	e := newProbeEnv(t)
	rolloutsV2ProbeBusy.Store(true)
	w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", `{}`, auth.RoleAdmin)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"errorType":"busy"`) {
		t.Fatalf("meşgul → %d %s", w.Code, w.Body)
	}
	if len(e.fake.requests("")) != 0 {
		t.Fatal("meşgulken istek atıldı")
	}
}

// ── Mutlu yol ────────────────────────────────────────────────────────────

func TestRolloutsV2ProbeHappyPath(t *testing.T) {
	e := newProbeEnv(t)
	post := e.start(t, `{}`)
	if post["planned"].(float64) < 300 || post["status"] != "running" {
		t.Fatalf("202 gövdesi: %v", post)
	}
	w, m := e.get(t, "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET %d %s", w.Code, w.Body)
	}
	if m["status"] != "done" {
		t.Fatalf("status %v (units %v)", m["status"], m["units"])
	}
	calls, planned := int(m["calls"].(float64)), int(m["planned"].(float64))
	if calls != planned {
		t.Errorf("calls %d ≠ planned %d (hiçbir şey atlanmamalıydı)", calls, planned)
	}
	if m["tokens"].(float64) == 0 {
		t.Error("tokens 0")
	}
	md := m["markdown"].(string)
	if !strings.Contains(md, "| ID | cluster-a | cluster-b | hub-1 | hub-2 |") {
		t.Error("§11.9 sütunları")
	}
	if as, _ := m["assumptions"].([]any); len(as) != 15 {
		t.Errorf("%d varsayım", len(as))
	}
	// Sızıntı: JSON gövdesi ve markdown ham ad/host/etiket değeri taşımaz.
	body := w.Body.String()
	for _, s := range append(append([]string{}, probeSentinels...), probeIDA, probeIDB, probeIDHub1, probeIDHub2) {
		if strings.Contains(body, s) {
			t.Errorf("GET gövdesi nöbetçi sızdırdı: %q", s)
		}
	}
	// Form parametreleri.
	dedupOff := map[string]int{}
	nsRepeat := map[string]int{}
	hub1NoMatch := 0
	for _, r := range e.fake.requests("") {
		if r.Path == "/api/v1/query" {
			if r.Form.Get("partial_response") != "false" {
				t.Errorf("%s: partial_response=%q", r.Unit, r.Form.Get("partial_response"))
			}
			switch r.Form.Get("dedup") {
			case "true":
			case "false":
				dedupOff[r.Unit]++
			default:
				t.Errorf("%s: dedup=%q (her anlık çağrı açıkça taşır)", r.Unit, r.Form.Get("dedup"))
			}
			if r.Form.Get("timeout") != "30s" {
				t.Errorf("%s: timeout=%q", r.Unit, r.Form.Get("timeout"))
			}
			q := r.Form.Get("query")
			if strings.Contains(q, `namespace=~"team-.*"`) {
				nsRepeat[r.Unit]++
			}
			if r.Unit == "hub-1" && !strings.Contains(q, `cluster="hubraw"`) {
				hub1NoMatch++
			}
			if r.Unit == "hub-2" && strings.Contains(q, "cluster=") {
				t.Errorf("hub-2 etiketsiz; matcher görüldü: %s", q)
			}
			continue
		}
		// label API'leri: start/end pencerede, limit 501, partial_response=false.
		st, _ := strconv.ParseFloat(r.Form.Get("start"), 64)
		en, _ := strconv.ParseFloat(r.Form.Get("end"), 64)
		if st == 0 || en == 0 || en-st < 299 || en-st > 601 {
			t.Errorf("%s %s: start/end penceresi %v..%v", r.Unit, r.Path, st, en)
		}
		if r.Form.Get("limit") != "501" || r.Form.Get("partial_response") != "false" {
			t.Errorf("%s %s: limit=%q partial=%q", r.Unit, r.Path, r.Form.Get("limit"), r.Form.Get("partial_response"))
		}
		if r.Unit == "hub-1" && !strings.Contains(strings.Join(r.Form["match[]"], " "), `cluster="hubraw"`) {
			t.Errorf("hub-1 label API match[] matcher taşımalı: %v", r.Form["match[]"])
		}
	}
	for _, u := range []string{"cluster-a", "cluster-b", "hub-1", "hub-2"} {
		if dedupOff[u] != 1 {
			t.Errorf("%s: dedup=false %d kez (K0.4b/H0.2b: 1)", u, dedupOff[u])
		}
	}
	if nsRepeat["cluster-a"] != 7 || nsRepeat["cluster-b"] != 0 {
		t.Errorf("ns tekrarı: a=%d b=%d", nsRepeat["cluster-a"], nsRepeat["cluster-b"])
	}
	if hub1NoMatch != 14 {
		t.Errorf("hub-1 matcher'sız çağrı %d (14 nomatch varyantı)", hub1NoMatch)
	}
	// Sonuçlar: nomatch varyantı yalnız hub-1'de; inst varyantı jetonlu.
	nm, inst := 0, 0
	for _, r := range probeResults(m) {
		switch v, _ := r["variant"].(string); {
		case v == "nomatch":
			nm++
			if r["unit"] != "hub-1" {
				t.Errorf("nomatch %v biriminde", r["unit"])
			}
		case strings.HasPrefix(v, "inst:"):
			inst++
			if v != "inst:<team-1>-prod" {
				t.Errorf("inst varyantı %q", v)
			}
		}
	}
	if nm != 14 || inst != 1 {
		t.Errorf("nomatch=%d inst=%d", nm, inst)
	}
	// Audit: tek satır, kimlikler var, değer/jeton yok.
	as := e.audits()
	if len(as) != 1 || as[0].Action != rolloutsV2ProbeAction || as[0].TargetKind != "settings" {
		t.Fatalf("audit: %+v", as)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(as[0].Details), &d); err != nil {
		t.Fatalf("audit details JSON değil: %s", as[0].Details)
	}
	for _, k := range []string{"runId", "targets", "hubs", "packs", "status", "calls", "planned", "durationMs", "budgetS", "units"} {
		if _, ok := d[k]; !ok {
			t.Errorf("audit details %q yok", k)
		}
	}
	for _, k := range []string{"rows", "value", "markdown", "results", "warnings"} {
		if strings.Contains(as[0].Details, `"`+k+`"`) {
			t.Errorf("audit details %q taşımamalı", k)
		}
	}
	for _, s := range probeSentinels {
		if strings.Contains(as[0].Details, s) {
			t.Errorf("audit nöbetçi sızdırdı: %s", s)
		}
	}
	if d["status"] != "done" || as[0].ActorRole != auth.RoleAdmin {
		t.Errorf("audit status/actor: %v %s", d["status"], as[0].ActorRole)
	}
	// ?format=md
	w2, _ := e.get(t, "?format=md")
	if w2.Code != 200 || !strings.HasPrefix(w2.Header().Get("Content-Type"), "text/markdown") ||
		!strings.HasPrefix(w2.Header().Get("Content-Disposition"), `attachment; filename="rollouts-v2-probe-`) ||
		!strings.HasPrefix(w2.Body.String(), "# Rollouts v2 §11 sorgu paketi") {
		t.Errorf("format=md: %d %v", w2.Code, w2.Header())
	}
	// TTL: 61 dk sonra 404 none.
	rolloutsV2ProbeNow = func() time.Time { return time.Now().Add(61 * time.Minute) }
	if w3, m3 := e.get(t, ""); w3.Code != http.StatusNotFound || m3["status"] != "none" || m3["pod"] == "" {
		t.Errorf("TTL: %d %v", w3.Code, m3)
	}
}

func TestRolloutsV2ProbeNoneAndRunning(t *testing.T) {
	e := newProbeEnv(t)
	if w, m := e.get(t, ""); w.Code != http.StatusNotFound || m["status"] != "none" {
		t.Fatalf("rapor yokken %d %v", w.Code, m)
	}
	// Koşarken 202 + progress (busy başka bir koşuyu reddeder).
	rolloutsV2ProbeState.mu.Lock()
	rolloutsV2ProbeState.cur = &rolloutsV2ProbeRun{ID: "abc", StartedAt: time.Now(), plan: &rolloutsV2ProbePlan{Budget: time.Minute, Planned: 10}, done: make(chan struct{}), status: "running", calls: 3, unitToken: "cluster-a", pack: "K"}
	rolloutsV2ProbeState.mu.Unlock()
	w, m := e.get(t, "")
	if w.Code != http.StatusAccepted || m["status"] != "running" {
		t.Fatalf("koşarken %d %v", w.Code, m)
	}
	if p := m["progress"].(map[string]any); p["calls"] != float64(3) || p["unit"] != "cluster-a" || p["pack"] != "K" || p["planned"] != float64(10) {
		t.Errorf("progress: %v", p)
	}
}

// ── Erken durma ──────────────────────────────────────────────────────────

func TestRolloutsV2ProbeEarlyStop(t *testing.T) {
	e := newProbeEnv(t)
	e.fake.fail = func(unit string, req probeFakeReq) (int, string) {
		if unit == "cluster-b" {
			return http.StatusForbidden, `{"status":"error","errorType":"bad_data","error":"forbidden"}`
		}
		return 0, ""
	}
	e.start(t, `{"packs":["K","D","R"]}`)
	if n := len(e.fake.requests("cluster-b")); n != 2 {
		t.Fatalf("cluster-b'ye %d istek (2 ardışık unauthorized'da durmalı)", n)
	}
	_, m := e.get(t, "")
	if m["status"] != "done" {
		t.Fatalf("status %v", m["status"])
	}
	for _, u := range m["units"].([]any) {
		um := u.(map[string]any)
		switch um["token"] {
		case "cluster-a":
			if um["state"] != "ok" || um["calls"].(float64) < 60 {
				t.Errorf("cluster-a tamamlanmalı: %v", um)
			}
		case "cluster-b":
			if um["state"] != "unauthorized" || um["calls"] != float64(2) || !strings.Contains(um["earlyStop"].(string), "unauthorized streak after K0.2") {
				t.Errorf("cluster-b erken durma: %v", um)
			}
		}
	}
	unauth, skipped := 0, 0
	for _, r := range probeResults(m) {
		if r["unit"] != "cluster-b" {
			continue
		}
		switch {
		case r["state"] == "unauthorized":
			unauth++
		case r["skipped"] == true && strings.Contains(r["detail"].(string), "unit unauthorized (streak)"):
			skipped++
		default:
			t.Errorf("cluster-b beklenmeyen sonuç: %v", r)
		}
	}
	if unauth != 2 || skipped < 60 {
		t.Errorf("cluster-b unauthorized=%d skipped=%d", unauth, skipped)
	}
}

// ── Bütçe ────────────────────────────────────────────────────────────────

// v0.10.982 — tam paket yükü altında 1 ms bütçe zamana bağlıydı (zamanlayıcı
// gecikince 60'tan fazla çağrı koşabiliyordu; bir kez düştü). Bütçe 0:
// context.WithTimeout(0) bağlamı OLUŞURKEN iptal eder → ilk bütçe kapısından
// itibaren her sorgu deterministik olarak "run budget exhausted".
func TestRolloutsV2ProbeBudgetExhausted(t *testing.T) {
	e := newProbeEnv(t)
	rolloutsV2ProbeBudget = 0
	e.start(t, `{}`)
	_, m := e.get(t, "")
	if m["status"] != "budget_exhausted" {
		t.Fatalf("status %v", m["status"])
	}
	rs := probeResults(m)
	skipped := 0
	for _, r := range rs {
		if r["skipped"] == true && strings.Contains(r["detail"].(string), "run budget exhausted") {
			skipped++
		}
	}
	// Bütçe koşu başlamadan tükenmiş: T dahil hiçbir çağrı koşmaz.
	if skipped < 200 || skipped != len(rs) {
		t.Errorf("%d/%d atlanan sonuç", skipped, len(rs))
	}
	e.fake.mu.Lock()
	n := len(e.fake.reqs)
	e.fake.mu.Unlock()
	if n != 0 {
		t.Errorf("tükenmiş bütçede sahte Thanos'a %d çağrı gitti", n)
	}
	as := e.audits()
	if len(as) != 1 || !strings.Contains(as[0].Details, `"status":"budget_exhausted"`) || !strings.Contains(as[0].Details, `"errorType":"budget_exhausted"`) {
		t.Fatalf("audit: %+v", as)
	}
	if !strings.Contains(m["markdown"].(string), "run budget exhausted") {
		t.Error("markdown bütçe notu")
	}
}

// ── Panik → failed (v0.10.979) ──────────────────────────────────────────

// TestRolloutsV2ProbePanicInLoop — döngü İÇİ panik (T dikişi): kısmi sonuçlar
// yine Finalize edilir, durum failed, tek audit satırı, busy bırakılır.
func TestRolloutsV2ProbePanicInLoop(t *testing.T) {
	e := newProbeEnv(t)
	rolloutsV2ProbeSpanCoverage = func(*Server, context.Context, time.Time, time.Time) (*chstore.RolloutProbeSpans, error) {
		panic("chstore fixture panic")
	}
	e.start(t, `{"packs":["T"]}`)
	w, m := e.get(t, "")
	if w.Code != http.StatusOK || m["status"] != "failed" {
		t.Fatalf("GET %d %v", w.Code, m["status"])
	}
	as := e.audits()
	if len(as) != 1 || !strings.Contains(as[0].Details, `"errorType":"failed"`) {
		t.Fatalf("audit: %+v", as)
	}
	if rolloutsV2ProbeBusy.Load() {
		t.Error("busy bırakılmadı")
	}
}

// TestRolloutsV2ProbePanicInFinalize — döngü DIŞI panik (Finalize/Render):
// süreç düşmez; asgari rapor status failed, audit satırı errorType failed ve
// değer/satır taşımaz, sonraki POST 409 değil 202.
func TestRolloutsV2ProbePanicInFinalize(t *testing.T) {
	e := newProbeEnv(t)
	rolloutsV2ProbeFinalize = func(v2probe.RawRun, v2probe.Seeds) v2probe.Report { panic("render fixture panic") }
	e.start(t, `{"packs":["T"]}`)
	w, m := e.get(t, "")
	if w.Code != http.StatusOK || m["status"] != "failed" || m["runId"] == "" {
		t.Fatalf("GET %d %v", w.Code, m)
	}
	if md, _ := m["markdown"].(string); !strings.Contains(md, "status: failed") {
		t.Errorf("asgari markdown: %q", md)
	}
	as := e.audits()
	if len(as) != 1 || !strings.Contains(as[0].Details, `"errorType":"failed"`) || !strings.Contains(as[0].Details, `"status":"failed"`) {
		t.Fatalf("audit: %+v", as)
	}
	for _, k := range []string{"rows", "value", "markdown", "results"} {
		if strings.Contains(as[0].Details, `"`+k+`"`) {
			t.Errorf("audit details %q taşımamalı", k)
		}
	}
	// Busy bırakıldı: yeni koşu 202 (Finalize dikişi geri alınmış hâlde).
	rolloutsV2ProbeFinalize = v2probe.Finalize
	if w := e.do(t, "POST", "/api/admin/rollouts-v2/probe", `{"packs":["T"]}`, auth.RoleAdmin); w.Code != http.StatusAccepted {
		t.Fatalf("panik sonrası POST %d %s", w.Code, w.Body)
	}
	rolloutsV2ProbeState.mu.Lock()
	run := rolloutsV2ProbeState.cur
	rolloutsV2ProbeState.mu.Unlock()
	<-run.done
	if _, m2 := e.get(t, ""); m2["status"] != "done" {
		t.Errorf("ikinci koşu: %v", m2["status"])
	}
}

// ── Detay kırpma (v0.10.979) ────────────────────────────────────────────

// TestRolloutsV2ProbeDetailClip — 20 KiB'lik upstream hata metni rapora
// DetailMax'a kırpılmış girer; execution erken durdurmaz (partial), 403'te
// earlyStop da kırpılır.
func TestRolloutsV2ProbeDetailClip(t *testing.T) {
	e := newProbeEnv(t)
	big := strings.Repeat("e", 20<<10)
	e.fake.fail = func(unit string, req probeFakeReq) (int, string) {
		if unit == "cluster-b" {
			return http.StatusUnprocessableEntity, `{"status":"error","errorType":"execution","error":"` + big + `"}`
		}
		return 0, ""
	}
	e.start(t, `{"packs":["K"]}`)
	_, m := e.get(t, "")
	if m["status"] != "done" {
		t.Fatalf("status %v", m["status"])
	}
	n := 0
	for _, r := range probeResults(m) {
		if r["unit"] != "cluster-b" {
			continue
		}
		n++
		d, _ := r["detail"].(string)
		if len(d) > v2probe.DetailMax+len("…") || !strings.HasPrefix(d, "execution:") {
			t.Fatalf("%v detay kırpılmamış: len=%d", r["id"], len(d))
		}
	}
	if n < 50 {
		t.Errorf("cluster-b %d sonuç (execution erken durdurmamalı)", n)
	}
	for _, u := range m["units"].([]any) {
		if um := u.(map[string]any); um["token"] == "cluster-b" && um["state"] != "partial" {
			t.Errorf("cluster-b durumu: %v", um)
		}
	}
	if len(m["markdown"].(string)) > 1<<20 {
		t.Errorf("markdown %d bayt", len(m["markdown"].(string)))
	}
	// 403 + 20 KiB gövde: earlyStop kırpılır.
	e.fake.fail = func(unit string, req probeFakeReq) (int, string) {
		if unit == "cluster-b" {
			return http.StatusForbidden, `{"status":"error","errorType":"bad_data","error":"` + big + `"}`
		}
		return 0, ""
	}
	e.start(t, `{"packs":["K"]}`)
	_, m = e.get(t, "")
	for _, u := range m["units"].([]any) {
		um := u.(map[string]any)
		if um["token"] != "cluster-b" {
			continue
		}
		es, _ := um["earlyStop"].(string)
		if len(es) > v2probe.DetailMax+len("…") || !strings.HasPrefix(es, "unauthorized streak after K0.2") {
			t.Errorf("earlyStop kırpılmamış: len=%d", len(es))
		}
	}
}

// ── T dikişi ─────────────────────────────────────────────────────────────

func TestRolloutsV2ProbeTPack(t *testing.T) {
	e := newProbeEnv(t)
	// store yok → not_configured.
	e.start(t, `{"packs":["T"]}`)
	_, m := e.get(t, "")
	for _, r := range probeResults(m) {
		if r["unit"] != "clickhouse" || r["state"] != "not_configured" {
			t.Errorf("store yokken T sonucu: %v", r)
		}
	}
	if m["calls"] != float64(3) || m["planned"] != float64(3) {
		t.Errorf("T calls/planned: %v/%v", m["calls"], m["planned"])
	}
	// Dikiş: fallback + eşleşmemiş değer.
	rolloutsV2ProbeSpanCoverage = func(*Server, context.Context, time.Time, time.Time) (*chstore.RolloutProbeSpans, error) {
		return &chstore.RolloutProbeSpans{FallbackUsed: true, WindowSec: 900, SampleRows: 200000,
			Coverage: []chstore.RolloutProbeClusterRow{{Cluster: "realcluster-prod-01", Sampled: 120000, Depl: 119500, K8sCluster: 120000}},
			Env:      []chstore.RolloutProbeEnvRow{{DeployEnv: "prod-realcluster-prod-01", Cluster: "realcluster-prod-01", N: 120000}}}, nil
	}
	rolloutsV2ProbeSeenClusters = func(*Server, context.Context, time.Time) (chstore.SeenClusterValues, error) {
		return chstore.SeenClusterValues{Source: "entity_seen_5m", Rows: []chstore.SeenClusterValue{{Value: "realcluster-prod-01", Spans: 9}, {Value: "legacy-cluster", Spans: 3}}}, nil
	}
	e.start(t, `{"packs":["T"]}`)
	w, m := e.get(t, "")
	md := m["markdown"].(string)
	if !strings.Contains(md, "T0 fallback: `cluster` column missing — 0011 not applied") {
		t.Error("fallback notu yok")
	}
	if m["calls"] != float64(4) {
		t.Errorf("fallback çağrısı sayılmalı: %v", m["calls"])
	}
	seen := map[string]bool{}
	for _, r := range probeResults(m) {
		id, _ := r["id"].(string)
		variant, _ := r["variant"].(string)
		seen[id+variant] = true
		if id == "T1" && variant == "" {
			rows := r["rows"].([]any)
			if len(rows) != 1 || rows[0].(map[string]any)["labels"].(map[string]any)["cluster"] != "cluster-a" {
				t.Errorf("T1 satırı jetonlu olmalı: %v", rows)
			}
		}
		if id == "T2" {
			rows := r["rows"].([]any)
			if rows[0].(map[string]any)["labels"].(map[string]any)["deploy_env"] != "prod-cluster-a" {
				t.Errorf("T2 deploy_env biçimi: %v", rows)
			}
		}
		if id == "T3" {
			if !strings.Contains(r["detail"].(string), "unmapped=1 total=2") || len(r["rows"].([]any)) != 1 {
				t.Errorf("T3: %v", r)
			}
			if r["rows"].([]any)[0].(map[string]any)["labels"].(map[string]any)["span_cluster"] == "legacy-cluster" {
				t.Error("T3 eşleşmemiş değer jetonlanmalı")
			}
		}
	}
	if !seen["T1fallback"] || !seen["T3"] || !seen["T2"] {
		t.Errorf("T sonuçları: %v", seen)
	}
	for _, s := range []string{"realcluster-prod-01", "legacy-cluster"} {
		if strings.Contains(w.Body.String(), s) {
			t.Errorf("T sızıntı: %s", s)
		}
	}
}

// ── H5.1 bad_data → H5.2 fallback ───────────────────────────────────────

func TestRolloutsV2ProbeLabelsFallback(t *testing.T) {
	e := newProbeEnv(t)
	e.fake.fail = func(unit string, req probeFakeReq) (int, string) {
		if unit == "hub-2" && req.Path == "/api/v1/labels" && strings.Join(req.Form["match[]"], "") == "argocd_app_info" {
			return http.StatusBadRequest, `{"status":"error","errorType":"bad_data","error":"match[] is not supported"}`
		}
		return 0, ""
	}
	e.start(t, `{"packs":["H"]}`)
	_, m := e.get(t, "")
	var h51a, h52a map[string]any
	for _, r := range probeResults(m) {
		if r["unit"] != "hub-2" {
			if r["id"] == "H5.2a" {
				t.Errorf("H5.2a yalnız hub-2'de koşmalı: %v", r)
			}
			continue
		}
		switch r["id"] {
		case "H5.1a":
			h51a = r
		case "H5.2a":
			h52a = r
		}
	}
	if h51a == nil || h51a["state"] != "error" || !strings.HasPrefix(h51a["detail"].(string), "bad_data") {
		t.Fatalf("H5.1a: %v", h51a)
	}
	if h52a == nil || h52a["state"] != "ok" || h52a["rows"] != nil {
		t.Fatalf("H5.2a: %v", h52a)
	}
	names := h52a["names"].([]any)
	if len(names) != 4 || names[0] != "dest_server" || names[1] != "name" {
		t.Errorf("H5.2a yalnız etiket ADLARI: %v", names)
	}
	n := 0
	for _, r := range e.fake.requests("hub-2") {
		if r.Form.Get("query") == "topk(1, argocd_app_info)" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("hub-2'de H5.2a %d kez koştu", n)
	}
	if strings.Contains(m["markdown"].(string), "checkout-real-api") {
		t.Error("fallback değeri sızdı")
	}
}
