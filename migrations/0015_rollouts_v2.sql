-- 0015_rollouts_v2.sql — ROLLOUTS v2 veri katmanı: sekiz state tablosu (dış Distributed prod).
-- v0.10.960 — Rollouts v2 P1.8 (docs/rollouts/v2-audit.md §10; operatör
-- kararları 6, 9, 24, 25 — 2026-09-26). Boot ASLA koşmaz (ev kuralı: ON
-- CLUSTER DDL'i N pod yarıştırırsa kuyruk tıkanır — v0.9.613). Admin →
-- ClickHouse → Rollouts katmanı sihirbazının 0015 yolu
-- (POST /api/admin/rollout-layer/apply-0015; ön kontrol
-- GET …/preflight-0015) bu dosyayı GÖMÜLÜ olarak, operatörün isteğiyle,
-- ifade ifade uygular (uptrace_all token'ı gerçek küme adıyla; ilk hatada
-- durur); elle koşmak da geçerli.
--
-- ============================================================
-- GEREKÇE
-- ============================================================
-- Uygulama aynı sekiz tabloyu KENDİ yönettiği kurulumlarda boot'ta kurar
-- (internal/chstore/rollout_v2_schema.go, store.go `tables` dilimi; tek
-- düğümde düz ReplacingMergeTree, cluster_name doluysa adaptDDL ile ON
-- CLUSTER + ReplicatedReplacingMergeTree). DIŞ Distributed prod'da
-- (cluster_name BOŞ) boot ya hiç başlamaz ya da ALLOW_UNSET_CLUSTER ile
-- tabloları bağlandığı İLK host'a Replicated OLMAYAN kopya olarak kurar —
-- replikalı şemanın sahibi bu dosya, OPERATÖR.
--
-- Tanımlar rollout_v2_schema.go ile BAYT BAYT aynı (0012 sözleşmesi,
-- 0012_rollout_layer.sql:11-17) — yorumlar ve girinti DAHİL. Tek fark
-- BELGELENMİŞ küme uyarlaması:
--   (1) tablo adının hemen ardına `ON CLUSTER uptrace_all`;
--   (2) `ENGINE = ReplacingMergeTree(version)` →
--       `ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/<ad>', '{shard}-{replica}', version)`.
-- rollout_layer_admin_test.go (TestRolloutV2MigrationByteIdenticalToBootDDL)
-- bu dosyayı Go DDL'inden türetip karşılaştırır: Go tarafında tek bayt
-- değişirse (kolon, yorum, TTL günü) test düşer — BU DOSYAYI ELLE DEĞİL,
-- Go DDL'iyle BİRLİKTE güncelle.
--
-- ZK YOLU — karar 25. Bu dosya '/clickhouse/tables/state/<ad>' SABİT yazar
-- (0012 gibi). Boot ise öneki ve kuşağı ÇALIŞMA ZAMANINDA çözer
-- (internal/chstore/state_replication.go: zkPrefix = cfg.ReplicaPath, boşsa
-- /clickhouse/tables; useUnifiedStatePath = kümede gözlenen yol). Varsayılan
-- önek + taze ya da 0009 sonrası kümede ikisi AYNI ifadeyi üretir (test
-- pinli). Ayrıştığı iki durum, sihirbazın ön kontrolünde REDDEDİLİR:
--   - küme kipinde özel önek (cfg.ReplicaPath): bu dosyadaki yol boot'un
--     kuşak probe'una "eski yol" görünür ve SONRAKİ yeni state tablolarını
--     shard'lı yola düşürürdü → dosyayı öneke uyarlayıp elle uygula;
--   - sekizden biri bir host'ta FARKLI ZK yolunda (0009 öncesi kümede boot
--     onu eski '<önek>/{shard}/<ad>','{replica}' yoluyla kurmuş olabilir):
--     IF NOT EXISTS eksik host'lara 0015 yolunu verir → iki replikasyon
--     grubu (split-brain). Önce o tabloyu ON CLUSTER düşür ya da boot'un
--     yoluyla hizala.
--
-- CODEC YOK (§10.2, v1 state tabloları gibi) → C1'in ZSTD(1)/ZSTD(3)
-- ayrışması burada yok. PARTITION YOK (Kural P1 oluşamaz), Nullable YOK,
-- LowCardinality yalnız yapısal olarak sınırlı kolonlarda (C2/C3).
--
-- ============================================================
-- ÖN KOŞUL — ŞEMA DOĞRULAMA (sihirbazın ön kontrolü aynısını sorar)
-- ============================================================
--   SELECT cluster, count() FROM system.clusters GROUP BY cluster;
--     → uptrace_all 4 host (2 shard × 2 replica) beklenir; farklıysa
--       aşağıdaki `uptrace_all` token'ını düzenle.
--   SELECT hostName(), name, engine FROM clusterAllReplicas('uptrace_all', system.tables)
--   WHERE database = currentDatabase() AND name IN ('rollout_events','rollout_workload_state',
--     'argocd_app_status','argocd_sync_events','argocd_app_mapping','rollout_classification',
--     'ado_commit_enrichment','rollout_worker_runs');
--     → boş (taze) ya da hepsi Replicated*. Replicated OLMAYAN satır =
--       boot'un (ALLOW_UNSET_CLUSTER) o host'a kurduğu düz kopya: IF NOT
--       EXISTS orada no-op kalır. Boşsa (count() = 0) o host'ta ON CLUSTER
--       OLMADAN `DROP TABLE <ad> SYNC`, sonra bu dosya.
--   SELECT hostName(), table, zookeeper_path FROM clusterAllReplicas('uptrace_all', system.replicas)
--   WHERE database = currentDatabase() AND table IN (… aynı sekiz ad …);
--     → her satır '/clickhouse/tables/state/<table>' olmalı.
--
-- ============================================================
-- UYGULAMA SIRASI
-- ============================================================
-- Tablolar arası bağımlılık yok (MV yok, Distributed sarmalayıcı yok —
-- state tabloları TEK replikasyon grubunda, 0009 şekli; her host tam kopya).
-- Sıra §10.3 sırası (rollout_v2_schema.go rolloutV2TableDDLs ile aynı).
-- Yazıcılar (§10.4) bu committe YOK — tablolar boş doğar; işçiler P2/P3/P4.
--
-- Geri alma: 0015_rollouts_v2_rollback.sql (sekiz tablo, ters sıra, SYNC;
-- argocd_sync_events tarihçesi GERİ GELMEZ — Argo yalnız son 10 kaydı tutar).
--
-- TUZAK (okuyucular, P3.4): rollout_classification'ın `final` kolonu CH'de
-- geçerli bir ad ama FINAL değiştiricisiyle karışır — okurken
-- `FROM rollout_classification FINAL WHERE final = 0` yaz; tablo adının
-- hemen ardındaki küçük harfli `final` DEĞİŞTİRİCİ olarak ayrıştırılır.

