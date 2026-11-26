package sre

// v0.10.1287 — AI SRE Faz 1b: araştırma orkestratörü (tarama + deploy + k8s +
// bağımlılık + pivot + log artışı + özet).

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

var t0 = time.Date(2026, 10, 17, 14, 20, 0, 0, time.UTC)
var start = time.Date(2026, 10, 17, 14, 5, 0, 0, time.UTC)

func names(specs []StepSpec) string {
	var out []string
	for _, s := range specs {
		out = append(out, s.Name)
	}
	return strings.Join(out, ",")
}

func planIn(prio string) planInput {
	return planInput{Env: "prod", Priority: prio, Anchor: anchorFor(start.UnixNano(), t0), StartNs: start.UnixNano(), Now: t0}
}

func TestPlanSteps(t *testing.T) {
	for _, c := range []struct {
		name     string
		p        chstore.Problem
		hyp      *chstore.RootCauseHypothesis
		prio     string
		want     string
		deep     bool
		pick     string
		level    string
		exemplar chstore.ExemplarKind
	}{
		{"hata P1 → derin tarama + hatalı pivot + ERROR log artışı", chstore.Problem{Service: "checkout", Metric: "error_rate"}, nil, "P1",
			"sweep,deploys,deps,pivot,log_spike,events", true, "slowest_error", "error", chstore.ExemplarError},
		{"gecikme P2 → en yavaş pivot, log artışı yok", chstore.Problem{Service: "checkout", Metric: "p99"}, nil, "P2",
			"sweep,deploys,deps,pivot,events", false, "slowest", "", chstore.ExemplarSlow},
		{"ayakta değil → hatalı pivot + ERROR log", chstore.Problem{Service: "checkout", Metric: "down"}, nil, "P1",
			"sweep,deploys,deps,pivot,log_spike,events", true, "slowest_error", "error", chstore.ExemplarError},
		{"exception kuralı (metrik yok) → hata planı", chstore.Problem{Service: "checkout", RuleName: "Exception storm"}, nil, "P2",
			"sweep,deploys,deps,pivot,log_spike,events", false, "slowest_error", "error", chstore.ExemplarError},
		{"doygunluk → pivot yok, log yok", chstore.Problem{Service: "checkout", Metric: "heap"}, nil, "P2",
			"sweep,deploys,deps,events", false, "", "", ""},
		{"hacim → yalnız dalga 1", chstore.Problem{Service: "checkout", Metric: "request_rate"}, nil, "P2",
			"sweep,deploys,deps,events", false, "", "", ""},
		{"hipotezin temsilci trace'i → pivot trace ile, exemplar okuması yok", chstore.Problem{Service: "checkout", Metric: "heap"},
			&chstore.RootCauseHypothesis{ExemplarTraceID: "abc"}, "P2", "sweep,deploys,deps,pivot,events", false, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			specs := planSteps(c.p, c.hyp, planIn(c.prio))
			if got := names(specs); got != c.want {
				t.Fatalf("adımlar %s, beklenen %s", got, c.want)
			}
			for _, s := range specs {
				switch s.Name {
				case stepSweep:
					if s.Wave != 1 || s.Sweep.Deep != c.deep || s.Sweep.Service != "checkout" || s.Sweep.Env != "prod" ||
						len(s.Sweep.Checks) != 1 || s.Sweep.Checks[0] != mcptools.SweepCheckK8s || !s.Sweep.Anchor.Equal(start) {
						t.Fatalf("sweep: %+v", s.Sweep)
					}
					if !s.Sweep.From.Equal(start.Add(-15*time.Minute)) || !s.Sweep.To.Equal(t0) {
						t.Fatalf("sweep penceresi %v–%v", s.Sweep.From, s.Sweep.To)
					}
				case stepDeploys:
					if s.Wave != 1 || !s.DeployAnchor.Equal(start) || !s.DeployTo.Equal(t0) || s.Cap != capDeploys {
						t.Fatalf("deploys: %+v", s)
					}
				case stepDeps:
					if s.Wave != 1 || s.Deps.Service != "checkout" || !s.Deps.At.Equal(start) || s.Deps.WindowSec != 900 {
						t.Fatalf("deps: %+v", s.Deps)
					}
				case stepPivot:
					if s.Wave != 2 {
						t.Fatal("pivot dalga 2")
					}
					if c.hyp != nil {
						if s.Pivot.TraceID != "abc" || s.Exemplar != nil {
							t.Fatalf("hipotez trace'i: %+v %+v", s.Pivot, s.Exemplar)
						}
						continue
					}
					if s.Pivot.Pick != c.pick || s.Exemplar == nil || s.Exemplar.Kind != c.exemplar {
						t.Fatalf("pivot: %+v %+v", s.Pivot, s.Exemplar)
					}
				case stepLogSpike:
					if s.Wave != 2 || s.Spike.Level != c.level || !s.Spike.SpikeFrom.Equal(start.Add(-15*time.Minute)) || !s.Spike.SpikeTo.Equal(t0) {
						t.Fatalf("log_spike: %+v", s.Spike)
					}
				case stepEvents: // v0.10.1288 — [başlangıç − 60 dk, şimdi], dalga 2
					if s.Wave != 2 || s.Events.Service != "checkout" || !s.Events.From.Equal(start.Add(-60*time.Minute)) || !s.Events.To.Equal(t0) || s.Cap != capEvents {
						t.Fatalf("events: %+v", s.Events)
					}
				}
			}
		})
	}
}

