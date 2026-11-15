package ml

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2199 — anomali kartı H1, kabul ölçütü 10 (docs/ml/anomaly-card-audit.md
// §11): iki prod örneğinin BUGÜNKÜ hâli doğrulayıcıdan nedenleriyle kalır,
// düzeltilmiş hâli geçer. Sayılar audit §1–§3'ten (prod satırı yok; fixture
// dışa aktarımı §7.1 gelince E diliminde gerçek seriyle değiştirilir).
// Örnek 3 (b4d95f…, K12) ayrıntı gelince eklenir.

var recDay = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func recAt(h, m int) int64 {
	return recDay.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute).UnixNano()
}

func f64(v float64) *float64 { return &v }

// series1m — T0'dan başlayan 1 dk seri; val(dakika ofseti) değeri, bant sabit.
func series1m(t0 int64, n int, lo, hi float64, val func(i int) float64) RecordSnapshot {
	sn := RecordSnapshot{T0: t0 / 1e9, StepSec: 60}
	for i := 0; i < n; i++ {
		sn.V = append(sn.V, val(i))
		sn.Lo = append(sn.Lo, lo)
		sn.Hi = append(sn.Hi, hi)
	}
	return sn
}

// Örnek 2 — 09ecef247b0557ea · callBagService op_latency p95. Bugün: seri tanımı
// saklanmaz, snapshot ml_series 5 dk + q25–q75 bandı, onset = kayan started_at
// (07:05), tepe 107 = son kova, 07:00 kovası 135 (pencere dışı), hacim yok.
func example2Today() EventRecord {
	vals := map[int]float64{50: 60, 55: 135, 60: 107, 65: 48} // dakika (06:05'ten) → değer
	sn := RecordSnapshot{T0: recAt(6, 5) / 1e9, StepSec: 300, Aggregation: "p95"}
	for i := 0; i < 14; i++ {
		v, ok := vals[i*5]
		if !ok {
			v = 43
		}
		sn.V, sn.Lo, sn.Hi = append(sn.V, v), append(sn.Lo, 38), append(sn.Hi, 52) // q25–q75
	}
	return EventRecord{V: RecordVersion, Class: ClassServiceAnomaly, Direction: "up",
		Subject:  RecordSubject{Service: "bsa-loan-interrogation-kkb-krs-inq-prod", Operation: "callBagService"},
		Baseline: RecordBaseline{Expected: 43, Lo: 0, Hi: 95, Method: "class_days_hour±1_median_mad", StepSec: 300},
		OnsetAt:  recAt(7, 5), DetectedAt: recAt(7, 5), EndedAt: recAt(7, 10),
		PeakValue: 107, ValueAtDetection: 107, ExpectedValue: 43,
		Snapshot: sn, Title: "callBagService p95 107 ms (normal 43 ms)"}
}

// Örnek 2 düzeltilmiş: 1 dk seri, onset = yükselişin başı (06:58), tepe 135,
// tespit 07:10 (100), banda dönüş 07:12, bant = alarm bandı, hacim 412 çağrı.
func example2Fixed() EventRecord {
	t0 := recAt(6, 28) // onset − 30 dk
	vals := map[int]float64{30: 110, 31: 118, 32: 125, 33: 130, 34: 135, 35: 128, 36: 120,
		37: 107, 38: 107, 39: 107, 40: 107, 41: 107, 42: 100, 43: 98, 44: 45}
	sn := series1m(t0, 75, 0, 95, func(i int) float64 {
		if v, ok := vals[i]; ok {
			return v
		}
		return 43
	})
	sn.Aggregation = "p95"
	return EventRecord{V: RecordVersion, Class: ClassServiceAnomaly, Direction: "up",
		Subject: RecordSubject{EntityID: "svc:bsa-loan-interrogation-kkb-krs-inq-prod", Service: "bsa-loan-interrogation-kkb-krs-inq-prod",
			Operation: "callBagService", Resolved: true},
		Series: RecordSeries{Source: "vm", Aggregation: "p95", Unit: "ms", StepSec: 60,
			Query: `histogram_quantile(0.95, sum by (le)(rate(http_server_duration_bucket{service="bsa-loan-interrogation-kkb-krs-inq-prod",route="callBagService"}[60s])))`},
		Baseline: RecordBaseline{Expected: 43, Lo: 0, Hi: 95, Method: "class_days_hour±1_median_mad", StepSec: 60},
		OnsetAt:  recAt(6, 58), DetectedAt: recAt(7, 10), EndedAt: recAt(7, 12),
		PeakValue: 135, ValueAtDetection: 100, ExpectedValue: 43, Volume: f64(412),
		Evidence: []RecordEvidence{{Kind: "exception", Source: "ch:spans", Summary: "KKB bağlantı zaman aşımı istisnası 06:58'den beri", Independent: true}},
		Snapshot: sn, Title: "callBagService p95 135 ms (tespit anında 100 ms; beklenen 43 ms; 412 çağrı)"}
}

