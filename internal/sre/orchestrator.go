package sre

// orchestrator.go — v0.10.1287 (AI SRE Faz 1b: araştırma orkestratörü —
// tarama + deploy + k8s + bağımlılık + pivot + log artışı + özet).
//
// Akış (iş bütçesi ItemBudgetSec, varsayılan 90 s; drain'in bctx'i):
//
//	0. problem satırı + sentezleyici hipotezi (salt-okunur, 3 s) → plan
//	1. DALGA 1 paralel: sweep 17 s · deploys 3 s · deps 8 s
//	2. DALGA 2 paralel: pivot 17 s (rollup exemplar → trace) · log_spike 15 s
//	3. TEK anlatım çağrısı (summary, ≤45 s, arka plan AI kapıları)
//
// Kurallar:
//   - Her adım bitince kayıt yazılır (checkpoint, koordinatör goroutine'inden).
//   - Başarısız adım yalnız kendi durumunu yazar; koşu ASLA kesilmez (kısmi).
//   - Resume: 10 dk'dan taze ok/skipped adım yeniden koşmaz (bulguları kayıtta).
//   - Okumalar sohbetle AYNI çekirdekler ve AYNI önbellek anahtarları
//     (ReadServiceSweepCached / RunTracePivotCached / RunLogSpike → Deps.Memo):
//     drain'in sonucu sohbetin önbelleğini tohumlar; kısmi sonuç saklanmaz.
//   - YALNIZ ÖNERİ: tek yazım kendi kaydı (QueueStore). problems /
//     root_cause_hypotheses / öncelik okunur, ASLA yazılmaz.

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrerules"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/cache"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	readCap       = 3 * time.Second
	waveStragglar = 2 * time.Second // bütçe dolduktan sonra adımlara tanınan ek süre
	depsGraphTopN = 20
)

// readers — orkestratörün okumaları (test dikişi). nil alan = okuyucu yok →
// adım dürüst "skipped/error".
type readers struct {
	problem    func(ctx context.Context, id string) (*chstore.Problem, error)
	hypothesis func(ctx context.Context, id string) (*chstore.RootCauseHypothesis, error)
	sweep      func(ctx context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error)
	deploys    func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error)
	graph      func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error)
	changes    func(ctx context.Context, at time.Time, windowSec int) ([]chstore.ChangedService, error)
	blast      func(ctx context.Context, services []string, from, to time.Time) (map[string]chstore.BlastRadius, error)
	exemplar   func(ctx context.Context, req chstore.ExemplarReq) (*chstore.Exemplar, error)
	pivot      func(ctx context.Context, req mcptools.PivotRequest) (mcptools.PivotResult, error)
	spike      func(ctx context.Context, req mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error)
	// v0.10.1288 (Faz 2) — okuma zenginleştirmesi (deploy → öncelik; terfi
	// Problem'inin bölüm alanları), olaylar (ReadProblemWindowEvents), incident
	// bağlanması, tembel node kontrolü.
	enrich   func(ctx context.Context, probs []chstore.Problem) []chstore.Problem
	events   func(ctx context.Context, service string, from, to time.Time, limit int) (mcptools.ProblemWindowData, error)
	incident func(ctx context.Context, problemID string) (string, int64, error)
	node     func(ctx context.Context, service string, from, to time.Time) (mcptools.NodeCheck, error)
	narrator Narrator
	budget   *llmBudget
	settings func() Settings
	// rules — v0.10.1289: operatör kuralları (nil → chstore.CurrentCosreRules).
	// YALNIZ anlatım girdisine (narrate.go); skor / plan / kontrol okumaz
	// (TestRulesNeverReachScoring).
	rules func() []cosrerules.Rule
	// v0.10.1293 (Faz 4) — öneri okumaları (recommend.go): etkin runbook
	// listesi, problemin runbook bağlantısı (EnrichProblemsWithRunbooks: kural →
	// servis kataloğu; OKUMA zenginleştirmesi, satıra yazmaz) ve TEMBEL Argo
	// durumu (Deps.ArgoApps). v0.10.1310: koşu başına TEK okuma, skordan önce
	// Facts.Argo'ya (argoFactsFor) — argo_sync kontrolü + öneri eki ortak.
	runbooks   func(ctx context.Context) ([]chstore.Runbook, error)
	runbookURL func(ctx context.Context, p chstore.Problem) string
	argo       func(ctx context.Context, service string) ([]mcptools.ArgoAppStatus, error)
	// v0.10.1494 (CoSRE 2d) — Context Pack okuyucuları (readers_*.go) ve
	// bayrak/bütçe. pack nil → CurrentContextPackSettings (varsayılan kapalı).
	redMetric   mcptools.ServiceMetricREDReader
	redSpan     func(ctx context.Context, service string, from, to time.Time) (chstore.ServiceWindowRED, error)
	anomalies   func(ctx context.Context, f chstore.ListAnomalyEventsFilter) ([]chstore.AnomalyEvent, error)
	errorOps    func(ctx context.Context, service string, from, to time.Time, limit int) ([]chstore.ErrorRank, error)
	exceptions  func(ctx context.Context, f chstore.ExceptionFilter) ([]chstore.ExceptionRow, error)
	sensitivity func() chstore.AnomalySensitivityConfig
	pack        func() ContextPackSettings
}

