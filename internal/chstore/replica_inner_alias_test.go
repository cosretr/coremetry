package chstore

import (
	"reflect"
	"strings"
	"testing"
)

// v0.10.2434 — host başına "Yeniden kur" sonrası dört farklı adlı iç tablo:
// kart artık onları sahip view / ZK yoluyla tek satırda eşler.

const (
	innerA = ".inner_id.aaaaaaaa-0000-4000-8000-000000000001"
	innerB = ".inner_id.bbbbbbbb-0000-4000-8000-000000000002"
	innerC = ".inner_id.cccccccc-0000-4000-8000-000000000003"
	innerD = ".inner_id.dddddddd-0000-4000-8000-000000000004"
	zk01   = "/clickhouse/tables/01/trace_id_ts"
	zk02   = "/clickhouse/tables/02/trace_id_ts"
)

func aliasRep(host string, shard int, zk string) ReplicaState {
	return ReplicaState{Host: host, Shard: shard, ZKPath: zk, TotalReplicas: 2, ActiveReplicas: 2, Rows: map[string]uint64{}}
}

// prodLikeFixture — 2×2: her host kendi uuid'li iç tablosu, ZK yolu shard başına ortak.
func prodLikeFixture() (map[string][]ReplicaState, map[string]map[string]string, map[string]innerOwner) {
	byTable := map[string][]ReplicaState{
		innerA:  {aliasRep("chnode01", 1, zk01)},
		innerB:  {aliasRep("chnode02", 1, zk01)},
		innerC:  {aliasRep("chnode03", 2, zk02)},
		innerD:  {aliasRep("chnode04", 2, zk02)},
		"spans": {aliasRep("chnode01", 1, "/x/01/spans"), aliasRep("chnode02", 1, "/x/01/spans")},
	}
	eng := "ReplicatedAggregatingMergeTree"
	engineOf := map[string]map[string]string{
		innerA: {"chnode01": eng}, innerB: {"chnode02": eng},
		innerC: {"chnode03": eng}, innerD: {"chnode04": eng},
		"spans": {"chnode01": "ReplicatedMergeTree", "chnode02": "ReplicatedMergeTree"},
	}
	owners := map[string]innerOwner{
		innerA: {View: "trace_id_ts_local", Hosts: []string{"chnode01"}},
		innerB: {View: "trace_id_ts_local", Hosts: []string{"chnode02"}},
		innerC: {View: "trace_id_ts_local", Hosts: []string{"chnode03"}},
		innerD: {View: "trace_id_ts_local", Hosts: []string{"chnode04"}},
	}
	return byTable, engineOf, owners
}

