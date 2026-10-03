package api

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.325 — ayar doğrulama sınırları.
func TestValidateDBSlowQuery(t *testing.T) {
	ok := chstore.DefaultDBSlowQuery()
	if err := validateDBSlowQuery(ok); err != nil {
		t.Fatalf("varsayılan geçmeli: %v", err)
	}
	for _, tc := range []struct {
		n string
		c chstore.DBSlowQueryConfig
	}{
		{"eşik küçük", chstore.DBSlowQueryConfig{ThresholdMs: 50, CriticalMs: 5000, MinExecutions: 20, ForBuckets: 2}},
		{"critical < eşik", chstore.DBSlowQueryConfig{ThresholdMs: 1000, CriticalMs: 500, MinExecutions: 20, ForBuckets: 2}},
		{"taban 0", chstore.DBSlowQueryConfig{ThresholdMs: 1000, CriticalMs: 5000, MinExecutions: 0, ForBuckets: 2}},
		{"kova 13", chstore.DBSlowQueryConfig{ThresholdMs: 1000, CriticalMs: 5000, MinExecutions: 20, ForBuckets: 13}},
		{"cooldown negatif", chstore.DBSlowQueryConfig{ThresholdMs: 1000, CriticalMs: 5000, MinExecutions: 20, ForBuckets: 2, CooldownSec: -1}},
	} {
		if err := validateDBSlowQuery(tc.c); err == nil {
			t.Errorf("%s: hata bekleniyordu", tc.n)
		}
	}
}

// v0.10.1073 — db-health vidaları: sınırlar + eski sekmenin PUT'u (health
// alanı yok) saklı değeri ezmez.
func TestValidateDBHealthAndKeepStored(t *testing.T) {
	base := chstore.DefaultDBSlowQuery()
	for _, tc := range []struct {
		n   string
		mut func(h *chstore.DBHealthConfig)
	}{
		{"hata % sıfır", func(h *chstore.DBHealthConfig) { h.ErrorPct = 0 }},
		{"hata % 100 üstü", func(h *chstore.DBHealthConfig) { h.ErrorPct = 101 }},
		{"p99 çok küçük", func(h *chstore.DBHealthConfig) { h.P99Ms = 10 }},
		{"çağrı tabanı 0", func(h *chstore.DBHealthConfig) { h.MinCalls = 0 }},
		{"p99 artış katı 1 altı", func(h *chstore.DBHealthConfig) { h.P99RiseFactor = 0.9 }},
		{"p99 artış katı 100 üstü", func(h *chstore.DBHealthConfig) { h.P99RiseFactor = 101 }},
		{"çağıran çağrı tabanı 0", func(h *chstore.DBHealthConfig) { h.MinCallerCalls = 0 }},
		{"çağıran 0", func(h *chstore.DBHealthConfig) { h.MinCallers = 0 }},
		{"tavan 0", func(h *chstore.DBHealthConfig) { h.MaxNewPerTick = 0 }},
	} {
		c := base
		h := *base.Health
		tc.mut(&h)
		c.Health = &h
		if err := validateDBSlowQuery(c); err == nil {
			t.Errorf("%s: hata bekleniyordu", tc.n)
		}
	}

	off := false
	stored := chstore.DefaultDBSlowQuery()
	stored.Health.Enabled, stored.Health.ErrorPct = &off, 8
	in := chstore.DBSlowQueryConfig{Enabled: true, ThresholdMs: 1500, CriticalMs: 5000, MinExecutions: 20, ForBuckets: 2}
	got := keepStoredHealth(in, stored)
	if got.Health == nil || got.Health.On() || got.Health.ErrorPct != 8 || got.ThresholdMs != 1500 {
		t.Errorf("saklı sağlık ayarı korunmalı, gelen yavaş-ifade alanları yazılmalı: %+v %+v", got, got.Health)
	}
	h := chstore.DefaultDBHealth()
	in.Health = &h
	if got := keepStoredHealth(in, stored); !got.Health.On() {
		t.Error("gövde health taşıyorsa o yazılır")
	}
}
