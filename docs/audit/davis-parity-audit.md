# Davis paritesi — FAZ 0 denetimi (2026-10-29, HEAD v0.10.1578)

Girdiler: dört salt-okunur kod denetimi (madde 1-8 + 10 kod tarafı), `/aiops`
skill'i, `docs/DECISIONS.md`. Satır numaraları HEAD v0.10.1577-1578 çalışma
ağacından; işlev adları kalıcıdır, `grep -n` ile doğrula. Prod ClickHouse bu
oturumdan erişilemedi — madde 10'daki yüzdeler OPERATÖRÜN koşacağı SQL'den
gelecek, bu belge sayı UYDURMAZ.

## 1. Özet

- **Var olan:** servis başına medyan+MAD modifiye-z (`anomaly/verdict.go:81`), 14 g mevsimsel
  ±3 kova (`anomaly.go:1411-1449`), yön (`anomaly.go:144-154`), dwell 3 + açılış/kapanış histerezisi
  (`anomaly.go:41-42,355-376`), VM örneklenmemiş RED + `vmStrict` (`red_vm_select.go:69-75`), 28 g
  hafta-saati davranış motoru (`behavior.go:56-71`), 3-hop aşağı akış yayılım yürüyüşü
  (`correlator/propagation.go:86-148`), deploy = 1. kademe kanıt (`hypothesis.go:380-416`),
  Problem standardı + güven kapıları G1-G7 (`signal_gates.go`), bildirim politikası gölge
  (`notify/policy_shadow.go`), problem/anomali/RCA kararları (üç ayrı kayıt).
- **En büyük boşluklar:** (1) baseline SERVİS düzeyinde — endpoint/kanal/DB-instance/pod başına yok;
  (2) olay dedektörlerinin yarısı (trace_op, log_pattern, svc-slowdown, monitor, fatal/shared exception)
  tek pencere, mevsimsiz, sayımı oran yerine kullanıyor; (3) gruplama = aynı servis / 1-hop /
  30 dk (`incident.go:391-432`), geçişli kapanış ve nedensel yön yok; (4) kök-neden yürüyüşü
  adayın kendi sağlığına bakmıyor, zamansal öncelik gölgede (`rootcause_temporal.go:87`);
  (5) etki, açıklama zinciri, "aynı problem olarak yeniden açılma" ve öğrenen kenar olasılığı yok;
  (6) hiçbir geri bildirim dedektör eşiğine / korelatöre dönmüyor.
- **En güçlü FP hipotezleri (kod tarafı):** (a) seyrek serviste `minBaselineRate=0` + gap-filled
  olmayan seri (`anomaly.go:1203,1241-1250`) → küçük medyan, dev oran, P1; (b) terfi hattında SAYIM
  oran yerine (`trace_ops.go:210`, `log_patterns.go:133`) → 20 yeni hata = critical `anomaly-auto`;
  (d) tek pencereli dedektörler + bayat süpürme "source silent"; (h) incident'a bağlanmayan aileler
  (`anomaly-auto`, shared/fatal exception, storm, external) → aynı olaya N satır.
- **Brief'in düzeltilen varsayımları:** Influx/TFAIL yok (v0.10.606'da söküldü) — tek dış kaynak
  Oracle; Coremetry VM'e KURAL YAZAMAZ (vmalert/recording rule/Helm yok); geniş rollup (0002)
  sihirbaz dışı, status'ta yok, prod'da muhtemelen kurulu değil, örneklenmiş span'dan.
- **Operatörden gerekenler:** §6'daki 12 karar + §4'teki iki SQL'in prod çıktısı (FAZ 0.5 seçimi
  ve FAZ 5 negatif eval seti buna bağlı).

## 2. Madde 1-8 — bugünkü durum (yoğun)

### 2.1 Madde 1 — tespit

| Dedektör | Baseline | Mevsimsel | Yön | Hacim tabanı | Flap muhafızı | Varsayılan |
|---|---|---|---|---|---|---|
| `anomaly:<svc>:<metric>` (`anomaly.go:499-779`, `verdict.go:55-161`) | medyan+MAD, ≥12 kova; VM örneklenmemiş ya da `service_summary_5m` | 14 g aynı dilim ±3 kova, gün sınıfı hafta içi/Cmt/Paz **UTC** (`anomaly.go:1358,1449`); ≥4 örnek ≥3 gün yoksa ardışık 24 sa (`:1633-1656`) | error_rate/p99 yukarı; request_rate iki yön (`:144-148`) | absFloor %1 / 10 ms, floorPct, minMAD; `minBaselineRate` **0 = kapalı** (`:1203`) | dwell 3, critical yalnız z≥6, açılış z≥3.5 kapanış z≤1.5, bant içi touch (`:878`) | error_rate, p99 AÇIK; request_rate KAPALI |
| error ramp (aynı satır, `error_ramp.go`) | hata/sn tabanı, eşik max(2×medyan, p90, %0.2) | aynı | yukarı | ≥1 hata/sn her kova | W=6 kova hepsi ihlal | AÇIK (v0.10.1518) |
| behaviour → `anomaly_events` (`behavior.go:421-516`) | 28 g × 168 saat-dilimi medyan+MAD | evet, UTC | **iki yön** | p99 ≥200 ms, hata ≥%1 (HV %0.2) | rejim ≥1.5× 6 dilim ya da z≥4 3 dilim; ≤50/tik | AÇIK; terfi edilebilir (kind süzgeci YOK `evaluator.go:1327`) |
| trace_op → `anomaly_events` (`trace_ops.go`) | düz 24 sa ortalama | **yok** | yukarı | ≥10 hata & ≥%1 pay | **yok**; tek 5 dk kova, batch dışı SAYIM oranı ≥3 (`:219,525`) | AÇIK (`LogAnomalyEnabled` kapısı) |
| trace_op_latency (`op_latency.go`) | 24 sa tDigest p99 | yok | yukarı | ≥30 çağrı, p99 ≥200 ms, ≥3× | dwell 2 (v0.10.1085) | AÇIK |
| log_pattern (`log_patterns.go`) | 1 sa trailing | yok | yukarı | ≥10 satır; yeni ≥30; payda yok | yok; oran ≥3 | AÇIK |
| log_template_new | — | — | — | — | — | **KAPALI** (v0.10.1061) |
| terfi `anomaly-auto:<fp>` (`evaluator.go:1315-1457`) | olay PeakRatio | — | — | MinCount 10 | peak ≥5 sürdürme ≥300 s; critical peak ≥20; Value=PeakRatio Threshold=5 | AÇIK; incident'a BAĞLANMAZ |
| kurallar + builtin (`evaluator.go:854`) | **statik** | yok | karşılaştırıcı | MinSamples | ForSec, Cooldown; ilk temiz okumada kapanır | builtin **KAPALI** (v0.10.1069) |
| db-health (`db_health.go`) | err% statik %5; p99/sayı dünkü aynı kova ×3 | yalnız dün | yukarı | ≥100 çağrı, ≥2 çağıran | 2 aç / 2 kapa | AÇIK; couchbase hariç |
| db-slow-stmt / db-capacity / runtime GC | statik 1 s / %85-90 / 2 s-%10 | yok | yukarı | ≥20 yürütme / — / — | 2 kova / 2 pp / %10-2 pp | AÇIK (GC yalnız VM) |
| svc-slowdown (`service_slowdown.go:173-227`) | 24 sa havuzlanmış p95, ×20 | yok | yukarı / trafik çöküşü | ≥3 op ≥30 çağrı, ≥100 toplam, p99 ≥5 s | **tek kova** (tasarım), 2 temiz kapanış | AÇIK, daima critical/P1 |
| SLO burn | politika | yok | yukarı | ≥50 span | iki pencere | **KAPALI** (v0.10.1081) |
| exception shared / fatal / storm | mutlak sayım; fatal Value=sayı Threshold=1 → 2 oluşum = P1 (`fatal_exception.go:103-107`) | yok | — | 1 oluşum / ≥3 servis / ≥5 servis | 15 dk aktif / tek satır | AÇIK; incident'a BAĞLANMAZ |
| self-* | statik / önceki gün ×4 | yok | yukarı | ≥100k span | — | AÇIK; disk ETA KAPALI |
| Oracle `anomaly:ext:…` (`external.go:246`) | evaluateAnomaly, 240 dk | ufuk ≥3 g ise | yukarı | MinAbsDelta 5, MinMAD 1 | dwell 10 dk; ≤20/tik | **shadow** |
| monitor | — | — | — | — | **tek prob** flip (`monitor/runner.go:341-358`) | AÇIK; 0/1 total loss P1 |

