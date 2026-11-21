package ml

// volume_test.go — v0.10.2242 (Dilim B): telemetri hacmi dedektörünün saf
// karar tablosu, kayan pencere simülasyonu, olay kaydı + kapı sınıflaması
// (örnek 1 negatif fixture: hacim sıçradı, hata/gecikme sabit → servis
// anomalisi DEĞİL, telemetry_volume).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestJudgeVolume(t *testing.T) {
	baseRatio := []float64{4, 4.2, 3.9, 4.1, 4, 3.8}
	baseSpan := []float64{120000, 118000, 125000, 121000, 119000, 122000} // saatlik
	cases := []struct {
		name    string
		in      volInput
		fire    bool
		measure string
		dir     string
	}{
		{"oran sıçradı → olay (ratio, up)", volInput{Spans: rep(40000, 3), Entries: rep(2500, 3), HourSpans: 200000, BaseRatio: baseRatio, BaseSpan: baseSpan, Z: 4}, true, volPartRatio, "up"},
		{"trafik 3 kat, oran sabit → olay yok", volInput{Spans: rep(30000, 3), Entries: rep(7500, 3), HourSpans: 360000, BaseRatio: baseRatio, BaseSpan: baseSpan, Z: 4}, false, "", ""},
		{"oran düştü → olay (ratio, down)", volInput{Spans: rep(2500, 3), Entries: rep(2500, 3), HourSpans: 30000, BaseRatio: baseRatio, BaseSpan: baseSpan, Z: 4}, true, volPartRatio, "down"},
		{"tek kova bant içinde → dwell kalır", volInput{Spans: []float64{40000, 10000, 40000}, Entries: rep(2500, 3), HourSpans: 200000, BaseRatio: baseRatio, BaseSpan: baseSpan, Z: 4}, false, "", ""},
		{"giriş span'i yok → ham span ölçüsü", volInput{Spans: rep(60000, 3), Entries: rep(0, 3), HourSpans: 700000, BaseSpan: baseSpan, Z: 4}, true, volPartSpans, "up"},
		{"küçük servis → hacim tabanı", volInput{Spans: rep(400, 3), Entries: rep(10, 3), HourSpans: 1600, BaseRatio: baseRatio, BaseSpan: []float64{500, 400, 600, 450}, Z: 4}, false, "", ""},
		{"taban yetersiz → sessiz", volInput{Spans: rep(40000, 3), Entries: rep(2500, 3), HourSpans: 200000, BaseRatio: baseRatio[:2], BaseSpan: baseSpan[:2], Z: 4}, false, "", ""},
		// Örnek kapısı (2026-11-21): ~7 istek/sn servis, %5 + tüm ERROR trace →
		// kovada ~110 örneklenmiş giriş span'i; oran bandı aşsa da olay yok.
		{"az örnek (110 giriş) → oran olayı yok", volInput{Spans: rep(2420, 3), Entries: rep(110, 3), HourSpans: 29000, BaseRatio: []float64{12, 13, 14, 12, 13, 14}, BaseSpan: rep(26000, 6), Z: 4}, false, "", ""},
		{"tek kova az örnek → olay ilerlemez", volInput{Spans: []float64{25000, 1500, 25000}, Entries: []float64{2500, 150, 2500}, HourSpans: 200000, BaseRatio: baseRatio, BaseSpan: baseSpan, Z: 4}, false, "", ""},
		{"sınırda örnek (200 giriş) → oran olayı", volInput{Spans: rep(2000, 3), Entries: rep(volMinEntries, 3), HourSpans: 24000, BaseRatio: baseRatio, BaseSpan: rep(24000, 6), Z: 4}, true, volPartRatio, "up"},
		{"aşağı yönde tam susma → olay yok", volInput{Spans: []float64{100, 0, 100}, Entries: rep(0, 3), HourSpans: 50000, BaseSpan: baseSpan, Z: 4}, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := judgeVolume(c.in)
			if j.Fire != c.fire {
				t.Fatalf("Fire = %v, beklenen %v (%s; bant [%v, %v], cur %v)", j.Fire, c.fire, j.Reason, j.Lo, j.Hi, j.Cur)
			}
			if c.fire && (j.Measure != c.measure || j.Dir != c.dir) {
				t.Fatalf("ölçü/yön = %s/%s, beklenen %s/%s", j.Measure, j.Dir, c.measure, c.dir)
			}
		})
	}
}

