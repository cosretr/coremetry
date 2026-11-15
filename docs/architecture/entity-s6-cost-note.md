# Entity S6 maliyet notu — "şema değişikliği gerçekten gerekli mi"

**Tarih:** 2026-11-15 · **Durum:** ONAYLANDI — seçenek C (operatör, 2026-11-15); uygulama v0.10.2260+ (plan, "S6 notları") · **Taban:** v0.10.2250
**İlgili:** `entity-autodiscovery-plan.md` (§5 S6 satırı, K5, K6, "S2–S6 ayarı", "S5 notları"), `entity-storage-cost-note.md` (C + R1b, E4/Q6), `.claude/skills/clickhouse-schema` (§5, §8, §9).
**Rakam etiketleri:** **[Ö]** koddan/şemadan okundu ya da operatör verdi · **[T]** türetildi (aritmetik yanında) · **[V]** varsayım, §6 sorgusuyla ölçülecek · **[?]** bilinmiyor. **Bu notta prod'da ölçülmüş tek rakam yoktur.** Span/gün bilinmiyor; maliyetler formüldür.

## 1. Karar özeti

Öneri **C: S6'da şema değişikliği yok.** Üç bulgu bunu taşıyor, üçü de koddan [Ö]:

1. **Host satırları bugünkü MV'de zaten var.** `k8s_pod` terfi kolonu `k8s.pod.name` yoksa `host_name`'e düşer (`promoted_attr.go:101`, `migrations/0011:53-55`). `entity_seen_5m` yalnız `k8s_pod != ''` süzer. Yani K8s dışı host, namespace'i boş bir satır olarak yazılıyor. Tick bu satırları her dakika okuyor ve Go'da atıyor (`spanpass.go:86`). "S5 notları"ndaki "host şema ister" gerekçesi yalnız metrik basan host için doğru; span basan host için değil.
2. **Açık iş yükü adı da zaten toplanmış.** `workload_revision_activity_1m` (0012) satırı `(cluster, namespace, workload, tür, revizyon, servis)` taşır, kaynağı `k8s.deployment.name` ve kardeşleridir (`rollout_schema.go:114-142`). v2 MV'nin "iş yükü kolonları" bu bilginin ikinci kopyası olurdu.
3. **`entity_seen_1m` okunmuyor.** Depoda bu tabloya `SELECT` atan kod yok (yalnız DDL, kayıt listeleri, purge, admin kartı). INSERT yolunda 5m ikiziyle aynı bedeli ödüyor, 5m'in beş katı satır üretiyor.

Sonuç: S6'nın kullanıcıya görünen dört kazancından üçü şemasız gelir. Dördüncüsü (KSM'siz pod → iş yükü bağının regex'siz olması) kısmen gelir; tamamı için şema gerekir ve bugün buna değmez (§5, §7).

## 2. S6'nın bağımsız parçaları

| # | Parça | Kullanıcı ne kazanır | Şema? | Tek başına gider mi |
|---|---|---|---|---|
| P-a | v2 MV, iş yükü kolonları | KSM'nin görmediği pod'da iş yükü, pod adı regex'i yerine açık attr'dan | **Evet.** Seçenek A: `0016` = 1 MV (`_local`) + 1 Distributed sarmalayıcı, `ON CLUSTER`; önkoşul 0012 kolonları her shard'da. Seçenek A2: iç tabloya `ADD COLUMN` × 2 + sarmalayıcıya `ADD COLUMN` × 2 + `MODIFY QUERY` | Evet |
| P-b | Host entity + host→servis `runs` | K8s dışı sunucu entity olur; "bu host'ta hangi servis", servisten host'a yol | **Hayır** (bulgu 1). Yeni tip/rel değeri, DDL yok | Evet |
| P-c | Yalnız metrik basan servis + metrikten `runs` | Span basmayan servis entity olur; pod → servis metrikten de dolar (S0'da silinen kol) | **Hayır.** VM'e tick dışı kadansta anlık sorgu | Evet |
| P-d | Log kapsama işareti | Entity sayfasında "bu varlığın logu var / yok" | **Hayır.** ES'e istek anında tek sorgu (K5: tick'te değil) | Evet |

Parçalar birbirine bağlı değil. Şema isteyen tek parça P-a'dır.

## 3. Ölçek aritmetiği

