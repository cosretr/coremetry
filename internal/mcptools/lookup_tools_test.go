package mcptools

// lookup_tools_test.go — v0.10.2449: endpoint_lookup / trace_where /
// repo_lookup / messaging_flow / downstream_calls sözleşme testleri + SAF
// yardımcılar (log ipucu çıkarıcısı dahil). Adlar sentetik.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

var lkAnchor = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)

func lkCtx() context.Context { return WithAnchor(context.Background(), lkAnchor) }

// ── log ipuçları ─────────────────────────────────────────────────────────

func TestExtractLogClues(t *testing.T) {
	log := `2026-11-22 10:41:07.123 ERROR [orders-api,4bf92f3577b34da6a3ce929d0e0e4736,00f067aa0ba902b7] call failed
traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01 spanId=00f067aa0ba902b7
org.springframework.web.client.ResourceAccessException: I/O error on POST request for "https://user:pw@partner-gw.example.com:8443/v1/orders?id=1": Read timed out
	at com.example.orders.OrderClient.call(OrderClient.java:42)
Caused by: java.net.SocketTimeoutException: Read timed out (host esb.internal.example.com)`
	c := ExtractLogClues(log)
	if len(c.TraceIDs) != 2 || c.TraceIDs[0] != "0af7651916cd43dd8448eb211c80319c" || c.TraceIDs[1] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("trace ids %v", c.TraceIDs)
	}
	if len(c.SpanIDs) != 2 || c.SpanIDs[0] != "b7ad6b7169203331" || c.SpanIDs[1] != "00f067aa0ba902b7" {
		t.Errorf("span ids %v", c.SpanIDs)
	}
	if len(c.URLs) != 1 || !strings.HasPrefix(c.URLs[0], "https://user:pw@partner-gw.example.com:8443/v1/orders") {
		t.Errorf("urls %v", c.URLs)
	}
	hosts := strings.Join(c.Hosts, ",")
	if !strings.Contains(hosts, "partner-gw.example.com") || !strings.Contains(hosts, "esb.internal.example.com") {
		t.Errorf("hosts %v", c.Hosts)
	}
	for _, h := range c.Hosts {
		if strings.HasPrefix(h, "java.") || strings.HasPrefix(h, "org.") || strings.HasPrefix(h, "com.example.orders") {
			t.Errorf("paket adı host sayıldı: %q", h)
		}
	}
	exc := strings.Join(c.Exceptions, ",")
	if !strings.Contains(exc, "org.springframework.web.client.ResourceAccessException") || !strings.Contains(exc, "java.net.SocketTimeoutException") {
		t.Errorf("exceptions %v", c.Exceptions)
	}
	if !c.Timeout {
		t.Error("timeout işareti yok")
	}
	if !LooksLikePastedLog(log) {
		t.Error("log yapıştırması tanınmadı")
	}
	calls := ClueToolCalls(c)
	if len(calls) < 3 || calls[0] != "trace_where(trace_id=0af7651916cd43dd8448eb211c80319c)" {
		t.Errorf("öneriler %v", calls)
	}
	if ExtractLogClues("orders-api sağlığı nasıl?").Empty() == false {
		t.Error("düz soruda ipucu olmamalı")
	}
	if LooksLikePastedLog("orders-api servisinin url'i nedir") {
		t.Error("kısa soru log sayıldı")
	}
	if got := ExtractLogClues("00000000000000000000000000000000"); len(got.TraceIDs) != 0 {
		t.Errorf("sıfır trace id kabul edildi: %v", got.TraceIDs)
	}
}

// ── endpoint_lookup ──────────────────────────────────────────────────────

type fakeLookup struct {
	routes   []chstore.EndpointRouteRow
	obs      chstore.EndpointObservedResult
	obsQ     *chstore.EndpointObservedQuery
	routesQ  []string
	callers  []chstore.ExternalCaller
	trace    []chstore.SpanRow
	whereQ   *chstore.WhereQuery
	where    chstore.WhereResult
	attrs    map[string]string
	flow     []chstore.MessagingFlowRow
	edges    []chstore.DownstreamEdge
	routeErr error
}

