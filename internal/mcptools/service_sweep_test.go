package mcptools

// v0.10.1276 — CoSRE adım 7: servis sağlık taraması (pod / operasyon / fonksiyon + sorunluların logları)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

// syncLogStore — evre 2 paralel okur: -race altında güvenli stub.
type syncLogStore struct {
	stubLogStore
	mu      sync.Mutex
	filters []logstore.Filter
	err     error
	page    *logstore.Page
}

func (s *syncLogStore) Search(_ context.Context, f logstore.Filter) (*logstore.Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filters = append(s.filters, f)
	if s.err != nil {
		return nil, s.err
	}
	return s.page, nil
}

var sweepTo = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func sweepReq() SweepRequest {
	return SweepRequest{Service: "checkout", Window: "1h", From: sweepTo.Add(-time.Hour), To: sweepTo}
}

// baseSweepReaders — her bölüm okunur ve sağlıklı; testler tek tek bozar.
func baseSweepReaders() sweepReaders {
	return sweepReaders{
		windowRED: func(_ context.Context, _, _ string, from, _ time.Time) (chstore.ServiceWindowRED, error) {
			return chstore.ServiceWindowRED{Spans: 10000, Errors: 10, P99Ms: 120}, nil
		},
		openItems: func(context.Context, InboxOpenQuery) (InboxOpenResult, error) { return InboxOpenResult{}, nil },
		ops: func(context.Context, string, time.Time, time.Time) ([]chstore.OperationSummary, error) {
			return []chstore.OperationSummary{
				{Name: "GET /ok", SpanCount: 5000, ErrorRate: 0.1, P99Ms: 100, HasPrior: true, PriorSpanCount: 5000, PriorErrorRate: 0.1, PriorP99Ms: 100},
				{Name: "POST /pay", SpanCount: 3000, ErrorRate: 9, P99Ms: 900, HasPrior: true, PriorSpanCount: 3000, PriorErrorRate: 0.5, PriorP99Ms: 200,
					P99Sparkline: []float64{200, 900}, ErrorsSparkline: []uint64{1, 50}},
				{Name: "GET /rare", SpanCount: 50, ErrorRate: 60, P99Ms: 9000, HasPrior: true, PriorErrorRate: 0, PriorP99Ms: 10},
			}, nil
		},
		fnRollup: func(context.Context, string, time.Time, time.Time) ([]chstore.RollupSeries, error) {
			return nil, chstore.ErrRollupTablesMissing
		},
		fnSpans: func(context.Context, string, time.Time, time.Time) ([]chstore.BusinessSlice, error) {
			return []chstore.BusinessSlice{{Value: "F100", Calls: 2000, Errors: 200, ErrPct: 10}, {Value: "F200", Calls: 3000, Errors: 3, ErrPct: 0.1}}, nil
		},
		clusterValues: func(context.Context, string, time.Time, time.Time) ([]string, error) { return []string{"prod-a"}, nil },
		clusters:      func() []ClusterRef { return []ClusterRef{{ID: "c1", Name: "prod-a", SpanValues: []string{"prod-a"}}} },
		servicePods: func(_ context.Context, _ string, refs []string, _, _ time.Time, _ ServicePodsOpts) (ServicePodsResult, error) {
			return ServicePodsResult{Thanos: true, PodRegex: "(checkout)-.*", Clusters: []ServicePodsCluster{{
				ID: "c1", Name: "prod-a", Query: "PodMetrics", RestartsOK: true,
				Rows: []thanos.PodRow{
					{Namespace: "shop", Pod: "checkout-1", CPUPct: 20, MemPct: 40, Phase: "Running"},
					{Namespace: "shop", Pod: "checkout-2", CPUPct: 30, MemPct: 95, Phase: "Running", LastTermReason: "OOMKilled"},
				},
				Restarts: map[string]float64{"shop\x00checkout-2": 4},
			}}}, nil
		},
		runtime:       fakeRuntime{},
		runtimeSource: evSourceCH,
		runtimeAlerts: func(context.Context) chstore.RuntimeAlertConfig { return chstore.DefaultRuntimeAlerts() },
		logs:          &syncLogStore{page: &logstore.Page{}},
		searchTraces: func(context.Context, chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error) {
			return []chstore.TraceRow{{TraceID: strings.Repeat("ab", 16), StartTime: sweepTo.Add(-10 * time.Minute).UnixNano(), DurationMs: 900}}, 1, false, nil
		},
	}
}

