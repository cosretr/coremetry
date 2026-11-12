# D1 — anomali tekilleştirme ölçümü (prod, elle, salt okunur)

Tarih: 2026-11-12 · Kaynak: `docs/anomaly/anomaly-reassessment-audit.md` §9 D1 · Kod değişikliği yok.

Bu sorguları prod ClickHouse'ta (clickhouse-client ya da Admin → ClickHouse sorgu ekranı) Coremetry veritabanında elle koşun.
Hepsi salt okunur. Her biri LIMIT ve `max_execution_time` taşır. Zaman kolonu olan tablolarda pencere son 7 gün.
Kolon adları `internal/chstore/store.go` DDL'inden alındı (`ml_events`, `anomaly_events`, `system_settings`).

Not: `ml_events` ve `anomaly_events` `_local` + Distributed çifti değil. Bağlandığınız düğüm tablonun yalnız o düğümdeki kopyasını görür.
Önce 4. sorguyu koşun; tablo birden çok düğümde farklı satır sayısıyla duruyorsa 1–3'ü her düğümde ayrı koşun ya da 4b'deki `clusterAllReplicas` biçimini kullanın.

## 1. Son 7 gün, dedektör başına `ml_events` satırı

```sql
SELECT detector, signal, stage, count() AS rows, uniqExact(service) AS services
FROM ml_events FINAL
WHERE started_at >= now64(9) - INTERVAL 7 DAY
GROUP BY detector, signal, stage
ORDER BY rows DESC
LIMIT 500
SETTINGS max_execution_time = 30;
```

Bu sayı neyi söyler: hangi ML dedektörünün ne kadar olay ürettiğini ve bunların ne kadarının açık, kapanmış, kronik ya da terfi edilmiş olduğunu.

## 2. Aynı servis-zaman kümesi (D2 gruplama kuralı)

D2 kuralı (`internal/ml/group.go`): aynı servisin olayları, pencereleri (`started_at` … `last_seen`) arasındaki boşluk 10 dakikayı (`ml.quality.confirmWindowMin` varsayılanı) aşmıyorsa tek gruptur; zincirleme geçerlidir; `stage = 'chronic'` gruba girmez.
Ayarda teyit penceresi farklıysa iki yerdeki `INTERVAL 10 MINUTE` değerini değiştirin.

### 2a. Toplam

```sql
SELECT
  count()                         AS groups,
  countIf(n_det >= 2)             AS multi_detector_groups,
  countIf(n_sig >= 2)             AS cross_signal_groups,
  sum(n_rows)                     AS rows_in_groups,
  round(sum(n_rows) / count(), 2) AS rows_per_group
FROM (
  SELECT service, grp, count() AS n_rows, uniqExact(detector) AS n_det, uniqExact(signal) AS n_sig
  FROM (
    SELECT service, detector, signal,
      sum(is_new) OVER (PARTITION BY service ORDER BY started_at, id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS grp
    FROM (
      SELECT service, detector, signal, id, started_at,
        if(started_at > max(greatest(last_seen, started_at)) OVER (PARTITION BY service ORDER BY started_at, id
                         ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) + INTERVAL 10 MINUTE, 1, 0) AS is_new
      FROM ml_events FINAL
      WHERE started_at >= now64(9) - INTERVAL 7 DAY AND stage != 'chronic'
    )
  )
  GROUP BY service, grp
)
LIMIT 1
SETTINGS max_execution_time = 60;
```

Bu sayı neyi söyler: `rows_per_group` 1'den ne kadar büyükse aynı olay o kadar çok ayrı satıra bölünüyor; `multi_detector_groups` birden çok dedektörün birbirini teyit ettiği gerçek olay adayı sayısıdır.

### 2b. En sık birlikte tetiklenen dedektör kümeleri

