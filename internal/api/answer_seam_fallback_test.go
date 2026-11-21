package api

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot/styledialogs"
)

// answer_seam_fallback_test.go — v0.10.2439 (kanıt kapısı açık): kapının
// kararı ve kullanıcıya görünen düşüş (not + takip çipi), varsayılanın
// enforce olması, ve örnek diyalog fikstüründe ölçülen engelleme oranı.

func TestEnforceDefaultIsOn(t *testing.T) {
	chstore.SetCosreConversation(chstore.DefaultCosreConversation())
	t.Cleanup(func() { chstore.SetCosreConversation(chstore.DefaultCosreConversation()) })
	if !verifyEnforceOn(withCosreV2Loop(t.Context())) {
		t.Fatal("v2'de varsayılan kip enforce olmalı")
	}
	if verifyEnforceOn(t.Context()) {
		t.Fatal("v1'de zorla kipi koşmaz")
	}
}

func TestEnforceNotePure(t *testing.T) {
	a := "checkout p95 2.950 ms " + verifyUnverifiedNote + "."
	n := seamWithEnforceNote(a)
	if n != a+"\n\n"+verifyEnforceNoteTR || seamWithEnforceNote(n) != n {
		t.Fatalf("not: %q", n)
	}
	if stripEnforceNote(n) != a || stripEnforceNote(a) != a {
		t.Fatal("stripEnforceNote")
	}
	// Not öneri dili taşımaz; modelin kendi önerisi notla örtülmez.
	if detectOffer(verifyEnforceNoteTR, "checkout", time.Now()) != nil {
		t.Fatal("not öneri sayıldı")
	}
	withOffer := "checkout p95 yükselmiş.\n\nİstersen loglarına bakayım."
	if o := detectOffer(stripEnforceNote(seamWithEnforceNote(withOffer)), "checkout", time.Now()); o == nil || o.Kind != "logs" {
		t.Fatalf("modelin önerisi kayboldu: %+v", o)
	}
}

func TestEnforceRecheckChip(t *testing.T) {
	mm := []verifyClaim{{Text: "2.950 ms"}, {Text: "2.950 ms"}, {Text: "payments"}, {Text: "14:32"}, {Text: "p99.9"}}
	c := seamRecheckChip(mm)
	if c.Kind != verifyRecheckKind || c.Label != verifyRecheckLabel ||
		c.Text != "Şu değerleri veriden yeniden kontrol et: «2.950 ms», «payments», «14:32»" {
		t.Fatalf("çip: %+v", c)
	}
	prev := []chatFollowup{{Text: "checkout loglarına bak", Kind: fuKindErrors}}
	m := seamWithRecheckFollowup(map[string]any{"text": "x", "followups": prev, "suggestions": []string{"checkout loglarına bak"}}, mm)
	fu := m["followups"].([]chatFollowup)
	sug := m["suggestions"].([]string)
	if len(fu) != 2 || fu[0].Kind != fuKindErrors || fu[1].Kind != verifyRecheckKind || len(sug) != 2 || len(prev) != 1 {
		t.Fatalf("çip birleşimi: %+v / %v", fu, sug)
	}
	if again := seamWithRecheckFollowup(m, mm); len(again["followups"].([]chatFollowup)) != 2 {
		t.Fatal("ikinci kez eklendi")
	}
}

