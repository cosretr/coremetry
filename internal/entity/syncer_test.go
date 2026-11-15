package entity

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// v0.10.129 — senkronizasyon: sahte Thanos + bellek-içi store ile.
// Görev testleri: kısmi senkronizasyon (bazı cluster'lar yanıt vermezken
// tutarlılık), kesinti/resync, eşlenemeyen cluster değeri sayaçlanır,
// silme YOK (yaşlandırma).

type fakeSource struct {
	mu       sync.Mutex
	clusters []ClusterRef
	sets     map[string]SampleSets // cid → yanıt
	errs     map[string]error      // cid → hata
	calls    map[string]int
}

func (f *fakeSource) Clusters() []ClusterRef { return f.clusters }
func (f *fakeSource) Fetch(ctx context.Context, c ClusterRef, queries map[string]string) (SampleSets, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[c.ID]++
	if err := f.errs[c.ID]; err != nil {
		return SampleSets{}, err
	}
	return f.sets[c.ID], nil
}

type memStore struct {
	mu   sync.Mutex
	open map[string]map[string]Lifetime // cid → id → açık ömür
	ents map[string]map[string]Entity   // cid → id → açık satırın alanları
	rows []EntityRow
	rels []RelationRow
	runs []Run
}

func newMemStore() *memStore {
	return &memStore{open: map[string]map[string]Lifetime{}, ents: map[string]map[string]Entity{}}
}
func (m *memStore) OpenLifetimes(ctx context.Context, cid string) (OpenSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := OpenSet{Lifetimes: map[string]Lifetime{}, Entities: map[string]Entity{}, Relations: map[string]Relation{}}
	for k, v := range m.open[cid] {
		out.Lifetimes[k] = v
		out.RowsScanned++
		if e, ok := m.ents[cid][k]; ok {
			out.Entities[k] = e
		}
		if typ, p, c, ok := parseRelKey(k); ok {
			out.Relations[k] = Relation{Type: typ, ClusterID: cid, ParentID: p, ChildID: c, Source: SourceThanos}
		}
	}
	return out, nil
}
func (m *memStore) Apply(ctx context.Context, cid string, rows []EntityRow, rels []RelationRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open[cid] == nil {
		m.open[cid] = map[string]Lifetime{}
		m.ents[cid] = map[string]Entity{}
	}
	// RMT dedup anahtarı (id, valid_from): kapanış yalnız AYNI ömrü kapatır —
	// aynı tick'te kapat+aç gelirse yeni ömür açık kalır.
	for _, r := range rows {
		m.rows = append(m.rows, r)
		key := r.ID
		if r.ValidTo.IsZero() {
			m.open[cid][key] = Lifetime{ID: r.ID, UID: r.UID, ValidFrom: r.ValidFrom, LastSeen: r.LastSeen}
			m.ents[cid][key] = Entity{Type: r.Type, ClusterID: cid, ID: r.ID, Namespace: r.Namespace, Name: r.Name, UID: r.UID, ParentID: r.ParentID, Source: r.Source}
		} else if cur, ok := m.open[cid][key]; ok && cur.ValidFrom.Equal(r.ValidFrom) {
			delete(m.open[cid], key)
		}
	}
	for _, r := range rels {
		m.rels = append(m.rels, r)
		key := relKey(r.Type, r.ParentID, r.ChildID)
		if r.ValidTo.IsZero() {
			m.open[cid][key] = Lifetime{ID: key, ValidFrom: r.ValidFrom, LastSeen: r.LastSeen}
		} else if cur, ok := m.open[cid][key]; ok && cur.ValidFrom.Equal(r.ValidFrom) {
			delete(m.open[cid], key)
		}
	}
	return nil
}
func (m *memStore) RecordRun(ctx context.Context, run Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runs = append(m.runs, run)
	return nil
}

func podSet(pods ...string) SampleSets {
	var ss SampleSets
	ss.NodeInfo = []Sample{{Labels: map[string]string{"node": "w1"}}}
	for _, p := range pods {
		ss.PodInfo = append(ss.PodInfo, Sample{Labels: map[string]string{"namespace": "ns", "pod": p, "uid": "u-" + p, "node": "w1"}})
		ss.PodOwner = append(ss.PodOwner, Sample{Labels: map[string]string{"namespace": "ns", "pod": p, "owner_kind": "StatefulSet", "owner_name": "db"}})
	}
	return ss
}

