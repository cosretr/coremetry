package api

// ai_evalset_conversation_test.go — v0.10.2322 (H1): konuşma kipi.
//
// Pinlenenler: gömülü 24 vakalık küme (v0.10.2328: + iki doğrulayıcı
// yoklaması; v0.10.2332: + iki üslup vakası; kapsam, nötr adlar, bilinen okuma adları); dikişlerin üretimde ve tek turlu vakada etkisizliği; SAF
// yardımcılar (sahte seçimi, pencere, puanlama, profil açılımı); uçtan uca
// iki profilli çok turlu koşu sahte modelle (geçmiş, bellek içi bağlam, Redis'e
// yazılmaması, sayı denetimi, "tümü" koşusunun konuşma kümesini içermemesi).

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

var evalConvNeutral = map[string]bool{"checkout": true, "orders": true, "payments": true, "inventory": true}

func TestEvalConvFixtureValid(t *testing.T) {
	cases, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	// 20 akıcılık + 2 doğrulayıcı yoklaması (v0.10.2328) + 2 üslup (style.json, v0.10.2332) + 2 düzeltmeden
	// öğrenme (learning.json, v0.10.2334) + 2 cevap dikişi (seam.json, v0.10.2339). Toplam yalnız alt
	// sınırla pinlenir (paralel dilimler dosya ekleyebilir); her dosyanın vakaları kimlikle aranır.
	if len(cases) < 28 {
		t.Fatalf("konuşma kümesi en az 28 vaka olmalı, %d", len(cases))
	}
	seamIDs := map[string]bool{"conv-seam-boilerplate": false, "conv-seam-enforce": false}
	for _, c := range cases {
		if _, ok := seamIDs[c.ID]; ok {
			seamIDs[c.ID] = c.file == "seam.json"
		}
	}
	for id, ok := range seamIDs {
		if !ok {
			t.Errorf("seam.json vakası %q yok", id)
		}
	}
	known := evalFixtureToolNames(t)
	for _, n := range mcptools.V2OnlyTools(mcptools.Deps{}) {
		known[n.Name] = true
	}
	for _, in := range []guidedIntent{guidedServiceHealth, guidedProblems, guidedDeployImpact, guidedLogErrors, guidedRootCause,
		guidedWindowCompare, guidedServiceExceptions, guidedSlowTraces, guidedTopErrorOps} {
		known[string(in)] = true
	}
	classes := map[string]bool{"status": true, "followup": true, "deploy": true, "next_step": true, "error_hunt": true, "explain": true,
		"compare": true, "knowledge": true, "smalltalk": true, "no_finding": true, "correction": true}
	seen := map[string]bool{}
	gotClass := map[string]int{}
	users := map[string]bool{}
	turns, verifierTurns := 0, 0
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] || strings.Contains(c.ID, "@") {
			t.Errorf("%s: id boş, tekrar ya da '@' içeriyor", c.ID)
		}
		seen[c.ID] = true
		if strings.TrimSpace(c.Why) == "" || c.Surface != evalConversationSurface || !evalConvCase(c) {
			t.Errorf("%s: why / surface / mode", c.ID)
		}
		if len(c.Turns) < 2 || len(c.Turns) > 3 {
			t.Errorf("%s: 2–3 tur bekleniyor, %d", c.ID, len(c.Turns))
		}
		for name := range c.Stubs {
			if !known[name] {
				t.Errorf("%s: bilinmeyen sahte adı %q", c.ID, name)
			}
		}
		for i, tr := range c.Turns {
			turns++
			users[strings.TrimSpace(tr.User)] = true
			e := tr.Expect
			if strings.TrimSpace(tr.User) == "" || !classes[e.Class] {
				t.Errorf("%s/t%d: user boş ya da sınıf %q", c.ID, i+1, e.Class)
			}
			gotClass[e.Class]++
			for _, n := range append(append([]string{}, e.AnyOfCalls...), e.ForbiddenCalls...) {
				if !known[n] {
					t.Errorf("%s/t%d: bilinmeyen okuma adı %q", c.ID, i+1, n)
				}
			}
			for name := range tr.Stubs {
				if !known[name] {
					t.Errorf("%s/t%d: bilinmeyen sahte adı %q", c.ID, i+1, name)
				}
			}
			for _, n := range append([]string{e.Entity}, e.NotEntity...) {
				if n != "" && !evalConvNeutral[n] {
					t.Errorf("%s/t%d: nötr olmayan varlık %q", c.ID, i+1, n)
				}
			}
			if e.Verifier != "" && e.Verifier != evalConvVerifyPass && e.Verifier != evalConvVerifyFlag && e.Verifier != evalConvVerifyAnnotate {
				t.Errorf("%s/t%d: verifier %q", c.ID, i+1, e.Verifier)
			}
			if e.Verifier != "" {
				verifierTurns++
			}
			if _, _, cal := evalConvCalendarWindow(e.Window, time.Now(), time.UTC); e.Window != "" && !cal {
				if _, ok := evalParseLookback(e.Window); !ok {
					t.Errorf("%s/t%d: pencere %q", c.ID, i+1, e.Window)
				}
			}
			if e.Class == "status" && e.MaxSentences == 0 {
				t.Errorf("%s/t%d: durum turu maxSentences taşımalı", c.ID, i+1)
			}
			if e.NoCalls && len(e.AnyOfCalls) > 0 {
				t.Errorf("%s/t%d: noCalls ile anyOfCalls birlikte", c.ID, i+1)
			}
		}
		// Sahtelerdeki servis adları yalnız nötr küme.
		for _, svc := range evalStubServiceNames(t, c) {
			if !evalConvNeutral[svc] {
				t.Errorf("%s: sahtede nötr olmayan servis %q", c.ID, svc)
			}
		}
	}
	// Operatörün istediği kapsam.
	for _, u := range []string{"peki dün?", "ya ödeme tarafı?", "deploy mu vardı", "bak"} {
		if !users[u] {
			t.Errorf("kapsam: %q turu yok", u)
		}
	}
	hasCorrection := false
	for u := range users {
		hasCorrection = hasCorrection || strings.HasPrefix(u, "o değil,")
	}
	if !hasCorrection {
		t.Error("kapsam: 'o değil, …' düzeltme turu yok")
	}
	for cls := range classes {
		if gotClass[cls] == 0 {
			t.Errorf("kapsam: %q sınıfı yok", cls)
		}
	}
	if verifierTurns < 6 || verifierTurns > 12 { // v0.10.2328 — doğrulayıcı odaklı turlar; v0.10.2339 — + 2 (seam.json)
		t.Errorf("doğrulayıcı turu %d, 6–12 bekleniyor", verifierTurns)
	}
	if turns < 69 || turns > 100 { // v0.10.2332 — + 5 üslup turu (style.json); v0.10.2334 — + 5 düzeltme turu (learning.json); v0.10.2339 — + 4 dikiş turu (seam.json)
		t.Errorf("tur sayısı %d — profil başına ≈ 69 (iki profil ≈ 140 sohbet turu)", turns)
	}
}

