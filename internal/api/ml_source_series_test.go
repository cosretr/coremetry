package api

// v0.10.2036 — metrik/bağımlılık olayının detay grafiği: Thanos matrisi pod
// başına seriye çözülür; önbellek anahtarı her girdiyi taşır. Fikstür nötr.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestDecodeMatrixSeries(t *testing.T) {
	raw := json.RawMessage(`[
	 {"metric":{"pod":"orders-batch-7d9-b"},"values":[[1800000000,"2"],[1800000060,"NaN"],[1800000120,"3.5"]]},
	 {"metric":{"pod":"orders-batch-7d9-a"},"values":[[1800000000,"1"]]},
	 {"metric":{"pod":"orders-batch-7d9-c"},"values":[[1800000000,"+Inf"]]}]`)
	got := decodeMatrixSeries("matrix", raw, "pod")
	if len(got) != 2 || got[0].GroupKey[0] != "orders-batch-7d9-a" || got[1].GroupKey[0] != "orders-batch-7d9-b" {
		t.Fatalf("seriler: %+v", got)
	}
	if len(got[1].Points) != 2 || got[1].Points[1].Time != 1800000120*int64(time.Second) || got[1].Points[1].Value != 3.5 {
		t.Fatalf("noktalar: %+v", got[1].Points)
	}
	if n := len(decodeMatrixSeries("vector", raw, "pod")); n != 0 {
		t.Fatalf("matris dışı tür boş olmalı, %d", n)
	}
	if one := decodeMatrixSeries("matrix", raw, ""); one[0].GroupKey[0] != "" {
		t.Fatal("by boşken grup anahtarı boş")
	}
}

func TestMLSourceSeriesKeyCarriesAllInputs(t *testing.T) {
	a := time.Unix(1_800_000_000, 0)
	b := a.Add(time.Hour)
	base := mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", "", a, b, 60)
	for i, k := range []string{
		mlSourceSeriesKey("metric_k8s", "orders-batch", "metric_jvm|heap_used", "", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-consumer", "metric_jvm|heap_used", "", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|gc_time", "", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used@prod-east", "", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", "http", a, b, 60), // v0.10.2055 aile
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", "", a.Add(time.Minute), b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", "", a, b.Add(time.Minute), 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", "", a, b, 120),
	} {
		if k == base {
			t.Fatalf("girdi %d anahtarı değiştirmedi", i)
		}
	}
}

// v0.10.2061 — canlı seri yokken gerekçe tek cümle (eski: kayıtlı görüntüye
// sessiz düşüş); seri varsa gerekçe taşınmaz.
func TestWithReason(t *testing.T) {
	empty := mlSourceSeriesResponse{Series: []chstore.SpanMetricSeries{}}
	full := mlSourceSeriesResponse{Series: []chstore.SpanMetricSeries{{GroupKey: []string{""}}}}
	cases := []struct {
		name string
		in   mlSourceSeriesResponse
		r    string
		want string
	}{
		{"ifade yok", empty, srcGapNoExpr, srcGapNoExpr},
		{"VM yok", empty, srcGapNoVM, srcGapNoVM},
		{"kısmi", empty, srcGapPartial, srcGapPartial},
		{"gerekçesiz boş → boş pencere", empty, "", srcGapEmpty},
		{"seri var → gerekçe yok", full, srcGapPartial, ""},
	}
	for _, c := range cases {
		if got := withReason(c.in, c.r).Reason; got != c.want {
			t.Fatalf("%s: %q", c.name, got)
		}
	}
}

// v0.10.2055 — trace_pod_outlier: VM aralık serisinin anahtarı pod adına iner.
func TestSeriesByLabel(t *testing.T) {
	pt := []chstore.SpanMetricPoint{{Time: 1, Value: 2}}
	in := []chstore.SpanMetricSeries{
		{GroupKey: []string{`k8s_pod_name="orders-api-7f9c-b"`}, Points: pt},
		{GroupKey: []string{`k8s_pod_name="orders-api-7f9c-a"`}, Points: pt},
		{GroupKey: []string{`other="x"`}, Points: pt},
		{GroupKey: []string{`k8s_pod_name="orders-api-7f9c-c"`}},
	}
	got := seriesByLabel(in, "k8s_pod_name", 50)
	if len(got) != 2 || got[0].GroupKey[0] != "orders-api-7f9c-a" || got[1].GroupKey[0] != "orders-api-7f9c-b" {
		t.Fatalf("seriler: %+v", got)
	}
	if n := len(seriesByLabel(in, "k8s_pod_name", 1)); n != 1 {
		t.Fatalf("tavan 1, %d", n)
	}
}

func TestTraceREDMeasure(t *testing.T) {
	for _, c := range [][3]string{
		{"op_latency", "p95", "p95"}, {"op_latency", "slow_ratio", "slow_ratio"}, {"op_latency", "", "slow_ratio"},
		{"op_error_rate", "", "err_ratio"}, {"throughput_drop", "p95", "rate"},
	} {
		if got := traceREDMeasure(c[0], c[1]); got != c[2] {
			t.Fatalf("%s/%s = %s", c[0], c[1], got)
		}
	}
	if !isTraceREDDetector("op_latency") || isTraceREDDetector("trace_error_count") {
		t.Fatal("isTraceREDDetector")
	}
}

