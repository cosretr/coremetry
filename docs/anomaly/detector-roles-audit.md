# Dedektör rolleri ve altın set kapısı — denetim

Tarih: 2026-11-27 · Taban: v0.10.2482 · Durum: ONAY BEKLİYOR · Kod, şema, ayar ve test değişikliği yok.
Hedef çıta: Dynatrace Davis. Skill: `/aiops`.

Okunan girdiler (tekrar edilmez, atıf verilir): `docs/anomaly/anomaly-reassessment-audit.md` (aşağıda "R-audit"),
`docs/anomaly/old-detectors-ml-gap.md` ("eski-gap"), `docs/anomaly/davis-ml-gap.md` ("davis-gap"),
`docs/anomaly/d1-olcum.md`, `docs/plans/ml-next.md`, `docs/plans/spec-davis-style-problem-merge.md` ("merge-spec").
Paralel belge: `docs/anomaly/precision-program.md` (aynı tabandan, başka ajan). Etiket kaynakları ve dedektör
başına isabet orada; bu belge envanter, altın set kapısı ve rol modelinin sahibidir.

İşaretler: **[K]** kodda doğrulandı (dosya:satır verilir) · **[S]** yerel dev ClickHouse'ta (26.2, veritabanı
`coremetry`) `EXPLAIN PLAN` ile sözdizimi ve kolon adları denetlendi; prod'da koşulmadı, sayı sonucu yok ·
**[V]** varsayım ya da çalışma anında doğrulanmamış çıkarım.

---

## Özet

- Bugün Problem açan tespit yolları üç kola iniyor: ML grup kolu ("≥ 2 farklı sinyal"), ML tek sinyal kolu
  (600 sn + 2 × taban, etki kuralıyla açılmış consumer lag'de muaf) ve ML servis kolu (etki kuralı olayları
  tek başına açar). Bunların dışında kapısız açanlar var: exception fırtınası, ölümcül ve paylaşılan exception,
  Oracle dış kaynak ailesi (gölge kipte de Problem açar), SLO burn (varsayılan kapalı) ve yerel dedektörler
  (hepsi varsayılan kapalı). [K] (BÖLÜM 0)
- Rol modeli (SEMPTOM / KANIT / KESİN OLAY) bugünkü kodun büyük kısmıyla uyumlu. Servis kolunun beş
  dedektöründen dördü SEMPTOM ya da KESİN OLAY. Grup kolu ile tek sinyal kolunun açtığı Problem'lerin çoğu ise
  yeni kuralda açılmaz, çünkü tetikleyenleri KANIT rolünde. (BÖLÜM 2)
- Brifin rol listesi operatörün önceki altı kararıyla çelişiyor. Bunlar sessizce uygulanmadı ve
  "Açık kararlar"da iki tarafıyla yazıldı.
- Altın set için yeni tablo gerekmiyor: olay ve sakin pencere kayıtları `saved_views(page='ml-golden-case')`,
  koşular `saved_views(page='ml-golden-run')` olarak tutulur (`sre-eval` emsali). [K] emsal: `chstore/sre_eval.go:8-15`
- **Bugün ölçülemeyen dört şey var. Kapıdan önce kapatılmaları gerekiyor:**
  - Problem'in açılış anı saklanmıyor. `started_at` olayın başlangıcını tutar.
  - Servis kolu Problem'inin kimliği sabit (`anomaly-auto:svc:<svc>`). Yeniden açılış eski satırı ezer, geçmiş kaybolur. [K]/[V]
  - `problems` ve `ml_events` tablolarında cluster ve namespace kolonu yok.
  - Grup kapısını geçmiş üzerinde yeniden koşan bir motor yok. `ReplayGroupDecisions` kancası var, çağıranı yok. [K] `ml/group_gate.go:474`
- Kapı iki katmanlı önerilir:
  - CI (GitHub) prod verisi göremez. Burada sentetik, dondurulmuş fikstür setiyle SAF karar işlevleri koşar.
  - Prod'da gece işi gerçek altın seti koşar ve sonucu önceki koşuyla karşılaştırır.
  - Bütçe tahmini: gece başına ~500 CH sorgusu ve ≤ 300 VM aralık sorgusu, 5–15 dk. [V]
- Ayrıca istenen başlıklar bölümlerde: span'den oran hesaplayan yollar (BÖLÜM 3), sadeleştirme ve silme
  listesi (BÖLÜM 4), zorunlu sıradaki beş dilim (BÖLÜM 5). Belge operatöre üç soruyla biter.

---

## BÖLÜM 0 — Güncel envanter (v0.10.2482)

Notlar: ML ana anahtarı `ml.enabled` varsayılan kapalı (`chstore/ml_settings.go:343`); ML satırlarının "açık" varsayılanı ana anahtar açıkken geçerlidir. ML Problem'leri `ml.groupProblems` anahtarına bağlı (nil = açık, `ml_settings.go:249,290-292`). Kollar: **G** grup, ≥ 2 farklı sinyal (`ml/group_gate.go:185,210-214`) · **T** tek sinyal (`:185-209,335`) · **S** servis (`ml/svc_shadow.go:121-174`) · **D** hiçbir kolda sayılmaz (`groupExcluded`, `group_gate.go:244` → `chstore/ml_events.go:263-272`).

