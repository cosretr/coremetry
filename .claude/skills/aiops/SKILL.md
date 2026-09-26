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
→ DB yavaş ifade → runtime → paylaşılan exception patlaması → ölümcül
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
│    kind + FingerprintAnomaly(kind,pattern,service) (anomaly_event.go:62);
│    yazım yalnız mergeAnomalyCarry üzerinden (started_at korunur, peak yalnız
│    yükselir). Terfi: evaluator.go:1265 anomaly_promotion vidaları.
├─ Yalnız bildirim, saklanmayacak? ───────────────► notify-only Problem
│    (incident:* ve exception P1 duyurusu böyle; incident_alert.go:35,
│    exception_notifier.go:124). UpsertProblem ÇAĞIRMA.
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
| `builtin-*`, kural id | `evaluator.go:771` evaluateOne (+9 builtin :286) | compare + ForSec + Cooldown + MinSamples | kural WindowSec; MV fast-path | service |
| `slo:<id>:<sev>` | `slo_burn.go:35-86` | hızlı VE yavaş burn | 1h/6h · 6h/24h | service |
| `db-capacity:<check>` | `db_capacity.go:157-273` | ≥85 warn / ≥90 crit, histerezis 2pp, ETA | 2 sa regresyon | db |
| `db-slow-stmt` | `db_slow_statement.go:25` | DBSlowQueryConfig | — | db |
| `runtime:jvm-gc*` | `runtime_vm.go:66` (yalnız VM) | RuntimeAlertConfig | 10 dk | service; heap kuralı EMEKLİ v0.9.551 |
| `exception:shared-dependency` | `shared_exception.go:26-132` | aynı tip ≥3 servis / 5 dk kova; ≥10 critical | 24 sa / 15 dk aktif | id kova taşır |
| `exception:fatal-infrastructure` | `fatal_exception.go:28-74` | 1 oluşum, IsFatalExceptionType | 24 sa / 15 dk | id kova TAŞIMAZ |
| `self-*` (5) | `selfhealth.go:46-66`, `selfhealth_volume.go` | ingest-stall / spool / disk ETA / kanal / hacim ×4 | 10 dk … 24 sa | Service="" (hacim hariç); `self-volume-spike` eskalasyondan muaf |
| `anomaly-auto:<fp>` | `evaluator.go:1265` terfi | PeakRatio + Count + MinSustained | son 1 sa anomaly_events | mute'lara uyar |
| `anomaly:<svc>:<metric>` | `anomaly.go:579-797`, `verdict.go:34` | \|z\| ≥ CriticalZ, DwellBuckets | 5 dk kova; 24 sa ardışık ya da mevsimsel 14 g ±3 | Threshold = baseline medyanı, Comparator yöne göre (v0.9.978) |
| `anomaly:<svc>:service_silent` | `anomaly.go:876-1023` | 3 sıfır kova + %90 aktif taban | — | **varsayılan KAPALI** (v0.10.543) — yeniden önerme |
| `anomaly-cluster:<source>` | `clustering.go:23-441` | ≥3 yayılım-bağlı taze açılış, katılım ≤30 dk | 60 dk kanıt | üyeler bastırılır |
| `exception-storm` | `exception_storm.go:31-129` | ≥N servis yeni grup / pencere | exception_triage | Service="" — filo düzeyi, tanım gereği P1 |
| `anomaly:<ext>:…`, `anomaly:ext-down:`, `anomaly:ext-cap:` | `external.go` | aynı çekirdek; 3 ardışık poll hatası = down; tik başına açılış tavanı 20 | ≤240 dk kova | external; ext-down/cap **poller sahipli** |
| `monitor:<id>` | `monitor/runner.go:378-461` | prob durumu | — | Value 0 / Threshold 1 → totalLoss P1 |
| `incident:<id>` (notify-only) | `incident_alert.go:35` | created/resolved | — | saklanmaz |

anomaly_events üreticileri (Problem değil): recorder (`recorder.go:107,129`),
trace_op (`trace_ops.go:171`), davranış motoru (`behavior.go:224`,
kind=`behavior_change`, LLM dedektör DEĞİL hüküm katmanı — deterministik
kapılardan geçer, alert AÇAMAZ).

## 4. Problem modeli ve öncelik

`chstore.Problem` (`problem.go:129-265`). SAKLANMAYAN alanlar: RunbookURL,
Clusters, OwnerTeam/SRETeam, RecentDeploy, Priority/Reason, Category/DisplayID,
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
`fresh deploy` tetikleyicisi v0.9.612'de KALDIRILDI — deploy bilgisi görünür,
sıralamaya karışmaz. Vidalar `problem_priority` blobu (`problem_priority.go:27-116`);
sıfır struct = varsayılan, `StaleCriticalHours=0` anlamlı. Kapatma yolu
`MarkResolved` (:549) `Value`'yu EZMEZ (v0.9.977: açık/kapalı öncelik simetrisi).

