package chstore

// v0.10.2087 — trace_op_latency dedektörü silindi (operatör kararı
// 2026-11-13: sunucu uçları ML op_latency ile kapsanır; CONSUMER / INTERNAL
// p99 sıçraması bilinçli olarak kaybedildi). Kayıtlı anomaly_sensitivity
// blobunda kalan `opLatency` / `opLatencyDwellBuckets` / `opLatencyMinAffected`
// alanları hatasız okunur, yok sayılır ve Normalize'dan sonra yazılmaz.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnomalySensitivityRetiredOpLatencyFields(t *testing.T) {
	for _, raw := range []string{
		`{"opLatency":true,"opLatencyDwellBuckets":2,"opLatencyMinAffected":5,"criticalZ":7}`,
		`{"opLatency":false,"criticalZ":7}`,
		`{"opLatency":null,"opLatencyDwellBuckets":0,"criticalZ":7}`,
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
		if strings.Contains(string(out), "opLatency") {
			t.Fatalf("%s: yeniden yazımda opLatency* olmamalı: %s", raw, out)
		}
	}
}
