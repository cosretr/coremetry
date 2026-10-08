package chstore

// v0.10.1124 — "İndeksteki sayfalar" listesinin SQL şekli: sınırlı okuma,
// bağlı süzgeç argümanı, önizleme yalnız istenince.

import (
	"strings"
	"testing"
)

func TestWikiListSQLBounded(t *testing.T) {
	list, count, args := wikiListSQL("", false)
	assertBounded(t, "list", list)
	assertBounded(t, "count", count)
	if len(args) != 0 || strings.Contains(list, "content") || !strings.Contains(list, "LIMIT ? OFFSET ?") {
		t.Errorf("önizlemesiz liste içerik okumamalı: %s %v", list, args)
	}
	list, count, args = wikiListSQL("  Restart'; DROP  ", true)
	if len(args) != 2 || args[0] != "Restart'; DROP" || strings.Contains(list, "DROP") {
		t.Errorf("süzgeç bağlı argüman olmalı: %s %v", list, args)
	}
	if !strings.Contains(list, "substringUTF8(content, 1, 1000)") || !strings.Contains(count, "positionCaseInsensitiveUTF8(title, ?)") {
		t.Errorf("önizleme + süzgeç: %s\n%s", list, count)
	}
	if len(wikiPurgeTables) != 2 || wikiPurgeTables[0] != "wiki_pages" || wikiPurgeTables[1] != "wiki_chunks" {
		t.Errorf("temizlik iki wiki tablosunu da kapsamalı: %v", wikiPurgeTables)
	}
	long := strings.Repeat("ş", 500)
	if _, _, a := wikiListSQL(long, false); len([]rune(a[0].(string))) != 200 {
		t.Error("süzgeç 200 rune'a kırpılmalı")
	}
}
