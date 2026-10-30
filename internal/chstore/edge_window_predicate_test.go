package chstore

// v0.10.1628 — kök-neden kenar grafiği problem yaşlanınca komşuyu düşürüyordu
// (v0.10.1579 deploy kanıtının ikizi). Komşuluk okumaları artık pencere
// KÜMESİ alıyor (taban + eski çıpaların onset pencereleri). Bu test yüklemin
// her kolunu çiviler: boş küme sınırsız taramaya dönüşmez (son 1 sa), tek
// açık pencere bugünkü `time_bucket >= ?` biçimi, çok pencerede dış alt sınır
// + aynı anahtar kolonunda OR'lu aralıklar; sınırlar 5 dk kovaya hizalı.

import (
	"strings"
	"testing"
	"time"
)

func TestEdgeWindowPredicate(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 12, 3, 0, 0, time.UTC) // kova içi (12:00 kovası)
	cases := []struct {
		name     string
		windows  []EdgeWindow
		wantSQL  string
		wantArgs []time.Time
	}{
		{
			name:     "tek açık pencere — bugünkü biçim, From kovaya hizalı",
			windows:  []EdgeWindow{{From: t0}},
			wantSQL:  "time_bucket >= ?",
			wantArgs: []time.Time{t0.Truncate(5 * time.Minute)},
		},
		{
			name: "eski aralık + açık taban — dış alt sınır + OR",
			windows: []EdgeWindow{
				{From: t0.Add(-3 * time.Hour), To: t0.Add(-150 * time.Minute)},
				{From: t0.Add(-time.Hour)},
			},
			wantSQL: "time_bucket >= ? AND ((time_bucket >= ? AND time_bucket <= ?) OR time_bucket >= ?)",
			wantArgs: []time.Time{
				t0.Add(-3 * time.Hour).Truncate(5 * time.Minute),
				t0.Add(-3 * time.Hour).Truncate(5 * time.Minute),
				t0.Add(-150 * time.Minute).Truncate(5 * time.Minute),
				t0.Add(-time.Hour).Truncate(5 * time.Minute),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sql, args := edgeWindowPredicate(c.windows)
			if sql != c.wantSQL {
				t.Fatalf("SQL = %q, istenen %q", sql, c.wantSQL)
			}
			if len(args) != len(c.wantArgs) {
				t.Fatalf("arg sayısı %d, istenen %d", len(args), len(c.wantArgs))
			}
			for i, a := range args {
				if got, ok := a.(time.Time); !ok || !got.Equal(c.wantArgs[i]) {
					t.Fatalf("arg %d = %v, istenen %v", i, a, c.wantArgs[i])
				}
			}
		})
	}

	t.Run("boş küme son 1 sa'e düşer — sınırsız tarama yok", func(t *testing.T) {
		sql, args := edgeWindowPredicate(nil)
		if sql != "time_bucket >= ?" || len(args) != 1 {
			t.Fatalf("boş küme: %q %v", sql, args)
		}
		got := args[0].(time.Time)
		if d := time.Since(got); d < 55*time.Minute || d > 66*time.Minute {
			t.Fatalf("alt sınır şimdiden %s önce, ~1 sa istenen", d)
		}
	})

	t.Run("her kol time_bucket'a bağlı — indekssiz kol yok", func(t *testing.T) {
		sql, _ := edgeWindowPredicate([]EdgeWindow{
			{From: t0.Add(-5 * time.Hour), To: t0.Add(-4 * time.Hour)},
			{From: t0.Add(-3 * time.Hour), To: t0.Add(-2 * time.Hour)},
			{From: t0.Add(-time.Hour)},
		})
		for _, arm := range strings.Split(sql, " OR ") {
			if !strings.Contains(arm, "time_bucket") {
				t.Fatalf("time_bucket'sız kol: %q", arm)
			}
		}
		if strings.Count(sql, "?") != 6 {
			t.Fatalf("yer tutucu sayısı: %q", sql)
		}
	})
}