Ortak mekanizmalar: bayat süpürme `updated_at` > 3×aralık → "· auto-resolved: source silent"
(`evaluator.go:1123-1198`, yalnız `sweepIsTrustworthy`); yaş eskalasyonu info→warning 15 dk,
warning→critical 30 dk (`problem_escalation.go:45-51`), muaf yalnız `self-volume-spike` ve `slo:*`;
öncelik `computePriority` (`problem.go:449`): oran ≥2 büyük ihlal, Value=0 total loss, critical açık
≥4 sa → P1. Baseline matematiği HER YERDE Go'da; VM yalnız seri sağlar (`red_vm_query.go:157-175`).

### 2.2 Madde 2 — korelatör / kök neden (`internal/correlator/`)

- Kenarlar: `topology_edges_5m FINAL` son 1 sa, `sum(calls/errors/sum_duration_ns)`, LIMIT 20000
  (`service_adjacency.go:167-186`); yönlü+ağırlıklı ama ham toplam, öğrenilmiş ağırlık yok;
  `Neighbors`/`AreNeighbors` YÖNSÜZ 1 hop (`correlator.go:171-208`) — incident gruplama bunu kullanır.
  Worker pod'larında 5 dk tazeleme; api/ingest boş korelatör (`main.go:710-712`).
- Nedensel yürüyüş `propagation.go:68-148`: AŞAĞI akış ≤3 hop, çürüme 0.5/hop, skor = Π(hata payı);
  en yakın komşuyu kayırır (yaprak değil); adayın KENDİ sağlığı kontrol edilmez
  (`rootcause_worker.go:446-456`); hata payı baseline farkı değil son 1 sa toplamı.
- Zamansal öncelik `temporal.go:54-110` (Spearman, lag 0-2); yalnız `TemporalApply` ile skora girer
  — varsayılan **gölge** (`anomaly_sensitivity.go:186-190`).
- `Synthesize` (`hypothesis.go:344-658`): deploy 0.80+0.15·tazelik (yalnız çıpanın kendi servisi),
  tekrarlayan+regresyonsuz 0.10, rollout aynı imaj +0.05, yayılım 0.70×pay, aynı servis sinyali
  0.30-0.60, komşu 0.28-0.52 (yukarı akış ×0.6), eş-ateşleme 0.20; güven 0.5·(tür/4)+0.5·üst skor.
- Sentezleyici `rootcause_worker.go`: lider, 30 s, batch 64; çıpa 1 son 30 dk olaylar, çıpa 2 yalnız
  CRITICAL açık problemler (warning'e hipotez yok); derin kanıt kapısı `shouldDeepInvestigate(isP1,hasDeploy)`;
  BubbleUp v0.10.1577. Satır her tikte TÜMÜYLE yeniden yazılır (`:149,333`).
- Anomali kümesi `clustering.go:236-332`: yalnız `anomaly:<svc>:<metric>` adayları, ≥3 üye, sabit id
  `anomaly-cluster:<source>`, üyeler metinde, "merged into" ile kapanır.
- Incident bağlama `incident.go:378-519`: (1) mevcut bağ; (2) aynı servis 30 dk; (3) 1-hop komşu
  30 dk; yoksa yeni incident — başlık/servis İLK problemden, kök nedenden değil; gruplama gerekçesi
  yalnız timeline metni "clustered via service topology".
- RCA hüküm (`api/rca_verdict.go:144-167`, kalkanlar `rca_shields.go`): LLM anlatır, sıralamayı
  değiştiremez, güven tavanı ≤0.6 geçerli çürütme yoksa. WhatChanged (`chstore/correlate.go:177-259`)
  yönsüz skor + yön etiketi; `causeEligible` doğrudan kenar ister.

### 2.3 Madde 3 — Problem modeli ve yaşam döngüsü

| Tablo | Motor / anahtar | Not | ref |
|---|---|---|---|
| `problems` | RMT(version) ORDER BY id, partition YOK | + `ai_summary`, `ai_summary_at`, `comparator`, `kind` ALTER'ları; statü open/acknowledged/resolved (DDL yorumu bayat) | `store.go:2716-2734`, `:4034-4068` |
| `incidents` / `incident_events` / `incident_problems` | RMT / MergeTree / RMT(problem_id) | problem → tek incident; durum kontrolü yok | `:2789-2832` |
| `anomaly_events` | RMT ORDER BY id, TTL 30 g | + `episode_count`, `first_started_at`, `verified_ratio`; baseline/yön/durum kolonu YOK (durum = last_seen ≤10 dk) | `:2588-2602`, `:4082-4089` |
| `root_cause_hypotheses` | RMT (anchor_kind, anchor_id), TTL 30 g | candidates / recent_deploy / deep_evidence JSON | `:2642-2665` |
| `rca_verdicts`, `ai_feedback`, `anomaly_verdicts`, `incident_cases` | RMT | üç ayrı karar kaydı + vaka belleği (`cosre_correct`, `final_cause`) | `:3173`, `:2926`, `:2477`, `:3088` |
| problem kararı "gerçek / değil" | `saved_views(page='problem-verdict')` | id `pv:p:<ruleId>\|<service>`; JSON `reason` ∈ expected/too_sensitive/known_accepted/wrong_attribution/other | `problem_verdict.go:21-93,143-153` |

Yaşam döngüsü: kural ve metrik anomali yolu her açılışta YENİ id (`evaluator.go:943`,
`anomaly.go:924`); sabit-id aileler (`anomaly-auto`, `anomaly-cluster`, db-health, svc-slowdown,
self-*, monitor) satırı yeniden kullanır. Süreklilik yalnız bildirim konusu 24 sa
(`notify/lifecycle.go:41-50`) ve anomali bölüm sayaçları (`anomaly_event.go:180`, boşluk 22.5 dk).
Etki SAKLANMAZ: `StdImpact` okuma anında (`problem_standard.go:104-130`), `/affected` 1-hop ≤25 çağıran
(`problem_affected.go:96-163`). Geri bildirim tüketicileri: susturma/politika/DB süzgeci/vaka belleği —
**hiçbiri dedektör eşiğine, korelatöre, kenar ağırlığına ya da hipotez skoruna dönmez**
(`verdict_silence.go`, `policy_shadow.go`, `cosrelearn/cases.go:55-90`).

### 2.4 Madde 4 — varlık / topoloji

- Varlık tablosu `entities` / `entity_relations` / `entity_sync_runs` (0011) K8s + servis;
  **varsayılan KAPALI** (`entity/settings.go:61`), ilişkiler parent/runs_on/runs (`identity.go:15-34`).
  db/queue/ext/endpoint/kanal/Argo yalnız dizge id öneki (`identity.go:90-95`). 2026-08-23 denetimi
  birleşik varlık tablosuna KARŞI karar vermişti (`docs/audit/entity-model-audit-2026-08-23.md:421`).
- Span kenarları `WriteTopologyBucket` (`topology.go:517-998`): servis→servis, servis→node (runs_on),
  servis→db/queue/ext, queue→consumer; `topology_edges_5m` calls/errors/sum_duration/p99 TDigest,
  **14 g** saklama; op kenarları yalnız calls; kök akışlar yönsüz küme. Env etiket, süzgeç değil;
  env→cluster eşlemesi YOK.
- Sahiplik: `service_metadata.owner_team/sre_team` (+auto); LDAP grupları senkronlanır ama HİÇBİR
  tüketici servise bağlamaz (`ldap_groups.go`, `api/ldap_groupsync.go:24-26`); DB sahipliği en çok
  çağıran yaklaşıklığı.
- API: `/api/servicegraph` 1-3 hop (`servicegraph.go:157-169`), `/api/services/{name}/backtrace`,
  MCP `get_topology` 1 hop; **A→B yol / kök-neden yolu API'si YOK**.

### 2.5 Madde 5 — deploy / rollout olayları

| Kaynak | Tablo | RCA'ya ulaşır mı |
|---|---|---|
| `service.version` değişimi | `service_version_5m` (45 g) | evet — Problem RecentDeploy + Synthesize |
| pipeline/manual `events(kind='deploy')` | `events` | Synthesize havuzu EVET (`fusion.go:117`); **Problem RecentDeploy HAYIR** (`problem_telemetry.go:66-104`; `deploys.go:1090-1092` yorumu yanlış) |
| KSM rollout v1 `workload_rollouts` | 0012 | evet (`rollout_causes.go:151-198`, yalnız rollouts açıkken) |
| KSM rollout v2 `rollout_events` + sınıflandırma + ADO | 0015 (UI varsayılanı v2) | **HAYIR** — `internal/anomaly`, `correlator`, `rca` içinde referans yok |
| Argo CD sync webhook / `argocd_app_status` | `rollout_v2_schema.go:180-253` | **HAYIR** |

Geriye bakış 30 dk her yerde; rollout adayı −120..+5 dk (0.90 / 0.50); fresh deploy önceliğe
KARIŞMAZ (v0.9.612). `AnomalyPredatesDeploy` ≥3 bölüm ortalama boşluk ≤48 sa → nötr `PriorDeploy`.
Deploy-problem bağ tablosu yok; kanıt penceresi şimdiye göre (bkz. §5 bug 1).

### 2.6 Madde 6 — rollup'lar

- Dar aile 0001 (service, span_kind, status_code; TDigest; 10 s 7 g / 1 m 14 g / 5 m 90 g / 1 h 13 ay) ve
  metrik/rota aileleri sihirbazda; **geniş aile 0002** (… channel_code, endpoint, function_code;
  20-kovalı sabit histogram) ve 0004-0007 ELLE (`migrations/embed.go:25-27`), `RollupStatus`
  denetlemiyor (`rollup_admin.go:503-511`), bellek "prod'a kurulmasın" (kayıp A/B, ts ORDER BY sonu).
- Dedektör okumaları: `service_summary_5m` (metrik, davranış, takvim), `operation_summary_5m` (trace_op),
  `db_caller_summary_5m` (db-health), ham `spans` (op_latency `:473`, trace_ops `:701`); hiçbir dedektör
  `rollup_spans_*` okumaz; hiçbiri `channel_code` ile gruplamaz.
- 4 haftalık servis×endpoint×kanal için TEK uygun tablo `rollup_spans_wide_5m` (90 g) — örneklenmiş
  span (hatalar + ~%5), yüzdelik ±%15-30, kanal yazımı iki biçim (0007), Oracle üçüncü biçim
  `channel.code` (`oracle/counter.go:79`).

### 2.7 Madde 7 — VictoriaMetrics

Yalnız okuma (`/api/v1/query`, `/query_range`, label uçları; `vmetrics/client.go:512-699`) + OTLP
itme (`vmetrics/write.go:16-41`) + self-metrics. `metricSource` seam'i (`api/metricsource.go`) VM→CH
düşüş yapmaz. `redSource=auto` → VM servis başına `sum by (svc) increase(...[5m])` + `histogram_quantile(0.99)`
(`red_vm_query.go:157-182`), 28 g davranış kovaları VM'den (`red_vm_store.go:245-256`); `alertSource=auto`
kural×servis VM. **vmalert / recording rule / `/api/v1/rules` HİÇBİR YERDE yok**, Helm VM/vmalert
kurmaz (`values.yaml:153-169`). MetricsQL istatistik işlevleri tespitte 0 kullanım; yalnız copilot grafik
doğrulayıcısı `outliers_mad/zscore/mad`'a izin, `holt_winters/predict_linear`'ı reddeder
(`metric_chart_validate.go:98-116`).

### 2.8 Madde 8 — dış kaynaklar

Influx söküldü (v0.10.606, `ae82d19c`); `ExternalScanner` ortak altyapı olarak kaldı, Influx sözcükleri
`anomaly/external.go:3-16,51-52`'de bayat. Oracle hattı (`main.go:1215-1300`, lider worker): satır kancası →
özne gözlemi → dakikalık `ext:error_count` **CH `metric_points`'e** (VM'e değil; `counter.go:20`) →
exception grupları `ora:` → kip kapılı tarama (off/shadow/live; varsayılan **shadow**). Problem üretimi
`external.go`: ≤240 slot, melez özne (gerçek servis bulunursa Kind=service), eşik max(medyan,5), küme
`anomaly-cluster:ext:`, tavan `anomaly:ext-cap:`, 3 başarısız poll → `anomaly:ext-down:`. Dış olaylar
`anomaly_events`'i ATLAR, doğrudan problem. Varlık eşlemesi doğal: varlık = `ext:<kaynak>`, çocuklar
operation.code/error.code/channel.code; servis bağı çözümleyici oyu.

