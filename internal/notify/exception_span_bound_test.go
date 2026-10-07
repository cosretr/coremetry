package notify

// exception_span_bound_test.go — v0.10.1116: span kanal geçişlerinin
// last_seen alt sınırı hiçbir adayı düşüremez.

import (
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExceptionSpanScanLookbackPinned(t *testing.T) {
	if exSpanScanLookback != 2*time.Hour+15*time.Minute {
		t.Errorf("exSpanScanLookback = %s", exSpanScanLookback)
	}
	if exSpanScanLookback < exChannelActiveWin || exSpanScanLookback < exChannelNewMaxAge {
		t.Error("sınır tazelik pencerelerinin altında")
	}
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	for _, st := range []string{chstore.ExStateNew, chstore.ExStateRegressed} {
		f := exceptionChannelFilter(st, "exclude", now)
		if f.ActiveFromNs != now.Add(-exSpanScanLookback).UnixNano() || f.ActiveToNs != 0 {
			t.Errorf("%s span süzgeci: %+v", st, f)
		}
		o := exceptionChannelFilter(st, "only", now)
		if o.ActiveFromNs != now.Add(-exOracleScanLookback).UnixNano() {
			t.Errorf("%s Oracle süzgeci değişmemeli: %+v", st, o)
		}
	}
}

// Izgara: first_seen/last_seen yaşları 0..6 sa, her durum ve oluşum. Kabul
// edilen HER grup sınırın içinde; sınır dışı her grup reddedilir.
func TestChannelCandidateNeverOlderThanSpanBound(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	bound := exceptionChannelFilter(chstore.ExStateNew, "exclude", now).ActiveFromNs
	states := []string{chstore.ExStateNew, chstore.ExStateRegressed, chstore.ExStateAcknowledged, chstore.ExStateResolved}
	accepted := 0
	for _, st := range states {
		for lastMin := -5; lastMin <= 360; lastMin++ {
			for _, firstExtra := range []int{0, 1, 5, 14, 15, 16, 60, 24 * 60} {
				for _, occ := range []uint64{1, 2, 500} {
					g := chstore.ExceptionGroup{
						LastSeen:    now.Add(-time.Duration(lastMin) * time.Minute).UnixNano(),
						FirstSeen:   now.Add(-time.Duration(lastMin+firstExtra) * time.Minute).UnixNano(),
						Occurrences: occ,
					}
					// Span grubu: kapı gecikmesiz (GroupNotifyGate → 0), gnow = now.
					ok := isChannelCandidate(g, st, now)
					for _, prio := range []string{"P1", "P2"} {
						if exceptionChannelKey(g, st, prio, now) != "" {
							ok = true
						}
					}
					if ok {
						accepted++
						if g.LastSeen < bound {
							t.Fatalf("aday sınır dışında: state=%s last=%d dk first+%d occ=%d", st, lastMin, firstExtra, occ)
						}
					}
				}
			}
		}
	}
	if accepted == 0 {
		t.Fatal("ızgara hiç aday üretmedi — test boş")
	}
}
