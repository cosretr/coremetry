package api

// inbox_graph_fold_test.go — v0.10.2341 (entity grafı S4): kapalıyken liste,
// rozet ve gövde bugünküyle aynı; açıkken küme tek satır, rozet kümeyi sayar,
// kapatınca düz liste veri değişmeden geri gelir.

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func graphFoldItems() []InboxItem {
	row := func(id, svc, prio string, start int64) InboxItem {
		return InboxItem{ID: "problem:" + id, Kind: "problem", Service: svc, Priority: prio, Title: svc + " hata oranı",
			StartedAt: start, LastSeen: start + 10, Problem: &InboxProblemRef{ID: id}}
	}
	return []InboxItem{
		row("p-co", "checkout-api", "P3", 300),
		row("p-db", "db:postgresql@orders-db", "P2", 100),
		row("p-pay", "payments-api", "P1", 200),
		row("p-search", "search-api", "P3", 50),
		{ID: "exception:fp1", Kind: "exception", Service: "checkout-api", Priority: "P1"},
	}
}

func graphFoldGroups() []chstore.ProblemGraphGroup {
	return []chstore.ProblemGraphGroup{
		{OwnerID: "p-db", Entities: 3, Origin: &chstore.ProblemGraphOrigin{Name: "orders-db"}, Members: []chstore.ProblemGraphMember{
			{ProblemID: "p-db", Name: "orders-db", Owner: true, StartedAt: 100},
			{ProblemID: "p-pay", Name: "payments-api", Link: chstore.GraphRuleCall, StartedAt: 200},
			{ProblemID: "p-co", Name: "checkout-api", Link: chstore.GraphRuleCall, StartedAt: 300},
		}},
		// Tek varlıklı (bağlanmayanları olan) grup katlamaz.
		{OwnerID: "p-search", Entities: 1, Members: []chstore.ProblemGraphMember{{ProblemID: "p-search", Owner: true}}},
	}
}

// inboxGraphView — inboxView'daki sıranın S4 kesiti: katlama → sayaçlar → tavan.
func inboxGraphView(items []InboxItem, groups []chstore.ProblemGraphGroup) ([]InboxItem, int, map[string]int) {
	if chstore.ProblemGraphMergeEnforced() {
		items = foldGraphClusters(items, groups)
	}
	counts := inboxFacetCounts(items)
	out, total := inboxSortAndCap(items, "priority", "desc", 200)
	return out, total, counts
}

func TestInboxGraphFoldOffIdentical(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(false)()
	base, baseTotal := inboxSortAndCap(graphFoldItems(), "priority", "desc", 200)
	baseCounts := inboxFacetCounts(graphFoldItems())
	got, total, counts := inboxGraphView(graphFoldItems(), graphFoldGroups())
	jb, _ := json.Marshal(base)
	jg, _ := json.Marshal(got)
	if string(jb) != string(jg) || total != baseTotal || !reflect.DeepEqual(counts, baseCounts) {
		t.Fatalf("kapalı S4 listeyi / sayacı değiştirdi:\n%s\n%s", jb, jg)
	}
	if strings.Contains(string(jg), `"cluster"`) {
		t.Fatal("kapalıyken cluster alanı gövdeye girmemeli")
	}
	body := map[string]any{"items": got, "total": total}
	before, _ := json.Marshal(body)
	after, _ := json.Marshal(withInboxGraphMode(body, false))
	if string(before) != string(after) || inboxGraphKeySuffix(false) != "" {
		t.Fatal("kapalıyken gövde ve önbellek anahtarı aynı kalmalı")
	}
}

func TestInboxGraphFoldOn(t *testing.T) {
	defer chstore.SetProblemGraphMergeEnforceForTest(true)()
	got, total, _ := inboxGraphView(graphFoldItems(), graphFoldGroups())
	if total != 3 || len(got) != 3 {
		t.Fatalf("küme tek satır: 5 satır → 3 (küme + search + exception), got %d", total)
	}
	var rep *InboxItem
	for i := range got {
		if got[i].Cluster != nil {
			rep = &got[i]
		}
	}
	if rep == nil || rep.Problem.ID != "p-db" || rep.Priority != "P1" || rep.StartedAt != 100 || rep.LastSeen != 310 {
		t.Fatalf("temsilci sahip, en yüksek öncelik, en erken / en geç: %+v", rep)
	}
	if len(rep.Cluster.Members) != 3 || rep.Cluster.Members[2].ProblemID != "p-co" || rep.Cluster.Origin != "orders-db" {
		t.Fatalf("katlanan üyeler satırdan erişilebilir: %+v", rep.Cluster)
	}
	body, _ := json.Marshal(withInboxGraphMode(map[string]any{}, true))
	if string(body) != `{"graphMerge":true}` || inboxGraphKeySuffix(true) == "" {
		t.Fatalf("açıkken kip gövdede: %s", body)
	}
	// Sahip listede yoksa en erken bulunan üye temsilci olur; tek üye kalırsa katlama yok.
	all := graphFoldItems()
	noOwner := append(append([]InboxItem{}, all[:1]...), all[2:]...) // p-db süzgeçle elendi
	f := foldGraphClusters(noOwner, graphFoldGroups())
	if len(f) != 3 || f[0].Problem.ID != "p-pay" || f[0].Cluster == nil || len(f[0].Cluster.Members) != 2 {
		t.Fatalf("sahipsiz katlama: listedeki en erken üye temsilci: %+v", f)
	}
	lone := foldGraphClusters([]InboxItem{all[0], all[3]}, graphFoldGroups())
	if len(lone) != 2 || lone[0].Cluster != nil {
		t.Fatalf("listede tek üye: katlama yok: %+v", lone)
	}
	// Kapatınca: aynı girdi, düz liste (veri değişmedi).
	restore := chstore.SetProblemGraphMergeEnforceForTest(false)
	defer restore()
	flat, flatTotal, _ := inboxGraphView(graphFoldItems(), graphFoldGroups())
	if flatTotal != 5 || len(flat) != 5 {
		t.Fatalf("kapalı: düz liste geri gelir, got %d", flatTotal)
	}
}

// Kablolama pini: katlama yalnız S4 açıkken, incident katlamasından SONRA,
// sayaçlardan ÖNCE; gövde kipi ve anahtar eki kapalıyken boş.
func TestInboxGraphFoldWiring(t *testing.T) {
	b, err := os.ReadFile("inbox.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	inc := strings.Index(src, "items = s.foldInboxIncidents(ctx, items)")
	fold := strings.Index(src, "if graphMerge {\n\t\t\titems = s.foldInboxGraphClusters(ctx, items)")
	counts := strings.Index(src, "counts := inboxFacetCounts(items)")
	if inc < 0 || fold < 0 || counts < 0 || !(inc < fold && fold < counts) {
		t.Fatalf("sıra incident katlaması → graf katlaması → sayaçlar (%d %d %d)", inc, fold, counts)
	}
	for _, want := range []string{"inboxGraphKeySuffix(graphMerge)", "}, graphMerge), nil", "graphMerge := chstore.ProblemGraphMergeEnforced()"} {
		if !strings.Contains(src, want) {
			t.Fatalf("inbox.go: %q yok", want)
		}
	}
}
