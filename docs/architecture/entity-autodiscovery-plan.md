# Entity auto-discovery planı — "her şey bir entity"

**Tarih:** 2026-11-15 · **Durum:** onaylı (operatör 2026-11-15); S0 v0.10.2193, S1 v0.10.2214, S2 v0.10.2232, S3 v0.10.2233 · **Taban:** v0.10.2190
**Operatör ilkesi:** "everything is an entity — pod, service, deployment, namespace, node, cluster";
"entities can be auto-discovered like Dynatrace (Smartscape); later needed for pod logs, traces etc."
**Tetik:** Clusters namespace detayı, aynı adlı servis varken Deployment `bsa-digital-dashboard-prod` için
"servis eşleşmedi" dedi. Belirti v0.10.2191'de ayrıca düzeltiliyor; bu belge kök nedeni ele alır:
ilişkiler sayfa başına ad hoc hesaplanıyor.
**Önceki belgeler:** `docs/plans/entity-layer-design-2026-08-28.md` (bugünkü modelin tasarımı),
`docs/audit/entity-layer-discovery-2026-08-28.md`, `docs/audit/k8s-entity-layer-2026-08-26.md`,
`docs/audit/clusters-pod-service-correlation-audit.md`.

Özet: entity deposu **var** ve çalışıyor, ama (a) bayrak arkasında, (b) yalnız K8s dikey ekseni,
(c) tek bir çözümleyici (resolver) yok. Bu yüzden her sayfa kendi join'ini yazdı. Plan yeni bir depo
kurmaz; mevcut depoyu her zaman açık, kendi kendini keşfeden tek ilişki kaynağı yapar ve ad hoc
join'leri dilim dilim siler.

## 1. Bugünkü durum

Satır numaraları v0.10.2190 içindir. "Uçuşta" işaretliler v0.10.2191 çalışma ağacındadır, commit'li değildir.

### 1.1 Depo, ömür, ilişki

| Parça | Yer | Not |
|---|---|---|
| Tipler: cluster, node, namespace, workload, pod, container, service | `internal/entity/identity.go:15-21` | host, database, queue, external, service instance yok |
| Kimlik: `cluster:<cid>`, `node:<cid>/<n>`, `ns:`, `wl:<cid>/<ns>/<Kind>/<ad>`, `pod:<cid>/<ns>/<ad>`, `ctr:`, `svc:<ad>` | `identity.go:51-61`, `ParseID` `:97` | kök `cid` Remote Cluster kaydından gelir, keşfedilmez (`identity.go:5-6`) |
| İlişkiler: `parent`, `runs_on` (pod→node), `runs` (pod→service) | `identity.go:26-28` | workload→service, `calls`, host kenarı yok |
| Tablolar: `entities`, `entity_relations` (ReplicatedReplacingMergeTree(version), `valid_from`/`valid_to`/`last_seen`, TTL 180 gün), `entity_sync_runs` | `migrations/0011_entity_layer.sql:83-136` | state tabloları tek keeper yolunda; her düğümde tam kopya |
| MV: `entity_seen_1m` (3 gün) ve `entity_seen_5m` (30 gün), `ORDER BY (service_name, cluster, k8s_namespace, k8s_pod, time_bucket)`, shard `cityHash64(service_name)` | `internal/chstore/entity_schema.go:37-59`, `0011:141-183` | yalnız pod anahtarlı, iş yükü kolonu yok |
| Ömür farkı: Open, Close, Refresh; ulaşılamayan cluster'da hiçbir şey kapanmaz | `internal/entity/diff.go:35-72` | pod adı yeniden kullanılırsa uid değişimi yeni ömür açar |
| Syncer: lider kilidiyle, cluster başına tick | `main.go:1216-1222`, `internal/entity/syncer.go:190-355` | çok replikada yalnız lider yazar |
| Bayrak ve vidalar: `entity_layer.enabled` (varsayılan kapalı), `syncInterval`, `podGap`, `staleAfter`, `parallelClusters` | `internal/entity/settings.go:28-62` | uçlar kapalıyken 404 `{disabled:true}` döner (`internal/api/entities.go:51`) |

### 1.2 Besleyen sinyaller

