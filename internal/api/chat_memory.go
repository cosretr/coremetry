package api

// chat_memory.go — v0.10.1289 (CoSRE kurallar / hafıza: "bunu hatırla").
//
// Sohbetin "memory" kademesi: yapısal kapsam kademesinin (chat_scope.go)
// HEMEN ardından, wiki / guided / niyet / serbest döngüden önce.
//
//   - Ekleme ("hatırla: …", "aklında tut", "unutma ki …", "kural ekle",
//     "kural olarak kaydet", "remember"): kural metni tetik sözcükleri
//     DETERMİNİSTİK sökülerek çıkar; model çağrılmaz. İşaret zamirli istek
//     ("bunu hatırla") → TEK model çağrısı (yüzey chat-rule, son soru + son
//     cevap ≤1500 rune) tek cümlelik öneri üretir; AI yoksa "Hatırla: …
//     biçiminde yaz".
//   - Listeleme ("hangi kuralları", "kuralları listele"): LLM'siz, bağlama
//     uyan kurallar.
//   - Dışlananlar: "hatırlıyor musun", "hatırlat", "unuttum" (soru / hatırlatıcı
//     isteği, kural değil).
//
// HİÇBİR ŞEY KAYDEDİLMEZ: cevap bir `action` bloğu taşır (kind cosre_rule;
// problemNoteAction deseni, chat_actions.go) ve istemci kartı
// (CosreRuleConfirm.tsx) metni + kapsamı düzenletir; yalnız "Kaydet" tıkı
// POST /api/ai/rules çağırır (editor+, audit). Jeton yok: metin zaten
// operatörün düzenlemesine açık ve uç rol kapılı — problem notundaki imza
// tool çıktısının bütünlüğü içindi. Viewer kartı görür, düğme gerekçesiyle
// kapalı.

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

const (
	memoryAdd  = "add"
	memoryList = "list"

	ruleProposalQMax = 400
	ruleProposalAMax = 1100 // Q + A ≤ 1500 rune

	memoryListMax = 10

	memoryHowToTR = "Kuralı tek cümleyle yaz: `Hatırla: <kural>` — ör. `Hatırla: checkout'ta 02:00–02:20 arası batch işi çalışır, 5xx beklenir.`"
)

// memoryIntent — hafıza isteğinin çözümü.
type memoryIntent struct {
	Kind      string // memoryAdd | memoryList
	Text      string // tetik sözcükleri sökülmüş kural metni (ekleme)
	Deictic   bool   // "bunu hatırla" — metin son konuşmadan önerilecek
	ScopeHint string // "global" ("genel"/"global" tetiğin yanında) ya da ""
}

// memFold — SAF: karşılaştırma katlaması (küçük harf + Türkçe harfler
// ASCII'ye): "HATIRLA", "hatırla", "hatirla" aynı sözcük.
func memFold(s string) string { return memFolder.Replace(strings.ToLower(s)) }

var memFolder = strings.NewReplacer("i\u0307", "i", "ı", "i", "ş", "s", "ğ", "g", "ü", "u", "ö", "o", "ç", "c", "â", "a", "î", "i", "û", "u")

var memWordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

type memToken struct {
	f          string // katlanmış
	start, end int    // özgün metindeki bayt aralığı
}

func memTokens(s string) []memToken {
	var out []memToken
	for _, ix := range memWordRe.FindAllStringIndex(s, -1) {
		out = append(out, memToken{f: memFold(s[ix[0]:ix[1]]), start: ix[0], end: ix[1]})
	}
	return out
}

// memSeq — SAF: toks[i:] verilen katlanmış sözcük dizisiyle mi başlıyor.
func memSeq(toks []memToken, i int, words ...string) bool {
	if i+len(words) > len(toks) {
		return false
	}
	for k, w := range words {
		if toks[i+k].f != w {
			return false
		}
	}
	return true
}

