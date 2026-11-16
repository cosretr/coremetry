package sre

// server_text_golden_test.go — v0.10.2301 (sunucu metinleri S4): inceleme
// kaydına yazılan öneri metni, zaman çizelgesi etiketi ve atlama / veri-yok
// adım özetlerinin DONDURULMUŞ Türkçe matrisi. Altın metin anahtarları
// eklenmeden ÖNCE bugünkü koddan üretildi; bu metinler MCP
// investigation_summary'ye, CoSRE sohbet bloğuna (RenderSavedTR) ve anlatım
// istemine aynı bayt gider. Yalnız bilinçli bir metin değişikliğinde
// -update-inv-golden ile yazılır.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

var updateInvGolden = flag.Bool("update-inv-golden", false, "testdata/inv_text_*.golden.json'ı yeniden yaz")

type invGoldenRow struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Rat  string `json:"rationale,omitempty"`
}

func pf(v float64) *float64 { return &v }

// invGoldenRecs — öneri metninin her dalı (nötr adlar).
func invGoldenRecs() []struct {
	name string
	rec  Recommendation
} {
	type row = struct {
		name string
		rec  Recommendation
	}
	var out []row
	rbFacts := func() *Facts {
		return &Facts{Deploys: &DeployFacts{Rows: []DeployFact{
			{At: start.Add(-12 * time.Minute).UnixNano(), Source: "rollout", Name: "svc-a", Version: "rev 42", Prev: "rev 41"},
		}}}
	}
	hyp := func(kind string) Hypothesis {
		return Hypothesis{Kind: kind, Label: HypothesisLabel(kind), Score: 0.62, Band: BandMedium,
			Checks: []HypothesisCheck{{Name: "x", Outcome: 1, Reason: "destek-1"}}}
	}
	bare := func(kind string) Hypothesis { h := hyp(kind); h.Checks = nil; return h }
	in := func(f *Facts) recInput {
		return recInput{ProblemID: "p-1", Service: "svc-a", StartNs: start.UnixNano(), Facts: f}
	}
	add := func(name string, h Hypothesis, ri recInput) {
		var rb *DeployFact
		if ri.Facts != nil {
			rb = rollbackCandidate(ri.Facts, ri.StartNs)
		}
		if r, ok := hypothesisRec(h, ri, rb); ok {
			out = append(out, row{name, r})
		}
	}
	add("rollback", hyp(HypDeploy), in(rbFacts()))
	add("rollback-bare", bare(HypCodeRegression), in(rbFacts()))
	argo := in(rbFacts())
	argo.ArgoRead, argo.Argo = true, []mcptools.ArgoAppStatus{{App: "app-1", Sync: "OutOfSync", Health: "Degraded", Workloads: []string{"svc-a"}}}
	add("rollback-argo", hyp(HypDeploy), argo)
	argoU := in(rbFacts())
	argoU.ArgoRead, argoU.Argo = true, []mcptools.ArgoAppStatus{{App: "app-1", Health: "Healthy", Workloads: []string{"svc-a"}}}
	add("rollback-argo-unknown", hyp(HypDeploy), argoU)
	ev := func(src string) recInput {
		ri := in(rbFacts())
		ri.Facts.Deploys.Rows[0].Source = src
		return ri
	}
	add("rollback-bare-event", bare(HypDeploy), ev("event"))
	add("rollback-bare-inferred", bare(HypDeploy), ev("inferred"))
	add("deploy", hyp(HypDeploy), in(&Facts{}))
	add("code-regression", hyp(HypCodeRegression), in(&Facts{}))
	add("config", hyp(HypConfig), in(&Facts{}))
	cfgArgo := in(&Facts{})
	cfgArgo.ArgoRead, cfgArgo.Argo = true, []mcptools.ArgoAppStatus{{App: "app-1", Sync: "Synced", Health: "Progressing"}}
	add("config-argo", hyp(HypConfig), cfgArgo)
	add("sat-oom", hyp(HypSaturation), in(&Facts{Pods: &PodFacts{OOMPods: []string{"pod-1", "pod-2"}}}))
	add("sat-hot", hyp(HypSaturation), in(&Facts{Pods: &PodFacts{HotPod: "pod-1", MaxCPUPct: pf(91.4), MaxMemPct: pf(62.5)}}))
	add("sat-hot-nil", hyp(HypSaturation), in(&Facts{Pods: &PodFacts{HotPod: "pod-1", MaxCPUPct: pf(91.4)}}))
	add("sat-jvm", hyp(HypSaturation), in(&Facts{Pods: &PodFacts{HotJVM: "pod-1", MaxPostGCPct: pf(88)}}))
	add("sat-generic", hyp(HypSaturation), in(&Facts{}))
	add("dep", hyp(HypDependency), in(&Facts{Deps: &DepsFacts{Causes: []depsCause{{Service: "svc-b"}}}}))
	add("dep-generic", hyp(HypDependency), in(&Facts{}))
	add("ext", hyp(HypExternal), in(&Facts{Deps: &DepsFacts{Edges: []EdgeFact{{Target: "ext:api.example"}}}}))
	add("ext-generic", hyp(HypExternal), in(&Facts{}))
	add("db", hyp(HypDBSlow), in(&Facts{Deps: &DepsFacts{Edges: []EdgeFact{{Target: "db:db-1"}}}}))
	add("db-generic", hyp(HypDBSlow), in(&Facts{}))
	add("traffic", hyp(HypTrafficSurge), in(&Facts{Deps: &DepsFacts{Subject: &SubjectChange{RateDeltaPct: 245.6}}}))
	add("traffic-generic", hyp(HypTrafficSurge), in(&Facts{}))
	rb := in(&Facts{})
	rb.Problem = chstore.Problem{ID: "p-1", Service: "svc-a", RuleID: "rule-x"}
	rb.Books = []chstore.Runbook{{ID: "rb-1", Title: "Runbook bir", Enabled: true, Labels: []string{"service:svc-a"}}}
	for _, r := range buildRecommendations(rb) {
		out = append(out, row{"runbook:" + r.ID, r})
	}
	return out
}

