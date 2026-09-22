package chstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.844 — Operator-reported sınıfın sistemik kapısı (v0.10.842: boşta
// serviste `points:null` → Overview .map → tüm sayfa düştü; v0.9.1315 aynı
// sınıf). Tel üstünde seri dilimleri ASLA null olmaz: nil → [].
func TestSpanMetricSeriesJSONNilSlicesAreEmptyArrays(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"ikisi nil", SpanMetricSeries{}, []string{`{"groupKey":[],"points":[]}`}},
		{"dolu aynen", SpanMetricSeries{GroupKey: []string{"a", "b"}, Points: []SpanMetricPoint{{Time: 1, Value: 2.5}}},
			[]string{`"groupKey":["a","b"]`, `"points":[{"time":1,"value":2.5}]`}},
		{"işaretçi", &SpanMetricSeries{GroupKey: []string{}}, []string{`{"groupKey":[],"points":[]}`}},
		{"harita → dilim (batch zarfı)", map[string][]SpanMetricSeries{"error_rate": {{}}},
			[]string{`{"error_rate":[{"groupKey":[],"points":[]}]}`}},
	}
	for _, c := range cases {
		b, err := json.Marshal(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		s := string(b)
		if strings.Contains(s, "null") {
			t.Fatalf("%s: null sızdı: %s", c.name, s)
		}
		for _, w := range c.want {
			if !strings.Contains(s, w) {
				t.Fatalf("%s: %q yok: %s", c.name, w, s)
			}
		}
	}
	// Gidiş-dönüş: kodlanan şekil aynı tipe geri okunur (alan adları değişmedi).
	var back SpanMetricSeries
	if err := json.Unmarshal([]byte(`{"groupKey":["x"],"points":[{"time":7,"value":1}]}`), &back); err != nil || len(back.Points) != 1 || back.Points[0].Time != 7 {
		t.Fatalf("gidiş-dönüş: %+v %v", back, err)
	}
}
