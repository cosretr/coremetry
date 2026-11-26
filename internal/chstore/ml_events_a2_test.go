package chstore

// ml_events_a2_test.go — v0.10.2343 (anomali kartı A2, kapalı gelir): liste
// SQL'i. HideHeld false (A2 kapalı) iken WHERE gövdesi v0.10.2340 ile bayt
// bayt aynı (beklenen metinler o sürümden alındı); true iken yalnız servis /
// platform görünümüne `publish_state != 'held'` eklenir, ID okuması ve held
// görünümü değişmez (tutulan olay erişilebilir kalır).

import (
	"testing"
	"time"
)

func TestMLEventsWhereA2(t *testing.T) {
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	const base = "last_seen >= ? AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion')"
	pre := map[bool]map[string]string{ // v0.10.2340 çıktısı
		false: {
			"":             base,
			MLViewService:  base + " AND NOT detector IN ('svc_volume_spike', 'svc_telemetry_volume')",
			MLViewPlatform: base + " AND detector IN ('svc_volume_spike', 'svc_telemetry_volume')",
			MLViewHeld:     base + " AND 0",
		},
		true: {
			"":             base,
			MLViewService:  base + " AND NOT (class = 'telemetry_volume' OR detector IN ('svc_volume_spike', 'svc_telemetry_volume'))",
			MLViewPlatform: base + " AND (class = 'telemetry_volume' OR detector IN ('svc_volume_spike', 'svc_telemetry_volume'))",
			MLViewHeld:     base + ` AND record != '' AND publish_state != 'invalid' AND position(record, '"passed":false') > 0`,
		},
	}
	for rc, views := range pre {
		for v, want := range views {
			if got, _ := mlEventsWhere(MLEventsFilter{Since: since, View: v, recCols: rc}); got != want {
				t.Errorf("A2 kapalı recCols=%v view=%q:\n got %s\nwant %s", rc, v, got, want)
			}
			on := want
			if rc && (v == MLViewService || v == MLViewPlatform) {
				on += " AND publish_state != 'held'"
			}
			if got, _ := mlEventsWhere(MLEventsFilter{Since: since, View: v, recCols: rc, HideHeld: true}); got != on {
				t.Errorf("A2 açık recCols=%v view=%q:\n got %s\nwant %s", rc, v, got, on)
			}
		}
	}
	// tutulan olay id ile okunur (kart, kanıt, MCP): HideHeld etkisiz
	idWant := "last_seen >= ? AND id = ? AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion')"
	for _, hh := range []bool{false, true} {
		if got, _ := mlEventsWhere(MLEventsFilter{Since: since, ID: "x", recCols: true, HideHeld: hh}); got != idWant {
			t.Errorf("id okuması HideHeld=%v: %s", hh, got)
		}
	}
}
