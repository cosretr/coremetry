# Anomali tespitinin yeniden değerlendirilmesi ve tekilleştirme — Faz 0 audit

Tarih: 2026-11-12 · Durum: ONAY BEKLİYOR · Kod değişikliği yok
Operatörün isteği: "coremetry anomaliler için de yeniden değerlendirelim ve tekilleştirelim."
Netleştirme: "Dedektörler, ML jobları vs. derken çok fazla yapı oldu, hâlâ anomaliler istediği gibi çalışmıyor; tekilleştirelim."

## 1. Özet (düz cümleyle)

Bugün anomaliyi üç ayrı motor üretiyor: eski yerel dedektörler, `internal/ml` katmanı ve Elastic ML okuyucusu.
Bunların yanında bir kümeleme ve bir de "Davis özneleri" yolu var. Hepsinin ayrı anahtarı, ayrı terfi yolu ve ayrı ekranı var.

| Yapı | Bugün | Hedef |
|---|---|---|
| Tespit motoru | 3 (yerel, ML, Elastic ML) + kümeleme + özneler | 1 (`internal/ml`) |
| Dedektör yolu | yerel tarafta yaklaşık 15 yol (9'u `native_detectors` kapısında, 6'sı kendi anahtarında); ML'de 8 dedektör, 21 dedektör anahtarı; Elastic ML'de 1 poller | ML'nin 8 dedektörü |
| Olay tablosu | 2 (`anomaly_events`, `ml_events`) | 1 (`ml_events`) |
| Olaydan Problem'e terfi yolu | 4 (evaluator süpürmesi, özne süpürmesi, `ml/promote.go` kopyası, `clustering.go`) | 1 (ML grubu doğrudan Problem açar) |
| Ayar anahtarı | 4 (`native_detectors`, `anomaly_sensitivity`, `ml`, `COREMETRY_ES_ML_*` env) | 1 (`ml`; batch kalıpları buraya taşınır) |
| UI yüzeyi | `/anomalies` 3 sekme (detectors, ml, groups) + 2 detay rotası | 1 liste + 1 detay rotası |

Neden istenildiği gibi çalışmıyor? Koddan çıkan cevap kısa. Bugünkü varsayılanlarla hiçbir anomali Problem'e ya da bildirime ulaşamıyor.
- Yerel dedektörlerin hepsi kapalı. Terfi süpürmesi de `metric_anomaly` kapısının arkasında olduğu için hiç koşmuyor.
- ML olayları ise yalnız "gölge" olarak yazılıyor. Terfileri, etiket gerektiren bir karne kapısına bağlı.
- Bir servis yavaşladığında ML aynı olayı 2–3 ayrı satır olarak yazıyor ve bunları birleştirmiyor.
- Yerel taraf aynı şeyi başka bir kaynaktan (örneklenmiş span) ölçüyor, o yüzden farklı sonuç veriyor.

**Önerilen karar (Seçenek B):**
- Tek motor `internal/ml` olur. Tek olay tablosu `ml_events` olur.
- Olay modeli tektir: epizot artı servis × zaman penceresi grubu. Problem'i grup açar.
- Tek ayar ekranı (`ml`) ve tek UI yüzeyi kalır.
- Yerel dedektörler, Elastic ML poller, özneler, kümeleme ve `anomaly_events` silinir.
- Geriye uyum katmanı yok. Alarm kuralları, SLO burn ve exception akışı anomali tespiti sayılmadığı için kapsam dışında ve yerinde kalır.

Operatöre ilk soru (§8 S1): "İstediğiniz gibi çalışması" somut olarak ne demek? Kaçırılan ya da yanlış açılan 2–3 gerçek olay örneği istiyoruz. Hedef gate bu örneklerle ayarlanacak.

## 2. Envanter — bugün anomali/olay üreten her yol

Kısaltmalar:
- "yerel kapı": `native_detectors` ayarı (`internal/chstore/native_detectors.go:24-68`). 9 ad var, varsayılanı hepsi `false` (`:48`).
- "terfi": `promoteStrongAnomalies` (`internal/evaluator/evaluator.go:1343`). Bu süpürme `metric_anomaly` kapısının arkasında (`evaluator.go:712`).

