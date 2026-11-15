# Entity depolama maliyeti notu — "entity nasıl SAKLANMALI"

**Tarih:** 2026-11-15 · **Durum:** ONAYLANDI (operatör, 2026-11-15) — uygulandı v0.10.2225 · **Taban:** v0.10.2224 (S0 v0.10.2193, S1 v0.10.2214)
**İlgili:** `docs/architecture/entity-autodiscovery-plan.md` (§3.3, §5, K1–K6; "S1 değişikliği" ve "S2–S6 ayarı").
**Rakam etiketleri:** **[Ö]** koddan/şemadan okundu ya da operatör verdi · **[T]** türetildi (aritmetik yanında) · **[V]** varsayım, §9 sorgusuyla ölçülecek. Bu notta prod'da ölçülmüş tek rakam yoktur.

## 1. Karar özeti

Öneri **C + R1b**: üç depo, her biri tek iş. **ClickHouse referans satırları** yalnız uzun ömürlü entity'ler için (cluster, namespace, workload, service, node; S5'te host/db/queue/ext), değişimde + günde bir yazılır. **Geçmiş** zaten 30 günlük `entity_seen_5m` MV'sinde ve Thanos'ta durur. **Şimdiki durum** (pod → iş yükü → servis → cluster, servis → pod'lar) lider tick'inin Redis'e yazdığı canlı dizinden okunur. Pod, container ve pod uçlu kenarlar CH'ye **satır olarak yazılmaz**. Açık ömürlerin %91'i geçicidir [T]; yazımın neredeyse tamamını onlar üretir ve tarif ettikleri telemetriden 150 gün uzun yaşarlar. Şema/DDL ve yeni ayar gerekmez.

## 2. Bugünkü mekanik (koddan, hepsi [Ö])

- `entities` / `entity_relations`: `ReplicatedReplacingMergeTree(version)`, **partition yok**, dedup anahtarı `(…, entity_id, valid_from)`, `TTL last_seen + 180 gün`; 4 host'un her birinde tam kopya (`migrations/0011_entity_layer.sql:83-118`).
- Tick 60 sn, `PodGap` 10 dk, `RefreshEvery = PodGap/2 = 5 dk` (`internal/entity/settings.go:30-34`). Her tick, cluster başına: 6 KSM sorgusu + `entity_seen_5m` son 10 dk + `EntityOpenLifetimes` (`entities FINAL` LIMIT 500K, `entity_relations FINAL` LIMIT 1M, 15 sn tavan) → `DiffLifetimes` → `ThrottleRefresh` → 10K'lık INSERT parçaları.
- `DiffLifetimes`: `now − last_seen > PodGap` ise **tip ayırmadan** kapat + yeniden aç (`diff.go:46-50`).
- Resolver v0 depoyu okumaz: pod→servis `entity_seen_5m` (1 saat, ≤ 500 pod), sahiplik çağıranın KSM satırından (`resolver.go:9-27`). `problems.pod` ve `ml_events.pod` düz ad kolonudur; `ml_events` TTL 30 gün (`chstore/store.go:3136,3150,4242`).
- Redis: `cache.Cache` yalnız dize anahtar + bayt değer sunar (`Get/Set/SetNX/MGet/ScanPrefix/DelPrefix`); **pipeline, hash, zset yok**. `ScanPrefix`/`DelPrefix` `SCAN COUNT 200` ile tüm anahtar uzayını gezer. Zaman aşımı 500 ms, yeniden deneme yok. Redis erişilemezse kilit Noop'a düşer: **her replika lider olur** (`cache/degraded.go`). Emsaller: fleet özeti tek blob + `SetNX` tur hakkı (`coremetry:cluster-fleet:v1:<digest>`); `coremetry:pod:` öneki Coremetry'nin kendi replika kalp atışlarına aittir (`internal/cluster/cluster.go:38`).

## 3. Ölçek aritmetiği

| Kalem | Değer | Kaynak |
|---|---|---|
| Canlı pod / cluster | 40K / 20 | [Ö] operatör |
| Container | 40K × 1,5 = 60K | [V] |
| Kalıcı entity: workload 8K + ns 2K + node 2K + service 3K + cluster 20 | ≈ 15K | [V] |
| Açık entity ömrü | 100K geçici + 15K kalıcı = 115K | [T] (plan §3.3 ile aynı) |
| Kalıcı kenar: cluster→node 2K + cluster→ns 2K + ns→wl 8K | 12K | [T] |
| Pod uçlu kenar: wl→pod 40K + pod→ctr 60K + `runs_on` 40K + `runs` 40K | 180K | [T] |
| Açık ömür toplamı / geçici payı | 307K / 280K = **%91** | [T] |
| Pod churn | 100K yeni pod/gün | [V] (plan varsayımı) |
| Kapanan ömür/gün: entity 100K × 2,5 + kenar 100K × 4,5 | 250K + 450K = 700K | [T] |
| CH satır boyu (sıkıştırmasız): entity ~350 B, kenar ~170 B, karışık ~237 B; sıkıştırma ~6× | | [V] |
| `entity_seen_5m`: 40K × 288 kova × ~1,2 servis/pod | 11,5–14M satır/gün; 30 günde 350–420M | [T] |

## 4. ClickHouse tarafı tasarımlar

**A — S1'deki hâl (kalıcı ömür + periyodik tazeleme).** Tazeleme saklı `last_seen` 5 dk'dan eskiyse yazılır; tick aralığı 60 sn + tick süresi `d` olduğundan satır her **5.** tick'te yazılır: 307K ÷ (5 × (60+d)/60) = **51K–61K satır/dk** [T] (d = 10 sn → 52,6K; S1 notundaki 51K bu aralıkta). Günde 73–88M satır × 237 B ≈ 17–21 GB sıkıştırmasız, ~3 GB/gün/replika × 4 [T/V]. Koddan görülen dört sorun:
1. **Faz kilidi:** aynı tick'te açılan ömürler aynı tick'te tazelenir; yazım düzgün değil, 5 tick'te bir ~307K satırlık patlamadır [T].
2. **Tick tüm tabloyu okur:** `valid_to = 0` ve `last_seen` sıralama anahtarında değil, `FINAL` süzgeçten önce birleştirir; 20 cluster × her tick = kapalı ömürler dahil tablonun tamamı. 180 günde 700K × 180 = **126M satır/dk FINAL** [T, churn V]. Tick günlüğündeki `rows_read` dönen satırı sayar, taramayı değil [Ö].
3. **Gözlemci kesintisi sahte ömür üretir:** syncer 10 dk'dan uzun susarsa **her** açık ömür (cluster ve workload dahil) kapanıp yeniden açılır: 614K satır [T]. Pay dar: saklı `last_seen` tasarım gereği ≤ ~6 dk eski, lider kirası 3 dk [Ö] → tek bir lider devri bütçenin ~9 dk'sını yiyebilir [T]. Redis kesintisinde çok liderli yazım aynı tabloya vurur.
4. **Saklama uyumsuzluğu:** ölü pod satırı 180 gün durur, telemetrisi 30 günde gider [Ö].

**B — Bir kez yaz + canlılık okuma anında MV'den.** Yazım yalnız churn: 700K × 2 = 1,4M satır/gün ≈ **~1K satır/dk**, ~0,33 GB/gün [T] (A'nın 1/50'si). Çözmedikleri: tablo yine churn kadar büyür; tick'in FINAL taraması sürer; `TTL last_seen + 180 gün` hiç tazelenmeyen **açık** satırı siler (operatör `MODIFY TTL` ya da seyrek tazeleme gerekir); yalnız metrikte görünen pod'un canlılığı MV'de yoktur.

**C — Oynaklığa göre katman.** *Kalıcı katman* (~27K açık satır; S2 `serves` ve S5 `calls` ile ≤ ~90K [T/V]): değişimde aç/kapat + günde bir tazeleme (TTL ifadesi DDL'siz geçerli kalsın diye) → **27K satır/gün ≈ 19 satır/dk, ~6,5 MB/gün** [T]. Boşluk kuralı kalıcı tiplere uygulanmaz: kimlik doğal anahtardır, gözlemcinin susması varlığın yeniden doğması değildir. *Geçici katman*: satır yok. `pod:<cid>/<ns>/<ad>` kimliği kendini tarif eder; servis/node/ilk-son görülme `entity_seen_5m`'den (TTL 30 gün = telemetri saklaması) [Ö], iş yükü KSM sahibinden ya da `DerivedWorkload(ad)` saf fonksiyonundan [Ö], yalnız metrikte görünen pod Thanos KSM'den. İş yükünden yukarısı kalıcı katmandan çözülür. Tek başına C'nin zayıf yeri okuma hızıdır: pod yönlü MV okuması önek dışıdır (§5).

**Diğerleri.** *Dictionary (kalıcı tablo üstünde):* SQL içi `dictGet` join'i gerekirse anlamlı; 27K satırda Go bellek haritası aynı işi DDL'siz görür, şimdi gereksiz. *Pod anahtarlı ömür MV'si (syncer'sız):* `spans_local` → `AggregatingMergeTree ORDER BY (cluster, k8s_namespace, k8s_pod, service_name)`, günlük partition; 30 günde ~(100K + 40K) × 1,2 × 30 ≈ 5M satır [T/V], pod yönlü **geçmiş** okuma önekten. Bedeli INSERT yoluna üçüncü MV + operatör DDL'i; yalnız span basan pod'ları kapsar. Geçmiş okumanın hızlandırıcısıdır; S6'daki K6'nın parçası olmalı.

## 5. Redis tasarımları (operatör: "hız açısından Redis iyi bir alternatif olabilir")

Ortak kural: Redis **asla gerçeğin kaynağı değildir**; her değer MV + KSM'den yeniden kurulur. Anahtar bulunamaması "ölü" değil "bilinmiyor" demektir (TTL, flush ya da tahliye) ve okuma S0'ın bugünkü MV + KSM yoluna düşer. "T anında canlı mıydı" sorusu Redis'e hiç sorulmaz: `at` son ~2 tick'ten eskiyse `entity_seen_5m` ve Thanos `at` sorgusu.

**R1 — Canlı dizin.** Lider her tick, zaten çektiği KSM anlık görüntüsünü + `entity_seen_5m` son 1 saatini (`SeenWindow` [Ö]) + kalıcı katmanı birleştirip Redis'e yazar. İki yerleşim:
- *R1a, entity başına anahtar + TTL:* `…:p:<cid>/<ns>/<pod>` anahtarı ~80 B + değer (iş yükü 35 + servis 25 + node 25 + uid 36 + iki zaman 16 + ayraç ≈ 150 B) + Redis ek yükü ~95 B ≈ 325 B × 40K = 13 MB; ters dizinler servis→pod'lar 3K × ~1 KB = 3,1 MB ve iş yükü→pod'lar 8K × ~0,4 KB = 3,2 MB → **≈ 20 MB, 51K anahtar** [T/V]. CH satırlarının birebir aynası olsaydı 115K × 325 B + 192K × 255 B ≈ **86 MB, 307K anahtar** [T/V].
- *R1b, cluster başına anlık görüntü blob'u:* pod kaydı ~235 B JSON × 40K = 9,4 MB + kalıcı 15K × ~120 B = 1,8 MB → **≈ 11 MB ham, gzip ile ~2–3 MB, 21 anahtar** (`coremetry:entity:v1:live:<cid>` × 20 + bir küresel) [T/V]. API replikası blob'u 30 sn'de bir çekip çözer (replika başına ~16–20 MB [V]); ters dizinleri çözerken kurar.

**R2 — Yalnız okuma önbelleği.** Resolver sonucu `fnv(tüm girdiler)` anahtarıyla 30 sn; ~5K sıcak sonuç × ~600 B ≈ 3 MB [V]. Depolamayı değiştirmez; A/B/C'nin hepsinin önüne konabilir (uçlarda `serveCached` bugün zaten bunu yapıyor).

| Boyut | R1a anahtar + TTL | **R1b blob** | R2 önbellek |
|---|---|---|---|
| Yazım | tick başına 51K `SET EX` ≈ 13 MB; arayüzde pipeline yok → tek tek 51K × ~0,3 ms ≈ 15 sn [T/V], toplu yazım eklenirse < 0,5 sn [V] | tick başına **21 `Set`**, 2–11 MB; mevcut arayüz | ıskada 1 `Set` |
| Sıcak okuma | `GET`/`MGET` ~0,2–0,5 ms [V] | bellek içi harita, < 1 µs; arka planda 30 sn'de bir `GET` | isabet ~0,3 ms; ıska = CH |
| CH MV'ye göre | servis→pod önek okuması 10–50 ms, pod yönü önek dışı 50–300 ms [V] yerine ms altı | aynı | yalnız tekrar eden sorguda; pod yönlü sorgular dağınık, ıska oranı yüksek |
| Pod'un düşmesi | TTL (≥ 3 tick olmalı) → ölü pod ≤ 3 dk görünür | blob değişimi: bir sonraki tick'te tam | TTL 30 sn |
| Flush/restart sonrası | sonraki tick tümünü yazar: ≤ ~70 sn [T]; arada MV yolu | aynı, 21 anahtar | kendiliğinden dolar |
| Çok replika | tek yazıcı; okurlar anlık aynı, tick ortasında yarı güncel | tek yazıcı; cluster başına atomik, okurlar ≤ 30 sn farkla aynı `gen` | replikalar ortak, 30 sn bayat |
| Redis kapalı | okuma MV yoluna düşer (500 ms beklemeden, degraded bayrağıyla) | aynı; replika son çözdüğü kopyayı blob TTL'i kadar kullanır | doğrudan CH |
| Çok liderli pencere | aynı girdi → aynı değerler, zararsız | aynı blob, zararsız | — |
| Repo kısıtı | `cache.Cache`'e toplu yazım gerekir; +51K anahtar her `ScanPrefix`/`DelPrefix` çağrısına +255 `SCAN` turu ekler [T] | kısıt yok; fleet özeti emsali | kısıt yok |

**Redis öneriyi değiştirir mi?** Depolama kararını değiştirmez, tamamlar. Redis pod'un *bugününü* hızlı verir ama geçmişi (30 gün) ve kalıcı kimliği taşıyamaz; CH'de pod satırı tutmanın gerekçesini de tümüyle ortadan kaldırır (plan §3.3'teki "canlılık ZSET'te, pod satırı CH'de" yedeği büyümeyi ve FINAL taramasını bırakırdı). En iyi cevap bileşimdir: **C (CH'de kalıcı referans + MV'de geçmiş) + R1b (Redis'te şimdi)**. R1a ancak replika belleği sorun olursa; R2 ayrıca gerekmez.

## 6. Karşılaştırma

| Boyut | A (S1) | B bir kez yaz | C (yalnız CH + MV) | **C + R1b** |
|---|---|---|---|---|
| CH yazımı | 51K–61K satır/dk, patlamalı [T] | ~1K satır/dk [T] | ~19 satır/dk [T] | **~19 satır/dk** + Redis 21 `Set`/tick |
| Bayt/gün (CH, sıkıştırmasız) | 17–21 GB [T/V] | ~0,33 GB | ~6,5 MB | **~6,5 MB** |
| Part/merge | ≥ 40 INSERT/tick → ~20 part/dk/tablo, partition'sız [T] | aynı sayıda küçük part | çoğu tick 0 INSERT | çoğu tick 0 INSERT |
| Depolama (30 gün / kararlı) | 21M / 126M satır; ~0,8 / ~5 GB/replika [T/V] | 126M (TTL DDL'i ile 21M) | < 100K satır, < 100 MB | aynı + Redis ~3 MB |
| Tick okuması | tüm tablo FINAL, 126M satır/dk [T] | aynı | 27K–90K satır | 27K–90K satır |
| pod→wl→svc→cluster (şimdi) | 2 FINAL sorgu, büyük tablo | A + MV | kimlik + bellek; servis için önek dışı MV 50–300 ms [V] | **bellek içi, CH yok** |
| servis→pod komşuları | `runs` kenarı ya da MV öneki | MV öneki | MV öneki 10–50 ms [V] + KSM | **bellek içi** |
| "T anında canlı mıydı" (eski trace/log) | `entity_id` ile FINAL, ~10 ms [V] | A + MV | MV, günlük partition, önek dışı ≤ 14M satır [T], 50–300 ms [V] | C ile aynı (Redis değil) |
| Pod churn | pod başına ≥ 14 satır + tazeleme | pod başına 14 satır | yazım yok | yazım yok |
| 1 ay sonra | pod satırı +150 gün yetim | aynı | MV partition'ı telemetriyle düşer | aynı |
| Yalnız-metrik pod | satır var (KSM) | satır var, canlılık kaynağı eksik | istek anında Thanos | tick'in KSM görüntüsü dizinde; geçmiş Thanos `at` |
| Lider devri / kesinti | boşluk > 10 dk → hepsi yeniden açılır | watermark ile çözülebilir | geçicide yazıcı yok; kalıcıda boşluk kuralı yok | aynı; Redis flush = 1 tick MV yolu |
| DDL / yeni ayar | yok / yok | TTL değişimi muhtemel / yok | yok / yok | **yok / yok** |

A'nın tek gerçek üstünlüğü: Thanos kesintisinde KSM kaynaklı pod listesinin son hâli CH'de durur. C + R1b'de bu, blob TTL'i kadar korunur; span basan pod'lar etkilenmez.

## 7. Öneri ve gerekçe

**C + R1b.** Operatörün iki gerçeğine doğrudan bağlanır:
1. *Telemetri 1 ay saklanıyor.* Pod satırının tek işi telemetriye pivot olmaktır. 30 günden yaşlı satır hiçbir veriye bağlanmaz; 30 günden genç satırın söylediği her şey zaten aynı 30 günlük MV'de ve Thanos'ta durur. Pod satırı gereksiz ikinci kopyadır ve yazımın ~%91'ini üretir. Türetilen pod, kaynağının TTL'iyle birlikte kendiliğinden düşer.
2. *Workload ve cluster adları uzun süre aynı.* Küme küçük (~27K satır) ve nadiren değişir: değişimde yazmak bedavaya yakın; kalıcı kimlik de problem/olay/topoloji referanslarının tam ihtiyacıdır ve Redis'e emanet edilemez.

Redis bu ikisinin arasındaki boşluğu kapatır: "şu an" sorusu en sık ve en hız duyarlı olanıdır, kaybı ise bir tick'tir.

## 8. Diğer kayıtlardan entity referansı

- **Özne/kimlik = kalıcı entity id'si** (`svc:`, `wl:`, `ns:`, `node:`, `cluster:`, S5'te `db:`/`queue:`/`ext:`). Problem parmak izine ve dedup anahtarına pod adı girmez; girerse her rollout aynı problemi böler.
- **Pod = öznitelik + zaman sınırı:** kayıt `(cluster değeri ya da cid, namespace, pod adı)` ve kendi penceresini (`started_at … last_seen`) taşır; pivot telemetriyi bu ad ve pencereyle süzer, pod satırının varlığına bağlı değildir. `problems.pod` ve `ml_events.pod` bugün zaten bu şekilde [Ö]; yeni kolon gerekmez.
- **Saklama sonrası:** pod adı metin olarak kalır, bağlantı iş yüküne düşer (`DerivedWorkload` → `wl:` → `serves`).
- **Topoloji ve CoSRE komşuları:** çizge yalnız kalıcı id'lerden kurulur; pod yaprakları istenince, `at` ile açılır.

## 9. Prod ölçüm sorguları (yalnız metadata/MV; zaman sınırlı, LIMIT, `max_execution_time`)

```sql
-- Q1 tablo/MV boyutu, part sayısı, satır başına bayt (her host)
SELECT hostName() h, table, sum(rows) r, count() parts, formatReadableSize(sum(data_compressed_bytes)) comp,
       round(sum(data_uncompressed_bytes) / sum(rows)) b_per_row
FROM clusterAllReplicas('<cluster>', system.parts)
WHERE active AND database = currentDatabase() AND (table IN ('entities','entity_relations') OR table LIKE '%entity_seen_%')
GROUP BY h, table ORDER BY table, h LIMIT 100 SETTINGS max_execution_time = 10;
-- Q2 depoda tip başına kimlik ve satır (FINAL yok; rows/ids = birleşmemiş sürüm çarpanı)
SELECT entity_type, uniq(entity_id) ids, uniqIf(entity_id, valid_to = toDateTime(0)) open_ids, count() rows
FROM entities WHERE last_seen >= now() - INTERVAL 30 DAY GROUP BY entity_type ORDER BY rows DESC LIMIT 20 SETTINGS max_execution_time = 20;
-- Q3 gün başına ve 30 gün toplam (TOTALS) tekil sayılar: churn ve kalıcı/geçici oranı
SELECT toDate(time_bucket) d, uniq(cluster, k8s_namespace, k8s_pod) pods, uniq(service_name) services,
       uniq(cluster, k8s_namespace, replaceRegexpOne(k8s_pod, '-[a-z0-9]{6,10}-[a-z0-9]{5}$', '')) approx_workloads,
       uniq(cluster, k8s_namespace) namespaces, uniq(cluster) clusters, count() mv_rows
FROM entity_seen_5m WHERE time_bucket >= now() - INTERVAL 30 DAY AND time_bucket <= now()
GROUP BY d WITH TOTALS ORDER BY d LIMIT 40 SETTINGS max_execution_time = 60;
-- Q4 syncer'ın gerçek yazımı ve kapanışı, saatlik
SELECT toStartOfHour(started_at) h, sum(entities_written) e, sum(relations_written) r, sum(closed) closed,
       countIf(status = 'failed') failed, max(ch_ms) max_ch_ms
FROM entity_sync_runs WHERE started_at >= now() - INTERVAL 1 DAY GROUP BY h ORDER BY h LIMIT 30 SETTINGS max_execution_time = 10;
-- Q5 tick'in açık-ömür okuması gerçekte kaç satır tarıyor
SELECT count() q, max(read_rows) max_rows, sum(read_rows) sum_rows, quantile(0.95)(query_duration_ms) p95_ms
FROM clusterAllReplicas('<cluster>', system.query_log)
WHERE event_date >= today() - 1 AND event_time >= now() - INTERVAL 1 HOUR AND type = 'QueryFinish'
  AND query LIKE '%FINAL%' AND query LIKE '%valid_to = toDateTime(0)%' AND query LIKE '%LIMIT 500000%'
LIMIT 1 SETTINGS max_execution_time = 20;
-- Q6 önek dışı geçmiş okuma: "T anında canlı mıydı" (15 gün önce, ±10 dk); süreye ve read_rows'a bak
SELECT k8s_pod, minMerge(first_seen_state) f, maxMerge(last_seen_state) l FROM entity_seen_5m
WHERE time_bucket >= now() - INTERVAL 15 DAY - INTERVAL 10 MINUTE AND time_bucket <= now() - INTERVAL 15 DAY + INTERVAL 10 MINUTE
  AND cluster = '<cluster-değeri>' AND k8s_pod = '<pod-adı>' GROUP BY k8s_pod LIMIT 10 SETTINGS max_execution_time = 10;
```

Q3 churn varsayımını, Q1 satır boyunu, Q4 51K–61K türetimini, Q5 "tick tüm tabloyu okur" türetimini, Q6 geçmiş okuma gecikmesini ölçüme çevirir. Redis tarafı: `redis-cli --latency` (gidiş-dönüş) ve `INFO memory`, `DBSIZE` (bugünkü anahtar sayısı).

## 10. S1'de (v0.10.2214) ve S2–S6'da ne değişir

**S1 üstüne tek dilim (şema yok):**
- `syncCluster`: pod/container entity'leri ve pod uçlu kenarlar diff'e ve `Apply`'a girmez; tick sonunda cluster'ın canlı görüntüsü Redis'e yazılır (TTL 3 tick, sabit). Span geçişi penceresi dizin için 1 saate çıkar; `container_info` sorgusu düşer (6 → 5).
- `DiffLifetimes`: `PodGap` boşluk kuralı kalkar; kapanış, ardışık başarılı görüntülerde ≥ `PodGap` yokluk ister.
- `RefreshEvery`: `PodGap/2` → 24 saat (aynı `ThrottleRefresh`, yeni sabit).
- Taşıma kuralı (invariant 4): `EntityOpenLifetimes` kalıcı katmanda tam satır döndürür; tazeleme ve kapanış alanları bellekteki `prevEnts` yerine CH'den taşır (bugün restart sonrası kapanış yalnız kimlikten türetiyor, `syncer.go:506-509`).
- Resolver: önce canlı dizin, yoksa bugünkü MV + KSM yolu. Depodan pod okuyanlar resolver'a geçer: `internal/api/entities.go` pod/container kolları, `internal/mcptools/entity_catalog.go`, `internal/mcptools/resolve_entity.go:287`. `backfillDead` / `ClosedRowsForDead` / `Existing` silinir (geçmiş zaten MV'de).

