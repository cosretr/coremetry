package chstore

import (
	"strings"
	"testing"
	"time"
)

func TestThinningSettingsValidate(t *testing.T) {
	ok := DefaultThinningSettings()
	if err := ok.Validate(); err != nil {
		t.Fatalf("varsayılan geçerli olmalı: %v", err)
	}
	ok.Mode = ThinningModeShadow
	if err := ok.Validate(); err != nil {
		t.Fatalf("shadow geçerli olmalı: %v", err)
	}
	bad := []ThinningSettings{
		{Mode: ThinningModeOn, KeepPercent: 10, FullDays: 7}, // ayrılmış
		{Mode: "delete", KeepPercent: 10, FullDays: 7},
		{Mode: ThinningModeShadow, KeepPercent: 101, FullDays: 7},
		{Mode: ThinningModeShadow, KeepPercent: -1, FullDays: 7},
		{Mode: ThinningModeShadow, KeepPercent: 10, FullDays: 90},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v reddedilmeliydi", c)
		}
	}
	if n := (ThinningSettings{}).normalize(); n != DefaultThinningSettings() {
		t.Errorf("boş blob varsayılana dönmeli: %+v", n)
	}
}

func TestThinningClassifyPriority(t *testing.T) {
	slow := ThinTrace{HashBucket: 50, DurNs: 2000, OpP99Ns: 1000, OpSamples: 500}
	cases := []struct {
		name string
		in   ThinTrace
		want int
	}{
		{"hash bandında, hata da olsa hash'e sayılır", ThinTrace{HashBucket: 3, Errors: 4}, ThinClassHash},
		{"hash sınırı dışlayıcı", ThinTrace{HashBucket: 10}, ThinClassDrop},
		{"hata", ThinTrace{HashBucket: 50, Errors: 1, IsRef: true}, ThinClassError},
		{"referans", ThinTrace{HashBucket: 50, IsRef: true, DurNs: 2000, OpP99Ns: 1000, OpSamples: 500}, ThinClassRef},
		{"yavaş kuyruk", slow, ThinClassSlow},
		{"p99'a eşit yavaş değil", ThinTrace{HashBucket: 50, DurNs: 1000, OpP99Ns: 1000, OpSamples: 500}, ThinClassDrop},
		{"az örnekli operasyon yavaş sayılmaz", ThinTrace{HashBucket: 50, DurNs: 2000, OpP99Ns: 1000, OpSamples: 99}, ThinClassDrop},
		{"sıradan", ThinTrace{HashBucket: 99}, ThinClassDrop},
	}
	for _, c := range cases {
		if got := ThinningClassify(c.in, 10); got != c.want {
			t.Errorf("%s: got %d want %d", c.name, got, c.want)
		}
	}
	if ThinningClassify(ThinTrace{HashBucket: 99}, 100) != ThinClassHash {
		t.Error("keepPercent=100 her şeyi tutmalı")
	}
}

// SQL ikizi Go kuralıyla aynı sırayı ve eşikleri taşımalı.
func TestThinningClassSQLMirrorsGo(t *testing.T) {
	got := thinningClassSQL(10)
	for _, want := range []string{
		"cityHash64(trace_id) % 100 < 10, 1",
		"errs > 0, 2",
		"is_ref, 3",
		"n_op >= 100 AND p99_op > 0 AND dur > p99_op, 4",
		", 0)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("SQL %q içermiyor: %s", want, got)
		}
	}
	if strings.Index(got, "errs > 0") > strings.Index(got, "is_ref") {
		t.Error("öncelik sırası bozuk")
	}
}

