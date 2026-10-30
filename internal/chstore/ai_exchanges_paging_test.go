package chstore

import (
	"strings"
	"testing"
	"time"
)

// v0.10.1646 (operatör onaylı) — /ai "CoSRE etkileşimleri" TÜM soruları
// göstersin: önceden LIMIT 200 eski turları gizliyordu, arama / kullanıcı /
// kademe süzgeci yoktu. Bu dosya SAF SQL kurucusunu çiviler: zaman sınırı,
// LIMIT + OFFSET + max_execution_time, parametreli arama (kullanıcı metni
// SQL'e GİRMEZ), kapalı küme kademe, bind sayısı = arg sayısı.

func pagingFixtureParams() ListAIExchangesParams {
	from := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	return ListAIExchangesParams{From: from, To: from.Add(6 * time.Hour), Limit: 100}
}

func TestAIExchangeFilterSQL_BindsMatchArgs(t *testing.T) {
	base := pagingFixtureParams()
	cases := []struct {
		name  string
		patch func(p *ListAIExchangesParams)
		want  []string
	}{
		{"none", func(*ListAIExchangesParams) {}, nil},
		{"search", func(p *ListAIExchangesParams) { p.Q = "neden yavaş" }, []string{"positionCaseInsensitiveUTF8(prompt_sample, ?) > 0"}},
		{"user", func(p *ListAIExchangesParams) { p.User = "user-a@example.test" }, []string{"(user_email = ? OR user_id = ?)"}},
		{"tier", func(p *ListAIExchangesParams) { p.Tier = "explain" }, []string{"(surface = ? OR startsWith(surface, ?) OR startsWith(surface, ?))"}},
		{"feedback", func(p *ListAIExchangesParams) { p.Feedback = AIExchangeFeedbackDown }, []string{"GLOBAL IN", "verdict = -1"}},
		{"all", func(p *ListAIExchangesParams) {
			p.Q, p.User, p.Tier, p.Feedback, p.Offset = "x", "u", "loop", AIExchangeFeedbackRated, 300
		}, []string{"positionCaseInsensitiveUTF8", "user_email = ?", "startsWith(surface, ?)", "verdict != 0"}},
	}
	for _, c := range cases {
		p := base
		c.patch(&p)
		p = NormalizeAIExchangeParams(p)
		for _, ext := range []bool{false, true} {
			q := aiExchangeTurnsSQL(ext, p)
			args := aiExchangeTurnsArgs(p)
			if n := strings.Count(q, "?"); n != len(args) {
				t.Errorf("%s ext=%v: %d bind, %d arg\n%s", c.name, ext, n, len(args), q)
			}
			for _, w := range append([]string{
				"created_at >= toDateTime64(?, 9, 'UTC')", "created_at <  toDateTime64(?, 9, 'UTC')",
				aiCallsTurnCond, "ORDER BY created_at DESC, exchange_id DESC", "LIMIT ? OFFSET ?", "max_execution_time",
			}, c.want...) {
				if !strings.Contains(q, w) {
					t.Errorf("%s: %q eksik\n%s", c.name, w, q)
				}
			}
			// LIMIT, OFFSET son iki bind.
			if args[len(args)-2] != p.Limit || args[len(args)-1] != p.Offset {
				t.Errorf("%s: son iki bind limit/offset olmalı: %v", c.name, args)
			}
		}
		cq := aiExchangeCountSQL(p)
		cargs := aiExchangeFilterArgs(p)
		if n := strings.Count(cq, "?"); n != len(cargs) {
			t.Errorf("%s count: %d bind, %d arg\n%s", c.name, n, len(cargs), cq)
		}
		for _, w := range append([]string{"count()", "created_at >= toDateTime64(?, 9, 'UTC')", aiCallsTurnCond, "max_execution_time"}, c.want...) {
			if !strings.Contains(cq, w) {
				t.Errorf("%s count: %q eksik\n%s", c.name, w, cq)
			}
		}
	}
}

