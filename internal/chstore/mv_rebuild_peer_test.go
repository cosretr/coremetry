package chstore

import (
	"strings"
	"testing"
)

// v0.10.2434 — "Yeniden kur" shard eşinin uuid'lerini taşır.

const (
	rpViewUUID  = "aaaaaaaa-1111-4111-8111-111111111111"
	rpInnerUUID = "bbbbbbbb-2222-4222-8222-222222222222"
)

func rpCov() []MVHostState {
	return []MVHostState{
		{Host: "chnode01", View: "trace_id_ts_local", State: MVStateMissing},
		{Host: "chnode02", View: "trace_id_ts_local", State: MVStateOK, UUID: rpViewUUID, InnerUUID: rpInnerUUID, TargetUUID: rpInnerUUID},
		{Host: "chnode03", View: "trace_id_ts_local", State: MVStateOK, UUID: "cccccccc-3333-4333-8333-333333333333", InnerUUID: "dddddddd-4444-4444-8444-444444444444"},
		{Host: "chnode02", View: "other_mv", State: MVStateOK, UUID: "eeeeeeee-5555-4555-8555-555555555555", InnerUUID: "ffffffff-6666-4666-8666-666666666666"},
	}
}

var rpShards = map[string]int{"chnode01": 1, "chnode02": 1, "chnode03": 2, "chnode04": 2}

func TestMVRebuildPeerIDs(t *testing.T) {
	got := mvRebuildPeerIDs(rpCov(), "chnode01", "trace_id_ts_local", rpShards)
	if got != (mvPeerIDs{Host: "chnode02", View: rpViewUUID, Inner: rpInnerUUID}) {
		t.Fatalf("eş: %+v", got)
	}
	// Shard 2: eş yalnız aynı shard'dan (chnode03); shard 1'in chnode02'si aday değil.
	if got := mvRebuildPeerIDs(rpCov(), "chnode04", "trace_id_ts_local", rpShards); got.Host != "chnode03" {
		t.Errorf("shard 2 eşi chnode03 olmalı: %+v", got)
	}
	// Shard bilinmiyor → eş yok.
	if got := mvRebuildPeerIDs(rpCov(), "chnode09", "trace_id_ts_local", rpShards); got.Host != "" {
		t.Errorf("bilinmeyen shard'da eş olmamalı: %+v", got)
	}
	// Sağlıksız eş aday değil.
	cov := rpCov()
	cov[1].State = MVStatePlain
	if got := mvRebuildPeerIDs(cov, "chnode01", "trace_id_ts_local", rpShards); got.Host != "" {
		t.Errorf("plain eş aday olmamalı: %+v", got)
	}
	// MV hedefi iç tablo değilse (TargetUUID ≠ InnerUUID) kopyalanmaz.
	cov = rpCov()
	cov[1].TargetUUID = "99999999-9999-4999-8999-999999999999"
	if got := mvRebuildPeerIDs(cov, "chnode01", "trace_id_ts_local", rpShards); got.Host != "" {
		t.Errorf("hedefi kaymış eş aday olmamalı: %+v", got)
	}
	// Tek host'luk shard → eski davranış.
	if got := mvRebuildPeerIDs(rpCov(), "chnode01", "trace_id_ts_local", map[string]int{"chnode01": 1, "chnode02": 2}); got.Host != "" {
		t.Errorf("shard eşi yokken sıfır değer: %+v", got)
	}
}

func TestInjectMVPeerUUIDs(t *testing.T) {
	ids := mvPeerIDs{Host: "chnode02", View: rpViewUUID, Inner: rpInnerUUID}
	ddl := "CREATE MATERIALIZED VIEW IF NOT EXISTS trace_id_ts_local \n ENGINE = ReplicatedAggregatingMergeTree('/clickhouse/tables/{shard}/trace_id_ts', '{replica}') ORDER BY x AS SELECT 1"
	got, ok := injectMVPeerUUIDs(ddl, "trace_id_ts_local", ids)
	want := "CREATE MATERIALIZED VIEW IF NOT EXISTS trace_id_ts_local UUID '" + rpViewUUID + "' TO INNER UUID '" + rpInnerUUID + "' \n ENGINE"
	if !ok || !strings.HasPrefix(got, want) {
		t.Fatalf("got %q", got)
	}
	// Önek benzeri ad eşleşmez (trace_id_ts ≠ trace_id_ts_local).
	if _, ok := injectMVPeerUUIDs(ddl, "trace_id_ts", ids); ok {
		t.Error("ad sınırı yok")
	}
	// TO <tablo> biçimi: iç tablo yok → dokunma.
	to := "CREATE MATERIALIZED VIEW IF NOT EXISTS x_mv TO x AS SELECT 1"
	if out, ok := injectMVPeerUUIDs(to, "x_mv", ids); ok || out != to {
		t.Error("TO'lu MV değişmemeli")
	}
	// Zaten UUID'li metin → dokunma.
	has := "CREATE MATERIALIZED VIEW IF NOT EXISTS x_mv UUID '" + rpViewUUID + "' ENGINE = AggregatingMergeTree ORDER BY k AS SELECT 1"
	if _, ok := injectMVPeerUUIDs(has, "x_mv", ids); ok {
		t.Error("UUID iki kez eklenmemeli")
	}
	// Sıfır değer → dokunma.
	if _, ok := injectMVPeerUUIDs(ddl, "trace_id_ts_local", mvPeerIDs{}); ok {
		t.Error("eş yokken metin değişmemeli")
	}
	// Distributed sarmalayıcı (CREATE TABLE) → dokunma.
	if _, ok := injectMVPeerUUIDs("CREATE TABLE IF NOT EXISTS trace_id_ts AS trace_id_ts_local ENGINE = Distributed(c, db, t, rand())", "trace_id_ts", ids); ok {
		t.Error("CREATE TABLE değişmemeli")
	}
}