func TestPickExemplar(t *testing.T) {
	p := chstore.Problem{Service: "checkout", Metric: "error_rate"}
	// Başlangıç + 15 dk now'u aşıyor → now'a kırpılır.
	pr, ex, ok := pickExemplar(p, nil, symptomError, planIn("P1"))
	if !ok || ex == nil || !ex.From.Equal(start.Add(-5*time.Minute)) || !ex.To.Equal(t0) || ex.Kind != chstore.ExemplarError || ex.Service != "checkout" {
		t.Fatalf("exemplar: %+v", ex)
	}
	if pr.Service != "checkout" || pr.Pick != "slowest_error" || pr.TraceID != "" || pr.Env != "prod" {
		t.Fatalf("yedek pivot: %+v", pr)
	}
	// Geçmiş problem: [başlangıç − 5, başlangıç + 15] tam.
	in := planIn("P1")
	in.Now = start.Add(2 * time.Hour)
	_, ex, _ = pickExemplar(p, nil, symptomLatency, in)
	if !ex.To.Equal(start.Add(15*time.Minute)) || ex.Kind != chstore.ExemplarSlow {
		t.Fatalf("geçmiş pencere: %+v", ex)
	}
	if _, _, ok := pickExemplar(p, nil, symptomSaturation, planIn("P1")); ok {
		t.Fatal("doygunlukta pivot yok")
	}
	if _, _, ok := pickExemplar(chstore.Problem{}, nil, symptomError, planIn("P1")); ok {
		t.Fatal("servissiz pivot yok")
	}
	pr, ex, ok = pickExemplar(p, &chstore.RootCauseHypothesis{ExemplarTraceID: "t1"}, "", planIn("P1"))
	if !ok || ex != nil || pr.TraceID != "t1" {
		t.Fatalf("hipotez trace'i öncelikli: %+v %+v", pr, ex)
	}
}

// ── sahte okuyucular ────────────────────────────────────────────────────────

type fakeNarrator struct {
	mu                         sync.Mutex
	active, auto, quota        bool
	calls                      int
	surface, user, sys, answer string
	err                        error
	callID, exchangeID         string // v0.10.1312 — ctx'te sabitlenen ai_calls / exchange kimliği
}

func (n *fakeNarrator) Active() bool             { return n.active }
func (n *fakeNarrator) AutoExplainEnabled() bool { return n.auto }
func (n *fakeNarrator) QuotaBackoffActive() bool { return n.quota }
func (n *fakeNarrator) ActiveModel() string      { return "gemma-test" }
func (n *fakeNarrator) Explain(ctx context.Context, sys, user string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	n.surface = copilot.MetaFromContext(ctx).Surface
	n.callID, n.exchangeID = copilot.CallIDFromContext(ctx), copilot.MetaFromContext(ctx).ExchangeID
	n.sys, n.user = sys, user
	return n.answer, n.err
}

type calls struct {
	mu sync.Mutex
	n  map[string]int
}

func (c *calls) hit(k string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == nil {
		c.n = map[string]int{}
	}
	c.n[k]++
}
func (c *calls) get(k string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n[k]
}

