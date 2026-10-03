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
		{"çip yok → metric-batch (dar rollup)", TraceFilter{HasError: true}, false},
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
	} {
		if !strings.Contains(src, w) {
			t.Errorf("trace_error_histogram.go %q taşımalı", w)
		}
	}
	lvl := readSrc(t, "trace_error_tracelevel.go")
	if !strings.Contains(lvl, "return chip, errw, traceErrBothWhere(f, clusterExpr)") {
		t.Error("liste probu ① WHERE'ini traceErrBothWhere'den almalı (şeritle tek kaynak)")
	}
}
