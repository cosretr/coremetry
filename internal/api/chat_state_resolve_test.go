package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// chat_state_resolve_test.go — v0.10.2324: çözücü tablo testi (SAF).

var stLoc = time.FixedZone("TRT", 3*3600)

// 2026-11-17 Salı 14:30 yerel.
var stNow = time.Date(2026, 11, 17, 14, 30, 0, 0, stLoc)

func stAt(d, h, m int) int64 { return time.Date(2026, 11, d, h, m, 0, 0, stLoc).UnixMilli() }

func resolveQ(q string, prev ChatContext) stateResolveOut {
	return resolveChatState(stateResolveIn{Question: q, Prev: prev, Now: stNow, Loc: stLoc,
		Services: []string{"checkout", "orders", "payments"}, Envs: []string{"prod", "uat"}, HasPrior: true})
}

func TestResolveTurnWindowTable(t *testing.T) {
	morning := ChatContext{Service: "checkout", RangeS: 6 * 3600, WinToMs: stAt(17, 12, 0), WinLabel: "bu sabah (06:00–12:00)", RangeExplicit: true}
	cases := []struct {
		q          string
		prev       ChatContext
		rangeS     int64
		toMs       int64
		labelStart string
	}{
		{"checkout'ta sabah bir şey mi oldu", ChatContext{}, 6 * 3600, stAt(17, 12, 0), "bu sabah"},
		{"dün sabah?", ChatContext{}, 6 * 3600, stAt(16, 12, 0), "dün sabah"},
		{"peki dün?", morning, 6 * 3600, stAt(16, 12, 0), "dün sabah"},
		{"dün ne oldu", ChatContext{}, 86400, stAt(17, 0, 0), "dün"},
		{"bugün hata var mı", ChatContext{}, int64(14*3600 + 30*60), 0, "bugün"},
		{"geçen hafta deploy var mıydı", ChatContext{}, 7 * 86400, stAt(16, 0, 0), "geçen hafta"},
		{"bu hafta nasıl", ChatContext{}, int64(86400 + 14*3600 + 30*60), 0, "bu hafta"},
		{"dün aynı saatte nasıldı", morning, 6 * 3600, stAt(16, 12, 0), "dün aynı saatte"},
		{"dün bu saatte", ChatContext{}, 3600, stAt(16, 14, 30), "dün aynı saatte"},
		{"son 1 saatte hata", ChatContext{}, 3600, 0, "son 1 saat"},
		{"son 15 dakika", ChatContext{}, 900, 0, "son 15 dakika"},
		{"09:00-11:00 arası", ChatContext{}, 2 * 3600, stAt(17, 11, 0), "17/11 09:00"},
		{"dün 09:00-11:00 arası", ChatContext{}, 2 * 3600, stAt(16, 11, 0), "16/11 09:00"},
		{"akşam", ChatContext{}, 0, 0, ""}, // bugünün akşamı henüz başlamadı → dünün akşamı
		{"dün gece", ChatContext{}, 8 * 3600, stAt(17, 6, 0), "dün gece"},
		{"öğleden sonra yavaşladı mı", ChatContext{}, int64(2*3600 + 30*60), stAt(17, 14, 30), "bugün öğleden sonra"},
	}
	for _, c := range cases {
		out := resolveQ(c.q, c.prev)
		if c.q == "akşam" {
			if !out.WindowSet || out.Next.WinToMs != stAt(17, 0, 0) || !strings.HasPrefix(out.Next.WinLabel, "dün akşam") {
				t.Errorf("%q → %+v", c.q, out.Next)
			}
			continue
		}
		if !out.WindowSet || out.Next.RangeS != c.rangeS || out.Next.WinToMs != c.toMs || !strings.HasPrefix(out.Next.WinLabel, c.labelStart) || !out.Next.RangeExplicit {
			t.Errorf("%q → set=%v rangeS=%d toMs=%d label=%q (beklenen %d %d %q)", c.q, out.WindowSet, out.Next.RangeS, out.Next.WinToMs, out.Next.WinLabel, c.rangeS, c.toMs, c.labelStart)
		}
	}
	for _, q := range []string{"deploy mu vardı", "checkout nasıl", "önceki gün", "geçen salı"} {
		if out := resolveQ(q, ChatContext{}); out.WindowSet {
			t.Errorf("%q pencere sanıldı: %+v", q, out.Next)
		}
	}
}

