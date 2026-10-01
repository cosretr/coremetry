# Decision log — architectural calls

Referenced from CLAUDE.md. Newest entries append at the bottom.

- **v0.5.208** — Tempo external trace backend as a *fallback*,
  not a replacement. Coremetry samples at low rate, Tempo holds
  100%, fallback resolves the long-tail trace-by-id.
- **v0.5.210** — P1/P2/P3 priority score blended at READ time
  (no extra column). Persisted is wasteful; fresh recompute is
  cheap and lets fresh deploys/threshold ratios re-rank
  instantly.
- **v0.5.220** — Local monolithic Tempo + 30/100 collector split
  for POC. Operators replicate this layout in prod (sample to
  Coremetry, 100% to Tempo).
- **v0.5.226 / v0.5.235** — Faceted sidebar shipped THEN dropped
  at billion-doc scale because top-10 terms aren't useful when
  the operator's value is in the long tail. Replaced with
  click-from-row filter (still in place) + significant_text +
  Drain templates.
- **v0.5.241** — Log-pattern detector consumes `logstore.Store`,
  not raw `chstore`. Decoupled detector from CH-only path so
  ES-backed installs get coverage too. ES backend batches via
  `_msearch`.
- **v0.5.244** — Drain templater is sample-based on purpose.
  Three-layer log anomaly cover: curated regex (high-priority
  known failures) + significant_text (rare tokens, unsupervised)
  + Drain templates (full shape clustering, sample-based).
- **v0.5.246-247** — Topology op view + service view share the
  same NODE / COL / ROW constants + orphan handling so the
  operator's eye doesn't recalibrate when switching tabs.
- **v0.6.0** — `COREMETRY_MODE` env var lets the single
  binary run in four roles: `all` (default, monolithic POC),
  `ingest` (OTLP receivers + CH writers), `api` (HTTP API +
  SSE + Copilot), `worker` (evaluator + anomaly + topology
  agg + notifier; replicas=1 — leader-elected). Preserves the
  single-binary pitch (one image, one tag) while letting
  banks run 5×ingest + 2×api + 1×worker at billion-spans
  scale.
- **v0.6.2** — Helm chart `deployment.mode: monolithic |
  distributed` toggle. Monolithic = unchanged behaviour from
  v0.5.x (one Deployment, replicaCount applies). Distributed
  = three Deployments + four Services (`<release>` alias →
  api, plus `-ingest`/`-api`/`-worker`). HPA targets api in
  distributed mode; worker locked at 1 replica.
- **v0.6.3** — SSE Redis pub/sub bridge. Worker-pod-fired
  events (problem.open, anomaly.fire) ride a `coremetry-
  events` Redis channel so every api pod's local SSE
  subscribers receive them. PodID-stamped envelopes prevent
  loops; 200ms publish deadline so a wedged Redis doesn't
  stall the evaluator. Single-pod / Noop-cache installs are
  unchanged (no Redis activity).
- **v0.6.4-v0.6.7** — Model Context Protocol server. JSON-RPC
  2.0 over HTTP+SSE per spec 2024-11-05. Exposes tools (7
  telemetry surfaces in `internal/mcptools/`), resources
  (URI-addressed snapshots + templated per-id reads), and
  prompts (curated system+user message pairs that surface the
  in-app ✨ Explain workflows). Auth via existing JWT
  middleware — viewer/editor/admin roles carry into MCP. Runs
  on api+all modes only (worker/ingest pods don't take
  operator traffic).
- **v0.6.8** — AI-driven CH query optimizer on
  `/admin/clickhouse`. Operator pastes SQL, Copilot rewrites
  it against the MV catalogue + six-rule checklist (MV bypass
  / LIMIT / max_execution_time / time-bounded WHERE / GLOBAL
  IN / quantileTDigest defaults). Suggestion only — no auto-
  run; operator copies the optimized SQL to their CH client.
  Routes through `s.copilotExplain` so every call writes an
  ai_calls row for /ai attribution.

## v0.7 → v0.10.629 — karar kaydı (2026-05-29 → 2026-09-10)

Temaya göre gruplu, tema içinde sürüm sırasıyla. Git mesajı olan
kararlar sürüm etiketiyle anılır; yalnız operatör notundan bilinenler
"(kaynak: operatör notu, tarih)" ile işaretli. Müşteri/kurum adı,
alan adı, gerçek şema/tablo/kolon ve iş yükü adları bu dosyaya
girmez — jenerik ad kullanılır.

### Depolama / ClickHouse

- **v0.9.385-387 + `migrations/0001-0003`** — Rollup katmanı iki
  aile: DAR (service · kind · status, tdigest, 10s→1m→5m→1h) hızlı
  overview için; GENİŞ (endpoint · channel_code · function_code,
  20 kovalı eksponansiyel sayaç, 1m→5m→1h) breakdown için; metrik
  zinciri ayrı (0003). Okuma `PickRollup` karar tablosuyla step'i
  tam bölen en kaba kademeye iner; pencere başı tablonun en eski
  verisinden eskiyse ham yola düşer (kapsama dürüstlüğü), tablo
  yoksa sessizce ham yol. Reddedilen: DDL'in boot'ta otomatik
  koşması — ON CLUSTER DDL'i N pod yarıştırınca dağıtık kuyruk
  tıkanır (v0.9.613 vakası), sahibi operatör. `spanmetrics_*`
  ailesinin emekliliği v0.9.428'de RESMEN ertelendi (ölçüt: rollup
  prod'da ≥30 g + resolver okumaları eşit/hızlı + 0004 backfill).
  Kod: `internal/chstore/rollupselect.go`, `rollup_read.go`,
  `rollup_fastpath.go`.
- **v0.9.770 + 2026-08-09 A/B** — Rollup kurulumu UI sihirbazından
  (/admin/clickhouse → "Rollup katmanı": durum → ön kontrol →
  kur → 5 dk write_failed bekçisi → geri al); yine boot'ta ASLA
  koşmaz, tek tetik admin'in düğmesi. Aynı hafta ölçülen A/B
  (9 koşu, query_log medyanı, iki bağımsız tekrar): GENİŞ ailenin
  ORDER BY'ında `ts` sonda olduğu için servis+zaman okuması zaman
  budaması yapamıyor ve aile ≈1.02:1 sıkışıyor → **0002+0005 prod
  kurulumu ÖNERİLMEZ**; geniş aile ancak ORDER BY yeniden
  tasarımıyla (ayrı bilinçli karar) değer üretir. Kod:
  `frontend/src/pages/AdminClickhouse.tsx`,
  `migrations/0002_rollup_wide.sql` (kaynak: operatör notu,
  2026-08-09).
- **v0.9.1308 + `migrations/0009_state_unify.sql`** — State
  tabloları (problems, users, alert_rules, dashboards, …) tek
  replikasyon grubunda: ZK yolunda `{shard}` yok, yol koda gömülü
  değil kümeden okunuyor (`resolveStateReplicaPaths` — komşular
  hangi yolu tutuyorsa o), 37 state tablosu negatif türetimle
  bulunuyor (shard kayıtlarında adı geçmeyen = state). Sebep:
  `Replicated*` ama Distributed sarmalayıcısız tablolar failover'da
  bağlanılan host'a göre ayrışıyordu (prod: problems 633k / 4k iki
  shard'da). `ReplacingMergeTree(version)` + `FINAL` sözleşmesi
  göçü güvenli kıldı: iki shard'ın bağımsız ürettiği aynı
  deterministik id'ler `version` ile birleşti (prod 635.864,
  birleşim aritmetiği oturdu). Reddedilen: uygulamayı tek host'a
  pinlemek (yeni bölünmeyi durdurur, ayrışmayı onarmaz); ayrı
  1×N replica küme tanımı (ZK yolu yerinde değişemediği için
  iki kat iş). Kod: `internal/chstore/state_unify.go`.
