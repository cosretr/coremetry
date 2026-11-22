package mcptools

// sampling_label_test.go — v0.10.2457: CoSRE tools label span-derived
// numbers "örnekleme düzeltilmiş" when the chstore read was weighted and
// keep the old "sampled" texts otherwise; get_messaging_health reads lag
// through the messaging_flow resolver. Names are synthetic.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEndpointLookupWeightedLabels(t *testing.T) {
	for _, w := range []bool{false, true} {
		f := &fakeLookup{
			routes: []chstore.EndpointRouteRow{{Service: "svc-a", Name: "OP_A", Calls: 40, Errors: 2, P95Ms: 9, Weighted: w}},
			obs:    chstore.EndpointObservedResult{Rows: []chstore.EndpointObservedRow{{Service: "svc-a", Calls: 40}}, Weighted: w},
		}
		out, err := runEndpointLookup(lkCtx(), Deps{}, f, endpointLookupArgs{Service: "svc-a"})
		if err != nil {
			t.Fatal(err)
		}
		red := out["red"].([]elREDRow)
		js := lkJSON(t, out["notes"])
		if w {
			if red[0].Source != "spanmetrics_1m (örnekleme düzeltilmiş spans)" || !strings.Contains(js, "örnekleme düzeltilmiş (sampling-weighted)") || strings.Contains(js, "stored (sampled)") {
				t.Errorf("weighted labels: %s / %s", red[0].Source, js)
			}
		} else if red[0].Source != "spanmetrics_1m (sampled spans)" || !strings.Contains(js, "count stored (sampled) spans") {
			t.Errorf("mode-off labels changed: %s / %s", red[0].Source, js)
		}
	}
}

func TestTraceWhereWeightedNote(t *testing.T) {
	for _, w := range []bool{false, true} {
		f := &fakeLookup{where: chstore.WhereResult{Rows: []chstore.WhereRow{{Span: "GET", Spans: 3}}, Weighted: w}}
		out, err := runTraceWhere(lkCtx(), f, traceWhereArgs{Service: "svc-a"})
		if err != nil {
			t.Fatal(err)
		}
		first := out["notes"].([]string)[0]
		if got := strings.Contains(first, "örnekleme düzeltilmiş"); got != w || (!w && first != "counts are stored (sampled) spans — use them to locate pods/peers, not as request totals") {
			t.Errorf("w=%v note %q", w, first)
		}
	}
}

func TestMessagingFlowAndDownstreamWeightedLabels(t *testing.T) {
	f := &fakeLookup{
		flow:  []chstore.MessagingFlowRow{{System: "kafka", Destination: "topic-a", Service: "svc-a", Role: "producer", Spans: 10, Weighted: true}},
		edges: []chstore.DownstreamEdge{{Target: "svc-b", Kind: "service", Calls: 10, Weighted: true}},
	}
	out, err := runMessagingFlow(lkCtx(), Deps{}, f, messagingFlowArgs{Topic: "topic-a"})
	if err != nil {
		t.Fatal(err)
	}
	if s := out["source"].(string); !strings.Contains(s, "örnekleme düzeltilmiş") {
		t.Errorf("flow source %q", s)
	}
	if n := lkJSON(t, out["notes"]); !strings.Contains(n, "error_pct is over örnekleme düzeltilmiş") {
		t.Errorf("flow notes %s", n)
	}
	out, _ = runDownstreamCalls(lkCtx(), Deps{}, f, downstreamCallsArgs{Service: "svc-a"})
	if s := out["topology_source"].(string); !strings.Contains(s, "calls/errors örnekleme düzeltilmiş") {
		t.Errorf("topology source %q", s)
	}
	// Unweighted rows keep the v0.10.2449 texts.
	f.flow[0].Weighted, f.edges[0].Weighted = false, false
	out, _ = runMessagingFlow(lkCtx(), Deps{}, f, messagingFlowArgs{Topic: "topic-a"})
	if out["source"] != "messaging_caller_summary_5m (sampled spans, 5-minute buckets)" {
		t.Errorf("mode-off flow source %v", out["source"])
	}
	out, _ = runDownstreamCalls(lkCtx(), Deps{}, f, downstreamCallsArgs{Service: "svc-a"})
	if out["topology_source"] != "topology_edges_5m (sampled spans; avg over the window, p99 = worst 5-minute bucket)" {
		t.Errorf("mode-off topology source %v", out["topology_source"])
	}
}

// weightedDepPath — the fake graph read as sampling-weighted.
type weightedDepPath struct{ *fakeDepPath }