func TestResolveInheritanceGuards(t *testing.T) {
	prev := ChatContext{Service: "checkout", Env: "prod", RangeS: 3600, RangeExplicit: true, Operation: "GET /cart", Trace: strings.Repeat("a", 32)}
	if out := resolveQ("deploy mu vardı", prev); !out.InheritService || !out.InheritEnv || !out.InheritWindow || out.ServiceSet {
		t.Fatalf("kısa devam devralmalı: %+v", out)
	}
	out := resolveQ("payments'ta deploy mu vardı", prev)
	if !out.ServiceSet || out.InheritService || out.Next.Service != "payments" || out.Next.Operation != "" || out.Next.Trace != "" {
		t.Fatalf("açık yeni servis eskisini ezmeli, özne düşmeli: %+v", out.Next)
	}
	if out := resolveQ("tüm servislerde deploy var mı", prev); out.InheritService {
		t.Fatal("filo sorusu servis devralmamalı")
	}
	long := "bu arada şunu da sormak istiyorum, filodaki genel durum nasıl, en çok hangi endpoint yavaşlıyor, kim ne yapıyor"
	if out := resolveQ(long, prev); out.InheritService || out.InheritWindow {
		t.Fatal("uzun ilgisiz soru devralmamalı")
	}
	in := stateResolveIn{Question: "deploy mu vardı", Prev: prev, Now: stNow, Loc: stLoc, Services: []string{"checkout"}}
	if out := resolveChatState(in); out.InheritService {
		t.Fatal("ilk tur (önceki tur yok) devralmamalı")
	}
	if out := resolveQ("uat'ta nasıl", prev); !out.EnvSet || out.Next.Env != "uat" || !out.InheritService {
		t.Fatalf("ortam değişir, servis kalır: %+v", out)
	}
}

func TestResolveOfferAndTTL(t *testing.T) {
	fresh := &chatOffer{Kind: "logs", Service: "checkout", Label: "checkout logları", At: stNow.Add(-time.Minute).UnixMilli()}
	prev := ChatContext{Service: "checkout", Offer: fresh}
	for _, q := range []string{"bak", "Evet.", "devam et", "bakar mısınız?", "tamam"} {
		if out := resolveQ(q, prev); out.Offer == nil || out.Next.Offer != nil || offerPrompt(*out.Offer) != "checkout loglarına bak" {
			t.Errorf("%q öneriyi yürütmeli: %+v", q, out.Offer)
		}
	}
	for _, q := range []string{"evet ama hataları", "orders nasıl", "bakma"} {
		if out := resolveQ(q, prev); out.Offer != nil || out.Next.Offer != nil {
			t.Errorf("%q öneriyi yürütmemeli ve silmeli: %+v", q, out)
		}
	}
	stale := ChatContext{Service: "checkout", Offer: &chatOffer{Kind: "logs", Service: "checkout", At: stNow.Add(-chatOfferTTL - time.Minute).UnixMilli()}}
	if out := resolveQ("bak", stale); out.Offer != nil {
		t.Fatal("ömrü geçmiş öneri yürüdü")
	}
	old := ChatContext{Service: "checkout", RangeS: 3600, RangeExplicit: true, Address: "siz", UpdatedAt: stNow.Add(-chatStateTTL - time.Second).UnixNano()}
	out := resolveQ("deploy mu vardı", old)
	if !out.Stale || out.InheritService || out.Next.Service != "" || out.Next.Address != "siz" {
		t.Fatalf("TTL dışı durum yok sayılmalı (hitap kalır): %+v", out)
	}
}

func TestResolveAddressCodesProblem(t *testing.T) {
	cases := []struct{ q, prev, want string }{
		{"checkout'a bakar mısınız", "", addressSiz},
		{"loglarına bak", "", addressSen},
		{"durum ne", "", addressSen},
		{"durum ne", addressSiz, addressSiz},
		{"servisiniz yavaş mı", "", addressSiz},
		{"bakar mısın", addressSiz, addressSen},
	}
	for _, c := range cases {
		if got := resolveQ(c.q, ChatContext{Address: c.prev}).Next.Address; got != c.want {
			t.Errorf("%q (önceki %q) → %q, beklenen %q", c.q, c.prev, got, c.want)
		}
	}
	out := resolveQ("kanal kodu MB2 fonksiyon 4711 için problem P-42 nedir", ChatContext{})
	if out.Next.ChannelCode != "MB2" || out.Next.FunctionCode != "4711" || out.Next.Problem != "P-42" {
		t.Fatalf("kodlar: %+v", out.Next)
	}
	if out := resolveQ("kanal kodu nedir", ChatContext{}); out.Next.ChannelCode != "" {
		t.Fatalf("sözcük kod sanıldı: %q", out.Next.ChannelCode)
	}
}