func okReaders(c *calls, n Narrator) readers {
	return readers{
		problem: func(context.Context, string) (*chstore.Problem, error) {
			c.hit("problem")
			return &chstore.Problem{ID: "p1", Service: "checkout", Metric: "error_rate", RuleName: "Error rate", StartedAt: start.UnixNano()}, nil
		},
		hypothesis: func(context.Context, string) (*chstore.RootCauseHypothesis, error) {
			c.hit("hypothesis")
			return &chstore.RootCauseHypothesis{TopSuspect: "payments-db", Confidence: 0.58, ComputedAt: start.UnixNano()}, nil
		},
		sweep: func(_ context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error) {
			c.hit("sweep")
			return mcptools.SweepResult{OK: true, Verdict: mcptools.SweepVerdict{Label: "Sorunlu", Reasons: []string{"POST /pay hata %9"}},
				Findings: []mcptools.SweepFinding{{Kind: "operation", Name: "POST /pay", Level: "crit", Reasons: []string{"hata %9"}, EvidenceRefs: []string{"evidences[0]"}}}}, nil
		},
		deploys: func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
			c.hit("deploys")
			return mcptools.DeployCheck{Section: mcptools.SweepSection{Name: "deploys", State: "ok", Summary: "1 değişiklik"},
				Findings: []mcptools.SweepFinding{{Kind: "deploy", Name: "checkout@rev 42", Level: "warn", Reasons: []string{"başlangıçtan 12 dk önce rollout"}}}}, nil
		},
		graph: func(context.Context, string, time.Time, time.Time) ([]chstore.ServiceEdge, error) {
			c.hit("graph")
			return []chstore.ServiceEdge{{Source: "checkout", Target: "payments-db", CallCount: 100}}, nil
		},
		changes: func(context.Context, time.Time, int) ([]chstore.ChangedService, error) {
			c.hit("changes")
			return []chstore.ChangedService{{Service: "payments-db", Direction: chstore.ChangeWorse, Reasons: []string{"p99 +180%"}}}, nil
		},
		blast: func(_ context.Context, svcs []string, _, _ time.Time) (map[string]chstore.BlastRadius, error) {
			c.hit("blast")
			return map[string]chstore.BlastRadius{svcs[0]: {Service: svcs[0], TotalCallers: 2}}, nil
		},
		exemplar: func(context.Context, chstore.ExemplarReq) (*chstore.Exemplar, error) {
			c.hit("exemplar")
			return &chstore.Exemplar{TraceID: "deadbeefdeadbeefdeadbeefdeadbeef"}, nil
		},
		pivot: func(_ context.Context, req mcptools.PivotRequest) (mcptools.PivotResult, error) {
			c.hit("pivot:" + req.TraceID)
			return mcptools.PivotResult{OK: true, Message: "trace okundu"}, nil
		},
		spike: func(context.Context, mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
			c.hit("spike")
			return mcptools.LogSpikeResult{OK: true, Message: "belirgin artış yok"}, nil
		},
		events: func(_ context.Context, svc string, _, _ time.Time, _ int) (mcptools.ProblemWindowData, error) {
			c.hit("events:" + svc)
			return mcptools.ProblemWindowData{Rows: []mcptools.ProblemWindowRow{
				{ID: "p1", RuleID: "r", RuleName: "Error rate", Service: "checkout", StartedAtNs: start.UnixNano()},
				{ID: "p0", RuleID: "anomaly:checkout:p99_ms", RuleName: "p99 anomaly", Service: "checkout", Priority: "P2", StartedAtNs: start.Add(-5 * time.Minute).UnixNano()},
			}}, nil
		},
		incident: func(context.Context, string) (string, int64, error) {
			c.hit("incident")
			return "inc-7", start.Add(2 * time.Minute).UnixNano(), nil
		},
		narrator: n,
		budget:   newLLMBudget(nil, func() time.Time { return t0 }),
		settings: func() Settings { return NormalizeSettings(DefaultSettings()) },
	}
}

func newInv() *Investigation {
	return &Investigation{ProblemID: "p1", DisplayID: "P-1042", Service: "checkout", Env: "prod", Priority: "P1", Kind: KindAuto,
		State: StateRunning, ProblemAt: start.UnixNano()}
}

