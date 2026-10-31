# ES log ML katmanı — Faz 0 denetimi (2026-10-31, taban v0.10.1742)

Loglar Collector'dan geçmez, doğrudan ES'e yazılır. Hedef: Elastic ML job'larının
karşılığı worker rolünde, ES'e düşük yükle. Yeni depo/dil/imaj yok. Kod yok; onay bekler.

## 1. Boşluk tablosu

| Sinyal | Bugün | Kanıt |
|---|---|---|
| Servis log hacmi + susma | **YOK.** Hacim/seviye dedektörü hiçbir yerde yok; "traffic loss" yalnız span RED'inde | `internal/anomaly/anomaly.go:318`; `internal/anomaly/*` grep: logVolume/logSilence eşleşmiyor |
| Servis hata oranı (mevsimsel, yönlü) | **YOK** (log tarafı). Mevsimsel taban yalnız metrik dedektöründe | `anomaly.go:1445 seasonalParams`, `:1718 chooseBaseline`, `:1736 medianMAD` |
| Şablon spike | **YOK**, bilinçli ertelenmiş; `log_templates` satırı kümülatif sayaç, pencere sayımı yok | `internal/anomaly/log_templates.go:1-14` |
| Nadir hata kodu | **YOK.** `log_patterns.go` ~20 sabit regex (new/spike), açık uçlu kod yok | `internal/anomaly/log_patterns.go:388,404,473` |
| Aykırı pod | **YOK** log tarafında; exception için var | `internal/anomaly/exception_pods_signal.go` |
| Change point + rollout | Rollout korelasyonu yalnız Problem→deploy; log change point yok | `internal/anomaly/rollout_causes.go:1-6` |
| Elastic ML kayıtları | Var: 5 dk poll, `kind=elastic_ml`, `FingerprintAnomaly(elastic_ml, pattern, service)` | `internal/elasticml/poller.go:27,234-244` |
| Drain şablonlayıcı | Var: 5 dk / 1000 örnek, yalnız "yeni şablon" olayı | `internal/templater/puller.go:15-45`, `recorder.go:146-150` |

Hüküm: **servis × seviye log hacmi / hata oranı dedektörü yok**, teyit.

## 2. Worker'ın bugünkü ES sorguları (tik = 60 sn, pencere 5 dk)

| Çağrı | Şekil | Yük |
|---|---|---|
| `DetectLogPatternsSustained` → `CountPatterns` | TEK `_msearch`, desen başına `size:0` + `filters`/`date_range`, `track_total_hits:false`; `_msearch` istek-seviyesi timeout taşımaz | `recorder.go:109`, `elasticsearch.go:2260+` |
| `DetectNewLogTemplates` | ES'e gitmez, CH `log_templates` okur | `recorder.go:148` |
| templater puller | 5 dk'da bir 1000 doc örnek (`Search`) | `puller.go` |
| elasticml poller | 5 dk'da bir ML records API | `poller.go:104` |
| `Histogram(f, bucketSec, groupBy)` | `date_histogram` + `terms` (size `groupTermsSize`), `size:0`, `min_doc_count:1`, yumuşak `timeout`, `request_cache`; alanlar `field_caps` önbellekli | `es_group_fields.go:205-250`, `elasticsearch.go:2039` |

Cache: worker okumaları `serveCached`'den geçmez (API değil). Eşzamanlılık tavanı logstore'da ad verilmiş bir semafor yok; tik seri. Backoff: `_msearch` hatası tiki atlar, yeniden deneme yok.
→ Yeni katman `Histogram` ailesini (servis × seviye `terms` + `date_histogram`) tik başına **tek** çağrıyla kullanabilir; `_msearch` değil, tek `_search`.

## 3. Tasarım

**Yeniden kullanılacak parçalar:** `medianMAD`/`effectiveMAD`/`flatMADFloor` (`anomaly.go:193,229,1736`), `fillDwellGrid` (`:1271`), `chooseBaseline`+`seasonalParams`+takvim (`:1445-1728`, `calendar.go`), `AnomalySensitivityConfig.DwellBuckets/CriticalZ` (`chstore/anomaly_sensitivity.go:31-38`), tek-yazıcı deseni (`Recorder` + `cache.Lock`, `recorder.go:42`), `FingerprintAnomaly`, `anomaly_verdicts` (`store.go:2493`, event_id anahtarlı, `verdict`+`note`+`created_by`; `/api/anomalies/...` verdict uçları `problem_verdicts.go` deseni).

**Kolon mu, ayrı tablo mu?**