// Örnek 1 — 59d8f32338b5e7b5 · bsa-psl-card-gain-prod svc_volume_spike. Bugün:
// seri ve snapshot yok, başlık iki 24 sa toplamı + oran, onset = eşik aşımının
// fark edildiği tik, tek kanıt satırı başlığın kendisi.
func example1Today() EventRecord {
	title := "bsa-psl-card-gain-prod span hacmi önceki 24 saatin 4,2 katı (son 24 sa 1.260.000 span)"
	return EventRecord{V: RecordVersion, Class: ClassServiceAnomaly, Direction: "up",
		Subject:  RecordSubject{Service: "bsa-psl-card-gain-prod"},
		Baseline: RecordBaseline{Expected: 300000, Method: "previous_24h"},
		OnsetAt:  recAt(10, 48), DetectedAt: recAt(10, 48), EndedAt: recAt(12, 50),
		PeakValue: 1260000, ValueAtDetection: 1260000, ExpectedValue: 300000,
		Evidence: []RecordEvidence{{Kind: "rule", Source: "ch:service_summary_5m", Summary: title + "."}},
		Title:    title}
}

// Örnek 1 düzeltilmiş: istek başına span (spanmetrics_1m), 1 dk seri, onset =
// gerçek değişim anı (bir önceki gün 12:20 varsayımı, audit S2), sınıf
// telemetry_volume, kanıt başlıktan farklı (rollout).
func example1Fixed() EventRecord {
	t0 := recAt(11, 50)
	sn := series1m(t0, 90, 2.4, 3.6, func(i int) float64 {
		switch {
		case i < 30:
			return 3
		case i == 45:
			return 12.9
		}
		return 12.6
	})
	sn.Aggregation = "spans_per_request"
	return EventRecord{V: RecordVersion, Class: ClassTelemetryVolume, Direction: "up",
		Subject: RecordSubject{EntityID: "svc:bsa-psl-card-gain-prod", Service: "bsa-psl-card-gain-prod", Resolved: true},
		Series: RecordSeries{Source: "ch:spanmetrics_1m", Aggregation: "spans_per_request", Unit: "span/istek", StepSec: 60,
			Query: "countMerge(calls_state) / countMergeIf(calls_state, kind IN ('server','consumer')) BY 1m WHERE service_name = 'bsa-psl-card-gain-prod'"},
		Baseline: RecordBaseline{Expected: 3, Lo: 2.4, Hi: 3.6, Method: "weekly_slot", StepSec: 60},
		OnsetAt:  recAt(12, 20), DetectedAt: recAt(12, 50),
		PeakValue: 12.9, ValueAtDetection: 12.6, ExpectedValue: 3, Volume: f64(18400),
		Evidence: []RecordEvidence{{Kind: "rollout", Source: "deploys", Summary: "12:18'de dağıtım (v2.4.1)", Independent: true}},
		Snapshot: sn, Title: "bsa-psl-card-gain-prod istek başına span 3'ten 12,9'a çıktı; istek hızı değişmedi"}
}

func rulesOf(vs []RecordViolation) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vs {
		if !seen[v.Rule] {
			seen[v.Rule] = true
			out = append(out, v.Rule)
		}
	}
	sort.Strings(out)
	return out
}

