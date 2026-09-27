package api

// v0.10.981 — GitOps sekmesi: MV referansları → tekil iş yükleri.
// v0.10.985 — Argo bölümünün kaynağı (eşleyici tablosu | canlı Thanos).

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func TestServiceGitOpsWorkloads(t *testing.T) {
	refs := []chstore.WorkloadRevisionRef{
		{Cluster: "prod-a", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "prod-a", Namespace: "pay", Workload: "checkout", Revision: "r2"}, // aynı iş yükü, başka revizyon
		{Cluster: "prod-a-alias", Namespace: "pay", Workload: "checkout", Revision: "r2"},
		{Cluster: "prod-b", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "ghost", Namespace: "pay", Workload: "checkout", Revision: "r1"},
		{Cluster: "prod-a", Namespace: "pay", Workload: "", Revision: "r1"},
	}
	bySpan := map[string]string{"prod-a": "c-a", "prod-a-alias": "c-a", "prod-b": "c-b"}
	got, unm := serviceGitOpsWorkloads(refs, bySpan)
	want := []argocd.ServiceWorkload{
		{ClusterID: "c-a", Namespace: "pay", Workload: "checkout"},
		{ClusterID: "c-b", Namespace: "pay", Workload: "checkout"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("iş yükleri %+v", got)
	}
	if !reflect.DeepEqual(unm, []string{"ghost"}) {
		t.Fatalf("eşlenemeyen %v", unm)
	}

	// küme tanımsız (tek küme): değer olduğu gibi geçer, eşlenemeyen yok
	got, unm = serviceGitOpsWorkloads(refs[:1], nil)
	if len(got) != 1 || got[0].ClusterID != "prod-a" || len(unm) != 0 {
		t.Fatalf("tek küme: %+v %v", got, unm)
	}
}

// ── v0.10.985 — Rollouts v2 P3.2: Argo bölümünün kaynağı ─────────────────
//
// Sözleşme (service_gitops_mapper.go):
//   - metricsWorker kapalı (varsayılan): eşleme tablosu HİÇ okunmaz, hub
//     Thanos'u canlı sorgulanır (v0.10.981), source "live";
//   - açık + taze kenar: Thanos'a gidilmez, uygulama kenar + son durum + 24
//     sa 'sync' satırlarından, source "mapper";
//   - açık ama kenar yok / okuma düştü: canlı yol;
//   - açık ama kapsam kapısı kapalı (işçi durmuş, son koşu 'ok' değil, ayar
//     sonrası koşmamış, devre dışı instance, instance'sız hub): canlı yol +
//     not (v0.10.985 inceleme);
//   - önbellek anahtarı kaynak kararının girdisini (mw) taşır: aynı blob
//     sürümünde bayrak değişince bayat kaynak servis edilmez.

type fakeGitOpsStore struct {
	mu        sync.Mutex
	refs      []chstore.WorkloadRevisionRef
	edges     []argocd.MappingRow
	edgeErr   error
	wlCapped  bool
	statuses  map[argocd.AppKey]argocd.StatusRow
	syncs     map[argocd.AppKey]map[string]int
	syncErr   error
	calls     []string
	since     time.Time
	fold      time.Duration
	run       *rollout.WorkerRun
	runErr    error
	runWorker string
}

func (f *fakeGitOpsStore) hit(n string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, n)
}

func (f *fakeGitOpsStore) ServiceWorkloads(_ context.Context, _ string, _, _ time.Time) ([]chstore.WorkloadRevisionRef, bool, error) {
	f.hit("workloads")
	return f.refs, false, nil
}

func (f *fakeGitOpsStore) ArgoCDServiceEdgesFor(_ context.Context, wl []argocd.ServiceWorkload, pairs [][2]string, since time.Time) (chstore.ArgoCDServiceEdges, error) {
	f.hit("edges")
	f.since = since
	if len(pairs) != 1 || pairs[0] != [2]string{"c-a", "pay"} || len(wl) != 1 || wl[0].Workload != "checkout" {
		return chstore.ArgoCDServiceEdges{}, errors.New("beklenmeyen çiftler/iş yükleri")
	}
	return chstore.ArgoCDServiceEdges{Edges: f.edges, WorkloadCapped: f.wlCapped}, f.edgeErr
}

