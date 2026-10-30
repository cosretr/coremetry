package mcptools

// service_sweep.go — v0.10.1276 (CoSRE adım 7/8): servis sağlık TARAMASI.
//
// Operatör: "Servisin tüm podlarını operationlarını fonksiyonlarını da
// tarasın genel sağlığı için sorunlu olanların loglarına baksın ve kullanıcıyı
// bu anlamda da assist etsin."
//
// TEK çekirdek, üç tüketici (ReadPodHealth deseni, guided_parity.go):
// ReadServiceSweep → (a) MCP / sohbet aracı service_health_sweep (JSON),
// (b) guided servis-sağlık demeti (Türkçe kanıt + hüküm birleşimi), (c) testler.
// get_service_health değişmedi (tek servis RED'i); tarama onun üstünde.
//
// EVRE 1 (paralel; sweepBudget 15 s, executor 20 s):
//
//	red        ServiceWindowRED / ServiceEnvWindowRED (service_summary_5m / env MV), şimdiki + önceki eş pencere   3 s  env VAR
//	problems   açık konular: Deps.InboxOpen + ServiceAttentionQuery (servis şeridinin kümesi, v0.10.1694)            3 s  env YOK (şerit)
//	ops        GetOperationSummaryCompared (operation_summary_5m, şimdiki + önceki)                              5 s  env YOK (not)
//	functions  FUNCTION_CODE: geniş rollup (function_code grubu, ≤20) → yoksa BusinessBreakdown (ham, LIMIT 8)    5 s  env YOK
//	pods       Deps.ServicePods: Thanos PodMetrics + increase(restarts[pencere]); servisin eşlenen cluster'ları (≤3), yoksa tüm etkin (≤6)  6 s  env YOK
//	jvm        RuntimePods heap + GC, son 10 dk (RuntimePodWindow), servisin pod'ları                             5 s  env YOK
//
// Puanlama (service_sweep_score.go, SAF): seviye → etki → tür → ad; ilk N
// (standart 3 / derin 5, tavan 8).
//
// EVRE 2 (sorunluların logları; son tarih = kalan − 1 s, ≤4 paralel, 5 s):
//
//	pod / JVM bulgusu        logstore.Filter{Service, Pod, SeverityMin: ERROR} ≤20 → ilk 5
//	operasyon / fonksiyon    hatalı trace (name= / FUNCTION_CODE=, Limit 2) → o trace'in logları
//	derin kip                code.* örneklemi (chstore.CodeFunctionSample) + log desenleri (SkipPatterns değilse)
//
// Zincir ASLA kopmaz: boş bölüm "veri yok: …", okunamayan bölüm
// error/timeout/degraded (okunamadı ≠ yok); süre biterse skipped ("süre
// bütçesi doldu"). İlerleme etiketleri YALNIZ koordinatör goroutine'inden.
//
// ZARF (alan sırası = JSON sırası; sohbet sonucu 6000 rune'da keser): tool,
// ok, partial, empty?, message?, error?, verdict, scanned, findings,
// findingsHasMore?, sections, warnings, request, evidence (= evidences[0]),
// evidences (≤6), data (satırlar + bloklar) — veri EN SONDA.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/healthverdict"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/metricsrc"
	"github.com/cilcenk/coremetry/internal/promptfmt"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// ServiceHealthSweepName — aracın adı (kanıt eşlemesi + sohbet evresi).
const ServiceHealthSweepName = "service_health_sweep"

// Bütçe ve bölüm tavanları.
const (
	sweepBudget        = 15 * time.Second
	sweepPhase2Margin  = time.Second
	sweepCapRED        = 3 * time.Second
	sweepCapProblems   = 3 * time.Second
	sweepCapOps        = 5 * time.Second
	sweepCapFunctions  = 5 * time.Second
	sweepCapPods       = 6 * time.Second
	sweepCapJVM        = 5 * time.Second
	sweepCapLogs       = 5 * time.Second
	sweepLogParallel   = 4
	sweepLogFetch      = 20
	sweepLogKeep       = 5
	sweepFnGroups      = 20
	sweepFnMDP         = 30
	sweepOpsRowsMax    = 20
	sweepPodRowsMax    = 20
	sweepMappedMax     = 3
	sweepAllClusterMax = 6
	sweepMaxBlocks     = 6
	sweepCacheTTL      = 60 * time.Second
	sweepProblemsMax   = 10
	sweepPatternWindow = 15 * time.Minute
	sweepPatternsMax   = 5
	sweepReasonRunes   = 160
	sweepLogPad        = 5 * time.Minute
	sweepMaxWindow     = 6 * time.Hour
)

// Bölüm (halka) adları.
const (
	sweepHopRED       = "red"
	sweepHopProblems  = "problems"
	sweepHopOps       = "ops"
	sweepHopFunctions = "functions"
	sweepHopPods      = "pods"
	sweepHopJVM       = "jvm"
	sweepHopLogs      = "logs"
	sweepHopCode      = "code"
	sweepHopPatterns  = "patterns"
	sweepHopCorrob    = "corroborate" // v0.10.1299 — gecikme adaylarının ham span doğrulaması
)

// Fonksiyon verisinin kaynağı.
const (
	sweepFnSourceRollup = "rollup"
	sweepFnSourceSpans  = "spans"
	sweepFnSourceNone   = "none"
)

var sweepWindowEnum = []string{"15m", "1h", "6h"}

// ── dış dikiş: pod adaptörü (api/mcp_service_pods.go) ───────────────────

// ServicePodsCluster — bir cluster'ın pod okuması.
type ServicePodsCluster struct {
	ID         string
	Name       string
	Query      string             // çalışan okuma(lar)ın metni (kanıt)
	Rows       []thanos.PodRow    // PodMetrics (anlık: CPU/bellek/limit %/faz/son sonlanma)
	Restarts   map[string]float64 // "namespace\x00pod" → penceredeki restart artışı
	RestartsOK bool               // restart artışı okundu mu (değilse puanlanmaz)
	// v0.10.1287 (AI SRE Faz 1b, k8s kontrolü) — "namespace\x00pod" → şu anki
	// bekleme sebebi (thanos.PodWaitingReasonQuery; yalnız opt.WaitingReasons).
	Waiting   map[string]string
	WaitingOK bool // bekleme sebebi okundu mu (değilse k8s bölümü "okunamadı" der)
	// v0.10.1288 (AI SRE Faz 2, tembel node kontrolü; yalnız opt.Nodes):
	// "namespace\x00pod" → node (kube_pod_info), node'ların CPU baskısı + en
	// işlek pod'ları. NodesOK = node okuması koştu (pod→node eşlemesi boşsa
	// Nodes boş: "node etiketi yok"); NodeErr = baskı okunamadı.
	PodNodes   map[string]string
	Nodes      []NodePressure
	NodesOK    bool
	NodeErr    string
	Truncated  bool
	Err        string // okunamadıysa neden
	DurationMs int64
}

// ServicePodsResult — adaptörün cevabı.
type ServicePodsResult struct {
	Thanos   bool // en az bir etkin cluster var mı
	PodRegex string
	Clusters []ServicePodsCluster
}

// ServicePodsOpts — v0.10.1287: isteğe bağlı ek okumalar. WaitingReasons =
// k8s kontrolü (bekleme sebebi; cluster başına +1 anlık sorgu).
type ServicePodsOpts struct {
	WaitingReasons bool
	// Nodes — v0.10.1288 (AI SRE Faz 2): TEMBEL node / gürültülü komşu
	// kontrolü. Pod metrikleri (PodMetrics / restart) OKUNMAZ; yalnız
	// kube_pod_info → node (≤3), node CPU % allocatable, o node'lardaki en
	// işlek 5 pod (thanos.PodNodeQuery / NodeCPUPressureQuery /
	// NodeTopPodsQuery). Yalnız doygunluk hipotezi isterse (internal/sre).
	Nodes bool
}

// NodePressure — v0.10.1288: bir node'un CPU baskısı (allocatable'ın %'si)
// ve en işlek pod'ları. Service = pod servisin kendi pod'u (komşu değil).
type NodePressure struct {
	Cluster string       `json:"cluster,omitempty"`
	Node    string       `json:"node"`
	CPUPct  *float64     `json:"cpuPct,omitempty"` // nil = okunamadı
	TopPods []NodeTopPod `json:"topPods,omitempty"`
}

// NodeTopPod — node'daki işlek pod (CPU, node allocatable'ının %'si).
type NodeTopPod struct {
	Namespace string  `json:"namespace,omitempty"`
	Pod       string  `json:"pod"`
	Pct       float64 `json:"pct"`
	Service   bool    `json:"service,omitempty"`
}

// ServicePodsFunc — servis pod'larının Thanos okuması; clusterRefs boş = tüm
// etkin cluster'lar (adaptör tavanlar). nil = Thanos yok (bölüm skipped).
type ServicePodsFunc func(ctx context.Context, service string, clusterRefs []string, from, to time.Time, opt ServicePodsOpts) (ServicePodsResult, error)

// SweepRequest — normalize argümanlar. From/To sıfırsa pencere ctx
// çıpasından kurulur.
type SweepRequest struct {
	Service      string
	Env          string
	Window       string // 15m | 1h | 6h
	From, To     time.Time
	Deep         bool
	TopN         int  // 0 → standart 3 / derin 5; tavan 8
	SkipPatterns bool // guided: log desenleri okunmaz
	// Checks — v0.10.1287 (AI SRE Faz 1b; follow-up B): ek kontroller
	// (service_sweep_checks.go). nil = standart (deploys + k8s); boş dilim =
	// hiçbiri. AI SRE orkestratörü deploy'u kendi adımında okur → [k8s].
	Checks []string
	// Anchor — deploy kontrolünün çıpası (problem başlangıcı); sıfır → To.
	Anchor time.Time
}

// ── çıktı ───────────────────────────────────────────────────────────────

// SweepVerdict — tarama hükmü (Sağlıklı | Dikkat | Sorunlu | Veri yok).
type SweepVerdict struct {
	Label   string   `json:"label"`
	Reasons []string `json:"reasons"`
	Next    string   `json:"next"`
}

// SweepScanned — ne kadar şey tarandı.
type SweepScanned struct {
	Pods            int    `json:"pods"`
	Operations      int    `json:"operations"`
	Functions       int    `json:"functions"`
	FunctionsSource string `json:"functionsSource"` // rollup | spans | none
	JVMPods         int    `json:"jvmPods"`
	Clusters        int    `json:"clusters"`
	// v0.10.1299 — operasyon gecikme adayları ve ham span ile doğrulananlar
	// (AI SRE chkP99Up bunu okur: aday var ama doğrulanan yok → bilinmez).
	LatencyCandidates   int `json:"latencyCandidates,omitempty"`
	LatencyCorroborated int `json:"latencyCorroborated,omitempty"`
}

// SweepLogs — bulgunun logları (evre 2).
type SweepLogs struct {
	State   string `json:"state"`
	Summary string `json:"summary"`
	TraceID string `json:"traceId,omitempty"`
	// Rows — süreç içi tüketici (guided) için; JSON'da data.findingLogs'ta
	// (zarfın veri-öncesi kısmı 6000 rune'a sığsın diye bulguda taşınmaz).
	Rows []qlRow `json:"-"`
}

// sweepFindingLogRows — bir bulgunun log satırları (data altında).
type sweepFindingLogRows struct {
	Rank int     `json:"rank"`
	Name string  `json:"name"`
	Rows []qlRow `json:"rows"`
}

// SweepFinding — sıralı bulgu.
type SweepFinding struct {
	Rank int    `json:"rank"`
	Kind string `json:"kind"` // pod | jvm | operation | function
	Name string `json:"name"`
	// SpanKind — v0.10.1391: operasyon bulgusunun span türü (SERVER | CLIENT |
	// PRODUCER | CONSUMER | INTERNAL; spanmetrics_1m), bilinmiyorsa boş.
	// Anlatım giden çağrıyı servisin gelen trafiği sanmasın.
	SpanKind     string     `json:"spanKind,omitempty"`
	Cluster      string     `json:"cluster,omitempty"`
	ClusterID    string     `json:"clusterId,omitempty"`
	Namespace    string     `json:"namespace,omitempty"`
	Level        string     `json:"level"`
	Reasons      []string   `json:"reasons"`
	EvidenceRefs []string   `json:"evidenceRefs"`
	Logs         *SweepLogs `json:"logs,omitempty"`
	hop          string
	impact       float64
}

