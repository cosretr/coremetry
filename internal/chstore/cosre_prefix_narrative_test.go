package chstore

import (
	"strings"
	"testing"
)

// TestCosrePrefixStatsNarrativeSeparate — v0.10.2482: anlatım koşuları ayrı
// sayılır; bölümlü üç sayım onları dışlar (oran bozulmaz).
func TestCosrePrefixStatsNarrativeSeparate(t *testing.T) {
	if PrefixStyleNarrative != "style=anlatim" {
		t.Fatal("işaret sabiti sorgudaki literal ile aynı olmalı")
	}
	if n := strings.Count(cosrePrefixStatsSQL, "position(detail, 'style=anlatim') = 0"); n != 3 {
		t.Errorf("bölümlü sayımların üçü de anlatımı dışlamalı: %d", n)
	}
	if n := strings.Count(cosrePrefixStatsSQL, "position(detail, 'style=anlatim') > 0"); n != 2 {
		t.Errorf("anlatım payda + eklendi sayımı: %d", n)
	}
	p := CosrePrefixStats{Applied: 4, Added: 1, NarrativeApplied: 10, NarrativeAdded: 10}.WithRates()
	if p.AddedRate != 0.25 {
		t.Errorf("anlatım koşuları bölümlü oranı etkilememeli: %v", p.AddedRate)
	}
}
