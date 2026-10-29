package api

// chat_clarify_flow_test.go — v0.10.1571 (operatör, 2026-10-29: "çipler çok
// kullanılmıyor, soru cevap şeklinde frontier model gibi olsun"): kaynak
// sorusunun GERÇEK copilotChat handler'ından iki turluk akışı. Tur 1 belirsiz
// soruya yalnız sourceAskTextTR'yi sorar (LLM'siz, bekleyen soru Redis
// bağlamında); tur 2 serbest metin cevabı yorumlar ve ÖZGÜN soruyu seçilen
// kaynakta yeniden koşar. Tanınmayan cevap / süresi dolmuş soru akışı
// değiştirmez.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	clarifyUID  = "u-clarify"
	clarifyConv = "conv-clarify"
	clarifyQ    = "servis sahipliği kimde"
)

// clarifyRig — wiki kurulu, operasyon kataloğu boş, guided demeti dikişte.
type clarifyRig struct {
	s      *Server
	mc     *ctxMemCache
	llm    *loopLLM
	rows   func() []chstore.CosreInteraction
	seen   *[]string // wiki anlatıcısının gördüğü bağlam
	routes *[]string // guided rota kararları
}

func newClarifyRig(t *testing.T, llm *loopLLM) *clarifyRig {
	t.Helper()
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{
		"/Platform/Servis sahipliği": "# Servis sahipliği\nHer servisin sahibi takım kataloğunda; sahiplik değişikliği platform ekibine bildirilir.",
	}})
	seen := withFakeNarrator(t, "Servis sahipliği takım kataloğunda tutulur.")
	prevCat := opCatalogSeam
	opCatalogSeam = func(context.Context, []string, int) ([]chstore.CatalogOperation, error) { return nil, nil }
	t.Cleanup(func() { opCatalogSeam = prevCat })
	var mu sync.Mutex
	routes := &[]string{}
	prevSeam := guidedDispatchSeam
	guidedDispatchSeam = func(r guidedRoute) bool {
		mu.Lock()
		*routes = append(*routes, string(r.Intent))
		mu.Unlock()
		return true
	}
	t.Cleanup(func() { guidedDispatchSeam = prevSeam })
	rows := captureCosreRows(t)
	if llm == nil {
		llm = noLLM(t)
	}
	s, mc := newLoopTestServer(t, llm, clarifyUID)
	return &clarifyRig{s: s, mc: mc, llm: llm, rows: rows, seen: seen, routes: routes}
}

func (rg *clarifyRig) post(t *testing.T, turns ...string) []chatFrame {
	t.Helper()
	msgs := make([]map[string]any, 0, len(turns))
	for i, txt := range turns {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, map[string]any{"role": role, "text": txt})
	}
	return postLoopChat(t, rg.s, context.Background(), clarifyUID,
		map[string]any{"messages": msgs, "context": map[string]any{"conversation": clarifyConv}})
}

func (rg *clarifyRig) pending(t *testing.T) *chatPendingAsk {
	t.Helper()
	rg.mc.mu.Lock()
	b := rg.mc.m[chatContextKey(clarifyUID, clarifyConv)]
	rg.mc.mu.Unlock()
	if len(b) == 0 {
		return nil
	}
	var c ChatContext
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return c.Pending
}

func lastRow(t *testing.T, rows []chstore.CosreInteraction) chstore.CosreInteraction {
	t.Helper()
	if len(rows) == 0 {
		t.Fatal("etkileşim satırı yok")
	}
	return rows[len(rows)-1]
}

// replyTool — satırın clarify_reply çipinin argümanları (yoksa nil).
func replyTool(r chstore.CosreInteraction) map[string]string {
	for _, tl := range r.Tools {
		if tl.Name == clarifyReplyTool {
			var m map[string]string
			_ = json.Unmarshal([]byte(tl.Args), &m)
			return m
		}
	}
	return nil
}

func lastAnswerText(fr []chatFrame) string {
	a := framesOf(fr, "answer")
	if len(a) == 0 {
		return ""
	}
	s, _ := a[len(a)-1]["text"].(string)
	return s
}

