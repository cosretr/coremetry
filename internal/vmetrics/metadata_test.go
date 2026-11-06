package vmetrics

// v0.10.1837 — /api/v1/metadata reader (Metrics Explorer Faz 1 dilim 1.3).
// Response shape ASSUMED from the Prometheus HTTP API docs (VM mirrors it):
// {"status":"success","data":{"<family>":[{"type":"counter","help":"…","unit":""}]}}.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDecodeMetadata(t *testing.T) {
	m, err := decodeMetadata([]byte(`{"http_requests_total":[{"type":"counter","help":"Requests.","unit":""},{"type":"gauge"}],"empty":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 || m["http_requests_total"].Type != "counter" || m["http_requests_total"].Help != "Requests." {
		t.Fatalf("decoded %+v", m)
	}
	for _, in := range []string{``, `null`, `{}`} {
		m, err := decodeMetadata([]byte(in))
		if err != nil || len(m) != 0 {
			t.Fatalf("%q → %v %v", in, m, err)
		}
	}
	if _, err := decodeMetadata([]byte(`[1]`)); err == nil {
		t.Fatal("garbage must error")
	}
}

func TestMetadataFamilyAndType(t *testing.T) {
	fam := map[string]string{
		"http_server_request_duration_seconds_bucket": "http_server_request_duration_seconds",
		"http_server_request_duration_seconds_count":  "http_server_request_duration_seconds",
		"http_server_request_duration_seconds_sum":    "http_server_request_duration_seconds",
		"orders_total":     "orders",
		"process_open_fds": "process_open_fds",
	}
	for in, want := range fam {
		if got := MetadataFamily(in); got != want {
			t.Errorf("MetadataFamily(%q) = %q, want %q", in, got, want)
		}
	}
	types := map[string]string{"counter": "sum", "GAUGE": "gauge", "histogram": "histogram", "gaugehistogram": "histogram", "summary": "summary", "unknown": "", "": "", "info": ""}
	for in, want := range types {
		if got := PromTypeToCatalog(in); got != want {
			t.Errorf("PromTypeToCatalog(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchMetricMetadataIsBounded(t *testing.T) {
	var gotLimit, gotPer, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotLimit, gotPer = r.URL.Path, r.URL.Query().Get("limit"), r.URL.Query().Get("limit_per_metric")
		fmt.Fprint(w, `{"status":"success","data":{"up":[{"type":"gauge","help":"Target up.","unit":""}]}}`)
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	m, err := s.FetchMetricMetadata(context.Background())
	if err != nil || m["up"].Help != "Target up." {
		t.Fatalf("%v %+v", err, m)
	}
	if gotPath != "/api/v1/metadata" || gotLimit != fmt.Sprint(MetadataLimit) || gotPer != "1" {
		t.Fatalf("request path=%q limit=%q per=%q", gotPath, gotLimit, gotPer)
	}
}
