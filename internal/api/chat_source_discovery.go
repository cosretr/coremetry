package api

// chat_source_discovery.go — v0.10.1553 KAYNAK KEŞFİ (operatör: "Gerekirse tüm
// kaynakları search edip … otomatik mi discover etsin?" → otomatik keşif +
// alternatif çipleri; spec onayı "Önerin").
//
// Kök sorun: sohbet kademeleri "ilk eşleşen kazanır" sırasıyla koşuyordu; bir
// kaynak cevaplayınca diğerine hiç bakılmıyordu (CRMHouse vakası: wiki anlatımı
// Excel'in 16 satırından 1'ini okudu). Burada cevaplamadan ÖNCE dört kaynak
// PARALEL ve LLM'siz yoklanır, her biri kendi bütçesiyle:
//
//	excel       yüklenen tablolarda birebir hücre eşleşmesi (docRowsFind, sıkı)
//	wiki        yerel lexical içerik yoklaması (wikiProbe — ADO'ya istek yok)
//	telemetri   katalogda servis adı (ya da yalnız telemetri sinyali)
//	exceptions  exception tipi / hata kodu (BSAException, ORA-00001) Exceptions'ta
//
// Karar SAF (pickChatSource): en güçlü kanıt cevaplar; kanıtı olan diğerleri
// cevabın altında "/<kaynak> · <kanıt>" çipi olur (çip = eğik komut, chat_scope;
// argümansız komut önceki soruyu kullanır). İki kaynak güçlü ve başa baş →
// "Hangi kaynaktan?" sorusu + çipler. Tek kaynak → bugünkü zincir AYNEN (yalnız
// exceptions'ın kendi cevabı yeni). Kanıt yok → zincir aynen (sonunda genel
// bilgi). Kapı: ai.generalQuestions.sourceDiscovery (nil = açık); panel /
// çekmece / trace bağlamında koşmaz (wikiTierAllowed).

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/promptfmt"
)

const (
	srcExcel      = "excel"
	srcWiki       = "wiki"
	srcTelemetry  = "telemetri"
	srcExceptions = "exceptions"
	srcGeneral    = "genel"

	discoverProbeTimeout = 1500 * time.Millisecond
	// discoverStrongMin / discoverTieMargin — iki kaynak ≥ strongMin ve aralarındaki
	// fark < margin ise başa baş (soru sorulur).
	discoverStrongMin  = 0.7
	discoverTieMargin  = 0.1
	discoverExLookback = 7 * 24 * time.Hour
	discoverMaxChips   = 4
)

// srcCand — bir kaynağın kanıtı (çip etiketi + cevap için önbellek).
type srcCand struct {
	Kind  string
	Score float64
	Label string // "16 satır", "OpenTelemetry sayfası", "bsa-deposit servisi"

	rows   []docRowMatch
	vals   []string
	exTerm string
	exEv   exceptionGroupsEvidence
}

// sourceChoice — pickChatSource sonucu.
type sourceChoice struct {
	Winner *srcCand
	Alts   []srcCand
	Tie    []srcCand
}

// srcKindOrder — eşit skorda kararlı sıra (daha özgül kanıt önce).
var srcKindOrder = map[string]int{srcExcel: 0, srcExceptions: 1, srcTelemetry: 2, srcWiki: 3}

// pickChatSource — SAF: adaylar → kazanan / alternatifler / başa baş.
func pickChatSource(cands []srcCand) sourceChoice {
	if len(cands) == 0 {
		return sourceChoice{}
	}
	c := append([]srcCand(nil), cands...)
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Score != c[j].Score {
			return c[i].Score > c[j].Score
		}
		return srcKindOrder[c[i].Kind] < srcKindOrder[c[j].Kind]
	})
	if len(c) >= 2 && c[1].Score >= discoverStrongMin && c[0].Score-c[1].Score < discoverTieMargin {
		var tie []srcCand
		for _, x := range c {
			if x.Score >= discoverStrongMin && c[0].Score-x.Score < discoverTieMargin {
				tie = append(tie, x)
			}
		}
		return sourceChoice{Tie: tie}
	}
	w := c[0]
	return sourceChoice{Winner: &w, Alts: c[1:]}
}

// sourceChip — SAF: çip metni = eğik komut + kanıt etiketi (komut önceki soruyu kullanır).
func sourceChip(c srcCand) string {
	if c.Label == "" {
		return "/" + c.Kind
	}
	return "/" + c.Kind + " · " + c.Label
}

