package argocd

// metrics_worker_test.go — v0.10.983 — argocd-metrics işçisi (P3.1) sahte
// hub + sahte CH ile. Sözleşme:
//   - bayrak kapalı ya da hub yok → Tick hiçbir G/Ç yapmaz;
//   - ilk koşu: tam envanter + senkron penceresi + sayaç tabanı, taban satırı,
//     TEK rollout_worker_runs satırı (worker=argocd-metrics);
//   - değişim: sabit olmayan okumada state; sabite dönen uygulama hedefli
//     okumayla; senkron artışı (ve taban varken yeni doğan seri) tek 'sync';
//   - uyarılı/kesik okuma parçanın farkını atlar, bellek aynen;
//   - iki hub: hub başına injectClusterLabel, tokenRef'i çözülmeyen hub atlanır;
//   - lider edinimi durumu CH'den yeniden kurar (sahte taban/appeared yok);
//   - liderlik yazımdan önce kaybedilirse yazım yok; iki envanter sonra deleted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ── sahte hub ─────────────────────────────────────────────────────────────

type fakeCounter struct {
	labels map[string]string
	value  float64
	recent bool // pencere okumasında görünür (changes > 0 ya da yeni doğdu)
}

type fakeTarget struct {
	job string
	up  bool
}

type fakeCall struct {
	hub, kind, expr string
	noLabel         bool
}

type fakeHub struct {
	apps      []map[string]string
	counters  []fakeCounter
	partial   map[string]bool // okuma türü → uyarılı
	truncated map[string]bool
	err       error
	// live — scrape hedefleri {up==1, up==0}; nil = {1, 0} (sağlam tek hedef).
	live *[2]int
	// targets — doluysa live yerine: job'lu hedefler; sorgu `and on (job)`
	// taşıyorsa yalnız uygulama serisi üreten job'lar (apps[i]["job"]) sayılır.
	targets []fakeTarget
	// labelMissing — Argo serileri hub'ın küme etiketini TAŞIMAZ (§11 H0.3 ≠
	// H0.5): etiket enjekte edilen (noLabel=false) her okuma 0 seri döner.
	labelMissing bool
}

type fakeQuerier struct {
	mu    sync.Mutex
	hubs  map[string]*fakeHub
	calls []fakeCall
}

var (
	fqNSRe   = regexp.MustCompile(`namespace="([^"]+)"`)
	fqNameRe = regexp.MustCompile(`name=~"([^"]+)"`)
)

func fqKind(expr string) string {
	switch {
	case strings.Contains(expr, "count_values("):
		return "liveness"
	case strings.Contains(expr, "changes("):
		return "syncw"
	case strings.Contains(expr, SyncTotalMetric):
		return "syncf"
	case strings.Contains(expr, " unless "+AppInfoMetric):
		return "nonsteady"
	case strings.Contains(expr, "name=~"):
		return "targeted"
	}
	return "inventory"
}

func fqVector(rows []map[string]string, vals []float64) json.RawMessage {
	type s struct {
		Metric map[string]string `json:"metric"`
		Value  []any             `json:"value"`
	}
	out := make([]s, 0, len(rows))
	for i, m := range rows {
		v := "1"
		if vals != nil {
			v = fmt.Sprintf("%g", vals[i])
		}
		out = append(out, s{Metric: m, Value: []any{1, v}})
	}
	b, _ := json.Marshal(out)
	return b
}

func (f *fakeQuerier) Query(_ context.Context, hub, expr string, _ time.Time, noLabel bool, _ Reader) (MetricsQueryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kind := fqKind(expr)
	f.calls = append(f.calls, fakeCall{hub: hub, kind: kind, expr: expr, noLabel: noLabel})
	h := f.hubs[hub]
	if h == nil {
		return MetricsQueryResult{}, errors.New("bilinmeyen hub")
	}
	if h.err != nil {
		return MetricsQueryResult{}, h.err
	}
	ns := ""
	if m := fqNSRe.FindStringSubmatch(expr); m != nil {
		ns = m[1]
	}
	names := map[string]bool{}
	if m := fqNameRe.FindStringSubmatch(expr); m != nil {
		for _, n := range strings.Split(m[1], "|") {
			names[n] = true
		}
	}
	var rows []map[string]string
	var vals []float64
	switch {
	case h.labelMissing && !noLabel:
		vals = []float64{}
	case kind == "liveness":
		lv := [2]int{1, 0}
		if h.live != nil {
			lv = *h.live
		}
		if h.targets != nil {
			appJobs := map[string]bool{}
			for _, a := range h.apps {
				if a["namespace"] == ns {
					appJobs[a["job"]] = true
				}
			}
			byJob := strings.Contains(expr, " and on (job) ")
			lv = [2]int{}
			for _, tg := range h.targets {
				if byJob && !appJobs[tg.job] {
					continue
				}
				if tg.up {
					lv[0]++
				} else {
					lv[1]++
				}
			}
		}
		vals = []float64{}
		for i, v := range []string{"1", "0"} {
			if lv[i] > 0 {
				rows, vals = append(rows, map[string]string{"up": v}), append(vals, float64(lv[i]))
			}
		}
	case kind == "inventory" || kind == "nonsteady" || kind == "targeted":
		for _, a := range h.apps {
			if a["namespace"] != ns {
				continue
			}
			if kind == "nonsteady" && a["sync_status"] == "Synced" && a["health_status"] == "Healthy" && a["operation"] == "" {
				continue
			}
			if kind == "targeted" && !names[a["name"]] {
				continue
			}
			rows = append(rows, a)
		}
	default:
		for _, c := range h.counters {
			if c.labels["namespace"] != ns || (kind == "syncw" && !c.recent) {
				continue
			}
			rows, vals = append(rows, c.labels), append(vals, c.value)
		}
		if vals == nil {
			vals = []float64{}
		}
	}
	return MetricsQueryResult{ResultType: "vector", Result: fqVector(rows, vals), Series: len(rows),
		Partial: h.partial[kind], Truncated: h.truncated[kind]}, nil
}

func (f *fakeQuerier) kinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	for i, c := range f.calls {
		out[i] = c.hub + ":" + c.kind
	}
	f.calls = nil
	return out
}

