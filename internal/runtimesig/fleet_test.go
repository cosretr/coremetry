package runtimesig

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Filo okuması iki kuşağı AYRI çözer: semconv servisi ile eski kuşak servisi
// aynı katalogda; ikisi de okunur.
func TestReadFleetDotnetBothGenerations(t *testing.T) {
	r := &fakeReader{
		names: []string{"dotnet_thread_pool_queue_length", "process_runtime_dotnet_thread_pool_queue_length"},
		series: map[string][]chstore.SpanMetricSeries{
			"dotnet_thread_pool_queue_length":                 {toSMS([]string{"svc-new", "pod-n", "", ""}, series(step(1, 200)))},
			"process_runtime_dotnet_thread_pool_queue_length": {toSMS([]string{"svc-old", "pod-o", "", ""}, series(step(1, 200)))},
		},
	}
	pods, _, err := ReadFleetDotnet(context.Background(), r, time.Unix(0, fixNow))
	if err != nil || len(pods) != 2 || pods[0].Service != "svc-new" || pods[1].Service != "svc-old" {
		t.Fatalf("pods=%+v err=%v", pods, err)
	}
	for _, q := range r.queries {
		if q.Service != "" || len(q.GroupBy) == 0 || q.GroupBy[0] != "service.name" {
			t.Errorf("fleet query must group by service: %+v", q)
		}
	}
	if sigs := Evaluate(pods, fixNow, th()); len(sigs) != 2 {
		t.Fatalf("sigs = %+v", sigs)
	}
}