var (
	memDeicticWords = map[string]bool{"bunu": true, "sunu": true, "bunlari": true, "sunlari": true, "this": true, "that": true, "it": true}
	memFillerWords  = map[string]bool{"lutfen": true, "please": true}
	memScopeWords   = map[string]bool{"genel": true, "global": true}
)

// memExcluded — SAF: hatırlatıcı / soru biçimleri kural değildir.
func memExcluded(toks []memToken) bool {
	for i, t := range toks {
		switch {
		case strings.HasPrefix(t.f, "hatirlat"), t.f == "unuttum":
			return true
		case strings.HasPrefix(t.f, "hatirliyor") && i+1 < len(toks) && strings.HasPrefix(toks[i+1].f, "musun"):
			return true
		case t.f == "remember" && i > 0 && (toks[i-1].f == "you" || toks[i-1].f == "do"):
			return true
		}
	}
	return false
}

// memListAt — SAF: listeleme tetiği.
func memListAt(toks []memToken) bool {
	isRules := func(f string) bool { return f == "kurallari" || f == "kurallarini" || f == "kurallar" }
	for i := range toks {
		if toks[i].f == "hangi" && i+1 < len(toks) && isRules(toks[i+1].f) {
			return true
		}
		if isRules(toks[i].f) && i+1 < len(toks) && toks[i+1].f == "listele" {
			return true
		}
	}
	return false
}

// memAddAt — SAF: ekleme tetiğinin sözcük aralığı [i, j); yoksa ok=false.
func memAddAt(text string, toks []memToken) (i, j int, ok bool) {
	for k := range toks {
		switch {
		case toks[k].f == "hatirla", toks[k].f == "remember":
			return k, k + 1, true
		case memSeq(toks, k, "aklinda", "tut"):
			return k, k + 2, true
		case memSeq(toks, k, "kural", "olarak", "kaydet"):
			return k, k + 3, true
		case memSeq(toks, k, "kural", "ekle"):
			return k, k + 2, true
		case toks[k].f == "unutma":
			if memSeq(toks, k+1, "ki") {
				return k, k + 2, true
			}
			if rest := strings.TrimLeft(text[toks[k].end:], " \t"); strings.HasPrefix(rest, ":") {
				return k, k + 1, true
			}
		}
	}
	return 0, 0, false
}

// detectMemoryIntent — SAF: mesaj bir hafıza isteği mi. Tablo-testli
// (chat_memory_test.go TestDetectMemoryIntent).
func detectMemoryIntent(msg string) (memoryIntent, bool) {
	toks := memTokens(msg)
	if len(toks) == 0 || memExcluded(toks) {
		return memoryIntent{}, false
	}
	if memListAt(toks) {
		return memoryIntent{Kind: memoryList}, true
	}
	i, j, ok := memAddAt(msg, toks)
	if !ok {
		return memoryIntent{}, false
	}
	mi := memoryIntent{Kind: memoryAdd}
	// Tetiğin hemen önündeki/arkasındaki işaret zamiri, dolgu ve kapsam
	// sözcükleri ("lütfen bunu genel olarak hatırla") metinden düşer.
	deictic := false
	for i > 0 && (memDeicticWords[toks[i-1].f] || memFillerWords[toks[i-1].f] || memScopeWords[toks[i-1].f] || toks[i-1].f == "olarak" || toks[i-1].f == "kural") {
		i--
		deictic = deictic || memDeicticWords[toks[i].f]
		if memScopeWords[toks[i].f] {
			mi.ScopeHint = cosrerules.ScopeGlobal
		}
	}
	for j < len(toks) && (memDeicticWords[toks[j].f] || memFillerWords[toks[j].f] || memScopeWords[toks[j].f] || toks[j].f == "olarak") {
		deictic = deictic || memDeicticWords[toks[j].f]
		if memScopeWords[toks[j].f] {
			mi.ScopeHint = cosrerules.ScopeGlobal
		}
		j++
	}
	start, end := toks[i].start, toks[j-1].end
	text := msg[:start] + " " + msg[end:]
	text = strings.Trim(cosrerules.NormalizeText(text), " :,;.-–—!()")
	text = strings.TrimSpace(strings.Trim(text, `"'`))
	mi.Text = text
	mi.Deictic = deictic && utf8.RuneCountInString(text) < cosrerules.MinTextRunes
	if text == "" {
		mi.Deictic = true
	}
	return mi, true
}

