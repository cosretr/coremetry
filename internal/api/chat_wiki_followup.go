package api

// chat_wiki_followup.go — v0.10.1134: wiki cevabının TAKİP sorusu.
//
// Operatör (prod, CoSRE): 1. tur "production clusterlarında yeni namespace
// nasıl oluşturabilirim" → wiki sayfasından doğru anlatım ("ilgili namespace
// pipeline'ı tetiklenmelidir"). 2. tur "pipeline linki nedir" → bağlam
// kayboldu: wiki'de yalnız "pipeline linki" arandı, alakasız sayfaların
// pipeline linkleri sıralandı.
//
// Kök neden: her wiki yolu yalnız SON kullanıcı metnini görüyordu —
// wikiChatAnswer `lastUserText(msgs)` + işaret kapısı (takip sorusu işaret
// taşımaz → kademe atlanır), RAG kademesi `ragWikiHits(ctx, question)` ile
// çıplak takip sorusunu arar ve anlatım prompt'u ("SORU: …") önceki turu
// hiç taşımaz. İstemci geçmişi yalnız {role,text}: önceki cevabın kaynak
// çipleri sunucuya hiç ulaşmıyordu.
//
// Düzeltme:
//  1. Takip tespiti: önceki asistan turu bir wiki cevabıysa (kaynak sayfası
//     çözülebiliyorsa) VE yeni soru kısa/eliptik (≤8 sözcük) ya da geri atıf
//     taşıyorsa ("bu", "linki", "peki", "nerede" …). Telemetri sinyali taşıyan
//     soru (servis adı + sağlık/hata …) takip SAYILMAZ — guided'ın sorusu.
//  2. Önceki sayfa(lar)ın çözümü — yapısal veri önce: istemci önceki turun
//     "Wiki · " çip/kaynak href'lerini context.wikiRefs ile yollar; yoksa
//     sunucunun kendi hafızası (cevap metninin özeti → sayfa kimlikleri,
//     Redis + süreç-içi); o da yoksa önceki cevap metnindeki wiki url'leri.
//     URL → sayfa: hafıza, pagePath'li url ayrıştırma, başlıkla arama.
//  3. Sıra: (a) önceki sayfa(lar)ı YENİDEN OKU (ReadPage: karma/senkron ve
//     canlı mod), modele sayfa + önceki S/C + takip sorusu; model sayfadan
//     cevaplar ya da WikiNotInPageSentinel döner (tahmin yok). (b) Değilse
//     bağlamlı arama: takip terimleri + önceki soru + sayfa başlığı, önce aynı
//     proje. (c) O da cevaplamazsa bugünkü akış AYNEN.
//
// Kapı kademeyle aynı (wikiChatAnswer içinden çağrılır): API token'ı yok,
// panel/çekmece bağlamı yok, wiki kapalıysa hiç koşmaz. Önceki tur wiki
// cevabı değilse hiçbir şey okunmaz/aranmaz — akış bayt bayt eski.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// wikiFollowUpMaxWords — bu kadar ya da daha kısa soru eliptik sayılır.
	wikiFollowUpMaxWords = 8
	// wikiFollowUpPages — yeniden okunan en çok önceki sayfa.
	wikiFollowUpPages = 2
	// wikiFollowUpFirstShare — v0.10.1136: iki sayfa yeniden okunursa ilkinin bütçe payı.
	wikiFollowUpFirstShare = 0.65
	// wikiPriorUserRunes / wikiPriorAssistantRunes — önceki turun prompt tavanları.
	wikiPriorUserRunes      = 400
	wikiPriorAssistantRunes = 1200
	// wikiPriorTurnPairs — anlatıma giren en çok önceki soru/cevap çifti.
	wikiPriorTurnPairs = 1
	// wikiRefsMax — istemcinin yolladığı en çok önceki-sayfa href'i.
	wikiRefsMax = 4
	// wikiMemoTTL — cevap → sayfa hafızasının ömrü.
	wikiMemoTTL = 24 * time.Hour
	// wikiMemoMax — süreç-içi hafızanın kayıt tavanı.
	wikiMemoMax = 512
	// wikiFollowUpStepPrefix — adım çipi (yeniden okunan sayfayı adlandırır).
	wikiFollowUpStepPrefix = "wiki_followup · "
)

