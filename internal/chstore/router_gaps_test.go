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
	}{{RouterGapKindLoop, 7}, {RouterGapKindGeneral, 3}, {"", 1}, {RouterGapKindGeneral, 2}, {RouterGapKindClarify, 4}} {
		addRouterGapTotal(&tot, c.kind, c.n)
	}
	if tot.Loop != 8 || tot.General != 5 || tot.Clarify != 4 {
		t.Errorf("toplam = %+v, want loop 8 general 5 clarify 4", tot)
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
	if routerGapKindExpr != `if(has_loop, 'loop', if(has_clarify, 'clarify', 'general'))` {
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
		"max(surface != ? AND NOT startsWith(surface, ?))",
		"max(startsWith(surface, ?))",
		"(surface IN (?) OR startsWith(surface, ?))",
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

// v0.10.2465 (operator-reported, 2026-11-22) — netleştirme turları (kaynak
// sorusu doc_or_entity_ask vb., LLM'siz) router boşluğu sayılır, tur başına
// TEK satır: etkileşim satırı "kök:turn" kökünde döngü / işaret satırlarıyla
// aynı tur anahtarına iner; bind sırası SQL'deki ? sırasıyla aynı.
func TestRouterGapClarifyTurns(t *testing.T) {
	for _, route := range []string{"doc_or_entity_ask", "source_ask", ""} {
		for _, deep := range []bool{false, true} {
			l := aisurface.TurnLabel(aisurface.TierClarify, route, deep)
			if !strings.HasPrefix(l, routerGapClarifyPrefix) {
				t.Errorf("netleştirme etiketi %q önekle (%q) eşleşmiyor", l, routerGapClarifyPrefix)
			}
		}
	}
	for _, other := range []string{aisurface.TierLoop, aisurface.TierGuided, aisurface.TierDocRows} {
		if l := aisurface.TurnLabel(other, "x", false); strings.HasPrefix(l, routerGapClarifyPrefix) {
			t.Errorf("%q netleştirme sayılmamalı", l)
		}
	}
	root := "0123456789abcdef0123456789abcdef"
	if got, _, _ := strings.Cut(aisurface.TurnExchangeID(root), aisurface.ExchangeSep); got != root {
		t.Fatalf("etkileşim satırı kökü %q, want %q (tur başına tek satır)", got, root)
	}
	args := routerGapTurnsArgs([]string{aisurface.Chat}, "since")
	if n := strings.Count(routerGapTurnsSQL(), "?"); n != len(args) {
		t.Fatalf("SQL %d bind, argüman %d", n, len(args))
	}
	want := []any{aisurface.ChatIntentNone, aisurface.ChatIntentNone, routerGapClarifyPrefix, routerGapClarifyPrefix}
	for i, w := range want {
		if args[i] != w {
			t.Errorf("arg[%d] = %v, want %v", i, args[i], w)
		}
	}
	if args[5] != routerGapClarifyPrefix || args[6] != "since" || args[7] != "since" {
		t.Errorf("WHERE argümanları = %v", args[4:])
	}
}

// v0.10.2472 — "Yeniden üret" turu (attempt=regenerate) aynı soruyu ikinci
// kez saymaz: tur anahtarı cosre_interactions'taki regenerate exchange'i
// değil, zaman sınırlı alt sorgu.
func TestRouterGapTurnsSkipRegenerate(t *testing.T) {
	sql := routerGapTurnsSQL()
	for _, want := range []string{"HAVING turn_key GLOBAL NOT IN (", "SELECT exchange_id FROM cosre_interactions", "attempt = 'regenerate'", "created_at >= ?)"} {
		if !strings.Contains(sql, want) {
			t.Errorf("routerGapTurnsSQL %q içermiyor", want)
		}
	}
}