func sectionByName(r SweepResult, n string) SweepSection {
	for _, s := range r.Sections {
		if s.Name == n {
			return s
		}
	}
	return SweepSection{}
}

func TestClassifyPodRow(t *testing.T) {
	rs := func(v float64) *float64 { return &v }
	for _, c := range []struct {
		name  string
		row   thanos.PodRow
		rs    *float64
		level string
		why   string
	}{
		{"sağlıklı", thanos.PodRow{Pod: "p", CPUPct: 50, MemPct: 60, Phase: "Running"}, rs(0), levelOK, ""},
		{"bellek %85 uyarı", thanos.PodRow{Pod: "p", MemPct: 86, Phase: "Running"}, rs(0), levelWarn, "bellek limitin %86"},
		{"CPU %90 kritik", thanos.PodRow{Pod: "p", CPUPct: 90, Phase: "Running"}, nil, levelCrit, "CPU limitin %90"},
		{"OOMKilled kritik", thanos.PodRow{Pod: "p", LastTermReason: "OOMKilled", Phase: "Running"}, rs(0), levelCrit, "OOMKilled"},
		{"Pending kritik", thanos.PodRow{Pod: "p", Phase: "Pending"}, nil, levelCrit, "faz Pending"},
		{"1 restart uyarı", thanos.PodRow{Pod: "p", Phase: "Running"}, rs(1), levelWarn, "1 restart"},
		{"3 restart kritik", thanos.PodRow{Pod: "p", Phase: "Running"}, rs(3), levelCrit, "3 restart"},
		{"restart okunamadı: ömür boyu sayaç puanlanmaz", thanos.PodRow{Pod: "p", Phase: "Running", Restarts: 40}, nil, levelOK, ""},
	} {
		got := classifySweepPod(c.row, c.rs)
		if got.Level != c.level || (c.why != "" && !strings.Contains(strings.Join(got.Reasons, ";"), c.why)) {
			t.Errorf("%s: %s %v", c.name, got.Level, got.Reasons)
		}
	}
	oom := classifySweepPod(thanos.PodRow{Pod: "a", LastTermReason: "OOMKilled"}, nil)
	mem := classifySweepPod(thanos.PodRow{Pod: "b", MemPct: 99}, nil)
	if oom.Impact <= mem.Impact {
		t.Errorf("OOM etkisi bellek %%'sinden büyük olmalı: %v ≤ %v", oom.Impact, mem.Impact)
	}
}

func TestClassifyJVM(t *testing.T) {
	cfg := chstore.DefaultRuntimeAlerts()
	heap := &chstore.CapacitySample{Usage: 50, Limit: 100, PostGC: 86}
	row := classifyJVM("checkout", "p1", heap, nil, cfg)
	c := sweepJVMCand(row)
	if c.Level != levelWarn || c.Kind != sweepKindJVM || c.Impact != 0.86 {
		t.Fatalf("post-GC %%86 uyarı bekleniyordu: %+v", c)
	}
	row = classifyJVM("checkout", "p1", &chstore.CapacitySample{Usage: 98, Limit: 100}, &chstore.CapacitySample{Usage: 3500}, cfg)
	if c := sweepJVMCand(row); c.Level != levelCrit || len(c.Reasons) != 2 {
		t.Fatalf("ham %%98 + GC 3500 ms kritik bekleniyordu: %+v", c)
	}
}

func TestScoreFindingsOrder(t *testing.T) {
	in := []sweepCand{
		{Kind: sweepKindOperation, Name: "op-warn", Level: levelWarn, Impact: 9},
		{Kind: sweepKindFunction, Name: "fn-crit-low", Level: levelCrit, Impact: 0.1},
		{Kind: sweepKindPod, Name: "pod-crit-high", Level: levelCrit, Impact: 3},
		{Kind: sweepKindOperation, Name: "ok", Level: levelOK, Impact: 100},
		{Kind: sweepKindJVM, Name: "jvm-crit-tie", Level: levelCrit, Impact: 0.1},
	}
	got, more := scoreFindings(in, 3)
	names := []string{}
	for _, f := range got {
		names = append(names, fmt.Sprintf("%d:%s", f.Rank, f.Name))
	}
	if strings.Join(names, ",") != "1:pod-crit-high,2:jvm-crit-tie,3:fn-crit-low" || !more {
		t.Fatalf("sıra %v more=%v", names, more)
	}
	if all, more := scoreFindings(in, 8); len(all) != 4 || more {
		t.Fatalf("ok satırı bulgu olmamalı: %d %v", len(all), more)
	}
	if sweepTopN(false, 0) != 3 || sweepTopN(true, 0) != 5 || sweepTopN(false, 50) != 8 || sweepTopN(true, 2) != 2 {
		t.Fatal("topN varsayılan/tavan")
	}
}

