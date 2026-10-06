package chstore

import (
	"bytes"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1115 — Service › Operations, Normalized kip: satır /traces'i
// `name = <şekil>` ile açıyordu → hiçbir span adı şekle eşit değil, BOŞ liste
// ve boş şerit. Pivot artık `op_group = <şekil>` taşır; bu dosya anahtarın
// kolona çözümünü, op sınırını, kolonsuz kurulumun `name` düşüşünü ve her
// /traces okuma yolunun (ham WHERE, trace düzeyi HAVING, Errors şeridi,
// trace_summary_5m kapısı, hacim şeridinin MV/rollup kapıları) anahtarı
// nasıl ele aldığını pinler.

const opShape = "GET /orders/:id"

var opGroupChip = FilterExpr{Key: OpGroupFilterKey, Op: "=", Values: []string{opShape}}

func withOpGroupCol(t *testing.T, ready bool) {
	t.Helper()
	prev := opGroupColReady.Load()
	opGroupColReady.Store(ready)
	t.Cleanup(func() { opGroupColReady.Store(prev) })
}

func TestOpGroupFilterSQLGolden(t *testing.T) {
	withOpGroupCol(t, true)
	cases := []struct {
		name     string
		f        FilterExpr
		wantSQL  string
		wantArgs []any
	}{
		{"eşitlik", opGroupChip, "op_group = ?", []any{opShape}},
		{"boş op = sayılır", FilterExpr{Key: OpGroupFilterKey, Values: []string{opShape}}, "op_group = ?", []any{opShape}},
		{"!=", FilterExpr{Key: OpGroupFilterKey, Op: "!=", Values: []string{opShape}}, "op_group != ?", []any{opShape}},
		{"IN (küçük harf op)", FilterExpr{Key: OpGroupFilterKey, Op: "in", Values: []string{opShape, "POST /orders"}}, "op_group IN (?,?)", []any{opShape, "POST /orders"}},
		{"NOT IN", FilterExpr{Key: OpGroupFilterKey, Op: "NOT IN", Values: []string{opShape}}, "op_group NOT IN (?)", []any{opShape}},
	}
	for _, c := range cases {
		sql, args, err := c.f.SQL()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sql != c.wantSQL || !reflect.DeepEqual(args, c.wantArgs) {
			t.Errorf("%s:\n got %s %#v\nwant %s %#v", c.name, sql, args, c.wantSQL, c.wantArgs)
		}
		// Kolon; dizi araması / kvh / ad kolonu / metne gömülü değer YOK.
		if strings.Contains(sql, "attr_") || strings.Contains(sql, "name") || strings.Contains(sql, "orders") {
			t.Errorf("%s: şekil süzgeci kolon dışına düştü: %s", c.name, sql)
		}
		if err := c.f.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", c.name, err)
		}
	}
	// Self-join (relations) alias'ı kolonu niteler.
	if asql, _, _ := opGroupChip.SQLAliased("c"); asql != "c.op_group = ?" {
		t.Fatalf("alias'lı kolon: %s", asql)
	}
}

func TestOpGroupFilterRejectsNonIdentityOps(t *testing.T) {
	withOpGroupCol(t, true)
	bad := []FilterExpr{
		{Key: OpGroupFilterKey, Op: "LIKE", Values: []string{"orders"}},
		{Key: OpGroupFilterKey, Op: "=~", Values: []string{"GET .*"}},
		{Key: OpGroupFilterKey, Op: ">", Values: []string{"a"}},
		{Key: OpGroupFilterKey, Op: "EXISTS"},
		{Key: OpGroupFilterKey, Op: "=", Values: []string{"a", "b"}},
		{Key: OpGroupFilterKey, Op: "IN"},
	}
	for _, f := range bad {
		if err := f.Validate(); err == nil {
			t.Errorf("%s %v reddedilmeliydi (400)", f.Op, f.Values)
		}
		if _, _, err := f.SQL(); err == nil {
			t.Errorf("%s %v derlenmemeliydi", f.Op, f.Values)
		}
	}
}