func (f *fakeGitOpsStore) RolloutWorkerLastRun(_ context.Context, worker string) (*rollout.WorkerRun, error) {
	f.hit("run")
	f.runWorker = worker
	return f.run, f.runErr
}

func (f *fakeGitOpsStore) ArgoCDLatestStatusFor(_ context.Context, keys []argocd.AppKey) (map[argocd.AppKey]argocd.StatusRow, error) {
	f.hit("status")
	return f.statuses, nil
}

func (f *fakeGitOpsStore) ArgoCDSyncCounts(_ context.Context, _ []argocd.AppKey, _ time.Time, fold time.Duration) (map[argocd.AppKey]map[string]int, error) {
	f.hit("syncs")
	f.fold = fold
	return f.syncs, f.syncErr
}

func (f *fakeGitOpsStore) touched(n string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == n {
			return true
		}
	}
	return false
}

var gitopsT0 = time.Date(2026, 9, 27, 12, 0, 30, 0, time.UTC)

type gitopsEnv struct {
	mux   *http.ServeMux
	store *fakeGitOpsStore
	fake  *argoFakeThanos
	svc   *argocd.SettingsService
}

func gitopsSettings(worker bool) argocd.Settings {
	set := argocd.DefaultSettings()
	set.Enabled, set.MetricsWorker.Enabled = true, worker
	set.Hubs = []argocd.Hub{{ClusterID: "hub"}}
	set.Instances = []argocd.Instance{{ID: "i1", HubClusterID: "hub", HubNamespace: "t-prod", Name: "Prod Argo", Enabled: true}}
	return set
}

func newGitOpsEnv(t *testing.T, worker bool, fs *fakeGitOpsStore) *gitopsEnv {
	t.Helper()
	f := newArgoFakeThanos(t)
	f.queries = []argoFakeQueryRule{
		{contains: argocd.SyncTotalMetric, result: `[]`},
		{contains: argocd.AppInfoMetric, result: `[{"metric":{"job":"t-prod-metrics","namespace":"t-prod","exported_namespace":"apps","name":"p-pay-checkout-prod-live","sync_status":"OutOfSync","health_status":"Healthy","dest_server":"https://api.a:6443","dest_namespace":"pay"},"value":[1,"1"]}]`},
	}
	th := thanos.New()
	th.Configure(thanos.Settings{Clusters: []thanos.ClusterConfig{
		{ID: "hub", Name: "hub-a", URL: f.URL, Enabled: true},
		{ID: "c-a", Name: "cluster-a", URL: f.URL, SpanClusterValue: "span-a", APIServerURLs: []string{"https://api.a:6443"}, Enabled: true},
	}})
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(64), stats: newCacheStats(), thanos: th}
	svc := argocd.NewSettingsService()
	svc.Configure(gitopsSettings(worker))
	prevSvc, prevStore, prevNow := argocdSettingsSvc.Load(), serviceGitOpsStoreOf, serviceGitOpsNow
	SetArgoCDSettings(svc)
	serviceGitOpsStoreOf = func(*Server) serviceGitOpsStore { return fs }
	serviceGitOpsNow = func() time.Time { return gitopsT0 }
	t.Cleanup(func() {
		argocdSettingsSvc.Store(prevSvc)
		serviceGitOpsStoreOf, serviceGitOpsNow = prevStore, prevNow
		f.Close()
	})
	if fs.refs == nil {
		fs.refs = []chstore.WorkloadRevisionRef{{Cluster: "span-a", Namespace: "pay", Workload: "checkout"}}
	}
	mux := http.NewServeMux()
	s.registerServiceGitOpsRoutes(mux)
	return &gitopsEnv{mux: mux, store: fs, fake: f, svc: svc}
}

func (e *gitopsEnv) thanosQueries() int {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	n := 0
	for _, r := range e.fake.reqs {
		if r.Path == "/api/v1/query" {
			n++
		}
	}
	return n
}

func (e *gitopsEnv) get(t *testing.T) serviceGitOpsResponse {
	t.Helper()
	w := rv2Get(e.mux, "/api/services/checkout/gitops")
	if w.Code != http.StatusOK {
		t.Fatalf("gitops %d %s", w.Code, w.Body)
	}
	var out serviceGitOpsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("gövde: %v\n%s", err, w.Body)
	}
	return out
}