// readersFrom — üretim okuyucuları: srv.MCPDeps() (sohbetle aynı kurucu).
func readersFrom(d mcptools.Deps, n Narrator, c cache.Cache, now func() time.Time) readers {
	r := readers{
		sweep: func(ctx context.Context, req mcptools.SweepRequest) (mcptools.SweepResult, error) {
			return mcptools.ReadServiceSweepCached(ctx, d, req)
		},
		deploys: func(ctx context.Context, service string, anchor, to time.Time) (mcptools.DeployCheck, error) {
			return mcptools.ReadDeployCheck(ctx, d, service, anchor, to)
		},
		pivot: func(ctx context.Context, req mcptools.PivotRequest) (mcptools.PivotResult, error) {
			return mcptools.RunTracePivotCached(ctx, d, req)
		},
		spike: func(ctx context.Context, req mcptools.LogSpikeRequest) (mcptools.LogSpikeResult, error) {
			return mcptools.RunLogSpike(ctx, d, req)
		},
		node: func(ctx context.Context, service string, from, to time.Time) (mcptools.NodeCheck, error) {
			return mcptools.ReadNodeCheck(ctx, d, service, from, to)
		},
		narrator: n,
		budget:   newLLMBudget(c, now),
		settings: CurrentSettings,
		// v0.10.1494 — paket okuyucuları: metrik RED (Overview metrik kipi;
		// VM-first, örneklenmemiş) + bayrak.
		redMetric: d.ServiceMetricRED,
		pack:      CurrentContextPackSettings,
	}
	if st := d.Store; st != nil {
		// v0.10.1494 — paket okuyucuları (hepsi okuma; MV-first ya da sınırlı).
		r.redSpan = st.ServiceWindowRED
		r.anomalies = st.ListAnomalyFeed // v0.10.2081 — ML olayları da (aynı satır şekli)
		r.errorOps = st.TopErrorOperations
		r.exceptions = st.GetExceptions
		r.sensitivity = st.AnomalySensitivity
		r.problem = st.GetProblem
		r.hypothesis = func(ctx context.Context, id string) (*chstore.RootCauseHypothesis, error) {
			return st.GetHypothesis(ctx, "problem", id)
		}
		// Aşağı akış: topology_edges_5m (GetServiceGraphTopN) — ServiceNeighbors
		// DEĞİL (ham spans).
		r.graph = func(ctx context.Context, service string, from, to time.Time) ([]chstore.ServiceEdge, error) {
			return st.GetServiceGraphTopN(ctx, service, 0, from, to, depsGraphTopN)
		}
		r.changes = func(ctx context.Context, at time.Time, windowSec int) ([]chstore.ChangedService, error) {
			return st.GetCorrelatedChangesMVTop(ctx, at, windowSec, windowSec*4, chstore.ChangedServicesMarkPool)
		}
		r.blast = st.GetBlastRadiusBatch
		r.exemplar = st.FindExemplarRollup
		// v0.10.1288 — OKUMA zenginleştirmesi (yazım yok; chstore
		// "EnrichProblemsForRead": deploy → öncelik), olaylar (sohbetin
		// list_problem_window_events okuması) ve incident bağlanması.
		r.enrich = func(ctx context.Context, probs []chstore.Problem) []chstore.Problem {
			return st.EnrichProblemsForRead(ctx, probs, problemDeployLookback)
		}
		r.events = func(ctx context.Context, service string, from, to time.Time, limit int) (mcptools.ProblemWindowData, error) {
			return mcptools.ReadProblemWindowEvents(ctx, d, service, from, to, limit)
		}
		r.incident = st.GetProblemIncident
		// v0.10.1293 — öneri okumaları (yalnız Get/List/okuma zenginleştirmesi).
		r.runbooks = st.ListRunbooks
		r.runbookURL = func(ctx context.Context, p chstore.Problem) string {
			if ps := st.EnrichProblemsWithRunbooks(ctx, []chstore.Problem{p}); len(ps) == 1 {
				return ps[0].RunbookURL
			}
			return ""
		}
	}
	r.argo = d.ArgoApps // v0.10.1293 — nil = Argo eki yok
	return r
}