func fqApp(ns, name, sync, health string) map[string]string {
	return map[string]string{"namespace": ns, "exported_namespace": ns, "name": name, "sync_status": sync,
		"health_status": health, "project": "p", "dest_server": "https://api.a.example:6443", "dest_namespace": "team-a"}
}

func fqCounter(ns, name, phase string, v float64, recent bool) fakeCounter {
	return fakeCounter{labels: map[string]string{"namespace": ns, "exported_namespace": ns, "name": name, "phase": phase}, value: v, recent: recent}
}

// ── sahte CH ──────────────────────────────────────────────────────────────

type fakeStatusStore struct {
	mu       sync.Mutex
	rows     []StatusRow
	runs     []rollout.WorkerRun
	reads    int
	writeErr error
	readErr  error
}

func (s *fakeStatusStore) ArgoCDLatestStatuses(_ context.Context, instanceID string) ([]StatusRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reads++
	if s.readErr != nil {
		return nil, s.readErr
	}
	latest := map[AppKey]StatusRow{}
	for _, r := range s.rows {
		if r.InstanceID != instanceID {
			continue
		}
		// Gerçek okumanın sözleşmesi (chstore argocdRebuildKeep): son satırı
		// 'deleted' olan uygulama yeniden kuruluma alınmaz — önce son satır
		// seçilir, sonra elenir.
		cur, ok := latest[r.Key()]
		if !ok || r.ChangedAt.After(cur.ChangedAt) || (r.ChangedAt.Equal(cur.ChangedAt) && r.Version > cur.Version) {
			latest[r.Key()] = r
		}
	}
	out := []StatusRow{}
	for _, k := range sortedAppKeys(latest) {
		if latest[k].ChangeKind != ChangeDeleted {
			out = append(out, latest[k])
		}
	}
	return out, nil
}

func (s *fakeStatusStore) ArgoCDWriteStatuses(_ context.Context, rows []StatusRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	for _, r := range rows {
		if err := ValidateStatusRow(r); err != nil {
			return err
		}
	}
	s.rows = append(s.rows, rows...)
	return nil
}

func (s *fakeStatusStore) RecordRolloutWorkerRun(_ context.Context, r rollout.WorkerRun) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := rollout.ValidateWorkerRun(r); err != nil {
		return err
	}
	s.runs = append(s.runs, r)
	return nil
}