// evalStubServiceNames — sahtelerdeki "service"/"name"/$byService anahtarları.
func evalStubServiceNames(t *testing.T, c evalCase) []string {
	t.Helper()
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, vv := range x {
				if k == evalStubByServiceKey {
					for svc := range vv.(map[string]any) {
						if svc != "*" {
							out = append(out, svc)
						}
					}
				}
				if s, ok := vv.(string); ok && (k == "service" || (k == "name" && x["kind"] == "service")) {
					out = append(out, s)
				}
				walk(vv)
			}
		case []any:
			for _, vv := range x {
				walk(vv)
			}
		}
	}
	all := []map[string]json.RawMessage{c.Stubs}
	for _, tr := range c.Turns {
		all = append(all, tr.Stubs)
	}
	for _, m := range all {
		for _, raw := range m {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				t.Fatal(err)
			}
			walk(v)
		}
	}
	return out
}

// TestEvalConvSeamInertInProduction — üretimde (işaret yok) ve tek turlu ajan
// vakasında konuşma dikişleri hiçbir şeye dokunmaz.
func TestEvalConvSeamInertInProduction(t *testing.T) {
	var nilEM *chatEvalMode
	single := &chatEvalMode{Surface: "evalset-ChatLoop"}
	if nilEM.singleTurn() {
		t.Error("nil işaret singleTurn true — handler kapıları üretimde değişir")
	}
	if !single.singleTurn() {
		t.Error("tek turlu ajan vakası singleTurn false — eski kapılar açıldı")
	}
	if st := nilEM.evalChatContext(); st == nil || !st.ctx.Empty() || st.user != "" {
		t.Error("nil işaretin bağlamı boş olmalı")
	}
	h := func(context.Context, json.RawMessage) (any, error) { return "gerçek", nil }
	for _, em := range []*chatEvalMode{nilEM, single} {
		byName := map[string]mcp.ToolHandler{"get_service_health": h}
		em.stubLoopHandlers(byName)
		if reflect.ValueOf(byName["get_service_health"]).Pointer() != reflect.ValueOf(h).Pointer() {
			t.Errorf("stubLoopHandlers (%v) handler'ı değiştirdi", em)
		}
	}
	for _, ctx := range []context.Context{context.Background(), withChatEvalMode(context.Background(), single)} {
		if got := evalGuidedBundleIntent(ctx, guidedServiceHealth); got != guidedServiceHealth {
			t.Errorf("guided paket etiketi değişti: %q", got)
		}
		if evalConvCatalog(ctx) != nil {
			t.Error("üretimde katalog geçersiz kılındı")
		}
	}
	// Konuşma kipinde: kademeler açık, handler'lar sahte, bağlam araçları gerçek.
	conv := &chatEvalMode{Conversation: &evalConvTurn{State: &chatContextState{}, Stubs: map[string]json.RawMessage{"get_service_health": json.RawMessage(`{"ok":1}`)}, Catalog: []string{"checkout"}}}
	if conv.singleTurn() {
		t.Error("konuşma kipi singleTurn true")
	}
	byName := map[string]mcp.ToolHandler{"get_service_health": h, "list_deploys": h, "set_context": h}
	conv.stubLoopHandlers(byName)
	if v, _ := byName["get_service_health"](context.Background(), json.RawMessage(`{}`)); string(v.(json.RawMessage)) != `{"ok":1}` {
		t.Errorf("sahte sonuç %v", v)
	}
	if v, _ := byName["list_deploys"](context.Background(), json.RawMessage(`{}`)); string(v.(json.RawMessage)) != evalConvNoStub {
		t.Errorf("sahtesiz araç %v", v)
	}
	if reflect.ValueOf(byName["set_context"]).Pointer() != reflect.ValueOf(h).Pointer() {
		t.Error("sohbet bağlamı aracı sahteye bağlandı")
	}
	cctx := withChatEvalMode(context.Background(), conv)
	if evalGuidedBundleIntent(cctx, guidedServiceHealth) != guidedEvalStub || evalGuidedBundleIntent(cctx, guidedAskService) != guidedAskService {
		t.Error("konuşma kipi guided paket etiketi")
	}
	if got := evalConvCatalog(cctx); len(got) != 1 || got[0] != "checkout" {
		t.Errorf("katalog %v", got)
	}
}