| Dilim | Değişiklik |
|---|---|
| S2 | Kapsam aynı, önemi artar: `serves` (wl→svc) geçici pod ile kalıcı servis arasındaki tek kalıcı köprü. "Sahiplik S2'de depodan" notu düşer; canlı dizinden gelir |
| S3 | `/api/logs?entity=pod:…` kimliği ayrıştırır; ad + namespace + cluster değeri + **zorunlu zaman penceresi** ile süzer, depo okuması yok. Span çipleri span'in kendi kolonlarından + dizinden |
| S4 | `Neighbours(svc→pod)` ve `list_pods` canlı dizinden; `at` geçmişteyse MV öneki |
| S5 | host, db, queue, ext ve `calls` kalıcı katmana (aç/kapat + günlük tazeleme); `ext:` kardinalite tavanı aynen |
| S6 | K6'ya pod önekli ömür MV'si seçeneği eklenir; VM'den gelen metrik `runs` kalıcı kenar değil, dizin girdisi olur |
| Plan §3.3 | Redis ZSET yedeği ve "pod yönlü soru `entity_relations.runs` ile cevaplanır" cümlesi bu notla değişir |

## 11. Göç ve geri alma

- **Göç:** yalnız sürüm. İlk tick mevcut açık pod/container ömürlerini ve pod uçlu kenarları kapatır: tek seferlik ~280K kapanış satırı [T]; bu satırlar artık okunmaz, 180 günlük TTL ile düşer.
- **Doğrulama:** Q4'te yazım birkaç yüz satır/saate, Q5'te `max_rows` ~100K altına iner; günde bir ~27K satırlık tazeleme görülür.
- **Geri alma:** v0.10.2214'e dön. Syncer sonraki tick'te pod ömürlerini yeniden açar; aradaki dönemin gerçeği MV'de durur. Redis anahtarları TTL ile kendiliğinden silinir. Şema iki yönde de aynı.

