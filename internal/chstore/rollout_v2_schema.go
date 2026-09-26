package chstore

import "fmt"

// rollout_v2_schema.go — v0.10.959 — ROLLOUTS v2 veri katmanı, P1.7
// (docs/rollouts/v2-audit.md §10; operatör kararları 6, 9, 24 — 2026-09-26).
//
// ── SEKİZ STATE TABLOSU ───────────────────────────────────────────────────
//
//   rollout_events          rollout başına TEK satır, yerinde güncellenir
//                           (KSM doğruluk kaynağı). Anahtar karar 9:
//                           (cluster_id, namespace, workload_kind, workload,
//                           incarnation_at, generation) — sil/yeniden kur
//                           tarihçeyi ezemesin (§4.3).
//   rollout_workload_state  dedektörün kalıcı belleği (baseline, bekleyen
//                           generation, bilinen revizyonlar). İlk koşu
//                           YALNIZ buraya yazar, olay üretmez.
//   argocd_app_status       Argo uygulama durumunun DEĞİŞİM kaydı (yalnız
//                           tuple değişince ya da senkron bitince satır).
//   argocd_sync_events      Argo API operasyonları; Running ve sonraki
//                           Succeeded aynı anahtarı paylaşır, RMT birleştirir.
//   argocd_app_mapping      workload ↔ Application kenarları (workload-önce,
//                           uygulama anahtarda — bir namespace'te çok uygulama).
//   rollout_classification  rollout başına tetikleyici (argo_auto |
//                           argo_manual | out_of_band | unknown); anahtar
//                           rollout_events ile aynı.
//   ado_commit_enrichment   commit → PR → yazar → pipeline, (repo_url_norm, sha).
//   rollout_worker_runs     dört v2 işçisinin ortak koşu kaydı; `worker`
//                           anahtarda (0 seri / truncated / partial ayrımı —
//                           v1 rollout_reconcile_runs'ın kör noktası).
//
// Ev kuralları (/clickhouse-schema §1, §4; audit §10.2):
//   - RMT(version) + FINAL; version DEFAULT toUnixTimestamp64Nano(now64(9)),
//     ama her yazım AÇIK istemci version'ı taşır (Replicated insert-dedup
//     dersi, ai_eval_runs / settings.go).
//   - ORDER BY = dedup anahtarı, MÜNHASIRAN (O3). PARTITION YOK (Kural P1
//     bu tablolarda oluşamaz). Nullable YOK (C4, sentinel '' / 0 / epoch).
//   - LowCardinality yalnız yapısal olarak sınırlı kolonlarda (C2/C3);
//     ölçülmemiş adlar (workload, app_name, sha …) düz String.
//   - CODEC YOK — v1 state tabloları gibi (rollout_schema.go); böylece
//     migrations/0015 buradaki metinle birebir kalır (0012 sözleşmesi).
//   - TTL `toDate(x) + INTERVAL N DAY` (P2); günler karar 6. TTL çapası
//     §10.3'teki gibi DEFAULT'suz (bildirilmiş sentinel yok) — ama CH'de
//     DEFAULT'suz ≠ ZORUNLU: INSERT kolonu atlarsa tip varsayılanı
//     1970-01-01, Go sıfır time.Time bağlanırsa (chDateTime64Arg
//     '0001-01-01') 1970 ya da öncesi yazılır ve satır ilk TTL
//     birleşmesinde SESSİZCE düşer. Koruma ŞEMADA DEĞİL, YAZICIDA —
//     YAZICI SÖZLEŞMESİ (P2/P3/P4; E2, §10.2): her tam-satır yazım TTL
//     çapasını (started_at, last_seen_at, changed_at, op_started_at,
//     last_verified_at, first_requested_at; rollout_worker_runs.started_at
//     her işçide) sıfır-olmayan değerle taşır; yazıcı batch'ten ÖNCE
//     sıfır/epoch çapalı satırı reddeder (saf doğrulayıcı + test, yazıcının
//     kendi commit'inde). CHECK CONSTRAINT bilinçli EKLENMEDİ: repo emsali
//     yok, tek kötü satır bütün INSERT bloğunu reddeder, §10.3 DDL'i olduğu
//     gibi kalır (istenirse ayrı operatör kararı).
//   - DateTime64(3) her yerde; bağlama tz'siz toDateTime64(?,3,'UTC').
//
// Yazıcı matrisi (§10.4) — TABLO BAŞINA TEK YAZICI, tam satır:
//   rollout_events, rollout_workload_state          → rollout-detector lideri
//   argocd_app_status, argocd_app_mapping,
//   rollout_classification                          → argocd-metrics lideri
//   argocd_sync_events                              → argocd-api (lockDegraded'da atlar)
//   ado_commit_enrichment                           → ado-enrichment (lockDegraded'da atlar)
//   rollout_worker_runs                             → her işçi, kendi `worker` değeriyle
// Bu committe OKUYUCU / YAZICI / İŞÇİ YOK — yalnız boot şeması (P1.7).
//
// ── BOOT DAVRANIŞI (migrate → canonicalTables `tables` dilimi) ─────────────
//
//   Tek düğüm (uygulama sahibi): adaptDDL ifadeyi değiştirmez; planDDL var
//     olanı eler, yoksa CREATE senkron koşar (erteleme yok).
//   Küme kipi (cluster_name dolu): hiçbiri shard kayıtlarında değil →
//     stateTableDDL → ON CLUSTER + ReplicatedReplacingMergeTree; ZK yolu
//     state_replication.go'nun kümeden okuduğu kuşağa göre
//     '<önek>/state/<ad>','{shard}-{replica}' (taze / 0009 sonrası) ya da
//     eski '<önek>/{shard}/<ad>','{replica}'. spans varsa DDL arka plana
//     ertelenir (ddl_defer.go) — boot beklemez.
//   Dış Distributed (spans Distributed, cluster_name BOŞ): boot ya hiç
//     başlamaz (externalDistributedFatal) ya da COREMETRY_CH_ALLOW_UNSET_CLUSTER
//     ile tek-düğüm DDL'i koşar — state tabloları için ATLAMA YOK: sekiz
//     tablo bağlanılan İLK host'ta (ConnOpenInOrder) Replicated OLMAYAN
//     ReplacingMergeTree olarak kurulur (v1 workload_rollouts ile aynı
//     davranış). Prod'un gerçek yolu migrations/0015 sihirbazıdır (P1.8,
//     boot ASLA koşmaz — v0.9.613).
//
// ZK yolu (karar 25): 0015 '/clickhouse/tables/state/<ad>' SABİT yazar;
// boot öneki çalışma zamanında çözer (cfg.ReplicaPath, boşsa
// /clickhouse/tables — state_replication.go zkPrefix). Varsayılan önekte
// ikisi aynı yolu üretir (test pinli); özel önekli kurulumda 0015 elle
// uyarlanır.
//
// Purge (karar 24): yedisi telemetryPurgeTables (türev, yeniden doğar);
// argocd_sync_events configPreserveTables — Argo yalnız son 10 geçmiş
// kaydını tutar, purge edilen senkron tarihçesi geri gelmez.

