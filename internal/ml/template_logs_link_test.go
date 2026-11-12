package ml

// v0.10.2026 — Operator-reported: log kalıbı anomalisinde "Logs" o kalıbın
// satırlarını değil, servisin tüm loglarını açıyordu. ML log kartında yeni
// mesaj bağlantısı KESİLMEMİŞ ifadeyle arar; 120 karakterde kesilip "…"
// eklenen metin tırnaklanınca hiçbir satırla eşleşmiyordu. Fikstür nötr.

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestTemplateLogsLinkUsesFullPhrase(t *testing.T) {
	long := "orders-batch: payment reconciliation for com.acme.orders.Settlement step " + strings.Repeat("x", 80)
	st := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		tpl  *CardTemplate
		want string
	}{
		{"Query varsa o kullanılır", &CardTemplate{Text: string([]rune(long)[:120]) + "…", Query: quoteQ(long)}, quoteQ(long)},
		{"Query yoksa kesik işaret atılır", &CardTemplate{Text: "Connection pool exhausted…"}, `"Connection pool exhausted"`},
		{"kısa metin aynen", &CardTemplate{Text: "Connection pool exhausted"}, `"Connection pool exhausted"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := CardInput{Signal: "log", Detector: "error_rate", Service: "orders-batch", StartedAt: st, LastSeen: st.Add(10 * time.Minute), Now: st.Add(10 * time.Minute), NewTemplate: c.tpl}
			links := whereToLookOf(in, buildCardLinks(in, true, st.Add(10*time.Minute)))
			u, err := url.Parse(links[0].Href)
			if err != nil || u.Path != "/logs" {
				t.Fatalf("bağlantı %q", links[0].Href)
			}
			if got := u.Query().Get("q"); got != c.want {
				t.Fatalf("q = %q, istenen %q", got, c.want)
			}
			if u.Query().Get("service") != "orders-batch" {
				t.Error("servis süzgeci kalmalı")
			}
		})
	}
}