// Kayan pencere: oran T anında 4 → 10'a çıkar; ilk olay T + 2 kova
// (dwell 3), onset T'nin kovası.
func TestVolumeSlidingWindow(t *testing.T) {
	baseRatio := []float64{4, 4.2, 3.9, 4.1, 4, 3.8}
	const n, jump = 24, 12
	ts := make([]int64, n)
	spans, entries := make([]float64, n), make([]float64, n)
	for i := range ts {
		ts[i] = int64(1_800_000_000 + i*volBucketSec)
		entries[i] = 2500
		spans[i] = 10000
		if i >= jump {
			spans[i] = 25000
		}
	}
	first := -1
	for k := volDwell - 1; k < n; k++ {
		j := judgeVolume(volInput{Spans: spans[k-volDwell+1 : k+1], Entries: entries[k-volDwell+1 : k+1], HourSpans: 200000, BaseRatio: baseRatio, BaseSpan: []float64{120000, 121000, 119000, 122000}, Z: 4})
		if j.Fire {
			first = k
			vals := make([]float64, k+1)
			for i := range vals {
				vals[i] = spans[i] / entries[i]
			}
			if on := volOnsetBucket(ts[:k+1], vals, j.Lo, j.Hi, j.Dir); on != ts[jump] {
				t.Fatalf("onset = %d, beklenen sıçrama kovası %d", on, ts[jump])
			}
			break
		}
	}
	if first != jump+volDwell-1 {
		t.Fatalf("ilk olay kovası %d, beklenen %d", first, jump+volDwell-1)
	}
}

func TestVolHourRows(t *testing.T) {
	h := int64(1_800_000_000) / 3600 * 3600
	var rows []chstore.MLVolumeRow
	for i := int64(0); i < 14; i++ { // tam saat + 2 kova
		rows = append(rows, chstore.MLVolumeRow{Service: "checkout-api", Bucket: h + i*volBucketSec, Spans: 100, Entries: 25})
	}
	out := volHourRows(rows, h+14*volBucketSec)
	if len(out) != 2 {
		t.Fatalf("satır = %d, beklenen 2 (yalnız tam saat: spans + ratio)", len(out))
	}
	for _, r := range out {
		if r.Signal != volSignal || r.BucketSec != 3600 || r.Bucket.Unix() != h {
			t.Fatalf("satır %+v", r)
		}
		if (r.Part == volPartSpans && r.Count != 1200) || (r.Part == volPartRatio && r.Count != 4) {
			t.Fatalf("değer %+v", r)
		}
	}
}

// volFixture — örnek 1 biçimi: 10:00–11:24 penceresi, oran 10:45'te 4 → 10.
func volFixture(errAfter, latAfter float64) VolRecordInput {
	t0 := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	var rows []chstore.MLVolumeRow
	for b := t0.Add(-5 * time.Minute); !b.After(t0.Add(80 * time.Minute)); b = b.Add(5 * time.Minute) {
		spans := uint64(10000)
		if !b.Before(t0.Add(45 * time.Minute)) {
			spans = 25000
		}
		rows = append(rows, chstore.MLVolumeRow{Service: "checkout-api", Bucket: b.Unix(), Spans: spans, Entries: 2500})
	}
	var errP, latP []chstore.SpanMetricPoint
	for m := t0; !m.After(t0.Add(84 * time.Minute)); m = m.Add(time.Minute) {
		e, l := 0.5, 120.0
		if !m.Before(t0.Add(45 * time.Minute)) {
			e, l = errAfter, latAfter
		}
		errP = append(errP, chstore.SpanMetricPoint{Time: m.UnixNano(), Value: e})
		latP = append(latP, chstore.SpanMetricPoint{Time: m.UnixNano(), Value: l})
	}
	return VolRecordInput{Service: "checkout-api", Measure: volPartRatio, Direction: "up", Expected: 4, Lo: 2, Hi: 6, CriticalZ: 4,
		DetectedAt: t0.Add(75 * time.Minute), Rows: rows, From: t0, To: t0.Add(84 * time.Minute),
		ErrQuery: "100 * sum(rate(err)) / sum(rate(count))", LatQuery: "histogram_quantile(0.95, …)", ErrPoints: errP, LatPoints: latP}
}

func TestVolumeRecordTelemetryVolume(t *testing.T) {
	rec, ok := BuildVolumeRecord(volFixture(0.5, 120))
	if !ok {
		t.Fatal("kayıt kurulamadı")
	}
	if vs := ValidateRecord(rec); len(vs) > 0 {
		t.Fatalf("doğrulayıcı: %+v", vs)
	}
	if rec.Class != ClassTelemetryVolume {
		t.Fatalf("sınıf %s, beklenen telemetry_volume (hata/gecikme bant içinde)", rec.Class)
	}
	if want := time.Date(2026, 11, 2, 10, 45, 0, 0, time.UTC).UnixNano(); rec.OnsetAt != want {
		t.Fatalf("onset %s, beklenen gerçek değişim anı 10:45", time.Unix(0, rec.OnsetAt).UTC())
	}
	if len(rec.Context) != 2 {
		t.Fatalf("yan seri sayısı %d, beklenen 2", len(rec.Context))
	}
	gates := EvaluateGates(rec)
	o := gateVerdict(rec, gates)
	if o.WouldClass != ClassTelemetryVolume || o.Drop {
		t.Fatalf("kapı kararı %+v, beklenen telemetry_volume", o)
	}
	for _, g := range gates {
		if g.Name == GateImpact && (g.Passed || g.NotEvaluable) {
			t.Fatalf("etki kapısı %+v, beklenen değerlendirilmiş + etki yok", g)
		}
	}
	if !strings.HasPrefix(rec.Title, "İstek başına span tespit anında 10, normal üst sınır 6") {
		t.Fatalf("başlık %q", rec.Title)
	}
}

