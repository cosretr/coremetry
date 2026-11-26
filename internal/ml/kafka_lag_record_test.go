package ml

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2215 (H2) — consumer lag olay kaydı: sözleşme gidiş-dönüşü,
// doğrulayıcıdan geçiş, kalan örnek ve kartın başlığı kayıttan okuması.
// v0.10.2382 (I5) — kayıt etki kuralının yargıladığı seridir: 1 dakikalık
// okuma, bant [0, kural eşiği]; 5 dakikalık kova / medyan-MAD bandı yok.

func lagTestInput(closed bool, vals func(min int) float64) LagRecordInput {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	var pts []chstore.SpanMetricPoint
	for m := 0; m <= 60; m++ {
		pts = append(pts, chstore.SpanMetricPoint{Time: from.Add(time.Duration(m) * time.Minute).UnixNano(), Value: vals(m)})
	}
	return LagRecordInput{Service: "orders-api", Topic: "orders-events", Query: lagSeriesQuery("service_name", "orders-api", "orders-events"),
		Median: 600, Threshold: 1200, DetectedAt: from.Add(45 * time.Minute), Closed: closed,
		Points: pts, From: from, To: from.Add(time.Hour), Confirmers: []string{"op_latency:abc"}}
}

func lagSpike(m int) float64 {
	switch {
	case m >= 40 && m < 55 && m != 50:
		return 5000
	case m == 50:
		return 9000
	}
	return 600
}

func TestLagRecordContractRoundTrip(t *testing.T) {
	for _, closed := range []bool{false, true} {
		rec, ok := BuildLagRecord(lagTestInput(closed, lagSpike))
		if !ok {
			t.Fatal("kayıt kurulmalı")
		}
		if vs := ValidateRecord(rec); len(vs) != 0 {
			t.Fatalf("closed=%v doğrulayıcıdan geçmeli: %+v", closed, vs)
		}
		from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
		if rec.OnsetAt != from.Add(40*time.Minute).UnixNano() || rec.PeakValue != 9000 || rec.ValueAtDetection != 5000 || rec.ExpectedValue != 600 {
			t.Fatalf("seriden türetme: onset=%d peak=%v det=%v", rec.OnsetAt, rec.PeakValue, rec.ValueAtDetection)
		}
		// bant etki kuralının bandı, seri 1 dakikalık; 5 dakikalık kova iddiası yok
		if rec.Baseline.Lo != 0 || rec.Baseline.Hi != 1200 || rec.Baseline.Method != lagImpactBandMethod || rec.Baseline.StepSec != 60 || rec.Series.StepSec != 60 {
			t.Fatalf("bant / adım: %+v %+v", rec.Baseline, rec.Series)
		}
		if closed != (rec.EndedAt == from.Add(55*time.Minute).UnixNano()) || closed != strings.Contains(rec.Title, "tepe 9.000") ||
			!strings.HasPrefix(rec.Title, "Consumer lag tespit anında 5.000 kayıt, normal üst sınır 1.200") {
			t.Fatalf("closed=%v ended=%d title=%q", closed, rec.EndedAt, rec.Title)
		}
		if rec.Volume != nil || rec.VolumeReason == "" || rec.Class != ClassServiceAnomaly || len(rec.Gates) != 0 {
			t.Fatalf("hacim nedeni / sınıf / kapı yer tutucusu: %+v", rec)
		}
		if len(rec.Evidence) != 2 || rec.Evidence[0].Independent || !rec.Evidence[1].Independent {
			t.Fatalf("kanıt bağımsızlığı: %+v", rec.Evidence)
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		var back EventRecord
		if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
			t.Fatalf("gidiş-dönüş: %v\n%+v\n%+v", err, back, rec)
		}
	}
}

