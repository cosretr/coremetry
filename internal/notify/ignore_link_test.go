package notify

// ignore_link_test.go — v0.10.749 "Sustur" bağlantısı: üretici sözleşmesi
// (imzalayıcı/public URL yoksa yok; tür incident/problem; kimlik
// jetona gider), e-posta gövdeleri bağlantıyı alıcıya özel taşır,
// ve KABLOLAMA pinleri: beş şablon + webhook + alıcı başına e-posta.

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func fakeSigner(kind, id, who string, ttl time.Duration) (string, error) {
	return "tok-" + kind + "-" + id + "-" + who + "-" + ttl.String(), nil
}

func TestIgnoreURL(t *testing.T) {
	n := New(nil)
	p := chstore.Problem{ID: "p1", RuleName: "r"}
	if n.ignoreURL(p, "a@example.com") != "" {
		t.Error("imzalayıcısız bağlantı üretildi")
	}
	n.SetActionSigner(fakeSigner)
	if n.ignoreURL(p, "a@example.com") != "" {
		t.Error("public URL yokken bağlantı üretildi")
	}
	n.SetPublicURL("https://coremetry.example.com")
	got := n.ignoreURL(p, "a@example.com")
	want := "https://coremetry.example.com/api/public/notify/ignore/tok-problem-p1-a@example.com-168h0m0s"
	if got != want {
		t.Fatalf("problem bağlantısı:\n got %s\nwant %s", got, want)
	}
	inc := chstore.Problem{ID: "i1", Kind: chstore.NotifyKindIncident}
	if got := n.ignoreURL(inc, channelWho(chstore.NotificationChannel{Type: "slack", Name: "oncall"})); !strings.Contains(got, "tok-incident-i1-channel:slack/oncall-") {
		t.Fatalf("incident/kanal kimliği: %s", got)
	}
	if n.ignoreURL(chstore.Problem{}, "x") != "" {
		t.Error("boş id için bağlantı üretildi")
	}
}

func TestEmailBodiesCarryIgnoreLink(t *testing.T) {
	n := New(nil)
	p := chstore.Problem{ID: "p1", RuleName: "err rate", Service: "shop", Severity: "critical", Status: "open"}
	const link = "https://coremetry.example.com/api/public/notify/ignore/tok"
	plain := n.buildEmailBodyWith(p, nil, link)
	html := n.buildEmailHTMLWith(p, nil, link)
	if !strings.Contains(plain, "Sustur:     "+link) {
		t.Errorf("düz metin bağlantıyı taşımıyor:\n%s", plain)
	}
	if !strings.Contains(html, `href="`+link+`"`) || !strings.Contains(html, "Bu alarmı sustur") {
		t.Errorf("HTML düğme yok:\n%s", html)
	}
	// Bağlantısız çağrı bayt-bayt eski: "Sustur" geçmez.
	if strings.Contains(n.buildEmailBody(p, nil), "Sustur") || strings.Contains(n.buildEmailHTML(p, nil), "sustur") {
		t.Error("bağlantısız gövdede Sustur metni var")
	}
}

func TestIgnoreLinkWiring(t *testing.T) {
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if n := strings.Count(s, "n.ignoreURL(p, channelWho(c))"); n < 4 {
		t.Errorf("paylaşımlı kanal şablonlarında ignoreURL %d (< 4: slack, teams, zoom, webhook)", n)
	}
	for _, want := range []string{
		"for _, rcpt := range ec.Recipients",     // alıcı başına e-posta
		`"ignoreUrl":`,                           // webhook payload alanı
		"IgnoreURL:",                             // webhook şablon değişkeni
		"n.store.NotificationIgnored(ctx, p.ID)", // fan-out kapısı
	} {
		if !strings.Contains(s, want) {
			t.Errorf("notify.go %q içermeli", want)
		}
	}
}