// since — i. yazılan satırdan sonrakiler, "ad:tür[:faz]" biçiminde.
func (s *fakeStatusStore) since(i int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.rows[i:] {
		v := r.InstanceID + "/" + r.AppName + ":" + r.ChangeKind
		if r.SyncPhase != "" {
			v += ":" + r.SyncPhase
		}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func (s *fakeStatusStore) lastRun(t *testing.T) rollout.WorkerRun {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.runs) == 0 {
		t.Fatal("koşu satırı yok")
	}
	return s.runs[len(s.runs)-1]
}

type fakeRegistry struct{ reg Registry }

func (r fakeRegistry) ArgoRegistry() Registry { return r.reg }

// ── kurulum ───────────────────────────────────────────────────────────────

const (
	mwHub1 = "c-hub1"
	mwHub2 = "c-hub2"
)

type mwFixture struct {
	w     *MetricsWorker
	q     *fakeQuerier
	st    *fakeStatusStore
	cfg   Settings
	now   time.Time
	lead  bool
	cfgMu sync.Mutex
}

func newMWFixture(cfg Settings, hubs map[string]*fakeHub, reg Registry) *mwFixture {
	f := &mwFixture{q: &fakeQuerier{hubs: hubs}, st: &fakeStatusStore{}, cfg: cfg, lead: true,
		now: time.Date(2026, 9, 27, 12, 0, 20, 0, time.UTC)}
	f.w = NewMetricsWorker(f.st, f.q, fakeRegistry{reg}, func() Settings { f.cfgMu.Lock(); defer f.cfgMu.Unlock(); return f.cfg })
	f.w.now = func() time.Time { return f.now }
	f.w.host = "pod-a"
	f.w.SetLeaderCheck(func() bool { return f.lead })
	return f
}

func (f *mwFixture) tick(t *testing.T) {
	t.Helper()
	if !f.w.Tick(context.Background()) {
		t.Fatal("tik koşmadı")
	}
}

func mwOneHubSettings() Settings {
	s := DefaultSettings()
	s.Enabled, s.MetricsWorker.Enabled = true, true
	s.Hubs = []Hub{{ClusterID: mwHub1}}
	s.Instances = []Instance{{ID: "team-a-prod", HubClusterID: mwHub1, HubNamespace: "team-a-prod", Enabled: true}}
	return s
}

func mwReg(hubs ...string) Registry {
	r := Registry{Hubs: map[string]HubInfo{}, ByServer: map[string]string{"https://api.a.example:6443": "c-a"}}
	for i, h := range hubs {
		r.Hubs[h] = HubInfo{ID: h, Name: h, URL: fmt.Sprintf("http://thanos-%d.example", i), LabelName: "cluster", LabelValue: h}
	}
	return r
}

// ── testler ───────────────────────────────────────────────────────────────

func TestMetricsWorkerOffDoesNoIO(t *testing.T) {
	for name, cfg := range map[string]Settings{
		"varsayılan (kapalı)": DefaultSettings(),
		"açık ama hub yok":    func() Settings { s := DefaultSettings(); s.Enabled = true; return s }(),
		"hub var ama kapalı":  func() Settings { s := mwOneHubSettings(); s.Enabled = false; return s }(),
		// v0.10.983 inceleme: P1/P2'de "yalnız bayrağı kaydeder" denerek
		// enabled=true + hub kaydedilmiş blob (metricsWorker alanı yok) deploy'da
		// işçiyi BAŞLATMAZ.
		"eski blob: enabled + hub, metricsWorker yok": func() Settings {
			var s Settings
			if err := json.Unmarshal([]byte(`{"enabled":true,"hubs":[{"clusterId":"c-hub1"}],"instances":[{"id":"team-a-prod","hubClusterId":"c-hub1","hubNamespace":"team-a-prod","enabled":true}]}`), &s); err != nil {
				panic(err)
			}
			return s
		}(),
		"işçi açık ama entegrasyon kapalı": func() Settings { s := mwOneHubSettings(); s.Enabled = false; return s }(),
	} {
		f := newMWFixture(cfg, map[string]*fakeHub{mwHub1: {}}, mwReg(mwHub1))
		if f.w.Tick(context.Background()) {
			t.Errorf("%s: tik koşmamalı", name)
		}
		if len(f.q.kinds()) != 0 || f.st.reads != 0 || len(f.st.runs) != 0 {
			t.Errorf("%s: G/Ç olmamalı", name)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if f.w.WaitActive(ctx, time.Millisecond) {
			t.Errorf("%s: kapalı bayrakta WaitActive false döner", name)
		}
	}
	if !MetricsActive(mwOneHubSettings()) {
		t.Fatal("enabled + metricsWorker.enabled + hub → etkin")
	}
	// Validate: entegrasyon kapalıyken açık işçi bayrağı saklanmaz.
	in := mwOneHubSettings()
	in.Enabled, in.Hubs = false, nil
	in.Instances = nil
	if out, err := Validate(in, nil); err != nil || out.MetricsWorker.Enabled {
		t.Fatalf("kapalı entegrasyonda metricsWorker.enabled kapanmalı: %+v %v", out.MetricsWorker, err)
	}
}

func TestMetricsWorkerBaselineChangeSync(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{
		fqApp("team-a-prod", "a", "Synced", "Healthy"),
		fqApp("team-a-prod", "b", "Synced", "Healthy"),
		fqApp("other-ns", "x", "OutOfSync", "Healthy"), // başka instance namespace'i: seçici dışı
	}, counters: []fakeCounter{fqCounter("team-a-prod", "b", "Succeeded", 3, false)}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))

	// 1) ilk koşu: tam envanter, taban satırları.
	f.tick(t)
	if got := f.q.kinds(); strings.Join(got, ",") != "c-hub1:inventory,c-hub1:syncw,c-hub1:syncf,c-hub1:liveness" {
		t.Fatalf("ilk tik sorguları: %v", got)
	}
	if got := f.st.since(0); strings.Join(got, ",") != "team-a-prod/a:baseline,team-a-prod/b:baseline" {
		t.Fatalf("taban: %v", got)
	}
	run := f.st.lastRun(t)
	if run.Worker != rollout.WorkerArgoCDMetrics || run.Status != rollout.RunOK || run.ScopesTotal != 1 || run.ScopesOK != 1 ||
		run.RowsWritten != 2 || run.APICalls != 4 || run.Unmapped != 0 || run.Host != "pod-a" {
		t.Fatalf("koşu satırı: %+v", run)
	}
	for _, r := range f.st.rows {
		if r.ClusterID != "c-a" || !r.ChangedAt.Equal(time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("cluster_id ve tik aralığına kesik changed_at: %+v", r)
		}
	}

	// 2) a OutOfSync olur → sabit olmayan okuma, state satırı.
	n := len(f.st.rows)
	f.now = f.now.Add(time.Minute)
	hub.apps[0]["sync_status"] = "OutOfSync"
	f.tick(t)
	if got := f.q.kinds(); strings.Join(got, ",") != "c-hub1:nonsteady,c-hub1:syncw" {
		t.Fatalf("ucuz tik sorguları: %v", got)
	}
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/a:state" {
		t.Fatalf("değişim: %v", got)
	}

	// 3) a sabite döner + b'nin senkronu biter (b sabit; ikisi de hedefli okunur).
	n = len(f.st.rows)
	f.now = f.now.Add(time.Minute)
	hub.apps[0]["sync_status"] = "Synced"
	hub.counters[0].value, hub.counters[0].recent = 4, true
	f.tick(t)
	if got := f.q.kinds(); strings.Join(got, ",") != "c-hub1:nonsteady,c-hub1:syncw,c-hub1:targeted" {
		t.Fatalf("hedefli okuma bekleniyordu: %v", got)
	}
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/a:state,team-a-prod/b:sync:Succeeded" {
		t.Fatalf("sabite dönüş + senkron: %v", got)
	}

	// 4) aynı pencere yeniden okunur: artış yok, satır yok (çift sayım yok).
	n = len(f.st.rows)
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.st.since(n); len(got) != 0 {
		t.Fatalf("pencere örtüşmesi çift sayım üretmemeli: %v", got)
	}

	// 5) a için taban varken YENİ doğan seri (controller restart sonrası ilk senkron).
	n = len(f.st.rows)
	f.now = f.now.Add(time.Minute)
	hub.counters[0].recent = false
	hub.counters = append(hub.counters, fqCounter("team-a-prod", "a", "Failed", 1, true))
	f.tick(t)
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/a:sync:Failed" {
		t.Fatalf("yeni doğan seri: %v", got)
	}
}

func TestMetricsWorkerWarningSkipsShard(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "Synced", "Healthy")}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	hub.partial = map[string]bool{"inventory": true}
	f.tick(t)
	if len(f.st.rows) != 0 {
		t.Fatalf("uyarılı envanterden satır yazılmaz: %v", f.st.since(0))
	}
	run := f.st.lastRun(t)
	if run.Status != rollout.RunPartial || !run.PartialResponse || run.ScopesOK != 0 || !strings.Contains(run.Error, "shard_partial=1") {
		t.Fatalf("koşu partial + teşhis: %+v", run)
	}
	// Uyarı kalkınca taban yazılır (bellek atlanan tikte ilerlemedi).
	hub.partial = nil
	hub.truncated = map[string]bool{"syncw": true}
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if len(f.st.rows) != 0 || !f.st.lastRun(t).Truncated || !strings.Contains(f.st.lastRun(t).Error, "shard_truncated=1") {
		t.Fatalf("kesik senkron okuması da parçayı atlatır: %+v", f.st.lastRun(t))
	}
	hub.truncated = nil
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.st.since(0); strings.Join(got, ",") != "team-a-prod/a:baseline" {
		t.Fatalf("uyarısız tikte taban: %v", got)
	}
}

