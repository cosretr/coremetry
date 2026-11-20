package chstore

// v0.10.1282 — AI SRE Faz 0a: sre rolü, kuyruk, drain, durum. Kuyruk satırı
// saved_views(page='sre-investigation') sistem sayfası; okuma yalnız sistem
// satırlarını, sınırlı ve süreli okur.

import (
	"os"
	"strings"
	"testing"
)

func TestListSREInvestigationsSQL(t *testing.T) {
	for _, want := range []string{
		"FROM saved_views FINAL",
		"page = ?",
		"owner_id = ''",
		"startsWith(id, 'sre-')",
		"name IN ?",
		"ORDER BY created_at ASC",
		"LIMIT ?",
		"SETTINGS max_execution_time = 5",
	} {
		if !strings.Contains(listSREInvestigationsSQL, want) {
			t.Errorf("listSREInvestigationsSQL %q taşımalı:\n%s", want, listSREInvestigationsSQL)
		}
	}
	for _, want := range []string{"owner_id = ''", "startsWith(id, 'sre-')", "name = 'queued'", "SETTINGS max_execution_time"} {
		if !strings.Contains(countSREQueuedSQL, want) {
			t.Errorf("countSREQueuedSQL %q taşımalı", want)
		}
	}
	src, err := os.ReadFile("sre_investigation.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "lim := clampSREListLimit(limit)") ||
		!strings.Contains(string(src), "s.conn.Query(ctx, listSREInvestigationsSQL, SREInvestigationPage, states, lim)") {
		t.Error("ListSREInvestigations listSREInvestigationsSQL'i sayfa + kıskaçlı limitle kullanmalı")
	}
	for in, want := range map[int]int{0: 200, -1: 200, 1: 1, 50: 50, 200: 200, 201: 200, 10000: 200} {
		if got := clampSREListLimit(in); got != want {
			t.Errorf("clampSREListLimit(%d) = %d, %d beklenir", in, got, want)
		}
	}
	if SREInvestigationID("p-1") != "sre-p-1" {
		t.Errorf("SREInvestigationID = %q", SREInvestigationID("p-1"))
	}
}

func TestSREPageIsSystem(t *testing.T) {
	if !IsSystemSavedViewPage(SREInvestigationPage) {
		t.Fatal("sre-investigation bir sistem sayfası olmalı — genel /api/views ucu kuyruğu yazamamalı")
	}
	if IsSystemSavedViewPage("SRE-Investigation") {
		t.Fatal("tam eşitlik: büyük/küçük harf farkı sistem sayfası sayılmaz")
	}
}
