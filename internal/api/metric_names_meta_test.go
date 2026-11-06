package api

// v0.10.1837 — metadata ladder, hiddenByRule and /api/metrics/capabilities
// (Metrics Explorer Faz 1 dilim 1.3). Kabul ölçütü (audit §7): "VM'de
// typeSource doğru; CH'de catalog".

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/pipeline"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestVMLadder(t *testing.T) {
	md := map[string]vmetrics.MetricMetadata{
		"orders_created_total":                 {Type: "counter", Help: "Orders created."},
		"http_server_request_duration_seconds": {Type: "histogram", Help: "Request latency.", Unit: "seconds"},
		"queue_depth":                          {Type: "unknown"},
	}
	row := func(name string) chstore.MetricInfo {
		u, typ := vmetrics.DescribeMetricName(name)
		return chstore.MetricInfo{Name: name, Unit: u, Type: typ}
	}
	cases := []struct {
		name, wantType, wantUnit, wantSrc, wantHelp string
	}{
		{"orders_created_total", "sum", "", "vm-metadata", "Orders created."},
		{"http_server_request_duration_seconds_bucket", "histogram", "s", "vm-metadata", "Request latency."},
		// a histogram's _count / _sum parts are counters, not histograms
		{"http_server_request_duration_seconds_count", "sum", "s", "vm-metadata", "Request latency."},
		{"http_server_request_duration_seconds_sum", "sum", "s", "vm-metadata", "Request latency."},
		// metadata present but says nothing → falls to inference
		{"queue_depth", "", "", "", ""},
		{"payments_total", "sum", "", "inferred", ""},
		{"process_resident_memory_bytes", "", "B", "inferred", ""},
		{"branch_teller_sessions", "", "", "", ""},
	}
	for _, tc := range cases {
		got := vmLadder(row(tc.name), md)
		if got.Type != tc.wantType || got.Unit != tc.wantUnit || got.TypeSource != tc.wantSrc || got.Help != tc.wantHelp {
			t.Errorf("%s: got type=%q unit=%q src=%q help=%q", tc.name, got.Type, got.Unit, got.TypeSource, got.Help)
		}
	}
	// metadata unavailable entirely → every rung is inference
	if got := vmLadder(row("orders_created_total"), nil); got.TypeSource != "inferred" || got.Type != "sum" {
		t.Errorf("no metadata: %+v", got)
	}
}

func TestAnnotateMetricNamesCHIsCatalog(t *testing.T) {
	rows := annotateMetricNames([]chstore.MetricInfo{
		{Name: "jvm.memory.used", Description: "Heap used.", Unit: "By", Type: "gauge"},
		{Name: "raw.scan.only"},
	}, metricSourceCH, nil, func(n string) bool { return n == "raw.scan.only" })
	if rows[0].TypeSource != "catalog" || rows[0].Help != "Heap used." || rows[0].HiddenByRule {
		t.Errorf("catalog row: %+v", rows[0])
	}
	if rows[1].TypeSource != "" || !rows[1].HiddenByRule {
		t.Errorf("bare row: %+v", rows[1])
	}
	b, _ := json.Marshal(rows[1])
	if strings.Contains(string(b), "typeSource") || strings.Contains(string(b), `"help"`) {
		t.Errorf("empty ladder fields must be omitted: %s", b)
	}
}

func TestVMMetadataKeyHashesAllInputs(t *testing.T) {
	a := vmMetadataKey("http://vm-a:8428", 5000)
	if a == vmMetadataKey("http://vm-b:8428", 5000) || a == vmMetadataKey("http://vm-a:8428", 100) || a != vmMetadataKey("http://vm-a:8428", 5000) {
		t.Fatal("metadata key must vary with base URL and limit, and be stable")
	}
}

func namesVM(t *testing.T, metadataBody string) *fakeVM {
	return newFakeVM(t, func(path string, v url.Values) (int, string) {
		switch path {
		case "/api/v1/label/__name__/values":
			return 200, `{"status":"success","data":["http_server_request_duration_seconds_bucket","orders_created_total","secret_health_probe"]}`
		case "/api/v1/metadata":
			return 200, metadataBody
		}
		return fakeVMDefault(path, v)
	})
}

type namesPage struct {
	Names []metricNameRow `json:"names"`
}

func TestMetricNamesCarryTheLadder(t *testing.T) {
	fv := namesVM(t, `{"status":"success","data":{"orders_created_total":[{"type":"counter","help":"Orders created.","unit":""}]}}`)
	s := fv.server()
	get := func(q string) namesPage {
		rec := httptest.NewRecorder()
		s.getMetricNames(rec, httptest.NewRequest("GET", "/api/metrics/names?limit=50&q="+q, nil))
		var p namesPage
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("%v: %s", err, rec.Body)
		}
		return p
	}
	p := get("")
	by := map[string]metricNameRow{}
	for _, r := range p.Names {
		by[r.Name] = r
	}
	if r := by["orders_created_total"]; r.TypeSource != "vm-metadata" || r.Help != "Orders created." {
		t.Errorf("metadata rung: %+v", r)
	}
	if r := by["http_server_request_duration_seconds_bucket"]; r.TypeSource != "inferred" || r.Type != "histogram" {
		t.Errorf("inferred rung: %+v", r)
	}
	get("orders") // second request: metadata comes from cache
	if n := len(fv.requestsTo("/api/v1/metadata")); n != 1 {
		t.Errorf("metadata fetched %d times, want 1 (cached)", n)
	}
}

