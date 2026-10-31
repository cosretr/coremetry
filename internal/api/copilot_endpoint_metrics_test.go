package api

// copilot_endpoint_metrics_test.go — CoSRE v2 dilim 3b (endpoint_metrics):
// skip gerekçeleri (VM yok / histogram yok / gRPC / rota etiketi yok), okuma
// hatası ≠ skip, top-10 PromQL'i, route + top uçtan uca (sahte VM), adım
// sonucunun data-tool-result kind skipped'e eşlenmesi.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/uistream"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestEndpointMetricSkip(t *testing.T) {
	http := redFamilies[0]
	grpc := redFamilies[1]
	cases := []struct {
		name     string
		probe    svcHTTPREDProbe
		in       svcREDInputs
		ok       bool
		wantSkip string
		wantErr  bool
	}{
		{"okunur", svcHTTPREDProbe{VM: true}, svcREDInputs{fam: http, routeLabelOK: true}, true, "", false},
		{"gRPC ailesi", svcHTTPREDProbe{VM: true}, svcREDInputs{fam: grpc, routeLabelOK: true}, true, "gRPC", false},
		{"rota etiketi yok", svcHTTPREDProbe{VM: true}, svcREDInputs{fam: http}, true, "http_route", false},
		{"VM yok", svcHTTPREDProbe{}, svcREDInputs{}, false, "yapılandırılmamış", false},
		{"histogram yok", svcHTTPREDProbe{VM: true, GRPCChecked: true}, svcREDInputs{}, false, "bulunamadı", false},
		{"etiket okuma hatası", svcHTTPREDProbe{VM: true, LabelErr: errors.New("deadline")}, svcREDInputs{}, false, "", true},
	}
	for _, c := range cases {
		skip, err := endpointMetricSkip(c.probe, c.in, c.ok)
		if (err != nil) != c.wantErr || (c.wantSkip == "") != (skip == "") || !strings.Contains(skip, c.wantSkip) {
			t.Errorf("%s: skip=%q err=%v", c.name, skip, err)
		}
		if err != nil && !strings.Contains(err.Error(), "okunamadı ≠ yok") {
			t.Errorf("%s: hata metni: %v", c.name, err)
		}
	}
}

func TestEndpointTopQueries(t *testing.T) {
	qs := endpointTopQueries(redFamilies[0], "orders-api", "service_name", `deployment_environment="prod"`, "http_response_status_code", 240)
	if len(qs) != 2 {
		t.Fatalf("sorgu sayısı %d", len(qs))
	}
	for _, want := range []string{"topk(10, sum by (http_route) (rate(", `service_name="orders-api"`, `deployment_environment="prod"`, "[240s]"} {
		if !strings.Contains(qs[0].Query, want) {
			t.Errorf("rate sorgusu %q içermiyor: %s", want, qs[0].Query)
		}
	}
	if !strings.Contains(qs[1].Query, "and on (http_route) topk(10,") || !strings.Contains(qs[1].Query, `http_response_status_code=~"5.."`) {
		t.Errorf("5xx sorgusu: %s", qs[1].Query)
	}
	if qs := endpointTopQueries(redFamilies[0], "orders-api", "service_name", "", "", 240); len(qs) != 1 {
		t.Errorf("durum etiketi yokken 5xx sorgusu olmamalı: %d", len(qs))
	}
}

// epReader — top kipinin iki sorgusunu ayıran sahte VM (redReader'ın üstüne).
type epReader struct {
	*redReader
	top, top5xx []chstore.SpanMetricSeries
	topErr      error
}

func (r *epReader) QueryPromQLRange(ctx context.Context, q string, from, to time.Time, step, mdp int) ([]chstore.SpanMetricSeries, error) {
	switch {
	case strings.HasPrefix(q, "topk("):
		r.redReader.mu.Lock()
		r.redReader.qs = append(r.redReader.qs, q)
		r.redReader.mu.Unlock()
		return r.top, r.topErr
	case strings.HasPrefix(q, "(100 * "):
		return r.top5xx, nil
	}
	return r.redReader.QueryPromQLRange(ctx, q, from, to, step, mdp)
}

