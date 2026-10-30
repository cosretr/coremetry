package anomaly

// v0.10.1628 — kök-neden kenar grafiği problem yaşlanınca komşuyu düşürüyordu
// (v0.10.1579 deploy kanıtının ikizi).
//
// Belirti: tik başına paylaşılan komşuluk okuması [now−60m, now]. Kenar
// yalnız problemin başlangıcında akmışsa (devre kesici açıldı, çağıran
// bağımlılığı bıraktı) 60 dk'dan yaşlı açık problemin komşu adayı her
// 30 s'lik tam-satır yeniden yazımda kayboluyordu. Davranışsal kanıt gerçek
// CH'ye karşı: rootcause_edge_age_chsmoke_test.go. Bu test pencere kümesinin
// her kolunu (taban, taban içinde çıpa, tabana değen çıpa, eski çıpa,
// birleşme, tavan, geçersiz onset) ve SQL yükleminin biçimini çiviler.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestEdgeFetchWindowsCoverAnchorOnsets(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	nowNs := now.UnixNano()
	ago := func(d time.Duration) int64 { return nowNs - int64(d) }
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	open := func(from time.Duration) chstore.EdgeWindow { return chstore.EdgeWindow{From: at(from)} }
	win := func(from, to time.Duration) chstore.EdgeWindow { return chstore.EdgeWindow{From: at(from), To: at(to)} }
	cases := []struct {
		name   string
		onsets []int64
		want   []chstore.EdgeWindow
	}{
		{name: "çıpa yok → yalnız taban 60 dk", onsets: nil, want: []chstore.EdgeWindow{open(time.Hour)}},
		{name: "taze çıpa (10 dk) tabanın içinde", onsets: []int64{ago(10 * time.Minute)}, want: []chstore.EdgeWindow{open(time.Hour)}},
		{name: "tam sınırda (75 dk) — onset−15m = taban başı", onsets: []int64{ago(75 * time.Minute)}, want: []chstore.EdgeWindow{open(90 * time.Minute)}},
		{name: "70 dk — onset penceresi tabana değiyor → taban genişler", onsets: []int64{ago(70 * time.Minute)}, want: []chstore.EdgeWindow{open(85 * time.Minute)}},
		{name: "3 sa önceki çıpa → ayrı onset penceresi", onsets: []int64{ago(3 * time.Hour)},
			want: []chstore.EdgeWindow{win(3*time.Hour+15*time.Minute, 3*time.Hour-15*time.Minute), open(time.Hour)}},
		{name: "üst üste binen iki eski çıpa birleşir", onsets: []int64{ago(3 * time.Hour), ago(3*time.Hour - 20*time.Minute)},
			want: []chstore.EdgeWindow{win(3*time.Hour+15*time.Minute, 3*time.Hour-35*time.Minute), open(time.Hour)}},
		{name: "ayrık iki eski çıpa ayrı kalır, sıralı", onsets: []int64{ago(2 * time.Hour), ago(5 * time.Hour)},
			want: []chstore.EdgeWindow{
				win(5*time.Hour+15*time.Minute, 5*time.Hour-15*time.Minute),
				win(2*time.Hour+15*time.Minute, 2*time.Hour-15*time.Minute),
				open(time.Hour),
			}},
		{name: "tavan 24 sa: başlangıç kırpılır", onsets: []int64{ago(24*time.Hour + 5*time.Minute)},
			want: []chstore.EdgeWindow{win(24*time.Hour, 24*time.Hour-10*time.Minute), open(time.Hour)}},
		{name: "tavanın tamamen dışında → düşer", onsets: []int64{ago(30 * time.Hour)}, want: []chstore.EdgeWindow{open(time.Hour)}},
		{name: "gelecek / sıfır onset yok sayılır", onsets: []int64{nowNs + int64(time.Hour), 0, -5}, want: []chstore.EdgeWindow{open(time.Hour)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := edgeFetchWindows(nowNs, c.onsets)
			if len(got) != len(c.want) {
				t.Fatalf("pencere sayısı %d, istenen %d: %+v", len(got), len(c.want), got)
			}
			for i := range got {
				if !got[i].From.Equal(c.want[i].From) || !got[i].To.Equal(c.want[i].To) {
					t.Fatalf("pencere %d = [%s, %s], istenen [%s, %s]", i,
						got[i].From.Format(time.RFC3339), got[i].To.Format(time.RFC3339),
						c.want[i].From.Format(time.RFC3339), c.want[i].To.Format(time.RFC3339))
				}
			}
		})
	}
}
