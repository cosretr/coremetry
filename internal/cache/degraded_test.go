package cache

import (
	"testing"
	"time"
)

// v0.10.982 — P2.2 ikinci inceleme: reprobe gerçek kilidi takınca degraded
// bayrağı hemen düşerse her pod hâlâ lider iken (kalp atışı ≤ 60 s sonra
// fark eder) dedektörün tik başına CH yeniden okuması kapanıyordu. Bayrak
// pay süresi boyunca doğru kalmalı, sonra düşmeli.
func TestDegradedFlagGrace(t *testing.T) {
	clock := time.Unix(1_800_000_000, 0)
	f := NewDegradedFlag(true)
	f.now = func() time.Time { return clock }
	if !f.Load() {
		t.Fatal("başlangıçta degraded")
	}
	f.ClearAfter(LeaderTTL(time.Minute))
	clock = clock.Add(59 * time.Second)
	if !f.Load() {
		t.Fatal("pay süresinde (holder kalp atışından önce) doğru kalmalı")
	}
	clock = clock.Add(LeaderTTL(time.Minute))
	if f.Load() {
		t.Fatal("pay bitince düşmeli")
	}
	if NewDegradedFlag(false).Load() {
		t.Fatal("sağlıklı açılış degraded değil")
	}
}