// Kolonsuz kurulum (dış Distributed, cluster_name boş): Normalized tablo orada
// HAM span adlarını gösterir (v0.8.186 düşüşü), yani op_group değerinin tek
// üreticisi ham ad taşır → anahtar `name` ile birebir aynı derlenir (aynı op,
// aynı değerler), 400 YOK; bugün çalışan tıklama kırılmaz. Op kısıtı aynen
// kalır. Elle yazılmış şekil boş liste verir (daha dar, asla daha geniş).
func TestOpGroupFilterMissingColumnFallsBackToName(t *testing.T) {
	withOpGroupCol(t, false)
	cases := []struct {
		f        FilterExpr
		wantSQL  string
		wantArgs []any
	}{
		{FilterExpr{Key: OpGroupFilterKey, Op: "=", Values: []string{"GET /orders/8421"}}, "name = ?", []any{"GET /orders/8421"}},
		{FilterExpr{Key: OpGroupFilterKey, Values: []string{"GET /orders/8421"}}, "name = ?", []any{"GET /orders/8421"}},
		{FilterExpr{Key: OpGroupFilterKey, Op: "!=", Values: []string{"POST /orders"}}, "name != ?", []any{"POST /orders"}},
		{FilterExpr{Key: OpGroupFilterKey, Op: "in", Values: []string{"a", "b"}}, "name IN (?,?)", []any{"a", "b"}},
		{FilterExpr{Key: OpGroupFilterKey, Op: "NOT IN", Values: []string{"a"}}, "name NOT IN (?)", []any{"a"}},
	}
	for _, c := range cases {
		if err := c.f.Validate(); err != nil {
			t.Errorf("%s: kolonsuz kurulumda 400 olmamalı: %v", c.f.Op, err)
		}
		sql, args, err := c.f.SQL()
		if err != nil || sql != c.wantSQL || !reflect.DeepEqual(args, c.wantArgs) {
			t.Errorf("%s:\n got %q %#v %v\nwant %q %#v", c.f.Op, sql, args, err, c.wantSQL, c.wantArgs)
		}
		// `name` anahtarının KENDİ derlemesiyle bayt bayt aynı.
		nf := c.f
		nf.Key = "name"
		nsql, nargs, _ := nf.SQL()
		if sql != nsql || !reflect.DeepEqual(args, nargs) {
			t.Errorf("%s: name anahtarıyla ayrıştı: %q vs %q", c.f.Op, sql, nsql)
		}
	}
	if asql, _, _ := opGroupChip.SQLAliased("c"); asql != "c.name = ?" {
		t.Fatalf("alias'lı düşüş: %s", asql)
	}
	// Op kısıtı kolon yokken de aynı (400).
	for _, f := range []FilterExpr{
		{Key: OpGroupFilterKey, Op: "LIKE", Values: []string{"orders"}},
		{Key: OpGroupFilterKey, Op: "EXISTS"},
		{Key: OpGroupFilterKey, Op: "=", Values: []string{"a", "b"}},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%s reddedilmeliydi", f.Op)
		}
		if _, _, err := f.SQL(); err == nil {
			t.Errorf("%s derlenmemeliydi", f.Op)
		}
	}
	if err := ValidateFilters([]FilterExpr{{Key: "service.name", Op: "=", Values: []string{"svc-orders"}}, opGroupChip}); err != nil {
		t.Fatalf("ValidateFilters: %v", err)
	}
	// Liste WHERE'i: op_group kolonu ANILMAZ (code 47 → 500 olurdu), name'e düşer.
	wc := buildGetTracesWhere(TraceFilter{Service: "svc-orders", Filters: []FilterExpr{opGroupChip}, From: histFrom, To: histFrom.Add(time.Hour)}, "")
	if wc.sql() != "WHERE time >= ? AND time <= ? AND service_name = ? AND name = ?" || strings.Contains(wc.sql(), "op_group") {
		t.Fatalf("kolonsuz kurulumda WHERE: %s", wc.sql())
	}
}