// TestEvalConvHandlerGatesUseSingleTurn — handler'daki konuşma kademesi
// kapıları yalnız singleTurn üzerinden açılır (eski `evm == nil` kalmadı;
// kalan `evm == nil`/`evm != nil` ifadeleri bilinçli tek turlu anahtarlardır).
func TestEvalConvHandlerGatesUseSingleTurn(t *testing.T) {
	src := readFileT(t, "copilot_chat.go")
	if n := strings.Count(src, "!evm.singleTurn()"); n != 7 {
		t.Errorf("!evm.singleTurn() kapısı %d, want 7 (netleştirme, uç nokta erken, doküman/varlık, rota, operasyon-neden, uç nokta kartı, kaynak sorusu)", n)
	}
	for _, want := range []string{"cst = evm.evalChatContext()", "evm.stubLoopHandlers(byName)", "if evm.singleTurn() {\n\t\t\ttools = evalAgentTools(tools)"} {
		if !strings.Contains(src, want) {
			t.Errorf("copilot_chat.go: %q yok", want)
		}
	}
}

func readFileT(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEvalStubFor(t *testing.T) {
	by := json.RawMessage(`{"$byService":{"checkout":{"v":1},"payments":{"v":2},"*":{"v":0}}}`)
	for _, tc := range []struct {
		args, want string
		ok         bool
	}{
		{`{"service":"payments"}`, `{"v":2}`, true},
		{`{"services":["checkout"]}`, `{"v":1}`, true},
		{`{"text":"checkout servisi"}`, `{"v":1}`, true},
		{`{"service":"orders"}`, `{"v":0}`, true},
		{`{}`, `{"v":0}`, true},
	} {
		got, ok := evalStubFor(by, json.RawMessage(tc.args))
		if ok != tc.ok || string(got) != tc.want {
			t.Errorf("%s → %s %v, want %s", tc.args, got, ok, tc.want)
		}
	}
	if got, ok := evalStubFor(json.RawMessage(`{"plain":true}`), nil); !ok || string(got) != `{"plain":true}` {
		t.Errorf("düz sahte %s", got)
	}
	if _, ok := evalStubFor(json.RawMessage(`{"$byService":{"checkout":1}}`), json.RawMessage(`{"service":"orders"}`)); ok {
		t.Error("eşleşmeyen servis ve '*' yok → sahte yok olmalı")
	}
	if _, ok := evalStubFor(nil, nil); ok {
		t.Error("boş sahte")
	}
}

func TestEvalConvWindowOK(t *testing.T) {
	// v0.10.2328 — takvim pencereleri koşunun sabit diliminde (İstanbul, UTC+3):
	// yerel 13:30; dün = 16 Kasım 00:00–17 Kasım 00:00 yerel = 15 Kasım 21:00Z–16 Kasım 21:00Z.
	loc := istanbul(t)
	now := time.Date(2026, 11, 17, 10, 30, 0, 0, time.UTC)
	call := func(args string) []evalConvCall { return []evalConvCall{{Name: "x", Args: json.RawMessage(args)}} }
	mid := time.Date(2026, 11, 17, 0, 0, 0, 0, loc)
	yFrom, yTo := mid.Add(-24*time.Hour).UnixMilli(), mid.UnixMilli()
	utcMid := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	ms := func(a, b time.Time) []evalConvCall {
		return call(`{"from_ms":` + itoa64(a.UnixMilli()) + `,"to_ms":` + itoa64(b.UnixMilli()) + `}`)
	}
	morningFrom, morningTo := mid.Add(6*time.Hour), mid.Add(12*time.Hour) // yerel 13:30 > 12:00 → kırpılmaz
	for _, tc := range []struct {
		want  string
		calls []evalConvCall
		state int64
		ok    bool
	}{
		{"1h", call(`{"range_s":3600}`), 0, true},
		{"1h", call(`{"window":"1h"}`), 0, true},
		{"1h", call(`{"range_s":1800}`), 0, false},
		{"15m", call(`{"window_s":900}`), 0, true},
		{"6h", nil, 21600, true},
		{"6h", call(`{"range_s":3600}`), 21600, false}, // okuma varsa okuma kazanır
		{"1h", call(`{"from_ms":` + itoa64(now.Add(-time.Hour).UnixMilli()) + `,"to_ms":` + itoa64(now.UnixMilli()) + `}`), 0, true},
		{"yesterday", call(`{"from_ms":` + itoa64(yFrom) + `,"to_ms":` + itoa64(yTo) + `}`), 0, true},
		{"yesterday", call(`{"from_iso":"2026-11-15T21:00:00Z","to_iso":"2026-11-16T21:00:00Z"}`), 0, true},
		{"yesterday", ms(utcMid.Add(-24*time.Hour), utcMid), 0, false}, // H1'in UTC günü: çözücü yerel günü kullanır
		{"yesterday", call(`{"range_s":86400}`), 0, false},
		{"yesterday", nil, 86400, false},
		{"morning", ms(morningFrom, morningTo), 0, true},
		{"morning", ms(morningFrom.Add(-24*time.Hour), morningTo.Add(-24*time.Hour)), 0, false},
		{"yesterday_morning", ms(morningFrom.Add(-24*time.Hour), morningTo.Add(-24*time.Hour)), 0, true},
		{"yesterday_morning", ms(mid.Add(-24*time.Hour), mid), 0, false}, // sabahtan sonra çıplak "dün" dünün tamamı DEĞİL
	} {
		if got := evalConvWindowOK(tc.want, tc.calls, tc.state, 0, now, loc); got != tc.ok {
			t.Errorf("%s %v state=%d → %v, want %v", tc.want, tc.calls, tc.state, got, tc.ok)
		}
	}
	// Durumun mutlak penceresi (okuma uçsuzsa): çözücünün yazdığı RangeS + WinToMs.
	if !evalConvWindowOK("yesterday", call(`{"range_s":86400}`), 86400, yTo, now, loc) {
		t.Error("durum penceresi dünü taşıyor ama kabul edilmedi")
	}
	// Yerel 06:00'dan önce: "bu sabah" dünün sabahı, ardından çıplak "dün" dünün tamamı (çözücü kuralı).
	early := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC) // yerel 04:00
	if !evalConvWindowOK("yesterday_morning", ms(mid.Add(-24*time.Hour), mid), 0, 0, early, loc) {
		t.Error("06:00 öncesi yesterday_morning dünün tamamına düşmeli")
	}
	if !evalConvWindowOK("morning", ms(mid.Add(-18*time.Hour), mid.Add(-12*time.Hour)), 0, 0, early, loc) {
		t.Error("06:00 öncesi morning dünün sabahı olmalı")
	}
}

