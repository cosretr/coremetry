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

**Rollup yeterliliği (T1):** `rollup_spans_wide_1m` (0002, 1m→5m→1h): boyut service_name × span_kind × status_code × endpoint × channel_code × function_code; `span_count`, `error_count`, `q_state` TDigest(0.5, 0.95, 0.99). op_latency (p95/p99), op_error_rate, throughput_drop **buradan okunur, ham span yok**; trafik tabanı `span_count` ile. `rollup_spans_narrow_10s` endpoint taşımaz (servis düzeyi). **T2:** client span'lerde `endpoint` hedef operasyon; DB hedefi `db_summary_1m` (7 g TTL), HTTP/Kafka peer `topology_edges_5m`; `rare_exception` YOK (sahiplik: exception akışı; op_error_rate kanıtı exception ID + Oracle bağlamına referans verir, kopyalamaz). **T3:** pod boyutu wide ailede YOK → `spanmetrics_1m` pod kolonu varsa oradan, yoksa pod_outlier yalnız exception/heap kaynaklı (audit'te açık).

**Tik tahmini (5.000 servis × endpoint, 5 dk kova):** güncel pencere okuması wide_5m `FINAL`siz AggregatingMergeTree merge: 5.000 anahtar × 2 dwell kova ≈ 10k satır → <1 sn. Taban: aynı sınıf 7 gün × ±1 saat (3 kova) × 5.000 = ~105k durum birleşimi, `groupArray` ile Go'da median/MAD → prod'da 2–4 sn (ölçülecek, self-obs `chQueryMs`). Toplam tik < 10 sn hedef; aşarsa taban CH'de önceden toplanır (`ml_series` gece işi).