func TestMetricNamesMetadataUnavailableIsSilent(t *testing.T) {
	fv := namesVM(t, `{"status":"success","data":{}}`)
	s := fv.server()
	rec := httptest.NewRecorder()
	s.getMetricNames(rec, httptest.NewRequest("GET", "/api/metrics/names?limit=50", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"typeSource":"inferred"`) || strings.Contains(rec.Body.String(), "vm-metadata") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestMetricNamesHiddenByRuleAdminOnly(t *testing.T) {
	fv := namesVM(t, `{"status":"success","data":{}}`)
	s := fv.server()
	eng := pipeline.New()
	if _, err := eng.Upsert(context.Background(), &pipelineTestStore{}, pipeline.Rule{
		Name: "hc", Kind: pipeline.KindDrop, Signal: pipeline.SignalMetrics, Enabled: true,
		When: pipeline.Condition{Key: "metric", Op: pipeline.OpMatches, Value: "secret_health.*"},
	}); err != nil {
		t.Fatal(err)
	}
	s.pipeline = eng
	call := func(role, q string) string {
		r := httptest.NewRequest("GET", "/api/metrics/names?limit=50"+q, nil)
		if role != "" {
			r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u-" + role, Role: role}))
		}
		rec := httptest.NewRecorder()
		s.getMetricNames(rec, r)
		return rec.Body.String()
	}
	for _, tc := range []struct {
		role, q string
		want    bool
	}{
		{auth.RoleViewer, "&includeHidden=1", false},
		{auth.RoleEditor, "&includeHidden=1", false},
		{auth.RoleAdmin, "", false},
		{auth.RoleAdmin, "&includeHidden=1", true},
	} {
		body := call(tc.role, tc.q)
		has := strings.Contains(body, "secret_health_probe")
		if has != tc.want {
			t.Errorf("role=%s q=%q: hidden name returned=%v, want %v", tc.role, tc.q, has, tc.want)
		}
		if tc.want && !strings.Contains(body, `"hiddenByRule":true`) {
			t.Errorf("admin includeHidden row not flagged: %s", body)
		}
	}
	// legacy array shape too
	r := httptest.NewRequest("GET", "/api/metrics/names?includeHidden=1", nil)
	r = r.WithContext(auth.ContextWithClaims(r.Context(), &auth.Claims{UserID: "u1", Role: auth.RoleViewer}))
	rec := httptest.NewRecorder()
	s.getMetricNames(rec, r)
	if strings.Contains(rec.Body.String(), "secret_health_probe") {
		t.Errorf("legacy shape leaked the hidden name to a viewer: %s", rec.Body)
	}
}

func TestCapabilitiesFor(t *testing.T) {
	vm := capabilitiesFor(metricSourceVM, true)
	if vm.Source != "vm" || vm.Language != "metricsql" || !vm.MetricsQL || !vm.InstantQuery ||
		vm.Metadata.VMMetadata != "available" || vm.Limits.MaxSeries != 1000 || !vm.Features["instantQuery"].Supported {
		t.Errorf("vm: %+v", vm)
	}
	ch := capabilitiesFor(metricSourceCH, false)
	if ch.Source != "clickhouse" || ch.Language != "promql-subset" || ch.MetricsQL || ch.InstantQuery ||
		len(ch.Metadata.Ladder) != 1 || ch.Metadata.Ladder[0] != "catalog" || ch.Metadata.VMMetadata != "" ||
		ch.Features["instantQuery"].Supported || ch.Features["instantQuery"].Reason == "" || ch.Limits.MaxRows != chstore.SpanMetricRowCap {
		t.Errorf("ch: %+v", ch)
	}
	if capabilitiesFor(metricSourceVM, false).Metadata.VMMetadata != "unavailable" {
		t.Error("vm without metadata must say unavailable")
	}
	if metricsCapabilitiesKey("vm", "http://a") == metricsCapabilitiesKey("vm", "http://b") ||
		metricsCapabilitiesKey("vm", "http://a") == metricsCapabilitiesKey("ch", "http://a") {
		t.Error("capabilities key must vary with source and base URL")
	}
}

func TestCapabilitiesHandler(t *testing.T) {
	fv := namesVM(t, `{"status":"success","data":{"up":[{"type":"gauge","help":"x"}]}}`)
	s := fv.server()
	rec := httptest.NewRecorder()
	s.getMetricsCapabilities(rec, httptest.NewRequest("GET", "/api/metrics/capabilities", nil))
	var c metricsCapabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("%v %s", err, rec.Body)
	}
	if c.Source != "vm" || c.Metadata.VMMetadata != "available" {
		t.Fatalf("caps %+v", c)
	}
	rec = httptest.NewRecorder()
	s.getMetricsCapabilities(rec, httptest.NewRequest("GET", "/api/metrics/capabilities?metricsrc=ch", nil))
	if !strings.Contains(rec.Body.String(), `"source":"clickhouse"`) {
		t.Fatalf("metricsrc=ch: %s", rec.Body)
	}
}