func sourceChips(cs []srcCand) []string {
	var out []string
	for _, c := range cs {
		if len(out) >= discoverMaxChips {
			break
		}
		out = append(out, sourceChip(c))
	}
	return out
}

// sourceKindTR — SAF: kaynak adı (adım çipi / soru metni).
func sourceKindTR(kind string) string {
	switch kind {
	case srcExcel:
		return "Yüklenen tablolar"
	case srcWiki:
		return "Wiki"
	case srcTelemetry:
		return "Telemetri"
	case srcExceptions:
		return "Exceptions"
	}
	return kind
}

// discoverStepLabel — SAF: "kaynak keşfi: Yüklenen tablolar 16 satır · Wiki …".
func discoverStepLabel(cs []srcCand) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, strings.TrimSpace(sourceKindTR(c.Kind)+" "+c.Label))
	}
	return "kaynak keşfi: " + strings.Join(parts, " · ")
}

// exceptionTermRe — exception tipi (…Exception / …Error, paket nitelikli de) ya da
// hata kodu (ORA-00001, BKMQR-39).
var exceptionTermRe = regexp.MustCompile(`(?:[A-Za-z_][A-Za-z0-9_]*\.)*([A-Z][A-Za-z0-9]*(?:Exception|Error))\b|\b([A-Z]{2,}[A-Z0-9]*-\d{1,6})\b`)

// exceptionTermOf — SAF: sorudaki ilk exception tipi / hata kodu ("" = yok).
func exceptionTermOf(q string) string {
	m := exceptionTermRe.FindStringSubmatch(q)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// discoverChatSources — dört yoklama paralel, LLM'siz; kanıtı olanlar döner.
func (s *Server) discoverChatSources(ctx context.Context, question string) []srcCand {
	var (
		mu  sync.Mutex
		out []srcCand
		wg  sync.WaitGroup
	)
	add := func(c srcCand) {
		mu.Lock()
		out = append(out, c)
		mu.Unlock()
	}
	run := func(f func(pctx context.Context)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
			defer cancel()
			f(pctx)
		}()
	}
	run(func(pctx context.Context) {
		rows, vals := s.docRowsFind(pctx, question, true)
		if len(rows) == 0 {
			return
		}
		sc := 0.9
		if len(vals) >= 2 {
			sc = 0.95
		}
		add(srcCand{Kind: srcExcel, Score: sc, Label: fmt.Sprintf("%d satır", len(rows)), rows: rows, vals: vals})
	})
	run(func(pctx context.Context) {
		w := wikiKB()
		if w == nil || wikiProbeFromCtx(ctx) == nil {
			return
		}
		hits := s.wikiProbe(pctx, w, question)
		if len(hits) == 0 {
			return
		}
		cov := hits[0].TermCoverage
		if cov > 1 {
			cov = 1
		}
		add(srcCand{Kind: srcWiki, Score: 0.6 + 0.3*cov, Label: clipRunes(hits[0].Title, 40)})
	})
	run(func(pctx context.Context) {
		norm := normalizeGuidedMsg(question)
		if svc := extractServiceEntity(norm, s.guidedServiceNames(pctx), s.guidedEnvNames(pctx)); svc != "" {
			add(srcCand{Kind: srcTelemetry, Score: 0.9, Label: svc + " servisi"})
			return
		}
		if hasGuidedSignal(norm) {
			add(srcCand{Kind: srcTelemetry, Score: 0.5, Label: "canlı veri"})
		}
	})
	if term := exceptionTermOf(question); term != "" {
		run(func(pctx context.Context) {
			now := time.Now()
			ev := s.exceptionGroupsEvidence(pctx, exScope{Search: term, From: now.Add(-discoverExLookback), To: now, Max: exGroupTeamRenderMax})
			if ev.Err != "" || len(ev.Groups) == 0 {
				return
			}
			add(srcCand{Kind: srcExceptions, Score: 0.85, Label: fmt.Sprintf("%s · %d grup", term, len(ev.Groups)), exTerm: term, exEv: ev})
		})
	}
	wg.Wait()
	sort.SliceStable(out, func(i, j int) bool { return srcKindOrder[out[i].Kind] < srcKindOrder[out[j].Kind] })
	return out
}

// sourceDiscoveryEnabled — kapı: ayar + çağıran bağlamı + depo.
func (s *Server) sourceDiscoveryEnabled(tc wikiTierContext) bool {
	return s != nil && s.store != nil && chstore.CurrentAIGeneralQuestions().SourceDiscoveryOn() && wikiTierAllowed(tc)
}