## 3. Madde 9 — Davis katmanı boşluk tablosu

| Davis katmanı | Durum | Kanıt | Not |
|---|---|---|---|
| Varlık başına baseline | kısmen | `anomaly.go:1288` (servis), `red_vm_query.go:159-175` (`sum by svc`), `db_health.go:88-102` (instance) | endpoint / kanal / pod / queue yok; DB yalnız dünle kıyas |
| Mevsimsellik | kısmen | `anomaly.go:1411-1449` 14 g; `behavior.go:56-71` 28 g; `calendar.go` | trace_op/op_latency/log_pattern/svc-slowdown/db-health mevsimsiz; gün sınıfı UTC |
| Yön | kısmen | `anomaly.go:144-154`; `behavior.go:587-608`; `correlate.go:237-259` | trace_op error_spike SAYIM oranı (`trace_ops.go:219`); log_pattern paydasız; behaviour request_rate yukarı terfi edilebilir |
| Hacim ağırlığı | kısmen | absFloor/minMAD `anomaly_sensitivity.go:546-552`; D6 tabanları `signal_gates.go`; `minBaselineRate=0` `anomaly.go:1203` | bildirimde var (D6), AÇILIŞTA zayıf; olay dedektörleri yalnız mutlak taban |
| Flapping / histerezis | kısmen | dwell `anomaly.go:355-376`; touch `:878`; W=6 `verdict.go:124-139`; db-health 2+2; op_latency 2 | trace_op, log_pattern, svc-slowdown, monitor, GC, fatal/shared exception TEK pencere; kural yolu ilk temizde kapanır |
| Topoloji varlıkları | kısmen | 0011 `entities` (K8s+servis, KAPALI); `identity.go:28-120` önek tipleri | db/queue/ext/endpoint/Argo app varlık değil; kalıcı altyapı id'si yok |
| Tipli / ağırlıklı kenarlar | kısmen | `topology_edges_5m` calls/errors/p99 (`store.go:3479-3540`); protokol multiIf (`topology.go:560-566`) | tip = protokol+önek; op kenarları yalnız calls; 14 g saklama; öğrenilmiş ağırlık yok |
| Dikey kenarlar (pod→node→cluster) | kısmen | servis→node `topology.go:639-696` → `RankNodeCauses` `propagation.go:191-230`; pod→node yalnız `entity_relations` | node→cluster yürüyüşü yok; problem cluster listesi taşır, varlık zinciri değil |
| Sahiplik | kısmen | `service_metadata` owner/sre (`store.go:2312-2340`); DB yaklaşık (`db_ownership.go:9-51`) | LDAP grupları bağlanmamış; ext/queue/Argo sahipsiz |
| Yol API'si (A→B, kök yolu) | **yok** | `servicegraph.go:157-169` 1-3 hop komşuluk; `Path` boş (`rootcause_worker.go:378-383`) | kök akışlar sırasız küme |
| Olayları TEK probleme gruplama | kısmen | `incident.go:391-432` (aynı servis / 1-hop / 30 dk); `clustering.go:23,236-332` (≥3 metrik anomalisi) | geçişli kapanış yok; terfi/exception/external aileleri hiç bağlanmaz (`AttachProblemToIncident` çağıranları); baş = ilk problem |
| Nedensel yürüyüş → bağımlılık | kısmen | `propagation.go:86-148` 3 hop aşağı; `temporal.go` gölge | adayın sağlığı kontrolsüz; yaprak değil yakın; pay baseline farkı değil |
| Deploy = birinci sınıf kanıt | kısmen | `hypothesis.go:380-416` kademe 1; `rollout/score.go` 0.90/0.50 | yalnız çıpanın kendi deploy'u; ~30 dk sonra düşer (§5 bug 1); v2/Argo/ADO ulaşmaz (§5 aday 5); Problem RecentDeploy olayları görmez (§5 aday 4) |
| Etki analizi | kısmen | `problem_standard.go:104-130` okuma anı; `problem_affected.go:96-163` 1 hop ≤25 | saklanmaz; endpoint/kullanıcı/geçişli etki yok |
| Açıklama zinciri | kısmen | `ScoredCause.Reason/Path/TemporalReason` (`rootcause_hypothesis.go:220-235`); `rca_record.go:36-51` | LLM CausalChain, reddedilen hipotezler, gruplama gerekçesi SAKLANMAZ (serbest metin) |
| Aynı problem olarak yeniden açılma | kısmen | sabit-id aileler satır tekrar kullanır; `notify/lifecycle.go:41-50` 24 sa; `anomaly_event.go:180` bölüm | ana yollar her açılışta yeni id; sabit-id yeniden açılış kapalı incident'a bağlanır (§5 bug 2) |
| Öğrenilmiş kenar olasılığı | **yok** | sabitler `hypothesis.go:47-126`, `propagation.go:40-49` | `propagation.go:36-39` "future refinement" |
| Geri bildirim döngüsü | **yok** | tüketiciler yalnız susturma/politika/süzgeç/vaka belleği (`verdict_silence.go:62-109`, `cosrelearn/cases.go:55-90`) | RCA 👍 yalnız prompt öncülü (`rca_verdict.go:117-141`) |
| Değişim noktası (onset) | kısmen | bölüm `started_at` (`anomaly_event.go:153-204`); behaviour shift onset (`behavior_scan.go:326`) | dwell ilk kovası = onset; CUSUM/PELT yok; behaviour onset terfi sürdürmesini atlatıyor |
| Eval seti | kısmen | `sre-eval` replay (`sre/` Faz 5, onaylı `incident_cases`); notify replay `FoldNotifyReplay` | dedektör düzeyi FP/recall ölçüsü ve `make eval-problems` yok |