// ruleScopeOption — onay kartının kapsam seçeneği.
type ruleScopeOption struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
	Label string `json:"label"`
}

// ruleScopeHints — kapsam çıkarımının girdileri (saf çekirdek için).
type ruleScopeHints struct {
	ScopeServices []string // doğrulanmış @-kapsam
	ScopeTeam     string
	CtxService    string // sohbetin bağlam servisi
	OwnerTeam     string // bağlam servisinin katalog takımı
	UserTeam      string
	TeamNames     []string // canlı takım kataloğu (metinde ad geçiyor mu)
}

// memMentionsName — SAF: katlanmış metin sözcük sınırında adı içeriyor mu.
func memMentionsName(text, name string) bool {
	nt := memTokens(name)
	if len(nt) == 0 || utf8.RuneCountInString(name) < 3 {
		return false
	}
	words := make([]string, len(nt))
	for i, t := range nt {
		words[i] = t.f
	}
	tt := memTokens(text)
	for i := range tt {
		if memSeq(tt, i, words...) {
			return true
		}
	}
	return false
}

// inferRuleScope — SAF: varsayılan kapsam + seçenekler. Sıra: @-servis →
// @takım → "genel/global" → metinde geçen takım adı → bağlam servisi → genel.
func inferRuleScope(mi memoryIntent, h ruleScopeHints) (cosrerules.Scope, []ruleScopeOption) {
	var def cosrerules.Scope
	textTeam := ""
	for _, t := range h.TeamNames {
		if memMentionsName(mi.Text, t) {
			textTeam = t
			break
		}
	}
	switch {
	case len(h.ScopeServices) > 0:
		def = cosrerules.Scope{Kind: cosrerules.ScopeService, Value: h.ScopeServices[0]}
	case h.ScopeTeam != "":
		def = cosrerules.Scope{Kind: cosrerules.ScopeTeam, Value: h.ScopeTeam}
	case mi.ScopeHint == cosrerules.ScopeGlobal:
		def = cosrerules.Scope{Kind: cosrerules.ScopeGlobal}
	case textTeam != "":
		def = cosrerules.Scope{Kind: cosrerules.ScopeTeam, Value: textTeam}
	case h.CtxService != "":
		def = cosrerules.Scope{Kind: cosrerules.ScopeService, Value: h.CtxService}
	default:
		def = cosrerules.Scope{Kind: cosrerules.ScopeGlobal}
	}
	var opts []ruleScopeOption
	seen := map[string]bool{}
	add := func(sc cosrerules.Scope) {
		n, err := cosrerules.NormalizeScope(sc)
		if err != nil || seen[cosrerules.ScopeKey(n)] {
			return
		}
		seen[cosrerules.ScopeKey(n)] = true
		opts = append(opts, ruleScopeOption{Kind: n.Kind, Value: n.Value, Label: cosrerules.ScopeLabelTR(n)})
	}
	add(def)
	for _, svc := range h.ScopeServices {
		add(cosrerules.Scope{Kind: cosrerules.ScopeService, Value: svc})
	}
	add(cosrerules.Scope{Kind: cosrerules.ScopeService, Value: h.CtxService})
	for _, t := range []string{h.ScopeTeam, textTeam, h.OwnerTeam, h.UserTeam} {
		add(cosrerules.Scope{Kind: cosrerules.ScopeTeam, Value: t})
	}
	add(cosrerules.Scope{Kind: cosrerules.ScopeGlobal})
	return def, opts
}