| | `anomaly_events` + `source`/`stage` kolonu | Ayrı `log_ml_events` |
|---|---|---|
| Okuyucular | `ListAnomalyEvents` **tür süzgeci taşımıyor** (`anomaly_event.go:682-684`); Inbox `kind=anomaly` şeridi, `/api/anomalies/events|active`, fusion, rootcause, promotion hepsi yeni satırı görür → **her okuyucuya `stage != 'shadow'`** eklemek gerekir (≥6 yer, kaçırılan = karışma) | Sıfır okuyucu değişikliği; eski yol dokunulmaz |
| Şema | İki-boot probe + `ddl_defer` yeniden-probe listesi (1741 dersi: unutulursa kolon süreç boyunca kapalı) | Yeni tablo boot'ta `adaptDDL`/ON CLUSTER, probe yok |
| RMT whole-row | Tüm Upsert yolları yeni kolonları taşımalı | Kendi Upsert'i |
| Terfi | Satırın stage'i değişir (yerinde) | Terfi = `anomaly_events`'e kopya satır (`kind=log_ml`), kaynak id'si `sample`/yeni kolonda |
| TTL/temizlik | Ortak 30 gün | Kendi TTL'i (karne için 30 gün yeter) |

**Öneri: ayrı tablo `log_ml_events`** (aynı şekil + `detector LowCardinality`, `level`, `window_sec`, `baseline`, `observed`, `z`, `stage`, `pod`, `code`), `ORDER BY id`, RMT(version), TTL 30 gün. Terfi açıkken satır `anomaly_events`'e `kind=log_ml:<detector>` olarak kopyalanır; o andan sonra mevcut fusion/Problem yolu olduğu gibi çalışır. Verdict: `anomaly_verdicts` aynen, `event_id='logml:'+id`. Karne: `anomaly_verdicts` × `log_ml_events` 30 gün join (dedektör başına isabet = anomaly / (anomaly+not_anomaly), örnek sayısı).

**Tekilleştirme (Elastic ML):** terfi öncesi aynı `service` + çakışan `[started_at,last_seen]` için `kind=elastic_ml` satırı varsa terfi etme, `log_ml_events.dup_of` doldur.

**Tik:** worker'da ayrı `LogMLRecorder` (kendi `cache.Lock`, 5 dk tik; `Recorder` 60 sn'ye dokunulmaz). Tik başına ES: **1** agregasyon (`terms service` × `terms level` × `date_histogram 5m`, pencere son 2 saat, `size:0`, `track_total_hits:false`, keyword alanlar `field_caps`'ten, zaman 5 dk'ya yuvarlı). Dilim 2-4 ek sorgular kendi tavanlarıyla (config: `timeoutMs`, `maxServices`, `maxConcurrent=1`).
Durum: taban = son 7 gün aynı saat-sınıfı (takvim), `medianMAD`, `DwellBuckets`; CH `log_ml_baseline` **yok** — taban ES histogramından tik içinde değil, CH `log_ml_events`'e değil, **ayrı `log_ml_series`** (servis × seviye × 5 dk sayım, TTL 14 gün, tek yazıcı) → ES'e yalnız son 2 saat sorulur, taban CH'den. Backfill: tek seferlik, saatlik bucket, 1 istek/sn.

**Kararlar (onay):** K1 ayrı tablo. K2 `log_ml_series` ile taban CH'de (ES yükü sabit). K3 terfi varsayılan KAPALI, dedektör başına `anomaly_promotion.logML[detector]={enabled, minHitRate, minSamples}`. K4 Problem'e giden tek yol kopya satır, yerinde stage değişimi yok.
**Sorular:** S1 ES seviye alanı: `severityTx` keyword mi (`s.fields.SeverityTx`), yoksa sayısal `SeverityNo` üzerinden mi gruplanacak? S2 Susma için "beklenen hacim > 0" eşiği servis başına mı, `anomaly_tracked` listesi mi? S3 `/anomalies` sayfasında bugün sekmeler inbox/acknowledged/ignored/resolved — "Log ML" beşinci sekme mi, yoksa `?view=logml` ayrı tablo mu? S4 Karne 30 gün isabeti, verdict girilmeyen satırlar paydaya girsin mi (öneri: hayır)?

## 4. Dilim planı (her biri kendi v0.10.X)

| # | İş | Tahmin |
|---|---|---|
| 0 | `log_ml_events` + `log_ml_series` DDL, `LogMLRecorder` iskeleti (boş), `/api/log-ml/*` (`registerLogMLRoutes`), Anomalies "Log ML" sekmesi (DataTable), verdict yeniden kullanımı, karne şeridi, admin terfi ayarı (kapalı) | ~1 gün |
| 1 | Hacim + hata oranı + susma: tek agregasyon, taban/MAD/dwell/takvim, backfill | ~1 gün |
| 2 | Şablon spike: `log_templates` pencere sayımı eki (yeni kolon, iki-boot + reprobe), spike kuralı | ~yarım gün |
| 3 | Nadir hata kodu (`terms code` nadir eşik) + aykırı pod (`terms pod` MAD) — tik başına +2 sorgu, tavanlı | ~yarım gün |
| 4 | Change point (CUSUM, `log_ml_series`) + rollout eşlemesi (`rollout_causes` deseni) | ~yarım gün |
| 5 | CoSRE v2 tool `log_ml_recent(service)`: özet + kanıt id'leri | ~2 saat |
