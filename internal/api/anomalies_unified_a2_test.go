package api

// anomalies_unified_a2_test.go — v0.10.2343 / v0.10.2473: gürültü kapısı
// kipine (off | shadow | on) göre listenin ml_events süzgeci, önbellek
// anahtarı, Elenenler'in olayları, satır başına neden ve sayaç. shadow / off:
// süzgeç ve yanıt gövdesi v0.10.2340 ile aynı; on: servis / platform görünümü
// held satırı düşürür, olay ve held görünümü düşürmez. Servis adları SENTETİK.

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func TestUnifiedGatesFilterByMode(t *testing.T) {
	since := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for _, v := range []string{"", chstore.MLViewService, chstore.MLViewPlatform, chstore.MLViewHeld} {
		want := chstore.MLEventsFilter{Since: since, Limit: chstore.MLUnifiedCap, View: v} // v0.10.2340 çağrısı
		for _, mode := range []string{chstore.MLGatesOff, chstore.MLGatesShadow} {
			if got := unifiedMLFilter(v, since, mode); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s view=%q süzgeç farklı: %+v", mode, v, got)
			}
		}
		if !unifiedMLFilter(v, since, chstore.MLGatesOn).HideHeld {
			t.Fatalf("on view=%q: HideHeld bekleniyordu (SQL yalnız servis/platformda uygular)", v)
		}
	}
	// servis listesi yanıtı kip alanı taşımaz (gövde v0.10.2340 ile aynı)
	b, _ := json.Marshal(unifiedAnomaliesResponse{Items: []unifiedAnomalyRow{}, Limit: 50})
	if string(b) != `{"items":[],"total":0,"openTotal":0,"doneTotal":0,"offset":0,"limit":50,"truncated":false}` {
		t.Fatalf("yanıt gövdesi değişti: %s", b)
	}
}

// Kip önbellek anahtarında: kip değişince eski kipin sayfası sunulmaz.
func TestUnifiedKeyCarriesGatesMode(t *testing.T) {
	seen := map[string]string{}
	for _, mode := range []string{chstore.MLGatesOff, chstore.MLGatesShadow, chstore.MLGatesOn} {
		p := parseUnifiedParams(func(string) string { return "" })
		p.Gates = mode
		k := unifiedKey(p, "UTC")
		if prev, dup := seen[k]; dup {
			t.Fatalf("%s ve %s aynı anahtarı paylaşıyor: %s", prev, mode, k)
		}
		seen[k] = mode
	}
}

// heldEv — kaydında kapı satırları olan sentetik olay.
func heldEv(id, det, state string, gates ...ml.RecordGate) chstore.MLEvent {
	raw, _ := json.Marshal(ml.EventRecord{Gates: gates})
	return chstore.MLEvent{ID: id, Detector: det, Service: "checkout", PublishState: state, Record: string(raw)}
}

