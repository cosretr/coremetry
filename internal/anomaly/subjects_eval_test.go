package anomaly

// v0.10.1792 — Davis paritesi FAZ 1.4: yönlü anomaly_event. Pinlenen:
//   - evaluateAnomaly'nin olay alanları (EventDirection / EventBaselineKind /
//     Median = baseline_value) — TestEvaluateAnomaly'nin genişlemesi;
//   - yön / taban / özne kolonlarını YAZAN her yol (recorder'ın dört
//     kurucusu, davranış motoru, takvim düşüşü, özne olayı);
//   - özne kararları: sıçrama → olay, sabit → yok, batch servisin endpoint'inde
//     request_rate yok, deterministik sıra, boş tik → yazım yok.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func steadySeries(n int, v float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func spikeSeries(n int, base, spike float64, dwell int) []float64 {
	out := steadySeries(n, base)
	for i := n - dwell; i < n; i++ {
		out[i] = spike
	}
	return out
}

func TestEvaluateAnomalyEventFields(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	up := evaluateAnomaly("p99_ms", spikeSeries(40, 100, 900, cfg.DwellBuckets), nil, steadySeries(40, 5), 4, false, batchLatSeries{}, cfg)
	if up.Action != "open" || up.EventDirection() != "up" || up.EventBaselineKind() != "consecutive" || up.Median != 100 {
		t.Fatalf("sıçrama: %q %q %q med=%v", up.Action, up.EventDirection(), up.EventBaselineKind(), up.Median)
	}
	seas := evaluateAnomaly("p99_ms", spikeSeries(40, 100, 900, cfg.DwellBuckets), steadySeries(20, 110), steadySeries(40, 5), 4, false, batchLatSeries{}, cfg)
	if seas.EventBaselineKind() != "seasonal" || seas.Median != 110 {
		t.Fatalf("mevsimsel taban: %q med=%v", seas.EventBaselineKind(), seas.Median)
	}
	down := evaluateAnomaly("request_rate", spikeSeries(40, 100, 1, cfg.DwellBuckets), nil, spikeSeries(40, 100, 1, cfg.DwellBuckets), 4, false, batchLatSeries{}, cfg)
	if down.Action != "open" || down.EventDirection() != "down" {
		t.Fatalf("düşüş: %q %q", down.Action, down.EventDirection())
	}
	skip := evaluateAnomaly("p99_ms", steadySeries(4, 100), nil, steadySeries(4, 5), 4, false, batchLatSeries{}, cfg)
	if skip.Action != "skip" || skip.EventBaselineKind() != "none" {
		t.Fatalf("skip: %q %q", skip.Action, skip.EventBaselineKind())
	}
	// Açılmamış karar: yön cari değerin tarafından.
	if (anomalyOutcome{Current: 5, Median: 10}).EventDirection() != "down" || (anomalyOutcome{Current: 10, Median: 10}).EventDirection() != "up" {
		t.Error("açılmamış kararın yönü")
	}
}

// TestEveryEventWriterFillsDirectionBaseline — "ikinci yarı": kolonları yazan
// her kurucu yön + taban türü + özne türünü doldurur.
func TestEveryEventWriterFillsDirectionBaseline(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	type want struct{ dir, kind string }
	cases := []struct {
		name string
		ev   chstore.AnomalyEvent
		want want
		base float64
	}{
		{"log_pattern spike", logPatternEvent(LogPatternAnomaly{Pattern: "p", Service: "s", Ratio: 4, BaselineCount: 10}, now), want{"up", "consecutive"}, 10},
		{"log_pattern new", logPatternEvent(LogPatternAnomaly{Pattern: "p", Service: "s", Ratio: 0}, now), want{"up", "none"}, 0},
		{"trace_op spike", traceOpEvent(TraceOpAnomaly{Operation: "o", Service: "s", Ratio: 6, BaselineErrors: 3}, now), want{"up", "consecutive"}, 3},
		{"trace_op new_error", traceOpEvent(TraceOpAnomaly{Operation: "o", Service: "s", Ratio: 0}, now), want{"up", "none"}, 0},
		{"davranış mevsimsel düşüş", behaviorEvent(behaviorCandidate{Service: "s", Metric: "request_rate", Signal: "seasonal", Direction: "down", Ratio: 0.4, Baseline: 50}, now), want{"down", "seasonal"}, 50},
		{"davranış rejim", behaviorEvent(behaviorCandidate{Service: "s", Metric: "p99_ms", Signal: "regime", Direction: "up", Ratio: 3, Baseline: 80}, now), want{"up", "consecutive"}, 80},
		{"takvim düşüşü", calendarDropEvent("s", chstore.CalendarHoliday, 2, 10, "consecutive", now), want{"down", "consecutive"}, 10},
		{"takvim düşüşü (taban boş → mevsimsel)", calendarDropEvent("s", chstore.CalendarHoliday, 2, 10, "", now), want{"down", "seasonal"}, 10},
	}
	for _, c := range cases {
		if c.ev.Direction != c.want.dir || c.ev.BaselineKind != c.want.kind || c.ev.BaselineValue != c.base || c.ev.SubjectKind != "service" {
			t.Errorf("%s: dir=%q kind=%q base=%v subj=%q", c.name, c.ev.Direction, c.ev.BaselineKind, c.ev.BaselineValue, c.ev.SubjectKind)
		}
		// Eski okuyucu sözleşmesi korunur: tabansız olay oranı 0 yazar.
		if c.ev.BaselineKind == "none" && c.ev.CurrentRatio != chstore.CountOnlyRatio {
			t.Errorf("%s: tabansız olayın oranı %v", c.name, c.ev.CurrentRatio)
		}
	}
}

