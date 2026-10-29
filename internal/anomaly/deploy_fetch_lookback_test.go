package anomaly

// v0.10.1579 — deploy kanıtı kalıcı hipotezden yaşla düşüyordu.
//
// Belirti (kod denetimi, operatör onaylı): tik başına deploy okuması
// [now−30m, now]; seçim [onset−30m, onset]. Onset'i 30 dk'dan eski açık
// critical problemin deploy adayı, RecentDeploy'u ve derin-kanıt kapısı
// her 30 s'lik tam-satır yeniden yazımda kayboluyordu. Davranışsal kanıt
// gerçek CH'ye karşı: rootcause_deploy_age_chsmoke_test.go. Bu test
// pencere hesabının her kolunu (taban, büyüme, tavan, geçersiz onset)
// ve geniş okumanın satır tavanını çiviler.

import (
	"testing"
	"time"
)

func TestDeployFetchLookbackCoversOldestAnchorOnset(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC).UnixNano()
	ago := func(d time.Duration) int64 { return now - int64(d) }
	cases := []struct {
		name   string
		onsets []int64
		want   time.Duration
	}{
		{name: "çıpa yok → banner tabanı", onsets: nil, want: 30 * time.Minute},
		{name: "şu an başlayan çıpa", onsets: []int64{now}, want: 30 * time.Minute},
		{name: "10 dk önce başlayan — taban yetmez", onsets: []int64{ago(10 * time.Minute)}, want: 40 * time.Minute},
		{name: "en eski çıpa belirler", onsets: []int64{ago(5 * time.Minute), ago(3 * time.Hour), ago(time.Hour)}, want: 3*time.Hour + 30*time.Minute},
		{name: "tavan 24h", onsets: []int64{ago(30 * time.Hour)}, want: 24 * time.Hour},
		{name: "tavanın hemen altı", onsets: []int64{ago(23*time.Hour + 30*time.Minute)}, want: 24 * time.Hour},
		{name: "sıfır onset yok sayılır", onsets: []int64{0}, want: 30 * time.Minute},
		{name: "gelecekteki onset yok sayılır", onsets: []int64{now + int64(time.Hour)}, want: 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deployFetchLookback(now, tc.onsets)
			if got != tc.want {
				t.Fatalf("deployFetchLookback = %s, istenen %s", got, tc.want)
			}
			// Sözleşme: en eski geçerli çıpanın seçim penceresinin alt ucu
			// (onset−30m) okumanın içinde — tavan altında.
			for _, o := range tc.onsets {
				if o <= 0 || o > now {
					continue
				}
				lo := o - int64(evidenceDeployLookback)
				if now-int64(got) > lo && time.Duration(now-lo) <= evidenceDeployFetchCap {
					t.Fatalf("onset %s önce: seçim alt ucu okumanın dışında", time.Duration(now-o))
				}
			}
		})
	}
}

func TestDeployFetchLimitWidensWithWindow(t *testing.T) {
	cases := []struct {
		lb   time.Duration
		want int
	}{
		{30 * time.Minute, 200},
		{31 * time.Minute, 1000},
		{24 * time.Hour, 1000},
	}
	for _, tc := range cases {
		if got := deployFetchLimit(tc.lb); got != tc.want {
			t.Fatalf("deployFetchLimit(%s) = %d, istenen %d", tc.lb, got, tc.want)
		}
	}
}