## 12. Açık kararlar (operatör)

| # | Karar | Öneri |
|---|---|---|
| E1 | Pod, container ve pod uçlu kenarlar CH'ye satır olarak yazılmasın mı (C) | **Evet.** Önce Q3–Q5; churn varsayımı 10 kat yanlış olsa da sıralama değişmez (yalnız A ile B arası daralır) |
| E2 | Redis canlı dizini: cluster başına blob (R1b) mi, entity başına anahtar + TTL (R1a) mı | **R1b.** Mevcut `cache.Cache` ile çalışır, anahtar uzayını şişirmez, cluster başına atomiktir. R1a yalnız replika belleği (~20 MB) sorun olursa ve arayüze toplu yazım eklenerek |
| E3 | Mevcut pod satırları TTL'e mi bırakılsın, tek seferlik silinsin mi | **TTL'e bırak.** Q1 birkaç GB/replika üstü gösterirse yoğun olmayan saatte operatör eliyle tek seferlik `ALTER TABLE … DELETE WHERE entity_type IN ('pod','container')` ve kenar karşılığı |
| E4 | Geçmiş okuma: yalnız-metrik pod Thanos KSM saklamasına bağlı kalsın mı; pod önekli ömür MV'si K6'ya girsin mi | **Evet / Q6'ya göre.** KSM serilerinin ≥ 30 gün tutulduğu doğrulanmalı. Q6 soğuk p95 > 200 ms ise MV S6'da eklenir, değilse eklenmez |