func TestMetricsWorkerTwoHubs(t *testing.T) {
	cfg := DefaultSettings()
	cfg.Enabled, cfg.MetricsWorker.Enabled = true, true
	off := false
	cfg.Hubs = []Hub{{ClusterID: mwHub1}, {ClusterID: mwHub2, InjectClusterLabel: &off}, {ClusterID: "c-hub3"}}
	cfg.Instances = []Instance{
		{ID: "gitops-1", HubClusterID: mwHub1, HubNamespace: "openshift-gitops", Enabled: true},
		{ID: "gitops-2", HubClusterID: mwHub2, HubNamespace: "openshift-gitops", Enabled: true},
		{ID: "gitops-3", HubClusterID: "c-hub3", HubNamespace: "openshift-gitops", Enabled: true},
		{ID: "idle", HubClusterID: mwHub1, HubNamespace: "idle", Enabled: false},
	}
	reg := mwReg(mwHub1, mwHub2, "c-hub3")
	h3 := reg.Hubs["c-hub3"]
	h3.TokenBad = true
	reg.Hubs["c-hub3"] = h3
	hubs := map[string]*fakeHub{
		mwHub1: {apps: []map[string]string{fqApp("openshift-gitops", "shared-name", "Synced", "Healthy")}},
		mwHub2: {apps: []map[string]string{fqApp("openshift-gitops", "shared-name", "OutOfSync", "Healthy")}},
	}
	// hub2 kendi apiServerUrls'ünde değil ama in-cluster adres hub'ın kendisine çözülür.
	hubs[mwHub2].apps[0]["dest_server"] = InClusterServer
	f := newMWFixture(cfg, hubs, reg)
	f.tick(t)
	if got := f.st.since(0); strings.Join(got, ",") != "gitops-1/shared-name:baseline,gitops-2/shared-name:baseline" {
		t.Fatalf("iki hub, aynı ad → iki instance anahtarı: %v", got)
	}
	for _, c := range f.q.calls {
		if (c.hub == mwHub2) != c.noLabel {
			t.Fatalf("injectClusterLabel hub başına: %+v", c)
		}
		if c.hub == "c-hub3" {
			t.Fatal("tokenRef'i çözülmeyen hub'a istek gitmemeli")
		}
	}
	for _, r := range f.st.rows {
		if r.InstanceID == "gitops-2" && r.ClusterID != mwHub2 {
			t.Fatalf("in-cluster hedef instance'ın KENDİ hub'ına: %+v", r)
		}
	}
	run := f.st.lastRun(t)
	if run.ScopesTotal != 3 || run.ScopesOK != 2 || run.Status != rollout.RunPartial || !strings.Contains(run.Error, "tokenRef") {
		t.Fatalf("üç parça (etkin olmayan hariç), biri atlandı: %+v", run)
	}
}

func TestPlanShardsOverlapGuard(t *testing.T) {
	cfg := DefaultSettings()
	off := false
	cfg.Hubs = []Hub{{ClusterID: mwHub1, InjectClusterLabel: &off}, {ClusterID: mwHub2, InjectClusterLabel: &off}}
	cfg.Instances = []Instance{
		{ID: "g1", HubClusterID: mwHub1, HubNamespace: "gitops", Enabled: true},
		{ID: "g2", HubClusterID: mwHub2, HubNamespace: "gitops", Enabled: true},
		{ID: "g3", HubClusterID: mwHub2, HubNamespace: "other", Enabled: true},
	}
	reg := mwReg(mwHub1, mwHub2)
	h2 := reg.Hubs[mwHub2]
	h2.URL = reg.Hubs[mwHub1].URL + "/" // aynı Thanos
	reg.Hubs[mwHub2] = h2
	plans := planShards(cfg, reg)
	// v0.10.983 inceleme: yalnız ikinciyi atlatmak yetmez — ilki diğer hub'ın
	// serilerini de okur ve kendi instance_id'si + hub'ının cluster_id'siyle
	// yazardı. Aynı kapsamdaki parçaların HEPSİ atlanır.
	if len(plans) != 3 || !strings.Contains(plans[0].Skip, "g1, g2") || !strings.Contains(plans[1].Skip, "g1, g2") || plans[2].Skip != "" {
		t.Fatalf("etiketsiz aynı Thanos + aynı seçici iki parçayı da atlatır: %+v", plans)
	}
	if plans[2].Scope == "" || plans[0].Scope == plans[1].Scope {
		t.Fatalf("kapsam hub id'sini taşır: %q %q", plans[0].Scope, plans[1].Scope)
	}
	// v0.10.983 ikinci inceleme: yalnız birine metricsJob vermek AYIRMAZ —
	// g1'in job'suz seçicisi g2'nin serilerini de okur (üst küme).
	cfg.Instances[1].MetricsJob = "hub2-metrics"
	if p := planShards(cfg, reg); p[0].Skip == "" || p[1].Skip == "" || p[2].Skip != "" {
		t.Fatalf("job'suz + job'lu aynı Thanos/namespace çakışır: %+v", p)
	}
	// İkisinde de farklı job → ayrık.
	cfg.Instances[0].MetricsJob = "hub1-metrics"
	for _, p := range planShards(cfg, reg) {
		if p.Skip != "" {
			t.Fatalf("farklı metricsJob ile ayrılan parçalar okunur: %+v", p)
		}
	}
	// İkisinde de AYNI job → çakışır.
	cfg.Instances[0].MetricsJob = "hub2-metrics"
	if p := planShards(cfg, reg); p[0].Skip == "" || p[1].Skip == "" {
		t.Fatalf("aynı job çakışır: %+v", p)
	}
	cfg.Instances[0].MetricsJob, cfg.Instances[1].MetricsJob = "", ""
	// Etiketli hub + etiketsiz hub aynı Thanos'ta: etiketsiz okuma üst küme → ikisi de atlanır.
	cfg.Hubs = []Hub{{ClusterID: mwHub1}, {ClusterID: mwHub2, InjectClusterLabel: &off}}
	if p := planShards(cfg, reg); p[0].Skip == "" || p[1].Skip == "" || p[2].Skip != "" {
		t.Fatalf("etiketli + etiketsiz aynı Thanos/namespace çakışır: %+v", p)
	}
	// Enjeksiyon ikisinde de açık, etiket değerleri farklı → çakışma yok.
	cfg.Hubs = []Hub{{ClusterID: mwHub1}, {ClusterID: mwHub2}}
	for _, p := range planShards(cfg, reg) {
		if p.Skip != "" {
			t.Fatalf("etiketli okumada çakışma yok: %+v", p)
		}
	}
	// Etiket değerleri aynı (iki kayıt aynı cluster= değeri) → çakışır.
	h2l := reg.Hubs[mwHub2]
	h2l.LabelValue = reg.Hubs[mwHub1].LabelValue
	reg.Hubs[mwHub2] = h2l
	if p := planShards(cfg, reg); p[0].Skip == "" || p[1].Skip == "" {
		t.Fatalf("aynı etiket değeri çakışır: %+v", p)
	}
	h2l.LabelValue = mwHub2
	reg.Hubs[mwHub2] = h2l
	// Kayıtta olmayan (devre dışı) hub'ın parçaları atlanır.
	delete(reg.Hubs, mwHub2)
	if p := planShards(cfg, reg); p[1].Skip == "" || p[1].Hub.ID != mwHub2 {
		t.Fatalf("devre dışı hub: %+v", p[1])
	}
}

