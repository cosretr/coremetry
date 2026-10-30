package chstore

// trace_services_in_test.go — v0.10.1684 (CoSRE v2 araç seti dilim 3):
// TraceFilter.Services / LogFilter.Services sözleşmesi. Tekil Service yolu
// bayt bayt aynı kalmalı; küme sıralı + tekil + ≤MaxTraceServices; tek
// elemanlı küme tekil yola iner; hatalı-yalnız küme MV'den düşer.

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNormalizeTraceServices(t *testing.T) {
	many := make([]string, 0, MaxTraceServices+5)
	for i := 0; i < MaxTraceServices+5; i++ {
		many = append(many, fmt.Sprintf("svc-%03d", i))
	}
	cases := []struct {
		name      string
		in        TraceFilter
		service   string
		services  []string
		countMode string
	}{
		{"boş küme aynen", TraceFilter{Service: "orders-api", CountMode: "exact"}, "orders-api", nil, "exact"},
		{"tekil Service kümeyi düşürür", TraceFilter{Service: "orders-api", Services: []string{"a", "b"}}, "orders-api", nil, ""},
		{"RequireServices kümeyi düşürür", TraceFilter{RequireServices: []string{"x"}, Services: []string{"a", "b"}}, "", nil, ""},
		{"tek eleman tekil yola", TraceFilter{Services: []string{" orders-api ", ""}}, "orders-api", nil, ""},
		{"sırala + tekilleştir + sayım skip", TraceFilter{Services: []string{"payments-api", "orders-api", "payments-api"}, CountMode: "exact"}, "", []string{"orders-api", "payments-api"}, "skip"},
		{"yalnız boşluk → küme yok", TraceFilter{Services: []string{" ", ""}}, "", nil, ""},
	}
	for _, c := range cases {
		got := normalizeTraceServices(c.in)
		if got.Service != c.service || !reflect.DeepEqual(got.Services, c.services) || got.CountMode != c.countMode {
			t.Errorf("%s: got service=%q services=%v count=%q", c.name, got.Service, got.Services, got.CountMode)
		}
	}
	got := normalizeTraceServices(TraceFilter{Services: many})
	if len(got.Services) != MaxTraceServices || got.Services[0] != "svc-000" {
		t.Errorf("tavan: got %d first=%q", len(got.Services), got.Services[0])
	}
}

func TestBuildGetTracesWhereServices(t *testing.T) {
	from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_003_600, 0)
	cases := []struct {
		name     string
		f        TraceFilter
		wantCond string
		wantArgs []any
		notCond  string
	}{
		{"tekil yol aynen", TraceFilter{Service: "orders-api", From: from, To: to}, "service_name = ?", []any{"orders-api"}, "service_name IN"},
		{"küme IN", TraceFilter{Services: []string{"orders-api", "payments-api"}, From: from, To: to}, "service_name IN (?,?)", []any{"orders-api", "payments-api"}, "service_name = ?"},
		{"RequireServices önce", TraceFilter{RequireServices: []string{"x"}, Services: []string{"a", "b"}, From: from, To: to}, "service_name IN (?)", []any{"x"}, "IN (?,?)"},
	}
	for _, c := range cases {
		wc := buildGetTracesWhere(c.f, "cluster")
		sql := wc.sql()
		if !strings.Contains(sql, c.wantCond) || strings.Contains(sql, c.notCond) {
			t.Errorf("%s: sql=%s", c.name, sql)
		}
		joined := fmt.Sprint(wc.args)
		for _, a := range c.wantArgs {
			if !strings.Contains(joined, a.(string)) {
				t.Errorf("%s: arg %v yok: %s", c.name, a, joined)
			}
		}
	}
}

func TestTracesMVEligibleServices(t *testing.T) {
	from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_003_600, 0)
	base := TraceFilter{From: from, To: to}
	cases := []struct {
		name string
		mut  func(*TraceFilter)
		want bool
	}{
		{"tekil + hata MV", func(f *TraceFilter) { f.Service, f.HasError = "orders-api", true }, true},
		{"küme MV", func(f *TraceFilter) { f.Services = []string{"a", "b"} }, true},
		{"küme + hata ham", func(f *TraceFilter) { f.Services, f.HasError = []string{"a", "b"}, true }, false},
	}
	for _, c := range cases {
		f := base
		c.mut(&f)
		if got := tracesMVEligible(f); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestTraceServicesSliceScanSQL(t *testing.T) {
	s := &Store{}
	for _, c := range []struct {
		order string
		n     int
		want  []string
	}{
		{"desc", 2, []string{"FROM trace_service_index_5m", "service_name IN (?,?)", "ORDER BY time_bucket DESC", "LIMIT ?", "max_execution_time = 10"}},
		{"asc", 3, []string{"service_name IN (?,?,?)", "ORDER BY time_bucket ASC"}},
	} {
		sql := s.traceServicesSliceScanSQL(c.order, c.n)
		for _, w := range c.want {
			if !strings.Contains(sql, w) {
				t.Errorf("%s/%d: %q yok:\n%s", c.order, c.n, w, sql)
			}
		}
	}
	// tekil ikiz değişmedi
	if !strings.Contains(s.traceServiceSliceScanSQL("desc"), "WHERE service_name = ? AND time_bucket >= ?") {
		t.Error("tekil servis dilimi SQL'i değişti")
	}
}

func TestLogsWhereServices(t *testing.T) {
	cases := []struct {
		name, want, not string
		f               LogFilter
	}{
		{"tekil aynen", "service_name = ?", "service_name IN", LogFilter{Service: "orders-api", Services: []string{"a", "b"}}},
		{"küme IN", "service_name IN (?,?)", "service_name = ?", LogFilter{Services: []string{"payments-api", "orders-api", "orders-api"}}},
		{"boş", "", "service_name", LogFilter{Services: []string{""}}},
	}
	for _, c := range cases {
		wc := logsWhere(c.f)
		sql := wc.sql()
		if (c.want != "" && !strings.Contains(sql, c.want)) || strings.Contains(sql, c.not) {
			t.Errorf("%s: %s", c.name, sql)
		}
	}
	if got := LogServicesSet("", []string{"b", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("LogServicesSet sırası: %v", got)
	}
}
