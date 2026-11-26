package api

// inbox_graph_fold_test.go — v0.10.2341 (entity grafı S4) + v0.10.2473
// (görünüm kipi ayarı): `off` / `shadow` kipinde liste, rozet ve gövde
// v0.10.2472 ile aynı; `on` iken bağlı küme tek satır, sayaçlar satır sayar,
// köken satırın standardında, hiçbir üye erişilmez kalmaz.

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Zincir: api-gateway → checkout → payments (üçü de sapmış) + ilgisiz search
// + aynı serviste bir exception grubu.
func graphFoldItems() []InboxItem {
	row := func(id, svc, prio string, start int64) InboxItem {
		return InboxItem{ID: "problem:" + id, Kind: "problem", Service: svc, Priority: prio, Title: svc + " hata oranı arttı",
			StartedAt: start, LastSeen: start + 10, Problem: &InboxProblemRef{ID: id}, Std: &chstore.ProblemStandard{Subject: svc}}
	}
	return []InboxItem{
		row("p-co", "checkout", "P3", 300),
		row("p-pay", "payments", "P2", 100),
		row("p-gw", "api-gateway", "P1", 200),
		row("p-search", "search", "P3", 50),
		{ID: "exception:fp1", Kind: "exception", Service: "checkout", Priority: "P1"},
	}
}

func graphFoldOrigin() *chstore.ProblemGraphOrigin {
	return &chstore.ProblemGraphOrigin{Entity: "svc:payments", Name: "payments", Type: "service",
		Rule: chstore.OriginRuleDeep, Confidence: chstore.OriginWeak, Leads: []string{chstore.OriginRuleDeep}}
}

func graphFoldGroups() []chstore.ProblemGraphGroup {
	return []chstore.ProblemGraphGroup{
		{OwnerID: "p-pay", Entities: 3, Origin: graphFoldOrigin(), Members: []chstore.ProblemGraphMember{
			{ProblemID: "p-pay", Entity: "svc:payments", Type: "service", Name: "payments", Owner: true, StartedAt: 100},
			{ProblemID: "p-gw", Entity: "svc:api-gateway", Type: "service", Name: "api-gateway", Link: chstore.GraphRuleCall, StartedAt: 200},
			{ProblemID: "p-co", Entity: "svc:checkout", Type: "service", Name: "checkout", Link: chstore.GraphRuleCall, StartedAt: 300},
		}},
		// Tek varlıklı (bağlanmayanları olan) grup katlamaz.
		{OwnerID: "p-search", Entities: 1, Members: []chstore.ProblemGraphMember{{ProblemID: "p-search", Entity: "svc:search", Type: "service", Name: "search", Owner: true}}},
	}
}

// inboxGraphView — inboxView'daki sıranın kesiti: (kip on ise) katlama →
// sayaçlar → köken → tavan. Kümeler kip on değilken hiç okunmaz.
func inboxGraphView(items []InboxItem, groups []chstore.ProblemGraphGroup) ([]InboxItem, int, map[string]int) {
	var gs []chstore.ProblemGraphGroup
	if chstore.ProblemGraphMergeOn() {
		gs = groups
		items = foldGraphClusters(items, gs)
	}
	counts := inboxFacetCounts(items)
	attachInboxOrigins(items, gs)
	out, total := inboxSortAndCap(items, "priority", "desc", 200)
	return out, total, counts
}

// narrowByService — inboxView'daki servis / arama daraltmasının test ikizi
// (büyük-küçük harf duyarsız alt dizi; sıra pini TestInboxGraphFoldWiring).
func narrowByService(items []InboxItem, needle string) []InboxItem {
	var out []InboxItem
	for _, it := range items {
		if strings.Contains(strings.ToLower(it.Service), strings.ToLower(needle)) ||
			strings.Contains(strings.ToLower(it.Title), strings.ToLower(needle)) {
			out = append(out, it)
		}
	}
	return out
}

