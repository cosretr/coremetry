package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// inbox_scan_test.go — v0.9.318. Pins the per-source candidate ceiling.
//
// The bug: every narrowing filter on /api/inbox (service, q, env, ownerTeam,
// sreTeam) runs on the MERGED list, but each source was fetched with a
// hardcoded LIMIT 200. So the narrow answered over a slice that had already
// been truncated by the source's own ordering. With 900 open exception
// groups, searching "OOMKill" could only ever match within the first 200 —
// the operator saw an empty table and read it as an empty queue.
//
// Same shape as the drawer (v0.9.306), pivots (v0.9.307) and entry points
// (v0.9.313): a filter present in the caller, absent in the callee.
func TestInboxSourceLimit(t *testing.T) {
	cases := []struct {
		name     string
		limit    int
		narrowed bool
		want     int
	}{
		// Unnarrowed: the old 200 stays the floor, so the common poll costs
		// exactly what it did before this change.
		{"default page, no filter", 200, false, 200},
		{"small page, no filter", 50, false, 200},

		// …but a page bigger than the floor must be satisfiable from ONE
		// source. The frontend asks for 300; at 200/source a source holding
		// 300 genuinely open rows could not fill the page it was asked for.
		{"page above the floor lifts the scan", 300, false, 300},
		{"max page", 500, false, 500},

		// Narrowed: scan the candidate set. The narrow can only REMOVE rows,
		// so an honest answer needs the candidates, not a page of them.
		{"search widens the scan", 200, true, inboxNarrowScan},
		{"service filter widens the scan", 50, true, inboxNarrowScan},
		{"max page still narrowed", 500, true, inboxNarrowScan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inboxSourceLimit(tc.limit, tc.narrowed); got != tc.want {
				t.Errorf("inboxSourceLimit(%d, %v) = %d, want %d",
					tc.limit, tc.narrowed, got, tc.want)
			}
		})
	}
}

// A narrowed scan must never be SMALLER than an unnarrowed one at the same
// page size: narrowing is the case that needs more candidates, not fewer.
// Pinned as a property because the two branches are easy to invert by hand.
func TestInboxSourceLimitNarrowNeverShrinks(t *testing.T) {
	for _, limit := range []int{1, 50, 200, 300, 500} {
		wide := inboxSourceLimit(limit, true)
		narrow := inboxSourceLimit(limit, false)
		if wide < narrow {
			t.Errorf("limit=%d: narrowed scan %d < unnarrowed %d", limit, wide, narrow)
		}
		if wide < limit || narrow < limit {
			t.Errorf("limit=%d: scan (%d/%d) cannot fill the requested page",
				limit, narrow, wide)
		}
	}
}

// ── v0.9.319 — server-side sort ──────────────────────────────────────────
//
// The page sorted the RETURNED rows client-side. Since the server ranks by
// priority and caps, "Last seen ascending" meant "the oldest of the
// priority-ranked top 300" — not "the oldest in the queue". Every column but
// priority answered a different question than its header claimed.

func TestNormalizeInboxSort(t *testing.T) {
	cases := []struct{ inID, inDir, wantID, wantDir string }{
		{"", "", "priority", "desc"},
		{"lastSeen", "asc", "lastSeen", "asc"},
		{"service", "desc", "service", "desc"},
		// A stale link from an older build must open a usable queue, not a
		// 400 — unknown falls back rather than erroring.
		// (v0.9.319 used "occurrences" as the unknown-id example; v0.9.331 made
		// it a real column, so the example moved to something still unknown.)
		{"runbook", "asc", "priority", "asc"},
		{"'; DROP", "asc", "priority", "asc"},
		// Anything that isn't exactly "asc" is descending.
		{"lastSeen", "sideways", "lastSeen", "desc"},
		// v0.9.331 — the Occurrences column is sortable, so the server has to
		// accept its id. Without this the header would silently fall back to
		// priority and the arrow would point at a sort that never happened.
		{"occurrences", "desc", "occurrences", "desc"},
		{"occurrences", "asc", "occurrences", "asc"},
	}
	for _, tc := range cases {
		gotID, gotDir := normalizeInboxSort(tc.inID, tc.inDir)
		if gotID != tc.wantID || gotDir != tc.wantDir {
			t.Errorf("normalizeInboxSort(%q,%q) = (%q,%q), want (%q,%q)",
				tc.inID, tc.inDir, gotID, gotDir, tc.wantID, tc.wantDir)
		}
	}
}

func inboxFixture() []InboxItem {
	return []InboxItem{
		{ID: "a", Priority: "P3", Service: "checkout", Title: "zeta", LastSeen: 300, Source: "Anomaly"},
		{ID: "b", Priority: "P1", Service: "Payments", Title: "alpha", LastSeen: 100, Source: "Exception"},
		{ID: "c", Priority: "P2", Service: "checkout", Title: "beta", LastSeen: 200, Source: "Alert rule"},
		{ID: "d", Priority: "P1", Service: "billing", Title: "gamma", LastSeen: 400, Source: "Exception"},
	}
}

