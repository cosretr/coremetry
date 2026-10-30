# CoSRE Login Briefing — Faz 0 denetimi (2026-10-30)

Durum: **salt-okunur denetim; kod değişmedi, commit yok.** Anlık görüntü: yerel `main`
a213cf8b (v0.10.1671) + dilim 2 için `origin/main` 2daab28c (v0.10.1683; yerel main 12
commit geride, dosyalar `git show origin/main:` ile okundu — `[o]` ile işaretli). Her
iddia `dosya:satır` taşır; olmayan şey "bulunamadı". Örnekler nötr (`orders-api`, `com.acme`).
Önceki denetim `docs/cosre/tools-audit.md` §9–§11 (araç seti) yeniden yapılmadı, referans verildi.

Operatör düzeltmeleri (bu denetime işlendi): kapsam anahtarı **takım** (users.team →
team_aliases → servisler), AD grup seti DEĞİL; worker "Hangi takım?" soramaz → takım/servis
çözülmezse o kullanıcıya briefing ÜRETİLMEZ, sohbette tek satır; `list_my_services`'in env
ve açık-Problem sayısı eksikleri Faz 1'de `open_problems` ile birlikte kapanır (§10 R6);
Faz 1 dilim 3 main'e inmeden BAŞLAMAZ (§12).

**Özet — en büyük engeller:** (1) Service sayfasında "Errors/Exceptions" sekmesi yok, Kafka
sayıları iki ayrı yoldan (FE PromQL · Go soru seti) çıkıyor → "sekmeyle aynı sayı" kalem 2/3'te
tanımsız; (2) exception "artış" için pencere-bazlı oluşum agregatı yok (`occurrences`
ömürlük); (3) ES takım log penceresi 60 dk tavanlı, CH logstore'da desteklenmiyor;
(4) v2 akışında sunucu-başlatan mesaj türü yok; persist/istek yolu (`cosrePersist.ts`)
enjekte edilen asistan mesajını modele ve arşive taşır; (5) dilim 3 main'de yok (§12).

## 1. v2 araçları — 7 kalem × hazır / parametre / eksik

