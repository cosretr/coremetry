package sre

// facts.go — v0.10.1288 (AI SRE Faz 2: hipotez kartları + zaman çizelgesi).
//
// Faz 1b adımlarının TOPLADIĞI yapısal gerçekler — hipotez skoru (Score) ve
// zaman çizelgesi (buildTimeline) yalnız bunları okur, yeni okuma yapmaz
// (tek istisna tembel node kontrolü ve olaylar adımı; ikisi de kendi adımı).
// Her parça adımın SAF dönüştürücüsünde kurulur ve kayıtta saklanır: resume'da
// taze adım yeniden koşmasa da skor aynı girdiden yeniden hesaplanır;
// doğrulama koşusu (Kind=verify) yalnız hipotezin adımlarını yeniler, kalan
// gerçekler önceki koşudan gelir. Tavanlar küçük (kayıt ≤64 KB).
//
// v0.10.1310 — Argo parçası adım DEĞİL: koşu başına TEK Deps.ArgoApps okuması
// (recommend.go argoFactsFor) skordan ÖNCE buraya yazılır; argo_sync kontrolü
// ve öneri eki aynı parçayı okur. Tekrarda (Kind=replay) okuma yok, parça
// State=replay (Argo durumu "şimdi"dir — geçmiş olayı puanlamaz).

