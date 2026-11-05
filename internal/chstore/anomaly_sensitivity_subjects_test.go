package chstore

// v0.10.1790 — Davis paritesi FAZ 1.2: anomaly_sensitivity.subjects +
// seasonalTimezone. Varsayılan: üç özne türü KAPALI (F1.5 eval kapısı), dilim
// Europe/Istanbul (karar 3). Eski blob (alanlar yok) varsayılanı alır; PUT
// yolunda Normalize alanları KOPYALAMAZSA açılan özne her kayıtta kapanırdı.

import (
	"encoding/json"
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
			if got.Endpoint != c.want || got.DB != c.want || got.Queue != c.want {
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
