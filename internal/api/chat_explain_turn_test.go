package api

// chat_explain_turn_test.go — v0.10.1400 (Tek CoSRE). Operatör: "Trace ve
// exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı
// drawer hissi, CoSRE bütünselliğine uymuyor." / "Explain trace'te çıkan
// loading ikonu güzel ama chat'ten 'bu trace'i açıkla' deyince aynı yerde
// devam etmiyor, sanki yeni drawer açılıyor."
//
// Pinler: yapısal explainAsk turu ✨ düğmesinin çekirdeğiyle (aynı prompt,
// aynı önbellek anahtarı) sohbet içinde akar; kademe her metin eşlemesinden
// önce; sohbete yazılan "<id> açıkla" artık `open` ile çekmece açmaz; arşiv
// künyesi sanitize edilir; grup çipi açıklama niyeti taşır.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func TestNormalizeExplainAsk(t *testing.T) {
	const tid = "4BF92F3577B34DA6A3CE929D0E0E4736"
	for _, c := range []struct {
		name string
		in   *chatExplainAsk
		want chatExplainAsk
		ok   bool
	}{
		{"nil", nil, chatExplainAsk{}, false},
		{"trace büyük harf → küçük", &chatExplainAsk{Kind: "Trace", ID: " " + tid + " ", Src: "button"}, chatExplainAsk{Kind: "trace", ID: strings.ToLower(tid), Src: "button"}, true},
		{"trace kısa", &chatExplainAsk{Kind: "trace", ID: "4bf92f35"}, chatExplainAsk{}, false},
		{"trace hex değil", &chatExplainAsk{Kind: "trace", ID: strings.Repeat("z", 32)}, chatExplainAsk{}, false},
		{"oracle grubu", &chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2"}, chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2"}, true},
		{"span grubu", &chatExplainAsk{Kind: "exception", ID: "0000000000000c3"}, chatExplainAsk{Kind: "exception", ID: "0000000000000c3"}, true},
		{"exception boşluk", &chatExplainAsk{Kind: "exception", ID: "a b"}, chatExplainAsk{}, false},
		{"exception enjeksiyon", &chatExplainAsk{Kind: "exception", ID: "x&ai=trace"}, chatExplainAsk{}, false},
		{"exception çok uzun", &chatExplainAsk{Kind: "exception", ID: strings.Repeat("a", chatExplainIDMax+1)}, chatExplainAsk{}, false},
		{"bilinmeyen tür", &chatExplainAsk{Kind: "span", ID: tid}, chatExplainAsk{}, false},
	} {
		got, ok := normalizeExplainAsk(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %+v ok=%v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

type xtEvent struct {
	kind string
	data any
}

func xtRecorder(evs *[]xtEvent) func(string, any) {
	return withStepIDs(func(k string, v any) { *evs = append(*evs, xtEvent{k, v}) })
}

func xtKinds(evs []xtEvent) string {
	var k []string
	for _, e := range evs {
		k = append(k, e.kind)
	}
	return strings.Join(k, ",")
}

func xtAnswer(t *testing.T, evs []xtEvent) map[string]any {
	t.Helper()
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].kind == "answer" {
			return evs[i].data.(map[string]any)
		}
	}
	t.Fatalf("answer yok: %s", xtKinds(evs))
	return nil
}

