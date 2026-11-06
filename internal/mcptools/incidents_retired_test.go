package mcptools

// incidents_retired_test.go — v0.10.1828 (Incidents emekliliği, audit §16.1
// E5; operatör kararı 2026-11-05: "Incident sekmesi yerine Anomalies olsun").
// Ana anahtar kapalıyken CoSRE/MCP menüde olmayan Incidents sayfasına link
// üretmez: build_link page=incident → /anomalies?tab=groups, ürün yol
// tarifi "problems" konusu Anomalies'i anlatır ve bağlar. Açıkken aynen.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func withIncidents(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.IncidentsEnabled()
	chstore.SetIncidentsEnabled(on)
	t.Cleanup(func() { chstore.SetIncidentsEnabled(prev) })
}

func TestBuildLinkIncidentRetired(t *testing.T) {
	withIncidents(t, false)
	for _, a := range []buildLinkArgs{{Page: "incident", IncidentID: "i-9"}, {Page: "incident"}} {
		h, err := buildLink(a, "c-1", nil, "custom:1000-2000")
		if err != nil || h != "/anomalies?tab=groups" {
			t.Errorf("%+v: got %q err=%v, want /anomalies?tab=groups", a, h, err)
		}
	}
	withIncidents(t, true)
	if h, err := buildLink(buildLinkArgs{Page: "incident", IncidentID: "i-9"}, "c-1", nil, "custom:1000-2000"); err != nil || h != "/incident?id=i-9" {
		t.Errorf("açık: got %q err=%v", h, err)
	}
}

func TestProblemsGuideIncidentsSwitch(t *testing.T) {
	cases := []struct {
		on        bool
		lastHref  string
		titleHas  string
		stepsHas  string
		stepsLack string
	}{
		{true, "/incidents", "incident'lar", "Incidents sayfası", ""},
		{false, "/anomalies", "anomaliler", "Anomalies sayfası", "Incidents"},
	}
	for _, c := range cases {
		withIncidents(t, c.on)
		links := problemsGuideLinks("", c.on)
		if got := links[len(links)-1].Href; got != c.lastHref {
			t.Errorf("on=%v: son bağlantı %q, istenen %q", c.on, got, c.lastHref)
		}
		a, ok := GuideFor("problems", "", "", "")
		if !ok {
			t.Fatal("problems konusu yok")
		}
		if !strings.Contains(a.Title, c.titleHas) {
			t.Errorf("on=%v: başlık %q", c.on, a.Title)
		}
		joined := strings.Join(a.Steps, " ")
		if !strings.Contains(joined, c.stepsHas) || (c.stepsLack != "" && strings.Contains(joined, c.stepsLack)) {
			t.Errorf("on=%v: adımlar %q", c.on, joined)
		}
		for _, l := range a.Links {
			if !c.on && strings.HasPrefix(l.Href, "/incidents") {
				t.Errorf("emekliyken Incidents bağlantısı: %+v", l)
			}
		}
	}
}
