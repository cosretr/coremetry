package api

// inbox_priority_oldest_test.go — v0.10.2122, Problems sadeleştirme S2
// (docs/frontend/problems-page-plan.md; mockup operatör onaylı): varsayılan
// sıra öncelik (P1 önce), aynı öncelikte EN ESKİ önce; üst sayım cümlesinin
// "en eskisi" sayaçlarla aynı kümeden, tavandan önce.

import (
	"strings"
	"testing"
)

func TestInboxPrioritySortOldestFirst(t *testing.T) {
	const min = int64(60e9)
	base := int64(1_790_000_000e9)
	mk := func(id, prio string, ageMin int64, lastSeenAgo int64) InboxItem {
		return InboxItem{ID: id, Kind: "exception", Priority: prio, StartedAt: base - ageMin*min, LastSeen: base - lastSeenAgo*min}
	}
	items := []InboxItem{
		mk("p3-old", "P3", 900, 0),
		mk("p1-new", "P1", 5, 0),   // son görülme en yeni ama başlangıcı en yeni
		mk("p1-old", "P1", 300, 9), // en eski P1 → en üstte
		mk("p2-mid", "P2", 60, 0),
		mk("p1-mid", "P1", 60, 1),
	}
	id, dir := normalizeInboxSort("priority", "desc")
	page, total := inboxSortAndCap(items, id, dir, 4)
	var got []string
	for _, it := range page {
		got = append(got, it.ID)
	}
	want := "p1-old,p1-mid,p1-new,p2-mid"
	if strings.Join(got, ",") != want || total != 5 {
		t.Fatalf("sıra = %v (total %d), istenen %s — öncelik önce, aynı öncelikte en eski önce", got, total, want)
	}

	// Başlangıcı bilinmeyen (0) satır eşitlik kırıcıda eski sayılmaz:
	// son görülme sırasına düşer.
	a := InboxItem{ID: "a", Priority: "P1", StartedAt: 0, LastSeen: base}
	b := InboxItem{ID: "b", Priority: "P1", StartedAt: base - min, LastSeen: base - min}
	two := []InboxItem{b, a}
	sortInboxItems(two, "priority", "desc")
	if two[0].ID != "a" {
		t.Fatalf("başlangıcı bilinmeyen satır: %v", []string{two[0].ID, two[1].ID})
	}
}

func TestInboxOldestFirstSeen(t *testing.T) {
	if got := inboxOldestFirstSeen(nil); got != 0 {
		t.Fatalf("boş küme = %d", got)
	}
	items := []InboxItem{{StartedAt: 0}, {StartedAt: 50}, {StartedAt: 20}, {StartedAt: 90}}
	if got := inboxOldestFirstSeen(items); got != 20 {
		t.Fatalf("en eski = %d, istenen 20 (0 = bilinmiyor, sayılmaz)", got)
	}
}
