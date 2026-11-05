package chstore

// v0.10.1816 — ml_learned (audit §6.7): etkin revizyon = anahtarın en büyük
// rev'i; okumalar sınırlı (FINAL, LIMIT, max_execution_time); rev zaman
// damgası iki yazıcıda çakışmaz ve geri gitmez.

import (
	"strings"
	"testing"
	"time"
)

func TestMLLearnedSQLBounded(t *testing.T) {
	for _, q := range []string{mlLearnedLatestSQL(false), mlLearnedLatestSQL(true), mlProfileCallsSQL()} {
		for _, want := range []string{"FINAL", "max_execution_time"} {
			if !strings.Contains(q, want) {
				t.Errorf("sorguda %q yok:\n%s", want, q)
			}
		}
	}
	if q := mlLearnedLatestSQL(false); !strings.Contains(q, "LIMIT 1 BY key") || !strings.Contains(q, "ORDER BY key, rev DESC") || !strings.Contains(q, "LIMIT ? OFFSET ?") {
		t.Errorf("etkin revizyon = en büyük rev, sayfalı:\n%s", q)
	}
	if q := mlProfileCallsSQL(); !strings.Contains(q, "week_start >= ? AND week_start < ?") || !strings.Contains(q, "part = '_calls'") || !strings.Contains(q, "LIMIT ?") {
		t.Errorf("profil girdisi hafta sınırlı + _calls:\n%s", q)
	}
}

func TestMLLearnedNextRev(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	cases := []struct {
		prev, want uint64
	}{
		{0, 1_800_000_000_000},
		{1_700_000_000_000, 1_800_000_000_000},
		{1_800_000_000_000, 1_800_000_000_001}, // aynı ms: geri gitmez, çakışmaz
		{1_900_000_000_000, 1_900_000_000_001}, // saat geride: yine artar
	}
	for _, c := range cases {
		if got := MLLearnedNextRev(c.prev, now); got != c.want {
			t.Errorf("prev %d: %d, want %d", c.prev, got, c.want)
		}
	}
}
