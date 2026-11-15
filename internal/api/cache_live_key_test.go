package api

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// v0.10.2228 (Redis R2, O1): canlı pencerede 30 sn arayla iki poll AYNI
// anahtara düşer (SWR devrede); geçmiş (mutlak) pencerenin anahtarı eskisiyle
// birebir aynı kalır.
func TestCacheBucketLiveWindowSameKeyAcrossPolls(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 7, 123, time.UTC)
	poll := func(at time.Time) string { return cacheBucketAt(at.Add(-time.Hour), at, at.Add(150*time.Millisecond)) }
	a, b := poll(now), poll(now.Add(30*time.Second))
	if a != b || a != "live:1h0m0s" {
		t.Fatalf("canlı pencere iki poll'da aynı anahtar olmalı: %q %q", a, b)
	}
	// Farklı süre farklı anahtar (tüm girdiler anahtarda).
	if poll24 := cacheBucketAt(now.Add(-24*time.Hour), now, now); poll24 == a {
		t.Fatal("1 sa ve 24 sa canlı pencereler ayrışmalı")
	}
	// Saat kayması (tarayıcı ≤ 60 sn ileri/geri) hâlâ canlı.
	if k := cacheBucketAt(now.Add(20*time.Second-time.Hour), now.Add(20*time.Second), now); k != "live:1h0m0s" {
		t.Fatalf("küçük ileri kayma canlı sayılmalı: %q", k)
	}
}

func TestCacheBucketAbsoluteUnchanged(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 11, 14, 8, 0, 13, 0, time.UTC)
	to := from.Add(time.Hour)
	legacy := fmt.Sprintf("%d_%d", from.Truncate(30*time.Second).UnixNano(), to.Truncate(30*time.Second).UnixNano())
	if got := cacheBucketAt(from, to, now); got != legacy {
		t.Fatalf("mutlak pencere eski biçimde kalmalı: %q != %q", got, legacy)
	}
	// Şimdiden 2 dk önce biten pencere mutlak.
	end := now.Add(-2 * time.Minute)
	if got := cacheBucketAt(end.Add(-time.Hour), end, now); got[:5] == "live:" {
		t.Fatalf("2 dk önce biten pencere canlı sayılmamalı: %q", got)
	}
}

func TestCacheRawQueryLiveWindow(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 7, 0, time.UTC)
	q := func(to time.Time) string {
		from := to.Add(-15 * time.Minute)
		return "service=api&from=" + strconv.FormatInt(from.UnixNano(), 10) + "&to=" + strconv.FormatInt(to.UnixNano(), 10) + "&refresh=1"
	}
	a := cacheRawQueryStringAt(q(now), now)
	b := cacheRawQueryStringAt(q(now.Add(30*time.Second)), now.Add(30*time.Second))
	if a != b || a != "from=15m0s&service=api&to=live" {
		t.Fatalf("canlı ham sorgu iki poll'da aynı olmalı: %q %q", a, b)
	}
	past := now.Add(-3 * time.Hour)
	if got, want := cacheRawQueryStringAt(q(past), now), cacheRawQueryStringAt(q(past), past.Add(10*time.Hour)); got != want {
		t.Fatalf("mutlak ham sorgu now'dan bağımsız olmalı: %q %q", got, want)
	}
}