// Düşüşün INFO satırı süreç başına BİR kez (her derlemede değil).
func TestOpGroupFilterFallbackLogsOnce(t *testing.T) {
	withOpGroupCol(t, false)
	prevLogged := opGroupFallbackLogged.Load()
	opGroupFallbackLogged.Store(false)
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
		opGroupFallbackLogged.Store(prevLogged)
	})
	for i := 0; i < 3; i++ {
		if _, _, err := opGroupChip.SQL(); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(buf.String(), "INFO: spans.op_group column absent"); n != 1 {
		t.Fatalf("INFO satırı %d kez yazıldı, 1 bekleniyordu:\n%s", n, buf.String())
	}
	// Kolon varken log yok.
	buf.Reset()
	opGroupFallbackLogged.Store(false)
	withOpGroupCol(t, true)
	_, _, _ = opGroupChip.SQL()
	if buf.Len() != 0 {
		t.Fatalf("kolon varken log beklenmiyordu: %s", buf.String())
	}
}

// metric_points'te kolon yok: anahtar orada eski davranışta (dizi araması).
func TestOpGroupFilterNotOnMetricPoints(t *testing.T) {
	withOpGroupCol(t, true)
	sql, _, err := opGroupChip.SQLForMetricPoints()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "op_group =") || !strings.Contains(sql, "attr_values") {
		t.Fatalf("metric_points kolonu anılmamalı: %s", sql)
	}
}

// Ham /traces WHERE'i: servis daraltması + parametreli `op_group = ?`.
// Arama + çip birlikteyken çip trace düzeyi HAVING'e taşınır (v0.10.341).
func TestOpGroupFilterTracesWhereAndHaving(t *testing.T) {
	withOpGroupCol(t, true)
	from := histFrom
	f := TraceFilter{Service: "svc-orders", Filters: []FilterExpr{opGroupChip}, From: from, To: from.Add(time.Hour)}
	wc := buildGetTracesWhere(f, "")
	want := "WHERE time >= ? AND time <= ? AND service_name = ? AND op_group = ?"
	if wc.sql() != want {
		t.Fatalf("liste WHERE:\n got %s\nwant %s", wc.sql(), want)
	}
	wantArgs := []any{from.Truncate(5 * time.Minute), from.Add(time.Hour), "svc-orders", opShape}
	if !reflect.DeepEqual(wc.args, wantArgs) {
		t.Fatalf("args %#v\nwant %#v", wc.args, wantArgs)
	}

	f.Search = "timeout"
	if wc := buildGetTracesWhere(f, ""); strings.Contains(wc.sql(), "op_group") {
		t.Fatalf("arama varken çip WHERE'de kalmamalı: %s", wc.sql())
	}
	parts, args := traceLevelFilterHaving(f)
	if !reflect.DeepEqual(parts, []string{"countIf(op_group = ?) > 0"}) || !reflect.DeepEqual(args, []any{opShape}) {
		t.Fatalf("HAVING %#v %#v", parts, args)
	}
}

