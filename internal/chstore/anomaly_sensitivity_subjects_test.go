package chstore

// v0.10.1790 — Davis paritesi FAZ 1.2: anomaly_sensitivity.subjects +
// seasonalTimezone. Varsayılan: üç özne türü KAPALI (F1.5 eval kapısı), dilim
// Europe/Istanbul (karar 3). Eski blob (alanlar yok) varsayılanı alır; PUT
// yolunda Normalize alanları KOPYALAMAZSA açılan özne her kayıtta kapanırdı.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnomalySubjectsDefaults(t *testing.T) {
	d := DefaultAnomalySensitivity()
	for name, k := range map[string]AnomalySubjectKindConfig{"endpoint": d.Subjects.Endpoint, "db": d.Subjects.DB, "queue": d.Subjects.Queue} {
		if k.Enabled {
			t.Errorf("%s varsayılan AÇIK — eval kapısı geçmeden açılmaz", name)
		}
		if k.TopN != 20 || k.MaxSeriesPerTick != 2000 {
			t.Errorf("%s tavanları = %+v, want topN 20 / maxSeries 2000", name, k)
		}
	}
	if d.Subjects.AnyEnabled() {
		t.Error("AnyEnabled varsayılanda true")
	}
	if d.SeasonalTimezone != "Europe/Istanbul" {
		t.Errorf("SeasonalTimezone = %q, want Europe/Istanbul", d.SeasonalTimezone)
	}
	if n := NormalizeAnomalySensitivity(d); n.Subjects != d.Subjects || n.SeasonalTimezone != d.SeasonalTimezone {
		t.Errorf("varsayılan normalize'da değişti: %+v", n)
	}
}

