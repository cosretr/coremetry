package mcptools

// v0.10.1275 — CoSRE adım 6: sinyaller arası pivot (trace → pod → JVM → log) + playbook.
//
// pivot_from_trace tek deterministik bileşik çağrı: trace seçimi (saf),
// pod kümesi (saf), halka süresi (saf), zincirin kopmaması (okuma arızası
// halka durumuna çevrilir), JVM'in trace pod'larına süzülmesi, çok-kanıtlı
// zarfın href'leri / blok bağları ve zarfın alan sırası (veri EN SONDA).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/thanos"
)

const pivotTID = "0123456789abcdef0123456789abcdef"

func TestPickPivotTrace(t *testing.T) {
	rows := []chstore.TraceRow{{TraceID: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}, {TraceID: "b"}}
	for _, tc := range []struct {
		name     string
		req      PivotRequest
		rows     []chstore.TraceRow
		wantID   string
		wantBy   string
		wantCode string
	}{
		{"trace_id kazanır", PivotRequest{TraceID: pivotTID, Service: "x", Pick: "slowest"}, rows, pivotTID, "trace_id", ""},
		{"servis → ilk aday (küçük harf)", PivotRequest{Service: "x", Pick: "slowest"}, rows, strings.Repeat("a", 32), "slowest", ""},
		{"servis + hatalı seçimi", PivotRequest{Service: "x", Pick: "slowest_error"}, rows, strings.Repeat("a", 32), "slowest_error", ""},
		{"servis, aday yok → boş (veri yok)", PivotRequest{Service: "x", Pick: "slowest"}, nil, "", "slowest", ""},
		{"ikisi de yok → bad_args", PivotRequest{Pick: "slowest"}, rows, "", "", cosreErrBadArgs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, by, cerr := pickPivotTrace(tc.req, tc.rows)
			if id != tc.wantID || by != tc.wantBy {
				t.Fatalf("(%q,%q) beklenen (%q,%q)", id, by, tc.wantID, tc.wantBy)
			}
			if tc.wantCode == "" && cerr != nil || tc.wantCode != "" && (cerr == nil || cerr.Code != tc.wantCode) {
				t.Fatalf("hata %+v, beklenen %q", cerr, tc.wantCode)
			}
			if cerr != nil && strings.Join(cerr.Need, ",") != "trace_id,service" {
				t.Fatalf("need %v", cerr.Need)
			}
		})
	}
}

func TestParsePivotRequestTolerant(t *testing.T) {
	req, _, w := parsePivotRequest(json.RawMessage(`{"traceId":"` + strings.ToUpper(pivotTID) + `","Service":"svc","pick":"hata"}`))
	if req.TraceID != pivotTID || req.Service != "svc" || req.Pick != "slowest_error" || len(w) != 0 {
		t.Fatalf("hoşgörülü çözüm: %+v %v", req, w)
	}
	req, _, w = parsePivotRequest(json.RawMessage(`{"trace_id":"nothex","pick":"zzz"}`))
	if req.TraceID != "" || req.Pick != "slowest" || len(w) != 2 {
		t.Fatalf("bozuk argüman uyarısız düştü: %+v %v", req, w)
	}
}

func TestHopDeadline(t *testing.T) {
	for _, tc := range []struct{ remaining, cap, want time.Duration }{
		{15 * time.Second, 6 * time.Second, 6 * time.Second},
		{4 * time.Second, 6 * time.Second, 3500 * time.Millisecond},
		{500 * time.Millisecond, 6 * time.Second, 0},
		{-time.Second, 6 * time.Second, 0},
	} {
		if got := hopDeadline(tc.remaining, tc.cap); got != tc.want {
			t.Errorf("hopDeadline(%v,%v)=%v, beklenen %v", tc.remaining, tc.cap, got, tc.want)
		}
	}
}

// ── fikstürler ───────────────────────────────────────────────────────────

