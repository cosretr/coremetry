package chstore

// anomaly_sensitivity_oplatency_test.go — `opLatency` anahtarı ve
// `opLatencyDwellBuckets` sürdürme vidası.
//
// v0.10.1056 (operatör, prod): "Trace op latency false pozitif geliyor, gerek
// yok gelmelerine bence." → nil = kapalı.
// v0.10.1085 (operatör, sürdürme kuralı önerisine): "Önerini yapalım." → nil =
// AÇIK, YALNIZ iki ardışık kova sürdürme kuralıyla (opLatencyDwellBuckets,
// vars. 2, aralık 1–6). Açıkça false kapalı kalır; Normalize iki alanı da
// düşürmez (PUT'ta kaybolsaydı her kayıt dedektörü sessizce değiştirirdi).
// 1056 döneminin somutlaştırılmış false'u tek seferlik göçle nil'e çekilir —
// yalnız blob bu sürümde kaydedilmemişse (dwell alanı yok).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOpLatencyOn(t *testing.T) {
	tr, fa := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil (alan yok) → AÇIK (v0.10.1085)", nil, true},
		{"açıkça false → kapalı", &fa, false},
		{"açıkça true → açık", &tr, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (AnomalySensitivityConfig{OpLatency: tc.in}).OpLatencyOn(); got != tc.want {
				t.Fatalf("OpLatencyOn = %v, beklenen %v", got, tc.want)
			}
		})
	}
	d := DefaultAnomalySensitivity()
	if !d.OpLatencyOn() || d.OpLatencyDwell() != 2 {
		t.Fatalf("varsayılan AÇIK + 2 kova olmalı (operatör onayı): on=%v dwell=%d", d.OpLatencyOn(), d.OpLatencyDwell())
	}
	var zero AnomalySensitivityConfig
	if !zero.OpLatencyOn() || zero.OpLatencyDwell() != 2 {
		t.Fatal("sıfır değer AÇIK + 2 kova olmalı (eski blob)")
	}
}

func TestOpLatencyDwellClamp(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{
		{0, 2},  // eski blob: alan yok
		{-1, 2}, // anlamsız
		{1, 1},  // tek-kova (v0.10.1085 öncesi davranış)
		{2, 2},
		{6, 6},
		{7, 2}, // tavan üstü → varsayılan (sıfırlanmaz)
	} {
		c := AnomalySensitivityConfig{OpLatencyDwellBuckets: tc.in}
		if got := c.OpLatencyDwell(); got != tc.want {
			t.Errorf("OpLatencyDwell(%d) = %d, beklenen %d", tc.in, got, tc.want)
		}
		if got := NormalizeAnomalySensitivity(c).OpLatencyDwellBuckets; got != tc.want {
			t.Errorf("Normalize(%d) = %d, beklenen %d", tc.in, got, tc.want)
		}
	}
}

func TestOpLatencyNormalizeKeepsPointer(t *testing.T) {
	tr, fa := true, false
	for _, tc := range []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil → somut true", nil, true},
		{"false → false", &fa, false},
		{"true → true", &tr, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := NormalizeAnomalySensitivity(AnomalySensitivityConfig{OpLatency: tc.in})
			if n.OpLatency == nil {
				t.Fatal("Normalize alanı düşürdü (nil) — PUT'ta kaybolur")
			}
			if *n.OpLatency != tc.want || n.OpLatencyOn() != tc.want {
				t.Fatalf("Normalize = %v, beklenen %v", *n.OpLatency, tc.want)
			}
		})
	}
}