| # | Kalem | Mevcut okuyucu (file:line) | Durum |
|---|---|---|---|
| 1 | RED sapması | `ReadTeamServicesRED(ctx,d,names,from,to)` `internal/mcptools/team_ownership.go:352` (`service_summary_5m`, IN ≤ `MaxTeamServices`=100 `:86`); tekil `ServiceWindowRED` `internal/chstore/compare_periods.go:1056`; önceki pencere `PriorWindow` `internal/chstore/prior_window.go:58` | **hazır** — cur+prior = 2 çağrı; eşik/baseline §3 |
| 2a | Kafka istemci (VM) | `vmetrics.KafkaServiceQuestions()` `internal/vmetrics/kafka.go:181-190` (connection_count, request_latency_avg/max, record_error_rate by topic, records_lag_max by topic·client_id); `runKafkaQuestions` `internal/api/messaging_metric.go:318`; `buildServiceKafkaClients` `:565`; `/api/services/{name}/kafka-clients` `:62`, serveCached `:665` | **parametre** — servis başına çağrı; prior pencere yok; etiket: `service_name` kapsam, `client_id/topic/partition/node_id` `kafka.go:51-76`; `host_name` yalnız FE `frontend/src/lib/kafkaMetrics.ts:61-65`; `consumer_group` **bulunamadı** |
| 2b | HTTP 5xx + p95, gRPC non-OK + p95 | sunucu tarafı `readServiceHTTPRED(ctx,rd,isVM,service,env,from,to,rangeS)` `internal/api/copilot_service_http_red.go:205`, sorgular `svcREDFamilyQueries` `:88`; aile `resolveServiceREDFamily` `internal/api/red_family.go:297`, `redFamilies` `:112-125`, hata regex `:77,79` | **parametre** — bunlar SERVER metrikleri (`http_server_*`, `rpc_server_*`); HTTP **client** (`http_client_request_duration_*`) Go'da **bulunamadı** (yalnız FE `frontend/src/lib/httpMetrics.ts:100-102`) |
| 3 | Errors/Exceptions | `ListExceptionGroups(ctx,f)` `internal/chstore/exception_inbox.go:790`, filtre `:585` (State, Services, ActiveFromNs/ToNs, MinOccurrences); yeni gruplar `RecentNewExceptionServices` `internal/chstore/exception_storm.go:34`; sayım `RecentExceptionGroupCounts` `exception_inbox.go:871`; op başına hata `TopErrorOperations` `internal/chstore/top_errors.go:59` | **kısmen** — "yeni" hazır; "artan tip" için pencere-bazlı oluşum yok (`occurrences` ömürlük; seri `GetExceptionOccurrences` `:1338` grup başına) → yardımcı gerek (§10 R2) |
| 4 | Log ERROR/WARN + yeni şablon | `TeamRecentLogs` `internal/logstore/team_logs.go:368` (ES; CH `:466` unsupported; tavan 60 dk `:48`); `Histogram(ctx,f,bucketSec,"severity")` `internal/logstore/logstore.go:541`; `GroupBySignatureN` `internal/logstore/patterns.go:167` + `JoinPatternBaseline` `:82` (`New`, `PrevCount`, `Ratio`); `CountPatterns` `logstore.go:513` | **parametre** — örnekleme tabanlı (2000 `patterns.go:34`), 3 sa pencere 60 dk'ya kırpılır |
| 5 | Oracle hata tablosu | CH kopyası `oracle_error_log` DDL `internal/chstore/oracle_error_log.go:93` (RMT, trace_id `:100` + bloom `:117`); kod×op×kanal×dakika `OracleGroupAggregates(ctx,sourceID,from,to)` `internal/chstore/oracle_exception_groups.go:112`; son/önceki saat `HourStats` `internal/oracle/exgroups.go:111`, `GroupStatsCache.Stats` `:565`; jenerik kod `GenericCodes` `internal/oracle/settings.go:139-142`, `QualifierFor` `internal/oracle/counter.go:61`; trace pivot `OracleErrorsByTrace` `oracle_error_log.go:193`, `OracleTraceIDsForOperation` `internal/chstore/oracle_op_traces.go:31` | **hazır** — 3 sa cur/prior için `OracleGroupAggregates` iki pencere; jenerik kod ayrı sayılır (`QualifierFor`) |
| 6 | Açık Problem | `ListProblems(ctx,ProblemFilter{Services[] :1136, Env :1182, NotStatuses :1092})` `internal/chstore/problem.go:1419`; açık = `status IN ('open','acknowledged')` `:1549`; takım demeti `readTeamSummary(ctx,team,svcs,env,from,to)` `internal/api/copilot_team_summary.go:256` | **hazır** — araç tarafı: `list_problems` tekil `service`; `list_my_services` env + açık sayı yok [o] `internal/mcptools/v2_tools.go:283-310` → Faz 1 |
| 7 | Son rollout + hata oranı farkı | `GetServiceDeploys(ctx,service,from,to)` `internal/chstore/deploys.go:1231`; `ComputeDeployImpact(ctx,service,version,deployTimeNs,windowSec)` `:259` (Before/After ErrorRate, `ErrorRateDeltaPct`); K8s `GetServiceRollouts` `:519`; servis↔workload `RolloutRefsForService` `internal/chstore/rollout_problem_telemetry.go:56`; araç `list_deployments` `internal/mcptools/list_deployments.go:164` | **hazır** |

Oracle entegrasyon katmanı: Oracle'a istek anında gidilmez; `internal/oracle/poller.go:219`
(`Run(ctx,isLeader)`, lider kapısı `:227`) periyodik kopyalar, lider `main.go:1308-1311`.
Oracle operasyon → `service.name`: öğrenilmiş harita `system_settings["oracle_opsvc:<kaynak>"]`
`internal/oracle/subject.go:47` (trace → pod → öğrenilmiş → fonksiyon kodu → `ext:<kaynak>`);
çözülmüş servis `exception_groups.service`'te `ora:` önekli grup olarak durur
`internal/chstore/oracle_exception_groups.go:48-67`. Çözülmeyen satırlar takım kapsamına girmez (§10 R5).

## 2. user_scope (dilim 2, origin/main v0.10.1683)

- Çözüm zinciri `resolveUserScope(ctx,escape,teamOverride)` [o] `internal/api/chat_user_scope.go:125-150`:
  `GetUserByID` `:133` → `users.team` → `ListServiceMetadata` → `TeamServiceNames(aliases,mds,team)`
  `:146` (`internal/mcptools/team_ownership.go:226`, owner **OR** sre). Karar saf: `resolveUserScopeCore` `:95-116`.
- Nedenler kapalı küme `:40-48` (`team | no_identity | no_team | no_services | disabled | escape | error`);
  `uistream.ScopeReasons` [o] `internal/cosre/uistream/part.go:26`; FE `parseDataScope` [o]
  `frontend/src/lib/cosre/protocol.ts:112`.
