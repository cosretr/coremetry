---
name: aiops
description: 'AIOps map: detectors, evaluator, Problem priority/lifecycle, incidents, RCA, exceptions. WHEN: adding/changing a detector, rule id, priority/escalation or a loop writing problems. WHEN NOT: notify templates, CH schema, copilot UI, MCP tools.'
---

# /aiops — dedektörden bildirime, tek harita

**Katalog: `reference/detectors.md` (gerekince oku)** — §3 dedektör tablosu, §9 bildirim arayüzü, §12 sessiz bozulmalar orada; bu dosya karar ağaçları + kurallar.

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
| Oracle exception tazeleyicisi (v0.10.1092) | `oracle/exgroups.go` ← oracle poller kancası (oracle-poller lideri) | başarılı poll başına | exception_groups (`ora:`) |
| ProblemExplainer / ExceptionExplainer | `problem_explainer.go:43`, `exception_explainer.go:35` | 30 s / 60 s | ai_summary — v0.10.1100: `ora:` grubu Oracle bağlamı + `SystemPromptOracleException`; aday yaşı kapanmış dakika gecikmesi kadar geriden (`isExceptionExplainCandidateAt`, `GroupStatsCache` gecikmesi), kaynaksız Oracle grubu aday değil |
| RootCauseSynthesizer | `rootcause_worker.go:84` — `api.NewServer` SONRASI başlar (`main.go:1276`) | 30 s | root_cause_hypotheses |
| ExternalScanner (Oracle serileri) | `external.go:117` ← poller kancası | 30 s | problems (kind=external) |
| Rollout reconciler | `main.go:1124` | ayar | rollout olayları → RCA girdisi |
| Synthetic monitor | `monitor/runner.go:32` | tik | problems (monitor:*) |
| AI SRE üreticisi (v0.10.1282) | `sre/enqueuer.go` `coremetry:lock:sre-enqueue` (worker; tüketici `sre` rolü/all, iş başına kilit) | 30 s | YALNIZ saved_views(page='sre-investigation') — problems'a YAZMAZ |
| AI SRE drain + orkestratör (v0.10.1282 / v0.10.1287) | `sre/drain.go` iş başına `coremetry:lock:sre-item:<id>` (yalnız `sre` rolü / all; lider kilidi YOK — iş kilidi) → `sre/orchestrator.go` plan (`plan.go` planSteps/pickExemplar) · dalga 1 tarama+k8s / deploy / bağımlılık · dalga 2 pivot / log artışı · TEK anlatım (`narrate.go`, AutoExplain + kota + `sre_agent.maxLLMCallsPerHour` kapıları) · v0.10.1288 Faz 2: dalga 2 `events` (alarmlar + aşağı akış db:/ext: problemleri + incident bağı) · dalga 3 TEMBEL `node` (yalnız doygunluk hipotezi isterse) · SAF `Score` hipotez kartları + `buildTimeline` (okuma yok) · doğrulama koşusu (Kind=verify, yalnız hipotezin adımları) · v0.10.1293 Faz 4: öneriler (`recommend.go` — YALNIZ METİN: hipotez cümlesi, geri alma metni deploy satırlarından + `Deps.ArgoApps` eki, SAF `matchRunbooks` ≤3; ajan hiçbir şeyi uygulamaz) · v0.10.1310: `Deps.ArgoApps` koşu başına TEK okuma, skordan önce `Facts.Argo`'ya (`argoFactsFor`) — config `argo_sync` kontrolü + öneri eki ortak; tekrarda okuma yok (State=replay, kontrol 0) | 5 s yoklama, iş 90 s | YALNIZ kendi kaydı (saved_views sre-investigation; operatör işareti HumanState de orada); problems / root_cause_hypotheses / derin kanıt / öncelik OKUR, yazmaz (`TestEnqueueNeverUpsertsProblems`, `TestOrchestratorReadOnly`, `TestNeverWritesHypothesisTable`, v0.10.1293 `TestSRENoMutatingCalls` / `TestSREImports` / `TestSRENeverUpsertsProblemsOrPriority`); öneri işareti (Uygulandı / Ele) da kendi kaydında · v0.10.1301 Faz 5 değerlendirme: Kind=replay işi KENDİ anahtarında (`sre-replay-<runId>-<problemId>`; canlı kaydı ezmez, alış sırası en son, kuyruk payı MaxQueue/2, 0 LLM çağrısı) → özet saved_views `sre-eval` koşu satırına; doğruluk etiketi YALNIZ onaylı incident_cases, 👎 yalnız inceleme adayı (`TestReplayDoesNotOverwriteLive`, `TestEvalIgnoresFeedback`) |