func inboxIDs(items []InboxItem) string {
	out := ""
	for _, it := range items {
		out += it.ID
	}
	return out
}

// Occurrences sorts on the exception count, and the kinds that don't have one
// sort as 0 rather than being dropped or floated to the top — a triage list
// that hides incidents because they lack a count is worse than one that
// ranks them last.
func TestSortInboxItemsByOccurrences(t *testing.T) {
	items := []InboxItem{
		{ID: "low", Kind: "exception", Priority: "P2", Exception: &InboxExceptionRef{Occurrences: 3}},
		{ID: "high", Kind: "exception", Priority: "P2", Exception: &InboxExceptionRef{Occurrences: 900}},
		{ID: "none", Kind: "incident", Priority: "P2"},
	}
	sortInboxItems(items, "occurrences", "desc")
	if got := inboxIDs(items); got != "highlownone" {
		t.Errorf("occurrences desc = %q, want highlownone", got)
	}
	sortInboxItems(items, "occurrences", "asc")
	if got := inboxIDs(items); got != "nonelowhigh" {
		t.Errorf("occurrences asc = %q, want nonelowhigh", got)
	}
}

func TestSortInboxItems(t *testing.T) {
	cases := []struct {
		name, id, dir, want string
	}{
		// The historical rank, unchanged: P1 first, newest within a priority.
		// An operator who never touches a header must see the old page.
		{"default", "priority", "desc", "dbca"},
		{"priority asc", "priority", "asc", "acdb"},
		{"lastSeen asc", "lastSeen", "asc", "bcad"},
		{"lastSeen desc", "lastSeen", "desc", "dacb"},
		// Case-insensitive: "Payments" must not sort before "billing" just
		// because of a capital P.
		{"service asc", "service", "asc", "dca"}, // billing, checkout×2, Payments

		{"detail asc", "detail", "asc", "bcda"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items := inboxFixture()
			sortInboxItems(items, tc.id, tc.dir)
			got := inboxIDs(items)
			if tc.name == "service asc" {
				// billing(d) < checkout(c,a) < Payments(b) — the point is that
				// Payments lands LAST despite the capital.
				if got != "dcab" {
					t.Errorf("service asc = %q, want %q", got, "dcab")
				}
				return
			}
			if got != tc.want {
				t.Errorf("sort(%s,%s) = %q, want %q", tc.id, tc.dir, got, tc.want)
			}
		})
	}
}

// The triage tiebreak must NEVER flip with the header. Inside one service, a
// P1 stays above a P3 in both directions — otherwise sorting by service
// descending buries the urgent row at the bottom of its own group.
func TestSortInboxItemsTiebreakStaysTriageOrder(t *testing.T) {
	for _, dir := range []string{"asc", "desc"} {
		items := []InboxItem{
			{ID: "low", Priority: "P3", Service: "checkout", LastSeen: 900},
			{ID: "high", Priority: "P1", Service: "checkout", LastSeen: 100},
		}
		sortInboxItems(items, "service", dir)
		if items[0].ID != "high" {
			t.Errorf("dir=%s: tiebreak inverted — got %s first, want the P1", dir, items[0].ID)
		}
	}
}

// ── v0.9.320 — occurrence floor ──────────────────────────────────────────
//
// The floor shipped on /problems in v0.9.315 but not on the Inbox, which is
// the surface meant to REPLACE it. Operator: "1 defa Java timeout aldığı için
// problems'ta exception gözüküyor" — the merged queue had the same one-offs.

// v0.10.949 — ikinci dönüş "varsayılan kip": yalnız param yok/çöp iken true
// (taban + çoklu-servis istisnası); açık sayı (0 ve 5 dahil) istisnasız.
func TestNormalizeInboxMinOcc(t *testing.T) {
	cases := []struct {
		raw     string
		want    uint64
		defMode bool
	}{
		{"", 5, true},      // absent → the floor (v0.10.949: 5, istisnalı)
		{"5", 5, false},    // açık 5 = eski "5+ only" linki: istisnasız
		{"0", 0, false},    // explicit "show all" is honoured
		{"10", 10, false},  // the strip's second rung
		{"  7 ", 7, false}, // whitespace from a pasted URL
		{"-3", 5, true},    // nonsense → default, never an error
		{"abc", 5, true},
	}
	for _, tc := range cases {
		got, def := normalizeInboxMinOcc(tc.raw)
		if got != tc.want || def != tc.defMode {
			t.Errorf("normalizeInboxMinOcc(%q) = (%d, %v), want (%d, %v)", tc.raw, got, def, tc.want, tc.defMode)
		}
	}
}

