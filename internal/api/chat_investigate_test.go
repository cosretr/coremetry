package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/copilot/evalset"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestInvestigateTrigger(t *testing.T) {
	on := investigateIn{V2: true, Evidence: chstore.CosreEvidenceOn, Class: classErrorHunt, Service: "checkout"}
	cases := []struct {
		name string
		mut  func(*investigateIn)
		want bool
	}{
		{"hepsi tutar", func(*investigateIn) {}, true},
		{"v1", func(i *investigateIn) { i.V2 = false }, false},
		{"evidence off", func(i *investigateIn) { i.Evidence = chstore.CosreEvidenceOff }, false},
		{"evidence shadow", func(i *investigateIn) { i.Evidence = chstore.CosreEvidenceShadow }, false},
		{"durum sınıfı", func(i *investigateIn) { i.Class = classStatus }, false},
		{"rapor sınıfı", func(i *investigateIn) { i.Class = classReport }, false},
		{"servis yok", func(i *investigateIn) { i.Service = " " }, false},
		{"trace takibi / Derin / rota", func(i *investigateIn) { i.Blocked = true }, false},
	}
	for _, c := range cases {
		in := on
		c.mut(&in)
		if got := investigateWanted(in); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

func TestInvestigateCue(t *testing.T) {
	for q, want := range map[string]bool{
		"checkout'taki hatayı incele":         true,
		"checkout'ta kök nedeni araştır":      true,
		"orders'ta ne oldu":                   true,
		"Sorunu bul":                          true,
		"checkout'ta hata oranı kaç":          false,
		"payments'ta hangi istisna patlıyor?": false,
	} {
		if investigateCue(q) != want {
			t.Errorf("%q: %v", q, !want)
		}
	}
}

// İstem bölümü yalnız araştırma turunda; ≤650 B; taban istem değişmez.
func TestInvestigateAddendumOnlyOnInvestigationTurns(t *testing.T) {
	add := copilot.ChatInvestigateAddendum()
	if n := len(add); n > 650 || !strings.Contains(add, "evidence_pack") {
		t.Fatalf("bölüm %d B", n)
	}
	plan := &v2LoopPlan{Addendum: "EK"}
	if got := v2SubsetPlan(stepInput{V2: plan}, stepPlan{}).SystemAddendum; got != "EK" {
		t.Fatalf("paketsiz tur: %q", got)
	}
	plan.Investigate = true
	if got := v2SubsetPlan(stepInput{V2: plan}, stepPlan{}).SystemAddendum; got != "EK"+add {
		t.Fatalf("araştırma turu: %q", got)
	}
}

func investigateStubs() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		evidencePackTool: json.RawMessage(`{"red":{"spans":3600,"errors":180,"p95_ms":420},"prev_red":{"spans":3600,"errors":36,"p95_ms":300},
			"error_ops":[{"name":"POST /api/checkout","spans":1000,"errors":150}],
			"exceptions":[{"type":"java.net.SocketTimeoutException","message":"Read timed out","service":"checkout","count":412,"sampleTraceId":"4bf92f3577b34da6a3ce929d0e0e4736"}],
			"deploys":[],"edges":[{"source":"checkout","target":"payments","callCount":900,"errorRate":12.5,"avgMs":800}]}`),
		mcptools.ExplainTraceName: json.RawMessage(`{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","found":true,"page_url":"/trace/4bf92f3577b34da6a3ce929d0e0e4736","root":{"service":"checkout","name":"POST /api/checkout","duration_ms":1240},"wall_ms":1240,"error_spans":[{"service":"payments","name":"POST /api/payments","exception_type":"java.net.SocketTimeoutException"}]}`),
		mcptools.SearchLogsV2Name: json.RawMessage(`{"total":2,"query":"trace_id","page_url":"/logs?trace=4bf92f3577b34da6a3ce929d0e0e4736","rows":[{"ts":1794996000000,"sev":"ERROR","svc":"payments","body":"Read timed out","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}]}`),
	}
}

func investigateCtx(mode string) (context.Context, *evalConvTurn, *v2LoopPlan) {
	ct := &evalConvTurn{Stubs: investigateStubs(), Evidence: mode}
	ctx := withChatEvalMode(withCosreV2Loop(context.Background()), &chatEvalMode{Conversation: ct})
	plan := &v2LoopPlan{}
	plan.Evidence = newEvidenceTurn(ctx, "checkout'taki hatayı incele")
	return withV2LoopPlan(ctx, plan), ct, plan
}