// wikiFollowUpExact / wikiFollowUpPrefixes — katlanmış geri-atıf / takip sözcükleri.
var (
	wikiFollowUpExact = map[string]bool{
		"bu": true, "bunun": true, "buna": true, "bunu": true, "bunda": true,
		"o": true, "onun": true, "ona": true, "onu": true, "onda": true, "orada": true,
		"nerede": true, "nereden": true, "hangi": true, "hangisi": true,
		"peki": true, "ya": true, "kim": true, "kimde": true,
	}
	wikiFollowUpPrefixes = []string{"link", "detay", "adres"}
)

// wikiFollowUpCue — SAF: soru eliptik/kısa ya da geri atıf taşıyor mu.
func wikiFollowUpCue(q string) bool {
	words := strings.FieldsFunc(wiki.Fold(q), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	if len(words) == 0 {
		return false
	}
	if len(words) <= wikiFollowUpMaxWords {
		return true
	}
	for _, w := range words {
		if wikiFollowUpExact[w] {
			return true
		}
		for _, p := range wikiFollowUpPrefixes {
			if strings.HasPrefix(w, p) {
				return true
			}
		}
	}
	return false
}

// wikiPriorPair — SAF: son kullanıcı sorusundan ÖNCEKİ soru ve cevap.
func wikiPriorPair(msgs []copilot.ChatMessage) (prevUser, prevAssistant string) {
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Text) != "" {
			last = i
			break
		}
	}
	if last < 0 {
		return "", ""
	}
	j := last - 1
	for ; j >= 0; j-- {
		if msgs[j].Role == "assistant" && strings.TrimSpace(msgs[j].Text) != "" {
			prevAssistant = msgs[j].Text
			break
		}
	}
	for k := j - 1; k >= 0; k-- {
		if msgs[k].Role == "user" && strings.TrimSpace(msgs[k].Text) != "" {
			prevUser = msgs[k].Text
			break
		}
	}
	return prevUser, prevAssistant
}

// priorTurnTagRe — önceki tur çitinin etiketleri (çitten taşma girişimi).
var priorTurnTagRe = regexp.MustCompile(`(?i)<\s*/?\s*onceki_konusma\s*>`)

// wikiPriorTurns — SAF: wiki anlatımlarına giren önceki tur bloğu (son
// wikiPriorTurnPairs soru/cevap, kırpılmış, çitli). Önceki tur yoksa "" —
// prompt bayt bayt eski.
func wikiPriorTurns(msgs []copilot.ChatMessage) string {
	last := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" && strings.TrimSpace(msgs[i].Text) != "" {
			last = i
			break
		}
	}
	if last <= 0 {
		return ""
	}
	// Son kullanıcı sorusundan geriye: en çok wikiPriorTurnPairs×2 tur.
	var turns []copilot.ChatMessage
	for i := last - 1; i >= 0 && len(turns) < wikiPriorTurnPairs*2; i-- {
		if strings.TrimSpace(msgs[i].Text) != "" {
			turns = append([]copilot.ChatMessage{msgs[i]}, turns...)
		}
	}
	if len(turns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("ÖNCEKİ KONUŞMA (yalnız atıfları çözmek için; bilgi kaynağı değil):\n<onceki_konusma>\n")
	for _, m := range turns {
		t := priorTurnTagRe.ReplaceAllString(wikiDataCloseRe.ReplaceAllString(m.Text, ""), "")
		if m.Role == "assistant" {
			fmt.Fprintf(&b, "Asistan: %s\n", clipRunes(strings.TrimSpace(t), wikiPriorAssistantRunes))
		} else {
			fmt.Fprintf(&b, "Operatör: %s\n", clipRunes(strings.TrimSpace(t), wikiPriorUserRunes))
		}
	}
	b.WriteString("</onceki_konusma>\n\n")
	return b.String()
}

// ── önceki cevabın sayfa hafızası ───────────────────────────────────────────

type wikiMemoEntry struct {
	refs []wiki.ChunkRef
	at   time.Time
}

// wikiMemo — süreç-içi yedek (Redis yoksa / ıskalarsa). Boyut ve süre sınırlı.
var wikiMemo = struct {
	mu    sync.Mutex
	m     map[string]wikiMemoEntry
	order []string
}{m: map[string]wikiMemoEntry{}}

