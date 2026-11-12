package mcptools

// incidents_retired_test.go — v0.10.1828 (Incidents emekliliği, audit §16.1
// E5); Incidents ölü kod dilimi 3: anahtar dalı silindi. CoSRE/MCP silinmiş
// Incidents sayfasına link üretmez: build_link page=incident hata değil
// /anomalies?tab=groups döner; ürün yol tarifi "problems" konusu (incident
// sorusu da buraya eşlenir) Anomalies'i anlatır ve bağlar.

import (
	"strings"
	"testing"
)

func TestBuildLinkIncidentRetired(t *testing.T) {
	for _, a := range []buildLinkArgs{{Page: "incident"}, {Page: "incident", Service: "svc-a"}} {
		h, err := buildLink(a, "c-1", nil, "custom:1000-2000")
		if err != nil || h != "/anomalies?tab=groups" {
			t.Errorf("%+v: got %q err=%v, want /anomalies?tab=groups", a, h, err)
		}
	}
}

func TestProblemsGuidePointsToAnomalies(t *testing.T) {
	links := problemsGuideLinks("", "")
	if got := links[len(links)-1].Href; got != "/anomalies" {
		t.Errorf("son bağlantı %q, istenen /anomalies", got)
	}
	a, ok := GuideFor("", "incident nerede görülür", "", "")
	if !ok || !strings.Contains(a.Title, "anomaliler") {
		t.Fatalf("incident sorusu problems konusuna eşlenmeli: ok=%v %q", ok, a.Title)
	}
	joined := strings.Join(a.Steps, " ")
	if !strings.Contains(joined, "Anomalies sayfası") || strings.Contains(joined, "Incidents") {
		t.Errorf("adımlar %q", joined)
	}
	for _, l := range a.Links {
		if strings.HasPrefix(l.Href, "/incident") {
			t.Errorf("Incidents bağlantısı: %+v", l)
		}
	}
}
