package chstore

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.1801 — Operator-reported (test ortamı): /traces?range=24h&service=X +
// `http.route = …` + `channel_code = …` → liste 15+ satır, şerit "0 SPANS ·
// No traces in view to bucket" (Errors kapalı). Liste servisli özel çipi
// trace düzeyinde yarı-birleşimle keser (http.route servisin span'inde,
// channel_code başka span'de); şerit metric-batch'te iki çipi AYNI span'de
// AND'liyordu. ④ çip-trace kipi: şerit listenin KENDİ WHERE'inin trace'lerini
// başlangıç zamanına kovalar.

var (
	chipRoute   = FilterExpr{Key: "http.route", Op: "=", Values: []string{"/api/v1/orders"}}
	chipChannel = FilterExpr{Key: "channel_code", Op: "=", Values: []string{"MOBILE"}}
)

func chipHistFilter(filters ...FilterExpr) TraceFilter {
	return TraceFilter{Service: "orders-api", Filters: filters, From: histFrom, To: histFrom.Add(24 * time.Hour)}
}

func TestTraceChipTraceEligible(t *testing.T) {
	with := func(mut func(*TraceFilter)) TraceFilter {
		f := chipHistFilter(chipRoute, chipChannel)
		mut(&f)
		return f
	}
	cases := []struct {
		name     string
		f        TraceFilter
		chip     bool // traceChipTraceEligible
		eligible bool // TraceErrorHistogramEligible
	}{
		{"operatör şekli: servis + http.route + channel_code", chipHistFilter(chipRoute, chipChannel), true, true},
		{"Root serbest (adaylar üstünde doğrulanır)", with(func(f *TraceFilter) { f.RootOnly = true }), true, true},
		{"gruplu kök, iki yaprak", with(func(f *TraceFilter) {
			f.Filters = nil
			f.FilterRoot = &FilterGroup{Join: "OR", Filters: []FilterExpr{chipRoute}, Groups: []FilterGroup{{Filters: []FilterExpr{chipChannel}}}}
		}), true, true},
		{"tek çip → metric-batch", chipHistFilter(chipChannel), false, false},
		{"servissiz → metric-batch (aynı span yüklemi)", with(func(f *TraceFilter) { f.Service = "" }), false, false},
		{"Errors → ① / ② yolu (çip kipi değil)", with(func(f *TraceFilter) { f.HasError = true }), false, true},
		{"arama → metric-batch", with(func(f *TraceFilter) { f.Search = "x" }), false, false},
		{"süre → metric-batch", with(func(f *TraceFilter) { f.MinMs = 5 }), false, false},
		{"services → metric-batch", with(func(f *TraceFilter) { f.RequireServices = []string{"a", "b"} }), false, false},
		{"trace id → metric-batch", with(func(f *TraceFilter) { f.TraceID = "abc" }), false, false},
		{"NOT HAS → metric-batch", with(func(f *TraceFilter) {
			f.Filters = append(f.Filters, FilterExpr{Key: "peer.service", Op: "NOT HAS", Values: []string{"cache"}})
		}), false, false},
	}
	for _, c := range cases {
		if got := traceChipTraceEligible(c.f); got != c.chip {
			t.Errorf("%s: chip %v, beklenen %v", c.name, got, c.chip)
		}
		if got := TraceErrorHistogramEligible(c.f); got != c.eligible {
			t.Errorf("%s: eligible %v, beklenen %v", c.name, got, c.eligible)
		}
	}
}

func TestTraceChipTraceIDsSQLGolden(t *testing.T) {
	got := traceChipTraceIDsSQL("WHERE time >= ? AND time <= ?")
	want := `
		SELECT trace_id
		FROM spans WHERE time >= ? AND time <= ?
		ORDER BY time DESC
		LIMIT ?
		SETTINGS max_execution_time = 15,
		         distributed_product_mode = 'global'`
	if got != want {
		t.Fatalf("chip SQL:\n%s\nwant\n%s", got, want)
	}
}

