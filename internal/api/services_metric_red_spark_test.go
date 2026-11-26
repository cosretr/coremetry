package api

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestServicesREDSparkPairs(t *testing.T) {
	got := servicesREDSparkPairs("svc-b, svc-a,svc-c,svc-a,,svc-d", "http,http,grpc_legacy,http,spans,bogus")
	if len(got) != 2 || strings.Join(got[redFamilyHTTP], ",") != "svc-a,svc-b" || strings.Join(got[redFamilyGRPCLegacy], ",") != "svc-c" {
		t.Fatalf("pairs = %v", got)
	}
	if p := servicesREDSparkPairs("svc-a,svc-b", "http"); len(p[redFamilyHTTP]) != 1 {
		t.Fatalf("eksik kaynak atlanmalı: %v", p)
	}
}

func TestServicesREDSparkKeyHashesAllInputs(t *testing.T) {
	to := time.Now()
	from := to.Add(-time.Hour)
	p := map[string][]string{redFamilyHTTP: {"svc-a"}}
	a := servicesREDSparkKey(p, "", "", from, to, 80, rd0LM(), false)
	for _, b := range []string{
		servicesREDSparkKey(map[string][]string{redFamilyHTTPLegacy: {"svc-a"}}, "", "", from, to, 80, rd0LM(), false),
		servicesREDSparkKey(map[string][]string{redFamilyHTTP: {"svc-b"}}, "", "", from, to, 80, rd0LM(), false),
		servicesREDSparkKey(p, "env-1", "", from, to, 80, rd0LM(), false),
		servicesREDSparkKey(p, "", "cl-1", from, to, 80, rd0LM(), false),
		servicesREDSparkKey(p, "", "", from, to, 120, rd0LM(), false),
		servicesREDSparkKey(p, "", "", from, to, 80, rd0LM(), true),
		servicesREDSparkKey(p, "", "", from.Add(-time.Hour), to, 80, rd0LM(), false),
	} {
		if a == b {
			t.Errorf("anahtar girdiyi taşımıyor: %s", a)
		}
	}
}

func TestServicesREDSparkStep(t *testing.T) {
	if s := servicesREDSparkStep(time.Hour, 80); s != 60 {
		t.Errorf("1h/80 → %d", s)
	}
	if s := servicesREDSparkStep(24*time.Hour, 80); s != 1080 {
		t.Errorf("24h/80 → %d", s)
	}
}

// sparkReader — aile başına birleşik aralık sorgusunu yanıtlar.
type sparkReader struct {
	famReader
	mu sync.Mutex
	qs []string
}

func (r *sparkReader) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	r.mu.Lock()
	r.qs = append(r.qs, q)
	r.mu.Unlock()
	pt := func(t int64, v float64) chstore.SpanMetricPoint { return chstore.SpanMetricPoint{Time: t, Value: v} }
	if strings.Contains(q, "http_server_request_duration_seconds") {
		return []chstore.SpanMetricSeries{
			{GroupKey: []string{`service_name="svc-a"`, `http_response_status_code="200"`, `cm_k="c"`}, Points: []chstore.SpanMetricPoint{pt(1, 90), pt(2, 80)}},
			{GroupKey: []string{`service_name="svc-a"`, `http_response_status_code="503"`, `cm_k="c"`}, Points: []chstore.SpanMetricPoint{pt(1, 10)}},
			{GroupKey: []string{`service_name="svc-a"`, `cm_k="p"`}, Points: []chstore.SpanMetricPoint{pt(1, 250), pt(2, 300)}},
			{GroupKey: []string{`service_name="svc-a"`, `cm_k="a"`}, Points: []chstore.SpanMetricPoint{pt(1, 40)}},
			{GroupKey: []string{`service_name="svc-x"`, `cm_k="c"`}, Points: []chstore.SpanMetricPoint{pt(1, 5)}}, // istenmedi
		}, nil
	}
	// gRPC: durum etiketi yok, gecikme yok → hata/gecikme eğrisi span'e düşer.
	return []chstore.SpanMetricSeries{
		{GroupKey: []string{`service_name="svc-c"`, `cm_k="c"`}, Points: []chstore.SpanMetricPoint{pt(1, 7)}},
	}, nil
}