// Kullanıcı metni HİÇBİR koşulda SQL metnine girmez — yalnız bind.
func TestAIExchangeFilterSQL_NoInjection(t *testing.T) {
	evil := "x') OR 1=1 -- %_"
	p := pagingFixtureParams()
	p.Q, p.User, p.Tier = evil, evil, evil
	p = NormalizeAIExchangeParams(p)
	for _, q := range []string{aiExchangeTurnsSQL(true, p), aiExchangeCountSQL(p), aiExchangeUsersSQL(evil)} {
		if strings.Contains(q, "1=1") || strings.Contains(q, evil) {
			t.Errorf("kullanıcı metni SQL'e sızdı:\n%s", q)
		}
	}
	if p.Tier != "" {
		t.Errorf("bilinmeyen kademe boşa düşmeli: %q", p.Tier)
	}
	found := false
	for _, a := range aiExchangeFilterArgs(p) {
		if a == evil {
			found = true
		}
	}
	if !found {
		t.Error("arama metni bind olarak AYNEN gitmeli (joker kaçışı gerekmez: positionCaseInsensitiveUTF8)")
	}
}

func TestNormalizeAIExchangeParams(t *testing.T) {
	long := strings.Repeat("ğ", aiExchangeSearchMaxRunes+50)
	cases := []struct {
		name string
		in   ListAIExchangesParams
		chk  func(p ListAIExchangesParams) bool
	}{
		{"limit sıfır → varsayılan", ListAIExchangesParams{}, func(p ListAIExchangesParams) bool { return p.Limit == aiExchangesDefaultLimit }},
		{"limit tavan üstü → varsayılan", ListAIExchangesParams{Limit: 10_000}, func(p ListAIExchangesParams) bool { return p.Limit == aiExchangesDefaultLimit }},
		{"limit geçerli", ListAIExchangesParams{Limit: 50}, func(p ListAIExchangesParams) bool { return p.Limit == 50 }},
		{"offset negatif → 0", ListAIExchangesParams{Offset: -5}, func(p ListAIExchangesParams) bool { return p.Offset == 0 }},
		{"offset tavan", ListAIExchangesParams{Offset: aiExchangesMaxOffset + 1}, func(p ListAIExchangesParams) bool { return p.Offset == aiExchangesMaxOffset }},
		{"arama kırpılır + rune tavanı", ListAIExchangesParams{Q: "  " + long + "  "}, func(p ListAIExchangesParams) bool {
			return len([]rune(p.Q)) == aiExchangeSearchMaxRunes && !strings.HasPrefix(p.Q, " ")
		}},
		{"kademe bilinen", ListAIExchangesParams{Tier: "explain"}, func(p ListAIExchangesParams) bool { return p.Tier == "explain" }},
		{"kademe bilinmeyen", ListAIExchangesParams{Tier: "EXPLAIN"}, func(p ListAIExchangesParams) bool { return p.Tier == "" }},
		{"kullanıcı kırpılır", ListAIExchangesParams{User: " u1 "}, func(p ListAIExchangesParams) bool { return p.User == "u1" }},
		{"pencere sıfır → son 24 sa", ListAIExchangesParams{}, func(p ListAIExchangesParams) bool {
			return !p.To.IsZero() && p.To.Sub(p.From) == 24*time.Hour
		}},
	}
	for _, c := range cases {
		if got := NormalizeAIExchangeParams(c.in); !c.chk(got) {
			t.Errorf("%s: %+v", c.name, got)
		}
	}
}

// Kademe süzgeci etiketin TAM kademesini eşler: "code" "code-sticky"yi
// yakalamaz — üç kol: tam eşitlik, "/rota", "+deep".
func TestAIExchangeTierArgs(t *testing.T) {
	got := aiExchangeTierArgs("explain")
	want := []string{"cosre-turn:explain", "cosre-turn:explain/", "cosre-turn:explain+"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] %v != %v", i, got[i], want[i])
		}
	}
}

func TestAIExchangeUsersSQL(t *testing.T) {
	for _, q := range []string{"", "abc"} {
		s := aiExchangeUsersSQL(q)
		args := aiExchangeUsersArgs(pagingFixtureParams().From, pagingFixtureParams().To, q, 20)
		if n := strings.Count(s, "?"); n != len(args) {
			t.Errorf("q=%q: %d bind, %d arg\n%s", q, n, len(args), s)
		}
		for _, w := range []string{"created_at >= toDateTime64(?, 9, 'UTC')", aiCallsTurnCond, "GROUP BY", "LIMIT ?", "max_execution_time"} {
			if !strings.Contains(s, w) {
				t.Errorf("q=%q: %q eksik\n%s", q, w, s)
			}
		}
		if strings.Contains(s, "positionCaseInsensitiveUTF8") != (q != "") {
			t.Errorf("q=%q: arama koşulu yalnız sorgu varken", q)
		}
	}
}