// evidence off / shadow: konuşma, künye ve olaylar aynen; on: tek tohum çifti,
// adım çipleri, S3 adayları, ES okuması ≤1.
func TestChatInvestigateOffShadowInertOnSeeds(t *testing.T) {
	s := &Server{}
	in := investigateTurn{Question: "checkout'taki hatayı incele", Service: "checkout", RangeS: 3600, To: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)}
	conv := []copilot.ChatMessage{{Role: "user", Text: in.Question}}
	for _, mode := range []string{chstore.CosreEvidenceOff, chstore.CosreEvidenceShadow} {
		ctx, ct, plan := investigateCtx(mode)
		var events int
		got, tools := s.chatInvestigate(ctx, func(string, any) { events++ }, in, conv, nil)
		if len(got) != 1 || tools != nil || events != 0 || plan.Investigate || len(ct.snapshot()) != 0 {
			t.Fatalf("%s: akış değişti (conv %d, olay %d)", mode, len(got), events)
		}
	}
	ctx, ct, plan := investigateCtx(chstore.CosreEvidenceOn)
	var labels []string
	got, tools := s.chatInvestigate(ctx, func(kind string, p any) {
		if m, ok := p.(map[string]any); ok && kind == "step" { // v0.10.2363 — any (withStepIDs yerinde damgalar)
			labels = append(labels, m["label"].(string))
		}
	}, in, conv, nil)
	if len(got) != 3 || got[1].ToolCalls[0].Name != evidencePackTool || got[2].ToolResults[0].CallID != evidencePackToolID {
		t.Fatalf("tohum çifti: %+v", got)
	}
	digest := got[2].ToolResults[0].Content
	if len(digest) > 2048 || !strings.Contains(digest, `"err_pct":5`) || !strings.Contains(digest, `"prev_err_pct":1`) {
		t.Fatalf("özet %s", digest)
	}
	if !plan.Investigate || len(tools) != 1 || len(labels) != 7 || labels[0] != "belirti: hata %5 (önceki %1), p95 420 ms" {
		t.Fatalf("plan %v tools %v çipler %q", plan.Investigate, tools, labels)
	}
	if len(plan.Evidence.cands) != 2 {
		t.Fatalf("S3 adayları %d", len(plan.Evidence.cands))
	}
	logs := 0
	for _, c := range ct.snapshot() {
		if c.Name == mcptools.SearchLogsV2Name {
			logs++
		}
	}
	if ran, ok, un, _ := ct.pack(); !ran || ok != 6 || un != 1 || logs != 1 { // anomali sahtesi yok → unchecked
		t.Fatalf("paket %v %d %d, log %d", ran, ok, un, logs)
	}
}

// Trace takibi (Blocked) ve rapor sınıfında paket koşmaz; ipucu orta kademeyi atlamaz.
func TestChatInvestigateNotInTraceFollowUp(t *testing.T) {
	ctx, _, plan := investigateCtx(chstore.CosreEvidenceOn)
	in := investigateTurn{Question: "checkout'taki hatayı incele", Service: "checkout", Blocked: true, To: time.Now()}
	if got, _ := (&Server{}).chatInvestigate(ctx, func(string, any) {}, in, nil, nil); len(got) != 0 || plan.Investigate {
		t.Fatal("trace takibinde paket koştu")
	}
	if investigateSkipTiers(ctx, in.Question, ChatContext{}, "checkout", true) {
		t.Fatal("engelli turda kademe atlandı")
	}
	if !investigateSkipTiers(ctx, in.Question, ChatContext{}, "checkout", false) {
		t.Fatal("ipucu kademeleri atlamadı")
	}
	if investigateSkipTiers(ctx, "checkout'ta hata var mı", ChatContext{}, "checkout", false) {
		t.Fatal("ipucusuz soru kademe atladı")
	}
}

func TestInvestigateWindow(t *testing.T) {
	to := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	from, got := investigateWindow(investigateTurn{To: to})
	if got != to || to.Sub(from) != time.Hour {
		t.Fatalf("varsayılan %v %v", from, got)
	}
	st := ChatContext{RangeS: 900, WinToMs: to.Add(-time.Hour).UnixMilli()}
	from, got = investigateWindow(investigateTurn{To: to, State: st, RangeS: 3600})
	if !got.Equal(to.Add(-time.Hour)) || got.Sub(from) != 15*time.Minute {
		t.Fatalf("durum penceresi %v %v", from, got)
	}
}

