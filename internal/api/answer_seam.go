package api

// answer_seam.go — v0.10.2338 (docs/cosre/conversation-audit.md §6.0 "cevap
// dikişi", §6.1 D + E2; karar 9 önerildiği gibi): v2 emit zincirinin EN
// DIŞINDAKİ tek sarıcı. Bütün anlatım kademelerinin (guided, genel cevap,
// serbest döngü) metni buradan geçer; ikinci bir geri alma mekanizması yok —
// taslak (delta) zaten `answer` gelince çevirici tarafından geri alınır
// (uistream translator retract), dikiş yalnız taslağın NE ZAMAN görüneceğini
// ve otoriter `answer` metninin son hâlini belirler.
//
// D (bu sürüm, `cosre_conversation.style` arkasında):
//   - cevap: modelin metninin İLK ve SON cümlesi kalıp açılış / kapanışsa
//     (varsayılan liste evalrubric.BoilerplateOpenings/Closings + operatörün
//     bannedPhrases eki) cümle SİLİNİR. Bulgu taşıyan cümle (rakam / saat,
//     durumdaki ya da kanıttaki varlık adı, ters tırnaklı tanımlayıcı,
//     kesme işaretli özel ad) ya da tek cümle hiç silinmez. Yeniden üretim
//     yok. Sunucu metni (varsay-ve-söyle cümlesi, kaynak notu, soru) süzülmez:
//     süzgeç yalnız modelin metnine (answerEndClean'in gördüğü) uygulanır.
//   - taslak: ilk cümle bitene kadar tutulur (cümle kapısı), temizse çıkar,
//     kalıpsa hiç görünmez; sonrası geldiği gibi akar. Kapı en çok
//     seamFirstHoldRunes rune tutar (cümle sonu gelmese de bırakır).
//   - sunucunun kalıp satırları (karar 9): guided "Kaynak:" satırı ve genel
//     cevap notu metinden çıkar, adım çipine düşer (döngü emsali,
//     chat_source_note.go splitSourceNoteV2).
//
// Anahtar kapalıyken (ya da v1'de) sarıcı HİÇ kurulmaz: emit aynen.

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// seamFirstHoldRunes — ilk cümle kapısının tavanı: cümle sonu gelmese de bu
// kadar rune birikince taslak bırakılır (araç turu kapısıyla aynı ölçü,
// provider/tools_stream_gate.go 160). En kötü ek gecikme: modelin 160 rune
// (≈ 40–55 token) üretme süresi.
const seamFirstHoldRunes = 160

// generalNoteChipTR — genel cevap notunun v2 + üslup açıkken adım çipi hâli.
const generalNoteChipTR = "genel bilgiyle cevaplandı — soru telemetriyle eşleşmedi, cevap sistemin verisine dayanmıyor"

// answerSeam — bir turun dikişi (wrapEmit kurar; nil = sarıcı yok).
type answerSeam struct {
	ctx    context.Context
	t      *answerStyleTurn
	emit   func(string, any)
	style  bool
	banned seamBanned

	mu        sync.Mutex
	pending   string // tutulan taslak metni
	firstDone bool   // ilk cümle kararı verildi
	trimLead  bool   // silinen ilk cümlenin ardındaki boşluk atılacak
}

// newAnswerSeam — anahtar kapalıyken nil.
func newAnswerSeam(ctx context.Context, t *answerStyleTurn, emit func(string, any)) *answerSeam {
	if t == nil || !t.on {
		return nil
	}
	return &answerSeam{ctx: ctx, t: t, emit: emit, style: t.on,
		banned: seamBannedLists(chstore.CurrentCosreConversation().BannedPhrases)}
}

// emitFn — sarılmış emit.
func (g *answerSeam) emitFn(kind string, payload any) {
	switch kind {
	case "delta":
		if g.delta(payload) {
			return
		}
	case "answer":
		payload = g.answer(payload)
	case "error", "done":
		g.flush()
		if kind == "done" {
			g.t.recordClass(g.ctx)
		}
	}
	g.emit(kind, payload)
}

// ── taslak ────────────────────────────────────────────────────────────────

// seamDeltaFields — delta yükünün metni ve reset bayrağı.
func seamDeltaFields(payload any) (text string, reset, ok bool) {
	switch m := payload.(type) {
	case map[string]string:
		return m["text"], false, true
	case map[string]any:
		text, _ = m["text"].(string)
		reset, _ = m["reset"].(bool)
		return text, reset, true
	}
	return "", false, false
}