| Sinyal | Bugün | Yer |
|---|---|---|
| Metrik, Thanos kube-state-metrics | 6 anlık sorgu/cluster/tick: `kube_node_info`, `kube_pod_info`, `kube_pod_owner`, `kube_replicaset_owner`, `kube_job_owner`, `kube_pod_container_info` | `internal/entity/samples.go:21-30` |
| Span resource attr | `k8s.namespace.name`, `k8s.pod.name` (yoksa `host_name`), `k8s.node.name` terfi kolonları → `entity_seen_*`; span geçişi KSM'nin görmediği pod/ns/node/servisi üretir | `internal/chstore/promoted_attr.go:100-102`, `internal/entity/spanpass.go:69-135` |
| Span `k8s.deployment.name` / statefulset / daemonset / replicaset | Terfi kolonu var, entity katmanı okumuyor; iş yükü pod adından regex'le türetiliyor | `promoted_attr.go:116-119`, `internal/entity/derived_workload.go:23` |
| Span cluster değeri | Bir Remote Cluster kaydına atanmamışsa satırlar `(unmapped)` koşusuna sayılır, entity üretilmez; atama operatör adımı | `syncer.go:219-226`, `internal/api/thanos_identity.go:241` |
| Metrik, OTLP `host_name` (metric_points / VM) | Entity katmanına girmiyor; yalnız ad hoc `PodServiceMap` | `internal/chstore/podservice.go:35` |
| Topoloji (`topology_edges_5m`: service→service/db/queue/ext) | Ayrı kimlik şeması (`db:`, `queue:`, `ext:`), entity deposuna bağlı değil | `internal/chstore/identity.go:91-105`, `internal/chstore/topology.go:48` |
| Log | Keşif kaynağı değil. Pod alanı backend eşlemesinde var | `internal/logstore/field_mapping.go:511`, `internal/logstore/elasticsearch.go:3097` |

### 1.3 Okuyanlar

- Entity/Pod sayfaları ve Service → Pods: `/api/entities`, `/api/entity*`, `/api/services/{name}/pods`
  (`internal/api/entities.go:38-47`); FE `frontend/src/lib/queries/entities.ts`, `pages/Pod.tsx`, `pages/EntityDetail.tsx`.
- MCP/CoSRE: `list_namespaces`, `list_workloads`, `list_pods` (`internal/mcptools/entity_catalog.go:140,298,516`),
  `resolve_entity` (`internal/mcptools/resolve_entity.go:347`).
- Anomaly: exception pod sinyali `EntitySeenForService` (`internal/anomaly/exception_pods_signal.go:376`).
- Services namespace süzgeci: `entity_seen_5m` (`docs/DECISIONS.md:3153`).
- Clusters sayfaları depoyu **okumaz**; canlı Thanos satırları üzerinde çalışır (`internal/api/thanos_handlers.go`).

### 1.4 Modeli atlayan ad hoc join'ler

| # | Yer | Ne yapıyor | Sorun |
|---|---|---|---|
| A1 | `internal/api/thanos_handlers.go:154-168`, `internal/api/podservice.go:12` | Clusters `/pods` servis kolonu: `metric_points` üzerinde `host_name = pod adı`, 15 dk ham tarama | Uygulama metrik basmıyorsa sessizce boş; tetikle aynı kök |
| A2 | `internal/api/cluster_entity_links.go:22,39` (uçuşta), FE `nsDetailModel.ts` `workloadService` | İş yükü → servis: `entity_seen_5m` çoğunluğu, sonra attr, sonra ad eşitliği | Doğru yön, ama sayfaya özel ve kalıcı kenar yazmıyor |
| A3 | `internal/thanos/pod_family.go:38`, `internal/api/copilot_pods_thanos.go:82`, `internal/api/mcp_service_pods.go:39` | CoSRE/MCP servis pod'larını ad regex'iyle seçiyor | Kardeş önek yanlış eşleşir; ad ≠ deployment ise pod bulunmaz |
| A4 | `internal/chstore/service_metadata.go:391,631` | Servisin deployment/namespace'i: 3 saatlik `FROM spans` deriver'ı ve pod adından türetme | Ham span taraması (`max_execution_time = 25`); entity ile ikinci gerçek |
| A5 | `internal/api/entity_workload_fallback.go:31` | Rollouts `wl:` linki "Kayıt bulunamadı": tür kaynağa göre Deployment / ReplicaSet / Derived | Aynı iş yükü, üç kimlik (`docs/DECISIONS.md:8726`) |
| A6 | `workload_revision_activity_1m` → workload→service (`docs/DECISIONS.md:8718`) | Rollouts kendi eşlemesini yapıyor | Üçüncü workload↔service yolu |
| A7 | FE `pages/clusters/podWorkload.ts:14`, `internal/thanos/promql.go:671` | Pod adından iş yükü sezgiseli, iki dilde ayna | Sahiplik zinciri depoda zaten var |
| A8 | FE `pages/Pod.tsx:373`, `pages/pod/PodLogsSection.tsx:75` | `/api/logs` pod parametresi almıyor; CH backend'de pod adı log gövdesinde aranıyor | Pod logları eksik ya da yanlış |
| A9 | `internal/api/trace_pod_metrics.go:259`, FE `lib/traceK8sLinks.ts:26` | Trace → pod: cluster eşlemesi ve entity linki yerinde kuruluyor | Çözümleme iki yerde |
| A10 | `internal/chstore/pod_inventory.go:123`, `internal/chstore/hosts.go:97` | Pod envanteri ham span örnekleminden; host listesi ayrı | Depoyla çakışan envanterler |

