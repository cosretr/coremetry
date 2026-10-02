package chstore

// selfhealth_disk_eta_switch_test.go — v0.10.1031 (operatör 2026-10-02:
// "Disk dolacak niye geliyor, gerek yok."). self-disk-eta kuralı
// SelfHealthConfig.DiskEta anahtarının arkasına alındı ve varsayılanı
// KAPALI: nil = kapalı (Enabled'ın tersi; emsal ServiceSilent, v0.10.543).
//
// Buradaki tuzaklar:
//   - Sahadaki her kayıtlı blob alanı TAŞIMAZ → kapalı okunmalı (operatör
//     kararı). Açık okunsaydı karar yalnız hiç ayar kaydetmemiş kurulumlara
//     uygulanırdı.
//   - patchSelfHealth sayısal alanları varsayılana çeker; pointer'a
//     DOKUNMAMALI. Dokunsaydı (ör. nil → &true) karar okuma anında
//     sessizce geri alınırdı.
//   - PUT → GET round-trip'i alanı kaybetmemeli: admin "açtım" der, GET
//     "yok" derse anahtar dönmeyen vida olur.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSelfHealthDiskEtaOn(t *testing.T) {
	tests := []struct {
		name string
		in   *bool
		want bool
	}{
		{"nil (yazılmamış) → KAPALI (v0.10.1031 varsayılanı)", nil, false},
		{"açıkça false → KAPALI", selfHealthBoolPtr(false), false},
		{"açıkça true → AÇIK", selfHealthBoolPtr(true), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := SelfHealthConfig{DiskEta: tt.in}
			if got := c.DiskEtaOn(); got != tt.want {
				t.Fatalf("DiskEtaOn() = %v, beklenen %v", got, tt.want)
			}
		})
	}

	// Varsayılan blob kuralı AÇMAZ — kodda yaşayan varsayılan operatör
	// kararıdır; aile (Enabled) ise açık kalır, diğer dört kural etkilenmez.
	d := DefaultSelfHealth()
	if d.DiskEta != nil || d.DiskEtaOn() {
		t.Fatalf("DefaultSelfHealth disk ETA kuralını açıyor: %+v", d.DiskEta)
	}
	if !d.SelfHealthOn() {
		t.Fatal("DefaultSelfHealth aileyi kapattı — yalnız disk ETA kapanmalıydı")
	}
}

func TestSelfHealthDiskEtaJSON(t *testing.T) {
	d := DefaultSelfHealth()

	tests := []struct {
		name string
		raw  string
		want bool
	}{
		// v0.9.1294'ün yazdığı tam biçim — sahadaki kayıtlı bloblar böyle.
		{"eski tam blob (alan yok) → KAPALI",
			`{"enabled":true,"ingestStallMin":10,"spoolMaxFiles":100000,"spoolMaxBytes":10737418240,` +
				`"diskEtaDays":7,"channelConsecFails":3,"volumeSpikeFactor":4,"volumeSpikeMinSpans":100000}`, false},
		{"tamamen boş blob → KAPALI", `{}`, false},
		{"yalnız eşik yazılmış → KAPALI (eşik kuralı açmaz)", `{"diskEtaDays":3}`, false},
		{"açıkça false → KAPALI", `{"diskEta":false}`, false},
		{"açıkça true → AÇIK", `{"diskEta":true}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c SelfHealthConfig
			if err := json.Unmarshal([]byte(tt.raw), &c); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := patchSelfHealth(c, d)
			if got.DiskEtaOn() != tt.want {
				t.Fatalf("DiskEtaOn() = %v, beklenen %v (blob %s)", got.DiskEtaOn(), tt.want, tt.raw)
			}
			// Anahtar aileyi ETKİLEMEZ: hiçbir vakada self-health kapanmaz.
			if !got.SelfHealthOn() {
				t.Fatalf("diskEta okuması aileyi kapattı (blob %s)", tt.raw)
			}
		})
	}

	// PUT → GET round-trip: SaveSelfHealth json.Marshal'lar, GetSelfHealth
	// Unmarshal + patch eder, handler cevabı yine Marshal'lar. Üç adımda da
	// alan yaşamalı.
	for _, on := range []bool{true, false} {
		c := d
		c.DiskEta = selfHealthBoolPtr(on)
		saved, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var read SelfHealthConfig
		if err := json.Unmarshal(saved, &read); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		got := patchSelfHealth(read, d)
		if got.DiskEta == nil || *got.DiskEta != on {
			t.Fatalf("round-trip diskEta=%v kayboldu: kaydedilen %s", on, saved)
		}
		echoed, err := json.Marshal(got)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		// omitempty yalnız nil'i düşürür: açıkça yazılmış false da GET'te görünür.
		want := `"diskEta":true`
		if !on {
			want = `"diskEta":false`
		}
		if !strings.Contains(string(echoed), want) {
			t.Fatalf("GET cevabında %s yok: %s", want, echoed)
		}
	}

	// Hiç yazılmamış alan GET'te görünmez (omitempty) ve kapalı okunur.
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "diskEta\"") {
		t.Fatalf("varsayılan blob diskEta alanı yazdı: %s", raw)
	}
}

func TestPatchSelfHealthLeavesDiskEtaAlone(t *testing.T) {
	d := DefaultSelfHealth()
	d.DiskEta = selfHealthBoolPtr(true) // varsayılanda olsa bile patch KOPYALAMAMALI

	for _, in := range []*bool{nil, selfHealthBoolPtr(false), selfHealthBoolPtr(true)} {
		got := patchSelfHealth(SelfHealthConfig{DiskEta: in}, d)
		if got.DiskEta != in {
			t.Fatalf("patchSelfHealth DiskEta'ya dokundu: girdi %v, çıktı %v", in, got.DiskEta)
		}
	}
}
