package api

// v0.10.1137 — cevaptaki URL'lerin doğrulama listesi (allowedLinks):
// katı çıkarım, noktalama kırpma, tekil, tavan; wiki kademesinin cevap
// yükünde bağlamdaki Jenkins adresi var, önceki tur metnindeki adres YOK.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

func TestExtractContextURLs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"bare + trailing punct", "Pipeline: https://jenkins.example.test/job/orders/. Bitti.", []string{"https://jenkins.example.test/job/orders/"}},
		{"markdown link", "[Jenkins](https://jenkins.example.test/job/a?x=1) ve (http://ci.example.test/b)", []string{"https://jenkins.example.test/job/a?x=1", "http://ci.example.test/b"}},
		{"dedupe case-insensitive host", "https://Jenkins.Example.test/a https://jenkins.example.test/a", []string{"https://Jenkins.Example.test/a"}},
		{"no scheme / js / data", "javascript:alert(1) data:text/html,x ftp://x.example.test www.example.test", nil},
		{"single-label host rejected, localhost ok", "http://intranet/x http://localhost:8080/y", []string{"http://localhost:8080/y"}},
		{"quotes and angle brackets end url", `<a href="https://wiki.example.test/p">x</a>`, []string{"https://wiki.example.test/p"}},
		{"port", "https://grafana.example.test:3000/d/abc,", []string{"https://grafana.example.test:3000/d/abc"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractContextURLs(c.in, allowedLinksMax)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestExtractContextURLsCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&b, "https://h%d.example.test/x ", i)
	}
	if n := len(extractContextURLs(b.String(), allowedLinksMax)); n != allowedLinksMax {
		t.Fatalf("tavan %d olmalı, %d", allowedLinksMax, n)
	}
	got := buildAllowedLinks(b.String(), []guidedAnswerLink{{Href: "/service?service=a"}}, nil)
	if len(got) != allowedLinksMax || got[0] != "/service?service=a" {
		t.Fatalf("liste tavanı + çip önce: %d %q", len(got), got[0])
	}
}

func TestBuildAllowedLinks(t *testing.T) {
	links := []guidedAnswerLink{
		{Label: "Wiki · A", Href: "https://devops.example.test/wiki/A"},
		{Label: "Servis", Href: "/service?service=orders"},
		{Label: "kötü", Href: "//evil.example.test/x"},
		{Label: "kötü2", Href: "javascript:alert(1)"},
	}
	sources := []chatSource{{Doc: "Wiki · A", Ref: "https://devops.example.test/wiki/A"}, {Doc: "doc.pdf"}}
	got := buildAllowedLinks("bkz. https://jenkins.example.test/job/orders/", links, sources)
	want := []string{"https://devops.example.test/wiki/A", "/service?service=orders", "https://jenkins.example.test/job/orders/"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := buildAllowedLinks("", nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("boş girdi boş dilim olmalı (JSON []): %#v", got)
	}
}

func TestWithAllowedLinksIdempotentMerge(t *testing.T) {
	ans := map[string]any{"links": []guidedAnswerLink{{Href: "https://a.example.test/1"}}}
	withAllowedLinks(ans, "x https://ctx.example.test/p y")
	ans["links"] = []guidedAnswerLink{{Href: "https://a.example.test/1"}, {Href: "/problems"}}
	withAllowedLinks(ans, "")
	want := []string{"https://a.example.test/1", "https://ctx.example.test/p", "/problems"}
	if got := ans["allowedLinks"].([]string); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

// Wiki kademesi: sayfadaki Jenkins URL'si cevap yükünde allowedLinks'te;
// önceki asistan turunun (model çıktısı) URL'si listede DEĞİL.
func TestWikiTierAnswerCarriesAllowedLinks(t *testing.T) {
	page := runbookText + "\nPipeline: https://jenkins.example.test/job/svc-orders/ adresinde."
	api := &fakeWikiAPI{pages: map[string]string{"/Runbooks/Restart svc-orders": page}}
	withWikiService(t, wiki.Config{Enabled: true}, api)
	withFakeNarrator(t, "Pipeline https://jenkins.example.test/job/svc-orders/ adresinde.")
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "selam"},
		{Role: "assistant", Text: "bkz https://attacker.example.test/?q=gizli"},
		{Role: "user", Text: "svc-orders runbook'unda yeniden başlatma nasıl anlatılıyor?"},
	}
	if h, ok := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); !h || !ok {
		t.Fatalf("kademe cevaplamalı: %+v", *ev)
	}
	a := answerOf(t, *ev)
	al, ok := a["allowedLinks"].([]string)
	if !ok {
		t.Fatalf("allowedLinks yükte yok: %+v", a)
	}
	joined := strings.Join(al, " ")
	if !strings.Contains(joined, "https://jenkins.example.test/job/svc-orders/") {
		t.Errorf("bağlamdaki Jenkins URL'si listede olmalı: %q", al)
	}
	if !strings.Contains(joined, "https://devops.example.test/") {
		t.Errorf("wiki çip href'i listede olmalı: %q", al)
	}
	if strings.Contains(joined, "attacker") {
		t.Errorf("önceki tur metnindeki URL listeye GİRMEMELİ: %q", al)
	}
}

func TestWikiTierNotFoundHasEmptyAllowedLinks(t *testing.T) {
	withWikiService(t, wiki.Config{Enabled: true}, &fakeWikiAPI{pages: map[string]string{}})
	ev, emit := collectEmit()
	msgs := []copilot.ChatMessage{{Role: "user", Text: "wikide zxqv prosedürü"}}
	if h, _ := (&Server{}).wikiChatAnswer(sessionCtx(), emit, msgs, wikiTierContext{}); !h {
		t.Fatal("güçlü işaret kademede cevaplanmalı")
	}
	if al, ok := answerOf(t, *ev)["allowedLinks"].([]string); !ok || len(al) != 0 {
		t.Fatalf("bulunamadı cevabı boş allowedLinks taşımalı: %#v", al)
	}
}
