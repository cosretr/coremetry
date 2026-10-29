//go:build chsmoke

package logstore

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: CountPatterns'ın sürdürme
// pencereleri GERÇEK ClickHouse'a karşı (tip + sınır). Pencere i = [prevStarts[i],
// üst), taban [baseStart, en eski başlangıç); prevStarts boşken sayım bugünkü
// gibi (taban cari pencerenin başına kadar).
//
//	CH_SMOKE_ADDR=localhost:19000 go test -tags=chsmoke ./internal/logstore/ -run TestCountPatternsPrevWindowBoundaries -v

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/config"
)

func TestCountPatternsPrevWindowBoundaries(t *testing.T) {
	addr := os.Getenv("CH_SMOKE_ADDR")
	if addr == "" {
		t.Skip("CH_SMOKE_ADDR verilmedi — duman testi atlanıyor")
	}
	st, err := chstore.New(config.CHConfig{Addr: addr, Database: "default", DialTimeout: "10s"}, config.RetentionConfig{})
	if err != nil {
		t.Fatalf("şema kurulamadı: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	cur := now.Add(-5 * time.Minute)
	prev0 := now.Add(-10 * time.Minute)
	prev1 := now.Add(-15 * time.Minute)
	base := now.Add(-60 * time.Minute)
	svc := fmt.Sprintf("smoke-lp-%d", now.UnixNano())
	marker := fmt.Sprintf("SMOKEPAT-%d", now.UnixNano())
	// Sayılar sınırların iki yanına: tam başlangıç dahil, tam bitiş hariç.
	at := map[time.Time]int{
		cur:                         3, // cari (tam sınır dahil)
		now.Add(-1 * time.Minute):   2, // cari
		cur.Add(-time.Nanosecond):   4, // prev0 (cur'un hemen öncesi)
		prev0:                       1, // prev0 (tam sınır)
		prev1.Add(time.Second):      5, // prev1
		prev1.Add(-time.Nanosecond): 7, // taban
		base.Add(time.Minute):       6, // taban
		base.Add(-time.Minute):      9, // pencere dışı
	}
	for ts, n := range at {
		for k := 0; k < n; k++ {
			if err := st.Conn().Exec(ctx, `INSERT INTO logs (time, body, service_name, attr_keys, attr_values, res_keys, res_values)
				SETTINGS async_insert = 0
				VALUES (fromUnixTimestamp64Nano(?), ?, ?, [], [], [], [])`,
				ts.UnixNano(), "ERROR "+marker+" failed", svc); err != nil {
				t.Fatalf("insert: %v", err)
			}
		}
	}
	cs := NewCH(st)
	pat := PatternSpec{Name: "smoke", Regex: marker, Tokens: []string{marker}}

	got, err := cs.CountPatterns(ctx, []PatternSpec{pat}, cur, []time.Time{prev0, prev1}, base, now)
	if err != nil {
		t.Fatalf("CountPatterns: %v", err)
	}
	s := got[0]
	if s.Cur != 5 || len(s.Prev) != 2 || s.Prev[0] != 5 || s.Prev[1] != 5 || s.Base != 13 {
		t.Fatalf("cur=%d prev=%v base=%d, want 5 [5 5] 13", s.Cur, s.Prev, s.Base)
	}
	if s.Service != svc || s.LastSeenNs == 0 {
		t.Fatalf("servis / son görülme taşınmadı: %+v", s)
	}

	legacy, err := cs.CountPatterns(ctx, []PatternSpec{pat}, cur, nil, base, now)
	if err != nil {
		t.Fatalf("CountPatterns (legacy): %v", err)
	}
	if l := legacy[0]; l.Cur != 5 || l.Prev != nil || l.Base != 23 {
		t.Fatalf("legacy cur=%d prev=%v base=%d, want 5 nil 23", l.Cur, l.Prev, l.Base)
	}
}
