# Scale-audit — 2026-09-23

**Kapsam:** `/scale-audit` skill'inin 8 kontrolü, 4 paralel Explore ajanı; her aday
±10 satır bağlamla okundu, 🔴/🟡'lar ana oturumda koda karşı yeniden doğrulandı
([[feedback-audit-verify-context]]). Önceki koşum: 2026-07-20 (v0.9.132-137);
aradaki ~700 sürüm tarandı. **Önceki koşumun yanlış pozitifleri yeniden
bayraklanmadı** (audit-log iç rol kontrolü, log-templates CH okuması).

**Yargı:** yapısal katman temiz — cache anahtarlarında uzunluk-özeti sınıfı yok
(~185 `serveCached`), `timeRangeToNs` 31/31 memoized, `setInterval` 6/6
`document.hidden` korumalı, copilot attribution tek çekirdekten (`aiCall`),
settings hydration 14 servis + 7 sunucu blobu tam, logstore yönlendirmesi
backend-agnostik, 72 mutasyon rotası admin kapılı + audit'li. Bulgular
kenarlarda: bir sınırsız tablo + satır başına fan-out (SLOs), üç cv'siz
büyük tablo, üç audit'siz test rotası, iki staleTime uyumsuzluğu, bir trace
sayım/liste tutarsızlığı.

## 🔴 Critical