// SweepSection — bölüm durumu.
type SweepSection struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Summary     string `json:"summary"`
	EnvApplied  *bool  `json:"envApplied,omitempty"`
	Note        string `json:"note,omitempty"`
	EvidenceRef string `json:"evidenceRef,omitempty"`
	DurationMs  int64  `json:"durationMs"`
}

type sweepOpRow struct {
	Name           string  `json:"name"`
	Calls          uint64  `json:"calls"`
	ErrorRate      float64 `json:"errorRate"`
	P99Ms          float64 `json:"p99Ms"`
	HasPrior       bool    `json:"hasPrior"`
	PriorErrorRate float64 `json:"priorErrorRate,omitempty"`
	PriorP99Ms     float64 `json:"priorP99Ms,omitempty"`
	Level          string  `json:"level"`
	Reason         string  `json:"reason,omitempty"`
	// Corroboration — v0.10.1299: gecikme adayının ham span doğrulaması
	// (confirmed | rejected | unverified); "" = gecikme adayı değildi.
	Corroboration string `json:"corroboration,omitempty"`
}

type sweepFnRow struct {
	Code           string  `json:"code"`
	Calls          uint64  `json:"calls"`
	Errors         uint64  `json:"errors"`
	ErrorRate      float64 `json:"errorRate"`
	P99Ms          float64 `json:"p99Ms,omitempty"` // rollup: kova kestirimi (yaklaşık); spans: yok
	HasPrior       bool    `json:"hasPrior"`
	PriorErrorRate float64 `json:"priorErrorRate,omitempty"`
	PriorP99Ms     float64 `json:"priorP99Ms,omitempty"`
	Level          string  `json:"level"`
	Reason         string  `json:"reason,omitempty"`
}

