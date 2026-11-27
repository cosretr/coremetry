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
//   - KARAR cümle sayısı [min,max] (v0.10.2483 varsayılan 5–9; sezgisel
//     sayım: alıntılar ve kısaltmalar atılır, ondalık bölmez) ve en çok iki
//     paragraf (ham cevapta sayılır; ayrıştırıcı fazlasını katlar);
//   - belirleyici mesaj / kod BİREBİR (harf duyarlı) cevapta;
//   - v0.10.2483 — fikstürün LOGLAR bloğunda satır varsa en az bir satırın
//     saati cevapta AYNEN geçer (uydurma saat sayılmaz); LOGLAR "log
//     bulunamadı" / "okunamadı" diyorsa cevap bunu söyler;
//   - ham alan dökümü yok: KARAR + Sonraki adım'daki kod biçimli sözcük
//     sayısı ≤ N (v0.10.2483 varsayılan 8 — log paragrafı; beklenen
//     alıntılar sayılmaz);
//   - Sonraki adım boş değil.

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// Varsayılan anlatım eşikleri.
const (
	evalNarrativeMinSentences = 5
	evalNarrativeMaxSentences = 9
	evalNarrativeMaxParas     = 2
	evalNarrativeMaxCodeToks  = 8
)

var (
	// evalLogLineTimeRe — fikstür LOGLAR satırının saati ("- 13:02:11.204 ERROR …").
	evalLogLineTimeRe = regexp.MustCompile(`(?m)^(?:- )?(\d{2}:\d{2}:\d{2}\.\d{3}) [A-Z]+ `)
	evalKararParaRe   = regexp.MustCompile(`\n[ \t]*\n`)
)

// evalFixtureLogTimes — SAF: fikstürün LOGLAR bloğundaki satır saatleri.
func evalFixtureLogTimes(user string) []string {
	i := strings.Index(user, "\nLOGLAR (")
	if i < 0 && strings.HasPrefix(user, "LOGLAR (") {
		i = 0
	}
	if i < 0 {
		return nil
	}
	var out []string
	for _, m := range evalLogLineTimeRe.FindAllStringSubmatch(user[i:], -1) {
		out = append(out, m[1])
	}
	return out
}

// evalFixtureLogsAbsent — SAF: LOGLAR log olmadığını / okunamadığını mı söylüyor.
func evalFixtureLogsAbsent(user string) bool {
	return strings.Contains(user, "LOGLAR: bu trace için log bulunamadı") || strings.Contains(user, "LOGLAR: log deposu") ||
		strings.Contains(user, "LOGLAR: loglar okunamadı")
}

// evalRawKararParas — SAF: ham cevaptaki KARAR paragraf sayısı (ayrıştırıcı
// katlamadan önce; Sonraki adım / Kod bulgusu öncesi).
func evalRawKararParas(answer string) int {
	head := answer
	for _, cut := range []string{"\nSonraki adım:", "\n**Sonraki adım", "\nKod bulgusu:", "\n**Kod bulgusu"} {
		if i := strings.Index(head, cut); i >= 0 {
			head = head[:i]
		}
	}
	if i := strings.Index(head, "KARAR:"); i >= 0 {
		head = head[i:]
	}
	n := 0
	for _, p := range evalKararParaRe.Split(strings.TrimSpace(head), -1) {
		if strings.TrimSpace(p) != "" {
			n++
		}
	}
	return n
}

// evalNarrativeExpect — expect.narrative (yalnız TraceNarrative yüzeyi; orada zorunlu).
type evalNarrativeExpect struct {
	Label         string   `json:"label"`                   // beklenen TÜR (kapalı kümeden)
	Quotes        []string `json:"quotes,omitempty"`        // cevapta BİREBİR geçmesi gereken mesaj / kod
	MinSentences  int      `json:"minSentences,omitempty"`  // KARAR alt sınırı (0 = 5)
	MaxSentences  int      `json:"maxSentences,omitempty"`  // KARAR üst sınırı (0 = 9)
	MaxCodeTokens *int     `json:"maxCodeTokens,omitempty"` // nil = 8
}

// evalNarrativeLogFails — SAF (v0.10.2483): log paragrafı ölçüsü. Fikstürde
// log satırı varsa en az bir satırın saati cevapta aynen; LOGLAR log
// olmadığını söylüyorsa cevap da söyler.
func evalNarrativeLogFails(user, answer string) []string {
	if times := evalFixtureLogTimes(user); len(times) > 0 {
		for _, t := range times {
			if strings.Contains(answer, t) {
				return nil
			}
		}
		return []string{"narrative: no log line cited by its time (" + strings.Join(times, ", ") + ")"}
	}
	if evalFixtureLogsAbsent(user) {
		l := strings.ToLower(answer)
		if !strings.Contains(l, "log bulunamadı") && !strings.Contains(l, "loglar okunamadı") && !strings.Contains(l, "log okunamadı") {
			return []string{"narrative: fixture has no logs but the answer does not say so"}
		}
	}
	return nil
}

// evalNarrativeFails — SAF: anlatım beklentisinin ihlalleri (log ölçüsü
// fikstür kanıtına bağlı: evalNarrativeFailsFor).
func evalNarrativeFails(e *evalNarrativeExpect, answer string) []string {
	return evalNarrativeFailsFor(e, "", answer)
}

// evalNarrativeFailsFor — evalNarrativeFails + fikstür kanıtının log ölçüsü.
func evalNarrativeFailsFor(e *evalNarrativeExpect, user, answer string) []string {
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
	if p := evalRawKararParas(answer); p > evalNarrativeMaxParas {
		fails = append(fails, fmt.Sprintf("narrative: KARAR %d paragraphs, want at most %d", p, evalNarrativeMaxParas))
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
	return append(fails, evalNarrativeLogFails(user, answer)...)
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
