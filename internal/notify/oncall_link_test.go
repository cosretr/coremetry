package notify

import (
	"os"
	"strings"
	"testing"
)

// v0.10.798 — katalogdaki nöbet bağlantısı dört şablonda da Runbook'un yanında
// (metin, HTML, Slack alanı, Teams OpenUri) ve SendProblemAlert'te katalogdan
// doldurulur. Şablonlar Go string'i; bu pin dördünün birden düşmemesini sağlar.
func TestOncallLinkInEveryTemplate(t *testing.T) {
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`p.OncallURL = md.OncallURL`,
		`"On-call:    %s\n", p.OncallURL`,
		`row("On-call", ` + "`" + `<a href="` + "`" + `+esc(p.OncallURL)`,
		`"<%s|On-call ↗>", p.OncallURL`,
		`"name":  "On-call",`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	if strings.Count(s, "p.OncallURL != \"\"") != 4 {
		t.Errorf("dört şablonda da koşullu alan bekleniyor (bulunan %d)", strings.Count(s, "p.OncallURL != \"\""))
	}
}