func TestOpLatencyJSONRoundTrip(t *testing.T) {
	// PUT = Normalize + Marshal (SaveAnomalySensitivity); GET = Unmarshal +
	// Normalize (ReadAnomalySensitivity).
	putGet := func(t *testing.T, in AnomalySensitivityConfig) (AnomalySensitivityConfig, string) {
		t.Helper()
		raw, err := json.Marshal(NormalizeAnomalySensitivity(in))
		if err != nil {
			t.Fatal(err)
		}
		var back AnomalySensitivityConfig
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		return NormalizeAnomalySensitivity(back), string(raw)
	}

	t.Run("eski blob (alan yok) → AÇIK, 2 kova", func(t *testing.T) {
		var old AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(`{"dwellBuckets":3,"criticalZ":6,"serviceSilent":true}`), &old); err != nil {
			t.Fatal(err)
		}
		if old.OpLatency != nil {
			t.Fatal("alan olmayan blob nil olmayan işaretçi üretti")
		}
		n := NormalizeAnomalySensitivity(old)
		if !n.OpLatencyOn() || n.OpLatencyDwellBuckets != 2 {
			t.Fatalf("eski blob: on=%v dwell=%d — açık + 2 bekleniyordu", n.OpLatencyOn(), n.OpLatencyDwellBuckets)
		}
	})

	t.Run("açıkça false tur atar (operatörün kapatması kalır)", func(t *testing.T) {
		fa := false
		got, raw := putGet(t, AnomalySensitivityConfig{OpLatency: &fa})
		if !strings.Contains(raw, `"opLatency":false`) {
			t.Fatalf("kaydedilen blob opLatency:false taşımıyor: %s", raw)
		}
		if got.OpLatencyOn() {
			t.Fatal("false kayıt-okuma turunda açıldı")
		}
	})

	t.Run("yeni kayıt iki alanı da açıkça yazar", func(t *testing.T) {
		got, raw := putGet(t, AnomalySensitivityConfig{OpLatencyDwellBuckets: 3})
		for _, frag := range []string{`"opLatency":true`, `"opLatencyDwellBuckets":3`} {
			if !strings.Contains(raw, frag) {
				t.Fatalf("kaydedilen blob %s taşımıyor: %s", frag, raw)
			}
		}
		if !got.OpLatencyOn() || got.OpLatencyDwell() != 3 {
			t.Fatalf("tur: on=%v dwell=%d", got.OpLatencyOn(), got.OpLatencyDwell())
		}
	})
}

// TestOpLatencyForDetectorsUnconfirmedOff — varsayılan AÇIK'a dönünce 1056'nın
// "henüz okunamadı → kapalı" güvencesi ForDetectors'ta sürer: doğrulanmamış
// ayarda dedektör KAPALI; doğrulanınca yayınlanan değer.
func TestOpLatencyForDetectorsUnconfirmedOff(t *testing.T) {
	s := &Store{}
	if s.AnomalySensitivityForDetectors().OpLatencyOn() {
		t.Fatal("hiç yayınlanmamış ayarda dedektör açık okundu")
	}
	if !s.AnomalySensitivity().OpLatencyOn() {
		t.Fatal("yayınlanan VARSAYILAN açık olmalı (yalnız dedektör okuması kapalı)")
	}
	_ = s.loadAnomalySensitivityWith(func() (AnomalySensitivityConfig, error) {
		return AnomalySensitivityConfig{}, errors.New("ch down")
	})
	if s.AnomalySensitivityForDetectors().OpLatencyOn() {
		t.Fatal("okuma hatasında (doğrulanmamış) dedektör açık okundu")
	}
	s.SetAnomalySensitivity(AnomalySensitivityConfig{}) // alan yok → açık
	if !s.AnomalySensitivityForDetectors().OpLatencyOn() {
		t.Fatal("doğrulanmış eski blob açık okunmalı")
	}
	fa := false
	s.SetAnomalySensitivity(AnomalySensitivityConfig{OpLatency: &fa})
	if s.AnomalySensitivityForDetectors().OpLatencyOn() {
		t.Fatal("doğrulanmış false kapalı kalmalı")
	}
}

func TestPlanOpLatencyMigration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		outcome string
		changed bool
	}{
		{"satır yok", ``, OpLatencyMigrateNoRow, false},
		{"alan yok (1056 öncesi kayıt) → zaten açık", `{"criticalZ":6}`, OpLatencyMigrateDefault, false},
		{"null", `{"opLatency":null}`, OpLatencyMigrateDefault, false},
		{"açıkça true", `{"opLatency":true}`, OpLatencyMigrateOn, false},
		{"1056 dönemi false, dwell alanı yok → nil", `{"criticalZ":6,"opLatency":false,"logTemplateNew":true}`, OpLatencyMigrateReenabled, true},
		{"bu sürümde kaydedilmiş false (dwell alanı var) → dokunma", `{"opLatency":false,"opLatencyDwellBuckets":2}`, OpLatencyMigrateCustomised, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newRaw, outcome, err := planOpLatencyMigration([]byte(tc.raw))
			if err != nil || outcome != tc.outcome || (newRaw != nil) != tc.changed {
				t.Fatalf("outcome=%q err=%v changed=%v (beklenen %q / %v)", outcome, err, newRaw != nil, tc.outcome, tc.changed)
			}
			if !tc.changed {
				return
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(newRaw, &fields); err != nil {
				t.Fatal(err)
			}
			if _, ok := fields[opLatencyField]; ok {
				t.Fatalf("opLatency alanı silinmeliydi: %s", newRaw)
			}
			// Diğer alanlar AYNEN korunur.
			if string(fields["criticalZ"]) != "6" || string(fields["logTemplateNew"]) != "true" {
				t.Fatalf("diğer alanlar değişti: %s", newRaw)
			}
			var c AnomalySensitivityConfig
			if err := json.Unmarshal(newRaw, &c); err != nil || !NormalizeAnomalySensitivity(c).OpLatencyOn() {
				t.Fatalf("göç sonrası blob açık okunmalı (err=%v)", err)
			}
		})
	}
	if _, _, err := planOpLatencyMigration([]byte(`{"opLatency":"evet"}`)); err == nil {
		t.Fatal("çözülemeyen değer hata vermeli")
	}
}