// Trace turu: düğmenin klasik çekirdeği; düğme ile sohbet AYNI önbellek satırı.
func TestChatExplainTurnTraceSharesButtonCache(t *testing.T) {
	p := newDefProvider(t, "**Kök neden:** payments POST /charge ", "card declined.")
	s := defServer(t, p)
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-trace-1"})
	handled, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &chatExplainAsk{Kind: "trace", ID: defTestTrace, Src: "button"}, time.UTC)
	if !handled || !ok {
		t.Fatalf("handled=%v ok=%v %s", handled, ok, xtKinds(evs))
	}
	if got := xtKinds(evs); got != "step,step-result,delta,delta,answer" {
		t.Fatalf("olay sırası %s", got)
	}
	sys, user := p.lastMessages(t)
	if sys != copilot.SystemPromptTrace() {
		t.Fatal("sistem prompt'u Explain trace'inki değil")
	}
	ans := xtAnswer(t, evs)
	if ans["text"] != defLatencyLine+"**Kök neden:** payments POST /charge card declined." || ans["exchangeId"] != "x-trace-1" { // v0.10.1630 — POST /charge en büyük pay, çocuksuz CLIENT
		t.Fatalf("cevap %v / %v", ans["text"], ans["exchangeId"])
	}
	if _, has := ans["open"]; has {
		t.Fatal("açıklama turu çekmece açmamalı (open)")
	}
	meta, _ := ans["explain"].(chatExplainMeta)
	if meta.Kind != "trace" || meta.ID != defTestTrace || meta.Surface != aisurface.ExplainTraceChat || meta.Cached {
		t.Fatalf("künye %+v", meta)
	}
	if len(meta.EvidenceSpanIDs) == 0 {
		t.Fatal("künye kanıt span'lerini taşımalı (waterfall kutulaması)")
	}
	// Anahtar = explainTraceClassicPrepared'ın formülü.
	key := explainCacheKey(copilot.SystemPromptTrace(), user, "")
	if _, hit, _ := s.cache.Get(context.Background(), key); !hit {
		t.Fatal("cevap düğmenin önbellek anahtarına yazılmadı")
	}
	// Düğme şimdi isabet eder: LLM çağrısı yok, cached etiketi.
	calls := p.requests()
	w := defCall(s, "", "")
	if w.Code != http.StatusOK {
		t.Fatalf("düğme HTTP %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["cached"] != true || p.requests() != calls {
		t.Fatalf("düğme sohbetin cevabına isabet etmeli: cached=%v calls %d→%d", body["cached"], calls, p.requests())
	}
	// İkinci sohbet turu: isabet → tek delta, saklanan exchangeId, sıfır LLM.
	evs = nil
	if h, o := s.chatExplainTurn(copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-trace-2"}), xtRecorder(&evs),
		&chatExplainAsk{Kind: "trace", ID: defTestTrace}, time.UTC); !h || !o {
		t.Fatal("isabet turu işlenmedi")
	}
	ans = xtAnswer(t, evs)
	meta, _ = ans["explain"].(chatExplainMeta)
	if p.requests() != calls || ans["cached"] != true || !meta.Cached || meta.CachedAtMs == 0 || ans["exchangeId"] != "x-trace-1" {
		t.Fatalf("isabet: calls %d→%d cached=%v meta=%+v xid=%v", calls, p.requests(), ans["cached"], meta, ans["exchangeId"])
	}
	if got := xtKinds(evs); got != "step,step-result,delta,answer" {
		t.Fatalf("isabet olay sırası %s", got)
	}
}

// Exception turu: Exceptions açıklamasının girdisi + prompt'u + anahtarı.
func TestChatExplainTurnException(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"**Hata ne demek** ORD-200 ", "SAXParseException."}}
	s := newOpExplainServer(t, prov)
	mc := newMemCache()
	s.cache = mc
	_, asked := opExplainFixture(t, nil)
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: "x-exc-1"})
	handled, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2", Src: "deeplink"}, time.UTC)
	if !handled || !ok || len(*asked) != 1 || (*asked)[0] != "ora:00000000000000b2" {
		t.Fatalf("handled=%v ok=%v asked=%v", handled, ok, *asked)
	}
	if got := xtKinds(evs); got != "step,step-result,delta,delta,answer" {
		t.Fatalf("olay sırası %s", got)
	}
	if prov.system != copilot.SystemPromptOracleException() || !strings.HasPrefix(prov.user, "EXCEPTION KANITI ora:00000000000000b2") {
		t.Fatal("Exceptions açıklamasının prompt çifti kullanılmadı")
	}
	ans := xtAnswer(t, evs)
	text := ans["text"].(string)
	if text != "**Hata ne demek** ORD-200 SAXParseException." {
		t.Fatalf("metin %q (künye satırı olmamalı — düğmeyle birebir)", text)
	}
	meta, _ := ans["explain"].(chatExplainMeta)
	if meta.Kind != "exception" || meta.ID != "ora:00000000000000b2" || meta.Label != "ORD-200" || meta.Surface != aisurface.ExplainExceptionChat {
		t.Fatalf("künye %+v", meta)
	}
	links, _ := ans["links"].([]guidedAnswerLink)
	if len(links) == 0 || links[0].Href != "/problems?exc=ora%3A00000000000000b2" {
		t.Fatalf("grup linki çıplak grup sayfası olmalı (ai= yok): %+v", links)
	}
	key := explainCacheKey(copilot.SystemPromptOracleException(), prov.user, "")
	if _, hit, _ := mc.Get(context.Background(), key); !hit {
		t.Fatal("cevap copilotExplainException'ın anahtarına yazılmadı")
	}
	calls := prov.calls
	evs = nil
	if h, o := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2"}, time.UTC); !h || !o {
		t.Fatal("isabet turu")
	}
	if ans = xtAnswer(t, evs); prov.calls != calls || ans["cached"] != true || ans["exchangeId"] != "x-exc-1" {
		t.Fatalf("isabet: calls %d→%d cached=%v xid=%v", calls, prov.calls, ans["cached"], ans["exchangeId"])
	}
}

