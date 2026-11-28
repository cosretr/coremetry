package api

import (
	"strings"
	"testing"
)

// v0.10.2484 — kuyruk ve karne satırı anahtarları "ml:" önekli: oy PUT'u
// (putMLVerdict → cacheInvalidatePrefix "ml:") ikisini de düşürür, etiketlenen
// olay kuyruktan hemen çıkar. Kuyruk anahtarı tek girdisini (ISO hafta) taşır.
func TestMLLabelQueueCacheKeys(t *testing.T) {
	a, b := mlLabelQueueKey("2026-W48"), mlLabelQueueKey("2026-W49")
	if a == b || !strings.HasSuffix(a, "2026-W48") {
		t.Fatalf("hafta anahtarda olmalı: %s %s", a, b)
	}
	for _, k := range []string{a, mlScorecardRowsKey} {
		if !strings.HasPrefix(k, "ml:") {
			t.Errorf("%s: oy PUT'unun 'ml:' geçersizleştirmesine girmeli", k)
		}
	}
}