func (f *fakeLookup) EndpointEntryRoutes(_ context.Context, services []string, frag string, _, _ time.Time, _ int) ([]chstore.EndpointRouteRow, error) {
	f.routesQ = append(append([]string{}, services...), "frag="+frag)
	return f.routes, f.routeErr
}
func (f *fakeLookup) EndpointObserved(_ context.Context, q chstore.EndpointObservedQuery) (chstore.EndpointObservedResult, error) {
	f.obsQ = &q
	r := f.obs
	r.From, r.To = q.From, q.To
	return r, nil
}
func (f *fakeLookup) OutboundHostCallers(context.Context, string, time.Time, time.Time) ([]chstore.ExternalCaller, error) {
	return f.callers, nil
}
func (f *fakeLookup) GetTrace(context.Context, string) ([]chstore.SpanRow, error) {
	return f.trace, nil
}
func (f *fakeLookup) TraceWhere(_ context.Context, q chstore.WhereQuery) (chstore.WhereResult, error) {
	f.whereQ = &q
	r := f.where
	r.From, r.To = q.From, q.To
	return r, nil
}
func (f *fakeLookup) ServiceRepoAttrs(context.Context, string, time.Time, time.Time) (map[string]string, error) {
	return f.attrs, nil
}
func (f *fakeLookup) MessagingFlow(context.Context, string, string, time.Time, time.Time, int) ([]chstore.MessagingFlowRow, error) {
	return f.flow, nil
}
func (f *fakeLookup) DownstreamEdges(context.Context, string, time.Time, time.Time, int) ([]chstore.DownstreamEdge, error) {
	return f.edges, nil
}

func lkJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEndpointLookupDomainToPods(t *testing.T) {
	f := &fakeLookup{
		obs: chstore.EndpointObservedResult{Rows: []chstore.EndpointObservedRow{{Service: "orders-api", Cluster: "ocp-a", Host: "api.example.com",
			Method: "GET", Path: "/v1/orders/{id}", Route: "/v1/orders/{id}", Calls: 120, Errors: 3, P95Ms: 84.5, Pods: []string{"orders-api-7d9f-x1"}}}},
		callers: []chstore.ExternalCaller{{Service: "mobile-bff", Calls: 40}},
	}
	d := Deps{EntityNeighbours: func(_ context.Context, c EntityCandidate) (*EntityNeighbours, error) {
		if c.Name != "orders-api" || c.Kind != "service" {
			t.Errorf("entity adayı %+v", c)
		}
		return &EntityNeighbours{Pods: []string{"pod:ocp-a/shop/orders-api-7d9f-x1", "pod:ocp-b/shop/orders-api-55c-q2", "wl:ocp-a/shop/Deployment/orders-api"}, PodCount: 2, PodSource: "live"}, nil
	}}
	out, err := runEndpointLookup(lkCtx(), d, f, endpointLookupArgs{Domain: "https://API.example.com:443/"})
	if err != nil {
		t.Fatal(err)
	}
	if f.obsQ == nil || f.obsQ.Host != "api.example.com" || len(f.obsQ.Services) != 0 {
		t.Fatalf("observed sorgusu %+v", f.obsQ)
	}
	if f.routesQ != nil {
		t.Errorf("yalnız domain'de rota okuması koşmamalı: %v", f.routesQ)
	}
	js := lkJSON(t, out)
	for _, want := range []string{`"pods_by_cluster":{"ocp-a":["orders-api-7d9f-x1"],"ocp-b":["orders-api-55c-q2"]}`, `"host":"api.example.com"`,
		`"external_callers":[{"service":"mobile-bff"`, `"pod_source":"live"`} {
		if !strings.Contains(js, want) {
			t.Errorf("çıktıda %s yok: %s", want, js)
		}
	}
}