// TestEvalConvMatchesResolver — v0.10.2328: takvim beklentisi çözücünün
// GERÇEK çıktısıyla aynı pencereyi kabul eder ("bu sabah" → "peki dün?").
func TestEvalConvMatchesResolver(t *testing.T) {
	loc := evalConvLoc()
	for _, h := range []int{4, 9, 15} { // yerel 07:00, 12:00, 18:00 (UTC+3)
		now := time.Date(2026, 11, 17, h, 0, 0, 0, time.UTC)
		svcs := []string{"checkout"}
		r1 := resolveChatState(stateResolveIn{Question: "selam, checkout bu sabah nasıl gidiyor?", Now: now, Loc: loc, Services: svcs})
		w1 := r1.Next
		if !evalConvWindowOK("morning", nil, w1.RangeS, w1.WinToMs, now, loc) {
			t.Errorf("%02d:00Z morning: çözücü %d sn → %d", h, w1.RangeS, w1.WinToMs)
		}
		r2 := resolveChatState(stateResolveIn{Question: "peki dün?", Prev: w1, Now: now, Loc: loc, Services: svcs, HasPrior: true})
		if !evalConvWindowOK("yesterday_morning", nil, r2.Next.RangeS, r2.Next.WinToMs, now, loc) {
			t.Errorf("%02d:00Z yesterday_morning: çözücü %d sn → %d (%s)", h, r2.Next.RangeS, r2.Next.WinToMs, r2.Next.WinLabel)
		}
		r3 := resolveChatState(stateResolveIn{Question: "peki dün?", Prev: ChatContext{Service: "checkout", UpdatedAt: now.UnixMilli()}, Now: now, Loc: loc, Services: svcs, HasPrior: true})
		if !evalConvWindowOK("yesterday", nil, r3.Next.RangeS, r3.Next.WinToMs, now, loc) {
			t.Errorf("%02d:00Z yesterday: çözücü %d sn → %d", h, r3.Next.RangeS, r3.Next.WinToMs)
		}
	}
}