## 4. Madde 10 — FP sınıfları, kod kaynakları, SQL

### 4.1 Sınıf → kod kaynağı → mevcut muhafız

| Sınıf | Kod kaynağı (hipotez) | Mevcut muhafız | SQL'de nasıl işaretlenir |
|---|---|---|---|
| (a) düşük trafik | `minBaselineRate=0` (`anomaly.go:1203`); seri gap-filled değil, dwell saatlere yayılabilir (`:1241-1276`); error_rate Threshold = küçük medyan → dev oran (`:931`, `problem.go:524`); olay dedektörleri yalnız mutlak taban | absFloor %1 / 10 ms, minMAD, z≥6, dwell 3; D6 bildirim tabanı (açılışı durdurmaz) | `anomaly`+error_rate ve threshold < 0.5 (%); oran ≥ 20; `source silent` kapanış |
| (b) statik / yanlış eşik | builtin/slo (kapalı), db-health %5, db-slow-stmt 1 s, db-capacity 85/90, GC 2 s/%10, svc-slowdown 5 s; fatal Value=sayı/Threshold=1; storm daima P1; terfi SAYIM-oran (`trace_ops.go:210`, `log_patterns.go:133`, PeakRatio ≥5/≥20) | ForSec/Cooldown/MinSamples; db-health 2+2; rule disabled kapanışları | aile statik listesi; `anomaly-auto` için `anomaly_events.peak_ratio` + kind `trace_op`/`log_pattern` + `current_count` küçük |
| (c) mevsimsellik eksik | trace_op düz 24 sa, op_latency 24 sa, log_pattern 1 sa, svc-slowdown 24 sa, db-health yalnız dün; metrik gün sınıfı UTC (`anomaly.go:1358`); ardışık 24 sa'ya düşüş (`:1633-1642`) | behaviour tam mevsimsel; takvim yalnız request_rate düşüşü (`:900-908`) | bayrak: yerel Cmt/Paz ya da 00-06 açılış + ≤60 dk kapanış (zayıf; yalnız aday) |
| (d) flapping / tek pencere | trace_op, log_pattern, svc-slowdown, monitor, GC, fatal/shared tek pencere; kural yolu ilk temizde kapanır; behaviour onset terfi sürdürmesini atlatır (`behavior_scan.go:326`) | dwell/W=6/2+2/touch; Cooldown; bölüm boşluğu 22.5 dk | aynı rule_id+service önceki kapanıştan ≤15 dk içinde yeni açılış; açık süre ≤10 dk |
| (e) yanlış yön | trace_op error_spike SAYIM ≥3× (`trace_ops.go:219,525`); log_pattern paydasız; behaviour request_rate↑ olayı terfi edilebilir (`evaluator.go:1327`); down olayları yazılıyor | batch pay kuralı (`:277`); error ramp saf yükü yoksayar | `anomaly`+request_rate; `anomaly-auto` + olay kind trace_op pattern error_spike |
| (f) dış kaynak | Oracle `anomaly:ext:*`, `anomaly-cluster:ext:*`, `ext-down` (poller hatası problem olur), `ext-cap`; melez özne | shadow, dwell 10, MinAbsDelta 5, tavan 20 | aile external (kind='external' ya da önek) |
| (g) yanlış kök neden | `hypothesis.go`, WhatChanged, deploy atfı; shared/fatal/storm/db-health özne; Oracle `SubjectResolver` | kalkanlar (`rca_shields.go`), AnomalyPredatesDeploy | `saved_views` reason='wrong_attribution'; `incident_cases.cosre_correct=0` |
| (h) diğer: örnekleme yanlılığı, kopya, eskalasyon | span MV örneklenmiş (`DECISIONS.md:10470`); terfi/shared/fatal/storm/external incident'a bağlanmaz → aynı olaya N satır; 30 dk warning→critical + 4 sa → P1 | VM `vmStrict`; incident katlama (yalnız bağlılar) | `kaynak: span` işareti; aynı servis ±30 dk içinde ≥3 farklı aile; eskalasyon description'a DAMGALANMAZ → ölçülemez (`evaluator.go:1221-1272`) |

### 4.2 Tablo erişimi (prod, dış Distributed CH)

`problems`, `incident_problems`, `anomaly_events`, `anomaly_verdicts`, `saved_views`, `incident_cases`,
`notification_log` STATE tablolarıdır: 0009 birleşik replikasyon grubunda `ReplicatedReplacingMergeTree`,
shard edilmez, Distributed sarmalayıcı YOK (`migrations/0009_state_unify.sql:8-13`, `state_replication.go:6-16`).
Kod her yerde `FROM problems FINAL` okur (`problem.go:423,1201,1273,…`). Bu yüzden SQL herhangi bir
replikada çıplak tablo adıyla koşar. **Önce** split-brain kontrolü (0009 öncesi kurulumda replikalar
farklı sayı verir): `SELECT hostName(), count() FROM clusterAllReplicas('<küme>', currentDatabase(), problems)
GROUP BY hostName()` — sayılar eşit olmalı. Tüm referanslar NİTELİKSİZ.

### 4.3 Sorgu A — son 30 gün, satır başına sezgisel sınıf + aile/sınıf sayımı

