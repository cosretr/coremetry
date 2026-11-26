package copilot

// trace_narrative.go — v0.10.2482: trace açıklamasının ANLATIM çıktısının Go
// ayrıştırıcısı (FE frontend/src/lib/traceNarrative.ts parseTraceNarrative
// aynası) + deterministik ölçüler. Kullananlar: MCP explain_trace yapısal
// çıktısı (api/mcp_trace_narrative.go) ve evalset anlatım beklentileri
// (api/ai_evalset_narrative.go). SAF.

import (
	"regexp"
	"strings"
)

// TraceNarrativeLabelKeys — TÜR → dil-bağımsız anahtar (FE TRACE_LABEL_KEY;
// İngilizce adlar i18n'de: ai.tn.label.<anahtar>).
var TraceNarrativeLabelKeys = map[string]string{
	"Business hata": "business", "Bağımlılık hatası": "dependency", "Zaman aşımı": "timeout", "Yavaşlık": "slow",
	"Yetki hatası": "auth", "Altyapı hatası": "infra", "Başarılı": "ok", "Belirsiz": "unclear",
}

// TraceNarrative — ayrıştırılmış anlatım. Label kapalı kümeden ya da "".
type TraceNarrative struct {
	Label string
	Karar string
	Code  string // "Kod bulgusu" (yalnız kod dalı)
	Next  string
}

var (
	tnMarkdownRe = regexp.MustCompile(`\*\*|__`)
	tnLeadRe     = regexp.MustCompile(`^\s*(?:#{1,6}\s+|[-*•>]\s+)`)
	tnLabelPfxRe = regexp.MustCompile(`(?i)^(?:tür|tur|type|etiket)\s*:\s*`)
	tnParenRe    = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	tnWaitRe     = regexp.MustCompile(`(?i)^\s*[*_#>\s]*dış bağımlılık bekleme:`)
	tnKararRe    = regexp.MustCompile(`(?im)^[ \t]*(?:[*_#>-]+[ \t]*)*karar[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?`)
	tnCodeRe     = regexp.MustCompile(`(?im)^[ \t]*(?:[*_#>-]+[ \t]*)*kod[ \t]+bulgusu[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?`)
	tnNextRe     = regexp.MustCompile(`(?im)^[ \t]*(?:[*_#>-]+[ \t]*)*(?:önerilen[ \t]+)?sonraki[ \t]+adım[ \t]*(?:\*\*|__)?[ \t]*:[ \t]*(?:\*\*|__)?`)
)

func tnPlain(line string) string {
	return strings.TrimSpace(tnLeadRe.ReplaceAllString(tnMarkdownRe.ReplaceAllString(line, ""), ""))
}

// MatchTraceNarrativeLabel — satır tek başına bir TÜR etiketi mi.
func MatchTraceNarrativeLabel(line string) string {
	s := strings.TrimSpace(strings.TrimRight(tnLabelPfxRe.ReplaceAllString(tnPlain(line), ""), ".:"))
	for _, cand := range []string{s, strings.TrimSpace(tnParenRe.ReplaceAllString(s, ""))} {
		l := strings.ToLower(cand)
		for _, lab := range TraceNarrativeLabels {
			if strings.ToLower(lab) == l {
				return lab
			}
		}
	}
	if strings.ToLower(s) == "iş kuralı reddi" {
		return "Business hata"
	}
	return ""
}

func tnFlat(s string) string {
	var parts []string
	for _, ln := range strings.Split(s, "\n") {
		if p := tnPlain(ln); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
}

// ParseTraceNarrative — model çıktısı → {TÜR, KARAR, Kod bulgusu, Sonraki adım}.
// Sunucunun mekanik "dış bağımlılık bekleme" satırı atılır; KARAR işareti
// yoksa etiketten sonraki metin KARAR sayılır.
func ParseTraceNarrative(text string) TraceNarrative {
	var lines []string
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		if !tnWaitRe.MatchString(l) {
			lines = append(lines, l)
		}
	}
	i := 0
	for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
		i++
	}
	var out TraceNarrative
	if i < len(lines) {
		if out.Label = MatchTraceNarrativeLabel(lines[i]); out.Label != "" {
			i++
		}
	}
	rest := strings.Join(lines[i:], "\n")
	head := rest
	if m := tnNextRe.FindStringIndex(rest); m != nil {
		head, out.Next = rest[:m[0]], tnFlat(rest[m[1]:])
	}
	if m := tnCodeRe.FindStringIndex(head); m != nil {
		head, out.Code = head[:m[0]], tnFlat(head[m[1]:])
	}
	if m := tnKararRe.FindStringIndex(head); m != nil {
		head = head[m[1]:]
	}
	out.Karar = tnFlat(head)
	return out
}

// PlainText — anlatımın düz metin hâli (MCP `text`): etiket satırı + parçalar.
func (n TraceNarrative) PlainText() string {
	var b strings.Builder
	if n.Label != "" {
		b.WriteString(n.Label + "\n")
	}
	if n.Karar != "" {
		b.WriteString("KARAR: " + n.Karar + "\n")
	}
	if n.Code != "" {
		b.WriteString("Kod bulgusu: " + n.Code + "\n")
	}
	if n.Next != "" {
		b.WriteString("Sonraki adım: " + n.Next + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

var (
	tnQuotedRe   = regexp.MustCompile("\"[^\"]*\"|“[^”]*”|`[^`]*`|«[^»]*»")
	tnAbbrevRe   = regexp.MustCompile(`(?i)(^|\s)(ör|örn|vb|vs|bkz)\.`)
	tnSentenceRe = regexp.MustCompile(`[.!?…]+(\s|$)`)
)

// CountNarrativeSentences — SAF, sezgisel: tırnak içi alıntılar ve yaygın
// kısaltmalar atılır; ondalık (12.21) bölmez; sonda noktasız cümle de sayılır.
func CountNarrativeSentences(s string) int {
	s = tnQuotedRe.ReplaceAllString(s, "Q")
	s = tnAbbrevRe.ReplaceAllString(s, "$1$2")
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := len(tnSentenceRe.FindAllStringIndex(s, -1))
	if last := s[len(s)-1]; last != '.' && last != '!' && last != '?' && !strings.HasSuffix(s, "…") {
		n++
	}
	return n
}

var (
	tnTrimPunct = ".,;:!?()[]\"'“”«»`"
	tnCamelRe   = regexp.MustCompile(`[a-z][A-Z]`)
	tnHexRe     = regexp.MustCompile(`^[0-9a-fA-F]{16,}$`)
)

// CountCodeTokens — SAF, "ham alan dökümü" sezgisi: anlatımdaki kod biçimli
// sözcükler (anahtar=değer, süslü/köşeli parantez, alt çizgi, ≥2 noktalı
// tanımlayıcı, camelCase, ≥16 hex kimlik). allowed içindeki alıntılar
// (beklenen belirleyici mesaj) önce metinden çıkarılır: alıntı ceza almaz.
func CountCodeTokens(s string, allowed []string) int {
	for _, a := range allowed {
		if a = strings.TrimSpace(a); a != "" {
			s = strings.ReplaceAll(s, a, " ")
		}
	}
	n := 0
	for _, f := range strings.Fields(s) {
		t := strings.Trim(f, tnTrimPunct)
		if t == "" {
			continue
		}
		switch {
		case strings.ContainsAny(t, "={}[]_<>"),
			strings.Count(t, ".") >= 2 && !strings.ContainsAny(t, " "),
			tnCamelRe.MatchString(t),
			tnHexRe.MatchString(t):
			n++
		}
	}
	return n
}
