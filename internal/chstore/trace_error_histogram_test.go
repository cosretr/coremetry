package chstore

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1082 — Operator-reported (prod): "Error seçildiğinde histogram
// gelmiyor." /traces'te Errors + `function_code = KYC0001` (ya da
// `k8s.pod.name = …`) çipiyle liste dolu, şerit "0 SPANS · 0 ERROR SPANS".
// Şerit Errors'u metric-batch'te span düzeyinde `status = error` çipi olarak
// AND'liyordu (giriş kapsamında üstüne `kind IN (server, consumer)`); liste
// ise iki basamakta (çipe uyan hatalı span ↔ trace düzeyi) arıyor. Şerit artık
// listenin süzgecini ve kip kararını paylaşır (trace_error_histogram.go).
// Sorgular clickhouse-local'de sentetik spans tablosuyla koşturuldu (eski
// şerit 0, yeni şerit liste kadar).

var histFrom = time.Date(2026, 10, 3, 10, 2, 30, 0, time.UTC)

func histFilter(filters ...FilterExpr) TraceFilter {
	return TraceFilter{HasError: true, Filters: filters, From: histFrom, To: histFrom.Add(time.Hour)}
}

var (
	chipFunctionCode = FilterExpr{Key: "function_code", Op: "=", Values: []string{"KYC0001"}}
	chipPod          = FilterExpr{Key: "k8s.pod.name", Op: "=", Values: []string{"svc-a-7d9f-x2"}}
)

func TestTraceErrorHistogramEligible(t *testing.T) {
	withSvc := histFilter(chipFunctionCode)
	withSvc.Service = "svc-a"
	cases := []struct {
		name string
		f    TraceFilter
		want bool
	}{
		{"Errors + function_code çipi", histFilter(chipFunctionCode), true},
		{"Errors + k8s.pod.name çipi", histFilter(chipPod), true},
		{"servisli de", withSvc, true},
		{"Errors yok → metric-batch", TraceFilter{Filters: []FilterExpr{chipFunctionCode}}, false},
		// v0.10.1101 — çipsiz Errors da bu uçta (③ kapsam kipi).
		{"çip yok, yalnız Errors", TraceFilter{HasError: true}, true},
		{"çip yok, Errors + servis", TraceFilter{HasError: true, Service: "svc-a"}, true},
		{"çip yok, Errors + ortam + küme", TraceFilter{HasError: true, Env: "prod", Cluster: "c1"}, true},
		{"çip yok + Root → metric-batch (kök trace düzeyi)", TraceFilter{HasError: true, RootOnly: true}, false},
		{"çip yok + arama → metric-batch", TraceFilter{HasError: true, Search: "timeout"}, false},
		{"çip yok + süre → metric-batch", TraceFilter{HasError: true, MaxMs: 900}, false},
		{"çip yok + services → metric-batch", TraceFilter{HasError: true, RequireServices: []string{"a"}}, false},
		{"çip yok + trace id → metric-batch", TraceFilter{HasError: true, TraceID: "abc"}, false},
		{"çip yok, Errors yok → metric-batch", TraceFilter{Service: "svc-a"}, false},
		{"arama + çip → liste HAVING'de, metric-batch", func() TraceFilter { f := histFilter(chipFunctionCode); f.Search = "x"; return f }(), false},
		{"süre süzgeci → metric-batch", func() TraceFilter { f := histFilter(chipFunctionCode); f.MinMs = 5; return f }(), false},
		{"services → metric-batch", func() TraceFilter { f := histFilter(chipFunctionCode); f.RequireServices = []string{"a"}; return f }(), false},
		{"trace id → metric-batch", func() TraceFilter { f := histFilter(chipFunctionCode); f.TraceID = "abc"; return f }(), false},
		{"Root serbest (② kipinde adaylar üstünde doğrulanır)", func() TraceFilter { f := histFilter(chipFunctionCode); f.RootOnly = true; return f }(), true},
	}
	for _, c := range cases {
		if got := TraceErrorHistogramEligible(c.f); got != c.want {
			t.Errorf("%s: %v, beklenen %v", c.name, got, c.want)
		}
	}
}