-- ── ADIM 1/8: rollout_events (§10.3.1 — rollout başına tek satır; yazıcı: rollout-detector) ──
CREATE TABLE IF NOT EXISTS rollout_events ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/rollout_events', '{shard}-{replica}', version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL 180 DAY;

-- ── ADIM 2/8: rollout_workload_state (§10.3.2 — dedektörün kalıcı belleği; yazıcı: rollout-detector) ──
CREATE TABLE IF NOT EXISTS rollout_workload_state ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/rollout_workload_state', '{shard}-{replica}', version)
ORDER BY (cluster_id, namespace, workload_kind, workload)
TTL toDate(last_seen_at) + INTERVAL 400 DAY;

-- ── ADIM 3/8: argocd_app_status (§10.3.3 — Argo durum değişim kaydı; yazıcı: argocd-metrics) ──
CREATE TABLE IF NOT EXISTS argocd_app_status ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/argocd_app_status', '{shard}-{replica}', version)
ORDER BY (instance_id, app_namespace, app_name, changed_at)
TTL toDate(changed_at) + INTERVAL 180 DAY;

-- ── ADIM 4/8: argocd_sync_events (§10.3.4 — Argo API operasyonları; yazıcı: argocd-api) ──
CREATE TABLE IF NOT EXISTS argocd_sync_events ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/argocd_sync_events', '{shard}-{replica}', version)
ORDER BY (instance_id, app_namespace, app_name, op_started_at)
TTL toDate(op_started_at) + INTERVAL 180 DAY;

-- ── ADIM 5/8: argocd_app_mapping (§10.3.5 — workload ↔ Application kenarları; yazıcı: argocd-metrics) ──
CREATE TABLE IF NOT EXISTS argocd_app_mapping ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/argocd_app_mapping', '{shard}-{replica}', version)
ORDER BY (cluster_id, namespace, workload_kind, workload, instance_id, app_namespace, app_name)
TTL toDate(last_verified_at) + INTERVAL 30 DAY;

-- ── ADIM 6/8: rollout_classification (§10.3.6 — rollout başına tetikleyici; yazıcı: argocd-metrics) ──
CREATE TABLE IF NOT EXISTS rollout_classification ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/rollout_classification', '{shard}-{replica}', version)
ORDER BY (cluster_id, namespace, workload_kind, workload, incarnation_at, generation)
TTL toDate(started_at) + INTERVAL 180 DAY;

-- ── ADIM 7/8: ado_commit_enrichment (§10.3.7 — commit → PR → yazar → pipeline; yazıcı: ado-enrichment) ──
CREATE TABLE IF NOT EXISTS ado_commit_enrichment ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/ado_commit_enrichment', '{shard}-{replica}', version)
ORDER BY (repo_url_norm, sha)
TTL toDate(first_requested_at) + INTERVAL 180 DAY;

-- ── ADIM 8/8: rollout_worker_runs (§10.3.8 — v2 işçilerinin koşu kaydı; her işçi kendi satırı) ──
CREATE TABLE IF NOT EXISTS rollout_worker_runs ON CLUSTER uptrace_all (
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
) ENGINE = ReplicatedReplacingMergeTree('/clickhouse/tables/state/rollout_worker_runs', '{shard}-{replica}', version)
ORDER BY (worker, started_at, host)
TTL toDate(started_at) + INTERVAL 30 DAY;
