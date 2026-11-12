# Davis özneleri → ML motoru: yetenek boşluğu (2026-11-12)

Amaç: "özneler" yolu (`internal/anomaly/subjects*.go` → `anomaly_events`) silinmeden önce her yeteneğin
`internal/ml` → `ml_events` → /anomalies üzerinde karşılığı olsun. Salt-okuma incelemesi; kod değişmedi.
Kanıtlar koddan; spec belgeleri yalnız başlık düzeyinde tarandı.

## Doğrulanan iddia

`docs/anomaly/anomaly-reassessment-audit.md:154` "özneler = dependency_latency + metric_kafka + trace_red" diyor.
**Kısmen yanlış.** Üçü de servis anahtarlı; hiçbiri DB'yi ya da topic'i ayrı bir varlık olarak tutmuyor:
- `dependency_latency` (`internal/ml/dependency.go:39-43,128`): çağıran servis × hedef, yalnız istemci p95. Hata oranı yok.
- `metric_kafka` (`internal/chstore/ml_settings.go:80-82`): `max by (svc)`. Topic yok, consumer group yok.
- `trace_red` (`internal/ml/trace_red.go:3-12`): servis × rota. Gecikmeyi p95/p99 ile değil, "1 sn üstü istek payı" ile ölçüyor (`:94,:233`).

## Yetenek tablosu

