# Dynatrace paritesi — Services · Problems · Predictive analytics (2026-09-12)

**Taban:** v0.10.695 · **Yöntem:** operatörün yüklediği Dynatrace skill'leri
(`dt-obs-services`, `dt-obs-problems`, `dt-obs-predictive-analytics`) yetenek
listesi olarak alındı; her madde koddan `dosya:satır` ile doğrulandı (iki
salt-okunur keşif). **Durum:** rapor; kod değişikliği yok. Önceki parite
denetimi `docs/plans/dynatrace-parity-2026-08-21.md` (EK B1 #1/#6 kapandı,
#3/#4/#5 açık).

Durum sözlüğü: **VAR** = Dynatrace'teki soru Coremetry'de aynı kalitede
cevaplanır · **KISMİ** = cevaplanır ama sınırlı · **YOK**.

---

## 1. Application Services

| Yetenek | Durum | Yüzey | Boşluk |
|---|---|---|---|
| RED (rate / error / p50-p95-p99), servis + operasyon | VAR | `service_summary_5m`, `operation_summary_5m`, `spanmetrics_1m`; `/api/services`, `/api/endpoints`, `POST /api/spans/metric-batch`; Services/Endpoints/Overview | — |
| Cluster / env yan yana kıyas | KISMİ | `/api/services/{name}/clusters` tablo (err/avg/p99) | MV'lerde cluster ve env boyutu yok → filtre ham span taramasına düşer (`servicesUseMV`, `errEndpointsMVEnv`); env altında sparkline kapalı; p50/p95 ve zaman serisi yok |
| SLA / SLO | VAR | `slo.go` availability+latency, burn-rate 2 pencere, budget forecast, autocreate; `/slos` | — |
| Sağlık skoru | KISMİ | `scoreHealth` 3 renk (critical problem / err>5 / err>1) | Sayısal 0-100 skor yok; latency ve SLO durumu skora girmiyor |
| Messaging (publish/receive/process/failure) | VAR | `messaging_summary_5m` operation boyutlu, e2e p95, Kafka istemci kataloğu, `/messaging` | Consumer-group **broker** lag yok (yalnız istemci JMX görüşü); Kafka dışı sistemlerde lag yok |
| Service mesh (Istio/Envoy overhead) | YOK | — | Hiç temel yok |
| Runtime metrikleri | KISMİ | `RuntimeCharts.tsx` JVM (heap/GC/threads), .NET, Go temel | Node.js (event loop), Python, PHP yok; Go goroutines, JVM class yok; runtime alarmları emekli (v0.9.1075) |
| Statik eşik / anomali / kıyas | VAR (endpoint eşiği KISMİ) | alert_rules p95/error_rate/rate, MAD-z + 14 g mevsimsel, `compare=prior`, CoSRE window_compare | Route/endpoint bazlı statik eşik yok (`RuleTarget` yalnız db_statement, kafka_client) |

## 2. Problems (Davis)

| Yetenek | Durum | Yüzey | Boşluk |
|---|---|---|---|
| Problem modeli | KISMİ | `problem.go` Kind service/db/external, P1-P3 + reason, RootCause özeti, blast radius, deploy | Kategori (AVAILABILITY/ERROR/SLOWDOWN/RESOURCE) alanı yok (rule_id önekinden türetilebilir); görüntü kimliği (P-1234) yok; etkilenen kullanıcı sayısı yok; host/node öznesi yok |
| Korelasyon / birleştirme | KISMİ | Anomali kümeleme (≥3 üye, kaynak servis), exception fırtınası, incident otomatik gruplama (30 dk / 1-hop), çapraz-sinyal füzyon | Dakikalara yayılan kaskadda erken açılanlar ayrı kalır (join-on-open yok); incident kök-neden çapası değil; nedensellik skorlu birleştirme yok |
| Kök neden | VAR | `topology_edges_5m` korelatör (2 hop, decay), deploy ≫ propagation ≫ co-firing füzyonu, DeepEvidence playbook, LLM verdict (auto v0.9.1281), etki (error share) | Temporal korelasyon çarpanı yok; host/process zincir adımı değil (yalnız node co-tenancy); kullanıcı etkisi yok |
| Yaşam döngüsü & trend | KISMİ | open→ack→resolved, snooze, `sweepStaleProblems`, benzer problem şeridi, `/shift`, gürültülü kural sayfası | MTTR/MTTA yok; problem zaman serisi/trend yok (`listProblemBuckets` yalnız sayım); "tekrarlayan" rozeti/filtresi yok |
| Çapraz alan | KISMİ | DB/external/K8s pod/rollout/log kanıtı/sentetik | Host/node özneli problem yok; external/db düğümü topoloji grafında değil |

## 3. Predictive analytics

| Yetenek | Durum | Yüzey | Boşluk |
|---|---|---|---|
| Forecast | KISMİ | SLO bütçe tükenme saati; DB kapasite ETA (lineer, R²≥0.6, ≤24 sa); CH disk ETA gün | Genel metrik forecast primitifi yok; mevsimsel/trend ayrışması yok; güven bandı yok; grafik üstünde çizilmiyor |
| Değişim / yenilik | KISMİ | MAD-z spike, log pattern new/spike, trace op new_error/error_spike, op p99 sıçraması, davranış motoru rejim kayması, `/api/correlate` "ne değişti" | Change-point (CUSUM/PELT) yok; step vs trend-onset ayrımı yok; şablon spike ertelenmiş |
| Eşik / adaptif / mevsimsel | KISMİ | statik kurallar + Watcher import; MAD-z openZ 3.5 / resolveZ 1.5; 14 g gün-sınıfı mevsimsel; hassasiyet ayarları | OTLP/infra metrikleri (JVM heap, CPU) için öğrenilmiş baseline yok — yalnız sabit eşik; kural editöründe "baseline mi eşik mi" seçimi yok |
| Sinyal karakterizasyonu | KISMİ (zayıf) | 7 g `MetricBaseline` → suggest threshold; SLO autocreate | Mevsimsellik/gürültü/trend profili yüzeyi yok; dedektör önerisi yok |
| Doygunluğa kaç gün | KISMİ | DB, CH disk | Host CPU/mem, pod/JVM heap, cluster, CH tablo büyümesi için projeksiyon yok |

---

## 4. Öncelikli boşluklar (birleşik sıralama)

Sıralama ölçütü: operatörün Dynatrace alışkanlığında en çok arayacağı şey ×
"korelasyon farklılaştırıcıdır" ilkesine hizmet (docs/DECISIONS.md).

| # | Boşluk | Uygulama taslağı | Boy | Korelasyon ilkesi |
|---|---|---|---|---|
| 1 | **Join-on-open birleştirme + incident düzeyi kök neden** — **GEMİDE** v0.10.698 (B: incident kök neden) + v0.10.699 (A: join-on-open, `clusterJoinWindow` 30 dk) | `detectAnomalyClusters` girdisine son N dk açık problemler; yeni açılış propagation-bağlantılı açık kümeye üye olsun; incident satırı üye hipotezlerin en yüksek güvenli TopSuspect'ini taşısın | M | ✔ doğrudan |
| 2 | **Temporal korelasyon çarpanı + 3 hop + dikey zincir** — **GEMİDE** v0.10.700 (dilim 1 gölge: faktör + gerekçe + ayar) + v0.10.701 (dilim 2: 3 hop, prompt/katalog zamansal satır, causal_chain node yönergesi) | `propagationMaxHops` vidası 2→3; 5 dk hata-serisi korelasyonunu (`ChangedService.Score`) propagation skoruna çarpan; `RankNodeCauses` adayı `causal_chain` adımı olarak verdict prompt'una | M | ✔ doğrudan |
| 3 | **Endpoint/route hedefli alert rule** — **GEMİDE** v0.10.705 (`http_route` hedefi, spanmetrics_1m ölçüsü, Endpoints ⚠ + detay düğmesi) | `RuleTarget`'a `http_route` türü; ölçü `spanmetrics_1m` (service, route) state'lerinden; Endpoints satırından "alarm kur" | S-M | kısmen |
| 4 | **OTLP/infra metrikleri için adaptif baseline** | `metricPolicies` desenine `jvm_heap_pct`, `gc_pause_ms`, `cpu_pct` (metricSource seam'i); mevcut dwell/seasonal kapıları aynen | M | ✔ (infra anomalisi hipoteze kanıt) |
| 5 | **Problem modeli: kategori + görüntü kimliği + etkilenen varlıklar** — **GEMİDE** v0.10.706 (kategori + `P-xxxxx`) + v0.10.707 (`/api/problems/{id}/affected`: çağıranlar ∪ hipotez pod'ları ∪ cluster'lar; çekmece + detay) | `rule_id` önek → `category` türetici (okuma anı, saf); `display_id` sıralı sayaç (boot-ALTER, iki-boot); `affectedEntities[]` = blast-radius callers ∪ AffectedPods ∪ cluster üyeleri | S-M | kısmen |
| 6 | **Genel forecast primitifi + "kaç gün" chip'i** | `capacityETA` + `diskETADays` → tek `forecast` paketi (lineer + haftalık mevsimsel ortalama, R² kapısı, ±band); Hosts/Clusters/AdminClickhouse'da chip; `self-*` ailesine host-disk/pod-heap ETA | M | GEMİDE dilim 1+2 (v0.10.901: `internal/forecast` + AdminStats disk rozeti — AdminClickhouse değil, disk paneli orada yok; §13); dilim 3/4 ayrı Onay |
| 7 | **MTTR/MTTA + problem zaman serisi** | `/api/problems/series` (`noisy_rules` medyan süre mantığı), Problems sayfasında trend şeridi | S | ✘ |
| 8 | **Cluster/env boyutlu RED rollup** | `service_env_summary_5m` (cluster, deploy_env) MV; `servicesUseMV` kapısı kalkar; ClusterBreakdown'a p50/p95 + seri; `/clickhouse-schema` iki-boot | L | ✘ |
| 9 | **Node.js / Python runtime kartları + Go goroutines** | `RuntimeCharts.tsx` FAMILIES'e nodejs (eventloop.delay/utilization, heap) ve python | S | ✘ |
| 10 | **Broker-side consumer-group lag** | VM'de `kafka_consumergroup_lag` ailesi `KafkaCatalog`'a `Side:"broker"`; topic çekmecesinde group lag; `kafka_group_lag` alert hedefi | M | ✘ |
| — | Sayısal sağlık skoru (0-100) | `scoreHealth` → error_rate + p99/baseline + SLO burn + açık problem ağırlıkları | M | ✘ |
| — | Service mesh | Istio `istio_requests_total` VM ailesi + sidecar span ayrımı | L | ✘ (temel yok; sıraya alınmadı) |

**Öneri:** 1 → 2 (ikisi de korelasyon çekirdeği, spec ister) · ardından 3 ve
5 (küçük, görünür) · 4 (adaptif baseline'ı infra'ya taşımak) · 6-7 (Davis
görünürlüğü). 8/9/10 operatör önceliğine göre.

## 5. Operatör UX bulgusu (2026-09-12, prod ekran görüntüsü)

Servis → **Operations** sekmesindeki TREND sütunu: satır başına üç mikro
sparkline (calls · errors · p99) 30 px yükseklikte, okunmuyor ("kullanışsız").
Seçenekler — Operations tablosu "klasik" yüzey, mockup-first + tek commit
geri alınabilir kuralı geçerli:

- **A.** TREND sütununu kaldır; satır başındaki 📈 zaten grafiği açıyor.
- **B.** Tek geniş sparkline: çağrı çubukları (hata payı kırmızı) + p99 çizgisi,
  ~160 px, hover'da değer. Dynatrace/Datadog "top requests" düzeni.
- **C.** Sütunu kapalı varsayılan yap (kolon yöneticisinde seçilebilir).

Karar operatörde; öneri **B** (mockup ile).

**Sonuç (2026-09-12):** B onaylandı ve gemide v0.10.697 (`TrendSpark`).

## 6. #1 uygulama notları (v0.10.698 + v0.10.699)

- **B — incident kök neden:** `chstore/incident_rootcause.go`
  (`IncidentProblemIDs` tek sorgu + `GetHypotheses("problem")` tek sorgu +
  saf `pickIncidentRootCause`: adı olan, eşik 0.05 üstü, max güven → max
  skor → ad). `Incident.rootCause` okuma-anı; liste kolonu + detay çipi.
  Yalnız hipotezler (küme SourceScore karıştırılmadı); warning-only
  incident dürüstçe "—".
- **A — join-on-open:** `recentOpenCandidates` (son 30 dk açılmış bireysel
  `anomaly:<svc>:<metrik>` satırları, bu tik çözülenler hariç) aday
  listesine katılır; küme yazıldıktan sonra `mergeIntoCluster` üyeleri
  `resolved` + "· merged into anomaly-cluster:<src>" ekiyle kapatır
  (kolon yok); küme `StartedAt` = en eski katılan üye. Bildirim: bireysel
  anomali resolve'u bugün de bildirmiyor, merge de sessiz; kümenin tek
  bildirimi kaynağa. Kayan-pencere simülasyon testi
  `clustering_join_test.go` (kaskad t0/t+2/t+4, 31 dk kenarı, determinizm).
- Ayrı kalem: merged üyenin ekibine küme haberi (519 ekip yönlendirmesi).

## 7. #2 uygulama notları (v0.10.700, dilim 1 / gölge)

- `correlator/temporal.go`: `ComputeTemporalFactor` — birinci farklar
  üzerinde Spearman, lag 0..2 kova (aday önde), onset sırası cezası
  (aday tetikleyiciden sonra sıçradıysa ×0.5), <6 dolu kova → nötr 0.5.
- `Synthesize`: Tier 2 (yapısal komşu, Kind=="") adayına `Structural /
  Temporal / TemporalReason` yazılır; `TemporalApply` ile
  `Score = Structural × (0.5 + 0.5·t)`. Reason metni gölgede AYNEN kalır
  (üç metin pini korunur), gerekçe ayrı alanda.
- Seri okuması `chstore.ServiceErrorRateSeries5m` (service_summary_5m,
  IN ≤12, LIMIT 300 BY, 10 s); işçi pencere [onset−60m, son tam kova),
  tavan 3 saat. İşçi iki anchor yolunda Synthesize ÖNCESİ `attachTemporal`.
- Anahtar `anomaly_sensitivity.temporalRanking` (`shadow` varsayılan /
  `on`); Settings → Anomaly seçici. Operatör prod'da gölgeyi izleyip açar.
- Ribbon: kalıcı hipotez adayları artık çiziliyor (`lib/rootCauseCandidates.ts`;
  bugüne dek yalnız canlı correlations çiziliyordu) — kind rozeti + ⏱ gerekçe.
- **Dilim 2 GEMİDE v0.10.701:** `propagationMaxHops` 2→3 (hop-3 = 0.25×pay
  çarpımı, `TestRank3HopDecay`); zamansal gerekçe üç prompt yüzeyinde aday
  satırına " · " ile eklenir (hipotez bloğu TR, hakem kanıt kataloğu, Explain
  düzyazı) — gerekçesiz satır bayt-özdeş; hakem sistem prompt'una NEDENSEL
  ZİNCİR paragrafı (node adayı ayrı adım, "leads by" nedene yakın, "rose
  after it" semptom). Anahtar hâlâ operatörde (`temporalRanking` shadow).

## 8. #3 uygulama notları (v0.10.705)

- `RuleTarget.Kind = http_route` (+ `Route`), kimlik (service, http.route);
  method MV'de yok, RPC ve imza kipi kapsam dışı. Metrik ailesi önekli:
  `http_route_p95_ms | p99_ms | error_rate | rate` (düz adlar servis kuralıyla
  çakışmasın). Doğrulama `ValidateRuleTarget` üçüncü dalı; şema yok
  (target_json).
- Ölçü `chstore.RouteWindowStats`: spanmetrics_1m, giriş-span yüklemi,
  1 dk grid'e hizalı [now−w, now) yalnız tam kovalar, tDigest idx 3/4;
  rate = calls / kapsanan sn. Env-agnostik (MV'de deploy_env yok) — modal
  satır env altında okunduysa söyler.
- Evaluator: özne servis, Kind=service (Kafka emsali); MinSamples =
  penceredeki çağrı; açıklama `/endpoint?service=&path=` yolunu taşır.
- FE: RouteAlertModal (Statement/Kafka klonu, karşılaştırıcı seçilebilir —
  hız düşüşü için `<`), Endpoints satırı ⚠ (editor/admin, yalnız HTTP
  sekmesi), EndpointDetail başlığında "⚠ Alarm oluştur" (imza kipi hariç),
  /alerts "HTTP ROUTE" rozeti + salt-okunur kapsam.

## 9. #5 uygulama notları (v0.10.706, dilim 1)

- `chstore.ProblemCategory(p)` saf: rule_id öneki + metric + kind + comparator
  → AVAILABILITY / ERROR / SLOWDOWN / RESOURCE / CUSTOM (18 üretici ailesi
  tablo testinde). `ProblemDisplayID(id)` = "P-" + FNV-32a base36 (türetilmiş,
  sıralı değil; operatör kararı); `GetProblemByDisplayID` son 90 g / 5000 id
  tarar, en yeni eşleşme kazanır. İkisi de `EnrichProblemsWithPriority`
  döngüsünde dolar; şema yok.
- `/api/problems/{id}` ve MCP `get_problem` "P-…" kabul eder; `/api/problems?
  category=`, `/api/inbox?cat=` çip süzgeçleri (okuma-anı, zenginleştirme
  sonrası; api.go süzgeçleri `problems_read_filters.go`'ya taşındı, api.go
  10 satır küçüldü).
- Inbox: exception/httperror → ERROR, anomali türüne göre ERROR/SLOWDOWN,
  incident → CUSTOM (`inboxDerivedCategory`). FE: "Kategori" çipi (`?cat=`),
  SOURCE altında rozet, çekmece başlığında kategori + tıkla-kopyala `P-…`,
  palete `P-3f9a2` yazınca problem açılır.
- **Dilim 2 GEMİDE v0.10.707:** `GET /api/problems/{id}/affected` (kendi
  dosyası, route defteri, 60 s cache, `P-…` kabul): pencere onset−1h..çözüm|
  şimdi (≤24 s); saf `buildAffectedEntities` = blast-radius çağıranlar (çağrı
  desc, ≤25, kendi problemi açık olan işaretli) ∪ hipotez `Deep.AffectedPods`
  ∪ `Clusters`, tekrarsız, özne dışarıda. FE `AffectedEntitiesList` çekmece
  (yalnız problem satırı) + detay Blast radius bölümünde; servissiz problem
  dürüst "—". #5 KAPANDI (küme üyeleri yapısal alan olarak ertelendi).

## 10. #8 uygulama notları (v0.10.881–883, 2026-09-23)

Spec onayı: operatör "go" (spec gösterildi ve soruldu). Üç dilim, hepsi MV-first invariantı içinde:

- **881 — `service_env_summary_5m`:** AggregatingMergeTree, `ORDER BY (service_name, cluster, deploy_env, time_bucket)`, 5-dk kova, 90 gün TTL; cluster `clusterDeriveExpr` ile MV'de doğar (ham yol `clusterExpr` ile aynı türetme). State: count/countIf(error)/sum(duration)/tdigest(0.5,0.95,0.99)/apdex sat+tol. `canonicalMVs()` sonuna eklendi (mv_positional), cluster modunda `_local`+Distributed (highVolumeTables, shard `cityHash64(service_name)`), purge listesinde. `EnvSummaryCovers(from)` probu: `min(time_bucket)` 60 sn önbellekli — MV pencerenin başını kapsamıyorsa çağıranlar ham yola düşer (yeni MV geriye dolmaz; ilk 5 dk/gün boş).
- **882 — /api/services cluster/env filtresi:** `servicesUseMV(window, cluster, env, envMV)` kapısı (services_mv_gate.go): <5 dk her zaman ham; filtre yokken service_summary_5m; filtre varken yalnız env MV kapsıyorsa MV. `GetServicesEnvAggFiltered`/`CountServicesEnvAgg` — `envScopeClause` saf kurucu; clickhouse-local 26.2'de stub spans + MV DDL ile doğrulandı.
- **883 — /api/services/{name}/clusters:** MV yolunda p50/p95/p99 (arrayElement ile tdigest, Array(Float32) scan tuzağı yok) + cluster başına çağrı serisi (5 dk katı adım, ≤288 nokta, 0-dolgu); ham yolda eski davranış (p50/p95/seri yok). Cevap `source: mv|spans`, FE rozet + P50/P95 kolonları + Trend Sparkline. Önbellek anahtarı `v2 … mv=%t`.

Dürüstlük: MV kapsam dışıyken UI "spans" rozetini gösterir, kolonlar "—". Kapsam dolunca kendiliğinden MV'ye geçer (probe 60 sn). #8 KAPALI.

## 11. #4 uygulama notları — dilim 1 (v0.10.887, 2026-09-23)

Spec süreci: 4 okuyucu kod haritası → 3 bağımsız tasarım (motor-öncelikli /
VM-offset bant / en küçük dürüst dilim) → 2 jüri; "en küçük dürüst dilim"
13/15 ile kazandı, operatör "Onay" (2026-09-23). Uygulama sonrası 4 mercekli
inceleme + çürütücü turu; doğrulanan 12 bulgu 887'nin içinde kapandı.

- **Kapsam:** JVM heap-after-GC (% limit) pod başına 5-dk kova serisi + 24 s
  ardışık pencereden bant (medyan ± 3.5·MAD/0.6745; minMAD 2 puan, floorPct
  .10, minAbsDelta 5, dwell 3, criticalZ 6, yön yalnız yukarı). Yalnız
  görünürlük — Problem/bildirim yok. Pods › Runtime, yalnız JVM ailesi.
- **Veri:** `GET /api/services/{name}/heap-baseline` (route defteri, api.go
  0 satır), metricSource dikişi (VM/CH), `jvm.memory.used_after_last_gc` /
  `jvm.memory.limit` {type=heap}, GroupBy pod→host→instance[:8] (`cluster`
  hariç), kaynaktan 5-dk adım (CH LIMIT 50k tuzağı), `capped` bayrağı, en çok
  50 pod, önbellek 5-dk ızgarada.
- **Dürüstlük:** metrik/JVM pod yoksa kart yok; n<15 → "baseline yok (< 75 dk
  veri)"; rozet "bant: ardışık 24s · n"; ölmüş pod "sessiz · N dk önce"; hata
  tek satır; alt cümle kaynağı ve "problem açmaz"ı söyler.
- **Dilim 2/3 (ayrı Onay):** Problem (`runtime.jvm_heap_pct` → RESOURCE,
  servis başına 1 Problem + suçlu pod, Threshold = bant üstü, 1 hafta
  would-open logu, varsayılan kapalı; Inbox'ta kind=problem hâlâ P3 çivili);
  VM'de pod ön-seçimi; `cluster` anahtarı; mevsimsel (kaydırmalı okuma) +
  GC pause. CPU kaynağı Thanos cAdvisor — bu hatla gelmiyor.
- **Operatör teyidi bekleyen:** prod JBoss pod'ları OTel `jvm.memory.*`
  basıyor mu (basmıyorsa kart hiç çıkmaz — Thanos JMX yolu ayrı iş); prod
  metrik deposu VM mi.

## 12. #4 uygulama notları — dilim 2 (v0.10.889–891, 2026-09-23)

Spec süreci: 4 okuyucu (dedektör tiki, yaşam döngüsü tüketicileri, ayar vidası,
FE) → 2 tasarım (artımlı halka / 10-dk epoch 24 s okuma) → 2 jüri (bölündü) →
hibrit: halka + lider başlangıcında tek seferlik doldurma. Operatör "Onay".
Cevapsız sorularda varsayılan: P1 politikası RED ile aynı (gölge **would-P1**
ölçer), elle kapatma → RED gibi yeniden açılır, Inbox tür kuralı olduğu gibi,
gölge→canlı ölçütü 7 gün / ≤5 would-open/gün.

- **889 (yan kazanım):** RED anomalisinin histerezis ("none") dalı açık satırı
  touch etmiyordu → süpürme 3 dk'da "kaynak sustu" diye kapatıyordu; resolveZ
  histerezisi fiilen yoktu. Düzeltildi (external.go emsali).
- **890:** `anomaly_sensitivity.runtime` alt bloğu (heapMode off|shadow|on —
  varsayılan off, heapSource auto|vm|ch, minMAD/floorPct/minAbsDelta/silent/
  maxPods); HeapBandPolicy; okuyucu `anomaly/heap_read.go`'ya taşındı — kart ve
  dedektör aynı okuyucu + aynı politika. Settings › Anomaly alt bölümü.
- **891 (gölge):** dedektör tikinde `scanHeap` (kümeleme sonrası, davranış
  öncesi, soft-fail). (servis,pod) halkası 288 kova; tik başına son 2 kova
  filo-geneli tek çift sorgu (GroupBy servis+pod), tavanda filo sonucu korunur +
  ≤60 servis/tik parçalı; lider başlangıcında parçalı 24 s doldurma (≤60
  servis/tik, 45 s bütçe); kaynak HeapSourceOr (VM/CH çağrı anında, main.go
  atomik setter). Problem YAZILMAZ; sayaçlar `/api/stats.heap`; would-open
  geçişinde tek log; would-P1 (computePriority sentetik satır); verdict Redis
  15 dk → kart rozeti "◐ gölge: problem AÇILIRDI". `on` bu sürümde gölge gibi.
- **Canlı kip dilimi (≥1 hafta gölge sonrası, operatör kararı; sürüm numarası o gün):** applyOutcome'a Pod +
  touch, resolve gerekçeleri ("no live JVM pod"), off → "detector disabled"
  kapanışı, notify_kind pin satırı, displayMetric/unitOf '%', ProblemDetail pill
  "en kötü pod", kartta açık problem bağlantısı; opsiyonel Inbox pod rozeti dilimi.
- **Bilinen sınırlar:** `cluster` kimlik anahtarında yok; CH sum-kova oran
  belirsizliği (limit seyrekse); VM ≥1000 seri tavanı parçalı okumayla aşılıyor
  (rotasyon); lider değişiminde açık satır touch boşluğu (892'de RED ile aynı).

## 13. #6 uygulama notları — dilim 1+2 (v0.10.901, 2026-09-23)

Spec süreci: 3 okuyucu (ETA çekirdekleri, tüketici yüzeyler, veri kaynakları/
maliyet) → 2 tasarım ("tek forecast paketi" / "en küçük dürüst dilim") → jüri:
tek paket tasarımı kazandı, diğerinden iki aşı — ilk yüzey Problem satırından
beslensin (sıfır ek sorgu), sayı satırda yoksa yazılmasın. Spec + mockup
gösterildi; operatör "devam" (gösterilmiş spec'e cevap = onay). İnceleme turu:
5 mercek → 30 bulgu, 13 onaylı (3 major), hepsi bu sürümde kapatıldı.

- **Dilim 1 — `internal/forecast` (davranış değişimi sıfır):** iki kopya
  OLS+R² bloğu (`evaluator.capacityETA` saat, `evaluator.diskETADays` gün)
  tek saf `forecast.Fit(points, limit, Opts) Result` çağrısına indi. Kapılar
  (min nokta / aralık / R² / ufuk) çağırandan; **"zaten limitte" bir DURUM**
  (`StatusAtLimit`): capacityETA tahmin yok (eşik dalı konuşur), diskETADays
  0 gün — iki zıt test pini korunur (docs/DECISIONS.md). Kayan-nokta birikim
  sırası birebir; `linear_test` eski gövdeyi `==` ile tutturur (ufuk sınırı
  ±ulp, R² düşük dal dâhil). Tek bilinçli sapma: NaN/±Inf örnek "geçersiz"
  (eskiden `ok=true` + "~NaNh"). Yeni: ±band (delta yöntemi — Var ŷₙ + Var b +
  kovaryans; kantil t(n−2); eğim sıfırdan ayırt edilemiyorsa üst sınır +Inf) ve
  `Reason` cümlesi. Band R² kapısının ÜSTÜNE gelir, yerine değil; bu sürümde
  hiçbir yüzeyde yok (Monte Carlo kapsama pini ≥%85/88/90, sabit tohum).
- **Dilim 2 — /admin/stats "ClickHouse disk capacity" ⏳ rozeti:** kaynak
  YALNIZ `OpenProblemsSnapshot`'taki çözülmemiş (open|acknowledged)
  `self-disk-eta` satırı (`Value` = gün; 5 s memo, zarf 60 s serveCached).
  Yeni rota yok, polling yok, hesap yok, api.go dokunulmadı. `disks[].forecast
  {days, critical, severity, thresholdDays, note, problemId}`; rozet
  `/problems?problem=<id>`; tooltip `diskReason` cümlesi. Yüzey **AdminStats**
  (§4 satırı "AdminClickhouse" demişti; orada disk paneli yok). Tek düğümde
  anahtar uyuşmazlığı (CollectDisks her dalda `hostName()`, sysstats `''`) →
  "/<disk>" son-ek eşlemesi, yalnız tek aday varsa.
- **Dürüstlük kuralları (inceleme turu):** rozet TONU `days < 2`'den
  (`chstore.SelfDiskCriticalDays`), satırın `Severity`'sinden değil —
  Severity yaş eskalasyonuyla 30 dk'da critical'a çıkar (Inbox sözleşmesi);
  tavanda (0 gün) "dolu (projeksiyon tavanda)", "≈ 0 dakika kaldı" değil;
  değerlendirici kalp atışı ok değilse rozet soluk + "değerlendirici N dk
  sessiz" (satır donar — süpürme de değerlendiricinin içinde koşar); FE
  `fmtEtaDays` Go `fmtDays` ile tam-ikili yarımlarda da aynı (yarım-çift);
  alt yazı "rozet yoksa tahmin ya eşiğin üstünde ya da kurulamadı".
- **Lider değişimi (major bulgu, düzeltildi):** yeni liderin boş bellek-içi
  serisi ilk tikte açık satırı "çözüldü" diye kapatıp 30 dk sonra yeni
  StartedAt + yeni bildirimle yeniden açıyordu → ısınma penceresinde
  (`diskSeriesWarm`) açık satır son değeriyle taşınır (`diskCarryOver`,
  v0.9.1294 volCache deseni). Isınma dışı "eğilim yok" satırı yine kapatır.
- **Yan düzeltmeler:** sysstats küme disk sorgusuna `skip_unavailable_shards`
  (CollectDisks paritesi); `self-disk-eta` literal'leri `chstore.SelfDiskRuleID`.
- **Bilinen sınırlar:** chip yalnız açık satır varken — "tahmin var ama
  problem yok" (>7 gün) görünmez (dilim 4); R² satırda yok → yazılmaz; ±band
  yüzeyde yok; VM 5 dk MAX / CH 5 dk avg kova farkı olduğu gibi (dilim 3
  istek-anı hesabında `Method`/title taşımalı).
- **Sonraki dilimler (her biri ayrı mockup + Onay):** 3 = `problems.
  forecast_hours` kolonu (migration kapılı) → DB kapasite ETA'sı metinden
  sayıya, Oracle/Postgres `GaugeStat` "≈ N saat kaldı" + ProblemDetail rozeti;
  ön şart panel `?instance` ↔ `CapacitySample.Instance` eşleme testi. 4 =
  disk tarihçesi kalıcılaştırma (liderde `self.disk_used_bytes` gauge →
  metric_points, rollup otomatik; operatör kararı) → "tahmin var ama problem
  yok" + haftalık mevsimsel; Hosts/Clusters CPU-mem chip (6 sa pencere, yalnız
  lineer, ufuk pencere×N); JVM heap ETA. Host DİSK kaynağı yok (ayrı iş).
- **Kapsam dışı bulgu:** vitest tam koşuda önceden var olan
  `resetLayoutAdoption` 70>69 hatası (HEAD v0.10.900'de de kırmızı; bu
  diff'ten bağımsız) — kuyruğa.
