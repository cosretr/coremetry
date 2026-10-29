package anomaly

// v0.10.1586 — Davis paritesi FAZ 0.5 / C: log_pattern ("new" ve "spike")
// iki ardışık pencere ister. Belirti (FP sınıfı d_single_window): tek bir 5 dk
// patlama recorder'ın 1 dk'lık tikleriyle 5 kez yeniden yazılıp terfi
// sürdürmesini geçiyordu.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

type dwellLogStore struct {
	logstore.Store
	stats      []logstore.PatternStats
	gotPrev    []time.Time
	gotCur     time.Time
	gotBase    time.Time
	countCalls int
}

func (s *dwellLogStore) CountPatterns(_ context.Context, pats []logstore.PatternSpec, cur time.Time, prev []time.Time, base, _ time.Time) ([]logstore.PatternStats, error) {
	s.countCalls++
	s.gotCur, s.gotPrev, s.gotBase = cur, prev, base
	out := make([]logstore.PatternStats, len(pats))
	copy(out, s.stats)
	return out, nil
}

func (s *dwellLogStore) VerifyPatterns(context.Context, []logstore.PatternSpec, time.Time, time.Time) ([]logstore.PatternVerification, error) {
	return nil, nil // CH: doğrulama yok
}

func TestDetectLogPatternsRequiresPrevWindow(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	ora := patterns[0].Name // "Oracle errors (ORA-)"
	const svc = "orders-api"
	// stats[0] = ORA- deseni: cur 100, taban yok → "new"; stats[1] = TNS:
	// taban 120 / saat (≈10 / pencere), cur 60 → "spike" (6×).
	mk := func(prevOra, prevTns uint64) []logstore.PatternStats {
		return []logstore.PatternStats{
			{Cur: 100, Base: 0, Service: svc, Prev: []uint64{prevOra}},
			{Cur: 60, Base: 110, Service: svc, Prev: []uint64{prevTns}},
		}
	}
	names := func(out []LogPatternAnomaly) map[string]string {
		m := map[string]string{}
		for _, a := range out {
			m[a.Pattern] = a.Kind
		}
		return m
	}
	tns := patterns[1].Name

	t.Run("dwell 1: tek pencere, sürdürme penceresi istenmez", func(t *testing.T) {
		st := &dwellLogStore{stats: mk(0, 0)}
		out, err := detectLogPatterns(context.Background(), st, now, 5*time.Minute, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if st.gotPrev != nil {
			t.Fatalf("dwell 1 sürdürme penceresi istememeli: %v", st.gotPrev)
		}
		if got := names(out); got[ora] != "new" || got[tns] != "spike" {
			t.Fatalf("bugünkü davranış: %v", got)
		}
	})

	t.Run("dwell 2: önceki pencere sessizse olay yok", func(t *testing.T) {
		st := &dwellLogStore{stats: mk(0, 5)}
		out, err := detectLogPatterns(context.Background(), st, now, 5*time.Minute, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.gotPrev) != 1 || !st.gotPrev[0].Equal(now.Add(-10*time.Minute)) || !st.gotCur.Equal(now.Add(-5*time.Minute)) {
			t.Fatalf("sürdürme penceresi [cur−5dk, cur) olmalı: cur=%v prev=%v", st.gotCur, st.gotPrev)
		}
		if st.countCalls != 1 {
			t.Fatalf("tek CountPatterns çağrısı: %d", st.countCalls)
		}
		if len(out) != 0 {
			t.Fatalf("tek pencerelik patlama olay yazmamalı: %v", names(out))
		}
	})

	t.Run("dwell 2: önceki pencere de eşikte → olay", func(t *testing.T) {
		st := &dwellLogStore{stats: mk(40, 50)}
		out, err := detectLogPatterns(context.Background(), st, now, 5*time.Minute, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(out); got[ora] != "new" || got[tns] != "spike" {
			t.Fatalf("sürmüş patlama olay olmalı: %v", got)
		}
	})

	t.Run("dwell 2: AKTİF olay muaf (yalnız açılış kapılı)", func(t *testing.T) {
		st := &dwellLogStore{stats: mk(0, 0)}
		active := map[string]bool{chstore.FingerprintAnomaly("log_pattern", ora, svc): true}
		out, err := detectLogPatterns(context.Background(), st, now, 5*time.Minute, 2, active)
		if err != nil {
			t.Fatal(err)
		}
		if got := names(out); got[ora] != "new" || got[tns] != "" {
			t.Fatalf("yalnız aktif desen tazelenmeli: %v", got)
		}
	})
}

func TestLogPatternWindowsLegacyRatio(t *testing.T) {
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	cur, base := now.Add(-5*time.Minute), now.Add(-time.Hour)
	if prev, wr := logPatternWindows(now, cur, base, 5*time.Minute, 1); prev != nil || wr != 5.0/60 {
		t.Fatalf("dwell 1 bugünkü oran: %v %v", prev, wr)
	}
	prev, wr := logPatternWindows(now, cur, base, 5*time.Minute, 3)
	if len(prev) != 2 || !prev[1].Equal(now.Add(-15*time.Minute)) || wr != 5.0/45 {
		t.Fatalf("dwell 3: prev=%v wr=%v (taban [−60, −15) dk)", prev, wr)
	}
}