**Açık (operatör 2026-10-31, onay bekler):** native dedektör anahtarları (bu sürümde hepsi kapalı; alarm kuralları, Influx/Oracle akışları, Elastic ML poller hariç), ML › Metrik grubu (JVM, connection pool, Kafka, HTTP client, k8s aileleri; MetricsQL varsayılan + düzenlenebilir), Türkiye takvim ön ayarı (anomaly/calendar.go altyapısı; yarım gün saat desteği audit'e yazılacak).

## 6. Yerleşik dedektör anahtarları (v0.10.1746, operatör onayı 2026-10-31)

`native_detectors` blobu (Settings › Anomaly › Yerleşik dedektörler), hepsi KAPALI: log_pattern, trace_op, metric_anomaly (+anomaliden terfi; error_ramp metrik taramasının içinde, aynı anahtarla kapanır), behavior, service_slowdown, db_health, db_capacity, db_slow_statement, runtime_pods (+runtime_vm). Kapı noktaları: `recorder.go` (log/trace blokları), `anomaly.go scan()`, `behavior_scan.go scanBehavior()`, `evaluator.go` (beş çağrı + `promoteStrongAnomalies`). 15 sn önbellekli okuma; restart yok; PUT audit eski/yeni. **Kalır:** alarm/watcher kuralları, Influx/Oracle external scanner, Elastic ML poller, exception akışı (gruplama, fırtına, P1 merdiveni, fatal/shared), slo_burn, heartbeat, self-health, rollout korelasyonu (Problem AÇMAZ: `internal/rollout` Problem yazmaz, yalnız deploy eşler), kök neden sentezi, açıklayıcılar. op_latency / log_template_new / heap kendi anahtarlarıyla zaten kapalı.

**Trace kaynağı (kalite çerçevesi, operatör):** Collector'da tail sampling var → span sayımından oran/gecikme hesaplanmaz. T1 kaynağı: VM RED serileri (spanmetrics/route histogramları, brifingle aynı etiket çözümü) birincil; VM yoksa `rollup_spans_wide_1m` örnekleme ağırlığı düzeltmesiyle (sampling attribute MV'de yoksa dedektör "kaynak yok" diye sessiz kalır ve bunu self-obs'a yazar).

## 7. Log dilim 1 (v0.10.1747): hacim + hata oranı + susma

`internal/ml/log_red.go` bileşik dedektör (log_volume, error_rate, service_silent; her biri ayrı anahtar). Tik başına **1** ES isteği (`logstore/es_ml.go`: svc terms × lvl terms × date_histogram + `latest` max + isteğe bağlı influencer terms, hepsi aynı gövdede; size 0, track_total_hits false, keyword alanlar `termsAggField`, script yok). Seri `ml_series`'e (RMT) yazılır; taban CH'den tek okuma: `anomaly.CalendarDayClass` ile aynı sınıf günler (<3 eşleşme → tüm günler) × ±1 saat, `groupArray` → Go'da medyan+MAD (MAD tabanı max(1.4826·MAD, 0.1·medyan, 1)). Kurallar: ingest payı (`ingestLagSec`, son tam kova), eksik veri (`minBaselineSamples`), taban hacmi (`minBaselineRate`), dwell, günlük tavan, tatil/yarım gün/pazar sınıfında düşüş ve susma aday değil (hata oranı değerlendirilir). Influencer: `influencerField` boşsa adaylar (channel, channel_code, kanal…) `field_caps` ile saatte bir keşfedilir; error_rate kanıtına ilk 5 değer. Backfill: ilk tikte 21 gün, günlük istek × saatlik kova, 1 istek/sn, arka planda (`bucket_sec=3600`, taban okuması normalize eder). Ölçülmedi: prod ES süresi (self-obs `esP95Ms`), kanal alanının gerçek adı. **Yarım gün:** `CalendarDayClass` saat taşımıyor; yarım gün bütün gün off-day sayılır (13:00 kuralı takvim dilimine bırakıldı).

## 8. Trace T1 (v0.10.1748): op_latency + op_error_rate + throughput_drop

`internal/ml/trace_red.go`. **Kaynak yalnız VictoriaMetrics** (Collector tail sampling: span/rollup sayımından oran hesaplanmaz; VM yoksa dedektör sessiz, `lastError` self-obs'ta). Aile pinleri `anomaly.REDFamilyPins` (HTTP + 2 gRPC); servis/route/durum etiketleri `MetricLabelNamesIn` ile saatte bir keşfedilir (servis: LabelMap.Service → service_name/service/job; route: http_route/rpc_method/route). Aile başına 3 gruplu `QueryPromQLRange` (count, 5xx/gRPC hata count, `histogram_quantile(0.95, sum by (svc, route, le))`), adım = kova, topk yok; seri tavanı 5.000 endpoint. Seri `ml_series` (signal=trace, part `route|count|err_ratio|p95`), taban CH takvim sınıfı × ±1 saat; trafik tabanı (minBaselineRate) altındaki endpoint değerlendirilmez; throughput_drop tatilde aday değil; CH taban süresi `lastChMs`. **Ertelenen (T1b):** op_error_rate kanıtına ±5 dk exception ID + Oracle referansı, BubbleUp özeti (rootcause_bubbleup yeniden kullanımı), örnekleme ağırlığı düzeltmesi (VM yokken) — sampling attribute MV'de yok, dolayısıyla uygulanamaz; audit notu: rollup yedeği bu yüzden kapalı.

## 9. Metrik M1 (v0.10.1749): aileler

`internal/ml/metric_ml.go`. Aileler = dedektörler (`metric_jvm`, `metric_pool`, `metric_kafka`, `metric_http_client`, `metric_k8s`; her biri ayrı anahtar + terfi). Seriler `cfg.MetricFamilies` (Settings › ML › "Metrik MetricsQL ifadeleri": varsayılan OTel/Prometheus adları, düzenlenebilir; `{svc}` = VM LabelMap servis etiketi, `{win}` = kova). Kaynak VM `QueryPromQLRange`, adım = kova. Counter reset: ifadeler rate/increase; scrape boşluğu: dwell penceresinde eksik nokta → değerlendirme yok (eksik veri kuralı); pod etiketi ifadede servis düzeyine toplanır (pod_outlier T3'te). Taban CH `ml_series` (signal=metric), takvim sınıfı × ±1 saat; düşüş yönü tatilde aday değil. Ölçülmedi: gerçek metrik adları (prod'da ifade düzenlenmesi beklenir), VM sorgu süresi (self-obs `esP95Ms` ortak sayaç).

## 10. Türkiye takvimi (v0.10.1750)

Mevcut `anomaly/calendar.go` altyapısı üstüne, yeni takvim motoru YOK. `AnomalyCalendarConfig.FixedHolidaysTR` (sabit resmi tatiller her yıl: 1 Oca, 23 Nis, 1 May, 19 May, 15 Tem, 30 Ağu, 29 Eki; 28 Eki yarım gün) `SpecialKindOn` içinde; `AutoRules` (ayın 15'i, ayın son iş günü, yıl sonu, tatil öncesi son iş günü; her biri ayrı anahtar) `calendarDayKind` içinde "ay sonu benzeri" sınıf verir. Dini bayramlar yıla bağlı → özel gün listesi (2027 ön ayarda; Settings "Türkiye ön ayarını yükle": Europe/Istanbul + bayrak + 2027 günleri + dört kural; gelecek yıl girdisi yoksa uyarı). ML dedektörleri takvim sınıfını `anomaly.CalendarDayClass` ile kullanır (taban aynı sınıf günler; tatil/yarım gün/pazar'da düşüş ve susma aday değil, hata oranı/gecikme değerlendirilir). **Yarım gün saat desteği YOK:** sınıf gün düzeyinde; 28 Ekim ve arifeler bütün gün yarım gün sayılır (13:00 sonrası slot ayrımı yapılmadı — v0.10.1762 ML tarafında kapandı, aşağıda).

## 11. Kalite çerçevesi (v0.10.1751, üç sinyal ortak)

Ortak etiket seti `chstore.MLLabels` (ML kararları × olay penceresi; sinyal fark etmez). Recorder tüm dedektör olaylarını tek süzgeçten geçirir (`internal/ml/quality.go`): etki eşiği (göreli 0.2 + mutlak), geri bildirim (dedektör × servis net "değil" ≥3 → z +0.5/adım, tavan 4), rejim değişimi (6 sa kesintisiz → yeni normal, yazılmaz), teyit skoru (aynı servis ±10 dk başka dedektör/sinyal; kanıtta `confirmScore/confirmSignals/confirmedBy`), `configHash`; `started_at` artık koşu başı (önceden her tik ezilirdi). Taban hijyeni `MLBaselineRequest.Exclude` (gerçek etiketli kovalar tabana girmez). Eksik veri: VM kovası yoksa trace/metrik değerlendirilmez (önceden trace eksik kovayı 0 sayıyordu → sahte trafik düşüşü). Backtest `POST /api/ml/backtest` (servis zorunlu, ≤14 g, salt hesap): canlı kararla aynı judge, isabet/etiket eşlemesi, precision (yalnız etiketli) + recall; geri bildirim uygulanmaz (etiket sızıntısı). Terfi kapısı `PromotionGate` gerekçeli (ana/dedektör/terfi anahtarı, örnek, isabet, takvim dilimi); karne sinyal + dedektör başına. Bastırma sayaçları /admin/stats `ml.suppressed*`. Rejim/teyit durumu süreç-içi: lider değişince sıfırlanır (kabul edilen sapma). Terfi yolu hâlâ YOK.

## 12. Replay kaydı (v0.10.1752)

`ml_event_snapshots` (RMT, ORDER BY event_id, TTL 120 g): koşu başında ön kayıt (−2 sa seri, taban medyan+MAD, eşikler + etkin z + config hash, takvim sınıfı, tetiklenen kovalar, teyit ID'leri, kanıt, ≤5 trace ID CH spans'ten + ES tik bütçesi kaldıysa ≤3 log satırı), +2 sa sonra tam kayıt (±2 sa seri + servisin rollout'ları) aynı satırı değiştirir. Tik başına ≤50 kayıt. `GET /api/ml/{id}/replay` yalnız bu tabloyu okur (+ servisin ±2 sa diğer kayıtları). Sapmalar: lider değişirse +2 sa kaydı yazılmaz (ön kayıt kalır, `complete=false`); aynı olay kimliğinin yeni koşusu eski kaydı ezer (ml_events ile aynı kimlik modeli); varsayılan ES tik tavanı 1 olduğundan log dedektörü açıkken log satırı örneği alınmaz (tavan ≥2 gerekir).
**Replay UI (v0.10.1753):** ML sekmesi satır eylemi "Replay" → Sheet (`?replay=<id>`): TimeChart seri + taban bandı (medyan ± etkin z × effMAD, sunucuyla aynı), tetik kovaları bölge, rollout işaretleri, takvim sınıfı, eşikler, config hash, teyit eden ID'ler, servisin ±2 sa diğer sinyalleri, ham örnek (traceHref / logsHref pivotu). What-if: aynı pencere (tespit −24 sa … +2 sa) iki backtest yan yana; `overrides` (z, dwell, etki) editor'a açık, "Bu ayarı uygula" yalnız admin (PUT /api/admin/ml audit'li). Dönem replay'i: servis + son 1–14 gün (`?bt=&btDays=`), dedektör özeti + ilk 100 isabet. explain_ml_event dilim 5'te.

## 13. Trace T1b (v0.10.1754): bağlam referansı

`internal/ml/trace_context.go`. op_error_rate: aynı servis + endpoint ±5 dk `EndpointTopExceptions` ilk 3 tip (fingerprint = grup kimliği, Inbox pivotu `/problems?exc=`) + örnek trace'lerin `OracleErrorsByTrace` referansı (kaynak, kod, operasyon; gövde kopyalanmaz). op_error_rate / op_latency: BubbleUp özeti (`ServiceBubbleUp` + `rca.TopBubbleUp`, sentezleyiciyle aynı kesim/tavan; tik başına ≤2). Koşu başına bir kez, tracker'da tutulur, her tik kanıta eklenir; Replay Sheet "Bağlam" bölümünde gösterilir. BubbleUp örneklenmiş span'lerin dağılımıdır, oran hesaplamaz. Örnekleme ağırlığı düzeltmesi hâlâ uygulanamaz (sampling attribute MV'de yok).

## 14. Log dilim 2 (v0.10.1755): şablon spike + nadir şablon

**Plan değişti (operatör kararı C, 2026-10-31):** `log_templates`'e pencere sayımı kolonu YOK — Drain kimliği tikten tike kararsız (v0.10.1030) ve sayımı 1000 satır örnekten; kimlik bazlı pencere sayımı spike ölçemezdi. Sayım ES'te gerçek: `logstore/es_ml_templates.go` tik başına tek `_search`, aile başına `filters` kolu (servis `term` + gövde `match_phrase` sabit ifade; regex/wildcard/script yok) × `date_histogram` (min_doc_count 0). Aile = servis × şablonun `<*>` içermeyen en uzun ardışık belirteç dizisi (≥3 belirteç, ≥12 karakter, ≤8 belirteç); ifade seri anahtarıdır (`ml_series` part `tpl|<ifade>`), restart sonrası izlenen aileler `MLSeriesParts` ile geri yüklenir. Adaylar saatte bir `log_templates` (son 6 sa, sayıma göre ≤500), izlenen aile tavanı 100, aynı Drain ailesi yeni aile açmaz. Yeni aileye bir kez geçmiş (BaselineDays × saatlik kova, tik başına ≤10 aile, 1 istek). `template_spike`: taban medyanı ≥1, yukarı judge; `rare_template`: taban örneği yeterli ve tamamı 0, dwell kovalarının hepsi ≥ hacim tabanı. ES tik tavanı varsayılanı 1 → 3 (log RED + şablon + backfill); bütçe bitince şablon tiki atlanır ve `lastError`'a yazılır. Şema değişikliği yok. Ölçülmedi: prod ES'te 100 kollu `filters` süresi (self-obs `esP95Ms`), `match_phrase`'in gövde çözümleyicisiyle eşleşme oranı.

## 15. Log dilim 3 (v0.10.1756): nadir hata kodu + aykırı pod

`internal/ml/log_breakdown.go` + `logstore/es_ml_breakdown.go`: tik başına TEK `_search` (servis terms × {hata filtresi → kod terms 20, pod terms 50 → hata filtresi}), dwell penceresi. Kod alanı adaylardan (error.code, error_code, errorCode, …), pod alanı log `Filter.Pod` listesinden saatte bir keşfedilir; bulunamazsa dedektör sessiz + `lastError`. `rare_error_code`: kod serviste son 14 g hiç görülmemiş (`ml_series` `code|<kod>`, pencere öncesi), servisin kod geçmişi ≥3 g eski, sayım ≥ hacim tabanı; ilk tikte bir kez 14 günlük günlük-kova geçmişi. `pod_outlier`: ≥3 pod (toplamı hacim tabanı altındaki pod sayılmaz), hata oranı medyan + max(1.4826·MAD, 0.02) ölçeğinde z ≥ kritik z, pod hatası ≥5, oran ≥2× medyan; kesitsel karar. ES tik tavanı varsayılanı 3 → 4. Ölçülmedi: prod'da kod alanının gerçek adı, pod alanı doluluk oranı.

## 16. Log dilim 4 (v0.10.1757): kırılma noktası + rollout

`internal/ml/change_point.go` (jenerik çekirdek; T3 trace aynısını kullanır). İki CH okuması/tik: son 6 sa seri (`MLSeriesRecent`, hedef kova) + aynı takvim sınıfı günlerin aynı gün-içi diliminin çeyreklikleri (`MLSeasonalProfile`, CH `quantiles`). z = (v − Q50) / max(IQR/1.349, 0.1·Q50, 1), katkı ±h/2 kırpılı; iki yönlü CUSUM k=1, h=2×kritik z; olay yalnız kayma pencere sonunda sürüyorsa (son 3 kovanın z'si alarm yönünde > k). Kırılma anı = alarmdan önce S'nin son sıfırlandığı kova (`started_at` olur; kalite süzgeci daha erken anı korur). Eksik kova boşluktur, CUSUM'u sıfırlar — log'da ES sıfır kovası seride yok, bu yüzden susma kırılma sayılmaz (service_silent'in işi). Kırılma ±15 dk içinde servisin rollout'u kanıta `rollouts` olarak eklenir (eşleme, nedensellik iddiası değil). Tatilde aşağı kırılma aday değil. Ölçülmedi: prod'da profil sorgusu süresi (self-obs `lastChMs`).

## 17. Trace T2 (v0.10.1758): bağımlılık gecikmesi

**Kaynak değişti (operatör kararı 2026-10-31):** §5'teki `db_summary_1m` / `topology_edges_5m` örneklenmiş span'lerden türediği için KULLANILMAZ; kaynak VM OTel client histogramları: `http_client_request_duration_seconds`, `db_client_operation_duration_seconds`, `messaging_client_operation_duration_seconds`. Servis × hedef etiketi saatte bir keşfedilir (server_address / db_namespace / messaging_destination_name …). Aile başına 2 gruplu sorgu (count, p95); seri `dep|<aile>|<hedef>|{count,p95}`; trafik tabanı altı sessiz; yukarı judge. Metrik yoksa aile atlanır, hiçbiri yoksa sessiz + `lastError`. **Yan düzeltme:** trace/metrik VM sorguları `ObserveES` üzerinden ES tik bütçesini tüketiyordu (log örneği / şablon geçmişi gereksiz kısılırdı) → `ObserveVM` ayrı sayaç (`vmQueries`), p95 örneklemi ortak.

## 18. Trace T3 (v0.10.1759): pod aykırısı + kırılma

**Kaynak (operatör kararı 2026-10-31):** `trace_pod_outlier` VM server RED aileleri (T1 pinleri) + keşfedilen pod etiketi (k8s_pod_name, pod, …); `spanmetrics_1m` pod kolonu örneklenmiş span'den türediği için KULLANILMAZ. Dwell penceresi üzerinde aile başına 3 anlık sorgu (istek artışı, hata artışı, p95); hata oranında log dilim 3'ün `podOutliers` kuralı, p95'te pod'lar arası medyan+effMAD, ≥3 pod, ≥1.5× medyan. Pod serisi tutulmaz (replay serisiz, kanıt sayılarıyla). `trace_change_point`: `change_point.go` çekirdeği trace ml_series `|p95` serilerinde (`endsWith`), rollout ±15 dk eşlemesi aynı. Etiket yoksa sessiz + `lastError`.

## 19. Dilim 5 (v0.10.1760): CoSRE v2 araçları

`internal/mcptools/v2_ml.go`, yalnız v2 sohbet kataloğu (`registerV2Subset`; v1 kataloğu ve dış MCP `tools/list` değişmez, altında yalnız `*/v2` anahtarları). `get_log_anomalies` (signal log varsayılan | trace | metric, service, detector, range_s ≤7 g, ≤20 satır, ≤2 KB): gölge olaylar + teyit + karar; "Problem değil" açıkça. `explain_ml_event(id)`: replay kaydından olgular (takvim sınıfı, taban medyan/MAD, kritik/etkin z, tetiklenen kova sayısı, seri tepe/taban özeti, teyit, servisin diğer sinyalleri, rollout, exception/Oracle/BubbleUp referansları, ≤3 örnek trace, Replay page_url); ham seri dönmez, gövde ≤2 KB. Viewer düzeyi, salt okuma.

## 20. Terfi kopya yolu (v0.10.1761, K4)

`internal/ml/promote.go`: `PromotionGate` geçen gölge olay `anomaly_events`'e kopya satır (kind `ml:<dedektör>`, aynı id, pattern = seri parçası); ml_events satırı `stage=promoted`. Aynı servis + çakışan pencerede `elastic_ml` satırı varsa terfi yok, `dup_of` doldurulur. Terfi anahtarı açık dedektör yoksa tik başına SIFIR ek okuma (varsayılan). Açılınca tik başına +3 okuma (karne 2 + anomaly_events 1). Ölçülmedi: `ml:*` türünün fusion/Problem yolunda nasıl göründüğü (terfi açılmadan önce bir dedektörle denenmeli). Self-obs `promoted`.

## 21. Yarım gün saati (v0.10.1762)

ML dedektörleri `mlDayClass` kullanır: yarım günün 13:00 öncesi sıradan gün sınıfı (taban o güne göre, düşüş/susma aday), 13:00 ve sonrası `half_day` (off-day kuralı). Yerleşik dedektörlerin gün düzeyi sınıfı DEĞİŞMEDİ; saat sabit (13), ayar yok.

## 22. Prod gözlemi 01.11.2026 — düzeltmeler

**v0.10.1763 log backfill.** Gözlem: 06:48 "backfill başlıyor" → ilk gün sorgusu "context canceled", sonra sessizlik. Kök neden: goroutine tik context'ine (`tctx`, ~34 sn bütçe) bağlıydı; tik bitince iptal, `backfillRun` bayrağı yüzünden tekrar yok → log tabanı hiç oluşmadı. Düzeltme (`internal/ml/backfill.go`): `Deps.Background` (alt sistem ömrü); saatlik pencereler (504 istek), `backfillTimeoutMs` (30 sn) ve `backfillIntervalMs` (2 sn) Settings'te; kısmi sonuç (`timed_out` / `_shards.failed`, `rawCountSearchEnv`) ya da hata → iki yarı (bucket_sec 1800), yarı da düşerse saat eksik; ilerleme `system_settings ml_backfill:log` (DoneUntil, Missing), restart'ta devam; log gün başına "N/504 saat (eksik K)", karnede "Log geçmişi: N/504 saat". Hata "bitti" değil; tamamlanan plan 48 sa içinde yeniden koşmaz. Test: tik iptali sonrası sürer, resume, yarıya bölme.
**v0.10.1764 VM bütçesi.** Gözlem: dependency_latency ve metric_http_client ham histogram query_range'i tüm servisler için → 422 maxSamplesPerQuery, sonra her tikte timeout. Düzeltme (`ml/vm_budget.go`): instant sorgu yalnız son kova, dwell geçmişi CH ml_series'ten (`mergeHistory`; trace_red, dependency, metric_ml); bağımlılıkta önce `topk by (svc)(K)` hedef (count), sonra p95 servis grupları (`vmServiceBatch`) × seçili hedef regex'i; 422/zaman aşımı → aile başına artan backoff 5 dk×2ⁿ ≤ 1 sa, beklemede sorgu gönderilmez; tik durumu `system_settings ml_tick_status` (backoff, süre, olay, hata) karnede "son tik … · VM bekleme". Metrik aileleri serbest ifade: gruplama yok, yalnız instant + backoff (422 alan ifade operatörce daraltılır).
**v0.10.1766 A3 eski dedektörler.** Gözlem: yerleşik liste 13:07'de kapatıldı, 13:15'te "NEW LOG SHAPE" ve op_latency (102. tekrar) açıldı, "Trace operation anomalies" kartı canlı spike gösterdi. İki kök neden: (1) op_latency ve log_template_new anahtarları anomaly_sensitivity'de, yerleşik listeden bağımsız → şimdi aynı ekranda `own` anahtarları (snapshot+merge, audit) + "Eski dedektörlerin hepsini kapat" (`POST /api/admin/native-detectors/off-all`; kalanlar: alarm kuralları, exception akışı, Oracle/Influx, Elastic ML, slo_burn, heartbeat, self-health); (2) `/api/anomalies/trace-ops` kartı anahtara bakmadan `DetectTraceOpAnomalies`'i istek anında koşuyordu (anomaly_events'ten değil) → `traceOpHits` anahtar kapalıyken boş döner (api.go satır sayısı değişmedi). Heap bandı için ayrı anahtar bulunamadı (docs/plans/ml-next.md). Kalan maddeler (4–7, B2–C3) `docs/plans/ml-next.md`.
**v0.10.1767 kuyruk 2–5.** (2) `MLResolveField` sonucu (boş aday dahil) 1 sa önbellek — field_caps her tik tekrarlanıyordu. (3) Tik blobu `Steps` (labels/detectors/write/snapshot ms) + `Signals` (dedektör başına çalıştı/servis/aday/yazıldı/süre/kısmi/hata); log satırı ve karnede dedektör satırları ("çalışmadı" ≠ "0 olay"). (4) BaselineDays varsayılanı 21, MaxServices Settings'te. (5) pod_outlier kanıtına `queryAt`/`sampleTs`/`tickAt` (bayat VM serisi teşhisi); kimlik zamansız ve ölçüye bağlı (test) — tekrar satır B3 epizot modelinde ele alınır (docs/plans/ml-next.md).

## 23. Epizot modeli (B3, v0.10.1768)
Taban kimlik (dedektör × servis × bölüm) bir koşu açar; olay sürerken aynı epizot kimliği (`MLEpisodeID` = taban + epizot başlangıcı) üzerine yazılır; art arda 3 tik görülmeyince satır `stage=closed` (bitiş = last_seen; yeni kolon YOK — ended_at türetilir); kapanıştan 30 dk içinde tekrar aynı epizot yeniden açılır, sonrası yeni epizot = yeni satır. Eski zamansız kimlik satırları olduğu gibi kalır (veri taşıma yok; 30 g TTL'le düşer). Teyit/replay/karar epizot kimliğiyle. ML sekmesi: Durum (açık / kapandı / terfi / kronik) ve Süre sütunları. pod_outlier tekrar gözlemi (madde 6) bununla kapanır: aynı epizot tek satır, kesinti sonrası yeni epizot ayrı satır.