func TestInboxGraphFoldOffAndShadowIdentical(t *testing.T) {
	base, baseTotal := inboxSortAndCap(graphFoldItems(), "priority", "desc", 200)
	baseCounts := inboxFacetCounts(graphFoldItems())
	jb, _ := json.Marshal(base)
	for _, mode := range []string{chstore.GraphMergeOff, chstore.GraphMergeShadow} {
		restore := chstore.SetProblemGraphMergeModeForTest(mode)
		got, total, counts := inboxGraphView(graphFoldItems(), graphFoldGroups())
		on := chstore.ProblemGraphMergeOn()
		restore()
		jg, _ := json.Marshal(got)
		if string(jb) != string(jg) || total != baseTotal || !reflect.DeepEqual(counts, baseCounts) {
			t.Fatalf("%s: liste / sayaç değişti:\n%s\n%s", mode, jb, jg)
		}
		if strings.Contains(string(jg), `"cluster"`) || strings.Contains(string(jg), `"origin"`) {
			t.Fatalf("%s: cluster / origin alanı gövdeye girmemeli", mode)
		}
		body := map[string]any{"items": got, "total": total}
		before, _ := json.Marshal(body)
		after, _ := json.Marshal(withInboxGraphMode(body, on))
		if string(before) != string(after) || inboxGraphKeySuffix(on) != "" {
			t.Fatalf("%s: gövde ve önbellek anahtarı aynı kalmalı", mode)
		}
	}
}

func TestInboxGraphFoldOnChainOneRowWithOrigin(t *testing.T) {
	defer chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)()
	got, total, counts := inboxGraphView(graphFoldItems(), graphFoldGroups())
	if total != 3 || len(got) != 3 {
		t.Fatalf("zincir tek satır: 5 satır → 3 (küme + search + exception), got %d", total)
	}
	var rep, search *InboxItem
	for i := range got {
		if got[i].Cluster != nil {
			rep = &got[i]
		}
		if got[i].Problem != nil && got[i].Problem.ID == "p-search" {
			search = &got[i]
		}
	}
	if rep == nil || rep.Problem.ID != "p-pay" || rep.Priority != "P1" || rep.StartedAt != 100 || rep.LastSeen != 310 {
		t.Fatalf("temsilci sahip, en yüksek öncelik, en erken / en geç: %+v", rep)
	}
	c := rep.Cluster
	if c.Entities != 3 || c.Services != 3 || c.Problems != 3 || len(c.Members) != 3 || c.Members[2].ProblemID != "p-co" || c.Members[1].Type != "service" {
		t.Fatalf("küme özeti + katlanan üyeler satırdan erişilebilir: %+v", c)
	}
	// Köken satırın standardında: varlık kimliğiyle (bağlantı entity modelinden).
	o := rep.Std.Origin
	if o == nil || o.Entity != "svc:payments" || o.Name != "payments" || o.Rule != chstore.OriginRuleDeep || o.Entities != 3 || !o.Self {
		t.Fatalf("küme satırının kökeni: %+v", o)
	}
	// İlgisiz Problem ayrı satır, kümesiz, kökensiz.
	if search == nil || search.Cluster != nil || search.Std.Origin != nil {
		t.Fatalf("ilgisiz Problem ayrı kalmalı: %+v", search)
	}
	// SAYIM = SATIR: küme bir kez, en yüksek üye önceliğiyle sayılır.
	if counts["problem"] != 2 || counts["exception"] != 1 || counts["P1"] != 2 || counts["P3"] != 1 || counts["P2"] != 0 {
		t.Fatalf("sayaçlar satır sayar (küme = 1): %+v", counts)
	}
	sum := 0
	for _, p := range []string{"P1", "P2", "P3"} {
		sum += counts[p]
	}
	if sum != total {
		t.Fatalf("öncelik çipleri toplamı (%d) listenin toplamına (%d) eşit olmalı", sum, total)
	}
	body, _ := json.Marshal(withInboxGraphMode(map[string]any{}, true))
	if string(body) != `{"graphMerge":true}` || inboxGraphKeySuffix(true) == "" {
		t.Fatalf("on iken kip gövdede ve anahtarda: %s", body)
	}
}

