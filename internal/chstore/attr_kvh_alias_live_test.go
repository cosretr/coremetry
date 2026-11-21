package chstore

import (
	"context"
	"testing"
	"time"
)

// attr_kvh_alias_live_test.go — v0.10.2410: 0017 sihirbazının uçtan uca
// koşusu CANLI (scratch) kümede. VARSAYILAN ATLANIR; yalnız 0014 kurulu,
// dış Distributed `spans` + `spans_local` taşıyan ATILACAK bir kümeye:
//
//	COREMETRY_LIVE_CH=localhost:19017 COREMETRY_LIVE_DB=cm COREMETRY_LIVE_CH_WRITE=1 \
//	go test ./internal/chstore/ -run TestLiveAttrKVHAliasWizard -v
//
// (COREMETRY_LIVE_CLUSTER BOŞ: dış Distributed algısı cluster_name'in boş olmasına bağlı.)
func TestLiveAttrKVHAliasWizard(t *testing.T) {
	s := liveWriteStore(t)
	ctx := context.Background()
	pods := func(context.Context) []AttrKVHPod {
		return []AttrKVHPod{{ID: "p", Hostname: "p", Mode: AttrIndexModeName(), IsThisPod: true}}
	}
	registerAttrIndex(s.probeAttrIndex(ctx))
	before := s.AttrKVHAliasStatus(ctx, pods(ctx))
	t.Logf("önce: pending=%v blocker=%q cluster=%s checks=%+v", before.Pending, before.Blocker, before.Cluster, before.Checks)
	for _, st := range before.Steps {
		t.Logf("  %s: %s %s", st.ID, st.State, st.Detail)
	}
	run := func(mat bool) AttrKVHJob {
		if _, err := s.AttrKVHAliasStart(ctx, AttrKVHRunOptions{Materialize: mat, Pods: pods, SwitchWait: 30 * time.Second, Poll: 500 * time.Millisecond}); err != nil {
			t.Fatalf("başlat: %v", err)
		}
		for attrKVHJobSnapshot().Running {
			time.Sleep(200 * time.Millisecond)
		}
		j := attrKVHJobSnapshot()
		for _, l := range j.Log {
			t.Logf("  log ok=%v %s %s", l.OK, l.Head, l.Err)
		}
		if j.Err != "" {
			t.Fatalf("koşu durdu: %s: %s", j.FailedStep, j.Err)
		}
		return j
	}
	run(true)
	after := s.AttrKVHAliasStatus(ctx, pods(ctx))
	for _, st := range after.Steps {
		t.Logf("  sonra %s: %s %s", st.ID, st.State, st.Detail)
	}
	if after.Pending || AttrIndexModeName() != "v2" {
		t.Fatalf("bitmedi: pending=%v mode=%s", after.Pending, AttrIndexModeName())
	}
	for _, p := range after.Partitions {
		if !p.Materialized {
			t.Errorf("partition %s materialize görünmüyor", p.ID)
		}
	}
	// Tekrar koşu: iş yok → reddedilir; ifadeleri elle tekrar göndermek de güvenli.
	if _, err := s.AttrKVHAliasStart(ctx, AttrKVHRunOptions{Pods: pods}); err == nil {
		t.Error("iş yokken koşu reddedilmeli")
	}
	for _, step := range []string{AttrKVHStepColumns, AttrKVHStepIndexes, AttrKVHStepLegacy} {
		for _, q := range attrKVHStepSQL(step, after.Cluster, "") {
			if err := s.conn.Exec(ctx, q); err != nil {
				t.Errorf("tekrar koşu %s: %v", q, err)
			}
		}
	}
	// Doğruluk: v2 = eski ALIAS = dizi yolu.
	var a, b, c uint64
	_ = s.conn.QueryRow(ctx, `SELECT countIf(has(attr_kvh2, cityHash64('k', 'v7'))), countIf(has(attr_kvh, cityHash64(concat('k', '\x1F', 'v7')))), countIf(attr_values[indexOf(attr_keys, 'k')] = 'v7') FROM spans`).Scan(&a, &b, &c)
	if a == 0 || a != b || b != c {
		t.Errorf("sonuçlar farklı: v2=%d eski=%d dizi=%d", a, b, c)
	}
	// Sonraki mutasyon Code 47 ile düşmemeli (MODIFY yolunun kusuru).
	if err := s.conn.Exec(ctx, "ALTER TABLE spans_local ON CLUSTER "+after.Cluster+" UPDATE attr_values = attr_values WHERE 1 SETTINGS mutations_sync = 2"); err != nil {
		t.Errorf("sonraki mutasyon: %v", err)
	}
}
