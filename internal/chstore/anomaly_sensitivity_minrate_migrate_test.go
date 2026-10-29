package chstore

// v0.10.1585 — Davis paritesi FAZ 0.5 / A: minBaselineRate varsayılanı
// metrik başına küçük taban (error_rate 0.2 istek/sn, p99_ms 0.5 istek/sn,
// request_rate 0). Belirti (FP sınıfı a_low_traffic): varsayılan 0 = kapı
// kapalıydı; saatte birkaç isteklik serviste tek hata %50+ "hata oranı"
// anomalisi açıyordu. Normalize kayıtlı 0'ı "operatör 0 seçti" diye
// koruduğu için varsayılan değişikliği prod'a ulaşmaz → tek seferlik göç.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestDefaultMinBaselineRatePerMetric(t *testing.T) {
	d := DefaultAnomalySensitivity()
	want := map[string]float64{"error_rate": 0.2, "p99_ms": 0.5, "request_rate": 0}
	for m, w := range want {
		if got := d.Metrics[m].MinBaselineRate; got != w {
			t.Errorf("%s minBaselineRate = %v, want %v", m, got, w)
		}
	}
	// Normalize eksik metriğe yeni varsayılanı verir, açık 0'ı korur.
	n := NormalizeAnomalySensitivity(AnomalySensitivityConfig{Metrics: map[string]AnomalyMetricSensitivity{
		"p99_ms": {FloorPct: 0.1, AbsFloor: 10, MinMAD: 1, MinBaselineRate: 0},
	}})
	if n.Metrics["error_rate"].MinBaselineRate != 0.2 || n.Metrics["p99_ms"].MinBaselineRate != 0 {
		t.Errorf("normalize: %+v", n.Metrics)
	}
}

func TestPlanMinRateMigration(t *testing.T) {
	rate := func(raw []byte, m string) (float64, bool) {
		var b struct {
			Metrics map[string]map[string]json.RawMessage `json:"metrics"`
		}
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("çıktı çözülemedi: %v", err)
		}
		v, ok := b.Metrics[m]["minBaselineRate"]
		if !ok {
			return 0, false
		}
		var f float64
		_ = json.Unmarshal(v, &f)
		return f, true
	}
	cases := []struct {
		name        string
		raw         string
		wantOutcome string
		wantWrite   bool
		wantErr     float64
		wantP99     float64
	}{
		{name: "satır yok", raw: "", wantOutcome: MinRateMigrateNoRow},
		{name: "metrics yok → Normalize varsayılanı verir", raw: `{"dwellBuckets":3}`, wantOutcome: MinRateMigrateDefault},
		{
			name:        "ikisi de 0 → yeni taban",
			raw:         `{"dwellBuckets":4,"metrics":{"error_rate":{"floorPct":0.1,"absFloor":1,"minBaselineRate":0},"p99_ms":{"floorPct":0.1,"minBaselineRate":0},"request_rate":{"floorPct":0.15,"minBaselineRate":0}}}`,
			wantOutcome: MinRateMigrateApplied, wantWrite: true, wantErr: 0.2, wantP99: 0.5,
		},
		{
			name:        "sıfır olmayan değer korunur",
			raw:         `{"metrics":{"error_rate":{"minBaselineRate":1.5},"p99_ms":{"minBaselineRate":0}}}`,
			wantOutcome: MinRateMigrateApplied, wantWrite: true, wantErr: 1.5, wantP99: 0.5,
		},
		{
			name:        "ikisi de sıfır değil → dokunma",
			raw:         `{"metrics":{"error_rate":{"minBaselineRate":0.3},"p99_ms":{"minBaselineRate":2}}}`,
			wantOutcome: MinRateMigrateCustomised,
		},
		{
			name:        "metrik nesnesinde alan yok (v0.9.826 öncesi) = 0 okunur → yeni taban",
			raw:         `{"metrics":{"error_rate":{"floorPct":0.1},"p99_ms":{"floorPct":0.1,"minBaselineRate":0.7}}}`,
			wantOutcome: MinRateMigrateApplied, wantWrite: true, wantErr: 0.2, wantP99: 0.7,
		},
		{
			name:        "metrik nesnesi yok → dokunulmaz (Normalize varsayılanı verir)",
			raw:         `{"metrics":{"p99_ms":{"minBaselineRate":0}}}`,
			wantOutcome: MinRateMigrateApplied, wantWrite: true, wantP99: 0.5,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, outcome, err := planMinRateMigration([]byte(c.raw))
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if outcome != c.wantOutcome || (out != nil) != c.wantWrite {
				t.Fatalf("outcome=%q write=%v, want %q write=%v", outcome, out != nil, c.wantOutcome, c.wantWrite)
			}
			if !c.wantWrite {
				return
			}
			if g, ok := rate(out, "p99_ms"); !ok || g != c.wantP99 {
				t.Errorf("p99_ms = %v (%v), want %v", g, ok, c.wantP99)
			}
			if c.wantErr > 0 {
				if g, ok := rate(out, "error_rate"); !ok || g != c.wantErr {
					t.Errorf("error_rate = %v (%v), want %v", g, ok, c.wantErr)
				}
			} else if _, ok := rate(out, "error_rate"); ok {
				t.Errorf("eksik error_rate nesnesi yaratıldı")
			}
			// Diğer alanlar aynen.
			var before, after map[string]json.RawMessage
			_ = json.Unmarshal([]byte(c.raw), &before)
			_ = json.Unmarshal(out, &after)
			for k, v := range before {
				if k != "metrics" && string(after[k]) != string(v) {
					t.Errorf("alan %s değişti: %s → %s", k, v, after[k])
				}
			}
			if r, ok := rate(out, "request_rate"); ok && r != 0 {
				t.Errorf("request_rate değişti: %v", r)
			}
		})
	}
}

