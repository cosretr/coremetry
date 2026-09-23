package chstore

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// v0.10.881 (paritesi #8, dilim 1) — service_env_summary_5m: katalogda, kardeşiyle
// BİREBİR state kolonları (aynı *Merge okuyucuları çalışsın), boyutlu ORDER BY,
// terfi ailesinde (küme kipinde _local + Distributed), kardeşiyle aynı shard
// anahtarı, trace_id'siz tablo listesinde; katalogun sonunda; kapsama kararı saf.
func TestServiceEnvSummaryMVShape(t *testing.T) {
	const name = "service_env_summary_5m"
	names := canonicalMVNames()
	found := false
	for _, n := range names {
		if n == name {
			found = true
		}
	}
	if !found {
		t.Fatal("katalogda yok")
	}
	ddl := canonicalMVDDL(name)
	for _, w := range []string{
		"ORDER BY (service_name, cluster, deploy_env, time_bucket)",
		"GROUP BY service_name, cluster, deploy_env, time_bucket",
		"AS cluster,", "deploy_env,", "FROM spans", "TTL toDate(time_bucket) + INTERVAL 90 DAY", "PARTITION BY toDate(time_bucket)",
	} {
		if !strings.Contains(ddl, w) {
			t.Errorf("DDL %q taşımıyor", w)
		}
	}
	states := regexp.MustCompile(`AS ([a-z_]+_state)`)
	mine := states.FindAllStringSubmatch(ddl, -1)
	sib := states.FindAllStringSubmatch(canonicalMVDDL("service_summary_5m"), -1)
	if len(mine) != len(sib) || len(mine) == 0 {
		t.Fatalf("state kolon sayısı: %d vs kardeş %d", len(mine), len(sib))
	}
	for i := range mine {
		if mine[i][1] != sib[i][1] {
			t.Errorf("state kolonu ayrıştı: %s vs %s", mine[i][1], sib[i][1])
		}
	}
	if !highVolumeTables[name] || defaultShardPolicy[name] != defaultShardPolicy["service_summary_5m"] || !tablesWithoutTraceID[name] {
		t.Error("terfi / shard anahtarı / trace_id'siz kayıtları kardeşle aynı olmalı")
	}
	if names[len(names)-1] != name {
		t.Errorf("yeni MV dilimin sonunda olmalı (konumsal pinler), son: %s", names[len(names)-1])
	}
}

func TestEnvSummaryCoversDecision(t *testing.T) {
	from := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	if envSummaryCovers(time.Time{}, true, from) || envSummaryCovers(from.Add(-time.Hour), false, from) {
		t.Fatal("boş/başarısız probe kapsamaz")
	}
	if !envSummaryCovers(from.Add(-time.Hour), true, from) || !envSummaryCovers(from, true, from) {
		t.Fatal("ilk kova pencere başından önce/eşit → kapsar")
	}
	if envSummaryCovers(from.Add(5*time.Minute), true, from) {
		t.Fatal("ilk kova pencere başından sonra → kapsamaz (ham yol)")
	}
}
