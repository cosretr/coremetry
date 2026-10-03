package api

import (
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.1086 regresyon testi — kenar çubuğu Problems rozeti varsayılan
// listeyle AYNI sayar (katlama dahil).
//
// Operatör onaylı kusur: v0.10.1084'ten beri liste açık incident'ın bağlı
// problemlerini tek satıra katlıyor, ama rozet (/api/inbox/count) kaynak
// başına COUNT'ların toplamıydı — incident ve bağlı problemleri ayrı, üstelik
// tüm öncelikler. Rozet ekrandaki satırdan büyük okunuyordu. Artık rozet
// varsayılan görünümün derlemesini (inboxView(inboxBadgeQuery)) ve onun
// `total`ını okur; bu dosya (1) satır sayısını karışık bir fikstürde,
// (2) anahtarın sayfanın varsayılan isteğiyle aynı olduğunu, (3) sayfanın
// varsayılan sabitlerini çiviler.

// badgeMixedFixture — 2 bağlı db-health problemli açık incident + 1 P1
// exception + P3'e düşen / P3 satırlar. Eski toplam (problems + anomalies +
// incidents, tüm öncelikler) burada 3 + 1 + 1 = 5 derdi.
func badgeMixedFixture() ([]InboxItem, map[string][]string) {
	chstore.SetProblemPriority(chstore.DefaultProblemPriority())
	const base = int64(1_790_000_000_000_000_000)
	const minute = int64(60_000_000_000)
	dbProblem := func(id, db string, startMin int64) InboxItem {
		return InboxItem{
			ID: "problem:" + id, Kind: "problem", Source: "Alert rule", Priority: "P1", Severity: "critical",
			Service: "db:couchbase@" + db, SubjectKind: chstore.ProblemKindDB, Status: "open",
			Description: "couchbase " + db + " (cb-node-1) veritabanında hata oranı yüksek.",
			StartedAt:   base + startMin*minute, LastSeen: base + startMin*minute,
			Problem: &InboxProblemRef{ID: id, RuleID: "db-health:couchbase@cb-node-1/" + db},
		}
	}
	items := []InboxItem{
		dbProblem("p-orders", "orders", 5),
		dbProblem("p-carts", "carts", 12),
		// İstisna listesinde olmayan kural: görünümde P3'e düşer.
		{
			ID: "problem:p-cpu", Kind: "problem", Source: "Alert rule", Priority: "P1", Severity: "critical",
			Service: "demo-checkout", Status: "open", Title: "CPU yüksek", StartedAt: base + 30*minute,
			Problem: &InboxProblemRef{ID: "p-cpu", RuleID: "rule-demo-cpu-high"},
		},
		// Warning incident (kaynakta P2, görünümde P3) — katlama P1'e taşır.
		{
			ID: "incident:inc-1", Kind: "incident", Source: "Incident", Priority: "P2", Severity: "warning",
			Service: "db:couchbase@orders", Status: "open", Title: "db:couchbase@orders — DB health",
			StartedAt: base + 6*minute, LastSeen: base + 15*minute,
			Incident: &InboxIncidentRef{ID: "inc-1", Severity: "warning", Status: "open"},
		},
		// P1 exception — kendi merdiveni, dokunulmaz.
		{
			ID: "exception:fp-demo-1", Kind: "exception", Source: "Exception", Priority: "P1", Severity: "critical",
			Service: "demo-payments", Status: "open", Title: "DemoTimeoutException", StartedAt: base + 40*minute,
			Exception: &InboxExceptionRef{Fingerprint: "fp-demo-1", Type: "DemoTimeoutException", Occurrences: 120},
		},
		// P3 exception — varsayılan (yalnız P1) görünümde yok.
		{
			ID: "exception:fp-demo-2", Kind: "exception", Source: "Exception", Priority: "P3", Severity: "warning",
			Service: "demo-payments", Status: "open", Title: "DemoParseException", StartedAt: base + 41*minute,
			Exception: &InboxExceptionRef{Fingerprint: "fp-demo-2", Type: "DemoParseException", Occurrences: 7},
		},
		// Log deseni anomalisi — istisna kimliği yok, P3'e düşer.
		{
			ID: "anomaly:a-demo", Kind: "anomaly", Source: "Anomaly", Priority: "P1", Severity: "critical",
			Service: "demo-checkout", Status: "active", Title: "log pattern spike", StartedAt: base + 50*minute,
			Anomaly: &InboxAnomalyRef{ID: "a-demo", Kind: "log_pattern"},
		},
	}
	return items, map[string][]string{"inc-1": {"p-orders", "p-carts"}}
}

// inboxDefaultViewRows — inboxView'ın satır kuyruğunu (görünüm önceliği →
// katlama → tür/öncelik facet'i → kategori → sıralama + tavan) rozet
// sorgusunun parametreleriyle koşar. Sıra inbox_incident_fold_test /
// inbox_scan_test pinleriyle sabit.
func inboxDefaultViewRows(t *testing.T, items []InboxItem, attached map[string][]string, q url.Values) ([]InboxItem, int) {
	t.Helper()
	kinds := normalizeInboxSet(q.Get("kind"), inboxKindsAll)
	prios := normalizeInboxSet(q.Get("prio"), inboxPriosAll)
	cats := normalizeInboxSet(q.Get("cat"), chstore.ProblemCategories)
	sortID, sortDir := normalizeInboxSort(q.Get("sort"), q.Get("dir"))
	limit := parseInt(q.Get("limit"), 200)

	forceNonExceptionP3(items)
	items = foldIncidentProblems(items, attached)
	items = applyInboxFacets(items, kinds, prios)
	for i := range items {
		if items[i].Category == "" {
			items[i].Category = inboxDerivedCategory(items[i])
		}
	}
	items = applyInboxCategoryFacet(items, cats)
	return inboxSortAndCap(items, sortID, sortDir, limit)
}

func TestInboxBadgeEqualsDefaultListRows(t *testing.T) {
	items, attached := badgeMixedFixture()
	rows, total := inboxDefaultViewRows(t, items, attached, inboxBadgeQuery(""))
	if total != 2 {
		t.Fatalf("rozet (total) = %d, want 2 (katlanmış incident + P1 exception)", total)
	}
	if len(rows) != total {
		t.Fatalf("liste satırı %d ≠ rozet %d — rozet ekrandaki satır sayısı olmalı", len(rows), total)
	}
	got := map[string]bool{}
	for _, r := range rows {
		got[r.ID] = true
		if r.Priority != "P1" {
			t.Errorf("%s önceliği %s — varsayılan görünüm yalnız P1", r.ID, r.Priority)
		}
	}
	if !got["incident:inc-1"] || !got["exception:fp-demo-1"] {
		t.Errorf("beklenen satırlar incident:inc-1 + exception:fp-demo-1, gelen %v", got)
	}
	for _, r := range rows {
		if r.ID == "incident:inc-1" && (r.Incident == nil || r.Incident.ProblemCount != 2) {
			t.Errorf("incident satırı bağlı 2 problemi taşımalı: %+v", r.Incident)
		}
	}
}

// Rozet sayfanın PARAMETRESİZ açılışıyla aynı önbellek girdisini okur:
// aynı anahtar = aynı derleme = aynı satır kümesi.
func TestInboxBadgeSharesDefaultListKey(t *testing.T) {
	s := &Server{}
	page := url.Values{
		"status": {"open"}, "limit": {"300"}, "sort": {"firstSeen"}, "dir": {"desc"},
		"kind": {"problem,exception,httperror,anomaly,incident"}, "prio": {"P1"}, "subject": {"service"},
	}
	pageKey, _ := s.inboxView(page)
	badgeKey, _ := s.inboxView(inboxBadgeQuery(""))
	if pageKey != badgeKey {
		t.Fatalf("rozet anahtarı sayfanınkinden farklı:\n page  %s\n badge %s", pageKey, badgeKey)
	}
	page.Set("env", "uat")
	envPage, _ := s.inboxView(page)
	envBadge, _ := s.inboxView(inboxBadgeQuery("uat"))
	if envPage != envBadge || envBadge == badgeKey {
		t.Fatalf("env kapsamlı rozet sayfanın env anahtarını okumalı: %s vs %s", envPage, envBadge)
	}
	all := url.Values{"status": {"open"}, "limit": {"300"}, "sort": {"firstSeen"}, "dir": {"desc"}, "prio": {"P1,P2,P3"}}
	if k, _ := s.inboxView(all); k == badgeKey {
		t.Fatal("tüm öncelikler görünümü rozetle aynı anahtarı paylaşmamalı")
	}
}

// Sayfanın varsayılan sabitleri inboxBadgeQuery'nin varsaydığı gibi kalmalı;
// biri değişirse rozet sessizce başka bir görünümü sayar.
func TestInboxBadgeQueryMatchesPageDefaults(t *testing.T) {
	b, err := os.ReadFile("../../frontend/src/pages/Inbox.tsx")
	if err != nil {
		t.Skipf("Inbox.tsx okunamadı: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"const PRIO_DEFAULT = ['P1'] as const;",
		"const SORT_DEFAULT = { id: 'firstSeen', dir: 'desc' as const };",
		"const KIND_DEFAULT: readonly InboxKind[] = ['problem', 'exception', 'httperror', 'anomaly', 'incident'];",
		"limit: 300,",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("Inbox.tsx varsayılanı değişmiş (%q) — inboxBadgeQuery'yi birlikte güncelle", want)
		}
	}
	q := inboxBadgeQuery("")
	if q.Get("limit") != "300" || q.Get("prio") != "P1" || q.Get("sort") != "firstSeen" || q.Get("dir") != "desc" ||
		q.Get("minOcc") != "" || q.Get("since") != "" || q.Get("cat") != "" {
		t.Errorf("rozet sorgusu varsayılan görünümden sapıyor: %v", q)
	}
}

// :v2: — count'un anlamı değişti; önek mutasyon düşürmesi için aynen kalır.
func TestInboxCountKeyBumpedUnderPrefix(t *testing.T) {
	k := inboxCountKey("prod")
	if !strings.HasPrefix(k, "inbox:count:v2:") {
		t.Fatalf("rozet anahtarı sürümlenmeli (:v2:): %q", k)
	}
	if !strings.HasPrefix(k, inboxListCachePrefix) {
		t.Fatalf("rozet anahtarı önek düşürmesinin dışında: %q", k)
	}
	src := readSrc(t, "inbox.go")
	body := funcBody(src, "computeInboxCountFor")
	for _, want := range []string{
		"s.inboxView(inboxBadgeQuery(env))",
		"s.cachedJSON(gctx, key, inboxListTTL, false, build)",
		`"count":      view.Total,`,
		`"scanCapped": view.ScanCapped,`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rozet varsayılan görünümün derlemesinden okumuyor: %q eksik", want)
		}
	}
	for _, gone := range []string{"CountProblemsNotInStatuses", "CountIncidentsNotInStatuses", "CountActiveAnomalyEvents"} {
		if strings.Contains(body, gone) {
			t.Errorf("rozet hâlâ ayrı COUNT'la sayıyor: %s", gone)
		}
	}
}