**Evaluator tik sırası** (`evaluator.go:452-622`): kurallar → SLO → DB kapasite
→ DB yavaş ifade → DB sağlığı (`db-health`, v0.10.1073) → yaygın yavaşlama (`svc-slowdown`, v0.10.1091) → runtime → paylaşılan exception patlaması → ölümcül
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
   (`incident.go` `AttachProblemToIncidentWith`; mevcut bağ yalnız incident
   çözülmemişse — v0.10.1580; aynı servis, 1-hop komşu: çözülmemiş VE son
   30 dk'da ETKİN `greatest(started_at, updated_at)`, bağlanma updated_at'i
   tazeler — v0.10.1581; yoksa yeni). Anomali
   ailesi tek kapılı olan (`attachToIncident`, `anomaly_sensitivity.go:58-73`).
   **v0.10.1588 (Davis FAZ 0.5 / D, operatör kararı 7):** terfi (`anomaly-auto:`,
   açılışta, vida `anomaly_promotion.attachToIncident`), fırtına (vida
   `exception_triage.stormAttachToIncident`) ve dış kaynak (seri / küme / tavan /
   ext-down — YALNIZ canlı tarayıcı, gölge bağlamaz; vida `anomaly_sensitivity.
   attachToIncident`) da `AttachProblemToIncident` ile bağlanır; tekil exception
   (ölümcül + paylaşılan patlama, vida `exception_triage.singleGroupJoinIncident`)
   `JoinOpenIncident` ile YALNIZ açık incident'a katılır, ASLA açmaz (kural 1 →
   servisler → komşular `joinCandidateNeighbours`). Üç vida da `*bool` nil = açık.
   Bağlama yalnız AÇILIŞTA (sonra açılan incident'a geriye bağlanmaz).
4. **Tam satır değiştirme:** `UpsertProblem` tüm satırı yazar; tazeleme yolu
   Status/Assignee/Pod/AISummary'yi TAŞIMALI (`carryProblemOperatorState`,
   `problem_aisummary_test.go`). Unutulan alan sıfırlanır (invariant #4).

## 3. Dedektör tablosu — kural id önekleri

→ `reference/detectors.md` (aynı başlık; gerekince oku).

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
staleCrit (eskalasyonla critical'a çıkan), exception-storm, incident, db-health ve monitor totalLoss'tan gelir (SLO burn
v0.10.1081'ten beri varsayılan Problem üretmez).
"P1 neden az" sorusunda önce bunu düşün; yerleşikleri varsayılan açmak çözüm DEĞİL.
`fresh deploy` tetikleyicisi v0.9.612'de KALDIRILDI — deploy bilgisi görünür,
sıralamaya karışmaz. Vidalar `problem_priority` blobu (`problem_priority.go:27-116`);
sıfır struct = varsayılan, `StaleCriticalHours=0` anlamlı. Kapatma yolu
`MarkResolved` (:549) `Value`'yu EZMEZ (v0.9.977: açık/kapalı öncelik simetrisi).

**Inbox görünüm önceliği** (`forceNonExceptionP3`, v0.9.487; v0.10.1072 kısmi revizyon):
exception/httperror dışı satırlar inbox'ta P3 — YALNIZ dar istisna listesine
(`problem_priority.inboxKeepSourcePriority`, glob `*`, tam eşleşme; kimlik Problem
RuleID / incident `incident:<severity>`) uyan satır kaynak P1/P2'sini korur, gerekçe
"kaynak önceliği korundu (…)". Varsayılan `anomaly:*:error_rate`, `builtin-*`, `svc-slowdown:*` (v0.10.1091),
`db-health:*`, `incident:critical`, `anomaly:ext:*:ext:error_count`, `anomaly-cluster:ext:*`, `anomaly:ext-cap:*`, `anomaly-auto:group:*` (v0.10.2056, ML grup Problem'i; v4 göçü `problem_priority_inbox_keep_v4`, 1091 varsayılanından); nil = varsayılan, `[]` = saf v0.9.487; geniş kalıp
400 (literal önek + ≥3 literal; `*:*` red); PUT kayıtlı değerin üstüne çözülür (alan
yoksa liste korunur). **v0.10.1091:** varsayılana `svc-slowdown:*` (yaygın yavaşlama) eklendi; kayıtlı liste 1083 varsayılanına KÜME olarak eşitse aynı desenle ikinci tek seferlik göç (`MigrateInboxKeepDefaultsV3`, işaret `problem_priority_inbox_keep_v3`; tazeleyicide v2 → v3 sırayla, v2 düşerse v3 o tur koşmaz). **v0.10.1083:** varsayılana `anomaly:ext:*:ext:error_count` + `anomaly-cluster:ext:*` (Oracle hata
serisi/kümesi) + `anomaly:ext-cap:*` (tavan özeti; `ext-down` DIŞARIDA) eklendi; kayıtlı liste ESKİ varsayılana küme olarak eşitse tek seferlik
göç yeniye taşır (`MigrateInboxKeepDefaults`, ayar tazeleyicisinde okumadan önce, işaret
`system_settings.problem_priority_inbox_keep_v2`, system audit), özelleştirilmişse dokunmaz ve bir kez loglar. Regressed P1 yükseltmesi regresyon başına bir kez bildirilir (v0.10.1078: `<fp>:regressed:p1:<epoch>`, `claimRegressed`); taban `<fp>:regressed` 90 günde bir, damgasız (kronik grup gürültüsü). Bilinçli DIŞARIDA: trace_op(_latency) / log_* / behavior_change /
`anomaly-auto:*` (grup alt ailesi hariç) / `slo:*` / yavaş ifade / self-health — listeye ekleme önerme.
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
**Problems (/inbox) varsayılanı (v0.10.1081, operatör: "Problems sayfasında sadece
P1'ler gözüksün ve first seen'e göre sıralı olsun"):** parametresiz adres
`prio=P1` + `sort=firstSeen&dir=desc` ister (FE `PRIO_DEFAULT`/`SORT_DEFAULT`,
`s_inbox` URL durumu, kişisel localStorage sıralaması okunmaz); tür varsayılanı
HER ŞEY (v0.10.1014 aynen). Sunucuda öncelik süzgeci (`applyInboxFacets`)
tavandan ÖNCE, sonra `inboxSortAndCap` — en yeni P1 kırpılmaz; çip sayaçları
süzgeçten önceki küme. "tüm öncelikler" tek tık. Açık `?prio=` / `?s_inbox=`
ve kayıtlı görünümler kendi değerini taşır. Satır tıkı HER türde tam sayfa
(çekmece v0.10.1081'te kalktı; incident → `/incident?id=`, eski `?item=`
yönlendirilir); incident sayfasının manşeti birincil (en erken) bağlı
problemin açıklaması (`features/anomalies/incidentSummary.ts`).
**Incident başına tek satır (v0.10.1084, operatör: "tekilleştir, Exceptions'taki
format güzel"):** AÇIK (resolved/closed değil) incident'ın bağlı problem satırları
gizlenir, incident satırı onların yerini tutar — açıklama birincil bağlı problemin
cümlesi, `incident.problemCount` (Occurrences "N problem"), öncelik incident +
bağlıların GÖRÜNÜM önceliklerinin en yükseği, ilk görülme en erken / son görülme en
geç, kategori birincilden. Sıra: `forceNonExceptionP3` → `foldInboxIncidents` →
facet sayaçları → facet → sıralama/tavan (`inbox_incident_fold_test.go` pinler);
bağlantı çağrı başına TEK `IncidentProblemIDs` okuması, düşerse katlama yok.
Incident satırı süzgeçle elendiyse problem gizlenmez; anomali dokunulmaz; db şeridi
incident çekmez (katlama yok). Rozet sayacı katlamaz. Satır biçimi Exceptions
listesiyle ortak `TriageTitleCell` (kalın başlık + satır içi durum çipi, soluk
ayrıntı; FE metni `lib/inboxRowText.ts`, Go ikizi `inboxHeadline` "detail"
sıralamasında); Source kolonu yok. Cache anahtarı `inbox:v10:`.

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
| Sentezleyici | `rootcause_worker.go:188` | girdiler tek toplama (`fusion.go` `gatherEvidenceInputs`, 60 dk; deploy okuması v0.10.1579'dan beri en eski çıpanın onset−30 dk'sına kadar, tavan 24 sa — `deployFetchLookback`, `GetEvidenceDeploys` geniş çıkarımı yalnız MV kapsıyorsa; kenar/ağırlıklı kenar/RUNS_ON okumaları v0.10.1628'den beri taban 60 dk + tabana sığmayan her çıpanın [onset−15m, onset+15m] penceresi, birleşik, tavan 24 sa — `edgeFetchWindows` → `Get*In(windows)`); çıpa 1 anomaliler (30 dk), çıpa 2 critical açık problemler; **derin kanıt kapısı** `shouldDeepInvestigate(isP1, hasDeploy)` (:20, v0.9.1060); derin kanıtı YALNIZ sentezleyici yazar (tek yazıcı) |
| Hakem (LLM) | `api/rca_verdict.go:144` prompt, `rca_shields.go` (kanıt id süzgeci, çürütme geçerliliği, güven tavanı, varlık kontrolü) | LLM sıra DEĞİŞTİREMEZ, güveni yalnız aşağı çekebilir; oto-hüküm `rca_auto_verdict.go:82` (AutoExplain açık + kota + 30 dk dedup) |
| Deploy korelasyonu | `correlate.go:56,205`, `rollout_problem*.go` | RCA'dan ayrı yüzey (`/api/correlate/context`) |
| "Ne başka değişti" (WhatChanged) | `chstore/correlate.go` `scoreChangedService` (SAF) ← `GetCorrelatedChangesMV` (`service_summary_5m`); `correlate_cause.go` `MarkCorrelationCauses`; manşet `RootCausePanel.tsx` `likelyCause` ← `lib/rootCauseCandidates.ts` `coMovingCause` | skor = \|Δhata puan\|·4 + min(200,\|Δrate%\|)·0.5 + min(200,\|Δp99%\|)·0.5 — **YÖNSÜZ büyüklük**, yalnız sıralar. `direction` (`changeDirection`): worse (hata ≥ +1 puan, p99 > +%25 hacim kapısız, rate > +%25 hacim kapılı) · unknown (NaN/Inf, asla better) · lost (rate ≤ −%90) · better (hata ya da p99 GERÇEKTEN düştü) · quieter (yalnız rate −%25…−%90). "Co-moving … propagation" manşeti YALNIZ `causeEligible` VE skor ≥ 20; uygunluk = özneyle taban+cari pencerede DOĞRUDAN `topology_edges_5m` kenarı (`GetServiceGraphTopN` özne odaklı, tavan 500; servissiz problemde okuma yok; okunamazsa kimse uygun değil, `topologyKnown=false` → "bağlantı doğrulanamadı") VE konuma göre yön: downstream/both → worse \|\| lost; upstream → YALNIZ trafik sıçraması (çağıranın yavaşlaması etki alanı). Skor değişmez; /rootcause 50'lik havuzu (`GetCorrelatedChangesMVTop`) işaretleyip 20'ye SONRA keser; sıra uygun → diğer → better/quieter ("iyileşti"/"trafik azaldı"). v0.10.1063, operatör: "<svc-B> ile ilgili olduğunu düşünüyor ama alakasız" — iyileşen, bağlantısız servis skor 404'le manşetteydi. v0.10.1090 — DİĞER tüketiciler de süzer: hakem kataloğu (`rca/extras.go` "kötüleşen / trafiği kesilen komşu") yalnız `causeEligible` (`gatherRCACatalogExtras` aynı işaretlemeyi yapar: havuz 50 + `rootCauseTopo`), kalanlar ADSIZ "kök neden adayı DEĞİLDİR" satırı; `/shift` `shiftChangeGroups` (worse → Worsened, lost → Lost, better/quieter → Improved, unknown hiçbiri); şerit canlı adayları yalnız `causeEligible`, yoksa `ribbonNoCandidateNote` (= `localizedNote` hükmü); anomali demeti de işaretlenir. Yönsüz skoru aday/"kötüleşen" için YENİDEN kullanma — çivi `api/correlation_consumers_pin_test.go` |
| Kıyas türü + etki alanı (v0.10.1495; v0.10.1577 derin kanıt) | `chstore/bubbleup_mode.go` `ProblemBubbleMode` (SAF; api `rootCauseBubbleMode` ince sarmalayıcı); `chstore/bubbleup_rank.go`; `api/problem_affected.go` `buildAffectedEntities`; sentezleyici `anomaly/rootcause_bubbleup.go` `bubbleUpDecision` (SAF) + `attachBubbleUp`; metin `rca/bubbleup_render.go` `RenderBubbleUpTR` (CoSRE ile tek gövde) | BubbleUp bölümü problem türünden: errors / window / none (runtime:, db-*, monitor:, self- → tarama YOK). v0.10.1577: `DeepEvidence.BubbleUp`'ı YALNIZ sentezleyici yazar, derin çıpada, bölüm (id + StartedAt) başına BİR tarama + taşıma (tik başına TEK `GetHypotheses`), tik bütçesi 2, 8 s tavan, pencere açılışı izleyen 10 dk; ≤1 tazeleme / ≤1 yeniden deneme (5 dk); problem açıklama istemine girer (mail AISummary üzerinden), kanıt kataloğuna satır EKLEMEZ. Sıralamadan ölçü/kimlik/zaman damgası adları ve dağınık sayısal değerler elenir (tüm tüketiciler). Etkilenen = doğrulanmış bağ + gözlenen etki; hata görmeyen çağıran `linked`; kenarsız co-mover panelde ayrı kapalı grup — karıştırma |
| AI SRE hipotez kartları (v0.10.1288, AYRI motor) | `sre/hypotheses.go` SAF `Score(ev)` ← `Investigation.Facts` (Faz 1b adımlarının gerçekleri + `events` + tembel `node`); tablo `hypothesisTable` | 8 tür → incident_cases kategorisi; skor = clamp(öncül + Σ ağırlık × sonuç{+1,−1,0}, 0, 1); bant yüksek ≥0.70 / orta ≥0.40; kartta ilk 3. Sentezleyiciyle YARIŞMAZ ve ona yazmaz: SynthRef yalnız bir kontrolün girdisi ("sentezleyici aynı komşu"), FE "Korelatör — ayrı motor" kutusu. LLM / oy / operatör işareti (Ele/Onayla → HumanState) skora GİRMEZ (`TestHypothesisMarkNeverChangesScore`). Yeni kontrol / ağırlık = `TestHypothesisTable` satırı (tür başına doğrula/çürüt/bilinmiyor) + DECISIONS.md |
| Başlangıçta doğan log şablonları (v0.10.1113) | `anomaly/log_template_evidence.go` `LogTemplateEvidenceFor` ← `log_templates`; uç `api/problem_log_templates.go`; FE `ProblemNewLogTemplates.tsx` | Problem başlangıcı −10 dk…+5 dk, özne + hipotezin çağrı-grafiği adayları ≤5; dedektörün aile süzgeci AYNEN (çatal yok; tek fark sayı tabanı — aday ≥1, bilinen tabansız, çünkü puller total_count/services alanlarını her tik ezer), bilinen okuması fail-closed, ≤5 satır yakınlık sırası. Kanıt, Problem DEĞİL — `log_template_new` kapalı kalır; hipotez skoruna ve Explain istemlerine GİRMEZ |

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

**Grup kaynakları (v0.10.1092):** (1) span exception'ları — `RefreshExceptionGroups`, parmak izi
`FingerprintException` (servis hash'te); (2) **Oracle hata tablosu satırları** (operatör: "Oracle hataları Exceptions
gibi görünsün, hatta Exceptions altında da olabilir") — `oracle/exgroups.go` `ExGroupRefresher`, parmak izi
`chstore.OracleGroupFingerprint` = `ora:` + sha1(kaynak kimliği | kod | operasyon)[:16] (kanal/host/servis kırılım,
anahtar değil); `ex_type` = hata kodu, `ex_message` = operasyon, servis = trace → servis oylarının baskını
(`SubjectResolver.ServiceVotes`) yoksa `oracle:<kaynak>` (merge gerçek servisi korur). Yalnız KAPANMIŞ dakikalar
(`ExGroupClosedEnd`: özel SQL `trunc(now) − windowMin − 1 dk`, tablo kipi overlap + aralık) `oracle_error_log FINAL`
dakika tanesinde (`OracleGroupAggregates`, tavan 20 000, dakika sıralı — tavanda imleç tam okunan son dakikaya).
Kalıcı blob `system_settings.oracle_exgroups:<kaynak>` (imleç + kanal/servis kırılımı + 10 dk dilimler) HER turda
yeniden okunur; artımlar TEK batch INSERT (`UpsertExceptionGroups`), imleç ondan sonra. Ayrık beyin / imleç kaydı
düşüp liderlik değişmesi bir aralığı iki kez sayabilir (bilinçli kalan risk). Kip: off = yazılmaz; shadow = grup,
bildirim yok; live = `ExceptionNotifier` kapısı (`oracle.GroupNotifyGate`; tazelik kapanmış dakika gecikmesi kadar
geriden; takım anonsu yalnız merdiven P1'inde). **Öncelik AYRI kural** (`api.oraclePriorityAt`, koordinatör kararı —
"kazanılmış P1 düşmez"den bilinçli sapma): yapışkan hacim P1'i ve regressed "açıldıktan sonra ≥N" P1'i Oracle'a
UYGULANMAZ; P1 = son 1 sa ≥ `exception_triage.oracleP1MinOccurrences` (vars. 5000) VE ≥ 3× önceki 1 sa, ya da yeni
grup + son 1 sa ≥ eşik; P2 = son 1 sa ≥ 100 ve taze/regressed; P3 "sürekli akış (Oracle)". Saatlik toplamlar +
gecikme `oracle.GroupStatsCache` üzerinden (main.go `api.SetOracleGroupStats`, her rol, 30 sn TTL) — /inbox,
Exceptions ve bildirimci aynı sayı. Span-merkezli okuyucular (`RecentNewExceptionServices`,
`FindSharedExceptionBursts`, `FindFatalExceptions`, yayılım, `MaxExceptionGroupLastSeen` boot tohumu) `ora:`'yı
`NotOracleGroupSQL` ile dışlar — pin `chstore/oracle_exception_groups_test.go`. Örnek/oluşum ucu önekte Oracle
satırlarına dallanır (kaynak parmak izinden geri çözülür; seri dakika başına ağırlık, son 24 sa). Liste
`?oracle=only|exclude` (varsayılan dahil), satır bilgisi `ExceptionGroup.Oracle` (yalnız JSON, `api/exception_oracle.go`).
**AI açıklaması (v0.10.1100):** `ora:` grubunda explain / insight girdisi `api.exceptionExplainInput` üzerinden
`anomaly.BuildOracleExceptionExplainInput` (kaynak, kod+operasyon, saatlik akış + oran, kanal %, servisler, host/instance,
≤5 çözülen trace, en yeni 3 satırın eşlenen alanları; `OracleErrorsByOpCode` tavanı 50) + `SystemPromptOracleException`;
blob gerçekleri `anomaly.SetOracleExplainFacts` (main.go, aynı `GroupStatsCache`). "Kodu da incele" Oracle'da koşmaz.
**v0.10.1103:** çözülen id'lerden Coremetry'de bulunan İLKİ tek trace olarak span + log ile girer (span yoluyla ortak
yardımcılar `anomaly/trace_evidence.go`; yüklenmeyen trace için log sorgusu yok; `logs` nil-güvenli).
Yeni bir grup kaynağı eklerken: önek + span dedektörlerinden dışlama + örnek/oluşum dalı + bildirim kapısı + öncelik
kuralı + AI girdi kurucusu (ve gecikmesi varsa oto-özet aday yaşı) altılısı.

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

→ `reference/detectors.md` (aynı başlık; gerekince oku).

## 10. AI açıklama (problem/exception tarafı)

| Yüzey | Prompt kurucu | Sistem promptu |
|---|---|---|
| Problem explain (tık + arka plan) | `anomaly.buildProblemPrompt` `problem_explainer.go:240-279` (kural → `HypothesisPromptBlockTR` → `renderEvidence` → `renderDeepEvidence` + uydurma-yasağı) | `SystemPromptProblem` `copilot/prompts.go:152` |
| Exception explain | `BuildExceptionExplainInput` `exception_context.go:298` (örnek trace + loglar `sampleTraceEvidence` → `trace_evidence.go`) → `assembleExceptionPrompt` :509 (damga operatör diliminde, v0.10.745) | `SystemPromptException` :225 |
| RCA düzyazı / hüküm | `rootcause.go:411,476`; `rca_verdict.go:144` | `SystemPromptRCAVerdict` :841 |
| Insight kartı | `api/insight.go:78` — deterministik yarı AI kapalıyken de çalışır | — |
| AI SRE araştırma anlatımı (v0.10.1287, arka plan, yüzey `sre-investigation`) | `sre/narrate.go` `renderInvestigationTR` (≤4 KB, bulgular EN SONDA; golden `TestRenderInvestigationTRGolden`; v0.10.1288 "AJAN HİPOTEZİ (kod skoru)" satırı `renderHypothesesTR`, golden `TestRenderHypothesesTR`) | `SystemPromptSREInvestigation` (dört bölüm + tek örnek + `DataNotInstruction`; hipotezi anabilir, skoru değiştiremez) |

Tüm sistem promptları TEK dosyada (`internal/copilot/prompts.go`); açıklama
affordance'ı `s.copilotExplain(r, …)` üzerinden, `s.copilot.Explain` DOĞRUDAN
değil. Golden pinler: `anomaly/prompt_golden_test.go`, `rootcause_prompt_test.go`,
`copilot/prompt_problem_test.go`, `autoexplain_gate_test.go`.

## 11. Ayar deseni (sekiz anahtar)

`anomaly_tracked` · `anomaly_sensitivity` · `anomaly_promotion` · `problem_priority`
· `problem_escalation` · `exception_triage` (v0.10.1092 `oracleP1MinOccurrences`, 0/yok = 5000) · `self_health`
· `notify_policy` (Problem standardı R1: `mode` off|shadow|enforce, varsayılan **shadow**; `p3` digest|off|immediate;
`digestTimes` ["09:00","16:00"]; `quietHours` yalnız P2; `noiseDedupeHours` 12; `impactFloors` D6; admin GET/PUT
`/api/admin/notify-policy`, audit `notify.policy`; R3 `noiseMuteHours` 168; R4 `template` tr|legacy; UI R5:
Settings → Bildirim politikası) — hepsi
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

→ `reference/detectors.md` (aynı başlık; gerekince oku).

## 13. Değişiklik kontrol listesi

1. Kural id öneki yeni mi? → §2 tüketici listesi + pin testi.
2. Problem yazıyor mu? → §2 dört soru (süpürme, eskalasyon, incident, tam satır).
3. Öncelik/merdivene dokunuyor mu? → SAF işlevde kal, gerekçe cümlesi yalan
   söylemesin, `problem_priority_*`/`exception_triage_test` tablosuna satır;
   operatör direktiflerini (P1 yapışkan, taban 2, Inbox tüm durumlar, service
   silent kapalı, disk ETA kapalı,
   yeni log deseni (`log_template_new`) kapalı, yerleşik alarm kuralları (`builtin-*`) kapalı,
   SLO burn Problem'i (`problem_priority.sloBurnProblems`) kapalı, Problems varsayılanı
   yalnız P1 + ilk görülme, fresh deploy yok) YENİDEN AÇMA. Operasyon gecikmesi
   (`trace_op_latency`) bu listeden v0.10.1085'te ÇIKTI: operatör iki ardışık kova sürdürme
   kuralıyla yeniden açılmasını onayladı ("Önerini yapalım") — varsayılanı açık; sürdürmesiz
   tek-kova kararına geri dönme.
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

- 21 saklanan kural-id ailesi (v0.10.1073 `db-health:`, v0.10.1091 `svc-slowdown:` dahil) + 2 notify-only = `internal/{anomaly,evaluator,monitor}` test-dışı dosyalarda `RuleID:` / `ruleID :=`.
- 15 arka plan döngüsü = AIOps paketleri + `main.go`'da `NewLeaderHolder` / `time.NewTicker` sahipleri (v0.10.1287: 13 → 15 — AI SRE üreticisi + drain/orkestratör; Job başlatıcı operatör kararıyla yok).
- 8 ayar anahtarı = `internal/chstore/{anomaly_tracked,anomaly_sensitivity,anomaly_promotion,problem_priority,problem_escalation,exception_triage,selfhealth,notify_policy}.go` içindeki `const *Key`.
- 12 state tablosu = `store.go`'da problems|anomaly_*|root_cause_hypotheses|rca_verdicts|incident*|exception_groups|events|notification_log CREATE'leri.
- Kaynak: `docs/DECISIONS.md:385-436` (Anomali/Problems kararları), `docs/INCIDENTS.md:285-299`, planlar `docs/plans/spec-anomaly-clustering.md`, `anomaly-rootcause.md`.

## CLAUDE.md'den taşınan kurallar (2026-11-05)

- ES: no `case_insensitive` on `query_string` (8.x rejects); `_msearch` over per-pattern `_search`; `significant_text` needs `background_filter` + `sampler`.
- Drain templating is sample-based (1000/5min), never full-scan.
- **Logstore plurality:** detectors go through `logstore.Store.CountPatterns(...)` batched (ES `_msearch` / CH tokenbf) — never call ES directly from a detector.
- **Triage:** P1 = now (critical + ayarlanabilir eşik katı (varsayılan 2×) / tamamen kayıp / ayarlanabilir açık-saat (varsayılan 4h) / exception fırtınası (≥N servis aynı pencerede yeni grup; vida: `exception_triage`); vida: `problem_priority` blobu), P2 = today, P3 = convenient. Exception'da kazanılmış P1 zamanla düşmez (operatör): patlama ya da `p1MinOccurrences`'ı aşmış grup resolve/ignore edilene dek P1; `regressed` dalı P2 ama yeniden açıldıktan sonra ≥`p1MinOccurrences` oluşumda P1; inbox'ta exception dışı türler P3, istisna listesi `problem_priority.inboxKeepSourcePriority` kaynak önceliğini korur. Deploy bilgisi görünür ama önceliğe karışmaz — `fresh deploy` tetikleyicisi yok, geri ekleme. Reason string ships with every Problem. Ayrıntı: `/aiops`.
