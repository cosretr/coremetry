package api

// v0.10.2036 — metrik/bağımlılık olayının detay grafiği: Thanos matrisi pod
// başına seriye çözülür; önbellek anahtarı her girdiyi taşır. Fikstür nötr.

import (
	"encoding/json"
	"testing"
	"time"
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
	base := mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", a, b, 60)
	for i, k := range []string{
		mlSourceSeriesKey("metric_k8s", "orders-batch", "metric_jvm|heap_used", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-consumer", "metric_jvm|heap_used", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|gc_time", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used@prod-east", a, b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", a.Add(time.Minute), b, 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", a, b.Add(time.Minute), 60),
		mlSourceSeriesKey("metric_jvm", "orders-batch", "metric_jvm|heap_used", a, b, 120),
	} {
		if k == base {
			t.Fatalf("girdi %d anahtarı değiştirmedi", i)
		}
	}
}
