package chstore

// router_gaps_test.go — v0.10.2134 (CoSRE router boşlukları D1). /ai › Router
// boşlukları: (1) satır türü yüzeyden (none işareti "general", serbest döngü
// "loop"); (2) toplam penceredeki GERÇEK sayı — eskiden handler yalnız ilk 50
// grubu topluyordu; (3) ön-getirmeyle tohumlanan döngü (chat-prefetch) liste
// dışı.

import (
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
	if routerGapKindExpr != `if(surface = ?, 'general', 'loop')` {
		t.Errorf("tür ifadesi değişti: %s", routerGapKindExpr)
	}
}
