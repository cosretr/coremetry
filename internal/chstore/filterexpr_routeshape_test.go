package chstore

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1117 — /endpoints "Group by shape" satırı (ve endpoint detay sayfası)
// /traces'i `http.route = <şekil>` ile açıyordu → span'ler ham id taşıdığında
// (`/users/8421`) hiçbir satır şekle (`/users/:id`) eşit değil, BOŞ liste ve
// boş şerit. Pivot artık `http.route_shape = <şekil>` (RPC sekmesi + şekil:
// `name_shape`) taşır; bu dosya anahtarın endpoints sayfasının GRUPLADIĞI
// ifadeye (opSigWrap) çözümünü, op sınırını ve her /traces okuma yolunun
// (ham WHERE, trace düzeyi HAVING, Errors şeridi, trace_summary_5m kapısı,
// hacim şeridinin MV/rollup/kademe kapıları) anahtarı nasıl ele aldığını
// pinler (filterexpr_opgroup_test.go şablonu, v0.10.1115). Sürücü bağlama
// yolu round-trip'i: ch_bind_roundtrip_test.go.

const routeShape = "/users/:id"

var routeShapeChip = FilterExpr{Key: RouteShapeFilterKey, Op: "=", Values: []string{routeShape}}

// wrapped — opSigWrap'in beklenen metni (UUID, hex, sayı desenleri bağlı).
func wrapped(col string) string {
	return "replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(" + col + ", ?, ':id'), ?, '/:id'), ?, '/:id')"
}

func shapeArgs(vs ...any) []any {
	return append([]any{OpSigReUUID, OpSigReHex, OpSigReNum}, vs...)
}

func TestShapeFilterSQLGolden(t *testing.T) {
	cases := []struct {
		name     string
		f        FilterExpr
		wantSQL  string
		wantArgs []any
	}{
		{"route =", routeShapeChip, wrapped("http_route") + " = ?", shapeArgs(routeShape)},
		{"route boş op = sayılır", FilterExpr{Key: RouteShapeFilterKey, Values: []string{routeShape}}, wrapped("http_route") + " = ?", shapeArgs(routeShape)},
		{"route !=", FilterExpr{Key: RouteShapeFilterKey, Op: "!=", Values: []string{routeShape}}, wrapped("http_route") + " != ?", shapeArgs(routeShape)},
		{"route IN (küçük harf op)", FilterExpr{Key: RouteShapeFilterKey, Op: "in", Values: []string{routeShape, "/orders"}}, wrapped("http_route") + " IN (?,?)", shapeArgs(routeShape, "/orders")},
		{"route NOT IN", FilterExpr{Key: RouteShapeFilterKey, Op: "NOT IN", Values: []string{routeShape}}, wrapped("http_route") + " NOT IN (?)", shapeArgs(routeShape)},
		{"name =", FilterExpr{Key: NameShapeFilterKey, Op: "=", Values: []string{"process order/:id"}}, wrapped("name") + " = ?", shapeArgs("process order/:id")},
		{"name NOT IN", FilterExpr{Key: NameShapeFilterKey, Op: "NOT IN", Values: []string{"a/:id", "b"}}, wrapped("name") + " NOT IN (?,?)", shapeArgs("a/:id", "b")},
	}
	for _, c := range cases {
		sql, args, err := c.f.SQL()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if sql != c.wantSQL || !reflect.DeepEqual(args, c.wantArgs) {
			t.Errorf("%s:\n got %s %#v\nwant %s %#v", c.name, sql, args, c.wantSQL, c.wantArgs)
		}
		// Placeholder sayısı = argüman sayısı (konumsal bağlama sözleşmesi).
		if n := strings.Count(sql, "?"); n != len(args) {
			t.Errorf("%s: %d yer tutucu, %d argüman", c.name, n, len(args))
		}
		// Dizi araması / kvh / metne gömülü değer ya da desen YOK; süslü
		// parantez yok (v0.8.356 tuzağı).
		if strings.Contains(sql, "attr_") || strings.Contains(sql, "users") || strings.ContainsAny(sql, "{}") {
			t.Errorf("%s: şekil süzgeci kolon ifadesi dışına düştü / değer metne sızdı: %s", c.name, sql)
		}
		if err := c.f.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", c.name, err)
		}
	}
	// Self-join (relations) alias'ı sarılan kolonu niteler.
	if asql, _, _ := routeShapeChip.SQLAliased("c"); asql != wrapped("c.http_route")+" = ?" {
		t.Fatalf("alias'lı kolon: %s", asql)
	}
}

