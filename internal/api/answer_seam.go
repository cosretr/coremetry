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
// E2 (v0.10.2339, `cosre_conversation.verification` = enforce; v0.10.2439'tan
// beri VARSAYILAN — önce shadow; işaretli cevaba not + takip çipi
// answer_seam_fallback.go):
//   - cevap: otoriter `answer`dan ÖNCE doğrulayıcı (answer_verify.go, SAF,
//     ≈3 ms) modelin son metninde koşar; uyuşmayan (mismatch) her değerin
//     hemen ardına " (doğrulanmamış değer)" yazılır — cümle silinmez (karar 6),
//     türetilen (derived) işaretlenmez. Sunucu metni işaretlenmez.
//   - yeniden üretim: yalnız Derin kipte, alışveriş başına en çok BİR; aynı
//     alışverişte yineleme düşüşü (chatDegenRetryLabel) ya da ağ geçidi
//     yeniden denemesi (chatRoundRetryLabel) olduysa hiç — üç mekanizma
//     üst üste binmez. Yeniden üretilen cevap da uyuşmazsa işaretlenir.
//   - taslak: cümle cümle bırakılır; her cümle O ANA DEK toplanan kanıtla
//     denetlenir ve yalnız bütün iddiaları "doğrulandı" ise görünür. İlk
//     denetlenemeyen cümlede taslak DONAR (sonrası da tutulur); otoriter
//     cevap gelince taslak zaten geri alınır. Yani kanıtta olmayan değer
//     taslakta hiçbir an görünmez.
//
// Anahtarlar kapalıyken (ya da v1'de) sarıcı HİÇ kurulmaz: emit aynen.

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// seamFirstHoldRunes — ilk cümle kapısının tavanı: cümle sonu gelmese de bu
// kadar rune birikince taslak bırakılır (araç turu kapısıyla aynı ölçü,
// provider/tools_stream_gate.go 160). En kötü ek gecikme: modelin 160 rune
// (≈ 40–55 token) üretme süresi.
const seamFirstHoldRunes = 160

// verifyUnverifiedNote — zorla kipinin satır içi notu (karar 6).
const verifyUnverifiedNote = "(doğrulanmamış değer)"

// seamRegenMaxTokens — yeniden üretim notunda adı geçen en çok değer.
const seamRegenMaxTokens = 6

// generalNoteChipTR — genel cevap notunun v2 + üslup açıkken adım çipi hâli.
const generalNoteChipTR = "genel bilgiyle cevaplandı — soru telemetriyle eşleşmedi, cevap sistemin verisine dayanmıyor"

// answerSeam — bir turun dikişi (wrapEmit kurar; nil = sarıcı yok).
type answerSeam struct {
	ctx     context.Context
	t       *answerStyleTurn
	emit    func(string, any)
	style   bool // D (üslup)
	enforce bool // E2 (doğrulama = zorla)
	banned  seamBanned

	mu           sync.Mutex
	pending      string // tutulan taslak metni
	firstDone    bool   // ilk cümle kararı verildi
	trimLead     bool   // silinen ilk cümlenin ardındaki boşluk atılacak
	frozen       bool   // E2: denetlenemeyen cümle — taslak otoriter cevaba dek durur
	regenUsed    bool   // E2: alışverişin tek yeniden üretimi harcandı
	regenBlocked bool   // E2: yineleme düşüşü ya da ağ geçidi yeniden denemesi oldu
}

// newAnswerSeam — iki anahtar da kapalıyken nil.
func newAnswerSeam(ctx context.Context, t *answerStyleTurn, emit func(string, any)) *answerSeam {
	if t == nil {
		return nil
	}
	enforce := verifyEnforceOn(ctx)
	if !t.on && !enforce {
		return nil
	}
	return &answerSeam{ctx: ctx, t: t, emit: emit, style: t.on, enforce: enforce,
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
	case "step":
		g.observeStep(payload)
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
	g.pending, g.firstDone, g.trimLead, g.frozen = "", false, false, false
}

// observeStep — E2: diğer iki yeniden deneme mekanizması bu alışverişte koştu mu.
func (g *answerSeam) observeStep(payload any) {
	m, ok := payload.(map[string]string)
	if !ok || (m["label"] != chatDegenRetryLabel && m["label"] != chatRoundRetryLabel) {
		return
	}
	g.mu.Lock()
	g.regenBlocked = true
	g.mu.Unlock()
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
				if final || (!g.enforce && utf8.RuneCountInString(g.pending) >= seamFirstHoldRunes) {
					g.firstDone = true // tavan: cümle sonu yok, taslak bırakılır (otoriter cevap yine süzülür)
					continue
				}
				break
			}
			g.firstDone = true
			if s := g.pending[:end]; g.banned.opening(s) && !g.carriesFinding(s) {
				g.pending = g.pending[end:]
				g.trimLead = true
				continue
			}
			if !g.enforce {
				out.WriteString(g.pending[:end])
				g.pending = g.pending[end:]
			}
			continue // zorla kipinde aynı cümle aşağıdaki denetimden geçer
		}
		if g.enforce {
			if g.frozen {
				break
			}
			end := seamSentenceEnd(g.pending, final)
			if end < 0 {
				if !final {
					break
				}
				end = len(g.pending)
			}
			if !g.draftPasses(g.pending[:end]) {
				g.frozen = true
				break
			}
			out.WriteString(g.pending[:end])
			g.pending = g.pending[end:]
			continue
		}
		out.WriteString(g.pending)
		g.pending = ""
	}
	return out.String()
}

