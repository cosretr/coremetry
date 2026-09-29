package argocd

// mapper_worker_test.go — v0.10.985 — P3.2 eşleyici adımı argocd-metrics
// işçisinin içinde (mapper.go). Sahte hub + sahte CH (metrics_worker_test.go
// fikstürü) + sahte eşleme deposu. Sözleşme:
//   - ilk tik: envanter → aynı tikte tur; ad + zayıf kenar yazılır, iş yükü
//     kenarı almayan uygulama koşu satırının unmapped'ına eklenir;
//   - aralık dolmadan tur yok; ayar kaydı (UpdatedAt) turu hemen koşturur;
//   - kesik iş yükü listesi / okuma hatası: yazım yok, koşu partial + not;
//     başarılı tura dek tur arası her tik de partial;
//   - hazır olmayan instance (atlanan parça): sorgu yok, kenarına dokunulmaz;
//   - liderlik yazımdan önce kaybedilirse yazım yok;
//   - eşleyici bağlanmamışsa (SetMapperStore yok) G/Ç yok.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/rollout"
)

type fakeMapperStore struct {
	mu        sync.Mutex
	workloads []WorkloadObs
	capped    bool
	wlErr     error
	rows      []MappingRow
	calls     []string
	liveIDs   [][]string
	onRead    func() // iş yükü okumasında (tur başladı)
}

func (m *fakeMapperStore) ArgoCDMapperWorkloads(_ context.Context, from, to time.Time) ([]WorkloadObs, bool, error) {
	if m.onRead != nil {
		m.onRead()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "workloads")
	if to.Sub(from) != MapperWorkloadWindow {
		return nil, false, errors.New("pencere 24 sa olmalı")
	}
	return m.workloads, m.capped, m.wlErr
}

func (m *fakeMapperStore) ArgoCDLiveMappings(_ context.Context, ids []string) ([]MappingRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "live")
	m.liveIDs = append(m.liveIDs, ids)
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	latest := map[EdgeKey]MappingRow{}
	for _, r := range m.rows {
		if cur, ok := latest[r.Key()]; !ok || r.Version > cur.Version {
			latest[r.Key()] = r
		}
	}
	var out []MappingRow
	for _, r := range latest {
		if want[r.InstanceID] && r.RemovedAt.IsZero() {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *fakeMapperStore) ArgoCDWriteMappings(_ context.Context, rows []MappingRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, "write")
	for _, r := range rows {
		if err := ValidateMappingRow(r); err != nil {
			return err
		}
	}
	m.rows = append(m.rows, rows...)
	return nil
}

func (m *fakeMapperStore) take() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.calls
	m.calls = nil
	return out
}

// live — son sürüm canlı kenarlar "ns/workload ← app method" biçiminde.
func (m *fakeMapperStore) live() map[string]bool {
	rows, _ := m.ArgoCDLiveMappings(context.Background(), []string{"team-a-prod"})
	m.take()
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Namespace+"/"+r.Workload+" ← "+r.AppName+" "+r.MatchMethod] = true
	}
	return out
}

func mapperFixture(t *testing.T) (*mwFixture, *fakeHub, *fakeMapperStore) {
	t.Helper()
	app := func(name, dns string) map[string]string {
		a := fqApp("team-a-prod", name, "Synced", "Healthy")
		a["dest_namespace"] = dns
		return a
	}
	hub := &fakeHub{apps: []map[string]string{
		app("team-a-api-prod-ca", "team-a"),
		app("team-a-web-prod-ca", "team-a"),
		app("team-a-tools-prod-ca", "team-a"), // iş yükü yok → unmapped
	}}
	reg := mwReg(mwHub1)
	reg.BySpan = map[string]string{"span-a": "c-a"}
	reg.Suffix = map[string]string{"c-a": "ca"}
	cfg := mwOneHubSettings()
	cfg.EnvList = []string{"prod"}
	f := newMWFixture(cfg, map[string]*fakeHub{mwHub1: hub}, reg)
	ms := &fakeMapperStore{workloads: []WorkloadObs{
		{SpanCluster: "span-a", Namespace: "team-a", Kind: "Deployment", Workload: "api"},
		{SpanCluster: "span-a", Namespace: "team-a", Kind: "Deployment", Workload: "web"},
		{SpanCluster: "ghost", Namespace: "team-a", Kind: "Deployment", Workload: "api"},
	}}
	f.w.SetMapperStore(ms)
	return f, hub, ms
}

