package chstore

// ml_force_on_migrate_test.go — v0.10.2074: op_latency + metric_kafka
// kayıtlı ML ayarında bir kez açılır (operatör kararı 2026-11-13). Belirti:
// v0.10.2059/2064 varsayılanı yalnız boş blobda açıyordu; prod'da kayıtlı
// `false` korunduğu için iki dedektör kapalı kalıyordu.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestPlanMLForceOn(t *testing.T) {
	names := mlForceOnDetectors
	cases := []struct {
		name    string
		raw     string
		outcome string
		flipped []string
	}{
		{"blob yok → varsayılan", "", MLForceOnNoRow, nil},
		{"detectors yok", `{"enabled":true}`, MLForceOnCurrent, nil},
		{"detectors null", `{"enabled":true,"detectors":null}`, MLForceOnCurrent, nil},
		{"ikisi de false → true", `{"enabled":true,"detectors":{"op_latency":false,"metric_kafka":false,"log_volume":false}}`, MLForceOnReplaced, []string{"metric_kafka", "op_latency"}},
		{"biri false", `{"detectors":{"op_latency":true,"metric_kafka":false}}`, MLForceOnReplaced, []string{"metric_kafka"}},
		{"ikisi de açık", `{"detectors":{"op_latency":true,"metric_kafka":true}}`, MLForceOnCurrent, nil},
		{"anahtar yok (varsayılan açık)", `{"detectors":{"log_volume":true}}`, MLForceOnCurrent, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, oc, fl, err := planMLForceOn([]byte(c.raw), names)
			if err != nil || oc != c.outcome || len(fl) != len(c.flipped) {
				t.Fatalf("outcome=%s flipped=%v err=%v", oc, fl, err)
			}
			for i := range fl {
				if fl[i] != c.flipped[i] {
					t.Fatalf("flipped=%v want %v", fl, c.flipped)
				}
			}
			if (out != nil) != (oc == MLForceOnReplaced) {
				t.Fatalf("yazım beklentisi tutmadı: %s", out)
			}
			if out == nil {
				return
			}
			var m map[string]any
			_ = json.Unmarshal(out, &m)
			d := m["detectors"].(map[string]any)
			if d["op_latency"] != true || d["metric_kafka"] != true {
				t.Fatalf("açılmadı: %s", out)
			}
			// ana anahtar ve diğer dedektörler korunur
			if c.name == "ikisi de false → true" && (m["enabled"] != true || d["log_volume"] != false) {
				t.Fatalf("diğer alanlar değişti: %s", out)
			}
		})
	}
	if _, _, _, err := planMLForceOn([]byte("{bozuk"), names); err == nil {
		t.Fatal("bozuk blob hata vermeli")
	}
}

func TestMigrateMLForceOn(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
	stored := []byte(`{"enabled":false,"detectors":{"op_latency":false,"metric_kafka":false}}`)

	t.Run("kayıtlı false → true bir kez; audit + işaret; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{mlSettingsKey: stored}}
		out, err := MigrateMLForceOn(ctx, f, now)
		if err != nil || out != MLForceOnReplaced {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if len(f.audits) != 1 || f.audits[0].Action != "settings.ml.migrate" || f.audits[0].ActorID != "system" {
			t.Fatalf("audit: %+v", f.audits)
		}
		cfg := NormalizeMLConfig(MLConfig{})
		_ = json.Unmarshal(f.kv[mlSettingsKey], &cfg)
		if cfg.Enabled || !cfg.Detectors["op_latency"] || !cfg.Detectors["metric_kafka"] {
			t.Fatalf("ana anahtar korunmalı, iki dedektör açılmalı: %s", f.kv[mlSettingsKey])
		}
		if len(f.kv[mlForceOnMigrateKey]) == 0 {
			t.Fatal("işaret yazılmadı")
		}
		// operatör sonradan kapatır → işaret varken dokunulmaz
		f.kv[mlSettingsKey] = stored
		f.putKeys = nil
		if out, err := MigrateMLForceOn(ctx, f, now); err != nil || out != "" || len(f.putKeys) != 0 {
			t.Fatalf("işaret varken koşmamalı: out=%q puts=%v err=%v", out, f.putKeys, err)
		}
	})
	t.Run("blob yok → yazım yok, işaret var", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{}}
		out, err := MigrateMLForceOn(ctx, f, now)
		if err != nil || out != MLForceOnNoRow || f.kv[mlSettingsKey] != nil || len(f.audits) != 0 {
			t.Fatalf("outcome=%s err=%v audits=%d", out, err, len(f.audits))
		}
		if d := DefaultMLConfig(); !d.Detectors["op_latency"] || !d.Detectors["metric_kafka"] {
			t.Fatal("varsayılan iki dedektörü açık vermeli")
		}
	})
	t.Run("işaret okunamadı → koşmaz", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{mlSettingsKey: stored}, getErr: map[string]error{mlForceOnMigrateKey: errors.New("ch down")}}
		if _, err := MigrateMLForceOn(ctx, f, now); err == nil || len(f.putKeys) != 0 {
			t.Fatalf("hata beklenirdi, yazım yok: %v %v", err, f.putKeys)
		}
	})
	t.Run("blob yazımı düşer → işaret yok", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{mlSettingsKey: stored}, putErr: map[string]error{mlSettingsKey: errors.New("ch down")}}
		if _, err := MigrateMLForceOn(ctx, f, now); err == nil || f.kv[mlForceOnMigrateKey] != nil {
			t.Fatal("işaret yazılmamalı")
		}
	})
}