func TestEndpointLookupServiceREDPrefersVM(t *testing.T) {
	f := &fakeLookup{routes: []chstore.EndpointRouteRow{
		{Service: "orders-api", Name: "GET /v1/orders", Route: "/v1/orders", Calls: 1000, Errors: 10, P95Ms: 90},
		{Service: "orders-api", Name: "OP_ORDER_QUERY_REST", Calls: 50, Errors: 0, P95Ms: 12},
	}}
	called := 0
	d := Deps{EndpointMetricRED: func(_ context.Context, svc, env, route, method string, _, _ time.Time) (EndpointREDResult, error) {
		called++
		return EndpointREDResult{Rate: []EvChartSeries{{Name: "rps", Points: [][2]float64{{1, 2}, {2, 4}}}},
			Latency: []EvChartSeries{{Name: "p95", Points: [][2]float64{{1, 0.1}, {2, 0.1}}}}}, nil
	}}
	out, err := runEndpointLookup(lkCtx(), d, f, endpointLookupArgs{Service: "orders-api"})
	if err != nil {
		t.Fatal(err)
	}
	red := out["red"].([]elREDRow)
	if len(red) != 2 || called != 1 {
		t.Fatalf("red %+v (vm çağrısı %d)", red, called)
	}
	if red[0].Source != "vm (unsampled)" || *red[0].RPS != 3 || *red[0].P95Ms != 100 {
		t.Errorf("vm satırı %+v", red[0])
	}
	if red[1].Source != "spanmetrics_1m (sampled spans)" || *red[1].Calls != 50 || red[1].Route != "OP_ORDER_QUERY_REST" {
		t.Errorf("rota yoksa MV satırı beklenir: %+v", red[1])
	}
	if f.obsQ == nil || len(f.obsQ.Services) != 1 {
		t.Errorf("servis önekli observed beklenir: %+v", f.obsQ)
	}
}

func TestEndpointLookupOperationScopesObserved(t *testing.T) {
	f := &fakeLookup{routes: []chstore.EndpointRouteRow{{Service: "orders-api", Name: "OP_ORDER_QUERY_REST", Route: "/api/order/query", Calls: 5}}}
	if _, err := runEndpointLookup(lkCtx(), Deps{}, f, endpointLookupArgs{Operation: "OP_ORDER_QUERY_REST"}); err != nil {
		t.Fatal(err)
	}
	if f.obsQ == nil || strings.Join(f.obsQ.Services, ",") != "orders-api" || f.obsQ.URL != "" {
		t.Errorf("operasyon → rotanın servisi önek olmalı: %+v", f.obsQ)
	}
	if _, err := runEndpointLookup(lkCtx(), Deps{}, f, endpointLookupArgs{}); err == nil {
		t.Error("boş argüman adlandırılmış hata dönmeli")
	}
}

func TestEndpointLookupUnreadIsNotNone(t *testing.T) {
	f := &fakeLookup{routeErr: errors.New("timeout")}
	out, err := runEndpointLookup(lkCtx(), Deps{}, f, endpointLookupArgs{Service: "orders-api"})
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := out["unread"].([]string); len(u) != 1 || !strings.HasPrefix(u[0], "routes:") {
		t.Errorf("unread %v", out["unread"])
	}
}

func TestSplitLookupURL(t *testing.T) {
	h, p := splitLookupURL("https://api.example.com/v1/orders?id=3")
	if h != "api.example.com" || p != "/v1/orders" {
		t.Errorf("%q %q", h, p)
	}
	if h, p = splitLookupURL("/v1/orders"); h != "" || p != "/v1/orders" {
		t.Errorf("%q %q", h, p)
	}
}

// ── trace_where ──────────────────────────────────────────────────────────

func lkSpan(id, parent, svc, kind, name, status string, startMs, endMs int64, attrs map[string]string) chstore.SpanRow {
	return chstore.SpanRow{SpanID: id, ParentSpanID: parent, ServiceName: svc, Kind: kind, Name: name, StatusCode: status,
		StartTime: startMs * 1e6, EndTime: endMs * 1e6, DurationMs: float64(endMs - startMs), HostName: svc + "-pod",
		Attributes: attrs, ResourceAttributes: map[string]string{"k8s.cluster.name": "ocp-a"}}
}