func TestMigrateMinRateDefault(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	saved := []byte(`{"criticalZ":6,"metrics":{"error_rate":{"floorPct":0.1,"absFloor":1,"minBaselineRate":0},"p99_ms":{"floorPct":0.1,"absFloor":10,"minMAD":1,"minBaselineRate":0},"request_rate":{"floorPct":0.15,"minBaselineRate":0}}}`)

	t.Run("0 → taban, audit + işaret; Normalize sonrası yeni değer; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: saved}}
		out, err := MigrateMinRateDefault(ctx, f, now)
		if err != nil || out != MinRateMigrateApplied {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var c AnomalySensitivityConfig
		if err := json.Unmarshal(f.kv[anomalySensitivityKey], &c); err != nil {
			t.Fatal(err)
		}
		n := NormalizeAnomalySensitivity(c)
		if n.Metrics["error_rate"].MinBaselineRate != 0.2 || n.Metrics["p99_ms"].MinBaselineRate != 0.5 ||
			n.Metrics["request_rate"].MinBaselineRate != 0 || n.CriticalZ != 6 || n.Metrics["p99_ms"].MinMAD != 1 {
			t.Fatalf("göç sonrası: %+v", n)
		}
		if len(f.audits) != 1 || f.audits[0].Action != "settings.anomaly_sensitivity.migrate" || f.audits[0].ActorID != "system" {
			t.Errorf("tek system audit: %+v", f.audits)
		}
		// Operatör göçten sonra 0'a çeker → işaret varken göç koşmaz.
		f.kv[anomalySensitivityKey] = saved
		writes := len(f.putKeys)
		if out, err := MigrateMinRateDefault(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: outcome=%q err=%v", out, err)
		}
	})

	t.Run("işaret okunamadı → yazım yok", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: saved}, getErr: map[string]error{minRateMigrateKey: errors.New("ch down")}}
		if _, err := MigrateMinRateDefault(ctx, f, now); err == nil || len(f.putKeys) != 0 {
			t.Errorf("err=%v yazım=%v", err, f.putKeys)
		}
	})

	t.Run("blob yazılamadı → işaret yok", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: saved}, putErr: map[string]error{anomalySensitivityKey: errors.New("ch down")}}
		if _, err := MigrateMinRateDefault(ctx, f, now); err == nil || len(f.kv[minRateMigrateKey]) != 0 {
			t.Errorf("işaret yazılmamalı: err=%v", err)
		}
	})
}
