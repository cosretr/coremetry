package ml

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2241 — anomali kartı A1 (audit §10 "kapı başına bir geçen bir kalan
// örnek"). Prod örnekleri nötr adlarla: örnek 2 = tek kovalık gecikme olayı,
// örnek 1 = kanıtı başlığın tekrarı olan hacim kaydı.

// gateLatRec — geçerli gecikme kaydı (örnek 2'nin düzeltilmiş biçimi, nötr ad):
// 14 dk bant dışı, 412 çağrı, fark 92 ms, iki bağımsız kanıt.
func gateLatRec() EventRecord {
	r := example2Fixed()
	r.Subject = RecordSubject{EntityID: "svc:svc-a", Service: "svc-a", Operation: "op-x", Resolved: true}
	r.Series.Query = `histogram_quantile(0.95, sum by (le)(rate(http_server_duration_bucket{service="svc-a",route="op-x"}[60s])))`
	r.Evidence = []RecordEvidence{
		{Kind: "series", Source: "vm", Summary: "Endpoint p95 serisi", Independent: false},
		{Kind: "exception", Source: "ch:spans", Summary: "Bağlantı zaman aşımı istisnası", Independent: true},
		{Kind: "rollout", Source: "deploys", Summary: "Olaydan 2 dk önce dağıtım", Independent: true},
	}
	r.Title = "op-x p95 135 ms (tespit anında 100 ms; beklenen 43 ms; 412 çağrı)"
	return r
}

// gateSingleBucket — örnek 2'nin prod hâli (nötr ad): tek dakikalık sıçrama,
// 40 çağrı. Doğrulayıcıdan geçer; görünür sapma (art arda 1 dk) ve hacim kalır.
func gateSingleBucket() EventRecord {
	r := gateLatRec()
	r.Snapshot = series1m(recAt(6, 28), 75, 0, 95, func(i int) float64 {
		if i == 30 {
			return 135
		}
		return 43
	})
	r.Snapshot.Aggregation = "p95"
	r.OnsetAt, r.DetectedAt, r.EndedAt = recAt(6, 58), recAt(6, 58), recAt(6, 59)
	r.PeakValue, r.ValueAtDetection, r.Volume = 135, 135, f64(40)
	r.Title = "op-x p95 135 ms (tespit anında 135 ms; beklenen 43 ms; 40 çağrı)"
	return r
}

func gateByName(gs []RecordGate, name string) RecordGate {
	for _, g := range gs {
		if g.Name == name {
			return g
		}
	}
	return RecordGate{Name: "yok:" + name}
}

func TestGateFixturesValid(t *testing.T) {
	for name, r := range map[string]EventRecord{"geçen": gateLatRec(), "tek kova": gateSingleBucket()} {
		if vs := ValidateRecord(r); len(vs) != 0 {
			t.Fatalf("%s doğrulayıcıdan kaldı: %+v", name, vs)
		}
	}
}

func TestGatesPassAndFail(t *testing.T) {
	pass := EvaluateGates(gateLatRec())
	for _, g := range pass {
		if !g.Passed || g.NotEvaluable {
			t.Fatalf("geçen kayıtta %s kaldı: %+v", g.Name, g)
		}
	}
	if len(pass) != 6 {
		t.Fatalf("gecikme kaydında 6 kapı beklenir: %d", len(pass))
	}

	badAgg := gateLatRec()
	badAgg.Snapshot.Aggregation = "avg"
	errDown := gateLatRec() // hata oranı düştü: görünür sapma var, servis etkisi yok
	errDown.Series.Unit, errDown.Series.Aggregation, errDown.Snapshot.Aggregation = "percent", "err_ratio", "err_ratio"
	errDown.Direction = "down"
	errDown.Snapshot = series1m(recAt(6, 28), 75, 2, 6, func(i int) float64 {
		if i >= 30 && i < 40 {
			return 0.5
		}
		return 4
	})
	absSmall := gateLatRec()
	absSmall.PeakValue = 60
	ex1 := example1Fixed() // örnek 1 prod hâli (nötr ad): kanıt = başlık + seri kaynağı
	ex1.Title = "svc-b istek başına span 3'ten 12,9'a çıktı"
	ex1.Evidence = []RecordEvidence{
		{Kind: "rule", Source: "deploys", Summary: ex1.Title + ".", Independent: true},
		{Kind: "breakdown", Source: ex1.Series.Source, Summary: "Span türü kırılımı", Independent: true},
	}
	cases := []struct {
		gate  string
		r     EventRecord
		value float64
	}{
		{GateSeriesConsistency, badAgg, 1},
		{GateVisibleDeviation, gateSingleBucket(), 1},
		{GateMinVolume, gateSingleBucket(), 40},
		{GateMinAbsDiff, absSmall, 17},
		{GateEvidence, ex1, 0},
		{GateImpact, errDown, 0},
	}
	for _, c := range cases {
		g := gateByName(EvaluateGates(c.r), c.gate)
		if g.Passed || g.NotEvaluable || g.Value != c.value || g.Reason == "" {
			t.Errorf("%s kalmalı (değer %v): %+v", c.gate, c.value, g)
		}
	}
	if g := gateByName(EvaluateGates(errDown), GateVisibleDeviation); !g.Passed {
		t.Errorf("hata oranı düşüşü görünür sapma: %+v", g)
	}
}

func TestGatesNotEvaluable(t *testing.T) {
	noVol := gateLatRec()
	noVol.Volume, noVol.VolumeReason = nil, "çağrı sayısı sorgusu veri döndürmedi"
	slow := gateLatRec()
	slow.Series.Unit, slow.Series.Aggregation = "percent", "slow_ratio"
	for _, c := range []struct {
		gate, want string
		r          EventRecord
	}{
		{GateMinVolume, "veri döndürmedi", noVol},
		{GateMinAbsDiff, "ms farkı tanımsız", slow},
		{GateImpact, "hata oranı / gecikme serisi yok", example1Fixed()},
	} {
		g := gateByName(EvaluateGates(c.r), c.gate)
		if !g.NotEvaluable || !g.Passed || !strings.Contains(g.Reason, c.want) {
			t.Errorf("%s değerlendirilemez olmalı: %+v", c.gate, g)
		}
	}
	// gecikme dışı seride hacim / mutlak fark satırı yok; eşik 1 nokta
	gs := EvaluateGates(example1Fixed())
	if gateByName(gs, GateMinVolume).Name != "yok:"+GateMinVolume {
		t.Fatalf("hacim kapısı yalnız gecikmede: %+v", gs)
	}
	if g := gateByName(gs, GateVisibleDeviation); !g.Passed || g.Threshold != 1 {
		t.Fatalf("hacim serisi görünür sapma: %+v", g)
	}
}

func TestGateEvidenceDerivedConfirm(t *testing.T) {
	r := gateLatRec()
	r.Series.Source = "ml:op_latency"
	r.Evidence = []RecordEvidence{{Kind: "confirm", Source: "ml", Summary: "a", Independent: true},
		{Kind: "rollout", Source: "deploys", Summary: "b", Independent: true}}
	if n := independentEvidence(r); n != 1 {
		t.Fatalf("türetilmiş serinin onayı bağımsız değil: %d", n)
	}
}

func TestGateVerdictAndReport(t *testing.T) {
	ok := gateLatRec()
	single := gateSingleBucket()
	weak := gateLatRec()
	weak.Evidence = weak.Evidence[:2]
	es := map[string]gateEntry{
		"a": {detector: "op_latency", o: gateVerdict(ok, EvaluateGates(ok))},
		"b": {detector: "op_latency", o: gateVerdict(single, EvaluateGates(single))},
		"c": {detector: "op_latency", o: gateVerdict(weak, EvaluateGates(weak))},
		"d": {detector: "metric_kafka", o: gateOutcome{}},
		"e": {detector: "metric_kafka", o: gateOutcome{Invalid: true}},
	}
	m := foldGateShadow(es)
	op := m["op_latency"]
	if op.Evaluated != 3 || op.WouldPublish != 1 || op.WouldDrop != 1 || op.WouldInfo != 1 ||
		op.FailedByGate[GateVisibleDeviation] != 1 || op.FailedByGate[GateMinVolume] != 1 || op.FailedByGate[GateEvidence] != 1 {
		t.Fatalf("op_latency raporu: %+v", op)
	}
	if k := m["metric_kafka"]; k.NotEvaluated != 2 || k.Invalid != 1 || k.Evaluated != 0 {
		t.Fatalf("kafka raporu: %+v", k)
	}
	line := gateShadowLine(m)
	if !strings.Contains(line, "evaluated=3 would_publish=1 would_drop=1 would_info=1 would_telemetry_volume=0 not_evaluated=2 invalid=1") ||
		!strings.Contains(line, "failed_by_gate=independent_evidence:1,min_volume:1,visible_deviation:1") {
		t.Fatal(line)
	}
	if got := gateShadowLine(nil); !strings.Contains(got, "evaluated=0") {
		t.Fatal(got)
	}
}

// Gölge: kapılar gates[]'e yazılır, sınıf / publish_state değişmez.
func TestApplyRecordContractWritesGatesShadow(t *testing.T) {
	single := gateSingleBucket()
	b, _ := json.Marshal(single)
	ev := chstore.MLEvent{ID: "gate-shadow-1", Detector: "op_latency", Class: ClassServiceAnomaly, Record: string(b)}
	got := applyRecordContract(ev)
	if got.PublishState != "" || got.Class != ClassServiceAnomaly {
		t.Fatalf("gölgede durum değişti: %q %q", got.PublishState, got.Class)
	}
	var rec EventRecord
	if err := json.Unmarshal([]byte(got.Record), &rec); err != nil || rec.Class != ClassServiceAnomaly {
		t.Fatalf("kayıt: %v %q", err, rec.Class)
	}
	if g := gateByName(rec.Gates, GateVisibleDeviation); g.Passed || g.Value != 1 {
		t.Fatalf("gates[] yazılmadı: %+v", rec.Gates)
	}
	if r := global.gateShadowReport(time.Now()); r["op_latency"].Evaluated < 1 || r["op_latency"].WouldDrop < 1 {
		t.Fatalf("gölge raporu olayı görmüyor: %+v", r["op_latency"])
	}
}
