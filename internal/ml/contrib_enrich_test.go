package ml

// v0.10.2037 — suçlu seri sorgu bütçesi: açılışta bir sorgu; sonraki
// tiklerde taşınır; tepe 1,5 kattan fazla büyürse bir kez daha; ikiden sonra
// asla; sorgu hatasında olay yine yazılır, ad boş. Fikstür nötr.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

type contribVM struct {
	fakeVM
	rows []chstore.SpanMetricSeries
}

func (f *contribVM) QueryPromQLInstant(_ context.Context, q string, _ time.Time) ([]chstore.SpanMetricSeries, error) {
	f.calls = append(f.calls, q)
	return f.rows, f.err
}

func TestEnrichContributorsQueryBudget(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: chstore.DefaultMLMetricFamilies(), BucketSec: 300}
	now := time.Unix(1_800_000_000, 0)
	vm := &contribVM{rows: []chstore.SpanMetricSeries{
		{GroupKey: []string{"client_id=orders-collector-consumer-v1"}, Points: []chstore.SpanMetricPoint{{Time: now.UnixNano(), Value: 2790}}},
		{GroupKey: []string{"client_id=orders-audit-consumer"}, Points: []chstore.SpanMetricPoint{{Time: now.UnixNano(), Value: 10}}},
	}}
	deps := Deps{VM: vm, Backoff: newVMBackoff()}
	cs := contribSource{}
	base := chstore.MLEvent{ID: "ep1", Detector: "metric_kafka", Signal: "metric", Code: "metric_kafka|consumer_lag", Service: "orders-consumer",
		WindowSec: 300, LastSeen: now.UnixNano(), Stage: chstore.MLStageShadow, Evidence: `{"source":"vm"}`}
	var last chstore.MLEvent
	prev := func(string) (chstore.MLEvent, bool) { return last, last.ID != "" }
	step := func(obs float64) (int, *Contrib) {
		ev := base
		ev.Observed = obs
		kept := []chstore.MLEvent{ev}
		n := cs.enrichContributors(context.Background(), deps, cfg, kept, prev, now)
		last = kept[0]
		return n, evidenceContrib(kept[0].Evidence)
	}
	if n, c := step(2800); n != 1 || c == nil || c.Kind != "single" || c.Items[0].Name != "orders-collector-consumer-v1" || c.N != 1 || c.Series != 2 {
		t.Fatalf("açılış: n=%d c=%+v", n, c)
	}
	if n, c := step(3000); n != 0 || c == nil || c.N != 1 {
		t.Fatalf("taşıma: n=%d c=%+v", n, c)
	}
	if n, c := step(4300); n != 1 || c == nil || c.N != 2 || c.Peak != 4300 {
		t.Fatalf("tepe büyüdü: n=%d c=%+v", n, c)
	}
	if n, c := step(50000); n != 0 || c == nil || c.N != 2 {
		t.Fatalf("tavan: n=%d c=%+v", n, c)
	}
	if len(vm.calls) != 2 || !strings.Contains(vm.calls[0], "max by (client_id)") {
		t.Fatalf("VM çağrıları: %v", vm.calls)
	}
	// Hata: olay yazılır, ad boş.
	last = chstore.MLEvent{}
	vm.err = errors.New("connection refused")
	ev := base
	ev.Observed = 10
	kept := []chstore.MLEvent{ev}
	if n := cs.enrichContributors(context.Background(), deps, cfg, kept, prev, now); n != 1 || evidenceContrib(kept[0].Evidence) != nil || len(kept) != 1 {
		t.Fatalf("hata yolu: n=%d ev=%s", n, kept[0].Evidence)
	}
	// Log/trace olayına sorgu yok.
	vm.err = nil
	tr := []chstore.MLEvent{{ID: "t", Detector: "op_latency", Signal: "trace", Code: "POST /orders", Service: "orders-api"}}
	if n := cs.enrichContributors(context.Background(), deps, cfg, tr, prev, now); n != 0 {
		t.Fatalf("trace olayı sorgu attı: %d", n)
	}
}
