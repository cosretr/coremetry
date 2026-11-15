package entity

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// v0.10.2214 (S1) — yazım diyeti: açık ömrün last_seen'i yalnız saklı değer
// RefreshEvery'den eskiyse yazılır; karar CH'den okunan prev'e dayanır.
func TestThrottleRefreshTable(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	prev := map[string]Lifetime{
		"fresh":   {ID: "fresh", UID: "u", LastSeen: t0.Add(-time.Minute)},
		"edge":    {ID: "edge", UID: "u", LastSeen: t0.Add(-RefreshEvery)},
		"old":     {ID: "old", UID: "u", LastSeen: t0.Add(-RefreshEvery - time.Second)},
		"uidfill": {ID: "uidfill", LastSeen: t0.Add(-time.Minute)},
	}
	refresh := []Lifetime{
		{ID: "edge", UID: "u", LastSeen: t0},
		{ID: "fresh", UID: "u", LastSeen: t0},
		{ID: "old", UID: "u", LastSeen: t0},
		{ID: "uidfill", UID: "u2", LastSeen: t0},
	}
	keep, skipped := ThrottleRefresh(t0, prev, refresh, RefreshEvery)
	ids := []string{}
	for _, l := range keep {
		ids = append(ids, l.ID)
	}
	if !reflect.DeepEqual(ids, []string{"old", "uidfill"}) || skipped != 2 {
		t.Fatalf("keep=%v skipped=%d", ids, skipped)
	}
}

func cloneStore(m *memStore) *memStore {
	c := newMemStore()
	for cid, ls := range m.open {
		c.open[cid] = map[string]Lifetime{}
		c.ents[cid] = map[string]Entity{}
		for k, v := range ls {
			c.open[cid][k] = v
		}
		for k, v := range m.ents[cid] {
			c.ents[cid][k] = v
		}
	}
	return c
}

// v0.10.2225 — RefreshEvery = 24 saat: kalıcı satır günde bir tazelenir
// (TTL last_seen + 180 g). Aritmetik: 60 sn tick, 24 saat = 1440 tick'te
// tek tazeleme; saklı last_seen'den tam 24 saat sonra DEĞİL, aşınca.
func TestRefreshEvery24h(t *testing.T) {
	if RefreshEvery != 24*time.Hour {
		t.Fatalf("RefreshEvery 24 saat olmalı: %v", RefreshEvery)
	}
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	prev := map[string]Lifetime{"x": {ID: "x", UID: "u", LastSeen: t0}}
	writes := 0
	for tick := 1; tick <= 1441; tick++ {
		now := t0.Add(time.Duration(tick) * SyncInterval)
		keep, _ := ThrottleRefresh(now, prev, []Lifetime{{ID: "x", UID: "u", LastSeen: now}}, RefreshEvery)
		if len(keep) > 0 {
			writes++
			if tick != 1441 {
				t.Fatalf("tazeleme %d. tick'te (24 saat dolmadan)", tick)
			}
		}
	}
	if writes != 1 {
		t.Fatalf("24 saatte tek yazım beklenir: %d", writes)
	}
}

// Tick dizisi: ilk tick açar; RefreshEvery içinde tazeleme yazılmaz; aştığında
// bir kez yazılır. Lider devri: yeni Syncer (boş bellek) aynı store'dan aynı
// kararı verir — aynı girdi → aynı satırlar.
func TestSyncerRefreshThrottleAndLeaderHandover(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0", "db-1")}}
	st := newMemStore()
	a := NewSyncer(src, st, func() Resolved { return Resolved{} })
	a.now = func() time.Time { return t0 }
	a.Tick(context.Background())
	opened := len(st.rows) + len(st.rels)
	if opened == 0 {
		t.Fatal("ilk tick açmalı")
	}
	for h := 1; h <= 23; h++ {
		at := t0.Add(time.Duration(h) * time.Hour)
		a.now = func() time.Time { return at }
		a.Tick(context.Background())
	}
	if got := len(st.rows) + len(st.rels); got != opened {
		t.Fatalf("RefreshEvery içinde yazım olmamalı: %d → %d", opened, got)
	}
	if a.Observability().RefreshSkipped == 0 {
		t.Fatal("kısılan tazelemeler sayılmalı")
	}

	// t0+24h+1m: A ile devam eden dünya vs B'ye devredilen dünya — aynı satırlar.
	t6 := t0.Add(24*time.Hour + time.Minute)
	stB := cloneStore(st)
	nA := len(st.rows)
	a.now = func() time.Time { return t6 }
	a.Tick(context.Background())
	b := NewSyncer(src, stB, func() Resolved { return Resolved{} })
	b.now = func() time.Time { return t6 }
	b.Tick(context.Background())
	rowsA, rowsB := st.rows[nA:], stB.rows
	if len(rowsA) == 0 || !reflect.DeepEqual(rowsA, rowsB) {
		t.Fatalf("lider devrinde aynı satırlar beklenir:\nA=%+v\nB=%+v", rowsA, rowsB)
	}
	if rowsA[0].LastSeen != t6 {
		t.Fatalf("tazeleme last_seen=now yazmalı: %+v", rowsA[0])
	}
}

// K2 — kaydı olmayan span cluster değeri span-only cluster entity'si olur;
// Thanos sorgusu yapılmaz; boş değer ve kayıt id'siyle çakışan değer atlanır.
func TestSpanOnlyClusterEntities(t *testing.T) {
	t0 := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	src := &fakeSource{clusters: []ClusterRef{{ID: "c-a", Name: "a"}}, sets: map[string]SampleSets{"c-a": podSet("db-0")}}
	st := newMemStore()
	sy := NewSyncer(src, st, func() Resolved { return Resolved{} })
	sy.now = func() time.Time { return t0 }
	sy.seen = fakeSeen{rows: []SeenRow{
		{ClusterValue: "edge-b", Namespace: "ns", Pod: "web-6f7c9-abcde", Service: "web", Spans: 3, LastSeen: t0},
		{ClusterValue: "", Namespace: "ns", Pod: "x", Service: "x", Spans: 1, LastSeen: t0},
		{ClusterValue: "c-a", Namespace: "ns", Pod: "y", Service: "y", Spans: 1, LastSeen: t0},
	}}
	sy.Tick(context.Background())
	if _, ok := st.open["edge-b"][ClusterID("edge-b")]; !ok {
		t.Fatalf("span-only cluster entity açılmalı: %v", st.open["edge-b"])
	}
	if _, ok := st.open["edge-b"][PodID("edge-b", "ns", "web-6f7c9-abcde")]; ok {
		t.Fatal("pod satırı yazılmamalı (canlı dizinde)")
	}
	if _, ok := st.open["edge-b"][WorkloadID("edge-b", "ns", "Deployment", "web")]; !ok {
		t.Fatal("span-only cluster'ın türetilmiş iş yükü açılmalı")
	}
	if src.calls["edge-b"] != 0 {
		t.Fatal("span-only cluster için Thanos sorgusu olmamalı")
	}
	if len(st.open[""]) != 0 {
		t.Fatal("boş değer cluster olmamalı")
	}
	for _, r := range st.rows {
		if r.ID == ClusterID("edge-b") && r.Source != SourceSpan {
			t.Fatalf("source=span: %+v", r)
		}
	}

	got := SpanOnlyClusters(map[string]int{"b": 2, "a": 2, "z": 9, "": 5, "c-a": 3}, []ClusterRef{{ID: "c-a"}}, 2)
	if len(got) != 2 || got[0].ID != "z" || got[1].ID != "a" || !got[0].SpanOnly {
		t.Fatalf("tavan + sıralama: %+v", got)
	}
}