| Yol | Kaynak | Tablo | Kimlik / dedup | Problem yolu | Varsayılan |
|---|---|---|---|---|---|
| log_pattern | logs (`recorder.go:110`) | anomaly_events | fp(kind,pattern,svc) `anomaly_event.go:165` | terfi `anomaly-auto:<fp>` | kapalı (yerel) |
| trace_op | `operation_summary_5m` (`trace_ops.go:496`) | anomaly_events | fp | terfi | kapalı (yerel) |
| trace_op_latency | MV (`op_latency.go:15`) | anomaly_events | fp | terfi | AÇIK, kendi anahtarı (`anomaly_sensitivity.go:232`), yerel kapı dışında (`recorder.go:169`) |
| log_template_new | Drain | anomaly_events | şablon id | terfi (sayım kapısı dışlar) | kapalı (`:279`) |
| metric_anomaly + error_ramp + calendar_traffic_drop | `service_summary_5m` / VM RED (`anomaly.go:926,1381`) | yok (calendar: anomaly_events) | rule `anomaly:<svc>:<metric>` (`anomaly.go:896`) | doğrudan Problem | kapalı (yerel, `anomaly.go:521`) |
| service_silent | metrik taraması (`anomaly.go:778`) | yok | `anomaly:<svc>:service_silent` | doğrudan | kapalı (operatör kararı) |
| heap bandı | `heap_scan.go` | yok | `anomaly:<svc>:<heap>` (`:221`) | doğrudan, gölge | kapalı |
| behavior_change | 28 g MV + VM (`behavior.go:185`, `behavior_scan.go:151`) | anomaly_events | fp | Problem YOK (`anomaly_explain_behavior.go:10`) | kapalı (yerel + kendi) |
| özneler (endpoint/db/queue, Davis F3) | VM + CH MV + Kafka lag (`subjects_scan.go:3-4`) | anomaly_events (`subject_*`, `promotion`) | fp; `anomaly-auto:subject:…` (`anomaly_subject_group.go:11`) | özne süpürmesi, kip off/shadow/live | kip `off` (`anomaly_sensitivity_subjects.go:95`); `scan()` içinde, bu yüzden yerel kapıya da bağlı |
| service_slowdown | evaluator (`evaluator.go:640`) | yok | `svc-slowdown:` | doğrudan | kapalı (yerel) |
| db_health / db_capacity / db_slow_statement | DB gauge (`evaluator.go:624-636`) | yok | (instance, check) | doğrudan | kapalı (yerel) |
| runtime_pods | JVM gauge (`evaluator.go:644-650`) | yok | Problem | doğrudan | kapalı (yerel) |
| kümeleme | açık Problem'ler (`clustering.go:23-50`) | problems | `anomaly-cluster:<kaynak>` (`:157`) | üyeleri kapatıp kaynakta tek Problem açar | açık (learning-loop D5) |
| ML log ailesi: log_red (log_volume, error_rate, service_silent), log_templates (template_spike, rare_template), log_breakdown (rare_error_code, pod_outlier), log_change_point | ES + `ml_series` (`log_red.go:3-30`, `log_templates.go:3-12`, `log_breakdown.go:3-14`, `change_point.go:3-12`) | ml_events | id = epizot (`ml_events.go:69,76`) | gölge; terfi kopyası `ml:<det>` | hepsi kapalı (`ml_settings.go:237-244`); prod'da AÇIK (bellek, doğrulanmadı) |
| ML trace ailesi: trace_red (op_latency, op_error_rate, throughput_drop), dependency_latency, trace_pod_outlier, trace_change_point | VM RED + client histogramları + `ml_series` (`trace_red.go:3-12`, `dependency.go:3-12`, `trace_pods.go:4-12`) | ml_events | epizot | aynı | aynı |
| ML metric_ml: jvm, pool, kafka, http_client, k8s | VM MetricsQL (`metric_ml.go:3-10`) | ml_events | epizot | aynı | aynı; `metric_jvm` prod'da sessiz (JVM Thanos'ta, `ml-next.md:31-38`) |
| ML terfi | ml_events'ten kopya (`promote.go:52-61`) | anomaly_events `ml:<det>` | id = epizot; `elastic_ml` çakışırsa `dup_of` (`promote.go:34-45`) | evaluator terfisi | 3 anahtar + karne kapısı, hepsi kapalı (`ml_settings.go:366-388`) |
| Elastic ML poller | ES `_ml` kayıtları, skor ≥ 75 (`poller.go:141-197`) | anomaly_events `elastic_ml` | fp(job[:fn], svc) (`poller.go:216-246`) | evaluator terfisi | kapalı; yalnız `COREMETRY_ES_ML_ENABLED` ile açılır (`config.go:904-905`) |

