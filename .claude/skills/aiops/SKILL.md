---
name: aiops
description: Code map + decision trees for Coremetry's AIOps layer — anomaly detectors, alert evaluator, Problem model/priority/lifecycle, incidents, correlation + root-cause synthesis, RCA verdict shields, exception groups/storms, self-health problems, behaviour engine, the notify hand-off and the AI-explain prompt builders. Use BEFORE adding or changing a detector, a rule id, a priority/escalation rule, an incident-attach path, a hypothesis score, an exception ladder step, or any background loop that writes problems/anomaly_events/root_cause_hypotheses. Do NOT use for notification channel details (internal/notify templates), for CH schema design (/clickhouse-schema), for copilot surfaces outside problems/exceptions (/copilot-surface) or for MCP tool wiring (/mcp-tools).
---

# /aiops — dedektörden bildirime, tek harita

Sayım tabanı bu dosyanın sonunda (§13). Satır numaraları 2026-09-17 çalışma
ağacından; işlev adları kalıcı, satırlar kayabilir — `grep -n` ile doğrula.

**Tek cümlelik teşhis:** kural id öneki bu katmanın tip sistemidir; öncelik
okuma anında ve SAF hesaplanır; her arka plan döngüsü `OpenProblemsSnapshot`
üzerinden okur; yeni bir dedektör kendi dosyasında görünmeyen İKİ mekanizmayla
(bayat süpürme + yaş eskalasyonu) çarpışır.

## 1. Topoloji — kim nerede koşar

Tüm döngüler `if mode.worker` (`main.go:652,1221,1251`) + Redis lider kilidi
(`cache.LeaderHolder`); Redis yoksa her pod lider olur ve yüksek sesle uyarır
(`main.go:586-596`). Ayar tazeleyicileri ise HER pod'da 30 s (`main.go:1284-1315`).

| Döngü | Kilit / kurucu | Aralık | Ne yazar |
|---|---|---|---|
| Alert evaluator | `evaluator.go:26` `coremetry:lock:evaluator` | 1 dk | problems, incidents, notify |
| Anomaly detector | `anomaly.go:28` `coremetry:lock:anomaly` | 2 dk (`config.go:474`) | problems (anomaly:*), incidents (kapılı) |
| Anomaly recorder | `recorder.go:36` | 1 dk, pencere 5 dk | anomaly_events (log_pattern / trace_op / new_template) |
| Topology correlator | `correlator/correlator.go:70` — kilit YOK (saf önbellek) | 5 dk | bellek (komşu grafı) |
| Exception refresher | `main.go:1616` | 60 s; bayat süpürme her 6. tik | exception_groups |
| ProblemExplainer / ExceptionExplainer | `problem_explainer.go:43`, `exception_explainer.go:35` | 30 s / 60 s | ai_summary |
| RootCauseSynthesizer | `rootcause_worker.go:84` — `api.NewServer` SONRASI başlar (`main.go:1276`) | 30 s | root_cause_hypotheses |
| ExternalScanner (Oracle serileri) | `external.go:117` ← poller kancası | 30 s | problems (kind=external) |
| Rollout reconciler | `main.go:1124` | ayar | rollout olayları → RCA girdisi |
| Synthetic monitor | `monitor/runner.go:32` | tik | problems (monitor:*) |

**Evaluator tik sırası** (`evaluator.go:452-622`): kurallar → SLO → DB kapasite
→ DB yavaş ifade → DB sağlığı (`db-health`, v0.10.1073) → runtime → paylaşılan exception patlaması → ölümcül
exception → self-health → **yaş eskalasyonu** (:588) → **bayat süpürme** (:601)
→ incident kaskadı (:611) → anomali terfisi (:619). Sıra önemli: eskalasyon
süpürmeden ÖNCE koşar.

**Anomaly tik sırası** (`anomaly.go:481-697`): aktif servisler (24 sa) →
mevsimsel vidalar → izlenen set → duyarlılık (tik başına bir kez) → toplu MV
okuması `batchSeries` → `OpenProblemsSnapshot` → faz 1 kararlar → faz 1.5
kümeleme → bayat küme çözümü → `applyOutcome` → service_silent → davranış
motoru → exception fırtınası.

## 2. Karar ağacı — "yeni bir sinyal üreteceğim"

```
Ne üretiyorsun?
├─ Operatörün TRİYAJ edeceği açık durum? ─────────► Problem (chstore.Problem)
│    RuleID ÖNEKİ SEÇ (§3 tablosu) — önek tip sistemidir; sınıflandırıcıların
│    hepsi öneke bakar: ProblemNotifyKind, ProblemCategory, PollerOwnedRule,
│    escalationExempt, silentProblemsToResolve, resolveStaleClusters.
│    Yeni önek = her tüketiciye satır + kendi paketinden *_notify_kind_pin_test.
├─ Terfi hattına aday, henüz problem değil? ──────► anomaly_events
│    kind + FingerprintAnomaly(kind,pattern,service) (anomaly_event.go:65);
│    yazım yalnız MergeAnomalyCarry üzerinden (bölüm İÇİNDE started_at
│    korunur, peak yalnız yükselir; satır "cleared" iken gelen yazım YENİ
│    bölüm — §5). Terfi: evaluator.go:1296 anomaly_promotion vidaları,
│    sayısal kapı SAF `promotionGate`.
├─ Yalnız bildirim, saklanmayacak? ───────────────► notify-only Problem
│    (incident:* ve exception P1 duyurusu böyle; incident_alert.go:35,
│    exception_notifier.go:136). UpsertProblem ÇAĞIRMA.
└─ Kanıt (RCA girdisi)? ──────────────────────────► correlator.Synthesize'a sinyal
     (hypothesis.go:284); yeni skor katmanı = hypothesis_*_test.go'ya tablo.
```

**Problem üretecek her dedektör için dört zorunlu soru** (feedback:
"seyrek dedektör vs yaşam döngüsü"):

1. **Ölçüm sıklığın tik sıklığından seyrek mi?** `sweepStaleProblems`
   ~3×interval tazelenmeyen açık problemi "kaynak sustu" diye KAPATIR
   (`evaluator.go:1105-1153`). Seyrek ölçen kural = her turda yeniden açılma +
   bildirim seli. Çare: ölçüm seyrek, yaşam döngüsü tik başına (son sonuç
   bellekten sunulur) — ya da `staleSweepCandidates` muafiyeti (:1163,
   yalnız YAŞAYAN kaynak için, v0.10.592/605).
2. **"P1 olmasın / spam olmasın" direktifi var mı?** Yaş eskalasyonu
   (`problem_escalation.go:23-51`: 15 dk info→warning, 30 dk warning→critical)
   + `computePriority` 4 saati aşan critical'ı P1 yapar. Muafiyet
   `escalationExempt` (`evaluator.go:1226`) — hem tazeleme dalına hem
   süpürmeye; tekine konursa v0.8.309 seli geri gelir.
3. **Incident'a bağlanacak mı?** Tek açıcı `AttachProblemToIncidentWith`
   (`incident.go:368-488`; 30 dk aynı servis, 1-hop komşu, yoksa yeni). Anomali
   ailesi tek kapılı olan (`attachToIncident`, `anomaly_sensitivity.go:58-73`).
