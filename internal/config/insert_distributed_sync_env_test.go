package config

import (
	"os"
	"testing"
)

// v0.10.778 — COREMETRY_CH_INSERT_DISTRIBUTED_SYNC: yalnız 1/true açar;
// yokluk ve başka her değer KAPALI (spool davranışı değişmez).
func TestInsertDistributedSyncEnv(t *testing.T) {
	cases := []struct {
		val  string
		set  bool
		want bool
	}{
		{"", false, false}, {"1", true, true}, {"true", true, true}, {"0", true, false}, {"yes", true, false},
	}
	for _, c := range cases {
		_ = os.Unsetenv("COREMETRY_CH_INSERT_DISTRIBUTED_SYNC")
		if c.set {
			t.Setenv("COREMETRY_CH_INSERT_DISTRIBUTED_SYNC", c.val)
		}
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.ClickHouse.InsertDistributedSync != c.want {
			t.Errorf("val=%q set=%v → %v, istenen %v", c.val, c.set, cfg.ClickHouse.InsertDistributedSync, c.want)
		}
	}
}
