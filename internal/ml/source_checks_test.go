package ml

// source_checks_test.go — v0.10.1886. Kaynak ölçümü: seri varsa "var",
// yoksa "yok", hata = "ölçülemedi" (asla "yok"); VM bağlı değilken hiçbir
// sorgu gitmez; gRPC iki yazımdan (legacy / stable) biri yeter.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

type probeVM struct {
	off     bool
	labels  map[string][]string // metrik → etiketler
	labErr  map[string]error
	rowsFor string // ifade bu parçayı içeriyorsa 1 satır
	qErrFor string // ifade bu parçayı içeriyorsa hata
	calls   atomic.Int64
}

func (f *probeVM) Configured() bool { return !f.off }
func (f *probeVM) MetricLabelMap() vmetrics.LabelMap {
	return vmetrics.LabelMap{Service: "service_name"}
}
func (f *probeVM) MetricLabelNamesIn(_ context.Context, m string, _, _ time.Time) ([]string, bool, error) {
	f.calls.Add(1)
	return f.labels[m], false, f.labErr[m]
}
func (f *probeVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, false, nil
}
func (f *probeVM) QueryPromQLRange(context.Context, string, time.Time, time.Time, int, int) ([]chstore.SpanMetricSeries, error) {
	return nil, nil
}
func (f *probeVM) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.calls.Add(1)
	if f.qErrFor != "" && strings.Contains(q, f.qErrFor) {
		return nil, errors.New("timeout")
	}
	if f.rowsFor != "" && strings.Contains(q, f.rowsFor) {
		return []chstore.SpanMetricSeries{{}}, nil
	}
	return nil, nil
}

func checkByID(cs []chstore.MLSourceCheck, id string) chstore.MLSourceCheck {
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	return chstore.MLSourceCheck{}
}

func TestSourceChecks(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	vm := &probeVM{
		labels: map[string][]string{
			"http.server.request.duration":               {"service_name", "http_route"},
			"rpc.server.call.duration":                   {"service_name"}, // yalnız stable gRPC
			"http_client_request_duration_seconds_count": {"service_name", "server_address"},
		},
		labErr:  map[string]error{"messaging_client_operation_duration_seconds_count": errors.New("vm 503")},
		rowsFor: "kafka_consumer_records_lag_max",
		qErrFor: "jvm_",
	}
	cs := SourceChecks(context.Background(), vm, chstore.DefaultMLConfig(), now)
	want := map[string][2]bool{ // id → {checked, present}
		chstore.MLSourceHTTPServer:      {true, true},
		chstore.MLSourceGRPCServer:      {true, true},
		chstore.MLSourceHTTPClient:      {true, true},
		chstore.MLSourceDBClient:        {true, false},
		chstore.MLSourceMessagingClient: {false, false}, // hata: ölçülemedi
		"metric_kafka":                  {true, true},
		"metric_jvm":                    {false, false}, // tüm ifadeler hata
		"metric_pool":                   {true, false},
		"metric_k8s":                    {true, false},
	}
	for id, w := range want {
		c := checkByID(cs, id)
		if c.Label == "" || c.Checked != w[0] || c.Present != w[1] {
			t.Errorf("%s: %+v, want checked=%v present=%v", id, c, w[0], w[1])
		}
	}
	if n := checkByID(cs, chstore.MLSourceMessagingClient).Note; !strings.HasPrefix(n, "ölçülemedi") {
		t.Errorf("hata notu: %q", n)
	}
	// Öneriyle uçtan uca: HTTP/gRPC/Kafka açılır, JVM'e (ölçülemedi) dokunulmaz.
	cur := chstore.DefaultMLConfig()
	cur.Detectors["metric_jvm"] = true
	rec, _ := chstore.RecommendMLConfig(cur, cs)
	if !rec.Enabled || !rec.Detectors["op_latency"] || !rec.Detectors["metric_kafka"] || !rec.Detectors["metric_jvm"] || rec.Detectors["metric_k8s"] {
		t.Fatalf("öneri: %+v", rec.Detectors)
	}
}

func TestSourceChecksVMOff(t *testing.T) {
	vm := &probeVM{off: true}
	for _, c := range [][]chstore.MLSourceCheck{
		SourceChecks(context.Background(), vm, chstore.DefaultMLConfig(), time.Now()),
		SourceChecks(context.Background(), nil, chstore.DefaultMLConfig(), time.Now()),
	} {
		if len(c) != 2+len(depFamilies)+len(chstore.MLMetricDetectorNames) {
			t.Fatalf("satır sayısı: %d", len(c))
		}
		for _, x := range c {
			if x.Checked || x.Present || x.Note == "" {
				t.Fatalf("VM yokken: %+v", x)
			}
		}
	}
	if vm.calls.Load() != 0 {
		t.Fatalf("VM bağlı değilken sorgu gitmemeli: %d", vm.calls.Load())
	}
}
