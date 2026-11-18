package api

// anomalies_unified_a2_test.go — v0.10.2343 (anomali kartı A2, kapalı
// gelir): A2 kapalıyken listenin ml_events süzgeci ve yanıt gövdesi
// v0.10.2340 ile aynı; açıkken (dikiş) servis / platform görünümü held
// satırı düşürür, olay ve held görünümü düşürmez.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func withGatesEnforced(t *testing.T, on bool) {
	t.Helper()
	prev := publishGatesEnforced
	publishGatesEnforced = func() bool { return on }
	t.Cleanup(func() { publishGatesEnforced = prev })
}

func TestUnifiedA2OffSameFilterAndBody(t *testing.T) {
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for _, v := range []string{"", chstore.MLViewService, chstore.MLViewPlatform, chstore.MLViewHeld} {
		want := chstore.MLEventsFilter{Since: since, Limit: chstore.MLUnifiedCap, View: v} // v0.10.2340 çağrısı
		if got := unifiedMLFilter(v, since); !reflect.DeepEqual(got, want) {
			t.Fatalf("A2 kapalı view=%q süzgeç farklı: %+v", v, got)
		}
	}
	// v0.10.2340 yanıt gövdesi (boş held sayfası) — yeni alan yazılmaz
	b, _ := json.Marshal(unifiedAnomaliesResponse{Items: []unifiedAnomalyRow{}, Limit: 50, GatesEnforced: publishGatesEnforced()})
	if string(b) != `{"items":[],"total":0,"openTotal":0,"doneTotal":0,"offset":0,"limit":50,"truncated":false}` {
		t.Fatalf("A2 kapalı yanıt gövdesi değişti: %s", b)
	}
}

func TestUnifiedA2OnHidesHeldOnlyInLists(t *testing.T) {
	withGatesEnforced(t, true)
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for _, v := range []string{"", chstore.MLViewService, chstore.MLViewPlatform, chstore.MLViewHeld} {
		if !unifiedMLFilter(v, since).HideHeld {
			t.Fatalf("A2 açık view=%q: HideHeld bekleniyordu (SQL yalnız servis/platformda uygular)", v)
		}
	}
	b, _ := json.Marshal(unifiedAnomaliesResponse{Items: []unifiedAnomalyRow{}, GatesEnforced: publishGatesEnforced()})
	if !json.Valid(b) || !strings.Contains(string(b), `"gatesEnforced":true`) {
		t.Fatalf("held görünümü kipi taşımalı: %s", b)
	}
}
