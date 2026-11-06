package chstore

// log_masker_version_test.go — v0.10.1829: maske sürümü geçiş kararı.
// Operator-reported düzeltmesi ("GUID, UUID, id değerlerini yeni log
// pattern'i algılamasın") Drain kimliklerini değiştirir; sürüm değişince
// 24 sa sessiz pencere açılmalı, aynı sürümde açılmamalı.

import (
	"testing"
	"time"
)

func TestNextLogMaskerState(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	quiet := now.Add(LogMaskerQuietWindow).UnixNano()
	cases := []struct {
		name      string
		stored    LogMaskerState
		found     bool
		current   int
		wantWrite bool
		want      LogMaskerState
	}{
		{"upgrade from pre-blob (v1) install", LogMaskerState{}, false, 2, true,
			LogMaskerState{Version: 2, From: 1, ChangedAt: now.UnixNano(), QuietUntil: quiet}},
		{"same version: no write, window kept", LogMaskerState{Version: 2, From: 1, QuietUntil: 7}, true, 2, false,
			LogMaskerState{Version: 2, From: 1, QuietUntil: 7}},
		{"explicit v1 blob → v2", LogMaskerState{Version: 1}, true, 2, true,
			LogMaskerState{Version: 2, From: 1, ChangedAt: now.UnixNano(), QuietUntil: quiet}},
		{"rollback v2 → v1 also quiets", LogMaskerState{Version: 2}, true, 1, true,
			LogMaskerState{Version: 1, From: 2, ChangedAt: now.UnixNano(), QuietUntil: quiet}},
		{"no blob and running v1: record only", LogMaskerState{}, false, 1, true, LogMaskerState{Version: 1}},
		{"corrupt zero version treated as v1", LogMaskerState{Version: 0}, true, 2, true,
			LogMaskerState{Version: 2, From: 1, ChangedAt: now.UnixNano(), QuietUntil: quiet}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, write := NextLogMaskerState(c.stored, c.found, c.current, now)
			if write != c.wantWrite || got != c.want {
				t.Fatalf("got %+v write=%v; want %+v write=%v", got, write, c.want, c.wantWrite)
			}
		})
	}
	if LogMaskerQuietWindow != 24*time.Hour {
		t.Fatalf("quiet window = %v, operator brief says 24h", LogMaskerQuietWindow)
	}
}

func TestLogMaskerStateQuietAt(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	st := LogMaskerState{Version: 2, QuietUntil: now.Add(time.Minute).UnixNano()}
	if !st.QuietAt(now) || st.QuietAt(now.Add(2*time.Minute)) || (LogMaskerState{}).QuietAt(now) {
		t.Fatal("QuietAt boundaries wrong")
	}
}
