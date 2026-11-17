package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// chat_state_flow_test.go — v0.10.2324 (conversation-audit §6.1 A, kabul):
// GERÇEK v2 handler'ı (POST /api/cosre/v2/chat, buildMux) + sahte model +
// sahte araçlar + Redis sahtesi (ctxMemCache). Dört tur: durum sorusu →
// "deploy mu vardı" → öneri + "bak" → "peki dün?". Guided kademesi store'suz
// sunucuda demet koşamaz; dikiş rotayı ve penceresini kaydeder, tur guided
// olarak biter (yönlendirme regresyon kümesinin deseni).

const stateConv = "conv-state"

func setConversationState(t *testing.T, on bool) {
	t.Helper()
	prev := chstore.CurrentCosreConversation()
	c := prev
	c.State = on
	chstore.SetCosreConversation(c)
	t.Cleanup(func() { chstore.SetCosreConversation(prev) })
}

type guidedCall struct {
	route  guidedRoute
	rangeS int64
	anchor time.Time
}

type stateRig struct {
	t      *testing.T
	s      *Server
	mc     *ctxMemCache
	llm    *loopLLM
	tools  *fakeTools
	mu     sync.Mutex
	guided []guidedCall
	msgs   []map[string]any
	now    time.Time // çözücünün saati (chatStateNow dikişi)
}

// stateFlowNow — günün saatinden bağımsız deterministik "şimdi": yerel 14:30
// (henüz gelmediyse dünün 14:30'u) — çıpalar gerçek saatten ileri olmaz.
func stateFlowNow() time.Time {
	loc, err := time.LoadLocation("Europe/Istanbul")
	if err != nil {
		loc = time.FixedZone("TRT", 3*3600)
	}
	n := time.Now().In(loc)
	at := time.Date(n.Year(), n.Month(), n.Day(), 14, 30, 0, 0, loc)
	if at.After(n) {
		at = at.AddDate(0, 0, -1)
	}
	return at
}

// newStateRig — loop: döngünün n. turunda (araç sunulan çağrı) modelin
// mesajı; araçsız çağrılar (niyet sınıflandırıcısı) "none" döner. guidedAns:
// guided rotasının cevap metni (dikiş "answer" yayınlar; "" = cevapsız).
func newStateRig(t *testing.T, loop func(last string, n int) map[string]any, guidedAns func(guidedRoute) string) *stateRig {
	t.Helper()
	rg := &stateRig{t: t, now: stateFlowNow()}
	prevNow := chatStateNow
	chatStateNow = func() time.Time { return rg.now }
	t.Cleanup(func() { chatStateNow = prevNow })
	perQ := map[string]int{}
	var mu sync.Mutex
	rg.llm = newLoopLLM(t, func(_ int, body map[string]any) map[string]any {
		if _, ok := body["tools"]; !ok {
			return answerMsg(`{"intent":"none"}`)
		}
		last := lastUserContent(body)
		mu.Lock()
		n := perQ[last]
		perQ[last]++
		mu.Unlock()
		return loop(last, n)
	})
	rg.s, rg.mc = newLoopTestServer(t, rg.llm, "u-state")
	rg.s.l1, rg.s.stats = newL1Cache(64), newCacheStats()
	rg.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","payments"]`)
	setCosreV2(t, rg.s, true)
	rg.tools = &fakeTools{}
	rg.tools.install(t)
	prev := guidedDispatchSeam
	prevW := guidedWindowSeam
	guidedDispatchSeam = func(guidedRoute) bool { return true }
	guidedWindowSeam = func(r guidedRoute, rangeS int64, anchor time.Time, emit func(string, any)) {
		rg.mu.Lock()
		rg.guided = append(rg.guided, guidedCall{r, rangeS, anchor})
		rg.mu.Unlock()
		if guidedAns != nil {
			if a := guidedAns(r); a != "" {
				emit("answer", map[string]any{"text": a})
			}
		}
	}
	t.Cleanup(func() { guidedDispatchSeam, guidedWindowSeam = prev, prevW })
	return rg
}