// Altın SQL — span kipi listenin probuyla AYNI WHERE'i taşır (tek yardımcı:
// traceErrBothWhere). Terfi haritası / kvh test ortamında boş → dizi yolu.
func TestTraceErrHistSpanSQLGolden(t *testing.T) {
	// Paket-genel durum (terfi haritası, kvh hazır bayrağı) başka testlerce
	// değişebilir; altın metin dizi yolunu sabitler.
	prevPM, prevKVH := promotedColsPtr.Load(), attrIndexReady.Load()
	empty := map[string]string{}
	promotedColsPtr.Store(&empty)
	attrIndexReady.Store(false)
	t.Cleanup(func() { promotedColsPtr.Store(prevPM); attrIndexReady.Store(prevKVH) })
	f := histFilter(chipFunctionCode)
	f.Service, f.Env = "svc-a", "prod"
	wc := traceErrBothWhere(f, "")
	wantWhere := "WHERE time >= ? AND time <= ? AND service_name = ? AND deploy_env = ? AND attr_values[indexOf(attr_keys, ?)] = ? AND status_code = 'error'"
	if wc.sql() != wantWhere {
		t.Fatalf("WHERE:\n got %s\nwant %s", wc.sql(), wantWhere)
	}
	// Pencere başı listenin 5 dk hizasıyla (buildGetTracesWhere) — şerit
	// eskiden çıplak from kullanıyordu; hizalı dilimdeki satırlar listede
	// görünüp şeritte yoktu.
	wantArgs := []any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour), "svc-a", "prod", "function_code", "KYC0001"}
	if !reflect.DeepEqual(wc.args, wantArgs) {
		t.Fatalf("args:\n got %#v\nwant %#v", wc.args, wantArgs)
	}
	got := traceErrHistSpanSQL(wc.sql(), 0.95)
	want := `
		SELECT intDiv(greatest(toUnixTimestamp64Nano(time), ?), ?) * ? AS bucket,
		       count() AS n,
		       toFloat64(quantileTDigest(0.95)(duration / 1e6)) AS rt
		FROM spans ` + wantWhere + `
		GROUP BY bucket
		ORDER BY bucket
		LIMIT 50000
		SETTINGS max_execution_time = 25`
	if got != want {
		t.Fatalf("span SQL:\n%s\nwant\n%s", got, want)
	}
}

func TestTraceErrHistTraceSQLGolden(t *testing.T) {
	got := traceErrHistTraceSQL(2, "WHERE time >= ? AND time <= ?")
	want := `
		SELECT trace_id,
		       toUnixTimestamp64Nano(min(time)) AS t0,
		       (max(toUnixTimestamp64Nano(time) + duration) -
		        toUnixTimestamp64Nano(min(time))) / 1e6 AS dur_ms
		FROM spans PREWHERE trace_id IN (?,?) WHERE time >= ? AND time <= ?
		GROUP BY trace_id
		LIMIT ?
		SETTINGS max_execution_time = 25, optimize_move_to_prewhere = 0`
	if got != want {
		t.Fatalf("trace SQL:\n%s\nwant\n%s", got, want)
	}
	// Liste satırının trace_start / dur_ms ifadeleriyle aynı (kovalanan
	// başlangıç = satırdaki başlangıç).
	list := buildGetTracesListSQL("WHERE 1", "", "trace_start", "DESC")
	for _, w := range []string{"min(time)", "(max(toUnixTimestamp64Nano(time) + duration) -\n\t\t        toUnixTimestamp64Nano(min(time))) / 1e6"} {
		if !strings.Contains(list, w) || !strings.Contains(got, w) {
			t.Errorf("liste ve şerit %q ifadesini paylaşmalı", w)
		}
	}
}