func TestHeldViewEventsReasonsAndCounter(t *testing.T) {
	vol := ml.RecordGate{Name: ml.GateMinVolume, Value: 40, Threshold: 100}
	diff := ml.RecordGate{Name: ml.GateMinAbsDiff, Value: 17, Threshold: 25}
	evid := ml.RecordGate{Name: ml.GateEvidence, Value: 1, Threshold: 2}
	pass := ml.RecordGate{Name: ml.GateVisibleDeviation, Passed: true, Value: 5, Threshold: 3}
	items := []chstore.MLEvent{
		heldEv("held-vol", "op_latency", ml.PublishHeld, pass, vol),
		heldEv("held-both", "op_latency", ml.PublishHeld, vol, diff),
		heldEv("would-hold", "op_latency", ml.PublishPublished, vol),      // kip açılmadan önce yazılmış
		heldEv("k10-only", "op_latency", ml.PublishPublished, pass, evid), // K10 satırı elemez
		heldEv("impact", "svc_response_time", ml.PublishPublished, vol),   // etki kuralı muaf
		{ID: "bare", Detector: "op_latency", Service: "checkout"},
	}
	ids := func(evs []chstore.MLEvent) []string {
		out := []string{}
		for _, e := range evs {
			out = append(out, e.ID)
		}
		return out
	}
	for _, c := range []struct {
		mode string
		want []string
	}{
		{chstore.MLGatesOn, []string{"held-vol", "held-both"}},                   // yalnız gerçekten tutulanlar
		{chstore.MLGatesShadow, []string{"held-vol", "held-both", "would-hold"}}, // on olsaydı tutulacaklar
		{chstore.MLGatesOff, []string{}},
	} {
		if got := ids(heldOnly(items, c.mode)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Elenenler %v, istenen %v", c.mode, got, c.want)
		}
	}
	// satır başına neden: yalnız kalan gürültü kapıları, değer + eşik
	if got := heldGatesOf(items[1]); !reflect.DeepEqual(got, []heldGate{{ml.GateMinVolume, 40, 100}, {ml.GateMinAbsDiff, 17, 25}}) {
		t.Fatalf("neden: %+v", got)
	}
	if heldGatesOf(items[3]) != nil || heldGatesOf(items[4]) != nil || heldGatesOf(items[5]) != nil {
		t.Fatal("K10 satırı, etki kuralı olayı ve kayıtsız olay neden taşımaz")
	}
	// sayaç: olay sayısı + kapı başına olay
	s := summarizeHeld(heldOnly(items, chstore.MLGatesOn))
	if s.Events != 2 || s.ByGate[ml.GateMinVolume] != 2 || s.ByGate[ml.GateMinAbsDiff] != 1 || len(s.ByGate) != 2 {
		t.Fatalf("sayaç: %+v", s)
	}
	b, _ := json.Marshal(unifiedAnomaliesResponse{Items: []unifiedAnomalyRow{}, GatesMode: chstore.MLGatesOn, Held: s})
	var back struct {
		GatesMode string `json:"gatesMode"`
		Held      struct {
			Events int            `json:"events"`
			ByGate map[string]int `json:"byGate"`
		} `json:"held"`
	}
	if json.Unmarshal(b, &back) != nil || back.GatesMode != "on" || back.Held.Events != 2 || back.Held.ByGate["min_volume"] != 2 {
		t.Fatalf("held görünümü kipi ve sayacı taşımalı: %s", b)
	}
}

// v0.10.2476 — invalid kayıt (doğrulayıcıdan kaldı) hiçbir listede yok →
// Elenenler'de her kipte, nedeni "invalid_record"; sayaçta ayrı satır.
// "Yük ve yapı" görünümü (view=load) çözülür ve anahtarda ayrı.
func TestHeldViewIncludesInvalidAndLoadView(t *testing.T) {
	inv := chstore.MLEvent{ID: "inv", Detector: "op_latency", Service: "checkout", PublishState: ml.PublishInvalid, Record: "{}"}
	for _, mode := range []string{chstore.MLGatesOn, chstore.MLGatesShadow, chstore.MLGatesOff} {
		if got := heldOnly([]chstore.MLEvent{inv}, mode); len(got) != 1 {
			t.Errorf("%s: invalid kayıt Elenenler'de olmalı", mode)
		}
	}
	if got := heldGatesOf(inv); !reflect.DeepEqual(got, []heldGate{{Name: heldGateInvalid}}) {
		t.Fatalf("invalid nedeni: %+v", got)
	}
	if s := summarizeHeld([]chstore.MLEvent{inv}); s.ByGate[heldGateInvalid] != 1 {
		t.Fatalf("sayaç invalid: %+v", s)
	}
	p := parseUnifiedParams(func(k string) string {
		if k == "view" {
			return chstore.MLViewLoad
		}
		return ""
	})
	if p.View != chstore.MLViewLoad {
		t.Fatalf("view=load çözülmeli: %+v", p)
	}
	if unifiedKey(p, "UTC") == unifiedKey(parseUnifiedParams(func(string) string { return "" }), "UTC") {
		t.Fatal("load görünümü servis listesiyle aynı anahtarı paylaşmamalı")
	}
}