func TestMapperStepWritesEdgesAndCounts(t *testing.T) {
	f, hub, ms := mapperFixture(t)
	f.tick(t)
	if got := strings.Join(ms.take(), ","); got != "workloads,live,write" {
		t.Fatalf("ilk tik eşleyici çağrıları: %s", got)
	}
	live := ms.live()
	for _, want := range []string{
		"team-a/api ← team-a-api-prod-ca name",
		"team-a/web ← team-a-web-prod-ca name",
		"team-a/ ← team-a-api-prod-ca namespace",
		"team-a/ ← team-a-tools-prod-ca namespace",
	} {
		if !live[want] {
			t.Errorf("kenar yok: %s (%v)", want, live)
		}
	}
	run := f.st.lastRun(t)
	if run.Status != rollout.RunOK || run.Unmapped != 1 || run.RowsWritten != 3+5 {
		t.Fatalf("koşu: 3 durum + 5 kenar satırı, 1 eşlenmemiş uygulama: %+v", run)
	}
	for _, code := range []string{"mapper_runs=1", "mapper_edges_name=2", "mapper_edges_namespace=3", "mapper_unmapped_apps=1",
		"mapper_workload_cluster_unmapped=1", "mapper_name_component_exact=2", "mapper_rows_written=5"} {
		if !strings.Contains(run.Error, code) {
			t.Errorf("teşhiste %s yok: %s", code, run.Error)
		}
	}

	// Aralık dolmadı: tur yok.
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := ms.take(); len(got) != 0 {
		t.Fatalf("mapperMin dolmadan tur olmamalı: %v", got)
	}
	// Ayar kaydı turu hemen koşturur; web uygulaması silinmiş olsa bile iki
	// tam envanter dolmadan bellekte canlıdır — kenar kalır, değişmeyen taze
	// kenar yeniden yazılmaz.
	f.cfgMu.Lock()
	f.cfg.UpdatedAt = 42
	f.cfgMu.Unlock()
	f.now = f.now.Add(time.Minute)
	f.tick(t)
	if got := strings.Join(ms.take(), ","); got != "workloads,live" {
		t.Fatalf("ayar değişince tur koşmalı, değişiklik yoksa yazım yok: %s", got)
	}
	// 10 dk sonra web iş yükü artık span üretmiyor → ad kenarı kaldırılır.
	ms.workloads = ms.workloads[:1]
	f.now = f.now.Add(10 * time.Minute)
	f.tick(t)
	if got := strings.Join(ms.take(), ","); got != "workloads,live,write" {
		t.Fatalf("aralık dolunca tur: %s", got)
	}
	live = ms.live()
	if live["team-a/web ← team-a-web-prod-ca name"] || !live["team-a/ ← team-a-web-prod-ca namespace"] {
		t.Fatalf("iş yükü gidince yalnız ad kenarı kapanır: %v", live)
	}
	_ = hub
}