const (
	rolloutEventsTTLDays         = 180
	rolloutWorkloadStateTTLDays  = 400 // rollout_events'ten UZUN: state düştükten sonraki yeniden kurulum canlı olay satırıyla çakışmasın
	argocdAppStatusTTLDays       = 180
	argocdSyncEventsTTLDays      = 180
	argocdAppMappingTTLDays      = 30
	rolloutClassificationTTLDays = 180
	adoCommitEnrichmentTTLDays   = 180
	rolloutWorkerRunsTTLDays     = 30
)

// rolloutEventsDDL — §10.3.1. Yazıcı: rollout-detector lideri. Argo /
// Azure DevOps / etki verisi KOPYALANMAZ (kendi tek-yazıcılı tablolarında,
// okuma anında birleşir). started_at anahtarda DEĞİL: iki lider farklı
// scrape'te görse de aynı satıra düşer, RMT son version'ı tutar.
var rolloutEventsDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollout_events (
	cluster_id           LowCardinality(String),        -- Remote Cluster EffectiveID(), span değeri DEĞİL
	namespace            LowCardinality(String),        -- workload_rollouts emsali
	workload_kind        LowCardinality(String),        -- Deployment | StatefulSet | DaemonSet | Rollout (ayrılmış: DeploymentConfig)
	workload             String,                        -- C3: ölçülmedi, düz String
	incarnation_at       DateTime64(3),                 -- karar 9: varsa kube_<kind>_created, yoksa ilk tam okumanın KSM örnek zamanı (dakikaya kesik, taşınır)
	generation           UInt64,                        -- rollout'u BAŞLATAN metadata.generation
	started_at           DateTime64(3),                 -- generation'daki ilk örneğin scrape zamanı, bir kez yazılır, taşınır (TTL çapası)
	status               LowCardinality(String),        -- progressing | succeeded | stuck | rolled_back | superseded
	change_type          LowCardinality(String),        -- rollout | config | rollback | initial (scale / yalnız-annotation YAZILMAZ)
	observed_generation  UInt64        DEFAULT 0,
	spec_replicas        UInt32        DEFAULT 0,
	updated_replicas     UInt32        DEFAULT 0,
	available_replicas   UInt32        DEFAULT 0,
	new_revision         String        DEFAULT '',      -- RS adı | STS update_revision | DS controller_revision_hash | boş
	old_revision         String        DEFAULT '',
	images               Array(String),                 -- sıralı, tekil (kube_pod_container_info)
	prev_images          Array(String),
	version_tag          String        DEFAULT '',      -- gösterim sürümü: imaj tag'i, yoksa span effectiveVersionExpr (yalnız etiket)
	stuck_reason         LowCardinality(String) DEFAULT '', -- progress_deadline | timeout | boş
	succeeded_at         DateTime64(3) DEFAULT 0,
	stuck_at             DateTime64(3) DEFAULT 0,
	finished_at          DateTime64(3) DEFAULT 0,       -- succeeded, rolled_back ya da superseded
	note                 String        DEFAULT '',
	updated_at           DateTime64(3) DEFAULT now64(3), -- SSE tail kursörü
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL %d DAY`, rolloutEventsTTLDays)

// rolloutWorkloadStateDDL — §10.3.2. _created denylist'te (CMO), RS yaşı ve
// incarnation zamanı yok; saf-scale tespiti önceki revizyon kümesini ister;
// failover olayı yeniden basmamalı/kaçırmamalı. Yalnız değişimde + günlük
// dokunuşta yazılır.
var rolloutWorkloadStateDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollout_workload_state (
	cluster_id           LowCardinality(String),
	namespace            LowCardinality(String),
	workload_kind        LowCardinality(String),
	workload             String,
	incarnation_at       DateTime64(3),
	generation           UInt64,                        -- işlenen son generation
	observed_generation  UInt64        DEFAULT 0,
	pending_generation   UInt64        DEFAULT 0,       -- observed_generation / şablon kanıtını bekleyen artış
	pending_started_at   DateTime64(3) DEFAULT 0,       -- pending_generation'daki ilk örneğin KSM zamanı, kapı geçince rollout_events.started_at olur (failover'da korunur)
	current_revision     String        DEFAULT '',
	known_revisions      Array(String),                 -- sınırlı (knownRevisionsMax, varsayılan 32): ROLLBACK kanıtı, revisionHistoryLimit'ten uzun yaşar (§4.4 c)
	images               Array(String),
	open_generation      UInt64        DEFAULT 0,       -- açık rollout_events satırının generation'ı (0 = yok)
	first_seen_at        DateTime64(3),
	last_seen_at         DateTime64(3),                 -- başka değişiklik yoksa günde en fazla bir kez dokunulur (TTL çapası)
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload)
TTL toDate(last_seen_at) + INTERVAL %d DAY`, rolloutWorkloadStateTTLDays)

