package api

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
)

// chat_summary_card_test.go — v0.10.2101 (docs/cosre/experience-audit.md D4):
// kısa özet + link kartı. Sözleşme: özet cümlesi SUNUCUDA, bundle'ın zaten
// hesapladığı sayılardan (guidedSummaryFacts) deterministik kurulur — model
// çıktısı okunmaz. Yalnız üç rota (service_health, problems, my_services);
// sayı yoksa kart yok (false). Kart linkleri href ile tekil, en çok 3; link
// yoksa kart yok. Emoji yok, tek düz cümle.

func TestGuidedSummaryTR(t *testing.T) {
	svc := func(f guidedSummaryFacts) guidedSummaryFacts { f.Scope = "orders-api"; return f }
	cases := []struct {
		name   string
		intent guidedIntent
		f      guidedSummaryFacts
		want   string
		ok     bool
	}{
		{"health: hepsi", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelWarn, REDKnown: true, ErrorRatePct: 1.24, P99Ms: 340, ProblemsKnown: true, Problems: 2, ProblemsExact: true}),
			"Sağlık durumu Dikkat; hata oranı %1,2, p99 340 ms, 2 açık problem.", true},
		{"health: problem yok", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelHealthy, REDKnown: true, ErrorRatePct: 0, P99Ms: 42.5, ProblemsKnown: true, ProblemsExact: true}),
			"Sağlık durumu Sağlıklı; hata oranı %0, p99 42,5 ms, açık problem yok.", true},
		{"health: problem okunamadı → cümlede yok", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelCrit, REDKnown: true, ErrorRatePct: 12.5, P99Ms: 2400}),
			"Sağlık durumu Sorunlu; hata oranı %12,5, p99 2,4 sn.", true},
		{"health: en az N", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelWarn, REDKnown: true, ErrorRatePct: 0.01, P99Ms: 10, ProblemsKnown: true, Problems: 10}),
			"Sağlık durumu Dikkat; hata oranı %<0,1, p99 10 ms, en az 10 açık problem.", true},
		{"health: RED yok", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelWarn, ProblemsKnown: true, ProblemsExact: true}), "", false},
		{"health: veri yok etiketi", guidedServiceHealth, svc(guidedSummaryFacts{Verdict: healthverdict.LabelNoData, REDKnown: true}), "", false},
		{"health: hüküm yok (operasyon dalı)", guidedServiceHealth, guidedSummaryFacts{}, "", false},
		{"problems: kesin", guidedProblems, guidedSummaryFacts{ProblemsKnown: true, Problems: 3, ProblemsExact: true}, "3 açık problem var.", true},
		{"problems: sıfır", guidedProblems, guidedSummaryFacts{ProblemsKnown: true, ProblemsExact: true}, "Açık problem yok.", true},
		{"problems: en az", guidedProblems, guidedSummaryFacts{ProblemsKnown: true, Problems: 50}, "En az 50 açık problem var.", true},
		{"problems: okunamadı", guidedProblems, guidedSummaryFacts{}, "", false},
		{"my_services: hatalı servis var", guidedMyServices, guidedSummaryFacts{Scope: "acme-team", TeamREDKnown: true, Services: 14, ErrServices: 3, Worst: "orders-api", WorstErrPct: 4.1},
			"14 servisten 3 tanesinde hata var; en yüksek hata oranı orders-api (%4,1).", true},
		{"my_services: hatasız", guidedMyServices, guidedSummaryFacts{Scope: "acme-team", TeamREDKnown: true, Services: 14}, "14 servisin hiçbirinde hata yok.", true},
		{"my_services: pencerede veri yok", guidedMyServices, guidedSummaryFacts{Scope: "acme-team", Services: 14}, "", false},
		{"my_services: servis yok", guidedMyServices, guidedSummaryFacts{Scope: "acme-team", TeamREDKnown: true}, "", false},
		{"kapsam dışı rota", guidedTeamServices, guidedSummaryFacts{Scope: "acme-team", TeamREDKnown: true, Services: 4}, "", false},
		{"kapsam dışı rota: slow_traces", guidedSlowTraces, svc(guidedSummaryFacts{Verdict: healthverdict.LabelWarn, REDKnown: true}), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := guidedSummaryTR(guidedRoute{Intent: c.intent}, c.f)
			if got != c.want || ok != c.ok {
				t.Fatalf("got (%q,%v) want (%q,%v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestGuidedSummaryTitle(t *testing.T) {
	cases := []struct {
		intent guidedIntent
		scope  string
		want   string
	}{
		{guidedServiceHealth, "orders-api", "orders-api"},
		{guidedProblems, "orders-api", "orders-api"},
		{guidedProblems, "", "Açık problemler"},
		{guidedMyServices, "acme-team", "acme-team takımı"},
		{guidedMyServices, "", "Takımın servisleri"},
	}
	for _, c := range cases {
		if got := guidedSummaryTitle(guidedRoute{Intent: c.intent}, guidedSummaryFacts{Scope: c.scope}); got != c.want {
			t.Errorf("%s/%q → %q, want %q", c.intent, c.scope, got, c.want)
		}
	}
}

func TestWithSummaryCard(t *testing.T) {
	facts := guidedSummaryFacts{ProblemsKnown: true, Problems: 3, ProblemsExact: true}
	route := guidedRoute{Intent: guidedProblems}
	l := func(h string) guidedAnswerLink { return guidedAnswerLink{Label: h, Href: "/" + h} }

	t.Run("dedup + tavan 3", func(t *testing.T) {
		ans := map[string]any{"text": "x", "links": []guidedAnswerLink{l("a"), l("a"), l("b"), l("c"), l("d")}}
		withSummaryCard(ans, route, facts)
		card, _ := ans["linkCard"].(map[string]string)
		if !reflect.DeepEqual(card, map[string]string{"title": "Açık problemler", "summary": "3 açık problem var."}) {
			t.Fatalf("kart %v", ans["linkCard"])
		}
		if got := ans["links"].([]guidedAnswerLink); !reflect.DeepEqual(got, []guidedAnswerLink{l("a"), l("b"), l("c")}) {
			t.Fatalf("linkler %v", got)
		}
	})
	t.Run("sayı yok → cevap aynen", func(t *testing.T) {
		links := []guidedAnswerLink{l("a"), l("b"), l("c"), l("d")}
		ans := map[string]any{"text": "x", "links": links}
		withSummaryCard(ans, route, guidedSummaryFacts{})
		if _, has := ans["linkCard"]; has || len(ans["links"].([]guidedAnswerLink)) != 4 {
			t.Fatalf("kart yokken cevap değişti: %v", ans)
		}
	})
	t.Run("kimlikli link → kart yok (satır içi link korunur)", func(t *testing.T) {
		links := []guidedAnswerLink{l("a"), l("b"), l("c"), {Label: "req", Href: "/logs?q=r1", ID: "r1"}}
		ans := map[string]any{"text": "x", "links": links}
		withSummaryCard(ans, route, facts)
		if _, has := ans["linkCard"]; has || len(ans["links"].([]guidedAnswerLink)) != 4 {
			t.Fatalf("kimlikli linkte cevap değişti: %v", ans)
		}
	})
	t.Run("dış link → kart yok", func(t *testing.T) {
		links := []guidedAnswerLink{l("a"), {Label: "wiki", Href: "https://wiki.example.test/p"}}
		ans := map[string]any{"text": "x", "links": links}
		withSummaryCard(ans, route, facts)
		if _, has := ans["linkCard"]; has {
			t.Fatal("dış linkli cevaba kart eklendi")
		}
	})
	t.Run("link yok → kart yok", func(t *testing.T) {
		ans := map[string]any{"text": "x", "links": []guidedAnswerLink(nil)}
		withSummaryCard(ans, route, facts)
		if _, has := ans["linkCard"]; has {
			t.Fatal("linksiz cevaba kart eklendi")
		}
	})
}

func TestSummaryProblemCount(t *testing.T) {
	if n, ok := summaryProblemCount(problemsTotal{n: 120, known: true}, 50); n != 120 || !ok {
		t.Fatalf("bilinen toplam: %d %v", n, ok)
	}
	if n, ok := summaryProblemCount(problemsTotal{}, 50); n != 50 || ok {
		t.Fatalf("bilinmeyen toplam: %d %v", n, ok)
	}
}

func TestTeamSummaryFacts(t *testing.T) {
	rows := []chstore.ServiceSummary{
		{Name: "billing-api", ErrorCount: 4, ErrorRate: 2.5},
		{Name: "orders-api", ErrorCount: 9, ErrorRate: 4.1},
		{Name: "cart-api"},
	}
	got := teamSummaryFacts("acme-team", rows, 14)
	want := guidedSummaryFacts{Scope: "acme-team", Services: 14, TeamREDKnown: true, ErrServices: 2, Worst: "orders-api", WorstErrPct: 4.1}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if f := teamSummaryFacts("acme-team", nil, 14); f.TeamREDKnown || f.ErrServices != 0 {
		t.Fatalf("satırsız: %+v", f)
	}
}

// v1 cevabı değişmez: kart TEK noktada ve yalnız v2 kapısının içinde.
func TestSummaryCardIsV2Gated(t *testing.T) {
	b, err := os.ReadFile("copilot_guided.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if n := strings.Count(src, "withSummaryCard("); n != 1 {
		t.Fatalf("withSummaryCard( %d kez, tam 1 bekleniyor", n)
	}
	if !strings.Contains(src, "if cosreV2Marked(ctx) { // v0.10.2101 (experience-audit D4) — özet kartı yalnız v2; v1 cevabı aynen\n\t\twithSummaryCard(ans, route, facts)\n\t}") {
		t.Fatal("withSummaryCard cosreV2Marked kapısının içinde değil")
	}
}
