-- 0017_attr_kvh_alias.sql — attribute hash indeksi depolama sıkıştırması
-- (dış Distributed prod; uygulama yönetimli kurulumda attr_index.go aynı
-- adımları boot'ta koşar). Boot ASLA bu dosyayı koşmaz (v0.9.613); OPERATÖR
-- clickhouse-client ile ifade ifade koşar, `uptrace_all` → gerçek küme adı.
-- Ön koşul: 0014 uygulanmış.
--
-- ============================================================
-- GEREKÇE (ölçülmüş, CH 26.2, 10M span)
-- ============================================================
-- attr_kvh MATERIALIZED Array(UInt64) = spans'ın %15.5'i (92 MiB); bloom
-- indeksi yalnız 2.98 MiB. Kolonun tek işi indeksi beslemek → ALIAS yeter
-- (indeks INSERT anında ifadeden hesaplanır, kolon diske yazılmaz).
--
-- Neden YENİ ad (attr_kvh2) + yeni hash cityHash64(k, v): analyzer açıkken
-- ALIAS ifadesi sabit taşırsa ('\x1F' ayırıcı) bloom indeksi sorguyla
-- eşleşmez (EXPLAIN indexes=1: 392/392 granül). Sabitsiz ifade eşleşir
-- (8/392). Eski attr_kvh aynı ifadeyle ALIAS'a çevrilir: 0017 öncesi pod'lar
-- `has(attr_kvh, cityHash64(concat(?, '\x1F', ?)))` ile DOĞRU sonuç almaya
-- devam eder (yalnız budama kaybolur). Yeni pod'lar probe ile attr_kvh2'yi
-- görür (attr_index.go probeAttrIndex) ve v2 hash'ine geçer.
--
-- İfadeler attr_index.go attrIndexV2Expr / attrIndexExpr ile BİREBİR
-- (attr_index_migration_test).
--
-- ============================================================
-- UYGULAMA SIRASI
-- ============================================================
--   1. spans_local + sarmalayıcı: ADD COLUMN attr_kvh2, res_kvh2 (ALIAS; metadata).
--   2. spans_local: bloom indeksleri (yalnız YENİ part'lara).
--   3. Pod'ları yeniden başlat → derleyici attr_kvh2 yoluna geçer.
--   4. Eski kolonlar: indeksleri düşür → TEK ALTER'da DROP COLUMN + ADD
--      COLUMN … ALIAS (aynı ifade). DROP COLUMN mutasyonu dosya siler (ucuz,
--      disk hemen boşalır); ad hiçbir an çözümsüz kalmaz — her an doğru.
--   5. (opsiyonel) MATERIALIZE INDEX idx_attr_kvh2 — eski part'larda budama.
-- Geri alma: ADIM 4 için DROP COLUMN + 0014'ün MATERIALIZED ADD COLUMN'u +
-- ADD INDEX idx_attr_kvh (eski part'lar okuma anında hesaplar); ADIM 1-2
-- için DROP INDEX → DROP COLUMN attr_kvh2.

-- ── ADIM 1: v2 ALIAS kolonları ───────────────────────────────────────────
ALTER TABLE spans_local ON CLUSTER uptrace_all
  ADD COLUMN IF NOT EXISTS attr_kvh2 Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(k, v), attr_keys, attr_values);
ALTER TABLE spans_local ON CLUSTER uptrace_all
  ADD COLUMN IF NOT EXISTS res_kvh2 Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(k, v), res_keys, res_values);
ALTER TABLE spans ON CLUSTER uptrace_all
  ADD COLUMN IF NOT EXISTS attr_kvh2 Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(k, v), attr_keys, attr_values);
ALTER TABLE spans ON CLUSTER uptrace_all
  ADD COLUMN IF NOT EXISTS res_kvh2 Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(k, v), res_keys, res_values);

-- ── ADIM 2: bloom indeksleri ─────────────────────────────────────────────
ALTER TABLE spans_local ON CLUSTER uptrace_all
  ADD INDEX IF NOT EXISTS idx_attr_kvh2 attr_kvh2 TYPE bloom_filter(0.01) GRANULARITY 4;
ALTER TABLE spans_local ON CLUSTER uptrace_all
  ADD INDEX IF NOT EXISTS idx_res_kvh2 res_kvh2 TYPE bloom_filter(0.01) GRANULARITY 4;

-- ── ADIM 3: pod'ları yeniden başlat ──────────────────────────────────────
-- DOĞRULA: EXPLAIN indexes = 1 SELECT count() FROM spans_local
--   WHERE time > now() - INTERVAL 10 MINUTE AND has(attr_kvh2, cityHash64('<k>', '<v>'));
--   → Skip / idx_attr_kvh2 satırı görünmeli (yeni part'larda).

-- ── ADIM 4: eski kolonlar ALIAS (önce indeksler, sonra TEK ALTER'da DROP+ADD) ─
-- MODIFY COLUMN … ALIAS KULLANMA: eski part'larda veri kalır ve tabloda başka
-- MATERIALIZED kolon olduğundan o part'lara dokunan her mutasyon kod 47 ile
-- düşer (CH 26.2, ölçüldü). DROP COLUMN dosyaları siler (ucuz, disk hemen
-- boşalır); aynı ALTER'daki ADD ile ad hiçbir an çözümsüz kalmaz.
ALTER TABLE spans_local ON CLUSTER uptrace_all DROP INDEX IF EXISTS idx_attr_kvh;
ALTER TABLE spans_local ON CLUSTER uptrace_all DROP INDEX IF EXISTS idx_res_kvh;
ALTER TABLE spans_local ON CLUSTER uptrace_all
  DROP COLUMN IF EXISTS attr_kvh, ADD COLUMN attr_kvh Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), attr_keys, attr_values);
ALTER TABLE spans_local ON CLUSTER uptrace_all
  DROP COLUMN IF EXISTS res_kvh, ADD COLUMN res_kvh Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), res_keys, res_values);
ALTER TABLE spans ON CLUSTER uptrace_all
  DROP COLUMN IF EXISTS attr_kvh, ADD COLUMN attr_kvh Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), attr_keys, attr_values);
ALTER TABLE spans ON CLUSTER uptrace_all
  DROP COLUMN IF EXISTS res_kvh, ADD COLUMN res_kvh Array(UInt64)
  ALIAS arrayMap((k, v) -> cityHash64(concat(k, '\x1F', v)), res_keys, res_values);
-- İzle: SELECT * FROM system.mutations WHERE table = 'spans_local' AND NOT is_done;

-- ── ADIM 5 (opsiyonel, mesai dışı): eski part'larda v2 budaması ──────────
-- ALTER TABLE spans_local ON CLUSTER uptrace_all MATERIALIZE INDEX idx_attr_kvh2;
-- ALTER TABLE spans_local ON CLUSTER uptrace_all MATERIALIZE INDEX idx_res_kvh2;