// invGoldenTimeline — çizelge etiketinin her dalı.
func invGoldenTimeline() []TimelineEvent {
	now := start.Add(2 * time.Hour)
	ev := baseEv(symptomLatency)
	ev.Facts.Problem = &ProblemFacts{RuleName: "rule-1"}
	ev.Facts.Spike = &SpikeFacts{Level: "error", FromMs: start.Add(-4 * time.Minute).UnixMilli(), Factor: 5.25}
	ev.Facts.Deploys = &DeployFacts{Rows: []DeployFact{
		{At: ns(-30 * time.Minute), Source: "rollout", Name: "svc-a", Version: "rev 2", Prev: "rev 1"},
		{At: ns(-20 * time.Minute), Source: "rollout", Name: "svc-a"},
		{At: ns(-10 * time.Minute), Source: "event", Name: "svc-a", Version: "v3", Status: "failed"},
		{At: ns(-5 * time.Minute), Source: "rollout", Name: "svc-a", Status: "degraded"},
	}}
	ev.Facts.Events = &EventFacts{AlertsOK: true, IncidentID: "inc-1", IncidentAt: ns(30 * time.Minute),
		Alerts: []AlertFact{
			{ID: "a-1", RuleName: "rule-2", Priority: "P2", StartedAt: ns(5 * time.Minute), ResolvedAt: ns(25 * time.Minute)},
			{ID: "a-2", RuleID: "rule-3", StartedAt: ns(6 * time.Minute)},
		},
		Related: []AlertFact{{ID: "a-3", Service: "svc-b", RuleName: "rule-4", Priority: "P1", StartedAt: ns(7 * time.Minute)}},
	}
	out := buildTimeline(ev, "p-1", now)
	ev2 := baseEv(symptomLatency)
	ev2.Facts.Spike = &SpikeFacts{Level: "warn", FromMs: start.Add(-4 * time.Minute).UnixMilli(), Factor: 3}
	ev2.Facts.Pivot = &PivotFacts{TraceID: "t", ErrorSpans: 1, StartMs: start.Add(-6 * time.Minute).UnixMilli()}
	out = append(out, buildTimeline(ev2, "p-1", now)...)
	return out
}

// invGoldenSteps — atlama ve veri-yok adım özetleri.
func invGoldenSteps() []Step {
	ctx := context.Background()
	var out []Step
	for _, n := range []string{stepSweep, stepDeploys, stepDeps, stepPivot, stepLogSpike, stepEvents, stepNode, stepBaseline, stepAnomalies, stepErrors, "bogus"} {
		out = append(out, execStep(ctx, readers{}, "svc-a", StepSpec{Name: n}).step)
	}
	out = append(out,
		runAnomalies(ctx, readers{}, anomaliesRequest{Service: "svc-a"}).step,
		runErrors(ctx, readers{}, errorsRequest{Service: "svc-a"}).step)
	empty := readers{
		anomalies: func(context.Context, chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error) {
			return nil, nil
		},
		errorOps: func(context.Context, string, time.Time, time.Time, int) ([]chstore.ErrorRank, error) {
			return nil, nil
		},
		exceptions: func(context.Context, chstore.ExceptionFilter) ([]chstore.ExceptionRow, error) { return nil, nil },
	}
	out = append(out,
		runAnomalies(ctx, empty, anomaliesRequest{Service: "svc-a", From: start, To: start.Add(time.Hour)}).step,
		runErrors(ctx, empty, errorsRequest{Service: "svc-a", From: start, To: start.Add(time.Hour)}).step)
	for _, n := range []Narrator{nil, &fakeNarrator{active: true}, &fakeNarrator{active: true, auto: true, quota: true}, &fakeNarrator{active: true, auto: true}} {
		_, why := narrationGate(n, Settings{MaxLLMCallsPerHour: 20}, 20)
		out = append(out, Step{Name: "gate", Summary: why})
	}
	return out
}