4. **Tam satır değiştirme:** `UpsertProblem` tüm satırı yazar; tazeleme yolu
   Status/Assignee/Pod/AISummary'yi TAŞIMALI (`carryProblemOperatorState`,
   `problem_aisummary_test.go`). Unutulan alan sıfırlanır (invariant #4).

## 3. Dedektör tablosu — kural id önekleri

| Önek / id | Üretici | Tetik | Pencere | Kind / kapsam |
|---|---|---|---|---|
| `builtin-*`, kural id | `evaluator.go:771` evaluateOne (+9 builtin :286) | compare + ForSec + Cooldown + MinSamples | kural WindowSec; MV fast-path | service; **9 yerleşik varsayılan KAPALI** (v0.10.1069 — operatör: "Built-in alertleri kaldıralım, çok false pozitif geliyor"; HTTP P99 >3s 340×/24 sa, >5s 215×). Satırlar durur (BUILT-IN · OFF), operatör açar. Yükseltme göçü `builtins_default_off.go`: lider tikinde `evaluateAll`'dan önce, tek seferlik — açıkları kapatır, açık/ack problemlerini "rule disabled by default v0.10.1069" ile kapatır (yoksa bayat süpürme "source silent" derdi), tek audit (`alert_rule.builtin_default_off`), işaret `system_settings.builtin_rules_default_off` (varken bir daha koşmaz → elle açılan kural kapanmaz). Devre dışı kural ölçülmez/değerlendirilmez (`evaluableRules`, `collectMeasureKeys`); "Noisy rules" kapalıyı düşer (`dropDisabledNoisy`) — yeniden önerme |
| `slo:<id>:<sev>` | `slo_burn.go:35-86` | hızlı VE yavaş burn | 1h/6h · 6h/24h | service |
| `db-capacity:<check>` | `db_capacity.go:157-273` | ≥85 warn / ≥90 crit, histerezis 2pp, ETA | 2 sa regresyon | db |
| `db-slow-stmt` | `db_slow_statement.go:25` | DBSlowQueryConfig | — | db |
| `db-health:<system>@<instance>/<db>` (rule id = problem id) | `db_health.go` `evaluateDBHealth` (saf `dbHealthDecide`) | 2 ardışık kova: hata % ≥ 5 (mutlak) YA DA p99 ≥ 2000 ms VE ≥ 3× dünkü aynı kova (GÖRELİ; dünkü kova yoksa p99 kapalı); kova başına ≥ 100 çağrı VE ≥ 2 etkilenen çağıran (≥ 10 çağrılı); 2× → critical (P1), tazelemede şiddet yükselirse yeniden bildirim; kapanış son iki TAMAMLANMIŞ kova temiz (ihlalsiz / çağrı tabanı altı / verisiz) × 2 ardışık okuma; tik başına ≤ 20 açılış | 5 dk kova (açılış: cari kova çağrı tabanını geçtiyse önceki+cari, yoksa son iki tamamlanmış) | db; v0.10.1073, operatör: "Dün akşam CRM database'inde sorun oldu ama problemlerde P1 gelmedi". Okuma `chstore.DBHealthBuckets` ← `db_caller_summary_5m`, tik başına bir sorgu (iç: çağıran başına -MergeState + finalizeAggregation; batch çağıranlar iç WHERE'de düşer; HAVING yalnız ihlal + AÇIK problem satırları) + p99 adayı varken `DBHealthReferenceP99` ← `db_summary_5m` (24 sa önce). Vidalar `db_slow_query.health` (keep-last-good). Özne `DBHealthSubject`: gerçek db.name → `db:<sys>@<db>`, `default` → instance biçimi; `DBProblemSubjectForm` aynı kuralı id'den türetir (FE `dbProblemForm`). Yumuşak-hata: ayar yok / ana ya da referans okuması düştü → açıklar yalnız TAZELENİR; kural kapalı → "rule disabled" kapanışı; kesik okumada kapanış yok. Metrik `db.error_pct` / `db.p99_ms` (kategori ERROR / SLOWDOWN); alarm grafiği yok (`hasAlertMetricChart`), detay pivotları `dbHealthPivots` |
| `runtime:jvm-gc*` | `runtime_vm.go:66` (yalnız VM) | RuntimeAlertConfig | 10 dk | service; heap kuralı EMEKLİ v0.9.551 |
| `exception:shared-dependency` | `shared_exception.go:26-132` | aynı tip ≥3 servis / 5 dk kova; ≥10 critical | 24 sa / 15 dk aktif | id kova taşır |
| `exception:fatal-infrastructure` | `fatal_exception.go:28-74` | 1 oluşum, IsFatalExceptionType | 24 sa / 15 dk | id kova TAŞIMAZ |
| `self-*` (5) | `selfhealth.go:54-69`, `selfhealth_volume.go` | ingest-stall / spool / disk ETA / kanal / hacim ×4 | 10 dk … 24 sa | Service="" (hacim hariç); `self-volume-spike` eskalasyondan muaf ve **batch servislerde açılmaz** (v0.10.1039, `dropBatchVolumeRows` saatlik önbellekten SONRA); `self-disk-eta` **varsayılan KAPALI** (v0.10.1031, `self_health.diskEta`; ölçüm + kalıcı seri sürer) — yeniden önerme |
| `anomaly-auto:<fp>` | `evaluator.go:1296` terfi | PeakRatio + Count + MinSustained (`promotionGate`, saf) — üçü de anomali BÖLÜMÜNÜN değerleri (§5); kapı yalnız AÇILIŞI yönetir, olayı aktif açık satır kapı geçmese de tazelenir (`anomalyPromotionStep`, v0.10.1045) | son 1 sa anomaly_events | mute'lara uyar; rule_id'nin `<fp>`'si = olay kimliği (`PromotedAnomalyEventID`, sorgusuz; problem id `…:<servis>`). Deploy atfı anomaliyle AYNI kural (v0.10.1054, `promoted_anomaly_source.go`): kaynak olay tek toplu okumayla, yalnız aynı bölümdeyse (`StartedAt` eşit) |
| `anomaly:<svc>:<metric>` | `anomaly.go:579-797`, `verdict.go:34` | \|z\| ≥ CriticalZ, DwellBuckets | 5 dk kova; 24 sa ardışık ya da mevsimsel 14 g ±3 | Threshold = baseline medyanı, Comparator yöne göre (v0.9.978); batch serviste `request_rate` faz 1'de atlanır, açık satırı `resolveBatchLoadProblems` dürüst gerekçeyle kapatır (v0.10.1039, `batch_load.go`); batch serviste `p99_ms` her dwell kovasının hızı ≥ 2× p99 tabanının kendi kovalarının (mevsimsel ya da ardışık) hız medyanıyken AÇILMAZ — yalnız açık satır yokken (v0.10.1046, `evaluateAnomaly` + `batch_latency.go`) |
| `anomaly:<svc>:service_silent` | `anomaly.go:876-1023` | 3 sıfır kova + %90 aktif taban | — | **varsayılan KAPALI** (v0.10.543) — yeniden önerme |
| `anomaly-cluster:<source>` | `clustering.go:23-441` | ≥3 yayılım-bağlı taze açılış, katılım ≤30 dk | 60 dk kanıt | üyeler bastırılır |
| `exception-storm` | `exception_storm.go:31-129` | ≥N servis yeni grup / pencere | exception_triage | Service="" — filo düzeyi, tanım gereği P1 |
| `anomaly:<ext>:…`, `anomaly:ext-down:`, `anomaly:ext-cap:` | `external.go` | aynı çekirdek; 3 ardışık poll hatası = down; tik başına açılış tavanı 20 | ≤240 dk kova | external; ext-down/cap **poller sahipli** |
| `monitor:<id>` | `monitor/runner.go:378-461` | prob durumu | — | Value 0 / Threshold 1 → totalLoss P1 |
| `incident:<id>` (notify-only) | `incident_alert.go:35` | created/resolved | — | saklanmaz |

anomaly_events üreticileri (Problem değil): recorder (`recorder.go:107,129`; `log_pattern`
koşulsuz — ES'te sayım token-OR, regex'in ÜST kümesi (`"tns-"` → çıplak `tns` terimi); v0.10.1080
tetiklemek üzere olan ≤10 aday (oran sırası) tek `_msearch` örneklemiyle (`VerifyPatterns`, ≤50
gövde) Go'da regex'e karşı doğrulanır: r=0 bastırır, 0<r<1 cur VE taban ×r, oran
`anomaly_events.verified_ratio`'da (grafik alt başlığı notu); CH no-op. Örneklemi kaldırma: standart
çözümleyicide "tire + rakam" token'la ifade edilemez; `log_template_new` **varsayılan KAPALI** v0.10.1061, `anomaly_sensitivity.logTemplateNew`
*bool nil = kapalı — operatör onaylı: "Bu log anomalileri de false pozitif geliyor"; kapı
`DetectNewLogTemplates` başında + recorder adımı `recordNewLogTemplates`; templater / `log_templates`
defteri sürer, açık olaylar 10 dk sonra düşer — yeniden önerme),
trace_op (`trace_ops.go:171`), trace_op_latency (`op_latency.go`, recorder'dan; batch
yük kapısı v0.10.1046; **varsayılan KAPALI** v0.10.1056, `anomaly_sensitivity.opLatency` *bool
nil = kapalı — operatör: "Trace op latency false pozitif geliyor, gerek yok gelmelerine bence";
kapı dedektörün başında, her G/Ç'den önce + recorder adımı `recordOpLatency`; kapalıyken MV
sorgusu / aktif-olay okuması / upsert yok, açık olaylar 10 dk sonra düşer, terfi Problem'i
"anomaly cleared" — yeniden önerme), davranış motoru (`behavior.go` `evalBehaviorWindow`,
kind=`behavior_change`, LLM dedektör DEĞİL hüküm katmanı — deterministik
kapılardan geçer, alert AÇAMAZ; batch p99 yük kapısı `behaviorFleetCandidates`; v0.10.1070
operatör onaylı "Bu da mesela false pozitif": mutlak taban `behavior.minP99Ms` 200 /
`minErrorRatePct` %1 — son dilim, rejim+mevsimsel, yön fark etmez, request_rate'te yok — ve
sıçramalı geçmiş toleransı `spikyBandFactor` 1.5: yukarı HER dilim kovasının p90'ının 1.5×'ini
aşmalı, p90 aynı `Values`'tan Go'da, SQL aynı; saklanan baseline medyan. Bedel: sıkı kovada
1.3× mevsimsel artık açılmaz — tabanları kaldırmayı önerme).

**Batch yüklemi (v0.10.1039, operatör: "Bazı batch işlerde ani yük artışı
olabilir, onları anomali gibi düşünme"):** `anomaly_sensitivity.batchServicePatterns`
(`*[]string`; nil = `["-batch"]`, `[]` = kapalı) → TEK yüklem
`AnomalySensitivityConfig.IsBatchService` (ASCII büyük-küçük duyarsız alt dizgi;
SQL ikizi `BatchServiceSQL` aynı listeden; Go↔SQL fikstür testleri `clickhouse` ikilisi
ister, CI'da atlanır — CI'yı şekil/golden + özellik tablosu kapsar). Kapsam: davranış
motoru batch'te `request_rate`'i değerlendirmez (`behaviorFleetCandidates`); metrik
dedektörü atlar + açık satırı kapatır; `self-volume-spike` açılmaz; `trace_op` batch'te
`error_spike` için **SAYIM VE PAY** ister (`traceOpBatchShareHolds`, SQL HAVING'de) —
SAF SUSTURMA: batch olayları eski kümenin alt kümesi, batch olmayan çiftler LIMIT/ilk
50'de yalnız yer kazanır; pay kuralını sayımın YERİNE koyma (yeni olay açar).
**Yük altında gecikme (v0.10.1046, operatör: "Batch servislerde yük altındaki gecikme
artışı da anomali sayılmasın"):** hacim işin OLAĞAN ÇALIŞMA hacminin ≥ 2 katıyken gecikme
artışı YENİ olay açmaz. Tek sabit `batchLoadSurgeFactor = 2`; kollar `batchLoadSurge`
(ondalık, medyan) ve `batchLoadSurgeCounts` (tamsayı) — `batch_latency.go`; taban
bilinmiyorsa SUSTURMA YOK. `trace_op_latency`: `cur_calls × base_buckets ≥ 2 × base_calls ×
cur_buckets`, `base_buckets` = taban penceresinin AKTİF kovaları (`uniqExact(time_bucket)`,
aynı geçiş; 24 sa ortalaması seyrek işin HER koşusunu sıçrama yapardı) — SQL HAVING
`opLatencyQuery` + aynı tamsayı Go kemeri `classifyOpLatency`; kapı yoksa SQL golden-birebir.
Metrik `p99_ms`: her dwell kovası hızı ≥ 2× p99 tabanının KENDİ kovalarının hız medyanı —
mevsimselde `buildAllSeasonalQuery`'nin `rate` kolonu (aynı okuma, tek kolon), ardışıkta 24 sa
(`batchLatMetricUnderLoad`, `evaluateAnomaly`). Davranış `p99_ms`: penceredeki her dilim ≥ 2×
kendi HOW kovasının hacim medyanı (yalnız yukarı; rejim yük altındaysa mevsimsel sorulur, yük
dışı dilim varsa REJİM adayı aynen kalır; kapı tavandan önce). **Zaten aktif olana kapı
uygulanmaz:** metrik → `!hasOpen`; `trace_op_latency` → son 15 dk içinde yazılmış olay
çiftleri (`opLatActiveAge` = 10 dk aktiflik + bir 5 dk kova: tek kaçırılmış kova muafiyeti
düşürmesin; `ListActiveAnomalyKeys`, recorder tikinde sorgudan önce tek okuma, HAVING'de tuple
`NOT IN`, tavan 200 çift + 64 KiB metin, tavan ötesi muaf değil); davranış → servisin aktif
(son 10 dk) p99 olayı (TEMBEL okuma, WHERE'de p99 pattern'i, tavan aşımı = okunamadı). Aktif
küme okunamazsa o tik kapı YOK. Kapatma geçişi YOK.
Yükte **SUSMAYANLAR** (yeniden genişletme — aşırı susturma en kötü sonuç): `error_rate`,
olağan çalışma yükündeki `p99_ms` / `trace_op_latency` artışı, zaten aktif her gecikme
olayı/problemi, `new_error` (aşağıdaki uzun taban dalı hariç), `log_pattern`, exception P1 hacmi, gömülü kurallar. Bedeller:
taban payı ≥ %33 batch op `error_spike` açamaz; batch servis span sızıntısında hacim
(maliyet) uyarısı almaz; gecikme kapısında ORANTI YOK (2× yük + 50× gecikme de susar);
susan batch p99 adayı küme en az üye sayısına (3) sayılmaz; `trace_op_latency` tabanı aktif
kova başına ORTALAMA (max/p90 değil — onlarla kayan taban sıçramayı dakikalar içinde yutar):
damla profilinde (çoğu kovada birkaç çağrı + gerçek koşu) aynı yükteki koşu da sıçrama sayılır,
operasyon düzeyi olay hiç açılmaz (servis p99'u görür); rampada (50, 50, 50, 2.000) büyük kova
sıçrama sayılır, küçük kovalar açabilir; 7/24 batch'te 24 sa ortalamasının ≥ 2× günlük tepesi
YENİ olayları susturur. Kalıplar tik başına
`AnomalySensitivityForDetectors()`'tan (atomic, CH okuması yok): ayar bu süreçte hiç
doğrulanmadıysa batch listesi BOŞ (süzgeçsiz) ve kapatma geçişi koşmaz; okuma hatası
son değeri KORUR (`LoadAnomalySensitivity`).

**Seyrek koşan batch işi (v0.10.1043, operatör: "son 24 saatte hiç koşmamış bir iş her
koşuda 'yeni hata' diye açılıyor"):** batch çiftinde 24 sa tabanda hata yoksa trace_op
new_error demeden önce UZUN tabana bakar — aynı MV, [hizalı şimdi − 8 g, taban başı) yarı
açık (`traceOpBatchExtWindow`). Uzun tabanda hata varsa taban payı = ext_errs /
(ext_calls + base_calls) (24 sa'in temiz çağrıları paydada) ve `traceOpBatchExtShare`:
cari pay − taban payı ≥ 25 puan → bugünkü new_error (mutlak artış kaçışı — seyrek işte
`error_rate` emniyeti YOK, ≥15 kova / ≥3 gün taban ister); değilse cari pay ≥ 3× →
`error_spike` (Ratio = pay oranı, BaselineErrors = taban payı × cari çağrı); değilse olay
yok. Uzun tabanda hata yok → new_error AYNEN. Kaçışın kapsamadığı: yüksek tabandan 25
puanın altındaki artış (%40 → %60) susar.

Okuma (`readTraceOpBatchExt`): tik başına ≤1 ek MV sorgusu, yalnız batch new_error adayı
varken; SQL'de batch koşulu + `(service_name, name) IN (tuple(?, ?), …)`, LIMIT 50,
max_execution_time 10; aday tavanı 50 (tavan dışı = bugünkü new_error + tik başına bir
sayı logu). Okuma/tarama hatası → SÜZGEÇ yönü: hiçbir aday susmaz, yarım sonuç
uygulanmaz, tek log. Aylık işler kapsam dışı. Sahte bağlantı seam'i
`detectTraceOps(ctx, traceOpConn, sens, now, window, logf)`. Kalan gürültü (kuyrukta):
koşu ortasında başlayan hatalar sonraki kovalarda v0.10.1039 yolundan sayı oranlı
`error_spike` açar.

## 4. Problem modeli ve öncelik

`chstore.Problem` (`problem.go:129-265`). SAKLANMAYAN alanlar: RunbookURL,
Clusters, OwnerTeam/SRETeam, RecentDeploy, PriorDeploy (kuralın bastırdığı deploy, nötr —
v0.10.1054), EpisodeCount/FirstStartedAt (yalnız terfi
Problem'i, kaynak olaydan — v0.10.1054), PredatesDeploy (yalnız deploy raporu / rollout
çekmecesi), Priority/Reason, Category/DisplayID,
RootCause — okuma zenginleştirmesi `EnrichProblemsForRead`
(`problem_telemetry.go:456`; öncelik EN SON). `AISummary` saklanır.

**Öncelik merdiveni** — tek SAF işlev `computePriority` (`problem.go:368-522`),
ekran ve mail aynı işlevden (`notify/alert_title.go:103`), SQL süzgeci ASLA
(`problem_filter_contract_test.go`):

```
info                                 → P3
RuleID == "exception-storm"          → P1  (tanım gereği, v0.9.1194)
RuleID hasPrefix "incident:"         → critical P1 / P2  (v0.10.748)
ratio = Value/Threshold; yalnız isBelowRule(Comparator) && 0<ratio<1 ise 1/ratio (v0.9.976)
bigBreach   = ratio ≥ BigBreachRatio (varsayılan 2.0; MinBigBreachRatio 1.1)
totalLoss   = Value==0 && Threshold>0 → bigBreach  (v0.9.825)
staleCrit   = critical açık ≥ StaleCriticalHours (4)  → P1 "critical open N h"
critical: bigBreach→P1 | staleCrit→P1 | P2      warning: bigBreach→P2 | P3
```
**v0.10.1069 notu:** kural tarafında "critical × 2" (critical + bigBreach) P1 yolunu çoğunlukla beş yerleşik
critical kural (`builtin-error-rate-15pct`, `-error-majority-50pct`, `-http-p99-5s`, `-db-p99-5s`, `-mq-consume-p99-2m`)
besliyordu; yerleşikler varsayılan kapalı olduğundan bu yol artık NADİREN tetiklenir — P1 çoğunlukla operatör kuralı,
SLO burn, staleCrit (eskalasyonla critical'a çıkan), exception-storm, incident ve monitor totalLoss'tan gelir.
"P1 neden az" sorusunda önce bunu düşün; yerleşikleri varsayılan açmak çözüm DEĞİL.
`fresh deploy` tetikleyicisi v0.9.612'de KALDIRILDI — deploy bilgisi görünür,
sıralamaya karışmaz. Vidalar `problem_priority` blobu (`problem_priority.go:27-116`);
sıfır struct = varsayılan, `StaleCriticalHours=0` anlamlı. Kapatma yolu
`MarkResolved` (:549) `Value`'yu EZMEZ (v0.9.977: açık/kapalı öncelik simetrisi).

**Inbox görünüm önceliği** (`forceNonExceptionP3`, v0.9.487; v0.10.1072 kısmi revizyon):
exception/httperror dışı satırlar inbox'ta P3 — YALNIZ dar istisna listesine
(`problem_priority.inboxKeepSourcePriority`, glob `*`, tam eşleşme; kimlik Problem
RuleID / incident `incident:<severity>`) uyan satır kaynak P1/P2'sini korur, gerekçe
"kaynak önceliği korundu (…)". Varsayılan `anomaly:*:error_rate`, `builtin-*`,
`db-health:*`, `incident:critical`; nil = varsayılan, `[]` = saf v0.9.487; geniş kalıp
400 (literal önek + ≥3 literal; `*:*` red); PUT kayıtlı değerin üstüne çözülür (alan
yoksa liste korunur). Regressed P1 yükseltmesi regresyon başına bir kez bildirilir (v0.10.1078: `<fp>:regressed:p1:<epoch>`, `claimRegressed`); taban `<fp>:regressed` 90 günde bir, damgasız (kronik grup gürültüsü). Bilinçli DIŞARIDA: trace_op(_latency) / log_* / behavior_change /
`anomaly-auto:*` / `slo:*` / yavaş ifade / self-health — listeye ekleme önerme.
Facet sayaçlarından ÖNCE (`inbox_keep_priority_test.go` sırayı pinler). Kod
`chstore/problem_priority_inbox.go` + `api/inbox_keep_priority.go`.

**Exception merdiveni** ayrı SAF işlev `exceptionPriorityAt` (`inbox.go:1832`):
patlama (BurstMinTotal + BurstMinRate) → **P1 yapışkan** (v0.9.1205); `regressed`
→ yeniden açıldıktan sonra (`Occurrences − OccurrencesAtResolve`) ≥ P1MinOccurrences
ise P1 "yeniden açıldıktan sonra ≥N oluşum", değilse P2 (v0.10.1072; anlık görüntü
resolve anında `markExceptionResolved` ile `exception_groups.occurrences_at_resolve`'a,
iki-boot probe `hasExResolveSnapCol`; anlık görüntüsüz grup P2 — kapıyı ömür toplamıyla
regressed'in üstüne taşıma, sel olur); `Occurrences ≥ P1MinOccurrences` → **P1 yapışkan** (v0.10.741, kronik
damlama istisnası kalktı); taze && ≥100 → P2; oran ≥ BurstMinRate/2 → P3
gerekçeli; değilse P3. **Operatör direktifleri (yeniden önerme):** P1 zamanla
P2/P3'e İNMEZ; "kuyruk şişer" cevabı `p1MinOccurrences` vidasıdır, zaman
değil. Occurrence tabanı 5 (v0.10.949, operatör 2026-09-26; önce 2 v0.10.740):
5'in altı TEK serviste gizli; aynı exception (tür + normalize mesaj) aynı anda
(±`stormWindowMinutes`) ≥2 serviste görülüyorsa görünür (`exception_spread.go`,
satırda "N servis" işareti). Regressed (P2 "regressed", `state='regressed'`)
gruplar da muaf — 5'in altında ve tek serviste de olsa görünür (operatör
2026-09-26; `exceptionIsRegressed` tek kaynak, SQL `FloorExemptRegressed`).
Etkin taban min(5, `p1MinOccurrences`) — P1
gizlenmez. İstisna yalnız varsayılan kipte (param yok; /problems `floor=default`);
açık ?minOcc=N ve show all = minOcc=0 istisnasız. Rozet aynı kümeyi sayar.
Exceptions varsayılan sırası ÖNCELİK (v0.10.703).
Inbox = ignored hariç TÜM durumlar, durum rozetiyle (v0.10.751); yalnız Ignored
ayrı sekme; `open` kovası /inbox rozet sayacı için aynen.

Kategori `problem_category.go:38-85` (önek → AVAILABILITY/ERROR/SLOWDOWN/
RESOURCE/CUSTOM); `DisplayID` `P-<base36>` (:92).

## 5. Yaşam döngüsü — kural dosyasında görünmeyen mekanizmalar

| Mekanizma | Yer | Kural |
|---|---|---|
| Anlık görüntü | `OpenProblemsSnapshot` `problem.go:1664` (5 s memo, `rule\|service` anahtarı) | her arka plan işi BUNU okur — `problem_snapshot_contract_test.go:39`, `evaluator/snapshot_batch_test.go:22` |
| Bayat süpürme | `evaluator.go:1105-1153` | cutoff 3×interval; yalnız `sweepIsTrustworthy` (`tick_continuity.go:95`: evaluator sessizliği KENDİSİ gözledi; rollout kör noktası sahte resolve→reopen üretiyordu v0.9.588) |
| Yaş eskalasyonu | `evaluator.go:1203-1251` | 15/30 dk; `effectiveSeverity` kelepçesi (:1553); terfi edilen şiddet eskalasyon tabanına kelepçeli (v0.8.309) |
| Incident kaskadı | `evaluator.go:640-724` | bağlı problemlerin hepsi kapanınca, `maxResolvedAt`; yetim incident 1 sa sonra (v0.9.332) |
| Sustu = düzeldi DEĞİL | `anomaly.go:839-852,885-900` | sıfır-dolgulu kuyrukta resolve gerekçesi "source silent"; tabandan sondaki sıfırlar kırpılır (v0.9.1051/1052) |
| Anomali bölümü (episode) | `anomaly_event.go` `MergeAnomalyCarry` / `anomalyNewEpisode`; sabit `anomalyEpisodeGap` = 2 × `anomalyActiveAge` + 150 sn = 22 dk 30 sn | v0.10.1045, operatör: "Eski yüksek oran taşınmasın … Yeni tetiklenme sıfırdan başlasın." Gelen last_seen − saklı last_seen > 22 dk 30 sn (OLAY saati; tam sınır = aynı bölüm) → YENİ bölüm: started_at + peak yalnız gelen olaydan. Sınır aktif yaşa (10 dk, status) EŞİTLENMEZ: 5 dk kovalı yazıcı tek kova kaçırınca ~10 dk boşluk üretir ve bölüm yazı-turayla sıfırlanırdı; 1–3 kaçırılmış kova aynı bölüm, her sıfırlamadan önce satır ≥ 12 dk 30 sn "cleared" → terfi Problem'i resolve geçişinde zaten kapanmış. Bölüm içinde started_at korunur, peak yalnız yükselir, last_seen GERİ GİTMEZ (sırası bozuk / 0'lı yazım sahte bölüm açmasın); taşıma okuması aynı id'ye iki sürüm döndürürse last_seen'i büyük olan (`foldAnomalyCarryRow`). Sonuç: yeniden tetiklenmede terfi 300 sn'yi YENİDEN bekler, /inbox önceliği yeni tepeden, `/anomalies` satırı SON bölümü gösterir (ilk-ever başlangıç + tüm zamanların tepesi artık yok), TTL son bölümden. Kısıt: yazım aralığı > 22 dk 30 sn olan yazıcı her yazımda yeni bölüm açar (kayıtçı olayları hiç terfi etmez) — koşulsuz taşımaya DÖNME, sınırı aktif yaşa ÇEKME. **Bölüm sayacı (v0.10.1049,** operatör: "Yinelenen anomali ayrımı … her seferinde 'yeni' görünüyor ve önceki deploy'a bağlanıyor"**):** `episode_count` (DEFAULT 1) + `first_started_at` (DEFAULT 0 = bilinmiyor → started_at) — ilk görülme 1/started; aynı bölüm ikisi değişmeden; yeni bölüm sayaç+1, ilk = saklı ilk (0 ise saklı started_at); sırası bozuk yazım artırmaz. "Yinelenen" = satırın 30 günlük TTL ömrü içinde. Deploy atfının TEK kuralı `chstore.AnomalyPredatesDeploy(first, started, count, deploy)` — "DÜZENLİ yinelenme": earliest ≥ deploy → false; started < deploy → true; aksi yalnız count ≥ 3 VE ortalama aralık ≤ 48 sa → true (gece işi 3. gecesinden). "Herhangi bir eski bölüm"e GENİŞLETME: 30 günlük satır ömründe sık görülen her operasyonun ilk görülmesi her deploy'dan önce düşer ve gerçek gerileme gizlenir (vaka A: kır → rollback → bozuk yeniden deploy = 2. bölüm; vaka B: 20 gün önce tek kıpırtı — ikisi de atfı KORUR). Tüketiciler GİZLEMEZ: deploy raporu/rollout `anomaliesSinceDeploy` satırı tutar, `predatesDeploy` ile "yinelenen" işaretler; `synthInputForAnomaly` deploy'u `DeployRecurring` ile işaretler, `Synthesize` adayı 0.10'a İNDİRİR (düşürmez; breadth'e sayılmaz, RecentDeploy boş) ve ölçülen etki gerileme gösterirse (p99 ≥ +%20 / hata ≥ +1 puan) normal puan geri gelir; çip (`pickAnomalyDeploy`) yalnız kural true iken iliştirilmez. Probe `hasAnomalyEpisodeCols` `atomic.Bool`; ertelenen DDL sonrası `reprobePromotedAttrs` → `reprobeAnomalyEpisodeCols` çevirir (probe false pod'un her yazımı sayacı sıfırlar — bayrağı süreç ömrü boyunca DONDURMA). **Terfi Problem'i (v0.10.1054,** operatör: "Anomaliden terfi eden problem de 'yinelenen' kuralına uysun"**):** aynı kural, aynı üç tüketici — `EnrichProblemsWithDeploys` kaynak olayları TEK toplu okumayla alır (`PromotedAnomalySources`; terfi satırı / kolon yoksa okuma yok), aynı bölümdeki Problem'e sayaç (> 1) + ilk görülme iliştirir (`attachPromotedEpisodes`), seçim `pickProblemDeploy` → `pickAnomalyDeploy` (çip, DeployBox, /rootcause, Insight, istemler); işçinin problem çıpası tik başına tek okuma + ortak `deployRecurrence`; rapor/çekmece `problemsSinceDeploy` işaretler, gizlemez; ProblemExplainer tik başına tek okumayla DEPLOY satırını nötr yazar (`DeployPredates`). Satır `RecurringMarker`, detay `problemWhenLine`. **Hiçbir şey kaybolmaz:** kuralın bastırdığı deploy aynı seçiciden nötr `PriorDeploy`'a (problem + anomali; ipucu / ne zaman eki / nötr zaman çizelgesi satırı), `RecentDeploy` = olası neden anlamını korur; ölçülen gerilemede `RestoreMeasuredDeploy` (Enrich*WithRootCause + iki /rootcause ucu; yalnız PriorDeploy doluyken) onu yeniden RecentDeploy yapar. Warning terfi Problem'inde hipotez yok → yalnız nötr metin. Okunamazsa / olay düştüyse / başka bölümse bugünkü atıf. Kurala bakmayan kalanlar: derin kanıt kapısı, imajı eşleşmeyen rollout adayı; explain/runbook istemi + MCP haritası "Recent deploy" satırını yinelenme satırı olmadan kaybeder (kuyrukta) |
| Yumuşak-hata yönü | `evaluator.go:1341,1544`; `selfhealth.go:277`; `rca_auto_verdict.go:101`; `trace_ops.go` `readTraceOpBatchExt`; `anomaly_event.go` `UpsertAnomalyEvents` | mute listesi okunamadı → süzgeçsiz terfi; aktif set okunamadı → hiçbir şeyi kapatma; ölçülmemiş ≠ temiz; dedup okunamadı → üret; batch uzun tabanı okunamadı → new_error aynen (v0.10.1043); anomaly_events taşıma okuması okunamadı → o tik YAZMA (v0.10.1045; "ilk görülme" yazımı çağrıdaki her bölümü sıfırlardı); terfi Problem'inin kaynak olay okuması okunamadı → bugünkü deploy atfı, işaret yok (v0.10.1054). Her kapı için yön BİLİNÇLİ seçilir ve yorumda yazar |
| Sürdürme damgaları | `stamps.go` | ForSec/Cooldown Redis'e aynalanır; failover sürdürme saatini sıfırlamaz (v0.8.354) |

**Kayan pencere kuralı (feedback, üç kez ısırdı):** olay = yalnız GÖZLENMİŞ
geçiş; pencere kenarına dayanan yokluk kanıt değildir; varlık ≠ etkinlik;
"bilinen" geçmiş ayrı ufuktan gelir; tek kovadan değil yürüyüşle karar;
veri başlangıcı ve kesik okuma girdiye taşınır. Kümeleme katılımı gözlenmiş
`StartedAt`'e bağlı, pencere kenarına değil (`clustering.go:21-50`). İnceleme
şekli: kayan-pencere simülasyonu + iki-yazıcı determinizmi — tek çağrılık test
bu sınıfı GÖREMEZ. Emsal: `internal/rollout/reconcile.go`.

## 6. Korelasyon ve kök neden

| Katman | Yer | Sözleşme |
|---|---|---|
| Komşu grafı | `correlator/correlator.go:169-222` ← `topology_edges_5m` (`service_adjacency.go:167`) | 5 dk önbellek; api/ingest rolleri kurar ama Start etmez |
| Yayılım sırası | `propagation.go:68 RootCauseRank`, `:200 RankNodeCauses` | aynı-node yerleşimi v0.10.94 |
| Zamansal katsayı | `temporal.go:54` Spearman | **gölge kip** (`anomaly_sensitivity.temporalRanking`, v0.10.700): katsayı kaydedilir, sıra DEĞİŞMEZ |
| Hipotez birleştirici | `hypothesis.go:284 Synthesize` (SAF) | deploy 0.80 (+0.15 tazelik; düzenli yinelenen anomalide 0.10 — `DeployRecurring`, ölçülen gerilemede geri, v0.10.1049; terfi Problem'i çıpasında da, kaynak olayın sayacıyla — `deployRecurrence` iki çıpanın tek yardımcısı, v0.10.1054) · yayılım ×0.70 · aynı-servis sinyal [0.30,0.60] · komşu [0.28,0.52] (upstream ×0.6) · eş-ateşleme 0.20 · rollout +0.05 doğrulama; güven = 0.5·genişlik + 0.5·güç (:112-125) |
| Sentezleyici | `rootcause_worker.go:188` | girdiler tek toplama (`fusion.go:98`, 60 dk); çıpa 1 anomaliler (30 dk), çıpa 2 critical açık problemler; **derin kanıt kapısı** `shouldDeepInvestigate(isP1, hasDeploy)` (:20, v0.9.1060); derin kanıtı YALNIZ sentezleyici yazar (tek yazıcı) |
| Hakem (LLM) | `api/rca_verdict.go:144` prompt, `rca_shields.go` (kanıt id süzgeci, çürütme geçerliliği, güven tavanı, varlık kontrolü) | LLM sıra DEĞİŞTİREMEZ, güveni yalnız aşağı çekebilir; oto-hüküm `rca_auto_verdict.go:82` (AutoExplain açık + kota + 30 dk dedup) |
| Deploy korelasyonu | `correlate.go:56,205`, `rollout_problem*.go` | RCA'dan ayrı yüzey (`/api/correlate/context`) |
| "Ne başka değişti" (WhatChanged) | `chstore/correlate.go` `scoreChangedService` (SAF) ← `GetCorrelatedChangesMV` (`service_summary_5m`); `correlate_cause.go` `MarkCorrelationCauses`; manşet `RootCausePanel.tsx` `likelyCause` ← `lib/rootCauseCandidates.ts` `coMovingCause` | skor = \|Δhata puan\|·4 + min(200,\|Δrate%\|)·0.5 + min(200,\|Δp99%\|)·0.5 — **YÖNSÜZ büyüklük**, yalnız sıralar. `direction` (`changeDirection`): worse (hata ≥ +1 puan, p99 > +%25 hacim kapısız, rate > +%25 hacim kapılı) · unknown (NaN/Inf, asla better) · lost (rate ≤ −%90) · better (hata ya da p99 GERÇEKTEN düştü) · quieter (yalnız rate −%25…−%90). "Co-moving … propagation" manşeti YALNIZ `causeEligible` VE skor ≥ 20; uygunluk = özneyle taban+cari pencerede DOĞRUDAN `topology_edges_5m` kenarı (`GetServiceGraphTopN` özne odaklı, tavan 500; servissiz problemde okuma yok; okunamazsa kimse uygun değil, `topologyKnown=false` → "bağlantı doğrulanamadı") VE konuma göre yön: downstream/both → worse \|\| lost; upstream → YALNIZ trafik sıçraması (çağıranın yavaşlaması etki alanı). Skor değişmez; /rootcause 50'lik havuzu (`GetCorrelatedChangesMVTop`) işaretleyip 20'ye SONRA keser; sıra uygun → diğer → better/quieter ("iyileşti"/"trafik azaldı"). Ribbon canlı adayları uygun-önce, better/quieter hariç. v0.10.1063, operatör: "<svc-B> ile ilgili olduğunu düşünüyor ama alakasız" — iyileşen, bağlantısız servis skor 404'le manşetteydi. Yönsüz skoru manşet için YENİDEN kullanma; hakem kataloğu (`rca/extras.go` "kötüleşen komşu") henüz yön/kenar süzmüyor |

Ürün tezi (operatör): fark metrik formülü değil, otomatik korelasyon + "problem
→ olası neden" akışı; parçalar (correlator, bubbleup, WhatChanged, blast
radius, deploy işaretleri, baseline) ayrı yaşar, birleştirme yüzeyi RootCause
paneli. Yeni skor = `hypothesis_*_test.go`'ya tablo satırı, kalibrasyon
`rca_calibration_test.go`.

## 7. Exceptions

`exception_inbox.go`: durumlar :28-34; `FingerprintException` :80 (ilk 5 çerçeve
yoksa normalize mesaj; servis DAİMA hash'te); `RefreshExceptionGroups` :1220
(tavan 20000; checkpoint `until` başarıda, hatada 5 dk geri — v0.9.769);
`shouldRegress` :751 (5 dk ödemesiz); bayat oto-çözüm 24 sa :745-824. Fırtına
adayları `exception_storm.go:31`; paylaşılan patlama `shared_exception.go:69`;
ölümcül `fatal_exception.go:82`. Liste ucu ÖNCELİĞİ EKLEMEK ZORUNDA — v0.10.364
Exceptions sayfası P1 çizmiyordu (`docs/INCIDENTS.md:285`). Ayar `exception_triage`
(`chstore/exception_triage.go:22-132`), 30 s atomic (`api/exception_triage.go:41`).

## 8. Self-health (kendi kendini izleme)

`self-ingest-stall` / `self-spool-depth` / `self-disk-eta` / `self-channel-broken`
/ `self-volume-spike` (`selfhealth.go:54-69`). Kapı: ölçülen ↔ kapsanan ayrımı
(`covered` haritası :278-281; ölçülmemiş = temiz DEĞİL, v0.9.984); kapatınca
açık satırlar boşaltılır (:273). Ayar `self_health` (pointer Enabled; disk ETA
ayrıca `diskEta` *bool, nil = KAPALI — v0.10.1031 operatör "gerek yok": kapalıyken
ölçüm/kalıcı seri sürer, problem üretilmez; `open` satırlar bir sonraki tikte
çözülür, `acknowledged` satırları reconcile kapatmaz — bayat süpürme ~3 aralık
sonra "source silent" ekiyle kapatır). Runbook
haritası `problem.go:708`. `selfobs` paketi BAŞKA şey: Coremetry'nin kendi OTel
yayını. Yeni "sinyal kaybı = critical" dedektörü EKLENMEZ (service silent
kararı, v0.10.543).

## 9. Bildirim arayüzü (yalnız sınır)

`notify.SendProblemAlert` (`notify.go:432-609`) hunisi: runbook → **öncelik bir
kez** `withPriority` (v0.9.828) → SSE → bakım penceresi → acknowledged kısa
devre → **ignore bağlantısı** (`NotificationIgnored`, v0.10.749) → ekip maili →
`ProblemNotifyKind` (`notify_kind.go:55`: `incident:`→incident; `anomaly:`,
`anomaly-cluster:`, `exception-storm`, `exception:`→anomaly; kalan→problem) →
kanal eşleşmesi `{Priority, Kind}` → dedup (`problem_dedup.go`: 15 dk birebir, 1 sa
aynı-durum, yalnız gerçek şiddet artışı geçer) → yönlendirme kaydı (eşleşmeyen
de notification_log'a, v0.9.1344). Her üretici paketi kendi önekini pinler
(`anomaly/notify_kind_pin_test.go`, `evaluator/notify_kind_pin_test.go`). Kanal
şablonu/ayrıntısı bu skill'in DIŞI.

## 10. AI açıklama (problem/exception tarafı)

| Yüzey | Prompt kurucu | Sistem promptu |
|---|---|---|
| Problem explain (tık + arka plan) | `anomaly.buildProblemPrompt` `problem_explainer.go:240-279` (kural → `HypothesisPromptBlockTR` → `renderEvidence` → `renderDeepEvidence` + uydurma-yasağı) | `SystemPromptProblem` `copilot/prompts.go:152` |
| Exception explain | `BuildExceptionExplainInput` `exception_context.go:246` → `assembleExceptionPrompt` :538 (damga operatör diliminde, v0.10.745) | `SystemPromptException` :225 |
| RCA düzyazı / hüküm | `rootcause.go:411,476`; `rca_verdict.go:144` | `SystemPromptRCAVerdict` :841 |
| Insight kartı | `api/insight.go:78` — deterministik yarı AI kapalıyken de çalışır | — |

Tüm sistem promptları TEK dosyada (`internal/copilot/prompts.go`); açıklama
affordance'ı `s.copilotExplain(r, …)` üzerinden, `s.copilot.Explain` DOĞRUDAN
değil. Golden pinler: `anomaly/prompt_golden_test.go`, `rootcause_prompt_test.go`,
`copilot/prompt_problem_test.go`, `autoexplain_gate_test.go`.

## 11. Ayar deseni (yedi anahtar)

`anomaly_tracked` · `anomaly_sensitivity` · `anomaly_promotion` · `problem_priority`
· `problem_escalation` · `exception_triage` · `self_health` — hepsi
`system_settings` JSON blobu: `Default*` / `Normalize*` / `Get*` (hataya
varsayılana düşer) + boot `Load*` + 30 s `Start*Refresh` + sıcak yol için paket
düzeyi atomic. Varsayılanı TRUE olan her bayrak `*bool` (AttachToIncident,
Behavior.Enabled, SelfHealth.Enabled) — `false` ile "yok" ayrılsın. Varsayılanı DOLU
liste `*[]string` (`batchServicePatterns`, v0.10.1039): nil = varsayılan, `[]` = kapalı;
Normalize boş listeyi `[]` (nil olmayan dilim) yazar — `null` geri okununca nil olur ve
kural sessizce geri açılırdı. Normalize'a kopyalanmayan alan PUT'ta düşer. Varsayılanı
TEK YÖNLÜ eylem süren bir ayar (kapatma) okuma hatasında varsayılan YAYINLAMAMALI:
`anomaly_sensitivity` son değeri korur + "doğrulandı" bayrağı taşır (v0.10.1039);
`problem_priority` da okuma hatasında son değeri korur (`LoadProblemPriorityWith`,
v0.10.1072 — inbox istisna listesi taşıdığı için).

## 12. Sessiz bozulmalar — sürüm etiketli

| Sürüm | Tuzak | Çapa |
|---|---|---|
| v0.5.352 / v0.9.588 | Süpürme evaluator'ın gözlemediği sessizlikte koşarsa sahte resolve→reopen çiftleri, StartedAt/eskalasyon sıfırlanır | `evaluator.go:590-601`; `tick_continuity.go` |
| v0.9.976 | Oran çevirimi yalnız `<`/`<=` için; boş karşılaştırıcı ASLA çevrilmez (5717 P1'in 299'u sahteydi) | `problem.go:415-436` |
| v0.9.825 | Tam kayıp (Value 0, Threshold>0) bigBreach; monitor DOWN P2'de takılıyordu | `problem.go:442-467` |
| v0.9.977 | Kapatma yolu Value'yu ezmez | `problem.go:524-555` |
| v0.9.978 | Anomali satırında Threshold=medyan, Comparator yöne göre — trafik düşüşü P1 kalır | `anomaly.go:768-797` |
| v0.9.827 | `attachToIncident` kapısı ile "güçlü anomaliyi terfi et" kutusu FARKLI hatları yönetir | `anomaly_sensitivity.go:58-73` |
| v0.8.250 / v0.9.957 | Örnek kıtlığı: mevsimsel taban 14 g ±3 kova, cmt/paz ayrı; davranış motoru kova başına ≥3 FARKLI gün ister (sayı yeterken gün çeşitliliği kapısı şart) | `anomaly.go:59-74`; `behavior_scarcity_test.go` |
| v0.8.507 / v0.9.691 / v0.10.156 | Tik başına toplu MV okuması + tek anlık görüntü; (servis,metrik) başına nokta sorgusu YOK | `anomaly.go:518-563`; `open_snapshot_test.go` |
| v0.9.1069 / v0.10.699 / v0.10.199 | İki faz karar→uygula; küme ≥3 üye; katılım gözlenmiş StartedAt'e bağlı | `clustering.go:21-50` |
| v0.9.337 / v0.9.444 | Terfi mute'lara uyar; tazeleme Status/Assignee/Pod/AISummary taşır | `evaluator.go:1320-1343,1428` |
| v0.9.587 / v0.9.825 | İki katmanlı bildirim dedup'ı; şiddet salınımı fırtına yapamaz; harita süreç-yerel (restart bir kopyaya izin verir) | `problem_dedup.go` |
| v0.9.516 / v0.9.1060 / v0.9.1281 | Derin kanıt tek yazıcı; derin kapı = P1 VEYA deploy; oto-hüküm hipotez yazıldıktan SONRA, 30 dk dedup zamana göre | `rootcause_worker.go:263-324` |
| v0.9.1304 / v0.9.1335 | problems / anomaly_events / root_cause_hypotheses PARTITION BY'sız (Kural P1) | `store.go` ilgili DDL yorumları |
| v0.9.627 → v0.10.741 | Exception P1 merdiveni tarihçesi: uçurum → basamak → 4 sa → yapılandırılabilir patlama → yapışkan P1 → hacim yapışkan | `inbox.go:1848-1985` |
| v0.10.364 | Liste ucu öncelik eklemezse sayfa sessizce boş çizer | `exception_inbox.go:52-58` |
| v0.9.1279 / v0.9.984 / v0.9.1294 | Self-health ölçülen-kapsanan ayrımı; hacim sıçraması eskalasyondan muaf | `selfhealth.go:19-33,277-280` |
| v0.10.587 / 588 / 597 | Dış seri: tik başına açılış tavanı (20) + özet problem; 3 hata = down; yalnız başarılı poll sonrası tarama (sıfır dolgulu sahte düzelme yok) | `external.go:3-24,132-151,489-511` |
| v0.9.572 / v0.9.609 | Paylaşılan patlama id'si zaman kovası taşır, ölümcül taşımaz | `shared_exception.go:52`; `fatal_exception.go:43` |
| v0.10.700 | Zamansal sıralama gölge kipte | `anomaly_sensitivity.go:83-88` |
| v0.10.1045 | anomaly_events tepe + ilk started_at'i parmak izi başına 30 gün taşıyordu: günler sonra yeniden tetiklenen olay eski tepeyle P1, terfi kapısını ilk tikte geçip critical açılıyordu. Artık 22 dk 30 sn'yi aşan boşluk yeni bölüm (aktif yaş DEĞİL — tek kaçırılmış 5 dk kova); kapı yalnız açılışı yönetir, açık satır kapı geçmese de tazelenir | `anomaly_event.go` `MergeAnomalyCarry`; `evaluator.go` `anomalyPromotionStep`; pinler `chstore/anomaly_event_test.go`, `evaluator/anomaly_episode_test.go`, `api/inbox_anomaly_episode_test.go` |
| v0.10.1049 | 1045'in bedeli: yeniden tetiklenen (gece işi) her bölüm "yeni" görünüp bir önceki deploy'a yazılıyordu (deploy raporu, rollout, kök-neden deploy adayı, çip). `episode_count` + `first_started_at` taşınır; deploy atfı "düzenli yinelenme"ye bakar (`AnomalyPredatesDeploy`: ≥ 3 bölüm, ortalama ≤ 48 sa) ve hiçbir yüzey gizlemez — rapor işaretler, kök neden İNDİRİR (gerilemede geri alır), çip yalnız o zaman düşer. Probe false (yeniden probe'a dek) / eski binary yazımı sayacı 1'e indirir (güvenli yön) | `anomaly_event.go` `MergeAnomalyCarry`/`AnomalyPredatesDeploy`; `correlator/hypothesis.go` `recurringDeployScore`; pinler `chstore/anomaly_episode_test.go`, `chstore/chsmoke_test.go`, `anomaly/rootcause_recurring_deploy_test.go`, `correlator/hypothesis_recurring_deploy_test.go`, `api/anomaly_since_deploy_test.go` |
| v0.10.1054 | 1049'un bilinen sınırı: anomali satırı "yinelenen" der ve çip göstermezken aynı olaydan terfi eden `anomaly-auto:` Problem'i deploy'u çipte, detay kutusunda, kök-neden adayında ve deploy raporunda "olası neden" gösteriyordu. Artık aynı kural: kaynak olay (rule id → olay kimliği, sorgusuz) çağrı başına TEK okumayla (5 kolon, 2 sn); yalnız aynı bölümde; okunamazsa bugünkü atıf. Bastırılan deploy ATILMAZ (nötr `PriorDeploy`), ölçülen gerilemede geri gelir (`RestoreMeasuredDeploy`). Diğer her Problem'in seçimi bayt bayt aynı | `chstore/promoted_anomaly_source.go`; `problem_telemetry.go` `pickAnomalyDeploy`/`pickProblemDeploy`; `rootcause_hypothesis.go` `RestoreMeasuredDeploy`; `anomaly/rootcause_worker.go` `deployRecurrence`/`promotedDeployRecurrence`; `anomaly/problem_explainer.go` `problemExplainerDeployPredates`; `api/deployment_report.go` `problemsSinceDeploy`; `api/rootcause.go`; pinler `chstore/promoted_anomaly_source_test.go`, `chstore/promoted_prior_deploy_test.go`, `anomaly/rootcause_promoted_recurring_test.go`, `api/problems_since_deploy_test.go`, `api/rootcause_measured_deploy_test.go` |

## 13. Değişiklik kontrol listesi

1. Kural id öneki yeni mi? → §2 tüketici listesi + pin testi.
2. Problem yazıyor mu? → §2 dört soru (süpürme, eskalasyon, incident, tam satır).
3. Öncelik/merdivene dokunuyor mu? → SAF işlevde kal, gerekçe cümlesi yalan
   söylemesin, `problem_priority_*`/`exception_triage_test` tablosuna satır;
   operatör direktiflerini (P1 yapışkan, taban 2, Inbox tüm durumlar, service
   silent kapalı, disk ETA kapalı, operasyon gecikmesi (`trace_op_latency`) kapalı,
   yeni log deseni (`log_template_new`) kapalı, yerleşik alarm kuralları (`builtin-*`) kapalı,
   fresh deploy yok) YENİDEN AÇMA.
4. Arka plan döngüsü mü? → lider kilidi + tik bütçesi + `OpenProblemsSnapshot`;
   ölçüm seyrekse §5 tasarımı; kayan pencere simülasyon testi.
5. Kanıt/skor mu? → `hypothesis_*_test.go` tablo, kalibrasyon, tek yazıcı.
6. Ayar mı? → §11 deseni; `*bool` kuralı; boot + 30 s tazeleme + atomic.
7. Bildirime çıkıyor mu? → `ProblemNotifyKind` pini; dedup katmanları; `withPriority` bir kez.
8. LLM'e gidiyor mu? → prompt kurucu golden testi; damga operatör diliminde
   (`explainOptions.location()`); uydurma-yasağı satırı; kalkan (`rca_shields`).
9. Doğrulama: `go test ./internal/{anomaly,evaluator,correlator,chstore,api,notify}/…`
   + `/tdd` saf çekirdek + `/release`.

## 14. Sayım tabanları

- 20 saklanan kural-id ailesi (v0.10.1073 `db-health:` dahil) + 2 notify-only = `internal/{anomaly,evaluator,monitor}` test-dışı dosyalarda `RuleID:` / `ruleID :=`.
- 13 arka plan döngüsü = AIOps paketleri + `main.go`'da `NewLeaderHolder` / `time.NewTicker` sahipleri.
- 7 ayar anahtarı = `internal/chstore/{anomaly_tracked,anomaly_sensitivity,anomaly_promotion,problem_priority,problem_escalation,exception_triage,selfhealth}.go` içindeki `const *Key`.
- 12 state tablosu = `store.go`'da problems|anomaly_*|root_cause_hypotheses|rca_verdicts|incident*|exception_groups|events|notification_log CREATE'leri.
- Kaynak: `docs/DECISIONS.md:385-436` (Anomali/Problems kararları), `docs/INCIDENTS.md:285-299`, planlar `docs/plans/spec-anomaly-clustering.md`, `anomaly-rootcause.md`.