// draftPasses — E2: taslak cümlesi o anki kanıtla tam doğrulanıyor mu.
func (g *answerSeam) draftPasses(sentence string) bool {
	return verifyTurnFrom(g.ctx).draftPasses(g.t.streamTier(), sentence)
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

// answer — `answer` yükünün son hâli: (zorla kipinde) uyuşmayan değer
// işaretlenir, (üslup açıkken) model metni süzülür, sonra varsay-ve-söyle
// cümlesi başa eklenir (o cümle hiçbir zaman süzülmez ya da işaretlenmez).
func (g *answerSeam) answer(payload any) any {
	g.mu.Lock()
	g.resetDraftLocked()
	g.mu.Unlock()
	t := g.t
	model := strings.TrimSpace(t.takeModelText())
	clause := t.assumeClause() // üslup kapalıyken ""
	t.noteAssumeEvidence(verifyTurnFrom(g.ctx), clause)
	m, ok := payload.(map[string]any)
	if !ok {
		return payload
	}
	orig, _ := m["text"].(string)
	text := orig
	var left []verifyClaim
	if g.enforce {
		text, model, left = g.enforceText(text, model)
	}
	if g.style && model != "" {
		if i := strings.Index(text, model); i >= 0 {
			if region, n := seamFilter(model, g.banned, g.carriesFinding); n > 0 {
				t.conv.noteSeamDropped(n)
				text = text[:i] + region + text[i+len(model):]
			}
		}
	}
	if len(left) > 0 { // v0.10.2439 — işaretli cevap sessiz kalmaz: not + takip çipi
		text = seamWithEnforceNote(text)
	}
	if text != orig {
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		out["text"] = text
		if len(left) > 0 {
			out = seamWithRecheckFollowup(out, left)
		}
		payload = out
	}
	return withAssumeClause(payload, clause)
}

// enforceText — E2: doğrulayıcı modelin son metninde; uyuşmazlık varsa
// (Derin + hak varsa) bir kez yeniden üretim, kalan uyuşmazlık işaretlenir.
// Dönüş: yeni cevap metni, modelin (işaretli) metni ve işaretlenen
// (yeniden üretimden sonra da kalan) uyuşmazlıklar.
func (g *answerSeam) enforceText(text, model string) (string, string, []verifyClaim) {
	v := verifyTurnFrom(g.ctx)
	r, ok := v.run()
	if !ok {
		return text, model, nil // anlatım yok (rapor, şablon, LLM'siz cevap)
	}
	mm := r.mismatches()
	if len(mm) == 0 {
		g.noteEnforce(v, verifyEnforcePass, nil)
		return text, model, nil
	}
	vm := strings.TrimSpace(v.answerText())
	var first *verifyResult
	if fn := g.takeRegen(r.Tier); fn != nil && vm != "" && strings.Contains(text, vm) {
		if nt, err := fn(seamRegenNote(mm)); err == nil && strings.TrimSpace(nt) != "" {
			nt = strings.TrimSpace(nt)
			f := r
			first = &f
			v.setAnswer(r.Tier, nt)
			text = strings.Replace(text, vm, nt, 1)
			if model == vm {
				model = nt
			}
			vm = nt
			if r2, ok2 := v.run(); ok2 {
				r = r2
			}
			mm = r.mismatches()
		}
	}
	mode := verifyEnforcePass
	if first != nil {
		mode = verifyEnforceRegenerated
	}
	if len(mm) > 0 {
		if i := strings.Index(text, vm); vm != "" && i >= 0 {
			region := seamAnnotate(vm, mm)
			text = text[:i] + region + text[i+len(vm):]
			if model == vm {
				model = region
			}
		} else {
			text = seamAnnotate(text, mm)
		}
		if first != nil {
			mode += "+" + verifyEnforceAnnotated
		} else {
			mode = verifyEnforceAnnotated
		}
	}
	g.noteEnforce(v, mode, first)
	return text, model, mm
}

func (g *answerSeam) noteEnforce(v *answerVerifyTurn, mode string, first *verifyResult) {
	v.noteEnforce(mode, first)
	g.t.conv.noteEnforce(mode)
}

// takeRegen — Derin kipte, alışverişte henüz yeniden üretim / yineleme
// düşüşü / ağ geçidi yeniden denemesi olmadıysa kademenin çağrısı; hak harcanır.
func (g *answerSeam) takeRegen(tier string) func(string) (string, error) {
	if !deepModeFrom(g.ctx).On {
		return nil
	}
	fn := g.t.regenFor(tier)
	g.mu.Lock()
	defer g.mu.Unlock()
	if fn == nil || g.regenUsed || g.regenBlocked {
		return nil
	}
	g.regenUsed = true
	return fn
}

// seamRegenNote — SAF: yeniden üretimin düzeltme notu (uyuşmayan değerler, ≤6).
func seamRegenNote(mm []verifyClaim) string {
	var toks []string
	seen := map[string]bool{}
	for _, c := range mm {
		if len(toks) < seamRegenMaxTokens && !seen[c.Text] {
			seen[c.Text] = true
			toks = append(toks, "«"+c.Text+"»")
		}
	}
	return "DÜZELTME: Önceki cevabındaki şu değerler bu turun verisinde yok: " + strings.Join(toks, ", ") +
		". Cevabı yalnız verideki değerlerle yeniden yaz; veride olmayan bir değeri yazma, bilinmiyorsa bilinmediğini söyle."
}

// ── zorla kipi: işaretleme (SAF) ─────────────────────────────────────────

var seamLinkTargetRe = regexp.MustCompile(`\]\([^)\n]*\)|https?://[^\s)]+`)

// seamProtected — işaretlenmeyecek bayt aralıkları: kod çitleri (grafik dahil),
// markdown bağlantı hedefleri ve çıplak URL'ler.
func seamProtected(s string) [][2]int {
	var out [][2]int
	in, start, off := false, 0, 0
	for _, line := range strings.SplitAfter(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !in {
				start = off
			} else {
				out = append(out, [2]int{start, off + len(line)})
			}
			in = !in
		}
		off += len(line)
	}
	if in {
		out = append(out, [2]int{start, len(s)})
	}
	for _, m := range seamLinkTargetRe.FindAllStringIndex(s, -1) {
		out = append(out, [2]int{m[0], m[1]})
	}
	return out
}

func seamIdentRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// seamTokenBounded — s[i:j] tam jeton: iki yanı tanımlayıcı karakteri değil;
// sayıdan sonra ondalık/binlik devamı ("2" ≠ "2.950") yok.
func seamTokenBounded(s string, i, j int) bool {
	if i > 0 {
		if r, _ := utf8.DecodeLastRuneInString(s[:i]); seamIdentRune(r) {
			return false
		}
	}
	if j < len(s) {
		r, w := utf8.DecodeRuneInString(s[j:])
		if seamIdentRune(r) {
			return false
		}
		if (r == '.' || r == ',') && j+w < len(s) {
			if n, _ := utf8.DecodeRuneInString(s[j+w:]); unicode.IsDigit(n) {
				return false
			}
		}
	}
	return true
}

// seamTokenTail — notun yeri: jetondan sonra kapanan ters tırnak ve Türkçe
// kesme işaretli ek ("2.950 ms'lik", "payments'ta") atlanır.
func seamTokenTail(s string, j int) int {
	if j < len(s) && s[j] == '`' {
		j++
	}
	if r, w := utf8.DecodeRuneInString(s[j:]); j < len(s) && (r == '\'' || r == '’') {
		k := j + w
		for k < len(s) {
			r2, w2 := utf8.DecodeRuneInString(s[k:])
			if !unicode.IsLetter(r2) {
				break
			}
			k += w2
		}
		if k > j+w {
			j = k
		}
	}
	return j
}