// Üye asla erişilmez kalmaz: servisine süzmek / aramak onu kendi satırıyla
// getirir (Problem kimliği aynı → doğrudan bağlantı aynı), kökeni de söyler.
func TestInboxGraphFoldMemberReachableByServiceFilter(t *testing.T) {
	defer chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)()
	for _, needle := range []string{"checkout", "CHECK"} {
		got, total, counts := inboxGraphView(narrowByService(graphFoldItems(), needle), graphFoldGroups())
		var member *InboxItem
		for i := range got {
			if got[i].Problem != nil && got[i].Problem.ID == "p-co" {
				member = &got[i]
			}
		}
		if member == nil || member.Cluster != nil || member.Priority != "P3" {
			t.Fatalf("%q: üye kendi satırıyla, kendi önceliğiyle gelmeli: %+v (toplam %d)", needle, got, total)
		}
		if inboxRowHref(*member) != "/problems?problem=p-co" {
			t.Fatalf("üyenin doğrudan bağlantısı değişmemeli: %s", inboxRowHref(*member))
		}
		if o := member.Std.Origin; o == nil || o.Name != "payments" || o.Self || o.Entities != 3 {
			t.Fatalf("tek başına kalan üye de kümesinin kökenini söyler: %+v", o)
		}
		if counts["problem"] != 1 || total != 2 {
			t.Fatalf("sayaç gösterilen satırlarla aynı: %+v toplam %d", counts, total)
		}
	}
	// Sahip süzgeçle elendiyse listede bulunan en erken üye temsilci olur.
	all := graphFoldItems()
	noOwner := []InboxItem{all[0], all[2], all[3], all[4]} // p-pay yok
	f := foldGraphClusters(noOwner, graphFoldGroups())
	if len(f) != 3 || f[0].Problem.ID != "p-gw" || f[0].Cluster == nil || len(f[0].Cluster.Members) != 2 || f[0].Cluster.Problems != 3 {
		t.Fatalf("sahipsiz katlama: listedeki en erken üye temsilci, küme toplamı korunur: %+v", f)
	}
	// Listede tek üye: katlama yok.
	lone := foldGraphClusters([]InboxItem{all[0], all[3]}, graphFoldGroups())
	if len(lone) != 2 || lone[0].Cluster != nil {
		t.Fatalf("listede tek üye: katlama yok: %+v", lone)
	}
}

// Köken yok / belirsiz → küme yine katlanır ama ETİKET YOK (tahmin yok).
func TestInboxGraphFoldNoOriginNoLabel(t *testing.T) {
	defer chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)()
	for name, o := range map[string]*chstore.ProblemGraphOrigin{
		"hesaplanmadı": nil,
		"belirsiz":     {Confidence: chstore.OriginUnclear, Earliest: "payments"},
	} {
		gs := graphFoldGroups()
		gs[0].Origin = o
		got, total, _ := inboxGraphView(graphFoldItems(), gs)
		if total != 3 {
			t.Fatalf("%s: küme yine tek satır (%d)", name, total)
		}
		for _, it := range got {
			if it.Std != nil && it.Std.Origin != nil {
				t.Fatalf("%s: köken etiketi olmamalı: %+v", name, it.Std.Origin)
			}
		}
	}
}

