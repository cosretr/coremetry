package chstore

// v0.10.2308 — Davis özne varsayılanı (endpoint + queue açık, gölge) ve
// mevcut kurulumlara tek seferlik geçiş. Kayıtlı blobun eksik subjects
// bölümü Normalize'da kapalıdır; yeni varsayılan oraya yalnız geçişle ulaşır.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func flipSubjects(t *testing.T, raw []byte) AnomalySubjectsConfig {
	t.Helper()
	var c AnomalySensitivityConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("blob çözülemedi: %v", err)
	}
	return NormalizeAnomalySensitivity(c).Subjects
}

func TestPlanSubjectsFlip(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		outcome string
		write   bool
	}{
		{"satır yok", "", SubjectsFlipNoRow, false},
		{"bölüm yok (eski blob) → geçiş", `{"dwellBuckets":4}`, SubjectsFlipApplied, true},
		{"bölüm null → geçiş", `{"subjects":null}`, SubjectsFlipApplied, true},
		{"üçü kapalı + kip off → geçiş", `{"subjects":{"endpoint":{"enabled":false,"topN":7,"maxSeriesPerTick":900},"db":{"enabled":false},"queue":{"enabled":false,"lagMinRecords":50},"promotionMode":"off","promotionMinRatio":3}}`, SubjectsFlipApplied, true},
		{"üçü kapalı + kip boş → geçiş", `{"subjects":{"endpoint":{"enabled":false},"promotionMode":""}}`, SubjectsFlipApplied, true},
		{"endpoint açık → dokunma", `{"subjects":{"endpoint":{"enabled":true},"promotionMode":"off"}}`, SubjectsFlipCustomised, false},
		{"yalnız db açık → dokunma", `{"subjects":{"db":{"enabled":true}}}`, SubjectsFlipCustomised, false},
		{"kip shadow → dokunma", `{"subjects":{"promotionMode":"shadow"}}`, SubjectsFlipCustomised, false},
		{"kip live → dokunma", `{"subjects":{"promotionMode":"live"}}`, SubjectsFlipCustomised, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, outcome, err := planSubjectsFlip([]byte(c.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if outcome != c.outcome || (out != nil) != c.write {
				t.Fatalf("outcome=%q write=%v, want %q write=%v", outcome, out != nil, c.outcome, c.write)
			}
			if !c.write {
				return
			}
			s := flipSubjects(t, out)
			if !s.Endpoint.Enabled || !s.Queue.Enabled || s.DB.Enabled || s.PromotionMode != SubjectPromotionShadow {
				t.Fatalf("geçiş sonrası: %+v", s)
			}
			// Diğer alanlar aynen.
			var before, after map[string]json.RawMessage
			_ = json.Unmarshal([]byte(c.raw), &before)
			_ = json.Unmarshal(out, &after)
			for k, v := range before {
				if k != subjectsField && string(after[k]) != string(v) {
					t.Errorf("alan %s değişti: %s → %s", k, v, after[k])
				}
			}
		})
	}
	// Tür nesnelerinin diğer anahtarları korunur.
	out, _, _ := planSubjectsFlip([]byte(cases[3].raw))
	s := flipSubjects(t, out)
	if s.Endpoint.TopN != 7 || s.Endpoint.MaxSeriesPerTick != 900 || s.Queue.LagMinRecords != 50 || s.PromotionMinRatio != 3 {
		t.Errorf("tür alanları korunmadı: %+v", s)
	}
}

func TestMigrateSubjectsDefaultFlip(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)
	pristine := []byte(`{"criticalZ":6,"subjects":{"endpoint":{"enabled":false},"db":{"enabled":false},"queue":{"enabled":false},"promotionMode":"off"}}`)

	t.Run("bakir → açık + gölge; tek audit; işaret bir kez; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: pristine}}
		out, err := MigrateSubjectsDefaultFlip(ctx, f, now)
		if err != nil || out != SubjectsFlipApplied {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		s := flipSubjects(t, f.kv[anomalySensitivityKey])
		if !s.Endpoint.Enabled || !s.Queue.Enabled || s.DB.Enabled || s.PromotionMode != SubjectPromotionShadow {
			t.Fatalf("geçiş sonrası: %+v", s)
		}
		if len(f.audits) != 1 || f.audits[0].ActorID != "system" || f.audits[0].ActorRole != "system" ||
			f.audits[0].Action != "settings.anomaly_sensitivity.migrate" {
			t.Errorf("tek system audit: %+v", f.audits)
		}
		var m minRateMigrateMarker
		if err := json.Unmarshal(f.kv[subjectsFlipKey], &m); err != nil || m.Outcome != SubjectsFlipApplied || m.Version != subjectsFlipVersion {
			t.Errorf("işaret: %s (%v)", f.kv[subjectsFlipKey], err)
		}
		// Operatör sonradan her şeyi elle kapatır → işaret varken geçiş koşmaz.
		f.kv[anomalySensitivityKey] = pristine
		writes := len(f.putKeys)
		for i := 0; i < 3; i++ {
			if out, err := MigrateSubjectsDefaultFlip(ctx, f, now); err != nil || out != "" {
				t.Errorf("işaret varken no-op: outcome=%q err=%v", out, err)
			}
		}
		if len(f.putKeys) != writes || len(f.audits) != 1 {
			t.Errorf("işaret varken yazım: %v audits=%d", f.putKeys[writes:], len(f.audits))
		}
		markerWrites := 0
		for _, k := range f.putKeys {
			if k == subjectsFlipKey {
				markerWrites++
			}
		}
		if markerWrites != 1 {
			t.Errorf("işaret %d kez yazıldı, want 1", markerWrites)
		}
		if s := flipSubjects(t, f.kv[anomalySensitivityKey]); s.AnyEnabled() {
			t.Errorf("elle kapatma geri açıldı: %+v", s)
		}
	})

	t.Run("operatör bir şey açmış → blob aynen, işaret yazılır, audit yok", func(t *testing.T) {
		custom := []byte(`{"subjects":{"db":{"enabled":true},"promotionMode":"off"}}`)
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: custom}}
		out, err := MigrateSubjectsDefaultFlip(ctx, f, now)
		if err != nil || out != SubjectsFlipCustomised {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if string(f.kv[anomalySensitivityKey]) != string(custom) || len(f.audits) != 0 || len(f.kv[subjectsFlipKey]) == 0 {
			t.Errorf("blob=%s audits=%d marker=%s", f.kv[anomalySensitivityKey], len(f.audits), f.kv[subjectsFlipKey])
		}
	})

	t.Run("satır yok → yazım yalnız işaret", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{}}
		if out, err := MigrateSubjectsDefaultFlip(ctx, f, now); err != nil || out != SubjectsFlipNoRow {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if len(f.putKeys) != 1 || f.putKeys[0] != subjectsFlipKey {
			t.Errorf("yazımlar: %v", f.putKeys)
		}
	})

	t.Run("işaret okunamadı → yazım yok", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: pristine}, getErr: map[string]error{subjectsFlipKey: errors.New("ch down")}}
		if _, err := MigrateSubjectsDefaultFlip(ctx, f, now); err == nil || len(f.putKeys) != 0 {
			t.Errorf("err=%v yazım=%v", err, f.putKeys)
		}
	})

	t.Run("blob yazılamadı → işaret yok", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: pristine}, putErr: map[string]error{anomalySensitivityKey: errors.New("ch down")}}
		if _, err := MigrateSubjectsDefaultFlip(ctx, f, now); err == nil || len(f.kv[subjectsFlipKey]) != 0 {
			t.Errorf("işaret yazılmamalı: err=%v", err)
		}
	})
}
