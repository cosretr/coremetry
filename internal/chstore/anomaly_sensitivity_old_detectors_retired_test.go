package chstore

// v0.10.2270 — eski motorun log_pattern, trace_op ve behavior dedektörleri
// silindi (operatör kararı). Kayıtlı anomaly_sensitivity blobunda kalan
// `behavior` bölümü ile `traceOpDwellBuckets` / `logPatternDwellBuckets`
// alanları hatasız okunur, yok sayılır ve Normalize'dan sonra yazılmaz.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnomalySensitivityRetiredFields(t *testing.T) {
	for _, raw := range []string{
		`{"behavior":{"enabled":true,"seasonalZ":4,"dwellRegime":6},"traceOpDwellBuckets":3,"logPatternDwellBuckets":1,"criticalZ":7}`,
		`{"behavior":null,"traceOpDwellBuckets":0,"criticalZ":7}`,
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
		for _, k := range []string{`"behavior"`, "traceOpDwellBuckets", "logPatternDwellBuckets"} {
			if strings.Contains(string(out), k) {
				t.Fatalf("%s: yeniden yazımda %s olmamalı: %s", raw, k, out)
			}
		}
	}
}