// delta — true = dikiş olayı kendisi yayınladı (ya da tuttu).
func (g *answerSeam) delta(payload any) bool {
	text, reset, ok := seamDeltaFields(payload)
	if !ok || !g.t.isNarrating() {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if reset {
		g.resetDraftLocked()
		if text == "" {
			g.emit("delta", payload)
			return true
		}
		g.emit("delta", map[string]any{"text": "", "reset": true})
	}
	g.pending += text
	if out := g.drainLocked(false); out != "" {
		g.emit("delta", map[string]any{"text": out})
	}
	return true
}

func (g *answerSeam) resetDraftLocked() {
	g.pending, g.firstDone, g.trimLead = "", false, false
}

// drainLocked — bırakılabilir taslak metni (tek parça; tek delta olur).
func (g *answerSeam) drainLocked(final bool) string {
	var out strings.Builder
	for g.pending != "" {
		if g.trimLead {
			g.pending = strings.TrimLeft(g.pending, " \t\r\n")
			if g.pending == "" {
				break
			}
			g.trimLead = false
		}
		if g.style && !g.firstDone {
			end := seamSentenceEnd(g.pending, final)
			if end < 0 {
				if final || utf8.RuneCountInString(g.pending) >= seamFirstHoldRunes {
					g.firstDone = true // tavan: cümle sonu yok, taslak bırakılır (otoriter cevap yine süzülür)
					continue
				}
				break
			}
			s := g.pending[:end]
			g.pending = g.pending[end:]
			g.firstDone = true
			if g.banned.opening(s) && !g.carriesFinding(s) {
				g.trimLead = true
				continue
			}
			out.WriteString(s)
			continue
		}
		out.WriteString(g.pending)
		g.pending = ""
	}
	return out.String()
}

// flush — `answer`sız biten akış (hata / done): tutulan taslak bırakılır.
func (g *answerSeam) flush() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == "" {
		return
	}
	if out := g.drainLocked(true); out != "" {
		g.emit("delta", map[string]any{"text": out})
	}
	g.pending = ""
}

// ── otoriter cevap ───────────────────────────────────────────────────────

// answer — `answer` yükünün son hâli: model metni süzülür, sonra (üslup
// açıkken) varsay-ve-söyle cümlesi başa eklenir.
func (g *answerSeam) answer(payload any) any {
	g.mu.Lock()
	g.resetDraftLocked()
	g.mu.Unlock()
	t := g.t
	model := strings.TrimSpace(t.takeModelText())
	clause := t.assumeClause()
	t.noteAssumeEvidence(verifyTurnFrom(g.ctx), clause)
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	text, _ := m["text"].(string)
	if i := strings.Index(text, model); model != "" && i >= 0 {
		region, n := seamFilter(model, g.banned, g.carriesFinding)
		if n > 0 {
			t.conv.noteSeamDropped(n)
			out := make(map[string]any, len(m))
			for k, v := range m {
				out[k] = v
			}
			out["text"] = text[:i] + region + text[i+len(model):]
			payload = out
		}
	}
	return withAssumeClause(payload, clause)
}

// ── süzgeç (SAF) ─────────────────────────────────────────────────────────

// seamBanned — katlanmış kalıp listeleri.
type seamBanned struct{ open, close []string }

// seamBannedLists — varsayılan liste (TEK kaynak: evalrubric, audit §4 S2) +
// operatörün ifadeleri (hem açılış hem kapanış olarak).
func seamBannedLists(extra []string) seamBanned {
	b := seamBanned{
		open:  append([]string(nil), evalrubric.BoilerplateOpenings...),
		close: append([]string(nil), evalrubric.BoilerplateClosings...),
	}
	for _, p := range extra {
		if f := seamFold(strings.Join(strings.Fields(p), " ")); f != "" {
			b.open = append(b.open, f)
			b.close = append(b.close, f)
		}
	}
	return b
}

var seamLeadMarkupRe = regexp.MustCompile(`^[\s*_>#-]+`)

// seamFold — S2 katlaması (evalrubric foldTR ile aynı): 'İ' → 'i', küçük harf.
func seamFold(s string) string { return strings.ToLower(strings.ReplaceAll(s, "İ", "i")) }

// opening — cümle (baştaki biçim işaretleri atılmış) kalıp açılışla başlıyor.
func (b seamBanned) opening(s string) bool {
	h := seamFold(seamLeadMarkupRe.ReplaceAllString(s, ""))
	for _, p := range b.open {
		if strings.HasPrefix(h, p) {
			return true
		}
	}
	return false
}

// closing — cümle kalıp kapanış içeriyor.
func (b seamBanned) closing(s string) bool {
	h := seamFold(s)
	for _, p := range b.close {
		if strings.Contains(h, p) {
			return true
		}
	}
	return false
}

// seamProperNounRe — kesme işaretli özel ad ("payments'ta", "checkout’un").
var seamProperNounRe = regexp.MustCompile(`\p{L}['’]\p{Ll}`)

// carriesFinding — cümle bulgu taşıyor mu: rakam (sayı, saat, tarih), kesme
// işaretli özel ad, ters tırnaklı tanımlayıcı / exception / "GET /yol" / pod
// biçimi, ya da durumdaki / kanıttaki / katalogdaki varlık adı.
func (g *answerSeam) carriesFinding(s string) bool {
	if strings.IndexFunc(s, unicode.IsDigit) >= 0 || seamProperNounRe.MatchString(s) {
		return true
	}
	if names, _ := verifyNameClaims(s, nil, false); len(names) > 0 {
		return true
	}
	l := seamFold(s)
	for _, n := range g.entityNames() {
		if len(n) >= 3 && containsBounded(l, seamFold(n)) {
			return true
		}
	}
	return false
}