func TestRunInvestigationFull(t *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: checkout hata üretiyor.\nKanıt:\n- POST /pay"}
	inv := newInv()
	cps := 0
	runInvestigation(context.Background(), okReaders(c, n), inv, func(*Investigation) { cps++ }, func() time.Time { return t0 })
	if got := len(inv.Steps); got != 7 {
		t.Fatalf("7 adım (6 okuma + özet) bekleniyordu: %d %+v", got, inv.Steps)
	}
	if cps != 8 {
		t.Fatalf("her adımda + skordan sonra checkpoint: %d", cps)
	}
	// v0.10.1288 — 8 hipotez kartı (skor sıralı), zaman çizelgesi, node okuması
	// YOK (pod verisi yok → doygunluk node'a ihtiyaç duymuyor).
	if len(inv.Hypotheses) != len(hypothesisTable) || inv.Hypotheses[0].Score < inv.Hypotheses[1].Score || inv.step(stepNode) != nil {
		t.Fatalf("hipotezler: %+v", inv.Hypotheses)
	}
	if len(inv.Timeline) < 3 || inv.Timeline[0].At > inv.Timeline[len(inv.Timeline)-1].At || c.get("incident") != 1 {
		t.Fatalf("zaman çizelgesi: %+v", inv.Timeline)
	}
	if !strings.Contains(n.user, "AJAN HİPOTEZİ") {
		t.Fatalf("anlatım girdisi en olası hipotezi taşımalı: %s", n.user)
	}
	if finalState(inv.Steps) != StateDone {
		t.Fatalf("durum %s: %+v", finalState(inv.Steps), inv.Steps)
	}
	if c.get("pivot:deadbeefdeadbeefdeadbeefdeadbeef") != 1 {
		t.Fatalf("pivot rollup exemplar trace'iyle koşmalı: %v", c.n)
	}
	if inv.SynthRef == nil || inv.SynthRef.TopSuspect != "payments-db" {
		t.Fatalf("sentezleyici kopyası: %+v", inv.SynthRef)
	}
	if n.calls != 1 || n.surface != NarrationSurface || inv.Summary == nil || inv.Summary.Gated || inv.Summary.Model != "gemma-test" {
		t.Fatalf("tek anlatım çağrısı: calls=%d surface=%q summary=%+v", n.calls, n.surface, inv.Summary)
	}
	if !strings.Contains(n.user, "SENTEZLEYİCİ: şüpheli payments-db (güven %58)") || n.sys != copilot.SystemPromptSREInvestigation() {
		t.Fatalf("anlatım girdisi: %s", n.user)
	}
	// Bulgular: crit operasyon > warn deploy > warn bağımlılık; adım etiketli.
	if len(inv.Findings) != 3 || inv.Findings[0].Name != "POST /pay" || inv.Findings[1].Kind != "deploy" || inv.Findings[2].Kind != "dependency" ||
		inv.Findings[2].Step != stepDeps || inv.Findings[0].Step != stepSweep {
		t.Fatalf("bulgu sırası: %+v", inv.Findings)
	}
	if inv.Anchor.Symptom != symptomError || len(inv.Anchor.Families) == 0 {
		t.Fatalf("çıpa: %+v", inv.Anchor)
	}
	if _, err := Encode(*inv); err != nil {
		t.Fatal(err)
	}
}

// TestRunInvestigationPartial — adım hataları koşuyu KESMEZ: hata / bütçe
// aşan adım kendi durumunu yazar, kalanlar koşar, sonuç partial.
func TestRunInvestigationPartial(t *testing.T) {
	c := &calls{}
	rd := okReaders(c, nil) // anlatıcı yok → kapı kapalı
	rd.deploys = func(context.Context, string, time.Time, time.Time) (mcptools.DeployCheck, error) {
		return mcptools.DeployCheck{}, context.DeadlineExceeded
	}
	rd.graph = func(context.Context, string, time.Time, time.Time) ([]chstore.ServiceEdge, error) {
		return nil, errors.New("ch: connection refused")
	}
	rd.spike = func(context.Context, mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
		return mcptools.LogSpikeResult{OK: false, Error: nil, Message: "log arka ucu bağlı değil"}, nil
	}
	inv := newInv()
	runInvestigation(context.Background(), rd, inv, func(*Investigation) {}, time.Now)
	st := map[string]string{}
	for _, s := range inv.Steps {
		st[s.Name] = s.State
	}
	if st[stepSweep] != StepOK || st[stepDeploys] != StepError || st[stepDeps] != StepPartial || st[stepLogSpike] != StepError ||
		st[stepPivot] != StepOK || st[stepSummary] != StepSkipped {
		t.Fatalf("adım durumları: %v", st)
	}
	if finalState(inv.Steps) != StatePartial {
		t.Fatalf("koşu partial olmalı: %s", finalState(inv.Steps))
	}
	if inv.Summary == nil || inv.Summary.Text != NarrationGatedText || !inv.Summary.Gated {
		t.Fatalf("kapalı kapı özeti: %+v", inv.Summary)
	}
	// Problem satırı okunamadı → kayıttaki servis ve başlangıçla plan: belirti
	// bilinmez, yalnız dalga 1 (tarama yine koşar).
	rd.problem = func(context.Context, string) (*chstore.Problem, error) { return nil, errors.New("problems okunamadı") }
	inv2 := newInv()
	runInvestigation(context.Background(), rd, inv2, func(*Investigation) {}, time.Now)
	if c.get("sweep") != 2 || inv2.step(stepPivot) != nil || inv2.step(stepSweep).State != StepOK {
		t.Fatalf("problem okunamasa da tarama koşmalı: %+v", inv2.Steps)
	}
	// Topoloji okunamadı → kenarsız "olası neden" YOK.
	for _, f := range inv.Findings {
		if f.Kind == "dependency" {
			t.Fatalf("topoloji yokken bağımlılık nedeni üretildi: %+v", f)
		}
	}
}