func TestMapperStepFailSafe(t *testing.T) {
	t.Run("kesik iş yükü listesi", func(t *testing.T) {
		f, _, ms := mapperFixture(t)
		ms.capped = true
		f.tick(t)
		if got := strings.Join(ms.take(), ","); got != "workloads" {
			t.Fatalf("kesik listede kenar okunmaz/yazılmaz: %s", got)
		}
		run := f.st.lastRun(t)
		if run.Status != rollout.RunPartial || !strings.Contains(run.Error, "eşleyici: iş yükü listesi") || !strings.Contains(run.Error, "mapper_workloads_capped=1") {
			t.Fatalf("koşu partial + not: %+v", run)
		}
		// Hatalı tur da bir sonraki denemeyi aralık sonraya atar; ama tur
		// arası her tik partial kalır (inceleme: yalnız hatalı tik partial
		// olsaydı sekme kapısı ~%90 açık, kenarlar ≤26 sa bayat kalırdı).
		for i := 0; i < 9; i++ {
			f.now = f.now.Add(time.Minute)
			f.tick(t)
			if got := ms.take(); len(got) != 0 {
				t.Fatalf("her tik yeniden denenmemeli: %v", got)
			}
			run := f.st.lastRun(t)
			if run.Status != rollout.RunPartial || !strings.Contains(run.Error, "eşleyici: son tur (") ||
				!strings.Contains(run.Error, "iş yükü listesi") || !strings.Contains(run.Error, "mapper_last_round_failed=1") {
				t.Fatalf("tur arası tik %d partial kalmalı: %+v", i+1, run)
			}
		}
		// Aralık dolunca başarılı tur notu temizler; sonraki tur arası tik ok.
		ms.capped = false
		f.now = f.now.Add(time.Minute)
		f.tick(t)
		if got := strings.Join(ms.take(), ","); got != "workloads,live,write" {
			t.Fatalf("aralık dolunca tur: %s", got)
		}
		if run := f.st.lastRun(t); run.Status != rollout.RunOK || strings.Contains(run.Error, "eşleyici:") {
			t.Fatalf("başarılı tur: %+v", run)
		}
		f.now = f.now.Add(time.Minute)
		f.tick(t)
		if run := f.st.lastRun(t); run.Status != rollout.RunOK || strings.Contains(run.Error, "mapper_last_round_failed") {
			t.Fatalf("başarılı turdan sonraki tik ok: %+v", run)
		}
	})
	t.Run("okuma hatası", func(t *testing.T) {
		f, _, ms := mapperFixture(t)
		ms.wlErr = errors.New("ch down")
		f.tick(t)
		if got := strings.Join(ms.take(), ","); got != "workloads" {
			t.Fatalf("hata: %s", got)
		}
		if run := f.st.lastRun(t); run.Status != rollout.RunPartial || !strings.Contains(run.Error, "ch down") {
			t.Fatalf("koşu: %+v", run)
		}
	})
	t.Run("hazır olmayan instance", func(t *testing.T) {
		f, hub, ms := mapperFixture(t)
		hub.partial = map[string]bool{"inventory": true}
		f.tick(t)
		if got := ms.take(); len(got) != 0 {
			t.Fatalf("envanteri alınmamış instance için tur yok: %v", got)
		}
		if run := f.st.lastRun(t); !strings.Contains(run.Error, "mapper_no_ready_instance=1") {
			t.Fatalf("teşhis: %s", run.Error)
		}
		// Uyarı kalkınca, envanter geri çekilmesi (2 × aralık) dolup ilk dolu
		// envanter alındığı tik tur koşar — mapperMin beklenmez.
		hub.partial = nil
		f.now = f.now.Add(3 * time.Minute)
		f.tick(t)
		if got := strings.Join(ms.take(), ","); got != "workloads,live,write" {
			t.Fatalf("ilk dolu envanterden sonra tur: %s", got)
		}
	})
	t.Run("parça tamam ama envanter eski (her tik partial)", func(t *testing.T) {
		f, _, ms := mapperFixture(t)
		f.tick(t)
		ms.take()
		// Tam envanter vadesi geldi ama geri çekilmede: parça hedefli
		// okumalarla tamam, lastInventory 3 × inventoryMin'i aşmış.
		m := f.w.mem["team-a-prod"]
		m.lastInventory = f.now.Add(-46 * time.Minute)
		m.invFails, m.invFailedAt = 5, f.now
		f.now = f.now.Add(time.Minute)
		f.tick(t)
		if got := ms.take(); len(got) != 0 {
			t.Fatalf("tur vadesi yok: %v", got)
		}
		run := f.st.lastRun(t)
		if run.Status != rollout.RunPartial || !strings.Contains(run.Error, "eşleyici: 1 instance hazır değil (team-a-prod") ||
			!strings.Contains(run.Error, "mapper_unready_instances=1") {
			t.Fatalf("hazır olmayan instance koşuyu partial yapmalı: %+v", run)
		}
	})
	t.Run("liderlik yazımdan önce kaybedildi", func(t *testing.T) {
		f, _, ms := mapperFixture(t)
		// Durum satırları yazıldıktan sonra, tur sırasında liderlik gider.
		ms.onRead = func() { f.lead = false }
		f.tick(t)
		if got := ms.take(); strings.Contains(strings.Join(got, ","), "write") {
			t.Fatalf("liderlik yokken yazım olmamalı: %v", got)
		}
	})
	t.Run("eşleyici bağlı değil", func(t *testing.T) {
		f, _, ms := mapperFixture(t)
		f.w.SetMapperStore(nil)
		f.tick(t)
		if got := ms.take(); len(got) != 0 || strings.Contains(f.st.lastRun(t).Error, "mapper_") {
			t.Fatalf("eşleyici yokken G/Ç ve teşhis yok: %v %s", got, f.st.lastRun(t).Error)
		}
	})
}