// ReplayNarrationText — tekrar işinin özet metni (anlatım çağrısı yapılmaz).
const ReplayNarrationText = "AI özeti yok (değerlendirme tekrarı — puanlama anlatım kullanmaz)"

// replayPlanNow — SAF: tekrarın planlama anı. AsOf yoksa (eski satır)
// başlangıç + replayAsOfDelay; hiçbir zaman duvar saatinden ileri değil.
func replayPlanNow(inv Investigation, wall time.Time) time.Time {
	at := time.Unix(0, inv.AsOf)
	if inv.AsOf <= 0 && inv.ProblemAt > 0 {
		at = time.Unix(0, inv.ProblemAt).Add(replayAsOfDelay)
	}
	if inv.AsOf <= 0 && inv.ProblemAt <= 0 || at.After(wall) {
		return wall
	}
	return at
}

// problemDeployLookback — okuma zenginleştirmesinin deploy penceresi (api
// problem_enrich.go / mcptools guided_parity.go ile aynı 30 dk).
const problemDeployLookback = 30 * time.Minute

// stepOutcome — bir adımın kayda girecek çıktısı.
type stepOutcome struct {
	step     Step
	findings []Finding
	warnings []string
	facts    *Facts // v0.10.1288 — adımın yapısal gerçekleri (skor girdisi)
}

// hasFacts — SAF: adımın gerçek parçası kayıtta var mı (1b kaydında yok →
// adım yeniden koşar; skor eksik girdiyle hesaplanmasın).
func hasFacts(f *Facts, step string) bool {
	if step == stepSummary {
		return true
	}
	if f == nil {
		return false
	}
	switch step {
	case stepSweep:
		return f.Pods != nil
	case stepDeploys:
		return f.Deploys != nil
	case stepDeps:
		return f.Deps != nil
	case stepPivot:
		return f.Pivot != nil
	case stepLogSpike:
		return f.Spike != nil
	case stepEvents:
		return f.Events != nil
	case stepNode:
		return f.Node != nil
	case stepBaseline:
		return f.Baseline != nil
	case stepAnomalies:
		return f.Anomalies != nil
	case stepErrors:
		return f.Errors != nil
	}
	return false
}

// fresh — SAF: adım bu koşuda yeniden koşmadan kullanılabilir mi.
func fresh(s *Step, now time.Time) bool {
	return s != nil && (s.State == StepOK || s.State == StepSkipped) && now.Sub(time.Unix(0, s.At)) < stepFreshForReuse
}

