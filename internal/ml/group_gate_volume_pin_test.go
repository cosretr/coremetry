package ml

// group_gate_volume_pin_test.go — v0.10.2118: servis hacmi sıçraması
// (svc_volume_spike) ML grup kapısının HİÇBİR kolunda Problem açmaz.
//
// Operatör kararı (2026-11-13): "Coremetry · servis hacmi sıçradı" Problem
// değil ANOMALİ; açılış bildirimi GÖNDERİLMEZ. Olay ml_events'e evaluator
// yazar; grup kapısı onu üye sayarsa (a) başka bir sinyalle "≥ 2 farklı
// sinyal" koluna, (b) tek-sinyal koluna girer ve Problem + bildirim arka
// kapıdan geri gelir. Kontrol vakası (op_latency aynı yerde) testin
// ısırdığını kanıtlar: dışlama kaldırılırsa ilk iki vaka kızarır.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestSvcVolumeSpikeNeverOpensGroupProblem(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	start := now.Add(-36 * time.Hour).UnixNano()
	vol := chstore.MLEvent{ID: "vol", Detector: chstore.MLDetSvcVolumeSpike, Signal: chstore.MLSignalTrace,
		Stage: chstore.MLStageShadow, Service: "orders-api", StartedAt: start, LastSeen: now.UnixNano(),
		WindowSec: 3600, Baseline: 200_000, Observed: 1_200_000, Score: 6, Direction: "up"}
	// Zayıf log olayı: tek başına tek-sinyal kolunu geçmez (kısa süre).
	logEv := chstore.MLEvent{ID: "log", Detector: "log_volume", Signal: chstore.MLSignalLog,
		Stage: chstore.MLStageShadow, Service: "orders-api", StartedAt: now.Add(-2 * time.Minute).UnixNano(),
		LastSeen: now.UnixNano(), WindowSec: 300, Baseline: 100, Observed: 130, Direction: "up"}
	ctrl := vol
	ctrl.ID, ctrl.Detector = "lat", "op_latency"

	cases := []struct {
		name    string
		members []chstore.MLEvent
		want    bool
	}{
		{"hacim + log = iki sinyal sayılmaz", []chstore.MLEvent{vol, logEv}, false},
		{"yalnız hacim, 36 sa, 6 kat: tek-sinyal kolu yok", []chstore.MLEvent{vol}, false},
		{"kontrol: op_latency aynı yerde → iki sinyal açar", []chstore.MLEvent{ctrl, logEv}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := MLGroup{ID: "g", Service: "orders-api"}
			for _, m := range c.members {
				g.Members = append(g.Members, m.ID)
			}
			d := DecideGroupPromotion(g, c.members, GroupGateInput{}, now)
			if d.WouldOpen != c.want {
				t.Fatalf("WouldOpen = %v, istenen %v (%s)", d.WouldOpen, c.want, d.Reason)
			}
			if !c.want && d.SingleWouldOpen {
				t.Fatalf("tek-sinyal kolu açtı: %s", d.Reason)
			}
			for _, det := range d.Detectors {
				if det == chstore.MLDetSvcVolumeSpike {
					t.Fatalf("hacim dedektörü karar kümesine girdi: %v", d.Detectors)
				}
			}
		})
	}

	// Replay yolu (geçmiş ml_events satırları) aynı kapıdan geçer.
	for _, d := range ReplayGroupDecisions([]chstore.MLEvent{vol, logEv}, 30*time.Minute, GroupGateInput{}, now) {
		if d.WouldOpen {
			t.Fatalf("replay hacim olayıyla Problem açardı: %+v", d)
		}
	}
}