// argocdAppStatusDDL — §10.3.3. Yazıcı: argocd-metrics (60 sn). Son durum =
// FINAL üstünde LIMIT 1 BY (instance_id, app_namespace, app_name). Failover'da
// işçi önceki durumu BURADAN kurar (yoksa ~40k "değişiklik" yeniden basılırdı).
// v0.10.959 — YAZICI SÖZLEŞMESİ (P3): anahtar (uygulama, tik); change_kind
// anahtarda DEĞİL. Aynı tikte aynı uygulamaya iki satır yazılırsa RMT onları
// tek satıra çöker ve düşük version'lı olay SESSİZCE kaybolur. Bu yüzden
// uygulama başına tik başına EN FAZLA BİR satır yazılır. Aynı tikte senkron
// bitişi (argocd_app_sync_total artışı) ile tuple değişimi ya da appeared
// birlikte görülürse TEK change_kind='sync' satırı yazılır; satır yeni
// tuple'ı ve sync_phase'i birlikte taşır (§10.3.4: metrikle görülen senkron
// yalnız burada yaşar; §7.5 metrik-yalnız sınıflandırma bu satıra dayanır).
// P3 yazıcısı bu katlamayı birim testle pinler. ORDER BY'a change_kind
// EKLENMEZ (§10.3 DDL'i olduğu gibi kalır; aynı changed_at'te iki satır
// LIMIT 1 BY son-durum okumasını belirsizleştirirdi).
var argocdAppStatusDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS argocd_app_status (
	instance_id          LowCardinality(String),        -- argocd ayar bloğundaki instance id (onlarca)
	app_namespace        LowCardinality(String),        -- Application CR namespace (§5.2)
	app_name             String,                        -- C3: ~40k'ya dek, ölçülmedi
	changed_at           DateTime64(3),                 -- tik aralığına kesik tik zamanı, lockDegraded altında en fazla bir tik arayla iki kez düşebilir, okuyucu ardışık özdeş satırları katlar
	change_kind          LowCardinality(String),        -- baseline | appeared | state | sync | deleted
	sync_status          LowCardinality(String),        -- Synced | OutOfSync | Unknown
	health_status        LowCardinality(String),        -- Healthy | Progressing | Degraded | Suspended | Missing | Unknown
	operation            LowCardinality(String) DEFAULT '', -- boş | sync | delete (geçici etiket)
	sync_phase           LowCardinality(String) DEFAULT '', -- change_kind=sync: Succeeded | Failed | Error (argocd_app_sync_total)
	autosync_enabled     LowCardinality(String) DEFAULT '', -- true | false | boş (etiket yok, Argo 2.8 ve öncesi)
	project              String        DEFAULT '',
	repo                 String        DEFAULT '',
	dest_server          String        DEFAULT '',      -- ham, eşleme anında normalize
	dest_namespace       LowCardinality(String) DEFAULT '',
	cluster_id           LowCardinality(String) DEFAULT '', -- dest_server -> apiServerUrls -> EffectiveID(), boş = eşlenmedi (sayılır)
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (instance_id, app_namespace, app_name, changed_at)
TTL toDate(changed_at) + INTERVAL %d DAY`, argocdAppStatusTTLDays)

// argocdSyncEventsDDL — §10.3.4. Yazıcı: argocd-api. Metrikle görülen senkron
// bitişleri BURAYA yazılmaz (op_started_at'leri yok) — argocd_app_status'ta
// change_kind='sync'. Coremetry, Argo'nun 10 kayıtlık geçmişinin ötesindeki
// TEK kayıt → purge'da korunur (karar 24, purge.go).
var argocdSyncEventsDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS argocd_sync_events (
	instance_id          LowCardinality(String),
	app_namespace        LowCardinality(String),
	app_name             String,
	op_started_at        DateTime64(3),                 -- operationState.startedAt (TTL çapası)
	phase                LowCardinality(String),        -- Running | Succeeded | Failed | Error | Terminating
	finished_at          DateTime64(3) DEFAULT 0,
	automated            UInt8         DEFAULT 0,       -- initiatedBy.automated
	initiator            String        DEFAULT '',      -- initiatedBy.username olduğu gibi (otomatikte boş)
	sync_revision        String        DEFAULT '',      -- syncResult.revision
	revisions            Array(String),                 -- çok kaynaklı uygulama
	repo_url             String        DEFAULT '',      -- sources[0].repoURL
	target_revision      String        DEFAULT '',
	history_id           Int64         DEFAULT -1,      -- status.history[].id, -1 = geçmişte yok (id 0'dan başlar)
	synced_workloads     Array(String),                 -- Kind/namespace/name, syncResult.resources: yalnız kanıt
	managed_workloads    Array(String),                 -- Kind/namespace/name, GET anında status.resources[]: kesin eşleme girdisi
	dest_server          String        DEFAULT '',
	dest_namespace       LowCardinality(String) DEFAULT '',
	cluster_id           LowCardinality(String) DEFAULT '',
	retry_count          UInt32        DEFAULT 0,
	message              String        DEFAULT '',
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (instance_id, app_namespace, app_name, op_started_at)
TTL toDate(op_started_at) + INTERVAL %d DAY`, argocdSyncEventsTTLDays)

