package api

// Log Search Faz 6 dilim 6.7 — servis log özeti ve "Bu logları açıkla" (6.3)
// AYNI deterministik katmanı kullanır (audit §8 kabul: "Aynı fonksiyon iki
// yerde (test)").

import (
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogDigestSharedBySurfaces(t *testing.T) {
	d := digestFixture()
	shared := renderLogDigestTR(d)
	explain := renderExplainLogsTR(logstore.Filter{Service: "checkout"}, d, nil, false, 0)
	summary := renderServiceLogSummaryTR("checkout", d)
	if !strings.Contains(explain, shared) || !strings.Contains(summary, shared) {
		t.Fatalf("iki yüzey ortak kanıt bölümünü birebir taşımalı\nexplain:\n%s\nsummary:\n%s", explain, summary)
	}
	if !strings.HasPrefix(summary, "SERVİS: checkout\n") {
		t.Fatalf("servis başlığı: %q", summary)
	}
	// İki handler da aynı okuyucuyu çağırır (kanıt aynı sorgudan).
	bodies := serverFuncBodies(t)
	for _, h := range []string{"copilotExplainLogs", "copilotExplainServiceLogs"} {
		b, ok := bodies[h]
		if !ok {
			t.Fatalf("handler %q yok", h)
		}
		if !strings.Contains(b, "s.buildLogDigest(") {
			t.Errorf("%s ortak katmanı (buildLogDigest) çağırmıyor", h)
		}
	}
}

func TestServiceLogsSummaryQuery(t *testing.T) {
	q := url.Values{"service": {" checkout "}, "env": {"prod"}, "from": {"1"}, "to": {"2"},
		"search": {"timeout"}, "severity": {"17"}, "traceId": {"abc"}, "scope": {"mine"}}
	got, ok := serviceLogsSummaryQuery(q)
	if !ok {
		t.Fatal("servisli sorgu reddedildi")
	}
	want := url.Values{"service": {"checkout"}, "env": {"prod"}, "from": {"1"}, "to": {"2"}}
	if got.Encode() != want.Encode() {
		t.Fatalf("got %s want %s", got.Encode(), want.Encode())
	}
	if _, ok := serviceLogsSummaryQuery(url.Values{"env": {"prod"}}); ok {
		t.Fatal("servissiz sorgu kabul edildi")
	}
}
