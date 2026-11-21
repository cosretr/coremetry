package chstore

import (
	"os"
	"strings"
	"testing"
)

func TestSpanmetrics1sTTLStmt(t *testing.T) {
	got := spanmetrics1sTTLStmt(".inner_id.abc", " ON CLUSTER `c`")
	want := "ALTER TABLE `.inner_id.abc` ON CLUSTER `c` MODIFY TTL time_bucket + INTERVAL 24 HOUR SETTINGS materialize_ttl_after_modify = 0, alter_sync = 0"
	if got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSpanmetrics1sTTLCurrent(t *testing.T) {
	if !spanmetrics1sTTLCurrent("... TTL time_bucket + toIntervalHour(24) SETTINGS") {
		t.Fatal("normalised 24h form not recognised")
	}
	if spanmetrics1sTTLCurrent("... TTL time_bucket + toIntervalHour(6) SETTINGS") {
		t.Fatal("6h treated as current")
	}
}

func TestSpanmetrics1sCreateAndTierAgree(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "CREATE MATERIALIZED VIEW IF NOT EXISTS spanmetrics_1s")
	if i < 0 || !strings.Contains(s[i:i+2500], "TTL "+spanmetrics1sTTL) {
		t.Fatal("spanmetrics_1s CREATE must carry the 24h TTL")
	}
	for _, tier := range spanmetricTiers {
		if tier.table == "spanmetrics_1s" && tier.ttl.Hours() != 24 {
			t.Fatalf("tier ttl %v, want 24h", tier.ttl)
		}
	}
}
