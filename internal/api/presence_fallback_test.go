package api

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/cache"
)

// presence_fallback_test.go — v0.10.1735 (kullanıcı detay sayfası S5,
// "kim çevrimiçi"). Belirti: Redis'siz kurulumda (Noop önbellek) varlık
// damgası hiç okunamıyordu — Users sayfası herkesi "—" gösterirdi. Yerel
// throttle haritası geri-düşme kaynağıdır; en yeni damga kazanır, TTL'den
// eski yerel damga alınmaz. Çevrimiçi eşiği (5 dk) presenceOnline'da.

func TestMergePresence(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano()
	min := time.Minute.Nanoseconds()
	cases := []struct {
		name         string
		redis, local map[string]int64
		want         map[string]int64
	}{
		{"redis yok, yerel var", map[string]int64{}, map[string]int64{"u1": now - min}, map[string]int64{"u1": now - min}},
		{"redis daha yeni kazanır", map[string]int64{"u1": now - min}, map[string]int64{"u1": now - 3*min}, map[string]int64{"u1": now - min}},
		{"yerel daha yeni kazanır", map[string]int64{"u1": now - 3*min}, map[string]int64{"u1": now - min}, map[string]int64{"u1": now - min}},
		{"TTL'den eski yerel alınmaz", map[string]int64{}, map[string]int64{"u1": now - presenceTTL.Nanoseconds()}, map[string]int64{}},
		{"sıfır damga alınmaz", map[string]int64{}, map[string]int64{"u1": 0}, map[string]int64{}},
	}
	for _, c := range cases {
		got := mergePresence(c.redis, c.local, now)
		if len(got) != len(c.want) {
			t.Errorf("%s: %v", c.name, got)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s=%d want %d", c.name, k, got[k], v)
			}
		}
	}
}

func TestPresenceOnlineThreshold(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC).UnixNano()
	cases := []struct {
		ago  time.Duration
		want bool
	}{
		{0, true}, {4*time.Minute + 59*time.Second, true}, {5 * time.Minute, false}, {10 * time.Minute, false},
	}
	for _, c := range cases {
		if got := presenceOnline(now-c.ago.Nanoseconds(), now); got != c.want {
			t.Errorf("%v önce: online=%v want %v", c.ago, got, c.want)
		}
	}
	if presenceOnline(0, now) {
		t.Error("damga yok → çevrimdışı")
	}
}

func TestPresenceNoopCacheFallsBackToLocal(t *testing.T) {
	c, _ := cache.NewNoop()
	p := newPresenceTracker(c)
	t0 := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return t0 }
	p.stamp("u1")
	p.now = func() time.Time { return t0.Add(2 * time.Minute) }
	seen := p.lastSeen(context.Background(), []string{"u1", "u2"})
	if seen["u1"] != t0.UnixNano() {
		t.Fatalf("Redis'siz yerel damga okunmalı: %v", seen)
	}
	if _, ok := seen["u2"]; ok {
		t.Fatal("hiç görülmeyen kullanıcı yok kalmalı")
	}
	if !presenceOnline(seen["u1"], t0.Add(2*time.Minute).UnixNano()) {
		t.Fatal("2 dk önce görülen çevrimiçi")
	}
}
