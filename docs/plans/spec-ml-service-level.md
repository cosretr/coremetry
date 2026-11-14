# Spec — ML servis düzeyi: svc-slowdown ve error_rate yerine (2026-11-12)

## Hedef
`internal/ml` servis düzeyinde RED bilmiyor; trace dedektörleri servis×route/operasyon (trace_red.go:3, trace_errors.go:9). Bu yüzden iki eski yol emekli edilemiyor, ikisi de keep listesiyle P1 taşıyor (problem_priority_inbox.go:36, :51). Hedef: ML bu iki işi üstlensin, P1 davranışı kaybolmasın, sonra eski yollar ve keep kalıpları silinsin.

## Bugün ne var (koddan)
| | `svc-slowdown:<svc>` (1091) | `anomaly:<svc>:error_rate` (1072) |
|---|---|---|
| Tetik | Tek 5 dk kova, sürdürme yok. ≥3 op; her biri ≥30 çağrı, p99 ≥ 5000 ms, p99 ≥ 20× kendi 24 sa p95 tabanı; servis ≥100 çağrı/kova. Çöküş kolu: çağrı ≤ %60 × önceki saat, servis p99 ≥ 3× taban (evaluator/service_slowdown.go:13-26; anomaly_sensitivity_slowdown.go:56-66) | modified z ≥ 3.5 açar, critical z ≥ 6; 3 kova (15 dk) dwell; %1 mutlak taban, taban trafik ≥ 0.2 istek/sn; ayrıca error_ramp kolu (≥2× taban, 6 kova) (anomaly.go:41,145; anomaly_sensitivity.go:570-611) |
| Kaynak | CH `operation_summary_5m` + `service_summary_5m`; %5 örnekleme düzeltmesi YOK | VM RED (1478) varsa VM, yoksa `service_summary_5m` (örneklemeyle şişik) |
| Öncelik | Daima critical; Value=op p99, Threshold=24 sa taban → `computePriority` (problem.go:449, :591-600) oran ≥ 2 → P1 | critical; Value=güncel %, Threshold=taban medyanı → çoğu kez P1 |
| -batch | dışlanır | muaf değil (doğru) |
| Varsayılan | `native_detectors.service_slowdown` KAPALI | `native_detectors.metric_anomaly` KAPALI |
| Tüketici | keep `svc-slowdown:*`; notify `problem`; incident attach koşulsuz; ServiceSlowdownSubsection.tsx, alert_rule_series.go:99, problem_standard.go:311 | keep `anomaly:*:error_rate`; notify `anomaly`; incident attach (varsayılan açık); `anomaly.error_rate[.ramp]` standardı |

ML grup Problem'i: ≥2 farklı sinyal → `anomaly-auto:group:<ilk olay>`; Severity critical sabit, Value=Threshold=sinyal sayısı → P2, 4 sa sonra stale-critical P1 (group_gate.go:163-166). Servis başına tek açık grup Problem'i (group_live.go:157-182). Tek sinyal kolu (2058) yalnız gölge: yön up, ≥600 sn, Observed/Baseline ≥ 2 (group_gate.go:171-243).

## Seçenekler — P1 sorusu
- **(a) Servis olayı tek başına açar.** Yeni aile `anomaly-auto:svc:<svc>`; Value=gözlenen, Threshold=mevsimsel taban → `computePriority` eskisiyle aynı yoldan P1 (≥2×). Keep listesine `anomaly-auto:svc:*` eklenir. Artı: eski semantiği birebir taşır, tek sinyalde de çalışır. Eksi: yeni kural ailesi, açma kapısı gürültü riski taşır (gölgeyle ölçülür).
- **(b) Yalnız grupta sinyal; critical servis üyesi varsa grup P1.** Artı: yeni aile yok. Eksi: grup ≥2 FARKLI sinyal ister; yaygın yavaşlama ve çoğu hata oranı olayı yalnız trace sinyalidir → Problem hiç açılmaz.
- **(c) Eski yollar kalıcı.** Artı: sıfır iş. Eksi: iki motor, CH MV'de %5 örnekleme sapması (svc-slowdown), ikisi de varsayılan kapalı native kapıya bağlı; "anomali tekilleştirme" hedefiyle çelişir.

