package chstore

// dangling_mv_admin_test.go — v0.10.762 sarkan MV: saf tespit (host başına,
// TO'lu MV hariç, sıfır uuid hariç), nesne adı → kanonik ad, ON CLUSTER
// sökme; ulaşılabilirlik: dropCombinedMV artık temizliğini çağırır, boot
// dedektörü New()'da.

import (
	"os"
	"strings"
	"testing"
)

func TestDanglingFromRows(t *testing.T) {
	rows := []mvTableRow{
		// sağlıklı: view + iç tablo aynı host
		{Host: "n1", Name: "service_summary_5m_local", UUID: "aaaa", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.service_summary_5m_local (`x` UInt8) ENGINE = ReplicatedAggregatingMergeTree AS SELECT"},
		{Host: "n1", Name: ".inner_id.aaaa", UUID: "1111", Engine: "ReplicatedAggregatingMergeTree"},
		// sarkan: view var, iç tablo yok (n2)
		{Host: "n2", Name: "service_summary_5m_local", UUID: "bbbb", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.service_summary_5m_local (`x` UInt8) ENGINE = ReplicatedAggregatingMergeTree AS SELECT"},
		// iç tablo başka host'ta olsa sayılmaz (host başına)
		{Host: "n1", Name: ".inner_id.bbbb", UUID: "2222", Engine: "ReplicatedAggregatingMergeTree"},
		// TO'lu MV: iç tablosu olmaz → sarkan değil
		{Host: "n2", Name: "span_links_reverse_mv", UUID: "cccc", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT"},
		// sıfır uuid → atla
		{Host: "n2", Name: "weird", UUID: zeroUUID, Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.weird ENGINE = AggregatingMergeTree AS SELECT"},
		// kanonik olmayan sarkan (migrations MV'si) → listelenir, Canonical=false
		{Host: "n2", Name: "rollup_custom_mv", UUID: "dddd", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.rollup_custom_mv ENGINE = AggregatingMergeTree AS SELECT"},
	}
	got := danglingFromRows(rows)
	if len(got) != 2 {
		t.Fatalf("2 sarkan bekleniyordu, %d: %+v", len(got), got)
	}
	if got[0].Host != "n2" || got[0].View != "rollup_custom_mv" || got[0].Canonical {
		t.Errorf("ilk satır (ada göre sıralı, kanonik değil): %+v", got[0])
	}
	if got[1].View != "service_summary_5m_local" || got[1].UUID != "bbbb" || !got[1].Canonical {
		t.Errorf("ikinci satır: %+v", got[1])
	}
}

func TestCanonicalMVForObjectAndStrip(t *testing.T) {
	if name, ok := canonicalMVForObject("service_summary_5m"); !ok || name != "service_summary_5m" {
		t.Errorf("çıplak kanonik ad: %q %v", name, ok)
	}
	if name, ok := canonicalMVForObject("service_summary_5m_local"); !ok || name != "service_summary_5m" {
		t.Errorf("_local terfi adı: %q %v", name, ok)
	}
	if _, ok := canonicalMVForObject("not_a_view"); ok {
		t.Error("bilinmeyen ad kanonik sayıldı")
	}
	in := "CREATE MATERIALIZED VIEW IF NOT EXISTS x_local ON CLUSTER `prod_eu` ENGINE = ReplicatedAggregatingMergeTree('/p/{shard}/x', '{replica}') AS SELECT 1"
	out := stripOnCluster(in)
	if strings.Contains(out, "ON CLUSTER") || !strings.Contains(out, "x_local ENGINE") {
		t.Errorf("ON CLUSTER sökülmedi: %s", out)
	}
	if stripOnCluster("CREATE TABLE t ON CLUSTER c AS x") != "CREATE TABLE t AS x" {
		t.Error("tırnaksız küme adı")
	}
	for _, bad := range []string{"", "a b", "x;drop", "`x`", "a.b"} {
		if chObjRe.MatchString(bad) {
			t.Errorf("%q nesne adı olarak kabul edildi", bad)
		}
	}
}

func TestDanglingMVReachable(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "s.dropLeftoverViewObjects(ctx, mv)") {
		t.Error("dropCombinedMV artık view temizliğini çağırmalı")
	}
	if !strings.Contains(s, "s.LogDanglingMVs(") {
		t.Error("New() boot sonrası sarkan MV logunu koşmalı")
	}
	bs, err := os.ReadFile("trace_backfill_shards.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bs), "func (s *Store) clusterHostRows(") {
		t.Error("clusterHostRows yardımcısı (tüm replikalar) yok")
	}
}