func TestScoreEvalConvTurn(t *testing.T) {
	now := time.Now()
	two := 2
	tr := evalConvTurnResult{
		Answer: "checkout sağlıklı. p95 310 ms. Hata oranı %0,8.\n\nKaynak: service_health",
		Calls:  []evalConvCall{{Name: "service_health", Origin: "guided", Args: json.RawMessage(`{"service":"checkout"}`)}},
	}
	tr.Services, tr.Windows = evalConvResolved(tr.Calls, now)
	ev := `service_health checkout: p95 310 ms, hata oranı %0,8`
	if f := scoreEvalConvTurn(evalConvExpect{Class: "status", AnyOfCalls: []string{"service_health"}, Entity: "checkout", MaxSentences: 3, MaxCalls: &two}, tr, ev, now, time.UTC); len(f) != 0 {
		t.Errorf("temiz tur ihlal üretti: %v", f)
	}
	f := scoreEvalConvTurn(evalConvExpect{Class: "status", AnyOfCalls: []string{"list_deploys"}, Entity: "payments", NotEntity: []string{"checkout"}, MaxSentences: 2, NoCalls: true, Window: "1h"}, tr, `p95 310 ms`, now, time.UTC)
	joined := strings.Join(f, "\n")
	for _, want := range []string{"veri okunmamalıydı", "beklenen okumalardan", "varlık \"payments\"", "düzeltilen varlık", "pencere \"1h\"", "cümle 3 > 2", "sahte sonuçlarda olmayan sayı"} {
		if !strings.Contains(joined, want) {
			t.Errorf("ihlal %q yok: %v", want, f)
		}
	}
	// Bağlamdan taşınan varlık okumasız turda da sayılır.
	if f := scoreEvalConvTurn(evalConvExpect{Class: "followup", Entity: "checkout"}, evalConvTurnResult{Answer: "Evet.", StateService: "checkout"}, "", now, time.UTC); len(f) != 0 {
		t.Errorf("bağlam varlığı: %v", f)
	}
	// v0.10.2328 — doğrulayıcı beklentisi: pass uyuşmazlıkta, flag kaçırmada ihlal.
	vtr := evalConvTurnResult{Answer: "p99.9 1.900 ms.", Verify: &evalConvVerify{Checked: 1, Mismatch: 1, Mismatches: []string{"duration:1.900 ms"}}}
	if f := scoreEvalConvTurn(evalConvExpect{Class: "followup", Verifier: "pass"}, vtr, "p95 1234 ms", now, time.UTC); !strings.Contains(strings.Join(f, " "), "doğrulayıcı uyuşmazlığı") {
		t.Errorf("pass: %v", f)
	}
	if f := scoreEvalConvTurn(evalConvExpect{Class: "followup", Verifier: "flag"}, vtr, "p95 1234 ms", now, time.UTC); strings.Contains(strings.Join(f, " "), "doğrulayıcı") {
		t.Errorf("flag yakalandı ama ihlal: %v", f)
	}
	vtr.Verify = &evalConvVerify{Checked: 1, Verified: 1}
	if f := scoreEvalConvTurn(evalConvExpect{Class: "followup", Verifier: "flag"}, vtr, "p95 1234 ms", now, time.UTC); !strings.Contains(strings.Join(f, " "), "kaçırdı") {
		t.Errorf("flag kaçırma: %v", f)
	}
	vtr.Verify = nil // doğrulama koşmadı → denetlenmez
	if f := scoreEvalConvTurn(evalConvExpect{Class: "followup", Verifier: "pass"}, vtr, "p95 1234 ms", now, time.UTC); strings.Contains(strings.Join(f, " "), "doğrulayıcı") {
		t.Errorf("ölçülmeyen tur: %v", f)
	}
}