func TestClarifySourceAskThenWiki(t *testing.T) {
	rg := newClarifyRig(t, nil)
	fr := rg.post(t, clarifyQ)
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierClarify || r.Surface != "cosre-turn:clarify/source_ask" {
		t.Fatalf("tur 1 kaynak sorusu olmalı: tier=%q surface=%q cevap=%q", r.Tier, r.Surface, lastAnswerText(fr))
	}
	if got := lastAnswerText(fr); got != sourceAskTextTR {
		t.Fatalf("cevap yalnız soru olmalı: %q", got)
	}
	if n := len(rg.llm.calls()); n != 0 || r.ModelCalls != 0 {
		t.Fatalf("soru LLM'siz olmalı: %d çağrı", n)
	}
	if fu := framesOf(fr, "answer")[0]["suggestions"]; fu != nil {
		t.Fatalf("çip yok (düz metin soru): %v", fu)
	}
	p := rg.pending(t)
	if p == nil || p.Kind != pendingKindSource || p.Question != clarifyQ {
		t.Fatalf("bekleyen soru Redis bağlamında olmalı: %+v", p)
	}

	fr = rg.post(t, clarifyQ, sourceAskTextTR, "wikide ara")
	r = lastRow(t, rg.rows())
	if r.Tier != aisurface.TierWiki {
		t.Fatalf("tur 2 wiki olmalı: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if len(*rg.seen) != 1 || !strings.Contains((*rg.seen)[0], clarifyQ) || strings.Contains((*rg.seen)[0], "wikide ara") {
		t.Fatalf("anlatıcı ÖZGÜN soruyu görmeli: %q", *rg.seen)
	}
	if !strings.Contains(lastAnswerText(fr), "takım kataloğunda") {
		t.Fatalf("wiki cevabı: %q", lastAnswerText(fr))
	}
	if m := replyTool(r); m["kind"] != pendingKindSource || m["reply"] != srcWiki {
		t.Fatalf("clarify_reply kaydı: %v", m)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("bekleyen soru silinmeli: %+v", p)
	}
}

func TestClarifySourceAskThenCoremetry(t *testing.T) {
	llm := newLoopLLM(t, func(int, map[string]any) map[string]any { return answerMsg("telemetri cevabı") })
	rg := newClarifyRig(t, llm)
	rg.post(t, clarifyQ)
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify {
		t.Fatalf("tur 1: %q", r.Tier)
	}
	fr := rg.post(t, clarifyQ, sourceAskTextTR, "coremetry'de")
	r := lastRow(t, rg.rows())
	if r.Tier == aisurface.TierWiki || r.Tier == aisurface.TierClarify {
		t.Fatalf("telemetri seçildi, tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if len(*rg.seen) != 0 {
		t.Fatalf("wiki anlatımı koşmamalı: %q", *rg.seen)
	}
	calls := llm.calls()
	if len(calls) == 0 {
		t.Fatal("zincir modele ulaşmadı")
	}
	msgs, _ := calls[0]["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	if c, _ := last["content"].(string); !strings.Contains(c, clarifyQ) || strings.Contains(c, "coremetry'de") {
		t.Fatalf("modelin son kullanıcı mesajı ÖZGÜN soru olmalı: %q", c)
	}
	if m := replyTool(r); m["reply"] != srcTelemetry {
		t.Fatalf("clarify_reply kaydı: %v", m)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("telemetri turu yeniden sormamalı: %+v", p)
	}
}

func TestClarifySourceAskUnrelatedReplyDropped(t *testing.T) {
	rg := newClarifyRig(t, nil)
	rg.post(t, clarifyQ)
	frames := rg.post(t, clarifyQ, sourceAskTextTR, "checkout sağlığı nasıl?")
	// v0.10.1575 — düşen soru ekranda adım çizmez; ölçüm satırda kalır (aşağıda).
	for _, f := range frames {
		if f.event == "step" && f.data["tool"] == clarifyReplyTool {
			t.Fatalf("düşen soru için clarify_reply adımı ekrana basılmamalı: %v", f.data)
		}
	}
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierGuided || len(*rg.routes) == 0 || (*rg.routes)[0] != string(guidedServiceHealth) {
		t.Fatalf("ilgisiz cevap yeni soru gibi guided'a gitmeli: tier=%q rotalar=%v", r.Tier, *rg.routes)
	}
	if m := replyTool(r); m["reply"] != clarifyReplyDropped {
		t.Fatalf("clarify_reply dropped olmalı: %v", m)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("düşen soru silinmeli: %+v", p)
	}
}

func TestClarifySourceAskExpiredIgnored(t *testing.T) {
	rg := newClarifyRig(t, nil)
	old, _ := json.Marshal(ChatContext{Pending: &chatPendingAsk{Kind: pendingKindSource, Question: clarifyQ,
		AskedAt: time.Now().Add(-11 * time.Minute).UnixNano()}})
	rg.mc.mu.Lock()
	rg.mc.m[chatContextKey(clarifyUID, clarifyConv)] = old
	rg.mc.mu.Unlock()
	rg.post(t, clarifyQ, sourceAskTextTR, "wikide ara")
	r := lastRow(t, rg.rows())
	if replyTool(r) != nil {
		t.Fatalf("süresi dolmuş soru cevap sayılmamalı: %+v", r.Tools)
	}
	for _, sn := range *rg.seen {
		if strings.Contains(sn, clarifyQ) {
			t.Fatalf("özgün soru yeniden koşmamalı: %q", sn)
		}
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("süresi dolmuş soru silinmeli: %+v", p)
	}
}

// TestClarifySourceAskGatedWithoutWiki — wiki kurulu değilse kaynak sorusu
// sorulmaz: akış bayt bayt eski (niyet kademesi / döngü).
func TestClarifySourceAskGatedWithoutWiki(t *testing.T) {
	rows := captureCosreRows(t)
	prevCat := opCatalogSeam
	opCatalogSeam = func(context.Context, []string, int) ([]chstore.CatalogOperation, error) { return nil, nil }
	t.Cleanup(func() { opCatalogSeam = prevCat })
	s, mc := newLoopTestServer(t, noLLM(t), clarifyUID)
	postLoopChat(t, s, context.Background(), clarifyUID, map[string]any{
		"messages": []map[string]any{{"role": "user", "text": clarifyQ}}, "context": map[string]any{"conversation": clarifyConv}})
	if r := lastRow(t, rows()); r.Tier == aisurface.TierClarify {
		t.Fatal("wiki yokken kaynak sorusu sorulmamalı")
	}
	if b := mc.m[chatContextKey(clarifyUID, clarifyConv)]; strings.Contains(string(b), "pending") {
		t.Fatalf("bekleyen soru yazılmamalı: %s", b)
	}
}
