package api

// chat_explain_code_test.go — v0.10.1406 (Tek CoSRE adım 3: sohbette "Kodu da
// incele" ve taze "Yeniden üret"). Operatör: "Trace ve exception üzerinden
// çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi, CoSRE
// bütünselliğine uymuyor."
//
// Pinler:
//   - kod turu (explainAsk.code) ✨ düğmesinin kod dalıyla AYNI önbellek
//     anahtarını kullanır (trace: uçtan uca iki yön; exception: düğmenin
//     anahtar formülü + handler'ın aynı kurucudan geçtiği);
//   - "Kodu da incele" çipi yalnız kodsuz trace / span exception turunda ve
//     kod entegrasyonu yapılandırılmışken;
//   - fresh önbelleği OKUMAZ ama YAZAR (sonraki olağan istek taze cevaba
//     isabet eder); fresh ve kod künyesi arşive girmez.
//
// Sentetik adlar: orders-api, payments, checkout, com.example.orders.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/devops"
)

func xcSetAnswer(p *defProvider, answer ...string) {
	p.mu.Lock()
	p.answer = answer
	p.mu.Unlock()
}

func xcTurn(t *testing.T, s *Server, ask chatExplainAsk, xid string) (map[string]any, []xtEvent) {
	t.Helper()
	var evs []xtEvent
	ctx := copilot.WithMeta(context.Background(), copilot.CallMeta{ExchangeID: xid})
	h, ok := s.chatExplainTurn(ctx, xtRecorder(&evs), &ask, time.UTC)
	if !h || !ok {
		t.Fatalf("tur işlenmedi: h=%v ok=%v %s", h, ok, xtKinds(evs))
	}
	return xtAnswer(t, evs), evs
}

func xcMeta(t *testing.T, ans map[string]any) chatExplainMeta {
	t.Helper()
	m, ok := ans["explain"].(chatExplainMeta)
	if !ok {
		t.Fatalf("künye yok: %v", ans)
	}
	return m
}

// Trace — düğmenin kod dalı ile sohbetin kod turu AYNI satır, iki yönde.
func TestChatExplainCodeTraceSharesButtonCodeKey(t *testing.T) {
	const answer = "**Kök neden:** payments POST /charge card declined (com.example.orders.ChargeClient)."
	t.Run("düğme üretir → sohbet isabet", func(t *testing.T) {
		p := newDefProvider(t, answer)
		s := defServer(t, p)
		w := defCall(s, "", `{"includeCode":true}`)
		if w.Code != http.StatusOK || p.requests() != 1 {
			t.Fatalf("düğme HTTP %d, LLM %d", w.Code, p.requests())
		}
		sys, _ := p.lastMessages(t)
		if sys != copilot.SystemPromptTraceWithCode() {
			t.Fatal("düğmenin kod dalı kodlu sistem prompt'unu kullanmalı")
		}
		ans, evs := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true, Src: "chip"}, "x-code-chat")
		meta := xcMeta(t, ans)
		if p.requests() != 1 || ans["cached"] != true || !meta.Cached {
			t.Fatalf("sohbet kod turu düğmenin satırına isabet etmeli: LLM %d cached=%v", p.requests(), ans["cached"])
		}
		if got := xtKinds(evs); got != "step,step-result,delta,answer" {
			t.Fatalf("olay sırası %s", got)
		}
		if meta.Code == nil || meta.Code.Reason != "kod entegrasyonu yapılandırılmamış" || meta.Surface != aisurface.ExplainTraceChat {
			t.Fatalf("kod künyesi (dürüst durum) %+v", meta.Code)
		}
		if _, has := ans["followups"]; has {
			t.Fatal("kod turu yeniden 'Kodu da incele' çipi önermemeli")
		}
		// Kodsuz tur kodlu satıra isabet etmez (anahtarlar ayrı).
		plain, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace}, "x-plain")
		if plain["cached"] == true || p.requests() != 2 || xcMeta(t, plain).Code != nil {
			t.Fatalf("kodsuz tur kodlu satırdan beslenmemeli: cached=%v LLM %d", plain["cached"], p.requests())
		}
	})
	t.Run("sohbet üretir → düğme isabet", func(t *testing.T) {
		p := newDefProvider(t, answer)
		s := defServer(t, p)
		ans, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}, "x-code-1")
		if ans["cached"] == true || p.requests() != 1 {
			t.Fatalf("ilk kod turu üretmeli: cached=%v LLM %d", ans["cached"], p.requests())
		}
		sys, user := p.lastMessages(t)
		if sys != copilot.SystemPromptTraceWithCode() || !strings.Contains(user, "Trace "+defTestTrace) {
			t.Fatal("sohbet kod turu düğmenin kod dalı prompt'unu kullanmalı")
		}
		w := defCall(s, "", `{"includeCode":true}`)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body["cached"] != true || p.requests() != 1 || body["exchangeId"] != "x-code-1" {
			t.Fatalf("düğme sohbetin kod cevabına isabet etmeli: cached=%v LLM %d xid=%v", body["cached"], p.requests(), body["exchangeId"])
		}
	})
}