func pivotSpan(id, parent, svc, name string, startMs, durMs int64, cluster, ns, pod string, errSpan bool) chstore.SpanRow {
	res := map[string]string{}
	if cluster != "" {
		res["k8s.cluster.name"] = cluster
	}
	if ns != "" {
		res["k8s.namespace.name"] = ns
	}
	if pod != "" {
		res["k8s.pod.name"] = pod
	}
	st := "ok"
	if errSpan {
		st = "error"
	}
	base := int64(1_760_000_000_000) // ms
	return chstore.SpanRow{TraceID: pivotTID, SpanID: id, ParentSpanID: parent, ServiceName: svc, Name: name,
		StartTime: (base + startMs) * 1e6, EndTime: (base + startMs + durMs) * 1e6, StatusCode: st,
		ResourceAttributes: res, Attributes: map[string]string{"FUNCTION_CODE": "BQRZ001"}}
}

// pivotSpans — gateway → checkout → payment; checkout 2 cluster'da değil, tek cluster.
func pivotSpans(withError bool) []chstore.SpanRow {
	return []chstore.SpanRow{
		pivotSpan("0000000000000001", "", "gateway", "GET /buy", 0, 1000, "c1", "edge", "gw-1", false),
		pivotSpan("0000000000000002", "0000000000000001", "checkout", "checkout.run", 10, 900, "c1", "shop", "co-1", false),
		pivotSpan("0000000000000003", "0000000000000002", "payment", "pay.charge", 20, 850, "c1", "shop", "pay-1", withError),
	}
}

type fakeTracePods struct {
	err   error
	calls []string
	hop   func(cv string, refs []thanos.TracePodRef) TracePodHop
}

func (f *fakeTracePods) TracePods(_ context.Context, cv string, refs []thanos.TracePodRef, _, _ time.Time) (TracePodHop, error) {
	f.calls = append(f.calls, cv)
	if f.err != nil {
		return TracePodHop{}, f.err
	}
	if f.hop != nil {
		return f.hop(cv, refs), nil
	}
	return TracePodHop{Thanos: true, ClusterValue: cv}, nil
}

type fakeRuntime struct {
	heap, gc []chstore.CapacitySample
	err      error
}

func (f fakeRuntime) JVMHeapPodUsage(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return f.heap, f.err
}
func (f fakeRuntime) JVMGCPodPause(context.Context, time.Time, time.Time) ([]chstore.CapacitySample, error) {
	return f.gc, f.err
}

func f64(v float64) *float64 { return &v }

func basePivotReaders(spans []chstore.SpanRow) pivotReaders {
	return pivotReaders{
		fetchTrace: func(context.Context, string) (pivotTraceFetch, error) { return pivotTraceFetch{Spans: spans}, nil },
		runtimeAlerts: func(context.Context) chstore.RuntimeAlertConfig {
			return chstore.DefaultRuntimeAlerts()
		},
		runtimeSource: evSourceCH,
		opSummary: func(context.Context, string, time.Time, time.Time) ([]chstore.OperationSummary, error) {
			return nil, nil
		},
	}
}

func hopByName(r PivotResult, h string) pivotHop {
	for _, x := range r.Hops {
		if x.Hop == h {
			return x
		}
	}
	return pivotHop{}
}

func TestPivotPodRefs(t *testing.T) {
	var spans []chstore.SpanRow
	// 3 servis kritik yolda; checkout 6 pod (≤4 alınır), payment 2 cluster
	// dışı (c3 → 3. cluster düşer), gateway pod'unun cluster değeri yok.
	spans = append(spans, pivotSpan("0000000000000001", "", "gateway", "root", 0, 1000, "", "edge", "gw-1", false))
	for i := 0; i < 6; i++ {
		for k := 0; k <= i; k++ { // co-5 en çok span taşır
			spans = append(spans, pivotSpan(fmt.Sprintf("10000000000000%02d", i*10+k), "0000000000000001", "checkout", "run", 10, 900, "c1", "shop", fmt.Sprintf("co-%d", i), false))
		}
	}
	spans = append(spans,
		pivotSpan("2000000000000001", "1000000000000050", "payment", "charge", 20, 850, "c2", "shop", "pay-1", false),
		pivotSpan("2000000000000002", "1000000000000050", "payment", "charge", 20, 10, "c3", "shop", "pay-2", false),
		pivotSpan("3000000000000001", "1000000000000050", "ledger", "write", 20, 5, "c1", "shop", "led-1", false),
	)
	an := buildTraceToolAnalysis(spans, false)
	refs, dropped := pivotPodRefs(spans, an)
	total, services := 0, map[string]int{}
	for _, rs := range refs {
		total += len(rs)
		for _, r := range rs {
			services[r.Service]++
		}
	}
	if total > pivotMaxPods {
		t.Fatalf("toplam pod %d > %d", total, pivotMaxPods)
	}
	if services["checkout"] != pivotMaxPodsPerService {
		t.Fatalf("checkout pod sayısı %d, beklenen %d", services["checkout"], pivotMaxPodsPerService)
	}
	if len(services) > pivotMaxServices {
		t.Fatalf("servis sayısı %d > %d: %v", len(services), pivotMaxServices, services)
	}
	if refs["c1"][0].Pod != "co-5" && refs["c1"][1].Pod != "co-5" {
		t.Fatalf("en çok span taşıyan pod önce gelmeli: %+v", refs["c1"])
	}
	if len(refs[""]) != 1 || refs[""][0].Pod != "gw-1" {
		t.Fatalf("cluster değersiz pod \"\" anahtarında olmalı: %+v", refs[""])
	}
	nonEmpty := 0
	for cv := range refs {
		if cv != "" {
			nonEmpty++
		}
	}
	if nonEmpty > pivotMaxClusters {
		t.Fatalf("cluster sayısı %d > %d", nonEmpty, pivotMaxClusters)
	}
	_ = dropped
}

