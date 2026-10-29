package cosrelearn

import (
	"math"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const baseScore = 0.5

// AnswerScore — SAF: sinyal türlerinden [0,1] puan; olumlu/olumsuz sayısı.
// Ayarlanabilir istemci sinyalleri (evidence_relevant / evidence_irrelevant /
// note) türden değil KART puanından gelir (cards: CardScores'un o cevaba ait
// kırpılmış değerleri); türleri yalnız olumlu/olumsuz sayımına girer
// (işaret = ayardaki ağırlık).
func AnswerScore(kinds []string, cards map[string]float64, w SignalWeights) (score float64, pos, neg int) {
	score = baseScore
	for _, k := range kinds {
		cw, configurable := w.For(k)
		if !configurable {
			cw = Weight[k]
			score += cw
		}
		switch {
		case cw > 0:
			pos++
		case cw < 0:
			neg++
		}
	}
	ids := make([]string, 0, len(cards))
	for id := range cards {
		ids = append(ids, id)
	}
	sort.Strings(ids) // sabit toplama sırası → bit-kararlı puan
	for _, id := range ids {
		score += ClampCardScore(cards[id])
	}
	return math.Max(0, math.Min(1, score)), pos, neg
}

// CardScores — SAF: exchange_id → kart (adım kimliği s<i>; mesaj notu "")
// → kırpılmış kart puanı. Yalnız penceredeki turlara düşen istemci
// sinyalleri sayılır (DeriveSignals ile aynı süzgeç). Ağırlığı 0 olan
// sinyal kart açmaz (not ağırlığı kilitli 0 → not hiç kart açmaz).
func CardScores(in Inputs, w SignalWeights) map[string]map[string]float64 {
	known := make(map[string]bool, len(in.Interactions))
	for _, r := range in.Interactions {
		known[r.ExchangeID] = true
	}
	sum := map[string]map[string]float64{}
	for _, g := range in.Signals {
		cw, ok := w.For(g.Kind)
		if !ok || cw == 0 || !known[g.ExchangeID] || !ClientSignalKinds[g.Kind] {
			continue
		}
		card := g.Detail
		if g.Kind == SigNote {
			card = messageCard
		}
		m := sum[g.ExchangeID]
		if m == nil {
			m = map[string]float64{}
			sum[g.ExchangeID] = m
		}
		m[card] += cw
	}
	for _, m := range sum {
		for card, v := range m {
			m[card] = ClampCardScore(v)
		}
	}
	return sum
}

// ScoreAnswers — SAF: penceredeki her tur için puan satırı (sinyalsiz tur
// taban puanı alır: "kimse bir şey demedi" nötrdür, iyi değil).
func ScoreAnswers(rows []chstore.CosreInteraction, derived map[string][]string, cards map[string]map[string]float64, w SignalWeights) []chstore.CosreAnswerQuality {
	out := make([]chstore.CosreAnswerQuality, 0, len(rows))
	for _, r := range rows {
		kinds := derived[r.ExchangeID]
		sc, pos, neg := AnswerScore(kinds, cards[r.ExchangeID], w)
		out = append(out, chstore.CosreAnswerQuality{
			ExchangeID: r.ExchangeID, CreatedAt: r.CreatedAt, Tier: r.Tier,
			Score: float32(sc), Positives: clampU8(pos), Negatives: clampU8(neg),
			Reasons: uniq(kinds),
		})
	}
	return out
}

// followUpNegative — "kullanıcı bu cevaptan sonra yeniden sordu/vazgeçti".
var followUpNegative = map[string]bool{
	SigRephrase: true, SigRegenerate: true, SigRetry: true, SigEdit: true, SigAbandon: true,
}

// ToolScore — SAF: başarı × (1 − yeniden-sorma oranı). chstore.WorstCosreTools aynı formül.
func ToolScore(okRate, rephraseRate float64) float64 {
	return okRate * (1 - rephraseRate)
}

// ScoreTools — SAF: gün × tool özetleri + koşu işareti (Tool "").
func ScoreTools(day time.Time, rows []chstore.CosreInteraction, derived map[string][]string) []chstore.CosreToolQuality {
	type acc struct {
		calls, ok, reph uint32
		sum             uint64
		ms              []uint32
	}
	by := map[string]*acc{}
	for _, r := range rows {
		neg := false
		for _, k := range derived[r.ExchangeID] {
			if followUpNegative[k] {
				neg = true
				break
			}
		}
		for _, t := range r.Tools {
			a := by[t.Name]
			if a == nil {
				a = &acc{}
				by[t.Name] = a
			}
			a.calls++
			if t.OK {
				a.ok++
			}
			if neg {
				a.reph++
			}
			a.sum += uint64(t.Ms)
			a.ms = append(a.ms, t.Ms)
		}
	}
	d := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	out := []chstore.CosreToolQuality{{Day: d, Tool: "", Score: 1}}
	names := make([]string, 0, len(by))
	for n := range by {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		a := by[n]
		okRate := float64(a.ok) / float64(a.calls)
		rr := float64(a.reph) / float64(a.calls)
		out = append(out, chstore.CosreToolQuality{
			Day: d, Tool: n, Calls: a.calls, OKCalls: a.ok, SumMs: a.sum,
			P95Ms: p95(a.ms), Rephrased: a.reph, Score: float32(ToolScore(okRate, rr)),
		})
	}
	return out
}

func p95(v []uint32) uint32 {
	if len(v) == 0 {
		return 0
	}
	s := append([]uint32(nil), v...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := int(math.Ceil(0.95*float64(len(s)))) - 1
	if i < 0 {
		i = 0
	}
	return s[i]
}

func clampU8(n int) uint8 {
	if n > 255 {
		return 255
	}
	return uint8(n)
}

func uniq(s []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
