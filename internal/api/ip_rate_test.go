package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func TestIPWindowLimiter(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 6, 12, 0, 10, 0, time.UTC)}
	l := newIPWindowLimiter(3, time.Minute, clk.now)
	for i := 0; i < 3; i++ {
		if !l.allow("10.0.0.1") {
			t.Fatalf("istek %d sınır içinde", i+1)
		}
	}
	if l.allow("10.0.0.1") {
		t.Fatal("4. istek reddedilmeli")
	}
	if !l.allow("10.0.0.2") {
		t.Fatal("başka anahtar etkilenmemeli")
	}
	clk.t = clk.t.Add(49 * time.Second) // aynı pencere (12:00:59)
	if l.allow("10.0.0.1") {
		t.Fatal("pencere bitmeden sıfırlanmamalı")
	}
	clk.t = clk.t.Add(2 * time.Second) // 12:01:01 → yeni pencere
	if !l.allow("10.0.0.1") {
		t.Fatal("yeni pencerede yeniden izin")
	}
}

// Pencere başına harita boşalır; tavan aşılınca yeni anahtar sayılmadan geçer.
func TestIPWindowLimiterBoundedMemory(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)}
	l := newIPWindowLimiter(1, time.Minute, clk.now)
	for i := 0; i < ipRateMaxKeys+100; i++ {
		if !l.allow(fmt.Sprintf("k%d", i)) {
			t.Fatalf("tavan aşımında yeni anahtar geçmeli (i=%d)", i)
		}
	}
	if len(l.counts) > ipRateMaxKeys {
		t.Errorf("harita tavanı aşmamalı: %d", len(l.counts))
	}
	clk.t = clk.t.Add(time.Minute)
	l.allow("x")
	if len(l.counts) != 1 {
		t.Errorf("yeni pencerede harita boşalır: %d", len(l.counts))
	}
}

func TestRemoteHostStripsPort(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.9:51234": "203.0.113.9", "[2001:db8::1]:443": "2001:db8::1", "weird": "weird"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = in
		if got := remoteHost(r); got != want {
			t.Errorf("remoteHost(%q) = %q, want %q", in, got, want)
		}
	}
}