// Exception (span grubu) — sohbetin kod turu düğmenin kod anahtar formülüne
// yazar; handler aynı kurucudan geçer (anahtar tek yerde).
func TestChatExplainCodeExceptionSharesButtonCodeKey(t *testing.T) {
	const fp = "00000000000000c3"
	stack := "java.lang.IllegalStateException: order missing\n\tat com.example.orders.OrderService.load(OrderService.java:42)\n"
	in := anomaly.ExceptionExplainInput{User: "EXCEPTION KANITI " + fp + ": orders-api OrderService.load", Stack: stack, ErrorText: "order missing"}
	g := &chstore.ExceptionGroup{Fingerprint: fp, Service: "orders-api", Type: "IllegalStateException", Message: "order missing"}
	exceptionInputSeam = func(_ context.Context, id string, _ *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, error) {
		return g, in, nil
	}
	t.Cleanup(func() { exceptionInputSeam = nil })
	prov := &opExplainProvider{deltas: []string{"**Hata ne demek** order missing — com.example.orders.OrderService.load:42."}}
	s := newOpExplainServer(t, prov)
	mc := newMemCache()
	s.cache = mc

	ans, _ := xcTurn(t, s, chatExplainAsk{Kind: "exception", ID: fp, Code: true, Src: "chip"}, "x-exc-code")
	if prov.calls != 1 || prov.system != copilot.SystemPromptExceptionWithCode() {
		t.Fatalf("kod turu düğmenin kodlu prompt'uyla bir kez üretmeli: calls=%d", prov.calls)
	}
	meta := xcMeta(t, ans)
	if meta.Code == nil || meta.Surface != aisurface.ExplainExceptionChat {
		t.Fatalf("kod künyesi %+v", meta)
	}
	// Düğmenin (v0.9.831…v0.10.1405) anahtar formülü, bağımsız hesap.
	cc := s.buildCodeContext(context.Background(), in.CodeService(g.Service), in.Stack, in.StackVersion)
	se := s.buildSchemaEvidence(in.ErrorText, in.DBStatements, mapperBlocks(cc))
	key := explainCacheKey(copilot.SystemPromptExceptionWithCode(), in.User, cc.PromptBlock()+se.Block)
	if _, hit, _ := mc.Get(context.Background(), key); !hit {
		t.Fatal("kod turu düğmenin kod anahtarına yazmadı")
	}
	if _, hit, _ := mc.Get(context.Background(), explainCacheKey(copilot.SystemPromptException(), in.User, "")); hit {
		t.Fatal("kod turu kodsuz satıra yazmamalı")
	}
	again, _ := xcTurn(t, s, chatExplainAsk{Kind: "exception", ID: fp, Code: true}, "x-exc-code-2")
	if prov.calls != 1 || again["cached"] != true || again["exchangeId"] != "x-exc-code" {
		t.Fatalf("ikinci kod turu isabet: calls=%d cached=%v", prov.calls, again["cached"])
	}
	// Handler aynı kurucudan: anahtar tek yerde hesaplanır.
	h := flatWS(funcBody(readRepoFile(t, "copilot_exception.go"), "copilotExplainException"))
	for _, want := range []string{"p := s.exceptionCodeExplainPrep(r.Context(), g, in)", "cacheKey = p.CacheKey", "opts.IncludeCode && in.Oracle == nil"} {
		if !strings.Contains(h, want) {
			t.Errorf("copilotExplainException %q içermeli", want)
		}
	}
	th := flatWS(funcBody(readRepoFile(t, "trace_explain_handler.go"), "copilotExplainTrace"))
	if !strings.Contains(th, "p := s.traceCodeExplainPrep(r.Context(), in)") || !strings.Contains(th, "cacheKey := p.CacheKey") {
		t.Error("copilotExplainTrace kod dalı traceCodeExplainPrep'ten geçmeli")
	}
	// Oracle grubu: code düşer → kodsuz Oracle prompt'u.
	ora := "ora:00000000000000b2"
	exceptionInputSeam = func(_ context.Context, id string, _ *time.Location) (*chstore.ExceptionGroup, anomaly.ExceptionExplainInput, error) {
		return &chstore.ExceptionGroup{Fingerprint: id, Service: "orders-api", Type: "ORD-200"}, anomaly.ExceptionExplainInput{User: "EXCEPTION KANITI " + id}, nil
	}
	oans, _ := xcTurn(t, s, chatExplainAsk{Kind: "exception", ID: ora, Code: true}, "x-ora")
	if prov.system != copilot.SystemPromptOracleException() || xcMeta(t, oans).Code != nil {
		t.Fatal("Oracle grubunda kod dalı koşmamalı")
	}
}

