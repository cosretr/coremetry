package chstore

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// spanmetrics_1s_ttl.go — v0.10.2409: the 1-second spanmetrics tier keeps
// 24 hours of rows instead of 6. Operator-approved 2026-11-21 after the
// v0.10.2408 spanmetrics recreate: second-level detail is useful for the
// last day; the daily partition was already dropped at 1 day by the
// retention enforcer (retention_mv.go), so the extra disk is small.
//
// Fresh installs get the TTL from the CREATE in store.go. Existing installs
// get a metadata-only MODIFY TTL on the MV's inner table(s) at boot, skipped
// when every host already carries the 24-hour TTL.

const spanmetrics1sTTL = "time_bucket + INTERVAL 24 HOUR"

func spanmetrics1sTTLStmt(inner, onCluster string) string {
	return fmt.Sprintf("ALTER TABLE `%s`%s MODIFY TTL %s"+
		" SETTINGS materialize_ttl_after_modify = 0, alter_sync = 0",
		inner, onCluster, spanmetrics1sTTL)
}

// spanmetrics1sTTLCurrent reports whether a CREATE statement already carries
// the 24-hour row TTL (CH may print the interval in either form).
func spanmetrics1sTTLCurrent(create string) bool {
	return strings.Contains(create, "INTERVAL 24 HOUR") ||
		strings.Contains(create, "toIntervalHour(24)")
}

// applySpanmetrics1sTTL is best-effort: failures log and boot continues.
func (s *Store) applySpanmetrics1sTTL(ctx context.Context) {
	name := "spanmetrics_1s"
	if s.clusterMode() && highVolumeTables[name] {
		name += "_local"
	}
	inners := s.mvInnerTablesCluster(ctx, name)
	if len(inners) == 0 {
		return
	}
	if creates, err := s.createQueriesEverywhere(ctx, inners); err == nil {
		current := true
		for _, inner := range inners {
			qs := creates[inner]
			if len(qs) == 0 {
				current = false
				break
			}
			for _, q := range qs {
				if !spanmetrics1sTTLCurrent(q) {
					current = false
				}
			}
		}
		if current {
			return
		}
	}
	for _, inner := range inners {
		if err := s.execWithReadonlyRetry(ctx, spanmetrics1sTTLStmt(inner, s.onCluster())); err != nil && len(inners) == 1 {
			log.Printf("[chstore] spanmetrics_1s 24h TTL not applied on %s: %v", inner, err)
		}
	}
}
