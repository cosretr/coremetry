package mcptools

// pivot_trace.go — v0.10.1275 (CoSRE adım 6/8): sinyaller arası pivot
// (trace → pod → JVM → log) + playbook.
//
// TEK deterministik bileşik araç, model zinciri DEĞİL: küçük model (2B) 32-hex
// trace id'yi, pod adlarını ve cluster değerlerini dört çağrı boyunca
// taşıyamıyor. Sunucu tek çağrıda zinciri yürütür; atomik araçlar takip
// soruları için yerinde kalır.
//
// HALKALAR (tümü pivotBudget = 15 s içinde; executor bütçesi 20 s):
//
//	H0 trace       Store.GetTrace (+ trace_summary_5m stub yedeği) → buildTraceToolAnalysis   6 s
//	H1 pods        Deps.TracePods → thanos.TracePodMetrics (cluster eşlemesi api'de)          8 s
//	H2 jvm         MetricRouter.RuntimePods (OTel JVM; JMX Thanos'a SORULMAZ)                 6 s
//	H3 logs        logstore.LogsForTrace, trace penceresi ±300 s; 30 oku, şiddete göre ≤12     6 s
//	H4 op_baseline GetOperationSummary (operation_summary_5m, env YOK), trace'ten önceki 1 sa  3 s
//
// Halka süresi = min(tavan, kalan − 500 ms); ≤ 0 → skipped ("süre bütçesi
// doldu"). Zincir ASLA kopmaz: boş halka kendi "veri yok: …" notunu taşır,
// okunamayan halka error/timeout/degraded olur (okunamadı ≠ yok). H0'dan sonra
// H1–H4 paralel koşar; ilerleme etiketleri (progress.go) YALNIZ bu dosyanın
// koordinatör goroutine'inden yayınlanır.
//
// PLAYBOOK: belirti = trace'te hata span'i varsa `error`, yoksa `latency`;
// aileler anomaly.SymptomPlan'dan (problem soruşturmasıyla AYNI saf dallanma).
// Aile → kanıt: saturation ← H1+H2, logs ← H3, operations ← H4, exceptions ←
// analiz hata span'leri (okuma yok), business ← kök span FUNCTION_CODE /
// CHANNEL_CODE (okuma yok). Çıktı `playbook{symptom, families, checked[]}`
// (renderDeepEvidence ile aynı CheckedSignal şekli). Hiçbir şey KALICI
// YAZILMAZ.
//
// ZARF (alan sırası = JSON sırası; sohbet sonucu 6000 rune'da baştan keser):
// tool, ok, partial, empty?, message?, error?, warnings, request, trace,
// hops, findings ≤5, playbook, evidence (= evidences[0]), evidences, data
// (pods, jvm, logs, candidates, blocks) — veri EN SONDA.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
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

// fnvHex — sıralı parçaların FNV-64a özeti (parçalar ayırıcıyla; sınır kaybolmaz).
func fnvHex(parts ...string) string {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("%016x", h.Sum64())
}

// PivotFromTraceName — aracın adı (kanıt eşlemesi + sohbet evresi buna bakar).
const PivotFromTraceName = "pivot_from_trace"

// Bütçe ve halka tavanları.
const (
	pivotBudget    = 15 * time.Second
	pivotHopMargin = 500 * time.Millisecond
	pivotCapTrace  = 6 * time.Second
	pivotCapPods   = 8 * time.Second
	pivotCapJVM    = 6 * time.Second
	pivotCapLogs   = 6 * time.Second
	pivotCapOp     = 3 * time.Second
)

// Kapsam tavanları.
const (
	pivotMaxServices       = 3
	pivotMaxPods           = 8
	pivotMaxPodsPerService = 4
	pivotMaxClusters       = 2
	pivotCandidates        = 5
	pivotLogFetch          = 30
	pivotLogKeep           = 12
	pivotFindingsMax       = 5
	pivotCritPathMax       = 3
	pivotPodPad            = 5 * time.Minute
	pivotPodMaxWindow      = 6 * time.Hour
	pivotJVMPadBefore      = 10 * time.Minute
	pivotJVMPadAfter       = 5 * time.Minute
	pivotLogPadS           = 300
	pivotOpLookback        = time.Hour
	pivotCacheTTL          = 60 * time.Second
	pivotReasonRunes       = 160
	pivotChartMaxPoints    = evBlockMaxPoints
)

// op_baseline eşikleri — servis hükmüyle AYNI kaynak (v0.10.1276:
// internal/healthverdict; önceden api hvP99* aynasıydı). Trace'in op süresi / op p99.
const (
	pivotOpWarnMult = 1 + healthverdict.LatWarnRel
	pivotOpCritMult = healthverdict.LatCritMult
	pivotOpCritMin  = healthverdict.LatCritMinMs
)

// Pod doygunluk eşikleri (% limit) — evaluator/db_capacity.go 85/90 emsali.
const (
	pivotPodWarnPct = 85.0
	pivotPodCritPct = 90.0
)

// Halka adları.
const (
	pivotHopTrace = "trace"
	pivotHopPods  = "pods"
	pivotHopJVM   = "jvm"
	pivotHopLogs  = "logs"
	pivotHopOp    = "op_baseline"
)

// Halka durumları.
const (
	hopOK       = "ok"
	hopEmpty    = "empty"
	hopPartial  = "partial"
	hopUnmapped = "unmapped"
	hopDegraded = "degraded"
	hopError    = "error"
	hopTimeout  = "timeout"
	hopSkipped  = "skipped"
)

// Bulgu seviyeleri (FE `level` sütunu).
const (
	levelCrit = "crit"
	levelWarn = "warn"
	levelOK   = "ok"
)

var pivotWindowEnum = []string{"15m", "1h", "6h"}

var pivotPickEnum = []string{"slowest", "slowest_error"}

var pivotPickSynonyms = map[string]string{
	"slow": "slowest", "duration": "slowest", "yavaş": "slowest", "en_yavaş": "slowest",
	"error": "slowest_error", "errors": "slowest_error", "hata": "slowest_error", "hatalı": "slowest_error",
}

// ── dış dikişler ────────────────────────────────────────────────────────

// TracePodHop — H1'in bir cluster değeri için cevabı (api/mcp_trace_pods.go
// doldurur; eşleme kuralı GET /api/trace-pods/metrics ile AYNI).
type TracePodHop struct {
	Thanos         bool                    // en az bir etkin cluster var mı
	ClusterValue   string                  // span cluster değeri
	Mapped         bool                    // değer bir Remote Cluster kaydına bağlandı
	UnmappedReason string                  // no_cluster_value | no_remote_cluster
	ClusterID      string                  // Remote Cluster kimliği (Pod sayfası ?cluster=)
	ClusterName    string                  // görünen ad
	Query          string                  // çalışan PromQL (R1 + R2)
	Start          int64                   // unix s, kova 0
	Step           int                     // s
	Points         int                     // kova sayısı
	Instant        string                  // ok | partial | failed
	Pods           []thanos.TracePodSeries // istek sırası
}

// TracePodReader — H1 okuyucusu; nil = Thanos yok (halka skipped).
type TracePodReader interface {
	TracePods(ctx context.Context, clusterValue string, refs []thanos.TracePodRef, from, to time.Time) (TracePodHop, error)
}

// PivotRequest — normalize edilmiş argümanlar. From/To sıfırsa pencere ctx
// çıpasından kurulur (runTracePivot).
type PivotRequest struct {
	TraceID   string
	Service   string
	Env       string
	Operation string
	Window    string // 15m | 1h | 6h
	Pick      string // slowest | slowest_error
	From, To  time.Time
}

// ── çıktı ───────────────────────────────────────────────────────────────

type pivotTrace struct {
	TraceID      string              `json:"traceId"`
	Root         string              `json:"root"`
	Service      string              `json:"service"`
	WallMs       float64             `json:"wallMs"`
	SpanCount    int                 `json:"spanCount"`
	ErrorSpans   int                 `json:"errorSpans"`
	StartMs      int64               `json:"startMs,omitempty"`
	CriticalPath []traceToolPathStep `json:"criticalPath"`
	StubReason   string              `json:"stubReason,omitempty"`
}

type pivotHop struct {
	Hop         string `json:"hop"`
	State       string `json:"state"`
	Summary     string `json:"summary"`
	Note        string `json:"note,omitempty"`
	EvidenceRef string `json:"evidenceRef,omitempty"`
	DurationMs  int64  `json:"durationMs"`
}

type pivotFinding struct {
	Hop     string `json:"hop"`
	Subject string `json:"subject"`
	Level   string `json:"level"`
	Reason  string `json:"reason"`
}

type pivotPlaybook struct {
	Symptom  string                  `json:"symptom"`
	Families []string                `json:"families"`
	Checked  []chstore.CheckedSignal `json:"checked"`
}

type pivotPodRow struct {
	Cluster        string   `json:"cluster,omitempty"`
	Namespace      string   `json:"namespace,omitempty"`
	Pod            string   `json:"pod"`
	Service        string   `json:"service,omitempty"`
	State          string   `json:"state"`
	CPUMaxCores    *float64 `json:"cpuMaxCores,omitempty"`
	CPULimitPct    *float64 `json:"cpuLimitPct,omitempty"`
	MemMaxBytes    *float64 `json:"memMaxBytes,omitempty"`
	MemLimitPct    *float64 `json:"memLimitPct,omitempty"`
	Restarts       *int     `json:"restarts,omitempty"`
	LastTermReason string   `json:"lastTermReason,omitempty"`
	Phase          string   `json:"phase,omitempty"`
	Level          string   `json:"level"`
}

type pivotJVMRow struct {
	Service   string   `json:"service"`
	Pod       string   `json:"pod"`
	HeapPct   *float64 `json:"heapPct,omitempty"`
	PostGCPct *float64 `json:"postGcPct,omitempty"`
	GCPauseMs *float64 `json:"gcPauseMs,omitempty"`
	Level     string   `json:"level"`
	Reason    string   `json:"reason,omitempty"`
}

