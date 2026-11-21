package runtimesig

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func gk(kv ...string) []string {
	out := []string{}
	for i := 0; i+1 < len(kv); i += 2 {
		out = append(out, kv[i]+`="`+kv[i+1]+`"`)
	}
	return out
}

func one(v float64, kv ...string) chstore.SpanMetricSeries {
	return chstore.SpanMetricSeries{GroupKey: gk(kv...), Points: []chstore.SpanMetricPoint{{Time: 1, Value: v}}}
}

func TestDownstreamQueriesBounded(t *testing.T) {
	q := DownstreamRateQuery("service_name", "svc-a", `deployment_environment_name=~"prod-cl-a"`, 900)
	want := `topk(20, sum by (server_address) (rate(http_client_request_duration_seconds_count{service_name="svc-a", deployment_environment_name=~"prod-cl-a"}[900s])))`
	if q != want {
		t.Fatalf("rate q =\n%s\nwant\n%s", q, want)
	}
	p := DownstreamP95Query("service_name", "svc-a", "", []string{"b.svc", "a.svc"}, 900)
	if !strings.Contains(p, `server_address=~"a\\.svc|b\\.svc"`) || !strings.HasPrefix(p, "histogram_quantile(0.95, sum by (server_address, le)") {
		t.Fatalf("p95 q = %s", p)
	}
	e := DownstreamErrQuery("service_name", "svc-a", "", []string{"a.svc"}, 900, false)
	if !strings.Contains(e, `error_type!=""`) || !strings.HasPrefix(e, "topk(100,") {
		t.Fatalf("err q = %s", e)
	}
	if c := DownstreamErrQuery("service_name", "svc-a", "", []string{"a.svc"}, 900, true); !strings.Contains(c, `http_response_status_code=~"5.."`) {
		t.Fatalf("code q = %s", c)
	}
	if w := DownstreamWindowSec(time.Unix(0, 0), time.Unix(60, 0)); w != 300 {
		t.Fatalf("min win = %d", w)
	}
	if w := DownstreamWindowSec(time.Unix(0, 0), time.Unix(86400, 0)); w != 3600 {
		t.Fatalf("max win = %d", w)
	}
}

func TestAssembleDownstream(t *testing.T) {
	rates := []chstore.SpanMetricSeries{one(10, "server_address", "b.svc"), one(40, "server_address", "a.svc")}
	errs := []chstore.SpanMetricSeries{
		one(2, "server_address", "a.svc", "error_type", "500"),
		one(6, "server_address", "a.svc", "error_type", "java.net.SocketTimeoutException"),
		one(1, "server_address", "zz.unknown", "error_type", "500"), // seçilmemiş hedef
	}
	p95 := []chstore.SpanMetricSeries{one(0.25, "server_address", "a.svc")}
	got := AssembleDownstream(rates, errs, p95, "error_type", func(a string) string {
		if a == "a.svc" {
			return "svc-b"
		}
		return ""
	})
	if len(got) != 2 || got[0].Address != "a.svc" || got[0].Service != "svc-b" {
		t.Fatalf("order/resolve = %+v", got)
	}
	a := got[0]
	if a.ErrorPct != 20 || a.DominantError != "java.net.SocketTimeoutException" || a.DominantErrorPct != 75 || a.P95Ms != 250 {
		t.Fatalf("a = %+v", a)
	}
	if b := got[1]; b.ErrorPct != 0 || b.P95Ms != -1 || b.DominantError != "" {
		t.Fatalf("b = %+v", b)
	}
}

type fakeInstant struct{ calls []string }

func (f *fakeInstant) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, q)
	switch {
	case strings.HasPrefix(q, "topk(20"):
		return []chstore.SpanMetricSeries{one(5, "server_address", "a.svc")}, nil
	case strings.HasPrefix(q, "label_set"):
		return []chstore.SpanMetricSeries{one(3, "k", "pending"), one(50, "k", "leased"), one(50, "k", "max")}, nil
	case strings.Contains(q, `error_type!=""`):
		return nil, nil // etiket yok → 5xx yedeği
	case strings.Contains(q, `http_response_status_code=~"5.."`):
		return []chstore.SpanMetricSeries{one(1, "server_address", "a.svc", "http_response_status_code", "503")}, nil
	}
	return nil, nil
}

func TestReadDownstreamStatusFallbackAndPool(t *testing.T) {
	f := &fakeInstant{}
	now := time.Unix(10_000, 0)
	rep, err := ReadDownstream(context.Background(), f, "", "svc-a", "", now.Add(-15*time.Minute), now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ErrorSource != "status_code" || len(rep.Targets) != 1 || rep.Targets[0].DominantError != "503" || rep.Targets[0].ErrorPct != 20 {
		t.Fatalf("rep = %+v", rep)
	}
	if rep.Pool == nil || rep.Pool.Pending != 3 || rep.Pool.Max != 50 {
		t.Fatalf("pool = %+v", rep.Pool)
	}
	if len(f.calls) > 5 {
		t.Fatalf("query budget exceeded: %d", len(f.calls))
	}
}
