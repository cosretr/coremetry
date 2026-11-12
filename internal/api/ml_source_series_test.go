package api

// v0.10.2036 — metrik/bağımlılık olayının detay grafiği: Thanos matrisi pod
// başına seriye çözülür; önbellek anahtarı her girdiyi taşır. Fikstür nötr.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
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
