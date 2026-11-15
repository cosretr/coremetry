package entity

import (
	"testing"
	"time"
)

// v0.10.129 — zaman geçerliliği: ömür farkı (design §1.3). v0.10.2225
// (entity-storage-cost-note C): yalnız kalıcı katman; sessizlik kuralı yok.
//
// Sözleşme (DiffLifetimes):
//   - görülen & açık ömür yok                         → AÇ (valid_from = now)
//   - görülen & uid farklı (ikisi de dolu)            → KAPAT eskiyi + AÇ yeni
//   - görülen & açık (last_seen ne kadar eski olursa olsun) → TAZELE
//   - görülmeyen açık ömür: yokluk başlar; art arda başarılı görüntülerde
//     ≥ closeAfter sürerse KAPAT (valid_to = yokluktan önceki son görülme)
//   - hiçbir şey SİLİNMEZ.
func TestDiffLifetimes(t *testing.T) {
	t0 := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	gap := 10 * time.Minute
	wl := func(n string) string { return WorkloadID("c", "ns", "Deployment", n) }
	open := func(id, uid string, from, last time.Time) Lifetime {
		return Lifetime{ID: id, UID: uid, ValidFrom: from, LastSeen: last}
	}
	prev := map[string]Lifetime{
		wl("a"): open(wl("a"), "u-a", t0.Add(-time.Hour), t0.Add(-time.Minute)),  // aynı uid → tazele
		wl("b"): open(wl("b"), "u-b1", t0.Add(-time.Hour), t0.Add(-time.Minute)), // uid değişti → kapat+aç
		wl("c"): open(wl("c"), "", t0.Add(-48*time.Hour), t0.Add(-30*time.Hour)), // syncer uzun sustu → yine TAZELE
		wl("d"): open(wl("d"), "u-d", t0.Add(-time.Hour), t0.Add(-time.Minute)),  // yok, yokluk yeni başlıyor
		wl("e"): open(wl("e"), "", t0.Add(-time.Hour), t0.Add(-20*time.Minute)),  // yok, yokluk ≥ gap
	}
	seen := map[string]Entity{
		wl("a"): {ID: wl("a"), UID: "u-a"},
		wl("b"): {ID: wl("b"), UID: "u-b2"},
		wl("c"): {ID: wl("c")},
		wl("n"): {ID: wl("n"), UID: "u-n"},
	}
	absent := map[string]Absence{wl("e"): {Since: t0.Add(-gap), LastSeen: t0.Add(-11 * time.Minute)}}
	ch, next := DiffLifetimes(t0, prev, seen, absent, t0.Add(-time.Minute), gap)
	ids := func(ls []Lifetime) map[string]Lifetime {
		m := map[string]Lifetime{}
		for _, l := range ls {
			m[l.ID] = l
		}
		return m
	}
	opened, closed, refreshed := ids(ch.Open), ids(ch.Close), ids(ch.Refresh)
	for _, id := range []string{wl("a"), wl("c")} {
		l, ok := refreshed[id]
		if !ok || !l.LastSeen.Equal(t0) || !l.ValidFrom.Equal(prev[id].ValidFrom) {
			t.Errorf("%s tazelenmeli (sessizlik yeniden açmaz): %+v", id, l)
		}
	}
	if c, ok := closed[wl("b")]; !ok || !c.ValidTo.Equal(prev[wl("b")].LastSeen) {
		t.Errorf("uid değişimi eski ömrü kapatır: %+v", c)
	}
	if o := opened[wl("b")]; !o.ValidFrom.Equal(t0) || o.UID != "u-b2" {
		t.Errorf("uid değişimi yeni ömür açar: %+v", o)
	}
	if _, ok := closed[wl("d")]; ok {
		t.Error("ilk yokluk gözleminde kapanmamalı")
	}
	if a := next[wl("d")]; !a.Since.Equal(t0) || !a.LastSeen.Equal(t0.Add(-time.Minute)) {
		t.Errorf("yokluk now'da başlar, son görülme = max(last_seen, lastOK): %+v", a)
	}
	if c, ok := closed[wl("e")]; !ok || !c.ValidTo.Equal(t0.Add(-11*time.Minute)) || !c.LastSeen.Equal(c.ValidTo) {
		t.Errorf("≥ gap yokluk kapanır, valid_to = yokluktan önceki son görülme: %+v", c)
	}
	if _, carried := next[wl("e")]; carried {
		t.Error("kapanan ömrün yokluğu taşınmaz")
	}
	if o, ok := opened[wl("n")]; !ok || !o.ValidFrom.Equal(t0) {
		t.Errorf("yeni varlık açılmalı: %+v", o)
	}
	if len(ch.Open) != 2 || len(ch.Close) != 2 || len(ch.Refresh) != 2 || len(next) != 1 {
		t.Fatalf("sayılar: open=%d close=%d refresh=%d absent=%d", len(ch.Open), len(ch.Close), len(ch.Refresh), len(next))
	}
	// Geri gelen varlığın yokluğu silinir.
	_, next2 := DiffLifetimes(t0.Add(time.Minute), prev, map[string]Entity{wl("d"): {ID: wl("d"), UID: "u-d"}}, next, t0, gap)
	if _, still := next2[wl("d")]; still {
		t.Error("yeniden görülen varlığın yokluğu sıfırlanmalı")
	}
}

// Aynı namespace + ad iki cluster'da: iki ayrı ömür, birbirine karışmaz.
func TestDiffLifetimesTwoClustersSameName(t *testing.T) {
	t0 := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	seen := map[string]Entity{
		NamespaceID("c-1", "ns"): {ID: NamespaceID("c-1", "ns")},
		NamespaceID("c-2", "ns"): {ID: NamespaceID("c-2", "ns")},
	}
	ch, _ := DiffLifetimes(t0, nil, seen, nil, time.Time{}, 10*time.Minute)
	if len(ch.Open) != 2 || ch.Open[0].ID == ch.Open[1].ID {
		t.Fatalf("iki cluster iki ömür: %+v", ch.Open)
	}
}
