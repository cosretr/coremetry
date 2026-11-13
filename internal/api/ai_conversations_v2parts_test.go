package api

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// ai_conversations_v2parts_test.go — CoSRE v2 araç seti dilim 1: arşiv kaydı
// kompakt ve güvenli (kapalı kümeler, kök-göreli page_url, tavanlar).

func TestSanitizeChatToolResults(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	in := []aiChatToolResult{
		{ID: "s2", Data: raw(`{"kind":"text","tool":"get_service_health","ok":true,"text":"büyük önizleme","page_url":"/service?name=orders-api"}`)},
		{ID: "s3", Data: raw(`{"kind":"evidence","tool":"query_traces","ok":true,"query":"SELECT 1","page_url":"//evil.example"}`)},
		{ID: "x9", Data: raw(`{"kind":"text"}`)},  // kimlik biçimsiz
		{ID: "s4", Data: raw(`{"kind":"chart"}`)}, // kapalı küme dışı
		{ID: "s5", Data: raw(`[1,2]`)},            // nesne değil
		{ID: "s6", Data: raw(`{"kind":"skipped","ok":false,"reason":"call_cap"}`)},
	}
	got := sanitizeChatToolResults(in)
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "s2,s3,s6" {
		t.Fatalf("kalan kimlikler %v", ids)
	}
	if strings.Contains(string(got[0].Data), "önizleme") || !strings.Contains(string(got[0].Data), `"page_url":"/service?name=orders-api"`) {
		t.Errorf("s2: text düşmeli, page_url kalmalı: %s", got[0].Data)
	}
	if strings.Contains(string(got[1].Data), "page_url") || !strings.Contains(string(got[1].Data), "SELECT 1") {
		t.Errorf("s3: dış page_url düşmeli, sorgu kalmalı: %s", got[1].Data)
	}
}

func TestSanitizeChatToolResultsCaps(t *testing.T) {
	var in []aiChatToolResult
	q := strings.Repeat("q", 500)
	for i := 1; i <= 30; i++ {
		in = append(in, aiChatToolResult{ID: "s" + strconv.Itoa(i), Data: json.RawMessage(`{"kind":"evidence","query":"` + q + `"}`)})
	}
	got := sanitizeChatToolResults(in)
	total := 0
	for _, r := range got {
		total += len(r.Data)
	}
	if len(got) > aiChatToolResultsMax || total > aiChatToolResultsMaxBytes || len(got) == 0 {
		t.Fatalf("tavan: %d kayıt, %d bayt", len(got), total)
	}
}

func TestSanitizeChatStop(t *testing.T) {
	if sanitizeChatStop(&aiChatStop{Reason: "bilinmeyen"}) != nil {
		t.Error("bilinmeyen neden kaldı")
	}
	got := sanitizeChatStop(&aiChatStop{Reason: "call_cap", Calls: -1, Rounds: 3, ElapsedMs: -5})
	if got == nil || got.Calls != 0 || got.Rounds != 3 || got.ElapsedMs != 0 {
		t.Errorf("%+v", got)
	}
}

// Kullanıcı turu araç sonucu / durma taşımaz.
func TestSanitizeChatMessagesV2PartsAssistantOnly(t *testing.T) {
	tr := []aiChatToolResult{{ID: "s1", Data: json.RawMessage(`{"kind":"text","ok":true}`)}}
	st := &aiChatStop{Reason: "stop", Calls: 1, Rounds: 1}
	out := sanitizeChatMessages([]aiChatMessage{
		{Role: "user", Text: "soru", ToolResults: tr, Stop: st},
		{Role: "assistant", Text: "cevap", ToolResults: tr, Stop: st},
	})
	if out[0].ToolResults != nil || out[0].Stop != nil {
		t.Error("kullanıcı turu v2 parçası taşıyor")
	}
	if len(out[1].ToolResults) != 1 || out[1].Stop == nil || out[1].Stop.Reason != "stop" {
		t.Errorf("asistan turu kaybetti: %+v", out[1])
	}
}

