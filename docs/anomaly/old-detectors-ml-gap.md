# Eski anomali makinesi → ML: silmeden önce boşluk denetimi

2026-11-12 · salt-okunur · kapsam: Davis özneleri DIŞINDA kalan her eski dedektör/yol
(özneler: `docs/anomaly/davis-ml-gap.md`). Kanıt koddan; denetim metninden değil.
Doğrulanan iddia: `docs/anomaly/anomaly-reassessment-audit.md` §5 (:145-158) ve N5 (:95-102).

## Kısa sonuç

Denetimin "ML zaten kapsıyor" iddiası yine kısmen yanlış. Eski yolların hiçbiri ML ile
birebir değil. Tek birebir yedek ML'nin kendi kopya yolu (`ml/promote.go`). Kalanlar ya
kısmi (farklı ölçüm, farklı anahtar) ya da ML'de hiç yok (exception fırtınası, Oracle/ext,
svc-slowdown seviyesi). Varsayılanı kapalı olanlar prod ayarı doğrulanınca silinebilir;
varsayılanı AÇIK iki yol var (`trace_op_latency`, prod'da `log_template_new`).

Sayım: birebir 1 · kısmi 11 · yok 3 (aşağıdaki 15 satır).

## Ortak noktalar

- Yazma: `chstore.UpsertAnomalyEvent(s)` `internal/chstore/anomaly_event.go:715,741`.
- Kapı: `native_detectors` hepsi varsayılan kapalı `internal/chstore/native_detectors.go:33-60`;
  Elastic ML poller ve `anomaly_sensitivity` anahtarları bu kapının DIŞINDA (:6-9).
- ML kapısı: ana `Enabled` + dedektör başına anahtar, varsayılan hepsi kapalı
  `internal/chstore/ml_settings.go:175-177,264`. ML grup Problem'i ≥2 FARKLI sinyal ister
  (`internal/ml/group_gate.go:3-22`); tek dedektör asla Problem açmaz.
- Ortak okuyucular (her `anomaly_events` kind'ını okur): unified liste
  `internal/api/anomalies_unified.go:169`; eski uçlar `internal/api/anomaly_extra.go:52`,
  `anomaly_event_get.go:90`; servis sayfası `frontend/src/pages/service/Overview.tsx:565`,
  `Pod.tsx:258`; CoSRE `internal/sre/orchestrator.go:82,118`, `sre/readers_anomalies.go:143`;
  MCP `internal/mcptools/tools.go:1367`; RCA `internal/anomaly/rootcause_worker.go:203`,
  `fusion.go:225`; `api/inbox.go:747`, `deployment_report.go:290`, `rollout_detail.go:202`;
  terfi `internal/evaluator/evaluator.go:1329,1389,1682` (`anomaly-auto:`).
- Export: `anomaly_events` admin export'ta yok.

## Tablo

Kısaltma: "ortak" = yukarıdaki ortak okuyucular.

| # | Ad (kind/rule) | Ne yakalar | Anahtar | Kaynak | Varsayılan / prod | Bugünkü tüketici | ML karşılığı | Hüküm |
|---|---|---|---|---|---|---|---|---|
| 1 | `log_pattern` | Elle yazılmış regex log deseninin sürmesi | servis × desen | logstore | kapalı `native_detectors.go:34`; prod bilinmiyor | ortak; aktif anahtar `anomaly/recorder.go:242` | `template_spike` `internal/ml/log_templates.go:29` | kısmi: regex değil ES sabit-ifade sayımı |
| 2 | `log_template_new` | Drain'in ilk kez gördüğü şablon (≥20 satır) | servis × Drain templateID | `log_templates` | kod: kapalı `anomaly_sensitivity.go:645`; prod AÇIK (`logTemplateNew=true`) | ortak; log grafiği `api/anomaly_log_chart.go` | `rare_template` `ml/log_templates.go:30` | kısmi: Drain id değil ifade; taban takvim-sınıfı günlerde 0 olmalı; ML kapalıysa hiç yok |
| 3 | `trace_op` | İşlem hata sayısı artışı (sürmeli) | servis × işlem | operation MV | kapalı (`native_detectors.trace_op`); prod bilinmiyor | ortak; `anomaly/trace_ops.go:807` | `trace_error_count` `ml/trace_errors.go:3-27` + `op_error_rate` `ml/trace_red.go:32-35` | kısmi: ML mutlak sayı (≥5) ya da VM 5xx oranı; artış tabanı farklı |
| 4 | `trace_op_latency` | İşlem p99 sıçraması | servis × işlem | `operation_summary` MV | kod AÇIK `anomaly_sensitivity.go:639`; prod bilinmiyor | ortak; `anomaly/op_latency.go:434` | `op_latency` `ml/trace_red.go:32` | kısmi: ML p99 değil ">1 sn payı"; 1 sn altı yavaşlamayı görmez |
| 5 | `behavior_change` | Davranış motoru adayı; Problem açmaz | servis × sinyal | 28 g MV / VM RED | kapalı (`native_detectors.behavior` `behavior_scan.go:114` + `cfg.Behavior` :118, varsayılan doğrulanmadı) | ortak; açıklama `anomaly_explain_behavior.go` | `change_point` `ml/change_point.go`, `trace_change_point` `ml/trace_pods.go` | kısmi: ML CUSUM yalnız log serisi ve endpoint p95 |
| 6 | `calendar_traffic_drop` | Tatil/yarım günde istek düşüşünü Problem yerine olay yapar | servis | request_rate | kapalı (scan = `metric_anomaly` kapısı `anomaly.go:519-522`) | ortak | `throughput_drop` `ml/trace_red.go` + ML Türkiye takvimi | kısmi: ML takvimi tabana katar, ayrı "tatil" olayı yok |
| 7 | `metric_anomaly` / `error_ramp` → `anomaly:<svc>:<metric>` | Servis RED metriğinde sapma, doğrudan Problem | servis × metrik | MV | kapalı (`metric_anomaly`); prod bilinmiyor | keep listesi `anomaly:*:error_rate` `chstore/problem_priority_inbox.go:36`; Problem yazımı `anomaly.go:688,917,1018` | `trace_red` + `throughput_drop` (yalnız grup Problem'i) | kısmi: ML tek sinyalle Problem açmaz; servis seviyesi artış yok |
| 8 | `service_silent` (yerel) | Servis span göndermeyi kesti | servis | spans MV | kapalı (operatör kararı) | Problem `anomaly.go:1161` | log `service_silent` `ml/log_red.go:28-30` | kısmi: ML log tabanlı, trace değil |
| 9 | Exception fırtınası | İstisna sayısında ani patlama, Problem | servis × istisna | CH | kapısız (v0.10.2011) | Problem `anomaly/exception_storm.go:73`, incident :131 | YOK | yok |
| 10 | Oracle/ext: `anomaly:ext:*`, `anomaly:ext-down:*`, `anomaly:ext-cap:*` | Harici hata tablosunda hata artışı / kaynak düşük / tavan | kaynak × hata kodu | Oracle hata tablosu | bilinmiyor | Problem `anomaly/external.go:184,248,317,521,571,608,955`; keep `problem_priority_inbox.go:42,46` (ext-down bilerek dışarıda :60); kategori `problem_category.go:43,45`; CoSRE `sre/hypotheses.go:815`; bildirim türü pin `anomaly/notify_kind_pin_test.go` | YOK | yok |
| 11 | `anomaly-cluster:<src>` (ext kümesi dahil) | Topoloji komşusu açık Problem'leri tek kümede birleştirir | kaynak servis | açık Problem'ler + topoloji | `metric_anomaly` kapısı (`anomaly.go:763-771`); ext kümesinin kapısı doğrulanmadı | keep `anomaly-cluster:ext:*` `problem_priority_inbox.go:43`; yeniden açma `chstore/incident.go:395`; `sre/hypotheses.go:815` | ML grup `ml/group.go:10-19`, `ml/group_live.go` | kısmi: ML grubu tek servis içi; servisler arası/ext kümesi yok |
| 12 | `svc-slowdown:<svc>` | Servis seviyesi yavaşlama, Problem | servis | MV | bilinmiyor | keep `problem_priority_inbox.go:51,66-75`; gerekçe `api/inbox_keep_priority.go:70-87`; `evaluator/service_slowdown.go:619` | `op_latency` (işlem bazlı, >1 sn payı) | yok: servis seviyesi gecikme değeri ML'de yok |
| 13 | `elastic_ml` (Elastic ML poller) | ES ML job kayıtlarını olay yapar | servis × job/desen | ES ML | kapalı: `config.go:151,904` (`COREMETRY_ES_ML_ENABLED`); prod bilinmiyor | ortak; kablo `main.go:735-750` | ML log aileleri `ml/log_red.go`, `log_templates.go` | kısmi: farklı model, ama ikinci motor; ML log aileleri işi yapıyor |
| 14 | `ml:<dedektör>` kopya (`ml/promote.go`) | ml_events olayını anomaly_events'e kopyalar | ML anahtarı | ml_events | 3 anahtar + karne kapısı, varsayılan kapalı | `evaluator.go:1389` terfisi | ML grup Problem'i `ml/group_live.go:3-25` | birebir (aynı motor) |
| 15 | Incident kodu (v0.10.2024 emekli) | — | — | `incidents` | `IncidentsEnabled()` hep false `chstore/incidents_settings.go:33` | çağrı no-op: `AttachProblemToIncident` 20 yer (örn. `ml/group_live.go:248`, `evaluator.go:986`); okuma `api/inbox.go:297,1110,1137`, `api/api.go:5351`, `auth_session.go:79`, `custom_roles.go:124`, `mcptools/product_guide.go:127,369`, `build_link.go:192`; FE `pages/Incident(s).tsx`, `Inbox.tsx`, `settings/AnomalyTab.tsx`, `Sidebar.tsx:90` | gerekmez | yok (emekli; ML karşılığı aranmaz) |

## Hemen silinebilir (her dilim tek commit, tek revert)

1. **D1 — `ml/promote.go` kopya yolu** (#14). Terfi anahtarları `ml_settings.go:179,366-388`,
   promote testleri birlikte gider. Grup Problem'i işi doğrudan yapıyor.
2. **D2 — Elastic ML poller** (#13), YALNIZ prod'da `COREMETRY_ES_ML_ENABLED` ve yaml
   `ml_enabled` kapalı doğrulanınca: `internal/elasticml/`, `main.go:735-750`, config alanı
   `config.go:151,904`, ilgili config testi. Geçmiş `elastic_ml` satırları okunmaya devam eder.
3. **D3 — yerel `service_silent`** (#8): hüküm kısmi ama operatör kararıyla kapalı, kayıp yok.
   `checkSilence` `anomaly.go:1161`, `native_detectors` anahtarı ve kapı pin testi birlikte.
4. **D4… — incident kodu** (#15): ML sorusu yok; ayrı program, ≥4 dilim (BE motor → API/MCP
   okuyucular → FE sayfaları → ayar anahtarı). `AttachProblemToIncident` 20 çağrısı ilk dilimde
   gider; `incidents_retire_test.go`, `chstore/incident.go:395` yeniden açma ile birlikte.
   Tablo verisi kalır.

## Önce taşınmalı

- **#4 `trace_op_latency`** (kod varsayılanı AÇIK): ML `op_latency`'e p95/p99 değer tabanlı
  sapma kipi eklenmeli ya da 1 sn eşiği servis başına öğrenilmeli. Yoksa 1 sn altı yavaşlamalar kör.
- **#2 `log_template_new`** (prod AÇIK): `rare_template` Drain templateID anahtarına bağlanmalı
  veya "ilk kez görüldü" kipi eklenmeli; `anomaly_log_chart.go` ml_events'e çevrilmeli.
- **#12 svc-slowdown**: servis seviyesi gecikme sinyali ML'de yok; ya ML'ye servis p95 ailesi
  ya da bu dedektör "silinmemeli"ye geçer. Keep listesindeki `svc-slowdown:*` kalıbı
  (`problem_priority_inbox.go:51,66-75`) taşınmadan dokunulmaz.
- **#7 metric_anomaly**: keep kalıbı `anomaly:*:error_rate` (:36) ML grubunda karşılıksız;
  önce prod'da kapalı olduğu doğrulanmalı, sonra kalıp grup kural kimliğine (`anomaly-auto:group:`)
  yönlendirilmeli ya da bırakılmalı (operatör kararı).
- **#1, #3, #5, #6** (varsayılan kapalı): prod `native_detectors` değeri okununca kapalıysa
  #7 ile tek dilimde silinir; açıksa ilgili ML ailesi (`template_spike`, `trace_error_count`,
  `change_point`, `throughput_drop`) prod'da açık mı bakılmalı.
- **#11 kümeleme**: metrik-anomali kümesi #7 ile gider; ext kümesi aşağıda.
- **Eski terfi** (`evaluator.go:1329-1700`, `anomaly-auto:` anomaly_events terfisi): yazıcılar
  bittikten sonra son dilim; `evaluator.go:198,1154,1221` süpürmeleri eski kimlikleri kapatır,
  önce açık Problem kalmadığı doğrulanmalı.

## Silinmemeli

- **#9 exception fırtınası**: anomali tespiti değil, exception merdiveninin parçası; ML'de yok.
  Denetim de saklıyor (:158). Yalnız `scan()`'den çıkarılması (v0.10.2011) kalıcı olmalı.
- **#10 Oracle/ext ailesi**: operatör onaylı P1/keep kuralları (`problem_priority_inbox.go:42-46`),
  CoSRE eşleşmesi ve bildirim türü pin'i buna bağlı; ML'de kaynak bile yok.
- **#11'in ext kolu** (`anomaly-cluster:ext:*`): keep listesinde; kümeleme silinirken ext kolu
  `external.go`'ya taşınmalı veya korunmalı.

## anomaly_events tablosu

Yukarıdakilerden sonra yazanlar kalır: #2 (taşınana dek), #4 (taşınana dek), #5, #6 ve Davis
`subject_red` (kapsam dışı). Hepsi gidince yazma durur; o zamana dek ortak okuyucular (unified
liste, servis/pod şeridi, CoSRE, MCP `list_anomalies`, RCA, inbox, deploy raporu, verdict
`api/anomaly_verdicts.go:29`) geçmiş veriyi TTL bitene dek okur. Yazma bitince sırasıyla: eski
terfi → ortak okuyucuları ml_events'e çevir (servis `Overview.tsx:565` ve `Pod.tsx:258`
ilk) → unified birleşmeden legacy kolu düş. Tablo ve verisi düşürülmez.

## Emin olmadıklarım

- Prod ayarları: `native_detectors`, `opLatency`, ES ML env, `cfg.Behavior` varsayılanı,
  ext dedektörlerinin ve svc-slowdown'ın kapısı. Hepsi koddan değil prod `system_settings`'ten okunmalı.
- `anomaly-cluster:ext:*` üreten yerin `clustering.go` mı `external.go` mı olduğu ve
  `metric_anomaly` kapısına bağlı olup olmadığı.
- `trace_op` eski tabanının (artış) ML `trace_error_count` ile pratikte ne kadar örtüştüğü.
- Bildirim tür eşlemesinin tam yeri (`notify.go:603,894` yalnız süzgeç/kategori).
- FE `unifiedRows.ts` içindeki eski kind eşlemeleri okunmadı.
- `git log` çalıştırılamadı; incident emekliliği kod yorumlarından doğrulandı.
- heap band, runtime_pods, db_health/capacity/slow_statement bu belgede ayrıca denetlenmedi;
  `db-health:*` keep listesinde (`problem_priority_inbox.go:38`), yani "kaynak boş → sil"
  denetim iddiası (:153) tüketicisiz değil.