func TestTraceWhereSummaryOriginTimeoutExternal(t *testing.T) {
	spans := []chstore.SpanRow{
		lkSpan("a", "", "mobile-bff", "server", "GET /orders", "error", 0, 1000, nil),
		lkSpan("b", "a", "mobile-bff", "client", "GET", "error", 10, 990, map[string]string{"server.address": "orders-api"}),
		lkSpan("c", "b", "orders-api", "server", "GET /orders", "error", 20, 980, nil),
		lkSpan("d", "c", "orders-api", "client", "POST", "error", 100, 900, map[string]string{"server.address": "osb.example.com", "url.full": "https://osb.example.com/svc"}),
		lkSpan("e", "c", "orders-api", "producer", "publish", "ok", 905, 950, map[string]string{"messaging.destination.name": "payments-topic"}),
	}
	spans[3].StatusMessage = "Read timed out"
	out := traceWhereSummary("4bf92f3577b34da6a3ce929d0e0e4736", spans, "osb")
	origin := out["error_origin"].([]twRow)
	if len(origin) != 1 || origin[0].SpanID != "d" || origin[0].Peer != "osb.example.com" || origin[0].Pod != "orders-api-pod" || origin[0].Cluster != "ocp-a" {
		t.Fatalf("köken %+v", origin)
	}
	if to := out["timeouts"].([]twRow); len(to) != 1 || to[0].SpanID != "d" {
		t.Errorf("timeouts %+v", to)
	}
	ext := out["external_calls"].([]twRow)
	if len(ext) != 2 || ext[0].SpanID != "d" || ext[1].SpanID != "e" {
		t.Errorf("dış çağrılar %+v (b servisli çağrı olmamalı)", ext)
	}
	if pm := out["peer_matches"].([]twRow); len(pm) != 1 || pm[0].SpanID != "d" {
		t.Errorf("peer eşleşmeleri %+v", pm)
	}
	js := lkJSON(t, out)
	// c: 960 ms − çocuklar [100,900] ∪ [905,950] = 960 − 845 = 115 ms öz süre.
	if !strings.Contains(js, `"span":"GET /orders","kind":"server","duration_ms":960,"self_ms":115`) {
		t.Errorf("öz süre: %s", js)
	}
	if !strings.Contains(js, `"messaging":[{"destination":"payments-topic","spans":1,"total_ms":45,"max_ms":45}]`) {
		t.Errorf("mesajlaşma: %s", js)
	}
}

func TestSpanSelfMsOverlap(t *testing.T) {
	p := lkSpan("p", "", "s", "server", "x", "ok", 0, 100, nil)
	kids := []chstore.SpanRow{lkSpan("1", "p", "s", "client", "a", "ok", 10, 50, nil), lkSpan("2", "p", "s", "client", "b", "ok", 40, 60, nil),
		lkSpan("3", "p", "s", "client", "c", "ok", 90, 130, nil)}
	if got := spanSelfMs(p, kids); got != 40 { // kapsanan [10,60] + [90,100] = 60
		t.Errorf("öz süre %v, 40 bekleniyordu", got)
	}
}

func TestTraceWhereModes(t *testing.T) {
	f := &fakeLookup{}
	if _, err := runTraceWhere(lkCtx(), f, traceWhereArgs{TraceID: "xyz"}); err == nil {
		t.Error("geçersiz trace id reddedilmeli")
	}
	out, err := runTraceWhere(lkCtx(), f, traceWhereArgs{TraceID: "4BF92F3577B34DA6A3CE929D0E0E4736"})
	if err != nil || out["found"] != false {
		t.Fatalf("bulunamayan trace dürüst found=false dönmeli: %v %v", out, err)
	}
	f.where = chstore.WhereResult{Rows: []chstore.WhereRow{{Pod: "orders-api-x", Cluster: "ocp-b", Span: "POST", Kind: "client", Peer: "osb", Spans: 4, Errors: 4, Timeouts: 3, TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}}}
	out, err = runTraceWhere(lkCtx(), f, traceWhereArgs{Service: "orders-api", Mode: "slow", RangeS: 999999, Peer: "osb"})
	if err != nil {
		t.Fatal(err)
	}
	if f.whereQ.Mode != chstore.WhereModeSlow || f.whereQ.MinMs != twDefaultSlowMs || f.whereQ.Peer != "osb" || f.whereQ.To.Sub(f.whereQ.From) != twMaxRangeS*time.Second {
		t.Errorf("sorgu %+v", f.whereQ)
	}
	if out["group_count"] != 1 || out["min_ms"] != int64(twDefaultSlowMs) {
		t.Errorf("çıktı %v", out)
	}
	if _, err := runTraceWhere(lkCtx(), f, traceWhereArgs{Service: "orders-api", Mode: "bogus"}); err == nil {
		t.Error("bilinmeyen kip reddedilmeli")
	}
	if _, err := runTraceWhere(lkCtx(), f, traceWhereArgs{}); err == nil {
		t.Error("argümansız çağrı adlandırılmış hata dönmeli")
	}
}

