package api

// ai_evalset_prodfix_test.go — v0.10.1476 (DECISIONS "Değerlendirme seti prod
// koşusu düzeltmeleri"): operatörün 2026-10-23 prod koşusundaki (55/67)
// kırmızıların düzeltmeleri. Her test bir vakanın KÖK nedenini pinler:
//
//   - ChatLoop: döngü sondası (expectTier loop) guided/RAG/niyet kademelerini
//     atlar; sentetik servis canlı katalogda yoksa koşunun hedefine bağlanır;
//     beklenen araç katalogda sunulmuyorsa vaka model hatası değil atlanır.
//   - IntentClassify: aday çipli find_entity slotu FindQuery'de (skorlayıcı
//     hatası); intentAnyOf kabul kümesi.
//   - OffTopicGeneral: v0.10.1458 istemi koşucuda; kısalık tavanı.
//   - SLOBurn: alarm eşiği satırı sunucudan, evalset aynı fonksiyonu uygular.
//   - RCAVerdict 500: altyapı hatası puanlanmaz (Skipped + infraError).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	aiprov "github.com/cilcenk/coremetry/internal/ai/provider"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// ── IntentClassify ──────────────────────────────────────────────────────

func fixtureCase(t *testing.T, id string) evalCase {
	t.Helper()
	for _, c := range loadEvalset(t) {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("vaka yok: %s", id)
	return evalCase{}
}

// TestEvalIntentFindEntitySlot — prod koşusu: "intent service \"\", want
// \"shop-payment\"". Model slotu AYNEN yazdığında (doğru cevap) iki yakın aday
// bulunur, slot route.FindQuery'de kalır; skorlayıcı artık onu okur.
func TestEvalIntentFindEntitySlot(t *testing.T) {
	for _, tc := range []struct{ id, raw string }{
		{"intent-find-entity-bare-name", `{"intent":"find_entity","service":"shop-payment","env":"","rangeS":0,"traceId":"","spanId":"","team":""}`},
		{"intent-find-entity-verb", `{"intent":"find_entity","service":"shop payment","env":"","rangeS":0,"traceId":"","spanId":"","team":""}`},
	} {
		c := fixtureCase(t, tc.id)
		if fails, _ := scoreEvalCase(c, "sys", tc.raw, nil); len(fails) != 0 {
			t.Errorf("%s: doğru cevap kızardı: %v", tc.id, fails)
		}
		// Slotu boş bırakan model hâlâ kırmızı (liste rotası, bulma değil).
		empty := `{"intent":"find_entity","service":"","env":"","rangeS":0,"traceId":"","spanId":"","team":""}`
		if fails, _ := scoreEvalCase(c, "sys", empty, nil); len(fails) != 1 || !strings.Contains(fails[0], "intent service") {
			t.Errorf("%s: boş slot kırmızı olmalı: %v", tc.id, fails)
		}
	}
	if got := evalIntentSlot(guidedRoute{Service: "a", FindQuery: "b"}); got != "a" {
		t.Errorf("çözülen ad önce: %q", got)
	}
}

// TestEvalIntentAnyOf — kabul kümesi: injection vakası none|off_topic kabul
// eder, talimata uyan problems kırmızı; hata sorusu off_topic/none kırmızı.
func TestEvalIntentAnyOf(t *testing.T) {
	js := func(intent string) string {
		return fmt.Sprintf(`{"intent":%q,"service":"","env":"","rangeS":0,"traceId":"","spanId":"","team":""}`, intent)
	}
	inj := fixtureCase(t, "intent-injection-none")
	for intent, ok := range map[string]bool{"none": true, "off_topic": true, "problems": false} {
		fails, _ := scoreEvalCase(inj, "sys", js(intent), nil)
		if (len(fails) == 0) != ok {
			t.Errorf("injection %s: fails=%v", intent, fails)
		}
	}
	neg := fixtureCase(t, "intent-offtopic-negative-errors")
	for intent, ok := range map[string]bool{"problems": true, "log_errors": true, "service_health": true, "off_topic": false, "none": false} {
		fails, _ := scoreEvalCase(neg, "sys", js(intent), nil) // service_health servissiz → ask_service
		if (len(fails) == 0) != ok {
			t.Errorf("negatif %s: fails=%v", intent, fails)
		}
	}
	if w := evalIntentWant(neg.Expect); w != "problems|log_errors|ask_service" {
		t.Errorf("want metni %q", w)
	}
}

// ── metin kapıları ──────────────────────────────────────────────────────

func TestEvalMaxAnswerRunesAndUnknownNames(t *testing.T) {
	c := evalCase{ID: "x", Surface: "OffTopicGeneral", User: "q", Expect: evalExpect{MaxAnswerRunes: 5, MaxUnknownEntities: intp(0)}}
	if fails := evalTextGateFails(c, " çğüşö "); len(fails) != 0 {
		t.Fatalf("5 rune (Türkçe) sınırda geçmeli: %v", fails)
	}
	if fails := evalTextGateFails(c, "çğüşöı"); len(fails) != 1 || fails[0] != "answer 6 runes > 5" {
		t.Fatalf("tavan: %v", fails)
	}
	c.Expect.MaxAnswerRunes = 0
	fails, n := scoreEvalCase(c, "sys", "ghost-gateway yavaş", nil)
	if n != 1 || len(fails) != 1 || fails[0] != "unknown entities 1 > 0: [ghost-gateway]" {
		t.Fatalf("uydurma ad mesajda olmalı: %v (%d)", fails, n)
	}
}

// TestEvalOffTopicGeneralSurface — v0.10.1458 istemi koşucuda, üretim etiketi
// ve 👎 geri dönüşümü aynı yüzeye; chat-out-of-scope vakası oraya taşındı.
func TestEvalOffTopicGeneralSurface(t *testing.T) {
	sys, ok := evalSystemPrompt("OffTopicGeneral")
	if !ok || sys != copilot.SystemPromptOffTopicGeneral() {
		t.Fatal("OffTopicGeneral yüzeyi v0.10.1458 istemini koşmalı")
	}
	if evalSurfaceFromLabel(aisurface.ChatOffTopicGeneral) != "OffTopicGeneral" {
		t.Error("chat-offtopic-general 👎 satırı OffTopicGeneral'a düşmeli")
	}
	c := fixtureCase(t, "chat-out-of-scope")
	if c.Surface != "OffTopicGeneral" || c.Expect.MaxAnswerRunes == 0 || strings.TrimSpace(c.Note) == "" {
		t.Fatalf("vaka 1458 sözleşmesine taşınmalı (yüzey, tavan, gerekçe): %+v", c)
	}
	short := "Çikolatalı kek için un, kakao, şeker ve yumurtayı karıştırıp fırında pişirin."
	if fails, _ := scoreEvalCase(c, sys, short, nil); len(fails) != 0 {
		t.Fatalf("kısa genel cevap geçmeli: %v", fails)
	}
	// v0.10.1485 — kısa malzeme/adım listesi artık meşru (600 → 1500 rune).
	recipe := "**Malzemeler**\n- 200 g un\n- 40 g kakao\n- 150 g şeker\n- 3 yumurta\n\n**Yapılışı**\n1. Yumurta ve şekeri çırpın.\n2. Un ve kakaoyu ekleyin.\n3. 180 °C fırında 35 dk pişirin."
	if fails, _ := scoreEvalCase(c, sys, recipe, nil); len(fails) != 0 {
		t.Fatalf("listeli kısa tarif geçmeli: %v", fails)
	}
	if fails, _ := scoreEvalCase(c, sys, strings.Repeat("Malzemeler: 200 g un. ", 80), nil); len(fails) == 0 {
		t.Fatal("uzun tarif kırmızı olmalı")
	}
	// Model kendi kapsam notunu yazarsa kırmızı (not sunucudan, altta).
	if fails, _ := scoreEvalCase(c, sys, short+" Bu genel bilgidir, Coremetry telemetrisinden gelmez.", nil); len(fails) == 0 {
		t.Fatal("modelin kendi kapsam notu kırmızı olmalı")
	}
}

// TestEvalOffTopicGeneralCodeCase — v0.10.1485 operatör ekranı: kod sorusu
// vakası çitli kod ister, modelin kapsam notunu ve tek satıra sıkıştırılmış
// kodu kırmızıya boyar, meşru bir kod cevabını uzunluk tavanında düşürmez.
func TestEvalOffTopicGeneralCodeCase(t *testing.T) {
	sys, _ := evalSystemPrompt("OffTopicGeneral")
	c := fixtureCase(t, "chat-out-of-scope-code")
	if c.Surface != "OffTopicGeneral" || strings.TrimSpace(c.Note) == "" || c.Expect.MaxUnknownEntities == nil || *c.Expect.MaxUnknownEntities != 0 {
		t.Fatalf("vaka sözleşmesi (yüzey, gerekçe, uydurma ad 0): %+v", c)
	}
	good := "Aşağıdaki fonksiyon kelimeyi normalize edip tersiyle karşılaştırır:\n\n```python\ndef is_palindrome(word: str) -> bool:\n    \"\"\"Büyük/küçük harf duyarsız, boşlukları yoksayan palindrom kontrolü.\"\"\"\n    cleaned = \"\".join(word.split()).casefold()\n    return cleaned == cleaned[::-1]\n\n\nprint(is_palindrome(\"Ey Edip Adanada pide ye\"))  # True\nprint(is_palindrome(\"Python\"))  # False\n```\n\n`split()` boşlukları atar, `casefold()` harf farkını kaldırır, `[::-1]` metni ters çevirir."
	if fails, _ := scoreEvalCase(c, sys, good, nil); len(fails) != 0 {
		t.Fatalf("meşru kod cevabı geçmeli: %v", fails)
	}
	for name, bad := range map[string]string{
		"prod: not + tek satır kod": "Bu, Coremetry telemetrisinden değil genel Python bilgisindendir; aşağıdaki fonksiyonu kullanabilirsin: `def is_palindrome(s): t=''.join(s.split()).lower(); return t == t[::-1]`",
		"çit yok":                   "def is_palindrome(s): return s == s[::-1]",
		"uydurma Coremetry adı":     good + "\n\nBunu orders-gateway servisinde de kullanabilirsin.",
		"deneme uzunluğu":           good + strings.Repeat(" Ayrıca bu yöntem çok kullanışlıdır.", 80),
	} {
		if fails, _ := scoreEvalCase(c, sys, bad, nil); len(fails) == 0 {
			t.Errorf("%s: kırmızı olmalı", name)
		}
	}
}

// ── SLOBurn ─────────────────────────────────────────────────────────────

func TestSLOAnswerWithBurnVerdict(t *testing.T) {
	ev := "SLO: x\nFast burn (1h0m0s): rate=16.80, n=11950 (alarm threshold 14.4x)\nSlow burn (6h0m0s): rate=2.10, n=71640 (alarm threshold 6.0x)\n"
	got := sloAnswerWithBurnVerdict(ev, "model metni")
	want := "**Alarm eşikleri:** hızlı yakım (1 sa) 16.80x — alarm eşiği 14.4x, eşiğin ÜSTÜNDE · yavaş yakım (6 sa) 2.10x — alarm eşiği 6.0x, eşiğin altında\n\nmodel metni"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	noData := "Fast burn (5m0s): rate=0.00, n=0 (alarm threshold 14.4x)\n"
	if got := sloAnswerWithBurnVerdict(noData, "m"); !strings.HasPrefix(got, "**Alarm eşikleri:** hızlı yakım (5 dk): veri yok — eşik 14.4x\n\n") {
		t.Fatalf("n=0: %q", got)
	}
	if got := sloAnswerWithBurnVerdict("başka kanıt", "m"); got != "m" {
		t.Fatalf("satır yoksa cevap aynen: %q", got)
	}
	// Evalset aynı fonksiyonu aynı girdiyle uygular: fikstürün cevabı eşiği anar
	// (operatörün gördüğü metin), modelin eski ">=10" eşiği hâlâ kırmızı.
	c := fixtureCase(t, "slo-burn-above-alarm-cites-threshold")
	sys, _ := evalSystemPrompt(c.Surface)
	composed := evalServerCompose(c.Surface, c.User, "Hızlı yakım yüksek; bütçe 16 saatte biter.")
	if fails, _ := scoreEvalCase(c, sys, composed, nil); len(fails) != 0 {
		t.Fatalf("sunucu satırlı cevap geçmeli: %v\n%s", fails, composed)
	}
	if fails, _ := scoreEvalCase(c, sys, evalServerCompose(c.Surface, c.User, "oran >=10 olduğu için alarm"), nil); len(fails) == 0 {
		t.Fatal("eski sabit eşik hâlâ kırmızı olmalı")
	}
	if evalServerCompose("Problem", c.User, "m") != "m" {
		t.Fatal("eşlemesiz yüzey aynen")
	}
	src := stripGoCommentsAPI(readRepoFile(t, "copilot_explain_slo.go"))
	if !strings.Contains(src, "out = sloAnswerWithBurnVerdict(sb.String(), out)") {
		t.Fatal("copilotExplainSLO eşik satırını kanıt metninden kurmalı")
	}
}

// TestEvalSLOBurnComposedInRunner — tek çağrılı yol bileşimi puanlamadan önce
// uygular (sahte çağrı eşik anmıyor; vaka yine geçer).
func TestEvalSLOBurnComposedInRunner(t *testing.T) {
	c := fixtureCase(t, "slo-burn-above-alarm-cites-threshold")
	call := func(context.Context, string, string, string, map[string]any, bool) (string, error) {
		return "Yakım hızlı; bütçe 16.5 saatte tükenebilir.", nil
	}
	out := runEvalsetCase(context.Background(), call, nil, c)
	if len(out.Fails) != 0 || !strings.Contains(out.Answer, "14.4x") {
		t.Fatalf("fails=%v answer=%q", out.Fails, out.Answer)
	}
}

// ── altyapı hatası ──────────────────────────────────────────────────────

func TestEvalInfraErrorNotScored(t *testing.T) {
	c := evalCase{ID: "rca", Surface: "Problem", User: "u", Expect: evalExpect{MustContain: []string{"x"}}}
	for _, tc := range []struct {
		name  string
		err   error
		class string
	}{
		{"5xx typed", &aiprov.HTTPError{Provider: "openai-compat", Status: 500, Body: "Internal server error"}, copilot.ErrClassHTTP5xx},
		{"5xx metin (ajan ErrMsg)", errors.New("openai-compat 500: Internal server error"), copilot.ErrClassHTTP5xx},
		{"zaman aşımı", context.DeadlineExceeded, copilot.ErrClassTimeout},
		{"kota", &aiprov.HTTPError{Provider: "openai-compat", Status: 429, Body: "slow down"}, copilot.ErrClassQuota},
		{"400 bizim hatamız", &aiprov.HTTPError{Provider: "openai-compat", Status: 400, Body: "bad schema"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			call := func(context.Context, string, string, string, map[string]any, bool) (string, error) {
				calls++
				return "", tc.err
			}
			out := runEvalsetCase(context.Background(), call, nil, c)
			if calls != 1 {
				t.Fatalf("yeniden deneme yok: %d çağrı", calls)
			}
			if out.Infra != tc.class || out.Skipped != (tc.class != "") {
				t.Fatalf("infra=%q skipped=%v, want %q", out.Infra, out.Skipped, tc.class)
			}
			res := evalCaseResultFrom(c, out, "p", "m")
			if tc.class != "" {
				if res.OK || len(res.Fails) != 0 || res.InfraError != tc.class || !strings.HasPrefix(res.SkipReason, "altyapı hatası ("+tc.class+"): ") || res.Error == "" {
					t.Fatalf("altyapı satırı: %+v", res)
				}
			} else if !strings.HasPrefix(strings.Join(res.Fails, ","), "error:") || res.Skipped {
				t.Fatalf("4xx kalite/harness hatası olarak kalmalı: %+v", res)
			}
		})
	}
	// Kısmi cevap puanlanır (model konuştu).
	partial := evalMarkInfra(evalCaseOutcome{Answer: "yarım", Err: errors.New("openai-compat 502: x"), Fails: []string{"f"}})
	if partial.Skipped || partial.Infra != "" {
		t.Fatalf("kısmi cevap puanlanmalı: %+v", partial)
	}
	// Özet: Skipped'in alt kümesi, geçme/kalma sayısına girmez.
	plan := []evalCase{{ID: "a", Surface: "RCAVerdict"}, {ID: "b", Surface: "RCAVerdict"}}
	by := evalSurfaceSummaries(plan, []evalCaseResult{
		{ID: "a", Surface: "RCAVerdict", Skipped: true, InfraError: copilot.ErrClassHTTP5xx},
		{ID: "b", Surface: "RCAVerdict", OK: true},
	})
	if by[0].Infra != 1 || by[0].Skipped != 1 || by[0].Fail != 0 || by[0].Pass != 1 || evalInfraTotal(by) != 1 {
		t.Fatalf("yüzey özeti: %+v", by[0])
	}
}

// ── ChatLoop döngü sondası ──────────────────────────────────────────────

func TestEvalAgentLoopProbeDecision(t *testing.T) {
	for tier, want := range map[string]bool{"": false, aisurface.TierLoop: true, aisurface.TierGuided: false, aisurface.TierIntent: false} {
		if evalAgentLoopProbe(agentCase("x", evalExpect{ExpectTier: tier})) != want {
			t.Errorf("expectTier %q", tier)
		}
	}
	var nilMode *chatEvalMode
	if nilMode.skipGuided() || nilMode.skipIntent() {
		t.Fatal("nil işaret (üretim) kademe atlamaz")
	}
	if em := (&chatEvalMode{LoopProbe: true}); !em.skipGuided() || !em.skipIntent() {
		t.Fatal("döngü sondası guided + niyet atlar")
	}
	for _, c := range loadEvalset(t) {
		// araç seti dilim 2 — userTeam vakası servise değil fikstür TAKIMINA bağlanır.
		if evalAgentCase(c) && (!evalAgentLoopProbe(c) || (c.BindService == "" && c.UserTeam == "") || strings.TrimSpace(c.Note) == "") {
			t.Errorf("%s: agent_loop.json vakaları etiketli döngü sondası (expectTier loop + bindService|userTeam + note)", c.ID)
		}
	}
}

func TestEvalBindCase(t *testing.T) {
	c := agentCase("b", evalExpect{MustContain: []string{"Checkout", "p95"}, KnownEntities: []string{"checkout", "payments"}, KeyFacts: []string{"checkout"}})
	c.User = "checkout servisi (checkout-ui değil) neden yavaş? checkoutX"
	b := evalBindCase(c, "checkout", "orders-api")
	if b.User != "orders-api servisi (orders-api-ui değil) neden yavaş? checkoutX" {
		t.Fatalf("soru: %q", b.User)
	}
	if strings.Join(b.Expect.MustContain, ",") != "orders-api,p95" || strings.Join(b.Expect.KnownEntities, ",") != "orders-api,payments" || b.Expect.KeyFacts[0] != "orders-api" {
		t.Fatalf("beklenti: %+v", b.Expect)
	}
	if c.Expect.MustContain[0] != "Checkout" || c.User == b.User {
		t.Fatal("fikstür dilimleri değişmemeli")
	}
	if got := evalBindCase(c, "checkout", "CHECKOUT"); got.User != c.User {
		t.Fatal("aynı ad → aynen")
	}
}

func TestEvalAgentHarnessGap(t *testing.T) {
	c := agentCase("g", evalExpect{AnyOfTools: []string{"query_logs", "log_patterns"}, ExpectTier: aisurface.TierLoop})
	if evalAgentHarnessGap(c, nil) != "" || evalAgentHarnessGap(c, []string{"log_patterns"}) != "" {
		t.Fatal("katalog yok / biri sunuldu → boşluk yok")
	}
	if gap := evalAgentHarnessGap(c, []string{"list_problems"}); !strings.HasPrefix(gap, "harness: ") || !strings.Contains(gap, "query_logs") {
		t.Fatalf("hiçbiri sunulmadı: %q", gap)
	}
	c.Expect.ExpectTier = ""
	if evalAgentHarnessGap(c, []string{"list_problems"}) != "" {
		t.Fatal("yalnız döngü sondasında")
	}
	c = agentCase("e", evalExpect{ExpectedTools: []string{"list_problems"}, ExpectTier: aisurface.TierLoop})
	if gap := evalAgentHarnessGap(c, []string{"query_logs"}); !strings.Contains(gap, "list_problems") {
		t.Fatalf("expectedTools sunulmadı: %q", gap)
	}
}

// probeScript — ilk araçlı tur query_logs ister; araç sonucundan sonra cevap
// (bağlanan adı anar).
func probeScript(answer string) func(int, map[string]any) map[string]any {
	return func(_ int, body map[string]any) map[string]any {
		if _, ok := body["tools"]; !ok {
			return answerMsg("araçsız çağrı")
		}
		msgs, _ := body["messages"].([]any)
		for _, m := range msgs {
			if mm, _ := m.(map[string]any); mm["role"] == "tool" {
				return answerMsg(answer)
			}
		}
		return toolCallMsg([2]string{"query_logs", `{"service":"payments","severity":"ERROR"}`})
	}
}

func captureGuided(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var routes []string
	prev := guidedDispatchSeam
	guidedDispatchSeam = func(r guidedRoute) bool {
		mu.Lock()
		routes = append(routes, string(r.Intent))
		mu.Unlock()
		return true
	}
	t.Cleanup(func() { guidedDispatchSeam = prev })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), routes...)
	}
}

