package provider

import (
	"strings"
	"unicode/utf8"
)

// tools_stream_gate.go — v0.10.1273 (CoSRE adım 4): serbest araç döngüsünde
// akan bir turun METNİNİ operatöre neyin, ne zaman gideceğine karar veren
// kapı. Saf (ağ yok), tablo testli.
//
// Kural tek cümle: buffered yolun GİZLEYECEĞİ hiçbir şey akmaz.
//
// Bir tool'lu turda model önce düz metin yazıp SONRA çağrı yapabilir
// (Claude'un "önce metriklere bakayım" önsözü; yapılandırılmış tool_calls
// metinden sonra gelir) ya da çağrıyı METNİN İÇİNE gömebilir (Gemma 4 /
// Hermes — parseOpenAIToolsChat'in ParseTextToolCalls geri düşüşü). Buffered
// yolda ikisi de ekrana çıkmaz: tool-call turunun metni konuşmaya girer,
// operatöre gitmez. Akışta karşılığı üç katman:
//
//  1. Düşünce soyucu (think_stream.go) — <think> içi hiç akmaz.
//  2. Commit beklemesi — tool sunulmuş bir turda ilk commitRunes görünür
//     rune birikmeden HİÇBİR ŞEY yayınlanmaz. Kısa önsöz + çağrı (en sık
//     durum) böylece hiç görünmeden düşer.
//  3. Satır muhafızı (yalnız metin-çağrı ayrıştırması açıkken) — çağrı
//     biçimiyle BAŞLAYAN satır (`{`, `[`, `<`, ``` çiti, `ad(`) satır/çit
//     tamamlanana dek tutulur ve ParseTextToolCalls'a sorulur: çağrıysa akış
//     o turda durur; değilse satır yayınlanır. Satır ortasındaki `<`/```
//     sonrası da satır sonuna dek tutulur (<tool_call> sınırlayıcısı).
//
// Kalan risk (uzun önsözden sonra yapılandırılmış çağrı) `Reset` ile
// kapanır: yayınlanan metin geri alınır (frontend balonu temizler). Tur
// sonunda finish UZLAŞTIRIR: nihai metin yayınlananın devamıysa yalnız
// kalan, değilse Reset + tam metin — yani son reset'ten sonraki parçaların
// birleşimi DAİMA turun buffered metnine eşittir.

// TextDelta — akan bir turun tek parçası. Reset=true: o ana dek yayınlanan
// metin geri alınır, Text (boş olabilir) yerine geçer.
type TextDelta struct {
	Text  string
	Reset bool
}

// commitRunes — tool sunulmuş turda akışın açılması için gereken görünür
// rune sayısı. Tipik önsöz ("Önce servis metriklerine bakıyorum.") bunun
// altında kalır; gerçek cevaplar üstünde.
const commitRunes = 160

// toolTurnGate — bir turun metin kapısı. Sıfır değeri KULLANILMAZ:
// newToolTurnGate ile kurulur.
type toolTurnGate struct {
	sink func(TextDelta)
	// guard — bu turda tool çağrısı mümkün (commit beklemesi + Reset).
	guard bool
	// textCalls — ParseTextToolCalls'a verilecek sunulan adlar; nil = metin
	// gömülü çağrı ayrıştırması YOK (satır muhafızı kapalı).
	textCalls []string

	think      thinkStripper
	emitted    strings.Builder // son Reset'ten beri yayınlanan
	approved   strings.Builder // onaylı, commit bekleyen
	committed  bool
	suppressed bool   // bu turda artık yayın yok
	toolSeen   bool   // yapılandırılmış tool çağrısı geldi
	trimLead   bool   // düşünce reset'inden sonra baştaki boşluk atılır (StripThinking trim'i)
	wsHold     string // yayınlanmamış sondaki boşluk

	line        strings.Builder // analiz edilen satır
	lineEmitted int             // satırın onaylanmış bayt sayısı
	fence       bool            // ``` çit bloğu tutuluyor
	block       strings.Builder // tutulan çit bloğu

	// v0.10.1324 — yineleme muhafızı (degen_guard.go): yayınlanan metni
	// izler; tetiklenince yayın durur, temiz önek kalır ve onDegenerate
	// (üst akış iptali) bir kez çağrılır.
	degen        degenGuard
	degenerated  bool
	cleanText    string
	onDegenerate func()
}

