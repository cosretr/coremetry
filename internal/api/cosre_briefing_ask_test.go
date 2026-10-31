package api

// v0.10.1716 — Login Briefing dilim 7: "Sor" satırının context.briefingId'si.
// Ek yalnız kart kimliği eşleşince üretilir, sayı taşımaz; birleşim sinyali
// exchange_id = briefing ULID ile yazılır ve sistem sinyali doğrulamasından
// geçer.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func TestBriefingAskFromPayload(t *testing.T) {
	const id = "01JBRF0000000000000000000A"
	b := briefing.Briefing{ID: id, Lines: []briefing.Line{{Item: briefing.ItemRED, Text: "orders-api hata oranı %2.1",
		Rows: []briefing.Finding{{Service: "orders-api", Now: 2.1, Prev: 0.4}}}}}
	raw, _ := json.Marshal(b)
	for _, tc := range []struct {
		name, payload, id string
		line              int
		ok                bool
	}{
		{"eşleşir", string(raw), id, 0, true},
		{"kart yenilendi", string(raw), "01JBRF0000000000000000000B", 0, false},
		{"satır dışı", string(raw), id, 1, false},
		{"bozuk yük", "{", id, 0, false},
	} {
		add, item, ok := briefingAskFromPayload(tc.payload, tc.id, tc.line)
		if ok != tc.ok {
			t.Fatalf("%s: ok=%v want %v", tc.name, ok, tc.ok)
		}
		if !ok {
			if add != "" || item != "" {
				t.Errorf("%s: eşleşmeyen satır ek üretti: %q", tc.name, add)
			}
			continue
		}
		if item != briefing.ItemRED || !strings.Contains(add, `"orders-api"`) || !strings.Contains(add, "BRİFİNG SATIRI") {
			t.Errorf("%s: ek = %q item=%q", tc.name, add, item)
		}
		for _, bad := range []string{"2.1", "0.4", "%"} {
			if strings.Contains(add, bad) {
				t.Errorf("%s: ek sayı taşıyor (%q): %q", tc.name, bad, add)
			}
		}
	}
}

func TestBriefingAskSignal(t *testing.T) {
	const id = "01JBRF0000000000000000000A"
	g := briefingAskSignal(id, "xch1", 2, briefing.ItemOracle, true)
	if g.ExchangeID != id || g.Kind != cosrelearn.SigBriefingAsk {
		t.Fatalf("birleşim anahtarı: %+v", g)
	}
	if g.Detail != "exchange=xch1 line=2 item=oracle matched=1" {
		t.Errorf("detail = %q", g.Detail)
	}
	if _, err := validateSystemSignal(g); err != nil {
		t.Errorf("sistem sinyali doğrulaması: %v", err)
	}
	if d := briefingAskSignal(id, "x", 0, "", false).Detail; !strings.HasSuffix(d, "matched=0") {
		t.Errorf("eşleşmeyen: %q", d)
	}
}