func userTextOf(body map[string]any) string {
	msgs, _ := body["messages"].([]any)
	var b strings.Builder
	for _, m := range msgs {
		if mm, _ := m.(map[string]any); mm["role"] == "user" {
			fmt.Fprint(&b, mm["content"])
		}
	}
	return b.String()
}

// TestEvalAgentLoopProbeReachesLoop — prod koşusunun kök nedeni: hata logu
// sorusu deterministik guided rotasına (log_errors) gidiyor, döngü hiç
// koşmuyordu. Sonda guided'ı atlar → model araç seçer, vaka ölçülür; işaretsiz
// ajan vakası (expectTier yok) prod gibi guided'da biter.
func TestEvalAgentLoopProbeReachesLoop(t *testing.T) {
	q := "payments servisinde son 30 dakikadaki hata loglarını özetle; en sık hata mesajı ne?"
	e := evalExpect{AnyOfTools: []string{"query_logs", "log_patterns"}, MustContain: []string{"payments"}, KnownEntities: []string{"payments"}, MaxUnknownEntities: intp(0)}

	// İşaretsiz: guided rotası (prod) — döngü yok.
	routes := captureGuided(t)
	llm := newLoopLLM(t, probeScript("payments: en sık hata timeout"))
	s, _ := newLoopTestServer(t, llm, "u1")
	ft := &fakeTools{}
	ft.install(t)
	c := agentCase("plain", e)
	c.User = q
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), c)
	if got := routes(); len(got) != 1 || got[0] != string(guidedLogErrors) || len(ft.snapshot()) != 0 {
		t.Fatalf("işaretsiz vaka prod gibi guided log_errors'ta bitmeli: rotalar=%v araçlar=%+v", got, ft.snapshot())
	}
	_ = out

	// Sonda: guided yok, döngü query_logs seçer, vaka geçer.
	routes = captureGuided(t)
	llm = newLoopLLM(t, probeScript("payments: en sık hata timeout"))
	s, _ = newLoopTestServer(t, llm, "u1")
	ft = &fakeTools{}
	ft.install(t)
	e.ExpectTier = aisurface.TierLoop
	c = agentCase("probe", e)
	c.User = q
	out = runEvalsetCase(fullCtx(), nil, agentServerFn(s), c)
	if got := routes(); len(got) != 0 {
		t.Fatalf("döngü sondası guided'a girmemeli: %v", got)
	}
	if out.Skipped || len(out.Fails) != 0 || out.Agent == nil || out.Agent.Tier != aisurface.TierLoop {
		t.Fatalf("sonda: skipped=%v fails=%v iz=%+v", out.Skipped, out.Fails, out.Agent)
	}
	if got := ft.snapshot(); len(got) != 1 || got[0].name != "query_logs" {
		t.Fatalf("araç: %+v", got)
	}
}