func invGoldenMatrix() []invGoldenRow {
	var out []invGoldenRow
	for _, r := range invGoldenRecs() {
		out = append(out, invGoldenRow{Name: "rec:" + r.name + ":" + r.rec.ID, Text: r.rec.Text, Rat: r.rec.Rationale})
	}
	for _, e := range invGoldenTimeline() {
		out = append(out, invGoldenRow{Name: "tl:" + e.Kind, Text: e.Label})
	}
	for _, s := range invGoldenSteps() {
		out = append(out, invGoldenRow{Name: "step:" + s.Name + ":" + s.State, Text: s.Summary})
	}
	return out
}

func checkInvGolden(t *testing.T, path string, v any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if *updateInvGolden {
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("altın %s değişti:\n- %s\n+ %s", path, want, buf.Bytes())
	}
}

func TestInvestigationTextGolden(t *testing.T) {
	checkInvGolden(t, filepath.Join("testdata", "inv_text_tr.golden.json"), invGoldenMatrix())
}

// invMsgFixture — FE fikstür satırı (frontend/src/features/anomalies/
// invMsg.lang.test.tsx): Türkçe metin + anahtar.
type invMsgFixture struct {
	Name string          `json:"name"`
	TR   string          `json:"tr"`
	Msg  *chstore.StdMsg `json:"msg"`
}