// Bayat blob (lider yazmıyor) → küme bilgisi kullanılmaz: düz liste.
func TestFreshGraphGroups(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	rep := chstore.ProblemGraphShadow{At: now.Add(-2 * time.Minute).Unix(), Groups: graphFoldGroups()}
	if g := freshGraphGroups(rep, true, now); len(g) != 2 {
		t.Fatalf("taze blob kümeleri kullanılır: %d", len(g))
	}
	rep.At = now.Add(-inboxGraphBlobMaxAge - time.Minute).Unix()
	if g := freshGraphGroups(rep, true, now); g != nil {
		t.Fatalf("bayat blob: küme yok (%d)", len(g))
	}
	if g := freshGraphGroups(rep, false, now); g != nil {
		t.Fatal("blob yok: küme yok")
	}
	if g := freshGraphGroups(chstore.ProblemGraphShadow{Groups: graphFoldGroups()}, true, now); g != nil {
		t.Fatal("zaman damgasız blob: küme yok")
	}
}

// Rozet listenin varsayılan görünümünün toplamı: anahtarı görünüm kipini
// taşır (kip değişince eski sayı servis edilmez), düşürme öneki aynı kalır.
func TestInboxCountKeyCarriesGraphMode(t *testing.T) {
	r1 := chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeShadow)
	shadow := inboxCountKey("uat")
	r1()
	r2 := chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)
	on := inboxCountKey("uat")
	r2()
	if shadow == on {
		t.Fatalf("rozet anahtarı kipi taşımalı: %q", on)
	}
	for _, k := range []string{shadow, on} {
		if !strings.HasPrefix(k, "inbox:count") || !strings.Contains(k, "uat") {
			t.Fatalf("önek / env korunmalı: %q", k)
		}
	}
}

// Detay ve liste alt satırı uçları kipe göre: off → küme yok; shadow → gölge
// önizleme; on → gerçek (gölge değil), alt satır yerine satırın standardı.
func TestProblemGraphEndpointsByMode(t *testing.T) {
	rep := chstore.ProblemGraphShadow{At: 7, Groups: graphFoldGroups(),
		Frequent: map[string]chstore.FrequentVerdict{"p-co": {Frequent: true, Kind: chstore.FrequentKindHold}}}
	off := problemGraphForMode(rep, true, "p-co", chstore.GraphMergeOff)
	if len(off.Members) != 0 || off.Origin != nil || off.Mode != chstore.GraphMergeOff || off.Frequent == nil {
		t.Fatalf("off: küme / köken yok, sık tekrar (S3) kalır: %+v", off)
	}
	sh := problemGraphForMode(rep, true, "p-co", chstore.GraphMergeShadow)
	if len(sh.Members) != 3 || !sh.Shadow || sh.Origin == nil {
		t.Fatalf("shadow: gölge bölümü bugünkü gibi: %+v", sh)
	}
	on := problemGraphForMode(rep, true, "p-co", chstore.GraphMergeOn)
	if len(on.Members) != 3 || on.Shadow || on.Mode != chstore.GraphMergeOn {
		t.Fatalf("on: aynı küme, gölge işareti yok: %+v", on)
	}
	if rep.Groups == nil {
		t.Fatal("kip süzgeci çağıranın blobunu değiştirmemeli")
	}
	if s := problemGraphSummaryForMode(rep, true, chstore.GraphMergeShadow); !s.Shadow || s.Rows["p-gw"].Origin != "payments" || s.Rows["p-co"].Frequent == nil {
		t.Fatalf("shadow özet: %+v", s)
	}
	for _, mode := range []string{chstore.GraphMergeOff, chstore.GraphMergeOn} {
		s := problemGraphSummaryForMode(rep, true, mode)
		if s.Shadow || s.Rows["p-gw"].Entities != 0 || s.Rows["p-co"].Frequent == nil || s.Rows["p-co"].Entities != 0 {
			t.Fatalf("%s özet: gölge alt satırı yok, sık tekrar çipi kalır: %+v", mode, s)
		}
	}
}