// v0.10.2104 (experience-audit D5) — data-links (özet kartı + çipler) ve
// data-question arşivi. Sözleşme: linkler kaydedildiği andaki hedefle döner
// (yeniden hesaplanmaz); href kök-göreli ya da http(s); kart (title/summary)
// yalnız ≤3 kök-göreli linkle kalır, değilse çipler kalır; soru kapalı
// kümede, seçim yalnız seçeneklerden biriyse saklanır. Tavanlar blob'u korur.
func TestSanitizeChatLinksPart(t *testing.T) {
	rel := func(n int) []aiChatLinkItem {
		out := make([]aiChatLinkItem, n)
		for i := range out {
			out[i] = aiChatLinkItem{Label: "L" + strconv.Itoa(i), Href: "/p/" + strconv.Itoa(i)}
		}
		return out
	}
	cases := []struct {
		name string
		in   *aiChatLinksPart
		want *aiChatLinksPart
	}{
		{"nil", nil, nil},
		{"boş", &aiChatLinksPart{}, nil},
		{"kart", &aiChatLinksPart{Links: rel(2), Title: " orders-api ", Summary: "3 açık problem var."},
			&aiChatLinksPart{Links: rel(2), Title: "orders-api", Summary: "3 açık problem var."}},
		{"kart >3 link → çip", &aiChatLinksPart{Links: rel(4), Title: "t", Summary: "s"}, &aiChatLinksPart{Links: rel(4)}},
		{"kart dış link → çip", &aiChatLinksPart{Links: []aiChatLinkItem{{Label: "w", Href: "https://wiki.example.test/a"}}, Title: "t", Summary: "s"},
			&aiChatLinksPart{Links: []aiChatLinkItem{{Label: "w", Href: "https://wiki.example.test/a"}}}},
		{"yarım kart → çip", &aiChatLinksPart{Links: rel(1), Title: "t"}, &aiChatLinksPart{Links: rel(1)}},
		{"kötü href düşer", &aiChatLinksPart{Links: []aiChatLinkItem{{Label: "a", Href: "//evil.example"}, {Label: "b", Href: "javascript:x"},
			{Label: "c", Href: "/\\x"}, {Label: "", Href: "/ok"}, {Label: "d", Href: "/ok"}}},
			&aiChatLinksPart{Links: []aiChatLinkItem{{Label: "d", Href: "/ok"}}}},
		{"hepsi kötü → nil", &aiChatLinksPart{Links: []aiChatLinkItem{{Label: "a", Href: "ftp://x"}}}, nil},
		{"tavan 12", &aiChatLinksPart{Links: rel(20)}, &aiChatLinksPart{Links: rel(12)}},
	}
	for _, c := range cases {
		if got := sanitizeChatLinksPart(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
	long := &aiChatLinksPart{Links: []aiChatLinkItem{{Label: strings.Repeat("ş", 500), Href: "/" + strings.Repeat("a", 5000)}, {Label: "ok", Href: "/ok"}}}
	if got := sanitizeChatLinksPart(long); got == nil || len(got.Links) != 1 || got.Links[0].Href != "/ok" {
		t.Errorf("uzun href düşmeli: %+v", got)
	}
	big := &aiChatLinksPart{Links: rel(12), Title: strings.Repeat("t", 900), Summary: strings.Repeat("s", 2000)}
	big.Links = big.Links[:3]
	got := sanitizeChatLinksPart(big)
	if got == nil || len([]rune(got.Title)) > aiChatLinkCardTitleMax || len([]rune(got.Summary)) > aiChatLinkCardSummaryMax {
		t.Errorf("başlık/özet kırpılmalı: %+v", got)
	}
}

func TestSanitizeChatQuestion(t *testing.T) {
	q := func() *aiChatQuestion {
		return &aiChatQuestion{Kind: "source", Text: "Hangisi?", AllowFree: true, Options: []questionOption{{ID: "telemetri", Label: "Coremetry verisi"}, {ID: "wiki", Label: "Wiki dokümanları"}}}
	}
	ok := q()
	ok.Chosen = "wiki"
	if got := sanitizeChatQuestion(ok); got == nil || got.Chosen != "wiki" || len(got.Options) != 2 || !got.AllowFree {
		t.Fatalf("geçerli soru: %+v", got)
	}
	bad := q()
	bad.Chosen = "yok"
	if got := sanitizeChatQuestion(bad); got == nil || got.Chosen != "" {
		t.Fatalf("seçenek dışı seçim düşmeli: %+v", got)
	}
	for name, mut := range map[string]func(*aiChatQuestion){
		"kind":       func(x *aiChatQuestion) { x.Kind = "later" },
		"metin":      func(x *aiChatQuestion) { x.Text = " " },
		"seçeneksiz": func(x *aiChatQuestion) { x.Options = nil },
		"4 seçenek": func(x *aiChatQuestion) {
			x.Options = append(x.Options, questionOption{ID: "a", Label: "a"}, questionOption{ID: "b", Label: "b"})
		},
		"yinelenen":  func(x *aiChatQuestion) { x.Options[1].ID = x.Options[0].ID },
		"boş etiket": func(x *aiChatQuestion) { x.Options[0].Label = "" },
	} {
		x := q()
		mut(x)
		if got := sanitizeChatQuestion(x); got != nil {
			t.Errorf("%s: düşmeli, got %+v", name, got)
		}
	}
	if sanitizeChatQuestion(nil) != nil {
		t.Error("nil")
	}
	long := q()
	long.Text = strings.Repeat("ç", 2000)
	if got := sanitizeChatQuestion(long); got == nil || len([]rune(got.Text)) > chatPendingQuestionMax {
		t.Errorf("metin kırpılmalı: %d", len([]rune(got.Text)))
	}
}

func TestSanitizeChatMessagesD5AssistantOnly(t *testing.T) {
	lp := &aiChatLinksPart{Links: []aiChatLinkItem{{Label: "a", Href: "/a"}}}
	qq := &aiChatQuestion{Kind: "source", Text: "?", Options: []questionOption{{ID: "wiki", Label: "Wiki"}}}
	out := sanitizeChatMessages([]aiChatMessage{
		{Role: "user", Text: "soru", LinksPart: lp, Question: qq},
		{Role: "assistant", Text: "cevap", LinksPart: lp, Question: qq},
	})
	if out[0].LinksPart != nil || out[0].Question != nil {
		t.Error("kullanıcı turu link/soru taşıyor")
	}
	if out[1].LinksPart == nil || out[1].Question == nil {
		t.Errorf("asistan turu kaybetti: %+v", out[1])
	}
}

// Tel adları FE ChatMessage ile aynı (linksPart / question / chosen).
func TestChatMessageD5WireNames(t *testing.T) {
	raw := `{"role":"assistant","text":"x","linksPart":{"links":[{"label":"a","href":"/a"}],"title":"t","summary":"s"},` +
		`"question":{"kind":"source","text":"?","options":[{"id":"wiki","label":"Wiki"}],"allowFree":true,"chosen":"wiki"}}`
	var m aiChatMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	out := sanitizeChatMessages([]aiChatMessage{m})
	b, _ := json.Marshal(out[0])
	for _, want := range []string{`"linksPart":{"links":[{"label":"a","href":"/a"}],"title":"t","summary":"s"}`, `"chosen":"wiki"`, `"allowFree":true`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s yok: %s", want, b)
		}
	}
}
