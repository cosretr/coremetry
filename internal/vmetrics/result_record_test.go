package vmetrics

// v0.10.1835 — ResultRecorder contract (Metrics Explorer Faz 1 dilim 1.1).
//
// Pins: every HTTP-facing VM read (QueryMetricNoted / QueryPromQLRange /
// QueryPromQLInstant / QueryMetricHistogram) records the expression it SENT,
// the step, the pre-cap series count, the 1000-series truncation and VM's
// isPartial flag; no recorder on ctx = no-op. The fake VM mirrors the
// documented Prometheus-API envelope ({status, data:{resultType,result},
// isPartial}).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/promapi"
)

// matrixBody — n series, one sample each, resultType matrix (query_range)
// or vector (query).
func matrixBody(n int, partial bool, vector bool) string {
	var b strings.Builder
	rt := "matrix"
	if vector {
		rt = "vector"
	}
	fmt.Fprintf(&b, `{"status":"success","isPartial":%v,"data":{"resultType":%q,"result":[`, partial, rt)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		if vector {
			fmt.Fprintf(&b, `{"metric":{"job":"j%04d"},"value":[1700003600,"1"]}`, i)
		} else {
			fmt.Fprintf(&b, `{"metric":{"job":"j%04d"},"values":[[1700003600,"1"]]}`, i)
		}
	}
	b.WriteString("]}}")
	return b.String()
}

func recorderServer(t *testing.T, n int, partial bool) (*Service, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, matrixBody(n, partial, r.URL.Path == "/api/v1/query"))
	}))
	t.Cleanup(srv.Close)
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	return s, &paths
}

func TestResultRecorderCapturesEveryHTTPRead(t *testing.T) {
	from := time.Unix(1_700_000_000, 0)
	to := from.Add(time.Hour)
	cases := []struct {
		name      string
		n         int
		partial   bool
		kind      string
		run       func(ctx context.Context, s *Service) error
		wantQuery string
		wantStep  bool
	}{
		{"promql range, partial", 3, true, ResultRange, func(ctx context.Context, s *Service) error {
			_, err := s.QueryPromQLRange(ctx, "sum(rate(x[5m]))", from, to, 60, 0)
			return err
		}, "sum(rate(x[5m]))", true},
		{"promql range, over the 1000 backstop", promapi.MaxSeriesParsed + 5, false, ResultRange, func(ctx context.Context, s *Service) error {
			_, err := s.QueryPromQLRange(ctx, "up", from, to, 60, 0)
			return err
		}, "up", true},
		{"promql instant", 2, false, ResultInstant, func(ctx context.Context, s *Service) error {
			_, err := s.QueryPromQLInstant(ctx, "up", to)
			return err
		}, "up", false},
		{"builder query (QueryMetricNoted)", 4, false, ResultRange, func(ctx context.Context, s *Service) error {
			_, _, err := s.QueryMetricNoted(ctx, chstore.MetricQueryFilter{Name: "process_cpu_seconds_total", Aggregation: "max", From: from, To: to, GroupBy: []string{"job"}})
			return err
		}, "process_cpu_seconds_total", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := recorderServer(t, tc.n, tc.partial)
			rec := &ResultRecorder{}
			if err := tc.run(WithResultRecorder(context.Background(), rec), s); err != nil {
				t.Fatal(err)
			}
			got := rec.Records()
			if len(got) != 1 {
				t.Fatalf("want 1 record, got %d: %+v", len(got), got)
			}
			r := got[0]
			if r.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", r.Kind, tc.kind)
			}
			if !strings.Contains(r.Query, tc.wantQuery) {
				t.Errorf("query %q does not contain %q", r.Query, tc.wantQuery)
			}
			if r.Total != tc.n {
				t.Errorf("total = %d, want %d (pre-cap count)", r.Total, tc.n)
			}
			if r.Truncated != (tc.n > promapi.MaxSeriesParsed) {
				t.Errorf("truncated = %v for n=%d", r.Truncated, tc.n)
			}
			if r.Partial != tc.partial {
				t.Errorf("partial = %v, want %v", r.Partial, tc.partial)
			}
			if tc.wantStep && r.StepSec <= 0 {
				t.Errorf("range read recorded no step")
			}
			if !tc.wantStep && r.StepSec != 0 {
				t.Errorf("instant read recorded step %d", r.StepSec)
			}
		})
	}
}

func TestResultRecorderHistogramAndNoRecorder(t *testing.T) {
	from := time.Unix(1_700_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"matrix","result":[`+
			`{"metric":{"le":"0.1"},"values":[[1700003600,"1"]]},`+
			`{"metric":{"le":"+Inf"},"values":[[1700003600,"2"]]}]}}`)
	}))
	defer srv.Close()
	s := New()
	s.Configure(Settings{BaseURL: srv.URL})
	rec := &ResultRecorder{}
	if _, err := s.QueryMetricHistogram(WithResultRecorder(context.Background(), rec), chstore.MetricQueryFilter{
		Name: "http_server_request_duration_seconds", Service: "orders-api", From: from, To: from.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	got := rec.Records()
	if len(got) != 1 || got[0].Kind != ResultHistogram || got[0].Total != 2 || !strings.Contains(got[0].Query, "_bucket") {
		t.Fatalf("histogram record = %+v", got)
	}
	// No recorder on ctx: the read still works and nothing panics.
	if _, err := s.QueryPromQLRange(context.Background(), "up", from, from.Add(time.Hour), 60, 0); err != nil {
		t.Fatal(err)
	}
	var nilRec *ResultRecorder
	if nilRec.Records() != nil {
		t.Fatal("nil recorder must return nil")
	}
}
