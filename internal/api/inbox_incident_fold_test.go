package api

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1084 (operatör: "tekilleştir, Exceptions'taki format güzel") — açık
// incident'ın bağlı problem satırları /inbox'ta incident satırına katlanır.
// Saf çekirdek (foldIncidentProblems) tablo-testli; sıra pini (görünüm
// önceliklerinden SONRA, facet sayaçlarından / sıralama-tavandan ÖNCE) ve
// cache anahtarı sürümü burada.

func foldFixture(incStatus string) []InboxItem {
	chstore.SetProblemPriority(chstore.DefaultProblemPriority())
	base := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC).UnixNano()
	minute := int64(time.Minute)
	dbProblem := func(id, db string, startMin int64, val float64) chstore.Problem {
		return chstore.Problem{
			ID: id, RuleID: "db-health:couchbase@cb-node-1/" + db, RuleName: "DB health · couchbase",
			Severity: "critical", Status: "open", Kind: chstore.ProblemKindDB, Service: "db:couchbase@" + db,
			Metric: chstore.DBHealthMetricErrorPct, Value: val, Threshold: 5, Comparator: ">=",
			Description: fmt.Sprintf("couchbase %s (cb-node-1) veritabanında hata oranı %%%v (eşik %%5), 3 çağıran servis etkilendi — 400 çağrı / 5 dk.", db, val),
			StartedAt:   base + startMin*minute,
		}
	}
	probs := chstore.EnrichProblemsWithPriority([]chstore.Problem{
		dbProblem("p-orders", "orders", 5, 100),  // birincil (en erken)
		dbProblem("p-carts", "carts", 12, 67),    // ikinci bağlı
		dbProblem("p-other", "sessions", 20, 11), // bağlı DEĞİL
	})
	var items []InboxItem
	for _, p := range probs {
		items = append(items, problemToInbox(p))
	}
	inc := chstore.Incident{
		ID: "inc-1", Title: "db:couchbase@orders — DB health · couchbase", Severity: "warning", Status: incStatus,
		Service: "db:couchbase@orders", Summary: "eski özet",
		StartedAt: base + 6*minute, UpdatedAt: base + 15*minute,
	}
	items = append(items, incidentToInbox(inc),
		InboxItem{ID: "anomaly:a1", Kind: "anomaly", Priority: "P1", StartedAt: base, LastSeen: base,
			Anomaly: &InboxAnomalyRef{ID: "a1"}})
	forceNonExceptionP3(items)
	return items
}

var foldAttached = map[string][]string{"inc-1": {"p-orders", "p-carts", "p-orders", ""}}

func rowByID(items []InboxItem, id string) *InboxItem {
	for i := range items {
		if items[i].ID == id {
			return &items[i]
		}
	}
	return nil
}