// Akış: işaretli cevap not + çip taşır; temiz cevapta ikisi de yok; gölgede cevap aynen.
func TestEnforceFallbackVisible(t *testing.T) {
	body, _, _ := enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop(verifyFlowAnswer, ""), nil)
	got := v2AnswerText(body)
	if !strings.HasSuffix(got, "\n\n"+verifyEnforceNoteTR) || !strings.Contains(got, "1.830 ms;") {
		t.Fatalf("not yok ya da cevap kayboldu: %q", got)
	}
	if !strings.Contains(body, "data-followups") || !strings.Contains(body, verifyRecheckLabel) || !strings.Contains(body, "«2.950 ms»") {
		t.Fatalf("takip çipi yok: %s", body)
	}
	good := "checkout p95 1.830 ms."
	body, _, _ = enforceRun(t, chstore.CosreVerifyEnforce, enforceLoop(good, ""), nil)
	if got := v2AnswerText(body); got != good || strings.Contains(body, verifyRecheckLabel) {
		t.Fatalf("temiz cevap değişti: %q", got)
	}
	body, _, _ = enforceRun(t, chstore.CosreVerifyShadow, enforceLoop(verifyFlowAnswer, ""), nil)
	if got := v2AnswerText(body); got != verifyFlowAnswer || strings.Contains(body, verifyRecheckLabel) {
		t.Fatalf("gölge cevabı değiştirdi: %q", got)
	}
}

// Engelleme oranı (fikstür): styledialogs'un 10 diyaloğu, 20 CoSRE turu.
// Kanıt = turun olgu notu + o ana dek operatör satırları + olgu notundaki
// "son N sa/dk" penceresinin saniyesi (üretimde addWindow'un verdiği).
// Topraklı cevaplar: 0/20 tur işaretlenir (pencere saniyesi olmadan 2/20 —
// "son 24 saatte", "son bir saatte"; üretimde pencere kanıttır). Bozulmuş
// cevaplar (ilk düz tamsayı ×7+3; p95 gibi persentil adı atlanır) hepsi yakalanır.
func TestEnforceFixtureBlockRate(t *testing.T) {
	winRe := regexp.MustCompile(`son (\d+|bir) (sa|dk)`)
	numRe := regexp.MustCompile(`(^|[^\w.,])(\d+)`)
	cat := []string{"payments", "checkout", "orders", "inventory"}
	type tally struct{ turns, flagged, pert, caught int }
	run := func(withWindow bool) tally {
		var r tally
		for _, d := range styledialogs.All() {
			var win []float64
			if withWindow {
				for _, m := range winRe.FindAllStringSubmatch(d.Facts, -1) {
					n := 1
					if m[1] != "bir" {
						n, _ = strconv.Atoi(m[1])
					}
					sec := 3600
					if m[2] == "dk" {
						sec = 60
					}
					win = append(win, float64(n*sec))
				}
			}
			var ops []string
			for _, ln := range strings.Split(d.Text, "\n") {
				if q, ok := strings.CutPrefix(ln, "K: "); ok {
					ops = append(ops, q)
					continue
				}
				ans, ok := strings.CutPrefix(ln, "C: ")
				if !ok {
					continue
				}
				ev := verifyEvidence{Texts: append([]string{d.Facts}, ops...), Numbers: win, Loc: istanbul(t)}
				r.turns++
				if mm := verifyAnswer(verifyTierLoop, ans, ev, cat).mismatches(); len(mm) > 0 {
					r.flagged++
					if withWindow {
						t.Errorf("%s: topraklı cevap işaretlendi: %q → %+v", d.ID, ans, mm)
					}
				}
				if loc := numRe.FindStringSubmatchIndex(ans); loc != nil {
					n, _ := strconv.Atoi(ans[loc[4]:loc[5]])
					bad := ans[:loc[4]] + strconv.Itoa(n*7+3) + ans[loc[5]:]
					r.pert++
					if len(verifyAnswer(verifyTierLoop, bad, ev, cat).mismatches()) > 0 {
						r.caught++
					} else {
						t.Errorf("%s: uydurma değer kaçtı: %q", d.ID, bad)
					}
				}
			}
		}
		return r
	}
	on, off := run(true), run(false)
	t.Logf("pencereli: tur=%d işaretli=%d bozulmuş=%d yakalanan=%d · penceresiz işaretli=%d", on.turns, on.flagged, on.pert, on.caught, off.flagged)
	if on.turns < 20 || on.flagged != 0 || on.pert == 0 || on.caught != on.pert || off.flagged > 2 {
		t.Fatalf("oran: pencereli %+v, penceresiz %+v", on, off)
	}
}
