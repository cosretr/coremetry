package chstore

import (
	"os"
	"strings"
	"testing"
)

func TestTraceMVTTLStmts(t *testing.T) {
	ttl, err := buildRetentionTTL("30d", "time_bucket")
	if err != nil {
		t.Fatal(err)
	}
	got := traceMVTTLStmts(".inner_id.abc", " ON CLUSTER `c1`", ttl)
	want := []string{
		"ALTER TABLE `.inner_id.abc` ON CLUSTER `c1` MODIFY SETTING ttl_only_drop_parts = 1",
		"ALTER TABLE `.inner_id.abc` ON CLUSTER `c1` MODIFY TTL toDate(time_bucket) + INTERVAL 30 DAY" +
			" SETTINGS materialize_ttl_after_modify = 0, alter_sync = 0",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d stmts, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stmt %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	// Single node, already-backticked name: no double backticks, no ON CLUSTER.
	single := traceMVTTLStmts("`.inner_id.abc`", "", ttl)[0]
	if single != "ALTER TABLE `.inner_id.abc` MODIFY SETTING ttl_only_drop_parts = 1" {
		t.Errorf("single-node stmt: %s", single)
	}
}

func TestTraceMVTTLCurrent(t *testing.T) {
	const ttl = "toDate(time_bucket) + INTERVAL 30 DAY"
	cur := "CREATE TABLE db.`.inner_id.abc` (`trace_id` String) ENGINE = AggregatingMergeTree " +
		"PARTITION BY toDate(time_bucket) ORDER BY (time_bucket, trace_id) " +
		"TTL toDate(time_bucket) + toIntervalDay(30) SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1"
	old90 := strings.Replace(cur, "toIntervalDay(30)", "toIntervalDay(90)", 1)
	noSetting := strings.Replace(cur, ", ttl_only_drop_parts = 1", "", 1)

	cases := []struct {
		name    string
		creates []string
		hosts   int
		want    bool
	}{
		{"current single node", []string{cur}, 1, true},
		{"current on every host", []string{cur, cur}, 2, true},
		{"old 90d literal", []string{old90}, 1, false},
		{"setting missing", []string{noSetting}, 1, false},
		{"one host drifted", []string{cur, old90}, 2, false},
		{"host missing", []string{cur}, 2, false},
	}
	for _, c := range cases {
		if got := traceMVTTLCurrent(c.creates, ttl, c.hosts); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestSpansRetentionValue(t *testing.T) {
	cases := []struct {
		persisted string
		days      int
		want      string
	}{
		{"48h", 30, "48h"},
		{"", 30, "30d"},
		{"", 0, ""},
	}
	for _, c := range cases {
		if got := spansRetentionValue(c.persisted, c.days); got != c.want {
			t.Errorf("spansRetentionValue(%q,%d)=%q want %q", c.persisted, c.days, got, c.want)
		}
	}
}

// Fresh-install DDL: the trace MVs and profiles carry the storage settings
// the boot ALTERs converge existing installs to.
func TestTraceMVAndProfilesCreateDDL(t *testing.T) {
	for _, mv := range traceRetentionMVs {
		ddl := canonicalMVDDL(mv)
		if ddl == "" {
			t.Fatalf("%s missing from canonicalMVs", mv)
		}
		if strings.Contains(ddl, "INTERVAL 90 DAY") {
			t.Errorf("%s still carries the 90-day TTL (outlives spans)", mv)
		}
		if !strings.Contains(ddl, "ttl_only_drop_parts = 1") {
			t.Errorf("%s: CREATE lacks ttl_only_drop_parts = 1", mv)
		}
	}
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "CREATE TABLE IF NOT EXISTS profiles (")
	if i < 0 {
		t.Fatal("profiles CREATE not found")
	}
	body := src[i:]
	body = body[:strings.Index(body, "`, md)")]
	for _, want := range []string{"pprof_data    String       CODEC(ZSTD(1))", "ttl_only_drop_parts = 1"} {
		if !strings.Contains(body, want) {
			t.Errorf("profiles CREATE lacks %q", want)
		}
	}
	for _, want := range []string{
		"`ALTER TABLE profiles MODIFY SETTING ttl_only_drop_parts = 1`",
		"`ALTER TABLE profiles MODIFY COLUMN pprof_data String CODEC(ZSTD(1))`",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("existing-install ALTER missing: %s", want)
		}
	}
}