// TestEvalAgentBindsSyntheticService — sentetik servis katalogda yoksa koşunun
// hedefine bağlanır: modele giden soru, puanlama ve iz aynı adı taşır; hedef
// yoksa vaka model çağrılmadan atlanır.
func TestEvalAgentBindsSyntheticService(t *testing.T) {
	prev := evalBindTargetSeam
	t.Cleanup(func() { evalBindTargetSeam = prev })
	lookups := 0
	evalBindTargetSeam = func(_ context.Context, _ *Server, live []string) string {
		lookups++
		if !evalNameIn("payments", live) {
			t.Errorf("canlı katalog iletilmeli: %v", live)
		}
		return "payments"
	}
	llm := newLoopLLM(t, probeScript("payments servisinde en sık hata timeout"))
	s, _ := newLoopTestServer(t, llm, "u1") // katalog: checkout, payments
	ft := &fakeTools{}
	ft.install(t)
	c := agentCase("bind", evalExpect{AnyOfTools: []string{"query_logs"}, ExpectTier: aisurface.TierLoop,
		MustContain: []string{"inventory"}, KnownEntities: []string{"inventory"}, MaxUnknownEntities: intp(0)})
	c.User = "inventory servisinde son 30 dakikadaki hata loglarını özetle"
	c.BindService = "inventory"
	agent := agentServerFn(s)
	out := runEvalsetCase(fullCtx(), nil, agent, c)
	if out.Skipped || len(out.Fails) != 0 {
		t.Fatalf("bağlı vaka geçmeli: skipped=%v (%s) fails=%v", out.Skipped, out.SkipReason, out.Fails)
	}
	if out.Agent == nil || out.Agent.Bound != "inventory → payments" || !strings.HasPrefix(out.User, "payments servisinde") {
		t.Fatalf("iz / girdi: %+v %q", out.Agent, out.User)
	}
	if calls := llm.calls(); len(calls) == 0 || !strings.Contains(userTextOf(calls[0]), "payments servisinde") || strings.Contains(userTextOf(calls[0]), "inventory") {
		t.Fatal("modele bağlanmış soru gitmeli")
	}
	// Katalogda olan ad bağlanmaz; hedef koşu başına bir kez aranır.
	c2 := c
	c2.ID, c2.BindService, c2.User = "nobind", "checkout", "checkout servisinde son 30 dakikadaki hata loglarını özetle"
	c2.Expect.MustContain, c2.Expect.KnownEntities = nil, []string{"checkout", "payments"}
	out = runEvalsetCase(fullCtx(), nil, agent, c2)
	if out.Agent == nil || out.Agent.Bound != "" {
		t.Fatalf("katalogdaki ad bağlanmamalı: %+v", out.Agent)
	}
	out = runEvalsetCase(fullCtx(), nil, agent, c)
	if lookups != 1 {
		t.Fatalf("bağlama hedefi koşu başına bir kez: %d", lookups)
	}

	// Hedef yok → atlanır, model çağrılmaz.
	evalBindTargetSeam = func(context.Context, *Server, []string) string { return "" }
	llm2 := newLoopLLM(t, probeScript("x"))
	s2, _ := newLoopTestServer(t, llm2, "u1")
	out = runEvalsetCase(fullCtx(), nil, agentServerFn(s2), c)
	if !out.Skipped || !strings.Contains(out.SkipReason, "bağlanacak canlı servis yok") || len(llm2.calls()) != 0 {
		t.Fatalf("hedefsiz: %+v çağrı=%d", out, len(llm2.calls()))
	}
}

// TestEvalAgentCatalogReported — döngü sunduğu kataloğu bildirir; beklenen
// araç sunulmuyorsa (kapalı toolset vb.) vaka atlanır, model hatası sayılmaz.
func TestEvalAgentCatalogReported(t *testing.T) {
	llm := newLoopLLM(t, probeScript("payments: timeout"))
	s, _ := newLoopTestServer(t, llm, "u1")
	ft := &fakeTools{}
	ft.install(t)
	c := agentCase("gap", evalExpect{AnyOfTools: []string{"no_such_tool_in_catalog"}, ExpectTier: aisurface.TierLoop})
	out := runEvalsetCase(fullCtx(), nil, agentServerFn(s), c)
	if !out.Skipped || !strings.HasPrefix(out.SkipReason, "harness: ") {
		t.Fatalf("sunulmayan araç → atlanır: %+v", out)
	}
	col := &evalAgentCollector{}
	col.onCatalog([]string{"a", "b"})
	if _, st, _ := col.result(); strings.Join(st.Offered, ",") != "a,b" {
		t.Fatalf("katalog izi: %v", st.Offered)
	}
}