func TestSweepVolumeFloor(t *testing.T) {
	res, err := runServiceSweep(context.Background(), baseSweepReaders(), sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Findings {
		if f.Name == "GET /rare" {
			t.Fatalf("100 span altı operasyon bulgu olmamalı: %+v", f)
		}
	}
	found := false
	for _, op := range res.Data.Operations {
		if op.Name == "GET /rare" {
			found = op.Level == levelOK && strings.Contains(op.Reason, "düşük hacim")
		}
	}
	if !found {
		t.Fatal("düşük hacimli satır tabloda 'düşük hacim' gerekçesiyle kalmalı")
	}
}

func TestFunctionsSourceFallback(t *testing.T) {
	ctx := context.Background()
	t.Run("rollup var → rollup", func(t *testing.T) {
		r := baseSweepReaders()
		r.fnRollup = func(_ context.Context, _ string, from, _ time.Time) ([]chstore.RollupSeries, error) {
			pts := []chstore.RollupPoint{{TS: from.Unix(), Calls: 1000, Errors: 100, P99Ms: 800}}
			if from.Before(sweepTo.Add(-time.Hour)) { // önceki pencere
				pts = []chstore.RollupPoint{{TS: from.Unix(), Calls: 1000, Errors: 1, P99Ms: 200}}
			}
			return []chstore.RollupSeries{{Group: "F1", Points: pts}}, nil
		}
		res, _ := runServiceSweep(ctx, r, sweepReq(), nil)
		if res.Scanned.FunctionsSource != sweepFnSourceRollup || res.Scanned.Functions != 1 {
			t.Fatalf("kaynak %+v", res.Scanned)
		}
		if len(res.Data.Functions) != 1 || !res.Data.Functions[0].HasPrior || res.Data.Functions[0].Level != levelCrit {
			t.Fatalf("rollup satırı %+v", res.Data.Functions)
		}
	})
	t.Run("rollup tablosu yok → ham span kırılımı", func(t *testing.T) {
		res, _ := runServiceSweep(ctx, baseSweepReaders(), sweepReq(), nil)
		if res.Scanned.FunctionsSource != sweepFnSourceSpans || res.Scanned.Functions != 2 {
			t.Fatalf("kaynak %+v", res.Scanned)
		}
		s := sectionByName(res, sweepHopFunctions)
		if !strings.Contains(s.Note, "baseline ve p99 yok") {
			t.Fatalf("ham yol notu yok: %+v", s)
		}
		var f100 *SweepFinding
		for i := range res.Findings {
			if res.Findings[i].Name == "F100" {
				f100 = &res.Findings[i]
			}
		}
		if f100 == nil || f100.Level != levelCrit {
			t.Fatalf("%%10 hatalı fonksiyon (baseline yok, mutlak eşik) kritik olmalı: %+v", res.Findings)
		}
	})
	t.Run("ikisi de boş → none", func(t *testing.T) {
		r := baseSweepReaders()
		r.fnSpans = func(context.Context, string, time.Time, time.Time) ([]chstore.BusinessSlice, error) { return nil, nil }
		res, _ := runServiceSweep(ctx, r, sweepReq(), nil)
		s := sectionByName(res, sweepHopFunctions)
		if res.Scanned.FunctionsSource != sweepFnSourceNone || s.State != hopEmpty || !strings.Contains(s.Summary, "fonksiyon verisi yok") {
			t.Fatalf("none: %+v %+v", res.Scanned, s)
		}
	})
}

func TestSweepPartial(t *testing.T) {
	ctx := context.Background()
	r := baseSweepReaders()
	r.servicePods = func(context.Context, string, []string, time.Time, time.Time, ServicePodsOpts) (ServicePodsResult, error) {
		return ServicePodsResult{}, errors.New("thanos: connection refused")
	}
	r.logs = &syncLogStore{err: fmt.Errorf("es: %w", logstore.ErrBackendSlow)}
	r.runtime = fakeRuntime{err: errors.New("vm down")}
	res, err := runServiceSweep(ctx, r, sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || !res.Partial {
		t.Fatalf("ok=%v partial=%v", res.OK, res.Partial)
	}
	if s := sectionByName(res, sweepHopPods); s.State != hopError {
		t.Fatalf("pods %+v", s)
	}
	if s := sectionByName(res, sweepHopJVM); s.State != hopError {
		t.Fatalf("jvm %+v", s)
	}
	// ops/functions bulguları yine de sıralanır; logları degraded (okunamadı ≠ yok).
	if len(res.Findings) == 0 {
		t.Fatal("bulgu kalmadı — zincir koptu")
	}
	degraded := false
	for _, f := range res.Findings {
		if f.Logs != nil && f.Logs.State == hopDegraded && strings.Contains(f.Logs.Summary, "okunamadı ≠ log yok") {
			degraded = true
		}
	}
	if !degraded {
		t.Fatalf("degraded log notu yok: %+v", res.Findings)
	}
	if len(res.Sections) < 7 {
		t.Fatalf("bölüm eksik: %d", len(res.Sections))
	}
}

func TestSweepFindingsAndLogs(t *testing.T) {
	ls := &syncLogStore{page: &logstore.Page{Logs: []*logstore.LogRecord{{Severity: 17, Body: "OutOfMemoryError: Java heap space", ServiceName: "checkout", Timestamp: sweepTo.UnixNano()}}}}
	r := baseSweepReaders()
	r.logs = ls
	res, err := runServiceSweep(context.Background(), r, sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Üçü de kritik; etki: POST /pay (pay %37.5 × (8.5 puan + 5×3.5)) > F100 > OOM'lu pod (3).
	if len(res.Findings) != 3 || res.Findings[0].Name != "POST /pay" || res.Findings[2].Name != "checkout-2" {
		t.Fatalf("sıra: %+v", res.Findings)
	}
	if res.Verdict.Label != "Sorunlu" || !strings.Contains(res.Verdict.Next, "1 numaralı") {
		t.Fatalf("hüküm %+v", res.Verdict)
	}
	f := res.Findings[2]
	if f.Kind != sweepKindPod || f.Logs == nil || f.Logs.State != hopOK || len(f.Logs.Rows) != 1 || len(f.EvidenceRefs) != 2 {
		t.Fatalf("pod bulgusunun logları/kanıtı: %+v", f)
	}
	if len(res.Data.FindingLogs) != 3 || res.Data.FindingLogs[2].Name != "checkout-2" {
		t.Fatalf("JSON log satırları data.findingLogs'ta: %+v", res.Data.FindingLogs)
	}
	podFilterSeen, traceFilterSeen := false, false
	ls.mu.Lock()
	for _, lf := range ls.filters {
		if lf.Pod == "checkout-2" && lf.SeverityMin == qlOTelErrorFloor {
			podFilterSeen = true
		}
		if lf.TraceID != "" {
			traceFilterSeen = true
		}
	}
	ls.mu.Unlock()
	if !podFilterSeen || !traceFilterSeen {
		t.Fatalf("pod süzgeçli ve trace'li log okumaları bekleniyordu: %+v", ls.filters)
	}
	if res.Scanned.Pods != 2 || res.Scanned.Operations != 3 || res.Scanned.Clusters != 1 {
		t.Fatalf("scanned %+v", res.Scanned)
	}
}

func TestSweepPhase2Budget(t *testing.T) {
	if sweepPhase2Deadline(500*time.Millisecond) != 0 || sweepPhase2Deadline(3*time.Second) != 2*time.Second {
		t.Fatal("evre 2 son tarihi = kalan − 1 s")
	}
	// Bütçe bitmiş: evre 2 hiçbir şey okumaz, bulguların logları "skipped".
	ls := &syncLogStore{page: &logstore.Page{}}
	r := baseSweepReaders()
	r.logs = ls
	findings := []SweepFinding{{Kind: sweepKindPod, Name: "p"}, {Kind: sweepKindOperation, Name: "op"}}
	p := sweepPhase2(context.Background(), r, sweepReq(), findings, nil, time.Now().Add(200*time.Millisecond))
	if len(ls.filters) != 0 {
		t.Fatalf("bütçe yokken log okundu: %d", len(ls.filters))
	}
	for i := range findings {
		if p.logs[i] == nil || p.logs[i].State != hopSkipped || !strings.Contains(p.logs[i].Summary, "süre bütçesi") {
			t.Fatalf("bulgu %d: %+v", i, p.logs[i])
		}
	}
	if len(p.outcomes) != 1 || p.outcomes[0].section.State != hopSkipped {
		t.Fatalf("log bölümü %+v", p.outcomes)
	}
}

func TestSweepKeyHashesAllInputs(t *testing.T) {
	base := sweepReq()
	base.Env = "prod"
	cl := []ClusterRef{{ID: "1", Name: "c", SpanValues: []string{"x"}}}
	k0 := sweepCacheKey(base, cl, chstore.DefaultRuntimeAlerts())
	mut := []func(*SweepRequest, *[]ClusterRef, *chstore.RuntimeAlertConfig){
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Service = "b" },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Env = "uat" },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Window = "6h" },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) {
			r.From = r.From.Add(-time.Minute)
		},
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.To = r.To.Add(time.Minute) },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Deep = true },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.TopN = 7 },
		func(r *SweepRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.SkipPatterns = true },
		func(_ *SweepRequest, c *[]ClusterRef, _ *chstore.RuntimeAlertConfig) {
			*c = []ClusterRef{{ID: "1", Name: "c", SpanValues: []string{"y"}}}
		},
		func(_ *SweepRequest, c *[]ClusterRef, _ *chstore.RuntimeAlertConfig) {
			*c = []ClusterRef{{ID: "2", Name: "c", SpanValues: []string{"x"}}}
		},
		func(_ *SweepRequest, _ *[]ClusterRef, a *chstore.RuntimeAlertConfig) { a.GCPauseWarnMs = 999 },
	}
	for i, m := range mut {
		r, c, a := base, append([]ClusterRef(nil), cl...), chstore.DefaultRuntimeAlerts()
		m(&r, &c, &a)
		if sweepCacheKey(r, c, a) == k0 {
			t.Errorf("girdi %d anahtara girmiyor", i)
		}
	}
	// 30 s kovası: aynı kova içi kayma aynı anahtar.
	r := base
	r.From, r.To = r.From.Add(5*time.Second), r.To.Add(5*time.Second)
	if sweepCacheKey(r, cl, chstore.DefaultRuntimeAlerts()) != k0 {
		t.Error("30 s kova içi kayma anahtarı değiştirmemeli")
	}
	// Küme sırası anahtarı değiştirmez (sıralı + FNV).
	two := []ClusterRef{{ID: "1", Name: "a"}, {ID: "2", Name: "b"}}
	rev := []ClusterRef{two[1], two[0]}
	if sweepCacheKey(base, two, chstore.DefaultRuntimeAlerts()) != sweepCacheKey(base, rev, chstore.DefaultRuntimeAlerts()) {
		t.Error("cluster sırası anahtara sızıyor")
	}
	if !strings.HasPrefix(k0, "sweep:v1:") {
		t.Fatal(k0)
	}
}