func TestFoldIncidentProblemsOneRowPerIncident(t *testing.T) {
	items := foldFixture("open")
	// Kurulum: katlamadan önce iki bağlı problem P1 (db-health istisna listesinde),
	// warning incident P3 (incident:warning listede değil).
	if p := rowByID(items, "problem:p-orders"); p == nil || p.Priority != "P1" {
		t.Fatalf("kurulum: bağlı problem P1 olmalı: %+v", p)
	}
	if inc := rowByID(items, "incident:inc-1"); inc == nil || inc.Priority != "P3" {
		t.Fatalf("kurulum: warning incident P3 olmalı: %+v", inc)
	}
	before := len(items)
	got := foldIncidentProblems(items, foldAttached)

	if len(got) != before-2 {
		t.Fatalf("iki bağlı problem gizlenmeli: %d → %d", before, len(got))
	}
	for _, id := range []string{"problem:p-orders", "problem:p-carts"} {
		if rowByID(got, id) != nil {
			t.Errorf("%s gizlenmeliydi", id)
		}
	}
	if rowByID(got, "problem:p-other") == nil || rowByID(got, "anomaly:a1") == nil {
		t.Error("bağlı olmayan problem ve anomali satırı dokunulmamalı")
	}
	inc := rowByID(got, "incident:inc-1")
	if inc == nil {
		t.Fatal("incident satırı kalmalı")
	}
	if inc.Incident.ProblemCount != 2 {
		t.Errorf("bağlı problem sayısı 2 (tekrar/boş id sayılmaz), %d", inc.Incident.ProblemCount)
	}
	if inc.Priority != "P1" || !strings.HasPrefix(inc.PriorityReason, "bağlı problem: ") {
		t.Errorf("öncelik bağlıların en yükseği (P1) olmalı: %s %q", inc.Priority, inc.PriorityReason)
	}
	base := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC).UnixNano()
	if inc.StartedAt != base+5*int64(time.Minute) {
		t.Errorf("ilk görülme en erken bağlı problemin (09:05): %v", time.Unix(0, inc.StartedAt).UTC())
	}
	if inc.LastSeen != base+15*int64(time.Minute) {
		t.Errorf("son görülme en geç (incident güncellemesi 09:15): %v", time.Unix(0, inc.LastSeen).UTC())
	}
	if !strings.HasPrefix(inc.Description, "couchbase orders (cb-node-1) veritabanında hata oranı") {
		t.Errorf("açıklama birincil (en erken) problemin cümlesi olmalı: %q", inc.Description)
	}
	if inc.Category != chstore.CategoryError {
		t.Errorf("kategori birincil problemden (ERROR): %q", inc.Category)
	}
	if inc.Title != "db:couchbase@orders — DB health · couchbase" {
		t.Errorf("incident başlığı değişmemeli: %q", inc.Title)
	}

	// Facet sayaçları gizlenen satırları saymaz: 1 problem + 1 incident + 1 anomali.
	counts := inboxFacetCounts(got)
	if counts["problem"] != 1 || counts["incident"] != 1 || counts["anomaly"] != 1 {
		t.Errorf("tür sayaçları: %v", counts)
	}
	// P1 çipi listeyle aynı kümeyi sayar: taşınmış incident + bağlı olmayan
	// db-health problemi (anomali exception dışı, istisna listesinde değil → P3).
	if counts["P1"] != 2 {
		t.Errorf("P1 çipi 2 olmalı (incident + p-other): %v", counts)
	}
	// Yalnız-P1 + ilk görülme (varsayılan görünüm): katlanmış incident tek satır,
	// bağlı problemler yok; en yeni ilk görülme önce (p-other 09:20, incident 09:05).
	sorted, total := inboxSortAndCap(applyInboxFacets(got, inboxKindsAll, []string{"P1"}), "firstSeen", "desc", 300)
	if total != 2 || len(sorted) != 2 || sorted[0].ID != "problem:p-other" || sorted[1].ID != "incident:inc-1" {
		ids := []string{}
		for _, it := range sorted {
			ids = append(ids, it.ID)
		}
		t.Errorf("yalnız-P1 görünümü: total %d, sıra %v", total, ids)
	}
}

// Incident'ı kapanmış (resolved / closed) problem kendisi olarak görünür.
func TestFoldIncidentProblemsResolvedIncidentReleases(t *testing.T) {
	for _, st := range []string{"resolved", "closed", " Resolved "} {
		items := foldFixture(st)
		got := foldIncidentProblems(items, foldAttached)
		if len(got) != len(items) {
			t.Errorf("%q: katlama olmamalı: %d → %d", st, len(items), len(got))
		}
		if rowByID(got, "problem:p-orders") == nil || rowByID(got, "problem:p-carts") == nil {
			t.Errorf("%q: problemler kendileri olarak görünmeli", st)
		}
		if inc := rowByID(got, "incident:inc-1"); inc != nil && inc.Incident.ProblemCount != 0 {
			t.Errorf("%q: kapalı incident'a sayı yazılmamalı", st)
		}
	}
	// acknowledged hâlâ açık → katlar.
	if got := foldIncidentProblems(foldFixture("acknowledged"), foldAttached); rowByID(got, "problem:p-orders") != nil {
		t.Error("acknowledged incident katlamalı")
	}
}