Kapsam dışında kalan, Problem açan yollar (anomali tespiti değil, yerinde kalır):
- alarm kuralları (`evaluator.go:771`; 9 `builtin-*` varsayılan kapalı)
- SLO burn (`slo:<id>:<sev>`, varsayılan kapalı)
- exception storm (`exception_storm.go:31,89`)
- `exception:shared-dependency` ve `exception:fatal-infrastructure`
- exception bildirimi (`notify/exception_notifier.go`)

## 3. Neden istenildiği gibi çalışmıyor — koddan çıkan nedenler

N1. **Terfi süpürmesi hiç koşmuyor.**
- `promoteStrongAnomalies` yalnız yerel `metric_anomaly` açıksa koşuyor (`evaluator.go:712`).
- Varsayılanla bu kapalı. Bu yüzden anomaly_events'e yazan hiçbir yol Problem'e ulaşmıyor: op_latency (açık), elastic_ml, `ml:*` ve log_template_new.
- Satırlar yalnız `/anomalies` listesinde kalıyor.

N2. **ML olayları gölgede kalıyor.**
- Terfi için ana anahtar, dedektör anahtarı ve terfi anahtarı birlikte açık olmalı. Üstüne karnede ≥ 20 etiketli örnek ve ≥ 0.8 isabet gerekiyor (`ml_settings.go:366-388`).
- Etiket yoksa terfi asla olmaz (`learning-loop-audit.md:15-21`).
- Terfi açılsa bile N1 yüzünden Problem açılmaz.
- ML doğrudan Problem açmıyor ve bildirim göndermiyor (`ml_events.go:4-9`).

N3. **`scan()` erken dönüyor ve ilgisiz kontrolleri de susturuyor.**
- `metric_anomaly` kapalıyken `scan()` erken dönüyor (`anomaly.go:521`).
- Bu dönüş arkasındaki şu çağrıları da atlıyor: behavior, özneler, heap, sessizlik ve **exception storm** (`anomaly.go:797-810`).
- Oysa `native_detectors.go:6-11` başlığı storm'u "her zaman açık" diye listeliyor. Bu bir çelişki ve muhtemel hata; çalışma anında doğrulanmadı.
- Davis F3 özneleri, kip "live" olsa da yerel kapı kapalıyken koşmuyor.

N4. **Aynı olay dedektörler arasında birleşmiyor.**
- ML'de bir servis yavaşlaması trace_red/op_latency, dependency_latency ve trace_change_point olarak ayrı kimliklerle ayrı satırlara düşüyor.
- `confirmers()` (`quality.go:202-217`) yalnız kanıta "teyit" yazıyor, satırları birleştirmiyor.

N5. **Aynı şeyi ölçen iki yol farklı kaynaktan farklı cevap veriyor.**

| Yerel yol (kaynak) | ML karşılığı (kaynak) |
|---|---|
| op_latency (CH MV, tail-sampled span) | trace_red op_latency (VM) |
| trace_op | op_error_rate |
| log_pattern / log_template_new | template_spike / rare_template |
| behavior_change | change_point |
| yerel service_silent | ML log service_silent |
| subjects db/queue | dependency_latency + metric_kafka |