// argocdAppMappingDDL — §10.3.5. Servis değil WORKLOAD anahtarlı (servis ↔
// workload çoka-çok, MV'nin: workload_revision_activity_1m). Yazıcı:
// argocd-metrics içindeki eşleyici adımı; manuel pinler system_settings
// ["argocd"]'dan match_method='manual' olarak kopyalanır → tablo saf türev.
var argocdAppMappingDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS argocd_app_mapping (
	cluster_id           LowCardinality(String),
	namespace            LowCardinality(String),
	workload_kind        LowCardinality(String),        -- namespace düzeyi (zayıf) kenarda boş
	workload             String,                        -- namespace düzeyi (zayıf) kenarda boş
	instance_id          LowCardinality(String),
	app_namespace        LowCardinality(String),
	app_name             String,
	match_method         LowCardinality(String),        -- manual | resource | pod_label | name | namespace
	match_class          LowCardinality(String),        -- exact (manual, resource) | estimated (pod_label, name) | weak (namespace)
	confidence           UInt8,                         -- 0..100
	candidates           UInt16        DEFAULT 1,       -- zayıf kenarda aynı (cluster_id, namespace) altındaki uygulama sayısı
	first_matched_at     DateTime64(3),                 -- taşınır
	last_verified_at     DateTime64(3),                 -- en az günde bir dokunulur (TTL çapası)
	removed_at           DateTime64(3) DEFAULT 0,       -- valid_to sentinel'i (entities emsali), 0 = canlı
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name)
TTL toDate(last_verified_at) + INTERVAL %d DAY`, argocdAppMappingTTLDays)

// rolloutClassificationDDL — §10.3.6. Yazıcı: argocd-metrics içindeki
// sınıflandırıcı; final=0 satırları yeniden değerlendirir (geç gelen API
// kanıtı metrik-tahminini yükseltir). Satır yok = unknown. Rollouts
// `trigger` filtresi LIMIT'ten ÖNCE yarı-join (rolloutWhere) — ikisi de düz
// replike state, GLOBAL IN gerekmez. `final` kolon adı CH'de geçerli
// (FINAL değiştiricisiyle çakışmaz; `clickhouse format` ile doğrulandı) ama
// okuyucu `FROM rollout_classification FINAL WHERE final = 0` yazarken
// değiştiriciyi atlamamalı.
var rolloutClassificationDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollout_classification (
	cluster_id           LowCardinality(String),        -- rollout_events anahtarı ...
	namespace            LowCardinality(String),
	workload_kind        LowCardinality(String),
	workload             String,
	incarnation_at       DateTime64(3),
	generation           UInt64,                        -- ... anahtar sonu
	started_at           DateTime64(3),                 -- TTL ve pencere hesabı için kopya
	trigger              LowCardinality(String),        -- argo_auto | argo_manual | out_of_band | unknown
	basis                LowCardinality(String),        -- api | metrics | none
	confidence           UInt8,                         -- 0..100
	reason               String        DEFAULT '',      -- insan-okur gerekçe (satırla birlikte gider)
	instance_id          LowCardinality(String) DEFAULT '',
	app_namespace        LowCardinality(String) DEFAULT '',
	app_name             String        DEFAULT '',
	match_method         LowCardinality(String) DEFAULT '',
	op_started_at        DateTime64(3) DEFAULT 0,       -- -> argocd_sync_events
	sync_revision        String        DEFAULT '',
	initiator            String        DEFAULT '',
	repo_url             String        DEFAULT '',
	final                UInt8         DEFAULT 0,       -- 1 = pencere kapandı ve girdiler tam
	evaluated_at         DateTime64(3),
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL %d DAY`, rolloutClassificationTTLDays)