| Kalem | Değer | Kaynak |
|---|---|---|
| Canlı pod / cluster / servis | 40K / ~20 / birkaç bin | [Ö] operatör |
| CH yerleşimi | 2 shard × 2 replika; MV her shard'da `_local`, okuma Distributed sarmalayıcıdan | [Ö] `0011`, skill |
| 5 dk'da aktif anahtar `P` = (servis, cluster, ns, pod) | 40K × 1,2 = **48K** | [V] |
| `K` = bir anahtarın düştüğü shard sayısı | **1** (span shard anahtarı servis adı hash'i; uygulama varsayılanı, `cluster.go:340`) ya da **2** (`rand()` / trace hash; `docs/clickhouse-cluster.md` örneği) | [?] §6 P4 |
| `entity_seen_5m` satır/gün | `P × 288 × K` = 48K × 288 = **13,8M × K** | [T] |
| `entity_seen_5m` 30 gün | 13,8M × 30 = **415M × K** | [T] |
| `entity_seen_1m` satır/gün, 3 gün | `P × 1440 × K` = **69M × K**; 3 günde 207M × K | [T] |
| Satır boyu (sıkıştırılmış) `b` | 20–30 B (5 anahtar + node + 3 sayaç + 2 zaman state'i; tDigest yok) | [V] §6 P1 |
| 5m disk | 13,8M × 25 B ≈ **0,35 GB/gün/kopya**; 30 gün ≈ 10 GB/kopya; 2 replika ≈ **21 GB × K** | [T/V] |
| Span'i tetikleyen MV sayısı `M` | ≈ 25 (uygulama DDL'lerinde `FROM spans` okuyan ~23 MV + operatör rollup'larından 2; prod'da hangileri kurulu [?]) | [Ö sayım] §6 P4 |

**Satır sayısı span hacmine bağlı değil.** Her pod her 5 dk'da en az bir span basıyorsa satır `P × 288 × K`'da doyar. Span hacmi `N` şunları belirler: INSERT blok sayısı, MV'nin okuduğu satır (= N), birleşmemiş ara satır. Formüller:

- **Blok başına:** her MV = bloğun üstünden 1 `GROUP BY` geçişi + hedefinde 1 yeni part (dokunulan gün partition'ı başına) + Replicated olduğu için Keeper'a 1 kayıt. MV'ler varsayılan ayarda sırayla koşar; INSERT süresi ≈ Σ MV süresi [T, CH davranışı; prod ayarı [?]].
- **Bir MV daha eklemek:** part ve Keeper yazımında `+1/(1+M)` ≈ **+%4** [T]; CPU payı P5'ten okunur (`entity_seen_5m_local` satırı v2'nin birebir vekilidir).
- **Milyon span başına:** MV 1M satır × 8 düz kolon okur (dizi `indexOf` yok; terfi kolonları INSERT'te bir kez hesaplanıyor [Ö]). Yazdığı ara satır ≤ blok başına tekil anahtar ≤ blok satırı. Süre: `P5.toplam_ms ÷ (P5.okunan_satir ÷ 1M)`.
- **Gün başına:** `N ÷ 1M ×` yukarıdaki süre. Örnek [V]: N = 1 milyar/gün, MV milyon span başına 50 ms ise günde 50 sn CPU; 500 ms ise 8 dk. Hangisi olduğunu yalnız P5 söyler.

## 4. Şema seçenekleri

**A — v2 MV, v1'in yanında 30 gün, sonra v1 düşer (planın varsayılanı).** v2 = v1'in aynı anahtarı + iki kolon (`workload`, `workload_kind`, ikisi de `anyLastSimpleState`; anahtara girmezler, satır sayısı değişmez). Yalnız 5m kademesi; 1m'in v2'si yapılmaz (bulgu 3).

**A2 — v1'e yerinde kolon ekleme.** İç tabloya ve sarmalayıcıya `ADD COLUMN`, sonra `MODIFY QUERY`. Çift koşu yok, geçmiş kaybı yok; eski kovalarda yeni kolon boştur. Emsal: `migrations/0007` ve `mv_dim_migrations.go` "kolon varsa dokunma" sözleşmesi [Ö].

**B — küçük, yalnız host MV'si; v1'e dokunulmaz.** `ORDER BY (host_name, service_name, time_bucket)`, `WHERE host_name != '' AND k8s_namespace = ''`, 5 dk, 30 gün. 1 MV + 1 sarmalayıcı.

**C — şema yok.** Host: v1'in namespace'siz satırları (zaten okunuyor). Metrik basan host/servis: VM'e 15 dk'da bir anlık sorgu. Açık iş yükü adı: `workload_revision_activity_1m`.

| Boyut | A (v2 yanında) | A2 (yerinde) | B (host MV) | **C (şema yok)** |
|---|---|---|---|---|
| INSERT yolu | +1 MV geçişi ve +1 part/blok, ≥ 30 gün; v1 düşünce net ≈ 0 | +0 MV; mevcut geçişe 2 kolon | +1 MV geçişi kalıcı; süzgeç tüm satırda koşar, az satır toplar; eşleşen her blokta +1 part | **+0** |
| Satır/gün | +13,8M × K (çift koşuda) | +0 | `H × s × 288 × K`; H = 1.000, s = 1,5 ise 0,43M × K [V] | +0 |
| Bayt/gün | +0,35–0,38 GB/kopya [T/V]; gerçeği = P1'deki v1 × ~1,1 | + iki LC kolonu, < %10 [V] | 0,43M × 25 B ≈ 11 MB/kopya [V] | +0 |
| Tick okuması | Aynı sorgu, başka tablo: gün partition'ında `time_bucket` süzgeci önek dışı → gün sonunda ≤ 13,8M × K satırlık tek kolon taraması + son 1 saatin ~0,6M × K satırı; dönen ≈ 48K [T] | Aynı + 2 kolon | Ek okuma: ≤ H × s × 12 satır, önekten | **Ek okuma yok** (host satırları zaten dönüyor). İş yükü için 15 dk'da bir `workload_revision_activity_1m` son 1 saat: ~8K iş yükü × 1,2 × 60 ≈ 0,6M satır [T/V] |
| Distributed | `_local` her shard'da, znode `/clickhouse/tables/{shard}/…`; INSERT'i alan replikada tetiklenir, part diğerine kopyalanır. `ON CLUSTER` operatörde, boot'ta asla [Ö §8]. `cluster.go`'daki üç kayıt + purge + admin nesne listesi gün-bir şart; yoksa okuma tek shard'ı görür [Ö] | `ALTER` iç tabloya ve sarmalayıcıya ayrı ayrı; iç tablo adı `.inner_id.<uuid>` olabilir. Küme kipinde DDL ertelenir: "yeni kolon iki boot ister" [Ö §3] | A ile aynı | Yeni nesne yok. Okuma bugünkü sarmalayıcıdan |
| Replika gecikmesi | Gecikmeli replika son kovaları eksik verir; tick penceresi 1 saat, etkisi yok. Parite denetiminde yanlış fark üretir (§5) | Aynı | Aynı | Bugünkünden farksız |
| Yanlışsa ne kırılır | Bir shard'da kolon eksikken MV kurulursa **o shard'da her span INSERT'i düşer** (0005 / v0.8.185 sınıfı) [Ö]. Yüksek blok hızında part baskısı. Çift koşuda disk 2× | `MODIFY QUERY` kolon eksik shard'da aynı sınıf hata. İç tablo ile sorgu uyuşmazsa INSERT düşer | A ile aynı sınıf; ayrıca host/pod ayrımı DDL süzgecine gömülür, düzeltmesi yeni DDL | Yanlış sınıflama → sahte host entity'si. **Ingest hiçbir durumda etkilenmez.** Düzeltme = sürüm |

**C'nin dürüst sınırları.**
- v1, `k8s_pod` değerinin `k8s.pod.name`'den mi `host_name`'den mi geldiğini taşımaz. Namespace attr'ı basmayan K8s pod'u da namespace'siz satır olur. Ayrım tick'te yapılır: ad, aynı cluster'ın canlı dizininde (KSM) pod olarak varsa pod'dur ve namespace'i oradan gelir; yoksa host adayıdır. KSM'siz cluster'da attr'sız pod host gibi görünür. Bu kitle P2'de sayılır.
- Hiç span basmayan host v1'de yoktur; VM kolu (P-c) kapatır.
- KSM'siz pod → iş yükü bağı ad türevi kalır. Değişen: iş yükü entity'si ve `serves` kenarı açık attr'dan gelir, türetilmiş ad attr'la doğrulanır. Yeterli olup olmadığını P7 söyler.
- Geçmişte "host X'te hangi servis vardı" sorusu v1'i önek dışı okur (cost note Q6 ile aynı sınıf, 50–300 ms [V]).

## 5. Çift koşu dönemi (yalnız A)

- **INSERT:** 30 gün boyunca `+1` MV geçişi ve blok başına `+1` part, her shard'da. v1 düşünce bugünkü sayıya döner.
- **Disk:** v2 doğrusal dolar. `d`. günde ek disk = `d × G`, `G` = v1'in günlük baytı × ~1,1. Tepe 30. günde ≈ v1'in bugünkü boyutu × 1,1. Örnek [V]: v1 = 10 GB/kopya ise tepe +11 GB/kopya, iki replikada +22 GB × K.
- **Part:** +30 günlük partition, replika başına.
- **Okuyucular:** kod v2'yi yalnız istenen pencereyi kapsıyorsa okur, kapsamıyorsa v1 (`sourceCovers` emsali). v1 düşünce bu dal silinir.

**v1'i düşürmek ne zaman güvenli** (dördü birlikte):
1. v2'nin en eski kovası ≥ 30 gün önce. Geçmiş `at` okumaları 30 güne kadar gidiyor; daha erken düşürmek o okumaları boş döndürür.
2. Son 3 tam günde parite: aşağıdaki sorguda `span` ve `pod` iki MV'de eşit.
3. `system.query_log`'da son 24 saatte v1'e `SELECT` yok.
4. Her host'ta v2 nesneleri var ve `system.replication_queue` boş.

```sql
SELECT 'v1' AS mv, toDate(time_bucket) AS d, countMerge(span_count_state) AS span, uniq(cluster, k8s_namespace, k8s_pod) AS pod
FROM entity_seen_5m WHERE time_bucket >= toStartOfDay(now()) - INTERVAL 3 DAY AND time_bucket < toStartOfDay(now()) GROUP BY d
UNION ALL
SELECT 'v2', toDate(time_bucket), countMerge(span_count_state), uniq(cluster, k8s_namespace, k8s_pod)
FROM entity_seen_v2_5m WHERE time_bucket >= toStartOfDay(now()) - INTERVAL 3 DAY AND time_bucket < toStartOfDay(now()) GROUP BY toDate(time_bucket)
ORDER BY d, mv LIMIT 20 SETTINGS max_execution_time = 60;
```

A2, B ve C'de çift koşu yoktur.

## 6. Preflight sorguları (karardan ÖNCE, prod'da, salt okunur)

Hepsi metadata, sistem günlüğü ya da MV okur; ham `spans` taraması yok. Sistem günlüğü tablosu yoksa o sorgu atlanır.

```sql
-- P1 entity MV'lerinin diski, satırı, part'ı (her host). İç tablo adı `.inner_id.<uuid>` olabilir; ikisi de yakalanır.
--    gun ≈ 30 → 5m, ≈ 3 → 1m, ≈ 7 → workload_revision_activity_1m.
SELECT hostName() AS h, table, sum(rows) AS satir, count() AS part, uniqExact(partition) AS gun,
       formatReadableSize(sum(data_compressed_bytes)) AS disk,
       round(sum(data_compressed_bytes) / sum(rows), 1) AS b_satir, round(sum(rows) / uniqExact(partition)) AS satir_gun
FROM clusterAllReplicas('<cluster_adı>', system.parts)
WHERE active AND database = currentDatabase()
  AND (table LIKE '%entity_seen_%' OR table LIKE '%workload_revision_activity%'
       OR table IN (SELECT concat('.inner_id.', toString(uuid)) FROM system.tables
                    WHERE database = currentDatabase() AND engine = 'MaterializedView'
                      AND (name LIKE 'entity_seen_%' OR name LIKE 'workload_revision_activity%')))
GROUP BY h, table ORDER BY table, h LIMIT 100 SETTINGS max_execution_time = 10;

-- P2 MV'nin günlük içeriği: fiziksel/mantıksal satır (oran ≈ K × birleşmemiş çarpan), host adayı, node.
--    Ağır gelirse 7 günü 2'ye indir.
SELECT toDate(time_bucket) AS d, count() AS fiziksel_satir,
       uniq(service_name, cluster, k8s_namespace, k8s_pod, time_bucket) AS mantiksal_satir,
       uniq(cluster, k8s_namespace, k8s_pod) AS pod_ya_da_host,
       uniqIf(k8s_pod, k8s_namespace = '') AS namespacesiz_ad,
       uniqIf(k8s_node, k8s_node != '') AS node, uniq(service_name) AS servis,
       countMerge(span_count_state) AS pod_etiketli_span
FROM entity_seen_5m
WHERE time_bucket >= toStartOfDay(now()) - INTERVAL 7 DAY AND time_bucket <= now()
GROUP BY d WITH TOTALS ORDER BY d LIMIT 10 SETTINGS max_execution_time = 60;

-- P3 span/gün (yalnız metadata; shard başına tek replika sayılır)
SELECT partition, sum(rows) AS span, count() AS part, formatReadableSize(sum(data_compressed_bytes)) AS disk
FROM cluster('<cluster_adı>', system.parts)
WHERE active AND database = currentDatabase() AND table = 'spans_local'
GROUP BY partition ORDER BY partition DESC LIMIT 10 SETTINGS max_execution_time = 10;

-- P4 span'e bağlı MV sayısı (M), span shard anahtarı (K), terfi kolonları her host'ta var mı
SELECT hostName() AS h, name, length(dependencies_table) AS mv_sayisi, engine_full
FROM clusterAllReplicas('<cluster_adı>', system.tables)
WHERE database = currentDatabase() AND name IN ('spans', 'spans_local') ORDER BY h, name LIMIT 20 SETTINGS max_execution_time = 10;
SELECT hostName() AS h, arraySort(groupArray(name)) AS kolonlar
FROM clusterAllReplicas('<cluster_adı>', system.columns)
WHERE database = currentDatabase() AND table = 'spans_local'
  AND name IN ('cluster','k8s_namespace','k8s_pod','k8s_node','k8s_deployment','k8s_statefulset','k8s_daemonset','k8s_replicaset')
GROUP BY h ORDER BY h LIMIT 20 SETTINGS max_execution_time = 10;

-- P5 MV başına INSERT bedeli, son 1 saat (entity_seen_5m_local satırı = v2'nin vekili)
SELECT view_name, count() AS blok, sum(read_rows) AS okunan_satir, sum(written_rows) AS yazilan_satir,
       round(avg(view_duration_ms), 1) AS ort_ms, quantile(0.95)(view_duration_ms) AS p95_ms, sum(view_duration_ms) AS toplam_ms
FROM clusterAllReplicas('<cluster_adı>', system.query_views_log)
WHERE event_date >= today() - 1 AND event_time >= now() - INTERVAL 1 HOUR AND status = 'QueryFinish'
GROUP BY view_name ORDER BY toplam_ms DESC LIMIT 40 SETTINGS max_execution_time = 20;

-- P6 span INSERT blok hızı ve gecikmesi, 5 dk'lık dilimler (blok hızı ≈ MV başına yeni part hızı)
SELECT toStartOfFiveMinute(event_time) AS t, count() AS blok, sum(written_rows) AS satir,
       quantile(0.5)(query_duration_ms) AS p50_ms, quantile(0.95)(query_duration_ms) AS p95_ms
FROM clusterAllReplicas('<cluster_adı>', system.query_log)
WHERE event_date >= today() - 1 AND event_time >= now() - INTERVAL 1 HOUR AND type = 'QueryFinish'
  AND query_kind IN ('Insert', 'AsyncInsertFlush') AND has(tables, concat(currentDatabase(), '.spans_local'))
GROUP BY t ORDER BY t LIMIT 20 SETTINGS max_execution_time = 20;

-- P7 ad türevi iş yükü kaç tane (P-a'nın gerçek kitlesi): span kaynaklı = KSM'nin görmediği
SELECT cluster_id, count() AS acik_is_yuku, countIf(source = 'span') AS span_kaynakli
FROM entities FINAL WHERE entity_type = 'workload' AND valid_to = toDateTime(0)
GROUP BY cluster_id ORDER BY span_kaynakli DESC LIMIT 100 SETTINGS max_execution_time = 20;

-- P8 tick'in bugünkü okuması (max_donen = 200000 ise LIMIT kesiyor) ve entity_seen_1m'i okuyan var mı
SELECT count() AS sorgu, max(read_rows) AS max_okunan, max(result_rows) AS max_donen, quantile(0.95)(query_duration_ms) AS p95_ms
FROM clusterAllReplicas('<cluster_adı>', system.query_log)
WHERE event_date >= today() - 1 AND event_time >= now() - INTERVAL 1 HOUR AND type = 'QueryFinish' AND is_initial_query
  AND query LIKE '%FROM entity_seen_5m%' AND query LIKE '%LIMIT 200000%' AND query NOT LIKE '%system.query_log%'
LIMIT 1 SETTINGS max_execution_time = 20;
SELECT count() AS okuma, max(event_time) AS son
FROM clusterAllReplicas('<cluster_adı>', system.query_log)
WHERE event_date >= today() - 7 AND type = 'QueryFinish' AND query_kind = 'Select'
  AND (has(tables, concat(currentDatabase(), '.entity_seen_1m')) OR has(tables, concat(currentDatabase(), '.entity_seen_1m_local')))
LIMIT 1 SETTINGS max_execution_time = 30;
```

**Sonuç nasıl okunur.**
- P2 `namespacesiz_ad` günden güne aynı, TOTALS günlük sayıya yakın → gerçek host; C doğrudan çalışır. TOTALS günlük sayının katları → churn var, bunlar attr'sız pod; C onları kalıcı katmana yazmaz, karar operatöre döner.
- P2 `fiziksel ÷ mantiksal` ≈ 1 → K = 1. ≈ 2 ve üstü → §3'teki her disk rakamı o kadar katlanır.
- P7 `span_kaynakli` toplamı açık iş yüklerinin küçük bir payıysa (örnek eşik: < %5) P-a şemaya değmez.
- P5'te `entity_seen_5m_local` toplam MV süresinin küçük payıysa A'nın INSERT bedeli de küçüktür; yine de C bedelsizdir.

## 7. Geri alma

| Seçenek | Adımlar | Kaybedilen veri |
|---|---|---|
| A, çift koşu sırasında | 1) Sürümü geri al (okuyucular v1'e döner). 2) `DROP TABLE entity_seen_v2_5m ON CLUSTER … SYNC`. 3) `DROP TABLE entity_seen_v2_5m_local ON CLUSTER … SYNC` (`SYNC` şart: yoksa znode kalır, yeniden kurulum 253 verir [Ö 0011 rollback]). 4) Her host'ta nesnenin gittiğini doğrula | Yalnız v2'nin biriktirdiği kovalar. v1 eksiksiz |
| A, v1 düştükten sonra | 1) v1'i 0011 ADIM 6 ile yeniden kur. 2) İstenirse shard başına `INSERT INTO entity_seen_5m_local SELECT <v1 kolonları> FROM entity_seen_v2_5m_local` (state tipleri aynı). 3) Sürümü geri al. 4) v2'yi yukarıdaki sırayla düşür | 2. adım yapılmazsa 30 güne kadar geçmiş `at` okuması boş. Yapılırsa kayıp yok |
| A2 | 1) `MODIFY QUERY` ile eski `SELECT`'e dön. 2) Sürümü geri al. Eklenen kolonlar boş kalır; istenirse sonra `DROP COLUMN` | Yok (yalnız yeni iki kolonun içeriği) |
| B | 1) Sürümü geri al. 2) Sarmalayıcıyı, sonra `_local` MV'yi `SYNC` ile düşür | Host MV'sinin geçmişi |
| **C** | Sürümü geri al | CH'de kayıp yok. Yazılmış `host` satırları `entities`'te kalır, 180 günlük TTL ile düşer; eski sürümün tick'i o tipi okumaz. Eski sürümün tipsiz listesinde görünüp görünmeyeceği doğrulanmadı [?]. Redis girdileri TTL ile silinir |