func wikiMemoPut(key string, refs []wiki.ChunkRef, now time.Time) {
	wikiMemo.mu.Lock()
	defer wikiMemo.mu.Unlock()
	if _, ok := wikiMemo.m[key]; !ok {
		wikiMemo.order = append(wikiMemo.order, key)
	}
	wikiMemo.m[key] = wikiMemoEntry{refs: refs, at: now}
	for len(wikiMemo.order) > wikiMemoMax {
		delete(wikiMemo.m, wikiMemo.order[0])
		wikiMemo.order = wikiMemo.order[1:]
	}
}

func wikiMemoGet(key string, now time.Time) ([]wiki.ChunkRef, bool) {
	wikiMemo.mu.Lock()
	defer wikiMemo.mu.Unlock()
	e, ok := wikiMemo.m[key]
	if !ok || now.Sub(e.at) > wikiMemoTTL {
		return nil, false
	}
	return e.refs, true
}

func wikiDigest(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(h.Sum(nil))
}

// wikiAnswerKey — cevap metni → sayfa kimlikleri (kullanıcı kapsamlı).
func wikiAnswerKey(ctx context.Context, text string) string {
	uid := ""
	if c := auth.FromContext(ctx); c != nil {
		uid = c.UserID
	}
	return "wikifu:ans:" + uid + ":" + wikiDigest(text)
}

func wikiURLKey(u string) string { return "wikifu:url:" + wikiDigest(u) }

func (s *Server) memoSet(ctx context.Context, key string, refs []wiki.ChunkRef) {
	wikiMemoPut(key, refs, time.Now())
	if s == nil || s.cache == nil {
		return
	}
	if b, err := json.Marshal(refs); err == nil {
		_ = s.cache.Set(ctx, key, b, wikiMemoTTL)
	}
}

func (s *Server) memoGet(ctx context.Context, key string) ([]wiki.ChunkRef, bool) {
	if refs, ok := wikiMemoGet(key, time.Now()); ok {
		return refs, true
	}
	if s == nil || s.cache == nil {
		return nil, false
	}
	b, ok, err := s.cache.Get(ctx, key)
	if err != nil || !ok {
		return nil, false
	}
	var refs []wiki.ChunkRef
	if json.Unmarshal(b, &refs) != nil || len(refs) == 0 {
		return nil, false
	}
	return refs, true
}

// wikiPageRefs — SAF: isabetlerin sayfa kimlikleri (sayfa başına bir, sıra korunur).
func wikiPageRefs(h []wiki.Hit) []wiki.ChunkRef {
	seen := map[string]bool{}
	var out []wiki.ChunkRef
	for _, x := range h {
		if seen[x.PageKey()] {
			continue
		}
		seen[x.PageKey()] = true
		r := x.ChunkRef
		r.Text, r.Heading, r.Idx = "", "", 0
		out = append(out, r)
	}
	return out
}

// rememberWikiAnswer — wiki cevabının metni ve url'leri → sayfa kimlikleri.
// Takip turunda istemci yapısal kaynak yollamasa da sayfa çözülebilsin.
func (s *Server) rememberWikiAnswer(ctx context.Context, text string, refs []wiki.ChunkRef) {
	if len(refs) == 0 || strings.TrimSpace(text) == "" {
		return
	}
	s.memoSet(ctx, wikiAnswerKey(ctx, text), refs)
	for _, r := range refs {
		if strings.TrimSpace(r.URL) != "" {
			s.memoSet(ctx, wikiURLKey(r.URL), []wiki.ChunkRef{r})
		}
	}
}

// ── URL → sayfa ─────────────────────────────────────────────────────────────

var wikiURLRe = regexp.MustCompile(`https?://[^\s<>"'()\[\]` + "`" + `]+`)

// wikiURLsInText — SAF: metindeki wiki sayfa url'leri (yedek yol).
func wikiURLsInText(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range wikiURLRe.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".,;:!?*_")
		if !strings.Contains(u, "/_wiki/") || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
		if len(out) >= wikiRefsMax {
			break
		}
	}
	return out
}