// runInvestigation — tam plan. checkpoint her adım bitince çağrılır.
func runInvestigation(ctx context.Context, rd readers, inv *Investigation, checkpoint func(*Investigation), now func() time.Time) {
	t0 := now()
	p := chstore.Problem{ID: inv.ProblemID, Service: inv.Service, StartedAt: inv.ProblemAt}
	if rd.problem != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if got, err := rd.problem(c, inv.ProblemID); err == nil && got != nil && got.ID != "" {
			p = *got
			if p.Service == "" {
				p.Service = inv.Service
			}
			if rd.enrich != nil { // okuma zenginleştirmesi: terfi Problem'inin bölüm alanları
				if ps := rd.enrich(c, []chstore.Problem{p}); len(ps) == 1 {
					p = ps[0]
				}
			}
		}
		cancel()
	}
	var hyp *chstore.RootCauseHypothesis
	if rd.hypothesis != nil {
		c, cancel := context.WithTimeout(ctx, readCap)
		if h, err := rd.hypothesis(c, inv.ProblemID); err == nil && h != nil {
			hyp = h
			inv.SynthRef = &SynthRef{TopSuspect: h.TopSuspect, Confidence: h.Confidence, ComputedAt: h.ComputedAt}
		}
		cancel()
	}
	// v0.10.1301 (Faz 5) — tekrar (Kind=replay): planın "şimdi"si AsOf
	// (problem başlangıcı + replayAsOfDelay), duvar saati DEĞİL — pencereler
	// problemin StartedAt'ine çıpalanır, bugünün verisi tekrara sızmaz. Adım
	// süreleri / tazelik yine duvar saatiyle (t0).
	replay := inv.Kind == KindReplay
	planNow := t0
	if replay {
		planNow = replayPlanNow(*inv, t0)
	}
	inv.Anchor = anchorFor(inv.ProblemAt, planNow)
	verify := ""
	if inv.Kind == KindVerify && IsHypothesisKind(inv.Hypothesis) {
		verify = inv.Hypothesis
	}
	// v0.10.1494 (CoSRE 2d) — Context Pack: canlı koşuda bayraktan, tekrarda
	// koşu isteğinden (inv.Pack, enqueue'da) — bayrak tekrarı DEĞİŞTİRMEZ.
	pk := packFor(rd, inv)
	if !replay {
		inv.Pack = pk != nil
	}
	specs := planSteps(p, hyp, planInput{Env: inv.Env, Priority: inv.Priority, Anchor: inv.Anchor, StartNs: inv.ProblemAt, Now: planNow,
		Verify: verify, ProblemID: inv.ProblemID, Pack: pk})
	inv.Anchor.Families = anomaly.PlanFamilies(p)
	inv.Anchor.Symptom = symptomOf(inv.Anchor.Families)
	if inv.Facts == nil {
		inv.Facts = &Facts{}
	}
	inv.Facts.Problem = problemFactsOf(p)
	forced := map[string]bool{}
	if verify != "" {
		forced = verifyForced(verify)
	}
	// reuse — adım bu koşuda yeniden okunmaz: normal koşuda 10 dk'dan taze
	// ok/skipped; doğrulamada hipotezin ZORLADIĞI adımlar dışında önceki
	// koşunun (hatasız) adımları. Gerçek parçası olmayan adım (1b kaydı) koşar.
	reuse := func(name string) bool {
		prev := inv.step(name)
		if prev == nil || !hasFacts(inv.Facts, name) {
			return false
		}
		if verify != "" {
			return !forced[name] && prev.State != StepError
		}
		return fresh(prev, t0)
	}

	groups := groupFindings(inv.Findings)
	apply := func(o stepOutcome) {
		inv.setStep(o.step)
		groups[o.step.Name] = o.findings
		if o.step.Name == stepSweep && o.step.State != StepError {
			inv.Warnings = o.warnings
		}
		inv.Facts.merge(o.facts)
		inv.Findings = mergeFindings(groups)
		checkpoint(inv)
	}
	for _, wave := range []int{1, 2} {
		var batch []StepSpec
		for _, s := range specs {
			if s.Wave == wave && !reuse(s.Name) {
				if s.Name == stepEvents && s.Events != nil {
					er := *s.Events
					er.Targets = eventTargets(inv.Facts.Deps)
					s.Events = &er
				}
				if s.Name == stepBaseline && s.Baseline != nil && inv.Facts.Deploys != nil {
					br := *s.Baseline
					br.Deploys = append([]DeployFact(nil), inv.Facts.Deploys.Rows...)
					s.Baseline = &br
				}
				if s.Name == stepAnomalies && s.Anomalies != nil {
					ar := *s.Anomalies
					ar.Neighbors = neighborTargets(inv.Facts.Deps, inv.Service)
					s.Anomalies = &ar
				}
				batch = append(batch, s)
			}
		}
		runWave(ctx, rd, inv.Service, batch, now, apply)
	}
	// Dalga 3 — TEMBEL node kontrolü: yalnız doygunluk hipotezi isterse.
	if !reuse(stepNode) && ctx.Err() == nil && needsNodeCheck(evidenceFrom(inv), verify) {
		runWave(ctx, rd, inv.Service, []StepSpec{nodeSpec(inv.Service, inv.Anchor)}, now, apply)
	}
	// v0.10.1310 — Argo CD gerçeği: koşu başına EN ÇOK BİR Deps.ArgoApps
	// okuması, skordan ÖNCE (argo_sync + öneri eki aynı parçayı okur). Tekrarda
	// okuma yok → State=replay (Argo durumu "şimdi", as-of değil).
	inv.Facts.Argo = argoFactsFor(ctx, rd, inv.Facts.Argo, inv.Service, replay, verify, now)
	// Hipotezler + zaman çizelgesi: SAF, okuma yok; operatör işaretleri taşınır.
	ev := evidenceFrom(inv)
	inv.Hypotheses = carryHuman(Score(ev), inv.Hypotheses)
	inv.ContextPack = packMetaFor(*inv, p, pk) // v0.10.1494 — künye (metin değil); kapalı → nil
	if replay {
		inv.Timeline = buildTimeline(ev, inv.ProblemID, planNow)
		// v0.10.1301 — tekrar: öneri YOK (bugünün runbook / Argo durumunu
		// okurdu; puanlama yalnız hipotez sırasına bakar) ve anlatım YOK:
		// skor LLM'den bağımsız (Score SAF), çağrı yalnız saatlik AI bütçesini
		// (sre_agent.maxLLMCallsPerHour) canlı araştırmaların elinden alırdı.
		// Sıfır LLM çağrısı → bütçeye 0 düşer.
		checkpoint(inv)
		if !fresh(inv.step(stepSummary), t0) {
			inv.Summary = &Narration{Text: ReplayNarrationText, At: now().UnixNano(), Gated: true, Reason: "değerlendirme tekrarı"}
			apply(stepOutcome{step: Step{Name: stepSummary, State: StepSkipped, Summary: ReplayNarrationText, At: now().UnixNano()}})
		}
		return
	}
	if inv.FirstHypothesisAt == 0 && len(inv.Hypotheses) > 0 {
		inv.FirstHypothesisAt = now().UnixNano() // v0.10.1301 — ilk hipoteze varış (değerlendirme p50/p90)
	}
	inv.Timeline = buildTimeline(ev, inv.ProblemID, now())
	// v0.10.1293 (Faz 4) — öneriler: YALNIZ METİN; anlatımdan ÖNCE (girdisine
	// ≤2 öneri girer). Operatör işaretleri kimliğe göre taşınır.
	inv.Recommendations = carryRecState(recommend(ctx, rd, inv, p), inv.Recommendations)
	inv.ContextPack = packMetaFor(*inv, p, pk) // öneriler pakete girer: künye öneriden SONRA
	checkpoint(inv)
	if forced[stepSummary] || !fresh(inv.step(stepSummary), t0) || inv.Summary == nil {
		// v0.10.1463 (D1) — anlatım sre.narrate span'ı içinde (trace.go).
		st := narrateTraced(ctx, func(nctx context.Context) Step { return narrate(nctx, rd, inv, p, now) }, inv)
		apply(stepOutcome{step: st, findings: groups[stepSummary]})
	}
}