**Onay (operatör, 2026-11-15):** öneri **C + R1b** ve E1–E4 öneriler gibi onaylandı — E1 evet (pod,
container ve pod uçlu kenarlar CH satırı değil), E2 R1b (cluster başına tek blob), E3 TTL'e bırak (bu
dilimde DELETE yok), E4 evet (yalnız-metrik pod geçmişi Thanos saklamasına bağlı; kabul edildi).

**Uygulama notları (v0.10.2225):** §10 "S1 üstüne tek dilim" aynen; şema yok, yeni ayar yok. Sapmalar:
(1) §11'deki "ilk tick mevcut açık pod ömürlerini kapatır" yapılmadı — tick okuması yalnız kalıcı tipleri
tarar (pod aralığına hiç dokunmaz, ~280K tek seferlik yazım da yok); S1'den kalan açık pod/container
satırları E3 gereği TTL'e kalır, tipsiz liste (`entityListSQL`) ve çocuk sayımı onları okumaz. (2) Blob
yalnız pod kayıtlarını taşır (iş yükü id'si, node, span payına göre servisler, cluster değeri); iş
yükünden yukarısı kalıcı CH satırlarından, ayrı "küresel" anahtar yok (cid'ler Remote Cluster
kayıtlarından). (3) Kilit degraded iken (Redis tanımlı ama kopuk → Noop kilit) tick hiç koşmaz
(`skipped_no_lock`). (4) Kapanışta `valid_to` = yokluktan önce görüldüğü bilinen en geç an (bu süreçteki
son başarılı tick, yoksa CH'deki `last_seen`).