func TestValidateRecordProdExamples(t *testing.T) {
	cases := []struct {
		name string
		rec  EventRecord
		want []string // tam kural kümesi (boş = geçer)
	}{
		{"örnek1 bugün", example1Today(), []string{RuleEvidenceTitle, RuleOnsetFromSerie, RulePeakFromSeries, RuleSeriesDefined, RuleSnapshotShape, RuleTitleNumbers, RuleVolume}},
		{"örnek1 düzeltilmiş", example1Fixed(), nil},
		{"örnek2 bugün", example2Today(), []string{RuleBandMatches, RuleOnsetFromSerie, RuleSeriesDefined, RuleSnapshotAgg, RuleSnapshotShape, RuleVolume}},
		{"örnek2 düzeltilmiş", example2Fixed(), nil},
	}
	for _, c := range cases {
		vs := ValidateRecord(c.rec)
		got := rulesOf(vs)
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: kurallar %v, beklenen %v\n%+v", c.name, got, want, vs)
		}
		for _, v := range vs {
			if v.Reason == "" {
				t.Errorf("%s: %s nedensiz", c.name, v.Rule)
			}
		}
	}
}

// Kural başına tek bozma: düzeltilmiş örnek 2'den yalnız o kural kalır.
func TestValidateRecordSingleRule(t *testing.T) {
	cases := []struct {
		rule string
		mut  func(r *EventRecord)
	}{
		{RulePeakFromSeries, func(r *EventRecord) { r.PeakValue = 107; r.Title = strings.Replace(r.Title, "135", "107", 1) }},
		{RuleOnsetOrder, func(r *EventRecord) { r.OnsetAt = r.DetectedAt + int64(time.Minute) }},
		{RuleSnapshotAgg, func(r *EventRecord) { r.Snapshot.Aggregation = "p50" }},
		{RuleTitleNumbers, func(r *EventRecord) { r.Title = "callBagService p95 135 ms (normal 44 ms)" }},
		{RuleEvidenceTitle, func(r *EventRecord) {
			r.Evidence = append(r.Evidence, RecordEvidence{Kind: "rule", Summary: "  " + r.Title + ". "})
		}},
		{RuleDetectionValue, func(r *EventRecord) {
			r.ValueAtDetection = 107
			r.Title = strings.Replace(r.Title, "100 ms", "107 ms", 1)
		}},
		{RuleVolume, func(r *EventRecord) { r.Volume = nil; r.Title = strings.Replace(r.Title, "; 412 çağrı", "", 1) }},
		{RuleBandMatches, func(r *EventRecord) { r.Baseline.Hi = 96 }},
		{RuleClass, func(r *EventRecord) { r.Class = "" }},
		{RuleVersion, func(r *EventRecord) { r.V = 0 }},
	}
	for _, c := range cases {
		r := example2Fixed()
		c.mut(&r)
		if got := rulesOf(ValidateRecord(r)); strings.Join(got, ",") != c.rule {
			t.Errorf("%s: kurallar %v", c.rule, got)
		}
	}
	// Hacim yerine neden yazılırsa geçer.
	r := example2Fixed()
	r.Volume, r.VolumeReason, r.Title = nil, "kaynak çağrı sayısı taşımıyor", "callBagService p95 135 ms (tespit anında 100 ms; beklenen 43 ms)"
	if vs := ValidateRecord(r); len(vs) != 0 {
		t.Fatalf("hacim nedeni kabul edilmeli: %+v", vs)
	}
	// Yön aşağı: tepe = pencerenin en küçüğü.
	r = example2Fixed()
	r.Direction = "down"
	if got := rulesOf(ValidateRecord(r)); strings.Join(got, ",") != RulePeakFromSeries {
		t.Fatalf("aşağı yön: %v", got)
	}
	// Seyrek tür: seri kuralları uygulanmaz.
	r = example1Today()
	r.Series, r.Evidence, r.Volume = RecordSeries{Source: "es", Query: "q", Aggregation: "count", Unit: "olay", StepSec: 60, Kind: SeriesKindOccurrence}, nil, f64(1)
	r.Title = "yeni şablon görüldü"
	if vs := ValidateRecord(r); len(vs) != 0 {
		t.Fatalf("occurrence: %+v", vs)
	}
}