func gitopsEdges() []argocd.MappingRow {
	base := argocd.MappingRow{ClusterID: "c-a", Namespace: "pay", InstanceID: "i1", AppNamespace: "apps",
		FirstMatchedAt: gitopsT0.Add(-time.Hour), LastVerifiedAt: gitopsT0.Add(-time.Minute), Candidates: 2, Version: 1}
	name := base
	name.WorkloadKind, name.Workload, name.AppName = "Deployment", "checkout", "p-pay-checkout-prod-ca"
	name.MatchMethod, name.MatchClass, name.Confidence, name.Candidates = argocd.MatchName, argocd.ClassEstimated, 70, 1
	weakA := base
	weakA.AppName, weakA.MatchMethod, weakA.MatchClass, weakA.Confidence = "p-pay-checkout-prod-ca", argocd.MatchNamespace, argocd.ClassWeak, 30
	weakB := weakA
	weakB.AppName = "p-pay-billing-prod-ca"
	return []argocd.MappingRow{name, weakA, weakB}
}

// gitopsHealthyRun — kapsam kapısını açan son argocd-metrics koşusu.
func gitopsHealthyRun() *rollout.WorkerRun {
	return &rollout.WorkerRun{Worker: rollout.WorkerArgoCDMetrics, StartedAt: gitopsT0.Add(-time.Minute), Status: rollout.RunOK, ScopesTotal: 1, ScopesOK: 1}
}

func gitopsMapperStore() *fakeGitOpsStore {
	k := argocd.AppKey{InstanceID: "i1", AppNamespace: "apps", Name: "p-pay-checkout-prod-ca"}
	return &fakeGitOpsStore{
		run:   gitopsHealthyRun(),
		edges: gitopsEdges(),
		statuses: map[argocd.AppKey]argocd.StatusRow{k: {InstanceID: "i1", AppNamespace: "apps", AppName: k.Name, ChangedAt: gitopsT0,
			ChangeKind: argocd.ChangeState, SyncStatus: "Synced", HealthStatus: "Progressing", AutoSync: "false",
			DestServer: "https://api.a:6443", DestNamespace: "pay", Repo: "https://git.example/pay.git", Version: 1}},
		syncs: map[argocd.AppKey]map[string]int{k: {"Succeeded": 2}},
	}
}