// lastUserContent — sahte modele giden son kullanıcı mesajı.
func lastUserContent(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		m, _ := msgs[i].(map[string]any)
		if m["role"] == "user" {
			s, _ := m["content"].(string)
			return s
		}
	}
	return ""
}

// ask — bir tur; asistan cevabı geçmişe eklenir.
func (rg *stateRig) ask(q string, extra map[string]any) string {
	rg.t.Helper()
	rg.msgs = append(rg.msgs, map[string]any{"role": "user", "text": q})
	cx := map[string]any{"conversation": stateConv, "tz": "Europe/Istanbul"}
	for k, v := range extra {
		cx[k] = v
	}
	raw, _ := json.Marshal(map[string]any{"messages": rg.msgs, "context": cx})
	r := httptest.NewRequest(http.MethodPost, "/api/cosre/v2/chat", bytes.NewReader(raw))
	r = r.WithContext(auth.ContextWithClaims(context.Background(), &auth.Claims{UserID: "u-state", Email: "u-state@example.test", Role: "viewer"}))
	w := serveV2(rg.s, r)
	if w.Code != http.StatusOK {
		rg.t.Fatalf("%q → %d %s", q, w.Code, w.Body.String())
	}
	body := w.Body.String()
	ans := v2AnswerText(body)
	rg.msgs = append(rg.msgs, map[string]any{"role": "assistant", "text": ans})
	return body
}

// v2AnswerText — akıştaki son tam metin parçası.
func v2AnswerText(sse string) string {
	var b strings.Builder
	cur := ""
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p) != nil {
			continue
		}
		switch p["type"] {
		case "text-start":
			b.Reset()
			cur, _ = p["id"].(string)
		case "text-delta":
			if p["id"] == cur {
				d, _ := p["delta"].(string)
				b.WriteString(d)
			}
		}
	}
	return b.String()
}

func (rg *stateRig) state() ChatContext {
	rg.t.Helper()
	var c ChatContext
	raw, ok := rg.mc.m[chatContextKey("u-state", stateConv)]
	if !ok {
		rg.t.Fatal("Redis'te durum yok")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		rg.t.Fatal(err)
	}
	return c
}

func (rg *stateRig) lastGuided() (guidedCall, bool) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	if len(rg.guided) == 0 {
		return guidedCall{}, false
	}
	return rg.guided[len(rg.guided)-1], true
}