func TestApplyInboxMinOcc(t *testing.T) {
	items := []InboxItem{
		{ID: "exc-1", Kind: "exception", Exception: &InboxExceptionRef{Occurrences: 1}},
		{ID: "exc-9", Kind: "exception", Exception: &InboxExceptionRef{Occurrences: 9}},
		{ID: "exc-5", Kind: "exception", Exception: &InboxExceptionRef{Occurrences: 5}},
		// An alert-rule Problem that fired ONCE is still a firing problem.
		// Neither problems nor anomalies carry an occurrence count, and
		// dropping a P1 because it fired once is the opposite of triage.
		{ID: "prob", Kind: "problem", Priority: "P1"},
		{ID: "anom", Kind: "anomaly"},
		// Defensive: an exception row with no ref must not be dropped by a
		// nil dereference guard that silently means "below the floor".
		{ID: "exc-nil", Kind: "exception"},
	}
	kept, hidden := applyInboxMinOcc(append([]InboxItem(nil), items...), 5, floorExemption{})
	if hidden != 1 {
		t.Errorf("hidden = %d, want 1 (only exc-1 is below 5)", hidden)
	}
	got := inboxIDs(kept)
	if got != "exc-9exc-5probanomexc-nil" {
		t.Errorf("kept = %q, want the 1-occurrence exception dropped and nothing else", got)
	}

	// Floor 0 = show all: no filtering, no hidden count, same slice — even
	// with an exempt set present (minOcc=0 short-circuits).
	all, none := applyInboxMinOcc(append([]InboxItem(nil), items...), 0, newFloorExemption([]string{"x"}))
	if none != 0 || len(all) != len(items) {
		t.Errorf("minOcc=0 filtered: kept %d/%d, hidden %d", len(all), len(items), none)
	}
}

// v0.10.949 — istisna kipi: tabanın altındaki exception VE httperror satırı
// ExemptBelow'un (SQL'le AYNI, SpreadExemptCap'le kırpılmış) kümesindeyse
// kalır; istisnasız kipte (açık ?minOcc=, sıfır floorExemption) aynı
// satırlar gizlenir.
// Spread yalnız UI işareti: Spread ≥ 2 olup kırpılmış kümede OLMAYAN satır
// (tavan dışı kalan) gizlenir — rozet/chip/problems sayımıyla ayrışmasın.
// Problem/anomaly/ref'siz satırlar hiç dokunulmaz.
func TestApplyInboxMinOccSpreadExempt(t *testing.T) {
	items := []InboxItem{
		{ID: "exc-solo", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "exc-solo", Occurrences: 2}},
		{ID: "exc-multi", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "exc-multi", Occurrences: 2, Spread: 3}},
		{ID: "http-multi", Kind: "httperror", Exception: &InboxExceptionRef{Fingerprint: "http-multi", Occurrences: 1, Spread: 2}},
		// Yayılımlı ama SpreadExemptCap dışında kalmış: SQL yolları onu
		// istisna saymıyor, Go ayrımı da saymamalı (regresyon).
		{ID: "exc-capped", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "exc-capped", Occurrences: 3, Spread: 2}},
		{ID: "exc-big-multi", Kind: "exception", Exception: &InboxExceptionRef{Fingerprint: "exc-big-multi", Occurrences: 50, Spread: 2}},
		{ID: "prob", Kind: "problem"},
		{ID: "anom", Kind: "anomaly"},
		{ID: "exc-nil", Kind: "exception"},
	}
	set := newFloorExemption([]string{"exc-multi", "http-multi"})
	kept, hidden := applyInboxMinOcc(append([]InboxItem(nil), items...), 5, set)
	bySpread, byRegressed := countFloorKept(kept, 5, set)
	if hidden != 2 || bySpread != 2 || byRegressed != 0 {
		t.Errorf("exempt: hidden=%d keptBySpread=%d keptRegressed=%d, want 2/2/0 (exc-solo + exc-capped gizli)", hidden, bySpread, byRegressed)
	}
	if got := inboxIDs(kept); got != "exc-multihttp-multiexc-big-multiprobanomexc-nil" {
		t.Errorf("exempt kept = %q", got)
	}
	kept, hidden = applyInboxMinOcc(append([]InboxItem(nil), items...), 5, floorExemption{})
	if bySpread, _ := countFloorKept(kept, 5, floorExemption{}); hidden != 4 || bySpread != 0 {
		t.Errorf("explicit: hidden=%d keptBySpread=%d, want 4/0", hidden, bySpread)
	}
	if got := inboxIDs(kept); got != "exc-big-multiprobanomexc-nil" {
		t.Errorf("explicit kept = %q", got)
	}
}