// cleanWikiRefs — SAF: istemcinin href listesi (http(s), tekil, tavanlı).
func cleanWikiRefs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, u := range in {
		u = strings.TrimSpace(u)
		if len(u) > 2048 || seen[u] || !(strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://")) {
			continue
		}
		seen[u] = true
		out = append(out, u)
		if len(out) >= wikiRefsMax {
			break
		}
	}
	return out
}

// parseWikiURL — SAF: ".../{project}/_wiki/wikis/{wiki}?pagePath=/a/b" ya da
// ".../{project}/_wiki/wikis/{wiki}/{id}/{Başlık-Adı}" → parçalar. path boşsa
// title (url'nin son parçası, tireler boşluk) doludur.
func parseWikiURL(href string) (project, wikiName, path, title string, ok bool) {
	u, err := url.Parse(href)
	if err != nil {
		return "", "", "", "", false
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 1; i+2 < len(segs); i++ {
		if segs[i] != "_wiki" || segs[i+1] != "wikis" {
			continue
		}
		project, wikiName = segs[i-1], segs[i+2]
		if p := wiki.NormalizePagePath(u.Query().Get("pagePath")); p != "" {
			return project, wikiName, p, "", true
		}
		if len(segs) >= i+5 {
			title = strings.TrimSpace(strings.ReplaceAll(segs[len(segs)-1], "-", " "))
			return project, wikiName, "", title, title != ""
		}
		return "", "", "", "", false
	}
	return "", "", "", "", false
}

// resolveWikiURL — href → sayfa kimliği: hafıza, url ayrıştırma, başlıkla arama.
func (s *Server) resolveWikiURL(ctx context.Context, w *wiki.Service, href string) (wiki.ChunkRef, bool) {
	if refs, ok := s.memoGet(ctx, wikiURLKey(href)); ok {
		return refs[0], true
	}
	project, wikiName, path, title, ok := parseWikiURL(href)
	if !ok {
		return wiki.ChunkRef{}, false
	}
	if path != "" {
		return wiki.ChunkRef{Project: project, WikiName: wikiName, Path: path, URL: href, Title: wiki.PageTitle(path)}, true
	}
	res, err := w.SearchWith(ctx, title, project, wiki.SearchOptions{Limit: 5, PerPage: 1, Live: wiki.LiveOnWeak})
	if err != nil {
		return wiki.ChunkRef{}, false
	}
	for _, h := range res.Hits {
		if h.URL == href {
			r := h.ChunkRef
			r.Text, r.Heading, r.Idx = "", "", 0
			return r, true
		}
	}
	return wiki.ChunkRef{}, false
}

// wikiPrevPages — önceki asistan turunun wiki sayfaları (yoksa nil: önceki
// tur wiki cevabı değil). Yapısal veri önce; sonra hafıza; sonra metin url'leri.
func (s *Server) wikiPrevPages(ctx context.Context, w *wiki.Service, msgs []copilot.ChatMessage, clientRefs []string) []wiki.ChunkRef {
	_, prevAssistant := wikiPriorPair(msgs)
	if strings.TrimSpace(prevAssistant) == "" {
		return nil
	}
	hrefs := cleanWikiRefs(clientRefs)
	if len(hrefs) == 0 {
		if refs, ok := s.memoGet(ctx, wikiAnswerKey(ctx, prevAssistant)); ok {
			return refs
		}
		hrefs = wikiURLsInText(prevAssistant)
	}
	var out []wiki.ChunkRef
	for _, h := range hrefs {
		if r, ok := s.resolveWikiURL(ctx, w, h); ok {
			out = append(out, r)
		}
	}
	return out
}

// ── takip cevabı ────────────────────────────────────────────────────────────

// wikiNotInPage — SAF: model sayfada cevap olmadığını mı söyledi.
func wikiNotInPage(raw string) bool {
	t := strings.TrimSpace(raw)
	return t == "" || strings.Contains(t, copilot.WikiNotInPageSentinel) || wikiDeclined(t)
}

// wikiDeclined — SAF: wiki anlatımı "Wikide bulunamadı" ile mi başladı.
func wikiDeclined(raw string) bool {
	return strings.HasPrefix(wiki.Fold(strings.TrimSpace(raw)), wiki.Fold("Wikide bulunamadı"))
}

func wikiRecSource(rec *wiki.PageRecord) chatSource {
	return chatSource{Doc: "Wiki · " + rec.Title, Ref: rec.URL, Chunk: 1, Score: 1}
}

// wikiRecLinks — SAF: sayfaların çipleri (sıra korunur → önceki sayfa ilk).
func wikiRecLinks(recs []*wiki.PageRecord) []guidedAnswerLink {
	h := make([]wiki.Hit, 0, len(recs))
	for _, r := range recs {
		h = append(h, wiki.Hit{ChunkRef: wiki.ChunkRef{Title: r.Title, URL: r.URL}})
	}
	return ragWikiLinks(h)
}

// wikiFollowUpLimits — SAF (v0.10.1136): yeniden okunan sayfaların bütçe
// payları — tek sayfa bütçenin tamamı; iki sayfada ilki wikiFollowUpFirstShare,
// ikincisi kalanı.
func wikiFollowUpLimits(n, budget int) []int {
	if n <= 0 {
		return nil
	}
	if n == 1 {
		return []int{budget}
	}
	first := int(float64(budget) * wikiFollowUpFirstShare)
	out := []int{first}
	rest := (budget - first) / (n - 1)
	for i := 1; i < n; i++ {
		out = append(out, rest)
	}
	return out
}

// buildWikiFollowUpUser — SAF: sayfa-okuma anlatımının kullanıcı bloğu.
// v0.10.1136: sayfa başlığıyla numaralı kaynak ("[n] Wiki · başlık"),
// tavanlar modelin penceresinden türeyen bütçeden (wikiFollowUpLimits).
func buildWikiFollowUpUser(prior, question string, recs []*wiki.PageRecord, num sourceNumbers, budget int) string {
	var b strings.Builder
	b.WriteString(prior)
	b.WriteString("TAKİP SORUSU: " + question + "\n\nSAYFA:\n")
	// Bütçe blokların TAMAMINI kapsar: işaret ve "Sayfa:" satırının payı düşülür.
	overhead := 0
	for _, rec := range recs {
		overhead += runeLen(wikiSourceBlock(0, rec.Title, "")) + 40
	}
	limits := wikiFollowUpLimits(len(recs), max(budget-overhead, wikiMinUsefulRunes))
	for i, rec := range recs {
		block := wikiSourceBlock(num.of(wikiRecSource(rec)), rec.Title, clipRunes(strings.TrimSpace(rec.Content), limits[i]))
		// "[n] Wiki · başlık" işaretine "(önceki cevabın kaynağı)" eki (çit dışı, sabit metin).
		if j := strings.IndexByte(block, '\n'); j > 0 {
			block = block[:j] + " (önceki cevabın kaynağı)" + block[j:]
		}
		b.WriteString(block)
	}
	return b.String()
}

// wikiContextQuery — SAF: bağlamlı arama sorgusu (takip terimleri önce —
// QueryTerms tavanı kuyruğu keser).
func wikiContextQuery(question, prevUser string, titles []string) string {
	parts := append([]string{question}, titles...)
	parts = append(parts, prevUser)
	return strings.Join(parts, " ")
}

// wikiFollowUpAnswer — takip kademesi. handled=false → takip değil ya da
// sayfa/bağlamlı arama cevaplamadı (akış bugünkü hâliyle sürer).
func (s *Server) wikiFollowUpAnswer(ctx context.Context, emit func(string, any), w *wiki.Service, msgs []copilot.ChatMessage, clientRefs []string, question string) (handled, ok bool) {
	if !wikiFollowUpCue(question) || guidedTelemetrySignal(question) {
		return false, false
	}
	pages := s.wikiPrevPages(ctx, w, msgs, clientRefs)
	if len(pages) == 0 {
		return false, false // önceki tur wiki cevabı değil
	}
	if s.wikiWeakCueVetoed(ctx, question) {
		return false, false // servis adı + telemetri sinyali: guided'ın sorusu
	}
	prior := wikiPriorTurns(msgs)
	prevUser, _ := wikiPriorPair(msgs)

	// (a) önceki sayfa(lar)ı yeniden oku.
	var recs []*wiki.PageRecord
	for _, p := range pages {
		if len(recs) >= wikiFollowUpPages {
			break
		}
		ref := p.WikiID
		if ref == "" {
			ref = p.WikiName
		}
		rec, err := w.ReadPage(ctx, p.Project, ref, p.Path)
		if err != nil || rec == nil || strings.TrimSpace(rec.Content) == "" {
			continue
		}
		if rec.URL == "" {
			rec.URL = p.URL
		}
		recs = append(recs, rec)
	}
	exID := copilot.MetaFromContext(ctx).ExchangeID
	if len(recs) > 0 {
		titles := make([]string, 0, len(recs))
		for _, r := range recs {
			titles = append(titles, r.Title)
		}
		emit("step", map[string]string{"label": wikiFollowUpStepPrefix + strings.Join(titles, ", ")})
		sources := make([]chatSource, 0, len(recs))
		for _, r := range recs {
			sources = append(sources, wikiRecSource(r))
		}
		user := buildWikiFollowUpUser(prior, question, recs, numberSources(sources), s.wikiBudget(ctx, w, "wiki-chat"))
		raw, err := wikiNarrateFn(s, ctx, copilot.SystemPromptWikiFollowUp(), user)
		if err != nil {
			if ctx.Err() != nil {
				emit("error", map[string]string{"error": err.Error()})
				return true, false
			}
		} else if !wikiNotInPage(raw) {
			text := strings.TrimSpace(raw)
			s.rememberWikiAnswer(ctx, text, recRefs(recs))
			emit("answer", map[string]any{"text": text, "exchangeId": exID,
				"sources": dedupeChatSources(sources), "links": wikiRecLinks(recs)})
			return true, true
		}
	}

	// (b) bağlamlı arama: önce aynı proje, önceki sayfa(lar) hariç.
	titles := make([]string, 0, len(pages))
	exclude := map[string]bool{}
	for _, p := range pages {
		titles = append(titles, p.Title)
		exclude[p.URL] = true
	}
	for _, r := range recs {
		exclude[r.URL] = true
		exclude[r.WikiID+"\x00"+r.Path] = true
	}
	q := wikiContextQuery(question, prevUser, titles)
	var hits []wiki.Hit
	for _, project := range []string{pages[0].Project, ""} {
		res, err := w.SearchWith(ctx, q, project, wiki.SearchOptions{Limit: wikiTierSearchLimit, PerPage: wikiTierPerPage, Live: wiki.LiveOnWeak})
		if err != nil && ctx.Err() != nil {
			return false, false
		}
		var keep []wiki.Hit
		for _, h := range res.Hits {
			if !exclude[h.URL] && !exclude[h.PageKey()] {
				keep = append(keep, h)
			}
		}
		if hits = wikiTierSelect(keep); len(hits) > 0 || project == "" {
			break
		}
	}
	if len(hits) == 0 || !hits[0].EvidencedAt(wikiTierFloor) {
		return false, false // (c) bugünkü akış
	}
	emit("step", map[string]string{"label": "kurum wiki'si"})
	ans, used, err := s.wikiNarratedAnswer(ctx, w, question, prior, hits, emit) // v0.10.1136: iki aşamalı seçim (b)
	if err != nil {
		if ctx.Err() != nil {
			emit("error", map[string]string{"error": err.Error()})
			return true, false
		}
		return false, false
	}
	if text, _ := ans["text"].(string); wikiDeclined(text) {
		return false, false
	}
	// v0.10.1136: yeni sayfalar önce ([n] / "Kaynak n" sırası), önceki sayfanın çipi (bağlam çıpası) sonda.
	links, _ := ans["links"].([]guidedAnswerLink)
	ans["links"] = dedupLinksByHref(append(append([]guidedAnswerLink(nil), links...), wikiRecLinks(recs)...))
	text, _ := ans["text"].(string)
	s.rememberWikiAnswer(ctx, text, wikiPageRefs(used))
	emit("answer", ans)
	return true, true
}

// recRefs — SAF: okunan sayfaların kimlikleri.
func recRefs(recs []*wiki.PageRecord) []wiki.ChunkRef {
	out := make([]wiki.ChunkRef, 0, len(recs))
	for _, r := range recs {
		out = append(out, wiki.ChunkRef{Project: r.Project, WikiID: r.WikiID, WikiName: r.WikiName, Path: r.Path, Title: r.Title, URL: r.URL})
	}
	return out
}