**Exception merdiveni** ayrı SAF işlev `exceptionPriorityAt` (`inbox.go:1832`):
patlama (BurstMinTotal + BurstMinRate) → **P1 yapışkan** (v0.9.1205); `regressed`
→ P2; `Occurrences ≥ P1MinOccurrences` → **P1 yapışkan** (v0.10.741, kronik
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
| Yumuşak-hata yönü | `evaluator.go:1323,1467`; `selfhealth.go:277`; `rca_auto_verdict.go:101` | mute listesi okunamadı → süzgeçsiz terfi; aktif set okunamadı → hiçbir şeyi kapatma; ölçülmemiş ≠ temiz; dedup okunamadı → üret. Her kapı için yön BİLİNÇLİ seçilir ve yorumda yazar |
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
| Hipotez birleştirici | `hypothesis.go:284 Synthesize` (SAF) | deploy 0.80 (+0.15 tazelik) · yayılım ×0.70 · aynı-servis sinyal [0.30,0.60] · komşu [0.28,0.52] (upstream ×0.6) · eş-ateşleme 0.20 · rollout +0.05 doğrulama; güven = 0.5·genişlik + 0.5·güç (:112-125) |
| Sentezleyici | `rootcause_worker.go:188` | girdiler tek toplama (`fusion.go:98`, 60 dk); çıpa 1 anomaliler (30 dk), çıpa 2 critical açık problemler; **derin kanıt kapısı** `shouldDeepInvestigate(isP1, hasDeploy)` (:20, v0.9.1060); derin kanıtı YALNIZ sentezleyici yazar (tek yazıcı) |
| Hakem (LLM) | `api/rca_verdict.go:144` prompt, `rca_shields.go` (kanıt id süzgeci, çürütme geçerliliği, güven tavanı, varlık kontrolü) | LLM sıra DEĞİŞTİREMEZ, güveni yalnız aşağı çekebilir; oto-hüküm `rca_auto_verdict.go:82` (AutoExplain açık + kota + 30 dk dedup) |
| Deploy korelasyonu | `correlate.go:56,205`, `rollout_problem*.go` | RCA'dan ayrı yüzey (`/api/correlate/context`) |

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
/ `self-volume-spike` (`selfhealth.go:46-66`). Kapı: ölçülen ↔ kapsanan ayrımı
(`covered` haritası :277-280; ölçülmemiş = temiz DEĞİL, v0.9.984); kapatınca
açık satırlar boşaltılır (:268). Ayar `self_health` (pointer Enabled). Runbook
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
Behavior.Enabled, SelfHealth.Enabled) — `false` ile "yok" ayrılsın.

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
| v0.9.337 / v0.9.444 | Terfi mute'lara uyar; tazeleme Status/Assignee/Pod/AISummary taşır | `evaluator.go:1308-1330,1419-1455` |
| v0.9.587 / v0.9.825 | İki katmanlı bildirim dedup'ı; şiddet salınımı fırtına yapamaz; harita süreç-yerel (restart bir kopyaya izin verir) | `problem_dedup.go` |
| v0.9.516 / v0.9.1060 / v0.9.1281 | Derin kanıt tek yazıcı; derin kapı = P1 VEYA deploy; oto-hüküm hipotez yazıldıktan SONRA, 30 dk dedup zamana göre | `rootcause_worker.go:263-324` |
| v0.9.1304 / v0.9.1335 | problems / anomaly_events / root_cause_hypotheses PARTITION BY'sız (Kural P1) | `store.go` ilgili DDL yorumları |
| v0.9.627 → v0.10.741 | Exception P1 merdiveni tarihçesi: uçurum → basamak → 4 sa → yapılandırılabilir patlama → yapışkan P1 → hacim yapışkan | `inbox.go:1848-1985` |
| v0.10.364 | Liste ucu öncelik eklemezse sayfa sessizce boş çizer | `exception_inbox.go:52-58` |
| v0.9.1279 / v0.9.984 / v0.9.1294 | Self-health ölçülen-kapsanan ayrımı; hacim sıçraması eskalasyondan muaf | `selfhealth.go:19-33,277-280` |
| v0.10.587 / 588 / 597 | Dış seri: tik başına açılış tavanı (20) + özet problem; 3 hata = down; yalnız başarılı poll sonrası tarama (sıfır dolgulu sahte düzelme yok) | `external.go:3-24,132-151,489-511` |
| v0.9.572 / v0.9.609 | Paylaşılan patlama id'si zaman kovası taşır, ölümcül taşımaz | `shared_exception.go:52`; `fatal_exception.go:43` |
| v0.10.700 | Zamansal sıralama gölge kipte | `anomaly_sensitivity.go:83-88` |

## 13. Değişiklik kontrol listesi

1. Kural id öneki yeni mi? → §2 tüketici listesi + pin testi.
2. Problem yazıyor mu? → §2 dört soru (süpürme, eskalasyon, incident, tam satır).
3. Öncelik/merdivene dokunuyor mu? → SAF işlevde kal, gerekçe cümlesi yalan
   söylemesin, `problem_priority_*`/`exception_triage_test` tablosuna satır;
   operatör direktiflerini (P1 yapışkan, taban 2, Inbox tüm durumlar, service
   silent kapalı, fresh deploy yok) YENİDEN AÇMA.
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

- 19 saklanan kural-id ailesi + 2 notify-only = `internal/{anomaly,evaluator,monitor}` test-dışı dosyalarda `RuleID:` / `ruleID :=`.
- 13 arka plan döngüsü = AIOps paketleri + `main.go`'da `NewLeaderHolder` / `time.NewTicker` sahipleri.
- 7 ayar anahtarı = `internal/chstore/{anomaly_tracked,anomaly_sensitivity,anomaly_promotion,problem_priority,problem_escalation,exception_triage,selfhealth}.go` içindeki `const *Key`.
- 12 state tablosu = `store.go`'da problems|anomaly_*|root_cause_hypotheses|rca_verdicts|incident*|exception_groups|events|notification_log CREATE'leri.
- Kaynak: `docs/DECISIONS.md:385-436` (Anomali/Problems kararları), `docs/INCIDENTS.md:285-299`, planlar `docs/plans/spec-anomaly-clustering.md`, `anomaly-rootcause.md`.
