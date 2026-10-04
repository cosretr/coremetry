package anomaly

// log_patterns_wfly_test.go — v0.10.1098: "JBoss / WildFly errors" regex'i
// gerçek kod biçimiyle. Eski `(WFLY|JBAS)[0-9]+` önekten hemen sonra rakam
// istiyordu; WildFly kodu önek + alt sistem harfleri + rakamdır
// (`WFLYCTL0013`), yani CH hiç saymıyor, ES'te 1080 örneklemi bastırıyordu.
// Tablo regex'i CH match() anlamıyla uygular (RE2, `(?s)` öneki — logstore
// patternRegexCH ile aynı; harf duyarlı). Gövdeler SENTETİK.

import (
	"regexp"
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore"
)

const wflyOldRegex = `(WFLY|JBAS)[0-9]+`

func TestWildFlyPattern_CHMatchTable(t *testing.T) {
	var p logPattern
	for _, x := range patterns {
		if x.Name == "JBoss / WildFly errors" {
			p = x
		}
	}
	if p.Name == "" {
		t.Fatal("desen yok")
	}
	ch := regexp.MustCompile("(?s)" + p.Regex) // CH match(): `.` satır sonunu da eşler
	old := regexp.MustCompile("(?s)" + wflyOldRegex)
	plan := logstore.ESPatternTerms(p.spec())

	cases := []struct {
		name, body string
		want       bool // CH match() sayar mı
		oldWant    bool // eski regex sayıyor muydu
	}{
		// ── arıza satırları: alt sistem harfli WFLY kodları artık sayılır ──
		{"ctl 3 harf", `WFLYCTL0013: Operation ("deploy") failed - address: ([("deployment" => "orders.war")])`, true, false},
		{"ejb", "WFLYEJB0034: Jakarta Enterprise Beans Invocation failed on component OrdersBean", true, false},
		{"ut 2 harf", "WFLYUT0012: Unable to start listener demo-https", true, false},
		{"srv with errors", "WFLYSRV0026: WildFly Full 26.1.3.Final started (with errors) in 9123ms", true, false},
		{"missing deps", "WFLYCTL0180: Services with missing/unavailable dependencies", true, false},
		{"6 harf alt sistem", "WFLYMSGAMQ0090: Could not create queue jms.queue.DemoOrders", true, false},
		{"seviye önekli satır", "2026-10-04 09:12:01,337 ERROR [org.jboss.as.ejb3.invocation] (default task-7) WFLYEJB0034: Invocation failed", true, false},
		{"çok satırlı gövde", "WFLYCTL0013: Operation (\"deploy\")\n  failed - address: demo.war", true, false},
		{"jbas 6 rakam", `JBAS014612: Operation ("add") failed - address: ([("subsystem" => "datasources")])`, true, true},
		{"jbas failed to start", "JBAS014777: Services which failed to start: service jboss.web.deployment", true, true},

		// ── açılış INFO / WARN: önek var, arıza işareti yok → sayılmaz ──
		{"info starting", "WFLYSRV0049: WildFly Full 26.1.3.Final (WildFly Core 18.1.2.Final) starting", false, false},
		{"info web context", "WFLYUT0021: Registered web context: '/orders' for server 'default-server'", false, false},
		{"info deployed", `WFLYSRV0010: Deployed "orders.war" (runtime-name : "orders.war")`, false, false},
		{"warn node id", "WFLYTX0013: The node-identifier attribute on /subsystem=transactions is set to the default value", false, false},
		{"jbas info", `JBAS015876: Starting deployment of "orders.war"`, false, true},

		// ── kod biçimi değil ──
		{"sözcük içinde", "xWFLYCTL0013 failed", false, false},
		{"küçük harf (CH duyarlı)", "wflyctl0013: operation failed", false, false},
		{"tek harf alt sistem", "WFLYC0013: operation failed", false, false},
		{"alt sistemsiz", "WFLY0013: operation failed", false, true},
		{"jbas 5 rakam", "JBAS01461: operation failed", false, true},
		{"jbas 7 rakam", "JBAS0146120: operation failed", false, true},
		{"rakamsız", "WFLYCTL: operation failed", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ch.MatchString(c.body); got != c.want {
				t.Errorf("CH match=%v, want %v: %q", got, c.want, c.body)
			}
			if got := old.MatchString(c.body); got != c.oldWant {
				t.Errorf("eski regex=%v, tablo %v diyor: %q", got, c.oldWant, c.body)
			}
			if !c.want {
				return
			}
			// CH token ön süzgeci eşleşen satırı budamaz.
			if !chTokensHit(p, c.body) {
				t.Errorf("CH token'ı yok: %q", c.body)
			}
			// ES: `wfly` / `jbas` öneki (1087) kodu taşıyan terimi bulur
			// (`WFLYCTL0013` → `wflyctl0013`).
			if !esPlanHits(plan, standardAnalyze(c.body)) {
				t.Errorf("ES planı bulmuyor (terimler %q)", standardAnalyze(c.body))
			}
		})
	}
}

// Açılış INFO satırını ES planı bulur (önek), regex reddeder: ES fazlasını
// 1080 örneklemi ayıklar, CH hiç saymaz — iki arka uç aynı sonuca varır.
func TestWildFlyPattern_ESOvercountLeftToSampling(t *testing.T) {
	spec, ok := LogPatternSpecByName("JBoss / WildFly errors")
	if !ok {
		t.Fatal("desen yok")
	}
	ch := regexp.MustCompile("(?s)" + spec.Regex)
	body := "WFLYSRV0025: WildFly Full 26.1.3.Final started in 9123ms - Started 512 of 734 services"
	if !esPlanHits(logstore.ESPatternTerms(spec), standardAnalyze(body)) {
		t.Fatalf("ES planı INFO satırını da bulmalı (önek): %q", standardAnalyze(body))
	}
	if ch.MatchString(body) {
		t.Fatalf("regex INFO satırını saymamalı: %q", body)
	}
}
