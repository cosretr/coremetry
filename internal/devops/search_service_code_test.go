package devops

// v0.10.1208 — search_code yanıt ayrıştırma: yalnız istenen depo, TFVC yok,
// satır/parça varsa alınır, parça tek satır ve kırpık, tavan.

import (
	"context"
	"strings"
	"testing"
)

func TestParseServiceCodeHits(t *testing.T) {
	raw := []byte(`{"results":[
	 {"path":"/src/Pin.java","repository":{"name":"bsa-atm","type":"git"},"versions":[{"branchName":"refs/heads/release"}],
	  "matches":{"content":[{"charOffset":10,"line":42,"codeSnippet":"throw new BSAException(\n\"MCA-00029\")"}]}},
	 {"path":"/x/Other.java","repository":{"name":"other-repo","type":"git"}},
	 {"path":"/$/tfvc/A.cs","repository":{"name":"bsa-atm","type":"tfvc"}},
	 {"path":"/src/NoLine.java","repository":{"name":"BSA-ATM","type":"git"},"matches":{"content":[{"charOffset":5}]}}
	]}`)
	hits, err := parseServiceCodeHits(raw, "bsa-atm", 5)
	if err != nil || len(hits) != 2 {
		t.Fatalf("%v %+v", err, hits)
	}
	if h := hits[0]; h.Path != "src/Pin.java" || h.Branch != "release" || h.Line != 42 || h.Snippet != `throw new BSAException( "MCA-00029")` {
		t.Errorf("%+v", h)
	}
	if h := hits[1]; h.Line != 0 || h.Snippet != "" {
		t.Errorf("satır yoksa boş: %+v", h)
	}
	if one, _ := parseServiceCodeHits(raw, "bsa-atm", 1); len(one) != 1 {
		t.Error("tavan")
	}
	if _, err := parseServiceCodeHits([]byte("x"), "r", 5); err == nil {
		t.Error("bozuk yanıt hata")
	}
}

// v0.10.1885 (operatör-raporlu: Endpoint sayfasında "rota araması başarısız:
// kod arama http 400 … Filter [Repository] is found but filter [Project] is
// not."). Depo süzgeci proje süzgeci OLMADAN gönderilmez: proje biliniyorsa
// ikisi birlikte, bilinmiyorsa süzgeçsiz + geniş $top (depo elemesi yanıtta).
func TestServiceSearchFiltersNeverRepositoryWithoutProject(t *testing.T) {
	f := serviceSearchFilters("SHOP", "orders-api")
	if p, _ := f["Project"].([]string); len(p) != 1 || p[0] != "SHOP" {
		t.Fatalf("proje süzgeci: %+v", f)
	}
	if r, _ := f["Repository"].([]string); len(r) != 1 || r[0] != "orders-api" {
		t.Fatalf("depo süzgeci: %+v", f)
	}
	for _, project := range []string{"", "  "} {
		if got := serviceSearchFilters(project, "orders-api"); got != nil {
			t.Fatalf("proje yokken süzgeç gönderilmemeli (sunucu 400 döner): %+v", got)
		}
		if serviceSearchTop(project, 10) != serviceSearchWideTop {
			t.Fatal("süzgeçsiz aramada geniş $top")
		}
	}
	if serviceSearchTop("SHOP", 10) != 10 {
		t.Fatal("süzgeçli aramada $top aynen")
	}
}

// Uçtan uca: sahte sunucu gerçek kuralı uygular (depo süzgeci projesiz → 400).
// Kart araması proje süzgeciyle gider ve başarısızlık notu düşmez.
func TestFetchOperationCard_SearchSendsProjectFilter(t *testing.T) {
	f, svc := opCardFixtureTFS(t, true)
	cc := svc.FetchOperationCard(context.Background(), opCardTestReq())
	if strings.Contains(cc.Reason, "araması başarısız") {
		t.Fatalf("arama düşmemeli: %s", cc.Reason)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.searchFilters) == 0 {
		t.Fatal("arama isteği gitmedi")
	}
	for _, fl := range f.searchFilters {
		if len(fl["Repository"]) > 0 && len(fl["Project"]) == 0 {
			t.Fatalf("depo süzgeci projesiz gitti: %+v", fl)
		}
		if len(fl["Project"]) == 0 {
			t.Fatalf("proje süzgeci bekleniyordu: %+v", fl)
		}
	}
}
