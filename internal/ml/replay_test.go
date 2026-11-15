package ml

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v0.10.2252 — Dilim E: replay koşumu. Sentetik fikstürler (uydurma veri,
// nötr adlar) testdata/replay/ altında; `-update-replay` ile bu dosyadaki
// kurucularla yeniden üretilir:
//
//	go test ./internal/ml -run TestReplayWriteSynthetic -update-replay
//
// Operatörün dışa aktardığı gerçek olaylar (§7.1) testdata/fixtures/ altına
// gider (.gitignore: commit edilmez, gerçek servis adları taşır);
// TestReplayExportedFixtures varsa okur.

var updateReplay = flag.Bool("update-replay", false, "testdata/replay sentetik fikstürlerini yeniden yaz")

const replaySynthDir = "testdata/replay"

type synthCase struct {
	dir, id, detector, signal, service string
	rec                                *EventRecord // nil = sözleşmesiz eski olay
	expect                             ReplayExpect
}

// latencyRec — 1 dk p95 serisi, bant [0, 120], beklenen 80; out indeksleri
// değer, onset/tespit/bitiş dakika ofseti (T0 = 09:00).
func latencyRec(svc, op string, seriesStep uint32, out map[int]float64, onset, det, end int, vol float64, ev []RecordEvidence) EventRecord {
	t0 := recAt(9, 0)
	sn := series1m(t0, 75, 0, 120, func(i int) float64 {
		if v, ok := out[i]; ok {
			return v
		}
		return 80
	})
	sn.Aggregation = "p95"
	peak := 0.0
	for i := onset; i <= end; i++ {
		peak = max(peak, sn.V[i])
	}
	at := func(m int) int64 { return t0 + int64(m)*int64(time.Minute) }
	return EventRecord{V: RecordVersion, Class: ClassServiceAnomaly, Direction: "up",
		Subject: RecordSubject{EntityID: "svc:" + svc, Service: svc, Operation: op, Resolved: true},
		Series: RecordSeries{Source: "vm", Aggregation: "p95", Unit: "ms", StepSec: seriesStep,
			Query: `histogram_quantile(0.95, sum by (le)(rate(http_server_duration_bucket{service="` + svc + `",route="` + op + `"}[1m])))`},
		Baseline: RecordBaseline{Expected: 80, Lo: 0, Hi: 120, Method: "class_days_hour±1_median_mad", StepSec: 60},
		OnsetAt:  at(onset), DetectedAt: at(det), EndedAt: at(end),
		PeakValue: peak, ValueAtDetection: sn.V[det], ExpectedValue: 80, Volume: f64(vol),
		Evidence: ev, Snapshot: sn,
		Title: op + " p95 " + trInt(peak) + " ms (tespit anında " + trInt(sn.V[det]) + " ms; beklenen 80 ms; " + trInt(vol) + " çağrı)"}
}

var twoIndependent = []RecordEvidence{
	{Kind: "exception", Source: "ch:spans", Summary: "Upstream bağlantı zaman aşımı istisnası", Independent: true},
	{Kind: "rollout", Source: "deploys", Summary: "Başlangıçtan 2 dk önce dağıtım", Independent: true},
}

func synthCases() []synthCase {
	// 1) temiz gecikme olayı: 10 dk bant dışı, 900 çağrı, iki bağımsız kanıt.
	clean := latencyRec("checkout-api", "POST /checkout", 60,
		map[int]float64{30: 200, 31: 210, 32: 220, 33: 200, 34: 230, 35: 240, 36: 220, 37: 200, 38: 190, 39: 170},
		30, 33, 40, 900, twoIndependent)
	// 2) tek kovalık sıçrama: 5 dk seri kovası (09:30–09:35) bant dışı.
	blip := latencyRec("cart-api", "GET /cart", 300,
		map[int]float64{30: 180, 31: 180, 32: 180, 33: 180, 34: 180}, 30, 34, 35, 600, twoIndependent)
	// 3) tek bağımsız kanıtlı hata oranı olayı → info.
	t0 := recAt(9, 0)
	at := func(m int) int64 { return t0 + int64(m)*int64(time.Minute) }
	errSn := series1m(t0, 75, 0, 2, func(i int) float64 {
		switch {
		case i == 33:
			return 8
		case i >= 30 && i <= 37:
			return 6
		}
		return 0.5
	})
	errSn.Aggregation = "err_ratio"
	single := EventRecord{V: RecordVersion, Class: ClassServiceAnomaly, Direction: "up",
		Subject: RecordSubject{EntityID: "svc:payments-api", Service: "payments-api", Operation: "POST /pay", Resolved: true},
		Series: RecordSeries{Source: "vm", Aggregation: "err_ratio", Unit: "%", StepSec: 60,
			Query: `100 * sum(rate(http_server_duration_count{service="payments-api",route="POST /pay",status=~"5.."}[1m])) / sum(rate(http_server_duration_count{service="payments-api",route="POST /pay"}[1m]))`},
		Baseline: RecordBaseline{Expected: 0.5, Lo: 0, Hi: 2, Method: "class_days_hour±1_median_mad", StepSec: 60},
		OnsetAt:  at(30), DetectedAt: at(31), EndedAt: at(38),
		PeakValue: 8, ValueAtDetection: 6, ExpectedValue: 0.5, Volume: f64(1500),
		Evidence: twoIndependent[:1], Snapshot: errSn,
		Title: "POST /pay hata oranı %8 (tespit anında %6; beklenen %0,5)"}
	// 4) yalnız hacim: istek başına span 3 → 12, hata oranı + gecikme bant içinde.
	volSn := series1m(t0, 75, 2.4, 3.6, func(i int) float64 {
		if i >= 30 {
			return 12
		}
		return 3
	})
	volSn.Aggregation = volAggRatio
	flat := func(v, lo, hi float64) RecordSnapshot {
		s := series1m(t0, 75, lo, hi, func(int) float64 { return v })
		s.Aggregation = volSn.Aggregation
		return s
	}
	volume := EventRecord{V: RecordVersion, Class: ClassTelemetryVolume, Direction: "up",
		Subject: RecordSubject{EntityID: "svc:inventory-api", Service: "inventory-api", Resolved: true},
		Series: RecordSeries{Source: "ch:spanmetrics_1m", Aggregation: volAggRatio, Unit: "span/request", StepSec: 60,
			Query: "countMerge(calls_state) / countMergeIf(calls_state, kind IN ('server','consumer')) BY 1m WHERE service_name = 'inventory-api'"},
		Baseline: RecordBaseline{Expected: 3, Lo: 2.4, Hi: 3.6, Method: "class_days_hour±1_weekly", StepSec: 60},
		OnsetAt:  at(30), DetectedAt: at(45),
		PeakValue: 12, ValueAtDetection: 12, ExpectedValue: 3, VolumeReason: "seri hacmin kendisi",
		Evidence: twoIndependent[1:], Snapshot: volSn,
		Context: []RecordContext{
			{Measure: ctxErrorRate, Source: "vm", Query: "err_ratio(inventory-api)", Unit: "%", Method: "pre_event_window", Snapshot: flat(0.4, 0, 1)},
			{Measure: ctxLatency, Source: "vm", Query: "p95(inventory-api)", Unit: "ms", Method: "pre_event_window", Snapshot: flat(60, 0, 90)},
		},
		Title: "inventory-api istek başına span 3'ten 12'ye çıktı"}
	// 5) geçersiz kayıt: tepe seriyle uyuşmuyor (seri 240, alan 180).
	bad := latencyRec("search-api", "GET /search", 60,
		map[int]float64{30: 200, 31: 210, 32: 220, 33: 200, 34: 240, 35: 200}, 30, 33, 36, 700, twoIndependent)
	bad.PeakValue = 180
	bad.Title = strings.Replace(bad.Title, "p95 240 ms", "p95 180 ms", 1)
	return []synthCase{
		{"latency_publish", "a1000000000000001", "op_latency", "trace", "checkout-api", &clean,
			ReplayExpect{Outcome: ReplayPublish, FailedGates: []string{}}},
		{"latency_one_bucket_blip", "a1000000000000002", "op_latency", "trace", "cart-api", &blip,
			ReplayExpect{Outcome: ReplayDrop, FailedGates: []string{GateVisibleDeviation}}},
		{"error_rate_single_evidence", "a1000000000000003", "op_error_rate", "trace", "payments-api", &single,
			ReplayExpect{Outcome: ReplayInfo, FailedGates: []string{GateEvidence}}},
		{"volume_only", "a1000000000000004", "svc_telemetry_volume", "trace", "inventory-api", &volume,
			ReplayExpect{Outcome: ReplayTelemetryVolume, FailedGates: []string{GateEvidence, GateImpact}}},
		{"invalid_record", "a1000000000000005", "op_latency", "trace", "search-api", &bad,
			ReplayExpect{Outcome: ReplayInvalid, Violations: []string{RulePeakFromSeries}}},
		{"legacy_no_record", "a1000000000000006", "svc_volume_spike", "trace", "orders-api", nil,
			ReplayExpect{Outcome: ReplayNoRecord}},
	}
}

// synthRow — ClickHouse JSONEachRow biçiminde ml_events satırı (§7.1 kolonları + kayıt kolonları).
func synthRow(c synthCase) ([]byte, error) {
	chTime := func(ns int64) string { return time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05.000000000") }
	row := map[string]any{"id": c.id, "detector": c.detector, "signal": c.signal, "stage": "shadow", "service": c.service,
		"level": "", "pod": "", "code": "", "window_sec": 300, "direction": "up", "evidence": "{}", "dup_of": "", "version": "1",
		"started_at": chTime(recAt(9, 30)), "last_seen": chTime(recAt(9, 45)), "baseline": 0, "observed": 0, "score": 0,
		"class": "", "publish_state": "", "onset_at": chTime(0), "record": ""}
	if c.rec != nil {
		b, err := json.Marshal(c.rec)
		if err != nil {
			return nil, err
		}
		row["record"], row["class"], row["publish_state"], row["onset_at"] = string(b), c.rec.Class, PublishPublished, chTime(c.rec.OnsetAt)
	}
	return json.Marshal(row)
}

func TestReplayWriteSynthetic(t *testing.T) {
	if !*updateReplay {
		t.Skip("-update-replay verilmedi")
	}
	for _, c := range synthCases() {
		d := filepath.Join(replaySynthDir, c.dir)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		row, err := synthRow(c)
		if err != nil {
			t.Fatal(err)
		}
		exp, _ := json.MarshalIndent(map[string]ReplayExpect{c.id: c.expect}, "", "  ")
		if err := os.WriteFile(filepath.Join(d, "ml_events.jsonl"), append(row, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "expect.json"), append(exp, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Dosyalar kurucularla aynı (fikstür elle bozulmadı / kurucu değişince yeniden üretildi).
func TestReplaySyntheticFilesCurrent(t *testing.T) {
	for _, c := range synthCases() {
		row, err := synthRow(c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(replaySynthDir, c.dir, "ml_events.jsonl"))
		if err != nil || !bytes.Equal(bytes.TrimSpace(got), row) {
			t.Errorf("%s: fikstür kurucudan farklı (go test ./internal/ml -run TestReplayWriteSynthetic -update-replay): %v", c.dir, err)
		}
	}
}

func TestReplaySynthetic(t *testing.T) {
	rep, err := ReplayDir(replaySynthDir)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := rep.WriteText(&buf); err != nil {
		t.Fatal(err)
	}
	t.Log("\n" + buf.String())
	if len(rep.Events) != len(synthCases()) {
		t.Fatalf("olay sayısı %d", len(rep.Events))
	}
	for _, e := range rep.Events {
		if !e.Expected || len(e.Mismatch) > 0 {
			t.Errorf("%s %s: beklenti %v %v", e.Fixture, e.ID, e.Expected, e.Mismatch)
		}
	}
	lat := rep.ByDetector["op_latency"]
	if lat.Evaluated != 2 || lat.WouldPublish != 1 || lat.WouldDrop != 1 || lat.Invalid != 1 || lat.NotEvaluated != 1 ||
		lat.FailedByGate[GateVisibleDeviation] != 1 {
		t.Errorf("op_latency: %+v", lat)
	}
	if r := rep.ByDetector["op_error_rate"]; r.WouldInfo != 1 || r.Evaluated != 1 {
		t.Errorf("op_error_rate: %+v", r)
	}
	if r := rep.ByDetector["svc_telemetry_volume"]; r.WouldTelemetryVolume != 1 {
		t.Errorf("svc_telemetry_volume: %+v", r)
	}
	if r := rep.ByDetector["svc_volume_spike"]; r.NotEvaluated != 1 || r.Invalid != 0 || r.Evaluated != 0 {
		t.Errorf("svc_volume_spike: %+v", r)
	}
	// Deterministik: ikinci koşu aynı metin.
	rep2, _ := ReplayDir(replaySynthDir)
	var buf2 bytes.Buffer
	_ = rep2.WriteText(&buf2)
	if buf.String() != buf2.String() {
		t.Fatal("replay deterministik değil")
	}
}

// Beklenti tutmazsa rapor söyler (koşum gerçekten denetliyor).
func TestReplayMismatchReported(t *testing.T) {
	e := ReplayRecord("x", "op_latency", "")
	if m := replayMismatch(e, ReplayExpect{Outcome: ReplayPublish}); len(m) != 1 {
		t.Fatalf("fark raporlanmadı: %v", m)
	}
	if m := replayMismatch(e, ReplayExpect{Outcome: ReplayNoRecord, FailedGates: []string{}}); len(m) != 0 {
		t.Fatalf("yanlış fark: %v", m)
	}
	if _, err := ReplayDir(filepath.Join(t.TempDir(), "yok")); err == nil {
		t.Fatal("olmayan dizin hata vermeli")
	}
}

// Operatörün dışa aktardığı olaylar (yerel, commit edilmez): varsa raporlanır;
// expect.json girdisi varsa tutmalı.
func TestReplayExportedFixtures(t *testing.T) {
	const dir = "testdata/fixtures"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("testdata/fixtures yok (§7.1 dışa aktarımı)")
	}
	rep, err := ReplayDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	_ = rep.WriteText(&buf)
	t.Log("\n" + buf.String())
	if rep.Mismatches > 0 {
		t.Fatalf("%d olayda beklenti tutmadı", rep.Mismatches)
	}
}

// Üretim kod yolu replay'i çağırmaz: ReplayDir / ReplayRecord yalnız replay.go,
// testler ve cmd/ml-replay'de geçer.
func TestReplayNotCalledFromProduction(t *testing.T) {
	out, err := exec.Command("git", "-C", "../..", "grep", "--untracked", "-l", "-E", `ReplayDir\(|ReplayRecord\(`, "--", "*.go").Output()
	if err != nil {
		t.Skipf("git grep: %v", err)
	}
	for _, f := range strings.Fields(string(out)) {
		if strings.HasSuffix(f, "_test.go") || f == "internal/ml/replay.go" || f == "cmd/ml-replay/main.go" {
			continue
		}
		t.Errorf("replay üretim kodundan çağrılıyor: %s", f)
	}
}