func TestEvalExpandProfiles(t *testing.T) {
	plan := []evalCase{{ID: "a"}, {ID: "b"}}
	if got := evalExpandProfiles(plan, nil); !reflect.DeepEqual(got, plan) {
		t.Error("profilsiz plan değişti")
	}
	one := evalExpandProfiles(plan, []string{"p"})
	if len(one) != 2 || one[0].ID != "a" || one[0].profile != "p" {
		t.Errorf("tek profil %+v", one)
	}
	two := evalExpandProfiles(plan, []string{"p", "q"})
	var ids []string
	for _, c := range two {
		ids = append(ids, c.ID)
	}
	if strings.Join(ids, ",") != "a@p,b@p,a@q,b@q" || two[3].profile != "q" || evalCaseBaseID("a@q") != "a" {
		t.Errorf("iki profil sırası %v", ids)
	}
	if _, err := evalNormProfiles([]string{"a", "b", "c"}); err == nil {
		t.Error("üç profil kabul edildi")
	}
	if got, _ := evalNormProfiles([]string{" a ", "a", "", "b"}); strings.Join(got, ",") != "a,b" {
		t.Errorf("norm %v", got)
	}
}

func TestEvalSentences(t *testing.T) {
	for _, tc := range []struct {
		in string
		n  int
	}{
		{"", 0},
		{"Evet", 1},
		{"checkout sağlıklı. p95 310 ms.", 2},
		{"Sürüm 2.4 yayında. Sorun yok!\n\nKaynak: deploy_impact", 2},
		{"1. bir\n2. iki\n3. üç", 3},
	} {
		if got := evalrubric.Sentences(tc.in); got != tc.n {
			t.Errorf("%q → %d, want %d", tc.in, got, tc.n)
		}
	}
}