// v0.10.2480 — operatör: taze uç nokta olayında canlı grafik "kaynak
// okunamadı". Olay kanıtında aile + etiketler varsa ifade dedektörün kayıt
// kurucusundan, etiket keşfi YOK; kaynak hatası 200 + kod + metin (URL'siz).
func TestTraceREDSourceSeriesUsesEventLabels(t *testing.T) {
	from, to := time.Unix(1_800_000_000, 0), time.Unix(1_800_003_600, 0)
	pin := anomaly.REDFamilyPins()[0]
	in := traceREDChartIn{Service: "orders", Route: `GET /api/v1/Orders/{id} "x"`, Measure: "p95", Family: pin.ID,
		SvcLabel: "service_name", RouteLabel: "http_route", BucketSec: 300, SlowSec: 1}
	pts := []chstore.SpanMetricPoint{{Time: 1, Value: 259}}
	rd := &fakeChartReader{series: []chstore.SpanMetricSeries{{Points: pts}}, labelErr: errors.New("must not discover")}
	out := traceREDSourceSeries(context.Background(), rd, mlSourceSeriesResponse{StepSec: 60}, in, from, to, 60)
	want, _ := ml.TraceREDSourceQuery(pin, ml.TraceREDLabels{Svc: "service_name", Route: "http_route"}, in.Service, in.Route, "p95", 300, 1)
	if rd.unscopedCalls != 0 || len(rd.scopedLabelCalls) != 0 {
		t.Fatalf("kanıt etiketi varken keşif koşmamalı: %d", rd.unscopedCalls)
	}
	if len(rd.queries) != 1 || rd.queries[0] != want.Expr || out.Query.Expr != want.Expr || rd.steps[0] != 60 {
		t.Fatalf("tek aralık sorgusu dedektör ifadesi olmalı:\n%v\n%s", rd.queries, want.Expr)
	}
	if len(out.Series) != 1 || out.ReasonCode != "" || out.Labels != "event" {
		t.Fatalf("seri: %+v", out)
	}
}

func TestTraceREDSourceSeriesFailureReasons(t *testing.T) {
	from, to := time.Unix(1_800_000_000, 0), time.Unix(1_800_003_600, 0)
	pin := anomaly.REDFamilyPins()[0]
	ev := traceREDChartIn{Service: "orders", Route: "/orders/{id}", Measure: "p95", Family: pin.ID, SvcLabel: "service_name", RouteLabel: "http_route", BucketSec: 300, SlowSec: 1}
	old := ev
	old.Family, old.SvcLabel, old.RouteLabel = "", "", ""
	cases := []struct {
		name, code, detail string
		in                 traceREDChartIn
		rd                 *fakeChartReader
	}{
		{"sorgu hatası", srcCodeQueryErr, "422: unknown function", ev,
			&fakeChartReader{queryErr: fmt.Errorf("%w: vm query_range http://user:secret@vm.example:8428/api/v1/query_range: 422: unknown function", errUpstream)}},
		{"zaman aşımı", srcCodeTimeout, "context deadline exceeded", ev, &fakeChartReader{queryErr: context.DeadlineExceeded}},
		{"boş", srcCodeEmpty, "", ev, &fakeChartReader{}},
		// Eski olay: bütün ailelerin keşfi hata → keşif hatası raporlanır (502 değil).
		{"keşif hatası", srcCodeQueryErr, "too many series", old, &fakeChartReader{labelErr: errors.New("too many series")}},
		{"keşif boş", srcCodeNoExpr, "", old, &fakeChartReader{}},
	}
	for _, c := range cases {
		out := traceREDSourceSeries(context.Background(), c.rd, mlSourceSeriesResponse{StepSec: 60}, c.in, from, to, 60)
		if out.ReasonCode != c.code || len(out.Series) != 0 {
			t.Fatalf("%s: kod %q (%+v)", c.name, out.ReasonCode, out)
		}
		if c.detail != "" && !strings.Contains(out.Detail, c.detail) {
			t.Fatalf("%s: ayrıntı %q", c.name, out.Detail)
		}
		if strings.Contains(out.Detail, "secret") || strings.Contains(out.Detail, "http://") {
			t.Fatalf("%s: ayrıntı URL / kimlik taşımamalı: %q", c.name, out.Detail)
		}
		if c.in.Family != "" && out.Query.Expr == "" {
			t.Fatalf("%s: denenen ifade teknik ayrıntı için dönmeli", c.name)
		}
	}
}

// Eski olay: ilk ailenin keşif hatası sonraki aileyi denemeyi kesmez.
func TestTraceREDSourceSeriesIsolatesFamilyErrors(t *testing.T) {
	from, to := time.Unix(1_800_000_000, 0), time.Unix(1_800_003_600, 0)
	pins := anomaly.REDFamilyPins()
	last := pins[len(pins)-1]
	rd := &famErrReader{fakeChartReader: fakeChartReader{series: []chstore.SpanMetricSeries{{Points: []chstore.SpanMetricPoint{{Time: 1, Value: 1}}}}},
		ok: ml.REDDiscoveryMetric(last), names: []string{"service_name", "http_route", "rpc_method", "status_code", "http_response_status_code", "rpc_grpc_status_code", "le"}}
	out := traceREDSourceSeries(context.Background(), rd, mlSourceSeriesResponse{StepSec: 60},
		traceREDChartIn{Service: "orders", Route: "/o", Measure: "p95", BucketSec: 300, SlowSec: 1}, from, to, 60)
	if len(out.Series) != 1 || out.ReasonCode != "" || out.Labels != "discovery" {
		t.Fatalf("son aile çizilmeli: %+v", out)
	}
}

type famErrReader struct {
	fakeChartReader
	ok    string
	names []string
}

func (f *famErrReader) MetricLabelNamesIn(_ context.Context, metric string, _, _ time.Time) ([]string, bool, error) {
	if metric == f.ok {
		return f.names, false, nil
	}
	return nil, false, errors.New("timeout")
}
