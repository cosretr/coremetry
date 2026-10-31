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

## 3b. Yerleşim (operatör ek kararı 2026-10-31)

Ayrı alt sistem: kendi goroutine'i, kendi `cache.Lock`'u, tik bütçesi (`context.WithTimeout`), bellek sınırı (servis × seviye × bucket tavanı config'te); evaluator (`main.go:690`, 60 sn) ve notifier (`:647`) ile aynı goroutine'i paylaşmaz, onları geciktiremez. Başlatma bayrağa bağlı: `COREMETRY_LOG_ML_ENABLED` + `COREMETRY_LOG_ML_ROLE=worker|sre|ml` (yalnız config).

| Soru | Bulgu |
|---|---|
| Worker'ın bugünkü tik yükü | 60 sn `Recorder` (`_msearch` + CH trace_ops + op_latency, 30 sn log timeout, `recorder.go:104`), 60 sn evaluator, 5 dk templater (1000 doc), 5 dk elasticml, 5 dk topology, rollout/argo/explainer döngüleri. Hepsi `mode.worker` kapısında (`main.go:992-1001`); log tarafı `COREMETRY_LOG_ANOMALY_ENABLED` ile kapatılabilir |
| sreOnly init dedektör bağımlılıklarını kuruyor mu | **Evet:** CH `store`, Redis cache + lock (`main.go:560-630`, Noop fallback), ES `logsStore` (`:940`, moda bağlı değil), settings `LoadPersisted` (`:569,:943`). Kurmadığı: lider yarışları (`:1664`), TTL DDL (`:863`), preset (`:852`), HTTP API (`:1776`) |
| sre replikasında tek yazıcı | Chart `sre.replicas: 1` (`values.yaml:107`); >1 olursa garanti yalnız `cache.Lock` (Redis). Redis yoksa `Noop` lock = always-leader → **iki yazıcı**. Kural: LogML kendi kilidini alır (`log_ml_leader`), Redis yoksa yalnız worker yerleşimi (`lockDegraded` bayrağı `/admin/stats`'ta) |
| Self-obs | `SystemStats` bloğu `logML` (`sysstats.go:49-52 Behavior/Heap deseni`): son/toplam tik süresi, ES sorgu süresi + sayısı, satır sayısı, bellek (servis×seviye seri sayısı), atlanan tik; log satırı `[logml] tick …` (1728 brifing deseni) |

**Yerleşim (operatör kararı 2026-10-31): varsayılan SRE pod'u** (`COREMETRY_LOG_ML_ROLE=sre`), `worker` geri alma bayrağı config'te. sre pod'u lider yarışlarına girmediği için alt sistem KENDİ Redis kilidini alır (`coremetry:lock:log-ml`, `cache.NewLeaderHolder`); sre replikası >1 olsa da tek yazıcı; Redis yoksa Noop kilit = tek pod varsayımı (`lockDegraded` /admin/stats'ta). sre pod'u TTL DDL koşmadığından `log_ml_events` DDL'i mevcut `migrate()` listesinde (api/worker/all boot'unda iner); sre pod'u tablo yoksa her tikte yoklar, loglar, çökmez. LLM drain'iyle (StartDrain) aynı süreç ama ayrı goroutine, ayrı context/timeout. Bellek kriteri KALDIRILDI; `seriesInMemory` yalnız bilgi. Kod: `internal/logml/` + `main_logml.go` (main.go tek satır). Ayrı `COREMETRY_MODE=ml` ileride, chart'a sre bloğu deseniyle.

**Settings › Anomaly › Log ML (dilim 0'da):** ana anahtar (kapalıyken ES'e sorgu yok), dedektör başına anahtar, dedektör başına ayrı terfi anahtarı, eşikler (taban günü, hacim tabanı, dwell, günlük tavan, ES tik tavanı, kova/tik/zaman aşımı). Hepsi varsayılan kapalı; blob bir sonraki tikte okunur; PUT audit'e eski/yeni değerle düşer.

## 4. Dilim planı (her biri kendi v0.10.X) — **hiçbir dedektör 0. dilimden önce açılmaz**

| # | İş | Tahmin |
|---|---|---|
| 0 | `log_ml_events` + `log_ml_series` DDL, `LogMLRecorder` iskeleti (boş), `/api/log-ml/*` (`registerLogMLRoutes`), Anomalies "Log ML" sekmesi (DataTable), verdict yeniden kullanımı, karne şeridi, admin terfi ayarı (kapalı) | ~1 gün |
| 1 | Hacim + hata oranı + susma: tek agregasyon, taban/MAD/dwell/takvim, backfill | ~1 gün |
| 2 | Şablon spike: `log_templates` pencere sayımı eki (yeni kolon, iki-boot + reprobe), spike kuralı | ~yarım gün |
| 3 | Nadir hata kodu (`terms code` nadir eşik) + aykırı pod (`terms pod` MAD) — tik başına +2 sorgu, tavanlı | ~yarım gün |
| 4 | Change point (CUSUM, `log_ml_series`) + rollout eşlemesi (`rollout_causes` deseni) | ~yarım gün |
| 5 | CoSRE v2 tool `log_ml_recent(service)`: özet + kanıt id'leri | ~2 saat |

## 5. Genelleştirme + Trace ML (operatör 2026-10-31, v0.10.1744)

Tek tablo `ml_events` (`signal='log'|'trace'`), sekme "ML" (Hepsi/Log/Trace), karne · geri bildirim · terfi · Settings › Anomaly › ML ortak; dedektör anahtarları sinyal başına gruplu. Mevcut trace dedektörlerine (trace_ops, op_latency, error_ramp, exception_storm, rootcause_bubbleup) dokunulmaz; ML trace dedektör adları ayrı ad alanı.

**Rollup yeterliliği (T1):** `rollup_spans_wide_1m` (0002, 1m→5m→1h): boyut service_name × span_kind × status_code × endpoint × channel_code × function_code; `span_count`, `error_count`, `q_state` TDigest(0.5, 0.95, 0.99). op_latency (p95/p99), op_error_rate, throughput_drop **buradan okunur, ham span yok**; trafik tabanı `span_count` ile. `rollup_spans_narrow_10s` endpoint taşımaz (servis düzeyi). **T2:** client span'lerde `endpoint` hedef operasyon; DB hedefi `db_summary_1m` (7 g TTL), HTTP/Kafka peer `topology_edges_5m`; `rare_exception` exception_groups. **T3:** pod boyutu wide ailede YOK → `spanmetrics_1m` pod kolonu varsa oradan, yoksa pod_outlier yalnız exception/heap kaynaklı (audit'te açık).

**Tik tahmini (5.000 servis × endpoint, 5 dk kova):** güncel pencere okuması wide_5m `FINAL`siz AggregatingMergeTree merge: 5.000 anahtar × 2 dwell kova ≈ 10k satır → <1 sn. Taban: aynı sınıf 7 gün × ±1 saat (3 kova) × 5.000 = ~105k durum birleşimi, `groupArray` ile Go'da median/MAD → prod'da 2–4 sn (ölçülecek, self-obs `chQueryMs`). Toplam tik < 10 sn hedef; aşarsa taban CH'de önceden toplanır (`ml_series` gece işi).

**Açık (operatör 2026-10-31, onay bekler):** native dedektör anahtarları (bu sürümde hepsi kapalı; alarm kuralları, Influx/Oracle akışları, Elastic ML poller hariç), ML › Metrik grubu (JVM, connection pool, Kafka, HTTP client, k8s aileleri; MetricsQL varsayılan + düzenlenebilir), Türkiye takvim ön ayarı (anomaly/calendar.go altyapısı; yarım gün saat desteği audit'e yazılacak).
