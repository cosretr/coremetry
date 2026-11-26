package api

// ai_evalset_narrative.go — v0.10.2482 (operatör onaylı): trace açıklamasının
// ANLATIM biçimi için deterministik (hakem modelsiz) evalset beklentileri.
// Yüzey "TraceNarrative" (copilot.SystemPromptTraceNarrative; üretim etiketi
// explain-trace, ctx traceNarrativeCtx ile aynı düşünme/tavan). Bölümlü
// "Trace" yüzeyi ve vakaları değişmez.
//
// Ölçüler (copilot.ParseTraceNarrative üzerinden; sunucunun mekanik "dış
// bağımlılık bekleme" satırı ayrıştırıcıda düşer):
//   - TÜR kapalı kümede VE fikstürün beklediği etiket;
//   - KARAR cümle sayısı [min,max] (varsayılan 3–6; sezgisel sayım:
//     alıntılar ve kısaltmalar atılır, ondalık bölmez);
//   - belirleyici mesaj / kod BİREBİR (harf duyarlı) cevapta;
//   - ham alan dökümü yok: KARAR + Sonraki adım'daki kod biçimli sözcük
//     sayısı ≤ N (varsayılan 6; beklenen alıntılar sayılmaz);
//   - Sonraki adım boş değil.

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// Varsayılan anlatım eşikleri.
const (
	evalNarrativeMinSentences = 3
	evalNarrativeMaxSentences = 6
	evalNarrativeMaxCodeToks  = 6
)

// evalNarrativeExpect — expect.narrative (yalnız TraceNarrative yüzeyi; orada zorunlu).
type evalNarrativeExpect struct {
	Label         string   `json:"label"`                   // beklenen TÜR (kapalı kümeden)
	Quotes        []string `json:"quotes,omitempty"`        // cevapta BİREBİR geçmesi gereken mesaj / kod
	MinSentences  int      `json:"minSentences,omitempty"`  // KARAR alt sınırı (0 = 3)
	MaxSentences  int      `json:"maxSentences,omitempty"`  // KARAR üst sınırı (0 = 6)
	MaxCodeTokens *int     `json:"maxCodeTokens,omitempty"` // nil = 6
}

// evalNarrativeFails — SAF: anlatım beklentisinin ihlalleri.
func evalNarrativeFails(e *evalNarrativeExpect, answer string) []string {
	if e == nil {
		return nil
	}
	var fails []string
	n := copilot.ParseTraceNarrative(answer)
	switch {
	case n.Label == "":
		fails = append(fails, "narrative: label missing or not in the closed set")
	case n.Label != e.Label:
		fails = append(fails, fmt.Sprintf("narrative: label %q, want %q", n.Label, e.Label))
	}
	lo, hi := e.MinSentences, e.MaxSentences
	if lo <= 0 {
		lo = evalNarrativeMinSentences
	}
	if hi <= 0 {
		hi = evalNarrativeMaxSentences
	}
	if c := copilot.CountNarrativeSentences(n.Karar); c < lo || c > hi {
		fails = append(fails, fmt.Sprintf("narrative: KARAR %d sentences, want %d-%d", c, lo, hi))
	}
	for _, q := range e.Quotes {
		if !strings.Contains(answer, q) {
			fails = append(fails, "narrative: decisive value not quoted verbatim: "+q)
		}
	}
	maxCode := evalNarrativeMaxCodeToks
	if e.MaxCodeTokens != nil {
		maxCode = *e.MaxCodeTokens
	}
	if c := copilot.CountCodeTokens(n.Karar+" "+n.Next, e.Quotes); c > maxCode {
		fails = append(fails, fmt.Sprintf("narrative: %d code-like tokens > %d (raw field dump)", c, maxCode))
	}
	if strings.TrimSpace(n.Next) == "" {
		fails = append(fails, "narrative: next step missing")
	}
	return fails
}

// evalNarrativeFixtureErr — SAF: fikstür doğrulaması ("" = geçerli).
func evalNarrativeFixtureErr(c evalCase) string {
	e := c.Expect.Narrative
	switch {
	case (c.Surface == evalSurfaceTraceNarrative) != (e != nil):
		return "narrative beklentisi yalnız TraceNarrative yüzeyinde ve orada zorunlu"
	case e == nil:
		return ""
	case !slices.Contains(copilot.TraceNarrativeLabels, e.Label):
		return fmt.Sprintf("narrative.label %q kapalı kümede değil", e.Label)
	case e.MinSentences < 0 || e.MaxSentences < 0 || (e.MaxSentences > 0 && e.MinSentences > e.MaxSentences):
		return "narrative cümle sınırları geçersiz"
	}
	for _, q := range e.Quotes {
		if !strings.Contains(c.User, q) {
			return "narrative.quotes kanıtta (user) birebir geçmeli: " + q
		}
	}
	return ""
}

// evalSurfaceTraceNarrative — anlatım biçiminin evalset yüzeyi.
const evalSurfaceTraceNarrative = "TraceNarrative"