Her DDL geri alımında sıra: önce okuyucu (sürüm), sonra sarmalayıcı, en son MV. MV kaynak INSERT'in tetikleyicisidir; yarım kalmış bir MV ingest'i keser.

## 8. Öneri

**C.** Gerekçe: şemanın getireceği iki bilginin ikisi de (host satırı, açık iş yükü adı) bugün zaten INSERT anında toplanıyor ve bedeli ödeniyor. Aynı bilgiyi ikinci kez toplamak için INSERT yoluna MV eklemek, S1'deki hatanın aynısı olur: maliyet kesin, kazanç ölçülmemiş.

Sıra (her biri tek sürüm, her birinden sonra dur):
1. **P-b host (C).** Tick'in attığı namespace'siz satırlar host adayı olur. Host kalıcı katmandadır (S5 ayarı): değişimde + günde bir yazım. H = 1.000, s = 1,5 ise 1.000 entity + 1.500 kenar = günde ~2.500 satır [V]; kalıcı katmanın bugünkü ~27K–90K satır/gün'ünün [T/V, cost note §4 C] yanında küçük. `ext:` emsali sabit bir tavan (5.000) şart. Önkoşul: P2.
2. **P-c metrik.** Servis adları: VM etiket değeri çağrısı (`vmetrics.LabelValues` mevcut), 15 dk'da bir, yanıt ≈ servis sayısı × ~30 B ≈ 100 KB [T/V]. Metrik `runs`: seçilmiş tek metrik üstünde `count by (servis, pod ya da host)`, 15 dk'da bir, yanıt ≈ 48K seri × ~200 B ≈ 10 MB [T/V]; sonuç canlı dizine girer, CH'ye satır yazılmaz. Bugün tick Thanos'a dakikada 5 × 20 = 100 anlık sorgu atıyor [Ö/T]; OTLP metrik backend'ine hiç sorgu atmıyor [Ö]. Günde +192 sorgu eklenir.
3. **Açık iş yükü adı.** `workload_revision_activity_1m`'den iş yükü ve `serves` (kaynak önceliğinde ikinci sıra, plan §2.4). Önkoşul: P4 (0012 kurulu mu) ve P7.
4. **P-d log işareti.** En son, istek anında, ES'e pencere zorunlu tek sorgu, `serveCached`. Liste sayfalarında hesaplanmaz. Değeri küçük; kuyruğun sonunda kalabilir.