- **v0.9.1139 · v0.10.247 · v0.10.561** — Kullanıcı durumu için
  yeni şema yok, `saved_views(page='<kind>')` tek tablo (invariant
  #5): sohbet kalıcılığı `ai-chat` blobu (1139), kişisel tablo
  tercihleri `page='table:<key>', id='pref:<uid>:<key>'` doğal
  upsert + `name=''` tombstone (247), 90 günlük sohbet arşivi
  süpürücüsü (561). Reddedilen: yüzey başına tablo. Kod:
  `internal/api/preferences_routes.go`.
- **v0.10.181** — Operatör kararı deposu `anomaly_verdicts`: düşük
  hacimli operatör state'i için şablon — `ReplacingMergeTree
  (version)`, ORDER BY event_id (olay başına SON karar), partition
  YOK, `FINAL` okuma, TTL 180 g, küme kipinde otomatik Replicated
  (`anomaly_silences` ile aynı sınıf). "Değil" artık yalnız
  susturma değil, kayıtlı karar; "Anomali" kararının deposu ilk
  kez var. Kod: `internal/chstore/anomaly_verdict.go`,
  `internal/api/anomaly_verdicts.go`.
- **v0.9.1150 → v0.10.601 (şablon)** — Her dış kaynak/ayar
  `system_settings` JSON blobu + `LoadPersisted` boot'ta + 30 s
  yenileme + maskeli `Snapshot` (Tempo şablonu,
  `internal/tempo/client.go`): `victoria_metrics` (v0.9.1150),
  `mcp_client_servers` (v0.10.87), `influx_sources` (v0.10.222),
  `oracle_sources` + `oracle_poll:<id>` watermark (v0.10.580/601).
  Sır sözleşmesi tek: blob'da düz token SAKLANIR, GET asla geri
  vermez (`hasToken`), boş girdi saklıyı korur, rotasyon = yenisini
  yapıştır; `env:`/`file:` referansı OPSİYONEL ve doluysa kazanır
  (`internal/secretref`, v0.10.271-273; Tempo/Thanos/VM'e yayıldı,
  ES/SMTP/AI/LDAP bilinçli kapsam dışı). Reddedilen: "yalnız secret
  referansı, düz token reddedilir" (v0.10.222-223 arasında yaşadı;
  operatör "saklanabilir, maskeli yaparsın", 2026-09-01). Kod:
  `internal/chstore/vmetrics.go`, `internal/secretref/`.

### Ingest / OTLP

- **v0.8.73** — Binary içi head+tail sampler ve Settings "Trace
  sampling" sekmesi tamamen söküldü (`internal/sampling/` yok);
  Coremetry ALDIĞI span'lerin %100'ünü saklar, örnekleme istenirse
  collector'da (v0.5.208/220 sample-to-Coremetry / 100%-to-Tempo
  ayrımı aynen). Ingest sıcak yolundan span başına bir karar ve bir
  ayar katmanı gitti; geriye dönük şim yok (eski `sampling:` bloğu
  sessizce yok sayılır). KALAN ve karıştırılmaması gereken:
  pipeline `KindSample` drop kuralı, self-obs SDK sampler'ı, okuma
  zamanı downsampling (heatmap/scatter). Kod:
  `internal/otlp/http.go` (addSpan → pipeline → store).
- **v0.9.797** — Dışlama mimarisi: ingest drop'un TEK sahibi
  `internal/pipeline` motoru (AcceptSpan/Log/Metric üçü OTLP
  hattına bağlı; kurallar `system_settings`, UI Settings →
  Pipeline). Span için OKUMA-zamanı route filtresi bilinçli YOK:
  dar rollup ve `operation_summary_5m` route bilmiyor, filtre
  entry-RED'i ham taramaya düşürür ve KPI fallback'i dışlanmamış
  sayı gösterirdi. Metrik için okuma-zamanı `metric_exclusions`
  var (kural seti cache anahtarına FNV özetiyle girer, kural yokken
  SQL bayt-bayt eski). Müşteri path desenleri repoya ASLA —
  operatör Settings'ten girer. Kod: `internal/pipeline/pipeline.go`,
  `internal/otlp/http.go` (kaynak: operatör notu, 2026-08-09).
- **v0.9.240 + 2026-07-25 direktifi** — Giriş-span ilkesi: bir
  servisin RED metrikleri yalnız `kind IN ('server','consumer')`
  giriş span'lerinden hesaplanır ("aynı Dynatrace gibi");
  client/producer/internal span'ler servisin YAPTIĞI çağrılardır,
  ölçüsü değil. Latency 240'ta, throughput+error operatör onayıyla
  çekildi (ölçülen etki: demo p50 60→807 ms, throughput 8× düşer,
  error rate 5× çıkar; prod latency SLI %99.99→%99.85, alarm
  fırtınası yok). Tuzak: `service_summary_5m`'de `kind` boyutu
  yok ve ORDER BY değişemez → çözüm boyut eklemek değil
  `entry_*_state` kolonları. Giriş span'i olmayan servis için "tüm
  span'lere düş + nedeni yaz" fallback'i şart. Kod:
  `frontend/src/lib/entrySpans.ts`, `internal/chstore/slo.go`
  (kaynak: operatör notu, 2026-07-25).
- **Sürekli (operatör kararı)** — PII/veri redaction özelliği YOK:
  attribute, log gövdesi, DB ifadesi, trace yükü hiçbir yerde
  maskelenmez/hash'lenmez/tokenize edilmez. Gerekçe: olay
  incelemesinde ham adli veri gerekir; erişim denetimi çevrede
  (roller, audit, depolama şifrelemesi) yaşar, okuma anında
  mutasyon değil. Reddedilen: field-level encryption, DLP,
  "configure off" toggle'lı redaction. Kod: `internal/otlp/
  convert.go` attribute'ları verbatim taşır (kaynak: operatör
  notu, "redact etme asla").

### Metrikler / VictoriaMetrics

- **v0.9.1150 (+1151)** — VictoriaMetrics OKUMA backend'i, Settings
  toggle'ıyla (env değil): açıkken katalog/picker,
  `/api/metrics/query` (Explore + dashboard + MCP), etiket
  değerleri VM'den; kapalıyken CH yolu bayt-bayt aynı. İki katı
  kural: **sessiz CH fallback YOK** (VM açık+erişilemez → 502;
  metrikte SAYILAR değişir ve ekran bunu söyleyemez — Tempo
  fallback'inden bu yüzden farklı) ve **400 ≠ 502** (çevrilemeyen
  sorgu 400 + desteklenen küme; transport/VM hatası 502 — sağlıklı
  VM'e "bozuk" dedirtmeyiz). Cache anahtarları `src=` backend
  işareti taşır (v0.5.187 sınıfının ayar-girdili hâli). 1151:
  `?metricsrc=vm|ch` deneme parametresi (Enabled şart değil, UI
  yazmaz). Span-türevi her şey + `/api/metrics/resolve` CH'de.
  Reddedilen: `internal/thanos`'u ortak çekirdeğe göçürmek —
  promapi kopya-desen, Thanos/clusters ayrı kalır. Kod:
  `internal/api/metricsource.go` (seam), `internal/vmetrics/`,
  `internal/promapi/`.
- **v0.9.1154-1160** — Çeviri dili MetricsQL (PromQL süperseti;
  operatör düzeltmesi 2026-08-17): rate/last 300 s pencere tabanı,
  `increase` ASLA taban almaz (v0.6.36 birim-ölçek sınıfı); `last`
  groupBy'da `max` ile çöker (CH argMax sınıfı); ad eşleyici OTel
  noktalı adı Prometheus yazımına PROBE ile değil aday
  alternation'ıyla eşler (varlık ≠ canlılık — bayat aile probe'u
  kandırır); OTLP histogramının taban serisi olmadığı için
  avg/rate/increase `_sum`/`_count` kollarıyla MetricsQL `or`
  kompozisyonu (operatör kararı, ilk kesimi değiştirdi: sol kol
  kazanır, alternation'ın çift sayma bedeli kalktı).
  `ValidatePromQL` asimetrik: CH parse eder, VM nil (üst kümeyi
  motorundan katı reddetmek "bozuk uç" gibi görünür). Kod:
  `internal/vmetrics/promql.go`, `names.go`, `histogram.go`.
- **2026-09-02 kararı → v0.10.292-294 (+365/366/374)** —
  **VictoriaMetrics TEK metrik deposu** (operatör: "VM'i ANA metrik
  deposu olarak konumlandırmak istiyorum", 2026-09-04; audit
  onaylı). OTLP metrik gövdesi HAM hâliyle VM'e forward edilir
  (`/opentelemetry/v1/metrics`; gzip aynen, JSON→protobuf; CH
  modelinden GERİ ÜRETİLMEZ çünkü CH modeli kayıplı), ayrı bayt
  bütçeli kuyruk, OTLP cevabını asla bekletmez, varsayılan KAPALI;
  `/api/metrics/compare` kıyas ucu; sabit-adlı okuyucular
  (hosts/infra, DB kapasite, JVM pod) `metricSource` seam'ine
  taşındı, `dql.go` bilinçli CH-çakılı. **AÇIK DURUM
  (2026-09-10):** ClickHouse `metric_points` yazımı HÂLÂ sürüyor —
  Dilim 5 (CH yazımını kapat, ölü `spanmetrics_*_5m` sil) ön koşulu
  prod çift yazım + compare sıfır fark, operatörde; VM OSS olduğu
  için 13 aylık saatlik rollup ufkunun VM karşılığı yok (ufuk VM
  retention'ına iner ya da CH rollup dondurulur — karar bekliyor).
  Ekip-hazırlık denetimi bunu "kural aspirasyonel, kod ÇİFT
  yazıyor" (B3) diye kaydetti. Thanos: remote_write tercih, olmazsa
  ayrı kaynak. 3b-2/3b-3 (db receiver keşfi + motor panelleri)
  PARK: "db receiver kullanmıyorum" (2026-09-05). Kod:
  `internal/otlp/forward.go`, `internal/vmetrics/write.go`,
  `internal/api/metrics_routes.go`,
  `docs/audit/vm-metrics-migration.md`.
- **v0.10.222-231 → v0.10.606** — InfluxDB 2.x dış hata serisi
  entegrasyonu eklendi (audit onayı 2026-09-01: kaynak yönetimi,
  1 dk kovalı poller → `metric_points` `ext:<sorgu>`, kind=external
  anomali, kanıt zinciri, Problem paneli, mevsimsel baseline) ve
  operatör "influxdb ile işimiz kalmadı" deyince Oracle Aşama 2
  gemiye çıktıktan SONRA tek sürümde söküldü (2026-09-10; şim yok,
  `influx_*` blobları okuyansız kaldı). Kalıcı olan kararlar: dış
  seri için AYRI tablo değil `metric_points` + `exemplars` (K1);
  istemci kütüphanesi değil net/http + annotated CSV (K2); seri
  kova bitiş zamanına yazılır, "operatörün Grafana'sıyla aynı
  sayı" ilkesi (v0.10.224). `ExternalScanner`/kind=external hattı
  KALDI — Oracle onun üstüne kurulu. Kod (kalan):
  `internal/anomaly/external.go`, `internal/secretref/`.

### Loglar / Elasticsearch

- **v0.7.14** — Collector logları Elasticsearch'e ÇİFT gönderir
  (Coremetry OTLP export'una ek); CH yazma deposu kalır, ES yalnız
  OKUMA backend'i (invariant #2), `COREMETRY_LOGS_BACKEND` ile
  seçilir. `logstore.Switchable` iki backend'i BİRLEŞTİRMEZ,
  aralarında SEÇER — prod ES'teyken CH `logs`'a yazılan satır hiç
  görünmez (Oracle satırlarının ayrı tabloya gitmesinin nedeni,
  aşağıda). Kod: `internal/logstore/switchable.go`, chart
  `otelCollector.elasticsearch.*`.
- **v0.8.8 · v0.8.139-140 · v0.8.270 (+2026-06-15 direktifi)** — ES
  maliyet disiplini: Live Patterns + Similar Traces silindi
  (`significant_text` 30 s poll'da, v0.8.3 CPU olayı; −1120
  satır); her ES okuma `serveCached` (singleflight + SWR);
  `track_total_hits` tavanı + soft timeout; canlı kuyruk tiki
  5→10 s; log-anomali `?window=` cache anahtarında sınırsız
  gezmesin diye {1m,5m,15m,30m} basamağına sabitlendi → uç başına
  en fazla 4 `_msearch`/TTL. Operatör: "Elastic üzerinde yoğun api
  istekleri olsun istemiyorum" — sayı VE sıklık; fetch yalnız
  expand/open'da, liste prefetch'i yok, staleTime ≥ sunucu TTL.
  Kod: `internal/api/cache.go`,
  `internal/api/log_patterns_explain.go` (kaynak: operatör notu,
  2026-06-15).
- **v0.10.566** — İstek kimliği (request_id) YALNIZ log gövde
  metninde yaşar; yapılandırılmış alan EKLENMEZ (operatör:
  ingest'i değiştirmek istemiyor; `LogRecord.Attributes` hiçbir
  backend'de dolmuyor). Dış log platformu linki: gövdede
  request_id varsa onunla, yoksa span attribute yolu (function_id
  + channel_code birlikte); kazanan span sırası seçili → ilk
  hatalı → root. ES bedeli (10B doc/gün) yüzünden okuma tavanlı.
  Kod: `internal/reqid/`, `internal/api/trace_link_identity.go`.
- **v0.10.580-603** — Kurumun Oracle hata tablosu → Problem hattı,
  Aşama 1-2. Sürücü **go-ora** (saf Go): Dockerfile `CGO_ENABLED=0`
  + alpine/musl, godror Instant Client ister ve musl için
  yayınlanmaz — "tek binary, tek imaj" kısıtı sürücüyü seçti.
  Şema/tablo/kolon adları ve tip süzgeci KODA GÖMÜLMEZ, ayardan
  (`Columns{alan: KOLON}`, `-` = alan tabloda yok; identifier
  regex'i tırnaksız SQL'e girer, değerler daima bind); zaman dilimi
  IANA (varsayılan Europe/Istanbul) + "kolon dilimli" anahtarı,
  `time/tzdata` gömülü (alpine'de zoneinfo yok). Tüketim modeli
  periyodik ingest (yalnız `COREMETRY_MODE=worker|all` lideri,
  `cache.LeaderHolder` 30 s kira), watermark `system_settings`'te
  kalıcı ve yalnız yazım başarılıysa ilerler; yeniden görülen satır
  aynı `row_id` → RMT'de tek satır. Satırlar CH `logs`'a DEĞİL
  kendi RMT tablosuna yazılır ve Trace Logs sekmesinde üçüncü dizi
  olarak (`/api/oracle/errors`, origin `oracle`) birleşir (audit
  B4). Bağlantı testi 200 + `{ok:false}`; dar pencere boşsa 24 s
  geniş ikinci deneme ("hata yok + satır yok" üç sorunu
  ayırt edemiyordu — Influx bunu prod'da öğretti). Aşama 3 (shadow
  Problem) açık. Kod: `internal/oracle/`,
  `internal/api/oracle_logs_routes.go`,
  `frontend/src/pages/settings/oracleForm.ts`,
  `docs/audit/oracle-error-log-2026-09-09.md`.

### CoSRE / AI / MCP

- **v0.8.374 + v0.8.397-398** — Copilot yerel küçük model
  (air-gapped kurum, 2B sınıfı; gemma4, 2026-07-21) için tasarlanır:
  veri sunucuda deterministik PREFETCH edilir, model yalnız ANLATIR
  (analyze-service deseni; guided chat = deterministik niyet
  router'ı + prefetch→anlatım); çok turlu tool döngüsü ve katı JSON
  şeması şüpheli. Her düzyazı yüzeyi Türkçe (`AnswerInTurkish`
  13 prompt'a; yapılandırılmış çıktı prompt'ları bilinçli
  dışarıda). Harici LLM API'si yok, dış ajan tüketicisi yok — MCP
  ToolList'in değeri in-app function-calling. Kod:
  `internal/copilot/`, `internal/api/copilot_guided.go` (kaynak:
  operatör notu, 2026-07-21).
- **v0.8.468 + Faz 1.6 → v0.9.1232** — AI çağrı disiplini: TÜM
  sistem prompt'ları `internal/copilot/prompts.go`'da (sicil +
  dil kapısı `prompt_language_test.go`; sicilde olmak doğru sınıfta
  olmak değil, satır-içi ekler de sicil dışı); her Explain
  `s.copilotExplain(r, …)` üzerinden ki `ai_calls` satırı düşsün ve
  `/ai` atfı dürüst kalsın — `s.copilot.Explain` doğrudan
  çağrılmaz. Erişilemez ve pahalı filo-geneli tek-atış
  SystemAnalysis (v0.8.75) silindi; tezi odaklı yüzeylerde yaşıyor
  (Problems Explain, RootCausePanel). Kod:
  `internal/api/ai_observability.go` (`copilotExplain`),
  `internal/copilot/prompts.go`.
- **v0.9.477 → v0.10.461/483** — TEK AI çekmecesi: inline Explain
  panelleri v0.9.477'de çekmeceye toplandı; operatör üç kez
  "Explain ile CoSRE iki ayrı drawer" deyince 461 kabuğu
  (genişlik 620, tek cevap kartı sınıfı), 483 bileşeni eşitledi:
  `AIDrawer` gövdesi `AIDrawerBody`'ye, `CopilotChat` `?ai=`
  öznesini kendisi okuyup aynı kabukta açıklama kipine geçer;
  AppShell yalnız CopilotChat mount eder, `AIDrawer` ince
  sarmalayıcı. Reddedilen: iki bileşen, iki başlık anatomisi. Kod:
  `frontend/src/components/CopilotChat.tsx`,
  `frontend/src/components/ai/AIDrawer.tsx`.
- **v0.10.172 + v0.10.194** — Serbest soru için kademe 3.5: router
  → çekmece → RAG eşleşmezse küçük modele TEK katı-JSON niyet
  sınıflandırma çağrısı (slotlar canlı listeye doğrulanır,
  adlandırılmış ama eşleşmeyen → none); eşleşirse mevcut
  prefetch→anlatım yolu. Kip `intentClassify` off | on |
  **on_no_loop (varsayılan)** — operatör "RAG'a gitmesine gerek
  yok"; 194'te `none` artık cevapsız değil: tool'suz TEK genel
  anlatım (`chat-general`, "telemetriyle eşleşmedi — genel
  bilgiyle" notu, KB adayı OLAMAZ). Reddedilen: serbest tool
  döngüsüne düşmek (2B modelde "schema soup"). Kod:
  `internal/api/copilot_intent.go`, `copilot_guided.go`,
  `internal/copilot/copilot.go` (IntentClassifyMode).
- **v0.9.1136** — MCP yetki: `MinRole` kayıt-defteri metadata'sı +
  tek `CallGate` (tools/call + resources/read + prompts/get; route
  tablosu yok); sıra rol → sonra rate (rol reddi bütçe tüketmez);
  tanınmayan MinRole FAIL-CLOSED; `cmk_` token kendi rolünü taşır,
  kişi-bazlı scoping yok. A7: REST↔MCP sapması sıfır olsun diye
  `GET /api/anomalies/active` editor kapısı viewer'a indi (okuma
  kapısı hiçbir şeyi korumuyordu, viewer'ın Cmd-K paletini
  bozuyordu). Yazma tool'u yok; gelirse MinRole ≥ editor + audit.
  Kod: `internal/api/mcp_gate.go`, `internal/mcp/`.
- **v0.10.86-89** — Dış MCP İSTEMCİSİ (Coremetry o güne dek yalnız
  sunucuydu): operatör izin listesi `system_settings`
  `mcp_client_servers` (8 sunucu tavanı, 200 tool katalog tavanı),
  tool başına allow/deny köprüde (deny kazanır, `*` önek; süzülen
  tool modele HİÇ görünmez), her çağrı audit `mcp.call`, `[dış:
  <sunucu>]` etiketi, önek `<sunucu>__<tool>` (çakışma politikayla
  değil biçimle imkânsız), tekrar muhafızı tüm tool'lara, deponun
  ilk giden-istemci span'leri. Bilinçli sınır: dış tool'lar YALNIZ
  serbest sohbet döngüsünde (guided/drawer/RAG'a sızmaz); MinRole
  viewer (rol merdiveni Coremetry verisinin). Kod:
  `internal/mcpclient/`, `internal/api/chat_mcp_bridge.go`.

### Anomali / Problems

- **v0.9.487 · v0.9.611 · v0.9.612** — P1 SEYREK kalmalı:
  paylaşılan patlama eşiği 10 servis (operatörün prod deneyimi;
  8→4 tahminleri yanlıştı); taze deploy tetikleyicisi önceliğe
  KARIŞMAZ (deploy sıklığı yüksek, her dağıtım penceresi P1
  üretiyordu) — deploy bilgisi ProblemDetail'de görünür, sıraya
  sokmaz; Inbox varsayılanı yalnız P1, exception-dışı türler
  inbox'ta HEP P3 (görünüm önceliği; evaluator/bildirim değişmez).
  P1 kapıları problemin KENDİ şiddetinden türer (2× eşik, 4+ saat
  açık); öncelik hâlâ okuma anında türetilir, yazılabilir alan yok
  (v0.5.210). Kod: `internal/chstore/problem.go` computePriority,
  `frontend/src/pages/Inbox.tsx`.
- **v0.9.1205** — P1'i HAK ETMİŞ exception grubu ele alınana dek
  (resolve/ignore) P1 KALIR; aynı sınıfın beşinci bildirimiydi
  (627→699→775→1189) — dört düzeltme "şiddet olgu, aciliyet
  zamanla düşer" ilkesini koruyup pencereyi oynatmıştı, operatör
  dördünde de aynı duvara çarptı. Patlama koşulsuz P1; yalnız
  kronik damlama basamaklara iner; P2/P3 eskisi gibi zamanla
  düşer. Kod: `internal/chstore/problem.go` (6 test direktife
  çevrildi).
- **v0.10.543** — `service_silent` dedektörü VARSAYILAN KAPALI
  (operatör: "Anomali service silent'lara ihtiyacım yok"); prod
  /incidents 100+ açık CRITICAL doluydu, span kesintisi bu filoda
  arıza değil. Kapalıyken açık kalanlar tikte çözülür. "Sinyal
  kaybı = critical" çıkarımı yapan yeni dedektör eklenmez
  (evaluator'ın "source silent" resolve gerekçesi ayrı, kalır).
  Kod: `internal/chstore/anomaly_sensitivity.go` (ServiceSilent).
- **v0.10.228 · 587 · 588 · 597** — Dış hat TEK tarayıcı:
  `ExternalScanner` `metric_points`'teki `ext:*` serilerini MEVCUT
  `evaluateAnomaly` çekirdeğine verir (MAD/dwell/z, aynı hassasiyet
  blobu), Problem `kind=external`, özne `ext:<kaynak>/<grup
  değerleri>`; kaynak başına ikinci tarayıcı YAZILMAZ — sözleşme
  "seriyi şu tabloya yaz, ben okurum" (Oracle audit B1). Tarama
  yalnız BAŞARILI poll sonrası (sıfır-padli sahte iyileşme yok).
  587 tik başına açılış tavanı (iki fazlı: önce değerlendir, sonra
  z'ye göre en güçlü N; refresh/resolve etkilenmez); 588 üç ardışık
  poll hatası = "kaynağa erişilemiyor" Problem'i (veri yokluğu ≠
  hata yokluğu); 597 aynı ilk boyutta ≥3 taze açılış tek küme
  Problem'i. Kod: `internal/anomaly/external.go`.
- **v0.10.592 + v0.10.605** — Poller-sahipli Problem'lerin yaşam
  döngüsü: evaluator'ın bayat süpürmesi (updated_at < 3×interval)
  `anomaly:ext-down:` / `anomaly:ext-cap:` kurallarını atlar — tek
  karar noktası `chstore.PollerOwnedRule` (önek sabitleri
  chstore'da; evaluator ve anomaly birbirini import etmez, ikisi
  chstore'u eder); seri Problem'leri BİLEREK süpürülür ("source
  silent" dürüst sinyal). 605: muafiyet yalnız YAŞAYAN kaynak için
  (`SetPollerSourceLive`) — kaynak silinince/kapatılınca Problem'i
  resolve edecek kimse kalmıyordu, Influx sökümü bunu ortaya
  çıkardı. Karar saf `staleSweepCandidates`, kablolama
  kaynak-pinli. Kod: `internal/chstore/problem.go:1439`,
  `internal/evaluator/evaluator.go`.

### Frontend

- **v0.7.55-65 + v0.8.306** — Her veri tablosu tek primitif:
  `useDataTable` (sort + resize, `storageKey` ile kalıcı genişlik,
  COLS `sortValue`/`numeric`) + `DataTable`; sunucu-sayfalı
  tablolar yalnız resize yarısını alır; >100 satır
  `content-visibility`. Elle sıralama yazan son tablolar 8.306'da
  göçtü. Kod: `frontend/src/components/ui/DataTable/DataTable.tsx`.
- **v0.8.253 (+v0.9.937)** — URL = seçimlerin tek kaynağı
  (filtre/çekmece/odak/sekme/aralık); sayfa `replace:true` ile
  yazar, URL→state içe aktarımı sig-guard'lı (bir range
  değişikliği yerel filtreleri siliyordu; tek-yönlü okuma tekrar
  eden bug sınıfı: 256/265/267). 937 v0.8.409'un "custom aralık
  asla kalıcı olmaz" kuralını TERSİNE çevirdi: kayıt sessionStorage
  (sekme ömrü) ve `?range=` olarak URL'e yazılır — asıl acı
  pencerenin SESSİZ olmasıydı. Reddedilen: localStorage kalıcılığı
  (haftalar sonra her sayfa geçmişte açılıyordu). Kod:
  `frontend/src/lib/logFilters.ts`,
  `frontend/src/lib/useUrlRange.ts`.
- **v0.8.268 / v0.8.285 (+2026-05 kararı)** — Tema yalnız token
  düzeyi CSS değişkeni: `redhat` paleti (PatternFly renkleri,
  OpenShift tarzı koyu nav) `#sidebar`'a kapsamlı token remap'iyle,
  sıfır bileşen değişikliği, yeni bağımlılık yok, ~1.5 KB CSS;
  285'te varsayılan tema. Grafikler `data-theme` değişince
  değişkenleri yeniden çözer. Reddedilen: Tailwind/shadcn göçü —
  30+ sayfayı yeniden stillemek kozmetik kazanç için haftalar,
  v1.0'a ertelendi. Kod: `frontend/src/styles/globals.css`
  (kaynak: operatör notu).
- **v0.8.428 · v0.8.509-510 · v0.9.67 · v0.9.1267** — Günlük triage
  yüzeyleri KLASİK kalır: Problems kart-feed'i (426) → sıralanabilir
  tablo geri (428; tam-sayfa detay kaldı); exception detayı
  drawer'ı (508) ve v426 düzeni → v0.8.425 klasik düzen (510);
  Operations Elastic-parity tablosu mockup onayına RAĞMEN canlıda
  reddedildi (67); Servis Overview "Tek-Bakış" `?newpage=1`
  bayrağıyla gerçek veride denendi, "eski hali daha iyiydi" (1267).
  Desen: operatör yoğun kullandığı listelerde yoğunluk/kas
  hafızasını estetiğe tercih ediyor; mockup onayı nihai değil.
  Kural: mockup-first + tek-commit'lik revert; 2026-07-30 "red
  geçmişimi overwrite edebiliriz" — hard kısıt değil, gerekçe hâlâ
  geçerli. Kod: `frontend/src/features/anomalies/ProblemDetail.tsx`
  (kaynak: operatör notları, 2026-07-10 → 2026-08-23).
- **v0.9.844 (+v0.10.283)** — Zaman serisi için TEK motor:
  `CorePanel` (`@grafana/ui` üstüne tek sarmalayıcı, altında uPlot);
  `chartsV2` bayrağı, MultiLineChart'ın kendi uPlot gövdesi ve
  DashboardViz SVG motoru söküldü (430 ekleme / 1992 silme).
  Chart.js vb. ağır kütüphane yok; @grafana/* + uplot tam pin
  (283). `'-ms'` senkron ad alanı kalıcı (saniye-eksenli motorlar
  yaşıyor). Reddedilen: eski yolu kaçış kapısı olarak tutmak (bir
  yıldır yalnız `?chartsV2=0`'da çalışıyordu). Kod:
  `frontend/src/components/chart/CorePanel.tsx`.
- **v0.9.1078** — Sayfa düzeyi yüzen/yapışkan şerit YOK: sticky
  kontrol çubuğu, sticky tablo başlığı ve `stickyBottom` Pager
  (v0.9.639/644/645'in denetim kararları) operatör isteğiyle geri
  alındı — görüş alanını yiyor, kaydırmada "oynuyor"; is-fit kaçışı
  yatay taşmayı `#content`'e sızdırıyordu. Kap içi `.is-scroll`
  (drawer) meşru; geniş tablo kendi `.table-wrap`'ında kaydırır.
  Kod: `frontend/src/components/Pager.tsx`, `globals.css` (kaynak:
  operatör notu, 2026-08-16).
- **v0.10.220 · 340 · 347 · 351 · 513** — Traces operatör UX
  kararları: zaman damgası EN SOLDA, sıra **Start time · Service ·
  Name · <attr> · Duration · Status · Spans** (Dynatrace'in sağ
  timestamp'i reddedildi; sabit sıra her kullanıcıda, sunucu yalnız
  EK kolonları saklar); Correlate ve alt Compare kalktı, dış link
  düğmeleri en sağda renkli; Tempo fallback ŞERİDİ kalktı ama
  PROVENANCE "source: Tempo fallback" ÇİPİ kalır (2026-09-07);
  şerit çizgisi p95 varsayılan, `?rt=` kompakt seçici,
  expand/shrink kalktı. Kod: `frontend/src/lib/traceColumns.ts`
  (test sırayı pinler),
  `frontend/src/components/traces/TraceHonesty.tsx`.
- **v0.10.255 + v0.10.257** — ContextBar sırası EnvPicker → kapsam
  (cluster/namespace/service/compare) → **TimeRangePicker sağ
  kenarda, son çocuk** (operatör: "eski yeri sağda daha iyiydi");
  sayfanın kendi kontrolüyle sunduğu boyut çubukta İKİNCİ KEZ
  çizilmez (`hidden` prop; devre dışı kutu bile değil) ve çubuğun
  URL→state effect'i yalnız uygulanan boyutlar için yazılır. Kod:
  `frontend/src/components/ContextBar/ContextBar.tsx`
  (`ContextBar.contract.test.tsx` sırayı pinler).

### Ops / Release

- **v0.9.0 (2026-07-17) → v0.10.1 (2026-08-25)** — Sürüm zinciri:
  her mantıksal birim KENDİ `vX.Y.Z` tag'i (operatör bug'ı derhal
  X+1, asla toplu; bisect tek küçük diff'e iner); v0.8 zinciri
  588'de, v0.9 zinciri 1388'de kapandı. 1.0 kesimi HAZIRDI
  (docs/RELEASE-1.0.md; canlı kapılar prod'da koşuldu, altısından
  beşi geçti) ama operatör "0.10.1 diye devam edelim, 1'e
  geçmeyelim" dedi; dosya silinmedi, ön-durum bloğu o gün yeniden
  ölçülür. Zincir değişince sürüm ÜRETEN makine (release skill'inin
  tag deseni) de çevrilir — aksi hâlde sessiz çatallanma
  (`version_chain_test.go` yönlü kapı). Kod:
  `.claude/skills/release/SKILL.md`,
  `internal/api/version_chain_test.go`.
- **2026-07-28 + 2026-08-25 (operatör kararları)** — Kabul edilen
  güvenlik duruşu, yeniden bulgu diye açılmaz: `/tempo/*` kimliksiz
  kalır; custom roller yalnız tarayıcıda uygulanır; admin config
  export'u saklı kimlik bilgilerini düz metin yazar; prod JWT
  imzalama anahtarı yer tutucu değerde ve ROTASYON REDDEDİLDİ
  (bedeli: tüm açık oturumlar düşer; `/admin/stats` şeridi bu
  yüzden kırmızı kalır). Gerekçe: tek-tenant, kurum ağı içi
  kurulum; operatör işletilebilirliği teorik maruziyete tercih
  ediyor. Kapsam TAM OLARAK bunlar — admin/editor/viewer rolleri
  sunucuda zorlanmaya devam eder, Settings GET asla secret geri
  vermez. (kaynak: operatör notları.)
- **2026-05-19 (operatör kararı)** — Multi-tenant YOK: her tabloya
  `tenant_id`, her sorguya WHERE, cache anahtarı patlaması ve ingest
  yönlendirmesi 3-5 sürümlük refactor; kurulum senaryosu
  (self-hosted, tek kurum, SaaS/MSP hedefi yok) karşılığını ödemiyor.
  "İleride lazım olur" diye kolon/WHERE eklenmez (YAGNI);
  tenant varsayan özellikler (per-tenant fiyat, signup, süper-admin
  çapraz görünüm) de kapsam dışı. (kaynak: operatör notu.)

### Repo / Ekip

- **v0.10.247 + v0.10.625** — `api.go` BÜYÜMEZ: yeni yüzey kendi
  `internal/api/<domain>.go` dosyasında `init() {
  registerRoutesExtra(name, fn) }` defteriyle kaydolur (çift ad
  panic; `buildMux` defteri ad sırasıyla deterministik kurar; Go
  1.22 çakışma testi defteri de kapsar; api.go 5 satır küçüldü).
  625: kural CLAUDE.md'de yazılıydı ama GLOBAL kapı yoktu →
  ratchet: taban `.claude/baselines/api_go_lines` (12113, yalnız
  aşağı iner), `api_go_size_test.go` büyümeyi VE indirilmemiş
  tabanı hata sayar, script + post-edit hook + CI adımı; tabanı
  yükseltmek CODEOWNERS onayı + `Api-Go-Baseline:` trailer'ı ister.
  Hedef: registerRoutes aileler hâlinde taşındıkça ~11000. Kod:
  `internal/api/route_registry.go`,
  `internal/api/api_go_size_test.go`,
  `scripts/guard-api-go-size.sh`.
- **v0.10.613 → v0.10.617-621** — Ekip geliştirmesi + GitHub org
  yayını hazırlık denetimi (`docs/audit/team-readiness-audit.md`):
  repo `cosretr` organizasyonuna taşındı; README/Chart/values/ghcr
  yolları org'a çekildi, Chart appVersion aylar sonra uygulama
  tag'iyle yeniden senkron (617). BİLİNÇLİ değişmeyen: go.mod
  modül yolu ve 622 dosyadaki import'lar (GitHub yönlendirir;
  kanonik yol ayrı karar). Maskeleme politikası koda girdi: gerçek
  prod IP/FQDN fixture'ları TEST-NET'e, LDAP fixture'ındaki gerçek
  kişi/birim adları, gerçek OpenShift iş yükü/küme adları (54
  dosya), kurumun Oracle şema/kolon adları ve Influx artıkları
  SENTETİK (618-621); kurum/müşteri adı, alan adı, gerçek
  şema/tablo adı repoya ve commit mesajına ASLA. Kalan: history
  rewrite (`git filter-repo`) ve kök manifestlerin repo dışına
  taşınması operatörde.
- **v0.10.623-624 · 626-628 · 629** — Katkı altyapısı ve kapılar:
  PR/issue şablonları, CODEOWNERS, .editorconfig, .gitattributes,
  CONTRIBUTING + SECURITY.md; `release.yml`'e `gate` job'ı
  (tsc+build, vet, `CGO_ENABLED=0` build, test, `make audit`) —
  tag push'u artık kırmızı main'den imaj basmaz (o gün üç kez
  basmıştı); ESLint gerçek kapı (0 hata); `go mod tidy -diff`;
  gofmt borcu tek mekanik sweep'te kapandı (86 dosya; dört
  toolchain aynı listeyi verdi — sapma değil borç) + CI gofmt
  kapısı; Trivy/npm audit HIGH'ları ve govulncheck pini
  (615/616/622). Reddedilen: go.mod'a `toolchain` satırı (operatör
  kararı; CI zaten 1.25.x). Onboarding: `docs/ENV.md` (89
  `COREMETRY_*`, yokken davranış + rol + secret bayrağı),
  `docs/local-dev.md`. Kod: `.github/workflows/ci.yml`,
  `.github/workflows/release.yml`, `.github/CODEOWNERS`.

## 2026-09-23 — Dış seri Problem'leri kaynak yaşarken süpürülmez (v0.10.900; v0.10.592 kararının tersi)

**Karar:** `anomaly:ext:<kaynak>/…` (seri) ve `anomaly-cluster:ext:<kaynak>/…` (küme)
satırları, kaynak etkin olduğu sürece evaluator'ın bayat süpürmesinden MUAF (ext-down /
ext-cap ile aynı `PollerOwnedSubject`). Yaşam döngüsü tarayıcıda: sayaç her poll'da aktif
anahtarlara sıfır yazar (v0.10.893), tarayıcı resolve eder; kaynak gerçekten susarsa ext-down
(v0.10.588) söyler; kaynak silinince muafiyet düşer.

**Neden 592 tersine döndü:** 592'de "seri bilerek süpürülür — 'source silent' dürüst sinyal"
denmişti. Oracle kaynağı aralığı 3600 s'ye kadar izinli; touch yalnız poll anında atılır;
süpürme eşiği 3×1 dk. >3 dk aralıkta her poll seri Problem'i "source silent" diye kapanıp
yeniden açılıyordu — canlı kipte alarm seli, gerekçe de yalan (kaynak susmamıştı). Dense
sıfır yazımı geldiğinde süpürmenin işi kalmadı.

**Kapsam:** yalnız dış hat. RED anomalileri (`anomaly:<svc>:<metric>`) süpürülmeye devam
eder; onların histerezis touch'u v0.10.889.

## 2026-09-23 — Forecast çekirdeği: "zaten limitte" bir DURUMDUR, kararı çağıran verir; disk "kaç gün" rozeti Problem satırından beslenir (v0.10.901; parite #6 dilim 1+2)

**Karar 1 — `forecast.StatusAtLimit`:** `internal/forecast.Fit` regresyon doğrusu limitin
üstündeyse tahmin yerine `StatusAtLimit` döner; anlamı ÇAĞIRAN verir. `capacityETA` (DB
kapasitesi) → tahmin yok (eşik dalı zaten alarmı açar; ikinci bir "0 saat" cümlesi gürültü),
`diskETADays` (CH diski) → 0 gün, ok=true (dolu diskte susmak alarmı kaçırmaktı; v0.9.1279).
İki test dosyası (capacity_eta_test, selfhealth_test:176) zıt sözleşmeyi pinler ve ikisi de
delegasyon sonrası DEĞİŞMEDİ. Paket bu ikiliği tek davranışa indirmez — birleştirilecek her
yeni çağıran (dilim 3 chip, dilim 4 Hosts/Clusters/heap) kendi çevirisini açıkça yazar.

**Karar 2 — kayan-nokta sırası pinli:** paket iki eski çekirdeğin sx/sy/sxx/sxy birikimini,
den/slope/intercept'i, SSres/SStot döngüsünü ve lastFit'i BİREBİR aynı sırada hesaplar;
`linear_test` eski gövdeyi `==` ile karşılaştırır (ufuk sınırı ±ulp dâhil). Eski testler kaba
(aralık kontrolü / 0.001 tolerans) olduğundan "temizlik" onlardan sessizce geçerdi. Tek
bilinçli sapma: NaN/±Inf örnek "geçersiz" (eskiden ok=true + "~NaNh").

**Karar 3 — "kaç gün kaldı" rozeti Problem satırından, yeni yüzey/polling YOK:** /admin/stats
disk rozeti yalnız `OpenProblemsSnapshot`'taki çözülmemiş `self-disk-eta` satırını okur
(sayı = `Problem.Value`, gün). Gerekçe: seri evaluator belleğinde ve yalnız liderde
(`COREMETRY_MODE=api` pod'unda sıfır); satır her pod'dan okunur, snapshot 5 s memo'lu, zarf
zaten 60 s önbellekli → ek sorgu sıfır. Bedeli dürüstçe beyan: chip yalnız eşiğin (7 gün)
altında görünür, "tahmin var ama problem yok" hâli görünmez (dilim 4: kalıcı seri). Satırda
olmayan sayı (R², band) chip'e YAZILMAZ.

**Karar 4 — rozet tonu SAYIDAN, satır ciddiyetinden değil:** `Severity` yaş eskalasyonuyla
30 dk'da critical'a çıkar (Inbox sözleşmesi, self-disk-eta muaf değil). "Kırmızı = 2 günden
az" cümlesi ancak `days < SelfDiskCriticalDays` ile doğru kalır; `severity` payload'da kalır
ama ton için okunmaz.

**Karar 5 — lider değişiminde açık satır taşınır:** bellek-içi disk serisi yeni liderde
boşken (ısınma: <4 örnek ya da <30 dk) açık satır son değeriyle yeniden sunulur
(`diskCarryOver`; v0.9.1294 volCache "son ölçümü yeniden sun" deseniyle aynı gerekçe).
Aksi hâlde her deploy bir sahte "çözüldü" + 30 dk sonra mükerrer bildirim üretiyordu.
Isınma dışında "eğilim yok" satırı yine kapatır — taşıma bir yaşam döngüsü uzatması değil,
kör pencerenin köprüsüdür.

## 2026-09-25 — CH disk doluluğu kalıcı seri olarak yazılır (v0.10.911; v0.9.1279 kararının tersi)

**Karar:** Evaluator lideri her tikte zaten okuduğu `system.disks` değerini `metric_points`'e
`coremetry.self.disk_used_bytes` gauge'ı olarak da yazar (service_name `coremetry-self/<disk>`
tek düğümde, `coremetry-self/<host>/<disk>` kümede — rollup şeması attr'ları katladığı için
kimlik service_name'de). /admin/stats disk rozeti açık problem YOKKEN son 7 günün saatlik
serisinden (rollup_metrics_1h) `forecast.Fit` ile tahmin eder (ufuk ≤30 gün, R² ≥ 0.6).

**Neden 1279 tersine döndü:** 1279 "kalıcılaştırmak yeni tablo + yeni yazma yolu demek, 6
saatlik bellek penceresi yeter" demişti. Yeter değildi: rozet yalnız 7 günün altındaki açık
problemden besleniyordu ("20 gün sonra dolacak" hiçbir yerde yoktu), deploy'da 30 dk kör pencere
vardı, mevsimsellik imkânsızdı. Yeni TABLO yok (metric_points + otomatik rollup); hacim dakikada
düğüm × disk satırı. Operatör onayı 2026-09-25 (parite #6 dilim 4 Karar 1).

**Kapsam:** self-disk-eta KURALI hâlâ bellek serisinden karar verir (davranış değişmedi; yazım
hatası kuralı etkilemez). Hosts/Clusters çipi ve heap ETA bu karara dahil değil. Haftalık
mevsimsellik ≥14 gün tarihçe biriktikten sonra ayrı adım.

## 2026-09-25 — Buton bütünlüğü: shadcn/Tailwind DEĞİL, mevcut atomlar (Seçenek B, v0.10.919)

**Karar:** Buton/bileşen tutarlılığı mevcut `components/ui` atomları üzerinde kurulur;
Tailwind + shadcn/ui eklenmez. Operatör onayı 2026-09-25 ("Onay", audit:
`docs/frontend/shadcn-audit.md` §6).

**Neden:** Sorun görünüm farkıydı (sayfaya özel ham düğmeler), stil motoru değil. Tailwind
4.3k satırlık global CSS'in yanına ikinci bir stil sistemi getirirdi; `button.sm` gibi
eleman kuralları (0,1,1) tek Tailwind yardımcısını (0,1,0) sessizce yener. İstenen
özelliklerin çoğu zaten vardı (zorunlu `variant`, `loading`, IconButton'da tip düzeyinde
`aria-label`); A seçeneği bunları yeniden yazıp ~700 çağrı yerini değiştirecek, ~20–35 KB gz
ekleyecekti. Üç tema (`data-theme` dark/light/redhat) shadcn'in `.dark` sözleşmesine uymuyor.

**Ne geldi (Seçenek B temeli):** ESLint `ui/no-raw-button` (+ `eslint-suppressions.json`
mevcut 102 ihlali sayar, yalnız azalır; istisna `-- gerekçe` ister), `ButtonGroup`,
`Tooltip` (ağaçta `position: fixed` — `--z-tooltip` çekmece/modal altında kaldığı için
portal değil), yüklenirken genişlik koruyan `Button`, yalnız geliştirmede `/design` kataloğu.

**Geri dönüş:** Atom arayüzleri korunduğu için ileride shadcn'e geçiş atomların İÇİNİ
değiştirmek demek; çağrı yerleri değişmez. Ters yön (A'dan B'ye) pahalı olurdu.

## 2026-09-25 — Sade palet: renk yalnız sapma, seçim ve veri için (v0.10.920 adım 0)

**Karar (operatör, mockup üzerinden):** K1 açık tema beyaz kart / gri sayfa (ters merdiven kapandı);
K2 koyu temada uyarı rengi sakin altın `#f3b94c`; K3 grafik serisi rengi isimden gelmeye devam eder
(açıklama/ipucu zorunlu); K4 redhat PatternFly kimliğini korur (nötrler, `#0066cc`, kare köşeler,
koyu kenar çubuğu) ve yalnız geçmeyen çiftler PF'nin kendi metin tonlarıyla düzeltilir
(uyarı `#795600`, form kenarı `#8a8d90`, hover `#004080`); K5 sağlıklı durum nötr, yeşil yalnız
"düzeldi" (adım 1); K6 Coremetry kırmızısı yalnız logoda (adım 1).

**Neden:** Varsayılan redhat temasında uyarı metni 1,99:1, bilgi 2,99:1, kenar çubuğunda seçili
menü 2,62:1 idi; bugünkü tokenlarda 130 çift WCAG'den kalıyordu. Rengin çoğu "sağlıklı" durumu
işaretliyordu. Değerler OKLCH ile karşıtlık hedeflerine göre çözüldü; 296 denetim, 0 başarısız
(kalan 5 uyarı: renk körlüğünde ok/uyarı/hata ayrımı AA içinde çözülemiyor → durum her zaman
ikon/yazıyla).

**Adım 0 kapsamı:** yalnız token değerleri + yeni tokenlar (`--accent-hover`, `--focus`,
`--*-bg`, `--err-solid`, `--warn-solid`) + kural düzeltmeleri + grafik paleti (durum renklerinden
OKLab ≥0,10). Rol ayrımı: `--accent` dolgu / `--accent2` metin; `--warn` metin / `--warn-solid`
dolgu (açık temalarda metin kahvesi renk körlüğünde kırmızıya yaklaşıyordu). İnceleme turu
(3 mercek + kuşkucu) 14 gerçek bulgu çıkardı; hepsi düzeltildi ve `styles/contrastTokens.test.ts`
tüm WCAG çift tablosunu artık doğrudan globals.css'ten denetliyor.
Bileşenlerden renk çekme (adım 1), yapı sadeleştirme (adım 2), hex/ton ratchet'i (adım 3) ve
yazı ölçeği (adım 4) ayrı onayla. Mockup: claude.ai artifact "Coremetry Sade Palet".

## 2026-09-26 — Sade palet adım 2: yapı sadeleştirme (v0.10.928)

**Karar (operatör: "Önerini yapalım"):** Y1 kartlar sabit — hover'da mavi kenar ve el imleci yok;
yalnız gerçekten bir yere giden kart `CardLink` (gerçek `<a>`, hover/odakta `--border-strong`).
Y2 iç ayırıcılar yeni `--divider` tonunda (bg1'e karşı 1,21–1,27:1; dış çerçeve `--border`
kalır). Y3 tablo başlıkları sade: `--fs-xs` (11px; 11,5 merdivende yok), 600, `--text2`,
büyük harf ve harf aralığı yok; küçük harfli doğal dil etiketleri cümle düzenine çekildi
(tanımlayıcı/birim olduğu gibi). Y4 ikincil buton dolgusuz: şeffaf + `--border-control`
(bg1'e ≥2:1), hover bg2 + `--border-strong`, basılı bg3; içeriğin ÜSTÜNDE yüzen ikinciller
`is-overlay` ile opak.

**Neden:** Rengin ve çizginin çoğu yapıyı değil gürültüyü taşıyordu: 76 statik kart
tıklanabilir gibi davranıyordu, başlıklar büyük harfle bağırıyordu, 325 ikincil buton
birincil kadar dolguluydu. Kapılar: `contrastTokens` iki yeni tokenı her temada ölçer,
`paletteStep2.pin` yapıyı çiviler; `.card-tight` (hiç uygulanmamıştı) kural, prop ve çağrı
yerleriyle birlikte kalktı.

## 2026-09-26 — Tablo standardı: dört tür, sessiz satır, renk yalnız sapmada (v0.10.930→)

**Karar (operatör: "önerini yapalım mockup gördüm uygundur", mockup "Coremetry Tablo
Standardı"):** T1–T12 ve sekiz açık soruda önerilenler. T1 dört tür tablo (DataTable,
gerekçeli statik tablo, KeyValue atomu, muaf lejant/sohbet); T2 satırın dört hâli, hover ve el
imleci yalnız tıklanabilir satırda; T3 sessiz başlık; T4 sayılar arayüz fontunda (günlük dört
tablo dahil); T5 tek yazı boyu, hiyerarşi renkle, monospace yalnız kimlik/kodda; T6 `--row-h`;
T7 bir satır bir eylem, gezinen satır gerçek bağlantı; T8 tek eylem sütunu (⋯ hep görünür,
soluk); T9 renk yalnız sapan değerde, durum sütunu nokta + metin; T10 tek çerçeve; T11 hücre
tek satır; T12 durumlar tablonun içinde. Yoğunluk 4 → 3 basamak; "Kolonları sıfırla" başlık
satırının ⋯ menüsünde; varlık başına tek açılış hedefi.

**Neden:** ~195 tablonun iskeleti ortaktı, gürültü detaydaydı: satırların %74'ü tıklanamadığı
hâlde el imleci alıyordu, 705 sütunda boşta ok, 284 monospace sayı hücresi, 405 satır içi hücre
stili (yoğunluk hücrelerin yarısına ulaşmıyordu), 13 farklı satır yüksekliği tahmini, 5 ayrı
satır-tıklama yolu, 26 dosyada kutu içinde kutu. Envanter iki gerçek hata da buldu (v0.10.930
SLO kırpma, v0.10.931 çizilmeyen hata rengi).

**Göç:** dilim 0 kapılar + hatalar, dilim 1 tek CSS sürümü, dilim 2 primitif, dilim 3 sayfa
süpürmeleri, dilim 4 durumlar. `tableUnityRatchet` tabanları yalnız azalır.


## 2026-09-26 — CoSRE değerlendirme paneli: donmuş vakalar Ayarlar'daki modelle sunucuda koşar (v0.10.940)

**Karar (operatör: "bunu settingsteki modelden alsa direkt", "Ücretli sağlayıcı yol local llm
modeline bağlıyız", mockup "CoSRE Değerlendirme Paneli" + "Ok"):** K1 Ayarlar › CoSRE içinde
"Değerlendirme" alt sekmesi (`?tab=eval`, yalnız admin). Koşu, binary'ye gömülü evalset'i
(`internal/copilot/evalset/*.json`) sunucuda, her yüzeyi üretimin o yüzey için seçtiği
profille (eşleme > grup kardeşi > varsayılan) ve üretimin çağrı yoluyla (`aiCall`) koşar;
onay adımı ve ücretli sağlayıcı uyarısı YOK. K2 çağrılar `ai_calls`'a `evalset-<Yüzey>`
yüzeyiyle yazılır; /ai sayaçları, seri, çağrı listesi ve bütçe bu satırları varsayılan olarak
HARİÇ tutar, `?source=evalset` ("Kaynak: Değerlendirme") yalnız onları gösterir — yeni kolon
yok, ayrım tek önek sabitinde (`chstore.AICallEvalsetSurfacePrefix`). K3 listede son 20 koşu;
fiziksel silme `ai_eval_runs` tablosunun 180 günlük TTL'i (mutasyon yok).

**Neden:** Vakalar yalnız CI dışı bir CLI koşumuyla ölçülebiliyordu; prompt denetiminin yeni
vakaları hiç koşulmamıştı. Puanlama ve vaka yolu CLI ile ORTAK (`runEvalsetCase`), böylece
panel ile `go test -tags evalset` aynı şeyi ölçer; CLI özel Service'iyle sıcaklık 0'da ve
`ai_calls`'a yazmadan kalır. Aynı anda tek koşu (süreç + taze `running` satırı), 15 dk
güncellenmeyen koşu "yarım kaldı" sayılır; son yazım başarısızsa sonuç bellekte tutulur.

## 2026-09-26 — CoSRE araştırma asistanı, Faz A: kaynak durumu, eşleme, hesap doğruluğu (v0.10.944)

**Karar (operatör: CoSRE trace/log/metrik kaynaklarını birlikte inceleyen, kanıt gösteren ve takip
sorularıyla süren bir asistana dönüşsün; "yalnızca plan sunma, uygula"):** Mevcut sağlayıcı ve
mimari korunur; araçlar sohbet ile MCP'nin ORTAK kayıt defterinde kalır. Her araç sonucu veriden
ayrı bir kaynak durumu taşır (`internal/sourcestate`: ok · empty · unreachable · unauthorized ·
timeout · partial · delayed · truncated · not_configured); erişilemeyen kaynak boş sonuç gibi
görünmez, "log yok" "hata yok" diye okunmaz. Yeni araç hata sınıfı `unauthorized` (401/403: tekrar
deneme, kapsam dışı olduğunu söyle). `search_logs` / `query_metric` / `get_trace` yenilendi,
`list_log_fields` / `list_metric_labels` / `compare_periods` eklendi (57 → 60 araç). ES alan ve VM
etiket eşlemesi yapılandırılabilir (servis, ortam, cluster, namespace, pod, sürüm); öncelik
yapılandırma > keşif > yok ve araç hangisini kullandığını söyler. Trace↔log eşleşmesi kimlikle
ise `match=trace_id/span_id`, servis/pod/zamanla ise `contextual`; trace kimliği metrik etiketi
olamaz (exemplar). Karşılaştırma tüm pencere yüzdeliği kullanır (p95 ortalaması YOK), çok ortamlı
serviste env ister, trafik karışımı / örnek sayısı / kapsama / örnekleme notu taşır; kritik yol
toplamı iç içe süreleri toplamaz. Çekmecede görünür bağlam şeridi (trace, span, servis, ortam,
cluster/namespace, pencere) her turda gönderilir ve konuşmayla saklanır.

**Neden:** Haritalama (6 okuyucu) mevcut araçların kaynak hatalarını sessizce boş sonuca, 401/403'ü
`internal`'a çevirdiğini; alanların kodda sabit aday listeleriyle çözüldüğünü; kritik yol
toplamının ve iki AI karşılaştırma yolunun (aggRED, guided window_compare) yanlış hesapladığını
gösterdi. **Sınırlar (mevcut kararlar):** üründe veri maskeleme yok (maskeleme collector'da;
yalnız DataNotInstruction çerçevesi ve FenceSafe); tenant modeli yok (rol tabanlı yetki, yeni
araçlar viewer, REST eşleriyle aynı). Faz B: seçili trace için kanıta dayalı inceleme akışı ve
takip sorularının araç döngüsüne bağlanması.

## 2026-09-26 — Exception varsayılan tabanı 5; çok servisli ve regressed gruplar muaf (v0.10.949)

**Karar (operatör: "aynı anda farklı servislerden gelmiyorsa 5'ten düşük exception'ı göstermeye
gerek yok; tek servisten gelen 5'ten küçükleri göstermeyebiliriz"; regressed sorusuna "görünür
kalsın"):** Inbox ve /problems varsayılan tabanı 2 → 5 (v0.10.740 kararının yerine). İstisnalar:
(1) aynı exception (tür + normalize mesaj; fingerprint servisi içerdiği için değil) aynı anda
(etkinlik aralıkları storm penceresi payıyla — varsayılan 10 dk — çakışan) ≥2 serviste görülüyorsa;
(2) regressed gruplar (öncelik koduyla aynı `state` kaynağı); (3) P1 eşiği 5'in altına çekilirse
taban ona iner. Açık `?minOcc=N` ve `minOcc=0` (hepsi) aynen. Şerit gizlenenleri ve muafiyetle
tutulanları sayar; muaf satırda "N servis" işareti.

**Neden:** Tek servisteki birkaç oluşum gürültüydü; aynı hatanın birden çok serviste aynı anda
görülmesi ve geri dönen hata ise sayı küçük olsa da sinyal. Mesajsız / yalnız kimlikten oluşan
exception'lar muafiyet anahtarı almaz (genel tür filoda her an bir yerde tekrarlanır).

## 2026-09-26 — PromQL konsolu (Thanos): editor+, audit_log, pod başına sınırlar (v0.10.950–953)

**Karar (operatör: Faz 0 denetimi docs/promql-console/audit.md, "4 onay"):** Uzak küme başına
Thanos üzerinde salt okunur PromQL konsolu; `/api/promql/*` kendi rota dosyasında (api.go büyümez).
Önerilerin tamamı onaylı: editor ve üstü (özel roller sunucuda kısıtlayamadığı için viewer değil);
her sorgu `audit_log`'a `promql.query` (başarılı, hata, reddedilen; otomatik tamamlama değil);
kullanıcı başına eşzamanlılık ve dakikalık sınırlar v1'de POD BAŞINA; `partial_response=false`;
7 günü aşan aralık REDDEDİLİR (kırpılmaz); otomatik adım grafik genişliğine göre, elle adım taban
ve 11k nokta tavanına yükseltilir; seri tavanı 500 + toplam sayı, gövde tavanı ayar (32 MiB) açık
hatayla; sorgu geçmişi sunucuda, kullanıcı başına son 50 (saved_views blob'u, localStorage değil);
paylaşılan querier kipinde küme etiketi yorum/dizgi güvenli eklenir, match[]'e de eklenir.
shadcn/Tailwind yok (2026-09-25 buton kararı): mevcut atomlar ve CorePanelMulti.

**İkinci Thanos okuyucusu:** `internal/promapi` başlığındaki "yeni çağıranlar promapi kullanır"
kuralına rağmen konsol okuyucusu `internal/thanos/console.go`'da (belirteç çözümü ve eşleştirici
ekleme o pakette, `effectiveTokenFor` dışa kapalı; onaylı denetimin planı). Eski `doQuery`'e ve
`promapi`'ye dokunulmadı. Rollouts v2 aynı taşıma üzerinden `WorkerQuery` girişi ekleyecek
(docs/rollouts/v2-audit.md §3.4) — üçüncü bir okuyucu yazılmaz.

## 2026-09-26 — Rollouts v2 Faz 1: KSM doğruluk kaynağı, Argo hub, ayrı durum tabloları (v0.10.955–960)

**Karar (operatör: Faz 0 denetimi docs/rollouts/v2-audit.md, "3 onay" = §12.3 karar 1–10,
"24 25 onay"):** Önerilerin tamamı onaylı.
1. İşçi okuyucusu PromQL konsolunun taşıması üzerinde tek dışa açık giriş: `thanos.WorkerQuery`
   + `WorkerLimits` (konsol yeniden platformlanmaz; `doQuery` ve `promapi` dokunulmaz;
   çözülemeyen `TokenRef` → istek gönderilmez).
2. İşçi tavanları: çağrı başına ≤ 50 000 seri, ≤ 64 MiB gövde, 30 sn (tavan 45 sn).
3. v1 KSM bacağı taşınmaz (v2 onun yerini alır).
4. Filtre `?trigger=`; kolon "Kaynak"/"Source" YENİ kolon kimliğiyle; API alanı `trigger`.
5. Argo hub sıradan bir Remote Cluster. Ortamda Argo CD İKİ hub kümesinde kurulu (operatör,
   2026-09-26): blobda `hubs[{clusterId, injectClusterLabel}]` (etiket ekleme hub başına), her
   instance kendi `hubClusterId`'sini taşır;
   `https://kubernetes.default.svc` o instance'ın kendi hub'ına çözülür, keşif hub başına koşar.
6. Sekiz tablo (`rollout_events`, `rollout_workload_state`, `argocd_app_status`,
   `argocd_sync_events`, `argocd_app_mapping`, `rollout_classification`,
   `ado_commit_enrichment`, `rollout_worker_runs`); TTL 180 g / 400 g (durum) / 30 g
   (eşleme, koşu kaydı). `workload_rollouts` genişletilmez (kimliği span türevi, iki yazarlı).
7. `apiServerUrls` liste (normalize: küçük harf şema+host, sondaki `/` yok, port yoksa
   `:6443`); `pairGroup` serbest metin; küme başına tek `argoSuffix`.
8. Dedektör tick'i 30 sn.
9. `rollout_events` anahtarında `incarnation_at` (CMO denylist'i `_created`'ı düşürüyor;
   sil/yeniden yarat geçmişi ezmesin).
10. İlk görülen iş yükü için `change_type='initial'` olayı; hiç koşmamış ilk tur yalnız taban.
24. Purge: sekizi de telemetri sınıfı, `argocd_sync_events` HARİÇ — o config gibi korunur
    (Argo kendi geçmişinde yalnız son 10 kaydı tutar; silinen geri gelmez).
25. `0015`'te ZooKeeper yolu `0012` gibi sabit `/clickhouse/tables/state/<ad>`; boot'un çalışma
    zamanında çözdüğü önekten farkı migration başlığında yazılı.

**Neden:** KSM her iş yükünü görür (span'i olmayan dahil), span türevi v1 görmez; Argo ve Azure
DevOps kanıtı geç gelir ve ayrı yazarlıdır, bu yüzden olay satırına kopyalanmaz, okurken
birleştirilir. **Faz 1 davranış değiştirmez:** ayarlar, okuyucu, tablolar; hiçbir işçi başlamaz,
bayraklar kapalı. Faz 2–5 her biri kendi §11 sorgu paketi yanıtlarını bekler.

## 2026-09-27 — On state tablosu birleşik ZK yolunda yeniden kurulur: sihirbaz, veri kaybı kabul, taşıma yok (v0.10.965)

**Karar (operatör: "sihirbaza ekle … düzeltmesi ve kontrolü"; "2 için önerin a"; "3 için de data
kaybı önemsiz"):** Prod'da (v0.10.960, 4 host / 2 shard) on state tablosu eski shard'lı ZK yolunda
(`/clickhouse/tables/<shard>/<ad>`) ve her biri iki replikasyon grubuna bölünmüş: `ingest_ledger`,
`ai_eval_runs` ve sekiz Rollouts v2 tablosu (`rollout_events`, `rollout_workload_state`,
`argocd_app_status`, `argocd_sync_events`, `argocd_app_mapping`, `rollout_classification`,
`ado_commit_enrichment`, `rollout_worker_runs`). Denetim ve düzeltme Admin → ClickHouse → Replika
tutarlılığı kartına eklendi (Rollouts kartına değil; oradaki 0015 ön kontrolü bağımsız son kontrol
olarak kalır). Düzeltme on tablonun HEPSİ için DROP (`ON CLUSTER … SYNC`) ve birleşik yolda
(`<önek>/state/<ad>`, replika `{shard}-{replica}`) CREATE; önce hepsi düşer ve her host'ta gittiği
yoklanır, sonra hepsi kurulur ve doğrulanır.
- `ingest_ledger` (türev sayaç defteri): en fazla 30 günlük filo defteri geçmişi gider (öneri a).
- `ai_eval_runs` (normalde korunan sınıf): evalset koşu geçmişinin kaybı kabul; çalışan koşu varsa ret.
- Sekiz Rollouts v2 tablosu yalnız BOŞKEN ve yazıcılar kapalıyken (source v1, argocd kapalı).
- Veri TAŞINMAZ (0009 usulü birleştirme yok). Onay planın ölçüm anını ve tablo başına satırı taşır.

**Onaylanmayan:** boot kuralı değişikliği (hiç var olmayan yeni state tablosunu eski yolda başka
tablo olsa bile her zaman birleşik yola kurmak — `useUnifiedStatePath` kural 3). Açık öneri olarak
duruyor; kural değişmedi. Bu yüzden izin listesi dışında eski yolda kalan bir state tablosu kilidi
kapalı tutar ve sihirbaz kısmi seçimi ancak açık onayla (`partialOK`) koşar.

**Neden:** Kural 3 yüzünden tek bir eski yollu state tablosu (ilk `ingest_ledger`, v0.10.767)
sonradan eklenen her state tablosunu bölünmüş doğurdu. Hepsi birleşik yola geçince kural 4 devreye
girer ve kilit her pod'un bir sonraki açılışında kendiliğinden açılır.

## 2026-09-27 — Rollouts v2: açık kararlar 11–23 önerilerle (16 hariç); Argo CD ayar sekmesi onaylı

**Karar (operatör: "Onay rollout için önerin"):** Argo CD ayar sekmesi mockup'ı onaylı (yapımı sürüyor;
boş tokenRef kayıtlıyı korur, keşifte uygulama/shard sayısı, kayıtlı instance kimliği salt okunur,
instance bağlı hub kaldırılamaz). docs/rollouts/v2-audit.md §12.3'ün 11–23 kararları audit'in
önerisiyle: 11 Argo Rollouts dilimi yalnız §11 R bulursa; 12 DeploymentConfig "kapsanmıyor" (pay
anlamlı değilse); 13 JBoss/k8s-dışı için span'den çıkarılan sürüm "çıkarım" etiketli yedek, KSM
olaylarına karışmaz; 14 eski `?rollout=` bağlantıları TTL'e dek eski tablodan, yenisi 6 parçalı
anahtar; 15 metrics-only modda tahmin gösterilir, açıkça etiketli; 17 tetikleyici Problem puanına
girmez, yalnız kanıtta; 18 v2'de Problem/olay üretilmez; 19 RecentDeploy kaynağı P2.5'te
rollout_events; 20 DevOps PAT önce tokenRef, Build (Read) yalnız pipeline commit status
yazmıyorsa; 21 ikinci atlama P4'te §11 V sonrası isteğe bağlı; 22 out_of_band aktör göstermez;
23 etki okumada hesaplanır (P5). **16 (operatör, 2026-09-27: "servis adı eki olsun ama env
bulamazsa env parçacığına da baksın"):** ortamın asıl kaynağı servis adı eki (`-prod/-int/-uat/-prep`);
ek yoksa ya da tanınmazsa Argo uygulama adındaki `<env>` parçası. `envList` bu ek sözlüğüyle hizalı.
26–30 karar değil, ortam bilgisi (§11). Hiçbiri P2.2'den önce koda girmez.

## 2026-09-27 — Argo CD ayar sekmesi: dört arka uç kuralı (v0.10.968)

**Karar (operatör: "Onay rollout için önerin" — mockup onayı bu dört arka uç değişikliğini de
kapsar; kod `internal/argocd/put.go`, `discover.go`, `internal/api/argocd_settings_routes.go`):**
1. **Boş `tokenRef` kayıtlıyı korur.** PUT'ta instance başına (id kırpılmış): dolu ref değiştirir
   (`secretref.Valid`); boş ref + id kayıtlı → kayıtlı ref kopyalanır; boş ref + istek-yalnız
   `clearTokenRef: true` → kaldırılır; dolu ref + `clearTokenRef: true` → 400
   `instances[i].clearTokenRef`; bool olmayan bayrak (null dahil) → 400 aynı yolda; kayıtlı
   olmayan id → ref yok, bayrak etkisiz. `clearTokenRef` `Instance` alanı DEĞİL: kalıcı blobda,
   GET/PUT cevabında ve audit'te görünmez. FE yeni satırın boş ref'i için `clearTokenRef: true`
   gönderir (aynı kimlikli silinmiş satırın ref'ini miras almasın). Config import'la bozuk gelmiş
   kayıtlı ref PUT'ta yeniden yazılmaz (400 `instances[i].tokenRef`, değer yankılanmaz). PUT
   birleştirmeden önce kalıcı blobu best-effort yeniden yükler: B pod'undaki kayıt A pod'unun az
   önceki kaydına karşı birleşir (hata loglanır, bellekteki blobla sürülür).
2. **Kayıtlı instance kimliği salt okunur — RED, örtük sil+ekle değil.** Kayıtlı bir
   `(hubClusterId, hubNamespace)` yuvasını, o yuvanın kayıtlı id'si gövdede yokken yeni bir id
   alırsa 400 `instances[i].id`; mesaj eski ve yeni id'yi, hub/ns'yi ve çareyi söyler (önce eski
   satırı kaldırıp kaydet, sonra yeni kimlikle ekle). Açık bir "sil+ekle" bayrağı yok: iki ayrı
   kayıt yeterince açık. İzinli: kayıtlı id'yi başka hub'a taşımak, silmek, boş yuvaya yeni id,
   taşınan (gövdede duran) id'nin boşalttığı yuvaya yeni id. Namespace'i de değişen yeniden
   adlandırma sil+ekle'den ayırt edilemez ve izinli; pinleri `pins[i].instanceId` korur. Gerekçe:
   id Faz 3'ten itibaren ClickHouse `instance_id`; sessiz değişim geçmişi yetim bırakırdı.
3. **Instance'ları bağlı hub kaldırılamaz.** `Validate`'ten ÖNCE ön denetim: kayıtlı bir hub
   `hubs`'tan çıkarılırken ona bağlı instance gövdede duruyorsa 400 — yol bugünkü
   `instances[i].hubClusterId` (mockup'ın kayıt hatası listesi ve mevcut test pini), mesaj hub adı
   + id, bağlı instance sayısı ve çare (taşı ya da hub'ı bırak). Aynı PUT'ta instance'ları taşımak
   kaldırmayı serbest bırakır. Genel "hubs listesinde değil" mesajı da artık çareyi söyler.
4. **Keşifte uygulama ve shard sayısı.** Aday bulma label-values'ta kalır. Adaylar kurulduktan
   SONRA ikinci tur: uygulama sayısı iş başına TEK anlık `count by (namespace) (group by
   (namespace, exported_namespace, name) (argocd_app_info{job="J"}))` (iş genelinde
   `exported_namespace` yoksa by'sız `count(...)`); shard sayısı sınırlı `pod` label-values (≤100,
   1 sa pencere; kesikse alt sınır, FE "≥100"). Onay metni "label-values çağrıları" diyordu; onaylı
   mockup dipnotu ve audit §5.4 izlendi: kural 1–3 anlık sorgu, listelemeden önce sayım ve
   kopyaları gruplama ister (shard/HA pod'ları çarpmaz); kural 4 `name` üzerinde label browser'ı
   yasaklar; §5.2'ye göre `name` tek başına durum C'yi eksik sayar. Sonuç ≤100 seri (iş başına
   namespace tavanı), ~40k serilik metrikte güvenli. **Bütçe:** sayım çağrıları aynı ≤150 çağrı /
   60 sn bütçesinden sayılır ve yalnız ARTANI kullanır (adaylar önce kurulur; sayım hiçbir adaya
   mal olmaz). Sayım hatası `error`/`incomplete` yazmaz, adayı düşürmez, 200'ü bozmaz; kısa neden
   `countNote`'ta. Bütçe biterse kalan adaylar "sayım atlandı: keşif bütçesi doldu" notu, sonuç
   `countsIncomplete` alır (audit details'te de). Çağrı sayısı iş başına 2'den ~4'e çıkar.

**Açık kalan (ertelendi):** iki admin arasında bütün-blob kayıp güncellemesi (son yazan kazanır);
hub Thanos 401/403 ayrı bir "yetki yok" durumu yerine "erişilemedi" + sabit metin (test pinli).

## 2026-09-27 — Runbook bash adımları Coremetry'nin sırlarını devralmaz (v0.10.966)

**Karar (operatör: "Önerini yapalım" — öneri 1, alt önerilerle):** bash adımı yalnız bir taban env
izin listesini görür (PATH, HOME, LANG/LC_ALL, TZ, TMPDIR, USER, HOSTNAME, sertifika yolları,
KUBECONFIG/KUBERNETES_SERVICE_*, NO_PROXY); proxy değişkenleri yalnız parola taşımıyorsa geçer;
`COREMETRY_AGENT_ENV_PASSTHROUGH` ile açıkça listelenen adlar geçer (tam ad; token benzeri ad
boot log'unda işaretlenir), `COREMETRY_*` ASLA. Linux'ta süreç her rolde dump edilemez
(PR_SET_DUMPABLE=0): aynı uid'deki çocuk `/proc/<pid>/environ`'u okuyamaz. Zaman aşımında ve adım
sonunda tüm süreç grubu öldürülür; boru tutan arka plan süreci 2 sn sonra kesilir, adım başarılı
sayılır ve çıktıya not düşülür. **Neden:** varsayılan all-mode'da editor rolündeki biri JWT secret'ı
(→ admin oturumu) ve CH parolasını adım çıktısına basabiliyordu (Helm incelemesi, v0.10.958).
**Kalan risk (belgeli):** aynı uid'in okuyabildiği dosyalar (config, bağlı secret'lar, SA token),
iç ağdaki CH/Redis; gerçek sınır ayrı agent rolü, ileride ayrı uid/sidecar.

## 2026-09-27 — Boot: hiç var olmayan yeni state tablosu her zaman birleşik ZK yoluna (öneri 2)

**Karar (operatör: "Önerini yapalım" — öneri 2):** `useUnifiedStatePath` kural 3 kaldırılır: kümede
HİÇBİR düğümde olmayan bir state tablosu, eski yolda başka state tabloları olsa bile birleşik yola
kurulur (kural 2 — tablo bir yerde varsa komşusuna katıl — "yeni düğüm" senaryosunu zaten korur).
**Neden:** kural 3 kendini besleyen bir kilitti: ingest_ledger eski yolda doğunca (v0.10.767) sonraki
her state tablosu shard'a bölünmüş doğdu (prod, 10 tablo; v0.10.965 sihirbazı onarır).

## 2026-09-27 — CoSRE trace incelemesi: «Kök neden» güvenle, koşullu «Stacktrace detayı» (v0.10.972)

**Karar (operatör: "CoSRE de neden kök neden çıkmıyor artık" → "Kök neden olsun yine de"; "Stacktrace
detayı bölümü de geri gelsin"):** "CoSRE'ye sor" ilk cevabında (v0.10.948) «Olası neden» → «Kök
neden»; ilk satırı güven: "Güven: kesin" (hata veren span/log'dan nedene kanıt zinciri kesintisiz)
ya da "Güven: olası — <eksik halka>". Kanıt kuralları aynen (kimlik, uydurmama, ilişki ≠ neden;
yalnız zamansal ilişki kesin olamaz). Kart Karar şeridini YALNIZ "kesin"de çizer; "olası" hipotez
olarak kalır (şerit yok). «Stacktrace detayı» Kanıt'la Kök neden arasında TEK koşullu bölüm: yalnız
L satırında stacktrace varken; Oracle satırı / çıplak exception.type sayılmaz, yoksa bölüm hiç
yazılmaz. Sunucu L satırına üst kareleri "stacktrace:" alanıyla koyar (önce exception.stacktrace
prompt'a hiç girmiyordu): kaynak değer kesik değilse ilk 3 kare + her Caused by'ın ilk karesi,
≤600 rune, FenceSafe, en çok 2 farklı stack; L bütçesi basılan stack kadar büyür.
get_logs_for_trace öznitelik değerini 200 runede kestiği için OTel/ECS exception.stacktrace'te
çoğunlukla başlık + ~1 kare gelir; kesik olduğu satırda "(kaynak kesik: …)" diye söylenir ve istem
kesik stack'i tek başına "Güven: kesin" dayanağı saymaz. Problem özetinin düz "Olası neden:"i ve RCA
etiketleri AYRI yüzey, değişmedi. Takip (onay ister): mcptools logAttrs'ta stack anahtarlarına
(exception.stacktrace, error.stack_trace, …) sınırlı büyük tavan (~1500 rune, FenceSafe) — MCP
çıktısını dış istemciler ve sohbet için de değiştirir.

## 2026-10-01 — Traces hacim şeridi: iş kimlikleri giriş span'i anahtarı değil (v0.10.1006)

**Operatör bildirimi + onayı ("olur düzelt"):** `function_code = …` çipinde tablo dolu, hacim şeridi "No
traces in view to bucket" (0 istek). **Kök neden:** şerit, çip giriş span'inde yaşayan bir anahtardaysa
kind IN (server, consumer) kısıtı ekler (v0.10.268 / 323); `channel_code`, `function_code`, `function_id`
`ENTRY_KEYS` listesindeydi ama prod'da bu iş kimliklerini giriş span'i taşımıyor — yalnız log-yayın MQ
span'leri (operatörün trace'inde 12 span'in 4'ü). Kısıt AND'lenince eşleşme sıfır. v0.10.323
(db.statement) ve v0.10.730 (name) ile aynı sınıfın üçüncü örneği. **Karar:** üçü de listeden çıkarıldı;
şerit bu çiplerde eşleşen SPAN'leri sayar, birim "spans", ipucu neyi saydığını yazar. `function_code`
için kanıt ekran görüntüsü; `channel_code` / `function_id` aynı yayın mesajının alanları olduğu için
birlikte çıkarıldı (yanılma bedeli: sayı trace sayısından büyük ve etiketli; tersi boş grafik). **Kabul
edilen bedel:** bir trace birkaç yayın span'i taşıyorsa şerit sayısı listedeki trace sayısından büyüktür.
1005'in listeyle ilişkisi: liste çipte trace düzeyinde eşleşir ve satırı tüm span'lerden kurar; şerit
span sayar — ikisi de etiketli.

## 2026-10-01 — Traces: çip trace'i seçer, satırı şekillendirmez (v0.10.1005)

**Operatör bildirimi (prod):** `function_code = …` çipiyle gelen listede Name kolonu kök span'in adı yerine
bir MQ "publish" span'inin adını gösteriyor; süre ve span sayısı da eksik (listede 21 ms / 4 span, trace
detayında 25 ms / 12 span). **Kök neden:** arama yokken çipler WHERE'de SPAN düzeyinde (terfi kolonu / kvh
indeksi budasın diye, v0.10.341) ve satır aynı WHERE'in bıraktığı span'lerden kuruluyor; çipi kök span
taşımıyorsa (prod'da fonksiyon kodunu yalnız log-yayın span'leri taşıyor) `anyIf(kök)` boş kalıp
`any(name)` eşleşen bir span'in adına düşüyor (v0.5.351 yedeği), süre/sayı da yalnız eşleşen span'lerden.
**Karar:** çip TRACE'İ SEÇER, satırı şekillendirmez — v0.10.258'in Errors için koyduğu kural çiplere
genellendi. Sayfa belli olduktan sonra satır alanları (ad, servis, rota, başlangıç, süre, span/hata sayısı)
çipsiz WHERE ile yeniden kurulur (`repairSpanScopedRows`): id listesi PREWHERE'de, pencere sayfanın kendi
zaman aralığı (alt sınır 5 dk geri — kök eşleşen span'den önce başlar); maliyet pencereden değil id
sayısından (≤ sayfa) gelir, extras'tan önce koşar. Servis / ortam / küme yüklemleri AYNEN kalır (satır
"çipler olmasaydı ne olacaksa" odur). Sıra ve sayfa üyeliği 1. geçişin kararıdır, değişmez. Yumuşak düşer:
onarım okuması hata verirse liste eski satırlarıyla döner. CSV dışa aktarımı (>500 id) onarılmaz.
**Reddedilen:** çipleri her zaman HAVING'e taşımak (trace düzeyi) — indeks budaması kaybolur, v0.10.341'in
kaçındığı tam pencere taraması. **Açık (operatör kararı):** aynı ekranda hacim şeridi boş — şerit
`function_code`'u giriş span'inde yaşıyor sayıyor (`ENTRY_KEYS`, volumeSeries.ts); v0.10.323 / 730 ile aynı
sınıf, düzeltme o anahtarı listeden çıkarmak (şerit eşleşen span'leri sayar, etiket "spans").

## 2026-10-01 — Oracle Problem'i: ilgili endpoint'ler kanıtta — özne servis KALIR (v0.10.1004)

**Karar (kuyruk "Problem'i endpoint'e bağlama", operatör "devam"):** Problem'in öznesi DEĞİŞTİRİLMEDİ —
`problems.service` gerçek servis olarak kalır (servis sayfası, ekip yönlendirmesi, bildirim süzgeçleri,
küme anahtarı hepsi ona bağlı; özneyi endpoint yapmak bunların tamamını sessizce değiştirirdi). Endpoint
ilişkisi EK KANIT olarak eklendi: kanıt panelinde "İlgili endpoint'ler" (≤5) — Problem'in Oracle
satırlarındaki trace'lerin geçtiği endpoint'ler, trace ve hatalı trace sayısıyla, /endpoint sayfasına
OLAY penceresiyle (−30 dk / +10 dk) bağlı. **Kaynak:** yeni sorgu yok — kanıtın zaten yaptığı span
okuması (`trace_id IN ≤50`, zaman sınırlı) iki kolon daha seçer (`kind`, `http_route`) ve aynı satırlardan
katlanır (`foldTraceEndpoints`). **Endpoint kimliği** /endpoints sayfasının kuralı: `http_route` doluysa
(giden çağrı hariç) yol = route; boşsa server/consumer span adı (RPC, link `entry=rpc`). **Trace başına
tek endpoint:** özne servis trace'te giriş span'i taşıyorsa onunki; taşımıyorsa trace'in EN DERİN hata
span'inin servisi (v0.10.892 kuralı — ilk hata span'i giriş noktasıdır, hep aynı gateway çıkardı); o da
yoksa en erken giriş span'i. Sentetik dış özne (`ext:…`) servis sayılmaz. Trace'i Coremetry'de olmayan
Problem'de blok hiç çizilmez. **Reddedilen:** Problem'i endpoint'e taşımak (yukarıdaki gerekçe) ve
fonksiyon kodundan endpoint çıkarmak (kodu taşıyan her serviste ayrı endpoint var; kanıt trace'i kesin).

## 2026-10-01 — Oracle: operasyon adı trace ve endpoint sayfasında (v0.10.1003)

**Karar (kuyruk 1, operatör "devam et"):** 1002'nin TERS yönü — ekranda görülen fonksiyon kodu Oracle
operasyon ADINA çevrilir. Tek kaynak bir SÖZLÜK: `GET /api/oracle/function-codes` (kod → en çok satırlı
≤3 operasyon adı + koda bağlı toplam ad sayısı; `oracle_error_log` son 7 gün, ≤5000 çift, sunucu 5 dk
önbellek, rol kapısı yok). **Neden sözlüğün tamamı tek cevapta:** trace başına istek atmamak için — FE onu
tek paylaşılan anahtarla (`useOracleFunctionCodes`) 5 dk tazelikle bir kez çeker ve YALNIZ ekranda bir
fonksiyon kodu varken. İki yüzey: **(1) Trace özet şeridi** — span'lerdeki `FUNCTION_CODE` / `function_code`
değerinden "Operasyon: <ad>" çipi; tıklayınca aynı fonksiyon kodunun diğer trace'leri (süzgeç anahtarı
span'de GÖRÜLEN yazımla). Trace kod taşımıyorsa ya da kod sözlükte yoksa hiçbir şey çizilmez — ad
uydurulmaz. **(2) Endpoint › Break down by** — beyaz listeye `function_code` boyutu (iki yazımı okuyan
DİZİ ifadesi: terfi kolonu her kurulumda yok ve harita statik; tarama zaten servis + rota + zamanla
sınırlı); değer hücresi "CAF0001 · <operasyon adı>". Kod birden çok operasyonda görülüyorsa en çok
satırlı ad + "(+N)" ve ipucunda diğerleri: 1:n ilişki gizlenmez. Sınır 1002 ile aynı: yalnız son 7 günde
Oracle'da hata satırı olan operasyonların adı bilinir.

## 2026-10-01 — Oracle: operasyon adıyla trace bulma — komut paleti (v0.10.1002)

**Karar (operatör: "operasyon ismiyle trace bulabilir miyim" → "yap"):** operasyon adı
(DIGITAL_TRANSFER_EFT_CONFIRM_SERVICE gibi) trace'lerde yok; arama KOMUT PALETİNE (⌘K / üst arama kutusu)
üçüncü sunucu taraflı kaynak olarak eklendi (`GET /api/oracle/operations?q=`, ≥3 karakter, 200 ms
debounce, 60 sn önbellek, rol kapısı yok — operasyon adı zaten Problem başlığında ve Trace › Logs'ta her
role görünür). Her isabet en çok iki sonuç: **(1) operasyon → trace'ler** — operasyonun son 7 gündeki
hata satırlarındaki fonksiyon kodları, Traces'te `FUNCTION_CODE = / IN` süzgeci olarak (başarılı + hatalı
TÜM trace'ler; fonksiyon kodu yoksa öğrenilmiş servisin hatalı trace'leri, ikisi de yoksa sonuç üretilmez);
**(2) son hata trace'i** — hata satırındaki en yeni trace kimliği, Logs sekmesinde (kesin eşleşme).
**Neden Traces sayfasına yeni süzgeç türü değil:** çeviri (operasyon → fonksiyon kodu) palette yapılıp
sıradan bir attribute süzgecine indiği için liste, sayım, grafik ve ısı haritası uçlarının hiçbiri
değişmedi; Traces'e özel bir `oraOp` parametresi her uçta ayrı çeviri isterdi. **Süzgeç anahtarının
yazımı sunucudan gelir** (`spanAttrKey` = terfi kolonu probe'unun doğruladığı yazım,
`chstore.PromotedAttrSpelling`): kullanıcı süzgeci harf duyarlı, sabit bir yazım v0.9.626 sınıfı sessiz
boş listeydi. Sınır: yalnız son 7 günde hata satırı olan operasyonlar bulunur (kaynak `oracle_error_log`);
hiç hata vermemiş operasyon adı Coremetry'de hiçbir yerde yok.

## 2026-10-01 — Oracle: fonksiyon kodu doğru kolondan okunur (v0.10.1001; 1000'in düzeltmesi)

**Bulgu (operatörün prod ekran görüntüsü):** güncel sorgu çıktısında hem `ERRORCODE` hem `FUNCTIONCODE`
var; `code` alanı (error.code) `ERRORCODE`'a bağlı (takma ad önceliği), `FUNCTIONCODE` eşlenmeyen kolon
olarak satırın attribute'u. v0.10.1000 fonksiyon kodunu `code` alanından okuyordu (v0.10.902 eşlemesi
varsayımı) → span'lerde hata kodu ("COR-…") aranıyor, hiçbir şey bulunmuyordu; kutu işaretlense de etkisi
sıfırdı. **Karar:** kod `oracle.FunctionCodeOf` ile okunur — `code` alanı bir fonksiyon kodu kolonuna
eşliyse (ad alt çizgisiz/büyük harf "FUNCTIONCODE" ile biter) oradan, değilse satırın aynı kurala uyan
attribute'undan. Yeni eşleme alanı / ayar YOK (sorgu çıktısında kolonun bulunması yeter). Sonuçları:
seri anahtarı (op, hata kodu, kanal) fonksiyon kodu taşımadığında seri, OPERASYONUNUN o tikteki fonksiyon
kodlarıyla çözülür; kapsam raporunun (operasyon, kod) dökümü aynı kuralı SQL'de uygular
(`oracleOpCodesSQL`, yerel ClickHouse'ta doğrulandı); satırlarda fonksiyon kodu hiç yoksa rapor bunu
söyler ("FUNCTIONCODE kolonu olmalı"). Hata kodu hiçbir durumda fonksiyon kodu sayılmaz.

## 2026-10-01 — Oracle: fonksiyon kodundan servis — öğrenen eşleme (v0.10.1000)

**Karar (operatör):** "Oracle'dan gelen DIGITAL_PAYMENT_EFT gibi operasyon adı trace'lerde yok, eşleştirebilir
miyiz" → evet, FONKSİYON KODU üzerinden; teyit: Oracle satırının kod alanı (özel SQL'de `FUNCTIONCODE` →
`error.code`) span'lerdeki `FUNCTION_CODE` ile aynı değer; "zaman içinde öğrendikçe güncellersin". Köprü:
satır (operasyon, fonksiyon kodu) → o kodu taşıyan span'lerin servisi; trace kimliğine gerek yok.
**Kaynak başına açılır** (`functionCodeMatch`, Ayarlar › Oracle › Problem üretimi; varsayılan KAPALI —
mevcut kaynakların öznesi değişmez). **Çözücü sırası:** trace → pod adı → öğrenilmiş → *fonksiyon kodu* →
bilinmiyor; yeni basamak yalnız öncekiler servis bulamadığında çalışır, çözülen hiçbir özneyi değiştirmez.
**Servis seçimi** (`oracle.PickFunctionService`): kodu taşıyan HATA span'lerinin (≥3 ise; yoksa tüm
span'lerin) ≥%70'i tek servisteyse o servis. Kod çağrı zinciri boyunca birden çok serviste taşınıyorsa
çoğunluk çıkmaz → servis uydurulmaz, not adayı söyler ("F200 3 serviste, çoğunluk yok"). **Öğrenme:** her
taze okuma (kod başına 10 dk önbellek), trace/pod oyu olmayan operasyonda haritaya (operasyon, kod) çifti
başına BİR oy yazar — aynı okuma üç tikte üç teyit sayılmaz; ≥3 teyit + ≥%70 ile "öğrenilmiş"e döner.
**Harita güncellemesi (yeni kural, tüm oy türleri):** mevcut servis hiç oy almazken aynı başka servis
ardışık ≥3 oy biriktirirse girdi ona döner (`LearnedEntry.alt/altHits`); mevcut servis oy alırsa sayaç
sıfırlanır. Eski kural tek tikte ≥3 oy istiyordu — tik başına 1-2 oy veren pod / fonksiyon kodu
kanıtında hiç sağlanmıyor, taşınan operasyon 30 gün "onaysız" kalıyordu. **Okuma yolu**
(`chstore.FunctionCodeServices`): ① geniş rollup (`rollup_spans_wide_1m/5m`, MV-first) varsa o; ② yoksa
terfi kolonu `attr_function_code` KAYITLIYSA ham spans (set(0) indeks, pencere ≤1 sa); ikisi de yoksa
basamak çalışmaz ve bunu söyler. Reddedilen: attribute dizisini açan servis süzgeçsiz ham tarama (1B
span/gün'de poll başına koşulacak sorgu değil). Geniş rollup prod'da önerilmediği için (2026-08-09 A/B)
beklenen prod yolu ②. **Ölçüm:** özne kapsamı raporu basamağı ayar KAPALIYKEN de ölçer ("açılırsa N satır
daha bağlanır, oran %X → %Y") — operatör açmadan etkisini görür; açıkken pay başlıkta. **Bu sürümde YOK
(ayrı karar):** operasyon adını trace / endpoint sayfasında göstermek, trace'leri operasyon adıyla aramak,
Problem'i servis yerine endpoint'e bağlamak.

## 2026-10-01 — Oracle: özne kapsamı raporu — önce ölç, sonra kapat (v0.10.999)

**Karar (operatör, Oracle odak "2" — "nasıl yapacaksın"):** trace'i Coremetry'de olmayan satırların
servissiz kalması (2026-09-23 canlı testinde ~%58) iki adımda kapatılır; bu sürüm yalnız ADIM 1.
**Adım 1 — ölç:** Ayarlar › Oracle'da kayıtlı kaynak kartında "Özne kapsamı"
(`GET /api/settings/oracle/{id}/subject-coverage`, admin, salt okuma, 60 sn önbellek): son 24 saatte
satırların yüzde kaçı bir servise bağlanıyor (öğrenilmiş eşleme / pod adından) ve bağlanmayanlar
NEDEN bağlanmıyor — satırlarda trace kimliği yok · trace Coremetry'de yok · çok servisli operasyon ·
eşleme henüz onaysız · öğrenilmiş servis canlı değil · operasyon kodu boş — artı en çok satırlı 20
çözülmeyen operasyon (en sık instance / host ile; instance pod adı biçiminde mi). Girdi
`oracle_error_log` (operasyon başına döküm, canlı Oracle'a gidilmez) + öğrenilmiş harita + canlı servis
adları; sınıflama saf (`oracle.BuildCoverage`) ve çözücünün kalıcı basamaklarını aynalar. **Neden önce
ölçüm:** özne yalnız Problem AÇILIRKEN çözülüyor ve cevap Problem notuna yazılıyordu; kaynağın bütününe
dair sayı hiçbir yerde yoktu, yani kapatma yöntemi tahminle seçilecekti. **Adım 2 — kapat (operatör
kararı, baskın nedene göre):** "instance pod adı değil" (host adı) baskınsa host adı → servis basamağı
(servisin span'lerindeki `host.name`); "trace Coremetry'de yok" + pod adı var ama servis canlı değilse
adlandırma kuralı (pod öneki ↔ servis adı) genişletilir; kalan az sayıda operasyon için elle op → servis
eşlemesi (pin); "çok servisli" operasyon tek servise bağlanmaz, servissiz kalması doğrudur. Çözücü,
eşikler (≥3 teyit, ≥%70) ve harita bu sürümde DEĞİŞMEDİ.

## 2026-10-01 — Oracle: canlıya geçiş önizlemesi (v0.10.998)

**Karar (operatör, Oracle odak "3": kaynak kipi gölge → canlı):** kipi operatör değiştirir; ürün
kararı VERİYLE verdirir. Ayarlar › Oracle'da kayıtlı her kaynak için "Canlıya geçiş önizlemesi"
(`GET /api/settings/oracle/{id}/live-preview`, admin, 60 sn önbellek, salt okuma): gölgede son 24 saat
/ 7 günde açılan Problem sayısı (kritik, küme, şu an açık, en çok açan 5 özne) ve bu Problem'lerin
bildirim türünü (**anomali** — `ProblemNotifyKind`) alan etkin kanallar + ekip maili. **Neden bu iki
soru:** canlı kip bildirimi yalnız AÇILIŞTA gönderir (`anomaly/external.go`), yani gölgedeki açılış
sayısı gidecek bildirim sayısının doğrudan ölçüsüdür; ve operatör anomali maillerini yanlış pozitif
yüzünden kapatmıştı (v0.10.814) — türü süzen kanal Oracle bildirimini de almaz, "canlıya aldım,
hiçbir şey gelmedi"nin en olası sebebi ekranda açıkça yazılır. Sayım `problems` FINAL'den, kaynağın
kural önekleriyle (`anomaly:ext:<kaynak>/`, `anomaly-cluster:ext:<kaynak>/`); seri kimliği her
açılışta yeni olduğu için açılışlar sayılabilir, küme kimliği anahtara sabit olduğu için küme sayısı
alt sınırdır ve öyle sunulur. Kip değişiminde zaten açık Problem'ler için bildirim gitmez (yalnız
yeni açılışlar) — önizleme bunu da söyler. **Değişmeyen:** tarayıcı, eşikler, tavan, kipin kendisi.

## 2026-10-01 — Argo CD: hub, instance'larıyla birlikte kaldırılabilir (v0.10.997)

**Karar (operatör: "Hub kaldıramıyorum instance varsa" — prod'da hub başına 190+ instance):**
Ayarlar › Argo CD'de instance'ları bağlı hub'ın "Kaldır"ı artık çıkmaz sokak değil. İlk tık hiçbir
şeyi kaldırmaz: ileti + "Hub'ı N instance ile birlikte kaldır" / "Vazgeç". Onay hub'ı ve ona bağlı
tüm instance'ları TEK taslak değişikliğiyle çıkarır; Kaydet'e kadar yazılmaz, "Değişiklikleri geri
al" geri getirir. Sunucu kuralı (BE4) DEĞİŞMEDİ: hub gövdeden çıkarken ona bağlı instance gövdede
kalırsa 400 — ikisi birlikte çıkınca kural zaten sağlanıyor, yani düzeltme yalnız arayüzde. Pin'i
olan instance kaldırılamaz kuralı da aynı: o hub'da onay sunulmaz, kaç pin olduğu söylenir (pin
editörü API'de). Kaldırılan instance'ların ClickHouse satırları (`instance_id`) TTL'e kadar durur.

## 2026-10-01 — MCP istemcisi çift dönemli: önce `server/discover`, olmazsa `initialize` (v0.10.995; denetim M2)

**Karar:** dış MCP sunucularına bağlanan istemci (`internal/mcpclient`) dönemi `Initialize`'da bir kez
belirler: `server/discover` yoklaması DiscoverResult ve `2026-07-28` döndürürse MODERN (el sıkışma
yok; her istek `_meta` + HTTP'de üç MCP başlığı, `Mcp-Name` gerekirse base64 nöbetçili; sonuçta
`resultType`), aksi hâlde LEGACY (`initialize` + `initialized`, v0.10.86 davranışı). **Düşüş kuralı
belirtimden:** tek bir hata koduna bağlanmaz (eski sunucular -32601, -32602 döner ya da hiç
yanıtlamaz → hepsi legacy); tanınan modern hata -32022 ise sunucu moderndir — ilan ettiği sürümlerde
konuşabildiğimiz varsa ona geçilir, yoksa "ortak sürüm yok" hatası (initialize'a körlemesine düşülmez);
-32020 / -32021 ve 401 / 403 düşüş üretmez. **Bedel:** legacy sunucuda bağlantı başına bir fazladan
istek; yanıt vermeyen legacy stdio sunucusunda ilk bağlantı yoklama tavanı (15 sn) kadar gecikir —
tavan bilinçli olarak kısa tutulmadı, çünkü yavaş açılan (`npx …`) modern bir sunucuyu legacy sanmak
initialize'da kalıcı hata üretirdi. **Desteklenmeyen:** MRTR (`input_required`) — sessizce boş sonuç
sayılmaz, açık hata. Legacy HTTP'de initialize'ın döndürdüğü sürüm sonraki isteklerin
`MCP-Protocol-Version` başlığıdır (2025-06-18+). Uçtan uca test kendi sunucumuza karşı: iki taraf
birbirinin başlık / `_meta` sözleşmesini doğrular.

## 2026-10-01 — MCP sunucusu çift dönemli: 2026-07-28 el sıkışmasız sözleşme (v0.10.994; denetim M1)

**Karar:** `POST /api/mcp` artık iki dönemi birden konuşur. İstek `params._meta` içinde
`io.modelcontextprotocol/protocolVersion` taşıyorsa (ya da yöntem `server/discover` ise) 2026-07-28'in
durumsuz sözleşmesiyle (`internal/mcp/modern.go`), aksi hâlde eski `initialize` yoluyla — o yol bayt
bayt aynı (testle pinli; Claude Code'un bugünkü istemcisi fark görmez). Modern yolda: zorunlu `_meta`
alanları (eksikse -32602 / 400), başlık ↔ gövde doğrulaması (`MCP-Protocol-Version`, `Mcp-Method`,
`Mcp-Name`; -32020 / 400), yalnız `2026-07-28` (başkası -32022 / 400 + `data.supported`), bilinmeyen
yöntem 404 (`initialize` ve `ping` dahil), her sonuçta `resultType: "complete"` + `_meta` içinde
`serverInfo`, liste / okuma / discover sonuçlarında `ttlMs` + `cacheScope: "private"` (uç kimlik
doğrulamalı), varlık bulunamadı -32602. Kapı (rol + hız) ve gözlem aynı handler'larda: modern yol
yalnız zarfı değiştirir. **Kaynak:** belirtimin kendisi okundu; denetim raporundaki M1 reçetesi alan
adlarında yanlıştı (`protocolVersions`, `_meta.protocolVersion`) ve rapor düzeltildi ("denetim
reçeteleri uygulanır" dersi — reçete değil birincil kaynak uygulanır). **Kapsam dışı:**
`subscriptions/listen` (list_changed yayınlamıyoruz), MRTR (sampling / elicitation yok),
`x-mcp-header`, logLevel; batch modern dönemde tanımsız → batch içindeki modern istek -32600.
İstemci tarafı (M2, `internal/mcpclient`) ayrı sürüm. CORS izinli başlıklarına üç MCP başlığı eklendi.

## 2026-10-01 — MCP `bubble_up` aracı yalnız dış istemcilere (v0.10.993; denetim V1 dilim 3)

**Karar:** BubbleUp MCP aracı olarak eklendi (`internal/mcptools/bubble_up.go`; kıyas
`chstore.ServiceBubbleUp`, yeni SQL yok) ama **uygulama içi sohbet kataloğuna GİRMEZ**. Yeni
ayrım: `externalOnlyTools` + `ChatToolList` — `Register` aracı dış MCP'ye kaydeder, sohbet ve
sunucu-yürütmeli inceleme `ChatToolList` okur (kaynak pinli). **Neden:** denetimin kararı "küçük
LLM için prefetch adımı, tool değil" (v0.10.992 o adımı getirdi); ayrıca sohbetin her tur yuttuğu
kompakt katalog 9.098 / 9.100 B'ydi — yer yoktu ve bütçeyi büyütmek her sohbet turunu
pahalılaştırırdı. Kompakt bütçe artık yalnız sohbetin gördüğü araçları sayar; alan kuralları
(dolu, taban/tavan, Türkçe) dış-yalnız araç için de geçerli. Maliyet dürüstçe ilan edilir: ham
spans taraması, pencere [300, 3600] sn (varsayılan 600), çağrı 15 sn tavanlı, yalnız ≥5 puan
ayrışan değerler; boş liste `note` ile gelir ("yoğunlaşmıyor" ≠ "bakılmadı"). Katalog 60 → 61.

## 2026-10-01 — CoSRE kök-neden demetine BubbleUp adımı (v0.10.992; dış skill denetimi V1 dilim 1)

**Karar:** "neden X bozuldu" demeti (guidedRootCauseBundle) RED'den sonra, deploy'dan önce bir
`bubble_up` adımı taşır: sorun hangi rota / pod / sürümde yoğunlaşıyor. Kıyas tek yerde
(`chstore.ServiceBubbleUp`; /rootcause'un iki fan-out'u ve verdict kataloğu da onu çağırır): hata
ailesi → hatalı span'ler aynı penceredeki tüm span'lere karşı, diğerleri → pencere önceki eş-boy
pencereye karşı. Metin en çok 3 boyut, yalnız ≥5 puan ayrışan; "ayrışma YOK", "kıyas kurulamadı"
ve "OKUNAMADI (sınıf)" ayrı cümleler (yokluk ≠ okunamadı). **Pencere bilinçli olarak 10 dk**
(`rca.ExtrasWindow`, katalogla aynı): açık problem varsa açılışını izleyen 10 dk, yoksa son 10 dk
— /rootcause paneli 1 saate kadar tarar ve ~40 sn sürebiliyor (v0.9.1082 ölçümü), sohbet cevabı
onu bekleyemez; üstüne 8 sn tavan (`rca.BubbleUpTimeout`), süre dolarsa adım "okunamadı" der.
Ortam süzgeci uygulanmaz (RED notu gibi, tüm ortamlar). **Uygulanmadı — karar ister (V1 dilim
2):** BubbleUp'ı `DeepEvidence`'a, yani sentezleyiciye taşımak; açık problem × tik başına ham
spans taraması ekler ("mevcutların hızlı ve doğru çalışması önce" direktifi). Dilim 3 (MCP
`bubble_up` aracı) ayrı sürüm. Yan bulgu v0.10.991: katalog satırı yüzdeleri oran (0–1) olarak
basıyordu ("%1 … %0").

## 2026-10-01 — Argo CD keşfi: 50 iş tavanı kalktı, "Tümünü ekle" (v0.10.990)

**Karar (operatör: "Argocd entegrasyonu da autodiscover etse daha iyi olacak, şu anda tek tek ekle
diyorum ve sadece ilk 50'yi bulduğu için eksikleri oluyor"):** ekip × ortam başına ayrı Argo CD
instance'ı (ayrı `job`) olan hub'da keşif 50 işte kesiliyor, 51. instance hiç aday olmuyordu.
Tavanlar: iş 50 → 500, iş başına namespace/exported_namespace değeri 100 → 500, çağrı 150 → 2000;
shard (`pod`) tavanı 100 kaldı; kaydedilebilir instance 100 → 500 (`maxInstances`). Süre bütçesi
aynı (60 s; istemci 75 s): sığsın diye iş başına çağrılar 4 eşzamanlı koşar
(`argocdDiscoverParallel`). **Tasarım değişmedi:** aday bulma yine `job` süzgeçli label-values
(§5.4 "asla süzgeçsiz"; hub genelinde tek toplu sorgu bilinçli olarak SEÇİLMEDİ), sayım yine iş
başına tek anlık count; bütçe ayırma ile sayım tek kilit altında (`argocdProbeRun.take`), yani
eşzamanlı kip tavanı aşamaz ve `calls` giden istek sayısıdır. Aday sırası iş listesinin sırası.
Arayüz: hub bloğunda "Tümünü ekle (N)" namespace'i belli tüm yeni adayları tek tıkla TASLAĞA koyar;
namespace'i bilinmeyen aday (durum B, çok namespace) elle kalır. "Öner, asla otomatik yazma"
(annex §7.2) duruyor: kayıt yine Kaydet'le yazılır. **Açık (operatör kararı):** arka planda
kendiliğinden kayıt (keşfi işçinin koşup blob'a yazması) bu sürümde YOK — instance kimliği kalıcı
CH `instance_id`'dir ve işçi açıkken her yeni instance hub'a tur başına sorgu ekler. Not:
argocd-metrics işçisi instance başına shard koşar; yüzlerce instance'ta tur maliyeti ölçülmedi
(bayrak varsayılan kapalı).

## 2026-10-01 — Trace: "Hızlı açıkla" düğmesi kaldırıldı (v0.10.989; v0.10.987 kararının tersi)

**Karar (operatör: "tracelere quick explain butonu koymuşsun onu kaldıralım"):** trace sayfasında
"CoSRE'ye sor"un yanındaki ikincil "Hızlı açıkla" / "Quick explain" düğmesi ve ona ait her şey
kaldırıldı: `?aiquick=1` paramı (AI_QUICK_PARAM, useAiSubject/AIExplainButton `quick`), gövdedeki
`quick:true` (explainInit, explainOptions.Quick), sunucunun `traceExplainPath` / `explainTraceQuick`
yolu, çekmecedeki "canlı okuma yok" notu, i18n anahtarları ve iki test dosyası. Uyumluluk katmanı
YOK (CLAUDE.md: özellik kaldırırken shim eklenmez): eski bir `?aiquick=1` linki artık yok sayılır ve
varsayılan incelemeyi açar; gövdede `quick` gönderen istemci de incelemeye düşer. Trace'te yine iki
yol var: varsayılan inceleme ve "Kodu da incele" (klasik istem + kod bağlamı). v0.10.986'nın klasik
üç başlıklı cevap biçimi değişmedi.

## 2026-09-29 — Trace: "Hızlı açıkla" ikinci düğme — tek atışlık klasik açıklama (v0.10.987)

**Karar (operatör: "3 seçenek yapalım"):** trace sayfasında "CoSRE'ye sor"un yanına ikincil
"Hızlı açıkla" / "Quick explain" düğmesi. Aynı özne (`?ai=trace`) + `?aiquick=1`; çekmece bunu
gövdede `quick:true` olarak gönderir, sunucu KLASİK tek atışlık yolu koşar (buildTraceExplainInput:
trace + loglar + Oracle satırları, SystemPromptTrace) — inceleme okumaları (dönem kıyası, pod,
deploy) ve adım akışı yok, tek LLM turu. Yol seçimi saf `traceExplainPath`: includeCode > quick >
inceleme (kod dalı zaten klasik istem + kod bağlamı). Önbellek anahtarı kodsuz klasik anahtar
(explainTraceClassicPrepared ile aynı satır). `aiquick` aicode/aisrc gibi yalnız o açılışta ve
paylaşılan linkte yaşar (useAiSubject özne değişiminde siler, quick ile yazar). Aynı trace'in iki
düğmesi aynı özneyi taşıdığından "açık" sayımı quick bayrağıyla ayrışır: biri açıkken ötekine tık
kapatmaz, kipe geçer. Panelde tek satır not: canlı okuma yok. **Neden:** v0.10.986 ilk cevabı klasik
biçime aldı; operatör yine de hızlı, okumasız eski cevabı ayrı bir düğme olarak istedi (seçenek 3).

## 2026-09-29 — CoSRE trace incelemesi: klasik üç başlık, kanıt kimliksiz, güven satırsız (v0.10.986)

**Karar (operatör: "trace'i açıkladığımda K1 T1 kesin gibi çıkarımlar yapıyor … kodu incele
dediğimde daha iyi sonuç veriyor, o hali olsa daha iyi olacak"):** "CoSRE'ye sor" ilk cevabı
"Kodu da incele" geçişinin biçimine geçer — `systemTraceBody` ile aynı üç başlık: «İşlem Akışı ve
Veri Özeti» / «Stacktrace Detayı» (yalnız kanıtta stacktrace varsa) / «Kök Neden ve Sonraki Adım»,
artı «Eksik veri» yalnız durumu ok olmayan kaynak varken. Kanıt kimlikleri ([T1], [L1], [K1] …)
sunucu istemde yine verir ama cevaba YAZILMAZ (modele yöneliktir); v0.10.972'nin "Güven: kesin /
olası" satırı kalktı. Arayüz kod değiştirmeden klasik davranışa döner: Kök Neden'in ilk cümlesi
Karar şeridi (`explainAnatomy` VERDICT_HDR; beşli önbellek metni için eski kural durur).
**Değişmeyen:** veri toplama (get_trace, loglar, dönem kıyası, pod, deploy, Oracle), kaynak durumu
künyesi ve sayı denetimi, dürüstlük kuralları (uydurma yok; ilişki ≠ neden; "log bulunamadı" ≠
"hata yok"; kanıt yetersizse söylenir). Takip eki de aynı başlıklara geçti. Önbellek revizyonu
inv-v0.10.986. **Neden:** v0.10.948/972'nin kanıt izi (kimlik + güven) cevabı okunmaz kılıyordu;
operatör kodlu geçişin düz anlatımını tercih etti. Uydurmaya karşı koruma istemdeki değer-aynen
ve "kanıt yetersiz" kurallarında sürer; kimlikler modele sunulmaya devam eder.

## 2026-09-27 — Argo CD: şimdilik yalnız metrik (API bağlantısı askıda)

**Karar (operatör: "argocd şimdilik metrikle"):** Argo CD entegrasyonu şimdilik yalnız hub'ların
Thanos'undaki `argocd_*` metrikleriyle çalışır; Argo CD API'sine bağlanılmaz. Ayarlar › Argo CD
sekmesindeki `apiUrl`/`tokenRef` alanları isteğe bağlı olarak KALIR (boş = bağlantı yok);
kaldırılmaz, çünkü karar kalıcı değil. Plan etkisi: Faz 3.3 (argocd-api işçisi: senkron
geçmişi, kesin uygulama↔iş yükü eşlemesi) ve ona dayanan Faz 4 (Azure DevOps zinciri) askıda;
Faz 3.1/3.2/3.4/3.5 metrikle sürer, sınıflandırma "tahmin" etiketiyle (karar 15). API açılınca
sekmeye alan girmek ve 3.3'ü başlatmak yeter; ayar şeması hazır.

## 2026-09-27 — Tablo standardı tamamlandı (dilim 5–7, v0.10.967 / 973 / 977)

**Karar (operatör: "Önerilerine evet", "Sırayla devam"):** 2026-09-26'da açılan tablo standardı
(dört tür, sessiz satır, renk yalnız sapmada, durumlar tablonun içinde) yedi dilimde bitti.
Dilim 5 primitife `detail` (durum satırında link/CTA) ve `colSpan` (statik tablo durumları)
ekledi; 6–7 kalan sayaçları süpürdü. `tableUnityRatchet` tavanları ölçülen değerde ve SON:
rawTable 47 (9 T1-muaf sohbet/lejant/iskelet + 38 gerekçeli statik), tdStyle 60 (lejant
tabloları, dinamik opaklık/maxWidth gerekçeli), containIntrinsicSize 3, trTitle 2,
inlineMonoStack 4 (uPlot canvas font dizgisi — var() çözülemez; çözümlü font token'ı
gelirse düşer), dtNoState 16 (kendini gizleyen "sağlıklı = boş" bölümleri, yorumla
gerekçeli); diğer altı sayaç 0. Kalan her sayı ratchet'in kendi yorumunda dosya+gerekçeyle
listeli; düşüş yeni bir olanak (KeyValue sıkı kip, canvas font token'ı) gerektirir, artış
suçlu dosyada düzeltilir, tavan yükseltilmez.

## 2026-09-27 — Dört küçük sertleştirme (v0.10.978)

**Karar (operatör: "Önerin" → dört öneri tek sürümde):**
1. **Boot Keeper muhafızı:** gözlem eksikse (roster'daki bir replika probe'a cevap vermediyse)
   hiç gözlenmeyen bir state tablosu kurulmadan önce Keeper'da `<önek>/<shard-dizini>/<t>/replicas`
   (gözlenen eski dizinler, yoksa `{shard}` makroları) ve `<önek>/state/<t>/replicas` okunur:
   yalnız eski yolda replika varsa ona katılır; birleşik varsa ya da hiçbiri yoksa birleşik (kural 4);
   ikisi de varsa birleşik + uyarı (kurulum zaten bölünmüş, sihirbaza yönlendirir); Keeper okunamazsa
   v0.10.971 davranışı + yüksek sesli log. Gözlem tamsa Keeper'a hiç gidilmez.
2. **Argo keşfi "yetki yok":** hub'ın Thanos'u 401/403 dönerse `errorType: unauthorized` +
   `upstreamStatus` (HTTP 502 kalır; URL/token yankılanmaz); sekmede token/rol yenileme adımı ve
   "Yeniden ara", diğer hub'ın sonucu durur.
3. **PUT /api/settings/argocd bayat yazım koruması:** sekme GET'teki `updatedAt`'ı
   `expectedUpdatedAt` olarak geri yollar; sunucu taze okuyup uyuşmazlıkta 409 `stale` döner;
   alan yoksa kabul (API/token çağıranlar) ama audit "önkoşul yok" der; pod içi mutex
   (pod'lar arası son yazan kazanır — belgeli kalan risk). Sekmede "başka biri değiştirdi —
   yeniden yükle" kutusu; yeniden yükleme sunucuda değişmemiş alanlardaki düzenlemeleri korur.
4. **CorePanel:** `title=""` ile boş `<h3>` basılmaz; `ariaLabel` prop'u (role=group) — Trace pod
   grafikleri çift duyurmaz.

## 2026-09-27 — §11 sorgu paketi shell script değil admin probe (v0.10.979)

**Karar (operatör: "Önerin" → "1: §11 sorgu paketini çalıştırılabilir hâle getir"):** Rollouts v2
Faz 2/3'ü açan §11 canlı sorgu paketi (K/D/R/H/N/T) `POST/GET /api/admin/rollouts-v2/probe` olarak
Coremetry'nin içinde koşar; shell script ya da Grafana tarifi değil. Gerekçe: token operatörün
eline geçmez (Remote Cluster `tokenRef`, `WorkerQuery` fail-closed; konsol ailesi fail-closed
olmadığı için çözülmemiş ref koşudan önce reddedilir); okuyucular mevcut olanlar (`WorkerQuery`,
`ConsoleQuery` etiketi silinmiş kopyaya — matcher'sız geçiş çifti yalnız matcher'da ayrışsın diye
`ConsoleInstantQuery.Dedup` eklendi —, `ConsoleLabels/LabelValues`, chstore T örneklemi); her
DEĞER koşu başına bir jetonlayıcıdan geçer (küme, host, namespace, job, ad, proje, repo, suffix,
pod, deploy_env; bilinmeyen etiket varsayılan-red), eşleme yalnız bellekte, rapor 1 saat pod
belleğinde, hiçbir yere yazılmaz. Asenkron (202 + yoklama): ~330 çağrı ve `[24h]` sorguları
Route'un 30 s'sini aşar. Rapor V1–V14'ü (v2detect.go başlığı) confirmed/refuted/unknown ile
yargılar; A (§11.6) ve V (§11.7) "skipped (metrics-only)". Bilinen sınırlar: rapor pod-yerel
(çok replikada 404 `none` + `pod`), H6 `[24h]` bölünmesi yok, arayüz yok (API + runbook).

## 2026-09-27 — Rollouts v2 P2.2 canlı KSM dedektörü §11'den önce kodlandı, bayrak kapalı (v0.10.982)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 2'nin kodlanması):**
P2.2 (`internal/rollout/v2worker.go`, `v2fetch.go`, `v2thanos.go`, `worker_run.go`;
`internal/chstore/rollout_v2_store.go`) §11 K/D cevapları GELMEDEN yazıldı; V1–V14 doğrulanmadı.
Bu yüzden dedektör yalnız `system_settings["rollouts"]` **enabled=true VE source="v2"** iken koşar.
"rollout-detector" kilidi ve döngüsü bayrak İLK kez açılınca başlar (`WaitActive`, 30 s yoklama):
varsayılan kurulumda sorgu, CH okuması, koşu satırı, Redis anahtarı, kalp atışı ya da günlük satırı
yok. source=v2 P2.3'te okuma yolunu da çevireceği için bayrak §11 sonuçları + P2.3 ile birlikte
açılır. Doğrulanmamış varsayıma dayanan her yer fail-safe: yazmak yerine atla + koşu teşhisinde say.
- **Açmadan önce doğrulanacaklar:** K1.D/K1.S/K1.DS (V1: generation + observed_generation üç türde),
  K2.1/K2.3 (V2: `namespace`+tür etiketi, `owner_is_controller` değerleri — sorgu yalnız açık
  `"false"`'u dışlar), K1 (V2b STS revizyon serileri), K1.R/K3 (V3: RS owner/spec, 50k tavanı),
  K1/K6.4 (V4: `_created` yokluğu; varsa incarnation ondan), K0.5/K0.6 (V5: `timestamp()` iç
  toplamada scrape zamanı; V9 tazelik — dedektör `up`/ikinci KSM denetlemez, yerine toplu yokluk
  koruması), K0.4 (V6: `max by` + dedup=true sonrası tek seri), K2.3c (V7), K2.4b (V8 DS hash),
  K1.H/K5 (V10 yazım hacmi), D (V12: DC kapsam dışı), K2.2 (V14 imaj birleşimi; STS pod
  `controller-revision-hash` = `update_revision` adı). Ek: querier `max by (__name__, …)` ile
  metrik adını korumalı (§11 K1 biçimi; korumazsa bütün ölçüler `fetch_bad_sample`, satır yok) ve
  anlık sorguda `time=` parametresini kabul etmeli (dedektör tik sorgularını sabitler).
- **Tasarım kararları:** tik başına TEK `rollout_worker_runs` satırı (anahtar (worker, started_at,
  host) küme başına satırı çökertirdi; teşhis sayaçları `error` kolonunda, 0015 bayt-eşliği
  değişmez); hedefler etkin, URL'li **bütün** Remote Cluster'lar — Argo hub'ları DAHİL (karar 5 /
  §5.6: hub sıradan kayıttır, entity ve rollout işlemesi de alır; v1 reconciler da ayırmaz); bir
  kümenin bütün tik sorguları tek değerlendirme zamanında (tik − 15 s, `WorkerLimits.Time`) — aksi
  hâlde ardışık sorgular farklı scrape okuyup yeni RS'yi nesil artışından önce görebilir; lider
  belleği tikler arasında taşınır, edinim / liderlik kaybı / tik sırasında yeniden edinim /
  bayrak kapanışı / yazım hatası onu düşürür ve durum CH'den (FINAL keyset) kurulur —
  `V2Memory.Absent` DDL'de olmadığından boş başlar (çekirdek sözleşmesi). İmajlar açık olaylı,
  bekleyen, nesli değişmiş ya da ilk kez görülen iş yükleri için (≤100 iş yükü, ≤20 sorgu) +
  artan bütçeyle imajı boş durumlar (bootstrap baseline'ı; ≤20/tik; okuması boş dönen aday —
  ör. STS'de V8 etiketi yok — 2ⁿ tik, en çok 64, geri çekilir ki sonraki adaylar aç kalmasın);
  imaj okunamazsa fark atlanmaz, change_type çekirdeğin kuralıyla `rollout` kalır ve açık olayda
  sonradan düzelir.
- **`lockDegraded` (§10.3.1 mint öncesi okuma):** dedektör atlamaz (v1 gibi, §10.4) ama bu sürede
  durumu HER tik CH'den tazeler (yokluk sayaçları yereldir, korunur). Tek yazıcıda bellek CH'nin
  kendi yazdığımız görüntüsüdür; iki pod aynı yeni iş yüküne aynı tikte farklı incarnation basarsa
  sonraki tik ikisi de en yeni incarnation'a yakınsar (lost-state kuralı), eskisinin açık olayı
  kapanır — kalan risk yeni iş yükü başına en çok bir yinelenen `initial` satırı (`host`
  kolonuyla görünür). Maliyet yalnız degraded'da: pod × küme × tik başına iki keyset okuması.
  Redis reprobe'u gerçek kilidi takınca bayrak LeaderTTL(1 dk) = 3 dk daha doğru kalır
  (`cache.DegradedFlag`): holder'lar takası bir kalp atışı (≤60 s) sonra fark eder, diğer
  pod'lar kendi reprobe'larına dek Noop'ta liderdir — çok yazıcılı pencere bu kadar sürer.
- **Toplu yokluk koruması (V9 fail-safe, SÜRE SINIRLI):** taban = son kabul edilen tam okumada
  görünen iş yükleri; ailesi büsbütün yok okunan türde (çekirdek family_absent) taban düşmez —
  sonraki kısmi geri dönüş de korumayı tetikler. Yeniden kurulumdan sonraki ilk okumada taban
  last_seen_at'i son 25 sa (v2TouchEvery + 1 sa) içindeki durumlar, güncel incarnation'ının son
  olayı "gone" ile superseded kapanmış olanlar hariç; son 25 sa içinde açık olaysız silinenler yine
  sayılır (hata güvenli yönde: kapanış ≤30 dk gecikir, `mass_absence_rebuilt_base` teşhisi —
  tabanı ilk okumadan tohumlamak kısmi okumayla açılan yeniden kurulumda korumayı kapatırdı). Bir türde
  tabanın ≥20 iş yükünün yarıdan azı görünürse o türün YOKLUK işlemesi en çok 30 dk dondurulur
  (gone kapanışı ve yokluk sayacı geri alınır, koşu partial); görünen iş yükleri normal işlenir
  (START/SUCCEEDED gecikmez). 30 dk'yı aşan V9 ihlali yine sahte gone + dönüşte yeni incarnation
  üretir (bilinen sınır); süre dolunca taban sıfırlanır, sonraki düşüş korumayı yeniden tetikler.
- **P2.1 çekirdek düzeltmesi:** artışsız görülen yeni revizyon (`evidence_without_bump`) artık
  `known_revisions`'a katılmaz — katılırsa artış geldiği tik sahte ROLLBACK yazılır ve önceki
  başarılı rollout `rolled_back` olurdu (sorgu kayması ya da KSM informer kayması).

## 2026-09-27 — Rollouts v2 P3.1 argocd-metrics işçisi §11'den önce kodlandı, bayrak kapalı (v0.10.983)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 3.1'in kodlanması;
"Argo CD: şimdilik yalnız metrik" kararıyla):** P3.1 (`internal/argocd/app_status.go`,
`metrics_worker.go`, `metrics_thanos.go`; `internal/chstore/argocd_status_store.go`) §11 H/N
cevapları GELMEDEN yazıldı. İşçi yalnız `system_settings["argocd"]` **enabled=true VE
metricsWorker.enabled=true VE ≥1 hub** iken koşar; "argocd-metrics" kilidi ve döngüsü bayrak İLK
kez açılınca başlar (`WaitActive`): varsayılan kurulumda hub sorgusu, CH okuması, koşu satırı ya da
Redis anahtarı yok. **Ayrı bayrak (inceleme):** üst düzey `enabled` P1'den beri "yalnız bayrağı
kaydeder" diye sunuldu; onu işçi anahtarı yapmak, P1/P2'de açmış kurulumlarda işçiyi deploy'la
(admin eylemi olmadan) başlatırdı — `metricsWorker.enabled` eski bloblarda yok = kapalı; Validate
entegrasyon kapalıyken onu da kapatır. Argo CD API'sine bağlanılmaz (P3.3 askıda). Ayarlar › Argo
CD'de entegrasyon kutusunun altında ayrı "Metrik işçisini çalıştır (argocd-metrics)" kutusu (kayıt
aynı admin PUT'u: audit + 409 bayat koruması). Doğrulanmamış varsayıma dayanan her yer fail-safe:
yazmak yerine atla + koşu teşhisinde say.
- **Açmadan önce doğrulanacaklar (§11 H):** H0.3 vs H0.5 her hub'da (küme etiketi Argo
  serilerinde var mı → `injectClusterLabel`; yanlışsa envanter 0 seri döner — boş envanter VERİ
  YOK sayılır: taban alınmaz, yokluk sayılmaz, `inventory_empty`; etiket düzeltilince okuma kapsamı
  değişir ve ilk dolu envanter bütün uygulamaları `baseline` yazar); `up` serisi aynı seçiciyle
  (namespace [+ job], aynı küme etiketi kararı) okunabilmeli ve `argocd_app_info` ile aynı `job`
  etiketini taşımalı — hedef sağlığı yalnız uygulama serisi üreten job'ların hedeflerinde sayılır
  (`up{sel} and on (job) group by (job) (argocd_app_info{sel})`; ikinci inceleme: job'suz seçicide
  ilgisiz kalıcı `up==0` hedefi — dex, redis-exporter — silmeyi sonsuza dek bekletiyordu); yokluk
  (`deleted`) yalnız bu hedefler sağlamken işlenir (hedef yok / `up==0` / sağlam hedef sayısı
  azaldı → o envanterde sayılmaz); `up` bulunamazsa silme HİÇ yazılmaz; bekletme 3 ardışık
  envanteri bulursa parça partial + not (`absence_held_streak`); H0.2/H0.4 (HA/shard kopyaları `max by`
  + dedup=true sonrası tek seri — aynı anahtarda iki farklı durum görülürse uygulama o tik
  `app_ambiguous`, satır yok); H1.2 (§5.2 durumu: A/C'de parça seçicisi `namespace=<instance ns>`
  doğru; B + apps-in-any-namespace KAPSANMAZ — eksik kapsama, yanlış satır değil); H1.1/H1.3
  (`metricsJob` tanımlıysa seçiciye eklenir; aynı job birden çok instance'ta sorun değil,
  namespace ayırır); H1.6 (parça başına uygulama ≤ `reader.maxSeries`; aşarsa parça her tik
  `shard_truncated` ile atlanır — dest_server alt parçalaması yok); H4 (`operation` etiketi ve
  değerleri; sabit olmayan küme = Synced+Healthy+işlem yok DIŞI; `autosync_enabled` yoksa '');
  H5 (`argocd_app_sync_total` da `exported_namespace` taşımalı — join anahtarı §5.3); H6
  (senkron hacmi ve `resets()`; controller yeniden başlayınca eski değere EŞİT yeni değer
  sıfırlanma olarak görülmez → o senkron kaçar, bilinen sınır); §5.1 `dry_run` (v3.1+) senkron
  seçicilerinde `dry_run!="true"` ile dışarıda (etiketsiz eski sürüm serileri de eşleşir). Ek:
  querier `changes()`, `offset`, `unless on (…)` ve `count_values` içeren anlık sorguyu `time=` ile
  kabul etmeli; okuma kapsamı ÖRTÜŞEN parçaların HEPSİ atlanır (birini okumak diğer hub'ın
  uygulamalarını yanlış instance/cluster_id altında yazardı): aynı Thanos + aynı namespace iki parça,
  ikisinde de dolu ve farklı `metricsJob` ya da ikisinde de aynı adlı farklı değerli enjekte küme
  etiketi yoksa çakışır (ikinci inceleme: job'suz seçici job'lunun, etiketsiz okuma etiketlinin üst
  kümesidir — yalnız birine job/etiket vermek ayırmaz).
- **Tasarım kararları:** parça = hub üzerindeki etkin instance (§5.4); ilk tik ve her
  `intervals.inventoryMin` tam envanter + tam sayaç tabanı, aradaki tikler (`intervals.metricsS`)
  yalnız sabit olmayan küme + senkron penceresi (değişen ya da yeni doğan sayaç, pencere
  `2×aralık…5 dk`) + sabite dönen / senkronu görülen ama gözlenmeyen uygulamalar için ada göre
  hedefli okuma (≤10×50 ad). Parçanın bütün sorguları tek değerlendirme zamanında (tik − 15 s);
  herhangi bir hata / kesik / uyarılı okuma parçanın farkını atlatır, bellek aynen. Satır yalnız
  tuple değişince ya da senkron bitince; uygulama başına tik başına TEK satır (senkron + değişim →
  tek `sync`); aynı aralıkta ikinci satır önceki satırın 1 ms sonrasına kayar (RMT anahtarı ezmesin).
  İlk koşu ve yeniden kurulumdan sonraki ilk tam envanterde bilinmeyen uygulama `baseline` (yeni mi,
  TTL'i dolmuş mu ayırt edilemez), sonrası `appeared`; taban envanterinde belirsiz kalan uygulama
  ilk gözlemde yine `baseline`; iki ardışık tam envanterde (hedefler sağlamken) yoksa `deleted`;
  bilinen uygulamaların yarıdan fazlası (≥20) birden yoksa silme en çok 30 dk dondurulur, HEPSİ
  birden yoksa (sayıdan bağımsız) hiç kabul edilmez (bilinen sınır: gerçekten tüm uygulamaları
  silinen instance'a `deleted` yazılmaz). Okuma kapsamı (hub, Thanos URL'si, etkin küme etiketi,
  seçici) değişirse envanter/sayaç/hedef tabanı yeniden alınır ve farklı tuple da `baseline`
  yazılır. Son satırı 150 günden eski değişmemiş uygulama tam envanterde `baseline` ile tazelenir
  (180 günlük TTL tek satırı düşürmesin). Başarısız/kesik envanter 2ⁿ × aralık (tavan
  inventoryMin) geri çekilir, arada ucuz okuma. Yeniden kurulum son satırı `deleted` olanları
  almaz (churn'lü instance 200k tavanına silinmişlerle dayanmasın); canlı lider de silinen
  uygulamayı bir tik sonra belleğinden düşürür (geri dönen yine `appeared`). Sayaç
  tabanı yokken yeni seri SAYILMAZ; pencerenin kaçırdığı artış envanterde `sync_missed` sayılır ama
  YAZILMAZ (zamanı bilinmeyen senkron satırı §7.5 penceresini yanıltırdı); tuple'ı bilinmeyen
  uygulamanın senkronu ertelenir. Lider edinimi / yazım hatası / bayrak kapanışı belleği düşürür,
  durum `argocd_app_status`'tan (FINAL, uygulama başına LIMIT 1 BY, keyset) kurulur; lockDegraded'da
  her tik yeniden okunur. Tik başına TEK `rollout_worker_runs` satırı (worker=argocd-metrics;
  `unmapped` = cluster_id'si boş yazılan satır). `thanos.WorkerLimits.NoClusterLabel` hub'ın
  injectClusterLabel=false kararını işçi yoluna taşır. Eşleyici (P3.2) ve metrics-only
  sınıflandırma (P3.4, §7.5) bu işçide değil. `/api/services/{name}/gitops` (bayraksız) bu
  sürümde v0.10.981 küme-içi kuralını korur (yalnız TAM `https://kubernetes.default.svc`);
  işçinin geniş kuralı (`:443`, sondaki `/`, `.cluster.local`) eşleyiciyle (P3.2) gelir.

## 2026-09-27 — Rollouts v2 P2.3 okuma yolu §11'den önce kodlandı, bayrak kapalı (v0.10.984)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 2.3'ün kodlanması):**
P2.3 (`internal/api/rollouts_v2_read.go`, `internal/chstore/rollout_v2_read.go`,
`internal/rollout/v2read.go`; FE `lib/rolloutRow.ts`, `lib/serviceRolloutLabel.ts`, `pages/Rollouts.tsx`)
§11 K/D cevapları GELMEDEN yazıldı. Okuma yolu yalnız `system_settings["rollouts"]` **enabled=true VE
source="v2"** iken rollout_events'e döner — P2.2 dedektörünün koşma koşuluyla aynı bayrak; varsayılan v1'de
cevaplar bayt bayt bugünkü (handler testleri v1 gövdesini ve "v2 okuması çağrılmadı"yı pinler; değişen
yalnız önbellek anahtarlarının içi). Açma yolu arayüzsüz: `GET /api/settings/rollouts` → dönen
`settings` içinde `source:"v2"` → tam blobu `PUT` (audit `rollouts.settings.update`); geri dönüş `"v1"`.
P2.3 yazmaz; doğrulanmamış varsayımın bedeli yanlış GÖSTERİMDİR, yanlış satır değil.
- **Açmadan önce doğrulanacaklar:** P2.2 kaydındaki V1–V14 listesinin tamamı (gösterilen satırları o
  dedektör yazar) + `0015` sihirbazla uygulanmış olmalı (§2.5; tablo yoksa liste boş + dedektör notu,
  tail her tik hata loglar) + §10.6 "measure first": tail her 15 s (dinleyici varken) rollout_events
  FINAL taraması yapar — bölümsüz tabloda v1 workload_rollouts tail'iyle aynı sınıf; prod'da ilk hafta
  `system.query_log`'dan süre/okunan satır ölçülür + V14 (`kube_pod_container_info` `image` değeri
  `repo:tag`, `repo@digest` ya da `repo:tag@digest` — sonuncuda tag okunur; `SplitImageRef` bunu varsayar,
  başka biçimde imaj sütunu ham kalır) + sidecar'lı iş yükünde birincil imaj seçiminin (`PrimaryImagePair`:
  değişen repo > iş yükü adı > ilk) prod imaj adlarıyla tuttuğu +
  STS/DS `new_revision` doluluğu (boşsa Revizyon "—", kimlik yine 6 parça).
- **Tasarım kararları:** yeni rota YOK — FE kaynağı göremez (ayar ucu admin), cevap şekli aynı; liste ve
  istatistik gövdesine eklemeli `v2: true`, satıra eklemeli v2 alanları (`generation`, `incarnationAt`,
  `changeType`, `v2Status`, replikalar, tam imaj listeleri, `stuckReason`, …; `RolloutRow.V2` nil → anahtar
  yok). Durum v1 sözlüğünde (`progressing→in_progress`, `succeeded→completed`, `stuck→stalled`,
  `rolled_back`/`superseded` aynen; `?status=` sunucuda ters çevrilir, tablo testi `v2read_test.go`).
  Bağlantılar (karar 14): kodek parça sayısıyla ayırır — 6 parça (`cluster|namespace|kind|workload|
  incarnationAtMs|generation`) rollout_events, 5 parça eski bağlantı v2 açıkken de workload_rollouts'tan
  (TTL'e dek); v1'de 6 parçalı istek bugünkü 400. v2 kolon seti ayrı `storageKey` (`rollouts-live-v2`):
  "Kaynak" (detectedBy — v2'de hep ksm) ve Span düşer (karar 4; P3.4'ün tetikleyici kolonu "Kaynak"
  adını yeni kimlikle alır), Nesil + güncel/hazır/istenen replika gelir. Problem rozeti ve çekmecenin
  servisleri v2'de İŞ YÜKÜ düzeyinde (`RolloutWorkloadServicesBatch`, revizyonsuz, başlangıç − 1 sa):
  MV'nin STS/DS revizyonu imaj tag'i, v2'ninki controller revision — revizyonla eşleşmez. İstatistik
  süresi yalnız succeeded (`succeeded_at − started_at`); `initial` olaylar toplamda sayılır.
  `/api/rollouts/runs` ve boş liste notu v2'de `rollout_worker_runs` (worker="rollout-detector")
  okur (P2.2 yalnız yazıcıyı getirmişti). SSE tail kaynak başına ayrı kursör, kaynak değişince sıfırlanır.
  GitOps sekmesi (v0.10.981) aynı anahtarla rollout_events'ten. Her önbellek anahtarı kaynağı taşır.
- **§2.2 sapması (servis kapsamlı okuma):** audit "yeni rota, kendi dosyasında" diyordu; bunun yerine
  `/api/services/{name}/rollouts` sunucuda kaynağı seçer (`serviceRolloutsFor`): DeployHistoryPanel,
  sürüm çipi, ServiceCharts işaretleri, annotation şeridi ve CoSRE grafik anlatımı tek yardımcıdan
  beslenir, FE'de "hangi uç" dalı yok; pod-churn yolu P6'da emekli olur. KSM satırı pod-churn şeklinde
  `source:"ksm"` + iş yükü + durum taşır; `config` değişikliği `restart` sayılır (sürüm çipi ve deploy
  işaretleri saymaz), `podsRemoved`=0 (KSM emekli pod saymaz) — FE etiketleri `serviceRolloutLabel.ts`
  ("0 pods replaced" basılmaz). İş yükleri servisin son 7 günde span ürettikleri (MV): span'i olmayan
  iş yükünün rollout'u /rollouts'ta görünür, servis sayfasında görünmez (bilinen sınır, §10.6).
- **Ad:** «Deployment/Rollouts» — nav EN/TR, ⌘K etiketi + "deployments" takma adı, Topbar, kapalı
  durumu (metin artık olmayan "Settings → Rollouts" yerine sayfadaki «Etkinleştir»i söyler, §2.6 madde 5
  FE yarısı); `pageContext` `/deployment-report` → `rollouts` (ölü `deployment-report` PageId çıktı).
- **İnceleme düzeltmeleri:** v1 imaj/sürüm alanı sıralı listenin ilki değil `PrimaryImagePair` (istio
  sidecar'ı önde sıralanıp "1.20.1 → 1.20.1" + `VersionConstant` gösteriyordu); dedektör `updated_at`'i
  tik başı değil küme YAZIM anı basar (`v2StampWrite`; tik başı 15 s tail watermark'ını geçince keyset
  kursör satırı hiç görmezdi — v1 de yazım anını basar); tail kursörü yalnız başarıda ilerler
  (`rolloutTailPages`; v1'de de önceki anlam); v2 STS/DS "Traces →" süzgeci controller revizyonu değil
  `imageTag`, revizyonsuz Deployment deployment adıyla; DeployHistoryPanel KSM satırını zaman + iş yüküyle
  anahtarlar (aynı scrape zamanı).

## 2026-09-27 — Rollouts v2 P3.2 Argo uygulaması ↔ iş yükü eşleyicisi §11'den önce kodlandı, bayrak kapalı (v0.10.985)

**Karar (operatör: "devam et … bitir işleri" — §11 probe'u henüz koşulmadan Faz 3.2'nin kodlanması;
"Argo CD: şimdilik yalnız metrik" kararıyla):** P3.2 (`internal/argocd/mapping.go` saf çekirdek,
`mapper.go` işçi adımı; `internal/chstore/argocd_mapping_store.go`; `internal/api/service_gitops_mapper.go`)
§11 H/N/T cevapları GELMEDEN yazıldı. Eşleyici ayrı bayrak AÇMAZ: argocd-metrics işçisinin içinde koşar
(§10.4, lider "argocd-metrics"), yani yalnız **enabled=true VE metricsWorker.enabled=true VE ≥1 hub**
iken; varsayılan kurulumda sorgu, CH okuması ya da yazımı yok. GitOps sekmesi (`/api/services/{name}/gitops`)
yalnız aynı bayrak açıkken, kapsam kapısı açıkken (aşağıda, inceleme) VE servisin (cluster, ns)
çiftlerinde taze kenar varken tablodan okur; aksi hâlde v0.10.981 canlı yolu aynen (+ eklemeli `argo.source: "live"`). Resource (kesin) kenarı yalnız Argo
CD API'sinden gelir (P3.3, askıda) — yazılmaz; pod_label (§11 K2.4) yazılmaz.
- **Açmadan önce doğrulanacaklar:** P3.1 kaydındaki §11 H listesi (eşleyici o işçinin belleğini okur) +
  §5.3 dest_server biçimleri: Remote Cluster `apiServerUrls` Argo cluster Secret'ındaki yazımla
  normalleşince eşleşmeli (v2.x ham `spec.destination.server`, v3 çözülmüş; `""` = `destination.name`) —
  çözülmeyen uygulama ad kenarını HER kümeye alır (canlı yolla aynı kural), zayıf kenar almaz
  (`mapper_dest_unresolved`); §11 N5/N6 (suffix → küme tekilliği, `mapper_suffix_mismatch` ölçer —
  eşlemeyi değiştirmez) ve N1–N4 (ad ayrışma oranı `mapper_name_parsed/unparsed`, bileşen tam eşleşmesi
  `mapper_name_component_exact` vs `mapper_name_part_only`); §11 T / §9: iş yükü kimliği span'lerin k8s
  deployment/statefulset/daemonset adından (MV) ve span cluster değeri Remote Cluster'a eşlenmeli
  (`mapper_workload_cluster_unmapped`); `0015` sihirbazla uygulanmış olmalı (argocd_app_mapping); §10.6
  "measure first": filo iş yükü okuması (workload_revision_activity_1m, son 24 sa, GROUP BY, ≤200k satır,
  25 s) her mapperMin'de bir — prod'da ilk hafta `system.query_log`'dan süre/okunan satır ölçülür.
- **Tasarım kararları:** kural fonksiyonları canlı matcher'la ORTAK (`pinnedWorkloads`, `pinMatchesApp`,
  `nameEdgeMatches`; `TestMapperMatchesLiveMatcher` iki yolun aynı listeyi verdiğini pinler): manual (100)
  pinden, pinli (cluster, ns, workload) ad tahmini almaz, servis kartında manual kenarlı uygulamanın o
  servisteki ad kenarları düşer; name (mapping.nameConfidence) tire sınırlı ad parçası + dest_namespace +
  çözülmüşse küme; namespace (zayıf, mapping.namespaceConfidence) dest_server'ı çözülen her uygulama,
  candidates = o (cluster, ns)'e deploy eden uygulama sayısı — kartta yalnız "aynı namespace'te
  eşleşmeyen" sayısı. Kenar türü taşır (manual → pinin türü, name → MV `anyLast(workload_kind)`); türü boş
  iş yükü atlanır. Ad ayrıştırma (§6: envList + argoSuffix birleşimi, QuoteMeta, sona çapalı) kenarı
  DEĞİŞTİRMEZ, yalnız teşhis; base key / pairGroup çift uyarısı P3.5. Tur yalnız HAZIR instance'ları
  uzlaştırır (parça atlanmadı, bellek kurulu, son 3 × inventoryMin içinde dolu tam envanter); hazır
  olmayanın kenarı ne eklenir ne kaldırılır. Uzlaştırma tam satır: yeni kenar first_matched_at=şimdi,
  değişen ya da 20 saatten eski kenar first_matched_at taşınarak yeniden yazılır, istenmeyen canlı kenar
  removed_at=şimdi (bütün alanlar taşınır); okuyucu last_verified_at'i 26 saatten eski kenarı canlı
  saymaz (kopmuş hub / kaldırılmış instance kenarı 30 gün TTL'de kalsa da sekmeye gelmez). Fail-safe:
  iş yükü listesi tavanda kesikse, kenar sayısı 400k'yı aşarsa ya da okuma düşerse tur atlanır (yazım
  yok, koşu partial + "eşleyici: …"); hatalı tur da sonrakini mapperMin sonraya atar. Tur her mapperMin
  (10 dk), ayar kaydı ya da Remote Cluster kaydı değişince hemen. Koşu satırı: mapping satırları
  `rows_written`'a, iş yükü kenarı almayan uygulamalar `unmapped`'a eklenir (o tikte durum satırlarının
  cluster_id'siz sayısıyla toplanır; ayrım `mapper_*` teşhisinde). Sekmenin eşleyici yolu: son durum
  argocd_app_status'tan (son satırı 'deleted' ya da hiç satırı olmayan uygulama gösterilmez, not düşülür),
  "Senkron (24 sa)" metrikle görülen 'sync' satırlarının sayısı — işçi tik başına tek satır yazar ve sayaç
  tabanı yokken görülen artışı yazmaz, yani ALT SINIR (canlı yolun `increase()`'inden düşük olabilir);
  hub satırları sorgu sonucu değil kayıt durumu (kayıt yok/devre dışı ya da tokenRef çözülmüyorsa
  "skipped"). Önbellek anahtarı kaynak kararının girdisini (`mw`) açıkça taşır.
- **İnceleme (v0.10.985) — kapsam kapısı:** kenar tazeliği işçinin canlı olduğunu SÖYLEMEZ (değişmeyen
  kenar 20 sa'de bir dokunulur; hub Thanos'u düşen, parçası atlanan ya da envanteri eskiyen instance'ın
  kenarı 26 sa taze kalır, argocd_app_status yalnız değişimde yazıldığından donmuş durum "güncel"
  okunurdu, hub satırı "ok" derdi). Sekme tabloyu yalnız şu hâlde kullanır, aksi hâlde canlı yol +
  `argo.note`'ta sebep: hub'lardaki HER instance etkin ve her hub'ın ≥1 instance'ı var; son
  argocd-metrics koşusu (rollout_worker_runs, FINAL, 1 gün, LIMIT 1) var, `ok`, `started_at` ≤
  `argocd.MetricsRunFreshness` (2 × metricsS + tik bütçesi; varsayılan 7 dk) yaşında, son ayar kaydından
  sonra başlamış ve `scopes_total` ayardaki etkin instance sayısına eşit. İşçi, parçası tamam olduğu
  hâlde eşleyiciye hazır olmayan instance için her tik koşuyu partial yapar (`mapper_unready_instances`),
  yani `ok` = her instance okundu VE hazır. Başarısız eşleyici turu (okuma hatası/zaman aşımı, 200k iş
  yükü ya da 400k kenar tavanı, yazım hatası) da başarılı tura dek tur arası HER tiki partial yapar
  (`mapper_last_round_failed`; yalnız hatalı tik partial olsaydı 60 s tik / 10 dk turda koşuların ~%90'ı
  `ok` yazar, kapı son koşuya bakıp ≤26 sa bayat kenarla açılırdı). Kapı geçici bir Thanos uyarısında da kapanır (bir tik canlı
  yol) — bilerek: yanlış "güncel" yerine eski davranış. Devre dışı instance'ın kenarı kartta da atlanır.
  Servis kartı kenarları iki okuma, ayrı bütçe: iş yükü kenarları yalnız servisin (cluster, ns,
  workload) üçlüleri için, zayıf kenarlar ayrıca (ORDER BY'da önce geldikleri için ortak LIMIT'i doldurup
  sonraki kümelerin eşleşen kenarlarını kesiyorlardı); zayıf kesikte yalnız sayı alt sınır. "Senkron
  (24 sa)": lockDegraded'da iki pod'un bir tik arayla yazdığı aynı senkron, önceki 'sync' satırıyla aynı
  fazda ve ≤ metricsS sonraki satır sayılmayarak katlanır (lagInFrame; bitişik tikteki gerçek ikinci
  senkron da katlanır — sütun alt sınır kalır). Açmadan önce ayrıca doğrulanacak: prod'da argocd-metrics
  koşularının çoğunun `ok` olduğu (yoksa sekme hep canlı yolda kalır; sebep notta).
- **Bilinçli farklar (sekme, eşleyici yolu):** Ayarlar'da TANIMLI OLMAYAN bir Argo CD'nin (hub'da koşan,
  instance olarak eklenmemiş) uygulamaları tabloda yoktur — canlı sorgu hub'daki her argocd_app_info
  serisini görür (instance'ı çözülmeyen uygulamayı da gösterir); kapı bunu hub'ı sorgulamadan bilemez
  (inceleme). Küme-içi adres geniş kuralla (`:443`, sondaki `/`,
  `.cluster.local`) instance'ın kendi hub'ına çözülür (canlı yol bayraksız olduğu için TAM yazımda kalır —
  v0.10.983 inceleme); "aynı namespace'te eşleşmeyen" küme kapsamlı ve dest_server'ı çözülmeyen eşleşmemiş
  uygulamayı saymaz; canlı yolun topk(50) kesiği yok (tam envanter). İki hub aynı iş yükünü yönetirse iki
  kenar, iki satır (anahtar instance_id taşır; §5.6).
- **Kapsam dışı / sonraya:** iş yükü kaynağı yalnız span MV'si — KSM'de görülüp span üretmeyen iş yükü
  (rollout_workload_state) kenar almaz; P3.4 sınıflandırması rollout_events anahtarıyla birleşeceği için
  o birleşim P3.4'te eklenir. Pin düzenleyicisi yok (pinler API'den); Ayarlar › Argo CD metinleri artık
  "Faz 3" demiyor, pinlerin ne zaman kullanıldığını ve instance_id'nin durum + eşleme satırlarına bağlı
  olduğunu söylüyor.