func TestServiceGitOpsLivePathWhenWorkerOff(t *testing.T) {
	fs := gitopsMapperStore()
	e := newGitOpsEnv(t, false, fs)
	w := rv2Get(e.mux, "/api/services/checkout/gitops")
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if fs.touched("edges") || fs.touched("status") || fs.touched("syncs") || fs.touched("run") {
		t.Fatalf("bayrak kapalıyken eşleme tablosu okunmamalı: %v", fs.calls)
	}
	if e.thanosQueries() == 0 {
		t.Fatal("canlı yol hub Thanos'unu sorgulamalı")
	}
	var raw struct {
		Argo map[string]json.RawMessage `json:"argo"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &raw)
	keys := make([]string, 0, len(raw.Argo))
	for k := range raw.Argo {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	// v0.10.981 anahtarları + yalnız eklemeli "source".
	if strings.Join(keys, ",") != "apps,configured,hubs,otherInNamespace,source" || string(raw.Argo["source"]) != `"live"` {
		t.Fatalf("argo anahtarları: %v source=%s", keys, raw.Argo["source"])
	}
	out := e.get(t)
	if len(out.Argo.Apps) != 1 || out.Argo.Apps[0].Name != "p-pay-checkout-prod-live" || out.Argo.Apps[0].Match != argocd.MatchName ||
		len(out.Argo.Hubs) != 1 || out.Argo.Hubs[0].Status != "ok" {
		t.Fatalf("canlı uygulama: %+v", out.Argo)
	}
}

func TestServiceGitOpsMapperPath(t *testing.T) {
	fs := gitopsMapperStore()
	e := newGitOpsEnv(t, true, fs)
	out := e.get(t)
	if e.thanosQueries() != 0 {
		t.Fatalf("eşleyici yolunda hub Thanos'u sorgulanmamalı (%d sorgu)", e.thanosQueries())
	}
	if out.Argo.Source != serviceGitOpsSourceMapper || !out.Argo.Configured {
		t.Fatalf("kaynak: %+v", out.Argo)
	}
	if want := gitopsT0.Truncate(time.Minute).Add(-argocd.MappingFreshness); !fs.since.Equal(want) {
		t.Fatalf("tazelik sınırı %v, beklenen %v", fs.since, want)
	}
	if fs.runWorker != rollout.WorkerArgoCDMetrics || fs.fold != 60*time.Second || out.Argo.Note != "" {
		t.Fatalf("koşu kaydı işçisi %q, senkron katlama aralığı %v (metricsS), not %q", fs.runWorker, fs.fold, out.Argo.Note)
	}
	if len(out.Argo.Apps) != 1 {
		t.Fatalf("1 uygulama: %+v", out.Argo.Apps)
	}
	a := out.Argo.Apps[0]
	if a.Name != "p-pay-checkout-prod-ca" || a.Match != argocd.MatchName || a.Confidence != 70 || a.SyncStatus != "Synced" ||
		a.HealthStatus != "Progressing" || a.AutoSync == nil || *a.AutoSync || a.DestCluster != "c-a" || a.InstanceName != "Prod Argo" ||
		a.HubClusterID != "hub" || a.Syncs24h["Succeeded"] != 2 || len(a.Workloads) != 1 || a.Workloads[0].Workload != "checkout" {
		t.Fatalf("uygulama: %+v", a)
	}
	if out.Argo.OtherInNamespace != 1 {
		t.Fatalf("aynı namespace'te eşleşmeyen (billing): %d", out.Argo.OtherInNamespace)
	}
	if len(out.Argo.Hubs) != 1 || out.Argo.Hubs[0].Status != "ok" || out.Argo.Hubs[0].Apps != 1 || out.Argo.Hubs[0].HubName != "hub-a" {
		t.Fatalf("hub satırı: %+v", out.Argo.Hubs)
	}
}

func TestServiceGitOpsMapperSyncReadFailure(t *testing.T) {
	fs := gitopsMapperStore()
	fs.syncErr = errors.New("ch down")
	e := newGitOpsEnv(t, true, fs)
	out := e.get(t)
	if out.Argo.Source != serviceGitOpsSourceMapper || len(out.Argo.Apps) != 1 || out.Argo.Apps[0].Syncs24h != nil {
		t.Fatalf("senkron okunamazsa sütun null, kart durur: %+v", out.Argo)
	}
}

func TestServiceGitOpsMapperFallsBackToLive(t *testing.T) {
	for name, mut := range map[string]func(*fakeGitOpsStore){
		"kenar yok":           func(f *fakeGitOpsStore) { f.edges = nil },
		"kenar okuması düştü": func(f *fakeGitOpsStore) { f.edgeErr = errors.New("ch down") },
	} {
		fs := gitopsMapperStore()
		mut(fs)
		e := newGitOpsEnv(t, true, fs)
		out := e.get(t)
		if out.Argo.Source != serviceGitOpsSourceLive || e.thanosQueries() == 0 || len(out.Argo.Apps) != 1 || out.Argo.Apps[0].Name != "p-pay-checkout-prod-live" {
			t.Errorf("%s: canlı yola düşmeli: %+v", name, out.Argo)
		}
	}
}

// v0.10.985 inceleme — kapsam kapısı: kenar tazeliği işçinin canlı olduğunu
// söylemez. İşçi durmuş / son koşu partial (hub Thanos'u düştü, parça
// atlandı, instance eşleyiciye hazır değil) / ayar sonrası henüz koşmamış /
// devre dışı ya da instance'sız hub varken TAZE kenarlar olsa bile tablo
// kullanılmaz: canlı yol (hub satırı sorgu sonucu) + notta sebep.
func TestServiceGitOpsMapperCoverageGate(t *testing.T) {
	for name, tc := range map[string]struct {
		store func(*fakeGitOpsStore)
		set   func(*argocd.Settings)
		want  string
	}{
		"koşu kaydı yok":        {store: func(f *fakeGitOpsStore) { f.run = nil }, want: "son 24 saatte koşmadı"},
		"koşu kaydı okunamadı":  {store: func(f *fakeGitOpsStore) { f.runErr = errors.New("ch down") }, want: "koşu kaydı okunamadı"},
		"işçi durmuş":           {store: func(f *fakeGitOpsStore) { f.run.StartedAt = gitopsT0.Add(-8 * time.Minute) }, want: "işçi durmuş olabilir"},
		"son koşu partial":      {store: func(f *fakeGitOpsStore) { f.run.Status = rollout.RunPartial }, want: `"partial"`},
		"son koşu failed":       {store: func(f *fakeGitOpsStore) { f.run.Status = rollout.RunFailed }, want: `"failed"`},
		"kapsam ayardan farklı": {store: func(f *fakeGitOpsStore) { f.run.ScopesTotal = 2 }, want: "kapsamı 2 instance"},
		"ayar sonrası koşmadı": {
			set:  func(s *argocd.Settings) { s.UpdatedAt = gitopsT0.UnixNano() },
			want: "henüz koşmadı",
		},
		"devre dışı instance": {
			set: func(s *argocd.Settings) {
				s.Instances = append(s.Instances, argocd.Instance{ID: "i2", HubClusterID: "hub", HubNamespace: "t-b", Enabled: false})
			},
			want: "instance i2 devre dışı",
		},
		"instance'sız hub": {
			set:  func(s *argocd.Settings) { s.Hubs = append(s.Hubs, argocd.Hub{ClusterID: "c-a"}) },
			want: "hub c-a'in etkin instance'ı yok",
		},
	} {
		t.Run(name, func(t *testing.T) {
			fs := gitopsMapperStore()
			if tc.store != nil {
				tc.store(fs)
			}
			e := newGitOpsEnv(t, true, fs)
			if tc.set != nil {
				set := gitopsSettings(true)
				tc.set(&set)
				e.svc.Configure(set)
			}
			out := e.get(t)
			if out.Argo.Source != serviceGitOpsSourceLive || e.thanosQueries() == 0 || fs.touched("edges") {
				t.Fatalf("canlı yola düşmeli, kenar okunmamalı: %+v %v", out.Argo, fs.calls)
			}
			if !strings.Contains(out.Argo.Note, "eşleyici tablosu kullanılmadı") || !strings.Contains(out.Argo.Note, tc.want) {
				t.Fatalf("not %q, beklenen %q", out.Argo.Note, tc.want)
			}
		})
	}
	// Kapı açık + kesik iş yükü kenarı: not "liste eksik".
	fs := gitopsMapperStore()
	fs.wlCapped = true
	e := newGitOpsEnv(t, true, fs)
	if out := e.get(t); out.Argo.Source != serviceGitOpsSourceMapper || !strings.Contains(out.Argo.Note, "iş yükü eşleme kenarları") {
		t.Fatalf("kesik iş yükü kenarı notu: %+v", out.Argo)
	}
}

func TestServiceGitOpsCacheKeyCarriesSourceDecision(t *testing.T) {
	fs := gitopsMapperStore()
	e := newGitOpsEnv(t, false, fs)
	if out := e.get(t); out.Argo.Source != serviceGitOpsSourceLive {
		t.Fatalf("kapalı: %s", out.Argo.Source)
	}
	// Aynı blob sürümü (UpdatedAt değişmedi), yalnız bayrak: anahtar farklı olmalı.
	e.svc.Configure(gitopsSettings(true))
	if out := e.get(t); out.Argo.Source != serviceGitOpsSourceMapper {
		t.Fatalf("bayrak açılınca önbellek bayat kaynağı servis etmemeli: %s", out.Argo.Source)
	}
}

func TestServiceGitOpsPairsAndEdgeApps(t *testing.T) {
	wl := []argocd.ServiceWorkload{{ClusterID: "c-b", Namespace: "pay", Workload: "a"}, {ClusterID: "c-a", Namespace: "pay", Workload: "b"}, {ClusterID: "c-a", Namespace: "pay", Workload: "c"}}
	if got := serviceGitOpsPairs(wl); !reflect.DeepEqual(got, [][2]string{{"c-a", "pay"}, {"c-b", "pay"}}) {
		t.Fatalf("çiftler: %v", got)
	}
	edges := gitopsEdges()
	edges[0].ClusterID = "c-a"
	edges[0].Workload = "b"
	gone := edges[0]
	gone.AppName, gone.RemovedAt = "gone", gitopsT0
	got := serviceGitOpsEdgeApps(append(edges, gone), wl)
	if len(got) != 1 || got[0].Name != "p-pay-checkout-prod-ca" {
		t.Fatalf("yalnız iş yükü kenarlı canlı uygulamalar: %v", got)
	}
}
