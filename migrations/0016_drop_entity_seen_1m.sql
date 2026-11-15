-- 0016_drop_entity_seen_1m.sql — entity_seen_1m'i (0011 ADIM 6) düşürür.
-- v0.10.2265. Operatör onayı: docs/architecture/entity-s6-cost-note.md
-- madde 3 — depoda bu nesneyi SELECT'leyen kod yok; her span INSERT'inde
-- 5m ikiziyle aynı fan-out bedelini ödüyor ve ~5 kat satır üretiyordu.
-- entity_seen_5m DOKUNULMAZ (tüm okuyucular onu okur).
--
-- Kim koşar: Admin → ClickHouse → "K8s entity katmanı" sihirbazının Uygula'sı
-- 0011'den HEMEN SONRA bu dosyayı da koşar (entityLayerStatements); elle de
-- koşulabilir. Boot yolunda (cluster_name set / tek düğüm) aynı düşürme
-- dropRetiredEntitySeen1m ile (iç tablo `.inner_id.<uuid>` önce, hacim
-- guard'ı kapalı) yapılır.
--
-- SIRA: önce MV (kaynak INSERT trigger'ı kesilir; combined MV olduğu için
-- iç tablosu da onunla düşer), sonra Distributed sarmalayıcı.
-- IF EXISTS: tekrar koşmak ve nesnenin zaten olmadığı replikada koşmak
-- güvenli. SYNC: Atomic DB'de tembel DROP Replicated znode'unu bırakır
-- (0011_entity_layer_rollback.sql başlığı).
--
-- İç tablo 50 GB'ı (max_table_size_to_drop) aşıyorsa MV DROP'u Code 359
-- verir: o host'ta `SELECT uuid FROM system.tables WHERE name =
-- 'entity_seen_1m_local'` → `DROP TABLE `.inner_id.<uuid>` SYNC SETTINGS
-- max_table_size_to_drop = 0` önce, sonra bu dosya (store.go dropCombinedMV).
--
-- Geri alma: 0011'in ADIM 6'daki iki entity_seen_1m ifadesi nesneyi BOŞ
-- olarak yeniden kurar; düşürülen tarihçe geri gelmez.

DROP TABLE IF EXISTS entity_seen_1m_local ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS entity_seen_1m ON CLUSTER uptrace_all SYNC;