// Etki kuralı ardışıklık istemez (5'te 3): onset olayın İLK ihlal dakikasının
// okumasıdır, tespit anındaki son koşunun başı değil. Kapanmış olayın bitişi
// son ihlalden sonraki noktadır (tespitten sonraki ilk banda dönüş değil) ve
// tepe bütün olaydan gelir.
func TestLagRecordOnsetAndEndFollowImpactRule(t *testing.T) {
	from := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	wavy := func(m int) float64 {
		switch {
		case m == 38, m == 40, m >= 42 && m < 45: // 38 ihlal, 39 değil, 40 ihlal, 41 değil, 42–44 ihlal
			return 5000
		case m >= 47 && m < 52: // tespitten sonra ikinci dalga
			return 70000
		}
		return 600
	}
	in := lagTestInput(true, wavy)
	in.DetectedAt, in.FirstViolation, in.LastSeen = from.Add(42*time.Minute), from.Add(38*time.Minute), from.Add(53*time.Minute)
	rec, ok := BuildLagRecord(in)
	if !ok || len(ValidateRecord(rec)) != 0 {
		t.Fatalf("kayıt geçerli olmalı: %+v", ValidateRecord(rec))
	}
	if rec.OnsetAt != from.Add(38*time.Minute).UnixNano() || rec.DetectedAt != from.Add(42*time.Minute).UnixNano() ||
		rec.EndedAt != from.Add(52*time.Minute).UnixNano() || rec.PeakValue != 70000 {
		t.Fatalf("onset=%s det=%s end=%s peak=%v", time.Unix(0, rec.OnsetAt).UTC(), time.Unix(0, rec.DetectedAt).UTC(), time.Unix(0, rec.EndedAt).UTC(), rec.PeakValue)
	}
	// ilk ihlal anı seride bant içindeyse (ya da bilinmiyorsa) tespit anındaki koşunun başı
	in.FirstViolation = from.Add(39 * time.Minute)
	if rec, _ := BuildLagRecord(in); rec.OnsetAt != from.Add(42*time.Minute).UnixNano() || len(ValidateRecord(rec)) != 0 {
		t.Fatalf("doğrulanamayan ilk ihlalde seriden onset: %s %+v", time.Unix(0, rec.OnsetAt).UTC(), ValidateRecord(rec))
	}
	// son görülme bilinmiyorsa bitiş tespitten sonraki ilk banda dönüş (eski kural)
	in.LastSeen = time.Time{}
	if rec, _ := BuildLagRecord(in); rec.EndedAt != from.Add(45*time.Minute).UnixNano() {
		t.Fatalf("bitiş: %s", time.Unix(0, rec.EndedAt).UTC())
	}
}

// attachLagRecords: yalnız etki kuralı damgalı olaya kayıt; pencere ölçülen
// son tam dakikada biter, tespit anı ilk tikin karar penceresinin bitişidir,
// onset olayın başlangıç dakikasının okumasıdır; olay başına tek aralık sorgusu.
func TestAttachLagRecordsFollowsImpactDecision(t *testing.T) {
	cfg := chstore.DefaultMLConfig()
	t0 := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	firstTick := t0.Add(15*time.Minute + 30*time.Second) // pencere bitişi 10:13
	now := firstTick.Add(10 * time.Minute)
	part := kafkaTopicPart("orders.events")
	vm := &lagVM{t0: t0, lag: map[[2]string]func(int) (float64, bool){{"orders", "orders.events"}: lagIncident(10, 60, 600, 165000)}}
	deps := Deps{VM: vm}
	samples := []impactSample{lagS(600), lagS(600), lagS(165000), lagS(165000), lagS(165000)}
	end := svcImpactEnd(firstTick, cfg.IngestLagSec)
	ev := lagImpactEvent("orders", part, samples, lagImpactDecide(samples, lagBaseOf(repeat(600, 30)), 20, false), end, 0, cfg, "weekday")
	legacy := ev
	legacy.ID, legacy.Evidence = "legacy", `{"source":"vm","dwell":2}`
	evs := []chstore.MLEvent{ev, legacy}
	budget := lagRecordMaxPerTick
	attachLagRecords(context.Background(), deps, cfg, evs, false, func(string) time.Time { return firstTick }, nil, &budget, now)
	if evs[1].Record != "" || vm.ranges != 1 || budget != lagRecordMaxPerTick-1 {
		t.Fatalf("damgasız olaya kayıt/sorgu yok: record=%q sorgu=%d bütçe=%d", evs[1].Record, vm.ranges, budget)
	}
	if want := svcImpactEnd(now, cfg.IngestLagSec).Unix(); vm.to != want || vm.from != end.Add(-impactWindow*time.Minute-lagRecordPad).Unix() {
		t.Fatalf("pencere ölçülen son tam dakikada bitmeli: from=%d to=%d (want to %d)", vm.from, vm.to, want)
	}
	var rec EventRecord
	if err := json.Unmarshal([]byte(evs[0].Record), &rec); err != nil {
		t.Fatalf("kayıt yazılmalı: %v", err)
	}
	if vs := ValidateRecord(rec); len(vs) != 0 {
		t.Fatalf("doğrulayıcıdan geçmeli: %+v", vs)
	}
	// 10:10 dakikasının kapanış okuması 10:11'dedir: onset o nokta
	if rec.OnsetAt != t0.Add(11*time.Minute).UnixNano() || rec.OnsetAt != ev.StartedAt+int64(time.Minute) || evs[0].OnsetAt != rec.OnsetAt ||
		rec.DetectedAt != end.UnixNano() || rec.ValueAtDetection != 165000 || rec.Baseline.Hi != 1200 || rec.Baseline.Lo != 0 || rec.ExpectedValue != 600 {
		t.Fatalf("kayıt kararla tutarlı olmalı: onset=%s det=%s hi=%v", time.Unix(0, rec.OnsetAt).UTC(), time.Unix(0, rec.DetectedAt).UTC(), rec.Baseline.Hi)
	}
	if evs[0].PublishState != PublishPublished || evs[0].Class != ClassServiceAnomaly {
		t.Fatalf("yayın durumu: %+v", evs[0])
	}
	// operatör ifadeyi değiştirdiyse kayıt yazılmaz (eski davranış)
	changed := chstore.DefaultMLConfig()
	fams := map[string][]chstore.MLMetricExpr{}
	for k, v := range changed.MetricFamilies {
		fams[k] = append([]chstore.MLMetricExpr(nil), v...)
	}
	for i, e := range fams[detMetricKafka] {
		if e.ID == kafkaLagExprID {
			fams[detMetricKafka][i].Expr = "sum by ({svc}) (kafka_consumer_records_lag_max)"
		}
	}
	changed.MetricFamilies = fams
	evs2 := []chstore.MLEvent{ev}
	attachLagRecords(context.Background(), deps, changed, evs2, false, func(string) time.Time { return firstTick }, nil, &budget, now)
	if evs2[0].Record != "" || vm.ranges != 1 {
		t.Fatal("değiştirilmiş ifadede kayıt yazılmamalı")
	}
}