func (w weightedDepPath) DependencyHop(ctx context.Context, q chstore.DepHopQuery) ([]chstore.DepHopEdge, error) {
	es, err := w.fakeDepPath.DependencyHop(ctx, q)
	for i := range es {
		es[i].Weighted = true
	}
	return es, err
}

func TestDependencyPathWeightedLabels(t *testing.T) {
	g := dpGraph{}.add(dpE("svc-a", "svc-b", dpOK, dpOK))
	out, err := runDependencyPath(lkCtx(), Deps{}, weightedDepPath{&fakeDepPath{g: g}}, dependencyPathArgs{Service: "svc-a"}, lkAnchor)
	if err != nil {
		t.Fatal(err)
	}
	js := lkJSON(t, out)
	if !strings.Contains(js, `"source":"topology_edges_5m (örnekleme düzeltilmiş)"`) || !strings.Contains(js, dpWeightedNote) ||
		!strings.Contains(js, "edge values are örnekleme düzeltilmiş MV values") || strings.Contains(js, "mv rows are stored (sampled)") {
		t.Errorf("weighted dependency_path labels: %s", js)
	}
	out, _ = runDependencyPath(lkCtx(), Deps{}, &fakeDepPath{g: g}, dependencyPathArgs{Service: "svc-a"}, lkAnchor)
	if js := lkJSON(t, out); !strings.Contains(js, `"source":"topology_edges_5m (sampled)"`) || !strings.Contains(js, "edge values are sampled MV values") {
		t.Errorf("mode-off dependency_path labels changed: %s", js)
	}
}

func TestHealthReportWeightedLabels(t *testing.T) {
	src := hrFullSources()
	off := hrBuild(src, "")
	if off.TraceErrors.Source != "service_summary_5m (örneklenmiş span)" || !strings.Contains(off.ErrorOps.Source, "saklanan span'leri (örneklenmiş)") ||
		!strings.HasPrefix(off.Messaging.Source, "messaging_caller_summary_5m (örneklenmiş span)") {
		t.Errorf("mode-off labels changed: %q / %q / %q", off.TraceErrors.Source, off.ErrorOps.Source, off.Messaging.Source)
	}
	src.spanWeighted = func(time.Time, time.Time) bool { return true }
	prof, tot, edges, flow := src.errProfile, src.opTotals, src.edges, src.msgFlow
	src.errProfile = func(c context.Context, f, to time.Time) (chstore.ErrorProfile, error) {
		p, err := prof(c, f, to)
		p.Weighted = true
		return p, err
	}
	src.opTotals = func(c context.Context, n []string, f, to time.Time) (map[string]chstore.OpTotal, error) {
		m, err := tot(c, n, f, to)
		for k, v := range m {
			v.Weighted = true
			m[k] = v
		}
		return m, err
	}
	src.edges = func(c context.Context, f, to time.Time) ([]chstore.DownstreamEdge, error) {
		es, err := edges(c, f, to)
		for i := range es {
			es[i].Weighted = true
		}
		return es, err
	}
	src.msgFlow = func(c context.Context, f, to time.Time) ([]chstore.MessagingFlowRow, error) {
		rs, err := flow(c, f, to)
		for i := range rs {
			rs[i].Weighted = true
		}
		return rs, err
	}
	on := hrBuild(src, "")
	if on.TraceErrors.Source != "service_summary_5m (örnekleme düzeltilmiş span)" || on.TraceErrors.Note != hrWeightedSpanNote {
		t.Errorf("trace_errors %q / %q", on.TraceErrors.Source, on.TraceErrors.Note)
	}
	if !strings.Contains(on.ErrorOps.Source, "örnekleme düzeltilmiş") || strings.Contains(on.ErrorOps.Source, "(örneklenmiş)") {
		t.Errorf("error ops source %q", on.ErrorOps.Source)
	}
	if !strings.Contains(on.Technical.DownstreamStatus, "topology = span'ler (örnekleme düzeltilmiş)") {
		t.Errorf("technical %q", on.Technical.DownstreamStatus)
	}
	if !strings.HasPrefix(on.Messaging.Source, "messaging_caller_summary_5m (örnekleme düzeltilmiş span)") {
		t.Errorf("messaging %q", on.Messaging.Source)
	}
	if env := hrBuild(src, "prod"); env.TraceErrors.Source != "spans (örnekleme düzeltilmiş, ortam prod)" {
		t.Errorf("env source %q", env.TraceErrors.Source)
	}
}

