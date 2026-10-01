package rca

// v0.10.991 — BubbleUp yüzdeleri ORAN olarak basılıyordu: chstore
// SelectionPct / BaselinePct 0–1 aralığında oran taşır, katalog satırı ise
// `%%%.0f` ile doğrudan yazıyordu → gerçek veride "hatalı kümede %1, tabanda
// %0". Eski test fikstürü alanlara 80 / 11 yazdığı için hatayı çiviliyordu;
// buradaki fikstür chstore'un GERÇEK birimiyle (sayım / toplam) kurulur.

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// buVal — chstore.bubbleUpKey'in ürettiği şekil: pay = sayım / toplam (oran).
func buVal(value string, sel, selTotal, base, baseTotal int64) chstore.BubbleUpValue {
	sp, bp := float64(sel)/float64(selTotal), float64(base)/float64(baseTotal)
	return chstore.BubbleUpValue{Value: value, SelectionCount: sel, BaselineCount: base, SelectionPct: sp, BaselinePct: bp, Score: sp - bp}
}

func TestTopBubbleUp(t *testing.T) {
	bu := &chstore.BubbleUpResult{SelectionTotal: 40, BaselineTotal: 900, Attributes: []chstore.BubbleUpAttribute{
		{Key: "http.route", Values: []chstore.BubbleUpValue{buVal("/v1/pay-now", 32, 40, 99, 900), buVal("/v1/cart", 4, 40, 300, 900)}},
		{Key: "empty"},
		{Key: "pod", Values: []chstore.BubbleUpValue{buVal("api-gw-7f", 24, 40, 270, 900)}},
		{Key: "flat", Values: []chstore.BubbleUpValue{buVal("x", 4, 40, 90, 900)}},   // puan 0 → girmez
		{Key: "under", Values: []chstore.BubbleUpValue{buVal("y", 2, 40, 180, 900)}}, // puan < 0 → girmez
		{Key: "slight", Values: []chstore.BubbleUpValue{buVal("z", 5, 40, 90, 900)}}, // puan 0.025
		{Key: "version", Values: []chstore.BubbleUpValue{buVal("2.4.1", 20, 40, 90, 900)}},
	}}
	cases := []struct {
		name     string
		limit    int
		minScore float64
		want     []string
	}{
		{"puan > 0, tavan 3", 3, 0, []string{"http.route", "pod", "slight"}},
		{"eşik 0.05 küçük farkı eler", 3, 0.05, []string{"http.route", "pod", "version"}},
		{"tavan 1", 1, 0, []string{"http.route"}},
		{"tavan 0 → boş", 0, 0, nil},
	}
	for _, c := range cases {
		got := TopBubbleUp(bu, c.limit, c.minScore)
		var keys []string
		for _, g := range got {
			keys = append(keys, g.Key)
		}
		if strings.Join(keys, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: %v, istenen %v", c.name, keys, c.want)
		}
	}
	if TopBubbleUp(nil, 3, 0) != nil {
		t.Error("nil sonuç → nil")
	}
	top := TopBubbleUp(bu, 1, 0)[0]
	if top.Value != "/v1/pay-now" || top.SelPct != 80 || top.BasePct != 11 || top.SelCount != 32 || top.BaseCount != 99 {
		t.Errorf("yüzdeler 0–100, sayımlar ham olmalı: %+v", top)
	}
}

// Katalog satırı GERÇEK birimle doğru yüzdeyi basar (eski: "%1 … %0").
func TestCatalogExtBubbleUpPercentUnits(t *testing.T) {
	h := &chstore.RootCauseHypothesis{AnchorKind: "problem", AnchorID: "p1", Service: "checkout"}
	cat := BuildEvidenceCatalogExt(h, CatalogExtras{BubbleUp: &chstore.BubbleUpResult{
		SelectionTotal: 40, BaselineTotal: 900,
		Attributes: []chstore.BubbleUpAttribute{
			{Key: "http.route", Values: []chstore.BubbleUpValue{buVal("/v1/pay-now", 32, 40, 99, 900)}},
			{Key: "pod", Values: []chstore.BubbleUpValue{buVal("api-gw-7f", 24, 40, 270, 900)}},
		},
	}})
	var all []string
	for _, r := range cat.Refs {
		all = append(all, r.Text)
	}
	text := strings.Join(all, "\n")
	for _, want := range []string{
		"hatalarda ayrışan boyut: http.route=/v1/pay-now (hatalı kümede %80, tabanda %11)",
		"hatalarda ayrışan boyut: pod=api-gw-7f (hatalı kümede %60, tabanda %30)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("katalog %q içermeli:\n%s", want, text)
		}
	}
}