func TestMetricsWorkerRebuildOnAcquire(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{
		fqApp("team-a-prod", "a", "Synced", "Healthy"),
		fqApp("team-a-prod", "b", "OutOfSync", "Healthy"),
		fqApp("team-a-prod", "c", "Synced", "Healthy"),
	}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	// Önceki lider a ve b'yi yazmıştı (b o zaman Synced idi).
	prevAt := f.now.Add(-time.Hour).Truncate(time.Minute)
	mk := func(name, sync string) StatusRow {
		r := StatusRow{InstanceID: "team-a-prod", AppNamespace: "team-a-prod", AppName: name, ChangedAt: prevAt, ChangeKind: ChangeBaseline,
			ClusterID: "c-a", Version: uint64(prevAt.UnixNano())}
		return r.WithTuple(AppTuple{SyncStatus: sync, HealthStatus: "Healthy", Project: "p", DestServer: "https://api.a.example:6443", DestNamespace: "team-a"})
	}
	f.st.rows = []StatusRow{mk("a", "Synced"), mk("b", "Synced")}
	f.tick(t)
	if f.st.reads != 1 {
		t.Fatalf("durum CH'den kurulmalı: %d okuma", f.st.reads)
	}
	// a aynı → satır yok; b değişmiş → state; c bilinmiyor (yeni mi TTL mi belirsiz) → baseline.
	if got := f.st.since(2); strings.Join(got, ",") != "team-a-prod/b:state,team-a-prod/c:baseline" {
		t.Fatalf("yeniden kurulum sonrası fark: %v", got)
	}
	// Bellek taşınır: sonraki tik CH okumaz.
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if f.st.reads != 1 {
		t.Fatalf("lider belleği tikler arasında taşınır: %d okuma", f.st.reads)
	}
	// Yeniden edinim → yeniden okuma, yeni satır yok (her şey CH'de).
	n := len(f.st.rows)
	f.w.OnAcquire()
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if f.st.reads != 2 || len(f.st.rows) != n {
		t.Fatalf("edinimde yeniden kurulum, sahte satır yok: okuma %d, yeni satır %v", f.st.reads, f.st.since(n))
	}
	// Okuma hatası: parça sert hata, satır yok.
	f.w.OnAcquire()
	f.st.readErr = errors.New("ch down")
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if run := f.st.lastRun(t); run.Status != rollout.RunFailed || !strings.Contains(run.Error, "önceki durum okunamadı") {
		t.Fatalf("okuma hatası: %+v", run)
	}
}

func TestMetricsWorkerLeaderLostAndDeletion(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "Synced", "Healthy"), fqApp("team-a-prod", "b", "Synced", "Healthy")}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	calls := 0
	f.w.SetLeaderCheck(func() bool { calls++; return calls == 1 }) // tik başında lider, yazımdan önce değil
	f.tick(t)
	if len(f.st.rows) != 0 || !strings.Contains(f.st.lastRun(t).Error, "liderlik") {
		t.Fatalf("liderlik yazımdan önce kaybedildi: %v", f.st.since(0))
	}
	f.w.SetLeaderCheck(func() bool { return true })
	f.now = f.now.Add(time.Minute)
	f.tick(t) // taban
	if len(f.st.rows) != 2 {
		t.Fatalf("taban: %v", f.st.since(0))
	}
	// b kaybolur: sabit olmayan tikte bir şey olmaz; iki envanter sonra deleted.
	hub.apps = hub.apps[:1]
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t) // 1. envanter: yalnız sayılır
	if len(f.st.rows) != 2 {
		t.Fatalf("ilk envanter yokluğu satır yazmaz: %v", f.st.since(2))
	}
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t)
	if got := f.st.since(2); strings.Join(got, ",") != "team-a-prod/b:deleted" {
		t.Fatalf("ikinci envanterde deleted: %v", got)
	}
	// Yazım hatası: bellek düşer, sonraki tik CH'den kurar.
	hub.apps[0]["sync_status"] = "OutOfSync"
	f.st.writeErr = errors.New("insert failed")
	reads := f.st.reads
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	f.st.writeErr = nil
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if f.st.reads != reads+1 {
		t.Fatalf("yazım hatası sonrası CH'den kurulum: %d → %d", reads, f.st.reads)
	}
	if got := f.st.since(3); strings.Join(got, ",") != "team-a-prod/a:state" {
		t.Fatalf("başarısız yazım sonraki tikte yeniden türetilir: %v", got)
	}
}

func TestSyncWindow(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		last     time.Time
		interval time.Duration
		want     time.Duration
	}{
		{time.Time{}, time.Minute, 2 * time.Minute},
		{now.Add(-time.Minute), time.Minute, 2 * time.Minute},
		{now.Add(-3 * time.Minute), time.Minute, 4 * time.Minute},
		{now.Add(-time.Hour), time.Minute, 5 * time.Minute},
		{now.Add(-time.Hour), 5 * time.Minute, 10 * time.Minute},
	} {
		if got := syncWindow(c.last, now, c.interval); got != c.want {
			t.Errorf("syncWindow(%v, %v) = %v, beklenen %v", now.Sub(c.last), c.interval, got, c.want)
		}
	}
}

