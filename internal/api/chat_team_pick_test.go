package api

// v0.10.1725 — CoSRE v2 "Takımını seç" kartı (operatör onayı, mockup):
// takım biçimli soru + no_team → data-scope teamPick; "Kapsamsız devam et"
// (context.unscoped) takımı bir daha sordurmaz; context.userTeam temizlenir.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestScopeUnscopedFlag(t *testing.T) {
	noTeam := userScope{Unscoped: true, Reason: scopeReasonNoTeam}
	noSvc := userScope{Unscoped: true, Reason: scopeReasonNoServices, Team: "payments"}
	team := userScope{Reason: scopeReasonTeam, Team: "payments", Services: []string{"orders-api"}}
	cases := []struct {
		name           string
		u              userScope
		need, declined bool
		wantPick       bool
		wantAdd        string
	}{
		{"no_team + takım biçimli → kart + sor eki", noTeam, true, false, true, copilot.ChatUserScopeAskTeamAddendum()},
		{"no_team + kapsamsız devam → kart yok, bir daha sorma", noTeam, true, true, false, copilot.ChatUserScopeUnscopedAddendum()},
		{"no_team + çapalı → kart yok, çapadan cevapla", noTeam, false, false, false, copilot.ChatUserScopeAnchoredAddendum()},
		{"no_team + çapalı + kapsamsız → çapa kazanır", noTeam, false, true, false, copilot.ChatUserScopeAnchoredAddendum()},
		{"no_services → kart yok (takım biliniyor)", noSvc, true, false, false, copilot.ChatUserScopeAskTeamAddendum()},
		{"no_services + kapsamsız devam", noSvc, true, true, false, copilot.ChatUserScopeUnscopedAddendum()},
		{"kapsamlı → kart yok, bayrak etkisiz", team, true, true, false, copilot.ChatUserScopeAddendum("payments", 1)},
		{"kaçış → ek yok", userScope{Unscoped: true, Reason: scopeReasonEscape}, true, true, false, ""},
	}
	for _, c := range cases {
		ev := scopeEventFor(c.u, c.need, c.declined)
		_, got := ev["teamPick"]
		if got != c.wantPick {
			t.Errorf("%s: teamPick = %v, want %v", c.name, got, c.wantPick)
		}
		if ev["reason"] != c.u.Reason {
			t.Errorf("%s: reason değişti: %v", c.name, ev["reason"])
		}
		if a := scopeAddendumFor(c.u, nil, c.need, c.declined); a != c.wantAdd {
			t.Errorf("%s: addendum = %q, want %q", c.name, a, c.wantAdd)
		}
	}
}

func TestRequestUserTeam(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  payments  ", "payments"},
		{strings.Repeat("x", requestUserTeamMax), strings.Repeat("x", requestUserTeamMax)},
		{strings.Repeat("x", requestUserTeamMax+1), ""},
	} {
		if got := requestUserTeam(c.in); got != c.want {
			t.Errorf("requestUserTeam(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Kart varken model takım listesi yazmaz / list_teams çağırmaz; "bir kez
// sor" işareti (teamAlreadyAsked) korunur.
func TestAskTeamAddendumNoListing(t *testing.T) {
	a := strings.ToLower(copilot.ChatUserScopeAskTeamAddendum())
	if !strings.Contains(a, copilot.ChatUserScopeAskMarker) {
		t.Fatalf("ask marker kayıp")
	}
	if !strings.Contains(a, "list_teams çağırma") || !strings.Contains(a, "takım listesi yazma") {
		t.Fatalf("ek modeli takım listelemekten men etmiyor: %q", a)
	}
}

func TestScopeNamesWithServices(t *testing.T) {
	cat := []teamCountRow{{"payments", 12}, {"pay-ops", 3}, {"orders", 7}}
	names, svcs := filterScopeTeams(cat, "pay", 20)
	if strings.Join(names, ",") != "payments,pay-ops" || len(svcs) != 2 || svcs[0] != 12 || svcs[1] != 3 {
		t.Fatalf("got %v %v", names, svcs)
	}
	names, svcs = filterScopeTeams(cat, "", 2)
	if len(names) != 2 || len(svcs) != 2 {
		t.Fatalf("limit: %v %v", names, svcs)
	}
}