func TestSyncerPartialFailureAndLifecycle(t *testing.T) {
	src := &fakeSource{
		clusters: []ClusterRef{{ID: "c-a", Name: "a"}, {ID: "c-b", Name: "b"}},
		sets:     map[string]SampleSets{"c-a": podSet("db-0", "db-1"), "c-b": podSet("db-0")},
		errs:     map[string]error{"c-b": errors.New("thanos b: HTTP 503")},
	}
	st := newMemStore()
	t0 := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return t0 }

	// Tick 1: a başarılı, b hatalı.
	sy.Tick(context.Background())
	if len(st.runs) != 2 {
		t.Fatalf("iki cluster iki koşu kaydı, alınan %d", len(st.runs))
	}
	byCid := map[string]Run{}
	for _, r := range st.runs {
		byCid[r.ClusterID] = r
	}
	if byCid["c-a"].Status != RunOK || byCid["c-a"].EntitiesWritten == 0 {
		t.Fatalf("a: %+v", byCid["c-a"])
	}
	if byCid["c-b"].Status != RunFailed || !strings.Contains(byCid["c-b"].Error, "503") || byCid["c-b"].EntitiesWritten != 0 {
		t.Fatalf("b hatalı kaydedilmeli, yazım 0: %+v", byCid["c-b"])
	}
	wl := WorkloadID("c-a", "ns", "StatefulSet", "db")
	if _, ok := st.open["c-a"][wl]; !ok {
		t.Fatal("a'nın iş yükü kalıcı ömür olmalı")
	}
	if len(st.open["c-b"]) != 0 {
		t.Fatal("b'ye hiçbir şey yazılmamalı")
	}
	obs := sy.Observability()
	if obs.Ticks != 1 || obs.ClustersOK != 1 || obs.ClustersFailed != 1 {
		t.Fatalf("obs: %+v", obs)
	}

	// Tick 2 (2 dk sonra): a'da db-1 gitti → pod satırı olmadığından yazım yok;
	// b hâlâ hatalı → dokunulmaz.
	n := len(st.rows) + len(st.rels)
	t1 := t0.Add(2 * time.Minute)
	sy.now = func() time.Time { return t1 }
	src.sets["c-a"] = podSet("db-0")
	sy.Tick(context.Background())
	if got := len(st.rows) + len(st.rels); got != n {
		t.Fatalf("pod gidişi CH'ye yazım üretmemeli: %d → %d", n, got)
	}

	// Tick 3: b toparlandı → yazılır; a 15 dk sustu ve geri geldi → kalıcı
	// ömür YENİDEN AÇILMAZ (sessizlik yeniden doğuş değildir).
	t2 := t1.Add(15 * time.Minute)
	sy.now = func() time.Time { return t2 }
	delete(src.errs, "c-b")
	sy.Tick(context.Background())
	if len(st.open["c-b"]) == 0 {
		t.Fatal("toparlanan cluster yazılmalı")
	}
	if lt := st.open["c-a"][wl]; !lt.ValidFrom.Equal(t0) {
		t.Fatalf("sessizlik ömrü kapatıp yeniden açmamalı: %+v", lt)
	}
}