// ── get_messaging_health lag (v0.10.2457) ───────────────────────────────

func TestMsgHealthLagServicesOf(t *testing.T) {
	rows := []MessagingHealthRow{
		{Destination: "t1", ConsumeCalls: 0, Callers: []string{"producer-only"}},
		{Destination: "t2", ConsumeCalls: 5, Callers: []string{"svc-a", "svc-b"}},
		{Destination: "t3", ConsumeCalls: 9, Callers: []string{"svc-b", "", "svc-c", "svc-d"}},
	}
	if got := strings.Join(msgHealthLagServicesOf(rows), ","); got != "svc-a,svc-b,svc-c" {
		t.Errorf("lag services %q", got)
	}
}

func TestMessagingHealthLagRead(t *testing.T) {
	rows := []MessagingHealthRow{{System: "kafka", Destination: "topic-a", ConsumeCalls: 10, Callers: []string{"svc-a"}}}
	from, to := lkAnchor.Add(-6*time.Hour), lkAnchor

	// No metric source → ölçülmüyor, no value.
	l := readMessagingHealthLag(lkCtx(), Deps{}, rows, from, to)
	if len(l.Rows) != 0 || !strings.HasPrefix(l.Status, "ölçülmüyor") {
		t.Errorf("no source: %+v", l)
	}
	// No consumer row → no query.
	ms := &fakeMetricSrc{}
	l = readMessagingHealthLag(lkCtx(), Deps{Metrics: ms}, []MessagingHealthRow{{Destination: "t", Callers: []string{"x"}}}, from, to)
	if len(ms.names) != 0 || !strings.HasPrefix(l.Status, "ölçülmüyor") {
		t.Errorf("no consumer: %+v %v", l, ms.names)
	}
	// Series present → rows, same metric as messaging_flow.
	ms = &fakeMetricSrc{series: []chstore.SpanMetricSeries{{GroupKey: []string{"topic-a"}, Points: []chstore.SpanMetricPoint{{Value: 10}, {Value: 70}, {Value: 40}}}}}
	l = readMessagingHealthLag(lkCtx(), Deps{Metrics: ms}, rows, from, to)
	if len(l.Rows) != 1 || l.Rows[0].Last != 40 || l.Rows[0].Max != 70 || l.Status != "" || len(ms.names) != 1 || ms.names[0] != hcKafkaLagMetric {
		t.Errorf("lag rows %+v status %q names %v", l.Rows, l.Status, ms.names)
	}
	body := messagingHealthPayload(MessagingHealthData{Rows: rows, Total: 1, Lag: l}, 3600)
	if body["lag"] == nil || body["lag_status"] != nil || !strings.Contains(body["lag_source"].(string), "svc-a") || body["lag_note"] != messagingLagNote {
		t.Errorf("payload with lag: %v", body)
	}
	// Series absent → ölçülmüyor, no lag key.
	l = readMessagingHealthLag(lkCtx(), Deps{Metrics: &fakeMetricSrc{}}, rows, from, to)
	body = messagingHealthPayload(MessagingHealthData{Rows: rows, Total: 1, Lag: l}, 3600)
	if body["lag"] != nil || !strings.HasPrefix(body["lag_status"].(string), "ölçülmüyor: "+hcKafkaLagMetric) {
		t.Errorf("payload without lag: %v", body)
	}
	// Guided path (Lag nil): payload carries no lag keys.
	if body = messagingHealthPayload(MessagingHealthData{Rows: rows, Total: 1}, 3600); body["lag"] != nil || body["lag_status"] != nil {
		t.Errorf("nil lag must add nothing: %v", body)
	}
}

func TestFinishMessagingLagCapsAndUnread(t *testing.T) {
	l := &MessagingLag{Services: []string{"svc-a"}, Unread: []string{"svc-a: boom"}}
	if finishMessagingLag(l).Status != "okunamadı (okunamadı ≠ lag yok)" {
		t.Errorf("all unread: %q", l.Status)
	}
	l = &MessagingLag{Services: []string{"svc-a"}}
	for i := 0; i < msgHealthLagRows+5; i++ {
		l.Rows = append(l.Rows, mfLag{Service: "svc-a", Topic: "t", Max: float64(i)})
	}
	finishMessagingLag(l)
	if len(l.Rows) != msgHealthLagRows || l.Rows[0].Max != float64(msgHealthLagRows+4) {
		t.Errorf("cap / order: %d first %v", len(l.Rows), l.Rows[0].Max)
	}
}