func TestDetectOffer(t *testing.T) {
	now := stNow
	cases := []struct{ answer, kind string }{
		{"Deploy yok.\n\nİstersen checkout loglarına bakayım.", "logs"},
		{"Hata oranı düşük. Sonraki adım: deploy geçmişine bakabilirim.", "deploys"},
		{"Bir şey yok.", ""},
		{"İstersen loglara ya da pod'lara bakabilirim.", ""}, // iki tür → belirsiz
	}
	for _, c := range cases {
		got := detectOffer(c.answer, "checkout", now)
		switch {
		case c.kind == "" && got != nil:
			t.Errorf("%q → %+v, öneri beklenmiyordu", c.answer, got)
		case c.kind != "" && (got == nil || got.Kind != c.kind || got.Service != "checkout"):
			t.Errorf("%q → %+v, beklenen %s", c.answer, got, c.kind)
		}
	}
}

func TestValidateStateEcho(t *testing.T) {
	now := stNow
	in := chatStateWire{Service: "CHECKOUT", Env: "nope", Window: &chatStateWireWin{RangeS: 3600, ToMs: now.Add(-time.Hour).UnixMilli(), Label: "dün"},
		Trace: "zz", Address: "biz", Offer: &chatStateWireOff{Kind: "rm -rf", At: now.UnixMilli()}, ChannelCode: "MB2", UpdatedAt: now.UnixMilli()}
	out, ok := validateStateEcho(in, []string{"checkout"}, []string{"prod"}, now)
	if !ok || out.Service != "checkout" || out.Env != "" || out.Window == nil || out.Trace != "" || out.Address != "" || out.Offer != nil || out.ChannelCode != "MB2" {
		t.Fatalf("doğrulama: %+v", out)
	}
	in.UpdatedAt = now.Add(-chatStateTTL - time.Minute).UnixMilli()
	if _, ok := validateStateEcho(in, []string{"checkout"}, nil, now); ok {
		t.Fatal("TTL dışı yankı kabul edildi")
	}
	in.UpdatedAt = 0
	if _, ok := validateStateEcho(in, []string{"checkout"}, nil, now); ok {
		t.Fatal("damgasız yankı kabul edildi")
	}
	c := mergeStateEcho(ChatContext{Ledger: []chatFact{{Tool: "x"}}}, out)
	if c.Ledger != nil {
		t.Fatal("yankı defter taşıyamaz")
	}
}

func TestApplyScreenContextStateService(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{"service": map[string]any{}, "range_s": map[string]any{}}}
	args, applied := applyScreenContext("list_deploys", json.RawMessage(`{}`), schema, toolScreenScope{StateService: "checkout"}, stNow)
	if string(args) != `{"service":"checkout"}` || applied.Service != "checkout" {
		t.Fatalf("durum servisi isteğe bağlı service alanına inmeli: %s %+v", args, applied)
	}
	args, _ = applyScreenContext("list_deploys", json.RawMessage(`{"service":"orders"}`), schema, toolScreenScope{StateService: "checkout"}, stNow)
	if string(args) != `{"service":"orders"}` {
		t.Fatalf("modelin servisi ezilmemeli: %s", args)
	}
	args, _ = applyScreenContext("list_deploys", json.RawMessage(`{}`), schema, toolScreenScope{}, stNow)
	if string(args) != `{}` {
		t.Fatalf("durum yokken enjeksiyon: %s", args)
	}
}