// Incident satırı listede yoksa (bir süzgeç eledi) problem gizlenmez; bağlantı
// okunamadıysa (nil harita) hiçbir şey değişmez.
func TestFoldIncidentProblemsNeedsTheIncidentRow(t *testing.T) {
	items := foldFixture("open")
	var noInc []InboxItem
	for _, it := range items {
		if it.Kind != "incident" {
			noInc = append(noInc, it)
		}
	}
	if got := foldIncidentProblems(noInc, foldAttached); len(got) != len(noInc) {
		t.Errorf("incident satırı yokken problem gizlendi: %d → %d", len(noInc), len(got))
	}
	if got := foldIncidentProblems(items, nil); len(got) != len(items) {
		t.Error("bağlantı yoksa katlama olmamalı")
	}
	if ids := inboxFoldCandidates(items); len(ids) != 1 || ids[0] != "inc-1" {
		t.Errorf("okuma adayı açık incident: %v", ids)
	}
	if ids := inboxFoldCandidates(foldFixture("resolved")); len(ids) != 0 {
		t.Errorf("kapalı incident okuma adayı değil: %v", ids)
	}
	var onlyInc []InboxItem
	for _, it := range items {
		if it.Kind == "incident" {
			onlyInc = append(onlyInc, it)
		}
	}
	if ids := inboxFoldCandidates(onlyInc); ids != nil {
		t.Errorf("problem satırı yokken okuma olmamalı: %v", ids)
	}
}

func TestInboxProblemSentence(t *testing.T) {
	for in, want := range map[string]string{
		"couchbase orders veritabanında hata oranı %100 · auto-resolved: source silent": "couchbase orders veritabanında hata oranı %100",
		"x veritabanında … · resolved: system excluded (db-health)":                     "x veritabanında …",
		"  düz cümle  ": "düz cümle",
		"":              "",
	} {
		if got := inboxProblemSentence(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// "Problem" (detail) sıralaması ekranda kalın okunan başlığa; Occurrences
// sıralaması katlanmış incident'ın bağlı problem sayısına bakar (FE ikizi
// lib/inboxRowText.ts).
func TestInboxHeadlineAndIncidentOccurrencesSort(t *testing.T) {
	inc := InboxItem{ID: "i", Kind: "incident", Title: "z-incident", Description: "a cümle · auto-resolved: x",
		Incident: &InboxIncidentRef{ID: "i", ProblemCount: 2}}
	prob := InboxItem{ID: "p", Kind: "problem", Title: "a-rule", Description: "m cümle"}
	exc := InboxItem{ID: "e", Kind: "exception", Title: "", Exception: &InboxExceptionRef{Type: "b.Type", Occurrences: 1}}
	if inboxHeadline(inc) != "a cümle" || inboxHeadline(prob) != "m cümle" || inboxHeadline(exc) != "b.Type" {
		t.Fatalf("başlıklar: %q %q %q", inboxHeadline(inc), inboxHeadline(prob), inboxHeadline(exc))
	}
	items := []InboxItem{prob, exc, inc}
	sortInboxItems(items, "detail", "asc")
	if items[0].ID != "i" || items[1].ID != "e" || items[2].ID != "p" {
		t.Errorf("detail asc: %s %s %s", items[0].ID, items[1].ID, items[2].ID)
	}
	sortInboxItems(items, "occurrences", "desc")
	if items[0].ID != "i" || items[1].ID != "e" || items[2].ID != "p" {
		t.Errorf("occurrences desc (incident 2 > exception 1 > problem 0): %s %s %s", items[0].ID, items[1].ID, items[2].ID)
	}
}

// Sıra pini: katlama görünüm önceliklerinden (forceNonExceptionP3) SONRA,
// facet sayaçlarından ÖNCE; sıralama/tavan zaten sayaçlardan sonra.
func TestFoldRunsAfterViewPriorityBeforeCounts(t *testing.T) {
	src := readSrc(t, "inbox.go")
	force := strings.Index(src, "\t\tforceNonExceptionP3(items)")
	fold := strings.Index(src, "items = s.foldInboxIncidents(ctx, items)")
	counts := strings.Index(src, "counts := inboxFacetCounts(items)")
	capAt := strings.Index(src, "items, total := inboxSortAndCap(items, sortID, sortDir, limit)")
	if force < 0 || fold < 0 || counts < 0 || capAt < 0 || !(force < fold && fold < counts && counts < capAt) {
		t.Fatalf("sıra bozuk: force=%d fold=%d counts=%d cap=%d", force, fold, counts, capAt)
	}
	if strings.Count(src, "s.foldInboxIncidents(ctx, items)") != 1 {
		t.Error("katlama derleme başına bir kez")
	}
}