// ── repo_lookup ──────────────────────────────────────────────────────────

func TestRepoLookupOrder(t *testing.T) {
	f := &fakeLookup{attrs: map[string]string{"vcs.repository.url.full": "git@git.example.com:shop/orders-api.git", "vcs.ref.head.name": "main"}}
	d := Deps{RepoSources: func(context.Context, string) RepoSourceReport {
		return RepoSourceReport{Candidates: []RepoCandidate{{Source: "azure_devops", URL: "https://dev.example.com/c/SHOP/_git/orders-api", Verified: false}},
			Checked: []RepoSourceCheck{{Source: "azure_devops", Status: RepoStatusFound}}}
	}}
	out, err := runRepoLookup(lkCtx(), d, f, repoLookupArgs{Service: "orders-api"})
	if err != nil {
		t.Fatal(err)
	}
	if out["repo_url"] != "https://git.example.com/shop/orders-api" || out["source"] != "resource_attributes" || out["verified"] != true {
		t.Errorf("öznitelik önce gelmeli: %v", out)
	}
}

func TestRepoLookupUnknownHonest(t *testing.T) {
	out, err := runRepoLookup(lkCtx(), Deps{}, &fakeLookup{attrs: map[string]string{}}, repoLookupArgs{Service: "orders-api"})
	if err != nil {
		t.Fatal(err)
	}
	if out["found"] != false || out["repo_url"] != "unknown" || !strings.Contains(out["note"].(string), "vcs.repository.url.full") {
		t.Errorf("dürüst unknown + açma yolu beklenir: %v", out)
	}
	js := lkJSON(t, out["sources_checked"])
	if !strings.Contains(js, `"status":"absent"`) || !strings.Contains(js, `"status":"not_configured"`) {
		t.Errorf("kontrol listesi %s", js)
	}
}

func TestRepoLookupPodViaEntity(t *testing.T) {
	d := Deps{PodServices: func(_ context.Context, pod string) ([]string, error) {
		if pod == "orders-api-7d9f-x1" {
			return []string{"orders-api"}, nil
		}
		return []string{"a", "b"}, nil
	}}
	out, err := runRepoLookup(lkCtx(), d, &fakeLookup{}, repoLookupArgs{Pod: "orders-api-7d9f-x1"})
	if err != nil || out["service"] != "orders-api" || out["service_from_pod"] != "entity model" {
		t.Fatalf("pod → servis: %v %v", out, err)
	}
	out, _ = runRepoLookup(lkCtx(), d, &fakeLookup{}, repoLookupArgs{Pod: "shared"})
	if out["found"] != false || out["candidates_services"] == nil {
		t.Errorf("belirsiz pod sorulmalı: %v", out)
	}
	if _, err := runRepoLookup(lkCtx(), Deps{}, &fakeLookup{}, repoLookupArgs{Pod: "x"}); err == nil {
		t.Error("entity yoksa pod girişi adlandırılmış hata dönmeli")
	}
}

func TestRepoWebURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@git.example.com:shop/orders-api.git":           "https://git.example.com/shop/orders-api",
		"https://token@git.example.com/shop/orders-api.git": "https://git.example.com/shop/orders-api",
		"https://dev.example.com/c/SHOP/_git/orders-api":    "https://dev.example.com/c/SHOP/_git/orders-api",
	} {
		if got := RepoWebURL(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

// ── messaging_flow / downstream_calls ────────────────────────────────────

type fakeMetricSrc struct {
	MetricSource
	series []chstore.SpanMetricSeries
	names  []string
}

func (f *fakeMetricSrc) QueryMetric(_ context.Context, q chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	f.names = append(f.names, q.Name)
	return f.series, nil
}

func TestMessagingFlowGroupsAndLag(t *testing.T) {
	f := &fakeLookup{flow: []chstore.MessagingFlowRow{
		{System: "kafka", Broker: "kafka-a", Destination: "payments-topic", Service: "orders-api", Role: "producer", Spans: 100, Errors: 1, AvgMs: 2, P95Ms: 5, Pods: []string{"orders-api-x"}},
		{System: "kafka", Broker: "kafka-a", Destination: "payments-topic", Service: "payments-worker", Role: "consumer", Spans: 100, Errors: 10, AvgMs: 40, P95Ms: 120},
	}}
	ms := &fakeMetricSrc{series: []chstore.SpanMetricSeries{{GroupKey: []string{"payments-topic"}, Points: []chstore.SpanMetricPoint{{Value: 900}, {Value: 4200}, {Value: 3100}}}}}
	out, err := runMessagingFlow(lkCtx(), Deps{Metrics: ms}, f, messagingFlowArgs{Topic: "payments"})
	if err != nil {
		t.Fatal(err)
	}
	js := lkJSON(t, out)
	for _, want := range []string{`"producers":[{"service":"orders-api","spans":100,"error_pct":1`, `"consumers":[{"service":"payments-worker","spans":100,"error_pct":10`,
		`"lag":[{"service":"payments-worker","topic":"payments-topic","last_records":3100,"max_records":4200}]`} {
		if !strings.Contains(js, want) {
			t.Errorf("çıktıda %s yok: %s", want, js)
		}
	}
	if len(ms.names) != 1 || ms.names[0] != hcKafkaLagMetric {
		t.Errorf("lag sorgusu yalnız tüketici için, katalog adıyla: %v", ms.names)
	}
	out, _ = runMessagingFlow(lkCtx(), Deps{}, f, messagingFlowArgs{Service: "orders-api"})
	if s, _ := out["lag_status"].(string); !strings.HasPrefix(s, "not_configured") || out["lag"] != nil {
		t.Errorf("metrik kaynağı yokken lag değeri olmamalı: %v", out)
	}
	if _, err := runMessagingFlow(lkCtx(), Deps{}, f, messagingFlowArgs{}); err == nil {
		t.Error("argümansız çağrı reddedilmeli")
	}
}

func TestDownstreamCalls(t *testing.T) {
	f := &fakeLookup{edges: []chstore.DownstreamEdge{{Target: "payments-api", Kind: "service", Calls: 200, Errors: 20, AvgMs: 33.3}}}
	d := Deps{DownstreamRED: func(context.Context, string, time.Time, time.Time) (runtimesig.DownstreamReport, error) {
		return runtimesig.DownstreamReport{Metric: "http_client_request_duration_seconds", Targets: []runtimesig.TargetRED{
			{Address: "payments-api:8080", Service: "payments-api", RPS: 3.5, P95Ms: 410, ErrorPct: 9.5, DominantError: "504", DominantErrorPct: 80},
			{Address: "x:80", RPS: 1, P95Ms: -1}}}, nil
	}}
	out, err := runDownstreamCalls(lkCtx(), d, f, downstreamCallsArgs{Service: "orders-api"})
	if err != nil {
		t.Fatal(err)
	}
	js := lkJSON(t, out)
	for _, want := range []string{`"error_pct":10`, `"address":"payments-api:8080","service":"payments-api","rps":3.5,"p95_ms":410,"error_pct":9.5,"dominant_error":"504"`,
		`"address":"x:80","rps":1,"error_pct":0`} {
		if !strings.Contains(js, want) {
			t.Errorf("çıktıda %s yok: %s", want, js)
		}
	}
	out, _ = runDownstreamCalls(lkCtx(), Deps{}, f, downstreamCallsArgs{Service: "orders-api"})
	if s, _ := out["vm_status"].(string); !strings.HasPrefix(s, "not_configured") {
		t.Errorf("vm yokken durum söylenmeli: %v", out)
	}
}