// Ayrışma tablosu — eski şerit (metric-batch: çipler + `status = error` çipi,
// giriş kapsamında + kind) ile listenin ① WHERE'i. Yeni şerit HER durumda
// listenin WHERE'ini birebir (SQL + arg) taşır.
func TestTraceErrHistDivergenceTable(t *testing.T) {
	entryKind := FilterExpr{Key: "kind", Op: "IN", Values: []string{"server", "consumer"}}
	statusChip := FilterExpr{Key: "status", Op: "=", Values: []string{"error"}}
	cases := []struct {
		name string
		chip FilterExpr
		// eski şeridin gönderdiği ek kapsam çipi (stripScope 'entry' ise kind)
		oldEntryKind bool
		// eski şeridin neden sıfır saydığı
		why string
	}{
		{"function_code (spans kapsamı)", chipFunctionCode, false,
			"② kipinde (çipli span hatasız, hata başka span'de) çip ∧ hata aynı span'de: kesin 0"},
		{"k8s.pod.name (giriş kapsamı)", chipPod, true,
			"hata istemci/iç span'de: pod ∧ kind∈{server,consumer} ∧ hata aynı span'de: 0"},
	}
	for _, c := range cases {
		f := histFilter(c.chip)
		listProbe := func() whereClause { _, _, both := traceErrWheres(f, ""); return both }()
		strip := traceErrBothWhere(f, "")
		if strip.sql() != listProbe.sql() || !reflect.DeepEqual(strip.args, listProbe.args) {
			t.Errorf("%s: şerit listenin WHERE'inden ayrıştı:\n%s %v\n%s %v", c.name, strip.sql(), strip.args, listProbe.sql(), listProbe.args)
		}
		if strings.Contains(strip.sql(), "kind IN") {
			t.Errorf("%s: yeni şerit giriş kind kısıtı taşımamalı (liste taşımıyor)", c.name)
		}
		// Eski şekil: metric-batch WHERE'i. Hata çip olarak (bind 'error'),
		// pencere çıplak from, giriş kapsamında kind.
		old := []FilterExpr{c.chip}
		if c.oldEntryKind {
			old = append(old, entryKind)
		}
		old = append(old, statusChip)
		ow := spanMetricBatchWhere(SpanMetricBatchFilter{Filters: old, From: f.From, To: f.To, HasError: true}, 0, 0)
		if ow.sql() == strip.sql() {
			t.Errorf("%s: eski şekil listeyle aynı olmamalıydı (%s)", c.name, c.why)
		}
		if got := strings.Contains(ow.sql(), "kind IN"); got != c.oldEntryKind {
			t.Errorf("%s: eski şeritte kind kısıtı %v, beklenen %v", c.name, got, c.oldEntryKind)
		}
		if ow.args[0] != f.From {
			t.Errorf("%s: eski şerit çıplak from ile başlıyordu: %v", c.name, ow.args[0])
		}
	}
}

func TestBucketTraceStarts(t *testing.T) {
	from := histFrom.UnixNano()
	step := 300
	stepNs := int64(step) * int64(time.Second)
	b0 := (from / stepNs) * stepNs
	cases := []struct {
		name      string
		rows      []traceStartRow
		wantTimes []int64
		wantCount []float64
		wantRT    []float64
	}{
		{"boş → seri yok", nil, nil, nil, nil},
		{"tek trace", []traceStartRow{{"t1", from + 10, 40}}, []int64{b0}, []float64{1}, []float64{40}},
		{"pencere başından önce başlayan ilk kovaya katlanır (toplam liste kadar)",
			[]traceStartRow{{"t0", from - 4*int64(time.Minute), 10}, {"t1", from + 1, 30}},
			[]int64{b0}, []float64{2}, []float64{20}},
		{"iki kova, sıralı; medyan doğrusal",
			[]traceStartRow{{"a", b0 + stepNs + 5, 100}, {"b", from, 10}, {"c", b0 + stepNs + 9, 300}, {"d", from + 2, 20}, {"e", from + 3, 30}},
			[]int64{b0, b0 + stepNs}, []float64{3, 2}, []float64{20, 200}},
	}
	for _, c := range cases {
		cnt, rt := bucketTraceStarts(c.rows, from, step, 0.5)
		var gt []int64
		var gc, gr []float64
		for i := range cnt {
			gt = append(gt, cnt[i].Time)
			gc = append(gc, cnt[i].Value)
			gr = append(gr, rt[i].Value)
		}
		if !reflect.DeepEqual(gt, c.wantTimes) || !reflect.DeepEqual(gc, c.wantCount) || !reflect.DeepEqual(gr, c.wantRT) {
			t.Errorf("%s: zaman %v sayı %v rt %v", c.name, gt, gc, gr)
		}
	}
	if cnt, _ := bucketTraceStarts([]traceStartRow{{"a", from, 1}}, from, 0, 0.5); cnt != nil {
		t.Error("step 0 → seri yok")
	}
}

