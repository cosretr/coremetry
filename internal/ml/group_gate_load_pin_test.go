package ml

// group_gate_load_pin_test.go — v0.10.2477: yük / yapı dedektörleri
// (chstore.MLLoadStructureDetectors) ML grup kapısının HİÇBİR kolunda
// sayılmaz.
//
// Operatör kararı (2026-11-26): bu olaylar ana anomali listesinden "Yük ve
// yapı" sekmesine taşındı (v0.10.2477) ama "≥ 2 farklı sinyal" kolunda
// sayılmaya devam ediyordu: listede görünmeyen bir üye, tek başına kapıyı
// geçemeyen zayıf bir olayla birlikte Problem açtırabiliyordu. Kontrol
// vakası (hata oranı + gecikme) testin ısırdığını kanıtlar.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestLoadStructureNeverCountsInGroupGate(t *testing.T) {
	in := GroupGateInput{IsBatch: func(string) bool { return false }}
	// Zayıf gecikme olayı: tek başına tek-sinyal kolunu geçmez (1,5 kat).
	weak := gateEv("a", detOpLatency, "orders-api", 5, 20, 100, 150)
	for _, det := range chstore.MLLoadStructureDetectors {
		load := gateEv("x", det, "orders-api", 0, 30, 100, 10)
		if !groupExcluded(load) {
			t.Errorf("%s: groupExcluded = false; yük / yapı kapıda sayılmamalı", det)
		}
		d := decideFor(t, []chstore.MLEvent{load, weak}, in)
		if d.WouldOpen {
			t.Errorf("%s + zayıf gecikme Problem açtı: %+v", det, d)
		}
	}
	// Kontrol: iki gerçek sinyal (log hata oranı + trace gecikme) açar.
	d := decideFor(t, []chstore.MLEvent{gateEv("l", detErrorRate, "orders-api", 0, 30, 10, 14), weak}, in)
	if !d.WouldOpen {
		t.Fatalf("kontrol: hata oranı + gecikme açmalıydı: %+v", d)
	}
}
