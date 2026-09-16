package api

import (
	"net/url"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.733 — traces listesi anahtarı kök tanımını YALNIZ Root süzgeci
// açıkken taşır (v0.5.187: cevabı değiştiren her girdi anahtarda; değiştirmeyen
// girdi anahtarı karıştırmaz).
func TestTracesRootDefKeySuffix(t *testing.T) {
	on := url.Values{"rootOnly": {"true"}}
	if got := tracesRootDefKeySuffix(on, chstore.TraceRootDefEntry); got != ":rd=entry" {
		t.Fatalf("root açık: %q", got)
	}
	if a, b := tracesRootDefKeySuffix(on, chstore.TraceRootDefStrict), tracesRootDefKeySuffix(on, chstore.TraceRootDefEntry); a == b {
		t.Fatal("iki tanım ayrışmalı")
	}
	for _, q := range []url.Values{{}, {"rootOnly": {"false"}}, {"rootOnly": {"auto"}}} {
		if got := tracesRootDefKeySuffix(q, chstore.TraceRootDefEntry); got != "" {
			t.Fatalf("root kapalı/auto: sonek olmamalı, %q", got)
		}
	}
}

// Giriş-kökü toplamı satırlardan türer: giriş servisli satırın tamamı,
// giriş servisi olmayan satırın yalnız tam köklüleri.
func TestRootCoverageEntryRoot(t *testing.T) {
	rows := []chstore.TraceRootCoverageRow{
		{EntryService: "gw", Traces: 100, WithRoot: 6},
		{EntryService: "", Traces: 50, WithRoot: 12},
	}
	if got := rootCoverageEntryRoot(rows); got != 112 {
		t.Fatalf("112 bekleniyor, %d", got)
	}
	tr, wr := rootCoverageTotals(rows)
	if tr != 150 || wr != 18 {
		t.Fatalf("toplamlar: %d/%d", tr, wr)
	}
}