func newToolTurnGate(sink func(TextDelta), guard bool, textCalls []string) *toolTurnGate {
	if sink == nil {
		sink = func(TextDelta) {}
	}
	if !guard {
		textCalls = nil
	}
	return &toolTurnGate{sink: sink, guard: guard, textCalls: textCalls, committed: !guard}
}

// Content — sağlayıcının content kanalından gelen ham parça.
func (g *toolTurnGate) Content(s string) {
	if g.degenerated {
		return
	}
	out, reset := g.think.Feed(s)
	if reset {
		g.thinkReset()
	}
	g.feedVisible(out)
}

// ToolCall — yapılandırılmış bir tool çağrısı başladı: tur bir tool-call
// turudur, metni operatöre gitmez.
func (g *toolTurnGate) ToolCall() {
	if g.degenerated {
		return // v0.10.1324 — kesilmiş turun kalan parçaları yok sayılır
	}
	g.toolSeen = true
	g.suppress(true)
}

// Finish — turun buffered-eşdeğeri çözümlendikten sonra UZLAŞTIRMA.
// toolTurn: çözümleme tool çağrısı üretti (yapılandırılmış ya da metinden).
func (g *toolTurnGate) Finish(final string, toolTurn bool) {
	e := g.emitted.String()
	if toolTurn {
		if e != "" {
			g.sink(TextDelta{Reset: true})
			g.emitted.Reset()
		}
		g.wsHold = ""
		return
	}
	switch {
	case strings.HasPrefix(final, e):
		if rem := final[len(e):]; rem != "" {
			g.sink(TextDelta{Text: rem})
		}
	case strings.HasPrefix(e, final) && strings.TrimSpace(e[len(final):]) == "":
		// yalnız sondaki boşluk fazla (StripThinking trim'i) — dokunma
	default:
		g.sink(TextDelta{Text: final, Reset: true})
	}
	g.emitted.Reset()
	g.emitted.WriteString(final)
	g.wsHold = ""
}

// Degenerated — v0.10.1324: yineleme muhafızı turu kesti mi.
func (g *toolTurnGate) Degenerated() bool { return g.degenerated }

// CleanText — kesilen turun operatöre kalan temiz öneki ("" = kesilmedi).
func (g *toolTurnGate) CleanText() string { return g.cleanText }

// degenerate — muhafız tetiklendi: yayın temiz öneke uzlaştırılır, tur
// susturulur, üst akış okuması iptal edilir.
func (g *toolTurnGate) degenerate() {
	clean := g.degen.CleanText()
	e := g.emitted.String()
	switch {
	case strings.HasPrefix(clean, e):
		if rem := clean[len(e):]; rem != "" {
			g.sink(TextDelta{Text: rem})
		}
	default:
		g.sink(TextDelta{Text: clean, Reset: true})
	}
	g.emitted.Reset()
	g.emitted.WriteString(clean)
	g.wsHold = ""
	g.cleanText = clean
	g.degenerated = true
	g.suppressed = true
	g.approved.Reset()
	if g.onDegenerate != nil {
		g.onDegenerate()
	}
}

// emit — sondaki boşluk bir SONRAKİ parçaya taşınır: buffered metin
// (StripThinking) sonu kırpar; akış da kırpık biterse birleşim birebir tutar.
func (g *toolTurnGate) emit(s string) {
	s = g.wsHold + s
	t := strings.TrimRight(s, " \t\r\n")
	g.wsHold = s[len(t):]
	if t == "" {
		return
	}
	if g.degen.Feed(t) {
		g.degenerate()
		return
	}
	g.emitted.WriteString(t)
	g.sink(TextDelta{Text: t})
}

func (g *toolTurnGate) suppress(reset bool) {
	g.suppressed = true
	g.approved.Reset()
	if reset && g.emitted.Len() > 0 {
		g.sink(TextDelta{Reset: true})
		g.emitted.Reset()
		g.degen.Reset()
		g.wsHold = ""
	}
}

// thinkReset — bir </think> tüketildi: buffered yol öncesini atardı.
func (g *toolTurnGate) thinkReset() {
	if g.emitted.Len() > 0 {
		g.sink(TextDelta{Reset: true})
		g.emitted.Reset()
	}
	g.degen.Reset()
	g.wsHold = ""
	g.approved.Reset()
	g.line.Reset()
	g.lineEmitted = 0
	g.fence = false
	g.block.Reset()
	g.committed = !g.guard
	g.suppressed = g.toolSeen
	g.trimLead = true
}