func TestMergeInnerAliasesProdShape(t *testing.T) {
	byTable, engineOf, owners := prodLikeFixture()
	got := mergeInnerAliases(byTable, engineOf, owners)
	want := map[string][]string{innerA: {innerB, innerC, innerD}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
	for _, gone := range []string{innerB, innerC, innerD} {
		if _, ok := byTable[gone]; ok {
			t.Errorf("%s byTable'da kaldı", gone)
		}
		if _, ok := engineOf[gone]; ok {
			t.Errorf("%s engineOf'ta kaldı", gone)
		}
	}
	if len(byTable[innerA]) != 4 || len(engineOf[innerA]) != 4 {
		t.Fatalf("birleşik satır 4 host taşımalı: %d replika, %d motor", len(byTable[innerA]), len(engineOf[innerA]))
	}
	if o := owners[innerA]; o.View != "trace_id_ts_local" || !reflect.DeepEqual(o.Hosts, []string{"chnode01", "chnode02", "chnode03", "chnode04"}) {
		t.Errorf("sahip birleşmedi: %+v", o)
	}
	if len(byTable["spans"]) != 2 {
		t.Error("düz tabloya dokunulmamalı")
	}

	// Karar: her shard'da aynı ZK yolu + 2/2 → tutarlı; "eksik replika" yok.
	expected := map[int][]string{1: {"chnode01", "chnode02"}, 2: {"chnode03", "chnode04"}}
	for sh, hosts := range expected {
		var rs []ReplicaState
		for _, r := range byTable[innerA] {
			if r.Shard == sh {
				rs = append(rs, r)
			}
		}
		d, _ := shardDecision(innerA, "", "", sh, hosts, rs, engineOf[innerA])
		if d.Verdict != ReplicaOK || len(d.Missing) != 0 {
			t.Errorf("shard %d: verdict=%s missing=%v hint=%s", sh, d.Verdict, d.Missing, d.Hint)
		}
	}
}

// ZK yolu farklıysa birleşme yine olur (aynı view), ama karar no_replication:
// gerçek kusur gizlenmez.
func TestMergeInnerAliasesDifferentZKStillFlagged(t *testing.T) {
	byTable, engineOf, owners := prodLikeFixture()
	byTable[innerB][0].ZKPath = "/clickhouse/tables/01/trace_id_ts_other"
	mergeInnerAliases(byTable, engineOf, owners)
	var rs []ReplicaState
	for _, r := range byTable[innerA] {
		if r.Shard == 1 {
			rs = append(rs, r)
		}
	}
	d, _ := shardDecision(innerA, "", "", 1, []string{"chnode01", "chnode02"}, rs, engineOf[innerA])
	if d.Verdict != ReplicaNoReplication {
		t.Errorf("farklı ZK yolu no_replication olmalı, %s", d.Verdict)
	}
}

// Shard'ın öteki host'unda bu MV'nin hiçbir iç tablosu yoksa eksik replika kalır.
func TestMergeInnerAliasesTrulyMissing(t *testing.T) {
	byTable, engineOf, owners := prodLikeFixture()
	delete(byTable, innerB)
	delete(engineOf, innerB)
	delete(owners, innerB)
	mergeInnerAliases(byTable, engineOf, owners)
	var rs []ReplicaState
	for _, r := range byTable[innerA] {
		if r.Shard == 1 {
			rs = append(rs, r)
		}
	}
	d, _ := shardDecision(innerA, "", "", 1, []string{"chnode01", "chnode02"}, rs, engineOf[innerA])
	if d.Verdict != ReplicaMissing || len(d.Missing) != 1 || d.Missing[0].Host != "chnode02" {
		t.Errorf("chnode02 eksik replika olmalı: %s %v", d.Verdict, d.Missing)
	}
}

// Sahip view bilinmiyor (MV satırı yok) — aynı ZK yolu yine eşler.
func TestMergeInnerAliasesByZKPathWithoutOwner(t *testing.T) {
	byTable, engineOf, _ := prodLikeFixture()
	got := mergeInnerAliases(byTable, engineOf, map[string]innerOwner{})
	want := map[string][]string{innerA: {innerB}, innerC: {innerD}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("aliases = %v, want %v", got, want)
	}
}

// Aynı host iki ad taşıyorsa (eski sahipsiz iç tablo + yenisi) birleşme yok.
func TestMergeInnerAliasesHostClashSkipped(t *testing.T) {
	byTable, engineOf, owners := prodLikeFixture()
	byTable[innerB] = append(byTable[innerB], aliasRep("chnode01", 1, zk01))
	got := mergeInnerAliases(byTable, engineOf, owners)
	if len(got) != 0 {
		t.Fatalf("çakışmada birleşme olmamalı: %v", got)
	}
	if _, ok := byTable[innerB]; !ok {
		t.Error("çakışan ad yerinde kalmalı")
	}
}

func TestInnerAliasNotes(t *testing.T) {
	byTable, engineOf, owners := prodLikeFixture()
	notes := innerAliasNotes(mergeInnerAliases(byTable, engineOf, owners), owners)
	if len(notes) != 1 || !strings.HasPrefix(notes[0], "trace_id_ts_local:") || !strings.Contains(notes[0], innerD) {
		t.Errorf("not: %v", notes)
	}
}
