package api

// cosre_briefing_cost.go — v0.10.1728: Login Briefing üreticisinin tik
// maliyeti (briefing-audit R9 ölçümü; CLAUDE.md "expensive endpoint =
// register a counter"). briefing.Cost okuma yollarından geçer ve ÇAĞRI
// NOKTASINDA artar:
//   - VM: kalem 2'nin okuyucusu briefingCountingRED ile sarılır — range /
//     instant / etiket keşfi isteklerinin her biri (önbellek isabeti VM'e
//     gitmez, sayılmaz); Kafka soruları briefingCountingSrc ile (kaynak VM
//     ise vm, CH ise ch).
//   - CH: üreticinin her chstore çağrısı (çağrı yerinde AddCH).
//   - ES: readTeamLogs gerçekten logstore'a gittiyse (issued), ES kaynağında.
// Tik sonunda: /admin/stats `briefing` bloğu (GetSystemStats yanıtına
// süreç-içi, Behavior / CodeFetch ile aynı kablo; lider olmayan pod'da sıfır
// = doğru cevap) + tek info satırı `[briefing] tick teams=… dur=…ms`.

import (
	"context"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

var briefingCostObs struct {
	mu sync.Mutex
	st briefing.CostStats
}

// recordBriefingTick — tik ölçümünü süreç-içi bloğa yazar.
func recordBriefingTick(t briefing.TickCost) {
	briefingCostObs.mu.Lock()
	briefingCostObs.st = briefingCostObs.st.Record(t)
	briefingCostObs.mu.Unlock()
}

// briefingCostSnapshot — /admin/stats okuması.
func briefingCostSnapshot() briefing.CostStats {
	briefingCostObs.mu.Lock()
	defer briefingCostObs.mu.Unlock()
	return briefingCostObs.st
}

// withBriefingCost — getSystemStats yanıtına `briefing` bloğu.
func withBriefingCost(st *chstore.SystemStats) *chstore.SystemStats {
	if st != nil {
		st.Briefing = briefingCostSnapshot()
	}
	return st
}

// briefingCountingRED — VM okuyucusu; VM'e giden her istek bir sayım.
type briefingCountingRED struct {
	redFamilyReader
	cost *briefing.Cost
}

func countingRED(rd redFamilyReader, cost *briefing.Cost) redFamilyReader {
	if cost == nil || rd == nil {
		return rd
	}
	return briefingCountingRED{rd, cost}
}

func (c briefingCountingRED) QueryPromQLRange(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints int) ([]chstore.SpanMetricSeries, error) {
	c.cost.AddVM(1)
	return c.redFamilyReader.QueryPromQLRange(ctx, query, from, to, stepSeconds, maxDataPoints)
}

func (c briefingCountingRED) QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error) {
	c.cost.AddVM(1)
	return c.redFamilyReader.QueryPromQLInstant(ctx, query, at)
}

func (c briefingCountingRED) MetricLabelNamesForServiceIn(ctx context.Context, metric, svcLabel, service string, from, to time.Time) ([]string, bool, error) {
	c.cost.AddVM(1)
	return c.redFamilyReader.MetricLabelNamesForServiceIn(ctx, metric, svcLabel, service, from, to)
}

func (c briefingCountingRED) MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error) {
	c.cost.AddVM(1)
	return c.redFamilyReader.MetricLabelNamesIn(ctx, metric, from, to)
}

func (c briefingCountingRED) MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error) {
	c.cost.AddVM(1)
	return c.redFamilyReader.MetricLabelValuesIn(ctx, metric, label, from, to, limit)
}

// briefingCountingSrc — Kafka sorularının metrik kaynağı (VM ya da CH).
type briefingCountingSrc struct {
	metricSource
	cost *briefing.Cost
}

func countingSrc(src metricSource, cost *briefing.Cost) metricSource {
	if cost == nil || src == nil {
		return src
	}
	return briefingCountingSrc{src, cost}
}

func (c briefingCountingSrc) QueryMetric(ctx context.Context, f chstore.MetricQueryFilter) ([]chstore.SpanMetricSeries, error) {
	if c.Name() == metricSourceVM {
		c.cost.AddVM(1)
	} else {
		c.cost.AddCH(1)
	}
	return c.metricSource.QueryMetric(ctx, f)
}
