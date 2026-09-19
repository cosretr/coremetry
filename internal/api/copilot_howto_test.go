package api

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// v0.10.809 — CoSRE yol tarifi: tek tık (open yok), deterministik, konu dizini.

func TestGuidedHowToAnswerSingleClick(t *testing.T) {
	var events []map[string]any
	var kinds []string
	emit := func(kind string, v any) {
		kinds = append(kinds, kind)
		if m, ok := v.(map[string]any); ok {
			events = append(events, m)
		}
	}
	s := &Server{}
	handled, ok := s.guidedHowToAnswer(emit, guidedRoute{Intent: guidedHowTo, Service: "shop"}, "shop hatalı trace'lerine nasıl ulaşırım?", "")
	if !handled || !ok {
		t.Fatal("how_to cevaplanmalı")
	}
	var ans map[string]any
	for _, e := range events {
		if _, has := e["links"]; has {
			ans = e
		}
	}
	if ans == nil {
		t.Fatal("answer olayı yok")
	}
	if _, has := ans["open"]; has {
		t.Error("TEK TIK: how_to cevabı `open` taşımamalı (otomatik geçiş yok)")
	}
	text, _ := ans["text"].(string)
	if !strings.Contains(text, "NASIL: Hatalı trace'lere ulaşmak") || !strings.Contains(text, "shop servisini seç") {
		t.Errorf("metin: %q", text)
	}
	links, _ := ans["links"].([]guidedAnswerLink)
	var hrefs []string
	for _, l := range links {
		hrefs = append(hrefs, l.Href)
	}
	joined := strings.Join(hrefs, " ")
	if !strings.Contains(joined, "/service?name=shop&tab=operations") || !strings.Contains(joined, "/traces?service=shop&hasError=true") {
		t.Errorf("bağlantılar: %v", hrefs)
	}
	sugg, _ := ans["suggestions"].([]string)
	if len(sugg) == 0 || !strings.Contains(strings.Join(sugg, "|"), "nasıl") {
		t.Errorf("ilgili konu çipleri: %v", sugg)
	}
	if !strings.Contains(strings.Join(kinds, ","), "step") {
		t.Error("product_guide adım çipi yayınlanmalı")
	}
}

func TestGuidedHowToAnswerIndexWhenUnknown(t *testing.T) {
	var ans map[string]any
	emit := func(kind string, v any) {
		if kind == "answer" {
			ans, _ = v.(map[string]any)
		}
	}
	(&Server{}).guidedHowToAnswer(emit, guidedRoute{Intent: guidedHowTo}, "lorem ipsum", "")
	if ans == nil {
		t.Fatal("answer yok")
	}
	if text, _ := ans["text"].(string); !strings.Contains(text, "Hangi konuda yol tarif edeyim") {
		t.Errorf("dizin metni: %q", text)
	}
	if sugg, _ := ans["suggestions"].([]string); len(sugg) != 6 {
		t.Errorf("dizin çipleri 6 olmalı: %v", sugg)
	}
	if _, has := ans["open"]; has {
		t.Error("open yok")
	}
}

// Kablolama: niyet beyaz listede, sınıflandırıcı ve ajan prompt'u anıyor,
// router how_to'yu open_page'den önce yakalıyor.
func TestHowToWired(t *testing.T) {
	if intentAllowed["how_to"] != guidedHowTo {
		t.Error("intentAllowed how_to eksik")
	}
	if !strings.Contains(copilot.SystemPromptIntentClassify(), "how_to") {
		t.Error("sınıflandırıcı prompt'u how_to anmalı")
	}
	if !strings.Contains(copilot.SystemPromptChatAgentLoop(), "product_guide") {
		t.Error("ajan döngüsü product_guide anmalı")
	}
	src, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i, j := strings.Index(body, "if route.Intent == guidedHowTo {"), strings.Index(body, "if route.Intent == guidedOpenPage {")
	if i < 0 || j < 0 || i > j {
		t.Errorf("how_to dalı open_page'den önce olmalı (how=%d open=%d)", i, j)
	}
}