// entityNames — turun varlık adları: konuşma durumu + doğrulayıcının adları ve kataloğu.
func (g *answerSeam) entityNames() []string {
	var out []string
	if st := chatContextFromCtx(g.ctx); st != nil {
		c := st.ctx
		out = append(out, c.Service, c.Cluster, c.Namespace, c.Workload, c.Pod, c.Operation, c.Env, c.Problem)
		out = append(out, c.Services...)
	}
	return append(out, verifyTurnFrom(g.ctx).entityNames()...)
}

// seamSentenceEnd — SAF: ilk cümle sonunun bitiş baytı (noktalama dahil) ya
// da satır sonunun indeksi; içerikten önceki boşluk/satır sınır değildir.
// Noktalama ardından boşluk gelirse sınırdır; metnin sonundaysa yalnız final
// akışta ya da "2." gibi sürebilecek bir sayı değilse. Yoksa -1.
func seamSentenceEnd(s string, final bool) int {
	content := false
	var prev rune
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if content {
			switch {
			case r == '\n':
				return i
			case strings.ContainsRune(".!?…", r):
				j := i + w
				if j < len(s) {
					if nx, _ := utf8.DecodeRuneInString(s[j:]); unicode.IsSpace(nx) {
						return j
					}
				} else if final || r != '.' || !unicode.IsDigit(prev) {
					return j
				}
			}
		}
		if !unicode.IsSpace(r) {
			content = true
		}
		prev = r
		i += w
	}
	return -1
}

// seamSentences — SAF: metnin cümle aralıkları [baş, son) (baştaki boşluk hariç).
func seamSentences(text string) [][2]int {
	var out [][2]int
	for off := 0; off < len(text); {
		rest := text[off:]
		lead := len(rest) - len(strings.TrimLeft(rest, " \t\r\n"))
		if lead == len(rest) {
			break
		}
		e := seamSentenceEnd(rest, true)
		if e < 0 {
			e = len(rest)
		}
		out = append(out, [2]int{off + lead, off + e})
		off += e
	}
	return out
}

// seamSentenceOK — süzgecin dokunabileceği cümle: kod çiti ya da tablo satırı değil.
func seamSentenceOK(s string) bool {
	return !strings.Contains(s, "```") && !strings.HasPrefix(strings.TrimSpace(s), "|")
}

// seamFilter — SAF: model metninin ilk cümlesi kalıp açılışsa, son cümlesi
// kalıp kapanışsa silinir — bulgu taşıyorsa (keep) ya da tek cümleyse asla.
// Dönüş: süzülmüş metin, silinen cümle sayısı.
func seamFilter(text string, b seamBanned, keep func(string) bool) (string, int) {
	sp := seamSentences(text)
	if len(sp) < 2 {
		return text, 0
	}
	first := text[sp[0][0]:sp[0][1]]
	lastSp := sp[len(sp)-1]
	last := text[lastSp[0]:lastSp[1]]
	dropFirst := seamSentenceOK(first) && b.opening(first) && !keep(first)
	dropLast := seamSentenceOK(last) && strings.Count(text[:lastSp[0]], "```")%2 == 0 && b.closing(last) && !keep(last)
	if dropFirst && dropLast && len(sp) == 2 {
		dropLast = false // ikisi silinirse cevap kalmaz: tek cümle kuralı
	}
	out, n := text, 0
	if dropLast {
		out = strings.TrimRight(out[:sp[len(sp)-2][1]], " \t\r\n")
		n++
	}
	if dropFirst {
		out = out[sp[1][0]:]
		n++
	}
	return out, n
}

// ── sunucu satırları (karar 9) ───────────────────────────────────────────

// guidedSourceNote — guided "Kaynak:" künyesi: v2 + üslup açıkken metne
// girmez, adım çipi olur (tek satırsa); aksi hâlde bugünkü ek aynen.
func guidedSourceNote(ctx context.Context, emit func(string, any), sources string) string {
	if !answerStyleOn(ctx) || strings.Contains(sources, "\n") || strings.TrimSpace(sources) == "" {
		return "\n\nKaynak: " + sources
	}
	emit("step", map[string]string{"label": "Kaynak: " + strings.TrimSpace(sources)})
	return ""
}

// emitGeneralNote — genel cevap notu: v2 + üslup açıkken adım çipi, aksi
// hâlde bugünkü gibi cevabın başında akan not.
func emitGeneralNote(ctx context.Context, emit func(string, any)) {
	if answerStyleOn(ctx) {
		emit("step", map[string]string{"label": generalNoteChipTR})
		return
	}
	emit("delta", map[string]string{"text": intentGeneralNoteTR + "\n\n"})
}

// generalAnswerText — intentGeneralAnswer + karar 9: üslup açıkken model
// cevabının başına not yazılmaz (not çipte).
func generalAnswerText(ctx context.Context, raw string) (string, bool) {
	text, general := intentGeneralAnswer(raw)
	if general && answerStyleOn(ctx) {
		return strings.TrimSpace(raw), true
	}
	return text, general
}