func TestThinningSampleSQLBounded(t *testing.T) {
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	q := thinningOuterSQL(thinningSampleSQL(thinningSampleBuckets(day), 7, true), 10)
	for _, want := range []string{"time_bucket IN (toDateTime(", "sipHash64(trace_id) % 7 = 0", "trace_id NOT IN (?)", "LIMIT 5000000", "LIMIT 10", "FROM trace_summary_5m"} {
		if !strings.Contains(q, want) {
			t.Errorf("örneklem SQL %q içermiyor", want)
		}
	}
	if strings.Contains(q, "FROM spans") {
		t.Error("ham spans okunmamalı")
	}
	if strings.Contains(thinningSampleSQL(thinningSampleBuckets(day), 1, false), "NOT IN") {
		t.Error("referans yokken dışlama olmamalı")
	}
	r := thinningRefSQL(thinningHourBuckets(map[int64]bool{day.Unix(): true}))
	if !strings.Contains(r, "trace_id IN (?)") || !strings.Contains(r, "LIMIT 2000") {
		t.Errorf("referans SQL sınırsız: %s", r)
	}
}

func TestThinningSampleBuckets(t *testing.T) {
	day := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	b := thinningSampleBuckets(day)
	if len(b) != 24 {
		t.Fatalf("saat başına bir kova: %d", len(b))
	}
	seen := map[int]bool{}
	for h, ts := range b {
		if ts.Hour() != h || ts.Minute()%5 != 0 || ts.Day() != 2 {
			t.Errorf("kova %d hatalı: %s", h, ts)
		}
		seen[ts.Minute()] = true
	}
	if len(seen) < 6 {
		t.Errorf("ofset dönmüyor: %v", seen)
	}
	hb := thinningHourBuckets(map[int64]bool{day.Add(5 * time.Hour).Unix(): true, day.Add(time.Hour).Unix(): true})
	if len(hb) != 24 || hb[0] != day.Add(time.Hour) || hb[12] != day.Add(5*time.Hour) {
		t.Errorf("saat kovaları sırasız/eksik: %v", hb)
	}
}

func TestThinningSampleDen(t *testing.T) {
	if d := thinningSampleDen(330_000, 1.0/12); d != 1 {
		t.Errorf("lokal ölçek kesin olmalı: %d", d)
	}
	// prod: 2.3 milyar span/gün, 1/12 kova → ~191M span → ~9.
	if d := thinningSampleDen(2_300_000_000, 1.0/12); d < 5 || d > 15 {
		t.Errorf("prod bölen beklenmedik: %d", d)
	}
	if d := thinningSampleDen(1<<62, 1); d != 1000 {
		t.Errorf("tavan 1000: %d", d)
	}
}

func TestThinningEstimate(t *testing.T) {
	// Örneklem: 100 trace / 1000 span; hash 10 trace/100 span, hata 5/200,
	// yavaş 1/50; geri kalan 84 trace/650 span düşer.
	var s thinSampleAgg
	s.Traces, s.Spans = 100, 1000
	s.Classes[ThinClassHash] = ThinClassCount{10, 100}
	s.Classes[ThinClassError] = ThinClassCount{5, 200}
	s.Classes[ThinClassSlow] = ThinClassCount{1, 50}
	s.Classes[ThinClassDrop] = ThinClassCount{84, 650}
	// Referans: 2 trace / 20 span, ref sınıfı (kesin).
	var r thinSampleAgg
	r.Traces, r.Spans = 2, 20
	r.Classes[ThinClassRef] = ThinClassCount{2, 20}

	// ölçek = den 2 / kova payı 0.5 = 4 → örneklem 400 trace / 4000 span.
	d := thinningEstimate(s, r, 2, 0.5, 8040, 1_000_000)
	if d.Traces != 402 {
		t.Errorf("traces: %d", d.Traces)
	}
	if d.KeepTraces != 4*16+2 {
		t.Errorf("keepTraces: %d", d.KeepTraces)
	}
	// tutulan span oranı = (4*350+20)/(4000+20) = 1420/4020
	wantFrac := 1420.0 / 4020
	if got := float64(d.KeepSpans) / 8040; got < wantFrac-0.001 || got > wantFrac+0.001 {
		t.Errorf("keepSpans oranı %f, beklenen %f", got, wantFrac)
	}
	if want := uint64((1-wantFrac)*1_000_000 + 0.5); d.BytesSaved != want {
		t.Errorf("bytesSaved %d want %d", d.BytesSaved, want)
	}
	if d.ByClass["ref"].Traces != 2 || d.ByClass["hash"].Traces != 40 || d.ByClass["slow"].Traces != 4 {
		t.Errorf("sınıflar: %+v", d.ByClass)
	}
	// sınıf span'leri kesin toplama ölçeklenir: hash 400/4020*8040 = 800.
	if d.ByClass["hash"].Spans != 800 {
		t.Errorf("hash spans %d", d.ByClass["hash"].Spans)
	}
	// boş gün: bölme yok, tasarruf 0.
	z := thinningEstimate(thinSampleAgg{}, thinSampleAgg{}, 0, 0, 0, 0)
	if z.BytesSaved != 0 || z.KeepSpans != 0 || z.SampleDen != 1 {
		t.Errorf("boş gün: %+v", z)
	}
}

