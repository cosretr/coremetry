-- 0015_rollouts_v2_rollback.sql — 0015'i geri alır. OPERATÖR UYGULAR.
-- v0.10.960 — Rollouts v2 P1.8 (docs/rollouts/v2-audit.md §10.5). Admin →
-- ClickHouse → Rollouts katmanı sihirbazının 0015 geri alma yolu
-- (POST /api/admin/rollout-layer/rollback-0015, confirm:true) bu dosyayı
-- GÖMÜLÜ olarak ifade ifade koşar (uptrace_all token'ı gerçek küme adıyla);
-- elle koşmak da geçerli. Boot ASLA koşmaz (v0.9.613).
--
-- NE GİDER: sekiz Rollouts v2 state tablosu VERİSİYLE. v1 nesnelerine
-- (workload_rollouts, rollout_reconcile_runs, workload_revision_activity_1m)
-- ve span'lere DOKUNULMAZ — onların geri alması 0012_rollout_layer_rollback.sql.
--
-- ⚠ argocd_sync_events tarihçesi geri gelmez. Argo CD Application başına
-- yalnız son 10 geçmiş kaydını tutar (status.history); Coremetry bu sınırın
-- ötesindeki TEK kayıttır (karar 24 — bu yüzden purge'da da korunur). Tablo
-- düşünce eski senkron operasyonları hiçbir kaynaktan yeniden kurulamaz.
-- Öteki yedisi türev veridir: işçiler açıkken KSM / Argo metriklerinden /
-- Azure DevOps'tan yeniden dolar (rollout_events tarihçesi yalnız KSM'nin
-- bugün gösterdiği kadar geri gelir).
--
-- ÖNCE işçileri kapat (system_settings["rollouts"].source=v1, argocd /
-- enrichment kapalı): açık bir işçi düşen tabloya yazarken UNKNOWN_TABLE
-- alır. Boot sekiz tabloyu bir sonraki açılışta YENİDEN KURAR
-- (rollout_v2_schema.go, `tables` dilimi) — bu dosya hiçbir kipte kalıcı
-- bir kaldırma değildir:
--   - UYGULAMA YÖNETİMLİ kurulumda (tek düğüm ya da cluster_name dolu)
--     boot'un kendi şekliyle (küme kipinde ON CLUSTER + Replicated).
--   - DIŞ Distributed + COREMETRY_CH_ALLOW_UNSET_CLUSTER'da (cluster_name
--     BOŞ) bağlanılan İLK host'a Replicated OLMAYAN düz kopya olarak.
--     0015'i yeniden basmadan önce o boş kopyaları YALNIZ o host'ta, ON
--     CLUSTER OLMADAN `DROP TABLE <ad> SYNC` ile düşür (preflight-0015
--     onları çakışma diye listeler — 0015 başlığı, ÖN KOŞUL). Arada bir pod
--     yeniden başlarsa boot onları yine kurar: düşürdükten hemen sonra uygula.
--
-- SYNC: Atomic DB'de tembel DROP (8 dk) Replicated znode'unu bırakır ve
-- yeniden CREATE 253 REPLICA_ALREADY_EXISTS verir (0012 rollback ölçümü).
-- SIRA: 0015'in TERSİ (tablolar arası bağımlılık yok; okunabilirlik için).

DROP TABLE IF EXISTS rollout_worker_runs ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS ado_commit_enrichment ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS rollout_classification ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS argocd_app_mapping ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS argocd_sync_events ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS argocd_app_status ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS rollout_workload_state ON CLUSTER uptrace_all SYNC;
DROP TABLE IF EXISTS rollout_events ON CLUSTER uptrace_all SYNC;