func TestQuantileLinear(t *testing.T) {
	cases := []struct {
		xs   []float64
		q    float64
		want float64
	}{
		{nil, 0.5, 0},
		{[]float64{7}, 0.99, 7},
		{[]float64{30, 10, 20}, 0.5, 20},
		{[]float64{10, 20}, 0.5, 15},
		{[]float64{1, 2, 3, 4, 5}, 0.95, 4.8},
		{[]float64{1, 2, 3}, 0, 1},
		{[]float64{1, 2, 3}, 1, 3},
	}
	for _, c := range cases {
		if got := quantileLinear(c.xs, c.q); got < c.want-1e-9 || got > c.want+1e-9 {
			t.Errorf("quantileLinear(%v, %v) = %v, beklenen %v", c.xs, c.q, got, c.want)
		}
	}
}

// v0.10.1101 — Errors açık, çip YOK (yalnız servis / ortam / küme). 1082 bu
// sınıfı metric-batch'te bırakmıştı: şerit giriş span'lerinin hatasını
// sayıyordu (`kind IN (server, consumer)` + `status = error`), liste herhangi
// bir span'i hatalı trace'leri gösteriyor → istemci / iç span hataları şeritte
// ve "x ERROR SPANS / ERR RATE" başlığında yoktu. Parite: şeridin ham WHERE'i
// listenin bu sınıftaki HER yolunun WHERE'iyle bayt bayt aynı.
func TestTraceErrScopeWhereMatchesList(t *testing.T) {
	const ce = "cluster_name"
	base := TraceFilter{HasError: true, From: histFrom, To: histFrom.Add(time.Hour), Limit: 50, CountMode: "skip"}
	cases := []struct {
		name      string
		mut       func(*TraceFilter)
		wantWhere string
		wantArgs  []any
		// listenin bu süzgeçte koştuğu yol
		listMV, errorFirst bool
		// dar rollup'a oturur mu (ortam / küme rollup boyutu değil)
		rollup bool
	}{
		{"(a) yalnız Errors", func(*TraceFilter) {},
			"WHERE time >= ? AND time <= ? AND status_code = 'error'",
			[]any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour)}, true, false, true},
		{"(b) Errors + servis", func(f *TraceFilter) { f.Service = "svc-a" },
			"WHERE time >= ? AND time <= ? AND service_name = ? AND status_code = 'error'",
			[]any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour), "svc-a"}, true, true, true},
		{"(c) Errors + ortam", func(f *TraceFilter) { f.Env = "prod" },
			"WHERE time >= ? AND time <= ? AND status_code = 'error' AND deploy_env = ?",
			[]any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour), "prod"}, false, false, false},
		{"(c) Errors + servis + küme", func(f *TraceFilter) { f.Service, f.Cluster = "svc-a", "c1" },
			"WHERE time >= ? AND time <= ? AND service_name = ? AND status_code = 'error' AND cluster_name = ?",
			[]any{histFrom.Truncate(5 * time.Minute), histFrom.Add(time.Hour), "svc-a", "c1"}, false, false, false},
	}
	for _, c := range cases {
		f := base
		c.mut(&f)
		if !traceErrScopeOnlyEligible(f) || !TraceErrorHistogramEligible(f) {
			t.Fatalf("%s: ③ kapsam kipine uygun olmalı", c.name)
		}
		if !hasErrorSpanLocal(f) {
			t.Fatalf("%s: bu sınıfta liste hatayı WHERE'de aramalı (hasErrorSpanLocal)", c.name)
		}
		strip := traceErrScopeWhere(f, ce)
		if strip.sql() != c.wantWhere || !reflect.DeepEqual(strip.args, c.wantArgs) {
			t.Errorf("%s: şerit WHERE\n got %s %#v\nwant %s %#v", c.name, strip.sql(), strip.args, c.wantWhere, c.wantArgs)
		}
		if strings.Contains(strip.sql(), "kind") {
			t.Errorf("%s: şerit giriş kind kısıtı taşımamalı (liste taşımıyor)", c.name)
		}
		// Listenin ham yolu (MV dışı / MV-gap / <5 dk pencere): GetTraces →
		// buildGetTracesWhere(f) — kip probu ve hata-önce bu sınıfta koşmaz.
		if traceLevelErrorEligible(f) || errorFirstEligible(f) {
			t.Errorf("%s: ham liste yolu WHERE'i değiştirmemeli", c.name)
		}
		raw := buildGetTracesWhere(f, ce)
		if raw.sql() != strip.sql() || !reflect.DeepEqual(raw.args, strip.args) {
			t.Errorf("%s: liste ham WHERE'i ayrıştı:\n%s\n%s", c.name, raw.sql(), strip.sql())
		}
		if got := tracesMVEligible(f) && countModeAllowsMV(f.CountMode); got != c.listMV {
			t.Errorf("%s: liste MV yolu %v, beklenen %v", c.name, got, c.listMV)
		}
		if c.errorFirst {
			// Servisli MV dalı: adaylar hata-önce (spans) — aynı WHERE.
			ef := buildGetTracesWhere(errorFirstFilter(f), ce)
			ef.add("status_code = 'error'")
			if ef.sql() != strip.sql() || !reflect.DeepEqual(ef.args, strip.args) {
				t.Errorf("%s: hata-önce WHERE'i ayrıştı:\n%s\n%s", c.name, ef.sql(), strip.sql())
			}
		}
		if c.listMV && !c.errorFirst {
			// Servissiz MV dalı: trace_summary_5m'de aynı yüklem (hatalı span ≥ 1).
			if !noServiceSlicePlan(f).errorsPrefilter ||
				!strings.Contains((&Store{}).traceSliceScanSQL("desc", true), "finalizeAggregation(error_count_state) > 0") {
				t.Errorf("%s: liste MV dilimi hata ön süzgecini taşımalı", c.name)
			}
		}
		// Dar rollup: aynı yüklem (status = error [+ servis]), kind YOK, aynı pencere başı.
		bf, ok := traceErrScopeRollupFilter(f, 60, 0.95)
		if ok != c.rollup {
			t.Fatalf("%s: rollup uygunluğu %v, beklenen %v", c.name, ok, c.rollup)
		}
		if !ok {
			continue
		}
		q, nok := narrowRollupEligible(bf)
		if !nok {
			t.Fatalf("%s: rollup süzgeci dar rollup'a oturmalı", c.name)
		}
		got := map[string][]string{}
		for _, cj := range q.conjuncts {
			got[cj.col] = cj.values
		}
		want := map[string][]string{"status_code": {"error"}}
		if f.Service != "" {
			want["service_name"] = []string{f.Service}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: rollup yüklemi %v, beklenen %v", c.name, got, want)
		}
		if !bf.From.Equal(strip.args[0].(time.Time)) || !bf.To.Equal(f.To) {
			t.Errorf("%s: rollup penceresi listeninkinden ayrıştı: %v–%v", c.name, bf.From, bf.To)
		}
		if bf.Aggs[1].Aggregation != "p95" {
			t.Errorf("%s: rt istatistiği p95 olmalı: %v", c.name, bf.Aggs[1])
		}
	}
}

