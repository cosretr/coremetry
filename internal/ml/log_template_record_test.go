package ml

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.2253 (H13) — şablon dedektörleri (template_spike, rare_template) ve
// oluşum türleri (new_template, rare_error_code) olay kaydı.

func tplTestInput(det string, closed bool, val func(i int) float64) CPRecordInput {
	in := logTestInput(detLogVolume, "up", closed, val)
	in.Detector, in.Part = det, tplPartPrefix+"connection pool exhausted for upstream"
	if det == detRareTemplate {
		for i := range in.Lo {
			in.Lo[i], in.Hi[i] = 0, 0
		}
	}
	return in
}

func TestTemplateRecordContract(t *testing.T) {
	spike := func(i int) float64 {
		switch {
		case i == 18:
			return 5000
		case i >= 16 && i < 22:
			return 3000
		}
		return 1000
	}
	rare := func(i int) float64 {
		if i >= 16 && i < 22 {
			return 40
		}
		return -1 // aile yokken satır yok → 0
	}
	for _, c := range []struct {
		det    string
		val    func(int) float64
		peak   float64
		method string
	}{{detTemplateSpike, spike, 5000, lagBandMethod}, {detRareTemplate, rare, 40, tplRareBandMethod}} {
		for _, closed := range []bool{false, true} {
			in := tplTestInput(c.det, closed, c.val)
			rec, ok := BuildTemplateRecord(in, "connection pool exhausted for upstream")
			if !ok {
				t.Fatal("kayıt kurulmalı")
			}
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s closed=%v doğrulayıcıdan geçmeli: %+v", c.det, closed, vs)
			}
			if rec.OnsetAt != (in.T0+16*300)*1e9 || rec.PeakValue != c.peak || rec.Baseline.Method != c.method || rec.Volume == nil ||
				!strings.Contains(rec.Title, `"connection pool exhausted for upstream"`) || !strings.Contains(rec.Series.Query, "'tpl|connection pool exhausted for upstream'") ||
				rec.Class != ClassServiceAnomaly || rec.Evidence[0].Independent {
				t.Fatalf("%s: %+v", c.det, rec)
			}
			raw, _ := json.Marshal(rec)
			var back EventRecord
			if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back, rec) {
				t.Fatalf("gidiş-dönüş: %v", err)
			}
		}
	}
	// İfadede sayı → ifadesiz başlık, kayıt yine geçer.
	in := tplTestInput(detTemplateSpike, false, spike)
	rec, _ := BuildTemplateRecord(in, "upstream returned HTTP 503 again")
	if strings.Contains(rec.Title, "503") || len(ValidateRecord(rec)) != 0 || !strings.Contains(rec.Evidence[0].Summary, "503") {
		t.Fatalf("sayılı ifade: %q %+v", rec.Title, ValidateRecord(rec))
	}
}

func occEvents(now time.Time) []chstore.MLEvent {
	cfg := chstore.MLConfig{BucketSec: 300, CriticalZ: 4, DwellBuckets: 2}
	nt := newTemplateMLEvent(anomaly.LogTemplateAnomaly{TemplateID: "t-42", Template: "payment gateway timeout after <*> ms", Service: "checkout-api",
		FirstSeenNs: now.Add(-8 * time.Minute).UnixNano(), LastSeenNs: now.Add(-time.Minute).UnixNano(), TotalCount: 37}, cfg, now)
	rc := chstore.MLEvent{ID: "rc", Detector: detRareErrorCode, Signal: chstore.MLSignalLog, Service: "payments-api", Code: "503",
		StartedAt: now.Add(-10 * time.Minute).UnixNano(), LastSeen: now.Add(-5 * time.Minute).UnixNano(), Observed: 12,
		Evidence: mergeEvidence("", map[string]any{"field": "error.code", "bucketSec": 300})}
	return []chstore.MLEvent{nt, rc}
}

func TestOccurrenceRecordContract(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	cfg := chstore.MLConfig{BucketSec: 300}
	for _, closed := range []bool{false, true} {
		evs := occEvents(now)
		attachOccurrenceRecords(cfg, evs, closed, func(string) time.Time { return now.Add(-2 * time.Minute) }, nil, now)
		for _, ev := range evs {
			if ev.Record == "" || ev.PublishState != PublishPublished {
				t.Fatalf("%s: kayıt yazılmadı", ev.Detector)
			}
			var rec EventRecord
			_ = json.Unmarshal([]byte(ev.Record), &rec)
			if vs := ValidateRecord(rec); len(vs) != 0 {
				t.Fatalf("%s doğrulayıcıdan geçmeli: %+v", ev.Detector, vs)
			}
			if rec.Series.Kind != SeriesKindOccurrence || rec.OnsetAt > rec.DetectedAt || rec.Volume == nil || *rec.Volume != rec.PeakValue ||
				rec.Baseline.Hi != 0 || len(rec.Snapshot.V) != 0 || (closed != (rec.EndedAt != 0)) {
				t.Fatalf("%s: %+v", ev.Detector, rec)
			}
			// Kart eski yolda kalır (snapshot yok); yazım yolu kaydı geçirir.
			if CardRecordView(&ev, CardInput{}) != nil {
				t.Fatalf("%s: oluşum kaydı kart grafiğine girmemeli", ev.Detector)
			}
			if got := applyRecordContractShadow(ev); got.PublishState != PublishPublished {
				t.Fatalf("%s: %q", ev.Detector, got.PublishState)
			}
			// Gölge: seri kapıları değerlendirilemez, tek bağımlı kanıt → info.
			r := ReplayRecord(ev.ID, ev.Detector, ev.Record)
			if r.Outcome != ReplayInfo || !sameSet(r.NotEvalGate, []string{GateSeriesConsistency, GateVisibleDeviation, GateImpact}) {
				t.Fatalf("%s gölge: %+v", ev.Detector, r)
			}
		}
		nt, rc := evs[0], evs[1]
		var a, b EventRecord
		_ = json.Unmarshal([]byte(nt.Record), &a)
		_ = json.Unmarshal([]byte(rc.Record), &b)
		if !strings.Contains(a.Title, "payment gateway timeout") || a.OnsetAt != now.Add(-8*time.Minute).UnixNano() ||
			a.Series.Source != "ch:log_templates" || !strings.Contains(a.Series.Query, "'t-42'") || a.PeakValue != 37 {
			t.Fatalf("new_template: %+v", a)
		}
		// Sayısal kod başlığa girmez (sayı kuralı), kanıtta kalır.
		if strings.Contains(b.Title, "503") || !strings.Contains(b.Evidence[0].Summary, "503") || !strings.Contains(b.Series.Query, "'code|503'") {
			t.Fatalf("rare_error_code: %+v", b)
		}
	}
	// Kanıtı eksik eski olay: kayıt yazılmaz.
	old := []chstore.MLEvent{{ID: "o", Detector: detNewTemplate, Service: "x", Observed: 3}}
	attachOccurrenceRecords(cfg, old, false, func(string) time.Time { return now }, nil, now)
	if old[0].Record != "" {
		t.Fatal("templateId yoksa kayıt yazılmamalı")
	}
}
