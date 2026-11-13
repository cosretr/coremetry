package cosrelearn

// CoSRE öğrenme Faz 4 — çevrimdışı dayanak denetimi (saf çekirdek + gece
// işinde puana bağlanışı).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestParseGrounding(t *testing.T) {
	out := "Tabii:\n```json\n{\"claims\":[{\"claim\":\"error rate 4%\",\"label\":\"GROUNDED\",\"fact\":\"4%\"}," +
		"{\"claim\":\"deploy 2.3 caused it\",\"label\":\"ungrounded\",\"fact\":\"2.3\"}," +
		"{\"claim\":\"x\",\"label\":\"maybe\",\"fact\":\"x\"},{\"claim\":\"\",\"label\":\"grounded\"}]}\n```"
	cs, err := ParseGrounding(out)
	if err != nil || len(cs) != 3 {
		t.Fatalf("%v %+v", err, cs)
	}
	if cs[0].Label != "grounded" || cs[1].Label != "ungrounded" || cs[2].Label != "no_evidence" {
		t.Errorf("etiket normalizasyonu: %+v", cs)
	}
	if _, err := ParseGrounding("bilemiyorum"); err == nil {
		t.Error("JSON yoksa hata")
	}
}

func TestSummarizeGrounding(t *testing.T) {
	in := chstore.CosreGroundingInput{ExchangeID: "e"}
	cases := []struct {
		labels []string
		want   string
	}{
		{[]string{"grounded", "ungrounded"}, "ungrounded"},
		{nil, "no_claims"},
		{[]string{"no_evidence", "no_evidence", "grounded"}, "no_evidence"},
		{[]string{"grounded", "no_evidence"}, "grounded"},
	}
	for _, c := range cases {
		var cs []GroundingClaim
		for _, l := range c.labels {
			cs = append(cs, GroundingClaim{Claim: "c", Label: l})
		}
		if g := SummarizeGrounding(in, cs, "m"); g.Verdict != c.want || int(g.Claims) != len(c.labels) {
			t.Errorf("%v → %s (%+v)", c.labels, g.Verdict, g)
		}
	}
}

func TestBuildGroundingUser(t *testing.T) {
	u := BuildGroundingUser(chstore.CosreGroundingInput{Question: "neden?", Answer: "çünkü X",
		Tools: []chstore.CosreTool{{Name: "a", OK: true, Output: "MCA-00029 x12"}, {Name: "b", OK: false, Output: "err"}, {Name: "c", OK: true}}})
	if !strings.Contains(u, "--- a ---\nMCA-00029 x12") || strings.Contains(u, "--- b ---") || strings.Contains(u, "--- c ---") {
		t.Errorf("yalnız başarılı ve dolu çıktı gider:\n%s", u)
	}
	if u2 := BuildGroundingUser(chstore.CosreGroundingInput{Answer: "a"}); !strings.Contains(u2, "(none") {
		t.Errorf("tool yoksa açıkça söylenir:\n%s", u2)
	}
	big := strings.Repeat("x", 5000)
	u3 := BuildGroundingUser(chstore.CosreGroundingInput{Tools: []chstore.CosreTool{{Name: "a", OK: true, Output: big}, {Name: "b", OK: true, Output: big},
		{Name: "c", OK: true, Output: big}, {Name: "d", OK: true, Output: big}, {Name: "e", OK: true, Output: big}}})
	if len(u3) > groundingTotalOutMax+1000 || !strings.Contains(u3, "omitted") {
		t.Errorf("toplam tavan: %d", len(u3))
	}
}

func TestQualityDayFeedsUngroundedFromJudge(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a := turn("a", "c", 0, "q")
	b := turn("b", "c2", time.Hour, "q2")
	st := &fakeStore{rows: []chstore.CosreInteraction{a, b},
		ground: []chstore.CosreGroundingInput{{ExchangeID: "a", Answer: "x"}, {ExchangeID: "b", Answer: "y"}, {ExchangeID: "z", Answer: "w"}}}
	judge := func(_ context.Context, _, user string) (string, error) {
		switch {
		case strings.HasSuffix(user, "x"):
			return `{"claims":[{"claim":"c","label":"ungrounded","fact":"9"}]}`, nil
		case strings.HasSuffix(user, "w"):
			return "", errors.New("timeout")
		}
		return `{"claims":[{"claim":"c","label":"grounded","fact":"1"}]}`, nil
	}
	if _, _, err := RunQualityDayWithJudge(context.Background(), st, judge, "gemma", day); err != nil {
		t.Fatal(err)
	}
	if len(st.grounded) != 3 || st.grounded[2].Verdict != "judge_error" {
		t.Fatalf("hakem hatası judge_error satırı olmalı: %+v", st.grounded)
	}
	reasons := map[string][]string{}
	for _, q := range st.answers {
		reasons[q.ExchangeID] = q.Reasons
	}
	// a: ungrounded + b'nin 👎'si yok (fakeStore 👎'yi "a"ya verir) → iki olumsuz
	if !contains(reasons["a"], SigUngrounded) || contains(reasons["b"], SigUngrounded) {
		t.Errorf("dayanaksız sinyal yalnız a'ya: %v", reasons)
	}
	if _, _, err := RunQualityDayWithJudge(context.Background(), st, nil, "", day); err != nil {
		t.Errorf("hakemsiz koşu da çalışmalı: %v", err)
	}
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestLocalOnlyJudge — v0.10.2114 (audit §8 soru 5): profil yerel değilse
// hakem modeli ÇAĞRILMAZ, judge_error satırı yazılmaz, puanlama yine koşar.
func TestLocalOnlyJudge(t *testing.T) {
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	st := &fakeStore{rows: []chstore.CosreInteraction{turn("a", "c", 0, "q")},
		ground: []chstore.CosreGroundingInput{{ExchangeID: "a", Answer: "x"}, {ExchangeID: "b", Answer: "y"}}}
	calls := 0
	raw := func(context.Context, string, string) (string, error) {
		calls++
		return `{"claims":[{"claim":"c","label":"grounded","fact":"1"}]}`, nil
	}
	local := false
	judge := LocalOnlyJudge(raw, func(context.Context) bool { return local })
	if _, _, err := RunQualityDayWithJudge(context.Background(), st, judge, "gemma", day); err != nil {
		t.Fatal(err)
	}
	if calls != 0 || len(st.grounded) != 0 {
		t.Fatalf("yerel olmayan profil: model %d kez çağrıldı, %d satır yazıldı", calls, len(st.grounded))
	}
	if len(st.answers) == 0 {
		t.Fatal("hakem atlanınca puanlama da durdu")
	}
	local = true
	if _, _, err := RunQualityDayWithJudge(context.Background(), st, judge, "gemma", day); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(st.grounded) != 2 {
		t.Fatalf("yerel profil: %d çağrı, %d satır (istenen 2/2)", calls, len(st.grounded))
	}
	if LocalOnlyJudge(nil, nil) != nil {
		t.Fatal("nil hakem nil kalmalı (hakemsiz koşu yolu)")
	}
	if _, err := LocalOnlyJudge(raw, nil)(context.Background(), "", ""); !errors.Is(err, ErrJudgeNotLocal) {
		t.Fatalf("denetimsiz hakem çağrıldı: %v", err)
	}
}