// v2StepLabels — akıştaki data-step etiketleri.
func v2StepLabels(sse string) []string {
	var out []string
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data: {") {
			continue
		}
		var p struct {
			Type string `json:"type"`
			Data struct {
				Label string `json:"label"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p) == nil && p.Type == "data-step" && p.Data.Label != "" {
			out = append(out, p.Data.Label)
		}
	}
	return out
}

func hasLabel(labels []string, sub string) bool {
	for _, l := range labels {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func anchorChip(ms int64) string {
	return "pencere: " + time.UnixMilli(ms).UTC().Format("2006-01-02 15:04 UTC") + "'de bitiyor"
}

// referansLoop — kabul diyaloğunun sahte modeli: her soru için önce bir
// araç, sonra cevap. Model servis ve pencereyi argümana YAZMAZ: devralma
// sunucunun işidir.
func referansLoop(last string, n int) map[string]any {
	if n > 0 {
		switch {
		case strings.Contains(last, "sabah"):
			return answerMsg("checkout bu sabah sakin görünüyor: hata oranı düşük, p99 olağan.")
		case strings.Contains(last, "loglarına"):
			return answerMsg("checkout loglarında bu sabah belirgin bir hata yok.")
		default:
			return answerMsg("Dün aynı dilimde de olağan dışı bir şey yok.")
		}
	}
	switch {
	case strings.Contains(last, "sabah"):
		return toolCallMsg([2]string{"get_service_health", `{"service":"checkout"}`})
	case strings.Contains(last, "loglarına"):
		return toolCallMsg([2]string{"query_logs", `{"severity":"error"}`})
	default:
		return toolCallMsg([2]string{"get_service_health", `{}`})
	}
}

func referansGuided(r guidedRoute) string {
	if r.Intent == guidedDeployImpact {
		return "Hayır, bu sabah checkout'ta deploy görünmüyor.\n\nİstersen checkout loglarına bakayım."
	}
	return "checkout loglarında bu sabah belirgin bir hata yok."
}

// TestChatStateReferenceDialogue — kabul: dört tur.
func TestChatStateReferenceDialogue(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)

	// 1. durum sorusu: servis + sabah penceresi duruma yazılır.
	body := rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	st := rg.state()
	if st.Service != "checkout" || !strings.HasPrefix(st.WinLabel, "bu sabah") || st.RangeS <= 0 || !st.RangeExplicit {
		t.Fatalf("tur 1 durumu: %+v", st)
	}
	calls := rg.tools.snapshot()
	if len(calls) != 1 || calls[0].args["service"] != "checkout" || int64(calls[0].args["range_s"].(float64)) != st.RangeS {
		t.Fatalf("tur 1 araç argümanı pencereyi taşımıyor: %+v", calls)
	}
	if !hasLabel(v2StepLabels(body), "pencere: bu sabah") {
		t.Fatalf("pencere çipi yok: %v", v2StepLabels(body))
	}
	win1, to1 := st.RangeS, st.WinToMs

	// 2. "deploy mu vardı": deploy okuması AYNI servis ve pencereyle.
	body = rg.ask("deploy mu vardı", nil)
	g, ok := rg.lastGuided()
	if !ok || g.route.Intent != guidedDeployImpact {
		t.Fatalf("tur 2 deploy rotasına gitmedi: %+v", g)
	}
	if g.route.Service != "checkout" || g.rangeS != snapRangeS(win1) || g.anchor.UnixMilli() != to1 {
		t.Fatalf("tur 2 devralma: servis=%q rangeS=%d anchor=%v (beklenen checkout %d %v)", g.route.Service, g.rangeS, g.anchor, snapRangeS(win1), time.UnixMilli(to1))
	}
	if !hasLabel(v2StepLabels(body), "bağlam (önceki turdan): checkout") {
		t.Fatalf("devralınan alan söylenmedi: %v", v2StepLabels(body))
	}
	if st = rg.state(); st.Offer == nil || st.Offer.Kind != "logs" || st.Offer.Service != "checkout" {
		t.Fatalf("öneri durumda yok: %+v", st.Offer)
	}

	// 3. "bak": önerilen adım (loglar) aynı servis ve pencereyle yürür.
	before := len(rg.tools.snapshot())
	body = rg.ask("bak", nil)
	calls = rg.tools.snapshot()[before:]
	if len(calls) != 1 || calls[0].name != "query_logs" || calls[0].args["service"] != "checkout" || calls[0].args["window"] != "6h" {
		t.Fatalf("tur 3 öneri yürütülmedi / kapsam kaydı: %+v", calls)
	}
	if !hasLabel(v2StepLabels(body), anchorChip(to1)) {
		t.Fatalf("tur 3 sabah penceresine çapalanmadı: %v", v2StepLabels(body))
	}
	if q := lastUserContent(rg.llm.calls()[len(rg.llm.calls())-1]); q != "checkout loglarına bak" {
		t.Fatalf("modele giden öneri metni %q", q)
	}
	if !hasLabel(v2StepLabels(body), "öneri yürütülüyor: checkout logları") {
		t.Fatalf("öneri çipi yok: %v", v2StepLabels(body))
	}
	if st = rg.state(); st.Offer != nil {
		t.Fatalf("kullanılan öneri durumda kaldı: %+v", st.Offer)
	}

	// 4. "peki dün?": yalnız pencere değişir (dün aynı dilim), servis aynı.
	before = len(rg.tools.snapshot())
	body = rg.ask("peki dün?", nil)
	st = rg.state()
	if st.Service != "checkout" || !strings.HasPrefix(st.WinLabel, "dün sabah") || st.RangeS != win1 || st.WinToMs != to1-86400000 {
		t.Fatalf("tur 4 durumu: %+v", st)
	}
	calls = rg.tools.snapshot()[before:]
	if len(calls) != 1 || calls[0].args["service"] != "checkout" || int64(calls[0].args["range_s"].(float64)) != win1 {
		t.Fatalf("tur 4 araç argümanı: %+v", calls)
	}
	if !hasLabel(v2StepLabels(body), anchorChip(st.WinToMs)) {
		t.Fatalf("araçlar dünün penceresine çapalanmadı: %v", v2StepLabels(body))
	}
	if !hasLabel(v2StepLabels(body), "niyet eşleşmedi — konuşmanın öznesiyle araç döngüsüne") {
		t.Fatalf("karar 14 çipi yok: %v", v2StepLabels(body))
	}
	sys := llmSystem(rg.llm.calls()[len(rg.llm.calls())-1])
	if !strings.Contains(sys, "KONUŞMA DURUMU") || !strings.Contains(sys, "dün sabah") {
		t.Fatalf("durum bloğu istemde yok:\n%s", sys)
	}
}

// ── yanlış devralma muhafızları ───────────────────────────────────────────

func TestChatStateNewExplicitServiceReplaces(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)
	rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	rg.ask("payments için deploy var mı", nil)
	g, ok := rg.lastGuided()
	if !ok || g.route.Service != "payments" {
		t.Fatalf("açık yeni servis eskisinin yerine geçmedi: %+v", g.route)
	}
	if st := rg.state(); st.Service != "payments" || !strings.HasPrefix(st.WinLabel, "bu sabah") {
		t.Fatalf("durum: servis değişmeli, pencere kalmalı: %+v", st)
	}
}

func TestChatStateUnrelatedQuestionDropsOffer(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)
	rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	rg.ask("deploy mu vardı", nil)
	if rg.state().Offer == nil {
		t.Fatal("kurgu: öneri yazılmadı")
	}
	rg.ask("tüm servislerde en yavaş endpoint hangisi, son bir saatte filo geneline bakarak söyler misin", nil)
	if st := rg.state(); st.Offer != nil {
		t.Fatalf("ilgisiz soru eski öneriyi taşıdı: %+v", st.Offer)
	}
	before := len(rg.tools.snapshot())
	body := rg.ask("bak", nil)
	if hasLabel(v2StepLabels(body), "öneri yürütülüyor") {
		t.Fatal("bayat öneri yürütüldü")
	}
	for _, c := range rg.tools.snapshot()[before:] {
		if c.name == "query_logs" {
			t.Fatalf("bayat öneri log aracını çağırttı: %+v", c)
		}
	}
}

func TestChatStateOlderThanTTLIgnored(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)
	old := ChatContext{Service: "checkout", RangeS: 21600, RangeExplicit: true, WinLabel: "bu sabah (06:00–12:00)",
		WinToMs: rg.now.Add(-4 * time.Hour).UnixMilli(), Offer: &chatOffer{Kind: "logs", Service: "checkout", At: rg.now.Add(-3 * time.Hour).UnixMilli()},
		UpdatedAt: rg.now.Add(-chatStateTTL - time.Minute).UnixNano()}
	raw, _ := json.Marshal(old)
	rg.mc.m[chatContextKey("u-state", stateConv)] = raw
	rg.msgs = []map[string]any{{"role": "user", "text": "checkout'ta sabah bir şey mi oldu"}, {"role": "assistant", "text": "sakin"}}
	body := rg.ask("deploy mu vardı", nil)
	if hasLabel(v2StepLabels(body), "bağlam (önceki turdan)") {
		t.Fatalf("TTL dışı durum devralındı: %v", v2StepLabels(body))
	}
	if g, ok := rg.lastGuided(); !ok || g.route.Intent != guidedDeployImpact || g.route.Service == "checkout" || g.rangeS == 21600 {
		t.Fatalf("TTL dışı durum rotaya indi (ya da rota yok): %+v rangeS=%d", g.route, g.rangeS)
	}
}

// TestChatStateSwitchOffIsPreSlice — anahtar kapalı: v2 bu dilimden önceki
// hâli — yeni alan yazılmaz, çip yok, araç argümanına durum enjeksiyonu yok,
// istemde durum bloğu yok.
func TestChatStateSwitchOffIsPreSlice(t *testing.T) {
	setConversationState(t, false)
	rg := newStateRig(t, referansLoop, referansGuided)
	body := rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	body += rg.ask("deploy mu vardı", nil)
	for _, l := range v2StepLabels(body) {
		if strings.HasPrefix(l, "pencere: bu") || strings.HasPrefix(l, "bağlam (önceki turdan)") || strings.HasPrefix(l, "öneri") || strings.HasPrefix(l, "niyet eşleşmedi") {
			t.Fatalf("kapalıyken durum çipi: %q", l)
		}
	}
	if raw, ok := rg.mc.m[chatContextKey("u-state", stateConv)]; ok {
		for _, k := range []string{`"winLabel"`, `"winToMs"`, `"offer"`, `"address"`, `"ledger"`, `"env"`} {
			if strings.Contains(string(raw), k) {
				t.Fatalf("kapalıyken yeni alan yazıldı %s: %s", k, raw)
			}
		}
	}
	for _, c := range rg.llm.calls() {
		if strings.Contains(llmSystem(c), "KONUŞMA DURUMU") || strings.Contains(lastUserContent(c), "konuşma bağlamı:") {
			t.Fatal("kapalıyken istemde durum bloğu")
		}
	}
	if g, ok := rg.lastGuided(); !ok || g.route.Intent != guidedDeployImpact || g.route.Service != "" {
		t.Fatalf("kapalıyken servis devralındı (ya da rota yok): %+v", g.route)
	}
}

// TestChatStateNoRedisClientEcho — karar 2: Redis yokken istemcinin yankısı
// doğrulanıp kullanılır; katalogda olmayan servis düşer.
func TestChatStateNoRedisClientEcho(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)
	noop, _ := cache.NewNoop()
	cat := rg.mc.m
	rg.s.cache = &catalogOnlyCache{Cache: noop, m: cat}
	prevR := chatStateRedisless
	chatStateRedisless = func(s *Server) bool { return cache.IsNoop(s.cache.(*catalogOnlyCache).Cache) }
	t.Cleanup(func() { chatStateRedisless = prevR })
	now := rg.now
	echo := map[string]any{"service": "checkout", "window": map[string]any{"rangeS": 3600, "toMs": now.Add(-time.Hour).UnixMilli(), "label": "dün aynı saatte", "explicit": true},
		"offer": map[string]any{"kind": "logs", "at": now.UnixMilli()}, "updatedAt": now.UnixMilli()}
	rg.msgs = []map[string]any{{"role": "user", "text": "checkout'ta sabah bir şey mi oldu"}, {"role": "assistant", "text": "sakin. İstersen loglara bakayım."}}
	before := len(rg.tools.snapshot())
	body := rg.ask("bak", map[string]any{"state": echo})
	calls := rg.tools.snapshot()[before:]
	if len(calls) != 1 || calls[0].name != "query_logs" || calls[0].args["service"] != "checkout" || calls[0].args["window"] != "1h" {
		t.Fatalf("yankı devralınmadı: %+v / %v\n%s", calls, v2StepLabels(body), body)
	}
	echo["service"] = "uydurma-servis"
	rg.ask("deploy mu vardı", map[string]any{"state": echo})
	if g, ok := rg.lastGuided(); ok && g.route.Service == "uydurma-servis" {
		t.Fatal("katalog dışı servis yankıdan alındı")
	}
}

// catalogOnlyCache — Redis'siz kurulum: sohbet durumu yazılmaz/okunmaz, ama
// guided katalog anahtarları (svcnames/envnames) okunur.
type catalogOnlyCache struct {
	cache.Cache
	m map[string][]byte
}

func (c *catalogOnlyCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	if strings.HasPrefix(key, "copilot:guided:") {
		b, ok := c.m[key]
		return b, ok, nil
	}
	return c.Cache.Get(ctx, key)
}

// TestChatStateV1Untouched — v1 ucu aynı diyaloğu durum alanı yazmadan,
// istemde durum bloğu olmadan koşar (bayt altınları ayrıca chat_stream_golden_test).
func TestChatStateV1Untouched(t *testing.T) {
	setConversationState(t, true)
	llm := newLoopLLM(t, func(_ int, body map[string]any) map[string]any {
		if _, ok := body["tools"]; !ok {
			return answerMsg(`{"intent":"none"}`)
		}
		return answerMsg("checkout sakin. İstersen checkout loglarına bakayım.")
	})
	s, mc := newLoopTestServer(t, llm, "u-v1")
	s.l1, s.stats = newL1Cache(64), newCacheStats()
	mc.m["copilot:guided:svcnames"] = []byte(`["checkout","orders","payments"]`)
	for _, q := range []string{"checkout'ta sabah bir şey mi oldu", "bak"} {
		postLoopChat(t, s, context.Background(), "u-v1", map[string]any{
			"messages": []map[string]any{{"role": "user", "text": "merhaba"}, {"role": "assistant", "text": "selam"}, {"role": "user", "text": q}},
			"context":  map[string]any{"conversation": "conv-v1"}})
	}
	raw := string(mc.m[chatContextKey("u-v1", "conv-v1")])
	for _, k := range []string{`"winLabel"`, `"offer"`, `"address"`, `"ledger"`} {
		if strings.Contains(raw, k) {
			t.Fatalf("v1 yeni alan yazdı %s: %s", k, raw)
		}
	}
	for _, c := range llm.calls() {
		if strings.Contains(llmSystem(c), "KONUŞMA DURUMU") {
			t.Fatal("v1 isteminde durum bloğu")
		}
	}
}

// v2StateParts — akıştaki data-state yükleri.
func v2StateParts(t *testing.T, sse string) []chatStateWire {
	t.Helper()
	var out []chatStateWire
	for _, line := range strings.Split(sse, "\n") {
		if !strings.HasPrefix(line, "data: {") || !strings.Contains(line, `"type":"data-state"`) {
			continue
		}
		var p struct {
			ID   string        `json:"id"`
			Data chatStateWire `json:"data"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &p); err != nil || p.ID != "state" {
			t.Fatalf("data-state biçimi: %s", line)
		}
		out = append(out, p.Data)
	}
	return out
}

