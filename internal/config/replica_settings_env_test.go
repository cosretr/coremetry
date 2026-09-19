package config

import (
	"os"
	"strings"
	"testing"
)

// v0.10.790 — replika-duyarlı ayarların env köprüsü: sayısal olanlar
// bozuk değerde yok sayılır (sessizce 0 olmaz, uyarı loglanır), bool olan
// yalnız 1/true ile açılır. Quorum 0/1 kapalı demektir (chOpts ≥2 arar).
func TestReplicaAwareSettingsEnv(t *testing.T) {
	t.Run("read max replica delay", func(t *testing.T) {
		cases := []struct {
			val  string
			set  bool
			want int
		}{
			{"", false, 0}, {"60", true, 60}, {"0", true, 0}, {"abc", true, 0}, {"-5", true, 0},
		}
		for _, c := range cases {
			_ = os.Unsetenv("COREMETRY_CH_READ_MAX_REPLICA_DELAY")
			if c.set {
				t.Setenv("COREMETRY_CH_READ_MAX_REPLICA_DELAY", c.val)
			}
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ClickHouse.ReadMaxReplicaDelayS != c.want {
				t.Errorf("val=%q set=%v → %d, istenen %d", c.val, c.set, cfg.ClickHouse.ReadMaxReplicaDelayS, c.want)
			}
		}
	})
	t.Run("mv dedup blocks", func(t *testing.T) {
		cases := []struct {
			val  string
			set  bool
			want bool
		}{
			{"", false, false}, {"1", true, true}, {"true", true, true}, {"0", true, false}, {"yes", true, false},
		}
		for _, c := range cases {
			_ = os.Unsetenv("COREMETRY_CH_MV_DEDUP_BLOCKS")
			if c.set {
				t.Setenv("COREMETRY_CH_MV_DEDUP_BLOCKS", c.val)
			}
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ClickHouse.MVDedupBlocks != c.want {
				t.Errorf("val=%q set=%v → %v, istenen %v", c.val, c.set, cfg.ClickHouse.MVDedupBlocks, c.want)
			}
		}
	})
	t.Run("insert quorum", func(t *testing.T) {
		cases := []struct {
			val  string
			set  bool
			want int
		}{
			{"", false, 0}, {"2", true, 2}, {"1", true, 1}, {"auto", true, 0}, {"-1", true, 0},
		}
		for _, c := range cases {
			_ = os.Unsetenv("COREMETRY_CH_INSERT_QUORUM")
			if c.set {
				t.Setenv("COREMETRY_CH_INSERT_QUORUM", c.val)
			}
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ClickHouse.InsertQuorum != c.want {
				t.Errorf("val=%q set=%v → %d, istenen %d", c.val, c.set, cfg.ClickHouse.InsertQuorum, c.want)
			}
		}
	})
}

// v0.10.790 — imaj varsayılanları: okuma gecikme eşiği 60, MV tekilleştirme
// açık; insert_quorum İMAJDA YOK (erişilebilirlik bedeli, opt-in).
func TestReplicaAwareSettingsImageDefaults(t *testing.T) {
	src, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{"ENV COREMETRY_CH_READ_MAX_REPLICA_DELAY=60", "ENV COREMETRY_CH_MV_DEDUP_BLOCKS=1"} {
		if !strings.Contains(s, want) {
			t.Errorf("Dockerfile %q taşımalı", want)
		}
	}
	if strings.Contains(s, "COREMETRY_CH_INSERT_QUORUM=") {
		t.Error("insert_quorum imaj varsayılanı OLMAMALI — replika düşükken INSERT hata verir; bilinçli opt-in")
	}
}
