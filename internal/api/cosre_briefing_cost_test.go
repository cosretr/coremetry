package api

// v0.10.1728 — briefing üretici tik maliyeti: /admin/stats `briefing` bloğu
// alanları taşır; VM sayımı okuyucu sarmalayıcısında (çağrı noktası), nil
// sayaç sarmalamaz.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
)

func TestBriefingStatsBlockFields(t *testing.T) {
	recordBriefingTick(briefing.TickCost{AtUnix: 1, Teams: 2, VMQueries: 12, CHQueries: 30, ESQueries: 2, DurationMs: 800})
	st := withBriefingCost(&chstore.SystemStats{})
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Briefing map[string]map[string]float64 `json:"briefing"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"last", "total"} {
		for _, k := range []string{"teams", "vm_queries", "ch_queries", "es_queries", "duration_ms"} {
			if _, ok := out.Briefing[part][k]; !ok {
				t.Errorf("briefing.%s.%s yok: %s", part, k, raw)
			}
		}
	}
	if out.Briefing["last"]["vm_queries"] != 12 || out.Briefing["total"]["ticks"] < 1 {
		t.Errorf("değerler: %s", raw)
	}
}

type fakeRED struct{ redFamilyReader }

func (fakeRED) QueryPromQLRange(context.Context, string, time.Time, time.Time, int, int) ([]chstore.SpanMetricSeries, error) {
	return nil, nil
}

func (fakeRED) QueryPromQLInstant(context.Context, string, time.Time) ([]chstore.SpanMetricSeries, error) {
	return nil, nil
}

func (fakeRED) MetricLabelNamesForServiceIn(context.Context, string, string, string, time.Time, time.Time) ([]string, bool, error) {
	return nil, false, nil
}

func TestBriefingCountingRED(t *testing.T) {
	c := &briefing.Cost{}
	rd := countingRED(fakeRED{}, c)
	ctx, now := context.Background(), time.Now()
	_, _ = rd.QueryPromQLRange(ctx, "q", now, now, 60, 10)
	_, _ = rd.QueryPromQLRange(ctx, "q", now, now, 60, 10)
	_, _ = rd.QueryPromQLInstant(ctx, "q", now)
	_, _, _ = rd.MetricLabelNamesForServiceIn(ctx, "m", "service_name", "orders-api", now, now)
	if tk := c.Tick(0, 0, 0); tk.VMQueries != 4 {
		t.Errorf("VM sayımı %d, want 4", tk.VMQueries)
	}
	if _, wrapped := countingRED(fakeRED{}, nil).(briefingCountingRED); wrapped {
		t.Error("nil sayaç sarmalamamalı")
	}
}