// runWave — dalganın adımları paralel; sonuçlar koordinatörde (apply) sırayla.
// Bütçe dolarsa dönmeyen adım "süre bütçesi doldu" hatası yazar.
func runWave(ctx context.Context, rd readers, service string, batch []StepSpec, now func() time.Time, apply func(stepOutcome)) {
	if len(batch) == 0 {
		return
	}
	ch := make(chan stepOutcome, len(batch))
	for _, s := range batch {
		go func(s StepSpec) {
			sctx, cancel := context.WithTimeout(ctx, s.Cap)
			defer cancel()
			sctx, endSpan := beginStepSpan(sctx, s.Name) // v0.10.1463 (D1) — sre.step
			t := now()
			o := execStep(sctx, rd, service, s)
			end := now()
			o.step.Name = s.Name
			o.step.DurationMs = end.Sub(t).Milliseconds()
			o.step.At = end.UnixNano()
			endSpan(o, end.Sub(t))
			ch <- o
		}(s)
	}
	done := map[string]bool{}
	ctxDone := ctx.Done()
	var straggle <-chan time.Time // nil = bütçe sürüyor (bloklar)
	for len(done) < len(batch) {
		select {
		case o := <-ch:
			if !done[o.step.Name] {
				done[o.step.Name] = true
				apply(o)
			}
		case <-ctxDone:
			ctxDone, straggle = nil, time.After(waveStragglar)
		case <-straggle:
			for _, s := range batch {
				if !done[s.Name] {
					done[s.Name] = true
					apply(stepOutcome{step: Step{Name: s.Name, State: StepError, Summary: "süre bütçesi doldu", At: now().UnixNano()}})
				}
			}
		}
	}
}