| Olay | (a) | (b) | (c) |
|---|---|---|---|
| CRM yaygın yavaşlama (1091): çok op yavaş, yalnız trace | `anomaly-auto:svc:crm` P1 (genişlik ≥3 op, oran ≥2) | Tek sinyal → yalnız gölge, Problem YOK (log da bozulduysa P2, 4 sa sonra P1) | `svc-slowdown:crm` P1 (native açıksa) |
| Kritik hata oranı (1072) | `anomaly-auto:svc:<svc>` P1 (VM oranı ≥2× taban, 15 dk) | İkinci sinyal (log hatası) yoksa YOK; varsa P2 → 4 sa P1 | P1 (metric_anomaly açıksa) |

**Öneri: (a).** Yalnız (a) iki tarihî olayda da ilk tikte P1 verir; (b) asıl sorunu (tek sinyalde sessizlik) geri getirir; (c) tekilleştirmeyi kalıcı olarak bırakır.

## Karar 1 — Dedektörler
1. **`svc_error_rate`** (sinyal trace, anahtar servis). Kaynak YALNIZ VM RED (örneklenmemiş); VM yok/ısınıyorsa Hold, CH'ye düşülmez. Aile (HTTP/gRPC) başına 2 anlık PromQL: `sum by (svc)(rate(<pin>_count[5m]))` ve aynı ifade `status=~ServerErr` ile. Ölçü err/count; açılış: oran ≥ 2× mevsimsel taban (medyan+MAD, baselineKind), güncel ≥ %1, taban trafik ≥ 0.2 istek/sn, 3 tik (15 dk) sürmüş. `-batch` muaf değil (batchKeep yük sinyali saymaz). Giriş-span ilkesi: VM RED metriği sunucu tarafı; doğrulanacak (aşağı).
2. **`svc_breadth`** ("yaygın yavaşlama") — yeni sorgu YOK, açık per-route olaylardan türetilir (Karar 3).
- p95/p99 servis ölçüsü ve servis throughput'u EKLENMEZ: servis p95'i az trafikli op'lardaki yavaşlığı gizler; throughput_drop zaten route düzeyinde var. Çöküş kolu: svc_breadth kanıtına svc_error_rate'in count serisinden "trafik −%X" satırı eklenir (bedava).
- **Tik maliyeti:** 2 aile × 2 = 4 VM anlık sorgu / 300 sn tik; dönüş ≤ servis sayısı (prod ~1300, test 4000 seri); `ml_series` yazımı servis başına 1 satır/tik; CH/ES okuması 0. Bütçe vm_budget.go'dan; top-N gerekmez (4000 seri trace_red'in 5000 route tavanının altında).