func TestReadyInstancesAndDigest(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	plans := []shardPlan{{Inst: Instance{ID: "ok"}}, {Inst: Instance{ID: "skip"}, Skip: "x"}, {Inst: Instance{ID: "stale"}},
		{Inst: Instance{ID: "noinv"}}, {Inst: Instance{ID: "lost"}}}
	mem := map[string]*shardMem{
		"ok":    {loaded: true, invDone: true, lastInventory: now.Add(-20 * time.Minute)},
		"skip":  {loaded: true, invDone: true, lastInventory: now},
		"stale": {loaded: true, invDone: true, lastInventory: now.Add(-46 * time.Minute)},
		"noinv": {loaded: true},
		"lost":  {loaded: true, invDone: true, lastInventory: now},
	}
	results := []shardResult{{}, {}, {}, {}, {lostLeader: true}}
	got := readyInstances(plans, results, mem, now, 15*time.Minute)
	if len(got) != 1 || !got["ok"] {
		t.Fatalf("yalnız 'ok' hazır: %v", got)
	}
	// v0.10.985 inceleme: parçası tamam ama hazır olmayan (eski envanter)
	// instance notu koşuyu partial yapar (sekme kapısı canlı yola düşer);
	// atlanan / hatalı parça kendi notunu taşır, teşhiste hepsi sayılır.
	okRes := []shardResult{{ok: true}, {hard: true}, {ok: true}, {}, {lostLeader: true}}
	note, n := mapperUnreadyNote(plans, okRes, got)
	if n != 4 || !strings.Contains(note, "1 instance hazır değil (stale:") {
		t.Fatalf("hazır olmayan: n=%d not=%q", n, note)
	}
	if note, n := mapperUnreadyNote(plans[:2], []shardResult{{ok: true}, {hard: true}}, got); note != "" || n != 1 {
		t.Fatalf("yalnız atlanan parça: n=%d not=%q", n, note)
	}
	a := Registry{ByServer: map[string]string{"u": "c"}, BySpan: map[string]string{"s": "c"}, Suffix: map[string]string{"c": "x"}}
	b := Registry{ByServer: map[string]string{"u": "c"}, BySpan: map[string]string{"s": "c"}, Suffix: map[string]string{"c": "y"}}
	// v0.10.988 — kararlılık iki ayrı çağrıyla (SA4000: aynı ifade iki yanda).
	da1, da2, db := registryDigest(a), registryDigest(a), registryDigest(b)
	if da1 == db || da1 != da2 {
		t.Fatal("suffix değişimi özeti değiştirmeli, özet kararlı olmalı")
	}
	ws, unm := mapperWorkloads([]WorkloadObs{{SpanCluster: "s", Namespace: "n", Kind: "Deployment", Workload: "w"}, {SpanCluster: "z"}, {SpanCluster: "z"}}, a.BySpan)
	if len(ws) != 1 || ws[0].ClusterID != "c" || unm != 1 {
		t.Fatalf("span → EffectiveID: %+v %d", ws, unm)
	}
}
