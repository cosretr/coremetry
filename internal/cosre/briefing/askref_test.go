package briefing

// v0.10.1716 — Login Briefing dilim 7: "Sor" satır bağlamı. Kimlik eşleşmesi
// (yenilenmiş kartın satırı eski tıklamaya bağlanmaz), satır sınırı, özne
// tekilleştirme + tavan ve SAYI taşımama sözleşmesi.

import (
	"reflect"
	"strings"
	"testing"
)

const testID = "01JBRF0000000000000000000A"

func askBriefing() Briefing {
	return Briefing{ID: testID, Lines: []Line{
		{Item: ItemRED, PageURL: "/service?name=orders-api", Rows: []Finding{
			{Service: "orders-api", Now: 2.1, Prev: 0.4, Text: "orders-api %2.1"},
			{Service: "payments-core", Label: "POST /pay", Now: 9},
			{Service: "orders-api"},
			{Service: "ledger"},
		}},
		{Item: ItemOracle, Rows: []Finding{{Service: "  billing\n-svc ", Label: strings.Repeat("x", 200)}}},
	}}
}

func TestValidID(t *testing.T) {
	for _, tc := range []struct {
		id string
		ok bool
	}{
		{testID, true},
		{"", false},
		{"b1", false},
		{"01JBRF000000000000000000AI", false}, // I yok
		{"01jbrf0000000000000000000a", false}, // küçük harf
		{testID + "0", false},
	} {
		if got := ValidID(tc.id); got != tc.ok {
			t.Errorf("ValidID(%q) = %v, want %v", tc.id, got, tc.ok)
		}
	}
}

func TestLineAskRef(t *testing.T) {
	b := askBriefing()
	for _, tc := range []struct {
		name string
		id   string
		line int
		want AskRef
		ok   bool
	}{
		{"kalem + özneler, tekil, tavan 3", testID, 0,
			AskRef{Item: ItemRED, PageURL: "/service?name=orders-api", Subjects: []string{"orders-api", "payments-core", "POST /pay"}}, true},
		{"kontrol karakteri + tavan", testID, 1,
			AskRef{Item: ItemOracle, Subjects: []string{"billing -svc", strings.Repeat("x", MaxAskSubjectRunes)}}, true},
		{"kart yenilendi → bağlanmaz", "01JBRF0000000000000000000B", 0, AskRef{}, false},
		{"satır yok", testID, 2, AskRef{}, false},
		{"negatif satır", testID, -1, AskRef{}, false},
		{"geçersiz kimlik", "b1", 0, AskRef{}, false},
	} {
		got, ok := LineAskRef(b, tc.id, tc.line)
		if ok != tc.ok || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: LineAskRef = %+v,%v want %+v,%v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

// Sayılar LLM'den geçmez: ek bağlamı bulgu değerlerini (Now/Prev/Text) taşımaz.
func TestLineAskRefCarriesNoValues(t *testing.T) {
	ref, _ := LineAskRef(askBriefing(), testID, 0)
	joined := strings.Join(ref.Subjects, " ")
	for _, bad := range []string{"2.1", "0.4", "%"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("özne %q değer taşıyor: %q", joined, bad)
		}
	}
}