func TestThinningBandAndNext(t *testing.T) {
	now := time.Date(2026, 3, 31, 15, 0, 0, 0, time.UTC)
	band := thinningBand(now, 7, 30)
	if len(band) != 23 {
		t.Fatalf("7–30 bandı 23 gün: %d", len(band))
	}
	if band[0].Format("2006-01-02") != "2026-03-23" || band[22].Format("2006-01-02") != "2026-03-01" {
		t.Errorf("band uçları: %s … %s", band[0], band[22])
	}
	if len(thinningBand(now, 7, 7)) != 0 {
		t.Error("retention ≤ fullDays → bant boş")
	}
	have := map[string]ThinningDay{}
	if got := thinningNextDay(band, have, 10, now); got != "2026-03-23" {
		t.Errorf("ilk ölçülecek en yeni gün: %s", got)
	}
	fresh := now.UnixMilli()
	for _, d := range band {
		have[d.Format("2006-01-02")] = ThinningDay{KeepPercent: 10, MeasuredAt: fresh}
	}
	if got := thinningNextDay(band, have, 10, now); got != "" {
		t.Errorf("hepsi taze: %s", got)
	}
	if got := thinningNextDay(band, have, 20, now); got != "2026-03-23" {
		t.Errorf("keepPercent değişti → yeniden: %s", got)
	}
	have["2026-03-10"] = ThinningDay{KeepPercent: 10, MeasuredAt: now.Add(-8 * 24 * time.Hour).UnixMilli()}
	if got := thinningNextDay(band, have, 10, now); got != "2026-03-10" {
		t.Errorf("bayat ölçüm yeniden: %s", got)
	}
	have["2026-03-10"] = ThinningDay{KeepPercent: 10, MeasuredAt: now.Add(-time.Hour).UnixMilli(), Failed: true}
	if got := thinningNextDay(band, have, 10, now); got != "" {
		t.Errorf("yeni başarısız gün hemen yeniden denenmez: %s", got)
	}
	have["2026-03-10"] = ThinningDay{KeepPercent: 10, MeasuredAt: now.Add(-7 * time.Hour).UnixMilli(), Failed: true}
	if got := thinningNextDay(band, have, 10, now); got != "2026-03-10" {
		t.Errorf("6 sa sonra yeniden: %s", got)
	}
}

func TestThinningMergeAndTotals(t *testing.T) {
	now := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	band := thinningBand(now, 7, 10) // 3 gün: 23, 22, 21
	old := []ThinningDay{
		{Day: "2026-03-01", Spans: 999}, // bandın dışı → budanır
		{Day: "2026-03-22", Spans: 10, Bytes: 100, BytesSaved: 50, ByClass: map[string]ThinClassCount{"hash": {1, 2}}},
		{Day: "2026-03-21", Failed: true, Spans: 5},
	}
	nd := &ThinningDay{Day: "2026-03-23", Spans: 20, Bytes: 200, BytesSaved: 150, ByClass: map[string]ThinClassCount{"hash": {3, 4}}}
	got := thinningMergeDays(old, band, nd)
	if len(got) != 3 || got[0].Day != "2026-03-23" || got[2].Day != "2026-03-21" {
		t.Fatalf("birleşim: %+v", got)
	}
	tot := thinningTotals(got)
	if tot.Days != 2 || tot.Spans != 30 || tot.BytesSaved != 200 || tot.ByClass["hash"].Traces != 4 {
		t.Errorf("toplam (başarısız gün hariç): %+v", tot)
	}
}