```sql
-- Sorgu A. Heuristik SINIF, zemin gerçeği DEĞİL. Sabitler: 30 gün, bigBreach 2.0 (problem_priority
-- varsayılanı), flap 15 dk, kopya penceresi ±30 dk (incident.go:391 ile aynı). İlk eşleşen sınıf kazanır.
WITH
base AS (
  SELECT id, rule_id, rule_name, severity, service, metric, value, threshold, comparator, status,
         description, kind, started_at, resolved_at,
    multiIf(
      startsWith(rule_id,'anomaly-auto:'),                                   'anomaly-auto',
      startsWith(rule_id,'anomaly-cluster:ext:') OR startsWith(rule_id,'anomaly:ext') OR kind='external', 'external',
      startsWith(rule_id,'anomaly-cluster:'),                                'anomaly-cluster',
      startsWith(rule_id,'anomaly:') AND endsWith(rule_id,':service_silent'),'service-silent',
      startsWith(rule_id,'anomaly:'),                                        'anomaly',
      startsWith(rule_id,'builtin-'),                                        'builtin',
      startsWith(rule_id,'slo:'),                                            'slo',
      startsWith(rule_id,'db-health:'),                                      'db-health',
      startsWith(rule_id,'db-slow-stmt'),                                    'db-slow-stmt',
      startsWith(rule_id,'db-capacity:'),                                    'db-capacity',
      startsWith(rule_id,'svc-slowdown:'),                                   'svc-slowdown',
      startsWith(rule_id,'runtime:'),                                        'runtime',
      startsWith(rule_id,'exception:shared-dependency'),                     'exception-shared',
      startsWith(rule_id,'exception:fatal-infrastructure'),                  'exception-fatal',
      rule_id = 'exception-storm',                                           'exception-storm',
      startsWith(rule_id,'self-'),                                           'self',
      startsWith(rule_id,'monitor:'),                                        'monitor',
      'rule') AS family,
    -- computePriority ikizi (problem.go:499-527): '<' kuralında 1/oran
    if(threshold > 0,
       if(comparator IN ('<','<='), if(value > 0, threshold / value, 0), value / threshold), 0) AS ratio,
    dateDiff('minute', started_at, coalesce(resolved_at, now64(9))) AS open_min,
    multiIf(
      position(description,'source silent')>0 OR position(description,'signal loss')>0,         'silent',
      position(description,'merged into')>0,                                                   'merged',
      position(description,'rule disabled')>0 OR position(description,'disabled by default')>0
        OR position(description,'problems disabled')>0,                                        'disabled',
      position(description,'anomaly cleared')>0,                                               'cleared',
      position(description,'anomaly muted')>0,                                                 'muted',
      position(description,'batch servis')>0,                                                  'batch',
      position(description,'system excluded')>0,                                               'excluded',
      position(description,'problem mode off')>0,                                              'mode_off',
      position(description,'watch source broken')>0,                                           'watch_broken',
      status = 'resolved',                                                                     'recovered_or_unknown',
      'open') AS resolution,
    position(description,'kaynak: VictoriaMetrics')>0 AS src_vm,
    position(description,'kaynak: span')>0            AS src_span,
    if(startsWith(rule_id,'anomaly-auto:'), substring(rule_id, 14, 16), '') AS event_fp
  FROM problems FINAL
  WHERE started_at >= now64(9) - INTERVAL 30 DAY AND started_at < now64(9)
  ORDER BY started_at
  LIMIT 200000
),
ev AS (
  SELECT id AS fp, kind AS ev_kind, pattern AS ev_pattern, peak_ratio, current_count, episode_count
  FROM anomaly_events FINAL
  WHERE last_seen >= now64(9) - INTERVAL 31 DAY
  LIMIT 100000
),
w AS (
  SELECT b.*, ev.ev_kind, ev.ev_pattern, ev.peak_ratio, ev.current_count, ev.episode_count,
    lagInFrame(resolved_at, 1) OVER (PARTITION BY rule_id, service ORDER BY started_at
      ROWS BETWEEN 1 PRECEDING AND 1 PRECEDING) AS prev_resolved,
    count() OVER (PARTITION BY service ORDER BY toUnixTimestamp(started_at)
      RANGE BETWEEN 1800 PRECEDING AND 1800 FOLLOWING) AS dup_n,
    uniqExact(family) OVER (PARTITION BY service ORDER BY toUnixTimestamp(started_at)
      RANGE BETWEEN 1800 PRECEDING AND 1800 FOLLOWING) AS dup_families,
    (toDayOfWeek(started_at, 0, 'Europe/Istanbul') >= 6 OR toHour(started_at, 'Europe/Istanbul') < 6) AS off_hours
  FROM base b LEFT JOIN ev ON ev.fp = b.event_fp
),
classified AS (
  SELECT *,
    multiIf(
      family IN ('builtin','slo') OR resolution = 'disabled',                              'b_static_disabled',
      family = 'external',                                                                   'f_external',
      family = 'service-silent',                                                             'a_low_traffic',
      resolution = 'silent',                                                                 'a_low_traffic_silent',
      family = 'anomaly' AND metric = 'error_rate' AND threshold > 0 AND threshold < 0.5,   'a_low_traffic',
      family = 'anomaly' AND ratio >= 20,                                                    'a_low_traffic_hugeratio',
      family = 'anomaly' AND metric = 'request_rate',                                        'e_direction',
      family = 'anomaly-auto' AND ev_kind = 'trace_op' AND ev_pattern LIKE '%error_spike%',  'e_direction_count',
      family = 'anomaly-auto' AND ev_kind IN ('trace_op','log_pattern') AND current_count < 50, 'b_count_as_ratio',
      family = 'anomaly-auto' AND ev_kind = 'behavior_change',                                'd_behaviour_onset',
      prev_resolved IS NOT NULL AND dateDiff('minute', prev_resolved, started_at) BETWEEN 0 AND 15, 'd_flapping',
      status = 'resolved' AND open_min <= 10,                                                'd_single_window',
      family IN ('exception-fatal','exception-storm','exception-shared','monitor','runtime',
                 'db-slow-stmt','db-capacity','svc-slowdown','self'),                        'b_static_family',
      dup_families >= 3,                                                                     'h_duplicate_wave',
      src_span = 1 AND family IN ('anomaly','anomaly-cluster'),                              'h_sampled_source',
      family = 'anomaly' AND off_hours = 1 AND status = 'resolved' AND open_min <= 60,       'c_seasonality_suspect',
      family IN ('db-health','rule','anomaly-cluster','anomaly','anomaly-auto'),             'g_or_true_unclassified',
      'unclassified') AS cls
  FROM w
)
SELECT family, cls, count() AS n,
       round(100 * n / sum(n) OVER (), 1)                   AS pct_all,
       round(100 * n / sum(n) OVER (PARTITION BY family), 1) AS pct_family,
       countIf(severity = 'critical') AS critical_n,
       countIf(ratio >= 2) AS big_breach_n,
       round(avg(open_min), 0) AS avg_open_min
FROM classified
GROUP BY family, cls
ORDER BY family, n DESC
LIMIT 500
SETTINGS max_execution_time = 60;
```

Sınırlar: (1) `escalation` description'a yazılmıyor → eskalasyonla critical'a çıkan satır
ayrıştırılamaz (critical ∧ ratio<2 ∧ open ≥30 dk şüphelidir, SQL'e koymadım — `rule` ailesinde
warning açılıp critical biten satır bu); (2) `rule` ailesi (operatör kuralları) eşiğin doğruluğu
bilinmeden sınıflanamaz; (3) `b_count_as_ratio` eşiği 50 keyfî; (4) `off_hours` dilimi İstanbul
varsayıldı; (5) `source silent` gerçek çökme de olabilir; (6) `anomaly_events` 30 g TTL'li, eski terfi
satırları olaysız kalır (LEFT JOIN boş `ev_kind` → `g_or_true_unclassified`). Yüzdeler ham satır payıdır,
"FP oranı" değil — zemin gerçeği Sorgu B'nin birleşimlerinden gelir.

### 4.4 Sorgu B — ≥50 örnek + zemin gerçeği birleşimi (manuel inceleme)

Sorgu A'nın `WITH … classified AS (…)` bloğunu aynen alıp SON `SELECT`'in yerine şunu koy:

