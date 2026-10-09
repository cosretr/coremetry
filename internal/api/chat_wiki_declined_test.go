package api

// v0.10.1137 (inceleme) — wiki ret / "sayfada yok" önek dedektörleri, cevap
// biçimi ekinin özet satırına (**Özet:**, **TL;DR:**, > [!ÖZET]) rağmen tutar.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestWikiDeclinedStripsLeadingSummary(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Wikide bulunamadı: eşleşen sayfa yok.", true},
		{"**Özet:** Wikide bulunamadı: eşleşen sayfa yok.", true},
		{"**Özet**: Wikide bulunamadı.", true},
		{"**TL;DR:** wikide BULUNAMADI.", true},
		{"**Kısaca:** Wikide bulunamadı.", true},
		{"> [!ÖZET]\n> Wikide bulunamadı: kaynaklarda yalnız deploy adımları var.", true},
		{"> [!NOTE] not\n> Wikide bulunamadı.", true},
		{"**Özet:** Bu konuda wiki boş.\n\nWikide bulunamadı: kaynaklar başka konuda.", true},
		{"**Özet:** Pipeline Jenkins'te [1].\n\nAyrıntı …", false},
		{"Pipeline adresi wikide bulunamadı diye düşünmeyin; adres [1]'de.", false},
		{"", false},
	}
	for _, c := range cases {
		if got := wikiDeclined(c.in); got != c.want {
			t.Errorf("wikiDeclined(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWikiNotInPageWithSummary(t *testing.T) {
	for in, want := range map[string]bool{
		"**Özet:** Wikide bulunamadı.":               true,
		"**Özet:** " + copilot.WikiNotInPageSentinel: true,
		"   ":                    true,
		"**Özet:** Link [1]'de.": false,
	} {
		if got := wikiNotInPage(in); got != want {
			t.Errorf("wikiNotInPage(%q) = %v, want %v", in, got, want)
		}
	}
}
