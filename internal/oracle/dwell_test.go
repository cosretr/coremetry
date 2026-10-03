package oracle

// dwell_test.go — v0.10.1083. Operatör: "Oracle hataları da problemse hâlâ
// düşmüyor" → onay "Yap". Hata serisinin Problem açması için gereken ardışık
// 1 dk'lık kova kaynak ayarında (`dwellMinutes`, varsayılan 10, 3–30); Oracle
// taraması bunu dış tarayıcının Dwell üst-yazımı olarak geçer.

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNormalize_DwellMinutes(t *testing.T) {
	if s := mustNormalize(t, one(base()), Settings{}).Sources[0]; s.DwellMinutes != DefaultDwellMinutes || DefaultDwellMinutes != 10 {
		t.Fatalf("boş dwellMinutes → varsayılan 10: %d", s.DwellMinutes)
	}
	for _, c := range []struct {
		v   int
		bad bool
	}{{MinDwellMinutes - 1, true}, {MinDwellMinutes, false}, {15, false}, {MaxDwellMinutes, false}, {MaxDwellMinutes + 1, true}, {-1, true}} {
		src := base()
		src.DwellMinutes = c.v
		out, err := Normalize(one(src), Settings{}, NewSourceID)
		if c.bad != (err != nil) {
			t.Errorf("dwellMinutes %d: err=%v, istenen hata=%v", c.v, err, c.bad)
		}
		if err == nil && out.Sources[0].DwellMinutes != c.v {
			t.Errorf("dwellMinutes %d korunmalı: %d", c.v, out.Sources[0].DwellMinutes)
		}
	}
	// JSON adı sözleşmesi (FE OracleSource.dwellMinutes).
	b, _ := json.Marshal(SourceConfig{DwellMinutes: 12})
	if !strings.Contains(string(b), `"dwellMinutes":12`) {
		t.Errorf("JSON alanı dwellMinutes olmalı: %s", b)
	}
}

func TestDwellOf_DefaultsForUnnormalizedBlob(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 10}, {2, 10}, {31, 10}, {3, 3}, {10, 10}, {30, 30}} {
		if got := DwellOf(SourceConfig{DwellMinutes: c.in}); got != c.want {
			t.Errorf("DwellOf(%d) = %d, istenen %d", c.in, got, c.want)
		}
	}
}

// main.go kablosu: Oracle taraması kaynak ayarının sürdürme değerini geçer.
func TestOracleScanPassesDwellFromSettings(t *testing.T) {
	b, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "rep, err := scanner.Scan(hctx, anomaly.ExternalTarget{")
	if i < 0 {
		t.Fatal("Oracle Scan çağrısı bulunamadı")
	}
	j := strings.Index(src[i:], "})")
	if j < 0 || !strings.Contains(src[i:i+j], "Thresholds: anomaly.ExternalThresholds{Dwell: oracle.DwellOf(src)}") {
		t.Error("Oracle taraması Thresholds{Dwell: oracle.DwellOf(src)} geçmeli")
	}
}