func TestValidateRecordSnapshotStep(t *testing.T) {
	// 720 nokta tavanı aşılacaksa 60'ın katı adım kabul (6 sa+ olay).
	r := example2Fixed()
	if vs := ValidateRecord(r); len(vs) != 0 {
		t.Fatal(vs)
	}
	r.Snapshot.StepSec = 120 // 75 nokta × 2 = 150 ≤ 720 → küçültülebilirdi
	if got := rulesOf(ValidateRecord(r)); !contains(got, RuleSnapshotShape) {
		t.Fatalf("120 sn adım reddedilmeli: %v", got)
	}
	sn := series1m(0, 500, 0, 1, func(int) float64 { return 0.5 })
	sn.StepSec = 120 // 500 × 2 = 1000 > 720 → büyütme meşru
	r2 := EventRecord{Snapshot: sn}
	for _, v := range validateSnapshot(r2) {
		if v.Rule == RuleSnapshotShape {
			t.Fatalf("meşru büyütme reddedildi: %v", v)
		}
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestParseTRNumber(t *testing.T) {
	cases := []struct {
		in  string
		v   float64
		dec int
	}{{"74.167", 74167, 0}, {"15.600", 15600, 0}, {"4,2", 4.2, 1}, {"1.234,5", 1234.5, 1}, {"107", 107, 0}, {"0.25", 0.25, 2}, {"1.260.000", 1260000, 0}}
	for _, c := range cases {
		v, dec, ok := parseTRNumber(c.in)
		if !ok || v != c.v || dec != c.dec {
			t.Errorf("%s → %v %d %v", c.in, v, dec, ok)
		}
	}
}

func TestTitleNumbersSkipsTimesAndDurations(t *testing.T) {
	r := EventRecord{PeakValue: 74167, ValueAtDetection: 15600,
		Title: "orders p95 tepe 74.167 · tespit anında 15.600 (14:05'ten beri, son 30 dk, 10.11.2026, v2)"}
	if bad := titleNumbersOutsideFields(r); len(bad) != 0 {
		t.Fatalf("atlanmalıydı: %v", bad)
	}
	r.Title = "tepe 74,5"
	if bad := titleNumbersOutsideFields(r); len(bad) != 1 {
		t.Fatalf("74,5 eşleşmemeli: %v", bad)
	}
}

func TestApplyRecordContract(t *testing.T) {
	before := global.recordInvalidCopy()["op_latency"]
	// Kaydı olmayan (eski / H1'deki her) olay aynen geçer.
	old := chstore.MLEvent{ID: "x", Detector: "op_latency", Observed: 107}
	if got := applyRecordContract(old); got != old {
		t.Fatalf("eski olay değişti: %+v", got)
	}
	ok, _ := json.Marshal(example2Fixed())
	ev := chstore.MLEvent{ID: "y", Detector: "op_latency", Record: string(ok)}
	if got := applyRecordContract(ev); got.PublishState != "" {
		t.Fatalf("geçen kayıt işaretlendi: %q", got.PublishState)
	}
	bad, _ := json.Marshal(example2Today())
	ev.Record = string(bad)
	if got := applyRecordContract(ev); got.PublishState != PublishInvalid {
		t.Fatalf("kalan kayıt invalid olmalı: %q", got.PublishState)
	}
	ev.Record = "{"
	if got := applyRecordContract(ev); got.PublishState != PublishInvalid {
		t.Fatalf("çözülemeyen kayıt invalid olmalı: %q", got.PublishState)
	}
	if n := global.recordInvalidCopy()["op_latency"] - before; n != 2 {
		t.Fatalf("sayaç +2 olmalı, +%d", n)
	}
	if got := Observability().RecordInvalid["op_latency"]; got < 2 {
		t.Fatalf("admin/stats sayacı görmüyor: %d", got)
	}
}

func TestRecordInvalidLine(t *testing.T) {
	if got := recordInvalidLine(nil); got != "[ml] tick record_invalid total=0 by_detector=" {
		t.Fatal(got)
	}
	if got := recordInvalidLine(map[string]int64{"op_latency": 2, "metric_kafka": 1}); got != "[ml] tick record_invalid total=3 by_detector=metric_kafka:1,op_latency:2" {
		t.Fatal(got)
	}
}