func TestLedgerFacts(t *testing.T) {
	got := ledgerFacts("get_service_health", "checkout", "bu sabah", `{"p99_ms":812,"error_rate":0.4,"name":"checkout","red":{"rps":12.5},"list":[1,2]}`)
	if len(got) != 3 || got[0].Name != "error_rate" || got[1].Name != "p99_ms" || got[1].Unit != "ms" || got[2].Name != "red.rps" {
		t.Fatalf("olgular: %+v", got)
	}
	line := chatStateLineTR(ChatContext{RangeS: 3600, RangeExplicit: true, WinLabel: "bu sabah", Env: "prod", Address: addressSiz, Ledger: got})
	for _, want := range []string{"KONUŞMA DURUMU", "- pencere: bu sabah", "- ortam: prod", "hitap: siz", "p99_ms=812 ms", "ÖNCEKİ TURUN OLGULARI"} {
		if !strings.Contains(line, want) {
			t.Fatalf("durum satırında %q yok:\n%s", want, line)
		}
	}
	if chatStateLineTR(ChatContext{Service: "checkout"}) != "" {
		t.Fatal("yalnız servis (önsözde zaten var) blok üretmemeli")
	}
}

func TestCosreConversationSettingsRoundTrip(t *testing.T) {
	c, _ := cache.NewNoop()
	s := &Server{cache: c, l1: newL1Cache(16), stats: newCacheStats(), auditQ: make(chan chstore.AuditEntry, 16)}
	st := &conversationFakeStore{}
	prevStore := cosreConversationStoreOf
	cosreConversationStoreOf = func(*Server) cosreConversationStore { return st }
	t.Cleanup(func() { cosreConversationStoreOf = prevStore })
	setConversationState(t, true)
	mux := http.NewServeMux()
	s.registerCosreConversationRoutes(mux)
	if w := rulesDo(mux, "GET", "/api/ai/cosre-conversation", "", auth.RoleViewer, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":true`) {
		t.Fatalf("viewer GET %d: %s", w.Code, w.Body)
	}
	if w := rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"state":false}`, auth.RoleEditor, ""); w.Code != http.StatusForbidden {
		t.Fatalf("editor PUT %d", w.Code)
	}
	for _, bad := range []string{`{}`, `{"verification":"x"}`, `{bozuk`, `{"interaction":"shadow"}`} { // v0.10.2339 — enforce artık geçerli
		if w := rulesDo(mux, "PUT", "/api/ai/cosre-conversation", bad, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
			t.Fatalf("%s → %d", bad, w.Code)
		}
	}
	if st.saves != 0 || len(s.auditQ) != 0 {
		t.Fatal("reddedilen istek yazmamalı")
	}
	w := rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"state":false}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || chstore.CurrentCosreConversation().State || st.saved.UpdatedBy != "admin@example.test" {
		t.Fatalf("admin PUT %d: %s / %+v", w.Code, w.Body, st.saved)
	}
	a := nextAudit(t, s)
	if a.Action != "settings.cosre_conversation.update" || a.TargetID != chstore.CosreConversationKey ||
		a.Details != `{"before":{"state":true,"style":false,"verification":"enforce","bannedPhrases":0,"interaction":"off","evidence":"off","statusLine":true,"traceExplainStyle":"anlatim","flow":"inline"},"after":{"state":false,"style":false,"verification":"enforce","bannedPhrases":0,"interaction":"off","evidence":"off","statusLine":true,"traceExplainStyle":"anlatim","flow":"inline"}}` {
		t.Fatalf("audit = %+v", a)
	}
	// v0.10.2327 — verification bağlı: off (kill switch) ↔ shadow; state korunur.
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"verification":"off"}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || chstore.CurrentCosreConversation().Verification != chstore.CosreVerifyOff || chstore.CurrentCosreConversation().State {
		t.Fatalf("verification off PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"after":{"state":false,"style":false,"verification":"off",`) {
		t.Fatalf("audit = %+v", a)
	}
	// v0.10.2331 — style bağlı (B): aç/kapa kabul; state ve verification korunur.
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"style":true}`, auth.RoleAdmin, "")
	if cur := chstore.CurrentCosreConversation(); w.Code != http.StatusOK || !cur.Style || cur.State || cur.Verification != chstore.CosreVerifyOff {
		t.Fatalf("style on PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"after":{"state":false,"style":true,"verification":"off",`) {
		t.Fatalf("audit = %+v", a)
	}
	// v0.10.2338 — bannedPhrases (D): tam liste yazılır, tavan aşımı 400, diğer alanlar korunur.
	many := make([]string, chstore.CosreBannedPhrasesMax+1)
	for i := range many {
		many[i] = fmt.Sprintf("ifade %d", i)
	}
	raw, _ := json.Marshal(map[string]any{"bannedPhrases": many})
	if w := rulesDo(mux, "PUT", "/api/ai/cosre-conversation", string(raw), auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("41 ifade → %d", w.Code)
	}
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"bannedPhrases":["Sorunuzu inceledim"," "]}`, auth.RoleAdmin, "")
	if cur := chstore.CurrentCosreConversation(); w.Code != http.StatusOK || len(cur.BannedPhrases) != 1 || !cur.Style || cur.Verification != chstore.CosreVerifyOff {
		t.Fatalf("bannedPhrases PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"after":{"state":false,"style":true,"verification":"off","bannedPhrases":1,"interaction":"off","evidence":"off","statusLine":true,"traceExplainStyle":"anlatim","flow":"inline"}`) {
		t.Fatalf("audit = %+v", a)
	}
	// v0.10.2339 — E2: enforce kabul edilir (v0.10.2439: varsayılan).
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"verification":"enforce"}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || chstore.CurrentCosreConversation().Verification != chstore.CosreVerifyEnforce {
		t.Fatalf("enforce PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"verification":"enforce"`) {
		t.Fatalf("audit = %+v", a)
	}
	if chstore.DefaultCosreConversation().Verification != chstore.CosreVerifyEnforce {
		t.Fatal("v0.10.2439 — varsayılan enforce")
	}
	// v0.10.2439 — geri dönüş: admin shadow'a döner, audit önce/sonra taşır.
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"verification":"shadow"}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || chstore.CurrentCosreConversation().Verification != chstore.CosreVerifyShadow {
		t.Fatalf("shadow PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"before":{"state":false,"style":true,"verification":"enforce"`) ||
		!strings.Contains(a.Details, `"after":{"state":false,"style":true,"verification":"shadow"`) {
		t.Fatalf("audit = %+v", a)
	}
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"verification":"enforce"}`, auth.RoleAdmin, "")
	if w.Code != http.StatusOK || chstore.CurrentCosreConversation().Verification != chstore.CosreVerifyEnforce {
		t.Fatalf("enforce PUT %d: %s", w.Code, w.Body)
	}
	_ = nextAudit(t, s)
	// v0.10.2353 — I5: interaction off | on; diğer alanlar korunur, varsayılan off.
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"interaction":"on"}`, auth.RoleAdmin, "")
	if cur := chstore.CurrentCosreConversation(); w.Code != http.StatusOK || cur.Interaction != chstore.CosreInteractionOn || cur.Verification != chstore.CosreVerifyEnforce {
		t.Fatalf("interaction on PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"bannedPhrases":1,"interaction":"off","evidence":"off","statusLine":true,"traceExplainStyle":"anlatim","flow":"inline"},"after":`) || !strings.HasSuffix(a.Details, `"interaction":"on","evidence":"off","statusLine":true,"traceExplainStyle":"anlatim","flow":"inline"}}`) {
		t.Fatalf("audit = %+v", a)
	}
	if chstore.DefaultCosreConversation().Interaction != chstore.CosreInteractionOff {
		t.Fatal("interaction varsayılanı off kalmalı")
	}
	// v0.10.2491 — flow inline | block; varsayılan inline, geçersiz 400, audit önce/sonra.
	if chstore.DefaultCosreConversation().Flow != chstore.CosreFlowInline {
		t.Fatal("flow varsayılanı inline olmalı")
	}
	if w := rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"flow":"yan"}`, auth.RoleAdmin, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("geçersiz flow → %d", w.Code)
	}
	w = rulesDo(mux, "PUT", "/api/ai/cosre-conversation", `{"flow":"block"}`, auth.RoleAdmin, "")
	if cur := chstore.CurrentCosreConversation(); w.Code != http.StatusOK || cur.Flow != chstore.CosreFlowBlock || cur.Interaction != chstore.CosreInteractionOn {
		t.Fatalf("flow block PUT %d: %s", w.Code, w.Body)
	}
	if a := nextAudit(t, s); !strings.Contains(a.Details, `"flow":"inline"},"after":`) || !strings.HasSuffix(a.Details, `"flow":"block"}}`) {
		t.Fatalf("audit = %+v", a)
	}
}

type conversationFakeStore struct {
	saves int
	saved chstore.CosreConversation
}

func (f *conversationFakeStore) SaveCosreConversation(_ context.Context, c chstore.CosreConversation) (chstore.CosreConversation, error) {
	f.saves++
	f.saved = c
	return c, nil
}