// ── uçtan uca: iki profil, iki tur, sahte model ─────────────────────────

func TestEvalConversationRunEndToEnd(t *testing.T) {
	answers := map[string]string{
		"model-a": "checkout sağlıklı; p95 310 ms.",
		"model-b": "checkout iyi görünüyor; p95 999 ms.",
	}
	h, llm, _ := newAgentEvalHarness(t, func(_ int, body map[string]any) map[string]any {
		m, _ := body["model"].(string)
		return map[string]any{"role": "assistant", "content": answers[m]}
	}, []evalCase{{ID: "single", Surface: "Problem", Why: "w", User: "x", Expect: evalExpect{MustContain: []string{"x"}}}})
	h.s.l1, h.s.stats = newL1Cache(64), newCacheStats() // konuşma kipinde açılan v2 kademeleri önbellek okur
	cop := h.s.copilot
	cop.SetProfiles([]copilot.ModelProfile{
		{ID: "pa", Label: "A", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "model-a", LocalOnly: true},
		{ID: "pb", Label: "B", Provider: copilot.ProviderOpenAI, BaseURL: llm.srv.URL, APIKey: "k", Model: "model-b", LocalOnly: true},
	}, "pa", nil)
	mc := h.s.cache.(*ctxMemCache)
	conv := []evalCase{{ID: "conv-x", Surface: evalConversationSurface, Mode: evalModeConversation, Why: "w",
		Stubs: map[string]json.RawMessage{
			"service_health":     json.RawMessage(`{"$byService":{"checkout":"checkout: p95 310 ms, hata oranı %0,8."}}`),
			"get_service_health": json.RawMessage(`{"$byService":{"checkout":{"p95_ms":310}}}`),
		},
		Turns: []evalConvFixtureTurn{
			{User: "checkout nasıl?", Expect: evalConvExpect{Class: "status", AnyOfCalls: []string{"service_health", "get_service_health"}, Entity: "checkout", MaxSentences: 3}},
			{User: "peki dün?", Expect: evalConvExpect{Class: "followup", Entity: "checkout", Window: "yesterday"}},
		}}}
	prevConv := evalConversationLoader
	evalConversationLoader = func() ([]evalCase, error) { return conv, nil }
	t.Cleanup(func() { evalConversationLoader = prevConv })

	// "tümü" koşusu konuşma kümesini içermez.
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":[],"profiles":["pa","pb","pc"]}`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("üç profil %d", w.Code)
	}
	if w := h.do(t, http.MethodPost, "/api/ai/evalset/runs", `{"surfaces":["ChatConversation"],"profiles":["nope"]}`, auth.RoleAdmin); w.Code != http.StatusBadRequest {
		t.Fatalf("bilinmeyen profil %d", w.Code)
	}
	run, cases := runAgentEval(t, h, `{"surfaces":["ChatConversation"],"profiles":["pa","pb"]}`)
	if run.Total != 2 || len(cases) != 2 {
		t.Fatalf("koşu %d vaka, sonuç %d", run.Total, len(cases))
	}
	byID := map[string]evalCaseResult{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	a, b := byID["conv-x@pa"], byID["conv-x@pb"]
	if a.ProfileID != "pa" || b.ProfileID != "pb" || a.Model != "model-a" || b.Model != "model-b" {
		t.Fatalf("profil/model: %+v / %+v", a.ProfileID+a.Model, b.ProfileID+b.Model)
	}
	if len(a.Turns) != 2 || len(b.Turns) != 2 || a.Mode != evalModeConversation {
		t.Fatalf("tur sayısı %d/%d", len(a.Turns), len(b.Turns))
	}
	t1 := a.Turns[0]
	if t1.Answer == "" || !strings.Contains(t1.Answer, "310") {
		t.Errorf("tur 1 cevabı %q", t1.Answer)
	}
	if len(t1.Calls) == 0 || !evalNameIn("checkout", t1.Services) {
		t.Errorf("tur 1 sahte okuma yok: %+v", t1.Calls)
	}
	if t1.StateService != "checkout" {
		t.Errorf("tur 1 sonrası bellek içi bağlam servisi %q", t1.StateService)
	}
	// Tur 2'nin model çağrısında tur 1'in cevabı geçmişte.
	sawHist := false
	llm.mu.Lock()
	for _, body := range llm.bodies {
		raw, _ := json.Marshal(body["messages"])
		if strings.Contains(string(raw), "peki dün?") && strings.Contains(string(raw), "checkout sağlıklı; p95 310 ms.") {
			sawHist = true
		}
	}
	llm.mu.Unlock()
	if !sawHist {
		t.Error("tur 2 isteği tur 1 cevabını geçmiş olarak taşımıyor")
	}
	// Sayı denetimi: B profili sahtede olmayan 999'u yazdı.
	if !strings.Contains(strings.Join(b.Turns[0].Fails, " "), "999") || b.OK {
		t.Errorf("B profili sayı ihlali yok: %v", b.Turns[0].Fails)
	}
	// v0.10.2328 — gölge doğrulayıcı koşu ayarından bağımsız ölçer: A geçer, B'nin 999'u uyuşmaz.
	if v := t1.Verify; v == nil || v.Mismatch != 0 || v.Verified == 0 {
		t.Errorf("A tur 1 doğrulayıcı: %+v", v)
	}
	if v := b.Turns[0].Verify; v == nil || v.Mismatch == 0 || !strings.Contains(strings.Join(v.Mismatches, " "), "999") {
		t.Errorf("B tur 1 doğrulayıcı: %+v", v)
	}
	// v0.10.2323 — döngüye düşen turda v0.10.2318 telemetrisi okunur; rapor ucu.
	for _, tr := range a.Turns {
		if tr.Tier == "loop" && tr.LoopRounds == 0 {
			t.Errorf("döngü turunda loopRounds okunmadı: %+v", tr)
		}
	}
	h.s.registerAIEvalsetFluencyRoutes(h.mux)
	w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+run.ID+"/fluency-report", "", auth.RoleAdmin)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/markdown") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "fluency-") || !strings.Contains(w.Body.String(), "| | pa (`model-a`) | pb (`model-b`) |") {
		t.Errorf("rapor ucu %d %q:\n%s", w.Code, w.Header(), w.Body.String())
	}
	if w := h.do(t, http.MethodGet, "/api/ai/evalset/runs/"+run.ID+"/fluency-report", "", auth.RoleViewer); w.Code != http.StatusForbidden {
		t.Errorf("viewer rapor %d", w.Code)
	}
	// Redis (sahte önbellek) — koşu kullanıcının sohbet bağlamına yazmadı.
	mc.mu.Lock()
	for k := range mc.m {
		if strings.HasPrefix(k, "copilot:ctx:") {
			t.Errorf("koşu Redis bağlamına yazdı: %s", k)
		}
	}
	mc.mu.Unlock()
}