// v0.10.949 — keptBySpread / keptRegressed facet'lerden SONRA sayılır.
// İstisnayla tutulan <5 satır varsayılan triage ayarında P3'tür (P1 ≥500 /
// patlama ≥1000, P2 ≥100 ya da regressed); Inbox'ın varsayılan P1+P2
// görünümünde yalnız regressed olan kalır. Regressed satır yayılımlı olsa da
// keptRegressed'e sayılır (yayılımdan bağımsız görünürdü) — bir satır tek
// sayaçta.
func TestCountFloorKeptAfterFacets(t *testing.T) {
	mk := func() []InboxItem {
		return []InboxItem{
			{ID: "a-p3", Kind: "exception", Priority: "P3", Status: "new", Exception: &InboxExceptionRef{Fingerprint: "fa", Occurrences: 3, Spread: 2}},
			{ID: "b-p2", Kind: "exception", Priority: "P2", Status: "regressed", Exception: &InboxExceptionRef{Fingerprint: "fb", Occurrences: 2, Spread: 2}},
			{ID: "big-p1", Kind: "exception", Priority: "P1", Status: "new", Exception: &InboxExceptionRef{Fingerprint: "fbig", Occurrences: 7}},
		}
	}
	set := newFloorExemption([]string{"fa", "fb"})
	cases := []struct {
		name          string
		minOcc        uint64
		ex            floorExemption
		wantSpread    int
		wantRegressed int
	}{
		{"varsayılan kip, P1+P2 → yalnız regressed", 5, set, 0, 1},
		{"açık ?minOcc (istisnasız) → 0", 5, floorExemption{}, 0, 0},
		{"show all (minOcc=0) → 0", 0, set, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, _ := applyInboxMinOcc(mk(), tc.minOcc, tc.ex)
			items = applyInboxFacets(items, []string{"exception"}, []string{"P1", "P2"})
			if bs, br := countFloorKept(items, tc.minOcc, tc.ex); bs != tc.wantSpread || br != tc.wantRegressed {
				t.Errorf("countFloorKept=(%d, %d), want (%d, %d)", bs, br, tc.wantSpread, tc.wantRegressed)
			}
		})
	}
	// Facet öncesi sayım (eski davranış) 1+1 derdi — farkın kendisi hata.
	pre, _ := applyInboxMinOcc(mk(), 5, set)
	if bs, br := countFloorKept(pre, 5, set); bs != 1 || br != 1 {
		t.Fatalf("facet öncesi sayım=(%d, %d), want (1, 1) (testin ayırt ediciliği)", bs, br)
	}
}

// ── v0.9.321 — incidents as the fourth source ────────────────────────────

func TestInboxKeepsIncident(t *testing.T) {
	cases := []struct {
		incident, inbox string
		want            bool
	}{
		{"open", "open", true},
		{"acknowledged", "open", true}, // still open, just picked up
		{"resolved", "open", false},
		{"resolved", "all", true},
		// Defensive: a row written before the status field existed must never
		// be silently hidden.
		{"", "open", true},
	}
	for _, tc := range cases {
		if got := inboxKeepsIncident(tc.incident, tc.inbox); got != tc.want {
			t.Errorf("inboxKeepsIncident(%q,%q) = %v, want %v",
				tc.incident, tc.inbox, got, tc.want)
		}
	}
}

func TestIncidentToInbox(t *testing.T) {
	cases := []struct {
		name, severity, status, wantPrio string
	}{
		{"critical unacked", "critical", "open", "P1"},
		{"warning unacked", "warning", "open", "P2"},
		{"info unacked", "info", "open", "P3"},
		// Acknowledged = somebody is on it. Weaker claim on attention than an
		// untouched one, but still open — one rung down, not out of the queue.
		{"critical acked", "critical", "acknowledged", "P2"},
		{"warning acked", "warning", "acknowledged", "P3"},
		{"info acked", "info", "acknowledged", "P3"},
		// An unknown severity must not vanish or become urgent.
		{"unknown severity", "", "open", "P3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := incidentToInbox(chstore.Incident{
				ID: "inc-1", Title: "checkout down", Severity: tc.severity,
				Status: tc.status, Service: "checkout", StartedAt: 100, UpdatedAt: 900,
			})
			if it.Priority != tc.wantPrio {
				t.Errorf("priority = %s, want %s (reason %q)", it.Priority, tc.wantPrio, it.PriorityReason)
			}
			if it.PriorityReason == "" {
				t.Error("every row ships a reason — see CLAUDE.md triage")
			}
			if it.Kind != "incident" || it.ID != "incident:inc-1" {
				t.Errorf("kind/id = %s/%s", it.Kind, it.ID)
			}
			if it.Incident == nil || it.Incident.ID != "inc-1" {
				t.Error("drill-down ref missing — the row would open nothing")
			}
			if it.LastSeen != 900 {
				t.Errorf("lastSeen = %d, want the updated_at (900)", it.LastSeen)
			}
		})
	}

	// A never-updated incident must not sort as epoch-old: lastSeen falls
	// back to when it started.
	it := incidentToInbox(chstore.Incident{ID: "i", StartedAt: 500, UpdatedAt: 0})
	if it.LastSeen != 500 {
		t.Errorf("lastSeen fallback = %d, want startedAt 500", it.LastSeen)
	}
}