- Boş kapsam: `unscoped:true` + neden; `data-scope` parçası [o] `translator.go:64-67`; döngü eki
  `userScopeAddendum` `:164-176` modele "Hangi takım?" sordurur (`ChatUserScopeAskMarker` [o]
  `internal/copilot/prompts.go:2945`, `teamAlreadyAsked` `:153`). Ayar `ai.userScope`
  [o] `internal/api/ai_user_scope_settings.go:35`, `GET/PUT /api/ai/user-scope` `:30-31`, varsayılan açık `:110`.
- Worktree/dal: dilim 2 yalnız `origin/main`'de; yerel worktree'lerde kopyası yok (`ls .claude/worktrees/*/internal/mcptools/v2_*.go` boş).
- **Briefing kuralı (operatör):** neden ≠ `team` → o kullanıcı için üretim yok; sohbet açılışında
  tek satır "Ekibin tanımlı değil — hangi takımdasın?" ve mevcut `data-scope` akışına bağlanır
  (yeni mekanizma yok). AD grup senkronu (`ldap_groups`, tools-audit §3) daha sonra.
- **Deterministik kapsam anahtarı önerisi:** `briefing:v1:<fnv(CanonTeam(team))>:<fnv(sorted services)>:<env>:<windowS>:<to/300>`.
  Emsal `myServicesCacheKey` [o] `v2_tools.go:211-213` (fnvHex servisler + takım, `to/60`);
  kanonik ad `TeamAliases.CanonTeam` `internal/chstore/settings.go:658` (`NormTeamName` `:650`, Türkçe-İ güvenli).
  Takım başına tek satır: ORDER BY anahtarı `team_key` = `CanonTeam` çıktısı; servis özeti hash'i
  içerikte taşınır (kapsam değişince satır yenilenir, eski sürüm FINAL ile düşer).

## 3. Rollup tabloları, önceki pencere, baseline

- Önceki aynı pencere: `PriorWindow(from,to)` `internal/chstore/prior_window.go:58`, `PriorWindowGrid` `:87`,
  `PriorReadable` `:274`, `ScalePriorCount` `:251`; API emsali `redComparisonPlan` `internal/api/deployment_report.go:146`,
  `servicesPriorWindow` `internal/api/services_delta.go:77`.
- Dönem karşılaştırma: `ReadComparePeriods(ctx,sc,cur,ref)` `compare_periods.go:534` +
  `BuildPeriodComparison` `:816` (tdigest birleşimi, servis başına).
- Baseline: `GetMetricBaseline(ctx,service,metric,lookback)` `internal/chstore/baseline.go:49`;
  median+MAD `medianMAD` `internal/anomaly/anomaly.go:1736` (paket-içi, dışa açık değil) — briefing
  için 3 sa vs önceki 3 sa oranı (`PriorWindow`) yeter; MAD gerekirse `anomaly` içinden dışa alınır.
- Kalem 1 için en ucuz yol: `ReadTeamServicesRED` iki pencere (cur, prior) → servis başına oran.

## 4. ES

- Servis → index: `queryIndices` `internal/logstore/es_indices.go:273` (`IndexTemplate` `{service}/{namespace}`
  `:220`; yoksa gün indeksleri `narrowIndices` `:136`); env `es_env_field.go:135,182`.
- Servis×seviye×zaman tek çağrı **bulunamadı**; `Histogram(..., "severity")` servis başına
  (`internal/api/copilot_guided.go:4059` emsali). Takım başına tek `_search`: `TeamRecentLogs`
  `team_logs.go:368` (ErrorCount, Services, Patterns ≤5, 60 dk tavan `:48`; takım alanı yoksa `ErrTeamLogsDisabled`).
- Şablon çıkarımı Drain değil: `NormalizeSignature` `internal/logstore/signature.go:69`; örnekleme
  `GroupBySignatureN` `patterns.go:167`; yeni-vs-taban `JoinPatternBaseline` `:82`; Drain ayrı paket `internal/templater`.
- Önbellek: `/api/logs/patterns` 30 s `internal/api/api_logs_patterns.go:115`; takım logları 60 s `internal/api/copilot_team_logs.go:204`.

## 5. Problem ve rollout kaynakları

- Problem: §1 kalem 6; HTTP `/api/problems*`; servis eşlemesi `problems.service` ∈ kapsam (`ProblemFilter.Services`).
- Rollout: §1 kalem 7; servis eşlemesi `workload_revision_activity_1m` MV üzerinden
  `RolloutRefsForService` (`rollout_problem_telemetry.go:56`); deploy olayı `GetServiceDeploys` span
  çıkarımı ∪ `events kind='deploy'`; kümülatif `readTeamSummary` `copilot_team_summary.go:256`
  (Problems/Deploys/Exceptions tek demet — briefing kalem 6+7 için en yakın hazır fonksiyon).