**Yapmayacaklarım:**
- **A (v2 çift koşu).** Kazancı P7'deki kitleyle sınırlı; bedeli 30 gün çift INSERT ve çift disk.
- **B (host MV).** v1'in alt kümesi; tek katkısı host önekli okuma, o da ölçülmedi.
- **Pod önekli ömür MV'si** (cost note §4 "diğerleri"). E4 koşulu Q6 soğuk p95 > 200 ms idi; ölçüm bu notta yok.
- **Her tick'te VM `count by`.** 15 dk yeter; 60 sn'de bir 10 MB yanıt gereksiz.

**P-a ileride istenirse** (P7 büyük çıkarsa): A değil **A2**. Çift koşu ve disk tepesi olmadan aynı iki kolonu verir. Önkoşul: P4'te iş yükü kolonlarının her host'ta bulunması.

**Kapsam dışı ama aynı tabloda görünen:** `entity_seen_1m` okuyucusuz. Düşürülmesi INSERT yolundan bir MV geçişi ve ~207M × K satır kaldırır [T]. Bu bir şema değişikliğidir, S6'ya dahil değildir; ayrı karar (soru 2).

## 9. Koddan belirlenemeyenler

- Prod span/gün, INSERT blok hızı ve boyutu, MV başına gerçek süre (P3, P5, P6).
- Span tablosunun prod shard anahtarı; dolayısıyla `K` (P4).
- Prod'da namespace'siz satır sayısı ve bunların host mu attr'sız pod mu olduğu (P2).
- 0012'nin (rollout katmanı) prod'da kurulu olup olmadığı (P1, P4).
- VM'de servis/pod/host etiket adları; kurulumdan kuruluma değişiyor (`vmetrics/labelmap.go`).
- `system.query_views_log` ve `part_log`'un prod'da açık olup olmadığı.
- Cost note Q6'nın ölçülüp ölçülmediği.

## 10. Açık sorular (operatör)

| # | Soru | Tek satırlık cevap biçimi |
|---|---|---|
| 1 | K8s dışı (VM / fiziksel) host'lardan span ya da metrik geliyor mu, kabaca kaç tane | "yok" / "~N" |
| 2 | `entity_seen_1m`'i Coremetry dışında okuyan var mı (pano, elle sorgu); yoksa ayrı bir kararla düşürülsün mü | "okuyan yok, düşür" / "kalsın" |
| 3 | 0012 (rollout katmanı) prod'da uygulanmış mı | "evet" / "hayır" |
| 4 | Yalnız metrik basan servisler entity listesinde span basanlarla aynı listede mi görünsün, kaynak rozetiyle mi | "aynı liste + rozet" / "ayrı" |
| 5 | Preflight (P1–P8) çıktısı gelmeden P-b'ye başlansın mı (şema yok, geri alma = sürüm) | "başla" / "önce ölç" |
