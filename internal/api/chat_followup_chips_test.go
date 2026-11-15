package api

// v0.10.1291 — kural-türevli takip çipleri (chat_followup_chips.go). Saf;
// model / CH yok. Her METİN şablonu guided router'da yönlenmeli; aksiyon
// çipi (hipotez doğrula) mesaj göndermez, mevcut sre_hypothesis aksiyonuna gider.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sre"
)

const fuTrace = "0af7651916cd43dd8448eb211c80319c"

func fuJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFollowupFactsFromTool(t *testing.T) {
	series := func(name string, avg, max, last float64) map[string]any {
		return map[string]any{"name": name, "unit": "ms", "stats": map[string]float64{"avg": avg, "max": max, "last": last}, "points": []any{}}
	}
	env := func(req map[string]any, data any) string {
		return fuJSON(t, map[string]any{"tool": "x", "ok": true, "request": req, "data": data})
	}
	cases := []struct {
		name, tool, args, content string
		want                      []followupFact
	}{
		{"p99 son değer ≥1.5×ort → yavaş", "query_metrics", `{}`,
			env(map[string]any{"family": "latency", "service": "payments", "env": "prod"},
				map[string]any{"series": []any{series("p50", 10, 12, 11), series("p99", 100, 140, 160)}}),
			[]followupFact{{Kind: fuKindSlow, Service: "payments", Env: "prod"}}},
		{"p99 max ≥2×ort → yavaş (red)", "query_metrics", `{}`,
			env(map[string]any{"family": "red", "service": "payments"}, map[string]any{"series": []any{series("p99", 100, 200, 90)}}),
			[]followupFact{{Kind: fuKindSlow, Service: "payments"}}},
		{"p99 sakin → olgu yok", "query_metrics", `{}`,
			env(map[string]any{"family": "latency", "service": "payments"}, map[string]any{"series": []any{series("p99", 100, 150, 120)}}),
			nil},
		{"errors ailesi sıfır değil", "query_metrics", `{}`,
			env(map[string]any{"family": "errors", "service": "payments"}, map[string]any{"series": []any{series("error_rate", 0, 0, 0), series("errors", 1, 4, 0)}}),
			[]followupFact{{Kind: fuKindErrors, Service: "payments"}}},
		{"errors ailesi hep sıfır", "query_metrics", `{}`,
			env(map[string]any{"family": "errors", "service": "payments"}, map[string]any{"series": []any{series("error_rate", 0, 0, 0)}}),
			nil},
		{"pod satırı limit ≥85", "query_metrics", `{"service":"payments"}`,
			env(map[string]any{"family": "jvm_heap"}, map[string]any{"rows": []any{
				map[string]any{"pod": "payments-a", "limitPct": 92}, map[string]any{"pod": "payments-b", "limitPct": 60}}}),
			[]followupFact{{Kind: fuKindPod, Service: "payments", Pod: "payments-a"}}},
		{"query_traces hata ≥5 + ilk trace", "query_traces", `{}`,
			env(map[string]any{"service": "payments"}, map[string]any{"traces": []any{map[string]any{"traceId": fuTrace}},
				"serviceTotals": map[string]any{"errorRatePct": 7.5}}),
			[]followupFact{{Kind: fuKindErrors, Service: "payments"}, {Kind: fuKindTrace, Service: "payments", TraceID: fuTrace}}},
		{"query_traces ok=false", "query_traces", `{}`, `{"ok":false,"request":{}}`, nil},
		{"pivot pod bulgusu + aday", mcptools.PivotFromTraceName, `{}`,
			fuJSON(t, map[string]any{"ok": true, "request": map[string]any{}, "trace": map[string]any{"service": "checkout"},
				"findings": []any{map[string]any{"hop": "pods", "subject": "orders-7f9", "level": "crit"},
					map[string]any{"hop": "logs", "subject": "orders", "level": "warn"},
					map[string]any{"hop": "jvm", "subject": "checkout-1", "level": "ok"}},
				"data": map[string]any{"pods": []any{map[string]any{"pod": "orders-7f9", "service": "orders"}},
					"candidates": []any{map[string]any{"traceId": fuTrace}}}}),
			[]followupFact{{Kind: fuKindPod, Service: "orders", Pod: "orders-7f9"}, {Kind: fuKindTrace, TraceID: fuTrace}}},
		{"tarama pod + deploy bulgusu", mcptools.ServiceHealthSweepName, `{}`,
			fuJSON(t, map[string]any{"ok": true, "request": map[string]any{"service": "payments"},
				"findings": []any{map[string]any{"kind": "deploy", "name": "rev 42", "level": "warn"},
					map[string]any{"kind": "pod", "name": "payments-a", "level": "crit"},
					map[string]any{"kind": "operation", "name": "GET /x", "level": "crit"}}}),
			[]followupFact{{Kind: fuKindDeploy, Service: "payments"}, {Kind: fuKindPod, Service: "payments", Pod: "payments-a"}}},
		{"log artışı pod katkısı", mcptools.ExplainLogSpikeName, `{}`,
			fuJSON(t, map[string]any{"ok": true, "request": map[string]any{"service": "payments"},
				"contributors": []any{map[string]any{"dim": "level", "value": "ERROR"}, map[string]any{"dim": "pod", "value": "payments-a"}}}),
			[]followupFact{{Kind: fuKindPod, Service: "payments", Pod: "payments-a"}}},
		{"tanınmayan araç", "list_services", `{}`, `{"ok":true}`, nil},
		{"bozuk JSON", "query_metrics", `{}`, `{`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := followupFactsFromTool(c.tool, json.RawMessage(c.args), c.content)
			if len(got) != len(c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("[%d] got %+v want %+v", i, got[i], c.want[i])
				}
			}
		})
	}
}