- [Slos.tsx:115](../../frontend/src/pages/Slos.tsx#L115) — `dt.sortedRows.map`
  TÜM `/api/slos` listesini çizer (limit/offset yok, `VirtualTable` yok,
  `contentVisibility` yok) ve her satır iki HAM `useEffect` isteği açar:
  `ForecastChip` → `api.sloForecast` (:332), `BurnSparkline` →
  `api.sloBurnSeries` (:551) — React Query dışı, dedup/staleTime/iptal yok.
  `POST /api/slos/autocreate` `limit` ≤ 200 SLO damgalar → 400 istek/yükleme
  (sunucu 60 s önbellekli ama round-trip sayısı aynı). ES-maliyet disiplinine
  ("liste ön-çekimi yok, aç/expand'da çek") aykırı.
  **Fix:** satırlar `contentVisibility` (>100) + iki çip `LazyMount` içinde
  ve `useQuery` (staleTime 60 s = sunucu TTL) — fan-out görünür satırla sınırlanır.

## 🟡 Risk (kuyruk)

Frontend — tablolar / pickers
- [EntityDetail.tsx:124](../../frontend/src/pages/EntityDetail.tsx#L124) —
  `svc.services.map` düz `<table>`, `api.entityServices` limit almıyor;
  cluster entity'sinde penceredeki HER servis (1000+). cv yok. **Fix:** sunucu
  LIMIT 500 + "+N daha" notu + cv.
- [EntityDetail.tsx:144](../../frontend/src/pages/EntityDetail.tsx#L144) —
  `rows.slice(0, 200)` sınırlı ama >100 satır cv'siz (ev kuralı:
  `DBQueriesPanel.tsx:213` / `Profiling.tsx:176` kalıbı). **Fix:** cv.
- [AdminCatalog.tsx:67](../../frontend/src/pages/AdminCatalog.tsx#L67) —
  `serviceNames()` varsayılan 200 → katalog 200 serviste SESSİZCE kırpılır
  (doğruluk). Düzeltme (857): sunucu tavanı 1000. *Ajanın "cv yok" iddiası
  YANLIŞTI: cv `DisplayRow` içinde v0.5.199'dan beri var (map noktasına bakılmış).*
- [lib/queries/services.ts:24](../../frontend/src/lib/queries/services.ts#L24) —
  `useServiceNames(q?)` `q`siz çağrılınca tam katalog (200) 5 dk önbellek;
  bugün SIFIR çağıran — yüklü tuzak. **Fix:** `q` zorunlu ya da hook'u sil.
- [FilterBuilder.tsx:189](../../frontend/src/components/FilterBuilder.tsx#L189) ·
  [MetricQueryEditor.tsx:292](../../frontend/src/components/viz/MetricQueryEditor.tsx#L292) —
  `api.metricLabels(metric, key)` `q`/`limit`siz; daraltma istemcide.
  Sunucu 200 tavanı var (debounce'lu, mount'ta değil) → bugün sınırlı;
  yüksek kardinalite etikette (pod, user.id) uzun kuyruk ulaşılamaz.
  **Fix:** `q` parametresi (ServicePicker kalıbı).
- [DetailsMetricsSection.tsx:57](../../frontend/src/pages/service/DetailsMetricsSection.tsx#L57) —
  `metricNamesSearch(service, '', 500)` Details mount'unda; servis kapsamlı,
  yalnız panel var/yok kararı için. Kabul edilebilir; `service` boş olamaz
  pinlenmeli.

Frontend — polling
- [ProblemsSection.tsx:300](../../frontend/src/features/anomalies/ProblemsSection.tsx#L300) —
  `staleTime 5 s` / `refetchInterval 30 s`: her remount ek fetch; kardeşler
  30/25 s. **Fix:** 30 s.
- [lib/queries/rollouts.ts:46](../../frontend/src/lib/queries/rollouts.ts#L46) —
  `useRolloutRuns` 10 s / 30 s; sunucu TTL 10 s (`rollouts:runs`) — dosya
  başlığı "15 s / 60 s" diyor, bayat. **Fix:** 30 s + yorum.
- ⚪ [AdminClickhouse.tsx:3193](../../frontend/src/pages/AdminClickhouse.tsx#L3193) —
  backfill ilerleme poll'u 2 s: `document.hidden` korumalı, iş bitince
  kendini kapatır, yalnız koşarken kurulu — bilinçli, bırakıldı.

Backend — yetki / audit
- [mcp_client_routes.go:34](../../internal/api/mcp_client_routes.go#L34) ·
  [oracle_routes.go:43](../../internal/api/oracle_routes.go#L43) ·
  [vmetrics_routes.go:30](../../internal/api/vmetrics_routes.go#L30) —
  `POST /api/settings/{mcp-servers,oracle,victoria-metrics}/test` admin
  kapılı ama audit YAZMIYOR; operatör-sağlanan kimlik bilgisiyle dış
  bağlantı açıyorlar. Emsal: `ai_settings_profiles.go:223`
  (`settings.ai.profile.test`, `ok=%v`). **Fix:** üçüne aynı satır.
- ⚪ [trace_root_def_settings.go:39](../../internal/api/trace_root_def_settings.go#L39) —
  `GET /api/settings/trace-root-def` kapısız: `pages/Traces.tsx` (viewer
  sayfası) tüketiyor, değer gizli olmayan bir enum — bilinçli.
- ⚪ `POST …/replica-consistency/repair/plan` audit'siz — salt okuma (plan).

Backend — cache anahtarları (soğuk anahtar, zehirlenme değil)
- [shapes.go:35](../../internal/api/shapes.go#L35) · [api.go:2812](../../internal/api/api.go#L2812)
  (`service-deploys`) — ham `UnixNano()` + `to = time.Now()` varsayılanı:
  parametresiz çağrıda her istek yeni anahtar, TTL hiç işlemez. Kardeşleri
  `cacheBucket` / `deployWindowBucket`. **Fix:** kova.
- [api.go:3962](../../internal/api/api.go#L3962) (`attr-values`) — ham
  `from/to` dizesi; kardeşi `attr-keys` (:3765) `cacheRawQueryGrid`'e
  oturuyor ("SPA to=Date.now() her poll MISS etmesin"). **Fix:** aynı grid.
- ⚪ `explain-charts` singleflight anahtarı (copilot_service_charts.go:45):
  FE from/to'yu açıkça gönderiyor → sayfa aralığına göre stabil; `time.Now()`
  yalnız parametresiz varsayılan. Bulgu düştü.

Backend — doğruluk (cache değil)
- [api.go:4525](../../internal/api/api.go#L4525) `getTracesCount` ↔
  [trace_count.go:104](../../internal/chstore/trace_count.go#L104) — sayım
  MV yolunda `rootOnly`'yi `root_service_state != ''` (ilk-span tanımı) ile
  uygular, liste `rootHavingRaw(TraceRootDef)` (v0.10.733 canlı kök tanımı).
  `def=entry`'de rozet ile satırlar AYRIŞIR. **Fix:** kök tanımı varsayılan
  değilse sayımı ham yola düşür ya da rozete "≈" + tanım notu.

## ✅ Clean
- Cache anahtarları: ~185 site, set/dilim/harita hepsi sorted+FNV; `n=%d`
  yalnız limit/size skalerleri. `refresh=1` tutarlı (`cacheRawQueryString`
  refresh'i anahtardan düşürür — v0.10.256 C4).
- `timeRangeToNs` 31/31 memo/effect içinde; `Date.now()` hiçbir queryKey/dep
  dizisinde değil (ev kalıbı: `problemOffenders.ts:40` 60 s rungu).
- Picker'lar: `ServicePicker`/`OperationPicker`/`MetricNamePicker` 180 ms
  debounce + sunucu 200; `Combobox` 200 seçenekte kırpar; `GroupedMetricPicker`
  `enabled: open`.
- Polling: 6/6 ham `setInterval` `document.hidden` korumalı; dashboard
  refresh seçenekleri `[0,30,60,300]`'e sabit; SSE yeniden bağlanma tarayıcıya
  bırakılmış + Web Locks tek lider sekme.
- Yetki: 2026-07-20'den beri 32 rota dosyası, 72 mutasyon — hepsi admin
  kapılı + audit'li; Settings viewer'a kapalı (backend GET'ler de kapılı —
  tutarlı).
- Hydration: 14 servis blobu `LoadPersisted` + 30 s tazeleme + PUT'ta
  `SavePersisted`; 7 sunucu blobu (`LoadX` + `StartXRefresh`).
- Copilot attribution: tek çekirdek `aiCall` (ai_observability.go:153);
  matris `ai_call_matrix_test.go` ile pinli.
- Logstore: dedektörler `logstore.Store.CountPatterns`; `chstore.ListLogTemplates`
  türetilmiş Drain tablosunu okur (07-20 kararı).

## 5. ClickHouse sınırları + MV bypass

🔴
- [trace_explain.go:163](../../internal/chstore/trace_explain.go#L163) —
  `countMatchingTracesSQL`: `SELECT count() FROM (SELECT trace_id FROM spans
  <where> GROUP BY trace_id <having>)`, yalnız `max_execution_time = 10`;
  LIMIT/erken durma yok. Her BOŞ `/api/traces` cevabında otomatik koşar
  (api.go:4297 `EmptyDiagWanted` → `CountMatchingSpans` → trace-düzeyi dal).
  İkizi `buildTraceCountSQL` (trace_count.go:109) GROUP BY'ın tüm pencereyi
  okuduğunu ÖLÇÜP (v0.9.633) `DISTINCT … LIMIT cap+1 · max_threads=1`'e
  geçmişti; bu kopya kapsız kaldı. **Fix:** teşhis sayımı yaklaşık olabilir —
  `LIMIT cap+1` + `max_rows_to_group_by = cap, group_by_overflow_mode =
  'break', max_threads = 1`; FE tavanı "≥" ile gösterir.

🟡
- [repo.go:5390](../../internal/chstore/repo.go#L5390) — `metricNamesRawSelectSQL`
  `FROM metric_points … GROUP BY metric` yalnız `paged` iken LIMIT alır;
  `defaultUnlimited` (:5518, picker ön-çekimi) 7 günlük pencerede LIMIT'siz.
  Belgeli `metric_catalog` → ham düşüşü ama sonuç kümesi sınırsız. **Fix:**
  LIMIT 2000.
- [logstore/clickhouse.go:430](../../internal/logstore/clickhouse.go#L430) —
  `countOnePattern` ana toplaması `FROM logs … match(body, ?)`
  `max_execution_time`siz; 20 satır altındaki servis-kırılımı kardeşi `= 5`.
  Dedektör tikinde desen başına sıralı koşar → N tavansız regex taraması.
  **Fix:** `= 5`.
- [topology.go:213](../../internal/chstore/topology.go#L213) — `GetFlowTopology`
  `root_traces` CTE'si LIMIT'siz, iki geçişte `GLOBAL IN`'e besler; dış LIMIT +
  `max_execution_time = 60` var ama 60 s canlı operatör görünümünde
  (`/api/topology`). **Fix:** CTE'ye LIMIT (örnek tavanı) ya da 25 s.
- [baseline.go:73](../../internal/chstore/baseline.go#L73) — gecikme
  baseline'ı `FROM spans WHERE time >= ?` üst sınırsız + `service==""` iken
  servis pinsiz: tüm-servis quantile geçişi yalnız `max_execution_time = 10`
  ile. **Fix:** `time < ?` + servis zorunlu.
- [api.go:4054](../../internal/api/api.go#L4054) — attr-values `q`li dal:
  iç `FROM spans … has(attr_keys,?)` örnek LIMIT'siz (kardeş `q`siz dal
  `attrValuesSampleRows` ile kapaklı); dış LIMIT toplamadan sonra, 25 s
  tavan typeahead'de. v0.9.242 belgeli ödünleşim — artık risk.
- [state_replication.go:274](../../internal/chstore/state_replication.go#L274) —
  `queryReplicaPaths` `clusterAllReplicas(system.replicas)` yalnız
  `skip_unavailable_shards = 1`, `max_execution_time` yok. **Fix:** `= 10`.
- MV-bypass ŞEKLİ, ölü kod: [topology.go:1540](../../internal/chstore/topology.go#L1540)
  `GetServiceTopologyEdges` (ham `spans GLOBAL JOIN spans` = `topology_edges_5m`)
  ve [topology.go:166](../../internal/chstore/topology.go#L166) `GetRootFlows`
  (= `topology_root_flows_5m`) — çağıranı YOK, invariant #3'ü ihlal eden ve
  bir kablolama uzağında duran gövdeler. **Fix:** sil.

✅ Clean
- Her diğer ham `spans`/`metric_points` okuması ya belgeli MV→ham düşüşü
  (`useMV`/`operationsUseMV`/`slowQueriesUseMV` kapıları, env filtresi MV'de
  yok, deploys MV hatası) ya da MV'nin cevaplayamadığı filtre (attribute,
  search); hepsi zaman pinli + LIMIT/tavan sarmalayıcılı (`heavyScanSpill`,
  `shardSkipSetting`, `queryMemSetting`, `dbInstanceQuerySettings`).
- `clusterAllReplicas(system.*)` okumaları (biri hariç) tavanlı; GLOBAL'sız
  `IN (SELECT)`/JOIN yok; `spans`/`metric_points`/`logs` üstünde `FINAL` yok.
- Trace-id fan-out şekilleri (neighbors/service_map/endpoints_downstream)
  LIMIT'siz ama örneklenmiş id listesiyle sınırlı (v0.9.231).

## Sonuç (2026-09-23, aynı gün)

| Sürüm | Bulgu |
|---|---|
| 852 (+853 ileri düzeltme) | 🔴 countMatchingTracesSQL kapağı — max_rows_to_group_by + break + LIMIT cap+1 + max_threads=1; `matchingCapped` → FE "≥" |
| 854 | 🔴 Slos.tsx — useQuery + LazyMount `compact` + cv |
| 855 | mcp/oracle/vm test rotaları audit |
| 856 | ProblemsSection / useRolloutRuns staleTime 30 s |
| 857 | EntityDetail cv ×2 + AdminCatalog serviceNames 1000 (*AdminCatalog "cv yok" iddiası yanlıştı*) |
| 858 | countOnePattern = 10 · replicaPathsSQL = 10 · metricNamesRaw sayfasız LIMIT 5000 |
| 859 | GetRootFlows + GetServiceTopologyEdges silindi (Ç10 pinleri 3→2, 2→1) |
| 860 | traceShapesKey / serviceDeploysKey / attrValuesKey kovaları (`_windows.go` GOOS tuzağı → `_window.go`) |
| 861 | trace sayımı ↔ liste kök tanımı (traceCountRootPred + anahtar soneki) |
| 863 | GetFlowTopology root_traces CTE LIMIT 20k (yazıcı CTE'ye dokunulmadı) |
| 865 | useServiceNames silindi |
| 866 | baseline.go → service_summary_5m (dört dal; tdigest q(1) ≈ max) |

**Düşen bulgular:** entityServices LIMIT — handler cluster tipini `errBadRequest` ile
reddediyor, namespace/workload yolunda pod listesi 500'e kelepçeli → sınırlı (yanlış
pozitif). explain-charts anahtarı, trace-root-def GET, backfill 2 s poll → ⚪.

**Kuyrukta:** metricLabels `q`/`limit` — `metricSource` arayüzü (CH + VM + Thanos) üç
uygulamada değişir (~1 saat); attr-values `q`li dalın örnek LIMIT'i (v0.9.242 belgeli
ödünleşim); DetailsMetricsSection `service` boş olamaz pini.