// ruleProposalInput — SAF: son soru + son cevap (≤1500 rune). Son kullanıcı
// mesajı ("bunu hatırla") HARİÇ; önceki tur yoksa ok=false.
func ruleProposalInput(msgs []copilot.ChatMessage) (string, bool) {
	ai := -1
	for i := len(msgs) - 2; i >= 0; i-- {
		if msgs[i].Role == "assistant" && strings.TrimSpace(msgs[i].Text) != "" {
			ai = i
			break
		}
	}
	if ai < 0 {
		return "", false
	}
	q := ""
	for i := ai - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Text) != "" {
			q = msgs[i].Text
			break
		}
	}
	a := clampChatRunes(collapseChatWS(msgs[ai].Text), ruleProposalAMax)
	return "SON SORU: " + clampChatRunes(collapseChatWS(q), ruleProposalQMax) + "\nSON CEVAP: " + a, true
}

// parseRuleProposal — SAF: modelin tek satırı → kural metni; "YOK" / geçersiz → "".
func parseRuleProposal(raw string) string {
	line := ""
	for _, l := range strings.Split(raw, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			line = l
			break
		}
	}
	line = strings.TrimLeft(line, "-•*> ")
	line = strings.TrimSpace(strings.Trim(line, "\"'“”‘’`"))
	if f := memFold(strings.TrimRight(line, ".!")); f == "yok" || f == "" {
		return ""
	}
	line = clipRuleText(cosrerules.NormalizeText(line))
	if !cosrerules.ValidText(line) {
		return ""
	}
	return line
}

// clipRuleText — SAF: 200 rune'u aşan metni sözcük sınırında kırpar
// (operatör kartta düzenler).
func clipRuleText(t string) string {
	if utf8.RuneCountInString(t) <= cosrerules.MaxTextRunes {
		return t
	}
	r := []rune(t)[:cosrerules.MaxTextRunes]
	cut := len(r)
	for k := len(r) - 1; k > cosrerules.MaxTextRunes/2; k-- {
		if unicode.IsSpace(r[k]) {
			cut = k
			break
		}
	}
	return strings.TrimSpace(string(r[:cut]))
}

// cosreRuleAction — SAF: onay kartının aksiyon bloğu.
func cosreRuleAction(text string, def cosrerules.Scope, opts []ruleScopeOption, canSave bool) map[string]any {
	act := map[string]any{
		"kind": "cosre_rule", "text": text, "scope": def, "scopeOptions": opts,
		"canSave": canSave, "label": "Kuralı kaydet",
	}
	if !canSave {
		act["reason"] = "Kural kaydetmek editör ya da admin rolü ister."
	}
	return act
}

func canEditRules(c *auth.Claims) bool {
	return c != nil && (c.Role == auth.RoleAdmin || c.Role == auth.RoleEditor)
}

// memoryTierReq — hafıza kademesinin girdisi.
type memoryTierReq struct {
	Msgs       []copilot.ChatMessage
	CtxService string
}