// Yatırım hipotezi (Faz 2, v0.10.1288) — get_problem araştırma çıktısı.
func TestFollowupHypothesisChip(t *testing.T) {
	content := fuJSON(t, map[string]any{"id": "p-1", "displayId": "PRB-7", "investigation": map[string]any{
		"hypotheses": []any{map[string]any{"kind": sre.HypSaturation, "band": sre.BandHigh}}}})
	facts := followupFactsFromTool("get_problem", nil, content)
	if len(facts) != 1 || facts[0].Kind != fuKindHypothesis || facts[0].Hypothesis != sre.HypSaturation {
		t.Fatalf("olgu: %+v", facts)
	}
	// Kapı: viewer yok, sayfa problemi farklıysa yok; DisplayID ile eşleşme kabul.
	if g := gateFollowupFacts(facts, false, "p-1"); len(g) != 0 {
		t.Fatalf("viewer'a aksiyon çipi önerildi: %+v", g)
	}
	if g := gateFollowupFacts(facts, true, "p-2"); len(g) != 0 {
		t.Fatalf("başka problemin hipotezi önerildi: %+v", g)
	}
	g := gateFollowupFacts(facts, true, "PRB-7")
	chips := followupChips(g, nil, 3)
	if len(chips) != 1 || chips[0].Action == nil {
		t.Fatalf("çip: %+v", chips)
	}
	a := chips[0].Action
	if a.Kind != "sre_hypothesis" || a.ProblemID != "PRB-7" || a.Hypothesis != sre.HypSaturation {
		t.Fatalf("aksiyon: %+v", a)
	}
	// Metin chat_actions.go sreHypothesisAction etiketiyle aynı.
	want := sre.HypothesisLabel(sre.HypSaturation) + " hipotezini doğrula"
	if chips[0].Text != want || a.Label != want {
		t.Fatalf("etiket %q / %q, want %q", chips[0].Text, a.Label, want)
	}
	// Düşük bant / işaretli / bilinmeyen tür → olgu yok (sreHypothesisPick kuralı).
	for _, h := range []map[string]any{
		{"kind": sre.HypSaturation, "band": sre.BandLow},
		{"kind": sre.HypSaturation, "band": sre.BandHigh, "operator": "dismissed"},
		{"kind": "bogus", "band": sre.BandHigh},
	} {
		c := fuJSON(t, map[string]any{"id": "p-1", "investigation": map[string]any{"hypotheses": []any{h}}})
		if f := followupFactsFromTool("get_problem", nil, c); len(f) != 0 {
			t.Errorf("%v → olgu %+v", h, f)
		}
	}
	// loopFollowups uçtan uca: editor + sayfa problemi.
	ed := &auth.Claims{Role: auth.RoleEditor}
	if fu := loopFollowups(ed, facts, nil, "p-1"); len(fu) != 1 || fu[0].Action == nil {
		t.Fatalf("editor: %+v", fu)
	}
	if fu := loopFollowups(&auth.Claims{Role: auth.RoleViewer}, facts, nil, "p-1"); len(fu) != 0 {
		t.Fatalf("viewer: %+v", fu)
	}
}