// execStep — tek adımın okuması (adım goroutine'i; kayda DOKUNMAZ).
func execStep(ctx context.Context, rd readers, service string, s StepSpec) stepOutcome {
	switch s.Name {
	case stepSweep:
		if rd.sweep == nil || s.Sweep == nil {
			return skippedOutcome(s.Name, "tarama okuyucusu bağlı değil")
		}
		res, err := rd.sweep(ctx, *s.Sweep)
		st, f, w := sweepStep(res, err)
		o := stepOutcome{step: st, findings: f, warnings: w}
		if err == nil && res.OK {
			o.facts = &Facts{Pods: podFactsOf(res)}
		}
		return o
	case stepDeploys:
		if rd.deploys == nil {
			return skippedOutcome(s.Name, "deploy okuyucusu bağlı değil")
		}
		dc, err := rd.deploys(ctx, service, s.DeployAnchor, s.DeployTo)
		ds, f := deployStep(dc, err)
		o := stepOutcome{step: ds, findings: f}
		if err == nil && ds.State != StepError {
			o.facts = &Facts{Deploys: deployFactsOf(dc)}
		}
		return o
	case stepDeps:
		if s.Deps == nil {
			return skippedOutcome(s.Name, "bağımlılık isteği yok")
		}
		st, f, df := runDeps(ctx, rd, *s.Deps)
		return stepOutcome{step: st, findings: f, facts: &Facts{Deps: df}}
	case stepPivot:
		if rd.pivot == nil || s.Pivot == nil {
			return skippedOutcome(s.Name, "pivot okuyucusu bağlı değil")
		}
		req := *s.Pivot
		via := "hipotezin temsilci trace'i"
		if req.TraceID == "" {
			via = "pivotun kendi seçimi"
			if s.Exemplar != nil && rd.exemplar != nil {
				c, cancel := context.WithTimeout(ctx, readCap)
				if ex, err := rd.exemplar(c, *s.Exemplar); err == nil && ex != nil && ex.TraceID != "" {
					req.TraceID, via = ex.TraceID, "rollup exemplar ("+string(s.Exemplar.Kind)+")"
				}
				cancel()
			}
		}
		res, err := rd.pivot(ctx, req)
		st, f := pivotStep(res, err, via)
		o := stepOutcome{step: st, findings: f}
		if err == nil && res.OK && res.Error == nil {
			o.facts = &Facts{Pivot: pivotFactsOf(res)}
		}
		return o
	case stepLogSpike:
		if rd.spike == nil || s.Spike == nil {
			return skippedOutcome(s.Name, "log okuyucusu bağlı değil")
		}
		res, err := rd.spike(ctx, *s.Spike)
		st, f := spikeStep(res, err)
		o := stepOutcome{step: st, findings: f}
		if err == nil && res.OK && res.Error == nil {
			o.facts = &Facts{Spike: spikeFactsOf(s.Spike, res)}
		}
		return o
	case stepEvents:
		if rd.events == nil || s.Events == nil {
			return skippedOutcome(s.Name, "olay okuyucusu bağlı değil")
		}
		return runEvents(ctx, rd, *s.Events)
	case stepNode:
		if rd.node == nil || s.Node == nil {
			return skippedOutcome(s.Name, "node okuyucusu bağlı değil")
		}
		nc, err := rd.node(ctx, s.Node.Service, s.Node.From, s.Node.To)
		st, f := nodeStep(nc, err)
		o := stepOutcome{step: st, findings: f}
		if err == nil {
			o.facts = &Facts{Node: nodeFactsOf(nc)}
		}
		return o
	case stepBaseline:
		if s.Baseline == nil {
			return skippedOutcome(s.Name, "baseline isteği yok")
		}
		return runBaseline(ctx, rd, *s.Baseline)
	case stepAnomalies:
		if s.Anomalies == nil {
			return skippedOutcome(s.Name, "anomali isteği yok")
		}
		return runAnomalies(ctx, rd, *s.Anomalies)
	case stepErrors:
		if s.Errors == nil {
			return skippedOutcome(s.Name, "hata özeti isteği yok")
		}
		return runErrors(ctx, rd, *s.Errors)
	}
	return skippedOutcome(s.Name, "bilinmeyen adım")
}

func skippedOutcome(name, why string) stepOutcome {
	return stepOutcome{step: Step{Name: name, State: StepSkipped, Summary: why}}
}