// trace_summary_5m'de op_group yok: çip MV hızlı yolunu (liste + sayım)
// kapatır ve SPAN düzeyi çip sayılır (satır onarımı + kök sorusu daraltılmamış
// kaynaktan) — diğer nitelik çipleriyle aynı.
func TestOpGroupFilterRoutesTracesOffMV(t *testing.T) {
	withOpGroupCol(t, true)
	base := TraceFilter{Service: "svc-orders", From: histFrom, To: histFrom.Add(time.Hour)}
	if !tracesMVEligible(base) {
		t.Fatal("ön koşul: çipsiz servis süzgeci MV'de")
	}
	f := base
	f.Filters = []FilterExpr{opGroupChip}
	if tracesMVEligible(f) {
		t.Fatal("op_group çipi trace_summary_5m hızlı yolunu kapatmalı")
	}
	if src, _, _, reason := traceCountPlan(f, TraceRootDefStrict, false); src != "" || reason != traceCountReasonRawPath {
		t.Fatalf("sayım planı ham yol demeli: src=%q reason=%q", src, reason)
	}
	if !spanScopedChips(f) || !rootScopeNarrowed(f) {
		t.Fatal("op_group span düzeyi çip sayılmalı")
	}
	g := base
	g.FilterRoot = &FilterGroup{Join: "AND", Filters: []FilterExpr{opGroupChip}}
	if tracesMVEligible(g) {
		t.Fatal("gruplu kökteki op_group da MV'yi kapatmalı")
	}
}

// Errors + op_group çipi: şerit listenin kip kararını paylaşır (v0.10.1082) ve
// aynı WHERE'i taşır.
func TestOpGroupFilterListAndErrorStripParity(t *testing.T) {
	withOpGroupCol(t, true)
	f := histFilter(opGroupChip)
	if !TraceErrorHistogramEligible(f) {
		t.Fatal("Errors + op_group çipi şeridi liste yoluna almalı")
	}
	list := buildGetTracesWhere(TraceFilter{Filters: f.Filters, From: f.From, To: f.To}, "")
	wantList := "WHERE time >= ? AND time <= ? AND op_group = ?"
	if list.sql() != wantList {
		t.Fatalf("liste WHERE:\n got %s\nwant %s", list.sql(), wantList)
	}
	strip := traceErrBothWhere(f, "")
	if strip.sql() != wantList+" AND status_code = 'error'" {
		t.Fatalf("şerit WHERE: %s", strip.sql())
	}
	if !reflect.DeepEqual(list.args, strip.args) {
		t.Fatalf("args ayrıştı:\n list  %#v\n strip %#v", list.args, strip.args)
	}
}

// Hacim şeridi (metric-batch): op_group op-MV'nin ve dar rollup'ın boyutu
// değil, metrik kademesi çözücüsünde de yok → ham spans; ham WHERE parametreli
// yüklemi taşır.
func TestOpGroupFilterVolumeStripTakesRawPath(t *testing.T) {
	withOpGroupCol(t, true)
	svc := FilterExpr{Key: "service.name", Op: "=", Values: []string{"svc-orders"}}
	if _, ok := operationMVGate(nil, []FilterExpr{svc, opGroupChip}); ok {
		t.Fatal("operation_summary_5m kapısı op_group'u reddetmeli")
	}
	if _, ok := operationMVGate(nil, []FilterExpr{svc, {Key: "name", Op: "=", Values: []string{"GET /orders/8421"}}}); !ok {
		t.Fatal("ön koşul: ad çipi op-MV'de kalır")
	}
	bf := SpanMetricBatchFilter{
		From: histFrom, To: histFrom.Add(time.Hour), StepSeconds: 60,
		Filters: []FilterExpr{svc, opGroupChip},
		Aggs:    []SpanMetricAggSpec{{Name: "count", Aggregation: "count"}},
	}
	if _, ok := narrowRollupEligible(bf); ok {
		t.Fatal("dar rollup op_group'u boyut olarak tanımamalı")
	}
	if _, ok := tierDimColumn(OpGroupFilterKey); ok {
		t.Fatal("spanmetrics kademeleri op_group taşımaz")
	}
	wc := spanMetricBatchWhere(bf, 0, 0)
	if wc.sql() != "WHERE time >= ? AND time <= ? AND service_name = ? AND op_group = ?" {
		t.Fatalf("ham şerit WHERE: %s", wc.sql())
	}
	if got := wc.args[len(wc.args)-1]; got != opShape {
		t.Fatalf("şekil bağlanmalı: %#v", wc.args)
	}
}