// v0.9.321 — the invalidation prefix must NOT carry the response-shape
// version. v0.9.319 bumped the key to :v3 while every mutation site still
// dropped "inbox:v2", so acknowledging a problem left it in the queue for a
// full TTL and nothing failed loudly. The prefix must match both the list key
// and the count key.
func TestInboxListCachePrefixMatchesKeys(t *testing.T) {
	listKey := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, nil, nil, inboxSubjectService)
	if !strings.HasPrefix(listKey, inboxListCachePrefix) {
		t.Errorf("list key %q is not dropped by prefix %q", listKey, inboxListCachePrefix)
	}
	if !strings.HasPrefix(inboxCountKey("prod"), inboxListCachePrefix) {
		t.Errorf("count key %q is not dropped by prefix %q", inboxCountKey("prod"), inboxListCachePrefix)
	}
	// It must not be so broad that it drops unrelated namespaces.
	if strings.HasPrefix("exc-groups:foo", inboxListCachePrefix) {
		t.Error("prefix is too broad — it would drop unrelated cache entries")
	}
}

// ── v0.9.322 — badge/list status agreement ───────────────────────────────
//
// Found by running the deployed build against real data: the badge said 29
// open incidents while the list showed 2.
//
// Cause: the badge counted `status IN (...)` in SQL, while the list fetched
// EVERY status ordered by started_at DESC, applied the LIMIT, and only then
// dropped the resolved rows in Go. On an install whose history is 99%
// resolved (local: 994 of the newest 1000 incidents), the LIMIT was entirely
// consumed by resolved rows and the open ones never entered the window.
//
// The narrow now runs in SQL for both, from ONE shared status set. This test
// pins the invariant that made them disagree: the SQL narrow and the Go
// keeper must classify every status identically. If they ever drift, the
// badge and the list drift with them.
func TestInboxStatusNarrowMatchesKeepers(t *testing.T) {
	inSQLNarrow := func(status string) bool {
		for _, s := range inboxDoneStatuses {
			if s == status {
				return false // excluded by SQL
			}
		}
		return true
	}
	// Every status either source can hold, including the empty one written
	// before the field existed and a value neither side knows.
	for _, status := range []string{"open", "acknowledged", "resolved", "", "investigating"} {
		sql := inSQLNarrow(status)
		if got := inboxKeepsProblem(status, "open"); got != sql {
			t.Errorf("problem status %q: SQL narrow keeps=%v but Go keeper keeps=%v — badge and list will disagree",
				status, sql, got)
		}
		if got := inboxKeepsIncident(status, "open"); got != sql {
			t.Errorf("incident status %q: SQL narrow keeps=%v but Go keeper keeps=%v — badge and list will disagree",
				status, sql, got)
		}
	}
}

func TestPickExcludedStatuses(t *testing.T) {
	// "all" must apply NO narrow — the pivot exists to show resolved rows.
	if got := pickExcludedStatuses("all"); got != nil {
		t.Errorf(`pickExcludedStatuses("all") = %v, want nil (no narrow)`, got)
	}
	for _, pivot := range []string{"open", "ignored", ""} {
		if got := pickExcludedStatuses(pivot); len(got) != len(inboxDoneStatuses) {
			t.Errorf("pickExcludedStatuses(%q) = %v, want the shared done set", pivot, got)
		}
	}
	// The set must NOT contain the empty status. Excluding '' would hide rows
	// written before the status field existed — the exact defensive case the
	// Go keepers were written for.
	for _, s := range inboxDoneStatuses {
		if s == "" {
			t.Error("inboxDoneStatuses excludes status-less rows — they would vanish silently")
		}
	}
}

// v0.9.322 — the honesty probe has to compare against what the store will
// ACTUALLY return, not what the handler asked for.
//
// Found by an audit of the v0.9.312→321 diff: inboxSourceLimit returns 2000
// under any narrow, but ListExceptionGroups clamps Limit to 500 and
// ListIncidents collapses anything over 1000 to 200. `len(rows) >= 2000` is
// then false for the two sources most likely to be truncated — so the source
// that WAS capped is exactly the one that could never say so, and the page
// stayed silent while answering over a slice.
func TestInboxEffectiveLimit(t *testing.T) {
	cases := []struct {
		name           string
		want, storeMax int
		expect         int
	}{
		{"incidents clamp the wide scan", inboxNarrowScan, inboxIncStoreMax, 1000},
		{"unclamped source gets what it asked for", inboxNarrowScan, inboxNoStoreMax, inboxNarrowScan},
		{"base scan is under the incident ceiling", inboxBaseScan, inboxIncStoreMax, inboxBaseScan},
		{"a page of 300 is under the incident ceiling", 300, inboxIncStoreMax, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inboxEffectiveLimit(tc.want, tc.storeMax); got != tc.expect {
				t.Errorf("inboxEffectiveLimit(%d, %d) = %d, want %d",
					tc.want, tc.storeMax, got, tc.expect)
			}
		})
	}

	// The property that makes the probe honest: the effective limit is
	// reachable. If it ever exceeded the store ceiling, `len(rows) >= limit`
	// could not fire even on a fully truncated read.
	for _, storeMax := range []int{inboxIncStoreMax} {
		for _, want := range []int{50, 200, 300, 500, 2000, 5000} {
			if got := inboxEffectiveLimit(want, storeMax); got > storeMax {
				t.Errorf("effective limit %d exceeds store ceiling %d — the cap flag could never fire",
					got, storeMax)
			}
		}
	}
}