type pivotBlock struct {
	Hop     string `json:"hop"`
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

type pivotData struct {
	Pods              []pivotPodRow `json:"pods"`
	JVM               []pivotJVMRow `json:"jvm"`
	Logs              []qlRow       `json:"logs"`
	Candidates        []qtTrace     `json:"candidates,omitempty"`
	CandidatesHasMore bool          `json:"candidatesHasMore,omitempty"`
	Blocks            []pivotBlock  `json:"blocks"`
}

// PivotResult — zarf; alan sırası = JSON sırası (veri EN SONDA).
type PivotResult struct {
	Tool            string             `json:"tool"`
	OK              bool               `json:"ok"`
	Partial         bool               `json:"partial"`
	Empty           bool               `json:"empty,omitempty"`
	Message         string             `json:"message,omitempty"`
	Error           *cosreError        `json:"error,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
	Request         map[string]any     `json:"request"`
	Trace           *pivotTrace        `json:"trace,omitempty"`
	Hops            []pivotHop         `json:"hops"`
	Findings        []pivotFinding     `json:"findings"`
	FindingsHasMore bool               `json:"findingsHasMore,omitempty"`
	Playbook        *pivotPlaybook     `json:"playbook,omitempty"`
	Evidence        *cosreEvidence     `json:"evidence,omitempty"`
	Evidences       []cosreHopEvidence `json:"evidences,omitempty"`
	Data            *pivotData         `json:"data,omitempty"`
}

// ── okuyucular (test dikişi) ────────────────────────────────────────────

// pivotTraceFetch — H0 okuması: span'ler ya da (yoksa) MV özeti.
type pivotTraceFetch struct {
	Spans      []chstore.SpanRow
	Stub       *chstore.TraceAggregateStub
	StubReason string // aged_out | replica_miss | not_found | stub_unreadable
	Capped     bool
}

type pivotReaders struct {
	fetchTrace    func(ctx context.Context, id string) (pivotTraceFetch, error)
	searchTraces  func(ctx context.Context, f chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error)
	tracePods     TracePodReader
	runtime       chstore.RuntimePodReader
	runtimeSource string
	runtimeAlerts func(ctx context.Context) chstore.RuntimeAlertConfig
	logs          logstore.Store
	opSummary     func(ctx context.Context, service string, from, to time.Time) ([]chstore.OperationSummary, error)
	traceWindow   func(ctx context.Context, id string) (lo, hi time.Time, ok bool)
}

func pivotReadersFrom(d Deps) pivotReaders {
	r := pivotReaders{tracePods: d.TracePods, logs: d.LogStore, traceWindow: d.TraceWindow,
		runtimeAlerts: func(context.Context) chstore.RuntimeAlertConfig { return chstore.DefaultRuntimeAlerts() }}
	r.runtime, r.runtimeSource = d.RuntimePods, evSourceCH
	if d.MetricRouter != nil {
		r.runtime = d.MetricRouter.RuntimePods()
		if d.MetricRouter.Resolve(metricsrc.FamilyOTelRuntime, metricsrc.Scope{}).Backend == metricsrc.BackendVM {
			r.runtimeSource = evSourceVM
		}
	}
	if st := d.Store; st != nil {
		r.fetchTrace = func(ctx context.Context, id string) (pivotTraceFetch, error) {
			return pivotFetchTraceStore(ctx, st, id)
		}
		r.searchTraces = st.GetTraces
		if r.runtime == nil {
			r.runtime = st
		}
		r.runtimeAlerts = st.GetRuntimeAlerts
		r.opSummary = func(ctx context.Context, service string, from, to time.Time) ([]chstore.OperationSummary, error) {
			return st.GetOperationSummary(ctx, service, to.Sub(from), from, to, false, "")
		}
		if r.traceWindow == nil {
			r.traceWindow = st.TraceWindow
		}
	}
	return r
}

// pivotFetchTraceStore — get_trace'in okuma yolu (trace_tools.go): Distributed
// okuma → 0 span ise trace_summary_5m özeti → TTL içindeyse tüm-replika yedeği.
func pivotFetchTraceStore(ctx context.Context, st *chstore.Store, id string) (pivotTraceFetch, error) {
	spans, err := st.GetTrace(ctx, id)
	if err != nil {
		return pivotTraceFetch{}, err
	}
	if len(spans) > 0 {
		return pivotTraceFetch{Spans: spans, Capped: len(spans) >= traceStoreSpanCap}, nil
	}
	stub, ok, serr := st.GetTraceAggregateStubErr(ctx, id)
	if serr != nil {
		return pivotTraceFetch{StubReason: "stub_unreadable"}, nil
	}
	if !ok {
		return pivotTraceFetch{StubReason: "not_found"}, nil
	}
	ret := ""
	if rs, rerr := st.GetRetention(ctx); rerr == nil {
		ret = rs.Spans
	}
	if chstore.TraceAgedOut(stub.StartTimeNs, wallNow(), ret, st.DefaultSpansDays()) {
		return pivotTraceFetch{Stub: &stub, StubReason: "aged_out"}, nil
	}
	rows, rerr := st.GetTraceAllReplicas(ctx, id, stub.StartTimeNs, stub.EndTimeNs)
	if rerr == nil && len(rows) > 0 {
		return pivotTraceFetch{Spans: rows}, nil
	}
	return pivotTraceFetch{Stub: &stub, StubReason: "replica_miss"}, nil
}

// ── saf yardımcılar ─────────────────────────────────────────────────────

// parsePivotRequest — hoşgörülü argüman çözümü (cosreArgs). SAF (pencere hariç).
func parsePivotRequest(raw json.RawMessage) (PivotRequest, cosreArgs, []string) {
	a, warnings := parseCosreArgs(raw)
	req := PivotRequest{
		Service:   a.str("service", "service_name", "servis"),
		Env:       a.str("env", "environment", "ortam"),
		Operation: truncateRunes(a.str("operation", "op", "endpoint"), qtMaxNameRunes),
	}
	if raw := a.str("trace_id", "traceid", "trace"); raw != "" {
		if id, err := normalizeTraceID(raw); err == nil {
			req.TraceID = id
		} else {
			warnings = append(warnings, fmt.Sprintf("trace_id %q 32 hex değil — yok sayıldı", truncateRunes(raw, 40)))
		}
	}
	pick, w := coerceEnum("pick", a.str("pick", "sort_by", "sort"), pivotPickEnum, pivotPickSynonyms, "slowest")
	if w != "" {
		warnings = append(warnings, w)
	}
	req.Pick = pick
	return req, a, warnings
}

// pickPivotTrace — SAF: hangi trace pivotlanır. trace_id → o; yoksa servis
// verilmişse aday listesinin ilki (boş liste → "", pick — veri yok); ikisi de
// yoksa bad_args (need trace_id|service).
func pickPivotTrace(req PivotRequest, rows []chstore.TraceRow) (traceID, pickedBy string, cerr *cosreError) {
	if req.TraceID != "" {
		return req.TraceID, "trace_id", nil
	}
	if strings.TrimSpace(req.Service) == "" {
		return "", "", &cosreError{Code: cosreErrBadArgs,
			Message: "trace_id ya da service gerekli (trace sayfasındaysan trace_id; servis için en yavaş trace seçilir)",
			Need:    []string{"trace_id", "service"}}
	}
	if len(rows) == 0 {
		return "", req.Pick, nil
	}
	return strings.ToLower(rows[0].TraceID), req.Pick, nil
}

// hopDeadline — SAF: halka süresi = min(tavan, kalan − pay); ≤ 0 → 0 (skipped).
func hopDeadline(remaining, hopCap time.Duration) time.Duration {
	r := remaining - pivotHopMargin
	if r <= 0 {
		return 0
	}
	if hopCap < r {
		return hopCap
	}
	return r
}

// pivotPodRef — H1/H2'nin pod kümesinin bir elemanı.
type pivotPodRef struct {
	Service   string
	Namespace string
	Pod       string
}

// pivotPodRefs — SAF: trace'in pod kümesi, span cluster değerine göre gruplu.
// Servisler kritik yol sırasıyla (≤3; yol kısaysa öz süre sırasıyla tamamlanır),
// servis başına en çok span taşıyan ≤4 pod, toplam ≤8. Cluster değeri olmayan
// pod'lar "" anahtarında (unmapped: no_cluster_value). İkinci dönüş: kümeye
// girmeyen cluster değerleri (≤2 tavanı).
func pivotPodRefs(spans []chstore.SpanRow, an traceToolAnalysis) (map[string][]pivotPodRef, []string) {
	var services []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] && len(services) < pivotMaxServices {
			seen[s] = true
			services = append(services, s)
		}
	}
	for _, st := range an.CriticalPath {
		add(st.Service)
	}
	for _, sv := range an.Services {
		add(sv.Service)
	}
	type key struct{ cluster, ns, pod string }
	counts := map[string]map[key]int{}
	for i := range spans {
		s := &spans[i]
		if !seen[s.ServiceName] {
			continue
		}
		_, cl, ns, pod, _ := ttSpanContext(s)
		if pod == "" {
			continue
		}
		m := counts[s.ServiceName]
		if m == nil {
			m = map[key]int{}
			counts[s.ServiceName] = m
		}
		m[key{cl, ns, pod}]++
	}
	out := map[string][]pivotPodRef{}
	var clusterOrder, dropped []string
	total := 0
	for _, svc := range services {
		ks := make([]key, 0, len(counts[svc]))
		for k := range counts[svc] {
			ks = append(ks, k)
		}
		sort.Slice(ks, func(i, j int) bool {
			ci, cj := counts[svc][ks[i]], counts[svc][ks[j]]
			if ci != cj {
				return ci > cj
			}
			if ks[i].pod != ks[j].pod {
				return ks[i].pod < ks[j].pod
			}
			return ks[i].cluster < ks[j].cluster
		})
		n := 0
		for _, k := range ks {
			if n >= pivotMaxPodsPerService || total >= pivotMaxPods {
				break
			}
			if _, known := out[k.cluster]; !known && k.cluster != "" {
				nonEmpty := 0
				for _, c := range clusterOrder {
					if c != "" {
						nonEmpty++
					}
				}
				if nonEmpty >= pivotMaxClusters {
					dropped = appendUnique(dropped, k.cluster)
					continue
				}
			}
			if _, known := out[k.cluster]; !known {
				clusterOrder = append(clusterOrder, k.cluster)
			}
			out[k.cluster] = append(out[k.cluster], pivotPodRef{Service: svc, Namespace: k.ns, Pod: k.pod})
			n++
			total++
		}
	}
	return out, dropped
}

func appendUnique(xs []string, v string) []string {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// classifyJVM — SAF: bir pod'un heap + GC örneği → satır + seviye. Heap
// post-GC varsa post-GC eşikleri (varsayılan 85/90), yoksa ham doluluk
// eşikleri (92/97); GC duraklama ortalaması 2000/3000 ms (GetRuntimeAlerts).
func classifyJVM(service, pod string, heap, gc *chstore.CapacitySample, cfg chstore.RuntimeAlertConfig) pivotJVMRow {
	row := pivotJVMRow{Service: service, Pod: pod, Level: levelOK}
	var reasons []string
	bump := func(l string) {
		if l == levelCrit || (l == levelWarn && row.Level == levelOK) {
			row.Level = l
		}
	}
	if heap != nil && heap.Limit > 0 {
		raw := round4(100 * heap.Usage / heap.Limit)
		row.HeapPct = &raw
		if heap.PostGC > 0 {
			pg := round4(100 * heap.PostGC / heap.Limit)
			row.PostGCPct = &pg
			switch {
			case pg >= cfg.HeapPostGCCritPct:
				bump(levelCrit)
				reasons = append(reasons, fmt.Sprintf("GC sonrası heap %%%.0f ≥ %%%.0f", pg, cfg.HeapPostGCCritPct))
			case pg >= cfg.HeapPostGCWarnPct:
				bump(levelWarn)
				reasons = append(reasons, fmt.Sprintf("GC sonrası heap %%%.0f ≥ %%%.0f", pg, cfg.HeapPostGCWarnPct))
			}
		} else {
			switch {
			case raw >= cfg.HeapRawCritPct:
				bump(levelCrit)
				reasons = append(reasons, fmt.Sprintf("heap %%%.0f ≥ %%%.0f (GC sonrası metrik yok)", raw, cfg.HeapRawCritPct))
			case raw >= cfg.HeapRawWarnPct:
				bump(levelWarn)
				reasons = append(reasons, fmt.Sprintf("heap %%%.0f ≥ %%%.0f (GC sonrası metrik yok)", raw, cfg.HeapRawWarnPct))
			}
		}
	}
	if gc != nil {
		ms := round4(gc.Usage)
		row.GCPauseMs = &ms
		switch {
		case ms >= cfg.GCPauseCritMs:
			bump(levelCrit)
			reasons = append(reasons, fmt.Sprintf("GC duraklaması ort. %.0f ms ≥ %.0f", ms, cfg.GCPauseCritMs))
		case ms >= cfg.GCPauseWarnMs:
			bump(levelWarn)
			reasons = append(reasons, fmt.Sprintf("GC duraklaması ort. %.0f ms ≥ %.0f", ms, cfg.GCPauseWarnMs))
		}
	}
	row.Reason = strings.Join(reasons, "; ")
	return row
}

// classifyPod — SAF: Thanos pod satırı → satır + seviye + gerekçe.
func classifyPod(ref pivotPodRef, cluster string, s *thanos.TracePodSeries) (pivotPodRow, string) {
	row := pivotPodRow{Cluster: cluster, Namespace: ref.Namespace, Pod: ref.Pod, Service: ref.Service, State: "no_samples", Level: levelOK}
	if s == nil {
		return row, ""
	}
	row.State, row.Restarts, row.LastTermReason, row.Phase = s.State, s.Restarts, s.LastTermReason, s.Phase
	if s.Namespace != "" {
		row.Namespace = s.Namespace
	}
	if mx, ok := seriesMax(s.CPU); ok {
		v := round4(mx)
		row.CPUMaxCores = &v
		if s.CPULimit != nil && *s.CPULimit > 0 {
			p := round4(100 * mx / *s.CPULimit)
			row.CPULimitPct = &p
		}
	}
	if mx, ok := seriesMax(s.Mem); ok {
		v := math.Round(mx)
		row.MemMaxBytes = &v
		if s.MemLimit != nil && *s.MemLimit > 0 {
			p := round4(100 * mx / *s.MemLimit)
			row.MemLimitPct = &p
		}
	}
	var reasons []string
	bump := func(l, why string) {
		if l == levelCrit || (l == levelWarn && row.Level == levelOK) {
			row.Level = l
		}
		reasons = append(reasons, why)
	}
	if row.Phase != "" && row.Phase != "Running" && row.Phase != "Succeeded" {
		bump(levelCrit, "faz "+row.Phase)
	}
	if strings.EqualFold(row.LastTermReason, "OOMKilled") {
		bump(levelCrit, "son sonlanma OOMKilled")
	}
	for _, x := range []struct {
		pct  *float64
		what string
	}{{row.MemLimitPct, "bellek"}, {row.CPULimitPct, "CPU"}} {
		if x.pct == nil {
			continue
		}
		switch {
		case *x.pct >= pivotPodCritPct:
			bump(levelCrit, fmt.Sprintf("%s limitin %%%.0f'i", x.what, *x.pct))
		case *x.pct >= pivotPodWarnPct:
			bump(levelWarn, fmt.Sprintf("%s limitin %%%.0f'i", x.what, *x.pct))
		}
	}
	return row, strings.Join(reasons, "; ")
}

func seriesMax(vs []*float64) (float64, bool) {
	mx, ok := 0.0, false
	for _, v := range vs {
		if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
			continue
		}
		if !ok || *v > mx {
			mx, ok = *v, true
		}
	}
	return mx, ok
}

// levelRank — sıralama: crit < warn < ok.
func levelRank(l string) int {
	switch l {
	case levelCrit:
		return 0
	case levelWarn:
		return 1
	}
	return 2
}

var pivotHopOrder = map[string]int{pivotHopTrace: 0, pivotHopPods: 1, pivotHopJVM: 2, pivotHopLogs: 3, pivotHopOp: 4}

// rankFindings — SAF: seviye, sonra halka sırası, sonra özne; tavan
// pivotFindingsMax. İkinci dönüş: kırpıldı mı.
func rankFindings(in []pivotFinding) ([]pivotFinding, bool) {
	out := append([]pivotFinding(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if a, b := levelRank(out[i].Level), levelRank(out[j].Level); a != b {
			return a < b
		}
		if a, b := pivotHopOrder[out[i].Hop], pivotHopOrder[out[j].Hop]; a != b {
			return a < b
		}
		return out[i].Subject < out[j].Subject
	})
	if len(out) > pivotFindingsMax {
		return out[:pivotFindingsMax], true
	}
	if out == nil {
		out = []pivotFinding{}
	}
	return out, false
}

// pivotSymptom — SAF: trace'in belirtisi.
func pivotSymptom(errorSpans int) string {
	if errorSpans > 0 {
		return "error"
	}
	return "latency"
}

// ── koşucu ──────────────────────────────────────────────────────────────

// hopOutcome — bir halkanın tüm çıktısı (işçi goroutine'i yalnız bunu doldurur
// ve kanala yollar; koordinatör sonuca yazar — paylaşılan durum yok).
type hopOutcome struct {
	hop       pivotHop
	evidences []cosreHopEvidence
	findings  []pivotFinding
	blocks    []pivotBlock
	pods      []pivotPodRow
	jvm       []pivotJVMRow
	logs      []qlRow
	opFound   bool
	failed    bool // okunamadı (partial'a sayılır)
}

// pivotCtx — H1–H4'ün ortak girdisi (H0'dan).
type pivotCtx struct {
	traceID       string
	spans         []chstore.SpanRow
	an            traceToolAnalysis
	haveSpans     bool
	start, end    time.Time // trace penceresi (span'ler ya da özet)
	haveWindow    bool
	podRefs       map[string][]pivotPodRef
	alerts        chstore.RuntimeAlertConfig
	topOpService  string
	topOpName     string
	topOpSpanMs   float64
	topOpHasValue bool
}

func runTracePivot(ctx context.Context, r pivotReaders, req PivotRequest, warnings []string) (PivotResult, error) {
	t0 := wallNow()
	deadline := t0.Add(pivotBudget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	remaining := func() time.Duration { return time.Until(deadline) }
	if req.Window == "" {
		req.Window = "1h"
	}
	if req.Pick == "" {
		req.Pick = "slowest"
	}
	if req.From.IsZero() || req.To.IsZero() {
		req.From, req.To = rangeWindow(ctx, cosreWindowSeconds[req.Window])
		req.From, req.To = req.From.UTC(), req.To.UTC()
	}
	res := PivotResult{Tool: PivotFromTraceName, Warnings: warnings, Hops: []pivotHop{}, Findings: []pivotFinding{}}
	reqEcho := map[string]any{"window": req.Window, "pick": req.Pick}
	for k, v := range map[string]string{"service": req.Service, "env": req.Env, "operation": req.Operation} {
		if v != "" {
			reqEcho[k] = v
		}
	}
	res.Request = reqEcho

	// ── trace seçimi ───────────────────────────────────────────────────
	var candidates []qtTrace
	var candEv *cosreHopEvidence
	candHasMore := false
	var rows []chstore.TraceRow
	if req.TraceID == "" && req.Service != "" {
		if r.searchTraces == nil {
			res.Error = &cosreError{Code: cosreErrNotConfigured, Message: "ClickHouse trace okuyucusu bağlı değil"}
			return res, nil
		}
		reportProgress(ctx, "Trace", "en yavaş trace seçiliyor: "+req.Service)
		var filters []chstore.FilterExpr
		if req.Operation != "" {
			filters = append(filters, chstore.FilterExpr{Key: "name", Op: "=", Values: []string{req.Operation}})
		}
		from, to := req.From, req.To
		gate, gerr := applySearchGate(int(to.Sub(from)/time.Second), pivotCandidates, filters, true, true, chstore.AttrIndexAvailable())
		if gerr != nil {
			res.Error = &cosreError{Code: cosreErrBadArgs, Message: gerr.Error()}
			return res, nil
		}
		if gate.Reason != "" {
			from = to.Add(-time.Duration(gate.RangeS) * time.Second)
			res.Warnings = append(res.Warnings, "pencere daraltıldı: "+gate.Reason)
		}
		f := chstore.TraceFilter{Service: req.Service, Env: req.Env, From: from, To: to, Sort: "duration", Order: "desc",
			Limit: pivotCandidates, CountMode: "skip", HasError: req.Pick == "slowest_error", Filters: filters}
		sctx, cancel := context.WithTimeout(ctx, hopDeadline(remaining(), pivotCapTrace))
		elapsed := stopwatch()
		var err error
		rows, _, candHasMore, err = r.searchTraces(sctx, f)
		cancel()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return PivotResult{}, ctx.Err()
			}
			res.Error = &cosreError{Code: cosreClassify(err), Message: cosreErrMsg("ClickHouse trace araması başarısız", err)}
			return res, nil
		}
		for _, t := range rows {
			if len(candidates) >= pivotCandidates {
				candHasMore = true
				break
			}
			candidates = append(candidates, qtTrace{TraceID: t.TraceID, Root: t.RootName, Service: t.ServiceName, StartMs: t.StartTime / 1e6,
				DurationMs: round4(t.DurationMs), Spans: t.SpanCount, Error: t.HasError, ErrorSpans: t.ErrorSpans})
		}
		ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Query: qtListQueryText(f), Window: evWin(from, to), Env: req.Env,
			Rows: intPtr(len(candidates)), DurationMs: elapsed()}
		status := qtStatusAny
		if req.Pick == "slowest_error" {
			status = qtStatusError
		}
		req := map[string]any{"service": req.Service, "sort_by": qtSortSlowest, "status": status}
		if op := f.Filters; len(op) > 0 {
			req["operation"] = op[0].Values[0]
		}
		candEv = &cosreHopEvidence{Hop: pivotHopTrace, As: queryTracesName, Request: req, cosreEvidence: ev}
	}
	traceID, pickedBy, cerr := pickPivotTrace(req, rows)
	if cerr != nil {
		res.Error = cerr
		return res, nil
	}
	if req.TraceID != "" && req.Env != "" {
		reqEcho["env_applied"] = false
		reqEcho["env_note"] = "trace kimliği zaten kesin; env yalnız aday aramasında uygulanır"
	}
	reqEcho["picked_by"] = pickedBy
	if traceID == "" {
		res.OK, res.Empty = true, true
		res.Message = "veri yok: " + scopeTR([2]string{"servis", req.Service}, [2]string{"operasyon", req.Operation}, [2]string{"env", req.Env}) +
			" için " + windowTR(req.From, req.To) + " penceresinde pivotlanacak trace bulunamadı (ClickHouse)"
		if candEv != nil {
			res.Evidences = []cosreHopEvidence{*candEv}
			res.Evidence = &res.Evidences[0].cosreEvidence
		}
		res.Data = &pivotData{Pods: []pivotPodRow{}, JVM: []pivotJVMRow{}, Logs: []qlRow{}, Blocks: []pivotBlock{}}
		return res, nil
	}
	reqEcho["trace_id"] = traceID

	// ── H0 trace ───────────────────────────────────────────────────────
	reportProgress(ctx, "Trace", "trace okunuyor: "+traceID[:8])
	pc := pivotCtx{traceID: traceID, alerts: chstore.DefaultRuntimeAlerts()}
	h0 := hopOutcome{hop: pivotHop{Hop: pivotHopTrace}}
	h0start := stopwatch()
	var fetch pivotTraceFetch
	if r.fetchTrace == nil {
		h0.hop.State, h0.hop.Summary, h0.failed = hopSkipped, "ClickHouse trace okuyucusu bağlı değil", true
	} else if dl := hopDeadline(remaining(), pivotCapTrace); dl <= 0 {
		h0.hop.State, h0.hop.Summary, h0.failed = hopSkipped, "süre bütçesi doldu", true
	} else {
		tctx, cancel := context.WithTimeout(ctx, dl)
		var err error
		fetch, err = r.fetchTrace(tctx, traceID)
		cancel()
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return PivotResult{}, ctx.Err()
			}
			h0.failed = true
			h0.hop.State = hopError
			if cosreClassify(err) == cosreErrTimeout {
				h0.hop.State = hopTimeout
			}
			h0.hop.Summary = cosreErrMsg("trace okunamadı", err)
		}
	}
	h0.hop.DurationMs = h0start()
	tr := &pivotTrace{TraceID: traceID, CriticalPath: []traceToolPathStep{}, StubReason: fetch.StubReason}
	switch {
	case len(fetch.Spans) > 0:
		pc.spans, pc.haveSpans = fetch.Spans, true
		pc.an = buildTraceToolAnalysis(fetch.Spans, fetch.Capped)
		pc.start, pc.end, pc.haveWindow = time.Unix(0, pc.an.StartUnixNs).UTC(), time.Unix(0, pc.an.EndUnixNs).UTC(), true
		tr.Root, tr.Service, tr.WallMs = pc.an.Root.Name, pc.an.Root.Service, pc.an.WallMs
		tr.SpanCount, tr.ErrorSpans, tr.StartMs = pc.an.SpanCount, pc.an.ErrorSpanCount, pc.an.StartUnixNs/1e6
		tr.CriticalPath = pivotTopPath(pc.an.CriticalPath, pivotCritPathMax)
		h0.hop.State = hopOK
		h0.hop.Summary = fmt.Sprintf("%d span, %d hata span'i, kök %s/%s, %.0f ms", pc.an.SpanCount, pc.an.ErrorSpanCount, tr.Service, tr.Root, tr.WallMs)
		if fetch.Capped {
			h0.hop.Note = fmt.Sprintf("ClickHouse %d span tavanına takıldı; analiz ilk %d span üzerinden", traceStoreSpanCap, traceStoreSpanCap)
		}
		pc.podRefs, _ = pivotPodRefs(pc.spans, pc.an)
		pc.topOpService, pc.topOpName, pc.topOpSpanMs, pc.topOpHasValue = pivotTopOp(pc.spans, pc.an)
	case fetch.Stub != nil:
		st := fetch.Stub
		pc.start, pc.end, pc.haveWindow = time.Unix(0, st.StartTimeNs).UTC(), time.Unix(0, st.EndTimeNs).UTC(), true
		tr.Root, tr.Service, tr.SpanCount, tr.ErrorSpans, tr.StartMs = st.RootName, st.RootService, int(st.SpanCount), int(st.ErrorCount), st.StartTimeNs/1e6
		tr.WallMs = ttNsToMs(st.EndTimeNs - st.StartTimeNs)
		if fetch.StubReason == "aged_out" {
			h0.hop.State = hopEmpty
			h0.hop.Summary = "trace span'leri yok: ham saklama süresi dolmuş, yalnız trace_summary_5m özeti var"
		} else {
			h0.hop.State, h0.failed = hopPartial, true
			h0.hop.Summary = "trace özeti var ama span'ler okunamadı (" + fetch.StubReason + ")"
		}
	case h0.hop.State == "":
		if fetch.StubReason == "stub_unreadable" {
			h0.hop.State, h0.failed = hopError, true
			h0.hop.Summary = "trace okunamadı: span yok ve trace_summary_5m özeti okunamadı — yokluk kanıtı DEĞİL"
		} else {
			h0.hop.State = hopEmpty
			h0.hop.Summary = "veri yok: trace Coremetry ClickHouse'ta yok (saklama dışı, örneklenmiş ya da hiç ulaşmamış)"
		}
	}
	if tr.Service != "" || tr.SpanCount > 0 {
		res.Trace = tr
	}
	if r.runtimeAlerts != nil {
		pc.alerts = r.runtimeAlerts(ctx)
	}
	{
		ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, DurationMs: h0.hop.DurationMs,
			Query: "GetTrace: SELECT … FROM spans WHERE trace_id = " + sqlQuote(traceID) + fmt.Sprintf(" LIMIT %d", traceStoreSpanCap)}
		if pc.haveWindow {
			ev.Window = evWin(pc.start, pc.end.Add(time.Millisecond))
		} else {
			ev.Window = evWin(req.From, req.To)
		}
		ev.Rows = intPtr(len(pc.spans))
		if candEv != nil {
			// Servis seçimi: aday araması + trace okuması TEK çip (kanıt tavanı 6).
			ev.Query = candEv.Query + qtEvidenceQueryJoin + ev.Query
			ev.Env = candEv.Env
			ev.DurationMs += candEv.DurationMs
		}
		h0.evidences = append(h0.evidences, cosreHopEvidence{Hop: pivotHopTrace, As: "trace", Request: map[string]any{"trace_id": traceID}, cosreEvidence: ev})
	}
	// Bulgular: hata span'leri (exceptions ailesi — ek okuma yok).
	for i, es := range pc.an.ErrorSpans {
		if i >= 2 {
			break
		}
		why := es.ExceptionType
		if es.ExceptionMessage != "" {
			why = strings.TrimSpace(why + ": " + es.ExceptionMessage)
		}
		if why == "" {
			why = es.StatusMessage
		}
		if why == "" {
			why = "status=error"
		}
		h0.findings = append(h0.findings, pivotFinding{Hop: pivotHopTrace, Subject: es.Service + " · " + es.Name, Level: levelCrit,
			Reason: truncateRunes("hata span'i: "+why, pivotReasonRunes)})
	}
	// trace_list bloğu: adaylar (servis seçimi) ya da trace'in kendisi.
	if len(candidates) > 0 || res.Trace != nil {
		win := EvBlockWindow{FromNs: req.From.UnixNano(), ToNs: req.To.UnixNano()}
		p := EvTraceListPayload{Query: "pivot · " + firstNonEmpty(req.Service, tr.Service, traceID[:8]), Window: win, Traces: []EvTraceRow{}, Truncated: candHasMore}
		for _, c := range candidates {
			p.Traces = append(p.Traces, EvTraceRow{TraceID: c.TraceID, StartTime: c.StartMs * 1e6, ServiceName: c.Service, RootName: c.Root,
				DurationMs: c.DurationMs, SpanCount: c.Spans, HasError: c.Error})
		}
		if len(candidates) == 0 {
			if pc.haveWindow {
				p.Window = EvBlockWindow{FromNs: pc.start.UnixNano(), ToNs: pc.end.UnixNano()}
			}
			p.Traces = append(p.Traces, EvTraceRow{TraceID: traceID, StartTime: tr.StartMs * 1e6, ServiceName: tr.Service, RootName: tr.Root,
				DurationMs: tr.WallMs, SpanCount: uint64(tr.SpanCount), HasError: tr.ErrorSpans > 0})
		}
		h0.blocks = append(h0.blocks, pivotBlock{Hop: pivotHopTrace, Type: blocks.TypeTraceList, Payload: p})
	}

	// ── H1–H4 paralel ──────────────────────────────────────────────────
	type job struct {
		name  string
		phase string
		cap   time.Duration
		run   func(context.Context) hopOutcome
	}
	jobs := []job{
		{pivotHopPods, "Altyapı", pivotCapPods, func(c context.Context) hopOutcome { return pivotRunPods(c, r, pc) }},
		{pivotHopJVM, "Metrik", pivotCapJVM, func(c context.Context) hopOutcome { return pivotRunJVM(c, r, pc) }},
		{pivotHopLogs, "Log", pivotCapLogs, func(c context.Context) hopOutcome { return pivotRunLogs(c, r, pc, req) }},
		{pivotHopOp, "Sağlık", pivotCapOp, func(c context.Context) hopOutcome { return pivotRunOp(c, r, pc) }},
	}
	outs := make([]hopOutcome, len(jobs))
	type done struct {
		i int
		o hopOutcome
	}
	ch := make(chan done, len(jobs))
	pending := 0
	reportProgress(ctx, "Altyapı", "pod · JVM · log · operasyon taban çizgisi paralel okunuyor")
	for i, j := range jobs {
		dl := hopDeadline(remaining(), j.cap)
		if dl <= 0 {
			outs[i] = hopOutcome{hop: pivotHop{Hop: j.name, State: hopSkipped, Summary: "süre bütçesi doldu"}, failed: true}
			continue
		}
		pending++
		go func(i int, j job, dl time.Duration) {
			hctx, cancel := context.WithTimeout(ctx, dl)
			defer cancel()
			elapsed := stopwatch()
			o := j.run(hctx)
			o.hop.Hop = j.name
			o.hop.DurationMs = elapsed()
			ch <- done{i, o}
		}(i, j, dl)
	}
	got := make([]bool, len(jobs))
	budgetTimer := time.NewTimer(time.Until(deadline))
	defer budgetTimer.Stop()
wait:
	for pending > 0 {
		select {
		case dn := <-ch:
			outs[dn.i], got[dn.i] = dn.o, true
			pending--
			reportProgress(ctx, jobs[dn.i].phase, pivotHopLabelTR(jobs[dn.i].name)+": "+truncateRunes(dn.o.hop.Summary, 80))
		case <-budgetTimer.C:
			break wait
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return PivotResult{}, ctx.Err()
			}
			break wait
		}
	}
	for i, j := range jobs {
		if outs[i].hop.State == "" && !got[i] {
			outs[i] = hopOutcome{hop: pivotHop{Hop: j.name, State: hopTimeout, Summary: "süre bütçesi doldu — halka bitmedi"}, failed: true}
		}
	}

	// ── birleştir ──────────────────────────────────────────────────────
	all := append([]hopOutcome{h0}, outs...)
	data := &pivotData{Pods: []pivotPodRow{}, JVM: []pivotJVMRow{}, Logs: []qlRow{}, Candidates: candidates, CandidatesHasMore: candHasMore, Blocks: []pivotBlock{}}
	var findings []pivotFinding
	allEmpty := true
	for _, o := range all {
		if len(o.evidences) > 0 {
			o.hop.EvidenceRef = fmt.Sprintf("evidences[%d]", len(res.Evidences))
		}
		res.Hops = append(res.Hops, o.hop)
		for _, ev := range o.evidences {
			if len(res.Evidences) < compositeEvidenceMax {
				res.Evidences = append(res.Evidences, ev)
			}
		}
		findings = append(findings, o.findings...)
		data.Pods = append(data.Pods, o.pods...)
		data.JVM = append(data.JVM, o.jvm...)
		data.Logs = append(data.Logs, o.logs...)
		for _, b := range o.blocks {
			if len(data.Blocks) < pivotMaxBlocks {
				data.Blocks = append(data.Blocks, b)
			}
		}
		if o.failed {
			res.Partial = true
		}
		if o.hop.State == hopOK || o.hop.State == hopPartial {
			allEmpty = false
		}
	}
	res.Findings, res.FindingsHasMore = rankFindings(findings)
	if len(res.Evidences) > 0 {
		e := res.Evidences[0].cosreEvidence
		res.Evidence = &e
	}
	res.Playbook = pivotBuildPlaybook(pc, tr, all)
	res.OK = true
	if allEmpty {
		res.Empty = true
		res.Message = "veri yok: trace " + traceID[:8] + " için hiçbir halkada veri bulunamadı — halka notlarına bak (okunamadı ≠ yok)"
	}
	res.Data = data
	return res, nil
}

// pivotMaxBlocks — sohbet içi blok tavanı (trace_list, pod tablosu, JVM tablosu,
// log tablosu, pod grafiği).
const pivotMaxBlocks = 5

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// pivotTopPath — kritik yolun yolda en uzun kalan ≤n adımı (yol sırasıyla).
func pivotTopPath(steps []traceToolPathStep, n int) []traceToolPathStep {
	out := ttTrimPathSteps(steps, n)
	if out == nil {
		return []traceToolPathStep{}
	}
	return out
}

// pivotTopOp — SAF: kritik yolda öz süresi en büyük adımın servisi/adı ve o
// span'in süresi (bu trace'te X ms).
func pivotTopOp(spans []chstore.SpanRow, an traceToolAnalysis) (service, name string, spanMs float64, ok bool) {
	best := -1.0
	var id string
	for _, st := range an.CriticalPath {
		if st.SelfOnPathMs > best {
			best, id, service, name = st.SelfOnPathMs, st.SpanID, st.Service, st.Name
		}
	}
	if id == "" {
		return "", "", 0, false
	}
	for i := range spans {
		if spans[i].SpanID == id {
			return service, name, ttNsToMs(ttSpanDur(&spans[i])), true
		}
	}
	return service, name, 0, false
}

// pivotHopLabelTR — ilerleme etiketinin halka adı.
func pivotHopLabelTR(h string) string {
	switch h {
	case pivotHopPods:
		return "pod'lar"
	case pivotHopJVM:
		return "JVM"
	case pivotHopLogs:
		return "loglar"
	case pivotHopOp:
		return "operasyon taban çizgisi"
	}
	return "trace"
}

// ── H1 pods ─────────────────────────────────────────────────────────────

func pivotRunPods(ctx context.Context, r pivotReaders, pc pivotCtx) hopOutcome {
	o := hopOutcome{hop: pivotHop{Hop: pivotHopPods}}
	if !pc.haveSpans {
		o.hop.State, o.hop.Summary = hopSkipped, "trace span'leri yok — pod kümesi çıkarılamadı"
		return o
	}
	if len(pc.podRefs) == 0 {
		o.hop.State, o.hop.Summary = hopEmpty, "veri yok: span'ler k8s.pod.name / host.name taşımıyor"
		return o
	}
	if r.tracePods == nil {
		o.hop.State, o.hop.Summary = hopSkipped, "Thanos yapılandırılmamış — pod CPU/bellek okunamaz"
		return o
	}
	from, to := pc.start.Add(-pivotPodPad), pc.end.Add(pivotPodPad)
	if to.Sub(from) > pivotPodMaxWindow {
		from = to.Add(-pivotPodMaxWindow)
	}
	cvs := sortedKeys(pc.podRefs)
	var states []string
	var notes []string
	chart := EvChartPayload{Title: "pod CPU / bellek · trace ±5 dk", Window: EvBlockWindow{FromNs: from.UnixNano(), ToNs: to.UnixNano()}, Series: []EvChartSeries{}}
	for _, cv := range cvs {
		refs := pc.podRefs[cv]
		if cv == "" {
			states = append(states, hopUnmapped)
			notes = append(notes, fmt.Sprintf("%d pod'un span'leri cluster özniteliği taşımıyor (no_cluster_value)", len(refs)))
			for _, ref := range refs {
				row, _ := classifyPod(ref, "", nil)
				row.State = "unmapped"
				o.pods = append(o.pods, row)
			}
			continue
		}
		trefs := make([]thanos.TracePodRef, 0, len(refs))
		for _, ref := range refs {
			trefs = append(trefs, thanos.TracePodRef{Namespace: ref.Namespace, Pod: ref.Pod})
		}
		elapsed := stopwatch()
		hop, err := r.tracePods.TracePods(ctx, cv, trefs, from, to)
		dur := elapsed()
		if err != nil {
			st := hopError
			if errors.Is(err, context.DeadlineExceeded) || cosreClassify(err) == cosreErrTimeout {
				st = hopTimeout
			}
			states = append(states, st)
			notes = append(notes, cosreErrMsg("cluster "+cv+" okunamadı", err))
			o.failed = true
			continue
		}
		if !hop.Thanos {
			states = append(states, hopSkipped)
			notes = append(notes, "Thanos'ta etkin cluster yok")
			continue
		}
		if !hop.Mapped {
			states = append(states, hopUnmapped)
			notes = append(notes, fmt.Sprintf("cluster %q bir Remote Cluster kaydına bağlı değil (%s)", cv, firstNonEmpty(hop.UnmappedReason, "no_remote_cluster")))
			continue
		}
		bySeries := map[string]*thanos.TracePodSeries{}
		for i := range hop.Pods {
			p := &hop.Pods[i]
			bySeries[p.Namespace+"\x00"+p.Pod] = p
			if _, ok := bySeries["\x00"+p.Pod]; !ok {
				bySeries["\x00"+p.Pod] = p
			}
		}
		sampled := 0
		for _, ref := range refs {
			s := bySeries[ref.Namespace+"\x00"+ref.Pod]
			if s == nil {
				s = bySeries["\x00"+ref.Pod]
			}
			row, why := classifyPod(ref, hop.ClusterName, s)
			if row.State == "ok" {
				sampled++
			}
			o.pods = append(o.pods, row)
			if row.Level != levelOK {
				o.findings = append(o.findings, pivotFinding{Hop: pivotHopPods, Subject: ref.Pod, Level: row.Level, Reason: truncateRunes(why, pivotReasonRunes)})
			}
			if s != nil {
				chart.Series = append(chart.Series, pivotPodSeries(ref.Pod, "cores", s.CPU, hop.Start, hop.Step)...)
				chart.Series = append(chart.Series, pivotPodSeries(ref.Pod, "bytes", s.Mem, hop.Start, hop.Step)...)
			}
		}
		st := hopOK
		if sampled == 0 {
			st = hopEmpty
		}
		if hop.Instant != "" && hop.Instant != "ok" {
			notes = append(notes, "kube-state-metrics anlık okumaları "+hop.Instant+" — limit/restart/faz eksik olabilir")
		}
		states = append(states, st)
		ev := cosreEvidence{Source: evSourceThanos, QueryKind: evKindPromQL, Query: hop.Query, Window: evWin(from, to), Cluster: hop.ClusterName,
			Rows: intPtr(len(refs)), DurationMs: dur}
		top := pivotWorstPod(o.pods, hop.ClusterName)
		o.evidences = append(o.evidences, cosreHopEvidence{Hop: pivotHopPods, As: "pod",
			Request:       map[string]any{"cluster": hop.ClusterID, "namespace": top.Namespace, "pod": top.Pod, "service": top.Service},
			cosreEvidence: ev})
	}
	o.hop.State = pivotMergeStates(states)
	if o.hop.State == hopError || o.hop.State == hopTimeout || o.hop.State == hopPartial {
		o.failed = true
	}
	o.hop.Note = strings.Join(notes, "; ")
	o.hop.Summary = pivotPodsSummary(o.pods)
	if len(o.pods) > 0 {
		cols := []EvTableColumn{{Key: "pod", Label: "Pod", Kind: "text"}, {Key: "namespace", Label: "Namespace", Kind: "text"},
			{Key: "cpu", Label: "CPU (çekirdek, maks)", Kind: "num"}, {Key: "mem", Label: "Bellek (MiB, maks)", Kind: "num"},
			{Key: "limitPct", Label: "Limit %", Kind: "num"}, {Key: "restarts", Label: "Restart", Kind: "num"},
			{Key: "lastTerm", Label: "Son sonlanma", Kind: "text"}}
		p := EvTablePayload{Title: "trace pod'ları", Columns: cols, Rows: [][]any{}}
		for _, row := range o.pods {
			var cpu, mem, lim, rs any
			if row.CPUMaxCores != nil {
				cpu = *row.CPUMaxCores
			}
			if row.MemMaxBytes != nil {
				mem = round4(*row.MemMaxBytes / (1 << 20))
			}
			if l := maxPtr(row.CPULimitPct, row.MemLimitPct); l != nil {
				lim = *l
			}
			if row.Restarts != nil {
				rs = *row.Restarts
			}
			p.Rows = append(p.Rows, []any{row.Pod, row.Namespace, cpu, mem, lim, rs, row.LastTermReason})
		}
		o.blocks = append(o.blocks, pivotBlock{Hop: pivotHopPods, Type: blocks.TypeTable, Payload: p})
	}
	if len(chart.Series) > 0 && len(o.evidences) > 0 {
		o.blocks = append(o.blocks, pivotBlock{Hop: pivotHopPods, Type: blocks.TypeChart, Payload: chart})
	}
	return o
}

func maxPtr(a, b *float64) *float64 {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case *a >= *b:
		return a
	}
	return b
}

// pivotPodSeries — hizalı Thanos serisi → grafik serisi (nil kova atlanır,
// son pivotChartMaxPoints nokta).
func pivotPodSeries(pod, unit string, vs []*float64, start int64, step int) []EvChartSeries {
	if len(vs) == 0 || step <= 0 {
		return nil
	}
	name := "cpu"
	if unit == "bytes" {
		name = "mem"
	}
	s := EvChartSeries{Name: name, Group: pod, Unit: unit, Points: [][2]float64{}}
	for i, v := range vs {
		if v == nil {
			continue
		}
		s.Points = append(s.Points, [2]float64{float64((start + int64(i*step)) * 1000), *v})
	}
	if len(s.Points) > pivotChartMaxPoints {
		s.Points = s.Points[len(s.Points)-pivotChartMaxPoints:]
	}
	if len(s.Points) == 0 {
		return nil
	}
	return []EvChartSeries{s}
}

// pivotWorstPod — kanıt bağlantısının pod'u: bu cluster'ın en kötü seviyeli pod'u.
func pivotWorstPod(rows []pivotPodRow, cluster string) pivotPodRow {
	var best pivotPodRow
	found := false
	for _, r := range rows {
		if r.Cluster != cluster {
			continue
		}
		if !found || levelRank(r.Level) < levelRank(best.Level) {
			best, found = r, true
		}
	}
	return best
}

// pivotMergeStates — SAF: cluster başına durumlar → halka durumu.
func pivotMergeStates(states []string) string {
	if len(states) == 0 {
		return hopEmpty
	}
	count := map[string]int{}
	for _, s := range states {
		count[s]++
	}
	if count[hopOK] > 0 {
		if count[hopOK] == len(states) {
			return hopOK
		}
		if count[hopError]+count[hopTimeout] > 0 {
			return hopPartial
		}
		return hopOK
	}
	for _, s := range []string{hopTimeout, hopError, hopUnmapped, hopSkipped} {
		if count[s] == len(states) {
			return s
		}
	}
	if count[hopError]+count[hopTimeout] > 0 {
		return hopError
	}
	if count[hopUnmapped] > 0 {
		return hopUnmapped
	}
	return hopEmpty
}

func pivotPodsSummary(rows []pivotPodRow) string {
	if len(rows) == 0 {
		return "veri yok: pod metriği okunamadı"
	}
	crit, warn, sampled := 0, 0, 0
	for _, r := range rows {
		switch r.Level {
		case levelCrit:
			crit++
		case levelWarn:
			warn++
		}
		if r.State == "ok" {
			sampled++
		}
	}
	if sampled == 0 {
		return fmt.Sprintf("veri yok: %d pod için Thanos örneği yok", len(rows))
	}
	return fmt.Sprintf("%d pod (%d örnekli): %d kritik, %d uyarı", len(rows), sampled, crit, warn)
}

// ── H2 JVM ──────────────────────────────────────────────────────────────

func pivotRunJVM(ctx context.Context, r pivotReaders, pc pivotCtx) hopOutcome {
	o := hopOutcome{hop: pivotHop{Hop: pivotHopJVM}}
	if !pc.haveSpans {
		o.hop.State, o.hop.Summary = hopSkipped, "trace span'leri yok — pod kümesi çıkarılamadı"
		return o
	}
	if r.runtime == nil {
		o.hop.State, o.hop.Summary = hopSkipped, "JVM runtime okuyucusu bağlı değil"
		return o
	}
	want := map[string]map[string]bool{} // servis (küçük harf) → pod
	var services []string
	for _, refs := range pc.podRefs {
		for _, ref := range refs {
			k := strings.ToLower(ref.Service)
			if want[k] == nil {
				want[k] = map[string]bool{}
				services = append(services, ref.Service)
			}
			want[k][ref.Pod] = true
		}
	}
	if len(want) == 0 {
		o.hop.State, o.hop.Summary = hopEmpty, "veri yok: span'ler pod kimliği taşımıyor"
		return o
	}
	sort.Strings(services)
	from, to := pc.start.Add(-pivotJVMPadBefore), pc.end.Add(pivotJVMPadAfter)
	elapsed := stopwatch()
	heap, herr := r.runtime.JVMHeapPodUsage(ctx, from, to)
	var gc []chstore.CapacitySample
	var gerr error
	if herr == nil || !errors.Is(herr, context.DeadlineExceeded) {
		gc, gerr = r.runtime.JVMGCPodPause(ctx, from, to)
	}
	dur := elapsed()
	q := qmRuntimeQueryText("jvm_heap", map[bool]string{true: "VictoriaMetrics", false: "ClickHouse metric_points"}[r.runtimeSource == evSourceVM], strings.Join(services, ","), from, to) +
		"\n" + qmRuntimeQueryText("jvm_gc", "", strings.Join(services, ","), from, to)
	ev := cosreEvidence{Source: r.runtimeSource, QueryKind: evKindSummary, Query: q, Window: evWin(from, to), DurationMs: dur}
	if herr != nil && gerr != nil {
		o.failed = true
		o.hop.State = hopError
		if errors.Is(herr, context.DeadlineExceeded) || cosreClassify(herr) == cosreErrTimeout {
			o.hop.State = hopTimeout
		}
		o.hop.Summary = cosreErrMsg("JVM metriği okunamadı", herr)
		return o
	}
	pick := func(in []chstore.CapacitySample) map[string]*chstore.CapacitySample {
		m := map[string]*chstore.CapacitySample{}
		for i := range in {
			s := &in[i]
			pods := want[strings.ToLower(s.Instance)]
			if pods != nil && pods[s.Subkey] {
				m[strings.ToLower(s.Instance)+"\x00"+s.Subkey] = s
			}
		}
		return m
	}
	hm, gm := pick(heap), pick(gc)
	keys := map[string]bool{}
	for k := range hm {
		keys[k] = true
	}
	for k := range gm {
		keys[k] = true
	}
	svcName := map[string]string{}
	for _, s := range services {
		svcName[strings.ToLower(s)] = s
	}
	for _, k := range sortedKeys(keys) {
		svc, pod, _ := strings.Cut(k, "\x00")
		row := classifyJVM(svcName[svc], pod, hm[k], gm[k], pc.alerts)
		o.jvm = append(o.jvm, row)
		if row.Level != levelOK {
			o.findings = append(o.findings, pivotFinding{Hop: pivotHopJVM, Subject: pod, Level: row.Level, Reason: truncateRunes(row.Reason, pivotReasonRunes)})
		}
	}
	ev.Rows = intPtr(len(o.jvm))
	o.evidences = append(o.evidences, cosreHopEvidence{Hop: pivotHopJVM, As: queryMetricsName,
		Request: map[string]any{"service": services[0], "family": "jvm_heap"}, cosreEvidence: ev})
	if herr != nil || gerr != nil {
		o.failed = true
		o.hop.Note = "heap ya da GC okumalarından biri başarısız — tablo eksik"
	}
	if len(o.jvm) == 0 {
		o.hop.State = hopEmpty
		o.hop.Summary = "veri yok (OTel JVM) — trace pod'ları için jvm.memory/jvm.gc metriği yok; JMX Thanos'ta olabilir (Service › Infrastructure)"
		return o
	}
	o.hop.State = hopOK
	if o.failed {
		o.hop.State = hopPartial
	}
	crit, warn := 0, 0
	for _, row := range o.jvm {
		switch row.Level {
		case levelCrit:
			crit++
		case levelWarn:
			warn++
		}
	}
	o.hop.Summary = fmt.Sprintf("%d pod JVM: %d kritik, %d uyarı", len(o.jvm), crit, warn)
	p := EvTablePayload{Title: "trace pod'ları · JVM", Columns: []EvTableColumn{
		{Key: "pod", Label: "Pod", Kind: "text"}, {Key: "heapPct", Label: "Heap %", Kind: "num"},
		{Key: "postGcPct", Label: "GC sonrası %", Kind: "num"}, {Key: "gcMs", Label: "GC ms", Kind: "num"},
		{Key: "level", Label: "Seviye", Kind: "level"}}, Rows: [][]any{}}
	for _, row := range o.jvm {
		p.Rows = append(p.Rows, []any{row.Pod, ptrVal(row.HeapPct), ptrVal(row.PostGCPct), ptrVal(row.GCPauseMs), row.Level})
	}
	o.blocks = append(o.blocks, pivotBlock{Hop: pivotHopJVM, Type: blocks.TypeTable, Payload: p})
	return o
}

func ptrVal(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ── H3 logs ─────────────────────────────────────────────────────────────

func pivotRunLogs(ctx context.Context, r pivotReaders, pc pivotCtx, req PivotRequest) hopOutcome {
	o := hopOutcome{hop: pivotHop{Hop: pivotHopLogs}}
	if r.logs == nil {
		o.hop.State, o.hop.Summary = hopSkipped, "log arka ucu yapılandırılmamış"
		return o
	}
	var from, to time.Time
	switch {
	case pc.haveWindow:
		from, to = pc.start.Add(-pivotLogPadS*time.Second), pc.end.Add(pivotLogPadS*time.Second)
	case r.traceWindow != nil:
		if lo, hi, ok := r.traceWindow(ctx, pc.traceID); ok && !lo.IsZero() && !hi.Before(lo) {
			from, to = lo.Add(-pivotLogPadS*time.Second), hi.Add(pivotLogPadS*time.Second)
		}
	}
	if from.IsZero() {
		from, to = req.From, req.To
		o.hop.Note = "trace penceresi bilinmiyor; sohbet çıpasından geriye " + req.Window
	}
	backend := r.logs.Backend()
	source := logsBackendSource(r.logs)
	f := logstore.Filter{TraceID: pc.traceID, From: from, To: to, Limit: pivotLogFetch}
	ev := cosreEvidence{Source: source, QueryKind: evKindSummary, Query: qlQueryText(backend, f), Window: evWin(from, to)}
	elapsed := stopwatch()
	page, err := logstore.LogsForTrace(ctx, r.logs, pc.traceID, from, to, pivotLogFetch)
	ev.DurationMs = elapsed()
	evh := cosreHopEvidence{Hop: pivotHopLogs, As: queryLogsName, Request: map[string]any{"trace_id": pc.traceID}, cosreEvidence: ev}
	if err != nil {
		st := sourcestate.FromError(logsSource, backend, err)
		o.failed = true
		o.evidences = append(o.evidences, evh)
		if errors.Is(err, logstore.ErrBackendSlow) || logsDegradedState(st.State) {
			o.hop.State = hopDegraded
			o.hop.Summary = "log arka ucu şu an okunamadı (" + string(st.State) + ") — okunamadı ≠ log yok"
			return o
		}
		o.hop.State = hopError
		o.hop.Summary = cosreErrMsg(source+" log okuması başarısız", err)
		return o
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
		return recs[i].Timestamp < recs[j].Timestamp
	})
	m := logFieldMapping(ctx, r.logs, true)
	errCount := 0
	var firstErr *logstore.LogRecord
	for _, rec := range recs {
		if rec.Severity >= qlOTelErrorFloor {
			errCount++
			if firstErr == nil {
				firstErr = rec
			}
		}
		if len(o.logs) >= pivotLogKeep {
			continue
		}
		o.logs = append(o.logs, qlRow{TsMs: rec.Timestamp / 1e6, Severity: logSeverityLabel(rec), Service: rec.ServiceName,
			Pod: logstore.RoleValue(rec, logstore.RolePod, m), TraceID: rec.TraceID,
			Body: promptfmt.FenceSafe(truncateRunes(rec.Body, qlBodyMaxRunes))})
	}
	ev.Rows = intPtr(len(o.logs))
	evh.cosreEvidence = ev
	o.evidences = append(o.evidences, evh)
	if len(o.logs) == 0 {
		o.hop.State = hopEmpty
		o.hop.Summary = "veri yok: " + windowTR(from, to) + " penceresinde bu trace kimliğini taşıyan log yok (" + source + ")"
		return o
	}
	o.hop.State = hopOK
	o.hop.Summary = fmt.Sprintf("%d log satırı (%d ERROR+), şiddete göre ilk %d", len(recs), errCount, len(o.logs))
	if firstErr != nil {
		o.findings = append(o.findings, pivotFinding{Hop: pivotHopLogs, Subject: firstNonEmpty(firstErr.ServiceName, "log"), Level: levelWarn,
			Reason: truncateRunes(fmt.Sprintf("%d ERROR+ log; ilki: %s", errCount, strings.Join(strings.Fields(firstErr.Body), " ")), pivotReasonRunes)})
	}
	p := EvTablePayload{Title: "trace logları · şiddete göre", Columns: []EvTableColumn{
		{Key: "time", Label: "Zaman", Kind: "time"}, {Key: "severity", Label: "Seviye", Kind: "severity"},
		{Key: "source", Label: "Pod / servis", Kind: "text"}, {Key: "body", Label: "Mesaj", Kind: "text"},
		{Key: "traceId", Label: "Trace", Kind: "trace"}}, Rows: [][]any{}}
	for _, l := range o.logs {
		p.Rows = append(p.Rows, []any{l.TsMs, l.Severity, firstNonEmpty(l.Pod, l.Service), truncateRunes(l.Body, evBlockBodyRunes-1), l.TraceID})
	}
	o.blocks = append(o.blocks, pivotBlock{Hop: pivotHopLogs, Type: blocks.TypeTable, Payload: p})
	return o
}

// ── H4 op_baseline ──────────────────────────────────────────────────────

func pivotRunOp(ctx context.Context, r pivotReaders, pc pivotCtx) hopOutcome {
	o := hopOutcome{hop: pivotHop{Hop: pivotHopOp}}
	if !pc.haveSpans || pc.topOpName == "" {
		o.hop.State, o.hop.Summary = hopSkipped, "trace span'leri yok — kritik yol operasyonu çıkarılamadı"
		return o
	}
	if r.opSummary == nil {
		o.hop.State, o.hop.Summary = hopSkipped, "ClickHouse okuyucusu bağlı değil"
		return o
	}
	to := pc.start
	from := to.Add(-pivotOpLookback)
	q := "GetOperationSummary FROM operation_summary_5m WHERE service_name = " + sqlQuote(pc.topOpService) +
		" AND time >= " + sqlLiteral(from) + " AND time < " + sqlLiteral(to) + " — env yok (MV ortam boyutu taşımıyor)"
	elapsed := stopwatch()
	ops, err := r.opSummary(ctx, pc.topOpService, from, to)
	ev := cosreEvidence{Source: evSourceCH, QueryKind: evKindSummary, Query: q, Window: evWin(from, to), DurationMs: elapsed()}
	evh := cosreHopEvidence{Hop: pivotHopOp, As: queryTracesName,
		Request: map[string]any{"service": pc.topOpService, "operation": pc.topOpName, "sort_by": qtSortSlowest}}
	if err != nil {
		o.failed = true
		o.hop.State = hopError
		if errors.Is(err, context.DeadlineExceeded) || cosreClassify(err) == cosreErrTimeout {
			o.hop.State = hopTimeout
		}
		o.hop.Summary = cosreErrMsg("operasyon taban çizgisi okunamadı", err)
		evh.cosreEvidence = ev
		o.evidences = append(o.evidences, evh)
		return o
	}
	var row *chstore.OperationSummary
	for i := range ops {
		if ops[i].Name == pc.topOpName {
			row = &ops[i]
			break
		}
	}
	ev.Rows = intPtr(map[bool]int{true: 1, false: 0}[row != nil])
	evh.cosreEvidence = ev
	o.evidences = append(o.evidences, evh)
	if row == nil || row.P99Ms <= 0 {
		o.hop.State = hopEmpty
		o.hop.Summary = "veri yok: " + pc.topOpService + "/" + pc.topOpName + " trace'ten önceki 1 saatte operation_summary_5m'de yok"
		return o
	}
	o.opFound = true
	o.hop.State = hopOK
	level, mult := pivotOpLevel(pc.topOpSpanMs, row.P99Ms)
	o.hop.Summary = fmt.Sprintf("bu trace'te %s %.0f ms; operasyon p99 %.0f ms (%.1f×)", pc.topOpName, pc.topOpSpanMs, row.P99Ms, mult)
	if level != levelOK {
		o.findings = append(o.findings, pivotFinding{Hop: pivotHopOp, Subject: pc.topOpService + " · " + pc.topOpName, Level: level,
			Reason: fmt.Sprintf("trace'teki süre %.0f ms, son 1 saatin p99'u %.0f ms (%.1f×)", pc.topOpSpanMs, row.P99Ms, mult)})
	}
	return o
}

// pivotOpLevel — SAF: trace'teki op süresi / op p99 → seviye + kat.
// v0.10.1299 — oran anlamı korunur, ama mutlak tabanlar (healthverdict.
// LatencyFloorsMet: süre ≥ 100 ms VE süre − p99 ≥ 50 ms) geçilmeden seviye
// yükselmez: 3 ms'lik bir DB çağrısının 2 ms p99'a göre "1.5×" olması bulgu
// değildir (operatör 2026-10-17).
func pivotOpLevel(spanMs, p99 float64) (string, float64) {
	if p99 <= 0 {
		return levelOK, 0
	}
	mult := math.Round(10*spanMs/p99) / 10
	if !healthverdict.LatencyFloorsMet(spanMs, p99) {
		return levelOK, mult
	}
	switch {
	case spanMs/p99 >= pivotOpCritMult && spanMs >= pivotOpCritMin:
		return levelCrit, mult
	case spanMs/p99 >= pivotOpWarnMult:
		return levelWarn, mult
	}
	return levelOK, mult
}

// ── playbook ────────────────────────────────────────────────────────────

// pivotBuildPlaybook — belirti → aileler (anomaly.SymptomPlan) → her aile
// için halkaların sonucundan CheckedSignal (renderDeepEvidence şekli).
func pivotBuildPlaybook(pc pivotCtx, tr *pivotTrace, outs []hopOutcome) *pivotPlaybook {
	symptom := pivotSymptom(tr.ErrorSpans)
	pb := &pivotPlaybook{Symptom: symptom, Families: anomaly.SymptomPlan(symptom), Checked: []chstore.CheckedSignal{}}
	if pb.Families == nil {
		pb.Families = []string{}
	}
	byHop := map[string]hopOutcome{}
	for _, o := range outs {
		byHop[o.hop.Hop] = o
	}
	checkedFrom := func(family string, hs ...string) chstore.CheckedSignal {
		cs := chstore.CheckedSignal{Family: family}
		var details []string
		failed := 0
		for _, h := range hs {
			o := byHop[h]
			n := len(o.pods) + len(o.jvm) + len(o.logs)
			if o.opFound {
				n++
			}
			cs.Records += n
			switch o.hop.State {
			case hopError, hopTimeout, hopDegraded:
				failed++
				details = append(details, pivotHopLabelTR(h)+" okunamadı")
			default:
				details = append(details, pivotHopLabelTR(h)+": "+truncateRunes(o.hop.Summary, 80))
			}
		}
		cs.Found = cs.Records > 0
		cs.Detail = strings.Join(details, "; ")
		if !cs.Found && failed == 0 {
			cs.Detail = "bakıldı, kayıt yok — " + cs.Detail
		} else if !cs.Found && failed > 0 {
			cs.Detail = "okunamadı: " + cs.Detail
		}
		return cs
	}
	for _, f := range pb.Families {
		switch f {
		case "saturation":
			pb.Checked = append(pb.Checked, checkedFrom(f, pivotHopPods, pivotHopJVM))
		case "logs":
			pb.Checked = append(pb.Checked, checkedFrom(f, pivotHopLogs))
		case "operations":
			pb.Checked = append(pb.Checked, checkedFrom(f, pivotHopOp))
		case "exceptions":
			cs := chstore.CheckedSignal{Family: f, Records: pc.an.ErrorSpanCount, Found: pc.an.ErrorSpanCount > 0, Detail: "bakıldı, kayıt yok"}
			if cs.Found {
				cs.Detail = fmt.Sprintf("%d hata span'i (trace analizinden)", pc.an.ErrorSpanCount)
			}
			pb.Checked = append(pb.Checked, cs)
		case "business":
			pb.Checked = append(pb.Checked, pivotBusinessChecked(pc))
		default:
			pb.Checked = append(pb.Checked, chstore.CheckedSignal{Family: f, Detail: "bu araçta okunmaz (servis düzeyi sinyal)"})
		}
	}
	return pb
}

// pivotBusinessChecked — kök span'in FUNCTION_CODE / CHANNEL_CODE öznitelikleri.
func pivotBusinessChecked(pc pivotCtx) chstore.CheckedSignal {
	cs := chstore.CheckedSignal{Family: "business", Detail: "bakıldı, kayıt yok"}
	if !pc.haveSpans {
		cs.Detail = "trace span'leri yok"
		return cs
	}
	var root *chstore.SpanRow
	for i := range pc.spans {
		if pc.spans[i].SpanID == pc.an.Root.SpanID {
			root = &pc.spans[i]
			break
		}
	}
	if root == nil {
		return cs
	}
	var parts []string
	for _, k := range []string{"FUNCTION_CODE", "CHANNEL_CODE"} {
		if v := strings.TrimSpace(root.Attributes[k]); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	if len(parts) > 0 {
		cs.Found, cs.Records, cs.Detail = true, len(parts), "kök span: "+strings.Join(parts, ", ")
	}
	return cs
}

// ── araç ────────────────────────────────────────────────────────────────

// pivotCacheKey — SAF: hash-all-inputs. trace_id verilmişse seçim argümanları
// sonuca girmez ama yine hash'lenir (anahtar tek tip kalsın); servis seçiminde
// pencere 30 s kovaya oturur. Cluster kayıtları ve JVM eşikleri sonucu
// değiştirir → anahtarda.
func pivotCacheKey(req PivotRequest, clusters []ClusterRef, alerts chstore.RuntimeAlertConfig) string {
	cs := make([]string, 0, len(clusters))
	for _, c := range clusters {
		vals := append([]string(nil), c.SpanValues...)
		sort.Strings(vals)
		cs = append(cs, c.ID+"\x00"+c.Name+"\x00"+strings.Join(vals, ","))
	}
	sort.Strings(cs)
	ab, _ := json.Marshal(alerts)
	from, to := "", ""
	if req.TraceID == "" {
		from, to = fmt.Sprint(req.From.Unix()/30), fmt.Sprint(req.To.Unix()/30)
	}
	return "pivot:v1:" + fnvHex(req.TraceID, req.Service, req.Env, req.Operation, req.Window, req.Pick, from, to,
		fnvHex(cs...), string(ab))
}

func pivotFromTraceTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             PivotFromTraceName,
		ShortDescription: "Trace→pod→JVM→log pivotu tek çağrıda: trace_id ya da service (en yavaş seçilir). Halka başına durum, bulgular, playbook.",
		MinRole:          "",
		Description: "Cross-signal pivot in ONE deterministic call: trace → its pods (Thanos CPU/memory/limits/restarts, cluster-mapped) → " +
			"the same pods' JVM heap/GC (OTel runtime metrics; JMX is not queried) → the trace's logs (±5 min, ≤12 by severity) → " +
			"the slowest critical-path operation versus its own p99 over the hour before the trace (operation summary pre-aggregate, no env). " +
			"Args: trace_id (32 hex) OR service (+ optional operation, env, window 15m|1h|6h, pick slowest|slowest_error) — with a service the " +
			"slowest matching trace is chosen and up to 5 candidates are returned. Runs under a 15 s budget with per-hop caps; the chain never aborts: " +
			"every hop reports state ok|empty|partial|unmapped|degraded|error|timeout|skipped with its own note ('veri yok: …' for empty; " +
			"degraded/error mean the source could not be read, NOT that nothing exists). findings (≤5) rank crit/warn items with numeric reasons; " +
			"playbook{symptom, families, checked} says which signal families the symptom calls for and what each found. Each hop carries its own " +
			"evidence (query, window, rows). Read-only. Use when the question spans signals for one request ('bu trace neden yavaş, pod mu JVM mi'); " +
			"for a single signal use get_trace / query_metrics / get_logs_for_trace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"trace_id":  map[string]any{"type": "string", "description": "32-hex trace ID. Wins over service."},
				"service":   map[string]any{"type": "string", "description": "Exact service name; used when trace_id is absent (slowest trace in window)."},
				"operation": map[string]any{"type": "string", "description": "Exact operation/span name to narrow the trace choice. Optional."},
				"env":       map[string]any{"type": "string", "description": "Deployment environment for the trace choice (ignored when trace_id is given)."},
				"window":    map[string]any{"type": "string", "enum": pivotWindowEnum, "description": "Lookback for the trace choice. Default 1h."},
				"pick":      map[string]any{"type": "string", "enum": pivotPickEnum, "description": "slowest (default) or slowest_error (only traces with an error span)."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			req, a, warnings := parsePivotRequest(raw)
			from, to, label, ww := cosreWindow(ctx, a, cosreWindowSeconds["6h"], "1h")
			warnings = append(warnings, ww...)
			req.From, req.To, req.Window = from, to, label
			r := pivotReadersFrom(d)
			if d.Memo == nil {
				return runTracePivot(ctx, r, req, warnings)
			}
			var clusters []ClusterRef
			if d.Clusters != nil {
				clusters = d.Clusters()
			}
			alerts := chstore.DefaultRuntimeAlerts()
			if r.runtimeAlerts != nil {
				alerts = r.runtimeAlerts(ctx)
			}
			body, err := pivotMemo(ctx, d, r, req, clusters, alerts, warnings)
			if err != nil {
				return nil, err
			}
			return json.RawMessage(body), nil
		},
	}
}

// pivotMemo — aracın önbellekli yürütmesi (v0.10.1287'den beri AI SRE
// orkestratörü de aynı anahtarla — RunTracePivotCached).
func pivotMemo(ctx context.Context, d Deps, r pivotReaders, req PivotRequest, clusters []ClusterRef, alerts chstore.RuntimeAlertConfig, warnings []string) ([]byte, error) {
	key := pivotCacheKey(req, clusters, alerts) + ":w=" + fnvHex(warnings...)
	return d.Memo(ctx, key, pivotCacheTTL, func(c context.Context) (any, bool, error) {
		res, err := runTracePivot(c, r, req, warnings)
		if err != nil {
			return nil, false, err
		}
		// Kısmi / hatalı sonuç saklanmaz (v0.10.1119 kuralı).
		return res, res.OK && !res.Partial && res.Error == nil, nil
	})
}

// RunTracePivotCached — v0.10.1287 (AI SRE Faz 1b): runTracePivot'un
// Deps.Memo'lu eşi (Memo yoksa doğrudan); sohbet aracıyla aynı anahtar.
func RunTracePivotCached(ctx context.Context, d Deps, req PivotRequest) (PivotResult, error) {
	r := pivotReadersFrom(d)
	if d.Memo == nil {
		return runTracePivot(ctx, r, req, nil)
	}
	var clusters []ClusterRef
	if d.Clusters != nil {
		clusters = d.Clusters()
	}
	alerts := chstore.DefaultRuntimeAlerts()
	if r.runtimeAlerts != nil {
		alerts = r.runtimeAlerts(ctx)
	}
	body, err := pivotMemo(ctx, d, r, req, clusters, alerts, nil)
	if err != nil {
		return PivotResult{}, err
	}
	var out PivotResult
	if err := json.Unmarshal(body, &out); err != nil {
		return PivotResult{}, fmt.Errorf("pivot cache decode: %w", err)
	}
	return out, nil
}