func TestAnomalySubjectsOldBlobGetsDefaults(t *testing.T) {
	// v0.10.1789 dönemi blob: subjects / seasonalTimezone anahtarı yok.
	var c AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6}`), &c); err != nil {
		t.Fatal(err)
	}
	n := NormalizeAnomalySensitivity(c)
	if n.Subjects != DefaultAnomalySubjects() {
		t.Errorf("eski blob subjects = %+v, want varsayılan", n.Subjects)
	}
	if n.SeasonalTimezone != DefaultSeasonalTimezone {
		t.Errorf("eski blob seasonalTimezone = %q, want %q", n.SeasonalTimezone, DefaultSeasonalTimezone)
	}
}

func TestNormalizeAnomalySubjects(t *testing.T) {
	cases := []struct {
		name string
		in   AnomalySubjectKindConfig
		want AnomalySubjectKindConfig
	}{
		{"sıfır → varsayılan tavan", AnomalySubjectKindConfig{}, AnomalySubjectKindConfig{TopN: 20, MaxSeriesPerTick: 2000}},
		{"açık + geçerli aynen", AnomalySubjectKindConfig{Enabled: true, TopN: 5, MaxSeriesPerTick: 300}, AnomalySubjectKindConfig{Enabled: true, TopN: 5, MaxSeriesPerTick: 300}},
		{"üst sınır dahil", AnomalySubjectKindConfig{TopN: 200, MaxSeriesPerTick: 50000}, AnomalySubjectKindConfig{TopN: 200, MaxSeriesPerTick: 50000}},
		{"aralık dışı → varsayılan", AnomalySubjectKindConfig{Enabled: true, TopN: 201, MaxSeriesPerTick: -1}, AnomalySubjectKindConfig{Enabled: true, TopN: 20, MaxSeriesPerTick: 2000}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeAnomalySubjects(AnomalySubjectsConfig{Endpoint: c.in, DB: c.in, Queue: c.in})
			wantQ := c.want
			wantQ.LagMinRecords = 1000 // v0.10.1802 — yalnız queue taşır
			if got.Endpoint != c.want || got.DB != c.want || got.Queue != wantQ {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// TestAnomalySubjectsSurvivePutRoundTrip — açılan tür ve seçilen dilim
// Normalize → JSON → Normalize zincirinde (PUT → Save → Read) korunur.
func TestAnomalySubjectsSurvivePutRoundTrip(t *testing.T) {
	in := DefaultAnomalySensitivity()
	in.Subjects.Endpoint = AnomalySubjectKindConfig{Enabled: true, TopN: 7, MaxSeriesPerTick: 900}
	in.Subjects.Queue.Enabled = true
	in.SeasonalTimezone = "UTC"
	raw, err := json.Marshal(NormalizeAnomalySensitivity(in))
	if err != nil {
		t.Fatal(err)
	}
	var back AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	back = NormalizeAnomalySensitivity(back)
	if back.Subjects != in.Subjects || back.SeasonalTimezone != "UTC" {
		t.Errorf("gidiş-dönüşte kayıp: %+v / %q", back.Subjects, back.SeasonalTimezone)
	}
}

func TestSeasonalTimezoneValidateNormalize(t *testing.T) {
	for _, tz := range []string{"", "UTC", "Europe/Istanbul", "America/New_York"} {
		if err := ValidateSeasonalTimezone(tz); err != nil {
			t.Errorf("%q reddedildi: %v", tz, err)
		}
	}
	for _, tz := range []string{"Local", "local", "Mars/Olympus"} {
		err := ValidateSeasonalTimezone(tz)
		if err == nil {
			t.Errorf("%q kabul edildi", tz)
			continue
		}
		if got := err.Error(); len(got) < 16 || got[:16] != "seasonalTimezone" {
			t.Errorf("%q hata metni alan adıyla başlamıyor: %q", tz, got)
		}
	}
	for in, want := range map[string]string{"": DefaultSeasonalTimezone, "  UTC ": "UTC", "Mars/Olympus": DefaultSeasonalTimezone, "Local": DefaultSeasonalTimezone} {
		if got := normalizeSeasonalTimezone(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// v0.10.1797 (Davis FAZ 3 dilim 1) — özne terfi kipi: varsayılan off,
// bilinmeyen/boş → off (normalize), PUT kapısı bilinmeyeni reddeder.
func TestSubjectPromotionMode(t *testing.T) {
	if got := DefaultAnomalySubjects().PromotionMode; got != SubjectPromotionOff {
		t.Errorf("varsayılan kip %q, want off", got)
	}
	cases := []struct {
		in, norm string
		valid    bool
	}{
		{"", "off", true},
		{"off", "off", true},
		{"shadow", "shadow", true},
		{"live", "live", true},
		{" live ", "live", true},
		{"LIVE", "off", false},
		{"liv", "off", false},
	}
	for _, c := range cases {
		n := NormalizeAnomalySubjects(AnomalySubjectsConfig{PromotionMode: c.in})
		if n.PromotionMode != c.norm {
			t.Errorf("normalize(%q) = %q, want %q", c.in, n.PromotionMode, c.norm)
		}
		if err := ValidateSubjectPromotionMode(c.in); (err == nil) != c.valid {
			t.Errorf("validate(%q) err=%v, want valid=%v", c.in, err, c.valid)
		}
	}
	// Eski blob (alan yok) kapalı okunur.
	var c AnomalySensitivityConfig
	if err := json.Unmarshal([]byte(`{"subjects":{"endpoint":{"enabled":true}}}`), &c); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeAnomalySensitivity(c).Subjects.PromotionMode; got != SubjectPromotionOff {
		t.Errorf("eski blob kip %q, want off", got)
	}
}

// v0.10.1798 (FAZ 3 dilim 2) — özne terfi eşikleri: varsayılan 600 sn / 2,0;
// 0 ve aralık dışı varsayılana, sınırlar dahil.
func TestSubjectPromotionThresholds(t *testing.T) {
	d := DefaultAnomalySubjects()
	if d.PromotionMinSustainedSec != 600 || d.PromotionMinRatio != 2.0 {
		t.Fatalf("varsayılan = %d / %v, want 600 / 2", d.PromotionMinSustainedSec, d.PromotionMinRatio)
	}
	cases := []struct {
		sec       int
		ratio     float64
		wantSec   int
		wantRatio float64
	}{
		{0, 0, 600, 2},
		{60, 1, 60, 1},
		{86400, 1000, 86400, 1000},
		{59, 0.99, 600, 2},
		{86401, 1001, 600, 2},
		{900, 3.5, 900, 3.5},
	}
	for _, c := range cases {
		n := NormalizeAnomalySubjects(AnomalySubjectsConfig{PromotionMinSustainedSec: c.sec, PromotionMinRatio: c.ratio})
		if n.PromotionMinSustainedSec != c.wantSec || n.PromotionMinRatio != c.wantRatio {
			t.Errorf("(%d, %v) → (%d, %v), want (%d, %v)", c.sec, c.ratio, n.PromotionMinSustainedSec, n.PromotionMinRatio, c.wantSec, c.wantRatio)
		}
	}
}

// TestQueueLagMinRecords — v0.10.1802 (Davis dilim 3): queue.lagMinRecords
// varsayılan 1000, aralık dışı → 1000, endpoint / db'de hep 0 (JSON'a yazılmaz).
func TestQueueLagMinRecords(t *testing.T) {
	if d := DefaultAnomalySubjects(); d.Queue.LagMinRecords != 1000 || d.Endpoint.LagMinRecords != 0 || d.DB.LagMinRecords != 0 {
		t.Errorf("varsayılan: %+v", d)
	}
	for in, want := range map[int]int{0: 1000, -5: 1000, 1: 1, 250: 250, 1_000_000_000: 1_000_000_000, 1_000_000_001: 1000} {
		got := NormalizeAnomalySubjects(AnomalySubjectsConfig{Queue: AnomalySubjectKindConfig{LagMinRecords: in}, DB: AnomalySubjectKindConfig{LagMinRecords: in}})
		if got.Queue.LagMinRecords != want || got.DB.LagMinRecords != 0 {
			t.Errorf("lagMinRecords %d → queue %d db %d, want %d / 0", in, got.Queue.LagMinRecords, got.DB.LagMinRecords, want)
		}
	}
	raw, _ := json.Marshal(DefaultAnomalySubjects())
	if s := string(raw); !strings.Contains(s, `"queue":{"enabled":false,"topN":20,"maxSeriesPerTick":2000,"lagMinRecords":1000}`) || strings.Count(s, "lagMinRecords") != 1 {
		t.Errorf("JSON şekli: %s", s)
	}
}