// Endpoints sayfasıyla AYNI ifade: liste satırını üreten opSigWrap(http_route)
// ve detay çekmecesinin endpointRoutePred yüklemi ile bayt bayt aynı.
func TestShapeFilterMatchesEndpointsExpression(t *testing.T) {
	sql, args, err := routeShapeChip.SQL()
	if err != nil {
		t.Fatal(err)
	}
	var wc whereClause
	endpointRoutePred(&wc, routeShape, true)
	if len(wc.conds) != 1 || wc.conds[0] != sql || !reflect.DeepEqual(wc.args, args) {
		t.Fatalf("detay çekmecesi yüklemiyle ayrıştı:\n chip   %s %#v\n drawer %v %#v", sql, args, wc.conds, wc.args)
	}
	if !strings.HasPrefix(sql, opSigWrap("http_route")) {
		t.Fatalf("liste projeksiyonu opSigWrap(http_route) değil: %s", sql)
	}
	nsql, _, _ := FilterExpr{Key: NameShapeFilterKey, Values: []string{"x"}}.SQL()
	if !strings.HasPrefix(nsql, opSigWrap("name")) {
		t.Fatalf("RPC sekmesi projeksiyonu opSigWrap(name) değil: %s", nsql)
	}
}

func TestShapeFilterRejectsNonIdentityOps(t *testing.T) {
	for _, key := range []string{RouteShapeFilterKey, NameShapeFilterKey} {
		bad := []FilterExpr{
			{Key: key, Op: "LIKE", Values: []string{"users"}},
			{Key: key, Op: "=~", Values: []string{"/users/.*"}},
			{Key: key, Op: ">", Values: []string{"a"}},
			{Key: key, Op: "EXISTS"},
			{Key: key, Op: "NOT EXISTS"},
			{Key: key, Op: "=", Values: []string{"a", "b"}},
			{Key: key, Op: "IN"},
		}
		for _, f := range bad {
			if err := f.Validate(); err == nil {
				t.Errorf("%s %s %v reddedilmeliydi (400)", key, f.Op, f.Values)
			}
			if _, _, err := f.SQL(); err == nil {
				t.Errorf("%s %s %v derlenmemeliydi", key, f.Op, f.Values)
			}
		}
	}
	if err := ValidateFilters([]FilterExpr{{Key: "service.name", Op: "=", Values: []string{"svc-orders"}}, routeShapeChip}); err != nil {
		t.Fatalf("ValidateFilters: %v", err)
	}
}

// Ham http.route anahtarı değişmedi (yalnız yeni anahtar sarılır).
func TestShapeFilterLeavesRawRouteKeyAlone(t *testing.T) {
	sql, args, err := FilterExpr{Key: "http.route", Op: "=", Values: []string{"/users/8421"}}.SQL()
	if err != nil || sql != "http_route = ?" || !reflect.DeepEqual(args, []any{"/users/8421"}) {
		t.Fatalf("http.route: %q %#v %v", sql, args, err)
	}
}

// metric_points'te kolon yok: anahtar orada eski davranışta (dizi araması).
func TestShapeFilterNotOnMetricPoints(t *testing.T) {
	sql, _, err := routeShapeChip.SQLForMetricPoints()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "replaceRegexpAll") || !strings.Contains(sql, "attr_values") {
		t.Fatalf("metric_points şekil ifadesi kurmamalı: %s", sql)
	}
}

// Ham /traces WHERE'i: servis daraltması + parametreli şekil yüklemi. Arama +
// çip birlikteyken çip trace düzeyi HAVING'e taşınır (v0.10.341).
func TestShapeFilterTracesWhereAndHaving(t *testing.T) {
	from := histFrom
	f := TraceFilter{Service: "svc-orders", Filters: []FilterExpr{routeShapeChip}, From: from, To: from.Add(time.Hour)}
	wc := buildGetTracesWhere(f, "")
	want := "WHERE time >= ? AND time <= ? AND service_name = ? AND " + wrapped("http_route") + " = ?"
	if wc.sql() != want {
		t.Fatalf("liste WHERE:\n got %s\nwant %s", wc.sql(), want)
	}
	wantArgs := append([]any{from.Truncate(5 * time.Minute), from.Add(time.Hour), "svc-orders"}, shapeArgs(routeShape)...)
	if !reflect.DeepEqual(wc.args, wantArgs) {
		t.Fatalf("args %#v\nwant %#v", wc.args, wantArgs)
	}

	f.Search = "timeout"
	if wc := buildGetTracesWhere(f, ""); strings.Contains(wc.sql(), "replaceRegexpAll") {
		t.Fatalf("arama varken çip WHERE'de kalmamalı: %s", wc.sql())
	}
	parts, args := traceLevelFilterHaving(f)
	if !reflect.DeepEqual(parts, []string{"countIf(" + wrapped("http_route") + " = ?) > 0"}) || !reflect.DeepEqual(args, shapeArgs(routeShape)) {
		t.Fatalf("HAVING %#v %#v", parts, args)
	}
}