func TestRunPivotPartial(t *testing.T) {
	ctx := context.Background()
	t.Run("Thanos hatası → pods error + partial; log degraded; zincir sürer", func(t *testing.T) {
		r := basePivotReaders(pivotSpans(false))
		r.tracePods = &fakeTracePods{err: errors.New("CPU sorgusu: connection failed")}
		r.logs = &stubLogStore{searchErr: fmt.Errorf("es: %w", logstore.ErrBackendSlow)}
		r.runtime = fakeRuntime{}
		res, err := runTracePivot(ctx, r, PivotRequest{TraceID: pivotTID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK || !res.Partial {
			t.Fatalf("ok=%v partial=%v", res.OK, res.Partial)
		}
		if h := hopByName(res, pivotHopPods); h.State != hopError {
			t.Fatalf("pods halkası %+v", h)
		}
		if h := hopByName(res, pivotHopLogs); h.State != hopDegraded || !strings.Contains(h.Summary, "okunamadı ≠ log yok") {
			t.Fatalf("logs halkası %+v", h)
		}
		if len(res.Hops) != 5 {
			t.Fatalf("zincir koptu: %d halka", len(res.Hops))
		}
	})
	t.Run("stub aged_out → pods/JVM skipped, log denendi", func(t *testing.T) {
		r := basePivotReaders(nil)
		stub := chstore.TraceAggregateStub{RootService: "gateway", RootName: "GET /buy", SpanCount: 3, StartTimeNs: 1_760_000_000_000 * 1e6, EndTimeNs: 1_760_000_001_000 * 1e6}
		r.fetchTrace = func(context.Context, string) (pivotTraceFetch, error) {
			return pivotTraceFetch{Stub: &stub, StubReason: "aged_out"}, nil
		}
		tp := &fakeTracePods{}
		r.tracePods = tp
		r.runtime = fakeRuntime{}
		ls := &stubLogStore{page: &logstore.Page{}}
		r.logs = ls
		res, err := runTracePivot(ctx, r, PivotRequest{TraceID: pivotTID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range []string{pivotHopPods, pivotHopJVM, pivotHopOp} {
			if got := hopByName(res, h).State; got != hopSkipped {
				t.Errorf("%s = %s, beklenen skipped", h, got)
			}
		}
		if len(tp.calls) != 0 {
			t.Fatal("span'siz trace'te Thanos'a gidildi")
		}
		if ls.gotFilter.TraceID != pivotTID {
			t.Fatalf("log halkası denenmedi: %+v", ls.gotFilter)
		}
		if res.Trace == nil || res.Trace.StubReason != "aged_out" || res.Trace.Service != "gateway" {
			t.Fatalf("stub özeti trace'e yazılmadı: %+v", res.Trace)
		}
	})
	t.Run("hepsi boş → halka başına 'veri yok'", func(t *testing.T) {
		r := basePivotReaders(nil)
		r.fetchTrace = func(context.Context, string) (pivotTraceFetch, error) {
			return pivotTraceFetch{StubReason: "not_found"}, nil
		}
		r.logs = &stubLogStore{page: &logstore.Page{}}
		res, err := runTracePivot(ctx, r, PivotRequest{TraceID: pivotTID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.OK || !res.Empty || !strings.HasPrefix(res.Message, "veri yok") {
			t.Fatalf("boş zincir: ok=%v empty=%v msg=%q", res.OK, res.Empty, res.Message)
		}
		for _, h := range res.Hops {
			if h.Summary == "" {
				t.Errorf("%s halkasının notu yok", h.Hop)
			}
		}
		if !strings.HasPrefix(hopByName(res, pivotHopTrace).Summary, "veri yok") || !strings.HasPrefix(hopByName(res, pivotHopLogs).Summary, "veri yok") {
			t.Fatalf("boş halka 'veri yok' demedi: %+v", res.Hops)
		}
	})
	t.Run("servis seçimi, aday yok → veri yok + kanıt", func(t *testing.T) {
		r := basePivotReaders(nil)
		r.searchTraces = func(context.Context, chstore.TraceFilter) ([]chstore.TraceRow, uint64, bool, error) {
			return nil, 0, false, nil
		}
		now := time.Now().UTC()
		res, _ := runTracePivot(ctx, r, PivotRequest{Service: "checkout", Pick: "slowest", Window: "1h", From: now.Add(-time.Hour), To: now}, nil)
		if !res.Empty || res.Evidence == nil || len(res.Evidences) != 1 {
			t.Fatalf("adaysız servis: %+v", res)
		}
	})
	t.Run("ne trace ne servis → bad_args", func(t *testing.T) {
		res, _ := runTracePivot(ctx, basePivotReaders(nil), PivotRequest{}, nil)
		if res.OK || res.Error == nil || res.Error.Code != cosreErrBadArgs {
			t.Fatalf("bad_args beklenirdi: %+v", res)
		}
	})
}

func TestPivotJVMFiltersToTracePods(t *testing.T) {
	cfg := chstore.DefaultRuntimeAlerts()
	t.Run("classifyJVM", func(t *testing.T) {
		row := classifyJVM("s", "p", &chstore.CapacitySample{Usage: 90, Limit: 100, PostGC: 86}, nil, cfg)
		if row.Level != levelWarn || row.PostGCPct == nil || *row.PostGCPct != 86 {
			t.Fatalf("post-GC %%86 → warn: %+v", row)
		}
		row = classifyJVM("s", "p", &chstore.CapacitySample{Usage: 98, Limit: 100}, nil, cfg)
		if row.Level != levelCrit || row.PostGCPct != nil {
			t.Fatalf("ham %%98, post-GC yok → crit: %+v", row)
		}
		row = classifyJVM("s", "p", &chstore.CapacitySample{Usage: 98, Limit: 100, PostGC: 40}, nil, cfg)
		if row.Level != levelOK {
			t.Fatalf("testere dişi: ham yüksek ama post-GC düşük → ok: %+v", row)
		}
		row = classifyJVM("s", "p", nil, &chstore.CapacitySample{Usage: 3500}, cfg)
		if row.Level != levelCrit {
			t.Fatalf("GC 3500 ms → crit: %+v", row)
		}
	})
	t.Run("yalnız trace'in (servis, pod) çiftleri", func(t *testing.T) {
		r := basePivotReaders(pivotSpans(false))
		r.runtime = fakeRuntime{heap: []chstore.CapacitySample{
			{Instance: "payment", Subkey: "pay-1", Usage: 86, Limit: 100, PostGC: 86},
			{Instance: "payment", Subkey: "pay-9", Usage: 99, Limit: 100},   // trace dışı pod
			{Instance: "inventory", Subkey: "pay-1", Usage: 99, Limit: 100}, // başka servis
			{Instance: "CHECKOUT", Subkey: "co-1", Usage: 98, Limit: 100},   // harf farkı eşleşir
		}}
		res, err := runTracePivot(context.Background(), r, PivotRequest{TraceID: pivotTID}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Data.JVM) != 2 {
			t.Fatalf("JVM satırları trace pod'larına süzülmedi: %+v", res.Data.JVM)
		}
		levels := map[string]string{}
		for _, row := range res.Data.JVM {
			levels[row.Pod] = row.Level
		}
		if levels["pay-1"] != levelWarn || levels["co-1"] != levelCrit {
			t.Fatalf("seviyeler %v", levels)
		}
	})
	t.Run("OTel JVM boş → JMX notu", func(t *testing.T) {
		r := basePivotReaders(pivotSpans(false))
		r.runtime = fakeRuntime{}
		res, _ := runTracePivot(context.Background(), r, PivotRequest{TraceID: pivotTID}, nil)
		if h := hopByName(res, pivotHopJVM); h.State != hopEmpty || !strings.Contains(h.Summary, "JMX Thanos'ta olabilir") {
			t.Fatalf("JVM boş notu: %+v", h)
		}
	})
}

// pivotFullResult — tüm halkaları dolu bir sonuç (8 pod, 2 cluster).
func pivotFullResult(t *testing.T) PivotResult {
	t.Helper()
	var spans []chstore.SpanRow
	spans = append(spans, pivotSpan("0000000000000001", "", "gateway", "GET /buy", 0, 3000, "c1", "edge", "gw-1", false))
	for i := 0; i < 4; i++ {
		spans = append(spans, pivotSpan(fmt.Sprintf("10000000000000%02d", i), "0000000000000001", "checkout", "checkout.run", 10, 2900, "c1", "shop", fmt.Sprintf("checkout-7d9f8c-%05d", i), i == 0))
	}
	for i := 0; i < 4; i++ {
		spans = append(spans, pivotSpan(fmt.Sprintf("20000000000000%02d", i), "1000000000000000", "payment", "pay.charge", 20, 2800, "c2", "pay", fmt.Sprintf("payment-5f6d7e-%05d", i), false))
	}
	r := basePivotReaders(spans)
	r.tracePods = &fakeTracePods{hop: func(cv string, refs []thanos.TracePodRef) TracePodHop {
		h := TracePodHop{Thanos: true, Mapped: true, ClusterValue: cv, ClusterID: "id-" + cv, ClusterName: "name-" + cv,
			Query: thanos.TracePodQueryText(refs, ""), Start: 1_760_000_000, Step: 60, Points: 3, Instant: "ok"}
		for _, ref := range refs {
			h.Pods = append(h.Pods, thanos.TracePodSeries{Namespace: ref.Namespace, Pod: ref.Pod, State: "ok",
				CPU: []*float64{f64(0.2), nil, f64(0.4)}, Mem: []*float64{f64(900 << 20), f64(950 << 20), f64(960 << 20)},
				CPULimit: f64(1), MemLimit: f64(1 << 30), Phase: "Running", LastTermReason: "OOMKilled"})
		}
		return h
	}}
	r.runtime = fakeRuntime{heap: []chstore.CapacitySample{{Instance: "checkout", Subkey: "checkout-7d9f8c-00000", Usage: 95, Limit: 100, PostGC: 91}}}
	recs := []*logstore.LogRecord{}
	for i := 0; i < 20; i++ {
		recs = append(recs, &logstore.LogRecord{Timestamp: int64(1_760_000_000_000+i) * 1e6, Severity: uint8(9 + i%10), ServiceName: "checkout",
			TraceID: pivotTID, Body: strings.Repeat("ödeme servisi yanıt vermedi ", 8)})
	}
	r.logs = &stubLogStore{page: &logstore.Page{Logs: recs}}
	r.opSummary = func(context.Context, string, time.Time, time.Time) ([]chstore.OperationSummary, error) {
		return []chstore.OperationSummary{{Name: "pay.charge", P99Ms: 400}, {Name: "checkout.run", P99Ms: 400}}, nil
	}
	res, err := runTracePivot(context.Background(), r, PivotRequest{TraceID: pivotTID, Env: "prod"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestPivotFieldOrder(t *testing.T) {
	res := pivotFullResult(t)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	order := []string{`"tool":`, `"ok":`, `"partial":`, `"request":`, `"trace":`, `"hops":`, `"findings":`, `"playbook":`, `"evidence":`, `"evidences":`, `"data":`}
	last := -1
	for _, k := range order {
		i := strings.Index(s, k)
		if i < 0 || i < last {
			t.Fatalf("alan sırası bozuk: %s (indeks %d, önceki %d)", k, i, last)
		}
		last = i
	}
	if !strings.HasSuffix(strings.TrimSpace(s), "}}") || strings.LastIndex(s, `"data":`) != last {
		t.Fatal("data en sonda değil")
	}
	// Durum öneki (kanıtlardan önceki her şey: halkalar, bulgular, playbook)
	// sohbetin 6000 rune'luk model kırpmasına sığmalı; kanıtlar çip içindir ve
	// sunucu onları kırpılmamış sonuçtan okur (attachStepEvidenceList kırpmadan önce).
	status := s[:strings.Index(s, `"evidence":`)]
	if n := utf8.RuneCountInString(status); n > 6000 {
		t.Fatalf("durum öneki %d rune — sohbetin 6000 rune'luk kırpması durumu keser", n)
	}
	if len(res.Data.Pods) != 8 {
		t.Fatalf("8 pod beklendi, %d", len(res.Data.Pods))
	}
	if len(res.Findings) > pivotFindingsMax || !res.FindingsHasMore {
		t.Fatalf("bulgular tavanlı ve has_more işaretli olmalı: %d %v", len(res.Findings), res.FindingsHasMore)
	}
	if res.Findings[0].Level != levelCrit {
		t.Fatalf("ilk bulgu kritik olmalı: %+v", res.Findings[0])
	}
	if res.Request["env_applied"] != false {
		t.Fatalf("trace_id verilince env uygulanmadı künyesi: %+v", res.Request)
	}
	if len(res.Data.Logs) != pivotLogKeep || res.Data.Logs[0].Severity == "INFO" {
		t.Fatalf("loglar şiddete göre ≤%d: %+v", pivotLogKeep, res.Data.Logs[0])
	}
	if len(res.Data.Blocks) > pivotMaxBlocks {
		t.Fatalf("blok tavanı: %d", len(res.Data.Blocks))
	}
	if res.Playbook == nil || res.Playbook.Symptom != "error" || len(res.Playbook.Checked) != len(res.Playbook.Families) {
		t.Fatalf("playbook: %+v", res.Playbook)
	}
	if h := hopByName(res, pivotHopOp); h.State != hopOK || !strings.Contains(h.Summary, "p99 400 ms") {
		t.Fatalf("op_baseline: %+v", h)
	}
}

func TestPivotEvidenceListHrefs(t *testing.T) {
	res := pivotFullResult(t)
	b, _ := json.Marshal(res)
	evs, ok := ToolResultEvidenceList(PivotFromTraceName, string(b))
	if !ok || len(evs) < 5 || len(evs) > compositeEvidenceMax {
		t.Fatalf("kanıt listesi %d", len(evs))
	}
	want := map[string]string{
		pivotHopTrace: "/trace?id=" + pivotTID,
		pivotHopPods:  "/pod?cluster=id-c1",
		pivotHopJVM:   "/service?",
		pivotHopLogs:  "/logs?",
		pivotHopOp:    "/traces?",
	}
	seen := map[string]bool{}
	for _, ev := range evs {
		if ev.Tool != PivotFromTraceName || ev.Hop == "" || ev.Source == "" {
			t.Fatalf("kalem eksik: %+v", ev)
		}
		if w, ok := want[ev.Hop]; ok && !seen[ev.Hop] {
			seen[ev.Hop] = true
			if !strings.HasPrefix(ev.Href, w) {
				t.Errorf("%s href %q, beklenen önek %q", ev.Hop, ev.Href, w)
			}
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("eksik halka kanıtı: %v", seen)
	}
	// Atomik query_* — liste tek kalem ve ToolResultEvidence ile bayt-aynı.
	atomic := `{"tool":"query_logs","ok":true,"request":{"service":"checkout","severity":"error"},"evidence":{"source":"Elasticsearch","query":"q","queryKind":"summary","window":{"fromMs":1760000000000,"toMs":1760003600000},"rows":2,"durationMs":5}}`
	one, ok1 := ToolResultEvidence(queryLogsName, atomic)
	list, ok2 := ToolResultEvidenceList(queryLogsName, atomic)
	a, _ := json.Marshal(one)
	l, _ := json.Marshal(list)
	if !ok1 || !ok2 || len(list) != 1 || "["+string(a)+"]" != string(l) {
		t.Fatalf("atomik kanıt değişti:\n%s\n%s", a, l)
	}
	if strings.Contains(string(a), `"hop"`) {
		t.Fatal("atomik kanıt hop alanı taşımamalı (omitempty)")
	}
	if IsCompositeEvidenceTool(queryLogsName) || !IsCompositeEvidenceTool(PivotFromTraceName) || !IsEvidenceTool(PivotFromTraceName) {
		t.Fatal("bileşik araç sınıflaması")
	}
}

func TestToolResultBlocksComposite(t *testing.T) {
	res := pivotFullResult(t)
	b, _ := json.Marshal(res)
	evs, _ := ToolResultEvidenceList(PivotFromTraceName, string(b))
	for k := range evs {
		evs[k].Ref = fmt.Sprintf("ev7.%d", k)
		if k == 0 {
			evs[k].Ref = "ev7"
		}
	}
	blks := ToolResultBlocks(PivotFromTraceName, string(b), evs)
	if len(blks) == 0 || len(blks) > compositeBlockMax {
		t.Fatalf("blok sayısı %d", len(blks))
	}
	refs := map[string]string{}
	for _, ev := range evs {
		if _, ok := refs[ev.Hop]; !ok {
			refs[ev.Hop] = ev.Ref
		}
	}
	types := map[string]bool{}
	for _, bl := range blks {
		p := bl.Payload.(map[string]any)
		types[bl.Type] = true
		ref, _ := p["evidenceRef"].(string)
		if ref == "" || !strings.HasPrefix(ref, "ev7") {
			t.Fatalf("blok ref'i yok: %+v", p)
		}
	}
	for _, typ := range []string{"trace_list", "table", "chart"} {
		if !types[typ] {
			t.Errorf("%s bloğu yok", typ)
		}
	}
	// Eşleşen kanıtı olmayan halkanın bloğu düşer.
	if got := ToolResultBlocks(PivotFromTraceName, string(b), evs[:1]); len(got) != 1 || got[0].Type != "trace_list" {
		t.Fatalf("bağsız bloklar düşmeli: %d", len(got))
	}
	// Tavan: 8 blok taşıyan zarf → 6.
	many := map[string]any{"tool": PivotFromTraceName, "ok": true, "data": map[string]any{"blocks": func() []any {
		var out []any
		for i := 0; i < 8; i++ {
			out = append(out, map[string]any{"hop": pivotHopTrace, "type": "table", "payload": map[string]any{"title": "x"}})
		}
		return out
	}()}}
	mb, _ := json.Marshal(many)
	if got := ToolResultBlocks(PivotFromTraceName, string(mb), []ChatEvidence{{Hop: pivotHopTrace, Ref: "ev1", Source: "ClickHouse"}}); len(got) != compositeBlockMax {
		t.Fatalf("tavan %d, beklenen %d", len(got), compositeBlockMax)
	}
}

func TestPivotProgressCoordinatorOnly(t *testing.T) {
	var labels []string // kilitsiz: koordinatör dışı çağrı -race altında yakalanır
	ctx := WithProgress(context.Background(), func(phase, label string) { labels = append(labels, phase+"|"+label) })
	r := basePivotReaders(pivotSpans(true))
	r.logs = &stubLogStore{page: &logstore.Page{}}
	r.runtime = fakeRuntime{}
	if _, err := runTracePivot(ctx, r, PivotRequest{TraceID: pivotTID}, nil); err != nil {
		t.Fatal(err)
	}
	if len(labels) < 3 || !strings.HasPrefix(labels[0], "Trace|") {
		t.Fatalf("ilerleme etiketleri: %v", labels)
	}
	// Bitmiş ctx → no-op.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	n := len(labels)
	reportProgress(cctx, "Trace", "x")
	if len(labels) != n {
		t.Fatal("iptal edilmiş ctx'te ilerleme yayınlandı")
	}
	src, err := os.ReadFile("pivot_trace.go")
	if err != nil {
		t.Fatal(err)
	}
	// İşçi goroutine'i (go func) reportProgress çağırmaz: tek çağrı yeri koordinatör.
	body := string(src)
	gi := strings.Index(body, "go func(i int, j job, dl time.Duration) {")
	ge := strings.Index(body[gi:], "}(i, j, dl)")
	if gi < 0 || ge < 0 || strings.Contains(body[gi:gi+ge], "reportProgress(") {
		t.Fatal("hop işçisi ilerleme yayınlıyor — emit tek goroutine sözleşmesi bozulur")
	}
}

func TestPivotCacheKeyHashesAllInputs(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	base := PivotRequest{Service: "a", Env: "prod", Operation: "op", Window: "1h", Pick: "slowest", From: now.Add(-time.Hour), To: now}
	cl := []ClusterRef{{ID: "1", Name: "c", SpanValues: []string{"x"}}}
	k0 := pivotCacheKey(base, cl, chstore.DefaultRuntimeAlerts())
	mut := []func(*PivotRequest, *[]ClusterRef, *chstore.RuntimeAlertConfig){
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Service = "b" },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Env = "uat" },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Operation = "op2" },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Pick = "slowest_error" },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.Window = "6h" },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.To = r.To.Add(time.Minute) },
		func(r *PivotRequest, _ *[]ClusterRef, _ *chstore.RuntimeAlertConfig) { r.TraceID = pivotTID },
		func(_ *PivotRequest, c *[]ClusterRef, _ *chstore.RuntimeAlertConfig) {
			*c = []ClusterRef{{ID: "1", Name: "c", SpanValues: []string{"y"}}}
		},
		func(_ *PivotRequest, _ *[]ClusterRef, a *chstore.RuntimeAlertConfig) { a.HeapPostGCWarnPct = 80 },
	}
	for i, m := range mut {
		r, c, a := base, append([]ClusterRef(nil), cl...), chstore.DefaultRuntimeAlerts()
		m(&r, &c, &a)
		if pivotCacheKey(r, c, a) == k0 {
			t.Errorf("girdi %d anahtara girmiyor", i)
		}
	}
	if !strings.HasPrefix(k0, "pivot:v1:") {
		t.Fatal(k0)
	}
}

func TestPivotMemoSkipsPartial(t *testing.T) {
	var stored []bool
	d := Deps{Memo: func(ctx context.Context, _ string, _ time.Duration, fn func(context.Context) (any, bool, error)) ([]byte, error) {
		v, store, err := fn(ctx)
		stored = append(stored, store)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}}
	// Store yok → trace halkası okunamaz → kısmi → saklanmaz.
	out, err := pivotFromTraceTool(d).Handler(context.Background(), json.RawMessage(`{"trace_id":"`+pivotTID+`"}`))
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

func TestRootCausePlaybook(t *testing.T) {
	fam := []string{"saturation", "operations"}
	called := 0
	gather := func() chstore.DeepEvidence {
		called++
		return chstore.DeepEvidence{Checked: []chstore.CheckedSignal{{Family: "saturation", Found: true, Records: 2}}}
	}
	stored := &chstore.DeepEvidence{Checked: []chstore.CheckedSignal{{Family: "logs"}}}
	if pb := buildRootCausePlaybook(fam, stored, true, gather); pb.Source != "stored" || called != 0 || pb.Checked[0].Family != "logs" {
		t.Fatalf("saklı iz kazanmalı: %+v", pb)
	}
	if pb := buildRootCausePlaybook(fam, nil, false, gather); pb.Source != "none" || called != 0 || pb.Note == "" {
		t.Fatalf("investigate yok → none + not: %+v", pb)
	}
	if pb := buildRootCausePlaybook(nil, nil, true, gather); pb.Source != "none" || called != 0 || pb.Families == nil {
		t.Fatalf("plansız → none: %+v", pb)
	}
	if pb := buildRootCausePlaybook(fam, &chstore.DeepEvidence{}, true, gather); pb.Source != "on_demand" || called != 1 || !pb.Checked[0].Found {
		t.Fatalf("canlı okuma: %+v", pb)
	}
	// Tek yazıcı kuralı: playbook yolu hipoteze YAZMAZ.
	for _, f := range []string{"tools.go", "pivot_trace.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "UpsertHypothesis") {
			t.Fatalf("%s UpsertHypothesis çağırıyor — derin kanıtı yalnız sentezleyici yazar", f)
		}
	}
	b, _ := os.ReadFile("../anomaly/investigation.go")
	if i := strings.Index(string(b), "func GatherDeepEvidenceOnDemand"); i < 0 || strings.Contains(string(b)[i:], "UpsertHypothesis") {
		t.Fatal("GatherDeepEvidenceOnDemand yazıyor ya da yok")
	}
}