// TestRunInvestigationBudget — iş bütçesi dolarsa dönmeyen adım "süre
// bütçesi doldu" yazar, koordinatör takılmaz.
func TestRunInvestigationBudget(t *testing.T) {
	c := &calls{}
	rd := okReaders(c, nil)
	block := make(chan struct{})
	defer close(block)
	rd.sweep = func(context.Context, mcptools.SweepRequest) (mcptools.SweepResult, error) {
		<-block // ctx'i DİNLEMEYEN okuyucu
		return mcptools.SweepResult{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	inv := newInv()
	done := make(chan struct{})
	go func() {
		runInvestigation(ctx, rd, inv, func(*Investigation) {}, time.Now)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("koordinatör asılı okuyucuda takıldı")
	}
	if s := inv.step(stepSweep); s == nil || s.State != StepError || !strings.Contains(s.Summary, "süre bütçesi") {
		t.Fatalf("sweep: %+v", s)
	}
}

func TestStepResume(t *testing.T) {
	c := &calls{}
	n := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: x"}
	rd := okReaders(c, n)
	now := time.Now()
	inv := newInv()
	inv.Steps = []Step{
		{Name: stepSweep, State: StepOK, At: now.Add(-time.Minute).UnixNano(), Summary: "taze"},
		{Name: stepDeploys, State: StepOK, At: now.Add(-20 * time.Minute).UnixNano()}, // bayat
		{Name: stepDeps, State: StepError, At: now.Add(-time.Minute).UnixNano()},      // hata → yeniden
		{Name: stepSummary, State: StepOK, At: now.Add(-time.Minute).UnixNano()},
	}
	inv.Summary = &Narration{Text: "eski özet", At: now.Add(-time.Minute).UnixNano()}
	inv.Findings = []Finding{{Kind: "pod", Name: "checkout-1", Level: "crit", Step: stepSweep}}
	inv.Facts = &Facts{Pods: &PodFacts{PodsState: "ok", Pods: 3}} // v0.10.1288 — taze adımın gerçekleri kayıtta
	runInvestigation(context.Background(), rd, inv, func(*Investigation) {}, time.Now)
	if c.get("sweep") != 0 {
		t.Fatal("taze ok tarama yeniden koştu")
	}
	if c.get("deploys") != 1 || c.get("graph") != 1 {
		t.Fatalf("bayat / hatalı adım yeniden koşmalı: %v", c.n)
	}
	if n.calls != 0 || inv.Summary.Text != "eski özet" {
		t.Fatalf("taze özet yeniden üretildi: calls=%d", n.calls)
	}
	if inv.Findings[0].Name != "checkout-1" {
		t.Fatalf("atlanan adımın bulgusu korunmalı: %+v", inv.Findings)
	}
	if s := inv.step(stepSweep); s.Summary != "taze" {
		t.Fatalf("atlanan adım değişti: %+v", s)
	}
}

func TestInvestigationLLMGate(t *testing.T) {
	cfg := NormalizeSettings(DefaultSettings())
	for _, c := range []struct {
		name string
		n    Narrator
		used int
		ok   bool
		why  string
	}{
		{"anlatıcı yok", nil, 0, false, "yapılandırılmamış"},
		{"kapalı", &fakeNarrator{active: false, auto: true}, 0, false, "yapılandırılmamış"},
		{"otomatik AI kapalı", &fakeNarrator{active: true, auto: false}, 0, false, "otomatik AI"},
		{"kota beklemesi", &fakeNarrator{active: true, auto: true, quota: true}, 0, false, "kota"},
		{"saatlik tavan", &fakeNarrator{active: true, auto: true}, cfg.MaxLLMCallsPerHour, false, "saatlik"},
		{"açık", &fakeNarrator{active: true, auto: true}, cfg.MaxLLMCallsPerHour - 1, true, ""},
	} {
		ok, why := narrationGate(c.n, cfg, c.used)
		if ok != c.ok || !strings.Contains(why, c.why) {
			t.Errorf("%s: %v %q", c.name, ok, why)
		}
	}
	// Kapı kapalı: çağrı YOK, sabit cümle.
	n := &fakeNarrator{active: true, auto: false, answer: "x"}
	rd := okReaders(&calls{}, n)
	inv := newInv()
	st := narrate(context.Background(), rd, inv, chstore.Problem{Service: "checkout"}, time.Now)
	if n.calls != 0 || st.State != StepSkipped || inv.Summary.Text != "AI özeti yok (otomatik AI kapalı)" {
		t.Fatalf("kapalı kapı: calls=%d %+v %+v", n.calls, st, inv.Summary)
	}
	// Saatlik sayaç: tavan 1 → ikinci araştırma kapıda kalır.
	n2 := &fakeNarrator{active: true, auto: true, answer: "Ne oldu: y"}
	rd2 := okReaders(&calls{}, n2)
	rd2.settings = func() Settings { s := DefaultSettings(); s.MaxLLMCallsPerHour = 1; return NormalizeSettings(s) }
	if st := narrate(context.Background(), rd2, newInv(), chstore.Problem{}, time.Now); st.State != StepOK {
		t.Fatalf("ilk çağrı: %+v", st)
	}
	inv2 := newInv()
	if st := narrate(context.Background(), rd2, inv2, chstore.Problem{}, time.Now); st.State != StepSkipped || !strings.Contains(inv2.Summary.Reason, "saatlik") {
		t.Fatalf("tavan: %+v %+v", st, inv2.Summary)
	}
	if n2.calls != 1 {
		t.Fatalf("tavan sonrası çağrı yapıldı: %d", n2.calls)
	}
	// Sağlayıcı hatası → adım error, özet yok (uydurma yok).
	n3 := &fakeNarrator{active: true, auto: true, err: errors.New("429 quota")}
	inv3 := newInv()
	if st := narrate(context.Background(), okReaders(&calls{}, n3), inv3, chstore.Problem{}, time.Now); st.State != StepError || inv3.Summary != nil {
		t.Fatalf("hata: %+v %+v", st, inv3.Summary)
	}
}

func TestRenderInvestigationTRGolden(t *testing.T) {
	inv := Investigation{ProblemID: "p1", DisplayID: "P-1042", Service: "checkout", Env: "prod", Priority: "P1",
		ProblemAt: start.UnixNano(), Anchor: Anchor{From: start.Add(-15 * time.Minute).UnixNano(), To: start.Add(30 * time.Minute).UnixNano(), Symptom: "error"},
		Steps: []Step{
			{Name: stepSweep, State: StepOK, Summary: "Sorunlu: POST /pay hata %9 (önceki %0,5)"},
			{Name: stepDeploys, State: StepOK, Summary: "3 değişiklik (son 2 sa); başlangıca yakın: checkout@rev 42"},
			{Name: stepDeps, State: StepPartial, Summary: "topoloji okunamadı; olası neden olabilecek korele bağımlılık yok"},
			{Name: stepPivot, State: StepOK, Summary: "POST /pay — 2300 ms, 41 span (3 hatalı)"},
			{Name: stepLogSpike, State: StepError, Summary: "log arka ucu bağlı değil"},
			{Name: stepSummary, State: StepOK, Summary: "eski"},
		},
		Findings: []Finding{
			{Kind: "deploy", Name: "checkout@rev 42", Level: "crit", Reasons: []string{"başlangıçtan 12 dk önce rollout (rev 42←rev 41)", "rollout stalled"}},
			{Kind: "operation", Name: "POST /pay", Level: "crit", Reasons: []string{"hata oranı %9\n(önceki %0,5)"}},
		},
		SynthRef: &SynthRef{TopSuspect: "payments-db", Confidence: 0.58},
	}
	p := chstore.Problem{RuleName: "Error rate > 5%", Metric: "error_rate", Value: 9, Threshold: 5}
	got := renderInvestigationTR(inv, p, time.UTC)
	want := `PROBLEM: P-1042 · servis=checkout · env=prod · öncelik=P1 · belirti=error
KURAL: Error rate > 5% · error_rate=9 (eşik 5)
BAŞLANGIÇ: 14:05 UTC · pencere 13:50–14:35 UTC
ADIMLAR:
- servis taraması: tamam — Sorunlu: POST /pay hata %9 (önceki %0,5)
- deploy: tamam — 3 değişiklik (son 2 sa); başlangıca yakın: checkout@rev 42
- bağımlılık: kısmi — topoloji okunamadı; olası neden olabilecek korele bağımlılık yok
- trace pivotu: tamam — POST /pay — 2300 ms, 41 span (3 hatalı)
- log artışı: OKUNAMADI — log arka ucu bağlı değil
SENTEZLEYİCİ: şüpheli payments-db (güven %58)
BULGULAR (sunucu sırası):
1. [crit] deploy checkout@rev 42 — başlangıçtan 12 dk önce rollout (rev 42←rev 41); rollout stalled
2. [crit] operasyon POST /pay — hata oranı %9 (önceki %0,5)
`
	if got != want {
		t.Fatalf("golden farkı:\n--- got\n%s\n--- want\n%s", got, want)
	}
	// Tavan: 4 KB, rune sınırında.
	for i := 0; i < 8; i++ {
		inv.Findings = append(inv.Findings, Finding{Kind: "log", Name: strings.Repeat("ğ", 200), Level: "warn", Reasons: []string{strings.Repeat("ş", 400)}})
	}
	for i := range inv.Steps {
		inv.Steps[i].Summary = strings.Repeat("ç", 500)
	}
	big := renderInvestigationTR(inv, p, time.UTC)
	if len(big) > renderMaxBytes || !strings.HasSuffix(big, "…\n") || !utf8Valid(big) {
		t.Fatalf("tavan: %d B", len(big))
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "\uFFFD") == s }

func TestMergeFindings(t *testing.T) {
	got := mergeFindings(map[string][]Finding{
		stepSweep:    {{Kind: "pod", Name: "a", Level: "warn"}, {Kind: "operation", Name: "op", Level: "crit"}},
		stepDeploys:  {{Kind: "deploy", Name: "d", Level: "warn"}},
		stepPivot:    {{Kind: "pod", Name: "a", Level: "warn"}, {Kind: "jvm", Name: "a", Level: "crit"}},
		stepLogSpike: {{Kind: "log", Name: "pod=a", Level: "warn"}},
		stepDeps:     {{Kind: "dependency", Name: "db", Level: "warn"}},
	})
	var order []string
	for _, f := range got {
		order = append(order, f.Step+":"+f.Name)
	}
	want := "pivot:a,sweep:op,deploys:d,sweep:a,deps:db,log_spike:pod=a"
	if strings.Join(order, ",") != want {
		t.Fatalf("sıra %v, beklenen %s", order, want)
	}
	many := map[string][]Finding{}
	for i := 0; i < 20; i++ {
		many[stepSweep] = append(many[stepSweep], Finding{Kind: "pod", Name: string(rune('a' + i)), Level: "warn"})
	}
	if len(mergeFindings(many)) != maxFindings {
		t.Fatal("tavan 8")
	}
}

func TestDepsStep(t *testing.T) {
	req := depsRequest{Service: "checkout", From: start, To: t0, At: start, WindowSec: 900}
	res := depsResult{
		edges: []chstore.ServiceEdge{{Source: "checkout", Target: "payments-db"}, {Source: "web", Target: "checkout"}},
		changes: []chstore.ChangedService{
			{Service: "payments-db", Direction: chstore.ChangeWorse, Reasons: []string{"p99 +180%"}},
			{Service: "web", Direction: chstore.ChangeWorse, Reasons: []string{"hata +3"}}, // çağıranın kötüleşmesi SONUÇTUR
			{Service: "unrelated", Direction: chstore.ChangeWorse},                         // kenarsız
		},
		blast: &chstore.BlastRadius{TotalCallers: 1, CascadingCallers: 1, Callers: []chstore.BlastRadiusCaller{{Service: "web", HasOpenProblem: true}}},
	}
	st, f := depsStep(req, res)
	if st.State != StepOK || len(f) != 1 || f[0].Name != "payments-db" || !strings.Contains(f[0].Reasons[0], "aşağı akış") ||
		!strings.Contains(f[0].Reasons[0], "nedensellik değil") {
		t.Fatalf("bağımlılık: %+v %+v", st, f)
	}
	if !strings.Contains(st.Summary, "1 aşağı akış bağımlılığı") || !strings.Contains(st.Summary, "1 çağıran (1'inde açık problem)") {
		t.Fatalf("özet: %s", st.Summary)
	}
	res.edgeErr = errors.New("x")
	st, f = depsStep(req, res)
	if st.State != StepPartial || len(f) != 0 {
		t.Fatalf("topolojisiz neden yok: %+v %+v", st, f)
	}
	st, _ = depsStep(req, depsResult{edgeErr: errors.New("a"), chgErr: errors.New("b"), blstErr: errors.New("c")})
	if st.State != StepError {
		t.Fatalf("hepsi okunamadı: %+v", st)
	}
}

func TestSummaryOfAndRenderSaved(t *testing.T) {
	inv := Investigation{ProblemID: "p1", State: StateDone, FinishedAt: start.UnixNano(),
		Findings: []Finding{{Kind: "operation", Name: "POST /pay", Level: "crit", Reasons: []string{"hata %9"}}, {Kind: "pod", Name: "a", Level: "warn"},
			{Kind: "k8s", Name: "b", Level: "warn"}, {Kind: "log", Name: "c", Level: "warn"}},
		Summary: &Narration{Text: "Ne oldu: x"}}
	s := SummaryOf(inv)
	if s.At != start.UnixNano() || len(s.Findings) != 3 || s.Findings[0] != "[crit] operasyon POST /pay: hata %9" || s.Summary != "Ne oldu: x" {
		t.Fatalf("özet: %+v", s)
	}
	inv.Summary.Gated = true
	if SummaryOf(inv).Summary != "" {
		t.Fatal("kapalı kapı cümlesi sohbete kanıt gibi gitmemeli")
	}
	txt := RenderSavedTR("P-1042", s, time.UTC)
	if !strings.HasPrefix(txt, "KAYITLI ARAŞTIRMA (14:05)") || !strings.Contains(txt, "ÖNERİDİR") || !strings.Contains(txt, "- [crit] operasyon POST /pay") {
		t.Fatalf("blok: %s", txt)
	}
	s.State = StateRunning
	if RenderSavedTR("x", s, time.UTC) != "" {
		t.Fatal("bitmemiş kayıt bloğu yok")
	}
	// Paylaşılan kayıt kaynağın bulgularını taşır.
	st := newFakeStore()
	src := inv
	src.Summary = &Narration{Text: "kaynak"}
	_ = save(context.Background(), st, src)
	_ = save(context.Background(), st, Investigation{ProblemID: "p2", State: StateShared, SharedFrom: "p1"})
	got, err := LoadSummary(context.Background(), st, "p2")
	if err != nil || got == nil || got.State != StateShared || got.SharedFrom != "p1" || got.Summary != "kaynak" {
		t.Fatalf("paylaşılan: %+v %v", got, err)
	}
	if none, _ := LoadSummary(context.Background(), st, "p9"); none != nil {
		t.Fatal("kayıt yokken özet yok")
	}
}

// TestOrchestratorReadOnly — orkestratörün mağaza okuyucuları yalnız
// Get/Find/List/Count; problems / hipotez / öncelik yazıcısı yok (öneri ajanı).
// v0.10.1288 — + Enrich*: chstore'un OKUMA zenginleştirmeleri
// (EnrichProblemsForRead: deploy → öncelik, satıra yazmaz; terfi Problem'inin
// bölüm alanları deploy hipotezinin "düzenli yinelenme" çürütmesine girer).
// v0.10.1293 — + EnrichProblemsWithRunbooks (Faz 4 öneri okuması: kural →
// servis kataloğu runbook bağlantısı, kopya dilimde; satıra yazmaz). Paket
// geneli kardeşi TestSRENoMutatingCalls (guardrails_test.go).
func TestOrchestratorReadOnly(t *testing.T) {
	fset := token.NewFileSet()
	af, err := parser.ParseFile(fset, "orchestrator.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	ast.Inspect(af, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "st" {
			seen++
			name := sel.Sel.Name
			if !(strings.HasPrefix(name, "Get") || strings.HasPrefix(name, "Find") || strings.HasPrefix(name, "List") || strings.HasPrefix(name, "Count") ||
				strings.HasPrefix(name, "EnrichProblemsFor") || name == "EnrichProblemsWithRunbooks" ||
				// v0.10.1494 (CoSRE 2d) — Context Pack okuyucuları: MV pencere RED'i,
				// MV hata sıralaması, yayınlanmış (atomic) duyarlılık ayarı — hepsi okuma.
				name == "ServiceWindowRED" || name == "TopErrorOperations" || name == "AnomalySensitivity" ||
				// v0.10.2477 — platformun bildiği (known.go): üye olaylar, vaka
				// hafızası, exception kodları — hepsi sınırlı okuma.
				name == "ProblemEvents" || name == "SimilarIncidentCases" || name == "TopExceptionCodesForService") {
				t.Errorf("%s: st.%s — orkestratör yalnız okur", fset.Position(sel.Pos()), name)
			}
		}
		return true
	})
	if seen < 5 {
		t.Fatalf("yalnız %d mağaza okuyucusu bulundu — tarama bozuk", seen)
	}
}