// chatSourceDiscoveryTier — keşif kademesi. handled=true → cevap basıldı.
// handled=false → zincir sürer; emitOut alternatif çiplerini cevaba ekler,
// skipDocs=true → kazanan telemetri: tablo / wiki kademeleri atlanır.
func (s *Server) chatSourceDiscoveryTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext) (handled, ok bool, emitOut func(string, any), skipDocs bool) {
	emitOut = emit
	if !s.sourceDiscoveryEnabled(tc) {
		return
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if q == "" {
		return
	}
	cands := s.discoverChatSources(ctx, q)
	if len(cands) == 0 {
		return
	}
	ctx = withRecordsQuestion(ctx, q)
	choice := pickChatSource(cands)
	if len(cands) >= 2 {
		emitGuidedContextStep(emit, discoverStepLabel(cands))
	}
	if len(choice.Tie) > 0 {
		names := make([]string, 0, len(choice.Tie))
		for _, c := range choice.Tie {
			names = append(names, "**"+sourceKindTR(c.Kind)+"** ("+c.Label+")")
		}
		emit("answer", map[string]any{
			"text":        "Bu soru için birden çok kaynakta güçlü kanıt buldum: " + strings.Join(names, ", ") + ". Hangisinden cevaplayayım?",
			"suggestions": sourceChips(choice.Tie),
		})
		return true, true, emit, false
	}
	w := choice.Winner
	alts := sourceChips(choice.Alts)
	switch w.Kind {
	case srcExcel:
		if len(alts) == 0 {
			return // tek kaynak: bugünkü sıkı satır kademesi aynı satırları basar
		}
		s.docRowsEmit(ctx, emit, w.rows, w.vals, alts)
		return true, true, emit, false
	case srcExceptions:
		s.exceptionsSourceAnswer(ctx, emit, w.exTerm, w.exEv, alts)
		return true, true, emit, false
	case srcWiki:
		if len(alts) == 0 {
			return
		}
		ew := emitWithAltSources(emit, alts)
		h, wok := s.wikiProbeAnswer(ctx, ew, wikiKB(), msgs, q)
		if h {
			return true, wok, emit, false
		}
		return false, false, ew, false
	case srcTelemetry:
		if len(alts) == 0 {
			return
		}
		return false, false, emitWithAltSources(emit, alts), true
	}
	return
}

// emitWithAltSources — "answer" olayına alternatif kaynak çiplerini ekler
// (var olan önerilerin ardına, tekrarsız).
func emitWithAltSources(emit func(string, any), alts []string) func(string, any) {
	if len(alts) == 0 {
		return emit
	}
	return func(ev string, data any) {
		if ev == "answer" {
			if m, ok := data.(map[string]any); ok {
				cur, _ := m["suggestions"].([]string)
				out := append([]string(nil), cur...)
				for _, a := range alts {
					if !containsStr(out, a) {
						out = append(out, a)
					}
				}
				m["suggestions"] = out
			}
		}
		emit(ev, data)
	}
}

// exceptionsSourceAnswer — Exceptions'tan deterministik cevap (LLM yok): grup
// tablosu + grup bağlantıları.
func (s *Server) exceptionsSourceAnswer(ctx context.Context, emit func(string, any), term string, ev exceptionGroupsEvidence, alts []string) {
	emitGuidedContextStep(emit, "Exceptions · "+term)
	table := exceptionsSourceMarkdown(term, ev)
	text := table
	if ev.Err == "" && len(ev.Groups) > 0 {
		text = withRecordsNarrative(s.narrateRecords(ctx, emit, lastQuestionOr(ctx, term), "Exceptions grupları", table), table)
	}
	ans := map[string]any{"text": text, "exchangeId": copilot.MetaFromContext(ctx).ExchangeID,
		"sources": []any{}, "links": exceptionsSourceLinks(ev)}
	withAllowedLinks(ans, "")
	if len(alts) > 0 {
		ans["suggestions"] = alts
	}
	emit("answer", ans)
}

// exceptionsSourceMarkdown — SAF: grup tablosu (en yeni önce).
func exceptionsSourceMarkdown(term string, ev exceptionGroupsEvidence) string {
	if ev.Err != "" {
		return fmt.Sprintf("Exceptions grupları okunamadı (%s); **%s** için grup olup olmadığı bilinmiyor.", clipRunes(oneLine(ev.Err), 120), term)
	}
	if len(ev.Groups) == 0 {
		return fmt.Sprintf("Exceptions'ta son %s içinde **%s** ile eşleşen teknik hata grubu yok.", fmtAgoTR(ev.LookbackS), term)
	}
	esc := func(s string) string { return strings.ReplaceAll(oneLine(s), "|", "\\|") }
	var b strings.Builder
	fmt.Fprintf(&b, "Exceptions'ta son %s içinde **%s** için %d grup (en yeni önce):\n\n", fmtAgoTR(ev.LookbackS), term, len(ev.Groups))
	b.WriteString("| Exception | Servis | Durum | Öncelik | Toplam | Son görülme (UTC) |\n|---|---|---|---|---:|---|\n")
	for _, l := range ev.Groups {
		head := l.Type
		if l.Oracle {
			head = l.Code + " · " + l.Operation
		}
		last := ""
		if l.LastSeen > 0 {
			last = time.Unix(0, l.LastSeen).UTC().Format("01-02 15:04")
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", esc(clipRunes(head, exGroupOpRunes)), esc(l.Service), l.State, l.Priority,
			fmtThousands(l.Occurrences), last)
	}
	if ev.More > 0 {
		fmt.Fprintf(&b, "\n+%d grup daha — Exceptions sayfasında.", ev.More)
	}
	return b.String()
}

func exceptionsSourceLinks(ev exceptionGroupsEvidence) []guidedAnswerLink {
	links := make([]guidedAnswerLink, 0, len(ev.Groups))
	for i, l := range ev.Groups {
		if i >= 5 {
			break
		}
		label := l.Type
		if l.Oracle {
			label = l.Code
		}
		links = append(links, guidedAnswerLink{Label: clipRunes(label, 40) + prefixed(" · ", l.Service), Href: l.Href})
	}
	return links
}

// ── kaynak komutları (/excel /telemetri /exceptions /genel; /wiki chat_scope'ta) ──

// sourceCommandKind — SAF: komut bir kaynak komutu mu.
func sourceCommandKind(cmd string) string {
	switch cmd {
	case srcExcel, srcTelemetry, srcExceptions, srcGeneral:
		return cmd
	}
	return ""
}

// sourceCommandQuestion — SAF: komutun sorusu. Argümansız ya da yalnız çip
// etiketi ("· 16 satır") → önceki kullanıcı sorusu (çip tıkı aynı soruyu o
// kaynakla yeniden sorar).
func sourceCommandQuestion(rest string, msgs []copilot.ChatMessage) string {
	r := strings.TrimSpace(rest)
	if r != "" && !strings.HasPrefix(r, "·") {
		return r
	}
	return previousUserQuestion(msgs)
}

// previousUserQuestion — SAF: son kullanıcı mesajından ÖNCEKİ, komut olmayan
// kullanıcı sorusu (komut önekliyse önekten arındırılmış hâli).
func previousUserQuestion(msgs []copilot.ChatMessage) string {
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Text) != "" {
			last = i
			break
		}
	}
	for i := last - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		_, rest := cleanScopedQuestion(msgs[i].Text)
		if rest = strings.TrimSpace(rest); rest != "" && !strings.HasPrefix(rest, "·") {
			return rest
		}
	}
	return ""
}