// Çip tablosu: yalnız kodsuz trace / span exception turu + kod entegrasyonu.
func TestExplainCodeChipOffered(t *testing.T) {
	for _, c := range []struct {
		name       string
		ask        chatExplainAsk
		configured bool
		want       bool
	}{
		{"trace, DevOps var", chatExplainAsk{Kind: "trace", ID: defTestTrace, Src: "button"}, true, true},
		{"trace, DevOps yok", chatExplainAsk{Kind: "trace", ID: defTestTrace}, false, false},
		{"trace zaten kod turu", chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}, true, false},
		{"span exception", chatExplainAsk{Kind: "exception", ID: "00000000000000c3"}, true, true},
		{"Oracle exception", chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2"}, true, false},
		{"problem", chatExplainAsk{Kind: "problem", ID: xrProblemID}, true, false},
		{"span", chatExplainAsk{Kind: "span", ID: defTestTrace, Subject: "span:" + defTestTrace + ":" + defTestPaySpan}, true, false},
	} {
		chip, ok := explainCodeChip(c.ask, c.configured)
		if ok != c.want {
			t.Errorf("%s: çip=%v, want %v", c.name, ok, c.want)
			continue
		}
		if !ok {
			continue
		}
		a := chip.ExplainAsk
		if chip.Label != "Kodu da incele" || a == nil || !a.Code || a.Src != "chip" || a.Kind != c.ask.Kind || a.ID != c.ask.ID || a.Fresh {
			t.Errorf("%s: çip %+v / %+v", c.name, chip, a)
		}
		if _, has := withAnswerFollowups(map[string]any{}, []chatFollowup{chip})["suggestions"]; has {
			t.Errorf("%s: kod çipi eski istemcinin önerilerine sızdı", c.name)
		}
	}
	chip, _ := explainCodeChip(chatExplainAsk{Kind: "trace", ID: defTestTrace}, true)
	b, _ := json.Marshal(chip)
	if !strings.Contains(string(b), `"explainAsk":{"kind":"trace","id":"`+defTestTrace+`","src":"chip","code":true}`) {
		t.Fatalf("tel şekli %s", b)
	}
	// Uçtan uca: DevOps yapılandırılmışken kodsuz trace turu çipi taşır, yokken taşımaz.
	p := newDefProvider(t, "özet")
	s := defServer(t, p)
	plain, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace}, "x-1")
	if _, has := plain["followups"]; has {
		t.Fatal("DevOps yokken çip önerildi")
	}
	dv := devops.New()
	dv.Configure(devops.Settings{BaseURL: "http://devops.example.test", Collection: "DefaultCollection", Project: "Orders"})
	s.devops = dv
	withChip, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace}, "x-2")
	fus, _ := withChip["followups"].([]chatFollowup)
	if len(fus) != 1 || fus[0].ExplainAsk == nil || !fus[0].ExplainAsk.Code {
		t.Fatalf("kod çipi yok: %+v", withChip["followups"])
	}
	// Trace turunun olağan alanları (guided önerileri) çiple değişmez.
	if a, b := fmt.Sprint(withChip["suggestions"]), fmt.Sprint(plain["suggestions"]); a != b {
		t.Fatalf("öneriler kod çipiyle değişti: %s ≠ %s", a, b)
	}
}