import (
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

// Facts — kayıttaki gerçekler (adım başına bir parça; nil = adım koşmadı).
type Facts struct {
	Problem *ProblemFacts `json:"problem,omitempty"`
	Deploys *DeployFacts  `json:"deploys,omitempty"`
	Pods    *PodFacts     `json:"pods,omitempty"`
	Deps    *DepsFacts    `json:"deps,omitempty"`
	Pivot   *PivotFacts   `json:"pivot,omitempty"`
	Spike   *SpikeFacts   `json:"spike,omitempty"`
	Events  *EventFacts   `json:"events,omitempty"`
	Node    *NodeFacts    `json:"node,omitempty"`
	Argo    *ArgoFacts    `json:"argo,omitempty"` // v0.10.1310
	// v0.10.1494 (CoSRE 2d) — Context Pack okuyucuları (yalnız bayrak açıkken;
	// Score okumaz — anlatım paketi + değerlendirme künyesi).
	Baseline  *BaselineFacts `json:"baseline,omitempty"`
	Anomalies *AnomalyFacts  `json:"anomalies,omitempty"`
	Errors    *ErrorFacts    `json:"errors,omitempty"`
}

// merge — SAF: src'nin dolu parçaları dst'ye.
func (f *Facts) merge(src *Facts) {
	if src == nil {
		return
	}
	if src.Problem != nil {
		f.Problem = src.Problem
	}
	if src.Deploys != nil {
		f.Deploys = src.Deploys
	}
	if src.Pods != nil {
		f.Pods = src.Pods
	}
	if src.Deps != nil {
		f.Deps = src.Deps
	}
	if src.Pivot != nil {
		f.Pivot = src.Pivot
	}
	if src.Spike != nil {
		f.Spike = src.Spike
	}
	if src.Events != nil {
		f.Events = src.Events
	}
	if src.Node != nil {
		f.Node = src.Node
	}
	if src.Argo != nil {
		f.Argo = src.Argo
	}
	if src.Baseline != nil {
		f.Baseline = src.Baseline
	}
	if src.Anomalies != nil {
		f.Anomalies = src.Anomalies
	}
	if src.Errors != nil {
		f.Errors = src.Errors
	}
}

// ProblemFacts — problem satırından (okuma zenginleştirmesi dahil).
type ProblemFacts struct {
	RuleID   string `json:"ruleId,omitempty"`
	RuleName string `json:"ruleName,omitempty"`
	Metric   string `json:"metric,omitempty"`
	// EpisodeCount / FirstStartedAt — terfi Problem'inin kaynak olay bölümü
	// (EnrichProblemsWithDeploys); deploy hipotezinin "düzenli yinelenme"
	// çürütmesi chstore.AnomalyPredatesDeploy ile buradan.
	EpisodeCount   uint32 `json:"episodeCount,omitempty"`
	FirstStartedAt int64  `json:"firstStartedAt,omitempty"`
}

// DeployFact — deploy adımının bir satırı.
type DeployFact struct {
	At      int64  `json:"at"` // unix ns
	Source  string `json:"source,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Prev    string `json:"prev,omitempty"`
	Status  string `json:"status,omitempty"`
	// Origin / OriginConfidence — v0.10.1494 (CoSRE 2d): rollout satırının git
	// kaynağı (rollback | redeploy_older | forward | unknown; list_deployments
	// ChangeRow.Origin ← chstore.AttachRolloutOriginWith). Argo yoksa boş.
	Origin           string `json:"origin,omitempty"`
	OriginConfidence uint8  `json:"originConfidence,omitempty"`
}

// DeployFacts — [başlangıç − 2 sa, şimdi] değişiklikleri (≤10).
type DeployFacts struct {
	Rows []DeployFact `json:"rows"`
}

// PodFacts — servis taramasının pod / JVM / desen bölümleri.
type PodFacts struct {
	PodsState    string   `json:"podsState,omitempty"` // tarama bölüm durumu (ok | empty | skipped | error …)
	Pods         int      `json:"pods"`
	MaxCPUPct    *float64 `json:"maxCpuPct,omitempty"` // limitin %'si (nil = limit/metrik yok)
	MaxMemPct    *float64 `json:"maxMemPct,omitempty"`
	HotPod       string   `json:"hotPod,omitempty"`  // en yüksek limit yüzdeli pod
	OOMPods      []string `json:"oomPods,omitempty"` // pencerede OOMKilled + restart (≤3)
	JVMState     string   `json:"jvmState,omitempty"`
	JVMPods      int      `json:"jvmPods"`
	MaxPostGCPct *float64 `json:"maxPostGcPct,omitempty"`
	HotJVM       string   `json:"hotJvm,omitempty"`
	// Desenler — yalnız derin taramada (P1 / doğrulama); "" = okunmadı.
	PatternsState string   `json:"patternsState,omitempty"`
	NewPatterns   []string `json:"newPatterns,omitempty"` // ≤3
	// v0.10.1299 — taramanın operasyon gecikme adayları ve ham span ile
	// DOĞRULANANLAR (chkP99Up: doğrulanan yoksa p99 artışı "olası" → 0).
	LatencyCandidates   int `json:"latencyCandidates,omitempty"`
	LatencyCorroborated int `json:"latencyCorroborated,omitempty"`
	// 2026-11-21 — taramanın runtime tespiti + .NET sinyalleri (runtimesig).
	Runtime        string              `json:"runtime,omitempty"`
	RuntimeState   string              `json:"runtimeState,omitempty"`
	RuntimeSignals []runtimesig.Signal `json:"runtimeSignals,omitempty"`
}

// EdgeFact — aşağı akış kenarı (servis, db:, ext:, q: düğümleri).
type EdgeFact struct {
	Target    string  `json:"target"`
	Calls     uint64  `json:"calls"`
	ErrorRate float64 `json:"errorRate"` // %
	AvgMs     float64 `json:"avgMs"`
}

// SubjectChange — öznenin kendi korele değişim satırı (trafik sıçraması).
type SubjectChange struct {
	RateDeltaPct float64 `json:"rateDeltaPct"`
	P99DeltaPct  float64 `json:"p99DeltaPct"`
	ErrDeltaPct  float64 `json:"errDeltaPct"`
	// v0.10.1299 — mutlak p99'lar (chkP99Up'ın healthverdict tabanları için;
	// eski kayıtta 0 → tabanlar sağlanmamış sayılır, kontrol 0 döner).
	CurP99Ms  float64 `json:"curP99Ms,omitempty"`
	BaseP99Ms float64 `json:"baseP99Ms,omitempty"`
}

// DepsFacts — bağımlılık adımı.
type DepsFacts struct {
	TopologyOK bool           `json:"topologyOk"`
	ChangesOK  bool           `json:"changesOk"`
	Edges      []EdgeFact     `json:"edges"`
	Causes     []depsCause    `json:"causes"`
	Subject    *SubjectChange `json:"subject,omitempty"`
}

// PathFact — kritik yol adımı.
type PathFact struct {
	Service string  `json:"service,omitempty"`
	Name    string  `json:"name"`
	SelfMs  float64 `json:"selfMs"`
}

// PivotFacts — temsilci trace.
type PivotFacts struct {
	TraceID    string     `json:"traceId,omitempty"`
	StartMs    int64      `json:"startMs,omitempty"`
	WallMs     float64    `json:"wallMs"`
	ErrorSpans int        `json:"errorSpans"`
	Path       []PathFact `json:"path,omitempty"` // öz süresine göre ≤3
}

// SpikeContrib — log artışı katkısı.
type SpikeContrib struct {
	Dim         string  `json:"dim"`
	Value       string  `json:"value"`
	SharePct    float64 `json:"sharePct"`
	BaseCount   int64   `json:"baseCount"`
	New         bool    `json:"new,omitempty"`
	BaseUnknown bool    `json:"baseUnknown,omitempty"`
}

// SpikeFacts — log artışı (FromMs = 0 → artış bulunmadı).
type SpikeFacts struct {
	Level        string         `json:"level,omitempty"`
	FromMs       int64          `json:"fromMs,omitempty"`
	ToMs         int64          `json:"toMs,omitempty"`
	Factor       float64        `json:"factor"`
	Contributors []SpikeContrib `json:"contributors,omitempty"` // ≤5
}

// AlertFact — problem penceresi olayı (ReadProblemWindowEvents satırı).
type AlertFact struct {
	ID         string `json:"id"`
	RuleID     string `json:"ruleId,omitempty"`
	RuleName   string `json:"ruleName,omitempty"`
	Service    string `json:"service,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Status     string `json:"status,omitempty"`
	Priority   string `json:"priority,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	ResolvedAt int64  `json:"resolvedAt,omitempty"`
}

// EventFacts — olaylar adımı: öznenin alarmları + db:/ext: aşağı akış
// düğümlerinin problemleri + incident bağlanması.
type EventFacts struct {
	AlertsOK   bool        `json:"alertsOk"`
	Alerts     []AlertFact `json:"alerts,omitempty"` // ≤30
	Targets    []string    `json:"targets,omitempty"`
	RelatedOK  bool        `json:"relatedOk"`
	Related    []AlertFact `json:"related,omitempty"` // ≤10
	IncidentOK bool        `json:"incidentOk"`
	IncidentID string      `json:"incidentId,omitempty"`
	IncidentAt int64       `json:"incidentAt,omitempty"`
}

// NodeFacts — tembel node kontrolü.
type NodeFacts struct {
	State   string                  `json:"state"` // ok | partial | skipped | error | timeout
	Summary string                  `json:"summary,omitempty"`
	Nodes   []mcptools.NodePressure `json:"nodes,omitempty"`
}

// Argo parçasının durumları.
const (
	argoStateOK     = "ok"
	argoStateError  = "error"
	argoStateReplay = "replay"
)

// ArgoFacts — v0.10.1310: servise eşlenen Argo CD uygulamalarının OKUMA
// ANINDAKİ durumu (as-of değil). ReadAt argo_sync'in pencere kapsamı için:
// Syncs24h [ReadAt − 24 sa, ReadAt] aralığını sayar.
type ArgoFacts struct {
	State  string                   `json:"state"`            // ok | error | replay
	ReadAt int64                    `json:"readAt,omitempty"` // unix ns
	Note   string                   `json:"note,omitempty"`
	Apps   []mcptools.ArgoAppStatus `json:"apps,omitempty"` // ≤factsArgoApps
}

const (
	factsArgoApps      = 5
	factsArgoWorkloads = 5
	factsDeployRows    = 10
	factsEdges         = 20
	factsAlerts        = 30
	factsRelated       = 10
	factsContribs      = 5
	factsPathSteps     = 3
	factsListedNames   = 3
)

// argoFactsOf — SAF: dikiş satırları → kayıt parçası (tavanlı kopya; çağıranın
// dilimlerine takma ad tutmaz).
func argoFactsOf(apps []mcptools.ArgoAppStatus, readAt time.Time) *ArgoFacts {
	out := &ArgoFacts{State: argoStateOK, ReadAt: readAt.UnixNano()}
	for _, a := range apps {
		if len(out.Apps) >= factsArgoApps {
			out.Note = fmt.Sprintf("%d uygulamadan ilk %d saklandı", len(apps), factsArgoApps)
			break
		}
		row := mcptools.ArgoAppStatus{App: a.App, Sync: a.Sync, Health: a.Health}
		for _, w := range a.Workloads {
			if len(row.Workloads) >= factsArgoWorkloads {
				break
			}
			row.Workloads = append(row.Workloads, w)
		}
		if a.Syncs24h != nil {
			n := *a.Syncs24h
			row.Syncs24h = &n
		}
		out.Apps = append(out.Apps, row)
	}
	return out
}

// ── SAF dönüştürücüler (adım sonucu → parça) ────────────────────────────────

func problemFactsOf(p chstore.Problem) *ProblemFacts {
	return &ProblemFacts{RuleID: p.RuleID, RuleName: p.RuleName, Metric: p.Metric, EpisodeCount: p.EpisodeCount, FirstStartedAt: p.FirstStartedAt}
}

func deployFactsOf(dc mcptools.DeployCheck) *DeployFacts {
	out := &DeployFacts{Rows: []DeployFact{}}
	for _, r := range dc.Rows {
		if len(out.Rows) >= factsDeployRows {
			break
		}
		out.Rows = append(out.Rows, DeployFact{At: r.TimeUnixNs, Source: r.Source, Name: firstNonEmpty(r.Workload, r.Service),
			Version: r.Version, Prev: r.PrevVersion, Status: r.Status, Origin: r.Origin, OriginConfidence: r.OriginConfidence})
	}
	return out
}

func sectionState(res mcptools.SweepResult, name string) string {
	for _, s := range res.Sections {
		if s.Name == name {
			return s.State
		}
	}
	return ""
}

func maxPtr(cur *float64, v *float64) *float64 {
	if v == nil {
		return cur
	}
	if cur == nil || *v > *cur {
		x := *v
		return &x
	}
	return cur
}

func podFactsOf(res mcptools.SweepResult) *PodFacts {
	out := &PodFacts{PodsState: sectionState(res, "pods"), JVMState: sectionState(res, "jvm"), PatternsState: sectionState(res, "patterns")}
	out.Pods = res.Scanned.Pods
	out.JVMPods = res.Scanned.JVMPods
	out.LatencyCandidates = res.Scanned.LatencyCandidates
	out.LatencyCorroborated = res.Scanned.LatencyCorroborated
	out.Runtime, out.RuntimeState, out.RuntimeSignals = res.Scanned.Runtime, sectionState(res, "runtime"), res.Data.RuntimeSignals
	best := -1.0
	for _, p := range res.Data.Pods {
		before := best
		out.MaxCPUPct = maxPtr(out.MaxCPUPct, p.CPUPct)
		out.MaxMemPct = maxPtr(out.MaxMemPct, p.MemPct)
		for _, v := range []*float64{p.CPUPct, p.MemPct} {
			if v != nil && *v > best {
				best = *v
			}
		}
		if best > before {
			out.HotPod = p.Pod
		}
		if strings.EqualFold(p.LastTermReason, "OOMKilled") && p.RestartsWindow != nil && *p.RestartsWindow >= 0.5 && len(out.OOMPods) < factsListedNames {
			out.OOMPods = append(out.OOMPods, p.Pod)
		}
	}
	gc := -1.0
	for _, j := range res.Data.JVM {
		out.MaxPostGCPct = maxPtr(out.MaxPostGCPct, j.PostGCPct)
		if j.PostGCPct != nil && *j.PostGCPct > gc {
			gc, out.HotJVM = *j.PostGCPct, j.Pod
		}
	}
	for _, pt := range res.Data.Patterns {
		if pt.Kind == "new" && len(out.NewPatterns) < factsListedNames {
			out.NewPatterns = append(out.NewPatterns, truncRunes(pt.Pattern, 120))
		}
	}
	return out
}

// isServiceNode — topoloji hedefi bir servis mi (db:/ext:/q: düğümü değil).
func isServiceNode(t string) bool {
	return !strings.HasPrefix(t, "db:") && !strings.HasPrefix(t, "ext:") && !strings.HasPrefix(t, "q:")
}

func depsFactsOf(req depsRequest, res depsResult) *DepsFacts {
	out := &DepsFacts{TopologyOK: res.edgeErr == nil, ChangesOK: res.chgErr == nil, Edges: []EdgeFact{}, Causes: []depsCause{}}
	if res.edgeErr == nil {
		for _, e := range res.edges {
			if e.Source == req.Service && e.Target != req.Service && len(out.Edges) < factsEdges {
				out.Edges = append(out.Edges, EdgeFact{Target: e.Target, Calls: e.CallCount, ErrorRate: round2(e.ErrorRate), AvgMs: round2(e.AvgMs)})
			}
		}
	}
	if res.chgErr == nil {
		for _, c := range chstore.MarkCorrelationCauses(res.changes, req.Service, res.edges, res.edgeErr == nil) {
			if c.Service == req.Service {
				out.Subject = &SubjectChange{RateDeltaPct: round2(c.RateDeltaPct), P99DeltaPct: round2(c.P99DeltaPct), ErrDeltaPct: round2(c.ErrDeltaPct),
					CurP99Ms: round2(c.CurrentP99Ms), BaseP99Ms: round2(c.BaselineP99Ms)}
				continue
			}
			if c.CauseEligible && len(out.Causes) < depsCausesMax {
				out.Causes = append(out.Causes, depsCause{Service: c.Service, Relation: c.Relation, Direction: c.Direction})
			}
		}
	}
	return out
}

func pivotFactsOf(res mcptools.PivotResult) *PivotFacts {
	out := &PivotFacts{}
	if res.Trace == nil {
		return out
	}
	tr := res.Trace
	out.TraceID, out.StartMs, out.WallMs, out.ErrorSpans = tr.TraceID, tr.StartMs, round2(tr.WallMs), tr.ErrorSpans
	path := make([]PathFact, 0, len(tr.CriticalPath))
	for _, st := range tr.CriticalPath {
		path = append(path, PathFact{Service: st.Service, Name: truncRunes(st.Name, 120), SelfMs: round2(st.SelfOnPathMs)})
	}
	// öz süresine göre azalan (eşitlikte yol sırası).
	for i := 1; i < len(path); i++ {
		for j := i; j > 0 && path[j].SelfMs > path[j-1].SelfMs; j-- {
			path[j], path[j-1] = path[j-1], path[j]
		}
	}
	if len(path) > factsPathSteps {
		path = path[:factsPathSteps]
	}
	out.Path = path
	return out
}

func spikeFactsOf(req *mcptools.LogSpikeRequest, res mcptools.LogSpikeResult) *SpikeFacts {
	out := &SpikeFacts{}
	if req != nil {
		out.Level = req.Level
	}
	if res.Spike != nil {
		out.FromMs, out.ToMs, out.Factor = res.Spike.FromMs, res.Spike.ToMs, round2(res.Spike.Factor)
	}
	for _, c := range res.Contributors {
		if len(out.Contributors) >= factsContribs {
			break
		}
		out.Contributors = append(out.Contributors, SpikeContrib{Dim: c.Dim, Value: truncRunes(c.Value, 120), SharePct: round2(c.SharePct),
			BaseCount: c.BaseCount, New: c.New, BaseUnknown: c.BaseUnknown})
	}
	return out
}

func alertFactsOf(rows []mcptools.ProblemWindowRow, skipID string, max int) []AlertFact {
	out := []AlertFact{}
	for _, r := range rows {
		if r.ID == skipID {
			continue
		}
		if len(out) >= max {
			break
		}
		out = append(out, AlertFact{ID: r.ID, RuleID: r.RuleID, RuleName: truncRunes(r.RuleName, 120), Service: r.Service, Severity: r.Severity,
			Status: r.Status, Priority: r.Priority, StartedAt: r.StartedAtNs, ResolvedAt: r.ResolvedAtNs})
	}
	return out
}

func nodeFactsOf(nc mcptools.NodeCheck) *NodeFacts {
	out := &NodeFacts{State: nc.Section.State, Summary: truncRunes(nc.Section.Summary, 300)}
	for i, n := range nc.Nodes {
		if i >= 3 {
			break
		}
		if len(n.TopPods) > 5 {
			n.TopPods = n.TopPods[:5]
		}
		out.Nodes = append(out.Nodes, n)
	}
	return out
}

// eventsWindow — SAF: olay penceresi [başlangıç − 60 dk, şimdi].
func eventsWindow(startNs int64, now time.Time) (time.Time, time.Time) {
	start := time.Unix(0, startNs)
	if startNs <= 0 {
		start = now.Add(-30 * time.Minute)
	}
	from := start.Add(-60 * time.Minute)
	if !now.After(from) {
		return from, from.Add(time.Minute)
	}
	return from, now
}