func TestReadEndpointMetricRED(t *testing.T) {
	to := time.Date(2026, 10, 31, 12, 0, 30, 0, time.UTC)
	from := to.Add(-time.Hour)
	full := func() *epReader {
		return &epReader{redReader: &redReader{
			fakeChartReader: fakeChartReader{labels: map[string][]string{mcHTTPMetric: {"http_response_status_code", "http_route", "http_request_method", "service_name"}}},
			rate:            []chstore.SpanMetricSeries{{Points: svcREDPts(from, 12, 13, 12, 40)}},
			fiveXX:          []chstore.SpanMetricSeries{{Points: svcREDPts(from, 0, 0, 1, 3)}},
			lat:             []chstore.SpanMetricSeries{{GroupKey: []string{`phi="0.95"`}, Points: svcREDPts(from, 0.1, 0.1, 0.2, 0.5)}},
		},
			top: []chstore.SpanMetricSeries{
				{GroupKey: []string{`http_route="/api/orders"`}, Points: svcREDPts(from, 5, 6)},
				{GroupKey: []string{`http_route="/api/pay"`}, Points: svcREDPts(from, 1, 1)},
			},
			top5xx: []chstore.SpanMetricSeries{{GroupKey: []string{`http_route="/api/pay"`}, Points: svcREDPts(from, 50, 50)}},
		}
	}
	s := &Server{}
	ctx := context.Background()

	// Route kipi: 3 sorgu, rota eşleyicili; satır mcptools'ta.
	rd := full()
	r, err := s.readEndpointMetricRED(ctx, rd, true, "orders-api", "", "/api/orders/{id}", "GET", from, to)
	if err != nil || r.Skip != "" || len(r.Rate) != 1 || len(r.Err5xx) != 1 || len(r.Latency) != 1 || len(r.Queries) != 3 {
		t.Fatalf("route: err=%v %+v", err, r)
	}
	for _, q := range r.Queries {
		if !strings.Contains(q, `http_route="/api/orders/{id}"`) || !strings.Contains(q, `http_request_method="GET"`) {
			t.Errorf("rota eşleyicisi yok: %s", q)
		}
	}

	// Top kipi: rota başına seri, 5xx aynı kümeye bağlı.
	r, err = s.readEndpointMetricRED(ctx, full(), true, "orders-api", "", "", "", from, to)
	if err != nil || len(r.Rate) != 2 || len(r.Err5xx) != 1 || r.Err5xx[0].Name != "/api/pay" || len(r.Latency) != 0 {
		t.Fatalf("top: err=%v %+v", err, r)
	}

	// Rota etiketi yok → skipped (span'e düşüş yok).
	rd = full()
	rd.labels = map[string][]string{mcHTTPMetric: {"http_response_status_code", "service_name"}}
	if r, err = s.readEndpointMetricRED(ctx, rd, true, "orders-api", "", "", "", from, to); err != nil || !strings.Contains(r.Skip, "http_route") {
		t.Errorf("rota etiketi yok: err=%v skip=%q", err, r.Skip)
	}
	// Fiil etiketi yok → fiil düşer, not yazılır.
	rd = full()
	rd.labels = map[string][]string{mcHTTPMetric: {"http_response_status_code", "http_route", "service_name"}}
	r, err = s.readEndpointMetricRED(ctx, rd, true, "orders-api", "", "/api/orders", "POST", from, to)
	if err != nil || strings.Contains(strings.Join(r.Queries, " "), "http_request_method") || !strings.Contains(strings.Join(r.Notes, " "), "UYGULANMADI") {
		t.Errorf("fiil etiketi yok: err=%v %+v", err, r)
	}
	// VM yok → skipped.
	if r, _ = s.readEndpointMetricRED(ctx, nil, false, "orders-api", "", "", "", from, to); !strings.Contains(r.Skip, "yapılandırılmamış") {
		t.Errorf("VM yok: %q", r.Skip)
	}
	// İstek hızı okuma hatası → hata (okunamadı ≠ yok), skip DEĞİL.
	rd = full()
	rd.topErr = errors.New("vm down")
	if r, err = s.readEndpointMetricRED(ctx, rd, true, "orders-api", "", "", "", from, to); err == nil || r.Skip != "" || !strings.Contains(err.Error(), "okunamadı ≠ yok") {
		t.Errorf("okuma hatası: err=%v skip=%q", err, r.Skip)
	}
}

// TestEndpointMetricsStepKinds — araç gövdesi → step-result → data-tool-result:
// okunmadı = skipped + gerekçe; okundu = evidence + PromQL + page_url.
func TestEndpointMetricsStepKinds(t *testing.T) {
	skipped := `{"tool":"endpoint_metrics","service":"orders-api","mode":"top","window_s":3600,"skipped":true,"reason":"servisin VM ailesi gRPC"}`
	ev := map[string]any{"i": 1, "tool": mcptools.EndpointMetricsName, "ok": true}
	markV2Skipped(ev, mcptools.EndpointMetricsName, skipped)
	d := uistream.ToolResultData(ev)
	if d["kind"] != uistream.ToolResultKindSkipped || d["reason"] != "servisin VM ailesi gRPC" {
		t.Errorf("skipped: %v", d)
	}
	if _, ok := toolResultDeepLink(mcptools.EndpointMetricsName, skipped); ok {
		t.Error("skipped gövde link üretmemeli")
	}

	tool := mcptools.V2OnlyTools(mcptools.Deps{EndpointMetricRED: func(context.Context, string, string, string, string, time.Time, time.Time) (mcptools.EndpointREDResult, error) {
		return mcptools.EndpointREDResult{Queries: []string{"topk(10, sum by (http_route) (rate(x[240s])))"},
			Rate: []mcptools.EvChartSeries{{Name: "/api/orders", Points: [][2]float64{{0, 3}}}}}, nil
	}})
	var h func(context.Context, json.RawMessage) (any, error)
	for _, tl := range tool {
		if tl.Name == mcptools.EndpointMetricsName {
			h = tl.Handler
		}
	}
	if h == nil {
		t.Fatal("endpoint_metrics v2 kataloğunda yok")
	}
	out, err := h(context.Background(), json.RawMessage(`{"service":"orders-api"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	step := map[string]any{"i": 2, "tool": mcptools.EndpointMetricsName, "ok": true}
	attachStepEvidenceList(step, mcptools.EndpointMetricsName, string(b))
	if l, ok := toolResultDeepLink(mcptools.EndpointMetricsName, string(b)); ok {
		step["href"] = l.Href
	}
	markV2Skipped(step, mcptools.EndpointMetricsName, string(b))
	evJSON, _ := json.Marshal(step)
	var round map[string]any
	_ = json.Unmarshal(evJSON, &round)
	d = uistream.ToolResultData(round)
	if d["kind"] != uistream.ToolResultKindEvidence || !strings.Contains(d["query"].(string), "topk(10") ||
		!strings.HasPrefix(d["page_url"].(string), "/endpoints?") {
		t.Errorf("evidence: %v", d)
	}
}