| # | Yetenek | Ne yakalar | Anahtar | Kaynak | Yön | Eşikler | Mevsimsellik | ML karşılığı | Hüküm |
|---|---|---|---|---|---|---|---|---|---|
| 1 | Rota gecikmesi | Rotanın p99'u (terfide p95) tabandan saptı | servis × http_route, topN 20 | VM RED, `subjects_scan.go:120-167` | yukarı | z≥3.5, 3 kova (15 dk) | gün sınıfı + saat | `trace_red` op_latency `trace_red.go:94,233` | **kısmi**: p95/p99 yerine 1 sn üstü istek payını ölçüyor; 50→400 ms gibi bir sıçramayı görmez |
| 2 | Rota hata oranı / 5xx | Rotanın hata oranı arttı | servis × rota | VM RED | yukarı | z≥3.5, 3 kova | aynı | `trace_red` op_error_rate `trace_red.go:350` (≥0.01, sunucu 5xx etiketi) | **birebir** (z 4 / 2 kova) |
| 3 | Rota istek hızı | Trafik arttı ya da düştü | servis × rota | VM RED | iki yön (`anomaly.go:144`) | aynı | aynı | `throughput_drop` `trace_red.go:322` | **kısmi**: yalnız düşüşü görür. Artış bilerek eklenmez; Davis de terfi ettirmiyordu (`anomaly_subject_group.go:73-79`) |
| 4 | DB gecikmesi | DB örneğinin p99'u saptı | db_system + instance | CH `db_summary_5m` (`subjects_ch.go:36`) | yukarı | z≥3.5, 3 kova | gün sınıfı; takvim yok (`subjects_ch.go:176`) | `dependency_latency` db ailesi `dependency.go:128` | **kısmi**: anahtar çağıran × db_namespace, ölçü istemci p95; instance yok |
| 5 | DB hata oranı | DB çağrılarında hata oranı arttı | db_system + instance | CH `db_summary_5m` | yukarı | aynı | aynı | YOK | **yok** |
| 6 | Kuyruk gecikmesi / hızı | Topic'in p99'u ya da hızı saptı | sistem + destination | CH `messaging_summary_5m` | p99 yukarı, hız iki yön | aynı | aynı | `dependency_latency` messaging ailesi | **kısmi**: yalnız istemci p95 (çağıran × destination); topic hızı yok |
| 7 | Kuyruk hata oranı | Topic'te hata oranı arttı | sistem + destination | CH `messaging_summary_5m` | yukarı | aynı | aynı | YOK | **yok** |
| 8 | Kafka consumer lag | Tüketicinin topic lag'i arttı | tüketici servis × topic, topN 20 (son 1 sa ort. lag) | VM `kafka.consumer.records_lag_max` (`subjects_kafka_lag.go:66-112`) | yukarı | lag ≥1000 (`:352`), z≥3.5, 3 kova | takvimli (`:291`) | `metric_kafka` consumer_lag, taban 1000 `ml_settings.go:140`, `quality.go:95-103` | **kısmi**: servis düzeyinde tüm topic'lerin max'ı; hangi topic belli değil, küçük topic'teki artış büyük topic'in altında kalır |
| 9 | Yön + taban değeri ekranda | Olayda "yukarı, taban X" | olay | `anomaly_events.direction/baseline_value` | — | — | — | `ml_events.direction/baseline/observed` `chstore/ml_events.go:54-57`; UI `AnomalyEventDetail.tsx:284-285`, `anomalyDetailParts.tsx:70-86` | **birebir**. Not: `trace_error_count` tabanı medyan değil, normalMax |
| 10 | Taban türü / "taban yok" | Tabanın mevsimsel mi, ardışık mı, hiç yok mu olduğu (`verdict.go:265`, `lib/anomalyBaseline.ts`) | olay | — | — | — | — | YOK. ML 20 örnekten azsa susar; aynı sınıftan 3 gün yoksa tüm günlere düşer, ama bunu yazmaz (`log_red.go:164`) | **kısmi**: soğuk başlangıç bilerek sessiz, sorun değil; eksik olan, düşülen tabanın etiketlenmemesi |
| 11 | Yalnız-sayım kipi | Taban 0 iken oran yerine sayıya bakar (`anomaly_subject_group.go:81-85,104`) | olay | — | yukarı | CountOnlyMinCount | — | `trace_error_count` `trace_errors.go:114-141` (mutlak sayı, ≥5, sıfır dolgulu taban) | **kısmi**: giriş span'larında birebir; DB/kuyruk hatalarında yok (satır 5/7 ile gelir) |
| 12 | Mevsimsel saat dilimi + takvim | Gün sınıfı yerel saatle hesaplanır, tatiller operatör takviminden gelir | — | `SeasonalTimezone` varsayılanı Europe/Istanbul (`chstore/anomaly_sensitivity_subjects.go:87`); `anomaly/calendar.go` | — | yarım gün | 3 sınıf + saat ± yarıçap | `anomaly.CalendarDayClass`, 5 sınıf, saat ±1, 21 gün (`log_red.go:164-207`) | **kısmi**: takvim ortak. ML'de saat dilimi boşsa UTC; Istanbul varsayılanını devralıp almadığı doğrulanmadı. Hiçbir yolda yerleşik bayram listesi yok |
| 13 | Terfi kapısı | Tek olay ≥600 sn sürdü, ≥2× saptı ve yön kötüleşme → Problem (`anomaly_subject_group.go:97`) | olay | — | yalnız kötüleşme | 600 sn, 2×; kip off/shadow/live | — | `group_gate.go:10-17` yalnız shadow; ≥2 farklı dedektör ister; 2× yalnız önceliği belirler | **kısmi**: tek dedektörlü uzun sapma hiç "açılırdı" olmaz |
| 14 | Ardışık pencere | Açılış için art arda kovaların tümü sapmalı | — | `verdict.go` | yön dönüşü engeller | Dwell 3 | — | `judge` dwell 2, tüm kovalar z≥4, yön dönüşü yok (`log_red.go:94-160`) | **birebir** (10 dk'ya karşı 15 dk) |
| 15 | Seyrek seri tabanı + dwell | Trafiksiz kova varsa açmaz; taban hızı altı sayılmaz | — | `verdict.go:89,113-116` | — | minBaselineRate | — | MinBaselineRate 5, MinBaselineSamples 20; VM boşluğu = değerlendirme yok (`quality.go:75-88`) | **birebir** |
| 16 | Rota gruplama | Aynı servis/metrik/yön, ±15 dk, 5 rota | servis | `anomaly_subject_group.go` | — | — | — | `group.go:10-19` (aynı servis, 10 dk zincir) | **birebir** |
| 17 | `-batch` yük kuralı | Batch servisinde yük artışı olay açmaz | servis | `subjects_eval.go:105-112` | — | — | — | `Load` bayrağı `ml_settings.go:62-65` + `group_gate` batch kuralı | **birebir** |

Sayım: **birebir 6, kısmi 9, yok 2**.

## Eklenecekler (sıra = öncelik)

Ortak kurallar: yeni ES yükü yok. Metrik yalnız VM'den okunur. CH span okuması yalnız hata sayımı için meşru,
çünkü başarılı trace'ler collector'da %5 örnekleniyor, hatalılar %100 CH'de.
**Bu yüzden `db_summary_5m` / `messaging_summary_5m` ML'e taşınmaz.** O MV'lerden çıkan hata oranı yaklaşık 20 kat şişik,
p99 de hatalı span'lara kayık. Davis satır 4-7 bu yanlılıkla çalışıyordu.
Tüm ML dedektörleri varsayılanda kapalı (`ml_settings.go:248-260`); yeni dilimler de kapalı gelir, ilgili dedektör ayarını izler.

**S1 — `trace_red` p95 ölçüsü** (satır 1)
- Ne eklenir: op_latency'nin yanında `op_p95` ölçüsü. p95 zaten sorgulanıyor (`trace_red.go:215`), yani **ek VM sorgusu yok**.
- Anahtar: mevcut servis × rota `|p95` serisi (rota tavanı 5000, `:35`).
- Yön: yukarı. Mutlak taban olarak p95 ≥ 100 ms konur, gürültü kesilir.
- /anomalies cümlesi: "checkout · POST /pay: p95 420 ms, taban 60 ms (7×), 10 dakikadır."
- Grafik: `MLSourceChart` → `HttpChartPanel`.
- Açık/kapalı: op_latency açıksa açık, çünkü maliyeti sıfır.

**S2 — `dependency_error_rate`** (satır 5, 7 ve 11'in DB/kuyruk kolu)
- Ne eklenir: `dependency_latency`'nin db ve messaging ailelerinde `_count{error_type!=""} / _count`.
- Anahtar: çağıran × hedef (db_namespace / destination). Sınır mevcut: servis başına topk 20 (`dependency.go:128`), toplam tavan 10000 (`:111`).
- Maliyet: aile başına tick'te +1 sorgu, 3 aile için +3. Servis partileri 50'lik (`:147`).
- Yön: yukarı. Gözlenen ≥ 0.01 ve hatalı istek ≥ 5 şartı aranır; taban 0 ise yalnız-sayım koluyla çalışır (satır 11).
- /anomalies cümlesi: "orders → pg/orders_db: hata oranı %4,2, taban %0,1."
- Açık/kapalı: kapalı, operatör açar. Önce etiket varlığı doğrulanmalı (aşağıya bakın).

**S3 — `metric_kafka` topic anahtarı** (satır 8)
- Ne eklenir: consumer_lag için `max by (svc, topic)` serisi. Partition/client_id max ile katlanır, Davis'teki gibi.
- Sınır: topk 20, son 1 saatteki ortalama lag > 0 olanlar arasından seçilir. Seçim sorgusu 30 dakikada bir, ardından tick başına 1 sorgu.
- Lag tabanı 1000 aynen kalır.
- Kod: `metric_ml` iki etiketli anahtarı taşımalı (bugün yalnız `by ({svc})`); topic `ml_events.code`'a yazılır.
- /anomalies cümlesi: "billing-consumer · topic invoices: lag 48.000 kayıt, taban 1.200."
- Açık/kapalı: metric_kafka açıksa açık. Servis düzeyindeki seri, topic serisi gelince emekli edilir; çift olay olmaz.

**S4 — `group_gate` tek-dedektör kolu** (satır 13, 11)
- Kural: tek olay ≥600 sn sürdü, observed/baseline ≥2 ve yön kötüleşme ise "açılırdı" sayılır. Taban 0 ise oran yerine observed ≥ CountOnlyMinCount bakılır.
- Davis gibi request_rate ve `throughput_drop` hariç tutulur.
- Ek sorgu yok. Yalnız shadow; canlı Problem ayrı karardır, Davis'te de varsayılan off'tu.

**S5 — taban türü etiketi** (satır 10)
- `evidence.baselineKind`: mevsimsel / tüm günler (sınıf yetersiz) / normalMax / pod medyanı.
- UI'daki "Yön · taban" satırına "taban: tüm günler" gibi bir ek gelir.
- Ek sorgu yok.

**S6 — saat dilimi devri** (satır 12; silmenin ön şartı)
- ML takviminin saat dilimini `SeasonalTimezone` ile aynı kaynaktan ve aynı varsayılanla (Europe/Istanbul) okut.
- `DefaultSeasonalTimezone` / `ValidateSeasonalTimezone` öznelere ait dosyadan ortak bir dosyaya taşınır.

## Silinebilir

**Hemen silinebilecek bağımsız parça yok.** Dosyalar türler arasında paylaşılıyor (`subjects.go`, `subjects_scan.go`, `subjects_eval.go`); bu yüzden sökme tür tür, ayarla kapatılarak yapılır. Sıra:
- **S1'den sonra:** endpoint türü (VM rota seçimi, `subjects_scan.go:120-167`) ve endpoint gruplaması.
- **S2'den sonra:** `subjects_ch.go`, `DBSubjectID` / `QueueSubjectID`.
- **S3'ten sonra:** `subjects_kafka_lag.go`, `subjectMetricDirections` (`anomaly.go:150-157,169-171`), `newKafkaLagSource` (`anomaly.go:481`).
- **S4'ten sonra:** `evaluator/anomaly_subject_group.go`, `anomaly_subject_sweep.go`, `evaluator.go:1562` çağrısı, `problem_subject_lane.go`.
- **Son adım:** `anomaly.go:827-828` çağrısı, `chstore/anomaly_sensitivity_subjects.go` (S6 bittikten sonra), API doğrulama + audit detayı (`api/anomaly_sensitivity.go:114,140-156`), `anomaly_red_source.go:67`, FE `SubjectsSubsection.tsx` + testleri.
- **Asla silinmez (servis yolu ve explain ile ortak):** `seasonal_tz.go`, `calendar.go`, `chstore/anomaly_calendar.go`, `evaluateAnomaly`, `fillDwellGrid`, `red_vm_*`, `anomaly_event_direction.go` sabitleri.
- `anomaly_events` içindeki `subject_*` / `baseline_kind` kolonları veri olarak kalır; şema geri alınmaz.

## Emin olmadıklarım

- Prod VM'deki `db.client.operation.duration` ve `messaging.client.operation.duration` histogramlarında `error_type` etiketinin dolu olup olmadığı. S2 buna bağlı; yoksa DB/kuyruk hata oranının VM'de kaynağı yok. CH yolu sampling yüzünden yanlı.
- ML takviminin saat dilimini nereden okuduğu: `AnomalySensitivityForDetectors` mı, `SeasonalTimezone` mı? Boş olduğunda UTC mi kalıyor?
- Prod'da `anomaly_sensitivity.subjects` açık mı, `promotionMode` shadow mu? Açıksa sökme, operatörün gördüğü olayları keser.
- Endpoint topN'inin servis başına mı, toplam mı olduğu (`red_vm_query.go:211` okunmadı). Davis'in metrik başına mutlak tabanları (`chstore/anomaly_sensitivity.go` metrik tablosu) da okunmadı.
- dependency/metric olaylarının detayında `MLSourceChart`'ın hangi paneli çizdiği. /anomalies'in eski `anomaly_events` özne satırlarını sökmeden sonra göstermeye devam edip etmediği.
- Spec'teki "2 ardışık pencere" kuralı kodda ayrı bir sembol olarak bulunmadı. Satır 14'te dwell kovalarının ardışıklığı olarak ele alındı (`davis-parity-audit.md:446`).
