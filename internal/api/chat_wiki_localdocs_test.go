package api

// chat_wiki_localdocs_test.go — v0.10.1175 (Operator-reported: yüklenen
// Excel'de hostname ↔ appname soruları bulunamıyordu).

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestIdentifierTerms(t *testing.T) {
	got := identifierTerms("hstqzwx01 sunucusunda hangi app çalışıyor, kredi-onay-api nerede? 10.1.2.3")
	for _, want := range []string{"hstqzwx01", "kredi-onay-api", "10.1.2.3"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q yok: %v", want, got)
		}
	}
	for _, g := range got {
		if g == "hangi" || g == "app" || g == "nerede" {
			t.Errorf("kimlik olmayan terim: %q", g)
		}
	}
}

func TestRagWikiSelectIDsKeepsExactIdentifierUnderFloor(t *testing.T) {
	row := wiki.Hit{Score: 0.3} // kapsam yarıda: "uygulama" satırda yok
	row.WikiID, row.Path, row.Idx = wiki.LocalWikiID, "/envanter.xlsx", 4
	row.Text = "Envanter · satır 12 — Hostname: hstqzwx01.corp.local · AppName: kredi-onay-api"
	other := wiki.Hit{Score: 0.2}
	other.WikiID, other.Path, other.Idx = "devops", "/x", 0
	other.Text = "alakasız"
	if got := ragWikiSelect([]wiki.Hit{row, other}); len(got) != 0 {
		t.Fatalf("kök neden: taban altı isabet eski seçimde atılıyordu, şimdi %d", len(got))
	}
	got := ragWikiSelectIDs([]wiki.Hit{row, other}, identifierTerms("hstqzwx01 hangi uygulama"))
	if len(got) != 1 || got[0].Key() != row.Key() {
		t.Fatalf("kimliği birebir taşıyan satır seçilmeli: %+v", got)
	}
	if got := ragWikiSelectIDs([]wiki.Hit{row, other}, nil); len(got) != 0 {
		t.Fatal("kimlik yoksa davranış eskisi gibi")
	}
	a := []wiki.Hit{row}
	if m := appendNewHits(a, []wiki.Hit{row, other}); !reflect.DeepEqual(m, []wiki.Hit{row, other}) {
		t.Fatalf("appendNewHits: %+v", m)
	}
}