// investigate.json: üç S4 vakası kanıt kipini açar; ilk tur paket + tur tavanı ister.
func TestInvestigateEvalCases(t *testing.T) {
	cases, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"conv-investigate-5xx-after-deploy": false, "conv-investigate-exception-downstream": false, "conv-investigate-partial-pack": false}
	for _, c := range cases {
		if _, ok := want[c.ID]; !ok {
			continue
		}
		e := c.Turns[0].Expect
		want[c.ID] = c.file == "investigate.json" && c.Evidence == chstore.CosreEvidenceOn && e.Class == "error_hunt" &&
			e.Pack != nil && *e.Pack && e.MaxRounds != nil && e.Evidence != nil && len(c.Stubs[evidencePackTool]) > 0
		if !investigateCue(c.Turns[0].User) || answerClassHint(c.Turns[0].User, ChatContext{}, false) != classErrorHunt {
			t.Errorf("%s: ilk tur ipucu / hata avı sınıfı taşımıyor", c.ID)
		}
	}
	for id, ok := range want {
		if !ok {
			t.Errorf("%s: investigate.json'da yok ya da beklentisi eksik", id)
		}
	}
}

// Uçtan uca (sahte model): investigate.json'un 5xx vakası gerçek handler'da
// koşar — paket tek tohum çifti, istem bölümü yalnız ilk turda, tek model
// turunda cevap, trace kartı; kanıt kapalıyken istekte paket yok.
func TestInvestigateEvalCaseEndToEnd(t *testing.T) {
	all, err := loadEvalConversationFS(evalset.ConversationFS)
	if err != nil {
		t.Fatal(err)
	}
	var c evalCase
	for _, x := range all {
		if x.ID == "conv-investigate-5xx-after-deploy" {
			c = x
		}
	}
	for _, mode := range []string{chstore.CosreEvidenceOn, chstore.CosreEvidenceOff} {
		c.Evidence = mode
		h, llm, _ := newAgentEvalHarness(t, func(_ int, _ map[string]any) map[string]any {
			return map[string]any{"role": "assistant", "content": "checkout'ta hata oranı %5'e çıktı; 2.7.0 deploy'undan sonra POST /api/checkout 1650 kez 503 döndü, örnek trace 4bf92f35. Bağımlılık temiz; anomaliye bakamadım. İstersen 2.6.3'e dönüşü değerlendirelim."}
		}, []evalCase{{ID: "single", Surface: "Problem", Why: "w", User: "x", Expect: evalExpect{MustContain: []string{"x"}}}})
		h.s.l1, h.s.stats = newL1Cache(64), newCacheStats()
		prev := evalConversationLoader
		evalConversationLoader = func() ([]evalCase, error) { return []evalCase{c}, nil }
		_, cases := runAgentEval(t, h, `{"surfaces":["ChatConversation"]}`)
		evalConversationLoader = prev
		if len(cases) != 1 || len(cases[0].Turns) != 2 {
			t.Fatalf("%s: sonuç %+v", mode, cases)
		}
		t1, t2 := cases[0].Turns[0], cases[0].Turns[1]
		llm.mu.Lock()
		seeded, sections := 0, 0
		for _, body := range llm.bodies {
			raw, _ := json.Marshal(body["messages"])
			if strings.Contains(string(raw), evidencePackToolID) {
				seeded++
			}
			if strings.Contains(string(raw), "ARAŞTIRMA: evidence_pack") {
				sections++
			}
		}
		llm.mu.Unlock()
		if mode == chstore.CosreEvidenceOff {
			if seeded != 0 || sections != 0 || t1.Pack {
				t.Fatalf("kapalıyken paket koştu (%d / %d)", seeded, sections)
			}
			continue
		}
		if !t1.Pack || t1.PackOK != 7 || t1.LoopRounds > 1 || seeded == 0 || sections != 1 || t2.Pack {
			t.Fatalf("paket %v/%d tur %d tohum %d bölüm %d t2 %v fails %v", t1.Pack, t1.PackOK, t1.LoopRounds, seeded, sections, t2.Pack, t1.Fails)
		}
		if !strings.Contains(strings.Join(t1.EvidenceBlocks, ","), "trace") {
			t.Fatalf("trace kartı yok: %v (fails %v)", t1.EvidenceBlocks, t1.Fails)
		}
		for _, f := range t1.Fails {
			if strings.Contains(f, "sahte sonuçlarda olmayan sayı") {
				t.Fatalf("paket sayıları kanıtta değil: %v", f)
			}
		}
	}
}
