package chstore

// mv_inner_peer_test.go — v0.10.832 incelemesi (ÖNEMLİ): "Eşten kur" dalının
// DAVRANIŞ testi. Öncesinde yalnız kaynak-metin pini vardı ve iki anlamsal
// tersine çevirme (nesne uuid'si yerine ad uuid'si gömmek, doğrulamayı
// count()'a düşürmek) TÜM paketi yeşil bırakıyordu — "test edilmiş ama
// ulaşılamaz" sınıfı.
//
// Dal canlı bir eş bağlantısı ister; o yüzden `repairInnerOn` iki bağlantıyı
// PARAMETRE alır (bağlantı çözümü repairInnerFromPeer'da kaldı) ve buradaki
// betikli sahte bağlantılarla gerçek sıra koşulur.

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	peerViewUUID = "11111111-2222-4333-8444-555555555555"
	peerObjUUID  = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	peerZK       = "/clickhouse/tables/shop/01/db_summary_5m_local"
)

func peerRow() *DanglingMV {
	return &DanglingMV{
		Host: "ch-03", View: "db_summary_5m_local", UUID: peerViewUUID,
		PeerHost: "ch-04", PeerAddr: "ch-04:9000", Canonical: true,
	}
}

// localDDL — ayar AÇIK biçimli MV metni (nesne uuid'si görünür).
func localDDL(obj string) string {
	return "CREATE MATERIALIZED VIEW coremetry.db_summary_5m_local UUID '" + peerViewUUID +
		"' TO INNER UUID '" + obj + "' (`x` Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1"
}

// peerInnerDDL — eşin iç tablosunun create_table_query'si (ayar 1 olduğu için
// tablo düzeyi uuid metinde ZATEN var).
func peerInnerDDL(obj string) string {
	return "CREATE TABLE coremetry.`" + innerTableName(peerViewUUID) + "` UUID '" + obj +
		"' (`x` Int) ENGINE = ReplicatedAggregatingMergeTree('/clickhouse/tables/shop/{shard}/db_summary_5m_local', '{replica}') ORDER BY x"
}