## Karar 3 — Yaygın yavaşlama
Türetilmiş kural, saf fonksiyon: aynı serviste aynı anda açık ≥3 farklı `op_latency` olayı, her biri Observed/Baseline ≥ 2 ve p95 ≥ 1000 ms, en az 2 tik (10 dk); servis trafiği ≥ 100 çağrı/5 dk (svc_error_rate count'undan). Value = op oranlarının medyanı × taban, Threshold = taban → oran ≥ 2 P1. `-batch` servis dışlanır (eskisiyle aynı). Neden türetilmiş: genişlik sinyali zaten var, sorgu maliyeti 0, eskinin "çok op" tanımıyla birebir.

## Karar 4 — Gölge kolu ve dedupe
- Tek sinyal gölgesi (2058) per-route olaylar için sürer; `svc_*` olaylar gölge hesabından ÇIKAR (canlı karşılığı oldukları için çift sayım olmaz).
- **Servis başına tek açık ML Problem'i:** `anomaly-auto:svc:<svc>` ve `anomaly-auto:group:*` aynı dedupe anahtarını (servis) paylaşır. Hangisi önce açtıysa sahibi o; sonra gelen üye olarak eklenir, satır tam taşınır, Value/Threshold daha yüksek oranla güncellenir (öncelik yalnız yükselir). Kapanış: açık üye kalmayınca, mevcut 3 tik kuralı.
- Bildirim yalnız açılışta; notify kind `anomaly` (önek `anomaly-auto:`), incident attach grup ile aynı.

## Karar 5 — Göç (her dilim kendi sürümü)
1. `svc_error_rate` dedektörü → yalnız `ml_events` (Problem yok); detay sayfasında düz cümle ("X servisinde hata oranı %Y, olağanı %Z").
2. `svc_breadth` türetilmiş kural → `ml_events`.
3. Gölge açma kapısı: `evidence.svcShadow = {wouldOpen, priority, value, threshold}`; mevcut ML karşılaştırma raporuna "eski ↔ yeni" tablosu: servis × zaman örtüşmesiyle eşleşir; sütunlar: eski kural + açılış + öncelik, yeni olurdu + açılış + öncelik, fark (kaçırdı / fazladan / geç). Süre: 14 gün, eski yollar açık kalır.
4. Canlı: `anomaly-auto:svc:*` Problem açar; keep listesine eklenir (inbox göçü v5). **GEMİDE v0.10.2130** (operatör kararı 2026-11-14 "Yenileri canlıya al"; 14 günlük gölge beklenmedi): anahtar `ml.groupProblems` (yeni vida yok); grup ile tek plan `planMLProblems`; aynı serviste eski kural açıksa açıklamaya çift kayıt notu; eski yollar dokunulmadı.
5. Emeklilik (operatör onayıyla, 0 "kaçırdı" sonrası): `native_detectors.service_slowdown` kodu, `metric_anomaly` içindeki yalnız `error_rate` ölçüsü + ramp kolu, keep kalıpları `svc-slowdown:*` ve `anomaly:*:error_rate` silinir (göç v6). Açık eski Problem'ler "rule retired" ile kapanır; yeni aile aynı servisi zaten açmışsa çift kayıt olmaz. metric_anomaly'nin diğer ölçüleri bu spec'in dışında.

## Yüzeyler
Yeni: internal/ml/svc_error_rate.go, svc_breadth.go, svc_gate.go. Değişen: internal/ml/group_gate.go, group_live.go (ortak dedupe), batch.go; internal/chstore/problem_priority_inbox.go + _migrate.go, problem.go (önek sabiti), notify_kind.go; internal/api/problem_standard.go; frontend ProblemDetail.tsx (düz cümle bölümü); ML karşılaştırma raporu; internal/sre/{orchestrator,investigation,recommend}.go (svc-slowdown adı geçiyor). Dilim 5'te silinen: evaluator/service_slowdown.go, chstore/service_slowdown.go, anomaly_sensitivity_slowdown.go, ServiceSlowdownSubsection.tsx, alert_rule_series.go:99 kolu.

## Testler (saf dikişler)
`svcErrorRateVerdict` tablo testi (taban 0, %1 tabanı, 0.2 istek/sn, 15 dk dwell, -batch açar); `breadthVerdict(events)` (2 op açmaz, 3 op açar, -batch açmaz, 1 tik açmaz); `svcPromote` → Value/Threshold ile `computePriority` ≥2× P1, <2× P2; `ownerDedupe(open, incoming)` (sahiplik, öncelik yalnız yükselir); karşılaştırma eşleme fonksiyonu; keep göçü v5/v6 idempotent; inbox_keep_priority_test'e `anomaly-auto:svc:*` satırı.

## Risk
- VM'de durum etiketi olmayan servislerde svc_error_rate kör; eski yol span'e düşüyordu (şişik ama vardı). Gölge raporu bu servisleri "kaynak yok" diye ayrı sayar.
- Genişlik ≥2× eşiği eski 20×'ten çok gevşek → P1 gürültüsü; gölge 14 gün bunu ölçer, eşik sabit varsayılan olarak ayarlanır, vida eklenmez.
- ML tamamen varsayılan kapalı: canlı dilimden sonra ML kapalı kurulumda P1 kaynağı kalmaz; emeklilik ML açık olmayı şart koşar.

## Doğrulanamayanlar
- Prod'da `native_detectors.service_slowdown` / `metric_anomaly` açık mı (old-detectors-ml-gap.md:79-81 de soruyor).
- VM RED serilerinin yalnız server+consumer span'den üretildiği; trace_red'de açık süzgeç görülmedi.
- `BigBreachRatio` varsayılanı 2 (yorumdan çıkarıldı, doğrudan okunmadı).
- ML karşılaştırma raporunun kod içindeki yeri; CoSRE/MCP ve servis sayfası şeritlerinin bu iki kural adına bağımlılığı tam taranmadı.

## Operatörden gereken girdiler (önerilen varsayılanla)
1. P1 için (a) mı? — Öneri: (a), yeni aile `anomaly-auto:svc:*` keep listesinde.
2. Yaygın yavaşlama mutlak tabanı — Öneri: op p95 ≥ 1000 ms (eski p99 ≥ 5000 ms idi).
3. Prod'da iki eski native kapı açık mı? Kapalıysa gölge karşılaştırması tarihî olaylarla (problems tablosu) yapılır — Öneri: 14 gün gölge, eskiler açık.
4. Emeklilikte açık eski Problem'ler — Öneri: "rule retired" ile kapat, yeni aile devralır.