```sql
SELECT c.family, c.cls, c.id, c.rule_id, c.service, c.metric, c.severity,
       round(c.value, 3) AS value, round(c.threshold, 3) AS threshold, round(c.ratio, 2) AS ratio,
       c.open_min, c.resolution, c.src_vm, c.dup_families, c.ev_kind, c.peak_ratio, c.current_count,
       c.started_at,
       -- operatör problem kararı (saved_views page='problem-verdict'; FE imzası p:<ruleId>|<service>)
       pv.name                                         AS verdict,          -- real | noise | ''
       JSONExtractString(pv.query_string, 'reason')     AS verdict_reason,
       -- anomali olayı kararı (yalnız anomaly-auto)
       av.verdict                                      AS event_verdict,    -- anomaly | not_anomaly | ''
       -- vaka belleği (CoSRE doğruluğu, onaylı final_cause)
       ic.status AS case_status, ic.cosre_correct, ic.category AS case_category,
       substring(ic.final_cause, 1, 80)                AS final_cause,
       -- incident bağı
       ip.incident_id,
       -- bildirim politikası gölgesi (en son karar)
       nl.policy_decision,
       substring(c.description, 1, 160)                AS description_head
FROM (SELECT *, concat('pv:p:', rule_id, '|', service) AS pv_key FROM classified) c
LEFT JOIN (
  SELECT id, name, query_string FROM saved_views FINAL
  WHERE page = 'problem-verdict' AND owner_id = '' AND name != '' LIMIT 5000
) pv ON pv.id = c.pv_key
LEFT JOIN (
  SELECT event_id, verdict FROM anomaly_verdicts FINAL
  WHERE created_at >= now64(9) - INTERVAL 60 DAY LIMIT 50000
) av ON av.event_id = c.event_fp
LEFT JOIN (
  SELECT source_id, status, cosre_correct, category, final_cause FROM incident_cases FINAL
  WHERE source = 'problem' AND opened_at >= now64(3) - INTERVAL 31 DAY LIMIT 50000
) ic ON ic.source_id = c.id
LEFT JOIN (
  SELECT problem_id, incident_id FROM incident_problems FINAL
  WHERE attached_at >= now64(9) - INTERVAL 31 DAY LIMIT 200000
) ip ON ip.problem_id = c.id
LEFT JOIN (
  SELECT related_id, argMax(error, sent_at) AS policy_decision FROM notification_log
  WHERE channel_kind = 'policy' AND sent_at >= now64(9) - INTERVAL 31 DAY
  GROUP BY related_id LIMIT 200000
) nl ON nl.related_id = c.id
ORDER BY c.family, c.cls, cityHash64(c.id)
LIMIT 6 BY c.family, c.cls
LIMIT 150
SETTINGS max_execution_time = 60;
```

Okuma kuralı: `verdict='noise'` ya da `event_verdict='not_anomaly'` ya da `cosre_correct=0` →
onaylı FP; `verdict='real'` ya da `case_status='confirmed'` → onaylı TP; geri kalanı inceleme
kuyruğu (sınıf yalnız ÖNERİ). Zemin gerçeği az olacaktır (kararlar v0.10.1015+, vaka belleği
onay bekleyen taslaklar) — bu, FAZ 5 eval setinin neden elle etiket istediğinin kanıtı.

### 4.5 Sonuçlar nereye akar

- **FAZ 0.5 seçimi:** Sorgu A'da `pct_all` en yüksek 2-3 sınıf × Sorgu B'de o sınıfın `noise`/`not_anomaly`
  ağırlığı. Kod tarafı adaylar §6 FAZ 0.5'te; nihai seçim SQL sonucunu bekler.
- **FAZ 5 negatif eval seti:** Sorgu B'de `verdict='noise'` / `not_anomaly` / `cosre_correct=0` satırları
  + manuel etiketlenen örnekler → `testdata/eval/problems/negatives.jsonl` (id, rule_id, service, metric,
  started_at, sınıf, etiket, kaynak). Pozitif set: `verdict='real'`, onaylı `incident_cases`.
  `make eval-problems` bu seti dedektör replay'inden (`anomaly_explain.go` + `sre-eval`) geçirir;
  ana ölçü **FP oranı = işaretli negatiflerin kaçı yine açılıyor**.

## 5. Bug adayları

**Devam ediyor (operatör onaylı, ayrı ajan; yeniden planlanmaz):**

1. Deploy kanıtı saklanan hipotezden ~30 dk sonra düşüyor — havuz `[now−30m, now]` (`fusion.go:30,117`),
   seçim `[StartedAt−30m, StartedAt]` (`fusion.go:247-256`), satır her tikte tam yazılıyor
   (`rootcause_worker.go:149,333`).
2. Yeniden açılan sabit-id problem kapalı incident'a bağlanıyor — kural 1 durum kontrolsüz
   (`incident.go:382-387`), `GetIncident` durum süzmüyor (`:237-249`).
3. Uzun incident 30 dk sonra üye almıyor — kural 2/3 `started_at ≥ now−30m` (`incident.go:391-398,420-426`).

**Onay bekleyen (topoloji/deploy denetiminin bulguları):**

4. Problem `RecentDeploy` pipeline deploy olaylarını görmüyor — `fetchDeploysByService` yalnız
   `service_version_5m`/span (`problem_telemetry.go:66-104`); `deploys.go:1090-1092` yorumu aksini iddia
   ediyor (yanlış). Etki: `/api/operator-events` ile bildirilen deploy problem kartında yok, nötr
   `PriorDeploy` da hesaplanmıyor. Düzeltme: `GetDeploysInWindow` birleşik okuyucusunu kullan.
5. RCA rollout adayları yalnız v1 `workload_rollouts` okuyor (`rollout_problem.go:34-49`,
   `rollout_causes.go:151-198`); v2 `rollout_events` / Argo sync / ADO zenginleştirmesi RCA'ya hiç
   ulaşmıyor — UI varsayılanı v2 olduğundan prod'da rollout kanıtı fiilen YOK. Düzeltme: kaynak=v2 iken
   `rollout_events` + `rollout_classification` okuyan ikinci okuyucu, aynı `rollout/score.go` puanı.

Her ikisi FAZ 0.5'e girebilir ama FP sınıfı değil, EKSİK kanıt sınıfıdır (g); etki ölçümü için
Sorgu B'deki `final_cause`'u deploy içeren vakalar sayılır.

## 6. Faz planı (gerçeğe göre revize)

Genel kurallar: her faz kendi birimi (v0.10.X dizisi), eşikler `system_settings` blobundan,
her kök-neden kararı gerekçeli dize taşır, belirsiz tasarım kararında DUR ve sor; `api.go`
büyümez (yeni uçlar kendi dosyasında, `/api-route`); her fazda `go test -race ./...` + fe kapıları
+ `make audit` yeşil; Problem modeli/UI geriye uyumlu (yeni kolon = `DEFAULT`'lu ALTER, iki-boot
kuralı `/clickhouse-schema`).

### FAZ 0.5 — kanamayı durdur (2-3 kalem, minimal)

Kod tarafı adaylar (nihai seçim §4 SQL sonucunu bekler):

