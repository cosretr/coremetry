package notify

import (
	"os"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.814 — ekip maili tür kapısı + altbilgi (operatör: anomali mailleri
// kapatılamıyordu: ekip maili kanal süzgecinden bağımsız gidiyordu, mail
// nereden kapatılacağını söylemiyordu).

func TestTeamMailKindGateWired(t *testing.T) {
	src, err := os.ReadFile("notify.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "func (n *Notifier) sendTeamMail(")
	if i < 0 {
		t.Fatal("sendTeamMail yok")
	}
	body := s[i:]
	if j := strings.Index(body[1:], "\nfunc "); j >= 0 {
		body = body[:j+1]
	}
	gate, reach := strings.Index(body, "tc.KindAllows(kind)"), strings.Index(body, "teamMailReachRule(")
	if gate < 0 || reach < 0 || gate > reach {
		t.Errorf("tür kapısı alıcı çözümünden ÖNCE olmalı (gate=%d reach=%d)", gate, reach)
	}
	// SendProblemAlert türü bir kez hesaplar ve iki kapıya verir.
	if !strings.Contains(s, "kind := chstore.ProblemNotifyKind(p)") || !strings.Contains(s, "n.ruleNotifyFor(ctx, p), kind)") || !strings.Contains(s, "Kind: kind,") {
		t.Error("SendProblemAlert türü bir kez hesaplayıp ekip maili + kanal süzgecine vermeli")
	}
}

func TestKindFooterTextAndHTML(t *testing.T) {
	n := New(nil)
	p := chstore.Problem{ID: "p1", Service: "shop", RuleID: chstore.PromotedAnomalyRulePrefix + "ev-1", RuleName: "Anomaly · pattern", Severity: "warning", Metric: "anomaly_ratio", StartedAt: 1_700_000_000_000_000_000}
	// PublicURL yok → yol tarifi, bağlantı yok.
	txt := n.buildEmailBodyWith(p, nil, "")
	if !strings.Contains(txt, "Olay türü: Anomali") || strings.Contains(txt, "http") || !strings.Contains(txt, "Team routing") {
		t.Errorf("metin altbilgi (URL'siz): %q", txt)
	}
	h := n.buildEmailHTMLWith(p, nil, "")
	if !strings.Contains(h, "Olay türü: Anomali") || strings.Contains(h, "<div") || strings.Contains(h, "<p>") {
		t.Errorf("HTML altbilgi (URL'siz) Outlook-güvenli olmalı: %q", h)
	}
	// PublicURL var → iki ayar bağlantısı.
	n.SetPublicURL("https://cm.local")
	txt = n.buildEmailBodyWith(p, nil, "")
	for _, want := range []string{"https://cm.local/settings/channels", "https://cm.local/settings/team-routing"} {
		if !strings.Contains(txt, want) {
			t.Errorf("metin altbilgi bağlantısı yok: %s", want)
		}
	}
	h = n.buildEmailHTMLWith(p, nil, "")
	for _, want := range []string{`href="https://cm.local/settings/channels"`, `href="https://cm.local/settings/team-routing"`, "kanal Türler"} {
		if !strings.Contains(h, want) {
			t.Errorf("HTML altbilgi bağlantısı yok: %s", want)
		}
	}
	// Kural problemi "Problem (kural)" der; incident/exception kendi etiketini.
	for rid, want := range map[string]string{"builtin-error-rate": "Problem (kural)", "incident:9f": "Incident", chstore.ExceptionGroupRulePrefix + "new": "Exception"} {
		if got := NotifyKindLabelTR(chstore.ProblemNotifyKind(chstore.Problem{RuleID: rid})); got != want {
			t.Errorf("%s → %q, istenen %q", rid, got, want)
		}
	}
}