func (g *toolTurnGate) approve(s string) {
	if s == "" || g.suppressed {
		return
	}
	if g.committed {
		g.emit(s)
		return
	}
	g.approved.WriteString(s)
	if utf8.RuneCountInString(g.approved.String()) >= commitRunes {
		g.committed = true
		a := g.approved.String()
		g.approved.Reset()
		g.emit(a)
	}
}

func (g *toolTurnGate) feedVisible(s string) {
	if g.trimLead {
		s = strings.TrimLeft(s, " \t\r\n")
		if s == "" {
			return
		}
		g.trimLead = false
	}
	if g.textCalls == nil {
		g.approve(s)
		return
	}
	for s != "" && !g.suppressed {
		i := strings.IndexByte(s, '\n')
		piece, nl := s, false
		if i >= 0 {
			piece, nl = s[:i+1], true
		}
		s = s[len(piece):]
		g.line.WriteString(piece)
		if nl {
			g.endLine()
		} else if !g.fence {
			g.partialLine()
		}
	}
}

// Satır başı sınıfları.
const (
	lineUndecided = iota
	lineProse
	lineSuspicious
	lineFence
)

func (g *toolTurnGate) partialLine() {
	ln := g.line.String()
	if classifyLineStart(ln, false) != lineProse {
		return // karar yok ya da şüpheli — satır sonunu bekle
	}
	rest := ln[g.lineEmitted:]
	if j := strings.IndexAny(rest, "<`"); j >= 0 {
		rest = rest[:j] // sınırlayıcı / çit olabilir — satır sonuna dek tut
	}
	g.lineEmitted += len(rest)
	g.approve(rest)
}

func (g *toolTurnGate) endLine() {
	ln := g.line.String()
	rest := ln[g.lineEmitted:]
	g.line.Reset()
	g.lineEmitted = 0
	if g.fence {
		g.block.WriteString(ln)
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			blk := g.block.String()
			g.block.Reset()
			g.fence = false
			g.checkSegment(blk)
		}
		return
	}
	switch classifyLineStart(ln, true) {
	case lineFence:
		if strings.Count(ln, "```") >= 2 { // tek satırlık çit
			g.checkSegment(ln)
			return
		}
		g.fence = true
		g.block.WriteString(ln)
	case lineSuspicious:
		if hasCallDelimiter(ln) {
			g.suppress(false) // sınırlayıcı satırlar boyu sürebilir — tur sonunu bekle
			return
		}
		g.checkSegment(ln)
	default:
		if hasCallDelimiter(rest) || strings.Contains(rest, "```") {
			g.suppress(false)
			return
		}
		g.approve(rest)
	}
}

// checkSegment — tamamlanmış şüpheli satır/çit: buffered ayrıştırıcı onu
// çağrı sayıyorsa akış durur, saymıyorsa yayınlanır.
func (g *toolTurnGate) checkSegment(seg string) {
	if _, _, ok := ParseTextToolCalls(seg, g.textCalls); ok {
		g.suppress(false)
		return
	}
	g.approve(seg)
}

func hasCallDelimiter(s string) bool {
	for _, m := range []string{"<tool_call", "<|tool_call", "<start_function_call", "<|channel"} {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// classifyLineStart — satırın çağrı biçimiyle başlayıp başlamadığı.
// complete=false iken karar verilemeyen önek lineUndecided döner.
func classifyLineStart(ln string, complete bool) int {
	t := strings.TrimLeft(ln, " \t\r")
	t = strings.TrimRight(t, "\r\n")
	if t == "" {
		if complete {
			return lineProse
		}
		return lineUndecided
	}
	switch t[0] {
	case '{', '[', '<':
		return lineSuspicious
	case '`':
		if strings.HasPrefix(t, "```") {
			return lineFence
		}
		if !complete && strings.HasPrefix("```", t) {
			return lineUndecided
		}
		return lineProse
	}
	if !isIdentStart(t[0]) {
		return lineProse
	}
	i := 0
	for i < len(t) && isIdentChar(t[i]) {
		i++
	}
	ident := t[:i]
	for i < len(t) && (t[i] == ' ' || t[i] == '\t') {
		i++
	}
	if i >= len(t) {
		if complete {
			return lineProse
		}
		return lineUndecided
	}
	if t[i] == '(' || (t[i] == ':' && ident == "call") {
		return lineSuspicious
	}
	return lineProse
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.' || c == '-'
}