- `svc-slowdown`, op_latency olaylarını bilerek bastırmıyor (`aiops/detectors.md:15`).

N6. **Bazı yolların kaynağı prod'da boş.**
- db_health, db_capacity, db_slow_statement ve runtime_pods CH gauge okuyor. Prod CH'de metrik yok (bellek: metrikler yalnız VM'de).
- `metric_jvm` boş: JVM metrikleri Thanos'ta (`ml-next.md:33`).
- CH log yolu boş: prod CH'de log yok.

N7. **Koşu durumu süreç içinde tutuluyor.** Lider değişince ML epizot koşuları sıfırlanıyor (`quality.go:139-140`). Bildirim dedup'ı da pod başına bellekte (`notify/problem_dedup.go`).

N8. **Tekilleştirme katmanları kapalı veya gölgede.**
- Incident bağlama kapalı: `IncidentsEnabled` false olunca `incident.go:385` nil dönüyor.
- `notify_policy` varsayılanı shadow.
- Kümeleme ise açık ama yalnız ≥ 3 üye olunca devreye giriyor (`clustering.go:23-50`).

N9. **Belge ile kod kaymış.** `native_detectors.go:10-11` op_latency'yi "varsayılan kapalı" diyor. Kod v0.10.1085'ten beri açık.

Koddan çıkmayanlar operatöre soruluyor (§8): prod'da hangi anahtarların açık olduğu ve "çalışmıyor" denen somut olaylar.

## 4. Çakışma haritası (tekilleştirme delikleri)

| Mekanizma | Yer | Kapsadığı | Delik |
|---|---|---|---|
| anomaly_events fp + epizot (22 dk 30 sn boşluk) | `anomaly_event.go:319` | aynı kind/pattern/servis | farklı kind'lar aynı olayda ayrı satır |
| ML epizot (3 tik kapanış, 30 dk içinde yeniden açılış) | `quality.go:112-199` | aynı dedektör × servis × bölüm | dedektörler arası yok (N4) |
| `dup_of` | `promote.go:34-45` | yalnız `ml:*` ile `elastic_ml` | ML ile yerel yollar arasında yok |
| `(rule_id, service)` açık arama | `problem.go:1809,1871` | aynı kural | her yolun rule_id ailesi farklı |
| kümeleme | `clustering.go:23-50` | ≥ 3 topoloji bağlı açılış | 1–2 servisli olaylar |
| incident bağlama | `incident.go:381` | servis / 1-hop, 30 dk | varsayılan kapalı |
| bildirim dedup | `problem_dedup.go` | aynı Problem × kanal × durum | farklı Problem id'leri; pod başına bellek |

Senaryo: tek bir servis yavaşlaması bugün en kötü durumda kaç kayda dönüşür?
- ML tarafında 3 satır: trace_red, dependency_latency (çağıran serviste) ve trace_change_point.
- Yerel tarafta 2 satır: op_latency ve behavior_change.
- Elastic ML job'u açıksa 1 satır daha.
- Yerel anahtarlar açıksa 3'e kadar Problem: `anomaly:<svc>:latency`, `svc-slowdown:` ve `anomaly-auto:<fp>`.
- Ayrıca kullanıcı tanımlı bir alarm kuralı varsa ayrı bir Problem ve ayrı bir bildirim.

Yani varsayılanla 5–6 liste satırı çıkıyor ve sıfır Problem açılıyor. Yerel anahtarlar açıldığında aynı olay 3–4 Problem açıyor. Tek doğru ara durum yok.

## 5. Emeklilik / kalma kararı