func TestRegistryFrom(t *testing.T) {
	snap := []thanos.ClusterSnapshot{
		{ID: "c-hub", Name: "hub", TokenRef: "env:X", TokenResolved: false, APIServerURLs: []string{"https://api.hub.example:6443"}},
		{ID: "c-a", Name: "a", APIServerURLs: []string{"https://API.a.example"}},
		{ID: "c-off", Name: "off", APIServerURLs: []string{"https://api.off.example:6443"}},
	}
	byID := func(id string) (thanos.ClusterConfig, bool) {
		switch id {
		case "c-hub":
			return thanos.ClusterConfig{ID: id, Name: "hub", URL: "http://thanos-hub", ThanosLabelName: "cluster"}, true
		case "c-a":
			return thanos.ClusterConfig{ID: id, Name: "a", URL: "http://thanos-a"}, true
		}
		return thanos.ClusterConfig{}, false
	}
	reg := registryFrom(snap, byID)
	if h := reg.Hubs["c-hub"]; !h.TokenBad || h.LabelName != "cluster" || h.LabelValue != "hub" || h.URL != "http://thanos-hub" {
		t.Fatalf("hub bilgisi: %+v", h)
	}
	if _, ok := reg.Hubs["c-off"]; ok {
		t.Fatal("devre dışı kayıt hub değil")
	}
	if got := DestClusterID("https://api.a.example:6443/", "c-hub", reg.ByServer, reg.Normalize); got != "c-a" {
		t.Fatalf("dest_server normalleşip eşlenmeli: %q (%v)", got, reg.ByServer)
	}
	if got := DestClusterID("https://api.off.example:6443", "c-hub", reg.ByServer, reg.Normalize); got != "" {
		t.Fatalf("devre dışı kaydın adresi eşlenmez: %q", got)
	}
	if r := metricsResultFromConsole(&thanos.ConsoleResult{ResultType: "vector", Series: 3, Truncated: true,
		ConsoleNotes: thanos.ConsoleNotes{Warnings: []string{"partial"}}}); !r.Partial || !r.Truncated || r.Series != 3 {
		t.Fatalf("uyarı → Partial: %+v", r)
	}
}

// ── v0.10.983 inceleme düzeltmeleri ───────────────────────────────────────

func (s *fakeStatusStore) kindsSince(i int) map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, r := range s.rows[i:] {
		out[r.ChangeKind]++
	}
	return out
}

// §11 H0.3 ≠ H0.5: varsayılan injectClusterLabel=true ile ilk envanter 0 seri
// döner. Bu "taban alındı" SAYILMAZ; admin injectClusterLabel=false yapınca
// (kapsam değişir) ilk dolu envanter HER uygulamayı 'baseline' yazar — tek bir
// 'appeared' yok.
func TestMetricsWorkerEmptyInventoryThenLabelFix(t *testing.T) {
	hub := &fakeHub{labelMissing: true, apps: []map[string]string{
		fqApp("team-a-prod", "a", "Synced", "Healthy"), fqApp("team-a-prod", "b", "OutOfSync", "Healthy"), fqApp("team-a-prod", "c", "Synced", "Degraded"),
	}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	if len(f.st.rows) != 0 {
		t.Fatalf("boş envanter satır yazmaz: %v", f.st.since(0))
	}
	if run := f.st.lastRun(t); run.Status != rollout.RunPartial || !strings.Contains(run.Error, "inventory_empty=1") || !strings.Contains(run.Error, "H0.3") {
		t.Fatalf("boş envanter partial + teşhis: %+v", run)
	}
	// Bir sonraki tik de envanterdir (taban yok) ve yine boş.
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.q.kinds(); got[len(got)-1] != "c-hub1:inventory" {
		t.Fatalf("taban alınana dek her tik envanter: %v", got)
	}
	f.cfgMu.Lock()
	off := false
	f.cfg.Hubs[0].InjectClusterLabel = &off
	f.cfgMu.Unlock()
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if k := f.st.kindsSince(0); k[ChangeBaseline] != 3 || len(k) != 1 {
		t.Fatalf("etiket düzeltildi → hepsi baseline, appeared yok: %v", f.st.since(0))
	}
	if run := f.st.lastRun(t); !strings.Contains(run.Error, "scope_changed=1") || run.Status != rollout.RunOK {
		t.Fatalf("kapsam değişimi teşhiste: %+v", run)
	}
	// Sonraki ucuz tikte satır yok (bellek taban).
	n := len(f.st.rows)
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if len(f.st.rows) != n {
		t.Fatalf("taban sonrası sahte satır: %v", f.st.since(n))
	}
}

// Dolu bir tabandan sonra hub'ın Thanos URL'si (ya da küme etiketi) değişirse
// aynı seçici başka bir seri kümesini okur: ilk envanter taban sayılır —
// bilinmeyen uygulama 'appeared' değil, farklı tuple 'state' değil 'baseline'.
func TestMetricsWorkerScopeChangeRebaselines(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "Synced", "Healthy"), fqApp("team-a-prod", "b", "Synced", "Healthy")}}
	reg := mwReg(mwHub1)
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, reg)
	f.tick(t)
	n := len(f.st.rows)
	h := reg.Hubs[mwHub1]
	h.URL = "http://thanos-new.example"
	reg.Hubs[mwHub1] = h
	hub.apps = []map[string]string{fqApp("team-a-prod", "a", "OutOfSync", "Healthy"), fqApp("team-a-prod", "b", "Synced", "Healthy"), fqApp("team-a-prod", "z", "Synced", "Healthy")}
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/a:baseline,team-a-prod/z:baseline" {
		t.Fatalf("kapsam değişimi sonrası taban: %v", got)
	}
	// Taban bitti: sonraki gerçek değişim yine 'state'.
	n = len(f.st.rows)
	hub.apps[1]["sync_status"] = "OutOfSync"
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/b:state" {
		t.Fatalf("taban sonrası değişim state: %v", got)
	}
}