func TestChatExplainTurnFallsThroughAndNotFound(t *testing.T) {
	prov := &opExplainProvider{deltas: []string{"x"}}
	s := newOpExplainServer(t, prov)
	for _, bad := range []*chatExplainAsk{nil, {Kind: "span", ID: defTestTrace}, {Kind: "trace", ID: "nope"}, {Kind: "exception", ID: ""}} {
		var evs []xtEvent
		if h, _ := s.chatExplainTurn(context.Background(), xtRecorder(&evs), bad, time.UTC); h || len(evs) != 0 {
			t.Errorf("geçersiz niyet sahiplenildi: %+v %s", bad, xtKinds(evs))
		}
	}
	if h, _ := (&Server{}).chatExplainTurn(context.Background(), func(string, any) {}, &chatExplainAsk{Kind: "trace", ID: defTestTrace}, time.UTC); h {
		t.Fatal("copilot yokken sahiplenildi")
	}
	// Grup okunamadı → dürüst LLM'siz cevap, tur sahiplenilir.
	opExplainFixture(t, errors.New("yok"))
	var evs []xtEvent
	h, ok := s.chatExplainTurn(context.Background(), xtRecorder(&evs), &chatExplainAsk{Kind: "exception", ID: "ora:00000000000000ff"}, time.UTC)
	if !h || ok || xtKinds(evs) != "step,step-result,answer" || prov.calls != 0 {
		t.Fatalf("bulunamadı: h=%v ok=%v %s calls=%d", h, ok, xtKinds(evs), prov.calls)
	}
	if txt, _ := xtAnswer(t, evs)["text"].(string); !strings.Contains(txt, "bulunamadı") {
		t.Fatalf("metin %q", txt)
	}
}

// Sohbete yazılan "bu trace'i açıkla": v0.10.460'ın `open`'ı yok; aynı künye,
// düğmenin anahtarı.
func TestGuidedTraceExplainAskStaysInChat(t *testing.T) {
	p := newDefProvider(t, "özet")
	s := defServer(t, p)
	var evs []xtEvent
	to := time.Now()
	handled, ok := s.guidedTraceExplain(context.Background(), xtRecorder(&evs), guidedRoute{Intent: guidedTraceByID, TraceID: defTestTrace},
		"bu trace'i açıkla", to.Add(-time.Hour), to, "")
	if !handled || !ok {
		t.Fatalf("handled=%v ok=%v", handled, ok)
	}
	ans := xtAnswer(t, evs)
	if _, has := ans["open"]; has {
		t.Fatal("open yayınlandı — açıklama sohbet içinde akmalı")
	}
	if meta, _ := ans["explain"].(chatExplainMeta); meta.Kind != "trace" || meta.ID != defTestTrace {
		t.Fatalf("künye %+v", ans["explain"])
	}
	_, user := p.lastMessages(t)
	if _, hit, _ := s.cache.Get(context.Background(), explainCacheKey(copilot.SystemPromptTrace(), user, "")); !hit {
		t.Fatal("açıklama isteği düğmenin anahtarını kullanmalı")
	}
	// Odaklı soru: künye yok, önbelleğe girmez.
	evs = nil
	if h, o := s.guidedTraceExplain(context.Background(), xtRecorder(&evs), guidedRoute{Intent: guidedTraceByID, TraceID: defTestTrace},
		"bu trace neden yavaş", to.Add(-time.Hour), to, ""); !h || !o {
		t.Fatal("odaklı soru")
	}
	if _, has := xtAnswer(t, evs)["explain"]; has {
		t.Fatal("odaklı soru açıklama künyesi taşımamalı")
	}
}

// Kademe sırası: explainAsk her metin eşlemesinden ÖNCE ve handled → döner.
func TestChatExplainTurnTierIsFirst(t *testing.T) {
	src := stripGoCommentsAPI(readRepoFile(t, "copilot_chat.go"))
	iX := strings.Index(src, "s.chatExplainTurn(ctx, emit,")
	if iX < 0 {
		t.Fatal("açıklama turu kademesi yok")
	}
	for _, later := range []string{"s.chatScopeTier(ctx, emit,", "s.chatMemoryTier(ctx, emit,", "s.wikiChatAnswer(ctx, emit,",
		"s.chatOperationPrefetch(ctx, emit,", tierMarkers[0].marker} {
		if j := strings.Index(src, later); j < 0 || j < iX {
			t.Errorf("%q açıklama turundan önce (ya da yok): %d < %d", later, j, iX)
		}
	}
	cond := src[iX:]
	if br := strings.Index(cond, "{"); br > 0 && !strings.Contains(cond[:br], "handled") {
		t.Error("kademe handled okumuyor")
	}
	if !strings.Contains(tierBlock(src, iX), "return") {
		t.Error("kademe handled olduğunda dönmüyor")
	}
	// Üç çağıran da tek çekirdekten akar. v0.10.1406 — + fresh (Yeniden üret).
	for f, want := range map[string]string{
		"chat_explain_turn.go":                `s.streamTraceExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, in.User, cacheKey, ask.Fresh)`, /* v0.10.1630 — dış bağımlılık satırlı ikiz */
		"trace_explain_unified.go":            `s.streamTraceExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, user, "", false)`,              // v0.10.1630
		"chat_operation_exception_explain.go": `s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User,`,
	} {
		if !strings.Contains(readRepoFile(t, f), want) {
			t.Errorf("%s çekirdekten geçmiyor (%q)", f, want)
		}
	}
	if !strings.Contains(readRepoFile(t, "chat_explain_turn.go"), `explainCacheKey(system, in.User, "") // explainTraceClassicPrepared ile AYNI formül`) {
		t.Fatal("trace turu düğmenin anahtar formülünü kullanmalı")
	}
	if evalSurfaceFromLabel(aisurface.ExplainTraceChat) != "Trace" || evalSurfaceFromLabel(aisurface.ExplainExceptionChat) != "Exception" {
		t.Fatal("sohbet içi açıklama yüzeyleri evalset ailesine çözülmüyor")
	}
}