func TestMigrateOpLatencyDefault(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	era1056 := []byte(`{"criticalZ":6,"dwellBuckets":3,"opLatency":false}`)

	t.Run("1056 dönemi false → nil (açık), audit + işaret; ikinci koşu no-op", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: era1056}}
		out, err := MigrateOpLatencyDefault(ctx, f, now)
		if err != nil || out != OpLatencyMigrateReenabled {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		var c AnomalySensitivityConfig
		if err := json.Unmarshal(f.kv[anomalySensitivityKey], &c); err != nil {
			t.Fatal(err)
		}
		if c.OpLatency != nil || !NormalizeAnomalySensitivity(c).OpLatencyOn() || c.CriticalZ != 6 {
			t.Fatalf("göç sonrası: %s", f.kv[anomalySensitivityKey])
		}
		if len(f.audits) != 1 || f.audits[0].ActorID != "system" || f.audits[0].TargetID != anomalySensitivityKey {
			t.Errorf("tek system audit satırı: %+v", f.audits)
		}
		var m opLatencyMigrateMarker
		if err := json.Unmarshal(f.kv[opLatencyMigrateKey], &m); err != nil || m.Outcome != OpLatencyMigrateReenabled || m.Version == "" {
			t.Errorf("işaret: %s (%v)", f.kv[opLatencyMigrateKey], err)
		}
		// Operatör göçten sonra kapatır (eski bir UI kaydı dwell alanı yazmasa
		// bile) → işaret varken göç bir daha koşmaz, false kalır.
		f.kv[anomalySensitivityKey] = era1056
		writes := len(f.putKeys)
		if out, err := MigrateOpLatencyDefault(ctx, f, now); err != nil || out != "" || len(f.putKeys) != writes {
			t.Errorf("işaret varken no-op: outcome=%q err=%v yazım=%d", out, err, len(f.putKeys)-writes)
		}
	})

	t.Run("bu sürümde kaydedilmiş false dokunulmaz, işaret yazılır, audit yok", func(t *testing.T) {
		saved := []byte(`{"opLatency":false,"opLatencyDwellBuckets":2}`)
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: saved}}
		out, err := MigrateOpLatencyDefault(ctx, f, now)
		if err != nil || out != OpLatencyMigrateCustomised {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if string(f.kv[anomalySensitivityKey]) != string(saved) || len(f.audits) != 0 || len(f.kv[opLatencyMigrateKey]) == 0 {
			t.Errorf("blob değişmemeli, audit olmamalı, işaret yazılmalı: %s %+v", f.kv[anomalySensitivityKey], f.audits)
		}
	})

	t.Run("satır yok → yalnız işaret", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{}}
		if out, err := MigrateOpLatencyDefault(ctx, f, now); err != nil || out != OpLatencyMigrateNoRow {
			t.Fatalf("outcome=%s err=%v", out, err)
		}
		if len(f.putKeys) != 1 || f.putKeys[0] != opLatencyMigrateKey {
			t.Errorf("yalnız işaret yazılmalı: %v", f.putKeys)
		}
	})

	t.Run("işaret okunamadı → hiçbir şey yazılmaz", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: era1056}, getErr: map[string]error{opLatencyMigrateKey: errors.New("ch down")}}
		if _, err := MigrateOpLatencyDefault(ctx, f, now); err == nil || len(f.putKeys) != 0 {
			t.Errorf("hata + yazım yok: err=%v yazım=%v", err, f.putKeys)
		}
	})

	t.Run("blob yazılamadı → işaret YOK (sonraki tur yeniden dener)", func(t *testing.T) {
		f := &migFakeStore{kv: map[string][]byte{anomalySensitivityKey: era1056}, putErr: map[string]error{anomalySensitivityKey: errors.New("ch down")}}
		if _, err := MigrateOpLatencyDefault(ctx, f, now); err == nil || len(f.kv[opLatencyMigrateKey]) != 0 {
			t.Errorf("işaret yazılmamalı: err=%v", err)
		}
	})
}
