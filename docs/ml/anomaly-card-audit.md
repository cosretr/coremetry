# Anomali kartı denetimi — Faz 0 (kod yazılmadı)

Tarih 2026-11-15 · kod `v0.10.2195` (worktree `rel`) · durum: **onaylandı (2026-11-15), sırayla; H1'den başlanır** (§12).

**Yöntem ve sınır.** Yalnız kod okundu; prod/cluster erişimi yok, hiçbir sorgu ya da test koşulmadı.
Prod örneklerine dair zaman hesapları koddan **çıkarımdır**, satırla doğrulanmadı (öyle işaretlendi).
`†` = keşif ajanının raporundan alındı, satırı ben yeniden okumadım. Yollar repo köküne göre.

**Alınamayan girdiler (operatör mesajı kesik geldi):** soru 9–12; kabul ölçütü 1–6; ölçüt 8'in sonu
("…onset_at yükselişin başladığı yer" sonrası); üçüncü örnek `b4d95f…` (id kesik, ayrıntı ve consumer-lag
bağlamı yok); Dilim F "hızlı şerit" tanımı; "ML işleri SRE rolü pod'unda k…" cümlesi. Hepsi §12'de açık madde.

## 1. Kök neden — tek cümle

Bir anomali kartında **dedektörün baktığı veri**, **kartın çizdiği seri** ve **kartın yazdığı sayılar** üç ayrı
yerden gelir; üçü arasında sözleşme yok. `ml_events` satırı yalnız iki skaler (`baseline`, `observed`) ve
serbest biçimli `evidence` taşır; grafik ve kanıt satırları kart **açılırken** başka sorgularla kurulur.