// adoCommitEnrichmentDDL — §10.3.7. Anahtar (repo_url_norm, sha): unmapped_repo
// / not_git_sha satırlarının repo_id'si yok ve akış rollout_classification.repo_url
// üzerinden aramasız birleşir. Yazıcı: ado-enrichment. Kimlikler olduğu gibi
// saklanır (ev kuralı: redaksiyon yok).
var adoCommitEnrichmentDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS ado_commit_enrichment (
	repo_url_norm        String,                        -- küçük harf host, userinfo/.git yok, ssh -> https
	sha                  String,
	repo_id              String        DEFAULT '',      -- Azure DevOps repo GUID, unmapped_repo / not_git_sha için boş
	enrichment_status    LowCardinality(String),        -- ok | no_pr | not_found | unmapped_repo | not_git_sha | auth | throttled | error | disabled
	attempts             UInt16        DEFAULT 0,
	next_retry_at        DateTime64(3) DEFAULT 0,
	first_requested_at   DateTime64(3),                 -- taşınır (TTL çapası)
	enriched_at          DateTime64(3) DEFAULT 0,
	project              String        DEFAULT '',
	repo_name            String        DEFAULT '',
	commit_author        String        DEFAULT '',
	commit_author_email  String        DEFAULT '',
	committed_at         DateTime64(3) DEFAULT 0,
	commit_message       String        DEFAULT '',
	is_merge             UInt8         DEFAULT 0,
	pushed_by            String        DEFAULT '',
	pr_id                UInt64        DEFAULT 0,
	pr_title             String        DEFAULT '',
	pr_created_by        String        DEFAULT '',
	pr_closed_by         String        DEFAULT '',
	pr_closed_at         DateTime64(3) DEFAULT 0,
	merge_strategy       LowCardinality(String) DEFAULT '',
	build_number         String        DEFAULT '',
	build_url            String        DEFAULT '',
	reason               String        DEFAULT '',
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (repo_url_norm, sha)
TTL toDate(first_requested_at) + INTERVAL %d DAY`, adoCommitEnrichmentTTLDays)

// rolloutWorkerRunsDDL — §10.3.8. Her işçi yalnız kendi satırlarını yazar
// (`worker` anahtarda → anahtar aralığı başına tek yazıcı); host ayırıcı
// split-brain'de iki pod'un aynı milisaniyede birbirini ezmesini önler
// (rollout_reconcile_runs emsali).
var rolloutWorkerRunsDDL = fmt.Sprintf(`CREATE TABLE IF NOT EXISTS rollout_worker_runs (
	worker               LowCardinality(String),        -- rollout-detector | argocd-metrics | argocd-api | ado-enrichment
	started_at           DateTime64(3),
	host                 LowCardinality(String) DEFAULT '', -- yazan pod (split-brain ayırıcı, v1 emsali)
	finished_at          DateTime64(3),
	status               LowCardinality(String),        -- ok | partial | failed | skipped
	scopes_total         UInt16        DEFAULT 0,       -- cluster ya da instance sayısı
	scopes_ok            UInt16        DEFAULT 0,
	series_read          UInt32        DEFAULT 0,
	truncated            UInt8         DEFAULT 0,
	partial_response     UInt8         DEFAULT 0,
	rows_written         UInt32        DEFAULT 0,
	unmapped             UInt32        DEFAULT 0,       -- kayıtta eşi olmayan dest_server / span değerleri
	api_calls            UInt32        DEFAULT 0,
	api_throttled        UInt32        DEFAULT 0,
	duration_ms          UInt32        DEFAULT 0,
	error                String        DEFAULT '',
	version              UInt64        DEFAULT toUnixTimestamp64Nano(now64(9))
) ENGINE = ReplacingMergeTree(version)
ORDER BY (worker, started_at, host)
TTL toDate(started_at) + INTERVAL %d DAY`, rolloutWorkerRunsTTLDays)

// rolloutV2TableDDLs — sekiz CREATE, §10.3 sırasıyla (store.go `tables`
// dilimine de bu sırayla eklenir). SAF. Testler ve P1.8'in 0015 bayt-eşlik
// testi bu listeyi okur; sihirbazın nesne listesi aynı sırayı izler.
func rolloutV2TableDDLs() []string {
	return []string{
		rolloutEventsDDL,
		rolloutWorkloadStateDDL,
		argocdAppStatusDDL,
		argocdSyncEventsDDL,
		argocdAppMappingDDL,
		rolloutClassificationDDL,
		adoCommitEnrichmentDDL,
		rolloutWorkerRunsDDL,
	}
}
