package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.790 — replika-duyarlı ayarlar chOpts haritasında, her biri KENDİ
// bayrağının altında (operatör 2026-09-19: deployment bu ayarları
// taşımayabilir, imaj taşısın). Kaynak pini: ayar bir Go string'inde,
// başka hiçbir şey şekli zorlamaz.
func TestReplicaAwareSettingsAreFlaggedAndInChOpts(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	i := strings.Index(s, "chOpts := func() *clickhouse.Options {")
	if i < 0 {
		t.Fatal("chOpts kurucusu yok")
	}
	block := s[i:]
	if j := strings.Index(block, "\n\t}\n"); j > 0 {
		block = block[:j]
	}
	cases := []struct {
		flag, setting string
	}{
		{"if cfg.ReadMaxReplicaDelayS > 0 {", `o.Settings["max_replica_delay_for_distributed_queries"] = cfg.ReadMaxReplicaDelayS`},
		{"if cfg.MVDedupBlocks {", `o.Settings["deduplicate_blocks_in_dependent_materialized_views"] = 1`},
		{"if cfg.InsertQuorum >= 2 {", `o.Settings["insert_quorum"] = cfg.InsertQuorum`},
	}
	for _, c := range cases {
		k := strings.Index(block, c.flag)
		if k < 0 {
			t.Errorf("bayrak yok: %s", c.flag)
			continue
		}
		if !strings.Contains(block[k:min(k+300, len(block))], c.setting) {
			t.Errorf("%s bayrağının hemen altında %s bekleniyordu", c.flag, c.setting)
		}
	}
	// Quorum tek başına asılır: paralel + zaman aşımı olmadan açılmaz.
	if k := strings.Index(block, `o.Settings["insert_quorum"] = cfg.InsertQuorum`); k >= 0 {
		tail := block[k:min(k+300, len(block))]
		if !strings.Contains(tail, `o.Settings["insert_quorum_parallel"] = 1`) || !strings.Contains(tail, `o.Settings["insert_quorum_timeout"] = 60000`) {
			t.Error("insert_quorum paralel kip ve 60 sn tavan olmadan açılamaz (replika düşükken INSERT süresiz bekler)")
		}
	}
	for _, name := range []string{`"max_replica_delay_for_distributed_queries"`, `"deduplicate_blocks_in_dependent_materialized_views"`, `"insert_quorum"`} {
		if n := strings.Count(s, name); n != 1 {
			t.Errorf("%s tek yerde, bayrağın altında olmalı (bulunan: %d)", name, n)
		}
	}
}