// ── v0.9.330 — facets are server-side ────────────────────────────────────
//
// Operator-reported, prod: the Problems page rendered "Queue clear" while the
// same screen said "2144 kalemden ilk 300'i gösteriliyor". Kind and priority
// were CLIENT-side facets over a page the server had already capped at 300 by
// priority. On prod those 300 were all Incidents, so the v0.9.328
// exception-first default filtered them to nothing. The chips agreed with the
// emptiness — "Exceptions 0" — because they counted the returned page too.
//
// Both halves are fixed here: the facet runs before the cap, and the counts
// are taken before the facet.

func TestNormalizeInboxSet(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		// Absent / empty / all-unknown → the FULL set. Never empty: a stale
		// or hand-edited link must open the whole queue, not a blank page
		// that reads as "nothing is wrong".
		{"", inboxKindsAll},
		{"   ", inboxKindsAll},
		{"nonsense,garbage", inboxKindsAll},
		{"exception", []string{"exception"}},
		{"exception,incident", []string{"exception", "incident"}},
		{" exception , incident ", []string{"exception", "incident"}},
		// Unknown tokens are dropped, known ones survive — a partially stale
		// link still narrows to what it can.
		{"exception,bogus", []string{"exception"}},
		// Duplicates collapse.
		{"exception,exception", []string{"exception"}},
	}
	for _, tc := range cases {
		got := normalizeInboxSet(tc.raw, inboxKindsAll)
		if len(got) != len(tc.want) {
			t.Errorf("normalizeInboxSet(%q) = %v, want %v", tc.raw, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("normalizeInboxSet(%q) = %v, want %v", tc.raw, got, tc.want)
				break
			}
		}
	}
}

func inboxFacetFixture() []InboxItem {
	return []InboxItem{
		{ID: "e1", Kind: "exception", Priority: "P2"},
		{ID: "i1", Kind: "incident", Priority: "P1"},
		{ID: "i2", Kind: "incident", Priority: "P1"},
		{ID: "a1", Kind: "anomaly", Priority: "P3"},
		{ID: "p1", Kind: "problem", Priority: "P1"},
	}
}

// The counts must describe the whole set, not the filtered one — that is the
// difference between a chip that says "there are 3 incidents you're not
// looking at" and one that says "0" while hiding them.
func TestInboxFacetCountsArePreFacet(t *testing.T) {
	c := inboxFacetCounts(inboxFacetFixture())
	for kind, want := range map[string]int{
		"exception": 1, "incident": 2, "anomaly": 1, "problem": 1,
	} {
		if c[kind] != want {
			t.Errorf("counts[%s] = %d, want %d", kind, c[kind], want)
		}
	}
	for prio, want := range map[string]int{"P1": 3, "P2": 1, "P3": 1} {
		if c[prio] != want {
			t.Errorf("counts[%s] = %d, want %d", prio, c[prio], want)
		}
	}
	// Every vocabulary entry is present even at zero, so a chip renders "0"
	// rather than disappearing when its kind is empty.
	empty := inboxFacetCounts(nil)
	for _, k := range append(append([]string{}, inboxKindsAll...), inboxPriosAll...) {
		if _, ok := empty[k]; !ok {
			t.Errorf("counts is missing %q on an empty set", k)
		}
	}
}

func TestApplyInboxFacets(t *testing.T) {
	all := inboxFacetFixture()

	// The prod case: exception-only must return the exception, not nothing.
	got := applyInboxFacets(append([]InboxItem(nil), all...),
		[]string{"exception"}, inboxPriosAll)
	if len(got) != 1 || got[0].ID != "e1" {
		t.Errorf("kind=exception returned %d rows (%v), want just e1", len(got), got)
	}

	// Both facets AND together.
	got = applyInboxFacets(append([]InboxItem(nil), all...),
		[]string{"incident", "problem"}, []string{"P1"})
	if len(got) != 3 {
		t.Errorf("incident+problem × P1 returned %d rows, want 3", len(got))
	}

	// A full selection is a pass-through: the default view must pay nothing.
	got = applyInboxFacets(append([]InboxItem(nil), all...), inboxKindsAll, inboxPriosAll)
	if len(got) != len(all) {
		t.Errorf("full selection filtered %d → %d", len(all), len(got))
	}
}

// The facets change WHICH rows come back, so two operators on different
// facets must not share one cached page — the v0.5.187 shape.
func TestInboxFacetsInCacheKey(t *testing.T) {
	base := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, inboxKindsAll, inboxPriosAll, inboxSubjectService)
	exc := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, []string{"exception"}, inboxPriosAll, inboxSubjectService)
	p1 := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, inboxKindsAll, []string{"P1"}, inboxSubjectService)
	if base == exc || base == p1 || exc == p1 {
		t.Errorf("facet variants collide:\n base=%s\n exc=%s\n p1=%s", base, exc, p1)
	}
	// Order must NOT matter: ?kind=a,b and ?kind=b,a are one view.
	ab := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, []string{"exception", "incident"}, inboxPriosAll, inboxSubjectService)
	ba := inboxListKey("open", "", "", "", "", "", "", 200, "priority", "desc", 5, []string{"incident", "exception"}, inboxPriosAll, inboxSubjectService)
	if ab != ba {
		t.Errorf("facet order changed the key:\n %s\n %s", ab, ba)
	}
}