func TestDecodeProblemGraphMergePut(t *testing.T) {
	for _, m := range []string{"off", "shadow", "on"} {
		if got, err := decodeProblemGraphMergePut([]byte(`{"mode":"` + m + `"}`)); err != nil || got != m {
			t.Fatalf("%s: %q %v", m, got, err)
		}
	}
	for _, bad := range []string{`{}`, `{"mode":""}`, `{"mode":"enforce"}`, `{"mode":"ON"}`, `[]`, `{"mode":`} {
		if _, err := decodeProblemGraphMergePut([]byte(bad)); err == nil {
			t.Fatalf("%s: geçersiz gövde reddedilmeli", bad)
		}
	}
}

// CoSRE / servis şeridi aynı çekirdeği okur: katlanan satır kümenin diğer
// varlıklarını adlarıyla taşır (sayı listeyle aynı, üyeler görünür).
func TestInboxRowsCarryLinkedEntities(t *testing.T) {
	defer chstore.SetProblemGraphMergeModeForTest(chstore.GraphMergeOn)()
	got, _, _ := inboxGraphView(graphFoldItems(), graphFoldGroups())
	rows := inboxRowsFrom(got)
	var linked []string
	for _, r := range rows {
		if r.ID == "problem:p-pay" {
			linked = r.Linked
		} else if len(r.Linked) != 0 {
			t.Fatalf("katlanmayan satır bağlı varlık taşımamalı: %+v", r)
		}
	}
	if !reflect.DeepEqual(linked, []string{"api-gateway", "checkout"}) {
		t.Fatalf("katlanan satırın bağlı varlıkları: %v", linked)
	}
}

// Kablolama pini: kip AYARDAN okunur; katlama satır daraltmalarından (servis,
// arama) SONRA, incident katlamasından SONRA, sayaçlardan ÖNCE; köken
// standartlardan sonra; gövde kipi ve anahtar eki yalnız on iken.
func TestInboxGraphFoldWiring(t *testing.T) {
	b, err := os.ReadFile("inbox.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	svc := strings.Index(src, "if service != \"\" {\n\t\t\tneedle := strings.ToLower(service)")
	search := strings.Index(src, "if search != \"\" {\n\t\t\tneedle := strings.ToLower(search)")
	inc := strings.Index(src, "items = s.foldInboxIncidents(ctx, items)")
	fold := strings.Index(src, "if graphMerge {\n\t\t\tgraphGroups = s.inboxGraphGroups(ctx)\n\t\t\titems = foldGraphClusters(items, graphGroups)")
	counts := strings.Index(src, "counts := inboxFacetCounts(items)")
	std := strings.Index(src, "s.attachInboxStandards(ctx, items, time.Now())")
	origin := strings.Index(src, "attachInboxOrigins(items, graphGroups)")
	if svc < 0 || search < 0 || inc < 0 || fold < 0 || counts < 0 || std < 0 || origin < 0 ||
		!(svc < fold && search < fold && inc < fold && fold < counts && counts < std && std < origin) {
		t.Fatalf("sıra: servis / arama daraltması → incident katlaması → graf katlaması → sayaçlar → standart → köken (%d %d %d %d %d %d %d)",
			svc, search, inc, fold, counts, std, origin)
	}
	for _, want := range []string{"inboxGraphKeySuffix(graphMerge)", "}, graphMerge), nil", "graphMerge := chstore.ProblemGraphMergeOn()"} {
		if !strings.Contains(src, want) {
			t.Fatalf("inbox.go: %q yok", want)
		}
	}
	if strings.Contains(src, "ProblemGraphMergeEnforced()") {
		t.Fatal("inbox.go bildirim tutması sabitini okumamalı; liste görünüm kipi ayarına bağlı")
	}
	// Admin yazımı audit satırı bırakır.
	a, err := os.ReadFile("problem_graph_merge_setting.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), `s.audit(r, "problem_graph.merge"`) || !strings.Contains(string(a), "auth.RequireRole(auth.RoleAdmin, s.putProblemGraphMerge)") {
		t.Fatal("PUT /api/admin/problem-graph-merge admin kapılı olmalı ve audit yazmalı")
	}
}