// chatMemoryTier — kademe. handled=false → akış bayt bayt eski yoldan sürer.
func (s *Server) chatMemoryTier(ctx context.Context, emit func(string, any), seq *blocks.Sequencer, rq memoryTierReq) (handled, ok bool) {
	_, rest := cleanScopedQuestion(lastUserText(rq.Msgs))
	if stmt, ok := cosrememory.DetectCorrection(rest); ok { // CoSRE Faz E — açık düzeltme (chat_correction.go)
		return s.chatCorrectionTier(ctx, emit, seq, rq, stmt)
	}
	mi, hit := detectMemoryIntent(rest)
	if !hit {
		return false, false
	}
	sc := chatScopeFromCtx(ctx)
	if mi.Kind == memoryList {
		emit("answer", map[string]any{"text": s.memoryListText(ctx, rq.CtxService)})
		return true, true
	}
	text := clipRuleText(mi.Text)
	llm := false
	if mi.Deictic || !cosrerules.ValidText(text) {
		if !mi.Deictic && text != "" {
			emit("answer", map[string]any{"text": "Bu kural çok kısa (en az " + strconv.Itoa(cosrerules.MinTextRunes) + " karakter). " + memoryHowToTR})
			return true, true
		}
		input, prev := ruleProposalInput(rq.Msgs)
		switch {
		case !prev:
			emit("answer", map[string]any{"text": "Neyi hatırlamamı istediğini anlayamadım — önceki bir cevap yok. " + memoryHowToTR})
			return true, true
		case s.copilot == nil || !s.copilot.Active():
			emit("answer", map[string]any{"text": "Son konuşmadan kural önermem için AI gerekli (şu an kapalı). " + memoryHowToTR})
			return true, true
		}
		emit("step", map[string]string{"label": "kural önerisi"})
		raw, err := s.copilotExplainSurface(ctx, aisurface.ChatRule, copilot.SystemPromptRuleProposal(), input)
		if err != nil {
			emit("error", map[string]string{"error": err.Error()})
			return true, false
		}
		text = parseRuleProposal(raw)
		if text == "" {
			emit("answer", map[string]any{"text": "Son konuşmadan kalıcı bir kural çıkaramadım. " + memoryHowToTR,
				"exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
			return true, true
		}
		llm = true
	}
	h := ruleScopeHints{CtxService: strings.TrimSpace(rq.CtxService)}
	if sc != nil {
		h.ScopeServices, h.ScopeTeam = sc.Services, sc.Team
	}
	if s.store != nil {
		h.TeamNames = s.guidedTeamNames(ctx) // 60 s önbellekli katalog (router'ın listesi)
	}
	h.OwnerTeam = s.serviceOwnerTeam(ctx, h.CtxService)
	c := auth.FromContext(ctx)
	if c != nil {
		if u := s.userByID(ctx, c.UserID); u != nil {
			h.UserTeam = strings.TrimSpace(u.Team)
		}
	}
	def, opts := inferRuleScope(memoryIntent{Text: text, ScopeHint: mi.ScopeHint}, h)
	canSave := canEditRules(c)
	act := cosreRuleAction(text, def, opts, canSave)
	emit("block", seq.Next(blocks.TypeAction, act))
	ans := "Bunu operatör kuralı olarak kaydedebilirim. Aşağıda metni ve kapsamı düzenleyebilirsin; **Kaydet**'e basmadan hiçbir şey saklanmaz."
	if llm {
		ans = "Son konuşmadan şu kuralı önerdim — aşağıda düzenleyebilirsin; **Kaydet**'e basmadan hiçbir şey saklanmaz."
	}
	if !canSave {
		ans += " Kural kaydetmek editör ya da admin rolü ister; kartı bir editöre iletebilirsin."
	}
	out := map[string]any{"text": ans}
	if llm {
		out["exchangeId"] = copilot.MetaFromContext(ctx).ExchangeID
	}
	emit("answer", out)
	return true, true
}

// memoryListText — LLM'siz liste: bağlama uyan kurallar (≤10) + toplam.
func (s *Server) memoryListText(ctx context.Context, ctxService string) string {
	rules := cosreRulesSource()
	if len(rules) == 0 {
		return "Kayıtlı operatör kuralı yok. " + memoryHowToTR
	}
	picked, omitted := cosrerules.Relevant(rules, s.cosreRuleContext(ctx, ctxService, rules), memoryListMax, 1<<20)
	var b strings.Builder
	if len(picked) == 0 {
		b.WriteString("Bu bağlama uyan kural yok.")
	} else {
		b.WriteString("Bu bağlamda geçerli kurallar (servis > takım > genel, en yeni önce):\n")
		for _, r := range picked {
			b.WriteString(cosrerules.Line(r, tzdefault.Location()) + "\n")
		}
		if omitted > 0 {
			b.WriteString("(+" + strconv.Itoa(omitted) + " kural daha)\n")
		}
	}
	b.WriteString("\nToplam " + strconv.Itoa(len(rules)) + " kural kayıtlı; tamamı /cosre › Kurallar'da (yönetim: Ayarlar › CoSRE › Kurallar).")
	return b.String()
}