func TestSweepFieldOrder(t *testing.T) {
	res, err := runServiceSweep(context.Background(), baseSweepReaders(), sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	s := string(b)
	order := []string{`"tool"`, `"ok"`, `"partial"`, `"verdict"`, `"scanned"`, `"findings"`, `"sections"`, `"request"`, `"evidence"`, `"evidences"`, `"data"`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k+":")
		if i < 0 || i < last {
			t.Fatalf("alan sırası bozuk: %s @%d (önceki @%d)", k, i, last)
		}
		last = i
	}
	// Sohbet sonucu 6000 rune'da baştan keser: hüküm + bulgular + bölümler +
	// istek kanıt listesinden (uzun sorgu metinleri) ve veriden ÖNCE sığmalı;
	// kesilen kuyruk kanıt/veri olur (çipler ham sonuçtan, kesilmeden kurulur).
	if ei := strings.Index(s, `"evidence":`); len([]rune(s[:ei])) > 6000 {
		t.Fatalf("kanıt öncesi zarf %d rune", len([]rune(s[:ei])))
	}
	if len(res.Evidences) > compositeEvidenceMax || len(res.Data.Blocks) > sweepMaxBlocks {
		t.Fatalf("tavan: %d kanıt, %d blok", len(res.Evidences), len(res.Data.Blocks))
	}
}