// Kalan örnek: seri hiç bant dışına çıkmıyor (kayıt sorgusu sapmayı
// göstermiyor) → onset seriden türemez → publish_state=invalid.
func TestLagRecordInvalidWhenSeriesNeverLeavesBand(t *testing.T) {
	rec, ok := BuildLagRecord(lagTestInput(false, func(int) float64 { return 600 }))
	if !ok {
		t.Fatal("kayıt kurulmalı")
	}
	vs := ValidateRecord(rec)
	found := false
	for _, v := range vs {
		found = found || v.Rule == RuleOnsetFromSerie
	}
	if !found {
		t.Fatalf("onset_from_series ihlali beklenir: %+v", vs)
	}
	raw, _ := json.Marshal(rec)
	ev := applyRecordContractShadow(chstore.MLEvent{ID: "x", Detector: "metric_kafka", Record: string(raw), PublishState: PublishPublished})
	if ev.PublishState != PublishInvalid {
		t.Fatalf("geçersiz kayıt yayımlanmamalı: %q", ev.PublishState)
	}
}

// Kart: geçerli kayıt varsa başlık yalnız kayıttan, panel tepesi parçası
// (Value) yok → FE withPanelPeak başlığı yeniden yazmaz. Kayıt geçersizse
// ya da yoksa eski yol aynen.
func TestCardTitleFromRecord(t *testing.T) {
	rec, _ := BuildLagRecord(lagTestInput(false, lagSpike))
	raw, _ := json.Marshal(rec)
	at := time.Date(2026, 11, 15, 12, 45, 0, 0, time.UTC)
	ev := chstore.MLEvent{ID: "e1", Detector: "metric_kafka", Signal: chstore.MLSignalMetric, Service: "orders-api",
		Code: kafkaTopicPart("orders-events"), StartedAt: at.UnixNano(), LastSeen: at.Add(5 * time.Minute).UnixNano(),
		WindowSec: 300, Observed: 500, Baseline: 100, Direction: "up", Record: string(raw), PublishState: PublishPublished}
	c := BuildExplainCard(CardInputFromEvent(ev, at.Add(10*time.Minute), time.UTC))
	if c.Summary != rec.Title || c.Value != nil {
		t.Fatalf("başlık kayıttan: %q / value=%v", c.Summary, c.Value)
	}
	ev.PublishState = PublishInvalid
	if c := BuildExplainCard(CardInputFromEvent(ev, at.Add(10*time.Minute), time.UTC)); c.Summary == rec.Title || c.Value == nil {
		t.Fatal("geçersiz kayıtta eski yol")
	}
}

func TestLagSeriesQuery(t *testing.T) {
	if got := lagSeriesQuery("service_name", `a"b`, ""); got != `max by (service_name) (kafka_consumer_records_lag_max{service_name="a\"b"})` {
		t.Fatal(got)
	}
	if got := lagSeriesQuery("svc", "s", "t"); got != `max by (svc, topic) (kafka_consumer_records_lag_max{svc="s", topic="t"})` {
		t.Fatal(got)
	}
}