**Kod okumasından ölçek riski (ölçülmedi):** `DiffLifetimes` her açık ömür için her tick `Refresh` üretir ve
syncer bunları koşulsuz yazar (`diff.go:50-56`, `syncer.go:427,460`). 40K pod'da kaba tahmin: ~115K entity +
~190K ilişki ≈ 300K satır/dk, günde ~430M satır; iki RMT tablosu `FINAL` ile okunuyor. S1 bunu keser.
Bayrağın prod değeri bu denetimde doğrulanmadı.

## 2. Hedef model

### 2.1 Tipler ve kimlikler

| Tip | `entity_id` | Not |
|---|---|---|
| cluster | `cluster:<cid>` | `cid`: Remote Cluster kaydı id'si; kayıt yoksa span cluster değeri (karar K2) |
| node | `node:<cid>/<node>` | |
| namespace | `ns:<cid>/<ns>` | |
| workload{kind} | `wl:<cid>/<ns>/<Kind>/<ad>` | Kind kanonik: Deployment, StatefulSet, DaemonSet, CronJob, Job, Rollout |
| pod | `pod:<cid>/<ns>/<ad>` | uid attribute; yeniden kullanılan ad yeni ömür |
| container | `ctr:<cid>/<ns>/<pod>/<ad>` | |
| service | `svc:<service.name>` | küresel (karar K3); ad verbatim |
| host | `host:<host.name>` | K8s dışı (namespace'siz `host_name`); bugün `spanpass.go:76` atıyor |
| database / queue / external | `db:<sistem>@<host>`, `queue:<sistem>/<ad>`, `ext:<host>` | topoloji düğüm kimliğiyle **birebir** (`chstore/identity.go:91-93`); ikinci şema yok |
| service instance | entity değil, `runs` kenarının ömrü (karar K4) | `service.instance.id` süreç başına UUID, churn |

Kimlik kuralları: ad bileşenleri verbatim (OTel gerçeğin kaynağı); id yalnız doğal anahtardan oluşur
(hash, sayaç yok), her replika aynı girdiden aynı id'yi üretir. İş yükünde tek kanonikleştirme fonksiyonu:
`<ad>-<rs-hash>` ReplicaSet → Deployment `<ad>`; `Derived` yalnız yetkili kaynak yokken. Yetkili kaynak
sonradan gelirse `Derived` ömrü kapanır, `same_as` kenarı yazılır (eski linkler çözülür).

### 2.2 İlişkiler

Depolama yönü bugünkü gibi `(parent_id, child_id)`. Brief'teki `part_of`/`owns` = depodaki `parent`
(yeniden adlandırma yok; `rel_type` ORDER BY'da, tam tablo yeniden yazımı kazanç getirmez).

| `rel_type` | Yön | Kaynak | Durum |
|---|---|---|---|
| `parent` (part_of / owns) | cluster→node, cluster→ns, ns→wl, wl→pod, pod→ctr | KSM, span | var |
| `runs_on` | pod→node | KSM, span | var |
| `runs` (instance_of) | pod→svc, host→svc; çoktan çoğa | span, sonra metrik | var (host yeni) |
| `serves` | wl→svc | pod `runs` kenarlarının span ağırlıklı toplamı; açık `k8s.*.name` attr'ı | **yeni**, tetiğin kalıcı çözümü |
| `calls` | svc→svc, svc→db, svc→queue, svc→ext | `topology_edges_5m` | **yeni**; yalnız ömür satırı, sayılar MV'de kalır |
| `same_as` | eski id→kanonik id | syncer | **yeni**; resolver en çok 1 adım izler |

Türetilenler saklanmaz, resolver yoldan hesaplar: svc→ns, svc→cluster, svc→node, node→svc.

### 2.3 Ömür

`valid_from`/`valid_to`/`first_seen`/`last_seen` aynen kalır; silme yok, eskitme var. `first_seen` kaynakların
en erkeni; kapanış `valid_to = last_seen`. Ulaşılamayan kaynak hiçbir şeyi kapatmaz (`diff.go:14-15`).
Kenarın ömrü uçlarından bağımsızdır (pod yaşarken servis değişebilir). Tüm okumalar `at` alır; trace ya da
problem anındaki topoloji sorgulanabilir.

### 2.4 Kaynak önceliği (sinyaller çelişirse)

| Soru | Öncelik (soldan sağa) |
|---|---|
| K8s nesnesi var mı, sahibi/node'u ne | KSM > span resource attr > pod adı sezgiseli |
| pod/host → servis | span (`entity_seen`, span payı) > OTLP metrik `host_name` > hiçbiri |
| iş yükü → servis | pod `runs` toplamı > açık `k8s.deployment.name` (ve türevleri) attr'ı > ad eşitliği (harf duyarsız) |
| cluster kimliği | Remote Cluster kaydı (`SpanClusterKeys`) > span cluster değeri |
| Ad yazımı | sinyaldeki verbatim değer; normalizasyon yok |

Kurallar: (1) düşük öncelikli kaynak, yükseğin yazdığı satırı ezmez (`spanpass.go:80` emsali). (2) Aynı
kenarı birden çok kaynak görürse `source` en yükseğidir. (3) Belirsizlikte yanlış kenar yerine **kenar yok**
(`pickPodService` kuralı); çoklu servisli pod'da tüm `runs` kenarları saklanır, "birincil" okuma anında span
payıyla seçilir. (4) Oy (👍/👎) hiçbir önceliğe girmez. (5) Eşikler koddaki sabitlerdir; ayar yok.

## 3. Auto-discovery

### 3.1 Akış

```
spans ──INSERT──▶ entity_seen_5m (MV) ─┐
KSM (Thanos, cluster başına 6 sorgu) ──┼─▶ lider syncer tick ─▶ entities / entity_relations (RMT)
topology_edges_5m (MV) ────────────────┘        │                        ▲
                                                 └─ sync_runs             │ FINAL, at-geçerli
                        entity.Resolver ◀── cache (Redis, 30 s) ◀────────┘
```

Yapılandırma yok: bayrak ve dört vida sabite iner (K1); katman, 0011 nesneleri varsa çalışır, yoksa uçlar
bugünkü gibi fail-open kalır. Atanmamış span cluster değeri kendi cluster entity'sini üretir (K2).

### 3.2 Geçişler (hepsi lider tick'inde, hepsi sınırlı)

| Geçiş | Okuma | Sınır |
|---|---|---|
| KSM | cluster başına seçicili 6 anlık sorgu (bugünkü) | paralel 4 cluster, cluster başına zaman aşımı; düşen cluster atlanır |
| Span | `entity_seen_5m`, son ≤ 1 saat, `GROUP BY cluster, ns, pod, service` (bugünkü, `entity_store.go:195-204`) | `LIMIT 200000`, `max_execution_time = 10`; LIMIT dolarsa cluster başına sayfalanır |
| `serves` | Yeni okuma yok: aynı tick'in `wl→pod` ve `pod→svc` kümelerinden bellekte | O(pod) |
| `calls` | `topology_edges_5m`, son 15 dk, `GROUP BY parent_service, child_service`, zaman sınırlı + LIMIT | yalnız aç/kapa yazılır |
| Metrik `runs` (S6) | VM'de tek anlık `count by (service, pod\|host)`; ham `metric_points` taraması yok | etiket kardinalitesi ≈ pod sayısı |

Ham `spans` hiçbir geçişte okunmaz. A4 ve A10'daki ham okumalar depoya taşınınca silinir.

### 3.3 Prod ölçeği (20 cluster, ~40K pod, milyarlarca span/gün) — tahmin, S1'de ölçülecek

- `entity_seen_5m`: anahtar (service, cluster, ns, pod, kova). 40K pod × 288 kova ≈ 11,5M satır/gün,
  30 günde ~350M (AggregatingMergeTree, tDigest yok). MV maliyeti INSERT başına düz kolon okuması (bugün de ödeniyor).
- `entities`: ~115K açık ömür. Churn günde 100K pod varsayımıyla ~0,5M satır/gün (aç + kapa, pod + container).
- `entity_relations`: pod başına 3 kenar, günde ~1M satır; `serves` ~10K, `calls` ~50K açık satır.
- **Yazım diyeti (S1):** `Refresh` yalnız `last_seen` sabit bir aralıktan (podGap/2 = 5 dk) eskiyse yazılır;
  boşluk denetimi aynı değeri okuduğundan lider devrinde davranış değişmez. Beklenen: 300K satır/dk → ≤ 60K/dk.
  **v0.10.2225 ile değişti** (`entity-storage-cost-note.md`, C + R1b): CH'de yalnız kalıcı katman (cluster,
  node, namespace, workload, service + parent kenarları; değişimde + günde bir, ~19 satır/dk); pod/container
  ve pod uçlu kenarlar satır değil, lider tick'inin Redis'e yazdığı cluster başına tek blob
  (`coremetry:entity:v1:live:<cid>`, TTL 3 tick). ZSET yedeği düştü.
- Distributed güvenliği: state tabloları her düğümde tam kopya, okuma yerel, `GLOBAL IN` gerekmez.
  `entity_seen` shard anahtarı `service_name`; pod yönlü "şimdi" sorusu canlı dizinden (bellek içi),
  ıskada ya da geçmiş anda `entity_seen_5m`'den (v0.10.2225; `entity_relations.runs` artık yazılmıyor).
- Liste sayfası N+1 yapmaz: toplu yol çözümü (≤ 500 id) 2 sorgudur (`parent_id IN`, `entity_id IN`).

### 3.4 Pod churn ve çok replika

- Churn: kısa ömürlü pod (CronJob) tek tick'te görülür ya da hiç; span geçişi `entity_seen_5m`'in
  `first_seen`/`last_seen` state'lerinden geriye dönük ömür yazar (`ClosedRowsForDead` emsali, `spanpass.go:208`).
- StatefulSet ad tekrarı: uid değişimi ya da boşluk > podGap yeni ömür (bugünkü kural).
- Çok replika: yalnız lider yazar (Redis mutex, invariant 8). Tick idempotent: aynı girdi → aynı satırlar,
  RMT `version` en yeniyi seçer; çift lider anında çift yazım zararsızdır. Okuma her replikadan; cache anahtarı
  tüm girdileri hash'ler.
- Kaynak kesintisi: Thanos düşerse KSM kaynaklı ömürler kapanmaz; span geçişi sürer.

## 4. Tüketiciler — tek resolver

`internal/entity/resolver.go` (saf çekirdek + chstore adaptörü), HTTP `internal/api/entity_resolve.go`:

```go
Get(ctx, id, at) (Node, bool)                         // same_as izlenir
Lookup(ctx, Hint{ClusterValue, Namespace, Pod, Host, Service, Workload}, at) []Node  // ham sinyal → entity
Neighbours(ctx, id, rels []string, dir, at, limit) []Edge
Path(ctx, id, at) Path                                 // pod → workload → service → namespace → node → cluster
Paths(ctx, ids []string, at) map[string]Path           // liste sayfaları, ≤ 500
```

`GET /api/entity/resolve`, `GET /api/entity/neighbours`, `POST /api/entity/paths`; hepsi `s.serveCached`.

| Tüketici | Bugün | Sonra | Silinen |
|---|---|---|---|
| Clusters pods / workloads / ns detayı | A1, A2, A7 | handler `Paths` ile zenginleştirir; FE yalnız gösterir | `PodServiceMap`, `pickPodService`, FE `workloadService` çoğunluğu, `podWorkloadName`, `cluster_entity_links.go` (resolver'a taşınır) |
| Services (liste, Infra sekmesi) | A4 | svc → `serves`/`runs` komşuları | `deploymentFromPodName`, 3 saatlik span deriver'ının deployment/namespace kolu |
| Rollouts | A5, A6 | satır → `wl:` kanonik id → `serves` | `entity_workload_fallback.go`, `RolloutWorkloadServicesBatch` eşlemesi |
| Pod sayfası, log ↔ trace pivotları | A8, A9 | `/api/logs?entity=pod:…` → backend alan süzgeci; span → `Lookup` → çipler | gövdede pod adı araması, FE `traceK8sHref` içindeki cluster eşlemesi, `tracePodClusterFor` |
| CoSRE / MCP | A3 | svc → pod kümesi `Neighbours`; `resolve_entity` yol + komşu döndürür | `ServicePodRegex` ve çağıranları |
| Topoloji, DB/queue sayfaları | ayrı kimlik | düğüm id = entity id; `calls` komşuları | `TopologyNodeIdentity` dışındaki önek ayrıştırmaları |
| Envanter kartları | A10 | depo sayımları | `GetPodInventory` ham örneklemi |

Araç çıktıları deterministik veridir; LLM'e bağlı adım yok (prod LLM yalnız yerel, etkilenmez).

## 5. Dilimler (her biri tek sürüm, bağımsız gönderilebilir)

| Dilim | İçerik | Kullanıcının gördüğü | Risk | Göç / geri alma | Testler |
|---|---|---|---|---|---|
| **S0 — Resolver v0 + Clusters** ✅ v0.10.2193 | `entity.Resolver` (`Lookup`, `Paths`), mevcut kaynaklar üstünde, şema yok. Clusters `/pods` ve `/deployments` aynı yoldan; A1 ve A2 silinir | Pod satırlarının servis kolonu metrik basmayan uygulamalarda da dolar; pod ve iş yükü tabloları aynı cevabı verir | `/pods` gecikmesi (+1 MV okuması) | Şema yok; geri alma = sürümü geri al | Öncelik tablo testi; SQL şekil testi (zaman sınırı, LIMIT, `max_execution_time`); handler golden; `PodServiceMap` çağıranı kalmadı ratchet'i |
| **S1 — Her zaman açık + yazım diyeti** ✅ v0.10.2214, değişiklik v0.10.2225 (aşağıda "S1 değişikliği") | Bayrak ve 4 vida → sabit (K1); atanmamış span cluster değeri → cluster entity (K2); `Refresh` kısma; Settings sekmesi yalnız durum | Kurulum yapmadan Pod/Entity sayfaları, Service → Pods, `list_pods`; atanmamış cluster'lar görünür | Açılışta ilk tick yükü; yanlış cluster çoğalması | `entity_layer` blob'unda yalnız backfill durumu kalır; geri alma = sürüm, veri durur | Kısma tablo testi (lider devri dahil); iki syncer aynı girdi → aynı satırlar; prod'da satır/dk ölçümü |
| **S2 — Kararlı iş yükü kimliği + `serves`** ✅ v0.10.2232 | Kanonik Kind, `same_as`, wl→svc kenarı kalıcı. Rollouts ve Service Infra resolver'dan; A5, A6, A7 silinir | Rollouts iş yükü hapı her zaman doğru servise/entity'ye gider; Service sayfasında "çalıştığı iş yükleri" | Eski `Derived`/ReplicaSet id'li linkler | Yeni `rel_type` değerleri, DDL yok; eski satırlar TTL ile düşer; geri alma = sürüm | Kanonikleştirme tablo testi; `same_as` 1 adım; `bsa-digital-dashboard-prod` regresyon satırı |
| **S3 — Pod ↔ log ↔ trace** ✅ v0.10.2233 | `/api/logs` entity kapsamı (ES ve CH alan eşlemesiyle); span detayında yol çipleri; pod → trace `k8s_pod` kolonundan; A8, A9 silinir | Pod logları iki backend'de de doğru; trace'ten pod/iş yükü/servise tek tık ve geri | ES alan adı çeşitliliği | Şema yok; geri alma = sürüm | Backend başına süzgeç golden'ı; FE vitest; ES sorgu şekli (term, PIT yok) |
| **S4 — CoSRE / MCP** | `ServicePodRegex` → `Neighbours`; `resolve_entity` yol + komşu; sweep/healthcheck pod adaptörleri | Adı deployment'tan farklı servislerde pod sağlığı gelir; kardeş önek karışmaz | Depoda olmayan servis (span yok) | Geri dönüş yolu yok (shim yasağı); depo boşsa araç "pod bilinmiyor" der | Araç golden'ları; CoSRE eval setinde pod soruları; regex çağıranı kalmadı ratchet'i |
| **S5 — Yatay eksen** | host, db, queue, ext entity; `calls` kenarı; topoloji id = entity id; A10 silinir | DB/queue/host entity sayfaları; "bu pod'un servisi hangi DB'yi çağırıyor" yolu; blast radius tek kaynaktan | `ext:` kardinalitesi | Yeni tip/rel değerleri; `LowCardinality` tip kolonları uygun; geri alma = sürüm | `calls` SQL şekli; kardinalite tavanı testi; topoloji ↔ entity id gidiş-dönüş |
| **S6 — Sinyal tamamlama** | `entity_seen` v2 MV (iş yükü kolonları, host satırları); VM'den metrik `runs`; log kapsama işareti (K5, K6) | Yalnız metrik/log basan servisler entity olur; regex türevi iş yükü kalmaz | MV INSERT maliyeti; çift MV dönemi | Yeni MV 30 gün çift koşar, sonra eski düşer; geri alma = yeni MV'yi DROP | MV DDL testi; prod preflight; INSERT gecikmesi öncesi/sonrası |

Her dilim sonunda dur, onay al. S0 → S1 → S2 sıralı; S3, S4, S5 S2'den sonra herhangi bir sırada; S6 en son.

## 6. Operatör kararları

**Onay (operatör, 2026-11-15):** plan ve K1–K6 öneriler gibi onaylandı — K1 evet (S1), K2 evet,
K3 evet (`svc:<service.name>`), K4 hayır, K5 hayır, K6 S6'da.

| # | Karar | Öneri |
|---|---|---|
| K1 | `entity_layer.enabled` bayrağı ve dört vida kalksın mı | **Evet (S1).** "Ayar yok" ilkesi; bayrak tüketicilerin depoya güvenmesini engelliyor. Sabitler: 60 s, 10 dk, 24 sa, 4 |
| K2 | Remote Cluster kaydı olmayan span cluster değeri otomatik cluster entity olsun mu | **Evet**, `source = span`; kayıt değeri sahiplenince `same_as`. Remote Cluster kaydı (kimlik bilgisi) asla otomatik yaratılmaz |
| K3 | Servis kimliği küresel `svc:<service.name>` mi, ortam/cluster başına mı | **Küresel.** Cluster ve namespace ömürlü ilişkidir; ortam süzgeç olarak kalır |
| K4 | Service instance ayrı entity mi | **Hayır, kenar.** `runs` (pod\|host → svc) ömrü instance'tır; `service.instance.id` attribute |
| K5 | Log keşif kaynağı olsun mu | **Tick'te hayır** (prod log backend'i ES, milyarlarca doküman). Log S3'te tüketici; kapsama işareti entity başına tembel hesaplanır |
| K6 | `entity_seen` v2 MV (iş yükü kolonları) yapılsın mı | **S6'da, S0–S4 değerini gösterdikten sonra**; `/clickhouse-schema` incelemesi ve prod preflight ile |

**Kapsam dışı:** multi-tenant, binary içi örnekleme, redaksiyon, yeni ayar/vida, oyların karara etkisi.

**S0 notları (v0.10.2193):** `entity.Resolver` (`internal/entity/resolver.go`) `Lookup` + `Paths`; api adaptörü
`internal/api/entity_resolver.go`. S0'da iş yükü ↔ pod sahipliği depodan değil, çağıranın KSM satırından
(`owned` argümanı; S2'de depodan). Pod → servis önceliğinin metrik kolu S0'da yok (ham `metric_points`
okuması silindi; VM kolu S6). Redis 30 s önbelleği yok; uçların `serveCached`'i yeterli. HTTP uçları
(`/api/entity/resolve` …) ilk tüketicisiyle (S3/S4) gelir.

**S1 notları (v0.10.2214):** sabitler `internal/entity/settings.go` (`SyncInterval`, `PodGap`, `StaleAfter`,
`ParallelClusters`, `RefreshEvery = PodGap/2`, `MaxSpanOnlyClusters = 64`); kısma `ThrottleRefresh` (`diff.go`),
karar yalnız CH'deki `last_seen`'e bağlı. K2 span-only cluster: `ClusterRef.SpanOnly`, id = span değeri, Thanos
sorgusu yok; atanmış değerin eski span-only ömürleri S2'nin `same_as`'ına kadar açık kalır, `last_seen` donar.
`EntityApply` 10K satırlık parçalar. Tick başına `[entity] tick: … rows_read … refresh_skipped … duration_ms`.
Settings sekmesi yalnız durum. Şema yok. Beklenen: ~300K satır/dk → ~51K/dk (40K pod).

**S1 değişikliği (v0.10.2225; `entity-storage-cost-note.md`, operatör onayı 2026-11-15: C + R1b, E1–E4
öneriler gibi):** pod, container ve pod uçlu kenarlar (wl→pod, ns→pod, pod→ctr, `runs`, `runs_on`) CH'ye
yazılmaz; kalıcı katman (cluster, node, namespace, workload, service + iki ucu kalıcı parent kenarları)
değişimde + günde bir (`RefreshEvery` 5 dk → 24 sa). `PodGap` sessizlik kuralı kalktı: kapanış yalnız art
arda başarılı görüntülerde ≥ `PodGap` yokluk (`diff.go` `Absence`); syncer susması ömrü kapatıp yeniden açmaz.
Tick'in açık ömür okuması yalnız kalıcı tipler (birincil anahtar öneki, LIMIT 200K, 10 s), tam satır taşır
(invariant #4); günlükte gerçek taranan satır (`rows_scanned`). Canlı dizin: lider tick'i cluster başına tek
gzip JSON blob, `coremetry:entity:v1:live:<cid>`, TTL 3 tick; `entity.Resolver` "şimdi" sorularında önce
ondan, ıska/Redis kesintisi/geçmiş anda MV. Redis kilidi degraded iken tick atlanır. `container_info`
sorgusu düştü (6 → 5). `backfillDead` / `ClosedRowsForDead` / `Existing` silindi. S1'den kalan açık pod
satırları TTL'e bırakıldı (E3; tipsiz liste ve çocuk sayımı onları okumaz).

**S2–S6 ayarı (aynı onay):** S2 — `serves` (wl→svc) geçici pod ile kalıcı servis arasındaki tek kalıcı
köprü; "sahiplik S2'de depodan" notu düşer, canlı dizinden gelir. S3 — `/api/logs?entity=pod:…` kimliği
ayrıştırır, ad + namespace + cluster değeri + zorunlu zaman penceresiyle süzer, depo okuması yok; span
çipleri span kolonlarından + dizinden. S4 — `Neighbours(svc→pod)` ve `list_pods` canlı dizinden, `at`
geçmişteyse MV öneki. S5 — host, db, queue, ext ve `calls` kalıcı katmana (aç/kapat + günlük tazeleme).
S6 — K6'ya pod önekli ömür MV'si seçeneği (Q6'ya göre); VM'den gelen metrik `runs` dizin girdisi olur.

**S2 notları (v0.10.2232):** kanonikleştirme `internal/entity/wlkind` (entity, thanos, api ortak; FE aynası
silindi). `serves` ve `same_as` lider tick'inde `BridgeRelations` (bellekte, aynı canlı görüntüden) ve
`mergeSpanOnly` (K2); `IsStableRelation` ikisini kalıcı katmana alır. Okuma `entity.Resolver`
(`Canonical`, `WorkloadServices`, `ServiceWorkloads`; `neighbours.go`) + chstore `EntityRelationsFor`.
HTTP: `GET /api/services/{name}/workloads`; genel `/api/entity/neighbours` S4'te ilk MCP tüketicisiyle.
Regresyon satırı kurgusal adla (`shop-dashboard-prod`). Şema yok.

**S3 notları (v0.10.2233):** `/api/logs?entity=` (`internal/api/logs_entity.go`) kimliği backend alan
süzgeçlerine indirir (iki backend; CH liste yolunun eksik cluster/namespace conjunct'ı eklendi); pencere
zorunlu, ≤ 7 gün. `POST /api/entity/paths` (`internal/api/entity_paths.go`) span → yol; cluster çözümü tek
yerde (`resolveSpanCluster`). Trace listesi hücreleri v0.10.330'dan beri düz metin; FE'deki ölü
`traceK8sHref` silindi. Sapma: `GET /api/entity/resolve` ayrı uç olarak açılmadı — tekil ipucu aynı
`POST /api/entity/paths`'ten.