// E1 — CH'ye yalnız kalıcı katman: pod/container satırı ve pod uçlu kenar
// (wl→pod, runs, runs_on) YAZILMAZ; pod'lar canlı dizinde.
func TestSyncerPersistsStableOnly(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a", SpanClusterValue: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0", "db-1")}}
	st := newMemStore()
	kv := newMemKV()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	sy.SetLive(kv)
	sy.now = func() time.Time { return t0 }
	sy.seen = fakeSeen{rows: []SeenRow{{ClusterValue: "a", Namespace: "ns", Pod: "db-0", Node: "w1", Service: "db", Spans: 5, LastSeen: t0}}}
	sy.Tick(context.Background())
	types := map[string]bool{}
	for _, r := range st.rows {
		types[r.Type] = true
		if !IsStableType(r.Type) {
			t.Fatalf("kalıcı olmayan satır yazıldı: %+v", r)
		}
	}
	for _, want := range []string{TypeCluster, TypeNode, TypeNamespace, TypeWorkload, TypeService} {
		if !types[want] {
			t.Errorf("%s satırı yazılmalı: %v", want, types)
		}
	}
	for _, r := range st.rels {
		if r.Type != RelParent || strings.HasPrefix(r.ChildID, "pod:") || strings.HasPrefix(r.ParentID, "pod:") {
			t.Fatalf("pod uçlu kenar yazıldı: %+v", r)
		}
	}
	raw, ok := kv.m[LiveKey("c-a")]
	if !ok || kv.ttl[LiveKey("c-a")] != LiveTTL {
		t.Fatalf("canlı dizin blob'u TTL=LiveTTL ile yazılmalı: %v", kv.ttl)
	}
	snap, err := DecodeLive(raw)
	if err != nil || len(snap.Pods) != 2 {
		t.Fatalf("blob iki pod taşımalı: %+v %v", snap, err)
	}
	p := snap.Pods[0]
	if p.Name != "db-0" || p.Workload != WorkloadID("c-a", "ns", "StatefulSet", "db") || p.Node != "w1" ||
		len(p.Services) != 1 || p.Services[0].Name != "db" || p.ClusterValue != "a" {
		t.Fatalf("pod kaydı: %+v", p)
	}
}

// Kapanış yalnız art arda BAŞARILI görüntülerde ≥ PodGap süren yoklukta;
// hatalı tick yokluğu ne kanıtlar ne sıfırlar.
func TestSyncerClosesStableOnlyAfterConsecutiveAbsence(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0")}}
	st := newMemStore()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	at := t0
	sy.now = func() time.Time { return at }
	sy.Tick(context.Background())
	wl := WorkloadID("c-a", "ns", "StatefulSet", "db")
	// İş yükü kalktı (yalnız node kaldı).
	src.sets["c-a"] = SampleSets{NodeInfo: []Sample{{Labels: map[string]string{"node": "w1"}}}}
	for m := 1; m <= 10; m++ {
		at = t0.Add(time.Duration(m) * time.Minute)
		sy.Tick(context.Background())
		if _, open := st.open["c-a"][wl]; !open {
			t.Fatalf("%d. dk: PodGap dolmadan kapanmamalı", m)
		}
	}
	at = t0.Add(11 * time.Minute)
	sy.Tick(context.Background())
	if _, open := st.open["c-a"][wl]; open {
		t.Fatal("art arda ≥ PodGap yokluktan sonra kapanmalı")
	}
	for _, r := range st.rows {
		if r.ID == wl && !r.ValidTo.IsZero() {
			if !r.ValidTo.Equal(t0) || r.Name != "db" || r.Namespace != "ns" {
				t.Fatalf("kapanış son görülmede, CH'deki alanlarla: %+v", r)
			}
		}
	}
}

// Degraded kilit (Redis yok → Noop kilit, her replika lider): tick yazmaz.
func TestSyncerSkipsWithoutLock(t *testing.T) {
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0")}}
	st := newMemStore()
	kv := newMemKV()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	sy.SetLive(kv)
	sy.SetLockDegraded(func() bool { return true })
	sy.Tick(context.Background())
	if len(st.rows)+len(st.rels)+len(st.runs) != 0 || len(kv.m) != 0 || src.calls["c-a"] != 0 {
		t.Fatalf("kilitsiz tick hiçbir şey yazmamalı: rows=%d runs=%d kv=%d", len(st.rows), len(st.runs), len(kv.m))
	}
	if sy.noLockN.Load() != 1 {
		t.Fatal("atlanan tick sayılmalı")
	}
}

type memKV struct {
	mu  sync.Mutex
	m   map[string][]byte
	ttl map[string]time.Duration
	err error
	get int
}

func newMemKV() *memKV { return &memKV{m: map[string][]byte{}, ttl: map[string]time.Duration{}} }
func (k *memKV) Get(_ context.Context, key string) ([]byte, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.get++
	if k.err != nil {
		return nil, false, k.err
	}
	v, ok := k.m[key]
	return v, ok, nil
}
func (k *memKV) Set(_ context.Context, key string, v []byte, ttl time.Duration) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key], k.ttl[key] = v, ttl
	return nil
}

