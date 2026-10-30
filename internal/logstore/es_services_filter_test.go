package logstore

// es_services_filter_test.go — v0.10.1684 (CoSRE v2 araç seti dilim 3):
// Filter.Services her iki arka uçta; tekil Service yolu bayt bayt aynı.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestESServicesFilter(t *testing.T) {
	s := &ESStore{}
	s.cfg.defaults()
	s.fields = s.cfg.Fields
	q := func(f Filter) string {
		b, err := json.Marshal(s.buildQuery(f))
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(b)
	}
	single := q(Filter{Service: "orders-api"})
	cases := []struct {
		name      string
		f         Filter
		want, not []string
	}{
		{"tekil Service kümeyi yok sayar (bayt aynı)", Filter{Service: "orders-api", Services: []string{"payments-api", "x"}}, nil, []string{`"terms"`}},
		{"küme terms, sıralı + env eki soyulur", Filter{Services: []string{"payments-api", "orders-api-prod", "payments-api"}},
			[]string{`"terms":{"service.name.keyword":["orders-api-prod","orders-api","payments-api"]}`, `"kubernetes.container_name.keyword"`, `"must_not"`}, []string{`"term":{`}},
		{"boş küme süzgeç yok", Filter{Services: []string{" "}}, nil, []string{`service.name`}},
	}
	for _, c := range cases {
		got := q(c.f)
		if c.f.Service != "" && got != single {
			t.Errorf("%s: tekil yol değişti", c.name)
		}
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %s yok:\n%s", c.name, w, got)
			}
		}
		for _, n := range c.not {
			if strings.Contains(got, n) {
				t.Errorf("%s: %s olmamalı:\n%s", c.name, n, got)
			}
		}
	}
}

func TestCHHistogramWhereServices(t *testing.T) {
	from, to := time.Unix(1_700_000_000, 0), time.Unix(1_700_003_600, 0)
	cases := []struct {
		name      string
		f         Filter
		want, not string
		nArgs     int
	}{
		{"tekil", Filter{Service: "orders-api", Services: []string{"a", "b"}}, "service_name = ?", "service_name IN", 1},
		{"küme", Filter{Services: []string{"payments-api", "orders-api"}}, "service_name IN (?,?)", "service_name = ?", 2},
	}
	for _, c := range cases {
		wc, args := chLogsHistogramWhere(c.f, from, to)
		if !strings.Contains(wc, c.want) || strings.Contains(wc, c.not) {
			t.Errorf("%s: %s", c.name, wc)
		}
		svc := 0
		for _, a := range args {
			if s, ok := a.(string); ok && (s == "orders-api" || s == "payments-api" || s == "a" || s == "b") {
				svc++
			}
		}
		if svc != c.nArgs {
			t.Errorf("%s: servis bağı %d, istenen %d (%v)", c.name, svc, c.nArgs, args)
		}
	}
}
