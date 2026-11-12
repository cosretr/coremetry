package ml

// v0.10.1764 — VM bütçesi: 422/zaman aşımı sınıflaması, artan backoff (5 dk ×
// 2ⁿ ≤ 1 sa), beklemedeki aileye sorgu gönderilmez, kapalı aile/dedektör
// hiç sorgu planlamaz, servis grupları + hedef regex'i, geçmiş birleştirme.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

type fakeVM struct {
	calls []string
	err   error
}

func (f *fakeVM) Configured() bool { return true }
func (f *fakeVM) MetricLabelMap() vmetrics.LabelMap {
	return vmetrics.LabelMap{Service: "service_name"}
}
func (f *fakeVM) MetricLabelNamesIn(context.Context, string, time.Time, time.Time) ([]string, bool, error) {
	return []string{"service_name", "server_address", "le"}, true, nil
}
func (f *fakeVM) MetricLabelValuesIn(context.Context, string, string, time.Time, time.Time, int) ([]string, bool, error) {
	return nil, true, nil
}
func (f *fakeVM) QueryPromQLRange(_ context.Context, q string, _, _ time.Time, _, _ int) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, "range:"+q)
	return nil, f.err
}
func (f *fakeVM) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, q)
	return nil, f.err
}

func TestHeavyVMErrorAndBackoff(t *testing.T) {
	for _, e := range []error{errors.New("HTTP 422: cannot select more than maxSamplesPerQuery=1e9"), errors.New("Get: Client.Timeout exceeded"), context.DeadlineExceeded} {
		if !heavyVMError(e) {
			t.Errorf("ağır sayılmalı: %v", e)
		}
	}
	if heavyVMError(errors.New("connection refused")) || heavyVMError(nil) {
		t.Error("ağ hatası backoff'a girmez")
	}
	if backoffFor(1) != 5*time.Minute || backoffFor(3) != 20*time.Minute || backoffFor(9) != time.Hour || backoffFor(50) != time.Hour {
		t.Fatalf("backoff: %v %v %v", backoffFor(1), backoffFor(3), backoffFor(9))
	}
	now := time.Unix(1_800_000_000, 0)
	vm := &fakeVM{err: errors.New("HTTP 422 maxSamplesPerQuery")}
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	_, skipped, err := vmInstant(context.Background(), deps, "dep:http", "q", now, now)
	if skipped || err == nil || len(vm.calls) != 1 {
		t.Fatalf("ilk çağrı VM'e gitmeli ve hata dönmeli: skipped=%v err=%v calls=%d", skipped, err, len(vm.calls))
	}
	if _, skipped, _ := vmInstant(context.Background(), deps, "dep:http", "q", now.Add(time.Minute), now.Add(time.Minute)); !skipped || len(vm.calls) != 1 {
		t.Fatalf("beklemede: sorgu gönderilmemeli (calls=%d)", len(vm.calls))
	}
	if snap := deps.Backoff.snapshot(now.Add(time.Minute)); len(snap) != 1 || snap[0].Family != "dep:http" || snap[0].Until != now.Add(5*time.Minute).Unix() {
		t.Fatalf("snapshot=%+v", snap)
	}
	vm.err = nil
	if _, skipped, err := vmInstant(context.Background(), deps, "dep:http", "q", now.Add(6*time.Minute), now.Add(6*time.Minute)); skipped || err != nil || len(vm.calls) != 2 {
		t.Fatal("bekleme bitince yeniden dener")
	}
	if len(deps.Backoff.snapshot(now.Add(6*time.Minute))) != 0 {
		t.Fatal("başarı beklemeyi siler")
	}
}

func TestDisabledSendsNoQuery(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	if NewDependency().Enabled(cfg) || NewMetricML().Enabled(cfg) || NewTraceRED().Enabled(cfg) {
		t.Fatal("varsayılan: hiçbir VM dedektörü açık değil")
	}
	if got := metricExprs(cfg, "service_name", 300); len(got) != 0 {
		t.Fatalf("kapalı aileler sorgu planlamaz: %d", len(got))
	}
	cfg.Detectors["metric_kafka"] = false // v0.10.2064: varsayılan açık (Davis S3)
	cfg.Enabled, cfg.Detectors["metric_jvm"] = true, true
	for _, p := range metricExprs(cfg, "service_name", 300) {
		if p.family != "metric_jvm" {
			t.Fatalf("yalnız jvm planlanmalı: %+v", p)
		}
	}
}

func TestSvcBatchesAndMergeHistory(t *testing.T) {
	bySvc := map[string][]string{"c": {"db.x"}, "a": {"h1", "h2"}, "b": {"h1"}}
	bs := svcBatches(bySvc, 2)
	if len(bs) != 2 || bs[0].svcRe != "a|b" || bs[0].tgtRe != "h1|h2" || bs[1].tgtRe != `db\.x` || !bs[0].allowed["b"]["h1"] || bs[0].allowed["b"]["h2"] {
		t.Fatalf("batches=%+v", bs)
	}
	series := map[string]map[int64]float64{"k": {600: 3}}
	mergeHistory(series, map[string][]chstore.MLPoint{"k": {{T: 0, V: 1}, {T: 300, V: 2}, {T: 600, V: 9}}, "other": {{T: 300, V: 7}}}, 300, 600)
	if len(series) != 1 || series["k"][300] != 2 || series["k"][600] != 3 {
		t.Fatalf("merge=%v", series)
	}
	if _, ok := series["k"][0]; ok {
		t.Fatal("pencere dışı kova girmez")
	}
}