func TestRepairInnerOnBehaviour(t *testing.T) {
	inner := innerTableName(peerViewUUID)

	t.Run("mutlu yol: nesne uuid'si MV'nin hedefi, ZK yolu eşle aynı", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
			{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerObjUUID}},
			{match: "zookeeper_path", vals: []any{peerZK}},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, peerInnerDDL(peerObjUUID)}},
			{match: "zookeeper_path", vals: []any{peerZK}},
		}}
		steps, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err != nil {
			t.Fatalf("mutlu yol düştü: %v (%v)", err, steps)
		}
		if len(local.execs) != 1 {
			t.Fatalf("tam bir CREATE koşmalıydı: %v", local.execs)
		}
		ddl := local.execs[0]
		// Koşan metin: ad VIEW uuid'sinden, nesne uuid'si MV'nin hedefi.
		if !strings.Contains(ddl, "`"+inner+"`") {
			t.Errorf("ad view uuid'sinden kurulmalı: %s", ddl)
		}
		if !strings.Contains(ddl, "UUID '"+peerObjUUID+"'") {
			t.Errorf("nesne uuid'si MV'nin TO INNER UUID'si olmalı: %s", ddl)
		}
		if strings.Contains(ddl, "UUID '"+peerViewUUID+"'") {
			t.Errorf("ADIN uuid'si nesne uuid'si olarak gömülmüş — MV bulamaz (ve CH UUID collision ile reddeder): %s", ddl)
		}
		if strings.Contains(ddl, "ON CLUSTER") {
			t.Errorf("yalnız bu host: %s", ddl)
		}
		if len(peer.execs) != 0 {
			t.Errorf("eşe DDL koşulmamalı: %v", peer.execs)
		}
		if !strings.Contains(strings.Join(steps, "\n"), peerZK) {
			t.Errorf("doğrulama ZK yolunu yazmalı: %v", steps)
		}
	})

	// Sunucu ayarı YOK SAYARSA (eski sürüm / kısıtlı profil) eşin metni tablo
	// uuid'si TAŞIMAZ ve onu injectTableUUID koyar. Mutlu yolun fixture'ı
	// uuid'i zaten metinde taşıdığı için injection orada HİÇ koşmaz — bu
	// hücre olmadan "ad uuid'sini nesne uuid'si olarak göm" mutasyonu
	// davranışsal olarak ısırmıyordu.
	t.Run("eşin metni tablo uuid'si taşımıyor → NESNE uuid'si gömülür", func(t *testing.T) {
		bare := "CREATE TABLE coremetry.`" + inner + "` (`x` Int) ENGINE = ReplicatedAggregatingMergeTree('/clickhouse/tables/shop/{shard}/db_summary_5m_local', '{replica}') ORDER BY x"
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
			{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerObjUUID}},
			{match: "zookeeper_path", vals: []any{peerZK}},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, bare}},
			{match: "zookeeper_path", vals: []any{peerZK}},
		}}
		if _, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow()); err != nil {
			t.Fatalf("düştü: %v", err)
		}
		ddl := local.execs[0]
		if !strings.Contains(ddl, "`"+inner+"` UUID '"+peerObjUUID+"'") {
			t.Errorf("NESNE uuid'si ad'dan hemen sonra gömülmeliydi: %s", ddl)
		}
		if strings.Contains(ddl, "UUID '"+peerViewUUID+"'") {
			t.Errorf("ADIN uuid'si gömülmüş — MV hedefini bulamaz, CH UUID collision verir: %s", ddl)
		}
	})

	t.Run("yerel nesne uuid'si okunamıyor → RET, hiçbir DDL koşmaz", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", err: errors.New("read: i/o timeout")},
		}}
		peer := &scriptConn{name: "eş"}
		_, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err == nil {
			t.Fatal("uuid okunamazken eylem reddedilmeli (fail-closed)")
		}
		if len(local.execs) != 0 || len(peer.execs) != 0 {
			t.Errorf("RET'e rağmen DDL koştu: %v %v", local.execs, peer.execs)
		}
		if len(peer.queries) != 0 {
			t.Errorf("yerel kapı geçilmeden eşe gidilmemeli: %v", peer.queries)
		}
	})

	t.Run("MV'nin to_inner_uuid'si YOK (Nil/eski sürüm) → RET + sonucu söyler", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{
				"CREATE MATERIALIZED VIEW coremetry.db_summary_5m_local (`x` Int) ENGINE = AggregatingMergeTree AS SELECT 1"}},
		}}
		_, err := (&Store{}).repairInnerOn(context.Background(), local, &scriptConn{name: "eş"}, peerRow())
		if err == nil {
			t.Fatal("TO INNER UUID yokken eylem reddedilmeli")
		}
		if !strings.Contains(err.Error(), "to_inner_uuid Nil") {
			t.Errorf("ret metni dördüncü nedeni (eski sürüm/ATTACH → Nil) saymalı: %v", err)
		}
		if !strings.Contains(err.Error(), "tarihçe getirmez") {
			t.Errorf("ret metni SONUCU da söylemeli: %v", err)
		}
	})

	t.Run("eşin nesne uuid'si tutmuyor → RET, DDL koşmaz", func(t *testing.T) {
		const other = "99999999-7777-4888-8999-aaaaaaaaaaaa"
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{other, peerInnerDDL(other)}},
		}}
		_, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err == nil {
			t.Fatal("eşin uuid'si tutmazken eylem reddedilmeli")
		}
		if !strings.Contains(err.Error(), "BULAMAZ") {
			t.Errorf("ret gerekçesi MV'nin hedefi çözememesi olmalı: %v", err)
		}
		if len(local.execs) != 0 {
			t.Errorf("RET'e rağmen DDL koştu: %v", local.execs)
		}
	})

	t.Run("doğrulama YANLIŞ uuid görüyor → hata", func(t *testing.T) {
		const wrong = "deadbeef-7777-4888-8999-aaaaaaaaaaaa"
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
			{match: "SELECT toString(uuid) FROM system.tables", vals: []any{wrong}},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, peerInnerDDL(peerObjUUID)}},
		}}
		_, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err == nil {
			t.Fatal("yanlış nesne uuid'si doğrulamadan geçti — count()>0 bakan doğrulama tam bunu yutuyordu")
		}
		if len(local.execs) != 1 {
			t.Errorf("DDL koşmuş olmalı (hata doğrulamada): %v", local.execs)
		}
	})

	// v0.10.832 incelemesi: tarihçenin GELMESİ ZK yolu eşliğine bağlı ve bu
	// ölçülmüyordu. Nesne uuid'si eşitliği bunu ÖLÇMEZ — bu depoda iç tablonun
	// Replicated argümanları AD tabanlıdır (replicatedArgs: '<önek>/{shard}/<ad>'),
	// içinde `{uuid}` yoktur; yolu `{shard}` makrosu ayırır ve "aynı shard,
	// FARKLI ZK yolu" bu filoda bilinen bir sınıftır (no_replication).
	t.Run("ZK yolu eşten FARKLI → hata (tarihçe gelmez)", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
			{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerObjUUID}},
			{match: "zookeeper_path", vals: []any{"/clickhouse/tables/shop/02/db_summary_5m_local"}},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, peerInnerDDL(peerObjUUID)}},
			{match: "zookeeper_path", vals: []any{peerZK}},
		}}
		_, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err == nil {
			t.Fatal("ayrı ZK grupları 'onarıldı' sayıldı — tarihçe hiç gelmez")
		}
		if !strings.Contains(err.Error(), "{shard}") {
			t.Errorf("hata kök nedeni (makro ayrışması) söylemeli: %v", err)
		}
	})

	// system.replicas OKUNAMAZSA bu bir UYARIDIR, hata değil: DDL koştu ve
	// başarılı bir onarımı okuma hatası yüzünden "başarısız" göstermek
	// v0.10.820'nin düzelttiği sınıftır.
	t.Run("ZK okuması düşerse UYARI, hata değil", func(t *testing.T) {
		local := &scriptConn{name: "yerel", steps: []scriptStep{
			{match: "SELECT create_table_query", vals: []any{localDDL(peerObjUUID)}},
			{match: "SELECT toString(uuid) FROM system.tables", vals: []any{peerObjUUID}},
			{match: "zookeeper_path", err: errors.New("table is not replicated")},
		}}
		peer := &scriptConn{name: "eş", steps: []scriptStep{
			{match: "SELECT toString(uuid), create_table_query", vals: []any{peerObjUUID, peerInnerDDL(peerObjUUID)}},
		}}
		steps, err := (&Store{}).repairInnerOn(context.Background(), local, peer, peerRow())
		if err != nil {
			t.Fatalf("okuma hatası onarımı başarısız göstermemeli: %v", err)
		}
		if !strings.Contains(strings.Join(steps, "\n"), "uyarı") {
			t.Errorf("atlama SESSİZ olmamalı: %v", steps)
		}
	})
}
