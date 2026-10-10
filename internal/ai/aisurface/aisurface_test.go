package aisurface

import "testing"

// v0.10.1153 — etkileşim etiketi ve exchange kimliği kodeği (SAF, tablo).

func TestTurnLabelRoundTrip(t *testing.T) {
	cases := []struct {
		tier, route string
		deep        bool
		want        string
		wantTier    string
		wantRoute   string
	}{
		{TierScope, "", false, "cosre-turn:scope", TierScope, ""},
		{TierGuided, "service_health", false, "cosre-turn:guided/service_health", TierGuided, "service_health"},
		{TierWiki, "", true, "cosre-turn:wiki+deep", TierWiki, ""},
		{TierIntent, "root_cause", true, "cosre-turn:intent/root_cause+deep", TierIntent, "root_cause"},
		// Bilinmeyen kademe → other; kurala uymayan rota düşer (kardinalite).
		{"bogus", "Service Health", false, "cosre-turn:other", TierOther, ""},
		{TierLoop, "a:b", false, "cosre-turn:loop", TierLoop, ""},
		{TierLoop, "x_0123456789_0123456789_0123456789_0123456789", false, "cosre-turn:loop", TierLoop, ""},
	}
	for _, c := range cases {
		got := TurnLabel(c.tier, c.route, c.deep)
		if got != c.want {
			t.Errorf("TurnLabel(%q,%q,%v) = %q, want %q", c.tier, c.route, c.deep, got, c.want)
		}
		tier, route, deep, ok := ParseTurnLabel(got)
		if !ok || tier != c.wantTier || route != c.wantRoute || deep != c.deep {
			t.Errorf("ParseTurnLabel(%q) = %q %q %v %v", got, tier, route, deep, ok)
		}
		if !IsTurn(got) {
			t.Errorf("IsTurn(%q) false", got)
		}
	}
	for _, l := range []string{Chat, ChatGuided, "explain-trace:chat", "evalset-chat", ""} {
		if _, _, _, ok := ParseTurnLabel(l); ok || IsTurn(l) {
			t.Errorf("%q etkileşim etiketi sanıldı", l)
		}
	}
}

func TestExchangeIDCodec(t *testing.T) {
	const root = "0123456789abcdef0123456789abcdef"
	if got := ChildExchangeID(root, WikiSelect); got != root+":wiki-select" {
		t.Errorf("çocuk kimlik %q", got)
	}
	if got := TurnExchangeID(root); got != root+":turn" {
		t.Errorf("etkileşim kimliği %q", got)
	}
	// Çocuğun çocuğu yine köke bağlanır (iç içe önek birikmez).
	if got := ChildExchangeID(root+":turn", ChatIntent); got != root+":chat-intent" {
		t.Errorf("iç içe kimlik %q", got)
	}
	if got := ChildExchangeID("", ChatIntent); got != "" {
		t.Errorf("kök yokken çocuk kimlik boş kalmalı: %q", got)
	}
	for _, id := range []string{root, root + ":turn", root + ":chat-intent", root + ":explain-trace:chat"} {
		if ExchangeRoot(id) != root {
			t.Errorf("ExchangeRoot(%q) = %q", id, ExchangeRoot(id))
		}
	}
}

func TestRegistryDerivedLists(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range All() {
		if s.Label == "" || seen[s.Label] {
			t.Fatalf("boş ya da yinelenen etiket: %q", s.Label)
		}
		seen[s.Label] = true
		if s.Role < RoleAnswer || s.Role > RoleMarker {
			t.Errorf("%q rolü tanımsız", s.Label)
		}
		if IsTurn(s.Label) {
			t.Errorf("%q etkileşim önekiyle çakışıyor", s.Label)
		}
	}
	// Router-gap ve KB listeleri kayıttan türer (eski elle yazılmış değerler).
	if got := RouterGapLabels(); len(got) != 2 || got[0] != Chat || got[1] != ChatIntentNone {
		t.Errorf("RouterGapLabels = %v", got)
	}
	if got := NoKBLabels(); len(got) != 1 || got[0] != ChatGeneral {
		t.Errorf("NoKBLabels = %v", got)
	}
	if IsLLMCall(ChatOffTopic) || IsLLMCall(ChatIntentNone) || !IsLLMCall(WikiSelect) || !IsLLMCall("explain-problem") {
		t.Error("IsLLMCall işaret satırını ayırmıyor")
	}
}