// v0.10.1101 — rollup okuma adımı ≤5 dk: saatlik katman ilk kısmi saati kaçırırdı.
func TestTraceErrScopeRollupStepCap(t *testing.T) {
	f := TraceFilter{HasError: true, From: time.Unix(1_700_000_000, 0), To: time.Unix(1_700_259_200, 0)}
	for _, c := range []struct {
		step, want int
		ok         bool
	}{{60, 60, true}, {300, 300, true}, {3600, 300, true}, {14400, 300, true}, {450, 0, false}} {
		bf, ok := traceErrScopeRollupFilter(f, c.step, 0.95)
		if ok != c.ok || (ok && bf.StepSeconds != c.want) {
			t.Errorf("adım %d: ok=%v step=%d, beklenen ok=%v step=%d", c.step, ok, bf.StepSeconds, c.ok, c.want)
		}
	}
}

func TestTraceErrHistRollupQuantile(t *testing.T) {
	for q, want := range map[float64]string{0.5: "p50", 0.95: "p95", 0.99: "p99", 0.9: "p50"} {
		if got := traceErrHistRollupQuantile(q); got != want {
			t.Errorf("%v → %s, beklenen %s", q, got, want)
		}
	}
}

// Rollup kovaları ham yolun greatest() kuralıyla ilk kovaya katlanır; toplam korunur.
func TestFoldHeadBuckets(t *testing.T) {
	t.Run("adım katlama: 5 dk satırları saatlik kovaya, baş dilimi ilk kovaya", func(t *testing.T) {
		p := func(t int64, v float64) SpanMetricPoint { return SpanMetricPoint{Time: t, Value: v} }
		// first = 3600 (pencere başı 3600 hizalı), adım 3600; satırlar 300 adımlı.
		count := []SpanMetricPoint{p(3300, 1), p(3600, 2), p(6900, 3), p(7200, 4)}
		rt := []SpanMetricPoint{p(3300, 10), p(3600, 20), p(6900, 30), p(7200, 40)}
		gc, gr := foldHeadBuckets(count, rt, 3600, 3600)
		wantC := []SpanMetricPoint{p(3600, 6), p(7200, 4)}
		wantRT := []SpanMetricPoint{p(3600, (10*1+20*2+30*3)/6.0), p(7200, 40)}
		if !reflect.DeepEqual(gc, wantC) || !reflect.DeepEqual(gr, wantRT) {
			t.Errorf("sayı %v rt %v", gc, gr)
		}
	})

	const first = int64(600)
	p := func(t int64, v float64) SpanMetricPoint { return SpanMetricPoint{Time: t, Value: v} }
	cases := []struct {
		name          string
		count, rt     []SpanMetricPoint
		wantC, wantRT []SpanMetricPoint
	}{
		{"boş", nil, nil, nil, nil},
		{"katlanacak yok, rt aynen", []SpanMetricPoint{p(600, 3), p(660, 1)}, []SpanMetricPoint{p(600, 12.5), p(660, 7)},
			[]SpanMetricPoint{p(600, 3), p(660, 1)}, []SpanMetricPoint{p(600, 12.5), p(660, 7)}},
		{"baş dilimi ilk kovaya, rt sayı-ağırlıklı", []SpanMetricPoint{p(480, 1), p(540, 1), p(600, 2)}, []SpanMetricPoint{p(480, 40), p(540, 20), p(600, 10)},
			[]SpanMetricPoint{p(600, 4)}, []SpanMetricPoint{p(600, 20)}},
		{"ilk kova boşken baş dilimi onu kurar", []SpanMetricPoint{p(540, 2), p(720, 1)}, []SpanMetricPoint{p(540, 9), p(720, 3)},
			[]SpanMetricPoint{p(600, 2), p(720, 1)}, []SpanMetricPoint{p(600, 9), p(720, 3)}},
	}
	for _, c := range cases {
		gc, gr := foldHeadBuckets(c.count, c.rt, first, 0)
		if !reflect.DeepEqual(gc, c.wantC) || !reflect.DeepEqual(gr, c.wantRT) {
			t.Errorf("%s: sayı %v rt %v", c.name, gc, gr)
		}
	}
}