// fresh: önbellek okunmaz, cevap yazılır; sonraki olağan istek YENİ metne isabet.
func TestChatExplainFreshSkipsReadAndWrites(t *testing.T) {
	p := newDefProvider(t, "eski cevap")
	s := defServer(t, p)
	first, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace}, "x-old")
	if first["text"] != defLatencyLine+"eski cevap" || p.requests() != 1 { // v0.10.1630
		t.Fatalf("ilk tur %v", first["text"])
	}
	xcSetAnswer(p, "taze ", "cevap")
	fresh, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace, Fresh: true}, "x-new")
	if p.requests() != 2 || fresh["cached"] == true || fresh["text"] != defLatencyLine+"taze cevap" || xcMeta(t, fresh).Cached {
		t.Fatalf("fresh önbelleği okumamalı: LLM %d cached=%v text=%v", p.requests(), fresh["cached"], fresh["text"])
	}
	again, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace}, "x-later")
	if p.requests() != 2 || again["cached"] != true || again["text"] != defLatencyLine+"taze cevap" || again["exchangeId"] != "x-new" {
		t.Fatalf("olağan istek taze cevaba isabet etmeli: LLM %d cached=%v text=%v xid=%v", p.requests(), again["cached"], again["text"], again["exchangeId"])
	}
	// Düğme de aynı satırı okur (✨ "Yeniden sor" ile aynı sözleşme).
	var body map[string]any
	_ = json.Unmarshal(defCall(s, "", "").Body.Bytes(), &body)
	if body["cached"] != true || body["explanation"] != defLatencyLine+"taze cevap" {
		t.Fatalf("düğme taze cevabı görmeli: %v", body)
	}
	// Kayıt türleri (genel tur) de fresh'i taşır.
	xp := newDefProvider(t, "problem eski")
	xs := xrServer(t, xp)
	xcTurn(t, xs, chatExplainAsk{Kind: "problem", ID: xrProblemID}, "p-1")
	xcSetAnswer(xp, "problem taze")
	pf, _ := xcTurn(t, xs, chatExplainAsk{Kind: "problem", ID: xrProblemID, Fresh: true}, "p-2")
	pn, _ := xcTurn(t, xs, chatExplainAsk{Kind: "problem", ID: xrProblemID}, "p-3")
	if xp.requests() != 2 || pf["cached"] == true || pn["cached"] != true || pn["text"] != "problem taze" {
		t.Fatalf("problem fresh: LLM %d fresh.cached=%v sonraki=%v/%v", xp.requests(), pf["cached"], pn["cached"], pn["text"])
	}
	// Kod turu da fresh'i taşır.
	xcSetAnswer(p, "kodlu")
	xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}, "c-1")
	n := p.requests()
	cf, _ := xcTurn(t, s, chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true, Fresh: true}, "c-2")
	if p.requests() != n+1 || cf["cached"] == true {
		t.Fatalf("kod turunda fresh önbelleği atlamalı: LLM %d→%d", n, p.requests())
	}
}

// Niyet normalizasyonu: code yalnız kod-yetenekli öznede; fresh her türde;
// arşiv künyesi ne fresh ne kod künyesi taşır.
func TestExplainAskCodeFreshNormalizeAndSanitize(t *testing.T) {
	for _, c := range []struct {
		name            string
		in              chatExplainAsk
		wantCode, fresh bool
	}{
		{"trace kod", chatExplainAsk{Kind: "trace", ID: defTestTrace, Code: true}, true, false},
		{"span exception kod + fresh", chatExplainAsk{Kind: "exception", ID: "00000000000000c3", Code: true, Fresh: true}, true, true},
		{"Oracle kod düşer", chatExplainAsk{Kind: "exception", ID: "ora:00000000000000b2", Code: true}, false, false},
		{"problem kod düşer, fresh kalır", chatExplainAsk{Kind: "problem", ID: xrProblemID, Code: true, Fresh: true}, false, true},
		{"service-health fresh", chatExplainAsk{Kind: "service-health", Subject: formatAiSubject(aiSubject{Kind: "service-health", ID: "orders-api",
			From: time.Unix(0, 1), To: time.Unix(0, 2)}), Fresh: true}, false, true},
	} {
		got, ok := normalizeExplainAsk(&c.in)
		if !ok || got.Code != c.wantCode || got.Fresh != c.fresh {
			t.Errorf("%s: %+v ok=%v", c.name, got, ok)
		}
	}
	// İstemcinin geri gönderdiği künye fresh / code taşısa da arşive girmez.
	raw := `[{"role":"assistant","text":"x","explain":{"kind":"trace","id":"` + defTestTrace + `","fresh":true,"code":{"repo":"orders","files":[{"path":"a.java","fromLine":1,"toLine":2}]}}}]`
	var msgs []aiChatMessage
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatal(err)
	}
	out := sanitizeChatMessages(msgs)
	b, _ := json.Marshal(out)
	if len(out) != 1 || out[0].Explain == nil || strings.Contains(string(b), "fresh") || strings.Contains(string(b), `"code"`) {
		t.Fatalf("arşiv künyesi fresh/kod taşımamalı: %s", b)
	}
	// Niyetin teli: fresh / code yalnız doluyken.
	plain, _ := json.Marshal(chatExplainAsk{Kind: "trace", ID: defTestTrace})
	if strings.Contains(string(plain), "fresh") || strings.Contains(string(plain), "code") {
		t.Fatalf("boş bayraklar telde görünmemeli: %s", plain)
	}
}