| Ad | Kaynak | Varsayılan (anahtar) | Problem yolu | Yazıcı |
|---|---|---|---|---|
| svc_error_rate | VM RED, etki kuralı | açık (`ml.detectors`, `ml_settings.go:327`) | S: tek başına açar (sahip) | `ml/svc_error_rate.go:336` |
| svc_response_time | VM RED `_bucket`, etki kuralı | açık (:329) | S: tek başına | `ml/svc_response_time.go:515` |
| endpoint_impact | VM RED rota, etki kuralı | açık (:330) | S: servis olayı varsa üye, yoksa açar (`svc_shadow.go:87,305`) | `ml/endpoint_impact.go:606` |
| pod_oom | KSM, Thanos üzerinden | açık (:331) | S: endpoint ile aynı ikincil kural | `ml/pod_oom.go:389` |
| svc_breadth | op_latency olaylarından türev | açık (:328) | S: tek başına | `ml/svc_breadth.go:141` |
| op_latency (yavaş pay + p95) | VM RED | açık (:324) | T + G | `ml/trace_red.go:676` |
| op_error_rate | VM RED | kapalı | T + G | `trace_red.go:676` |
| dependency_latency / dependency_error_rate | VM istemci histogramı | açık / açık | T + G / yalnız G | `ml/dependency.go:358` |
| metric_kafka consumer_lag | VM (etki kuralı) ya da Thanos | açık | T, süre ve kat şartsız; ≥ 2 × taban → P1, günlük desen → P3 (`group_gate.go:195-208`) | `ml/kafka_lag_impact.go:244` |
| metric_kafka diğer, jvm, pool, http_client, k8s | VM ya da Thanos (aile başına otomatik) | kapalı (kafka hariç) | yalnız G | `ml/metric_thanos.go:512` |
| trace_change_point / change_point (log) | VM p95 / log | kapalı | T (log'da yalnız level=error) + G | `ml/change_point.go:189` |
| trace_error_count | CH `spanmetrics_1m` | kapalı | T (sayım) + G | `ml/trace_errors.go:152` |
| log error_rate / template_spike / rare_template / rare_error_code / pod_outlier | logs (ES/CH) | kapalı | error_rate: T + G; diğerleri yalnız G | `ml/log_red.go:443`, `log_templates.go:352`, `log_breakdown.go:212,242` |
| throughput_drop, service_silent, log_volume, trace_pod_outlier, new_template | VM / log / Drain | yalnız new_template açık | D: hiçbir kolda sayılmaz (v0.10.2477) | `trace_red.go:676`, `log_red.go:443`, `trace_pods.go:184`, `new_template.go:95` |
| svc_telemetry_volume | span hacmi / istek | ML açıksa koşar | D (platform satırı, akış dışı) | `ml/volume.go:284` |
| exception-storm | exception_groups | her zaman açık, anahtar yok (`anomaly/anomaly.go:539`) | tek başına açar, P1 sabit | `anomaly/exception_storm.go:101,120,141` |
| exception:fatal-infrastructure / shared-dependency | exception hattı | her zaman açık | tek başına açar | `anomaly/fatal_exception.go:112`, `shared_exception.go:193` |
| Oracle `anomaly:ext:*`, `anomaly-cluster:ext:*`, `ext-down`, `ext-cap` | Oracle poller → `metric_points ext:` | `oracle_sources[].problemMode` = shadow; shadow da bildirimsiz Problem açar (`oracle/settings.go:204-213`, `main.go:1351-1363`) | tek başına açar | `anomaly/external.go:194-237,452-595,952` |
| slo:<id>:<sev> | span MV / spanmetrics (burn) | kapalı (`problem_priority.sloBurnProblems`, `chstore/problem_priority.go:65-72`) | tek başına açar | `evaluator/slo_burn.go:102-206` |
| metric_anomaly (+error_ramp), anomaly-cluster | VM RED, yoksa `service_summary_5m` | kapalı (`native_detectors.metric_anomaly`, `native_detectors.go:55-62`) | error_rate sahip; p99/request_rate yalnız üye | `anomaly/anomaly.go:978`, `clustering.go:375-464` |
| svc-slowdown, db-health, db-capacity, db-slow-stmt, runtime:* | MV / gauge / VM | kapalı (`native_detectors.*`, `evaluator.go:622-646`) | tek başına açar (svc-slowdown sahip sınıfı) | `evaluator/service_slowdown.go:607`, `db_health.go:689`, `runtime_vm.go:67` |
| subjects, calendar drop, heap band, Elastic ML poller | CH / VM / ES `_ml` | kapalı ya da metric_anomaly'ye bağlı; poller config'te açık ama ES ister | yalnız `anomaly_events`; terfi `promoteStrongAnomalies` (kapalı kapı arkasında) | `subjects_eval.go:135`, `elasticml/poller.go:246`, `evaluator.go:1375` |
| self-*, kullanıcı alarm kuralı, watcher, monitor | self-obs / kural / sentetik | kural başına | tek başına açar — **kapsam dışı** | `selfhealth.go:715`, `evaluator.go:961`, `monitor/runner.go:382` |


**12 Kasım'dan bu yana** [K, `git log --diff-filter=D` ve dosya başlıkları]:
- **Silinen:** `ml/promote.go` (v0.10.2067), yerel `service_silent` (2068), `log_template_new` (2085), `trace_op_latency` (2087), `selfhealth_volume` (2242), `log_pattern`/`trace_op`/`behavior_change` (2270); Incidents API 410 (2075, `api/incidents_gone.go:38`); taban hijyeni, yani etiketli pencereyi tabandan dışlama (2269, `ml/quality.go:12`).
- **Eklenen:** grup kolu canlı (2052), tek sinyal kolu (2092), servis kolu canlı (2130), servis içi birleşme (2155–2164), yayın kapıları gölge (2241), entity grafı birleşme (2274–2341), etki kuralı I1–I5 (2372–2396), impact replay (2392), endpoint_impact (2474), pod_oom ve lag günlük deseni P3 (2476), Sinyaller "Neden Problem değil" (2481–2482, `ml/why_not_problem.go`).
- **Değişen:** 2473 graf birleşme görünümü açık (`problem_graph_merge`) ve yayın kapıları varsayılan `on` (`ml_settings.go:282-287`), ama yalnız üç gürültü kapısı uygulanıyor (`ml/publish_gate.go:47`), K10 hiçbir kipte uygulanmıyor (`publish_gate_enforce.go:50-53`) · 2475 telemetri hacmi akış dışı (`chstore/ml_events.go:286,302-316`) · 2476 bitişinden ≥ 15 dk geçmiş olay Problem'i açık tutmuyor (`ml/problem_end.go:33`) · 2477 yük/yapı dedektörleri hiçbir kolda yok · 2479 Problems listesi ve rozet yalnız Problem.

---

## BÖLÜM 1 — Altın set kapısı

### 1.1 Bugünkü değerlendirme düzeni (neye oturuyor)

| Motor | Ne koşar | Girdi / saklama | Sınır | Grup kapısını koşar mı |
|---|---|---|---|---|
| `ml/backtest.go` (`POST /api/ml/backtest`) | judgeMM (z/MAD, takvim tabanı, dwell, rejim) | `ml_series` tek servis × tek sinyal; hiçbir şey yazmaz | 14 gün (`:26`), 500 isabet, 20 sn (`api/ml.go:331`), 2 CH sorgusu (`backtest.go:410-425`) | hayır |
| `ml/impact_replay.go` (admin ucu) | etki kuralı (`impactDecide`) | `ml_series` 5 dk kovaları, her kova 5 özdeş dakika sayılır (`:9-15`) | 1–14 gün, 200 özne, 10 sn bütçe (`:51-60`) | hayır; birleşme ve yeniden açma yok (`:17-20`) |
| `ml/replay.go` (`make eval-ml`) | kayıt sözleşmesi + yayın kapıları | dışa aktarılmış `ml_events` JSONL, çevrimdışı (`:5-13`) | kaydı olmayan olay `no_record` | hayır |
| `ReplayGroupDecisions` | grup + tek sinyal kolu | `ml_events` satırları | — | **kanca var, çağıran yok** (`group_gate.go:471-480`) |
| SRE eval (`sre-eval`) | CoSRE araştırma doğruluğu | onaylı `incident_cases`, `saved_views` koşu satırı ≤ 64 KB (`chstore/sre_eval.go:8-15`) | ≤ 50 vaka (`api/sre_eval.go:51`) | ilgisiz (tespit değil) |

Sonuç: altın setin "tespit" kapısı bugün hiçbir motorda yok. Etki kuralı, grup kolu ve servis kolu üç ayrı
SAF işlevde duruyor (`impactDecide`, `DecideGroupPromotion`, `decideSvcShadow`). Kapı bu üçünü tik sırasıyla
ve aynı girdiyle koşan tek bir "kapı replay" yürütücüsü ister. Yeni dedektör gerekmez; yalnız yürütücü gerekir. [K]

### 1.2 Kayıt biçimi

**Servis kimliği.**
- Kimlik = cluster + namespace + service.name.
- Entity modelinde servis kimliği bugün global: `svc:<name>`, cluster ya da namespace taşımıyor (`internal/entity/identity.go:81`).
- Cluster ve namespace iş yükünde: `wl:<cid>/<ns>/<kind>/<name>`. Servis ile iş yükü `entity_relations` üzerinden `serves`/`runs` ile bağlı (`identity.go:38-45,79-83`). [K]
- Kayıt bu yüzden üçlüyü ve olay anındaki iş yükü kimliklerini birlikte saklar.
- Eşleştirme kuralı ("ad eşleştirme yok"):
  1. Problem'in servisi, entity id `svc:<name>` olarak alınır.
  2. Pencere içinde `serves` ilişkisiyle kayıttaki iş yüklerinden birine bağlıysa eşleşir.
- Aynı adın iki cluster'da koşması `problems` satırından ayırt edilemiyor, çünkü tabloda cluster kolonu yok (`store.go:2900`). Bu bir boşluk (bkz. 1.6). [K]

**Olay kaydı** (`saved_views`: `page='ml-golden-case'`, `id='golden-<kısa-ad>'`, `owner_id=''`, `name='incident'`, `query_string` = JSON):

```json
{
  "kind": "incident",
  "entity": {"cluster": "cl-a", "namespace": "payments", "service": "orders-api",
             "entityId": "svc:orders-api", "workloads": ["wl:cl-a/payments/Deployment/orders-api"]},
  "start": "2026-11-03T10:12:00+03:00",          // ilk sapma (bilinmiyorsa kaynak zamanı)
  "end":   "2026-11-03T10:55:00+03:00",
  "symptom": "error_rate",                       // error_rate | latency | traffic_drop | silence |
                                                 // certain:oom | certain:exception_storm |
                                                 // certain:fatal_exception | certain:oracle_burst
  "rootCause": {"category": "deploy", "culprit": "orders-api 4.12.0", "text": "..."},
  "source": {"kind": "incident_case", "ref": "p:<problemId>"},  // incident_case | rollback |
                                                 // oracle | exception_storm | manual
  "expect": {"problem": true, "maxProblems": 1},
  "addedBy": "user-1", "addedAt": "...", "confirmedBy": "user-1"
}
```

- Kök neden kategorisi `incident_cases.category` kümesindendir (`internal/cosrelearn/cases.go:52`). Yeni sözlük yok. [K]

**Sakin pencere kaydı** (aynı sayfa, `name='quiet'`):

```json
{"kind": "quiet", "team": "team-a", "services": ["svc:orders-api", "svc:ledger"],
 "start": "2026-11-10T00:00:00+03:00", "end": "2026-11-17T00:00:00+03:00",
 "reason": "operatör: bu hafta olay yok", "addedBy": "user-1", "confirmedBy": "user-1"}
```

- Takım `service_metadata.owner_team` alanından gelir. Okuma anı zenginleştirmesi `EnrichProblemsWithTeams` yapar (`chstore/problem.go:867-898`; tablo `store.go:2496`). [K]
- Kayıtta takımın o anki servis listesi dondurulur, böylece sonradan ekip değişse de pencere aynı kalır.

**Koşu kaydı** (`page='ml-golden-run'`, `id='ml-golden-run-<runId>'`):
- İçerik: vaka başına sonuç, toplam ölçüler ve kod sürümü.
- `sre-eval` sözleşmesiyle aynı: ≤ 64 KB, 180 gün saklama.

**Neden yeni tablo değil.**
- Proje kuralı "kayıtlı durum için tek CH tablosu: `saved_views(page=…)`" (CLAUDE.md değişmez #5). Altın set kayıtlı durumdur.
- Hacmi küçük: yüzlerce kayıt.
- Emsalleri aynı yerde: `problem-verdict`, `problem-rootcause`, `sre-eval`.
- Yapılacak tek ekleme: iki sayfa adının sistem sayfaları listesine yazılması, yoksa genel `/api/views` bu sayfalara yazabilir (`chstore/saved_view.go:41-79`). [K]
- Seri verisi bu tabloya girmez (bkz. 1.5).

### 1.3 Aday kaynakları — sorgular (operatör prod'da koşar; burada koşulmadı)

Hepsi salt okunur, zaman sınırlı, LIMIT ve `max_execution_time` taşır. `FINAL` yalnız ReplacingMergeTree
durum tablolarında kullanılır. Yedi sorgunun yedisi de yerelde `EXPLAIN PLAN` ile denetlendi. **[S]**

**Q1 — Onaylı Problem'ler (kök neden etiketi).**
- "Onaylı" = `incident_cases.status='confirmed'`: `final_cause` ve `category` dolu, `confirmed_by`/`confirmed_at` set (`chstore/incident_cases.go:17-18`).
- Kök neden etiketi ayrı tablo değil, onaylı vaka satırıdır (`api/problem_rootcause_label.go:11-19`).
- `category='noise'` olan onaylı vakalar sakin pencere ve yanlış açılış adayıdır; sona sıralanır. [K]

```sql
SELECT c.case_id, c.source_id AS problem_id, c.service, c.opened_at, c.closed_at,
       c.category, c.final_cause, c.confirmed_by, c.confirmed_at,
       p.rule_id, p.started_at, p.resolved_at
FROM incident_cases AS c FINAL
LEFT JOIN (SELECT id, rule_id, started_at, resolved_at FROM problems FINAL
           WHERE started_at >= now64(9) - INTERVAL 120 DAY) AS p ON p.id = c.source_id
WHERE c.status = 'confirmed' AND c.source = 'problem'
  AND c.opened_at >= now64(3) - INTERVAL 120 DAY
ORDER BY c.category = 'noise', c.opened_at DESC
LIMIT 500 SETTINGS max_execution_time = 30;
```

**Q2 — Açık "Gerçek problem / Problem değil" kararları.**
- Karar imza düzeyindedir: `p:<ruleId>|<svc>` imzası o kural ve servisin geçmiş ve gelecek bütün Problem'lerini kapsar (`chstore/problem_verdict.go:21-49`, `frontend/src/lib/problemVerdict.ts:38-50`). [K]
- Bu yüzden **etiket değil, aday** sayılır; her satırı insan altın sete taşır.
- Oy (👍/👎) hiç okunmaz (operatör kuralı: oylar zayıf sinyal).

```sql
WITH v AS (
  SELECT name AS verdict, JSONExtractString(query_string, 'signature') AS sig,
         JSONExtractString(query_string, 'by') AS by
  FROM saved_views FINAL
  WHERE page = 'problem-verdict' AND name IN ('real', 'noise')
    AND startsWith(JSONExtractString(query_string, 'signature'), 'p:')
  LIMIT 5000)
SELECT v.verdict, v.by, p.id, p.rule_id, p.service, p.started_at, p.resolved_at
FROM problems AS p FINAL
INNER JOIN v ON v.sig = concat('p:', p.rule_id, '|', p.service)
WHERE p.started_at >= now64(9) - INTERVAL 120 DAY
ORDER BY p.started_at DESC
LIMIT 2000 SETTINGS max_execution_time = 30;
```

**Q3 — Rollback ile biten rollout'lar.**
- Rollout v2'de rollback iki satırla görünür: yeni olayda `change_type='rollback'`, geri alınan olayda `status='rolled_back'` (`internal/rollout/v2detect.go:669,784-830`; tablo `rollout_v2_schema.go:111`, TTL 180 g). [K]
- Rollout tablosunda servis kolonu yok. Servis listesi `workload_revision_activity_1m` görünümünden gelir (`chstore/rollout_services.go:66-81`). [K]
- Olay penceresi: başlangıç = `rolled_back` satırının `started_at`'i, bitiş = rollback satırının `finished_at`'i.
- v1 tablosu `workload_rollouts` (`status='rolled_back'`) yedek kaynaktır.

```sql
SELECT r.cluster_id, r.namespace, r.workload, r.change_type, r.status,
       r.started_at, r.finished_at, r.old_revision, r.new_revision, a.services
FROM rollout_events AS r FINAL
LEFT JOIN (SELECT cluster, k8s_namespace, workload, groupUniqArray(10)(service_name) AS services
           FROM workload_revision_activity_1m
           WHERE bucket >= now() - INTERVAL 90 DAY
           GROUP BY cluster, k8s_namespace, workload) AS a
  ON a.cluster = r.cluster_id AND a.k8s_namespace = r.namespace AND a.workload = r.workload
WHERE (r.change_type = 'rollback' OR r.status = 'rolled_back')
  AND r.started_at >= now64(3) - INTERVAL 90 DAY
ORDER BY r.cluster_id, r.namespace, r.workload, r.started_at
LIMIT 1000 SETTINGS max_execution_time = 30;
```

**Q4 — Oracle hata kodu patlamaları (ham satır).**
- Kaynak `oracle_error_log` (`chstore/oracle_error_log.go:93`, TTL 30 g, poller yazar).
- `FINAL` kullanılmaz: 30 günlük RMT birleştirmesi pahalı, yinelenen satır sayımı en çok hafifçe şişirir. [V]
- Medyan yalnız satırı olan kovalardan hesaplanır, boş kovalar sayılmaz; bu yüzden eşik muhafazakâr kalır.
- **Influx bugün yok:** v0.10.606'da söküldü (`docs/audit/davis-parity-audit.md:30,168`). `anomaly/external.go:1-16` başlığındaki "Influx" sözcükleri bayattır. [K]

```sql
SELECT source_id, error_code, b, n, med
FROM (SELECT source_id, error_code, b, n,
             median(n) OVER (PARTITION BY source_id, error_code) AS med
      FROM (SELECT source_id, error_code, toStartOfFiveMinutes(time) AS b, count() AS n
            FROM oracle_error_log
            WHERE time >= now64(9) - INTERVAL 30 DAY AND error_code != ''
            GROUP BY source_id, error_code, b))
WHERE n >= 50 AND n >= 10 * med
ORDER BY n DESC
LIMIT 500 SETTINGS max_execution_time = 60;
```

**Q5 — Bugün kesin olay ailesinin açtığı Problem'ler (fırtına, exception, Oracle).**
- `problems` tablosunda TTL yok; kapanan satır `started_at` ve `resolved_at` ile kalır (`store.go:2900`, `purge.go:71-76`). [K]

```sql
SELECT id, rule_id, service, severity, value, threshold, started_at, resolved_at, description
FROM problems FINAL
WHERE started_at >= now64(9) - INTERVAL 120 DAY
  AND (rule_id = 'exception-storm' OR startsWith(rule_id, 'exception:')
       OR startsWith(rule_id, 'anomaly:ext:') OR startsWith(rule_id, 'anomaly-cluster:ext:'))
ORDER BY started_at DESC
LIMIT 1000 SETTINGS max_execution_time = 30;
```

**Q6 — Exception fırtınası adayları (Problem açılmamış olanlar dahil).**
- Fırtına kuralını yeniden kurar: ≥ 5 serviste yeni grup, 10 dk içinde (`chstore/exception_triage.go:139-140`; sorgu emsali `chstore/exception_storm.go:34-46`). Oracle grupları hariç tutulur. [K]

```sql
SELECT toStartOfTenMinutes(first_seen) AS w, uniqExact(service) AS services,
       count() AS new_groups, groupUniqArray(8)(service) AS sample
FROM exception_groups FINAL
WHERE first_seen >= now64(9) - INTERVAL 120 DAY AND state != 'ignored'
  AND NOT startsWith(fingerprint, 'ora:')
GROUP BY w HAVING services >= 5
ORDER BY w DESC
LIMIT 500 SETTINGS max_execution_time = 30;
```

**Q7 — Sakin pencere adayı: takım × gün Problem sayısı.** Problem'i 0 olan ya da az olan takım-günleri
operatöre "sakin mi?" diye sunulur.
- Uyarı: servis kolu satırları kimlik çakışması yüzünden eksik sayılır (bkz. 1.6/2).

```sql
SELECT m.owner_team AS team, toDate(p.started_at) AS day, count() AS problems,
       groupUniqArray(5)(p.rule_id) AS rules
FROM problems AS p FINAL
LEFT JOIN (SELECT service, owner_team FROM service_metadata FINAL LIMIT 100000) AS m
  ON m.service = p.service
WHERE p.started_at >= now64(9) - INTERVAL 30 DAY
GROUP BY team, day
ORDER BY day DESC, problems DESC
LIMIT 5000 SETTINGS max_execution_time = 30;
```

### 1.4 Operatörün elle olay eklemesi — en kısa yol

**Bugün ne var:**
- Problem'i olan bir olay için tek çağrı yetiyor: `PUT /api/problems/{id}/root-cause-label` (editor+, audit'li) onaylı `incident_cases` satırı yazar (`api/problem_rootcause_label.go:50-52,127,214-220`). Q1 bu satırı sonraki gece işinde altın set adayı olarak getirir. [K]
- **Kaçırılan olay için hiçbir yol yok.** Olayın Problem'i olmadığı için etiket ucu 404 döner, yoktan Problem ya da incident yaratan bir uç da yok (`/api/incidents*` 410, `api/incidents_gone.go:38-39`). [K]

**En küçük ekleme** (tarif; kurulmaz):
- Tek dosya `internal/api/ml_golden.go`:
  - `GET /api/ml/golden` (viewer okur)
  - `PUT /api/ml/golden/{id}` ve `DELETE /api/ml/golden/{id}` (admin, `s.audit(r, "ml.golden.put", …)`)
  - gövde 1.2'deki JSON
- `page='ml-golden-case'` sistem sayfaları listesine eklenir.
- Arayüz: Settings › ML altında bir form (servis seçici sunucu aramalı; cluster/namespace iş yükü seçiminden gelir) ve Problem detayına "Altın sete ekle" düğmesi. Düğme formu Problem ve kök neden etiketinden doldurur.
- Kaçırılan olayda operatör servisi, başlangıcı, bitişi ve semptomu kendisi girer.

### 1.5 Ölçüler

Her ölçü altın setin olay ve sakin pencere kayıtları üzerinden, "bugünkü kapı" ve "aday kapı" için ayrı hesaplanır.

**M1 — Yakalama oranı.**
- Tanım: olay "yakalandı" sayılırsa = [start − 15 dk, end] içinde açılmış ve olay kimliğiyle eşleşen (1.2) en az bir Problem var. Problem sahip de olabilir, olay çizelgesindeki üye de (`ProblemEvents`, `chstore/problem_events.go`, merge-spec D4).
- Oran = yakalanan / toplam. Semptom türü başına ve kesin olay türü başına ayrı raporlanır.
- Var olan veri: `problems`, `ml_events.evidence.problem_id`.
- Eksik veri:
  - Problem açılış anı (M2'ye bakın).
  - Cluster/namespace (yalnız ad + `serves` ile eşleşir).

**M2 — Tespit gecikmesi (ilk sapmadan).**
- Tanım: gecikme = açılış anı − ilk sapma anı.
- "İlk sapma" semptom başına işlemsel olarak tanımlanır; hepsi 1 dk VM örneğiyle ve etki kuralının kendi eşiğiyle (`ml/impact_rule.go:60-73`):
  - Hata oranı: oranın ilk kez max(%1, taban × 1,5, taban + 1 puan) eşiğini aştığı ve yükün ≥ 10 istek/dk olduğu dakika. Kural 5'te 3 ihlal ister; ilk sapma ise ilk ihlaldir.
  - Yavaşlama: medyanın taban × 1,5 ve taban + 100 ms'yi, ya da p90'ın 2 × taban ve taban + 1 sn'yi ilk aştığı dakika (aynı yük şartıyla).
  - Trafik düşüşü / sessizlik: istek sayısının tabanın belirli bir payının altına ilk düştüğü dakika ya da 0 olduğu dakika. **Bugün VM tabanlı bir düşüş ya da sessizlik eşiği yok**: `throughput_drop` z/MAD ile çalışıyor ve hiçbir kolda sayılmıyor; `service_silent` log tabanlı. Eşik açık kararla belirlenir.
  - Kesin olay: olayın kendi damgası (KSM OOMKilled zamanı, fırtınanın ilk yeni grubu `first_seen`, Oracle patlamasının ilk kovası).
- Açılış anı:
  - Canlı Problem'de **saklanmıyor**. `problems.started_at` olayın başlangıcıdır; ML servis kolunda `in.StartedAt` (`ml/svc_shadow.go:372-379`). Satırda `created_at` yok (`store.go:2900`). [K]
  - Vekil: `notification_log.sent_at` (`related_kind='problem'`, TTL 90 g, `store.go:4134-4150`). Yalnız bildirim giden Problem'ler için; Oracle gölge kipi ve P3'ler eksik kalır.
  - Gece replay'inde açılış anı belirleyicidir: kararın ilk kez "açar" dediği tik.
- Eksik veri:
  - Etki kuralının 1 dk örnekleri saklanmıyor (`impact_replay.go:9-12`). `ml_series` 5 dk kovalı ve 28 g TTL'li. Gecikme ölçümü ya VM'den yeniden okumayı ya da dondurulmuş seri dosyasını ister.

**M3 — Sakin pencerede takım başına günlük Problem.**
- Tanım: sakin pencerede, takımın dondurulmuş servis kümesinde açılan (replay'de "açar" denen) farklı Problem sayısı ÷ (takım × gün).
- Liste görünümüyle aynı sayılır: servis içi birleşme ve graf birleşmesi katlandıktan sonraki satır. Ham satır sayısı da yanında verilir.
- Hedef değer operatörde (Soru 2).
- Eksik veri:
  - Servis kolu Problem kimliği servis başına sabit (`anomaly-auto:svc:<svc>`, `ml/svc_shadow.go:375`). `problems` `ORDER BY id` RMT olduğu için (`store.go:2900`) aynı servisin ikinci açılışı birincinin satırını ezer. **Geçmiş Problem sayımı bu ailede eksik çıkar.** [K kimlik ve motor; ezme davranışı çalışma anında denenmedi → V]
  - Aynı nedenle `CaseIDFor` = `p:`+problemId (`chstore/incident_cases.go:66`) aynı servisin iki ayrı olayı için aynı vaka kimliğini üretir. [V]
  - Grup kolu kimliği ilk olay kimliğini taşıdığı için eşsizdir (`ml/group_live.go:192`).

**M4 — Aynı olay için açılan Problem adedi.**
- Tanım: olay penceresiyle örtüşen ve olay kimliğiyle ya da kayıttaki ilgili servislerle eşleşen, liste görünümünde ayrı satır olan Problem sayısı. Hedef 1.
- Ham satır sayısı (birleşme öncesi) ayrıca raporlanır; birleşmenin kazancı böylece görünür.
- Var olan veri: merge-spec D1–D4 üye olayları, `problem_graph_shadow` blobu (küme üyeleri).
- Eksik veri: M3'teki kimlik ezmesi.

### 1.6 Bugün ölçülemeyenler (özet)

| # | Eksik | Neden | Kapatma yolu (tarif) |
|---|---|---|---|
| E1 | Problem açılış anı | `problems`'ta yok, `started_at` olay başlangıcı | gece replay'i belirler; canlı için üye olayı gibi ml_events damgası ya da açık karar |
| E2 | Servis kolu Problem geçmişi | sabit kimlik + RMT | kimliğe bölüm başlangıcı eklenmesi (grup kolu emsali) — davranış değişikliği, ayrı onay |
| E3 | cluster/namespace | `problems`, `ml_events` kolonsuz; `svc:` global | `serves` ilişkisiyle pencere içi eşleşme; aynı adın iki cluster'ı ayrışmaz |
| E4 | Grup kapısının geçmişte koşulması | yürütücü yok (`ReplayGroupDecisions` çağıransız) | dilim 1'in asıl işi |
| E5 | 28 günden eski kaçırılan olayın serisi | `ml_series` 28 g, 1 dk örnek saklanmıyor | VM'den yeniden okuma (VM saklama süresi prod'da bilinmiyor [V]) ya da prod içinde dondurulmuş dosya |
| E6 | Oracle ham satır > 30 g | `oracle_error_log` TTL 30 g | olay kaydına sayılar dondurulur |
| E7 | "Olay yoktu" etiketi | sakin pencere kaydı bugün yok | 1.2 kaydı |
| E8 | Ekip | `owner_team` boş olabilir | boş ekip "atanmamış" ayrı satır |

### 1.7 Kapı: CI mi, gece işi mi

**Öneri: iki katman.**
1. **CI (GitHub, her PR):**
   - Sentetik, dondurulmuş fikstür seti koşar: olay başına 1 dk VM örnek serisi + `ml_series` tabanı + beklenen sonuç.
   - Adlar sentetik; şekil prod olaylarından kopyalanır, veri prod'dan çıkmaz.
   - Koşan işlevler: `impactDecide`, `decideSvcShadow`, `DecideGroupPromotion`, rol kuralı, tik tik.
   - Bugünkü `internal/ml/testdata/replay` + `make eval-ml` düzenine oturur (`Makefile:89-92`).
   - Gerileme = önceki altın sonuç dosyasından fark → kırmızı. Süre: `go test` içinde < 30 sn [V].
   - Gerekçe: banka ortamı hava boşluklu. CI prod'u göremez, prod verisini GitHub'a taşımak da veri sızıntısıdır. CI yalnız karar mantığındaki gerilemeyi yakalar.
2. **Prod gece işi (worker lideri, 03:00 taban işinden sonra):**
   - Gerçek altın seti koşar: seri kaynağı `ml_series` (≤ 28 g) ve VM `query_range` (daha eskiler, VM saklaması yettiği kadar).
   - Koşu `ml-golden-run` olarak yazılır ve önceki koşuyla karşılaştırılır.
   - Gerileme:
     - M1'de herhangi bir olayın kaybı
     - M2 medyanında > 2 dk artış
     - M3'te > %20 artış
     - M4'te herhangi bir olayda artış
     - Bunlardan biri olursa koşu kırmızı olur ve Settings › ML'de görünür.
   - Kırmızıyı Problem'e çevirmek yeni bir kural öneki (`self-*`) demektir; açık karar.
   - Prod dağıtımı sürümden sonra yapıldığı için gece kapısı sürümü engelleyemez. Engelleme sırası: dağıtımdan sonra elle `POST /api/ml/golden/run` (tarif). Kırmızıysa geri alma.

**Bütçe** [V; prod ölçülmedi, prod hacmi yerelin ~7000 katı; yerel süre ölçüsü anlamsız olduğundan koşulmadı].
Varsayım: 50 olay + 20 sakin takım-haftası.

| Kalem | Kaynak | Sorgu | Not |
|---|---|---|---|
| Olay başına etki kuralı replay'i | `ml_series` | 2 aile sorgusu | `impact_replay` bütçesi 10 sn |
| Olay başına judgeMM backtest | `ml_series` | 3 sinyal × 2 | backtest 20 sn sınırı |
| Olay başına ml_events okuması (grup/svc yeniden karar) | `ml_events` | 1 | 30 g TTL |
| Sakin takım-haftası | `ml_series`, 200 özne tavanı | 2 | `impact_replay` sınırı |
| 28 g'den eski olay, VM'den | VM `query_range`, ±2 sa, 60 sn adım | ~6 (count, err, `_bucket` medyan/p90) | ≤ 300 toplam |

- Toplam yaklaşık 50 × 9 + 20 × 2 ≈ **500 CH sorgusu** ve **≤ 300 VM aralık sorgusu**.
- En kötü durum, sorgu başına 10 sn tavanla seri koşu: ~85 dk. Beklenen (sorgu başına 0,5–2 sn, 2 eşzamanlı): **5–15 dk**.
- Karşılaştırma: canlı ML tiki 300 sn'de bir, 500 servisi 50'lik partilerle VM'den okuyor (`ml_settings.go:343`). Gece işinin VM yükü canlının bir saatinden azdır [V].
- Gece işi `vmBackoff`'a tabi olmalı; canlı tiki aç bırakmaz.

### 1.8 `precision-program.md` ile ilişki

- Altın setin olayları, paralel belgedeki insan etiketlerinden gelir: onaylı `incident_cases`, kök neden etiketi, imza düzeyindeki Problem kararları, ML olay kararları.
- Bu belge etiket sözlüğünü ve dedektör başına isabeti yeniden tanımlamaz; o tanımlar oradan alınır.
- İki fark var:
  - Altın set yalnız onaylı ve insan tarafından taşınmış kayıt tutar; isabet karnesi daha geniş etiket kümesini kullanabilir.
  - Altın set kaçırılan olayları ve sakin pencereleri de taşır. Bunlar isabet karnesinin göremediği iki şeydir: geri çağırma ve gürültü.
- Otomatik gölge kuralı (karne) ile altın set kapısı aynı sonucu ölçmez. Karne "bu dedektör çoğunlukla yanlış mı?" sorusunu, altın set "bu değişiklik bilinen olayları kaçırttı ya da sakin günde Problem açtı mı?" sorusunu yanıtlar.

---

## BÖLÜM 2 — Rol modeli

### 2.1 Atama tablosu

Kısaltma "Bugün" sütununda: S/T/G/D kolları (BÖLÜM 0). Davis karşılığı davis-gap ve eski-gap'ten alındı; girdi belgelerde karşılığı yoksa "—".

| Yol | Rol | Bugün | Gerekçe | Davis karşılığı |
|---|---|---|---|---|
| svc_error_rate | **SEMPTOM** (hata oranı) | S | VM RED, örneklemesiz, üçlü eşik zaten var (göreli + mutlak + yük) | failure rate increase (eski-gap #7) |
| svc_response_time | **SEMPTOM** (yavaşlama) | S | VM `_bucket` medyan + p90, üçlü eşik var | response time degradation (eski-gap #12) |
| endpoint_impact | **SEMPTOM** (uç nokta düzeyi, servise katılır) | S ikincil | aynı kural, rota düzeyinde; servis olayı varsa üye | rota alt özneleri (davis-gap #1–2) |
| svc_breadth | KANIT → sil adayı | S | op_latency'den türev; svc_response_time aynı semptomu VM'den ölçüyor | svc-slowdown karşılığı (eski-gap #12) |
| op_latency | KANIT (rota yavaş payı) | T + G | semptom rolü endpoint_impact'te; tek başına açması kalkar | davis-gap #1 |
| op_error_rate | sil | T + G (kapalı) | endpoint_impact hata ölçüsü aynı işi yapıyor | davis-gap #2 |
| throughput_drop | brif: **SEMPTOM** (trafik düşüşü) — **çelişki 1** | D | bugün yük dedektörü, hiçbir kolda yok | davis-gap #3 (Davis de terfi ettirmiyordu) |
| service_silent (log) | brif: **SEMPTOM** (sessizlik) — çelişki 1; kaynak log, metrik değil | D | VM tabanlı sessizlik **bugün yok** | eski-gap #8 |
| dependency_latency / dependency_error_rate | KANIT (bağımlılık gecikmesi) | T+G / G | çağıranın semptomuna neden adayı | davis-gap #4–7 |
| metric_kafka consumer_lag | brif: **KANIT** — **çelişki 2** | T (etki kuralı) | bugün tek başına açıyor, P1/P3 dalları var | davis-gap #8 |
| metric_kafka diğer / metric_pool | KANIT (havuz) | G | — | — |
| metric_jvm | KANIT (JVM/GC) | G | kaynak Thanos (2.5) | — |
| metric_http_client | KANIT (bağımlılık) | G | dependency_* ile örtüşür; sil adayı | — |
| metric_k8s | KANIT (pod/küme) | G | — | — |
| trace_pod_outlier, log pod_outlier | KANIT (pod aykırılığı) | D / G | — | — |
| new_template, rare_template, template_spike | KANIT (yeni/nadir log şablonu) | D / G / G | — | eski-gap #2 |
| rare_error_code | KANIT | G | — | — |
| change_point (log), trace_change_point | KANIT (change point) | T + G | — | davis-gap #14 |
| log_volume | KANIT (log hacmi) | D | — | — |
| log error_rate | KANIT | T + G | semptom yalnız metrikten (brif); log hata sayısı kanıt | — |
| trace_error_count | sil | T + G (kapalı) | span'den sayım (BÖLÜM 3); semptom metrik kaynağa taşınınca gereksiz | eski-gap #3 |
| rollout (reconciler, K-ROLLOUT) | KANIT — **çelişki 3** (hafif) | Problem açmaz | bugün de yalnız köken etiketi | — |
| pod_oom | **KESİN OLAY** (OOMKill) | S ikincil | baseline'sız, KSM'den | — |
| exception-storm | **KESİN OLAY** | kapısız | baseline'sız: ≥ 5 serviste yeni grup / 10 dk | eski-gap #9 (ML'de yok) |
| exception:fatal-infrastructure | **KESİN OLAY** (fatal exception) | kapısız | — | — |
| exception:shared-dependency | **KESİN OLAY** (fırtınanın bağımlılık kolu) — öneri | kapısız | brifte yok; fırtınayla aynı aile | — |
| Oracle `anomaly:ext:*` (+ `anomaly-cluster:ext:*`) | **KESİN OLAY** — **çelişki 4**: bugün MAD tabanlı, baseline'sız değil | kapısız (gölge de açar) | `external.go:1-9`: `evaluateAnomaly` (MAD, dwell, z) | eski-gap #10 (ML'de yok) |
| `anomaly:ext-down`, `anomaly:ext-cap` | rol dışı (kaynak sağlığı) | açar | Problem değil, platform durumu | — |
| Influx hata kodu patlaması | **bugün yok** | — | v0.10.606'da söküldü | — |
| svc_telemetry_volume, (svc_volume_spike yazıcısız) | rol dışı (platform) / sil | D | operatör kararıyla anomali değil | — |
| slo:<id> | SLO'lu serviste **SEMPTOM** (2.4) — **çelişki 6** | kapalı | burn çok pencereli; kaynak span MV | — |
| metric_anomaly, error_ramp, anomaly-cluster (metrik) | sil | kapalı | svc_error_rate + graf birleşmesi karşılıyor | eski-gap #7, #11 |
| svc-slowdown | sil | kapalı | svc_response_time | eski-gap #12 |
| db-health, db-capacity, db-slow-stmt | sil (KANIT işi dependency_* / metric_pool'da) | kapalı | span MV; prod'da kaynak boş (R-audit N6) | davis-gap #4–5 |
| runtime:jvm-gc, dotnet, heap band | sil | kapalı / hiç açmaz | metric_jvm KANIT'ı | — |
| subjects (Davis F3), calendar drop, promoteStrongAnomalies, anomaly_promotion | sil | kapalı | ML karşılıkları gemide (davis-gap S1–S4 ile) | davis-gap tümü |
| Elastic ML poller | sil | yalnız anomaly_events | ikinci motor | eski-gap #13 |
| Problem graf birleşmesi, servis içi birleşme | rol dışı (birleştirme katmanı) | — | dedektör değil; M4'ü besler | merge-spec |
| alarm kuralları, watcher, monitor, self-* | **kapsam dışı** | — | brif | — |

Brifin listelerinde rolü olmayan, bugün var olan yollar ve önerilen rolleri tabloda: svc_breadth,
op_error_rate, dependency_error_rate, metric_http_client, metric_k8s, rare_error_code, log error_rate,
trace_error_count, `exception:shared-dependency`, `ext-down`/`ext-cap`, telemetri hacmi, SLO burn,
graf birleşmesi.

### 2.2 Yeni kapı kuralı ile bugünkü `group_gate` — kol kol fark

Kural: Problem yalnız **≥ 1 SEMPTOM ya da KESİN OLAY** ile açılır. KANIT olaylar açık Problem'e neden adayı
olarak eklenir. Bugünkü "≥ 2 farklı sinyal" kolu (`group_gate.go:8-10,50,185,210`) ve tek sinyal kolu
(`:185-209,335`) bu kurala göre yeniden yazılır, servis kolu (`svc_shadow.go:121`) olduğu gibi kalır.

| Bugünkü kol | Bugün kim açıyor | Rol kuralında | Sonuç |
|---|---|---|---|
| G (≥ 2 farklı sinyal) | herhangi iki sinyal ailesinden (trace / log / metric) dedektör; svc olayları da trace üyesi | yalnız KANIT'tan oluşan grup açmaz; grupta SEMPTOM varsa onu zaten S açıyor | **Açılmayacaklar:** "dependency_latency + rare_template", "metric_jvm + template_spike" gibi kanıt-kanıt grupları |
| T (tek sinyal) | log error_rate, trace_error_count, change_point(error), op_error_rate, op_latency, dependency_latency, trace_change_point, consumer lag | hepsi KANIT ya da sil | **Açılmayacaklar:** tek bir op_latency / dependency_latency / change point / consumer lag Problem'i (lag için çelişki 2) |
| S (servis) | svc_error_rate, svc_response_time, svc_breadth, endpoint_impact, pod_oom | svc_breadth dışında aynı | svc_breadth tek başına açmaz; diğerleri değişmez |
| kapısız | fırtına, fatal, shared, Oracle | KESİN OLAY | değişmez (Oracle için çelişki 4) |
| D (hiçbir kol) | — | throughput_drop / sessizlik SEMPTOM olursa | **Yeni açılacaklar:** trafik düşüşü ve sessizlik Problem'leri (çelişki 1) |
| kapalı | slo:* | SLO'lu serviste semptom | **Yeni açılacaklar:** SLO'lu servislerde burn Problem'i; aynı serviste anomali semptomu susar (2.4) |

**Farkın altın sette ölçülmesi** (tamamlanan cümle):
- Aynı yürütücü, aynı girdiyle (aynı seri, aynı tik dizisi) iki kararı yan yana koşar:
  - bugünkü kapı: `DecideGroupPromotion` + `decideSvcShadow`
  - rol kapısı: aynı girdi + rol tablosu
- Fark raporu vaka başına dört sütun taşır:
  - Yakalanma: bugün / aday.
  - Gecikme farkı (dk).
  - Problem adedi farkı.
  - Sakin pencerede açılan fazla ya da eksik Problem; takım-gün başına.
- Yalnız iki tür satır ayrıca listelenir:
  - "bugün yakalanıyor, adayda kaçıyor" (geri çağırma kaybı)
  - "bugün sakinde açılıyor, adayda açılmıyor" (gürültü kazancı)
- Son 30 gün için replay bile gerekmez:
  - `ml_events.evidence` her tikte `groupShadow` (`group_gate.go:453`) ve `svcShadow` (`svc_shadow.go:189`) kararını taşıyor.
  - Rol kuralı bu satırlardan SAF olarak yeniden hesaplanabilir. Dilim 3'ün gölgesi budur.
  - Eksik kalan, olayın tik içindeki ara durumları: kanıt her tik üzerine yazılıyor, yalnız son karar kalıyor. Gecikme farkı bu yüzden yalnız replay'den çıkar.
- Bu belge hiçbir davranışı değiştirmez.

### 2.3 Semptom tasarımı

**Kaynak yalnız metrik (VM RED).**
- Servis kolunun üç semptomu (svc_error_rate, svc_response_time, endpoint_impact) zaten yalnız VM'den okuyor ve CH'ye düşmüyor (`ml/svc_error_rate.go:6-11`). [K]
- Span'den oran hesaplayan yolların tam listesi BÖLÜM 3'te.

**Üçlü eşik.**
- Bugün etki kuralında var (`ml/impact_rule.go:60-65`). Her ölçüde göreli + mutlak eşik ve yük kapısı birlikte isteniyor:
  - hata oranı: max(%1, taban × 1,5, taban + 1 puan), yük ≥ 10 istek/dk
  - medyan: taban × 1,5 ve +100 ms
  - p90: 2 × taban ve +1 sn
- Yayın kapılarının üç sabiti (100 çağrı, 25 ms, görünür sapma; `publish_gate.go:39-43`) etki kuralı olaylarına uygulanmıyor (`publish_gate_enforce.go:57`). Yalnız T/G kolundaki op_latency ve benzerlerini eliyor. [K]
- "Etkilenen istek sayısı" üçüncü eşik olarak ayrı yazılmamış. Yük kapısı (≥ 10 istek/dk) ve `minAffectedRequests` (op_latency, `ml_settings.go:217`) bu işi yapıyor. Kesin hali "eşik × istek ≥ N etkilenen" — açık karar.
- p50 / p90 / p99 anahtarları:
  - ML'de ayar anahtarı yok. Ölçü anahtarları `svc_p50`/`svc_p90` (`svc_response_time.go:77-78`) ve `ep_p50`/`ep_p90` (`endpoint_impact.go:84-85`). op_latency p95 + yavaş pay kullanıyor (`trace_red.go:44`).
  - p99 yalnız eski yollarda: `anomaly.go` `p99_ms`, svc-slowdown.
- **Tek "yavaşlama"ya (yavaş istek oranı) iner mi:**
  - VM histogramıyla mümkün. `histogram_share(le, …)` op_latency'de zaten kullanılıyor (`trace_red.go:276-280`, `op_latency_record.go:54`). Eşik `slowRequestSec` (varsayılan 1 sn, 0,05–60; `ml_settings.go:215,369`). Kova `le` değeri birimden türetiliyor (`ml/red_selector.go:99-104`).
  - Kısıt: `histogram_share` kova içinde doğrusal ara değer verir. Eşik gerçek bir kova sınırı değilse pay yaklaşık olur. OTel varsayılan HTTP kovalarında 1 sn var, prod kovaları doğrulanmadı [V].
  - Öneri (değerlendirme): servis başına tek ölçü "yavaş istek payı", eşik servisin tabanının p90'ına en yakın kova sınırı. Bu yeni bir ölçü değil, mevcut sorgu biçimi.

**Servis arketipi.**
- `ml/profile.go` dört sınıf üretiyor (`:12-20`), haftalık, histerezisli ve elle sabitlenebilir (`api/ml_profiles.go`):
  - batch
  - low_traffic: günlük medyan < 2000 çağrı ya da mesai saatlerinin > %50'si boş
  - business_hours
  - always_on
- **Ama hiçbir dedektöre girmiyor** (`profile.go:34`, `api/ml_profiles.go:15`). Yalnız açıklama kartında etiket olarak kullanılıyor (`ml/explain_card.go:268`). [K]
- Seyrek serviste sayım tabanlı test:
  - Etki kuralının yük kapısı (≥ 10 istek/dk) low_traffic servisleri fiilen kör bırakıyor. Günde 2000 çağrı dakikada ~1,4 eder [V hesap].
  - Taban 0 iken tek sinyal kolunda "sayım ≥ 20" kuralı var (`group_gate.go:225`), ama o kol rol modelinde kalkıyor.
  - Seyrek servis için sayım tabanlı (binom / Poisson) semptom testi **bugün yok**.

**Baseline hijyeni.**

| Konu | Bugün | Kaynak |
|---|---|---|
| Olay penceresini öğrenmeme | **yok.** Taban medyan/MAD ile dayanıklı ama olay kovaları dışlanmıyor; etiketli dışlama v0.10.2269'da kaldırıldı | `ml/quality.go:9-12`, `ml/baseline_obs.go:24`, `chstore/ml_series.go:57-80` |
| Uzun sapma | rejim kuralı: `regimeHours` sonra sapma yeni normal sayılır (bastırma) | `quality.go:391-398` |
| Rollout sonrası yeniden kurma | **yok** (taban kodunda rollout okuması yok) | grep [V] |
| Tatil / yarım gün | var: sabit TR tatilleri + elle günler, yarım gün 13:00 | `chstore/anomaly_calendar.go:33-46`, `ml/log_red.go:198-219` |
| Ay sonu | var, otomatik kural varsayılan **kapalı** (`MonthEndAuto`) | `anomaly_calendar.go:16-17,33-34` |
| Maaş günü | **yok** | — |
| Saat dilimi | boş = UTC | `anomaly_calendar.go:37-39` |
| Nadir gün sınıfı | sınıf eşleşmesi < 3 günse tüm günlere düşer, `baselineKind=recent` | `log_red.go:183-195`, `ml/baseline_kind.go` |

- Kod içi bayat yorum: `chstore/ml_series.go:325` "hijyen dışlaması uygulanmış". v0.10.2269'dan beri dışlama yok.

### 2.4 SLO burn

- `evaluator/slo_burn.go` çok pencereli burn yapıyor (Workbook değerleri):
  - critical: 1 sa / 6 sa, 14,4 / 6
  - warning: 6 sa / 24 sa, 6 / 3
  - Kaynak `chstore.BurnPolicies` (pin `slo_burn_policy_test.go:13-22`). [K]
- Problem üretimi operatör kararıyla kapalı: "SLO burn rate problem olmasın" (v0.10.1081, `slo_burn.go:49-54`).
- Kaynak span MV ve spanmetrics (`chstore/slo.go:207-574`). Ağırlık yalnız örnekleme kipi `on` iken uygulanıyor (BÖLÜM 3).
- Rol modeli önerisi: SLO'su olan serviste semptom = burn, anomali semptomu yalnız SLO'suz servislerde. **Çelişki 6.** Burn'ün VM kaynağına alınması dilim 2'nin işi.
- Geçmişten otomatik SLO önerisi (yalnız değerlendirme): mümkün.
  - `ml_baseline_obs` saatlik q50/q90/q99 ve çağrı toplamı tutuyor (`baseline_obs.go:7-14`). Admin geri doldurmasıyla 56 güne kadar uzanıyor (`baseline_backfill.go`).
  - Kullanılabilirlik hedefi `*|svc_err_ratio` 28 g serisinden, gecikme hedefi q90 bandından türetilebilir.
  - Engeller: (a) ML ana anahtarı ve `ml.baseline.enabled` varsayılan kapalı (`baseline_job.go:7-9`); (b) yalnız sunucu RED'i olan servisler.

### 2.5 JVM/GC kanıtının kaynağı

- ML metrik aileleri saatte bir otomatik kaynak seçiyor: VM'de varsa VM, yoksa metriği taşıyan Thanos cluster'ları (`ml/thanos_source.go`, `metric_thanos.go`; ml-next.md:38).
- Thanos'ta ifade pod düzeyinde, pod → servis `thanos.ServicePodKeys` ile; cluster × tik ≤ 12 sorgu. [K, ml-next.md atfı + dosya varlığı]
- pod_oom aynı Thanos okuyucusunu KSM için kullanıyor (`ml/pod_oom.go:5-12`).
- KANIT için kural: kanıt yalnız açık bir Problem'in servisi için ve yalnız Problem penceresi kadar okunur (tembel). Tik başına bütün filoyu taramak gerekmez.
- Bu, bugünkü sürekli metric_jvm taramasından ucuzdur. Kodlanmadı; dilim 3–4 kararı.

---

## BÖLÜM 3 — Span'den oran hesaplayan yollar

**Bağlam:**
- Collector tüm hatalı trace'leri ve başarılıların %5'ini gönderiyor.
- Ağırlıklı okuma (`weighSQL` / `WeighSQL`) yalnız `ingest.sampling.mode = on` iken devrede; kip `off` ya da `shadow` iken SQL'i değiştirmeden döndürüyor (`chstore/sampling_weighted_read.go:24,114-139`). [K]
- Varsayılan `off` [K]; prod değeri bilinmiyor [V]. Aşağıdaki "evet" satırları kip `off` iken de yanlıdır.

| Yol | Yer | Ölçü | Kaynak | Ağırlık | VM karşılığı |
|---|---|---|---|---|---|
| trace_error_count | `chstore/ml_trace_errors.go:24-101` | hata span sayısı | spanmetrics_1m | evet | `_count{status=error}` rota başına |
| Telemetri hacmi + 200 giriş kapısı | `chstore/ml_volume.go:42-83`, `ml/volume.go:56-66` | span/istek, giriş sayısı | service_summary_5m + spanmetrics_1m | **bilerek hayır** (v0.10.2475: ağırlık kapıyı ~20 kat gevşetmişti) | giriş ≈ VM `_count` |
| metric_anomaly error_rate / p99 / request_rate (+ramp) | `anomaly/anomaly.go:1273-1569`, `red_vm_select.go:63-78` | oran, p99 | önce VM, yoksa service_summary_5m | span kolunda evet | VM |
| svc-slowdown | `chstore/service_slowdown.go:175-212`, `op_p99_cache.go:328` | p99, taban p95 | service/operation_summary_5m | evet | VM histogram |
| db-health / slow-stmt / DB-kuyruk özneleri | `db_health.go:208`, `db_slow_statement.go:308`, `anomaly/subjects_ch.go:187,206` | hata %, p99 | db_*_summary_5m, messaging_summary_5m | evet | VM db/messaging istemci histogramı [V] |
| SLO burn | `chstore/slo.go:207-574` | iyi/toplam | summary MV, spanmetrics, ham span | evet | VM `_count` + `_bucket` |
| Problem kartı Etki tabanı (Calls5m, Failed5m) | `chstore/problem_standard.go:647-760`, `notify_policy_replay.go:84-140` | çağrı, hata /5 dk | service_summary_5m | evet | VM `increase(_count[5m])` |
| Kök neden korelasyonu, hata serisi | `chstore/correlate.go:326`, `rootcause_series.go:105` | oran değişimi | service_summary_5m | evet | VM |
| **Topoloji kenarları (RCA, CoSRE bağımlılık)** | `chstore/repo.go:1761-1830` (`GetServiceGraphTopN`) | kenar çağrı, hata % | topology_edges_5m | **hayır** | VM istemci [V] |
| **Etki yarıçapı (Problem "etkilenen", RCA, CoSRE, MCP)** | `chstore/blast_radius.go:90-125`, yazıcı `backtrace.go:23-45` | çağıran RPS, hata % | service_callers_5m | **hayır** (yazıcı da okuyucu da) | VM istemci [V] |
| **Fonksiyon kodu RED (CoSRE, vaka sayıları)** | `chstore/rollup_read.go:217`, `incident_cases.go:506` | RED | rollup_spans_* | **hayır** | fonksiyon kodu VM'de yok |
| **Trace metrikleri** | `chstore/tracemetric.go:87` | % hatalı trace, süre | trace_summary_5m | **hayır** (bilinen boşluk) | yaklaşık VM kök span |
| CoSRE taban adımı | `sre/readers_baseline.go:193-240` | RED | önce VM, span yedek | yedekte evet | VM |
| CoSRE/MCP tarama, servis/uç nokta ekranları | `mcptools/service_sweep.go:469-1129`, `chstore/spanmetric.go`, `summary.go:686` | RED | MV, ham span | evet | VM |

- Kalın satırlar örnekleme yanlılığına açık ve ağırlıksız.
- Rol modelindeki semptomların hiçbiri bu listede değil. Etkilenen yüzeyler kanıt ve anlatım: Etki satırı, etki yarıçapı, CoSRE.
- Dilim 2 bunları VM'e alır ya da "örneklenmiş" diye işaretler.

**HTTP ailesi adlandırması (v0.10.2390):**
- Motor yalnız noktalı `http.server.request.duration` adını sorguluyordu, Prometheus biçimini göremiyordu.
- Düzeltme: `famSelector` aday adların regex'ini kuruyor (`ml/red_selector.go:3-37,56-76`, `vmetrics/names.go:594,623`).
- İki yazım birlikte varsa sayımlar toplanır; oran ve kuantil etkilenmez (`red_selector.go:30-32`). Birim yazımdan değil sabitten gelir.
- Prod'daki adlandırma doğrulanmadı [V]. Yanlışsa VM semptomlarının hepsi o ailede sessiz kalır. Bu yüzden dilim 1'in giriş şartı: Settings › ML tik durumunda "keşfedildi ama boş" notu (`trace_red.go:86`) prod'da sıfır olmalı.

---

## BÖLÜM 4 — Sadeleştirme

### 4.1 Ayar yüzeyi

Hedef: servis kademesi başına tek hassasiyet (düşük / normal / yüksek).
- **Servis kademesi alanı bugün yok** (`service_metadata` kolonları `store.go:2496-2520`).
- En yakın mevcut eksen ML profili (always_on / business_hours / low_traffic / batch, sabitlenebilir).
- Öneri: kademe = profil, yeni şema yok. Kademe başına hassasiyet üç sabit tablosuna eşlenir:
  - etki kuralı göreli katı
  - mutlak taban
  - yük kapısı
- Hangi kademenin hangi profile karşılık geldiği açık karar.

Kaldırılacak anahtarlar (geriye uyum katmanı yok — CLAUDE.md anti-pattern listesi):

| Anahtar (`system_settings`) | Alan | Yerine |
|---|---|---|
| `native_detectors` | metric_anomaly, service_slowdown, db_health, db_capacity, db_slow_statement, runtime_pods (`native_detectors.go:35-62`) | yok (yollar silinir) |
| `anomaly_promotion` | tüm blob (`anomaly_promotion.go:86,108`) | yok |
| `anomaly_sensitivity` | subjects (promotionMode), runtime.heapMode, errorRamp, metrik eşikleri, slowdown | yok; **calendar** ve **batch kalıpları** `ml`'e taşınır |
| `ml.detectors.*` | 27 dedektör anahtarı (`ml_settings.go:29-44`) | SEMPTOM ve KESİN OLAY her zaman açık; KANIT dedektörleri tek "kanıt" anahtarı (maliyet için) |
| `ml.groupProblems` | (`:249`) | yok; rol kuralı canlı |
| `ml.publishGates` | (`:254`) | yok; sabitler semptom üçlü eşiğine katılır |
| `ml.lagDailyPattern` | (`:259`) | çelişki 2'nin kararına bağlı |
| `ml.quality.*`, `criticalZ`, `dwellBuckets`, `minBaselineRate`, `minAffectedRequests`, `slowRequestSec`, `chronicEpisodes`, `dailyEventCap` | (`:132-140,205-231`) | kademe hassasiyeti (üç sabit tablo) |
| `problem_priority.sloBurnProblems` | (`problem_priority.go:65-72`) | "SLO'lu serviste semptom = burn" kuralı (çelişki 6) |
| `problem_priority.inboxKeepSourcePriority` | kalır; silinen ailelerin kalıpları çıkarılır | — |
| yaml `logs.elasticsearch.ml_enabled`, env `COREMETRY_ES_ML_*` | (`config.go:555`) | yok (poller silinir) |

Kalanlar:
- `ml` altyapı alanları: bucketSec, tickSec, timeoutMs, maxServices, VM/ES bütçeleri, backfill, baseline.
- `problem_graph_merge` (görünüm).
- `oracle_sources[].problemMode`.
- `exception_triage`.
- `notify_policy`.

### 4.2 Gürültü bütçesi

**Öneri** (kurulmaz):
- Takım başına günlük Problem üst sınırı N, Soru 2'nin cevabı.
- Sayım M3 ile aynı: liste görünümündeki satır, takım `owner_team`.

**Aşılınca:**
1. P1 ve KESİN OLAY Problem'leri hiçbir zaman bütçeye takılmaz; açılır ve bildirilir.
2. Bütçeyi aşan P2/P3 SEMPTOM Problem'i yine açılır, ama:
   - açılış bildirimi gönderilmez
   - satırda "günlük bütçe aşıldı" işareti olur
   - takımın ilk aşımında tek bir özet bildirim gider: "bugün N Problem, en gürültülü 3 kaynak".
3. Aşım gece işinin raporuna girer. Aynı dedektör × servis üç gün üst üste aşıma katkı yaparsa precision-program'daki otomatik gölge kuralına aday olarak düşer. Karar orada.

**Öncelikle etkileşim:**
- Bütçe önceliği değiştirmez. `computePriority` aynı kalır.
- Bütçe yalnız bildirim kararını değiştirir; "P1 zamanla inmez" kuralına dokunmaz.
- Bütçe sayacı Redis'te süreçler arası tutulmalıdır (`p1-escalated` SETNX emsali, merge-spec D5).

### 4.3 Problem kartı

**Bugün** (`chstore/problem_standard.go:93-108`, `ProblemStandardHeader.tsx`):
- manşet ("normalde X, şimdi Y" ailesi)
- Etki: v0.10.2482'de kanıttan; dakikada hatalı istek ve trafik payı, etkilenen istek ve gecikme, lag birikimi, öldürülen pod
- Ne zamandan beri
- Kanıt + kaynak
- Güven (signal_gates)
- Sonraki adım
- köken satırı (graf birleşmesi)

**Önerilen kart:**
1. Tek cümle "normal → şimdi" (manşet, değişmez).
2. Etki satırı: etkilenen istek ve pay (değişmez).
3. **En fazla 3 kanıt.** KANIT rolündeki üyeler, rollout ve köken, Problem'den önceki ya da aynı başlangıç sırasıyla.

**Kalkanlar:**
- Güven satırı: rol modelinde Problem'i semptom açtığı için güven ikili oluyor.
- Öncelik gerekçe metni: katlı teknik ayrıntıya iner.
- 3'ten fazla kanıt: "Teknik kanıt" katlanır bölümüne iner.

Etki tabanının span MV'den okunması (BÖLÜM 3) ayrı bir düzeltme ister. Kart metni bugün örnekleme kipine bağlı.

### 4.4 Silinecekler ve bugünkü tüketicileri

Her satır ayrı silme dilimi ve ayrı sürüm. Tüketiciler grep ile bulundu [K, dosya düzeyi]; satır düzeyi
dilim başında yeniden grep'lenir.

| Silinecek | Bugünkü tüketiciler |
|---|---|
| Yerel metric_anomaly + error_ramp + `anomaly-cluster` (metrik) + calendar drop (`anomaly/anomaly.go`, `error_ramp.go`, `clustering.go`) | `chstore/problem_owner.go` (sahip sınıfı), `problem_priority_inbox.go` + `_migrate.go` (keep kalıpları), `notify_kind.go`, `problem_source_breakdown.go`, `problem_standard.go`, `evaluator/problem_graph.go`, `sre/hypotheses.go:815`, FE `ProblemDetail.tsx`, `lib/problemSubject.ts`, `lib/types.ts`, golden `api/testdata/priority_reason_*.golden.json` |
| svc-slowdown (`evaluator/service_slowdown.go`, `chstore/service_slowdown.go`, `op_p99_*`) | `chstore/problem_member.go`, `problem_owner.go`, `signal_gates.go`, `anomaly_sensitivity_slowdown.go`, `engine_compare_read.go`, `svc_shadow_problems.go`, `ml/svc_breadth.go`, `svc_compare.go`, `group_live.go`, `api/ml_svc_shadow.go`, `alert_rule_series.go`, FE `svcSlowdownPivots.ts`, `MLSvcShadowCompare.tsx`, `alertMetricSeries.ts` |
| db-health / db-capacity / db-slow-stmt | `api/inbox.go`, `inbox_keep_priority.go`, `anomaly/rootcause_bubbleup.go`, `chstore/bubbleup_mode.go`, `sampling_rule_shadow.go`, `evaluator/problem_graph.go`, FE `databaseProblems.ts`, `problemSubject.ts`, `alertMetricSeries.ts`. **DB sayfaları kalır.** |
| runtime:* + heap band | `evaluator.go:646` çağrısı, `anomaly_sensitivity_runtime.go` |
| subjects + `anomaly-auto:subject` + `promoteStrongAnomalies` + `anomaly_promotion` | `evaluator/anomaly_subject_sweep.go`, `anomaly_subject_group.go`, `chstore/subject_group_rule.go`, `promoted_anomaly_source.go`, `promoted_source_event.go`, `notify_kind.go`, `problem_owner.go`, `api/anomalies_unified_attached.go` |
| Elastic ML poller (`internal/elasticml`) | `main.go` kablosu, config alanı |
| `anomaly_events` yazımı ve okuyucuları (son dilim; tablo TTL'e kadar okunur) | `anomaly/fusion.go`, `rootcause_worker.go`, `mcptools/tools.go` (list_anomalies), `sre/readers_anomalies.go`, `sre/orchestrator.go`, `sre/chatpack.go`, `api/inbox.go`, `rollout_detail.go`, `deployment_report.go`, `problem_insight.go`, `anomalies_unified.go`, `anomaly_feed_routes.go`, `anomaly_extra.go`, `annotation_routes.go`, `chat_*`, `copilot_*`, `chstore/anomaly_feed.go`, `engine_compare_read.go`, `log_templates.go` |
| ML: op_error_rate, trace_error_count, svc_breadth, metric_http_client | ML ayar ekranı, `ml/group_gate.go` `singleWorsening`, `svc_shadow.go` `isSvcDetector`, FE `lib/mlDetectorLabels.ts`, karne, evalset (`api/ai_evalset_*`) [V: evalset atfı yalnız dosya adından] |
| Ayar ekranları | FE `pages/settings/NativeDetectorsSubsection.tsx`, `AnomalyTab.tsx`, `SubjectsSubsection.tsx`; `api/native_detectors.go`, `api/anomaly_sensitivity.go` |
| Yazıcısı olmayan `svc_volume_spike` sabiti | `chstore/ml_events.go:270` |

Bildirim: silinen ailelerin `ProblemNotifyKind` satırları ve `*_notify_kind_pin_test` pinleri aynı dilimde
gider (/aiops §2). Takımın kendi alarm kuralları kapsam dışıdır.

---

## BÖLÜM 5 — Dilim sırası (zorunlu)

| # | Dilim | Giriş şartı | Çıkış şartı (altın set) | Davranış |
|---|---|---|---|---|
| 1 | Altın set + kapı: `ml-golden-case`/`-run` sayfaları, elle giriş ucu, kapı replay yürütücüsü (`ReplayGroupDecisions` + `decideSvcShadow` + `impactDecide`), CI sentetik fikstürler, gece işi | Soru 1 cevabı (≥ 5 olay) + Q1–Q7 çıktıları; HTTP adlandırması prod'da doğrulanmış | ilk gece koşusu yeşil = bugünkü kapının referans sonucu kaydedildi; CI fikstürleri bugünkü kararı birebir üretiyor | değişmez |
| 2 | Semptomların metrik kaynağına alınması: Etki tabanı, SLO burn, etki yarıçapı ve topoloji okumaları VM'e ya da "örneklenmiş" işaretine | dilim 1 yeşil | M1 ve M4 gerilemez; Etki sayıları VM ile ±%10 [V eşik] | kart sayıları değişir (OK gerekir) |
| 3 | Rol kuralı gölgede: `ml_events` kanıtına `roleShadow` damgası; fark raporu altın sette | dilim 2 yeşil | 14 gün gölge; fark raporu operatöre: kaybolan olay = 0 ya da operatör kabulü; sakin pencerede M3 ≤ Soru 2 | değişmez |
| 4 | Canlıya alma: rol kuralı Problem açar; grup ve tek sinyal kolları kapanır | dilim 3 raporu onaylı; açık kararlar 1–6 kapandı | ilk gece koşusu yeşil; M2 medyanı ≤ Soru 3 | **OK gerekir** |
| 5 | Silme (BÖLÜM 4.4 satır satır, her biri ayrı sürüm) | dilim 4 sonrası ≥ 7 gün yeşil | her silme sürümünden sonra gece koşusu yeşil | **OK gerekir** (satır başına) |

---

## Girdi belgelerindeki bayat satırlar

| Belge:satır | Alıntı (kısa) | Bugünkü gerçek |
|---|---|---|
| R-audit:15 | "ML'de 8 dedektör" | 27 dedektör adı (`ml_settings.go:29-44`) |
| R-audit:21-23 | "Bugünkü varsayılanlarla hiçbir anomali Problem'e … ulaşamıyor", "ML olayları ise yalnız gölge" | ML ana anahtarı açıkken grup (v0.10.2052), tek sinyal (2092) ve servis (2130) kolları canlı (`group_gate.go:5-6,185-209`); ana anahtar hâlâ varsayılan kapalı (`ml_settings.go:343`) |
| R-audit:46 | trace_op_latency "AÇIK" | silindi (v0.10.2087) |
| R-audit:57 | ML aileleri "hepsi kapalı" | 10 dedektör varsayılan açık (`ml_settings.go:324-331`) |
| R-audit:60, eski-gap:56 | `ml/promote.go` terfi kopyası | silindi (v0.10.2067) |
| R-audit:85-86 (N3) | storm `scan()` erken dönüşüne bağlı | storm kapıdan bağımsız (`anomaly/anomaly.go:535-539`) |
| R-audit:114 | incident bağlama kapalı | Incidents emekli, API 410 (`api/incidents_gone.go:38`) |
| eski-gap:26-27 | "ML grup Problem'i ≥2 FARKLI sinyal ister; tek dedektör asla Problem açmaz" | tek sinyal kolu ve servis kolu tek olayla açar (`group_gate.go:185-209`, `svc_shadow.go:13-24`) |
| eski-gap:44, :46 | log_template_new "prod AÇIK", trace_op_latency "kod AÇIK" | ikisi de silindi (2085, 2087); halefi new_template akış dışı (D) |
| eski-gap:52 | Oracle/ext varsayılan "bilinmiyor" | `problemMode` varsayılan shadow ve shadow da Problem açar (`oracle/settings.go:204-213`, `main.go:1351-1363`) |
| davis-gap:13, satır 1 | trace_red "p95/p99 yerine 1 sn payı; 50→400 ms görmez" | p95 kolu var (`opP95FloorMs = 100`, `trace_red.go:44-46`) |
| davis-gap satır 13 | `group_gate` "yalnız shadow; ≥2 farklı dedektör" | canlı; "≥ 2 farklı sinyal"; tek sinyal kolu var |
| davis-gap:45 | "Tüm ML dedektörleri varsayılanda kapalı" | `mlDefaultOnDetectors` (`ml_settings.go:324-331`) |
| davis-gap S2, S3 | dependency_error_rate ve topic anahtarı "eklenecek" | dependency_error_rate var, varsayılan açık; lag topic serisi `metric_kafka|consumer_lag[#topic]` (`impact_replay.go:9-12`) |
| ml-next:31 | "Heap bandı için ayrı anahtar bulunamadı" | `anomaly_sensitivity.runtime.heapMode` (`anomaly_sensitivity_runtime.go:48`), "on" da gölge gibi (`heap_scan.go:261-263`) |
| ml-next:13 | BaselineDays 7 → 21 (öneri) | gemide: varsayılan 21 (`ml_settings.go:345`) |
| Kod içi | `anomaly/external.go:1-16` "Influx"; `chstore/ml_series.go:325` "hijyen dışlaması" | Influx yok (v0.10.606); dışlama v0.10.2269'da kalktı |

d1-olcum.md ve merge-spec'te bu denetimin konusuna giren bayat satır bulunmadı. merge-spec'in "exception-storm
AYRI" ve "Alarm kuralı AYRI satır" kararları kodda hâlâ geçerli (`chstore/problem_owner.go:72-93`).

---

## Açık kararlar

Her biri iki tarafıyla. "Altın set ne göstermeli" sütunu kararın kanıtıdır.

1. **Trafik düşüşü / sessizlik SEMPTOM mu?**
   - Brif: semptom, tek başına açar.
   - Bugün: `throughput_drop` ve `service_silent` yük/yapı dedektörü. Hiçbir kolda sayılmıyor (v0.10.2477, operatör onaylı, `ml_events.go:263-272`). `singleWorsening` onları kötüleşme saymıyor (`group_gate.go:253-276`).
   - Ek: VM tabanlı sessizlik bugün yok, `service_silent` log tabanlı.
   - Altın set göstermeli: trafik düşüşüyle başlayan en az 3 gerçek olayın başka bir semptomla (hata, yavaşlama) yakalanmadığı ya da geç yakalandığı. Ayrıca sakin pencerede (tatil, mesai dışı, batch) düşüş semptomunun M3'ü bütçe içinde kalmalı.
2. **Kafka lag KANIT mı?**
   - Brif: asla tek başına açmaz.
   - Bugün: consumer lag etki kuralı ailesi tek başına açıyor (I1/I5, 2026-11-19; `group_gate.go:354-357`). ≥ 2 × taban → P1 (v0.10.2396), günlük desen → P3 (v0.10.2476). Okunur lag Problem sayfası bunun üstüne kurulu.
   - Altın set göstermeli: lag Problem'lerinin kaçında aynı serviste bir semptom da açıldı. Hepsinde açıldıysa KANIT kaybı yok. Yalnız tüketici olan servisler (giriş RED'i yok, `svc_error_rate.go` "giriş-span ilkesi") lag'siz kör kalır mı?
3. **Rollout KANIT mı?**
   - Brif: kanıt.
   - Bugün: K-ROLLOUT köken kuralı ("deploy sonrası başladı") yalnız çok varlıklı kümenin kökenini seçiyor, Problem açmıyor (`problem_graph_origin.go:9,155-183`).
   - Çelişki adlandırmada; davranış zaten KANIT. Karar: köken kuralının rol modelinde "kanıt sıralaması" diye adlandırılması.
   - Altın set göstermeli: Q3 rollback olaylarında kökenin doğru seçilme oranı.
4. **Exception fırtınası / fatal exception / Oracle patlaması KESİN OLAY mı?**
   - Bugün:
     - fırtına ve fatal kapısız açıyor (P1 sabit).
     - Oracle ailesi baseline'lı (MAD, dwell; `external.go:1-9`) ve gölge kipte de bildirimsiz Problem açıyor.
     - Problems listesi v0.10.2479'dan beri yalnız Problem gösteriyor; exception grupları ayrı.
   - Brif: "baseline'sız, doğrudan".
   - Oracle için iki yol var: (a) mutlak eşik (Q4 biçimi), (b) bugünkü MAD kalır ve rol yalnız "kesin olay" etiketi olur. Ayrıca `problemMode=shadow`'un Problem açması gürültü bütçesine sayılsın mı?
   - Altın set göstermeli: Q4/Q5 olaylarında iki eşiğin M1 ve M3'ü.
5. **Batch, yayın kapıları ve etki kuralı eşikleri rol modelinde nerede yaşar?**
   - `batchKeep` (`ml/batch.go:115-126`) semptom tarafına taşınır: batch serviste yük ölçüsü semptom değildir.
   - Yayın kapıları (100 çağrı / 25 ms / görünür sapma) yalnız T/G kollarını eliyordu. Kollar kalkınca ya KANIT dedektörlerinin "kanıt sayılma" süzgeci olarak kalırlar ya da silinirler.
   - Etki kuralı eşikleri semptomun üçlü eşiğidir; kademe hassasiyeti onları ölçekler.
   - Altın set göstermeli: kapılar KANIT süzgeci olarak kalırsa kart kanıt listesinin isabeti (precision-program).
6. **SLO'lu serviste semptom = burn.**
   - Bu, operatörün v0.10.1081 kararıyla çelişir ("SLO burn rate problem olmasın") ve kaynağı span MV.
   - Altın set göstermeli: SLO'lu servislerde burn'ün anomali semptomundan erken ya da eşit yakaladığı.
7. **svc_breadth ve op_latency KANIT'a iniyor:** bugün tek başına açan iki aile. Altın sette kaybolan olay sıfır olmalı.
8. **Servis kolu Problem kimliği (E2):** bölüm başlangıcını kimliğe eklemek geçmişi korur, ama bildirim ve verdict imzasını (`p:<ruleId>|<svc>`) etkiler mi? Ayrı onay.
9. **Kademe = ML profili mi**, yoksa yeni bir servis alanı mı? Yeni alan `service_metadata` şeması demek.
10. **Gece kapısı kırmızısı** yalnız ekranda mı kalsın, yoksa `self-*` Problem'i mi olsun (yeni önek)?

## Operatöre sorular (CEVAP BEKLENİYOR)

1. Kaçırılan ya da yanlış açılan 5 gerçek olay (servis, tarih-saat, ne olmalıydı). — **cevap bekleniyor**
2. Takım başına kabul edilebilir günlük Problem sayısı. — **cevap bekleniyor**
3. Kabul edilebilir tespit gecikmesi (dk). — **cevap bekleniyor**