| Yol | Karar | Gerekçe |
|---|---|---|
| ML log, trace ve metric aileleri | KALIR (tek motor) | VM/ES kaynaklı, sampling'den etkilenmez, epizot + karne + replay var |
| metric_anomaly, error_ramp, calendar | SİLİNİR | trace_red ve throughput_drop aynı işi yapıyor; Türkiye takvimi ML'de var (`log-ml-audit.md:112`) |
| trace_op, op_latency, service_slowdown | SİLİNİR | CH span'i örneklenmiş, ML trace_red VM'den ölçüyor (N5) |
| log_pattern, log_template_new | SİLİNİR | template_spike ve rare_template |
| behavior_change | SİLİNİR | change_point |
| service_silent (yerel) | SİLİNİR | ML log service_silent var; varsayılan kapalı kalır |
| heap bandı, runtime_pods, db_health, db_capacity, db_slow_statement | SİLİNİR (S5) | kaynak prod'da boş; DB yüzeyleri (sayfalar) yerinde kalır, taşınma önerilmiyor |
| özneler (Davis F3) | SİLİNİR (S4); **`spec-davis-faz3.md`nin yerine geçer** | dependency_latency + metric_kafka + trace_red; prod'da gölge gözlem sonucu yok |
| kümeleme | SİLİNİR; **`spec-anomaly-clustering.md`nin yerine geçer** (learning-loop S3 zaten öngörmüştü) | ML grubu tek Problem açar |
| `ml/promote.go` kopya yolu | SİLİNİR | grup doğrudan Problem açar; kopya tablosu gerekmez |
| Elastic ML poller | SİLİNİR (S3) | env'le kapalı; ikinci motor |
| exception storm | KALIR, `scan()`'den çıkarılır | anomali tespiti değil (N3) |

## 6. Hedef model — seçenekler

**A. Okuma tarafında birleşik görünüm.** İki tablo kalır, Go iki tabloyu birleştirip tek liste ve tek detay gösterir.
- Bedeli düşük: 2–3 sürüm, şema değişmez.
- Ama yapı fazlalığını gidermiyor: 3 motor, 4 terfi yolu ve 4 ayar kalıyor. N1–N5 çözülmüyor.
- Reddedildi.

**B. Tek motor ML + tek olay tablosu `ml_events` + grup → Problem (ÖNERİLEN).**
- Yeni tablo yok.
- `ml_events`'e tek kolon eklenir: `group_id String DEFAULT ''`. Grup = servis × açık pencere ve teyit eden dedektörler; `confirmers()` kuralı genişletilir.
- Tek yazıcı ML lideri olduğu için grup kimliğini yazım anında atar.
- Kolon tüm satırda taşınır: ReplacingMergeTree(version) kuralına göre satır bütün olarak yeniden yazılır, eski kolonlar da taşınır.
- Problem anahtarı `anomaly-auto:group:<group_id>`. Bu önek sayesinde mevcut `anomaly` bildirim türü ve incident yolu değişmez (`notify_kind.go:84`).
- Kalan tek terfi yolu evaluator'da yeni bir `promoteMLGroups` olur, eski süpürme silinir.
- `anomaly_events`'e yazım durur. Tablo 30 g TTL sonrasında DROP edilir.
- Dağıtık risk: `ml_events` `_local` + Distributed çifti değil (`learning-loop-audit.md:496-501`). ALTER ve okuma davranışı prod'un dağıtık CH'sinde doğrulanmadı. D1'de ölçülmeden kolon eklenmez.
- Öğrenme döngüsünde planlanmış bir `anomaly_groups` tablosu ve GroupsTab var. Bu tablo kodda varsa `group_id` kolonu yerine o kullanılır. Varlığı doğrulanmadı.
- Bedel: 8–10 sürüm. Silinen kod en büyük kalem.

**C. Yeni birleşik `anomalies` tablosu, iki motorun ortak yazdığı.**
- "Kayıtlı durum için yeni şema yok" ilkesine aykırı.
- İki motoru da yaşatıyor, göç maliyeti yüksek.
- Reddedildi.

## 7. UI

Liste:
- `/anomalies` tek tablo olur ve satır başına bir grup gösterir.
- Satır başlığı düz cümledir, örneğin "checkout yavaşladı; 3 sinyal teyit etti".
- Açılınca gruptaki dedektör satırları görünür.
- "detectors" sekmesi silinir. "ml" ile "groups" tek listede birleşir.

