package chstore

// router_gaps_test.go — v0.10.2134 (CoSRE router boşlukları D1). /ai › Router
// boşlukları: (1) satır türü yüzeyden (none işareti "general", serbest döngü
// "loop"); (2) toplam penceredeki GERÇEK sayı — eskiden handler yalnız ilk 50
// grubu topluyordu; (3) ön-getirmeyle tohumlanan döngü (chat-prefetch) liste
// dışı.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
)

func TestAddRouterGapTotal(t *testing.T) {
	var tot RouterGapTotals
	for _, c := range []struct {
		kind string
		n    uint64
	}{{RouterGapKindLoop, 7}, {RouterGapKindGeneral, 3}, {"", 1}, {RouterGapKindGeneral, 2}} {
		addRouterGapTotal(&tot, c.kind, c.n)
	}
	if tot.Loop != 8 || tot.General != 5 {
		t.Errorf("toplam = %+v, want loop 8 general 5", tot)
	}
}

func TestRouterGapKindExprMatchesLabels(t *testing.T) {
	labels := aisurface.RouterGapLabels()
	has := map[string]bool{}
	for _, l := range labels {
		has[l] = true
	}
	// İfade iki türü ayırır: none işareti general, kalan TEK üye loop. Kayda
	// üçüncü RouterGap yüzeyi eklenirse "loop" sessizce onu da yutardı.
	if len(labels) != 2 || !has[aisurface.Chat] || !has[aisurface.ChatIntentNone] {
		t.Fatalf("RouterGapLabels = %v: routerGapKindExpr yeniden düşünülmeli", labels)
	}
	if has[aisurface.ChatPrefetch] {
		t.Error("chat-prefetch router boşluğu sayılmamalı")
	}
	if routerGapKindExpr != `if(has_loop, 'loop', 'general')` {
		t.Errorf("tür ifadesi değişti: %s", routerGapKindExpr)
	}
}

// v0.10.2448 (operatör, 2026-11-22) — tek tur = tek satır: none işareti
// ("kök:chat-intent-none") ile döngü satırı ("kök") aynı tur anahtarına iner;
// son rota turda döngü varsa loop. Okuma tur başına toplar.
func TestRouterGapTurnsSQLGroupsByExchangeRoot(t *testing.T) {
	for _, want := range []string{
		"splitByChar('" + aisurface.ExchangeSep + "', exchange_id)[1]",
		"if(exchange_id = '', id,",
		"GROUP BY turn_key",
		"max(surface = ?)",
		"max(surface != ?)",
		"anyIf(user_email, user_email != '')",
	} {
		if !strings.Contains(routerGapTurnsSQL(), want) {
			t.Errorf("routerGapTurnsSQL %q içermiyor", want)
		}
	}
	// Kodek sözleşmesi: işaretin kökü döngü satırının kimliğiyle aynı.
	root := "0123456789abcdef0123456789abcdef"
	marker := aisurface.ChildExchangeID(root, aisurface.ChatIntentNone)
	if got, _, _ := strings.Cut(marker, aisurface.ExchangeSep); got != root {
		t.Fatalf("işaret kökü %q, want %q", got, root)
	}
}