func TestFollowupChipsDedupeMax3(t *testing.T) {
	facts := []followupFact{
		{Kind: fuKindDeploy, Service: "payments"},
		{Kind: fuKindSlow, Service: "payments"},
		{Kind: fuKindSlow, Service: "payments"}, // kendi içinde tekrar
		{Kind: fuKindErrors, Service: "payments"},
		{Kind: fuKindTrace, TraceID: fuTrace},
		{Kind: fuKindPod, Service: "payments", Pod: "payments-a"},
		{Kind: fuKindPod, Service: "payments", Pod: `bad"pod`}, // şablon dışı → düşer
	}
	got := followupChips(facts, nil, 3)
	if len(got) != 3 {
		t.Fatalf("en çok 3: %+v", got)
	}
	// Öncelik: pod → trace → slow (errors/deploy kesilir).
	if got[0].Kind != fuKindPod || got[1].Kind != fuKindTrace || got[2].Kind != fuKindSlow {
		t.Fatalf("sıra: %v %v %v", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	// Sorulmuş mesaj: birebir (normalize) ve ≥0.8 token benzeri düşer.
	asked := []string{"Payments en yavaş trace'ler", fuTrace + " trace'ini açıkla lütfen"}
	got = followupChips(facts, asked, 3)
	for _, c := range got {
		if c.Kind == fuKindSlow || c.Kind == fuKindTrace {
			t.Errorf("sorulmuş çip tekrar önerildi: %q", c.Text)
		}
	}
	if len(got) != 3 || got[0].Kind != fuKindPod || got[1].Kind != fuKindErrors || got[2].Kind != fuKindDeploy {
		t.Fatalf("dedupe sonrası: %+v", got)
	}
	// Benzer ama ayrı soru (<0.8) çipi düşürmez.
	if got := followupChips([]followupFact{{Kind: fuKindErrors, Service: "payments"}}, []string{"payments sağlığı nasıl?"}, 3); len(got) != 1 {
		t.Fatalf("farklı soru çipi düşürdü: %+v", got)
	}
	// Tür başına bir çip: iki pod olgusu → tek pod çipi (ilk geçerli).
	two := followupChips([]followupFact{{Kind: fuKindPod, Service: "payments", Pod: "payments-a"},
		{Kind: fuKindPod, Service: "payments", Pod: "payments-b"}, {Kind: fuKindErrors, Service: "payments"}}, nil, 3)
	if len(two) != 2 || !strings.Contains(two[0].Text, "payments-a") || two[1].Kind != fuKindErrors {
		t.Fatalf("tür başına bir: %+v", two)
	}
	// Sorulmuş olduğu için düşen pod çipi türü harcamaz: sıradaki pod denenir.
	alt := followupChips([]followupFact{{Kind: fuKindPod, Service: "payments", Pod: "payments-a"},
		{Kind: fuKindPod, Service: "payments", Pod: "payments-b"}}, []string{`payments logları pod:"payments-a"`}, 3)
	if len(alt) != 1 || !strings.Contains(alt[0].Text, "payments-b") {
		t.Fatalf("düşen çip türü harcamamalı: %+v", alt)
	}
	if followupChips(nil, nil, 3) != nil {
		t.Fatal("olgusuz çip")
	}
}

// TestFollowupTemplatesRouteGuided — guidedSuggestions'ın v0.9.411
// sözleşmesi: her METİN şablonu router'da kendi başına, PİNLİ niyete
// yönlenir; yapısal kapsamıyla (chat_scope.go) da aynı rotaya düşer. Yeni
// bir metin türü eklenip buraya pin konmazsa test kırılır.
func TestFollowupTemplatesRouteGuided(t *testing.T) {
	pinned := map[string]struct {
		fact   followupFact
		intent guidedIntent
	}{
		fuKindSlow:   {followupFact{Kind: fuKindSlow, Service: "payments", Env: "prod"}, guidedSlowTraces},
		fuKindErrors: {followupFact{Kind: fuKindErrors, Service: "payments"}, guidedLogErrors},
		fuKindPod:    {followupFact{Kind: fuKindPod, Service: "payments", Pod: "payments-7f9c-abcde"}, guidedLogField},
		fuKindTrace:  {followupFact{Kind: fuKindTrace, TraceID: fuTrace}, guidedTraceByID},
		fuKindDeploy: {followupFact{Kind: fuKindDeploy, Service: "checkout-service"}, guidedDeployImpact},
		// v0.10.1388 — operasyon cevabının topoloji çipi trace_topology'ye (operasyonla) yönlenir.
		fuKindOpTopology: {followupFact{Kind: fuKindOpTopology, Operation: "PAYMENT_CREATE"}, guidedTraceTopology},
	}
	for kind := range fuKindOrder {
		if kind == fuKindHypothesis || kind == fuKindExceptionGroup {
			continue // aksiyon / link çipi (v0.10.1331) — mesaj göndermez, aşağıda
		}
		p, ok := pinned[kind]
		if !ok {
			t.Errorf("tür %q için pinli niyet yok — yönlenmeyen şablon gönderilmez", kind)
			continue
		}
		c, ok := renderFollowup(p.fact)
		if !ok || c.Action != nil || c.Kind != kind {
			t.Fatalf("%s: çip %+v ok=%v", kind, c, ok)
		}
		r := routeGuidedIntent(c.Text, fuServices, fuEnvs, nil, "")
		if r.Intent != p.intent {
			t.Errorf("%q → %s, want %s", c.Text, r.Intent, p.intent)
		}
		switch kind {
		case fuKindPod:
			if r.LogField != "pod" || r.LogValue != p.fact.Pod || r.Service != "payments" {
				t.Errorf("pod çipi rotası: %+v", r)
			}
		case fuKindTrace:
			if r.TraceID != fuTrace || c.Scope == nil || c.Scope.Trace != fuTrace {
				t.Errorf("trace çipi: rota %+v kapsam %+v", r, c.Scope)
			}
		case fuKindOpTopology: // v0.10.1388 — kapsamsız (operasyon servisi katalogdan çözülür)
			if r.TopoOperation != p.fact.Operation || c.Scope != nil {
				t.Errorf("op_topology çipi: rota %+v kapsam %+v", r, c.Scope)
			}
		default:
			if r.Service != p.fact.Service || c.Scope == nil || len(c.Scope.Services) != 1 || c.Scope.Services[0] != p.fact.Service {
				t.Errorf("%s: servis rota=%q kapsam=%+v", kind, r.Service, c.Scope)
			}
			// Kapsam yolu: komutsuz + metinli → plan boş, router aynı rotayı verir.
			_, rest := cleanScopedQuestion(c.Text)
			if pl := planChatScope("", *c.Scope, c.Text, rest, fuServices, fuEnvs); pl.Kind != "" {
				t.Errorf("%s: kapsam planı router'ı atladı: %+v", kind, pl)
			}
		}
	}
	// v0.10.1383 — guided cevabın yapısal servis adımları (guidedFollowups) ve
	// takım özeti çipleri (teamSummaryChips): her tür PİNLİ niyete, aynı
	// servise ve kapsam yolunda da router'a düşer. Yeni tür pinsiz kalırsa kırılır.
	stepPinned := map[string]guidedIntent{
		fuKindOperations: guidedTopErrorOps,
		fuKindExceptions: guidedServiceExceptions,
		fuKindSlow:       guidedSlowTraces,
		fuKindDeploy:     guidedDeployImpact,
		fuKindReport:     guidedServiceReport,
	}
	checkStep := func(c chatFollowup, svc string) {
		t.Helper()
		want, ok := stepPinned[c.Kind]
		if !ok {
			t.Errorf("adım türü %q için pinli niyet yok", c.Kind)
			return
		}
		if c.Label == "" || c.Action != nil || c.Href != "" {
			t.Errorf("%s: etiketli metin çipi bekleniyordu: %+v", c.Kind, c)
		}
		r := routeGuidedIntent(c.Text, fuServices, fuEnvs, nil, "")
		if r.Intent != want || r.Service != svc {
			t.Errorf("%q → %s/%q, want %s/%q", c.Text, r.Intent, r.Service, want, svc)
		}
		if c.Scope == nil || len(c.Scope.Services) != 1 || c.Scope.Services[0] != svc {
			t.Errorf("%s: kapsam %+v", c.Kind, c.Scope)
			return
		}
		_, rest := cleanScopedQuestion(c.Text)
		if pl := planChatScope("", *c.Scope, c.Text, rest, fuServices, fuEnvs); pl.Kind != "" {
			t.Errorf("%s: kapsam planı router'ı atladı: %+v", c.Kind, pl)
		}
	}
	for _, k := range append(append([]string(nil), serviceStepOrder...), fuKindReport) {
		checkStep(serviceStepFollowup(k, "checkout-service", "prod"), "checkout-service")
	}
	for intent := range serviceStepCovered {
		for _, c := range guidedFollowups(guidedRoute{Intent: intent, Service: "checkout-service"}, nil) {
			checkStep(c, "checkout-service")
		}
	}
	// v0.10.1431 — kaynak incelemesinin takip adımları: hata logları / son deploy
	// pinli adımlarla aynı; "Diğer pod'lar" aynı aileyle servis kipinde kaynak
	// incelemesine yönlenir. v0.10.1432 — "GC/heap'e bak" artık aynı niyetin jvm
	// ailesine (1386 grafiği değil).
	resRoute := guidedRoute{Intent: guidedResourceInvestigation, Service: "checkout-service", ResTarget: "checkout-service-6d4f8b9c7d-x2k9q",
		ResKind: resKindPod, ResFamily: resFamCPU, ResJVM: true}
	resPinned := map[string]guidedIntent{fuKindErrors: guidedLogErrors, fuKindDeploy: guidedDeployImpact,
		fuKindResJVM: guidedResourceInvestigation, fuKindResPods: guidedResourceInvestigation}
	for _, c := range guidedFollowups(resRoute, nil) {
		want, ok := resPinned[c.Kind]
		r := routeGuidedIntent(c.Text, fuServices, fuEnvs, nil, "")
		svcOK := r.Service == "checkout-service" || (c.Kind == fuKindResJVM && r.Service == "") // v0.10.2239 (S4): pod sahibi demette resolver'dan
		if !ok || r.Intent != want || !svcOK || c.Scope == nil || c.Scope.Services[0] != "checkout-service" {
			t.Errorf("kaynak takibi %+v → %s/%q", c, r.Intent, r.Service)
		}
	}
	// v0.10.1515 — takım cevabının genel seçici çipleri: seçimle doldurulan
	// cümle pinli niyete, seçilen servise ve kapsam yolunda da router'a düşer.
	pickPinned := map[string]guidedIntent{fuKindTeamPickHealth: guidedServiceHealth, fuKindTeamPickExceptions: guidedServiceExceptions}
	for _, c := range teamServicePickChips([]string{"checkout-service", "payments"}, "prod") {
		want, ok := pickPinned[c.Kind]
		if !ok || c.Pick == nil {
			t.Fatalf("seçici çipi türü %q pinsiz / yüksüz: %+v", c.Kind, c)
		}
		for _, svc := range c.Pick.Services {
			q := fillPickTemplate(c.Pick.Template, svc)
			if r := routeGuidedIntent(q, fuServices, fuEnvs, nil, ""); r.Intent != want || r.Service != svc {
				t.Errorf("%q → %s/%q, want %s/%q", q, r.Intent, r.Service, want, svc)
			}
			sc := chatScope{Env: c.Scope.Env, Services: []string{svc}}
			_, rest := cleanScopedQuestion(q)
			if pl := planChatScope("", sc, q, rest, fuServices, fuEnvs); pl.Kind != "" {
				t.Errorf("%s: kapsam planı router'ı atladı: %+v", c.Kind, pl)
			}
		}
	}

	// Aksiyon çipi: metin router'a GİTMEZ (yönlenmiyor — bu yüzden aksiyon);
	// eski istemcinin suggestions'ına da girmez.
	h, ok := renderFollowup(followupFact{Kind: fuKindHypothesis, ProblemID: "p-1", Hypothesis: sre.HypDeploy})
	if !ok || h.Action == nil || h.Action.Kind != "sre_hypothesis" {
		t.Fatalf("hipotez çipi: %+v", h)
	}
	if got := routeGuidedIntent(h.Text, fuServices, fuEnvs, nil, ""); got.Intent != guidedNone {
		t.Logf("not: hipotez metni %s'e yönleniyor (aksiyon yolu yine de mesaj göndermez)", got.Intent)
	}
}

func TestAnswerFollowupsBackcompat(t *testing.T) {
	base := func() map[string]any { return chatAnswerEvent("cevap", "x1", nil, "") }
	// Olgu yok → olay bayt bayt eski: ne followups ne suggestions.
	ev := withAnswerFollowups(base(), nil)
	if _, ok := ev["followups"]; ok {
		t.Fatal("çipsiz cevapta followups")
	}
	if _, ok := ev["suggestions"]; ok {
		t.Fatal("çipsiz cevapta suggestions — FE statik çiplerini kaybeder")
	}
	chips := followupChips([]followupFact{
		{Kind: fuKindHypothesis, ProblemID: "p-1", Hypothesis: sre.HypDeploy},
		{Kind: fuKindErrors, Service: "payments"},
	}, nil, 3)
	ev = withAnswerFollowups(base(), chips)
	sug, _ := ev["suggestions"].([]string)
	if len(sug) != 1 || sug[0] != "payments hata logları?" {
		t.Fatalf("suggestions yalnız metin çipleri: %v", sug)
	}
	raw, _ := json.Marshal(ev)
	var back struct {
		Suggestions []string `json:"suggestions"`
		Followups   []struct {
			Text  string `json:"text"`
			Kind  string `json:"kind"`
			Scope *struct {
				Services []string `json:"services"`
			} `json:"scope"`
			Action *struct {
				Kind, ProblemID, Hypothesis string
			} `json:"action"`
		} `json:"followups"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Followups) != 2 || back.Followups[0].Action == nil || back.Followups[0].Action.Kind != "sre_hypothesis" ||
		back.Followups[1].Scope == nil || back.Followups[1].Scope.Services[0] != "payments" {
		t.Fatalf("tel şekli: %s", raw)
	}
	// Yalnız aksiyon çipi → suggestions YOK (eski istemci metni serbest döngüye yollamasın).
	ev = withAnswerFollowups(base(), chips[:1])
	if _, ok := ev["suggestions"]; ok {
		t.Fatal("aksiyon-yalnız cevapta suggestions")
	}
	if !strings.Contains(string(raw), `"followups"`) {
		t.Fatal("followups yok")
	}
}

// İki cevap çıkışı da (normal tur + tur tavanı) çipleri taşır — kaynak pini.
func TestLoopAnswerSitesCarryFollowups(t *testing.T) {
	b := mustRead(t, "copilot_chat.go")
	if n := strings.Count(b, "withAnswerFollowups(withAnswerEvidence(chatAnswerEvent("); n != 2 {
		t.Fatalf("followups taşıyan cevap çıkışı %d, want 2", n)
	}
	if !strings.Contains(b, "fuFacts = append(fuFacts, followupFactsFromTool(tc.Name, cargs, tr.Content)...)") {
		t.Fatal("olgular başarılı tool sonucundan toplanmıyor")
	}
}

// TestExceptionGroupFollowupChip — v0.10.1331: operasyon ön-getirmesinin en
// yeni Exceptions grubu "<KOD> grubunu açıkla" LİNK çipi olur (grubun sayfası +
// AI açıklaması); mesaj göndermez → eski istemcinin suggestions'ına girmez;
// bozuk kod / parmak izi çip üretmez; öncelik sırasında hipotezden sonra ilk.
func TestExceptionGroupFollowupChip(t *testing.T) {
	c, ok := renderFollowup(followupFact{Kind: fuKindExceptionGroup, Code: "COR-30518", Fingerprint: "ora:0123456789abcdef"})
	// v0.10.1400 — link çipi yerine açıklama çipi (sohbet içi tur, çekmece yok).
	if !ok || c.Text != "COR-30518 grubunu açıkla" || c.Href != "" || c.ExplainAsk == nil ||
		*c.ExplainAsk != (chatExplainAsk{Kind: explainAskException, ID: "ora:0123456789abcdef", Src: "chip"}) || c.Action != nil || c.Scope != nil {
		t.Fatalf("çip: %+v ok=%v", c, ok)
	}
	for _, bad := range []followupFact{
		{Kind: fuKindExceptionGroup, Code: "COR 30518", Fingerprint: "ora:0123456789abcdef"},
		{Kind: fuKindExceptionGroup, Code: "COR-30518", Fingerprint: "javascript:alert(1)"},
		{Kind: fuKindExceptionGroup, Code: "", Fingerprint: "0123456789abcdef"},
	} {
		if _, ok := renderFollowup(bad); ok {
			t.Errorf("bozuk olgu çip üretti: %+v", bad)
		}
	}
	chips := followupChips([]followupFact{
		{Kind: fuKindErrors, Service: "payments"},
		{Kind: fuKindExceptionGroup, Code: "COR-30518", Fingerprint: "ora:0123456789abcdef"},
	}, nil, followupMax)
	if len(chips) != 2 || chips[0].Kind != fuKindExceptionGroup {
		t.Fatalf("sıra: %+v", chips)
	}
	ans := withAnswerFollowups(map[string]any{}, chips)
	if sg, _ := ans["suggestions"].([]string); len(sg) != 1 || sg[0] != "payments hata logları?" {
		t.Fatalf("link çipi suggestions'a girmemeli: %#v", ans["suggestions"])
	}
	p := &opPrefetch{Exceptions: &exceptionGroupsEvidence{Groups: []exGroupLine{{Fingerprint: "ora:0123456789abcdef", Code: "COR-30518", Oracle: true}}}}
	if f := p.exceptionFollowupFacts(); len(f) != 1 || f[0].Code != "COR-30518" {
		t.Fatalf("ön-getirme olgusu: %+v", f)
	}
	for _, q := range []*opPrefetch{nil, {}, {Exceptions: &exceptionGroupsEvidence{Err: "x", Groups: p.Exceptions.Groups}}} {
		if f := q.exceptionFollowupFacts(); f != nil {
			t.Errorf("grup yok / okunamadı → olgu yok: %+v", f)
		}
	}
}
