package logstore

import (
	"encoding/json"
	"strings"
	"testing"
)

// v0.10.844 — chstore.SpanMetricSeries kapısının log serisi ikizi: nil
// Points tel üstünde [] olur; dolu seri ve alan adları aynen.
func TestLogSeriesJSONNilPointsIsEmptyArray(t *testing.T) {
	b, err := json.Marshal([]LogSeries{{Name: "ERROR"}, {Name: "WARN", Points: []LogPoint{{T: 1, V: 2}}}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if strings.Contains(s, "null") {
		t.Fatalf("null sızdı: %s", s)
	}
	for _, w := range []string{`{"name":"ERROR","points":[]}`, `{"name":"WARN","points":[{"t":1,"v":2}]}`} {
		if !strings.Contains(s, w) {
			t.Fatalf("%q yok: %s", w, s)
		}
	}
}