// groupFindings — SAF: kayıttaki bulgular → adım başına (0a kaydı: Step boş = sweep).
func groupFindings(in []Finding) map[string][]Finding {
	out := map[string][]Finding{}
	for _, f := range in {
		k := f.Step
		if k == "" {
			k = stepSweep
			f.Step = stepSweep
		}
		out[k] = append(out[k], f)
	}
	return out
}

// findingKindRank — eşit seviyede tür sırası (tarama sırası korunur: k8s >
// deploy > pod > JVM > operasyon > fonksiyon; ardından bağımlılık, pivotun
// türleri, log artışı).
var findingKindRank = map[string]int{
	"k8s": 0, "deploy": 1, "pod": 2, "jvm": 3, "operation": 4, "function": 5,
	"dependency": 6, "trace": 7, "log": 8, "node": 9, "baseline": 10,
}

// stepRank — aynı tür + seviyede adım sırası.
var stepRank = map[string]int{stepSweep: 0, stepDeploys: 1, stepDeps: 2, stepPivot: 3, stepLogSpike: 4, stepEvents: 5, stepNode: 6,
	stepErrors: 7, stepBaseline: 8, stepAnomalies: 9}

// mergeFindings — SAF: adım bulguları → kayıt bulguları (≤8). Sıra: seviye
// (crit > warn > diğer) → tür → adım → adımın kendi sırası. Aynı (tür, ad)
// iki adımdan gelirse ilki kalır (ör. tarama ve pivot aynı pod'u bulur).
func mergeFindings(groups map[string][]Finding) []Finding {
	type it struct {
		f   Finding
		pos int
	}
	var all []it
	for step, fs := range groups {
		for i, f := range fs {
			f.Step = step
			all = append(all, it{f, i})
		}
	}
	lvl := func(l string) int {
		switch l {
		case "crit":
			return 0
		case "warn":
			return 1
		}
		return 2
	}
	kind := func(k string) int {
		if r, ok := findingKindRank[k]; ok {
			return r
		}
		return 9
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if lvl(a.f.Level) != lvl(b.f.Level) {
			return lvl(a.f.Level) < lvl(b.f.Level)
		}
		if kind(a.f.Kind) != kind(b.f.Kind) {
			return kind(a.f.Kind) < kind(b.f.Kind)
		}
		if stepRank[a.f.Step] != stepRank[b.f.Step] {
			return stepRank[a.f.Step] < stepRank[b.f.Step]
		}
		return a.pos < b.pos
	})
	out := []Finding{}
	seen := map[string]bool{}
	for _, x := range all {
		if len(out) >= maxFindings {
			break
		}
		k := x.f.Kind + "\x00" + x.f.Name
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, x.f)
	}
	return out
}

// llmBudget — saatlik AI çağrı sayacı (sre_agent.MaxLLMCallsPerHour). Redis
// (pod'lar arası, Get/Set — cache arayüzünde INCR yok) + bellek; ikisinin
// büyüğü. Yarış pod'lar arasında ±1 sapabilir (bilinçli: tavan koruma, fatura
// değil).
type llmBudget struct {
	mu    sync.Mutex
	cache cache.Cache
	now   func() time.Time
	key   string
	mem   int
}

const llmHourPrefix = "coremetry:sre:llm:hour:"

func newLLMBudget(c cache.Cache, now func() time.Time) *llmBudget {
	if now == nil {
		now = time.Now
	}
	return &llmBudget{cache: c, now: now}
}

func (b *llmBudget) hourKey() string { return llmHourPrefix + b.now().UTC().Format("2006010215") }

// used — bu saatin çağrı sayısı.
func (b *llmBudget) used(ctx context.Context) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.usedLocked(ctx)
}

func (b *llmBudget) usedLocked(ctx context.Context) int {
	k := b.hourKey()
	if k != b.key {
		b.key, b.mem = k, 0
	}
	n := b.mem
	if b.cache != nil {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if raw, ok, err := b.cache.Get(c, k); err == nil && ok && len(raw) > 0 {
			if v, err := strconv.Atoi(string(raw)); err == nil && v > n {
				n = v
			}
		}
	}
	return n
}

// inc — bir çağrı say (çağrıdan ÖNCE: başarısız çağrı da kotadan yer).
func (b *llmBudget) inc(ctx context.Context) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.usedLocked(ctx) + 1
	b.mem = n
	if b.cache != nil {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = b.cache.Set(c, b.key, []byte(strconv.Itoa(n)), 2*time.Hour)
	}
}