// ── v0.9.336 — the occurrence floor narrows in SQL ───────────────────────
//
// The floor was a Go filter over rows the store had already capped at 500 by
// last_seen. On an install whose recent exception groups are mostly one-offs
// — which is exactly what the operator complained about — that window filled
// with rows the floor was about to drop, so the queue showed a handful of
// real exceptions or none at all. Same lesson as the status narrow
// (v0.9.322): the LIMIT has to bite on rows that survive.
//
// The floor is now two fetches: above-floor rows to show, below-floor rows to
// COUNT. Both ride through the service / search / env / team narrows before
// applyInboxMinOcc splits them, so `hiddenByMinOcc` keeps meaning "rows that
// passed everything else and failed only the floor". A single SQL count would
// ignore those narrows and overstate it.
// readSrc reads a file from this package for shape assertions. The chstore
// package has its own mustReadSource; this is the api-side equivalent rather
// than an import cycle.
func readSrc(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestInboxFloorFetchesBothSides(t *testing.T) {
	src := readSrc(t, "inbox.go")

	if !strings.Contains(src, "MinOccurrences: minOcc") {
		t.Error("the floor must reach the exception query — otherwise the LIMIT is spent on rows it will drop")
	}
	if !strings.Contains(src, "MaxOccurrences: minOcc") {
		t.Error("the below-floor rows must be fetched too, or hiddenByMinOcc stops being countable after the Go narrows")
	}
	// v0.10.949 — çoklu-servis istisnası tabanla AYNI (üst) çekimde: LIMIT
	// görünecek satırlara harcansın (v0.9.336).
	iAbove := strings.Index(src, "MinOccurrences: minOcc,\n\t\t\t\tServices:       teamServices,")
	iExempt := strings.Index(src, "FloorExempt:          floorExempt,\n\t\t\t\tFloorExemptRegressed: floorDefault,\n\t\t\t\tHTTPErrors:           httpFilter,")
	iBelow := strings.Index(src, "MaxOccurrences: minOcc")
	if iAbove < 0 || iExempt < iAbove || iExempt > iBelow {
		t.Error("FloorExempt + FloorExemptRegressed üst (taban) çekiminde olmalı, alt çekimde değil")
	}
	// The split must still happen at the END, after every narrow.
	iSplit := strings.Index(src, "applyInboxMinOcc(items, minOcc, floorEx)")
	if iBelow < 0 || iSplit < 0 || iSplit < iBelow {
		t.Error("applyInboxMinOcc must run after both fetches and after the narrows")
	}
	// İşaretler tabandan ÖNCE: istisna kipi Exception.Spread'i okur.
	if iAnn := strings.Index(src, "annotateInboxSpread(items, sp)"); iAnn < 0 || iAnn > iSplit {
		t.Error("annotateInboxSpread applyInboxMinOcc'tan önce çağrılmalı")
	}
	// v0.10.949 — keptBySpread satır facet'lerinden SONRA, sıralama/tavandan
	// ÖNCE (total ile aynı küme): şerit tabloda olmayan satırı saymasın.
	iKept := strings.Index(src, "keptBySpread, keptRegressed := countFloorKept(items, minOcc, floorEx)")
	iCat := strings.Index(src, "items = applyInboxCategoryFacet(items, cats)")
	iSort := strings.Index(src, "sortInboxItems(items, sortID, sortDir)")
	if iKept < 0 || iCat < 0 || iSort < 0 || iKept < iCat || iKept > iSort {
		t.Errorf("countFloorKept facet'lerden sonra, sortInboxItems'tan önce olmalı (kept=%d cat=%d sort=%d)", iKept, iCat, iSort)
	}
	// Go ayrımı SQL'le aynı (kırpılmış) istisna kümesini kullanır.
	if !strings.Contains(src, "floorExempt = sp.ExemptBelow(minOcc)\n\t\t\tfloorEx = newFloorExemption(floorExempt)") {
		t.Error("floorEx ExemptBelow listesinden kurulmalı (SpreadExemptCap tavanı Go'da da geçerli)")
	}
	if !strings.Contains(src, `"spreadAvailable": sp != nil,`) {
		t.Error("inbox yanıtı yayılımın okunup okunmadığını söylemeli (soft-fail dürüstlüğü)")
	}
}

// The two fetches must be complements: nothing counted twice, nothing lost at
// the boundary. `>= n` and `< n` partition the set exactly.
func TestOccurrenceFloorBoundsArePartition(t *testing.T) {
	src := readSrc(t, "../chstore/exception_inbox.go")
	if !strings.Contains(src, `wc.add("occurrences >= ?", f.MinOccurrences)`) {
		t.Error("MinOccurrences must be inclusive (>=)")
	}
	if !strings.Contains(src, `wc.add("occurrences < ?", f.MaxOccurrences)`) {
		t.Error("MaxOccurrences must be EXCLUSIVE (<) — with >= on the other side, a row at exactly the floor would otherwise appear in both fetches and be counted as both shown and hidden")
	}
}

// ── v0.9.441 + v0.9.571 — exception aday bütçesi + arama pushdown ───────
//
// İKİ KEZ raporlandı, ikisi de aynı kök:
//
//	v0.9.441: "Exception'da gördüğüm kaydı Problems'te P3 seçsem de
//	          göremiyorum." (3.1K grup)
//	v0.9.571: "Gece bazı sql exception'ları gelmiş ama problems altında
//	          gözükmüyor." (2.4K grup, gece 03:04-03:06 arası biten
//	          ORA-18730 patlaması)
//
// Aday penceresi last_seen sıralı. 500'lük pencere, birkaç saat önce
// SONA ERMİŞ bir patlamayı yapısal olarak dışarıda bırakır: grup hâlâ
// açık, Exceptions sayfasında görünüyor, ama inbox'a hiç aday olmuyor.
//
// v0.9.441 bunu YALNIZ "tür filtresi sadece exception" hâlinde
// düzeltmişti; VARSAYILAN görünüm (tüm türler seçili) 500'de kaldı ve
// aynı hata beş sürüm sonra tekrar geldi. Ders: bütçe kaynağın
// MALİYETİNE bağlı olmalı, kaç tür seçili olduğuna değil.
//
// İki pin:
//  1. Exception bütçesi HER görünümde inboxExcScanMax — koşulsuz.
//  2. Arama STORE'a iner (ExceptionGroupFilter.Search) — eskiden Go'da
//     yalnız aday seti içinde aranıyordu; aday setine girmemiş kayıt
//     aramayla da bulunamıyordu.
func TestInboxExceptionBudgetIsUnconditional(t *testing.T) {
	src := readSrc(t, "inbox.go")

	// Bütçe bir TABAN olmalı, tavan değil. Bağlayıcı kısıt hiçbir zaman
	// tavan değildi: varsayılan görünüm daraltılmamış sayıldığı için
	// srcLimit = inboxBaseScan = 200 ve exception kaynağı 500'lük tavana
	// hiç değmiyordu. Tavanı yükseltmek NO-OP olurdu.
	if !strings.Contains(src, "excLimit := srcLimit") ||
		!strings.Contains(src, "if excLimit < inboxExcScanMax {") {
		t.Error("exception bütçesi TABAN olarak kurulmamış — srcLimit (varsayılan " +
			"görünümde 200) bağlayıcı kalır ve saatler önce bitmiş bir patlama " +
			"last_seen penceresine hiç giremez")
	}
	if inboxExcScanMax <= inboxBaseScan {
		t.Errorf("exception tabanı (%d) taban taramadan (%d) büyük olmalı — "+
			"aksi halde taban hiçbir şey eklemez", inboxExcScanMax, inboxBaseScan)
	}
	// Bütçe KOŞULA bağlanmamalı: v0.9.441 tam olarak bu yüzden yarım kaldı.
	if strings.Contains(src, "excLimit = inboxExcSoloMax") ||
		strings.Contains(src, "inboxKindsAllExcFamily(kinds)") {
		t.Error("exception bütçesi yine tür-filtresine koşullanmış — varsayılan " +
			"görünüm (tüm türler) dar tavanda kalır ve hata tekrar eder")
	}
	// NOT: sabit adını burada string olarak aramak, testin KENDİ
	// metnini yakalayan bir yanlış pozitif üretirdi (aynı tuzak
	// v0.9.564'te çıktı). Sabit silindiği için derleyici zaten
	// koruyor — kullanan kod derlenmez.
}

// v0.10.740 (operatör: "1 tane geldiyse dahil etme") — varsayılan taban 2.
// v0.10.949 (operatör 2026-09-26: "tek servisten gelen 5'ten küçük
// exception'ları göstermeyebiliriz") — varsayılan taban 5, çoklu-servis
// istisnalı; açık "0" hepsi, istisnasız.
func TestInboxDefaultMinOccHidesSingleServiceBelowFive(t *testing.T) {
	if inboxDefaultMinOcc != 5 {
		t.Fatalf("inboxDefaultMinOcc = %d, want 5", inboxDefaultMinOcc)
	}
	if got, def := normalizeInboxMinOcc(""); got != 5 || !def {
		t.Fatalf("param yokken taban 5 ve varsayılan kip olmalı, (%d, %v)", got, def)
	}
	if got, def := normalizeInboxMinOcc("0"); got != 0 || def {
		t.Fatalf("açık 0 = hepsi, istisnasız: (%d, %v)", got, def)
	}
}
