package api

// v0.10.1687 — Operator-reported (prod, 2026-10-30): CoSRE'de "<takım>
// takımının servisleri nasıl?" sorusu wiki içerik yoklamasından (chat_wiki_probe.go)
// cevaplandı — namespace'lere göre servis listesi + wiki'deki sahip grupları,
// hiç RED/hata/Problem yok. Kök neden: yoklamanın telemetri vetosu
// (wikiWeakCueVetoed) yalnız SERVİS adını tanıyordu; guided router'ın adı
// geçen takım dalı (team_services / team_exceptions / team_top_errors)
// vetoda yoktu, yani takım adı başlığı taşıyan bir wiki sayfası soruyu
// guided'dan ÖNCE kapıyordu. Sözleşme: router soruyu bir takıma çözüyorsa
// yoklama hiç aramaz; takım adsız içerik sorusu yoklamada kalır.
// Adlar sentetik (payments-core, orders-api).

import (
	"context"
	"testing"
)

var teamProbeSvcs = []string{"orders-api", "billing-worker", "ledger-api"}
var teamProbeTeams = []string{"payments-core", "platform-sre"}

const teamProbePage = "# Payments Core takımı\n## Servisler\nPayments Core takımının servisleri: orders-api (orders namespace), billing-worker (billing namespace).\n" +
	"## Sahipler\nSahip grup: payments-core; SRE: platform-sre."

// SAF: router takıma çözüyorsa veto.
func TestWikiProbeTeamRouted(t *testing.T) {
	cases := []struct {
		q    string
		want bool
	}{
		{"payments-core takımının servisleri nasıl?", true},
		{"payments-core servisleri nasıl", true},
		{"payments-core takımının hataları", true},
		{"payments-core", true},
		{"Payments Core takımının servisleri nasıl", true}, // boşluklu yazım da router'da takıma çözülür
		{"Sık karşılaşılan cache refresh hataları nelerdir?", false},
		{"orders-api hataları neler", false}, // servis — servis vetosu ayrı
	}
	for _, c := range cases {
		if got := wikiProbeTeamRouted(c.q, teamProbeSvcs, nil, teamProbeTeams); got != c.want {
			t.Errorf("%q: got %v want %v (route=%+v)", c.q, got, c.want, routeGuidedIntent(c.q, teamProbeSvcs, nil, teamProbeTeams, ""))
		}
	}
	if wikiProbeTeamRouted("payments-core takımının servisleri nasıl?", teamProbeSvcs, nil, nil) {
		t.Error("takım kataloğu boşken veto yok")
	}
}

// Davranışsal: takım adı başlıklı wiki sayfası varken takım sorusu wiki
// kademesine girmez (guided team_services'e kalır); takım adsız içerik
// sorusu yoklamada kalır.
func TestWikiProbeSkipsNamedTeamQuestion(t *testing.T) {
	st, api := withIndexedWiki(t, probePages(map[string]string{
		"/Platform/Payments Core takımı": teamProbePage,
		"/Platform/Cache Refresh Akışı":  cacheRefreshPage,
	}))
	seen := withFakeNarrator(t, "Payments Core takımının servisleri [1].")
	s := guidedTestServer(t)
	prev := wikiProbeNames
	wikiProbeNames = func(*Server, context.Context, bool) ([]string, []string, []string) {
		return teamProbeSvcs, nil, teamProbeTeams
	}
	t.Cleanup(func() { wikiProbeNames = prev })

	for _, q := range []string{"payments-core takımının servisleri nasıl?", "payments-core servisleri nasıl"} {
		_, ev, h := runProbeTier(t, s, sessionCtx(), q, wikiTierContext{})
		if h || len(ev) != 0 {
			t.Errorf("%q: takım sorusu wiki'den cevaplanmamalı (ev=%+v)", q, ev)
		}
	}
	if len(*seen) != 0 || len(api.searches) != 0 || st.searchQueries() != 0 {
		t.Errorf("takım sorusunda wiki araması olmamalı: model=%d ado=%v ch=%d", len(*seen), api.searches, st.searchQueries())
	}
	// Takım adsız içerik sorusu bayt bayt eski: yoklama cevaplar.
	if _, _, h := runProbeTier(t, s, sessionCtx(), cacheRefreshQ, wikiTierContext{}); !h {
		t.Error("cache refresh sorusu yoklamada kalmalı")
	}
}