Detay:
- Tek rota `/anomalies/:id` kalır ve ML detay düzenini kullanır (`MLReplaySheet.tsx`).
- `/anomalies/ml/:id` ve `AnomalyEventDetail.tsx` silinir. Eski bağlantılar için yönlendirme yazılmaz (S6).
- Detay operatörün sade tercihine uyar: önce bir cümle ve "ne zaman" satırı, sonra oynatma. Teknik kanıt kapalı `<details>` içinde, karne de bu kapalı bölümde.

Ayar:
- Admin'deki `native_detectors` ekranı ve `anomaly_sensitivity` ekranı silinir.
- Batch kalıpları ML ayarına taşınır, tek ekran kalır. Mevcut "önerilen kurulum tek düğme" (DECISIONS v1886) bu ekranda yaşar.

## 8. Operatöre sorular (önerilen varsayılanla)

- S1. "İstediğiniz gibi çalışması" ne demek? Kaçırılan ya da yanlış açılan 2–3 gerçek olay örneği (servis, zaman, beklenen sonuç) istiyoruz. *Varsayılan yok; D3 gate'i bu örneklerle ayarlanır.*
- S2. Canlı terfi kapısı: karne etiket istiyor ve etiket yok (N2). *Varsayılan: grup, ≥ 2 farklı dedektörün teyidi ve etki eşiğiyle Problem açsın. Karne terfi kapısı olmaktan çıksın, yalnız susturma için kullanılsın.*
- S3. Elastic ML poller silinsin mi? *Varsayılan: evet. Prod env'inde açık mı, sizden teyit gerekiyor.*
- S4. Davis özneleri silinsin mi? Bu Davis F3'ün yerine geçer. *Varsayılan: evet, ML grubu canlıya geçtikten sonra.*
- S5. db_*, runtime_pods ve heap dedektör kodu silinsin mi? Kaynakları prod'da boş. *Varsayılan: evet. DB sayfalarına dokunulmaz.*
- S6. Eski `/anomalies/ml/:id` bağlantıları yönlendirilsin mi? *Varsayılan: hayır, kaldırılsın.*
- S7. Exception storm'un `metric_anomaly` kapısına bağlı olması bilinçli miydi? *Varsayılan: hayır. Storm kapıdan ayrılsın (D0).*
- S8. Prod değerlerini paylaşır mısınız? Gerekenler: `ml.enabled` ve açık dedektörler, terfi anahtarı, `native_detectors`, `subjects.promotionMode`, `incidents.enabled`, `notify_policy.mode`, `COREMETRY_ES_ML_ENABLED`.

## 9. Dilim planı (her dilim kendi sürümü, tek commit ile geri alınır)

Bu dilimler yeni ES/VM sorgusu eklemiyor. D2–D3 yalnız mevcut `ml_events` satırlarını ve süreç içi koşu durumunu kullanıyor.