// v0.10.2437: aile başına TEK aralık sorgusu; sayı/durum, p99, ortalama aynı
// cevaptan ayrışır; istenmeyen servis düşer; durum etiketi yoksa HasErrors=false.
func TestServicesREDSparkBuild(t *testing.T) {
	to := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	rd := &sparkReader{}
	s := &Server{}
	pairs := servicesREDSparkPairs("svc-a,svc-c", "http,grpc_legacy")
	res := s.buildServicesREDSpark(context.Background(), rd, pairs, "", "", to.Add(-time.Hour), to, 80, false)
	if res.Queries != 2 || len(rd.qs) != 2 {
		t.Fatalf("sorgu sayısı %d / %d (aile başına 1)", res.Queries, len(rd.qs))
	}
	for _, q := range rd.qs {
		if strings.Count(q, `"cm_k"`) != 3 || strings.Count(q, " or ") != 2 {
			t.Errorf("birleşik sorgu değil: %s", q)
		}
	}
	a, ok := res.Services["svc-a"]
	if !ok || len(a.Buckets) != 2 || !a.HasErrors || !a.HasLatency || a.Source != redFamilyHTTP {
		t.Fatalf("svc-a = %+v", a)
	}
	if b := a.Buckets[0]; b.T != 1 || b.Spans != 100 || b.Errs != 10 || b.P99Ms != 250 || b.AvgMs != 40 {
		t.Errorf("kova 1 = %+v", b)
	}
	if _, ok := res.Services["svc-x"]; ok {
		t.Error("istenmeyen servis cevapta olmamalı")
	}
	c := res.Services["svc-c"]
	if c.HasErrors || c.HasLatency || len(c.Buckets) != 1 || c.Buckets[0].Spans != 7 {
		t.Errorf("svc-c = %+v", c)
	}
}

// v0.10.2470 — mini-grafik sorgusu rate tabanlı (örnek sayısından bağımsız),
// pencere ≥ 180 sn, istek sayısı = rate × adım; increase(...[adım]) geri gelmez.
func TestServicesREDSparkQueryUsesRate(t *testing.T) {
	f := redFamilies[0]
	for _, step := range []int{60, 135, 1080} {
		q := servicesREDSparkQuery(f, "service_name", []string{"orders-api"}, "", step, false)
		if strings.Contains(q, "increase(") {
			t.Fatalf("step %d: increase() geri gelmiş: %s", step, q)
		}
		win := fmt.Sprintf("[%ds]", max(step, 180))
		if n := strings.Count(q, win); n < 4 {
			t.Fatalf("step %d: rate penceresi %s bekleniyordu (%d kez): %s", step, win, n, q)
		}
		if !strings.Contains(q, fmt.Sprintf(")) * %d", step)) {
			t.Fatalf("step %d: istek sayısı rate × adım olmalı: %s", step, q)
		}
	}
}

func TestServicesREDSparkGridAndFill(t *testing.T) {
	from, to := servicesREDSparkGrid(time.Unix(1_000_007, 0), time.Unix(1_000_700, 0), 135)
	if from.Unix()%135 != 0 || to.Unix()%135 != 0 {
		t.Fatalf("ızgara adıma hizalı değil: %d %d", from.Unix(), to.Unix())
	}
	ns := func(i int) int64 { return from.Add(time.Duration(i*135) * time.Second).UnixNano() }
	row := serviceREDSparkRow{Buckets: []serviceREDSparkBucket{{T: ns(0), Spans: 5}, {T: ns(3), Spans: 7}}}
	got := servicesREDSparkFill(row, from, to, 135)
	want := int(to.Sub(from)/(135*time.Second)) + 1
	if len(got.Buckets) != want {
		t.Fatalf("kova sayısı %d; want %d", len(got.Buckets), want)
	}
	if got.Buckets[0].Spans != 5 || got.Buckets[1].Spans != 0 || got.Buckets[3].Spans != 7 {
		t.Fatalf("doldurma yanlış: %+v", got.Buckets)
	}
	// Izgara dışı veri: dokunulmaz (sıfırla ezilmez).
	off := serviceREDSparkRow{Buckets: []serviceREDSparkBucket{{T: ns(0) + 1, Spans: 9}}}
	if g := servicesREDSparkFill(off, from, to, 135); len(g.Buckets) != 1 || g.Buckets[0].Spans != 9 {
		t.Fatalf("ızgara dışı veri ezildi: %+v", g.Buckets)
	}
}
