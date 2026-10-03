package api

// v0.10.1081 — Problems varsayılanı yalnız P1, ilk görülmeye göre (en yeni
// önce). Operatör: "Problems sayfasında sadece P1'ler gözüksün ve first
// seen'e göre sıralı olsun".
//
// Sayfa sunucudan prio=P1&sort=firstSeen&dir=desc ister ve liste sunucuda
// tavanlanır ("ilk 300 …"). Sözleşme: öncelik süzgeci tavandan ÖNCE uygulanır
// ve tavan ilk görülmeye göre keser — en yeni P1'ler asla kırpılmaz; P2/P3
// satırları (P1'lerden yeni olsalar da) yer kaplamaz; çip sayıları süzgeçten
// önceki kümeyi sayar. Handler'ın sırası (forceNonExceptionP3 → sayaçlar →
// facet'ler → inboxSortAndCap) CH'siz, gerçek işlevlerle.

import (
	"strings"
	"testing"
)

func TestInboxP1FirstSeenDefaultPipeline(t *testing.T) {
	const minute = int64(60e9)
	base := int64(1_790_000_000e9)
	// 6 P1 exception (ilk görülme 1..6 dk önce sırasıyla değil, karışık),
	// 5 P3 exception — P3'lerin hepsi P1'lerden YENİ.
	var items []InboxItem
	p1Ages := []int64{30, 5, 50, 10, 40, 20} // dakika önce
	for i, age := range p1Ages {
		items = append(items, InboxItem{
			ID: "exception:p1-" + string(rune('a'+i)), Kind: "exception", Priority: "P1",
			StartedAt: base - age*minute, LastSeen: base,
		})
	}
	for i := 0; i < 5; i++ {
		items = append(items, InboxItem{
			ID: "exception:p3-" + string(rune('a'+i)), Kind: "exception", Priority: "P3",
			StartedAt: base - int64(i+1)*minute, LastSeen: base,
		})
	}

	sortID, sortDir := normalizeInboxSort("firstSeen", "desc")
	if sortID != "firstSeen" || sortDir != "desc" {
		t.Fatalf("sunucu firstSeen/desc'i tanımalı, %s/%s döndü", sortID, sortDir)
	}
	prios := normalizeInboxSet("P1", inboxPriosAll)

	forceNonExceptionP3(items)
	counts := inboxFacetCounts(items)
	items = applyInboxFacets(items, inboxKindsAll, prios)
	page, total := inboxSortAndCap(items, sortID, sortDir, 4)

	if counts["P1"] != 6 || counts["P3"] != 5 {
		t.Fatalf("çip sayıları süzgeçten önceki kümeyi saymalı: P1=%d P3=%d", counts["P1"], counts["P3"])
	}
	if total != 6 {
		t.Fatalf("total = %d, yalnız P1'ler (6) — P3 tavandan önce düşmeli", total)
	}
	var got []string
	for _, it := range page {
		if it.Priority != "P1" {
			t.Fatalf("sayfada %s satırı var — öncelik süzgeci tavandan önce uygulanmalı", it.Priority)
		}
		got = append(got, it.ID)
	}
	// En yeni dört P1: 5, 10, 20, 30 dk önce → b, d, f, a.
	want := []string{"exception:p1-b", "exception:p1-d", "exception:p1-f", "exception:p1-a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("sayfa = %v, want %v (ilk görülme azalan; en eski P1'ler kırpılır)", got, want)
	}
}

// Açık parametreler aynen geçerli: eski linkler (öncelik sırası, tüm
// öncelikler) bugünkü gibi çalışır; tanınmayan sıralama varsayılana (priority)
// düşer — 400 değil.
func TestInboxExplicitSortParamsRespected(t *testing.T) {
	if id, dir := normalizeInboxSort("priority", "desc"); id != "priority" || dir != "desc" {
		t.Fatalf("priority/desc = %s/%s", id, dir)
	}
	if id, dir := normalizeInboxSort("firstSeen", "asc"); id != "firstSeen" || dir != "asc" {
		t.Fatalf("firstSeen/asc = %s/%s", id, dir)
	}
	if id, _ := normalizeInboxSort("first_seen", "desc"); id != inboxSortDefault {
		t.Fatalf("tanınmayan kimlik varsayılana düşmeli, %s döndü", id)
	}
	all := normalizeInboxSet("P1,P2,P3", inboxPriosAll)
	if len(all) != 3 {
		t.Fatalf("açık P1,P2,P3 = %v", all)
	}
	// Farklı sıralama/öncelik farklı önbellek anahtarı (hash-all-inputs).
	k1 := inboxListKey("open", "", "", "", "", "", "", 300, "firstSeen", "desc", 5, inboxKindsAll, []string{"P1"}, "service")
	k2 := inboxListKey("open", "", "", "", "", "", "", 300, "priority", "desc", 5, inboxKindsAll, []string{"P1"}, "service")
	k3 := inboxListKey("open", "", "", "", "", "", "", 300, "firstSeen", "desc", 5, inboxKindsAll, inboxPriosAll, "service")
	if k1 == k2 || k1 == k3 {
		t.Fatal("sıralama ve öncelik önbellek anahtarına girmeli")
	}
}
