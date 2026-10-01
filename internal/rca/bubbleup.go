package rca

// bubbleup.go — v0.10.991 — BubbleUp sonucunun "ayrışan boyut" özeti: TEK
// seçim + TEK birim dönüşümü. İki okuyucu paylaşır: verdict hakeminin kanıt
// kataloğu (extras.go, E-satırları) ve CoSRE kök-neden demetinin BubbleUp
// adımı (internal/api/copilot_bubbleup.go; dış skill denetimi 2026-09-19 V1).
//
// NEDEN ayrı ve saf: chstore.BubbleUpValue.SelectionPct / BaselinePct ORANDIR
// (0–1; frontend `* 100` ile çizer). extras.go bu oranı `%%%.0f` ile doğrudan
// basıyordu → gerçek veride "hatalı kümede %1, tabanda %0" (0.80 → "1",
// 0.11 → "0"); hakem yanlış sayıyı okuyordu. Test fikstürü alanlara 80 / 11
// yazdığı için (yanlış birim) yeşildi. Dönüşüm artık burada, bir kez.

import "github.com/cilcenk/coremetry/internal/chstore"

// BubbleUpTop — bir attribute'un en yüksek puanlı değeri; yüzdeler 0–100.
type BubbleUpTop struct {
	Key       string
	Value     string
	SelPct    float64 // seçim kümesindeki pay, 0–100
	BasePct   float64 // taban kümesindeki pay, 0–100
	SelCount  int64
	BaseCount int64
}

// TopBubbleUp — SAF: en çok `limit` attribute, her birinin EN ÜST değeri.
// Yalnız puanı (seçim payı − taban payı, oran) minScore'dan BÜYÜK olanlar:
// seçimde ayrışmayan boyut kanıt değildir. chstore attribute'ları en üst
// puana göre azalan verir; sıra korunur. nil / boş sonuç → nil.
func TopBubbleUp(bu *chstore.BubbleUpResult, limit int, minScore float64) []BubbleUpTop {
	if bu == nil || limit <= 0 {
		return nil
	}
	var out []BubbleUpTop
	for _, attr := range bu.Attributes {
		if len(out) >= limit {
			break
		}
		if len(attr.Values) == 0 {
			continue
		}
		v := attr.Values[0]
		if v.Score <= minScore {
			continue
		}
		out = append(out, BubbleUpTop{
			Key: attr.Key, Value: v.Value,
			SelPct: v.SelectionPct * 100, BasePct: v.BaselinePct * 100,
			SelCount: v.SelectionCount, BaseCount: v.BaselineCount,
		})
	}
	return out
}