// Scrape kesintisi: bilinen 5 uygulama, metrikler 3 envanter boyunca yok →
// tek bir 'deleted' yok; metrikler dönünce 'appeared' da yok.
func TestMetricsWorkerOutageWritesNoDeleted(t *testing.T) {
	var all []map[string]string
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		all = append(all, fqApp("team-a-prod", n, "Synced", "Healthy"))
	}
	hub := &fakeHub{apps: all}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	n := len(f.st.rows)
	hub.apps, hub.live = nil, &[2]int{0, 0}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if got := f.st.since(n); len(got) != 0 {
		t.Fatalf("kesinti deleted yazmamalı: %v", got)
	}
	if !strings.Contains(f.st.lastRun(t).Error, "inventory_empty=1") {
		t.Fatalf("boş envanter teşhiste: %+v", f.st.lastRun(t))
	}
	hub.apps, hub.live = all, nil
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t)
	if got := f.st.since(n); len(got) != 0 {
		t.Fatalf("dönüşte appeared yazılmamalı: %v", got)
	}
}

// Controller shard'ı çöktü (hedef up==0) ya da hedef kayboldu: uygulamaların
// bir kısmı envanterde yok ama yokluk SAYILMAZ; hedefler sağlamken silme iki
// envanter sonra yazılır.
func TestMetricsWorkerLivenessGatesDeletion(t *testing.T) {
	var all []map[string]string
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		all = append(all, fqApp("team-a-prod", n, "Synced", "Healthy"))
	}
	hub := &fakeHub{apps: all, live: &[2]int{3, 0}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	n := len(f.st.rows)
	hub.apps = all[:4] // e, f bir shard'ın uygulamaları
	hub.live = &[2]int{2, 1}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if got := f.st.since(n); len(got) != 0 || !strings.Contains(f.st.lastRun(t).Error, "absence_held_targets_down=1") {
		t.Fatalf("düşmüş hedefte silme yok: %v %s", got, f.st.lastRun(t).Error)
	}
	// Shard döndü: hepsi yine görülüyor, satır yok.
	hub.apps, hub.live = all, &[2]int{3, 0}
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t)
	// Hedef kayboldu (up sayısı 3 → 2, düşen yok): o envanterde yine sayılmaz.
	hub.apps, hub.live = all[:4], &[2]int{2, 0}
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t)
	if got := f.st.since(n); len(got) != 0 || !strings.Contains(f.st.lastRun(t).Error, "absence_held_targets_lost=1") {
		t.Fatalf("kaybolan hedefte silme yok: %v %s", got, f.st.lastRun(t).Error)
	}
	// Hedef sayısı sabit ve sağlam (bilinen sınır: kalıcı küçülme bir envanter
	// sonra olağan sayılır): iki envanter sonra deleted.
	for i := 0; i < 2; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/e:deleted,team-a-prod/f:deleted" {
		t.Fatalf("sağlam hedeflerle iki envanter sonra deleted: %v", got)
	}
	// Hiç hedef yoksa (up serisi bulunamadı) silme hiç yazılmaz.
	n = len(f.st.rows)
	hub.apps, hub.live = all[:2], &[2]int{0, 0}
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if got := f.st.since(n); len(got) != 0 || !strings.Contains(f.st.lastRun(t).Error, "absence_held_no_targets=1") {
		t.Fatalf("hedef yokken silme yok: %v %s", got, f.st.lastRun(t).Error)
	}
}

// v0.10.983 ikinci inceleme: metricsJob boşken seçici bütün namespace'tir;
// kalıcı up=0 kalan ilgisiz hedef (dex) silmeyi sonsuza dek bekletiyor, koşu
// ok görünüyordu. Hedef sağlığı yalnız argocd_app_info üreten job'larda
// sayılır; aynı job'un düşmüş shard'ı ise silmeyi bekletmeye devam eder ve
// metricsHeldWarn envanterden sonra koşu partial + not olur.
func TestMetricsWorkerLivenessIgnoresUnrelatedTargets(t *testing.T) {
	var all []map[string]string
	for _, n := range []string{"a", "b", "c", "d"} {
		a := fqApp("team-a-prod", n, "Synced", "Healthy")
		a["job"] = "argocd-metrics"
		all = append(all, a)
	}
	hub := &fakeHub{apps: all, targets: []fakeTarget{{"argocd-metrics", true}, {"argocd-dex-server", false}, {"argocd-redis-exporter", false}}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	n := len(f.st.rows)
	hub.apps = all[:3] // d silindi
	for i := 0; i < 2; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if got := f.st.since(n); strings.Join(got, ",") != "team-a-prod/d:deleted" {
		t.Fatalf("ilgisiz düşmüş hedef silmeyi bekletmemeli: %v %s", got, f.st.lastRun(t).Error)
	}
	// Aynı job'un ikinci shard'ı kalıcı düştü: silme bekler, 3 envanter sonra koşu partial.
	hub.targets = append(hub.targets, fakeTarget{"argocd-metrics", false})
	hub.apps = all[:2]
	n = len(f.st.rows)
	for i := 0; i < metricsHeldWarn; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
		run := f.st.lastRun(t)
		wantPartial := i == metricsHeldWarn-1
		if (run.Status == rollout.RunPartial) != wantPartial {
			t.Fatalf("envanter %d: bekletme %d envanterden sonra partial olmalı: %+v", i, metricsHeldWarn, run)
		}
	}
	if got := f.st.since(n); len(got) != 0 {
		t.Fatalf("düşmüş app job'u hedefinde silme yok: %v", got)
	}
	if run := f.st.lastRun(t); !strings.Contains(run.Error, "absence_held_streak=1") || !strings.Contains(run.Error, "ardışık envanterdir bekletiliyor") {
		t.Fatalf("kalıcı bekletme görünür olmalı: %s", run.Error)
	}
	// Hedef döndü: bekletme biter, koşu ok.
	hub.targets = hub.targets[:3]
	f.now = f.now.Add(15 * time.Minute)
	f.tick(t)
	if run := f.st.lastRun(t); run.Status != rollout.RunOK {
		t.Fatalf("hedef dönünce ok: %+v", run)
	}
}

