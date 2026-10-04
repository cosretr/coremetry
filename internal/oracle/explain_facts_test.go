package oracle

// explain_facts_test.go — v0.10.1100: AI açıklamasının Oracle gerçekleri
// GroupStatsCache'ten AYNEN gelir (Exceptions satırıyla aynı sayı); kırılımlar
// azalan sıralı, kırılımsız blob zarif (nil dilim).

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestExplainFactsFrom(t *testing.T) {
	st := GroupStats{
		Source: SourceConfig{ID: "o-1", Name: "core-errlog"}, Lag: 16 * time.Minute, LastHour: 9000, PrevHour: 2000, BlobOK: true,
		Breakdown: &ExGroupBreakdown{
			Channels: map[string]uint64{"WEB": 300, "MOB": 600, "ATM": 60},
			Services: map[string]uint64{"svc-b": 5, "svc-a": 5, "svc-c": 9},
		},
	}
	f := ExplainFactsFrom(st)
	if f.SourceID != "o-1" || f.SourceName != "core-errlog" || f.Lag != 16*time.Minute || f.LastHour != 9000 || f.PrevHour != 2000 || !f.BlobOK {
		t.Fatalf("kimlik/saatlik: %+v", f)
	}
	if len(f.Channels) != 3 || f.Channels[0].Name != "MOB" || f.Channels[2].Name != "ATM" {
		t.Fatalf("kanal sırası: %+v", f.Channels)
	}
	if len(f.Services) != 3 || f.Services[0].Name != "svc-c" || f.Services[1].Name != "svc-a" {
		t.Fatalf("servis sırası (eşitlikte ad): %+v", f.Services)
	}
	if e := ExplainFactsFrom(GroupStats{Source: SourceConfig{ID: "o-1"}}); e.Channels != nil || e.Services != nil || e.BlobOK {
		t.Fatalf("kırılımsız blob: %+v", e)
	}
}

func TestGroupStatsCacheExplainFacts(t *testing.T) {
	cfg := Settings{Sources: []SourceConfig{{ID: "o-1", Name: "core-errlog", Enabled: true}}}
	c := NewGroupStatsCache(func(_ context.Context, _ string) ([]byte, error) { return nil, nil }, func() Settings { return cfg })
	ora := chstore.ExceptionGroup{Fingerprint: chstore.OracleGroupFingerprint("o-1", "APP_ERR_042", "OP_TRANSFER"), Type: "APP_ERR_042", Message: "OP_TRANSFER"}
	if f, ok := c.ExplainFacts(ora); !ok || f.SourceName != "core-errlog" {
		t.Fatalf("Oracle grubu: %+v %v", f, ok)
	}
	if _, ok := c.ExplainFacts(chstore.ExceptionGroup{Fingerprint: "0a1b2c3d"}); ok {
		t.Fatal("span grubu Oracle gerçeği almamalı")
	}
}