func TestSubjectAnomalyEvent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	oc := anomalyOutcome{Action: "open", Direction: "spiked", Current: 900, Median: 100, Z: 12, Dwell: 3, BaselineKind: "seasonal", BaselineN: 30}
	ser := subjectSeries{Rates: []float64{2, 3}}
	ep := subjectAnomalyEvent(subjectKey{Kind: "endpoint", Owner: "checkout", ID: "GET /cart"}, "p99_ms", oc, ser, now)
	if ep.Kind != chstore.SubjectAnomalyKind || ep.Service != "checkout" || ep.Pattern != "p99_ms @ GET /cart" ||
		ep.SubjectKind != "endpoint" || ep.SubjectID != "GET /cart" || ep.Direction != "up" || ep.BaselineKind != "seasonal" ||
		ep.BaselineValue != 100 || ep.CurrentRatio != 9 || ep.CurrentCount != 900 || ep.PeakRatio != 9 {
		t.Errorf("endpoint olayı: %+v", ep)
	}
	if ep.ID != chstore.FingerprintAnomaly(chstore.SubjectAnomalyKind, "p99_ms @ GET /cart", "checkout") {
		t.Error("parmak izi (tür, desen, servis) değil")
	}
	other := subjectAnomalyEvent(subjectKey{Kind: "endpoint", Owner: "checkout", ID: "GET /pay"}, "p99_ms", oc, ser, now)
	if other.ID == ep.ID {
		t.Error("aynı servisin iki rotası aynı parmak izini aldı")
	}
	db := subjectAnomalyEvent(subjectKey{Kind: "db", Owner: "db:oracle@x", ID: "db:oracle@x"}, "error_rate",
		anomalyOutcome{Action: "open", Direction: "dropped", Current: 0, Median: 4, BaselineKind: "consecutive", BaselineN: 20}, subjectSeries{}, now)
	if db.Service != "db:oracle@x" || db.Pattern != "error_rate" || db.Direction != "down" || db.BaselineKind != "consecutive" || db.CurrentRatio != 0 {
		t.Errorf("db olayı: %+v", db)
	}
	// Sıfıra düşüş tabansız DEĞİL (1587 tuzağının ayrımı).
	if chstore.AnomalyBaselineless(db) {
		t.Error("tabanlı sıfır düşüşü tabansız okundu")
	}
}

func TestDecideSubjects(t *testing.T) {
	cfg := chstore.DefaultAnomalySensitivity()
	now := time.Unix(1_700_000_000, 0)
	spike := subjectSeries{Values: spikeSeries(40, 100, 900, cfg.DwellBuckets), Rates: steadySeries(40, 5)}
	steady := subjectSeries{Values: steadySeries(40, 100), Rates: steadySeries(40, 5)}
	load := subjectSeries{Values: spikeSeries(40, 5, 500, cfg.DwellBuckets), Rates: spikeSeries(40, 5, 500, cfg.DwellBuckets)}
	k := func(kind, owner, id string) string { return subjectKey{Kind: kind, Owner: owner, ID: id}.String() }
	tick := subjectTick{
		"p99_ms": {
			k("endpoint", "checkout", "/b"): spike,
			k("endpoint", "checkout", "/a"): spike,
			k("endpoint", "checkout", "/c"): steady,
			"bozuk":                         spike,
		},
		"request_rate": {
			k("endpoint", "nightly-batch", "/run"):       load, // batch kuralı: yük anomali değil
			k("queue", "queue:kafka@t", "queue:kafka@t"): load,
		},
	}
	evs := decideSubjects(tick, cfg, 4, now)
	var got []string
	for _, e := range evs {
		got = append(got, e.Pattern+"|"+e.Service)
	}
	want := []string{"p99_ms @ /a|checkout", "p99_ms @ /b|checkout", "request_rate|queue:kafka@t"}
	if len(got) != len(want) {
		t.Fatalf("olaylar %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("olaylar %v, want %v", got, want)
		}
	}
	if decideSubjects(nil, cfg, 4, now) != nil {
		t.Error("boş tik olay üretti")
	}
	// Boş tik: mağaza yok, yazım denemesi yok (panik etmez).
	if n := (&Detector{}).applySubjects(context.Background(), nil, cfg, 4, now); n != 0 {
		t.Error("boş tik yazdı")
	}
}