// TestSweepEvidenceListAndBlocks — bileşik zarf: bölüm başına çip + href;
// bloklar aynı bölüm kanıtına bağlı.
func TestSweepEvidenceListAndBlocks(t *testing.T) {
	res, err := runServiceSweep(context.Background(), baseSweepReaders(), sweepReq(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	evs, ok := ToolResultEvidenceList(ServiceHealthSweepName, string(b))
	if !ok || len(evs) < 4 {
		t.Fatalf("kanıt listesi %d", len(evs))
	}
	hops := map[string]ChatEvidence{}
	for i := range evs {
		evs[i].Ref = fmt.Sprintf("ev3.%d", i)
		hops[evs[i].Hop] = evs[i]
	}
	if hops[sweepHopRED].Href == "" || !strings.Contains(hops[sweepHopPods].Href, "/pod") {
		t.Fatalf("href'ler: red=%q pods=%q", hops[sweepHopRED].Href, hops[sweepHopPods].Href)
	}
	bl := ToolResultBlocks(ServiceHealthSweepName, string(b), evs)
	if len(bl) == 0 || len(bl) > compositeBlockMax {
		t.Fatalf("blok %d", len(bl))
	}
	if p := bl[0].Payload.(map[string]any); p["evidenceRef"] != hops[sweepHopRED].Ref {
		t.Fatalf("özet tablosu RED kanıtına bağlı olmalı: %v", p["evidenceRef"])
	}
}

func TestSweepBadArgsAndProgress(t *testing.T) {
	res, err := runServiceSweep(context.Background(), baseSweepReaders(), SweepRequest{}, nil)
	if err != nil || res.Error == nil || res.Error.Code != cosreErrBadArgs || res.Error.Need[0] != "service" {
		t.Fatalf("servissiz çağrı bad_args olmalı: %+v %v", res.Error, err)
	}
	var labels []string // kilitsiz: koordinatör dışı çağrı -race altında yakalanır
	ctx := WithProgress(context.Background(), func(phase, label string) { labels = append(labels, phase+"|"+label) })
	if _, err := runServiceSweep(ctx, baseSweepReaders(), sweepReq(), nil); err != nil {
		t.Fatal(err)
	}
	if len(labels) < 3 || !strings.HasPrefix(labels[0], "Sağlık|taranıyor") {
		t.Fatalf("ilerleme: %v", labels)
	}
	src, err := os.ReadFile("service_sweep.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, w := range []struct{ start, end string }{
		{"go func(i int, j job, dl time.Duration) {", "}(i, j, dl)"},
		{"go func(t task) {", "}(t)"},
	} {
		gi := strings.Index(body, w.start)
		ge := strings.Index(body[gi:], w.end)
		if gi < 0 || ge < 0 || strings.Contains(body[gi:gi+ge], "reportProgress(") {
			t.Fatalf("işçi goroutine ilerleme yayınlıyor (%s)", w.start)
		}
	}
}

func TestSweepMemoSkipsPartial(t *testing.T) {
	var stored []bool
	d := Deps{Memo: func(ctx context.Context, _ string, _ time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
		v, store, err := fn(ctx)
		stored = append(stored, store)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}}
	// Store yok → bölümler skipped (failed) → kısmi → saklanmaz.
	out, err := serviceHealthSweepTool(d).Handler(context.Background(), json.RawMessage(`{"service":"checkout"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out.(json.RawMessage); !ok {
		t.Fatalf("memo yolu ham gövde döndürmeli: %T", out)
	}
	if len(stored) != 1 || stored[0] {
		t.Fatalf("kısmi sonuç saklandı: %v", stored)
	}
}

func TestParseSweepRequest(t *testing.T) {
	req, _, w := parseSweepRequest(json.RawMessage(`{"Service":"checkout","deep":true,"top_n":"4","env":"prod"}`))
	if req.Service != "checkout" || !req.Deep || req.TopN != 4 || req.Env != "prod" || len(w) != 0 {
		t.Fatalf("%+v %v", req, w)
	}
	req, _, _ = parseSweepRequest(json.RawMessage(`{"service":"x","mode":"derin"}`))
	if !req.Deep {
		t.Fatal("mode=derin → deep")
	}
}

func TestSweepMappedClusters(t *testing.T) {
	all := []ClusterRef{{ID: "1", Name: "a", SpanValues: []string{"ocp-a"}}, {ID: "2", Name: "b"}, {ID: "3", Name: "c"}, {ID: "4", Name: "d"}, {ID: "5", Name: "e"}}
	got := sweepMappedClusters([]string{"OCP-A", "b", "zzz", "c", "d", "e"}, all)
	if len(got) != sweepMappedMax || got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("eşleme %+v", got)
	}
	if len(sweepMappedClusters([]string{"zzz"}, all)) != 0 {
		t.Fatal("eşlenmeyen değer cluster üretmemeli")
	}
}
