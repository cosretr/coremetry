package notify

import (
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// problem_url_test.go — v0.8.492 regression. Bildirim derin linki
// /anomalies?problem= üretiyordu; /anomalies rotası v0.8.219 sonrası
// anomaly-STREAM sayfası olduğundan (?problem='i yok sayar) PublicURL
// ayarlı her kurulumda link yanlış sayfaya düşüyordu. Doğru hedef
// /problems?problem= (api.go statusPageProblemURL ile aynı şekil).
func TestProblemURL(t *testing.T) {
	cases := []struct {
		name, base, id, want string
	}{
		{"unset base → empty (caller omits the line)", "", "p1", ""},
		{"points at /problems, not /anomalies",
			"https://coremetry.example.local", "p1",
			"https://coremetry.example.local/problems?problem=p1&src=notify"},
		{"id is appended verbatim",
			"http://localhost:8088", "prob-42",
			"http://localhost:8088/problems?problem=prob-42&src=notify"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := New(nil)
			n.SetPublicURL(c.base)
			if got := n.problemURL(c.id); got != c.want {
				t.Fatalf("problemURL(%q) = %q, want %q", c.id, got, c.want)
			}
		})
	}
}

// v0.10.2484 — precision programı A.3: her kanalın Problem / incident
// bağlantısı (subjectURL, problemURL → e-posta, Slack, Teams, webhook, özet)
// `src=notify` taşır; ? / & doğru seçilir.
func TestNotifyLinksCarrySrc(t *testing.T) {
	if withNotifySrc("") != "" || withNotifySrc("https://x/a") != "https://x/a?src=notify" || withNotifySrc("https://x/a?b=1") != "https://x/a?b=1&src=notify" {
		t.Fatal("withNotifySrc ? / & seçimi")
	}
	n := New(nil)
	n.SetPublicURL("https://coremetry.example.com")
	for _, id := range []string{"p1", "anomaly-auto:svc:orders-api"} {
		if got := n.subjectURL(chstore.Problem{ID: id}); !strings.HasSuffix(got, "&src=notify") || strings.Count(got, "src=notify") != 1 {
			t.Errorf("%s: %s", id, got)
		}
	}
}