## 6. v2 akışı

- `start` + `start-step` ilk `Translate`'te `internal/cosre/uistream/translator.go:40-47`; `messageId`
  `Options.MessageID` (`emitter.go:14-16,47-51`; ULID `ulid.go:49-50`; prod seam `internal/api/cosre_v2_chat.go:60`).
- `delta{reset}` → `retract` `translator.go:179-195`: `text-end` + `data-reset{textId}` (transient),
  yeni text id `t<N>`; `message-metadata` yalnız `answer()` `:240-242`.
- Golden: `testdata/cosre/ui-stream-v1{,-error,-closed,-reset,-reset2,-approval,-scope}.{events.jsonl,sse}`;
  Go `TestGoldenFixtures` `internal/cosre/uistream/golden_test.go:73`; FE `frontend/src/lib/cosre/uiStreamFixture.test.ts:13`;
  kural `protocol.ts:10-12` (yeni parça adı = aynı commit'te fixture + `DATA_PART_NAMES` `:40-50`).
- Sunucu-başlatan mesaj türü: **bulunamadı** (`KnownKinds` `part.go:10-16`; grep greeting/briefing boş).
- **En az müdahaleci yol:** akışa DOKUNMA. `GET /api/cosre/v2/briefing` hazır bir asistan mesajı
  döndürür: `{id:<ULID>, role:'assistant', parts:[{type:'data-briefing', data}], metadata:{kind:'briefing'}}`;
  FE `DATA_PART_NAMES`'e `'briefing'` + `parseDataBriefing` (fixture dosyası gerekmez; parser birim testi).
  Streaming istenirse ileride tek fixture çifti `ui-stream-v1-briefing`. Persist tuzakları:
  `toRequestMessages` `frontend/src/lib/cosre/cosrePersist.ts:113` (modele gider), `toPersistMessages` `:52`
  (arşive gider), `fromPersistMessages` `:81` (parçalar düşer) → `metadata.kind==='briefing'` üçünde de süzülmeli.

## 7. Lider kilitli periyodik worker

- Yardımcı: `cache.NewLeaderHolder(lock,key,ttl)` `internal/cache/leader.go:104`, `LeaderTTL` `:87`, `IsLeader` `:128`;
  Redis yoksa noop (her pod lider) `main.go:605-628`.
- Anomali: `lockKey` `internal/anomaly/anomaly.go:29`, `New` `:451`, ilk+tik `runIfLeader` `:480-486`; `main.go:717` (`if mode.worker` `:705`).
- Kanonik emsal: `StartOperationCatalog(ctx,lock)` `internal/api/cosre_operation_catalog.go:21` (10 dk `:18`,
  leader holder + tik kapısı + `WithQueryTag`); takılma noktası `main.go:1598` yanı (`go srv.StartBriefingProducer(ctx, lockImpl)`),
  yeni dosya `internal/api/cosre_briefing_worker.go` (`api.go` büyümez). Takım listesi: `users.team` distinct
  (`internal/chstore/store.go:4005` kolonu) ∩ `ReadTeamCatalogue` `team_ownership.go:316` — kullanıcısı olmayan takım üretilmez.

## 8. cosre_signals

- DDL `internal/chstore/store.go:2996`: `exchange_id, created_at, user_email, kind LC, detail`; MergeTree, `ORDER BY (created_at, kind)`,
  TTL 180 g; `source/surface` kolonu **bulunamadı**. Yazım `InsertCosreSignal` `internal/chstore/cosre_quality.go:23`,
  okuma `ListCosreSignals` `:85`; rota `POST /api/ai/signal` `internal/api/cosre_quality.go:47`; kinds
  `cosrelearn.ClientSignalKinds` `internal/cosrelearn/signals.go:53`, sistem `:71`; FE `api.aiSignal` `frontend/src/lib/api.ts:1255`, tip `frontend/src/lib/types.ts:8261`.
- Satır-bazlı `source=briefing` uzantısı — **DDL'siz:** `exchange_id` = briefing ULID; yeni istemci kinds
  `briefing_line_click | briefing_useful | briefing_noise | briefing_dismiss`; `detail` JSON
  `{line:n, item:"red|client|exception|log|oracle|problem|rollout", target}`; gece işi kind ile süzer.
  Kolon seçeneği (`source LowCardinality(String) DEFAULT ''`) iki-boot sözleşmesi ister (`/clickhouse-schema` §3) — gerekmiyor.

## 9. Frontend

- Ağaç: `AppShell.tsx:287` → `CopilotChatGate.tsx:37,115` → `CosreChatV2` `frontend/src/components/cosre/conversation/CosreChatV2.tsx:70-281`
  → `MessageList.tsx:50-71` → `AssistantMessage` `:78-110` (`toChatTurn` `:86` → v1 `ChatBubble`).
- Boş durum: `CosreChatV2.tsx:261-267` `messages.length===0 ? <CosreWelcome/> : <MessageList/>`; `CosreWelcome`
  `frontend/src/components/CopilotChat.tsx:668`; selam verisi `useCosreWelcomeData` `frontend/src/components/ai/cosreAsk.ts:217-228`
  (`['cosre-greeting',env]`, `enabled = open && boş` `CosreChatV2.tsx:98`) → briefing sorgusu için hazır tetik.
- Metadata'ya göre kart seçimi: **bulunamadı** — `MessageList.tsx:57-61` yalnız `role` anahtarlar. Kart ailesi
  düz `frontend/src/components/ai/` (ServiceReportCard, EvidenceCard, LogSpikeCard…); `components/cosre/evidence/` yok.
- İstek bağlamı `requestContext.ts:22-45` (service/env/rangeS/trace/scope/command…); açılışta POST gitmez →
  briefing `briefingId`'yi bağlama ekler, sunucu `userScopeAddendum` gibi ek üretir (sahte geçmiş yok).

## 10. Riskler

| # | Boşluk | Etki |
|---|---|---|
| R1 | Service sayfasında Errors/Exceptions sekmesi yok; kalem 3 "sekmeyle aynı sayı" → Inbox/`/api/exception-groups` yolu (`internal/api/exception_priority_sort.go:130`) | **degrade** — kaynak Inbox olur, pivot `/problems` |
| R2 | Exception "artan tip" pencere agregatı yok | **bloke (kalem 3 yarısı)** — grup başına `GetExceptionOccurrences` N sorgu ya da yeni rollup (`/clickhouse-schema` kararı) |
| R3 | Kafka iki yol: Messaging sekmesi FE PromQL `kafkaPanels` `kafkaMetrics.ts:76` vs Infra paneli Go `KafkaServiceQuestions`; pin testi yok | **degrade** — Go yolunu seç, sekme eşitliği Q9 |
| R4 | HTTP client metrikleri Go'da yok; gRPC/HTTP yalnız server ailesi | **degrade** — kalem 2 "server 5xx/non-OK + p95" ile başlar |
| R5 | Oracle çözülmemiş özne (`ext:/oracle:<kaynak>`) takım kapsamı dışı; CH `oracle_error_log` servis kolonu yok | **degrade** — çözülmüş `ora:` grupları üzerinden; eksik oran raporlanır |
| R6 | `list_my_services` env + açık Problem sayısı yok [o] `v2_tools.go:300-310` | **bloke (kalem 6)** — Faz 1'de `open_problems` ile birlikte (dilim 2 borcu olarak ayrı kapatılmaz) |
| R7 | ES takım log penceresi 60 dk, CH logstore unsupported, takım alanı şart | **degrade** — 3×60 dk ya da tavan artışı; CH'de kalem 4 boş satır |
| R8 | Akışta sunucu-başlatan mesaj yok; persist/istek yolu enjekte mesajı taşır | **degrade** — GET + metadata süzgeci (§6) |
| R9 | Worker maliyeti: takım × 7 kalem × 5 dk; Oracle `GroupStatsCache` 30 s blob | **degrade** — kademeli; takım başına ≤ 15 CH + ≤ 6 VM + 1 ES |
| R10 | Dilim 3 main'de yok (§12) | **bloke (Faz 1 başlangıcı)** |

## 11. Service Overview sekmeleri — paylaşılan sorgu yolları + pivot URL

Sayfa `frontend/src/pages/Service.tsx` (`/service` `App.tsx:174`); sekmeler `:844-852`:
`overview | operations | details | infra | gitops | http (HTTP/GRPC) | messaging | topology | logs`.
**Errors/Exceptions, gRPC, Deploys ayrı sekme olarak bulunamadı** (gRPC `http` içinde; deploy Overview `useServiceDeploys` `Overview.tsx:21`;
exception yalnız `ServiceAttentionStrip` `Service.tsx:563` → `api.inbox` `ServiceAttentionStrip.tsx:102` → `internal/api/inbox.go:185` → `ListExceptionGroups` `:658,674`).

| Sekme | FE → api.ts → rota → handler → paylaşılan Go fonksiyonu (briefing BUNU çağırır) |
|---|---|
| Overview RED | `api.serviceMetricRED` `api.ts:529` → `/api/services/{name}/metric-red` `service_metric_red.go:49` → `buildServiceMetricRED` `:330` (VM) · span RED `api.spanMetricBatch` `api.ts:2869` → `QuerySpanMetricMulti` `internal/chstore/spanmetric.go:1056` · demet `getServiceBundle` `api.go:2269` |
| HTTP/GRPC | `useHttpPromql` `frontend/src/lib/queries/httpMetrics.ts:25` → `/api/metrics/promql` `internal/api/promql.go:50` (FE `httpQueries` `httpMetrics.ts:158`); aile `useRedFamily` `:60` → `/api/services/{name}/red-family` `service_red_family_route.go:36` → `resolveServiceREDFamily` `red_family.go:297`. Sunucu eşdeğeri: `readServiceHTTPRED` `copilot_service_http_red.go:205` (etiket pin `http_labels_test.go:116`) |
| Messaging | `ServiceMessagingTab.tsx:18,33,47` FE PromQL `kafkaPanels` `kafkaMetrics.ts:76` → `/api/metrics/promql`. Sunucu eşdeğeri: `runKafkaQuestions(…,KafkaServiceQuestions())` `messaging_metric.go:318` (Infra paneli `/kafka-clients` `:62`) |
| Infra | `api.servicePods` `api.ts:2100` → `entities.go:386` → `EntitySeenForService` `entity_queries.go:369`; Kafka paneli yukarıda |
| Operations | `api.serviceOperations` `api.ts:3147` → `api.go:869` → `GetOperationSummaryCompared/GetOperationSummary` `internal/chstore/repo.go:1334/1435` |
| Logs | `api.logs` `api.ts:865` → `POST /api/logs/search` `logs_routes.go:20` → `postLogsSearch` `api_logs.go:319` → `logstore.SearchWithTimeout` `:465` |
| GitOps / Topology | `service_gitops.go:66,158` · `service_neighbors.go:43` |
| Deploys (Overview içi) | `api.serviceDeploys` `api.ts:2953` → `/api/services/{name}/deploys` → `GetServiceDeploys` `deploys.go:1231` |

Pivot URL: `/service?name=<svc>&tab=<tab>&range=<preset|custom:<fromMs>-<toMs>>[&env=][&cluster=][&op=]`
(`Service.tsx:98` name/service, `useUrlRange.ts:66,144` varsayılan `3h`, `urlState.ts:9-14`, `useUrlEnv.ts:65`);
tek üretici `serviceHref(name,{tab,env,range,params,hash})` `frontend/src/lib/serviceHref.ts:62-76` — Go tarafında
`page_url` aynı şekli yazar (`MyServicesPageURL` emsali [o] `v2_tools.go:219`). Kalem pivotları: 1→`tab` yok (overview),
2→`http`/`messaging`, 3→`/problems` (Inbox), 4→`logs`, 5→`/trace/<id>` (varsa) yoksa `/problems`, 6→`/problems?problem=`, 7→`/rollouts`/`/changes`.

## 12. Dilim 3 bağımlılığı (Faz 1 ön koşulu)

Dilim 3 (`find_traces` / `search_logs` v2 / `explain_trace`) anlık görüntüde ne `origin/main`'de ne yerel
worktree'lerde (**bulunamadı**). Faz 1'in dilim 3'ten aldığı:

| Faz 1 aracı | Dilim 3'ten alınan | Neden |
|---|---|---|
| `exception_diff` | `find_traces` (closed filters + `TracesDeepLink` `internal/mcptools/search_filters.go:158`) | satıra örnek trace + `deep_link`; aynı kapsam enjeksiyonu (`applyUserScope` [o] `chat_user_scope.go:233`) |
| `log_error_diff` | `search_logs` v2 gövdesi (words + `services[]` + `severity_min` + `trace_id`, `logsHref`) | şablon satırının `page_url` ve ES sorgu inşası tek yerde kalır |
| `oracle_error_diff` | `find_traces` (trace pivot) | Oracle satırında trace varsa aynı çip |
| hepsi | `data-tool-result` zarfı `{summary, rows, page_url, source}` (dilim 1, [o] `protocol.ts:14` `CosreToolResult`) | briefing satırı = aynı satır şekli |

Faz 1 dilim 3 main'e indikten SONRA başlar; Faz 0 raporu bağımsızdır.

## Tasarım önerisi

**Araç imzaları** (mevcut sarmalanır, yeniden yazılmaz; hepsi `services[]` + `window_s`(=10800) + `env` alır, `{summary ≤ 2 KB, rows, page_url, source}` döndürür):

| Araç | Sarar | Satır şekli |
|---|---|---|
| `service_health_summary(services[], env, window_s)` | `ReadTeamServicesRED` ×2 (`PriorWindow`) | `{service, err_rate, err_rate_prev, p95_ms, p95_prev, rps, rps_prev, flags[err↑,p95↑,traffic↓]}` |
| `client_metrics_diff(services[], env, window_s, family∈kafka\|http\|grpc)` | `runKafkaQuestions`/`KafkaServiceQuestions`; `readServiceHTTPRED`; prior = ikinci pencere | `{service, metric, now, prev, delta_pct, label{client_id\|topic\|route}}` → pivot `tab=messaging\|http` |
| `exception_diff(services[], env, window_s)` | `ListExceptionGroups{Services, ActiveFromNs}` + `RecentNewExceptionServices` + `TopErrorOperations` ×2 | `{service, ex_type, state(new\|regressed\|rising), occ_cur, occ_prev?, operation?}` — "rising" R2 kapanınca |
| `log_error_diff(services[], env, window_s)` | `TeamRecentLogs` (≤60 dk dilimler) + `GroupBySignatureN`+`JoinPatternBaseline` | `{service, level, count, prev, new_templates[≤3]{template, count}}` |
| `oracle_error_diff(services[], window_s)` | `ListExceptionGroups` (`ora:` önek) + `OracleGroupAggregates` ×2; jenerik kod `QualifierFor` ayrı | `{operation_code, error_code, generic:bool, count, prev, service, trace_id?}` — jenerik tek başına satır üretmez |
| `open_problems(services[], env)` | `ListProblems{Services, Env, NotStatuses:[resolved]}` (+ `list_my_services`'e `env` ve `open_problems` alanı — R6) | `{id, displayId, priority, service, kind, reason, since}` |
| `recent_rollouts(services[], window_s)` | `GetServiceDeploys` + `ComputeDeployImpact` | `{service, version_from→to, at, err_before, err_after, delta_pts, source}` |

Üretim: saf `briefingFromFindings(findings) → lines[≤7] | silence` (sıra sabit 1→7; eşikler config) → LLM yalnız
cümleleştirir (`copilot` üzerinden, `ai_calls.surface="briefing"`; kapalıysa deterministik metin). Sayılar
LLM'den geçmez — kart satırı araç satırını gösterir, metin yalnız başlık.

**`cosre_briefings` (CH, `/clickhouse-schema` §1 "uygulama state'i")** — RMT(version), FINAL okuma, ORDER BY = dedup anahtarı
münhasıran (O3), PARTITION BY yok (P1), Nullable yok (C4), LC yalnız ölçülü düşük-distinct (C2/C3):

| Kolon | Tip | Not |
|---|---|---|
| `team_key` | `String` | `CanonTeam` çıktısı — ORDER BY (tek kolon); takım sayısı ölçülmedi → LC değil (C3) |
| `env` | `LowCardinality(String) DEFAULT ''` | mevcut `env` kolonlarıyla aynı sınıf; ORDER BY'a girer: `(team_key, env)` |
| `scope_hash` | `String` | `fnv(sorted services)` — kapsam değişimini kartta gösterir |
| `window_s`, `generated_at` | `UInt32`, `DateTime64(3)` | kart "N dk önce" |
| `lines` | `String CODEC(ZSTD(3))` | JSON `[{item, service, text, rows, page_url}]` ≤ 7 |
| `silence` | `UInt8` | 1 = "sessizlik" satırı |
| `briefing_id` | `String` | ULID; `cosre_signals.exchange_id` ile birleşir |
| `version` | `UInt64 DEFAULT toUnixTimestamp64Nano(now64(9))` | — |

TTL `toDate(generated_at) + 7 DAY` (geri bildirim birleşimi için yeter). Alternatif: Redis `cache.Cache` (TTL 10 dk) — geçmiş/geri
bildirim birleşimi istenmiyorsa tablo açılmaz (Q-open). Tek dağıtık hususu yok (yüksek hacim değil, O5 dışı).

**Config** — `system_settings["cosre_briefing"]` (`cosre_v2` `internal/api/cosre_v2_flag.go:40` ve `ai.userScope` yanında; `LoadPersisted` boot, admin PUT + audit `settings.cosre_briefing.update`; `GET/PUT /api/ai/briefing`, viewer GET görür):
`{enabled:false, windowS:10800, refreshS:300, maxLines:7, env:"", llmPhrasing:true, thresholds:{errRateX:2, p95X:1.5, trafficDropPct:50, logLevelX:2, oracleCodeX:2}, minSpansForRatio:200, oracleGenericSeparate:true}` — eşikler yalnız daraltır, sabitler tavan.

**SSE değişikliği:** Faz 1'de **yok**. `GET /api/cosre/v2/briefing` (kullanıcı → takım → `cosre_briefings FINAL`; `serveCached` anahtarı §2 hash;
neden ≠ team → `{scope:{unscoped, reason}}` tek satır). FE `DATA_PART_NAMES += 'briefing'`, `CosreBriefing` tipi `lib/types.ts`, `api.cosreBriefing(env)`.
Streaming'e geçilirse: v1 olay `briefing` → `data-briefing{id:"briefing"}` + `KnownKinds` + fixture `ui-stream-v1-briefing` (tek çift).

**UI kartı:** boş durumda (`CosreChatV2.tsx:261-267`) `CosreWelcome` içinde selam satırının yerine `BriefingCard`
(`components/ai/BriefingCard.tsx`): başlık "Son 3 sa · <takım> · N servis · X dk önce"; 4–7 satır, her satır
`[kalem rozeti] metin → pivot çipi (serviceHref)`; sessizlikte tek satır; satır sonunda `useful/noise` sinyal çipi
(`api.aiSignal(briefing_id, kind, detail)`); "Sor" tıklaması composer'a satırı prefill eder ve `context.briefingId` gönderir.
`unscoped` → tek satır "Ekibin tanımlı değil — hangi takımdasın?" (mevcut takım seçici `CosreWelcome`'da).

**Dilim planı** (her biri kendi `v0.10.X`): 0 dilim 3 main'de (ön koşul) → 1 `open_problems` + `list_my_services` env/açık sayı (R6) →
2 saf `briefingFromFindings` + `service_health_summary`/`recent_rollouts` + `cosre_briefings` + worker + GET (LLM'siz, sessizlik satırı dahil) →
3 FE kart + `'briefing'` parçası + sinyal kinds → 4 `client_metrics_diff` + `oracle_error_diff` + `log_error_diff` → 5 `exception_diff`
(R2 kararıyla) → 6 LLM cümleleştirme + evalset fikstürü → 7 `context.briefingId` eki.

## Operatör soruları (12)

| # | Soru | Öneri |
|---|---|---|
| Q1 | Kart boş durumda mı (welcome yuvası) yoksa enjekte asistan mesajı mı? | **welcome yuvası** — persist/model-geçmişi tuzakları yok (§6) |
| Q2 | Pencere 3 sa, yenileme 5 dk? | **evet**; worker maliyeti R9 ölçülür, sonra 2 dk'ya inilebilir |
| Q3 | Env: takım başına tek briefing mi, env başına mı? | **sayfanın env'i** (`ask.env`); worker `env=""` + prod üretir |
| Q4 | RED eşikleri sabit kat mı (2× / 1.5× / −%50) yoksa median+MAD? | **sabit kat Faz 1**, MAD Faz 2 |
| Q5 | Exception "artış" için yeni pencere agregatı açılsın mı? | **hayır Faz 1** — yalnız new/regressed; R2 ayrı `/clickhouse-schema` kararı |
| Q6 | Çözülmemiş Oracle özneleri (`ext:/oracle:`) kartta görünsün mü? | **tek filo satırı olarak, takım satırlarından ayrı** |
| Q7 | ES 60 dk tavanı: 3 dilim mi, tavan artışı mı? | **3 dilim** (tavan ES koruması) |
| Q8 | LLM cümleleştirme varsayılan açık mı? | **açık, model yoksa deterministik** |
| Q9 | Kafka "sekme sayısı": Messaging (FE PromQL) mi Infra paneli (Go) mi? | **Go yolu**; Messaging sekmesine aynı sorgunun pin testi ayrı iş |
| Q10 | Satır başı useful/noise çipi Faz 1'de mi? | **evet** — `cosre_signals` DDL'siz |
| Q11 | HTTP client (giden) metrikleri Go'ya taşınsın mı? | **Faz 2**; Faz 1 server aileleri |
| Q12 | >100 servisli takım: kırpılmış küme üzerinden mi, uyarı mı? | **kırpılmış + kartta "N servis dışarıda"** |

## Onay

- 2026-10-30: Q1–Q12 öneriler olduğu gibi onaylandı; Faz 1 başladı (v0.10.1690).
- Q10 operatör 2026-10-31 geri aldı: satır geri bildirimi yok (cosre_signals'a briefing kinds eklenmez).
- 2026-10-31: kalem 6 / `open_problems` kaynağı problems tablosu değil, Service şeridiyle aynı Inbox boru hattı (`inboxView`; v0.10.1691).
