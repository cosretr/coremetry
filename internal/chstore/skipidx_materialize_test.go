package chstore

import (
	"strings"
	"testing"
	"time"
)

func TestSkipMatSQLAndLike(t *testing.T) {
	j := SkipMatJob{Local: "spans_local", Cluster: "c1", Kind: SkipMatKindIndex, Name: "idx_db_stmt_ngram"}
	got := skipMatSQL(j, "20261120")
	want := "ALTER TABLE spans_local ON CLUSTER c1 MATERIALIZE INDEX idx_db_stmt_ngram IN PARTITION ID '20261120'"
	if got != want {
		t.Fatalf("sql\n got %s\nwant %s", got, want)
	}
	j1 := SkipMatJob{Local: "spans", Kind: SkipMatKindProjection, Name: "p_x"}
	if got := skipMatSQL(j1, "20261120"); got != "ALTER TABLE spans MATERIALIZE PROJECTION p_x IN PARTITION ID '20261120'" {
		t.Fatalf("single-node projection sql: %s", got)
	}
	// system.mutations.command (26.2): "(MATERIALIZE INDEX idx IN PARTITION ID '…')"
	like := skipMatCommandLike(SkipMatJob{Kind: SkipMatKindIndex, Name: "idx_name"}, "20261120")
	if !likeMatch("(MATERIALIZE INDEX idx_name IN PARTITION ID '20261120')", like) {
		t.Fatal("own command must match")
	}
	if likeMatch("(MATERIALIZE INDEX idx_name_ngram IN PARTITION ID '20261120')", like) {
		t.Fatal("prefix-named index must not match")
	}
	if likeMatch("(MATERIALIZE INDEX idx_name IN PARTITION ID '20261121')", like) {
		t.Fatal("other partition must not match")
	}
}

// likeMatch — '%' only (test helper).
func likeMatch(s, pat string) bool {
	parts := strings.Split(pat, "%")
	pos := 0
	for i, p := range parts {
		if p == "" {
			continue
		}
		k := strings.Index(s[pos:], p)
		if k < 0 || (i == 0 && k != 0) {
			return false
		}
		pos += k + len(p)
	}
	return strings.HasSuffix(pat, "%") || pos == len(s)
}

func TestSkipMatBackoffAndFail(t *testing.T) {
	if skipMatBackoff(1) != time.Minute || skipMatBackoff(3) != 4*time.Minute || skipMatBackoff(30) != time.Hour {
		t.Fatal("backoff 1m,2m,4m … capped 1h")
	}
	now := time.Unix(1_800_000_000, 0)
	p := SkipMatPartition{ID: "x", State: skipMatRunning, IssuedAt: 1}
	for i := 1; i < skipMatMaxAttempts; i++ {
		skipMatFail(&p, "boom", now)
		if p.State != skipMatPending || p.NextAt != now.Add(skipMatBackoff(i)).UnixMilli() || p.IssuedAt != 0 {
			t.Fatalf("attempt %d: %+v", i, p)
		}
	}
	skipMatFail(&p, "boom", now)
	if p.State != skipMatFailed || p.Attempts != skipMatMaxAttempts {
		t.Fatalf("exhausted → failed: %+v", p)
	}
}

func TestSkipMatNextAndSettle(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	j := SkipMatJob{Partitions: []SkipMatPartition{
		{ID: "20261121", State: skipMatDone},
		{ID: "20261120", State: skipMatPending, NextAt: now.Add(time.Minute).UnixMilli()}, // backing off
		{ID: "20261119", State: skipMatPending},
	}}
	if i := skipMatNextIdx(j, now); i != 2 {
		t.Fatalf("next skips backing-off partition: %d", i)
	}
	if skipMatSettle(j) != "" {
		t.Fatal("pending left → not settled")
	}
	j.Partitions[1].State, j.Partitions[2].State = skipMatDropped, skipMatDone
	if skipMatSettle(j) != skipMatDone {
		t.Fatal("done + dropped → done")
	}
	j.Partitions[0].State = skipMatFailed
	if skipMatSettle(j) != skipMatFailed {
		t.Fatal("any failed → failed")
	}
	if skipMatRunningIdx(j) != -1 {
		t.Fatal("no running")
	}
	c := skipMatCount(j)
	if c.Total != 3 || c.Failed != 1 || c.Done != 1 || c.Dropped != 1 {
		t.Fatalf("counts %+v", c)
	}
}

func TestSkipMatThrottleReason(t *testing.T) {
	if r := skipMatThrottleReason(0, 10, 0.5); r != "" {
		t.Fatalf("clear: %q", r)
	}
	if r := skipMatThrottleReason(1, 10, 0.5); !strings.Contains(r, "mutasyon") {
		t.Fatalf("other mutation: %q", r)
	}
	if r := skipMatThrottleReason(0, skipMatMaxPartsBacklog+1, 0.5); !strings.Contains(r, "merge") {
		t.Fatalf("backlog: %q", r)
	}
	if r := skipMatThrottleReason(0, 10, 0.05); !strings.Contains(r, "disk") {
		t.Fatalf("disk: %q", r)
	}
}

func TestSkipMatHealthNote(t *testing.T) {
	ok := skipMatState{Jobs: []SkipMatJob{{ID: "a", State: skipMatRunning, Partitions: []SkipMatPartition{{ID: "1", State: skipMatPending, Attempts: 2}}}}}
	if bad, _ := skipMatHealthNote(ok); bad {
		t.Fatal("2 attempts → quiet")
	}
	flaky := skipMatState{Jobs: []SkipMatJob{{ID: "a", State: skipMatRunning, Partitions: []SkipMatPartition{{ID: "1", State: skipMatPending, Attempts: 3, Err: "x"}}}}}
	if bad, note := skipMatHealthNote(flaky); !bad || !strings.Contains(note, "a (") {
		t.Fatalf("3 attempts → note: %v %q", bad, note)
	}
	failed := skipMatState{Jobs: []SkipMatJob{{ID: "b", State: skipMatFailed, Partitions: []SkipMatPartition{{ID: "1", State: skipMatFailed, Attempts: 5}}}}}
	if bad, _ := skipMatHealthNote(failed); !bad {
		t.Fatal("failed job → note")
	}
	cancelled := skipMatState{Jobs: []SkipMatJob{{ID: "c", State: skipMatCancelled, Partitions: []SkipMatPartition{{ID: "1", State: skipMatFailed, Attempts: 5}}}}}
	if bad, _ := skipMatHealthNote(cancelled); bad {
		t.Fatal("cancelled job → quiet (operator decided)")
	}
}