// Kablolama: kip kararı listenin KENDİ fonksiyonundan (traceLevelErrorCandidates),
// ① WHERE'i traceErrBothWhere'den; liste probu da aynı yardımcıyı çağırır.
func TestTraceErrorHistogramWiring(t *testing.T) {
	readSrc := func(t *testing.T, name string) string {
		t.Helper()
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	src := readSrc(t, "trace_error_histogram.go")
	for _, w := range []string{
		"s.traceLevelErrorCandidates(ctx, f)",
		"traceErrBothWhere(f, s.clusterExpr())",
		"s.filterRootTracesAt(ctx, cands, f.From, f.To",
		"bucketTraceStarts(tr, fromNs, out.Step, q)",
		// v0.10.1101 — ③ kapsam kipi: rollup önce, yoksa listenin ham WHERE'i.
		"s.tryNarrowRollupFastPathMulti(ctx, bf, 0, 0)",
		"traceErrScopeWhere(f, s.clusterExpr())",
		"return buildGetTracesWhere(f, clusterExpr)",
	} {
		if !strings.Contains(src, w) {
			t.Errorf("trace_error_histogram.go %q taşımalı", w)
		}
	}
	// ③'ün ham WHERE'i listenin ham yolunun KENDİ çağrısıdır (GetTraces).
	if !strings.Contains(funcBody(t, "repo.go", "func (s *Store) GetTraces("), "wc := buildGetTracesWhere(f, s.clusterExpr())") {
		t.Error("GetTraces ham yolu buildGetTracesWhere(f, …) çağırmalı (şerit ③ aynı çağrıyı paylaşır)")
	}
	lvl := readSrc(t, "trace_error_tracelevel.go")
	if !strings.Contains(lvl, "return chip, errw, traceErrBothWhere(f, clusterExpr)") {
		t.Error("liste probu ① WHERE'ini traceErrBothWhere'den almalı (şeritle tek kaynak)")
	}
}
