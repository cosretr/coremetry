package api

// inbox_teams_test.go — v0.10.2121, Problems sadeleştirme S1: "Ekip"
// kolonunun çok ekipli satır kümesi (inbox_teams.go).

import (
	"reflect"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestInboxRowTeams(t *testing.T) {
	md := map[string]chstore.ServiceMetadata{
		"orders":   {OwnerTeam: "team-orders"},
		"payments": {SRETeam: "team-sre"}, // owner yok → SRE
		"ledger":   {OwnerTeam: "team-orders"},
		"nobody":   {},
	}
	exc := func(owner string, partners ...string) InboxItem {
		it := InboxItem{Kind: "exception", OwnerTeam: owner}
		it.Exception = &InboxExceptionRef{SpreadServices: partners}
		return it
	}
	cases := []struct {
		name string
		it   InboxItem
		want []string
	}{
		{"tek ekip", InboxItem{Kind: "problem", OwnerTeam: "team-orders"}, []string{"team-orders"}},
		{"owner yok, SRE", InboxItem{Kind: "anomaly", SRETeam: "team-sre"}, []string{"team-sre"}},
		{"sahipsiz", InboxItem{Kind: "problem"}, []string{}},
		{"yayılım: farklı ekipler sıralı tekil", exc("team-checkout", "orders", "payments", "ledger"), []string{"team-checkout", "team-orders", "team-sre"}},
		{"kataloğu olmayan / ekipsiz ortak atlanır", exc("team-checkout", "ghost", "nobody"), []string{"team-checkout"}},
	}
	for _, c := range cases {
		if got := inboxRowTeams(c.it, md); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, istenen %v", c.name, got, c.want)
		}
	}

	items := []InboxItem{exc("team-checkout", "orders"), {Kind: "problem", OwnerTeam: "team-orders", Teams: []string{"bayat"}}}
	annotateInboxTeams(items, md)
	if !reflect.DeepEqual(items[0].Teams, []string{"team-checkout", "team-orders"}) {
		t.Errorf("çok ekipli satır: %v", items[0].Teams)
	}
	if items[1].Teams != nil {
		t.Errorf("tek ekipli satırda Teams dolu: %v", items[1].Teams)
	}
}
