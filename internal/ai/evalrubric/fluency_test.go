package evalrubric

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestFluencyOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want Fluency
	}{
		{"düz", "checkout sağlıklı. p95 310 ms.", Fluency{Sentences: 2, Chars: 30}},
		{"kalıp açılış biçimli", "**Tabii**, hemen bakalım. checkout sağlıklı.", Fluency{BoilerplateOpening: true, Sentences: 2, Chars: 44}},
		{"İ katlama", "İşte durum: sorun yok.", Fluency{BoilerplateOpening: true, Sentences: 1, Chars: 22}},
		{"kapanış + soru", "Sorun yok. Başka bir sorunuz var mı?", Fluency{BoilerplateClosing: true, EndsWithQuestion: true, Sentences: 2, Chars: 36}},
		{"liste", "Bulgular:\n- p95 310 ms\n- hata %0,8", Fluency{Bullets: true, Sentences: 1, Chars: 34}},
		{"numaralı liste", "1) bir\n2) iki", Fluency{Bullets: true, Sentences: 1, Chars: 13}},
		{"Kaynak satırı gövdeye girmez", "Deploy yok.\n\nKaynak: deploy_impact", Fluency{Sentences: 1, Chars: 11}},
		{"Kaynak öncesi soru", "Bakayım mı?\n\n⚠ Kaynak: hiçbir araç", Fluency{EndsWithQuestion: true, Sentences: 1, Chars: 11}},
	} {
		if got := FluencyOf(tc.in); got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestBoilerplateListsMatchAuditS2 — kalıp listesi TEK yerde (bu paket) ve
// audit §4 S2 sorgusundaki dizilerle birebir: biri değişirse öteki de.
func TestBoilerplateListsMatchAuditS2(t *testing.T) {
	b, err := os.ReadFile("../../../docs/cosre/conversation-audit.md")
	if err != nil {
		t.Fatal(err)
	}
	doc := string(b)
	for name, want := range map[string][]string{"acilis_kaliplari": BoilerplateOpenings, "kapanis_kaliplari": BoilerplateClosings} {
		m := regexp.MustCompile(`(?s)\[([^\]]*)\] AS ` + name).FindStringSubmatch(doc)
		if m == nil {
			t.Fatalf("audit S2'de %s yok", name)
		}
		var got []string
		for _, q := range regexp.MustCompile(`'([^']*)'`).FindAllStringSubmatch(m[1], -1) {
			got = append(got, q[1])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s ayrıştı:\n belge %q\n kod   %q", name, got, want)
		}
		for _, p := range want {
			if p != strings.ToLower(p) {
				t.Errorf("%q küçük harf değil", p)
			}
		}
	}
}