// seamAnnotate — SAF: uyuşmayan her jetonun (her geçişinin) hemen ardına
// " (doğrulanmamış değer)". Korunan aralıklar ve zaten işaretli jeton atlanır.
func seamAnnotate(s string, mm []verifyClaim) string {
	prot := seamProtected(s)
	inProt := func(i int) bool {
		for _, p := range prot {
			if i >= p[0] && i < p[1] {
				return true
			}
		}
		return false
	}
	note := " " + verifyUnverifiedNote
	var at []int
	seenTok, seenAt := map[string]bool{}, map[int]bool{}
	for _, c := range mm {
		tok := strings.TrimSpace(c.Text)
		if tok == "" || seenTok[tok] {
			continue
		}
		seenTok[tok] = true
		for from := 0; from < len(s); {
			k := strings.Index(s[from:], tok)
			if k < 0 {
				break
			}
			i := from + k
			j := i + len(tok)
			from = j
			if !seamTokenBounded(s, i, j) || inProt(i) {
				continue
			}
			end := seamTokenTail(s, j)
			if strings.HasPrefix(s[end:], note) || seenAt[end] {
				continue
			}
			seenAt[end] = true
			at = append(at, end)
		}
	}
	if len(at) == 0 {
		return s
	}
	sort.Ints(at)
	var b strings.Builder
	last := 0
	for _, p := range at {
		b.WriteString(s[last:p])
		b.WriteString(note)
		last = p
	}
	b.WriteString(s[last:])
	return b.String()
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
	answerStyleFrom(ctx).setTier(verifyTierGeneral) // v0.10.2339 — E2: taslak genel cevap kuralıyla denetlenir
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

// ── E2: kademelerin kancaları ────────────────────────────────────────────

// seamDraftEvidence — guided kanıt bloğu anlatımdan ÖNCE taslak denetimine
// (yalnız zorla kipinde; kayıt kanıtı değişmez).
func seamDraftEvidence(ctx context.Context, texts ...string) {
	if verifyEnforceOn(ctx) {
		verifyTurnFrom(ctx).addDraftEvidence(texts...)
	}
}

// seamNarrationTiers — tek çağrılı anlatım yüzeyi → doğrulayıcı kademesi.
var seamNarrationTiers = map[string]string{"chat-guided": verifyTierGuided, "chat-general": verifyTierGeneral}

// seamNoteNarrationCall — aiCall'ın akan anlatım çağrısı: Derin + zorla
// kipinde aynı sistem/kullanıcı istemiyle (sona düzeltme notu) tek buffered
// yeniden üretimin kapısı. Diğer her durumda no-op.
func seamNoteNarrationCall(ctx context.Context, surface string, call func(ctx context.Context, extraUser string) (string, error)) {
	t := answerStyleFrom(ctx)
	tier := seamNarrationTiers[surface]
	if t == nil || tier == "" || !deepModeFrom(ctx).On || !verifyEnforceOn(ctx) {
		return
	}
	t.setRegen(tier, func(note string) (string, error) { return call(ctx, "\n\n"+note) })
}

// seamNoteNarration — aiCall kancası: aynı sistem / kullanıcı istemi, sona
// düzeltme notu, akışsız (buffered) tek çağrı.
func (s *Server) seamNoteNarration(ctx context.Context, surface, system, user string) {
	seamNoteNarrationCall(ctx, surface, func(c context.Context, extra string) (string, error) {
		return s.aiCall(c, nil, system, user+extra, aiCallOpts{surface: surface})
	})
}

// seamNoteLoopCall — döngünün cevap turu (araç çağrısız): Derin + zorla
// kipinde aynı sistem istemi + konuşma + modelin cevabı + düzeltme notu ile
// araçsız, tek buffered istekli yeniden üretimin kapısı (ağ geçidi yeniden
// denemesinden geçmez).
func seamNoteLoopCall(ctx context.Context, s *Server, pctx context.Context, system string, conv []copilot.ChatMessage, specs []copilot.ToolSpec, answer string) {
	t := answerStyleFrom(ctx)
	if t == nil || s == nil || s.copilot == nil || !deepModeFrom(ctx).On || !verifyEnforceOn(ctx) {
		return
	}
	base := append(append([]copilot.ChatMessage(nil), conv...), copilot.ChatMessage{Role: "assistant", Text: answer})
	t.setRegen(verifyTierLoop, func(note string) (string, error) {
		msgs := append(append([]copilot.ChatMessage(nil), base...), copilot.ChatMessage{Role: "user", Text: note})
		turn, err := s.copilot.ChatWithToolsStream(copilot.WithBufferedOnly(copilot.WithNoToolCalls(pctx)), system, msgs, specs, nil)
		return turn.Text, err
	})
}
