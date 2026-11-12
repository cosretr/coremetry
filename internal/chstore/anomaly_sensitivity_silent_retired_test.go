package chstore

// v0.10.2068 — yerel service_silent dedektörü (anomaly.go checkSilence,
// v0.9.1051; v0.10.543'ten beri varsayılan kapalı, operatör: "Olmasınlar")
// kaldırıldı. Kayıtlı anomaly_sensitivity blobunda kalan `serviceSilent`
// alanı hatasız okunur, yok sayılır ve Normalize'dan sonra yazılmaz.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnomalySensitivityRetiredServiceSilentField(t *testing.T) {
	for _, raw := range []string{
		`{"serviceSilent":true,"criticalZ":7}`,
		`{"serviceSilent":false,"criticalZ":7}`,
	} {
		var c AnomalySensitivityConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatalf("%s: eski blob okunmalı: %v", raw, err)
		}
		n := NormalizeAnomalySensitivity(c)
		if n.CriticalZ != 7 {
			t.Fatalf("%s: diğer alanlar aynen: %+v", raw, n.CriticalZ)
		}
		out, _ := json.Marshal(n)
		if strings.Contains(string(out), "serviceSilent") {
			t.Fatalf("%s: yeniden yazımda serviceSilent olmamalı: %s", raw, out)
		}
	}
}
