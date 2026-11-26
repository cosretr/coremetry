package sre

// plan.go — v0.10.1287 (AI SRE Faz 1b: araştırma orkestratörü). SAF plan:
// problemin şekli → adımlar. I/O yok, saat dışarıdan; tablo testli
// (TestPlanSteps, TestPickExemplar).
//
// Dalga 1 (paralel): sweep (servis taraması + k8s kontrolü; P1'de derin),
// deploys (list_deployments okuyucusu, çıpa = problem başlangıcı), deps
// (topology_edges_5m aşağı akış + korele değişiklikler + blast radius).
// Dalga 2 (paralel): pivot (temsilci trace → pod → JVM → log; yalnız
// belirtisi trace'e inen problemde), log_spike (explain_log_spike; belirti
// error/down ya da plan aileleri logs/exceptions). Ardından TEK anlatım
// çağrısı (summary). Adım adları önbellek anahtarıdır (resume).

import (
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Adım adları (kayıttaki Step.Name; FE etiketleri sreInvestigationView).
const (
	stepSweep    = "sweep"
	stepDeploys  = "deploys"
	stepDeps     = "deps"
	stepPivot    = "pivot"
	stepLogSpike = "log_spike"
	stepSummary  = "summary"
	// v0.10.1288 (Faz 2): olaylar (alarmlar + aşağı akış db:/ext: problemleri +
	// incident bağlanması; dalga 2, bağımlılık adımının kenarlarına bakar) ve
	// TEMBEL node kontrolü (dalga 3; yalnız doygunluk hipotezi isterse).
	stepEvents = "events"
	stepNode   = "node"
	// v0.10.1494 (CoSRE 2d) — Context Pack okuyucuları (yalnız
	// sre_agent.contextpack.enabled ya da paketli değerlendirme tekrarı):
	// hata özeti (dalga 1), baseline + deploy etkisi ve anomaliler (dalga 2:
	// deploy satırları / bağımlılık kenarları dalga 1'den).
	stepErrors    = "errors"
	stepBaseline  = "baseline"
	stepAnomalies = "anomalies"
)

// Adım tavanları (tasarım: dalga 1 15/3/8 s, dalga 2 15/15 s, anlatım 45 s;
// toplam ≤ 90 s iş bütçesi). Tarama ve pivot kendi 15 s bütçeleriyle koşar;
// +2 s evre-2 / kod çözme payı.
const (
	capSweep    = 17 * time.Second
	capDeploys  = 3 * time.Second
	capDeps     = 8 * time.Second
	capPivot    = 17 * time.Second
	capLogSpike = 15 * time.Second
	capSummary  = 45 * time.Second
	capEvents   = 5 * time.Second
	capNode     = 7 * time.Second
	// v0.10.1494 — paket okuyucuları (baseline ≤ 5 okuma × 3 s alt tavan).
	capErrors    = 8 * time.Second
	capBaseline  = 12 * time.Second
	capAnomalies = 5 * time.Second
)

// Belirtiler (anomaly.SymptomPlan sözlüğü).
const (
	symptomError      = "error"
	symptomLatency    = "latency"
	symptomDown       = "down"
	symptomSaturation = "saturation"
)

// StepSpec — planlanan bir okuma adımı. İstek alanlarından YALNIZ adımın
// türüne ait olan dolu.
type StepSpec struct {
	Name  string
	Wave  int
	Cap   time.Duration
	Sweep *mcptools.SweepRequest
	// Deploy adımı: çıpa (problem başlangıcı) + pencere sonu.
	DeployAnchor, DeployTo time.Time
	// Bağımlılık adımı: pencere + korelasyon çıpası.
	Deps *depsRequest
	// Pivot: istek + (temsilci trace kimliği yoksa) rollup exemplar sorgusu.
	Pivot    *mcptools.PivotRequest
	Exemplar *chstore.ExemplarReq
	Spike    *mcptools.LogSpikeRequest
	// v0.10.1288 — olaylar / node adımlarının penceresi.
	Events *eventsRequest
	Node   *nodeRequest
	// v0.10.1494 — Context Pack okuyucularının girdileri.
	Errors    *errorsRequest
	Baseline  *baselineRequest
	Anomalies *anomaliesRequest
	// v0.10.2477 — platformun bildiği adımlar (known.go).
	Known *knownRequest
}

// eventsRequest — olaylar adımının girdisi. Targets (aşağı akış db:/ext:
// düğümleri, ≤3) dalga 2 başında bağımlılık adımının gerçeklerinden dolar.
type eventsRequest struct {
	Service   string
	ProblemID string
	From, To  time.Time
	Targets   []string
}

// nodeRequest — tembel node kontrolünün girdisi.
type nodeRequest struct {
	Service  string
	From, To time.Time
}

// eventsTargetsMax — olaylar adımının okuduğu en çok aşağı akış düğümü.
const eventsTargetsMax = 3

// eventTargets — SAF: aşağı akış db:/ext: düğümleri (çağrı sırası, ≤3).
func eventTargets(df *DepsFacts) []string {
	if df == nil {
		return nil
	}
	var out []string
	for _, e := range df.Edges {
		if (strings.HasPrefix(e.Target, "db:") || strings.HasPrefix(e.Target, "ext:")) && len(out) < eventsTargetsMax {
			out = append(out, e.Target)
		}
	}
	return out
}

// verifyForced — SAF: doğrulama koşusunda hipotez için YENİDEN okunan
// adımlar (+ anlatım). Kalan adımların gerçekleri önceki koşudan gelir.
func verifyForced(kind string) map[string]bool {
	m := map[string]bool{stepSummary: true}
	add := func(ns ...string) {
		for _, n := range ns {
			m[n] = true
		}
	}
	switch kind {
	case HypDeploy:
		add(stepDeploys, stepSweep, stepLogSpike)
	case HypConfig:
		add(stepDeploys, stepEvents)
	case HypSaturation:
		add(stepSweep, stepNode)
	case HypDependency:
		add(stepDeps, stepPivot, stepEvents)
	case HypExternal, HypDBSlow:
		add(stepDeps, stepPivot, stepEvents)
	case HypTrafficSurge:
		add(stepDeps, stepSweep)
	case HypCodeRegression:
		add(stepSweep, stepLogSpike, stepPivot, stepDeploys)
	}
	return m
}

// verifyDeep — SAF: doğrulamada tarama derin mi (desen + kod örneklemi:
// deploy / konfigürasyon / kod gerilemesi hipotezleri yeni desene bakar).
func verifyDeep(kind string) bool {
	return kind == HypDeploy || kind == HypConfig || kind == HypCodeRegression
}

// depsRequest — bağımlılık adımının girdisi.
type depsRequest struct {
	Service   string
	From, To  time.Time
	At        time.Time // korelasyon çıpası (problem başlangıcı)
	WindowSec int
}

// symptomOf — SAF: plan aileleri → belirti (pivot seçimi / log seviyesi).
// anomaly.PlanFamilies sözlüğü: error → exceptions+logs+business; latency →
// saturation+operations+business; down → runtime+logs; saturation →
// saturation+runtime; hacim → boş.
func symptomOf(families []string) string {
	has := map[string]bool{}
	for _, f := range families {
		has[f] = true
	}
	switch {
	case has["exceptions"]:
		return symptomError
	case has["operations"]:
		return symptomLatency
	case has["runtime"] && has["logs"]:
		return symptomDown
	case has["saturation"]:
		return symptomSaturation
	}
	return ""
}

// planInput — planın problem dışı girdileri.
type planInput struct {
	Env      string
	Priority string
	Anchor   Anchor // [başlangıç − 15 dk, min(now, başlangıç + 30 dk)]
	StartNs  int64  // problem başlangıcı (unix ns; 0 → çıpa başı + 15 dk)
	Now      time.Time
	// v0.10.1288 — doğrulama koşusunun hipotezi ("" = normal koşu). Derin
	// okumalar: tarama derin (verifyDeep), pivot / log artışı belirti
	// gerektirmese de eklenir (hipotez onlara bakar).
	Verify    string
	ProblemID string
	// Pack — v0.10.1494: Context Pack açık (nil = kapalı → paket adımları
	// planlanmaz, plan bayt bayt eski).
	Pack *ContextPackSettings
}

// planSteps — SAF: problem + (varsa) sentezleyici hipotezi → adımlar.
// Her zaman: sweep (P1'de derin; deploy kendi adımında olduğu için
// Checks=[k8s]), deploys, deps. pivot: belirti trace'e iniyorsa (error /
// latency / down) ya da hipotezin temsilci trace'i varsa. log_spike: aileler
// logs / exceptions ya da belirti error / down.
func planSteps(p chstore.Problem, hyp *chstore.RootCauseHypothesis, in planInput) []StepSpec {
	fam := anomaly.PlanFamilies(p)
	sym := symptomOf(fam)
	from, to := time.Unix(0, in.Anchor.From).UTC(), time.Unix(0, in.Anchor.To).UTC()
	start := time.Unix(0, in.StartNs).UTC()
	if in.StartNs <= 0 {
		start = from.Add(15 * time.Minute)
	}
	svc := p.Service
	steps := []StepSpec{
		{Name: stepSweep, Wave: 1, Cap: capSweep, Sweep: &mcptools.SweepRequest{
			Service: svc, Env: in.Env, From: from, To: to, Deep: in.Priority == "P1" || verifyDeep(in.Verify),
			Checks: []string{mcptools.SweepCheckK8s}, Anchor: start,
		}},
		{Name: stepDeploys, Wave: 1, Cap: capDeploys, DeployAnchor: start, DeployTo: in.Now.UTC()},
		{Name: stepDeps, Wave: 1, Cap: capDeps, Deps: &depsRequest{Service: svc, From: from, To: to, At: start, WindowSec: depsWindowSec(start, to)}},
	}
	forced := map[string]bool{}
	if in.Verify != "" {
		forced = verifyForced(in.Verify)
	}
	pickSym := sym
	if forced[stepPivot] && pickSym != symptomError && pickSym != symptomDown && pickSym != symptomLatency {
		// doğrulama: belirti trace'e inmese de hipotez kritik yola bakar.
		pickSym = symptomLatency
		if in.Verify == HypCodeRegression {
			pickSym = symptomError
		}
	}
	if pr, ex, ok := pickExemplar(p, hyp, pickSym, in); ok {
		steps = append(steps, StepSpec{Name: stepPivot, Wave: 2, Cap: capPivot, Pivot: &pr, Exemplar: ex})
	}
	logs := sym == symptomError || sym == symptomDown || forced[stepLogSpike]
	for _, f := range fam {
		if f == "logs" || f == "exceptions" {
			logs = true
		}
	}
	if logs {
		level := "all"
		if sym == symptomError || sym == symptomDown {
			level = "error"
		}
		steps = append(steps, StepSpec{Name: stepLogSpike, Wave: 2, Cap: capLogSpike, Spike: &mcptools.LogSpikeRequest{
			Service: svc, Env: in.Env, Level: level, SpikeFrom: from, SpikeTo: to,
		}})
	}
	// v0.10.1288 — olaylar: [başlangıç − 60 dk, şimdi] (zaman çizelgesi + dış
	// servis / DB hipotezleri). Hedefler dalga 2 başında dolar.
	evFrom, evTo := eventsWindow(in.StartNs, in.Now)
	steps = append(steps, StepSpec{Name: stepEvents, Wave: 2, Cap: capEvents, Events: &eventsRequest{
		Service: svc, ProblemID: firstNonEmpty(in.ProblemID, p.ID), From: evFrom, To: evTo,
	}})
	if pk := in.Pack; pk != nil && svc != "" {
		steps = append(steps,
			StepSpec{Name: stepErrors, Wave: 1, Cap: capErrors, Errors: &errorsRequest{Service: svc, From: from, To: to}},
			StepSpec{Name: stepBaseline, Wave: 2, Cap: capBaseline, Baseline: &baselineRequest{Service: svc, Env: in.Env, From: from, To: to,
				Kinds: append([]string(nil), pk.Baselines...), StartNs: start.UnixNano(), Now: in.Now.UTC()}},
			StepSpec{Name: stepAnomalies, Wave: 2, Cap: capAnomalies, Anomalies: &anomaliesRequest{Service: svc, From: from, To: to, TopN: pk.TopAnomalies}},
		)
	}
	return steps
}

// nodeSpec — tembel node adımı (dalga 3; needsNodeCheck karar verir).
func nodeSpec(service string, a Anchor) StepSpec {
	return StepSpec{Name: stepNode, Wave: 3, Cap: capNode, Node: &nodeRequest{
		Service: service, From: time.Unix(0, a.From).UTC(), To: time.Unix(0, a.To).UTC(),
	}}
}

// needsNodeCheck — SAF: doygunluk hipotezi node okumasına ihtiyaç duyuyor mu.
// Doğrulamada (Verify=saturation) her zaman; aksi hâlde pod'lar okunabildiyse
// ve pod kanıtı doygunluğu ZATEN doğrulamıyorsa: belirti gecikme / doygunluk
// (kendi pod'u rahat ama node'u boğan gürültülü komşu) ya da pod kanıtı
// belirsiz. Thanos yoksa (pod bölümü skipped) okuma da boş döner — koşmaz.
func needsNodeCheck(ev Evidence, verify string) bool {
	if verify == HypSaturation {
		return true
	}
	pf := ev.Facts.Pods
	if pf == nil || (pf.PodsState != "ok" && pf.PodsState != "partial") || pf.Pods == 0 {
		return false
	}
	o, _, _ := chkPodLimits(ev)
	if o > 0 {
		return false
	}
	return ev.Symptom == symptomLatency || ev.Symptom == symptomSaturation || o == 0
}

// depsWindowSec — SAF: korelasyon penceresi (başlangıç → pencere sonu, 5..30 dk).
func depsWindowSec(start, to time.Time) int {
	w := int(to.Sub(start).Seconds())
	switch {
	case w < 300:
		return 300
	case w > 1800:
		return 1800
	}
	return w
}

// pickExemplar — SAF: pivotun trace seçimi.
//   - hipotezin temsilci trace'i (ExemplarTraceID) → PivotRequest{TraceID}.
//   - yoksa belirti trace'e iniyorsa (error / latency / down): rollup
//     exemplar sorgusu (spanmetrics_1m argMax durumları, [başlangıç − 5 dk,
//     başlangıç + 15 dk], error belirtisinde hatalı en yavaş, aksi hâlde en
//     yavaş) + yedek PivotRequest{Service, Pick} (exemplar bulunamazsa pivot
//     kendi seçimini yapar).
//   - doygunluk / hacim / belirtisiz → pivot yok (ok=false).
func pickExemplar(p chstore.Problem, hyp *chstore.RootCauseHypothesis, symptom string, in planInput) (mcptools.PivotRequest, *chstore.ExemplarReq, bool) {
	from, to := time.Unix(0, in.Anchor.From).UTC(), time.Unix(0, in.Anchor.To).UTC()
	if hyp != nil && hyp.ExemplarTraceID != "" {
		return mcptools.PivotRequest{TraceID: hyp.ExemplarTraceID, Service: p.Service, From: from, To: to}, nil, true
	}
	if p.Service == "" {
		return mcptools.PivotRequest{}, nil, false
	}
	switch symptom {
	case symptomError, symptomLatency, symptomDown:
	default:
		return mcptools.PivotRequest{}, nil, false
	}
	start := time.Unix(0, in.StartNs).UTC()
	if in.StartNs <= 0 {
		start = from.Add(15 * time.Minute)
	}
	exTo := start.Add(15 * time.Minute)
	if !in.Now.IsZero() && exTo.After(in.Now) {
		exTo = in.Now.UTC()
	}
	kind, pick := chstore.ExemplarSlow, "slowest"
	if symptom == symptomError || symptom == symptomDown {
		kind, pick = chstore.ExemplarError, "slowest_error"
	}
	ex := &chstore.ExemplarReq{Service: p.Service, From: start.Add(-5 * time.Minute), To: exTo, Kind: kind}
	return mcptools.PivotRequest{Service: p.Service, Env: in.Env, Pick: pick, From: from, To: to}, ex, true
}