| | Dedektörün test ettiği | Kartın çizdiği | Kartın yazdığı sayı |
|---|---|---|---|
| Örnek 1 `59d8f32338b5e7b5` (`svc_volume_spike`) | `service_summary_5m` span sayısı, 24 sa toplam / önceki 24 sa | servis istek hızı (VM `/metric-red`, yoksa CH giriş span'i), 1–2 dk adım | iki 24 sa toplamı + eşik aşım saati |
| Örnek 2 `09ecef247b0557ea` (`op_latency` p95) | VM `histogram_quantile(0.95, rate[300s])`, 5 dk kova, son 2 kova | önce CH örneklenmiş span şeridi (1 dk); boşsa kayıtlı `ml_series` 5 dk görüntüsü + q25–q75 çizgileri | son kovanın değeri (107) + ±1 sa havuz medyanı (43); tepe (≈135) "başlangıç"tan önce |
| Örnek 3 `b4d95f…` (alınamadı; consumer lag varsayımı) | VM `max by (svc,topic)(…lag_max)` kova sonu anlık değer | Messaging panelinin sorgusu, pencereye göre adım | başlıktaki sayı render anında panel tepesiyle **değiştirilir** (`mlValueText.ts:94`) |

Çözüm dedektör dedektör yama değil, **tek olay-kaydı sözleşmesi** (§9, Dilim H): kapılar, kart ve replay yalnız onu okur.

## 2. Örnek 1 — hacim sıçraması (Soru 1–4)

**S1. Dedektör.** `svc_volume_spike` bir ML dedektörü değil; **evaluator**'ın self-health kuralı
(`internal/evaluator/selfhealth_volume.go`), worker pod'unda koşar (`main.go:705-707`, `if mode.worker`).
- Sorgu: `ReadServiceVolumeWindows` (`internal/chstore/selfhealth_volume.go:57-80`) — `service_summary_5m`,
  `countMergeIf(span_count_state)`; `cur` = son 24 sa, `prev` = ondan önceki 24 sa; çapa = tamamlanmış son 5 dk kova;
  `HAVING cur >= minSpans`, `LIMIT 500`, `max_execution_time = 20`. **Tüm span'ler** sayılır (kind ayrımı yok).
- Pencere 24 sa (`selfhealth_volume.go:58`), ölçüm **saatte bir** (`:70`), arada sonuç bellekten sunulur (`:183-214`).
- Eşik: `cur >= prev × 4.0` ve `cur >= 100.000` ve `prev > 0` (`:96-107`; varsayılanlar `internal/chstore/selfhealth.go:110-111`).
- Taban: **önceki 24 saat**. Takvim/gün sınıfı/hafta günü yok. Batch adlı servisler düşer (`:146-155`).
- Çıktı: `ml_events` satırı, `stage=shadow`, `signal=trace`, `window_sec=3600`, `baseline=prev`, `observed=cur`,
  `score=cur/prev`, evidence `{prev,cur,factor,threshold,measure_window_sec,min_spans}` (`:236-270`). Snapshot yazmaz.

**S2. started_at / ended_at.**
- `started_at` = servis sıçrama listesinde **ilk görüldüğü tikin `now`'ı** (`planVolumeEvents`, `:304-307`) — yani eşik aşımının
  fark edildiği saatlik tarama; değişim anı değil.
- Bitiş = taze taramada koşul artık sağlanmadığında aynı satır `stage=closed`, `last_seen=now` (`:316-328`).
- **24 sa toplam neden 2 sa'lik olay verir (hesap, doğrulanmadı):** düz seride T0 anında k katlık kalıcı sıçrama için
  oran(t) = 1 + (k−1)·t/24 (t ≤ 24 sa). Eşik 4'ü t = 72/(k−1) saatte aşar; t > 24'te sıçrama `prev`'e girmeye başlar,
  oran 24k/(24+(k−1)(t−24)) ile düşer ve t = 24 + 6(k−4)/(k−1)'de 4'ün altına iner. Başlıktaki 4,2 kat için: aşım
  **22,5 sa sonra**, düşüş 24,4 sa sonra → olay ≈ 1,9 sa. Saatlik tarama + tik kayması (her tarama ~1 dk geç: `:188`)
  = 10:48 → 12:50. Yani gerçek değişim büyük olasılıkla **bir önceki gün ~12:20**; "bitti" denen anda seri hâlâ yeni
  seviyede. Genel sonuç: k < 4 hiç yakalanmaz; her epizot değişimden 24–30 sa sonra **pencere kaydığı için** kapanır.

**S3. "Olayın seyri" serisi.** Dedektörün serisine bağlı **değil**; dedektör adına göre sabit bir servis paneli seçilir:
`sourcePanelFor` (`frontend/src/components/coremetry/anomalies/mlSourcePanel.ts:50`), `svc_volume_spike → service-red, agg 'rate'`
(`:69`). `ServiceRedChart` (`MLSourceChart.tsx:278-313`) önce `/metric-red` (VM), boşsa CH giriş span'lerini çizer.
Pencere `panelWindow` = başlangıç − 30 dk … bitiş + 15 dk (`mlSourcePanel.ts:160`) → 10:18–13:05; değişim anını içermez.

**S4. "Kanıt" satırı.** Başlıkla **aynı işlev**: grup üyesi yokken tek satır `card.howMuch` (`MLReplaySheet.tsx:174-176`);
`howMuchOf` bu dedektörde `volumeSpikeHead(in) + "."` döner (`internal/ml/explain_card.go:586-587`), başlık da
`volumeSpikeHead` (`:477-478`, gövde `:640-647`). Kaynak etiketi `evidenceSource` log/Thanos/`trace_error_count` dışındaki
her şeye "VictoriaMetrics" der (`mlGroupDetail.ts:20-28`) — veri CH MV'si olduğu hâlde. Grup kapısı bu dedektörü
dışladığı için (`internal/ml/group_gate.go:197`) ikinci kanıt satırı yapısal olarak hiç gelemez.

## 3. Örnek 2 — sığ grafik, tek kovalık olay (Soru 13–16)

**S13. Kart sayfasındaki çizim yolları** (`/anomalies/ml/:id`, `MLReplaySheet.tsx`): 6 ayrı grafik bileşeni, 10 bağlama noktası.

| # | Yol → bileşen | Dosya:satır | Ne zaman | Kaynak · adım |
|---|---|---|---|---|
| 1 | `KafkaLag` → `HttpChartPanel` | `MLSourceChart.tsx:90-117` | `metric_kafka` + `consumer_lag`, cluster'sız | VM, Messaging paneli sorgusu · `stepSeconds(pencere)` (kısa pencerede < 60 sn) |
| 2 | `TracesStrip` → `VolumeChart` | `:121-160` | `op_latency`, `op_error_rate`, `throughput_drop`, `trace_change_point`, `trace_error_count` | CH **örneklenmiş** span (`spanMetricBatch`) · 60 sn |
| 3 | `MetricSeries` → `HttpChartPanel` | `:193-241` | diğer `metric_*`, `dependency_*`, `trace_pod_outlier` | `/api/ml/source-series` (VM/Thanos) · 60 sn, pencere 120 sn |
| 4 | `LogsChart` → `LogsHistogram` | `:167-186` | `signal=log` ve kartta Logs bağlantısı varsa | ES · ≤3 sa'de 1 dk |
| 5 | `ServiceRedChart` → `HttpChartPanel` | `:278-313` | `svc_error_rate`, `svc_breadth`, `svc_volume_spike` | VM `/metric-red` (rateWindow 180) → yoksa CH giriş span'i |
| 6–7 | kayıtlı görüntü: `MLEventPlayer` (compact) / `MLLogPlayer` → `TimeChart` | `MLReplaySheet.tsx:177-184`, `MLEventPlayer.tsx:98` | panel `snapshot` türündeyse, istek hata verirse ya da seri boşsa | `ml_event_snapshots.series` · 5 dk |
| 8 | tam `MLEventPlayer` | `MLReplaySheet.tsx:255-258` | Teknik ayrıntı, snapshot var ve log değil | aynı kayıt |
| 9 | `MLTraceHeatmap` / `MLTraceDots` | `:260`, `:272-274` | endpoint'li trace olayı | `course.trace` (`ml_series`) |
| 10 | komşu metrik mini `TimeChart` | `MLSignalEvidence.tsx:126` | metrik olayı, bölüm açılınca | `ml_series` |

Seçim `MLSourceChart.tsx:66-79`. Düşüş metrikte gerekçe satırıyla (`Gap`), **trace/log'da sessiz** (`:146-148`, `:179`).
Örnek 2'de yol 2 boş döndü (VM `route` etiketi ile span adı uyuşmuyor ya da örneklemde trace yok — doğrulanamadı) → yol 6.
Eski olay sayfası `AnomalyEventDetail.tsx:275-282` ayrıca `LegacyLogChart`/`ServiceRedChart` kullanır.

**S14. Kayıtlı görüntü.** Yazıcı yalnız ML recorder (`internal/ml/snapshot.go:262-300`): koşunun ilk tikinde **ön kayıt**,
epizot kapanınca ya da 24 sa sonra **tam kayıt** (yorum "+2 sa" diyor, kod `24*time.Hour`: `:286`). Çözünürlük `cfg.BucketSec`
(300 sn). Pencere `started_at − 1 sa` → şimdi; tam kayıtta `min(şimdi, last_seen + 1 sa)` (`:101-110`). İçerik: `series`
`[{t,v}]`, `thresholds` (z, dwell… + `band` = kova başına `[t,q25,q50,q75]`, `:306-340`), `baseline_med/mad`, `dwell`,
`confirm_ids`, `evidence`, `samples` (≤5 trace, ≤3 log satırı †), `deploys` (≤10). Tik başına en çok 50 yazım (`:20`).
`trace_pod_outlier`, `svc_breadth`, `new_template` serisiz; evaluator olayı hiç yazmaz. `detected_at` kolonuna aslında
`started_at` yazılır (`:142`). **Boyut ölçülmedi**; yapıdan tahmin 5 dk'lık olayda ~25 nokta ≈ 0,8 KB seri + ~1,1 KB bant
+ 1–4 KB kanıt/örnek. Ölçüm sorgusu §7-D.

**S15. Gecikme dedektörü** (`internal/ml/trace_red.go`).
- Kova 300 sn, tik 300 sn (`internal/chstore/ml_settings.go:297`); son kova = ingest payı (120 sn) geçmiş son tam kova (`log_red.go:235-238`).
- Süreklilik: son `DwellBuckets` = **2** kovanın ikisi de aynı yönde `medyan ± criticalZ(4)·effMAD` dışında (`log_red.go:106-172` †).
- Hacim: (a) sayım tabanı ≥ 20 örnek ve medyan ≥ `MinBaselineRate` (`trace_red.go:353-355`); (b) p95 yolu için
  `p95 ≥ 100 ms` ve `%5 × istek/sn × 300 ≥ 5` → kovada ≥ 100 çağrı (`:37-41`, `:121-126`). Çağrı sayısı karta **yazılmaz**.
- Mutlak fark (ms) kapısı **yok** (`ImpactMinAbs` varsayılan 0; göreli %20: `quality.go:104-114`). 107 ms, 100 ms tabanının hemen üstü.
- ⚠ `:353` ifadesi `MinBaselineRate/BucketSec*BucketSec` kendini götürür; seri istek/**sn** olduğundan kapı fiilen "medyan ≥ 5 istek/sn"
  ya da kastedilen "5 istek/kova" — birim belirsiz, ayrı doğrulama ister (§8 B12).
- Tek kovalık kayıt payı: sorgu §7-B. **Dikkat:** aşağıdaki kayma yüzünden "1 kova" = "en çok 2 tikte görüldü".

**started_at kayması (yeni bulgu, çıkarım).** Kova tabanlı dedektörler `StartedAt = son kova başı` verir (`trace_red.go:407`);
`qualityFilter` bunu yalnız koşu başı daha **erkense** değiştirir (`quality.go:372-375`). Koşu başı = ilk tikin duvar saati
(kova başı + 7–12 dk). Sonuç: 1. tik `B1`, 2. tik `B1+5 dk`, 3.–4. tikten sonra ilk tik saatine sabitlenir. `last_seen` =
son kova sonu. Tek/iki tikte görülen olay hep "5 dk" görünür; oysa dwell gereği sapma en az 10 dk sürmüştür ve ilk dwell
kovası "başlangıç"tan **öncedir**. Kapanış satırı son yazılan kopyadır (`quality.go:196-214`): `observed` tepe değil son değerdir.

**S16. 107, ≈135, 43.**
| Sayı | Üreten | Hizalama |
|---|---|---|
| 107 ms | `ev.Observed` = 07:05 kovasının değeri: VM anlık `histogram_quantile(0.95, sum by (svc,route,le)(rate(…_bucket[300s])))`, kova sonunda (`trace_red.go:95`, `:372-376`, `:407`); kartta `howMuchOf` (`explain_card.go:634`) | kova [07:05, 07:10) |
| ≈135 | kayıtlı görüntüdeki 07:00 noktası = aynı sorgunun bir önceki tikte `ml_series`'e yazdığı değer (`trace_red.go:296-299`, `snapshot.go:113-117`) | kova [07:00, 07:05) — aynı sorgu, **başka kova** |
| 43 ms | `ev.Baseline` = aynı gün sınıfı günlerin saat ±1 havuzundaki `ml_series` değerlerinin Go medyanı (`trace_red.go:314-324` †, `chstore/ml_series.go:71-90` †) | kova değil; ~3 saatlik havuz × N gün |

Üç ayrı "normal" var: kart metnindeki medyan (43), grafikteki ince çizgiler (aynı 5 dk diliminin q25–q75'i) ve asıl alarm
sınırı (medyan ± 4·effMAD; sade grafikte hiç çizilmez). Eksen birimsiz: `MLEventPlayer` kayıttaki ham sayıyı çizer.

### 3.1 Örnek 4 — süren olayda grafik hiç yok (`6a03002be1529b57`, v0.10.2204'te düzeltildi)

`acm-portfolio-summary-prod` · `callBagService`, `op_latency` p95 ("İzleniyor · 14 dk", 12:30'dan beri).
Kart: "Olayın seyri: Bu olay için grafik kaydı yok"; kanıt satırı başlığı tekrar ediyor, yanında "VictoriaMetrics".

**Kök neden (koddan, kesin):** dedektör VM RED ailesinden okur ve olayın `Code`'u route etiketinin **değeridir**
(`trace_red.go` `routeLabelCandidates` → gRPC'de `rpc_method="callBagService"`, `event()` `Code: route`). Canlı
grafik ise Traces şeridiydi: `mlSourcePanel.ts` `op_latency` → `kind:'traces'`, `MLSourceChart.tsx` `TracesStrip`
→ `spanMetricBatch` (ClickHouse span'leri) süzgeci `{k:'name', op:'=', v:['callBagService']}`; prod span adı
`com.example.core.grpc.BagService/callBagService` → hiçbir span eşleşmez → `count` boş → sessizce kayıtlı
görüntüye düşer. Olay 14 dk'lık ve 5 dk'lık görüntü henüz yazılmamış (`snapshot.go`) → boş durum. Örnek 2'deki
"yol 2 boş" da aynı uyuşmazlık (HTTP'de `http_route` şablonu ile span adı da aynı biçimde ayrışabilir).

**Düzeltme (v0.10.2204):** `op_latency` / `op_error_rate` / `throughput_drop` grafiği dedektörün kendi serisi:
`/api/ml/source-series?detector=op_latency&code=callBagService&measure=p95` → `ml.TraceREDSourceQuery`
(aynı aile pinleri ve etiket keşfi `pickTraceLabels`, servis + route matcher, `histogram_quantile(0.95, sum by (le)
(rate(…_bucket[BucketSec])))` ms; slow → `histogram_share`, hata → oran %, düşüş → istek/sn), 1 dk adım.
Sıra: canlı seri → kayıtlı görüntü → boş durum (yalnız ikisi de yoksa); başlıkta küçük "canlı seri" / "kayıtlı
görüntü" etiketi (i18n `mlCourse.*`). Kanıt kaynağı olayın `evidence.source`'undan: trace + `vm` →
"VictoriaMetrics · span RED metrikleri" (ham trace değil), aksi → "ClickHouse span'leri". Dedektör/eşik değişmedi
(dilim G/D). Prod doğrulama: kartın Network sekmesinde `source-series` yanıtında `query.expr` ve `series[0].points`.

## 4. Kayıt nasıl dolar, render'da ne yeniden sorgulanır (Soru 17–18) ve örüntü tablosu (Soru 6)

**S17/S6.** Başlık ve "Ne kadar" her dedektörde `BuildExplainCard` (`explain_card.go:872`) ile `ev.Observed/Baseline/StartedAt/LastSeen`'den
kurulur; grafik §3'teki yoldan gelir. "Aynı mı?" sütunu: test edilen seri ile çizilen seri aynı sorgu mu.

| Dedektör | Test edilen seri (kaynak · kova) | Başlık/kanıt sayısı | Çizilen (yol #) | Aynı mı? | Başlangıç / bitiş | Tek kanıt? |
|---|---|---|---|---|---|---|
| `svc_volume_spike` | CH `service_summary_5m` span sayısı · 24 sa toplam | iki toplam; kanıt = başlık | istek hızı (5) | **Hayır** (ölçü + kaynak) | eşik aşım tiki / pencere kayınca | **Evet, yapısal** |
| `op_latency` | VM histogram p95 ya da yavaş pay · 5 dk | son kova + havuz medyanı | CH örneklenmiş span p95/p99 (2) → görüntü (6) | **Hayır** (kaynak; p95'te ölçü aynı, slow'da p99) | kayan kova başı / son kova sonu | grup üyesi yoksa evet |
| `op_error_rate`, `throughput_drop` | VM RED oran/sayım · 5 dk | aynı | CH span hata/hız şeridi (2) | Hayır (kaynak) | aynı | aynı |
| `trace_change_point` | CH `ml_series` `route\|p95` CUSUM · 6 sa † | kırılma sonrası ortalama | CH span p95 (2) | Hayır (kaynak) | **hesaplanmış kırılma anı** † | aynı |
| `trace_error_count` | CH `spanmetrics_1m` hata sayısı · 5 dk † | `baseline` = `normalMax` (medyan değil) † | CH span hata şeridi (2) | Kısmen (aynı depo, MV ≠ ham) | kayan kova | aynı |
| `svc_error_rate` | TraceRED satırlarının servis toplamı (VM) · 5 dk, dwell 3 † | oran + mevsimsel medyan | `/metric-red` hata oranı (5) | Kısmen (aynı aile, pencere 180 sn) | kayan kova | kendi kapısı |
| `svc_breadth` | türetilmiş: ≥3 `op_latency` p95 olayı † | medyan oran × taban | servis **p99** (5) | **Hayır** (türev ≠ servis p99) | koşulun ilk tiki | üyeleri aynı kaynak |
| `trace_pod_outlier` | VM pod başına `increase[dwell×kova]` · kesitsel † | pod değeri / pod medyanı | pod başına 1 dk (3) | Kısmen (pencere farklı), görüntü serisiz | kayan kova | evet |
| `dependency_latency`/`_error_rate` | VM client histogram · 5 dk † | son kova + medyan | aynı ifade, 120 sn pencere, **bugünkü ayardan** yeniden kurulur (3; `source_query.go:31`) | Kısmen (pencere; ifade render anında) | kayan kova | grup yoksa evet |
| `metric_*` (VM) | Settings'teki MetricsQL · kova sonu anlık † | başlık sayısı render'da **panel tepesi** (`MLReplaySheet.tsx:173`) | aynı ifade 1 dk (3) | Kısmen; sayı kayıttan değil | kayan kova | grup yoksa evet |
| `metric_kafka` consumer_lag | `max by (svc,topic)` · kova sonu † | panel tepesi | Messaging paneli (1) | **Hayır** (panel sorgusu ≠ dedektör ifadesi) | kayan kova | + katkı satırı (aynı panel) |
| `metric_*` (Thanos) | PromQL, pod→servis eşlemesi † | dedektör değeri | pod başına seri (3) | Hayır (servis değeri ≠ pod serileri) | kayan kova | evet |
| `log_volume`, `error_rate`, `service_silent` | ES sayım · 5 dk † | görüntü serisinden pencere toplamı (`explain_card.go:615`) | Logs histogramı 1 dk (4) | Kısmen (aynı depo; süzgeç kart bağlantısından) | kayan kova | grup yoksa evet |
| `template_spike`, `rare_template` | ES aile sayımı · 5 dk † | son kova | Logs histogramı `q` ile (4) | Kısmen | kayan kova | evet |
| `new_template` | CH `log_templates` defteri † | satır sayısı, taban 0 | Logs histogramı (4) | Kısmen | **şablonun ilk görülmesi** † | evet |
| `rare_error_code`, log `pod_outlier` | ES kırılım · dwell penceresi † | sayım / pod oranı | Logs histogramı (4), pod serisi yok | Hayır (kırılım çizilmez) | kayan kova | evet |
| `change_point` (log) | CH `ml_series` CUSUM † | ortalamalar | Logs histogramı (4) | Kısmen | hesaplanmış kırılma † | evet |
| üye satırları (`svc_slowdown`, `metric_anomaly`, `anomaly_promotion`) | eski kural değeri/eşiği † | — | anomali yüzeyinde görünmez (`ml_events.go:163-165`) | kapsam dışı | üyelik süresi | — |

"Toplam pencere ≠ olay süresi" yalnız `svc_volume_spike`'ta saf hâliyle var; kova tabanlı dedektörlerde eşdeğeri
"dwell penceresi ≠ yazılan süre"dir (kayma). Gerçek başlangıç anı üreten yalnız `change_point` ailesi ve `new_template`.

**S18. Render anında yeniden sorgulananlar** (`GET /api/ml/{id}/replay`, `internal/api/ml.go:409-466`, önbellek 30 sn):

| Alan | Render'da kaynak | Kayıt anından sapabilir mi |
|---|---|---|
| Başlık sayısı (metrik) | canlı panel tepesi (`mlValueText.ts:94`, `peakInRange`) | Evet — VM saklama/downsample, panel sorgusu farklı |
| `observed`/`baseline`/`score` | `ml_events` son yazım (her tik tam satır) | Evet — açıkken her tik değişir; kapanınca son değer, tepe değil |
| `started_at` | aynı satır | Evet — ilk 2–3 tik kayar (§3) |
| "Normal" aralık | `ml_event_snapshots.thresholds.band` (`explain_card.go:417`) | Evet — ön kayıt → tam kayıt yeniden yazılır; kayıt yoksa medyan |
| Grafik (yol 1–5) | canlı VM / CH span / ES | Evet — veri TTL'i dolunca aynı olay başka grafikle açılır |
| Metrik etiketi, birim, ifade | **bugünkü** `MLConfig` (`explain_input.go:337-361`, `source_query.go`) | Evet — ayar değişirse eski olayın grafiği/etiketi değişir |
| Kanıt satırları (grup üyeleri) | liste ucunun son 24 sa satırları (`mlGroupDetail.ts:5-17`) | Evet — 24 sa sonra üyeler düşer, tek satıra (başlık tekrarı) iner |
| Kanıt sekmeleri | `spanmetrics` MV 2 pencere / logstore 2 çağrı / `ml_series` (`internal/api/ml_evidence.go:84-108`) | Evet — pencere `started_at`'e bağlı, örneklemeli |
| Etkin eşik, profil, yayınlar, ilişkili kayıtlar | etiket sayımı, `MLLearnedOne`, `GetServiceDeploys`, ±2 sa snapshot'lar | Evet |

## 5. `ml_events` şeması (Soru 5)

DDL `internal/chstore/store.go:3119-3139`: RMT(version), `ORDER BY id`, TTL 30 g. Kolonlar: `id, detector, signal, stage,
service, level, pod, code, started_at, last_seen, window_sec, baseline, observed, score, direction, evidence, dup_of`.

| İstenen | Bugün | Eksik |
|---|---|---|
| Sinyal türü | `signal` (log/trace/metric), `detector` | — |
| Test edilen seri tanımı | yok; `code`/`evidence.source` ("vm", "spanmetrics_1m", "thanos") ipucu, PromQL/SQL metni saklanmaz | kaynak + sorgu + toplama + birim + adım |
| Beklenen değer / bant | `baseline` skaleri; bazılarında `evidence.mad`; bant yalnız snapshot'ta ve **farklı istatistik** (q25–q75) | alarm bandı (lo/hi), yöntem |
| Zamanlar | `started_at`, `last_seen` | `onset_at`, `detected_at`, `ended_at` ayrımı; gecikme |
| Değerler | `observed` (son yazım) | tepe, tespit anı değeri, birim, **hacim** |
| Kanıt listesi | `evidence` = dedektör parametreleri + teyit sayaçları (`quality.go:381-386`) | kanıt satırları (tür, kaynak, özet, link, bağımsızlık) |
| Sınıf / kategori | yok (`stage` yaşam döngüsü; "shadow" hem "Problem açmaz" hem varsayılan) | `class`, yayın durumu, kapı sonuçları |
| Geri bildirim nedeni | `anomaly_verdicts.verdict` + serbest `note` (`store.go:2504-2518`) | neden alanı (enum) |
| Özne | servis **adı**, pod **adı** | entity id (`internal/entity/identity.go:51-59`) |

Not: tablo yorumu "yazıcı yalnız log_ml lideri (sre pod'u)" der (`store.go:3117`); bugün evaluator (worker) ve üye yazıcıları da yazıyor.

## 6. Replay (Soru 7)

- Olay başına "replay" (`/api/ml/{id}/replay`) dedektörü **yeniden koşmaz**; donmuş kaydı + birkaç canlı okumayı yeniden çizer.
- Kararı yeniden koşan tek yol `RunBacktest` (`internal/ml/backtest.go:148`): `ml_series` üzerinde saf karar; `rulesFor` (`:81`)
  yalnız `log_volume/service_silent`, `error_rate`, `template_spike`, `dependency_latency` (p95), `throughput_drop`,
  `op_latency` (slow), `op_error_rate` ve metrik ailelerini tanır †. `svc_volume_spike`, `svc_error_rate`, `svc_breadth`, p95 ölçülü `op_latency` yok.
- Fixture düzeni yok: `internal/ml/testdata` yok; testler kod içi struct. Tek emsal eski hattın `internal/anomaly/eval.go` JSONL "series" kipi †.
  "Üretmemeli" iddiası için API yok (sıfır isabet sayılabilir; `scoreHits` yalnız pozitif etiket) †.

**Örnek 1 fixture olabilir mi?** Bugün hayır: seri yok (iki skaler), snapshot yok, kural haritasında yok. Eksikler: (1) saatlik/5 dk
`span` ve giriş-span serisinin dışa aktarımı (`service_summary_5m` TTL 90 g; `spanmetrics_1m` TTL **30 g** — veri düşmeden alınmalı);
(2) pencere-oranı / istek-başına-span karar yolunun saf işlevi; (3) fixture yükleyici + beklenen sonuç alanları
(`yayın`, `class`, `onset_at ± tolerans`); (4) negatif iddia.
**Örnek 2 ikinci aday:** `ml_series` `callBagService|p95`, `|count`, `|slow` (TTL **28 g**) + snapshot satırı (120 g) dışa aktarılırsa
bugünkü `judge` ile yeniden koşulabilir; eksik: p95 ölçüsünün `rulesFor` karşılığı ve 1 dk seri (bugün yalnız 5 dk saklanıyor).

## 7. Sorgular (Soru 8, 15, 14) — operatör koşar

`ml_events`/`ml_event_snapshots` düşük hacimli state tablolarıdır (MV'leri yok); okuma zaman sınırlı, `LIMIT` + `max_execution_time`.
JOIN / `IN` alt sorgusu yok (dağıtık-güvenli). Sayılar **yaklaşık üst sınırdır**: görünür sapma ve etki kapıları ham seriyi ister.

```sql
-- A) Dedektör başına: kaç kayıt hangi kapıya takılır (son 30 gün)
SELECT detector,
  count()                                                              AS kayit,
  countIf(detector = 'svc_volume_spike')                               AS sinif_telemetry_volume,   -- C: sınıf değişir
  countIf(dateDiff('second', started_at, last_seen) <= window_sec)     AS tek_kova,                 -- G: yayımlanmaz
  countIf(JSONExtractUInt(evidence, 'confirmSignals') = 0)             AS bagimsiz_kanit_lt2,       -- A: 'bilgi'
  countIf(JSONExtractUInt(evidence, 'confirmScore') = 0)               AS hic_teyit_yok,
  countIf(toUnixTimestamp(started_at) % window_sec != 0)               AS baslangic_kova_disi       -- §3 kayma doğrulaması
FROM ml_events FINAL
WHERE last_seen >= now() - INTERVAL 30 DAY
  AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion') AND stage != 'chronic'
GROUP BY detector ORDER BY kayit DESC LIMIT 100
SETTINGS max_execution_time = 20;

-- B) Gecikme dedektörü: tek kovalık pay + mutlak fark dağılımı (Soru 15)
SELECT JSONExtractString(evidence, 'measure') AS olcu, count() AS kayit,
  countIf(dateDiff('second', started_at, last_seen) <= window_sec) AS tek_kova,
  round(tek_kova / kayit, 3) AS tek_kova_payi,
  countIf(olcu = 'p95' AND observed - baseline < 100) AS fark_lt_100ms,
  countIf(olcu = 'p95' AND observed < 150)            AS tabana_yakin
FROM ml_events FINAL
WHERE detector = 'op_latency' AND last_seen >= now() - INTERVAL 30 DAY AND stage != 'chronic'
GROUP BY olcu ORDER BY kayit DESC LIMIT 10
SETTINGS max_execution_time = 20;

-- C) Etki kapısı: RED dışı kayıtlardan kaçı ±10 dk içinde aynı serviste hata/gecikme olayı olmadan duruyor
SELECT e.1 AS detector, count() AS kayit,
  countIf(NOT arrayExists(r -> r.2 <= e.3 + 600 AND r.3 >= e.2 - 600, red)) AS etkisiz_sinif_degisir
FROM (
  SELECT service,
    groupArray((detector, toUnixTimestamp(started_at), toUnixTimestamp(last_seen))) AS evs,
    arrayFilter(x -> x.1 IN ('op_latency','op_error_rate','svc_error_rate','svc_breadth','trace_error_count',
                             'dependency_latency','dependency_error_rate'), evs) AS red
  FROM ml_events FINAL
  WHERE last_seen >= now() - INTERVAL 30 DAY
    AND detector NOT IN ('svc_slowdown','metric_anomaly','anomaly_promotion') AND stage != 'chronic'
  GROUP BY service
) ARRAY JOIN evs AS e
WHERE e.1 NOT IN ('op_latency','op_error_rate','svc_error_rate','svc_breadth','trace_error_count',
                  'dependency_latency','dependency_error_rate')
GROUP BY detector ORDER BY kayit DESC LIMIT 100
SETTINGS max_execution_time = 30;

-- D) Görünür sapma kapısı (kayıtlı seriden) + kayıt boyutu (Soru 14)
SELECT detector, count() AS kayit,
  countIf(length(pts) = 0) AS seri_yok,
  countIf(length(pts) > 0 AND NOT arrayExists(p ->
      p.1 >= toUnixTimestamp(detected_at) - bucket_sec * (dwell - 1) AND p.1 < toUnixTimestamp(last_seen)
      AND abs(p.2 - baseline_med) > z * greatest(1.4826 * baseline_mad, 0.1 * abs(baseline_med), if(abs(baseline_med) < 1, 0.005, 1)),
    pts)) AS gorunur_sapma_yok,
  round(avg(length(series)))                                        AS seri_bayt_ort,
  max(length(series))                                               AS seri_bayt_max,
  round(avg(length(thresholds) + length(evidence) + length(samples) + length(deploys))) AS diger_bayt_ort
FROM (
  SELECT detector, detected_at, last_seen, bucket_sec, baseline_med, baseline_mad, series, thresholds, evidence, samples, deploys,
    JSONExtract(series, 'Array(Tuple(t Int64, v Float64))') AS pts,
    greatest(JSONExtractFloat(thresholds, 'effectiveZ'), JSONExtractFloat(thresholds, 'criticalZ')) AS z,
    greatest(JSONExtractUInt(thresholds, 'dwellBuckets'), 1) AS dwell
  FROM ml_event_snapshots FINAL
  WHERE detected_at >= now() - INTERVAL 30 DAY
)
GROUP BY detector ORDER BY kayit DESC LIMIT 100
SETTINGS max_execution_time = 30;
```

Okuma notları: (A) `confirmSignals` yalnız **başka sinyalden** açık koşuları sayar (`quality.go:217-232`), son yazımda donar; evaluator
satırında alan yok → 0. (C) RED olayı yokluğu "bant içinde" demek değildir (trafik kapısı altındaki uçlar, VM ailesi olmayan servisler) → üst sınır.
(D) `baseline_mad` ham mı ölçekli mi doğrulanmadı; ölçekliyse 1,4826 çarpanı sayıyı şişirir. `baseline_med` bazı dedektörlerde medyan değil
(`trace_error_count`: `normalMax`). Seri değişikliği olmayan kapı sayımı: `svc_volume_spike` satırlarının tamamı "seri tutarlılığı"na takılır (tablo §4).

### 7.1 Fixture dışa aktarımı (K13) — operatör **şimdi** koşar

`ml_series` TTL 28 g, `spanmetrics_1m` TTL 30 g: iki örneğin serisi veri düşmeden alınır. Hedef dizin
`internal/ml/testdata/fixtures/<event-id>/` (dosya başına bir tablo, `JSONEachRow`; E dilimindeki replay biçimi bunlardan
üretilir, ham dosyalar değişmeden kalır). Kolon adları koddan: `ml_events` / `ml_series` / `ml_event_snapshots`
`internal/chstore/store.go` CREATE TABLE'ları, `spanmetrics_1m` aynı dosyadaki MV (state kolonları `calls_state`,
`error_state`, `duration_q_state` = `quantilesTDigestState(0.5, 0.9, 0.95, 0.99)`, süre ns). Her sorgu zaman sınırlı +
`LIMIT` + `max_execution_time`; JOIN / alt sorgu yok — pencere önce `ml_events` satırından okunur, sonra parametre geçer.
Tablo adları niteliksiz (veritabanı `--database` ile; küme kipinde aynı adlar).

```sh
CH="clickhouse-client --host <ch-host> --port 9000 --user <user> --password <pw> --database <db>"

# Ortak: olay satırı + pencere (FROM = started_at − 28 g, TO = last_seen + 1 g; unix sn)
export_event() {  # $1 = event id
  D=internal/ml/testdata/fixtures/$1; mkdir -p "$D"
  $CH --param_id="$1" --query "
    SELECT id, detector, signal, stage, service, level, pod, code, started_at, last_seen, window_sec,
           baseline, observed, score, direction, evidence, dup_of, version,
           class, publish_state, onset_at, record   -- v0.10.2252: replay (E) bunları okur
    FROM ml_events FINAL
    WHERE id = {id:String} AND last_seen >= now() - INTERVAL 30 DAY
    LIMIT 10 SETTINGS max_execution_time = 10 FORMAT JSONEachRow" > "$D/ml_events.jsonl"
  $CH --param_id="$1" --query "
    SELECT * FROM ml_event_snapshots FINAL
    WHERE event_id = {id:String} AND detected_at >= now() - INTERVAL 120 DAY
    LIMIT 10 SETTINGS max_execution_time = 10 FORMAT JSONEachRow" > "$D/ml_event_snapshots.jsonl"
  read FROM TO <<< "$($CH --param_id="$1" --query "
    SELECT toUnixTimestamp(started_at) - 28 * 86400, toUnixTimestamp(last_seen) + 86400
    FROM ml_events FINAL
    WHERE id = {id:String} AND last_seen >= now() - INTERVAL 30 DAY
    LIMIT 1 SETTINGS max_execution_time = 10 FORMAT TSV")"
  echo "$1 FROM=$FROM TO=$TO"
}

# Örnek 1 — 59d8f32338b5e7b5 · acm-promo-points-gain-prod (svc_volume_spike)
export_event 59d8f32338b5e7b5; D=internal/ml/testdata/fixtures/59d8f32338b5e7b5
$CH --param_svc=acm-promo-points-gain-prod --param_from="$FROM" --param_to="$TO" --query "
  SELECT signal, service, part, bucket, bucket_sec, count
  FROM ml_series FINAL
  WHERE service = {svc:String} AND bucket >= toDateTime({from:UInt32}) AND bucket < toDateTime({to:UInt32})
  ORDER BY part, bucket LIMIT 500000 SETTINGS max_execution_time = 30 FORMAT JSONEachRow" > "$D/ml_series.jsonl"
$CH --param_svc=acm-promo-points-gain-prod --param_from="$FROM" --param_to="$TO" --query "
  SELECT time_bucket, kind, countMerge(calls_state) AS calls, countMerge(error_state) AS errors
  FROM spanmetrics_1m
  WHERE service_name = {svc:String}
    AND time_bucket >= toDateTime({from:UInt32}) AND time_bucket < toDateTime({to:UInt32})
  GROUP BY time_bucket, kind
  ORDER BY time_bucket, kind LIMIT 400000 SETTINGS max_execution_time = 60 FORMAT JSONEachRow" > "$D/spanmetrics_1m.jsonl"

# Örnek 2 — 09ecef247b0557ea · acm-risk-score-bureau-inq-prod · callBagService (op_latency p95)
export_event 09ecef247b0557ea; D=internal/ml/testdata/fixtures/09ecef247b0557ea
$CH --param_svc=acm-risk-score-bureau-inq-prod --param_from="$FROM" --param_to="$TO" --query "
  SELECT signal, service, part, bucket, bucket_sec, count
  FROM ml_series FINAL
  WHERE service = {svc:String} AND (part = '' OR startsWith(part, 'callBagService|'))
    AND bucket >= toDateTime({from:UInt32}) AND bucket < toDateTime({to:UInt32})
  ORDER BY part, bucket LIMIT 200000 SETTINGS max_execution_time = 30 FORMAT JSONEachRow" > "$D/ml_series.jsonl"
$CH --param_svc=acm-risk-score-bureau-inq-prod --param_op=callBagService --param_from="$FROM" --param_to="$TO" --query "
  SELECT time_bucket, name, kind, http_route,
         countMerge(calls_state) AS calls, countMerge(error_state) AS errors,
         arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 3) / 1e6 AS p95_ms,
         arrayElement(quantilesTDigestMerge(0.5, 0.9, 0.95, 0.99)(duration_q_state), 1) / 1e6 AS p50_ms
  FROM spanmetrics_1m
  WHERE service_name = {svc:String} AND (name = {op:String} OR http_route = {op:String})
    AND time_bucket >= toDateTime({from:UInt32}) AND time_bucket < toDateTime({to:UInt32})
  GROUP BY time_bucket, name, kind, http_route
  ORDER BY time_bucket LIMIT 400000 SETTINGS max_execution_time = 60 FORMAT JSONEachRow" > "$D/spanmetrics_1m.jsonl"

wc -l internal/ml/testdata/fixtures/*/*.jsonl   # boş dosya = satır TTL ile düşmüş ya da id/ad uyuşmadı
```

**Gizlilik (v0.10.2252).** Dışa aktarılan dosyalar gerçek servis / operasyon adları taşır: `internal/ml/testdata/fixtures/`
`.gitignore`'dadır, **commit edilmez** (depo kuralı: gerçek kurum / servis adı depoda geçmez). Fikstür depoya girecekse önce adlar nötr
adlarla değiştirilir (ör. `checkout-api`) ve dizin `internal/ml/testdata/replay/` altına taşınır.

Notlar: (1) `FROM` 28 g geriye gider ama tablo TTL'inden eskisi zaten yoktur — fazladan tarama yok. (2) Örnek 1 dedektörü
`ml_series` yazmaz (evaluator kuralı); oradaki satırlar aynı servisin trace RED parçalarıdır (B dilimi tabanı için yine
gerekli). Asıl hacim serisi `spanmetrics_1m` `kind` kırılımı (istek başına span = tüm kind / `server`+`consumer`).
(3) Örnek 2 `part` biçimi `route|measure` (`trace_red.go` `tracePart`); `callBagService` VM `route` etiketidir,
span adıyla uyuşmayabilir (§3 S13) — bu yüzden `name` **ya da** `http_route`. (4) Dosyalar commit edilmeden önce
boyut kontrolü: tek dosya > 5 MB ise `gzip` ile `.jsonl.gz` (yükleyici E diliminde ikisini de okur).

## 8. Bulgular

| # | Bulgu | Kanıt |
|---|---|---|
| B1 | Olay kaydı test edilen seriyi tanımlamaz; grafik dedektör adından seçilen panel | `mlSourcePanel.ts:50-88`, §5 |
| B2 | Başlık ve tek kanıt satırı aynı işlevden; bağımsız kanıt kavramı yok | `explain_card.go:477`, `:586`; `MLReplaySheet.tsx:174-176` |
| B3 | Hacim kuralı ham span sayar, önceki 24 saati taban alır; başlangıç = eşik aşımı (değişimden 72/(k−1) sa sonra), bitiş = pencere kayması | `selfhealth_volume.go:58,96-107,304-328` |
| B4 | Hacim olayı servis anomalisi gibi listelenir; sınıf alanı yok, hata/gecikme kontrolü yok | §5; `chstore/ml_events.go:34-40` |
| B5 | Kova tabanlı dedektörlerde `started_at` kayar; süre ve "tek kova" yanlış; tepe "başlangıç"tan önce kalabilir | `quality.go:372-375`, `trace_red.go:407` |
| B6 | Kapanan kayıtta `observed` tepe değil son değer; grafik tepesiyle kart sayısı ayrışır | `quality.go:196-214` |
| B7 | Üç ayrı "normal": metin medyanı, grafik q25–q75, alarm bandı (çizilmez) | `snapshot.go:306-340`, `explain_card.go:417,579-582` |
| B8 | Metrik başlık sayısı render'da canlı panelden yazılır | `MLReplaySheet.tsx:173`, `mlValueText.ts:94` |
| B9 | 6 grafik bileşeni / 10 bağlama; trace/log'da kayıtlı görüntüye **sessiz** düşüş; görüntü 5 dk, birimsiz | §3 S13 |
| B10 | Kaynak etiketi yanlış ("VictoriaMetrics" varsayılanı) | `mlGroupDetail.ts:20-28` |
| B11 | Kanıt üyeleri 24 sa sonra düşer; aynı kart ertesi gün farklı kanıt gösterir | `mlGroupDetail.ts:5-17` |
| B12 | Gecikme trafik kapısında birim belirsizliği; çağrı sayısı ve mutlak fark kapısı yok | `trace_red.go:353-355`, `:121-126` |
| B13 | Olay başına replay kararı yeniden koşmaz; ML için fixture/negatif iddia altyapısı yok; evaluator olayı kayıtsız | §6 |
| B14 | ✅ **kapandı v0.10.2218** (K3: `feedbackZ` ve dört geri bildirim ayarı silindi). Geri bildirim bugün **çalışma zamanı kararını sürüyor**: `feedbackZ` "değil" sayısıyla z eşiğini yükseltir | `quality.go:119-126,361-365`; `chstore/ml_labels.go:47-49` |
| B15 | `svc_volume_spike` worker pod'unda (evaluator) üretilir; ML işleri SRE rolünde | `main.go:705-707`, `main_ml.go:5,33-36` |
| B16 | Tek sinyal Problem kolu (v0.10.2092) hedefteki "bağımsız kanıt < 2 → Problem adayı değil" kuralıyla çelişir | `group_gate.go:234` † |

## 9. Hedef: tek olay-kaydı sözleşmesi (Dilim H tasarımı)

**Alanlar** (her alan tek yerde tanımlanır; A–G bunları kullanır, yenisini eklemez):

| Alan | İçerik | Türetme kuralı |
|---|---|---|
| `subject` | `entity_id` (`entity.ServiceID`), `operation`, pod/workload entity id (çözülemezse ad + `resolved=false`) | `internal/entity/identity.go`; bağlantılar entity sayfasına |
| `series` | `source` (vm/thanos:<cluster>/ch:<mv>/es), `query` (çözülmüş metin), `aggregation`, `unit`, `step_sec` | dedektörün **koştuğu** ifade; render'da yeniden kurulmaz |
| `baseline` | `expected`, `lo`, `hi`, `method` (ör. `class_days_hour±1_median_mad`, `weekly_slot`, `cross_section`), `step_sec` | alarmı veren bandın kendisi |
| `onset_at`, `detected_at`, `ended_at`, `detection_delay` | sapmanın başladığı an / kaydın açıldığı tik / banda dönüş | `onset_at ≤ detected_at`; `delay = detected − onset` |
| `peak_value`, `value_at_detection`, `expected_value` | hepsi `series`'ten | başka sorgudan gelemez |
| `volume` | olay penceresindeki istek/çağrı sayısı (yoksa `null` + neden) | `series` ile aynı kaynak ailesi |
| `evidence[]` | `{tür, kaynak, özet, link, bağımsız_mı}` | kaynak + ölçü `series` ile aynıysa `bağımsız_mı=false`; türetilmiş dedektör (`svc_breadth`) kendi üyelerinden bağımsız sayılmaz |
| `class` | `service_anomaly` \| `telemetry_volume` \| `info` | A kapıları belirler |
| `gates[]` | `{ad, geçti_mi, sayı, eşik, neden}` | her yayın kapısı bir satır |
| `snapshot` | olay ±30 dk, ≤ 1 dk adım: değer, `lo`, `hi`, hacim | `series` ile aynı toplama |

**Kurallar.** Başlık ve kanıt metni yalnız bu alanlardan şablonlanır; metin üreten kod sorgu yapamaz (bugünkü kart zaten LLM'siz Go
şablonu — korunur). Ölçü başına tek sayı; ikinci sayı adlandırılır ("tepe 74.167 · tespit anında 15.600"). **Doğrulayıcı** yazımdan
önce: `peak_value == max(series, olay penceresi)`; `onset_at ≤ detected_at`; snapshot toplaması = `series` toplaması; başlıktaki sayılar
alanlarla aynı; `evidence` içinde başlık tekrarı yok. Tutarsız kayıt yayımlanmaz (`publish_state=invalid`) ve sayaç artar
(`ml.Observability`, `tick_meter.go` deseni). "Teknik ayrıntı" `gates[]` + `series` tanımını gösterir.

**Nerede saklanır (öneri; karar §12-K1).**
- `ml_events`'e dört kolon: `class LowCardinality(String) DEFAULT ''`, `publish_state LowCardinality(String) DEFAULT ''`,
  `onset_at DateTime64(9) DEFAULT 0`, `record String DEFAULT '' CODEC(ZSTD(3))` (sözleşmenin geri kalanı JSON; sürüm alanı içinde;
  `record == ''` → eski kayıt, kart bugünkü davranışa düşer). Tipli üç kolon şart: liste SQL'i `class`/`publish_state` süzmeli —
  Go'da süzmek `MLUnifiedCap` tavanını gizli satırlarla doldurur. `ORDER BY id` değişmez (dedup anahtarına kolon eklenmez).
- Seri `ml_event_snapshots`'ta kalır (TTL 120 g, replay 30 g'lik `ml_events`'ten bağımsız): mevcut `series`/`thresholds` kolonlarına
  sürümlü yeni biçim, `bucket_sec=60` → snapshot tablosunda **DDL yok**.
- Reddedilenler: (a) her alan ayrı kolon — 15+ `ALTER`, her yeni alan iki boot; (b) yalnız `evidence` içine gömmek (Davis D'nin yolu,
  sıfır DDL) — sınıf/yayın süzgeci SQL'de yapılamaz; (c) yan tablo — liste için JOIN gerekir.
- **Göç sınıfı:** düşük hacimli state tablosuna `ADD COLUMN IF NOT EXISTS` = güvenli sınıf, `store.go migrate()` (`migrations/*.sql` değil).
  Küme kipinde DDL ertelenir → **iki boot sözleşmesi**; SRE pod'u DDL koşmaz (`MLTableReady`) → `hasMLRecordCols` yoklaması + koşullu
  INSERT (kolon yokken eski biçim yazılır, kayıp yok). `ml_events` küme kipinde sarmalayıcılı mı — doğrulanmadı, `/clickhouse-schema` §9 listesi uygulanır.
- **RMT tam satır taşıma:** (1) tek yazım yolu `UpsertMLEvent`, struct'a alanlar eklenir; (2) okuyup yeniden yazan her yol yeni kolonları
  **okumalı**: `ListMLEvents` (evaluator tohumu `seedVolumeOpen`, kapanış kopyası), üye yazıcıları (`anomaly.Members`,
  `evaluator/problem_merge.go`); `mlGroupRowsSQL` hafif okuması yeniden yazımda kullanılamaz; (3) pin testi: INSERT kolon listesi =
  SELECT kolon listesi ve Upsert → List → Upsert gidiş-dönüşü hiçbir alanı sıfırlamaz.

**Snapshot boyutu ve sınır.** Sütunlu JSON `{t0, step, v[], lo[], hi[], n[]}` ≈ 30 bayt/nokta. Nokta = 60 + olay süresi (dk):
5 dk'lık olay ≈ 2 KB, 2 sa ≈ 5,5 KB, 6 sa (rejim tavanı) ≈ 13 KB ham. **Sınır: en çok 720 nokta** (aşarsa adım 60 sn'nin katına
büyütülür ve `step` alanına yazılır), kayıt başına ham 64 KB (aşan doğrulayıcıdan kalır). Kuramsal tavan 200 aday/gün/dedektör ×
~20 dedektör × ~6 KB ≈ 24 MB/gün ham, ZSTD sonrası birkaç MB; 120 g'de < 1 GB. Gerçek sayı §7-A/D ile ölçülür.

**Davis tek-Problem modeliyle (v0.10.2155–2164).** Üyelik bugünkü gibi `evidence.problem_id` (şema eklenmez). Grup/servis kapıları
girdi olarak yalnız `class=service_anomaly` ve `publish_state=''|published` kayıtları alır; `groupExcluded` özel durumu
(`group_gate.go:197`) bu süzgece dönüşür. Üye satırları (`svc_slowdown` vb.) ölçüm değil üyelik kaydıdır: sözleşme dışı, doğrulayıcıdan
muaf. Bildirim kuralı değişmez (yalnız açılış + P1'e çıkış); `info`/`telemetry_volume` hiçbir kolda sayılmaz.

**S19. Dedektör başına göç eforu**

| Dedektör | Efor | Ne değişir | Geriye uyum riski |
|---|---|---|---|
| `metric_kafka` consumer_lag (ilk) | S–M | çözülmüş ifade + topic kayda; 1 dk VM aralık sorgusuyla snapshot; `withPanelPeak` ve Messaging paneli kalkar | operatörün v0.10.2033 isteği ("servis sayfasındaki panel") ile çelişir → §12-K4 |
| diğer `metric_*` (VM) | S | aynı yol | düşük |
| `metric_*` (Thanos) | M | pod kümesi + cluster dondurulur; servis değeri tek seri | pod başına görünüm kanıt satırına iner |
| `trace_error_count` | S | kaynak zaten açık (`spanmetrics_1m`); `baseline` medyan/bant ayrılır | "normal" metni değişir |
| `svc_error_rate` | S | TraceRED üstünde | düşük |
| `op_latency`, `op_error_rate`, `throughput_drop` | M | PromQL kayda; `started_at` kayması yerine onset; G'nin 1 dk kuralı | olay sayısı değişir → önce gölge kıyas |
| `dependency_*` | S–M | hedef etiketi + ifade dondurulur | düşük |
| `trace_change_point`, `change_point` | S | onset zaten var; seri `ml_series` | düşük |
| `svc_breadth` | M | seri tanımı gerek ("yavaş operasyon sayısı"); üyeler bağımsız kanıt değil | sınıf `info`'ya düşebilir |
| `trace_pod_outlier`, log `pod_outlier` | M | kesitsel taban (`cross_section`); pod serisi snapshot'a | grafik değişir |
| `log_volume`, `error_rate`, `service_silent` | M | 1 dk ES histogramı = yeni olay başına +1 ES sorgusu (bütçe `esLeft`) | ES maliyeti |
| `template_spike`, `rare_template` | M | aile sorgusu kayda | düşük |
| `new_template`, `rare_error_code` | L | sayısal seri yok → sözleşmede `series.kind=occurrence` istisnası, doğrulayıcı dalı | sözleşme istisnası |
| `svc_volume_spike` | L | göç edilmez; Dilim B'de yeni dedektörle değişir | eski satırlar TTL ile düşer |

## 10. Dilimler (her biri tek sürüm, bağımsız gemilenir; sıra H → A → F → B, C → G, D → E; D0/G0 onaylı → G/D/A/B/C engelsiz)

**H1 — sözleşme tipi + doğrulayıcı + saklama.** ✅ **gemilendi v0.10.2199** (`internal/ml/record.go`, `record_test.go`; kolonlar `store.go` alters, probe `hasMLRecordCols`; sayaç `ml.recordInvalid`). `internal/ml/record.go` (saf tip + `Validate`), dört kolon, gölge sayaç ve rapor
satırı "doğrulayıcıdan kalan kayıt (dedektör başına)". Hiçbir dedektör göçmez, davranış değişmez. *Risk:* düşük; tek risk kolon taşıma.
*Göç:* 4 × `ADD COLUMN` (yukarıdaki sınıf, iki boot, SRE yoklaması). *Test:* doğrulayıcı tablo testi — iki prod örneğinin bugünkü hâli
**kalır** (nedenleriyle), düzeltilmiş hâli geçer; üçüncü örnek ayrıntısı gelince eklenir (ölçüt 10); kolon gidiş-dönüş pini.

**H2 — consumer lag göçü.** ✅ **gemilendi v0.10.2215** (`internal/ml/kafka_lag_record.go`, `kafka_lag_record_test.go`; kart `RecordTitle`). Kayıt dolar; kart `record` varsa yalnız onu okur. *Risk:* orta (K4 çelişkisi). *Göç:* yok.
*Test:* render'da ölçü sayısı için ek istek atılmadığını sabitleyen FE testi (ölçüt 9); başlık sayısı = alan.

**H3 — `op_latency` göçü.** ✅ **gemilendi v0.10.2219** (`internal/ml/op_latency_record.go`, `op_latency_record_test.go`; ortak `seriesWindow`).
Seri = ölçünün (p95 ms | yavaş istek %) servis × route için çözülmüş VM RED ifadesi, dedektörün kova penceresiyle (aile + etiket adları
tik anında kanıta: `redFamily/svcLabel/routeLabel`); hacim aynı ailenin `_count`'u (dakika başına çağrı, `snapshot.n`). Tespit, eşik,
`started_at` ve G'nin 1 dk kuralı **değişmedi** (G'de). Maliyet: açık olay başına tik başına 2 VM aralık sorgusu, tik başına ≤ 10 olay.

**H4 — VM `metric_*` göçü.** ✅ **gemilendi v0.10.2220** (`internal/ml/metric_vm_record.go`, `metric_vm_record_test.go`). Seri = aile
ifadesinin tik hâli (`renderExpr`, `{win}` = kova) + servis eşleyicisi; yön "down" ise dip ve alt sınır; hacim `null` + neden (gauge/oran).
Thanos (`@cluster`) olayları ve consumer lag (H2) kapsam dışı. Maliyet: açık olay başına tik başına 1 VM aralık sorgusu, tik başına ≤ 20.

**H5 — hata oranı göçü (`svc_error_rate`, `op_error_rate`).** ✅ **gemilendi v0.10.2221** (`internal/ml/error_rate_record.go`,
`error_rate_record_test.go`; ortak `BuildREDRecord`). Seri = err/count oranının VM RED ifadesi (yüzde), servis [× route], kova penceresi;
aile + etiketler tik anında kanıta (`statusLabel` dahil). Bant: op medyan ± criticalZ·effMAD; servis üst max(2 × medyan, %1), alt 0.
Hacim aynı ailenin `_count`'u. Maliyet: açık olay başına tik başına 2 VM aralık sorgusu, tik başına ≤ 10 olay.

**H6 — `throughput_drop` göçü.** ✅ **gemilendi v0.10.2222** (`internal/ml/throughput_record.go`, `throughput_record_test.go`). Seri =
servis × route istek/sn'si (`count` ölçüsünün VM ifadesi, kova penceresi); yön "down" (dip, alt sınır); hacim aynı seriden (istek/sn × adım).
Maliyet: açık olay başına tik başına 1 VM aralık sorgusu, tik başına ≤ 20.

**H7 — `trace_error_count` göçü.** ✅ **gemilendi v0.10.2223** (`internal/ml/trace_error_record.go`, `trace_error_record_test.go`;
okuma `chstore.MLTraceErrorMinutes`). Seri = `spanmetrics_1m` giriş span'i hata sayısı, kova toplamı; snapshot dakika okumasının kayan kova
toplamı. Bant: medyan (beklenen) ayrıldı, üst sınır `normalMax`, alt 0. Hacim `null` + neden (çağrı sayısı örneklenmiş). Maliyet: açık olay
başına tik başına 1 CH MV okuması, tik başına ≤ 20.

**H8 — Thanos `metric_*` göçü.** ✅ **gemilendi v0.10.2224** (`internal/ml/metric_thanos_record.go`, `metric_thanos_record_test.go`;
aralık okuması `thanos.WorkerQuery` + `WorkerLimits.Range*`). Pod kümesi + cluster tik anında kanıta dondurulur (≤ 50 pod); seri = pod düzeyi
ifadenin aynı `combine` birleşimi, kaynak `thanos:<cluster>`; pod görünümü kanıt satırına iner. Maliyet: açık olay başına tik başına 1 Thanos
aralık sorgusu, tik başına ≤ 10.

**H9 — `dependency_latency` / `dependency_error_rate` göçü.** ✅ **gemilendi v0.10.2234** (`internal/ml/dependency_record.go`,
`dependency_record_test.go`). Servis + hedef etiket adları tik anında kanıta (`svcLabel/targetLabel`; render anında bugünkü ayardan yeniden
kurulmaz). Seri = servis × hedef client histogramı, kova penceresi: p95 (ms) ya da `error_type` dolu çağrı oranı (yüzde, `clamp_max` 100);
bant medyan ± criticalZ·effMAD; hacim aynı çiftin `_count`'u. Maliyet: açık olay başına tik başına 2 VM aralık sorgusu, tik başına ≤ 10 olay.

**H10 — `trace_change_point` / `change_point` göçü.** ✅ **gemilendi v0.10.2235** (`internal/ml/change_point_record.go`,
`change_point_record_test.go`). Seri = dedektörün koştuğu `ml_series` parçası (route `|p95` ya da log `_total`/`_error`, hedef kova); CUSUM'un
gördüğü bitişik kuyruk (kırılmadan 30 dk önce → son kova) ve kova başına referans bandı (aynı takvim sınıfı dilimi, Q50 ± k·σ — CUSUM'un katkı
eşiği) tik anında kanıta dondurulur, yeniden okunmaz. Snapshot 1 dk ızgarada basamak (kova değeri kova boyunca); `started_at` (hesaplanmış
kırılma) değişmedi, `onset_at` seriden. Hacim log'da aynı seriden, trace p95'te gerekçeli yok. ±15 dk rollout bağımsız kanıt. Maliyet: ek sorgu yok.

**H11 — `log_volume` / log `error_rate` / `service_silent` göçü.** ✅ **gemilendi v0.10.2236** (`internal/ml/log_red_record.go`,
`log_red_record_test.go`). Seri = tikte ES sayımından `ml_series`'e yazılan servis × `_total`/`_error` kova sayısı (ES adıyla,
`logServiceOf`). Snapshot için ES'e gidilmez: aynı seri tek servis, zaman sınırlı CH okumasıyla (`MLSeriesRange`, LIMIT + 10 sn), 1 dk
ızgarada basamak (H10 kurucusu); eksik kova 0 (susma). Bant medyan ± criticalZ·effMAD; hacim aynı seriden (log satırı). Maliyet: açık olay
başına tik başına 1 CH okuması, 0 ES sorgusu, tik başına ≤ 20.

**H12 — `trace_pod_outlier` + `svc_breadth` göçü.** ✅ **gemilendi v0.10.2237** (`internal/ml/pod_outlier_record.go`,
`breadth_record.go`, `pod_breadth_record_test.go`). *Pod:* seri = aykırı pod'un ölçüsü (5xx oranı % ya da p95 ms), dedektör penceresiyle;
servis/pod/durum etiketleri tik anında kanıta. Taban `cross_section_pod_median`: her dakikada servisin uygun pod'larının medyanı ve
dedektörün eşiği (err: max(medyan + z·max(1,4826·MAD, 0,02), 2 × medyan); p95: max(medyan + z·effMAD, 1,5 × medyan)), alt 0. Hacim pod'un
dakika başına çağrısı. Maliyet: açık olay başına tik başına 3 VM aralık sorgusu, tik başına ≤ 6 olay (≤ 18). *svc_breadth:* seri = "yavaş
operasyon sayısı" (tik başına üye kuralını sağlayan farklı operasyon), süreç-içi geçmişten (lider değişince sıfır → onset görünmezse kayıt
`invalid`, kart eski yol); bant sabit kural (üst `breadthMinOps − 1`); üyeler kanıtta bağımsız değil; hacim gerekçeli yok. Ek sorgu yok.
Sınıf her ikisinde `service_anomaly` (svc_breadth için `info` kararı K8/C ile birlikte açık).

**Kalan (H12 sonrası).** Log `pod_outlier` — atlandı: pod başına seri `ml_series`'te yok; snapshot ES pod kırılım histogramı ister
(logstore'da toplu yöntem yok; yeni ES yükü) ve sözleşmede snapshot'sız sayısal kayıt istisnası yok. `template_spike` / `rare_template`
(aile sorgusu kayda; seri `ml_series` `tpl|`), `new_template` / `rare_error_code` (`series.kind=occurrence` dalı), diğer oluşum türleri.

**H3…Hn — dedektör başına birer sürüm** (§9 tablosu; önerilen sıra: VM metrik → `trace_error_count` → `svc_error_rate` → TraceRED →
bağımlılık → kırılma noktası → log RED → şablon → pod → `svc_breadth` → oluşum türleri). *Risk:* S/M/L sütunu. *Göç:* yok. *Test:* dedektör başına ölçüt 9 + doğrulayıcı.

**A1 — yayın kapıları, gölge.** Recorder'da `qualityFilter` sonrası tek katman (`internal/ml/publish_gate.go`): seri tutarlılığı,
görünür sapma, kanıt (bağımsız < 2 → `info`), etki (hata oranı ve gecikme bant içinde → `service_anomaly` olamaz). Sonuç yalnız
`gates[]`'e yazılır, hiçbir şey gizlenmez. *Risk:* düşük. *Göç:* yok. *Test:* kapı başına bir geçen bir kalan örnek.
✅ **gemilendi v0.10.2241** (`internal/ml/publish_gate.go`, `publish_gate_test.go`; çağrı `applyRecordContract`). Doğrulayıcıdan geçen
her kayıt yazımdan önce değerlendirilir; kapılar yalnız kaydı okur (ek sorgu yok), veri yoksa satır `notEvaluable` + neden (geçti sayılır).
Satırlar: `series_consistency` (tutarsızlık sayısı, eşik 0), `visible_deviation` (olay penceresinde yönünde art arda bant dışı dk; gecikme
serisinde ≥ 3, diğerlerinde ≥ 1 nokta), yalnız gecikme serisinde (ms | `slow_ratio` | `ml:op_latency`) `min_volume` (`volume` ≥ 100 çağrı)
ve `min_abs_diff` (|tepe − beklenen| ≥ 25 ms; `slow_ratio`'da değerlendirilemez), `independent_evidence` (≥ 2; bağımsız işaretsiz, başlık
tekrarı, seriyle aynı kaynak + K9 dışı tür, türetilmiş serinin onay satırı sayılmaz), `service_impact` (seri hata oranı/gecikmeyse yukarı
yönde bant dışı dk > 0; diğer ölçülerde kayıtta hata oranı/gecikme yok → değerlendirilemez). Eşikler kod sabiti (K2). Sınıf,
`publish_state`, bildirim, Problem terfisi, kart **değişmedi**. Gölge raporu: `GET /api/ml/scorecard` → `tick.publishGates` (dedektör
başına son 24 sa farklı olay: evaluated / wouldPublish / wouldDrop / wouldInfo / wouldTelemetryVolume / notEvaluated (sözleşmesiz +
invalid) / invalid / failedByGate / notEvaluableByGate) + tik satırı `[ml] tick publish_gates_shadow …` (toplamlar). Sınır:
`wouldTelemetryVolume` A1'de hep 0 (hiçbir kayıt hata oranı + gecikmeyi birlikte taşımıyor; B/C hacim kaydıyla dolar).
**A2'nin değiştireceği:** `wouldDrop` olaylar `publish_state=held` (liste SQL'i ve grup/servis Problem kapısı düşürür); kanıt/etki kalanlar
`class=info` (Problem adayı değil, tek sinyal kolu kapanır — K10); bildirim yalnız yayımlanan `service_anomaly`'den.
**A2 — kapılar yürürlükte.** Gölge raporu operatörce okunduktan sonra: `publish_state=held` listeden ve Problem kapısından düşer.
*Risk:* **yüksek** — gerçek olay gizlenebilir; B16 çelişkisi çözülmeden açılmaz. *Göç:* yok. *Test:* liste SQL süzgeci pini, grup kapısı pini.

**F — "hızlı şerit".** Tanım alınamadı; plan yazılamadı (§12-K12).

**B — hacim dedektörü.** ML recorder'da yeni dedektör (SRE pod'u; varsayım §12-K5), evaluator kuralı emekli (açık epizotlar mevcut
"aile kapalı" yoluyla kapanır, `internal/evaluator/selfhealth.go:274`; `VolumeSpikeFactor/MinSpans` ayarları silinir, shim yok). Ölçü: istek başına span =
`service_summary_5m` span / `spanmetrics_1m` giriş span'i (server + consumer), saatlik; ikisi de MV, trace düzeyi örneklemeden etkilenmez.
İstek hızı da arttıysa "trafik artışı", oran arttıysa "telemetri hacmi değişimi". Taban: geçmiş haftaların aynı gün + saati, mevcut takvimle
(`anomaly.CalendarDayClass`, `internal/anomaly/calendar_export.go:17`; `FixedHolidaysTR`, `chstore/anomaly_calendar.go:65-68`); saatlik
oran `ml_series`'e yazılır (taban okuması oradan, her tikte 4 haftalık MV taraması yok). Başlangıç: 5 dk seride kırılma noktası
(`change_point.go` CUSUM çekirdeği); bitiş: seri banda dönünce. *Risk:* orta-yüksek — giriş span'i olmayan servis (oran tanımsız → ham
sayım + her zaman `telemetry_volume`); 4 haftada dilim başına ≤ 4 örnek (K6); iki yazıcı aynı anda yaşamamalı (yeni dedektör adı → kimlik
uzayı ayrık). *Göç:* yok (`ml_series` yeni `part`). *Test:* saf karar tablo testi; kayan pencere simülasyonu; örnek 1 negatif fixture.
✅ **gemilendi v0.10.2242** (`internal/ml/volume.go`, `volume_record.go`, `volume_test.go`; okuma `chstore/ml_volume.go`). Dedektör
`svc_telemetry_volume` (ayrı anahtar yok, ML ana anahtarı; lider). Ölçü 5 dk istek başına span (giriş span'i yoksa 5 dk span sayısı); taban
`ml_series` signal `volume` (part `ratio`/`spans`, saatlik), takvim sınıfı × saat ±1, ≥ 4 örnek, K6 küçük örnek bandı; karar art arda 3 kova
+ saatlik ≥ 6.000 span. Onset kayıtta seriden (`seriesWindow`; CUSUM kullanılmadı). Kayıt `context[]` (yeni isteğe bağlı alan): aynı
pencerede servis hata oranı + p95 (VM, olay öncesi referans bandı); A1 etki kapısı bunları okur → `wouldTelemetryVolume`. Rejim muafiyeti +
7 gün tavanı (K15; "yeni normal" notu yok). Evaluator kuralı + `VolumeSpike*` ayarları silindi. Maliyet: tik başına 3 CH; açık olay başına
1 CH + ≤ 2 VM (tik başına ≤ 5 olay, ≤ 10 VM).

**C — sınıf `telemetry_volume`.** Servis ekibi listesinden SQL'de gizli (`class` süzgeci; eski `svc_volume_spike` satırları dedektör adıyla),
platform görünümünde listelenir (yer K8). Kart şablonu: "<servis> istek başına span sayısı <a>'dan <b>'ye çıktı (<tarih saat>'ten beri).
İstek hızı, hata oranı ve gecikme değişmedi. Servis etkisi yok; ingest hacmi <k> kat." Aday neden kanıtları: değişim anına yakın rollout
(`GetServiceDeploys`), span-kind dağılım kayması (`spanmetrics_1m.kind`), yeni görülen instrumentation scope adı (`spans` scope alanı —
MV'si var mı doğrulanmadı; yoksa bu kanıt ertelenir, ham `spans` taraması yazılmaz). *Risk:* düşük-orta; rejim kuralı (6 sa sonra bastırma,
`quality.go:366-369`) bu sınıfta "<tarih>'ten beri" kartını öldürür → muafiyet + süre tavanı gerekir (K15). *Göç:* yok. *Test:* şablon golden; liste süzgeci pini.
Yeni uç gerekirse kendi dosyasında `registerMLPlatformRoutes` (`api.go` büyümez).
✅ **gemilendi v0.10.2243** (`chstore/ml_events.go` `MLEventsFilter.View`, `api/anomalies_unified.go` `view=`,
`ml.ShadowHeld`; FE `AnomalyStreamsPage` sekmeleri, `lib/anomalyView.ts`). Yeni uç yok: `/api/anomalies/unified?view=platform|held`.
Servis listesi SQL'de `NOT (class = 'telemetry_volume' OR detector = 'svc_volume_spike')`; Platform tersini okur (eski dedektör olayı
okunmaz). Elenenler (yalnız admin, 403): SQL ön süzgeci (kayıtta kalan kapı satırı) + `ml.ShadowHeld` (gölge `Drop`); salt okuma — liste,
bildirim, Problem değişmez. Varsayılan `View ""` diğer okuyucuları süzmez (Inbox/menü rozeti `ListAnomalyFeed` dahil). Yapılmayanlar:
kart şablonu ("<a>'dan <b>'ye … ingest hacmi <k> kat"; başlık B kaydından), aday neden kanıtları (rollout, span-kind kayması, scope adı).

**D0/G0 — mockup.** ✅ **onaylandı 2026-11-15** (https://claude.ai/artifact/RNzbH98KCNtJhVb3vJZ6xQ; kararlar §12). Kod yok. G, D, A, B, C artık engelsiz.

**G — karar destekleyen tek grafik.** Kartta tek bileşen (grafik katmanının hedef `viz/TimeSeriesPanel`'i; yeni sarmalayıcı yok): birimli
eksen, yuvarlak etiket, gölgeli beklenen bant, sapma bölgesi, `onset_at` ve `detected_at` işaretleri. Kaynak sırası: ham veri duruyorsa canlı
seri (≤ 1 dk; `series.query` kayıttan) + kayıtlı bant; yoksa snapshot; hangisi olduğu küçük etiketle. Snapshot: ±30 dk, ≤ 1 dk, değer + bant + hacim
(sınır §9). Gecikme dedektörü: 1 dk seride karar, art arda ≥ 3 sapma, pencerede asgari çağrı, asgari mutlak fark (ms); seyrek uçta p95 yerine
yavaş çağrı sayısı; süresi tek değerlendirme kovası olan kayıt yayımlanmaz; kart metni hacim taşır ("p95 107 ms (N çağrı)"). *Risk:* yüksek —
olay sayısı ve VM yükü değişir (K7); eşiklerin yeri K2. *Göç:* yok (K7'de öneri seçilirse). *Test:* sayfada tam bir grafik bileşeni kaldığını
sabitleyen test (ölçüt 7); örnek 2 fixture (ölçüt 8); kapı testleri.
✅ **gemilendi v0.10.2247** (FE `anomalies/MLRecordChart.tsx`, `mlRecord.ts`; BE `ml/record_view.go`, `publish_gate.go`
`outsideRunBuckets`). Replay ucu `record` taşır (geçerli kayıt; yok/invalid → `null`, kart eski yol; önbellek anahtarı v4).
Kayıtlı olayda "Olayın seyri" tek bileşen (`viz/TimeSeriesPanel`): kayıttaki seri, seri adımında (5 dk seri 1 dk snapshot'tan
hizalı kovaya; kova değeri yönündeki uç nokta), beklenen bant + kesikli beklenen değer, bant dışı kovalar bölge, başlangıç /
tespit / bitiş işaretleri, sağ eksende hacim (çağrı/dk); servis paneli bağlantı (K4 A). Teknik ayrıntıdaki tam oynatıcı ve ısı
haritası kayıtlı olayda çizilmez (ölçüt 7 testi `MLRecordChart.layout.test.tsx`). Kapı: `visible_deviation` artık seri KOVASI
sayar (kovadaki herhangi bir nokta bant dışı → kova bant dışı); gecikmede art arda ≥ 3 kova, diğerlerinde ≥ 1. Önceden 5 dk
kovanın 1 dk basamağı "art arda 5 dk" sayılıp tek kovalık gecikme sapması kapıyı geçiyordu. Gölge kalır. Yapılmayanlar: canlı
seri (render'da sorgu), 1 dk gecikme kararı (K7), seyrek uçta yavaş çağrı sayısı.

**D — kart.** Başlık şablonu (kim, ne, ne zamandan beri, ne kadar); kanıt satırları başlıktan farklı bilgi ve süzülmüş sayfaya bağlantı
(entity id üstünden); kaynak etiketi `series.source`'tan (B10); "Teknik ayrıntı"da kapılar + seri tanımı; "anomali değil" neden sorar
(grafikte sapma yok / servis etkisi yok / beklenen / diğer + serbest metin). *Risk:* orta; neden alanı yalnız insan/eval incelemesi içindir,
`Labels.Feedback`'e **girmez** (B14, K3). *Göç:* `anomaly_verdicts`'e `reason LowCardinality(String) DEFAULT ''` (güvenli sınıf; RMT tam satır:
`UpsertAnomalyVerdict` + okuyucuları taşır) — ya da DDL'siz `note` öneki (önerilmez: sayılamaz). *Test:* şablon golden; kanıt ≠ başlık pini; neden zorunluluğu.
✅ **gemilendi v0.10.2248** (FE `anomalies/MLRecordCard.tsx`, `mlRecordText.ts`; `MLReplaySheet` kayıtlı olayda yeni kartı çizer,
kayıtsız olay eski kart). Mockup "ÖNERİLEN" sırası: servis · operasyon + durum + sınıf çipi → düz dilde cümle (tespit anında / sürerken
"şu an" değeri, hacim, beklenen) + zaman satırı (başlangıç, tespit, banda dönüş, adlandırılmış tepe/dip) → G grafiği → ölçülen /
beklenen (birimli; hacim kaydında yan seriler önce → olay sırasında, ingest katı) → kanıt satırları (metin kayıt alanlarından FE'de,
etiket bağımsız / aynı seri ailesi / bağlam, kaynak `series.source`/kanıt kaynağından, süzülmüş sayfaya bağlantı) → eylemler → kapalı
teknik ayrıntı (yayın kapıları gölge notuyla + seri tanımı, bant, zamanlar, sınıf · yayın). `telemetry_volume` şablonu (C'den açık
kalan): "<servis> istek başına span sayısı a değerinden b değerine çıktı (<tarih saat> itibarıyla). Hata oranı ve gecikme değişmedi.
Servis etkisi yok; ingest hacmi k kat." — "değişmedi" yalnız etki kapısı "etki yok" derse; k = hacim şeridinin olay penceresi /
öncesi ortalaması; istek hızı kayıtta olmadığından cümleye girmedi. Metinler i18n (EN + TR). Kritik yol yüzdesi cümlesi yok; oylar
yalnız etiket. Yapılmayanlar: "Sorun değil" neden alanı (şema değişikliği gerektirir), liste satırı özeti, aday neden kanıtları
(span-kind kayması, scope adı), canlı seri.

**E — replay.** Özgün tasarım (aşağıda) fixture biçimi `internal/ml/testdata/replay/<id>.json` (seri + taban örnekleri + ayar özeti + beklenen `publish_state`/`class`/`onset_at`);
yükleyici + negatif iddia; `make eval-ml` karnesi. Örnek 1: servis anomalisi **üretmez**, `telemetry_volume` `info` kaydı üretir, başlangıç =
gerçek değişim anı. Örnek 2: hacim/süreklilik koşulunu sağlamıyorsa yayımlanmaz; sağlıyorsa `onset_at` = yükselişin başladığı an.
*Risk:* düşük; veri TTL'i (aşağıda). *Göç:* yok. *Test:* kendisi.
✅ **gemilendi v0.10.2252** (`internal/ml/replay.go`, `replay_test.go`, `cmd/ml-replay`, `make eval-ml`; sentetik fikstür
`internal/ml/testdata/replay/`). Çevrimdışı ve deterministik: §7.1 biçimindeki dışa aktarımı (olay başına dizin,
`ml_events.jsonl` ya da `.jsonl.gz`, JSONEachRow) okur; her satırın `record` kolonunu BUGÜNKÜ doğrulayıcıdan (`ValidateRecord`) ve gölge
kapılardan (`EvaluateGates` + `gateVerdict`) geçirir — yazım yoluyla aynı karar (`contractVerdict`, `applyRecordContract` de onu çağırır).
ClickHouse'a bağlanmaz, yazmaz; üretim kodu çağırmaz (`TestReplayNotCalledFromProduction` pini); gölge sayacına dokunmaz.
Rapor dedektör başına gölge raporuyla aynı sayaçlar: `evaluated / would_publish / would_drop / would_info / would_telemetry_volume /
not_evaluable (kayıt yok + invalid) / invalid / failed_by_gate`, ardından olay satırları (sonuç, kalan kapılar, ilk ihlal) ve beklenti
özeti. Fikstür dizininde isteğe bağlı `expect.json` (`{"<olay id>": {"outcome": "publish|drop|info|telemetry_volume|invalid|no_record",
"failedGates": [...], "violations": [...]}}`) varsa karşılaştırılır; tutmayan olay çıkış kodu 2.
**Operatör için:** (1) §7.1 dışa aktarımını (kayıt kolonları eklenmiş hâliyle) koş; dosyalar `internal/ml/testdata/fixtures/<olay-id>/`
altına düşer (`.gitignore`'da, commit edilmez). (2) `make eval-ml` (= `go run ./cmd/ml-replay -dir internal/ml/testdata/fixtures`;
başka dizin `ML_REPLAY_DIR=…`, JSON çıktı `go run ./cmd/ml-replay -json -dir …`). (3) `go test ./internal/ml -run TestReplayExportedFixtures -v`
aynı dizini test olarak koşar (dizin yoksa atlanır). Sentetik koşum: `make eval-ml ML_REPLAY_DIR=internal/ml/testdata/replay`.
Sentetik fikstürler (uydurma veri, nötr adlar; `go test ./internal/ml -run TestReplayWriteSynthetic -update-replay` ile kuruculardan
yeniden üretilir): temiz gecikme olayı → publish; 5 dk kovalık tek sıçrama → drop (`visible_deviation` 1 < 3 kova); tek bağımsız kanıtlı hata
oranı → info; yalnız hacim (yan seriler bant içinde) → telemetry_volume; tepe seriyle uyuşmayan kayıt → invalid (`peak_from_series`);
kayıtsız eski olay → no_record. Yapılmayanlar: kaydı olmayan eski olayı ham seriden (`ml_series` / `spanmetrics_1m`) dedektörü yeniden
koşarak yargılamak (dedektör başına saf karar yolu ister; örnek 1/2'nin §11 beklentileri buna bağlı); `onset_at ± tolerans` beklentisi.

## 11. Kabul ölçütleri eşlemesi (alınanlar)

| # | Ölçüt | Dilim |
|---|---|---|
| 1–6 | alınamadı | — |
| 7 | kart sayfasında tam bir grafik bileşeni (testle) | G |
| 8 | örnek 2 replay: koşul yoksa yayımlanmaz; varsa `onset_at` yükselişin başı (cümlenin devamı alınamadı) | G, E |
| 9 | göç eden dedektörde render'da ölçü sayısı için ek sorgu yok (testle) | H2…Hn |
| 10 | üç örneğin bugünkü hâli doğrulayıcıdan kalır, düzeltilmişi geçer | H1 (üçüncüsü ayrıntı gelince) |
| 11 | gölge raporunda "doğrulayıcıdan kalan kayıt (dedektör başına)" | H1 |

Beklenen doğrulayıcı sonuçları: **örnek 1** — seri tanımı yok / çizilen ≠ test edilen; başlık sayıları hiçbir serinin değeri değil; `onset_at`
seriden türemiyor; kanıt = başlık (bağımsız 0). **Örnek 2** — tepe (≈135) olay penceresi dışında ve başlıktaki 107'den farklı; hacim yok;
snapshot adımı 300 sn; bant yöntemi alarm bandı değil. **Örnek 3** — (mekanizmadan) başlık sayısı kayıtta yok, render anında üretiliyor.

## 12. Operatör için açık kararlar

| # | Karar | Öneri |
|---|---|---|
| K1 | Sözleşme nerede saklanır | `ml_events` + 3 tipli kolon + `record` JSON; seri `ml_event_snapshots` mevcut kolonlarında (§9) |
| K2 | G eşikleri "hepsi config'te" ↔ kalıcı kural "yeni ayar/vida yok" | **kod sabiti**; operatör açıkça config derse `MLConfig`'e eklenir |
| K3 | D'nin neden alanı ↔ "geri bildirim çalışma zamanı kararını sürmez"; bugün `feedbackZ` sürüyor (B14) | neden alanı yalnız kayıt; `feedbackZ` ayrı bir dilimde kaldırılsın. Karne etiketi olarak `verdict` kullanımı ("Gerçek anomali / Sorun değil" açık karar, 👍/👎 değil) sürsün mü — teyit |
| K4 | "Test edilen seriyi çiz" ↔ v0.10.2033 "servis sayfasındaki paneli çiz" | test edilen seri çizilir; servis paneli bağlantı olarak kalır |
| K5 | Kesik cümle "ML işleri SRE rolü pod'unda k…" | varsayım: "koşar" — yeni hacim dedektörü, kapılar, doğrulayıcı ve snapshot yazımı ML recorder'da (SRE rolü, `COREMETRY_ML_ROLE` varsayılanı); evaluator kuralı worker'dan kalkar |
| K6 | B tabanı: aynı gün + saat için 4 haftada ≤ 4 örnek (`ml_series` TTL 28 g, `MinBaselineSamples` 20) | saat ±1 ile ≤ 12 örnek + küçük örnek kuralı (dilim en büyüğü × pay + mutlak fark); TTL uzatma (DDL) yapılmasın. Haftalık dilim yolu (`baseline_band.go`/`baseline_obs.go`) canlıda besleniyor mu — doğrulanmalı |
| K7 | 1 dk gecikme kararı: `ml_series` 1 dk (satır ×5) ↔ aralık sorgusuyla bellek içi karar + 5 dk bandın 1 dk'ya açılması | ikincisi; tik başına sorgu sayısı aynı kalır, `baseline.method` yerel çözünürlüğü yazar |
| K8 | "Platform görünümü" hangi yüzey | Anomalies sayfasında ayrı sekme (mockup D0'da); yeni ayar yok |
| K9 | Bağımsız kanıt tanımı | farklı kaynak **ya da** farklı ölçü ailesi; türetilmiş dedektör kendi girdisinden bağımsız değil; rollout / endpoint kırılımı / exception bağımsız sayılır |
| K10 | Tek sinyal Problem kolu (v0.10.2092) ↔ "bağımsız kanıt < 2 → Problem adayı değil" (B16) | kol kalır ama `evidence[]` bağımsız sayısı ≥ 2 şartına bağlanır; A2 öncesi gölge raporunda etkisi ölçülür |
| K11 | Eski `svc_volume_spike` satırları (≤ 30 g) | yeniden yazılmaz; C'de dedektör adıyla servis listesinden gizlenir |
| K12 | Alınamayan girdiler: soru 9–12, ölçüt 1–6, ölçüt 8'in sonu, örnek `b4d95f…`, Dilim F | yeniden iletilmeli; F ve örnek 3 testleri onlara bağlı |
| K13 | Fixture verisi TTL'i: `ml_series` 28 g, `spanmetrics_1m` 30 g | iki örneğin serileri **şimdi** dışa aktarılsın (operatör; Faz 1 beklenmeden) |
| K14 | Gecikme trafik kapısının birimi (B12) | G içinde netleştirilsin: "kovada asgari çağrı sayısı" olarak yeniden yazılır |
| K15 | `telemetry_volume` kaydının ömrü ↔ 6 sa rejim bastırması | sınıf rejim kuralından muaf; banda dönünce ya da 7 gün sonra "yeni normal" notuyla kapanır |
| K16 | A2 (kapıların yürürlüğe girmesi) koşulu | gölge raporu operatörce onaylanmadan açılmaz (Problem standardı R-dilimleriyle aynı düzen) |

**Kayıtlı kararlar (2026-11-15).** Operatör "sırayla" ilerlemeyi onayladı, H1'den başlanır. K1 öneri yönünde (H1'in
kapsamı). K2 öneriyi izler — eşikler **kod sabiti**, operatör açıkça "config" demedikçe `MLConfig`'e eklenmez. K13:
dışa aktarım sorguları §7.1'de, operatör koşar. **Açık kalanlar:** K3, K4, K10 (ve K5–K9, K11, K12, K14–K16 ilgili
dilime gelince sorulur).

**Kayıtlı kararlar (2026-11-15, ikinci tur; koordinatörün "mockup onay" yorumu — operatöre bildirildi, düzeltebilir).**
- **Mockup (D0/G0) onaylı:** https://claude.ai/artifact/RNzbH98KCNtJhVb3vJZ6xQ. **K4 = A seçeneği:** kart yalnız test edilen seriyi
  çizer (servis paneli bağlantı). **K8:** `telemetry_volume` için "Platform" sekmesi + yalnız admin "Elenenler" sekmesi, çizildiği gibi.
  G kapı eşikleri **kod sabiti**: art arda ≥ 3 dakika, pencerede ≥ 100 çağrı, ≥ 25 ms mutlak fark. Başlık sayısı kuralı çizildiği gibi:
  tespit anındaki değer + kapanınca adlandırılmış tepe; sürerken "şu an". Çağrı/dk hacim şeridi tek grafik bileşeninin parçası.
- **K3:** "Gerçek anomali / Sorun değil" kararları çalışma zamanında eşik **değiştirmez** — `feedbackZ` etkisi kendi diliminde kalkar
  (H2 değil); karar + neden yalnız insan incelemesi ve ML karnesi için saklanır. ✅ **uygulandı v0.10.2218** (`qualityFilter` kararları
  okumaz; `Labels` yalnız taban hijyeni + backtest). Açık soru: taban hijyeni ("Gerçek anomali" pencereleri tabandan dışlanır,
  `Labels.Exclude`, ayar `hygieneOff`) de karara bağlı bir çalışma zamanı etkisidir — kalsın mı, operatöre soruldu.
  **Operatör kararı 2026-11-15:** taban hijyeni **kalır** — "Gerçek anomali" işaretli pencereler tabandan dışlanmaya devam eder
  (`Labels.Exclude` / `hygieneOff`); K3 ihlali sayılmaz (gerçek olayları "normal"in dışında tutar). Kod değişikliği yok.
- **K10:** "bağımsız kanıt < 2 → sınıf `info`, Problem adayı değil" kapısı geçerli; tek sinyal Problem kolu kapı yürürlüğe girince
  kalkar (önce gölge, A1 → A2).
- **K2:** eşikler kod sabiti, yeni ayar yok.
- H2 bunlardan hiçbirini uygulamaz: kayıtta `gates[]` boş, sınıf bugünkü davranış (`service_anomaly`), başlık H2 biçiminde
  (tespit anı + kapanınca tepe; "şu an" D'de).