// TestChatStateDataPartAndPatch — v0.10.2325: data-state turun çözülmüş
// kapsamını taşır (defter yok); context.statePatch doğrulanıp uygulanır,
// geçersiz yama bütünüyle reddedilir; anahtar kapalıyken parça yok.
func TestChatStateDataPartAndPatch(t *testing.T) {
	setConversationState(t, true)
	rg := newStateRig(t, referansLoop, referansGuided)
	body := rg.ask("checkout'ta sabah bir şey mi oldu", nil)
	parts := v2StateParts(t, body)
	if len(parts) != 1 || parts[0].Service != "checkout" || parts[0].Window == nil || !strings.HasPrefix(parts[0].Window.Label, "bu sabah") || parts[0].UpdatedAt == 0 {
		t.Fatalf("data-state: %+v", parts)
	}
	if strings.Contains(body, `"ledger"`) || strings.Contains(body, "p99_ms") {
		t.Fatal("data-state defter değeri taşımamalı")
	}
	body = rg.ask("deploy mu vardı", map[string]any{"statePatch": map[string]any{"service": "payments"}})
	if g, _ := rg.lastGuided(); g.route.Service != "payments" {
		t.Fatalf("yama servisi uygulanmadı: %+v", g.route)
	}
	if p := v2StateParts(t, body); len(p) != 1 || p[0].Service != "payments" || p[0].Offer == nil {
		t.Fatalf("yamalı data-state: %+v", p)
	}
	rg.ask("deploy mu vardı", map[string]any{"statePatch": map[string]any{"service": "uydurma", "env": "prod"}})
	if st := rg.state(); st.Service != "payments" || st.Env != "" {
		t.Fatalf("geçersiz yama kısmen uygulandı: %+v", st)
	}

	setConversationState(t, false)
	if p := v2StateParts(t, rg.ask("checkout'ta sabah bir şey mi oldu", nil)); len(p) != 0 {
		t.Fatalf("anahtar kapalıyken data-state: %+v", p)
	}
}