type forcedSourceKey struct{}

// forcedChatSource — /telemetri komutu: zincir tablo / wiki / keşif kademelerini atlar.
func forcedChatSource(ctx context.Context) string {
	v, _ := ctx.Value(forcedSourceKey{}).(string)
	return v
}

// sourceForcedAnswer — kaynak komutu. handled=false yalnız /telemetri'de (zincir
// guided'dan sürer, ctx işaretli).
func (s *Server) sourceForcedAnswer(ctx context.Context, emit func(string, any), kind, question string, msgs []copilot.ChatMessage) (context.Context, bool, bool) {
	plain := func(text string) (context.Context, bool, bool) {
		emit("answer", map[string]any{"text": text, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		return ctx, true, true
	}
	if question == "" {
		return plain(fmt.Sprintf("`/%s` ile hangi soruyu cevaplayayım? Örnek: `/%s CRMHouse kayıtları`.", kind, kind))
	}
	ctx = withRecordsQuestion(ctx, question)
	switch kind {
	case srcTelemetry:
		return withForcedChatSource(ctx, srcTelemetry), false, false
	case srcExcel:
		rows, vals := s.docRowsFind(ctx, question, false)
		if len(rows) == 0 {
			return plain("Yüklenen tablolarda bu soruyla birebir eşleşen satır bulunamadı. Hücre değerini (ör. sahip, sunucu adı, IP) aynen yazmayı dene.")
		}
		s.docRowsEmit(ctx, emit, rows, vals, nil)
		return ctx, true, true
	case srcExceptions:
		term := exceptionTermOf(question)
		if term == "" {
			term = strings.TrimSpace(question)
		}
		now := time.Now()
		ev := s.exceptionGroupsEvidence(ctx, exScope{Search: term, From: now.Add(-discoverExLookback), To: now, Max: exGroupTeamRenderMax})
		s.exceptionsSourceAnswer(ctx, emit, term, ev, nil)
		return ctx, true, true
	case srcGeneral:
		ans := map[string]any{"text": "", "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}}
		if !s.generalSourceAnswer(ctx, question, ans, emit) {
			return plain("Genel bilgi cevabı şu an kullanılamıyor (CoSRE genel soru ayarı kapalı ya da model yanıt vermedi).")
		}
		emit("answer", ans)
		return ctx, true, true
	}
	return ctx, false, false
}

// generalSourceNoteTR — /genel cevabının sunucu etiketi.
const generalSourceNoteTR = "_Genel bilgiyle cevaplıyorum (`/genel`) — kurumunuza özgü bilgi ya da canlı telemetri içermez._"

// generalSourceAnswer — /genel: tool'suz tek genel-bilgi çağrısı (genel sohbet
// istemi — v0.10.1552'den beri eksiksiz cevap; kapı off_topic ile aynı).
func (s *Server) generalSourceAnswer(ctx context.Context, question string, ans map[string]any, emit func(string, any)) bool {
	if !s.offTopicGeneralEnabled(question) {
		return false
	}
	emit("delta", map[string]string{"text": generalSourceNoteTR + "\n\n"})
	raw, _ := s.copilotStreamSurface(ctx, "chat-general", copilot.SystemPromptGeneralChat(), question,
		func(d string) { emit("delta", map[string]string{"text": d}) })
	if strings.TrimSpace(raw) == "" { // hata + metinsiz = cevap yok; kopan akış metni taşır
		return false
	}
	ans["text"] = generalSourceNoteTR + "\n\n" + strings.TrimSpace(raw)
	ans["exchangeId"] = copilot.MetaFromContext(ctx).ExchangeID
	return true
}

// ── LLM anlatımı (v0.10.1553, operatör: "LLM'i iyi kullanalım muhakkak,
// kullanıcı cevaplardan tatmin olsun") ─────────────────────────────────────

// recordsMaxRunes — modele giden kayıt bloğunun tavanı (200 satır × 12 kolon
// çoğu tabloda sığar; aşarsa kesilir ve modele söylenir).
const recordsMaxRunes = 24000

type recordsQuestionKey struct{}

// withRecordsQuestion / lastQuestionOr — anlatımın sorusu (kademe ctx'e koyar).
func withRecordsQuestion(ctx context.Context, q string) context.Context {
	return context.WithValue(ctx, recordsQuestionKey{}, q)
}

func lastQuestionOr(ctx context.Context, fallback string) string {
	if q, _ := ctx.Value(recordsQuestionKey{}).(string); strings.TrimSpace(q) != "" {
		return q
	}
	return fallback
}

// recordsUserPrompt — SAF: soru + kaynak + (kesilmişse notlu) kayıt bloğu.
func recordsUserPrompt(question, kindTR, data string) string {
	r := []rune(data)
	note := ""
	if len(r) > recordsMaxRunes {
		data = string(r[:recordsMaxRunes])
		note = " (liste uzun olduğu için KESİLDİ — cevapta kesildiğini söyle)"
	}
	return "SORU: " + strings.TrimSpace(question) + "\n\nKAYNAK: " + kindTR + note +
		"\n\nKAYITLAR:\n```text\n" + promptfmt.FenceSafe(data) + "\n```"
}

// withRecordsNarrative — SAF: anlatım varsa üstte, tam tablo altta.
func withRecordsNarrative(narrative, table string) string {
	if strings.TrimSpace(narrative) == "" {
		return table
	}
	return strings.TrimSpace(narrative) + "\n\n---\n\n" + table
}

// narrateRecords — kayıt kümesinin akışlı LLM anlatımı; model yok / boş → "".
func (s *Server) narrateRecords(ctx context.Context, emit func(string, any), question, kindTR, data string) string {
	if s == nil || s.copilot == nil || !s.copilot.Active() || strings.TrimSpace(question) == "" {
		return ""
	}
	raw, _ := s.copilotStreamSurface(ctx, aisurface.ChatRecords, copilot.SystemPromptRecordsAnswer(),
		recordsUserPrompt(question, kindTR, data), func(d string) { emit("delta", map[string]string{"text": d}) })
	return strings.TrimSpace(raw)
}