// v0.10.983 ikinci inceleme: silinmiş uygulama lider belleğinden düşer.
func TestMetricsWorkerForgetsDeletedApps(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "Synced", "Healthy"), fqApp("team-a-prod", "pr-1", "Synced", "Healthy")}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	hub.apps = hub.apps[:1]
	for i := 0; i < 3; i++ {
		f.now = f.now.Add(15 * time.Minute)
		f.tick(t)
	}
	if _, ok := f.w.mem["team-a-prod"].apps[AppKey{"team-a-prod", "team-a-prod", "pr-1"}]; ok {
		t.Fatalf("silinmiş uygulama bellekte kalmamalı: %v / %v", f.w.mem["team-a-prod"].apps, f.st.since(0))
	}
}

// HA çifti taban envanterinde aynı uygulama için iki farklı durum döndürdü
// (belirsiz): taban bittikten sonra ilk gözlemde 'baseline', 'appeared' değil.
func TestMetricsWorkerAmbiguousDuringBaseline(t *testing.T) {
	dup := fqApp("team-a-prod", "a", "Synced", "Degraded")
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "Synced", "Healthy"), dup, fqApp("team-a-prod", "b", "Synced", "Healthy")}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	f.tick(t)
	if got := f.st.since(0); strings.Join(got, ",") != "team-a-prod/b:baseline" {
		t.Fatalf("belirsiz uygulama tabanda yazılmaz: %v", got)
	}
	if !strings.Contains(f.st.lastRun(t).Error, "baseline_pending_ambiguous=1") {
		t.Fatalf("teşhis: %s", f.st.lastRun(t).Error)
	}
	hub.apps = []map[string]string{fqApp("team-a-prod", "a", "OutOfSync", "Healthy"), hub.apps[2]}
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := f.st.since(1); strings.Join(got, ",") != "team-a-prod/a:baseline" {
		t.Fatalf("taban anahtarı ilk gözlemde baseline: %v", got)
	}
	if f.w.mem["team-a-prod"].pendingBase != nil {
		t.Fatal("gözlenen anahtar bekleyen listeden düşmeli")
	}
}

// Kesik (reader.maxSeries) envanter her tik yeniden çekilmez: 2ⁿ × aralık geri
// çekilme (tavan inventoryMin), arada ucuz sabit-olmayan okuma.
func TestMetricsWorkerInventoryBackoff(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "a", "OutOfSync", "Healthy")}, truncated: map[string]bool{"inventory": true}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	step := func() string {
		f.now = f.now.Add(time.Minute)
		f.tick(t)
		return f.q.kinds()[0]
	}
	f.tick(t)
	f.q.kinds()
	seq := []string{step(), step(), step(), step(), step(), step(), step()}
	// t+1 ucuz (2 dk beklenir), t+2 envanter (kesik → 4 dk), t+3..5 ucuz, t+6 envanter, t+7 ucuz.
	want := []string{"c-hub1:nonsteady", "c-hub1:inventory", "c-hub1:nonsteady", "c-hub1:nonsteady", "c-hub1:nonsteady", "c-hub1:inventory", "c-hub1:nonsteady"}
	if strings.Join(seq, ",") != strings.Join(want, ",") {
		t.Fatalf("geri çekilme sırası:\n got %v\nwant %v", seq, want)
	}
	if !strings.Contains(f.st.lastRun(t).Error, "inventory_backoff=1") || len(f.st.rows) != 0 {
		t.Fatalf("geri çekilme teşhiste, taban yokken satır yok: %s %v", f.st.lastRun(t).Error, f.st.since(0))
	}
	for _, c := range []struct {
		n    int
		want time.Duration
	}{{0, 0}, {1, 2 * time.Minute}, {2, 4 * time.Minute}, {3, 8 * time.Minute}, {4, 15 * time.Minute}, {9, 15 * time.Minute}} {
		if got := inventoryBackoff(c.n, time.Minute, 15*time.Minute); got != c.want {
			t.Errorf("inventoryBackoff(%d) = %v, beklenen %v", c.n, got, c.want)
		}
	}
	// Kesiklik kalkınca bekleme dolduğunda taban alınır.
	hub.truncated = nil
	for i := 0; i < 8 && len(f.st.rows) == 0; i++ {
		step()
	}
	if got := f.st.since(0); strings.Join(got, ",") != "team-a-prod/a:baseline" {
		t.Fatalf("kesiklik kalkınca taban: %v", got)
	}
}

// §10.3.3: 180 günlük TTL'e yaklaşan (StatusRefreshAge'den eski) değişmemiş
// uygulamanın satırı tam envanterde 'baseline' ile tazelenir.
func TestMetricsWorkerTTLRefresh(t *testing.T) {
	hub := &fakeHub{apps: []map[string]string{fqApp("team-a-prod", "old", "Synced", "Healthy"), fqApp("team-a-prod", "new", "Synced", "Healthy")}}
	f := newMWFixture(mwOneHubSettings(), map[string]*fakeHub{mwHub1: hub}, mwReg(mwHub1))
	mk := func(name string, at time.Time) StatusRow {
		r := StatusRow{InstanceID: "team-a-prod", AppNamespace: "team-a-prod", AppName: name, ChangedAt: at, ChangeKind: ChangeBaseline,
			ClusterID: "c-a", Version: uint64(at.UnixNano())}
		return r.WithTuple(AppTuple{SyncStatus: "Synced", HealthStatus: "Healthy", Project: "p", DestServer: "https://api.a.example:6443", DestNamespace: "team-a"})
	}
	f.st.rows = []StatusRow{mk("old", f.now.Add(-160*24*time.Hour)), mk("new", f.now.Add(-24*time.Hour))}
	f.tick(t)
	if got := f.st.since(2); strings.Join(got, ",") != "team-a-prod/old:baseline" {
		t.Fatalf("eski satır tazelenir, yenisi değil: %v", got)
	}
	if !strings.Contains(f.st.lastRun(t).Error, "ttl_refresh=1") {
		t.Fatalf("teşhis: %s", f.st.lastRun(t).Error)
	}
}