| # | Aday | Değişiklik | Dosya | Ölçü |
|---|---|---|---|---|
| A | `minBaselineRate` varsayılanı 0 → metrik başına küçük taban (ör. error_rate için ≥0.2 req/s, p99 için ≥0.5 req/s) + seyrek seride dwell kovalarının ARDIŞIK olması | ayar varsayılanı + `evaluateAnomaly` ardışıklık kapısı | `anomaly_sensitivity.go:546-552`, `anomaly.go:1203,1241-1276`, `verdict.go` | Sorgu A `a_*` sınıfı; replay `anomaly_explain.go` ile 30 g yeniden koşum: kaç satır açılmazdı |
| B | Terfi hattında SAYIM-oran: `new_error` / yeni log deseni olaylarında `peak_ratio` ham sayı yerine pay ya da taban-normalize; critical ≥20 kapısı yalnız gerçek oran | `trace_ops.go:210`, `log_patterns.go:133`, `promotionGate` | `evaluator.go:1512-1520` | `b_count_as_ratio` + `e_direction_count` sayısı |
| C | Tek pencereli olay dedektörlerine dwell 2 (trace_op error_spike, log_pattern) — op_latency 1085 deseniyle | `classifyOpLatency` ikizi | `trace_ops.go:209-220`, `log_patterns.go:121-138` | `d_single_window` + `d_flapping` |
| D | Terfi/shared/fatal/storm/external ailelerini incident'a bağla (katlama için) — davranış değişikliği, ÖNCE SOR (karar 7) | `AttachProblemToIncident` çağrısı | `evaluator.go:1413-1457`, `shared_exception.go`, `fatal_exception.go` | `h_duplicate_wave` |
| E | §5 aday 4 + 5 (deploy kanıtı eksikleri) | okuyucu değişimi | `problem_telemetry.go:66-104`, `rollout_causes.go:151-198` | deploy içeren `final_cause` vakalarında RecentDeploy doluluğu |

Çıkış ölçütü: seçilen her kalem için "son 30 günde açılan N problemden M'si açılmazdı" sayısı
(replay ile, Sorgu A sınıfı başına) + işaretli `real`/onaylı vaka recall'ı %100 (D8 ile aynı bar).
Boyut: kalem başına 1-2 sürüm, S.

### FAZ 1 — varlık başına baseline + yönlü `anomaly_event`

- Yeniden kullanım: `evaluateAnomaly` çekirdeği (medyan+MAD, dwell, histerezis), VM RED okuyucusu
  (`red_vm_query.go`), davranış motoru mevsimsel kovaları, takvim.