func TestVolumeRecordWithImpact(t *testing.T) {
	rec, ok := BuildVolumeRecord(volFixture(6, 120)) // hata oranı %0,5 → %6
	if !ok {
		t.Fatal("kayıt kurulamadı")
	}
	if vs := ValidateRecord(rec); len(vs) > 0 {
		t.Fatalf("doğrulayıcı: %+v", vs)
	}
	if rec.Class != ClassServiceAnomaly {
		t.Fatalf("sınıf %s, beklenen service_anomaly (hata oranı arttı)", rec.Class)
	}
	if o := gateVerdict(rec, EvaluateGates(rec)); o.WouldClass == ClassTelemetryVolume {
		t.Fatalf("etkili hacim olayı telemetry_volume sayıldı: %+v", o)
	}
}

func TestVolumeRecordNoCompanions(t *testing.T) {
	in := volFixture(0.5, 120)
	in.ErrPoints, in.LatPoints = nil, nil
	rec, _ := BuildVolumeRecord(in)
	// 2026-11-21: hacim ölçüsü tek başına service_anomaly değil; etki kanıtı
	// (yan serinin bandı aşması) yoksa telemetry_volume.
	if rec.Class != ClassTelemetryVolume {
		t.Fatalf("yan seri yokken sınıf %s, beklenen telemetry_volume (etki kanıtı yok)", rec.Class)
	}
	o := gateVerdict(rec, EvaluateGates(rec))
	if o.WouldClass != ClassTelemetryVolume || o.Drop {
		t.Fatalf("yan seri yokken kapı kararı %+v, beklenen telemetry_volume", o)
	}
	in.Measure = volPartSpans // giriş span'i yok: her zaman telemetry_volume
	in.Expected, in.Lo, in.Hi = 10000, 5000, 15000
	rec, ok := BuildVolumeRecord(in)
	if !ok || rec.Class != ClassTelemetryVolume {
		t.Fatalf("span ölçüsünde sınıf %s", rec.Class)
	}
	if vs := ValidateRecord(rec); len(vs) > 0 {
		t.Fatalf("doğrulayıcı: %+v", vs)
	}
	if o := gateVerdict(rec, EvaluateGates(rec)); o.WouldClass != ClassTelemetryVolume {
		t.Fatalf("span ölçüsü kapı kararı %+v", o)
	}
}

// Tek yan seri: bant içindeyse telemetry_volume (eskiden service_anomaly),
// bandı aşıyorsa service_anomaly (etki kanıtı tek seriyle de yeter).
func TestVolumeRecordOneCompanion(t *testing.T) {
	in := volFixture(0.5, 120)
	in.LatPoints = nil
	if rec, _ := BuildVolumeRecord(in); rec.Class != ClassTelemetryVolume || len(rec.Context) != 1 {
		t.Fatalf("tek yan seri bant içinde: sınıf %s, yan seri %d", rec.Class, len(rec.Context))
	}
	in = volFixture(6, 120)
	in.LatPoints = nil
	rec, _ := BuildVolumeRecord(in)
	if rec.Class != ClassServiceAnomaly {
		t.Fatalf("hata oranı bandı aştı: sınıf %s, beklenen service_anomaly", rec.Class)
	}
	if o := gateVerdict(rec, EvaluateGates(rec)); o.WouldClass == ClassTelemetryVolume {
		t.Fatalf("etkili tek yan seri telemetry_volume sayıldı: %+v", o)
	}
}

func TestVolumeRegimeExemptAndGroupExcluded(t *testing.T) {
	q := chstore.DefaultMLQuality()
	if regimeLimit(detTelemetryVolume, q) != 7*24*time.Hour {
		t.Fatal("hacim dedektörü rejim kuralından muaf olmalı (tavan 7 gün, K15)")
	}
	if regimeLimit("op_latency", q) != time.Duration(q.RegimeHours)*time.Hour {
		t.Fatal("diğer dedektörler RegimeHours")
	}
	if !groupExcluded(chstore.MLEvent{Detector: detTelemetryVolume}) {
		t.Fatal("hacim dedektörü grup/Problem kapısında sayılmamalı")
	}
	if chstore.MLDetectorSignal(detTelemetryVolume) != chstore.MLSignalTrace {
		t.Fatal("hacim dedektörünün sinyali trace")
	}
}