| Dilim | İçerik | Davranış değişir mi |
|---|---|---|
| D0 | exception storm (ve gerekiyorsa sessizlik) `scan()` erken dönüşünden ayrılır; `native_detectors.go` yorumu düzeltilir | **OK gerekir** (storm prod'da açılmaya başlayabilir) |
| D1 | Ölçüm, kod yok. Prod'da 7 günlük sayım: dedektör başına `ml_events` satırı, teyitli küme sayısı, `anomaly_events` kind dağılımı. `ml_events`'in dağıtık CH'de hangi düğümde durduğu (`system.tables`) | hayır |
| D2 | ML'de dedektörler arası gruplama + `group_id` yazımı (veya `anomaly_groups`), yalnız gölgede; liste bunu gösterir | hayır (yalnız yeni alan) |
| D3 | `promoteMLGroups`: gölge kipte "açardı" kaydı; ardından S1 örnekleriyle replay karşılaştırması | hayır |
| D4 | Grup → Problem canlı (`anomaly-auto:group:`), bildirim mevcut hattan | **OK gerekir** |
| D5 | UI tek liste + tek detay; detectors sekmesi, `/anomalies/ml/:id` ve `AnomalyEventDetail.tsx` silinir | **OK gerekir** |
| D6 | Yerel dedektörler, `native_detectors` ayarı, Admin ekranı ve eski terfi süpürmesi silinir | **OK gerekir** |
| D7 | Özneler + kümeleme silinir (Davis F3 ve clustering spec'lerinin yerine geçer) | **OK gerekir** |
| D8 | Elastic ML poller + `ml/promote.go` silinir | **OK gerekir** |
| D9 | `anomaly_events` yazımı ve API'leri (`/api/anomalies/events`, `/event`, `/active`) silinir; 30 gün sonra DROP | **OK gerekir** |

## 10. Kalkanlar (silinecekler — geriye uyum katmanı yok)

Kod:
- `internal/anomaly/`: `anomaly.go` metrik taraması (error_ramp, calendar, service_silent), `recorder.go` yerel yollar, `trace_ops.go`, `op_latency.go`, `behavior*.go`, `subjects*.go`, `anomaly_subject_group.go`, `heap_scan.go`, `clustering.go`
- `internal/evaluator/`: service_slowdown, db_*, runtime_pods, `promoteStrongAnomalies`, `anomaly_subject_sweep.go`
- `internal/ml/promote.go`
- `internal/elasticml/`
- `internal/api/native_detectors.go`
- Yerel anomali API uçları ve frontend sorguları: `logPatternAnomalies`, `traceOpAnomalies`, `metricAnomalies`, `anomalyEvents`

Tablo: `anomaly_events` (TTL sonrası). `anomaly_verdicts` kalır, çünkü ML karnesi onu kullanıyor (`ml_events.go:262-286`).

Ayar:
- `native_detectors`
- `anomaly_sensitivity`: batch kalıpları `ml` ayarına taşındıktan sonra
- `COREMETRY_ES_ML_ENABLED` ve `COREMETRY_ES_ML_MIN_SCORE` (`docs/ENV.md:192`)

UI: detectors sekmesi, groups sekmesi (listeye katılır), `/anomalies/ml/:id`, `AnomalyEventDetail.tsx`, Admin'deki yerel dedektör ve anomali hassasiyeti ekranları.

Kalanlar:
- `internal/ml` ve onun tabloları: `ml_events`, `ml_series`, `ml_event_snapshots`, `ml_baseline_obs`
- `problems`, alarm kuralları, SLO burn, exception akışı, bildirim hattı, `root_cause_hypotheses`

## 11. Doğrulanmadı / ölçülmedi

- §3'teki bulgular kod okumasından çıktı, hiçbiri çalışma anında denenmedi: `scan()` erken dönüşünün storm'u gerçekten susturduğu (N3), yerel kapı kapalıyken terfi süpürmesinin hiç koşmadığı (N1).
- Prod ayar değerlerinin hiçbiri bilinmiyor (S8).
- `ml_events`'in dağıtık CH'deki yerleşimi ve ALTER güvenliği bilinmiyor; tabloda ON CLUSTER DDL bulunamadı.
- `anomaly_groups` tablosunun varlığı ve GroupsTab'ın veri kaynağı bulunamadı.
- `getAnomalyEvents` SQL'i (`api.go:9473`, büyük dosya) okunmadı.
- `r.logs`'un ES/CH seçimi okunmadı.
- `trace_ops.go:935` ve `op_latency.go:486`'daki `FROM spans` kullanımı okunmadı; muhtemelen örnek trace aramasıdır.
- Evaluator'da `ml:*` kind'larına özel bir kapı olup olmadığı okunmadı.
- behavior olaylarının "fusion" tüketicisi izlenmedi.
- ML dedektörlerinin isabeti ölçülmedi. Etiket olmadığı için karne boş, isabet bilinmiyor.
- Senaryo sayıları (§4) rule-id şekillerinden çıkarıldı, yeniden üretilmedi.