// TestInvestigationTextMessages — v0.10.2301 (S4): her öneri metninin, çizelge
// etiketinin ve atlama / veri-yok özetinin yanında anahtar; kapı satırı
// ("Text — Reason") GateMsg taşır. Fikstür FE testine gider.
func TestInvestigationTextMessages(t *testing.T) {
	var fx []invMsgFixture
	push := func(name, tr string, m *chstore.StdMsg) {
		if m == nil || m.K == "" {
			t.Errorf("%s: anahtar yok (%q)", name, tr)
			return
		}
		fx = append(fx, invMsgFixture{Name: name, TR: tr, Msg: m})
	}
	for _, r := range invGoldenRecs() {
		var m *chstore.StdMsg // aynı JSON şekli (RecTextMsg)
		if r.rec.TextMsg != nil {
			b, _ := json.Marshal(r.rec.TextMsg)
			_ = json.Unmarshal(b, &m)
		}
		push("rec:"+r.name, r.rec.Text, m)
	}
	for i, e := range invGoldenTimeline() {
		push("tl:"+e.Kind+":"+itoa(i), e.Label, e.LabelMsg)
	}
	for i, s := range invGoldenSteps() {
		if s.Name == "gate" {
			continue
		}
		push("step:"+s.Name+":"+itoa(i), s.Summary, s.SummaryMsg)
	}
	for i, n := range []Narrator{nil, &fakeNarrator{active: true}, &fakeNarrator{active: true, auto: true, quota: true}, &fakeNarrator{active: true, auto: true}} {
		_, why, m := narrationGateMsg(n, Settings{MaxLLMCallsPerHour: 20}, 20)
		push("gated:"+itoa(i), NarrationGatedText+" — "+why, chstore.NewStdMsg("inv.gated", "whyM", m))
	}
	push("replay-gated", ReplayNarrationText+" — değerlendirme tekrarı", chstore.NewStdMsg("inv.replayGated"))
	push("replay-step", ReplayNarrationText, chstore.NewStdMsg("step.replayNarration"))
	checkInvGolden(t, filepath.Join("testdata", "inv_text_msgs.golden.json"), fx)
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// invWithMsgs — anahtarlı bir kayıt (öneri + çizelge + adım + kapı).
func invWithMsgs() Investigation {
	recs := invGoldenRecs()
	inv := Investigation{ProblemID: "p-1", Service: "svc-a", Kind: "live", State: StatePartial,
		Steps:    invGoldenSteps()[:3],
		Timeline: invGoldenTimeline(),
		Summary:  &Narration{Text: NarrationGatedText, Gated: true, Reason: "AI yapılandırılmamış ya da kapalı", GateMsg: chstore.NewStdMsg("inv.gated", "whyM", chstore.NewStdMsg("inv.gate.off"))},
	}
	for i := 0; i < RecommendationsMax && i < len(recs); i++ {
		inv.Recommendations = append(inv.Recommendations, recs[i].rec)
	}
	return inv
}

// TestInvestigationBlobTextMsgs — eski kayıt (anahtarsız) çözülür ve anahtarsız
// kalır; yeni kayıt anahtarları gidiş-dönüşte korur; tavanı YALNIZ anahtarlar
// aşıyorsa önce anahtarlar düşer (başka hiçbir alan kaybolmaz); sohbet özeti
// (MCP investigation_summary + CoSRE bloğu) anahtarlardan bağımsız, bayt aynı;
// ack işareti yeniden koşuda taşınır ve anahtar yeni öneriden gelir.
func TestInvestigationBlobTextMsgs(t *testing.T) {
	old := `{"problemId":"p-1","service":"svc-a","kind":"live","state":"done","attempts":0,"run":1,"enqueuedAt":0,"problemStartedAt":0,"anchor":{},` +
		`"steps":[{"name":"deps","state":"skipped","summary":"bağımlılık isteği yok","durationMs":0,"at":0}],"findings":[],` +
		`"timeline":[{"at":1,"kind":"incident_attach","label":"Incident'a bağlandı"}],` +
		`"recommendations":[{"id":"hyp-config","text":"İmaj değişmeden yapılan son yapılandırma / secret değişikliğini gözden geçirin","rationale":"r","state":"pending"}]}`
	var inv Investigation
	if err := json.Unmarshal([]byte(old), &inv); err != nil {
		t.Fatal(err)
	}
	if inv.Steps[0].SummaryMsg != nil || inv.Timeline[0].LabelMsg != nil || inv.Recommendations[0].TextMsg != nil {
		t.Fatal("eski kayıtta anahtar uyduruldu")
	}
	if raw, err := Encode(inv); err != nil || bytes.Contains([]byte(raw), []byte("Msg")) {
		t.Fatalf("eski kayıt yeniden kodlanınca anahtar kazandı: %v %s", err, raw)
	}
	// Yeni kayıt: gidiş-dönüş.
	nw := invWithMsgs()
	raw, err := Encode(nw)
	if err != nil {
		t.Fatal(err)
	}
	var back Investigation
	if err := json.Unmarshal([]byte(raw), &back); err != nil {
		t.Fatal(err)
	}
	if back.Recommendations[0].TextMsg == nil || back.Timeline[0].LabelMsg == nil || back.Steps[0].SummaryMsg == nil || back.Summary.GateMsg == nil {
		t.Fatalf("anahtar gidiş-dönüşte kayboldu: %s", raw)
	}
	// Sohbet özeti anahtarsız kayıtla bayt aynı.
	bare := invWithMsgs()
	dropTextMsgs(&bare)
	a, _ := json.Marshal(SummaryOf(nw))
	b, _ := json.Marshal(SummaryOf(bare))
	if !bytes.Equal(a, b) || RenderSavedTR("p-1", SummaryOf(nw), time.UTC) != RenderSavedTR("p-1", SummaryOf(bare), time.UTC) {
		t.Fatalf("sohbet özeti anahtarlara göre değişti:\n%s\n%s", a, b)
	}
	// Tavan: anahtarsız hâli tam sığacak kadar doldur → anahtarlar düşer, Error aynen.
	bareRaw, _ := Encode(bare)
	pad := chstore.SREInvestigationMaxBytes - len(bareRaw) - 4
	full := invWithMsgs()
	full.Error = strings.Repeat("x", pad)
	bare.Error = full.Error
	gotRaw, err := Encode(full)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, _ := Encode(bare)
	if gotRaw != wantRaw {
		t.Fatalf("tavanda önce anahtarlar düşmeli, başka alan değil (len %d / %d)", len(gotRaw), len(wantRaw))
	}
	// Ack taşıma: işaret önceki kayıttan, anahtar yeni öneriden.
	prev := []Recommendation{{ID: "hyp-config", State: RecAck, AckBy: "op-1", AckAt: 5}}
	next := []Recommendation{{ID: "hyp-config", Text: "t", TextMsg: recMsg("rec.config"), State: RecPending}}
	got := carryRecState(next, prev)
	if got[0].State != RecAck || got[0].AckBy != "op-1" || got[0].TextMsg == nil || got[0].TextMsg.K != "rec.config" {
		t.Fatalf("ack taşıma: %+v", got[0])
	}
}