```sql
SELECT dets, count() AS groups, sum(n_rows) AS rows
FROM (
  SELECT service, grp, count() AS n_rows, arrayStringConcat(arraySort(groupUniqArray(detector)), ' + ') AS dets
  FROM (
    SELECT service, detector,
      sum(is_new) OVER (PARTITION BY service ORDER BY started_at, id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) AS grp
    FROM (
      SELECT service, detector, id, started_at,
        if(started_at > max(greatest(last_seen, started_at)) OVER (PARTITION BY service ORDER BY started_at, id
                         ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING) + INTERVAL 10 MINUTE, 1, 0) AS is_new
      FROM ml_events FINAL
      WHERE started_at >= now64(9) - INTERVAL 7 DAY AND stage != 'chronic'
    )
  )
  GROUP BY service, grp
)
GROUP BY dets
ORDER BY groups DESC
LIMIT 30
SETTINGS max_execution_time = 60;
```

Bu sayı neyi söyler: audit §4'teki senaryonun (trace_red + dependency_latency + trace_change_point) prod'da gerçekten ne sıklıkla oluştuğunu.

## 3. `anomaly_events` tür dağılımı

```sql
SELECT kind, count() AS rows, uniqExact(service) AS services, min(started_at) AS first_start, max(last_seen) AS last_seen
FROM anomaly_events FINAL
WHERE last_seen >= now64(9) - INTERVAL 7 DAY
GROUP BY kind
ORDER BY rows DESC
LIMIT 200
SETTINGS max_execution_time = 30;
```

Bu sayı neyi söyler: eski olay tablosuna bugün hangi yolların hâlâ yazdığını (yerel dedektörler, `ml:*` kopyaları, `elastic_ml`, özneler); D9'da silinecek yazıcıların listesi budur.

## 4. Dağıtık kümede yerleşim

### 4a. Küme adı

```sql
SELECT cluster, shard_num, replica_num, host_name
FROM system.clusters
ORDER BY cluster, shard_num, replica_num
LIMIT 100
SETTINGS max_execution_time = 10;
```

Bu sayı neyi söyler: 4b'de kullanılacak küme adını ve kaç shard × replika olduğunu.

### 4b. Tabloların hangi düğümde, hangi motorla durduğu

`<KUME>` yerine 4a'daki küme adını yazın (durum tabloları zamansız; yalnız LIMIT + süre sınırı).

```sql
SELECT hostName() AS host, database, name, engine, total_rows, total_bytes
FROM clusterAllReplicas('<KUME>', system.tables)
WHERE name IN ('ml_events', 'ml_series', 'ml_event_snapshots', 'anomaly_events', 'anomaly_verdicts', 'system_settings')
ORDER BY name, host
LIMIT 200
SETTINGS max_execution_time = 30;
```

Bu sayı neyi söyler: `ml_events`'in her düğümde mi yoksa tek düğümde mi durduğunu, motorun `Replicated*` olup olmadığını ve düğümler arası satır farkını; D2'de kolon yerine şemasız yolun seçilmesinin gerekçesi bu belirsizlikti.

### 4c. Düğüm başına son 7 günün satırı (yalnız 4b birden çok düğüm gösterirse)

```sql
SELECT hostName() AS host, count() AS rows
FROM clusterAllReplicas('<KUME>', currentDatabase(), 'ml_events')
WHERE started_at >= now64(9) - INTERVAL 7 DAY
GROUP BY host
ORDER BY host
LIMIT 50
SETTINGS max_execution_time = 30;
```

Bu sayı neyi söyler: düğümler aynı satırları mı taşıyor (replika) yoksa her pod kendi bağlandığı düğüme mi yazıyor (bölünmüş veri).

## 5. Ayar blob'ları (güncel değer)

```sql
SELECT key, value, updated_at, updated_by
FROM system_settings FINAL
WHERE key IN ('native_detectors', 'ml', 'anomaly_promotion', 'anomaly_sensitivity')
ORDER BY key
LIMIT 10
SETTINGS max_execution_time = 10;
```

Bu sayı neyi söyler: prod'da hangi yerel dedektörlerin ve ML dedektörlerinin açık olduğunu, terfi anahtarlarını ve batch kalıplarını (audit S8); satır yoksa o ayar hiç kaydedilmemiştir ve varsayılan geçerlidir (yerel dedektörler kapalı, ML kapalı).

## Dönüş

Çıktıları (ya da yalnız sayıları) koordinatöre iletin. 2a/2b D3 gölge kararının hacmini, 4b D4 öncesi şema kararını belirler.