// ④'ün aday WHERE'i GetTraces ham yolunun KENDİ çağrısıdır (kopya zincir
// yok) — liste ile şerit ayrışamaz. Operatör şeklinde bu WHERE channel_code'u
// trace düzeyinde (yarı-birleşim) taşır; aynı-span AND'i (eski şerit) değil.
func TestTraceChipTraceSharesListWhere(t *testing.T) {
	body := funcBody(t, "trace_error_histogram.go", "func (s *Store) traceChipTraceCandidates(")
	if !strings.Contains(body, "wc := buildGetTracesWhere(f, s.clusterExpr())") {
		t.Error("④ adayları listenin buildGetTracesWhere(f, …) çağrısını kullanmalı")
	}
	if !strings.Contains(funcBody(t, "repo.go", "func (s *Store) GetTraces("), "wc := buildGetTracesWhere(f, s.clusterExpr())") {
		t.Error("GetTraces ham yolu buildGetTracesWhere(f, …) çağırmalı")
	}
	prevPM := promotedColsPtr.Load()
	empty := map[string]string{}
	promotedColsPtr.Store(&empty)
	defer promotedColsPtr.Store(prevPM)
	wc := buildGetTracesWhere(chipHistFilter(chipRoute, chipChannel), "")
	sql := wc.sql()
	semi := strings.Index(sql, "trace_id GLOBAL IN (")
	if semi < 0 || !strings.Contains(sql, "service_name = ?") {
		t.Fatalf("liste WHERE'i servis + yarı-birleşim taşımalı:\n%s", sql)
	}
	inner := sql[semi:]
	if !strings.Contains(fmt.Sprint(wc.args), "MOBILE") || strings.Count(inner, "SELECT trace_id FROM spans") != 1 {
		t.Errorf("channel_code tek seviyeli zincirde olmalı:\n%s", sql)
	}
}

func TestBucketTraceErrors(t *testing.T) {
	from := histFrom.UnixNano()
	step := 60
	stepNs := int64(step) * int64(time.Second)
	b0 := (from / stepNs) * stepNs
	rows := []traceStartRow{
		{"a", from + 1, 10, true},
		{"b", from - int64(time.Minute), 10, false}, // pencere öncesi → ilk kova
		{"c", b0 + stepNs + 3, 10, false},
		{"d", b0 + 2*stepNs + 3, 10, true},
		{"e", b0 + 2*stepNs + 4, 10, true},
	}
	got := bucketTraceErrors(rows, from, step)
	want := []SpanMetricPoint{{Time: b0, Value: 1}, {Time: b0 + stepNs, Value: 0}, {Time: b0 + 2*stepNs, Value: 2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("errors %v, beklenen %v", got, want)
	}
	cnt, _ := bucketTraceStarts(rows, from, step, 0.5)
	if len(cnt) != len(got) {
		t.Errorf("errors kovaları count kovalarıyla aynı olmalı: %d vs %d", len(got), len(cnt))
	}
	for i := range cnt {
		if cnt[i].Time != got[i].Time || got[i].Value > cnt[i].Value {
			t.Errorf("kova %d: count %v errors %v", i, cnt[i], got[i])
		}
	}
	if bucketTraceErrors(rows, from, 0) != nil || bucketTraceErrors(nil, from, step) != nil {
		t.Error("step 0 / boş → nil")
	}
}

// ④ Errors serisi has_err'den; ② Errors = Count kalır.
func TestTraceChipHistogramWiring(t *testing.T) {
	body := funcBody(t, "trace_error_histogram.go", "func (s *Store) TraceErrorHistogram(")
	for _, w := range []string{
		"if traceChipTraceEligible(f) {",
		"s.traceChipTraceCandidates(ctx, f)",
		"out.Errors = bucketTraceErrors(tr, fromNs, out.Step)",
		"s.keepRootTraces(ctx, f, tr)",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("TraceErrorHistogram %q taşımalı", w)
		}
	}
	if !strings.Contains(funcBody(t, "trace_error_histogram.go", "func (s *Store) traceErrHistTraceRows("), "&r.hasErr") {
		t.Error("trace satırı has_err'i okumalı")
	}
}