// Arşiv künyesi: tür beyaz listesi, kimlik biçimi, kanıt tavanı; kullanıcı
// turunda künye düşer; JSON gidiş-dönüş aynı.
func TestSanitizeChatExplainRoundTrip(t *testing.T) {
	many := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		many = append(many, "aaaaaaaaaaaaaaa1")
	}
	in := []aiChatMessage{
		{Role: "user", Text: "Bu trace'i açıkla · 4bf92f35…", Explain: &chatExplainMeta{Kind: "trace", ID: defTestTrace}},
		{Role: "assistant", Text: "özet", Explain: &chatExplainMeta{Kind: "trace", ID: strings.ToUpper(defTestTrace), Surface: aisurface.ExplainTraceChat,
			Cached: true, CachedAtMs: 5, EvidenceSpanIDs: append(many, "<script>"), OracleRows: 2}},
		// v0.10.1405 — problem artık kayıtta; kayıt dışı tür (charts — 1407) düşer.
		{Role: "assistant", Text: "kötü tür", Explain: &chatExplainMeta{Kind: "charts", ID: "orders-api"}},
		{Role: "assistant", Text: "grup", Explain: &chatExplainMeta{Kind: "exception", ID: "ora:00000000000000b2", Label: strings.Repeat("L", 500),
			EvidenceTraceIDs: []string{defTestTrace, "javascript:x"}}},
	}
	out := sanitizeChatMessages(in)
	if len(out) != 4 || out[0].Explain != nil {
		t.Fatalf("kullanıcı turunda künye düşmeli: %+v", out[0])
	}
	m := out[1].Explain
	if m == nil || m.ID != defTestTrace || len(m.EvidenceSpanIDs) != chatExplainEvidenceMax || m.OracleRows != 2 || !m.Cached {
		t.Fatalf("trace künyesi %+v", m)
	}
	if out[2].Explain != nil {
		t.Fatal("bilinmeyen tür arşive girdi")
	}
	g := out[3].Explain
	if g == nil || len([]rune(g.Label)) != aiChatEvidenceFieldMax || len(g.EvidenceTraceIDs) != 1 {
		t.Fatalf("grup künyesi %+v", g)
	}
	raw, _ := json.Marshal(out)
	var back []aiChatMessage
	if err := json.Unmarshal(raw, &back); err != nil || back[1].Explain == nil || back[1].Explain.Kind != "trace" {
		t.Fatalf("gidiş-dönüş: %v %+v", err, back)
	}
	// Künyesiz mesajın gövdesi bayt bayt eski şekil.
	plain, _ := json.Marshal(aiChatMessage{Role: "assistant", Text: "x"})
	if strings.Contains(string(plain), "explain") {
		t.Fatalf("künyesiz mesaj explain alanı taşıdı: %s", plain)
	}
}

// Grup çipi: açıklama niyeti taşır, eski istemcinin suggestions'ına girmez.
func TestExceptionGroupChipCarriesExplainAsk(t *testing.T) {
	c, ok := renderFollowup(followupFact{Kind: fuKindExceptionGroup, Code: "ORD-100", Fingerprint: "ora:00000000000000a1"})
	if !ok || c.ExplainAsk == nil || c.ExplainAsk.Kind != explainAskException || c.Href != "" {
		t.Fatalf("çip %+v", c)
	}
	ans := withAnswerFollowups(map[string]any{}, []chatFollowup{c})
	if _, has := ans["suggestions"]; has {
		t.Fatal("açıklama çipi düz metin önerisi olarak sızdı")
	}
	b, _ := json.Marshal(c)
	if !strings.Contains(string(b), `"explainAsk":{"kind":"exception","id":"ora:00000000000000a1","src":"chip"}`) {
		t.Fatalf("tel şekli %s", b)
	}
}