// Cluster kaldırıldı (Settings'ten silindi): kayıtları SİLİNMEZ; yalnız
// artık sync edilmez. Yeni cluster eklendi: sonraki tick'te alınır
// (restart yok — Clusters() her tick okunur).
func TestSyncerClusterAddRemoveWithoutRestart(t *testing.T) {
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("p1"), "c-n": podSet("q1")}}
	st := newMemStore()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	t0 := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	sy.now = func() time.Time { return t0 }
	sy.Tick(context.Background())
	src.clusters = []ClusterRef{{ID: "c-n", Name: "n"}} // a kaldırıldı, n eklendi
	sy.now = func() time.Time { return t0.Add(time.Minute) }
	sy.Tick(context.Background())
	if len(st.open["c-a"]) == 0 {
		t.Fatal("kaldırılan cluster'ın kayıtları korunmalı (silme yok)")
	}
	if len(st.open["c-n"]) == 0 {
		t.Fatal("yeni cluster restart'sız alınmalı")
	}
	ids := []string{}
	for _, r := range st.runs {
		ids = append(ids, r.ClusterID)
	}
	sort.Strings(ids)
	if strings.Join(ids, ",") != "c-a,c-n" {
		t.Fatalf("koşular: %v", ids)
	}
}

// Satır üretimi: tam-satır replace — her satır TÜM alanları taşır
// (invariant #4), etiketler Array çifti; kapanış satırı CH'deki alanlarla.
func TestRowsForChange(t *testing.T) {
	t0 := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	wl := func(n string) string { return WorkloadID("c", "ns", "Deployment", n) }
	ents := map[string]Entity{
		wl("a"): {Type: TypeWorkload, ClusterID: "c", ID: wl("a"), Namespace: "ns", Name: "a", ParentID: "ns:c/ns",
			Labels: map[string]string{"kind": "Deployment", "tier": "db"}, Source: SourceThanos},
	}
	ch := Change{
		Open:    []Lifetime{{ID: wl("a"), ValidFrom: t0, LastSeen: t0}},
		Close:   []Lifetime{{ID: wl("gone"), ValidFrom: t0.Add(-time.Hour), ValidTo: t0.Add(-time.Minute), LastSeen: t0.Add(-time.Minute)}},
		Refresh: []Lifetime{{ID: wl("b"), ValidFrom: t0.Add(-time.Hour), LastSeen: t0}},
	}
	prev := map[string]Entity{
		wl("gone"): {Type: TypeWorkload, ClusterID: "c", ID: wl("gone"), Namespace: "ns", Name: "gone", Source: SourceThanos},
		wl("b"):    {Type: TypeWorkload, ClusterID: "c", ID: wl("b"), Namespace: "ns", Name: "b", Source: SourceThanos},
	}
	rows := RowsForChange("c", ch, ents, prev)
	if len(rows) != 3 {
		t.Fatalf("üç satır beklenir, alınan %d", len(rows))
	}
	byID := map[string]EntityRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	a := byID[wl("a")]
	if a.Type != TypeWorkload || a.ClusterID != "c" || a.Namespace != "ns" || a.Name != "a" || a.ParentID == "" || a.Source != SourceThanos {
		t.Fatalf("tam satır: %+v", a)
	}
	if len(a.LabelKeys) != 2 || a.LabelKeys[0] != "kind" || a.LabelValues[0] != "Deployment" {
		t.Fatalf("etiketler sıralı Array çifti: %+v", a)
	}
	if !a.ValidTo.IsZero() || !a.FirstSeen.Equal(t0) || !a.LastSeen.Equal(t0) {
		t.Fatalf("açık ömür: %+v", a)
	}
	g := byID[wl("gone")]
	if g.ValidTo.IsZero() || g.Name != "gone" {
		t.Fatalf("kapanış satırı valid_to + tam alanlar taşımalı: %+v", g)
	}
	b := byID[wl("b")]
	if !b.LastSeen.Equal(t0) || !b.ValidFrom.Equal(t0.Add(-time.Hour)) || b.Name != "b" {
		t.Fatalf("tazeleme valid_from korur, last_seen now, alanlar CH'den: %+v", b)
	}
}