- Yeni: (1) baseline ÖZNE anahtarı `(entity_kind, entity_id, metric)` — servis + endpoint
  (`http_route`, VM `sum by (svc, route)`) + db instance + queue; (2) `anomaly_events`'e `direction`,
  `baseline_value`, `baseline_kind` (seasonal/consecutive), `subject_kind`, `subject_id` kolonları
  (DEFAULT'lu ALTER, iki-boot); (3) günlük mevsimsel kovaları için **VM'de hesaplama kararı** (karar 1):
  (i) Coremetry kendi hesaplar (bugünkü yol, Go), (ii) vmalert recording rule
  (`coremetry:red:p99_5m`, `…:seasonal_median_14d`) — vmalert kurulumu + kural yazma yüzeyi
  (`/api/admin/vm-rules`, Helm `vmalert.enabled`) GEREKİR; (4) trace_op/log_pattern/svc-slowdown'a
  aynı mevsimsel taban (c sınıfı); (5) gün sınıfı için operatör dilimi (karar 3).
- Config: `anomaly_sensitivity.subjects` (endpoint/db/queue aç-kapa, tavanlar), `anomaly_sensitivity.seasonalTimezone`.
- Risk: endpoint kardinalitesi (1000s servis × 100s rota) → VM sorgu bütçesi tik ≤24 kuralı;
  `operation_summary_5m` span adı ≠ rota.
- Boyut: M-L (3-5 sürüm). Çıkış: eval seti üzerinde FP ≤ FAZ 0.5 sonucu, recall ≥ mevcut; endpoint
  düzeyi en az bir onaylı vaka yakalanır.

### FAZ 2 — topoloji grafı (entity / entity_edge, yol API)

- Yeniden kullanım: `topology_edges_5m` + `WriteTopologyBucket` (kenar üretimi), 0011 `entities` /
  `entity_relations` (dikey), `service_metadata` (sahiplik), `thanos.ClusterConfig` (cluster).
- Karar 4: (i) 0011'i genişlet — `entity_type`'a service/endpoint/db/queue/ext/argo_app ekle, `entity_relations`'a
  `calls`/`publishes`/`consumes` ilişki tipleri + ağırlık kolonları; ya da (ii) yeni `entity_edge_5m`
  (from_kind, from_id, to_kind, to_id, relation, calls, errors, p99, cluster, env). 2026-08-23 kararı (i)'e
  karşıydı; Davis için en az ilişki tipi + ağırlık + 90 g saklama gerekir.
- Yeni uç `internal/api/entity_path.go`: `GET /api/entity/path?from=&to=&hops≤4`, `GET /api/entity/{id}/upstream|downstream`
  (serveCached, hash-all-inputs, viewer okur). Env→cluster eşlemesi ayar (`clusters.envMap`).
- Boyut: M (3-4 sürüm). Çıkış: `/api/entity/path` p99 <200 ms sıcak; onaylı vakaların ≥%80'inde
  gerçek kök ↔ etkilenen arasında yol bulunur.

### FAZ 3 — gruplama + nedensel kök neden (+ etki, yeniden açılma, açıklama zinciri)

- Yeniden kullanım: `propagation.go` yürüyüşü, `temporal.go`, `Synthesize` kademeleri, `ScoredCause`,
  `StdImpact`, incident kuralları, `notify/lifecycle.go` imza sürekliliği.
- Yeni: (1) `problem_groups` (RMT; group_id, root_entity, member problem ids, reason chain JSON,
  opened/closed, version) — karar 5: incident'ı bu role evriltmek mi (geriye uyumlu, UI var) yoksa
  yeni tablo mu; (2) nedensel yürüyüş v2: adayın kendi anomali/problem durumu ZORUNLU, yaprak-önce,
  pay = baseline farkı, zamansal öncelik varsayılan AÇIK (gölgeden çıkış kararı 6); (3) deploy/rollout
  (v1+v2+Argo+events) tek kanıt kaynağı `deploy_evidence` okuyucusu, onset'e göre pencere;
  (4) etki: `problem_impact` kolonu (JSON: callers, endpoints, failed/min, transitive hops) yazım anında;
  (5) yeniden açılma: imza `(family, subject, metric)` + 24 sa → aynı `problem id`, `episode_count`
  artar, incident bağı durum kontrollü; (6) açıklama zinciri `explanation` JSON (adım, kanıt id, skor,
  gerekçe) `root_cause_hypotheses`'e; LLM CausalChain da buraya.
- Config: `problem_grouping` (hop, pencere, min üye), `rootcause.temporalApply`, `rootcause.requireCandidateDegraded`.
- Boyut: L (6-8 sürüm). Çıkış: eval setinde "olay başına problem sayısı" medyanı 1; onaylı vakalarda
  top-1 kök doğruluğu ≥ mevcut `sre-eval` sayısı + 20 puan (mevcut sayı FAZ 0 çıktısıyla sabitlenir).

### FAZ 4 — öğrenme döngüsü

- Girdi: `saved_views` problem kararları (reason), `anomaly_verdicts`, `rca_verdicts` + `ai_feedback`,
  onaylı `incident_cases`. Bugün hiçbiri eşiğe dönmüyor.
- Yeni: (1) `too_sensitive` → özne başına hassasiyet çarpanı `anomaly_subject_overrides` (sınırlı, operatör
  görür/sıfırlar); (2) `wrong_attribution` + onaylı kök → kenar başına nedensel olasılık `edge_prior`
  (Beta sayaçları; `propagation.go` payını çarpar); (3) vaka belleği → benzer imzada önceki `final_cause`
  önerisi (zaten `cosrelearn`, skora bağlanır); (4) değişim noktası: CUSUM/PELT ile onset, dwell ilk
  kovası yerine (karar 8 — behaviour olayları terfiye alınır mı); (5) isteğe bağlı Holt-Winters A/B —
  VM `holt_winters()` MetricsQL ile yalnız GÖLGE kolon, karar 1'e bağlı.
- Boyut: L. Çıkış: öğrenme açıkken eval FP oranı düşer, recall düşmez; her öğrenilmiş değer UI'da
  gerekçesiyle görünür ve sıfırlanabilir.

### FAZ 5 — CoSRE entegrasyonu + eval seti

- CoSRE yalnız anlatır: `explanation` zinciri + grup + etki → `SystemPromptProblem` girdisi; tespit
  LLM'siz (mevcut `/aiops` kuralı).
- `make eval-problems`: `testdata/eval/problems/{positives,negatives}.jsonl` + replay sürücüsü
  (`anomaly_explain.go` karar motoru + `sre-eval` replay) → FP oranı, recall, top-1 kök doğruluğu,
  olay başına problem sayısı; CI'da eşik regresyonunda kırmızı.
- Boyut: M. Çıkış: ana ölçü FP oranı; hedef sayısı FAZ 0 SQL + FAZ 0.5 sonrası ile konur.

### KARARLAR — OPERATÖRDEN GEREKEN

1. **VM kural altyapısı:** baseline'lar VM recording rule (vmalert) ile mi, Go'da mı? vmalert'i kim
   kurar/işletir (Helm'e `vmalert.enabled` eklenir mi, prod'da mevcut VM yanına konur mu)? Hayır ise FAZ 1
   bugünkü Go yolunda kalır, MetricsQL yalnız okuma işlevleriyle (`zscore_over_time`, `mad_over_time`).
2. **Endpoint/kanal kaynağı:** geniş rollup 0002'yi prod'a kurmak (örneklenmiş, sihirbaz dışı, A/B kaybı)
   yerine VM `http_route` histogramları + `spanmetrics_1m` (30 g) mi? Kanal kodu için tek kaynak Oracle
   sayacı mı?
3. **Mevsimsel gün sınıfı** UTC → operatör dilimi (`calendar.timezone`, İstanbul) — davranış değişikliği,
   geçmiş eşiklerle kıyas bozulur.
4. **Varlık tablosu:** 0011'i genişlet mi, yeni `entity_edge_5m` mi (2026-08-23 kararı geçersiz sayılır mı)?
   Entity katmanı prod'da açılacak mı (varsayılan kapalı)?
5. **Gruplama kabı:** incident'ı Davis "problem grubu" rolüne evriltmek mi, yeni `problem_groups` mu?
6. **Zamansal öncelik** (`TemporalApply`) gölgeden canlıya alınsın mı (FAZ 3)?
7. **Incident bağlama kapsamı:** `anomaly-auto`, shared/fatal exception, storm, external aileleri incident'a
   bağlansın mı (inbox katlaması değişir)?
8. **Davranış motoru olayları** terfiye (Problem'e) alınabilsin mi, yoksa yalnız kanıt mı kalsın?
9. **FAZ 0.5 kalemleri:** A-E'den hangileri — SQL çıktısından sonra.
10. **Bug adayları 4 ve 5** (deploy olayları RecentDeploy'a, v2/Argo rollout RCA'ya) onay.
11. **Eval seti etiketleme:** operatör ekibi Sorgu B çıktısından ≥50 satırı elle etiketler mi (FAZ 5 ön
    koşulu); hangi sürede?
12. **Oracle kipi:** FAZ 1'de dış seriler `anomaly_events`'ten geçsin mi (bugün doğrudan problem), shadow
    kalır mı?

### KARARLAR — VERİLDİ (operatör, 2026-10-29)

1. **Go'da.** vmalert yok; baseline Go'da, VM'den okunarak. Endpoint serileri servis başına üst-N
   (trafiğe göre, N ayardan) ile sınırlı.
2. **Metrikten.** Endpoint kırılımı VM `http_route` histogramlarından (örneklenmemiş); 0002 prod'a
   kurulmaz. Kanal kırılımı Oracle sayacından (madde 12). Prod VM'de `http_route` DOLU (operatör
   teyidi 2026-10-29); FAZ 1 yine de etiketsiz serviste servis düzeyine düşer.
3. **İstanbul.** Gün sınıfı operatör diliminde; geçiş haftası baseline bir kez kayar (sürüm notu).
4. **0011 genişler.** Mevcut K8s katmanına service/endpoint/db/ext türleri + ilişki tipleri. Prod'da
   katman kapalıysa FAZ 2 açılışı da kapsar.
5. **Incident üstlenir.** Yeni `problem_groups` tablosu yok; FAZ 3 incident'ı grup rolüne evriltir.
6. **Gölgede kalır (öneri onaylı).** FAZ 3'te eval setiyle gölge/canlı yan yana ölçülür; gerçek kökü
   kaybettirmiyorsa o sürümde canlı.
7. **Bağlanır (öneri onaylı).** `anomaly-auto`, storm, external her zaman bağlanır (gerekirse yeni
   incident); tekil exception grubu yalnız aynı servis/komşuda AÇIK incident varsa katılır, kendi
   incident'ını açmaz.
8. **Problem birincil.** Davranış olayları terfi edebilir; ham anomaliler iç sinyal katmanı, Problem
   dwell + gruplama sonrası açılır, Anomalies hacmi düşer. `-batch` yük kuralı korunur.
9. **A → C → B → D (öneri onaylı).** Her biri ayrı sürüm, eşikler ayardan. Sorgu A sonucu sıralamayı
   ve "açılmazdı" sayısını doğrular. E: 1579-1583 GEMİDE.
10. **GEMİDE** (1582, 1583).
11. **Eval:** aile başına dengeli ≥50 satır; gerçek/değil = mevcut düğmeler, kök neden = Problem
    detayına küçük alan. FAZ 1 bitmeden gerekir; süre operatörde.
12. **VM'e taşı + sinyal hattı (öneri onaylı).** (i) `ext:error_count` 14 g CH + VM çift yazım,
    (ii) okuma VM'e, CH yazımı kalkar, (iii) FAZ 1'de `kind=external` anomali sinyali → dwell/gruplama,
    servis incident'ına katılır, kök aday olur. Kip anahtarı kalır; gölge = "sinyal kaydedilir, Problem
    açılmaz". Prod kipinin bugünkü değeri operatörden teyit bekliyor.

## 7. Ek — düzeltilecek bayat iddialar (SKILL.md / yorumlar)

- `/aiops` SKILL.md satır numaraları 2026-09-17 ağacından; güncel: `evaluateAll` 521-705, anomali taraması
  499-779, `evaluateOne` 854, builtins 344-413, `evaluateAnomaly` `verdict.go:55`, `applyOutcome` 863-1003,
  `promoteStrongAnomalies` 1315, `promotionGate` 1512, süpürme 1123-1171, eskalasyon 1221-1269,
  `computePriority` 449, `MarkResolved` 636, `OpenProblemsSnapshot` 1809, `EnrichProblemsForRead`
  `problem_telemetry.go:508`, `Synthesize` `hypothesis.go:344`, incident bağlama `incident.go:378`.
- SKILL.md "davranış motoru alert AÇAMAZ" YANLIŞ — terfi kind süzgeci yok (`evaluator.go:1327-1334`).
- SKILL.md korelatör "iki-hop ertelendi" bayat — 3 hop (`propagation.go:48`).
- SKILL.md'de yok: heap bandı, `elastic_ml` / `calendar_traffic_drop` olay türleri, alert_target/watcher/
  log-query üreticileri, `minBaselineRate=0` varsayılanı, UTC gün sınıfı, terfi/shared/fatal/storm/external
  ailelerinin incident'a bağlanmaması; svc-slowdown p95 yavaş pay (v0.10.1546) DECISIONS.md'de eksik.
- `store.go:2590` `anomaly_events.kind` DDL yorumu yalnız log_pattern|trace_op diyor; behavior/calendar/
  elastic_ml de yazılıyor. `store.go:2725` problems status yorumu "open | resolved" — acknowledged de var.
- `deploys.go:1090-1092` yorumu "problem zenginleştirmesi olayları birleştirir" — yanlış (§5 aday 4).
- `runtime_pods.go:92-95,109-111` "GC işlevleri çağrılmıyor" — `runtime_vm.go:71,90` çağırıyor.
- `anomaly/external.go:3-16,51-52` Influx sözcükleri — kaynak yalnız Oracle.
- `incident.go:374-377` bağlama yorumu kuralları eski sırayla anlatıyor.
- Bellek `project-rollup-layer.md:72-80` geniş rollup prod durumu DOĞRULANMAMIŞ —
  `SELECT name FROM system.tables WHERE name LIKE 'rollup_spans_wide%'` ile ölçülmeli.