type sweepPodRow struct {
	Cluster        string   `json:"cluster,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	Pod            string   `json:"pod"`
	Phase          string   `json:"phase,omitempty"`
	CPUPct         *float64 `json:"cpuLimitPct,omitempty"`
	MemPct         *float64 `json:"memLimitPct,omitempty"`
	RestartsWindow *float64 `json:"restartsWindow,omitempty"`
	LastTermReason string   `json:"lastTermReason,omitempty"`
	Level          string   `json:"level"`
	Reason         string   `json:"reason,omitempty"`
}

type sweepPattern struct {
	Pattern string  `json:"pattern"`
	Kind    string  `json:"kind"`
	Ratio   float64 `json:"ratio"`
	Count   uint64  `json:"count"`
	Sample  string  `json:"sample,omitempty"`
}

type sweepData struct {
	Operations    []sweepOpRow                `json:"operations"`
	Functions     []sweepFnRow                `json:"functions"`
	CodeFunctions []chstore.CodeFunctionSlice `json:"codeFunctions,omitempty"` // derin kip, ÖRNEKLEM
	Pods          []sweepPodRow               `json:"pods"`
	JVM           []pivotJVMRow               `json:"jvm"`
	OpenItems     []InboxRow                  `json:"openItems,omitempty"`
	FindingLogs   []sweepFindingLogRows       `json:"findingLogs,omitempty"`
	Patterns      []sweepPattern              `json:"patterns,omitempty"`
	Blocks        []pivotBlock                `json:"blocks"`
}

// SweepResult — zarf; alan sırası = JSON sırası (veri EN SONDA).
type SweepResult struct {
	Tool            string             `json:"tool"`
	OK              bool               `json:"ok"`
	Partial         bool               `json:"partial"`
	Empty           bool               `json:"empty,omitempty"`
	Message         string             `json:"message,omitempty"`
	Error           *cosreError        `json:"error,omitempty"`
	Verdict         SweepVerdict       `json:"verdict"`
	Scanned         SweepScanned       `json:"scanned"`
	Findings        []SweepFinding     `json:"findings"`
	FindingsHasMore bool               `json:"findingsHasMore,omitempty"`
	Sections        []SweepSection     `json:"sections"`
	Warnings        []string           `json:"warnings,omitempty"`
	Request         map[string]any     `json:"request"`
	Evidence        *cosreEvidence     `json:"evidence,omitempty"`
	Evidences       []cosreHopEvidence `json:"evidences,omitempty"`
	Data            sweepData          `json:"data"`
}

// ── okuyucular (test dikişi) ────────────────────────────────────────────

type sweepReaders struct {
	windowRED func(ctx context.Context, service, env string, from, to time.Time) (chstore.ServiceWindowRED, error)
	// openItems — v0.10.1694: servis şeridinin "Açık konular" kümesi (Deps.InboxOpen + Attention).
	openItems     InboxOpenReader
	ops           func(ctx context.Context, service string, from, to time.Time) ([]chstore.OperationSummary, error)
	fnRollup      func(ctx context.Context, service string, from, to time.Time) ([]chstore.RollupSeries, error)
	fnSpans       func(ctx context.Context, service string, from, to time.Time) ([]chstore.BusinessSlice, error)
	codeSample    func(ctx context.Context, service string, ops []string, from, to time.Time) ([]chstore.CodeFunctionSlice, error)
	clusterValues func(ctx context.Context, service string, from, to time.Time) ([]string, error)
	clusters      func() []ClusterRef
	servicePods   ServicePodsFunc
	runtime       chstore.RuntimePodReader
	runtimeSource string
	runtimeAlerts func(ctx context.Context) chstore.RuntimeAlertConfig
	logs          logstore.Store
	searchTraces  func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error)
	patterns      func(ctx context.Context, window time.Duration) ([]anomaly.LogPatternAnomaly, error)
	// deploys — v0.10.1287: deploy kontrolünün okuyucusu (list_deployments ile
	// AYNI gövde, ListDeploymentsWindow). nil = bölüm skipped.
	deploys func(ctx context.Context, service string, from, to time.Time) ([]ChangeRow, string, error)
	// v0.10.1299 — gecikme adayının ham span doğrulaması + operasyon grafiğinin
	// ince (spanmetrics_1m) serisi. nil = doğrulama yok (aday "olası" kalır) /
	// 5 dk sparkline.
	slowSpans func(ctx context.Context, service, op string, thrMs float64, curFrom, curTo, priorFrom time.Time) (chstore.SlowSpanCorroboration, error)
	opLatency func(ctx context.Context, service, op string, from, to time.Time, stepSec int) ([]chstore.OpLatencyPoint, error)
	// v0.10.1391 — grafik adayı operasyonların span türleri (spanmetrics_1m):
	// giriş (SERVER/CONSUMER) grafiği giden çağrıya (CLIENT/PRODUCER) tercih
	// edilir. nil / hata = tür bilinmiyor, tercih yok (eski sıra).
	opKinds func(ctx context.Context, service string, names []string, from, to time.Time) (map[string][]string, error)
}

func sweepReadersFrom(d Deps) sweepReaders {
	r := sweepReaders{servicePods: d.ServicePods, logs: d.LogStore, clusters: d.Clusters, openItems: d.InboxOpen,
		runtimeAlerts: func(context.Context) chstore.RuntimeAlertConfig { return chstore.DefaultRuntimeAlerts() }}
	r.runtime, r.runtimeSource = d.RuntimePods, evSourceCH
	if d.MetricRouter != nil {
		r.runtime = d.MetricRouter.RuntimePods()
		if d.MetricRouter.Resolve(metricsrc.FamilyOTelRuntime, metricsrc.Scope{}).Backend == metricsrc.BackendVM {
			r.runtimeSource = evSourceVM
		}
	}
	if d.LogStore != nil {
		st := d.LogStore
		r.patterns = func(ctx context.Context, w time.Duration) ([]anomaly.LogPatternAnomaly, error) {
			return anomaly.DetectLogPatterns(ctx, st, w)
		}
	}
	if st := d.Store; st != nil {
		r.windowRED = func(ctx context.Context, service, env string, from, to time.Time) (chstore.ServiceWindowRED, error) {
			if env != "" {
				return st.ServiceEnvWindowRED(ctx, service, env, from, to)
			}
			return st.ServiceWindowRED(ctx, service, from, to)
		}
		r.ops = func(ctx context.Context, service string, from, to time.Time) ([]chstore.OperationSummary, error) {
			return st.GetOperationSummaryCompared(ctx, service, 0, from, to, false, "")
		}
		r.fnRollup = func(ctx context.Context, service string, from, to time.Time) ([]chstore.RollupSeries, error) {
			plan, err := chstore.PickRollup(chstore.RollupQuery{From: from, To: to, MaxDataPoints: sweepFnMDP,
				Dims: []string{"service_name", "function_code"}}, wallNow())
			if err != nil {
				return nil, err
			}
			// v0.10.1299 — WindowTotals: kod başına PENCERE birleşik histogram
			// (kova p99 ortalaması değil) + kova serisi (grafik + oynaklık).
			rows, _, err := st.QueryRollupRED(ctx, plan, chstore.RollupSeriesFilter{Service: service, GroupBy: "function_code", MaxGroups: sweepFnGroups, WindowTotals: true}, from, to)
			return rows, err
		}
		r.fnSpans = func(ctx context.Context, service string, from, to time.Time) ([]chstore.BusinessSlice, error) {
			return st.BusinessBreakdown(ctx, service, "FUNCTION_CODE", from, to)
		}
		r.codeSample = st.CodeFunctionSample
		r.clusterValues = func(ctx context.Context, service string, from, to time.Time) ([]string, error) {
			rows, _, err := st.GetServiceClusterBreakdownSourced(ctx, service, from, to)
			out := make([]string, 0, len(rows))
			for _, row := range rows {
				out = append(out, row.Cluster)
			}
			return out, err
		}
		if r.runtime == nil {
			r.runtime = st
		}
		r.runtimeAlerts = st.GetRuntimeAlerts
		r.searchTraces = st.GetTraces
		r.deploys = func(ctx context.Context, service string, from, to time.Time) ([]ChangeRow, string, error) {
			return deployCheckRows(ctx, d, service, from, to)
		}
		r.slowSpans = st.SlowSpanCorroborate
		r.opLatency = st.OperationLatencySeries
		r.opKinds = st.OperationKinds // v0.10.1391
	}
	return r
}

// ── koşucu ──────────────────────────────────────────────────────────────

// sweepOutcome — bir bölümün tüm çıktısı (işçi yalnız bunu doldurur ve
// kanala yollar; koordinatör birleştirir — paylaşılan durum yok).
type sweepOutcome struct {
	section   SweepSection
	evidences []cosreHopEvidence
	cands     []sweepCand
	failed    bool

	red, prior   chstore.ServiceWindowRED
	redErr       error
	redOK        bool
	priorOK      bool
	openItems    InboxOpenResult
	openItemsOK  bool
	ops          []chstore.OperationSummary
	opRows       []sweepOpRow
	fnRows       []sweepFnRow
	fnSource     string
	fnSeries     map[string]chstore.RollupSeries
	pods         []sweepPodRow
	clusters     int
	jvm          []pivotJVMRow
	scannedPods  int
	scannedOps   int
	scannedFns   int
	scannedJVM   int
	anyDataFound bool
	// v0.10.1287 — k8s kontrolünün girdisi (pods bölümü doldurur) + bekleme
	// sebebi okunamayan cluster notları.
	k8s        []K8sPodState
	k8sNotes   []string
	k8sWaitOK  bool
	k8sWaitAsk bool
	// v0.10.1299 — ops bölümü: doğrulanan gecikme adaylarının ham sayımları
	// (grafiğin yavaş-istek serisi) + sayaçlar.
	corrob          map[string]chstore.SlowSpanCorroboration
	corrobCands     int
	corrobConfirmed int
}

// ReadServiceSweep — taramanın tamamı. Hata yalnız çağıran iptal ettiğinde
// döner; her okuma arızası bölüm durumuna çevrilir.
func ReadServiceSweep(ctx context.Context, d Deps, req SweepRequest) (SweepResult, error) {
	return runServiceSweep(ctx, sweepReadersFrom(d), req, nil)
}

func runServiceSweep(ctx context.Context, r sweepReaders, req SweepRequest, warnings []string) (SweepResult, error) {
	t0 := wallNow()
	deadline := t0.Add(sweepBudget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	remaining := func() time.Duration { return time.Until(deadline) }
	if req.Window == "" {
		req.Window = "1h"
	}
	if req.From.IsZero() || req.To.IsZero() {
		req.From, req.To = rangeWindow(ctx, cosreWindowSeconds[req.Window])
		req.From, req.To = req.From.UTC(), req.To.UTC()
	}
	if req.To.Sub(req.From) > sweepMaxWindow {
		// Guided pencere 24 sa / 7 g olabilir; pod restart increase[] ve ham
		// fonksiyon kırılımı geniş pencerede pahalı — tarama son 6 saate iner.
		req.From = req.To.Add(-sweepMaxWindow)
		warnings = append(warnings, "tarama penceresi en fazla 6 saat; son 6 saat tarandı")
	}
	topN := sweepTopN(req.Deep, req.TopN)
	res := SweepResult{Tool: ServiceHealthSweepName, Warnings: warnings, Findings: []SweepFinding{}, Sections: []SweepSection{},
		Verdict: SweepVerdict{Label: healthverdict.LabelNoData, Reasons: []string{}},
		Data:    sweepData{Operations: []sweepOpRow{}, Functions: []sweepFnRow{}, Pods: []sweepPodRow{}, JVM: []pivotJVMRow{}, Blocks: []pivotBlock{}}}
	echo := map[string]any{"window": req.Window, "deep": req.Deep, "top_n": topN}
	if req.Service != "" {
		echo["service"] = req.Service
	}
	if req.Env != "" {
		echo["env"] = req.Env
	}
	res.Request = echo
	if strings.TrimSpace(req.Service) == "" {
		res.Error = &cosreError{Code: cosreErrBadArgs, Message: "service gerekli (taranacak servisin tam adı; list_services)", Need: []string{"service"}}
		return res, nil
	}
	alerts := chstore.DefaultRuntimeAlerts()
	if r.runtimeAlerts != nil {
		alerts = r.runtimeAlerts(ctx)
	}

	// ── evre 1 ─────────────────────────────────────────────────────────
	type job struct {
		name  string
		phase string
		cap   time.Duration
		run   func(context.Context) sweepOutcome
	}
	jobs := []job{
		{sweepHopRED, "Sağlık", sweepCapRED, func(c context.Context) sweepOutcome { return sweepRunRED(c, r, req) }},
		{sweepHopProblems, "Sağlık", sweepCapProblems, func(c context.Context) sweepOutcome { return sweepRunProblems(c, r, req) }},
		{sweepHopOps, "Sağlık", sweepCapOps, func(c context.Context) sweepOutcome { return sweepRunOps(c, r, req) }},
		{sweepHopFunctions, "Sağlık", sweepCapFunctions, func(c context.Context) sweepOutcome { return sweepRunFunctions(c, r, req) }},
		{sweepHopPods, "Altyapı", sweepCapPods, func(c context.Context) sweepOutcome { return sweepRunPods(c, r, req) }},
		{sweepHopJVM, "Metrik", sweepCapJVM, func(c context.Context) sweepOutcome { return sweepRunJVM(c, r, req, alerts) }},
	}
	checks := sweepChecksOf(req)
	if checks[SweepCheckDeploys] { // v0.10.1287 — deploy kontrolü (list_deployments okuyucusu)
		jobs = append(jobs, job{sweepHopDeploys, "Değişiklik", sweepCapDeploys, func(c context.Context) sweepOutcome {
			return sweepRunDeploys(c, r, req.Service, sweepAnchor(req), req.To)
		}})
	}
	reportProgress(ctx, "Sağlık", "taranıyor: "+req.Service+" — RED · problemler · operasyonlar · fonksiyonlar · pod'lar · JVM")
	outs := make([]sweepOutcome, len(jobs))
	got := make([]bool, len(jobs))
	type done struct {
		i int
		o sweepOutcome
	}
	ch := make(chan done, len(jobs))
	pending := 0
	for i, j := range jobs {
		dl := hopDeadline(remaining(), j.cap)
		if dl <= 0 {
			outs[i] = sweepOutcome{section: SweepSection{Name: j.name, State: hopSkipped, Summary: "süre bütçesi doldu"}, failed: true}
			got[i] = true
			continue
		}
		pending++
		go func(i int, j job, dl time.Duration) {
			hctx, cancel := context.WithTimeout(ctx, dl)
			defer cancel()
			elapsed := stopwatch()
			o := j.run(hctx)
			o.section.Name = j.name
			o.section.DurationMs = elapsed()
			ch <- done{i, o}
		}(i, j, dl)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
wait:
	for pending > 0 {
		select {
		case dn := <-ch:
			outs[dn.i], got[dn.i] = dn.o, true
			pending--
			reportProgress(ctx, jobs[dn.i].phase, sweepSectionLabelTR(jobs[dn.i].name)+": "+truncateRunes(dn.o.section.Summary, 80))
		case <-timer.C:
			break wait
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return SweepResult{}, ctx.Err()
			}
			break wait
		}
	}
	for i, j := range jobs {
		if !got[i] {
			outs[i] = sweepOutcome{section: SweepSection{Name: j.name, State: hopTimeout, Summary: "süre bütçesi doldu — bölüm bitmedi"}, failed: true}
		}
	}
	// v0.10.1287 — k8s kontrolü: pods bölümünün okuduğu satırlardan (ayrı okuma
	// yok); pod adayıyla aynı pod'un k8s gerekçeleri o adaya katılır.
	if checks[SweepCheckK8s] {
		for i := range outs {
			if outs[i].section.Name == sweepHopPods {
				k8s := sweepK8sOutcome(&outs[i])
				outs = append(outs, k8s)
				break
			}
		}
	}
	byName := map[string]*sweepOutcome{}
	for i := range outs {
		byName[outs[i].section.Name] = &outs[i]
	}

	// ── gecikme adaylarının ham span doğrulaması (v0.10.1299) ─────────
	corrobOut := sweepCorroborate(ctx, r, req, byName[sweepHopOps], deadline)
	if errors.Is(ctx.Err(), context.Canceled) {
		return SweepResult{}, ctx.Err()
	}

	// ── puanla ─────────────────────────────────────────────────────────
	var cands []sweepCand
	for _, o := range outs {
		cands = append(cands, o.cands...)
	}
	findings, more := scoreFindings(cands, topN)
	res.FindingsHasMore = more

	// ── evre 2: sorunluların logları (+ derin kip) ─────────────────────
	waitCharts := sweepStartOpCharts(ctx, r, req, findings, deadline) // v0.10.1299 — evre 2 ile paralel
	p2 := sweepPhase2(ctx, r, req, findings, byName[sweepHopOps], deadline)
	opCharts := waitCharts()
	sweepApplyOpKinds(findings, opCharts.kinds) // v0.10.1391 — bulgu satırı span türünü taşır
	if errors.Is(ctx.Err(), context.Canceled) {
		return SweepResult{}, ctx.Err()
	}

	// ── birleştir ──────────────────────────────────────────────────────
	all := append(append([]sweepOutcome(nil), outs...), p2.outcomes...)
	if corrobOut != nil {
		all = append(all, *corrobOut)
	}
	hopRef := map[string]string{}
	anyData := false
	for i := range all {
		o := &all[i]
		if len(o.evidences) > 0 && len(res.Evidences) < compositeEvidenceMax {
			ref := fmt.Sprintf("evidences[%d]", len(res.Evidences))
			o.section.EvidenceRef = ref
			if _, seen := hopRef[o.section.Name]; !seen {
				hopRef[o.section.Name] = ref
			}
			for _, ev := range o.evidences {
				if len(res.Evidences) < compositeEvidenceMax {
					res.Evidences = append(res.Evidences, ev)
				}
			}
		}
		if req.Env != "" {
			applied := o.section.Name == sweepHopRED
			o.section.EnvApplied = &applied
			if !applied && o.section.Note == "" && o.section.State != hopSkipped {
				o.section.Note = "env uygulanmadı: bu kaynak ortam boyutu taşımıyor"
			}
		}
		res.Sections = append(res.Sections, o.section)
		if o.failed {
			res.Partial = true
		}
		if o.anyDataFound {
			anyData = true
		}
	}
	for i := range findings {
		f := &findings[i]
		if ref, ok := hopRef[f.hop]; ok {
			f.EvidenceRefs = append(f.EvidenceRefs, ref)
		}
		if l, ok := p2.logs[i]; ok {
			// v0.10.1299 — bulgunun (modele / anlatıma giden) satırları gövde
			// ÖNİZLEMESİ; ham satırlar yalnız açılır log tablosunda (p2.logRowsByFinding).
			lc := *l
			lc.Rows = sweepPreviewRows(l.Rows)
			l = &lc
			f.Logs = l
			if ref, ok := hopRef[sweepHopLogs]; ok && l.State == hopOK {
				f.EvidenceRefs = append(f.EvidenceRefs, ref)
			}
		}
	}
	res.Findings = findings
	for _, f := range findings {
		if f.Logs != nil && len(f.Logs.Rows) > 0 {
			res.Data.FindingLogs = append(res.Data.FindingLogs, sweepFindingLogRows{Rank: f.Rank, Name: f.Name, Rows: f.Logs.Rows})
		}
	}
	if len(res.Evidences) > 0 {
		e := res.Evidences[0].cosreEvidence
		res.Evidence = &e
	}

	red := byName[sweepHopRED]
	pr := byName[sweepHopProblems]
	cur := healthverdict.RED{}
	base := healthverdict.RED{}
	redErr := errors.New("okunmadı")
	if red != nil && red.redOK {
		cur, redErr = sweepRED(red.red), nil
		if red.priorOK {
			base = sweepRED(red.prior)
		}
	}
	openN := 0
	if pr != nil && pr.openItemsOK {
		openN = pr.openItems.Total
	}
	label, reasons, next := healthverdict.Service(cur, base, redErr, openN, 0)
	res.Verdict = sweepVerdictOf(label, reasons, next, findings, anyData)

	sc := SweepScanned{FunctionsSource: sweepFnSourceNone}
	for _, o := range outs {
		sc.Pods += o.scannedPods
		sc.Operations += o.scannedOps
		sc.Functions += o.scannedFns
		sc.JVMPods += o.scannedJVM
		sc.Clusters += o.clusters
		sc.LatencyCandidates += o.corrobCands
		sc.LatencyCorroborated += o.corrobConfirmed
		if o.fnSource != "" {
			sc.FunctionsSource = o.fnSource
		}
		res.Data.Operations = append(res.Data.Operations, o.opRows...)
		res.Data.Functions = append(res.Data.Functions, o.fnRows...)
		res.Data.Pods = append(res.Data.Pods, o.pods...)
		res.Data.JVM = append(res.Data.JVM, o.jvm...)
	}
	res.Scanned = sc
	if pr != nil && pr.openItemsOK {
		for i, it := range pr.openItems.Rows {
			if i >= sweepProblemsMax {
				break
			}
			it.Title = truncateRunes(it.Title, sweepReasonRunes)
			res.Data.OpenItems = append(res.Data.OpenItems, it)
		}
	}
	res.Data.CodeFunctions = p2.code
	res.Data.Patterns = p2.patterns
	res.Data.Blocks = sweepBlocks(req, res, byName[sweepHopOps], byName[sweepHopFunctions], p2.logRowsByFinding, opCharts)
	res.OK = true
	if !anyData {
		res.Empty = true
		res.Message = "veri yok: " + req.Service + " için " + windowTR(req.From, req.To) + " penceresinde hiçbir bölümde veri bulunamadı — bölüm notlarına bak (okunamadı ≠ yok)"
	}
	return res, nil
}

func sweepRED(w chstore.ServiceWindowRED) healthverdict.RED {
	r := healthverdict.RED{Spans: w.Spans, P99Ms: w.P99Ms}
	if w.Spans > 0 {
		r.ErrorRate = round4(100 * float64(w.Errors) / float64(w.Spans))
	}
	return r
}

// sweepSectionLabelTR — ilerleme / kanıt metninin bölüm adı.
func sweepSectionLabelTR(n string) string {
	switch n {
	case sweepHopRED:
		return "servis RED"
	case sweepHopProblems:
		return "açık konular"
	case sweepHopOps:
		return "operasyonlar"
	case sweepHopFunctions:
		return "fonksiyonlar"
	case sweepHopPods:
		return "pod'lar"
	case sweepHopJVM:
		return "JVM"
	case sweepHopLogs:
		return "sorunluların logları"
	case sweepHopCode:
		return "kod fonksiyonları (örneklem)"
	case sweepHopPatterns:
		return "log desenleri"
	case sweepHopCorrob:
		return "gecikme doğrulaması (ham span)"
	}
	return n
}

func sweepFailState(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || cosreClassify(err) == cosreErrTimeout {
		return hopTimeout
	}
	return hopError
}

// ── evre 1 bölümleri ────────────────────────────────────────────────────

func sweepRunRED(ctx context.Context, r sweepReaders, req SweepRequest) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopRED}}
	if r.windowRED == nil {
		o.section.State, o.section.Summary, o.failed = hopSkipped, "ClickHouse okuyucusu bağlı değil", true
		return o
	}
	dur := req.To.Sub(req.From)
	elapsed := stopwatch()
	cur, err := r.windowRED(ctx, req.Service, req.Env, req.From, req.To)
	q := inlineSQL(chstore.ServiceWindowREDSQL(req.Env != ""), sweepREDArgs(req.Service, req.Env, req.From, req.To)...)
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSQL, Query: q, Window: evWin(req.From, req.To), Env: req.Env}
	if err != nil {
		ev.DurationMs = elapsed()
		o.failed, o.redErr = true, err
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("servis RED okunamadı", err)
		o.evidences = append(o.evidences, cosreHopEvidence{Hop: sweepHopRED, As: queryTracesName, Request: map[string]any{"service": req.Service}, cosreEvidence: ev})
		return o
	}
	o.red, o.redOK = cur, true
	if prior, perr := r.windowRED(ctx, req.Service, req.Env, req.From.Add(-dur), req.From); perr == nil {
		o.prior, o.priorOK = prior, true
	} else {
		o.section.Note = "önceki pencere okunamadı — baseline kıyası yok"
	}
	ev.DurationMs = elapsed()
	ev.Rows = intPtr(map[bool]int{true: 1, false: 0}[cur.Spans > 0])
	req2 := map[string]any{"service": req.Service}
	if cur.Errors > 0 {
		req2["status"] = qtStatusError
	}
	o.evidences = append(o.evidences, cosreHopEvidence{Hop: sweepHopRED, As: queryTracesName, Request: req2, cosreEvidence: ev})
	if cur.Spans == 0 {
		o.section.State = hopEmpty
		o.section.Summary = "veri yok: bu pencerede servisten span yok"
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	c := sweepRED(cur)
	o.section.Summary = fmt.Sprintf("%d span, hata oranı %%%.2f, p99 %.0f ms", cur.Spans, c.ErrorRate, cur.P99Ms)
	if o.priorOK && o.prior.Spans > 0 {
		b := sweepRED(o.prior)
		o.section.Summary += fmt.Sprintf(" (önceki pencere: %%%.2f, %.0f ms)", b.ErrorRate, o.prior.P99Ms)
	}
	return o
}

func sweepREDArgs(service, env string, from, to time.Time) []any {
	if env != "" {
		return []any{service, env, chstore.AlignBucketStart(from), to}
	}
	return []any{service, chstore.AlignBucketStart(from), to}
}

// sweepRunProblems — v0.10.1694 (operator-reported): "Açık konular", servis
// sayfası şeridinin AYNI kümesi (problem + exception/httperror + incident;
// Deps.InboxOpen + ServiceAttentionQuery → api inboxOpenAttention). Yalnız problems tablosu
// okunuyordu: exception grupları Inbox'ta dururken bölüm "açık problem yok"
// diyordu. Şerit ortamsız okur (serviceInboxHref env=), bölüm de öyle —
// env üyeliği şeridin gösterdiği satırı düşürmesin.
func sweepRunProblems(ctx context.Context, r sweepReaders, req SweepRequest) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopProblems}}
	if r.openItems == nil {
		o.section.State, o.section.Summary, o.failed = hopSkipped, "Inbox okuyucusu bağlı değil", true
		return o
	}
	oi, err := r.openItems(ctx, ServiceAttentionQuery(req.Service))
	if err != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("açık konular okunamadı", err)
		return o
	}
	o.openItems, o.openItemsOK = oi, true
	if req.Env != "" {
		o.section.Note = "env uygulanmadı: açık konular servis sayfası şeridiyle aynı kapsamda (tüm ortamlar)"
	}
	o.section.Summary = sweepOpenItemsSummary(oi, ServiceInboxHref(req.Service))
	if oi.Total == 0 {
		o.section.State = hopEmpty
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	return o
}

// sweepOpenItemsSummary — SAF: "N açık konu: P1 exception — başlık; … (Inbox: href)".
func sweepOpenItemsSummary(oi InboxOpenResult, href string) string {
	if oi.Total == 0 {
		s := "açık konu yok"
		if oi.SLOCount > 0 {
			s += fmt.Sprintf(" (SLO burn-rate %d uyarı)", oi.SLOCount)
		}
		return s
	}
	n := fmt.Sprint(oi.Total)
	if oi.Truncated || oi.ScanCapped {
		n += "+"
	}
	parts := make([]string, 0, 3)
	for i, it := range oi.Rows {
		if i >= 3 {
			break
		}
		parts = append(parts, it.Priority+" "+it.Kind+" — "+truncateRunes(it.Title, 80))
	}
	s := n + " açık konu: " + strings.Join(parts, "; ")
	if oi.SLOCount > 0 {
		s += fmt.Sprintf(" · SLO burn-rate %d uyarı", oi.SLOCount)
	}
	if href != "" {
		s += " (Inbox: " + href + ")"
	}
	return s
}

func sweepRunOps(ctx context.Context, r sweepReaders, req SweepRequest) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopOps}}
	if r.ops == nil {
		o.section.State, o.section.Summary, o.failed = hopSkipped, "ClickHouse okuyucusu bağlı değil", true
		return o
	}
	q := "GetOperationSummaryCompared FROM operation_summary_5m WHERE service_name = " + sqlQuote(req.Service) +
		" AND time >= " + sqlLiteral(req.From) + " AND time < " + sqlLiteral(req.To) + " (+ önceki eş pencere) — env yok (MV ortam boyutu taşımıyor)"
	elapsed := stopwatch()
	ops, err := r.ops(ctx, req.Service, req.From, req.To)
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Query: q, Window: evWin(req.From, req.To), DurationMs: elapsed()}
	evh := cosreHopEvidence{Hop: sweepHopOps, As: queryTracesName, Request: map[string]any{"service": req.Service, "sort_by": qtSortSlowest}}
	if err != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("operasyonlar okunamadı", err)
		evh.cosreEvidence = ev
		o.evidences = append(o.evidences, evh)
		return o
	}
	o.ops = ops
	o.scannedOps = len(ops)
	ev.Rows = intPtr(len(ops))
	var total uint64
	for _, op := range ops {
		total += op.SpanCount
	}
	var rows []sweepOpRow
	type ranked struct {
		row sweepOpRow
		c   sweepCand
	}
	var rs []ranked
	crit, warn := 0, 0
	for _, op := range ops {
		c := classifyOpRow(op, total)
		switch c.Level {
		case levelCrit:
			crit++
		case levelWarn:
			warn++
		}
		if c.Level != levelOK {
			o.cands = append(o.cands, c)
		}
		rs = append(rs, ranked{sweepOpRow{Name: op.Name, Calls: op.SpanCount, ErrorRate: round4(op.ErrorRate), P99Ms: round4(op.P99Ms),
			HasPrior: op.HasPrior, PriorErrorRate: round4(op.PriorErrorRate), PriorP99Ms: round4(op.PriorP99Ms), Level: c.Level,
			Reason: truncateRunes(strings.Join(c.Reasons, "; "), sweepReasonRunes)}, c})
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if a, b := healthverdict.LevelRank(rs[i].c.Level), healthverdict.LevelRank(rs[j].c.Level); a != b {
			return a < b
		}
		if rs[i].c.Impact != rs[j].c.Impact {
			return rs[i].c.Impact > rs[j].c.Impact
		}
		return rs[i].row.Calls > rs[j].row.Calls
	})
	for i, x := range rs {
		if i >= sweepOpsRowsMax {
			break
		}
		rows = append(rows, x.row)
	}
	o.opRows = rows
	if len(o.cands) > 0 {
		evh.Request["operation"] = o.cands[0].Name
		for _, c := range o.cands {
			if c.Level == levelCrit {
				evh.Request["operation"] = c.Name
				break
			}
		}
	}
	evh.cosreEvidence = ev
	o.evidences = append(o.evidences, evh)
	if len(ops) == 0 {
		o.section.State, o.section.Summary = hopEmpty, "veri yok: operation_summary_5m'de bu pencerede operasyon yok"
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	o.section.Summary = fmt.Sprintf("%d operasyon tarandı: %d kritik, %d uyarı (hacim tabanı > %d span)", len(ops), crit, warn, healthverdict.VolumeFloor)
	return o
}

func sweepRunFunctions(ctx context.Context, r sweepReaders, req SweepRequest) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopFunctions}, fnSource: sweepFnSourceNone}
	if r.fnRollup == nil && r.fnSpans == nil {
		o.section.State, o.section.Summary, o.failed = hopSkipped, "ClickHouse okuyucusu bağlı değil", true
		return o
	}
	dur := req.To.Sub(req.From)
	elapsed := stopwatch()
	var notes []string
	if r.fnRollup != nil {
		cur, err := r.fnRollup(ctx, req.Service, req.From, req.To)
		if err == nil {
			prior, perr := r.fnRollup(ctx, req.Service, req.From.Add(-dur), req.From)
			if perr != nil {
				notes = append(notes, "önceki pencere okunamadı — baseline kıyası yok")
			}
			o.fnSource = sweepFnSourceRollup
			o.fnSeries = map[string]chstore.RollupSeries{}
			curAgg, priorAgg := sweepRollupAgg(cur), sweepRollupAgg(prior)
			priorBuckets := sweepRollupBucketP99(prior)
			for _, s := range cur {
				o.fnSeries[s.Group] = s
			}
			var total uint64
			for _, a := range curAgg {
				total += a.Spans
			}
			ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, DurationMs: elapsed(), Window: evWin(req.From, req.To),
				Query: fmt.Sprintf("QueryRollupRED FROM rollup_spans_wide_* WHERE service_name = %s GROUP BY GROUPING SETS ((tb, function_code), (function_code)) (≤%d grup; p99 = pencere birleşik histogram kestirimi; + önceki eş pencere) — env yok",
					sqlQuote(req.Service), sweepFnGroups), Rows: intPtr(len(curAgg))}
			o.sweepFnFinish(req, curAgg, priorAgg, priorBuckets, perr == nil, total, notes)
			o.evidences = append(o.evidences, cosreHopEvidence{Hop: sweepHopFunctions, As: queryTracesName, Request: map[string]any{"service": req.Service}, cosreEvidence: ev})
			return o
		}
		if !errors.Is(err, chstore.ErrRollupTablesMissing) {
			notes = append(notes, cosreErrMsg("geniş rollup okunamadı, ham span kırılımına düşüldü", err))
		}
	}
	if r.fnSpans == nil {
		o.section.State, o.section.Summary = hopSkipped, "fonksiyon verisi yok: rollup yok ve ham okuyucu bağlı değil"
		return o
	}
	rows, err := r.fnSpans(ctx, req.Service, req.From, req.To)
	q := "BusinessBreakdown: SELECT FUNCTION_CODE, count(), countIf(status_code = 'error') FROM spans WHERE service_name = " + sqlQuote(req.Service) +
		" AND time >= " + sqlLiteral(req.From) + " AND time < " + sqlLiteral(req.To) + " GROUP BY 1 LIMIT 8 SETTINGS max_execution_time = 5 — env yok, p99 yok"
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSQL, Query: q, Window: evWin(req.From, req.To), DurationMs: elapsed()}
	evh := cosreHopEvidence{Hop: sweepHopFunctions, As: queryTracesName, Request: map[string]any{"service": req.Service}}
	if err != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("fonksiyon kırılımı okunamadı", err)
		o.section.Note = strings.Join(notes, "; ")
		evh.cosreEvidence = ev
		o.evidences = append(o.evidences, evh)
		return o
	}
	o.fnSource = sweepFnSourceSpans
	agg := map[string]healthverdict.RED{}
	var total uint64
	for _, b := range rows {
		agg[b.Value] = healthverdict.RED{Spans: b.Calls, ErrorRate: round4(b.ErrPct)}
		total += b.Calls
	}
	ev.Rows = intPtr(len(rows))
	evh.cosreEvidence = ev
	notes = append(notes, "ham span kırılımı (ilk 8 kod, hataya göre); baseline ve p99 yok — yalnız mutlak hata eşiği (≥%5)")
	o.sweepFnFinish(req, agg, nil, nil, false, total, notes)
	if len(rows) == 0 {
		o.fnSource = sweepFnSourceNone
	}
	o.evidences = append(o.evidences, evh)
	return o
}

// sweepFnFinish — fonksiyon satırları + adaylar + bölüm özeti (iki kaynak ortak).
func (o *sweepOutcome) sweepFnFinish(req SweepRequest, cur, prior map[string]healthverdict.RED, priorBuckets map[string][]float64, priorOK bool, total uint64, notes []string) {
	crit, warn := 0, 0
	for _, code := range sortedKeys(cur) {
		c := cur[code]
		b, has := prior[code]
		has = has && priorOK && b.Spans > 0
		cand := classifyFnRow(code, c, b, has, total, priorBuckets[code])
		switch cand.Level {
		case levelCrit:
			crit++
		case levelWarn:
			warn++
		}
		if cand.Level != levelOK {
			o.cands = append(o.cands, cand)
		}
		row := sweepFnRow{Code: code, Calls: c.Spans, ErrorRate: c.ErrorRate, P99Ms: round4(c.P99Ms), HasPrior: has, Level: cand.Level,
			Reason: truncateRunes(strings.Join(cand.Reasons, "; "), sweepReasonRunes)}
		row.Errors = uint64(math.Round(float64(c.Spans) * c.ErrorRate / 100))
		if has {
			row.PriorErrorRate, row.PriorP99Ms = b.ErrorRate, round4(b.P99Ms)
		}
		o.fnRows = append(o.fnRows, row)
	}
	sort.SliceStable(o.fnRows, func(i, j int) bool {
		if a, b := healthverdict.LevelRank(o.fnRows[i].Level), healthverdict.LevelRank(o.fnRows[j].Level); a != b {
			return a < b
		}
		return o.fnRows[i].Calls > o.fnRows[j].Calls
	})
	o.scannedFns = len(cur)
	o.section.Note = strings.Join(notes, "; ")
	if len(cur) == 0 {
		o.section.State = hopEmpty
		o.section.Summary = "fonksiyon verisi yok: span'ler bu pencerede FUNCTION_CODE taşımıyor"
		return
	}
	o.anyDataFound = true
	o.section.State = hopOK
	o.section.Summary = fmt.Sprintf("%d fonksiyon kodu tarandı (%s): %d kritik, %d uyarı", len(cur), o.fnSource, crit, warn)
}

// sweepRollupAgg — SAF: rollup serileri → kod başına RED. v0.10.1299: p99
// PENCERE düzeyi birleşik kestirim (RollupSeries.Total — 20'lik histogramın
// tüm pencere toplamından); Total yoksa (eski okuyucu / test sahtesi) çağrı-
// ağırlıklı kova ortalamasına düşer — bir p99 değildir, yalnız geri dönüş.
func sweepRollupAgg(series []chstore.RollupSeries) map[string]healthverdict.RED {
	out := map[string]healthverdict.RED{}
	for _, s := range series {
		if strings.TrimSpace(s.Group) == "" {
			continue
		}
		var calls, errs uint64
		var p99w float64
		for _, p := range s.Points {
			calls += p.Calls
			errs += p.Errors
			p99w += p.P99Ms * float64(p.Calls)
		}
		r := healthverdict.RED{Spans: calls}
		if s.Total != nil {
			calls, errs = s.Total.Calls, s.Total.Errors
			r.Spans = calls
		}
		if calls > 0 {
			r.ErrorRate = round4(100 * float64(errs) / float64(calls))
			r.P99Ms = round4(p99w / float64(calls))
			if s.Total != nil {
				r.P99Ms = round4(s.Total.P99Ms)
			}
		}
		out[s.Group] = r
	}
	return out
}

// sweepRollupBucketP99 — SAF: kod başına kova p99 dizisi (oynaklık kolu).
func sweepRollupBucketP99(series []chstore.RollupSeries) map[string][]float64 {
	out := map[string][]float64{}
	for _, s := range series {
		for _, p := range s.Points {
			if p.Calls > 0 {
				out[s.Group] = append(out[s.Group], p.P99Ms)
			}
		}
	}
	return out
}

// sweepMappedClusters — SAF: servisin span cluster değerleri → kayıtlı
// Remote Cluster referansları (qmResolveCluster eşlemesi; tavan sweepMappedMax).
func sweepMappedClusters(vals []string, all []ClusterRef) []ClusterRef {
	var mapped []ClusterRef
	seen := map[string]bool{}
	for _, v := range vals {
		for _, c := range all {
			match := strings.EqualFold(c.Name, v) || strings.EqualFold(c.ID, v)
			for _, sv := range c.SpanValues {
				if strings.EqualFold(sv, v) {
					match = true
				}
			}
			if match && !seen[c.ID+"\x00"+c.Name] && len(mapped) < sweepMappedMax {
				seen[c.ID+"\x00"+c.Name] = true
				mapped = append(mapped, c)
			}
		}
	}
	return mapped
}

// sweepPodClusterRefs — pod okumasının cluster referansları: servisin
// span'lerinde eşlenen cluster'lar, yoksa etkinlerin ilk sweepAllClusterMax'ı
// (+ notlar). v0.10.1288: tembel node kontrolü (ReadNodeCheck) aynı kümeyi okur.
func sweepPodClusterRefs(ctx context.Context, r sweepReaders, req SweepRequest, all []ClusterRef) ([]string, []string) {
	var notes []string
	var refs []string
	if r.clusterValues != nil {
		vals, err := r.clusterValues(ctx, req.Service, req.From, req.To)
		if err != nil && len(vals) == 0 {
			notes = append(notes, "servisin cluster'ı span'lerden çözülemedi")
		}
		for _, c := range sweepMappedClusters(vals, all) {
			refs = append(refs, firstNonEmpty(c.ID, c.Name))
		}
	}
	if len(refs) == 0 {
		n := len(all)
		if n > sweepAllClusterMax {
			n = sweepAllClusterMax
			notes = append(notes, fmt.Sprintf("servisin span'lerinde eşlenen cluster yok — etkin cluster'ların ilk %d'i tarandı (%d kayıtlı)", sweepAllClusterMax, len(all)))
		} else {
			notes = append(notes, "servisin span'lerinde eşlenen cluster yok — tüm etkin cluster'lar tarandı")
		}
		for _, c := range all[:n] {
			refs = append(refs, firstNonEmpty(c.ID, c.Name))
		}
	}
	return refs, notes
}

func sweepRunPods(ctx context.Context, r sweepReaders, req SweepRequest) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopPods}}
	var all []ClusterRef
	if r.clusters != nil {
		all = r.clusters()
	}
	if r.servicePods == nil || len(all) == 0 {
		o.section.State, o.section.Summary = hopSkipped, "Thanos Remote Cluster yapılandırılmamış — pod CPU/bellek/restart okunamaz"
		return o
	}
	refs, notes := sweepPodClusterRefs(ctx, r, req, all)
	ask := sweepChecksOf(req)[SweepCheckK8s]
	o.k8sWaitAsk = ask
	res, err := r.servicePods(ctx, req.Service, refs, req.From, req.To, ServicePodsOpts{WaitingReasons: ask})
	if err != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(err), cosreErrMsg("pod'lar okunamadı", err)
		o.section.Note = strings.Join(notes, "; ")
		return o
	}
	if !res.Thanos {
		o.section.State, o.section.Summary = hopSkipped, "Thanos'ta etkin cluster yok"
		return o
	}
	var states []string
	crit, warn := 0, 0
	type pr struct {
		row sweepPodRow
		c   sweepCand
	}
	var prs []pr
	for _, cl := range res.Clusters {
		o.clusters++
		if cl.Err != "" {
			states = append(states, hopError)
			notes = append(notes, "cluster "+cl.Name+" okunamadı: "+truncateRunes(cl.Err, 120))
			o.failed = true
			continue
		}
		if !cl.RestartsOK && len(cl.Rows) > 0 {
			notes = append(notes, "cluster "+cl.Name+": penceredeki restart artışı okunamadı — restart puanlanmadı")
		}
		if cl.Truncated {
			notes = append(notes, "cluster "+cl.Name+": pod listesi Thanos tavanına takıldı")
		}
		if ask && len(cl.Rows) > 0 { // v0.10.1287 — k8s kontrolü bekleme sebebi
			if cl.WaitingOK {
				o.k8sWaitOK = true
			} else {
				o.k8sNotes = append(o.k8sNotes, "cluster "+cl.Name+": bekleme sebebi okunamadı (okunamadı ≠ yok)")
			}
		}
		var worst *sweepCand
		for _, row := range cl.Rows {
			if row.Cluster == "" {
				row.Cluster = cl.Name
			}
			var rs *float64
			if cl.RestartsOK {
				v := cl.Restarts[row.Namespace+"\x00"+row.Pod]
				rs = &v
			}
			st := K8sPodState{Cluster: row.Cluster, ClusterID: cl.ID, Namespace: row.Namespace, Pod: row.Pod, Phase: row.Phase,
				LastTermReason: row.LastTermReason, RestartsWindow: rs}
			if cl.WaitingOK {
				st.WaitingReason = cl.Waiting[row.Namespace+"\x00"+row.Pod]
			}
			o.k8s = append(o.k8s, st)
			c := classifySweepPod(row, rs)
			c.ClusterID = cl.ID
			switch c.Level {
			case levelCrit:
				crit++
			case levelWarn:
				warn++
			}
			if c.Level != levelOK {
				o.cands = append(o.cands, c)
			}
			if worst == nil || healthverdict.LevelRank(c.Level) < healthverdict.LevelRank(worst.Level) ||
				(c.Level == worst.Level && c.Impact > worst.Impact) {
				cc := c
				worst = &cc
			}
			p := sweepPodRow{Cluster: row.Cluster, Namespace: row.Namespace, Pod: row.Pod, Phase: row.Phase, RestartsWindow: rs,
				LastTermReason: row.LastTermReason, Level: c.Level, Reason: truncateRunes(strings.Join(c.Reasons, "; "), sweepReasonRunes)}
			if row.CPUPct > 0 {
				v := round4(row.CPUPct)
				p.CPUPct = &v
			}
			if row.MemPct > 0 {
				v := round4(row.MemPct)
				p.MemPct = &v
			}
			prs = append(prs, pr{p, c})
		}
		o.scannedPods += len(cl.Rows)
		st := hopOK
		if len(cl.Rows) == 0 {
			st = hopEmpty
		}
		states = append(states, st)
		if len(o.evidences) == 0 {
			ev := cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Query: cl.Query, Window: evWin(req.From, req.To),
				Cluster: cl.Name, Rows: intPtr(len(cl.Rows)), DurationMs: cl.DurationMs}
			rq := map[string]any{"cluster": cl.ID, "service": req.Service}
			if worst != nil {
				rq["namespace"], rq["pod"] = worst.Namespace, worst.Name
			}
			o.evidences = append(o.evidences, cosreHopEvidence{Hop: sweepHopPods, As: "pod", Request: rq, cosreEvidence: ev})
		}
	}
	sort.SliceStable(prs, func(i, j int) bool {
		if a, b := healthverdict.LevelRank(prs[i].c.Level), healthverdict.LevelRank(prs[j].c.Level); a != b {
			return a < b
		}
		if prs[i].c.Impact != prs[j].c.Impact {
			return prs[i].c.Impact > prs[j].c.Impact
		}
		return prs[i].row.Pod < prs[j].row.Pod
	})
	for i, x := range prs {
		if i >= sweepPodRowsMax {
			notes = append(notes, fmt.Sprintf("pod tablosu ilk %d satır (toplam %d)", sweepPodRowsMax, len(prs)))
			break
		}
		o.pods = append(o.pods, x.row)
	}
	o.section.State = pivotMergeStates(states)
	if o.section.State == hopPartial || o.section.State == hopError || o.section.State == hopTimeout {
		o.failed = true
	}
	o.section.Note = strings.Join(notes, "; ")
	if o.scannedPods == 0 {
		if o.section.State == hopOK {
			o.section.State = hopEmpty
		}
		o.section.Summary = "veri yok: Thanos'ta " + firstNonEmpty(res.PodRegex, req.Service) + " kalıbına uyan pod yok"
		return o
	}
	o.anyDataFound = true
	o.section.Summary = fmt.Sprintf("%d pod, %d cluster: %d kritik, %d uyarı", o.scannedPods, o.clusters, crit, warn)
	return o
}

func sweepRunJVM(ctx context.Context, r sweepReaders, req SweepRequest, alerts chstore.RuntimeAlertConfig) sweepOutcome {
	o := sweepOutcome{section: SweepSection{Name: sweepHopJVM}}
	if r.runtime == nil {
		o.section.State, o.section.Summary = hopSkipped, "JVM runtime okuyucusu bağlı değil"
		return o
	}
	to := req.To
	from := to.Add(-chstore.RuntimePodWindow)
	elapsed := stopwatch()
	heap, herr := r.runtime.JVMHeapPodUsage(ctx, from, to)
	var gc []chstore.CapacitySample
	var gerr error
	if herr == nil || !errors.Is(herr, context.DeadlineExceeded) {
		gc, gerr = r.runtime.JVMGCPodPause(ctx, from, to)
	}
	src := map[bool]string{true: "VictoriaMetrics", false: "ClickHouse metric_points"}[r.runtimeSource == evSourceVM]
	q := qmRuntimeQueryText("jvm_heap", src, req.Service, from, to) + "\n" + qmRuntimeQueryText("jvm_gc", "", req.Service, from, to)
	ev := cosreEvidence{Source: r.runtimeSource, QueryKind: evKindSummary, Query: q, Window: evWin(from, to), DurationMs: elapsed()}
	evh := cosreHopEvidence{Hop: sweepHopJVM, As: queryMetricsName, Request: map[string]any{"service": req.Service, "family": "jvm_heap"}}
	if herr != nil && gerr != nil {
		o.failed = true
		o.section.State, o.section.Summary = sweepFailState(herr), cosreErrMsg("JVM metriği okunamadı", herr)
		evh.cosreEvidence = ev
		o.evidences = append(o.evidences, evh)
		return o
	}
	pick := func(in []chstore.CapacitySample) map[string]*chstore.CapacitySample {
		m := map[string]*chstore.CapacitySample{}
		for i := range in {
			if strings.EqualFold(in[i].Instance, req.Service) && in[i].Subkey != "" {
				m[in[i].Subkey] = &in[i]
			}
		}
		return m
	}
	hm, gm := pick(heap), pick(gc)
	pods := map[string]bool{}
	for k := range hm {
		pods[k] = true
	}
	for k := range gm {
		pods[k] = true
	}
	crit, warn := 0, 0
	for _, pod := range sortedKeys(pods) {
		row := classifyJVM(req.Service, pod, hm[pod], gm[pod], alerts)
		o.jvm = append(o.jvm, row)
		switch row.Level {
		case levelCrit:
			crit++
		case levelWarn:
			warn++
		}
		if row.Level != levelOK {
			o.cands = append(o.cands, sweepJVMCand(row))
		}
	}
	o.scannedJVM = len(o.jvm)
	ev.Rows = intPtr(len(o.jvm))
	evh.cosreEvidence = ev
	o.evidences = append(o.evidences, evh)
	if herr != nil || gerr != nil {
		o.failed = true
		o.section.Note = "heap ya da GC okumalarından biri başarısız — tablo eksik"
	}
	if len(o.jvm) == 0 {
		o.section.State = hopEmpty
		o.section.Summary = "veri yok (OTel JVM) — servis için son 10 dk jvm.memory/jvm.gc metriği yok; JMX Thanos'ta olabilir (Service › Infrastructure)"
		return o
	}
	o.anyDataFound = true
	o.section.State = hopOK
	if o.failed {
		o.section.State = hopPartial
	}
	o.section.Summary = fmt.Sprintf("%d pod JVM (son 10 dk): %d kritik, %d uyarı", len(o.jvm), crit, warn)
	return o
}

// ── evre 2 ──────────────────────────────────────────────────────────────

type sweepP2 struct {
	outcomes         []sweepOutcome
	logs             map[int]*SweepLogs
	logRowsByFinding map[int][]qlRow
	code             []chstore.CodeFunctionSlice
	patterns         []sweepPattern
}

// sweepPhase2Deadline — SAF: evre 2'nin süresi = kalan − pay (≤ 0 → koşmaz).
func sweepPhase2Deadline(remaining time.Duration) time.Duration {
	if r := remaining - sweepPhase2Margin; r > 0 {
		return r
	}
	return 0
}

func sweepPhase2(ctx context.Context, r sweepReaders, req SweepRequest, findings []SweepFinding, opsOut *sweepOutcome, deadline time.Time) sweepP2 {
	p := sweepP2{logs: map[int]*SweepLogs{}, logRowsByFinding: map[int][]qlRow{}}
	budget := sweepPhase2Deadline(time.Until(deadline))
	logSec := SweepSection{Name: sweepHopLogs}
	type res struct {
		kind string // logs | code | patterns
		i    int
		logs *SweepLogs
		ev   *cosreHopEvidence
		code []chstore.CodeFunctionSlice
		pats []sweepPattern
		err  error
		ms   int64
	}
	type task struct {
		kind string
		i    int
		run  func(context.Context) res
	}
	var tasks []task
	if r.logs != nil {
		for i := range findings {
			f := findings[i]
			if f.Kind == sweepKindDeploy {
				// v0.10.1287 — deploy bulgusunun "logu" yok (ad bir sürüm/rollout,
				// operasyon değil); okuma yapılmaz, bütçe harcanmaz.
				p.logs[i] = &SweepLogs{State: hopSkipped, Summary: "deploy bulgusu — log okunmaz"}
				continue
			}
			tasks = append(tasks, task{"logs", i, func(c context.Context) res {
				l, ev := sweepFindingLogs(c, r, req, f)
				return res{kind: "logs", i: i, logs: l, ev: ev}
			}})
		}
	}
	var problemOps []string
	if opsOut != nil {
		for _, f := range findings {
			if f.Kind == sweepKindOperation && len(problemOps) < 3 {
				problemOps = append(problemOps, f.Name)
			}
		}
	}
	codeRun := req.Deep && r.codeSample != nil && len(problemOps) > 0
	if codeRun {
		tasks = append(tasks, task{"code", -1, func(c context.Context) res {
			el := stopwatch()
			cs, err := r.codeSample(c, req.Service, problemOps, req.From, req.To)
			return res{kind: "code", code: cs, err: err, ms: el()}
		}})
	}
	patRun := req.Deep && !req.SkipPatterns && r.patterns != nil
	if patRun {
		tasks = append(tasks, task{"patterns", -1, func(c context.Context) res {
			el := stopwatch()
			hits, err := r.patterns(c, sweepPatternWindow)
			if err != nil {
				return res{kind: "patterns", err: err, ms: el()}
			}
			kept, _ := filterLogPatterns(hits, req.Service, "all", nil, sweepPatternsMax)
			var out []sweepPattern
			for _, h := range kept {
				out = append(out, sweepPattern{Pattern: truncateRunes(h.Pattern, 120), Kind: h.Kind, Ratio: round4(h.Ratio), Count: h.CurrentCount,
					Sample: promptfmt.FenceSafe(truncateRunes(h.Sample, 160))})
			}
			return res{kind: "patterns", pats: out, ms: el()}
		}})
	}
	finish := func() {
		// Bölümleri sabit sırayla ekle.
		if len(findings) > 0 || r.logs == nil {
			p.outcomes = append(p.outcomes, sweepOutcome{section: logSec, failed: logSec.State == hopError || logSec.State == hopTimeout})
		}
	}
	if len(tasks) == 0 {
		switch {
		case r.logs == nil:
			logSec.State, logSec.Summary = hopSkipped, "log arka ucu yapılandırılmamış"
		case len(findings) == 0:
			logSec.State, logSec.Summary = hopSkipped, "sorunlu bulgu yok — log okunmadı"
		default: // v0.10.1287 — yalnız deploy bulguları (logsuz tür)
			logSec.State, logSec.Summary = hopSkipped, "yalnız deploy bulgusu — log okunmadı"
		}
		finish()
		return p
	}
	if budget <= 0 {
		logSec.State, logSec.Summary = hopSkipped, "süre bütçesi doldu"
		for i := range findings {
			p.logs[i] = &SweepLogs{State: hopSkipped, Summary: "süre bütçesi doldu"}
		}
		finish()
		return p
	}
	reportProgress(ctx, "Log", fmt.Sprintf("sorunlu %d bulgunun logları okunuyor", len(findings)))
	pctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	ch := make(chan res, len(tasks))
	sem := make(chan struct{}, sweepLogParallel)
	for _, t := range tasks {
		go func(t task) {
			select {
			case sem <- struct{}{}:
			case <-pctx.Done():
				ch <- res{kind: t.kind, i: t.i, err: pctx.Err(), logs: &SweepLogs{State: hopSkipped, Summary: "süre bütçesi doldu"}}
				return
			}
			defer func() { <-sem }()
			tctx, tc := context.WithTimeout(pctx, sweepCapLogs)
			defer tc()
			ch <- t.run(tctx)
		}(t)
	}
	elapsed := stopwatch()
	var logEv *cosreHopEvidence
	codeSec := SweepSection{Name: sweepHopCode}
	patSec := SweepSection{Name: sweepHopPatterns}
	got := 0
	okLogs, failLogs := 0, 0
	timer := time.NewTimer(budget)
	defer timer.Stop()
collect:
	for got < len(tasks) {
		select {
		case x := <-ch:
			got++
			switch x.kind {
			case "logs":
				if x.logs == nil {
					x.logs = &SweepLogs{State: hopError, Summary: "okunamadı"}
				}
				p.logs[x.i] = x.logs
				p.logRowsByFinding[x.i] = x.logs.Rows
				switch x.logs.State {
				case hopOK, hopEmpty:
					okLogs++
				default:
					failLogs++
				}
				if logEv == nil && x.ev != nil {
					logEv = x.ev
				}
			case "code":
				codeSec.DurationMs = x.ms
				switch {
				case x.err != nil:
					codeSec.State, codeSec.Summary = sweepFailState(x.err), cosreErrMsg("code.* örneklemi okunamadı", x.err)
				case len(x.code) == 0:
					codeSec.State = hopEmpty
					codeSec.Summary = "fonksiyon (code.*) verisi yok: span'ler code.* özniteliği taşımıyor"
				default:
					codeSec.State = hopOK
					p.code = x.code
					codeSec.Summary = fmt.Sprintf("örneklem (sampled, ≤5000 hatalı/yavaş span, %s): %d kod fonksiyonu; en çok hata: %s",
						strings.Join(problemOps, ", "), len(x.code), x.code[0].Function)
				}
			case "patterns":
				patSec.DurationMs = x.ms
				switch {
				case x.err != nil:
					patSec.State, patSec.Summary = sweepFailState(x.err), cosreErrMsg("log desenleri okunamadı", x.err)
				case len(x.pats) == 0:
					patSec.State, patSec.Summary = hopEmpty, "son 15 dk'da bu servise ait yeni/patlayan log deseni yok"
				default:
					patSec.State = hopOK
					p.patterns = x.pats
					patSec.Summary = fmt.Sprintf("%d yeni/patlayan desen; ilki: %s", len(x.pats), truncateRunes(x.pats[0].Pattern, 80))
				}
			}
		case <-timer.C:
			break collect
		case <-ctx.Done():
			break collect
		}
	}
	for i := range findings {
		if _, ok := p.logs[i]; !ok && r.logs != nil {
			p.logs[i] = &SweepLogs{State: hopSkipped, Summary: "süre bütçesi doldu"}
			failLogs++
		}
	}
	logSec.DurationMs = elapsed()
	switch {
	case r.logs == nil:
		logSec.State, logSec.Summary = hopSkipped, "log arka ucu yapılandırılmamış"
	case len(findings) == 0:
		logSec.State, logSec.Summary = hopSkipped, "sorunlu bulgu yok — log okunmadı"
	case failLogs == 0:
		logSec.State = hopOK
		logSec.Summary = fmt.Sprintf("%d sorunlu bulgunun logları okundu", okLogs)
	case okLogs == 0:
		logSec.State = hopError
		logSec.Summary = "sorunluların logları okunamadı ya da süre doldu — okunamadı ≠ log yok"
	default:
		logSec.State = hopPartial
		logSec.Summary = fmt.Sprintf("%d bulgunun logları okundu, %d okunamadı", okLogs, failLogs)
	}
	lo := sweepOutcome{section: logSec, failed: logSec.State == hopError || logSec.State == hopPartial || logSec.State == hopTimeout}
	if logEv != nil {
		lo.evidences = []cosreHopEvidence{*logEv}
	}
	for _, l := range p.logs {
		if len(l.Rows) > 0 {
			lo.anyDataFound = true
		}
	}
	p.outcomes = append(p.outcomes, lo)
	if codeRun {
		if codeSec.State == "" {
			codeSec.State, codeSec.Summary = hopTimeout, "süre bütçesi doldu"
		}
		p.outcomes = append(p.outcomes, sweepOutcome{section: codeSec, failed: codeSec.State == hopError || codeSec.State == hopTimeout})
	}
	if patRun {
		if patSec.State == "" {
			patSec.State, patSec.Summary = hopTimeout, "süre bütçesi doldu"
		}
		patSec.Note = "desen dedektörü 'şimdi'ye göre son 15 dk'yı okur (örneklem, Drain)"
		p.outcomes = append(p.outcomes, sweepOutcome{section: patSec, failed: patSec.State == hopError || patSec.State == hopTimeout})
	}
	return p
}

// sweepFindingLogs — bir bulgunun logları. pod/JVM → pod süzgeçli ERROR+
// loglar; operasyon/fonksiyon → en yeni hatalı trace'in logları.
func sweepFindingLogs(ctx context.Context, r sweepReaders, req SweepRequest, f SweepFinding) (*SweepLogs, *cosreHopEvidence) {
	backend := r.logs.Backend()
	source := logsBackendSource(r.logs)
	var page *logstore.Page
	var err error
	var lf logstore.Filter
	traceID := ""
	elapsed := stopwatch()
	switch f.Kind {
	case sweepKindPod, sweepKindJVM, sweepKindK8s: // v0.10.1287 — k8s bulgusu da bir pod
		lf = logstore.Filter{Service: req.Service, Pod: f.Name, SeverityMin: qlOTelErrorFloor, Limit: sweepLogFetch, From: req.From, To: req.To}
		page, err = logstore.SearchWithTimeout(ctx, r.logs, lf, sweepCapLogs)
	default:
		if r.searchTraces == nil {
			return &SweepLogs{State: hopSkipped, Summary: "trace okuyucusu bağlı değil — hatalı trace bulunamadı"}, nil
		}
		key := "name"
		if f.Kind == sweepKindFunction {
			key = "FUNCTION_CODE"
		}
		filters := []chstore.FilterExpr{{Key: key, Op: "=", Values: []string{f.Name}}}
		from, to := req.From, req.To
		if gate, gerr := applySearchGate(int(to.Sub(from)/time.Second), 2, filters, true, false, chstore.AttrIndexAvailable()); gerr == nil && gate.Reason != "" {
			from = to.Add(-time.Duration(gate.RangeS) * time.Second)
		}
		rows, _, _, terr := r.searchTraces(ctx, chstore.TraceFilter{Service: req.Service, Env: req.Env, From: from, To: to, HasError: true,
			Limit: 2, Sort: "time", Order: "desc", CountMode: "skip", Filters: filters})
		if terr != nil {
			return &SweepLogs{State: sweepFailState(terr), Summary: cosreErrMsg("hatalı trace araması başarısız", terr)}, nil
		}
		if len(rows) == 0 {
			return &SweepLogs{State: hopEmpty, Summary: "bu pencerede hatalı trace yok — log aranmadı"}, nil
		}
		traceID = strings.ToLower(rows[0].TraceID)
		st := time.Unix(0, rows[0].StartTime).UTC()
		lo, hi := st.Add(-sweepLogPad), st.Add(time.Duration(rows[0].DurationMs*float64(time.Millisecond))).Add(sweepLogPad)
		lf = logstore.Filter{TraceID: traceID, From: lo, To: hi, Limit: sweepLogFetch}
		page, err = logstore.LogsForTrace(ctx, r.logs, traceID, lo, hi, sweepLogFetch)
	}
	ev := cosreEvidence{Source: source, QueryKind: evKindSummary, Query: qlQueryText(backend, lf), Window: evWin(lf.From, lf.To), DurationMs: elapsed()}
	rq := map[string]any{"service": req.Service, "severity": qlSeverityError}
	if traceID != "" {
		rq = map[string]any{"trace_id": traceID}
	}
	evh := &cosreHopEvidence{Hop: sweepHopLogs, As: queryLogsName, Request: rq}
	if err != nil {
		evh.cosreEvidence = ev
		st := sourcestate.FromError(logsSource, backend, err)
		if errors.Is(err, logstore.ErrBackendSlow) || logsDegradedState(st.State) {
			return &SweepLogs{State: hopDegraded, TraceID: traceID, Summary: "log arka ucu şu an okunamadı (" + string(st.State) + ") — okunamadı ≠ log yok"}, evh
		}
		return &SweepLogs{State: hopError, TraceID: traceID, Summary: cosreErrMsg(source+" log okuması başarısız", err)}, evh
	}
	var recs []*logstore.LogRecord
	if page != nil {
		for _, rec := range page.Logs {
			if rec != nil {
				recs = append(recs, rec)
			}
		}
	}
	sort.SliceStable(recs, func(i, j int) bool {
		if recs[i].Severity != recs[j].Severity {
			return recs[i].Severity > recs[j].Severity
		}
		return recs[i].Timestamp > recs[j].Timestamp
	})
	m := logFieldMapping(ctx, r.logs, true)
	out := &SweepLogs{TraceID: traceID}
	errN := 0
	for _, rec := range recs {
		if rec.Severity >= qlOTelErrorFloor {
			errN++
		}
		if len(out.Rows) >= sweepLogKeep {
			continue
		}
		out.Rows = append(out.Rows, qlRow{TsMs: rec.Timestamp / 1e6, Severity: logSeverityLabel(rec), Service: rec.ServiceName,
			Pod: logstore.RoleValue(rec, logstore.RolePod, m), TraceID: rec.TraceID,
			Body: promptfmt.FenceSafe(truncateRunes(rec.Body, qlBodyMaxRunes))})
	}
	ev.Rows = intPtr(len(out.Rows))
	evh.cosreEvidence = ev
	if len(out.Rows) == 0 {
		out.State = hopEmpty
		if traceID != "" {
			out.Summary = "veri yok: hatalı trace " + traceID[:8] + " için log yok (" + source + ")"
		} else {
			out.Summary = "veri yok: pod " + f.Name + " için pencerede ERROR+ log yok (" + source + ")"
		}
		return out, evh
	}
	out.State = hopOK
	// v0.10.1299 — ham JSON gövde anlatıma basılmaz: kısa insan satırı.
	first := LogBodyPreview(out.Rows[0].Body, sweepBodyRunes)
	if traceID != "" {
		out.Summary = fmt.Sprintf("hatalı trace %s: %d log (%d ERROR+); ilki: %s", traceID[:8], len(recs), errN, first)
	} else {
		out.Summary = fmt.Sprintf("%d ERROR+ log; ilki: %s", len(recs), first)
	}
	return out, evh
}

// ── bloklar ─────────────────────────────────────────────────────────────

// sweepBlocks — ≤6 blok: özet tablosu (Bulgu · Tür · Seviye · Gerekçe), sonra
// bulgu sırasıyla operasyon/fonksiyon grafiği, pod tablosu, JVM tablosu, log
// tablosu. Ek sorgu yok (sonucun kendi satırları).
func sweepBlocks(req SweepRequest, res SweepResult, ops, fns *sweepOutcome, logRows map[int][]qlRow, opCharts sweepOpChartSet) []pivotBlock {
	out := []pivotBlock{}
	add := func(b pivotBlock) {
		if len(out) < sweepMaxBlocks {
			out = append(out, b)
		}
	}
	win := EvBlockWindow{FromNs: req.From.UnixNano(), ToNs: req.To.UnixNano()}
	sum := EvTablePayload{Title: req.Service + " · sağlık taraması", Columns: []EvTableColumn{
		{Key: "finding", Label: "Bulgu", Kind: "text"}, {Key: "kind", Label: "Tür", Kind: "text"},
		{Key: "level", Label: "Seviye", Kind: "level"}, {Key: "reason", Label: "Gerekçe", Kind: "text"}}, Rows: [][]any{}}
	for _, f := range res.Findings {
		sum.Rows = append(sum.Rows, []any{f.Name, sweepKindTR(f.Kind), f.Level, truncateRunes(strings.Join(f.Reasons, "; "), evBlockBodyRunes-1)})
	}
	if len(sum.Rows) == 0 {
		sum.Rows = append(sum.Rows, []any{req.Service, "servis", levelOK, "taranan bölümlerde eşik aşan bulgu yok"})
	}
	add(pivotBlock{Hop: sweepHopRED, Type: blocks.TypeTable, Payload: sum})
	podDone, jvmDone := false, false
	for _, f := range res.Findings {
		switch f.Kind {
		case sweepKindOperation:
			// v0.10.1391 — yalnız seçilen operasyonlar çizilir: giriş (SERVER/
			// CONSUMER) bulgusu varken giden çağrı (CLIENT/PRODUCER) grafiği yok.
			if ops == nil || !opCharts.isChosen(f.Name) {
				continue
			}
			kindTag := sweepOpKindTag(opCharts.kinds[f.Name])
			for _, op := range ops.ops {
				if op.Name != f.Name {
					continue
				}
				// v0.10.1299 — ince (1 dk tabanlı) seri varsa o; yoksa 5 dk sparkline.
				var corr *chstore.SlowSpanCorroboration
				if cr, ok := ops.corrob[f.Name]; ok {
					corr = &cr
				}
				c, ok := EvChartPayload{}, false
				if pts := opCharts.pts[f.Name]; len(pts) > 0 {
					step, _ := sweepChartStep(req.To.Sub(req.From))
					c, ok = sweepOpLatencyChart(f.Name, kindTag, pts, step, op.PriorP99Ms, corr, win)
				}
				if !ok {
					c, ok = sweepSparkChart(f.Name, kindTag, op, req.From, req.To)
				}
				if ok {
					add(pivotBlock{Hop: sweepHopOps, Type: blocks.TypeChart, Payload: c})
				}
				break
			}
		case sweepKindFunction:
			if fns == nil || fns.fnSeries == nil {
				continue
			}
			if s, ok := fns.fnSeries[f.Name]; ok {
				if c, ok := sweepRollupChart(f.Name, s, win); ok {
					add(pivotBlock{Hop: sweepHopFunctions, Type: blocks.TypeChart, Payload: c})
				}
			}
		case sweepKindPod:
			if podDone || len(res.Data.Pods) == 0 {
				continue
			}
			podDone = true
			p := EvTablePayload{Title: req.Service + " · pod'lar", Columns: []EvTableColumn{
				{Key: "pod", Label: "Pod", Kind: "text"}, {Key: "cluster", Label: "Cluster", Kind: "text"},
				{Key: "cpu", Label: "CPU % limit", Kind: "num"}, {Key: "mem", Label: "Bellek % limit", Kind: "num"},
				{Key: "restarts", Label: "Restart (pencere)", Kind: "num"}, {Key: "lastTerm", Label: "Son sonlanma", Kind: "text"},
				{Key: "level", Label: "Seviye", Kind: "level"}}, Rows: [][]any{}}
			for _, r := range res.Data.Pods {
				p.Rows = append(p.Rows, []any{r.Pod, r.Cluster, ptrVal(r.CPUPct), ptrVal(r.MemPct), ptrVal(r.RestartsWindow), r.LastTermReason, r.Level})
			}
			add(pivotBlock{Hop: sweepHopPods, Type: blocks.TypeTable, Payload: p})
		case sweepKindJVM:
			if jvmDone || len(res.Data.JVM) == 0 {
				continue
			}
			jvmDone = true
			p := EvTablePayload{Title: req.Service + " · JVM (son 10 dk)", Columns: []EvTableColumn{
				{Key: "pod", Label: "Pod", Kind: "text"}, {Key: "heapPct", Label: "Heap %", Kind: "num"},
				{Key: "postGcPct", Label: "GC sonrası %", Kind: "num"}, {Key: "gcMs", Label: "GC ms", Kind: "num"},
				{Key: "level", Label: "Seviye", Kind: "level"}}, Rows: [][]any{}}
			for _, r := range res.Data.JVM {
				p.Rows = append(p.Rows, []any{r.Pod, ptrVal(r.HeapPct), ptrVal(r.PostGCPct), ptrVal(r.GCPauseMs), r.Level})
			}
			add(pivotBlock{Hop: sweepHopJVM, Type: blocks.TypeTable, Payload: p})
		}
	}
	lt := EvTablePayload{Title: "sorunluların logları", Columns: []EvTableColumn{
		{Key: "finding", Label: "Bulgu", Kind: "text"}, {Key: "time", Label: "Zaman", Kind: "time"},
		{Key: "severity", Label: "Seviye", Kind: "severity"}, {Key: "body", Label: "Mesaj", Kind: "text"},
		{Key: "traceId", Label: "Trace", Kind: "trace"}}, Rows: [][]any{}}
	for i, f := range res.Findings {
		for _, l := range logRows[i] {
			lt.Rows = append(lt.Rows, []any{f.Name, l.TsMs, l.Severity, truncateRunes(l.Body, evBlockBodyRunes-1), l.TraceID})
		}
	}
	if len(lt.Rows) > evBlockMaxLogs {
		lt.Rows, lt.Truncated = lt.Rows[:evBlockMaxLogs], true
	}
	if len(lt.Rows) > 0 {
		if len(out) >= sweepMaxBlocks {
			out[len(out)-1] = pivotBlock{Hop: sweepHopLogs, Type: blocks.TypeTable, Payload: lt} // loglar operatörün asıl istediği
		} else {
			add(pivotBlock{Hop: sweepHopLogs, Type: blocks.TypeTable, Payload: lt})
		}
	}
	return out
}

// sweepRollupChart — fonksiyon kodunun rollup serisi (p99 kestirimi + hata).
func sweepRollupChart(code string, s chstore.RollupSeries, win EvBlockWindow) (EvChartPayload, bool) {
	c := EvChartPayload{Title: "FUNCTION_CODE " + code + " · p99 / hata", Window: win, Series: []EvChartSeries{}}
	p99 := EvChartSeries{Name: "p99", Unit: "ms", Points: [][2]float64{}}
	er := EvChartSeries{Name: "errors", Unit: "count", Points: [][2]float64{}}
	for _, p := range s.Points {
		p99.Points = append(p99.Points, [2]float64{float64(p.TS * 1000), round4(p.P99Ms)})
		er.Points = append(er.Points, [2]float64{float64(p.TS * 1000), float64(p.Errors)})
	}
	if len(p99.Points) == 0 {
		return c, false
	}
	c.Series = append(c.Series, sweepTrimSeries(p99), sweepTrimSeries(er))
	return c, true
}

func sweepTrimSeries(s EvChartSeries) EvChartSeries {
	if len(s.Points) > evBlockMaxPoints {
		s.Points = s.Points[len(s.Points)-evBlockMaxPoints:]
	}
	return s
}

// ── araç ────────────────────────────────────────────────────────────────

// parseSweepRequest — hoşgörülü argüman çözümü (cosreArgs). SAF (pencere hariç).
func parseSweepRequest(raw json.RawMessage) (SweepRequest, cosreArgs, []string) {
	a, warnings := parseCosreArgs(raw)
	req := SweepRequest{
		Service: a.str("service", "service_name", "servis"),
		Env:     a.str("env", "environment", "ortam"),
	}
	switch strings.ToLower(a.str("deep", "mode", "kip")) {
	case "true", "1", "yes", "evet", "deep", "derin", "detaylı", "detayli", "full":
		req.Deep = true
	}
	if n, ok, w := a.num("top_n", "topn", "limit"); ok {
		req.TopN = int(n)
	} else if w != "" {
		warnings = append(warnings, w)
	}
	return req, a, warnings
}

func serviceHealthSweepTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             "service_health_sweep", // = ServiceHealthSweepName; literal: cost_claim_test tarayıcısı adı literalden okur
		ShortDescription: "Servisin TÜM pod/operasyon/fonksiyonlarını tarar, sorunluları sıralar, onların loglarına bakar. Tam sağlık taraması.",
		MinRole:          "",
		Description: "Full health SWEEP of ONE service in a single deterministic call: service RED vs the prior equal window, open problems, " +
			"EVERY operation (operation summary pre-aggregate, current + prior; no env), every FUNCTION_CODE (wide rollup when available, " +
			"else a bounded raw-span breakdown LIMIT 8, max_execution_time 5; no env), the service's pods across its mapped Thanos clusters " +
			"(CPU/memory % of limit, phase, last termination, restart INCREASE in the window) and its pods' JVM heap/GC (last 10 min). " +
			"Rows are judged with the SAME deterministic thresholds as the service verdict (errors +1/+5 pts or ≥5%; latency on window-merged p99 " +
			"only when both windows have ≥300 spans and current p99 ≥100 ms: warn +25% AND ≥+50 ms AND outside the prior window's bucket " +
			"dispersion, crit 2× and ≥200 ms; an operation latency candidate becomes a finding only when raw spans corroborate it — otherwise " +
			"it is reported as not corroborated / 'olası'; volume floor >100 spans; pods 85/90% of limit, OOMKilled or not Running = crit, ≥1 restart warn, ≥3 crit; JVM per runtime alerts). " +
			"The top findings (3, or 5 with deep=true; top_n ≤8) are ranked by level then impact, and THEIR logs are read: pod/JVM → that pod's " +
			"ERROR+ logs; operation/function → the newest error trace's logs. deep=true also samples code.function/code.namespace on the error/slow " +
			"spans of the top problem operations (≤5000 spans, labelled sampled) and new/spiking log patterns. Two extra checks always run: deploys " +
			"(the list_deployments reader — span version transitions, deploy events, Kubernetes rollouts — 2 h before the window end, ≤10 rows; " +
			"a change within −60/+5 min of it is a warning, a stalled/rolled-back rollout critical; deploys never change problem priority) and k8s " +
			"(the same pod read plus kube-state-metrics waiting reasons: CrashLoopBackOff/ImagePullBackOff/ErrImagePull/CreateContainerConfigError " +
			"critical, OOMKilled only with a restart in the window, Pending/Failed warn; Kubernetes Events are not ingested). Budget 15 s; every section reports " +
			"state ok|empty|partial|error|timeout|skipped with a note ('veri yok' = looked, nothing; error/degraded = could not read). " +
			"verdict{label Sağlıklı|Dikkat|Sorunlu|Veri yok, reasons, next}; evidence per section. Read-only. Use for 'servisi tara / genel sağlığı / " +
			"pod, operasyon, fonksiyon hepsine bak'; for a quick RED number use get_service_health, for one trace use pivot_from_trace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Exact service name. Required."},
				"env":     map[string]any{"type": "string", "description": "Deployment environment; applies to the RED summary (service membership). Open items mirror the Service page strip (all envs); operations, functions, pods and JVM carry no env dimension (each section says so)."},
				"window":  map[string]any{"type": "string", "enum": sweepWindowEnum, "description": "Sweep window, compared with the equal prior window. Default 1h."},
				"deep":    map[string]any{"type": "boolean", "description": "Deeper sweep: top 5 findings, sampled code.* functions, log patterns. Default false."},
				"top_n":   map[string]any{"type": "integer", "minimum": 1, "maximum": sweepTopNMax, "description": "Findings to rank and read logs for. Default 3 (5 when deep)."},
			},
			"required": []string{"service"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			req, a, warnings := parseSweepRequest(raw)
			from, to, label, ww := cosreWindow(ctx, a, cosreWindowSeconds["6h"], "1h")
			warnings = append(warnings, ww...)
			req.From, req.To, req.Window = from, to, label
			r := sweepReadersFrom(d)
			if d.Memo == nil {
				return runServiceSweep(ctx, r, req, warnings)
			}
			var clusters []ClusterRef
			if d.Clusters != nil {
				clusters = d.Clusters()
			}
			alerts := chstore.DefaultRuntimeAlerts()
			if r.runtimeAlerts != nil {
				alerts = r.runtimeAlerts(ctx)
			}
			body, err := sweepMemo(ctx, d, r, req, clusters, alerts, warnings)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		},
	}
}

// sweepMemo — aracın önbellekli yürütmesi; v0.10.1287'den beri AI SRE
// orkestratörü de AYNI anahtarla çağırır (ReadServiceSweepCached) → drain'in
// taraması, aynı istekle gelen sohbet çağrısına önbellek isabeti olur.
func sweepMemo(ctx context.Context, d Deps, r sweepReaders, req SweepRequest, clusters []ClusterRef, alerts chstore.RuntimeAlertConfig, warnings []string) ([]byte, error) {
	key := sweepCacheKey(req, clusters, alerts) + ":w=" + fnvHex(warnings...)
	return d.Memo(ctx, key, sweepCacheTTL, func(c context.Context) (any, bool, error) {
		res, err := runServiceSweep(c, r, req, warnings)
		if err != nil {
			return nil, false, err
		}
		// Kısmi / hatalı sonuç saklanmaz (v0.10.1119 kuralı).
		return res, res.OK && !res.Partial && res.Error == nil, nil
	})
}

// ReadServiceSweepCached — v0.10.1287 (AI SRE Faz 1b): ReadServiceSweep'in
// Deps.Memo'lu eşi (Memo yoksa doğrudan). Sohbet aracıyla aynı anahtar (uyarı
// yok) — drain'in sonucu sohbetin önbelleğini tohumlar.
func ReadServiceSweepCached(ctx context.Context, d Deps, req SweepRequest) (SweepResult, error) {
	r := sweepReadersFrom(d)
	if d.Memo == nil {
		return runServiceSweep(ctx, r, req, nil)
	}
	var clusters []ClusterRef
	if d.Clusters != nil {
		clusters = d.Clusters()
	}
	alerts := chstore.DefaultRuntimeAlerts()
	if r.runtimeAlerts != nil {
		alerts = r.runtimeAlerts(ctx)
	}
	body, err := sweepMemo(ctx, d, r, req, clusters, alerts, nil)
	if err != nil {
		return SweepResult{}, err
	}
	var out SweepResult
	if err := json.Unmarshal(body, &out); err != nil {
		return SweepResult{}, fmt.Errorf("sweep cache decode: %w", err)
	}
	return out, nil
}