// trace_summary_5m'de şekil yok: çip MV hızlı yolunu (liste + sayım) kapatır
// ve SPAN düzeyi çip sayılır (satır onarımı + kök sorusu daraltılmamış
// kaynaktan) — op_group ve diğer nitelik çipleriyle aynı.
func TestShapeFilterRoutesTracesOffMV(t *testing.T) {
	base := TraceFilter{Service: "svc-orders", From: histFrom, To: histFrom.Add(time.Hour)}
	if !tracesMVEligible(base) {
		t.Fatal("ön koşul: çipsiz servis süzgeci MV'de")
	}
	for _, chip := range []FilterExpr{routeShapeChip, {Key: NameShapeFilterKey, Op: "=", Values: []string{"a/:id"}}} {
		f := base
		f.Filters = []FilterExpr{chip}
		if tracesMVEligible(f) {
			t.Fatalf("%s çipi trace_summary_5m hızlı yolunu kapatmalı", chip.Key)
		}
		if src, _, _, reason := traceCountPlan(f, TraceRootDefStrict, false); src != "" || reason != traceCountReasonRawPath {
			t.Fatalf("%s: sayım planı ham yol demeli: src=%q reason=%q", chip.Key, src, reason)
		}
		if !spanScopedChips(f) || !rootScopeNarrowed(f) {
			t.Fatalf("%s span düzeyi çip sayılmalı", chip.Key)
		}
		g := base
		g.FilterRoot = &FilterGroup{Join: "AND", Filters: []FilterExpr{chip}}
		if tracesMVEligible(g) {
			t.Fatalf("gruplu kökteki %s de MV'yi kapatmalı", chip.Key)
		}
	}
}

// Errors + şekil çipi: şerit listenin kip kararını paylaşır (v0.10.1082) ve
// aynı WHERE'i (aynı bağlı desen argümanlarıyla) taşır.
func TestShapeFilterListAndErrorStripParity(t *testing.T) {
	f := histFilter(routeShapeChip)
	if !TraceErrorHistogramEligible(f) {
		t.Fatal("Errors + şekil çipi şeridi liste yoluna almalı")
	}
	list := buildGetTracesWhere(TraceFilter{Filters: f.Filters, From: f.From, To: f.To}, "")
	wantList := "WHERE time >= ? AND time <= ? AND " + wrapped("http_route") + " = ?"
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

// Hacim şeridi (metric-batch) + Explore metrik sonucu: şekil op-MV'nin, dar
// rollup'ın ve spanmetrics kademelerinin boyutu değil → ham spans; ham WHERE
// parametreli yüklemi taşır.
func TestShapeFilterVolumeStripTakesRawPath(t *testing.T) {
	svc := FilterExpr{Key: "service.name", Op: "=", Values: []string{"svc-orders"}}
	if _, ok := operationMVGate(nil, []FilterExpr{svc, routeShapeChip}); ok {
		t.Fatal("operation_summary_5m kapısı http.route_shape'i reddetmeli")
	}
	bf := SpanMetricBatchFilter{
		From: histFrom, To: histFrom.Add(time.Hour), StepSeconds: 60,
		Filters: []FilterExpr{svc, routeShapeChip},
		Aggs:    []SpanMetricAggSpec{{Name: "count", Aggregation: "count"}},
	}
	if _, ok := narrowRollupEligible(bf); ok {
		t.Fatal("dar rollup http.route_shape'i boyut olarak tanımamalı")
	}
	for _, k := range []string{RouteShapeFilterKey, NameShapeFilterKey} {
		if _, ok := tierDimColumn(k); ok {
			t.Fatalf("spanmetrics kademeleri %s taşımaz", k)
		}
	}
	// Ön koşul: ham http.route kademede (şekil anahtarı onu taklit etmiyor).
	if col, ok := tierDimColumn("http.route"); !ok || col != "http_route" {
		t.Fatal("ön koşul: http.route kademe boyutu")
	}
	wc := spanMetricBatchWhere(bf, 0, 0)
	if wc.sql() != "WHERE time >= ? AND time <= ? AND service_name = ? AND "+wrapped("http_route")+" = ?" {
		t.Fatalf("ham şerit WHERE: %s", wc.sql())
	}
	if got := wc.args[len(wc.args)-4:]; !reflect.DeepEqual(got, shapeArgs(routeShape)) {
		t.Fatalf("desenler + şekil bağlanmalı: %#v", wc.args)
	}
}
